package load

// imagedoor_test.go covers the image door's boundary answers that need
// no database: the machine scope's absence is the one 403 body, an
// unnamed or over long legacy key is 400 before any store call, an
// image the module's own rules refuse (over the cap, outside the
// allowlist) is 400 naming the field and never a value, and the door
// refuses to run with no validator, writer or audit sink. The door is
// built over a nil pool store on purpose: if any of these paths reached
// the pool the test would answer a 500 and fail, which is the
// assertion's teeth (the same idiom doors_handler_test.go carries).

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/jackc/pgx/v5"
)

// fakeImageRules is the test's validator: the cap is small so the
// over-cap leg needs no megabyte body, and the allowlist carries one
// type.
type fakeImageRules struct {
	cap      int
	allow    string
	sniffBad bool
}

func (r fakeImageRules) MaxImageBytes() int { return r.cap }

// NormaliseContentType is the double's own normalisation, the module's
// rule mirrored: the parameter strip and the case fold.
func (r fakeImageRules) NormaliseContentType(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

func (r fakeImageRules) ValidateImage(contentType string, body []byte) (string, string, bool) {
	if contentType != r.allow {
		return "content_type", "the declared type is outside the raster allowlist", false
	}
	if len(body) == 0 {
		return "image", "the image body is empty", false
	}
	if len(body) > r.cap {
		return "image", "the image is over the size cap the module carries", false
	}
	if r.sniffBad {
		return "content_type", "the bytes disagree with the declared type", false
	}
	return "", "", true
}

// noopImageWriter records that the door never reached the write.
type noopImageWriter struct{ reached bool }

func (w *noopImageWriter) ApplyImage(_ context.Context, _ pgx.Tx, _ ImageWrite) error {
	w.reached = true
	return nil
}

func testImageDoor(t *testing.T) (*ImageDoor, *ImageHandler, *noopImageWriter) {
	t.Helper()
	w := &noopImageWriter{}
	door := NewImageDoor(NewStore(nil), fakeImageRules{cap: 16, allow: "image/png"}, w, noopAudit{})
	return door, NewImageHandler(door, testLogger(t)), w
}

func putImage(t *testing.T, h *ImageHandler, scoped bool, path, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	return putImageKey(t, h, scoped, path, "P-1", contentType, body)
}

func putImageKey(t *testing.T, h *ImageHandler, scoped bool, path, legacyID, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	req := httptest.NewRequest(http.MethodPut, path+sep+"source_system=testsuite", bytes.NewReader(body))
	req.SetPathValue("legacyId", legacyID)
	req.Header.Set("Content-Type", contentType)
	if scoped {
		req = withScope(req)
	}
	rec := httptest.NewRecorder()
	h.putImage(rec, req)
	return rec
}

func TestImagePutRefusesAMissingScope(t *testing.T) {
	_, h, _ := testImageDoor(t)
	rec := putImage(t, h, false, "/api/v1/load/products/P-1/image", "image/png", []byte("bytes"))
	if rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
		t.Fatalf("an image put with no machine scope answered %d %q, want the one 403 body", rec.Code, rec.Body.String())
	}
}

func TestImagePutRefusesABadLegacyKey(t *testing.T) {
	_, h, _ := testImageDoor(t)
	rec := putImageKey(t, h, true, "/api/v1/load/products//image", "", "image/png", []byte("bytes"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an image put with no legacy key answered %d, want 400", rec.Code)
	}
	long := strings.Repeat("K", MaxKeyLength+1)
	rec = putImageKey(t, h, true, "/api/v1/load/products/"+long+"/image", long, "image/png", []byte("bytes"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an image put with an over long legacy key answered %d, want 400", rec.Code)
	}
}

func TestImagePutRefusesAnOverCapBodyNamingTheField(t *testing.T) {
	_, h, w := testImageDoor(t)
	rec := putImage(t, h, true, "/api/v1/load/products/P-1/image", "image/png", bytes.Repeat([]byte("x"), 17))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "image") {
		t.Fatalf("an over cap body answered %d %q, want 400 naming the field image", rec.Code, rec.Body.String())
	}
	if w.reached {
		t.Fatalf("the over cap body reached the writer; the boundary must refuse before any store call")
	}
	// The refusal names the field, never a value: no body byte appears.
	if strings.Contains(rec.Body.String(), "xxxx") {
		t.Fatalf("the refusal echoed a value: %q", rec.Body.String())
	}
}

func TestImagePutRefusesATypeOutsideTheAllowlistNamingTheField(t *testing.T) {
	_, h, _ := testImageDoor(t)
	rec := putImage(t, h, true, "/api/v1/load/products/P-1/image", "image/svg+xml", []byte("bytes"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "content_type") {
		t.Fatalf("a type outside the allowlist answered %d %q, want 400 naming content_type", rec.Code, rec.Body.String())
	}
}

func TestImagePutRefusesToRunUnwired(t *testing.T) {
	door := NewImageDoor(NewStore(nil), nil, nil, nil)
	if _, err := door.PutImage(context.Background(), PutImageRequest{}); err == nil {
		t.Fatalf("an unwired door answered no error; it must refuse loudly at the first put")
	}
}
