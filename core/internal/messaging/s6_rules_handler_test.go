package messaging

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// TestHandler_PatchChannelVisibility_NonMember_Refuses_403 proves the
// settings-panel hand on visibility runs the membership wall first: a
// caller who is not a member of the channel is refused before the manager
// wall runs. The room's settings panel hides behind the membership wall.
func TestHandler_PatchChannelVisibility_NonMember_Refuses_403(t *testing.T) {
	fs := newFakeStore()
	fs.exists["550e8400-e29b-41d4-a716-446655440001"] = true // exists but caller is not a member
	svc := &Service{repo: fs}
	h := NewHandler(svc, nil)

	body := strings.NewReader(`{"visibility":"PRIVATE"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/messaging/channels/550e8400-e29b-41d4-a716-446655440001/visibility", body)
	req.SetPathValue("id", "550e8400-e29b-41d4-a716-446655440001")
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	rr := httptest.NewRecorder()
	h.patchChannelVisibility(rr, req, guards.StaffCaller{StaffScope: authz.StaffScope{PlatformUserID: uAlice}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("a non-member must answer 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandler_PatchChannelVisibility_Owner_Reaches_200 proves the happy
// path: a room owner PATCHes PRIVATE and the store's SetChannelVisibility
// records the new value. The response is status: ok. The OWNER pen is the
// first line of the manager wall; the role matrix's Admin cell on
// house-messages is the second.
func TestHandler_PatchChannelVisibility_Owner_Reaches_200(t *testing.T) {
	fs := newFakeStore()
	id := "550e8400-e29b-41d4-a716-446655440002"
	fs.exists[id] = true
	fs.setMember(id, uAlice)
	fs.memberRoles[id+"|"+uAlice] = "OWNER"
	svc := &Service{repo: fs}
	h := NewHandler(svc, nil)

	body := strings.NewReader(`{"visibility":"PRIVATE"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/messaging/channels/"+id+"/visibility", body)
	req.SetPathValue("id", id)
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	rr := httptest.NewRecorder()
	h.patchChannelVisibility(rr, req, guards.StaffCaller{StaffScope: authz.StaffScope{PlatformUserID: uAlice}})
	if rr.Code != http.StatusOK {
		t.Fatalf("an owner patch must answer 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "ok" {
		t.Fatalf("the body must answer ok, got %v", got)
	}
}

// TestHandler_PatchChannelVisibility_Invalid_Body_Refuses_400 proves the
// body validator: an invalid JSON is the client's fault (400). The
// service's SetVisibility carries its own ErrBadVisibility refusal for a
// valid body whose value is outside PUBLIC/PRIVATE.
func TestHandler_PatchChannelVisibility_Invalid_Body_Refuses_400(t *testing.T) {
	fs := newFakeStore()
	id := "550e8400-e29b-41d4-a716-446655440003"
	fs.exists[id] = true
	fs.setMember(id, uAlice)
	svc := &Service{repo: fs}
	h := NewHandler(svc, nil)

	body := strings.NewReader(`{not json`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/messaging/channels/"+id+"/visibility", body)
	req.SetPathValue("id", id)
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	rr := httptest.NewRecorder()
	h.patchChannelVisibility(rr, req, guards.StaffCaller{StaffScope: authz.StaffScope{PlatformUserID: uAlice}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON must answer 400, got %d", rr.Code)
	}
}

// TestHandler_DeleteChannel_NonMember_Refuses_403 proves the manager wall
// runs after the membership wall: a caller who is not a member of the
// channel cannot archive it.
func TestHandler_DeleteChannel_NonMember_Refuses_403(t *testing.T) {
	fs := newFakeStore()
	id := "550e8400-e29b-41d4-a716-446655440004"
	fs.exists[id] = true // exists but caller is not a member
	svc := &Service{repo: fs}
	h := NewHandler(svc, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/messaging/channels/"+id, nil)
	req.SetPathValue("id", id)
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	rr := httptest.NewRecorder()
	h.deleteChannel(rr, req, guards.StaffCaller{StaffScope: authz.StaffScope{PlatformUserID: uAlice}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("a non-member must answer 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandler_DeleteChannel_Already_Archived_Refuses_409 proves the
// repository's re-archive refusal carries through the handler: a second
// DELETE answers 409, never a re-archive. The caller is the room OWNER,
// so the manager wall passes; the repository's pre-update read carries
// the "already archived" verdict to the route.
func TestHandler_DeleteChannel_Already_Archived_Refuses_409(t *testing.T) {
	fs := newFakeStore()
	id := "550e8400-e29b-41d4-a716-446655440005"
	fs.exists[id] = true
	fs.archived[id] = true // already archived by an earlier manager wall call
	fs.setMember(id, uAlice)
	fs.memberRoles[id+"|"+uAlice] = "OWNER"
	svc := &Service{repo: fs}
	h := NewHandler(svc, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/messaging/channels/"+id, nil)
	req.SetPathValue("id", id)
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	rr := httptest.NewRecorder()
	h.deleteChannel(rr, req, guards.StaffCaller{StaffScope: authz.StaffScope{PlatformUserID: uAlice}})
	if rr.Code != http.StatusConflict {
		t.Fatalf("an already-archived channel must answer 409, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandler_AddChannelMember_Carries_Added_Reason_And_Adder proves the
// change 7 ruling 7 audit pair the Settings > Membership rules panel
// records: the body's added_reason and added_by land on the row, the
// route answers the row the repository wrote, and an absent added_by
// defaults to the caller (the route is admin-gated; the audit names the
// admin).
func TestHandler_AddChannelMember_Carries_Added_Reason_And_Adder(t *testing.T) {
	fs := newFakeStore()
	id := "550e8400-e29b-41d4-a716-446655440006"
	fs.exists[id] = true
	fs.setMember(id, uAlice)
	fs.memberRoles[id+"|"+uAlice] = "OWNER"
	svc := &Service{repo: fs}
	h := NewHandler(svc, nil)

	targetID := "550e8400-e29b-41d4-a716-44665544abcd"
	aliceUUID := "550e8400-e29b-41d4-a716-44665544a11c"
	reason := "added by hand: paving crew swap for the Trenton run, week ahead"
	body := strings.NewReader(fmt.Sprintf(
		`{"user_id":"%s","role":"MEMBER","added_reason":%q,"added_by":"%s"}`,
		targetID, reason, aliceUUID))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messaging/channels/"+id+"/members", body)
	req.SetPathValue("id", id)
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
	rr := httptest.NewRecorder()
	h.addChannelMember(rr, req, guards.StaffCaller{StaffScope: authz.StaffScope{PlatformUserID: uAlice}})
	if rr.Code != http.StatusOK {
		t.Fatalf("the seat must answer 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["added_reason"] != reason {
		t.Fatalf("added_reason lost: got %q want %q", got["added_reason"], reason)
	}
	if got["added_by"] != aliceUUID {
		t.Fatalf("added_by lost: got %q want %q", got["added_by"], aliceUUID)
	}
	if got["source"] != "EXPLICIT" {
		t.Fatalf("the row's source must be EXPLICIT, got %q", got["source"])
	}
}
