package gl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

type fakeAuditRecorder struct {
	entries []audit.SensitiveAccess
}

func (r *fakeAuditRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.entries = append(r.entries, a)
	return nil
}

type fakeTaxSplitWithBranch struct {
	fakeTaxSplit
	branchID string
}

func (f fakeTaxSplitWithBranch) OrderBranch(_ context.Context, _ string) (string, error) {
	return f.branchID, nil
}

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
		svc := newTestService(f).WithTaxSplitReader(fakeTaxSplitWithBranch{
			fakeTaxSplit: fakeTaxSplit{
				full:     1300,
				onCredit: 500,
				net:      800,
				found:    true,
			},
			branchID: "branch-osh",
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

		// Legacy/replayed payload without branch_id falls back to OrderBranchForGL.
		rawNoBranch, _ := json.Marshal(map[string]any{
			"order_id":    "ord-fn-2",
			"reason_code": "FIRST_NATIONS_RST_RELIEF",
		})
		if err := svc.consume(ctx, notify.Event{Topic: "tax.relief.assessed", Payload: rawNoBranch}); err != nil {
			t.Fatalf("consume(tax.relief.assessed without payload branch): %v", err)
		}
		ins2 := f.lastInserted()
		if ins2 == nil || ins2.BranchID != "branch-osh" {
			t.Errorf("tax.relief.assessed fallback BranchID = %+v, want branch-osh", ins2)
		}
	})

	t.Run("year_end_close_closes_pl_by_branch_and_maps_company_lines_to_head_office", func(t *testing.T) {
		f := newFakeStore()
		f.plBranchRows = []PLBranchRow{
			{AccountCode: "4000", AccountType: "REVENUE", NormalBalance: NormalCredit, BranchID: "head-office", NetDebitCents: -100000},
			{AccountCode: "5000", AccountType: "EXPENSE", NormalBalance: NormalDebit, BranchID: "head-office", NetDebitCents: 40000},
			{AccountCode: "6100", AccountType: "EXPENSE", NormalBalance: NormalDebit, BranchID: "", NetDebitCents: 10000},
			{AccountCode: "4000", AccountType: "REVENUE", NormalBalance: NormalCredit, BranchID: "branch-kgn", NetDebitCents: -50000},
			{AccountCode: "5000", AccountType: "EXPENSE", NormalBalance: NormalDebit, BranchID: "branch-kgn", NetDebitCents: 20000},
		}
		svc := newTestService(f)

		res, err := svc.CloseYear(ctx, 2026, "FY2026 close")
		if err != nil {
			t.Fatalf("CloseYear: %v", err)
		}
		// Net income: head-office (100000 - 40000 - 10000 = 50000) + branch-kgn (50000 - 20000 = 30000) = 80000.
		if res.NetIncomeCents != 80000 {
			t.Errorf("CloseYear NetIncomeCents = %d, want 80000", res.NetIncomeCents)
		}
		if len(f.inserted) != 2 {
			t.Fatalf("expected 2 per-branch closing journals, got %d", len(f.inserted))
		}
		if f.inserted[0].BranchID != "head-office" || !f.inserted[0].balanced() {
			t.Errorf("closing journal[0] = %+v, want balanced head-office closing journal", f.inserted[0])
		}
		if f.inserted[1].BranchID != "branch-kgn" || !f.inserted[1].balanced() {
			t.Errorf("closing journal[1] = %+v, want balanced branch-kgn closing journal", f.inserted[1])
		}
	})

	t.Run("set_head_office_branch_controller_only_reason_required_and_audited", func(t *testing.T) {
		f := newFakeStore()
		aud := &fakeAuditRecorder{}

		// Non-controller is refused 403.
		mgrSvc := newTestService(f).WithAudit(aud)
		err := mgrSvc.SetHeadOfficeBranch(ctx, "branch-kgn", "relocate head office")
		var ce clientError
		if !errors.As(err, &ce) || ce.status != 403 {
			t.Fatalf("want 403 for non-controller SetHeadOfficeBranch, got %v", err)
		}

		// Controller without reason is refused 422.
		ctrlCaller := identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Scope: identity.ScopeGlobal}
		ctrlSvc := newTestServiceAs(f, ctrlCaller).WithAudit(aud)
		err = ctrlSvc.SetHeadOfficeBranch(ctx, "branch-kgn", "   ")
		if !errors.As(err, &ce) || ce.status != 422 {
			t.Fatalf("want 422 for empty reason on SetHeadOfficeBranch, got %v", err)
		}

		// Controller with reason updates HeadOfficeBranchID and records audit.
		if err := ctrlSvc.SetHeadOfficeBranch(ctx, "branch-kgn", "Ruling 82.3 head office update"); err != nil {
			t.Fatalf("SetHeadOfficeBranch: %v", err)
		}
		ho, err := ctrlSvc.HeadOfficeBranchID(ctx)
		if err != nil || ho != "branch-kgn" {
			t.Fatalf("HeadOfficeBranchID = %q (err=%v), want branch-kgn", ho, err)
		}
		if len(aud.entries) != 1 || aud.entries[0].Action != actGLHeadOfficeBranchSet || aud.entries[0].BranchID != "branch-kgn" {
			t.Fatalf("audit entries = %+v, want 1 %s entry for branch-kgn", aud.entries, actGLHeadOfficeBranchSet)
		}
	})
}

// TestFW1_08b_PartB_HeadOfficeSetChecksItsCandidate proves the part B rules on the head office
// set (FROM-PRINCIPAL 176): the candidate must be a branch row of the caller's org and is refused
// in words otherwise, and the non-controller refusal carries the set's own words, not the reopen
// ones (FROM-PRINCIPAL 206).
func TestFW1_08b_PartB_HeadOfficeSetChecksItsCandidate(t *testing.T) {
	ctx := context.Background()
	aud := &fakeAuditRecorder{}

	t.Run("another_orgs_branch_refused_in_words", func(t *testing.T) {
		f := newFakeStore()
		ctrl := identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Scope: identity.ScopeGlobal}
		svc := newTestServiceAs(f, ctrl).WithAudit(aud)
		scoped := authz.WithStaffScope(ctx, authz.StaffScope{OrgID: "org-a"})
		err := svc.SetHeadOfficeBranch(scoped, "branch-foreign-org", "part B: another org's branch")
		var ce clientError
		if !errors.As(err, &ce) || ce.status != 403 {
			t.Fatalf("want 403 client refusal for another org's branch, got %v", err)
		}
	})

	t.Run("non_branch_location_refused_in_words", func(t *testing.T) {
		f := newFakeStore()
		ctrl := identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Scope: identity.ScopeGlobal}
		svc := newTestServiceAs(f, ctrl).WithAudit(aud)
		scoped := authz.WithStaffScope(ctx, authz.StaffScope{OrgID: "org-a"})
		err := svc.SetHeadOfficeBranch(scoped, "loc-zone-1", "part B: a zone is not a branch")
		var ce clientError
		if !errors.As(err, &ce) || ce.status != 422 {
			t.Fatalf("want 422 client refusal for a non branch location, got %v", err)
		}
	})

	t.Run("the_non_controller_words_name_the_set_not_the_reopen", func(t *testing.T) {
		f := newFakeStore()
		mgrSvc := newTestService(f).WithAudit(aud)
		err := mgrSvc.SetHeadOfficeBranch(ctx, "branch-kgn", "part B: the set's own words")
		var ce clientError
		if !errors.As(err, &ce) || ce.msg != "gl: only the Financial Controller may set the head office branch" {
			t.Fatalf("want the head office set's own refusal words, got %q (err=%v)", ce.msg, err)
		}
	})
}
