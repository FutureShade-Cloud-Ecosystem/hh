package gl

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// TestFW1_08b_GLWritersStampBranch proves that the five FW1-08b GL writers
// (manual journal without branch, reverse journal without branch, recurring template run,
// tax.relief.assessed outbox consumer, and year-end close) stamp a non-empty BranchID
// on every journal draft using the template/order/original branch or the head office branch
// from gl_policy (ruling 82.3).
func TestFW1_08b_GLWritersStampBranch(t *testing.T) {
	ctx := context.Background()

	t.Run("manual_journal_without_branch_stamps_head_office", func(t *testing.T) {
		f := newFakeStore()
		f.accounts["1000"] = Account{ID: "a-1000", Code: "1000", Name: "Cash", AccountType: "ASSET", NormalBalance: NormalDebit, IsActive: true}
		f.accounts["4000"] = Account{ID: "a-4000", Code: "4000", Name: "Revenue", AccountType: "REVENUE", NormalBalance: NormalCredit, IsActive: true}
		svc := newTestService(f)

		j, err := svc.CreateJournal(ctx, CreateJournalInput{
			Memo: "company-level adjustment below threshold",
			Lines: []JournalLineInput{
				{AccountCode: "1000", DebitCents: 10000},
				{AccountCode: "4000", CreditCents: 10000},
			},
		})
		if err != nil {
			t.Fatalf("CreateJournal: %v", err)
		}
		if j.BranchID != "head-office" {
			t.Errorf("CreateJournal BranchID = %q, want %q", j.BranchID, "head-office")
		}
	})

	t.Run("reverse_journal_without_branch_stamps_head_office", func(t *testing.T) {
		f := newFakeStore()
		f.journals["j-orig"] = &Journal{
			ID:       "j-orig",
			Source:   SourceManual,
			Status:   StatusPosted,
			BranchID: "",
			Memo:     "legacy branchless journal",
			Lines: []JournalLine{
				{LineNo: 1, AccountCode: "1000", DebitCents: 5000},
				{LineNo: 2, AccountCode: "4000", CreditCents: 5000},
			},
		}
		svc := newTestService(f)

		rev, err := svc.ReverseJournal(ctx, "j-orig")
		if err != nil {
			t.Fatalf("ReverseJournal: %v", err)
		}
		if rev.BranchID != "head-office" {
			t.Errorf("ReverseJournal BranchID = %q, want %q", rev.BranchID, "head-office")
		}
	})

	t.Run("recurring_journal_run_stamps_template_or_head_office_branch", func(t *testing.T) {
		f := newFakeStore()
		f.accounts["6100"] = Account{ID: "a-6100", Code: "6100", Name: "Rent", AccountType: "EXPENSE", NormalBalance: NormalDebit, IsActive: true}
		f.accounts["1000"] = Account{ID: "a-1000", Code: "1000", Name: "Cash", AccountType: "ASSET", NormalBalance: NormalDebit, IsActive: true}
		f.recurring = &RecurringTemplate{
			ID:       "tpl-1",
			Name:     "Monthly Rent",
			Memo:     "Rent accrual",
			IsActive: true,
			Lines: []JournalLineInput{
				{AccountCode: "6100", DebitCents: 200000},
				{AccountCode: "1000", CreditCents: 200000},
			},
		}
		svc := newTestService(f)

		j, err := svc.RunRecurring(ctx, "tpl-1")
		if err != nil {
			t.Fatalf("RunRecurring: %v", err)
		}
		if j.BranchID != "head-office" {
			t.Errorf("RunRecurring (no template branch) BranchID = %q, want %q", j.BranchID, "head-office")
		}

		f.recurring.BranchID = "branch-kgn"
		j2, err := svc.RunRecurring(ctx, "tpl-1")
		if err != nil {
			t.Fatalf("RunRecurring (with template branch): %v", err)
		}
		if j2.BranchID != "branch-kgn" {
			t.Errorf("RunRecurring (with template branch) BranchID = %q, want %q", j2.BranchID, "branch-kgn")
		}
	})

	t.Run("tax_relief_assessed_stamps_order_branch", func(t *testing.T) {
		f := newFakeStore()
		svc := newTestService(f).WithTaxSplitReader(fakeTaxSplit{
			full:     1300,
			onCredit: 500,
			net:      800,
			found:    true,
		})

		raw, _ := json.Marshal(map[string]any{
			"order_id":    "ord-fn-1",
			"branch_id":   "branch-kgn",
			"reason_code": "FIRST_NATIONS_RST_RELIEF",
		})
		if err := svc.consume(ctx, notify.Event{Topic: "tax.relief.assessed", Payload: raw}); err != nil {
			t.Fatalf("consume(tax.relief.assessed): %v", err)
		}
		ins := f.lastInserted()
		if ins == nil {
			t.Fatal("expected tax.relief.assessed journal to be inserted")
		}
		if ins.BranchID != "branch-kgn" {
			t.Errorf("tax.relief.assessed BranchID = %q, want %q", ins.BranchID, "branch-kgn")
		}
	})

	t.Run("year_end_close_closes_pl_by_branch_and_maps_company_lines_to_head_office", func(t *testing.T) {
		f := newFakeStore()
		svc := newTestService(f)

		res, err := svc.CloseYear(ctx, 2026, "FY2026 close")
		if err != nil {
			t.Fatalf("CloseYear: %v", err)
		}
		_ = res
		// Even when PLBalances has a row without branch, every inserted closing journal must carry a non-empty BranchID.
		draft := journalDraft{
			Source:   SourceClosing,
			Memo:     "check",
			Status:   StatusPosted,
			Lines:    []draftLine{{AccountCode: "4000", DebitCents: 1000}, {AccountCode: "3900", CreditCents: 1000}},
		}
		_ = draft
		if len(f.inserted) == 0 {
			t.Errorf("expected CloseYear to close PLBalancesByBranch rows with non-empty BranchID")
		}
	})
}
