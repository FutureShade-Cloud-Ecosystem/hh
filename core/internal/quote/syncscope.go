// syncscope.go is the quotes sync adapter (HH-3.5 work item #168): the
// quote module's implementation of the sync core's Store seam over the
// quotes table this module owns. The pattern is the pick_tasks pilot's
// (tasks/syncscope.go, work item #165) and the drive-run adapters'
// (dispatch/syncscope.go, work item #167): the sync core owns the
// protocol and no table, this adapter touches only tables this module
// owns, and the two meet at the Store interface.
//
// The scope's policy is last_write_wins_by_field (the confirmed table),
// and the merge respects the document semantics this module owns:
// D14 keeps validity document-level (quotes.valid_until, expiry
// re-blocks the plan at document level) and D15 keeps the frozen line
// versus live reference split, so the merge writes FIELDS on the
// document and never touches a line's priced snapshot. The one field a
// pushed update may declare is valid_until, the module's own header
// PATCH, guarded by the same ratified edit matrix (editable in DRAFT
// and SENT, frozen everywhere else) and the same expiry guard (an
// expired document refuses every edit, including a validity extension:
// the merge never resurrects an expired document's price). Money legs
// (subtotal, tax, total) are server-computed and never writable
// through sync, exactly as they are never writable through the REST
// surface.
package quote

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

// quotesSyncScope is the per-scope sequence counter (scope_sequences,
// the 0213 convention) every quotes-row write this module makes draws
// from, beside the row, inside the writing transaction.
const quotesSyncScope = "quotes"

// quoteSeqCTE is the data-modifying CTE the module's single-statement
// write paths prepend to draw a fresh quotes seq atomically in the same
// statement (the drive-run adapters' syncSeqCTE pattern): no wrapping
// transaction exists on several of those paths, and a separate
// allocate-then-write round trip could interleave two writers. Gaps
// from no-op updates are allowed by the 0213 convention.
const quoteSeqCTE = `WITH qseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('quotes')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// quoteRefused wraps one of the module's invariant refusals in the
// Store seam's classified marker, so the engine reports it as that
// operation's rejected result with the module's own words.
func quoteRefused(err error) error {
	return fmt.Errorf("%w: %w", sync.ErrOpRefused, err)
}

// allocateQuoteSeqTx draws the next quotes seq inside the caller's
// transaction, the same upsert every module write path runs.
func allocateQuoteSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, quotesSyncScope).Scan(&next); err != nil {
		return 0, fmt.Errorf("quote: seq allocate: %w", err)
	}
	return next, nil
}

// syncQuoteView is the pull projection: the module's Quote wire shape
// with one honest difference, valid_until as a nullable leg. The struct
// above scans NULL into a zero time and would marshal the never-expires
// document as a wrong ancient date; the sync feed carries null, the
// fact the column holds (D2a: NULL means never expires).
type syncQuoteView struct {
	ID            string     `json:"id"`
	QuoteNo       string     `json:"quote_no"`
	CustomerID    string     `json:"customer_id"`
	Status        string     `json:"status"`
	ValidUntil    *time.Time `json:"valid_until"`
	SubtotalCents int64      `json:"subtotal_cents"`
	TaxCents      int64      `json:"tax_cents"`
	TotalCents    int64      `json:"total_cents"`
	CreatedAt     time.Time  `json:"created_at"`
}

// syncColumns is the pull projection: the module's quoteColumns plus
// the sync legs (seq, tombstone flag).
// The branch and the owner (the salesperson, else the creator) ride along
// so the door can decide, per row, what the caller may read.
const syncColumns = quoteColumns + `, seq, (deleted_at IS NOT NULL) AS deleted,
	branch_id::text, COALESCE(salesperson_id::text, COALESCE(created_by::text, ''))`

// syncRow is one scanned row of the sync projection.
type syncRow struct {
	quote    syncQuoteView
	seq      int64
	deleted  bool
	branchID string
	ownerID  string
}

// scanSyncRow reads the row: the nine Quote targets (plus the sync legs) re-declared beside
// scanQuote's (pgx cannot split one row across scanners; the shared
// quoteColumns constant is the guard against drift).
func scanSyncRow(row pgx.Row) (syncRow, error) {
	var q syncQuoteView
	var seq int64
	var deleted bool
	var branchID, ownerID string
	if err := row.Scan(
		&q.ID, &q.QuoteNo, &q.CustomerID, &q.Status, &q.ValidUntil,
		&q.SubtotalCents, &q.TaxCents, &q.TotalCents, &q.CreatedAt,
		&seq, &deleted, &branchID, &ownerID,
	); err != nil {
		return syncRow{}, err
	}
	return syncRow{quote: q, seq: seq, deleted: deleted, branchID: branchID, ownerID: ownerID}, nil
}

// quoteData projects one row through its json tags into the wire map
// the contract's SyncEntity.data carries.
func quoteData(q syncQuoteView) (map[string]any, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("quote: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("quote: sync projection decode: %w", err)
	}
	return data, nil
}

// rowStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func rowStateOf(r syncRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.quote.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := quoteData(r.quote)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// quoteScopePredicate is the WHERE predicate every adapter query runs
// under; quotes is an org scope, so a tenant filter fails closed and an
// absent org answers nothing.
func quoteScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.Tenant || f.OrgID == "" {
		return "", nil, false
	}
	return "org_id = $1::uuid", []any{f.OrgID}, true
}

// SyncStore is the quotes Store. The staff identity linker resolves
// the audit actor (the linked ERP user), the same seam the module's
// REST attribution reads; nil is legal only where creates will not run.
type SyncStore struct {
	pool *pgxpool.Pool
	link identity.UserIDLinker
	now  func() time.Time
}

// NewSyncStore wires the adapter.
func NewSyncStore(pool *pgxpool.Pool, link identity.UserIDLinker) *SyncStore {
	return &SyncStore{pool: pool, link: link, now: time.Now}
}

// WithNow pins the adapter's clock (the expiry guard reads today; a
// date-boundary test pins it rather than depending on the wall clock,
// the same injectability the repository's own expiry guard carries).
func (s *SyncStore) WithNow(f func() time.Time) *SyncStore { s.now = f; return s }

// Pull returns the page of quote changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// predicate and the caller's read rung (syncReadScope).
func (s *SyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := quoteScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	// Only the quotes the caller could list through REST (P2-C): the read
	// rung is part of the query, so a hidden row never shortens a page.
	read := s.syncReadScope(ctx, f)
	if !read.ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	readSQL, readArgs := read.predicate(4)
	q := fmt.Sprintf(`SELECT %s FROM quotes
		WHERE %s AND seq IS NOT NULL AND seq > $2%s
		ORDER BY seq ASC
		LIMIT $3`, syncColumns, where, readSQL)
	args = append(append(args, since, limit), readArgs...)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("quote: sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncRow(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("quote: sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.quote.ID, Seq: r.seq})
			continue
		}
		data, err := quoteData(r.quote)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.quote.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("quote: sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// row's D30 idempotency_key column (the latest key), kept through a
// tombstone (the drive-run design note: the delete does not stamp its
// own key, so the last live operation's key still resolves after a
// later delete, and the delete itself is idempotent through the
// engine's already-tombstoned arm).
func (s *SyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := quoteScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM quotes
		WHERE %s AND idempotency_key = $2::uuid
		LIMIT 1`, syncColumns, where)
	args = append(args, key)
	r, err := scanSyncRow(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("quote: sync find applied: %w", err)
	}
	rs, err := rowStateOf(r)
	if err == nil {
		s.maskUnreadable(ctx, f, r, &rs)
	}
	return rs, true, err
}

// maskUnreadable drops the projection of a quote the caller may not read
// (P2-C): the engine still needs the row's id and seq (the stale check, the
// idempotent replay), but the projection travels in a conflict envelope or
// a replayed answer, and a quote the caller cannot list through REST carries
// no data out of this door.
func (s *SyncStore) maskUnreadable(ctx context.Context, f sync.ScopeFilter, r syncRow, rs *sync.RowState) {
	if r.deleted {
		return
	}
	read := s.syncReadScope(ctx, f)
	if !read.ok || !read.reaches(r.branchID, r.ownerID) {
		rs.Data = nil
	}
}

// Load reads one quote by id under the org predicate; a row outside
// the caller's org is the same as an absent one (existence never
// leaks).
func (s *SyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := quoteScopePredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM quotes WHERE id = $2::uuid AND %s`, syncColumns, where)
	args = append(args, id)
	r, err := scanSyncRow(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("quote: sync load: %w", err)
	}
	rs, err := rowStateOf(r)
	if err == nil {
		s.maskUnreadable(ctx, f, r, &rs)
	}
	return rs, true, err
}

// parseSyncValidity reads one pushed validity leg and returns its
// YYYY-MM-DD form: a device edits the field it pulled, so the payload
// may carry the projection's own serialized form (RFC3339, the json
// round trip of the date) beside the plain date, and both normalize to
// the one canonical form the module stores.
func parseSyncValidity(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) == 10 {
		if _, err := time.Parse(dateLayout, s); err != nil {
			return "", fmt.Errorf("quote: valid_until must be YYYY-MM-DD: %w", sync.ErrOpRefused)
		}
		return s, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(dateLayout), nil
	}
	return "", fmt.Errorf("quote: valid_until must be YYYY-MM-DD: %w", sync.ErrOpRefused)
}

// quoteCreateFields is the field set a pushed create may declare: the
// customer the quote is opened for, the branch it is opened in, the
// optional validity, and the id. Everything else (status, the money
// legs, the number) is the server's to set.
func quoteCreateFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "customer_id", "branch_id", "valid_until":
		default:
			return fmt.Errorf("quote: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// quoteWriteFields is the field set a pushed update may declare: the
// one header field the module's own REST surface PATCHes. Declaring
// anything else refuses the operation whole.
func quoteWriteFields(fields map[string]any) error {
	for k := range fields {
		switch k {
		case "id", "valid_until":
		default:
			return fmt.Errorf("quote: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// quoteActor resolves the ERP user a push attributes its write to.
func (s *SyncStore) quoteActor(ctx context.Context, f sync.ScopeFilter) string {
	if s.link == nil || f.Actor == "" {
		return ""
	}
	if uid, ok := s.link(ctx, f.Actor); ok {
		return uid
	}
	return ""
}

// Create inserts one DRAFT quote from a pushed create: the caller needs
// Work on the Quote Desk and the branch in reach (the REST create's rule),
// and becomes the quote's salesperson as it does there; the branch must
// sit in the caller's org (the fence every module write honours, read
// off locations the same way the drive-run adapters do), the customer
// validates through the table's own FK exactly as the module's REST
// create does, the number mints from the module's own quote_no_seq,
// and the row stamps the caller's org, a freshly allocated seq and the
// idempotency key.
func (s *SyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := quoteScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := quoteCreateFields(fields); err != nil {
		return sync.RowState{}, err
	}
	customerID, _ := fields["customer_id"].(string)
	if !looksLikeUUID(customerID) {
		return sync.RowState{}, fmt.Errorf("quote: customer_id must be a uuid: %w", sync.ErrOpRefused)
	}
	branchID, _ := fields["branch_id"].(string)
	if !looksLikeUUID(branchID) {
		return sync.RowState{}, fmt.Errorf("quote: branch_id must be a uuid: %w", sync.ErrOpRefused)
	}
	// The REST create route's rule (P2-C): Work on the Quote Desk and the
	// branch in the caller's reach, before anything is read or written.
	if err := s.authorizeSyncCreate(ctx, f, branchID); err != nil {
		return sync.RowState{}, err
	}
	var inOrg bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1::uuid AND org_id = $2::uuid)`,
		branchID, f.OrgID).Scan(&inOrg); err != nil {
		return sync.RowState{}, fmt.Errorf("quote: sync create branch read: %w", err)
	}
	if !inOrg {
		// A foreign branch is not the caller's fact to learn apart
		// from a missing one: both answer not-found.
		return sync.RowState{}, sync.ErrRowNotFound
	}
	// An absent validity leg means never-expires (the module's own ""
	// semantics, D2a); a declared one may carry the plain date or the
	// projection's own serialized form.
	validUntil := ""
	if v, has := fields["valid_until"]; has && v != nil {
		parsed, perr := parseSyncValidity(fmt.Sprint(v))
		if perr != nil {
			return sync.RowState{}, perr
		}
		validUntil = parsed
	}
	id, hasID := fields["id"].(string)
	if hasID && id != "" && !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("quote: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	actor := s.quoteActor(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("quote: begin sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateQuoteSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncRow(tx.QueryRow(ctx, `
		INSERT INTO quotes (id, quote_no, customer_id, branch_id, status, valid_until, created_by, salesperson_id, org_id, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), '#Q-' || nextval('quote_no_seq'),
		        $2::uuid, $3::uuid, 'DRAFT', NULLIF($4,'')::date, NULLIF($5,'')::uuid, NULLIF($5,'')::uuid, $6::uuid, $7, $8::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+syncColumns,
		id, customerID, branchID, validUntil, actor, f.OrgID, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		// The ON CONFLICT arm answered no row: the client-chosen id is
		// taken, answered as the caller's own fact inside the org and
		// not-found outside it (existence never leaks). Nothing was
		// written, so the transaction ends first: the read-back goes
		// through the pool, and asking it for a second connection while
		// this one is held starves the pool once pool-size creates collide.
		_ = tx.Rollback(ctx)
		if _, own, _ := s.Load(ctx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("quote: sync create insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("quote: commit sync create: %w", err)
	}
	return rowStateOf(r)
}

// Write applies exactly the declared fields to one quote under the
// module's own rules, on a FOR UPDATE-locked pre-image: the ratified
// edit matrix (valid_until editable in DRAFT and SENT, frozen
// everywhere else) and the expiry guard (an expired document refuses
// every edit, including the extension that would resurrect its prices,
// D14). The row draws a fresh seq beside the write, so a desk edit is
// visible to a device's pull the moment it lands.
func (s *SyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := quoteScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := quoteWriteFields(fields); err != nil {
		return sync.RowState{}, err
	}
	raw, hasValidity := fields["valid_until"]
	if !hasValidity {
		// Declaring only the id is an update that moves nothing.
		return sync.RowState{}, quoteRefused(errors.New("an update that moves nothing refuses"))
	}
	validUntil, err := parseSyncValidity(fmt.Sprint(raw))
	if err != nil {
		return sync.RowState{}, err
	}
	actor := s.quoteActor(ctx, f)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("quote: begin sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status, branchID, ownerID string
	var current *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT status, valid_until, branch_id::text,
		        COALESCE(salesperson_id::text, COALESCE(created_by::text, ''))
		 FROM quotes
		 WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&status, &current, &branchID, &ownerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("quote: lock %q for sync write: %w", id, err)
	}
	// The ownership ladder first, as the REST edit path runs it (P2-4).
	if err := s.authorizeSyncWrite(ctx, f, branchID, ownerID); err != nil {
		return sync.RowState{}, err
	}
	if rule, ok := quoteEditRules[FieldQuoteValidUntil]; ok && !rule.from[status] {
		return sync.RowState{}, quoteRefused(fmt.Errorf("the quote is %s and its expiry date is frozen", status))
	}
	// The expiry guard is the module's own, verbatim (assertNotExpired,
	// D2/D14): the document's validity is the server's, and the merge
	// never resurrects an expired document's price by extending or
	// clearing the date the document lapsed on.
	if err := assertNotExpired(current, s.now()); err != nil {
		return sync.RowState{}, quoteRefused(err)
	}
	// The value-equal arm: the declared write is already the row's
	// standing fact (a superseded replay of one constant date meets
	// it, and so does clearing a validity the document never carried):
	// answer applied with the row as it stands, stamping nothing.
	if (current == nil && validUntil == "") || (current != nil && current.Format(dateLayout) == validUntil) {
		r, err := scanSyncRow(tx.QueryRow(ctx,
			`SELECT `+syncColumns+` FROM quotes WHERE id = $1::uuid`, id))
		if err != nil {
			return sync.RowState{}, fmt.Errorf("quote: sync write read back: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("quote: commit sync write: %w", err)
		}
		return rowStateOf(r)
	}
	seq, err := allocateQuoteSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncRow(tx.QueryRow(ctx, `
		UPDATE quotes SET
			valid_until     = NULLIF($3,'')::date,
			updated_by      = NULLIF($6,'')::uuid,
			updated_at      = now(),
			seq             = $4,
			idempotency_key = $5::uuid
		WHERE id = $1::uuid AND org_id = $2::uuid AND status = $7
		RETURNING `+syncColumns,
		id, f.OrgID, validUntil, seq, key, actor, status))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("quote: sync write %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("quote: commit sync write: %w", err)
	}
	return rowStateOf(r)
}

// Delete tombstones one quote: deleted_at set, the row retained, a
// fresh seq, the live row's idempotency key kept. Only a DRAFT may be
// withdrawn through sync: a quote somebody has seen (SENT) or agreed
// to (ACCEPTED, CONVERTED) is a record, and the tombstone is a sync
// fact beside it, not an erasure of it.
func (s *SyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := quoteScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("quote: begin sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, branchID, ownerID string
	if err := tx.QueryRow(ctx,
		`SELECT status, branch_id::text,
		        COALESCE(salesperson_id::text, COALESCE(created_by::text, ''))
		 FROM quotes WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL FOR UPDATE`,
		id, f.OrgID).Scan(&status, &branchID, &ownerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		return sync.RowState{}, fmt.Errorf("quote: lock %q for sync delete: %w", id, err)
	}
	// The same ladder as the write (P2-4): withdrawing is an edit.
	if err := s.authorizeSyncWrite(ctx, f, branchID, ownerID); err != nil {
		return sync.RowState{}, err
	}
	if status != "DRAFT" {
		return sync.RowState{}, quoteRefused(fmt.Errorf("a %s quote is a record, withdrawal is the desk's own path", status))
	}
	seq, err := allocateQuoteSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE quotes SET
			deleted_at = now(),
			updated_at = now(),
			seq        = $3
		WHERE id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL
		RETURNING (deleted_at IS NOT NULL)`, id, f.OrgID, seq).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("quote: sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("quote: commit sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}
