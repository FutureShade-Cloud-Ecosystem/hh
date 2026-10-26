package gl

// close_preflight.go, FW1-09b: the checks period close runs before it seals a
// month, and the dollar figures a refusal names (the GL-02 blocker "a month
// can be closed while it is still trading"). Each check reads its own owner's
// seam: the AR subledger through billing's ARSubledger, the stock ledger
// through StockValueReader, the outbox through OutboxQueueReader, the till
// drawers through TillDrawerReader. A nil seam is an unwired check: it passes
// with no figures, never blocks a close on data nobody can read.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// closePreflight fails is one named check with its figures; the refusal joins
// them so the controller reads everything wrong in one look.
type preflightFail struct{ words string }

// runClosePreFlight answers one line per failing check, empty when the month
// is clean to seal. The tie checks read INCEPTION-TO-DATE balances through
// the month's end in the dealer's house zone, the same basis the AR tie-out
// reads (AccountBalanceAsOf), so a month that ties at its end refuses for no
// later postings.
func (s *Service) runClosePreFlight(ctx context.Context, year, month int) []string {
	var fails []string
	zone := houseday.DefaultZone()
	asOf := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, zone.Location())
	monthStart := asOf
	if s.ar != nil {
		sub, err := s.ar.ARBalanceCents(ctx, asOf, "")
		if err != nil {
			fails = append(fails, fmt.Sprintf("the AR subledger could not be read (%v)", err))
		} else if gl, _, gerr := s.store.AccountBalanceAsOf(ctx, "1100", asOf, ""); gerr != nil {
			fails = append(fails, fmt.Sprintf("GL 1100 could not be read (%v)", err))
		} else if sub != gl {
			fails = append(fails, fmt.Sprintf("AR is out of tie with 1100 by %s (the subledger holds %s, 1100 holds %s)",
				usdCents(sub-gl), usdCents(sub), usdCents(gl)))
		}
	}
	if s.stockValue != nil {
		stock, err := s.stockValue.StockValueCents(ctx, "")
		if err != nil {
			fails = append(fails, fmt.Sprintf("the stock ledger could not be read (%v)", err))
		} else if gl, _, gerr := s.store.AccountBalanceAsOf(ctx, "1200", asOf, ""); gerr != nil {
			fails = append(fails, fmt.Sprintf("GL 1200 could not be read (%v)", err))
		} else if stock != gl {
			fails = append(fails, fmt.Sprintf("stock is out of tie with 1200 by %s (the stock ledger holds %s, 1200 holds %s)",
				usdCents(stock-gl), usdCents(stock), usdCents(gl)))
		}
	}
	if s.queue != nil {
		if n, err := s.queue.UnpostedForWindow(ctx, PostedTopics, monthStart, asOf); err != nil {
			fails = append(fails, fmt.Sprintf("the posting queue could not be read (%v)", err))
		} else if n > 0 {
			fails = append(fails, fmt.Sprintf("%d posting event%s still waiting or dead lettered for this month", n, plural(n)))
		}
	}
	if s.drawers != nil {
		if n, err := s.drawers.OpenDrawersInMonth(ctx, year, month); err != nil {
			fails = append(fails, fmt.Sprintf("the till drawers could not be read (%v)", err))
		} else if n > 0 {
			fails = append(fails, fmt.Sprintf("%d till drawer%s still open for this month", n, plural(n)))
		}
	}
	return fails
}

func plural(n int) string {
	if n == 1 {
		return " is"
	}
	return "s are"
}

// usdCents renders a signed cents figure as dollars with commas, the figures
// a pre flight refusal names ("-$12.34", "$1,234.56").
func usdCents(c int64) string {
	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}
	dollars := c / 100
	cents := c % 100
	s := fmt.Sprintf("%d", dollars)
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return fmt.Sprintf("%s$%s.%02d", sign, strings.Join(parts, ","), cents)
}
