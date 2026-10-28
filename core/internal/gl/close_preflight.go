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
)

// closePreflight fails is one named check with its figures; the refusal joins
// them so the controller reads everything wrong in one look.
type preflightFail struct{ words string }

// runClosePreFlight answers one line per failing check, empty when the month
// is clean to seal. The instants are the dealer's zone's, read through the
// store's BusinessZone (round 4, S3's P2: the same zone the posting path files
// months by, never a hard coded default). The AR tie reads INCEPTION-TO-DATE
// balances at the FIRST INSTANT OF THE NEXT MONTH (the month's end in the
// dealer's terms), the same basis the AR tie-out reads, so a month that ties
// at its end refuses for no later postings. The STOCK tie reads BOTH sides at
// the same instant (FW1-09e): the stock ledger's own value now against GL
// 1200 as of the service's clock now, because inventory answers no as of read
// yet. A tie that read one side at the month's end and the other now refused
// every correct month whose next month had moved the stock. The queue reads
// the month's own window, [month start, next month start).
func (s *Service) runClosePreFlight(ctx context.Context, year, month int) []string {
	var fails []string
	zone, err := s.store.BusinessZone(ctx)
	if err != nil {
		return []string{fmt.Sprintf("the dealer's business zone could not be read (%v)", err)}
	}
	monthStart := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, zone.Location())
	nextMonth := monthStart.AddDate(0, 1, 0)
	if s.ar != nil {
		sub, err := s.ar.ARBalanceCents(ctx, nextMonth, "")
		if err != nil {
			fails = append(fails, fmt.Sprintf("the AR subledger could not be read (%v)", err))
		} else if gl, _, gerr := s.store.AccountBalanceAsOf(ctx, "1100", nextMonth, ""); gerr != nil {
			fails = append(fails, fmt.Sprintf("GL 1100 could not be read (%v)", gerr))
		} else if sub != gl {
			fails = append(fails, fmt.Sprintf("AR is out of tie with 1100 by %s (the subledger holds %s, 1100 holds %s)",
				usdCents(sub-gl), usdCents(sub), usdCents(gl)))
		}
	}
	if s.stockValue != nil {
		// Both sides at the same instant (FW1-09e). The stock ledger answers one figure, today's,
		// so 1200 is read as of the service's clock now, never as of the month's end: a tie that
		// read one side at the month's end and the other now refused every correct month whose
		// next month had moved the stock.
		stock, err := s.stockValue.StockValueCents(ctx, "")
		if err != nil {
			fails = append(fails, fmt.Sprintf("the stock ledger could not be read (%v)", err))
		} else if gl, _, gerr := s.store.AccountBalanceAsOf(ctx, "1200", s.clock(), ""); gerr != nil {
			fails = append(fails, fmt.Sprintf("GL 1200 could not be read (%v)", gerr))
		} else if stock != gl {
			fails = append(fails, fmt.Sprintf("stock is out of tie with 1200 by %s (both read now: the stock ledger holds %s, 1200 holds %s; the stock tie reads both sides at the same instant until inventory answers an as of month end read)",
				usdCents(stock-gl), usdCents(stock), usdCents(gl)))
		}
	}
	if s.preFlightQueue != nil {
		if n, err := s.preFlightQueue.UnpostedForWindow(ctx, PostedTopics, monthStart, nextMonth); err != nil {
			fails = append(fails, fmt.Sprintf("the posting queue could not be read (%v)", err))
		} else if n > 0 {
			fails = append(fails, fmt.Sprintf("%d posting event%s still waiting or dead lettered for this month", n, plural(n)))
		}
	}
	if s.drawers != nil {
		if n, err := s.drawers.OpenDrawers(ctx, monthStart, nextMonth); err != nil {
			fails = append(fails, fmt.Sprintf("the till drawers could not be read (%v)", err))
		} else if n > 0 {
			fails = append(fails, fmt.Sprintf("%d till drawer%s still open for this month", n, plural(n)))
		}
	}
	if s.strictPreFlight {
		// A slice of pairs, not a map keyed on the bool: two unwired checks must both be named.
		for _, check := range []struct {
			wired bool
			name  string
		}{
			{s.ar != nil, "the AR tie check"},
			{s.stockValue != nil, "the stock tie check"},
			{s.preFlightQueue != nil, "the posting queue check"},
			{s.drawers != nil, "the open drawer check"},
		} {
			if !check.wired {
				fails = append(fails, check.name+" is not wired, so it cannot run")
			}
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
