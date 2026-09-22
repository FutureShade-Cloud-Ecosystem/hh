package messaging

import (
	"context"
	"log/slog"
	"time"
)

// - Approvals rail (0075): reconcile sweep -
//
// The reconcile worker is a periodic ticker that makes the rail's degradation story TRUE: it backfills
// (a) PENDING requests missing a card in a currently-eligible approver's channel, a notifier outage, a
// deploy that predates the request, or a later-granted approver (the zero-eligible edge self-heals here);
// and (b) decided requests whose card copies still show PENDING, a missed stamp/echo. Every action is
// idempotent through the card/echo event_key uniqueness + the stamp being a plain UPDATE, so a repeated
// tick never double-posts and a double-run produces zero new rows.
//
// Boundaries: the worker owns NO approval state. It pulls the current desired fan-out (which requests,
// which eligible approvers, which decided statuses) through the approvalSource seam, implemented by a
// main.go adapter over identity.Service, which is where CanApprove eligibility (identity's authority) is
// resolved. Nil source ⇒ the sweep is a no-op (kill switch: it does nothing when the notifier is unwired).
//
// It mirrors RetentionWorker / notify.Dispatcher: Run drains once immediately then every interval until
// ctx cancels; ReconcileOnce is the exposed single pass (tests + one-shot drain).

// defaultReconcileInterval is the 60s sweep cadence (D-P1 degradation budget: a recovered notifier
// backfills within 60s). Overridable via WithInterval (tests use a tiny value).
const defaultReconcileInterval = 60 * time.Second

// approvalSource is the reconcile worker's read seam over identity's approval state (the main.go adapter
// over identity.Service). It returns the CURRENT desired rail state, so the worker only reconciles the
// hub to it. Declared here (not in identity) so identity never imports messaging, the adapter translates.
type approvalSource interface {
	ApprovalReconcile(ctx context.Context) (ReconcilePlan, error)
}

// ReconcilePlan is one sweep's desired state: the still-PENDING requests (with their currently-eligible
// approver ids + the card ref to fan) and the recently-decided requests (with the status to stamp/echo).
type ReconcilePlan struct {
	Pending []PendingApproval
	Decided []DecidedApproval
}

// PendingApproval is a still-PENDING request's fan-out target: the card ref + the users who CanApprove it.
type PendingApproval struct {
	Ref         ApprovalRef
	ApproverIDs []string
}

// DecidedApproval is a recently-decided request whose rail copies must show the decision.
type DecidedApproval struct {
	ApprovalID  string
	Status      string // APPROVED | REJECTED
	DecidedBy   string
	Note        string
	RequestedBy string
}

// ReconcileWorker sweeps the rail to match the approval source on a ticker. It owns no state beyond the
// ticker, so Run is safe in a goroutine (go worker.Run(ctx)), exactly like RetentionWorker.
type ReconcileWorker struct {
	svc      *Service
	source   approvalSource
	log      *slog.Logger
	interval time.Duration
}

// NewReconcileWorker builds the worker. source may be nil (kill switch, the sweep no-ops).
func NewReconcileWorker(svc *Service, source approvalSource, log *slog.Logger) *ReconcileWorker {
	if log == nil {
		log = slog.Default()
	}
	return &ReconcileWorker{svc: svc, source: source, log: log, interval: defaultReconcileInterval}
}

// WithInterval overrides the sweep interval (tests use a tiny value; <=0 is ignored).
func (w *ReconcileWorker) WithInterval(v time.Duration) *ReconcileWorker {
	if v > 0 {
		w.interval = v
	}
	return w
}

// Run sweeps immediately, then every interval, until ctx is cancelled. Mirrors RetentionWorker.Run.
func (w *ReconcileWorker) Run(ctx context.Context) {
	if w.source == nil {
		w.log.Info("messaging: approvals reconcile worker not started (no source wired)")
		return
	}
	w.log.Info("messaging: approvals reconcile worker started", "interval", w.interval.String())
	w.ReconcileOnce(ctx)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.log.Info("messaging: approvals reconcile worker stopped")
			return
		case <-t.C:
			w.ReconcileOnce(ctx)
		}
	}
}

// ReconcileOnce runs one sweep: pull the desired state, re-fan missing PENDING cards, and backfill decided
// stamps/echoes. Each item is best-effort + log-and-continue (a bad item never blocks the rest), and every
// action is idempotent, so a double-run over unchanged state produces zero new rows. Returns the number of
// requests it touched (fanned or reflected) for observability + the sweep-idempotence test.
func (w *ReconcileWorker) ReconcileOnce(ctx context.Context) int {
	if w.source == nil {
		return 0
	}
	plan, err := w.source.ApprovalReconcile(ctx)
	if err != nil {
		w.log.Error("messaging: approvals reconcile failed to load plan", "err", err)
		return 0
	}
	touched := 0
	for _, p := range plan.Pending {
		if len(p.ApproverIDs) == 0 {
			// Zero-eligible-approver edge: nothing to fan yet. Logged (not silently dropped) so a
			// mis-scoped/deactivated-approver request is visible; it stays decidable via pending-approvals
			// and self-heals here once an approver becomes eligible.
			w.log.Error("messaging: approval has no eligible approver to fan to", "approval_id", p.Ref.ApprovalID)
			continue
		}
		if err := w.svc.PostApprovalToUserChannels(ctx, p.ApproverIDs, p.Ref); err != nil {
			w.log.Warn("messaging: reconcile re-fan partial failure", "approval_id", p.Ref.ApprovalID, "err", err)
		}
		touched++
	}
	for _, d := range plan.Decided {
		if err := w.svc.PostApprovalDecision(ctx, d.ApprovalID, d.Status, d.DecidedBy, d.Note); err != nil {
			w.log.Warn("messaging: reconcile decision reflect partial failure", "approval_id", d.ApprovalID, "err", err)
		}
		if d.RequestedBy != "" {
			w.svc.NotifyUser(d.RequestedBy, "approval.decided", map[string]string{
				"approval_id": d.ApprovalID,
				"status":      d.Status,
			})
		}
		touched++
	}
	return touched
}
