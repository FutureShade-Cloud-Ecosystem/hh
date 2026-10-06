// compare.go, DIB-59 quote comparison / value engineering (bundle W10-A, stretch item).
//
// WHAT THIS IS. Given a line on a quote and a set of candidate alternate products, score each
// alternate through the SAME A-07 pricing waterfall the real line went through, and show the staff
// member what swapping would do to the customer's price. That is the whole feature.
//
// WHAT IT IS EMPHATICALLY NOT (R6). It introduces no pricing rule, no discount, no override, and no
// new anchor. It does not write to any pricing table. It is a lens over the engine, not a participant
// in it, if a comparison ever seems to need a pricing change, that is above the bar and stops.
//
// HOW "WRITES NOTHING" IS ACTUALLY TRUE, not merely intended. The waterfall CAN write: with margin
// enforcement disabled for a category or product it appends a BYPASS row to pricing_audit_log so a
// disabled item stays observable. Scoring five alternates through the live entry point would stamp
// five BYPASS rows for prices nobody offered anyone. So this path goes through
// pricing.ResolvePricePreview, the read-only door, and the acceptance test for this item asserts the
// pricing_audit_log row count is UNCHANGED across a comparison, proving the absence rather than
// asserting the call.
//
// The output shape follows prior art rather than inventing one: W7's pricing_order_lines already
// snapshots the engine price alongside the list price so a printed document can carry a comparison
// (0043_pos_counter_v2.sql). Same idea, computed live instead of stored.
package quote

import (
	"context"
	"fmt"
	"sort"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// maxAlternates bounds one comparison request. Each alternate is a full waterfall evaluation with its
// own catalogue reads, so an unbounded list would let one request fan out arbitrarily.
const maxAlternates = 10

// PricePreviewer is the READ-ONLY pricing seam (satisfied by *pricing.Service.ResolvePricePreview).
//
// It is a DISTINCT interface from PriceResolver, not a second method on it, and that is the point: a
// type that can only preview cannot accidentally be handed to a write path, and a write path cannot
// accidentally be handed to the comparison. The compiler enforces the R6 boundary rather than a
// comment asking people to remember it.
type PricePreviewer interface {
	ResolvePricePreview(ctx context.Context, in pricing.ResolveInput) (pricing.Breakdown, error)
}

// WithPricePreview injects the read-only pricing seam used by the comparison surface.
func (s *StaffService) WithPricePreview(p PricePreviewer) *StaffService { s.preview = p; return s }

// Alternate is one candidate the caller wants scored against a quote line.
type Alternate struct {
	ProductID string `json:"product_id"`
	// UOM is optional; empty means "price it in the same unit the current line is entered in", which
	// is what makes the comparison like-for-like rather than a units puzzle.
	UOM string `json:"uom"`
}

// ComparisonRow is one scored option, the current line, or an alternate.
type ComparisonRow struct {
	ProductID      string `json:"product_id"`
	IsCurrent      bool   `json:"is_current"`
	QtyEntered     string `json:"qty_entered"`
	UOMEntered     string `json:"uom_entered"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	PriceUOM       string `json:"price_uom"`
	LineTotalCents int64  `json:"line_total_cents"`
	// ListPriceCents is the retail anchor the waterfall started from, so the row shows what the
	// customer is being saved relative to list, the same pairing pricing_order_lines persists.
	ListPriceCents int64  `json:"list_price_cents"`
	DiscountBps    int64  `json:"discount_bps"`
	Source         string `json:"source"`
	// DeltaCents is this option's line total minus the CURRENT line's. Negative = cheaper for the
	// customer. Zero on the current row by construction.
	DeltaCents int64 `json:"delta_cents"`
	// Blocked reports that this option resolves below the margin floor. It is surfaced rather than
	// hidden: "you cannot offer this one" is exactly what a value-engineering conversation needs to
	// know, and silently dropping it would leave staff wondering why an option vanished.
	Blocked     bool   `json:"blocked"`
	BlockPolicy string `json:"block_policy,omitempty"`
	// Unavailable carries why an option could not be scored at all (no conversion for the UOM, no such
	// product). Surfaced for the same reason.
	Unavailable string `json:"unavailable,omitempty"`
}

// LineComparison is the comparison for one quote line.
type LineComparison struct {
	QuoteID string          `json:"quote_id"`
	LineNo  int             `json:"line_no"`
	Options []ComparisonRow `json:"options"`
}

// CompareLine scores alternates against one quote line, read-only (R6).
//
// Authority is the ordinary staff read path: the caller must be active and must reach the quote's
// branch. It is deliberately NOT gated on the edit matrix or the expiry window, comparing options on
// an accepted or lapsed quote is a legitimate thing to do (it is how the next quote gets written), and
// nothing here can change the document.
func (s *StaffService) CompareLine(ctx context.Context, quoteID string, lineNo int, alts []Alternate) (LineComparison, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return LineComparison{}, err
	}
	if caller.Status != identity.StatusActive {
		return LineComparison{}, ErrForbidden
	}
	if s.preview == nil || s.inv == nil || s.custType == nil {
		return LineComparison{}, ErrEditNotConfigured
	}
	if len(alts) > maxAlternates {
		return LineComparison{}, httpx.BadRequestf(
			"compare at most %d alternates at a time", maxAlternates)
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return LineComparison{}, err
	}
	_, branchID, _, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return LineComparison{}, err
	}
	if !caller.CanAccessBranch(branchID) {
		return LineComparison{}, ErrForbidden
	}
	cur, uom, err := s.store.LineForEdit(ctx, quoteID, lineNo)
	if err != nil {
		return LineComparison{}, err
	}
	customerID, branchID, err := s.store.QuotePricingContext(ctx, quoteID)
	if err != nil {
		return LineComparison{}, err
	}
	custType, err := s.custType.CustomerType(ctx, customerID)
	if err != nil {
		return LineComparison{}, fmt.Errorf("resolve customer pricing tier: %w", err)
	}
	qty, err := parseQuoteQty(cur.QtyEntered)
	if err != nil {
		return LineComparison{}, err
	}

	out := LineComparison{QuoteID: quoteID, LineNo: lineNo, Options: []ComparisonRow{}}

	// The current line is scored through the SAME preview path as every alternate rather than read off
	// the stored row. Comparing a stored price (set whenever the line was last touched) against
	// freshly-resolved alternates would silently compare two different days' rates and present the
	// difference as a product choice.
	current := s.scoreOption(ctx, customerID, custType, branchID, cur.ProductID, uom, qty)
	current.IsCurrent = true
	out.Options = append(out.Options, current)
	baseline := current.LineTotalCents

	seen := map[string]bool{cur.ProductID: true}
	for _, a := range alts {
		pid := strings.TrimSpace(a.ProductID)
		if pid == "" || seen[pid] {
			continue // an empty or duplicate candidate is a caller slip, not an error worth failing on
		}
		seen[pid] = true
		u := strings.TrimSpace(a.UOM)
		if u == "" {
			u = uom // like-for-like by default
		}
		row := s.scoreOption(ctx, customerID, custType, branchID, pid, u, qty)
		if row.Unavailable == "" {
			row.DeltaCents = row.LineTotalCents - baseline
		}
		out.Options = append(out.Options, row)
	}
	// Cheapest first among the scorable options, current line pinned at the top: the point of the
	// surface is "what could I offer instead", so the answer should be the first thing read.
	sort.SliceStable(out.Options, func(i, j int) bool {
		a, b := out.Options[i], out.Options[j]
		if a.IsCurrent != b.IsCurrent {
			return a.IsCurrent
		}
		if (a.Unavailable == "") != (b.Unavailable == "") {
			return a.Unavailable == "" // scorable options above unscorable ones
		}
		return a.LineTotalCents < b.LineTotalCents
	})
	return out, nil
}

// scoreOption evaluates ONE product through the read-only waterfall.
//
// A failure to score is captured on the row rather than failing the whole comparison: one alternate
// with no UOM conversion must not blank the screen for the four that resolved fine.
func (s *StaffService) scoreOption(ctx context.Context, customerID, custType, branchID, productID, uom string, qty float64) ComparisonRow {
	row := ComparisonRow{ProductID: productID, UOMEntered: uom, QtyEntered: formatQty(qty)}
	cost, _, err := s.inv.CurrentCostCents(ctx, productID, branchID)
	if err != nil {
		row.Unavailable = "no cost basis for this product at this branch"
		return row
	}
	bd, err := s.preview.ResolvePricePreview(ctx, pricing.ResolveInput{
		CustomerID: customerID, CustomerType: custType,
		ProductID: productID, BranchID: branchID,
		QtyEntered: qty, UOMEntered: uom, CostCents: cost,
	})
	if err != nil {
		row.Unavailable = "cannot be priced in this unit"
		return row
	}
	row.UnitPriceCents = bd.FinalCents
	row.PriceUOM = bd.PriceUOM
	row.LineTotalCents = bd.LineTotalCents
	row.ListPriceCents = bd.OriginalCents
	row.DiscountBps = bd.DiscountBps
	row.Source = bd.Source
	row.Blocked = bd.Blocked
	row.BlockPolicy = bd.BlockPolicy
	return row
}
