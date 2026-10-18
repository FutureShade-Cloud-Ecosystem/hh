package order

// change_signapply.go, SCOPE:F10 stage 3: the manager signs and applies a change in one tap.
//
//	POST /api/v1/order-changes/{changeId}/sign-apply     manager, Idempotency-Key required
//
// From PENDING_FINANCE it records the finance signature (for a caller the FinanceApprover seam allows) and
// applies; from APPROVED it applies; PENDING_YARD refuses with ErrChangeYardAckRequired. The signature and
// the apply are ONE transaction under the order row lock (change_repository.go: applyChange), so there is
// no state where a change is signed and not applied by this route.
//
// - THE KEY -
// The Idempotency-Key is a UUID and lands on the CHANGE_APPLIED audit row (the allowlisted client_id), the
// convention of the quote door (quote/share_idempotency.go): no migration, one anchor. A press with a key
// that order already applied under returns the first answer and writes nothing; the same key on a different
// change is ErrChangeKeyReused; a new key on an applied change is ErrChangeTerminal. Two presses at once
// serialise on the order lock, so exactly one applies.
//
// - TAX -
// The apply re-assesses tax inside the transaction through TaxRateReader (see change_repository.go), the
// seam the quote builder reads its rate through. The rate is the one in force the day the order was
// created, never today's, and only a tax the module can reproduce is rewritten.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// TaxRateReader answers the dealer's standard tax rate in basis points on a date ("" is today), the seam the
// quote builder already reads the same rate through. Declared here, in the consumer, and satisfied in main.go
// by the tax engine.
type TaxRateReader interface {
	StandardRateBps(ctx context.Context, asOf string) (int, error)
}

// WithStandardRate injects the tax rate seam.
func (s *Service) WithStandardRate(r TaxRateReader) *Service { s.taxRate = r; return s }

// WithZone injects the dealer's business zone, the calendar the order's creation day is read in.
func (s *Service) WithZone(z houseday.Source) *Service { s.zone = z; return s }

// ChangeEventPublisher is the durable outbox seam an applied change is announced on, written on the apply's
// own transaction. Declared here and satisfied in main.go over the events outbox, so the order module writes
// no table it does not own.
type ChangeEventPublisher interface {
	EnqueueTx(ctx context.Context, tx pgx.Tx, topic string, payload any) error
}

// WithChangeEvents injects the outbox seam. An unwired seam applies without the event (fail open, said
// loudly at boot through ChangeEventsWired), like the reservation seam.
func (s *Service) WithChangeEvents(p ChangeEventPublisher) *Service { s.changeEvents = p; return s }

// ChangeEventsWired reports whether an applied change reaches the outbox.
func (s *Service) ChangeEventsWired() bool { return s.changeEvents != nil }

// ChangeAppliedTopic is the outbox topic of a signed and applied change.
const ChangeAppliedTopic = "order.change.applied"

// ChangeAppliedLine is one delta row of the event.
type ChangeAppliedLine struct {
	Op             string  `json:"op"`
	LineNo         int     `json:"line_no,omitempty"`
	ProductID      string  `json:"product_id,omitempty"`
	QtyDeltaBase   float64 `json:"qty_delta_base"`
	LineDeltaCents int64   `json:"line_delta_cents"`
}

// ChangeAppliedEvent is the payload of ChangeAppliedTopic. Money is integer cents, as written to the order.
type ChangeAppliedEvent struct {
	ChangeID      string              `json:"change_id"`
	ChangeNo      string              `json:"change_no"`
	OrderID       string              `json:"order_id"`
	BranchID      string              `json:"branch_id"`
	DeltaCents    int64               `json:"delta_cents"`
	SubtotalCents int64               `json:"subtotal_cents"`
	TaxCents      int64               `json:"tax_cents"`
	TotalCents    int64               `json:"total_cents"`
	AppliedBy     string              `json:"applied_by"`
	SignedBy      string              `json:"signed_by,omitempty"`
	Lines         []ChangeAppliedLine `json:"lines"`
}

// ErrChangeKeyReused: the Idempotency-Key was already used for a different change order.
var ErrChangeKeyReused = errors.New("order: this idempotency key was used for a different change order; use a new key for a new action")

// signApplyWrite is what the service hands the store for one sign and apply.
type signApplyWrite struct {
	ChangeID string
	Key      string
	Signer   string
	Note     string
	Hooks    changeApplyHooks
}

// normalizeChangeKey validates the Idempotency-Key: a UUID, any case, stored lower case. It is required.
func normalizeChangeKey(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", httpx.BadRequestf("Idempotency-Key is required (a UUID, one per press)")
	}
	if !looksLikeUUID(raw) {
		return "", httpx.BadRequestf("Idempotency-Key must be a UUID")
	}
	return strings.ToLower(raw), nil
}

// SignApplyChange signs (when a finance signature is still owed) and applies a change, once.
//
// AUTHORITY: a manager of the change's branch, and for a material delta a named approver. The branch wall
// reads a change in another branch as not found. Every refusal that can be decided from the row happens
// before the store is reached; the store re-decides the ladder under the lock.
func (s *Service) SignApplyChange(ctx context.Context, changeID, idempotencyKey, note string) (ChangeOrder, error) {
	caller, cs, co, err := s.loadChangeForDecision(ctx, changeID)
	if err != nil {
		return ChangeOrder{}, err
	}
	if !caller.IsManager() {
		return ChangeOrder{}, ErrForbidden
	}
	key, err := normalizeChangeKey(idempotencyKey)
	if err != nil {
		return ChangeOrder{}, err
	}
	switch co.Status {
	case ChangeStatusPendingYard:
		return ChangeOrder{}, ErrChangeYardAckRequired
	case ChangeStatusPendingFinance:
		// Status only moves forward, so a pass here cannot go stale into a signature nobody may give.
		if !s.mayFinanceSign(ctx, co.BranchID, caller.UserID, nil) {
			return ChangeOrder{}, ErrForbidden
		}
	case ChangeStatusRejected, ChangeStatusCancelled:
		return ChangeOrder{}, ErrChangeTerminal
	}
	// APPROVED applies, and APPLIED reaches the store too: it answers a replay of the same key with the
	// first answer and a new key with ErrChangeTerminal.
	out, err := cs.SignApplyChangeTx(ctx, signApplyWrite{
		ChangeID: co.ID, Key: key, Signer: caller.UserID, Note: clampNote(note),
		Hooks: s.changeApplyHooks(caller.UserID, co.OrderID, key, clampNote(note)),
	})
	if err != nil {
		return ChangeOrder{}, err
	}
	out.NextActions = s.changeActions(ctx, caller, out.Status, out.BranchID, out.RequestedBy, nil)
	return out, nil
}

// changeApplyHooks builds the closures an apply runs inside its transaction. key is empty for the plain
// apply route (its audit row is written after the commit, as it always was) and set for the one tap, whose
// audit row carries the key and is written on the transaction.
func (s *Service) changeApplyHooks(actor, orderID, key, note string) changeApplyHooks {
	h := changeApplyHooks{
		precheck: func(ctx context.Context) error { return s.assertNoMoneyAttached(ctx, orderID) },
		resync: func(ctx context.Context, tx pgx.Tx, orderID, branchID string) error {
			if s.stock == nil {
				return nil // reservation lane off, see reservations.go, fail-open by design
			}
			_, err := s.resyncReservationsTx(ctx, tx, orderID, branchID, actor)
			return err
		},
		record: func(ctx context.Context, tx pgx.Tx, ev changeApplied) error {
			if key != "" {
				if s.auditTx == nil {
					// A key with no audit trail to carry it could not be replayed safely.
					return errors.New("order: sign and apply needs the transactional audit recorder")
				}
				reason := ev.ChangeNo
				if note != "" {
					reason += ": " + note
				}
				// The one tap that signs leaves the signature on the trail as the separate finance approve
				// route does: a CHANGE_FINANCE_APPROVED row naming the signer, on this transaction, written
				// before the applied row. It carries no client_id, so the replay lookup reads only the
				// applied row and is unchanged.
				if ev.SignedBy != "" {
					if err := s.auditTx.RecordTx(ctx, tx, audit.SensitiveAccess{
						Action: audit.ActOrderChanged, EntityType: "order", EntityID: ev.OrderID,
						ActorID: ev.SignedBy, Purpose: "ORDER_CHANGE_ORDER", Reason: reason,
						Extra: map[string]any{
							"field": "change_order", "outcome": "CHANGE_FINANCE_APPROVED", "status": ChangeStatusApproved,
							"policy": PolicyOrderChangeOverThreshold,
						},
					}); err != nil {
						return fmt.Errorf("audit: %w", err)
					}
				}
				if err := s.auditTx.RecordTx(ctx, tx, audit.SensitiveAccess{
					Action: audit.ActOrderChanged, EntityType: "order", EntityID: ev.OrderID,
					ActorID: actor, Purpose: "ORDER_CHANGE_ORDER", Reason: reason,
					Extra: map[string]any{
						"field": "change_order", "outcome": "CHANGE_APPLIED", "status": ChangeStatusApplied,
						"client_id": key,
					},
				}); err != nil {
					return fmt.Errorf("audit: %w", err)
				}
			}
			if s.changeEvents == nil {
				return nil
			}
			lines := make([]ChangeAppliedLine, 0, len(ev.Lines))
			for _, l := range ev.Lines {
				lines = append(lines, ChangeAppliedLine{
					Op: l.Op, LineNo: l.LineNo, ProductID: l.ProductID,
					QtyDeltaBase: l.QtyDeltaBase, LineDeltaCents: l.LineDeltaCents,
				})
			}
			return s.changeEvents.EnqueueTx(ctx, tx, ChangeAppliedTopic, ChangeAppliedEvent{
				ChangeID: ev.ChangeID, ChangeNo: ev.ChangeNo, OrderID: ev.OrderID, BranchID: ev.BranchID,
				DeltaCents: ev.DeltaCents, SubtotalCents: ev.SubtotalCents, TaxCents: ev.TaxCents,
				TotalCents: ev.TotalCents, AppliedBy: actor, SignedBy: ev.SignedBy, Lines: lines,
			})
		},
	}
	h.replay = func(ctx context.Context, orderID, key string) (string, bool, error) {
		// The key lives on the CHANGE_APPLIED audit row (the allowlisted client_id), read back through the
		// audit module's own trail read, never its table. The order lock is held by the caller, so an earlier
		// press has committed its row before this one reads.
		if s.auditTrail == nil {
			return "", false, errors.New("order: sign and apply needs the audit trail reader")
		}
		entries, err := s.auditTrail.ReopenTransaction(ctx, "order", orderID)
		if err != nil {
			return "", false, fmt.Errorf("order: read the audit trail for the idempotency key: %w", err)
		}
		for _, e := range entries {
			if e.Action != audit.ActOrderChanged || e.Changes["field"] != "change_order" ||
				e.Changes["outcome"] != "CHANGE_APPLIED" || e.Changes["client_id"] != key {
				continue
			}
			reason, _ := e.Changes["reason"].(string)
			head, _, _ := strings.Cut(reason, ":")
			return head, true, nil
		}
		return "", false, nil
	}
	if s.taxRate != nil {
		h.taxRate = func(ctx context.Context, tx pgx.Tx, created time.Time) (int, error) {
			zone, err := houseday.ResolveTx(ctx, s.zone, tx)
			if err != nil {
				return 0, err
			}
			return s.taxRate.StandardRateBps(ctx, created.In(zone.Location()).Format("2006-01-02"))
		}
	}
	return h
}

// ChangeAppliedPayload decodes an event, for the observers that react to it.
func ChangeAppliedPayload(raw []byte) (ChangeAppliedEvent, error) {
	var ev ChangeAppliedEvent
	err := json.Unmarshal(raw, &ev)
	return ev, err
}
