package gl

// handler_pathid_test.go, regression coverage for the {id} path-param validation gate (sweep #46,
// finding #22). A malformed non-UUID {id} on the journal/period routes used to flow raw into a
// Postgres `WHERE id = $1` on a uuid PK, raising SQLSTATE 22P02 and surfacing as an opaque HTTP 500
// (and for the Exec-based writes it raised before any scan-map could catch it). The handlers now
// validate the UUID shape at the HTTP edge via pathID() and answer 400 'invalid id' BEFORE any DB
// call. These tests hit the handler methods directly over the DB-free fake-backed service, so a
// malformed id is rejected before the service/DB is reached; each case genuinely regresses the fix.

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validJournalUUID = "3f2504e0-4f89-41d3-9a0c-0305e82c3399"

// pathIDGate names each handler that reads {id} and flows it into a uuid PK predicate.
type pathIDGate struct {
	name   string
	method string
	call   func(h *Handler) http.HandlerFunc
}

func pathIDGates() []pathIDGate {
	return []pathIDGate{
		{"getJournal", http.MethodGet, func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.getJournal(w, r, guards.StaffCaller{}) }
		}},
		{"approveJournal", http.MethodPost, func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.approveJournal(w, r, guards.StaffCaller{}) }
		}},
		{"voidJournal", http.MethodPost, func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.voidJournal(w, r, guards.StaffCaller{}) }
		}},
		{"reverseJournal", http.MethodPost, func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.reverseJournal(w, r, guards.StaffCaller{}) }
		}},
		{"runRecurring", http.MethodPost, func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.runRecurring(w, r, guards.StaffCaller{}) }
		}},
		{"closePeriod", http.MethodPost, func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.closePeriod(w, r, guards.StaffCaller{}) }
		}},
		{"reopenPeriod", http.MethodPost, func(h *Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { h.reopenPeriod(w, r, guards.StaffCaller{}) }
		}},
	}
}

// A malformed {id} → 400 'invalid id' on every journal/period path route, BEFORE any DB call.
func TestPathID_Malformed_Returns400(t *testing.T) {
	f := newFakeStore()
	h := newTestHandler(f)
	for _, g := range pathIDGates() {
		for _, bad := range []string{"not-a-uuid", "123", "'; DROP TABLE gl_journals;--", validJournalUUID + "x"} {
			g := g
			bad := bad
			t.Run(g.name+"/"+bad, func(t *testing.T) {
				// A minimal valid JSON body so a route that decodes one (reopenPeriod) would still
				// reach decode, but pathID() runs first and must short-circuit before it.
				req := httptest.NewRequest(g.method, "/api/v1/gl/x", strings.NewReader(`{"reason":"r"}`))
				req.SetPathValue("id", bad)
				rr := httptest.NewRecorder()
				g.call(h)(rr, req)
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("%s bad id %q: want 400, got %d (body %s)", g.name, bad, rr.Code, rr.Body.String())
				}
				if msg := bodyMsg(rr.Body.Bytes()); msg != "invalid id" {
					t.Fatalf("%s bad id %q: want error 'invalid id', got %q", g.name, bad, msg)
				}
			})
		}
	}
}

// A valid UUID {id} passes the validation gate, it must NEVER be rejected with 400 'invalid id'
// (it may still fail downstream on the fake for unrelated not-found reasons, which is fine).
func TestPathID_ValidUUID_PassesGate(t *testing.T) {
	f := newFakeStore()
	h := newTestHandler(f)
	for _, g := range pathIDGates() {
		g := g
		t.Run(g.name, func(t *testing.T) {
			req := httptest.NewRequest(g.method, "/api/v1/gl/x", strings.NewReader(`{"reason":"r"}`))
			req.SetPathValue("id", validJournalUUID)
			rr := httptest.NewRecorder()
			g.call(h)(rr, req)
			if rr.Code == http.StatusBadRequest && bodyMsg(rr.Body.Bytes()) == "invalid id" {
				t.Fatalf("%s valid uuid: wrongly rejected by the id gate", g.name)
			}
		})
	}
}
