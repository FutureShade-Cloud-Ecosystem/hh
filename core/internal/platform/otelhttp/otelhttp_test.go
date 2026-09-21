package otelhttp

// otelhttp_test.go proves the helper against a recording fake tracer
// provider (the otel API only; no SDK dependency, exactly like the
// package). Both halves of every rule it carries: a span per request
// including gate refusals, error status only from 5xx, nothing but the
// three pinned attributes (no query, no headers), and the
// ResponseController chain staying walkable.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// fakeSpan records what the middleware did to one span. The embedded.Span
// marker satisfies the interface's unexported-method requirement the way
// the otel API documents for outside implementations.
type fakeSpan struct {
	embedded.Span
	name       string
	attrs      []attribute.KeyValue
	ended      bool
	statusCode codes.Code
}

func (s *fakeSpan) End(...trace.SpanEndOption) { s.ended = true }

func (s *fakeSpan) AddEvent(string, ...trace.EventOption)   {}
func (s *fakeSpan) AddLink(trace.Link)                      {}
func (s *fakeSpan) RecordError(error, ...trace.EventOption) {}
func (s *fakeSpan) SetStatus(code codes.Code, _ string)     { s.statusCode = code }
func (s *fakeSpan) SetAttributes(kv ...attribute.KeyValue)  { s.attrs = append(s.attrs, kv...) }
func (s *fakeSpan) SetName(string)                          {}
func (s *fakeSpan) TracerProvider() trace.TracerProvider    { return nil }
func (s *fakeSpan) IsRecording() bool                       { return true }
func (s *fakeSpan) SpanContext() trace.SpanContext          { return trace.SpanContext{} }

// fakeProvider records every span the middleware opened.
type fakeProvider struct {
	embedded.TracerProvider
	spans []*fakeSpan
}

func (p *fakeProvider) Tracer(string, ...trace.TracerOption) trace.Tracer {
	return fakeTracer{p: p}
}

type fakeTracer struct {
	embedded.Tracer
	p *fakeProvider
}

func (t fakeTracer) Start(_ context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	s := &fakeSpan{name: name, attrs: append([]attribute.KeyValue(nil), cfg.Attributes()...)}
	t.p.spans = append(t.p.spans, s)
	return context.Background(), s
}

// oneRequest runs a single wrapped request against a handler.
func oneRequest(t *testing.T, tp trace.TracerProvider, handler http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	srv := Wrap(tp)(handler)
	req := httptest.NewRequest("GET", target, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestWrapOpensOneServerSpanPerRequest: name, kind, method and path
// attributes, and End called after the handler ran.
func TestWrapOpensOneServerSpanPerRequest(t *testing.T) {
	p := &fakeProvider{}
	rec := oneRequest(t, p, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, "/api/v1/things")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(p.spans) != 1 {
		t.Fatalf("opened %d spans, want 1", len(p.spans))
	}
	s := p.spans[0]
	if !s.ended {
		t.Fatal("span was not ended")
	}
	if s.name != "GET /api/v1/things" {
		t.Fatalf("span name = %q", s.name)
	}
	var status int
	for _, kv := range s.attrs {
		switch string(kv.Key) {
		case attrRequestMethod:
			if kv.Value.AsString() != "GET" {
				t.Fatalf("method attr = %v", kv.Value.AsString())
			}
		case attrRequestPath:
			if kv.Value.AsString() != "/api/v1/things" {
				t.Fatalf("path attr = %v", kv.Value.AsString())
			}
		case attrResponseStatus:
			status = int(kv.Value.AsInt64())
		}
	}
	if status != http.StatusNoContent {
		t.Fatalf("status attr = %d, want 204", status)
	}
}

// TestWrapRecordsRefusals, the reason the middleware sits above the gate:
// a refused request (the gate writes 403 or 503 itself) still gets its
// span, and only a 5xx marks the span error.
func TestWrapRecordsRefusals(t *testing.T) {
	for _, c := range []struct {
		code int
		want codes.Code
	}{
		{http.StatusForbidden, codes.Unset},
		{http.StatusUnauthorized, codes.Unset},
		{http.StatusServiceUnavailable, codes.Error},
		{http.StatusInternalServerError, codes.Error},
	} {
		p := &fakeProvider{}
		oneRequest(t, p, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.code)
		}, "/api/v1/things")
		if len(p.spans) != 1 {
			t.Fatalf("code %d: %d spans, want 1 (a refused request is still a span)", c.code, len(p.spans))
		}
		if p.spans[0].statusCode != c.want {
			t.Fatalf("code %d: span status = %v, want %v", c.code, p.spans[0].statusCode, c.want)
		}
	}
}

// TestWrapNeverRecordsQueryOrHeaders: exactly the three pinned attributes
// exist, so a token in the query string can never reach a span, and no
// header attribute is ever set.
func TestWrapNeverRecordsQueryOrHeaders(t *testing.T) {
	p := &fakeProvider{}
	oneRequest(t, p, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, "/api/v1/things?token=secret-value&cursor=x")
	if len(p.spans) != 1 {
		t.Fatalf("%d spans, want 1", len(p.spans))
	}
	for _, kv := range p.spans[0].attrs {
		switch string(kv.Key) {
		case attrRequestMethod, attrRequestPath, attrResponseStatus:
		default:
			t.Fatalf("unexpected attribute %q recorded", kv.Key)
		}
		if v := kv.Value.Emit(); v == "secret-value" || v == "token=secret-value" {
			t.Fatalf("the query string leaked into a span attribute: %q", v)
		}
	}
}

// TestWrapNilProviderUsesTheGlobal: a nil provider resolves to the global
// one at wrap time, so activation needs no provider argument.
func TestWrapNilProviderUsesTheGlobal(t *testing.T) {
	prev := otel.GetTracerProvider()
	p := &fakeProvider{}
	otel.SetTracerProvider(p)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	got := oneRequest(t, nil, func(w http.ResponseWriter, r *http.Request) {}, "/healthz")
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", got.Code)
	}
	if len(p.spans) != 1 {
		t.Fatalf("nil provider must use the global provider: %d spans", len(p.spans))
	}
}

// TestWrapKeepsResponseControllerWalkable: a handler below the middleware
// can still flush (the SSE case), because statusWriter implements Unwrap.
func TestWrapKeepsResponseControllerWalkable(t *testing.T) {
	flushed := false
	srv := Wrap(&fakeProvider{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		if err := rc.Flush(); err != nil {
			t.Fatalf("flush through the middleware failed: %v", err)
		}
		flushed = true
	}))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/stream", nil))
	if !flushed {
		t.Fatal("handler did not run")
	}
}
