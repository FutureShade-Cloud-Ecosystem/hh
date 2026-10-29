//go:build integration

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// TestIntegration_W29A_FinanceCheckAPTieNamesTheManualEntry pins the box
// check's AP tie (W2-9a, FROM-PRINCIPAL 124): a 2100 entry that no vendor
// bill, PayBill payment or landed cost made is a tie difference, never an
// aging row, and the failing line names the journal the Controller
// reconciles.
func TestIntegration_W29A_FinanceCheckAPTieNamesTheManualEntry(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "w29apt")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("read embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("apply seed: %v", err)
	}
	const branchTRT = "5d1b3f6e-0000-4000-9000-000000000001"

	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	nowLocal := time.Now().In(zone)
	mustExec(t, ctx, pool, `INSERT INTO fiscal_periods (year, month) VALUES ($1, $2) ON CONFLICT (year, month) DO NOTHING`,
		nowLocal.Year(), int(nowLocal.Month()))

	glRepo := gl.NewRepository(pool).WithZone(newDealerZone(pool))
	glSvc := gl.NewService(glRepo)
	seams := TieSeams{Querier: pool, GLStore: glRepo, AP: financeCheckAPAging(pool)}
	month := func() TieMonth {
		return TieMonth{Year: nowLocal.Year(), Month: int(nowLocal.Month()), MonthEnd: time.Now().Add(time.Minute)}
	}

	before, err := TieAPAgingToGL2100(ctx, seams, month())
	if err != nil {
		t.Fatalf("the tie before: %v", err)
	}
	if !before.Applicable {
		t.Fatalf("the AP tie must apply when the aging is wired: %+v", before)
	}

	const memo = "W29A probe accrual"
	if _, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source:         "MANUAL",
		IdempotencyKey: "w29a:probe-accrual",
		Memo:           memo,
		BranchID:       branchTRT,
		Lines: []gl.ExternalLine{
			{AccountCode: "6200", DebitCents: 3000, Memo: memo},
			{AccountCode: "2100", CreditCents: 3000, Memo: memo},
		},
	}); err != nil {
		t.Fatalf("post the manual accrual: %v", err)
	}

	after, err := TieAPAgingToGL2100(ctx, seams, month())
	if err != nil {
		t.Fatalf("the tie after: %v", err)
	}
	if after.Pass {
		t.Fatalf("a manual 2100 accrual must break the AP tie: %+v", after)
	}
	if got := after.VarianceCents - before.VarianceCents; got != -3000 {
		t.Fatalf("the accrual moved the variance by %d, want -3000 (the aging is the subledger and does not carry it)", got)
	}
	if !strings.Contains(after.Detail, memo) || !strings.Contains(after.Detail, "MANUAL") {
		t.Fatalf("the failing line must name the journal the Controller reconciles: %q", after.Detail)
	}
}
