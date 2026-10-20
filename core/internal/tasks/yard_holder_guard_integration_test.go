//go:build integration

// yard_holder_guard_integration_test.go: the yard holder guard (UX-76, decision FROM-PRINCIPAL item 47).
// Two staff users with the yard team role share an org and a branch. A claims the pick and starts it. B,
// who does not hold it, then sends each yard write through both doors, the REST door and the sync door the
// Yard Pick app uses:
//
//   - Stage and Save pick (the pick lines write) are the holder's: B is refused, nothing changes.
//   - A shortage report is open to anyone in the branch (a shortage seen is a fact): B is accepted, the
//     reporter is stamped as B, and the counter is told.
//
// Every request's status and body is logged (run with -v) as the transcript. Run with DATABASE_URL set to a
// throwaway migrated database; the legs skip politely without one.
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

const holderGuardLines = `{"lines":[{"order_line_no":1,"qty_ordered":"5","qty_picked":"3","uom":"EA","short_reason":"CONDENSED_SKIDS"}]}`

type holderGuard struct {
	*pickDoor
	a, b   string
	task   string
	notes  *fakeNotifier
	h      *Handler
	shorts *hhsync.Engine
	sInfo  hhsync.ScopeInfo
}

func newHolderGuard(t *testing.T) *holderGuard {
	t.Helper()
	d := newPickDoor(t)
	g := &holderGuard{pickDoor: d, a: d.user, notes: &fakeNotifier{}}
	if err := d.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, display_name, status) VALUES ($1, 'holder guard leg B', 'ACTIVE') RETURNING id::text`,
		"holderguard-b-"+d.org+"@example.invalid").Scan(&g.b); err != nil {
		t.Fatalf("seed user B: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = d.pool.Exec(bg, `DELETE FROM task_shortages WHERE reported_by = $1::uuid`, g.b)
		_, _ = d.pool.Exec(bg, `DELETE FROM task_events WHERE actor_id = $1::uuid`, g.b)
		_, _ = d.pool.Exec(bg, `DELETE FROM users WHERE id = $1::uuid`, g.b)
	})
	d.svc.WithChatNotifier(g.notes).WithRoleLabels(fixedRoleLabels{})
	g.h = NewHandler(d.svc, nil)
	link := func(_ context.Context, _ string) (string, bool) { return d.user, true }
	g.shorts = hhsync.NewEngine()
	if err := g.shorts.RegisterScope("shortages", NewShortageSyncStore(d.pool, link).WithNoticer(d.svc)); err != nil {
		t.Fatalf("register shortages: %v", err)
	}
	info, ok := hhsync.Lookup("shortages")
	if !ok {
		t.Fatal("shortages missing from the registry")
	}
	g.sInfo = info

	// A claims and starts the pick, through the sync door as the app does.
	g.as(g.a)
	g.task = d.task(d.branchA)
	for _, st := range []string{"ACCEPTED", "IN_PROGRESS"} {
		res := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": g.task, "status": st}})
		if res[0].Status != "applied" {
			t.Fatalf("A moves the pick to %s: %+v %v", st, res[0], res[0].Error)
		}
	}
	if got := g.holder(); got != g.a {
		t.Fatalf("A must hold the pick, claimed_by = %q", got)
	}
	return g
}

// as makes the next calls come from this user: the sync door's link and staff authority read it lazily.
func (g *holderGuard) as(user string) { g.user = user }

func (g *holderGuard) holder() string {
	var h string
	if err := g.pool.QueryRow(context.Background(), `SELECT COALESCE(claimed_by::text, '') FROM tasks WHERE id = $1::uuid`, g.task).Scan(&h); err != nil {
		g.t.Fatalf("read holder: %v", err)
	}
	return h
}

func (g *holderGuard) status() string {
	var s string
	if err := g.pool.QueryRow(context.Background(), `SELECT status FROM tasks WHERE id = $1::uuid`, g.task).Scan(&s); err != nil {
		g.t.Fatalf("read status: %v", err)
	}
	return s
}

func (g *holderGuard) staffCtx(user string) identity.StaffContext {
	return identity.StaffContext{UserID: user, RoleCode: "YARD_TEAM", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identity.StatusActive, Branches: map[string]bool{g.branchA: true}}
}

// rest runs one request through the real handler as the user and logs the transcript line.
func (g *holderGuard) rest(who, user, method, path, body string, do func(http.ResponseWriter, *http.Request, guards.StaffCaller)) (int, string) {
	g.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.SetPathValue("id", g.task)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(identity.ContextWithStaff(r.Context(), g.staffCtx(user)))
	w := httptest.NewRecorder()
	do(w, r, guards.StaffCaller{})
	out := strings.TrimSpace(w.Body.String())
	g.t.Logf("REST  %-4s %-40s as %s -> %d %s", method, path, who, w.Code, out)
	return w.Code, out
}

func (g *holderGuard) syncPush(who, scope string, op apigen.SyncPushOperation) apigen.SyncPushResult {
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
	g.t.Logf("SYNC  %-4s %-40s as %s -> %s %s", op.Op, "/api/v1/sync/"+scope, who, res[0].Status, errText)
	return res[0]
}

// Each write runs on its own pick, so one accepted write cannot make another look refused.
func TestYardHolderGuard_NonHolderStageAndSavePickAreRefused(t *testing.T) {
	type leg struct {
		name string
		send func(g *holderGuard) (refused bool, detail string)
	}
	refusedRest := func(code int, body string) (bool, string) {
		// Exactly the holder's 409 and its words: a branch fence 403 must never make this leg pass (UX-76b).
		return code == http.StatusConflict && strings.Contains(body, ErrNotHolderWords), "got " + http.StatusText(code) + " " + body
	}
	refusedSync := func(res apigen.SyncPushResult) (bool, string) {
		return res.Status == "rejected" && res.Error != nil && strings.Contains(*res.Error, "someone else is on this pick"), "got " + res.Status
	}
	legs := []leg{
		{"REST stage", func(g *holderGuard) (bool, string) {
			return refusedRest(g.rest("B", g.b, http.MethodPost, "/api/v1/yard/picks/{id}/stage", `{"location":"Bay 3"}`, g.h.stage))
		}},
		{"REST save pick", func(g *holderGuard) (bool, string) {
			return refusedRest(g.rest("B", g.b, http.MethodPut, "/api/v1/yard/picks/{id}/lines", holderGuardLines, g.h.recordPick))
		}},
		{"sync stage", func(g *holderGuard) (bool, string) {
			return refusedSync(g.syncPush("B", "pick_tasks", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update",
				Payload: map[string]any{"id": g.task, "status": "STAGED", "stage_location": "Bay 3"}}))
		}},
		{"sync save pick", func(g *holderGuard) (bool, string) {
			return refusedSync(g.syncPush("B", "pick_tasks", recordOp(uuid.NewString(), g.task, pickLine(1))))
		}},
	}
	for _, l := range legs {
		t.Run(l.name, func(t *testing.T) {
			g := newHolderGuard(t)
			g.as(g.b)
			if ok, detail := l.send(g); !ok {
				t.Errorf("%s from a non holder must be refused with the holder 409 and its words (REST) or a rejected op with the holder words (sync); %s", l.name, detail)
			}
			if s := g.status(); s != "IN_PROGRESS" {
				t.Errorf("a refused write changes nothing: the pick is %s", s)
			}
			if n := len(g.lines(g.task)); n != 0 {
				t.Errorf("a refused write records nothing: %d lines", n)
			}
			if h := g.holder(); h != g.a {
				t.Errorf("the holder is still A, got %q", h)
			}
		})
	}
}

func TestYardHolderGuard_TheHolderStillStagesAndSaves(t *testing.T) {
	g := newHolderGuard(t)
	g.as(g.a)
	if res := g.syncPush("A", "pick_tasks", recordOp(uuid.NewString(), g.task, pickLine(1))); res.Status != "applied" {
		t.Errorf("the holder's sync save pick applies: %+v %v", res, res.Error)
	}
	if code, body := g.rest("A", g.a, http.MethodPut, "/api/v1/yard/picks/{id}/lines", holderGuardLines, g.h.recordPick); code != http.StatusOK {
		t.Errorf("the holder's REST save pick: %d %s", code, body)
	}
	if code, body := g.rest("A", g.a, http.MethodPost, "/api/v1/yard/picks/{id}/stage", `{"location":"Bay 3"}`, g.h.stage); code != http.StatusOK {
		t.Errorf("the holder's REST stage: %d %s", code, body)
	}
}

func TestYardHolderGuard_AShortageFromAnyoneInTheBranchIsAcceptedAndStamped(t *testing.T) {
	g := newHolderGuard(t)
	g.as(g.b)

	code, body := g.rest("B", g.b, http.MethodPost, "/api/v1/yard/picks/{id}/shortages",
		`{"order_line_no":1,"description":"pavers","qty_short":"2","uom":"EA"}`, g.h.reportShortage)
	if code != http.StatusCreated {
		t.Fatalf("REST shortage from a non holder is accepted: %d %s", code, body)
	}
	res := g.syncPush("B", "shortages", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "create", Payload: map[string]any{
		"id": uuid.NewString(), "task_id": g.task, "order_line_no": float64(1), "sku": "", "description": "sand",
		"qty_short": "1", "uom": "EA", "note": "bay 3 is empty"}})
	if res.Status != "applied" {
		t.Fatalf("sync shortage from a non holder is applied: %+v %v", res, res.Error)
	}

	var n, byB int
	if err := g.pool.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE reported_by = $2::uuid) FROM task_shortages WHERE task_id = $1::uuid`, g.task, g.b).Scan(&n, &byB); err != nil {
		t.Fatalf("read shortages: %v", err)
	}
	if n != 2 || byB != 2 {
		t.Errorf("both shortages are stored with B stamped as reporter: %d stored, %d by B", n, byB)
	}
	// The counter is told of each shortage, and so is the branch's yard channel (the holder is in it): the
	// notice names the pick and the reporter's role label, never a name or an id.
	counter, yard := 0, 0
	for _, p := range g.notes.posts {
		switch {
		case p.Topic == "task.shortage" && p.Audience == AudienceCounter:
			counter++
		case p.Topic == "task.shortage_yard" && p.Audience == AudienceYard:
			yard++
			if !strings.Contains(p.Summary, "A shortage was reported on a pick on this branch by the Yard Team") {
				t.Errorf("the yard notice names the reporter by role label: %q", p.Summary)
			}
			if strings.Contains(p.Summary, g.b) || strings.Contains(p.Summary, g.a) || strings.Contains(p.Summary, g.task) {
				t.Errorf("the yard notice carries no id: %q", p.Summary)
			}
			if p.BranchID != g.branchA {
				t.Errorf("the yard notice goes to the pick's branch: %q", p.BranchID)
			}
		}
	}
	if counter != 2 || yard != 2 {
		t.Errorf("each of the two shortages tells the counter and the yard: counter %d, yard %d", counter, yard)
	}
}

// fixedRoleLabels answers the yard team label for every user, as the identity resolver does for a yard login.
type fixedRoleLabels struct{}

func (fixedRoleLabels) RoleLabelsForUsers(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		out[id] = "Yard Team"
	}
	return out, nil
}
