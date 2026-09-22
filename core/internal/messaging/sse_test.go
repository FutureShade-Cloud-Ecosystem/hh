package messaging

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// nonFlusherWriter is an http.ResponseWriter that deliberately does NOT implement http.Flusher,
// reproducing the staging condition where the writer behind Caddy/HTTP2 cannot flush. httptest's
// ResponseRecorder implements Flusher, so we wrap it and expose ONLY the ResponseWriter methods.
type nonFlusherWriter struct{ rec *httptest.ResponseRecorder }

func (w *nonFlusherWriter) Header() http.Header         { return w.rec.Header() }
func (w *nonFlusherWriter) Write(b []byte) (int, error) { return w.rec.Write(b) }
func (w *nonFlusherWriter) WriteHeader(code int)        { w.rec.WriteHeader(code) }

// TestSSEServe_NoFlusherDegradesGracefully is the regression for the 500 'streaming unsupported'
// defect. When the ResponseWriter cannot flush (Caddy/HTTP2 on staging), Serve must NOT answer 500
// (a hard server error that broke the hub); it must degrade to 501 Not Implemented so the client's
// existing short-poll of /unread takes over, exactly as it does when no hub is wired.
func TestSSEServe_NoFlusherDegradesGracefully(t *testing.T) {
	hub := NewSSEHub(nil, nil)
	rec := httptest.NewRecorder()
	w := &nonFlusherWriter{rec: rec} // no Flusher → the failing branch

	// Sanity: the wrapper genuinely hides Flusher (else the test proves nothing).
	if _, ok := any(w).(http.Flusher); ok {
		t.Fatal("test setup: wrapper must NOT implement http.Flusher")
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/messaging/stream", nil).
		WithContext(context.Background())
	hub.Serve(w, r, "u-alice", nil)

	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("no-flusher path must never return 500; got %d", rec.Code)
	}
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("no-flusher path should degrade to 501 Not Implemented, got %d", rec.Code)
	}
}
