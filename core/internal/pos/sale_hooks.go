package pos

// sale_hooks.go, SCAFFOLD-owned, pos-DECLARED consumer seams for the two W6 cross-wave dependencies the Sale
// takes on: tax assessment (M5) and stock decrement (INV-CORE). Both ship with BEHAVIOR-PRESERVING defaults so
// pos runs bit-identically to W4/W5 until main.go rewires the real services (two later scaffold-owner rewire
// commits merged WITH W6-M5 and W6-INVCORE). FROZEN, a worker fills the real adapter, never edits these.
//
//	TaxAssessor      default FlatHSTAssessor{}, flat 13% (flatHSTRateBps), identical to the removed flatHST13
//	                 call sites; the binding tax + rate are unchanged.
//	StockDecrementer default NoopStock{}      , zero moves; a POS sale commits exactly as before.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// -- tax assessment seam -------------------------------------------------------

// TaxAssessor computes the BINDING tax on a sale subtotal. The default FlatHSTAssessor is the W4/W5 flat-13%
// behavior; M5 swaps in the real engine via Service.WithTaxAssessor (a main.go adapter over *tax.Service).
type TaxAssessor interface {
	AssessSale(ctx context.Context, in TaxQuoteInput) (QuoteResult, error)
}

// TaxQuoteInput is a tax-assessment request for one sale subtotal. ReasonCode "" = standard-rated.
type TaxQuoteInput struct {
	BranchID      string
	SubtotalCents int64
	ReasonCode    string
}

// QuoteResult is the binding tax + binding rate. RateBps carries the fallback rate the assess-relief paths
// route through (FlatHSTAssessor returns 1300 → bit-identical to the old flatHSTRateBps fallbacks).
type QuoteResult struct {
	TaxCents      int64
	RateBps       int
	OnCreditCents int64
	// ReasonCode is the reason the assessor bound (the dealer's own standard reason for a standard
	// sale), Jurisdiction the dealer's tax jurisdiction and StandardRateBps the standard rate of that
	// jurisdiction on the sale date. An assessor that answers none of the three is the Ontario dealer:
	// STD_ON_HST_13, ON and 1300.
	ReasonCode      string
	Jurisdiction    string
	StandardRateBps int
}

// FlatHSTAssessor is the behavior-preserving default: flat 13% HST, RateBps 1300, identical to flatHST13. It
// ignores BranchID/ReasonCode (the real M5 engine reads them). FROZEN.
type FlatHSTAssessor struct{}

// AssessSale returns the flat-13% tax + the 1300 bps rate (bit-identical to the removed flatHST13 call sites).
func (FlatHSTAssessor) AssessSale(_ context.Context, in TaxQuoteInput) (QuoteResult, error) {
	return QuoteResult{TaxCents: flatHST13(in.SubtotalCents), RateBps: flatHSTRateBps}, nil // Ontario by omission
}

// -- stock decrement seam ------------------------------------------------------

// StockDecrementer decrements stock for a fulfilled sale inside the Sale's tx. The default NoopStock does
// nothing; INV-CORE swaps in the real impl via Service.WithStock (a main.go adapter over *inventory.Service).
type StockDecrementer interface {
	DecrementForSaleTx(ctx context.Context, tx pgx.Tx, in StockDecrementInput) error
}

// StockDecrementInput is the decrement request for one fulfilled sale (COLLECT_NOW only at W6).
type StockDecrementInput struct {
	OrderID  string
	BranchID string
	SaleType string
	Lines    []StockLine
}

// StockLine is one fulfilled line (the frozen order.fulfilled shape).
type StockLine struct {
	ProductID string
	QtyBase   float64
}

// NoopStock is the behavior-preserving default: it moves no stock, so a POS sale commits exactly as at W5.
type NoopStock struct{}

// DecrementForSaleTx does nothing (identical to gl.NoopCostBasis's dormancy on the inventory side). FROZEN.
func (NoopStock) DecrementForSaleTx(context.Context, pgx.Tx, StockDecrementInput) error { return nil }
