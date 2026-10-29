//go:build integration

package tax

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	gl "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func hst1Ensure2092(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month) SELECT 2092, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`); err != nil {
		t.Fatalf("ensure periods 2092: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id=j.id AND j.fiscal_year=2092`)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE fiscal_year=2092`)
		_, _ = pool.Exec(bg, `DELETE FROM fiscal_periods WHERE year=2092`)
	})
}

func hst1Post2092(t *testing.T, pool *pgxpool.Pool, month int, memo string, lines []fw11Line) {
	t.Helper()
	ctx := context.Background()
	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year=2092 AND month=$1`, month).Scan(&periodID); err != nil {
		t.Fatalf("period 2092-%02d: %v", month, err)
	}
	var jid string
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id)
		VALUES (2092, (SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year=2092),
		        $1::uuid, 'ADJUSTMENT', $2, 'POSTED', $3, $4::uuid)
		RETURNING id::text`,
		periodID, memo, time.Date(2092, time.Month(month), 20, 12, 0, 0, 0, time.UTC), fw11TRT).Scan(&jid); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("plant journal: %v", err)
	}
	for _, l := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, branch_id, memo)
			SELECT $1::uuid, (SELECT COALESCE(MAX(line_no),0)+1 FROM gl_journal_lines WHERE journal_id=$1::uuid),
			       a.id, NULLIF($2,0), NULLIF($3,0), $6::uuid, $4 FROM gl_accounts a WHERE a.code = $5`,
			jid, l.debit, l.credit, memo, l.code, fw11TRT); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("plant line %s: %v", l.code, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit journal: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id=$1`, jid)
		_, _ = pool.Exec(bg, `DELETE FROM gl_journals WHERE id=$1`, jid)
	})
}

// TestIntegration_HST1_WorksheetAllFiveLegs exercises the five red legs on a migrated and seeded DB:
//  1. Line 109 equals line 105 less line 108.
//  2. Line 105 less line 108 equals GL 2200's net movement for the period.
//  3. The period follows the setting: a quarter is three months; monthly and yearly change only the dates.
//  4. A credit memo's HST reduces line 103.
//  5. A vendor bill's ITC raises line 106.
func TestIntegration_HST1_WorksheetAllFiveLegs(t *testing.T) {
	pool := intPool(t)
	t.Cleanup(pool.Close)
	hst1Ensure2092(t, pool)
	ctx := context.Background()

	_, _ = pool.Exec(ctx, `DELETE FROM tax_config WHERE branch_id = $1::uuid`, fw11TRT)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tax_config WHERE branch_id = $1::uuid`, fw11TRT)
	})

	glSvc := gl.NewService(gl.NewRepository(pool))
	glSeam := fw11GLBal{svc: glSvc}
	svc := intSvc(t, pool).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithReturnHSTs(pos.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(glSeam)

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
		t.Skipf("no seeded customer: %v", err)
	}
	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors ORDER BY id LIMIT 1`).Scan(&vendorID); err != nil {
		t.Skipf("no seeded vendor: %v", err)
	}

	// Plant a customer sale invoice in 2092-05: subtotal 100000, HST 13000, total 113000.
	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES ('HST1-INV-2092', $1::uuid, $2::uuid, 'FINALIZED', '2092-05-10', '2092-05-10',
		        100000, 13000, 113000, 113000, '2092-05-10T12:00:00Z', 'INVOICE')
		RETURNING id::text`,
		customerID, fw11TRT).Scan(&invID); err != nil {
		t.Fatalf("plant invoice: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM invoices WHERE id=$1`, invID)
	})
	hst1Post2092(t, pool, 5, "HST1 sale invoice", []fw11Line{
		{"1100", 113000, 0},
		{"4000", 0, 100000},
		{"2200", 0, 13000},
	})

	wsInitial, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2092, Quarter: 2, Month: 5, BranchID: fw11TRT})
	if err != nil {
		t.Fatalf("initial WorksheetReport: %v", err)
	}
	if wsInitial.Line103CollectedCents != 13000 {
		t.Errorf("initial line 103 = %d, want 13000", wsInitial.Line103CollectedCents)
	}

	// Leg 4: Plant a credit memo in 2092-05: subtotal -20000, HST -2600, total -22600.
	var cmID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type, reverses_invoice_id)
		VALUES ('HST1-CM-2092', $1::uuid, $2::uuid, 'FINALIZED', '2092-05-15', '2092-05-15',
		        -20000, -2600, -22600, 0, '2092-05-15T12:00:00Z', 'CREDIT_MEMO', $3::uuid)
		RETURNING id::text`,
		customerID, fw11TRT, invID).Scan(&cmID); err != nil {
		t.Fatalf("plant credit memo: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM invoices WHERE id=$1`, cmID)
	})
	hst1Post2092(t, pool, 5, "HST1 credit memo", []fw11Line{
		{"4000", 20000, 0},
		{"2200", 2600, 0},
		{"1100", 0, 22600},
	})

	wsAfterCM, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2092, Quarter: 2, Month: 5, BranchID: fw11TRT})
	if err != nil {
		t.Fatalf("after credit memo WorksheetReport: %v", err)
	}
	if wsAfterCM.Line103CollectedCents != 10400 {
		t.Errorf("Leg 4: credit memo HST (-2600) must reduce line 103 from 13000 to 10400, got %d", wsAfterCM.Line103CollectedCents)
	}
	if wsAfterCM.Line106ITCsCents != 0 {
		t.Errorf("before vendor bill, line 106 = %d, want 0", wsAfterCM.Line106ITCsCents)
	}

	// Leg 5: Plant a posted vendor bill in 2092-05: subtotal 30000, ITC 3900, total 33900.
	var billID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO purchasing_bills (bill_no, vendor_id, branch_id, status, match_state, bill_date,
		                              subtotal_cents, tax_cents, total_cents, posted_at)
		VALUES ('HST1-BILL-2092', $1::uuid, $2::uuid, 'POSTED', 'MATCHED', '2092-05-20', 30000, 3900, 33900, $3)
		RETURNING id::text`,
		vendorID, fw11TRT, time.Date(2092, 5, 21, 12, 0, 0, 0, time.UTC)).Scan(&billID); err != nil {
		t.Fatalf("plant vendor bill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM purchasing_bills WHERE id=$1`, billID)
	})
	hst1Post2092(t, pool, 5, "HST1 vendor bill ITC", []fw11Line{
		{"5250", 30000, 0},
		{"2200", 3900, 0},
		{"2100", 0, 33900},
	})

	wsQtr, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2092, Quarter: 2, Month: 5, BranchID: fw11TRT})
	if err != nil {
		t.Fatalf("quarterly WorksheetReport: %v", err)
	}
	if wsQtr.Line106ITCsCents != 3900 {
		t.Errorf("Leg 5: vendor bill ITC (3900) must raise line 106 from 0 to 3900, got %d", wsQtr.Line106ITCsCents)
	}
	if wsQtr.Line101SalesCents != 80000 {
		t.Errorf("line 101 revenue = %d, want 80000 (100000 sale less 20000 credit memo)", wsQtr.Line101SalesCents)
	}

	// Leg 1: Line 109 equals line 105 less line 108.
	if wsQtr.Line105TotalHSTCents != 10400 || wsQtr.Line108TotalITCsCents != 3900 || wsQtr.Line109NetTaxCents != 6500 ||
		wsQtr.Line109NetTaxCents != (wsQtr.Line105TotalHSTCents-wsQtr.Line108TotalITCsCents) {
		t.Errorf("Leg 1: line 105 = %d, line 108 = %d, line 109 = %d, want 10400, 3900, 6500",
			wsQtr.Line105TotalHSTCents, wsQtr.Line108TotalITCsCents, wsQtr.Line109NetTaxCents)
	}

	// Leg 2: Line 105 less line 108 equals GL 2200's net movement for the period.
	if (wsQtr.Line105TotalHSTCents-wsQtr.Line108TotalITCsCents) != wsQtr.GLPayableCents || wsQtr.GLPayableCents != 6500 || wsQtr.VarianceCents != 0 || !wsQtr.TiedOut {
		t.Errorf("Leg 2: line 105 less line 108 = %d, GL 2200 movement = %d, variance = %d, tied_out = %v, want 6500/6500/0/true",
			wsQtr.Line105TotalHSTCents-wsQtr.Line108TotalITCsCents, wsQtr.GLPayableCents, wsQtr.VarianceCents, wsQtr.TiedOut)
	}

	// Leg 3: The period follows the setting: a quarter is three months; monthly and yearly change only the dates.
	if wsQtr.FilingFrequency != "QUARTERLY" || wsQtr.FromDate != "2092-04-01" || wsQtr.ToDate != "2092-06-30" {
		t.Errorf("Leg 3 quarterly: freq=%q from=%q to=%q, want QUARTERLY 2092-04-01..2092-06-30",
			wsQtr.FilingFrequency, wsQtr.FromDate, wsQtr.ToDate)
	}

	if _, err := svc.UpdateConfig(ctx, Config{BranchID: fw11TRT, RoundMode: "INVOICE_TOTAL", FilingFrequency: "MONTHLY"}); err != nil {
		t.Fatalf("UpdateConfig MONTHLY: %v", err)
	}
	wsMon, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2092, Quarter: 2, Month: 5, BranchID: fw11TRT})
	if err != nil {
		t.Fatalf("monthly WorksheetReport: %v", err)
	}
	if wsMon.FilingFrequency != "MONTHLY" || wsMon.FromDate != "2092-05-01" || wsMon.ToDate != "2092-05-31" {
		t.Errorf("Leg 3 monthly: freq=%q from=%q to=%q, want MONTHLY 2092-05-01..2092-05-31",
			wsMon.FilingFrequency, wsMon.FromDate, wsMon.ToDate)
	}
	if wsMon.Line101SalesCents != wsQtr.Line101SalesCents ||
		wsMon.Line103CollectedCents != wsQtr.Line103CollectedCents ||
		wsMon.Line106ITCsCents != wsQtr.Line106ITCsCents ||
		wsMon.Line109NetTaxCents != wsQtr.Line109NetTaxCents ||
		wsMon.GLPayableCents != wsQtr.GLPayableCents {
		t.Errorf("Leg 3 monthly figures diverged from quarterly: got %+v, want %+v", wsMon, wsQtr)
	}

	if _, err := svc.UpdateConfig(ctx, Config{BranchID: fw11TRT, RoundMode: "INVOICE_TOTAL", FilingFrequency: "YEARLY"}); err != nil {
		t.Fatalf("UpdateConfig YEARLY: %v", err)
	}
	wsYr, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2092, Quarter: 2, Month: 5, BranchID: fw11TRT})
	if err != nil {
		t.Fatalf("yearly WorksheetReport: %v", err)
	}
	if wsYr.FilingFrequency != "YEARLY" || wsYr.FromDate != "2092-01-01" || wsYr.ToDate != "2092-12-31" {
		t.Errorf("Leg 3 yearly: freq=%q from=%q to=%q, want YEARLY 2092-01-01..2092-12-31",
			wsYr.FilingFrequency, wsYr.FromDate, wsYr.ToDate)
	}
	if wsYr.Line101SalesCents != wsQtr.Line101SalesCents ||
		wsYr.Line103CollectedCents != wsQtr.Line103CollectedCents ||
		wsYr.Line106ITCsCents != wsQtr.Line106ITCsCents ||
		wsYr.Line109NetTaxCents != wsQtr.Line109NetTaxCents ||
		wsYr.GLPayableCents != wsQtr.GLPayableCents {
		t.Errorf("Leg 3 yearly figures diverged from quarterly: got %+v, want %+v", wsYr, wsQtr)
	}

	if !strings.Contains(wsYr.ManualEntryNote, "entered by hand") {
		t.Errorf("manual_entry_note = %q, want wording naming lines entered by hand", wsYr.ManualEntryNote)
	}
}

func mountedWorksheetGet(t *testing.T, pool *pgxpool.Pool, caller identity.StaffContext, path string) *httptest.ResponseRecorder {
	t.Helper()
	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithSalesHSTs(billing.NewRepository(pool)).
		WithReturnHSTs(pos.NewRepository(pool)).
		WithBillITCs(purchasing.NewRepository(pool)).
		WithGLBalances(fw11GLBal{svc: glSvc})
	h := NewHandler(svc, slog.New(slog.DiscardHandler))

	sg := testkit.NewStaffGuards(t)
	roleID, _ := identity.CanonicalForERPCode(caller.RoleCode)
	sg.SetAuthority(auth.StaffAuthority{
		RoleID:   roleID,
		Tier:     int(caller.Tier),
		Scope:    identity.CanonicalScope(caller.Scope),
		Branches: fenceBranchList(caller.Branches),
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func TestIntegration_HST1_WorksheetRouteAndBranchFence(t *testing.T) {
	pool := intPool(t)
	t.Cleanup(pool.Close)
	hst1Ensure2092(t, pool)
	ctx := context.Background()

	_, _ = pool.Exec(ctx, `DELETE FROM tax_config WHERE branch_id = $1::uuid`, fenceTrenton)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tax_config WHERE branch_id = $1::uuid`, fenceTrenton)
	})

	kingston := identity.StaffContext{
		UserID: fenceSeedUser(t, pool), RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{fenceKingston: true},
	}
	controller := identity.StaffContext{
		UserID: fenceSeedUser(t, pool), RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}

	if got := mountedWorksheetGet(t, pool, kingston, "/api/v1/tax/worksheet?year=2092&quarter=2&branch_id="+fenceTrenton).Code; got != http.StatusForbidden {
		t.Errorf("GET Trenton's worksheet as a Kingston-only manager = %d, want 403", got)
	}
	if got := mountedWorksheetGet(t, pool, kingston, "/api/v1/tax/worksheet?year=2092&quarter=2").Code; got != http.StatusForbidden {
		t.Errorf("GET the consolidated worksheet as a Kingston-only manager = %d, want 403", got)
	}
	if got := mountedWorksheetGet(t, pool, kingston, "/api/v1/tax/worksheet?year=2092&quarter=2&branch_id="+fenceKingston).Code; got != http.StatusOK {
		t.Errorf("GET Kingston's own worksheet as a Kingston-only manager = %d, want 200", got)
	}

	rec := mountedWorksheetGet(t, pool, controller, "/api/v1/tax/worksheet?year=2092&quarter=2&branch_id="+fenceTrenton)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET Trenton's worksheet as the Controller = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var ws Worksheet
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("unmarshal worksheet: %v", err)
	}
	if ws.FilingFrequency != "QUARTERLY" || ws.FromDate != "2092-04-01" || ws.ToDate != "2092-06-30" || !ws.TiedOut {
		t.Errorf("worksheet response = %+v, want QUARTERLY 2092-04-01..2092-06-30 tied_out=true", ws)
	}
}
