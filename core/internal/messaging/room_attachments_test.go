package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

type fakeAttachmentChecker struct {
	metas map[string]AttachmentMeta
}

func (c *fakeAttachmentChecker) VerifyRoomAttachment(_ context.Context, _, attachmentID, _ string) (*AttachmentMeta, error) {
	if meta, ok := c.metas[attachmentID]; ok {
		return &meta, nil
	}
	return nil, ErrNotFound
}

func (c *fakeAttachmentChecker) ResolveAttachmentMetas(_ context.Context, _ string, ids []string) (map[string]AttachmentMeta, error) {
	out := make(map[string]AttachmentMeta)
	for _, id := range ids {
		if meta, ok := c.metas[id]; ok {
			out[id] = meta
		}
	}
	return out, nil
}

func TestRoomAttachments_ServiceUnit(t *testing.T) {
	fs := newFakeStore()
	channelID := "c1000000-0000-4000-8000-000000000001"
	fs.setMember(channelID, uAlice)

	attID1 := "a1000000-0000-4000-8000-000000000001"
	attID2 := "a2000000-0000-4000-8000-000000000002"

	checker := &fakeAttachmentChecker{
		metas: map[string]AttachmentMeta{
			attID1: {ID: attID1, AppwriteFileID: "file-photo-1", Bucket: "b-photo", Kind: "photo"},
			attID2: {ID: attID2, AppwriteFileID: "file-doc-2", Bucket: "b-doc", Kind: "document"},
		},
	}

	svc := svcWith(fs).WithAttachmentChecker(checker)

	callerAlice := Caller{UserID: uAlice, OrgID: "org-north"}
	callerBob := Caller{UserID: uBob, OrgID: "org-north"}

	// 1. Non-member GetChannelAttachments returns ErrNotMember
	_, err := svc.GetChannelAttachments(context.Background(), callerBob, channelID)
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember for Bob, got %v", err)
	}

	// 2. CreateChannelAttachment non-member returns ErrNotMember
	_, err = svc.CreateChannelAttachment(context.Background(), callerBob, channelID, ChannelAttachmentCreateInput{
		AttachmentID: attID1,
		Filename:     "photo1.png",
	})
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember on create for Bob, got %v", err)
	}

	// 3. CreateChannelAttachment validation failures
	_, err = svc.CreateChannelAttachment(context.Background(), callerAlice, channelID, ChannelAttachmentCreateInput{
		AttachmentID: attID1,
		Filename:     "",
	})
	if !errors.Is(err, ErrBadAttachment) {
		t.Fatalf("expected ErrBadAttachment for empty filename, got %v", err)
	}

	_, err = svc.CreateChannelAttachment(context.Background(), callerAlice, channelID, ChannelAttachmentCreateInput{
		AttachmentID: "not-a-uuid",
		Filename:     "test.png",
	})
	if !errors.Is(err, ErrBadAttachment) {
		t.Fatalf("expected ErrBadAttachment for bad UUID, got %v", err)
	}

	// 4. Create photo attachment as Alice
	photoAtt, err := svc.CreateChannelAttachment(context.Background(), callerAlice, channelID, ChannelAttachmentCreateInput{
		AttachmentID: attID1,
		Filename:     "photo1.png",
		Kind:         "photo",
	})
	if err != nil {
		t.Fatalf("create photo attachment failed: %v", err)
	}
	if photoAtt.AppwriteFileID != "file-photo-1" || photoAtt.Bucket != "b-photo" {
		t.Fatalf("expected resolved storage fields on photo, got %+v", photoAtt)
	}

	// 5. Create document attachment as Alice
	docAtt, err := svc.CreateChannelAttachment(context.Background(), callerAlice, channelID, ChannelAttachmentCreateInput{
		AttachmentID: attID2,
		Filename:     "manual.pdf",
		Kind:         "document",
	})
	if err != nil {
		t.Fatalf("create doc attachment failed: %v", err)
	}
	if docAtt.AppwriteFileID != "file-doc-2" || docAtt.Bucket != "b-doc" {
		t.Fatalf("expected resolved storage fields on doc, got %+v", docAtt)
	}

	// 6. GetChannelAttachments returns separated photos and files
	resp, err := svc.GetChannelAttachments(context.Background(), callerAlice, channelID)
	if err != nil {
		t.Fatalf("GetChannelAttachments failed: %v", err)
	}
	if len(resp.Photos) != 1 {
		t.Fatalf("expected 1 photo, got %d", len(resp.Photos))
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.Files))
	}
	if resp.Photos[0].AppwriteFileID != "file-photo-1" {
		t.Fatalf("expected photo appwrite_file_id file-photo-1, got %s", resp.Photos[0].AppwriteFileID)
	}
	if resp.Files[0].AppwriteFileID != "file-doc-2" {
		t.Fatalf("expected file appwrite_file_id file-doc-2, got %s", resp.Files[0].AppwriteFileID)
	}
	if resp.MemberCount != 1 {
		t.Fatalf("expected member count 1, got %d", resp.MemberCount)
	}
}

func TestRoomAttachments_HandlerUnit(t *testing.T) {
	fs := newFakeStore()
	channelID := "c1000000-0000-4000-8000-000000000001"
	fs.setMember(channelID, uAlice)

	attID := "a1000000-0000-4000-8000-000000000001"
	checker := &fakeAttachmentChecker{
		metas: map[string]AttachmentMeta{
			attID: {ID: attID, AppwriteFileID: "file-photo-1", Bucket: "b-photo", Kind: "photo"},
		},
	}
	svc := svcWith(fs).WithAttachmentChecker(checker)
	h := NewHandler(svc, nil)

	aliceCaller := guards.StaffCaller{
		StaffScope: authz.StaffScope{OrgID: "org-north", PlatformUserID: uAlice},
	}
	bobCaller := guards.StaffCaller{
		StaffScope: authz.StaffScope{OrgID: "org-north", PlatformUserID: uBob},
	}

	aliceCtx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: uAlice, Status: "ACTIVE",
	})
	aliceCtx = authz.WithStaffScope(aliceCtx, aliceCaller.StaffScope)

	bobCtx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: uBob, Status: "ACTIVE",
	})
	bobCtx = authz.WithStaffScope(bobCtx, bobCaller.StaffScope)

	// POST as Bob (non-member) -> 403
	postBody, _ := json.Marshal(map[string]any{
		"attachment_id": attID,
		"filename":      "photo1.png",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messaging/channels/"+channelID+"/attachments", bytes.NewReader(postBody))
	req.SetPathValue("id", channelID)
	req = req.WithContext(bobCtx)
	rec := httptest.NewRecorder()
	h.createChannelAttachment(rec, req, bobCaller)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for non-member create, got %d", rec.Code)
	}

	// POST as Alice (member) -> 201
	req = httptest.NewRequest(http.MethodPost, "/api/v1/messaging/channels/"+channelID+"/attachments", bytes.NewReader(postBody))
	req.SetPathValue("id", channelID)
	req = req.WithContext(aliceCtx)
	rec = httptest.NewRecorder()
	h.createChannelAttachment(rec, req, aliceCaller)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201 for member create, got %d body=%s", rec.Code, rec.Body.String())
	}

	// GET as Bob (non-member) -> 403
	req = httptest.NewRequest(http.MethodGet, "/api/v1/messaging/channels/"+channelID+"/attachments", nil)
	req.SetPathValue("id", channelID)
	req = req.WithContext(bobCtx)
	rec = httptest.NewRecorder()
	h.listChannelAttachments(rec, req, bobCaller)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for non-member list, got %d", rec.Code)
	}

	// GET as Alice (member) -> 200
	req = httptest.NewRequest(http.MethodGet, "/api/v1/messaging/channels/"+channelID+"/attachments", nil)
	req.SetPathValue("id", channelID)
	req = req.WithContext(aliceCtx)
	rec = httptest.NewRecorder()
	h.listChannelAttachments(rec, req, aliceCaller)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for member list, got %d body=%s", rec.Code, rec.Body.String())
	}
}
