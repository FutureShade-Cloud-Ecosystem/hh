// syncscope_readcache.go is the payments module's sync surface (HH-3.5
// #169): the payment_methods pull-only Store over portal_payment_methods,
// the table this module owns (migration 0027, the boundary model.go
// records). The fence rules hold: the sync core owns the protocol and no
// table, this module owns the table and meets the core at the Store seam.
// The scope is pull-only (Colton's START ruling): GET serves the tenant's
// cached method rows, every write primitive fails closed, and the route
// answers the contract's own refusal for POST.
package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PaymentMethodsStore is the payment_methods pull-only Store.
type PaymentMethodsStore struct {
	pool *pgxpool.Pool
}

// NewPaymentMethodsStore wires the adapter.
func NewPaymentMethodsStore(pool *pgxpool.Pool) *PaymentMethodsStore {
	return &PaymentMethodsStore{pool: pool}
}

// paymentMethodColumns is the projection: the display legs only. The
// vault token is the processor's credential and is not in the list.
const paymentMethodColumns = `id::text, customer_id::text, gateway, method_type, brand, last4,
	exp_month, exp_year, is_default, status, created_at, updated_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// PaymentMethodRow is the wire shape of one cached method row.
type PaymentMethodRow struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	Gateway    string    `json:"gateway"`
	MethodType string    `json:"method_type"`
	Brand      string    `json:"brand"`
	Last4      string    `json:"last4"`
	ExpMonth   *int      `json:"exp_month"`
	ExpYear    *int      `json:"exp_year"`
	IsDefault  bool      `json:"is_default"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// tenantPredicate is the mandatory scoping predicate: the tenant from
// the middleware-stamped filter (G4). A method row's customer IS the
// tenant anchor, and the 0213 tenant_id stamp carries the same fact
// (the equality ADR-0020 records), so both columns predicate the one
// tenant; the org plays no part in the row-level fence.
func tenantPredicate(f sync.ScopeFilter) (string, bool) {
	if !f.Tenant || f.TenantID == "" || f.OrgID == "" {
		return "", false
	}
	return "customer_id = $1::uuid AND tenant_id = $1::uuid", true
}

// looksLikeUUID is the shared boundary guard.
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
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}

// Pull returns the page of method changes past since under the tenant
// predicate.
func (s *PaymentMethodsStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, ok := tenantPredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM portal_payment_methods
		 WHERE %s AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`, paymentMethodColumns, where),
		f.TenantID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("payments: payment methods pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		var m PaymentMethodRow
		var seq int64
		var deleted bool
		if err := rows.Scan(&m.ID, &m.CustomerID, &m.Gateway, &m.MethodType, &m.Brand, &m.Last4,
			&m.ExpMonth, &m.ExpYear, &m.IsDefault, &m.Status, &m.CreatedAt, &m.UpdatedAt,
			&seq, &deleted); err != nil {
			return nil, nil, fmt.Errorf("payments: payment methods pull scan: %w", err)
		}
		if deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: m.ID, Seq: seq})
			continue
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return nil, nil, fmt.Errorf("payments: payment methods projection: %w", err)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, nil, fmt.Errorf("payments: payment methods projection decode: %w", err)
		}
		items = append(items, sync.Entity{ID: m.ID, Seq: seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("payments: payment methods pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied fails closed: the scope has no push.
func (s *PaymentMethodsStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	return sync.RowState{}, false, sync.ErrOpRefused
}

// Load reads one method row by id under the tenant predicate.
func (s *PaymentMethodsStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, ok := tenantPredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	var m PaymentMethodRow
	var seq int64
	var deleted bool
	err := s.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM portal_payment_methods WHERE id = $2::uuid AND %s`,
		paymentMethodColumns, where), f.TenantID, id).
		Scan(&m.ID, &m.CustomerID, &m.Gateway, &m.MethodType, &m.Brand, &m.Last4,
			&m.ExpMonth, &m.ExpYear, &m.IsDefault, &m.Status, &m.CreatedAt, &m.UpdatedAt,
			&seq, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("payments: payment methods load: %w", err)
	}
	if deleted {
		return sync.RowState{ID: m.ID, Seq: seq, Deleted: true}, true, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return sync.RowState{}, false, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return sync.RowState{}, false, err
	}
	return sync.RowState{ID: m.ID, Seq: seq, Data: data}, true, nil
}

// Create fails closed: the scope has no push.
func (s *PaymentMethodsStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// Write fails closed: the scope has no push.
func (s *PaymentMethodsStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}

// Delete fails closed: the scope has no push.
func (s *PaymentMethodsStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, sync.ErrOpRefused
}
