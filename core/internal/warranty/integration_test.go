//go:build integration

// Integration tests run the real pgx Repository + a real gl.Service (so a warranty settlement actually posts to
// gl_journals) against a MIGRATED + SEEDED Postgres. Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hos_wty01?sslmode=disable' \
//	  go test -tags=integration ./internal/warranty/... -count=1 -p 1
//
// The target DB must be migrated (0001..0054) AND seeded (cmd/seed) so the vendor/customer masters + the
// checklist template + the demo claims exist. These tests exercise the real SQL: #WTY-n assignment continuing
// past the seeded WTY-1..WTY-5, the checklist gate over the real 0054 template, back-dated capture, and the
// settlement GL post.
package warranty

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	trentonBranch = "5d1b3f6e-0000-4000-9000-000000000001"
	demoOwner     = "d7000000-0000-4000-9000-000000000001"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hos_wty01?sslmode=disable"
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
	// Guard: the warranty schema + seed must be present.
	var ok bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'warranty_claims')`).Scan(&ok); err != nil || !ok {
		pool.Close()
		fail("integration DB missing warranty schema (migrate + seed hos_wty01 first): %v", err)
	}
	t.Cleanup(pool.Close)

	// - W5-B: FIXTURES RESTORE WHAT THEY TOUCH -
	// Every integration test here captures claims and most of them settle one. Before Wave 5 that residue was
	// merely untidy. It is not any more: a settled VENDOR_CREDIT now writes an ACCRUAL (0129), and the
	// WTY-RECOVERY board (DIB-221) is the SUM of every open accrual in the branch. Left alone, each run added a
	// few thousand dollars to a number a manager is meant to act on, the board would have read six figures of
	// phantom vendor debt, and the person who noticed would have been the one asking why the business is owed
	// $160k it has never been owed.
	//
	// The sweep is time-boxed to THIS test: rows created since the pool opened. Tests in a package run
	// sequentially, so that window contains only this test's writes; the seeded/demo claims (0054/0057) are
	// older and untouched. ON DELETE CASCADE carries the lines, events, postings, recovery actions, intake
	// photos and SLA alerts with the claim.
	started := time.Now()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `DELETE FROM warranty_claims WHERE created_at >= $1`, started); err != nil {
			t.Logf("warranty integration cleanup: %v", err)
		}
		// The 0131 limiter is keyed on a hash, so its rows are not claim-scoped and cascade nothing.
		if _, err := pool.Exec(ctx, `DELETE FROM warranty_intake_rate_limit WHERE updated_at >= $1`, started); err != nil {
			t.Logf("warranty intake rate-limit cleanup: %v", err)
		}
	})
	return pool
}

// realGLPoster wraps a real gl.Service (mirrors main.go's warrantyGLPoster).
type realGLPoster struct{ svc *gl.Service }

func (a realGLPoster) PostJournal(ctx context.Context, in GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

func realSvc(pool *pgxpool.Pool, caller identity.StaffContext) *Service {
	glSvc := gl.NewService(gl.NewRepository(pool))
	return NewService(NewRepository(pool)).
		WithGLPoster(realGLPoster{svc: glSvc}).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, caller.UserID != "" }).
		withClock(intClock)
}

// intClock is THE clock every realSvc in this file runs at, and the single instant every fixture date
// here must be derived from.
//
// DIB-427. A fixture whose dates come from the DATABASE's clock (now() - INTERVAL '…') while the
// service reading them runs at a fixed instant is a dated time bomb: the two drift apart one day per
// real day. It reaches the assertions through Reports() → buildReport(claims, s.clock()), whose
// buildOpenAges/wholeDays CLAMP a negative age to zero, so when the wall clock overtakes the pinned
// instant the ages silently read 0 instead of failing, and the test keeps passing while testing
// nothing. Fixture dates below are therefore parameterised on intClock(), not on now().
// (House pattern: internal/messagingprovider/collections_integration_test.go.)
func intClock() time.Time { return time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC) }

// staffCaller is a branch-scoped caller granted BOTH demo branches (Trenton + Kingston), so the integration
// captures/settles pass the branch-scope gate (the IDOR fix). The cross-branch DENY path is asserted separately
// in TestIntegration_CrossBranchForbidden.
func staffCaller(uid string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{trentonBranch: true, kingstonBranch: true}}
}

// kingstonBranch is the second demo branch (Kingston).
const kingstonBranch = "5d1b3f6e-0000-4000-9000-000000000002"

// globalCaller is a Tier-1 GLOBAL caller (reaches every branch), used to prove GLOBAL bypasses branch scoping.
func globalCaller(uid string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

// advanceToDecision walks a fresh claim through the real 0054 checklist gates to the DECISION phase, so a
// settlement is in-sequence (settlement is restricted to DECISION/SETTLEMENT, MINOR-1). Returns the claim id.
func advanceToDecision(t *testing.T, svc *Service, ctx context.Context, in ClaimInput) string {
	t.Helper()
	c, err := svc.CreateClaim(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tick := func(keys ...string) {
		for _, k := range keys {
			if _, err := svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true}); err != nil {
				t.Fatalf("tick %s: %v", k, err)
			}
		}
	}
	adv := func(to string) {
		if _, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: to}); err != nil {
			t.Fatalf("advance %s: %v", to, err)
		}
	}
	tick("contact_captured", "product_identified", "description_captured")
	adv(PhaseTriage)
	tick("owner_assigned", "vendor_identified", "coverage_reviewed", "proof_of_purchase")
	adv(PhaseSubmitted)
	tick("photos_attached", "proof_attached", "vendor_form_sent")
	adv(PhaseWaitingOnDecision)
	tick("submission_ack")
	adv(PhaseDecision)
	return c.ID
}

// I1: capture assigns the next #WTY-n continuing PAST the seeded back-dated set (WTY-1..WTY-5), gap-free, no
// collision with the migrated live claims (FR-2 + FR-9).
func TestIntegration_ClaimNoContinuesPastSeed(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	c, err := svc.CreateClaim(ctx, ClaimInput{
		BranchID: trentonBranch, Source: SourceIntake, Description: "integration capture",
		ExpectedCents: 30000, Lines: []ClaimLineInput{{ProductText: "test paver", Qty: "50", UOMCode: "SF"}},
	})
	if err != nil {
		t.Fatalf("create claim: %v", err)
	}
	// The seed plants WTY-1..WTY-5, so a fresh capture must be WTY-6 or higher (never a re-used number).
	var n int
	if _, err := fmt.Sscanf(c.ClaimNo, "WTY-%d", &n); err != nil {
		t.Fatalf("claim_no %q not #WTY-n: %v", c.ClaimNo, err)
	}
	if n < 6 {
		t.Fatalf("claim_no %s should continue past the seeded WTY-1..WTY-5", c.ClaimNo)
	}
	if len(c.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(c.Lines))
	}
}

// I2: the checklist gate is enforced over the REAL 0054 template, advancing INTAKE→TRIAGE is blocked until the
// 3 required INTAKE items are ticked (FR-3).
func TestIntegration_ChecklistGateOverRealTemplate(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	c, err := svc.CreateClaim(ctx, ClaimInput{BranchID: trentonBranch, Description: "gate test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Blocked before ticking.
	if _, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage}); status(err) != 422 {
		t.Fatalf("advance should be gated (422), got status %d (err %v)", status(err), err)
	}
	// Tick the 3 required INTAKE items from the real 0054 template.
	for _, k := range []string{"contact_captured", "product_identified", "description_captured"} {
		if _, err := svc.TickChecklist(ctx, c.ID, ChecklistTickInput{ItemKey: k, Checked: true}); err != nil {
			t.Fatalf("tick %s: %v", k, err)
		}
	}
	got, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseTriage})
	if err != nil {
		t.Fatalf("advance after ticking: %v", err)
	}
	if got.Phase != PhaseTriage {
		t.Fatalf("expected TRIAGE, got %s", got.Phase)
	}
}

// I3: a vendor-credit settlement posts a real, balanced journal to gl_journals and stamps it on the claim (FR-5
// GL hand-off seam, live).
func TestIntegration_SettlementPostsRealJournal(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	id := advanceToDecision(t, svc, ctx, ClaimInput{BranchID: trentonBranch, Description: "settle test", ExpectedCents: 80000})
	res, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, ExpectedCents: ptrI64(80000), RealizedCents: 76000})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if !res.GLPosted || res.GLJournalID == "" {
		t.Fatalf("vendor credit should post to GL; got %+v", res)
	}
	// The gl_journals row exists.
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gl_journals WHERE id = $1)`, res.GLJournalID).Scan(&exists); err != nil || !exists {
		t.Fatalf("gl_journal %s not found (err=%v)", res.GLJournalID, err)
	}
	// The claim carries the realized amount + journal id.
	var realized int64
	var journal string
	if err := pool.QueryRow(ctx, `SELECT realized_cents, COALESCE(gl_journal_id::text,'') FROM warranty_claims WHERE id = $1`, id).Scan(&realized, &journal); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if realized != 76000 || journal != res.GLJournalID {
		t.Fatalf("claim realized=%d journal=%s, want 76000 + %s", realized, journal, res.GLJournalID)
	}
}

// I4: back-dated capture (FR-9) persists the TRUE opened_at + a back-dated CREATED event.
func TestIntegration_BackDatedCapture(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	c, err := svc.CreateClaim(ctx, ClaimInput{BranchID: trentonBranch, Description: "historical", OpenedAt: "2026-02-01"})
	if err != nil {
		t.Fatalf("create back-dated: %v", err)
	}
	var opened time.Time
	if err := pool.QueryRow(ctx, `SELECT opened_at FROM warranty_claims WHERE id = $1`, c.ID).Scan(&opened); err != nil {
		t.Fatalf("read opened_at: %v", err)
	}
	// Compare in UTC: a bare "2026-02-01" is midnight UTC, which in a negative-offset local TZ reads as the
	// previous evening, the instant is correct, so we assert on the UTC calendar day.
	openedUTC := opened.UTC()
	if openedUTC.Year() != 2026 || openedUTC.Month() != time.February || openedUTC.Day() != 1 {
		t.Fatalf("opened_at not back-dated to 2026-02-01 UTC: %v", openedUTC)
	}
	// The CREATED event carries the same true past occurred_at.
	var evOccurred time.Time
	if err := pool.QueryRow(ctx, `SELECT occurred_at FROM warranty_claim_events WHERE claim_id = $1 AND event_type = 'CREATED'`, c.ID).Scan(&evOccurred); err != nil {
		t.Fatalf("read CREATED event: %v", err)
	}
	if evOccurred.UTC().Month() != time.February {
		t.Fatalf("CREATED event should be back-dated to Feb; got %v", evOccurred.UTC())
	}
}

// I5: reports compute over the seeded demo set, the funnel, win/loss, and exposure are non-trivial (proves the
// SQL projection + pure math line up on real data).
func TestIntegration_ReportsOverSeed(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	b, err := svc.Reports(ctx, "")
	if err != nil {
		t.Fatalf("reports: %v", err)
	}
	if len(b.Funnel) != len(KanbanColumns()) {
		t.Fatalf("funnel should have every column, got %d", len(b.Funnel))
	}
	// The seed includes a Permacon WIN (WTY-3) → at least one settled claim with a positive win somewhere.
	var anyWon bool
	for _, m := range b.ByManufacturer {
		if m.WonClaims > 0 {
			anyWon = true
		}
	}
	if !anyWon {
		t.Fatal("expected at least one won claim in the by-manufacturer report from the seed")
	}
	if b.TimeToSettlement.SettledCount < 1 {
		t.Fatal("expected at least one settled claim in time-to-settlement from the seed")
	}
}

// I6: CONCURRENT capture assigns distinct, collision-free #WTY-n numbers, no capture 500s on the claim_no
// UNIQUE constraint. Proves the advisory-lock serialization of number assignment (MAJOR-1).
func TestIntegration_ConcurrentCaptureNoCollision(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	const n = 12
	type result struct {
		no  string
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start // release all goroutines at once to maximise contention
			c, err := svc.CreateClaim(ctx, ClaimInput{BranchID: trentonBranch, Description: "concurrent capture"})
			results <- result{no: c.ClaimNo, err: err}
		}()
	}
	close(start)

	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("concurrent capture %d failed (advisory lock should prevent collisions): %v", i, r.err)
		}
		if seen[r.no] {
			t.Fatalf("duplicate claim_no assigned under concurrency: %s", r.no)
		}
		seen[r.no] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct claim numbers, got %d", n, len(seen))
	}
}

// I7: a back-dated terminal advance (FR-9 migration) persists the TRUE closed_at, so time-to-settlement/age stay
// honest for a claim resolved before go-live (MAJOR-2).
func TestIntegration_BackDatedClose(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	c, err := svc.CreateClaim(ctx, ClaimInput{BranchID: trentonBranch, Description: "historical close", OpenedAt: "2026-03-01"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Abandon (an ungated exit) as of a true past date between open and now.
	if _, err := svc.AdvancePhase(ctx, c.ID, PhaseTransitionInput{ToPhase: PhaseAbandoned, OccurredAt: "2026-04-10"}); err != nil {
		t.Fatalf("back-dated abandon: %v", err)
	}
	var closed time.Time
	if err := pool.QueryRow(ctx, `SELECT closed_at FROM warranty_claims WHERE id = $1`, c.ID).Scan(&closed); err != nil {
		t.Fatalf("read closed_at: %v", err)
	}
	cu := closed.UTC()
	if cu.Year() != 2026 || cu.Month() != time.April || cu.Day() != 10 {
		t.Fatalf("closed_at should be the back-dated 2026-04-10 UTC, not now(); got %v", cu)
	}
}

// I8: re-settling a claim is REFUSED (409), the claim record and GL ledger cannot diverge (MAJOR).
func TestIntegration_ReSettleRefused(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	id := advanceToDecision(t, svc, ctx, ClaimInput{BranchID: trentonBranch, Description: "re-settle guard", ExpectedCents: 50000})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 50000}); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	_, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeWriteOff, RealizedCents: 80000})
	if status(err) != 409 {
		t.Fatalf("a second settle must be refused with 409; got status %d (err %v)", status(err), err)
	}
	// The claim retains the first settlement (no divergence).
	var outcome string
	var realized int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(outcome,''), realized_cents FROM warranty_claims WHERE id = $1`, id).Scan(&outcome, &realized); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if outcome != OutcomeVendorCredit || realized != 50000 {
		t.Fatalf("claim must retain first settlement; got outcome=%s realized=%d", outcome, realized)
	}
}

// I9: CONCURRENT settle, the money-critical guard. N goroutines each settle ONE fresh claim with DIFFERENT
// outcomes/amounts simultaneously. Asserts (a) EXACTLY ONE wins (success) and the rest are refused with 409;
// (b) the claim row, the FR-7 reporting figure, and the GL journal ALL reflect the SAME single winning outcome;
// (c) exactly ONE warranty-settle:* journal exists and it BALANCES. This is the regression guard for the
// claim-vs-ledger divergence under double-submit (the atomic winner-election serializes the whole settle).
func TestIntegration_ConcurrentSettleSingleWinner(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	// One fresh claim, walked to the DECISION phase (settlement is restricted to DECISION/SETTLEMENT, MINOR-1),
	// then hammered by concurrent settles.
	claimID := advanceToDecision(t, svc, ctx, ClaimInput{BranchID: trentonBranch, Description: "concurrent settle", ExpectedCents: 50000})

	// N goroutines, each a DIFFERENT outcome + amount, released at once to maximise contention. A mix of
	// recovery (VENDOR_CREDIT → posts a journal) and expense (WRITE_OFF → posts a journal) outcomes so whichever
	// wins, the single journal must match THAT winner's amounts.
	type attempt struct {
		outcome  string
		realized int64
	}
	attempts := []attempt{
		{OutcomeVendorCredit, 50000},
		{OutcomeWriteOff, 80000},
		{OutcomeVendorCredit, 51000},
		{OutcomeWriteOff, 82000},
		{OutcomeVendorCredit, 52000},
		{OutcomeWriteOff, 83000},
		{OutcomeVendorCredit, 53000},
		{OutcomeWriteOff, 84000},
	}
	const n = 8 // ≥8 as required

	type result struct {
		res SettlementResult
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		a := attempts[i]
		go func() {
			<-start // release all goroutines at once
			res, err := svc.RecordSettlement(ctx, claimID, SettlementInput{Outcome: a.outcome, ExpectedCents: ptrI64(a.realized), RealizedCents: a.realized})
			results <- result{res: res, err: err}
		}()
	}
	close(start)

	// (a) EXACTLY ONE success; every other attempt is a 409 conflict (never a silent success, never a 500).
	var winners int
	var winner SettlementResult
	for i := 0; i < n; i++ {
		r := <-results
		if r.err == nil {
			winners++
			winner = r.res
			continue
		}
		if s := status(r.err); s != 409 {
			t.Fatalf("a losing concurrent settle must be refused with 409; got status %d (err %v)", s, r.err)
		}
	}
	if winners != 1 {
		t.Fatalf("EXACTLY ONE concurrent settle must win; got %d winners", winners)
	}

	// (b) The claim row reflects the winner's outcome + realized amount (this row IS the FR-7 reporting money).
	var claimOutcome string
	var claimRealized, claimExpected int64
	var claimJournal string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(outcome,''), realized_cents, expected_cents, COALESCE(gl_journal_id::text,'')
		 FROM warranty_claims WHERE id = $1`, claimID).Scan(&claimOutcome, &claimRealized, &claimExpected, &claimJournal); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if claimOutcome != winner.Outcome || claimRealized != winner.RealizedCents {
		t.Fatalf("claim row must reflect the winning settle: got outcome=%s realized=%d, want %s/%d",
			claimOutcome, claimRealized, winner.Outcome, winner.RealizedCents)
	}

	// The FR-7 reporting projection reads the SAME winning figure (proves claim + report agree, no divergence).
	// Read the claim back through the same reporting projection the FR-7 math consumes.
	var reportRealized int64
	var foundInReport bool
	for _, r := range mustReportClaims(t, pool, trentonBranch) {
		if r.ID == claimID {
			reportRealized = r.RealizedCents
			foundInReport = true
			if r.Outcome != winner.Outcome {
				t.Fatalf("FR-7 projection outcome %s disagrees with the winning settle %s", r.Outcome, winner.Outcome)
			}
		}
	}
	if !foundInReport {
		t.Fatalf("settled claim %s missing from the FR-7 projection", claimID)
	}
	if reportRealized != winner.RealizedCents {
		t.Fatalf("FR-7 reporting money %d disagrees with the winning settle %d (claim/report DIVERGED)", reportRealized, winner.RealizedCents)
	}

	// (c) EXACTLY ONE warranty-settle:* journal exists for this claim, it is the one stamped on the claim, and it
	// BALANCES with the winner's amount, the journal cannot reflect a different (losing) outcome's money.
	var journalCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, "warranty-settle:"+claimID).Scan(&journalCount); err != nil {
		t.Fatalf("count settle journals: %v", err)
	}
	if journalCount != 1 {
		t.Fatalf("EXACTLY ONE warranty-settle journal must exist; got %d", journalCount)
	}
	// The winner posts a journal (both VENDOR_CREDIT and WRITE_OFF post one for a positive realized amount).
	if !winner.GLPosted || winner.GLJournalID == "" {
		t.Fatalf("the winning settle should have posted a GL journal; got %+v", winner)
	}
	if claimJournal != winner.GLJournalID {
		t.Fatalf("claim must carry the winner's journal id; got %s, want %s", claimJournal, winner.GLJournalID)
	}
	// The journal balances at the winner's realized amount (DR total == CR total == realized).
	var dr, cr int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(debit_cents),0), COALESCE(SUM(credit_cents),0)
		 FROM gl_journal_lines WHERE journal_id = $1`, winner.GLJournalID).Scan(&dr, &cr); err != nil {
		t.Fatalf("read journal lines: %v", err)
	}
	if dr != cr || dr != winner.RealizedCents {
		t.Fatalf("the settle journal must balance at the winner's realized amount %d; got dr=%d cr=%d",
			winner.RealizedCents, dr, cr)
	}
}

// mustReportClaims reads the claims back through the same repository projection the FR-7 math consumes.
func mustReportClaims(t *testing.T, pool *pgxpool.Pool, branchID string) []ReportClaim {
	t.Helper()
	rcs, err := NewRepository(pool).AllClaimsForReport(context.Background(), branchID)
	if err != nil {
		t.Fatalf("report claims projection: %v", err)
	}
	return rcs
}

// status extracts the HTTP status from a clientError (used by the gate assertions).
func status(err error) int {
	if err == nil {
		return 0
	}
	var ce clientError
	if errors.As(err, &ce) {
		return ce.status
	}
	return 500
}

// I10 (PUT-500 root cause): upserting a warranty profile for an UNKNOWN vendor (empty registry / typo'd id)
// must be a CLIENT error (422), NOT a raw 500. Previously the vendor_id FK violation (23503) surfaced as an
// unmapped 500, the exact staging PUT /warranty/vendors/{id} failure. A PUT against a REAL (seeded) vendor
// still succeeds. Guards the demo-gap PUT-500 fix.
func TestIntegration_UpsertUnknownVendorIs422(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	// A well-formed UUID that is NOT a vendor → FK violation → must map to 422 (not 500).
	_, err := svc.UpsertVendorProfile(ctx, VendorProfileInput{VendorID: "00000000-0000-4000-9000-0000000000ff", Status: "DOCUMENTED"})
	if status(err) != 422 {
		t.Fatalf("PUT for an unknown vendor must be 422 (was the 500 root cause); got status %d (err %v)", status(err), err)
	}
	// A non-UUID path segment → invalid_text_representation (22P02) → also 422, never 500.
	if _, err := svc.UpsertVendorProfile(ctx, VendorProfileInput{VendorID: "not-a-uuid", Status: "DOCUMENTED"}); status(err) != 422 {
		t.Fatalf("PUT for a non-UUID vendor must be 422; got status %d (err %v)", status(err), err)
	}

	// A PUT against a REAL seeded vendor succeeds (round-trips the profile).
	var realVendor string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors LIMIT 1`).Scan(&realVendor); err != nil {
		t.Fatalf("read a real vendor: %v", err)
	}
	p, err := svc.UpsertVendorProfile(ctx, VendorProfileInput{VendorID: realVendor, Status: "DOCUMENTED", Coverage: "test coverage", Channel: "EMAIL"})
	if err != nil {
		t.Fatalf("PUT for a real vendor must succeed; got %v", err)
	}
	if p.VendorID != realVendor || p.Status != "DOCUMENTED" {
		t.Fatalf("upsert should round-trip; got %+v", p)
	}
}

// I11 (IDOR): a branch-scoped caller cannot READ or MUTATE a claim in a branch they cannot access; a GLOBAL
// caller reaches it. Uses a Kingston claim + a Trenton-only caller.
func TestIntegration_CrossBranchForbidden(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	// Create a Kingston claim as a fully-scoped caller.
	full := realSvc(pool, staffCaller(demoOwner))
	kc, err := full.CreateClaim(ctx, ClaimInput{BranchID: kingstonBranch, Description: "kingston claim (idor)"})
	if err != nil {
		t.Fatalf("create kingston claim: %v", err)
	}

	// A caller scoped ONLY to Trenton must be forbidden from reading it.
	trentonOnly := realSvc(pool, identity.StaffContext{
		UserID: demoOwner, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: "ACTIVE", Branches: map[string]bool{trentonBranch: true},
	})
	if _, err := trentonOnly.GetClaim(ctx, kc.ID); status(err) != 403 {
		t.Fatalf("a Trenton-only caller must be forbidden (403) from a Kingston claim; got status %d (err %v)", status(err), err)
	}
	// And forbidden from mutating it.
	if _, err := trentonOnly.AssignClaim(ctx, kc.ID, "", demoOwner); status(err) != 403 {
		t.Fatalf("a Trenton-only caller must be forbidden from mutating a Kingston claim; got status %d", status(err))
	}
	// A GLOBAL caller reaches it.
	if _, err := realSvc(pool, globalCaller(demoOwner)).GetClaim(ctx, kc.ID); err != nil {
		t.Fatalf("a GLOBAL caller must reach a Kingston claim; got %v", err)
	}
}

// I12 (settled-exit / FR-7): a claim SETTLED with a real recovery cannot be diverted to ABANDONED/REJECTED, it
// can only close forward. Guards the FR-7 corruption where a settled recovery would be reported LOST.
func TestIntegration_SettledClaimCannotExit(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	id := advanceToDecision(t, svc, ctx, ClaimInput{BranchID: trentonBranch, Description: "settled-exit guard", ExpectedCents: 100000})
	if _, err := svc.RecordSettlement(ctx, id, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 95000}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if _, err := svc.AdvancePhase(ctx, id, PhaseTransitionInput{ToPhase: PhaseAbandoned}); status(err) != 422 {
		t.Fatalf("a settled claim must NOT be advanceable to ABANDONED; got status %d (err %v)", status(err), err)
	}
	if _, err := svc.AdvancePhase(ctx, id, PhaseTransitionInput{ToPhase: PhaseRejected}); status(err) != 422 {
		t.Fatalf("a settled claim must NOT be advanceable to REJECTED; got status %d", status(err))
	}
	// It stays settled with its recovery intact (never a LOST exit).
	var phase, outcome string
	var realized int64
	if err := pool.QueryRow(ctx, `SELECT phase, COALESCE(outcome,''), realized_cents FROM warranty_claims WHERE id = $1`, id).Scan(&phase, &outcome, &realized); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if outcome != OutcomeVendorCredit || realized != 95000 || phase == PhaseAbandoned || phase == PhaseRejected {
		t.Fatalf("settled claim must retain its recovery, not exit LOST; got phase=%s outcome=%s realized=%d", phase, outcome, realized)
	}
}

// I14 (MONEY-CRITICAL, CONCURRENT TOCTOU regression): races advance(ABANDONED) vs settle(VENDOR_CREDIT) on a
// DECISION claim N times. I12 asserts the invariant SEQUENTIALLY (settle-then-advance is refused); this asserts
// it under CONCURRENCY, where the old code stranded the claim phase=ABANDONED(LOST) WITH outcome=VENDOR_CREDIT +
// realized $ + a POSTED GL journal (a recovery reported as a loss). Both operations now SELECT … FOR UPDATE the
// claim row and re-check the mutual-exclusion guard under the lock, so exactly ONE terminal decision wins and the
// claim is NEVER terminal-LOST-with-a-recovery, and an ABANDONED claim NEVER carries a posted settlement journal.
//
// Fails against the pre-fix code (unconditional UpdateClaimPhase + a Go-side-only settle pre-check); passes after.
func TestIntegration_ConcurrentAdvanceExitVsSettle(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	const n = 16 // ≥12 as required, each iteration is a fresh DECISION claim raced by two goroutines
	for i := 0; i < n; i++ {
		claimID := advanceToDecision(t, svc, ctx, ClaimInput{
			BranchID: trentonBranch, Description: fmt.Sprintf("toctou race #%d", i), ExpectedCents: 100000,
		})

		// Two goroutines, released at once: one drives the claim to a terminal-LOST exit (ABANDONED), the other
		// settles it with a real recovery (VENDOR_CREDIT, realized 99000). Under the fix they serialize on the row
		// lock, whichever commits first, the other refuses.
		type outcome struct {
			what string // "advance" | "settle"
			err  error
			res  SettlementResult
		}
		out := make(chan outcome, 2)
		start := make(chan struct{})
		go func() {
			<-start
			_, err := svc.AdvancePhase(ctx, claimID, PhaseTransitionInput{ToPhase: PhaseAbandoned})
			out <- outcome{what: "advance", err: err}
		}()
		go func() {
			<-start
			res, err := svc.RecordSettlement(ctx, claimID, SettlementInput{Outcome: OutcomeVendorCredit, RealizedCents: 99000})
			out <- outcome{what: "settle", err: err, res: res}
		}()
		close(start)

		var settleErr, advanceErr error
		var settleRes SettlementResult
		for k := 0; k < 2; k++ {
			r := <-out
			switch r.what {
			case "settle":
				settleErr, settleRes = r.err, r.res
			case "advance":
				advanceErr = r.err
			}
		}

		// A losing operation must be REFUSED cleanly with a 4xx, never a 500 and never a silent double-success. At
		// most one of the two may succeed. A losing settle is 409 (lost the row-lock race, saw the terminal phase
		// under the lock) OR 422 (its stale-read pre-check already saw the committed terminal phase), both post
		// nothing. A losing advance-to-exit is 422 (the under-lock guard saw the committed settlement outcome).
		if advanceErr == nil && settleErr == nil {
			t.Fatalf("iter %d: BOTH advance(ABANDONED) and settle(VENDOR_CREDIT) succeeded, the TOCTOU race stranded a settled recovery on an abandoned claim", i)
		}
		if settleErr != nil && status(settleErr) != 409 && status(settleErr) != 422 {
			t.Fatalf("iter %d: a losing settle must be a 4xx (409/422); got status %d (err %v)", i, status(settleErr), settleErr)
		}
		if advanceErr != nil && status(advanceErr) != 422 {
			t.Fatalf("iter %d: a losing advance-to-exit must be 422; got status %d (err %v)", i, status(advanceErr), advanceErr)
		}

		// THE INVARIANT: read the committed claim row. It must NEVER be terminal-LOST (ABANDONED/REJECTED) WHILE
		// carrying a recovery outcome + realized $. Either it settled (outcome set, phase still DECISION) or it
		// abandoned (phase=ABANDONED, outcome empty, realized 0), never both.
		var phase, dbOutcome string
		var realized int64
		var journal string
		if err := pool.QueryRow(ctx,
			`SELECT phase, COALESCE(outcome,''), realized_cents, COALESCE(gl_journal_id::text,'')
			 FROM warranty_claims WHERE id = $1`, claimID).Scan(&phase, &dbOutcome, &realized, &journal); err != nil {
			t.Fatalf("iter %d: read claim: %v", i, err)
		}
		terminalLost := phase == PhaseAbandoned || phase == PhaseRejected
		hasRecovery := dbOutcome != "" || realized != 0
		if terminalLost && hasRecovery {
			t.Fatalf("iter %d: STRANDED LEDGER, claim is terminal-LOST (%s) yet carries a recovery (outcome=%s realized=%d journal=%s)",
				i, phase, dbOutcome, realized, journal)
		}

		// An ABANDONED claim must carry NO posted settlement journal (no money moved on a lost claim). Assert both
		// the stamped id is empty AND no warranty-settle:* journal was posted for it.
		if terminalLost {
			if journal != "" {
				t.Fatalf("iter %d: an abandoned claim must carry NO settlement journal; got %s", i, journal)
			}
			var journalCount int
			if err := pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1`, "warranty-settle:"+claimID).Scan(&journalCount); err != nil {
				t.Fatalf("iter %d: count settle journals: %v", i, err)
			}
			if journalCount != 0 {
				t.Fatalf("iter %d: an abandoned claim must have NO posted settlement journal; got %d", i, journalCount)
			}
		}

		// If the settle won, the winner's result + the row + its journal must all agree (no half-committed state).
		if settleErr == nil {
			if settleRes.Outcome != OutcomeVendorCredit || settleRes.RealizedCents != 99000 {
				t.Fatalf("iter %d: winning settle result malformed: %+v", i, settleRes)
			}
			if dbOutcome != OutcomeVendorCredit || realized != 99000 {
				t.Fatalf("iter %d: settle won but the claim row disagrees: outcome=%s realized=%d", i, dbOutcome, realized)
			}
			if terminalLost {
				t.Fatalf("iter %d: settle won yet the claim is terminal-LOST (%s), divergence", i, phase)
			}
		}
	}
}

// I13 (demo-seed): the 0057 demo seed is present AND idempotent, re-applying its guarded body inserts nothing
// (the WTY-D1 guard + ON CONFLICT clauses hold), so a re-run never duplicates the registry or the backlog.
func TestIntegration_DemoSeedIdempotent(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	// The demo backlog is present (migration 0057 ran), at least the 6 profiles + the WTY-D* claims.
	var profiles, demoClaims int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM warranty_vendor_profiles`).Scan(&profiles); err != nil {
		t.Fatalf("count profiles: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM warranty_claims WHERE claim_no LIKE 'WTY-D%'`).Scan(&demoClaims); err != nil {
		t.Fatalf("count demo claims: %v", err)
	}
	if profiles < 6 {
		t.Fatalf("0057 must seed at least 6 vendor profiles; got %d", profiles)
	}
	if demoClaims < 6 {
		t.Fatalf("0057 must seed a 6-8 claim demo backlog; got %d WTY-D* claims", demoClaims)
	}

	// Re-apply the guarded seed body: the WTY-D1 guard makes the claim block a no-op, and the ON CONFLICT
	// clauses make the profile/vendor/customer inserts no-ops. Counts must be UNCHANGED.
	body, err := os.ReadFile("../../migrations/0057_warranty_demo_seed.sql")
	if err != nil {
		t.Fatalf("read migration 0057: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("re-applying 0057 must be idempotent (no error); got %v", err)
	}
	var profiles2, demoClaims2 int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM warranty_vendor_profiles`).Scan(&profiles2)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM warranty_claims WHERE claim_no LIKE 'WTY-D%'`).Scan(&demoClaims2)
	if profiles2 != profiles || demoClaims2 != demoClaims {
		t.Fatalf("re-applying 0057 must not change counts; profiles %d→%d, demo claims %d→%d", profiles, profiles2, demoClaims, demoClaims2)
	}

	// The FR-7 report over the seed is non-trivial: at least one WON claim (a VENDOR_CREDIT recovery).
	b, err := realSvc(pool, globalCaller(demoOwner)).Reports(ctx, "")
	if err != nil {
		t.Fatalf("reports: %v", err)
	}
	var won int
	for _, m := range b.ByManufacturer {
		won += m.WonClaims
	}
	if won < 1 {
		t.Fatalf("the FR-7 report over the demo seed must show at least one won claim; got %d", won)
	}
}

// FR-7 SEED COHERENCE (L8 round 5, MONEY-CRITICAL): a terminal-LOST-exit claim (ABANDONED/REJECTED) must never
// carry a settlement outcome or realized recovery, that is the runtime settled-exit guard's invariant, and a
// seed that inserts such a row directly BYPASSES it and corrupts the by-manufacturer realized-$ totals (recovery
// attributed to a vendor with zero wins). Migration 0057's coherence clause (section 6) neutralizes any such row
// on migrate. This test injects the exact corrupt state the live DB carries, re-applies 0057, and asserts the
// row is cleaned + the by-manufacturer report attributes zero realized $ to the corrupt vendor. WITHOUT the 0057
// clause the incoherent row survives and this fails.
func TestIntegration_SeedCoherence_NoLostClaimCarriesSettlement(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	// A well-formed vendor to attribute the corrupt recovery to (Techo Bloc from the 0057 demo vendors).
	var techoID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE vendor_no = 'V-TECHO'`).Scan(&techoID); err != nil {
		t.Fatalf("resolve Techo vendor: %v", err)
	}
	var custID, branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE account_no = 'WTY-QUINTE'`).Scan(&custID); err != nil {
		t.Fatalf("resolve demo customer: %v", err)
	}
	branchID = trentonBranch

	// Inject the corrupt state: ABANDONED (terminal LOSS) yet carrying VENDOR_CREDIT + realized recovery. The
	// closed_at CHECK requires a close timestamp on a terminal-exit phase, so set it (that is precisely why the
	// runtime settled-exit guard exists, a direct INSERT bypasses the Go guard, not the column CHECK).
	const corruptNo = "WTY-COHERE"
	_, _ = pool.Exec(ctx, `DELETE FROM warranty_claims WHERE claim_no = $1`, corruptNo)
	if _, err := pool.Exec(ctx, `
		INSERT INTO warranty_claims (claim_no, customer_id, job_address, vendor_id, branch_id, phase, source,
			description, outcome, expected_cents, realized_cents, opened_at, closed_at)
		VALUES ($1, $2, '{}', $3, $4, 'ABANDONED', 'INTAKE', 'injected corrupt terminal-LOST+settlement row',
			'VENDOR_CREDIT', 100000, 87700, $5::timestamptz - INTERVAL '30 days', $5::timestamptz - INTERVAL '5 days')`,
		corruptNo, custID, techoID, branchID, intClock()); err != nil {
		t.Fatalf("inject corrupt row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM warranty_claims WHERE claim_no = $1`, corruptNo)
	})

	// Pre-condition: the corrupt row IS incoherent before the guard runs.
	var incoherentBefore int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM warranty_claims
		WHERE phase IN ('ABANDONED','REJECTED') AND (outcome IS NOT NULL OR realized_cents <> 0)`).Scan(&incoherentBefore)
	if incoherentBefore == 0 {
		t.Fatal("test setup failed: the injected corrupt row should be incoherent before the guard runs")
	}

	// Re-apply 0057 (idempotent), its coherence clause must clean every terminal-LOST claim's settlement fields.
	body, err := os.ReadFile("../../migrations/0057_warranty_demo_seed.sql")
	if err != nil {
		t.Fatalf("read migration 0057: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("re-applying 0057 must not error: %v", err)
	}

	var incoherentAfter int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM warranty_claims
		WHERE phase IN ('ABANDONED','REJECTED') AND (outcome IS NOT NULL OR realized_cents <> 0)`).Scan(&incoherentAfter); err != nil {
		t.Fatalf("count incoherent: %v", err)
	}
	if incoherentAfter != 0 {
		t.Fatalf("after 0057, no terminal-LOST claim may carry a settlement outcome/realized; got %d incoherent", incoherentAfter)
	}

	// And the FR-7 read is coherent end-to-end: the by-manufacturer report attributes zero realized $ to the
	// vendor from a purely-lost claim (the corrupt row is Techo's only claim in this injected state's lineage).
	b, err := realSvc(pool, globalCaller(demoOwner)).Reports(ctx, "")
	if err != nil {
		t.Fatalf("reports: %v", err)
	}
	for _, m := range b.ByManufacturer {
		if m.WonClaims == 0 && m.RealizedCents != 0 {
			t.Fatalf("vendor %s has 0 wins but %d realized cents, realized $ must never appear without a win",
				m.VendorName, m.RealizedCents)
		}
	}
}

// I15 (repository.go InsertClaim FK→422): capturing a claim whose vendor_id is a well-formed UUID that is NOT a
// real vendor trips the vendor_id FK (23503). That is an unknown-reference CLIENT error, not a 500, it must map
// to 422, mirroring the sibling UpsertVendorProfile path. Fails against the pre-fix code (plain fmt.Errorf → 500).
func TestIntegration_CreateClaimUnknownVendorIs422(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	// A well-formed UUID that is NOT a vendor → vendor_id FK violation → must be 422 (not the old 500).
	_, err := svc.CreateClaim(ctx, ClaimInput{
		BranchID: trentonBranch, Description: "bad vendor capture", VendorID: "00000000-0000-4000-9000-0000000000fe",
	})
	if status(err) != 422 {
		t.Fatalf("capture with an unknown vendor_id must be 422 (not a raw 500); got status %d (err %v)", status(err), err)
	}

	// A capture with a REAL (seeded) vendor still succeeds, the mapping doesn't reject valid references.
	var realVendor string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors LIMIT 1`).Scan(&realVendor); err != nil {
		t.Fatalf("read a real vendor: %v", err)
	}
	if _, err := svc.CreateClaim(ctx, ClaimInput{
		BranchID: trentonBranch, Description: "good vendor capture", VendorID: realVendor,
	}); err != nil {
		t.Fatalf("capture with a real vendor must succeed; got %v", err)
	}
}

// I16 (repository.go UpdateClaimVendorOwner FK→422): assigning a claim to a well-formed UUID that is NOT a real
// vendor trips the vendor_id FK (23503) on the UPDATE. That is an unknown-reference CLIENT error, not a 500, it
// must map to 422, mirroring the sibling UpsertVendorProfile path. Fails against the pre-fix code (500).
func TestIntegration_AssignClaimUnknownVendorIs422(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool, staffCaller(demoOwner))

	// A valid claim (vendor UNKNOWN pending triage) to assign against.
	c, err := svc.CreateClaim(ctx, ClaimInput{BranchID: trentonBranch, Description: "assign-fk guard"})
	if err != nil {
		t.Fatalf("create claim: %v", err)
	}

	// Assign to a well-formed UUID that is NOT a vendor → vendor_id FK violation → must be 422 (not the old 500).
	_, err = svc.AssignClaim(ctx, c.ID, "00000000-0000-4000-9000-0000000000fd", demoOwner)
	if status(err) != 422 {
		t.Fatalf("assign to an unknown vendor must be 422 (not a raw 500); got status %d (err %v)", status(err), err)
	}

	// Assign to a REAL (seeded) vendor still succeeds, the mapping doesn't reject valid references.
	var realVendor string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors LIMIT 1`).Scan(&realVendor); err != nil {
		t.Fatalf("read a real vendor: %v", err)
	}
	if _, err := svc.AssignClaim(ctx, c.ID, realVendor, demoOwner); err != nil {
		t.Fatalf("assign to a real vendor must succeed; got %v", err)
	}
}
