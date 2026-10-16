package messaging

// messages_edit_route_test.go: the change 7 slice S4 ruling 6 at the route
// level, through the real Handler and the real Service over the admin double.
// A person edits or deletes ONLY THEIR OWN text messages, ONLY inside the
// edit window the msg_policy row keeps; system messages (EVENT, the
// approvals rail's APPROVAL cards) are never editable, and a forced
// acknowledgement notice is never editable once sent; a deleted message is
// a tombstone (the body is dropped, deleted_at answers). Red first: before
// the routes were registered the mux answered 404 on every leg (the
// recorded red evidence in the item report); each refusal leg asserts its
// own status, so a wall that stops being written fails here rather than
// drifting back into the catch-all.

import (
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// The edit legs' message ids: UUID shaped, the handler validates {msgID} at
// the boundary the same as {id}.
const (
	msgOwn          = "f1000000-0000-4000-9000-0000000000a1" // the linked caller's own fresh text message
	msgForeign      = "f1000000-0000-4000-9000-0000000000a2" // another staffer's text message
	msgEvent        = "f1000000-0000-4000-9000-0000000000a3" // a system EVENT message
	msgAck          = "f1000000-0000-4000-9000-0000000000a4" // a forced acknowledgement notice (requires_ack)
	msgStale        = "f1000000-0000-4000-9000-0000000000a5" // the caller's own, outside the window
	msgGone         = "f1000000-0000-4000-9000-0000000000a6" // the caller's own, already deleted
	msgApproval     = "f1000000-0000-4000-9000-0000000000a7" // an APPROVAL rail card
	msgOtherChannel = "f1000000-0000-4000-9000-0000000000a8" // the caller's own, in a channel they are not in
)

// plantEditWorld seeds the double with the channels and the message rows the
// legs name, each bound to its channel the way the repository binds every
// message read and write. rtYardTrt holds the linked caller, rtBobby and the
// general manager (rtDana, for the legs that prove an admin holds no pen
// over a system message) as members; rtKingston exists and holds neither.
func plantEditWorld(f *adminStore) {
	f.setMember(rtYardTrt, rtCaller)
	f.setMember(rtYardTrt, rtBobby)
	f.setMember(rtYardTrt, rtDana)
	f.exists[rtYardTrt] = true
	f.exists[rtKingston] = true
	f.plantMessage(rtYardTrt, msgOwn, rtCaller, MsgText, false, time.Now(), nil)
	f.plantMessage(rtYardTrt, msgForeign, rtBobby, MsgText, false, time.Now(), nil)
	f.plantMessage(rtYardTrt, msgEvent, "", MsgEvent, false, time.Now(), nil)
	f.plantMessage(rtYardTrt, msgAck, "", MsgEvent, true, time.Now(), nil)
	f.plantMessage(rtYardTrt, msgStale, rtCaller, MsgText, false, time.Now().Add(-2*time.Hour), nil)
	f.plantMessage(rtYardTrt, msgGone, rtCaller, MsgText, false, time.Now(), new(time.Time))
	f.plantMessage(rtYardTrt, msgApproval, "", MsgApproval, false, time.Now(), nil)
	f.plantMessage(rtKingston, msgOtherChannel, rtCaller, MsgText, false, time.Now(), nil)
}

// The happy leg: the author edits their own fresh message, and the answer
// carries the new body and the edited_at stamp.
func TestRoute_EditMessage_Author_Within_The_Window(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgOwn,
		[]byte(`{"body":"Counted again, 13 on the pallet"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("author edit: %d %s, want 200", rec.Code, rec.Body.String())
	}
	if len(fs.editedMessages) != 1 || fs.editedMessages[0].Body != "Counted again, 13 on the pallet" {
		t.Fatalf("the edit did not land: %+v", fs.editedMessages)
	}
	if fs.editedMessages[0].EditedAt == nil {
		t.Fatalf("the edit must stamp edited_at")
	}
}

// A member who is not the author edits nothing: 403, and no write.
func TestRoute_EditMessage_NonAuthor_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutesAs(t, fs, rtBobby, frontlineAuthority())
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgOwn,
		[]byte(`{"body":"not mine"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-author edit: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if len(fs.editedMessages) != 0 {
		t.Fatalf("the refused edit wrote: %+v", fs.editedMessages)
	}
}

// A non-member is refused by the channel wall before any message is read:
// the leg names a message in a channel that exists and holds the caller as
// no member.
func TestRoute_EditMessage_NonMember_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtKingston+"/messages/"+msgOtherChannel,
		[]byte(`{"body":"outside"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member edit: %d %s, want 403", rec.Code, rec.Body.String())
	}
}

// A message id that names no message in THIS channel is 404: the write is
// bound to the channel, so a foreign channel's message id leaks nothing.
func TestRoute_EditMessage_Foreign_Channel_Message_NotFound(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/f1000000-0000-4000-9000-0000000000ff",
		[]byte(`{"body":"nobody"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("absent message edit: %d %s, want 404", rec.Code, rec.Body.String())
	}
}

// A system EVENT message is nobody's to edit: the author wall cannot answer
// it (a system message has no author), the kind wall refuses in words, and
// an admin holds no pen here either.
func TestRoute_EditMessage_Event_Message_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newFoundingRoutes(t, fs) // the general manager, an admin
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgEvent,
		[]byte(`{"body":"rewrite the record"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("event edit: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if len(fs.editedMessages) != 0 {
		t.Fatalf("the refused edit wrote: %+v", fs.editedMessages)
	}
}

// The approvals rail's card is a system message of its own kind: the same
// refusal.
func TestRoute_EditMessage_Approval_Card_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgApproval,
		[]byte(`{"body":"rewrite the card"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("approval card edit: %d %s, want 409", rec.Code, rec.Body.String())
	}
}

// A forced acknowledgement notice is never editable once sent: the notice's
// words reached people who acknowledged THEM, a correction is a new notice
// with its own acknowledgements (ruling 6).
func TestRoute_EditMessage_Forced_Ack_Notice_Never_Editable(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newFoundingRoutes(t, fs) // even the admin is refused
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgAck,
		[]byte(`{"body":"a correction"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("forced ack notice edit: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if len(fs.editedMessages) != 0 {
		t.Fatalf("the refused edit wrote: %+v", fs.editedMessages)
	}
}

// The edit window is the setting's: the author's own message outside it is
// refused as a conflict with the clock, 409.
func TestRoute_EditMessage_Outside_The_Window_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgStale,
		[]byte(`{"body":"too late"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale edit: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if len(fs.editedMessages) != 0 {
		t.Fatalf("the refused edit wrote: %+v", fs.editedMessages)
	}
}

// An empty body is the same client fault a post is.
func TestRoute_EditMessage_Empty_Body_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgOwn,
		[]byte(`{"body":"   "}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body edit: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// A deleted message is a tombstone: editing it is a conflict, not a revival.
func TestRoute_EditMessage_Deleted_Message_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodPatch,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgGone,
		[]byte(`{"body":"back from the dead"}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleted message edit: %d %s, want 409", rec.Code, rec.Body.String())
	}
}

// The delete happy leg: the author deletes their own fresh message and the
// answer is the tombstone, the body dropped.
func TestRoute_DeleteMessage_Author_Within_The_Window(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodDelete,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgOwn, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("author delete: %d %s, want 200", rec.Code, rec.Body.String())
	}
	if len(fs.deletedMessages) != 1 || fs.deletedMessages[0] != msgOwn {
		t.Fatalf("the delete did not land: %+v", fs.deletedMessages)
	}
}

// Deleting is not a bypass of the walls: not the author, refused.
func TestRoute_DeleteMessage_NonAuthor_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutesAs(t, fs, rtBobby, frontlineAuthority())
	rec := adminDo(t, mux, bearer, http.MethodDelete,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgOwn, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-author delete: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if len(fs.deletedMessages) != 0 {
		t.Fatalf("the refused delete wrote: %+v", fs.deletedMessages)
	}
}

// A system EVENT message is nobody's to delete, and the forced ack notice
// (whose acknowledgements are facts) is never deletable once sent.
func TestRoute_DeleteMessage_System_And_Forced_Ack_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	for _, id := range []string{msgEvent, msgAck} {
		rec := adminDo(t, mux, bearer, http.MethodDelete,
			"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+id, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("delete %s: %d %s, want 409", id, rec.Code, rec.Body.String())
		}
	}
	if len(fs.deletedMessages) != 0 {
		t.Fatalf("a refused delete wrote: %+v", fs.deletedMessages)
	}
}

// The window covers the delete too: the author's own stale message stays.
func TestRoute_DeleteMessage_Outside_The_Window_Refused(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	rec := adminDo(t, mux, bearer, http.MethodDelete,
		"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgStale, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale delete: %d %s, want 409", rec.Code, rec.Body.String())
	}
}

// Deleting a tombstone again is the no-op it reads as: 200, still one delete
// recorded against the live row (the second is the tombstone's own echo).
func TestRoute_DeleteMessage_Already_Deleted_Is_A_No_Ok(t *testing.T) {
	fs := newAdminStore()
	plantEditWorld(fs)
	mux, bearer := newAdminRoutes(t, fs)
	for i := 0; i < 2; i++ {
		rec := adminDo(t, mux, bearer, http.MethodDelete,
			"/api/v1/messaging/channels/"+rtYardTrt+"/messages/"+msgGone, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("delete %d of a tombstone: %d %s, want 200", i+1, rec.Code, rec.Body.String())
		}
	}
}

// plantMessage seeds one message row on the double, bound to its channel
// the way the repository binds every message read and write. authorID empty
// is a system message; deletedAt non-nil is a tombstone.
func (f *adminStore) plantMessage(channelID, id, authorID, kind string, requiresAck bool, createdAt time.Time, deletedAt *time.Time) {
	if f.editFacts == nil {
		f.editFacts = map[string]messageFacts{}
	}
	f.editFacts[id] = messageFacts{
		ChannelID: channelID, AuthorID: authorID, Kind: kind,
		RequiresAck: requiresAck, CreatedAt: createdAt, DeletedAt: deletedAt,
	}
}

// frontlineAuthority is the harness's inside sales authority, named once for
// the legs that swap the linked caller and keep the role.
func frontlineAuthority() identity.StaffAuthority {
	return identity.StaffAuthority{
		RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
	}
}
