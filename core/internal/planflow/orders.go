// orders.go is the orders Store: the contractor's board cards over the
// plan table (see the package doc for why the scope's name is the
// contractor's vocabulary for this table). The projection is the card
// the contractor edits offline (name, stage, fulfillment, requested
// date, delivery address, site instructions, po reference, board
// position); the price-bearing legs and the supplier-owned columns are
// absent from the writable sets and refuse whole.
//
// Idempotency is the row's single D30 idempotency_key column (the
// latest write or create key), the attachments module's doctrine: the
// delete leg does not restamp the column so a replayed write still
// resolves, and the engine's already-tombstoned arm answers a replayed
// delete from the retained row.
package planflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// ordersColumns is the pull projection: the card's legs plus the sync
// legs (seq, tombstone flag), unqualified so the same constant serves
// SELECTs and RETURNING alike.
const ordersColumns = `id::text, COALESCE(project_id::text,''), COALESCE(name,''), stage,
	COALESCE(fulfillment,''), to_char(requested_date, 'YYYY-MM-DD'), delivery_address,
	COALESCE(site_instructions,''), COALESCE(po_reference,''), sort_order, created_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// orderRow is one scanned row: the card's legs plus its sync legs.
type orderRow struct {
	Card            Card
	Seq             int64
	Deleted         bool
	deliveryAddress []byte
}

// Card is the wire shape of one board card, the projection the sync
// feed carries.
type Card struct {
	ID               string         `json:"id"`
	ProjectID        string         `json:"project_id"`
	Name             string         `json:"name"`
	Stage            string         `json:"stage"`
	Fulfillment      string         `json:"fulfillment"`
	RequestedDate    *string        `json:"requested_date"`
	DeliveryAddress  map[string]any `json:"delivery_address,omitempty"`
	SiteInstructions string         `json:"site_instructions"`
	POReference      string         `json:"po_reference"`
	SortOrder        int64          `json:"sort_order"`
	CreatedAt        time.Time      `json:"created_at"`
	// UpdatedAt is deliberately absent from the projection: a timestamp
	// that moves on every write is not a field a client merge owns.
}

// scanOrder reads the 14-target row. The requested date crosses as the
// DATE's own text (the database's calendar spelling): the module never
// decides which day it is, the house time zone rule.
func scanOrder(row rowScanner) (orderRow, error) {
	var r orderRow
	if err := row.Scan(
		&r.Card.ID, &r.Card.ProjectID, &r.Card.Name, &r.Card.Stage,
		&r.Card.Fulfillment, &r.Card.RequestedDate, &r.deliveryAddress, &r.Card.SiteInstructions,
		&r.Card.POReference, &r.Card.SortOrder, &r.Card.CreatedAt,
		&r.Seq, &r.Deleted,
	); err != nil {
		return orderRow{}, err
	}
	if len(r.deliveryAddress) > 0 {
		var addr map[string]any
		if err := jsonUnmarshal(r.deliveryAddress, &addr); err != nil {
			return orderRow{}, fmt.Errorf("planflow: card delivery address decode: %w", err)
		}
		r.Card.DeliveryAddress = addr
	}
	return r, nil
}

// rowStateOf folds one scanned row into the engine's RowState; a
// tombstone carries its seq and no projection.
func orderRowStateOf(r orderRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.Card.ID, Seq: r.Seq, Deleted: r.Deleted}
	if r.Deleted {
		return rs, nil
	}
	data, err := projection(r.Card)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Data = data
	return rs, nil
}

// OrdersSyncStore is the orders Store over the plan table.
type OrdersSyncStore struct {
	pool Pool
}

// NewOrdersSyncStore wires the adapter.
func NewOrdersSyncStore(pool Pool) *OrdersSyncStore {
	return &OrdersSyncStore{pool: pool}
}

// Pull returns the page of card changes past since, live rows and
// tombstones in seq order, at most limit rows total, under the tenant
// predicate.
func (s *OrdersSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, args, ok := tenantPredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM plan
		WHERE %s AND seq IS NOT NULL AND seq > $3
		ORDER BY seq ASC
		LIMIT $4`, ordersColumns, where)
	args = append(args, since, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("planflow: orders pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanOrder(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("planflow: orders pull scan: %w", err)
		}
		if r.Deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.Card.ID, Seq: r.Seq})
			continue
		}
		data, err := projection(r.Card)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.Card.ID, Seq: r.Seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("planflow: orders pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the row it applied under
// the tenant predicate: the row's idempotency_key column, the D30 shape
// (the latest write or create key).
func (s *OrdersSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, args, ok := tenantPredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM plan WHERE %s AND idempotency_key = $3::uuid LIMIT 1`,
		ordersColumns, where)
	args = append(args, key)
	r, err := scanOrder(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("planflow: orders find applied: %w", err)
	}
	rs, err := orderRowStateOf(r)
	return rs, true, err
}

// Load reads one card by id under the tenant predicate; a row outside
// the caller's tenant is the same as an absent one (existence never
// leaks).
func (s *OrdersSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.pool, f, id)
}

// ordersQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type ordersQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *OrdersSyncStore) load(ctx context.Context, db ordersQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := tenantPredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM plan WHERE id = $3::uuid AND %s`,
		ordersColumns, where)
	args = append(args, id)
	r, err := scanOrder(db.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("planflow: orders load: %w", err)
	}
	rs, err := orderRowStateOf(r)
	return rs, true, err
}

// writableCreate is the field set a pushed create may declare: the
// card-shaping legs. The birth stage is plan (a create cannot birth a
// card mid-flight), exactly the pilot's QUEUED-birth rule. project_id
// is deliberately absent: the project link is the procurement surface's
// to set, and the fence rules keep the procurement module's tables out
// of this module's queries.
var ordersWritableCreate = map[string]bool{
	"id": true, "name": true, "fulfillment": true,
	"requested_date": true, "delivery_address": true, "site_instructions": true,
	"po_reference": true, "sort_order": true,
}

// writable is the field set a pushed update may declare: the row
// address (the engine's addressing leg) and the contractor-editable
// legs, stage included through the tenant's own move matrix above.
var ordersWritable = map[string]bool{
	"id": true, "name": true, "stage": true, "fulfillment": true,
	"requested_date": true, "delivery_address": true, "site_instructions": true,
	"po_reference": true, "sort_order": true,
}

// checkWritable refuses a payload that declares anything outside the
// given set.
func checkWritable(set map[string]bool, fields map[string]any) error {
	for k := range fields {
		if !set[k] {
			return errRefused("field %q is not writable through sync", k)
		}
	}
	return nil
}

// parseRequestedDate reads the requested_date leg: absent or empty
// clears it, otherwise a strict YYYY-MM-DD date (the DATE column's own
// grammar).
func parseRequestedDate(v string) (*string, error) {
	if v == "" {
		return nil, nil
	}
	if len(v) != 10 || v[4] != '-' || v[7] != '-' {
		return nil, errRefused("requested_date must be a YYYY-MM-DD date")
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return nil, errRefused("requested_date must be a YYYY-MM-DD date")
	}
	return &v, nil
}

// Create inserts one card from a pushed create: org and tenant stamp
// from the filter, a fresh orders seq, the idempotency key, and the
// birth stage plan. The id is the payload's when the client generated
// one (D30), else the server assigns it.
func (s *OrdersSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkWritable(ordersWritableCreate, fields); err != nil {
		return sync.RowState{}, err
	}
	name := asString(fields, "name")
	if len(name) > maxNameLen {
		return sync.RowState{}, errRefused("name is too long (max %d)", maxNameLen)
	}
	fulfillment := asString(fields, "fulfillment")
	if fulfillment != "" && !validFulfillment(fulfillment) {
		return sync.RowState{}, errRefused("unknown fulfillment %q", fulfillment)
	}
	requested, err := parseRequestedDate(asString(fields, "requested_date"))
	if err != nil {
		return sync.RowState{}, err
	}
	address, err := asJSONLeg(fields, "delivery_address")
	if err != nil {
		return sync.RowState{}, errRefused("delivery_address is not a json object")
	}
	instructions := asString(fields, "site_instructions")
	if len(instructions) > maxInstructionsLen {
		return sync.RowState{}, errRefused("site_instructions is too long (max %d)", maxInstructionsLen)
	}
	poRef := asString(fields, "po_reference")
	if len(poRef) > maxPORefLen {
		return sync.RowState{}, errRefused("po_reference is too long (max %d)", maxPORefLen)
	}
	sortOrder, err := intLeg(fields, "sort_order")
	if err != nil {
		return sync.RowState{}, err
	}
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return sync.RowState{}, errRefused("payload id must be a uuid")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: begin orders create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateSeqTx(ctx, tx, ordersSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanOrder(tx.QueryRow(ctx, `
		INSERT INTO plan (id, org_id, tenant_id, name, stage, fulfillment,
		                  requested_date, delivery_address, site_instructions, po_reference,
		                  sort_order, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3::uuid,
		        $4, 'plan', NULLIF($5,''), NULLIF($6,'')::date,
		        $7::jsonb, $8, $9, COALESCE($10, 0), $11, $12::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+ordersColumns,
		id, f.OrgID, f.TenantID, name, fulfillment, requested, address,
		instructions, poRef, sortOrder, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		// The client-chosen id is taken; WHERE decides the refusal so
		// existence never leaks across the tenant boundary.
		if _, own, _ := s.load(ctx, tx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The unique idempotency index fired on another row: the key was
		// spent elsewhere, the caller's own defect.
		return sync.RowState{}, sync.ErrRowExists
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: orders create insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: commit orders create: %w", err)
	}
	return orderRowStateOf(r)
}

// tenantOwnsProject was deliberately not written: the project link leg
// is outside the writable sets, so no cross-module read is needed (see
// ordersWritableCreate).

// Write applies exactly the declared fields to one card, enforcing the
// module's own rules: the stage move must be the tenant's own (the
// supplier's columns refuse), and every write draws a fresh seq and
// stamps the idempotency key. A write whose every declared leg already
// equals the row is a no-op that draws no seq and restamps nothing, so
// a replayed operation that already landed converges on the unchanged
// row instead of churning the feed.
func (s *OrdersSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkWritable(ordersWritable, fields); err != nil {
		return sync.RowState{}, err
	}
	stage, hasStage := fields["stage"].(string)
	if hasStage && !validStage(stage) {
		return sync.RowState{}, errRefused("unknown stage %q", stage)
	}
	fulfillment := asString(fields, "fulfillment")
	if fulfillment != "" && !validFulfillment(fulfillment) {
		return sync.RowState{}, errRefused("unknown fulfillment %q", fulfillment)
	}
	name := asString(fields, "name")
	if len(name) > maxNameLen {
		return sync.RowState{}, errRefused("name is too long (max %d)", maxNameLen)
	}
	instructions := asString(fields, "site_instructions")
	if len(instructions) > maxInstructionsLen {
		return sync.RowState{}, errRefused("site_instructions is too long (max %d)", maxInstructionsLen)
	}
	poRef := asString(fields, "po_reference")
	if len(poRef) > maxPORefLen {
		return sync.RowState{}, errRefused("po_reference is too long (max %d)", maxPORefLen)
	}
	requested, err := parseRequestedDate(asString(fields, "requested_date"))
	if err != nil {
		return sync.RowState{}, err
	}
	address, err := asJSONLeg(fields, "delivery_address")
	if err != nil {
		return sync.RowState{}, errRefused("delivery_address is not a json object")
	}
	sortOrder, err := intLeg(fields, "sort_order")
	if err != nil {
		return sync.RowState{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: begin orders write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	before, err := scanOrder(tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM plan WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE`, ordersColumns), id))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: lock card %q for sync write: %w", id, err)
	}
	if hasStage && stage != before.Card.Stage {
		if !tenantStageMove(before.Card.Stage, stage) {
			return sync.RowState{}, errRefused("the supplier moves a card to %s; your move refuses", stage)
		}
	}
	// The no-op rule: every declared leg already equal to the row.
	if sameCard(fields, before, name, fulfillment, requested, address, instructions, poRef, sortOrder) {
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("planflow: commit orders no-op: %w", err)
		}
		return orderRowStateOf(before)
	}
	seq, err := allocateSeqTx(ctx, tx, ordersSeqScope)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanOrder(tx.QueryRow(ctx, `
		UPDATE plan SET
			name             = COALESCE(NULLIF($2,''), name),
			stage            = COALESCE(NULLIF($3,''), stage),
			fulfillment      = COALESCE(NULLIF($4,''), fulfillment),
			requested_date   = COALESCE($5::date, requested_date),
			delivery_address = COALESCE($6::jsonb, delivery_address),
			site_instructions = COALESCE(NULLIF($7,''), site_instructions),
			po_reference     = COALESCE(NULLIF($8,''), po_reference),
			sort_order       = COALESCE($9, sort_order),
			updated_at       = now(),
			seq              = $10,
			idempotency_key  = $11::uuid
		WHERE id = $1::uuid
		RETURNING `+ordersColumns,
		id, name, stage, fulfillment, requested, address, instructions, poRef, sortOrder, seq, key))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: orders write %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: commit orders write: %w", err)
	}
	return orderRowStateOf(r)
}

// sameCard reports whether every declared leg of the write already
// equals the locked row, the no-op rule above.
func sameCard(fields map[string]any, before orderRow, name, fulfillment string, requested *string, address []byte, instructions, poRef string, sortOrder *int64) bool {
	if _, ok := fields["name"]; ok && name != before.Card.Name {
		return false
	}
	if _, ok := fields["stage"]; ok {
		return false // a stage leg is a move, never a no-op
	}
	if _, ok := fields["fulfillment"]; ok && fulfillment != before.Card.Fulfillment {
		return false
	}
	if _, ok := fields["requested_date"]; ok {
		var cur *string = before.Card.RequestedDate
		if (requested == nil) != (cur == nil) || (requested != nil && *requested != *cur) {
			return false
		}
	}
	if _, ok := fields["delivery_address"]; ok {
		var beforeAddr []byte
		if len(before.deliveryAddress) > 0 {
			beforeAddr = before.deliveryAddress
		}
		if string(address) != string(beforeAddr) {
			return false
		}
	}
	if _, ok := fields["site_instructions"]; ok && instructions != before.Card.SiteInstructions {
		return false
	}
	if _, ok := fields["po_reference"]; ok && poRef != before.Card.POReference {
		return false
	}
	if _, ok := fields["sort_order"]; ok && (sortOrder == nil || *sortOrder != before.Card.SortOrder) {
		return false
	}
	return true
}

// Delete tombstones one card: deleted_at set, the row retained, a
// fresh seq, the idempotency key NOT restamped (the attachments
// doctrine: the last write key must keep resolving).
func (s *OrdersSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	_ = key // not stamped, see above
	if _, _, ok := tenantPredicate(f); !ok || !looksLikeUUID(id) {
		return sync.RowState{}, sync.ErrOpRefused
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: begin orders delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The tombstone relies on the sync seq trigger (0422): the row lock first, then the counter. An explicit
	// draw ahead of the UPDATE would take the counter before the row and cross a writer that relies on the
	// trigger (PR 556 round 3c); RETURNING answers the seq the trigger drew.
	r, err := scanOrder(tx.QueryRow(ctx, `
		UPDATE plan SET
			deleted_at = now(), updated_at = now()
		WHERE id = $1::uuid AND org_id = $2::uuid AND tenant_id = $3::uuid AND deleted_at IS NULL
		RETURNING `+ordersColumns,
		id, f.OrgID, f.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: orders delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: commit orders delete: %w", err)
	}
	rs, err := orderRowStateOf(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}

// intLeg reads one payload leg as an int64, nil when absent; the
// engine hands the adapter the map the client built (a direct Go push
// carries int or int64, a JSON-decoded push carries float64), so every
// integer spelling reads.
func intLeg(fields map[string]any, key string) (*int64, error) {
	switch raw := fields[key].(type) {
	case nil:
		return nil, nil
	case float64:
		if raw != float64(int64(raw)) {
			return nil, errRefused("%s must be an integer", key)
		}
		v := int64(raw)
		return &v, nil
	case int:
		v := int64(raw)
		return &v, nil
	case int64:
		return &raw, nil
	}
	return nil, errRefused("%s must be an integer", key)
}
