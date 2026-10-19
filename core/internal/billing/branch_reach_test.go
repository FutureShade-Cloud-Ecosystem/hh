package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

func TestReachOfCaller(t *testing.T) {
	if r := reachOfCaller(identity.StaffContext{Scope: identity.ScopeGlobal}); !r.Global {
		t.Fatal("a global caller reaches every branch")
	}
	r := reachOfCaller(identity.StaffContext{Scope: identity.ScopeBranch, Branches: map[string]bool{"b2": true, "b1": true, "b3": false}})
	if r.Global || strings.Join(r.IDs, ",") != "b1,b2" {
		t.Fatalf("a scoped caller reaches its granted branches, sorted, got %+v", r)
	}
	none := reachOfCaller(identity.StaffContext{Scope: identity.ScopeBranch})
	if none.Global || none.Allows("b1") || none.SQLArg() == nil {
		t.Fatal("a scoped caller with no grant reaches nothing, and the query argument is an empty list, never NULL")
	}
	if AllBranches.SQLArg() != nil {
		t.Fatal("every branch is the NULL argument")
	}
}

func TestNarrowRefusesABranchOutsideTheReach(t *testing.T) {
	r := BranchReach{IDs: []string{"b1"}}
	if got, err := r.narrow("b1"); err != nil || len(got.IDs) != 1 || got.IDs[0] != "b1" {
		t.Fatalf("a granted branch narrows, got %+v %v", got, err)
	}
	if _, err := r.narrow("b2"); err != ErrBranchForbidden {
		t.Fatalf("an ungranted branch is refused, got %v", err)
	}
	if got, err := r.narrow(""); err != nil || got.Global || len(got.IDs) != 1 {
		t.Fatalf("no branch keeps the reach, got %+v %v", got, err)
	}
}

// reachCaptureMoney records the ApplyReceivedCommand the register hands the money seam, so a test
// can answer for the branch reach ApplyReceivedPayment resolved, the one fact HH-694 is about.
type reachCaptureMoney struct {
	got *ApplyReceivedCommand
}

func (m *reachCaptureMoney) RecordReceipt(_ context.Context, _ ReceiptWrite) (ReceiptResult, error) {
	return ReceiptResult{}, nil
}

func (m *reachCaptureMoney) PayableCentsFor(_ context.Context, _ string) (int64, string, error) {
	return 0, "", nil
}

func (m *reachCaptureMoney) ApplyReceived(_ context.Context, in ApplyReceivedCommand) (payments.ApplyAnswer, error) {
	m.got = &in
	return payments.ApplyAnswer{}, nil
}

// applyReachPayment runs one captured ApplyReceivedPayment and answers the branch slice it passed.
func applyReachBranchIDs(t *testing.T, ctx context.Context) []string {
	t.Helper()
	money := &reachCaptureMoney{}
	s := NewRegisterService(nil).WithMoney(money)
	_, err := s.ApplyReceivedPayment(ctx, ApplyPaymentInput{
		CustomerID:     "cus",
		Method:         "CASH",
		AmountCents:    100,
		InvoiceIDs:     []string{"inv"},
		IdempotencyKey: "idem",
	})
	if err != nil {
		t.Fatalf("apply received payment: %v", err)
	}
	if money.got == nil {
		t.Fatal("the money seam was never called")
	}
	return money.got.BranchIDs
}

func TestApplyReceivedPaymentReachMatchesTheRegisterReach(t *testing.T) {
	t.Run("unstamped context reaches every branch", func(t *testing.T) {
		if got := applyReachBranchIDs(t, context.Background()); got != nil {
			t.Fatalf("an unstamped internal caller reaches every branch (nil slice), got %v", got)
		}
	})
	t.Run("scoped caller reaches granted branches only, sorted", func(t *testing.T) {
		ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
			UserID:   "u",
			Scope:    identity.ScopeBranch,
			Branches: map[string]bool{"b2": true, "b1": true, "b3": false},
		})
		if got := applyReachBranchIDs(t, ctx); strings.Join(got, ",") != "b1,b2" {
			t.Fatalf("a scoped caller reaches its granted branches, sorted, never a revoked one, got %v", got)
		}
	})
	t.Run("scoped caller with no grant reaches nothing", func(t *testing.T) {
		ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
			UserID: "u",
			Scope:  identity.ScopeBranch,
		})
		got := applyReachBranchIDs(t, ctx)
		if got == nil || len(got) != 0 {
			t.Fatalf("a scoped caller with no grant reaches nothing (non nil empty slice), got %v", got)
		}
	})
}

func TestBranchRefusalSpeaksInWords(t *testing.T) {
	rec := httptest.NewRecorder()
	(&RegisterHandler{}).writeErr(rec, httptest.NewRequest(http.MethodGet, "/", nil), ErrBranchForbidden)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), BranchForbiddenWords) {
		t.Fatalf("a branch refusal is a 403 in words, got %d %s", rec.Code, rec.Body.String())
	}
}
