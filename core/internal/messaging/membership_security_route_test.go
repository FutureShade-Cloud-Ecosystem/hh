package messaging

// membership_security_route_test.go: the security legs of the membership
// writes at the route level, through the real Handler behind the testkit
// guards over the admin double. Red first: before the walls the reviewer's
// legs were 200s (a third party seated in a direct message, a party and
// then the owner removed from it, a plain member managing a room, the last
// owner removing themselves); each leg here pins its own refusal status so
// a wall that stops being written fails rather than drifting back into the
// catch-all.

import (
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// A direct message's membership is fixed at its two parties: not even the
// room's own opener (its OWNER row) seats a third party.
func TestRoute_Membership_DM_Refuses_The_Third_Party(t *testing.T) {
	fs := newAdminStore()
	dm, _, err := fs.CreateDM(nil, rtCaller, rtBobby)
	if err != nil {
		t.Fatalf("setup dm: %v", err)
	}
	fs.activeUsers[rtDana] = true
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels/"+dm.ID+"/members",
		[]byte(`{"user_id":"`+rtDana+`"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("third party into a dm: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if ok := fs.members[dm.ID][rtDana]; ok {
		t.Fatalf("the third party was seated")
	}
}

// And nobody removes either party from a direct message: the removal of a
// party (or of the room's OWNER row) is the same fixed-membership conflict.
func TestRoute_Membership_DM_Refuses_Removing_A_Party_Or_The_Owner(t *testing.T) {
	fs := newAdminStore()
	dm, _, err := fs.CreateDM(nil, rtCaller, rtBobby)
	if err != nil {
		t.Fatalf("setup dm: %v", err)
	}
	mux, bearer := newAdminRoutes(t, fs)
	for _, target := range []string{rtBobby, rtCaller} {
		rec := adminDo(t, mux, bearer, http.MethodDelete, "/api/v1/messaging/channels/"+dm.ID+"/members/"+target, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("remove %s from a dm: %d %s, want 409", target, rec.Code, rec.Body.String())
		}
	}
	if !fs.members[dm.ID][rtCaller] || !fs.members[dm.ID][rtBobby] {
		t.Fatalf("the dm must keep both its parties")
	}
}

// A plain member manages nobody: adding and removing belong to the room's
// owner and to the roles the role matrix gives Admin on house-messages.
func TestRoute_Membership_Refuses_A_Plain_Member(t *testing.T) {
	fs := newAdminStore()
	room, _, err := fs.CreateChannel(nil, CreateChannelInput{Kind: KindTeam, Name: "Owner managed"}, rtCaller)
	if err != nil {
		t.Fatalf("setup room: %v", err)
	}
	fs.setMember(room.ID, rtBobby) // Bobby: a member, not the owner
	fs.activeUsers[rtDana] = true
	muxAs, bearerAs := newAdminRoutesAs(t, fs, rtBobby, identity.StaffAuthority{
		RoleID: "yard_team", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
	})
	rec := adminDo(t, muxAs, bearerAs, http.MethodPost, "/api/v1/messaging/channels/"+room.ID+"/members",
		[]byte(`{"user_id":"`+rtDana+`"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain member add: %d %s, want 403", rec.Code, rec.Body.String())
	}
	rec = adminDo(t, muxAs, bearerAs, http.MethodDelete, "/api/v1/messaging/channels/"+room.ID+"/members/"+rtDana, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain member remove: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if ok := fs.members[room.ID][rtDana]; ok {
		t.Fatalf("the plain member seated someone")
	}
}

// A removal never drops the room's last OWNER: the founder removing
// themselves is refused, the room keeps its owner.
func TestRoute_Membership_Refuses_The_Last_Owner(t *testing.T) {
	fs := newAdminStore()
	room, _, err := fs.CreateChannel(nil, CreateChannelInput{Kind: KindTeam, Name: "Keep my owner"}, rtCaller)
	if err != nil {
		t.Fatalf("setup room: %v", err)
	}
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodDelete, "/api/v1/messaging/channels/"+room.ID+"/members/"+rtCaller, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("remove the last owner: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if !fs.members[room.ID][rtCaller] {
		t.Fatalf("the room lost its owner")
	}
}

// The owner keeps the pen: the founder seats and drops a member of their
// own room (the regression pin that the walls did not overreach).
func TestRoute_Membership_Owner_Manages_The_Room(t *testing.T) {
	fs := newAdminStore()
	room, _, err := fs.CreateChannel(nil, CreateChannelInput{Kind: KindTeam, Name: "Owner managed"}, rtCaller)
	if err != nil {
		t.Fatalf("setup room: %v", err)
	}
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels/"+room.ID+"/members",
		[]byte(`{"user_id":"`+rtBobby+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("owner add: %d %s", rec.Code, rec.Body.String())
	}
	rec = adminDo(t, mux, bearer, http.MethodDelete, "/api/v1/messaging/channels/"+room.ID+"/members/"+rtBobby, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner remove: %d %s", rec.Code, rec.Body.String())
	}
}

// The role matrix's Admin cell on house-messages (the general manager,
// default.json's one admin cell of thirteen) carries the pen into rooms the
// admin is a member of. The member wall still runs first, so the pen works
// only inside the room.
func TestRoute_Membership_GeneralManagerAdmin_Manages_As_A_Member(t *testing.T) {
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	fs := newAdminStore()
	room, _, err := fs.CreateChannel(nil, CreateChannelInput{Kind: KindTeam, Name: "Admin managed"}, rtBobby)
	if err != nil {
		t.Fatalf("setup room: %v", err)
	}
	fs.setMember(room.ID, rtDana) // the GM is a member (the founder seated them)
	fs.activeUsers[rtDana] = true
	mux, bearer := newAdminRoutesAs(t, fs, rtDana, identity.StaffAuthority{
		RoleID: "general_manager", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	})
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels/"+room.ID+"/members",
		[]byte(`{"user_id":"`+rtCaller+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin add: %d %s", rec.Code, rec.Body.String())
	}
	rec = adminDo(t, mux, bearer, http.MethodDelete, "/api/v1/messaging/channels/"+room.ID+"/members/"+rtCaller, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin remove: %d %s", rec.Code, rec.Body.String())
	}
}
