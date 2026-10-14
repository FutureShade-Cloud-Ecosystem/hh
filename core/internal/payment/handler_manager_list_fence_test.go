package payment

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// The list is fenced by the caller's branch reach (the item 3 review, P3-1): a branch scoped manager reads
// the open card attempts of its own branches only, a global role reads every branch, and a row of a branch
// the caller does not reach is simply absent, with nothing in the body about it.

const (
	fenceBranchA = "a0a0a0a0-0000-0000-0000-00000000000a"
	fenceBranchB = "b0b0b0b0-0000-0000-0000-00000000000b"
	fenceBranchC = "c0c0c0c0-0000-0000-0000-00000000000c"
)

// fenceRows plants two attempts per branch, distinct ids, amounts, sale keys and charge refs.
func fenceRows() []Attempt {
	mk := func(id, branch, saleKey, ref string, cents int64) Attempt {
		a := listFixture
		a.ID, a.BranchID, a.SaleKey, a.ProcessorRef, a.AmountCents = id, branch, saleKey, ref, cents
		a.LastError = "last error of " + id
		return a
	}
	return []Attempt{
		mk("aaaaaaaa-0000-0000-0000-000000000001", fenceBranchA, "5a1e0000-0000-0000-0000-00000000000a", "trans_a1", 1111),
		mk("bbbbbbbb-0000-0000-0000-000000000002", fenceBranchB, "5a1e0000-0000-0000-0000-00000000000b", "trans_b1", 2222),
		mk("aaaaaaaa-0000-0000-0000-000000000003", fenceBranchA, "5a1e0000-0000-0000-0000-00000000000c", "trans_a2", 3333),
		mk("cccccccc-0000-0000-0000-000000000004", fenceBranchC, "5a1e0000-0000-0000-0000-00000000000d", "trans_c1", 4444),
	}
}

func fenceList(t *testing.T, fake *fakeAttemptStore, auth identity.StaffAuthority) []cardAttemptRow {
	t.Helper()
	sg, mux := newManagerTestMux(t, fake)
	sg.SetAuthority(auth)
	rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var rows []cardAttemptRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("body: %v (%s)", err, rec.Body.String())
	}
	return rows
}

func fenceBranches(rows []cardAttemptRow) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[r.BranchID]++
	}
	return out
}

func TestCardAttempts_List_BranchManagerSeesOwnBranchOnly(t *testing.T) {
	fake := &fakeAttemptStore{list: fenceRows()}
	auth := identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{fenceBranchA}}
	rows := fenceList(t, fake, auth)
	if got := fenceBranches(rows); len(rows) != 2 || got[fenceBranchA] != 2 {
		t.Fatalf("branch A manager rows by branch = %v, want only branch A's two", got)
	}

	// Nothing about a hidden row appears in the body: not its ids, branch, sale key, charge ref, amount or error.
	sg, mux := newManagerTestMux(t, fake)
	sg.SetAuthority(auth)
	body := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", "").Body.String()
	for _, hidden := range []string{fenceBranchB, fenceBranchC, "bbbbbbbb", "cccccccc", "trans_b1", "trans_c1",
		"5a1e0000-0000-0000-0000-00000000000b", "5a1e0000-0000-0000-0000-00000000000d", "2222", "4444", "last error of bbbbbbbb"} {
		if strings.Contains(body, hidden) {
			t.Fatalf("the body names a hidden row's %q: %s", hidden, body)
		}
	}
}

func TestCardAttempts_List_TwoGrantsSeeBothGrantsRows(t *testing.T) {
	fake := &fakeAttemptStore{list: fenceRows()}
	rows := fenceList(t, fake, identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{fenceBranchA, fenceBranchB}})
	if got := fenceBranches(rows); len(rows) != 3 || got[fenceBranchA] != 2 || got[fenceBranchB] != 1 {
		t.Fatalf("two grant manager rows by branch = %v, want A twice and B once", got)
	}
}

func TestCardAttempts_List_GlobalRoleSeesEveryBranch(t *testing.T) {
	fake := &fakeAttemptStore{list: fenceRows()}
	rows := fenceList(t, fake, identity.StaffAuthority{RoleID: "manager", Tier: 2, Scope: "global"})
	if len(rows) != 4 {
		t.Fatalf("global role rows = %d, want 4", len(rows))
	}
}

func TestCardAttempts_List_NoGrantSeesNothing(t *testing.T) {
	fake := &fakeAttemptStore{list: fenceRows()}
	rows := fenceList(t, fake, identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch"})
	if len(rows) != 0 {
		t.Fatalf("a branch caller with no grant sees %d rows, want 0", len(rows))
	}
	sg, mux := newManagerTestMux(t, fake)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch"})
	if body := strings.TrimSpace(doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", "").Body.String()); body != "[]" {
		t.Fatalf("an empty reach must answer an empty array, got %s", body)
	}
}

// The reach rides in the store query (so the store's limit counts only reachable rows), and it is deny by
// default: a zero reach lists nothing.
func TestCardAttempts_List_ReachIsPushedIntoTheQuery(t *testing.T) {
	fake := &fakeAttemptStore{}
	fenceList(t, fake, identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{fenceBranchA, "not-a-uuid", fenceBranchB}})
	if fake.lastQuery.AllBranches || len(fake.lastQuery.Branches) != 2 ||
		fake.lastQuery.Branches[0] != fenceBranchA || fake.lastQuery.Branches[1] != fenceBranchB {
		t.Fatalf("branch caller query reach = %+v, want its two valid grants and not all branches", fake.lastQuery)
	}
	fenceList(t, fake, identity.StaffAuthority{RoleID: "manager", Tier: 2, Scope: "global", Branches: []string{fenceBranchA}})
	if !fake.lastQuery.AllBranches || len(fake.lastQuery.Branches) != 0 {
		t.Fatalf("global caller query reach = %+v, want all branches", fake.lastQuery)
	}
}
