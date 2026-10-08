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
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

// SyncRoleResolver is the authority seam the driver fence reads: the
// bridged ERP role code of the caller's resolved staff authority, the
// same resolution the guards ran for the request, read again here
// because the Store seam carries the caller as the sync core's
// ScopeFilter alone, which names the actor and the org and no role. The
// composition root supplies it over identity's own resolver (cmd/core
// wires it); a nil seam is legal and the fence then keys on the linked
// driver record alone, so the adapters stay provable without identity
// beside them.
type SyncRoleResolver func(ctx context.Context, platformUserID, orgID string) (code string, has bool, err error)

// driverFence is the driver arm one caller's scope predicate carries:
// driver reports whether the caller is held to the driver fence at all,
// and driverID names the dispatch_drivers.id whose runs are theirs. A
// driver caller with no linked record carries driver with an empty
// driverID: MyRuns answers that person "you are not set up as a driver"
// and the sync read's honest shape is the same sentence said as an empty
// feed, never the whole org.
type driverFence struct {
	driver   bool
	driverID string
}

// resolveDriverFence resolves the driver arm for one caller. The chain
// is the module's own: the filter's actor (a platform subject) through
// the linker seam to the ERP users.id, then this module's own
// dispatch_drivers row for that user (the table the REST surface's
// MyRuns resolves the same way), so no cross-module table is read. The
// role seam, when wired, decides who the fence binds: a DRIVERS-role
// caller is held to their own runs (or to nothing when unlinked), and
// every other role keeps the org scope even when a driver record links
// to their login, the two-role edge the REST surface's
// completionAuthority resolves the same way (role first, ownership
// second). Without the seam the record alone decides, the adapters'
// test posture.
func resolveDriverFence(ctx context.Context, pool *pgxpool.Pool, link syncLinker, role SyncRoleResolver, f sync.ScopeFilter) (driverFence, error) {
	if f.Tenant || f.Actor == "" {
		return driverFence{}, nil
	}
	recordID := ""
	if link != nil && pool != nil {
		if userID, ok := link(ctx, f.Actor); ok && userID != "" {
			var id string
			err := pool.QueryRow(ctx,
				`SELECT id::text FROM dispatch_drivers WHERE user_id = $1::uuid AND is_active
				 ORDER BY created_at LIMIT 1`, userID).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return driverFence{}, fmt.Errorf("dispatch: sync driver fence record read: %w", err)
			}
			recordID = id
		}
	}
	if role != nil {
		code, has, err := role(ctx, f.Actor, f.OrgID)
		if err != nil {
			return driverFence{}, fmt.Errorf("dispatch: sync driver fence role read: %w", err)
		}
		if has {
			if code == identity.RoleDrivers {
				return driverFence{driver: true, driverID: recordID}, nil
			}
			return driverFence{}, nil
		}
	}
	if recordID != "" {
		return driverFence{driver: true, driverID: recordID}, nil
	}
	// FAILS OPEN BY DESIGN on one input: the role seam is wired but names
	// no staff authority for the actor (has == false) and no driver
	// record links. The sync door's guards turn a caller with no staff
	// authority away before any store is reached, so this arm is the
	// desk's org scope for a caller the fence has no basis to hold, not
	// a driver's. The fixtures that exercise the stores directly rely on
	// it. If a scope is ever served to a caller the guards do not vet,
	// this is the line that must fail closed instead.
	return driverFence{}, nil
}

// leg renders the driver arm for a row-addressed query. col is the
// row's reach expression given the rendered placeholder for the bound
// driver id: delivery_runs.driver_id compared directly, or the reach
// through a run for the tables that hang off one. Every shape binds
// exactly one argument, so the call site appends without branching: the
// desk caller binds NULL and the arm short-circuits true, a driver with
// a record binds their dispatch_drivers.id, and a driver with none gets
// the arm that matches no row (nothing is theirs).
func (d driverFence) leg(col func(ph string) string, ph int) (string, any) {
	p := syncPH(ph)
	if !d.driver {
		return " AND (" + p + "::uuid IS NULL OR " + col(p) + ")", nil
	}
	if d.driverID == "" {
		return " AND FALSE", nil
	}
	return " AND " + col(p), d.driverID
}

// runDriverCol, stopDriverCol and podDriverCol are the reach
// expressions the adapters' row-addressed statements carry: a run's own
// driver_id, a stop's run, and a POD photo's stop through to its run.
func runDriverCol(p string) string { return "driver_id = " + p + "::uuid" }
func stopDriverCol(p string) string {
	return "run_id IN (SELECT id FROM delivery_runs WHERE driver_id = " + p + "::uuid)"
}
func podDriverCol(p string) string {
	return "EXISTS (SELECT 1 FROM delivery_stops dsp JOIN delivery_runs drp ON drp.id = dsp.run_id WHERE dsp.id = p.stop_id AND drp.driver_id = " + p + "::uuid)"
}

// syncPH renders placeholder ph, the numbering every query that appends
// legs past the predicate's own args computes off len(args).
func syncPH(ph int) string { return "$" + strconv.Itoa(ph) }

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

// DriverStopFence is the attachments scope's window onto this module's
// driver fence: which of a set of candidate stops sit on runs assigned
// to the driver the platform subject names. The attachments module owns
// no run and no stop (its links reach stops polymorphically), so it
// asks the owner through this seam instead of reading the tables; the
// composition root wires NewDriverStopFence behind it.
type DriverStopFence struct {
	pool *pgxpool.Pool
	link syncLinker
	role SyncRoleResolver
}

// NewDriverStopFence wires the seam over the same linker and role
// resolver the drive-run adapters fence through.
func NewDriverStopFence(pool *pgxpool.Pool, link syncLinker, role SyncRoleResolver) *DriverStopFence {
	return &DriverStopFence{pool: pool, link: link, role: role}
}

// StopsOnDriverRuns answers which candidate stops sit on runs assigned
// to the driver the actor names. isDriver is false for every caller who
// is not held to the driver fence (the desk logins, whose scope stays
// the org's) and true for a driver caller, whose answer is the subset
// on their own runs: empty when no candidate is theirs, and empty for a
// driver login that links to no driver record, the same fail-closed
// shape the sync scopes' own pulls carry. Whether a caller is held does
// not depend on the candidates: a driver asked about no stops is still a
// driver, so a page that reaches no stop is held to nothing.
func (f *DriverStopFence) StopsOnDriverRuns(ctx context.Context, orgID, actor string, candidates []string) (map[string]bool, bool, error) {
	if f == nil || orgID == "" || actor == "" {
		return map[string]bool{}, false, nil
	}
	d, err := resolveDriverFence(ctx, f.pool, f.link, f.role, sync.ScopeFilter{OrgID: orgID, Actor: actor})
	if err != nil {
		return nil, false, err
	}
	if !d.driver {
		return map[string]bool{}, false, nil
	}
	out := map[string]bool{}
	if d.driverID == "" || len(candidates) == 0 {
		return out, true, nil
	}
	rows, err := f.pool.Query(ctx, `
		SELECT s.id::text FROM delivery_stops s
		JOIN delivery_runs r ON r.id = s.run_id
		WHERE s.org_id = $1::uuid AND r.driver_id = $2::uuid AND s.id = ANY($3::uuid[])`,
		orgID, d.driverID, candidates)
	if err != nil {
		return nil, true, fmt.Errorf("dispatch: driver stop fence read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, true, fmt.Errorf("dispatch: driver stop fence scan: %w", err)
		}
		out[id] = true
	}
	return out, true, rows.Err()
}
