package messaging

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// stubAuthn satisfies audit.Authenticator. A GET is non-mutating, so the middleware never calls Actor -
// the stub only exists to satisfy the signature.
type stubAuthn struct{}

func (stubAuthn) Actor(_ *http.Request) (string, string) { return "", "" }

// TestSSEFlush_ThroughMiddlewareChain assembles the handler stack EXACTLY as cmd/server/main.go does -
// auditSvc.Middleware(authn)(mux), and asserts an SSE flush reaches the client THROUGH the audit
// middleware's statusRecorder wrapper. statusRecorder implements neither http.Flusher nor (before 0075)
// Unwrap, so the old direct w.(http.Flusher) assertion failed on EVERY request through this chain (the
// diagnosed prod/staging bug). With statusRecorder.Unwrap() + http.NewResponseController, the flush walks
// to the real writer. This tests the CHAIN, not the type: any future wrapper that drops Unwrap re-breaks it.
func TestSSEFlush_ThroughMiddlewareChain(t *testing.T) {
	hub := NewSSEHub(nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/messaging/stream", func(w http.ResponseWriter, r *http.Request) {
		hub.Serve(w, r, "u-alice", nil)
	})
	// A GET never records, so the audit service's nil repo is never touched.
	auditSvc := audit.NewService(nil, nil)
	handler := auditSvc.Middleware(stubAuthn{})(mux)

	rec := httptest.NewRecorder() // implements http.Flusher
	// Pre-cancel the context so Serve writes the ready event, flushes, then returns from its select loop
	// immediately (deterministic, the ready event is written before the loop is entered).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/messaging/stream", nil).WithContext(ctx)

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotImplemented {
		t.Fatal("SSE flush failed through the middleware chain (statusRecorder.Unwrap missing?), degraded to 501")
	}
	if !strings.Contains(rec.Body.String(), "event: ready") {
		t.Fatalf("the ready event must flush through the chain, got body %q", rec.Body.String())
	}
}
