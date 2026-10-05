// syncscope.go is the price_matrix sync adapter (HH-3.5 work item
// #168): the pricing module's implementation of the sync core's Store
// seam over pricing_list_prices, the retail anchor matrix this module
// owns. The pattern is the pick_tasks pilot's (tasks/syncscope.go) and
// the drive-run adapters' (dispatch/syncscope.go): the sync core owns
// the protocol and no table, this adapter meets it at the Store
// interface, and EVERY write, pushed or from the desk's batch apply,
// runs through the one list-price write path this module owns
// (applyMatrixPrice below, extracted from ApplyBatch so the batch and
// the sync door cannot drift), never around it.
//
// The scope's policy is server_wins (the confirmed table): a desk
// publish that lands while a device was offline is the outcome the
// device is told to show. The scope serves both caller classes (the
// catalog declares it on the counter and the contractor's procurement
// app): the anchor matrix is dealer-level data, so both classes read
// the same org rows, the org arriving from X-Org under the staff
// bearer and from the token's Team under the tenant bearer (G4), and
// only staff devices push (a scope's tenancy says who may call, the
// catalog's declarations say who writes).
//
// The row's identity is the product whose anchor it is (the table's
// primary key), so a pushed create NAMES its product: the server
// cannot invent an anchor for a product nobody named, and a product
// outside the caller's org answers not-found, never a foreign row.
//
// THE PRODUCTS READ, stated plainly: the module's own shipped apply
// statement already derives the anchor's unit from products
// (ApplyBatch's SELECT price_uom FROM products, the ported code), and
// this adapter reads products.org_id beside it for the org fence and
// stamps it on the row. A product-reader seam would be the right door
// if the module grows one; until then the fence follows the module's
// own established posture on the same table, fail closed on a NULL or
// foreign org.
//
// THE UNIT SET READ, added with the anchor's own unit (LD-3 part B) and
// disclosed the same way: when the caller names the anchor's unit, the
// write path checks it against product_uoms, the product's own unit
// set, in one EXISTS beside the statement's products reads, on the
// caller's transaction (row 786); a unit outside the set refuses
// before anything is drawn. The sync door names no unit, so its
// clients' payloads and the sync projection are unchanged; the check
// exists for the list price door the load cycle assembles, which sends
// a fixture price per the unit its fixture names.
package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// matrixSyncScope is the per-scope sequence counter (scope_sequences,
// the 0213 convention) every pricing_list_prices write draws from.
const matrixSyncScope = "price_matrix"

// priceSeqCTE is the data-modifying CTE the one list-price write path
// prepends: the seq is drawn atomically in the same statement, the
// same shape every module's single-statement writers use. Gaps from
// no-op updates are allowed by the 0213 convention.
const priceSeqCTE = `WITH pseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('price_matrix')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// matrixExec is the executor the one list-price write path runs on:
// the pool, a transaction, anything pgx-shaped that can Exec. The
// caller named unit's set check reads beside the write, so the
// executor reads rows too; row 786: every caller passes its own
// transaction and the check rides it, never a stray pool read.
type matrixExec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// matrixPriceWrite is the one shape every list-price write carries:
// the anchor's cents, the trail's legs, and (sync only) the client
// idempotency key. BatchID nil and Effective nil mark a sync write;
// Key empty marks the batch path. PriceUOM is the anchor's own unit
// from the caller (LD-3 part B): empty keeps the shipped derivation
// from products.price_uom, a named unit must sit in the product's own
// unit set and denominates the cents the caller sent.
type matrixPriceWrite struct {
	ProductID string
	Cents     int64
	PriceUOM  string
	Before    *int64
	BatchID   *string
	Effective *time.Time
	Actor     string
	Key       string
}

// errPriceUOMNotInSet is the typed refusal for a caller named unit the
// product's own unit set does not carry: the field is named, never the
// value, and a caller answers errors.Is against it.
var errPriceUOMNotInSet = errors.New("pricing: price_uom is not in the product's own unit set")

// applyMatrixPrice is THE list-price write path, the statement the
// batch apply ran before the sync door existed, extracted so every
// writer of pricing_list_prices runs one upsert with one
// price_change_log trail beside it. The sync legs (0213) stamp in the
// same statement: the org read off the product (the fence, the same
// read the statement already made for the anchor's unit) and the seq
// from the price_matrix scope's allocator, so a desk publish is
// visible to a device's pull the moment it lands. The D30 key column
// carries the latest live key applied (the drive-run design note).
//
// The anchor's own unit (LD-3 part B) comes from the caller: a named
// unit is checked against the product's own unit set FIRST (the read
// rides the caller's transaction, row 786), refuses as
// errPriceUOMNotInSet naming the field and never the value, and draws
// nothing (no seq, no row, no trail) behind the refusal; it
// denominates the cents the caller sent and lands on create and on
// conflict, never half moved. An absent unit keeps the shipped
// derivation from products.price_uom, so every existing caller and
// the existing pricing operations keep their shapes and results.
func applyMatrixPrice(ctx context.Context, q matrixExec, in matrixPriceWrite) error {
	if in.PriceUOM != "" {
		var inSet bool
		if err := q.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM product_uoms WHERE product_id = $1::uuid AND uom_code = $2)`,
			in.ProductID, in.PriceUOM).Scan(&inSet); err != nil {
			return fmt.Errorf("pricing: matrix unit set read: %w", err)
		}
		if !inSet {
			return fmt.Errorf("pricing: matrix write refused: %w", errPriceUOMNotInSet)
		}
	}
	if _, err := q.Exec(ctx, priceSeqCTE+`
		INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom, org_id, seq, idempotency_key)
		VALUES ($1::uuid, $2, COALESCE(NULLIF($4,'')::citext, (SELECT price_uom FROM products WHERE id = $1::uuid)),
		        (SELECT org_id FROM products WHERE id = $1::uuid),
		        (SELECT next_value FROM pseq), NULLIF($3,'')::uuid)
		ON CONFLICT (product_id) DO UPDATE SET
			list_price_cents = EXCLUDED.list_price_cents,
			price_uom        = COALESCE(NULLIF($4,'')::citext, pricing_list_prices.price_uom),
			updated_at       = now(),
			seq              = EXCLUDED.seq,
			idempotency_key  = EXCLUDED.idempotency_key`,
		in.ProductID, in.Cents, in.Key, in.PriceUOM); err != nil {
		return fmt.Errorf("pricing: matrix upsert: %w", err)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO price_change_log (product_id, batch_id, field, before_cents, after_cents, effective_date, changed_by)
		 VALUES ($1::uuid, $2::uuid, 'retail_cents', $3, $4, $5, NULLIF($6,'')::uuid)`,
		in.ProductID, in.BatchID, in.Before, in.Cents, derefTimePtr(in.Effective), in.Actor); err != nil {
		return fmt.Errorf("pricing: matrix trail: %w", err)
	}
	return nil
}

// looksLikeUUID reports whether s is a canonical 36-char uuid, the
// boundary guard every pushed id passes before it reaches a Postgres
// uuid column (the pilot's guard, restated local to this package).
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

// matrixRefused wraps one of the module's invariant refusals in the
// Store seam's classified marker.
func matrixRefused(err error) error {
	return fmt.Errorf("%w: %w", sync.ErrOpRefused, err)
}

// syncCents reads one money leg off a pushed payload: integer cents
// only, as a JSON number that is integral or a digit string. Money is
// never a float (AGENTS.md), so 12.5 refuses rather than rounding.
func syncCents(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int64(n)) {
			return 0, false
		}
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case string:
		c, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, false
		}
		return c, true
	}
	return 0, false
}

// matrixView is the pull projection: the anchor row the catalogue and
// the quote surfaces read, id being the product whose anchor it is.
type matrixView struct {
	ID             string    `json:"id"`
	ListPriceCents int64     `json:"list_price_cents"`
	PriceUOM       string    `json:"price_uom"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// syncColumns is the pull projection plus the sync legs.
const syncMatrixColumns = `product_id::text, list_price_cents, price_uom::text, updated_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// syncMatrixRow is one scanned row of the sync projection.
type syncMatrixRow struct {
	view    matrixView
	seq     int64
	deleted bool
}

// scanSyncMatrix reads the six targets.
func scanSyncMatrix(row pgx.Row) (syncMatrixRow, error) {
	var r syncMatrixRow
	if err := row.Scan(&r.view.ID, &r.view.ListPriceCents, &r.view.PriceUOM, &r.view.UpdatedAt,
		&r.seq, &r.deleted); err != nil {
		return syncMatrixRow{}, err
	}
	return r, nil
}

// matrixData projects one row through its json tags into the wire map
// the contract's SyncEntity.data carries.
func matrixData(v matrixView) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("pricing: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("pricing: sync projection decode: %w", err)
	}
	return data, nil
}

// matrixStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func matrixStateOf(r syncMatrixRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.view.ID, Seq: r.seq, Deleted: r.deleted}
	if !r.deleted {
		data, err := matrixData(r.view)
		if err != nil {
			return sync.RowState{}, err
		}
		rs.Data = data
	}
	return rs, nil
}

// matrixScopePredicate is the WHERE predicate every adapter query runs
// under. price_matrix serves both caller classes the same org rows
// (the org is the dealer's, resolved per G4), so the predicate is the
// org alone and never a tenant leg.
func matrixScopePredicate(f sync.ScopeFilter) (string, []any, bool) {
	if f.OrgID == "" {
		return "", nil, false
	}
	return "org_id = $1::uuid", []any{f.OrgID}, true
}

// SyncStore is the price_matrix Store. The staff identity linker
// resolves the audit actor; nil is legal only where writes will not
// run.
type SyncStore struct {
	pool *pgxpool.Pool
	link identity.UserIDLinker
}

// NewSyncStore wires the adapter.
func NewSyncStore(pool *pgxpool.Pool, link identity.UserIDLinker) *SyncStore {
	return &SyncStore{pool: pool, link: link}
}

// Pull returns the page of anchor changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the org
// predicate.
func (s *SyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := matrixScopePredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM pricing_list_prices
		WHERE %s AND seq IS NOT NULL AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, syncMatrixColumns, where)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("pricing: sync pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanSyncMatrix(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("pricing: sync pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.view.ID, Seq: r.seq})
			continue
		}
		data, err := matrixData(r.view)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.view.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("pricing: sync pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// D30 column's latest key, kept through a tombstone.
func (s *SyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := matrixScopePredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM pricing_list_prices
		WHERE %s AND idempotency_key = $2::uuid
		LIMIT 1`, syncMatrixColumns, where)
	args = append(args, key)
	r, err := scanSyncMatrix(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("pricing: sync find applied: %w", err)
	}
	rs, err := matrixStateOf(r)
	return rs, true, err
}

// Load reads one anchor by its product id under the org predicate; a
// product outside the caller's org is the same as an absent one.
func (s *SyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := matrixScopePredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM pricing_list_prices WHERE product_id = $2::uuid AND %s`,
		syncMatrixColumns, where)
	args = append(args, id)
	r, err := scanSyncMatrix(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("pricing: sync load: %w", err)
	}
	rs, err := matrixStateOf(r)
	return rs, true, err
}

// matrixCentsField is the one field a pushed write may declare: the
// anchor's cents. The unit is the product's own (the same derivation
// the batch apply runs), the org is the product's, the trail is the
// module's: nothing else in the row is a client fact.
func matrixCentsField(fields map[string]any) (int64, error) {
	if len(fields) == 0 {
		return 0, matrixRefused(errors.New("an update that moves nothing refuses"))
	}
	cents, has := int64(0), false
	for k, v := range fields {
		if k != "id" && k != "list_price_cents" {
			return 0, fmt.Errorf("pricing: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
		if k == "list_price_cents" {
			c, ok := syncCents(v)
			if !ok {
				return 0, fmt.Errorf("pricing: list_price_cents must be integer cents: %w", sync.ErrOpRefused)
			}
			cents, has = c, true
		}
	}
	if !has {
		return 0, matrixRefused(errors.New("an update that moves nothing refuses"))
	}
	if cents < 0 {
		return 0, fmt.Errorf("pricing: a list price cannot be negative: %w", sync.ErrOpRefused)
	}
	return cents, nil
}

// matrixActor resolves the ERP user a push attributes its write to.
func (s *SyncStore) matrixActor(ctx context.Context, f sync.ScopeFilter) string {
	if s.link == nil || f.Actor == "" {
		return ""
	}
	if uid, ok := s.link(ctx, f.Actor); ok {
		return uid
	}
	return ""
}

// matrixFence reads the product's org and anchor unit: the org fence
// (a foreign product answers not-found, existence never leaks) and the
// unit the one write path derives the anchor's price_uom from, refused
// up front when the product carries none (the column is NOT NULL, and
// a DB error there would fail the whole push for one bad row).
func (s *SyncStore) matrixFence(ctx context.Context, f sync.ScopeFilter, productID string) error {
	var org, uom *string
	if err := s.pool.QueryRow(ctx,
		`SELECT org_id::text, price_uom::text FROM products WHERE id = $1::uuid AND deleted_at IS NULL`,
		productID).Scan(&org, &uom); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sync.ErrRowNotFound
		}
		return fmt.Errorf("pricing: sync product read: %w", err)
	}
	if org == nil || *org != f.OrgID {
		return sync.ErrRowNotFound
	}
	if uom == nil || *uom == "" {
		return matrixRefused(fmt.Errorf("the product carries no unit to denominate an anchor in"))
	}
	return nil
}

// Create sets one product's anchor from a pushed create: the product
// must be named (the row's identity is the product), sit in the
// caller's org, and carry a unit; the write runs the module's one
// list-price path with its trail, stamping the caller's org, a fresh
// seq and the idempotency key.
func (s *SyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	return s.writeAnchor(ctx, f, key, fields, true)
}

// Write moves one anchor's cents through the same one path.
func (s *SyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := matrixScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	merged := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		merged[k] = v
	}
	merged["id"] = id
	return s.writeAnchor(ctx, f, key, merged, false)
}

// writeAnchor is the create and update arm both: one fence, one value
// read, one pass through the module's own write path, one read-back.
func (s *SyncStore) writeAnchor(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any, isCreate bool) (sync.RowState, error) {
	if _, _, ok := matrixScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	id, _ := fields["id"].(string)
	if !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("pricing: a matrix row is a product's anchor, name the product: %w", sync.ErrOpRefused)
	}
	cents, err := matrixCentsField(fields)
	if err != nil {
		return sync.RowState{}, err
	}
	if err := s.matrixFence(ctx, f, id); err != nil {
		return sync.RowState{}, err
	}
	actor := s.matrixActor(ctx, f)

	// The value-equal arm: the declared cents are already the anchor's
	// standing fact (a superseded replay of one constant price meets
	// it): answer applied with the row as it stands, stamping nothing,
	// so a replay cannot inflate the feed. A tombstoned anchor is not
	// writable (the tombstone stands until the desk re-opens the
	// product's anchor); it answers not-found, the boundary every
	// adapter gives a deleted row. A create naming an anchor that
	// already stands under another key (the dedup already ran) is the
	// collision the caller owns.
	row, ok, err := s.Load(ctx, f, id)
	if err != nil {
		return sync.RowState{}, err
	}
	if ok {
		if row.Deleted {
			return sync.RowState{}, sync.ErrRowNotFound
		}
		if isCreate {
			return sync.RowState{}, sync.ErrRowExists
		}
		if row.Data["list_price_cents"] == float64(cents) {
			return row, nil
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("pricing: begin sync write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var before *int64
	if err := tx.QueryRow(ctx,
		`SELECT list_price_cents FROM pricing_list_prices WHERE product_id = $1::uuid AND deleted_at IS NULL`,
		id).Scan(&before); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, fmt.Errorf("pricing: sync before read: %w", err)
	}
	if err := applyMatrixPrice(ctx, tx, matrixPriceWrite{
		ProductID: id, Cents: cents, Before: before, Actor: actor, Key: key,
	}); err != nil {
		return sync.RowState{}, err
	}
	r, err := scanSyncMatrix(tx.QueryRow(ctx,
		`SELECT `+syncMatrixColumns+` FROM pricing_list_prices WHERE product_id = $1::uuid`, id))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("pricing: sync write read back: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("pricing: commit sync write: %w", err)
	}
	return matrixStateOf(r)
}

// Delete tombstones one anchor: deleted_at set, the row retained, a
// fresh seq, the live row's idempotency key kept. The catalogue's
// honest degradation is the same one a missing anchor row gives:
// absence is unknown, never zero.
func (s *SyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	if _, _, ok := matrixScopePredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	_ = key
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("pricing: begin sync delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateMatrixSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	var deleted bool
	err = tx.QueryRow(ctx, `
		UPDATE pricing_list_prices SET
			deleted_at = now(),
			updated_at = now(),
			seq        = $3
		WHERE product_id = $1::uuid AND org_id = $2::uuid AND deleted_at IS NULL
		RETURNING (deleted_at IS NOT NULL)`, id, f.OrgID, seq).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("pricing: sync delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("pricing: commit sync delete: %w", err)
	}
	return sync.RowState{ID: id, Seq: seq, Deleted: true}, nil
}

// allocateMatrixSeqTx draws the next price_matrix seq inside the
// caller's transaction, the same upsert the CTE inlines.
func allocateMatrixSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, matrixSyncScope).Scan(&next); err != nil {
		return 0, fmt.Errorf("pricing: seq allocate: %w", err)
	}
	return next, nil
}
