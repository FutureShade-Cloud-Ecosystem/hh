package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// fakePlane spins an httptest server that mimics the three Plane endpoints the
// service touches: GET/POST modules, POST issues, POST module-issues.
func fakePlane(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var issues []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces/ws/projects/proj/modules/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]string{{"id": "mod-1", "name": "Feedback"}}})
	})
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/issues/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("X-API-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if body["project_id"] != "proj" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"non_field_errors":["Project ID is required"]}`))
			return
		}
		issues = append(issues, body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "issue-1"})
	})
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/modules/mod-1/module-issues/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &issues
}

func testService(base string) *Service {
	return New(base+"/api/v1", "ws", "proj", "test-key")
}

func TestFileCreatesPlaneIssue(t *testing.T) {
	srv, issues := fakePlane(t)
	s := testService(srv.URL)
	id, err := s.File(context.Background(), Submission{
		Kind: KindBug, Message: "POS till drawer maths off by a cent\nsteps: ...",
		Screen: "pos", ActorID: "u-1", ActorRole: "GM",
	})
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if id != "issue-1" {
		t.Fatalf("id = %q", id)
	}
	if len(*issues) != 1 {
		t.Fatalf("issues filed = %d", len(*issues))
	}
	got := (*issues)[0]
	if got["name"] != "[bug] POS till drawer maths off by a cent" {
		t.Errorf("title = %q", got["name"])
	}
	if got["priority"] != "high" {
		t.Errorf("priority = %q (bug must be high)", got["priority"])
	}
	desc, _ := got["description_html"].(string)
	for _, want := range []string{"screen: pos", "actor: u-1 (GM)", "via ERP feedback widget"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q", want)
		}
	}
}

func TestFileValidation(t *testing.T) {
	srv, _ := fakePlane(t)
	s := testService(srv.URL)
	if _, err := s.File(context.Background(), Submission{Kind: KindBug, ActorID: "u"}); err == nil {
		t.Error("empty message must fail")
	}
	if _, err := s.File(context.Background(), Submission{Kind: "nonsense", Message: "x", ActorID: "u"}); err == nil {
		t.Error("unknown kind must fail")
	}
}

func TestFileRateLimit(t *testing.T) {
	srv, _ := fakePlane(t)
	s := testService(srv.URL)
	now := time.Now()
	s.now = func() time.Time { return now }
	for i := 0; i < rateLimit; i++ {
		if _, err := s.File(context.Background(), Submission{Kind: KindFlag, Message: "m", ActorID: "u-1"}); err != nil {
			t.Fatalf("submission %d: %v", i, err)
		}
	}
	if _, err := s.File(context.Background(), Submission{Kind: KindFlag, Message: "m", ActorID: "u-1"}); err != ErrRateLimited {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	// another user is unaffected; the window slides
	if _, err := s.File(context.Background(), Submission{Kind: KindFlag, Message: "m", ActorID: "u-2"}); err != nil {
		t.Fatalf("other user blocked: %v", err)
	}
	now = now.Add(rateWindow + time.Second)
	if _, err := s.File(context.Background(), Submission{Kind: KindFlag, Message: "m", ActorID: "u-1"}); err != nil {
		t.Fatalf("window did not slide: %v", err)
	}
}

func TestFilePlaneOutage(t *testing.T) {
	s := testService("http://127.0.0.1:1") // nothing listens
	s.http.Timeout = 500 * time.Millisecond
	_, err := s.File(context.Background(), Submission{Kind: KindBug, Message: "m", ActorID: "u"})
	var pe PlaneError
	if err == nil || !errors.As(err, &pe) {
		t.Fatalf("want PlaneError, got %v", err)
	}
}

// ---- image attachment tests (presign → upload → confirm) ----

// pngBytes returns n bytes beginning with the PNG signature so
// http.DetectContentType sniffs them as image/png regardless of the client's
// declared MIME.
func pngBytes(n int) []byte {
	b := make([]byte, n)
	copy(b, []byte("\x89PNG\r\n\x1a\n"))
	return b
}

// attachRecord captures what the fake Plane + presigned gateway observed.
type attachRecord struct {
	presign, upload, confirm int
	uploadHadAPIKey          bool
	uploadedBytes            []byte
	declaredSize             int
	presignFail, uploadFail  bool
}

// fakePlaneWithAttach mimics the module/issue endpoints AND the 3-step
// attachment flow: presign → presigned-gateway upload → confirm.
func fakePlaneWithAttach(t *testing.T, rec *attachRecord) *Service {
	t.Helper()
	var baseURL string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces/ws/projects/proj/modules/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]string{{"id": "mod-1", "name": "Feedback"}}})
	})
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/issues/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "issue-1"})
	})
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/modules/mod-1/module-issues/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	// 1) presign
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/issues/issue-1/issue-attachments/", func(w http.ResponseWriter, r *http.Request) {
		rec.presign++
		if rec.presignFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if sz, ok := body["size"].(float64); ok {
			rec.declaredSize = int(sz)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"asset_id": "asset-1",
			"upload_data": map[string]any{
				"url": baseURL + "/uploads",
				"fields": map[string]string{
					"key":             "abc/shot.png",
					"policy":          "base64policy",
					"x-amz-signature": "sig",
					"Content-Type":    "image/png",
				},
			},
			"attachment": map[string]any{"is_uploaded": false},
		})
	})
	// 2) presigned gateway upload (foreign host, MUST NOT carry X-API-Key)
	mux.HandleFunc("POST /uploads", func(w http.ResponseWriter, r *http.Request) {
		rec.upload++
		if r.Header.Get("X-API-Key") != "" {
			rec.uploadHadAPIKey = true
		}
		if err := r.ParseMultipartForm(10 << 20); err == nil {
			if f, _, err := r.FormFile("file"); err == nil {
				defer f.Close()
				rec.uploadedBytes, _ = io.ReadAll(f)
			}
		}
		if rec.uploadFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// 3) confirm
	mux.HandleFunc("PATCH /api/v1/workspaces/ws/projects/proj/issues/issue-1/issue-attachments/asset-1/", func(w http.ResponseWriter, _ *http.Request) {
		rec.confirm++
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	baseURL = srv.URL
	return New(srv.URL+"/api/v1", "ws", "proj", "test-key")
}

func TestAttachImageRoundTrip(t *testing.T) {
	rec := &attachRecord{}
	s := fakePlaneWithAttach(t, rec)
	img := pngBytes(1000)
	id, err := s.File(context.Background(), Submission{
		Kind: KindBug, Message: "with a shot", ActorID: "u-1", ActorRole: "GM",
		Image: img, ImageName: "shot.png", ImageType: "image/png",
	})
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if id != "issue-1" {
		t.Fatalf("id = %q", id)
	}
	if rec.presign != 1 || rec.upload != 1 || rec.confirm != 1 {
		t.Fatalf("calls presign=%d upload=%d confirm=%d (want 1/1/1)", rec.presign, rec.upload, rec.confirm)
	}
	if rec.uploadHadAPIKey {
		t.Error("presigned upload must NOT carry X-API-Key")
	}
	if rec.declaredSize != len(img) {
		t.Errorf("declared size = %d, want %d (policy enforces content-length-range==size)", rec.declaredSize, len(img))
	}
	if !bytes.Equal(rec.uploadedBytes, img) {
		t.Errorf("uploaded %d bytes, want %d identical", len(rec.uploadedBytes), len(img))
	}
}

func TestAttachImageBestEffort(t *testing.T) {
	// Upload leg fails → the issue MUST still be filed (attach is best-effort).
	rec := &attachRecord{uploadFail: true}
	s := fakePlaneWithAttach(t, rec)
	id, err := s.File(context.Background(), Submission{
		Kind: KindBug, Message: "shot fails to upload", ActorID: "u-1",
		Image: pngBytes(600), ImageName: "shot.png",
	})
	if err != nil {
		t.Fatalf("attach failure must not fail File: %v", err)
	}
	if id != "issue-1" {
		t.Fatalf("id = %q, filed issue lost on attach error", id)
	}
	if rec.confirm != 0 {
		t.Errorf("confirm called %d times despite failed upload", rec.confirm)
	}
}

func TestAttachImageServerValidation(t *testing.T) {
	rec := &attachRecord{}
	s := fakePlaneWithAttach(t, rec)

	// Client CLAIMS image/png but the bytes are text, server sniffs and rejects,
	// never reaching presign.
	err := s.attachImage(context.Background(), "issue-1", Submission{
		Image:     []byte("this is definitely not an image, just plain ascii text"),
		ImageType: "image/png", ImageName: "lie.png",
	})
	if err == nil {
		t.Error("non-image bytes must be rejected by server sniff")
	}

	// Oversized (> 5 MB) is rejected before any presign.
	err = s.attachImage(context.Background(), "issue-1", Submission{
		Image: pngBytes(maxImageBytes + 1), ImageName: "huge.png",
	})
	if err == nil {
		t.Error("oversized image must be rejected")
	}
	if rec.presign != 0 {
		t.Errorf("presign called %d times on invalid image (want 0, validate before upstream)", rec.presign)
	}
}

func TestHandlerMultipartWithImage(t *testing.T) {
	rec := &attachRecord{}
	s := fakePlaneWithAttach(t, rec)
	h := NewHandler(s, slog.Default())

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("type", "bug")
	_ = mw.WriteField("message", "screenshot attached")
	_ = mw.WriteField("screen", "pos")
	fw, _ := mw.CreateFormFile("image", "shot.png")
	img := pngBytes(500)
	_, _ = fw.Write(img)
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: "u-1", RoleCode: "GM"}))
	w := httptest.NewRecorder()
	h.create(w, req, guards.StaffCaller{})

	if w.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if rec.upload != 1 {
		t.Fatalf("upload calls=%d (want 1), multipart image did not reach Plane", rec.upload)
	}
	if !bytes.Equal(rec.uploadedBytes, img) {
		t.Errorf("uploaded bytes differ from the posted image")
	}
}

func TestHandlerMultipartTextTooLarge(t *testing.T) {
	// A message far larger than the JSON body budget (maxBodyBytes) must be
	// refused on the multipart path too, not streamed on to Plane.
	rec := &attachRecord{}
	s := fakePlaneWithAttach(t, rec)
	h := NewHandler(s, slog.Default())

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("type", "bug")
	_ = mw.WriteField("message", strings.Repeat("A", maxBodyBytes+1))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: "u-big", RoleCode: "GM"}))
	w := httptest.NewRecorder()
	h.create(w, req, guards.StaffCaller{})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s (want 400 for oversize message)", w.Code, w.Body.String())
	}
	if rec.presign != 0 {
		t.Errorf("oversize text must be rejected before Plane (presign=%d)", rec.presign)
	}
}

func TestHandlerMultipartRateLimitedBeforeParse(t *testing.T) {
	rec := &attachRecord{}
	s := fakePlaneWithAttach(t, rec)
	// Exhaust the caller's budget so the next submission is rate-limited.
	for i := 0; i < rateLimit; i++ {
		if !s.allow("u-flood") {
			t.Fatalf("priming allow() returned false at %d", i)
		}
	}
	h := NewHandler(s, slog.Default())

	// A body that is NOT valid multipart: had the handler parsed BEFORE checking
	// the rate limit it would answer 400 (invalid_multipart). Getting 429 proves
	// the limit is enforced before the (potentially ~5 MB) body is buffered.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback",
		strings.NewReader("not-a-real-multipart-body"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: "u-flood", RoleCode: "GM"}))
	w := httptest.NewRecorder()
	h.create(w, req, guards.StaffCaller{})

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("code=%d body=%s (want 429 before any multipart parse)", w.Code, w.Body.String())
	}
	if rec.presign != 0 || rec.upload != 0 {
		t.Errorf("rate-limited caller must not reach Plane (presign=%d upload=%d)", rec.presign, rec.upload)
	}
}

func TestAttachmentNameUsesSniffedExt(t *testing.T) {
	// Client sends a .png name but bytes sniff as jpeg → the extension must follow
	// the sniffed type, not the client's claim.
	if got := attachmentName("shot.png", ".jpg"); got != "shot.jpg" {
		t.Errorf("attachmentName(shot.png,.jpg) = %q, want shot.jpg", got)
	}
	// Path components are stripped; a base with no extension keeps its name.
	if got := attachmentName("/tmp/evil/x", ".png"); got != "x.png" {
		t.Errorf("attachmentName(/tmp/evil/x,.png) = %q, want x.png", got)
	}
	// Blank / dot-only names fall back to the stable default.
	if got := attachmentName("   ", ".webp"); got != "screenshot.webp" {
		t.Errorf("attachmentName(blank,.webp) = %q, want screenshot.webp", got)
	}
}

func TestHandlerJSONStillWorks(t *testing.T) {
	// Back-compat: a JSON (no-image) submission files with zero attachment calls.
	rec := &attachRecord{}
	s := fakePlaneWithAttach(t, rec)
	h := NewHandler(s, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback",
		strings.NewReader(`{"type":"feedback","message":"no image here","screen":"pos"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: "u-2", RoleCode: "GM"}))
	w := httptest.NewRecorder()
	h.create(w, req, guards.StaffCaller{})

	if w.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if rec.presign != 0 || rec.upload != 0 {
		t.Errorf("no-image path must not touch attachment endpoints (presign=%d upload=%d)", rec.presign, rec.upload)
	}
}
