package gl

// posting_window.go, the GL's own answer to the question a sibling module must ask before it
// takes a document date: would a posting dated on this instant file, and if not, why not.
//
// The rule is the posting path's own rule (the year roll forward, the closed month, the one
// year horizon), read here without writing anything, so the answer and the posting agree. The
// SQL lives here, inside the module that owns the fiscal periods; the other modules read the
// answer through their own consumer interfaces, never through the tables.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PostingDateRefusal answers the words a SYSTEM posting dated on the given instant would be
// refused with, or "" when it would post. A system posting is the shape a payment takes: a
// closed month files it late into the earliest open month at or after it, and a month before
// the books' first year files it late the same way, so only a date beyond the books' horizon
// refuses. A manual posting has one refusal more (a closed month), which this window does not
// answer.
//
// The instant is read in the dealer's own house zone, the zone a posting's period is filed in,
// so this is the answer to the question the posting path will ask about this very instant.
func (s *Service) PostingDateRefusal(ctx context.Context, at time.Time) (string, error) {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	zone, err := s.repo.journalZone(ctx, tx)
	if err != nil {
		return "", err
	}
	y, m := fiscalYearMonthIn(zone, at)
	return s.repo.postingDateRefusalTx(ctx, tx, y, m)
}

// postingDateRefusalTx is PostingDateRefusal's repository read, on the caller's transaction.
//
// It mirrors insertJournalIn's period resolution exactly:
//   - a period row for the month: an OPEN month takes the posting, a CLOSED month files a
//     system posting late into the earliest open month at or after it, and the roll forward
//     opens one when every month from there on is closed, so it posts;
//   - no period row: the year roll forward creates the following year when the date is one
//     year past the books' latest (exactly one year, never a bridge), a date beyond that
//     refuses, and a date before it files late into the earliest open month at or after it.
//
// So the only refusal a system posting meets is a date more than one year past the books'
// latest year, which is the same refusal the posting path hands back, word for word.
func (r *Repository) postingDateRefusalTx(ctx context.Context, tx pgx.Tx, y, m int) (string, error) {
	var status string
	err := tx.QueryRow(ctx,
		`SELECT status FROM fiscal_periods WHERE year = $1 AND month = $2`, y, m).Scan(&status)
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("gl: read the fiscal period for the posting window: %w", err)
	}
	maxYear, err := r.maxSeededYearTx(ctx, tx)
	if err != nil {
		return "", err
	}
	if y > maxYear+1 {
		return fmt.Sprintf("no fiscal period for %04d-%02d: a date more than one year past the books' latest year is refused", y, m), nil
	}
	return "", nil
}
