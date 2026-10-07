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
	discount     *DiscountInput // the optional whole-ticket discount (discount.go)
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
	// The manual discounts (discount.go): what the ring stores, logs and reports, and what the
	// preview answers. Every line total above is already net of them.
	discounts    []appliedDiscount
	saleDiscount saleDiscountRow
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
	}

	// The counter's manual discounts, a manager's yes on top of the rule and tier price (no
	// floor, row 481): applied here so the preview and the ring read one answer. After this
	// every line total is net, and the subtotal is the sum of the nets.
	reqs := in.lines
	applied, saleRow, err := s.applyDiscounts(ctx, in.branchID, reqs, lines, in.discount)
	if err != nil {
		return saleMoney{}, err
	}
	for i := range lines {
		subtotal += lines[i].LineTotalCents
	}
	// A discount may not take the whole ticket: the ring's payment rail refuses a zero amount, so
	// the preview and the ring say so alike, in words, here where they share one path.
	if len(applied) > 0 && subtotal <= 0 {
		if in.discount == nil {
			return saleMoney{}, errDiscountZeroTotalLine
		}
		return saleMoney{}, errDiscountZeroTotal
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
		discounts:     applied,
		saleDiscount:  saleRow,
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
	// Discount is the optional whole-ticket discount (discount.go), the same body the ring takes.
	Discount *DiscountInput `json:"discount,omitempty"`
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
	// The manual discounts the ring would apply (discount.go): the total, the ticket discount's
	// own cents, and one row per line with its own and its share of the ticket discount, so the
	// counter shows the server's cents and never computes one.
	DiscountCents     int64             `json:"discount_cents,omitempty"`
	SaleDiscountCents int64             `json:"sale_discount_cents,omitempty"`
	Lines             []SalePreviewLine `json:"lines,omitempty"`
	// TicketBaseCents is the subtotal after the line discounts, before any ticket discount: what
	// a ticket discount is struck from and approved against. TicketDiscountMaxCents is the most a
	// ticket discount may take off (the base less one cent), so the till never works either out.
	TicketBaseCents        int64 `json:"ticket_base_cents,omitempty"`
	TicketDiscountMaxCents int64 `json:"ticket_discount_max_cents,omitempty"`
}

// SalePreviewLine is one ticket line's priced answer: the net the line charges and what came off.
type SalePreviewLine struct {
	LineNo         int   `json:"line_no"`
	LineTotalCents int64 `json:"line_total_cents"` // net of every discount
	// BeforeTicketDiscountCents is the line's price less its own discount, before its share of
	// the ticket discount: the figure the receipt prints for the line.
	BeforeTicketDiscountCents int64 `json:"before_ticket_discount_cents"`
	DiscountCents             int64 `json:"discount_cents,omitempty"`
	SaleDiscountCents         int64 `json:"sale_discount_cents,omitempty"`
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
		discount:     in.Discount,
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
	if priced.subtotal > 0 {
		out.TicketBaseCents = priced.subtotal + priced.saleDiscount.AmountCents
		out.TicketDiscountMaxCents = out.TicketBaseCents - 1
	}
	if total, _ := reportDiscounts(priced.discounts); total > 0 {
		out.DiscountCents = total
		out.SaleDiscountCents = priced.saleDiscount.AmountCents
		out.Lines = make([]SalePreviewLine, len(priced.lines))
		for i, l := range priced.lines {
			out.Lines[i] = SalePreviewLine{
				LineNo: l.LineNo, LineTotalCents: l.LineTotalCents,
				BeforeTicketDiscountCents: l.LineTotalCents + l.SaleDiscountCents,
				DiscountCents:             l.DiscountCents, SaleDiscountCents: l.SaleDiscountCents,
			}
		}
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
