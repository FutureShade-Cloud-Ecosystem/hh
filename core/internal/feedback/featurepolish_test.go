package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// The Feature Polish contract (decision log row 958): the module comes from a
// configured NAME never a hard coded id, the work item names the submitter by
// display name and role never the raw user id (the row 949 rule, reinforced by
// row 959 because every note records under one board user), up to three images
// each its own attachment, and the project's existing "feedback" label is
// applied when present and never created. Every test here was written against
// the contract and failed on main before the change.

// polishPlane is a configurable fake of every board endpoint the bridge
// touches: modules (list and create), issue labels (list and create), the
// project detail (its identifier), issues, module-issues, and the three-step
// attachment flow. Tests set the module list and the label list to the shape
// they need and read back everything the bridge did.
type polishPlane struct {
	mu   sync.Mutex
	base string

	modules []map[string]string // GET /modules/ results: id + name
	labels  []map[string]string // GET /issue-labels/ results: id + name

	moduleCreates  int // POST /modules/ (the bridge must never)
	labelCreates   int // POST /issue-labels/ (the bridge must never)
	issues         []map[string]any
	moduleAttaches []string // the module id each issue was attached to
	sequenceID     int      // answered on issue create (0 to omit)
	projectIdent   string   // answered on the project detail read

	presigns, uploads, confirms int
	uploadBytes                 [][]byte
	uploadAPIKey                bool
}

func newPolishPlane(t *testing.T) *polishPlane {
	t.Helper()
	p := &polishPlane{
		modules:      []map[string]string{{"id": "mod-fp", "name": "Feature Polish"}},
		sequenceID:   42,
		projectIdent: "HH",
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/workspaces/ws/projects/proj/modules/", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"results": p.modules})
	})
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/modules/", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		p.moduleCreates++
		p.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "mod-made"})
	})
	mux.HandleFunc("GET /api/v1/workspaces/ws/projects/proj/issue-labels/", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"results": p.labels})
	})
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/issue-labels/", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		p.labelCreates++
		p.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "lab-made"})
	})
	mux.HandleFunc("GET /api/v1/workspaces/ws/projects/proj/", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"identifier": p.projectIdent})
	})
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/issues/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		p.mu.Lock()
		p.issues = append(p.issues, body)
		sequence := p.sequenceID
		p.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		answer := map[string]any{"id": "issue-1"}
		if sequence > 0 {
			answer["sequence_id"] = sequence
		}
		_ = json.NewEncoder(w).Encode(answer)
	})
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/modules/{module}/module-issues/", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.moduleAttaches = append(p.moduleAttaches, r.PathValue("module"))
		p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	// The attachment flow, one asset per presign so three images make three.
	mux.HandleFunc("POST /api/v1/workspaces/ws/projects/proj/issues/issue-1/issue-attachments/", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.presigns++
		n := p.presigns
		base := p.base
		p.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"asset_id": fmt.Sprintf("asset-%d", n),
			"upload_data": map[string]any{
				"url":    fmt.Sprintf("%s/uploads/%d", base, n),
				"fields": map[string]string{"key": fmt.Sprintf("abc/shot-%d.png", n), "policy": "base64policy"},
			},
		})
	})
	mux.HandleFunc("POST /uploads/{n}", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.uploads++
		if r.Header.Get("X-API-Key") != "" {
			p.uploadAPIKey = true
		}
		if err := r.ParseMultipartForm(20 << 20); err == nil {
			if f, _, err := r.FormFile("file"); err == nil {
				defer f.Close()
				b, _ := io.ReadAll(f)
				p.uploadBytes = append(p.uploadBytes, b)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PATCH /api/v1/workspaces/ws/projects/proj/issues/issue-1/issue-attachments/{asset}/", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.confirms++
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p.base = srv.URL
	return p
}

func (p *polishPlane) service(module string) *Service {
	s := New(p.base+"/api/v1", "ws", "proj", "test-key")
	if module != "" {
		s = s.WithModule(module)
	}
	return s
}

func polishImages(n int) []Image {
	out := make([]Image, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Image{Data: pngBytes(400 + i), Name: fmt.Sprintf("shot-%d.png", i+1)})
	}
	return out
}

// The module is the configured NAME, resolved against the board's own module
// list, never a hard coded id: the default lands on the module named Feature
// Polish even when a module named Feedback sits first in the list, and a
// different configured name lands on that module instead.
func TestModuleComesFromTheConfiguredNameNotAnID(t *testing.T) {
	p := newPolishPlane(t)
	p.modules = []map[string]string{
		{"id": "mod-old", "name": "Feedback"},
		{"id": "mod-fp", "name": "Feature Polish"},
		{"id": "mod-imp", "name": "Imports"},
	}

	// The default: Feature Polish, and the attach names that module's id.
	if _, err := p.service("").File(context.Background(), Submission{
		Kind: KindFeedback, Message: "the default module", ActorID: "u-1", ActorRole: "GM",
	}); err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(p.moduleAttaches) != 1 || p.moduleAttaches[0] != "mod-fp" {
		t.Fatalf("default attached to %v, want the module NAMED Feature Polish (mod-fp)", p.moduleAttaches)
	}

	// A different valid name files into that module, not the default's.
	if _, err := p.service("Imports").File(context.Background(), Submission{
		Kind: KindFeedback, Message: "the configured module", ActorID: "u-1", ActorRole: "GM",
	}); err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(p.moduleAttaches) != 2 || p.moduleAttaches[1] != "mod-imp" {
		t.Fatalf("configured name attached to %v, want mod-imp", p.moduleAttaches)
	}
	if p.moduleCreates != 0 {
		t.Fatalf("modules created: %d, the bridge never creates a module", p.moduleCreates)
	}
}

// A name that does not resolve is a clear refusal and no work item exists: the
// bridge must not fall back to another module, invent an id, or create the
// module the way it once created the Feedback one.
func TestUnresolvedModuleNameRefusesWithoutFiling(t *testing.T) {
	p := newPolishPlane(t)
	p.modules = []map[string]string{{"id": "mod-old", "name": "Feedback"}}

	_, err := p.service("Feature Polish").File(context.Background(), Submission{
		Kind: KindBug, Message: "no such module", ActorID: "u-1", ActorRole: "GM",
	})
	if err == nil {
		t.Fatal("an unresolvable module name filed anyway")
	}
	if !strings.Contains(err.Error(), "Feature Polish") {
		t.Fatalf("refusal %q does not name the module it could not resolve", err)
	}
	var pe PlaneError
	if !errors.As(err, &pe) {
		t.Fatalf("an unresolvable module is a board side refusal (502), got %T", err)
	}
	if len(p.issues) != 0 {
		t.Fatalf("issues filed: %d, want none", len(p.issues))
	}
	if p.moduleCreates != 0 {
		t.Fatalf("modules created: %d, the bridge never creates a module", p.moduleCreates)
	}
}

// Three images are three separate attachments: three presign, three upload,
// three confirm, each image's own bytes on their own asset.
func TestThreeImagesAreThreeAttachments(t *testing.T) {
	p := newPolishPlane(t)
	images := polishImages(3)
	if _, err := p.service("").File(context.Background(), Submission{
		Kind: KindBug, Message: "with three shots", ActorID: "u-1", ActorRole: "GM",
		Images: images,
	}); err != nil {
		t.Fatalf("File: %v", err)
	}
	if p.presigns != 3 || p.uploads != 3 || p.confirms != 3 {
		t.Fatalf("presign=%d upload=%d confirm=%d, want 3/3/3", p.presigns, p.uploads, p.confirms)
	}
	if p.uploadAPIKey {
		t.Error("a presigned upload carried the board key")
	}
	if len(p.uploadBytes) != 3 {
		t.Fatalf("uploads: %d, want one per image", len(p.uploadBytes))
	}
	for i, img := range images {
		if string(p.uploadBytes[i]) != string(img.Data) {
			t.Errorf("upload %d carries %d bytes, want the image's own %d", i, len(p.uploadBytes[i]), len(img.Data))
		}
	}
}

// Four images, an image over the per image bound, and a set over the total
// bound are all refused before anything reaches the board, as plain
// validation refusals (400), not board errors.
func TestImageBoundsRefuseBeforeTheBoard(t *testing.T) {
	p := newPolishPlane(t)

	cases := []struct {
		name   string
		images []Image
	}{
		{"four images", polishImages(4)},
		{"one image over the per image bound", []Image{{Data: pngBytes(maxImageBytes + 1), Name: "huge.png"}}},
		{"three images over the total bound", []Image{
			{Data: pngBytes(maxImageBytes), Name: "a.png"},
			{Data: pngBytes(maxImageBytes), Name: "b.png"},
			{Data: pngBytes(maxImageBytes), Name: "c.png"},
		}},
	}
	for _, c := range cases {
		_, err := p.service("").File(context.Background(), Submission{
			Kind: KindBug, Message: "bounded", ActorID: "u-1", ActorRole: "GM", Images: c.images,
		})
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		var pe PlaneError
		if errors.As(err, &pe) {
			t.Errorf("%s: refused as a board error, want a plain validation refusal", c.name)
		}
	}
	if len(p.issues) != 0 || p.presigns != 0 {
		t.Fatalf("issues=%d presigns=%d, the bounds must refuse before the board", len(p.issues), p.presigns)
	}
}

// The row 949 rule: the work item names the submitter by display name and
// role, and the raw user id appears nowhere in the title or the body. A
// missing display name degrades to the role alone, never to the id.
func TestWorkItemNamesTheSubmitterNeverTheID(t *testing.T) {
	p := newPolishPlane(t)
	if _, err := p.service("").File(context.Background(), Submission{
		Kind: KindBug, Message: "drawer maths off by a cent", Screen: "Counter Till",
		ActorID: "usr_9f8e7d6c5b4a", ActorName: "Dana Dealer", ActorRole: "inside_sales", ActorRoleName: "Inside Sales",
	}); err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(p.issues) != 1 {
		t.Fatalf("issues filed: %d", len(p.issues))
	}
	title, _ := p.issues[0]["name"].(string)
	desc, _ := p.issues[0]["description_html"].(string)
	for _, got := range []string{title, desc} {
		if strings.Contains(got, "usr_9f8e7d6c5b4a") {
			t.Errorf("the raw user id reached the work item: %q", got)
		}
	}
	if !strings.Contains(title, "Dana Dealer") || !strings.Contains(title, "Inside Sales") {
		t.Errorf("title %q does not name the submitter and the role", title)
	}
	if !strings.Contains(desc, "Dana Dealer") || !strings.Contains(desc, "Inside Sales") {
		t.Errorf("description does not name the submitter and the role: %q", desc)
	}

	// No display name: the role carries the line, the id still never appears.
	p2 := newPolishPlane(t)
	if _, err := p2.service("").File(context.Background(), Submission{
		Kind: KindFlag, Message: "unnamed actor", ActorID: "usr_deadbeef", ActorRole: "gm", ActorRoleName: "General Manager",
	}); err != nil {
		t.Fatalf("File: %v", err)
	}
	title2, _ := p2.issues[0]["name"].(string)
	desc2, _ := p2.issues[0]["description_html"].(string)
	if strings.Contains(title2, "usr_deadbeef") || strings.Contains(desc2, "usr_deadbeef") {
		t.Errorf("the raw user id reached the work item without a display name")
	}
	if !strings.Contains(title2, "General Manager") || !strings.Contains(desc2, "General Manager") {
		t.Errorf("the role must carry the actor line when no display name resolves: %q / %q", title2, desc2)
	}
}

// The project's existing "feedback" label rides the work item when the board
// already has one, and the bridge never creates it: a board without the label
// still files, untagged, with no label create call anywhere.
func TestFeedbackLabelAppliedOnlyWhenItAlreadyExists(t *testing.T) {
	p := newPolishPlane(t)
	p.labels = []map[string]string{
		{"id": "lab-bug", "name": "bug"},
		{"id": "lab-9", "name": "feedback"},
	}
	if _, err := p.service("").File(context.Background(), Submission{
		Kind: KindFeedback, Message: "labelled", ActorID: "u-1", ActorRole: "GM",
	}); err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(p.issues) != 1 {
		t.Fatalf("issues filed: %d", len(p.issues))
	}
	labels, _ := p.issues[0]["label_ids"].([]any)
	if len(labels) != 1 || labels[0] != "lab-9" {
		t.Fatalf("label_ids = %v, want the existing feedback label lab-9", p.issues[0]["label_ids"])
	}
	if p.labelCreates != 0 {
		t.Fatalf("labels created: %d, the bridge never creates one", p.labelCreates)
	}

	p2 := newPolishPlane(t)
	p2.labels = nil
	if _, err := p2.service("").File(context.Background(), Submission{
		Kind: KindFeedback, Message: "unlabelled", ActorID: "u-1", ActorRole: "GM",
	}); err != nil {
		t.Fatalf("File without the label: %v", err)
	}
	if len(p2.issues) != 1 {
		t.Fatalf("a missing label must not lose the submission: issues=%d", len(p2.issues))
	}
	if got, ok := p2.issues[0]["label_ids"]; ok && got != nil {
		t.Errorf("label_ids = %v on a board with no feedback label", got)
	}
	if p2.labelCreates != 0 {
		t.Fatalf("labels created: %d, the bridge never creates one", p2.labelCreates)
	}
}

// Send answers with the work item's number: the board's own project
// identifier and the issue's sequence make the number a person reads
// (HH-42), and the answer still carries the issue id.
func TestFileAnswersTheWorkItemNumber(t *testing.T) {
	p := newPolishPlane(t)
	filed, err := p.service("").File(context.Background(), Submission{
		Kind: KindBug, Message: "numbered", ActorID: "u-1", ActorRole: "GM",
	})
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if filed.ID != "issue-1" {
		t.Fatalf("id = %q", filed.ID)
	}
	if filed.Number != "HH-42" {
		t.Fatalf("number = %q, want HH-42 (the project identifier and the sequence)", filed.Number)
	}

	// A board that answers no identifier still files: the number degrades to
	// the bare sequence, and with no sequence to empty, never to the issue id.
	p2 := newPolishPlane(t)
	p2.projectIdent = ""
	if filed2, err := p2.service("").File(context.Background(), Submission{
		Kind: KindBug, Message: "bare sequence", ActorID: "u-1", ActorRole: "GM",
	}); err != nil {
		t.Fatalf("File: %v", err)
	} else if filed2.Number != "42" {
		t.Fatalf("number = %q, want the bare sequence 42", filed2.Number)
	}
	p3 := newPolishPlane(t)
	p3.sequenceID = 0
	if filed3, err := p3.service("").File(context.Background(), Submission{
		Kind: KindBug, Message: "no sequence", ActorID: "u-1", ActorRole: "GM",
	}); err != nil {
		t.Fatalf("File: %v", err)
	} else if filed3.Number != "" {
		t.Fatalf("number = %q, want empty when the board answers no sequence", filed3.Number)
	}
}

// The handler stamps the display name and the role label from its own
// resolver seam, never from anything the client sent, and answers the number.
func TestHandlerStampsActorAndAnswersNumber(t *testing.T) {
	p := newPolishPlane(t)
	h := NewHandler(p.service(""), slog.Default()).
		WithActorNames(func(ctx context.Context, userID string) (name, role string, ok bool) {
			if userID != "usr-linked" {
				return "", "", false
			}
			return "Dana Dealer", "Inside Sales", true
		})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("type", "bug")
	_ = mw.WriteField("message", "from the widget")
	_ = mw.WriteField("screen", "Quote Desk")
	fw, _ := mw.CreateFormFile("images", "shot-1.png")
	_, _ = fw.Write(pngBytes(300))
	fw2, _ := mw.CreateFormFile("images", "shot-2.png")
	_, _ = fw2.Write(pngBytes(320))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(identity.ContextWithStaff(req.Context(),
		identity.StaffContext{UserID: "usr-linked", RoleCode: "inside_sales"}))
	w := httptest.NewRecorder()
	h.create(w, req, guards.StaffCaller{})

	if w.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var answer struct {
		IssueID     string `json:"issue_id"`
		IssueNumber string `json:"issue_number"`
	}
	if err := json.NewDecoder(w.Body).Decode(&answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.IssueID != "issue-1" || answer.IssueNumber != "HH-42" {
		t.Fatalf("answer = %+v, want the id and HH-42", answer)
	}
	title, _ := p.issues[0]["name"].(string)
	if !strings.Contains(title, "Dana Dealer") || strings.Contains(title, "usr-linked") {
		t.Fatalf("title %q does not name the stamped actor without the raw id", title)
	}
	if p.uploads != 2 {
		t.Fatalf("uploads=%d, want one per attached image", p.uploads)
	}
}
