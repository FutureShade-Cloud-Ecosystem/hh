package gl

// journal_clock.go, the GL's own as-of filing rule answered to the other modules
// (W2-9a round 2b, S2's P2 1): a document's as-of instant is its posting journal's
// effective instant, the same asOfEffectiveInstant rule the account reads keep
// (FW1-01, ruling 82.2). A POSTED journal counts at its posted_at, except a late
// filed one, whose document date sits before the month it was filed in; that one
// counts from the end of its FILED period, because a closed month's figures are
// final at its end. The SQL lives here, inside the module that owns the tables;
// purchasing and landedcost read the rule through their own consumer interfaces,
// never through the tables.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// JournalClockEntry is one POSTED journal's as-of filing: the source document it
// was filed against, the instant it carries (its document date), and the instant an
// as of read counts it at.
type JournalClockEntry struct {
	SourceRefID string
	PostedAt    time.Time
	EffectiveAt time.Time
}

// JournalEffectiveInstants returns each named POSTED journal's effective instant. A
// journal that has no POSTED row is absent from the map.
func (s *Service) JournalEffectiveInstants(ctx context.Context, journalIDs []string) (map[string]time.Time, error) {
	return s.repo.JournalEffectiveInstants(ctx, journalIDs)
}

// RefJournalInstants returns, per source_ref_id, every POSTED journal filed against
// source with one of the named refs, each with its posted and effective instants.
func (s *Service) RefJournalInstants(ctx context.Context, source string, refIDs []string) (map[string][]JournalClockEntry, error) {
	return s.repo.RefJournalInstants(ctx, source, refIDs)
}

// JournalEffectiveInstantByKey returns the effective instant of the POSTED journal
// filed under an idempotency key, found false when no POSTED journal carries it.
func (s *Service) JournalEffectiveInstantByKey(ctx context.Context, key string) (time.Time, bool, error) {
	return s.repo.JournalEffectiveInstantByKey(ctx, key)
}

// JournalSourceRefByKey returns the source document ref of the POSTED journal filed
// under an idempotency key, found false when no POSTED journal carries it.
func (s *Service) JournalSourceRefByKey(ctx context.Context, key string) (string, bool, error) {
	return s.repo.JournalSourceRefByKey(ctx, key)
}

// JournalAmountByKey returns the amount the POSTED journal filed under an idempotency key
// moved, found false when no POSTED journal carries it. A journal is balanced, so the sum of
// its debit lines is what it moved: this is the other half of JournalSourceRefByKey's
// question, which document the key paid and how much, serving both PayBill's replay amount
// judgement and a payment run's read of what its own key already paid.
func (s *Service) JournalAmountByKey(ctx context.Context, key string) (int64, bool, error) {
	return s.repo.JournalAmountByKey(ctx, key)
}

func (r *Repository) JournalEffectiveInstants(ctx context.Context, journalIDs []string) (map[string]time.Time, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if len(journalIDs) == 0 {
		return map[string]time.Time{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT j.id::text, `+asOfEffectiveInstant+`
		  FROM gl_journals j
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.status = 'POSTED' AND j.id = ANY($1::uuid[])`,
		journalIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("gl: journal effective instants: %w", err)
	}
	defer rows.Close()
	out := make(map[string]time.Time, len(journalIDs))
	for rows.Next() {
		var id string
		var eff time.Time
		if err := rows.Scan(&id, &eff); err != nil {
			return nil, fmt.Errorf("gl: journal effective instants scan: %w", err)
		}
		out[id] = eff
	}
	return out, rows.Err()
}

func (r *Repository) RefJournalInstants(ctx context.Context, source string, refIDs []string) (map[string][]JournalClockEntry, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if len(refIDs) == 0 {
		return map[string][]JournalClockEntry{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT COALESCE(j.source_ref_id::text, ''), j.posted_at, `+asOfEffectiveInstant+`
		  FROM gl_journals j
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.status = 'POSTED' AND j.source = $1 AND j.source_ref_id = ANY($2::uuid[])
		 ORDER BY j.source_ref_id, j.posted_at, j.id`,
		source, refIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("gl: ref journal instants: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]JournalClockEntry)
	for rows.Next() {
		var e JournalClockEntry
		var ref string
		if err := rows.Scan(&ref, &e.PostedAt, &e.EffectiveAt); err != nil {
			return nil, fmt.Errorf("gl: ref journal instants scan: %w", err)
		}
		e.SourceRefID = ref
		out[ref] = append(out[ref], e)
	}
	return out, rows.Err()
}

func (r *Repository) JournalEffectiveInstantByKey(ctx context.Context, key string) (time.Time, bool, error) {
	if r.pool == nil {
		return time.Time{}, false, errNoDB
	}
	var eff time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT `+asOfEffectiveInstant+`
		  FROM gl_journals j
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.status = 'POSTED' AND j.idempotency_key = $1`,
		key,
	).Scan(&eff)
	if err == pgx.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("gl: journal effective instant by key: %w", err)
	}
	return eff, true, nil
}

func (r *Repository) JournalSourceRefByKey(ctx context.Context, key string) (string, bool, error) {
	if r.pool == nil {
		return "", false, errNoDB
	}
	var ref string
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(source_ref_id::text, '') FROM gl_journals WHERE status = 'POSTED' AND idempotency_key = $1`,
		key,
	).Scan(&ref)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("gl: journal source ref by key: %w", err)
	}
	return ref, true, nil
}

// JournalAmountByKey is JournalAmountByKey's repository read: the debit total of the POSTED
// journal filed under the key. The journal's own lines are the amount, so a credit line's null
// debit drops out of the sum and the balanced journal's debits are what it moved.
func (r *Repository) JournalAmountByKey(ctx context.Context, key string) (int64, bool, error) {
	if r.pool == nil {
		return 0, false, errNoDB
	}
	var amount int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(l.debit_cents)
		                   FROM gl_journal_lines l
		                  WHERE l.journal_id = j.id), 0)
		  FROM gl_journals j
		 WHERE j.status = 'POSTED' AND j.idempotency_key = $1`, key,
	).Scan(&amount)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("gl: journal amount by key: %w", err)
	}
	return amount, true, nil
}
