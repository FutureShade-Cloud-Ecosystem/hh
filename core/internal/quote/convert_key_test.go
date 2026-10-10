package quote

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A key reused for a different conversion is a 409 with its reason in words (FROM-PRINCIPAL 121).
func TestConvertKeyReusedIs409InWords(t *testing.T) {
	rec := httptest.NewRecorder()
	(&StaffHandler{}).writeErr(rec, ErrConvertKeyReused)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "different conversion") {
		t.Errorf("the refusal must say so in words: %s", rec.Body.String())
	}
}
