// Package apigen holds the server types and the route table generated from
// the contract (core/api/openapi.yaml) by core/api/tools/gen-types.mjs.
// The .gen.go files are regenerated, never hand-edited: run
//
//	go generate ./internal/platform/apigen
//
// from core (or node core/api/tools/gen-types.mjs --write from the repo
// root). The generator's default mode is a check: it fails when the
// committed files differ from what the document produces, and that check is
// wired into core/api/tools/check-all.mjs and ci.yml so document drift
// fails the suite rather than silently stale types.
//
// The types are the contract surface the core serves and decodes. The
// mapping is pinned in the generator's header comment; any document shape
// the mapping does not cover fails generation loudly instead of degrading
// to a weaker type.
package apigen

//go:generate node ../../../api/tools/gen-types.mjs --write
