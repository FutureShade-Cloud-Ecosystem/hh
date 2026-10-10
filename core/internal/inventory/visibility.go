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
//	    AvailabilityAllBranches(ctx context.Context, productID string) ([]BranchStock, error)
//	}
//
// *inventory.Service satisfies it as written. The exact signature is restated in REPORT-W4-B.md for
// the union, because the CONSUMER (quote) is the side that has to declare it.

import (
	"context"
	"sort"
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
	AvailabilityAllBranches(ctx context.Context, productID string) ([]BranchStock, error)
}

// compile-time proof the service satisfies the seam it publishes.
var _ QuoteAvailabilityReader = (*Service)(nil)

// AvailabilityAllBranches is the cross-branch view for one product: one row per BRANCH, every branch
// present even when it has no stock record.
//
// Every branch, always, is the point. A row missing because the branch has no stock_balances line is
// indistinguishable on screen from a branch that was not queried, and "Kingston isn't listed" reads
// as a bug rather than as "Kingston has none", which is exactly the answer the person asking needs.
func (s *Service) AvailabilityAllBranches(ctx context.Context, productID string) ([]BranchStock, error) {
	if productID == "" {
		return nil, badReq("inventory: product_id is required")
	}
	rows, err := s.repo.BranchStock(ctx, productID, "")
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].BranchCode < rows[j].BranchCode })
	return rows, nil
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
	rows, err := s.repo.BranchStock(ctx, productID, branchID)
	if err != nil {
		return BranchStock{}, err
	}
	if len(rows) == 0 {
		return BranchStock{ProductID: productID, BranchID: branchID}, nil
	}
	return rows[0], nil
}

// BranchStock is the pgx read behind the four-bucket view.
//
// It drives off LOCATIONS (every BRANCH row), left-joining the three quantity sources, so a branch
// with no stock, no holds and no inbound transfer still produces a row of zeros. The three sources
// are aggregated in separate lateral subqueries rather than joined directly: joining
// stock_balances to stock_transfer_lines would multiply the balance rows by the transfer rows and
// silently inflate on_hand for any product with more than one open transfer, the classic fan-out
// that produces a number nobody can reproduce by hand.
func (r *Repository) BranchStock(ctx context.Context, productID, branchID string) ([]BranchStock, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT $1::text AS product_id,
		       l.id::text, l.code::text, l.name,
		       COALESCE(bal.on_hand, 0),
		       COALESCE(bal.reserved, 0),
		       COALESCE(tr.in_transit, 0),
		       COALESCE(soft.qty, 0),
		       COALESCE(pbc.avg_unit_cost_cents, 0)
		  FROM locations l
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
		  ) soft ON TRUE
		  LEFT JOIN product_branch_costs pbc
		         ON pbc.product_id = $1::uuid AND pbc.branch_id = l.id
		 WHERE l.loc_type = 'BRANCH'
		   AND ($2 = '' OR l.id = $2::uuid)
		 ORDER BY l.code`, productID, branchID)
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
