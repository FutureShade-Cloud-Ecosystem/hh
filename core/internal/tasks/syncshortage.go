// syncshortage.go is the shortages sync adapter (HH-3.5 #166): the tasks
// module's Store implementation for the shortages scope, the yard-pick
// feed of task_shortages rows (0095). It lives beside the pick_tasks
// adapter for the same fence reason: the module owns task_shortages and
// tasks, every query below carries the caller's org predicate through the
// parent task (the shortage table itself carries no org column; its scope
// IS its task's), and the sync core touches no table.
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

// scanSyncShortage reads the 16-target row (the 14 Shortage targets of
// scanShortage plus seq and deleted; pgx cannot split one row across
// scanners).
func scanSyncShortage(row rowScanner) (syncShortageRow, error) {
	var s Shortage
	var lineNo *int
	var resolvedAt *time.Time
	var seq int64
	var deleted bool
	if err := row.Scan(
		&s.ID, &s.TaskID, &lineNo, &s.ProductID,
		&s.SKU, &s.Description, &s.QtyShort, &s.UOM, &s.Note,
		&s.ReportedBy, &s.ReportedAt,
		&resolvedAt, &s.ResolvedBy, &s.Resolution,
		&seq, &deleted,
	); err != nil {
		return syncShortageRow{}, err
	}
	s.OrderLineNo = lineNo
	s.ResolvedAt = resolvedAt
	return syncShortageRow{shortage: s, seq: seq, deleted: deleted}, nil
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

// NewShortageSyncStore wires the adapter.
func NewShortageSyncStore(pool *pgxpool.Pool, link identity.UserIDLinker) *ShortageSyncStore {
	return &ShortageSyncStore{pool: pool, link: link}
}

// Pull returns the page of shortage changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// predicate.
func (s *ShortageSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := shortageScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM task_shortages
		WHERE %s AND seq IS NOT NULL AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, shortageSyncColumns, where)
	args = append(args, since, limit)
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
// key the row ever applied, so multi-operation outboxes replay clean).
func (s *ShortageSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := shortageScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM task_shortages
		WHERE %s
		  AND (idempotency_key = $2::uuid
		       OR EXISTS (SELECT 1 FROM task_events e
		                   WHERE e.task_id = task_shortages.task_id
		                     AND e.note = $3 || task_shortages.id::text || ' ' || $2))
		LIMIT 1`, shortageSyncColumns, where)
	args = append(args, key, shortageIdemPrefix)
	r, err := scanSyncShortage(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("tasks: shortage sync find applied: %w", err)
	}
	rs, err := shortageRowState(r)
	return rs, true, err
}

// Load reads one shortage row by id under the org predicate; a row outside
// the caller's org is the same as an absent one (existence never leaks).
func (s *ShortageSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.pool, f, id)
}

// shortageQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type shortageQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *ShortageSyncStore) load(ctx context.Context, db shortageQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := shortageScopePredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM task_shortages WHERE id = $2::uuid AND %s`,
		shortageSyncColumns, where)
	args = append(args, id)
	r, err := scanSyncShortage(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("tasks: shortage sync load: %w", err)
	}
	rs, err := shortageRowState(r)
	return rs, true, err
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
	var parentStatus, parentType string
	if err := tx.QueryRow(ctx,
		`SELECT status, task_type FROM tasks WHERE id = $1::uuid AND org_id = $2::uuid FOR UPDATE`,
		taskID, f.OrgID).Scan(&parentStatus, &parentType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("tasks: shortage sync parent lock: %w", err)
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
		// taken, and WHERE decides the refusal, never leaking existence
		// across the scope boundary.
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
	if err := tx.QueryRow(ctx, `
		SELECT (resolved_at IS NOT NULL) FROM task_shortages
		WHERE id = $1::uuid AND deleted_at IS NULL
		  AND EXISTS (SELECT 1 FROM tasks pt WHERE pt.id = task_shortages.task_id AND pt.org_id = $2::uuid)
		FOR UPDATE`,
		id, f.OrgID).Scan(&resolved); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("tasks: lock %q for shortage sync write: %w", id, err)
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
	seq, err := allocateScopeSeqTx(ctx, tx, shortagesSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncShortage(tx.QueryRow(ctx, `
		UPDATE task_shortages SET
			deleted_at      = now(),
			seq             = $3,
			idempotency_key = $4::uuid
		WHERE id = $1::uuid
		  AND EXISTS (SELECT 1 FROM tasks pt WHERE pt.id = task_shortages.task_id AND pt.org_id = $2::uuid)
		  AND deleted_at IS NULL
		RETURNING `+shortageSyncColumns,
		id, f.OrgID, seq, key))
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
