//go:build integration

// handler_manager_list_fence_integration_test.go: the manager list route over the real attempt store, so the
// branch fence is proven where it matters: in the query, with rows of two branches in the table. A filter
// applied after the store's limit would leave a short page; these legs pin that it does not.
package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// otherBranchSession plants an open till on a second seeded branch and returns the branch and the session.
func (fx *attemptFx) otherBranchSession() (branch, session string) {
	fx.t.Helper()
	ctx := context.Background()
	if err := fx.helper.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' AND id::text <> $1 ORDER BY id LIMIT 1`, fx.branch).Scan(&branch); err != nil {
		fx.t.Skipf("no second seeded branch: %v", err)
	}
	if err := fx.helper.QueryRow(ctx, `INSERT INTO till_sessions (branch_id, station, status, opened_by)
	    VALUES ($1::uuid, $2, 'OPEN', $3::uuid) RETURNING id::text`, branch, "a1-"+uuid.NewString(), fx.user).Scan(&session); err != nil {
		fx.t.Fatalf("second branch till session: %v", err)
	}
	fx.t.Cleanup(func() {
		c := context.Background()
		_, _ = fx.helper.Exec(c, `DELETE FROM card_charge_attempts WHERE till_session_id = $1::uuid`, session)
		_, _ = fx.helper.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, session)
	})
	return branch, session
}

func (fx *attemptFx) plantUnknown(branch, session string, amount int64, age string) Attempt {
	fx.t.Helper()
	in := fx.in(amount)
	in.BranchID, in.TillSessionID = branch, session
	a := fx.claim(in)
	fx.force(a.ID, AttemptUnknown)
	if _, err := fx.helper.Exec(context.Background(), `UPDATE card_charge_attempts SET created_at = now() - $2::interval WHERE id = $1::uuid`, a.ID, age); err != nil {
		fx.t.Fatalf("age the planted row: %v", err)
	}
	return fx.get(a.LegKey)
}

func fenceHTTPList(t *testing.T, fx *attemptFx, auth identity.StaffAuthority) ([]cardAttemptRow, string) {
	t.Helper()
	sg, mux := newManagerTestMux(t, fx.store)
	sg.SetAuthority(auth)
	rec := doReq(t, sg, mux, http.MethodGet, "/api/v1/payments/card-attempts?state=open", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var rows []cardAttemptRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("body: %v", err)
	}
	return rows, rec.Body.String()
}

func idsOf(rows []cardAttemptRow) map[string]bool {
	out := map[string]bool{}
	for _, r := range rows {
		out[r.ID] = true
	}
	return out
}

func TestCardAttempts_List_BranchFenceOverTheRealStore(t *testing.T) {
	fx := newAttemptFx(t)
	branchB, sessionB := fx.otherBranchSession()
	a1 := fx.plantUnknown(fx.branch, fx.session, 1101, "3 hours")
	a2 := fx.plantUnknown(fx.branch, fx.session, 1102, "2 hours")
	b1 := fx.plantUnknown(branchB, sessionB, 2201, "1 hour")

	// A branch A manager: every row is branch A's, both planted A rows are there, B's is absent from the body.
	rows, body := fenceHTTPList(t, fx, identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{fx.branch}})
	got := idsOf(rows)
	if !got[a1.ID] || !got[a2.ID] {
		t.Fatalf("the branch A manager must see its own planted rows, got %v", got)
	}
	for _, r := range rows {
		if r.BranchID != fx.branch {
			t.Fatalf("a branch A manager was served a row of branch %s", r.BranchID)
		}
	}
	for _, hidden := range []string{b1.ID, b1.SaleKey, b1.TillSessionID, branchB} {
		if strings.Contains(body, hidden) {
			t.Fatalf("the body carries a hidden row's %q", hidden)
		}
	}

	// The same person granted both branches sees all three planted rows.
	rows, _ = fenceHTTPList(t, fx, identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{fx.branch, branchB}})
	got = idsOf(rows)
	if !got[a1.ID] || !got[a2.ID] || !got[b1.ID] {
		t.Fatalf("a two grant manager must see both grants' rows, got %v", got)
	}

	// A branch B manager sees B's row and neither of A's.
	rows, _ = fenceHTTPList(t, fx, identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{branchB}})
	got = idsOf(rows)
	if !got[b1.ID] || got[a1.ID] || got[a2.ID] {
		t.Fatalf("a branch B manager sees %v, want B's row only of the planted three", got)
	}

	// A global role sees every branch.
	rows, _ = fenceHTTPList(t, fx, identity.StaffAuthority{RoleID: "manager", Tier: 2, Scope: "global"})
	got = idsOf(rows)
	if !got[a1.ID] || !got[a2.ID] || !got[b1.ID] {
		t.Fatalf("a global role must see every branch, got %v", got)
	}

	// A branch scoped caller with no grant sees nothing, as an empty array.
	rows, body = fenceHTTPList(t, fx, identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch"})
	if len(rows) != 0 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("a caller with no grant got %s", body)
	}
}

// The limit applies after the fence, so a page is never short: a flood of newer rows in a branch the caller
// does not reach must not push the caller's own older rows out of the bounded list, and the global list is
// bounded at the same limit.
func TestCardAttempts_List_FenceIsInTheQueryNotAfterTheLimit(t *testing.T) {
	fx := newAttemptFx(t)
	branchB, sessionB := fx.otherBranchSession()
	a1 := fx.plantUnknown(fx.branch, fx.session, 1301, "3 hours")
	a2 := fx.plantUnknown(fx.branch, fx.session, 1302, "2 hours")
	if _, err := fx.helper.Exec(context.Background(), `
		INSERT INTO card_charge_attempts (leg_key, sale_key, leg_seq, amount_cents, branch_id, till_session_id, card_type, processor, status)
		SELECT 'flood-' || gen_random_uuid()::text, gen_random_uuid(), 1, 100 + g, $1::uuid, $2::uuid, 'CREDIT', 'run_payments', 'UNKNOWN'
		  FROM generate_series(1, $3::int) g`, branchB, sessionB, listLimit+1); err != nil {
		t.Fatalf("flood fixture: %v", err)
	}

	rows, _ := fenceHTTPList(t, fx, identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{fx.branch}})
	got := idsOf(rows)
	if !got[a1.ID] || !got[a2.ID] {
		t.Fatalf("the branch A manager's page is short: the flood in branch B pushed its rows out (%d rows)", len(rows))
	}
	for _, r := range rows {
		if r.BranchID != fx.branch {
			t.Fatalf("a branch A manager was served a row of branch %s", r.BranchID)
		}
	}

	all, _ := fenceHTTPList(t, fx, identity.StaffAuthority{RoleID: "manager", Tier: 2, Scope: "global"})
	if len(all) != listLimit {
		t.Fatalf("the global list = %d rows, want the limit %d", len(all), listLimit)
	}
}
