// syncscope.go is the cycle_counts sync adapter (HH-3.5 work item
// #168): the invcount module's implementation of the sync core's
// Store seam over count_sheets, the count sheet this module owns. The
// pattern is the pilot's and the drive-run adapters': the sync core
// owns the protocol and no table, this adapter touches only tables
// this module owns, and the rules it enforces are this module's own
// (the OPEN to FROZEN to COUNTED ladder, the snapshot freeze through
// the module's own StockCounter seam, the maker stamp, the POSTED
// sheet that cannot be cancelled).
//
// The scope's policy is manual (the confirmed table): a stale write
// NEVER overwrites, the server row comes back with the review marker,
// and the counter's edit waits on a review card. That is the whole
// point of a count: the sheet that posts is the one somebody can
// defend, and a replayed device edit quietly landing on a sheet the
// desk already moved would be exactly the quiet overwrite the manual
// policy exists to prevent.
//
// THE WRITE WINDOWS, the module's own vocabulary:
//
//	create   an OPEN sheet (branch, scope, notes), the CreateSheet
//	         surface: the sheet_no mints from the module's own
//	         CS-date-uuid pattern.
//	update   the yard's two moves: the freeze (OPEN to FROZEN, the
//	         snapshot captured through the module's own seam exactly as
//	         FreezeSheet captures it) and the submit (FROZEN or COUNTED
//	         to COUNTED, the maker's counted lines riding the payload's
//	         counts array, the module's own SaveCounts upsert with its
//	         approval invalidation). Approval and post are the
//	         checker's and the desk's own doors (four eyes, stock and
//	         GL) and are not reachable through sync.
//	delete   the abandonment: CANCELLED through the module's own rule
//	         (never a POSTED sheet) plus the tombstone.
//
// count_sheets carries no org column (0213 stamped none on it), so the
// org fence is the branch's, the same read every module fence of this
// shape makes.
package invcount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// cycleSyncScope is the per-scope sequence counter (scope_sequences,
// the 0213 convention) every count_sheets write draws from.
const cycleSyncScope = "cycle_counts"

// cycleSeqCTE is the data-modifying CTE the module's single-statement
// write paths prepend, the same shape every module's writers use.
const cycleSeqCTE = `WITH cseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('cycle_counts')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// cycleRefused wraps one of the module's invariant refusals in the
// Store seam's classified marker.
func cycleRefused(err error) error {
	return fmt.Errorf("%w: %w", sync.ErrOpRefused, err)
}

// syncSheetColumns is the pull projection: the module's sheetCols plus
// the sync legs.
const syncSheetColumns = sheetCols + `, seq, (deleted_at IS NOT NULL) AS deleted`

// syncSheetRow is one scanned row of the sync projection.
type syncSheetRow struct {
	sheet   Sheet
	seq     int64
	deleted bool
}

// scanSyncSheet reads sheetCols's targets then the two sync legs. The
// targets re-declare scanSheet's (pgx cannot split one row across
// scanners; the shared sheetCols constant is the guard against drift).
func scanSyncSheet(row pgx.Row) (syncSheetRow, error) {
	var s Sheet
	var seq int64
	var deleted bool
	if err := row.Scan(&s.ID, &s.SheetNo, &s.BranchID, &s.Scope, &s.Status,
		&s.FreezeAt, &s.CountedBy, &s.ApprovedBy, &s.ApprovedAt, &s.PostedAt,
		&s.Notes, &s.CreatedAt, &seq, &deleted); err != nil {
		return syncSheetRow{}, err
	}
	return syncSheetRow{sheet: s, seq: seq, deleted: deleted}, nil
}

// sheetData projects one row through its json tags into the wire map
// the contract's SyncEntity.data carries.
func sheetData(s Sheet) (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("invcount: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("invcount: sync projection decode: %w", err)
	}
	return data, nil
}

// sheetStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func sheetStateOf(r syncSheetRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.sheet.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := sheetData(r.sheet)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// cycleScopePredicate is the WHERE predicate every adapter query runs
// under; cycle_counts is an org scope, so a tenant filter fails
// closed, and the fence is the branch's.
func cycleScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return `branch_id IN (SELECT id FROM locations WHERE org_id = $1::uuid)`, []any{f.OrgID}, true
}

// SyncStore is the cycle_counts Store.
type SyncStore struct {
	pool  *pgxpool.Pool
	stock StockCounter
	link  identity.UserIDLinker
}

// NewSyncStore wires the adapter: the stock seam is the module's own
// (the freeze snapshot), the linker resolves the audit actor and the
// maker stamp.
func NewSyncStore(pool *pgxpool.Pool, stock StockCounter, link identity.UserIDLinker) *SyncStore {
	return &SyncStore{pool: pool, stock: stock, link: link}
}

// Pull returns the page of sheet changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// fence.
func (s *SyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := cycleScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM count_sheets
		WHERE %s AND seq IS NOT NULL AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, syncSheetColumns, where)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("invcount: sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncSheet(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("invcount: sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.sheet.ID, Seq: r.seq})
			continue
		}
		data, err := sheetData(r.sheet)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.sheet.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("invcount: sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// D30 column's latest key, kept through a tombstone.
func (s *SyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := cycleScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM count_sheets
		WHERE %s AND idempotency_key = $2::uuid
		LIMIT 1`, syncSheetColumns, where)
	args = append(args, key)
	r, err := scanSyncSheet(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("invcount: sync find applied: %w", err)
	}
	rs, err := sheetStateOf(r)
	return rs, true, err
}

// Load reads one sheet by id under the org fence; a sheet outside the
// caller's org is the same as an absent one.
func (s *SyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := cycleScopePredicate(f)
	if !ok || !cycleLooksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM count_sheets WHERE id = $2::uuid AND %s`,
		syncSheetColumns, where)
	args = append(args, id)
	r, err := scanSyncSheet(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("invcount: sync load: %w", err)
	}
	rs, err := sheetStateOf(r)
	return rs, true, err
}

// cycleLooksLikeUUID reports whether s is a canonical 36-char uuid
// (the boundary guard every module shares, restated local to this
// package).
func cycleLooksLikeUUID(s string) bool {
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

// cycleActor resolves the ERP user a push attributes its write to.
func (s *SyncStore) cycleActor(ctx context.Context, f sync.ScopeFilter) string {
	if s.link == nil || f.Actor == "" {
		return ""
	}
	if uid, ok := s.link(ctx, f.Actor); ok {
		return uid
	}
	return ""
}

// sheetCreateFields is the field set a pushed create may declare.
func sheetCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "branch_id", "scope", "notes":
		default:
			return fmt.Errorf("invcount: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// Create opens one count sheet from a pushed create: the branch fences
// to the caller's org, the scope validates against the module's own
// vocabulary (FULL, AD_HOC, or a CATEGORY:<id> reference), the number
// mints from the module's own CS-date-uuid pattern, and the row stamps
// a fresh seq and the idempotency key.
func (s *SyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := cycleScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := sheetCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	branchID, _ := fields["branch_id"].(string)
	if !cycleLooksLikeUUID(branchID) {
		return sync.RowState{}, fmt.Errorf("invcount: branch_id must be a uuid: %w", sync.ErrOpRefused)
	}
	var inOrg bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1::uuid AND org_id = $2::uuid)`,
		branchID, f.OrgID).Scan(&inOrg); err != nil {
		return sync.RowState{}, fmt.Errorf("invcount: sync create branch read: %w", err)
	}
	if !inOrg {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	scope, _ := fields["scope"].(string)
	if scope == "" {
		scope = "FULL"
	}
	if scope != "FULL" && scope != "AD_HOC" && !strings.HasPrefix(scope, "CATEGORY:") {
		return sync.RowState{}, fmt.Errorf("invcount: scope must be FULL, AD_HOC or CATEGORY:<id>: %w", sync.ErrOpRefused)
	}
	notes, _ := fields["notes"].(string)
	id, hasID := fields["id"].(string)
	if hasID && id != "" && !cycleLooksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("invcount: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	// count_sheets carries no created_by column: the sheet's people
	// arrive with their own acts (counted_by at the submit,
	// approved_by at the desk), the module's own shape.

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("invcount: begin sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateCycleSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncSheet(tx.QueryRow(ctx, `
		INSERT INTO count_sheets (id, sheet_no, branch_id, scope, notes, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()),
		        'CS-' || to_char(now(),'YYYYMMDD') || '-' || substr(gen_random_uuid()::text,1,8),
		        $2::uuid, $3, $4, $5, $6::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+syncSheetColumns,
		id, branchID, scope, notes, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, own, _ := s.Load(ctx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("invcount: sync create insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("invcount: commit sync create: %w", err)
	}
	return sheetStateOf(r)
}

// parseCountLines reads the counts array a submit carries: the maker's
// counted lines in the module's own CountLineInput shape.
func parseCountLines(v any) ([]CountLineInput, error) {
	switch raw := v.(type) {
	case nil:
		return nil, nil
	case []any:
		// The JSON-decoded shape a request body parses into.
		out := make([]CountLineInput, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invcount: each counted line is an object: %w", sync.ErrOpRefused)
			}
			ln, err := parseCountLine(m)
			if err != nil {
				return nil, err
			}
			out = append(out, ln)
		}
		return out, nil
	case []map[string]any:
		// The Go-built shape an operation object carries in-process.
		out := make([]CountLineInput, 0, len(raw))
		for _, m := range raw {
			ln, err := parseCountLine(m)
			if err != nil {
				return nil, err
			}
			out = append(out, ln)
		}
		return out, nil
	}
	return nil, fmt.Errorf("invcount: counts must be an array of lines: %w", sync.ErrOpRefused)
}

// parseCountLine reads one counted line.
func parseCountLine(m map[string]any) (CountLineInput, error) {
	var ln CountLineInput
	ln.ProductID, _ = m["product_id"].(string)
	if !cycleLooksLikeUUID(ln.ProductID) {
		return ln, fmt.Errorf("invcount: each counted line names a product: %w", sync.ErrOpRefused)
	}
	ln.LocationID, _ = m["location_id"].(string)
	ln.ReasonCode, _ = m["reason_code"].(string)
	switch q := m["counted_qty_base"].(type) {
	case float64:
		ln.CountedQtyBase = q
	case int:
		ln.CountedQtyBase = float64(q)
	case int64:
		ln.CountedQtyBase = float64(q)
	case nil:
		return ln, fmt.Errorf("invcount: each counted line carries counted_qty_base: %w", sync.ErrOpRefused)
	default:
		return ln, fmt.Errorf("invcount: counted_qty_base must be a number: %w", sync.ErrOpRefused)
	}
	return ln, nil
}

// Write applies the yard's move to one sheet under the module's own
// rules, on a FOR UPDATE-locked pre-image: the freeze captures the
// snapshot through the module's own StockCounter seam (refusing
// exactly as FreezeSheet refuses when the seam is unwired), the submit
// runs the module's own SaveCounts upsert (approval invalidation
// included) and stamps the maker. Approval and post are not reachable
// here (the four-eyes and the posting door are the desk's).
func (s *SyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := cycleScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	status, hasStatus := "", false
	counts, hasCounts := []CountLineInput(nil), false
	for k, v := range fields {
		switch k {
		case "id":
			continue
		case "status":
			st, ok := v.(string)
			if !ok {
				return sync.RowState{}, fmt.Errorf("invcount: status must be a string: %w", sync.ErrOpRefused)
			}
			status, hasStatus = st, true
		case "counts":
			lines, err := parseCountLines(v)
			if err != nil {
				return sync.RowState{}, err
			}
			counts, hasCounts = lines, true
		default:
			return sync.RowState{}, fmt.Errorf("invcount: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	if !hasStatus {
		return sync.RowState{}, cycleRefused(errors.New("an update that moves nothing refuses"))
	}
	actor := s.cycleActor(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("invcount: begin sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from, branchID string
	if err := tx.QueryRow(ctx,
		`SELECT status, branch_id::text FROM count_sheets
		 WHERE id = $1::uuid AND deleted_at IS NULL
		   AND branch_id IN (SELECT id FROM locations WHERE org_id = $2::uuid)
		 FOR UPDATE`,
		id, f.OrgID).Scan(&from, &branchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("invcount: lock %q for sync write: %w", id, err)
	}
	switch status {
	case StatusFrozen:
		// The freeze: the module's own gate and its own seam.
		if from != StatusOpen {
			return sync.RowState{}, cycleRefused(fmt.Errorf("only an OPEN sheet can be frozen (sheet is %s)", from))
		}
		if s.stock == nil {
			return sync.RowState{}, cycleRefused(errors.New("inventory snapshot seam not wired"))
		}
		snaps, err := s.stock.SnapshotOnHand(ctx, branchID)
		if err != nil {
			return sync.RowState{}, fmt.Errorf("invcount: snapshot read: %w", err)
		}
		for _, sn := range snaps {
			if _, err := tx.Exec(ctx, `INSERT INTO count_lines
				(sheet_id, product_id, location_id, snapshot_qty_base, unit_cost_cents)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (sheet_id, product_id, location_id) DO NOTHING`,
				id, sn.ProductID, nullStr(sn.LocationID), sn.OnHandBase, sn.AvgCostCents); err != nil {
				return sync.RowState{}, fmt.Errorf("invcount: insert snapshot line: %w", err)
			}
		}
		seq, err := allocateCycleSeqTx(ctx, tx)
		if err != nil {
			return sync.RowState{}, err
		}
		r, err := scanSyncSheet(tx.QueryRow(ctx, `
			UPDATE count_sheets SET status = 'FROZEN', freeze_at = now(), updated_at = now(),
			                         seq = $2, idempotency_key = $3::uuid
			WHERE id = $1::uuid AND status = 'OPEN'
			RETURNING `+syncSheetColumns,
			id, seq, key))
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		if err != nil {
			return sync.RowState{}, fmt.Errorf("invcount: sync freeze %q: %w", id, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("invcount: commit sync freeze: %w", err)
		}
		return sheetStateOf(r)
	case StatusCounted:
		// The submit: the module's own upsert, the maker stamped, the
		// prior approval invalidated exactly as SaveCounts invalidates
		// it.
		if from != StatusFrozen && from != StatusCounted {
			return sync.RowState{}, cycleRefused(errors.New("counts submit onto a frozen sheet (sheet is " + from + ")"))
		}
		if !hasCounts {
			return sync.RowState{}, cycleRefused(errors.New("the submit carries its counted lines"))
		}
		for _, ln := range counts {
			if _, err := tx.Exec(ctx, `INSERT INTO count_lines
				(sheet_id, product_id, location_id, snapshot_qty_base, counted_qty_base, variance_base, reason_code)
				VALUES ($1, $2, $3, 0, $4, $4, $5)
				ON CONFLICT (sheet_id, product_id, location_id) DO UPDATE
				SET counted_qty_base = EXCLUDED.counted_qty_base,
				    variance_base = EXCLUDED.counted_qty_base - count_lines.snapshot_qty_base,
				    reason_code = EXCLUDED.reason_code`,
				id, ln.ProductID, nullStr(ln.LocationID), ln.CountedQtyBase, nullStr(ln.ReasonCode)); err != nil {
				return sync.RowState{}, fmt.Errorf("invcount: save count line: %w", err)
			}
		}
		seq, err := allocateCycleSeqTx(ctx, tx)
		if err != nil {
			return sync.RowState{}, err
		}
		r, err := scanSyncSheet(tx.QueryRow(ctx, `
			UPDATE count_sheets SET status = 'COUNTED', counted_by = NULLIF($2,'')::uuid,
			                         approved_by = NULL, approved_at = NULL, updated_at = now(),
			                         seq = $3, idempotency_key = $4::uuid
			WHERE id = $1::uuid AND status IN ('FROZEN','COUNTED')
			RETURNING `+syncSheetColumns,
			id, actor, seq, key))
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		if err != nil {
			return sync.RowState{}, fmt.Errorf("invcount: sync submit %q: %w", id, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("invcount: commit sync submit: %w", err)
		}
		return sheetStateOf(r)
	}
	return sync.RowState{}, cycleRefused(fmt.Errorf("%s is the desk's own door, the yard freezes and counts", status))
}

// Delete tombstones one sheet and abandons it: CANCELLED through the
// module's own rule (never a POSTED sheet, the posted variances are
// the record), deleted_at set, the row retained, a fresh seq.
func (s *SyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := cycleScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("invcount: begin sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var from string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM count_sheets
		 WHERE id = $1::uuid AND deleted_at IS NULL
		   AND branch_id IN (SELECT id FROM locations WHERE org_id = $2::uuid)
		 FOR UPDATE`,
		id, f.OrgID).Scan(&from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("invcount: lock %q for sync delete: %w", id, err)
	}
	if from == StatusPosted {
		return sync.RowState{}, cycleRefused(errors.New("a POSTED sheet cannot be cancelled"))
	}
	seq, err := allocateCycleSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE count_sheets SET
			status     = 'CANCELLED',
			deleted_at = now(),
			updated_at = now(),
			seq        = $2
		WHERE id = $1::uuid AND deleted_at IS NULL AND status <> 'POSTED'
		RETURNING (deleted_at IS NOT NULL)`, id, seq).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("invcount: sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("invcount: commit sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}

// allocateCycleSeqTx draws the next cycle_counts seq inside the
// caller's transaction.
func allocateCycleSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, cycleSyncScope).Scan(&next); err != nil {
		return 0, fmt.Errorf("invcount: seq allocate: %w", err)
	}
	return next, nil
}
