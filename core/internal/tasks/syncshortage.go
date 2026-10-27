// syncshortage.go is the shortages sync adapter (HH-3.5 #166): the tasks
// module's Store implementation for the shortages scope, the yard-pick
// feed of task_shortages rows (0095). It lives beside the pick_tasks
// adapter for the same fence reason: the module owns task_shortages and
// tasks, every query below carries the caller's org predicate through the
// parent task (the shortage table itself carries no org column; its scope
// IS its task's), and the sync core touches no table. The reads hold the
// caller's branch reach the way the writes hold the branch fence (UX-105):
// a shortage's branch is its task's, read through the parent task, the
// pick scope's pull rule (567) and the weights scope's (594).
//
// The projection is the contract's TaskShortage schema, the same json
// shape the module's own yard surface serves. The policy is manual (the
// confirmed table): a stale device write never overwrites the server row,
// the server row is what the client is told to show, and the engine
// writes the review marker; a fresh write applies its declared fields
// only, the module's own bounds enforced exactly as FlagShortage enforces
// them.
//
// The report's append-only discipline (0095: the original report is never
// edited) governs the DESK. The offline yard corrects its own report
// through this scope (gloves mis-type a quantity); a fresh correction
// lands, a stale one becomes the review card the manual policy exists to
// surface, and a RESOLVED shortage's report is closed: no correction of
// a shortage the counter already settled, the same rule ResolveShortage
// enforces against a second resolver.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// shortagesSeqScope names the scope_sequences counter row every
// task_shortages write draws from: the manifest's shortages scope, the
// 0213 convention (one counter row per scope). The REST report and
// resolve paths draw it too (yard_repository.go), so a shortage a device
// has pulled reappears in the feed the moment the desk clears it.
const shortagesSeqScope = "shortages"

// shortageIdemPrefix heads the task_events note that records one applied
// sync operation's idempotency key for one shortage row. The events table
// is the module's append-only audit and the pilot's applied-key history
// pattern (an idem row is not a transition); the shortage's history rides
// its parent task's log with this distinguishable prefix naming the
// shortage row, so an outbox holding several operations on one shortage
// replays without re-applying the earlier ones and the pick adapter's
// own idem lookup ('sync idem <key>', task-scoped) can never match one.
const shortageIdemPrefix = "shortage idem "

// shortageIdemNote builds the note recording one key for one shortage row.
func shortageIdemNote(shortageID, key string) string {
	return shortageIdemPrefix + shortageID + " " + key
}

// shortageScopePredicate is the WHERE predicate every adapter query runs
// under: the caller's org stamp resolved through the parent task (the
// module's own tables, never a cross-module join). A tenant filter or an
// empty org fails closed and answers nothing, the same refusal shape the
// pick adapter carries (the route's 403 has already run; this is the
// adapter's own belt and braces).
func shortageScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return `EXISTS (SELECT 1 FROM tasks pt WHERE pt.id = task_shortages.task_id AND pt.org_id = $1::uuid)`,
		[]any{f.OrgID}, true
}

// shortageSyncColumns is the sync projection: shortageColumns' targets
// plus the sync legs (seq, tombstone flag), unqualified so the same
// constant serves the SELECTs, the UPDATE ... RETURNING legs and the
// INSERT's; the correlated predicates spell the outer table out in full
// (the pilot's own no-alias discipline, for the same reason). The
// unqualified shortageColumns stays the REST projection's own; the two
// scanners share column MEANING by reading the same underlying columns.
const shortageSyncColumns = `id::text, task_id::text, order_line_no,
	COALESCE(product_id::text, ''), sku, description, qty_short::text, uom, note,
	COALESCE(reported_by::text, ''), reported_at,
	resolved_at, COALESCE(resolved_by::text, ''), resolution,
	seq, (deleted_at IS NOT NULL) AS deleted`

// syncShortageRow is one scanned row: the Shortage projection plus sync legs.
type syncShortageRow struct {
	shortage Shortage
	seq      int64
	deleted  bool
}

// shortageSyncTargets is the sync projection's 16 scan targets in order,
// the one list every scanner of the projection builds from, so column
// meaning cannot drift between them (the guard the column constants carry
// on the SELECT side). lineNo and resolvedAt arrive as the scan
// destinations the optional legs land in.
func shortageSyncTargets(r *syncShortageRow, lineNo **int, resolvedAt **time.Time) []any {
	return []any{
		&r.shortage.ID, &r.shortage.TaskID, lineNo, &r.shortage.ProductID,
		&r.shortage.SKU, &r.shortage.Description, &r.shortage.QtyShort, &r.shortage.UOM, &r.shortage.Note,
		&r.shortage.ReportedBy, &r.shortage.ReportedAt,
		resolvedAt, &r.shortage.ResolvedBy, &r.shortage.Resolution,
		&r.seq, &r.deleted,
	}
}

// scanSyncShortage reads the 16-target row (the 14 Shortage targets of
// scanShortage plus seq and deleted; pgx cannot split one row across
// scanners).
func scanSyncShortage(row rowScanner) (syncShortageRow, error) {
	var r syncShortageRow
	var lineNo *int
	var resolvedAt *time.Time
	if err := row.Scan(shortageSyncTargets(&r, &lineNo, &resolvedAt)...); err != nil {
		return syncShortageRow{}, err
	}
	r.shortage.OrderLineNo = lineNo
	r.shortage.ResolvedAt = resolvedAt
	return r, nil
}

// scanSyncShortageBranch is scanSyncShortage over the 17-target row that
// also reads the parent task's branch, the caller-reach check's input: the
// shortage table carries no branch (its scope IS its task's), so the leg
// rides the row's own query exactly as the write paths' locks read it.
func scanSyncShortageBranch(row rowScanner) (syncShortageRow, string, error) {
	var r syncShortageRow
	var lineNo *int
	var resolvedAt *time.Time
	var branch string
	if err := row.Scan(append(shortageSyncTargets(&r, &lineNo, &resolvedAt), &branch)...); err != nil {
		return syncShortageRow{}, "", err
	}
	r.shortage.OrderLineNo = lineNo
	r.shortage.ResolvedAt = resolvedAt
	return r, branch, nil
}

// shortageData projects one Shortage into the wire shape the contract's
// TaskShortage schema pins, the same json tags the module's own surface
// serves (a marshal round trip, so NUMERIC-as-string and the optional
// legs land exactly as the REST surface writes them).
func shortageData(s Shortage) (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("tasks: shortage sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("tasks: shortage sync projection decode: %w", err)
	}
	return data, nil
}

// shortageRowState folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func shortageRowState(r syncShortageRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.shortage.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := shortageData(r.shortage)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// ShortageSyncStore is the shortages Store. It needs the staff identity
// linker for the audit attribution exactly as the pick adapter does;
// optional in tests, wired in production.
type ShortageSyncStore struct {
	pool *pgxpool.Pool
	link identity.UserIDLinker
	// noticer posts the counter's chat event for a created shortage, the direct door's twin.
	noticer ShortageNoticer
	// staff is the branch fence's authority seam (UX-77): the caller's staff context in the org. The reads'
	// branch reach holds to it too (UX-105). Nil leaves the door unfenced by branch, the unwired posture the
	// composition root asserts against at boot.
	staff identity.SyncStaffResolver
	// branchOrg bounds a GLOBAL caller's reach to the branches of its OWN org (UX-81 round 5), the pick and
	// weights doors' seam. Nil leaves the org bound off, the unwired posture.
	branchOrg BranchOrgReader
}

// ShortageNoticer is the counter notice the direct door posts after it stores a shortage: the tasks
// Service satisfies it. Best effort by contract; nil posts nothing.
type ShortageNoticer interface {
	NoticeShortage(ctx context.Context, taskID, shortageID string)
}

// WithNoticer wires the counter notice a created shortage owes.
func (s *ShortageSyncStore) WithNoticer(n ShortageNoticer) *ShortageSyncStore {
	s.noticer = n
	return s
}

// WithStaffAuthority wires the branch fence's authority seam: the caller's staff context in the org, read
// here because the sync core's ScopeFilter carries the subject and the org and no role.
func (s *ShortageSyncStore) WithStaffAuthority(r identity.SyncStaffResolver) *ShortageSyncStore {
	s.staff = r
	return s
}

// WithBranchOrgReader wires the branch to org seam the caller's reach is bounded by (UX-81 round 5), the
// pick and weights doors' seam: a GLOBAL caller's reach is every branch of its OWN org, never every branch
// in the database.
func (s *ShortageSyncStore) WithBranchOrgReader(r BranchOrgReader) *ShortageSyncStore {
	s.branchOrg = r
	return s
}

// StaffAuthorityWired reports whether the branch fence's seam is wired, the leg the boot fail fast pins.
func (s *ShortageSyncStore) StaffAuthorityWired() bool { return s.staff != nil }

// BranchOrgWired reports whether the org bound's seam is wired, the leg the boot fail fast pins beside the
// staff authority's: a box without it serves a GLOBAL caller whose reach is every branch in the database.
func (s *ShortageSyncStore) BranchOrgWired() bool { return s.branchOrg != nil }

// NewShortageSyncStore wires the adapter.
func NewShortageSyncStore(pool *pgxpool.Pool, link identity.UserIDLinker) *ShortageSyncStore {
	return &ShortageSyncStore{pool: pool, link: link}
}

// Pull returns the page of shortage changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// predicate and the caller's branch reach.
func (s *ShortageSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := shortageScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	// The branch reach clause is the pick scope's pull rule (SyncStore.pullReach), part of the query so the
	// page fills with the rows the caller reaches and `seq > since` stays exact (UX-105, the weights scope's
	// shape). The clause rides the parent task: a GLOBAL caller adds none and keeps the org predicate alone,
	// a shortage whose task has no branch is no branch scoped caller's, and an unwired authority fences
	// nothing, as the writes' branchFence.
	reachSQL := ""
	var branches []string
	if s.staff != nil {
		var reach bool
		var err error
		branches, reach, err = (&SyncStore{staff: s.staff}).pullReach(ctx, f)
		if err != nil {
			return nil, nil, err
		}
		if !reach {
			return []sync.Entity{}, []sync.Tombstone{}, nil
		}
		if branches != nil {
			reachSQL = " AND EXISTS (SELECT 1 FROM tasks rt WHERE rt.id = task_shortages.task_id AND rt.branch_id = ANY($4::uuid[]))"
		}
	}
	q := fmt.Sprintf(`SELECT %s FROM task_shortages
		WHERE %s AND seq IS NOT NULL AND seq > $2%s
		ORDER BY seq ASC
		LIMIT $3`, shortageSyncColumns, where, reachSQL)
	args = append(args, since, limit)
	if branches != nil {
		args = append(args, branches)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("tasks: shortage sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncShortage(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("tasks: shortage sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.shortage.ID, Seq: r.seq})
			continue
		}
		data, err := shortageData(r.shortage)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.shortage.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("tasks: shortage sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the shortage row it applied:
// the row's idempotency_key column (the latest key) or the applied-key
// history the adapter records in the parent task's task_events log (every
// key the row ever applied, so multi-operation outboxes replay clean). A
// replay never answers for a shortage the caller cannot pull (UX-105): the
// key's row out of reach is not "applied" for this caller, the same answer
// an absent key gets (the pick scope's fence, 567 round 2).
func (s *ShortageSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := shortageScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	// The reach clause is part of the query (the pick scope's pull rule, PR 640's shape), so the unordered
	// LIMIT 1 can only ever land on the caller's own rows: one key can name shortages on two branches (the
	// table carries no unique index on idempotency_key, 0213), and a post query check would let the other
	// branch's row win the LIMIT and answer "not applied" to the caller that owns the other row. A GLOBAL
	// caller adds no clause and keeps the org predicate alone; a shortage whose task has no branch is no
	// branch scoped caller's; an unwired authority fences nothing, as the writes' branchFence.
	reachSQL := ""
	var branches []string
	if s.staff != nil {
		var reach bool
		var err error
		branches, reach, err = (&SyncStore{staff: s.staff}).pullReach(ctx, f)
		if err != nil {
			return sync.RowState{}, false, err
		}
		if !reach {
			return sync.RowState{}, false, nil
		}
		if branches != nil {
			reachSQL = " AND EXISTS (SELECT 1 FROM tasks rt WHERE rt.id = task_shortages.task_id AND rt.branch_id = ANY($4::uuid[]))"
		}
	}
	q := fmt.Sprintf(`SELECT %s, (SELECT COALESCE(pt.branch_id::text, '') FROM tasks pt WHERE pt.id = task_shortages.task_id)
		FROM task_shortages
		WHERE %s
		  AND (idempotency_key = $2::uuid
		       OR EXISTS (SELECT 1 FROM task_events e
		                   WHERE e.task_id = task_shortages.task_id
		                     AND e.note = $3 || task_shortages.id::text || ' ' || $2))%s
		LIMIT 1`, shortageSyncColumns, where, reachSQL)
	args = append(args, key, shortageIdemPrefix)
	if branches != nil {
		args = append(args, branches)
	}
	r, branch, err := scanSyncShortageBranch(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("tasks: shortage sync find applied: %w", err)
	}
	// The org bound rides the answer: a GLOBAL caller's reach is every branch of its OWN org, never every
	// branch in the database (UX-81 round 5), the branchOrg seam the pick and weights doors carry. A row on
	// a branch outside the caller's org is not "applied" for it, the same answer an absent key gets.
	if reach, err := s.callerReaches(ctx, f, branch); err != nil || !reach {
		return sync.RowState{}, false, err
	}
	rs, err := shortageRowState(r)
	return rs, true, err
}

// Load reads one shortage row by id under the org predicate; a row outside
// the caller's org, or on a branch outside the caller's reach (UX-105), is
// the same as an absent one (existence never leaks).
func (s *ShortageSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	rs, branch, ok, err := s.loadWithBranch(ctx, s.pool, f, id)
	if err != nil || !ok {
		return sync.RowState{}, false, err
	}
	// The row is the caller's to see only under the pull's reach (UX-105): the engine puts what Load returns
	// into a stale write's conflict envelope, so a row the caller cannot pull is answered as absent ("row
	// not found", existence never leaks) and the envelope can never carry it. The branch rides the parent
	// task the same leg the write paths' locks read, so a tombstoned row's reach check works too.
	if reach, err := s.callerReaches(ctx, f, branch); err != nil || !reach {
		return sync.RowState{}, false, err
	}
	return rs, true, nil
}

// shortageQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type shortageQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is the org-held read the create's collision probe uses, over a querier. It does NOT hold the row to
// the caller's branch reach: the collision is inside the caller's own org, so it is the caller's own fact
// and answers ErrRowExists, the pick door's rule (its collision probe reads the row under the org
// predicate alone). A caller inside a transaction passes the transaction, so the read rides the connection
// it already holds instead of asking the shared pool for a second one (connection starvation by second
// acquisition when pool-size callers do this together).
func (s *ShortageSyncStore) load(ctx context.Context, db shortageQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	rs, _, ok, err := s.loadWithBranch(ctx, db, f, id)
	return rs, ok, err
}

// loadWithBranch is load that also answers the row's branch (empty for none), the reach check's input: the
// shortage table carries no branch (its scope IS its task's), so the leg rides the row's own query exactly
// as the write paths' locks read it.
func (s *ShortageSyncStore) loadWithBranch(ctx context.Context, db shortageQuerier, f sync.ScopeFilter, id string) (sync.RowState, string, bool, error) {
	where, args, ok := shortageScopePredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, "", false, nil
	}
	q := fmt.Sprintf(`SELECT %s, (SELECT COALESCE(pt.branch_id::text, '') FROM tasks pt WHERE pt.id = task_shortages.task_id)
		FROM task_shortages WHERE id = $2::uuid AND %s`,
		shortageSyncColumns, where)
	args = append(args, id)
	r, branch, err := scanSyncShortageBranch(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, "", false, nil
	}
	if err != nil {
		return sync.RowState{}, "", false, fmt.Errorf("tasks: shortage sync load: %w", err)
	}
	rs, err := shortageRowState(r)
	return rs, branch, true, err
}

// shortageWritableCreate is the field set a pushed create may declare: the
// report itself. The id is the row address, task_id the parent pick, and
// the resolution legs are absent by construction (a create cannot birth a
// resolved shortage). reported_by is attributed from the caller, never
// declared by the client.
func shortageWritableCreate(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "task_id", "order_line_no", "product_id", "sku", "description", "qty_short", "uom", "note":
		default:
			return fmt.Errorf("tasks: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// shortageWritable is the field set a pushed update may declare: the
// report's correctable legs (what the yard typed with gloves on). The
// resolution legs are the counter's act and refuse here.
var shortageWritable = map[string]bool{
	"id": true, "qty_short": true, "uom": true, "sku": true, "description": true, "note": true,
}

// shortageCheckWritable refuses a payload declaring anything outside the
// writable set.
func shortageCheckWritable(fields map[string]any) error {
	for k := range fields {
		if !shortageWritable[k] {
			return fmt.Errorf("tasks: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// shortageValidateReport checks the report legs against the module's own
// FlagShortage bounds: description required and bounded, qty_short a
// positive decimal string, uom defaulting to EA, note bounded.
func shortageValidateReport(fields map[string]any) (desc, qty, uom, note string, lineNo *int, productID string, err error) {
	desc, _ = fields["description"].(string)
	if strings.TrimSpace(desc) == "" {
		return "", "", "", "", nil, "", fmt.Errorf("tasks: description is required: %w", sync.ErrOpRefused)
	}
	if len(desc) > 400 {
		return "", "", "", "", nil, "", fmt.Errorf("tasks: description is too long (max 400): %w", sync.ErrOpRefused)
	}
	qty, ok := fields["qty_short"].(string)
	if !ok {
		return "", "", "", "", nil, "", fmt.Errorf("tasks: qty_short must be a decimal string: %w", sync.ErrOpRefused)
	}
	parsed, perr := parsePositiveQty(qty)
	if perr != nil {
		return "", "", "", "", nil, "", fmt.Errorf("tasks: qty_short: %v: %w", perr, sync.ErrOpRefused)
	}
	qty = parsed
	uom, _ = fields["uom"].(string)
	if uom == "" {
		uom = "EA"
	}
	note, _ = fields["note"].(string)
	if len(note) > 400 {
		return "", "", "", "", nil, "", fmt.Errorf("tasks: note is too long (max 400): %w", sync.ErrOpRefused)
	}
	if raw, ok := fields["order_line_no"]; ok && raw != nil {
		switch v := raw.(type) {
		case float64:
			if v <= 0 || v != float64(int(v)) {
				return "", "", "", "", nil, "", fmt.Errorf("tasks: order_line_no must be a positive integer: %w", sync.ErrOpRefused)
			}
			n := int(v)
			lineNo = &n
		case int:
			if v <= 0 {
				return "", "", "", "", nil, "", fmt.Errorf("tasks: order_line_no must be a positive integer: %w", sync.ErrOpRefused)
			}
			lineNo = &v
		default:
			return "", "", "", "", nil, "", fmt.Errorf("tasks: order_line_no must be a positive integer: %w", sync.ErrOpRefused)
		}
	}
	if raw, ok := fields["product_id"]; ok && raw != nil {
		productID, _ = raw.(string)
		if productID != "" && !looksLikeUUID(productID) {
			return "", "", "", "", nil, "", fmt.Errorf("tasks: product_id must be a uuid: %w", sync.ErrOpRefused)
		}
	}
	return desc, qty, uom, note, lineNo, productID, nil
}

// actorERP resolves the audit actor from the filter's caller identity,
// the pick adapter's same resolution.
func (s *ShortageSyncStore) actorERP(ctx context.Context, f sync.ScopeFilter) string {
	if s.link == nil || f.Actor == "" {
		return ""
	}
	if uid, ok := s.link(ctx, f.Actor); ok {
		return uid
	}
	return ""
}

// Create inserts one shortage from a pushed create: the parent task
// validates in the caller's org (the module's own tables), the row stamps
// a freshly allocated shortages seq and the idempotency key, and the idem
// history lands on the parent's task_events log, all in one transaction.
// A client-chosen id that already exists refuses whole, the existence
// answer scoped the same way the pick adapter scopes it.
func (s *ShortageSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := shortageScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := shortageWritableCreate(fields); err != nil {
		return sync.RowState{}, err
	}
	taskID, _ := fields["task_id"].(string)
	if !looksLikeUUID(taskID) {
		return sync.RowState{}, fmt.Errorf("tasks: task_id must be a uuid: %w", sync.ErrOpRefused)
	}
	desc, qty, uom, note, lineNo, productID, err := shortageValidateReport(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	sku, _ := fields["sku"].(string)
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("tasks: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	actor := s.actorERP(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin shortage sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The parent task must exist in the caller's org: a shortage against
	// another org's pick is that org's fact, and the refusal is the same
	// not-found that an absent task reads (existence never leaks).
	var parentStatus, parentType, parentBranch string
	if err := tx.QueryRow(ctx,
		`SELECT status, task_type, COALESCE(branch_id::text, '') FROM tasks WHERE id = $1::uuid AND org_id = $2::uuid FOR UPDATE`,
		taskID, f.OrgID).Scan(&parentStatus, &parentType, &parentBranch); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync parent lock: %w", err)
	}
	// The direct door's taskBranchFence, on the locked parent: a yard caller at one branch does not report
	// a shortage on another branch's pick by naming its task (UX-77).
	if err := s.branchFence(ctx, f, parentBranch); err != nil {
		return sync.RowState{}, err
	}
	seq, err := allocateScopeSeqTx(ctx, tx, shortagesSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncShortage(tx.QueryRow(ctx, `
		INSERT INTO task_shortages
			(id, task_id, order_line_no, product_id, sku, description, qty_short, uom, note,
			 reported_by, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3, NULLIF($4,'')::uuid, $5,
		        $6, $7::numeric, $8, $9, NULLIF($10,'')::uuid, $11, $12::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+shortageSyncColumns,
		id, taskID, lineNo, productID, sku, desc, qty, uom, note, actor, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		// The ON CONFLICT arm answered no row: the client-chosen id is
		// taken. Which refusal answers depends on WHERE it is taken:
		// inside the caller's org the collision is the caller's own fact
		// (the row is visible to it), outside it the answer is not-found
		// so existence never leaks across the scope boundary. The probe
		// reads the row under the org predicate alone, the pick door's
		// rule: a shortage on a branch the caller cannot reach is still
		// its own org's fact and answers ErrRowExists.
		if _, own, _ := s.load(ctx, tx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync create insert: %w", err)
	}
	// The parent pick draws its own scope's seq beside the shortage, as the direct door's insert does:
	// the task projection carries the shortage counts, so a device holding the task row must see them
	// move.
	taskSeq, err := allocateTaskSeqTx(ctx, tx, parentType)
	if err != nil {
		return sync.RowState{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET seq = $2 WHERE id = $1::uuid`, taskID, taskSeq); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync bump task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, $2, NULLIF($3,'')::uuid, $4)`,
		taskID, parentStatus, actor, shortageIdemNote(r.shortage.ID, key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit shortage sync create: %w", err)
	}
	// The counter hears of it after the record is safe, as the direct door tells it. Best effort: a
	// messaging blip never fails the write, and a replay never reaches here (the dedup answers first).
	if s.noticer != nil {
		s.noticer.NoticeShortage(ctx, taskID, r.shortage.ID)
	}
	return shortageRowState(r)
}

// Write applies exactly the declared report fields to one shortage row
// under the module's own bounds. A resolved shortage's report is closed
// (the counter settled it; the module refuses a second resolver for the
// same reason). The row locks under the org predicate, draws a fresh
// shortages seq, stamps the idempotency key, and records the idem event.
func (s *ShortageSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := shortageScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := shortageCheckWritable(fields); err != nil {
		return sync.RowState{}, err
	}
	actor := s.actorERP(ctx, f)

	// Validate the declared subset against the module's own bounds. An
	// undeclared leg keeps the row's value, so the validation reads the
	// declared legs only.
	if _, ok := fields["description"]; ok {
		d, _ := fields["description"].(string)
		if strings.TrimSpace(d) == "" || len(d) > 400 {
			return sync.RowState{}, fmt.Errorf("tasks: description is required and bounded (max 400): %w", sync.ErrOpRefused)
		}
	}
	if _, ok := fields["qty_short"]; ok {
		q, _ := fields["qty_short"].(string)
		if _, err := parsePositiveQty(q); err != nil {
			return sync.RowState{}, fmt.Errorf("tasks: qty_short: %v: %w", err, sync.ErrOpRefused)
		}
	}
	if n, ok := fields["note"].(string); ok && len(n) > 400 {
		return sync.RowState{}, fmt.Errorf("tasks: note is too long (max 400): %w", sync.ErrOpRefused)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin shortage sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the row under the org predicate; a resolved shortage refuses.
	var resolved bool
	var branch string
	if err := tx.QueryRow(ctx, `
		SELECT (resolved_at IS NOT NULL),
		       (SELECT COALESCE(pt.branch_id::text, '') FROM tasks pt WHERE pt.id = task_shortages.task_id)
		FROM task_shortages
		WHERE id = $1::uuid AND deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM tasks pt WHERE pt.id = task_shortages.task_id AND pt.org_id = $2::uuid)
		FOR UPDATE`,
		id, f.OrgID).Scan(&resolved, &branch); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("tasks: lock %q for shortage sync write: %w", id, err)
	}
	// The task's branch fence on the locked shortage (UX-77): a yard caller at one branch does not change
	// another branch's shortage by naming its id.
	if err := s.branchFence(ctx, f, branch); err != nil {
		return sync.RowState{}, err
	}
	if resolved {
		return sync.RowState{}, fmt.Errorf("tasks: a resolved shortage's report is closed: %w", sync.ErrOpRefused)
	}
	seq, err := allocateScopeSeqTx(ctx, tx, shortagesSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	// Exactly the declared legs move; undeclared legs keep the row's value.
	r, err := scanSyncShortage(tx.QueryRow(ctx, `
		UPDATE task_shortages SET
			description = CASE WHEN $3::text IS NOT NULL THEN $3 ELSE description END,
			qty_short   = CASE WHEN $4::text IS NOT NULL THEN $4::numeric ELSE qty_short END,
			uom         = CASE WHEN $5::text IS NOT NULL THEN $5 ELSE uom END,
			sku         = CASE WHEN $6::text IS NOT NULL THEN $6 ELSE sku END,
			note        = CASE WHEN $7::text IS NOT NULL THEN $7 ELSE note END,
			seq         = $8,
			idempotency_key = $9::uuid
		WHERE id = $1::uuid
		  AND EXISTS (SELECT 1 FROM tasks pt WHERE pt.id = task_shortages.task_id AND pt.org_id = $2::uuid)
		  AND deleted_at IS NULL
		RETURNING `+shortageSyncColumns,
		id, f.OrgID,
		optString(fields, "description"), optString(fields, "qty_short"), optString(fields, "uom"),
		optString(fields, "sku"), optString(fields, "note"),
		seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync write %q: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, (SELECT status FROM tasks WHERE id = $1), NULLIF($2,'')::uuid, $3)`,
		r.shortage.TaskID, actor, shortageIdemNote(id, key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit shortage sync write: %w", err)
	}
	return shortageRowState(r)
}

// Delete tombstones one shortage row: deleted_at set, the row retained, a
// fresh shortages seq, the idempotency key stamped, the idem event
// recorded, one transaction.
func (s *ShortageSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := shortageScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	actor := s.actorERP(ctx, f)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: begin shortage sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The task's branch fence on the locked shortage (UX-77, main). An absent row falls through to the update's
	// own not-found answer.
	var branch string
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT COALESCE(pt.branch_id::text, '') FROM tasks pt WHERE pt.id = task_shortages.task_id)
		FROM task_shortages
		WHERE id = $1::uuid AND deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM tasks pt WHERE pt.id = task_shortages.task_id AND pt.org_id = $2::uuid)
		FOR UPDATE`, id, f.OrgID).Scan(&branch); err == nil {
		if err := s.branchFence(ctx, f, branch); err != nil {
			return sync.RowState{}, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, fmt.Errorf("tasks: lock %q for shortage sync delete: %w", id, err)
	}
	// The tombstone relies on the sync seq trigger (0422): the row lock above, then the counter. An explicit
	// draw ahead of the UPDATE would take the counter before the row and cross a writer that relies on the
	// trigger (PR 556 round 3c); RETURNING answers the seq the trigger drew.
	r, err := scanSyncShortage(tx.QueryRow(ctx, `
		UPDATE task_shortages SET
			deleted_at      = now(),
			idempotency_key = $3::uuid
		WHERE id = $1::uuid
		  AND EXISTS (SELECT 1 FROM tasks pt WHERE pt.id = task_shortages.task_id AND pt.org_id = $2::uuid)
		  AND deleted_at IS NULL
		RETURNING `+shortageSyncColumns,
		id, f.OrgID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync delete %q: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_events (task_id, from_status, to_status, actor_id, note)
		VALUES ($1::uuid, NULL, (SELECT status FROM tasks WHERE id = $1), NULLIF($2,'')::uuid, $3)`,
		r.shortage.TaskID, actor, shortageIdemNote(id, key)); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync idem event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("tasks: commit shortage sync delete: %w", err)
	}
	rs, err := shortageRowState(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}

// optString reads one optional declared string leg: nil when the payload
// does not declare it (the SQL CASE keeps the row's value), the value
// when it does.
func optString(fields map[string]any, name string) any {
	v, ok := fields[name]
	if !ok {
		return nil
	}
	s, _ := v.(string)
	return s
}

// callerReaches is the pick door's rule over the same seams, never a second copy of it (SyncStore.callerReaches):
// a GLOBAL caller reaches every branch of its OWN org (the branchOrg seam bounds it, UX-81 round 5, never
// every branch in the database), a branch scoped caller its granted branches only, an absent or inactive
// authority nothing, and an unreadable one the plain retryable error. An unwired seam fences nothing, as
// the writes' branchFence: the posture the composition root refuses to boot a door in (StaffAuthorityWired
// and BranchOrgWired are the fail fast's legs).
func (s *ShortageSyncStore) callerReaches(ctx context.Context, f sync.ScopeFilter, branch string) (bool, error) {
	return (&SyncStore{staff: s.staff, branchOrg: s.branchOrg}).callerReaches(ctx, f, branch)
}

// branchFence refuses a write on a task whose branch the caller's staff authority does not reach. An
// unwired seam fences nothing (the composition root refuses to boot that way); a wired seam that finds no
// active standing refuses, never writes unfenced.
func (s *ShortageSyncStore) branchFence(ctx context.Context, f sync.ScopeFilter, branch string) error {
	if s.staff == nil {
		return nil
	}
	return syncBranchFence(ctx, s.staff, f, branch)
}
