package httpx

import "net/http"

// Registrar is the one-method registration seam every module's route
// registrar declares as its first parameter: the handle call the net/http
// mux exposes, named as a platform type so the recording registrars the
// parity tooling builds (and any test double) can stand in where a
// concrete *http.ServeMux is declared. Promoted to the platform package at
// the HH-3.2 fold per the B3 ruling (tools/org/outbox/tech-lead/
// c-b3-review/review-notes.md, items 1 and 2): real modules must not
// depend on identity/shims for a type, because shims carries the S-3
// removal obligation; the alias left beside the shims keeps old spellings
// compiling. *http.ServeMux satisfies the interface, so every caller
// passing a concrete mux compiles unchanged.
type Registrar interface {
	Handle(pattern string, h http.Handler)
}
