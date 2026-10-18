//go:build integration

package attachments

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestIntegration_Attachment_CreateLinkAndRuling1Fence(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	orgID := "a5000000-0000-4000-8000-0000000000f1"
	otherOrgID := "a5000000-0000-4000-8000-0000000000f2"
	if _, err := pool.Exec(ctx,
		`INSERT INTO org (id, slug, name) VALUES
			($1::uuid, 'attachments-ruling1-fence-leg', 'attachments ruling1 fence leg'),
			($2::uuid, 'attachments-ruling1-other-leg', 'attachments ruling1 other leg')
		 ON CONFLICT (id) DO NOTHING`,
		orgID, otherOrgID); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM attachment_link l USING attachment a
			WHERE l.attachment_id = a.id AND a.org_id IN ($1::uuid, $2::uuid)`, orgID, otherOrgID)
		_, _ = pool.Exec(c, `DELETE FROM attachment WHERE org_id IN ($1::uuid, $2::uuid)`, orgID, otherOrgID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id IN ($1::uuid, $2::uuid)`, orgID, otherOrgID)
	})

	roomID := "a5000000-0000-4000-9000-000000000001"
	memberUserID := "d7000000-0000-4000-9000-000000000002"
	nonMemberUserID := "d7000000-0000-4000-9000-000000000099"

	checker := &fakeRoomChecker{
		exists: map[string]bool{roomID: true},
		members: map[string]map[string]bool{
			roomID: {memberUserID: true},
		},
	}
	mirror := &fakeRoomMirror{}
	h := NewHandler(repo, nil).WithRoomChecker(checker).WithRoomMirror(mirror)

	callerMember := guards.StaffCaller{
		StaffScope: authz.StaffScope{OrgID: orgID, PlatformUserID: memberUserID},
	}
	callerNonMember := guards.StaffCaller{
		StaffScope: authz.StaffScope{OrgID: orgID, PlatformUserID: nonMemberUserID},
	}

	// 1. Register attachment via POST /api/v1/attachments (201)
	createBody, _ := json.Marshal(map[string]any{
		"kind":             "photo",
		"appwrite_file_id": "appwrite-file-int-1",
		"bucket":           "channel-photos",
		"mime":             "image/png",
		"size_bytes":       1024,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/attachments", bytes.NewReader(createBody))
	rec := httptest.NewRecorder()
	h.createAttachment(rec, req, callerMember)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201 on createAttachment, got %d body=%s", rec.Code, rec.Body.String())
	}

	var created Attachment
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created attachment: %v", err)
	}
	if created.ID == "" || created.AppwriteFileID != "appwrite-file-int-1" {
		t.Fatalf("unexpected created attachment: %+v", created)
	}
	attID := created.ID

	// 2. Link to channel via POST /api/v1/attachments/{id}/links with owner_kind: "channel"
	linkPayload, _ := json.Marshal(map[string]any{
		"owner_kind": "channel",
		"owner_id":   roomID,
		"role":       "photo",
	})

	// As non-member -> 403 Forbidden
	req = httptest.NewRequest(http.MethodPost, "/api/v1/attachments/"+attID+"/links", bytes.NewReader(linkPayload))
	req.SetPathValue("id", attID)
	rec = httptest.NewRecorder()
	h.createAttachmentLink(rec, req, callerNonMember)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 linking channel as non-member, got %d", rec.Code)
	}

	// As member -> 201 Created
	req = httptest.NewRequest(http.MethodPost, "/api/v1/attachments/"+attID+"/links", bytes.NewReader(linkPayload))
	req.SetPathValue("id", attID)
	rec = httptest.NewRecorder()
	h.createAttachmentLink(rec, req, callerMember)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201 linking channel as member, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(mirror.mirrored) != 1 {
		t.Fatalf("expected room mirror to be invoked, got %d calls", len(mirror.mirrored))
	}

	// 3. GET /api/v1/attachments/{id}
	// As member of room -> 200 OK
	req = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+attID, nil)
	req.SetPathValue("id", attID)
	rec = httptest.NewRecorder()
	h.getAttachment(rec, req, callerMember)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 getting attachment as room member, got %d body=%s", rec.Code, rec.Body.String())
	}

	// As non-member of room -> 403 Forbidden (Ruling 1 read fence)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+attID, nil)
	req.SetPathValue("id", attID)
	rec = httptest.NewRecorder()
	h.getAttachment(rec, req, callerNonMember)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 getting attachment as non-member (Ruling 1 fence), got %d body=%s", rec.Code, rec.Body.String())
	}
}
