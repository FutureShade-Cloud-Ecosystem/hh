package attachments

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

type fakeAttachmentStore struct {
	attachments map[string]Attachment
	links       map[string][]Link
}

func newFakeStore() *fakeAttachmentStore {
	return &fakeAttachmentStore{
		attachments: make(map[string]Attachment),
		links:       make(map[string][]Link),
	}
}

func (f *fakeAttachmentStore) CreateAttachment(_ context.Context, orgID, createdBy string, in AttachmentCreateInput) (Attachment, error) {
	id := "00000000-0000-0000-0000-000000000009"
	att := Attachment{
		ID:             id,
		OrgID:          orgID,
		Kind:           in.Kind,
		AppwriteFileID: in.AppwriteFileID,
		Bucket:         in.Bucket,
		Mime:           in.Mime,
		SizeBytes:      in.SizeBytes,
		Checksum:       in.Checksum,
		CreatedBy:      createdBy,
		CreatedAt:      time.Now(),
		Links:          []Link{},
	}
	f.attachments[orgID+"|"+id] = att
	return att, nil
}

func (f *fakeAttachmentStore) GetAttachment(_ context.Context, orgID, id string) (Attachment, error) {
	att, ok := f.attachments[orgID+"|"+id]
	if !ok {
		return Attachment{}, ErrNotFound
	}
	return att, nil
}

func (f *fakeAttachmentStore) GetLinks(_ context.Context, attachmentID string) ([]Link, error) {
	return f.links[attachmentID], nil
}

func (f *fakeAttachmentStore) CreateLink(_ context.Context, link Link) (Link, error) {
	f.links[link.AttachmentID] = append(f.links[link.AttachmentID], link)
	return link, nil
}

type fakeRoomChecker struct {
	exists  map[string]bool
	members map[string]map[string]bool
}

func (c *fakeRoomChecker) IsRoomMember(_ context.Context, roomID, userID string) (bool, error) {
	if c.members == nil || c.members[roomID] == nil {
		return false, nil
	}
	return c.members[roomID][userID], nil
}

func (c *fakeRoomChecker) RoomExists(_ context.Context, roomID string) (bool, error) {
	if c.exists == nil {
		return false, nil
	}
	return c.exists[roomID], nil
}

type fakeRoomMirror struct {
	mirrored []Attachment
}

func (m *fakeRoomMirror) MirrorAttachment(_ context.Context, _ string, att Attachment, _ string) error {
	m.mirrored = append(m.mirrored, att)
	return nil
}

func testStaffCaller(orgID, userID string) guards.StaffCaller {
	return guards.StaffCaller{
		StaffScope: authz.StaffScope{
			OrgID:          orgID,
			PlatformUserID: userID,
		},
	}
}

func TestHandlerCreateAttachment(t *testing.T) {
	store := newFakeStore()
	h := NewHandler(store, nil)
	caller := testStaffCaller("org-north", "user-1")

	// Missing appwrite_file_id
	badBody, _ := json.Marshal(map[string]any{"kind": "photo", "bucket": "b1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/attachments", bytes.NewReader(badBody))
	rec := httptest.NewRecorder()
	h.createAttachment(rec, req, caller)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing appwrite_file_id, got %d", rec.Code)
	}

	// Invalid kind
	badKind, _ := json.Marshal(map[string]any{"kind": "video", "appwrite_file_id": "f1", "bucket": "b1"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/attachments", bytes.NewReader(badKind))
	rec = httptest.NewRecorder()
	h.createAttachment(rec, req, caller)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid kind, got %d", rec.Code)
	}

	// Valid creation
	goodBody, _ := json.Marshal(map[string]any{"kind": "photo", "appwrite_file_id": "f1", "bucket": "b1"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/attachments", bytes.NewReader(goodBody))
	rec = httptest.NewRecorder()
	h.createAttachment(rec, req, caller)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201 for valid creation, got %d", rec.Code)
	}
}

func TestHandlerLinkAndGetRuling1Fence(t *testing.T) {
	store := newFakeStore()
	checker := &fakeRoomChecker{
		exists: map[string]bool{
			"00000000-0000-4000-8000-000000000100": true,
			"00000000-0000-4000-8000-000000000200": true,
		},
		members: map[string]map[string]bool{
			"00000000-0000-4000-8000-000000000100": {"user-member": true},
			"00000000-0000-4000-8000-000000000200": {"user-other": true},
		},
	}
	mirror := &fakeRoomMirror{}
	h := NewHandler(store, nil).WithRoomChecker(checker).WithRoomMirror(mirror)

	attID := "00000000-0000-4000-8000-000000000011"
	roomID := "00000000-0000-4000-8000-000000000100"
	store.attachments["org-north|"+attID] = Attachment{
		ID:             attID,
		OrgID:          "org-north",
		Kind:           "photo",
		AppwriteFileID: "f11",
		Bucket:         "b1",
	}

	callerMember := testStaffCaller("org-north", "user-member")
	callerNonMember := testStaffCaller("org-north", "user-stranger")

	// Attempt link as non-member -> 403
	linkBody, _ := json.Marshal(map[string]any{
		"owner_kind": "channel",
		"owner_id":   roomID,
		"role":       "photo",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/attachments/"+attID+"/links", bytes.NewReader(linkBody))
	req.SetPathValue("id", attID)
	rec := httptest.NewRecorder()
	h.createAttachmentLink(rec, req, callerNonMember)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 linking channel as non-member, got %d", rec.Code)
	}

	// Link as member -> 201
	req = httptest.NewRequest(http.MethodPost, "/api/v1/attachments/"+attID+"/links", bytes.NewReader(linkBody))
	req.SetPathValue("id", attID)
	rec = httptest.NewRecorder()
	h.createAttachmentLink(rec, req, callerMember)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201 linking channel as member, got %d", rec.Code)
	}
	if len(mirror.mirrored) != 1 {
		t.Fatalf("expected room mirror to be called once, got %d", len(mirror.mirrored))
	}

	// Read attachment: member of room -> 200
	req = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+attID, nil)
	req.SetPathValue("id", attID)
	rec = httptest.NewRecorder()
	h.getAttachment(rec, req, callerMember)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 reading attachment as room member, got %d", rec.Code)
	}

	// Read attachment: non-member of room -> 403 (Ruling 1 read fence)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+attID, nil)
	req.SetPathValue("id", attID)
	rec = httptest.NewRecorder()
	h.getAttachment(rec, req, callerNonMember)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 reading attachment as non-member (Ruling 1 read fence), got %d", rec.Code)
	}
}
