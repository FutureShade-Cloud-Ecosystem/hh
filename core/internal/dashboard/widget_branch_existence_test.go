package dashboard

// widget_branch_existence_test.go, WAVE 0b, DEFECT 1: the fifth gate on a widget's branch
// narrowing, and the 500 that was an existence oracle.
//
// -- THE DEFECT, AS IT REACHED A BROWSER ---------------------------------------------------------
//
// PUT /api/v1/dashboard/widgets as a manager-up GLOBAL caller, with one widget carrying a
// syntactically perfect but invented branch uuid:
//
//	{"widgets":[{"widget_key":"orders.open","shape":"stat","measure":"orders.open",
//	             "branch_id":"5d1b3f6e-0000-4000-9000-0000000000ff", …}]}
//	→ 500 {"error":"internal error"}
//
// while the same request naming a REAL branch answered 200. Nothing was written either way, so the
// security property held, but the pair of answers is an EXISTENCE ORACLE: the endpoint would tell
// you, for any uuid you cared to type, whether that yard is real. That is exactly what
// ValidateWidgets' one-message rule (widget.go:529-532) exists to prevent, reached from the other
// side. The route: BranchGate.Allows returns true for every well-formed uuid when the caller is
// global (widget.go:290-306); IsBranchID checks SHAPE only and its own header says so; so the id
// reached widget_repository.go:99-106, violated `branch_id uuid REFERENCES locations (id)` (0201),
// and the raw pgx error fell through widget_handler.go:159's `default:` arm.
//
// THE PRECEDENT COPIED HERE is train 16's gaps fix and its test,
// TestBoardGaps_RefusesAMissingBranchByNameLikeItsSiblings (dispatch/loadboard_test.go:809-863):
// the guard goes in the SERVICE, and the test asserts the status AND the words together, asserts
// SIBLING EQUALITY, and carries a POSITIVE CONTROL so the guard is a guard and not a wall.

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// branchInvented is well formed, passes IsBranchID, passes a global caller's BranchGate, and names
// no row in `locations`. It is the whole defect in one constant.
const branchInvented = "5d1b3f6e-0000-4000-9000-0000000000ff"

// fakeBranchResolver is the composition root's answer, in memory. It records what it was asked so a
// test can assert the ORDER of the gates, which is the security property, not a detail.
type fakeBranchResolver struct {
	real  map[string]bool
	err   error
	asked []string
}

func (f *fakeBranchResolver) Exists(_ context.Context, id string) (bool, error) {
	f.asked = append(f.asked, id)
	if f.err != nil {
		return false, f.err
	}
	return f.real[id], nil
}

// resolvedWidgetService is widgetService plus a wired resolver that knows Trenton and Belleville and
// has never heard of anything else.
func resolvedWidgetService(t *testing.T) (*Service, *fakeWidgetStore, *fakeBranchResolver) {
	t.Helper()
	s, ws := widgetService(t)
	br := &fakeBranchResolver{real: map[string]bool{branchTrenton: true, branchBelleville: true}}
	return s.WithBranchResolver(br), ws, br
}

func globalScope() Scope {
	return Scope{UserID: "u-global", Profile: ProfileAdmin, AllBranches: true}
}

func inventedWidget() []Widget {
	return []Widget{withBranch(
		widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0), branchInvented)}
}

// TestAWidgetNamingABranchThatDoesNotExistIsRefusedInWordsNotWithA500 is the defect, pinned.
//
// It asserts the SENTINEL and the STATUS together, because either alone is passable and wrong: a
// refusal that reaches the caller as a 500 is no better than the FK violation at telling somebody
// their branch is not real, and a sentinel nobody maps is a 500 in waiting.
func TestAWidgetNamingABranchThatDoesNotExistIsRefusedInWordsNotWithA500(t *testing.T) {
	s, ws, br := resolvedWidgetService(t)

	_, err := s.SaveMyWidgets(context.Background(), globalScope(), inventedWidget())
	if err == nil {
		t.Fatal("an invented branch must be refused here, not handed to the foreign key")
	}
	if !errors.Is(err, ErrBranchNotOffered) {
		t.Fatalf("want the branch refusal, got %v", err)
	}
	if ws.writes != 0 {
		t.Fatalf("the store was written %d times for a refused save", ws.writes)
	}
	if len(br.asked) != 1 || br.asked[0] != branchInvented {
		t.Fatalf("the resolver must be asked exactly about the branch submitted; asked %v", br.asked)
	}

	// THE STATUS, through the handler's own mapping, 422, and specifically not 500. 422 is what
	// widget_handler.go:140-144 already argues for a well-formed submission whose CONTENT is wrong.
	h := NewHandler(s, nil)
	rec := httptest.NewRecorder()
	h.failWidget(rec, err)
	if rec.Code == 500 {
		t.Fatalf("the whole defect was a 500; got one back: %s", rec.Body.String())
	}
	if rec.Code != 422 {
		t.Fatalf("status = %d, want 422 (the same arm its four sibling refusals take)", rec.Code)
	}
	// AND THE WORDS DO NOT NAME THE BRANCH, the posture its siblings hold, so the refusal itself
	// cannot be turned back into the oracle the status just stopped being.
	if strings.Contains(rec.Body.String(), branchInvented) {
		t.Fatalf("the refusal names what it refused: %s", rec.Body.String())
	}

	// AND THE GUARD IS A GUARD, NOT A WALL, the positive control the gaps precedent insists on. A
	// real branch, same caller, same widget, still saves.
	if _, err := s.SaveMyWidgets(context.Background(), globalScope(),
		[]Widget{withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
			branchTrenton)}); err != nil {
		t.Fatalf("a real branch must still save: %v", err)
	}
	// And so does the common case, which carries no branch at all and must never reach the resolver.
	before := len(br.asked)
	if _, err := s.SaveMyWidgets(context.Background(), globalScope(),
		[]Widget{widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0)}); err != nil {
		t.Fatalf("no branch at all must save: %v", err)
	}
	if len(br.asked) != before {
		t.Fatalf("the empty string is 'every branch I can see' and is never a row; asked %v", br.asked)
	}
}

// TestTheExistenceRefusalIsIndistinguishableFromTheGrantRefusal is SIBLING EQUALITY, and here it is
// not a style rule, it is the security property.
//
// Two different answers would mean 422 = "that yard is real and is not yours" and anything-else =
// "no such yard", which is a supported, documented enumeration oracle: strictly worse than the 500
// it replaced, because a crash is at least obviously a bug. So "you may not" and "there is no such
// thing" must arrive as the same sentinel, the same status and the same bytes.
func TestTheExistenceRefusalIsIndistinguishableFromTheGrantRefusal(t *testing.T) {
	s, _, _ := resolvedWidgetService(t)
	h := NewHandler(s, nil)

	// (a) a GLOBAL caller naming a branch that does not exist.
	_, missing := s.SaveMyWidgets(context.Background(), globalScope(), inventedWidget())
	// (b) a SCOPED caller naming a branch that exists and is not theirs.
	_, ungranted := s.SaveMyWidgets(context.Background(),
		Scope{UserID: "u-kgn", Profile: ProfileManager, BranchIDs: []string{branchTrenton}},
		[]Widget{withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
			branchBelleville)})
	if missing == nil || ungranted == nil {
		t.Fatal("both must be refused, this test compares them against each other")
	}
	if missing.Error() != ungranted.Error() {
		t.Fatalf("the two refusals must read identically:\n missing:   %v\n ungranted: %v",
			missing, ungranted)
	}

	mrec, urec := httptest.NewRecorder(), httptest.NewRecorder()
	h.failWidget(mrec, missing)
	h.failWidget(urec, ungranted)
	if mrec.Code != urec.Code || mrec.Body.String() != urec.Body.String() {
		t.Fatalf("the two refusals must be byte-identical on the wire:\n missing:   %d %s\n ungranted: %d %s",
			mrec.Code, mrec.Body.String(), urec.Code, urec.Body.String())
	}
}

// TestAScopedCallerNeverReachesTheExistenceReadAtAll pins the ORDER of the two gates.
//
// refuseUnknownBranches runs AFTER ValidateWidgets, so a scoped caller's invented id is stopped by
// the GRANT gate and no lookup happens on their behalf at all. That is what makes the oracle
// unreconstructible from timing as well as from wording: for the caller who must not learn whether a
// yard exists, the database is never asked.
func TestAScopedCallerNeverReachesTheExistenceReadAtAll(t *testing.T) {
	s, _, br := resolvedWidgetService(t)
	scope := Scope{UserID: "u-kgn", Profile: ProfileManager, BranchIDs: []string{branchTrenton}}

	_, err := s.SaveMyWidgets(context.Background(), scope, inventedWidget())
	if !errors.Is(err, ErrBranchNotOffered) {
		t.Fatalf("want the branch refusal, got %v", err)
	}
	if len(br.asked) != 0 {
		t.Fatalf("a scoped caller's invented branch must be refused by the GRANT gate, before any "+
			"existence read; the resolver was asked %v", br.asked)
	}
}

// TestARoleDefaultNeverReachesTheExistenceRead. SaveProfileWidgets validates under NoBranch, which
// refuses every named branch outright (widget.go:283-286), so there is no surviving branch id for
// the resolver to be asked about. Asserted rather than assumed, because "the role-default path also
// checks existence" is a plausible-looking future edit that would only add a read.
func TestARoleDefaultNeverReachesTheExistenceRead(t *testing.T) {
	s, _, br := resolvedWidgetService(t)
	rows := []Widget{
		withBranch(widget("hero.sales", WidgetStat, MetricSalesByBranch, SplitNone, SizeL, 0), branchTrenton),
	}
	if _, err := s.SaveProfileWidgets(context.Background(), ProfileYard, rows, "admin-1"); !errors.Is(err, ErrBranchNotOffered) {
		t.Fatalf("a role default naming a branch must still be refused outright, got %v", err)
	}
	if len(br.asked) != 0 {
		t.Fatalf("a role default may not name a branch AT ALL, so existence is never the question; "+
			"the resolver was asked %v", br.asked)
	}
}

// TestAnUnwiredOrUnreadableBranchResolverDegradesToTodaysBehaviour is the nil-seam contract, and it
// is the reason this could ship on a Wednesday.
//
// "Nil is honest and degrades DOWNWARD" is purchasing/module.go:74's sentence for the same shape of
// seam, and myBranchGate one function up makes the same call for the same reason ("a store that will
// not answer falls back to grants alone"). Refusing a save because a lookup blinked would break
// every branch-narrowed Home in the building over a transient, to protect against a mistake whose
// worst outcome is the 500 we already had.
func TestAnUnwiredOrUnreadableBranchResolverDegradesToTodaysBehaviour(t *testing.T) {
	// UNWIRED: exactly the shipped behaviour. This is also what keeps
	// TestAGlobalCallerMayNarrowToAnyBranch meaningful, that test builds a service with no resolver.
	bare, _ := widgetService(t)
	if _, err := bare.SaveMyWidgets(context.Background(), globalScope(), inventedWidget()); err != nil {
		t.Fatalf("with no resolver wired the save must behave exactly as it did before: %v", err)
	}

	// UNREADABLE: the resolver is there and cannot answer. An error is "I could not check", never
	// "that branch is not real".
	s, ws, br := resolvedWidgetService(t)
	br.err = errors.New("connection reset by peer")
	before := ws.writes
	if _, err := s.SaveMyWidgets(context.Background(), globalScope(),
		[]Widget{withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0),
			branchTrenton)}); err != nil {
		t.Fatalf("a database that blinked must not refuse a legitimate save: %v", err)
	}
	if ws.writes != before+1 {
		t.Fatal("the save must have reached the store")
	}
}

// TestTheExistenceReadAsksOncePerDistinctBranch. A bento with several cards on the same yard is one
// question; asking it per widget would be a cost with no answer attached.
func TestTheExistenceReadAsksOncePerDistinctBranch(t *testing.T) {
	s, _, br := resolvedWidgetService(t)
	rows := []Widget{
		withBranch(widget("orders.open", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 0), branchTrenton),
		withBranch(widget("orders.two", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 1), branchTrenton),
		withBranch(widget("orders.three", WidgetStat, MetricOrdersOpen, SplitNone, SizeS, 2), branchBelleville),
	}
	if _, err := s.SaveMyWidgets(context.Background(), globalScope(), rows); err != nil {
		t.Fatalf("three widgets across two real yards must save: %v", err)
	}
	if len(br.asked) != 2 {
		t.Fatalf("two distinct yards is two reads, not three; asked %v", br.asked)
	}
}
