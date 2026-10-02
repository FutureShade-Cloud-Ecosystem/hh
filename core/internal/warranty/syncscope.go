// syncscope.go is the warranty module's pull-only Stores (HH-3.5
// #169): warranty_claims (the claim rows the counter caches for capture
// context and lookup) and warranty_board (the same rows projected as
// the recovery board the manager reads: the phase funnel legs, the
// owner, the expected and realized recovery). Both POSTs answer the
// contract's refusal at the route (Colton's START ruling); the
// readcache skeleton's write primitives fail closed. The org fence
// rides the branch (warranty_claims.branch_id through locations), the
// same fence the module's own board reads use; the board scope is a
// second projection over one table, so both scopes read the table's
// one seq feed and stay monotonic together.
package warranty

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync/readcache"
)

// claimsColumns is the claims projection, c-qualified for the branch
// join.
const claimsColumns = `c.id::text, c.claim_no::text, c.branch_id::text, COALESCE(c.customer_id::text,''),
	COALESCE(c.vendor_id::text,''), COALESCE(c.owner_id::text,''), c.phase, c.source,
	c.expected_cents, c.realized_cents, c.opened_at, c.closed_at, c.created_at, c.updated_at,
	c.seq, (c.deleted_at IS NOT NULL) AS deleted`

// claimRow is the wire shape of one cached claim row.
type claimRow struct {
	ID            string     `json:"id"`
	ClaimNo       string     `json:"claim_no"`
	BranchID      string     `json:"branch_id"`
	CustomerID    string     `json:"customer_id"`
	VendorID      string     `json:"vendor_id"`
	OwnerID       string     `json:"owner_id"`
	Phase         string     `json:"phase"`
	Source        string     `json:"source"`
	ExpectedCents int64      `json:"expected_cents"`
	RealizedCents int64      `json:"realized_cents"`
	OpenedAt      time.Time  `json:"opened_at"`
	ClosedAt      *time.Time `json:"closed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// scanClaimRow reads the 16-target row and projects it.
func scanClaimRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r claimRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.ClaimNo, &r.BranchID, &r.CustomerID, &r.VendorID, &r.OwnerID,
		&r.Phase, &r.Source, &r.ExpectedCents, &r.RealizedCents, &r.OpenedAt, &r.ClosedAt,
		&r.CreatedAt, &r.UpdatedAt, &seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("warranty: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("warranty: sync projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// boardColumns is the board projection: the recovery funnel legs.
const boardColumns = `c.id::text, c.claim_no::text, c.branch_id::text, c.phase,
	COALESCE(c.owner_id::text,''), c.vendor_id::text, c.expected_cents, c.realized_cents,
	c.opened_at, c.closed_at, c.sla_due,
	c.seq, (c.deleted_at IS NOT NULL) AS deleted`

// boardRow is the wire shape of one cached board row.
type boardRow struct {
	ID            string     `json:"id"`
	ClaimNo       string     `json:"claim_no"`
	BranchID      string     `json:"branch_id"`
	Phase         string     `json:"phase"`
	OwnerID       string     `json:"owner_id"`
	VendorID      *string    `json:"vendor_id"`
	ExpectedCents int64      `json:"expected_cents"`
	RealizedCents int64      `json:"realized_cents"`
	OpenedAt      time.Time  `json:"opened_at"`
	ClosedAt      *time.Time `json:"closed_at"`
	SLADue        *time.Time `json:"sla_due"`
}

// scanBoardRow reads the 13-target row and projects it.
func scanBoardRow(row readcache.Scanner) (string, int64, bool, map[string]any, error) {
	var r boardRow
	var seq int64
	var deleted bool
	if err := row.Scan(&r.ID, &r.ClaimNo, &r.BranchID, &r.Phase, &r.OwnerID, &r.VendorID,
		&r.ExpectedCents, &r.RealizedCents, &r.OpenedAt, &r.ClosedAt, &r.SLADue,
		&seq, &deleted); err != nil {
		return "", 0, false, nil, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("warranty: board projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, false, nil, fmt.Errorf("warranty: board projection decode: %w", err)
	}
	return r.ID, seq, deleted, data, nil
}

// warrantyPredicate is the org predicate through the branch join.
func warrantyPredicate(f sync.ScopeFilter) ([]any, bool) {
	if f.Tenant || f.OrgID == "" {
		return nil, false
	}
	return []any{f.OrgID}, true
}

// NewWarrantyClaimsStore builds the warranty_claims Store.
func NewWarrantyClaimsStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + claimsColumns + ` FROM warranty_claims c
		 JOIN locations l ON l.id = c.branch_id
		 WHERE l.org_id = $1::uuid AND c.seq IS NOT NULL AND c.seq > $2
		 ORDER BY c.seq ASC LIMIT $3`,
		ByID: `SELECT ` + claimsColumns + ` FROM warranty_claims c
		 JOIN locations l ON l.id = c.branch_id
		 WHERE l.org_id = $1::uuid AND c.id = $2::uuid`,
		Args: warrantyPredicate,
		Scan: scanClaimRow,
	})
}

// NewWarrantyBoardStore builds the warranty_board Store, the recovery
// projection over the same rows and the same seq feed.
func NewWarrantyBoardStore(pool *pgxpool.Pool) *readcache.Store {
	return readcache.New(pool, readcache.Query{
		Page: `SELECT ` + boardColumns + ` FROM warranty_claims c
		 JOIN locations l ON l.id = c.branch_id
		 WHERE l.org_id = $1::uuid AND c.seq IS NOT NULL AND c.seq > $2
		 ORDER BY c.seq ASC LIMIT $3`,
		ByID: `SELECT ` + boardColumns + ` FROM warranty_claims c
		 JOIN locations l ON l.id = c.branch_id
		 WHERE l.org_id = $1::uuid AND c.id = $2::uuid`,
		Args: warrantyPredicate,
		Scan: scanBoardRow,
	})
}
