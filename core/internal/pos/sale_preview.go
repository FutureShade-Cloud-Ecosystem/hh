package pos

// sale_preview.go, the read-only sale preview (the row 985 item): the
// counter could not see its own total before the ring, because no read
// prices a ticket WITH its tax (counter-prices carries no tax or total).
// The preview is the sale's own body without any tender, answered through
// the ring's own pricing and tax path. priceAndAssess below is that path,
// extracted from Sale verbatim: the ring calls it at its steps 3 and 4, the
// preview calls it and nothing else, so the two can never drift apart (a
// copy would answer numbers the ring does not bind, the exact defect this
// read exists to close).
//
// The preview writes NOTHING: no order, no hold, no number used, no replay
// key. It is staff only, behind the same role walls the sale takes: the
// RequireStaff door, and the branch wall the sale asserts for every tender.

import (
	"context"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// saleMoneyInput is the pricing-and-tax half of a sale body: the fields the
// ring's own steps 3 and 4 read.
type saleMoneyInput struct {
	customerID   string
	customerType string
	branchID     string
	lines        []LineInput
	taxReason    string
}

// saleMoney is the ring's resolution of a body's money: the priced lines,
// the subtotal, the assessed tax, the binding rate, the FN point-of-sale
// credit, and the total.
type saleMoney struct {
	lines         []orderLineRow
	subtotal      int64
	tax           int64
	total         int64
	rateBps       int
	onCreditCents int64
}

// priceAndAssess is the ring's own pricing and tax path, steps 3 and 4 of
// Sale, extracted verbatim so the read-only preview shares the exact code
// the ring binds with. A line that cannot be priced at all (no retail
// anchor, no uom conversion) stays the client-correctable 4xx the cashier
// can act on, never a plausible zero.
func (s *Service) priceAndAssess(ctx context.Context, in saleMoneyInput) (saleMoney, error) {
	// Price every line through the counter resolve (the cashier never types a price, and
	// the price the customer's rules and tiers give is accepted: no margin floor at the
	// till, D48 row 481, D49).
	lines := make([]orderLineRow, len(in.lines))
	var subtotal int64
	for i, li := range in.lines {
		lp, err := s.pricing.DeriveCounterLinePrice(ctx, pricing.ResolveInput{
			CustomerID:   in.customerID,
			CustomerType: in.customerType,
			ProductID:    li.ProductID,
			BranchID:     in.branchID,
			QtyEntered:   li.QtyEntered,
			UOMEntered:   li.UOMEntered,
		})
		if err != nil {
			// A pricing FAILURE (e.g. a walk-in RETAIL line with no retail anchor or no uom conversion
			// factor) is a client-correctable 4xx, not a server fault. Surface it with the SAME status the
			// /pricing endpoints return, scoped to the offending line, so the cashier sees a real message.
			// Before this, the raw pricing sentinel fell through the handler's writeErr default to a bare
			// HTTP 500 "internal error", and because a walk-in CASH ring resolves RETAIL pricing, a
			// retail-pricing gap 500'd the whole sale (DIB-197). Every tender hits this same line-pricing
			// step, so the mapping fixes them all without touching a successfully-priced sale.
			if st, msg, ok := pricing.ClientStatus(err); ok {
				return saleMoney{}, clientErr(st, "line %d: %s", i+1, msg)
			}
			return saleMoney{}, err
		}
		lines[i] = orderLineRow{
			LineNo:         i + 1,
			ProductID:      li.ProductID,
			QtyEntered:     li.QtyEntered,
			UOMEntered:     li.UOMEntered,
			QtyBase:        lp.QtyBase,
			UnitPriceCents: lp.UnitPriceCents,
			PriceUOM:       lp.PriceUOM,
			LineTotalCents: lp.LineTotalCents,
		}
		subtotal += lp.LineTotalCents
	}

	// Totals, assessed through the TaxAssessor seam (M5 owns the real engine). The default
	// FlatHSTAssessor returns flat 13% (D-A19-9), so at scaffold the binding tax is
	// bit-identical to the old flatHST13. Relief sets the reason code + snapshot, but the
	// BINDING collected tax stays the assessed value at gate.
	quote, err := s.taxAssessor().AssessSale(ctx, TaxQuoteInput{BranchID: in.branchID, SubtotalCents: subtotal, ReasonCode: in.taxReason})
	if err != nil {
		return saleMoney{}, err
	}
	tax := quote.TaxCents
	return saleMoney{
		lines:         lines,
		subtotal:      subtotal,
		tax:           tax,
		total:         subtotal + tax,
		rateBps:       quote.RateBps,
		onCreditCents: quote.OnCreditCents,
	}, nil
}

// SalePreviewInput is the preview body: the sale's own body (lines,
// customer, branch, sale type, fulfilment, relief) WITHOUT any tender, no
// replay key, no drawer. The server resolves every price exactly as the
// ring would for this body.
type SalePreviewInput struct {
	BranchID   string          `json:"branch_id"`
	CustomerID string          `json:"customer_id"` // "" resolves to the house WALK-IN, same as the sale
	SaleType   string          `json:"sale_type,omitempty"`
	Fulfilment string          `json:"fulfilment,omitempty"`
	Relief     *ReliefDecision `json:"relief,omitempty"`
	Lines      []LineInput     `json:"lines"`
}

// SalePreviewTaxLine is one tax line the ring would bind for the body: the
// reason, the binding rate, and the cents it collects.
type SalePreviewTaxLine struct {
	ReasonCode string `json:"reason_code"`
	RateBps    int    `json:"rate_bps"`
	TaxCents   int64  `json:"tax_cents"`
}

// SalePreview is the read-only answer: the server-resolved subtotal, each
// tax line, and the total in integer cents, exactly as the ring computes
// them for the same body.
type SalePreview struct {
	SubtotalCents int64                `json:"subtotal_cents"`
	TaxCents      int64                `json:"tax_cents"`
	TaxLines      []SalePreviewTaxLine `json:"tax_lines"`
	TotalCents    int64                `json:"total_cents"`
	TaxReasonCode string               `json:"tax_reason_code"`
}

// SalePreview prices a would-be ticket through the ring's own resolver and
// writes nothing. The validation order mirrors the sale's own pre-write
// half where the two share a wall: the no-lines refusal first, then the
// caller and its branch reach (the DIB-389 check the sale asserts for every
// tender), then the customer read, then the shared pricing and tax path.
// A ticket that collects no tax shows no tax line and its total is its
// subtotal.
func (s *Service) SalePreview(ctx context.Context, in SalePreviewInput) (SalePreview, error) {
	if len(in.Lines) == 0 {
		return SalePreview{}, errNoLines
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return SalePreview{}, err
	}
	if !caller.CanAccessBranch(in.BranchID) {
		return SalePreview{}, clientErr(http.StatusForbidden, "pos: caller cannot access branch %s", in.BranchID)
	}
	customerID := in.CustomerID
	if customerID == "" {
		walkIn, err := s.store.ResolveWalkIn(ctx)
		if err != nil {
			return SalePreview{}, err
		}
		customerID = walkIn
	}
	cust, err := s.store.ReadCustomer(ctx, customerID)
	if err != nil {
		return SalePreview{}, err
	}
	taxReason := TaxStdON13
	if in.Relief != nil && in.Relief.ReasonCode != "" {
		taxReason = in.Relief.ReasonCode
	}
	priced, err := s.priceAndAssess(ctx, saleMoneyInput{
		customerID:   customerID,
		customerType: cust.CustomerType,
		branchID:     in.BranchID,
		lines:        in.Lines,
		taxReason:    taxReason,
	})
	if err != nil {
		return SalePreview{}, err
	}
	out := SalePreview{
		SubtotalCents: priced.subtotal,
		TaxCents:      priced.tax,
		TaxLines:      []SalePreviewTaxLine{},
		TotalCents:    priced.total,
		TaxReasonCode: taxReason,
	}
	if priced.tax != 0 {
		out.TaxLines = append(out.TaxLines, SalePreviewTaxLine{
			ReasonCode: taxReason,
			RateBps:    priced.rateBps,
			TaxCents:   priced.tax,
		})
	}
	return out, nil
}
