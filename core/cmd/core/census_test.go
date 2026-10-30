package main

// The census half of the completeness contract (the PR 44 review's P2-5),
// split out of the composition walk so it runs where the repository's CI
// actually runs: with no gate variable, no database, no composition. The
// document and the census are asserted here in both directions: all 680
// staff operations carry a surface and a level in roleconfig's
// vocabulary, no non-staff operation carries an annotation, and every
// named list is one the gate reads. What this test cannot see is a route
// registered in Go but annotated nowhere (the census would never learn of
// it): that half is the registered walk in rolegate_composition_test.go,
// database gated, and its CI gap is recorded in docs/BLOCKERS.md for W5.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func TestCensusStaffRoutesCarrySurfaceAndLevel(t *testing.T) {
	staff := 0
	for _, route := range apigen.Routes {
		switch route.XRole {
		case "requireStaff", "requireManager", "requireAdmin":
			staff++
			assertCensusAnnotated(t, route)
		default:
			if len(route.Surfaces) != 0 || route.Level != "" || route.List != "" {
				t.Fatalf("non-staff operation %s (%s) carries an annotation: surfaces %v level %q list %q", route.OperationID, route.XRole, route.Surfaces, route.Level, route.List)
			}
		}
	}
	// 707: the base's 681, main's routes (FW1-01's glOpenYear, FW1-12a's two
	// credit memo doors, FW1-04's bankrecRecordCutover and bankrecCutoverPreview,
	// FW1-07's controllerExceptions, paymentsVoidPayment), FW1-12b's two
	// credit application doors (billingApplyCredit and
	// billingReverseCreditApplication), W2-6b's three counter reason code
	// routes (tillListReasonCodes, tillCreateReasonCode and tillEditReasonCode),
	// W2-9a's purchasingGetAPAging and W2-9a round 2's purchasingPayBill,
	// W3 item 0.10's two write off doors (billingWriteOffInvoice and
	// billingReverseWriteOff), HST-1's taxGetWorksheet, W3 item 0.8's four vendor credit doors
	// (purchasingCreateVendorCredit, purchasingListVendorCredits, purchasingPostVendorCredit and
	// purchasingApplyVendorCredit), and W3's five payment run doors (purchasingProposePaymentRun,
	// purchasingListPaymentRuns, purchasingGetPaymentRun, purchasingUpdatePaymentRun and
	// purchasingPayPaymentRun). Both sides' routes are kept; the count is what
	// merge.mjs reports on the assembled document.
	if staff != 707 {
		t.Fatalf("census staff operations: %d, want the document's 707 (main's routes, the two write off doors, taxGetWorksheet, the four vendor credit doors and the five payment run doors)", staff)
	}
	t.Logf("surface and level asserted for all %d census staff operations, no database needed", staff)
}

func assertCensusAnnotated(t *testing.T, route apigen.Route) {
	t.Helper()
	if len(route.Surfaces) == 0 {
		t.Fatalf("staff operation %s carries no surface", route.OperationID)
	}
	for _, s := range route.Surfaces {
		if !roleconfig.IsSurface(s) {
			t.Fatalf("staff operation %s names unknown surface %q", route.OperationID, s)
		}
	}
	switch roleconfig.Level(route.Level) {
	case roleconfig.LevelNone:
		t.Fatalf("staff operation %s carries level none: a route level cannot be none (D49)", route.OperationID)
	case roleconfig.LevelRead, roleconfig.LevelWork, roleconfig.LevelManage, roleconfig.LevelAdmin:
	default:
		t.Fatalf("staff operation %s names unknown level %q", route.OperationID, route.Level)
	}
	// The named lists the gate reads (rolegate.go's listReaders): the
	// pricing editors, the cost and margin list (D10, the pricing log), the low
	// margin report's readers (the floor lists held to the cost list), the shortage
	// resolvers (X11, the yard's shortage resolve route), the PO writers (the
	// buying route's writer list), the order cancellers and the dispatch policy setters (X13).
	if route.List != "" && route.List != roleconfig.SettingPricingEditors && route.List != roleconfig.SettingCostVisibility && route.List != roleconfig.SettingDispatchPolicySetters && route.List != authz.ListLowMarginReport && route.List != roleconfig.SettingShortageResolvers && route.List != authz.ListPOWriters && route.List != authz.ListOrderCancellers {
		t.Fatalf("staff operation %s names list %q, which no gate reader knows", route.OperationID, route.List)
	}
}
