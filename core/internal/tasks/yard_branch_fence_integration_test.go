//go:build integration

// yard_branch_fence_integration_test.go: the branch fence on the yard's Stage and shortage report doors
// (UX-77, FROM-PRINCIPAL item 52.2). Two staff users with the yard role share an org and are scoped to
// different branches; a pick sits on branch A; the user scoped to branch B names it by task id.
//
// The REST lines handler already holds a write to the task's branch (taskBranchFence); Stage and the
// shortage report must answer a fenced call with the same status and words. The sync doors are held to the
// same refusal. Every request's status and body is logged (run with -v) as the transcript.
//
// Run with DATABASE_URL set to a throwaway migrated database; the legs skip politely without one.
package tasks

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

type branchFence struct {
	*pickDoor
	userA, userB string
	task         string
	h            *Handler
	shorts       *hhsync.Engine
	sInfo        hhsync.ScopeInfo
}

func newBranchFence(t *testing.T) *branchFence {
	t.Helper()
	d := newPickDoor(t)
	g := &branchFence{pickDoor: d, userA: d.user}
	if err := d.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, display_name, status) VALUES ($1, 'branch fence leg B', 'ACTIVE') RETURNING id::text`,
		"branchfence-b-"+d.org+"@example.invalid").Scan(&g.userB); err != nil {
		t.Fatalf("seed user B: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = d.pool.Exec(bg, `DELETE FROM task_shortages WHERE reported_by IN ($1::uuid, $2::uuid)`, g.userA, g.userB)
		_, _ = d.pool.Exec(bg, `DELETE FROM task_events WHERE actor_id = $1::uuid`, g.userB)
		_, _ = d.pool.Exec(bg, `DELETE FROM users WHERE id = $1::uuid`, g.userB)
	})
	// The sync door's staff authority answers the current user, scoped to that user's own branch.
	d.authority = func() (identity.StaffContext, bool, error) {
		return g.staffCtx(d.user), true, nil
	}
	g.h = NewHandler(d.svc, nil)
	link := func(_ context.Context, _ string) (string, bool) { return d.user, true }
	g.shorts = hhsync.NewEngine()
	if err := g.shorts.RegisterScope("shortages", NewShortageSyncStore(d.pool, link).WithNoticer(d.svc).
		WithStaffAuthority(func(context.Context, string, string) (identity.StaffContext, bool, error) { return d.authority() })); err != nil {
		t.Fatalf("register shortages: %v", err)
	}
	info, ok := hhsync.Lookup("shortages")
	if !ok {
		t.Fatal("shortages missing from the registry")
	}
	g.sInfo = info

	// The pick is on branch A and started, with no holder (the fence is the only thing under test).
	g.task = d.task(d.branchA)
	if _, err := d.pool.Exec(context.Background(), `UPDATE tasks SET status = 'IN_PROGRESS' WHERE id = $1::uuid`, g.task); err != nil {
		t.Fatalf("start the pick: %v", err)
	}
	return g
}

// staffCtx is a yard team caller scoped to the branch of its own: A to branch A, B to branch B.
func (g *branchFence) staffCtx(user string) identity.StaffContext {
	branch := g.branchA
	if user == g.userB {
		branch = g.branchB
	}
	return identity.StaffContext{UserID: user, RoleCode: "YARD_TEAM", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{branch: true}}
}

func (g *branchFence) rest(who, user, method, path, body string, do func(http.ResponseWriter, *http.Request, guards.StaffCaller)) (int, string) {
	g.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.SetPathValue("id", g.task)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(identity.ContextWithStaff(r.Context(), g.staffCtx(user)))
	w := httptest.NewRecorder()
	do(w, r, guards.StaffCaller{})
	out := strings.TrimSpace(w.Body.String())
	g.t.Logf("REST  %-4s %-36s as %s -> %d %s", method, path, who, w.Code, out)
	return w.Code, out
}

func (g *branchFence) syncPush(who, scope string, op apigen.SyncPushOperation) apigen.SyncPushResult {
	g.t.Helper()
	var res []apigen.SyncPushResult
	if scope == "shortages" {
		resp, err := g.shorts.Push(context.Background(), g.sInfo, g.f, []apigen.SyncPushOperation{op})
		if err != nil {
			g.t.Fatalf("push %s: %v", scope, err)
		}
		res = resp.Results
	} else {
		res = g.push(op)
	}
	errText := ""
	if res[0].Error != nil {
		errText = *res[0].Error
	}
	g.t.Logf("SYNC  %-4s %-36s as %s -> %s %s", op.Op, "/api/v1/sync/"+scope, who, res[0].Status, errText)
	return res[0]
}

func (g *branchFence) untouched(t *testing.T) {
	t.Helper()
	var status, location string
	if err := g.pool.QueryRow(context.Background(), `SELECT status, COALESCE(stage_location, '') FROM tasks WHERE id = $1::uuid`, g.task).Scan(&status, &location); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if status != "IN_PROGRESS" || location != "" {
		t.Errorf("a fenced call changes nothing: status %s, stage location %q", status, location)
	}
	var shortages int
	if err := g.pool.QueryRow(context.Background(), `SELECT count(*) FROM task_shortages WHERE task_id = $1::uuid`, g.task).Scan(&shortages); err != nil {
		t.Fatalf("read shortages: %v", err)
	}
	if shortages != 0 {
		t.Errorf("a fenced call stores no shortage: %d stored", shortages)
	}
}

const (
	fenceShortage = `{"order_line_no":1,"description":"pavers","qty_short":"2","uom":"EA"}`
	fenceLines    = `{"lines":[{"order_line_no":1,"qty_ordered":"5","qty_picked":"3","uom":"EA","short_reason":"CONDENSED_SKIDS"}]}`
)

// TestYardBranchFence_RESTStageAndShortageAnswerAsTheirSiblingsDo: the lines handler's fenced answer is the
// pattern, and Stage and the shortage report must give the same status and words.
func TestYardBranchFence_RESTStageAndShortageAnswerAsTheirSiblingsDo(t *testing.T) {
	g := newBranchFence(t)
	g.as()
	wantCode, wantBody := g.rest("B", g.userB, http.MethodPut, "/api/v1/yard/picks/{id}/lines", fenceLines, g.h.recordPick)
	if wantCode != http.StatusForbidden || !strings.Contains(wantBody, "branch out of scope") {
		t.Fatalf("the sibling fence answers 403 branch out of scope, got %d %s", wantCode, wantBody)
	}
	code, body := g.rest("B", g.userB, http.MethodPost, "/api/v1/yard/picks/{id}/stage", `{"location":"Bay 3"}`, g.h.stage)
	if code != wantCode || body != wantBody {
		t.Errorf("Stage from another branch must answer as the lines handler does (%d %s), got %d %s", wantCode, wantBody, code, body)
	}
	code, body = g.rest("B", g.userB, http.MethodPost, "/api/v1/yard/picks/{id}/shortages", fenceShortage, g.h.reportShortage)
	if code != wantCode || body != wantBody {
		t.Errorf("a shortage report from another branch must answer as the lines handler does (%d %s), got %d %s", wantCode, wantBody, code, body)
	}
	g.untouched(t)
}

// TestYardBranchFence_TheOwnBranchStillStagesAndReports: the fence does not over refuse.
func TestYardBranchFence_TheOwnBranchStillStagesAndReports(t *testing.T) {
	g := newBranchFence(t)
	if code, body := g.rest("A", g.userA, http.MethodPost, "/api/v1/yard/picks/{id}/shortages", fenceShortage, g.h.reportShortage); code != http.StatusCreated {
		t.Errorf("a same branch shortage report: %d %s", code, body)
	}
	if code, body := g.rest("A", g.userA, http.MethodPost, "/api/v1/yard/picks/{id}/stage", `{"location":"Bay 3"}`, g.h.stage); code != http.StatusOK {
		t.Errorf("a same branch stage: %d %s", code, body)
	}
}

// TestYardBranchFence_SyncDoorsRefuseAnotherBranch: the sync writes the Yard Pick app uses.
func TestYardBranchFence_SyncDoorsRefuseAnotherBranch(t *testing.T) {
	g := newBranchFence(t)
	g.as()
	rec := g.syncPush("B", "pick_tasks", recordOp(uuid.NewString(), g.task, pickLine(1)))
	if rec.Status != "rejected" {
		t.Fatalf("the sync pick record fences by branch (the pattern), got %+v", rec)
	}
	stage := g.syncPush("B", "pick_tasks", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": g.task, "status": "STAGED", "stage_location": "Bay 3"}})
	if stage.Status != "rejected" {
		t.Errorf("sync stage from another branch must be rejected, got %+v", stage)
	}
	short := g.syncPush("B", "shortages", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: map[string]any{
		"id": uuid.NewString(), "task_id": g.task, "order_line_no": float64(1), "sku": "", "description": "sand",
		"qty_short": "1", "uom": "EA", "note": "bay 3 is empty"}})
	if short.Status != "rejected" {
		t.Errorf("a sync shortage report from another branch must be rejected, got %+v", short)
	}
	g.untouched(t)
}

// TestYardBranchFence_TheOwnBranchStillStagesAndReportsThroughSync: the sync fence does not over refuse.
func TestYardBranchFence_TheOwnBranchStillStagesAndReportsThroughSync(t *testing.T) {
	g := newBranchFence(t)
	short := g.syncPush("A", "shortages", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: map[string]any{
		"id": uuid.NewString(), "task_id": g.task, "order_line_no": float64(1), "sku": "", "description": "sand",
		"qty_short": "1", "uom": "EA", "note": "bay 3 is empty"}})
	if short.Status != "applied" {
		t.Errorf("a same branch sync shortage report applies: %+v %v", short, short.Error)
	}
	stage := g.syncPush("A", "pick_tasks", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": g.task, "status": "STAGED", "stage_location": "Bay 3"}})
	if stage.Status != "applied" {
		t.Errorf("a same branch sync stage applies: %+v %v", stage, stage.Error)
	}
}

// reportAsA stores one shortage on the pick as A (the own branch, accepted) and returns its id.
func (g *branchFence) reportAsA(t *testing.T) string {
	t.Helper()
	code, body := g.rest("A", g.userA, http.MethodPost, "/api/v1/yard/picks/{id}/shortages", fenceShortage, g.h.reportShortage)
	if code != http.StatusCreated {
		t.Fatalf("A reports a shortage on the own branch: %d %s", code, body)
	}
	var id string
	if err := g.pool.QueryRow(context.Background(), `SELECT id::text FROM task_shortages WHERE task_id = $1::uuid`, g.task).Scan(&id); err != nil {
		t.Fatalf("read shortage: %v", err)
	}
	return id
}

func (g *branchFence) resolveRest(who, user, shortage string) (int, string) {
	g.t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/yard/picks/{id}/shortages/{sid}/resolve", bytes.NewBufferString(`{"resolution":"settled at the counter"}`))
	r.SetPathValue("id", g.task)
	r.SetPathValue("sid", shortage)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(identity.ContextWithStaff(r.Context(), g.staffCtx(user)))
	w := httptest.NewRecorder()
	g.h.resolveShortage(w, r, guards.StaffCaller{})
	out := strings.TrimSpace(w.Body.String())
	g.t.Logf("REST  POST /api/v1/yard/picks/{id}/shortages/{sid}/resolve as %s -> %d %s", who, w.Code, out)
	return w.Code, out
}

func (g *branchFence) shortageState(t *testing.T, id string) (open bool, deleted bool, desc string) {
	t.Helper()
	if err := g.pool.QueryRow(context.Background(),
		`SELECT resolved_at IS NULL, deleted_at IS NOT NULL, description FROM task_shortages WHERE id = $1::uuid`, id).Scan(&open, &deleted, &desc); err != nil {
		t.Fatalf("read shortage %s: %v", id, err)
	}
	return open, deleted, desc
}

// TestYardBranchFence_RESTResolveShortage: resolve answers as its siblings do, and the own branch still resolves.
func TestYardBranchFence_RESTResolveShortage(t *testing.T) {
	g := newBranchFence(t)
	sid := g.reportAsA(t)
	wantCode, wantBody := g.rest("B", g.userB, http.MethodPut, "/api/v1/yard/picks/{id}/lines", fenceLines, g.h.recordPick)
	code, body := g.resolveRest("B", g.userB, sid)
	if code != wantCode || body != wantBody {
		t.Errorf("resolve from another branch must answer as the lines handler does (%d %s), got %d %s", wantCode, wantBody, code, body)
	}
	if open, _, _ := g.shortageState(t, sid); !open {
		t.Errorf("a fenced resolve leaves the shortage open")
	}
	if code, body := g.resolveRest("A", g.userA, sid); code != http.StatusOK {
		t.Errorf("the own branch still resolves: %d %s", code, body)
	}
}

// TestYardBranchFence_SyncShortageWritesAndSettle: the sync edit and tombstone of another branch's shortage are
// fenced like its create, and the settle legs stay refused for everyone (the counter's act, already so).
func TestYardBranchFence_SyncShortageWritesAndSettle(t *testing.T) {
	g := newBranchFence(t)
	sid := g.reportAsA(t)
	g.as()
	for _, leg := range []string{"resolution", "resolved_at", "resolved_by"} {
		r := g.syncPush("B", "shortages", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": sid, leg: "settled on the truck"}})
		if r.Status != "rejected" {
			t.Errorf("sync settle leg %s from another branch is rejected, got %+v", leg, r)
		}
	}
	edit := g.syncPush("B", "shortages", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": sid, "description": "changed from another branch"}})
	if edit.Status != "rejected" {
		t.Errorf("a sync edit of another branch's shortage must be rejected, got %+v", edit)
	}
	del := g.syncPush("B", "shortages", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "delete", Payload: map[string]any{"id": sid}})
	if del.Status != "rejected" {
		t.Errorf("a sync tombstone of another branch's shortage must be rejected, got %+v", del)
	}
	if open, deleted, desc := g.shortageState(t, sid); !open || deleted || desc != "pavers" {
		t.Errorf("the fenced calls change nothing: open %v deleted %v description %q", open, deleted, desc)
	}
	g.user = g.userA
	if r := g.syncPush("A", "shortages", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": sid, "description": "pavers, two bundles"}}); r.Status != "applied" {
		t.Errorf("the own branch still edits: %+v %v", r, r.Error)
	}
	if r := g.syncPush("A", "shortages", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "delete", Payload: map[string]any{"id": sid}}); r.Status != "applied" {
		t.Errorf("the own branch still tombstones: %+v %v", r, r.Error)
	}
}

// as makes the next calls come from user B, the one scoped to the other branch.
func (g *branchFence) as() { g.user = g.userB }

// TestYardBranchFence_TheFenceAnswersBeforeTheHolderGuard: a pick on branch A is claimed by a branch A user;
// a user from branch B stages it. The answer is the branch refusal, never the holder's 409, so a caller from
// another branch cannot learn that the pick exists or who is on it (UX-77 after UX-76).
func TestYardBranchFence_TheFenceAnswersBeforeTheHolderGuard(t *testing.T) {
	g := newBranchFence(t)
	if _, err := g.pool.Exec(context.Background(), `UPDATE tasks SET claimed_by = $2::uuid WHERE id = $1::uuid`, g.task, g.userA); err != nil {
		t.Fatalf("claim the pick for A: %v", err)
	}
	g.as()
	code, body := g.rest("B", g.userB, http.MethodPost, "/api/v1/yard/picks/{id}/stage", `{"location":"Bay 3"}`, g.h.stage)
	if code != http.StatusForbidden || !strings.Contains(body, "branch out of scope") {
		t.Errorf("REST stage from another branch on a claimed pick must answer the branch refusal, got %d %s", code, body)
	}
	stage := g.syncPush("B", "pick_tasks", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": g.task, "status": "STAGED", "stage_location": "Bay 3"}})
	if stage.Status != "rejected" || stage.Error == nil || !strings.Contains(*stage.Error, "branch out of scope") {
		t.Errorf("sync stage from another branch on a claimed pick must be rejected with the branch refusal, got %+v", stage)
	}
	g.untouched(t)
}
