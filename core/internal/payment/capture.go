package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// The card capture and compensation entry points (design sections 3.3 to 3.5), over A1's attempt store.
//
// Capture is the three committed steps of a charge with no transaction held anywhere: claim the leg (a
// pool transaction), dispatch to the processor on a context detached from the request, stamp the answer
// (a pool write). Compensate is the guarded CAPTURED to VOIDING move, then the void, with the refund as
// the fallback. Neither holds a pool connection across a processor call or asks for a second connection
// while holding one (decision log row 786), and neither is ever asked to run inside the sale's
// transaction: the sale only binds, through BindTx, on its own.
//
// The existing ChargeTx and Charge entry points are unchanged; the till and pos move off them in packet B.

// Bounds of the detached contexts the work runs on. Exported so the sweep's grace window (packet C) can
// be tested to exceed them: a request that is still inside these bounds can never be raced by the sweep.
const (
	// CaptureDispatchDeadline bounds Capture after the claim: the config read, the processor call and
	// the stamp. It exceeds the live client's own request timeout.
	CaptureDispatchDeadline = 45 * time.Second
	// CompensationDeadline bounds Compensate: the claim of the reversal, the void, the refund fallback
	// and the stamps. It covers two processor requests.
	CompensationDeadline = 90 * time.Second
)

// Result kinds.
const (
	// CaptureKindCaptured: this request owned the leg and the processor approved it.
	CaptureKindCaptured = "CAPTURED"
	// CaptureKindBound: the leg already belongs to a committed sale; the caller answers that sale.
	CaptureKindBound = "BOUND"

	// CompensateKindVoided: no money remains (the row is VOIDED, by void or by refund).
	CompensateKindVoided = "VOIDED"
	// CompensateKindCommitted: the sale's commit landed after all (the row is BOUND): the caller
	// answers the sale and voids nothing.
	CompensateKindCommitted = "COMMITTED"
	// CompensateKindNeedsManager: the customer's money is, or may be, held (VOID_DECLINED or
	// VOID_UNKNOWN, or a reversal already in flight): a person has to look.
	CompensateKindNeedsManager = "NEEDS_MANAGER"
)

// The typed answers of Capture. Each is returned with the attempt row in the CaptureResult, so the
// caller can name the attempt's short reference. pos maps them in packet B; no HTTP code is decided here.
var (
	// ErrCaptureInFlight: the leg's charge is claimed or captured and not yet bound (a retry or a
	// concurrent submit). Keep the key and ring again shortly.
	ErrCaptureInFlight = errors.New("payment: the card charge for this leg is in flight")
	// ErrCaptureDeclined: the processor declined the leg's card. No money moved.
	ErrCaptureDeclined = errors.New("payment: the card was declined")
	// ErrCaptureReversed: the leg was captured and then reversed. No money remains.
	ErrCaptureReversed = errors.New("payment: the card charge was reversed")
	// ErrCaptureNeedsManager: the leg has no usable answer, or its reversal is unresolved: a manager
	// has to close it before the card is charged again.
	ErrCaptureNeedsManager = errors.New("payment: the card charge needs a manager")
	// ErrCaptureResolved: a manager closed the leg's attempt; its key never charges again.
	ErrCaptureResolved = errors.New("payment: the card charge was resolved by a manager")
	// ErrCaptureNotDispatched: nothing reached the processor (no config, a missing credential, a
	// refused key refresh). No money moved; the row is closed as DECLINED with the cause kept.
	ErrCaptureNotDispatched = errors.New("payment: the card charge was not dispatched")
	// ErrCaptureAnswerUnrecorded: a processor answer that moved money could not be written to its row.
	// The processor ref is in the error text and the error log; a person has to reconcile it.
	ErrCaptureAnswerUnrecorded = errors.New("payment: a processor answer could not be recorded")
	// ErrCompensateNotCaptured: the attempt holds no captured charge to reverse.
	ErrCompensateNotCaptured = errors.New("payment: the attempt holds no captured charge to reverse")
	// ErrAttemptNotCaptured: BindTx matched no CAPTURED row; the sweep or the compensator took the
	// attempt (or it is already bound), so the sale's transaction must fail.
	ErrAttemptNotCaptured = errors.New("payment: the attempt is no longer captured")
)

// CaptureInput is one card leg. AccountToken is the client-tokenized instrument: it goes to the
// processor and nowhere else, never persisted and never logged.
type CaptureInput struct {
	LegKey, SaleKey                               string
	LegSeq                                        int
	AmountCents                                   int64
	BranchID, TillSessionID, CustomerID, CardType string
	CreatedBy                                     string // optional: the staff user the attempt is recorded for
	AccountToken                                  string
}

// CaptureResult carries the attempt row the answer was written to or read from, on every return
// (errors included) once a row exists.
type CaptureResult struct {
	Kind    string
	Attempt Attempt
}

// CompensateResult carries the attempt row after the compensation.
type CompensateResult struct {
	Kind    string
	Attempt Attempt
}

// getByID reads an attempt by its id; ok is false when there is none.
func (s *Store) getByID(ctx context.Context, id string) (Attempt, bool, error) {
	if s.pool == nil {
		return Attempt{}, false, errAttemptsNoDB
	}
	a, err := scanAttempt(s.pool.QueryRow(ctx, `SELECT `+attemptColumns+` FROM card_charge_attempts WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, false, nil
	}
	return a, err == nil, err
}

// StampLateCapture records a capture whose answer arrived after its row left CHARGING (the sweep moved
// it to UNKNOWN, or a manager closed it as RESOLVED while the call was out): UNKNOWN or RESOLVED to
// VOIDING in ONE guarded statement, with the processor ref the answer carried. The ref is kept (the
// table's cca_money_has_ref check holds), and the capture is reversed by the caller straight away: the
// customer's money moved for a leg nobody will bind. It is the smallest safe path: no new status, no
// widened guard on StampCaptured, and the same guarded row arbitrates between it and a manager's resolve.
func (s *Store) StampLateCapture(ctx context.Context, id, ref, respCode string) (Attempt, bool, error) {
	if id == "" || ref == "" {
		return Attempt{}, false, errBadStamp
	}
	return s.poolMove(ctx, id, []string{AttemptUnknown, AttemptResolved},
		`status = 'VOIDING', processor_ref = $3, resp_code = $4, last_error = $5`,
		ref, respCode, SanitizeLastError("capture answered after the attempt left CHARGING, reversing it"))
}

// Capture charges one card leg at most once, with no database transaction held (design 3.3 steps 1 to 4).
//
//  1. Claim (a committed pool transaction). If this request does not own the leg, the answer is read from
//     the existing row (design 3.4) and the processor is NOT called.
//  2. Dispatch on a context detached from the request (context.WithoutCancel with the bounded
//     CaptureDispatchDeadline), so a client hang-up cannot lose an answer. The active config is read on
//     the pool (no transaction is held now); the attempt id goes out as the per dispatch key
//     (ChargeInput.IdempotencyKey, so creds["__idem"] is no longer empty) and the leg key as Reference.
//     The rotation write-back keeps its ledger and flusher carrier.
//  3. Stamp (a committed pool write): CAPTURED with the ref, DECLINED, or UNKNOWN when the outcome cannot
//     be told (ErrProcessorOutcomeUnknown, or any error that is not an explicit decline).
//
// The returned error carries the typed answer; see the Err values above. A CAPTURED result is the only
// success: its attempt id and ref go to the sale, which binds them with BindTx.
func (s *Service) Capture(ctx context.Context, in CaptureInput) (CaptureResult, error) {
	if s.attempts == nil {
		return CaptureResult{}, errAttemptsNoDB
	}
	if in.AccountToken == "" || in.AmountCents <= 0 {
		return CaptureResult{}, errBadCharge
	}
	att, created, err := s.attempts.Claim(ctx, ClaimInput{
		LegKey: in.LegKey, SaleKey: in.SaleKey, LegSeq: in.LegSeq, AmountCents: in.AmountCents,
		BranchID: in.BranchID, TillSessionID: in.TillSessionID, CustomerID: in.CustomerID,
		CardType: in.CardType, Processor: s.proc.Name(), CreatedBy: in.CreatedBy,
	})
	if err != nil {
		return CaptureResult{}, err // ErrTillUnresolved when the till guard refused the claim
	}
	if !created {
		return answerFromRow(att)
	}
	return s.dispatchCapture(ctx, att, in)
}

// answerFromRow is design 3.4: the answer for a leg this request does not own, read from its row.
func answerFromRow(a Attempt) (CaptureResult, error) {
	res := CaptureResult{Attempt: a}
	switch a.Status {
	case AttemptBound:
		res.Kind = CaptureKindBound
		return res, nil
	case AttemptCharging, AttemptCaptured:
		return res, ErrCaptureInFlight
	case AttemptDeclined:
		return res, ErrCaptureDeclined
	case AttemptVoided:
		return res, ErrCaptureReversed
	case AttemptUnknown, AttemptVoiding, AttemptVoidUnknown, AttemptVoidDeclined:
		return res, ErrCaptureNeedsManager
	case AttemptResolved:
		return res, ErrCaptureResolved
	}
	return res, fmt.Errorf("payment: attempt %s has the unexpected status %q", a.ID, a.Status)
}

// dispatchCapture is steps 2 and 3, for a request that owns the claimed row a.
func (s *Service) dispatchCapture(ctx context.Context, a Attempt, in CaptureInput) (CaptureResult, error) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CaptureDispatchDeadline)
	defer cancel()

	cfg, creds, err := s.cfg.activeConfig(dctx)
	if err != nil {
		// Nothing was sent. No money moved, so the row is closed as a refusal and the cause kept.
		return s.stampNoMoney(dctx, a, "", "not dispatched: "+err.Error(), fmt.Errorf("%w: %w", ErrCaptureNotDispatched, err))
	}
	rcpt, chargeErr := s.charge(dctx, cfg, s.freshestCreds(cfg.Processor, creds), ChargeInput{
		AmountCents: in.AmountCents, PaymentToken: in.AccountToken, Reference: in.LegKey,
		IdempotencyKey: a.ID, CardType: in.CardType,
	}, s.carryRotationOutOfTheTransaction)

	var decline *DeclineError
	switch {
	case chargeErr == nil && rcpt.Status == "captured" && rcpt.ProcessorRef != "":
		return s.stampCaptured(dctx, a, rcpt.ProcessorRef)
	case chargeErr == nil:
		// Approved with no ref, or with a status that is not a capture: money may have moved and the
		// row cannot name it. Held for a person.
		return s.stampUnknown(dctx, a, fmt.Sprintf("approved without a capture ref (status %q)", rcpt.Status))
	case errors.As(chargeErr, &decline):
		return s.stampNoMoney(dctx, a, decline.RespCode, chargeErr.Error(), ErrCaptureDeclined)
	case errors.Is(chargeErr, ErrProcessorNotDispatched):
		return s.stampNoMoney(dctx, a, "", chargeErr.Error(), fmt.Errorf("%w: %w", ErrCaptureNotDispatched, chargeErr))
	default:
		// ErrProcessorOutcomeUnknown, and any error nobody classified: never assume no money moved.
		return s.stampUnknown(dctx, a, chargeErr.Error())
	}
}

// stampNoMoney closes the row as DECLINED (no money moved) and answers with answerErr. If the row is no
// longer CHARGING (the sweep took it) the answer is read from the row instead.
func (s *Service) stampNoMoney(ctx context.Context, a Attempt, respCode, why string, answerErr error) (CaptureResult, error) {
	row, ok, err := s.attempts.StampDeclined(ctx, a.ID, respCode, why)
	if err != nil {
		slog.Error("payment: a declined card charge could not be recorded; the sweep will move the attempt",
			"attempt", a.ID, "err", err)
		return CaptureResult{Attempt: a}, fmt.Errorf("%w: %w", answerErr, err)
	}
	if !ok {
		return s.answerFromCurrentRow(ctx, a)
	}
	return CaptureResult{Attempt: row}, answerErr
}

// stampUnknown holds the attempt for a person: UNKNOWN, with the diagnostic kept.
func (s *Service) stampUnknown(ctx context.Context, a Attempt, why string) (CaptureResult, error) {
	row, ok, err := s.attempts.StampUnknown(ctx, a.ID, why)
	if err != nil {
		slog.Error("payment: an unknown card charge outcome could not be recorded; the sweep will move the attempt",
			"attempt", a.ID, "err", err)
		return CaptureResult{Attempt: a}, fmt.Errorf("%w: %w", ErrCaptureNeedsManager, err)
	}
	if !ok {
		return s.answerFromCurrentRow(ctx, a)
	}
	return CaptureResult{Attempt: row}, ErrCaptureNeedsManager
}

func (s *Service) answerFromCurrentRow(ctx context.Context, a Attempt) (CaptureResult, error) {
	cur, found, err := s.attempts.getByID(ctx, a.ID)
	if err != nil || !found {
		return CaptureResult{Attempt: a}, fmt.Errorf("%w: attempt %s could not be re-read: %v", ErrCaptureNeedsManager, a.ID, err)
	}
	return answerFromRow(cur)
}

// stampCaptured records an approval. If the stamp matches no row the late answer is handled, never
// dropped: a captured ref must not be lost.
func (s *Service) stampCaptured(ctx context.Context, a Attempt, ref string) (CaptureResult, error) {
	row, ok, err := s.attempts.StampCaptured(ctx, a.ID, ref, "")
	if err != nil {
		return CaptureResult{Attempt: a}, s.unrecordedAnswer(a, ref, "capture", err)
	}
	if ok {
		return CaptureResult{Kind: CaptureKindCaptured, Attempt: row}, nil
	}
	return s.lateCapture(ctx, a, ref)
}

// lateCapture is the answer that arrived after the row left CHARGING (A1 guards StampCaptured from
// CHARGING only, so the stamp matched nothing). The money moved and nobody will bind it, so the capture
// is voided here: the row goes UNKNOWN or RESOLVED to VOIDING with the ref recorded (StampLateCapture),
// and the compensation runs from there. A row in any other status cannot take the ref and the capture is
// surfaced loudly instead.
func (s *Service) lateCapture(ctx context.Context, a Attempt, ref string) (CaptureResult, error) {
	row, ok, err := s.attempts.StampLateCapture(ctx, a.ID, ref, "")
	if err != nil {
		return CaptureResult{Attempt: a}, s.unrecordedAnswer(a, ref, "late capture", err)
	}
	if !ok {
		return CaptureResult{Attempt: a}, s.unrecordedAnswer(a, ref, "late capture", errors.New("the attempt is in neither UNKNOWN nor RESOLVED"))
	}
	slog.Error("payment: a card capture answered after its attempt left CHARGING; reversing it",
		"attempt", a.ID, "ref", ref, "was", "late capture")
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CompensationDeadline)
	defer cancel()
	cres, err := s.reverse(rctx, row)
	res := CaptureResult{Attempt: cres.Attempt}
	if err != nil {
		return res, err
	}
	if cres.Kind == CompensateKindVoided {
		return res, ErrCaptureReversed
	}
	return res, ErrCaptureNeedsManager
}

// unrecordedAnswer logs, at error level, a processor answer that moved money and could not be written,
// and returns the typed error with the ref in its text.
func (s *Service) unrecordedAnswer(a Attempt, ref, what string, cause error) error {
	slog.Error("payment: a processor answer that moved money could not be recorded",
		"attempt", a.ID, "ref", ref, "what", what, "err", cause)
	return fmt.Errorf("%w: attempt %s, %s ref %s: %v", ErrCaptureAnswerUnrecorded, a.ID, what, ref, cause)
}

// Compensate reverses one captured leg (design 3.3 step 6), on a context detached from the request: a
// client hang-up never abandons a reversal.
//
// CAPTURED to VOIDING is the guarded move that decides who owns the capture. If it matches nothing and the
// row reads BOUND, the sale's commit landed after all (a lost acknowledgement): the result is
// CompensateKindCommitted and nothing is voided, so the caller answers the sale. A VOIDED row answers
// VOIDED with no call, a reversal already in flight or unresolved answers NEEDS_MANAGER with no call, and
// any other row is ErrCompensateNotCaptured. Otherwise the void is sent; on a decline the refund is
// sent; a lost answer at either step stamps VOID_UNKNOWN and a reversal is never retried blindly.
func (s *Service) Compensate(ctx context.Context, attemptID string) (CompensateResult, error) {
	if s.attempts == nil {
		return CompensateResult{}, errAttemptsNoDB
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CompensationDeadline)
	defer cancel()
	row, moved, err := s.attempts.BeginVoiding(dctx, attemptID)
	if err != nil {
		return CompensateResult{}, err
	}
	if moved {
		return s.reverse(dctx, row)
	}
	cur, found, err := s.attempts.getByID(dctx, attemptID)
	if err != nil {
		return CompensateResult{}, err
	}
	if !found {
		return CompensateResult{}, fmt.Errorf("payment: card attempt %s not found", attemptID)
	}
	res := CompensateResult{Attempt: cur}
	switch cur.Status {
	case AttemptBound:
		res.Kind = CompensateKindCommitted
		return res, nil
	case AttemptVoided:
		res.Kind = CompensateKindVoided
		return res, nil
	case AttemptVoiding, AttemptVoidUnknown, AttemptVoidDeclined:
		res.Kind = CompensateKindNeedsManager
		return res, nil
	}
	return res, fmt.Errorf("%w: attempt %s is %s", ErrCompensateNotCaptured, attemptID, cur.Status)
}

// reversalOutcome reads one reversal call.
type reversalOutcome int

const (
	reversalApproved reversalOutcome = iota
	reversalRefused                  // no reversal happened (a decline, or nothing was sent)
	reversalLost                     // the answer is unknown: a reversal may or may not have happened
)

func classifyReversal(res RefundResult, err error) reversalOutcome {
	switch {
	case err == nil && res.ProcessorRef != "":
		return reversalApproved
	case err == nil:
		return reversalLost // approved with no ref: it cannot be recorded as VOIDED
	case errors.Is(err, ErrProcessorDeclined), errors.Is(err, ErrProcessorNotDispatched), errors.Is(err, ErrNotImplemented):
		return reversalRefused
	default:
		return reversalLost
	}
}

// reverse runs the void and its refund fallback for a row this call just moved to VOIDING.
func (s *Service) reverse(ctx context.Context, a Attempt) (CompensateResult, error) {
	cfg, creds, err := s.cfg.activeConfig(ctx)
	if err != nil {
		// Nothing was sent, and the money is held: stop at VOID_UNKNOWN so a person looks.
		return s.finishReversal(ctx, a, func() (Attempt, bool, error) {
			return s.attempts.StampVoidUnknown(ctx, a.ID, "reversal not dispatched: "+err.Error())
		}, "")
	}
	creds = s.freshestCreds(cfg.Processor, creds)

	vres, verr := s.callReversal(ctx, cfg, creds, "void:"+a.ID, a, s.proc.Void)
	switch classifyReversal(vres, verr) {
	case reversalApproved:
		return s.finishReversal(ctx, a, func() (Attempt, bool, error) {
			return s.attempts.StampVoided(ctx, a.ID, ReversalVoid, vres.ProcessorRef)
		}, vres.ProcessorRef)
	case reversalLost:
		return s.finishReversal(ctx, a, func() (Attempt, bool, error) {
			return s.attempts.StampVoidUnknown(ctx, a.ID, "void: "+errText(verr))
		}, vres.ProcessorRef)
	}

	// The void was refused: the refund is the fallback.
	rres, rerr := s.callReversal(ctx, cfg, creds, "refund:"+a.ID, a, s.proc.Refund)
	switch classifyReversal(rres, rerr) {
	case reversalApproved:
		return s.finishReversal(ctx, a, func() (Attempt, bool, error) {
			return s.attempts.StampVoided(ctx, a.ID, ReversalRefund, rres.ProcessorRef)
		}, rres.ProcessorRef)
	case reversalLost:
		return s.finishReversal(ctx, a, func() (Attempt, bool, error) {
			return s.attempts.StampVoidUnknown(ctx, a.ID, "refund: "+errText(rerr))
		}, rres.ProcessorRef)
	}
	return s.finishReversal(ctx, a, func() (Attempt, bool, error) {
		return s.attempts.StampVoidDeclined(ctx, a.ID, "void: "+errText(verr)+"; refund: "+errText(rerr))
	}, "")
}

func errText(err error) string {
	if err == nil {
		return "approved without a ref"
	}
	return err.Error()
}

// callReversal dispatches one reversal call with its per dispatch key and carries a key rotation to the
// ledger, exactly as charge does.
func (s *Service) callReversal(ctx context.Context, cfg activeCfg, creds map[string]string, key string, a Attempt,
	call func(context.Context, map[string]string, string, string, int64) (RefundResult, error)) (RefundResult, error) {
	prevKey := creds["api_key"]
	creds["__idem"] = key // call-scoped; not persisted
	res, err := call(ctx, creds, cfg.MerchantID, a.ProcessorRef, a.AmountCents)
	if creds["api_key"] != prevKey && creds["api_key"] != "" {
		_ = s.carryRotationOutOfTheTransaction(ctx, cfg.Processor, creds)
	}
	return res, err
}

// finishReversal runs the stamp of a reversal's outcome and shapes the result. ref is the reversal's
// processor ref when the call returned one, kept for the log if the stamp cannot land.
func (s *Service) finishReversal(_ context.Context, a Attempt, stamp func() (Attempt, bool, error), ref string) (CompensateResult, error) {
	row, ok, err := stamp()
	if err != nil || !ok {
		cause := err
		if cause == nil {
			cause = errors.New("the attempt left VOIDING before the answer was recorded")
		}
		slog.Error("payment: a card reversal's outcome could not be recorded",
			"attempt", a.ID, "charge_ref", a.ProcessorRef, "reversal_ref", ref, "err", cause)
		return CompensateResult{Attempt: a}, fmt.Errorf("%w: attempt %s, charge ref %s, reversal ref %q: %v",
			ErrCaptureAnswerUnrecorded, a.ID, a.ProcessorRef, ref, cause)
	}
	if row.Status == AttemptVoided {
		return CompensateResult{Kind: CompensateKindVoided, Attempt: row}, nil
	}
	return CompensateResult{Kind: CompensateKindNeedsManager, Attempt: row}, nil
}

// BindTx binds a captured leg to the payments row the sale's own transaction created: CAPTURED to BOUND on
// tx, so the bind commits or rolls back with the sale. It returns ErrAttemptNotCaptured when the guard
// matches nothing (the sweep or the compensator took the attempt, or it is already bound): the sale's
// transaction must then fail. The row lock the UPDATE takes makes a concurrent compensator wait for the
// sale's commit and then lose.
func (s *Service) BindTx(ctx context.Context, tx pgx.Tx, attemptID, paymentID string) error {
	if s.attempts == nil {
		return errAttemptsNoDB
	}
	_, ok, err := s.attempts.BindTx(ctx, tx, attemptID, paymentID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAttemptNotCaptured
	}
	return nil
}
