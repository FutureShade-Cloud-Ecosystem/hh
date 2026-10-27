package inventory

// visibility.go, DIB-73: cross-branch stock visibility.
//
// FOUR BUCKETS, PER BRANCH, IN ONE READ:
//
//	on_hand    what is physically in the yard
//	reserved   what is hard-held (DIB-60), spoken for, still in the yard
//	in_transit what is on a truck INBOUND to this branch (DIB-71), not in the yard, but coming
//	available  on_hand - reserved , what can be promised to the next customer
//
// available is the one a counter clerk acts on, and it is the number that did not exist before this
// wave: every surface showed on-hand and called it availability.
//
// IN-TRANSIT IS INBOUND-ONLY, and that is a deliberate asymmetry. Stock shipped OUT of this branch
// has already left its on-hand, showing it again as an outbound in-transit bucket would invite
// someone to subtract it twice. Stock coming IN has not yet been added anywhere, so a branch that
// is short today but has a truck arriving tomorrow needs to see it or it will re-order.
//
// THE AT-QUOTE READ (the consumer-declared seam). The quote surface lives outside this bundle's
// fence, so the read it needs is exposed here as a small, primitive-only interface that a main.go
// adapter satisfies without either module importing the other:
//
//	type QuoteAvailabilityReader interface {
//	    AvailabilityAtBranch(ctx context.Context, productID, branchID string) (BranchStock, error)
//	    AvailabilityPeek(ctx context.Context, orgID, productID string) ([]BranchPeek, error)
//	}
//
// *inventory.Service satisfies it as written. The exact signature is restated in REPORT-W4-B.md for
// the union, because the CONSUMER (quote) is the side that has to declare it.

import (
	"context"
	"sort"

	"github.com/google/uuid"
)

// BranchStock is the four-bucket view of one product at one branch.
type BranchStock struct {
	ProductID  string `json:"product_id"`
	BranchID   string `json:"branch_id"`
	BranchCode string `json:"branch_code"`
	BranchName string `json:"branch_name"`
	// OnHandBase is the physical balance at the branch.
	OnHandBase float64 `json:"on_hand_base"`
	// ReservedBase is the HARD holds only (SOFT holds are reported separately and do not gate).
	ReservedBase float64 `json:"reserved_base"`
	// InTransitBase is units shipped to this branch and not yet received.
	InTransitBase float64 `json:"in_transit_base"`
	// AvailableBase = OnHandBase - ReservedBase. May be negative; see AvailableBase().
	AvailableBase float64 `json:"available_base"`
	// SoftHeldBase is the advisory (quote) holds. Shown so a clerk can see interest without it
	// gating a sale, it is NOT subtracted from AvailableBase.
	SoftHeldBase float64 `json:"soft_held_base"`
	AvgCostCents int64   `json:"avg_cost_cents,omitempty"`
}

// QuoteAvailabilityReader is the seam a quote surface consumes. Declared HERE as documentation of
// the shape; the consuming module declares its own identical interface (the codebase's zero-trust
// convention, distinct named types, same shape, a main.go adapter between them).
type QuoteAvailabilityReader interface {
	AvailabilityAtBranch(ctx context.Context, productID, branchID string) (BranchStock, error)
	AvailabilityPeek(ctx context.Context, orgID, productID string) ([]BranchPeek, error)
}

// compile-time proof the service satisfies the seam it publishes.
var _ QuoteAvailabilityReader = (*Service)(nil)

// BranchPeek is the cost-free four-bucket view of one product at one branch, the cross-branch peek
// behind a THIN or NONE chip. It carries quantities only: the average cost stays on the caller's own
// stock screens (UX-88, the principal's ruling at row 2809).
type BranchPeek struct {
	ProductID     string  `json:"product_id"`
	BranchID      string  `json:"branch_id"`
	BranchCode    string  `json:"branch_code"`
	BranchName    string  `json:"branch_name"`
	OnHandBase    float64 `json:"on_hand_base"`
	ReservedBase  float64 `json:"reserved_base"`
	InTransitBase float64 `json:"in_transit_base"`
	AvailableBase float64 `json:"available_base"`
	SoftHeldBase  float64 `json:"soft_held_base"`
}

// AvailabilityAllBranches is the cross-branch view for one product: one row per BRANCH of the caller's
// dealer org, every such branch present even when it has no stock record. orgID is the org the
// middleware resolved for the request; a branch of another org, or of no org, is in no caller's org
// and never appears. An empty orgID is refused, never read as "every org".
//
// Every branch of the org, always, is the point. A row missing because the branch has no
// stock_balances line is indistinguishable on screen from a branch that was not queried, and
// "Kingston isn't listed" reads as a bug rather than as "Kingston has none", which is exactly the
// answer the person asking needs.
func (s *Service) AvailabilityAllBranches(ctx context.Context, orgID, productID string) ([]BranchStock, error) {
	if productID == "" {
		return nil, badReq("inventory: product_id is required")
	}
	if orgID == "" {
		return nil, badReq("inventory: the caller's org is required")
	}
	rows, err := s.repo.BranchStock(ctx, productID, "", orgID)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].BranchCode < rows[j].BranchCode })
	return rows, nil
}

// AvailabilityPeek is AvailabilityAllBranches without the cost leg: the same org-held rows, quantities
// only, read by a query that never touches product_branch_costs.
func (s *Service) AvailabilityPeek(ctx context.Context, orgID, productID string) ([]BranchPeek, error) {
	if productID == "" {
		return nil, badReq("inventory: product_id is required")
	}
	if orgID == "" {
		return nil, badReq("inventory: the caller's org is required")
	}
	rows, err := s.repo.BranchPeek(ctx, productID, orgID)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].BranchCode < rows[j].BranchCode })
	return rows, nil
}

// AvailabilityAtBranchInOrg is the single-branch form held to the caller's org: the branch must be a
// branch of orgID, and a branch of another org, of no org, or that does not exist is not found, so the
// answer cannot tell the three apart. An empty org is refused, never read as every org. It is the read
// behind the HTTP stock truth door; the at-quote seam below reads a branch the quote already names.
func (s *Service) AvailabilityAtBranchInOrg(ctx context.Context, orgID, productID, branchID string) (BranchStock, error) {
	if productID == "" {
		return BranchStock{}, badReq("inventory: product_id is required")
	}
	if branchID == "" {
		return BranchStock{}, badReq("inventory: branch_id is required")
	}
	if orgID == "" {
		return BranchStock{}, badReq("inventory: the caller's org is required")
	}
	if _, err := uuid.Parse(branchID); err != nil {
		return BranchStock{}, errNotFound
	}
	rows, err := s.repo.BranchStock(ctx, productID, branchID, orgID)
	if err != nil {
		return BranchStock{}, err
	}
	if len(rows) == 0 {
		return BranchStock{}, errNotFound
	}
	return rows[0], nil
}

// BranchInOrg reports whether branchID is a branch of orgID. An empty org is refused, never read as
// every org.
func (s *Service) BranchInOrg(ctx context.Context, orgID, branchID string) (bool, error) {
	if orgID == "" {
		return false, badReq("inventory: the caller's org is required")
	}
	return s.repo.BranchInOrg(ctx, orgID, branchID)
}

// BranchCostInOrg is CurrentCostCents for a named branch, held to the caller's org the same way: a
// branch outside the org is not found and no cost is read for it.
func (s *Service) BranchCostInOrg(ctx context.Context, orgID, productID, branchID string) (int64, bool, error) {
	in, err := s.BranchInOrg(ctx, orgID, branchID)
	if err != nil {
		return 0, false, err
	}
	if !in {
		return 0, false, errNotFound
	}
	return s.db().BranchCostCents(ctx, productID, branchID)
}

// AvailabilityAtBranch is the single-branch form, the at-quote read. A branch with no stock record
// returns a zeroed row rather than not-found: "none at Kingston" is a real, useful answer and a 404
// would force every caller to translate it back into one.
func (s *Service) AvailabilityAtBranch(ctx context.Context, productID, branchID string) (BranchStock, error) {
	if productID == "" {
		return BranchStock{}, badReq("inventory: product_id is required")
	}
	if branchID == "" {
		return BranchStock{}, badReq("inventory: branch_id is required")
	}
	rows, err := s.repo.BranchStock(ctx, productID, branchID, "")
	if err != nil {
		return BranchStock{}, err
	}
	if len(rows) == 0 {
		return BranchStock{ProductID: productID, BranchID: branchID}, nil
	}
	return rows[0], nil
}

// branchQuantitySQL is the quantity half of the four-bucket read, shared by the costed and the cost-free
// query: it drives off LOCATIONS (every BRANCH row of the org), left-joining the three quantity sources,
// so a branch with no stock, no holds and no inbound transfer still produces a row of zeros. The three
// sources are aggregated in separate lateral subqueries rather than joined directly: joining
// stock_balances to stock_transfer_lines would multiply the balance rows by the transfer rows and
// silently inflate on_hand for any product with more than one open transfer, the classic fan-out
// that produces a number nobody can reproduce by hand.
const branchQuantityJoins = `
		  LEFT JOIN LATERAL (
		      SELECT SUM(sb.on_hand_base) AS on_hand, SUM(sb.qty_reserved) AS reserved
		        FROM stock_balances sb
		       WHERE sb.product_id = $1::uuid AND sb.branch_id = l.id
		  ) bal ON TRUE
		  LEFT JOIN LATERAL (
		      SELECT SUM(tl.qty_shipped_base - tl.qty_received_base) AS in_transit
		        FROM stock_transfer_lines tl
		        JOIN stock_transfers t ON t.id = tl.transfer_id
		       WHERE tl.product_id = $1::uuid
		         AND t.to_branch_id = l.id
		         AND t.status = 'IN_TRANSIT'
		         AND tl.qty_shipped_base > tl.qty_received_base
		  ) tr ON TRUE
		  LEFT JOIN LATERAL (
		      SELECT SUM(sr.qty_base) AS qty
		        FROM stock_reservations sr
		       WHERE sr.product_id = $1::uuid AND sr.branch_id = l.id
		         AND sr.status = 'ACTIVE' AND sr.hold_mode = 'SOFT'
		  ) soft ON TRUE`

const branchStockSQL = `
		SELECT $1::text AS product_id,
		       l.id::text, l.code::text, l.name,
		       COALESCE(bal.on_hand, 0),
		       COALESCE(bal.reserved, 0),
		       COALESCE(tr.in_transit, 0),
		       COALESCE(soft.qty, 0),
		       COALESCE(pbc.avg_unit_cost_cents, 0)
		  FROM locations l` + branchQuantityJoins + `
		  LEFT JOIN product_branch_costs pbc
		         ON pbc.product_id = $1::uuid AND pbc.branch_id = l.id
		 WHERE l.loc_type = 'BRANCH'
		   AND ($2 = '' OR l.id = $2::uuid)
		   AND ($3 = '' OR l.org_id = $3::uuid)
		 ORDER BY l.code`

// branchPeekSQL has no cost column and no join to product_branch_costs. A branch with no org is in no
// org, so the org predicate is not optional here: the service refuses an empty org before this runs.
const branchPeekSQL = `
		SELECT $1::text AS product_id,
		       l.id::text, l.code::text, l.name,
		       COALESCE(bal.on_hand, 0),
		       COALESCE(bal.reserved, 0),
		       COALESCE(tr.in_transit, 0),
		       COALESCE(soft.qty, 0)
		  FROM locations l` + branchQuantityJoins + `
		 WHERE l.loc_type = 'BRANCH'
		   AND l.org_id = $2::uuid
		 ORDER BY l.code`

// BranchStock is the pgx read behind the four-bucket view. orgID holds the read to one dealer org
// ("" skips the org predicate, and only the single-branch read passes it: the branch there is one the
// caller's grants already reach).
func (r *Repository) BranchStock(ctx context.Context, productID, branchID, orgID string) ([]BranchStock, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, branchStockSQL, productID, branchID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BranchStock
	for rows.Next() {
		var b BranchStock
		if err := rows.Scan(&b.ProductID, &b.BranchID, &b.BranchCode, &b.BranchName,
			&b.OnHandBase, &b.ReservedBase, &b.InTransitBase, &b.SoftHeldBase, &b.AvgCostCents); err != nil {
			return nil, err
		}
		// Computed in Go, from the ONE definition, rather than as `on_hand - reserved` in the SELECT
		// list. If the rule ever changes, it changes in AvailableBase and every surface follows;
		// a second copy in SQL is a second thing to forget.
		b.AvailableBase = AvailableBase(b.OnHandBase, b.ReservedBase)
		out = append(out, b)
	}
	return out, rows.Err()
}

// BranchPeek is the cost-free read behind the cross-branch peek, held to the org.
func (r *Repository) BranchPeek(ctx context.Context, productID, orgID string) ([]BranchPeek, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, branchPeekSQL, productID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BranchPeek
	for rows.Next() {
		var b BranchPeek
		if err := rows.Scan(&b.ProductID, &b.BranchID, &b.BranchCode, &b.BranchName,
			&b.OnHandBase, &b.ReservedBase, &b.InTransitBase, &b.SoftHeldBase); err != nil {
			return nil, err
		}
		b.AvailableBase = AvailableBase(b.OnHandBase, b.ReservedBase)
		out = append(out, b)
	}
	return out, rows.Err()
}

// BranchInOrg reports whether branchID names a branch whose locations row carries orgID. A branch of no
// org is in no org, an empty org matches nothing, and a value that is not a uuid names no branch.
func (r *Repository) BranchInOrg(ctx context.Context, orgID, branchID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	if _, err := uuid.Parse(branchID); err != nil {
		return false, nil
	}
	var in bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM locations
		                WHERE id = $1::uuid AND loc_type = 'BRANCH' AND org_id = NULLIF($2, '')::uuid)`,
		branchID, orgID).Scan(&in)
	return in, err
}

// OrgBranchIDs returns the branch ids whose locations row carries orgID. An empty org matches
// nothing.
func (r *Repository) OrgBranchIDs(ctx context.Context, orgID string) ([]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if _, err := uuid.Parse(orgID); err != nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text FROM locations
		 WHERE loc_type = 'BRANCH' AND org_id = $1::uuid
		 ORDER BY id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
