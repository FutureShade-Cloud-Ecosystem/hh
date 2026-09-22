package branches

// caller_test.go, WAVE 0b, DEFECT 2: the branch list learns who is asking, and the payload the
// yard console will be built against.
//
// THE DEFECT, AS IT REACHED A BROWSER. GET /api/v1/branches answered EVERY loc_type='BRANCH' row to
// every staff caller. handler.list called svc.List(r.Context()) and never touched the StaffContext -
// identity.StaffContextFromContext was not called anywhere in this package, so a Kingston-only yard
// lead's branch picker offered Trenton, and the is_default flag beside it was the DEALER's default,
// which for that person names a yard they cannot reach. The picker therefore opened on nothing and
// the caller picked whichever row sorted first.
//
// The ruling copied here is one file over in the cockpit: "The branch list comes from the caller's
// GRANTS, never from a query parameter. A dashboard that let you ask for another branch's numbers by
// editing a URL would be a nicer-looking version of the thing branch scoping exists to prevent"
// (dashboard/handler.go:65-69).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const (
	idTrenton  = "5d1b3f6e-0000-4000-9000-000000000001"
	idKingston = "5d1b3f6e-0000-4000-9000-000000000002"
	idRetired  = "5d1b3f6e-0000-4000-9000-000000000003"
)

// threeYards is the dealer's whole list in code order, the order repository.go:95 returns.
// BEL is retired; TRT is the system default.
func threeYards() *fakeStore {
	return &fakeStore{
		defaultID: idTrenton,
		list: []Branch{
			{ID: idRetired, Code: "BEL", Name: "Belleville", IsActive: false},
			{ID: idKingston, Code: "KGN", Name: "Kingston", IsActive: true},
			{ID: idTrenton, Code: "TRT", Name: "Trenton", IsDefault: true, IsActive: true},
		},
	}
}

func codesOf(rows []Branch) []string {
	out := make([]string, 0, len(rows))
	for _, b := range rows {
		out = append(out, b.Code)
	}
	return out
}

func defaultCodeOf(t *testing.T, rows []Branch) string {
	t.Helper()
	got := ""
	n := 0
	for _, b := range rows {
		if b.IsDefault {
			n++
			got = b.Code
		}
	}
	if n > 1 {
		t.Fatalf("exactly one row may be flagged is_default; %d were: %v", n, codesOf(rows))
	}
	return got
}

// TestListFor_FencesTheDealersListToTheCallersOwnGrants is the three cases, and the middle one is
// the point, the same three internal/assist writes down for the same table
// (adapters/catalogue.go:296-298): global sees all; granted sees those; NO GRANTS SEES NOTHING.
//
// The third is asserted explicitly because it is the one a careless implementation gets wrong: an
// empty grant list looks exactly like a missing filter, and falling through to "everything" would
// hand the whole dealer's yard list to the caller with the least authority in the building.
func TestListFor_FencesTheDealersListToTheCallersOwnGrants(t *testing.T) {
	svc := NewService(threeYards())
	ctx := context.Background()

	all, err := svc.ListFor(ctx, CallerScope{AllBranches: true})
	if err != nil {
		t.Fatalf("ListFor(global): %v", err)
	}
	if got := codesOf(all); len(got) != 3 {
		t.Fatalf("a GLOBAL caller sees the dealer's whole list; got %v", got)
	}

	one, err := svc.ListFor(ctx, CallerScope{BranchIDs: []string{idKingston}})
	if err != nil {
		t.Fatalf("ListFor(scoped): %v", err)
	}
	if got := codesOf(one); len(got) != 1 || got[0] != "KGN" {
		t.Fatalf("a caller granted Kingston sees Kingston and nothing else; got %v", got)
	}

	none, err := svc.ListFor(ctx, CallerScope{})
	if err != nil {
		t.Fatalf("ListFor(no grants): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("NO GRANTS SEES NOTHING, an empty grant list must never fall through to everything; got %v",
			codesOf(none))
	}

	// AND THE FENCE IS A FENCE, NOT A WALL. The positive control the gaps precedent insists on: the
	// caller who holds both yards still receives both.
	both, err := svc.ListFor(ctx, CallerScope{BranchIDs: []string{idKingston, idTrenton}})
	if err != nil {
		t.Fatalf("ListFor(two grants): %v", err)
	}
	if got := codesOf(both); len(got) != 2 || got[0] != "KGN" || got[1] != "TRT" {
		t.Fatalf("two grants must yield both rows in code order; got %v", got)
	}
}

// TestListFor_TheDefaultIsTheCallersNotTheDealers pins the meaning change on is_default.
//
// A flag that survives its own row being filtered out is false on every row that remains. Before
// this, a Kingston-only lead received one KGN row with is_default=false and no other row at all, so
// every picker reading the flag opened on nothing.
func TestListFor_TheDefaultIsTheCallersNotTheDealers(t *testing.T) {
	svc := NewService(threeYards())
	ctx := context.Background()

	// The dealer's default WINS when the caller can actually reach it.
	holdsDefault, _ := svc.ListFor(ctx, CallerScope{BranchIDs: []string{idKingston, idTrenton}})
	if got := defaultCodeOf(t, holdsDefault); got != "TRT" {
		t.Fatalf("a caller who holds the dealer's default lands on it; landed on %q", got)
	}

	// And otherwise their own sole grant, which is the case that used to have no answer.
	scoped, _ := svc.ListFor(ctx, CallerScope{BranchIDs: []string{idKingston}})
	if got := defaultCodeOf(t, scoped); got != "KGN" {
		t.Fatalf("a Kingston-only caller lands on Kingston; landed on %q", got)
	}

	// THE DEALER'S FACT IS NOT LOST, it moved to its own field, an admin screen still has to be able
	// to say which yard the DEALER defaults to, which is a different question from where to land.
	for _, b := range holdsDefault {
		if b.Code == "TRT" && !b.IsSystemDefault {
			t.Fatal("is_system_default must still carry system_settings.default_branch_id")
		}
		if b.Code == "KGN" && b.IsSystemDefault {
			t.Fatal("is_system_default must be the dealer's fact, not the caller's")
		}
	}

	// A GLOBAL caller is unchanged from the shipped behaviour: the dealer's default is theirs.
	global, _ := svc.ListFor(ctx, CallerScope{AllBranches: true})
	if got := defaultCodeOf(t, global); got != "TRT" {
		t.Fatalf("a GLOBAL caller's default is the dealer's; got %q", got)
	}
}

// TestListFor_NeverLandsAPersonOnARetiredYard covers the two cases a "first grant" rule gets wrong.
//
// Landing somebody on a closed yard is worse than landing them nowhere, because nowhere is visible
// and a closed yard looks like it worked: the picker reads "Belleville", the quote is written against
// a location that no longer trades, and nothing anywhere says so.
func TestListFor_NeverLandsAPersonOnARetiredYard(t *testing.T) {
	svc := NewService(threeYards())
	ctx := context.Background()

	// BEL sorts first in code order and is retired; the live grant must win.
	mixed, _ := svc.ListFor(ctx, CallerScope{BranchIDs: []string{idRetired, idKingston}})
	if got := defaultCodeOf(t, mixed); got != "KGN" {
		t.Fatalf("a retired yard must never be the landing default; landed on %q", got)
	}
	if len(mixed) != 2 {
		t.Fatalf("the retired yard is still LISTED, the grant editor's whole job is showing it: %v",
			codesOf(mixed))
	}

	// And when every yard they hold is retired, NOTHING is flagged rather than something wrong.
	onlyRetired, _ := svc.ListFor(ctx, CallerScope{BranchIDs: []string{idRetired}})
	if got := defaultCodeOf(t, onlyRetired); got != "" {
		t.Fatalf("with no live yard there is no default to land on; got %q", got)
	}
}

// TestListFor_TheDefaultDoesNotDepEndOnTheOrderTheGrantsArriveIn is the determinism assertion.
// dashboard/handler.go:82 sorts the identical list "so a provider's SQL plan and any test assertion
// are stable"; here it decides something a person sees, so an unstable answer would open two logins
// of the same person on two different yards.
func TestListFor_TheDefaultDoesNotDependOnTheOrderTheGrantsArriveIn(t *testing.T) {
	svc := NewService(threeYards())
	ctx := context.Background()
	a, _ := svc.ListFor(ctx, CallerScope{BranchIDs: []string{idKingston, idRetired}})
	b, _ := svc.ListFor(ctx, CallerScope{BranchIDs: []string{idRetired, idKingston}})
	if defaultCodeOf(t, a) != defaultCodeOf(t, b) {
		t.Fatalf("the landing yard changed with the grant order: %q vs %q",
			defaultCodeOf(t, a), defaultCodeOf(t, b))
	}
	if len(a) != len(b) || codesOf(a)[0] != codesOf(b)[0] {
		t.Fatalf("the list order changed with the grant order: %v vs %v", codesOf(a), codesOf(b))
	}
}

// TestListDoesNotFence_BecauseItsOtherCallerAppliesItsOwn is the one that stops a future tidy-up
// from breaking the assistant.
//
// internal/assist reaches List directly through its own BranchLister interface
// (adapters/catalogue.go:229-232) and applies its own fence against an assist.Caller. Narrowing List
// itself would leave that path double-fencing, harmless, but would also change a signature two
// packages depend on and drop a caller assist has that this package cannot see.
func TestListDoesNotFence_BecauseItsOtherCallerAppliesItsOwn(t *testing.T) {
	svc := NewService(threeYards())
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List is the dealer's whole list and must stay that way; got %v", codesOf(got))
	}
}

// TestExists_AnswersTheWidgetGatesFifthQuestion pins the seam dashboard consumes.
func TestExists_AnswersTheWidgetGatesFifthQuestion(t *testing.T) {
	svc := NewService(threeYards())
	ctx := context.Background()

	ok, err := svc.Exists(ctx, idTrenton)
	if err != nil || !ok {
		t.Fatalf("a seeded branch must exist: ok=%v err=%v", ok, err)
	}
	ok, err = svc.Exists(ctx, "5d1b3f6e-0000-4000-9000-0000000000ff")
	if err != nil {
		t.Fatalf("an unknown branch is a false, not an error: %v", err)
	}
	if ok {
		t.Fatal("an invented uuid must not report as a real branch")
	}

	// AN ERROR IS NOT AN ANSWER. The consumer degrades on err and refuses on false, so the two must
	// never be conflated here, an unreadable lookup reported as `false` would refuse every
	// branch-narrowed Home in the building.
	boom := threeYards()
	boom.existsErr = errors.New("connection reset")
	if _, err := NewService(boom).Exists(ctx, idTrenton); err == nil {
		t.Fatal("an unreadable existence read must surface its error, never a bare false")
	}
}

// TestTheBranchListPayloadIsStillABareArray is the CONTRACT TEST, and it exists because a second
// surface is about to be written against this route.
//
// THE SHAPE IS LOAD-BEARING AND IT IS NOT MINE TO CHANGE. app/src/dashboard-api.ts:265 reads
// `Array.isArray(rows) ? … : []` and app/src/quote-builder.ts:287 calls `rows.filter(…)` on the
// parsed body, an envelope would degrade the first to an empty branch list and throw in the second,
// on a deploy where nothing in the backend tests noticed. So the caller-relative fact went into the
// ROW rather than around the array, and this test is what stops the tidier version landing later.
func TestTheBranchListPayloadIsStillABareArray(t *testing.T) {
	h := NewHandler(NewService(threeYards()), nil)

	// Guards-swap carry note (HH-3.2 fold): the payload shape is the
	// handler's, so the test drives the handler directly with the stamped
	// context the guard's adapter produces.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/branches", nil)
	req = req.WithContext(identity.ContextWithStaff(req.Context(), identity.StaffContext{
		UserID: "u-kgn", RoleCode: "YARD_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Branches: map[string]bool{idKingston: true},
	}))
	rec := httptest.NewRecorder()
	h.list(rec, req, guards.StaffCaller{})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("the body must decode as a TOP-LEVEL ARRAY, two shipping clients parse it as one "+
			"and one of them throws on an object: %v; body %s", err, rec.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("a Kingston-only caller receives one row; got %d: %s", len(rows), rec.Body.String())
	}
	for _, key := range []string{"id", "code", "name", "address", "is_default", "is_system_default", "is_active"} {
		if _, ok := rows[0][key]; !ok {
			t.Fatalf("the row is missing %q, the yard console is being built against these keys: %v",
				key, rows[0])
		}
	}
	if rows[0]["code"] != "KGN" {
		t.Fatalf("the fence must reach the HTTP layer, not just the service: %v", rows[0])
	}
	if rows[0]["is_default"] != true || rows[0]["is_system_default"] != false {
		t.Fatalf("Kingston is THIS caller's default and is not the DEALER's: %v", rows[0])
	}
}

// TestTheListFailsClosedWhenNobodyIsAsking. The route is registered behind an injected requireStaff
// guard and RegisterRoutes panics without one, so a request with no StaffContext can only come from
// a wiring mistake or a test that skipped the guard. Both must fail CLOSED: a fence whose absence
// means "show everything" is the fence being off, in exactly the situation nobody is watching.
func TestTheListFailsClosedWhenNobodyIsAsking(t *testing.T) {
	h := NewHandler(NewService(threeYards()), nil)

	// Guards-swap carry note (HH-3.2 fold): the same fail-closed property,
	// driven at the handler (no stamped context means no grants).
	rec := httptest.NewRecorder()
	h.list(rec, httptest.NewRequest(http.MethodGet, "/api/v1/branches", nil), guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "[]" && got != "[]\n" {
		t.Fatalf("an unidentified caller must receive an empty list, never the dealer's: %s", got)
	}
}
