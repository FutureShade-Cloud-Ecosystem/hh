//go:build integration

// dib451_pending_branch_integration_test.go, DIB-451 against real Postgres.
//
// The unit half (dib451_pending_branch_test.go) runs on the DB-free fake, which means it can prove
// the handler's CONTRACT but not the premise the contract rests on: that a non-UUID branch_id really
// does blow up in the driver, and that a branch with nothing pending really does come back clean.
// Both of those are claims about Postgres, so they are asserted against Postgres.
//
// This is the DIB-443 house pattern, and the reason for it is that without this file the unit suite
// is asserting a story about a 500 nobody reproduced, a guard with nothing behind it reads exactly
// like a guard that is still needed, and the next person to tidy the handler cannot tell them apart.
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5432/fixqol2?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/ -run DIB451 -count=1 -v

package inventory

import (
	"context"
	"encoding/json"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// TestIntegration_DIB451_NonUUIDBranchIDWouldBreakTheDriver pins the PREMISE of the fix rather than
// the fix: the repository, handed a branch NAME (or the reported one-character-short uuid), fails at
// the uuid cast. That failure is untyped, so writeErr can only call it "internal error", which is
// the 500 the report photographed. The handler-level grade is what stops it ever getting here; this
// asserts that the thing being stopped is real.
func TestIntegration_DIB451_NonUUIDBranchIDWouldBreakTheDriver(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	for _, raw := range []string{
		"Trenton",                             // the branch NAME
		"5d1b3f6e-0000-4000-9000-00000000000", // the reported typo: one character short
	} {
		if _, err := repo.PendingFulfilments(ctx, raw); err == nil {
			t.Fatalf("PendingFulfilments accepted %q as a branch id, the premise of DIB-451 no longer "+
				"holds, so the handler's 400 may be guarding nothing; re-check before deleting it", raw)
		} else if !strings.Contains(strings.ToLower(err.Error()), "uuid") {
			// Not fatal to the fix, but worth surfacing: if the failure has stopped being a cast
			// error the query has changed shape and this test is no longer watching what it thinks.
			t.Logf("note: %q failed with a non-uuid error: %v", raw, err)
		}
	}

	// And through the handler, the same input is a 400 that never reaches SQL.
	h := NewHandler(newIntSvc(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) },
		"/api/v1/inventory/pending-fulfilment?branch_id=5d1b3f6e-0000-4000-9000-00000000000")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a mistyped branch_id must be 400, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "branch_id") {
		t.Fatalf("the 400 must name the field, got %s", rec.Body)
	}
}

// TestIntegration_DIB451_RealBranchReadsCleanAndEmptyIsAList is the other half: a well-formed branch
// id goes all the way to Postgres and back, and a branch with nothing deferred answers 200 with an
// empty LIST, not a null, and not an error. Empty is never an error is a house rule, and it is only
// a rule if something checks it against the real query.
func TestIntegration_DIB451_RealBranchReadsCleanAndEmptyIsAList(t *testing.T) {
	pool := intPool(t)
	h := NewHandler(newIntSvc(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	// A branch uuid that is well-formed and matches nothing: the empty answer, guaranteed, without
	// depending on what the seed happens to have left pending at a real branch.
	const noSuchBranch = "00000000-0000-4000-9000-0000000000ff"
	rec := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/pending-fulfilment?branch_id="+noSuchBranch)
	if rec.Code != http.StatusOK {
		t.Fatalf("a well-formed branch id with nothing pending must be 200, got %d: %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body)
	}
	if got := strings.TrimSpace(string(raw["pending"])); got != "[]" {
		t.Fatalf("pending = %s, want [], a null is a different claim to a client that does not "+
			"defend itself, which is the exact ambiguity DIB-443 was about", got)
	}

	// The canonical form is what reaches SQL: the braced and shouted spellings read identically to
	// the plain one, through the real query rather than through a stub that records a string.
	plain := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/pending-fulfilment?branch_id="+branch1).Body.String()
	for _, spelling := range []string{
		"%7B" + branch1 + "%7D", // {uuid}
		strings.ToUpper(branch1),
		branch1 + "%20", // a trailing space off a copied cell
	} {
		other := get(t, func(w http.ResponseWriter, r *http.Request) { h.pendingFulfilment(w, r, guards.StaffCaller{}) }, "/api/v1/inventory/pending-fulfilment?branch_id="+spelling)
		if other.Code != http.StatusOK {
			t.Fatalf("%q: want 200, got %d: %s", spelling, other.Code, other.Body)
		}
		if other.Body.String() != plain {
			t.Fatalf("%q read differently from the plain spelling:\n  %s  %s", spelling, other.Body, plain)
		}
	}
}
