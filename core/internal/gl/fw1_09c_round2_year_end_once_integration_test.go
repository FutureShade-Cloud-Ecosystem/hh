//go:build integration

// fw1_09c_round2_year_end_once_integration_test.go, FW1-09c round 2 (S2's P2, FROM-PRINCIPAL 320):
// the year end close is once only for a year with NO P&L too.
//
// THE DEFECT. The close's once only check reads the year's CLOSING journals. A book with no P&L
// at all posts no closing journal, so the check cannot see the first run: a second press passes
// every check (the year has ended, no OPEN period is left to pre flight, no P&L to sweep) and
// records a SECOND GL_YEAR_END_CLOSE row, and two Controllers pressing at once both record one.
//
// THE LEGS. S2's two probes on a year with NO P&L, through the real recorder:
//   - two presses one after another leave exactly one row;
//   - two Controllers at once leave one close and one row, the second answered as already closed.
//
// Then a burst across the data shapes that matter: rounds on a year with NO P&L beside rounds on a
// year WITH P&L, each round two Controllers released together, the round count quoted.
//
// ROUND 3 (S2's narrowed P2, FROM-PRINCIPAL 329): the same defect on a THIRD shape, a year whose
// P&L nets to zero on every account. The year HOLDS a P&L (a revenue journal and its reversal,
// posted through the production seam with a branch stamp), so the round 2 early return does not
// fire: it asks whether the year holds a P&L AT ALL, and this year holds one that nets to zero.
// Every account nets to zero, so the close sweeps nothing and posts no closing journal, and the
// close's own once only check cannot see the first run. The new leg presses such a year twice,
// one after the other, through the real recorder, and the burst gains the shape beside the other
// two.
//
// ISOLATION. Fiscal years 3083, 3084, 3085, 3086 and the burst bands 3090 to 3129 and 3150 to
// 3169, outside the seeded 2026 periods, the 2081 to 2099 bands and the 3074 to 3082 bands the
// package's other integration tests use. 3085 is the year with NO periods at all (ruling 321's
// leg). 3086 is round 3's zero net year.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09c ./internal/gl/
package gl

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_09cSecondUser answers a second seeded user id, so the burst's two Controllers are two
// people and not one caller wearing two services.
func fw1_09cSecondUser(t *testing.T, pool *pgxpool.Pool, first string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE id <> $1::uuid ORDER BY id LIMIT 1`, first).Scan(&id); err != nil {
		t.Fatalf("read a second seeded user: %v", err)
	}
	return id
}

// fw1_09cController builds the Controller's service for a year, the clock standing after the
// year's last day (the year has ended, so the close's own refusal cannot be what answers).
func fw1_09cController(t *testing.T, pool *pgxpool.Pool, user string, year int, recorder AuditTxRecorder) *Service {
	t.Helper()
	svc := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}, time.Date(year+1, 1, 5, 12, 0, 0, 0, time.UTC))
	if recorder != nil {
		svc = svc.WithAuditTxRecorder(recorder)
	}
	return svc
}

// fw1_09cBranch answers a seeded branch id to stamp the round 3 journals under: a branch
// that is NOT the head office, so the zero net shape is proved on the branch stamped path
// and not on the company level path, which the close maps onto the head office itself.
func fw1_09cBranch(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT l.id::text FROM locations l
		  WHERE l.id <> COALESCE((SELECT p.head_office_branch_id FROM gl_policy p WHERE p.branch_id IS NULL LIMIT 1), l.id)
		  ORDER BY l.id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("read a seeded branch that is not the head office: %v", err)
	}
	return id
}

// fw1_09cZeroNetPL posts a revenue journal and its reversal into the year through the
// production seam (PostExternal), both stamped with a branch, so the year's P&L nets to
// zero on EVERY account: the revenue account ends where it started and the cash it touched
// is back. The year still HOLDS a P&L, which is the whole shape: the account moved.
func fw1_09cZeroNetPL(t *testing.T, svc *Service, year int, branch string) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("fw1-09c-r3-rev-%d", rand.Uint64()),
		DocumentDate: time.Date(year, 6, 10, 12, 0, 0, 0, time.UTC),
		BranchID:     branch,
		Lines:        []ExternalLine{{AccountCode: "1000", DebitCents: 100000}, {AccountCode: "4000", CreditCents: 100000}},
	}); err != nil {
		t.Fatalf("post the year's revenue: %v", err)
	}
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("fw1-09c-r3-rev-reversal-%d", rand.Uint64()),
		DocumentDate: time.Date(year, 6, 20, 12, 0, 0, 0, time.UTC),
		BranchID:     branch,
		Lines:        []ExternalLine{{AccountCode: "4000", DebitCents: 100000}, {AccountCode: "1000", CreditCents: 100000}},
	}); err != nil {
		t.Fatalf("post the year's revenue reversal: %v", err)
	}
}

// fw1_09cPLRows answers the year's P&L rows the way the close reads them: one row per
// account per branch, with its net, and how many of those nets are not zero. A year whose
// P&L nets to zero still LISTS its account (the rows are there) and every one of those rows
// nets to zero, which is what the close's own sweep skips.
func fw1_09cPLRows(t *testing.T, pool *pgxpool.Pool, year int) (rows, nonZero int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE net_debit_cents <> 0) FROM (
		   SELECT account_code, SUM(net_debit_cents) AS net_debit_cents
		     FROM v_gl_account_balances
		    WHERE fiscal_year = $1 AND account_type IN ('REVENUE','EXPENSE')
		    GROUP BY account_code, COALESCE(branch_id::text,'')) s`, year).Scan(&rows, &nonZero); err != nil {
		t.Fatalf("read the year's P&L rows: %v", err)
	}
	return rows, nonZero
}

// TestIntegration_FW1_09c_R2_RepeatPressOnAYearWithNoPLLLeavesOneAuditRow is S2's
// first probe: a year with no P&L at all, pressed twice, one after the other. The first press
// closes the year (twelve periods sealed, no closing journal, one GL_YEAR_END_CLOSE row). The
// second press must answer the first run's result and record nothing.
func TestIntegration_FW1_09c_R2_RepeatPressOnAYearWithNoPLLLeavesOneAuditRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3083
	ensurePeriods(t, pool, year)
	january := periodID(t, pool, year, 1)
	fw1_09cDropAudit(t, pool, january)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1_09cController(t, pool, user, year, recorder)

	// The year holds no P&L at all: the close posts no closing journal, so the close's own
	// once only check cannot see the first run.
	first, err := controller.CloseYear(ctx, year, "FW1-09c round 2 the first press")
	if err != nil {
		t.Fatalf("the first press on the no P&L year: %v", err)
	}
	if !first.PeriodsClosed {
		t.Fatalf("the first press did not close the year: %+v", first)
	}
	closings, overrides, sealed, audits := fw1_09cWrites(t, pool, year, january)
	if closings != 0 {
		t.Fatalf("the no P&L year posted %d closing journals, want none (a book with no P&L closes nothing)", closings)
	}
	if sealed != 12 || overrides != 12 {
		t.Fatalf("the first press sealed %d periods and wrote %d override rows, want 12 and 12", sealed, overrides)
	}
	if audits != 1 {
		t.Fatalf("the first press left %d GL_YEAR_END_CLOSE rows, want exactly 1", audits)
	}

	// The second press: the year is already closed. It must answer the first run's result and
	// record nothing.
	second, err := controller.CloseYear(ctx, year, "FW1-09c round 2 the second press")
	if err != nil {
		t.Fatalf("the second press on the no P&L year: %v", err)
	}
	if !second.PeriodsClosed || second.NetIncomeCents != first.NetIncomeCents ||
		len(second.ClosingJournalIDs) != len(first.ClosingJournalIDs) {
		t.Fatalf("the second press answered %+v, want the first run's answer %+v (the year is already closed)", second, first)
	}
	closings, overrides, sealed, audits = fw1_09cWrites(t, pool, year, january)
	if audits != 1 {
		t.Fatalf("the repeat press left %d GL_YEAR_END_CLOSE audit rows, want exactly 1 (a repeat press records nothing)", audits)
	}
	if closings != 0 || overrides != 12 || sealed != 12 {
		t.Fatalf("the repeat press wrote anyway: %d closing journals, %d override rows, %d sealed periods; a repeat press writes nothing",
			closings, overrides, sealed)
	}
}

// TestIntegration_FW1_09c_R2_TwoControllersAtOnceOnAYearWithNoPLLLeaveOneCloseAndOneRow is
// S2's second probe: two Controllers pressing the same no P&L year at the same instant. The
// year's advisory lock serializes them, so one closes and one records, and the second press is
// answered as already closed. Today both record a row.
func TestIntegration_FW1_09c_R2_TwoControllersAtOnceOnAYearWithNoPLLLeaveOneCloseAndOneRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	first := aUser(t, pool)
	second := fw1_09cSecondUser(t, pool, first)
	const year = 3084
	ensurePeriods(t, pool, year)
	january := periodID(t, pool, year, 1)
	fw1_09cDropAudit(t, pool, january)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	one := fw1_09cController(t, pool, first, year, recorder)
	two := fw1_09cController(t, pool, second, year, recorder)

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]YearCloseResult, 2)
	errs := make([]error, 2)
	for i, svc := range []*Service{one, two} {
		wg.Add(1)
		go func(i int, svc *Service) {
			defer wg.Done()
			<-start
			results[i], errs[i] = svc.CloseYear(ctx, year, "FW1-09c round 2 two Controllers at once")
		}(i, svc)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("press %d failed: %v", i, err)
		}
	}
	// One close: the year's twelve periods sealed once, twelve override rows, no closing
	// journal (the year holds no P&L).
	closings, overrides, sealed, audits := fw1_09cWrites(t, pool, year, january)
	if closings != 0 {
		t.Fatalf("the no P&L year posted %d closing journals, want none", closings)
	}
	if sealed != 12 || overrides != 12 {
		t.Fatalf("the two presses left %d sealed periods and %d override rows, want 12 and 12 (one close)", sealed, overrides)
	}
	if audits != 1 {
		t.Fatalf("the two Controllers left %d GL_YEAR_END_CLOSE audit rows, want exactly 1 (one close, one row)", audits)
	}
	// The second press is answered as already closed: both answers carry the closed year.
	for i, res := range results {
		if !res.PeriodsClosed || res.FiscalYear != year {
			t.Fatalf("press %d answered %+v, want the closed year %d", i, res, year)
		}
	}
	if results[0].NetIncomeCents != results[1].NetIncomeCents ||
		len(results[0].ClosingJournalIDs) != len(results[1].ClosingJournalIDs) {
		t.Fatalf("the two presses answered differently: %+v and %+v (the second must answer the first run's result)", results[0], results[1])
	}
}

// TestIntegration_FW1_09c_R2_AYearWithNoPeriodsRefusesInWords is ruling 321's leg: a year
// with NO periods at all has nothing to close, so the close must refuse in words ("fiscal year
// N has no periods to close") and never answer success. It also holds the early return honest:
// the once only check reads the year's periods, and on a year with no periods "every period is
// CLOSED" would be vacuously true, so the count comes first and the refusal stands.
func TestIntegration_FW1_09c_R2_AYearWithNoPeriodsRefusesInWords(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3085
	// The year has no periods at all: no fiscal_periods row for it, ever.
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	wired := fw1_09cController(t, pool, user, year, recorder)
	unwired := fw1_09cController(t, pool, user, year, nil)

	// The unwired service first: with nothing to record, the close has nothing to fail on
	// and answers success today, which is the sharpest form of the defect.
	for _, tc := range []struct {
		name string
		svc  *Service
	}{{"unwired", unwired}, {"wired", wired}} {
		_, err := tc.svc.CloseYear(ctx, year, "FW1-09c round 2 a year with no periods")
		if err == nil {
			t.Fatalf("the %s close on the year with no periods succeeded; it must refuse in words", tc.name)
		}
		if !strings.Contains(err.Error(), "has no periods to close") {
			t.Fatalf("the %s close on the year with no periods refused with %q, want the words 'has no periods to close'", tc.name, err.Error())
		}
	}

	// The refusal writes nothing: no closing journal, no period, no audit row.
	var closings, periods, audits int
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM gl_journals WHERE fiscal_year = $1 AND source = 'CLOSING'),
		        (SELECT count(*) FROM fiscal_periods WHERE year = $1),
		        (SELECT count(*) FROM audit_log WHERE action = 'GL_YEAR_END_CLOSE' AND changes->>'fiscal_year' = $1::text)`,
		year).Scan(&closings, &periods, &audits); err != nil {
		t.Fatalf("read what the refused close wrote: %v", err)
	}
	if closings != 0 || periods != 0 || audits != 0 {
		t.Fatalf("the refused close wrote %d closing journals, %d periods and %d audit rows; a refusal writes nothing",
			closings, periods, audits)
	}
}

// TestIntegration_FW1_09c_R3_RepeatPressOnAYearWhosePLNetsToZeroLeavesOneAuditRow is round 3's
// leg (S2's narrowed P2, FROM-PRINCIPAL 329): a year whose P&L nets to zero on every account,
// pressed twice, one after the other, through the real recorder.
//
// THE SHAPE. The year HOLDS a P&L: a revenue journal and its reversal, posted through the
// production seam with a branch stamp. The revenue account moved and ended where it started,
// so every account nets to zero and the close sweeps nothing. The close posts no closing
// journal, so the close's own once only check cannot see the first run, and the round 2 early
// return cannot either: it asks whether the year holds a P&L AT ALL, and this year holds one
// that nets to zero. So the second press passes every check and records a SECOND
// GL_YEAR_END_CLOSE row.
//
// THE CLAIM. A repeat press records nothing: two presses leave exactly one row.
func TestIntegration_FW1_09c_R3_RepeatPressOnAYearWhosePLNetsToZeroLeavesOneAuditRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	const year = 3086
	ensurePeriods(t, pool, year)
	january := periodID(t, pool, year, 1)
	fw1_09cDropAudit(t, pool, january)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1_09cController(t, pool, user, year, recorder)

	// The year's P&L: a revenue and its reversal, under a branch stamp.
	fw1_09cZeroNetPL(t, controller, year, fw1_09cBranch(t, pool))
	// The shape is proved before the close runs: the year HOLDS a P&L row (so the round 2
	// early return cannot fire on it) and every one of those rows nets to zero (so the
	// close sweeps nothing and posts no closing journal).
	rows, nonZero := fw1_09cPLRows(t, pool, year)
	if rows == 0 {
		t.Fatalf("the year holds no P&L row at all; this leg needs a year that HOLDS a P&L that nets to zero")
	}
	if nonZero != 0 {
		t.Fatalf("the year's P&L holds %d rows that do not net to zero, want none (the year nets to zero on every account)", nonZero)
	}

	// The first press: the year is closed, nothing is swept, one row is recorded.
	first, err := controller.CloseYear(ctx, year, "FW1-09c round 3 the first press")
	if err != nil {
		t.Fatalf("the first press on the zero net year: %v", err)
	}
	if !first.PeriodsClosed {
		t.Fatalf("the first press did not close the year: %+v", first)
	}
	closings, overrides, sealed, audits := fw1_09cWrites(t, pool, year, january)
	if closings != 0 {
		t.Fatalf("the zero net year posted %d closing journals, want none (every account nets to zero, so the close sweeps nothing)", closings)
	}
	if sealed != 12 || overrides != 12 {
		t.Fatalf("the first press sealed %d periods and wrote %d override rows, want 12 and 12", sealed, overrides)
	}
	if audits != 1 {
		t.Fatalf("the first press left %d GL_YEAR_END_CLOSE rows, want exactly 1", audits)
	}

	// The second press: the year is already closed. It must answer the first run's result
	// and record nothing.
	second, err := controller.CloseYear(ctx, year, "FW1-09c round 3 the second press")
	if err != nil {
		t.Fatalf("the second press on the zero net year: %v", err)
	}
	if !second.PeriodsClosed || second.NetIncomeCents != first.NetIncomeCents ||
		len(second.ClosingJournalIDs) != len(first.ClosingJournalIDs) {
		t.Fatalf("the second press answered %+v, want the first run's answer %+v (the year is already closed)", second, first)
	}
	closings, overrides, sealed, audits = fw1_09cWrites(t, pool, year, january)
	if audits != 1 {
		t.Fatalf("the repeat press left %d GL_YEAR_END_CLOSE audit rows, want exactly 1 (a repeat press records nothing)", audits)
	}
	if closings != 0 || overrides != 12 || sealed != 12 {
		t.Fatalf("the repeat press wrote anyway: %d closing journals, %d override rows, %d sealed periods; a repeat press writes nothing",
			closings, overrides, sealed)
	}
}

// TestIntegration_FW1_09c_R2_BurstAcrossTheDataShapesThatMatter is the burst ruling 320 asks
// for: rounds on a year with NO P&L beside rounds on a year WITH P&L, each round two
// Controllers released together against the real database and the real recorder. The round count
// is quoted on the last line.
//
// ROUND 3: the burst carries the third shape too, a year whose P&L nets to zero on every
// account, beside the other two.
func TestIntegration_FW1_09c_R2_BurstAcrossTheDataShapesThatMatter(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	first := aUser(t, pool)
	second := fw1_09cSecondUser(t, pool, first)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	branch := fw1_09cBranch(t, pool)

	const rounds = 20
	type shape struct {
		name  string
		first int
		kind  string
	}
	const (
		shapeNoPL    = "no_pl"
		shapePL      = "pl"
		shapeZeroNet = "zero_net"
	)
	shapes := []shape{
		{name: shapeNoPL, first: 3090, kind: shapeNoPL},
		{name: shapePL, first: 3110, kind: shapePL},
		{name: shapeZeroNet, first: 3150, kind: shapeZeroNet},
	}
	oneClose, oneRow, sameResult, deadlocks := 0, 0, 0, 0
	for _, sh := range shapes {
		for round := 0; round < rounds; round++ {
			year := sh.first + round
			ensurePeriods(t, pool, year)
			january := periodID(t, pool, year, 1)
			fw1_09cDropAudit(t, pool, january)
			one := fw1_09cController(t, pool, first, year, recorder)
			two := fw1_09cController(t, pool, second, year, recorder)
			switch sh.kind {
			case shapePL:
				fw1_09cPL(t, one, year)
			case shapeZeroNet:
				fw1_09cZeroNetPL(t, one, year, branch)
			}

			start := make(chan struct{})
			var wg sync.WaitGroup
			results := make([]YearCloseResult, 2)
			errs := make([]error, 2)
			for i, svc := range []*Service{one, two} {
				wg.Add(1)
				go func(i int, svc *Service) {
					defer wg.Done()
					<-start
					results[i], errs[i] = svc.CloseYear(ctx, year, "FW1-09c round 2 burst")
				}(i, svc)
			}
			close(start)
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					if strings.Contains(err.Error(), "40P01") {
						deadlocks++
					}
					t.Fatalf("%s round %d (%d): press %d failed: %v", sh.name, round, year, i, err)
				}
			}
			closings, _, sealed, audits := fw1_09cWrites(t, pool, year, january)
			wantClosings := 0
			if sh.kind == shapePL {
				wantClosings = 1
			}
			if closings != wantClosings {
				t.Fatalf("%s round %d (%d): %d closing journals, want %d (one close)", sh.name, round, year, closings, wantClosings)
			}
			if sealed != 12 {
				t.Fatalf("%s round %d (%d): %d sealed periods, want 12 (one close)", sh.name, round, year, sealed)
			}
			if audits != 1 {
				t.Fatalf("%s round %d (%d): %d GL_YEAR_END_CLOSE rows, want exactly 1 (one close, one row)", sh.name, round, year, audits)
			}
			oneClose++
			oneRow++
			if results[0].NetIncomeCents == results[1].NetIncomeCents &&
				len(results[0].ClosingJournalIDs) == len(results[1].ClosingJournalIDs) &&
				results[0].PeriodsClosed && results[1].PeriodsClosed {
				sameResult++
			}
		}
	}
	total := rounds * len(shapes)
	if oneClose != total || oneRow != total || sameResult != total {
		t.Fatalf("the burst: %d one closes, %d one rows, %d same answers, want %d of each", oneClose, oneRow, sameResult, total)
	}
	if deadlocks != 0 {
		t.Fatalf("the burst saw %d deadlocks (40P01), want none", deadlocks)
	}
	t.Logf("FW1-09c round 3 burst: rounds=%d no_pl_rounds=%d pl_rounds=%d zero_net_rounds=%d one_close=%d one_audit_row=%d same_result=%d deadlocks=%d",
		total, rounds, rounds, rounds, oneClose, oneRow, sameResult, deadlocks)
	fmt.Printf("FW1-09c round 3 burst: rounds=%d no_pl_rounds=%d pl_rounds=%d zero_net_rounds=%d one_close=%d one_audit_row=%d same_result=%d deadlocks=%d\n",
		total, rounds, rounds, rounds, oneClose, oneRow, sameResult, deadlocks)
}
