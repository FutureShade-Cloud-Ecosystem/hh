// syncscope.go is the customer module's pull-only customers Store
// (HH-3.5 #169, Colton's START ruling on undeclared scopes): the
// reference cache the counter and the desk read. The POST answers the
// contract's refusal at the route; the readcache skeleton's write
// primitives fail closed. The projection is the module's own
// identification legs; credit, hold and tax status are never cached on
// a device (they are server-decided facts, domain-map section 6).
package customer

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// customersColumns is the sync projection: the identification legs plus
// the sync legs.
const customersColumns = `id::text, account_no::text, name, COALESCE(home_branch_id::text,''),
	customer_type, is_active, created_at, updated_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// customersPredicate is the org predicate: org from X-Org under the
// staff bearer, stamped by the middleware (G4). A tenant filter fails
// closed.
func customersPredicate(f sync.ScopeFilter) ([]any, bool) {
	if f.Tenant || f.OrgID == "" {
		return nil, false
	}
	return []any{f.OrgID}, true
}

// NewCustomersStore builds the customers Store over the module's own
// query: every row the page carries sits under the org predicate, and
// the scan reads the module's own columns.
func NewCustomersStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + customersColumns + ` FROM customers
		 WHERE org_id = $1::uuid AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`,
		ByID: `SELECT ` + customersColumns + ` FROM customers
		 WHERE org_id = $1::uuid AND id = $2::uuid`,
		Args: customersPredicate,
		Scan: scanCustomerRow,
	})
}

// customerRow is the wire shape of one cached customer row.
type customerRow struct {
	ID           string    `json:"id"`
	AccountNo    string    `json:"account_no"`
	Name         string    `json:"name"`
	HomeBranchID string    `json:"home_branch_id"`
	CustomerType string    `json:"customer_type"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// scanCustomerRow reads the 10-target row and projects it.
func scanCustomerRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r customerRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.AccountNo, &r.Name, &r.HomeBranchID, &r.CustomerType, &r.IsActive,
		&r.CreatedAt, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	data, err := projectCustomer(r)
	return r.ID, seq, deleted, data, err
}

// projectCustomer marshals one row through its json tags.
func projectCustomer(r customerRow) (map[string]any, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("customer: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("customer: sync projection decode: %w", err)
	}
	return data, nil
}
