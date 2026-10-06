//go:build integration

// M2-C1 DEMO-SEED regression, pins the migration pair behind the FY2026-06 demonstration books:
//
//	0056_bistrack_tieout_demo.sql posts the four deterministic FY2026-06 counterpart journals
//	  (invoice / receipt / till-short / rent) so the consolidated staging call
//	    GET /api/v1/gl/tieout/bistrack?year=2026&month=6   (branch_id="")
//	  ties the canonical embedded fixture (testdata/bistrack_gl_snapshot_2026-06.json) at delta 0, and
//	0221_the_setup_entries_behind_no_customer_document_come_back_out.sql RETIRES that seed by posting the
//	  same four journals back out as POSTED reversing entries (reversing, never deleting), because the
//	  fabricated AR balance disagreed with the AR subledger by exactly 630,000 cents on the statements a
//	  client reads.
//
// The tie the original demo-seed regression asserted ended with 0221: the fixture still expects the
// pre-retirement balances, so the ERP side of the June reconciliation is now deliberately ZERO on every
// fixture account and the report is out of tie. This test pins the RETIREMENT, not a tie:
//
//  1. both halves of the migration pair are present and POSTED (the seed applied AND the retirement with it),
//  2. the June inception-to-date closing balance of every fixture account nets to exactly zero (the
//     retirement left no residue on the shared books), and
//  3. the service read agrees: BisTrackTieOut reports every fixture account as BOTH (TrialBalanceAsOf sees
//     the June legs), erp_cents 0, and delta = minus the fixture balance (the report surfaces the
//     retirement rather than hiding it).
//
// A reader regression (as-of boundary, status filter, join) flips the lines to BISTRACK_ONLY or a nonzero
// erp side; a future demo reseed or a partial reversal moves erp_cents off zero. Either breaks this test.
// The nonzero-balance reconciliation machinery itself (signs, the inception-to-date basis, negative
// perturbation) is proven on isolated sandbox years in bistrack_tieout_integration_test.go, exactly as the
// 0221 header records.
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable' \
//	  go test -tags=integration ./internal/gl/... -run M2C1_DemoSeed -count=1 -p 1
package gl

import (
	"context"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
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

// The fixed journal UUIDs of the migration pair: the four 0056 demonstration journals and the four 0221
// reversing journals that withdraw them.
var (
	m2c1SeedJournals = []string{
		"d0560000-0000-4000-9000-000000000001", // invoice finalized
		"d0560000-0000-4000-9000-000000000002", // payment received
		"d0560000-0000-4000-9000-000000000003", // till short
		"d0560000-0000-4000-9000-000000000004", // rent accrual
	}
	m2c1ReversalJournals = []string{
		"d0580000-0000-4000-9000-000000000001", // invoice finalized reversed
		"d0580000-0000-4000-9000-000000000002", // payment received reversed
		"d0580000-0000-4000-9000-000000000003", // till short reversed
		"d0580000-0000-4000-9000-000000000004", // rent accrual reversed
	}
)

// countPostedJournals counts how many of ids exist as POSTED gl_journals.
func countPostedJournals(t *testing.T, pool *pgxpool.Pool, ids []string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM gl_journals WHERE status = 'POSTED' AND id = ANY($1::uuid[])`, ids).Scan(&n); err != nil {
		t.Fatalf("probe posted journals: %v", err)
	}
	return n
}

// TestIntegration_M2C1_DemoSeed_RetiredSeedNetsZero is the exact staging call (default embedded fixture,
// FY2026-06, consolidated, tolerance 0) after the retirement: it asserts the migration pair is fully applied
// and netted to zero, and that the tie-out reads that state faithfully.
func TestIntegration_M2C1_DemoSeed_RetiredSeedNetsZero(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)

	// 1. The seed applied (all four 0056 journals POSTED) and the retirement with it (all four 0221
	// reversals POSTED). A missing seed half reads as "the fix is not in the DB"; a missing reversal half
	// reads as "the retirement is not in the DB" (the books would still carry the fabricated balances).
	if got := countPostedJournals(t, pool, m2c1SeedJournals); got != len(m2c1SeedJournals) {
		t.Fatalf("migration 0056_bistrack_tieout_demo.sql not fully applied: %d of %d demonstration journals POSTED (run bin/migrate)", got, len(m2c1SeedJournals))
	}
	if got := countPostedJournals(t, pool, m2c1ReversalJournals); got != len(m2c1ReversalJournals) {
		t.Fatalf("migration 0221_the_setup_entries_behind_no_customer_document_come_back_out.sql not fully applied: %d of %d reversing journals POSTED, so the demonstration balances are not retired (run bin/migrate)", got, len(m2c1ReversalJournals))
	}

	// 2. The retirement left no residue: the June inception-to-date closing balance (POSTED, posted_at <=
	// period end, the same basis TrialBalanceAsOf reconciles on) nets every fixture account to exactly zero.
	// The only writers to the FY2026-06 books on a migrated database are the frozen migrations 0056 and
	// 0221 (cmd/seed posts no GL journals), so this is deterministic on a fresh migrate. The boundary is the
	// test's OWN period end, independent of the production periodEnd, so a production boundary regression
	// cannot hide inside this probe: the probe keeps stating the database truth and assertion 3 catches the
	// service drifting from it.
	junePeriodEnd := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
	balances := map[string]int64{}
	rows, err := pool.Query(ctx,
		`SELECT a.code::text, COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
		 FROM gl_journals j
		 JOIN gl_journal_lines l ON l.journal_id = j.id
		 JOIN gl_accounts a      ON a.id = l.account_id
		 WHERE j.status = 'POSTED' AND j.posted_at <= $1
		 GROUP BY a.code`, junePeriodEnd)
	if err != nil {
		t.Fatalf("probe june inception-to-date balances: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var net int64
		if err := rows.Scan(&code, &net); err != nil {
			t.Fatalf("scan june balance row: %v", err)
		}
		balances[code] = net
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("probe june balances: %v", err)
	}
	for code := range m2c1FixtureNatural {
		if net, ok := balances[code]; !ok {
			t.Fatalf("fixture account %s absent from the june inception-to-date probe (the posted legs vanished), want net 0", code)
		} else if net != 0 {
			t.Fatalf("fixture account %s carries a post-retirement residue: june inception-to-date net %d, want 0 (a demo leg was posted or only partially reversed)", code, net)
		}
	}

	// 3. Exactly the handler's call: default embedded fixture (path ""), consolidated (branch ""), tolerance
	// 0. "now" is irrelevant to an as-of-June reconciliation.
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)
	rep, err := svc.BisTrackTieOut(ctx, 2026, 6, "", "", 0)
	if err != nil {
		t.Fatalf("bistrack tie-out: %v", err)
	}

	// The fixture still expects the pre-retirement balances, so the report must be OUT of tie: asserting the
	// opposite would re-pin the world 0221 retired.
	if rep.TiesWithinTol {
		t.Fatalf("retired demo seed: the june tie-out must be out of tie (erp side nets to zero against the pre-retirement fixture); report=%+v", rep)
	}

	// Every fixture account must reconcile from BOTH sides (the reader sees the June legs; BISTRACK_ONLY is
	// the reader-broken symptom), with erp_cents exactly 0 (the retirement invariant, seen through the
	// service) and delta = minus the fixture balance (the bistrack side is untouched by the retirement).
	seen := map[string]bool{}
	for _, ln := range rep.Lines {
		want, isFixtureAcct := m2c1FixtureNatural[ln.AccountCode]
		if !isFixtureAcct {
			continue // consolidated may carry unrelated accounts; the M2-C1 contract is the 7 fixture accounts
		}
		seen[ln.AccountCode] = true
		if ln.Source != "BOTH" {
			t.Fatalf("account %s reconciled from %s, expected BOTH (BISTRACK_ONLY means the ERP read lost the June legs)", ln.AccountCode, ln.Source)
		}
		if ln.ERPCents != 0 {
			t.Fatalf("account %s ERP closing balance %d, want 0 (the demonstration seed must net to zero after the retirement)", ln.AccountCode, ln.ERPCents)
		}
		if ln.DeltaCents != -want {
			t.Fatalf("account %s delta %d, want %d (erp 0 minus the untouched fixture balance)", ln.AccountCode, ln.DeltaCents, -want)
		}
	}
	for code := range m2c1FixtureNatural {
		if !seen[code] {
			t.Fatalf("fixture account %s absent from the tie-out report (migration pair legs missing?)", code)
		}
	}
}
