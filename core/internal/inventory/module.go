// Package inventory is the W6 INV-CORE module: the minimal stock-move ledger + weighted-average cost read
// model that lets a counter sale decrement stock and post real cost-of-goods, and that A-15 counts + A-16
// landed cost build on. It owns the 0037 additions to the 0005 stock ledger (append-only enforcement, the
// on-hand + weighted-avg-cost read model, the relaxed negative-stock CHECK).
//
// SCAFFOLD STATUS: exported types, the frozen seam interfaces/signatures, and the provider methods main.go +
// pos + gl depend on are frozen here; bodies are errNotImplemented stubs so the tree compiles + boots, EXCEPT
// the two LIVE-PATH seam methods, which are BEHAVIOR-PRESERVING at scaffold and must stay so until W6-INVCORE
// fills them:
//
//	UnitCostCents  → (0, false, nil) , byte-identical to gl.NoopCostBasis (the order.fulfilled COGS leg stays
//	                                    dormant; the day-one main.go swap gl.NoopCostBasis→inventorySvc is SAFE
//	                                    precisely because of this).
//	DecrementForSaleTx → nil no-op   , identical to pos.NoopStock (a POS sale still commits with zero moves).
//
// Signatures are FROZEN, a worker fills a body, never changes a signature. No lot/serial; branch-grain.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"log/slog"
	"math"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// errNotImplemented marks a scaffold stub the INV-CORE worker fills.
var errNotImplemented = errors.New("inventory: not implemented (scaffold stub)")

// Direction is the stock-move direction (0037 uses the 0005 stock_moves.move_type CHECK set; IN/OUT are the
// coarse-grained producer intents the seams speak).
const (
	DirectionIn  = "IN"
	DirectionOut = "OUT"
)

// StockMove is one immutable ledger line (append-only; corrections are compensating moves).
type StockMove struct {
	ID            string  `json:"id"`
	ProductID     string  `json:"product_id"`
	BranchID      string  `json:"branch_id"`
	QtyBase       float64 `json:"qty_base"`
	UOM           string  `json:"uom"`
	Direction     string  `json:"direction"`
	MoveType      string  `json:"move_type"`
	SourceType    string  `json:"source_type"`
	SourceRefID   string  `json:"source_ref_id"`
	UnitCostCents int64   `json:"unit_cost_cents,omitempty"`
	OccurredAt    string  `json:"occurred_at"`
}

// OnHand is a product's on-hand + available at a branch/location (available may be negative, by design).
type OnHand struct {
	ProductID     string  `json:"product_id"`
	LocationID    string  `json:"location_id"`
	BranchID      string  `json:"branch_id"`
	OnHandBase    float64 `json:"on_hand_base"`
	AllocatedBase float64 `json:"allocated_base"`
	AvailableBase float64 `json:"available_base"`
	AvgCostCents  int64   `json:"avg_cost_cents,omitempty"`
}

// SaleLine is one fulfilled line the POS hands to DecrementForSaleTx (the frozen order.fulfilled shape).
type SaleLine struct {
	ProductID string  `json:"product_id"`
	QtyBase   float64 `json:"qty_base"`
}

// DecrementInput is the POS stock-decrement request (COLLECT_NOW only at W6; DELIVERED/COLLECT_LATER record
// zero moves and are quantified on the pending-fulfilment surface, the DX-01 PARTIAL closure).
type DecrementInput struct {
	OrderID  string
	BranchID string
	SaleType string
	Lines    []SaleLine
}

// RestockInput is the POS return restock request (W7): stock comes back IN at the CURRENT weighted
// average (so the average is unchanged), one RETURN move per line, inside the caller's return tx.
type RestockInput struct {
	ReturnID string
	OrderID  string
	BranchID string
	ReturnNo string
	Lines    []SaleLine
}

// PendingFulfilment is one COLLECT_LATER/DELIVERED line awaiting a stock move (the DX-01 gap quantifier).
type PendingFulfilment struct {
	OrderID   string  `json:"order_id"`
	ProductID string  `json:"product_id"`
	QtyBase   float64 `json:"qty_base"`
	SaleType  string  `json:"sale_type"`
}

// - repository -

// Repository is inventory's pgx access layer over the 0005/0037 stock tables + read models (nil pool tolerated).
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Pool exposes the pool so a producer can compose a move into its own tx.
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// - service -

// GLPoster posts a balanced journal through the GL provider (satisfied by a main.go adapter over
// *gl.Service.PostExternal, inventory posts COGS/receipt journals here without importing gl internals).
type GLPoster interface {
	PostJournal(ctx context.Context, in GLJournal) (string, error)
}

// GLPosterTx is an optional extension of GLPoster that posts a journal inside the caller's transaction
// so a stock transfer move and its journal commit or roll back atomically.
type GLPosterTx interface {
	GLPoster
	PostJournalTx(ctx context.Context, tx pgx.Tx, in GLJournal) (string, error)
}

// RuleResolver resolves a posting-rule role to an account code (satisfied by a main.go adapter over
// *gl.Service.ResolveRule). INV-CORE resolves the 1200 inventory leg through this.
type RuleResolver interface {
	ResolveRule(ctx context.Context, eventType, lineRole, branchID string) (string, error)
}

// RuleResolverTx is an optional extension of RuleResolver that resolves a rule on the caller's transaction.
type RuleResolverTx interface {
	RuleResolver
	ResolveRuleTx(ctx context.Context, tx pgx.Tx, eventType, lineRole, branchID string) (string, error)
}

// GLJournal + GLLine mirror gl.ExternalJournal by value so inventory does not import gl's service internals.
type GLJournal struct {
	Source         string
	SourceRefID    string
	IdempotencyKey string
	Memo           string
	BranchID       string
	Lines          []GLLine
}

// GLLine is one leg of a GLJournal.
type GLLine struct {
	AccountCode string
	DebitCents  int64
	CreditCents int64
	Memo        string
}

// Service is the inventory domain seam: the move ledger, weighted-avg cost, the COGS basis, and the PO-receipt
// + POS-decrement producers. Sibling seams are injected via the With* options (main.go wires the adapters).
type Service struct {
	repo  *Repository
	st    store // DB seam; nil → repo (unit tests inject a DB-free fake via withStore)
	gl    GLPoster
	rules RuleResolver
	audit audit.Recorder
	log   *slog.Logger
	// poCreator is the DIB-72 create-PO handoff (consumer-declared seam, purchasing lives in another
	// fence). nil → StubPOCreator, which creates nothing and says so.
	poCreator POCreator
	// nudger is the DIB-461 procurement-nudge handoff (consumer-declared seam, messaging lives in
	// another fence). nil → StubProcurementNudger, which posts nothing and says so.
	nudger ProcurementNudger
	// onOrder is the DIB-470 "already on order" read (consumer-declared seam, purchase orders live
	// in purchasing's fence). nil → the leg is ZERO and the worklist says the figure is degraded,
	// rather than quietly grading against a number nobody measured. See on_order_seam.go.
	onOrder OnOrderReader
	// draftPOs points a worklist row at the draft a buyer started for it (draft_po_seam.go).
	draftPOs DraftPOReader
	// shortOrders names the sales orders a worklist row holds up (short_order_seam.go, change 5
	// slice S3). nil → the row carries no order numbers, the fields stay absent.
	shortOrders ShortOrderReader
	branchNames BranchNameReader
	// zone is the dealer's business day zone source; nil = the Ontario dealer's. A transfer's promised
	// arrival is counted in the dealer's own calendar.
	zone houseday.Source
}

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (s *Service) WithZone(src houseday.Source) *Service { s.zone = src; return s }

// NewService wires the repository. Optional deps are injected via the With* methods.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, log: slog.Default()}
}

// WithGLPoster injects the GL posting seam (COGS / receipt / adjustment journals).
func (s *Service) WithGLPoster(p GLPoster) *Service { s.gl = p; return s }

// WithRuleResolver injects the GL posting-rule resolver (the 1200 inventory leg).
func (s *Service) WithRuleResolver(r RuleResolver) *Service { s.rules = r; return s }

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// - LIVE-PATH seams (behavior-preserving at scaffold, DO NOT change these two bodies to error) -

// UnitCostCents returns the weighted-average unit cost for a fulfilled line, satisfying gl.CostBasis. SCAFFOLD:
// returns (0, false, nil), byte-identical to gl.NoopCostBasis, so the order.fulfilled COGS leg stays dormant.
// W6-INVCORE fills the real weighted-avg lookup (found=true → DR 5000 / CR 1200). FROZEN signature.
func (s *Service) UnitCostCents(ctx context.Context, productID string, qtyBase float64) (int64, bool, error) {
	return s.unitCost(ctx, productID, qtyBase)
}

// BranchUnitCostCents returns the branch weighted-average cost extended over qtyBase,
// satisfying gl.BranchCostBasis so the stock move and the GL journal share one cost basis.
func (s *Service) BranchUnitCostCents(ctx context.Context, productID, branchID string, qtyBase float64) (int64, bool, error) {
	return s.branchUnitCost(ctx, productID, branchID, qtyBase)
}

// DecrementForSaleTx decrements stock for a fulfilled sale inside the caller's tx, satisfying the pos
// StockDecrementer seam. SCAFFOLD: no-op returning nil, identical to pos.NoopStock, so a POS sale still
// commits unchanged. W6-INVCORE fills the real SHIP moves for a COLLECT_NOW sale (DELIVERED/COLLECT_LATER →
// zero moves, listed on pending-fulfilment). FROZEN signature.
func (s *Service) DecrementForSaleTx(ctx context.Context, tx pgx.Tx, in DecrementInput) error {
	_, err := s.decrementForSale(ctx, tx, in)
	return err
}

// ShipLineValue is one sale line's own move value, the figure the ring's
// order.fulfilled event carries so the COGS journal posts the value that
// moved (round 2, S3's P1).
type ShipLineValue struct {
	ProductID  string  `json:"product_id"`
	QtyBase    float64 `json:"qty_base"`
	ValueCents int64   `json:"value_cents"`
}

// DecrementForSaleValuesTx is DecrementForSaleTx answering each line's own
// move value. The ring's emit reads these, never a re-read average.
func (s *Service) DecrementForSaleValuesTx(ctx context.Context, tx pgx.Tx, in DecrementInput) ([]ShipLineValue, error) {
	return s.decrementForSale(ctx, tx, in)
}

// RestockForReturnTx books a POS return back into stock inside the caller's tx, satisfying the pos
// StockRestocker seam (W7, the mirror of DecrementForSaleTx): one RETURN move per line, to_location =
// the branch, valued at the CURRENT weighted average via GetBranchCost so the average is unchanged,
// balance upsert +qty. Inventory stays the sole stock writer.
func (s *Service) RestockForReturnTx(ctx context.Context, tx pgx.Tx, in RestockInput) error {
	return s.restockForReturn(ctx, tx, in)
}

// ReturnMoveValuesTx answers the return's RETURN moves' per line values, the
// figure the order.returned event carries (round 2, S3's P1).
func (s *Service) ReturnMoveValuesTx(ctx context.Context, tx pgx.Tx, returnID string) ([]ShipLineValue, error) {
	querier := interface {
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	}(tx)
	if querier == nil {
		if s.repo == nil || s.repo.pool == nil {
			return nil, nil
		}
		querier = s.repo.pool
	}
	rows, err := querier.Query(ctx, `
		SELECT product_id::text, qty_base::float8, value_cents
		  FROM stock_moves
		 WHERE source_type = 'pos_return' AND source_ref_id = $1::uuid AND move_type = 'RETURN'`, returnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ShipLineValue, 0, 4)
	for rows.Next() {
		var v ShipLineValue
		if err := rows.Scan(&v.ProductID, &v.QtyBase, &v.ValueCents); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ShippedValueForOrderLineTx answers the value the ORIGINAL SALE moved for one order line, the
// figure the write off path posts (round 2, S3's P1).
//
// THE GOODS WERE WRITTEN OFF, NOT MOVED, so there is no move of their own to read. The value they
// carry now is the value they left the shelf with when they were sold: the sale's own SHIP move
// value for that line. A re-read average would answer a different question (what the goods cost
// today) and would answer it differently for every line of one note.
//
// THE SHARE, NOT THE WHOLE MOVE. A move of four units for four hundred cents answers two units for
// two hundred cents, so a partial write off is not booked at the whole line's cost.
//
// NOT FOUND IS AN ANSWER, NOT A FAILURE: an order that never moved the line (an old row, or goods
// that never left the shelf) has no sale value, and the caller falls back to the average.
func (s *Service) ShippedValueForOrderLineTx(ctx context.Context, tx pgx.Tx, orderID, productID string, qtyBase float64) (ShipLineValue, bool, error) {
	if s.repo == nil || s.repo.pool == nil || orderID == "" || productID == "" {
		return ShipLineValue{}, false, nil
	}
	var out ShipLineValue
	var moveQty float64
	err := tx.QueryRow(ctx, `
		SELECT product_id::text, qty_base::float8, value_cents
		  FROM stock_moves
		 WHERE source_ref_id = $1::uuid AND product_id = $2::uuid AND move_type = 'SHIP'
		 ORDER BY created_at
		 LIMIT 1`, orderID, productID).Scan(&out.ProductID, &moveQty, &out.ValueCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ShipLineValue{}, false, nil
		}
		return ShipLineValue{}, false, err
	}
	if moveQty <= 0 {
		return ShipLineValue{}, false, nil
	}
	out.QtyBase = qtyBase
	out.ValueCents = int64(math.Round(float64(out.ValueCents) * qtyBase / moveQty))
	return out, true, nil
}

// - domain surface (errNotImplemented stubs, INV-CORE fills) -

// ReceivePOTx posts the RECEIPT moves + GRNI (DR 1200 / CR 2150) for a PO receipt inside the caller's tx, and
// maintains the weighted-average cost. FROZEN signature; INV-CORE fills the body.
func (s *Service) ReceivePOTx(ctx context.Context, tx pgx.Tx, poID string) error {
	if tx == nil && s.repo != nil && s.repo.pool != nil {
		_, err := s.receive(ctx, ReceiveRequest{POID: poID})
		return err
	}
	_, err := s.applyReceipt(ctx, tx, poID, nil, "")
	return err
}

// OnHandFor returns the on-hand + weighted-avg for a product across branches. INV-CORE fills the body.
func (s *Service) OnHandFor(ctx context.Context, productID string) ([]OnHand, error) {
	return s.db().OnHandFor(ctx, productID)
}

// PendingFulfilments lists the COLLECT_LATER/DELIVERED lines that recorded no stock move (the DX-01 gap).
func (s *Service) PendingFulfilments(ctx context.Context, branchID string) ([]PendingFulfilment, error) {
	return s.db().PendingFulfilments(ctx, branchID)
}

// PreReceiptException is a Controller exception row for a sale fulfilled before the product's
// first receipt at the branch (ruling 82.4: posted at fallback cost and awaiting true-up at first receipt).
type PreReceiptException struct {
	MoveID                   string  `json:"move_id"`
	OrderID                  string  `json:"order_id"`
	ProductID                string  `json:"product_id"`
	BranchID                 string  `json:"branch_id"`
	QtyBase                  float64 `json:"qty_base"`
	ProvisionalUnitCostCents int64   `json:"provisional_unit_cost_cents"`
	ProvisionalValueCents    int64   `json:"provisional_value_cents"`
	FallbackSource           string  `json:"fallback_source"`
	OccurredAt               string  `json:"occurred_at"`
}

// ControllerExceptions lists pre-receipt sales that were posted at a fallback cost and have not yet
// been trued up by a subsequent receipt at that branch.
func (s *Service) ControllerExceptions(ctx context.Context, branchID string) ([]PreReceiptException, error) {
	return s.db().PreReceiptExceptions(ctx, branchID)
}

// - posting consumer registration -

// InventoryTopics is the frozen set of outbox topics the inventory posting consumer owns. These do NOT overlap
// the eight gl.PostedTopics (the dispatcher now panics on a duplicate topic registration).
var InventoryTopics = []string{
	"po.received",
	"inventory.adjusted",
}

// RegisterPostingHandlers wires the inventory posting consumer for its topics onto the single dispatcher
// (main.go calls this once at boot, alongside glSvc.RegisterPostingHandlers). SCAFFOLD: the handler is an
// errNotImplemented stub; INV-CORE + A-15 fill it. No producer emits these topics at scaffold, so nothing
// fires. FROZEN topic set.
func (s *Service) RegisterPostingHandlers(d *notify.Dispatcher) {
	for _, topic := range InventoryTopics {
		d.Register(topic, s.postEvent)
	}
}

// postEvent is the single inventory outbox handler. INV-CORE fills it (RECEIPT moves for po.received, the
// reason-coded 1200 adjustment for inventory.adjusted). SCAFFOLD stub.
func (s *Service) postEvent(ctx context.Context, e notify.Event) error {
	switch e.Topic {
	case "po.received":
		return s.postPOReceived(ctx, e.Payload)
	case "inventory.adjusted":
		return s.postInventoryAdjusted(ctx, e.Payload)
	default:
		return fmt.Errorf("inventory: no posting builder for topic %q", e.Topic)
	}
}

// - handler -

// Handler exposes the inventory HTTP surface (on-hand, moves, pending-fulfilment). SCAFFOLD: routes are
// registered but every handler answers 501 until INV-CORE fills the service.
type Handler struct {
	userLink  identity.UserIDLinker
	svc       *Service
	log       *slog.Logger
	costRoles CostRoles
}

// NewHandler wires the service.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers the inventory routes behind the identity guards. Reads are RequireStaff;
// receiving + adjustments are RequireManager. FROZEN guard signature; INV-CORE fills the route bodies.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/inventory/on-hand", g.RequireStaff(h.staff(h.onHand)))
	r.Handle("GET /api/v1/inventory/moves", g.RequireStaff(h.staff(h.moves)))
	r.Handle("GET /api/v1/inventory/pending-fulfilment", g.RequireStaff(h.staff(h.pendingFulfilment)))
	// Round 2 (S3's P2): the Controller's pre receipt exceptions read (ruling 82.4). Manager gated on
	// top of the branch fence the service applies.
	r.Handle("GET /api/v1/inventory/controller-exceptions", g.RequireManager(h.staff(h.controllerExceptions)))
	r.Handle("POST /api/v1/inventory/receive", g.RequireManager(h.staff(h.receive)))

	// - DIB-472 · the transit-days config behind the transfer eta pre-fill -
	//
	// REGISTERED HERE, not in RegisterStockTruthRoutes, for one reason: requireAdmin is a parameter this
	// function ALREADY takes and main.go ALREADY passes (it has been unused since the scaffold), while
	// the stock-truth registration takes only the staff and manager guards. So the write lands behind
	// the house's real tech-admin gate, the same Tier-1-GLOBAL predicate the audit viewer and the
	// quick-action knob use, with NO change to main.go, which is parked for the union.
	//
	// READ is staff: the ship flow pre-fills from it, and a manager loading a truck must be able to see
	// the default they are accepting. WRITE is admin: it is a tech-admin knob, and it is the ONE thing
	// standing between "a maintained default" and the ticket's stated fear of a stale guess with a
	// database row behind it.
	r.Handle("GET /api/v1/inventory/transit-days", g.RequireStaff(h.staff(h.transitDays)))
	r.Handle("PUT /api/v1/inventory/transit-days", g.RequireAdmin(h.staff(h.putTransitDays)))
}
