package messaging

// channels_admin_gate_route_test.go: the change 7 ruling (row 2045, ruling A,
// and the c7 board "New channel, admins only") at the route level. Founding a
// channel is the admins' pen: the role matrix's Admin cell on house-messages
// (the general manager) holds it, every other staff role is refused with 403
// before any wall of the founding runs, and nothing is written. Red first:
// before the gate the route answered the frontline founding with 201 (the
// recorded red evidence is that 201), and the refused founding created the
// room.

import (
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// newFoundingRoutes wires the real Handler over the double behind the
// testkit guards as the general manager, the matrix's one admin on
// house-messages, with the decided matrix published for the wall (the wall
// fails closed without it, the same as the membership walls' tests).
func newFoundingRoutes(t *testing.T, fs *adminStore) (*http.ServeMux, string) {
	t.Helper()
	prev := publishDefaultRolesForTest(t)
	t.Cleanup(func() { PublishRoleConfig(prev) })
	return newAdminRoutesAs(t, fs, rtDana, identity.StaffAuthority{
		RoleID: "general_manager", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	})
}

// A frontline caller (the route harness's default inside sales authority)
// founding a channel is refused with 403, in the gate's own words, and the
// refusal writes nothing.
func TestRoute_CreateChannel_Frontline_Is_Refused(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
		[]byte(`{"kind":"TEAM","name":"Paving crew"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("frontline founding: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if len(fs.createdChannels) != 0 {
		t.Fatalf("the refused founding created a channel: %+v", fs.createdChannels)
	}
}

// A reserved slug does not matter to the refusal order: the gate runs
// first, so a non-admin probing the system namespaces learns the gate's
// answer, not the slug wall's.
func TestRoute_CreateChannel_Gate_Runs_Before_The_Slug_Walls(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
		[]byte(`{"kind":"TEAM","slug":"dm-probe","name":"Probe"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("frontline founding on a reserved slug: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if len(fs.createdChannels) != 0 {
		t.Fatalf("the refused founding created a channel: %+v", fs.createdChannels)
	}
}

// The admin founding keeps the route's own shape: 201 the first time, 200
// on the idempotent replay, one room.
func TestRoute_CreateChannel_Admin_Founds_And_Replays(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newFoundingRoutes(t, fs)
	body := []byte(`{"kind":"TEAM","name":"Paving crew","topic":"The paving crew's room"}`)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin founding: %d %s, want 201", rec.Code, rec.Body.String())
	}
	rec = adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin replay: %d %s, want 200", rec.Code, rec.Body.String())
	}
	if len(fs.createdChannels) != 1 {
		t.Fatalf("the replay created a second channel: %+v", fs.createdChannels)
	}
}

// The direct message route is NOT the admins' pen: a frontline caller still
// opens a DM with a colleague (the boards' New message is everyone's; only
// New channel is admins only).
func TestRoute_CreateDM_Is_Not_Gated_To_Admins(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/dms",
		[]byte(`{"user_id":"`+rtBobby+`"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("frontline dm: %d %s, want 201", rec.Code, rec.Body.String())
	}
}

// An unknown auto_scope on the rule write is a client fault the boundary answers with 400, not a
// 500: the sentinel reaches writeErr instead of a plain error falling through to the default.
func TestRoute_UpdateMembershipRule_BadAutoScope_Returns400(t *testing.T) {
	fs := newAdminStore()
	fs.plantRoom(Channel{ID: rtYardTrt, Kind: KindTeam, Slug: "yard-trt", Name: "Yard"}, rtDana)
	mux, bearer := newFoundingRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPut,
		"/api/v1/messaging/channels/"+rtYardTrt+"/membership-rule",
		[]byte(`{"visibility":"PUBLIC","auto_scope":"GALAXY"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad auto_scope: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if c, ok := fs.rooms["yard-trt"]; !ok || c.Visibility != "" {
		t.Fatalf("a refused rule write must not run: %+v", c)
	}
}
