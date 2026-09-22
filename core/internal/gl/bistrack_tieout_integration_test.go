//go:build integration

// M2-C1 proving integration test, "GL reconciled against static BisTrack test data" (roadmap M2 gate
// criterion C1, due 2026-07-31; the static-snapshot fallback while live BisTrack access is blocked on
// Q-045/046/047, decision_log D-031).
//
// It runs the REAL posting path (PostExternal, exactly the legs the ERP's posting rules produce for an
// invoice / receipt / till over-short / expense accrual), computes the ERP TrialBalance, then reconciles it
// against the static BisTrack GL snapshot via Service.BisTrackTieOut and asserts ZERO material discrepancy.
// A negative test perturbs one transaction and asserts the tie-out REPORTS the discrepancy, proving the
// reconciliation is genuine, not a tautology.
//
// The canonical fixture (testdata/bistrack_gl_snapshot_2026-06.json) is anchored to FY2026-06. To stay off
// the seeded 2026 periods, the test loads that fixture, re-anchors it to an isolated 209x test year in a temp
// file, and posts the scenario there, so the fixture ARTIFACT stays production-shaped while the test runs in
// its own sandbox year (the same isolation pattern the rest of the suite uses via ensurePeriods).
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable' \
//	  go test -tags=integration ./internal/gl/... -count=1 -p 1
package gl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// bistrackScenarioMonth is the fiscal month the M2-C1 scenario posts into (matches the fixture's period).
const bistrackScenarioMonth = 6

// aTestBranch creates a throwaway BRANCH location (branch_id = own id) and returns its UUID. The M2-C1
// scenario posts into, and the tie-out reconciles against, THIS branch only, so the inception-to-date
// closing balance isolates the scenario from the seeded demo branch and any other test's postings (an
// inception-to-date consolidated tie-out sums ALL history across ALL branches; branch scoping is how the test
// gets a clean, deterministic closing balance). Cleaned up after the test.
func aTestBranch(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var id string
	code := fmt.Sprintf("M2C1-%d", time.Now().UnixNano())
	// A BRANCH row must have branch_id = its OWN id (loc_type='BRANCH' ⇒ parent_id NULL, branch is self), and the
	// fk_locations_branch FK is checked at INSERT, so generate the UUID once and set both id and branch_id to it
	// in the same row (a post-hoc UPDATE can't satisfy an FK the INSERT already violates).
	if err := pool.QueryRow(ctx,
		`WITH new_id AS (SELECT gen_random_uuid() AS uid)
		 INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
		 SELECT uid, uid, NULL, 'BRANCH', $1, 'M2-C1 tie-out sandbox' FROM new_id
		 RETURNING id::text`, code).Scan(&id); err != nil {
		t.Fatalf("create test branch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.branch_id=$1::uuid`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE branch_id=$1::uuid`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id=$1::uuid`, id)
	})
	return id
}

// writeReanchoredFixture loads the canonical static snapshot, re-anchors it to (year, month) so the scenario
// can post into an isolated test year, and writes it to a temp file. Balances/crosswalk are untouched.
func writeReanchoredFixture(t *testing.T, year, month int) string {
	t.Helper()
	snap, err := LoadBisTrackSnapshot("") // default canonical fixture
	if err != nil {
		t.Fatalf("load canonical fixture: %v", err)
	}
	snap.FiscalYear = year
	snap.FiscalMonth = month
	snap.PeriodLabel = fmt.Sprintf("%04d-%02d", year, month)
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal re-anchored fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bistrack_snapshot_test.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write re-anchored fixture: %v", err)
	}
	return path
}

// postBisTrackScenario posts the deterministic M2-C1 scenario through the REAL GL posting path. Each journal
// uses exactly the account codes + legs the ERP's posting rules produce for that business event, so the
// resulting TrialBalance reproduces the static BisTrack closing balances. Amounts are in cents.
//
//  1. Invoice finalized: subtotal 1,000,000 + HST 13% 130,000 = total 1,130,000
//     DR 1100 AR 1,130,000 / CR 4000 Revenue 1,000,000 / CR 2200 HST 130,000
//  2. Payment received (cash/cheque → 1020 undeposited): 500,000
//     DR 1020 Undeposited 500,000 / CR 1100 AR 500,000
//  3. Till session over/short (short 2,500): DR 6350 Cash Over/Short 2,500 / CR 1020 Undeposited 2,500
//  4. Rent expense accrual: DR 6100 Rent 250,000 / CR 2100 AP 250,000
//
// Closing net-debit by account: 1020 +497,500 · 1100 +630,000 · 2100 −250,000 · 2200 −130,000 ·
// 4000 −1,000,000 · 6100 +250,000 · 6350 +2,500 (Σ = 0, balanced).
func postBisTrackScenario(t *testing.T, svc *Service, branchID, uniq string) {
	t.Helper()
	ctx := context.Background()
	posts := []struct {
		key   string
		memo  string
		lines []ExternalLine
	}{
		{"inv", "Invoice finalized", []ExternalLine{
			{AccountCode: "1100", DebitCents: 1_130_000, Memo: "AR"},
			{AccountCode: "4000", CreditCents: 1_000_000, Memo: "revenue"},
			{AccountCode: "2200", CreditCents: 130_000, Memo: "HST"},
		}},
		{"pay", "Payment received", []ExternalLine{
			{AccountCode: "1020", DebitCents: 500_000, Memo: "undeposited receipt"},
			{AccountCode: "1100", CreditCents: 500_000, Memo: "AR settled"},
		}},
		{"till", "Till over/short", []ExternalLine{
			{AccountCode: "6350", DebitCents: 2_500, Memo: "cash short"},
			{AccountCode: "1020", CreditCents: 2_500, Memo: "undeposited reduced"},
		}},
		{"rent", "Rent accrual", []ExternalLine{
			{AccountCode: "6100", DebitCents: 250_000, Memo: "rent"},
			{AccountCode: "2100", CreditCents: 250_000, Memo: "AP"},
		}},
	}
	for _, p := range posts {
		if _, err := svc.PostExternal(ctx, ExternalJournal{
			Source:         SourceAdjustment,
			BranchID:       branchID,
			IdempotencyKey: fmt.Sprintf("m2c1-%s-%s", p.key, uniq),
			Memo:           p.memo,
			Lines:          p.lines,
		}); err != nil {
			t.Fatalf("post %s: %v", p.key, err)
		}
	}
}

// M2-C1 (positive), the deterministic scenario, run through the real posting rules, reconciles against the
// static BisTrack snapshot with ZERO material discrepancy: every account ties, the grand delta is 0, and the
// report certifies ties_within_tolerance at tolerance 0 (exact).
func TestIntegration_M2C1_BisTrackTieOut_ZeroDiscrepancy(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	year := 2091
	ensurePeriods(t, pool, year)
	fixture := writeReanchoredFixture(t, year, bistrackScenarioMonth)

	branch := aTestBranch(t, pool)
	at := time.Date(year, bistrackScenarioMonth, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)
	postBisTrackScenario(t, svc, branch, fmt.Sprintf("%d", time.Now().UnixNano()))

	// Reconcile THIS branch's inception-to-date closing balance (isolated from the seeded demo branch).
	rep, err := svc.BisTrackTieOut(ctx, year, bistrackScenarioMonth, branch, fixture, 0)
	if err != nil {
		t.Fatalf("bistrack tie-out: %v", err)
	}

	if !rep.TiesWithinTol {
		t.Fatalf("M2-C1: expected exact tie-out, got %d discrepancies: %+v", len(rep.Discrepancies), rep.Discrepancies)
	}
	if rep.GrandDeltaCents != 0 {
		t.Fatalf("M2-C1: grand delta must be 0, got %d", rep.GrandDeltaCents)
	}
	if len(rep.Discrepancies) != 0 {
		t.Fatalf("M2-C1: expected no discrepancies, got %+v", rep.Discrepancies)
	}
	// The ERP books balance (double-entry invariant) AND the export balances → both net-debit totals are 0.
	if rep.TotalERPCents != 0 {
		t.Fatalf("M2-C1: ERP net-debit total should be 0 (balanced books), got %d", rep.TotalERPCents)
	}
	if rep.TotalBisTrackCents != 0 {
		t.Fatalf("M2-C1: BisTrack net-debit total should be 0 (balanced export), got %d", rep.TotalBisTrackCents)
	}
	// Every fixture account must be reconciled from BOTH sides (no ERP_ONLY / BISTRACK_ONLY drift).
	for _, ln := range rep.Lines {
		if ln.Source != "BOTH" {
			t.Fatalf("M2-C1: account %s reconciled from %s, expected BOTH (delta=%d)", ln.AccountCode, ln.Source, ln.DeltaCents)
		}
		if ln.DeltaCents != 0 {
			t.Fatalf("M2-C1: account %s out of tie by %d (erp=%d bistrack=%d)", ln.AccountCode, ln.DeltaCents, ln.ERPCents, ln.BisTrackCents)
		}
	}
	// Spot-check the natural-orientation presentation on one debit-normal + one credit-normal account.
	for _, ln := range rep.Lines {
		switch ln.AccountCode {
		case "1100": // AR, debit-normal → +630,000
			if ln.ERPCents != 630_000 || ln.NormalBalance != NormalDebit {
				t.Fatalf("1100 AR should read +630000 debit-normal, got %d/%s", ln.ERPCents, ln.NormalBalance)
			}
		case "4000": // Revenue, credit-normal → +1,000,000 in natural orientation
			if ln.ERPCents != 1_000_000 || ln.NormalBalance != NormalCredit {
				t.Fatalf("4000 Revenue should read +1000000 credit-normal, got %d/%s", ln.ERPCents, ln.NormalBalance)
			}
		}
	}
}

// REGRESSION (period-basis MAJOR), a balance-sheet account with a PRIOR-period opening balance must still tie
// out. The BisTrack fixture is a cumulative CLOSING trial balance, so the ERP side must be reconciled
// inception-to-date, NOT single-month movement.
//
// Design: split the ERP posting so PART of the 1020/1100 closing balance lands in MAY (an opening balance) and
// the REST in JUNE, summing to the SAME closing position the canonical fixture expects. Under the correct
// inception-to-date basis this ties exactly (delta 0). Under the buggy June-only movement basis, 1020 and 1100
// would each be short by the May portion → two false discrepancies. This test locks down that regression.
//
// Numbers: the canonical scenario's payment leg is DR 1020 500,000 / CR 1100 500,000. Here 200,000 of it is
// posted in MAY and 300,000 in JUNE; every other leg is the canonical June scenario. Closing 1020 =
// 200,000(May) + 300,000 − 2,500 = 497,500; closing 1100 = −200,000(May) + 1,130,000 − 300,000 = 630,000 -
// exactly the fixture.
func TestIntegration_M2C1_BisTrackTieOut_IncludesPriorPeriodOpeningBalance(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	year := 2089
	ensurePeriods(t, pool, year)
	fixture := writeReanchoredFixture(t, year, bistrackScenarioMonth)

	branch := aTestBranch(t, pool)
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())

	// MAY (prior period): seed part of the closing balance as an OPENING balance. DR 1020 200,000 / CR 1100
	// 200,000 posted in May. This is a receipt settling AR that occurred before June.
	mayAt := time.Date(year, bistrackScenarioMonth-1, 20, 12, 0, 0, 0, time.UTC)
	maySvc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, mayAt)
	if _, err := maySvc.PostExternal(ctx, ExternalJournal{
		Source:         SourceAdjustment,
		BranchID:       branch,
		IdempotencyKey: fmt.Sprintf("m2c1-may-open-%s", uniq),
		Memo:           "May opening: prior-period receipt (must be included inception-to-date)",
		Lines: []ExternalLine{
			{AccountCode: "1020", DebitCents: 200_000, Memo: "May undeposited"},
			{AccountCode: "1100", CreditCents: 200_000, Memo: "May AR settled"},
		},
	}); err != nil {
		t.Fatalf("post May opening: %v", err)
	}

	// JUNE: the canonical scenario, but with the May-seeded pair REMOVED from June so the CLOSING balances match
	// the fixture exactly. The scenario's payment leg (DR 1020 500,000 / CR 1100 500,000) becomes 300,000 in
	// June (200,000 already booked in May), identical June+May closing position, split across two periods.
	juneAt := time.Date(year, bistrackScenarioMonth, 15, 12, 0, 0, 0, time.UTC)
	juneSvc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, juneAt)
	posts := []struct {
		key   string
		memo  string
		lines []ExternalLine
	}{
		{"inv", "Invoice finalized", []ExternalLine{
			{AccountCode: "1100", DebitCents: 1_130_000, Memo: "AR"},
			{AccountCode: "4000", CreditCents: 1_000_000, Memo: "revenue"},
			{AccountCode: "2200", CreditCents: 130_000, Memo: "HST"},
		}},
		{"pay", "Payment received (June portion)", []ExternalLine{
			{AccountCode: "1020", DebitCents: 300_000, Memo: "undeposited receipt (June)"},
			{AccountCode: "1100", CreditCents: 300_000, Memo: "AR settled (June)"},
		}},
		{"till", "Till over/short", []ExternalLine{
			{AccountCode: "6350", DebitCents: 2_500, Memo: "cash short"},
			{AccountCode: "1020", CreditCents: 2_500, Memo: "undeposited reduced"},
		}},
		{"rent", "Rent accrual", []ExternalLine{
			{AccountCode: "6100", DebitCents: 250_000, Memo: "rent"},
			{AccountCode: "2100", CreditCents: 250_000, Memo: "AP"},
		}},
	}
	for _, p := range posts {
		if _, err := juneSvc.PostExternal(ctx, ExternalJournal{
			Source:         SourceAdjustment,
			BranchID:       branch,
			IdempotencyKey: fmt.Sprintf("m2c1-split-%s-%s", p.key, uniq),
			Memo:           p.memo,
			Lines:          p.lines,
		}); err != nil {
			t.Fatalf("post June %s: %v", p.key, err)
		}
	}

	// Reconcile as of June close. Inception-to-date, 1020 = 200,000(May) + 300,000 − 2,500 = 497,500 and
	// 1100 = −200,000(May) + 1,130,000 − 300,000 = 630,000, exactly the fixture. Must tie with ZERO
	// discrepancy. If the harness used June-only movement, 1020 would read 297,500 and 1100 would read 830,000,
	// producing two false discrepancies, the exact regression.
	rep, err := juneSvc.BisTrackTieOut(ctx, year, bistrackScenarioMonth, branch, fixture, 0)
	if err != nil {
		t.Fatalf("bistrack tie-out: %v", err)
	}
	if !rep.TiesWithinTol {
		t.Fatalf("prior-period opening balance must be included (inception-to-date); got discrepancies: %+v", rep.Discrepancies)
	}
	for _, ln := range rep.Lines {
		switch ln.AccountCode {
		case "1020":
			if ln.ERPCents != 497_500 {
				t.Fatalf("1020 must read the inception-to-date closing 497500 (incl May opening), got %d", ln.ERPCents)
			}
		case "1100":
			if ln.ERPCents != 630_000 {
				t.Fatalf("1100 must read the inception-to-date closing 630000 (incl May opening), got %d", ln.ERPCents)
			}
		}
	}
}

// M2-C1 (negative), perturbing ONE transaction (drop $100.00 off the recorded receipt) breaks the tie: the
// reconciliation REPORTS the discrepancy rather than passing, proving it is a real reconciliation. Exactly the
// two accounts the perturbation touches (1020 undeposited, 1100 AR) go out of tie, by equal-and-opposite
// amounts, while the grand delta stays 0 (the ERP books still balance internally, the break is vs BisTrack).
func TestIntegration_M2C1_BisTrackTieOut_ReportsPerturbation(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	year := 2090
	ensurePeriods(t, pool, year)
	fixture := writeReanchoredFixture(t, year, bistrackScenarioMonth)

	branch := aTestBranch(t, pool)
	at := time.Date(year, bistrackScenarioMonth, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)
	uniq := fmt.Sprintf("%d", time.Now().UnixNano())
	postBisTrackScenario(t, svc, branch, uniq)

	// Perturb: record an EXTRA $100.00 receipt (DR 1020 / CR 1100) that BisTrack does not have. Now the ERP
	// 1020 is 10,000 higher and 1100 is 10,000 lower than the static snapshot.
	const delta = 10_000
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source:         SourceAdjustment,
		BranchID:       branch,
		IdempotencyKey: fmt.Sprintf("m2c1-perturb-%s", uniq),
		Memo:           "perturbation: extra receipt not in BisTrack",
		Lines: []ExternalLine{
			{AccountCode: "1020", DebitCents: delta, Memo: "extra undeposited"},
			{AccountCode: "1100", CreditCents: delta, Memo: "extra AR settled"},
		},
	}); err != nil {
		t.Fatalf("post perturbation: %v", err)
	}

	rep, err := svc.BisTrackTieOut(ctx, year, bistrackScenarioMonth, branch, fixture, 0)
	if err != nil {
		t.Fatalf("bistrack tie-out: %v", err)
	}
	if rep.TiesWithinTol {
		t.Fatal("M2-C1 negative: a perturbed ledger must NOT tie out (the reconciliation would be a tautology)")
	}
	if len(rep.Discrepancies) != 2 {
		t.Fatalf("M2-C1 negative: expected exactly 2 out-of-tie accounts (1020, 1100), got %d: %+v", len(rep.Discrepancies), rep.Discrepancies)
	}
	byCode := map[string]TieoutLine{}
	for _, d := range rep.Discrepancies {
		byCode[d.AccountCode] = d
	}
	// 1020 undeposited (debit-normal): ERP is +delta over BisTrack.
	if d, ok := byCode["1020"]; !ok || d.DeltaCents != delta {
		t.Fatalf("M2-C1 negative: 1020 delta should be +%d, got %+v", delta, byCode["1020"])
	}
	// 1100 AR (debit-normal): ERP is −delta under BisTrack.
	if d, ok := byCode["1100"]; !ok || d.DeltaCents != -delta {
		t.Fatalf("M2-C1 negative: 1100 delta should be -%d, got %+v", delta, byCode["1100"])
	}
	// The perturbation is internally balanced, so the grand net-debit delta is still 0, the discrepancy is
	// visible ONLY at the account level, which is exactly why per-account tie-out (not just a grand total) is
	// required. This asserts the report catches an offsetting misclassification a grand total would miss.
	if rep.GrandDeltaCents != 0 {
		t.Fatalf("M2-C1 negative: an internally-balanced perturbation leaves grand delta 0, got %d", rep.GrandDeltaCents)
	}
}
