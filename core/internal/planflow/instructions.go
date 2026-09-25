// instructions.go is the delivery_instructions Store, the field crew's
// editable delivery note, last_write_wins_by_field. The legs are the
// card's instruction pair: site_instructions (the note the driver
// reads) and delivery_address (where to put the material), the plan
// table's own columns for exactly this (domain-map 5.4: the contractor
// side carries the requested date, delivery address and site
// instructions). The supplier's own stop-level instructions on the
// dispatch tables are the desk's, read through D17's contractor stop
// view, never written here.
//
// WHY THIS TABLE AND NOT orders.delivery_instructions: the carried
// portal surface writes the dealer order's note column, and that column
// has no create leg a tenant can honestly push (the dealer orders table
// is born at the desk); the plan card is the contractor's own editable
// document whose instruction pair IS this scope, and its D30 keys are
// what an offline create needs. The scope shares the plan table's
// orders counter with the orders scope (one row set, one feed counter,
// monotonic across both), so a card write through either scope
// reappears in both feeds: each feed projects its own legs off the same
// version history, which is the honest shape of two scopes over one
// document.
package planflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// InstructionsSyncStore is the delivery_instructions Store over the
// plan table's instruction pair.
type InstructionsSyncStore struct {
	pool Pool
}

// NewInstructionsSyncStore wires the adapter.
func NewInstructionsSyncStore(pool Pool) *InstructionsSyncStore {
	return &InstructionsSyncStore{pool: pool}
}

// Pull returns the page of instruction-document changes past since
// under the tenant predicate, the same rows the orders scope feeds.
func (s *InstructionsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
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
		return nil, nil, fmt.Errorf("planflow: instructions pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanOrder(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("planflow: instructions pull scan: %w", err)
		}
		if r.Deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.Card.ID, Seq: r.Seq})
			continue
		}
		data, err := instructionData(r)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.Card.ID, Seq: r.Seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("planflow: instructions pull: %w", err)
	}
	return items, tombstones, nil
}

// instructionData projects one card as the instruction document: the
// row address, the order number the crew reads, and the instruction
// pair.
func instructionData(r orderRow) (map[string]any, error) {
	doc := struct {
		ID               string         `json:"id"`
		Name             string         `json:"name"`
		Stage            string         `json:"stage"`
		RequestedDate    *string        `json:"requested_date"`
		DeliveryAddress  map[string]any `json:"delivery_address,omitempty"`
		SiteInstructions string         `json:"site_instructions"`
		UpdatedAt        string         `json:"-"`
	}{
		ID:               r.Card.ID,
		Name:             r.Card.Name,
		Stage:            r.Card.Stage,
		RequestedDate:    r.Card.RequestedDate,
		DeliveryAddress:  r.Card.DeliveryAddress,
		SiteInstructions: r.Card.SiteInstructions,
	}
	return projection(doc)
}

// FindApplied resolves one idempotency key against the plan table's
// single D30 column, shared with the orders scope (the latest key
// across both scopes; see the package doc for the cross-scope replay
// edge that leaves).
func (s *InstructionsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	store := &OrdersSyncStore{pool: s.pool}
	return store.FindApplied(ctx, f, key)
}

// Load reads one instruction document by card id under the tenant
// predicate.
func (s *InstructionsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, args, ok := tenantPredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	q := fmt.Sprintf(`SELECT %s FROM plan WHERE id = $3::uuid AND %s`, ordersColumns, where)
	args = append(args, id)
	r, err := scanOrder(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("planflow: instructions load: %w", err)
	}
	rs := sync.RowState{ID: r.Card.ID, Seq: r.Seq, Deleted: r.Deleted}
	if !r.Deleted {
		data, err := instructionData(r)
		if err != nil {
			return sync.RowState{}, false, err
		}
		rs.Data = data
	}
	return rs, true, nil
}

// instructionsCreateWritable is the create set: the address pair and
// the card-shaping legs a birth carries (the same birth legs the orders
// scope admits, so a field device can attach instructions to a card it
// is composing offline).
var instructionsCreateWritable = map[string]bool{
	"id": true, "name": true, "fulfillment": true, "requested_date": true,
	"delivery_address": true, "site_instructions": true,
}

// instructionsWritable is the update set: the instruction pair alone.
// Stage, fulfillment and every other card leg belong to the orders
// scope's writes; a delivery-instructions edit that tried to move the
// card would be a different operation wearing this scope's name.
var instructionsWritable = map[string]bool{
	"id": true, "site_instructions": true, "delivery_address": true,
}

// Create births one card carrying the instruction pair: delegated to
// the orders scope's create (the same birth rules, the same counter),
// then re-projected as the instruction document.
func (s *InstructionsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	for k := range fields {
		if !instructionsCreateWritable[k] {
			return sync.RowState{}, errRefused("field %q is not writable through sync", k)
		}
	}
	store := &OrdersSyncStore{pool: s.pool}
	rs, err := store.Create(ctx, f, key, fields)
	if err != nil {
		return rs, err
	}
	row, ok, err := s.Load(ctx, f, rs.ID)
	if err != nil || !ok {
		return rs, nil
	}
	return row, nil
}

// Write applies exactly the declared instruction legs under
// last_write_wins_by_field: the merge writes the declared legs only,
// the row keeps every other leg the desk or the owner left, and every
// real write draws a fresh orders-counter seq (the shared feed) and
// stamps the idempotency key. A no-op write draws no seq.
func (s *InstructionsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	if err := checkWritable(instructionsWritable, fields); err != nil {
		return sync.RowState{}, err
	}
	instructions := asString(fields, "site_instructions")
	if len(instructions) > maxInstructionsLen {
		return sync.RowState{}, errRefused("site_instructions is too long (max %d)", maxInstructionsLen)
	}
	address, err := asJSONLeg(fields, "delivery_address")
	if err != nil {
		return sync.RowState{}, errRefused("delivery_address is not a json object")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: begin instructions write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	before, err := scanOrder(tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM plan WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE`, ordersColumns), id))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: lock card %q for instructions write: %w", id, err)
	}
	addrSame := string(address) == string(before.deliveryAddress)
	// The no-op rule: neither declared leg differs means the write draws
	// no seq and restamps nothing.
	if instructions != before.Card.SiteInstructions || !addrSame {
		seq, err := allocateSeqTx(ctx, tx, ordersSeqScope)
		if err != nil {
			return sync.RowState{}, err
		}
		r, err := scanOrder(tx.QueryRow(ctx, `
			UPDATE plan SET
				site_instructions = COALESCE(NULLIF($2,''), site_instructions),
				delivery_address  = COALESCE($3::jsonb, delivery_address),
				updated_at        = now(),
				seq               = $4,
				idempotency_key   = $5::uuid
			WHERE id = $1::uuid
			RETURNING `+ordersColumns,
			id, instructions, address, seq, key))
		if err != nil {
			return sync.RowState{}, fmt.Errorf("planflow: instructions write %q: %w", id, err)
		}
		before = r
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("planflow: commit instructions write: %w", err)
	}
	rs := sync.RowState{ID: before.Card.ID, Seq: before.Seq}
	data, err := instructionData(before)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Data = data
	return rs, nil
}

// Delete tombstones the card, the same fact an orders-scope delete
// writes (one row set, one tombstone), drawing the shared counter.
func (s *InstructionsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	store := &OrdersSyncStore{pool: s.pool}
	rs, err := store.Delete(ctx, f, id, key)
	if err != nil {
		return rs, err
	}
	return rs, nil
}
