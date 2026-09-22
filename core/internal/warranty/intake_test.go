package warranty

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// intake_test.go, DIB-222 acceptance: the PUBLIC intake doorway and, above all, its REFUSALS.
//
// A public write route is judged by what it declines. The named refusals here are the three the ticket calls
// out, oversize, wrong content-type, rate-limit, plus the two that make the door safe to leave open at all:
// bytes that do not match their declared type, and the absence of any path from this route into the rest of the
// module.

// - fixtures: the smallest REAL images, so the magic-byte check is exercised for real -

// pngBytes is a valid 1x1 PNG: signature + IHDR/IDAT/IEND.
func pngBytes() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
		0, 0, 0, 10, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4,
		0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
	}
}

// jpegBytes is a minimal JPEG: SOI + APP0 + EOI. Enough for the magic-byte check, which is what is under test.
func jpegBytes() []byte {
	return append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0}, 0xFF, 0xD9)
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func validIntake() PublicIntakeInput {
	return PublicIntakeInput{
		Contact:     IntakeContact{Name: "Dana Whitfield", Email: "dana@example.com", Phone: "613-555-0114"},
		JobAddress:  Address{Line1: "88 Lakeshore Rd", City: "Trenton", Province: "ON", Postal: "K8V 1A1"},
		Description: "Efflorescence across the whole driveway three months after install.",
		PurchasedOn: "2026-04-02",
		Lines:       []PublicIntakeLine{{ProductText: "grey 60mm pavers", Qty: "180"}},
	}
}

// - the happy path (so the refusals mean something) -

func TestPublicIntake_CreatesAnIntakeClaimWithAClientFacingReference(t *testing.T) {
	svc, fs, _ := newSvc()
	in := validIntake()
	in.Photos = []PublicIntakePhoto{{ContentType: "image/png", DataBase64: b64(pngBytes()), Caption: "driveway"}}

	res, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:51514")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Reference, "WR-2026-") || len(res.Reference) != 16 {
		t.Errorf("the reference must be the client-facing WR-YYYY-XXXXXXXX form; got %q", res.Reference)
	}
	// It must NOT be the staff sequence: handing the public a gap-free number tells every submitter how many
	// warranty claims the business has.
	if strings.Contains(res.Reference, "WTY-") {
		t.Errorf("the public reference must not be the #WTY-n sequence; got %q", res.Reference)
	}
	// Exactly one claim, in INTAKE, sourced WEB.
	var found Claim
	for _, c := range fs.claims {
		if c.Source == SourceWeb {
			found = c
		}
	}
	if found.ID == "" || found.Phase != PhaseIntake {
		t.Fatalf("a public submission must land as an INTAKE/WEB claim; got %+v", found)
	}
	if len(fs.photos[found.ID]) != 1 || fs.photos[found.ID][0].ContentType != "image/png" {
		t.Errorf("the photo must be stored with its sniffed type; got %+v", fs.photos[found.ID])
	}
	// No staff context existed, so no owner was invented and no actor was fabricated on the audit spine.
	if found.OwnerID != "" {
		t.Errorf("a public claim must not be assigned an owner by the doorway; got %q", found.OwnerID)
	}
}

func TestPublicIntake_TakesNoStaffContextAndCannotSetPrivilegedFields(t *testing.T) {
	svc, fs, _ := newSvc()
	// The input type has NOWHERE to put a customer_id, vendor_id, owner_id, invoice_id, po_id or expected_cents.
	// That is the guarantee: the public path cannot set a field it cannot name. This test pins it by walking the
	// resulting claim and asserting every one of those is empty/zero.
	if _, err := svc.SubmitPublicIntake(context.Background(), validIntake(), "198.51.100.4:2000"); err != nil {
		t.Fatal(err)
	}
	for _, c := range fs.claims {
		if c.Source != SourceWeb {
			continue
		}
		if c.CustomerID != "" || c.VendorID != "" || c.OwnerID != "" || c.InvoiceID != "" || c.POID != "" || c.ExpectedCents != 0 {
			t.Fatalf("the public doorway must not be able to set privileged fields; got %+v", c)
		}
	}
}

// - REFUSAL 1: oversize -

func TestPublicIntake_RefusesOversizePhoto(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.intakeCfg.MaxPhotoBytes = 1024 // an operator tightened the door
	in := validIntake()
	big := append(pngBytes(), bytes.Repeat([]byte{0x42}, 4096)...)
	in.Photos = []PublicIntakePhoto{{ContentType: "image/png", DataBase64: b64(big)}}

	_, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:1")
	if err == nil {
		t.Fatal("an oversize photo must be refused")
	}
	if st := statusOf(err); st != http.StatusRequestEntityTooLarge {
		t.Errorf("want 413; got %d (%v)", st, err)
	}
	if !strings.Contains(err.Error(), "photo 1") {
		t.Errorf("the refusal must name WHICH photo so a submitter knows what to drop; got %q", err.Error())
	}
	// And nothing was written.
	if len(fs.claims) != 0 {
		t.Error("a refused submission must write nothing")
	}
}

func TestPublicIntake_RefusesTooManyPhotos(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.intakeCfg.MaxPhotos = 2
	in := validIntake()
	for i := 0; i < 3; i++ {
		in.Photos = append(in.Photos, PublicIntakePhoto{ContentType: "image/png", DataBase64: b64(pngBytes())})
	}
	if _, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:1"); err == nil {
		t.Fatal("exceeding the photo count must be refused")
	}
}

func TestPublicIntake_HTTPBodyCapIs413(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.intakeCfg.MaxBodyBytes = 512
	mux := http.NewServeMux()
	NewHandler(svc, nil).RegisterPublicRoutes(mux)

	in := validIntake()
	in.Photos = []PublicIntakePhoto{{ContentType: "image/png", DataBase64: b64(append(pngBytes(), bytes.Repeat([]byte{1}, 4096)...))}}
	body, _ := json.Marshal(in)
	req := httptest.NewRequest("POST", "/api/public/v1/warranty/intake", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// The process must never allocate the whole of a hostile upload: MaxBytesReader cuts it off at the door.
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversize BODY must be 413 at the door; got %d, %s", rec.Code, rec.Body.String())
	}
}

// - REFUSAL 2: wrong content type (declared, and actual) -

func TestPublicIntake_RefusesWrongRequestContentType(t *testing.T) {
	svc, _, _ := newSvc()
	mux := http.NewServeMux()
	NewHandler(svc, nil).RegisterPublicRoutes(mux)

	body, _ := json.Marshal(validIntake())
	req := httptest.NewRequest("POST", "/api/public/v1/warranty/intake", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a non-JSON submission must be 415; got %d", rec.Code)
	}
}

func TestPublicIntake_RefusesDisallowedPhotoType(t *testing.T) {
	svc, _, _ := newSvc()
	in := validIntake()
	// SVG carries script and these bytes are served back from an origin that also serves the authenticated app.
	in.Photos = []PublicIntakePhoto{{ContentType: "image/svg+xml", DataBase64: b64([]byte(`<svg onload="x"/>`))}}
	_, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:1")
	if err == nil {
		t.Fatal("a disallowed photo type must be refused")
	}
	if st := statusOf(err); st != http.StatusUnsupportedMediaType {
		t.Errorf("want 415; got %d (%v)", st, err)
	}
}

func TestPublicIntake_RefusesBytesThatDoNotMatchTheDeclaredType(t *testing.T) {
	svc, fs, _ := newSvc()
	in := validIntake()
	// The attacker's case: HTML declared as PNG. Stored and served back, this is stored XSS on the app's origin.
	in.Photos = []PublicIntakePhoto{{ContentType: "image/png", DataBase64: b64([]byte(`<html><script>alert(1)</script></html>`))}}
	_, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:1")
	if err == nil {
		t.Fatal("bytes that disagree with the declared type must be refused")
	}
	if st := statusOf(err); st != http.StatusUnsupportedMediaType {
		t.Errorf("want 415; got %d (%v)", st, err)
	}
	if len(fs.photos) != 0 || len(fs.claims) != 0 {
		t.Error("a refused photo must not be stored, and its claim must not exist")
	}
}

func TestPublicIntake_AcceptsAllThreeAllowedRasterTypes(t *testing.T) {
	webp := append([]byte("RIFF"), 0, 0, 0, 0)
	webp = append(webp, []byte("WEBP")...)
	cases := []struct {
		ct    string
		bytes []byte
	}{
		{"image/png", pngBytes()},
		{"image/jpeg", jpegBytes()},
		{"image/webp", webp},
	}
	for _, c := range cases {
		svc, _, _ := newSvc()
		in := validIntake()
		in.Photos = []PublicIntakePhoto{{ContentType: c.ct, DataBase64: b64(c.bytes)}}
		if _, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:1"); err != nil {
			t.Errorf("%s must be accepted; got %v", c.ct, err)
		}
	}
}

// - REFUSAL 3: rate limit -

func TestPublicIntake_RateLimitRefusesTheSixthSubmissionInAWindow(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.intakeCfg.MaxPerWindow = 5
	fs.intakeCfg.WindowSeconds = 3600
	addr := "203.0.113.77:40000"

	for i := 1; i <= 5; i++ {
		if _, err := svc.SubmitPublicIntake(context.Background(), validIntake(), addr); err != nil {
			t.Fatalf("submission %d should be allowed: %v", i, err)
		}
	}
	_, err := svc.SubmitPublicIntake(context.Background(), validIntake(), addr)
	if err == nil {
		t.Fatal("the sixth submission in the window must be refused")
	}
	if st := statusOf(err); st != http.StatusTooManyRequests {
		t.Errorf("want 429; got %d (%v)", st, err)
	}
	if len(fs.claims) != 5 {
		t.Errorf("a rate-limited submission must write nothing; got %d claims", len(fs.claims))
	}
}

func TestPublicIntake_RateLimitIsPerAddressAndRollsOverWithTheClock(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.intakeCfg.MaxPerWindow = 1
	fs.intakeCfg.WindowSeconds = 3600
	now := time.Date(2026, 7, 14, 12, 30, 0, 0, time.UTC)
	svc.withClock(func() time.Time { return now })

	if _, err := svc.SubmitPublicIntake(context.Background(), validIntake(), "203.0.113.1:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitPublicIntake(context.Background(), validIntake(), "203.0.113.1:2"); err == nil {
		t.Fatal("the same ADDRESS must share a bucket regardless of source port")
	}
	// A different address is a different bucket, one abuser must not close the form for everyone.
	if _, err := svc.SubmitPublicIntake(context.Background(), validIntake(), "198.51.100.2:1"); err != nil {
		t.Fatalf("a different address must have its own bucket; got %v", err)
	}
	// The window rolls: advance the FAKE clock past the hour boundary and the first address is allowed again.
	now = now.Add(90 * time.Minute)
	if _, err := svc.SubmitPublicIntake(context.Background(), validIntake(), "203.0.113.1:3"); err != nil {
		t.Fatalf("a new window must reset the bucket; got %v", err)
	}
}

func TestPublicIntake_RateLimitIgnoresClientSuppliedForwardedHeaders(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.intakeCfg.MaxPerWindow = 1
	mux := http.NewServeMux()
	NewHandler(svc, nil).RegisterPublicRoutes(mux)

	post := func(xff string) int {
		body, _ := json.Marshal(validIntake())
		req := httptest.NewRequest("POST", "/api/public/v1/warranty/intake", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", xff)
		req.RemoteAddr = "203.0.113.55:9000" // the SERVER-observed address never changes
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("10.0.0.1"); code != http.StatusCreated {
		t.Fatalf("first submission should succeed; got %d", code)
	}
	// Rotating a header the SENDER controls must not mint a fresh bucket, otherwise the limit is decorative.
	// Same reasoning the comms substrate applies to a payload's To field.
	if code := post("10.0.0.2"); code != http.StatusTooManyRequests {
		t.Fatalf("a rotated X-Forwarded-For must not reset the limiter; got %d", code)
	}
}

// - the door can be closed, and the validation is public-appropriate -

func TestPublicIntake_DisabledFormIs503NotAnError(t *testing.T) {
	svc, fs, _ := newSvc()
	fs.intakeCfg.IsEnabled = false
	_, err := svc.SubmitPublicIntake(context.Background(), validIntake(), "203.0.113.9:1")
	if err == nil {
		t.Fatal("a disabled form must refuse")
	}
	if st := statusOf(err); st != http.StatusServiceUnavailable {
		t.Errorf("a closed form is 503 (come back / phone us), not 500; got %d", st)
	}
}

func TestPublicIntake_RequiresNameAndAWayToReachYouAndADescription(t *testing.T) {
	cases := map[string]func(*PublicIntakeInput){
		"no name":        func(in *PublicIntakeInput) { in.Contact.Name = "" },
		"no contact":     func(in *PublicIntakeInput) { in.Contact.Email, in.Contact.Phone = "", "" },
		"no description": func(in *PublicIntakeInput) { in.Description = "" },
		"bad date":       func(in *PublicIntakeInput) { in.PurchasedOn = "last spring" },
	}
	for name, mutate := range cases {
		svc, fs, _ := newSvc()
		in := validIntake()
		mutate(&in)
		if _, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:1"); err == nil {
			t.Errorf("%s: must be refused", name)
		}
		if len(fs.claims) != 0 {
			t.Errorf("%s: a refused submission must write nothing", name)
		}
	}
}

func TestPublicIntake_ErrorsNameFieldsNeverInternals(t *testing.T) {
	svc, _, _ := newSvc()
	in := validIntake()
	in.Contact.Name = ""
	_, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:1")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	// A public error must be useful to a member of the public and useless to someone mapping the system.
	for _, leak := range []string{"warranty_claims", "uuid", "constraint", "branch_id", "SELECT"} {
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(leak)) {
			t.Errorf("the public error leaks an internal (%s): %q", leak, err.Error())
		}
	}
}

// - the doorway has exactly one route, and it is a POST -

func TestPublicIntake_RegistersExactlyOneUnguardedRouteAndNoPublicRead(t *testing.T) {
	svc, _, _ := newSvc()
	mux := http.NewServeMux()
	NewHandler(svc, nil).RegisterPublicRoutes(mux)

	// A public GET keyed on the reference number would be the auth bypass the ticket forbids. There is none.
	for _, probe := range []struct{ method, path string }{
		{"GET", "/api/public/v1/warranty/intake"},
		{"GET", "/api/public/v1/warranty/intake/WR-2026-ABCDEFGH"},
		{"GET", "/api/public/v1/warranty/claims/WR-2026-ABCDEFGH"},
	} {
		req := httptest.NewRequest(probe.method, probe.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("%s %s must not be a public route", probe.method, probe.path)
		}
	}
}

// - the regression the Chromium walkthrough found -

// TestPublicIntake_FreeTextQuantityDoesNotCrashAndKeepsTheUnits pins the bug the W5-B walkthrough surfaced: the
// form asks "how much?" and suggests "180 sq ft", the qty column is DECIMAL(19,4), and the raw string used to
// reach Postgres and come back 22P02, a 500 on an unauthenticated public form for a perfectly reasonable
// answer. The fix takes the leading number AND keeps the words, because a triager who is told "180" with no
// unit is choosing between square feet, pallets and pieces.
func TestPublicIntake_FreeTextQuantityDoesNotCrashAndKeepsTheUnits(t *testing.T) {
	cases := []struct {
		qty       string
		wantQty   string
		wantNotes string
	}{
		{"180 sq ft", "180", "quantity as submitted: 180 sq ft"},
		{"1,250 pieces", "1250", "quantity as submitted: 1,250 pieces"},
		{"about a pallet", "0", "quantity as submitted: about a pallet"},
		{"12.5", "12.5", ""},
		{"", "0", ""},
	}
	for _, c := range cases {
		svc, fs, _ := newSvc()
		in := validIntake()
		in.Lines = []PublicIntakeLine{{ProductText: "grey pavers", Qty: c.qty}}
		if _, err := svc.SubmitPublicIntake(context.Background(), in, "203.0.113.9:1"); err != nil {
			t.Fatalf("qty %q must not fail the submission: %v", c.qty, err)
		}
		// The stored numeric is what the column can hold …
		if got := nullDecimal(c.qty); got != c.wantQty {
			t.Errorf("qty %q → column %v, want %s", c.qty, got, c.wantQty)
		}
		// … and the words survive on the line.
		if got := publicLineNotes(PublicIntakeLine{Qty: c.qty}); got != c.wantNotes {
			t.Errorf("qty %q → notes %q, want %q", c.qty, got, c.wantNotes)
		}
		if len(fs.claims) != 1 {
			t.Errorf("qty %q: the claim must still be created", c.qty)
		}
	}
}
