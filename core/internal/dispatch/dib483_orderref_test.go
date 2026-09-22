package dispatch

// dib483_orderref_test.go, DIB-483 evidence for M3-G8 and M3-G12-WILLCALL-UUID.
//
// Two things are being pinned, and only one of them is behaviour:
//
//  1. the Schedule form and the Will-call form accept a visible #SO number, still accept a pasted
//     UUID, and refuse nonsense as a 400 that says what the box wants;
//  2. THIS PACKAGE CONTAINS NO ORDER-NUMBER PARSER. (2) is the one that matters in six months. The
//     easy way to have "fixed" M3-G8 was to prepend a '#' and query orders from here, which would
//     have worked on the day and then quietly diverged from order.Repository.ResolveOrderID the first
//     time the mint's format changed or the branch-scoping rule was tightened.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - a resolver seam that records what it was asked -

type spyOrders struct {
	// resolveCalls is every (ref, global, branches) the service handed to THE resolver.
	resolveCalls []resolveCall
	// resolved maps a reference to the id the resolver returns; anything else is not found.
	resolved map[string]string
	briefs   map[string]OrderBrief
}

type resolveCall struct {
	ref      string
	global   bool
	branches []string
}

func newSpyOrders() *spyOrders {
	return &spyOrders{
		resolved: map[string]string{},
		briefs:   map[string]OrderBrief{},
	}
}

func (s *spyOrders) ResolveOrderID(_ context.Context, idOrNo string, global bool, branchIDs []string) (string, error) {
	s.resolveCalls = append(s.resolveCalls, resolveCall{ref: idOrNo, global: global, branches: branchIDs})
	if id, ok := s.resolved[idOrNo]; ok {
		return id, nil
	}
	// mirror the real resolver's UUID passthrough, and ONLY that: everything else the spy does not
	// know about is a miss, exactly as an unreachable or absent number is.
	if looksLikeUUID(idOrNo) {
		return idOrNo, nil
	}
	return "", ErrNotFound
}

func (s *spyOrders) Brief(_ context.Context, orderID string) (OrderBrief, error) {
	if b, ok := s.briefs[orderID]; ok {
		return b, nil
	}
	return OrderBrief{}, ErrNotFound
}

const (
	refOrderID  = "a1b2c3d4-0000-4000-9000-000000000001"
	refBranch   = "5d1b3f6e-0000-4000-9000-000000000001"
	refOtherBr  = "5d1b3f6e-0000-4000-9000-000000000002"
	refOrderNo  = "#SO-TRT-1041"
	refOrderNoP = "SO-TRT-1041" // the same number as a person types it, hash dropped
)

func refCaller(scope identity.Scope, branches ...string) identity.StaffContext {
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return identity.StaffContext{
		UserID: "u-yard", RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
		Scope: scope, Status: identity.StatusActive, Branches: granted,
	}
}

func refSvc(t *testing.T, orders *spyOrders, caller identity.StaffContext) *Service {
	t.Helper()
	return NewService(nil).
		WithOrders(orders).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, true })
}

// - the grader -

// TestOrderRef_GradesInputInPlainWords is the productIDParam rule applied to this box: a caller's typo
// is graded HERE, in words, instead of arriving at Postgres and coming back as "internal error".
func TestOrderRef_GradesInputInPlainWords(t *testing.T) {
	pass := []string{
		refOrderID,        // a pasted id, the flow that shipped, unchanged
		refOrderNo,        // the number as printed
		refOrderNoP,       // the number as dictated
		"  #SO-TRT-1041 ", // with the whitespace a copy-paste drags along
		"#SO-4",           // the legacy global mint
		"so-trt-1041",     // order_no is CITEXT; case is the resolver's problem, not the grader's
	}
	for _, in := range pass {
		if _, err := gradeOrderRef(in); err != nil {
			t.Fatalf("%q must be worth resolving, got %v", in, err)
		}
	}

	refuse := map[string]string{
		"":                      "enter an order number",
		"   ":                   "enter an order number",
		"Bob Smith":             "is not an order number",
		"flagstone":             "is not an order number", // no digit: this is a product, not a number
		"613-555-0142; DROP":    "is not an order number",
		strings.Repeat("9", 65): "too long",
	}
	for in, want := range refuse {
		_, err := gradeOrderRef(in)
		if err == nil {
			t.Fatalf("%q must be refused", in)
		}
		var bre httpx.BadRequestError
		if !errors.As(err, &bre) {
			t.Fatalf("%q refused with %T, want a 400-shaped error", in, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusing %q said %q, want it to contain %q", in, err.Error(), want)
		}
	}
}

// TestOrderRef_BadInputNeverReachesTheResolver: grading happens BEFORE the round trip, which is the
// whole reason the grader exists.
func TestOrderRef_BadInputNeverReachesTheResolver(t *testing.T) {
	orders := newSpyOrders()
	svc := refSvc(t, orders, refCaller(identity.ScopeGlobal))
	if _, err := svc.resolveOrderRef(context.Background(), refCaller(identity.ScopeGlobal), "Bob Smith"); err == nil {
		t.Fatal("nonsense was accepted")
	}
	if len(orders.resolveCalls) != 0 {
		t.Fatalf("nonsense reached the resolver: %+v", orders.resolveCalls)
	}
}

// TestOrderRef_ResolutionIsBranchScoped: the caller's reach is projected onto the resolver's filter,
// so a visible number can only resolve inside a branch the caller can already see. Without this, the
// #SO- number space becomes an existence oracle for another branch's order book.
func TestOrderRef_ResolutionIsBranchScoped(t *testing.T) {
	orders := newSpyOrders()
	orders.resolved[refOrderNo] = refOrderID
	caller := refCaller(identity.ScopeBranch, refBranch)

	if _, err := refSvc(t, orders, caller).resolveOrderRef(context.Background(), caller, refOrderNo); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(orders.resolveCalls) != 1 {
		t.Fatalf("resolver called %d times, want 1", len(orders.resolveCalls))
	}
	got := orders.resolveCalls[0]
	if got.global {
		t.Fatal("a BRANCH-scoped caller was projected as GLOBAL, every number in the system would resolve for them")
	}
	if len(got.branches) != 1 || got.branches[0] != refBranch {
		t.Fatalf("resolver scoped to %v, want just %s", got.branches, refBranch)
	}

	// and a GLOBAL caller reaches everything, with no id list.
	orders2 := newSpyOrders()
	orders2.resolved[refOrderNo] = refOrderID
	gcaller := refCaller(identity.ScopeGlobal)
	if _, err := refSvc(t, orders2, gcaller).resolveOrderRef(context.Background(), gcaller, refOrderNo); err != nil {
		t.Fatalf("global resolve: %v", err)
	}
	if !orders2.resolveCalls[0].global {
		t.Fatal("a GLOBAL caller was projected as scoped")
	}
}

// TestOrderRef_UnreachableNumberIsNotFound: a number that resolves to nothing, whether it never
// existed or belongs to a branch out of reach, is the same 404. No oracle.
func TestOrderRef_UnreachableNumberIsNotFound(t *testing.T) {
	orders := newSpyOrders()
	caller := refCaller(identity.ScopeBranch, refOtherBr)
	_, err := refSvc(t, orders, caller).resolveOrderRef(context.Background(), caller, refOrderNo)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreachable number = %v, want ErrNotFound", err)
	}
}

// - the two forms -

// TestSchedule_AcceptsOrderNumber is M3-G8's acceptance: the delivery form takes the number off the
// paperwork. Both spellings, and a pasted id, all land on the same order.
func TestSchedule_AcceptsOrderNumber(t *testing.T) {
	for _, ref := range []string{refOrderNo, refOrderNoP, refOrderID} {
		orders := newSpyOrders()
		orders.resolved[refOrderNo] = refOrderID
		orders.resolved[refOrderNoP] = refOrderID
		orders.briefs[refOrderID] = OrderBrief{ID: refOrderID, BranchID: refBranch, Status: "CONFIRMED", Fulfillment: "DELIVERY"}
		caller := refCaller(identity.ScopeGlobal)

		// The repository is nil in this harness, so a successful resolution walks on into the stop
		// write and panics/errors there, which is fine and deliberate: what is under test is that
		// the reference RESOLVED, evidenced by the Brief lookup on the resolved id.
		svc := refSvc(t, orders, caller)
		_, _ = svc.Schedule(context.Background(), ScheduleInput{OrderID: ref, ScheduledDate: "2026-09-01"})

		if len(orders.resolveCalls) != 1 {
			t.Fatalf("%q: resolver called %d times, want 1", ref, len(orders.resolveCalls))
		}
		if orders.resolveCalls[0].ref != strings.TrimSpace(ref) {
			t.Fatalf("%q: the service altered the reference before resolving it (%q), that is parsing, "+
				"and parsing belongs to the resolver", ref, orders.resolveCalls[0].ref)
		}
	}
}

// TestSchedule_BlankOrderRefIs400, the empty box keeps saying so, in better words than before.
func TestSchedule_BlankOrderRefIs400(t *testing.T) {
	orders := newSpyOrders()
	caller := refCaller(identity.ScopeGlobal)
	_, err := refSvc(t, orders, caller).Schedule(context.Background(), ScheduleInput{OrderID: "  ", ScheduledDate: "2026-09-01"})
	var bre httpx.BadRequestError
	if !errors.As(err, &bre) {
		t.Fatalf("blank order ref = %v, want a 400", err)
	}
	if !strings.Contains(err.Error(), "order number") {
		t.Fatalf("the refusal does not say what the box wants: %v", err)
	}
}

// TestMintWillCall_AcceptsOrderNumber is M3-G12-WILLCALL-UUID's acceptance at the rack-tag form.
func TestMintWillCall_AcceptsOrderNumber(t *testing.T) {
	orders := newSpyOrders()
	orders.resolved[refOrderNo] = refOrderID
	orders.briefs[refOrderID] = OrderBrief{ID: refOrderID, BranchID: refBranch, Status: "CONFIRMED", Fulfillment: "PICKUP"}
	caller := refCaller(identity.ScopeGlobal)

	// PICKUP, so the mint refuses on FULFILMENT, which is exactly the proof wanted here: the refusal
	// is about the order, so the NUMBER must have resolved to it.
	_, err := refSvc(t, orders, caller).MintWillCall(context.Background(), refOrderNo, 0)
	if err == nil {
		t.Fatal("a PICKUP order minted a will-call tag")
	}
	if !strings.Contains(err.Error(), "not will-call") {
		t.Fatalf("mint failed before reaching the order: %v", err)
	}
	if len(orders.resolveCalls) != 1 || orders.resolveCalls[0].ref != refOrderNo {
		t.Fatalf("the number did not go through the resolver: %+v", orders.resolveCalls)
	}
}

// TestMintWillCall_BadRefIs400, nonsense in the rack-tag box is graded the same way as in the
// delivery box. One grader, so the two forms cannot disagree about what an order reference is.
func TestMintWillCall_BadRefIs400(t *testing.T) {
	orders := newSpyOrders()
	caller := refCaller(identity.ScopeGlobal)
	_, err := refSvc(t, orders, caller).MintWillCall(context.Background(), "the blue pallets", 0)
	var bre httpx.BadRequestError
	if !errors.As(err, &bre) {
		t.Fatalf("nonsense = %v, want a 400", err)
	}
	if len(orders.resolveCalls) != 0 {
		t.Fatal("nonsense reached the resolver")
	}
}

// - the invariant -

// TestOrderRef_OneResolverNoSecondParser is the source scan, and it is the test the mutation exercise
// targets: introduce a second order-number parser anywhere in this package and it dies.
//
// What it forbids, and why each is forbidden:
//
//	'#' normalization     , prepending or stripping the hash is THE parsing step. order's resolver
//	                         does it; a copy here is a second answer waiting to happen.
//	order_no in SQL       , resolution by number is branch-scoped in one place. A query here would
//	                         either forget the scoping (an enumeration oracle) or duplicate it.
//	a second resolve entry, every surface must go through resolveOrderRef, so they cannot drift.
func TestOrderRef_OneResolverNoSecondParser(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("scanned no source files, the scan is broken and would pass vacuously")
	}

	hashNormalizers := 0
	orderNoSQL := 0
	resolveEntryPoints := 0
	seamCallSites := 0
	scanned := 0

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		scanned++
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			// The forbidden act is BUILDING a normalized number, `"#" + no`, because that is the
			// step order.Repository.ResolveOrderID owns and the step that would silently diverge.
			// (Reading a leading '#' off to decide whether a string is number-SHAPED is not that:
			// the grader in orderref.go discards nothing and looks nothing up.)
			//
			// dispatch DOES normalize its own RUN numbers (service.go), those are its rows and its
			// format. Only the ORDER number belongs to somebody else.
			if !strings.Contains(line, "runNo") &&
				(strings.Contains(line, `"#" +`) || strings.Contains(line, `+ "#"`)) {
				hashNormalizers++
			}
			// SELECTING order_no to DISPLAY on the board is fine and predates this work; what is
			// forbidden is LOOKING UP by it, `order_no = $1`, which is the branch-scoped query the
			// resolver owns.
			if strings.Contains(line, "order_no =") || strings.Contains(line, "order_no=") ||
				strings.Contains(line, "order_no ILIKE") {
				orderNoSQL++
			}
			resolveEntryPoints += strings.Count(line, "func (s *Service) resolveOrderRef(")
			seamCallSites += strings.Count(line, "s.orders.ResolveOrderID(")
		}
	}

	if scanned == 0 {
		t.Fatal("no non-test source files scanned")
	}
	if hashNormalizers != 0 {
		t.Fatalf("found %d order-number '#' normalization site(s) in dispatch. There is exactly ONE "+
			"order-number resolver in this system (order.Repository.ResolveOrderID) and dispatch reaches "+
			"it through the OrderReader seam, it must not grow a second parser.", hashNormalizers)
	}
	if orderNoSQL != 0 {
		t.Fatalf("found %d order_no LOOKUP(s) in dispatch SQL. Resolution by number is branch-scoped in "+
			"exactly one place (order.Repository.ResolveOrderID); a query here duplicates that scoping "+
			"or, worse, forgets it and turns the number space into an enumeration oracle.", orderNoSQL)
	}
	if resolveEntryPoints != 1 {
		t.Fatalf("found %d definitions of resolveOrderRef, want exactly 1, every surface that takes an "+
			"order from a person must go through the same one", resolveEntryPoints)
	}
	if seamCallSites != 1 {
		t.Fatalf("found %d call sites of the resolver seam, want exactly 1 (inside resolveOrderRef)", seamCallSites)
	}
}
