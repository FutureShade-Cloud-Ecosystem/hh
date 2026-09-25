// syncscope.go heads the dispatch module's sync adapters (work item
// #167, Phase D): the runs, stops and pod Stores over this module's own
// tables. The pattern is the pick_tasks pilot's (tasks/syncscope.go, work
// item #165): the sync core owns the protocol and no table, this module
// owns delivery_runs, delivery_stops and delivery_pod_photos and meets
// the core at the Store seam, so the fence rules hold on the sync
// surface exactly as they hold on the REST surface. The three adapters
// live in their own files beside this one; what they share is here.
//
// The pull projections are the contract's resource schemas
// (core/api/openapi.yaml): DispatchRun for runs, DispatchStop for stops
// and DispatchPhotoMeta for pod, the same shapes the module's own REST
// surface serves, so a device's cached row and the board's screen read
// one shape. The D17 contractor stop view is a different surface
// (HH-3.6's) and nothing here touches it: this module syncs the
// staff-side stop rows only.
//
// Idempotency, the one design note that is not the pilot's: the row's
// single D30 idempotency_key column records the latest key applied to
// the LIVE row, and a tombstone deliberately KEEPS that key (the delete
// does not stamp its own). Two dedup facts then hold without any
// second table: the last live operation's key still resolves after a
// later delete, and the delete itself is idempotent through the
// engine's already-tombstoned arm, which reports the retained
// tombstone's seq instead of allocating a second one for the same
// fact. The module has no append-only per-row event trail (the tasks
// module's task_events) to carry a fuller applied-key history, the
// schema is frozen, and the audit log is an index and never a
// precondition, so this is the honest shape the frozen schema gives.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// syncLinker names the staff identity seam the adapters take, the same
// shape identity.UserIDLinker names (the linked ERP user for a platform
// subject), aliased so the adapter constructors read in this module's
// vocabulary without a second type declaration.
type syncLinker = identity.UserIDLinker

// syncScope names the per-scope sequence allocators this module draws
// from (scope_sequences, the 0213 convention): one counter per scope,
// gaps allowed, never reused, exactly the recipe every adapter write
// runs inside its own transaction.
const (
	runsSyncScope  = "runs"
	stopsSyncScope = "stops"
	podSyncScope   = "pod"
)

// allocateSyncSeqTx draws the next seq of one scope inside the caller's
// transaction, the same upsert every module write path runs.
func allocateSyncSeqTx(ctx context.Context, tx pgx.Tx, scope string) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, scope).Scan(&next); err != nil {
		return 0, fmt.Errorf("dispatch: seq allocate for scope %s: %w", scope, err)
	}
	return next, nil
}

// syncSeqCTE is the data-modifying CTE the repository's single-statement
// write paths prepend to draw a fresh seq atomically in the same
// statement (no wrapping transaction exists on those paths, and a
// separate allocate-then-write round trip could interleave two writers).
// Gaps from no-op updates are allowed by the 0213 convention.
const syncSeqCTE = `WITH seq AS (
	INSERT INTO scope_sequences (scope) VALUES ('%s')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// refused wraps one of the module's invariant refusals in the Store
// seam's classified marker, so the engine reports it as that operation's
// rejected result with the module's own words rather than as an
// operational fault that fails the whole push.
func refused(err error) error {
	return fmt.Errorf("%w: %w", sync.ErrOpRefused, err)
}

// projection marshals one row struct through its json tags into the
// wire map the contract's SyncEntity.data carries, so omitempty and the
// pointer-null legs land exactly as the REST surface writes them.
func projection(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("dispatch: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("dispatch: sync projection decode: %w", err)
	}
	return data, nil
}

// syncActor resolves the ERP user a push attributes its write to: the
// linked users row for the caller's platform subject, else the empty
// string (the module's system-actor convention, the NULL its own REST
// writes stamp for a system act).
func syncActor(ctx context.Context, link syncLinker, subject string) string {
	if link == nil || subject == "" {
		return ""
	}
	if uid, ok := link(ctx, subject); ok {
		return uid
	}
	return ""
}

// syncCreateFields is the shape every adapter create shares: the row id
// when the client generated one, else the server assigns it.
func syncCreateID(fields map[string]any) (string, error) {
	id, has := fields["id"].(string)
	if !has || id == "" {
		return "", nil
	}
	if !looksLikeUUID(id) {
		return "", fmt.Errorf("dispatch: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	return id, nil
}
