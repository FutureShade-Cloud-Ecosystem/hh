package purchasing

// delivery_method_test.go, DIB-572's correction path, ported one for one from the ERP's
// dib572_dib573_test.go (the delivery-method half; the DIB-573 search half is not ported, core's
// worklist takes no search term) and dib572_route_guard_test.go, adapted to core's guard shape:
// the ERP measured positional guard doubles around RegisterRoutes; core registers the real
// Guards through testkit.NewStaffGuards and proves the same three facts behaviourally, which
// tier each route requires, that a refused caller never reaches the store, and that the two
// correction routes carry the same gate.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auth "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestNormalizeDeliveryMethod_EmptyIsUnrecordedAndNonsenseIsRefused.
//
// Every caller written before DIB-572 sends no delivery_method at all, and folding those to
// DELIVERY would put a word on the receiving worklist that no person had ever said. An
// unanswered question is UNKNOWN.
//
// The refusal case matters for the opposite reason: "PICK UP" quietly becoming DELIVERY would
// put the WRONG word on a receiving worklist, and a receiver who reads "Delivered" on an order
// the yard is supposed to collect does not send the truck.
func TestNormalizeDeliveryMethod_EmptyIsUnrecordedAndNonsenseIsRefused(t *testing.T) {
	ok := map[string]string{
		"":          DeliveryUnknown,
		"   ":       DeliveryUnknown,
		"UNKNOWN":   DeliveryUnknown,
		"unknown":   DeliveryUnknown,
		"DELIVERY":  DeliveryDelivered,
		"delivery":  DeliveryDelivered,
		"  Pickup ": DeliveryPickup,
		"PICKUP":    DeliveryPickup,
	}
	for in, want := range ok {
		got, err := normalizeDeliveryMethod(in)
		if err != nil {
			t.Fatalf("normalizeDeliveryMethod(%q) errored: %v", in, err)
		}
		if got != want {
			t.Errorf("normalizeDeliveryMethod(%q) = %q, want %q", in, got, want)
		}
	}
	// These legs pin the pure function's ported contract: at this layer nothing an unasked caller
	// can send may come back as an arrival. (The ROUTE refuses an explicit empty before normalize
	// is consulted; that refusal is pinned in TestSetDeliveryMethod_ARefusedRequestNeverReachesTheStore.
	// The fold exists for the create path, a recorded residual that lands against this contract.)
	for _, silent := range []string{"", "   ", "\t", "\n "} {
		got, err := normalizeDeliveryMethod(silent)
		if err != nil {
			t.Fatalf("normalizeDeliveryMethod(%q) errored: %v", silent, err)
		}
		if got == DeliveryDelivered || got == DeliveryPickup {
			t.Errorf("a caller who said nothing (%q) was recorded as %q; the program invented an arrival", silent, got)
		}
	}
	for _, bad := range []string{"PICK UP", "WILL_CALL", "COLLECT", "DELIVERED", "x", "NONE", "N/A"} {
		if _, err := normalizeDeliveryMethod(bad); err == nil {
			t.Errorf("normalizeDeliveryMethod(%q) was accepted; a wrong word on the worklist is worse than a 400", bad)
		}
	}
}

// TestCanSetDeliveryMethod_OnlyWhileTheWorklistStillShowsIt.
//
// The statuses this route accepts are exactly the statuses the receiving worklist renders, and
// that is the property that makes the column honest: every row on which a person can SEE a
// wrong arrival is a row they can CORRECT. A finished order's arrival is a historical fact
// nobody is waiting on.
func TestCanSetDeliveryMethod_OnlyWhileTheWorklistStillShowsIt(t *testing.T) {
	for _, st := range []string{StatusDraft, StatusOrdered, StatusPartial} {
		if !CanSetDeliveryMethod(st) {
			t.Errorf("%s: a person can see this order's arrival and cannot correct it", st)
		}
	}
	for _, st := range []string{StatusReceived, StatusClosed, StatusCancelled} {
		if CanSetDeliveryMethod(st) {
			t.Errorf("%s is finished; rewriting its arrival changes a historical fact nobody is waiting on", st)
		}
	}
}

// TestNormalizeDeliveryMethod_RefusesWillCall names the one plausible wrong answer.
//
// WILL_CALL is a real value in this program (order.Fulfillment is PICKUP | DELIVERY | WILL_CALL)
// and it is a CUSTOMER's word: goods leaving the yard that someone will call for. It has no
// meaning on a purchase order, where the yard is the one collecting, and the CHECK the 0219
// port carries does not accept it. If this ever starts passing, the column and the constraint
// have drifted apart.
func TestNormalizeDeliveryMethod_RefusesWillCall(t *testing.T) {
	_, err := normalizeDeliveryMethod("WILL_CALL")
	if err == nil {
		t.Fatal("WILL_CALL was accepted; the CHECK constraint would then reject the insert instead, as a 500")
	}
	for _, want := range []string{DeliveryPickup, DeliveryDelivered, DeliveryUnknown} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %s among the values it accepts, got %q", want, err)
		}
	}
}

// - the route, behind the real Guards -

// deliveryRoute is the one route under test, written once so a rename cannot quietly make these
// tests exercise a 404 instead of a guard.
const deliveryRoute = "/api/v1/purchasing/pos/11111111-1111-4111-8111-111111111111/delivery-method"

// deliveryRouteFixture mounts the REAL route table (RegisterRoutes, the same call the
// composition root makes) behind the kit's DB-free Guards and returns the driver. The service
// is built over a nil pool and the recording incoming-store: the guard question is settled
// before any of that is reached, and an admitted request fails honestly at the missing
// database (503), which is itself the proof the request got PAST the guard. The fixture wires
// a user linker for the reason caller_stamp.go documents and order's handler fixture
// demonstrates: a nil linker stamps authority only, and the service's caller read then fails
// closed with 401 before the store is ever asked, which would make these tests measure the
// stamp instead of the guard.
func deliveryRouteFixture(t *testing.T) (sg *testkit.StaffGuards, store *fakeIncoming, do func(path, body string, withBearer bool) *httptest.ResponseRecorder) {
	t.Helper()
	sg = testkit.NewStaffGuards(t)
	store = &fakeIncoming{}
	svc := NewService(NewRepository(nil)).withIncomingStore(store)
	h := NewHandler(svc, nil).WithUserLinker(func(_ context.Context, _ string) (string, bool) {
		return "u-fixture", true
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	bearer := sg.Bearer(t)
	do = func(path, body string, withBearer bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if withBearer {
			r.Header.Set("Authorization", "Bearer "+bearer)
			r.Header.Set("X-Org", testkit.OrgNorthSlug)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		return rr
	}
	return sg, store, do
}

// yardHand is a receiver: frontline, scoped to one yard, tier 3, so IsManager() is false, which
// is the whole point.
var yardHandAuthority = auth.StaffAuthority{RoleID: "yard_team", Tier: 3, Scope: "branch", Branches: []string{"b-trenton"}}

// purchasingManagerAuthority is the caller the route is FOR: a manager who may correct an arrival.
var purchasingManagerAuthority = auth.StaffAuthority{RoleID: "branch_manager", Tier: 2, Scope: "global"}

// TestDeliveryMethodRoute_AYardHandIsRefused. Three assertions, because a bare status code
// cannot tell the three apart: refused by the ROUTE guard, refused later by the service, or
// refused for some unrelated reason. The one that matters most is the last: the write never
// reached the store.
func TestDeliveryMethodRoute_AYardHandIsRefused(t *testing.T) {
	sg, store, do := deliveryRouteFixture(t)
	sg.SetAuthority(yardHandAuthority)

	rr := do(deliveryRoute, `{"delivery_method":"PICKUP"}`, true)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("a YARD_TEAM caller PUTting an arrival got %d (%s), want 403; "+
			"a receiver must not be able to change what a purchase order says it is",
			rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if store.deliveryUpdated {
		t.Fatal("a refused request still called UpdateDeliveryMethod: the gate answered 403 " +
			"and the write happened anyway")
	}
}

// TestDeliveryMethodRoute_AManagerIsAdmitted is the other direction, and it is not decoration.
//
// A guard test that only proves refusal is satisfied by a route that refuses EVERYONE, the fix
// for which would be to delete the feature. This asserts the gate opens for the caller it is
// for. The service then fails on the nil pool, which is expected and is itself the proof:
// reaching a database error means the request got PAST the guard.
func TestDeliveryMethodRoute_AManagerIsAdmitted(t *testing.T) {
	sg, _, do := deliveryRouteFixture(t)
	sg.SetAuthority(purchasingManagerAuthority)

	rr := do(deliveryRoute, `{"delivery_method":"PICKUP"}`, true)
	if rr.Code == http.StatusNotFound {
		t.Fatalf("%s is not registered (404 from the mux)", deliveryRoute)
	}
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
		t.Fatalf("a manager PUTting an arrival was turned away with %d (%s); the correction path "+
			"is closed to the people it exists for", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("an admitted manager request should reach the service and fail on the nil pool (503), got %d (%s)",
			rr.Code, strings.TrimSpace(rr.Body.String()))
	}
}

// TestDeliveryMethodRoute_AnAnonymousCallerIsRefused: the floor beneath the tier check.
func TestDeliveryMethodRoute_AnAnonymousCallerIsRefused(t *testing.T) {
	sg, store, do := deliveryRouteFixture(t)
	sg.SetAuthority(purchasingManagerAuthority)

	rr := do(deliveryRoute, `{"delivery_method":"PICKUP"}`, false)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated PUT got %d (%s), want 401", rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	if store.deliveryUpdated {
		t.Fatal("an unauthenticated request reached UpdateDeliveryMethod")
	}
}

// TestDeliveryMethodRoute_IsGuardedNoLessThanItsSibling.
//
// The arrival and the expected date are the same kind of edit to the same object, a correction
// to what a placed order says, and they were deliberately given the same gate. This pins the
// pair behaviourally, so a future change which loosens one has to justify loosening both. It
// is a RELATIVE claim, and that is what makes it durable: if the repo ever decides these writes
// belong to frontline staff, this test does not stand in the way; it only refuses to let the
// two drift apart silently. (The ERP's original read back which named guard double wrapped each
// route; core's Guards are concrete constructors, so the pin here is the observable outcome for
// a tier 3 and a tier 2 caller on both routes.)
func TestDeliveryMethodRoute_IsGuardedNoLessThanItsSibling(t *testing.T) {
	const poPath = "/api/v1/purchasing/pos/11111111-1111-4111-8111-111111111111/"
	siblings := []string{poPath + "delivery-method", poPath + "expected-date"}

	sg, _, do := deliveryRouteFixture(t)

	// Tier 3: both refuse, identically.
	sg.SetAuthority(yardHandAuthority)
	for _, path := range siblings {
		if rr := do(path, `{"delivery_method":"PICKUP","expected_at":""}`, true); rr.Code != http.StatusForbidden {
			t.Fatalf("tier 3 PUT %s = %d, want 403; the two correction routes must carry the same gate", path, rr.Code)
		}
	}

	// Tier 2: both admit, identically (each then fails on the nil pool at 503, the proof it
	// passed the guard).
	sg.SetAuthority(purchasingManagerAuthority)
	for _, path := range siblings {
		rr := do(path, `{"delivery_method":"PICKUP","expected_at":""}`, true)
		if rr.Code == http.StatusNotFound {
			t.Fatalf("%s is not registered (404 from the mux)", path)
		}
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("tier 2 PUT %s = %d, want 503 (past the guard, no database); the two correction routes must carry the same gate", path, rr.Code)
		}
	}
}

// TestSetDeliveryMethod_ARefusedRequestNeverReachesTheStore: the service half, DB-free.
//
// The route guard above answers "may this caller call it at all". This answers the narrower
// question underneath: when the SERVICE refuses, does it refuse before writing? Every refusal
// the service can reach without a database is checked here against one recording store, so
// that no future rearrangement of SetDeliveryMethod can move a validation to after the UPDATE.
func TestSetDeliveryMethod_ARefusedRequestNeverReachesTheStore(t *testing.T) {
	cases := []struct {
		name   string
		poID   string
		method string
	}{
		{"no po id", "  ", "PICKUP"},
		{"an empty body must not be read as UNKNOWN", "po-1", ""},
		{"a method nobody defined", "po-1", "WILL_CALL"},
		{"a method nobody defined, in the right shape", "po-1", "DELIVERED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeIncoming{}
			svc := svcAs(store, dib460GlobalCaller())
			if _, err := svc.SetDeliveryMethod(context.Background(), tc.poID, tc.method); err == nil {
				t.Fatalf("SetDeliveryMethod(%q, %q) was accepted", tc.poID, tc.method)
			}
			if store.deliveryUpdated {
				t.Fatalf("SetDeliveryMethod(%q, %q) was refused and STILL wrote %q to the order",
					tc.poID, tc.method, store.gotDeliveryMethod)
			}
		})
	}
}
