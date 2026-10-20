//go:build integration

// yard_holder_rule_integration_test.go: the holder rule for every write that changes a held pick's work
// (UX-76b, FROM-PRINCIPAL items 52.3 and 55.2), on top of the Stage and Save pick guard of UX-76.
//
//   - Skid count, start (accept) and done are the holder's, on the locked row: ErrNotHolder, the same 409 and words.
//   - Cancel is the holder's, or a Yard Manager or a Tier-1 leader's as an explicit act with a reason that writes
//     an audit event naming the role and the reason; another yard hand is refused.
//   - A yard write with no actor (a staff user with no linked ERP user) is refused, so no pick is claimed with no
//     holder.
//
// Through REST (the handlers) and through the sync door. Every request's status and body is logged (run with -v).
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
)

const holderRuleSkids = `{"skid_count":2,"reason":"CONDENSED_SKIDS"}`

// restCtx runs one request through a handler as the given staff context and logs the transcript line.
func (g *holderGuard) restCtx(who string, sc identity.StaffContext, method, path, body string, do func(http.ResponseWriter, *http.Request, guards.StaffCaller)) (int, string) {
	g.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.SetPathValue("id", g.task)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(identity.ContextWithStaff(r.Context(), sc))
	w := httptest.NewRecorder()
	do(w, r, guards.StaffCaller{})
	out := strings.TrimSpace(w.Body.String())
	g.t.Logf("REST  %-4s %-36s as %s -> %d %s", method, path, who, w.Code, out)
	return w.Code, out
}

func (g *holderGuard) setStatus(status string) {
	g.t.Helper()
	if _, err := g.pool.Exec(context.Background(), `UPDATE tasks SET status = $2 WHERE id = $1::uuid`, g.task, status); err != nil {
		g.t.Fatalf("set status %s: %v", status, err)
	}
}

func (g *holderGuard) yardManager(user string) identity.StaffContext {
	sc := g.staffCtx(user)
	sc.RoleCode, sc.Tier = "YARD_MANAGER", identity.TierManager
	return sc
}

func (g *holderGuard) cancelEvent() string {
	var note string
	if err := g.pool.QueryRow(context.Background(),
		`SELECT COALESCE(note, '') FROM task_events WHERE task_id = $1::uuid AND to_status = 'CANCELLED' ORDER BY created_at DESC LIMIT 1`, g.task).Scan(&note); err != nil {
		return ""
	}
	return note
}

func wantHolder409(t *testing.T, what string, code int, body string) {
	t.Helper()
	if code != http.StatusConflict || !strings.Contains(body, ErrNotHolderWords) {
		t.Errorf("%s from a non holder must be refused with the holder 409 and its words, got %d %s", what, code, body)
	}
}

func wantHolderRejected(t *testing.T, what string, res apigen.SyncPushResult) {
	t.Helper()
	if res.Status != "rejected" || res.Error == nil || !strings.Contains(*res.Error, "someone else is on this pick") {
		t.Errorf("%s from a non holder must be rejected with the holder words, got %+v", what, res)
	}
}

func TestYardHolderRule_RESTSkidsStartAndDoneAreTheHolders(t *testing.T) {
	for _, leg := range []struct {
		name, from, want string
		do               func(g *holderGuard) (int, string)
	}{
		{"skid count", "IN_PROGRESS", "IN_PROGRESS", func(g *holderGuard) (int, string) {
			return g.rest("B", g.b, http.MethodPut, "/api/v1/yard/picks/{id}/skids", holderRuleSkids, g.h.recordSkids)
		}},
		{"start", "ACCEPTED", "ACCEPTED", func(g *holderGuard) (int, string) {
			return g.rest("B", g.b, http.MethodPost, "/api/v1/tasks/{id}/accept", "", g.h.accept)
		}},
		{"done", "IN_PROGRESS", "IN_PROGRESS", func(g *holderGuard) (int, string) {
			return g.rest("B", g.b, http.MethodPost, "/api/v1/tasks/{id}/done", "", g.h.done)
		}},
	} {
		t.Run(leg.name, func(t *testing.T) {
			g := newHolderGuard(t)
			g.setStatus(leg.from)
			code, body := leg.do(g)
			wantHolder409(t, "REST "+leg.name, code, body)
			if s := g.status(); s != leg.want {
				t.Errorf("a refused write changes nothing: the pick is %s", s)
			}
			if h := g.holder(); h != g.a {
				t.Errorf("the holder is still A, got %q", h)
			}
		})
	}
}

func TestYardHolderRule_TheHolderStillSkidsStartsAndFinishes(t *testing.T) {
	g := newHolderGuard(t)
	if code, body := g.rest("A", g.a, http.MethodPut, "/api/v1/yard/picks/{id}/skids", holderRuleSkids, g.h.recordSkids); code != http.StatusOK {
		t.Errorf("the holder's skid count: %d %s", code, body)
	}
	g.setStatus("ACCEPTED")
	if code, body := g.rest("A", g.a, http.MethodPost, "/api/v1/tasks/{id}/accept", "", g.h.accept); code != http.StatusOK {
		t.Errorf("the holder's start: %d %s", code, body)
	}
	if code, body := g.rest("A", g.a, http.MethodPost, "/api/v1/tasks/{id}/done", "", g.h.done); code != http.StatusOK {
		t.Errorf("the holder's done: %d %s", code, body)
	}
}

func TestYardHolderRule_RESTCancel(t *testing.T) {
	t.Run("another yard hand is refused", func(t *testing.T) {
		g := newHolderGuard(t)
		code, body := g.rest("B", g.b, http.MethodPost, "/api/v1/tasks/{id}/cancel", `{"note":"no longer needed"}`, g.h.cancel)
		if code != http.StatusConflict || !strings.Contains(body, ErrCancelNotAllowedWords) {
			t.Errorf("cancel from another yard hand must be refused with the cancel 409 and its words, got %d %s", code, body)
		}
		if s := g.status(); s != "IN_PROGRESS" {
			t.Errorf("a refused cancel changes nothing: the pick is %s", s)
		}
	})
	t.Run("the holder cancels", func(t *testing.T) {
		g := newHolderGuard(t)
		if code, body := g.rest("A", g.a, http.MethodPost, "/api/v1/tasks/{id}/cancel", `{"note":"customer called"}`, g.h.cancel); code != http.StatusOK {
			t.Errorf("the holder's cancel: %d %s", code, body)
		}
	})
	t.Run("a yard manager needs a reason, then the audit event names role and reason", func(t *testing.T) {
		g := newHolderGuard(t)
		mgr := g.yardManager(g.b)
		code, body := g.restCtx("B (yard manager)", mgr, http.MethodPost, "/api/v1/tasks/{id}/cancel", `{}`, g.h.cancel)
		if code != http.StatusBadRequest {
			t.Errorf("a manager's cancel of a held pick without a reason is a 400, got %d %s", code, body)
		}
		if s := g.status(); s != "IN_PROGRESS" {
			t.Errorf("a cancel without a reason changes nothing: %s", s)
		}
		code, body = g.restCtx("B (yard manager)", mgr, http.MethodPost, "/api/v1/tasks/{id}/cancel", `{"note":"customer moved the order"}`, g.h.cancel)
		if code != http.StatusOK {
			t.Fatalf("a yard manager cancels with a reason: %d %s", code, body)
		}
		note := g.cancelEvent()
		if !strings.Contains(note, "YARD_MANAGER") || !strings.Contains(note, "customer moved the order") {
			t.Errorf("the audit event names the role and the reason: %q", note)
		}
	})
	t.Run("a Tier 1 leader is above the yard manager", func(t *testing.T) {
		g := newHolderGuard(t)
		lead := g.staffCtx(g.b)
		lead.RoleCode, lead.Tier, lead.Scope = "GENERAL_MANAGER", identity.TierLeadership, identity.ScopeGlobal
		if code, body := g.restCtx("B (general manager)", lead, http.MethodPost, "/api/v1/tasks/{id}/cancel", `{"note":"duplicate order"}`, g.h.cancel); code != http.StatusOK {
			t.Errorf("a Tier 1 leader cancels with a reason: %d %s", code, body)
		}
	})
	t.Run("another branch's manager is refused", func(t *testing.T) {
		g := newHolderGuard(t)
		mgr := g.yardManager(g.b)
		mgr.Branches = map[string]bool{uuid.NewString(): true}
		code, body := g.restCtx("B (yard manager, other branch)", mgr, http.MethodPost, "/api/v1/tasks/{id}/cancel", `{"note":"x"}`, g.h.cancel)
		if code != http.StatusConflict {
			t.Errorf("a manager who does not reach the branch is refused like any other hand: %d %s", code, body)
		}
	})
}

func TestYardHolderRule_NoActorIsRefused(t *testing.T) {
	g := newHolderGuard(t)
	g.setStatus("QUEUED")
	if _, err := g.pool.Exec(context.Background(), `UPDATE tasks SET claimed_by = NULL, claimed_at = NULL WHERE id = $1::uuid`, g.task); err != nil {
		t.Fatalf("unclaim: %v", err)
	}
	none := g.staffCtx("")
	code, body := g.restCtx("no linked user", none, http.MethodPost, "/api/v1/tasks/{id}/claim", "", g.h.claim)
	if code != http.StatusForbidden || !strings.Contains(body, ErrNoActorWords) {
		t.Errorf("a claim with no actor is refused 403 with its words, got %d %s", code, body)
	}
	if s, h := g.status(), g.holder(); s != "QUEUED" || h != "" {
		t.Errorf("a refused claim leaves the pick unclaimed and queued: %s held by %q", s, h)
	}
	// Through sync: the link answers no ERP user.
	g.as("")
	res := g.syncPush("no linked user", "pick_tasks", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update",
		Payload: map[string]any{"id": g.task, "status": "ACCEPTED"}})
	if res.Status != "rejected" {
		t.Errorf("a sync claim with no actor must be rejected, got %+v", res)
	}
	if s, h := g.status(), g.holder(); s != "QUEUED" || h != "" {
		t.Errorf("a rejected sync claim leaves the pick unclaimed and queued: %s held by %q", s, h)
	}
}

func TestYardHolderRule_SyncStartDoneCancelAndSkidsAreTheHolders(t *testing.T) {
	for _, leg := range []struct{ name, from, to string }{
		{"start", "ACCEPTED", "IN_PROGRESS"},
		{"done", "IN_PROGRESS", "DONE"},
		{"cancel", "IN_PROGRESS", "CANCELLED"},
	} {
		t.Run(leg.name, func(t *testing.T) {
			g := newHolderGuard(t)
			g.setStatus(leg.from)
			g.as(g.b)
			res := g.syncPush("B", "pick_tasks", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update",
				Payload: map[string]any{"id": g.task, "status": leg.to}})
			wantHolderRejected(t, "sync "+leg.name, res)
			if s := g.status(); s != leg.from {
				t.Errorf("a rejected op changes nothing: the pick is %s", s)
			}
		})
	}
	t.Run("skid count stays off the sync door", func(t *testing.T) {
		g := newHolderGuard(t)
		g.as(g.b)
		res := g.syncPush("B", "pick_tasks", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update",
			Payload: map[string]any{"id": g.task, "skid_count": float64(3)}})
		if res.Status != "rejected" {
			t.Errorf("a sync skid count is rejected for everyone, got %+v", res)
		}
	})
	t.Run("the holder still starts and finishes through sync", func(t *testing.T) {
		g := newHolderGuard(t)
		g.setStatus("ACCEPTED")
		g.as(g.a)
		for _, to := range []string{"IN_PROGRESS", "DONE"} {
			if res := g.syncPush("A", "pick_tasks", apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update",
				Payload: map[string]any{"id": g.task, "status": to}}); res.Status != "applied" {
				t.Errorf("the holder's sync move to %s: %+v %v", to, res, res.Error)
			}
		}
	})
}
