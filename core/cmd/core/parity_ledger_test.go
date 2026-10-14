package main

// notYetRegistered is the explicit ledger of documented operations the
// core does not register (B-parity): every entry names the operation and
// the reason it is unimplemented. The parity test fails when a documented
// operation is missing from both the registered surface and this ledger,
// when a ledger entry names no documented operation, and when an entry
// goes stale because its operation registered (the ledger only ever
// shrinks: removing entries is the discipline the lanes follow as their
// modules land, one operationId at a time, with the ServeMux duplicate
// registration panic as the backstop against a route registering twice).
//
// THE PHASE C INTEGRATION SHRINK, stated per the ledger's own preamble
// rule: the fold's registerRoutes now registers the full served surface
// (the money spine, the operations cluster, the tops, the portal
// re-homing onto the documented tenant paths, and the customer-quote
// trio), so the ledger moved from 658 entries to the 31 below, the
// truly-unregistered set. Its composition: the D38 sign-in-methods
// leave-behinds (the four portal mint legs and the staff sign-in legs
// including the Google pair, all HH-4.2's to return), the operator
// launcher surface (the operator trio plus its requireStaff key listing,
// D4), the tenant brand pair held for the tenant_brand gap (ADR 0021
// annex, the table is HH-5's), the D10 attachment trio, the demo pair
// (carried, demo-box gated), the xlsx export trio, the Plane-bridge-gated
// feedback route, the staff identity admin remainder, and the load doors
// pair (HH-LOAD-1, carried behind the load doors deployment switch until
// a boot registers them). The sliding
// refresh (tenantRefreshToken) is REGISTERED, not ledgered: the identity
// proof shim serves its guard class until HH-4.2 registers the real mint
// surface, and the shim mints nothing.
var notYetRegistered = map[string]string{
	"createAttachment":              "D10 attachment surface; no implementation lane assigned",
	"createAttachmentLink":          "D10 attachment surface; no implementation lane assigned",
	"demoCreateSession":             "carried demo module; the credential bypass registers only on a demo box (DEMO_MODE with APP_ENV demo or dev)",
	"demoListFlows":                 "carried demo module; the credential bypass registers only on a demo box (DEMO_MODE with APP_ENV demo or dev)",
	"exportAuditXlsx":               "the export module carried the catalogue export; the xlsx trio has no implementing lane",
	"exportStatementXlsx":           "the export module carried the catalogue export; the xlsx trio has no implementing lane",
	"exportTransactionsXlsx":        "the export module carried the catalogue export; the xlsx trio has no implementing lane",
	"feedbackCreate":                "carried module; registration is gated on the Plane bridge configuration (three environment values)",
	"getAttachment":                 "D10 attachment surface; no implementation lane assigned",
	"getOperatorUsage":              "operator launcher surface (D4); no implementation lane assigned",
	"getTenantBrand":                "held for the tenant brand gap (ADR 0021 annex records the hold)",
	"identityAssignRole":            "no implementation lane assigned for the staff identity admin surface",
	"identityChangePassword":        "no implementation lane assigned for the staff identity admin surface",
	"identityConfirmPasswordReset":  "the D38 sign-in-methods leave-behind: the staff sign-in surface stays unregistered until HH-4.2 lands one",
	"identityCreateStaff":           "no implementation lane assigned for the staff identity admin surface",
	"identityGeneratePasswordReset": "no implementation lane assigned for the staff identity admin surface",
	"identityGoogleCallback":        "the D38 sign-in-methods leave-behind: the staff Google sign-in surface stays unregistered until HH-4.2 lands one",
	"identityGoogleStart":           "the D38 sign-in-methods leave-behind: the staff Google sign-in surface stays unregistered until HH-4.2 lands one",
	"identityListRoles":             "no implementation lane assigned for the staff identity admin surface",
	"identityLogin":                 "the D38 sign-in-methods leave-behind: the staff sign-in surface stays unregistered until HH-4.2 lands one",
	"identityPing":                  "no implementation lane assigned for the staff identity admin surface",
	"listPlatformKeys":              "operator launcher surface (D4); no implementation lane assigned",
	"loadGetBatch":                  "HH-LOAD-1 load doors: registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadMapBranches":               "HH-LOAD-1 load doors (LD-4): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadMapUsers":                  "HH-LOAD-1 load doors (LD-4): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadResolveKeys":               "HH-LOAD-1 load doors: registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertReceivables":         "HH-LOAD-1 load doors: registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertCategories":          "HH-LOAD-1 load doors (LD-4): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertContacts":            "HH-LOAD-1 load doors (LD-6): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertCustomers":           "HH-LOAD-1 load doors (LD-6): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertJobs":                "HH-LOAD-1 load doors (LD-6): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertSuppliers":           "HH-LOAD-1 load doors (LD-4): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertUnits":               "HH-LOAD-1 load doors (LD-3): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadPutUnitSets":               "HH-LOAD-1 load doors (LD-3): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertProducts":            "HH-LOAD-1 load doors (LD-5): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadPutProductImage":           "HH-LOAD-1 load doors (LD-5): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"loadUpsertSupplierSkus":        "HH-LOAD-1 load doors (LD-5): registers only where the deployment's configuration switches the load doors on (CORE_LOAD_DOORS, off by default), behind the machine gate the registrar carries exactly then",
	"putDealerCredential":           "operator launcher surface (D4); no implementation lane assigned",
	"putPlatformKey":                "operator launcher surface (D4); no implementation lane assigned",
	"putTenantBrand":                "held for the tenant brand gap (ADR 0021 annex records the hold)",
	"tenantAcceptInvite":            "the D38 sign-in-methods leave-behind: the portal mint surface stays unregistered until HH-4.2 lands one",
	"tenantConfirmPasswordReset":    "the D38 sign-in-methods leave-behind: the portal mint surface stays unregistered until HH-4.2 lands one",
	"tenantLogin":                   "the D38 sign-in-methods leave-behind: the portal mint surface stays unregistered until HH-4.2 lands one",
	"tenantRequestPasswordReset":    "the D38 sign-in-methods leave-behind: the portal mint surface stays unregistered until HH-4.2 lands one",
}
