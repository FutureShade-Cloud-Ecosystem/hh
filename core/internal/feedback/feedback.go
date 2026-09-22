// Package feedback bridges in-app staff feedback to the Dibbits Plane board
// (#16 / IMP-038): a widget in the ERP shell POSTs bug/feedback/flag submissions
// here, and each becomes a Plane issue in the project's "Feedback" module -
// closing client-observation → tracked-work without anyone leaving the app.
//
// The Plane API key lives server-side only (config); the frontend never sees
// Plane. Submissions require an authenticated staff session (RequireStaff at
// the route) and are rate-limited per user so a stuck retry loop cannot flood
// the board.
package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Kind is the submission type; it maps to Plane priority.
type Kind string

const (
	KindBug      Kind = "bug"
	KindFeedback Kind = "feedback"
	KindFlag     Kind = "flag"
)

var priorityOf = map[Kind]string{
	KindBug:      "high",
	KindFeedback: "medium",
	KindFlag:     "medium",
}

// Submission is what the widget sends plus the server-resolved actor.
type Submission struct {
	Kind    Kind   `json:"type"`
	Message string `json:"message"`
	// Client-supplied display context (informational; the trusted actor comes
	// from the staff session, not from these fields).
	Screen    string `json:"screen"`
	URL       string `json:"url"`
	UserAgent string `json:"user_agent"`
	// Server-stamped:
	ActorID   string `json:"-"`
	ActorRole string `json:"-"`
	// Optional image attachment (screenshot). Server-only: these are filled from
	// the multipart "image" part, NEVER from client-controlled JSON, so they carry
	// no json tags. Image is the raw bytes; ImageName/ImageType are advisory (the
	// server re-sniffs the real content type before trusting it).
	Image     []byte `json:"-"`
	ImageName string `json:"-"`
	ImageType string `json:"-"`
}

// moduleName is the Plane module feedback issues are grouped under.
const moduleName = "Feedback"

// maxImageBytes bounds an accepted image attachment (matches the client + the
// multipart body cap in the handler).
const maxImageBytes = 5 << 20

// allowedImageType is the server-enforced allow-list, checked against the SNIFFED
// content type (http.DetectContentType), not the client-declared MIME.
var allowedImageType = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

// Service files submissions to Plane. Safe for concurrent use.
type Service struct {
	base string // <PlaneAPIURL>/workspaces/<slug>/projects/<project-id>
	key  string
	http *http.Client
	now  func() time.Time
	log  *slog.Logger

	mu       sync.Mutex
	moduleID string                 // resolved-once cache
	buckets  map[string][]time.Time // per-user submission timestamps (rate limit)
}

// rateLimit is the max submissions per user per window.
const (
	rateLimit  = 10
	rateWindow = time.Minute
)

// New builds a Service. baseURL is the Plane API root (…/api/v1), slug the
// workspace slug, project the Plane project id.
func New(baseURL, slug, project, key string) *Service {
	return &Service{
		base: strings.TrimRight(baseURL, "/") + "/workspaces/" + slug + "/projects/" + project,
		key:  key,
		// 30s (up from 15s): the presigned attachment upload leg streams up to 5 MB,
		// which can outrun a tighter deadline on a slow uplink.
		http:    &http.Client{Timeout: 30 * time.Second},
		now:     time.Now,
		log:     slog.Default(),
		buckets: map[string][]time.Time{},
	}
}

// ErrRateLimited is returned when a user exceeds rateLimit per rateWindow.
var ErrRateLimited = fmt.Errorf("rate limited: max %d submissions per %s", rateLimit, rateWindow)

// PlaneError wraps any upstream Plane failure so the handler can answer 502
// without leaking the upstream body (which could include URLs/ids).
type PlaneError struct{ Err error }

func (e PlaneError) Error() string { return "plane upstream: " + e.Err.Error() }
func (e PlaneError) Unwrap() error { return e.Err }

// File validates, rate-limits, and creates the Plane issue. Returns the new
// issue's id.
func (s *Service) File(ctx context.Context, sub Submission) (string, error) {
	sub.Message = strings.TrimSpace(sub.Message)
	if sub.Message == "" {
		return "", fmt.Errorf("message is required")
	}
	if _, ok := priorityOf[sub.Kind]; !ok {
		return "", fmt.Errorf("type must be one of bug, feedback, flag")
	}
	if !s.allow(sub.ActorID) {
		return "", ErrRateLimited
	}

	moduleID, err := s.ensureModule(ctx)
	if err != nil {
		return "", PlaneError{err}
	}

	first := sub.Message
	if i := strings.IndexByte(first, '\n'); i > 0 {
		first = first[:i]
	}
	if len(first) > 80 {
		first = first[:80] + "…"
	}
	title := fmt.Sprintf("[%s] %s", sub.Kind, first)

	desc := fmt.Sprintf(
		"<p>%s</p><p><em>screen: %s · actor: %s (%s) · url: %s · ua: %s · submitted: %s · via ERP feedback widget</em></p>",
		html.EscapeString(sub.Message), html.EscapeString(sub.Screen),
		html.EscapeString(sub.ActorID), html.EscapeString(sub.ActorRole),
		html.EscapeString(sub.URL), html.EscapeString(sub.UserAgent),
		s.now().UTC().Format(time.RFC3339))

	issue := map[string]any{
		"name":             title,
		"description_html": desc,
		"priority":         priorityOf[sub.Kind],
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := s.req(ctx, http.MethodPost, "/issues/", issue, &made); err != nil {
		return "", PlaneError{err}
	}
	if err := s.req(ctx, http.MethodPost, "/modules/"+moduleID+"/module-issues/",
		map[string]any{"issues": []string{made.ID}}, nil); err != nil {
		// The issue exists; module attach failing should not lose the submission.
		s.log.Warn("feedback: module attach failed", "issue", made.ID, "err", err)
		return made.ID, nil
	}

	// Best-effort image attachment. The issue is already filed; an attachment
	// failure must NEVER lose the tester's report (mirrors the module-attach
	// fallback above), so we log and still return the issue id.
	if len(sub.Image) > 0 {
		if err := s.attachImage(ctx, made.ID, sub); err != nil {
			s.log.Warn("feedback: image attach failed", "issue", made.ID, "err", err)
		}
	}
	return made.ID, nil
}

// attachImage uploads sub.Image to the Plane issue as a real binary attachment
// via Plane's 3-step presign → upload → confirm flow. Best-effort: callers keep
// the filed issue even when this errors.
//
//  1. PRESIGN  POST …/issues/{id}/issue-attachments/ {name,type,size} → an
//     AWS-SigV4 presigned-POST policy (upload_data.url + upload_data.fields) and
//     an asset_id. The declared size MUST equal len(image): the policy's
//     content-length-range is [1,size] and rejects a mismatch.
//  2. UPLOAD   multipart POST to upload_data.url (an absolute foreign host, the
//     instance S3/MinIO gateway) with every policy field then the bytes LAST as
//     part "file". No X-API-Key (it is a presigned request). Expect 204.
//  3. CONFIRM  PATCH …/issue-attachments/{asset_id}/ {"is_uploaded":true} → 204,
//     which flips the row visible in the issue's Attachments panel.
func (s *Service) attachImage(ctx context.Context, issueID string, sub Submission) error {
	// Server-side re-validation, do NOT trust the client-declared type/size.
	if len(sub.Image) == 0 {
		return fmt.Errorf("empty image")
	}
	if len(sub.Image) > maxImageBytes {
		return fmt.Errorf("image too large: %d bytes (max %d)", len(sub.Image), maxImageBytes)
	}
	sniff := sub.Image
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	ct := http.DetectContentType(sniff)
	ext, ok := allowedImageType[ct]
	if !ok {
		return fmt.Errorf("unsupported image type: %s", ct)
	}
	name := attachmentName(sub.ImageName, ext)

	// 1) presign, POST gets the project_id injection in s.req, which is harmless.
	var presign struct {
		UploadData struct {
			URL    string            `json:"url"`
			Fields map[string]string `json:"fields"`
		} `json:"upload_data"`
		AssetID string `json:"asset_id"`
	}
	if err := s.req(ctx, http.MethodPost, "/issues/"+issueID+"/issue-attachments/",
		map[string]any{"name": name, "type": ct, "size": len(sub.Image)}, &presign); err != nil {
		return fmt.Errorf("presign: %w", err)
	}
	if presign.UploadData.URL == "" || presign.AssetID == "" {
		return fmt.Errorf("presign returned no upload target")
	}

	// 2) upload to the presigned foreign host (no X-API-Key).
	if err := s.uploadPresigned(ctx, presign.UploadData.URL, presign.UploadData.Fields, name, ct, sub.Image); err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	// 3) confirm, PATCH is not POST, so it gets no project_id injection (correct).
	if err := s.req(ctx, http.MethodPatch, "/issues/"+issueID+"/issue-attachments/"+presign.AssetID+"/",
		map[string]any{"is_uploaded": true}, nil); err != nil {
		return fmt.Errorf("confirm: %w", err)
	}
	return nil
}

// uploadPresigned POSTs the image bytes to Plane's presigned S3/MinIO gateway.
// The presigned URL is an absolute foreign host, so this deliberately bypasses
// s.base and sends NO X-API-Key, the policy fields authorize the write. Every
// field entry is written first, then the binary LAST as part "file"; S3-style
// presigned POSTs require the file part to come after the policy fields.
func (s *Service) uploadPresigned(ctx context.Context, url string, fields map[string]string, name, contentType string, data []byte) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return err
		}
	}
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("presigned upload -> %d: %s", resp.StatusCode, snippet)
	}
	return nil
}

// attachmentName derives a safe filename for the attachment. The client name is
// advisory: we take only its base, then force the extension to match the SNIFFED
// content type (ext) rather than trusting the client's, a .png name on JPEG
// bytes must not reach the board mislabeled. Falls back to a stable default.
func attachmentName(provided, ext string) string {
	provided = strings.TrimSpace(filepath.Base(strings.TrimSpace(provided)))
	if provided == "" || provided == "." || provided == string(filepath.Separator) {
		return "screenshot" + ext
	}
	base := strings.TrimSuffix(provided, filepath.Ext(provided))
	if base == "" {
		base = "screenshot"
	}
	return base + ext
}

// atLimit reports whether user has already exhausted their submission budget for
// the current window, WITHOUT recording anything. The multipart handler uses it
// to turn a flooding caller away before buffering a ~5 MB body; File's allow()
// stays the single place that records a submission, so this read-only peek never
// double-counts a permitted request.
func (s *Service) atLimit(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	n := 0
	for _, t := range s.buckets[user] {
		if now.Sub(t) < rateWindow {
			n++
		}
	}
	return n >= rateLimit
}

// allow implements the per-user sliding-window rate limit.
func (s *Service) allow(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	keep := s.buckets[user][:0]
	for _, t := range s.buckets[user] {
		if now.Sub(t) < rateWindow {
			keep = append(keep, t)
		}
	}
	if len(keep) >= rateLimit {
		s.buckets[user] = keep
		return false
	}
	s.buckets[user] = append(keep, now)
	return true
}

// ensureModule resolves (or creates) the "Feedback" module once and caches it.
func (s *Service) ensureModule(ctx context.Context) (string, error) {
	s.mu.Lock()
	cached := s.moduleID
	s.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	var page struct {
		Results []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := s.req(ctx, http.MethodGet, "/modules/", nil, &page); err != nil {
		return "", err
	}
	id := ""
	for _, m := range page.Results {
		if m.Name == moduleName {
			id = m.ID
			break
		}
	}
	if id == "" {
		var made struct {
			ID string `json:"id"`
		}
		if err := s.req(ctx, http.MethodPost, "/modules/",
			map[string]any{"name": moduleName}, &made); err != nil {
			return "", err
		}
		id = made.ID
	}
	s.mu.Lock()
	s.moduleID = id
	s.mu.Unlock()
	return id, nil
}

// req performs one Plane API call. Plane CE quirk: POST bodies must carry the
// project id even though it is already in the URL path.
func (s *Service) req(ctx context.Context, method, path string, body map[string]any, out any) error {
	var rdr io.Reader
	if body != nil {
		if method == http.MethodPost {
			// project id from the base path tail
			parts := strings.Split(strings.TrimRight(s.base, "/"), "/")
			body["project_id"] = parts[len(parts)-1]
		}
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", s.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("%s %s -> %d: %s", method, path, resp.StatusCode, snippet)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
