package inventory

// dataset_read.go, the report writer's stock data set (W3-RW1): on hand with value, read through this
// module's own valuation for the reporting module's curated data sets.
//
// -- THE VALUE IS THE VALUATION'S VALUE ---------------------------------------------------------------
// The read is StockValueCents, grouped: every stock_balances row valued at its branch's weighted
// average cost, each row rounded half away from zero in SQL exactly the way the valuation rounds, then
// summed to product by branch. Because the rounding sits on each row and not after the sum, the set's
// total is StockValueCents to the cent for any reach, and the reporting module's named integration test
// pins exactly that. Go never multiplies a quantity by a cost on this path (FW1-09d round 2).
//
// -- THE FENCE IS THE CALLER'S OWN REACH --------------------------------------------------------------
// The read answers the stamped caller's reach, this module's own shape (stock_truth_handler's reach
// read, lifted to a context argument for a service caller): a GLOBAL caller reads every branch, a
// branch scoped caller reads its granted branches, a scoped caller with no grants reads nothing, fail
// closed. The reach comes from the caller the guard stamped, so a consumer of this read cannot forget
// to pass it.

import (
	"context"
	"errors"
	"fmt"
	"sort"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// ErrDatasetReadNotConfigured marks a Service built without a repository (a unit-test fake store), the
// one state this read cannot answer in. Named so a consumer can say so.
var ErrDatasetReadNotConfigured = errors.New("inventory: dataset read not configured (no repository)")

// DatasetStockLine is one product's on hand at one branch, valued the way the valuation values it.
type DatasetStockLine struct {
	ProductID     string
	BranchID      string
	OnHandBase    float64
	UnitCostCents int64
	ValueCents    int64
}

// DatasetStockOnHand reads the set for the stamped caller's reach, one row per product per branch.
func (s *Service) DatasetStockOnHand(ctx context.Context) ([]DatasetStockLine, error) {
	if s == nil || s.repo == nil {
		return nil, ErrDatasetReadNotConfigured
	}
	var reach any // nil binds SQL NULL, the every-branch arm
	c, ok := identity.StaffContextFromContext(ctx)
	if ok && c.Scope != identity.ScopeGlobal {
		ids := make([]string, 0, len(c.Branches))
		for id, granted := range c.Branches {
			if granted {
				ids = append(ids, id)
			}
		}
		// Sorted so the SQL parameter does not depend on Go's randomised map iteration order. Empty
		// binds as the empty array: = ANY('{}') matches nothing, the fail-closed arm.
		sort.Strings(ids)
		reach = ids
	}
	return s.repo.DatasetStockOnHand(ctx, reach)
}

// DatasetStockOnHand is the store read: on hand by product by branch, valued per balance row at the
// branch's weighted average cost, the same statement StockValueCents answers, grouped.
func (r *Repository) DatasetStockOnHand(ctx context.Context, reach any) ([]DatasetStockLine, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT sb.product_id::text, sb.branch_id::text,
		       SUM(sb.on_hand_base)::float8,
		       COALESCE(pbc.avg_unit_cost_cents, 0)::bigint,
		       COALESCE(SUM(ROUND(sb.on_hand_base * COALESCE(pbc.avg_unit_cost_cents, 0))), 0)::bigint
		  FROM stock_balances sb
		  LEFT JOIN product_branch_costs pbc
		         ON pbc.product_id = sb.product_id AND pbc.branch_id = sb.branch_id
		 WHERE sb.on_hand_base <> 0
		   AND ($1::uuid[] IS NULL OR sb.branch_id = ANY($1::uuid[]))
		 GROUP BY sb.product_id, sb.branch_id, COALESCE(pbc.avg_unit_cost_cents, 0)
		 ORDER BY sb.product_id, sb.branch_id`, reach)
	if err != nil {
		return nil, fmt.Errorf("inventory: dataset stock on hand: %w", err)
	}
	defer rows.Close()
	out := make([]DatasetStockLine, 0)
	for rows.Next() {
		var l DatasetStockLine
		if err := rows.Scan(&l.ProductID, &l.BranchID, &l.OnHandBase, &l.UnitCostCents, &l.ValueCents); err != nil {
			return nil, fmt.Errorf("inventory: scan dataset stock line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
