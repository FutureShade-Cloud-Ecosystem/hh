package messaging

// channels_security_route_test.go: the security legs of the channel-create
// surface at the route level, through the real Handler behind the testkit
// guards over the admin double. Red first: before the walls the collision
// arm answered any existing room to any caller (the recorded red evidence
// is the 200 these legs now refuse), and a caller chosen slug could squat
// the system namespaces (the recorded red evidence is the 201).

import (
	"net/http"
	"testing"
)

// A slug the caller holds no membership in is refused with 409, in words,
// and the refusal seats nobody: the double's membership map is the proof.
// Founding is the admins' pen (change 7, ruling A), so the leg drives the
// general manager and the collision wall runs behind the gate.
func TestRoute_CreateChannel_Collision_Refuses_A_NonMember(t *testing.T) {
	fs := newAdminStore()
	// A room founded by another staffer: rtBobby is its only member.
	foreign, _, err := fs.CreateChannel(nil, CreateChannelInput{Kind: KindTeam, Name: "Finance desk"}, rtBobby)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	mux, bearer := newFoundingRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
		[]byte(`{"kind":"TEAM","name":"Finance desk"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("founding a room the caller is not in: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if ok := fs.members[foreign.ID][rtCaller]; ok {
		t.Fatalf("the refused founding seated the caller")
	}
	if len(fs.createdChannels) != 1 {
		t.Fatalf("the refusal created a second room: %+v", fs.createdChannels)
	}
}

// The replay keeps answering the caller's own room: 200, one channel, the
// founder still its member.
func TestRoute_CreateChannel_Collision_Answers_The_Callers_Own_Room(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newFoundingRoutes(t, fs)
	body := []byte(`{"kind":"TEAM","name":"Paving crew"}`)
	if rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels", body); rec.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", rec.Code, rec.Body.String())
	}
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("replayed create: %d %s, want 200 on the caller's own room", rec.Code, rec.Body.String())
	}
	if len(fs.createdChannels) != 1 {
		t.Fatalf("the replay created a second channel: %+v", fs.createdChannels)
	}
}

// A slug in a system namespace (dm-, rep-, approvals-) is refused with 400
// even when the slug is fresh: a room founded there would capture a later
// system provision that converges on the same slug.
func TestRoute_CreateChannel_Refuses_Reserved_Slug_Namespaces(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newFoundingRoutes(t, fs)
	for _, slug := range []string{"dm-probe", "rep-probe", "approvals-probe"} {
		rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
			[]byte(`{"kind":"TEAM","slug":"`+slug+`","name":"Probe"}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("founding slug %q: %d %s, want 400", slug, rec.Code, rec.Body.String())
		}
	}
	if len(fs.createdChannels) != 0 {
		t.Fatalf("a reserved slug created a channel: %+v", fs.createdChannels)
	}
}

// The slug column is CITEXT, so the unique index folds case; the reserved
// namespace wall folds the same way or an upper cased APPROVALS- slug
// squats a system room the next provision converges on. Any case variant
// of a system prefix is refused with 400.
func TestRoute_CreateChannel_Refuses_Reserved_Slug_In_Every_Case(t *testing.T) {
	fs := newAdminStore()
	mux, bearer := newFoundingRoutes(t, fs)
	for _, slug := range []string{
		"APPROVALS-probe", "Rep-probe", "dM-probe", "DM-PROBE",
	} {
		rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
			[]byte(`{"kind":"TEAM","slug":"`+slug+`","name":"Probe"}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("founding slug %q: %d %s, want 400", slug, rec.Code, rec.Body.String())
		}
	}
	if len(fs.createdChannels) != 0 {
		t.Fatalf("a case variant of a reserved slug created a channel: %+v", fs.createdChannels)
	}
}

// A direct message is reached only through its own route: the dm- namespace
// is refused outright (the reserved prefix wall), and a DM room holding a
// slug outside the reserved namespaces (the backstop arm) is refused by
// kind, even for a party of the room.
func TestRoute_CreateChannel_Never_Reaches_ADM_By_Slug(t *testing.T) {
	fs := newAdminStore()
	dm, _, err := fs.CreateDM(nil, rtCaller, rtBobby)
	if err != nil {
		t.Fatalf("setup dm: %v", err)
	}
	fs.plantRoom(dm, rtCaller, rtBobby)
	mux, bearer := newFoundingRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
		[]byte(`{"kind":"TEAM","slug":"`+dm.Slug+`","name":"Not a dm"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("founding the dm namespace: %d %s, want 400", rec.Code, rec.Body.String())
	}
	// The kind backstop: a DM whose slug sits outside the reserved
	// namespaces (planted, as only a hand written row could arrange) is
	// still never answered by the channel create.
	fs.plantRoom(Channel{ID: "e2000000-0000-4000-9000-0000000000d1", Kind: KindDM,
		Slug: "pair-room", Name: "DM", Tier: TierStandard}, rtCaller, rtBobby)
	rec = adminDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/channels",
		[]byte(`{"kind":"TEAM","slug":"pair-room","name":"Not a dm"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("founding a dm's slug outside the namespace: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if len(fs.createdChannels) != 0 {
		t.Fatalf("a dm slug founded a TEAM channel: %+v", fs.createdChannels)
	}
}
