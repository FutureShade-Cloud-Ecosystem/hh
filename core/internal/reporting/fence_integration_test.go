//go:build integration

package reporting

// fence_integration_test.go, HH-179. The DIB-391 fence was written for the paths that DECIDE a number
// (Run, Print, CommissionPreview) and the paths that READ one back were left open: a Kingston scoped
// manager who is refused Trenton at the run surface can still list every printed artefact in the
// company, open any of their documents, and export every rep's commission for every branch.
//
// The fence under test, in the module's own terms:
//
//	a read of ONE artefact answers the branch after the load, and a caller outside it gets NOT FOUND,
//	not forbidden, because a 403 confirms the artefact exists and whether it exists is branch
//	information too;
//	a list read carries the caller's reach into the SQL, so a scoped caller's list simply IS their
//	branches' rows;
//	an artefact with no branch is consolidated, the whole company's numbers, and answers to a GLOBAL
//	caller alone, the same ruling authorizeBranch makes for a run with no branch named.
//
// Every test here holds BOTH ways: a scoped caller must be held to their branch, and a global caller
// must keep reading everything, a fence that breaks leadership's consolidated view is not a fence.
//
// ROUND 2, the fence is on the DATA, not on the PRINTER. report_runs.branch_id is where the paper
// came out (print_queue.branch_id is NOT NULL, so even a consolidated report is printed AT a branch),
// and fencing on it let a consolidated artefact printed at Kingston read as Kingston's. The scope of
// what an artefact CARRIES is: the report's own branch parameter for a TEMPLATE artefact, empty
// meaning consolidated; and, for a COMMISSION artefact, the branches of the frozen periods its
// (rep, window) linkage matches, because the artefact row names no period id. Where that scope is
// genuinely underivable the artefact reads as consolidated, global only, fail closed.

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// printAt prints one sales_by_day artefact at a branch, as a GLOBAL caller, which is the honest
// provenance: a Kingston caller could never have printed Trenton.
func printAt(t *testing.T, f *fixture, svc *Service, branch, from, to string) Run {
	t.Helper()
	run, err := svc.Print(context.Background(), "sales_by_day",
		map[string]string{"from_date": from, "to_date": to, "branch_id": branch}, "")
	if err != nil {
		t.Fatalf("printing at %s: %v", branch, err)
	}
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, run.ID)
	f.del(`UPDATE report_runs SET print_job_id = NULL WHERE id=$1::uuid`, run.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, run.PrintJobID)
	return run
}

// consolidatedRun inserts a report_runs row with NO branch. The print path cannot produce one (paper
// comes out of a printer that lives somewhere), so it is written directly: the read fence has to hold
// for the row the service itself did not write.
func consolidatedRun(t *testing.T, f *fixture) string {
	t.Helper()
	id := f.newID()
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, id)
	f.exec(`INSERT INTO report_runs (id, kind, template_code, branch_id)
	        VALUES ($1::uuid, 'TEMPLATE', 'sales_by_day', NULL)`, id)
	return id
}

// freezeAt computes and freezes one rep's period at one branch, as a GLOBAL caller, over this test's
// private window. The caller's own rule is written company-wide so the arithmetic stays legible: with
// a 5% revenue rule, commission is a twentieth of the sale. The product (and its category) is made
// once per test and passed in: seeded users and branches share uuid prefixes, so names derived from
// their heads collide, and the tails are what distinguish them.
func freezeAt(t *testing.T, f *fixture, svc *Service, rep, branch string, saleCents int64, prod, from, to string) CommissionPeriod {
	t.Helper()
	cust := f.customer("c" + rep[len(rep)-6:] + branch[len(branch)-6:])
	f.repAssignment(cust, "", rep)
	ord := f.order(cust, branch, from)
	inv := f.invoicePaid(cust, branch, ord, from, saleCents, 0)
	f.invoiceLine(inv, prod, 1, 1, saleCents, saleCents)
	p, err := svc.ComputeAndFreeze(context.Background(), rep, from, to, branch)
	if err != nil {
		t.Fatalf("freezing %s at %s: %v", rep, branch, err)
	}
	f.del(`DELETE FROM commission_run_lines WHERE run_id=$1::uuid`, p.RunID)
	f.del(`DELETE FROM commission_runs WHERE id=$1::uuid`, p.RunID)
	return p
}

func runListHas(list []Run, id string) bool {
	for _, r := range list {
		if r.ID == id {
			return true
		}
	}
	return false
}

func periodListHas(list []CommissionPeriod, id string) bool {
	for _, p := range list {
		if p.RunID == id {
			return true
		}
	}
	return false
}

// accrualFor reads one rep's line out of the register, distinguishing "owed nothing" (found, zero)
// from "not the caller's business" (absent), which is exactly the distinction the fence draws.
func accrualFor(lines []AccrualLine, rep string) (int64, bool) {
	for _, l := range lines {
		if l.RepUserID == rep {
			return l.CommissionCents, true
		}
	}
	return 0, false
}

func ruleRatePresent(rules []CommissionRule, rateBps int) bool {
	for _, r := range rules {
		if r.RateBps == rateBps {
			return true
		}
	}
	return false
}

// TestIntegration_BranchFence_ArtefactReadsAreHeldToTheirBranch, the single artefact read, which is
// both the /runs/{id} surface and the /runs/{id}/document surface (one service call, two routes).
func TestIntegration_BranchFence_ArtefactReadsAreHeldToTheirBranch(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	global := globalSvc(t, pool)
	kingston := scopedSvc(t, pool, kingstonBranch)
	ctx := context.Background()
	from, to := window()

	cust := f.customer("acme")
	f.invoicePaid(cust, trentonBranch, "", from, 10_000, 500)

	kgnRun := printAt(t, f, global, kingstonBranch, from, to)
	trtRun := printAt(t, f, global, trentonBranch, from, to)
	consolidated := consolidatedRun(t, f)

	// A scoped caller reads their own branch's artefact...
	if _, err := kingston.RunArtefact(ctx, kgnRun.ID); err != nil {
		t.Errorf("a Kingston manager was refused their OWN branch's artefact: %v", err)
	}
	// ...is told Trenton's does not exist rather than that it is forbidden, because the 403 itself
	// would confirm the artefact is there...
	if _, err := kingston.RunArtefact(ctx, trtRun.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Kingston manager read Trenton's printed artefact (err=%v)", err)
	}
	// ...and a consolidated artefact, the whole company's numbers, is global-only, the same ruling the
	// run surface makes for a report with no branch named.
	if _, err := kingston.RunArtefact(ctx, consolidated); !errors.Is(err, ErrNotFound) {
		t.Errorf("a scoped manager read a CONSOLIDATED artefact, the whole company's numbers (err=%v)", err)
	}
	// A global caller keeps reading everything, consolidated included.
	for _, id := range []string{kgnRun.ID, trtRun.ID, consolidated} {
		if _, err := global.RunArtefact(ctx, id); err != nil {
			t.Errorf("a global caller was refused an artefact inside their reach (%s): %v", id, err)
		}
	}
}

// TestIntegration_BranchFence_ArtefactListIsHeldToTheirBranch, the /reporting/runs surface. The
// assertions are on THIS test's own artefacts, present or absent by id, never on the list's length,
// because the list is the whole table ordered by recency and other rows are not this test's business.
func TestIntegration_BranchFence_ArtefactListIsHeldToTheirBranch(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	global := globalSvc(t, pool)
	kingston := scopedSvc(t, pool, kingstonBranch)
	ctx := context.Background()
	from, to := window()

	cust := f.customer("acme")
	f.invoicePaid(cust, trentonBranch, "", from, 10_000, 500)

	kgnRun := printAt(t, f, global, kingstonBranch, from, to)
	trtRun := printAt(t, f, global, trentonBranch, from, to)
	consolidated := consolidatedRun(t, f)

	scopedList, err := kingston.Runs(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !runListHas(scopedList, kgnRun.ID) {
		t.Error("a Kingston manager's artefact list does not contain their OWN branch's artefact")
	}
	if runListHas(scopedList, trtRun.ID) {
		t.Error("a Kingston manager's artefact list contains Trenton's printed artefact")
	}
	if runListHas(scopedList, consolidated) {
		t.Error("a scoped manager's artefact list contains a CONSOLIDATED artefact, the whole company's numbers")
	}

	globalList, err := global.Runs(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{kgnRun.ID, trtRun.ID, consolidated} {
		if !runListHas(globalList, id) {
			t.Errorf("a global caller's artefact list lost an artefact inside their reach (%s)", id)
		}
	}
}

// TestIntegration_BranchFence_CommissionPeriodReadsAreHeldToTheirBranch, the single frozen period
// (/commission/runs/{id}), its export (/commission/runs/{id}/export.csv, the read that leaves the
// building), and the list (/commission/runs).
func TestIntegration_BranchFence_CommissionPeriodReadsAreHeldToTheirBranch(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	global := globalSvc(t, pool)
	kingston := scopedSvc(t, pool, kingstonBranch)
	ctx := context.Background()
	from, to := window()

	repK, repT := twoUsers(t, pool)
	f.commissionRule(repK, BasisRevenue, 500, "", "", from)
	f.commissionRule(repT, BasisRevenue, 500, "", "", from)
	prod := f.product("PAV1", "Paver", f.category("pavers"))

	kgnPeriod := freezeAt(t, f, global, repK, kingstonBranch, 20_000, prod, from, to)
	trtPeriod := freezeAt(t, f, global, repT, trentonBranch, 30_000, prod, from, to)
	// A consolidated period, no branch: only a global caller may compute one, and the read has to keep
	// it global too.
	consolidated, err := global.ComputeAndFreeze(ctx, repT, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM commission_run_lines WHERE run_id=$1::uuid`, consolidated.RunID)
	f.del(`DELETE FROM commission_runs WHERE id=$1::uuid`, consolidated.RunID)

	if kgnPeriod.CommissionCents != 1_000 || trtPeriod.CommissionCents != 1_500 {
		t.Fatalf("fixture arithmetic moved: Kingston %d and Trenton %d cents, want 1000 and 1500",
			kgnPeriod.CommissionCents, trtPeriod.CommissionCents)
	}

	// The single read.
	if _, err := kingston.CommissionRun(ctx, kgnPeriod.RunID); err != nil {
		t.Errorf("a Kingston manager was refused their OWN branch's period: %v", err)
	}
	if _, err := kingston.CommissionRun(ctx, trtPeriod.RunID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Kingston manager read Trenton's frozen period, every line of a rep's book (err=%v)", err)
	}
	if _, err := kingston.CommissionRun(ctx, consolidated.RunID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a scoped manager read a CONSOLIDATED period, the whole company's pay (err=%v)", err)
	}
	// The export rides the same read, and it is the one that leaves the building.
	if _, _, err := kingston.ExportCommissionRun(ctx, trtPeriod.RunID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Kingston manager exported Trenton's period as CSV (err=%v)", err)
	}
	// A global caller keeps reading everything.
	for _, id := range []string{kgnPeriod.RunID, trtPeriod.RunID, consolidated.RunID} {
		if _, err := global.CommissionRun(ctx, id); err != nil {
			t.Errorf("a global caller was refused a period inside their reach (%s): %v", id, err)
		}
	}

	// The list.
	scopedList, err := kingston.CommissionRuns(ctx, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	if !periodListHas(scopedList, kgnPeriod.RunID) {
		t.Error("a Kingston manager's period list does not contain their OWN branch's period")
	}
	if periodListHas(scopedList, trtPeriod.RunID) {
		t.Error("a Kingston manager's period list contains Trenton's period")
	}
	if periodListHas(scopedList, consolidated.RunID) {
		t.Error("a scoped manager's period list contains a CONSOLIDATED period, the whole company's pay")
	}
	globalList, err := global.CommissionRuns(ctx, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{kgnPeriod.RunID, trtPeriod.RunID, consolidated.RunID} {
		if !periodListHas(globalList, id) {
			t.Errorf("a global caller's period list lost a period inside their reach (%s)", id)
		}
	}
}

// TestIntegration_BranchFence_CommissionAccrualsAreHeldToTheirBranch, the accrual register. It is
// grouped by REP, but the money underneath is branch scoped, so the fence goes on the runs being
// summed: a scoped caller's register owes their branches' periods alone.
func TestIntegration_BranchFence_CommissionAccrualsAreHeldToTheirBranch(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	global := globalSvc(t, pool)
	kingston := scopedSvc(t, pool, kingstonBranch)
	ctx := context.Background()
	from, to := window()

	repK, repT := twoUsers(t, pool)
	f.commissionRule(repK, BasisRevenue, 500, "", "", from)
	f.commissionRule(repT, BasisRevenue, 500, "", "", from)
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	freezeAt(t, f, global, repK, kingstonBranch, 20_000, prod, from, to) // repK owed 1_000 at Kingston
	freezeAt(t, f, global, repT, trentonBranch, 30_000, prod, from, to)  // repT owed 1_500 at Trenton
	// The consolidated leg: the same window frozen again with no branch, whole company's pay for repT,
	// global only. The register sums both frozen runs for the global caller (re-freezing is an operator
	// decision and both are owed) and neither of them for the scoped one.
	consolidated, err := global.ComputeAndFreeze(ctx, repT, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM commission_run_lines WHERE run_id=$1::uuid`, consolidated.RunID)
	f.del(`DELETE FROM commission_runs WHERE id=$1::uuid`, consolidated.RunID)

	scoped, err := kingston.CommissionAccruals(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if cents, ok := accrualFor(scoped.Lines, repK); !ok || cents != 1_000 {
		t.Errorf("a Kingston manager's accrual register says rep %s is owed %d (found=%v), want 1000, "+
			"their branch's period alone", repK, cents, ok)
	}
	if _, ok := accrualFor(scoped.Lines, repT); ok {
		t.Error("a Kingston manager's accrual register contains a Trenton rep's pay at all, neither the " +
			"Trenton period nor the consolidated one is theirs to read")
	}

	whole, err := global.CommissionAccruals(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if cents, ok := accrualFor(whole.Lines, repK); !ok || cents != 1_000 {
		t.Errorf("the global accrual register says rep %s is owed %d (found=%v), want 1000", repK, cents, ok)
	}
	if cents, ok := accrualFor(whole.Lines, repT); !ok || cents != 3_000 {
		t.Errorf("the global accrual register says rep %s is owed %d (found=%v), want 3000, the Trenton "+
			"period plus the consolidated one", repT, cents, ok)
	}
}

// TestIntegration_BranchFence_CommissionRulesAreHeldToTheirReach, the rule list. A rule is readable
// when it is in force at a branch the caller can reach, which by chooseRule's own ruling means its
// branch is empty (in force everywhere) or names a granted branch: a Trenton rate is Trenton's pay
// decision, while a company-wide rate governs the caller's own branch and is part of the arithmetic
// their own fenced preview performs.
func TestIntegration_BranchFence_CommissionRulesAreHeldToTheirReach(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	global := globalSvc(t, pool)
	kingston := scopedSvc(t, pool, kingstonBranch)
	ctx := context.Background()
	from, _ := window()

	rep, _ := twoUsers(t, pool)
	f.commissionRule(rep, BasisRevenue, 500, "", kingstonBranch, from)
	f.commissionRule(rep, BasisRevenue, 700, "", trentonBranch, from)
	f.commissionRule(rep, BasisRevenue, 300, "", "", from)

	scoped, err := kingston.CommissionRules(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !ruleRatePresent(scoped, 500) {
		t.Error("a Kingston manager's rule list lost their OWN branch's rule")
	}
	if ruleRatePresent(scoped, 700) {
		t.Error("a Kingston manager's rule list contains Trenton's rate, another branch's pay decision")
	}
	if !ruleRatePresent(scoped, 300) {
		t.Error("a Kingston manager's rule list lost the company-wide rule, which is in force at their " +
			"branch and explains the commission numbers they may legitimately read")
	}

	globalRules, err := global.CommissionRules(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{500, 700, 300} {
		if !ruleRatePresent(globalRules, rate) {
			t.Errorf("a global caller's rule list lost the %d bps rule", rate)
		}
	}
}

// grantsSvc builds a Service whose caller is branch scoped with exactly the grant map given, which
// is how the empty grant set is posed: a nil map, an empty map, and an all-false map are three
// spellings of a real state, a scoped account with no branches.
func grantsSvc(t *testing.T, pool *pgxpool.Pool, branches map[string]bool) *Service {
	t.Helper()
	return NewService(NewRepository(pool)).withCaller(func(context.Context) (identity.StaffContext, bool) {
		return identity.StaffContext{
			UserID: fixtureActor(t, pool), RoleCode: "BRANCH_MANAGER",
			Tier: identity.TierManager, Scope: identity.ScopeBranch, Status: "ACTIVE",
			Branches: branches,
		}, true
	})
}

// TestIntegration_BranchFence_EmptyGrantSetSeesNothing, the committed legs for the caller with no
// grants: every fenced read answers nothing (an empty list, not an error, not every row), the single
// reads answer not found, and only the wildcard rules surface. The reviewer had to probe for these;
// now they are pinned for all three spellings of the empty grant set.
func TestIntegration_BranchFence_EmptyGrantSetSeesNothing(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	global := globalSvc(t, pool)
	ctx := context.Background()
	from, to := window()

	repK, _ := twoUsers(t, pool)
	f.commissionRule(repK, BasisRevenue, 500, "", "", from)
	prod := f.product("PAV1", "Paver", f.category("pavers"))
	kgnPeriod := freezeAt(t, f, global, repK, kingstonBranch, 20_000, prod, from, to)
	kgnRun := printAt(t, f, global, kingstonBranch, from, to)

	for name, branches := range map[string]map[string]bool{
		"nil grant map":    nil,
		"empty grant map":  {},
		"all false grants": {kingstonBranch: false, trentonBranch: false},
	} {
		t.Run(name, func(t *testing.T) {
			svc := grantsSvc(t, pool, branches)

			runs, err := svc.Runs(ctx, 200)
			if err != nil {
				t.Fatalf("the artefact list errored rather than answering empty: %v", err)
			}
			if len(runs) != 0 {
				t.Errorf("a caller with no grants listed %d artefacts, the empty grant set matches nothing", len(runs))
			}
			periods, err := svc.CommissionRuns(ctx, from, to, "")
			if err != nil {
				t.Fatalf("the period list errored rather than answering empty: %v", err)
			}
			if len(periods) != 0 {
				t.Errorf("a caller with no grants listed %d periods, the empty grant set matches nothing", len(periods))
			}
			acc, err := svc.CommissionAccruals(ctx, from, to)
			if err != nil {
				t.Fatalf("the accrual register errored rather than answering empty: %v", err)
			}
			if len(acc.Lines) != 0 {
				t.Errorf("a caller with no grants was owed %d accrual lines, the empty grant set matches nothing", len(acc.Lines))
			}
			if _, err := svc.RunArtefact(ctx, kgnRun.ID); !errors.Is(err, ErrNotFound) {
				t.Errorf("a caller with no grants read a single artefact (err=%v), want not found", err)
			}
			if _, err := svc.CommissionRun(ctx, kgnPeriod.RunID); !errors.Is(err, ErrNotFound) {
				t.Errorf("a caller with no grants read a single period (err=%v), want not found", err)
			}
			rules, err := svc.CommissionRules(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rules {
				if r.BranchID != "" {
					t.Errorf("a caller with no grants saw the branch scoped rule %s, only the "+
						"wildcard rules (no branch) are in force everywhere", r.ID)
				}
			}
		})
	}
}

// TestIntegration_BranchFence_ConsolidatedTemplateArtefactPrintedAtABranchIsGlobalOnly, round 2: a
// global caller running the consolidated sales_by_day must name a branch to print it AT, and that
// print branch must not turn the whole company's numbers into that branch's to read. Trenton's sales
// are inside the artefact; a Kingston manager must not see it, and a Trenton manager must still see
// their own numbers wherever the paper came out: the fence is on the data, not on the printer.
func TestIntegration_BranchFence_ConsolidatedTemplateArtefactPrintedAtABranchIsGlobalOnly(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	global := globalSvc(t, pool)
	kingston := scopedSvc(t, pool, kingstonBranch)
	trenton := scopedSvc(t, pool, trentonBranch)
	ctx := context.Background()
	from, to := window()

	cust := f.customer("acme")
	f.invoicePaid(cust, trentonBranch, "", from, 10_000, 500)

	// A consolidated artefact (no branch parameter, the whole company) printed at Kingston.
	consolidated, err := global.Print(ctx, "sales_by_day",
		map[string]string{"from_date": from, "to_date": to}, kingstonBranch)
	if err != nil {
		t.Fatal(err)
	}
	f.del(`UPDATE report_runs SET print_job_id = NULL WHERE id=$1::uuid`, consolidated.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, consolidated.PrintJobID)
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, consolidated.ID)

	// The honest reader's contrast: a Kingston report (data and printer both at Kingston).
	own, err := global.Print(ctx, "sales_by_day",
		map[string]string{"from_date": from, "to_date": to, "branch_id": kingstonBranch}, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`UPDATE report_runs SET print_job_id = NULL WHERE id=$1::uuid`, own.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, own.PrintJobID)
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, own.ID)

	// Trenton's data printed at Kingston: Trenton's to read, not Kingston's.
	misplaced, err := global.Print(ctx, "sales_by_day",
		map[string]string{"from_date": from, "to_date": to, "branch_id": trentonBranch}, kingstonBranch)
	if err != nil {
		t.Fatal(err)
	}
	f.del(`UPDATE report_runs SET print_job_id = NULL WHERE id=$1::uuid`, misplaced.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, misplaced.PrintJobID)
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, misplaced.ID)

	// The single read.
	if _, err := kingston.RunArtefact(ctx, own.ID); err != nil {
		t.Errorf("a Kingston manager was refused their OWN branch's artefact: %v", err)
	}
	if _, err := kingston.RunArtefact(ctx, consolidated.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Kingston manager read the CONSOLIDATED artefact because it was printed at "+
			"Kingston, and Trenton's numbers are inside it (err=%v)", err)
	}
	if _, err := kingston.RunArtefact(ctx, misplaced.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Kingston manager read Trenton's numbers because the paper came out at Kingston "+
			"(err=%v)", err)
	}
	if _, err := trenton.RunArtefact(ctx, misplaced.ID); err != nil {
		t.Errorf("a Trenton manager was refused their OWN numbers printed at another branch: %v", err)
	}
	// A global caller keeps reading everything.
	for _, id := range []string{own.ID, consolidated.ID, misplaced.ID} {
		if _, err := global.RunArtefact(ctx, id); err != nil {
			t.Errorf("a global caller was refused an artefact inside their reach (%s): %v", id, err)
		}
	}

	// The list answers the same way.
	scopedList, err := kingston.Runs(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !runListHas(scopedList, own.ID) {
		t.Error("a Kingston manager's artefact list does not contain their OWN branch's artefact")
	}
	if runListHas(scopedList, consolidated.ID) {
		t.Error("a Kingston manager's artefact list contains the CONSOLIDATED artefact because it was " +
			"printed at Kingston")
	}
	if runListHas(scopedList, misplaced.ID) {
		t.Error("a Kingston manager's artefact list contains Trenton's numbers because the paper " +
			"came out at Kingston")
	}
	trentonList, err := trenton.Runs(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !runListHas(trentonList, misplaced.ID) {
		t.Error("a Trenton manager's artefact list lost their OWN numbers printed at another branch")
	}
	globalList, err := global.Runs(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{own.ID, consolidated.ID, misplaced.ID} {
		if !runListHas(globalList, id) {
			t.Errorf("a global caller's artefact list lost an artefact inside their reach (%s)", id)
		}
	}
}

// TestIntegration_BranchFence_ConsolidatedCommissionArtefactPrintedAtABranchIsGlobalOnly, round 2:
// the period read already answers not found for a scoped caller, but printing the consolidated period
// wrote a report_runs row whose only branch is the PRINT branch, so the frozen statement stayed
// readable through the artefact surface. The artefact carries its (rep, window) linkage in params and
// nothing else, so its scope has to come from the periods that linkage matches.
func TestIntegration_BranchFence_ConsolidatedCommissionArtefactPrintedAtABranchIsGlobalOnly(t *testing.T) {
	pool := intPool(t)
	f := newFixture(t, pool)
	global := globalSvc(t, pool)
	kingston := scopedSvc(t, pool, kingstonBranch)
	ctx := context.Background()
	from, to := window()

	repK, repT := twoUsers(t, pool)
	f.commissionRule(repK, BasisRevenue, 500, "", "", from)
	f.commissionRule(repT, BasisRevenue, 500, "", "", from)
	prod := f.product("PAV1", "Paver", f.category("pavers"))

	// The honest reader's artefact: repK's Kingston period, frozen at Kingston and printed at
	// Kingston, data and printer agreeing.
	kgnPeriod := freezeAt(t, f, global, repK, kingstonBranch, 20_000, prod, from, to)
	ownArtefact, err := global.PrintCommissionRun(ctx, kgnPeriod.RunID, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`UPDATE report_runs SET print_job_id = NULL WHERE id=$1::uuid`, ownArtefact.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, ownArtefact.PrintJobID)
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, ownArtefact.ID)

	// repT's sale lives at Trenton, but the period is frozen consolidated (no branch), which only a
	// global caller may do. The sale is planted by hand rather than through freezeAt so no
	// branch-scoped twin period shares the linkage triple.
	custT := f.customer("ct" + repT[len(repT)-6:])
	f.repAssignment(custT, "", repT)
	ordT := f.order(custT, trentonBranch, from)
	invT := f.invoicePaid(custT, trentonBranch, ordT, from, 30_000, 0)
	f.invoiceLine(invT, prod, 1, 1, 30_000, 30_000)
	consolidatedPeriod, err := global.ComputeAndFreeze(ctx, repT, from, to, "")
	if err != nil {
		t.Fatal(err)
	}
	f.del(`DELETE FROM commission_run_lines WHERE run_id=$1::uuid`, consolidatedPeriod.RunID)
	f.del(`DELETE FROM commission_runs WHERE id=$1::uuid`, consolidatedPeriod.RunID)
	consolidatedArtefact, err := global.PrintCommissionRun(ctx, consolidatedPeriod.RunID, kingstonBranch)
	if err != nil {
		t.Fatal(err)
	}
	f.del(`UPDATE report_runs SET print_job_id = NULL WHERE id=$1::uuid`, consolidatedArtefact.ID)
	f.del(`DELETE FROM print_queue WHERE id=$1::uuid`, consolidatedArtefact.PrintJobID)
	f.del(`DELETE FROM report_runs WHERE id=$1::uuid`, consolidatedArtefact.ID)

	// The honest reader keeps their artefact...
	if _, err := kingston.RunArtefact(ctx, ownArtefact.ID); err != nil {
		t.Errorf("a Kingston manager was refused their OWN branch's commission artefact: %v", err)
	}
	// ...the period itself answers not found (round one)...
	if _, err := kingston.CommissionRun(ctx, consolidatedPeriod.RunID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Kingston manager read the consolidated period itself (err=%v)", err)
	}
	// ...and the frozen statement of that same period must answer not found too, not sneak back in
	// as an artefact printed at Kingston.
	if _, err := kingston.RunArtefact(ctx, consolidatedArtefact.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Kingston manager read the CONSOLIDATED commission artefact because it was printed "+
			"at Kingston, though the period itself answers not found (err=%v)", err)
	}
	if _, err := global.RunArtefact(ctx, consolidatedArtefact.ID); err != nil {
		t.Errorf("a global caller was refused the consolidated commission artefact: %v", err)
	}

	// The list answers the same way.
	scopedList, err := kingston.Runs(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !runListHas(scopedList, ownArtefact.ID) {
		t.Error("a Kingston manager's artefact list does not contain their OWN branch's commission artefact")
	}
	if runListHas(scopedList, consolidatedArtefact.ID) {
		t.Error("a Kingston manager's artefact list contains the CONSOLIDATED commission artefact " +
			"because it was printed at Kingston")
	}
	globalList, err := global.Runs(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !runListHas(globalList, consolidatedArtefact.ID) {
		t.Error("a global caller's artefact list lost the consolidated commission artefact")
	}
}
