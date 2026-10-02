package till

import (
	"context"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// tillStore is the persistence seam (mirrors the internal/pricing pricingStore pattern): the service
// holds an interface so unit tests inject a DB-free fake and the integration suite injects the real
// pgx *Repository. The money/state writes that ride INSIDE A-19's sale tx take a pgx.Tx; the drawer
// lifecycle paths own their tx via the pool. Every method reads/writes ONLY till-owned tables.
type tillStore interface {
	// - in-caller-tx writes (the A-19 sale path; capture-before-stock, single tx) -
	GetSessionTx(ctx context.Context, tx pgx.Tx, id string) (TillSession, error)
	FindPaymentByKey(ctx context.Context, tx pgx.Tx, key string) (id, gatewayRef string, found bool, err error)
	InsertPayment(ctx context.Context, tx pgx.Tx, p PaymentRow) (id, paymentNo string, inserted bool, err error)
	InsertAllocation(ctx context.Context, tx pgx.Tx, paymentID, invoiceID string, amountCents int64, allocatedBy string) error
	AllocatedTotal(ctx context.Context, tx pgx.Tx, paymentID string) (int64, error)
	PaymentAmount(ctx context.Context, tx pgx.Tx, paymentID string) (int64, error)

	// InsertMovementTx writes a drawer movement INSIDE the caller's tx (the POS return path, the
	// PAID_OUT cash-refund movement must commit or roll back with the return it belongs to).
	InsertMovementTx(ctx context.Context, tx pgx.Tx, m MovementRow) error

	// - drawer lifecycle (own pool/tx) -
	OpenSession(ctx context.Context, s TillSession, denoms []DenominationCount) (id string, err error)
	GetSession(ctx context.Context, id string) (TillSession, error)
	// FindOpenSession answers the OPEN drawer at a branch/station, when one exists (DIB-488 rev 5:
	// the terminal rediscovers its till on mount instead of holding it in memory only). found=false
	// is an ordinary answer, never an error.
	FindOpenSession(ctx context.Context, branchID, station string) (id string, openedAt time.Time, found bool, err error)
	SessionTenders(ctx context.Context, sessionID string) ([]TenderTotal, error)
	SessionMovements(ctx context.Context, sessionID string) ([]Movement, error)
	InsertMovement(ctx context.Context, m MovementRow) error
	ExpectedTenders(ctx context.Context, sessionID string) (map[string]int64, error)
	TenderActuals(ctx context.Context, sessionID string) (map[string]int64, error)
	MovementCashTotals(ctx context.Context, sessionID string) (MovementTotals, error)
	UpsertTenderActuals(ctx context.Context, sessionID string, actuals []TenderActual) error
	InsertDenominations(ctx context.Context, sessionID, phase string, denoms []DenominationCount) error
	SetCounted(ctx context.Context, sessionID, countedBy string, at time.Time, closingFloatCents int64) error
	// SetCompleted closes the session AND emits till.session.completed (over/short facts) in the SAME tx
	// so the GL over/short journal posts exactly once (W5/A-05 AC9).
	SetCompleted(ctx context.Context, sessionID, branchID, completedBy string, at time.Time, overShortCents, countedCashCents int64, overrideNotes string) error
	Policy(ctx context.Context, branchID string) (TillPolicy, error)

	// - W5 A-05: NSF + credit holds + declined-attempt log -
	// PaymentNSFContext resolves the finance facts of a payment for the NSF decision (non-tx read).
	PaymentNSFContext(ctx context.Context, paymentID string) (customerID, branchID, status, method string, amountCents int64, found bool, err error)
	// NSFPolicyFor reads the branch NSF policy (branch row overrides the org default).
	NSFPolicyFor(ctx context.Context, branchID string) (NSFPolicy, error)
	// MarkNSF performs the full atomic NSF reversal (flip BOUNCED, reverse allocations, reopen invoices,
	// write payment_nsf + customer_transactions, auto-place a credit hold, emit payment.bounced + fee facts).
	// It returns errAlreadyBounced (409) on a replay (in-tx FOR UPDATE status guard).
	MarkNSF(ctx context.Context, in nsfReverseInput) (nsfReverseResult, error)
	// HasActiveHold reports whether the customer currently carries an ACTIVE credit hold.
	HasActiveHold(ctx context.Context, customerID string) (bool, error)
	// ReleaseHold flips one ACTIVE hold to RELEASED (Tier-1); returns the customer id for the audit record.
	ReleaseHold(ctx context.Context, holdID, releasedBy, reason string) (customerID string, err error)
	// ListCreditHolds returns holds filtered by status ("" = all).
	ListCreditHolds(ctx context.Context, status string) ([]CreditHold, error)
	// ListBouncedPayments returns the bounced-payment queue; gatewayRef "" = all, else an exact-ref search.
	ListBouncedPayments(ctx context.Context, gatewayRef string) ([]BouncedPayment, error)
	// UpdateNSFPolicy upserts the org-default (branch "") NSF policy row.
	UpdateNSFPolicy(ctx context.Context, in NSFPolicy) error
	// RecordPaymentAttempt persists one declined/errored gateway attempt (DX-14), idempotent on the key.
	RecordPaymentAttempt(ctx context.Context, in PaymentAttempt) error

	// - Q-044 read-side attribution (payments-owned reads; history.go) -
	// PaymentHistory returns one payment header (who took it, when, how) + its application
	// history (which invoices, by whom, when). found=false when the payment does not exist.
	PaymentHistory(ctx context.Context, paymentID string) (PaymentHistory, bool, error)
	// InvoicePayments returns the payments/credits applied to one invoice, with attribution.
	InvoicePayments(ctx context.Context, invoiceID string) (InvoicePayments, bool, error)
}

// Service is the till domain seam. It owns the payments-row write (the SOLE writer) and the drawer
// lifecycle. The constructor, the With* injectors, and the seam signatures (TakeTender/Allocate) are
// the stable cross-module surface.
type Service struct {
	store      tillStore
	charger    CardCharger
	audit      audit.Recorder
	auditTrail AuditTrailReader // Q-044/M5: read side of the audit seam (bounced-payment history)
	now        func() time.Time
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewService wires the store. Optional deps are injected via the With* methods. callerFrom defaults to
// the identity guard-stamped caller (D-A03-6 additive helper); tests override it via withCaller.
func NewService(store tillStore) *Service {
	return &Service{store: store, now: time.Now, callerFrom: identity.StaffContextFromContext}
}

// WithCardCharger injects RUN-PAY's creds-free card rail (the only way the till charges a CARD tender).
func (s *Service) WithCardCharger(c CardCharger) *Service { s.charger = c; return s }

// WithAudit injects the A-21 Recorder for sensitive-access stamping (the over-threshold override close).
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// WithAuditTrail injects the READ side of the audit seam (satisfied by *audit.Service). PaymentHistory
// uses it to reconstruct a BOUNCED payment's application history after NSF hard-deleted the allocations.
func (s *Service) WithAuditTrail(r AuditTrailReader) *Service { s.auditTrail = r; return s }

// WithClock injects a deterministic clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// withCaller is the test seam for the guard-stamped StaffContext (manager gate + branch scope).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// caller resolves the trusted, server-stamped acting staff member (never the spoofable X-User-Id, C-1).
func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, errUnauthorized
	}
	return c, nil
}

// - TakeTender, the payments-row write (the keystone seam) -

// TakeTender records ONE tender against an open drawer and returns the persisted payment id + the opaque
// gateway_ref (the single source of truth for both, the till is the SOLE payments-row writer). It runs
// INSIDE the caller's tx (A-19's sale tx) so order→tender→invoice→allocate is atomic and payment is
// captured BEFORE stock is issued. Idempotent on in.IdempotencyKey (replay-safe sync).
func (s *Service) TakeTender(ctx context.Context, tx pgx.Tx, in TenderInput) (TenderResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return TenderResult{}, err
	}

	// 1. Idempotency first: a replay returns the SAME payment id + gateway_ref, with NO second charge and
	//    NO second row (the gateway_ref is read back from the existing row so the result stays correct).
	if in.IdempotencyKey != "" {
		if id, gatewayRef, found, ferr := s.store.FindPaymentByKey(ctx, tx, in.IdempotencyKey); ferr != nil {
			return TenderResult{}, ferr
		} else if found {
			return TenderResult{PaymentID: id, GatewayRef: gatewayRef}, nil
		}
	}

	// 2. Validate the session is OPEN, the amount is positive, and the caller can reach the branch.
	if in.AmountCents <= 0 {
		return TenderResult{}, unprocessable("till: amount_cents must be > 0")
	}
	sess, err := s.store.GetSessionTx(ctx, tx, in.TillSessionID)
	if err != nil {
		return TenderResult{}, err
	}
	if sess.Status != StatusOpen {
		return TenderResult{}, clientErr(409, "till: session %s is not OPEN", sess.ID)
	}
	if !caller.CanAccessBranch(sess.BranchID) {
		return TenderResult{}, forbidden("till: caller cannot access branch %s", sess.BranchID)
	}

	row := PaymentRow{
		Method:         in.Method,
		AmountCents:    in.AmountCents,
		Status:         PaymentReceived,
		BranchID:       sess.BranchID,
		TillSessionID:  sess.ID,
		CustomerID:     in.CustomerID,
		ReceivedBy:     caller.UserID,
		IdempotencyKey: in.IdempotencyKey,
		SignatureRef:   in.SignatureRef,
	}

	// 3. By method.
	switch in.Method {
	case MethodCash, MethodCheque, MethodEFT, MethodOther:
		// Offline-first: write directly, no processor, works fully offline.
		row.OfflineCaptured = in.Offline
	case MethodCard:
		if in.Offline {
			// D-A03-3 default: card needs connectivity → BLOCK at the counter (cash/cheque work offline).
			// (The queue-later flip writes status=PENDING here, no schema change, when D-A03-3 changes.)
			return TenderResult{}, errOffline
		}
		if s.charger == nil {
			return TenderResult{}, clientErr(503, "till: card rail not configured")
		}
		res, cerr := s.charger.Charge(ctx, in.AmountCents, in.PaymentToken, in.IdempotencyKey)
		// Capture-before-stock: the charge completes BEFORE we persist a RECEIVED row / return success.
		if cerr != nil || res.Status == ChargeFailed {
			// No RECEIVED row is committed for a failed/declined charge; the drawer is unaffected.
			return TenderResult{}, fmt.Errorf("%w: %v", errCardDeclined, cerr)
		}
		row.GatewayRef = res.ProcessorRef
		row.CardType = in.CardType // the 0027 column; "" → NULL for non-card (never reached here)
	default:
		return TenderResult{}, unprocessable("till: unknown tender method %q", in.Method)
	}

	// 4. Persist (idempotent: ON CONFLICT (idempotency_key) DO NOTHING RETURNING; on conflict, the
	//    existing id is returned and no second row is written). The gateway_ref on the result is the same
	//    value persisted to payments.gateway_ref (the processor ref for CARD; "" for cash/cheque).
	id, paymentNo, inserted, err := s.store.InsertPayment(ctx, tx, row)
	if err != nil {
		return TenderResult{}, err
	}
	// Q-044: the explicit "payment received" record is NOT written here. Mid-tx recording orphaned an
	// audit row when the caller's tx rolled back AND duplicated it on a rollback-retry (the fresh
	// insert reported inserted=true again). The caller invokes RecordPaymentReceived with this result
	// AFTER its tx commits; Inserted gates the record to exactly-once per committed tender.
	return TenderResult{
		PaymentID: id, GatewayRef: row.GatewayRef,
		PaymentNo: paymentNo, Method: in.Method, AmountCents: in.AmountCents,
		BranchID: sess.BranchID, Inserted: inserted,
	}, nil
}

// RecordPaymentReceived writes the Q-044 PAYMENT_RECEIVED business record for ONE committed tender -
// WHO took it, method, amount, payment_no. It is the single post-commit hook: callers MUST invoke it
// only after the money tx enclosing TakeTender has committed (pos does so next to its ActSaleCompleted
// record). Exactly-once: a replayed tender carries Inserted=false and records nothing; a rolled-back
// tx never reaches this call, so no orphan receipt row can exist. Best-effort like the other Q-044
// records, the money tables stay the source of truth.
func (s *Service) RecordPaymentReceived(ctx context.Context, res TenderResult) {
	if !res.Inserted || res.PaymentID == "" || s.audit == nil {
		return
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return
	}
	_ = s.audit.Record(ctx, audit.SensitiveAccess{
		Action: audit.ActPaymentReceived, EntityType: "payment", EntityID: res.PaymentID,
		ActorID: caller.UserID, BranchID: res.BranchID, Purpose: "PAYMENT_RECEIVED",
		Extra: map[string]any{"method": res.Method, "amount_cents": res.AmountCents, "payment_no": res.PaymentNo},
	})
}

// RefundTender records ONE refund against the drawer/card rail and returns the persisted REFUNDED
// payments-row id (the till is the SOLE writer of payments + till_movements, the POS return composes
// through this seam, never the tables). It runs INSIDE the caller's tx (the pos return tx) so the
// refund row + drawer movement commit or roll back with the return. Idempotent on in.IdempotencyKey.
//
//   - CASH: requires an OPEN session (whose branch must match in.BranchID when given); writes the
//     REFUNDED payments row + a PAID_OUT movement "RETURN <ref>" so expected drawer cash drops.
//   - CARD: no drawer effect; writes the REFUNDED payments row carrying the reversal's gateway ref
//     (the processor call already happened upstream through RUN-PAY, creds never enter the till).
func (s *Service) RefundTender(ctx context.Context, tx pgx.Tx, in RefundInput) (RefundResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return RefundResult{}, err
	}
	if in.IdempotencyKey != "" {
		if id, _, found, ferr := s.store.FindPaymentByKey(ctx, tx, in.IdempotencyKey); ferr != nil {
			return RefundResult{}, ferr
		} else if found {
			return RefundResult{PaymentID: id}, nil
		}
	}
	if in.AmountCents <= 0 {
		return RefundResult{}, unprocessable("till: refund amount_cents must be > 0")
	}

	row := PaymentRow{
		Method:         in.Method,
		AmountCents:    in.AmountCents, // positive; status REFUNDED conveys direction (0010 CHECK)
		Status:         PaymentRefunded,
		BranchID:       in.BranchID,
		CustomerID:     in.CustomerID,
		ReceivedBy:     caller.UserID,
		IdempotencyKey: in.IdempotencyKey,
	}

	switch in.Method {
	case MethodCash:
		sess, err := s.store.GetSessionTx(ctx, tx, in.TillSessionID)
		if err != nil {
			return RefundResult{}, err
		}
		if sess.Status != StatusOpen {
			return RefundResult{}, clientErr(409, "till: cash refunds require an OPEN session (%s is %s)", sess.ID, sess.Status)
		}
		if in.BranchID != "" && in.BranchID != sess.BranchID {
			return RefundResult{}, clientErr(409, "till: session %s belongs to another branch", sess.ID)
		}
		if !caller.CanAccessBranch(sess.BranchID) {
			return RefundResult{}, forbidden("till: caller cannot access branch %s", sess.BranchID)
		}
		row.BranchID = sess.BranchID
		row.TillSessionID = sess.ID
	case MethodCard:
		if in.BranchID == "" {
			return RefundResult{}, unprocessable("till: card refund needs a branch_id")
		}
		if !caller.CanAccessBranch(in.BranchID) {
			return RefundResult{}, forbidden("till: caller cannot access branch %s", in.BranchID)
		}
		row.GatewayRef = in.GatewayRef
	default:
		return RefundResult{}, unprocessable("till: refund method must be CASH or CARD (account credit never touches the till)")
	}

	id, _, inserted, err := s.store.InsertPayment(ctx, tx, row)
	if err != nil {
		return RefundResult{}, err
	}
	// The drawer movement follows the payments row and ONLY on a first insert (a replay must not
	// double-reduce expected cash).
	if in.Method == MethodCash && inserted {
		if err := s.store.InsertMovementTx(ctx, tx, MovementRow{
			TillSessionID: row.TillSessionID,
			MovementType:  MovementPaidOut,
			Method:        MethodCash,
			AmountCents:   in.AmountCents,
			Reason:        "RETURN " + in.Reference,
			Reference:     in.Reference,
			CreatedBy:     caller.UserID,
		}); err != nil {
			return RefundResult{}, err
		}
	}
	return RefundResult{PaymentID: id}, nil
}

// Allocate writes a payment_allocations row in the caller's tx; the sum of allocations for a payment
// never exceeds its amount_cents (A-03 owns the table; A-19 calls the seam after creating the invoice).
// The authenticated caller is stamped as allocated_by (Q-044 attribution).
func (s *Service) Allocate(ctx context.Context, tx pgx.Tx, paymentID, invoiceID string, amountCents int64) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if amountCents <= 0 {
		return unprocessable("till: allocation amount must be > 0")
	}
	amt, err := s.store.PaymentAmount(ctx, tx, paymentID)
	if err != nil {
		return err
	}
	allocated, err := s.store.AllocatedTotal(ctx, tx, paymentID)
	if err != nil {
		return err
	}
	if allocated+amountCents > amt {
		return unprocessable("till: allocation %d exceeds payment remaining %d", amountCents, amt-allocated)
	}
	if err := s.store.InsertAllocation(ctx, tx, paymentID, invoiceID, amountCents, caller.UserID); err != nil {
		return err
	}
	// Q-044: explicit "payment allocated" record (entity = payment, so the trail is queryable per
	// payment via GET /api/v1/audit/transaction/payment/{id}). Best-effort like ActPaymentReceived.
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActPaymentAllocated, EntityType: "payment", EntityID: paymentID,
			ActorID: caller.UserID, Purpose: "PAYMENT_ALLOCATED",
			Extra: map[string]any{"invoice_id": invoiceID, "amount_cents": amountCents},
		})
	}
	return nil
}

// - drawer lifecycle -

// OpenTill declares the float (+ optional OPEN denominations) and opens a drawer session.
func (s *Service) OpenTill(ctx context.Context, in OpenTillInput) (sessionID string, err error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return "", err
	}
	if !caller.CanAccessBranch(in.BranchID) {
		return "", forbidden("till: caller cannot access branch %s", in.BranchID)
	}
	if in.FloatCents < 0 {
		return "", unprocessable("till: float_cents must be >= 0")
	}
	if len(in.OpeningDenominations) > 0 {
		if sum := denomTotal(in.OpeningDenominations); sum != in.FloatCents {
			return "", unprocessable("till: opening denomination total %d must equal float %d", sum, in.FloatCents)
		}
	}
	station := in.Station
	if station == "" {
		station = "MAIN"
	}
	sess := TillSession{
		BranchID:   in.BranchID,
		Station:    station,
		Status:     StatusOpen,
		OpenedBy:   caller.UserID,
		FloatCents: in.FloatCents,
		BlindClose: true,
	}
	return s.store.OpenSession(ctx, sess, in.OpeningDenominations)
}

// OpenSessionLookup reports the OPEN drawer at a branch/station, when there is one, DIB-488
// rev 5. The POS chrome held tillSessionId in memory only, so a mid-shift reload (or a duplicated
// surface) showed "Open Session" and rang 409 while the drawer WAS open; this read lets a mount
// rediscover it. Same branch fence as OpenTill; "no open drawer" is an ordinary answer (found
// false), never an error, a closed till is a state, not a failure.
func (s *Service) OpenSessionLookup(ctx context.Context, branchID, station string) (id string, openedAt time.Time, found bool, err error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return "", time.Time{}, false, err
	}
	if !caller.CanAccessBranch(branchID) {
		return "", time.Time{}, false, forbidden("till: caller cannot access branch %s", branchID)
	}
	if station == "" {
		station = "MAIN"
	}
	return s.store.FindOpenSession(ctx, branchID, station)
}

// PaidIn records a paid-in movement (top-up change), raises expected cash.
func (s *Service) PaidIn(ctx context.Context, in MovementInput) error {
	return s.recordMovement(ctx, in, MovementPaidIn)
}

// PaidOut records a paid-out / drop movement (drop to safe, petty cash), reduces expected cash.
func (s *Service) PaidOut(ctx context.Context, in MovementInput) error {
	mt := in.MovementType
	if mt != MovementPaidOut && mt != MovementDrop {
		mt = MovementPaidOut
	}
	return s.recordMovement(ctx, in, mt)
}

// Lodge records a lodgement (banking) movement against the session.
func (s *Service) Lodge(ctx context.Context, in MovementInput) error {
	return s.recordMovement(ctx, in, MovementLodgement)
}

func (s *Service) recordMovement(ctx context.Context, in MovementInput, mt string) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if in.AmountCents <= 0 {
		return unprocessable("till: movement amount_cents must be > 0")
	}
	sess, err := s.store.GetSession(ctx, in.TillSessionID)
	if err != nil {
		return err
	}
	if sess.Status != StatusOpen {
		return clientErr(409, "till: movements require an OPEN session")
	}
	if !caller.CanAccessBranch(sess.BranchID) {
		return forbidden("till: caller cannot access branch %s", sess.BranchID)
	}
	method := in.Method
	if method == "" {
		method = MethodCash
	}
	return s.store.InsertMovement(ctx, MovementRow{
		TillSessionID: sess.ID,
		MovementType:  mt,
		Method:        method,
		AmountCents:   in.AmountCents,
		Reason:        in.Reason,
		Reference:     in.Reference,
		CreatedBy:     caller.UserID,
	})
}

// CountTill records the BLIND per-tender actuals (+ optional CLOSE denominations) and transitions the
// session OPEN -> COUNTED. The caller's response carries NO expected figures (blind close).
func (s *Service) CountTill(ctx context.Context, in CountInput) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	sess, err := s.store.GetSession(ctx, in.TillSessionID)
	if err != nil {
		return err
	}
	if sess.Status != StatusOpen {
		return clientErr(409, "till: only an OPEN session can be counted")
	}
	if !caller.CanAccessBranch(sess.BranchID) {
		return forbidden("till: caller cannot access branch %s", sess.BranchID)
	}
	// Optional closing denomination tally must equal the counted CASH.
	if len(in.ClosingDenominations) > 0 {
		var cashActual int64
		for _, t := range in.Tenders {
			if t.Method == MethodCash {
				cashActual = t.ActualCents
			}
		}
		if sum := denomTotal(in.ClosingDenominations); sum != cashActual {
			return unprocessable("till: closing denomination total %d must equal counted cash %d", sum, cashActual)
		}
	}
	if err := s.store.UpsertTenderActuals(ctx, sess.ID, in.Tenders); err != nil {
		return err
	}
	if len(in.ClosingDenominations) > 0 {
		if err := s.store.InsertDenominations(ctx, sess.ID, "CLOSE", in.ClosingDenominations); err != nil {
			return err
		}
	}
	return s.store.SetCounted(ctx, sess.ID, caller.UserID, s.now(), in.ClosingFloatCents)
}

// ZReportFor returns expected vs actual vs over/short, DERIVED with the start float in the cash math
// (never a stored per-tender column). Revealed AFTER the count.
func (s *Service) ZReportFor(ctx context.Context, sessionID string) (ZReport, error) {
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return ZReport{}, err
	}
	return s.deriveZReport(ctx, sess)
}

// deriveZReport is the single source of the over/short math, reused by ZReportFor and CompleteTill.
func (s *Service) deriveZReport(ctx context.Context, sess TillSession) (ZReport, error) {
	expected, err := s.store.ExpectedTenders(ctx, sess.ID)
	if err != nil {
		return ZReport{}, err
	}
	actuals, err := s.store.TenderActuals(ctx, sess.ID)
	if err != nil {
		return ZReport{}, err
	}
	mv, err := s.store.MovementCashTotals(ctx, sess.ID)
	if err != nil {
		return ZReport{}, err
	}

	// Cash reconciliation, start float IS in the math.
	expectedCash := sess.FloatCents + expected[MethodCash] + mv.PaidInCash - mv.PaidOutCash - mv.LodgementCash
	countedCash := actuals[MethodCash]

	// Per-tender variance, derived. Cash and cheque are physically (blind) counted; card/EFT/OTHER are
	// processor-confirmed captures, so their actual = expected captures (variance 0), D-A03-9.
	var perTender []TenderTotal
	for _, method := range []string{MethodCash, MethodCheque, MethodCard, MethodEFT, MethodOther} {
		exp, hasExp := expected[method]
		act, hasAct := actuals[method]
		if !hasExp && !hasAct {
			continue
		}
		switch method {
		case MethodCash:
			// The per-tender CASH line states the SAME expected the summary above derives (start
			// float + movements in) against the blind count, so one answer carries ONE expected
			// cash and this variance equals the summary's OverShortCents by construction. The raw
			// RECEIVED payments sum is not the expected drawer cash: a refund books a REFUNDED
			// payments row (excluded from that sum) plus a cash PAID_OUT movement.
			exp = expectedCash
			act = countedCash
		case MethodCheque:
			// Physically counted (blind) like cash, but no float and no movements accrue to
			// cheques (the float and drawer movements are cash-only), so expected stays the
			// RECEIVED payments sum: the asymmetry with CASH is deliberate, not an oversight.
		default:
			act = exp // processor-confirmed captures (D-A03-9)
		}
		perTender = append(perTender, TenderTotal{
			Method:        method,
			ExpectedCents: exp,
			ActualCents:   act,
			VarianceCents: act - exp,
		})
	}

	return ZReport{
		SessionID: sess.ID,
		PerTender: perTender,
		Cash: CashSummary{
			OpeningFloatCents: sess.FloatCents,
			ExpectedCents:     expectedCash,
			CountedCents:      countedCash,
			OverShortCents:    countedCash - expectedCash,
		},
		ClosingFloatCents: sess.ClosingFloatCents,
	}, nil
}

// CompleteTill reconciles + signs off (COUNTED -> CLOSED). The conditional manager gate reads the
// branch till_policy threshold: |over/short| > threshold requires a manager, with a logged override.
func (s *Service) CompleteTill(ctx context.Context, in CompleteInput) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	sess, err := s.store.GetSession(ctx, in.TillSessionID)
	if err != nil {
		return err
	}
	// Lifecycle guard: only a COUNTED session can be completed (OPEN → 409; re-completing CLOSED → 409).
	if sess.Status != StatusCounted {
		return clientErr(409, "till: session %s is %s, not COUNTED", sess.ID, sess.Status)
	}
	if !caller.CanAccessBranch(sess.BranchID) {
		return forbidden("till: caller cannot access branch %s", sess.BranchID)
	}

	z, err := s.deriveZReport(ctx, sess)
	if err != nil {
		return err
	}
	policy, err := s.store.Policy(ctx, sess.BranchID)
	if err != nil {
		return err
	}

	overThreshold := abs64(z.Cash.OverShortCents) > policy.VarianceThresholdCents
	if overThreshold {
		if !caller.IsManager() {
			// Stays COUNTED; a manager must sign off this variance.
			return forbidden("manager sign-off required for a $%.2f variance", float64(abs64(z.Cash.OverShortCents))/100)
		}
		if in.OverrideNotes == "" {
			return unprocessable("till: override_notes required for an over-threshold close")
		}
	}

	if err := s.store.SetCompleted(ctx, sess.ID, sess.BranchID, caller.UserID, s.now(), z.Cash.OverShortCents, z.Cash.CountedCents, in.OverrideNotes); err != nil {
		return err
	}

	// Audit the over-threshold sign-off (actor, variance, reason). The HTTP request itself is recorded
	// by the A-21 middleware; this is the sensitive-access record for the override.
	if overThreshold && s.audit != nil {
		if err := s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     audit.ActTillOverride,
			EntityType: "till.session",
			EntityID:   sess.ID,
			ActorID:    caller.UserID,
			BranchID:   sess.BranchID,
			Reason:     in.OverrideNotes,
			Extra:      map[string]any{"over_short_cents": fmt.Sprintf("%d", z.Cash.OverShortCents)},
		}); err != nil {
			return err
		}
	}
	return nil
}

// GetSession returns a session with its tenders + movements (the operator reopen/lookup read).
func (s *Service) GetSession(ctx context.Context, id string) (SessionDetail, error) {
	if _, err := s.caller(ctx); err != nil {
		return SessionDetail{}, err
	}
	sess, err := s.store.GetSession(ctx, id)
	if err != nil {
		return SessionDetail{}, err
	}
	tenders, err := s.store.SessionTenders(ctx, id)
	if err != nil {
		return SessionDetail{}, err
	}
	movements, err := s.store.SessionMovements(ctx, id)
	if err != nil {
		return SessionDetail{}, err
	}
	return SessionDetail{TillSession: sess, Tenders: tenders, Movements: movements}, nil
}

// Policy returns the branch variance threshold + close grain.
func (s *Service) Policy(ctx context.Context, branchID string) (TillPolicy, error) {
	return s.store.Policy(ctx, branchID)
}

// - W5 A-05: NSF handling + credit holds + declined-attempt log -

// ActCreditHoldRelease is the sensitive-access action verb for a Tier-1 credit-hold release. The audit
// Recorder accepts any action string; this keeps the release accountable (actor + reason) in audit_log.
const ActCreditHoldRelease = "CREDIT_HOLD_RELEASE"

// MarkNSF records a bounced cheque/payment: it resolves the runtime NSF policy for the payment's branch,
// then atomically reverses the payment (flip BOUNCED, reverse allocations, reopen invoices), captures the
// fee, auto-places a credit hold per policy, and emits payment.bounced + the fee journal facts in ONE tx.
// A payment already BOUNCED replays to 409 (idempotent). It never writes a reversing tax_decision_snapshot
// (the sale still happened; only the cash failed, the invoice reopens, the tax liability is unchanged).
func (s *Service) MarkNSF(ctx context.Context, in NSFInput) (NSFResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return NSFResult{}, err
	}
	if in.PaymentID == "" {
		return NSFResult{}, unprocessable("till: payment_id is required")
	}
	_, branchID, status, _, _, found, err := s.store.PaymentNSFContext(ctx, in.PaymentID)
	if err != nil {
		return NSFResult{}, err
	}
	if !found {
		return NSFResult{}, errNotFound
	}
	if !caller.CanAccessBranch(branchID) {
		return NSFResult{}, forbidden("till: caller cannot access branch %s", branchID)
	}
	// Fast idempotent guard (the store re-checks under FOR UPDATE to close the race).
	if status == "BOUNCED" {
		return NSFResult{}, errAlreadyBounced
	}
	policy, err := s.store.NSFPolicyFor(ctx, branchID)
	if err != nil {
		return NSFResult{}, err
	}
	res, err := s.store.MarkNSF(ctx, nsfReverseInput{
		PaymentID:      in.PaymentID,
		Reason:         in.Reason,
		ActorID:        caller.UserID,
		FeeCents:       policy.FeeCents,
		FeeTaxable:     policy.FeeTaxable,
		FeeAccountCode: policy.FeeAccountCode,
		AutoHold:       policy.AutoHold,
	})
	if err != nil {
		return NSFResult{}, err
	}
	return NSFResult{
		PaymentID:        in.PaymentID,
		CustomerID:       res.CustomerID,
		FeeCents:         policy.FeeCents,
		FeeTaxable:       policy.FeeTaxable,
		ReversedCents:    res.ReversedCents,
		ReopenedInvoices: res.ReopenedInvoices,
		HoldPlaced:       res.HoldPlaced,
		Status:           "BOUNCED",
	}, nil
}

// ReleaseHold releases one ACTIVE credit hold. It is Tier-1 (leadership) ONLY with a MANDATORY reason, and
// records a sensitive-access audit row (actor + reason). The HTTP route is additionally requireAdmin-gated.
func (s *Service) ReleaseHold(ctx context.Context, in ReleaseHoldInput) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if caller.Tier != identity.TierLeadership {
		return forbidden("till: Tier-1 leadership is required to release a credit hold")
	}
	if in.HoldID == "" {
		return unprocessable("till: hold id is required")
	}
	if in.Reason == "" {
		return unprocessable("till: a release reason is required")
	}
	if _, err := s.store.ReleaseHold(ctx, in.HoldID, caller.UserID, in.Reason); err != nil {
		return err
	}
	if s.audit != nil {
		if err := s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     ActCreditHoldRelease,
			EntityType: "credit.hold",
			EntityID:   in.HoldID,
			ActorID:    caller.UserID,
			Reason:     in.Reason,
			Extra:      map[string]any{"status": HoldReleased},
		}); err != nil {
			return err
		}
	}
	return nil
}

// ListCreditHolds returns credit holds filtered by status ("" = all) for the AR console.
func (s *Service) ListCreditHolds(ctx context.Context, status string) ([]CreditHold, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	return s.store.ListCreditHolds(ctx, status)
}

// ListBouncedPayments returns the bounced-payment queue; gatewayRef "" = all, else an exact-ref search.
func (s *Service) ListBouncedPayments(ctx context.Context, gatewayRef string) ([]BouncedPayment, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	return s.store.ListBouncedPayments(ctx, gatewayRef)
}

// GetNSFPolicy returns the effective NSF policy for a branch (branch row overrides the org default).
func (s *Service) GetNSFPolicy(ctx context.Context, branchID string) (NSFPolicy, error) {
	if _, err := s.caller(ctx); err != nil {
		return NSFPolicy{}, err
	}
	return s.store.NSFPolicyFor(ctx, branchID)
}

// UpdateNSFPolicy upserts the org-default NSF policy (fee/taxable/account/auto-hold). Manager-gated route.
func (s *Service) UpdateNSFPolicy(ctx context.Context, in NSFPolicy) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if !caller.IsManager() {
		return forbidden("till: manager rights are required to edit the NSF policy")
	}
	if in.FeeCents < 0 {
		return unprocessable("till: fee_cents must be >= 0")
	}
	if in.FeeAccountCode == "" {
		in.FeeAccountCode = "4200"
	}
	return s.store.UpdateNSFPolicy(ctx, in)
}

// RecordPaymentAttempt persists one declined/errored gateway attempt (DX-14). Idempotent on the key. The
// actor is stamped server-side from the authenticated caller (never trusted from the request body); the other
// caller-supplied fields (customer/branch/amount) are out of scope for this remediation and left as-is.
func (s *Service) RecordPaymentAttempt(ctx context.Context, in PaymentAttempt) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	in.AttemptedBy = caller.UserID // server-stamped; overrides anything in the body
	if in.AmountCents <= 0 {
		return unprocessable("till: attempt amount_cents must be > 0")
	}
	if in.Result != "DECLINED" && in.Result != "ERROR" {
		return unprocessable("till: attempt result must be DECLINED or ERROR")
	}
	if in.Method == "" {
		in.Method = MethodCard
	}
	return s.store.RecordPaymentAttempt(ctx, in)
}

// denomTotal sums a denomination breakdown (denomination_cents × quantity).
func denomTotal(ds []DenominationCount) int64 {
	var sum int64
	for _, d := range ds {
		sum += d.DenominationCents * int64(d.Quantity)
	}
	return sum
}
