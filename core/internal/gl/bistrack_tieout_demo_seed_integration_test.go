//go:build integration

// M2-C1 DEMO-SEED regression, proves migration 0056_bistrack_tieout_demo.sql makes the STAGING tie-out call
//
//	GET /api/v1/gl/tieout/bistrack?year=2026&month=6   (consolidated, branch_id="")
//
// tie to delta=0 (ties_within_tolerance:true) against the CANONICAL embedded fixture
// (testdata/bistrack_gl_snapshot_2026-06.json), reconciling the ERP inception-to-date closing balance
// (TrialBalanceAsOf, posted_at <= 2026-06-30), exactly what the handler does.
//
// WHY THIS FAILS WITHOUT THE FIX: before 0056 the June ERP books are empty, so all 7 fixture accounts come
// back BISTRACK_ONLY / erp_cents:0 and the report is ties_within_tolerance:false. The migration posts the four
// deterministic FY2026-06 counterpart journals (invoice / receipt / till-short / rent) that net each account to
// the fixture amount; with them present the consolidated closing balance reconciles exactly.
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable' \
//	  go test -tags=integration ./internal/gl/... -run M2C1_DemoSeed -count=1 -p 1
package gl

import (
	"context"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// The 7 fixture accounts and their expected NATURAL-orientation closing balances (cents), the source of truth
// mirrored from testdata/bistrack_gl_snapshot_2026-06.json. If the fixture changes, this map must change with
// it (and so must migration 0056).
var m2c1FixtureNatural = map[string]int64{
	"1020": 497_500,   // Undeposited funds (debit-normal)
	"1100": 630_000,   // AR (debit-normal)
	"2100": 250_000,   // AP (credit-normal, natural = +credit surplus)
	"2200": 130_000,   // HST payable (credit-normal)
	"4000": 1_000_000, // Revenue (credit-normal)
	"6100": 250_000,   // Rent (debit-normal)
	"6350": 2_500,     // Cash over/short (debit-normal)
}

// TestIntegration_M2C1_DemoSeed_StagingTieOutTies is the exact staging reconciliation: canonical embedded
// fixture, FY2026-06, consolidated. It requires migration 0056 to have been applied to the integration DB
// (bin/migrate applies it; the file is idempotent). It asserts the report ties at tolerance 0 and every fixture
// account reconciles from BOTH sides with delta 0, the live-demonstrable outcome.
func TestIntegration_M2C1_DemoSeed_StagingTieOutTies(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)

	// This is a REGRESSION test for migration 0056: it must FAIL on a DB where 0056 has not been applied (the
	// pre-fix state). We assert its presence up front with a failure that names the fix, so a missing migration
	// reads as "the fix is not in the DB" rather than an opaque tie-out mismatch below. bin/migrate applies 0056
	// (idempotent) before the suite runs.
	var seeded bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM gl_journals WHERE id = 'd0560000-0000-4000-9000-000000000001')`).Scan(&seeded); err != nil {
		t.Fatalf("probe 0056 seed: %v", err)
	}
	if !seeded {
		t.Fatalf("migration 0056_bistrack_tieout_demo.sql not applied, the M2-C1 FY2026-06 counterpart journals are absent, so the staging tie-out cannot tie (run bin/migrate)")
	}

	// Exactly the handler's call: default embedded fixture (path ""), consolidated (branch ""), tolerance 0.
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) // "now" is irrelevant to an as-of-June reconciliation
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)
	rep, err := svc.BisTrackTieOut(ctx, 2026, 6, "", "", 0)
	if err != nil {
		t.Fatalf("bistrack tie-out: %v", err)
	}

	if !rep.TiesWithinTol {
		t.Fatalf("M2-C1 demo seed: staging tie-out must tie (ties_within_tolerance:true); discrepancies=%+v", rep.Discrepancies)
	}
	if len(rep.Discrepancies) != 0 {
		t.Fatalf("M2-C1 demo seed: expected 0 discrepancies, got %+v", rep.Discrepancies)
	}

	// Every fixture account must reconcile from BOTH sides (not BISTRACK_ONLY, the pre-fix symptom) with the
	// exact natural-orientation closing balance from the fixture, and delta 0.
	seen := map[string]bool{}
	for _, ln := range rep.Lines {
		want, isFixtureAcct := m2c1FixtureNatural[ln.AccountCode]
		if !isFixtureAcct {
			continue // consolidated may carry unrelated accounts; the M2-C1 contract is the 7 fixture accounts
		}
		seen[ln.AccountCode] = true
		if ln.Source != "BOTH" {
			t.Fatalf("account %s reconciled from %s, expected BOTH (BISTRACK_ONLY is the pre-fix symptom)", ln.AccountCode, ln.Source)
		}
		if ln.DeltaCents != 0 {
			t.Fatalf("account %s out of tie by %d (erp=%d bistrack=%d)", ln.AccountCode, ln.DeltaCents, ln.ERPCents, ln.BisTrackCents)
		}
		if ln.ERPCents != want {
			t.Fatalf("account %s ERP closing balance %d, fixture expects %d", ln.AccountCode, ln.ERPCents, want)
		}
	}
	for code := range m2c1FixtureNatural {
		if !seen[code] {
			t.Fatalf("fixture account %s absent from the tie-out report (migration 0056 leg missing?)", code)
		}
	}
}
