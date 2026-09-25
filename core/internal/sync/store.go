package sync

import (
	"context"
	"errors"
)

// ErrRowNotFound is the Store seam's miss: the row the engine asked for is
// absent, or it sits outside the caller's scope filter. The two cases are
// deliberately indistinguishable (existence never leaks), and the engine
// maps both onto the per-operation rejected result with a not-found error.
var ErrRowNotFound = errors.New("sync: row not found")

// ErrRowExists is the Store seam's create collision: a row with the
// client-chosen id already exists under the caller's scope filter, under a
// different idempotency key than the one this operation carries.
var ErrRowExists = errors.New("sync: row already exists")

// ErrOpRefused is the Store seam's invariant refusal: the owning module's
// own rules (a state machine, a validity window, a fence) refused the
// write. The engine maps it onto the per-operation rejected result, the
// module's message travelling verbatim.
var ErrOpRefused = errors.New("sync: operation refused")

// ScopeFilter is the mandatory scoping predicate the engine forces into
// every Store call: the org the middleware resolved (X-Org under the staff
// bearer, the Team's org under the tenant bearer, per G4 and ADR-0020),
// plus the tenant for a tenant caller (from the token, never a header or
// parameter). A Store that queried its tables without this predicate would
// break the scoping invariant (AGENTS.md, module boundary rules), and the
// per-scope harness proves it does not, scope by scope.
type ScopeFilter struct {
	// OrgID is the resolved org anchor id, always set for both caller
	// classes.
	OrgID string
	// TenantID is the tenant from the token; empty for staff callers.
	TenantID string
	// Tenant reports the caller class: true for a tenant bearer, false
	// for staff.
	Tenant bool
	// Actor is the caller's platform subject, the identity fact the
	// per-operation audit rows and the owning module's own attribution
	// read (the module resolves its ERP user through its linker).
	Actor string
}

// Entity is one live projected row of a pull page: the row's id, its
// per-scope monotonic seq, and its projection, the shape the contract's
// SyncEntity.data carries (the resource schema the scope's owning module
// defines).
type Entity struct {
	ID   string
	Seq  int64
	Data map[string]any
}

// Tombstone is one deleted row of a pull page: deletes are tombstones
// (deleted_at set, the row retained), and the tombstone carries the id and
// the seq of the delete.
type Tombstone struct {
	ID  string
	Seq int64
}

// RowState is the Store's answer for one row on the write paths: the row's
// id, its current seq, its live projection, and whether it is a tombstone.
// Deleted rows carry their seq and no projection.
type RowState struct {
	ID      string
	Seq     int64
	Data    map[string]any
	Deleted bool
}

// Store is the seam the owning module implements for its scope: the pull
// projection and the write primitives the engine drives. The engine calls
// every method with the caller's ScopeFilter and treats the filter as
// load-bearing: rows outside it are the same as absent. Seq allocation is
// the Store's to do inside its own write transaction, from the per-scope
// allocator (scope_sequences, the 0213 convention), exactly the recipe the
// customerquote module already follows; the engine reads the assigned seq
// off the returned RowState and never allocates one itself.
//
// Idempotency: FindApplied is the dedup lookup for one client idempotency
// key. The D30 columns hold the key on the row the operation applied; where
// a scope's owning tables keep a fuller applied-key history (the pilot's
// task_events rows do), FindApplied consults it, so an outbox holding more
// than one operation on one row replays without re-applying the earlier
// operations.
type Store interface {
	// Pull returns the live rows and tombstones with seq greater than
	// since, in seq ascending order, at most limit rows in total across
	// both slices, under the caller's scope filter.
	Pull(ctx context.Context, f ScopeFilter, since int64, limit int) ([]Entity, []Tombstone, error)

	// FindApplied resolves one idempotency key to the row it applied, if
	// any, under the caller's scope filter. The returned RowState is the
	// row's live state: the dedup result reports the row's current seq
	// (the truth a resuming device needs), while the never-applies-twice
	// half is the engine's to guarantee.
	FindApplied(ctx context.Context, f ScopeFilter, key string) (RowState, bool, error)

	// Load reads one row by id under the caller's scope filter.
	Load(ctx context.Context, f ScopeFilter, id string) (RowState, bool, error)

	// Create inserts one row from the client's payload fields, stamping
	// the scope filter's org (and tenant for a tenant caller), the
	// idempotency key, and a freshly allocated seq. The row's id is the
	// payload's id when the client generated one, else the server assigns
	// it.
	Create(ctx context.Context, f ScopeFilter, key string, fields map[string]any) (RowState, error)

	// Write applies exactly the declared fields to one row, stamps the
	// idempotency key, and allocates a fresh seq, enforcing the module's
	// own invariants (ErrOpRefused carries the refusal).
	Write(ctx context.Context, f ScopeFilter, id, key string, fields map[string]any) (RowState, error)

	// Delete tombstones one row (deleted_at set, the row retained),
	// stamps the idempotency key, and allocates a fresh seq.
	Delete(ctx context.Context, f ScopeFilter, id, key string) (RowState, error)
}
