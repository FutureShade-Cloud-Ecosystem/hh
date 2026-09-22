package inventory

// availability.go, CP-04 (portal reorder): a READ-ONLY availability check other modules
// compose (the order/portal reorder preview). It answers "can qtyBase of this product be
// ordered at this branch right now?" off the product discontinued flag (products.is_active)
// plus the stock-balances read model. Plain read: no locking, no reservation, a DRAFT
// reorder does not allocate stock, so this is a courtesy flag, not an oversell guard.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Availability reason codes CheckAvailability returns (stable strings, the portal UI
// keys copy off them).
const (
	// ReasonDiscontinued, the product row is missing or is_active = false.
	ReasonDiscontinued = "DISCONTINUED"
	// ReasonOutOfStock, on-hand at the branch is below the requested base quantity.
	ReasonOutOfStock = "OUT_OF_STOCK"
)

// AvailabilityFacts is the single-read projection CheckAvailability grades:
// the catalog active flag + summed on-hand + summed HARD reservations (branch-scoped when
// branchID != "").
type AvailabilityFacts struct {
	Found bool // a products row exists
	// IsActive is products.is_active (false = discontinued).
	IsActive bool
	// OnHandBase is SUM(stock_balances.on_hand_base) for the product (at the branch).
	OnHandBase float64
	// ReservedBase is SUM(stock_balances.qty_reserved), the HARD holds only (DIB-60). This field is
	// what turns this from an on-hand check into an availability check; see AvailableBase.
	ReservedBase float64
}

// CheckAvailability reports whether qtyBase of a product can be ordered at a branch:
// ok=false carries a machine reason (ReasonDiscontinued / ReasonOutOfStock). branchID ""
// grades against the engagement-wide on-hand. A missing product reads as discontinued -
// a SKU that no longer exists in the catalog must never pass a reorder preview.
func (s *Service) CheckAvailability(ctx context.Context, productID, branchID string, qtyBase float64) (bool, string, error) {
	if productID == "" {
		return false, "", badReq("inventory: product_id is required")
	}
	if qtyBase <= 0 {
		return false, "", badReq("inventory: qty_base must be > 0")
	}
	facts, err := s.db().AvailabilityFacts(ctx, productID, branchID)
	if err != nil {
		return false, "", err
	}
	if !facts.Found || !facts.IsActive {
		return false, ReasonDiscontinued, nil
	}
	// DIB-60: grade against AVAILABLE, not on-hand. Stock already hard-held for a confirmed order or
	// an outbound transfer is not stock this customer can have, and telling them otherwise is the
	// oversell the reservation substrate exists to stop.
	// qtyEpsilon absorbs DECIMAL(19,4) float round-trip noise (see service.go).
	if AvailableBase(facts.OnHandBase, facts.ReservedBase)+qtyEpsilon < qtyBase {
		return false, ReasonOutOfStock, nil
	}
	return true, "", nil
}

// InStockProductIDs returns the product ids with positive on-hand (branch-scoped when
// branchID != ""). It backs the M4 catalogue in_stock_only filter (product.StockReader seam,
// A-4) so the product module reads stock through inventory rather than SELECTing
// stock_balances itself. A read-only courtesy list (no locking), it reads directly off the
// pgx repository (like the other pool-backed read models); a unit test of the product module
// injects a fake StockReader, not this.
func (s *Service) InStockProductIDs(ctx context.Context, branchID string) ([]string, error) {
	return s.repo.InStockProductIDs(ctx, branchID)
}

// AvailabilityFacts is the pgx read behind CheckAvailability: one row joining the
// catalog active flag with the summed stock_balances on-hand (empty $2 = every branch).
// Reading products.is_active here follows the existing precedent (InsertMove /
// ReceivedLinesForPO already resolve product facts in inventory SQL).
func (r *Repository) AvailabilityFacts(ctx context.Context, productID, branchID string) (AvailabilityFacts, error) {
	if r.pool == nil {
		return AvailabilityFacts{}, errNoDB
	}
	var f AvailabilityFacts
	err := r.pool.QueryRow(ctx, `
		SELECT p.is_active,
		       COALESCE(b.on_hand, 0),
		       COALESCE(b.reserved, 0)
		FROM products p
		LEFT JOIN LATERAL (
		    SELECT SUM(sb.on_hand_base) AS on_hand, SUM(sb.qty_reserved) AS reserved
		      FROM stock_balances sb
		     WHERE sb.product_id = p.id
		       AND ($2 = '' OR sb.branch_id = $2::uuid)
		) b ON TRUE
		WHERE p.id = $1`, productID, branchID).
		Scan(&f.IsActive, &f.OnHandBase, &f.ReservedBase)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AvailabilityFacts{Found: false}, nil
		}
		return AvailabilityFacts{}, err
	}
	f.Found = true
	return f, nil
}

// OnHandByBranch returns product id → (branch id → on-hand base) for the given product ids, backing
// the DIB-404 catalogue row detail (product.BranchStockReader seam). Same boundary as
// InStockProductIDs: the product module never SELECTs stock_balances, it asks inventory.
//
// BULK on purpose. The POS tiles read one product at a time through StockGlance, which is right for a
// grid of a dozen cards a cashier is looking at; the catalogue lists the entire filtered set, and a
// per-row round trip there is a query storm that grows with the catalogue.
//
// Read-only and non-locking, like the rest of this file. Branches with no ledger row for a product are
// ABSENT from the inner map rather than present as 0, the catalogue distinguishes "no stock record"
// from "zero on hand", and so must the read that feeds it.
func (s *Service) OnHandByBranch(ctx context.Context, productIDs []string) (map[string]map[string]float64, error) {
	return s.repo.OnHandByBranch(ctx, productIDs)
}
