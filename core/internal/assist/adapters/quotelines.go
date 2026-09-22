package adapters

// quotelines.go, quoteassist.QuoteLineReader: one quote's lines, each carrying the catalogue
// application a scope rule reasons about.
//
// TWO MODULES ANSWER THIS ONE QUESTION, AND NEITHER LEARNS ABOUT THE OTHER.
//
//	internal/quote     WHICH lines this quote has, and whether this caller may see them at all.
//	internal/product   WHAT each of those products is FOR (products.application).
//
// The join lives here, in the composition layer, which is the only place allowed to know both
// vocabularies. quote does not know that products have applications; product does not know that
// quotes exist; and quoteassist knows neither, it consumes ScopeLine, which is the QUESTION
// ("what is on this quote, and what is each thing for") rather than either module's answer.
//
// -----------------------------------------------------------------------------------------------
// THE AUTHORITY CHECK IS THE QUOTE MODULE'S OWN, NOT A SECOND ONE
// -----------------------------------------------------------------------------------------------
// StaffQuoteRead resolves the guard-stamped caller from the context, refuses an inactive account,
// re-checks branch reach against the quote's own branch, and answers a cross-branch probe with a
// flat refusal. This adapter calls it and does not re-derive any of that. There is no Caller
// argument on QuoteScopeLines for exactly that reason: an argument would be a second opinion about
// who is asking, and the day the two disagreed the more permissive one would win.
//
// Both refusals, "no such quote" and "not yours", collapse to found=false, deliberately. The quote
// module went to some trouble to make those indistinguishable (ErrForbidden carries no detail); an
// adapter that separated them again would rebuild the existence oracle that care was taken to remove.
//
// -----------------------------------------------------------------------------------------------
// ONE CATALOGUE READ PER DISTINCT PRODUCT, AND WHY THAT IS FINE HERE
// -----------------------------------------------------------------------------------------------
// A quote is a handful of lines, the seeded demo quotes run 2 to 6, and the reads are deduplicated
// by product id, so a five-line quote with two Blu 60 lines costs four queries. product exposes no
// batch attribute read and adding one would mean editing the catalogue module, which this lane is
// fenced out of (read-only reuse only). The alternative inside the fence, SELECTing products from
// quoteassist, would break the module boundary that makes any of this reviewable.
//
// The honest bound is stated rather than assumed: maxScopeLines caps the work, and a quote longer
// than that is evaluated on its first lines with the fact recorded in a log line rather than a
// silent truncation.

import (
	"context"
	"errors"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quoteassist"
)

// maxScopeLines bounds the per-product catalogue reads one scope evaluation performs.
//
// 60 is an order of magnitude above the largest quote in the seeded data and two orders below
// anything that would matter for latency. It exists so the cost of this read has a stated ceiling
// rather than an assumed one, the same reason assist bounds its tool loop at one round.
const maxScopeLines = 60

// QuoteDetailReader is the quote-side read this adapter needs: one quote's staff view, authorised as
// the guard-stamped caller. Declared here so the adapter can be tested against a double.
type QuoteDetailReader interface {
	StaffQuoteRead(ctx context.Context, quoteID string) (quote.StaffQuoteView, error)
}

// ProductAttrReader is the catalogue-side read: one product's attributes. READ-ONLY, and the
// narrowest method on product.Service that answers "what is this for".
type ProductAttrReader interface {
	Get(ctx context.Context, id string) (product.Product, error)
}

// The shipped services satisfy both. Compile-time, for the reason the five assertions in adapters.go
// give: the wiring block is pasted into main.go by someone else, and a seam that did not fit would
// otherwise break in their commit rather than in this one.
var (
	_ QuoteDetailReader = (*quote.StaffService)(nil)
	_ ProductAttrReader = (*product.Service)(nil)
)

type quoteLinesAdapter struct {
	quotes   QuoteDetailReader
	products ProductAttrReader
	log      *slog.Logger
}

var _ quoteassist.QuoteLineReader = quoteLinesAdapter{}

// NewQuoteScopeLines builds the seam. EITHER service missing returns a nil seam, scope-gap
// detection needs both halves of the question, and a reader that could answer "which lines" but not
// "what for" would report a paved surface with no jointing sand on every quote in the business.
func NewQuoteScopeLines(quotes QuoteDetailReader, products ProductAttrReader, log *slog.Logger) quoteassist.QuoteLineReader {
	if quotes == nil || products == nil {
		return nil
	}
	return quoteLinesAdapter{quotes: quotes, products: products, log: log}
}

// QuoteScopeLines reads the quote and classifies each line.
func (a quoteLinesAdapter) QuoteScopeLines(ctx context.Context, quoteID string) ([]quoteassist.ScopeLine, bool, error) {
	view, err := a.quotes.StaffQuoteRead(ctx, quoteID)
	switch {
	case errors.Is(err, quote.ErrNotFound), errors.Is(err, quote.ErrForbidden):
		// ONE ANSWER for both. See the file header.
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}

	lines := view.Lines
	if len(lines) > maxScopeLines {
		if a.log != nil {
			a.log.Info("quoteassist: quote has more lines than the scope evaluator reads",
				"quote_id", quoteID, "lines", len(lines), "read", maxScopeLines)
		}
		lines = lines[:maxScopeLines]
	}

	// Deduplicated: the same product on two lines is one catalogue read.
	apps := map[string]string{}
	out := make([]quoteassist.ScopeLine, 0, len(lines))
	for _, l := range lines {
		app, seen := apps[l.ProductID]
		if !seen {
			app = a.applicationOf(ctx, l.ProductID)
			apps[l.ProductID] = app
		}
		out = append(out, quoteassist.ScopeLine{
			LineNo: l.LineNo,
			// The line's OWN description, which is what the quote actually says, not the
			// catalogue's current name for the product. A finding that names "line 1, Techo-Bloc
			// Blu 60 Slab" has to name it the way the quote does, or the rep goes looking for a
			// line that is not there.
			Name:        l.Description,
			Application: app,
		})
	}
	return out, true, nil
}

// applicationOf reads one product's application, or "" when it cannot.
//
// A FAILED CLASSIFICATION IS NOT AN ERROR HERE, and that is the load-bearing decision in this file.
// An unreadable or deleted product means one line cannot trigger or satisfy a rule; it does not mean
// the whole suggestion panel should fail. The direction of that degradation is deliberately the
// QUIET one: an unclassified line neither raises a suggestion nor silences one
// (scope.go normaliseApps drops blanks), so a catalogue hiccup produces fewer suggestions rather
// than a wrong one.
func (a quoteLinesAdapter) applicationOf(ctx context.Context, productID string) string {
	if productID == "" {
		return ""
	}
	p, err := a.products.Get(ctx, productID)
	if err != nil {
		if a.log != nil && !errors.Is(err, product.ErrNotFound) {
			a.log.Warn("quoteassist: could not classify a quote line's product, it will neither trigger nor satisfy a scope rule",
				"err", err, "product_id", productID)
		}
		return ""
	}
	return p.Application
}
