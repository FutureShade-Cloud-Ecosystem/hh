package printing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// receiptStore is the receipt's read seam (satisfied by *Repository). Kept separate from queueStore
// so a unit test can fake the sale reads without faking the queue, and vice versa.
type receiptStore interface {
	ReadReceiptSale(ctx context.Context, id string, global bool, branchIDs []string) (receiptSale, bool, error)
	ReadReceiptLines(ctx context.Context, orderID string) ([]ReceiptLine, error)
	ReadReceiptTenders(ctx context.Context, paymentID, invoiceID string) ([]ReceiptTender, error)
	ReadBranchHeader(ctx context.Context, branchID string) (branchHeader, error)
}

// ReceiptService renders the counter receipt for a committed sale (DIB-410).
type ReceiptService struct {
	store      receiptStore
	queue      *QueueService
	audit      audit.Recorder
	callerFrom func(context.Context) (identity.StaffContext, bool)
	now        func() time.Time
}

// NewReceiptService composes the sale reads with the print queue. The queue is REQUIRED, not
// optional: the print record is what makes the reprint marker true.
func NewReceiptService(store receiptStore, queue *QueueService) *ReceiptService {
	return &ReceiptService{
		store:      store,
		queue:      queue,
		callerFrom: identity.StaffContextFromContext,
		now:        time.Now,
	}
}

// WithAudit injects the A-21 recorder, every receipt fetch is a document handed to a customer, so
// who printed what, when is a recorded fact.
func (s *ReceiptService) WithAudit(r audit.Recorder) *ReceiptService { s.audit = r; return s }

// WithClock injects a deterministic clock (tests).
func (s *ReceiptService) WithClock(now func() time.Time) *ReceiptService {
	if now != nil {
		s.now = now
	}
	return s
}

// caller resolves the authenticated staff caller exactly as order.Service does (order/service.go:176).
func (s *ReceiptService) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, ErrUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, ErrUnauthorized
	}
	return c, nil
}

// Receipt builds the receipt document for one committed sale, addressed by pos_sale id OR order id.
//
// AUTHORITY (the order/edit.go authorizeEdit shape, order/edit.go:365). RequireStaff gets a caller
// onto the context; this adds the two checks that guard actually matters for: the caller must be
// ACTIVE, and the SALE'S BRANCH must be one the caller can reach (CanAccessBranch). A receipt names
// the customer, every line they bought, what they paid and how, a Kingston cashier pulling a
// Trenton sale's receipt is precisely the cross-branch read the branch scope exists to stop.
//
// REPRINT. The count is read from print_queue BEFORE this fetch's own job is recorded, so the first
// fetch renders as the original and every later one carries the REPRINT marker with its ordinal.
// That count survives a refresh, another cashier and another terminal, because it lives in the
// database rather than in the tab that printed it.
func (s *ReceiptService) Receipt(ctx context.Context, id string) (Receipt, error) {
	if strings.TrimSpace(id) == "" {
		return Receipt{}, ErrNotFound
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if caller.Status != identity.StatusActive {
		return Receipt{}, ErrForbidden
	}
	// The "#SO-" path resolves BRANCH-SCOPED (a sequential number must not become an existence
	// oracle for another branch); a uuid resolves unscoped and is then answered 403 by the reach
	// check below. Both legs land on the same sale row.
	global, branches := callerScope(caller)
	sale, found, err := s.store.ReadReceiptSale(ctx, strings.TrimSpace(id), global, branches)
	if err != nil {
		return Receipt{}, err
	}
	if !found {
		return Receipt{}, ErrNotFound
	}
	if !caller.CanAccessBranch(sale.BranchID) {
		return Receipt{}, ErrForbidden
	}

	head, err := s.store.ReadBranchHeader(ctx, sale.BranchID)
	if err != nil {
		return Receipt{}, err
	}
	lines, err := s.store.ReadReceiptLines(ctx, sale.OrderID)
	if err != nil {
		return Receipt{}, err
	}
	tenders, err := s.store.ReadReceiptTenders(ctx, sale.PaymentID, sale.InvoiceID)
	if err != nil {
		return Receipt{}, err
	}
	// An ACCOUNT (or as-yet-unsettled COD) sale has no payments row. A receipt that lists no tender
	// at all reads like an unpaid mistake, so the charge itself is stated as the tender.
	if len(tenders) == 0 {
		tenders = append(tenders, ReceiptTender{
			Method: sale.Tender, Label: "ON ACCOUNT", AmountCents: sale.TotalCents, Ref: sale.InvoiceNo,
		})
	}

	priorPrints, err := s.queue.countPrinted(ctx, DocReceipt, sale.OrderID)
	if err != nil {
		return Receipt{}, err
	}

	rec := Receipt{
		OrgName:           head.OrgName,
		BranchName:        head.Name,
		BranchCode:        head.Code,
		BranchAddress:     head.Address,
		OrderNo:           sale.OrderNo,
		OrderID:           sale.OrderID,
		InvoiceNo:         sale.InvoiceNo,
		SoldAt:            sale.SoldAt,
		SaleType:          sale.SaleType,
		CashierRef:        sale.CashierName,
		CustomerName:      sale.CustomerName,
		CustomerAccountNo: sale.CustomerAcct,
		Lines:             lines,
		Tenders:           tenders,
		SubtotalCents:     sale.SubtotalCents,
		TaxCents:          sale.TaxCents,
		TotalCents:        sale.TotalCents,
		BalanceCents:      sale.BalanceCents,
		TaxLabel:          hstLabel(head.HSTRateBps),
		ReprintCount:      priorPrints,
		PrintedAt:         s.now(),
	}

	// The print record is written HERE, in the request that produced the paper, and its failure
	// fails the fetch. An untracked receipt is a duplicate document nobody can detect afterwards -
	// exactly what the reprint marker exists to prevent, so "printed but unrecorded" is not an
	// outcome this route is allowed to have.
	job, err := s.queue.Enqueue(ctx, EnqueueInput{
		DocType: DocReceipt, RefID: sale.OrderID, BranchID: sale.BranchID, RenderTarget: TargetBrowser,
	})
	if err != nil {
		return Receipt{}, err
	}
	// BROWSER jobs complete in-request: the operator's print dialog is the device, so the job is
	// PRINTED the moment the document is handed to the browser. A ZPL job would stay QUEUED here
	// for a device agent to claim.
	if err := s.queue.MarkPrinted(ctx, job.ID); err != nil {
		return Receipt{}, err
	}

	s.record(ctx, audit.SensitiveAccess{
		Action: audit.ActHTTPPost, EntityType: "pos.receipt", EntityID: sale.OrderID,
		ActorID: caller.UserID, BranchID: sale.BranchID, Purpose: "RECEIPT_PRINT",
		Extra: map[string]any{"status": receiptAuditStatus(priorPrints)},
	})
	return rec, nil
}

// callerScope flattens a StaffContext into the (global, branchIDs) pair the branch-scoped resolution
// takes, the order module's callerBranchScope, same shape.
func callerScope(c identity.StaffContext) (bool, []string) {
	if c.Scope == identity.ScopeGlobal {
		return true, nil
	}
	ids := make([]string, 0, len(c.Branches))
	for id, ok := range c.Branches {
		if ok {
			ids = append(ids, id)
		}
	}
	return false, ids
}

func receiptAuditStatus(priorPrints int) string {
	if priorPrints > 0 {
		return "REPRINT"
	}
	return "ORIGINAL"
}

func (s *ReceiptService) record(ctx context.Context, a audit.SensitiveAccess) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, a)
}

// hstLabel names the tax line with the rate actually configured, so a rate change shows on paper
// without a code change.
func hstLabel(rateBps int) string {
	if rateBps <= 0 {
		return "HST"
	}
	whole := rateBps / 100
	frac := rateBps % 100
	if frac == 0 {
		return fmt.Sprintf("HST (%d%%)", whole)
	}
	return fmt.Sprintf("HST (%d.%02d%%)", whole, frac)
}

// - the receipt document (DIB-410) -

// Receipt is the rendered counter receipt's whole data model. It is a SNAPSHOT read: everything the
// paper says is read at fetch time from the committed sale, so a receipt reprinted next month shows
// what the sale actually was.
type Receipt struct {
	// branch header, locations (the branch row) + system_settings org fields
	OrgName       string
	BranchName    string
	BranchCode    string
	BranchAddress string

	// the document's identity. OrderNo is the "#SO-" number the counter, the customer and every
	// downstream lookup speak in, it is the receipt's headline.
	OrderNo    string
	OrderID    string
	InvoiceNo  string
	SoldAt     time.Time
	SaleType   string
	CashierRef string

	CustomerName      string
	CustomerAccountNo string

	Lines   []ReceiptLine
	Tenders []ReceiptTender

	SubtotalCents int64
	TaxCents      int64
	TotalCents    int64
	BalanceCents  int64
	TaxLabel      string // "HST (13%)", the rate comes from system_settings, never a literal

	// ReprintCount is how many times this receipt was ALREADY printed before this fetch. 0 → the
	// original; >0 → the REPRINT marker renders. It is counted from print_queue, so it survives a
	// page refresh, a different cashier, and a different terminal.
	ReprintCount int
	PrintedAt    time.Time
}

// IsReprint reports whether this rendering is a reprint (used by the template).
func (r Receipt) IsReprint() bool { return r.ReprintCount > 0 }

// ReceiptLine is one sold line as the customer reads it: what, how many, at what unit price, for
// how much.
type ReceiptLine struct {
	LineNo         int
	SKU            string
	Description    string
	Qty            float64
	UOM            string
	UnitPriceCents int64
	PriceUOM       string
	LineTotalCents int64
}

// ReceiptTender is one line of "how this was paid". A tendered sale carries its payments rows; an
// ACCOUNT sale carries the single synthetic "ON ACCOUNT" row for the balance charged, because a
// receipt that lists no tender at all reads like an unpaid mistake.
type ReceiptTender struct {
	Method      string
	Label       string
	AmountCents int64
	Ref         string

	// Simulated marks a CARD tender whose gateway ref names a simulated rail's mint: the TILL's
	// capture stub by its exported prefix (payment.StubCaptureRefPrefix), the PORTAL rail's own
	// stub by its exact mint shape (payments.IsStubChargeRef). Either way the charge was that
	// deterministic stub's, nothing reached a processor, and the printed receipt says so beside
	// the label. The flag is stamped from the row's own ref, so a reprint stays honest even after
	// the box switches rails: the word follows what actually processed that payment.
	Simulated bool
}
