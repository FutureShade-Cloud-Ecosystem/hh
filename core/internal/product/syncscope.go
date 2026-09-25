// syncscope.go is the product module's pull-only sync Stores (HH-3.5
// #169): products (staff callers, org from X-Org) and catalog (tenant
// callers, the org the tenant's Team resolves to, never from X-Org).
// Both are Colton's START ruling on undeclared scopes: the client never
// writes them, so the POST answers the contract's refusal at the route
// and the readcache skeleton's write primitives fail closed. The
// catalog is the SAME product rows read through the tenant's org:
// catalogue data is shared (the carried portal catalog is
// tenant-agnostic), and the priced-per-account artifact is the pricing
// module's resolved output, never these rows (C55).
package product

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// productsColumns is the shared projection: the catalogue's
// identification legs plus the sync legs.
const productsColumns = `id::text, sku::text, name, COALESCE(description,''), COALESCE(manufacturer,''),
	COALESCE(collection,''), COALESCE(sold_by,''), COALESCE(base_uom,''), is_special_order,
	is_active, lead_time_days, created_at, updated_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// productRow is the wire shape of one cached product row.
type productRow struct {
	ID             string    `json:"id"`
	SKU            string    `json:"sku"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Manufacturer   string    `json:"manufacturer"`
	Collection     string    `json:"collection"`
	SoldBy         string    `json:"sold_by"`
	BaseUOM        string    `json:"base_uom"`
	IsSpecialOrder bool      `json:"is_special_order"`
	IsActive       bool      `json:"is_active"`
	LeadTimeDays   *int      `json:"lead_time_days"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// scanProductRow reads the 15-target row and projects it.
func scanProductRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r productRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.SKU, &r.Name, &r.Description, &r.Manufacturer,
		&r.Collection, &r.SoldBy, &r.BaseUOM, &r.IsSpecialOrder, &r.IsActive,
		&r.LeadTimeDays, &r.CreatedAt, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("product: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("product: sync projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// orgProductsPredicate is the org predicate for a staff caller.
func orgProductsPredicate(f sync.ScopeFilter) ([]any, bool) {
	if f.Tenant || f.OrgID == "" {
		return nil, false
	}
	return []any{f.OrgID}, true
}

// tenantOrgProductsPredicate is the tenant's org predicate (G4: the org
// from the Team, never from X-Org).
func tenantOrgProductsPredicate(f sync.ScopeFilter) ([]any, bool) {
	if !f.Tenant || f.OrgID == "" {
		return nil, false
	}
	return []any{f.OrgID}, true
}

// NewProductsStore builds the products Store (org tenancy).
func NewProductsStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + productsColumns + ` FROM products
		 WHERE org_id = $1::uuid AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`,
		ByID: `SELECT ` + productsColumns + ` FROM products
		 WHERE org_id = $1::uuid AND id = $2::uuid`,
		Args: orgProductsPredicate,
		Scan: scanProductRow,
	})
}

// NewCatalogStore builds the catalog Store (tenant tenancy).
func NewCatalogStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + productsColumns + ` FROM products
		 WHERE org_id = $1::uuid AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`,
		ByID: `SELECT ` + productsColumns + ` FROM products
		 WHERE org_id = $1::uuid AND id = $2::uuid`,
		Args: tenantOrgProductsPredicate,
		Scan: scanProductRow,
	})
}
