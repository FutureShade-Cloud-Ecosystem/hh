// syncscope.go is the pos module's pull-only parked_tickets Store
// (HH-3.5 #169): the till's parked-sale cache (D5's read-cache-only
// till). The POST answers the contract's refusal at the route (Colton's
// START ruling); the readcache skeleton's write primitives fail
// closed. The org scope rides the branch: pos_parked_tickets carries
// branch_id, and the branch resolves to the org through locations (the
// join stays inside the module's own read surface: locations is the
// shared branch table every module reads for scoping).
package pos

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// parkedColumns is the sync projection: the parked ticket's legs plus
// the sync legs. The lines jsonb rides as the projection's lines leg
// (re-priced at resume, so the cache carries what was parked, not a
// price).
const parkedColumns = `p.id::text, p.ticket_no::text, p.branch_id::text, COALESCE(p.customer_id::text,''),
	p.sale_type, COALESCE(p.notes,''), p.status, p.lines, p.created_at, p.updated_at,
	p.seq, (p.deleted_at IS NOT NULL) AS deleted`

// parkedRow is the wire shape of one cached parked ticket.
type parkedRow struct {
	ID         string          `json:"id"`
	TicketNo   string          `json:"ticket_no"`
	BranchID   string          `json:"branch_id"`
	CustomerID string          `json:"customer_id"`
	SaleType   string          `json:"sale_type"`
	Notes      string          `json:"notes"`
	Status     string          `json:"status"`
	Lines      json.RawMessage `json:"lines"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// scanParkedRow reads the 11-target row and projects it.
func scanParkedRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r parkedRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.TicketNo, &r.BranchID, &r.CustomerID, &r.SaleType,
		&r.Notes, &r.Status, &r.Lines, &r.CreatedAt, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("pos: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("pos: sync projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// parkedPredicate is the org predicate through the module's own branch
// read.
func parkedPredicate(f sync.ScopeFilter) ([]any, bool) {
	if f.Tenant || f.OrgID == "" {
		return nil, false
	}
	return []any{f.OrgID}, true
}

// parkedFrom is the shared FROM clause: the ticket joined to its branch
// for the org fence.
const parkedFrom = ` FROM pos_parked_tickets p
	JOIN locations l ON l.id = p.branch_id `

// NewParkedTicketsStore builds the parked_tickets Store.
func NewParkedTicketsStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + parkedColumns + parkedFrom + `
		 WHERE l.org_id = $1::uuid AND p.seq IS NOT NULL AND p.seq > $2
		 ORDER BY p.seq ASC LIMIT $3`,
		ByID: `SELECT ` + parkedColumns + parkedFrom + `
		 WHERE l.org_id = $1::uuid AND p.id = $2::uuid`,
		Args: parkedPredicate,
		Scan: scanParkedRow,
	})
}
