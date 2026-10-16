package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	s5UnitCallerID = "d7000000-0000-4000-9000-000000000e01"
	s5UnitPeerID   = "d7000000-0000-4000-9000-000000000e02"
)

func newSavedUnitRoutes(t *testing.T, fs *fakeStore, linkedUser string) (*http.ServeMux, string) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{
		RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
	})
	h := NewHandler(&Service{repo: fs}, nil).
		WithUserLinker(func(_ context.Context, _ string) (string, bool) { return linkedUser, true })
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return mux, sg.Bearer(t)
}

func savedUnitDo(t *testing.T, mux *http.ServeMux, bearer, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func savedUnitRoom(t *testing.T, fs *fakeStore, owner, name string) Channel {
	t.Helper()
	room, _, err := fs.CreateChannel(context.Background(), CreateChannelInput{
		Kind: KindTopic, Name: name, Slug: name, Topic: "Saved route probe",
	}, owner)
	if err != nil {
		t.Fatalf("plant room: %v", err)
	}
	return room
}

func savedUnitMessage(fs *fakeStore, room Channel, id, body string) Message {
	m := Message{
		ID: id, ChannelID: room.ID, AuthorID: s5UnitCallerID, Kind: MsgText,
		Body: body, CreatedAt: time.Now(),
	}
	fs.inserted = append(fs.inserted, m)
	return m
}

func savedUnitList(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("saved list: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode saved list: %v (%s)", err, rec.Body.String())
	}
	return body.Items
}

func TestRouteSaved_SaveRequiresMembership(t *testing.T) {
	fs := newFakeStore()
	ownRoom := savedUnitRoom(t, fs, s5UnitCallerID, "s5-unit-own")
	foreignRoom := savedUnitRoom(t, fs, s5UnitPeerID, "s5-unit-foreign")
	own := savedUnitMessage(fs, ownRoom, "e5000000-0000-4000-9000-000000000001", "own message")
	foreign := savedUnitMessage(fs, foreignRoom, "e5000000-0000-4000-9000-000000000002", "foreign message")
	mux, bearer := newSavedUnitRoutes(t, fs, s5UnitCallerID)

	rec := savedUnitDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/saved",
		[]byte(`{"message_id":"`+foreign.ID+`"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member save: %d %s, want 403", rec.Code, rec.Body.String())
	}
	rec = savedUnitDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/saved",
		[]byte(`{"message_id":"e5000000-0000-4000-9000-0000000000ff"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing message save: %d %s, want 404", rec.Code, rec.Body.String())
	}
	rec = savedUnitDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/saved",
		[]byte(`{"message_id":"`+own.ID+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("member save: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode saved item: %v", err)
	}
	if item["message_id"] != own.ID || item["channel_id"] != ownRoom.ID || item["body"] != own.Body {
		t.Fatalf("saved item has the wrong projection: %+v", item)
	}
}

func TestRouteSaved_ListIsPrivateAndUnsaveIsIdempotent(t *testing.T) {
	fs := newFakeStore()
	room := savedUnitRoom(t, fs, s5UnitCallerID, "s5-unit-private")
	fs.setMember(room.ID, s5UnitPeerID)
	message := savedUnitMessage(fs, room, "e5000000-0000-4000-9000-000000000003", "private saved message")
	callerMux, callerBearer := newSavedUnitRoutes(t, fs, s5UnitCallerID)
	peerMux, peerBearer := newSavedUnitRoutes(t, fs, s5UnitPeerID)

	rec := savedUnitDo(t, callerMux, callerBearer, http.MethodPost, "/api/v1/messaging/saved",
		[]byte(`{"message_id":"`+message.ID+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s, want 200", rec.Code, rec.Body.String())
	}
	if items := savedUnitList(t, savedUnitDo(t, callerMux, callerBearer, http.MethodGet, "/api/v1/messaging/saved", nil)); len(items) != 1 {
		t.Fatalf("caller saved list has %d items, want 1", len(items))
	}
	if items := savedUnitList(t, savedUnitDo(t, peerMux, peerBearer, http.MethodGet, "/api/v1/messaging/saved", nil)); len(items) != 0 {
		t.Fatalf("peer sees %d of the caller's saved items, want 0", len(items))
	}
	for attempt := 0; attempt < 2; attempt++ {
		rec = savedUnitDo(t, callerMux, callerBearer, http.MethodDelete, "/api/v1/messaging/saved/"+message.ID, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("unsave attempt %d: %d %s, want 204", attempt+1, rec.Code, rec.Body.String())
		}
	}
	if items := savedUnitList(t, savedUnitDo(t, callerMux, callerBearer, http.MethodGet, "/api/v1/messaging/saved", nil)); len(items) != 0 {
		t.Fatalf("unsave left %d items, want 0", len(items))
	}
}

func TestRouteSaved_InvalidMessageIDsReturn400(t *testing.T) {
	fs := newFakeStore()
	mux, bearer := newSavedUnitRoutes(t, fs, s5UnitCallerID)
	rec := savedUnitDo(t, mux, bearer, http.MethodPost, "/api/v1/messaging/saved",
		[]byte(`{"message_id":"not-a-uuid"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid save id: %d %s, want 400", rec.Code, rec.Body.String())
	}
	rec = savedUnitDo(t, mux, bearer, http.MethodDelete, "/api/v1/messaging/saved/not-a-uuid", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid unsave id: %d %s, want 400", rec.Code, rec.Body.String())
	}
}
