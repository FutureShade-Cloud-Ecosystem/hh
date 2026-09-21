// Package otelhttp is the serve role's one OpenTelemetry surface: a
// middleware constructor that opens a server span around every request,
// including the refusals the authz gate writes. OpenTelemetry enters the
// core here and in the pool tracer hook only (the plan's placement: helper
// in the foundations batch, activation on the serve role when the
// composition root lands): the ERP carried no direct otel usage, so
// telemetry is platform wiring, never a domain-module concern, and slog
// stays the logging API.
//
// THE ORDERING CONTRACT (the zero-statement proofs depend on it):
// activation wraps the mux from the OUTSIDE, above the authz gate wrapping,
// never between the gate and a handler. Two reasons. A span per refused
// request is the point (the sweeps count statements per refused request
// with a tracer on the pool, and the middleware's spans must never be
// mistaken for pool statements); and the traced pool construction stays the
// test-only variant it is (serve constructs its pool with a nil tracer at
// cmd/core/main.go), because a production traced pool would add statement
// spans under every request and the refusals would no longer read zero.
// This package itself opens spans and reads nothing else: no database, no
// headers, no query string.
package otelhttp

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// tracerName names the tracer this middleware requests, so spans it opens
// are attributable to exactly this package in any backend that exports them.
const tracerName = "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/otelhttp"

// Semantic-convention attribute keys, stated as literals so the package
// pulls only the otel API surface. Exactly these three are ever set: the
// request method, the request path, and the response status. The raw query
// is deliberately NEVER recorded (a query string can carry a token), and no
// header is ever recorded.
const (
	attrRequestMethod  = "http.request.method"
	attrRequestPath    = "url.path"
	attrResponseStatus = "http.response.status_code"
)

// Wrap returns a middleware that opens a server span around every request
// handled by next. A nil provider resolves to the global one
// (otel.GetTracerProvider), which is a no-op until something installs a
// real provider, so wiring this before any exporter configured its provider
// costs one no-op span context per request and records nothing.
func Wrap(tp trace.TracerProvider) func(http.Handler) http.Handler {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	tracer := tp.Tracer(tracerName)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String(attrRequestMethod, r.Method),
					attribute.String(attrRequestPath, r.URL.Path),
				))
			defer span.End()
			rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))
			span.SetAttributes(attribute.Int(attrResponseStatus, rec.status))
			if rec.status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(rec.status))
			}
		})
	}
}

// statusWriter captures the response status for the span without buffering
// the body. Unwrap exposes the wrapped writer so http.NewResponseController
// can walk the chain to the real writer's Flusher and deadlines: without
// it, a handler that needs to flush (an SSE stream) would silently fail on
// every request through this middleware.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Unwrap satisfies the http.ResponseController chain walk.
func (sw *statusWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }
