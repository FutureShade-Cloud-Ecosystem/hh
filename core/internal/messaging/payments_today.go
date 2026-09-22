package messaging

import (
	"context"
	"time"
)

// - DIB-463: THE PAYMENTS-TODAY READ -
//
// "A dashboard card version of the daily payments rollup for owner / finance roles: the day's total
// plus a click-through to the day's payment list. Same underlying data as the #payments channel, one
// source, two surfaces."
//
// - The whole ticket is that last sentence -
//
// The obvious implementation of a payments-today tile is `SELECT count(*), sum(amount_cents) FROM
// payments WHERE received_at::date = today`. It would be one line, it would look right, and it would
// be a SECOND definition of "today's payments" living beside the rollup's. The two would then disagree
// the first time anything moved between them, a payment voided after the rollup wrote its line, a
// reply removed by the retention sweep (which the rollup's aggregate honours via deleted_at, see
// RollupAggregate), a payment that arrived while the outbox was behind, or simply the day boundary,
// since the rollup files a day in America/Toronto and `received_at::date` files it in whatever the
// session's timezone happens to be.
//
// A tile and a channel line that disagree about how much money came in today do not produce a
// question about the tile. They produce a person who stops believing both. So:
//
//	THE TILE READS THE ROLLUP. It does not recompute payments, and it has no access to a payments
//	table to recompute them from.
//
// What it reads is the rollup ROOT, the day's message, whose ref carries the count and total the
// channel surface is literally showing (rollup.go: rollupRootRef). Not a parallel aggregate over the
// replies, which would be *a* correct number but a different computation; the ROOT, so the tile shows
// the same characters the day line shows. Pinned by TestPaymentsToday_ReadsTheRollupNotThePayments and
// by TestIntegration_PaymentsToday_IgnoresPaymentsTheRollupNeverSaw.

// PaymentsToday is the tile's measurement: the day's count and total, and the day it is FOR.
//
// Day is echoed back rather than assumed by the caller because the rollup's day is the HOUSE day
// (America/Toronto, rollup.go), not the server's UTC date and not the browser's. A tile that says
// "today" must be able to say which today it means.
type PaymentsToday struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
	Cents int64  `json:"amount_cents"`
	// Channels is how many payments channels contributed a rollup line. Zero means no branch the
	// caller can see has taken a payment today, a real, common answer (it is 9am), and the reason the
	// tile shows "$0.00 · 0 payments" rather than an em-dash: the number IS measured, and it is zero.
	Channels int `json:"channels"`
}

// paymentsTodayStore is the DIB-463 persistence seam, discovered by type assertion off the base store
// like every other seam in this module.
type paymentsTodayStore interface {
	// RollupTotalsForDay folds the rollup ROOTS of one day across the payments channels the caller may
	// see. branchIDs are the caller's granted branches; allBranches is a GLOBAL-scope caller.
	RollupTotalsForDay(ctx context.Context, eventKey string, branchIDs []string, allBranches bool) (count int, cents int64, channels int, err error)
}

func (s *Service) paymentsToday() (paymentsTodayStore, error) {
	if ps, ok := s.repo.(paymentsTodayStore); ok {
		return ps, nil
	}
	return nil, errNoDB
}

// PaymentsTodayFor measures the payments-today tile for one caller.
//
// Branch scoping is the caller's, and it is enforced HERE rather than trusted to the dashboard: the
// MetricProvider contract says in as many words that "the dashboard cannot enforce branch scoping on a
// number it did not compute". A branch-scoped manager sees their branches' day lines plus the GLOBAL
// #payments channel (a branchless payment is nobody's branch and everybody's business, it is where
// 0152 routes a payment whose event carries no branch, so excluding it would silently drop money out
// of the only total that claims to be the day's).
//
// `at` is the clock the day is derived from, injected rather than read here so a test can pin the day
// and so the caller's own Now (dashboard.Scope.Now) is the one that decides.
func (s *Service) PaymentsTodayFor(ctx context.Context, branchIDs []string, allBranches bool, at time.Time) (PaymentsToday, error) {
	ps, err := s.paymentsToday()
	if err != nil {
		return PaymentsToday{}, err
	}
	day := rollupDay(at)
	count, cents, channels, err := ps.RollupTotalsForDay(ctx, rollupKey(ClassRoutine, day), branchIDs, allBranches)
	if err != nil {
		return PaymentsToday{}, err
	}
	return PaymentsToday{Day: day, Count: count, Cents: cents, Channels: channels}, nil
}
