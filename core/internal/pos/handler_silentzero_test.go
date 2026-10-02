package pos

// handler_silentzero_test.go, the writeErr mapping pin (no database): the tax
// rail's refusal must reach the counter as the 503 it can act on, in the
// Error envelope's counter-readable words (the module prefix stripped), never
// a bare 500 internal error and never a 200 with a zero rate. The sentinel
// this pin names does not exist on main, so the pin lands with the fix; its
// bite is proven by removing the writeErr arm alone.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func TestWriteErr_TaxRailUnavailable_IsCounterReadable503(t *testing.T) {
	h := NewHandler(nil, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", nil)

	// A wrapped sentinel is the production shape: the adapter returns the
	// sentinel, a service or a seam may wrap it further, the mapping must
	// survive the wrap (errors.Is).
	h.writeErr(rec, req, fmt.Errorf("ring order: %w", ErrTaxRailUnavailable))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("the tax rail refusal must map to 503, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the error envelope: %v", err)
	}
	if want := httpx.CounterMessage(ErrTaxRailUnavailable.Error()); body.Error != want {
		t.Errorf("the 503 must carry the sentinel's own message, got %q, want %q", body.Error, want)
	}
	if !strings.Contains(body.Error, "not configured") || !strings.Contains(body.Error, "cannot be ringed") {
		t.Errorf("the 503's words must be the counter-readable shape (what failed, what it means for the ticket), got %q", body.Error)
	}
	if !errors.Is(ErrTaxRailUnavailable, ErrTaxRailUnavailable) {
		t.Fatal("sanity: the sentinel must match itself")
	}
}
