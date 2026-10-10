package billing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
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

func TestBranchRefusalSpeaksInWords(t *testing.T) {
	rec := httptest.NewRecorder()
	(&RegisterHandler{}).writeErr(rec, httptest.NewRequest(http.MethodGet, "/", nil), ErrBranchForbidden)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), BranchForbiddenWords) {
		t.Fatalf("a branch refusal is a 403 in words, got %d %s", rec.Code, rec.Body.String())
	}
}
