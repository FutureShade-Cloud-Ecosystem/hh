// syncscope.go is the inventory module's pull-only inventory_lookup
// Store (HH-3.5 #169): the yard's read seam over stock_balances, the
// on-hand and allocated projection a picker's device caches. The POST
// answers the contract's refusal at the route (Colton's START ruling);
// the readcache skeleton's write primitives fail closed.
//
// THE ROW ID IS DERIVED. stock_balances is keyed by the pair
// (product_id, location_id); the contract's SyncEntity.id carries a
// uuid, so the projection derives one: uuid v5 over the pair under this
// module's namespace, deterministic across pulls so a device's cached
// row keeps one id while the balance moves. The pair itself rides in
// the projection, so nothing is hidden behind the derivation.
package inventory

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// balanceNamespace is the uuid v5 namespace of the derived balance ids.
var balanceNamespace = uuid.MustParse("f1a7ba1a-0000-4000-8000-590000000001")

// balancesColumns is the sync projection: the balance legs plus the
// sync legs.
const balancesColumns = `product_id::text, location_id::text, branch_id::text,
	on_hand_base::text, allocated_base::text, updated_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// balanceRow is the wire shape of one cached balance row.
type balanceRow struct {
	ID            string    `json:"id"`
	ProductID     string    `json:"product_id"`
	LocationID    string    `json:"location_id"`
	BranchID      string    `json:"branch_id"`
	OnHandBase    string    `json:"on_hand_base"`
	AllocatedBase string    `json:"allocated_base"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// balanceID derives the stable uuid of one (product, location) pair.
func balanceID(productID, locationID string) string {
	return uuid.NewSHA1(balanceNamespace, []byte(productID+"|"+locationID)).String()
}

// scanBalanceRow reads the 8-target row and projects it.
func scanBalanceRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r balanceRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ProductID, &r.LocationID, &r.BranchID, &r.OnHandBase,
		&r.AllocatedBase, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	r.ID = balanceID(r.ProductID, r.LocationID)
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("inventory: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("inventory: sync projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// balancesPredicate is the org predicate over the module's own table.
func balancesPredicate(f sync.ScopeFilter) ([]any, bool) {
	if f.Tenant || f.OrgID == "" {
		return nil, false
	}
	return []any{f.OrgID}, true
}

// NewInventoryLookupStore builds the inventory_lookup Store.
func NewInventoryLookupStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + balancesColumns + ` FROM stock_balances
		 WHERE org_id = $1::uuid AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`,
		// ByID stays empty: the balance row's natural key is the
		// (product, location) pair and the feed's id is derived from it;
		// no write path addresses one row by id, so Load answers the
		// honest not-found.
		Args: balancesPredicate,
		Scan: scanBalanceRow,
	})
}
