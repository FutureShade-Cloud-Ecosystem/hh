// syncscope.go is the procurement module's pull-only projects Store
// (HH-3.5 #169): the planning board's projects as the contractor's read
// cache. The POST answers the contract's refusal at the route (Colton's
// START ruling); the readcache skeleton's write primitives fail
// closed. The writes stay on the module's own REST surface (the board's
// compose and submit-rfq routes), which is where they belong: a
// project carries indicative prices, and an offline outbox replaying a
// project write would move financial intent without the online gates.
package procurement

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// projectsColumns is the sync projection: the project legs plus the
// sync legs.
const projectsColumns = `id::text, customer_id::text, name, status,
	created_at, updated_at,
	seq, (deleted_at IS NOT NULL) AS deleted`

// projectRow is the wire shape of one cached project row.
type projectRow struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// scanProjectRow reads the 8-target row and projects it.
func scanProjectRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r projectRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.CustomerID, &r.Name, &r.Status, &r.CreatedAt, &r.UpdatedAt,
		&seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("procurement: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("procurement: sync projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// projectsPredicate is the tenant predicate: the tenant from the token
// (G4), matched on the row's tenant_id (0213) and customer_id, which
// the 0213 equality holds to one fact.
func projectsPredicate(f sync.ScopeFilter) ([]any, bool) {
	if !f.Tenant || f.TenantID == "" || f.OrgID == "" {
		return nil, false
	}
	return []any{f.TenantID}, true
}

// NewProjectsStore builds the projects Store.
func NewProjectsStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + projectsColumns + ` FROM procurement_projects
		 WHERE tenant_id = $1::uuid AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`,
		ByID: `SELECT ` + projectsColumns + ` FROM procurement_projects
		 WHERE tenant_id = $1::uuid AND id = $2::uuid`,
		Args: projectsPredicate,
		Scan: scanProjectRow,
	})
}
