// items.go is the scope_items Store: the board card's lines over the
// plan_line table, last_write_wins_by_field. The contractor builds the
// scope of a card offline (lines from the catalogue, quantities in the
// entered unit, notes); the price legs are the pricing flow's and are
// outside every writable set, so no client merge can write, resurrect
// or counterfeit a price (the #168 quotes hazard, refused by
// construction here).
package planflow

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// itemsColumns is the pull projection: the line's legs plus the sync
// legs, unqualified so the same constant serves SELECTs and RETURNING
// alike. The nullable cents and decimal legs cast to text so the scan
// reads NULL as an explicit unknown, never a zero.
const itemsColumns = `id::text, plan_id::text, COALESCE(product_id::text,''), description,
	COALESCE(qty_entered::text,''), COALESCE(uom_entered::text,''), qty_base::text,
	unit_price_cents::text, COALESCE(price_uom::text,''), line_total_cents::text,
	price_source, price_valid_until, snapshot, is_special_order,
	COALESCE(special_order_description,''), COALESCE(supplier_ref,''), notes,
	COALESCE(added_by,''), created_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// itemRow is one scanned row: the line's legs plus its sync legs.
type itemRow struct {
	Line            Line
	Seq             int64
	Deleted         bool
	snapshot        []byte
	priceValidUntil *time.Time
}

// Line is the wire shape of one scope line, the projection the sync
// feed carries. Quantities are DECIMAL serialized as strings (the
// boundary rule) and the price legs are read-only through sync: they
// carry what the pricing flow resolved, never what a client declared.
type Line struct {
	ID                      string    `json:"id"`
	PlanID                  string    `json:"plan_id"`
	ProductID               string    `json:"product_id"`
	Description             string    `json:"description"`
	QtyEntered              string    `json:"qty_entered"`
	UOMEntered              string    `json:"uom_entered"`
	QtyBase                 *string   `json:"qty_base"`
	UnitPriceCents          *int64    `json:"unit_price_cents"`
	PriceUOM                string    `json:"price_uom"`
	LineTotalCents          *int64    `json:"line_total_cents"`
	PriceSource             string    `json:"price_source"`
	PriceValidUntil         *string   `json:"price_valid_until"`
	IsSpecialOrder          bool      `json:"is_special_order"`
	SpecialOrderDescription string    `json:"special_order_description"`
	SupplierRef             string    `json:"supplier_ref"`
	Notes                   string    `json:"notes"`
	AddedBy                 string    `json:"added_by"`
	CreatedAt               time.Time `json:"created_at"`
	// UpdatedAt is deliberately absent from the projection: the lww
	// merge leg compares every undeclared field, and a timestamp that
	// moves on every write is not a field the client's merge owns (the
	// attachments module set the same precedent).
}

// scanItem reads the 23-target row.
func scanItem(row rowScanner) (itemRow, error) {
	var r itemRow
	var qtyBase, unitPrice, lineTotal *string
	if err := row.Scan(
		&r.Line.ID, &r.Line.PlanID, &r.Line.ProductID, &r.Line.Description,
		&r.Line.QtyEntered, &r.Line.UOMEntered, &qtyBase,
		&unitPrice, &r.Line.PriceUOM, &lineTotal,
		&r.Line.PriceSource, &r.priceValidUntil, &r.snapshot, &r.Line.IsSpecialOrder,
		&r.Line.SpecialOrderDescription, &r.Line.SupplierRef, &r.Line.Notes,
		&r.Line.AddedBy, &r.Line.CreatedAt,
		&r.Seq, &r.Deleted,
	); err != nil {
		return itemRow{}, err
	}
	r.Line.QtyBase = qtyBase
	r.Line.UnitPriceCents = centsOf(unitPrice)
	r.Line.LineTotalCents = centsOf(lineTotal)
	if r.priceValidUntil != nil {
		s := r.priceValidUntil.UTC().Format(time.RFC3339)
		r.Line.PriceValidUntil = &s
	}
	return r, nil
}

// centsOf parses one DECIMAL-as-string cents leg the scan read into an
// int64; NULL stays nil (unpriced renders as the explicit unknown, per
// the never-render-a-confident-wrong-number rule).
func centsOf(v *string) *int64 {
	if v == nil {
		return nil
	}
	n, ok := new(big.Rat).SetString(*v)
	if !ok {
		return nil
	}
	c := n.Num().Int64()
	return &c
}

// itemRowStateOf folds one scanned row into the engine's RowState.
func itemRowStateOf(r itemRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.Line.ID, Seq: r.Seq, Deleted: r.Deleted}
	if r.Deleted {
		return rs, nil
	}
	data, err := projection(r.Line)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Data = data
	return rs, nil
}

// ItemsSyncStore is the scope_items Store over the plan_line table.
type ItemsSyncStore struct {
	pool Pool
}

// NewItemsSyncStore wires the adapter.
func NewItemsSyncStore(pool Pool) *ItemsSyncStore {
	return &ItemsSyncStore{pool: pool}
}

// Pull returns the page of line changes past since under the tenant
// predicate.
func (s *ItemsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := tenantPredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM plan_line
		WHERE %s AND seq IS NOT NULL AND seq > $3
		ORDER BY seq ASC
		LIMIT $4`, itemsColumns, where)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("planflow: items pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanItem(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("planflow: items pull scan: %w", err)
		}
		if r.Deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.Line.ID, Seq: r.Seq})
			continue
		}
		data, err := projection(r.Line)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.Line.ID, Seq: r.Seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("planflow: items pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied: the
// row's single D30 column (the latest write or create key).
func (s *ItemsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := tenantPredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM plan_line WHERE %s AND idempotency_key = $3::uuid LIMIT 1`,
		itemsColumns, where)
	args = append(args, key)
	r, err := scanItem(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("planflow: items find applied: %w", err)
	}
	rs, err := itemRowStateOf(r)
	return rs, true, err
}

// Load reads one line by id under the tenant predicate; a row outside
// the caller's tenant is the same as an absent one.
func (s *ItemsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.pool, f, id)
}

// itemsQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type itemsQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *ItemsSyncStore) load(ctx context.Context, db itemsQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := tenantPredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM plan_line WHERE id = $3::uuid AND %s`,
		itemsColumns, where)
	args = append(args, id)
	r, err := scanItem(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("planflow: items load: %w", err)
	}
	rs, err := itemRowStateOf(r)
	return rs, true, err
}

// itemsWritableCreate is the field set a pushed create may declare: the
// compose legs. The price legs and qty_base are absent (the pricing
// flow's), and the birth price_source is unpriced, exactly C47's
// starting state for a line the quote desk has not seen.
var itemsWritableCreate = map[string]bool{
	"id": true, "plan_id": true, "product_id": true, "description": true,
	"qty_entered": true, "uom_entered": true, "is_special_order": true,
	"special_order_description": true, "notes": true, "added_by": true,
}

// itemsWritable is the field set a pushed update may declare: the
// compose legs the contractor edits. Price legs, price_source,
// price_valid_until, snapshot and qty_base refuse whole.
var itemsWritable = map[string]bool{
	"id": true, "product_id": true, "description": true,
	"qty_entered": true, "uom_entered": true, "is_special_order": true,
	"special_order_description": true, "notes": true,
}

// parseQty reads the qty_entered leg as a fixed-point decimal string
// (the boundary rule: quantities cross as strings, and the value must
// parse as a decimal, never a float round trip).
func parseQty(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if _, ok := new(big.Rat).SetString(v); !ok {
		return "", errRefused("qty_entered must be a decimal number")
	}
	return v, nil
}

// Create inserts one line from a pushed create: the parent card must be
// the caller's own (the fence within the module's own tables), the row
// stamps org and tenant from the filter, a fresh scope_items seq and
// the idempotency key, and the birth state is unpriced.
func (s *ItemsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkWritable(itemsWritableCreate, fields); err != nil {
		return sync.RowState{}, err
	}
	planID := asString(fields, "plan_id")
	if !looksLikeUUID(planID) {
		return sync.RowState{}, errRefused("plan_id must be a uuid")
	}
	productID := asString(fields, "product_id")
	if productID != "" && !looksLikeUUID(productID) {
		return sync.RowState{}, errRefused("product_id must be a uuid")
	}
	if productID == "" && asString(fields, "description") == "" {
		return sync.RowState{}, errRefused("a line carries a product or a description")
	}
	qty, err := parseQty(asString(fields, "qty_entered"))
	if err != nil {
		return sync.RowState{}, err
	}
	uom := asString(fields, "uom_entered")
	notes := asString(fields, "notes")
	if len(notes) > maxNotesLen {
		return sync.RowState{}, errRefused("notes is too long (max %d)", maxNotesLen)
	}
	desc := asString(fields, "description")
	if len(desc) > maxNameLen*10 {
		return sync.RowState{}, errRefused("description is too long")
	}
	specialDesc := asString(fields, "special_order_description")
	addedBy := asString(fields, "added_by")
	if len(addedBy) > maxNameLen {
		return sync.RowState{}, errRefused("added_by is too long (max %d)", maxNameLen)
	}
	isSpecial, err := boolLeg(fields, "is_special_order")
	if err != nil {
		return sync.RowState{}, err
	}
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return sync.RowState{}, errRefused("payload id must be a uuid")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: begin items create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The parent card must exist and be the caller's own, locked so the
	// line cannot land on a card being tombstoned.
	var cardSeq int64
	err = tx.QueryRow(ctx,
		`SELECT seq FROM plan WHERE id = $1::uuid AND org_id = $2::uuid AND tenant_id = $3::uuid AND deleted_at IS NULL FOR UPDATE`,
		planID, f.OrgID, f.TenantID).Scan(&cardSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: lock parent card for item create: %w", err)
	}
	seq, err := allocateSeqTx(ctx, tx, scopeItemsSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanItem(tx.QueryRow(ctx, `
		INSERT INTO plan_line (id, plan_id, org_id, tenant_id, product_id, description,
		                       qty_entered, uom_entered, is_special_order,
		                       special_order_description, notes, added_by, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid, $4::uuid,
		        NULLIF($5,'')::uuid, $6, NULLIF($7,'')::decimal, NULLIF($8,''), COALESCE($9, FALSE),
		        NULLIF($10,''), $11, NULLIF($12,''), $13, $14::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+itemsColumns,
		id, planID, f.OrgID, f.TenantID, productID, desc, qty, uom, isSpecial,
		specialDesc, notes, addedBy, seq, key))
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
		return sync.RowState{}, fmt.Errorf("planflow: items create insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: commit items create: %w", err)
	}
	return itemRowStateOf(r)
}

// Write applies exactly the declared fields to one line under the
// module's rules: the price legs never move (they are outside the
// writable set, so the lww merge cannot touch them), every real write
// draws a fresh seq and stamps the key, and a write whose every
// declared leg already equals the row is a no-op that draws no seq.
func (s *ItemsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkWritable(itemsWritable, fields); err != nil {
		return sync.RowState{}, err
	}
	productID := asString(fields, "product_id")
	if productID != "" && !looksLikeUUID(productID) {
		return sync.RowState{}, errRefused("product_id must be a uuid")
	}
	qty, err := parseQty(asString(fields, "qty_entered"))
	if err != nil {
		return sync.RowState{}, err
	}
	uom := asString(fields, "uom_entered")
	notes := asString(fields, "notes")
	if len(notes) > maxNotesLen {
		return sync.RowState{}, errRefused("notes is too long (max %d)", maxNotesLen)
	}
	desc := asString(fields, "description")
	specialDesc := asString(fields, "special_order_description")
	isSpecial, err := boolLeg(fields, "is_special_order")
	if err != nil {
		return sync.RowState{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: begin items write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	before, err := scanItem(tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM plan_line WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE`, itemsColumns), id))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: lock line %q for sync write: %w", id, err)
	}
	if sameLine(fields, before, productID, desc, qty, uom, notes, specialDesc, isSpecial) {
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("planflow: commit items no-op: %w", err)
		}
		return itemRowStateOf(before)
	}
	seq, err := allocateSeqTx(ctx, tx, scopeItemsSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanItem(tx.QueryRow(ctx, `
		UPDATE plan_line SET
			product_id  = COALESCE(NULLIF($2,'')::uuid, product_id),
			description = COALESCE(NULLIF($3,''), description),
			qty_entered = COALESCE(NULLIF($4,'')::decimal, qty_entered),
			uom_entered = COALESCE(NULLIF($5,''), uom_entered),
			notes       = COALESCE(NULLIF($6,''), notes),
			special_order_description = COALESCE(NULLIF($7,''), special_order_description),
			is_special_order = COALESCE($8, is_special_order),
			updated_at  = now(),
			seq         = $9,
			idempotency_key = $10::uuid
		WHERE id = $1::uuid
		RETURNING `+itemsColumns,
		id, productID, desc, qty, uom, notes, specialDesc, isSpecial, seq, key))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: items write %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: commit items write: %w", err)
	}
	return itemRowStateOf(r)
}

// sameLine reports whether every declared leg already equals the locked
// row, the no-op rule.
func sameLine(fields map[string]any, before itemRow, productID, desc, qty, uom, notes, specialDesc string, isSpecial *bool) bool {
	if _, ok := fields["product_id"]; ok && productID != before.Line.ProductID {
		return false
	}
	if _, ok := fields["description"]; ok && desc != before.Line.Description {
		return false
	}
	if _, ok := fields["qty_entered"]; ok && qty != before.Line.QtyEntered {
		return false
	}
	if _, ok := fields["uom_entered"]; ok && uom != before.Line.UOMEntered {
		return false
	}
	if _, ok := fields["notes"]; ok && notes != before.Line.Notes {
		return false
	}
	if _, ok := fields["special_order_description"]; ok && specialDesc != before.Line.SpecialOrderDescription {
		return false
	}
	if isSpecial != nil && *isSpecial != before.Line.IsSpecialOrder {
		return false
	}
	return true
}

// Delete tombstones one line: deleted_at set, the row retained, a
// fresh seq, the idempotency key not restamped.
func (s *ItemsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	_ = key
	if _, _, ok := tenantPredicate(f); !ok || !looksLikeUUID(id) {
		return sync.RowState{}, sync.ErrOpRefused
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: begin items delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The tombstone relies on the sync seq trigger (0422): the row lock first, then the counter. An explicit
	// draw ahead of the UPDATE would take the counter before the row and cross a writer that relies on the
	// trigger (PR 556 round 3c); RETURNING answers the seq the trigger drew.
	r, err := scanItem(tx.QueryRow(ctx, `
		UPDATE plan_line SET
			deleted_at = now(), updated_at = now()
		WHERE id = $1::uuid AND org_id = $2::uuid AND tenant_id = $3::uuid AND deleted_at IS NULL
		RETURNING `+itemsColumns,
		id, f.OrgID, f.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: items delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: commit items delete: %w", err)
	}
	rs, err := itemRowStateOf(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}

// boolLeg reads one payload leg as a bool, nil when absent.
func boolLeg(fields map[string]any, key string) (*bool, error) {
	raw, ok := fields[key]
	if !ok || raw == nil {
		return nil, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return nil, errRefused("%s must be a boolean", key)
	}
	return &b, nil
}
