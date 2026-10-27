package billing

// credit_memo_route_handler_test.go: THE ROUTE'S OWN DOOR.
//
// The route test beside this one (credit_memo_route_test.go) checks the route in the SERVED route
// table, so it names no Go symbol of this feature and could run against a tree that had no route.
// This file is the other half: it is about the handler itself, the pattern it registers and the status
// each refusal answers with, so it lives in its own file and it names the feature's own types.
import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// recordingRegistrar records the patterns the handler registers.
type recordingRegistrar struct{ patterns []string }

func (r *recordingRegistrar) Handle(pattern string, _ http.Handler) {
	r.patterns = append(r.patterns, pattern)
}

// TestCreditMemoRoute_RegistersTheCreditMemoPath proves the handler registers exactly the credit
// memo's two paths, and that they are the credit's: the credit itself, and the quote that says what
// a credit against one invoice would come to (FW1-12 round 3).
func TestCreditMemoRoute_RegistersTheCreditMemoPath(t *testing.T) {
	reg := &recordingRegistrar{}
	h := NewCreditMemoRouteHandler(NewCreditMemoService(NewInvoiceWriter(nil), slog.Default()))
	h.RegisterRoutes(reg, &guards.Guards{})
	if len(reg.patterns) != 2 {
		t.Fatalf("the handler registered %d routes, want the credit path and its quote: %v", len(reg.patterns), reg.patterns)
	}
	if reg.patterns[0] != "POST /api/v1/ar/credit-memos" {
		t.Fatalf("the handler registered %q, want POST /api/v1/ar/credit-memos", reg.patterns[0])
	}
	if reg.patterns[1] != "POST /api/v1/ar/credit-memos/quote" {
		t.Fatalf("the handler registered %q, want POST /api/v1/ar/credit-memos/quote", reg.patterns[1])
	}
}

// TestCreditMemoRoute_RefusalsAnswerWithTheirStatus proves the refusal taxonomy: a caller who broke
// the request gets a 400 and words, not a 500 with a stack.
func TestCreditMemoRoute_RefusalsAnswerWithTheirStatus(t *testing.T) {
	h := NewCreditMemoRouteHandler(NewCreditMemoService(NewInvoiceWriter(nil), slog.Default()))
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"a broken request is a 400", httpx.BadRequestf("a credit needs a reason"), http.StatusBadRequest},
		{"nothing to credit is a 400", ErrNothingToInvoice, http.StatusBadRequest},
		{"an unconfigured database is a 503", errNoDB, http.StatusServiceUnavailable},
		{"the customer answer is a 404", errCreditMemoCustomerNotFound, http.StatusNotFound},
		{"anything else is a 500", errors.New("the bus fell over"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/ar/credit-memos", nil)
			h.writeErr(rec, req, c.err)
			if rec.Code != c.want {
				t.Fatalf("the refusal answered %d, want %d", rec.Code, c.want)
			}
		})
	}
}

// TestCreditMemoRoute_TheCustomerAnswerIsTheHouseBody proves the house's customer body, byte for
// byte: 404 and {"error":"customer not found"}. The pos and AR doors write this same body for an
// account the caller cannot reach, and the UX-89 walk reads it as the answer a door must give for
// an account of another branch and for an id that is no account.
func TestCreditMemoRoute_TheCustomerAnswerIsTheHouseBody(t *testing.T) {
	h := NewCreditMemoRouteHandler(NewCreditMemoService(NewInvoiceWriter(nil), slog.Default()))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ar/credit-memos", nil)
	h.writeErr(rec, req, errCreditMemoCustomerNotFound)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the customer answer wrote %d, want 404", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"customer not found"}` {
		t.Fatalf("the customer answer wrote %q, want the house's %q", got, `{"error":"customer not found"}`)
	}
}
