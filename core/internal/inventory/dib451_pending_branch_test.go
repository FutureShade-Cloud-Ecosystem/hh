package inventory

// dib451_pending_branch_test.go, DIB-451: ?branch_id= on the pending-fulfilment read.
//
// THE DEFECT THIS ENCODES
// -----------------------
// GET /api/v1/inventory/pending-fulfilment took the raw query value and handed it to the service,
// which handed it to a query that casts it to `uuid`. A branch id with one character fat-fingered off
// the end therefore died in the driver, came back as an untyped pgx error, and writeErr, correctly,
// given what it was handed, answered HTTP 500 {"error":"internal error"}.
//
// That is DIB-443's defect on the other id. It is worth fixing twice for the reason DIB-443 gave: 500
// has to keep meaning "the server actually failed", because that is the only thing that lets a surface
// say "fix your input" for one case and "this is not your fault" for the other. A 500 that a user can
// cause with a typo makes every real 500 less believable.
//
// MUTATION TARGETS. Each of these dies on a specific plausible edit, not on "something changed":
//
//	drop the uuid.Parse grade in branchIDParam       -> TestPendingFulfilment_NonUUIDBranchIDIs400 (500 again)
//	drop the TrimSpace                               -> TestPendingFulfilment_TrimsAndCanonicalises
//	pass the raw value instead of id.String()        -> TestPendingFulfilment_TrimsAndCanonicalises
//	treat an EMPTY branch_id as an error             -> TestPendingFulfilment_NoBranchIDIsEveryBranch
//	map a real store failure to 400                  -> TestPendingFulfilment_RealFailureIsStill500
//	return the nil slice instead of []               -> TestPendingFulfilment_EmptyIsListNotNull
//
// The premise underneath all of it, that a non-UUID branch_id really does break the driver, so the
// grade is guarding something, is asserted against Postgres in dib451_pending_branch_integration_test.go.
// Without that file this one is a story about a 500 nobody reproduced (the DIB-443 house rule).

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// stubPending answers the pending-fulfilment seam with canned rows and records the branch id the
// handler passed down, which is how the trim/canonicalisation assertions see through to SQL.
type stubPending struct {
	*fakeStore
	rows      []PendingFulfilment
	err       error
	gotBranch string
	calls     int
}

func (s *stubPending) PendingFulfilments(_ context.Context, branchID string) ([]PendingFulfilment, error) {
	s.calls++
	s.gotBranch = branchID
	return s.rows, s.err
}

func newPendingHandler(st *stubPending) *Handler {
	return NewHandler(NewService(NewRepository(nil)).withStore(st),
		slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
}

// - the typo: 400, not 500 -

func TestPendingFulfilment_NonUUIDBranchIDIs400(t *testing.T) {
	// Every one of these is something a person actually types, pastes or leaves in a bookmarked URL.
	for _, bad := range []string{
		"Trenton",                               // the branch NAME, which is what a human knows
		"TRT",                                   // the branch code
		"5d1b3f6e-0000-4000-9000-00000000000",   // one character short, the reported typo
		"5d1b3f6e-0000-4000-9000-000000000001x", // one character long
		"1",                                     // a row number from somewhere else
		"'; DROP TABLE stock_moves; --",         // and the one that must never reach a cast
	} {
		t.Run(bad, func(t *testing.T) {
			st := &stubPending{fakeStore: newFakeStore()}
			h := newPendingHandler(st)

			rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/pending-fulfilment?branch_id="+urlQ(bad))
			if rec.Code == http.StatusInternalServerError {
				t.Fatalf("a non-UUID branch_id still answers 500, that is DIB-451: the user's typo is "+
					"reported as the server breaking, and no surface can tell it from a real outage (%s)", rec.Body)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400 for branch_id=%q, got %d: %s", bad, rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), "branch_id") {
				t.Fatalf("the 400 must say which field is wrong, got %s", rec.Body)
			}
			// And nothing reached the store: a refused id never becomes SQL, which is what makes the
			// injection case above uninteresting rather than merely escaped.
			if st.calls != 0 {
				t.Fatalf("a rejected branch_id was still passed to the store as %q", st.gotBranch)
			}
		})
	}
}

// - empty is never an error -

func TestPendingFulfilment_NoBranchIDIsEveryBranch(t *testing.T) {
	// The house rule, and the surface depends on it: <hos-inventory-pending> calls this read with no
	// branch at all. Grading the OPTIONAL filter as required would take the whole pane down.
	for name, url := range map[string]string{
		"omitted":         "/api/v1/inventory/pending-fulfilment",
		"present-empty":   "/api/v1/inventory/pending-fulfilment?branch_id=",
		"whitespace-only": "/api/v1/inventory/pending-fulfilment?branch_id=%20%20",
	} {
		t.Run(name, func(t *testing.T) {
			st := &stubPending{fakeStore: newFakeStore()}
			h := newPendingHandler(st)
			rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) }, url)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s branch_id means every branch and must answer 200, got %d: %s", name, rec.Code, rec.Body)
			}
			if st.calls != 1 {
				t.Fatalf("the read must still run once for the all-branches case, ran %d times", st.calls)
			}
			if st.gotBranch != "" {
				t.Fatalf("the store received branch %q, want the empty all-branches filter", st.gotBranch)
			}
		})
	}
}

func TestPendingFulfilment_EmptyIsListNotNull(t *testing.T) {
	// A branch with nothing pending is the ANSWER to the question, not a failure, and `{"pending":
	// null}` is a different claim to a consumer that does not write `?? []` than `{"pending":[]}` is.
	// "The field was absent" is exactly the ambiguity DIB-443 was about; the same handler family must
	// not reintroduce it one endpoint over.
	st := &stubPending{fakeStore: newFakeStore()} // no rows, no error
	h := newPendingHandler(st)

	rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/pending-fulfilment?branch_id="+someBranch)
	if rec.Code != http.StatusOK {
		t.Fatalf("a branch with nothing pending must answer 200, got %d: %s", rec.Code, rec.Body)
	}
	// The raw body, not a decode into []T, a decode cannot tell null from [].
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body)
	}
	if got := strings.TrimSpace(string(raw["pending"])); got != "[]" {
		t.Fatalf("pending = %s, want []", got)
	}
}

// - the paste, and the shape that reaches SQL -

func TestPendingFulfilment_TrimsAndCanonicalises(t *testing.T) {
	// Copying a branch id out of a table or a chat message brings whitespace with it more often than
	// not, and uuid.Parse accepts spellings Postgres does not (urn:). One shape reaches SQL either
	// way, so the query plan and the logs never depend on how it was typed.
	//
	// THE PADDING MATTERS (the lesson dib443_lookup_test.go paid for): uuid.Parse switches on LENGTH,
	// and at 36+2 it strips the first and last characters assuming braces, without checking. A
	// symmetrically padded " <uuid>\n" is 38 characters and parses by accident with no trimming at
	// all. The one-sided pastes below are 37 characters, where the trim is the only thing working.
	for _, spelling := range []string{
		"%20" + someBranch,                // a leading space (37)
		someBranch + "%20",                // a trailing space (37)
		someBranch + "%0A",                // a trailing newline, from a copied table cell (37)
		"%20" + someBranch + "%0A",        // and both, which the length switch would have rescued (38)
		urlQ("urn:uuid:" + someBranch),    // the urn spelling
		urlQ("{" + someBranch + "}"),      // the braced spelling
		urlQ(strings.ToUpper(someBranch)), // shouted
	} {
		st := &stubPending{fakeStore: newFakeStore()}
		h := newPendingHandler(st)
		rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/pending-fulfilment?branch_id="+spelling)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: a pasted branch id must be accepted, got %d: %s", spelling, rec.Code, rec.Body)
		}
		if st.gotBranch != someBranch {
			t.Fatalf("%q reached the store as %q, want the trimmed canonical %q", spelling, st.gotBranch, someBranch)
		}
	}
}

// - the error path that must SURVIVE the fix -

func TestPendingFulfilment_RealFailureIsStill500(t *testing.T) {
	// The point of DIB-451 is not "stop returning 500". It is "stop returning 500 for things that are
	// not failures". A store that genuinely fails must still say so, with a status the surface can
	// tell apart from a bad request, otherwise the fix has merely moved the lie.
	st := &stubPending{fakeStore: newFakeStore(), err: errors.New("connection reset by peer")}
	h := newPendingHandler(st)

	rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/pending-fulfilment?branch_id="+someBranch)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a real store failure must stay 500, got %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "connection reset") {
		t.Fatalf("the driver error leaked to the client: %s", rec.Body)
	}
}

// - the grade is shared, not copied -

func TestBranchIDParam_OneGradeForEveryReadThatTakesIt(t *testing.T) {
	// DIB-451 lifted the rule the moves handler had inlined into branchIDParam so both reads answer
	// the same way. A future third read that inlines its own copy is the drift this guards: the two
	// handlers must not disagree about what a branch id is.
	stMoves := &stubReads{fakeStore: newFakeStore()}
	stPending := &stubPending{fakeStore: newFakeStore()}

	moves := get(t, func(w http.ResponseWriter, r *http.Request) {
		newLookupHandler(stMoves).moves(w, r, guards.StaffCaller{})
	},
		"/api/v1/inventory/moves?product_id="+noHistoryProduct+"&branch_id=Trenton")
	pending := get(t, func(w http.ResponseWriter, r *http.Request) {
		newPendingHandler(stPending).pendingFulfilment(w, r, guards.StaffCaller{})
	},
		"/api/v1/inventory/pending-fulfilment?branch_id=Trenton")

	if moves.Code != pending.Code {
		t.Fatalf("the same bad branch_id answers %d on moves and %d on pending-fulfilment", moves.Code, pending.Code)
	}
	if moves.Body.String() != pending.Body.String() {
		t.Fatalf("the same bad branch_id is worded two ways:\n  moves:   %s  pending: %s", moves.Body, pending.Body)
	}
}
