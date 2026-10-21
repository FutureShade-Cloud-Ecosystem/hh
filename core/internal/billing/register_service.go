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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
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
	return registerStatusIn(houseday.DefaultZone(), v, asOf, dueSoonDays)
}

// registerStatusIn is registerStatus in a named dealer's zone: "past due" is judged on the dealer's
// own calendar day.
func registerStatusIn(zone houseday.Zone, v RegisterRow, asOf time.Time, dueSoonDays int) (string, int) {
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
	days := DaysPastDueIn(zone, v.DueDate, asOf)
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

// daysBetween is asOf − due in whole days on the house day (see DaysPastDue for the two meanings of
// its arguments). Positive means past due.
func daysBetween(due, asOf time.Time) int { return DaysPastDue(due, asOf) }

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
	SelectionTotals(ctx context.Context, invoiceIDs []string, reach BranchReach) (int64, int, error)
	InvoiceHeader(ctx context.Context, invoiceID string) (InvoiceHeaderFacts, error)
	DetailLines(ctx context.Context, invoiceID string) ([]DetailLine, error)
	AppliedCents(ctx context.Context, invoiceID string) (int64, error)
	CustomerBlockFor(ctx context.Context, customerID string) (CustomerBlock, bool, bool, error)
	AccountPositionFor(ctx context.Context, asOf time.Time, customerID string) (AccountPosition, error)
	ProofFor(ctx context.Context, orderID string) (Proof, bool, error)
	OrderFactsFor(ctx context.Context, orderID string) (string, string, string, error)
	ActivityFor(ctx context.Context, invoiceID, orderID string, withPayments bool) ([]ActivityEntry, error)
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
	// ApplyReceived is the one received payment the server allocates across invoices (the money
	// seam's own walk, oldest first, one transaction), the replacement for the app splitting a
	// cheque into per invoice slices.
	ApplyReceived(ctx context.Context, in ApplyReceivedCommand) (payments.ApplyAnswer, error)
}

// ApplyReceivedCommand is the register's request to the money seam's received payment walk.
type ApplyReceivedCommand struct {
	CustomerID     string
	Method         string
	Reference      string
	IdempotencyKey string
	InvoiceIDs     []string
	AmountCents    int64
	Preview        bool
	// BranchIDs is the caller's branch reach as the service resolves it: nil for a global caller,
	// the caller's branches otherwise. The seam answers an invoice outside it as not found.
	BranchIDs  []string
	ReceivedBy string
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
	reapplied  reappliedReader
	sender     Sender
	statements statementRunner
	clock      func() time.Time
	callerFrom func(context.Context) (identity.StaffContext, bool)
	zone       houseday.Source // the dealer's business day zone; nil = the Ontario dealer's
}

// WithZone binds the dealer's business day zone source, so "the promised date is already past" is
// judged against the dealer's own calendar day. Nil keeps the Ontario dealer's.
func (s *RegisterService) WithZone(src houseday.Source) *RegisterService { s.zone = src; return s }

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

// ReappliedSlice is one slice of a reversed payment's held money applied to an invoice (SF-2r): what the invoice's
// Applied figure and its timeline add to the allocations the settle wrote.
type ReappliedSlice struct {
	PaymentNo   string
	Method      string
	AmountCents int64
	AppliedAt   time.Time
	AppliedBy   string // users.id, "" when none
}

// reappliedReader is the payments module's read of re-applied slices, reached through an interface (the module owns
// the table; the census refuses a direct read).
type reappliedReader interface {
	ReappliedToInvoice(ctx context.Context, invoiceID string) ([]ReappliedSlice, error)
}

// WithReapplied binds the re-applied slices read. Nil leaves the register reading allocations alone.
func (s *RegisterService) WithReapplied(r reappliedReader) *RegisterService {
	s.reapplied = r
	return s
}

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
	reach, err := s.reach(ctx).narrow(f.BranchID)
	if err != nil {
		return RegisterPage{}, err
	}
	f.Reach = reach
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
	return s.store.SelectionTotals(ctx, invoiceIDs, s.reach(ctx))
}

// reach is the caller's branches, see branch_reach.go.
func (s *RegisterService) reach(ctx context.Context) BranchReach {
	c, ok := s.callerFrom(ctx)
	if !ok {
		return AllBranches
	}
	return reachOfCaller(c)
}

// header reads one invoice's header inside the caller's reach. An invoice in another branch is not
// found, the same answer as one that does not exist: a distinguishable refusal would be an existence
// oracle for another branch's documents. Every by id read and act goes through here.
func (s *RegisterService) header(ctx context.Context, invoiceID string) (InvoiceHeaderFacts, error) {
	h, err := s.store.InvoiceHeader(ctx, invoiceID)
	if err != nil {
		return InvoiceHeaderFacts{}, err
	}
	if !s.reach(ctx).Allows(h.BranchID) {
		return InvoiceHeaderFacts{}, ErrNotFound
	}
	return h, nil
}

// Detail assembles the right-hand panel. withPayments says whether the timeline carries the PAYMENT
// entries (payment number, method, amount and who applied it): they are the payment trail ruling 87
// holds to money desk WORK, so the caller who asks for them is one the payment trail reader (below)
// admitted. Without it the entries are absent, not blanked, and the rest of the timeline is unchanged.
func (s *RegisterService) Detail(ctx context.Context, invoiceID string, withPayments bool) (InvoiceDetail, error) {
	asOf := s.clock()
	h, err := s.header(ctx, invoiceID)
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
	// Re-application writes no allocation, so the payments module's own read adds the slices of a reversed payment's
	// held money applied to this invoice: the Applied figure and the timeline both carry them (SF-2r).
	var slices []ReappliedSlice
	if s.reapplied != nil {
		if slices, err = s.reapplied.ReappliedToInvoice(ctx, invoiceID); err != nil {
			return InvoiceDetail{}, err
		}
		for _, sl := range slices {
			applied += sl.AmountCents
		}
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
	activity, err := s.store.ActivityFor(ctx, invoiceID, h.OrderID, withPayments)
	if err != nil {
		return InvoiceDetail{}, err
	}
	if withPayments {
		activity = withReappliedEntries(activity, slices)
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
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return InvoiceDetail{}, err
	}
	d.Status, d.DaysOverdue = registerStatusIn(zone, RegisterRow{
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
	// The branch door first: a payment against another branch's invoice is not found before the money
	// seam is asked anything about it.
	h, err := s.header(ctx, in.InvoiceID)
	if err != nil {
		return ReceiptResult{}, err
	}
	payable, customerID, err := s.money.PayableCentsFor(ctx, in.InvoiceID)
	if err != nil {
		return ReceiptResult{}, err
	}
	if in.AmountCents > payable {
		return ReceiptResult{}, httpx.BadRequestf("this invoice can take at most %d cents right now; refresh and retry", payable)
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

// ApplyReceivedPayment records ONE received payment (amount, method, reference) against a list of
// the account's open invoices and lets the money seam's own walk split it oldest first. The caller
// names the money and the invoices; the server computes the split, in one transaction, so no part
// of a cheque is ever held in a browser. A preview answers the same split without writing.
//
// The refusal words an operator reads (the ceiling, the not open document, the invoice that is not
// the account's) are the seam's; this method adds only the caller's facts: who took the payment and
// which branches the caller reaches.
func (s *RegisterService) ApplyReceivedPayment(ctx context.Context, in ApplyPaymentInput) (payments.ApplyAnswer, error) {
	if s.money == nil {
		return payments.ApplyAnswer{}, errors.New("billing: recording payments is not wired on this deployment")
	}
	if in.IdempotencyKey == "" {
		return payments.ApplyAnswer{}, httpx.BadRequestf("recording a payment needs an idempotency key")
	}
	// The amount and the invoice list are judged by the money seam itself (validateApply), not
	// here: the seam answers the replay judgement first (FP 186 and 187), so a spent key retried
	// with a zero amount or an empty list hears the replay verdict, never a validation word that
	// would tell the desk the payment was not recorded.
	caller, _ := s.callerFrom(ctx)
	res, err := s.money.ApplyReceived(ctx, ApplyReceivedCommand{
		CustomerID:     in.CustomerID,
		Method:         strings.ToUpper(strings.TrimSpace(in.Method)),
		Reference:      in.Reference,
		IdempotencyKey: in.IdempotencyKey,
		InvoiceIDs:     in.InvoiceIDs,
		AmountCents:    in.AmountCents,
		Preview:        in.Preview,
		BranchIDs:      s.reach(ctx).IDs,
		ReceivedBy:     caller.UserID,
	})
	if err != nil {
		return payments.ApplyAnswer{}, err
	}
	// A promise the close kept stops being a promise, exactly as the per invoice settle treats one:
	// only on a genuinely new posting, never on a replay (its rows were settled the first time).
	if !res.Replayed && !res.Preview {
		for _, line := range res.Lines {
			if line.Outcome == payments.ApplyOutcomeCloses {
				_, _ = s.store.SettlePromise(ctx, line.InvoiceID, "KEPT", "paid in full")
			}
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
		h, err := s.header(ctx, id)
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
	h, err := s.header(ctx, invoiceID)
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
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return PromiseView{}, false, err
	}
	if today, _ := time.Parse("2006-01-02", zone.Today(s.clock)); day.Before(today) {
		// A promise for a date already gone is a typo, and accepting it would put a row in the
		// worklist that is broken the moment it is written.
		return PromiseView{}, false, httpx.BadRequestf("the promised date is already past")
	}
	h, err := s.header(ctx, in.InvoiceID)
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
	h, err := s.header(ctx, in.InvoiceID)
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
	// The branch door first: resolving is a per invoice act like every other, and the true or false
	// answer for an invoice in another branch would say whether it has an open dispute.
	if _, err := s.header(ctx, invoiceID); err != nil {
		return false, err
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
		// The gate (caller.IsAdmin()) admits the four Tier-1 GLOBAL roles; the words here mirror
		// the settings kit's NotAdminError so the till can carry one refusal phrase across both
		// surfaces. The caller's role code never reaches the words; it stays on the staff context
		// for the audit.
		return httpx.BadRequestf("billing: only the General Manager, the Financial Controller, HR or the Platform Administrator change the dealer screen settings")
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
		h, err := s.header(ctx, id)
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

// reappliedDetail is the words of a re-applied slice's timeline entry: the reversed payment it came from and how.
const reappliedDetail = "re applied from a reversed payment"

// withReappliedEntries adds one PAYMENT entry per re-applied slice to the timeline and keeps it newest first and at
// the same forty rows the read is bounded to, so a slice is as visible as any applied entry beside it.
func withReappliedEntries(activity []ActivityEntry, slices []ReappliedSlice) []ActivityEntry {
	if len(slices) == 0 {
		return activity
	}
	out := append([]ActivityEntry(nil), activity...)
	for _, sl := range slices {
		out = append(out, ActivityEntry{Kind: "PAYMENT", At: sl.AppliedAt, Detail: sl.PaymentNo + " · " + sl.Method + " · " + reappliedDetail, AmountCents: sl.AmountCents})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}
