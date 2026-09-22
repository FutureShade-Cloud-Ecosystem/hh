package gl

// handler_branchid_test.go, regression coverage for the branch_id validation gate. A malformed
// ?branch_id= query param used to flow raw into a Postgres `$N::uuid` cast (repository.go
// TrialBalanceAsOf and its siblings), raising SQLSTATE 22P02 and surfacing as an opaque HTTP 500.
// The handlers now validate the UUID shape at the HTTP edge and answer 400 'invalid branch_id'.
//
// These tests hit the handler methods directly (no auth middleware) over a DB-free fake-backed
// service, so a malformed branch_id is rejected BEFORE any service/DB call. Without the validation
// gate every "bad branch_id → 400" case fails (the handler would call the service and answer 200 /
// 500 instead), so each case genuinely regresses the fix.
//
// The query-param reads are exercised by branchIDGates() below; export() is the POST sibling that
// takes branch_id in the JSON body and is covered by TestBranchID_Export_Body.

import (
	"bytes"
	"encoding/json"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// newTestHandler wires a Handler over the DB-free fake-backed service.
func newTestHandler(f *fakeStore) *Handler {
	return NewHandler(newTestService(f), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

const validBranchUUID = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"

// branchIDGate names each handler that accepts ?branch_id= and flows it into a `$N::uuid` cast.
type branchIDGate struct {
	name string
	path string
	call func(h *Handler) http.HandlerFunc
}

func branchIDGates() []branchIDGate {
	return []branchIDGate{
		{"trialBalance", "/api/v1/gl/trial-balance", func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.trialBalance(w, r, guards.StaffCaller{}) }
		}},
		{"tieOut", "/api/v1/gl/tie-out", func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.tieOut(w, r, guards.StaffCaller{}) }
		}},
		{"bistrackTieOut", "/api/v1/gl/bistrack-tie-out", func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.bistrackTieOut(w, r, guards.StaffCaller{}) }
		}},
		{"listJournals", "/api/v1/gl/journals", func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.listJournals(w, r, guards.StaffCaller{}) }
		}},
		{"policy", "/api/v1/gl/policy", func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.policy(w, r, guards.StaffCaller{}) }
		}},
	}
}

// bodyMsg pulls the httpx error "error" field out of a JSON body (best-effort).
func bodyMsg(b []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(b, &e)
	return e.Error
}

// A malformed branch_id → 400 'invalid branch_id' on every branch-filtering read, BEFORE any DB call.
func TestBranchID_Malformed_Returns400(t *testing.T) {
	f := newFakeStore()
	h := newTestHandler(f)
	for _, g := range branchIDGates() {
		for _, bad := range []string{"not-a-uuid", "123", "'; DROP TABLE gl_journals;--", validBranchUUID + "x"} {
			g := g
			bad := bad
			t.Run(g.name+"/"+bad, func(t *testing.T) {
				q := url.Values{"year": {"2026"}, "month": {"6"}, "branch_id": {bad}}
				req := httptest.NewRequest(http.MethodGet, g.path+"?"+q.Encode(), nil)
				rr := httptest.NewRecorder()
				g.call(h)(rr, req)
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("%s bad branch_id %q: want 400, got %d (body %s)", g.name, bad, rr.Code, rr.Body.String())
				}
				if msg := bodyMsg(rr.Body.Bytes()); msg != "invalid branch_id" {
					t.Fatalf("%s bad branch_id %q: want error 'invalid branch_id', got %q", g.name, bad, msg)
				}
			})
		}
	}
}

// export() is the POST sibling of the reads above: it accepts branch_id in the JSON body (not the
// query string) and feeds it into the same `($N = " OR branch_id = $N::uuid)` predicate
// (ExportRows). It shares the bug, a malformed branch_id used to reach the ::uuid cast and surface
// as HTTP 500, and now shares the fix: a malformed value → 400 'invalid branch_id' BEFORE any DB
// call, while a valid/empty value passes the gate and completes the export.
func TestBranchID_Export_Body(t *testing.T) {
	f := newFakeStore()
	h := newTestHandler(f)

	post := func(branchID string, set bool) *httptest.ResponseRecorder {
		body := map[string]any{"year": 2026, "month": 6}
		if set {
			body["branch_id"] = branchID
		}
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/gl/export", bytes.NewReader(raw))
		rr := httptest.NewRecorder()
		h.export(rr, req, guards.StaffCaller{})
		return rr
	}

	t.Run("malformed→400", func(t *testing.T) {
		for _, bad := range []string{"not-a-uuid", "123", "'; DROP TABLE gl_journals;--", validBranchUUID + "x"} {
			rr := post(bad, true)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("export bad branch_id %q: want 400, got %d (body %s)", bad, rr.Code, rr.Body.String())
			}
			if msg := bodyMsg(rr.Body.Bytes()); msg != "invalid branch_id" {
				t.Fatalf("export bad branch_id %q: want error 'invalid branch_id', got %q", bad, msg)
			}
		}
	})

	t.Run("valid_or_empty→ok", func(t *testing.T) {
		// empty (field set to "" and field omitted entirely) = all-branches; canonical UUID = one branch.
		cases := []struct {
			branch string
			set    bool
		}{{"", true}, {"", false}, {validBranchUUID, true}}
		for _, c := range cases {
			rr := post(c.branch, c.set)
			if rr.Code == http.StatusBadRequest && bodyMsg(rr.Body.Bytes()) == "invalid branch_id" {
				t.Fatalf("export branch_id %q (set=%v): wrongly rejected by UUID gate", c.branch, c.set)
			}
			if rr.Code != http.StatusOK {
				t.Fatalf("export branch_id %q (set=%v): want 200, got %d (body %s)", c.branch, c.set, rr.Code, rr.Body.String())
			}
		}
	})
}

// A valid or empty branch_id passes the validation gate (never 'invalid branch_id'). It may still
// fail downstream for unrelated reasons, but must NOT be rejected by the UUID gate.
func TestBranchID_ValidOrEmpty_PassesGate(t *testing.T) {
	f := newFakeStore()
	h := newTestHandler(f)
	for _, g := range branchIDGates() {
		for _, ok := range []string{"", validBranchUUID} {
			g := g
			ok := ok
			t.Run(g.name+"/"+ok, func(t *testing.T) {
				vals := url.Values{"year": {"2026"}, "month": {"6"}}
				if ok != "" {
					vals.Set("branch_id", ok)
				}
				req := httptest.NewRequest(http.MethodGet, g.path+"?"+vals.Encode(), nil)
				rr := httptest.NewRecorder()
				g.call(h)(rr, req)
				if rr.Code == http.StatusBadRequest && bodyMsg(rr.Body.Bytes()) == "invalid branch_id" {
					t.Fatalf("%s branch_id %q: wrongly rejected by UUID gate (400 invalid branch_id)", g.name, ok)
				}
			})
		}
	}
}
