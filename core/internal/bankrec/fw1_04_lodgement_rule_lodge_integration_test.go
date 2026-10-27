//go:build integration

package bankrec_test

// fw1_04_lodgement_rule_lodge_integration_test.go (FW1-04, the stack 1 fix): the Lodgement Rule leg,
// rebuilt so its "new" lodgements are made through the till's own Lodge (FROM-PRINCIPAL 117 and 120),
// not by posting a journal on the movement by hand. The leg proves the merged rule on the production
// facts: the till's emission (the till.movement.posted outbox row, written in the Lodge's own
// transaction) is what makes a lodgement carry its own journal, the GL consumer's pass keys and posts
// the DR 1000 / CR 1020 journal, the match of a carrying lodgement only confirms the line, and an
// older lodgement with no journal keeps today's match and posts the clearing journal itself. The
// bankrec service is wired the way cmd/core wires it: the till repository as the lodgement seam and
// the GL as the journal key reader.
//
// The leg lives in the external test package because the till imports bankrec for the production seam
// (internal/till/recon_seam.go), so an in-package bankrec test cannot import the till.
//
// The world's month is 2023-05. A real Lodge can only happen today (the movement row's created_at is
// the database's clock), so after each Lodge the leg places that lodgement's business instant (the
// emitted payload's occurred_at, the instant the consumer dates the journal by) into its month: the
// same hand dating the leg applies to its statement lines, its fiscal periods and its poster's clock.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The world helpers below are this file's own copies of the in-package integration helpers
// (intPool, cleanTables, rbcAccountID, aBranch, the fw1_04 year helpers, insertLine): the external
// test package cannot reach the in-package ones.

func lodgeLegCleanTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE bank_matches, bank_statement_lines, bank_statement_imports, bank_reconciliations RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	_, _ = pool.Exec(context.Background(), `
		UPDATE bank_accounts
		   SET cutover_date = NULL,
		       cutover_statement_balance_cents = NULL,
		       cutover_outstanding_journal_ids = '{}'::uuid[],
		       cutover_recorded_by = NULL,
		       cutover_recorded_at = NULL`)
}

func lodgeLegAccount(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&id); err != nil {
		t.Fatalf("rbc account: %v", err)
	}
	return id
}

func lodgeLegBranch(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded location: %v", err)
	}
	return id
}

func lodgeLegCleanYear(t *testing.T, pool *pgxpool.Pool, acct string, year int) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM bank_matches WHERE statement_line_id IN (SELECT id FROM bank_statement_lines WHERE bank_account_id = $1::uuid AND EXTRACT(YEAR FROM txn_date) = $2)`, []any{acct, year}},
		{`DELETE FROM bank_matches WHERE journal_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, []any{year}},
		{`DELETE FROM bank_matches WHERE target_ref_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, []any{year}},
		{`DELETE FROM bank_statement_lines WHERE bank_account_id = $1::uuid AND EXTRACT(YEAR FROM txn_date) = $2`, []any{acct, year}},
		{`DELETE FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2`, []any{acct, year}},
		{`UPDATE gl_journals SET status = 'DRAFT', reverses_journal_id = NULL WHERE fiscal_year = $1 OR reverses_journal_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, []any{year}},
		{`DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, []any{year}},
		{`DELETE FROM gl_journals WHERE fiscal_year = $1`, []any{year}},
		{`DELETE FROM gl_period_overrides o USING fiscal_periods p WHERE o.period_id = p.id AND p.year = $1 AND p.year != 2026`, []any{year}},
		{`DELETE FROM fiscal_periods WHERE year = $1 AND year != 2026`, []any{year}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("lodgeLegCleanYear(%d) query %q: %v", year, q.sql, err)
		}
	}
}

func lodgeLegEnsurePeriods(t *testing.T, pool *pgxpool.Pool, year int, at time.Time) {
	t.Helper()
	ctx := context.Background()
	for m := 1; m <= 12; m++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO fiscal_periods (year, month, status, created_at, updated_at)
			VALUES ($1, $2, 'OPEN', $3, $3)
			ON CONFLICT (year, month) DO UPDATE SET status = 'OPEN', closed_at = NULL, updated_at = $3`,
			year, m, at); err != nil {
			t.Fatalf("ensure period %d-%02d: %v", year, m, err)
		}
	}
}

func lodgeLegInsertLine(t *testing.T, pool *pgxpool.Pool, accountID string, date time.Time, desc, ref string, amount int64) string {
	t.Helper()
	hash := bankrec.DedupeHash(date, amount, desc, ref)
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'UNMATCHED') RETURNING id::text`,
		accountID, date, desc, ref, amount, hash).Scan(&id)
	if err != nil {
		t.Fatalf("insert line: %v", err)
	}
	return id
}

// lodgeLegPoster posts bankrec's clearing journals through the real GL, dating a journal that carries
// no document date by the leg's clock (the fw1_04 month end legs' poster shape).
type lodgeLegPoster struct {
	svc   *gl.Service
	clock func() time.Time
}

func (p lodgeLegPoster) PostJournal(ctx context.Context, ext gl.ExternalJournal) (string, error) {
	if ext.DocumentDate.IsZero() && p.clock != nil {
		ext.DocumentDate = p.clock()
	}
	return p.svc.PostExternal(ctx, ext)
}

func lodgeLegStaffCtx(userID, branchID string) context.Context {
	return staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
		UserID: userID, Tier: staffcontext.TierFrontline, Scope: staffcontext.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{branchID: true},
	})
}

func lodgeLegPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestIntegration_FW1_04_LodgementRuleNewInTransitAndClearedBesideOlderLodgement(t *testing.T) {
	pool := lodgeLegPool(t)
	ctx := context.Background()

	lodgeLegCleanTables(t, pool)
	acct := lodgeLegAccount(t, pool)
	branch := lodgeLegBranch(t, pool)

	const year = 2023
	may10 := time.Date(year, 5, 10, 0, 0, 0, 0, time.UTC)
	may20 := time.Date(year, 5, 20, 0, 0, 0, 0, time.UTC)
	may20Noon := may20.Add(12 * time.Hour)
	may30Noon := time.Date(year, 5, 30, 12, 0, 0, 0, time.UTC)
	june05 := time.Date(year, 6, 5, 14, 0, 0, 0, time.UTC)
	lodgeLegCleanYear(t, pool, acct, year)
	lodgeLegEnsurePeriods(t, pool, year, may10)
	t.Cleanup(func() { lodgeLegCleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	tillRepo := till.NewRepository(pool)
	tillSvc := till.NewService(tillRepo)

	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	caller := lodgeLegStaffCtx(userID, branch)

	// The drawer the two new lodgements are made on.
	sessionID, err := tillSvc.OpenTill(caller, till.OpenTillInput{BranchID: branch, Station: "FW104-LODGE-RULE", FloatCents: 0})
	if err != nil {
		t.Fatalf("open the drawer: %v", err)
	}
	t.Cleanup(func() {
		// Scoped to this leg's own rows: the shared gate database holds other legs' rows (the till
		// package's world cleans up by the same shape).
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (
			  SELECT j.id FROM gl_journals j WHERE j.idempotency_key IN (
			    SELECT 'till.movement.posted:' || m.id::text FROM till_movements m WHERE m.till_session_id = $1::uuid))`, sessionID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key IN (
			  SELECT 'till.movement.posted:' || m.id::text FROM till_movements m WHERE m.till_session_id = $1::uuid)`, sessionID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'till.movement.posted' AND payload->>'movement_id' IN (
			  SELECT m.id::text FROM till_movements m WHERE m.till_session_id = $1::uuid)`, sessionID)
		_, _ = pool.Exec(c, `DELETE FROM till_movements WHERE till_session_id = $1::uuid`, sessionID)
		_, _ = pool.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, sessionID)
	})

	// lodge makes one new lodgement through the till's own Lodge, then places the emission's business
	// instant in the leg's month (see the file comment) before the consumer's pass dates the journal
	// by it.
	lodge := func(ref, reason string, cents int64, at time.Time) string {
		t.Helper()
		if err := tillSvc.Lodge(caller, till.MovementInput{
			TillSessionID: sessionID, MovementType: "LODGEMENT", Method: "CASH",
			AmountCents: cents, Reason: reason, Reference: ref,
		}); err != nil {
			t.Fatalf("lodge %s: %v", ref, err)
		}
		var id string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND reference = $2`, sessionID, ref).Scan(&id); err != nil {
			t.Fatalf("find the %s movement: %v", ref, err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE events_outbox SET payload = jsonb_set(payload, '{occurred_at}', to_jsonb($2::text))
			  WHERE topic = 'till.movement.posted' AND payload->>'movement_id' = $1`, id, at.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("place the %s emission in the leg's month: %v", ref, err)
		}
		return id
	}
	newCleared := lodge("LODG-NEW", "FW1-04 new cleared lodgement", 40000, may20Noon)
	newTransit := lodge("LODG-TRN", "FW1-04 new in transit lodgement", 30000, may30Noon)

	// The older lodgement: a pre change movement row with no emission and no slip, so it carries no
	// journal of its own and its match keeps today's path.
	var oldMove string
	if err := pool.QueryRow(ctx, `
		INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference)
		VALUES ($1::uuid, 'LODGEMENT', 'CASH', 25000, 'FW1-04 old lodgement (pre change, no journal)', 'LODG-OLD')
		RETURNING id::text`, sessionID).Scan(&oldMove); err != nil {
		t.Fatalf("seed the old lodgement: %v", err)
	}

	// The GL consumer's pass, exactly as the worker runs it: the till.movement.posted rows become the
	// keyed DR 1000 / CR 1020 lodgement journals.
	disp := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(disp)
	for i := 0; i < 30; i++ {
		if disp.DrainOnce(ctx) == 0 {
			break
		}
	}
	for _, move := range []string{newCleared, newTransit} {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "till.movement.posted:"+move).Scan(&n); err != nil || n != 1 {
			t.Fatalf("the lodgement journal for movement %s: count %d (err %v), want the consumer's one keyed journal", move, n, err)
		}
	}

	// May's statement carries the old lodgement's line and the new cleared lodgement's line; the
	// transit lodgement is not on it.
	oldLine := lodgeLegInsertLine(t, pool, acct, may10, "MAY OLD LODGEMENT DEPOSIT", "LODG-OLD", 25000)
	newLine := lodgeLegInsertLine(t, pool, acct, may20, "MAY NEW LODGEMENT DEPOSIT", "LODG-NEW", 40000)

	svc := bankrec.NewService(bankrec.NewRepository(pool)).
		WithLodgements(tillRepo).
		WithJournals(glSvc).
		WithJournalPoster(lodgeLegPoster{svc: glSvc, clock: func() time.Time { return june05 }}).
		WithGLBalances(glSvc).
		WithJournalKeys(glSvc)

	// The older lodgement's match posts the clearing journal itself (DR 1000 / CR 1020, once).
	oldMatch, err := svc.ManualMatch(ctx, bankrec.MatchInput{
		Mode: "manual", StatementLineID: oldLine, TargetType: bankrec.TargetLodgement, TargetRefID: oldMove, AmountCents: 25000, CanAccessBranch: bankrec.AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("match older lodgement: %v", err)
	}
	if oldMatch.JournalID == "" {
		t.Errorf("older lodgement match posted no clearing journal")
	}

	// The new cleared lodgement carries its own journal (the till's emission), so its match only
	// confirms the line and books nothing.
	if _, err := svc.ManualMatch(ctx, bankrec.MatchInput{
		Mode: "manual", StatementLineID: newLine, TargetType: bankrec.TargetLodgement, TargetRefID: newCleared, AmountCents: 40000, CanAccessBranch: bankrec.AllBranches,
	}, ""); err != nil {
		t.Fatalf("match new cleared lodgement: %v", err)
	}

	// May closes at zero: statement 65000, one deposit in transit (the new transit lodgement's 30000),
	// and a ledger that holds each dollar once (25000 + 40000 + 30000 = 95000).
	mayRec, err := svc.OpenReconciliation(ctx, bankrec.ReconInput{BankAccountID: acct, Year: year, Month: 5, StatementBalanceCents: 65000})
	if err != nil {
		t.Fatalf("open may: %v", err)
	}
	if mayRec.DepositsInTransitCents != 30000 || len(mayRec.DepositsInTransit) != 1 {
		t.Errorf("May DepositsInTransitCents = %d (count %d), want 30000 (count 1 for the new transit lodgement)",
			mayRec.DepositsInTransitCents, len(mayRec.DepositsInTransit))
	}
	if mayRec.GLBalanceCents != 95000 || mayRec.AdjustedBankBalanceCents != 95000 || mayRec.VarianceCents != 0 {
		t.Errorf("May GL=%d Adjusted=%d Variance=%d, want 95000, 95000, 0",
			mayRec.GLBalanceCents, mayRec.AdjustedBankBalanceCents, mayRec.VarianceCents)
	}
	doneMay, err := svc.CompleteReconciliation(ctx, mayRec.ID, "")
	if err != nil {
		t.Fatalf("complete may with older + new cleared + new in-transit lodgements: %v", err)
	}
	if doneMay.Status != bankrec.ReconCompleted || doneMay.VarianceCents != 0 {
		t.Errorf("completed May = %+v, want COMPLETED with variance 0", doneMay)
	}
}
