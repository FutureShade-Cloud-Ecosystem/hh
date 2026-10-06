// Package feedback bridges in-app staff feedback to the Dibbits Plane board
// (#16 / IMP-038): a widget in the ERP shell POSTs bug/feedback/flag submissions
// here, and each becomes a Plane issue in the project's Feature Polish module -
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
	"strconv"
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

// DefaultModuleName is the module feedback is grouped under when the operator
// has not said otherwise (PLANE_FEEDBACK_MODULE). The module is resolved by
// its NAME against the board's own module list, never by a hard coded id: the
// id is a board side detail this server never writes.
const DefaultModuleName = "Feature Polish"

// feedbackLabelName is the project label applied to every work item WHEN the
// project already carries a label of that name. The bridge never creates it:
// a missing label files the item untagged rather than reaching for a write it
// was never granted.
const feedbackLabelName = "feedback"

// maxImagesPerSubmission bounds how many screenshots one submission carries.
const maxImagesPerSubmission = 3

// maxImageBytes bounds one accepted image attachment (matches the client + the
// multipart body cap in the handler).
const maxImageBytes = 5 << 20

// maxTotalImageBytes bounds every image of one submission together: two full
// captures and a smaller third fit, three full size ones do not. A submission
// is a note with evidence, not an archive, and the total bound bites below the
// per image bound times three on purpose.
const maxTotalImageBytes = 10 << 20

// allowedImageType is the server-enforced allow-list, checked against the SNIFFED
// content type (http.DetectContentType), not the client-declared MIME.
var allowedImageType = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

// Image is one screenshot of a submission, read server side from the multipart
// body (never from client-controlled JSON). Data is the raw bytes; Name is
// advisory (the server re-sniffs the real content type before trusting it).
type Image struct {
	Data []byte
	Name string
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
	// ActorName and ActorRoleName name the submitter in the work item by
	// display name and role (the row 949 rule: every note records under one
	// board user, so the item's text is the only place the sender is named;
	// the raw ActorID never reaches the board). Both may be empty when no
	// linked row resolves, and the item then names the role alone.
	ActorName     string `json:"-"`
	ActorRoleName string `json:"-"`
	// Images are the attached screenshots, server-only: filled from the
	// multipart "images" parts, NEVER from client-controlled JSON, so they
	// carry no json tags. A legacy single "image" part lands here too.
	Images []Image `json:"-"`
}

// Filed is a submission that became a board work item: its id and, when the
// board answered one, the number a person reads (the project's identifier and
// the issue's sequence, the panel's "Sent: HH-42" line).
type Filed struct {
	ID     string
	Number string
}

// Service files submissions to Plane. Safe for concurrent use.
type Service struct {
	base string // <PlaneAPIURL>/workspaces/<slug>/projects/<project-id>
	key  string
	http *http.Client
	now  func() time.Time
	log  *slog.Logger

	// moduleName is the module feedback is grouped under, resolved by name.
	moduleName string

	mu         sync.Mutex
	moduleID   string                 // resolved-once cache for moduleName
	labelID    string                 // resolved-once cache for the feedback label ("" = none yet)
	projectTag string                 // resolved-once cache for the project identifier
	buckets    map[string][]time.Time // per-user submission timestamps (rate limit)
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
		base:       strings.TrimRight(baseURL, "/") + "/workspaces/" + slug + "/projects/" + project,
		key:        key,
		moduleName: DefaultModuleName,
		// 30s (up from 15s): the presigned attachment upload leg streams up to 5 MB,
		// which can outrun a tighter deadline on a slow uplink.
		http:    &http.Client{Timeout: 30 * time.Second},
		now:     time.Now,
		log:     slog.Default(),
		buckets: map[string][]time.Time{},
	}
}

// WithModule sets the module feedback is grouped under, resolved by its name
// against the board's module list. An empty name keeps the default.
func (s *Service) WithModule(name string) *Service {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		s.moduleName = trimmed
	}
	return s
}

// ErrRateLimited is returned when a user exceeds rateLimit per rateWindow.
var ErrRateLimited = fmt.Errorf("rate limited: max %d submissions per %s", rateLimit, rateWindow)

// PlaneError wraps any upstream Plane failure so the handler can answer 502
// without leaking the upstream body (which could include URLs/ids).
type PlaneError struct{ Err error }

func (e PlaneError) Error() string { return "plane upstream: " + e.Err.Error() }
func (e PlaneError) Unwrap() error { return e.Err }

// actorWords renders the submitter for the work item's text: the display name
// and the role (the role label when one resolved, else the role code). When no
// display name resolved the role carries the line alone; the raw user id is
// never returned, so it can never reach the board (the row 949 rule).
func (sub Submission) actorWords() string {
	role := sub.ActorRoleName
	if role == "" {
		role = sub.ActorRole
	}
	if sub.ActorName == "" {
		return role
	}
	if role == "" {
		return sub.ActorName
	}
	return sub.ActorName + " (" + role + ")"
}

// validateImages enforces the image bounds on the server side: at most three
// images, each within the per image bound, all together within the total
// bound, each sniffing as an allowed image type. It runs before anything
// reaches the board, so an oversized or misnamed upload is a plain 400 rather
// than a half filed submission.
func validateImages(images []Image) error {
	if len(images) > maxImagesPerSubmission {
		return fmt.Errorf("too_many_images: at most %d images per submission", maxImagesPerSubmission)
	}
	total := 0
	for i, img := range images {
		if len(img.Data) == 0 {
			return fmt.Errorf("image %d is empty", i+1)
		}
		if len(img.Data) > maxImageBytes {
			return fmt.Errorf("image %d is too large: %d bytes (max %d)", i+1, len(img.Data), maxImageBytes)
		}
		total += len(img.Data)
		if total > maxTotalImageBytes {
			return fmt.Errorf("images exceed the %d byte total bound", maxTotalImageBytes)
		}
		sniff := img.Data
		if len(sniff) > 512 {
			sniff = sniff[:512]
		}
		if _, ok := allowedImageType[http.DetectContentType(sniff)]; !ok {
			return fmt.Errorf("image %d is not a PNG, JPEG or WebP", i+1)
		}
	}
	return nil
}

// File validates, rate-limits, and creates the Plane issue. Returns the new
// issue's id and its readable number.
func (s *Service) File(ctx context.Context, sub Submission) (Filed, error) {
	sub.Message = strings.TrimSpace(sub.Message)
	if sub.Message == "" {
		return Filed{}, fmt.Errorf("message is required")
	}
	if _, ok := priorityOf[sub.Kind]; !ok {
		return Filed{}, fmt.Errorf("type must be one of bug, feedback, flag")
	}
	if err := validateImages(sub.Images); err != nil {
		return Filed{}, err
	}
	if !s.allow(sub.ActorID) {
		return Filed{}, ErrRateLimited
	}

	moduleID, err := s.resolveModule(ctx)
	if err != nil {
		return Filed{}, PlaneError{err}
	}

	first := sub.Message
	if i := strings.IndexByte(first, '\n'); i > 0 {
		first = first[:i]
	}
	if len(first) > 80 {
		first = first[:80] + "…"
	}
	actor := sub.actorWords()
	title := fmt.Sprintf("[%s] %s", sub.Kind, first)
	if actor != "" {
		title += " - " + actor
	}

	desc := fmt.Sprintf(
		"<p>%s</p><p><em>screen: %s · actor: %s · url: %s · ua: %s · submitted: %s · via ERP feedback widget</em></p>",
		html.EscapeString(sub.Message), html.EscapeString(sub.Screen),
		html.EscapeString(actor),
		html.EscapeString(sub.URL), html.EscapeString(sub.UserAgent),
		s.now().UTC().Format(time.RFC3339))

	issue := map[string]any{
		"name":             title,
		"description_html": desc,
		"priority":         priorityOf[sub.Kind],
	}
	// The project's existing "feedback" label rides the item when present. The
	// lookup is best effort and cached: a board without the label (or a label
	// list the board refuses to list) files untagged rather than failing the
	// tester's report.
	if labelID := s.feedbackLabel(ctx); labelID != "" {
		issue["label_ids"] = []string{labelID}
	}
	var made struct {
		ID         string `json:"id"`
		SequenceID int64  `json:"sequence_id"`
	}
	if err := s.req(ctx, http.MethodPost, "/issues/", issue, &made); err != nil {
		return Filed{}, PlaneError{err}
	}
	filed := Filed{ID: made.ID, Number: s.workItemNumber(ctx, made.SequenceID)}
	if err := s.req(ctx, http.MethodPost, "/modules/"+moduleID+"/module-issues/",
		map[string]any{"issues": []string{made.ID}}, nil); err != nil {
		// The issue exists; module attach failing should not lose the submission,
		// and it must not drop the screenshots either: they attach below.
		s.log.Warn("feedback: module attach failed", "issue", made.ID, "err", err)
	}

	// Best-effort image attachments, each its own. The issue is already filed;
	// an attachment failure must NEVER lose the tester's report (mirrors the
	// module-attach fallback above), so we log and still return the issue.
	for _, img := range sub.Images {
		if err := s.attachImage(ctx, made.ID, img); err != nil {
			s.log.Warn("feedback: image attach failed", "issue", made.ID, "err", err)
		}
	}
	return filed, nil
}

// workItemNumber renders the number a person reads on the board: the project's
// own identifier and the issue's sequence (HH-42). Both halves are cached,
// best-effort reads; a board that answers neither leaves the number empty, and
// the id never stands in for it.
func (s *Service) workItemNumber(ctx context.Context, sequence int64) string {
	if sequence <= 0 {
		return ""
	}
	ident := s.projectIdentifier(ctx)
	if ident == "" {
		return strconv.FormatInt(sequence, 10)
	}
	return ident + "-" + strconv.FormatInt(sequence, 10)
}

// projectIdentifier resolves (and caches) the project's identifier, the prefix
// of every work item's readable number. Best effort: an unreadable identifier
// answers empty and the number degrades to the bare sequence.
func (s *Service) projectIdentifier(ctx context.Context) string {
	s.mu.Lock()
	cached := s.projectTag
	s.mu.Unlock()
	if cached != "" {
		return cached
	}
	var proj struct {
		Identifier string `json:"identifier"`
	}
	if err := s.req(ctx, http.MethodGet, "/", nil, &proj); err != nil {
		s.log.Warn("feedback: project identifier unreadable", "err", err)
		return ""
	}
	if ident := strings.TrimSpace(proj.Identifier); ident != "" {
		s.mu.Lock()
		s.projectTag = ident
		s.mu.Unlock()
		return ident
	}
	return ""
}

// feedbackLabel resolves (and caches) the project's existing "feedback" label
// id, or "" when the project has no such label. It never creates the label and
// never fails the submission: any refusal on this read is a board without the
// label as far as the caller is concerned, and a later submission looks again.
func (s *Service) feedbackLabel(ctx context.Context) string {
	s.mu.Lock()
	cached := s.labelID
	s.mu.Unlock()
	if cached != "" {
		return cached
	}
	var page struct {
		Results []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := s.req(ctx, http.MethodGet, "/issue-labels/", nil, &page); err != nil {
		s.log.Warn("feedback: label lookup failed, filing untagged", "err", err)
		return ""
	}
	for _, l := range page.Results {
		if l.Name == feedbackLabelName {
			s.mu.Lock()
			s.labelID = l.ID
			s.mu.Unlock()
			return l.ID
		}
	}
	return ""
}

// attachImage uploads one image to the Plane issue as a real binary attachment
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
func (s *Service) attachImage(ctx context.Context, issueID string, img Image) error {
	// Server-side re-validation, do NOT trust the client-declared type/size.
	if len(img.Data) == 0 {
		return fmt.Errorf("empty image")
	}
	if len(img.Data) > maxImageBytes {
		return fmt.Errorf("image too large: %d bytes (max %d)", len(img.Data), maxImageBytes)
	}
	sniff := img.Data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	ct := http.DetectContentType(sniff)
	ext, ok := allowedImageType[ct]
	if !ok {
		return fmt.Errorf("unsupported image type: %s", ct)
	}
	name := attachmentName(img.Name, ext)

	// 1) presign, POST gets the project_id injection in s.req, which is harmless.
	var presign struct {
		UploadData struct {
			URL    string            `json:"url"`
			Fields map[string]string `json:"fields"`
		} `json:"upload_data"`
		AssetID string `json:"asset_id"`
	}
	if err := s.req(ctx, http.MethodPost, "/issues/"+issueID+"/issue-attachments/",
		map[string]any{"name": name, "type": ct, "size": len(img.Data)}, &presign); err != nil {
		return fmt.Errorf("presign: %w", err)
	}
	if presign.UploadData.URL == "" || presign.AssetID == "" {
		return fmt.Errorf("presign returned no upload target")
	}

	// 2) upload to the presigned foreign host (no X-API-Key).
	if err := s.uploadPresigned(ctx, presign.UploadData.URL, presign.UploadData.Fields, name, ct, img.Data); err != nil {
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
// to turn a flooding caller away before buffering a large body; File's allow()
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

// resolveModule resolves the configured module NAME against the board's module
// list and caches it. It never creates a module: a name that does not resolve
// is a clear refusal and no work item is made, because a misnamed variable must
// not scatter feedback into a freshly made module nobody reads.
func (s *Service) resolveModule(ctx context.Context) (string, error) {
	s.mu.Lock()
	cached := s.moduleID
	name := s.moduleName
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
	for _, m := range page.Results {
		if m.Name == name {
			s.mu.Lock()
			s.moduleID = m.ID
			s.mu.Unlock()
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("module %q is not on the board: set PLANE_FEEDBACK_MODULE to an existing module's name (or create the module on the board) and try again", name)
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
