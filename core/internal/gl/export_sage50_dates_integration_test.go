//go:build integration

// export_sage50_dates_integration_test.go, W3 AX-3 item 2's database half: each leg seeds its own
// journal of the shape the review named, through the real write doors (PostExternal, and the
// parked draft's CreateJournal plus ApproveJournal), and asserts every transaction's date falls
// inside the file's period.

package gl

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sageDatesWorld builds a Controller service pinned mid year, with the two Sage 50 mappings the
// legs' journals move, for one test year.
func sageDatesWorld(t *testing.T, year int) (context.Context, *Service, string) {
	t.Helper()
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, year)
	svc := exportSvc(t, pool, time.Date(year, 7, 10, 16, 0, 0, 0, time.UTC))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_account_mapping WHERE target = $1`, TargetSage50)
	})
	for _, m := range [][2]string{{"1000", "1000"}, {"4000", "4000"}} {
		if _, err := svc.SetAccountMapping(ctx, TargetSage50, m[0], m[1]); err != nil {
			t.Fatalf("map %s: %v", m[0], err)
		}
	}
	return ctx, svc, aUser(t, pool)
}

// seedSageJournalAt posts one balanced external journal at a chosen instant, the movement the
// export period then carries.
func seedSageJournalAt(t *testing.T, pool *pgxpool.Pool, at time.Time, key, debitCode, creditCode string, cents int64) {
	t.Helper()
	user := aUser(t, pool)
	svc := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, at)
	if _, err := svc.PostExternal(context.Background(), ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: key, Memo: "sage leg",
		Lines: []ExternalLine{{AccountCode: debitCode, DebitCents: cents}, {AccountCode: creditCode, CreditCents: cents}},
	}); err != nil {
		t.Fatalf("seed the journal at %s: %v", at.Format(time.RFC3339), err)
	}
}

// A June 28 payment whose month is closed rolls into open July and goes out dated July 1, the
// note naming the document's own day with the year. The year is 2051 because Sage 50's date is a
// two digit year and the legs read it back the way the importer must: the years the mm-dd-yy
// round trip can carry are the ones the legs book in (never the seeded 2026).
func TestIntegration_Export_Sage50ARolledForwardTransactionIsDatedInsideItsPeriod(t *testing.T) {
	ctx, svc, user := sageDatesWorld(t, 2051)
	pool := intPool(t)
	if _, err := NewRepository(pool).ClosePeriod(ctx, periodID(t, pool, 2051, 6), user, "AX-3 roll forward", nil); err != nil {
		t.Fatalf("close June: %v", err)
	}
	var branch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	if _, err := svc.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: "ax3-roll", Memo: "june payment", BranchID: branch,
		DocumentDate: time.Date(2051, 6, 28, 4, 0, 0, 0, time.UTC),
		Lines:        []ExternalLine{{AccountCode: "1000", DebitCents: 4100}, {AccountCode: "4000", CreditCents: 4100}},
	}); err != nil {
		t.Fatalf("post into closed June: %v", err)
	}
	res, err := svc.Export(ctx, ExportQuery{Year: 2051, Month: 7, Target: TargetSage50})
	if err != nil {
		t.Fatalf("July export: %v", err)
	}
	txs := sageFile(t, res.Content)
	sageAssertInPeriod(t, txs, 2051, 7)
	if len(txs) == 0 {
		t.Fatalf("July's file carries no transaction, so there is nothing to read the clamp off:\n%s", res.Content)
	}
	if txs[0].Date != "07-01-51" || !strings.Contains(txs[0].Comment, "dated 28 June 2051, posted to July 2051") {
		t.Fatalf("the rolled forward transaction must be dated 07-01-51 and say June 28 2051, got %s with %q", txs[0].Date, txs[0].Comment)
	}
}

// A draft parked in a month and approved after it goes out in that month's file dated its last
// day, the note naming the approval day with the year.
func TestIntegration_Export_Sage50ALateApprovedDraftIsDatedInsideItsPeriod(t *testing.T) {
	// 2024 is long past, so the database's own clock stamps the approval after the parked month.
	ctx, svc, user := sageDatesWorld(t, 2024)
	pool := intPool(t)
	var branch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	park := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2024, 5, 20, 12, 0, 0, 0, time.UTC))
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
	res, err := svc.Export(ctx, ExportQuery{Year: 2024, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("May export: %v", err)
	}
	txs := sageFile(t, res.Content)
	sageAssertInPeriod(t, txs, 2024, 5)
	if len(txs) == 0 {
		t.Fatalf("May's file carries no transaction, so there is nothing to read the approval date off:\n%s", res.Content)
	}
	if txs[0].Date != "05-31-24" || !strings.Contains(txs[0].Comment, "approved ") || !strings.Contains(txs[0].Comment, ", posted to May 2024") {
		t.Fatalf("the late approved transaction must be dated 05-31-24 and say the approval day, got %s with %q", txs[0].Date, txs[0].Comment)
	}
}

// An ordinary transaction is unchanged: its own house day, its own memo.
func TestIntegration_Export_Sage50AnOrdinaryTransactionIsUnchanged(t *testing.T) {
	ctx, svc, _ := sageDatesWorld(t, 2052)
	pool := intPool(t)
	seedSageJournalAt(t, pool, time.Date(2052, 5, 14, 16, 0, 0, 0, time.UTC), "ax3-plain", "1000", "4000", 2200)
	res, err := svc.Export(ctx, ExportQuery{Year: 2052, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	txs := sageFile(t, res.Content)
	sageAssertInPeriod(t, txs, 2052, 5)
	if len(txs) == 0 {
		t.Fatalf("the file carries no transaction, so there is nothing to read the ordinary day off:\n%s", res.Content)
	}
	if txs[0].Date != "05-14-52" || txs[0].Comment != "sage leg" {
		t.Fatalf("an ordinary transaction must go out untouched, got dated %s comment %q", txs[0].Date, txs[0].Comment)
	}
}
