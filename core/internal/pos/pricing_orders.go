package pos

// pricing_orders.go, the W7 "pricing order": the client's official printable price/compare document.
// NOT a quote (no acceptance lifecycle) and NOT an order (no stock effect, no AR), it snapshots the
// engine price AND the list anchor per line so the printed doc carries the comparison, plus HST via the
// TaxAssessor seam, with a validity window on the face. Prices stay indicative until rung.

import (
	"context"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// pricingDocValidityDays is the default valid-until window printed on the face (today+14d).
const pricingDocValidityDays = 14

// CreatePricingOrder prices every line through the SAME engine seam as the Sale (the cashier never
// types a price), snapshots SKU/name + list-vs-offered, assesses HST (standard reason only, relief
// runs at ring, not on an indicative doc), and persists the printable document.
func (s *Service) CreatePricingOrder(ctx context.Context, in PricingOrderInput) (PricingOrder, error) {
	if in.BranchID == "" {
		return PricingOrder{}, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	if len(in.Lines) == 0 {
		return PricingOrder{}, errNoLines
	}
	for _, ln := range in.Lines {
		if ln.ProductID == "" || ln.QtyEntered <= 0 {
			return PricingOrder{}, clientErr(http.StatusUnprocessableEntity, "pos: every pricing line needs a product_id and a positive qty_entered")
		}
	}

	// Customer type drives the engine tier; a walk-in/prospect prices RETAIL.
	customerType := "RETAIL"
	if in.CustomerID != "" {
		cust, err := s.store.ReadCustomer(ctx, in.CustomerID)
		if err != nil {
			return PricingOrder{}, err
		}
		customerType = cust.CustomerType
	}

	lines := make([]PricingOrderLine, len(in.Lines))
	var subtotal int64
	for i, ln := range in.Lines {
		lp, err := s.pricing.DeriveLinePrice(ctx, pricing.ResolveInput{
			CustomerID:   in.CustomerID,
			CustomerType: customerType,
			ProductID:    ln.ProductID,
			BranchID:     in.BranchID,
			QtyEntered:   ln.QtyEntered,
			UOMEntered:   ln.UOMEntered,
		})
		if err != nil {
			return PricingOrder{}, err
		}
		if lp.Breakdown.Blocked {
			return PricingOrder{}, ErrPriceBlocked{Policy: lp.Breakdown.BlockPolicy, LineNo: i + 1}
		}
		sku, name, err := s.store.ReadProductSnapshot(ctx, ln.ProductID)
		if err != nil {
			return PricingOrder{}, err
		}
		// The list comparison is the retail anchor from the breakdown; when the engine carries no
		// anchor the offered price stands in (savings render as zero, never negative-by-accident).
		list := lp.Breakdown.OriginalCents
		if list == 0 {
			list = lp.UnitPriceCents
		}
		lines[i] = PricingOrderLine{
			LineNo: i + 1, ProductID: ln.ProductID, SKU: sku, Name: name,
			QtyEntered: ln.QtyEntered, UOMEntered: ln.UOMEntered,
			UnitPriceCents: lp.UnitPriceCents, ListPriceCents: list, LineTotalCents: lp.LineTotalCents,
		}
		subtotal += lp.LineTotalCents
	}

	quote, err := s.taxAssessor().AssessSale(ctx, TaxQuoteInput{BranchID: in.BranchID, SubtotalCents: subtotal})
	if err != nil {
		return PricingOrder{}, err
	}

	validUntil := in.ValidUntil
	if validUntil == "" {
		validUntil = s.now().AddDate(0, 0, pricingDocValidityDays).Format("2006-01-02")
	}
	return s.store.InsertPricingOrder(ctx, PricingOrder{
		BranchID: in.BranchID, CustomerID: in.CustomerID, CustomerName: in.CustomerName, Notes: in.Notes,
		ValidUntil: validUntil, SubtotalCents: subtotal, TaxCents: quote.TaxCents,
		TotalCents: subtotal + quote.TaxCents, Lines: lines,
	}, actorFrom(ctx))
}

// GetPricingOrder returns one committed doc with its lines, the print-view read.
func (s *Service) GetPricingOrder(ctx context.Context, id string) (PricingOrder, error) {
	if id == "" {
		return PricingOrder{}, errBadRequest
	}
	po, found, err := s.store.GetPricingOrder(ctx, id)
	if err != nil {
		return PricingOrder{}, err
	}
	if !found {
		return PricingOrder{}, errPricingNotFound
	}
	return po, nil
}

// ListPricingOrders returns the recent docs for a branch (newest first, default 50).
func (s *Service) ListPricingOrders(ctx context.Context, branchID string) ([]PricingOrder, error) {
	if branchID == "" {
		return nil, clientErr(http.StatusBadRequest, "pos: branch_id is required")
	}
	return s.store.ListPricingOrders(ctx, branchID, 50)
}
