//go:build integration

package main

// tasks_org_rest_integration_test.go: the yard REST doors held to the caller's org (UX-81 round 5, FROM-PRINCIPAL
// item 108, S2's round 4 read). Through the wired routes (the composition root's real fold), a GLOBAL caller of org
// north names org south's branch and task at every yard door. GLOBAL reaches every branch of the caller's OWN org,
// never another org's: the doors answer a foreign dealer's branch as out of scope and a foreign dealer's task as not
// found, the claim included, and GET /api/v1/tasks gets the yard branch fence it never had.
//
// The kit's two orgs: north (the caller's) and south (a pickup order and a pick task planted for the run).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type orgRestWorld struct {
	*orgScopeWorld
	mux       *http.ServeMux
	sg        *testkit.StaffGuards
	southTask string
	northTask string
}

func newOrgRestWorld(t *testing.T) *orgRestWorld {
	t.Helper()
	base := newOrgScopeWorld(t)
	w := &orgRestWorld{orgScopeWorld: base}
	cw := plantWiringCounter(t, base.pool) // the linked user the claim needs (the caller's users row)
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("vault key: %v", err)
	}
	t.Setenv("APP_ENV", "dev")
	_, w.mux, w.sg = wireFold(t, base.pool, hex.EncodeToString(rawKey), cw.branchID)
	ctx := context.Background()
	plant := func(orgID, branchID, orderID string) string {
		var id string
		if err := base.pool.QueryRow(ctx, `
			INSERT INTO tasks (task_type, order_id, branch_id, priority, status, source, org_id, seq)
			VALUES ('PICK', $1::uuid, $2::uuid, 'NORMAL', 'QUEUED', 'MANUAL', $3::uuid, (SELECT COALESCE(max(seq),0)+1 FROM tasks))
			RETURNING id::text`, orderID, branchID, orgID).Scan(&id); err != nil {
			t.Fatalf("plant task: %v", err)
		}
		return id
	}
	w.southTask = plant(testkit.OrgSouthID, testkit.BranchSouthAID, base.southOrder)
	w.northTask = plant(testkit.OrgNorthID, testkit.BranchNorthAID, base.northOrder)
	return w
}

// globalNorth makes the caller a GLOBAL tier 1 staff user of org north; scopedNorthA a frontline user granted north A.
func (w *orgRestWorld) globalNorth() {
	w.sg.SetAuthority(identity.StaffAuthority{RoleID: "general_manager", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal})
}
func (w *orgRestWorld) scopedNorthA() {
	w.sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Branches: []string{testkit.BranchNorthAID}})
}

func (w *orgRestWorld) call(t *testing.T, method, path string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(""))
	r.Header.Set("Authorization", "Bearer "+w.sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, r)
	body := strings.TrimSpace(rec.Body.String())
	t.Logf("%-5s %-72s -> %d %.120s", method, path, rec.Code, body)
	return rec.Code, body
}

func (w *orgRestWorld) southTaskState(t *testing.T) (status, claimedBy string) {
	t.Helper()
	if err := w.pool.QueryRow(context.Background(), `SELECT status, COALESCE(claimed_by::text,'') FROM tasks WHERE id = $1::uuid`, w.southTask).Scan(&status, &claimedBy); err != nil {
		t.Fatalf("read the south task: %v", err)
	}
	return status, claimedBy
}

func holds(body string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(body, n) {
			return true
		}
	}
	return false
}

func TestTasksOrgREST_AGlobalCallerOfNorthDoesNotReachSouthsYard(t *testing.T) {
	w := newOrgRestWorld(t)
	w.globalNorth()
	southOrderNo := "FXORD-S-ORGSCOPE-" + w.southOrder[:8]

	// GET /api/v1/tasks: with and without branch_id.
	if code, body := w.call(t, http.MethodGet, "/api/v1/tasks"); code != http.StatusOK || holds(body, w.southTask) {
		t.Errorf("GET /api/v1/tasks lists no task of another org: %d %.200s", code, body)
	}
	if code, body := w.call(t, http.MethodGet, "/api/v1/tasks?branch_id="+testkit.BranchSouthAID); code != http.StatusForbidden || holds(body, w.southTask) {
		t.Errorf("GET /api/v1/tasks?branch_id=<another org's branch> is out of scope: %d %.200s", code, body)
	}
	// The yard board and the pending tickets at South A.
	if code, body := w.call(t, http.MethodGet, "/api/v1/yard/pick-queue?branch_id="+testkit.BranchSouthAID); code != http.StatusForbidden || holds(body, w.southTask) {
		t.Errorf("the pick queue at another org's branch is out of scope: %d %.200s", code, body)
	}
	if code, body := w.call(t, http.MethodGet, "/api/v1/yard/pending-tickets?branch_id="+testkit.BranchSouthAID); code != http.StatusForbidden || holds(body, southOrderNo, "Fixture Contractor Gamma") {
		t.Errorf("the pending tickets at another org's branch are out of scope and carry no customer or order: %d %.200s", code, body)
	}
	// Without a branch the board and the pending tickets still hold to the caller's org.
	if code, body := w.call(t, http.MethodGet, "/api/v1/yard/pick-queue"); code != http.StatusOK || holds(body, w.southTask, southOrderNo) {
		t.Errorf("the pick queue with no branch holds to the caller's org: %d %.200s", code, body)
	}
	if code, body := w.call(t, http.MethodGet, "/api/v1/yard/pending-tickets"); code != http.StatusOK || holds(body, southOrderNo, "Fixture Contractor Gamma") {
		t.Errorf("the pending tickets with no branch hold to the caller's org: %d %.200s", code, body)
	}
	// The pick detail and the claim: another dealer's task is not found, and nothing is written.
	if code, body := w.call(t, http.MethodGet, "/api/v1/yard/picks/"+w.southTask); code != http.StatusNotFound {
		t.Errorf("another org's pick is not found: %d %.200s", code, body)
	}
	if code, body := w.call(t, http.MethodPost, "/api/v1/tasks/"+w.southTask+"/claim"); code != http.StatusNotFound {
		t.Errorf("claiming another org's task is not found: %d %.200s", code, body)
	}
	if status, by := w.southTaskState(t); status != "QUEUED" || by != "" {
		t.Errorf("a refused claim writes nothing to another dealer's yard: %s claimed by %q", status, by)
	}
	// The controls: the caller's own org still lists, reads and claims.
	if code, body := w.call(t, http.MethodGet, "/api/v1/tasks"); code != http.StatusOK || !holds(body, w.northTask) {
		t.Errorf("the caller's own task is still listed: %d %.200s", code, body)
	}
	if code, body := w.call(t, http.MethodGet, "/api/v1/yard/picks/"+w.northTask); code != http.StatusOK {
		t.Errorf("the caller's own pick is still read: %d %.200s", code, body)
	}
	if code, body := w.call(t, http.MethodPost, "/api/v1/tasks/"+w.northTask+"/claim"); code != http.StatusOK {
		t.Errorf("the caller's own task is still claimed: %d %.200s", code, body)
	}
}

// GET /api/v1/tasks had no branch fence at all: a scoped caller granted north A listed south's task, with and
// without branch_id. It now answers as the yard board does.
func TestTasksOrgREST_TheTaskListHoldsTheYardBranchFence(t *testing.T) {
	w := newOrgRestWorld(t)
	w.scopedNorthA()
	if code, body := w.call(t, http.MethodGet, "/api/v1/tasks"); code != http.StatusForbidden || holds(body, w.southTask) {
		t.Errorf("a branch scoped caller must name a reachable branch to list tasks: %d %.200s", code, body)
	}
	if code, body := w.call(t, http.MethodGet, "/api/v1/tasks?branch_id="+testkit.BranchSouthAID); code != http.StatusForbidden || holds(body, w.southTask) {
		t.Errorf("a branch scoped caller naming another branch is out of scope: %d %.200s", code, body)
	}
	if code, body := w.call(t, http.MethodGet, "/api/v1/tasks?branch_id="+testkit.BranchNorthAID); code != http.StatusOK || !holds(body, w.northTask) || holds(body, w.southTask) {
		t.Errorf("a branch scoped caller lists its own branch: %d %.200s", code, body)
	}
}

// A GLOBAL caller of north on an order of its own org whose branch is south's (a data defect the old create let
// through): the create refuses it as an unknown order on both doors, because GLOBAL reach stops at the org.
func TestTasksOrgREST_AGlobalCallersReachIsBoundedToItsOrgsBranches(t *testing.T) {
	w := newOrgRestWorld(t)
	w.globalNorth()
	var odd string
	if err := w.pool.QueryRow(context.Background(), `
		INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, org_id, tenant_id)
		VALUES (gen_random_uuid(), 'FXORD-N-ODDBRANCH-'||substr(gen_random_uuid()::text,1,8), $1::uuid, $2::uuid, 'CONFIRMED', 'PICKUP', $3::uuid, $1::uuid)
		RETURNING id::text`, testkit.TenantAlphaID, testkit.BranchSouthAID, testkit.OrgNorthID).Scan(&odd); err != nil {
		t.Fatalf("plant a north order on south's branch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM task_events WHERE task_id IN (SELECT id FROM tasks WHERE order_id = $1::uuid)`, odd)
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM tasks WHERE order_id = $1::uuid`, odd)
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1::uuid`, odd)
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"order_id":"`+odd+`"}`))
	r.Header.Set("Authorization", "Bearer "+w.sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, r)
	t.Logf("POST /api/v1/tasks (a north order on south's branch) -> %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "order not found") {
		t.Errorf("REST create on an order whose branch is outside the caller's org is the unknown order answer: %d %s", rec.Code, rec.Body.String())
	}
	store := w.syncStore(w.globalOf(testkit.OrgNorthID))
	_, err := store.Create(context.Background(), syncFilter(testkit.OrgNorthID), uuid.NewString(), map[string]any{"order_id": odd})
	if err == nil || !strings.Contains(err.Error(), "unknown order") {
		t.Errorf("sync create on an order whose branch is outside the caller's org is the unknown order answer: %v", err)
	}
	var n int
	_ = w.pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE order_id = $1::uuid`, odd).Scan(&n)
	if n != 0 {
		t.Errorf("a refused create writes nothing: %d tasks", n)
	}
}

func syncFilter(org string) hhsync.ScopeFilter {
	return hhsync.ScopeFilter{OrgID: org, Actor: "org-scope-actor"}
}
