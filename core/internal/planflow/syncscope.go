// syncscope.go is the planflow module's shared sync plumbing: the two
// scope_sequences counters, the tenant scope predicate, the projection
// helper and the read-scan surface. The two Stores (orders over plan,
// scope_items over plan_line) live in their own files beside this one.
//
// Tenancy is the whole ballgame on these scopes (the pack's #169
// section): the tenant comes from the token through the middleware
// stamped filter, never from a header or a payload leg, and every query
// below carries org_id AND tenant_id from that filter. A staff filter
// fails closed and answers nothing: these are tenant scopes and the
// route's RBAC-403 has already refused the caller the adapter never
// sees.
package planflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// The scope_sequences counter rows the module's writes draw from: one
// counter per sync scope whose projection is one of the module's tables
// (orders for plan, scope_items for plan_line), the 0213 convention.
const (
	ordersSeqScope     = "orders"
	scopeItemsSeqScope = "scope_items"
)

// allocateSeqTx draws the next seq of one scope inside the caller's
// transaction, the same upsert every module write path runs.
func allocateSeqTx(ctx context.Context, tx pgx.Tx, scope string) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, scope).Scan(&next); err != nil {
		return 0, fmt.Errorf("planflow: seq allocate for scope %s: %w", scope, err)
	}
	return next, nil
}

// rowScanner is the shared scan surface a row or row-set carries.
type rowScanner interface {
	Scan(dest ...any) error
}

// Pool is the module's pool handle for the Stores.
type Pool = *pgxpool.Pool

// tenantPredicate is the mandatory scoping predicate every adapter query
// runs under: the org and tenant the middleware stamped, both from the
// verified token's world (G4: the tenant never from X-Org). ok false
// fails closed and answers nothing.
func tenantPredicate(f sync.ScopeFilter) (string, []any, bool) {
	if !f.Tenant || f.TenantID == "" || f.OrgID == "" {
		return "", nil, false
	}
	return "org_id = $1::uuid AND tenant_id = $2::uuid", []any{f.OrgID, f.TenantID}, true
}

// projection marshals one row struct through its json tags into the
// wire map the contract's SyncEntity.data carries, so omitempty and the
// nullable legs land exactly as a REST surface would write them.
func projection(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("planflow: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("planflow: sync projection decode: %w", err)
	}
	return data, nil
}

// asString reads one payload leg as a string, with the empty string for
// an absent or null leg.
func asString(fields map[string]any, key string) string {
	s, _ := fields[key].(string)
	return s
}

// asJSONLeg marshals one payload leg a client declared as a JSON object
// (delivery_address, metadata) into the bytes a JSONB column takes;
// absent returns nil so the column stays NULL.
func asJSONLeg(fields map[string]any, key string) ([]byte, error) {
	raw, ok := fields[key]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("planflow: leg %s is not a json object", key)
	}
	return b, nil
}

// jsonUnmarshal is the one decode door the module uses, named so the
// scan sites read plainly.
var jsonUnmarshal = json.Unmarshal
