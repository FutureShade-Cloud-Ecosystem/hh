// Package purchasing is the Wave 4 buy-side module: purchase orders with a real lifecycle, line-level
// receiving, AP vendor bills with a three-way match, and the landed-cost tie-in.
//
// - WHAT IT OWNS, AND WHAT IT REFUSES TO OWN -
//
// It owns the DOCUMENTS: purchasing_pos, purchasing_po_lines, purchasing_receipts, purchasing_receipt_lines,
// purchasing_bills, purchasing_bill_lines, purchasing_bill_instalments, purchasing_match_tolerance
// (migrations 0120-0121), plus the branch-scoped PO numbering (next_po_no).
//
// It owns NO stock arithmetic. Not one stock_move, not one balance row, not one weighted average. Receiving
// writes a RECEIVING DOCUMENT, a 0011 purchase_orders row whose lines carry exactly the quantities landing
// now, and hands its id to the StockReceiver seam, which internal/inventory satisfies with the write path it
// already has. If that seam is not wired, receiving REFUSES (ErrStockSeamUnwired) rather than recording an
// arrival that moved nothing. That refusal is a named test, and it is the load-bearing property of this
// module: there is exactly one place in the program where stock arithmetic happens, and it is not here.
//
// It owns no vendor data either (VendorDirectory seam), no landed-cost capitalization (LandedCoster seam,
// satisfied by internal/landedcost's existing Create+Apply path, purchasing never touches a weighted
// average), and no journal building beyond deciding the four legs (GLPoster + RuleResolver seams over
// internal/gl, every account resolved through a posting rule and never a literal code).
//
// - THE FLOW -
//
//	create PO (DRAFT, #PO-TRT-n)  →  order it (ORDERED, line set frozen)
//	  →  receive some (PARTIAL; RECEIPT moves + WAC + GRNI, all inventory's)
//	  →  optionally attach the freight invoice to that receipt (landedcost capitalizes it)
//	  →  receive the rest (RECEIVED)
//	  →  enter the vendor bill  →  three-way match  →  post (DR 2150/2200/5250, CR 2100)
package purchasing

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - consumer-declared seams (main.go wires the adapters) -

// StockReceiver is the ONLY way this module may cause stock to move.
//
// ReceiveDocumentTx is handed the id of a receiving document, a purchase_orders row plus po_lines rows whose
// quantities are exactly what is landing now, and, INSIDE THE CALLER'S TRANSACTION, converts it into stock:
// the RECEIPT moves, the weighted-average cost maintenance, the balance projection, and the po.received event
// that posts GRNI. Every one of those is inventory's, and none of them is reimplemented here.
//
// Why a document id and not a list of lines: it is the shape internal/inventory's existing exported write
// path (ReceivePOTx) already takes, so this seam is satisfied by a one-line adapter over shipped code rather
// than by widening another module's surface. See migration 0120 for the full argument.
type StockReceiver interface {
	ReceiveDocumentTx(ctx context.Context, tx pgx.Tx, receivingDocID string) error
}

// VendorDirectory is the read-only vendor seam. Purchasing validates that a PO names a real, ACTIVE vendor
// and renders vendor names; it owns no vendor table and writes nothing about a vendor.
type VendorDirectory interface {
	ListVendors(ctx context.Context) ([]Vendor, error)
	Vendor(ctx context.Context, vendorID string) (Vendor, error)
}

// BranchDirectory is the consumer-declared seam for "who is buying, and where does the truck go".
//
// A purchase order is a document with a SHIP-TO on it, and the dealer's own name and address live in
// `locations` + `system_settings`, neither of which purchasing owns or may read. It is a separate
// interface rather than a fifth method on VendorDirectory for the reason module.go already gives
// twice: a consumer needing a different fact asks through its own interface, so an outage in one
// answer cannot take the other down with it.
//
// Nil is honest and degrades DOWNWARD: the document still renders, with the branch id on the ship-to
// line instead of an address. A purchase order nobody can print because a name lookup blinked is a
// buyer back on a spreadsheet.
type BranchDirectory interface {
	Branch(ctx context.Context, branchID string) (BranchBrief, error)
}

// BranchBrief is the by-value projection the document prints. Assembled by the composition-root
// adapter so purchasing never learns what shape `locations.address` is stored in.
type BranchBrief struct {
	OrgName string
	Name    string
	Code    string
	Address string
	Phone   string
	Email   string
}

// LandedCoster is the landed-cost tie-in: capture a freight/duty/handling document against a receipt and
// apply it. Satisfied by internal/landedcost's EXISTING Create + Apply path, purchasing adds no
// capitalization logic and never touches a weighted average.
type LandedCoster interface {
	CreateAndApply(ctx context.Context, in LandedCostDraft) (landedCostID string, err error)
}

// LandedCostDraft is the by-value request the LandedCoster seam takes, so purchasing does not import
// landedcost's types.
type LandedCostDraft struct {
	// POID is the RECEIVING DOCUMENT's id, not the purchase order's: a freight invoice covers one truckload,
	// so it must apportion over the lines of one receipt.
	POID          string
	PORef         string
	BranchID      string
	Currency      string
	FXRate        float64
	FreightCents  int64
	DutyCents     int64
	HandlingCents int64
	Basis         string
}

// GLPoster posts a balanced journal through the GL provider (main.go adapter over *gl.Service.PostExternal).
// PostJournalTx is the same post through the caller's transaction (adapter over *gl.Service.PostExternalTx):
// PostBill holds the bill's transaction while it posts, so it takes this one, and the journal commits or
// rolls back with the bill instead of asking the shared pool for a second connection.
type GLPoster interface {
	PostJournal(ctx context.Context, in GLJournal) (string, error)
	PostJournalTx(ctx context.Context, tx pgx.Tx, in GLJournal) (string, error)
}

// RuleResolver resolves a posting-rule role to an account code (main.go adapter over *gl.Service.ResolveRule).
// ResolveRuleTx reads the rule through the caller's transaction (adapter over *gl.Service.ResolveRuleTx).
type RuleResolver interface {
	ResolveRule(ctx context.Context, eventType, lineRole, branchID string) (string, error)
	ResolveRuleTx(ctx context.Context, tx pgx.Tx, eventType, lineRole, branchID string) (string, error)
}

// WarrantyCreditReader provides access to vendor warranty credits.
type WarrantyCreditReader interface {
	OpenCreditForVendor(ctx context.Context, vendorID, branchID string) (*OpenWarrantyCredit, error)
	ClaimInfo(ctx context.Context, claimID string) (*OpenWarrantyCredit, error)
}

// GLJournal + GLLine mirror gl.ExternalJournal by value so purchasing does not import gl's internals.
type GLJournal struct {
	Source         string
	SourceRefID    string
	IdempotencyKey string
	Memo           string
	BranchID       string
	Lines          []GLLine
	// DocumentDate, when set, is the document the journal belongs to (a vendor bill's own
	// bill_date): the journal posts in the bill's month under the GL's document date rule, filing
	// late into the earliest open month at or after it when that month is closed, so the input tax
	// credit the tax tie-out reads by bill_date and the 2200 debit land on the same month. Zero
	// keeps the clock.
	//
	// A document date is a CALENDAR DAY in the dealer's own zone (houseday), the instant that day's
	// house midnight begins, never midnight at Greenwich: a Greenwich midnight is the previous
	// evening in the house zone, so a bill dated the first of a month would file into the month
	// before it.
	DocumentDate time.Time
}

// GLLine is one leg of a GLJournal.
type GLLine struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

// - repository -

// Repository is purchasing's pgx access layer (nil pool tolerated -> errNoDB everywhere).
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil offline).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// - service -

// Service is the purchasing domain seam. Sibling modules are injected via the With* options; every one of
// them is optional at construction and every one of them has a named, honest refusal when absent.
type Service struct {
	repo   *Repository
	stock  StockReceiver
	vendor VendorDirectory
	landed LandedCoster
	gl     GLPoster
	rules  RuleResolver
	// apPayments supplies the settlement side of the AP subledger read (ap_subledger.go), satisfied by
	// the composition over the GL; nil keeps OpenAPCentsAsOf refusing closed.
	apPayments APPaymentsReader
	audit  audit.Recorder
	log    *slog.Logger
	// callerFrom resolves the trusted, server-stamped acting staff member every branch-scoped read/mutation
	// gates against. Mirrors landedcost.Service.callerFrom.
	callerFrom func(context.Context) (identity.StaffContext, bool)
	// incomingSeam is the DIB-460 read seam (incoming.go). nil -> *Repository, which is what every real
	// deployment takes; a unit test injects a fake that RECORDS the BranchReach it was handed, so the
	// branch fence is proved where it is decided instead of inside a database.
	incomingSeam incomingStore
	// onOrderSeam is the DIB-470 aggregate read seam (on_order.go). Same shape, same reason: a unit
	// test states which PO STATUSES count toward a stock position without reaching a database.
	onOrderSeam onOrderStore

	// - item 5 · the standardized outbound purchase order -
	// Three more persistence seams in the established shape (nil -> *Repository), each because the
	// decision they carry is one a unit test must be able to state without a database: WHO SEES
	// COST (policySeam), WHO AN ORDER IS AIMED AT and the write-before-transport ordering
	// (sendSeam), and whose catalogue vocabulary a document speaks (catalogueSeam).
	policySeam    policyStore
	sendSeam      sendStore
	catalogueSeam vendorSKUStore
	orderSeam     orderStore

	// addresses/branches/poSender are the three OUTWARD seams. Every one of them is optional and
	// every one of them degrades downward when absent: no address book means the untagged fallback
	// has nothing to offer, no branch directory means the ship-to line is the branch code, and no
	// sender means UnwiredSender, which records the row and refuses in words.
	addresses VendorAddressBook
	branches  BranchDirectory
	poSender  POSender

	warrantyCredit   WarrantyCreditReader
	landedPayables   LandedCostPayableReader
	supplierPayments SupplierPaymentReader

	// now is the injected clock. The document stamps a printed-at time, and a document that read
	// the wall clock directly could not be rendered identically twice in a test.
	now func() time.Time

	// zone is the dealer's business day zone source; nil = the Ontario dealer's. A bill entered with
	// no bill date is dated the dealer's own day.
	zone houseday.Source
}

// WithWarrantyCreditReader binds the vendor warranty credit seam.
func (s *Service) WithWarrantyCreditReader(r WarrantyCreditReader) *Service {
	s.warrantyCredit = r
	return s
}

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (s *Service) WithZone(src houseday.Source) *Service { s.zone = src; return s }

// NewService wires the repository. Optional deps are injected via the With* methods.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, log: slog.Default(), callerFrom: identity.StaffContextFromContext,
		now: time.Now}
}

// WithClock injects the clock the document's printed-at stamp reads. Nil is ignored.
func (s *Service) WithClock(now func() time.Time) *Service {
	if now != nil {
		s.now = now
	}
	return s
}

// clock is the one reader of the injected time source, so no other file in this package needs to
// know whether one was injected.
func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// WithVendorAddresses injects the vendor address book the untagged fallback reads. Nil means the
// fallback has no candidates, which lands an untagged send on "tag an ordering contact", a refusal
// in words, never an error.
func (s *Service) WithVendorAddresses(a VendorAddressBook) *Service { s.addresses = a; return s }

// WithBranches injects the branch directory the document's ship-to line reads.
func (s *Service) WithBranches(b BranchDirectory) *Service { s.branches = b; return s }

// WithPOSender injects the outbound transport. Nil means UnwiredSender: the send is recorded and
// then honestly marked FAILED, because nothing in this build delivers email.
func (s *Service) WithPOSender(p POSender) *Service { s.poSender = p; return s }

// WithStockReceiver injects the inventory receiving seam. Without it, receiving refuses.
func (s *Service) WithStockReceiver(r StockReceiver) *Service { s.stock = r; return s }

// WithVendors injects the vendor directory.
func (s *Service) WithVendors(v VendorDirectory) *Service { s.vendor = v; return s }

// WithLandedCoster injects the landed-cost tie-in.
func (s *Service) WithLandedCoster(l LandedCoster) *Service { s.landed = l; return s }

// WithGLPoster injects the GL posting seam (the vendor-bill journal).
func (s *Service) WithGLPoster(p GLPoster) *Service { s.gl = p; return s }

// WithRuleResolver injects the GL posting-rule resolver (2150 / 2100 / 5250 / 2200).
func (s *Service) WithRuleResolver(r RuleResolver) *Service { s.rules = r; return s }

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// WithLogger injects a logger.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	if l != nil {
		s.log = l
	}
	return s
}

// withCaller is the test seam for the guard-stamped StaffContext.
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// withIncomingStore is the test seam for the DIB-460 read (incoming.go).
func (s *Service) withIncomingStore(st incomingStore) *Service {
	s.incomingSeam = st
	return s
}

// withOnOrderStore is the test seam for the DIB-470 aggregate read (on_order.go).
func (s *Service) withOnOrderStore(st onOrderStore) *Service {
	s.onOrderSeam = st
	return s
}

// withPolicyStore / withSendStore / withCatalogueStore are the item-5 test seams. Same shape and
// same argument as the two above: the cost gate, the one-recipient rule and the vendor vocabulary
// are all decided in Go, so they are proved in Go.
func (s *Service) withPolicyStore(st policyStore) *Service {
	s.policySeam = st
	return s
}

func (s *Service) withSendStore(st sendStore) *Service {
	s.sendSeam = st
	return s
}

func (s *Service) withCatalogueStore(st vendorSKUStore) *Service {
	s.catalogueSeam = st
	return s
}

func (s *Service) withOrderStore(st orderStore) *Service {
	s.orderSeam = st
	return s
}

// caller resolves the trusted, server-stamped acting staff member (never a spoofable header).
func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, errUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, errUnauthorized
	}
	return c, nil
}

// - handler -

// Handler exposes the purchasing HTTP surface.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the purchasing routes behind the identity guards.
//
// The gate split is about consequence, not seniority. Reads and the receiving worklist are RequireStaff, the
// people who receive a truck are frontline, and a receiving screen only managers can open is a screen nobody
// uses at 7am. Everything that commits money or stock (placing an order, receiving, attaching a freight
// invoice, entering and posting a bill) is RequireManager. The tolerance configuration is RequireAdmin: it
// decides how large a variance may pass without anyone looking at it, which is a control setting, not an
// operational one.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/purchasing/vendors", g.RequireStaff(h.staff(h.listVendors)))
	// The vendor MASTER writes sit at the same gate the module's other vendor writes (contact, SKU)
	// already sit at: RequireManager, a vendor record being a commitment the business stands behind.
	r.Handle("POST /api/v1/purchasing/vendors", g.RequireManager(h.staff(h.createVendor)))
	r.Handle("PUT /api/v1/purchasing/vendors/{vendorID}", g.RequireManager(h.staff(h.setVendor)))

	r.Handle("GET /api/v1/purchasing/pos", g.RequireStaff(h.staff(h.listPOs)))
	r.Handle("POST /api/v1/purchasing/pos", g.RequireManager(h.staff(h.createPO)))
	r.Handle("GET /api/v1/purchasing/pos/{id}", g.RequireStaff(h.staff(h.getPO)))
	r.Handle("POST /api/v1/purchasing/pos/{id}/lines", g.RequireManager(h.staff(h.addLine)))
	r.Handle("PUT /api/v1/purchasing/pos/{id}/lines/{lineID}", g.RequireManager(h.staff(h.setLine)))
	r.Handle("DELETE /api/v1/purchasing/pos/{id}/lines/{lineID}", g.RequireManager(h.staff(h.removeLine)))
	r.Handle("POST /api/v1/purchasing/pos/{id}/transition", g.RequireManager(h.staff(h.transition)))
	r.Handle("POST /api/v1/purchasing/pos/{id}/receive", g.RequireManager(h.staff(h.receive)))
	// DIB-460. The READ is RequireStaff for the same reason the receiving worklist is: "when is it
	// showing up" is a counter question, and an answer only managers can see is the pain the ticket
	// describes ("having to go ask procurement"). The WRITE is RequireManager: a promised receiving
	// date is a buyer's statement about a vendor commitment, and the receiving worklist sorts on it.
	r.Handle("GET /api/v1/purchasing/incoming", g.RequireStaff(h.staff(h.incoming)))
	r.Handle("PUT /api/v1/purchasing/pos/{id}/expected-date", g.RequireManager(h.staff(h.setExpectedDate)))
	// DIB-572 (ported from the ERP). RequireManager on the same principle as the line above: the
	// arrival a purchase order carries is a buyer's statement about how the goods are coming, and
	// in the ERP the receiving worklist renders it to a yard that plans around it (core's worklist
	// does not render the column yet; the DIB-573 read half is a recorded residual).
	r.Handle("PUT /api/v1/purchasing/pos/{id}/delivery-method", g.RequireManager(h.staff(h.setDeliveryMethod)))
	r.Handle("POST /api/v1/purchasing/pos/{id}/apply-warranty-credit", g.RequireManager(h.staff(h.applyWarrantyCredit)))

	r.Handle("GET /api/v1/purchasing/receiving", g.RequireStaff(h.staff(h.worklist)))
	r.Handle("GET /api/v1/purchasing/receipts/{id}", g.RequireStaff(h.staff(h.getReceipt)))
	r.Handle("POST /api/v1/purchasing/receipts/{id}/landed-cost", g.RequireManager(h.staff(h.attachLandedCost)))

	r.Handle("GET /api/v1/purchasing/bills", g.RequireStaff(h.staff(h.listBills)))
	r.Handle("POST /api/v1/purchasing/bills", g.RequireManager(h.staff(h.createBill)))
	r.Handle("GET /api/v1/purchasing/bills/{id}", g.RequireStaff(h.staff(h.getBill)))
	r.Handle("POST /api/v1/purchasing/bills/{id}/post", g.RequireManager(h.staff(h.postBill)))
	r.Handle("GET /api/v1/purchasing/ap-aging", g.RequireManager(h.staff(h.apAging)))

	r.Handle("GET /api/v1/purchasing/tolerance", g.RequireStaff(h.staff(h.getTolerance)))
	r.Handle("PUT /api/v1/purchasing/tolerance", g.RequireAdmin(h.staff(h.setTolerance)))

	// - item 5 · the standardized outbound purchase order -
	//
	// THE GATE SPLIT IS THE SAME ONE, ON THE SAME PRINCIPLE: consequence, not seniority.
	//
	// Reading the DOCUMENT is RequireStaff, because printing the order and walking it to the yard is
	// a receiving job, and the document a clerk gets is the same document with two fewer columns,
	// which is the cost gate doing its work at the marshal layer rather than at the door.
	// SENDING it is RequireManager: an order that leaves the building is a commitment.
	// The POLICY is RequireAdmin, alongside the match tolerance, and for its reason: it decides who
	// in the building learns what the dealer pays, which is a control setting and not an
	// operational one.
	r.Handle("GET /api/v1/purchasing/policy", g.RequireStaff(h.staff(h.getPurchasingPolicy)))
	r.Handle("PUT /api/v1/purchasing/policy", g.RequireAdmin(h.staff(h.setPurchasingPolicy)))

	r.Handle("GET /api/v1/purchasing/vendors/{vendorID}/contact", g.RequireStaff(h.staff(h.getVendorContact)))
	r.Handle("PUT /api/v1/purchasing/vendors/{vendorID}/contact", g.RequireManager(h.staff(h.setVendorContact)))
	r.Handle("DELETE /api/v1/purchasing/vendors/{vendorID}/contact", g.RequireManager(h.staff(h.deleteVendorContact)))
	r.Handle("GET /api/v1/purchasing/vendors/{vendorID}/skus", g.RequireStaff(h.staff(h.listVendorSKUs)))
	r.Handle("PUT /api/v1/purchasing/vendors/{vendorID}/skus", g.RequireManager(h.staff(h.setVendorSKU)))
	r.Handle("DELETE /api/v1/purchasing/vendors/{vendorID}/skus/{productID}", g.RequireManager(h.staff(h.deleteVendorSKU)))

	r.Handle("GET /api/v1/purchasing/pos/{id}/document", g.RequireStaff(h.staff(h.poDocumentHTML)))
	r.Handle("GET /api/v1/purchasing/pos/{id}/document.csv", g.RequireStaff(h.staff(h.poDocumentCSV)))
	r.Handle("GET /api/v1/purchasing/pos/{id}/costs", g.RequireStaff(h.staff(h.poCosts)))
	r.Handle("GET /api/v1/purchasing/pos/{id}/sends", g.RequireStaff(h.staff(h.listPOSends)))
	r.Handle("POST /api/v1/purchasing/pos/{id}/send", g.RequireManager(h.staff(h.sendPO)))

	r.Handle("GET /api/v1/purchasing/vendor-ref", g.RequireStaff(h.staff(h.vendorRef)))
}
