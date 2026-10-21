// wire_tops.go is the fold's tops wiring (HH-3.2 A6): the wire function for
// the leaf modules (bistrack, branches, customer), the money-adjacent tops
// (billing, payment, pos, procurement, portal, export, finstmt, tax,
// compliance, feedback, tour), the read surfaces (dashboard, reporting,
// quoteassist, assist with its adapters) and the audit viewer, plus the
// seam closures whose owners land here: order's cancel guards and edit
// seams (customer, billing, pos), quote's edit seams, the gl finance
// readers (billing, pos, till), pos's rewire onto the real tax and stock
// engines, lien's holdback facts and calendar, till's card charger,
// dispatch's signature capture and AR gate, the yard stage-outbox (passed
// into wireOperations), warranty's customer credit, order's multi-ship
// invoicer, and the quote automation's notice queue. The pin's
// composition-root order is kept; the pin's adapter belt pieces these
// modules need live in tops_adapters.go, carried verbatim where the
// dependency landed.
package main

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	assistadapt "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/adapters"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bistrack"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/compliance"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/export"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/feedback"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/finstmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/procurement"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quoteassist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/reporting"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tour"
	"github.com/jackc/pgx/v5/pgxpool"
)

// smsBelt is the out-of-band notification belt the fold owns: the provider
// selection (the stub unless the environment says otherwise, a
// half-configured live provider logged loudly), the sms_outbox store with
// its quiet-hours policy, and the consent resolver over customer_sms_prefs.
// The belt's DISPATCHER and its consumers are worker-role wiring; the belt
// itself is shared (the yard's stage transaction enqueues through the
// outbox, the quote automation queues through the resolver).
type smsBelt struct {
	// quoteStatus re-reads a quote's status when a follow-up notice is delivered, so a notice queued
	// before the customer answered is dropped. Nil (a belt with no pool) checks nothing.
	quoteStatus messagingprovider.QuoteStatusReader
	notifier    notify.Notifier
	outbox      *messagingprovider.Outbox
	resolver    *messagingprovider.ConsentResolver
	prefs       *messagingprovider.PrefsStore
	policySet   bool
	// policy is the send-window and rate gate the sms drain consults; the worker's claim store is a
	// second Outbox over a different pool and must share this one pointer (its rate state is the gate).
	policy *messagingprovider.SendPolicy
}

// buildSmsBelt constructs the belt exactly as the pin's W2-B block did,
// including the loud-not-fatal posture on a misconfigured live provider
// (the server still boots on the stub rather than refusing to start over a
// notification channel). The contact source is a SETTER: the belt is built
// before the operations wiring (the yard repository's stage transaction
// enqueues through the outbox) and the source lands with the tops modules
// (customer, billing, order), bound onto the same resolver pointer the
// delivery-time reads go through, so the wiring decision is the pin's.
func buildSmsBelt(pool *pgxpool.Pool, log *slog.Logger) *smsBelt {
	smsSettings := messagingprovider.SettingsFromEnv()
	smsNotifier, smsErr := messagingprovider.Select(smsSettings, log)
	if smsErr != nil {
		log.Error("sms: requested provider is not usable - falling back to the stub; NO texts will be sent", "err", smsErr)
	}
	smsOutbox := messagingprovider.NewOutbox(pool)
	smsPrefs := messagingprovider.NewPrefsStore(pool).WithZone(newDealerZone(pool))
	smsResolver := messagingprovider.NewConsentResolver(smsPrefs, log)
	smsPolicy := messagingprovider.PolicyFromEnv()
	// The send window is read in the dealer's own zone unless SMS_WINDOW_ZONE named one. Read once, at
	// boot, beside the other SMS_WINDOW_* variables; an unreadable zone keeps the Ontario default and
	// says so.
	zoneCtx, cancelZone := context.WithTimeout(context.Background(), 5*time.Second)
	if dz, zerr := newDealerZone(pool).BusinessZone(zoneCtx); zerr != nil {
		log.Warn("sms: the dealer's business zone could not be read; the send window keeps its default zone", "err", zerr)
	} else {
		smsPolicy.WithDealerZone(dz)
	}
	cancelZone()
	if zerr := smsPolicy.ZoneError(); zerr != nil {
		log.Warn("sms: send-window timezone fell back to the business default", "err", zerr)
	}
	for _, fb := range smsPolicy.Fallbacks() {
		msg := "sms: policy variable fell back to its default; the value was refused"
		if fb.Var == "SMS_RATE_PER_MIN" {
			msg += " (zero is not a pause)"
		}
		log.Warn(msg, "var", fb.Var, "value", fb.Value, "default", fb.Default)
	}
	smsOutbox.WithSendPolicy(smsPolicy).WithLogger(log)
	return &smsBelt{notifier: smsNotifier, outbox: smsOutbox, resolver: smsResolver, prefs: smsPrefs, policySet: true, policy: smsPolicy}
}

// topsSpine carries what the worker role needs from the tops wiring: the
// notification port and outbox stores the loops drain, the sweeps' subjects
// (the AR collections sweep, the credit services, the automation), and the
// reconciler's messaging services. The card rail rides beside them: the
// wired service and the two seam adapters, the very values the till and pos
// received, so the demo build's G7 refusal reads the service the wiring
// built and the card rail test pins the handoff (the default build's serve
// path never reads them; the worker never reads any of it).
type topsSpine struct {
	// portalSvc is the portal BFF's service, held so a build can give its
	// login and refresh answers a dealer config (the demo's) after wiring.
	portalSvc     *portal.Service
	bistrackSvc   *bistrack.Service
	billingRepo   *billing.Repository
	notifier      notify.Notifier
	sms           *smsBelt
	arSweep       *messagingprovider.ARSweep
	creditSvc     *billing.CreditService
	statementSvc  *billing.StatementService
	invoiceWriter *billing.InvoiceWriter
	landingSvc    *billing.ARLandingService
	cardRail      *payment.Service
	cardCharger   cardChargerAdapter
	cardRefunder  posCardRefunderAdapter
	// posHandler is held so the composition root can register the payment grant read, which needs
	// the role gate built after the module wiring (pos/grant.go).
	posHandler *pos.Handler
	// posSvc is held so a wiring test can read the role configuration the till's service was built with.
	posSvc *pos.Service
	// assistSvc is held so the demo serve can read which limits the wiring gave it (demoRefuseAssistWithoutDemoLimits).
	assistSvc *assist.Service
}

// feedbackActorNames resolves a linked ERP user id to the display name and
// role label the feedback work item names the submitter by (the row 949
// rule: the board records under one bridge user, so the item's text is the
// only place the sender is named, and the raw id never reaches it). A nil
// pool (scaffold) or an unlinked user answers not-ok and the item names the
// role alone; neither ever refuses the submission. A user with several roles
// is named by the earliest granted, the role name breaking a tie, so the same
// user always reads the same.
func feedbackActorNames(pool *pgxpool.Pool) feedback.ActorNames {
	if pool == nil {
		return nil
	}
	return func(ctx context.Context, userID string) (name, role string, ok bool) {
		var displayName, roleName string
		err := pool.QueryRow(ctx, `
			SELECT u.display_name, COALESCE(r.name, '')
			  FROM users u
			  LEFT JOIN user_roles ur ON ur.user_id = u.id
			  LEFT JOIN roles r ON r.id = ur.role_id
			 WHERE u.id = $1::uuid AND u.deleted_at IS NULL
			 ORDER BY ur.created_at ASC NULLS LAST, r.name ASC
			 LIMIT 1`, userID).Scan(&displayName, &roleName)
		if err != nil {
			return "", "", false
		}
		return displayName, roleName, true
	}
}

// wireTops constructs and registers the tops modules over the money spine
// and the operations spine, in the pin's order, closing every seam whose
// owner lands here. It returns the tops spine the worker role consumes.
// roleCfg is the one role configuration the composition root holds (the
// default's, or the demo's); the till's POS service is built from it, never
// from a second load.
// Nothing here starts anything: the dispatchers, the jobs runner, the
// retention sweep and the reconciler are worker-role loops (D37), and the
// sms belt's dispatcher is built beside them in the worker.
func wireTops(r httpx.Registrar, pool *pgxpool.Pool, g *guards.Guards, log *slog.Logger, spine *moneySpine, ops *operationsSpine, cfg config.Config, roleCfg *roleconfig.Config, sharedSmsBelt *smsBelt) *topsSpine {
	link := linkedUserID(pool)
	auditSvc := spine.auditSvc
	orderRepo := spine.orderRepo
	orderSvc := spine.orderSvc
	inventorySvc := spine.inventorySvc
	productSvc := spine.productSvc
	pricingSvc := spine.pricingSvc
	quoteRepo := spine.quoteRepo
	quoteStaffSvc := spine.quoteStaffSvc

	// DIB-53 M9 BisTrack (phase 0 scaffold): the observability service, its
	// health token feeding healthz; no connector, no worker, no sync.
	bistrackSvc := bistrack.NewService(bistrack.NewRepository(pool)).WithConfigured(cfg.BisTrackConfigured())
	bistrack.NewHandler(bistrackSvc, log).WithUserLinker(link).RegisterRoutes(r, g)

	// CP-07 customer surface: the write path validates addresses for the
	// dealer's country through localization; the variadic write guard's manager mapping is
	// named at the registrar.
	customerRepo := customer.NewRepository(pool)
	customerSvc := customer.NewService(customerRepo).WithWriter(customerRepo).
		WithCountry(dealerCountryAdapter{r: settings.NewDealerConfigReader(pool)})
	customer.NewHandler(customerSvc, log).WithUserLinker(link).RegisterRoutes(r, g)

	// A-1 branches: the read the ledger claimed VERIFIED and the dashboard's
	// branch resolver reads (w0bBranchExistence, below).
	branchesSvc := branches.NewService(branches.NewRepository(pool))
	branches.NewHandler(branchesSvc, log).WithUserLinker(link).RegisterRoutes(r, g)

	// billingRepo is reused as gl.InvoiceReader + gl.ARSubledger
	// (billing/gl_seam.go) and lien's holdback facts.
	billingRepo := holdBillingRepo(pool)
	billingSvc := billing.NewService(billingRepo)

	// The SMS belt (the fold's shared half) was constructed BEFORE the
	// operations wiring so the yard repository's stage transaction can
	// enqueue through its outbox; its contact source lands here, over the
	// customer, billing and order services the pin's resolver read.
	belt := sharedSmsBelt
	belt.resolver.WithContactSource(w3dContactSource{customers: customerSvc, invoices: billingRepo, orders: orderRepo, log: log})

	// The portal's payments service over the SELECTED gateway (the pin's
	// first construction, DIB-416; the second service the W3-B block built
	// stays in wireOperations, the duplication the A1 ruling carries
	// verbatim).
	portalPaySvc := payments.NewService(payments.NewRepository(pool), ops.unionGateway).WithCurrency(newDealerCurrency(pool))

	// Procurement Planning board: pricing + customers + the catalog seam.
	procurementSvc := procurement.NewService(procurement.NewRepository(pool)).
		WithPricing(pricingSvc).
		WithAccounts(customerSvc).
		WithCatalog(productSvc)

	// The portal BFF. The HS256 secret the pin passed is the superseded
	// issuer world: an empty secret leaves the mint paths unable to sign and
	// no served mux registers them (the bearer-split carry note on
	// RegisterRoutes); the BFF's tenant legs resolve through the Guards.
	portalSvc := portal.NewService(portal.NewRepository(pool), "")
	portalHandler := portal.NewHandler(portalSvc, log).
		WithCustomers(customerSvc).
		WithBilling(billingSvc).
		WithOrders(orderSvc).
		WithQuotes(quote.NewService(quoteRepo).WithAudit(auditSvc)).
		WithPayments(portalPaySvc).
		WithProcurement(procurementSvc).
		WithProducts(productSvc).
		WithProjects(portal.NewProjectsService(portal.NewProjectRepository(pool)))
	portalHandler.RegisterRoutes(r, g)

	// RUN-PAY (W4): the card rail builds and registers in buildCardRail
	// (end of file): the vault, the store, the service over the processor
	// the selector chose, and the config handler whose simulated flag reads
	// that service. The same vault returns for pos's signature providers,
	// its other consumer.
	paySvc, payStore, payVault := buildCardRail(r, g, pool, cfg, link, log)

	// The card rail's two seam adapters, each built ONCE over the service
	// the selector chose and handed to its seam from this one value: the
	// till's capture rail here (over the service for the captures and the
	// compensations, over the store for the read-only retry pass), the pos
	// refunder further down. The same values ride the spine as the wiring
	// witnesses, so the card rail test pins the handoff itself: what
	// charges and what refunds is the rail the selector chose, not a
	// sibling construction (the HH-178 shape again would fail the test, not
	// only the reader).
	cardCharger := cardChargerAdapter{svc: paySvc, store: payStore}
	cardRefunder := posCardRefunderAdapter{svc: paySvc}

	// A-03 till: the CARD branch's charger seam closes here (payment has
	// landed): the creds-free adapter, credentials never crossing into the
	// till module.
	tillSvc := till.NewService(ops.tillRepo).
		WithCardCharger(cardCharger).
		WithAudit(auditSvc).
		WithAuditTrail(auditSvc)

	// A-19 POS: the counter, composing pricing + till + the hold-aware
	// credit gate + the (initially simulated) signature provider. The
	// till's role-configuration rules ride the same wiring (RT-B): the
	// dealer's configuration loaded at boot, the approver credential
	// through the same guards the routes register behind.
	posRepo := pos.NewRepository(pool).WithPayBalanceSources(newPayBalanceSources(pool)).WithZone(newDealerZone(pool))
	posSvc := wireTillRules(pos.NewService(posRepo).
		WithZone(newDealerZone(pool)).
		WithLineResolver(pricingSvc).
		WithTenderTaker(tillSvc).
		WithCreditGate(till.NewHoldAwareCreditGate(ops.tillRepo)).
		WithSignatureProvider(pos.NewSimulatedSignatureProvider(payVault)).
		WithAudit(auditSvc).
		WithAuditTrail(auditSvc).
		WithActorNames(staffNameAdapter{repo: orderRepo}).
		WithBranchNames(posBranchNameAdapter{svc: branchesSvc}).
		WithOrderInvoices(billingRepo).
		WithOpenQuantityReleaser(orderRepo).
		// A return releases the order's supplier deposit; the documents are billing's, read through
		// its writer on the return's transaction.
		WithDepositDocuments(newInvoiceWriter(pool)).
		WithClock(time.Now), roleCfg, g, link)
	posHandler := pos.NewHandler(posSvc, log).WithUserLinker(link)
	posHandler.RegisterRoutes(r, g)

	// The portal returns seam rides the POS service.
	portalHandler.WithReturns(posSvc)

	// DIB-191 B2/R2: the order cancel predicate, and the boot fail-fast the
	// pin restored the moment both guards existed (the A4 checklist).
	orderSvc.WithCancelGuards(billingRepo, posRepo)
	if !orderSvc.CancelGuardsWired() {
		panic("order: cancel guards not wired - a manual CANCELLED could not verify attached money state")
	}

	// Row 1193: the deposit exit beside the predicate. A cancelled order's
	// supplier deposit documents settle on the cancel's own transaction
	// (billing owns them), under the dealer's default disposition read from
	// the pos policy the counter administers. The boot fail-fast keeps a
	// deploy from serving with the exit dark, the cancel guards' own posture.
	orderSvc.WithDepositCanceller(newInvoiceWriter(pool).WithAudit(auditSvc))
	orderSvc.WithDepositCancelDefault(posSvc)
	if !orderSvc.DepositCancelWired() {
		panic("order: deposit cancel seams not wired - a cancelled special order's deposit could not settle")
	}

	// Item 9.3 (the row 1047 deposits work): the pay before release hold on
	// every path that releases goods (picking, yard picks, the will call
	// doors), read from the money through billing. The lane is additive in the
	// service (a scaffold composition without it behaves as before), so the
	// boot fail-fast here is what keeps a deploy from serving with it dark,
	// the cancel guards' own posture.
	orderSvc.WithPickHoldReader(pickHoldAdapter{repo: billingRepo})
	if !orderSvc.PickHoldWired() {
		panic("order: pick hold reader not wired - a pay before release order could reach picking unpaid")
	}
	ops.dispatchSvc.WithPickHoldGate(dispatchPickHoldAdapter{repo: billingRepo})
	if !ops.dispatchSvc.PickHoldGateWired() {
		panic("dispatch: pick hold gate not wired - a pay before release order's will call doors could release goods unpaid")
	}

	// Item 9 review P2-2: the Pay balance take raises an uninvoiced order's
	// invoice through the SAME billing writer the desk's order payment raises
	// with (the quoteMoneyDoor writer), on the take's own transaction, so the
	// whole balance is one number at every door and there is no second writer
	// to drift from the first.
	posSvc.WithPayBalanceInvoicer(posPayBalanceInvoicer{w: newInvoiceWriter(pool).
		WithTaxAssessor(w6cInvoiceTaxAdapter{svc: newTaxEngine(pool)}).
		WithOrderSource(quoteAgreedOrderSource{w6cOrderInvoiceSource: w6cOrderInvoiceSource{svc: orderSvc}, svc: orderSvc})})

	// A-21 audit viewer routes (the module carried since the foundations;
	// its registration lands with the fold).
	audit.NewHandler(auditSvc, log).RegisterRoutes(r, g)

	// DIB-441: the native .xlsx exports over the same two services the
	// catalogue screen reads.
	export.NewHandler(export.NewCatalogueService(productSvc, inventorySvc, log), log).
		WithUserLinker(link).RegisterRoutes(r, g)

	// DIB-175: the order edit seams close (customer has landed), and the
	// pin's boot fail-fast returns with them.
	orderSvc.WithCustomerType(customerTypeAdapter{svc: customerSvc})
	if !orderSvc.EditSeamsWired() {
		panic("order: edit seams not wired - a staff order edit could not reprice or verify the order")
	}

	// W5-A: the reservation seams on order and quote. The stock reserver
	// passes the order's own transaction through (an order that fails to
	// commit leaves no hold), the pick-state reader answers "has the yard
	// started", and the finance approver is the DIB-360/361 substrate.
	orderSvc.
		WithStockReserver(w5aStockReserver{svc: inventorySvc}).
		WithPickState(w5aPickState{pool: pool}).
		WithFinanceApprover(w5aFinanceApprover{svc: spine.glSvc})
	// HH-681: the change order apply re-assesses tax through the tax engine's standard rate (the seam the
	// quote builder reads), reads the order's creation day in the dealer's zone, and announces an applied
	// change on the events outbox. An order that carries tax refuses without the rate seam.
	orderSvc.
		WithStandardRate(newTaxEngine(pool)).
		WithZone(newDealerZone(pool)).
		WithChangeEvents(orderChangeEvents{})
	// The change queue labels each requester by role (never by name); a nil pool (the wiring shape
	// tests) leaves the labels blank.
	if pool != nil {
		roleLabels, err := erpidentity.NewStaffAuthorityResolver(pool)
		if err != nil {
			panic("order: change queue role labels refused to boot: " + err.Error())
		}
		orderSvc.WithRequesterRoles(roleLabels)
	}
	// Boot warning, the pin's: an unwired reservation seam turns the hold
	// lane off silently (order's reservations fail open by design), so the
	// silence is said out loud. Wired, it never fires.
	if !orderSvc.StockSeamWired() {
		log.Warn("order: stock reservation seam UNWIRED - confirms will hold no stock")
	}
	if !quoteStaffSvc.AvailabilitySeamWired() {
		log.Warn("quote: availability seam UNWIRED - at-quote chips will read UNKNOWN")
	}

	// W3-A: the counter search's image store is the SAME repository the
	// catalogue reads through (no second pool).
	productSvc.WithImages(product.NewRepository(pool))

	// DIB-56: the quote edit seams close the same way.
	quoteStaffSvc.WithCustomerType(quoteCustomerTypeAdapter{svc: customerSvc})
	if !quoteStaffSvc.QuoteEditSeamsWired() {
		panic("quote: edit seams not wired - a staff quote line could not be priced")
	}

	// The GL finance readers close: billing (invoice facts + the AR
	// subledger), pos (the FN tax split), till (the payment facts the
	// 1010/1020/1000 leg choice reads). The pin bound these at GL
	// construction; the fold binds them the moment their owners exist, the
	// same setter calls.
	spine.glSvc.
		WithInvoiceReader(billingRepo).
		WithARSubledger(billingRepo).
		WithTaxSplitReader(posRepo).
		WithPaymentReader(ops.tillRepo)

	// A-04 finstmt: read-only statements over the 0034 views. The DIB-510
	// receivables register mounts from here (billing's register handler,
	// registered under the Finance unit's gates exactly as the pin did).
	finstmtSvc := finstmt.NewService(finstmt.NewRepository(pool)).WithAudit(auditSvc)
	finstmtHandler := finstmt.NewHandler(finstmtSvc, log).WithZone(newDealerZone(pool)).WithUserLinker(link).WithPaymentTrail(spine.paymentTrail).WithCustomerReach(billing.NewCustomerModuleReach(customerSvc)).WithRegisterTaxLabel(newTaxWords(pool))
	if !finstmtHandler.CustomerReachWired() {
		panic("finstmt: customer reach not wired - the workspace, the statement preview, the payment policy, hold and paylink routes would serve any named account")
	}
	finstmtHandler.RegisterRoutes(r, g)

	// M5 tax engine, then the W6 scaffold-owner rewire: the POS Sale leaves
	// its stubs for the real engines (tax, stock, restock, the card
	// refunder).
	taxSvc := newTaxEngine(pool).
		WithGLBalances(glBalanceAdapter{svc: spine.glSvc}).
		WithAudit(auditSvc)
	tax.NewHandler(taxSvc, log).WithUserLinker(link).RegisterRoutes(r, g)
	posSvc.WithTaxAssessor(posTaxAdapter{svc: taxSvc}).
		WithTaxRuleSource(posTaxRuleAdapter{svc: taxSvc}).
		WithStock(posStockAdapter{svc: inventorySvc}).
		WithStockLocks(posStockAdapter{svc: inventorySvc}).
		WithRestocker(posRestockAdapter{svc: inventorySvc}).
		WithCardRefunder(cardRefunder)

	// M6 compliance: the 45-day release registry, and lien's two seams that
	// close here (the holdback facts over billing, the calendar registrar).
	complianceSvc := compliance.NewService(compliance.NewRepository(pool)).WithAudit(auditSvc)
	compliance.NewHandler(complianceSvc, log).WithUserLinker(link).RegisterRoutes(r, g)
	ops.lienSvc.WithHoldbackFacts(billingRepo).WithCalendar(complianceSvc)

	// The AR console (DIB-65/52/166/170): credit and statement services,
	// the month-end handler, the counter's aging reader, the AR landing
	// half.
	w3bCreditSvc := billing.NewCreditService(billingRepo).WithAudit(auditSvc)
	w3bStatementSvc := billing.NewStatementService(billingRepo).WithZone(newDealerZone(pool)).WithAudit(auditSvc)
	billing.NewMonthEndHandler(w3bCreditSvc, w3bStatementSvc, log).
		WithZone(newDealerZone(pool)).
		WithUserLinker(link).RegisterRoutes(r, g)
	posSvc.WithAgingReader(posAgingAdapter{svc: w3bCreditSvc}).
		WithHoldReader(ops.tillRepo).
		WithAccountChecker(posAccountAdapter{svc: customerSvc})

	arLandingRepo := billing.NewARLandingRepository(pool).WithZone(newDealerZone(pool))
	arLandingSvc := billing.NewARLandingService(arLandingRepo).
		WithZone(newDealerZone(pool)).
		WithAudit(auditSvc).
		WithAuditTrail(auditSvc).
		WithActorNames(staffNameAdapter{repo: orderRepo})
	// HH-185: the reminder transport is UnwiredSender, named here rather
	// than left to the constructor's nil default. No transport wired means
	// the send is recorded and marked FAILED in words, never a phantom
	// DELIVERED the paper-path sweep would count; a real transport lands
	// as an adapter passed in this same slot.
	arReminderSvc := billing.NewARReminderService(arLandingRepo, arLandingSvc, billing.UnwiredSender{}, cfg.StaffBaseURL).WithZone(newDealerZone(pool)).
		WithCustomerReach(billing.NewCustomerModuleReach(customerSvc))
	arLandingHandler := billing.NewARLandingHandler(arLandingSvc, arReminderSvc, log).
		WithZone(newDealerZone(pool)).
		WithCustomerReach(billing.NewCustomerModuleReach(customerSvc)).
		WithUserLinker(link)
	if !arLandingHandler.CustomerReachWired() {
		panic("billing: customer reach not wired - the AP contact routes would serve any named account")
	}
	arLandingHandler.RegisterRoutes(r, g)
	billing.NewARWorklistHandler(billing.NewARWorklistService(billingRepo).WithZone(newDealerZone(pool)), log).
		WithZone(newDealerZone(pool)).
		WithUserLinker(link).RegisterRoutes(r, g)

	// The dispatch gate seam (W3-C closes at the union): Swarm B's billing
	// predicate through dispatch's consumer-declared gate.
	ops.dispatchSvc.WithARGate(unionARGate{gate: w3bCreditSvc})
	// The POD signature capture seam (B7's, the A4 checklist): the DIB-386
	// typed-name attestation, the same provider the counter uses.
	ops.dispatchSvc.WithSignatures(dispatchSignatureAdapter{svc: posSvc})

	// DIB-386: the REAL signature provider replaces the W4 stub as the pos
	// default (one wired default, named here).
	posSvc.WithSignatureProvider(pos.NewTypedNameSignatureProvider(payVault))
	posHandler.RegisterTypedSignatureRoutes(r, g)

	// The pickups sync scope (HH-3.5 #169, manual policy) registers here,
	// beside the typed-name provider its confirms seal through: the store
	// takes the same dispatch.SignatureCapturer the public will-call
	// confirm path takes, and the tenant order seam the order module's
	// tenant-scoped reads serve. Unwired provider would refuse confirms
	// exactly as the public path's ErrNoSignatureProvider does; wired
	// here, both paths seal through one provider. Item 9 Opus P1-2: the
	// confirm also takes the pick hold gate (the same billing read the
	// public confirm consults), read inside the confirm's own
	// transaction, with the boot fail-fast beside it.
	pickupsSync := dispatch.NewPickupsSyncStore(
		pool, dispatchTenantOrders{repo: orderRepo}, dispatchSignatureAdapter{svc: posSvc}, dispatchPickHoldAdapter{repo: billingRepo}, linkedUserID(pool))
	if !pickupsSync.PickHoldGateWired() {
		panic("dispatch: pickups sync store pick hold gate not wired - a pay before release order's pickup could be confirmed offline unpaid")
	}
	if err := ops.syncEngine.RegisterScope("pickups", pickupsSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	// The quote automation's comms seams (W3-D): the notice queue over the
	// SMS outbox and the consent resolver, the contact reader over customer.
	spine.quoteAutomation.
		WithQueue(w3dNoticeQueue{outbox: belt.outbox, resolver: belt.resolver}).
		WithContacts(w3dContactReader{svc: customerSvc}).
		WithShares(quoteRepo)

	// The AR collections producer (DIB-76): the sweep over the PgARAging
	// projection; its job registration is worker-role (both schedules ship
	// enabled=false, a human turns them on per environment).
	arSweep := messagingprovider.NewARSweep(pool, belt.outbox, belt.resolver, log).
		WithSource(messagingprovider.NewPgARAging(pool))

	// #16 in-app feedback, wired only when the Plane bridge is fully
	// configured; the key stays server-side. The module comes from the
	// configured NAME (resolved against the board's module list, never an
	// id), and the work item names the submitter through the actor-name
	// resolver over the linked row, so the raw user id never reaches the
	// board (the row 949 rule).
	if cfg.PlaneAPIURL != "" && cfg.PlaneAPIKey != "" && cfg.PlaneProject != "" && cfg.PlaneWorkspace != "" {
		feedbackSvc := feedback.New(cfg.PlaneAPIURL, cfg.PlaneWorkspace, cfg.PlaneProject, cfg.PlaneAPIKey).
			WithModule(cfg.PlaneFeedbackModule)
		feedback.NewHandler(feedbackSvc, log).
			WithUserLinker(link).
			WithActorNames(feedbackActorNames(pool)).
			RegisterRoutes(r, g)
		log.Info("feedback: Plane bridge wired", "project", cfg.PlaneProject)
	} else {
		log.Info("feedback: Plane bridge not configured - /api/v1/feedback not wired")
	}

	// SWARM-W6-A: the cockpit. The tile providers, the viz series, the
	// widget substrate and the needs sources register over the pool; in
	// scaffold mode (nil pool) every tile renders its labelled unwired
	// state, the honest answer.
	dashboardSvc := dashboard.NewService(dashboard.NewRepository(pool), log).WithZone(newDealerZone(pool))
	bindTileAccess(dashboardSvc, spine.paymentTrail)
	if pool != nil {
		for _, reg := range []struct {
			tile     string
			provider dashboard.MetricProvider
		}{
			{dashboard.TileSalesToday, w6aSalesToday(pool)},
			{dashboard.TileOrdersOpen, w6aOrdersOpen(pool)},
			{dashboard.TileDeliveriesToday, w6aDeliveriesToday(pool)},
			{dashboard.TilePickQueue, w6aPickQueue(pool)},
			{dashboard.TileARAging, w6aARAging(pool)},
			{dashboard.TileLowStock, w6aLowStock(pool)},
			{dashboard.TileWarrantyOpen, w6aWarrantyOpen(pool)},
			{dashboard.TileMessagingUnread, w6aMessagingUnread(pool)},
			{dashboard.TileStalePicks, w6aStalePicks(pool)},
			{dashboard.TileWeightPending, w6aWeightPending(pool)},
			{dashboard.TilePaymentsToday, w6aPaymentsToday(ops.messagingSvc)},
		} {
			if err := dashboardSvc.Register(reg.tile, reg.provider); err != nil {
				log.Error("dashboard: provider NOT registered - that tile will render an em-dash", "tile", reg.tile, "err", err)
			}
		}
		dashboardSvc.WithMetricStore(dashboard.NewMetricRepository(pool))
		for _, reg := range vizSeriesRegistrations(pool) {
			if err := dashboardSvc.RegisterSeries(reg.Metric, reg.Provider); err != nil {
				log.Error("dashboard: series NOT registered - that metric will render not connected", "metric", reg.Metric, "err", err)
			}
		}
	} else {
		log.Warn("dashboard: no database - every tile will render its unwired em-dash")
	}
	dashboardHandler := dashboard.NewHandler(dashboardSvc, log).WithUserLinker(link)
	dashboardHandler.RegisterRoutes(r, g)
	dashboardHandler.RegisterVizRoutes(r, g)
	if pool != nil {
		widgetRepo := dashboard.NewWidgetRepository(pool)
		dashboardSvc.WithWidgetStore(widgetRepo).WithNeedsStore(widgetRepo).WithGalleryStore(widgetRepo).
			WithBranchResolver(w0bBranchExistence{svc: branchesSvc})
		for _, reg := range []struct {
			key    string
			source dashboard.NeedsSource
		}{
			{dashboard.NeedsApprovals, w6bApprovalsWaiting(spine.approvals)},
			{dashboard.NeedsAREscalation, w6bAREscalation(arLandingSvc)},
			{dashboard.NeedsUnassigned, w6bLoadBoard(pool, ops.dispatchSvc, false)},
			{dashboard.NeedsDueToCommit, w6bLoadBoard(pool, ops.dispatchSvc, true)},
			{dashboard.NeedsParkedTickets, w6bParkedTickets(pool, posSvc)},
			{dashboard.NeedsStalePicks, w6bStalePicks(pool)},
			{dashboard.NeedsOpenShortages, w6bOpenShortages(ops.yardTasksSvc)},
			// A receiving difference is a purchase order received in part with a short line, aged by
			// its latest receipt; it leaves the queue on full receipt or close (see ReceivingDifferences).
			{dashboard.NeedsReceivingDifferences, w6bReceivingDifferences(ops.purchasingSvc)},
			// Approvals the caller filed that are still pending: the logistics manager's own ask
			// (0395), counted without exposing another person's request or its content.
			{dashboard.NeedsFiledPending, w6bApprovalsFiledPending(spine.approvals)},
		} {
			if err := dashboardSvc.RegisterNeedsSource(reg.key, reg.source); err != nil {
				log.Error("dashboard: needs source NOT registered - that queue row will never appear", "source", reg.key, "err", err)
			}
		}
	} else {
		log.Warn("dashboard: no database - Home has no widgets and the Needs-you queue is empty")
	}
	dashboardHandler.RegisterWidgetRoutes(r, g)
	log.Info("dashboard: widget substrate wired",
		"needs_sources", dashboardSvc.WiredNeedsSources(),
		"measures", len(dashboard.MetricCatalog))

	// DIB-434 quick actions on the cockpit's substrate.
	quickActionSvc := dashboard.NewQuickActionService(
		dashboard.NewQuickActionRepository(pool), dashboardSvc, log)
	dashboard.NewQuickActionHandler(quickActionSvc, log).
		WithUserLinker(link).RegisterRoutes(r, g)

	// DIB-431 TanyaAI: the repository, the one environment read for the
	// credential (the value goes into the provider and nowhere else), the
	// settings row, the provider.
	assistRepo := assist.NewRepository(pool)
	assistKey := os.Getenv(assist.EnvAPIKey)
	assistSettings := assist.SettingsFrom(func() map[string]string {
		if pool == nil {
			// The DB-free registration proof: no pool, no settings row,
			// the package defaults.
			return nil
		}
		raw, err := assistRepo.Settings(context.Background())
		if err != nil {
			log.Warn("assist: cannot read settings at boot - using defaults", "err", err)
			return nil
		}
		return raw
	}())
	// The model layer's deployment configuration: the operator's environment, never a request. A
	// demo box refuses every one of these names (demoenv_inventory.go) until a later slice opens them.
	if cfg.DemoMode {
		// The demo's route is tanyai-premium by composition, never by a settings row.
		assistSettings = assist.DemoSettings(assistSettings)
	}
	assistModelCfg := assist.ModelConfig{
		AnthropicKey: redact.NewSecret(assistKey),
		GatewayURL:   os.Getenv(assist.EnvGatewayURL),
		GatewayKey:   redact.NewSecret(os.Getenv(assist.EnvGatewayKey)),
		DealerTag:    os.Getenv(assist.EnvDealerTag),
	}
	assistProvider := assist.NewProviderFromConfig(assistSettings, assistKey, "", log)
	// The alias table and the conversation log live in Postgres (0416); the DB-free registration
	// proof (no pool) gets the shipped default table and an in memory log.
	var (
		assistAliases assist.AliasStore        = assist.StaticAliases(assist.DefaultAliases())
		assistConvs   assist.ConversationStore = assist.NewMemoryConversations()
	)
	if pool != nil {
		assistAliases, assistConvs = assistRepo, assistRepo
	}
	assistRouter := assist.NewRouter(assistAliases, assistModelCfg, assist.NewStubProvider(), log)
	if cfg.DemoMode {
		assistRouter = assistRouter.Only(assist.AliasPremium) // the demo reaches one alias
	}

	// DIB-66/67: the quote assist surfaces on the builder.
	quoteAssistSvc := quoteassist.NewService(
		assistadapt.NewQuoteCatalogueSearch(productSvc),
		assistadapt.NewQuoteScopeLines(quoteStaffSvc, productSvc, log),
		quoteassist.NewRepository(pool),
		log,
	)
	quoteRails := assistadapt.NewQuoteExtractRails(assistProvider, assistRepo, assistRepo, log)
	quoteAssistSvc.WithAI(quoteRails)
	quoteassist.NewHandler(quoteAssistSvc).WithUserLinker(link).RegisterRoutes(r, g)

	// SWARM-W6-B: reporting and commissions.
	reportingSvc := reporting.NewService(reporting.NewRepository(pool)).
		WithLogger(log).
		WithAudit(auditSvc).
		WithRepAssignments(w6bRepAssignmentReader{svc: ops.messagingSvc})
	reporting.NewHandler(reportingSvc).WithUserLinker(link).RegisterRoutes(r, g)

	// DIB-478: the assist deep readers over every landed service.
	assistDeps := assistadapt.DeepReaders(assistadapt.Services{
		Messaging:  ops.messagingSvc,
		Reporting:  reportingSvc,
		Credit:     w3bCreditSvc,
		Billing:    billingSvc,
		Orders:     orderSvc,
		Quotes:     quoteStaffSvc,
		Inventory:  inventorySvc,
		Purchasing: ops.purchasingSvc,
		Pricing:    pricingSvc,
		Customers:  customerSvc,
		Catalogue:  productSvc,
		Branches:   branchesSvc,
	})
	assistDeps.Payments = assistadapt.NewPaymentReader(portalPaySvc)
	assistDeps.Orders = assistadapt.NewOrderReader(orderSvc)
	assistDeps.Stock = assistadapt.NewStockReader(inventorySvc)
	assistDeps.Customers = assistadapt.NewCustomerReader(customerSvc, w3bCreditSvc)
	assistDeps.Now = time.Now
	// TanyAI slice 5: the structured data tool, over the assist logins (never the app role). With no
	// configuration, or a configuration the startup gate refuses (wrong login, no claims key, statement
	// pooling), data.query stays unwired and answers 503 while everything else works. The error names
	// the check and carries no URL or key.
	if pool != nil {
		if rcfg := readtx.ConfigFromEnv(os.Getenv); !rcfg.ReaderURL.IsEmpty() {
			octx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			readers, rerr := readtx.Open(octx, rcfg)
			cancel()
			if rerr != nil {
				log.Warn("assist: the data path is off", "reason", rerr.Error())
			} else {
				assistDeps.Query = &assist.ReadQuerier{
					Reader: readers.Staff, Zone: newDealerZone(pool), Now: time.Now,
					Log: assistRepo, Branches: assistDeps.Branches, Logger: log,
				}
			}
		}
	}
	assistSvc := assist.NewService(
		assist.NewRegistry(assistDeps, assistRepo),
		assistProvider,
		assistRepo, // LedgerStore
		assistRepo, // LimitStore
		assistRepo, // SettingStore
		log,
	).
		WithStaff(assistRepo).
		WithKeyPresent(strings.TrimSpace(assistKey) != "").
		// Slice 2: every provider call is reserved and settled in the spend table, and the dealer
		// wide cap (FutureBuild's deployment variable, never an assist setting) bounds them all.
		WithMetering(assistRepo, assistRepo).
		WithZone(newDealerZone(pool)). // the dealer's month, not the server clock's
		WithDealerCap(assist.ParseDealerCap(os.Getenv(assist.EnvDealerCap))).
		// Per asker rate limit on every build; the demo's per visitor, per address and box wide
		// buckets only on a demo copy.
		WithRateLimits(assist.NewRateLimits(assistDemoLimits(cfg.DemoMode), time.Now)).
		WithRouter(assistRouter).
		WithQueryLog(assistQueryLog(pool, assistRepo)).
		// Server kept conversations keyed by user and session (the jti), on the append only log.
		WithConversations(assistConvs).
		WithResolver(assist.NewResolver(assistDeps.Catalogue, assistDeps.Branches))
	// A demo copy is pinned to tanyai-premium and answers on the stub, and says so, when the model
	// cannot be used.
	if cfg.DemoMode {
		assistSvc = assistSvc.WithPinnedRoute(assist.AliasPremium).WithStubFallback()
	}
	// The quote rails call the provider only through the assist service's meter (reservation,
	// dealer cap, settlement); bound here because the service is built after them.
	if quoteRails != nil {
		quoteRails.WithCompleter(assistSvc)
	}
	// The claim's cost fields follow the dealer's cost and margin role list, the predicate every module's
	// screen asks (inventory, quote, pricing, tasks, dispatch); a global caller on the list gets every branch.
	assist.NewHandler(assistSvc, log).WithUserLinker(link).WithPaymentTrail(spine.paymentTrail).
		WithCostGate(spine.roleCfg, assistDeps.Branches).
		WithPurchaseCostPolicy(assistadapt.NewPurchaseCostPolicy(ops.purchasingSvc)).
		WithChannelMembership(assistadapt.NewChannelMembership(ops.messagingSvc)).RegisterRoutes(r, g)

	// SWARM-W6-C: the money tail. ONE writer value shared by all three
	// consumers (order's multi-ship invoicer, warranty's customer credit,
	// the credit-memo repost sweep the worker registers).
	w6cInvoiceWriter := newInvoiceWriter(pool).
		WithTaxAssessor(w6cInvoiceTaxAdapter{svc: taxSvc}).
		WithOrderSource(w6cOrderInvoiceSource{svc: orderSvc}).
		WithCreditMemoPoster(w6cCreditMemoPoster{svc: spine.glSvc, rules: gl.NewRepository(pool), words: newTaxWords(pool)})
	orderSvc.WithMultiShipInvoicer(w6cMultiShipInvoicer{w: w6cInvoiceWriter})
	ops.warrantySvc.WithCustomerCredit(w6cWarrantyCreditIssuer{w: w6cInvoiceWriter})

	// The invoices and payments sync scopes (HH-3.5 follow-up, both
	// server_wins) register here, beside the collaborators their doors
	// run through: the invoices Store settles documents through the
	// money tail's ONE writer value (the same order source, the same
	// tax engine the desk button drives, so the desk's double-click
	// and a device's replayed outbox converge on one document), and
	// the payments Store pays through the portal pay service over the
	// SELECTED gateway (the DIB-415 intent guard and the stamped
	// settle both holding on the replayed path).
	if err := ops.syncEngine.RegisterScope("invoices", billing.NewInvoicesSyncStore(
		pool, w6cInvoiceWriter, identity.NewApprovalRepository(pool))); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := ops.syncEngine.RegisterScope("payments", payments.NewPaymentsSyncStore(pool, portalPaySvc)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	// W5-C statement notices, flag-off unless the environment says so (the
	// queue-only posture: delivery is still gated by the SMS provider).
	if os.Getenv("HOS_STATEMENT_NOTICES") == "1" {
		w3bStatementSvc.WithOutbox(billing.NewStatementNoticeOutbox(
			w5cStatementQueue{outbox: belt.outbox}, billing.StatementNoticeTopic))
		log.Info("billing: statement notices ENABLED (queue only; delivery still gated by the sms notifier)",
			"topic", billing.StatementNoticeTopic, "notifier", belt.notifier.Name())
	}

	// 0192/0193 the guided demo walkthrough: registered unconditionally,
	// the read degrades downward by construction.
	tour.NewHandler(tour.NewService(tour.NewRepository(pool)).WithLogger(log)).
		WithUserLinker(link).RegisterRoutes(r, g)

	// The dev log stub outside dev is a loud warning, the pin's posture.
	notifier := notify.NewLogNotifier(log).WithBodies(cfg.Env == "dev")
	if cfg.Env != "dev" && !notifier.Live() {
		log.Warn("notify: running on the dev log stub outside dev - invite/reset email is NOT delivered; wire a live provider before onboarding go-live", "env", cfg.Env)
	}

	return &topsSpine{
		portalSvc:     portalSvc,
		bistrackSvc:   bistrackSvc,
		billingRepo:   billingRepo,
		notifier:      notifier,
		sms:           belt,
		arSweep:       arSweep,
		creditSvc:     w3bCreditSvc,
		statementSvc:  w3bStatementSvc,
		invoiceWriter: w6cInvoiceWriter,
		landingSvc:    arLandingSvc,
		cardRail:      paySvc,
		cardCharger:   cardCharger,
		cardRefunder:  cardRefunder,
		posHandler:    posHandler,
		posSvc:        posSvc,
		assistSvc:     assistSvc,
	}
}

// buildCardRail is the RUN-PAY (W4) card rail, the fold's one construction
// of it and the one place that knows which rail is wired: the vault, the
// store, the service over the SELECTED processor, and the config handler
// registered beside it. The processor the selector chooses is the one the
// service wires (HH-178), so the till's card charges (the charger seam)
// and the pos card refunds (the refunder seam) both ride the rail the
// setting names. The status route's simulated flag reads the wired
// service's own Simulated, never a handler default (HH-187): the screen
// badge and the demo build's refusal to serve on a live rail (G7) both
// read that one fact. The vault returns beside the service: pos's signature
// providers seal through the same vault, its other consumer. The vault and
// the selector failures are boot refusals exactly as at the pin (a payment
// credential sealed under a lost key, or a card rail that cannot say where
// a charge would go, is worse than no payment rail); the selector's own
// guard keeps its meaning unchanged (DIB-416): production still refuses to
// boot on a simulated rail.
func buildCardRail(r httpx.Registrar, g *guards.Guards, pool *pgxpool.Pool, cfg config.Config, link identity.UserIDLinker, log *slog.Logger) (*payment.Service, *payment.Store, *payment.Vault) {
	payVault, err := payment.NewVault(cfg.PaymentVaultKey)
	if err != nil {
		panic("payment vault init failed: " + err.Error())
	}
	payStore := payment.NewStore(pool, payVault).WithDealerCurrency(newDealerCurrency(pool))
	// DIB-416: the POS card rail selection is FATAL, the same fail-closed
	// posture the payments gateway selector carries.
	cardProc, cardErr := payment.SelectCardProcessor(payment.LoadCardRailConfig())
	if cardErr != nil {
		panic("POS card rail selection refused to boot (DIB-416): " + cardErr.Error())
	}
	paySvc := payment.NewService(payStore, cardProc)
	payment.NewHandler(payStore, log).WithUserLinker(link).WithSimulated(paySvc.Simulated()).RegisterRoutes(r, g)
	// The store returns beside the service: the till's capture adapter reads
	// the attempt rows through it on the seam's read-only arm, the same
	// store the service's own captures write.
	return paySvc, payStore, payVault
}

// assistDemoLimits returns the demo copy's extra assistant rate buckets, and nil everywhere else.
func assistDemoLimits(demoMode bool) *assist.DemoLimits {
	if !demoMode {
		return nil
	}
	return assist.NewDemoLimits(time.Now)
}

// bindTileAccess binds the dashboard's tile access rules (HH-777). The money tile's rule is the payment
// trail reader the register and the order activity already use (the role gate's own admission to the
// trail routes), asked with the caller's platform identity and org the handler stamps on the scope. The
// reader is the money spine's late one: unbound it answers no, so the tile stays closed until the role
// gate exists.
func bindTileAccess(svc *dashboard.Service, trail billing.PaymentTrailReader) {
	svc.WithTileAccess(dashboard.AccessPaymentTrail, func(ctx context.Context, sc dashboard.Scope) (bool, error) {
		if trail == nil || sc.PlatformUserID == "" || sc.OrgID == "" {
			return false, nil
		}
		return trail.HoldsPaymentTrail(ctx, sc.PlatformUserID, sc.OrgID)
	})
}

// assistQueryLog is the writer of the exchange summary row: the repository when there is a database,
// nothing in the DB free registration proof.
func assistQueryLog(pool *pgxpool.Pool, repo *assist.Repository) assist.QueryLog {
	if pool == nil {
		return nil
	}
	return repo
}
