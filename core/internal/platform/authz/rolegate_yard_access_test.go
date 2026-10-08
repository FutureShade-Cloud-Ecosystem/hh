package authz

// rolegate_yard_access_test.go, what the yard manager may no longer reach (decision log row 1156,
// which reverses row 370's yard manager cells): the Counter Till and the Quote Desk are removed from
// the role's row, so every route those surfaces serves refuses it at this gate, the payment door
// with them, while the yard's own surface and every other cell it keeps (dispatch board work,
// buying and stock work, the money desk at read, the manager home) still admit it. The annotation
// read is the generated census entry of each named operation, so the contract is what is proven,
// not a literal beside it.

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// censusAccess reads one operation's x-access from the generated census.
func censusAccess(t *testing.T, operationID string) RouteAccess {
	t.Helper()
	for _, r := range apigen.Routes {
		if r.OperationID == operationID {
			return RouteAccess{Surfaces: r.Surfaces, Level: roleconfig.Level(r.Level), List: r.List}
		}
	}
	t.Fatalf("%s is not in the route census", operationID)
	return RouteAccess{}
}

// The payment door the two removed cells carried: every route a yard manager could take or record
// a payment through, the till sale first, and beside it the quote desk's one action that pays.
var yardManagerPaymentRouteOperations = []string{
	"posCreateSale",            // the till sale itself
	"posTakePayBalance",        // take a whole order's balance at the till
	"quoteTakeOrderPayment",    // the payment door on a converted quote's order
	"tillRecordPaymentAttempt", // record a declined or errored gateway attempt
	"posPaymentGrant",          // the grant read whose Holds walk covers the two payment routes
	"posCreateReturn",          // a return pays money back
	"tillRecordMovement",       // drawer money: paid in, paid out, drops, lodgements
	"tillOpenSession",          // the drawer session that holds the till's money
	"paymentGetConfig",         // the payment processor config read, reached through the till cell
	"quoteConvertQuote",        // the conversion, served by the till and the desk together
}

// The desk itself: a quote read and a quote write, the two the ruling names.
var yardManagerQuoteDeskOperations = []string{
	"quoteReadQuote",
	"quoteCreateQuote",
	"quoteEditQuoteLine",
	"quoteListQuotes",
}

// What the ruling keeps: the yard's own surface (picking, the short pick with its reason, the skid
// count, the shortage resolve the shortage_resolvers list still names the yard manager on), the
// manager home, and one route of each other kept cell.
var yardManagerKeptOperations = []string{
	"yardListPickQueue",
	"yardRecordPick",
	"yardRecordSkids",
	"yardReportShortage",
	"yardResolveShortage",
	"dashboardGetHome",
	"dispatchGetBoard",
	"billingListRegister", // the money desk at read: recording a payment there needs work
}

func TestYardManager_RefusedAtTheTillTheDeskAndThePaymentDoor(t *testing.T) {
	f := newFixture(t)
	for _, op := range append(append([]string{}, yardManagerPaymentRouteOperations...), yardManagerQuoteDeskOperations...) {
		access := censusAccess(t, op)
		code, reached := runGatedAs(t, f, access, "yard_manager")
		if code != 403 || reached {
			t.Errorf("%s: the yard manager must be refused at the gate (row 1156), got %d reached=%v", op, code, reached)
		}
	}
}

func TestYardManager_StillReachesTheYardAndItsKeptCells(t *testing.T) {
	f := newFixture(t)
	for _, op := range yardManagerKeptOperations {
		access := censusAccess(t, op)
		code, reached := runGatedAs(t, f, access, "yard_manager")
		if code != 200 || !reached {
			t.Errorf("%s: the yard manager keeps this cell, got %d reached=%v", op, code, reached)
		}
	}
}

// The two cells the ruling removes, held at the configuration the gate reads: none on the Counter
// Till and none on the Quote Desk, with the yard's own manage cell and the money desk read beside
// them so the removal cannot take more than the ruling names.
func TestYardManager_TheTwoCellsAreNone(t *testing.T) {
	cfg := mustConfig(t)
	for _, tc := range []struct {
		surface string
		want    roleconfig.Level
	}{
		{"counter-till", roleconfig.LevelNone},
		{"quote-desk", roleconfig.LevelNone},
		{"yard-pick", roleconfig.LevelManage},
		{"drive-run", roleconfig.LevelRead},
		{"dispatch-board", roleconfig.LevelWork},
		{"buying-and-stock", roleconfig.LevelWork},
		{"money-desk", roleconfig.LevelRead},
		{"manager-home", roleconfig.LevelWork},
		{"house-messages", roleconfig.LevelWork},
		{"dealer-admin", roleconfig.LevelNone},
	} {
		got, err := cfg.Level("yard_manager", tc.surface)
		if err != nil {
			t.Fatalf("Level(yard_manager, %s): %v", tc.surface, err)
		}
		if got != tc.want {
			t.Errorf("yard_manager on %s is %s, want %s", tc.surface, got, tc.want)
		}
	}
}

// The grant read's Holds walk (the payment grant route answers it for its callers): the walk must
// say the yard manager holds neither payment route's access, the same refusal the gate gives.
func TestYardManager_HoldsNoPaymentRoute(t *testing.T) {
	f := newFixture(t)
	cfg := mustConfig(t)
	g := gateOverRoles(t, f, []string{"yard_manager"}, cfg)
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/pos/sales"},
		{"POST", "/api/v1/quotes/{id}/order-payment"},
	} {
		access, ok := RouteAccessFor(route.method, route.path)
		if !ok {
			t.Fatalf("%s %s carries no staff access in the document", route.method, route.path)
		}
		held, err := g.Holds(context.Background(), "sub", "org", access)
		if err != nil {
			t.Fatalf("Holds on %s: %v", route.path, err)
		}
		if held {
			t.Errorf("%s: Holds says the yard manager holds the payment route's access", route.path)
		}
	}
}
