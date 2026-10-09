package billing

// register_service.go, the receivables register's decisions (DIB-510).
//
// Three things live here and nothing else does:
//   1. THE DERIVED STATUS. `invoices.status` says FINALIZED for a document due next week and for one
//      ninety-one days late, and the difference between those two is the entire subject of this
//      screen. registerStatus is the one place that difference is decided, server-side, against the
//      SERVICE CLOCK, so the pill an operator argues about and the chip that counted it were computed
//      by the same function on the same instant.
//   2. THE ACCOUNT-POSITION GATE. Marshal-layer byte absence, fail closed. See gateAccount.
//   3. THE ORCHESTRATION of the several small reads the detail panel needs.
//
// WHAT IS NOT HERE: any money arithmetic. Totals come from stored columns; applied comes from a SQL
// aggregate; payable comes from the payments module's own definition. The only numbers this file
// produces are a day count and a set of booleans.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// registerStatus derives the pill a row wears, and the day count that goes in it.
//
// THE ORDER OF THE CASES IS THE DESIGN. A disputed invoice reads DISPUTED even when it is also
// ninety days late, because "they are arguing" is why it has not been paid and it is what the next
// person must know before they pick a tone. Everything below that is the document's own arithmetic.
//
// Days overdue is inclusive of today: an invoice due yesterday is one day overdue. Both dates are
// truncated to the day before subtracting, an invoice is not "0.6 days late", and a subtraction of
// timestamps would make the answer depend on what time of day somebody opened the screen.
func registerStatus(v RegisterRow, asOf time.Time, dueSoonDays int) (string, int) {
	if v.DocType == "CREDIT_MEMO" {
		return StatusCredit, 0
	}
	switch v.StoredStatus {
	case "DRAFT":
		return StatusDraft, 0
	case "VOID":
		return StatusVoid, 0
	case "PAID":
		return StatusPaid, 0
	}
	days := daysBetween(v.DueDate, asOf)
	if v.Disputed {
		if days > 0 {
			return StatusDisputed, days
		}
		return StatusDisputed, 0
	}
	if days > 0 {
		return StatusOverdue, days
	}
	if v.StoredStatus == "PARTIALLY_PAID" {
		return StatusPartiallyPaid, 0
	}
	if dueSoonDays > 0 && -days <= dueSoonDays {
		return StatusDueSoon, 0
	}
	return StatusOpen, 0
}

// daysBetween is asOf − due in whole days, both truncated to their calendar day in UTC. Positive
// means past due.
func daysBetween(due, asOf time.Time) int {
	d := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, time.UTC)
	a := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)
	return int(a.Sub(d).Hours() / 24)
}

// accountStatusFor is the line under the customer's name.
//
// It is NOT gated. Whether an account is stopped is operational: the POS credit gate already refuses
// the sale at the counter, so the person about to make that sale is entitled to know why before the
// customer is standing in front of them. What IS gated is the FIGURES behind it, see gateAccount.
//
// The order is severity: a stopped account is a stopped account whatever else is true of it.
func accountStatusFor(onHold, accountDisputed bool, balanceCents int64) string {
	switch {
	case onHold:
		return AccountCreditHold
	case accountDisputed:
		return AccountUnderReview
	case balanceCents > 0:
		return AccountCreditOK
	default:
		return AccountCurrent
	}
}

// -- the gate -----------------------------------------------------------------------------------

// accountAllowed answers whether THIS caller may receive the ACCOUNT-level position, per policy.
//
// FAIL CLOSED, three times over: an unreadable policy refuses, an unrecognised policy value refuses,
// and an unauthenticated caller refuses. The one permissive value (ALL_STAFF) has to be spelled out
// by a dealer administrator in the data, which is the only place a decision like that should be
// makeable.
//
// This is the whole of the enforcement. There is no CSS half, no "hidden" class, no field that
// arrives empty-but-present: the marshaller below simply never attaches the struct, so a caller who
// reads the JSON off the wire finds no key. That is what "byte-absent, never hidden" means and it is
// the only version of it that survives somebody opening the network tab.
func accountAllowed(ctx context.Context, callerFrom func(context.Context) (identity.StaffContext, bool), visibility string) bool {
	switch visibility {
	case VisibilityAllStaff:
		// still requires an authenticated caller, the route guard has one, but this function is the
		// thing that must be true on its own.
		_, ok := callerFrom(ctx)
		return ok
	case VisibilityManagerUp:
		c, ok := callerFrom(ctx)
		return ok && c.IsManager()
	default: // HIDDEN, "", or anything a future migration adds without teaching this function about it
		return false
	}
}

// -- the service --------------------------------------------------------------------------------

// registerStore is the persistence the RegisterService needs. Real impl: *Repository.
type registerStore interface {
	RegisterPolicyFor(ctx context.Context, branchID string) (RegisterPolicy, error)
	SetRegisterPolicy(ctx context.Context, p RegisterPolicy) (bool, error)
	RegisterRows(ctx context.Context, asOf time.Time, dueSoonDays int, f RegisterFilter) ([]RegisterRow, int, error)
	RegisterCountsFor(ctx context.Context, asOf time.Time, dueSoonDays int, f RegisterFilter) (RegisterCounts, error)
	SelectionTotals(ctx context.Context, invoiceIDs []string) (int64, int, error)
	InvoiceHeader(ctx context.Context, invoiceID string) (InvoiceHeaderFacts, error)
	DetailLines(ctx context.Context, invoiceID string) ([]DetailLine, error)
	AppliedCents(ctx context.Context, invoiceID string) (int64, error)
	CustomerBlockFor(ctx context.Context, customerID string) (CustomerBlock, bool, bool, error)
	AccountPositionFor(ctx context.Context, asOf time.Time, customerID string) (AccountPosition, error)
	ProofFor(ctx context.Context, orderID string) (Proof, bool, error)
	OrderFactsFor(ctx context.Context, orderID string) (string, string, string, error)
	ActivityFor(ctx context.Context, invoiceID, orderID string) ([]ActivityEntry, error)
	PromiseFor(ctx context.Context, invoiceID string) (PromiseView, bool, error)
	DisputeFor(ctx context.Context, invoiceID string) (DisputeView, bool, error)
	LastContactFor(ctx context.Context, customerID string) (*time.Time, error)
	RecordSend(ctx context.Context, d SendRow) (string, bool, error)
	MarkOpened(ctx context.Context, deliveryID string, at time.Time) (bool, error)
	LatestSendID(ctx context.Context, invoiceID string) (string, bool, error)
	AddPromise(ctx context.Context, p PromiseWrite) (PromiseView, bool, bool, error)
	SettlePromise(ctx context.Context, invoiceID, status, why string) (bool, error)
	OpenDispute(ctx context.Context, d DisputeWrite) (DisputeView, bool, bool, error)
	ResolveDispute(ctx context.Context, invoiceID, outcome, resolvedBy string) (bool, error)
}

// receiptWriter is the money seam, internal/payments' own settle, reached through an interface so
// this package depends on the CONTRACT rather than on that package's construction. billing never
// writes a payments row itself; there is one settle in this system and it is not here.
type receiptWriter interface {
	RecordReceipt(ctx context.Context, in ReceiptWrite) (ReceiptResult, error)
	PayableCentsFor(ctx context.Context, invoiceID string) (int64, string, error)
}

// ReceiptWrite is the register's request to the money seam.
type ReceiptWrite struct {
	InvoiceID      string
	CustomerID     string
	BranchID       string
	Method         string
	AmountCents    int64
	Reference      string
	IdempotencyKey string
	ReceivedBy     string
}

// Sender delivers a message. Bound to the shipped notify port at wiring time; nil leaves the register
// RECORDING sends without delivering them, which is the DIB-166 statement-notice posture: the ledger
// row is the durable fact and the transport is a separate concern that can be wired later. A nil
// sender is reported in the outcome rather than pretended away.
type Sender interface {
	SendInvoiceMessage(ctx context.Context, to, subject, body string) error
}

// RegisterService is the receivables register's logic.
type RegisterService struct {
	store      registerStore
	money      receiptWriter
	sender     Sender
	statements statementRunner
	clock      func() time.Time
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewRegisterService builds the service over a store.
func NewRegisterService(store registerStore) *RegisterService {
	return &RegisterService{
		store:      store,
		clock:      time.Now,
		callerFrom: identity.StaffContextFromContext,
	}
}

// WithMoney binds the payments settle seam. Unbound, Record Payment refuses in words rather than
// half-working: a receivables screen that took a payment and did not post it is worse than one that
// says it cannot take payments today.
func (s *RegisterService) WithMoney(w receiptWriter) *RegisterService { s.money = w; return s }

// WithSender binds the delivery port.
func (s *RegisterService) WithSender(n Sender) *RegisterService { s.sender = n; return s }

// WithClock injects the clock (tests pin the aging arithmetic).
func (s *RegisterService) WithClock(now func() time.Time) *RegisterService {
	if now != nil {
		s.clock = now
	}
	return s
}

func (s *RegisterService) withCaller(f func(context.Context) (identity.StaffContext, bool)) *RegisterService {
	s.callerFrom = f
	return s
}

// Policy resolves the dealer policy for a branch, the surface hook the settings kit calls, and the
// read the register itself opens on.
func (s *RegisterService) Policy(ctx context.Context, branchID string) (RegisterPolicy, error) {
	return s.store.RegisterPolicyFor(ctx, branchID)
}

// Register reads one page of the book plus its chip counts.
func (s *RegisterService) Register(ctx context.Context, f RegisterFilter) (RegisterPage, error) {
	asOf := s.clock()
	pol, err := s.store.RegisterPolicyFor(ctx, f.BranchID)
	if err != nil {
		return RegisterPage{}, err
	}
	rows, total, err := s.store.RegisterRows(ctx, asOf, pol.DueSoonDays, f)
	if err != nil {
		return RegisterPage{}, err
	}
	counts, err := s.store.RegisterCountsFor(ctx, asOf, pol.DueSoonDays, f)
	if err != nil {
		return RegisterPage{}, err
	}
	if accountAllowed(ctx, s.callerFrom, pol.AccountPositionVisibility) {
		// One position per ACCOUNT, not per row: a page of eleven invoices for one contractor asks the
		// same question eleven times otherwise.
		seen := map[string]*AccountPosition{}
		for i := range rows {
			pos, ok := seen[rows[i].CustomerID]
			if !ok {
				p, err := s.store.AccountPositionFor(ctx, asOf, rows[i].CustomerID)
				if err != nil {
					return RegisterPage{}, err
				}
				pos = &p
				seen[rows[i].CustomerID] = pos
			}
			rows[i].Account = pos
		}
	}
	return RegisterPage{Items: rows, Counts: counts, Total: total, AsOf: asOf}, nil
}

// Selection sums an explicit selection server-side, the bulk bar's figure.
func (s *RegisterService) Selection(ctx context.Context, invoiceIDs []string) (int64, int, error) {
	return s.store.SelectionTotals(ctx, invoiceIDs)
}

// Detail assembles the right-hand panel.
func (s *RegisterService) Detail(ctx context.Context, invoiceID string) (InvoiceDetail, error) {
	asOf := s.clock()
	h, err := s.store.InvoiceHeader(ctx, invoiceID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	pol, err := s.store.RegisterPolicyFor(ctx, h.BranchID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	lines, err := s.store.DetailLines(ctx, invoiceID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	applied, err := s.store.AppliedCents(ctx, invoiceID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	block, onHold, accountDisputed, err := s.store.CustomerBlockFor(ctx, h.CustomerID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	promise, hasPromise, err := s.store.PromiseFor(ctx, invoiceID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	dispute, hasDispute, err := s.store.DisputeFor(ctx, invoiceID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	activity, err := s.store.ActivityFor(ctx, invoiceID, h.OrderID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	lastContact, err := s.store.LastContactFor(ctx, h.CustomerID)
	if err != nil {
		return InvoiceDetail{}, err
	}
	orderNo, customerPO, salesperson, err := s.store.OrderFactsFor(ctx, h.OrderID)
	if err != nil {
		return InvoiceDetail{}, err
	}

	d := InvoiceDetail{
		ID: h.ID, InvoiceNo: h.InvoiceNo, DocType: h.DocType, StoredStatus: h.StoredStatus,
		IssueDate: h.IssueDate, DueDate: h.DueDate, BranchID: h.BranchID,
		OrderID: h.OrderID, OrderNo: orderNo, CustomerPO: customerPO, Salesperson: salesperson,
		Lines: lines, Activity: activity, ReminderTones: pol.ReminderTones,
		LastContactAt: lastContact, AsOf: asOf,
	}
	openDispute := hasDispute && dispute.Status == "OPEN"
	d.Status, d.DaysOverdue = registerStatus(RegisterRow{
		DocType: h.DocType, StoredStatus: h.StoredStatus, DueDate: h.DueDate, Disputed: openDispute,
	}, asOf, pol.DueSoonDays)

	block.AccountStatus = accountStatusFor(onHold, accountDisputed, h.BalanceCents)
	if accountAllowed(ctx, s.callerFrom, pol.AccountPositionVisibility) {
		pos, err := s.store.AccountPositionFor(ctx, asOf, h.CustomerID)
		if err != nil {
			return InvoiceDetail{}, err
		}
		block.Account = &pos
	}
	d.Customer = block

	payable := h.BalanceCents - h.HoldbackCents
	if payable < 0 {
		payable = 0
	}
	d.Totals = Totals{
		SubtotalCents: h.SubtotalCents, TaxCents: h.TaxCents, TotalCents: h.TotalCents,
		AppliedCents: applied, HoldbackCents: h.HoldbackCents, BalanceCents: h.BalanceCents,
		PayableCents: payable,
	}

	if proof, ok, err := s.store.ProofFor(ctx, h.OrderID); err != nil {
		return InvoiceDetail{}, err
	} else if ok {
		d.Proof = &proof
	}
	if hasPromise {
		d.Promise = &promise
	}
	if hasDispute {
		d.Dispute = &dispute
	}
	return d, nil
}

// -- writes -------------------------------------------------------------------------------------

// RecordPayment applies an already-taken payment to one invoice through the payments module's settle.
//
// The amount ceiling is read from the SAME definition the write enforces (PayableCentsFor), so the
// refusal an operator can hit is one the screen already stopped them reaching.
func (s *RegisterService) RecordPayment(ctx context.Context, in ReceiptInput) (ReceiptResult, error) {
	if s.money == nil {
		return ReceiptResult{}, errors.New("billing: recording payments is not wired on this deployment")
	}
	if in.IdempotencyKey == "" {
		return ReceiptResult{}, httpx.BadRequestf("recording a payment needs an idempotency key")
	}
	if in.AmountCents <= 0 {
		return ReceiptResult{}, httpx.BadRequestf("a payment amount must be greater than zero")
	}
	payable, customerID, err := s.money.PayableCentsFor(ctx, in.InvoiceID)
	if err != nil {
		return ReceiptResult{}, err
	}
	if in.AmountCents > payable {
		return ReceiptResult{}, httpx.BadRequestf("this invoice can take at most %d cents right now; refresh and retry", payable)
	}
	h, err := s.store.InvoiceHeader(ctx, in.InvoiceID)
	if err != nil {
		return ReceiptResult{}, err
	}
	caller, _ := s.callerFrom(ctx)
	res, err := s.money.RecordReceipt(ctx, ReceiptWrite{
		InvoiceID: in.InvoiceID, CustomerID: customerID, BranchID: h.BranchID,
		Method: strings.ToUpper(strings.TrimSpace(in.Method)), AmountCents: in.AmountCents,
		Reference: in.Reference, IdempotencyKey: in.IdempotencyKey, ReceivedBy: caller.UserID,
	})
	if err != nil {
		return ReceiptResult{}, err
	}
	// A promise that has been kept stops being a promise. Only on a genuinely new posting, a replay
	// already did this, and doing it again would rewrite settled_at on a row nobody touched.
	if !res.Replayed {
		if after, err := s.store.InvoiceHeader(ctx, in.InvoiceID); err == nil && after.BalanceCents <= 0 {
			_, _ = s.store.SettlePromise(ctx, in.InvoiceID, "KEPT", "paid in full")
		}
	}
	return res, nil
}

// SendReminder chases one or more documents with one tone.
//
// The tone is checked against POLICY, not against the screen: curation hides controls, policy is the
// authority (the 0173 line_discounts_enabled ruling). A dealer who removed FINAL gets a refusal in
// words if somebody posts it anyway.
//
// Per-invoice keys are derived from the caller's ONE key so a bulk send is idempotent as a whole and
// per document, a retry after a partial failure re-sends only what did not go.
func (s *RegisterService) SendReminder(ctx context.Context, in ReminderInput) (ReminderResult, error) {
	if in.IdempotencyKey == "" {
		return ReminderResult{}, httpx.BadRequestf("sending a reminder needs an idempotency key")
	}
	if len(in.InvoiceIDs) == 0 {
		return ReminderResult{}, httpx.BadRequestf("choose at least one invoice to chase")
	}
	tone := strings.ToUpper(strings.TrimSpace(in.Tone))
	caller, _ := s.callerFrom(ctx)
	out := ReminderResult{Tone: tone, Skipped: map[string]string{}}

	for _, id := range in.InvoiceIDs {
		h, err := s.store.InvoiceHeader(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				out.Skipped[id] = "no such invoice"
				continue
			}
			return ReminderResult{}, err
		}
		pol, err := s.store.RegisterPolicyFor(ctx, h.BranchID)
		if err != nil {
			return ReminderResult{}, err
		}
		if len(pol.ReminderTones) > 0 && tone != "" && !containsString(pol.ReminderTones, tone) {
			return ReminderResult{}, httpx.BadRequestf("this dealer does not send %s reminders; choose one of %s",
				tone, strings.Join(pol.ReminderTones, ", "))
		}
		if h.BalanceCents <= 0 {
			// Refusing to chase a settled document is not tidiness: a reminder for money already
			// received is the single most damaging message this system can send.
			out.Skipped[h.InvoiceNo] = "nothing outstanding"
			continue
		}
		block, _, _, err := s.store.CustomerBlockFor(ctx, h.CustomerID)
		if err != nil {
			return ReminderResult{}, err
		}
		if block.ContactEmail == "" {
			out.Skipped[h.InvoiceNo] = "no contact email on the account"
			continue
		}
		_, replayed, err := s.store.RecordSend(ctx, SendRow{
			InvoiceID: h.ID, CustomerID: h.CustomerID, InvoiceNo: h.InvoiceNo,
			Kind: "REMINDER", Tone: tone, Channel: "EMAIL", ToAddress: block.ContactEmail,
			BalanceCents: h.BalanceCents, IdempotencyKey: in.IdempotencyKey + ":" + h.ID,
			SentBy: caller.UserID,
		})
		if err != nil {
			return ReminderResult{}, err
		}
		if replayed {
			out.Replayed++
			continue
		}
		if s.sender != nil {
			// A transport failure does NOT unwrite the ledger row: we decided to chase and recorded the
			// decision, and the queue is what retries. Reported, never swallowed.
			if err := s.sender.SendInvoiceMessage(ctx, block.ContactEmail,
				reminderSubject(tone, h.InvoiceNo), reminderBody(tone, block.Name, h.InvoiceNo, h.BalanceCents, h.DueDate)); err != nil {
				out.Skipped[h.InvoiceNo] = "recorded, but delivery failed: " + err.Error()
			}
		}
		out.Sent++
	}
	if len(out.Skipped) == 0 {
		out.Skipped = nil
	}
	return out, nil
}

// SendInvoice emails the document itself and starts the open tracking for it.
func (s *RegisterService) SendInvoice(ctx context.Context, invoiceID, key string) (ReminderResult, error) {
	if key == "" {
		return ReminderResult{}, httpx.BadRequestf("sending an invoice needs an idempotency key")
	}
	h, err := s.store.InvoiceHeader(ctx, invoiceID)
	if err != nil {
		return ReminderResult{}, err
	}
	block, _, _, err := s.store.CustomerBlockFor(ctx, h.CustomerID)
	if err != nil {
		return ReminderResult{}, err
	}
	if block.ContactEmail == "" {
		return ReminderResult{}, httpx.BadRequestf("%s has no contact email on the account", block.Name)
	}
	caller, _ := s.callerFrom(ctx)
	_, replayed, err := s.store.RecordSend(ctx, SendRow{
		InvoiceID: h.ID, CustomerID: h.CustomerID, InvoiceNo: h.InvoiceNo,
		Kind: "INVOICE", Channel: "EMAIL", ToAddress: block.ContactEmail,
		BalanceCents: h.BalanceCents, IdempotencyKey: key, SentBy: caller.UserID,
	})
	if err != nil {
		return ReminderResult{}, err
	}
	res := ReminderResult{}
	if replayed {
		res.Replayed = 1
		return res, nil
	}
	if s.sender != nil {
		if err := s.sender.SendInvoiceMessage(ctx, block.ContactEmail,
			"Invoice "+h.InvoiceNo, invoiceBody(block.Name, h.InvoiceNo, h.BalanceCents, h.DueDate)); err != nil {
			res.Skipped = map[string]string{h.InvoiceNo: "recorded, but delivery failed: " + err.Error()}
		}
	}
	res.Sent = 1
	return res, nil
}

// MarkOpened records that the customer opened the document we sent them.
//
// THE STAFF PREVIEW NEVER CALLS THIS, and that omission is the feature. If opening the document from
// the register counted as an open, the warning "customer has not opened invoice" would be erased by
// the very act of a clerk checking whether the customer had opened it, a tracking signal that
// destroys itself the moment anybody looks at it. Only the tracked link in the customer's own message
// reaches this method.
func (s *RegisterService) MarkOpened(ctx context.Context, deliveryID string) (bool, error) {
	return s.store.MarkOpened(ctx, deliveryID, s.clock())
}

// LatestSendID is the tracked-link id for the document, when we have sent one.
func (s *RegisterService) LatestSendID(ctx context.Context, invoiceID string) (string, bool, error) {
	return s.store.LatestSendID(ctx, invoiceID)
}

// AddPromise records a commitment to pay.
func (s *RegisterService) AddPromise(ctx context.Context, in PromiseInput) (PromiseView, bool, error) {
	if in.IdempotencyKey == "" {
		return PromiseView{}, false, httpx.BadRequestf("recording a promise needs an idempotency key")
	}
	if in.PromisedCents <= 0 {
		return PromiseView{}, false, httpx.BadRequestf("a promise names an amount greater than zero")
	}
	day, err := time.Parse("2006-01-02", strings.TrimSpace(in.PromisedDate))
	if err != nil {
		return PromiseView{}, false, httpx.BadRequestf("the promised date must be a calendar date (YYYY-MM-DD)")
	}
	if day.Before(truncDay(s.clock())) {
		// A promise for a date already gone is a typo, and accepting it would put a row in the
		// worklist that is broken the moment it is written.
		return PromiseView{}, false, httpx.BadRequestf("the promised date is already past")
	}
	h, err := s.store.InvoiceHeader(ctx, in.InvoiceID)
	if err != nil {
		return PromiseView{}, false, err
	}
	if in.PromisedCents > h.BalanceCents {
		return PromiseView{}, false, httpx.BadRequestf("a promise cannot exceed the outstanding balance")
	}
	caller, _ := s.callerFrom(ctx)
	view, _, conflict, err := s.store.AddPromise(ctx, PromiseWrite{
		InvoiceID: h.ID, CustomerID: h.CustomerID, PromisedCents: in.PromisedCents,
		PromisedDate: day.Format("2006-01-02"), PromisedBy: strings.TrimSpace(in.PromisedBy),
		Note: strings.TrimSpace(in.Note), TakenBy: caller.UserID, IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		return PromiseView{}, false, err
	}
	return view, conflict, nil
}

// OpenDispute flags an invoice the customer is contesting.
func (s *RegisterService) OpenDispute(ctx context.Context, in DisputeInput) (DisputeView, bool, error) {
	if in.IdempotencyKey == "" {
		return DisputeView{}, false, httpx.BadRequestf("opening a dispute needs an idempotency key")
	}
	reason := strings.ToUpper(strings.TrimSpace(in.Reason))
	if !containsString([]string{"SHORT_DELIVERY", "DAMAGED", "PRICING", "NOT_ORDERED", "OTHER"}, reason) {
		return DisputeView{}, false, httpx.BadRequestf("a dispute names a reason: short delivery, damaged, pricing, not ordered, or other")
	}
	h, err := s.store.InvoiceHeader(ctx, in.InvoiceID)
	if err != nil {
		return DisputeView{}, false, err
	}
	if in.AmountCents > h.BalanceCents {
		return DisputeView{}, false, httpx.BadRequestf("the disputed amount cannot exceed the outstanding balance")
	}
	caller, _ := s.callerFrom(ctx)
	view, _, conflict, err := s.store.OpenDispute(ctx, DisputeWrite{
		InvoiceID: h.ID, CustomerID: h.CustomerID, Reason: reason, Note: strings.TrimSpace(in.Note),
		AmountCents: in.AmountCents, RaisedBy: caller.UserID, IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		return DisputeView{}, false, err
	}
	return view, conflict, nil
}

// ResolveDispute closes an argument with a named outcome.
func (s *RegisterService) ResolveDispute(ctx context.Context, invoiceID, outcome string) (bool, error) {
	o := strings.ToUpper(strings.TrimSpace(outcome))
	if !containsString([]string{"PAID", "CREDITED", "WRITTEN_OFF", "WITHDRAWN"}, o) {
		return false, httpx.BadRequestf("closing a dispute names how it ended: paid, credited, written off, or withdrawn")
	}
	caller, _ := s.callerFrom(ctx)
	return s.store.ResolveDispute(ctx, invoiceID, o, caller.UserID)
}

// SetPolicy writes the dealer knobs. The ADMIN GATE IS HERE, in the service, not only on the route:
// the route guard is the coarse first line and the caller is actually resolved here (the settings
// kit's own posture, and the acceptcontrols precedent it cites).
func (s *RegisterService) SetPolicy(ctx context.Context, p RegisterPolicy) error {
	caller, ok := s.callerFrom(ctx)
	if !ok {
		return httpx.BadRequestf("no authenticated staff caller")
	}
	if !caller.IsAdmin() {
		return httpx.BadRequestf("billing: changing dealer screen settings is limited to the General Manager, Financial Controller, HR or Platform Administrator")
	}
	if !containsString([]string{VisibilityManagerUp, VisibilityAllStaff, VisibilityHidden}, p.AccountPositionVisibility) {
		return httpx.BadRequestf("account position visibility must be MANAGER_UP, ALL_STAFF or HIDDEN")
	}
	if p.DueSoonDays < 1 || p.DueSoonDays > 90 {
		return httpx.BadRequestf("the due-soon window is between 1 and 90 days")
	}
	if p.ReminderCooldownHours < 0 || p.ReminderCooldownHours > 720 {
		return httpx.BadRequestf("the reminder cooldown is between 0 and 720 hours")
	}
	if p.WorkspaceSplitPct < 20 || p.WorkspaceSplitPct > 80 {
		return httpx.BadRequestf("the panel split is between 20 and 80 percent")
	}
	if !containsString([]string{"PRIMARY_LEFT", "PRIMARY_RIGHT"}, p.WorkspaceArrangement) {
		return httpx.BadRequestf("the panel arrangement is PRIMARY_LEFT or PRIMARY_RIGHT")
	}
	tones := []string{}
	for _, t := range p.ReminderTones {
		t = strings.ToUpper(strings.TrimSpace(t))
		if !containsString([]string{ToneFriendly, ToneFirm, ToneFinal}, t) {
			return httpx.BadRequestf("a reminder tone is FRIENDLY, FIRM or FINAL")
		}
		if !containsString(tones, t) {
			tones = append(tones, t)
		}
	}
	sort.SliceStable(tones, func(i, j int) bool { return toneRank(tones[i]) < toneRank(tones[j]) })
	p.ReminderTones = tones
	ok2, err := s.store.SetRegisterPolicy(ctx, p)
	if err != nil {
		return err
	}
	if !ok2 {
		return httpx.BadRequestf("no policy row for that branch, it inherits the dealer default; change that instead")
	}
	return nil
}

// toneRank keeps the offered tones in escalation order however an admin listed them: a selector that
// reads FINAL, FRIENDLY, FIRM invites the wrong click.
func toneRank(t string) int {
	switch t {
	case ToneFriendly:
		return 0
	case ToneFirm:
		return 1
	default:
		return 2
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func truncDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// -- statements from a selection ----------------------------------------------------------------

// statementRunner is the shipped statement machinery (statements.go), reached through an interface so
// this file depends on the contract rather than on the construction. The register does NOT compose
// statements of its own: an account statement is one document with one definition, and a second
// generator would eventually send a customer two statements that disagree about the same month.
type statementRunner interface {
	GenerateRun(ctx context.Context, mode, branchID string, asOf time.Time, actorID string, only ...string) (StatementRun, []Statement, error)
}

// WithStatements binds the shipped statement service.
func (s *RegisterService) WithStatements(r statementRunner) *RegisterService {
	s.statements = r
	return s
}

// StatementBatch is what the bulk bar's Create statement produced.
type StatementBatch struct {
	Accounts  int               `json:"accounts"`
	Generated int               `json:"generated"`
	RunIDs    []string          `json:"run_ids"`
	Skipped   map[string]string `json:"skipped,omitempty"`
}

// StatementsForSelection produces ONE INTERIM statement per DISTINCT ACCOUNT in the selection.
//
// PER ACCOUNT, NOT PER INVOICE, and that is the whole reason this method exists rather than the
// screen looping. A statement is an account's position, every open document, aged, so three
// selected invoices belonging to one contractor are ONE statement, and a screen that sent three would
// be sending the same document three times with the same total on it. The register selects
// documents because documents are what an operator can see; the statement is about who owes.
//
// The shipped GenerateRun does the work: same aging buckets, same HTML artefact, same ar_statements
// rows the month-end run writes. This adds the delivery-log row so the account's timeline shows that
// a statement went, which is the one thing the month-end path had no reason to record per invoice.
func (s *RegisterService) StatementsForSelection(ctx context.Context, invoiceIDs []string, key string) (StatementBatch, error) {
	if s.statements == nil {
		return StatementBatch{}, errors.New("billing: statement runs are not wired on this deployment")
	}
	if key == "" {
		return StatementBatch{}, httpx.BadRequestf("creating statements needs an idempotency key")
	}
	if len(invoiceIDs) == 0 {
		return StatementBatch{}, httpx.BadRequestf("choose at least one invoice")
	}
	asOf := s.clock()
	caller, _ := s.callerFrom(ctx)
	out := StatementBatch{Skipped: map[string]string{}}

	seen := map[string]bool{}
	for _, id := range invoiceIDs {
		h, err := s.store.InvoiceHeader(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				out.Skipped[id] = "no such invoice"
				continue
			}
			return StatementBatch{}, err
		}
		if seen[h.CustomerID] {
			continue
		}
		seen[h.CustomerID] = true
		out.Accounts++
		// branchID "", the account's WHOLE position. A statement filtered to one branch's invoices is
		// a number that appears on no other document (customer_ar_read.go's rule, same reason).
		run, statements, err := s.statements.GenerateRun(ctx, RunModeInterim, "", asOf, caller.UserID, h.CustomerID)
		if err != nil {
			out.Skipped[h.InvoiceNo] = err.Error()
			continue
		}
		out.RunIDs = append(out.RunIDs, run.ID)
		out.Generated += len(statements)
		block, _, _, err := s.store.CustomerBlockFor(ctx, h.CustomerID)
		if err != nil {
			return StatementBatch{}, err
		}
		// The send row is best-effort and per account: a statement that generated but whose log row
		// failed is still a statement, and losing the timeline entry is not worth losing the document.
		_, _, _ = s.store.RecordSend(ctx, SendRow{
			InvoiceID: h.ID, CustomerID: h.CustomerID, InvoiceNo: h.InvoiceNo,
			Kind: "STATEMENT", Channel: "EMAIL", ToAddress: block.ContactEmail,
			BalanceCents: h.BalanceCents, IdempotencyKey: key + ":" + h.CustomerID,
			SentBy: caller.UserID,
		})
	}
	if len(out.Skipped) == 0 {
		out.Skipped = nil
	}
	if out.RunIDs == nil {
		out.RunIDs = []string{}
	}
	return out, nil
}
