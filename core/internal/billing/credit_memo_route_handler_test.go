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
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// recordingRegistrar records the patterns the handler registers.
type recordingRegistrar struct{ patterns []string }

func (r *recordingRegistrar) Handle(pattern string, _ http.Handler) {
	r.patterns = append(r.patterns, pattern)
}

// TestCreditMemoRoute_RegistersTheCreditMemoPath proves the handler registers exactly one path, and
// that it is the credit memo's.
func TestCreditMemoRoute_RegistersTheCreditMemoPath(t *testing.T) {
	reg := &recordingRegistrar{}
	h := NewCreditMemoRouteHandler(NewCreditMemoService(NewInvoiceWriter(nil), slog.Default()))
	h.RegisterRoutes(reg, &guards.Guards{})
	if len(reg.patterns) != 1 {
		t.Fatalf("the handler registered %d routes, want the one credit memo path: %v", len(reg.patterns), reg.patterns)
	}
	if reg.patterns[0] != "POST /api/v1/ar/credit-memos" {
		t.Fatalf("the handler registered %q, want POST /api/v1/ar/credit-memos", reg.patterns[0])
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
