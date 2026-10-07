package customer

// account_read_test.go, the by-id read's service rules without a database: the reach the caller's
// grants render, the refusals that never touch the store, and not-found.

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const readID = "5d5a0000-0000-4000-8000-0000000000b1"

type fakeReadStore struct {
	*fakeStore
	acct     Account
	found    bool
	err      error
	gotReach AccountReach
	calls    int
}

func (f *fakeReadStore) GetAccountScoped(_ context.Context, _ string, reach AccountReach) (Account, bool, error) {
	f.gotReach, f.calls = reach, f.calls+1
	return f.acct, f.found, f.err
}

func readCtx(sc identity.StaffContext) context.Context {
	return identity.ContextWithStaff(context.Background(), sc)
}

func TestGetAccount_BranchCallerIsFencedToItsGrants(t *testing.T) {
	f := &fakeReadStore{fakeStore: newFakeStore(), acct: Account{CustomerID: readID, CreditLimitCents: 5, BalanceCents: 7}, found: true}
	svc := NewService(f)
	ctx := readCtx(identity.StaffContext{UserID: "u", Scope: identity.ScopeBranch, Branches: map[string]bool{"b-1": true, "b-2": false}})
	got, err := svc.GetAccount(ctx, readID)
	if err != nil || got.BalanceCents != 7 || got.CreditLimitCents != 5 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if f.gotReach.Global || len(f.gotReach.BranchIDs) != 1 || f.gotReach.BranchIDs[0] != "b-1" {
		t.Fatalf("the store was asked with %+v, want the one granted branch and not global", f.gotReach)
	}
}

func TestGetAccount_GlobalCallerIsUnfenced(t *testing.T) {
	f := &fakeReadStore{fakeStore: newFakeStore(), found: true}
	svc := NewService(f)
	if _, err := svc.GetAccount(readCtx(identity.StaffContext{UserID: "u", Scope: identity.ScopeGlobal}), readID); err != nil {
		t.Fatal(err)
	}
	if !f.gotReach.Global {
		t.Fatalf("a global caller reads unfenced: %+v", f.gotReach)
	}
}

func TestGetAccount_NotFoundIsErrNotFound(t *testing.T) {
	f := &fakeReadStore{fakeStore: newFakeStore(), found: false}
	svc := NewService(f)
	_, err := svc.GetAccount(readCtx(identity.StaffContext{UserID: "u", Scope: identity.ScopeGlobal}), readID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetAccount_RefusalsNeverTouchTheStore(t *testing.T) {
	f := &fakeReadStore{fakeStore: newFakeStore(), found: true}
	svc := NewService(f)
	if _, err := svc.GetAccount(context.Background(), readID); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("no caller: %v", err)
	}
	var bad httpx.BadRequestError
	if _, err := svc.GetAccount(readCtx(identity.StaffContext{UserID: "u", Scope: identity.ScopeGlobal}), "cust-abc"); !errors.As(err, &bad) {
		t.Fatalf("a malformed id is a bad request: %v", err)
	}
	if f.calls != 0 {
		t.Fatalf("the store was read %d times for refused calls", f.calls)
	}
}

func TestGetAccount_AStoreThatCannotFenceIsNotAskedToServe(t *testing.T) {
	svc := NewService(newFakeStore()) // no accountReadStore
	_, err := svc.GetAccount(readCtx(identity.StaffContext{UserID: "u", Scope: identity.ScopeGlobal}), readID)
	if !errors.Is(err, ErrLookupNotConfigured) {
		t.Fatalf("err = %v, want ErrLookupNotConfigured", err)
	}
}
