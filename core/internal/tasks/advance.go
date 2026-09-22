package tasks

// advance.go, DIB-325: completing a task MAY advance its order's status, per task type, OFF by
// default, audited when it fires.
//
// WHY THIS IS A DECISION AND NOT A FEATURE. service.go's Done() has carried a note since 0073 saying
// the order-status advance is "DIB-191's open SCOPE decision (a product ruling, not engineering)".
// That was right, and it still is: whether a picker finishing a pick may move an order toward
// invoicing depends on how a branch actually operates. A counter-and-collect branch says yes; a
// branch whose picks are followed by a delivery says absolutely not, because dispatch's own POD
// handoff is the correct writer and a task-driven advance would put the order in FULFILLED while the
// pallet is still on the rack.
//
// So the ruling is not made here. What is built here is the PLACE the ruling lives:
//
//   - a row per task type (0108 task_status_advance_rules), every one seeded FALSE;
//   - an advance that goes through the order module's OWN forward-only ladder, so even an enabled
//     rule cannot walk an order backwards;
//   - an audit row every single time it fires, naming the task, the order and the target, because
//     an automatic status change nobody can attribute is the thing that makes people distrust a
//     system's status column.
//
// THE DEFAULT IS OFF AND THE SEAM IS ABSENT. A deployment that wires nothing behaves exactly as Wave
// 2 did. That is deliberate: a flag whose default changes behaviour is not a flag.

import (
	"context"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// AdvanceRule is one task type's ruling (a task_status_advance_rules row, 0108).
type AdvanceRule struct {
	TaskType     string `json:"task_type"`
	Enabled      bool   `json:"enabled"`
	TargetStatus string `json:"target_status,omitempty"`
}

// Fires reports whether this rule should act. Both halves are required: an enabled rule with no
// target is a switch wired to nothing (0108 refuses to store one; this refuses to act on one that
// somehow exists).
func (r AdvanceRule) Fires() bool {
	return r.Enabled && strings.TrimSpace(r.TargetStatus) != ""
}

// OrderAdvancer is the CONSUMER-DECLARED seam over the order module's STATE-GUARDED event writer
// (order.Service.AdvanceFromEvent), satisfied in cmd/server/main.go.
//
// It is named for the FACT and the TARGET, not for a write, for the reason dispatch.OrderFulfiller's
// comment gives at length: the order module owns its own forward-only ladder and grades every
// system-driven move against it. An order a POS ring already carried past the target DECLINES the
// advance rather than being walked backwards, and that judgement stays with the module that owns the
// ladder rather than being re-implemented here from a table of statuses.
type OrderAdvancer interface {
	AdvanceOrder(ctx context.Context, orderID, targetStatus, reason string) error
}

// WithOrderAdvancer injects the advance seam. Absent, the rule is read and NOT acted on, logged once
// per firing so a half-wired deployment is visible rather than quietly inert.
func (s *Service) WithOrderAdvancer(a OrderAdvancer) *Service { s.advancer = a; return s }

// WithAudit injects the A-21 Recorder. Only the advance path uses it: this is the one thing tasks does
// that changes a row in another module, and it is the one thing that must be attributable.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// maybeAdvanceOrder is the decision gate, called after a task reaches DONE.
//
// EVERY EARLY RETURN IS SILENT EXCEPT THE HALF-WIRED ONE. A rule that is off is the normal case and
// must not produce a log line per completed pick. A rule that is ON with no seam behind it is a
// misconfiguration, and it says so.
func (s *Service) maybeAdvanceOrder(ctx context.Context, t Task) {
	if t.Status != StatusDone || t.OrderID == "" {
		return
	}
	rule, err := s.store.AdvanceRule(ctx, t.TaskType)
	if err != nil {
		if s.log != nil {
			s.log.Warn("tasks: advance rule read failed", "task", t.ID, "type", t.TaskType, "err", err)
		}
		return
	}
	if !rule.Fires() {
		return
	}
	if s.advancer == nil {
		if s.log != nil {
			s.log.Warn("tasks: order advance is ENABLED for this task type but no order seam is wired",
				"task", t.ID, "type", t.TaskType, "target", rule.TargetStatus)
		}
		return
	}
	reason := "task:" + t.ID
	if err := s.advancer.AdvanceOrder(ctx, t.OrderID, rule.TargetStatus, reason); err != nil {
		// A DECLINED advance is normal and is not an error: the order module grades the move against
		// its own ladder and says no when the order is already past the target. Info, not Warn.
		if s.log != nil {
			s.log.Info("tasks: order advance declined",
				"task", t.ID, "order", t.OrderID, "target", rule.TargetStatus, "err", err)
		}
		return
	}
	// It fired. This is the audited moment, an automatic status change with no attribution is the
	// thing that makes a status column untrustworthy.
	s.record(ctx, audit.SensitiveAccess{
		Action: "TASK_ADVANCED_ORDER", EntityType: "order", EntityID: t.OrderID,
		Extra: map[string]any{
			"task_id": t.ID, "task_type": t.TaskType, "target_status": rule.TargetStatus,
		},
	})
	if s.log != nil {
		s.log.Info("tasks: order advanced on task completion",
			"task", t.ID, "order", t.OrderID, "target", rule.TargetStatus)
	}
}

// AdvanceRuleFor exposes one task type's current ruling (the admin read behind
// GET /api/v1/yard/advance-rules). Read-only on purpose: turning a rule ON is a deliberate act
// against the database, not a button, see this file's header.
func (s *Service) AdvanceRuleFor(ctx context.Context, taskType string) (AdvanceRule, error) {
	return s.store.AdvanceRule(ctx, strings.ToUpper(strings.TrimSpace(taskType)))
}

// record writes an audit row best-effort. Audit is an index of who did what, never a precondition: a
// task does not fail because the trail could not be written (dispatch.Service.record, verbatim rule).
func (s *Service) record(ctx context.Context, ev audit.SensitiveAccess) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(ctx, ev); err != nil && s.log != nil {
		s.log.Warn("tasks: audit write failed", "action", ev.Action, "err", err)
	}
}
