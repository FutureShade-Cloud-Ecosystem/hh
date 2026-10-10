package gl

// service_test.go, DB-free unit tests for the journal lifecycle + CoA rules (the internal/till service_test
// pattern: fake store + fake caller, no database). Covers A-01 AC1 (post-journal invariants), AC4 (maker-
// checker), AC10 (deactivate 409), AC11 (net-zero reversing JE).

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func balancedLines() []JournalLineInput {
	return []JournalLineInput{
		{AccountCode: "1000", DebitCents: 10000},
		{AccountCode: "4000", CreditCents: 10000},
	}
}

// AC1, a manual journal must balance (Σdebit = Σcredit) and carry ≥2 lines; a balanced entry below the
// gl_policy threshold posts straight through.
func TestAC1_PostJournalInvariants(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f)

	// Unbalanced → 422.
	_, err := svc.CreateJournal(ctx, CreateJournalInput{Memo: "bad", Lines: []JournalLineInput{
		{AccountCode: "1000", DebitCents: 10000},
		{AccountCode: "4000", CreditCents: 9000},
	}})
	if !errors.Is(err, errUnbalanced) {
		t.Fatalf("unbalanced journal: want errUnbalanced, got %v", err)
	}

	// Fewer than two lines → 400.
	_, err = svc.CreateJournal(ctx, CreateJournalInput{Memo: "one", Lines: []JournalLineInput{{AccountCode: "1000", DebitCents: 1}}})
	if err == nil {
		t.Fatal("single-line journal should be rejected")
	}

	// Balanced, below threshold (500000) → posts.
	j, err := svc.CreateJournal(ctx, CreateJournalInput{Memo: "ok", Lines: balancedLines()})
	if err != nil {
		t.Fatalf("balanced journal: %v", err)
	}
	if j.Status != StatusPosted {
		t.Fatalf("below-threshold journal should POST, got status=%s derived=%s", j.Status, j.DerivedStatus)
	}
}

// AC4, maker-checker: a journal at/above the gl_policy threshold parks as PENDING_APPROVAL (DRAFT), the
// creator may NOT approve their own entry (four-eyes), and a different manager's approval posts it.
func TestAC4_MakerChecker(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.policy.JEApprovalThresholdCents = 500000

	maker := identity.StaffContext{UserID: "u-maker", RoleCode: "SALES_MANAGER", Tier: identity.TierManager}
	makerSvc := newTestServiceAs(f, maker)

	// $6,000 > $5,000 threshold → parks as PENDING_APPROVAL.
	j, err := makerSvc.CreateJournal(ctx, CreateJournalInput{Memo: "big", Lines: []JournalLineInput{
		{AccountCode: "1000", DebitCents: 600000},
		{AccountCode: "4000", CreditCents: 600000},
	}})
	if err != nil {
		t.Fatalf("create over-threshold: %v", err)
	}
	if j.Status != StatusDraft || j.DerivedStatus != StatusPendingApproval {
		t.Fatalf("over-threshold journal should be DRAFT/PENDING_APPROVAL, got %s/%s", j.Status, j.DerivedStatus)
	}

	// The maker cannot approve their own entry.
	if _, err := makerSvc.ApproveJournal(ctx, j.ID); !errors.Is(err, errFourEyes) {
		t.Fatalf("self-approval: want errFourEyes, got %v", err)
	}

	// A different manager approves → POSTED with approved_by stamped.
	checker := identity.StaffContext{UserID: "u-checker", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}
	checkerSvc := newTestServiceAs(f, checker)
	approved, err := checkerSvc.ApproveJournal(ctx, j.ID)
	if err != nil {
		t.Fatalf("checker approve: %v", err)
	}
	if approved.Status != StatusPosted {
		t.Fatalf("approved journal should be POSTED, got %s", approved.Status)
	}
	if approved.ApprovedBy != "u-checker" {
		t.Fatalf("approved_by should be the checker, got %q", approved.ApprovedBy)
	}
}

// The approve route's money guard: only an approved request for the journal's amount posts a journal over
// the threshold; every other state, a failed read and an unwired guard refuse, and nothing is posted.
func TestAC4_ApproveRefusesEveryStateButApproved(t *testing.T) {
	ctx := context.Background()
	checker := identity.StaffContext{UserID: "u-checker", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}
	for name, guard := range map[string]ApprovalStateChecker{
		"pending":        fakeHold{state: ApprovalPending},
		"rejected":       fakeHold{state: ApprovalRejected},
		"deleted":        fakeHold{state: ApprovalDeleted},
		"amount differs": fakeHold{state: ApprovalAmountDiffers},
		"none":           fakeHold{state: ApprovalNone},
		"unknown word":   fakeHold{state: "other"},
		"read fail":      fakeHold{err: errors.New("db down")},
		"unwired":        nil,
	} {
		f := newFakeStore()
		f.policy.JEApprovalThresholdCents = 500000
		maker := identity.StaffContext{UserID: "u-maker", RoleCode: "SALES_MANAGER", Tier: identity.TierManager}
		j, err := newTestServiceAs(f, maker).CreateJournal(ctx, CreateJournalInput{Memo: "big", Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: 600000},
			{AccountCode: "4000", CreditCents: 600000},
		}})
		if err != nil {
			t.Fatalf("%s: create: %v", name, err)
		}
		svc := newTestServiceAs(f, checker)
		svc.approvalChecker = guard
		if _, err := svc.ApproveJournal(ctx, j.ID); err == nil {
			t.Errorf("%s: the approve route posted the journal", name)
		}
		if got, _ := f.GetJournal(ctx, j.ID); got.Status != StatusDraft {
			t.Errorf("%s: the journal reads %s, want DRAFT", name, got.Status)
		}
	}
}

// Under the threshold a journal with no request needs none (a recurring run's small accrual); at or over
// it, no request refuses.
func TestAC4_ApproveNoRequestNeedsNoneOnlyUnderTheThreshold(t *testing.T) {
	ctx := context.Background()
	checker := identity.StaffContext{UserID: "u-checker", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}
	for amount, wantOK := range map[int64]bool{100000: true, 500000: false, 600000: false} {
		f := newFakeStore()
		f.policy.JEApprovalThresholdCents = 500000
		f.journals["j-x"] = &Journal{ID: "j-x", Status: StatusDraft, CreatedBy: "u-maker", Lines: []JournalLine{{DebitCents: amount}, {CreditCents: amount}}}
		svc := newTestServiceAs(f, checker)
		svc.approvalChecker = fakeHold{state: ApprovalNone}
		_, err := svc.ApproveJournal(ctx, "j-x")
		if (err == nil) != wantOK {
			t.Errorf("amount %d with no request: err=%v, want ok=%v", amount, err, wantOK)
		}
	}
}

// A recurring run lands a DRAFT with no branch and files no request (a filing needs a branch), so under the
// ruling the GL route refuses it when it is at or over the threshold: the run is stranded until a branch
// carrying path exists. Under the threshold it posts as before.
func TestAC12_RecurringRunOverTheThresholdIsRefusedByTheApproveRoute(t *testing.T) {
	ctx := context.Background()
	maker := identity.StaffContext{UserID: "u-maker", RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership}
	checker := identity.StaffContext{UserID: "u-checker", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership}
	for amount, wantOK := range map[int64]bool{100000: true, 600000: false} {
		f := newFakeStore()
		f.policy.JEApprovalThresholdCents = 500000
		f.recurring = &RecurringTemplate{ID: "t1", Name: "rent", Lines: []JournalLineInput{
			{AccountCode: "1000", DebitCents: amount}, {AccountCode: "4000", CreditCents: amount}}}
		filer := &fakeApprovalFiler{}
		j, err := newTestServiceAs(f, maker).WithApprovalFiler(filer).RunRecurring(ctx, "t1")
		if err != nil {
			t.Fatalf("run recurring: %v", err)
		}
		if len(filer.calls) != 0 {
			t.Fatalf("a recurring run filed %d requests; it carries no branch to file under", len(filer.calls))
		}
		svc := newTestServiceAs(f, checker)
		svc.approvalChecker = fakeHold{state: ApprovalNone}
		_, err = svc.ApproveJournal(ctx, j.ID)
		if (err == nil) != wantOK {
			t.Errorf("recurring journal of %d: approve err=%v, want ok=%v", amount, err, wantOK)
		}
	}
}

// AC10, an account referenced by a DRAFT journal cannot be deactivated (409); with no draft refs it flips
// inactive.
func TestAC10_DeactivateAccountInUse(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.accounts["6100"] = Account{ID: "a1", Code: "6100", Name: "Rent", AccountType: "EXPENSE", NormalBalance: "DEBIT", IsActive: true}
	svc := newTestService(f)

	f.draftRefs["6100"] = 2
	if _, err := svc.DeactivateAccount(ctx, "6100"); !errors.Is(err, errAccountInUse) {
		t.Fatalf("account with draft refs: want errAccountInUse, got %v", err)
	}

	f.draftRefs["6100"] = 0
	a, err := svc.DeactivateAccount(ctx, "6100")
	if err != nil {
		t.Fatalf("deactivate with no refs: %v", err)
	}
	if a.IsActive {
		t.Fatal("account should be inactive after deactivate")
	}
}

// AC11, reversing a POSTED journal posts a net-zero mirror: every leg's debit/credit is swapped, so each
// account nets to zero across the pair, and the reversal links back via reverses_journal_id.
func TestAC11_ReversingJournalNetZero(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	svc := newTestService(f)

	// A posted entry: DR 1000 400 / CR 4000 400 (below threshold → posts).
	orig, err := svc.CreateJournal(ctx, CreateJournalInput{Memo: "sale", Lines: []JournalLineInput{
		{AccountCode: "1000", DebitCents: 40000},
		{AccountCode: "4000", CreditCents: 40000},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rev, err := svc.ReverseJournal(ctx, orig.ID)
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if rev.ReversesJournalID != orig.ID {
		t.Fatalf("reversal must link to the original, got %q", rev.ReversesJournalID)
	}

	// Net every account across the two journals, all must be zero.
	net := map[string]int64{}
	for _, l := range orig.Lines {
		net[l.AccountCode] += l.DebitCents - l.CreditCents
	}
	for _, l := range rev.Lines {
		net[l.AccountCode] += l.DebitCents - l.CreditCents
	}
	for code, n := range net {
		if n != 0 {
			t.Fatalf("account %s not net-zero after reversal: %d", code, n)
		}
	}
	if !rev.balancedForTest() {
		t.Fatal("reversing journal is itself unbalanced")
	}
}

// balancedForTest checks a projected journal balances (test helper).
func (j Journal) balancedForTest() bool {
	var d, c int64
	for _, l := range j.Lines {
		d += l.DebitCents
		c += l.CreditCents
	}
	return d == c
}
