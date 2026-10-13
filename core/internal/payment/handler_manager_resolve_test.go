package payment

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// The resolve route: POST /api/v1/payments/card-attempts/{id}/resolve. It is
// the manager's close of an attempt that needed a person: reason-required,
// carried by the store's Resolve (the guarded move to RESOLVED), and audited
// with the caller's user id. The tests share the list file's fakeAttemptStore
// so no database is needed for the RBAC, the shape and the refusals.

const resolveBody = `{"resolution":"NOT_CHARGED","note":"checked at the processor: no capture exists"}`

func TestCardAttempts_Resolve_RBAC(t *testing.T) {
	fake := &fakeAttemptStore{
		byID:          map[string]Attempt{listFixture.ID: listFixture},
		resolveResult: listFixture,
		resolveMoved:  true,
	}
	sg, mux := newManagerTestMux(t, fake)
	path := "/api/v1/payments/card-attempts/" + listFixture.ID + "/resolve"

	// A tier 3 caller is refused the manager route: the RBAC 403.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "cashier", Tier: 3, Scope: "branch"})
	if rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody); rec.Code != http.StatusForbidden {
		t.Fatalf("cashier resolve = %d, want 403", rec.Code)
	}
	if len(fake.resolveCalls) != 0 {
		t.Fatalf("Resolve calls after a refused caller = %d, want 0", len(fake.resolveCalls))
	}

	// A tier 2 caller passes RequireManager: 200.
	managerAuth(sg)
	if rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody); rec.Code != http.StatusOK {
		t.Fatalf("manager resolve = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}

func TestCardAttempts_Resolve_SuccessShapeAndActor(t *testing.T) {
	resolved := listFixture
	resolved.Status = AttemptResolved
	resolved.Resolution = ResolutionNotCharged
	resolved.ResolutionNote = "checked at the processor: no capture exists"
	fake := &fakeAttemptStore{
		byID:          map[string]Attempt{listFixture.ID: listFixture},
		resolveResult: resolved,
		resolveMoved:  true,
	}
	sg, mux := newManagerTestMux(t, fake)
	managerAuth(sg)
	path := "/api/v1/payments/card-attempts/" + listFixture.ID + "/resolve"

	rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("manager resolve = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// The body is the resolved row's summary, the list row's own shape.
	var row cardAttemptRow
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatalf("the body must be the resolved row's summary: %v (%s)", err, rec.Body.String())
	}
	if row.ID != resolved.ID || row.Status != AttemptResolved {
		t.Fatalf("summary = %s/%s, want %s/%s", row.ID, row.Status, resolved.ID, AttemptResolved)
	}

	// The close carried exactly what the route was given, and the actor is
	// the caller's own user id (the audit fact), the linker's answer.
	if len(fake.resolveCalls) != 1 {
		t.Fatalf("Resolve calls = %d, want 1", len(fake.resolveCalls))
	}
	call := fake.resolveCalls[0]
	if call.id != listFixture.ID {
		t.Fatalf("resolve id = %q, want the path's %q", call.id, listFixture.ID)
	}
	if call.resolution != ResolutionNotCharged {
		t.Fatalf("resolve resolution = %q, want %q", call.resolution, ResolutionNotCharged)
	}
	if call.note != "checked at the processor: no capture exists" {
		t.Fatalf("resolve note = %q, want the body's note", call.note)
	}
	if call.actor != "users-1111" {
		t.Fatalf("resolve actor = %q, want the caller's linked users.id", call.actor)
	}
}

// The close is reason-required: a body without a known resolution or without
// a note is the client's error, refused before the store, so nothing moves.
func TestCardAttempts_Resolve_RequiresReason(t *testing.T) {
	fake := &fakeAttemptStore{byID: map[string]Attempt{listFixture.ID: listFixture}}
	sg, mux := newManagerTestMux(t, fake)
	managerAuth(sg)
	path := "/api/v1/payments/card-attempts/" + listFixture.ID + "/resolve"

	cases := []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"missing resolution", `{"note":"checked"}`},
		{"missing note", `{"resolution":"NOT_CHARGED"}`},
		{"empty note", `{"resolution":"NOT_CHARGED","note":""}`},
		{"blank note", `{"resolution":"NOT_CHARGED","note":"   "}`},
		{"unknown resolution", `{"resolution":"WHATEVER","note":"checked"}`},
		{"not json", `not json at all`},
		{"no body", ``},
	}
	for _, c := range cases {
		rec := doReq(t, sg, mux, http.MethodPost, path, c.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: resolve = %d, want 400 (%s)", c.name, rec.Code, rec.Body.String())
		}
	}
	if len(fake.resolveCalls) != 0 {
		t.Fatalf("Resolve calls after the refusals = %d, want 0 (a refused close stores nothing)", len(fake.resolveCalls))
	}
}

// An unknown id answers 404 and reaches no resolve move: the route reads the
// row first, so a mistyped id can never be answered as if the row decided.
func TestCardAttempts_Resolve_UnknownID(t *testing.T) {
	fake := &fakeAttemptStore{byID: map[string]Attempt{}}
	sg, mux := newManagerTestMux(t, fake)
	managerAuth(sg)

	rec := doReq(t, sg, mux, http.MethodPost, "/api/v1/payments/card-attempts/00000000-0000-0000-0000-00000000abcd/resolve", resolveBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id resolve = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no card attempt with that id") {
		t.Fatalf("the 404 must name the miss, got %s", rec.Body.String())
	}
	if len(fake.resolveCalls) != 0 {
		t.Fatalf("Resolve calls for an unknown id = %d, want 0", len(fake.resolveCalls))
	}
}

// A bound attempt already has its sale: the close is a conflict, not a move.
func TestCardAttempts_Resolve_Bound(t *testing.T) {
	bound := listFixture
	bound.Status = AttemptBound
	fake := &fakeAttemptStore{byID: map[string]Attempt{bound.ID: bound}}
	sg, mux := newManagerTestMux(t, fake)
	managerAuth(sg)
	path := "/api/v1/payments/card-attempts/" + bound.ID + "/resolve"

	rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("bound resolve = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not open to a resolve") {
		t.Fatalf("the 409 must name the conflict, got %s", rec.Body.String())
	}
	if len(fake.resolveCalls) != 0 {
		t.Fatalf("Resolve calls for a bound row = %d, want 0 (the guard is read before the move)", len(fake.resolveCalls))
	}
}

// The store's own refusal (the guarded move matched nothing, the row moved
// between the route's read and the store's move) answers the conflict too,
// never a 500 and never a silent 200.
func TestCardAttempts_Resolve_StoreGuardLost(t *testing.T) {
	fake := &fakeAttemptStore{
		byID:         map[string]Attempt{listFixture.ID: listFixture},
		resolveMoved: false,
	}
	sg, mux := newManagerTestMux(t, fake)
	managerAuth(sg)
	path := "/api/v1/payments/card-attempts/" + listFixture.ID + "/resolve"

	rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("lost guard resolve = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// The pairing rule: a row whose capture was confirmed before its reversal was
// lost or refused (VOID_UNKNOWN, VOID_DECLINED) cannot truthfully close as
// NOT_CHARGED. The route refuses it before any move is attempted.
func TestCardAttempts_Resolve_PairingRefused(t *testing.T) {
	for _, status := range []string{AttemptVoidUnknown, AttemptVoidDeclined} {
		reversed := listFixture
		reversed.Status = status
		fake := &fakeAttemptStore{byID: map[string]Attempt{reversed.ID: reversed}}
		sg, mux := newManagerTestMux(t, fake)
		managerAuth(sg)
		path := "/api/v1/payments/card-attempts/" + reversed.ID + "/resolve"

		rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s close as NOT_CHARGED = %d, want 400 (%s)", status, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "a reversed attempt closes as REVERSED_AT_PROCESSOR") {
			t.Fatalf("the refusal must name the pairing rule, got %s", rec.Body.String())
		}
		if len(fake.resolveCalls) != 0 {
			t.Fatalf("Resolve calls for a refused pairing = %d, want 0", len(fake.resolveCalls))
		}
	}
}

// The resolve is fenced by the caller's branch reach (the item 3 review, P2-2): a branch scoped manager
// resolves attempts of its own branches only. Another branch's attempt answers exactly as an unknown id
// (404, no branch named) and nothing moves; a global role and a manager granted the branch are admitted.
func TestCardAttempts_Resolve_FencedByBranchReach(t *testing.T) {
	const otherBranch = "a0a0a0a0-0000-0000-0000-00000000000a"
	fake := &fakeAttemptStore{
		byID:          map[string]Attempt{listFixture.ID: listFixture},
		resolveResult: listFixture,
		resolveMoved:  true,
	}
	sg, mux := newManagerTestMux(t, fake)
	path := "/api/v1/payments/card-attempts/" + listFixture.ID + "/resolve"

	// A branch manager of another branch: refused as a missing id, nothing moved.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{otherBranch}})
	unknown := doReq(t, sg, mux, http.MethodPost, "/api/v1/payments/card-attempts/00000000-0000-0000-0000-00000000abcd/resolve", resolveBody)
	rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody)
	if rec.Code != http.StatusNotFound || rec.Body.String() != unknown.Body.String() {
		t.Fatalf("another branch's attempt = %d %q, want the unknown id answer %d %q", rec.Code, rec.Body.String(), unknown.Code, unknown.Body.String())
	}
	if strings.Contains(rec.Body.String(), listFixture.BranchID) {
		t.Fatalf("the refusal must not name the branch: %s", rec.Body.String())
	}
	if len(fake.resolveCalls) != 0 {
		t.Fatalf("Resolve calls after a refused branch = %d, want 0", len(fake.resolveCalls))
	}

	// The same person granted two branches, one of them the attempt's: admitted.
	sg.SetAuthority(identity.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "branch", Branches: []string{otherBranch, listFixture.BranchID}})
	if rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody); rec.Code != http.StatusOK {
		t.Fatalf("manager granted the attempt's branch = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// A global role is admitted whatever the branch.
	managerAuth(sg)
	if rec := doReq(t, sg, mux, http.MethodPost, path, resolveBody); rec.Code != http.StatusOK {
		t.Fatalf("global role = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(fake.resolveCalls) != 2 {
		t.Fatalf("Resolve calls = %d, want 2 (the two admitted callers)", len(fake.resolveCalls))
	}
}
