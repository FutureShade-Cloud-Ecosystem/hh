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
