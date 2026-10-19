package main

// cq_auditwrap_test.go: the S-1 serve-shaped regression (the Phase C fix
// round, Security finding S-1). The composition suites above probe
// rec.inner, the bare mux, which is exactly why the coarse audit
// middleware's token leak survived the lane: no suite drove the public
// accept pair THROUGH the production wrap the way serve wires it
// (main.go auditWrap: audit.Service.Middleware(bridgedAuthn{...}) around
// the whole mux). This suite does. Three probes, each carrying its own
// X-Request-Id so every audit row it can produce is addressable:
//
//   - an accept (200): the separator-encoded CUSTOMER_QUOTE_ACCEPTED row
//     lands AND the coarse HTTP.POST row lands with the elided path, and
//     NO row's persisted changes contain the raw token;
//   - a replay (409): the pinned sentence, one coarse row, elided, no
//     leak, and no second acceptance row;
//   - a 405 mis-post (POST to the view leg's path, no /accept suffix):
//     one coarse row, elided, no leak. Blind probes leak nothing either.
//
// The token elision itself is core/internal/audit's elidePublicToken
// (unit-pinned offline there); this suite is the composition proof that
// the wired surface cannot write the credential. Revert the middleware's
// elidePublicToken call and this suite fails on the leak (the red proof
// of the fix round).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
)

// cqWrapProbeBody is the public accept pair's typed-name-and-affirmative
// shape, the same body the x-role suite's probes carry.
var cqWrapProbeBody = `{"typed_name":"Fixture Homeowner","accepted":true}`

// cqSeparatorHex pins the separator-encoded hex shape (groups of six
// lowercase hex joined by colons; a 64-hex atom is ten full groups plus a
// final four), so the acceptance row is asserted to have landed encoded,
// never as a bare hash that the chokepoint could redact.
var cqSeparatorHex = regexp.MustCompile(`^([0-9a-f]{6}:){10}[0-9a-f]{4}$`)

// cqWrapRow is one audit_log row as this suite reads it: the action, the
// decoded changes map, and the raw changes text for the leak scan.
type cqWrapRow struct {
	action  string
	changes map[string]any
	raw     string
}

// TestPublicTokenPairThroughAuditWrapElidesRawToken drives the customer
// quote public accept pair through the audit middleware exactly as serve
// wires it and asserts the coarse trail never persists the credential.
func TestPublicTokenPairThroughAuditWrapElidesRawToken(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	tokens := plantClassificationTokens(t, env, graph.pool)

	// The serve shape, not the bare mux: the composition point's audit
	// service wrapping the whole registered surface with the bridged
	// authenticator, the same construction auditWrap performs.
	served := graph.money.auditSvc.Middleware(
		bridgedAuthn{verify: graph.verify, link: graph.link})(rec.inner)

	// The rows this test writes are its own: every probe mints a request
	// id under this prefix, and the cleanup deletes exactly those rows so
	// the fixture database is left as it was found.
	t.Cleanup(func() {
		_, err := graph.pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE request_id LIKE 'fx-cqwrap-%'`)
		if err != nil {
			t.Fatalf("cqwrap cleanup: %v", err)
		}
	})

	post := func(reqID, path string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(cqWrapProbeBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-Id", reqID)
		rsp := httptest.NewRecorder()
		served.ServeHTTP(rsp, req)
		return rsp.Code
	}

	// rowsFor polls for at least want rows under one request id (the
	// coarse insert is async, a goroutine off the request) and returns
	// them decoded for assertion.
	rowsFor := func(reqID string, want int) []cqWrapRow {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		var rows []cqWrapRow
		for {
			rows = fetchCQWrapRows(t, graph, reqID)
			if len(rows) >= want || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if len(rows) < want {
			t.Fatalf("request %s: want at least %d audit rows, got %d (%+v)", reqID, want, len(rows), rows)
		}
		return rows
	}

	// assertNoLeak fails when any row the request produced carries the
	// raw credential anywhere in its persisted changes.
	assertNoLeak := func(reqID string, rows []cqWrapRow, raw string) {
		t.Helper()
		for _, r := range rows {
			if strings.Contains(r.raw, raw) {
				t.Fatalf("request %s: the raw token persists in audit_log row %s: %s", reqID, r.action, r.raw)
			}
		}
	}

	// 1. The accept: 200, one encoded acceptance row plus one coarse row,
	// neither carrying the credential.
	acceptID := "fx-cqwrap-accept"
	if code := post(acceptID,
		"/api/public/v1/customer-quotes/"+tokens.liveAccept+"/accept"); code != http.StatusOK {
		t.Fatalf("accept: status %d, want 200", code)
	}
	rows := rowsFor(acceptID, 2)
	assertNoLeak(acceptID, rows, tokens.liveAccept)
	var acceptance, coarse *cqWrapRow
	for i := range rows {
		switch rows[i].action {
		case customerquote.AuditAction:
			acceptance = &rows[i]
		case audit.ActHTTPPost:
			coarse = &rows[i]
		}
	}
	if acceptance == nil || coarse == nil {
		t.Fatalf("accept: want one %s row and one coarse %s row, got %+v",
			customerquote.AuditAction, audit.ActHTTPPost, rows)
	}
	if h, _ := acceptance.changes[customerquote.ExtraArtifactHash].(string); !cqSeparatorHex.MatchString(h) {
		t.Errorf("acceptance artifact_hash %q is not separator-encoded hex", h)
	}
	if h, _ := acceptance.changes[customerquote.ExtraTokenHash].(string); !cqSeparatorHex.MatchString(h) {
		t.Errorf("acceptance token_hash %q is not separator-encoded hex", h)
	}
	if got, _ := coarse.changes["path"].(string); got != "/api/public/v1/customer-quotes/{token}/accept" {
		t.Errorf("coarse path = %q, want the elided spelling", got)
	}
	if got, _ := coarse.changes["status"].(float64); int(got) != http.StatusOK {
		t.Errorf("coarse status = %v, want %d", coarse.changes["status"], http.StatusOK)
	}

	// 2. The replay: the pinned 409, one coarse row, no second acceptance
	// row, no leak.
	replayID := "fx-cqwrap-replay"
	if code := post(replayID,
		"/api/public/v1/customer-quotes/"+tokens.liveAccept+"/accept"); code != http.StatusConflict {
		t.Fatalf("replay: status %d, want 409", code)
	}
	rows = rowsFor(replayID, 1)
	assertNoLeak(replayID, rows, tokens.liveAccept)
	for _, r := range rows {
		if r.action == customerquote.AuditAction {
			t.Fatalf("replay: a second %s row landed", customerquote.AuditAction)
		}
	}
	if got, _ := rows[0].changes["path"].(string); got != "/api/public/v1/customer-quotes/{token}/accept" {
		t.Errorf("replay coarse path = %q, want the elided spelling", got)
	}
	if got, _ := rows[0].changes["status"].(float64); int(got) != http.StatusConflict {
		t.Errorf("replay coarse status = %v, want %d", rows[0].changes["status"], http.StatusConflict)
	}

	// 3. The 405 mis-post: POST against the view leg's path (no /accept
	// suffix). The mux refuses the method, the coarse row still lands, and
	// the blind probe's token-bearing URL still never persists.
	misPostID := "fx-cqwrap-mispost"
	if code := post(misPostID,
		"/api/public/v1/customer-quotes/"+tokens.liveView); code != http.StatusMethodNotAllowed {
		t.Fatalf("mis-post: status %d, want 405", code)
	}
	rows = rowsFor(misPostID, 1)
	assertNoLeak(misPostID, rows, tokens.liveView)
	if got, _ := rows[0].changes["path"].(string); got != "/api/public/v1/customer-quotes/{token}" {
		t.Errorf("mis-post coarse path = %q, want the elided spelling", got)
	}

	// 4. The HH-693 deny-by-default probe: a POST to an unmounted future
	// public token family. The mux 404s the path, the coarse row still
	// lands, and the elision is deny-by-default over /api/public/: the
	// unlisted family collapses to the version-level placeholder, never
	// the raw credential, so a new route cannot ship the S-1 leak by
	// default.
	futureID := "fx-cqwrap-future"
	if code := post(futureID,
		"/api/public/v1/unlisted-family/"+tokens.liveView+"/accept"); code != http.StatusNotFound {
		t.Fatalf("future family: status %d, want 404", code)
	}
	rows = rowsFor(futureID, 1)
	assertNoLeak(futureID, rows, tokens.liveView)
	if got, _ := rows[0].changes["path"].(string); got != "/api/public/v1/{token}/accept" {
		t.Errorf("future family coarse path = %q, want the elided spelling", got)
	}

	// 5. The P2-B doubled-slash probe: the audit wrap sits outside the
	// mux, so "/api//public/..." reaches the middleware before the mux
	// cleans it. The row must still record the elided spelling, and no
	// column (changes, entity_type, entity_id) may carry the credential.
	doubledID := "fx-cqwrap-doubled"
	if code := post(doubledID,
		"/api//public/v1/customer-quotes/"+tokens.liveAccept+"/accept"); code != http.StatusTemporaryRedirect {
		t.Fatalf("doubled slash: status %d, want 307", code)
	}
	rows = rowsFor(doubledID, 1)
	assertNoLeak(doubledID, rows, tokens.liveAccept)
	if got, _ := rows[0].changes["path"].(string); got != "/api/public/v1/customer-quotes/{token}/accept" {
		t.Errorf("doubled slash coarse path = %q, want the elided spelling", got)
	}

	// 6. The P2-B bare-token probe: a mis-POST whose credential sits
	// directly after /api/public/. The path elides and the derived
	// entity_type carries no fragment of the credential.
	bareID := "fx-cqwrap-bare"
	if code := post(bareID, "/api/public/"+tokens.liveView); code != http.StatusNotFound {
		t.Fatalf("bare token: status %d, want 404", code)
	}
	rows = rowsFor(bareID, 1)
	assertNoLeak(bareID, rows, tokens.liveView)
	if got, _ := rows[0].changes["path"].(string); got != "/api/public/{token}" {
		t.Errorf("bare token coarse path = %q, want the elided spelling", got)
	}
}

// fetchCQWrapRows reads one request id's audit rows: action, entity_type,
// entity_id, and changes as a decoded map plus the same three columns as raw
// text for the leak scan, so assertNoLeak inspects the whole persisted row,
// not just changes.
func fetchCQWrapRows(t *testing.T, graph *appGraph, reqID string) []cqWrapRow {
	t.Helper()
	rs, err := graph.pool.Query(context.Background(),
		`SELECT action, entity_type, COALESCE(entity_id::text, ''), changes::text
		 FROM audit_log WHERE request_id = $1`, reqID)
	if err != nil {
		t.Fatalf("audit rows query: %v", err)
	}
	defer rs.Close()
	out := []cqWrapRow{}
	for rs.Next() {
		var r cqWrapRow
		var entityType, entityID string
		if err := rs.Scan(&r.action, &entityType, &entityID, &r.raw); err != nil {
			t.Fatalf("audit rows scan: %v", err)
		}
		_ = json.Unmarshal([]byte(r.raw), &r.changes)
		r.raw = r.raw + " " + entityType + " " + entityID
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatalf("audit rows iteration: %v", err)
	}
	return out
}
