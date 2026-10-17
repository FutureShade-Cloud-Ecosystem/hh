//go:build integration

package messaging

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_RoomAttachments_FilesAndPhotos(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	orgID := testkit.OrgNorthID
	callerAlex := Caller{UserID: uAlexMorgan, Scope: "GLOBAL", OrgID: orgID}
	callerSam := Caller{UserID: uSamRivera, Scope: "BRANCH", OrgID: orgID, Branches: map[string]bool{branchTrenton: true}}

	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync alex: %v", err)
	}
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}

	// 1. Channel test with chYardTrenton: Sam and Alex are members. chYardKingston: Sam is NOT a member.
	// Sam accessing Kingston attachments -> 403 Forbidden
	svc := NewService(repo)
	_, err := svc.GetChannelAttachments(ctx, callerSam, chYardKingston)
	if err == nil {
		t.Fatalf("expected error accessing Kingston attachments as non-member Sam")
	}

	// 2. Create a test GROUP room (e.g. topic/team channel with explicit members)
	groupChannel, _, err := repo.CreateChannel(ctx, CreateChannelInput{
		Kind:  KindTopic,
		Name:  "Integration Test Group Room",
		Topic: "Files and photos verification",
		Slug:  "int-group-files-photos",
	}, uAlexMorgan)
	if err != nil {
		t.Fatalf("create group channel: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1::uuid`, groupChannel.ID)
	})

	// Check member count for group channel: Alex is the only member (1)
	count, err := repo.CountChannelMembers(ctx, groupChannel.ID)
	if err != nil {
		t.Fatalf("count members: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected member count 1 for fresh room, got %d", count)
	}

	// Sam is NOT a member of this group room -> 403
	_, err = svc.GetChannelAttachments(ctx, callerSam, groupChannel.ID)
	if err == nil {
		t.Fatalf("expected 403 / ErrNotMember for Sam on group channel")
	}

	// 3. Post a photo and a document attachment to the group channel as Alex
	photoAttID := "00000000-0000-4000-8000-000000000081"
	docAttID := "00000000-0000-4000-8000-000000000082"

	checker := &fakeAttachmentChecker{
		metas: map[string]AttachmentMeta{
			photoAttID: {ID: photoAttID, AppwriteFileID: "appwrite-photo-123", Bucket: "storage-photos", Kind: "photo", Mime: "image/jpeg", SizeBytes: 2048},
			docAttID:   {ID: docAttID, AppwriteFileID: "appwrite-doc-456", Bucket: "storage-files", Kind: "document", Mime: "application/pdf", SizeBytes: 10240},
		},
	}
	svcWithChecker := NewService(repo).WithAttachmentChecker(checker)

	// Non-member Sam attempting POST /api/v1/messaging/channels/{id}/attachments -> 403
	_, err = svcWithChecker.CreateChannelAttachment(ctx, callerSam, groupChannel.ID, ChannelAttachmentCreateInput{
		AttachmentID: photoAttID,
		Filename:     "site.jpg",
		Kind:         "photo",
	})
	if err == nil {
		t.Fatalf("expected non-member create attachment to be refused")
	}

	// Member Alex posting photo
	photoRes, err := svcWithChecker.CreateChannelAttachment(ctx, callerAlex, groupChannel.ID, ChannelAttachmentCreateInput{
		AttachmentID: photoAttID,
		Filename:     "site.jpg",
		Kind:         "photo",
	})
	if err != nil {
		t.Fatalf("create photo attachment: %v", err)
	}
	if photoRes.AppwriteFileID != "appwrite-photo-123" || photoRes.Bucket != "storage-photos" {
		t.Fatalf("expected resolved storage fields on photo, got %+v", photoRes)
	}

	// Member Alex posting document
	docRes, err := svcWithChecker.CreateChannelAttachment(ctx, callerAlex, groupChannel.ID, ChannelAttachmentCreateInput{
		AttachmentID: docAttID,
		Filename:     "specs.pdf",
		Kind:         "document",
	})
	if err != nil {
		t.Fatalf("create doc attachment: %v", err)
	}
	if docRes.AppwriteFileID != "appwrite-doc-456" || docRes.Bucket != "storage-files" {
		t.Fatalf("expected resolved storage fields on doc, got %+v", docRes)
	}

	// 4. Query attachments on the group room: verify photos, files, and member_count
	resp, err := svcWithChecker.GetChannelAttachments(ctx, callerAlex, groupChannel.ID)
	if err != nil {
		t.Fatalf("get channel attachments on group room: %v", err)
	}
	if resp.MemberCount != 1 {
		t.Fatalf("expected member_count 1, got %d", resp.MemberCount)
	}
	if len(resp.Photos) != 1 {
		t.Fatalf("expected 1 photo, got %d", len(resp.Photos))
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.Files))
	}
	if resp.Photos[0].Filename != "site.jpg" || resp.Photos[0].AppwriteFileID != "appwrite-photo-123" {
		t.Fatalf("unexpected photo fields: %+v", resp.Photos[0])
	}
	if resp.Files[0].Filename != "specs.pdf" || resp.Files[0].AppwriteFileID != "appwrite-doc-456" {
		t.Fatalf("unexpected file fields: %+v", resp.Files[0])
	}

	// Also verify on standard channel chOffice
	chOfficeResp, err := svcWithChecker.GetChannelAttachments(ctx, callerAlex, chOffice)
	if err != nil {
		t.Fatalf("get channel attachments on office: %v", err)
	}
	if chOfficeResp.MemberCount < 1 {
		t.Fatalf("expected member_count >= 1 on office, got %d", chOfficeResp.MemberCount)
	}

	// 5. Test message with attachment returned in GetMessages and GetThread
	msg, err := repo.InsertMessage(ctx, groupChannel.ID, uAlexMorgan, "Message with attachment", "")
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	msgAttID := "00000000-0000-4000-8000-000000000083"
	checker.metas[msgAttID] = AttachmentMeta{
		ID:             msgAttID,
		AppwriteFileID: "appwrite-msg-att-789",
		Bucket:         "storage-msg",
		Kind:           "document",
		Mime:           "text/plain",
		SizeBytes:      512,
	}
	_, err = svcWithChecker.CreateChannelAttachment(ctx, callerAlex, groupChannel.ID, ChannelAttachmentCreateInput{
		AttachmentID: msgAttID,
		MessageID:    &msg.ID,
		Filename:     "note.txt",
		Kind:         "document",
	})
	if err != nil {
		t.Fatalf("create message attachment: %v", err)
	}

	// GetMessages should include msg with attachments slice populated
	page, err := svcWithChecker.GetMessages(ctx, callerAlex, groupChannel.ID, "", 10)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	var foundMsg *Message
	for i := range page.Messages {
		if page.Messages[i].ID == msg.ID {
			foundMsg = &page.Messages[i]
			break
		}
	}
	if foundMsg == nil {
		t.Fatalf("inserted message not found in GetMessages")
	}
	if len(foundMsg.Attachments) != 1 {
		t.Fatalf("expected 1 attachment on message in GetMessages, got %d", len(foundMsg.Attachments))
	}
	if foundMsg.Attachments[0].Filename != "note.txt" || foundMsg.Attachments[0].AppwriteFileID != "appwrite-msg-att-789" {
		t.Fatalf("unexpected attachment fields on message: %+v", foundMsg.Attachments[0])
	}

	// 6. Test HTTP handler endpoints
	h := NewHandler(svcWithChecker, nil)
	alexCaller := guards.StaffCaller{
		StaffScope: authz.StaffScope{OrgID: orgID, PlatformUserID: uAlexMorgan},
	}
	samCaller := guards.StaffCaller{
		StaffScope: authz.StaffScope{OrgID: orgID, PlatformUserID: uSamRivera},
	}
	alexCtx := identity.ContextWithStaff(ctx, identity.StaffContext{UserID: uAlexMorgan, Status: "ACTIVE"})
	alexCtx = authz.WithStaffScope(alexCtx, alexCaller.StaffScope)
	samCtx := identity.ContextWithStaff(ctx, identity.StaffContext{UserID: uSamRivera, Status: "ACTIVE"})
	samCtx = authz.WithStaffScope(samCtx, samCaller.StaffScope)

	// GET attachments as Sam on groupChannel -> 403
	req := httptest.NewRequest(http.MethodGet, "/api/v1/messaging/channels/"+groupChannel.ID+"/attachments", nil)
	req.SetPathValue("id", groupChannel.ID)
	req = req.WithContext(samCtx)
	rec := httptest.NewRecorder()
	h.listChannelAttachments(rec, req, samCaller)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for non-member Sam on group attachments, got %d", rec.Code)
	}

	// GET attachments as Alex on groupChannel -> 200
	req = httptest.NewRequest(http.MethodGet, "/api/v1/messaging/channels/"+groupChannel.ID+"/attachments", nil)
	req.SetPathValue("id", groupChannel.ID)
	req = req.WithContext(alexCtx)
	rec = httptest.NewRecorder()
	h.listChannelAttachments(rec, req, alexCaller)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for member Alex on group attachments, got %d body=%s", rec.Code, rec.Body.String())
	}
	var handlerResp ChannelAttachmentsResponse
	if err := json.NewDecoder(rec.Body).Decode(&handlerResp); err != nil {
		t.Fatalf("decode handler response: %v", err)
	}
	if handlerResp.MemberCount != 1 {
		t.Fatalf("expected handler member_count 1, got %d", handlerResp.MemberCount)
	}
	if len(handlerResp.Photos) != 1 || len(handlerResp.Files) != 2 {
		t.Fatalf("expected 1 photo and 2 files (direct + message), got %d photos and %d files", len(handlerResp.Photos), len(handlerResp.Files))
	}
}
