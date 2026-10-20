// syncscope.go is the customer_quotes sync Store (HH-3.5 #169, manual
// policy): the contractor-to-homeowner document over this module's own
// customer_quote table. The pull projection is the module's own
// TenantQuote wire shape (totals included, the token never: the share
// token exists only as a hash on the row and the raw value leaves the
// server exactly once, at send, so no sync payload ever carries it).
//
// The manual policy composes with the module's own lifecycle rules: a
// STALE offline edit (the desk revised or sent the document while the
// contractor was offline) meets the engine's conflict arm, applies
// nothing, and returns the server row with the review marker; a FRESH
// write still passes the module's own guards (only a draft may edit,
// R5), so a sync write can never resurrect a dead token's document: the
// token legs are outside the writable sets entirely and every lifecycle
// write (send, revise, accept) draws its own seq on the row, which is
// exactly what turns a stale write into the conflict arm.
package customerquote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// quoteSyncColumns is the row read the adapter scans: the module's own
// quoteCols plus the sync legs (seq, tombstone flag).
const quoteSyncColumns = quoteCols + `, seq, (deleted_at IS NOT NULL) AS deleted`

// quoteSyncRow is one scanned row plus its sync legs.
type quoteSyncRow struct {
	row     QuoteRow
	seq     int64
	deleted bool
}

// scanQuoteSync reads the 22-target row over scanQuote's twenty.
func scanQuoteSync(row pgx.Row) (quoteSyncRow, error) {
	var r quoteSyncRow
	err := row.Scan(&r.row.ID, &r.row.OrgID, &r.row.TenantID, &r.row.Status, &r.row.MarkupBps,
		&r.row.LinesJSON, &r.row.LabourJSON, &r.row.OverheadJSON, &r.row.HideLinePrices, &r.row.CustomerContactJSON,
		&r.row.ShareTokenHash, &r.row.ShareExpiresAt, &r.row.ValidUntil, &r.row.SupplierHorizon,
		&r.row.ViewCount, &r.row.FirstViewedAt, &r.row.ChangeRequestsJSON, &r.row.CreatedAt, &r.row.UpdatedAt, &r.row.DeletedAt,
		&r.seq, &r.deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return quoteSyncRow{}, err
	}
	if err != nil {
		return quoteSyncRow{}, fmt.Errorf("customerquote: sync row read: %w", err)
	}
	return r, nil
}

// SyncStore is the customer_quotes Store.
type SyncStore struct {
	store *Store
	svc   *Service
}

// NewSyncStore wires the adapter over the module's own store and
// service (the projection reuses the service's tenantQuote, so the
// cached row and the tenant surface read one shape).
func NewSyncStore(store *Store, svc *Service) *SyncStore {
	return &SyncStore{store: store, svc: svc}
}

// tenantPredicate is the mandatory scoping predicate: org and tenant
// from the middleware-stamped filter (G4: the tenant never from X-Org).
func tenantPredicate(f sync.ScopeFilter) (string, bool) {
	if !f.Tenant || f.TenantID == "" || f.OrgID == "" {
		return "", false
	}
	return "tenant_id = $1::uuid AND org_id = $2::uuid", true
}

// quoteRowStateOf folds one scanned row into the engine's RowState
// through the module's own projection; a tombstone carries its seq and
// no projection.
func (s *SyncStore) quoteRowStateOf(r quoteSyncRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.row.ID, Seq: r.seq, Deleted: r.deleted}
	if r.deleted {
		return rs, nil
	}
	tq, err := s.svc.tenantQuote(r.row)
	if err != nil {
		return sync.RowState{}, err
	}
	raw, err := json.Marshal(tq)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: sync projection decode: %w", err)
	}
	rs.Data = data
	return rs, nil
}

// Pull returns the page of document changes past since, live rows and
// tombstones in seq order, under the tenant predicate.
func (s *SyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, ok := tenantPredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, err := s.store.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM customer_quote
		 WHERE %s AND seq IS NOT NULL AND seq > $3
		 ORDER BY seq ASC LIMIT $4`, quoteSyncColumns, where),
		f.TenantID, f.OrgID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("customerquote: sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanQuoteSync(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("customerquote: sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.row.ID, Seq: r.seq})
			continue
		}
		rs, err := s.quoteRowStateOf(r)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.row.ID, Seq: r.seq, Data: rs.Data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("customerquote: sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// row's single D30 column (the latest write or create key).
func (s *SyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, ok := tenantPredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	r, err := scanQuoteSync(s.store.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM customer_quote WHERE %s AND idempotency_key = $3::uuid LIMIT 1`,
		quoteSyncColumns, where), f.TenantID, f.OrgID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("customerquote: sync find applied: %w", err)
	}
	rs, err := s.quoteRowStateOf(r)
	return rs, true, err
}

// Load reads one document by id under the tenant predicate; a row
// outside the caller's tenant is the same as an absent one (existence
// never leaks).
func (s *SyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.store.pool, f, id)
}

// quoteQuerier is the read surface a pool and a transaction share, so
// one loader body serves the public Load (the pool) and the callers
// that already hold a transaction (their own connection).
type quoteQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *SyncStore) load(ctx context.Context, q quoteQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, ok := tenantPredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	r, err := scanQuoteSync(q.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM customer_quote WHERE id = $3::uuid AND %s`,
		quoteSyncColumns, where), f.TenantID, f.OrgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("customerquote: sync load: %w", err)
	}
	rs, err := s.quoteRowStateOf(r)
	return rs, true, err
}

// writable is the field set a pushed write may declare: the compose
// legs of the document. The lifecycle legs (status, the token pair,
// view counters, the acceptance's change_requests) are outside the set:
// they are the module's own, and a payload declaring them refuses
// whole, which is the never-resurrect rule as a boundary fact.
var syncWritable = map[string]bool{
	"id": true, "markup_bps": true, "hide_line_prices": true, "lines": true,
	"labour_lines": true, "overhead_lines": true, "customer_contact": true,
	"valid_until": true,
}

// checkWritable refuses a payload that declares anything outside the
// writable set.
func checkSyncWritable(fields map[string]any) error {
	for k := range fields {
		if !syncWritable[k] {
			return fmt.Errorf("customerquote: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	return nil
}

// intLegOf reads one payload leg as an integer; the engine hands the
// adapter the map the client built (a direct Go push carries int, a
// JSON-decoded push carries float64), so both spellings read.
func intLegOf(fields map[string]any, key string) (int64, bool, error) {
	switch raw := fields[key].(type) {
	case nil:
		return 0, false, nil
	case float64:
		if raw != float64(int64(raw)) {
			return 0, false, fmt.Errorf("customerquote: %s must be an integer: %%w", key)
		}
		return int64(raw), true, nil
	case int:
		return int64(raw), true, nil
	case int64:
		return raw, true, nil
	}
	return 0, false, fmt.Errorf("customerquote: %s must be an integer: %%w", key)
}

// decodeLeg decodes one JSON leg the payload declared into the typed
// shape the module's own validators take.
func decodeLeg[T any](fields map[string]any, key string, out *T) (bool, error) {
	raw, ok := fields[key]
	if !ok || raw == nil {
		return false, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return false, fmt.Errorf("customerquote: leg %q is not a json value: %w", key, sync.ErrOpRefused)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return false, fmt.Errorf("customerquote: leg %q does not carry the document shape: %w", key, sync.ErrOpRefused)
	}
	return true, nil
}

// Create builds one draft document from a pushed create, running the
// module's own validation (R2 no unpriced lines, R3 the validity cap)
// and stamping org, tenant, a fresh customer_quote seq and the
// idempotency key. The birth status is draft; the document's token is
// born only at send, never here.
func (s *SyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkSyncWritable(fields); err != nil {
		return sync.RowState{}, err
	}
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("customerquote: payload id must be a uuid: %w", sync.ErrOpRefused)
	}
	in := CreateInput{Customer: CustomerContact{}}
	if _, err := decodeLeg(fields, "customer_contact", &in.Customer); err != nil {
		return sync.RowState{}, err
	}
	if markup, ok, err := intLegOf(fields, "markup_bps"); err != nil {
		return sync.RowState{}, err
	} else if ok {
		in.MarkupBps = ptr(markup)
	}
	if raw, ok := fields["hide_line_prices"].(bool); ok {
		in.HideLinePrices = raw
	}
	if _, err := decodeLeg(fields, "lines", &in.Lines); err != nil {
		return sync.RowState{}, err
	}
	if _, err := decodeLeg(fields, "labour_lines", &in.LabourLines); err != nil {
		return sync.RowState{}, err
	}
	if _, err := decodeLeg(fields, "overhead_lines", &in.OverheadLines); err != nil {
		return sync.RowState{}, err
	}
	if v, ok := fields["valid_until"].(string); ok && v != "" {
		t, err := parseSyncTime(v)
		if err != nil {
			return sync.RowState{}, err
		}
		in.ValidUntil = &t
	}
	// The module's own validation, verbatim.
	now := s.svc.now()
	if _, err := ValidateLineSet(in.Lines, markupBpsOrCarried(in.MarkupBps), in.OverheadLines, now); err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: %v: %w", err, sync.ErrOpRefused)
	}
	if names := UnpricedLineNames(in.Lines, now); len(names) > 0 {
		return sync.RowState{}, fmt.Errorf("customerquote: %s: %w", RefuseUnpriced(names), sync.ErrOpRefused)
	}
	horizon := SupplierPriceHorizon(in.Lines)
	validUntil := CapValidUntil(s.svc.now().AddDate(0, 0, defaultValidDays), horizon)
	if in.ValidUntil != nil {
		validUntil = CapValidUntil(*in.ValidUntil, horizon)
	}

	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: begin sync create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := s.store.AllocateSeqTx(ctx, tx, ScopeCustomerQuote)
	if err != nil {
		return sync.RowState{}, err
	}
	row, err := scanQuoteSync(tx.QueryRow(ctx, `
		INSERT INTO customer_quote
		  (id, org_id, tenant_id, status, markup_bps, lines, labour_lines, overhead_lines,
		   hide_line_prices, customer_contact, valid_until, supplier_price_horizon, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2, $3, 'draft', $4, $5, $6, $7,
		        $8, $9, $10, $11, $12, $13::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+quoteSyncColumns,
		id, f.OrgID, f.TenantID, markupBpsOrCarried(in.MarkupBps), mustJSON(in.Lines), mustJSON(in.LabourLines),
		mustJSON(in.OverheadLines), in.HideLinePrices, mustJSON(in.Customer), validUntil, horizon, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, own, _ := s.load(ctx, tx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sync.RowState{}, sync.ErrRowExists
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: sync create insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: commit sync create: %w", err)
	}
	return s.quoteRowStateOf(row)
}

// Write applies the declared compose legs to one document through the
// module's own draft-state rule: the update lands only on a draft (R5),
// re-runs the module's validation on the merged document, and stamps a
// fresh seq and the idempotency key. A document the desk moved (sent,
// viewed, accepted) refuses with the module's own sentence, which is
// the composition of the manual policy with the lifecycle: the
// conflict arm carries the stale case, this guard carries the fresh
// one, and neither can touch the token legs.
func (s *SyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkSyncWritable(fields); err != nil {
		return sync.RowState{}, err
	}
	// The locked pre-image, under the row lock so the draft guard and
	// the write are one fact.
	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: begin sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	before, err := scanQuoteSync(tx.QueryRow(ctx, `
		SELECT `+quoteSyncColumns+` FROM customer_quote
		WHERE id = $1::uuid AND tenant_id = $2::uuid AND org_id = $3::uuid AND deleted_at IS NULL FOR UPDATE`,
		id, f.TenantID, f.OrgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: lock quote %q for sync write: %w", id, err)
	}
	doc, err := decodeDoc(before.row)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: document decode: %w", err)
	}
	// Merge exactly the service's own merge: a declared leg replaces,
	// an absent leg keeps the stored value.
	lines := doc.Lines
	var linesDeclared []Line
	if _, err := decodeLeg(fields, "lines", &linesDeclared); err != nil {
		return sync.RowState{}, err
	} else if fields["lines"] != nil {
		lines = linesDeclared
	}
	labour := doc.Labour
	var labourDeclared []LabourLine
	if _, err := decodeLeg(fields, "labour_lines", &labourDeclared); err != nil {
		return sync.RowState{}, err
	} else if fields["labour_lines"] != nil {
		labour = labourDeclared
	}
	overhead := doc.Overhead
	var overheadDeclared []OverheadLine
	if _, err := decodeLeg(fields, "overhead_lines", &overheadDeclared); err != nil {
		return sync.RowState{}, err
	} else if fields["overhead_lines"] != nil {
		overhead = overheadDeclared
	}
	contact := doc.Contact
	var contactDeclared CustomerContact
	if _, err := decodeLeg(fields, "customer_contact", &contactDeclared); err != nil {
		return sync.RowState{}, err
	} else if fields["customer_contact"] != nil {
		contact = contactDeclared
	}
	markup := before.row.MarkupBps
	if m, ok, err := intLegOf(fields, "markup_bps"); err != nil {
		return sync.RowState{}, err
	} else if ok {
		markup = m
	}
	hide := before.row.HideLinePrices
	if raw, ok := fields["hide_line_prices"].(bool); ok {
		hide = raw
	}
	validUntil := before.row.ValidUntil
	if v, ok := fields["valid_until"].(string); ok && v != "" {
		t, err := parseSyncTime(v)
		if err != nil {
			return sync.RowState{}, err
		}
		validUntil = &t
	}
	// The module's own validators, verbatim, on the merged document.
	if _, err := ValidateLineSet(lines, markup, overhead, s.svc.now()); err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: %v: %w", err, sync.ErrOpRefused)
	}
	if names := UnpricedLineNames(lines, s.svc.now()); len(names) > 0 {
		return sync.RowState{}, fmt.Errorf("customerquote: %s: %w", RefuseUnpriced(names), sync.ErrOpRefused)
	}
	horizon := SupplierPriceHorizon(lines)
	if validUntil != nil {
		capped := CapValidUntil(*validUntil, horizon)
		validUntil = &capped
	}
	seq, err := s.store.AllocateSeqTx(ctx, tx, ScopeCustomerQuote)
	if err != nil {
		return sync.RowState{}, err
	}
	updated, err := scanQuoteSync(tx.QueryRow(ctx, `
		UPDATE customer_quote SET
		  markup_bps = $4, lines = $5, labour_lines = $6, overhead_lines = $7,
		  hide_line_prices = $8, customer_contact = $9, valid_until = $10,
		  supplier_price_horizon = $11, updated_at = now(), seq = $12, idempotency_key = $13::uuid
		WHERE id = $1 AND tenant_id = $2 AND org_id = $3 AND status = 'draft' AND deleted_at IS NULL
		RETURNING `+quoteSyncColumns,
		id, f.TenantID, f.OrgID, markup, mustJSON(lines), mustJSON(labour), mustJSON(overhead),
		hide, mustJSON(contact), validUntil, horizon, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		// The draft guard refused under the lock: the document moved
		// past draft between the load and the write; the module's own
		// sentence travels with the rejection.
		return sync.RowState{}, fmt.Errorf("customerquote: the quote is no longer a draft, revise it first: %w", sync.ErrOpRefused)
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: sync write %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: commit sync write: %w", err)
	}
	return s.quoteRowStateOf(updated)
}

// Delete tombstones one document: deleted_at set, the row retained, a
// fresh seq, the idempotency key not restamped. A tombstoned quote
// answers 410 on the public leg, so the tombstone is the honest delete.
func (s *SyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	_ = key
	if _, ok := tenantPredicate(f); !ok || !looksLikeUUID(id) {
		return sync.RowState{}, sync.ErrOpRefused
	}
	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: begin sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The tombstone relies on the sync seq trigger (0422): the row lock first, then the counter. An explicit
	// draw ahead of the UPDATE would take the counter before the row and cross a writer that relies on the
	// trigger (PR 556 round 3c); RETURNING answers the seq the trigger drew.
	r, err := scanQuoteSync(tx.QueryRow(ctx, `
		UPDATE customer_quote SET deleted_at = now(), updated_at = now()
		WHERE id = $1::uuid AND tenant_id = $2::uuid AND org_id = $3::uuid AND deleted_at IS NULL
		RETURNING `+quoteSyncColumns, id, f.TenantID, f.OrgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("customerquote: commit sync delete: %w", err)
	}
	rs, err := s.quoteRowStateOf(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}

// looksLikeUUID reports whether s is a canonical 36-char uuid, the same
// boundary guard the other sync adapters use.
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

// ptr is the one local pointer helper.
func ptr[T any](v T) *T { return &v }

// parseSyncTime reads one RFC3339 timestamp leg; anything else is a
// client defect that refuses the operation whole.
func parseSyncTime(v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("customerquote: valid_until must be an RFC3339 timestamp: %w", sync.ErrOpRefused)
	}
	return t, nil
}
