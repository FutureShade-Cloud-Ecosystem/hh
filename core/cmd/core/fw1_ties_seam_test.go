package main

// fw1_ties_seam_test.go pins, without a database, that the AP tie's 2100 naming
// reads the GL's own as-of filing rule through the GL module's seam and never a
// copy of its SQL (W3 item 6): the rule lives once, in gl, and a later change to
// the filing rule has to touch one file, not two.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// tieSeamRows is a canned pgx.Rows over the candidate journals the naming's own
// read returns.
type tieSeamRows struct {
	rows [][]any
	i    int
}

func (r *tieSeamRows) Next() bool { r.i++; return r.i <= len(r.rows) }

func (r *tieSeamRows) Scan(dest ...any) error {
	row := r.rows[r.i-1]
	for j, d := range dest {
		switch d := d.(type) {
		case *string:
			*d = row[j].(string)
		case *int64:
			*d = row[j].(int64)
		default:
			return fmt.Errorf("the probe holds no %T scan target", d)
		}
	}
	return nil
}

func (r *tieSeamRows) Close()                                       {}
func (r *tieSeamRows) Err() error                                   { return nil }
func (r *tieSeamRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *tieSeamRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *tieSeamRows) RawValues() [][]byte                          { return nil }
func (r *tieSeamRows) Values() ([]any, error)                       { return r.rows[r.i-1], nil }
func (r *tieSeamRows) Conn() *pgx.Conn                              { return nil }

type tieSeamQuerier struct{ rows [][]any }

func (q tieSeamQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	// A fresh cursor per read: the naming may be asked more than once.
	return &tieSeamRows{rows: q.rows}, nil
}

func (q tieSeamQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("the 2100 naming probe takes no row reads")
}

// tieSeamClock is the probe GL clock: it records that the naming asked it, and
// answers one journal's effective instant as the GL's rule filed it.
type tieSeamClock struct {
	called bool
	ids    []string
	eff    map[string]time.Time
}

func (c *tieSeamClock) JournalEffectiveInstants(_ context.Context, journalIDs []string) (map[string]time.Time, error) {
	c.called = true
	c.ids = journalIDs
	return c.eff, nil
}

// TestTieAPOtherJournalsReadsTheFilingRuleThroughTheGLSeam proves the naming
// asks the seam for every candidate's effective instant and names by the GL's
// answer: a manual 2100 journal whose posted_at sits inside the month but whose
// effective instant (a late filing) sits after the month's end is outside GL
// 2100's figure for the month, so it is not named. On the copied SQL the naming
// cut by its own CASE and named it.
func TestTieAPOtherJournalsReadsTheFilingRuleThroughTheGLSeam(t *testing.T) {
	monthEnd := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	rows := &tieSeamRows{rows: [][]any{{"j1", "5", "MANUAL", "a manual accrual", int64(2500)}}}
	clock := &tieSeamClock{eff: map[string]time.Time{
		"j1": monthEnd.AddDate(0, 1, 0), // filed into the open month: counts from its end
	}}
	seams := TieSeams{Querier: tieSeamQuerier{rows: rows.rows}, GLClock: clock}

	named, err := tieAPOtherJournals(context.Background(), seams, TieMonth{MonthEnd: monthEnd})
	if err != nil {
		t.Fatalf("the 2100 naming: %v", err)
	}
	if !clock.called {
		t.Fatalf("the 2100 naming never asked the GL seam: the filing rule is still the naming's own SQL copy")
	}
	if len(clock.ids) != 1 || clock.ids[0] != "j1" {
		t.Fatalf("the 2100 naming asked the seam about %v, want the one candidate j1", clock.ids)
	}
	if named != "none" {
		t.Fatalf("the 2100 naming named %q, want none: the journal's effective instant is past the month's end, so GL 2100's figure for the month does not hold it", named)
	}

	// And one whose effective instant is inside the month is named, with the
	// rule's own words.
	clock.eff["j1"] = monthEnd.Add(-time.Hour)
	named, err = tieAPOtherJournals(context.Background(), seams, TieMonth{MonthEnd: monthEnd})
	if err != nil {
		t.Fatalf("the 2100 naming: %v", err)
	}
	if named == "none" {
		t.Fatalf("the 2100 naming named none, want journal 5 named inside the month's figure")
	}
}
