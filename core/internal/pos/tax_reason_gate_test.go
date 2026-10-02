package pos

// tax_reason_gate_test.go, the DB-free pin for the ring's 400: the writeErr arm must map the unknown
// tax reason sentinel, as the composition's adapter wraps it, to the counter-readable 400 carrying the
// engine's words (the offending reason). The sentinel does not exist on main, so the pin lands with the
// fix; its bite is proven by dropping the writeErr arm alone (then the sentinel falls to the default
// and the pin reads the bare 500).

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteErr_UnknownTaxReasonAnswers400(t *testing.T) {
	h := NewHandler(NewService(NewRepository(nil)), slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", nil)
	h.writeErr(rec, req, fmt.Errorf("%w: unknown tax reason %q: not one of the lawful reason codes the counter can ring",
		ErrUnknownTaxReason, "FROB_NOMINAL_RELIEF"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the unknown tax reason must answer the counter 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "FROB_NOMINAL_RELIEF") {
		t.Fatalf("the 400 must carry the engine's words, the offending reason: %s", rec.Body.String())
	}
}

// The sentinel stays a refusal even when it arrives bare (an adapter shape change must not turn it
// into a 500), and errors.Is, not equality, is what the arm judges: the wrap is the carrier.
func TestWriteErr_UnknownTaxReasonBareStill400(t *testing.T) {
	h := NewHandler(NewService(NewRepository(nil)), slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", nil)
	h.writeErr(rec, req, ErrUnknownTaxReason)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a bare sentinel must still answer 400 via errors.Is, got %d", rec.Code)
	}
}
