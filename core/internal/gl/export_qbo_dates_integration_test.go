//go:build integration

package gl

// export_qbo_dates_integration_test.go, the AX-2 fix round's P1-1 on the real core: each leg seeds
// its own journal of the shape the review named, and asserts every JournalDate of the file falls
// inside the file's period.

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func qboDatesWorld(t *testing.T, year int) (context.Context, *Service, string) {
	t.Helper()
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, year)
	svc := exportSvc(t, pool, time.Date(year, 7, 10, 16, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetQuickBooksOnline)
	})
	for _, m := range [][2]string{{"1000", "10100 Checking"}, {"4000", "40100 Sales"}} {
		if _, err := svc.SetAccountMapping(ctx, TargetQuickBooksOnline, m[0], m[1]); err != nil {
			t.Fatalf("map %s: %v", m[0], err)
		}
	}
	return ctx, svc, aUser(t, pool)
}

func qboInPeriod(t *testing.T, content string, year, month int) [][]string {
	t.Helper()
	recs := qboCSV(t, content)
	first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	lo, hi := first.Format("2006-01-02"), first.AddDate(0, 1, -1).Format("2006-01-02")
	if len(recs) < 2 {
		t.Fatalf("the file carries no entry:\n%s", content)
	}
	for _, r := range recs[1:] {
		if r[1] < lo || r[1] > hi {
			t.Fatalf("JournalDate %q is outside the file's period %s..%s:\n%s", r[1], lo, hi, content)
		}
	}
	return recs
}

// A June 28 payment whose month is closed rolls into open July and goes out dated July 1.
func TestIntegration_Export_QBOARolledForwardEntryIsDatedInsideItsPeriod(t *testing.T) {
	ctx, svc, user := qboDatesWorld(t, 2097)
	pool := intPool(t)
	if _, err := NewRepository(pool).ClosePeriod(ctx, periodID(t, pool, 2097, 6), user, "AX-2 fix roll forward", nil); err != nil {
		t.Fatalf("close June: %v", err)
	}
	var branch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: "ax2fix-roll", Memo: "june payment", BranchID: branch,
		DocumentDate: time.Date(2097, 6, 28, 4, 0, 0, 0, time.UTC),
		Lines:        []ExternalLine{{AccountCode: "1000", DebitCents: 4100}, {AccountCode: "4000", CreditCents: 4100}},
	}); err != nil {
		t.Fatalf("post into closed June: %v", err)
	}
	res, err := svc.Export(ctx, ExportQuery{Year: 2097, Month: 7, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("July export: %v", err)
	}
	recs := qboInPeriod(t, res.Content, 2097, 7)
	if recs[1][1] != "2097-07-01" || !strings.Contains(recs[1][5], "dated 28 June, posted to July") {
		t.Fatalf("the rolled forward entry must be dated July 1 and say June 28, got %q", recs[1])
	}
}

// A draft parked in a month and approved after it goes out in that month's file dated its last day.
func TestIntegration_Export_QBOALateApprovedDraftIsDatedInsideItsPeriod(t *testing.T) {
	// 2020 is long past, so the database's own clock stamps the approval after the parked month.
	ctx, svc, user := qboDatesWorld(t, 2020)
	pool := intPool(t)
	var branch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	park := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2020, 5, 20, 12, 0, 0, 0, time.UTC))
	j, err := park.CreateJournal(ctx, CreateJournalInput{
		BranchID: branch, Memo: "may draft",
		Lines: []JournalLineInput{{AccountCode: "1000", DebitCents: 900000}, {AccountCode: "4000", CreditCents: 900000}},
	})
	if err != nil || j.Status != StatusDraft {
		t.Fatalf("park the May draft: %v (%s)", err, j.Status)
	}
	if _, err := NewRepository(pool).ApproveJournal(ctx, j.ID, user); err != nil {
		t.Fatalf("approve the parked draft: %v", err)
	}
	res, err := svc.Export(ctx, ExportQuery{Year: 2020, Month: 5, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("May export: %v", err)
	}
	recs := qboInPeriod(t, res.Content, 2020, 5)
	if recs[1][1] != "2020-05-31" || !strings.Contains(recs[1][5], ", posted to May") {
		t.Fatalf("the late approved entry must be dated May 31 and say its own day, got %q", recs[1])
	}
}

// An ordinary journal is unchanged.
func TestIntegration_Export_QBOAnOrdinaryEntryIsUnchanged(t *testing.T) {
	ctx, svc, _ := qboDatesWorld(t, 2098)
	pool := intPool(t)
	seedQBOJournalAt(t, pool, time.Date(2098, 5, 14, 16, 0, 0, 0, time.UTC), "ax2fix-plain", "1000", "4000", 2200)
	res, err := svc.Export(ctx, ExportQuery{Year: 2098, Month: 5, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	recs := qboInPeriod(t, res.Content, 2098, 5)
	if recs[1][1] != "2098-05-14" || recs[1][5] != "qbo leg" {
		t.Fatalf("an ordinary entry must go out untouched, got %q", recs[1])
	}
}
