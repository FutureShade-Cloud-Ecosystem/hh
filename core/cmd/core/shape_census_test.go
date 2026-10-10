package main

// The response shape census (the hardening lane's test gate): for every
// operation whose contract response is a page shape (a top level object
// carrying items as an array and next_cursor as a string or null), a test
// asserts the served handler answers that shape. The document side is
// core/api/openapi.yaml (the merged, self contained contract, read only
// here), parsed with gopkg.in/yaml.v3 (promoted to a direct require for
// this census and for nothing else); the served side is the composition
// rig (compfixture_test.go): the real registerRoutes over the identity
// environment map and the gate database, driven the x-role suite's way
// (xrole_assert_test.go: the probe pattern and its absent uuid fill).
//
// The census is honest about coverage. Every page shape operation it
// drives (a caller ladder leg reached a 2xx) is shape checked; an
// operation no caller can drive to a 2xx is never silently skipped (it
// lands on the named uncovered list with the reason: which callers
// refused, what the fixture lacks), and a mismatch lands on the named
// allowlist with the reason read at source (the fixes are the owning
// lanes', not this gate's). The gate fails on a mismatch the allowlist
// does not name, on an uncovered operation the named list does not carry, on an allowlist
// entry that names no operation of the document, on an allowlist entry gone stale: its
// operation answers the right shape, so the entry must drop; that is the parity ledger's
// shrink discipline.
// Round 2 adds the mirror: the gate also fails on a stale uncovered entry, meaning an
// operation the uncovered list names that a caller just drove, fixture widened, or handler
// changed; the list must shrink. And it refuses a name sitting on both lists at once, as
// incoherent.
// The driven count, the allowlist and the uncovered list print in the log and in every
// failure report.
//
// The gate is split so the plain ci job (no database) still walks the
// contract: the parse and classify half (the walk, the cross check
// against apigen.Routes, and the answer checker against a fixture
// document whose page shape operation is served by a handler answering a
// bare array) runs ungated; the live drive of the served surface gates on
// HH_CORE_TEST_DATABASE_URL (the compfixture discipline) and skips
// politely without it.
//
// THE CENSUS'S FIRST FINDING (the run that classified the debt, the
// reason the two maps below are as long as they are): of the document's
// 118 page shape operations the rig drives 78 (the other 40 sit uncovered
// with their reasons) and only 6 answer the contract's page shape
// (glListJournals, quoteListQuotes, tasksList, tenantListInvoices,
// tenantListOrders, tenantListQuotes); the other 72 break it in four
// families (bare array writers, items null (the nil Go slice), answers
// with no next_cursor key, and answers carrying the list under a
// different key). Each entry names its handler source (file and
// function); the fixes belong to the owning lanes (the contract, not any
// handler's current answer, is the spec).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// shapeCensusWantPages pins the page shape operation count the document
// carries (119 at this census's cut: 114 staff lists, 3 tenant lists, the
// sync pull and the public products list, all answered at 200). The pin is
// the census's knowing (the same discipline as the 609 staff operation pin
// in census_test.go): a page shape added or removed must move this number
// consciously, with the live half's report (the new operation is driven,
// or the dropped one is named) as the evidence.
const shapeCensusWantPages = 119

// shapeCensusAllowlist names (per the brief) the page shape operations
// whose served answer already breaks the page shape, each with the reason
// read at source (handler file and function). The gate fails only on a
// mismatch this list does not name; a listed operation that starts
// answering the page shape (the owning lane's fix landed) fails as a
// stale entry (the parity ledger's own shrink discipline).
//
// The four families the first census run read at source:
//
//	(1) bare array: the handler writes the list slice (or a projection
//	    of it) as the whole answer body.
//	(2) items null: the handler writes a map whose items value is a nil
//	    Go slice (it marshals as null), and the same map carries no
//	    next_cursor key.
//	(3) no next_cursor: the answer carries items (an array) but no
//	    next_cursor key at all.
//	(4) wrong key: the list is written under a key the contract does
//	    not declare (items is the contract's spelling).
var shapeCensusAllowlist = map[string]string{
	// (1) bare array (23 of the 118, the first entry the brief names).
	"auditListEntries":            "answers a bare JSON array (internal/audit/handler.go, (*Handler).list writes the h.svc.List rows directly) against the contract's AuditEntryPage; the fix is the owning lane's",
	"auditReopenTransaction":      "answers a bare JSON array (internal/audit/handler.go, (*Handler).reopen writes its projected []Entry directly) against the contract's AuditEntryPage; the fix is the owning lane's",
	"bankrecListAccounts":         "answers a bare JSON array (internal/bankrec/handler.go, (*Handler).accounts writes the h.svc.Accounts slice, nil guarded to []) against the contract's BankRecAccountPage; the fix is the owning lane's",
	"bankrecListReconciliations":  "answers a bare JSON array (internal/bankrec/handler.go, (*Handler).reconciliations writes the h.svc.Reconciliations slice directly) against the contract's BankRecReconciliationPage; the fix is the owning lane's",
	"bankrecListRules":            "answers a bare JSON array (internal/bankrec/handler.go, (*Handler).rules writes the h.svc.Rules slice directly) against the contract's BankRecMatchRulePage; the fix is the owning lane's",
	"posListReturnRequests":       "answers a bare JSON array (internal/pos/return_requests.go, (*Handler).listReturnRequests writes the h.svc.ListReturnRequests slice directly, no nil guard) against the contract's PosReturnRequestPage; the fix is the owning lane's",
	"pricingListAudit":            "answers a bare JSON array (internal/pricing/handler.go, (*Handler).listAudit writes the h.svc.ListAudit slice directly) against the contract's PricingAuditLogPage; the fix is the owning lane's",
	"pricingListBatchLines":       "answers a bare JSON array (internal/pricing/batch_handler.go, (*Handler).listBatchLines writes the h.svc.ListBatchLines slice directly) against the contract's PricingBatchLinePage; the fix is the owning lane's",
	"pricingListBatches":          "answers a bare JSON array (internal/pricing/batch_handler.go, (*Handler).listBatches writes the h.svc.ListBatches slice directly) against the contract's PricingBatchPage; the fix is the owning lane's",
	"pricingListCategoryPolicies": "answers a bare JSON array (internal/pricing/handler.go, (*Handler).listCategoryPolicies writes the h.svc.ListCategoryPolicies slice directly) against the contract's PricingCategoryPolicyPage; the fix is the owning lane's",
	"pricingListChangeLog":        "answers a bare JSON array (internal/pricing/batch_handler.go, (*Handler).changeLog writes the h.svc.ChangeLog slice directly) against the contract's PricingChangeLogPage; the fix is the owning lane's",
	"pricingListClearance":        "answers a bare JSON array (internal/pricing/w4_handler.go, (*Handler).listClearance writes the h.svc.ListClearance slice directly) against the contract's PricingClearancePage; the fix is the owning lane's",
	"pricingListImportProfiles":   "answers a bare JSON array (internal/pricing/batch_handler.go, (*Handler).listImportProfiles writes the h.svc.ListImportProfiles slice directly) against the contract's PricingImportProfilePage; the fix is the owning lane's",
	"pricingListOverrides":        "answers a bare JSON array (internal/pricing/handler.go, (*Handler).listOverrides writes the h.svc.ListOverrides slice directly) against the contract's PricingOverridePage; the fix is the owning lane's",
	"pricingListPriceFileRuns":    "answers a bare JSON array (internal/pricing/pricefile_handler.go, (*Handler).priceFileRuns writes the h.svc.PriceImportRuns slice directly, no nil guard) against the contract's PricingPriceImportRunPage; the fix is the owning lane's",
	"pricingListPriceFileVendors": "answers a bare JSON array (internal/pricing/pricefile_handler.go, (*Handler).priceFileVendors writes the h.svc.PriceFileVendors slice directly) against the contract's PricingPriceFileVendorPage; the fix is the owning lane's",
	"pricingListPromotions":       "answers a bare JSON array (internal/pricing/w4_handler.go, (*Handler).listPromotions writes the h.svc.ListPromotions slice directly) against the contract's PricingPromotionPage; the fix is the owning lane's",
	"pricingListQuantityBreaks":   "answers a bare JSON array (internal/pricing/handler.go, (*Handler).listQuantityBreaks writes the h.svc.ListQuantityBreaks slice directly) against the contract's PricingQuantityBreakPage; the fix is the owning lane's",
	"pricingListRebatePrograms":   "answers a bare JSON array (internal/pricing/w4_handler.go, (*Handler).listRebatePrograms writes the h.svc.ListRebatePrograms slice directly) against the contract's PricingRebateProgramPage; the fix is the owning lane's",
	"pricingListRules":            "answers a bare JSON array (internal/pricing/handler.go, (*Handler).listRules writes the h.svc.ListRules slice directly) against the contract's PricingRulePage; the fix is the owning lane's",
	"pricingListUOMConversions":   "answers a bare JSON array (internal/pricing/w4_handler.go, (*Handler).listConversions writes the h.svc.ListConversions slice directly) against the contract's PricingUOMConversionPage; the fix is the owning lane's",
	"productList":                 "answers a bare JSON array (internal/product/handler.go, (*Handler).list writes the h.svc.List slice directly, totals in an X-Total-Count header); the handler source carries a comment calling the bare array load bearing for the POS grid and the export callers, so this entry is the contract and the handler disagreeing on purpose (the owning lane owns the resolution (a page envelope here, or a contract amendment)) against the contract's ProductPage",

	// (2) items null (13): the map (or struct) is (or holds) a nil slice
	// (it marshals as null) and carries no next_cursor key.
	"complianceListCalendar":          "answers {\"items\":null} on the empty list (internal/compliance/handler.go, Handler.calendar writes map[string]any{\"items\": items} with no nil guard) against the contract's ComplianceDeadlinePage; the fix is the owning lane's",
	"complianceListUpcoming":          "answers {\"items\":null} (internal/compliance/handler.go, Handler.upcoming, the same unguarded map pattern as its sibling) against the contract's ComplianceDeadlinePage; the fix is the owning lane's",
	"lienGetHoldbackAging":            "answers {\"items\":null} (internal/lien/handler.go, Handler.aging, unguarded items map) against the contract's LienAgingRowPage; the fix is the owning lane's",
	"lienListJobDocuments":            "answers {\"items\":null} (internal/lien/handler.go, Handler.listDocuments, unguarded items map) against the contract's LienDocumentPage; the fix is the owning lane's",
	"lienListJobs":                    "answers {\"items\":null} (internal/lien/handler.go, Handler.listJobs, unguarded items map) against the contract's LienJobPage; the fix is the owning lane's",
	"purchasingListBills":             "answers {\"items\":null} (internal/purchasing/handler.go, Handler.listBills, unguarded items map) against the contract's PurchasingBillPage; the fix is the owning lane's",
	"purchasingListPOs":               "answers {\"items\":null} (internal/purchasing/handler.go, Handler.listPOs, unguarded items map) against the contract's PurchasingPOPage; the fix is the owning lane's",
	"purchasingListReceivingWorklist": "answers {\"items\":null} (internal/purchasing/handler.go, Handler.worklist, unguarded items map) against the contract's PurchasingWorklistItemPage; the fix is the owning lane's",
	"reportingListCommissionRules":    "answers {\"items\":null} (internal/reporting/handler.go, Handler.listRules, unguarded items map) against the contract's ReportingCommissionRulePage; the fix is the owning lane's",
	"reportingListRuns":               "answers {\"items\":null} (internal/reporting/handler.go, Handler.listRuns, unguarded items map) against the contract's ReportingRunPage; the fix is the owning lane's",
	"reportingListSavedViews":         "answers {\"items\":null} (internal/reporting/handler.go, Handler.listSavedViews, unguarded items map) against the contract's ReportingSavedViewPage; the fix is the owning lane's",
	"tillListBouncedPayments":         "answers {\"items\":null} (internal/till/handler.go, Handler.listBounced, unguarded items map) against the contract's TillBouncedPaymentPage; the fix is the owning lane's",
	"tillListCreditHolds":             "answers {\"items\":null} (internal/till/handler.go, Handler.listCreditHolds, unguarded items map) against the contract's TillCreditHoldPage; the fix is the owning lane's",

	// (3) no next_cursor (23): items (an array) is present, the answer
	// object carries no next_cursor key (the modules write ad hoc maps or
	// page structs that never carry the cursor field; the sync pull's
	// generated response type (next_cursor,omitempty) drops the nil
	// cursor the engine never sets).
	"billingGetARLandingPolicyHistory": "answers items with no next_cursor key (internal/billing/ar_landing_handler.go, ARLandingHandler.policyHistory writes map[string]any{\"items\": rows}) against the contract's BillingPolicyChangePage; the fix is the owning lane's",
	"billingGetAging":                  "answers as_of and items with no next_cursor key (internal/billing/monthend_handler.go, MonthEndHandler.aging, nil guarded items map) against the contract's BillingAgingPage; the fix is the owning lane's",
	"billingListARReminders":           "answers items (an array) with no next_cursor key (internal/billing/ar_landing_handler.go, ARLandingHandler.reminderHistory) against the contract's BillingReminderSendPage; the fix is the owning lane's",
	"billingListHolds":                 "answers items (an array) with no next_cursor key (internal/billing/monthend_handler.go, MonthEndHandler.listHolds) against the contract's BillingCreditHoldPage; the fix is the owning lane's",
	"billingListPaperPath":             "answers items (an array) with no next_cursor key (internal/billing/ar_landing_handler.go, ARLandingHandler.paperPath) against the contract's BillingPaperPathPage; the fix is the owning lane's",
	"billingListRegister":              "answers items (nil guarded), counts and total with no next_cursor key (internal/billing/register_handler.go, RegisterHandler.register writes the RegisterPage struct (internal/billing/register_model.go), which carries no cursor field) against the contract's BillingRegisterPage; the fix is the owning lane's",
	"glListAccounts":                   "answers items (nil guarded) with no next_cursor key (internal/gl/handler.go, Handler.listAccounts) against the contract's GlAccountPage; the fix is the owning lane's",
	"glListPeriods":                    "answers items (nil guarded) with no next_cursor key (internal/gl/handler.go, Handler.listPeriods) against the contract's GlPeriodPage; the fix is the owning lane's",
	"glListRecurring":                  "answers items (an array) with no next_cursor key (internal/gl/handler.go, Handler.listRecurring) against the contract's GlRecurringTemplatePage; the fix is the owning lane's",
	"glTieOut":                         "answers items (the tie out state rows) with no next_cursor key (internal/gl/handler.go, Handler.tieOut) against the contract's GlTieoutStatePage; the fix is the owning lane's",
	"invcountListReasonCodes":          "answers items (nil guarded, the seeded codes) with no next_cursor key (internal/invcount/handler.go, Handler.reasonCodes) against the contract's InvCountReasonCodePage; the fix is the owning lane's",
	"invcountListSheets":               "answers items (an array) with no next_cursor key (internal/invcount/handler.go, Handler.listSheets) against the contract's InvCountSheetPage; the fix is the owning lane's",
	"landedCostList":                   "answers items (an array, the migration seeded landed costs) with no next_cursor key (internal/landedcost/handler.go, Handler.list, unguarded items map) against the contract's LandedCostPage; the fix is the owning lane's",
	"lienGetTriage":                    "answers items (an array) with no next_cursor key (internal/lien/handler.go, Handler.triage, the same unguarded items map as its siblings) against the contract's LienTriageItemPage; the fix is the owning lane's",
	"posListSalespeople":               "answers items (an array, the migration seeded salespeople) with no next_cursor key (internal/pos/handler.go, Handler.listSalespeople, unguarded items map) against the contract's PosSalespersonPage; the fix is the owning lane's",
	"posLookupSales":                   "answers items (nil guarded) with no next_cursor key (internal/pos/handler.go, Handler.lookupSales) against the contract's PosSaleSummaryPage; the fix is the owning lane's",
	"posPolicyHistory":                 "answers items (an array) with no next_cursor key (internal/pos/handler.go, Handler.policyHistory) against the contract's PosPolicyChangePage; the fix is the owning lane's",
	"purchasingListVendors":            "answers items (an array, the migration seeded vendors) with no next_cursor key (internal/purchasing/handler.go, Handler.listVendors, unguarded items map) against the contract's PurchasingVendorPage; the fix is the owning lane's",
	"warrantyListClaims":               "answers items (an array) with no next_cursor key (internal/warranty/handler.go, Handler.listClaims) against the contract's WarrantyClaimPage; the fix is the owning lane's",
	"warrantyListSLATargets":           "answers items (an array, the migration seeded targets) with no next_cursor key (internal/warranty/handler_w5b.go, Handler.slaTargets) against the contract's WarrantySLATargetPage; the fix is the owning lane's",
	"warrantyListVendorProfiles":       "answers items (an array, the migration seeded profiles) with no next_cursor key (internal/warranty/handler.go, Handler.listVendorProfiles) against the contract's WarrantyVendorProfilePage; the fix is the owning lane's",
	"purchasingListVendorSKUs":         "answers items (an array, empty for the absent vendor) with no next_cursor key (internal/purchasing/vendor_order_handler.go, Handler.listVendorSKUs writes map[string]any{\"items\": items}) against the contract's PurchasingVendorSKUPage; the fix is the owning lane's",
	"syncPull":                         "answers scope, items, tombstones and last_seq with no next_cursor key (the engine never sets the cursor (internal/sync/engine.go, Engine.Pull) and the generated response type's tag is next_cursor,omitempty (internal/platform/apigen/types.gen.go), so the nil pointer drops from the JSON) against the contract's SyncPullResponse (next_cursor a string or null); the fix is the owning lane's",

	// (4) wrong key (12): the list sits under a key the contract does not
	// declare (items is the contract's spelling).
	"finstmtListSchedules":           "writes the (nil guarded) list under the key \"schedules\" (internal/finstmt/handler.go, Handler.listSchedules) against the contract's FinStmtSchedulePage (items, next_cursor); the fix is the owning lane's",
	"inventoryBrowse":                "writes the (nil guarded) list under the key \"rows\" (beside facets and total) (internal/inventory/stock_truth_handler.go, Handler.browse writes the BrowseResult struct) against the contract's InventoryBrowsePage; the fix is the owning lane's",
	"inventoryListPendingFulfilment": "writes the (nil guarded) list under the key \"pending\" (internal/inventory/handler.go, Handler.pendingFulfilment) against the contract's InventoryPendingFulfilmentPage; the fix is the owning lane's",
	"inventoryListReorderPoints":     "writes the (possibly nil) list under the key \"reorder_points\" (internal/inventory/stock_truth_handler.go, Handler.listReorderPoints, no nil guard) against the contract's InventoryReorderPointPage; the fix is the owning lane's",
	"inventoryListReservations":      "writes the (possibly nil) list under the key \"reservations\" (internal/inventory/stock_truth_handler.go, Handler.listReservations, no nil guard) against the contract's InventoryReservationPage; the fix is the owning lane's",
	"inventoryListTransfers":         "writes the (possibly nil) list under the key \"transfers\" (internal/inventory/stock_truth_handler.go, Handler.listTransfers, no nil guard; the observed empty answer was {\"transfers\":null}) against the contract's InventoryTransferPage; the fix is the owning lane's",
	"inventoryListTransitDays":       "writes the (nil guarded) list under the key \"pairs\" (internal/inventory/stock_truth_handler.go, Handler.transitDays) against the contract's InventoryTransitDaysPairPage; the fix is the owning lane's",
	"inventoryShortfall":             "writes the list under the key \"shortfall\" (beside on_order_degraded) (internal/inventory/stock_truth_handler.go, Handler.shortfall) against the contract's InventoryShortfallPage; the fix is the owning lane's",
	"taxListEvidence":                "writes the (possibly nil) list under the key \"evidence\" (internal/tax/handler.go, Handler.listEvidence, no nil guard; the observed empty answer was {\"evidence\":null}) against the contract's TaxEvidencePage; the fix is the owning lane's",
	"taxListProfiles":                "writes the list under the key \"profiles\" (internal/tax/handler.go, Handler.listProfiles) against the contract's TaxStatusCardProfilePage; the fix is the owning lane's",
	"taxListRates":                   "writes the list under the key \"rates\" (internal/tax/handler.go, Handler.listRates) against the contract's TaxRatePage; the fix is the owning lane's",
	"taxReviewQueue":                 "writes the (nil guarded) list under the key \"pending_profiles\" (internal/tax/handler.go, Handler.reviewQueue) against the contract's TaxStatusCardProfilePage; the fix is the owning lane's",
}

// shapeCensusUncovered names every page shape operation the fixture cannot
// drive to a 2xx, each with the reason (which callers refused, with the
// leg statuses the first census run observed, and what the fixture
// lacks). The census fails when an uncovered operation appears that this
// list does not name.
var shapeCensusUncovered = map[string]string{
	// The four documented operations the core does not register (the
	// parity ledger carries them, parity_ledger_test.go): the mux answers
	// 404 before any caller class is consulted.
	"getOperatorUsage":  "the operator launcher surface (D4) is not registered (parity_ledger_test.go: no implementation lane assigned); the mux answers 404 before any caller class is consulted (all legs 404)",
	"identityListRoles": "the staff identity admin surface is not registered (parity_ledger_test.go: no implementation lane assigned); the mux answers 404 before any caller class is consulted (all legs 404)",
	"identityListStaff": "the staff identity admin surface is not registered (parity_ledger_test.go: no implementation lane assigned); the mux answers 404 before any caller class is consulted (all legs 404)",
	"listPlatformKeys":  "the operator launcher surface (D4) is not registered (parity_ledger_test.go: no implementation lane assigned); the mux answers 404 before any caller class is consulted (all legs 404)",

	// The required query parameter family (the brief's fill discipline is
	// path parameters (the kit's planted ids); the census sends no query
	// string, so a handler that requires one stays beyond the drive. Each
	// reason names the parameter (and its source), so widening the census
	// (a query fill (the planted ids again)) is a conscious one line edit.
	"bankrecListLines":               "the service requires the bank_account_id query (internal/bankrec/service.go, Service.Lines answers badRequest (bank_account_id is required) when it is absent); the census sends no query string, so no caller reaches a 2xx (tier legs 400, yard_team 403, the money desk's role gate (the route's cells), which is a correct gate answer, not a handler defect)",
	"bankrecListMatches":             "the service requires the statement_line_id query (internal/bankrec/service.go, Service.MatchHistory, statement_line_id is required); tier legs 400, yard_team 403 (the money desk's gate)",
	"dispatchGetLoadBoardCalendar":   "the service requires the branch_id query (plus from and to as dates) (internal/dispatch/service_loadboard.go, Service.Calendar, dispatch: branch_id is required); only the general manager leg passes the role gate (its 400 is the guard), the scoped roles' 403 is the dispatch-board gate (the route's cells)",
	"dispatchGetLoadBoardGaps":       "the service requires the branch_id query (internal/dispatch/service_loadboard.go, Service.BoardGaps, the same named missing field guard); GM 400 (the guard), the rest 403 (the dispatch-board gate)",
	"dispatchGetOrderGaps":           "the service checks the branch_id query before the (planted) order id (internal/dispatch/service_loadboard.go, Service.GapsForOrder, branch_id is required); GM 400 (the guard), the rest 403 (the dispatch-board gate); the fill (testkit.OrderAlphaConfID) sits behind the branch guard",
	"dispatchGetPolicyHistory":       "the service requires the branch_id query (internal/dispatch/service_loadboard.go, Service.PolicyHistory, behind the manager check and requireBranch); GM 400 (the guard), the rest 403 (the dispatch-board gate)",
	"dispatchListDrivers":            "the service requires the branch_id query (internal/dispatch/service.go, Service.Drivers, dispatch: branch_id is required); GM 400 (the guard), the rest 403 (the dispatch-board gate)",
	"dispatchListRuns":               "the service requires the branch_id query (internal/dispatch/service.go, Service.Runs, the same named guard); GM 400 (the guard), the rest 403 (the dispatch-board gate)",
	"dispatchListTrucks":             "the service requires the branch_id query (internal/dispatch/service.go, Service.Trucks, the same named guard); GM 400 (the guard), the rest 403 (the dispatch-board gate)",
	"dispatchListWillCall":           "the service requires the order_id query (internal/dispatch/orderref.go, gradeOrderRef answers (enter an order number or an order id) when the query is absent); the dispatch-board legs the gate admits (sales_manager, GM, branch_manager, yard_team) answer 400 (the guard); the counter and money legs 403 (the dispatch-board gate). The kit plants orders (testkit.OrderAlphaDraftID beside the lifecycle set), but the census's fill discipline (path parameters) carries no query fill",
	"dispatchListMyRuns":             "the gate admits the branch_manager leg (the drive-run read cell; the other legs 403 (the drive-run cells, LevelNone for the general manager role among them)) and the handler then resolves the caller's dispatch_drivers row (internal/dispatch/repository.go, DriverForUser, ErrNotFound on no rows); the kit plants a driver_profile (testkit/dbfixture.go) but never a dispatch_drivers row, so the not-found family (404) answers every gate passing caller (planting the dispatch driver row is the owning lane's fixture widen)",
	"dispatchListRunPaymentHolds":    "the kit plants no dispatch run row (the guardRun lookup (internal/dispatch/repository.go, GetRun, ErrNotFound) answers the 404 family for the gate passing legs (sales_manager, branch_manager, yard_team, GM); the rest 403 (the dispatch-board gate)); no run id the kit carries can fill the path (the brief's discipline (kit ids only))",
	"dispatchListRunReschedules":     "the kit plants no dispatch run row (the same guardRun (GetRun, ErrNotFound) behind the same leg split: 404 for the gate passing legs, 403 for the rest)",
	"dispatchListStopReschedules":    "the kit plants no dispatch stop row (guardStop (GetStop, ErrNotFound) in internal/dispatch/service.go, the same leg split: 404 for the gate passing legs, 403 for the rest)",
	"invcountListNegativeStock":      "the handler reads the branch_id query and passes the (absent) empty string into a uuid cast (internal/invcount/handler.go, negativeStock, via Service.NegativeStock (SnapshotOnHand (internal/inventory/repository.go, WHERE sb.branch_id = $1 with the empty string))); Postgres refuses the cast and the raw error lands in the module's 500 arm (all legs 500). No query string means no 2xx, and the empty value is a 500 where a 400 would be honest (a finding the owning lane may want beside the page fix)",
	"inventoryBranchStock":           "the handler requires the product_id query (internal/inventory/visibility.go, AvailabilityAllBranches, product_id is required); all legs 400 (no query string means no 2xx; the kit's planted product id (testkit.ProductID) would answer, but a query fill is outside the brief's path parameter discipline)",
	"inventoryListIncomingTransfers": "the handler requires the product_id query (internal/inventory/incoming.go, InTransitForProduct, the same guard); all legs 400",
	"inventoryListMoves":             "the handler requires the product_id query (internal/inventory/handler.go, productIDParam, product_id is required); all legs 400",
	"inventoryOnHand":                "the handler requires the product_id query (internal/inventory/handler.go, productIDParam, the same guard; the role gate admits the tier legs (the matrix suite's admitted leg for this route proves it) and the 400 is the handler's own guard, not a gate refusal); all legs 400",
	"posListParked":                  "the service requires the branch_id query (internal/pos/policy.go, Service.ListParked, pos: branch_id is required); the counter till legs (inside_sales, sales_manager, GM, branch_manager) answer 400 (the guard), the money and yard legs 403 (the counter till gate (their cells do not reach the route))",
	"posListPricingOrders":           "the service requires the branch_id query (internal/pos/pricing_orders.go, Service.ListPricingOrders, the same guard); the same leg split (counter legs 400 (the guard), money and yard legs 403 (the counter till gate))",
	"posListReturnPickups":           "the service requires the branch_id query (internal/pos/returns.go, Service.ListReturnPickups); the dispatch-board legs (sales_manager, GM, branch_manager) answer 400 (the guard), the counter and money legs 403 (the dispatch-board gate (the route's surface))",
	"purchasingListIncoming":         "the handler requires the product_id query (internal/purchasing/incoming.go, IncomingForProduct, purchasing: product_id is required); all legs 400",
	"reportingCommissionPreview":     "the service requires the from and to date queries (internal/reporting/commission_service.go, parseWindow (from must be a date (YYYY-MM-DD)) when they are absent); the manager legs the gate admits answer 400 (the guard), the tier 3 legs (inside_sales, yard_team) 403 (the manager home gate (their cells do not reach the route))",
	"reportingListCommissionRuns":    "the service requires the from and to date queries (internal/reporting/commission_service.go, parseWindow, the same guard); the same leg split (manager legs 400 (the guard), tier 3 legs 403 (the manager home gate))",

	// The absent parent row family (the kit plants no row the path can
	// name, so the rig's well-formed absent uuid (xrProbePath,
	// xrole_assert_test.go) reaches the handler's not-found family; the
	// census adds tests only (the brief's fences), it plants no rows).
	"lienListDocumentVersions":    "the kit plants no lien document row (internal/lien/repository.go, DocumentVersions, errDocNotFound on the empty version set); the manager legs the RequireManager gate admits answer the 404 family, inside_sales and yard_team 403 (the gate (their cells do not reach the route))",
	"messagingListMessages":       "the kit plants no messaging channel row (the lookup (assertMember, internal/messaging/service.go) answers ErrNotFound for the absent channel before the membership check); every staff leg (the route carries requireStaff alone) answers the 404 family",
	"purchasingListPOSends":       "the kit plants no purchase order row (internal/purchasing/repository.go, GetPO, errNotFound on pgx.ErrNoRows); every staff leg (requireStaff admits all) reaches the handler and answers the 404 family",
	"warrantyGetClaimChecklist":   "the kit plants no warranty claim row (internal/warranty/service.go, callerScopedClaim (store.GetClaim (errClaimNotFound, internal/warranty/repository.go))); the manager legs the gate admits answer the 404 family, inside_sales and yard_team 403 (the manager home gate (their cells)); no claim id the kit carries can fill the path (the brief's discipline (kit ids only))",
	"warrantyListClaimEvents":     "the kit plants no warranty claim row (the same callerScopedClaim (errClaimNotFound) behind the same leg split: 404 for the gate passing legs, 403 for inside_sales and yard_team)",
	"warrantyListClaimPostings":   "the kit plants no warranty claim row (the same callerScopedClaim (errClaimNotFound) (internal/warranty/moneypath.go), the same leg split)",
	"warrantyListIntakePhotos":    "the kit plants no warranty claim row (the same callerScopedClaim (errClaimNotFound) (internal/warranty/intake.go), the same leg split)",
	"warrantyListRecoveryActions": "the kit plants no warranty claim row (the same callerScopedClaim (errClaimNotFound) (internal/warranty/recovery.go), the same leg split)",

	// The handler's own fence (beyond the role gate: the gate admits, the
	// fence (a query the route's own vocabulary demands) still refuses).
	"yardListPendingTickets": "the route's cells sit with yard_team alone (the yard-pick work cell per roleconfig), so every other planted caller 403s at the role gate, and the admitting leg itself is refused by the handler's own branch fence (internal/tasks/yard_handler.go, yardBranchFence: a branch scoped caller must name a reachable branch_id query, the handler's own 403). No planted caller reaches a 2xx: the kit's driver principal holds no yard-pick cell either (driver x yard-pick is LevelNone, internal/roleconfig/matrix_test.go), so the role gate 403s it before the fence",
	"yardListPickQueue":      "the same shape as its sibling (yard-pick cells (yard_team alone), then the handler's own branch fence (yardBranchFence (a branch scoped caller must name a reachable branch_id query))); all legs 403 (the gate for six, the fence for the seventh)",
}

// scPageOp is one page shape operation of the contract, every fact (but
// the bearer shape) read straight off the document.
type scPageOp struct {
	OperationID string
	Method      string
	Path        string
	Tag         string
	Bearer      string // staff, tenant, staff-or-tenant, public (derived from the document's security)
	Schema      string // the component schema the success response names ("" when inline)
}

// scReadContract reads the merged, self contained contract (edit nothing
// there) relative to this file, so the census runs from whatever directory
// go test picks.
func scReadContract(t *testing.T) map[string]any {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not name this file; the census cannot find the contract")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read the contract: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the contract (gopkg.in/yaml.v3): %v", err)
	}
	return doc
}

// scResolveSchema follows $ref hops into the document's component schemas.
// The merged document is acyclic by construction; the hop cap only keeps a
// malformed document from spinning the census. A node that is not a
// mapping (or names a schema the document does not carry) resolves to nil.
func scResolveSchema(doc map[string]any, node any) map[string]any {
	for hop := 0; hop < 8; hop++ {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		ref, ok := m["$ref"].(string)
		if !ok || ref == "" {
			return m
		}
		schemas, _ := doc["components"].(map[string]any)["schemas"].(map[string]any)
		name := ref[strings.LastIndex(ref, "/")+1:]
		node, ok = schemas[name]
		if !ok {
			return nil
		}
	}
	return nil
}

// scRefName reports the component schema name a node names by ref ("" when
// it names none), so the census can say which page schema it read.
func scRefName(node any) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	ref, _ := m["$ref"].(string)
	return ref[strings.LastIndex(ref, "/")+1:]
}

// scPageShape reports whether a (resolved) response schema is a page
// shape: a top level object carrying BOTH properties, items (type array)
// and next_cursor (string, or the string or null union the contract
// spells for the empty page).
func scPageShape(doc map[string]any, schema map[string]any) bool {
	if schema == nil {
		return false
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return false
	}
	items := scResolveSchema(doc, props["items"])
	if items == nil {
		return false
	}
	if t, _ := items["type"].(string); t != "array" {
		return false
	}
	nc := scResolveSchema(doc, props["next_cursor"])
	if nc == nil {
		return false
	}
	switch t := nc["type"].(type) {
	case string:
		return t == "string"
	case []any:
		hasString, hasNull := false, false
		for _, v := range t {
			switch v {
			case "string":
				hasString = true
			case "null":
				hasNull = true
			}
		}
		return hasString && hasNull
	}
	return false
}

// scSuccessSchema returns the success response's JSON schema (the 200, and
// the 201 where an operation declares its success there instead) and the
// name of the component schema it refs ("" for an inline schema). No
// success JSON schema (or none at the documented codes) returns nil.
func scSuccessSchema(doc map[string]any, op map[string]any) (string, map[string]any) {
	responses, _ := op["responses"].(map[string]any)
	if responses == nil {
		return "", nil
	}
	for _, code := range []string{"200", "201"} {
		raw, ok := responses[code]
		if !ok {
			continue
		}
		resp := scResolveSchema(doc, raw)
		if resp == nil {
			continue
		}
		content, _ := resp["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)
		if media == nil {
			continue
		}
		schemaAny, ok := media["schema"]
		if !ok {
			continue
		}
		return scRefName(schemaAny), scResolveSchema(doc, schemaAny)
	}
	return "", nil
}

// scBearerShape derives the bearer shape the document declares for the
// operation (its own security requirement list, else the document default
// the header carries): the empty list is the public class, both bearer
// schemes together the sync class's staff-or-tenant, either alone its own.
func scBearerShape(t *testing.T, op map[string]any, docDefault any) string {
	t.Helper()
	sec, ok := op["security"]
	if !ok {
		sec = docDefault
	}
	requirements, ok := sec.([]any)
	if !ok {
		t.Fatalf("the operation's security carries no requirement list (got %T)", sec)
	}
	if len(requirements) == 0 {
		return "public"
	}
	staff, tenant := false, false
	for _, r := range requirements {
		req, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := req["StaffBearer"]; ok {
			staff = true
		}
		if _, ok := req["TenantBearer"]; ok {
			tenant = true
		}
	}
	switch {
	case staff && tenant:
		return "staff-or-tenant"
	case staff:
		return "staff"
	case tenant:
		return "tenant"
	}
	t.Fatalf("the operation's security names neither bearer scheme the document defines (%v)", requirements)
	return ""
}

// scWalkContractPages parses the (already unmarshalled) document and
// collects every operation whose success response resolves to a page
// shape, sorted by operationId, each with its derived bearer shape.
func scWalkContractPages(t *testing.T, doc map[string]any) []scPageOp {
	t.Helper()
	paths, _ := doc["paths"].(map[string]any)
	if paths == nil {
		t.Fatal("the document carries no paths (is this the merged contract?)")
	}
	var ops []scPageOp
	for path, pathItemAny := range paths {
		pathItem, ok := pathItemAny.(map[string]any)
		if !ok {
			continue
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			opAny, ok := pathItem[method]
			if !ok {
				continue
			}
			op, ok := opAny.(map[string]any)
			if !ok {
				continue
			}
			operationID, _ := op["operationId"].(string)
			if operationID == "" {
				continue
			}
			schemaName, schema := scSuccessSchema(doc, op)
			if !scPageShape(doc, schema) {
				continue
			}
			tag := ""
			if tags, ok := op["tags"].([]any); ok && len(tags) > 0 {
				tag, _ = tags[0].(string)
			}
			ops = append(ops, scPageOp{
				OperationID: operationID,
				Method:      strings.ToUpper(method),
				Path:        path,
				Tag:         tag,
				Bearer:      scBearerShape(t, op, doc["security"]),
				Schema:      schemaName,
			})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].OperationID < ops[j].OperationID })
	return ops
}

// scCrossCheckRouteTable holds every page shape operation against the
// generated route table (core/internal/platform/apigen, regenerated from
// the same document) by operationId: method, path, tag and bearer shape
// must agree, or the census (which drives the table's registrations from
// the document's facts) would test a fiction.
func scCrossCheckRouteTable(t *testing.T, ops []scPageOp) {
	t.Helper()
	byID := make(map[string]apigen.Route, len(apigen.Routes))
	for _, route := range apigen.Routes {
		byID[route.OperationID] = route
	}
	for _, op := range ops {
		route, ok := byID[op.OperationID]
		if !ok {
			t.Fatalf("page shape operation %s (%s %s) is in the document but the route table does not carry it; regenerate the census (node core/api/tools/gen-types.mjs --write)", op.OperationID, op.Method, op.Path)
		}
		if route.Method != op.Method || route.Path != op.Path {
			t.Fatalf("page shape operation %s: the document says %s %s, the route table says %s %s", op.OperationID, op.Method, op.Path, route.Method, route.Path)
		}
		if route.Tag != op.Tag {
			t.Fatalf("page shape operation %s: the document tags it %q, the route table says %q", op.OperationID, op.Tag, route.Tag)
		}
		if route.Bearer != op.Bearer {
			t.Fatalf("page shape operation %s: the document's security derives the %q bearer shape, the route table says %q", op.OperationID, op.Bearer, route.Bearer)
		}
	}
}

// scJSONKind names a decoded JSON value's kind (the mismatch sentences'
// vocabulary).
func scJSONKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return fmt.Sprintf("a %T", v)
}

// scPageMismatch holds one answer body against the page shape and reports
// what it lacks; "" when the body IS the shape (a top level JSON object
// carrying items as an array and next_cursor as a string or null). A bare
// array answer, an object missing either key, or a wrong typed key is a
// mismatch (the brief's sentence, word for word).
func scPageMismatch(body string) string {
	var decoded any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &decoded); err != nil {
		return fmt.Sprintf("the answer is not JSON (%v)", err)
	}
	switch v := decoded.(type) {
	case []any:
		return fmt.Sprintf("the answer is a bare JSON array (%d elements), not a page object", len(v))
	case map[string]any:
		items, ok := v["items"]
		if !ok {
			return "the answer object carries no items key"
		}
		if _, ok := items.([]any); !ok {
			return fmt.Sprintf("the items key is %s, not an array", scJSONKind(items))
		}
		nc, ok := v["next_cursor"]
		if !ok {
			return "the answer object carries no next_cursor key"
		}
		switch nc.(type) {
		case string, nil:
		default:
			return fmt.Sprintf("the next_cursor key is %s, not a string or null", scJSONKind(nc))
		}
		return ""
	default:
		return fmt.Sprintf("the answer is %s, not a page object", scJSONKind(decoded))
	}
}

// shapeCensusFixtureDoc is the (a) proof's document: a minimal contract
// whose one operation is a page shape (the approvals page rebuilt in
// miniature, the same spelling the merged document uses: a ref to the
// component schema, items an array, next_cursor the string or null union,
// security inherited from the document default).
const shapeCensusFixtureDoc = `openapi: 3.1.0
info:
  title: the shape census fixture document
  version: "1"
security:
  - StaffBearer: []
paths:
  /api/v1/staff/approvals:
    get:
      operationId: fixtureListApprovals
      tags:
        - staff-fixture
      responses:
        "200":
          description: The fixture page.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/FixtureApprovalPage"
components:
  securitySchemes:
    StaffBearer:
      type: http
      scheme: bearer
  schemas:
    FixtureApprovalPage:
      type: object
      required:
        - items
        - next_cursor
      properties:
        items:
          type: array
          items:
            $ref: "#/components/schemas/FixtureApproval"
        next_cursor:
          type:
            - string
            - "null"
    FixtureApproval:
      type: object
      properties:
        id:
          type: string
`

// TestShapeCensusContractKnowsEveryPageShape is the ungated half (no
// database): the walk derives the page shape operations from the merged
// document, the pin holds the census's knowing, and every operation is
// cross checked against the route table by operationId.
func TestShapeCensusContractKnowsEveryPageShape(t *testing.T) {
	ops := scWalkContractPages(t, scReadContract(t))
	if len(ops) != shapeCensusWantPages {
		t.Fatalf("the document carries %d page shape operations, want the pinned %d; a page shape was added or removed: run the live census (it drives or uncovers the new operation) and move the pin with that evidence", len(ops), shapeCensusWantPages)
	}
	scCrossCheckRouteTable(t, ops)
	bearers := map[string]int{}
	for _, op := range ops {
		bearers[op.Bearer]++
	}
	t.Logf("the document carries %d page shape operations, all cross checked against the route table (bearer shapes: %v)", len(ops), bearers)
}

// TestShapeCensusCheckerFailsTheBareArray is the (a) fail first proof (no
// database): the parse and classify half fed a fixture document (the
// minimal YAML above, built in this file) where the page shape operation
// is served by a handler answering a bare array. The walk must classify
// the operation, the checker must refuse the bare array (else the census
// cannot detect the mismatch it exists to catch and cannot be trusted),
// and (the positive control) the checker must pass the same document's
// page shaped answer (else the census cries wolf and the red means
// nothing).
func TestShapeCensusCheckerFailsTheBareArray(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(shapeCensusFixtureDoc), &doc); err != nil {
		t.Fatalf("parse the fixture document: %v", err)
	}
	ops := scWalkContractPages(t, doc)
	if len(ops) != 1 {
		t.Fatalf("the fixture document classifies %d page shape operations, want exactly one (fixtureListApprovals)", len(ops))
	}
	if ops[0].OperationID != "fixtureListApprovals" || ops[0].Method != "GET" || ops[0].Path != "/api/v1/staff/approvals" || ops[0].Bearer != "staff" || ops[0].Schema != "FixtureApprovalPage" {
		t.Fatalf("the walk derived %+v, want fixtureListApprovals (GET /api/v1/staff/approvals, staff bearer from the document default, FixtureApprovalPage)", ops[0])
	}
	// The fixture handler (the defect rebuilt in miniature): a 200 whose
	// body is a bare JSON array.
	bare := []map[string]string{{"id": "3ef10000-0000-4000-8000-0f0000000001"}}
	mux := http.NewServeMux()
	served := any(bare)
	mux.HandleFunc("GET /api/v1/staff/approvals", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(served)
	})
	rsp := httptest.NewRecorder()
	mux.ServeHTTP(rsp, httptest.NewRequest(http.MethodGet, "/api/v1/staff/approvals", nil))
	if rsp.Code != http.StatusOK {
		t.Fatalf("the fixture handler answered %d, want 200", rsp.Code)
	}
	mismatch := scPageMismatch(rsp.Body.String())
	if mismatch == "" {
		t.Fatal("the checker passed the fixture handler's bare array answer: the census cannot detect the mismatch it exists to catch (fail first, detection unproven)")
	}
	t.Logf("fail first (a): the checker refused the bare array answer: %s", mismatch)

	// The positive control: the same document, a handler answering the page
	// shape (items an array, next_cursor null (the empty page's spelling)).
	served = map[string]any{"items": bare, "next_cursor": nil}
	rsp = httptest.NewRecorder()
	mux.ServeHTTP(rsp, httptest.NewRequest(http.MethodGet, "/api/v1/staff/approvals", nil))
	if mismatch := scPageMismatch(rsp.Body.String()); mismatch != "" {
		t.Fatalf("the checker refused a page shaped answer (%s): the census would cry wolf (fail first, control unproven)", mismatch)
	}
	t.Logf("fail first (a): the checker passed the page shaped control answer (items array, next_cursor null)")
}

// TestShapeCensusStaleUncoveredEntryFailsTheVerdict is the round 2 P2 fail-first (a) leg,
// with no database. The same fixture document, the same walk and the SAME verdict code path
// the live census collects its report from (scVerdict): the fixture operation is named by
// the leg's own uncovered map, and a 2xx page answer is fed through the verdict. The verdict
// must classify the operation as a stale uncovered entry carrying the failure line the live
// census fails on. A verdict that stays green here would mean the uncovered list can never
// shrink and the mirror discipline would be unproven.
func TestShapeCensusStaleUncoveredEntryFailsTheVerdict(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(shapeCensusFixtureDoc), &doc); err != nil {
		t.Fatalf("parse the fixture document: %v", err)
	}
	ops := scWalkContractPages(t, doc)
	if len(ops) != 1 {
		t.Fatalf("the fixture document classifies %d page shape operations, want exactly one (fixtureListApprovals)", len(ops))
	}
	// The leg's own uncovered map: the fixture operation named undrivable.
	uncov := map[string]string{ops[0].OperationID: "the fixture leg names this operation undrivable (a stand in for shapeCensusUncovered)"}
	// A 2xx page answer for that named operation: the fixture widen rebuilt in miniature.
	out := scOutcome{legs: "fixture handler 200", status: http.StatusOK, body: `{"items":[],"next_cursor":null}`}
	findings := scVerdict(ops[0], out, shapeCensusAllowlist, uncov)
	stale := scFinding{}
	hasStale := false
	for _, f := range findings {
		if f.kind == scStaleUncovered && f.line != "" {
			stale = f
			hasStale = true
		}
	}
	if !hasStale {
		t.Fatalf("the verdict for a driven operation the uncovered list names produced %d findings with no scStaleUncovered finding carrying a failure line; the uncovered list cannot shrink and the mirror is unproven (fail first, unproven)", len(findings))
	}
	for _, f := range findings {
		if f.kind != scStaleUncovered && f.kind != scClean {
			t.Fatalf("the verdict for a clean 2xx page answer over an uncovered-named operation produced an unexpected extra kind %v: only scStaleUncovered and scClean belong here; the clean answer itself is no fault, the stale entry is the fault to name", f.kind)
		}
	}
	t.Logf("fail first (the stale uncovered mirror): a 2xx drive of an uncovered-named operation classifies as the census failure kind (%s)", stale.line)
}

// scCaller is one ladder leg: a planted principal and the headers its
// bearer class carries. Staff legs select the org; tenant legs carry the
// tenant id inside the token. The anon leg (the public class) sends no
// credential at all (the x-role suite's leg zero).
type scCaller struct {
	name     string
	anon     bool   // no credential (the public class's leg)
	tenant   bool   // mint a tenant (portal) bearer instead of a staff bearer
	sub      string // the platform subject (testkit or the matrix planting)
	tenantID string // the tenant id for tenant legs ("" on staff legs)
}

// scStaffLadder is the staff legs in role reach order (the brief's): the
// kit's three tier subjects first (compfixture wires their directory
// memberships; testkit/dbfixture.go plants the users and grant rows),
// then the matrix roles (plantMatrixStaff, rolegate_composition_test.go),
// whose cells reach the routes the tier legs do not.
var scStaffLadder = []scCaller{
	{name: "inside_sales (tier 3, branch A)", sub: testkit.SubInsideSales},
	{name: "sales_manager (tier 2, branch A)", sub: testkit.SubSalesManager},
	{name: "general_manager (tier 1, global)", sub: testkit.SubGeneralMgr},
	{name: "financial_controller (matrix, global)", sub: xrSubFinancialController},
	{name: "branch_manager (matrix, branch A)", sub: xrSubBranchManager},
	{name: "procurement_manager (matrix, branch A)", sub: xrSubProcurementManager},
	{name: "yard_team (matrix, branch A)", sub: xrSubYardTeam},
}

// scTenantLadder is the tenant legs: the portal principals the kit plants
// (testkit/dbfixture.go). The alpha account admin first (it carries the
// full capability set), then the alpha field crew and the gamma ap
// finance (a second tenant, for the cross tenant leg's honesty).
var scTenantLadder = []scCaller{
	{name: "portal account admin (alpha)", tenant: true, sub: testkit.SubPortalAdminAlpha, tenantID: testkit.TenantAlphaID},
	{name: "portal field crew (alpha)", tenant: true, sub: testkit.SubPortalCrewAlpha, tenantID: testkit.TenantAlphaID},
	{name: "portal ap finance (gamma)", tenant: true, sub: testkit.SubPortalApGamma, tenantID: testkit.TenantGammaID},
}

// scLadderFor picks the ladder the operation's derived bearer shape names:
// staff (the staff legs), tenant (the tenant legs), staff-or-tenant (both,
// staff first: the sync pair admits either class), public (the anonymous
// leg: the products catalogue list is the one page shape the document
// declares pre-auth).
func scLadderFor(op scPageOp) []scCaller {
	switch op.Bearer {
	case "staff":
		return scStaffLadder
	case "tenant":
		return scTenantLadder
	case "staff-or-tenant":
		return append(append([]scCaller(nil), scStaffLadder...), scTenantLadder...)
	case "public":
		return []scCaller{{name: "anonymous (the public class, no credential)", anon: true}}
	}
	return nil
}

// scFixturePath fills the documented path template with the ids the
// fixture carries. Every fill is named here with its source, per the
// brief (name every id you use and where it comes from):
//
//   - auditReopenTransaction {entityType}: the literal "order", an entity
//     type the audit trail carries rows for (the kit plants order
//     fixtures). {entityId} (and dispatchGetOrderGaps {id}):
//     testkit.OrderAlphaConfID, the kit's planted CONFIRMED order in the
//     north org, testkit/dbfixture.go.
//   - pricingListOverrides {customerId}: testkit.TenantAlphaID, the kit's
//     planted contractor account (the customers row,
//     testkit/dbfixture.go).
//   - syncPull {scope}: "pick_tasks", a scope whose store the binary
//     wires (cmd/core/wire_operations.go, tasks.NewSyncStore) and whose
//     registry row carries org tenancy (internal/sync/registry.go), so a
//     staff caller is admitted. An unwired scope answers the handler's
//     501 (internal/sync/handler.go), which the first census draft
//     (scope "drivers") proved.
//   - purchasingListVendorSKUs {vendorID}: xrUUIDParam, the rig's
//     well-formed absent uuid (xrole_assert_test.go): the parameter
//     reaches a Postgres uuid cast (internal/purchasing/vendor_order_repository.go,
//     WHERE m.vendor_id = $1::uuid), and the kit plants no vendor row.
//
// Every other parameter (the run, stop, claim, batch, purchase order,
// channel and lien document ids) has no fixture row planted behind it:
// those fills are the x-role rig's own well-formed absent uuid
// (xrProbePath), which reaches the handler's not-found family (the rig's
// own discipline: a uuid, never a parse refusal). The operations behind
// them (when no caller reaches a 2xx) surface on the census's uncovered
// list with exactly that reason (shapeCensusUncovered).
func scFixturePath(t *testing.T, op scPageOp) string {
	t.Helper()
	var fills map[string]string
	switch op.OperationID {
	case "auditReopenTransaction":
		fills = map[string]string{
			"{entityType}": "order",
			"{entityId}":   testkit.OrderAlphaConfID,
		}
	case "dispatchGetOrderGaps":
		fills = map[string]string{"{id}": testkit.OrderAlphaConfID}
	case "pricingListOverrides":
		fills = map[string]string{"{customerId}": testkit.TenantAlphaID}
	case "syncPull":
		fills = map[string]string{"{scope}": "pick_tasks"}
	case "purchasingListVendorSKUs":
		fills = map[string]string{"{vendorID}": xrUUIDParam}
	default:
		return xrProbePath(op.Path)
	}
	path := op.Path
	for k, v := range fills {
		path = strings.ReplaceAll(path, k, v)
	}
	return path
}

// scSnippet trims an answer body to the evidence length the failure
// report carries (the shape sentence is the finding; the snippet names
// the body).
func scSnippet(body string) string {
	const max = 240
	body = strings.TrimSpace(body)
	if len(body) > max {
		return body[:max] + "..."
	}
	return body
}

// scOutcome is one operation's drive: the legs (caller name and answer
// status, in ladder order up to and including the 2xx), and the admitting
// answer (status and body) when one caller reached a 2xx.
type scOutcome struct {
	legs   string
	status int
	body   string
}

// scDrive walks the ladder (in order) until one caller answers 2xx (the
// brief's caller ladder discipline). Headers are minted per leg: staff
// legs carry the org selection, tenant legs the tenant id in the token.
func scDrive(t *testing.T, fx *xrFixture, op scPageOp, path string, ladder []scCaller) scOutcome {
	t.Helper()
	var names []string
	for _, caller := range ladder {
		headers := map[string]string{}
		switch {
		case caller.anon:
			headers = nil
		case caller.tenant:
			headers["Authorization"] = "Bearer " + fx.env.minter.TenantToken(t, caller.sub, caller.tenantID)
		default:
			headers["Authorization"] = "Bearer " + fx.env.minter.StaffToken(t, caller.sub)
			headers[authz.HeaderXOrg] = testkit.OrgNorthSlug
		}
		code, body := fx.probe(t, op.Method, path, headers)
		names = append(names, fmt.Sprintf("%s %d", caller.name, code))
		if code >= 200 && code < 300 {
			return scOutcome{legs: strings.Join(names, ", "), status: code, body: body}
		}
	}
	return scOutcome{legs: strings.Join(names, ", ")}
}

// scVerdictKind is one drive verdict, the census's whole judgment about one operation,
// passed as a typed kind so the fixture leg can assert the classification itself.
type scVerdictKind int

const (
	scClean scVerdictKind = iota
	scAllowHit
	scFreshMismatch
	scStaleAllow
	scStaleUncovered
	scUncoveredNamed
	scUncoveredUnnamed
)

// scFinding is one verdict outcome: its kind and the report line the census carries for it
// (empty for the silent kinds, clean and allow hit, whose tallies use the operation name
// instead).
type scFinding struct {
	kind scVerdictKind
	line string
}

// scVerdict is THE one verdict code path: the live census collects its report from it, and
// the ungated fixture leg proves the stale uncovered discipline through this same function;
// no second implementation exists. The maps arrive as parameters so the fixture leg passes
// its own stand-ins and never mutates the package maps the live census reads.
//
// The uncovered list's mirror discipline (the round 2 P2 fix) lives here: an operation
// shapeCensusUncovered names that a caller just drove to a 2xx is a stale uncovered entry
// regardless of the answer's shape (the fixture widened, or the handler changed; either way
// the list must shrink), the allowlist's stale discipline has its mirror at last, and a name
// on both lists at once is refused at the live census's start as incoherent.
func scVerdict(op scPageOp, out scOutcome, allow, uncov map[string]string) []scFinding {
	var f []scFinding
	if out.status != 0 {
		if _, named := uncov[op.OperationID]; named {
			f = append(f, scFinding{scStaleUncovered, fmt.Sprintf("%s %s %s, reached a 2xx answer (status %d, via %s) while shapeCensusUncovered still names it: fixture widened, or handler changed; either way the uncovered list must shrink. Drop the entry, and read the answer at source: fresh mismatch until allowlisted or clean.", op.OperationID, op.Method, op.Path, out.status, out.legs)})
		}
		if mismatch := scPageMismatch(out.body); mismatch != "" {
			if _, allowlisted := allow[op.OperationID]; allowlisted {
				f = append(f, scFinding{scAllowHit, ""})
			} else {
				f = append(f, scFinding{scFreshMismatch, fmt.Sprintf("%s (%s %s, contract schema %s): %s (status %d via %s; body: %s)", op.OperationID, op.Method, op.Path, op.Schema, mismatch, out.status, out.legs, scSnippet(out.body))})
			}
			return f
		}
		if _, allowlisted := allow[op.OperationID]; allowlisted {
			f = append(f, scFinding{scStaleAllow, fmt.Sprintf("%s (%s %s) now answers the page shape (the allowlist entry is stale (the owning lane's fix landed), drop it (the parity ledger's shrink discipline))", op.OperationID, op.Method, op.Path)})
			return f
		}
		f = append(f, scFinding{scClean, ""})
		return f
	}
	if reason, named := uncov[op.OperationID]; named {
		f = append(f, scFinding{scUncoveredNamed, fmt.Sprintf("%s (%s %s): %s (this run: %s)", op.OperationID, op.Method, op.Path, reason, out.legs)})
		return f
	}
	f = append(f, scFinding{scUncoveredUnnamed, fmt.Sprintf("%s, %s, %s, no caller in the %s ladder reached a 2xx (%s); name it in shapeCensusUncovered with the reason read at source, or widen the fixture: a planted row, a uuid fill in scFixturePath, or a conscious query fill noted in the report, so a caller can drive it.", op.OperationID, op.Method, op.Path, op.Bearer, out.legs)})
	return f
}

// TestShapeCensusListPagesServed is the live census, gated per the compfixture discipline
// (it skips politely without the gate variable). Every page shape operation of the document
// is driven through the real mux (the rig's registerRoutes over the identity environment
// map and the gate database) with the caller ladder until one caller answers 2xx, and the
// answer body is held to the page shape (items an array, next_cursor a string or null)
// through scVerdict, the one verdict code path the fixture leg shares. A mismatch the
// allowlist does not name, an uncovered operation the named list does not carry, a stale
// allowlist entry (an operation the allowlist names that now answers the page shape), a
// stale uncovered entry (an operation the uncovered list names that a caller just drove,
// the fixture widened or the handler changed), or a name on both lists at once fails the
// census with the full report: the driven count, the allowlist, the uncovered list and the
// stale entries.
func TestShapeCensusListPagesServed(t *testing.T) {
	ops := scWalkContractPages(t, scReadContract(t))
	if len(ops) != shapeCensusWantPages {
		t.Fatalf("the document carries %d page shape operations, want the pinned %d; the ungated half (TestShapeCensusContractKnowsEveryPageShape) carries the same pin and its message names the discipline", len(ops), shapeCensusWantPages)
	}
	scCrossCheckRouteTable(t, ops)

	fx := newXRoleFixture(t)
	t.Logf("the fixture fills (named with their sources): {entityId} and the order gaps {id} = testkit.OrderAlphaConfID (the planted CONFIRMED order, north org); {customerId} = testkit.TenantAlphaID (the planted contractor account); {scope} = pick_tasks (a store the binary wires (cmd/core/wire_operations.go), org tenancy (internal/sync/registry.go)); {vendorID} = xrUUIDParam (the rig's well-formed absent uuid); every other parameter = xrProbePath (the rig's absent uuid fill)")

	var (
		driven      []string
		allowHits   []string
		freshFails  []string
		uncovered   []string
		uncoveredKn []string
		staleAllow  []string
		staleUnc    []string
	)
	known := make(map[string]bool, len(ops))
	for _, op := range ops {
		known[op.OperationID] = true
	}
	for name := range shapeCensusAllowlist {
		if !known[name] {
			staleAllow = append(staleAllow, fmt.Sprintf("the allowlist names %q, which is no page shape operation of the document (a typo, or an operation that stopped being a page shape): drop or correct the entry", name))
		}
		if _, also := shapeCensusUncovered[name]; also {
			staleAllow = append(staleAllow, fmt.Sprintf("the allowlist AND the uncovered list both name %q: incoherent, an operation cannot be both broken-when-driven and undrivable; whichever entry today's run does not support must go (a caller drove the operation: the uncovered entry is stale; none did: the allowlist entry is premature)", name))
		}
	}
	for name := range shapeCensusUncovered {
		if !known[name] {
			uncovered = append(uncovered, fmt.Sprintf("the uncovered list names %q, which is no page shape operation of the document: drop the entry (its operation is driven now)", name))
		}
	}

	for _, op := range ops {
		ladder := scLadderFor(op)
		if ladder == nil {
			t.Fatalf("page shape operation %s carries the derived bearer shape %q, which names no ladder (scLadderFor must be total over the bearer shapes the derivation returns)", op.OperationID, op.Bearer)
		}
		out := scDrive(t, fx, op, scFixturePath(t, op), ladder)
		if out.status != 0 {
			driven = append(driven, op.OperationID)
		}
		for _, f := range scVerdict(op, out, shapeCensusAllowlist, shapeCensusUncovered) {
			switch f.kind {
			case scClean:
			case scAllowHit:
				allowHits = append(allowHits, op.OperationID)
			case scFreshMismatch:
				freshFails = append(freshFails, f.line)
			case scStaleAllow:
				staleAllow = append(staleAllow, f.line)
			case scStaleUncovered:
				staleUnc = append(staleUnc, f.line)
			case scUncoveredNamed:
				uncoveredKn = append(uncoveredKn, f.line)
			case scUncoveredUnnamed:
				uncovered = append(uncovered, f.line)
			default:
				t.Fatalf("the verdict produced an unknown kind %d for %s; the switch must be total over scVerdictKind", f.kind, op.OperationID)
			}
		}
	}

	sort.Strings(driven)
	sort.Strings(allowHits)
	sort.Strings(uncoveredKn)

	t.Logf("SHAPE CENSUS: drove %d of %d page shape operations", len(driven), len(ops))
	t.Logf("SHAPE CENSUS allowlist: %d entries, %d observed (bare array or broken page shape, named with their reasons)", len(shapeCensusAllowlist), len(allowHits))
	t.Logf("SHAPE CENSUS uncovered (named): %d operations the fixture cannot drive, each with its reason", len(uncoveredKn))
	for _, line := range uncoveredKn {
		t.Logf("SHAPE CENSUS uncovered: %s", line)
	}

	if len(freshFails) > 0 || len(uncovered) > 0 || len(staleAllow) > 0 || len(staleUnc) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "the response shape census FAILED (drove %d of %d page shape operations)\n", len(driven), len(ops))
		for _, line := range freshFails {
			fmt.Fprintf(&b, "MISMATCH (not allowlisted): %s\n", line)
		}
		for _, line := range uncovered {
			fmt.Fprintf(&b, "UNCOVERED (not named): %s\n", line)
		}
		for _, line := range staleAllow {
			fmt.Fprintf(&b, "STALE (allowlist): %s\n", line)
		}
		for _, line := range staleUnc {
			fmt.Fprintf(&b, "STALE (uncovered): %s\n", line)
		}
		fmt.Fprintf(&b, "driven: %s\n", strings.Join(driven, ", "))
		fmt.Fprintf(&b, "allowlist (named, with reasons):")
		for name, reason := range shapeCensusAllowlist {
			fmt.Fprintf(&b, "\n  %s: %s", name, reason)
		}
		t.Fatalf("%s", b.String())
	}
	t.Logf("SHAPE CENSUS: every driven operation answers the page shape (or sits allowlisted), every uncovered operation is named with its reason")
}
