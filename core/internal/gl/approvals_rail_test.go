package gl

// approvals_rail_test.go, DB-free unit tests for the 0075 JE park→approvals-rail bridge. Asserts the
// filer fires EXACTLY on the manual-create park path (>= threshold, SourceManual) and never otherwise.

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

type filerCall struct {
	journalID string
	branchID  string
	createdBy string
	amount    int64
}

// fakeApprovalFiler records the JE approval filings the park-bridge makes.
type fakeApprovalFiler struct {
	calls []filerCall
	err   error
}

func (f *fakeApprovalFiler) FileJEApproval(_ context.Context, journalID, branchID string, amountCents int64, createdBy string) error {
	f.calls = append(f.calls, filerCall{journalID, branchID, createdBy, amountCents})
	return f.err
}

// TestCreateJournal_ParkBridge_FiresOnlyOnManualPark pins that the approvals-rail filer fires exactly when
// a MANUAL journal parks as DRAFT at/over the threshold, and never below threshold, never for a non-manual
// source. (The automated posting paths, posting.go/close.go/PostExternal, do not call CreateJournal, so
// they can never reach this bridge regardless of amount.)
func TestCreateJournal_ParkBridge_FiresOnlyOnManualPark(t *testing.T) {
	ctx := context.Background()
	maker := identity.StaffContext{UserID: "u-maker", RoleCode: "SALES_MANAGER", Tier: identity.TierManager}
	over := []JournalLineInput{{AccountCode: "1000", DebitCents: 600000}, {AccountCode: "4000", CreditCents: 600000}}

	// (a) manual, over threshold → parks DRAFT → filer fires once with the journal id + debit total.
	f := newFakeStore()
	f.policy.JEApprovalThresholdCents = 500000
	filer := &fakeApprovalFiler{}
	j, err := newTestServiceAs(f, maker).WithApprovalFiler(filer).
		CreateJournal(ctx, CreateJournalInput{BranchID: "b1", Memo: "big", Source: SourceManual, Lines: over})
	if err != nil {
		t.Fatalf("create over-threshold: %v", err)
	}
	if j.Status != StatusDraft {
		t.Fatalf("over-threshold manual journal should park DRAFT, got %s", j.Status)
	}
	if len(filer.calls) != 1 {
		t.Fatalf("filer should fire exactly once on a manual park, got %d", len(filer.calls))
	}
	if c := filer.calls[0]; c.journalID != j.ID || c.amount != 600000 || c.branchID != "b1" || c.createdBy != "u-maker" {
		t.Fatalf("filer call has wrong args: %+v (journal id %s)", c, j.ID)
	}

	// (b) manual, below threshold → posts immediately → filer does NOT fire.
	f = newFakeStore()
	f.policy.JEApprovalThresholdCents = 500000
	filer = &fakeApprovalFiler{}
	j, err = newTestServiceAs(f, maker).WithApprovalFiler(filer).
		CreateJournal(ctx, CreateJournalInput{BranchID: "b1", Source: SourceManual, Lines: balancedLines()}) // 10000 < 500000
	if err != nil {
		t.Fatalf("create below-threshold: %v", err)
	}
	if j.Status != StatusPosted {
		t.Fatalf("below-threshold journal should POST, got %s", j.Status)
	}
	if len(filer.calls) != 0 {
		t.Fatalf("filer must NOT fire below threshold, got %d", len(filer.calls))
	}

	// (c) NON-manual source over threshold via CreateJournal → filer does NOT fire (SourceManual only).
	f = newFakeStore()
	f.policy.JEApprovalThresholdCents = 500000
	filer = &fakeApprovalFiler{}
	if _, err := newTestServiceAs(f, maker).WithApprovalFiler(filer).
		CreateJournal(ctx, CreateJournalInput{BranchID: "b1", Source: SourceAdjustment, Lines: over}); err != nil {
		t.Fatalf("create non-manual: %v", err)
	}
	if len(filer.calls) != 0 {
		t.Fatalf("filer must fire ONLY for the SourceManual create path, got %d", len(filer.calls))
	}

	// (d) filer error never fails the park (log-and-continue): a parked DRAFT is still returned.
	f = newFakeStore()
	f.policy.JEApprovalThresholdCents = 500000
	filer = &fakeApprovalFiler{err: context.Canceled}
	j, err = newTestServiceAs(f, maker).WithApprovalFiler(filer).
		CreateJournal(ctx, CreateJournalInput{BranchID: "b1", Source: SourceManual, Lines: over})
	if err != nil {
		t.Fatalf("a filer error must not fail the journal park: %v", err)
	}
	if j.Status != StatusDraft {
		t.Fatalf("journal should still park DRAFT despite a filer error, got %s", j.Status)
	}
}
