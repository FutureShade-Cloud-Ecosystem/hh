// staff_decision.go, the staff doors that record the customer's decision on a quote.
//
// THE GAP. A customer often accepts by phone or by text. Until now only the customer's own link moved
// a quote from SENT to ACCEPTED, so the salesperson who had the answer in their ear had no way to
// write it down and had to ask the customer to go and click. This file is the staff door: the
// salesperson opens the quote, says how the customer told them, and the quote moves.
//
// WHO. The same people who may convert: the one predicate identity.CanSellOnCredit, called, never
// copied, so the three doors (accept, reject, convert) cannot drift apart. Branch reach is the staff
// quote writes' rule. There is no ownership rung: convert has none either, and a manager must be able
// to close a quote a colleague was handling.
//
// WHAT IS RECORDED. The status moves under the quote row lock, and in the same transaction two audit
// rows land: the QUOTE_ACCEPTED (or QUOTE_REJECTED) row, which says staff did it on the customer's
// behalf, by which channel, with what note, and what staff attested; and the QUOTE_CHANGED status row
// the quote's change history projects. The Idempotency-Key rides the allowlisted client_id on the first
// row, the same carrier the customer's keyed accept uses (share_idempotency.go).
package quote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// The channels a customer can tell staff through. A closed vocabulary so the history reads the same
// way for every salesperson and a report can count them.
const (
	DecisionChannelPhone    = "phone"
	DecisionChannelText     = "text"
	DecisionChannelEmail    = "email"
	DecisionChannelInPerson = "in_person"
)

var decisionChannels = map[string]bool{
	DecisionChannelPhone: true, DecisionChannelText: true, DecisionChannelEmail: true, DecisionChannelInPerson: true,
}

// maxDecisionNote bounds the free text. It is the staff member's own sentence, not a document.
const maxDecisionNote = 500

// decisionRefusedAction is the phrase the shared not-a-seller refusal uses at these doors.
const decisionRefusedAction = "recording a customer's decision on a quote"

// DecisionRefusedError is a staff decision refused because of the quote's state, in words. It is a 409:
// the person is allowed, the quote is not in a state this can happen from.
type DecisionRefusedError struct{ Msg string }

func (e *DecisionRefusedError) Error() string { return e.Msg }

// StaffAcceptIn is the staff accept payload. QuantitiesConfirmed is the staff member's statement that
// the customer confirmed the quantities, the same acknowledgment the customer's page asks for; it must
// be true, because an acceptance without it is not one the customer's own page would take.
type StaffAcceptIn struct {
	Channel             string
	Note                string
	QuantitiesConfirmed bool
	IdempotencyKey      string
}

// StaffRejectIn is the staff reject payload: the customer said no. The reason is a code from a closed
// list so lost quotes can be counted; the note carries the detail, and is required for "other".
type StaffRejectIn struct {
	Channel        string
	ReasonCode     string
	Note           string
	IdempotencyKey string
}

// The reasons a customer gives for declining.
var declineReasons = map[string]bool{
	"price": true, "competitor": true, "project_cancelled": true, "timing": true, "no_response": true, "other": true,
}

// StaffDecisionResult is the outcome of a staff accept or reject.
type StaffDecisionResult struct {
	QuoteID        string    `json:"quote_id"`
	QuoteNo        string    `json:"quote_no"`
	Status         string    `json:"status"`
	PreviousStatus string    `json:"previous_status"`
	Channel        string    `json:"channel,omitempty"`
	OnBehalf       bool      `json:"on_behalf"`
	DecidedAt      time.Time `json:"decided_at"`
	// Replayed is true when this answer is the first outcome of an earlier request with the same key;
	// nothing was written by this call.
	Replayed bool `json:"replayed"`
}

// decisionSpec is one decision, graded, ready for the shared path.
type decisionSpec struct {
	action     string // the audit action
	to         string // the status it moves the quote to
	channel    string
	note       string
	reasonCode string
	qtyOK      bool
	key        string
}

// decisionWrite is what the guarded write needs; decisionHooks is what must commit with it.
type decisionWrite struct {
	QuoteID string
	To      string
	Action  string
	Channel string
	Key     string
	ActorID string
	Now     time.Time
}

type decisionHooks struct {
	// record writes the audit rows on the write's own transaction; prev is the status before.
	record func(ctx context.Context, tx pgx.Tx, prev, quoteNo string) error
	// release closes the quote's soft stock holds on the same transaction. Set only by a reject, and
	// nil when the reservation seam is unwired.
	release func(ctx context.Context, tx pgx.Tx) error
}

// decisionOutcome is what the guarded write answers.
type decisionOutcome struct {
	QuoteNo   string
	Prev      string
	Status    string
	Channel   string
	DecidedAt time.Time
	Replayed  bool
}

// decisionStore is asserted OFF the injected staffStore, the module's established pattern, so every
// existing fake keeps compiling; a store without it answers ErrEditNotConfigured.
type decisionStore interface {
	DecideQuoteGuarded(ctx context.Context, w decisionWrite, hooks decisionHooks) (decisionOutcome, error)
}

// normalizeDecisionChannel is space and case forgiving ("In person" is not an error worth a 400) and
// names the legal answers when it refuses.
func normalizeDecisionChannel(v string) (string, error) {
	s := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(v)), " ", "_"), "-", "_")
	if !decisionChannels[s] {
		return "", httpx.BadRequestf("channel must be phone, text, email or in_person: how did the customer tell you?")
	}
	return s, nil
}

func normalizeDecisionNote(v string) (string, error) {
	s := strings.TrimSpace(v)
	if len(s) > maxDecisionNote {
		return "", httpx.BadRequestf("note is too long, keep it under %d characters", maxDecisionNote)
	}
	return s, nil
}

// AcceptOnBehalf moves a quote to ACCEPTED because the customer said yes some way other than the link.
//
// SENT is the normal source. DRAFT is allowed too, with one or more lines: a customer who agrees to
// what the salesperson read out has accepted a quote that was never sent, and refusing it would send
// the salesperson back to mint a link only to be told to ignore it. An empty draft is refused, there
// is nothing to have accepted. Expired, already accepted, converted and lost quotes are refused in
// words.
func (s *StaffService) AcceptOnBehalf(ctx context.Context, idOrNo string, in StaffAcceptIn) (StaffDecisionResult, error) {
	caller, err := s.decisionCaller(ctx)
	if err != nil {
		return StaffDecisionResult{}, err
	}
	sp := decisionSpec{action: audit.ActQuoteAccepted, to: "ACCEPTED", qtyOK: in.QuantitiesConfirmed}
	if sp.channel, err = normalizeDecisionChannel(in.Channel); err != nil {
		return StaffDecisionResult{}, err
	}
	if !in.QuantitiesConfirmed {
		return StaffDecisionResult{}, httpx.BadRequestf(
			"confirm that the customer confirmed the quantities before the quote is marked accepted")
	}
	if sp.note, err = normalizeDecisionNote(in.Note); err != nil {
		return StaffDecisionResult{}, err
	}
	if sp.key, err = normalizeIdempotencyKey(in.IdempotencyKey); err != nil {
		return StaffDecisionResult{}, err
	}
	return s.decide(ctx, caller, idOrNo, sp)
}

// RejectOnBehalf moves a quote to LOST because the customer said no some way other than the portal.
// The refusals are the accept door's: expired, already accepted, converted and already lost are all
// refused in words, and an accepted quote is never quietly turned into a lost one. Its soft stock holds
// are released on the same transaction, the reserve.go lifecycle for a quote that is lost. The
// follow-ups stop, as they do for an accept.
func (s *StaffService) RejectOnBehalf(ctx context.Context, idOrNo string, in StaffRejectIn) (StaffDecisionResult, error) {
	caller, err := s.decisionCaller(ctx)
	if err != nil {
		return StaffDecisionResult{}, err
	}
	sp := decisionSpec{action: audit.ActQuoteRejected, to: "LOST"}
	if sp.channel, err = normalizeDecisionChannel(in.Channel); err != nil {
		return StaffDecisionResult{}, err
	}
	sp.reasonCode = strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(in.ReasonCode)), " ", "_"), "-", "_")
	if !declineReasons[sp.reasonCode] {
		return StaffDecisionResult{}, httpx.BadRequestf(
			"reason_code must be price, competitor, project_cancelled, timing, no_response or other")
	}
	if sp.note, err = normalizeDecisionNote(in.Note); err != nil {
		return StaffDecisionResult{}, err
	}
	if sp.reasonCode == "other" && sp.note == "" {
		return StaffDecisionResult{}, httpx.BadRequestf("add a note saying why when the reason is other")
	}
	if sp.key, err = normalizeIdempotencyKey(in.IdempotencyKey); err != nil {
		return StaffDecisionResult{}, err
	}
	return s.decide(ctx, caller, idOrNo, sp)
}

// decisionCaller is the guard the doors share, in the convert door's order: authenticated, active,
// then a selling role by the one predicate.
func (s *StaffService) decisionCaller(ctx context.Context) (identity.StaffContext, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, err
	}
	if caller.Status != identity.StatusActive {
		return identity.StaffContext{}, ErrForbidden
	}
	if !identity.CanSellOnCredit(caller) {
		return identity.StaffContext{}, &NotSellingRoleError{RoleCode: caller.RoleCode, Action: decisionRefusedAction}
	}
	return caller, nil
}

// decide resolves the quote branch-scoped, proves the caller reaches its branch, and hands the status
// move to the guarded write. Status and expiry are judged there, under the row lock, once: judging
// them here as well would be a check-then-write window and a second opinion about one fact.
func (s *StaffService) decide(ctx context.Context, caller identity.StaffContext, idOrNo string, sp decisionSpec) (StaffDecisionResult, error) {
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err := s.store.ResolveQuoteID(ctx, idOrNo, rGlobal, rBranches)
	if err != nil {
		return StaffDecisionResult{}, err
	}
	ds, ok := s.store.(decisionStore)
	if !ok {
		return StaffDecisionResult{}, ErrEditNotConfigured
	}
	_, branchID, _, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return StaffDecisionResult{}, err
	}
	if !caller.CanAccessBranch(branchID) {
		return StaffDecisionResult{}, ErrForbidden
	}
	if sp.key != "" && s.auditTx == nil {
		return StaffDecisionResult{}, errors.New("quote decision: an idempotency key needs the audit trail")
	}
	out, err := ds.DecideQuoteGuarded(ctx, decisionWrite{
		QuoteID: quoteID, To: sp.to, Action: sp.action, Channel: sp.channel, Key: sp.key,
		ActorID: caller.UserID, Now: s.now(),
	}, s.decisionHooks(caller, quoteID, sp))
	if err != nil {
		return StaffDecisionResult{}, err
	}
	return StaffDecisionResult{
		QuoteID: quoteID, QuoteNo: out.QuoteNo, Status: out.Status, PreviousStatus: out.Prev,
		Channel: out.Channel, OnBehalf: true, DecidedAt: out.DecidedAt, Replayed: out.Replayed,
	}, nil
}

// decisionHooks is what commits with the move: the audit rows always, and for a reject the release of
// the quote's soft holds when the reservation seam is wired.
func (s *StaffService) decisionHooks(caller identity.StaffContext, quoteID string, sp decisionSpec) decisionHooks {
	h := decisionHooks{record: s.decisionAuditHook(caller, quoteID, sp)}
	if sp.to == "LOST" && s.stock != nil {
		h.release = func(ctx context.Context, tx pgx.Tx) error {
			_, err := s.stock.ReleaseBySourceTx(ctx, tx, ResSourceQuote, quoteID, ResReleased)
			return err
		}
	}
	return h
}

// decisionAuditHook writes the two rows on the decision's own transaction: the decision row, carrying
// everything staff said, and the QUOTE_CHANGED status row the change history shows. A failure rolls
// the status move back with it.
func (s *StaffService) decisionAuditHook(caller identity.StaffContext, quoteID string, sp decisionSpec) func(context.Context, pgx.Tx, string, string) error {
	return func(ctx context.Context, tx pgx.Tx, prev, _ string) error {
		if s.auditTx == nil {
			return nil
		}
		extra := map[string]any{
			"field": "status", "before": prev, "after": sp.to,
			"channel": sp.channel, "on_behalf": true, "actor_role": caller.RoleCode,
		}
		if sp.qtyOK {
			extra["quantities_confirmed"] = true
		}
		if sp.reasonCode != "" {
			extra["decline_reason"] = sp.reasonCode
		}
		if sp.note != "" {
			extra["note"] = sp.note
		}
		if sp.key != "" {
			extra["client_id"] = sp.key
		}
		if err := s.auditTx.RecordTx(ctx, tx, audit.SensitiveAccess{
			Action: sp.action, EntityType: "quote", EntityID: quoteID, ActorID: caller.UserID,
			Purpose: decisionPurpose, Reason: "status", Extra: extra,
		}); err != nil {
			return fmt.Errorf("quote-decision audit: %w", err)
		}
		return s.recordFieldChange(ctx, tx, caller.UserID, quoteID, 0, "status", prev, sp.to)
	}
}

// decisionPurpose marks the decision rows, so the keyed lookup never mistakes the customer's own
// acceptance row for one of these.
const decisionPurpose = "STAFF_QUOTE_DECISION"
