package sync

import (
	"context"
	"log/slog"
)

// AuditEntry is one per-operation audit fact the engine emits: every pushed
// operation is audited per the audit invariant (context section 5: an audit
// table records every privileged action), carrying the scope, the
// operation's shape, the client's idempotency key, and the outcome. Values
// are code-shaped (enum vocabulary and uuids); nothing a client typed
// free-hand rides along.
type AuditEntry struct {
	// Action is the audit verb: the push outcome row, or the manual
	// policy's review marker.
	Action string
	// Scope is the sync scope name.
	Scope string
	// Op is the operation kind: create, update or delete.
	Op string
	// ClientID is the operation's idempotency key.
	ClientID string
	// EntityID is the row the operation touched, when known.
	EntityID string
	// Actor identifies the caller (the platform subject of the stamped
	// scope).
	Actor string
	// Outcome is applied, conflict, or rejected; ConflictPolicy names the
	// policy when the outcome is conflict.
	Outcome        string
	ConflictPolicy string
}

// AuditRecorder is the audit seam the engine writes through. The concrete
// adapter maps onto the audit module's Recorder (audit.SensitiveAccess with
// the sync action verbs); the seam exists so tests inject doubles and so
// the engine carries no dependency on the audit package's own shapes.
// Audit is an index of who did what, never a precondition: a failed audit
// write is logged and the push result stands (the tasks module's own
// recorded doctrine for its audit rows).
type AuditRecorder interface {
	RecordSync(ctx context.Context, e AuditEntry) error
}

// auditLog is the engine's best-effort wrapper around the seam: nil-safe
// (an unwired seam means no audit rows, never a failed push) and
// error-swallowing by design, the failure logged for the operator.
type auditLog struct {
	rec AuditRecorder
	log *slog.Logger
}

func newAuditLog(rec AuditRecorder, log *slog.Logger) *auditLog {
	if log == nil {
		log = slog.Default()
	}
	return &auditLog{rec: rec, log: log}
}

// record writes one entry best-effort.
func (a *auditLog) record(ctx context.Context, e AuditEntry) {
	if a == nil || a.rec == nil {
		return
	}
	if err := a.rec.RecordSync(ctx, e); err != nil {
		a.log.Warn("sync: audit write failed", "action", e.Action, "scope", e.Scope, "err", err)
	}
}
