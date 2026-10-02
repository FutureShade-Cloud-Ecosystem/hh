package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// Engine is the sync core: the per-scope Stores wired at the composition
// root, driven by the two operations the contract declares. It owns the
// protocol mechanics (ordering, dedup, conflict dispatch, the pull page
// shape) and no table: every row belongs to the module whose Store serves
// the scope.
type Engine struct {
	stores map[string]Store
	audit  *auditLog
}

// NewEngine returns an empty Engine. Scopes arrive by RegisterScope; the
// construction validates nothing beyond the map so the composition root can
// build first and wire in any order.
func NewEngine() *Engine {
	return &Engine{stores: make(map[string]Store)}
}

// WithAudit wires the audit seam. nil or unwired means no per-operation
// audit rows (the A-21 middleware row for the mutating request still lands;
// the seam is the per-operation half).
func (e *Engine) WithAudit(rec AuditRecorder, log *slog.Logger) *Engine {
	e.audit = newAuditLog(rec, log)
	return e
}

// RegisterScope wires one scope's Store. The scope must exist in the
// registry, the store must be non-nil, and a scope may be wired exactly
// once: a second wiring is a composition defect and refuses loudly rather
// than silently preferring one module's tables.
func (e *Engine) RegisterScope(name string, s Store) error {
	if _, ok := Lookup(name); !ok {
		return fmt.Errorf("sync: register: %q is not a contract scope", name)
	}
	if s == nil {
		return fmt.Errorf("sync: register: store for %q is nil", name)
	}
	if _, dup := e.stores[name]; dup {
		return fmt.Errorf("sync: register: scope %q is already wired", name)
	}
	e.stores[name] = s
	return nil
}

// Store resolves one scope's wired store; ok is false when no module has
// wired the scope yet (the routes answer that case with the honest 501
// rather than fabricating an empty page).
func (e *Engine) Store(name string) (Store, bool) {
	s, ok := e.stores[name]
	return s, ok
}

// Pull runs the contract's syncPull against one scope: the live rows and
// tombstones with seq greater than since, in seq order, at most limit rows
// in total, under the caller's scope filter. The response's last_seq is the
// high-water mark the client stores as sync_state.last_seq: the seq of the
// last row of this page, or the caller's since when the page changed
// nothing, so an empty page never walks the client's cursor backwards.
func (e *Engine) Pull(ctx context.Context, info ScopeInfo, f ScopeFilter, since int64, limit int) (apigen.SyncPullResponse, error) {
	store, ok := e.stores[info.Name]
	if !ok {
		return apigen.SyncPullResponse{}, fmt.Errorf("sync: scope %q is not wired", info.Name)
	}
	items, tombstones, err := store.Pull(ctx, f, since, limit)
	if err != nil {
		return apigen.SyncPullResponse{}, err
	}
	resp := apigen.SyncPullResponse{
		Scope:      apigen.SyncScope(info.Name),
		Items:      make([]apigen.SyncEntity, 0, len(items)),
		Tombstones: make([]apigen.SyncTombstone, 0, len(tombstones)),
		LastSeq:    since,
	}
	for _, it := range items {
		resp.Items = append(resp.Items, apigen.SyncEntity{Id: it.ID, Seq: it.Seq, Data: it.Data})
		if it.Seq > resp.LastSeq {
			resp.LastSeq = it.Seq
		}
	}
	for _, t := range tombstones {
		resp.Tombstones = append(resp.Tombstones, apigen.SyncTombstone{Id: t.ID, Seq: t.Seq})
		if t.Seq > resp.LastSeq {
			resp.LastSeq = t.Seq
		}
	}
	// A full page may have a follower. The cursor is the page's last seq
	// as a plain decimal string: the pull operation takes no cursor
	// parameter (the contract's parameter set is X-Org, scope, since and
	// limit), so the resume is since=<next_cursor>, the same monotonic
	// key the page was ordered on.
	if limit > 0 && len(items)+len(tombstones) == limit {
		cursor := fmt.Sprintf("%d", resp.LastSeq)
		resp.NextCursor = &cursor
	}
	return resp, nil
}

// push outcome vocabulary, the contract's SyncPushResult.status enum.
const (
	resultApplied  = "applied"
	resultConflict = "conflict"
	resultRejected = "rejected"
)

// op kinds, the contract's SyncPushOperation.op enum.
const (
	opCreate = "create"
	opUpdate = "update"
	opDelete = "delete"
)

// Push replays the client's outbox in order, one result per operation, in
// push order. Per operation, in order: shape validation (a rejected
// result, never a failed request), idempotency-key dedup (a replayed
// operation returns applied and never applies twice), then the operation
// itself with the scope's conflict policy deciding any stale write.
// Operation-level refusals (the module's own invariants, a missing or
// out-of-scope row, an impossible create) are that operation's rejected
// result, and replay continues: the outbox is the client's own record,
// and one refused operation must not sink the operations behind it that
// the module would accept. An UNCLASSIFIED store failure is an
// operational fault rather than a verdict on any operation: the whole
// push fails, and the client retries the outbox whole, where the
// operations that had landed dedup on their keys.
func (e *Engine) Push(ctx context.Context, info ScopeInfo, f ScopeFilter, ops []apigen.SyncPushOperation) (apigen.SyncPushResponse, error) {
	store := e.stores[info.Name]
	resp := apigen.SyncPushResponse{Results: make([]apigen.SyncPushResult, 0, len(ops))}
	for _, op := range ops {
		res, fatal := e.pushOne(ctx, store, info, f, op)
		if fatal != nil {
			return apigen.SyncPushResponse{}, fatal
		}
		resp.Results = append(resp.Results, res)
	}
	return resp, nil
}

// pushOne resolves one operation. The result's legs map onto the contract's
// SyncPushResult: status (applied, conflict, rejected), seq when the
// operation wrote, the conflict envelope when it conflicted, error when it
// was rejected.
func (e *Engine) pushOne(ctx context.Context, store Store, info ScopeInfo, f ScopeFilter, op apigen.SyncPushOperation) (apigen.SyncPushResult, error) {
	if err := validOp(op); err != nil {
		return e.auditRejected(ctx, info, f, op, "", err), nil
	}
	// Dedup first, before any conflict evaluation: an operation that
	// already applied under this key is finished, and a replay must not
	// re-litigate a row that moved after it landed.
	row, applied, err := store.FindApplied(ctx, f, op.ClientId)
	if err != nil {
		return e.classified(ctx, info, f, op, "", err)
	}
	if applied {
		res := apigen.SyncPushResult{ClientId: op.ClientId, Status: resultApplied, Seq: &row.Seq}
		e.record(ctx, info, f, op, row.ID, resultApplied, "")
		return res, nil
	}
	switch op.Op {
	case opCreate:
		created, err := store.Create(ctx, f, op.ClientId, op.Payload)
		if err != nil {
			return e.classified(ctx, info, f, op, "", err)
		}
		res := apigen.SyncPushResult{ClientId: op.ClientId, Status: resultApplied, Seq: &created.Seq}
		e.record(ctx, info, f, op, created.ID, resultApplied, "")
		return res, nil
	case opUpdate:
		id, _ := op.Payload["id"].(string)
		row, ok, err := store.Load(ctx, f, id)
		if err != nil {
			return e.classified(ctx, info, f, op, id, err)
		}
		if !ok {
			return e.auditRejected(ctx, info, f, op, id, ErrRowNotFound), nil
		}
		if stale(op, row) {
			return e.resolveStale(ctx, store, info, f, op, row, id)
		}
		written, err := store.Write(ctx, f, id, op.ClientId, op.Payload)
		if err != nil {
			return e.classified(ctx, info, f, op, id, err)
		}
		res := apigen.SyncPushResult{ClientId: op.ClientId, Status: resultApplied, Seq: &written.Seq}
		e.record(ctx, info, f, op, id, resultApplied, "")
		return res, nil
	case opDelete:
		id, _ := op.Payload["id"].(string)
		row, ok, err := store.Load(ctx, f, id)
		if err != nil {
			return e.classified(ctx, info, f, op, id, err)
		}
		if !ok {
			return e.auditRejected(ctx, info, f, op, id, ErrRowNotFound), nil
		}
		if row.Deleted {
			// Already tombstoned: the delete's effect stands, and the
			// result reports the tombstone's seq rather than allocating
			// a second one for the same fact.
			res := apigen.SyncPushResult{ClientId: op.ClientId, Status: resultApplied, Seq: &row.Seq}
			e.record(ctx, info, f, op, id, resultApplied, "")
			return res, nil
		}
		deleted, err := store.Delete(ctx, f, id, op.ClientId)
		if err != nil {
			return e.classified(ctx, info, f, op, id, err)
		}
		res := apigen.SyncPushResult{ClientId: op.ClientId, Status: resultApplied, Seq: &deleted.Seq}
		e.record(ctx, info, f, op, id, resultApplied, "")
		return res, nil
	}
	// validOp pinned the op vocabulary; this arm is unreachable by
	// construction and fails closed.
	return e.auditRejected(ctx, info, f, op, "", fmt.Errorf("sync: unknown op %q", op.Op)), nil
}

// classified sorts one store error: the seam's declared refusals are the
// operation's rejected result (the module's own words travelling
// verbatim), anything else is an operational fault that fails the whole
// push.
func (e *Engine) classified(ctx context.Context, info ScopeInfo, f ScopeFilter, op apigen.SyncPushOperation, id string, err error) (apigen.SyncPushResult, error) {
	if errors.Is(err, ErrRowNotFound) || errors.Is(err, ErrRowExists) || errors.Is(err, ErrOpRefused) {
		return e.auditRejected(ctx, info, f, op, id, err), nil
	}
	return apigen.SyncPushResult{}, err
}

// resolveStale resolves one stale update under the scope's policy: the
// update is the only op kind that can conflict (a create has no base to be
// stale against, and a delete's fact is idempotent under the tombstone
// rule). The envelope travels per operation inside this push response, in
// the contract's own shape; the request itself is never failed for a
// conflict.
func (e *Engine) resolveStale(ctx context.Context, store Store, info ScopeInfo, f ScopeFilter, op apigen.SyncPushOperation, row RowState, id string) (apigen.SyncPushResult, error) {
	out := resolveConflict(info.Policy, row, op.Payload)
	if out.ReviewMarker {
		// The manual policy's review marker: the server-side record that
		// a client edit is waiting on a review card, beside the envelope
		// the response carries.
		if e.audit != nil {
			e.audit.record(ctx, AuditEntry{
				Action: "SYNC_CONFLICT_REVIEW", Scope: info.Name, Op: op.Op,
				ClientID: op.ClientId, EntityID: id, Actor: f.Actor,
				Outcome: resultConflict, ConflictPolicy: string(PolicyManual),
			})
		}
	}
	if out.MergedFields == nil {
		// Nothing applies: the envelope is the whole outcome, and the
		// server row is what the client is told to show.
		res := apigen.SyncPushResult{
			ClientId: op.ClientId,
			Status:   resultConflict,
			Conflict: &apigen.SyncConflict{
				ClientId:    op.ClientId,
				Policy:      string(out.Policy),
				ServerState: out.ServerState,
				Applied:     &out.Applied,
			},
		}
		e.record(ctx, info, f, op, id, resultConflict, string(out.Policy))
		return res, nil
	}
	// The merge writes: the declared fields land on the row (the server
	// row authoritative on every field the payload does not declare),
	// the core assigns a fresh seq, and the envelope reports the merged
	// state with applied true.
	written, err := store.Write(ctx, f, id, op.ClientId, out.MergedFields)
	if err != nil {
		return e.classified(ctx, info, f, op, id, err)
	}
	applied := out.Applied
	res := apigen.SyncPushResult{
		ClientId: op.ClientId,
		Status:   resultApplied,
		Seq:      &written.Seq,
		Conflict: &apigen.SyncConflict{
			ClientId:    op.ClientId,
			Policy:      string(out.Policy),
			ServerState: out.ServerState,
			Applied:     &applied,
		},
	}
	e.record(ctx, info, f, op, id, resultApplied, string(out.Policy))
	return res, nil
}

// record writes the per-operation audit row for one applied or conflicted
// result; policy is the envelope's policy leg when the result carries one,
// empty for a plain applied operation. Audit is an index, never a
// precondition (the tasks module's own recorded doctrine).
func (e *Engine) record(ctx context.Context, info ScopeInfo, f ScopeFilter, op apigen.SyncPushOperation, entityID, outcome, policy string) {
	if e.audit == nil {
		return
	}
	e.audit.record(ctx, AuditEntry{
		Action: "SYNC_PUSH", Scope: info.Name, Op: op.Op,
		ClientID: op.ClientId, EntityID: entityID, Actor: f.Actor, Outcome: outcome, ConflictPolicy: policy,
	})
}

// auditRejected builds one rejected result and its audit row, the message
// travelling verbatim (module prefixes strip at the one door every
// envelope leaves through).
func (e *Engine) auditRejected(ctx context.Context, info ScopeInfo, f ScopeFilter, op apigen.SyncPushOperation, entityID string, err error) apigen.SyncPushResult {
	if e.audit != nil {
		e.audit.record(ctx, AuditEntry{
			Action: "SYNC_PUSH", Scope: info.Name, Op: op.Op,
			ClientID: op.ClientId, EntityID: entityID, Actor: f.Actor, Outcome: resultRejected,
		})
	}
	msg := err.Error()
	return apigen.SyncPushResult{ClientId: op.ClientId, Status: resultRejected, Error: &msg}
}

// stale reports whether the operation's base is older than the row: the
// client based its write on base_seq, and the row has moved past it. An
// operation with no base_seq declares nothing to conflict against and is
// applied as declared (the contract makes the leg optional for exactly
// this blind-write case).
func stale(op apigen.SyncPushOperation, row RowState) bool {
	return op.BaseSeq != nil && row.Seq > *op.BaseSeq
}

// validOp checks the operation's shape: the idempotency key must be a uuid
// (it is the dedup key), the op must be one of the contract's three kinds,
// and the payload must be present, with an id for the row-addressed kinds.
func validOp(op apigen.SyncPushOperation) error {
	if !looksLikeUUID(op.ClientId) {
		return errors.New("client_id must be a uuid")
	}
	switch op.Op {
	case opCreate:
		if op.Payload == nil {
			return errors.New("create payload is required")
		}
	case opUpdate, opDelete:
		id, ok := op.Payload["id"].(string)
		if !ok || !looksLikeUUID(id) {
			return errors.New("payload id must be a uuid")
		}
	default:
		return errors.New("op must be create, update or delete")
	}
	return nil
}

// looksLikeUUID reports whether s is a canonical 36-char 8-4-4-4-12 hex
// uuid, the same boundary guard the tasks module uses so a malformed id is
// rejected before it reaches a Postgres uuid column.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}
