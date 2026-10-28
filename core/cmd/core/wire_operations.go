// wire_operations.go is the operations-cluster wiring of HH-3.2 batch B5:
// the wire functions for bankrec, dispatch, invcount, landedcost, lien,
// messaging, payments, printing, purchasing, tasks, till and warranty, in
// the pin's own construction order, carrying the pin's composition-root
// adapters for these modules verbatim where the adapter's dependencies
// have landed, and leaving the not-yet-landed seams unwired with their
// designed fail-closed behavior (recorded per seam in the batch report).
// Nothing here starts anything: the dispatchers, the jobs runner, the
// messaging retention sweep and the approvals reconciler are worker-role
// concerns that B7 wires (D37 pins their behaviour; only their startup
// location changes, and none of it is here).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/attachments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/invcount"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/landedcost"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/lien"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messagingprovider"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/planflow"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/printing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/procurement"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/warranty"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dispatchDriverSessionKnob is the P-4 pin closing: the driver session
// ceiling's knob source (identity.DriverSessionHours) over the
// dispatch-owned dispatch_policy table. The read is the pin's own
// dealer-default row (the empty branch id resolves to branch_id IS NULL,
// exactly as the pin's DriverSessionHours argued it must), validated by
// the module's own ValidatePolicy so the 1-to-168 CHECK and the identity
// ceiling agree.
//
// ONE DELIBERATE DIFFERENCE FROM THE PIN, recorded in the batch report:
// the ERP's login-time read failed to the twelve-hour default on every
// error arm; the target's enforcement-time read (B2-07) refuses on an
// unreadable or out-of-contract knob rather than guessing, so the guard
// answers the 503 family and no outage silently shortens or lengthens a
// driver's session.
type dispatchDriverSessionKnob struct{ repo *dispatch.Repository }

func (k dispatchDriverSessionKnob) DriverSessionHours(ctx context.Context, orgID string) (int, error) {
	p, err := k.repo.ReadPolicy(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("driver session knob: %w", err)
	}
	p, err = dispatch.ValidatePolicy(p)
	if err != nil {
		return 0, fmt.Errorf("driver session knob: %w", err)
	}
	return p.DriverSessionHours, nil
}

// tasksDefaultChannelSlug is a variable, not a constant, so the demo build can point it at its own room. It is the fallback chat channel for a task EVENT
// when the task carries no branch (the pin's constant, carried).
var tasksDefaultChannelSlug = "yard-trenton"

// yardCounterFallbackSlug is where a counter-audience EVENT lands when the
// branch has no counter channel (or no branch at all), the pin's constant.
const yardCounterFallbackSlug = "office"

// wireOperations constructs and registers the operations cluster over the
// money spine's shared instances, in the pin's order: printing before
// dispatch (the run manifest enqueues through it), till before bankrec
// (the recon reads run through the till repository), messaging before
// tasks (the chat notifier posts through the service), landedcost before
// purchasing (the AP tie-in capitalizes through it).
// operationsSpine carries the operations cluster's shared handles to the
// tops wiring and the worker role: the services whose seams the tops
// modules close (dispatch's signature capture and AR gate, the yard stage
// outbox, warranty's credit issuer) and the loops' subjects (the messaging
// services the retention sweep and reconciler read).
type operationsSpine struct {
	dispatchSvc   *dispatch.Service
	yardTasksSvc  *tasks.Service
	warrantySvc   *warranty.Service
	messagingSvc  *messaging.Service
	messagingRepo *messaging.Repository
	lienSvc       *lien.Service
	printQueue    *printing.QueueService
	tillRepo      *till.Repository
	// tillSvc is the till service built below, stored so the month walk (FW1-00, this
	// package) can pin its clock through the service's own WithClock setter
	// (FROM-PRINCIPAL 209). The field only keeps the instance already constructed:
	// production builds and registers it exactly as before and nothing in production
	// reads the field.
	tillSvc *till.Service
	// paymentsReverseSvc is the payment reversal service built below, stored for the
	// month walk's clock pin the same way; the field stores the instance already
	// built and nothing in production reads it.
	paymentsReverseSvc *payments.ReverseService
	unionGateway       payments.PaymentGateway
	purchasingSvc      *purchasing.Service
	landedcostSvc      *landedcost.Service
	// syncEngine is the sync core (HH-3.5): returned so the scopes
	// whose owning service the operations wiring does not build (the
	// customer-quote surface, constructed in main after the spines; the
	// invoice writer and the portal pay service, constructed in the
	// tops batch) can register beside their own module's construction.
	syncEngine *sync.Engine
}

func wireOperations(r httpx.Registrar, pool *pgxpool.Pool, g *guards.Guards, log *slog.Logger, spine *moneySpine, streamTicketKey string, smsOutbox *messagingprovider.Outbox) *operationsSpine {
	ops := &operationsSpine{}
	link := linkedUserID(pool)
	auditSvc := spine.auditSvc
	orderRepo := spine.orderRepo
	orderSvc := spine.orderSvc
	inventorySvc := spine.inventorySvc
	inventoryRepo := inventory.NewRepository(pool)

	// A-03 (W4): till / cash-drawer ops, the SOLE writer of the payments
	// row. The CARD branch's charger seam (the payment module's RUN-PAY
	// rail) stays unwired: that module is unlanded, and unwired the card
	// tender refuses rather than charging through a rail nobody wired
	// (fail closed, the module's own posture). The audit read seam feeds
	// the NSF-reversed-allocation history.
	tillRepo := till.NewRepository(pool)
	tillSvc := till.NewService(tillRepo).
		WithAudit(auditSvc).
		WithAuditTrail(auditSvc)
	tillHandler := till.NewHandler(tillSvc, log).WithUserLinker(link).WithCustomerReach(newCustomerWall(pool))
	if !tillHandler.CustomerReachWired() {
		panic("till: customer reach not wired - payment attempts would record against any named account")
	}
	tillHandler.RegisterRoutes(r, g)

	// DIB-387: the quote conversion's credit/hold gate, closable now till
	// has landed. The pin wired the SAME gate instance the POS ring uses;
	// POS is unlanded, so this is the gate's first wired consumer in the
	// target, on the quote repository the money batch serves.
	spine.quoteRepo.WithCreditGate(till.NewHoldAwareCreditGate(tillRepo))

	// A-02 (W5): bank reconciliation over the till repository's payment
	// and lodgement reads, posting clearing journals through the GL.
	bankrecSvc := bankrec.NewService(bankrec.NewRepository(pool)).
		WithZone(newDealerZone(pool)).
		WithPayments(tillRepo).
		WithLodgements(tillRepo).
		WithJournalPoster(glPosterAdapter{svc: spine.glSvc}).
		WithGLBalances(glBalanceAdapter{svc: spine.glSvc}).
		WithJournals(spine.glSvc).
		WithJournalKeys(spine.glSvc).
		WithAudit(auditSvc)
	bankrec.NewHandler(bankrecSvc, log).WithUserLinker(link).RegisterRoutes(r, g)

	// M6 (W6): the Lien Cockpit. The holdback-facts reader (billing) and
	// the calendar registrar (compliance) are unlanded modules: unwired,
	// the aging worklist degrades and booking does not register a
	// compliance deadline, each the module's designed unwired behavior.
	// The co-signer resolver is the re-pointed rail repository read (a
	// projection by ERP user id; decide-time authority never runs through
	// it).
	lienSvc := lien.NewService(lien.NewRepository(pool)).
		WithStandardRate(newTaxEngine(pool)). // the HST on holdback gross up reads the dealer's standard rate
		WithOrderHoldback(orderRepo).
		WithGLPoster(lienGLPoster{svc: spine.glSvc}).
		WithCoSigner(identity.NewApprovalRepository(pool)).
		WithAudit(auditSvc)
	lienHandler := lien.NewHandler(lienSvc, log).WithUserLinker(link).WithCustomerReach(newCustomerWall(pool))
	if !lienHandler.CustomerReachWired() {
		panic("lien: customer reach not wired - a lien job would open for any named account")
	}
	lienHandler.RegisterRoutes(r, g)

	// A-15 (W6): inventory counts over inventory's count seam and the GL
	// (the reason leg and the 1200 leg both resolve through the GL).
	invcountSvc := invcount.NewService(invcount.NewRepository(pool)).
		WithStockCounter(invCountAdapter{svc: inventorySvc}).
		WithGLPoster(invcountGLPoster{svc: spine.glSvc}).
		WithRuleResolver(glRuleAdapter{svc: spine.glSvc}).
		WithGLBalanceReader(glBalanceAdapter{svc: spine.glSvc}).
		WithAudit(auditSvc)
	// X3, the yard fence's counts half: the negative-stock worklist's
	// cost leg rides only for a caller on the configuration's cost and
	// margin list (inventory/cost_gate.go carries the argument).
	invcountHandler := invcount.NewHandler(invcountSvc, log).WithUserLinker(link).WithCostRoles(spine.roleCfg)
	if invcountHandler == nil || !invcountHandler.CostRolesWired() {
		panic("invcount: cost roles not wired - the negative-stock worklist would answer the cost leg to every caller")
	}
	invcountHandler.RegisterRoutes(r, g)

	// A-16 (W6): landed cost, capitalizing through inventory.
	landedcostSvc := landedcost.NewService(landedcost.NewRepository(pool)).
		WithCapitalizer(landedAdapter{svc: inventorySvc}).
		WithGLPoster(landedGLPoster{svc: spine.glSvc}).
		WithAudit(auditSvc)
	landedcost.NewHandler(landedcostSvc, log).WithUserLinker(link).RegisterRoutes(r, g)

	// SWARM-W4-C: purchasing, the whole AP side, over inventory's
	// receiving seam, the vendors projection, landed cost and the GL.
	// The PO sender stays the module's unwired default by the pin's own
	// recorded decision (no transport is wired on this server; the send
	// is recorded FAILED in words rather than DELIVERED into a log).
	purchasingSvc := finworldPurchasingComposition(pool, log, auditSvc).
		WithStockReceiver(w4cStockReceiver{svc: inventorySvc}).
		WithLandedCoster(w4cLandedCoster{svc: landedcostSvc}).
		WithLandedCostPayables(w4cLandedCoster{svc: landedcostSvc}).
		WithSupplierPayments(w4cSupplierPaymentReader{pool: pool}).
		WithGLPoster(w4cGLPoster{svc: spine.glSvc}).
		WithRuleResolver(glRuleAdapter{svc: spine.glSvc}).
		WithWarrantyCreditReader(w4cWarrantyCreditReader{pool: pool})
	purchasing.NewHandler(purchasingSvc, log).WithUserLinker(link).RegisterRoutes(r, g)

	// UNION-W4 and UNION-470: the inventory seams purchasing closes. The
	// create-draft-PO seam was the money batch's StubPOCreator default;
	// the real creator is bound now that purchasing exists, and the
	// on-order read stops reporting the constant zero.
	inventorySvc.WithPOCreator(unionPOCreator{svc: purchasingSvc})
	inventorySvc.WithOnOrderReader(inv470OnOrder{svc: purchasingSvc})
	inventorySvc.WithDraftPOReader(invDraftPOs{svc: purchasingSvc})
	// Change 5 slice S3: the shortfall row names the sales orders it holds up, read through the
	// order module's seam (the 0008 tables' sole reader) rather than any second look at them.
	inventorySvc.WithShortOrderReader(invShortOrders{svc: orderSvc})
	// Change 5 slice S4: the browse row names the branches it is out of, read through the branches
	// module's seam (the branch rows' owner) rather than any second look at them.
	inventorySvc.WithBranchNameReader(invBranchNames{svc: branches.NewService(branches.NewRepository(pool))})

	// MSG-01 / DIB-152: the messaging hub. The stream-ticket HMAC takes
	// its DEDICATED environment key (the A1 ruling; the HS256 secret it
	// reused at the pin no longer exists). An empty key leaves the
	// tickets seam nil, which is the designed degradation: the
	// stream-ticket route answers 501 and clients use the Bearer stream.
	messagingRepo := messaging.NewRepository(pool)
	messagingHub := messaging.NewSSEHub(messagingRepo, log)
	messagingSvc := messaging.NewService(messagingRepo).WithZone(newDealerZone(pool)).WithRealtime(messagingHub)
	// HH-424: the targeted payment notices and the money rooms follow the payment trail reader and the
	// branch reach. The reader is the money spine's late one, bound to the role gate once it exists
	// (bindPaymentTrail), and unbound it answers no, so the rooms and notices stay closed until then.
	messagingSvc.WithPaymentAccess(spine.paymentTrail)
	if pool != nil {
		reach, reachErr := erpidentity.NewStaffAuthorityResolver(pool)
		if reachErr != nil {
			panic("messaging: payment fence identity resolver refused to boot: " + reachErr.Error())
		}
		messagingSvc.WithPaymentRecipients(paymentRecipientReader{reach: reach})
	}
	messagingHub.WithRoomFence(messagingSvc.RoomAdmits)

	// D10 / S8 (0403): Attachments surface and room attachments mirror seams.
	attachmentsRepo := attachments.NewRepository(pool)
	messagingSvc.WithAttachmentChecker(attachmentsSeamAdapter{repo: attachmentsRepo})
	attachments.NewHandler(attachmentsRepo, log).
		WithRoomChecker(messagingRoomChecker{repo: messagingRepo}).
		WithRoomMirror(messagingAttachmentMirror{svc: messagingSvc}).
		WithUserLinker(link).
		RegisterRoutes(r, g)

	messaging.NewHandler(messagingSvc, log).WithHub(messagingHub).
		WithStreamTickets(streamTicketKey).
		WithUserLinker(link).RegisterRoutes(r, g)
	// SWARM-W5-D: the conversational tier registers through its own entry
	// point on a second Handler value, exactly as at the pin (this
	// surface needs neither the hub nor the tickets).
	messagingConversationHandler := messaging.NewHandler(messagingSvc, log).WithUserLinker(link).WithCustomerReach(newCustomerWall(pool))
	if !messagingConversationHandler.CustomerReachWired() {
		panic("messaging: customer reach not wired - the rep and inbound customer doors would serve any named account")
	}
	messagingConversationHandler.RegisterConversationRoutes(r, g)

	// The approvals rail's messaging seams (0075): the notifier fans
	// cards and decision echoes into the hub, best-effort, never failing
	// a filing or a decision. The reconcile worker that heals missed
	// fan-out is a worker-role loop (B7).
	spine.approvals.WithApprovalNotifier(approvalNotifierAdapter{svc: messagingSvc, log: log})

	// UNION-INV461: the inventory procurement nudge over messaging's
	// internal post path.
	inventorySvc.WithProcurementNudger(inv461Nudger{svc: messagingSvc})

	// WMS-Lite (0073): the PICK walking skeleton. The shipped tasks
	// surface and the yard surface each get their own Handler value over
	// their own service, exactly as at the pin. The yard service's
	// stage-outbox seam (messagingprovider's sms_outbox) is unlanded and
	// stays unwired: unwired, staging a pick stages it and queues no SMS
	// notice, the module's designed unwired behavior.
	tasksSvc := tasks.NewService(tasks.NewRepository(pool)).
		WithOrderReader(orderReaderAdapter{repo: orderRepo}).
		WithChatNotifier(taskChatNotifier{svc: messagingSvc}).
		WithLogger(log)
	branchOrg := branchOrgAdapter{repo: branches.NewRepository(pool)}
	tasksHandler := tasks.NewHandler(tasksSvc, log).WithUserLinker(link).WithBranchOrgReader(branchOrg)
	tasksHandler.RegisterRoutes(r, g)

	// The sync core (HH-3.5 #165): the engine with the pilot scope
	// pick_tasks wired over the tasks module's own adapter (the module
	// owns the tables; the adapter validates creates through the same
	// order-read seam the REST create uses and attributes actors through
	// the same linker). The route half of the G4 rule rides the guards'
	// RequireSync inside RegisterRoutes; the per-operation audit rows
	// ride the carried audit service through the seam adapter below.
	// Later Phase D items register their scopes beside this block, one
	// RegisterScope per owning module.
	// The key ledger (sync_op_hashes, migration 0389) is the engine's
	// fourth guarantee: an idempotency key reused with a different body is
	// refused, never applied.
	syncEngine := sync.NewEngine().
		WithAudit(syncAuditSeam{svc: spine.auditSvc, link: link}, log).
		WithKeyLedger(sync.NewPgKeyLedger(pool))
	// The pick record door (Field CP-2) runs the yard service's RecordPick under the branch fence the
	// REST door applies: the sync store takes the staff authority seam (the subject's authority in
	// the org, with the linked ERP user) here and the yard service, the record body, where that
	// service is built below. A nil pool (the wiring-shape tests) wires no seam; a real boot fails
	// fast without both.
	var syncStaff identity.SyncStaffResolver
	if pool != nil {
		authority, authErr := erpidentity.NewStaffAuthorityResolver(pool)
		if authErr != nil {
			panic("sync: pick record door authority resolver refused to boot: " + authErr.Error())
		}
		syncStaff = func(ctx context.Context, platformUserID, orgID string) (identity.StaffContext, bool, error) {
			a, has, err := authority.ResolveStaff(ctx, platformUserID, orgID)
			if err != nil || !has {
				return identity.StaffContext{}, false, err
			}
			sc := identity.FromAuthority(a)
			if uid, ok := link(ctx, platformUserID); ok {
				sc.UserID = uid
			}
			return sc, true, nil
		}
	}
	pickSync := tasks.NewSyncStore(pool, orderReaderAdapter{repo: orderRepo}, link).WithStaffAuthority(syncStaff).
		WithBranchOrgReader(branchOrg)
	if pool != nil {
		// The pull carries each task's ticket, built by the same feed the REST door reads.
		pickSync.WithPickSource(newDispatchPickSource(pool, orderRepo, log))
	}
	if err := syncEngine.RegisterScope("pick_tasks", pickSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	// The yard-pick item's scopes (HH-3.5 #166): shortages and weights
	// over the tasks module's own tables (task_shortages; the
	// NEED_WEIGHT rows of tasks, each drawing its own scope's counter),
	// and attachments over the D10 module the attachment entity owns.
	// Receiving stays unwired: the frozen schema gives purchasing_receipts
	// no column a per-scope monotonic feed seq can live in (its seq
	// column is the per-PO receiving number, NOT NULL, and carried rows
	// carry the ERP's own per-PO values), which is the D26 schema-shaped
	// stop condition reported to the supervisor rather than routed
	// around; both routes answer the honest 501 for it.
	shortageSync := tasks.NewShortageSyncStore(pool, link).WithStaffAuthority(syncStaff).WithBranchOrgReader(branchOrg)
	if pool != nil && !shortageSync.StaffAuthorityWired() {
		panic("tasks: shortage sync branch fence not wired - a yard device could report on another branch's pick")
	}
	if pool != nil && !shortageSync.BranchOrgWired() {
		panic("tasks: shortage sync org bound not wired - a GLOBAL caller could read a shortage on a branch outside its own org")
	}
	if err := syncEngine.RegisterScope("shortages", shortageSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	weightSync := tasks.NewWeightSyncStore(pool, orderReaderAdapter{repo: orderRepo}, link).WithStaffAuthority(syncStaff).
		WithBranchOrgReader(branchOrg)
	// The weight create's order read and branch reach are the pick door's (UX-81c); a box without them refuses to boot.
	if pool != nil && !weightSync.OrgScoped() {
		panic("tasks: the weight sync create is not bound to the caller's org and branch reach - a caller could request a weight on another dealer's order")
	}
	if err := syncEngine.RegisterScope("weights", weightSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	// THE DRIVER FENCE (the HH Drive fence, the role logins lane): the
	// drive-run adapters and the attachments scope carry the role seam
	// beside the linker, so a driver caller's pulls, loads and pushes
	// hold to the runs assigned to the driver record linked to their
	// login while every desk caller keeps the org scope. The seam is
	// identity's own per-request authority resolution (the same
	// resolution the guards ran at the door, read again here because
	// the sync core's ScopeFilter carries the actor and the org and no
	// role), bridged to the ERP role code the dispatch vocabulary
	// reads. A nil pool (the wiring-shape tests) wires no seam; the boot
	// fail-fasts below keep a real boot from serving these scopes
	// unfenced, the same idiom the will-call pick hold gate carries.
	var driverRole dispatch.SyncRoleResolver
	if pool != nil {
		staffRoles, roleErr := erpidentity.NewStaffAuthorityResolver(pool)
		if roleErr != nil {
			panic("sync: driver fence role resolver refused to boot: " + roleErr.Error())
		}
		driverRole = func(ctx context.Context, platformUserID, orgID string) (string, bool, error) {
			a, has, err := staffRoles.ResolveStaff(ctx, platformUserID, orgID)
			if err != nil || !has {
				return "", has, err
			}
			return identity.FromAuthority(a).RoleCode, true, nil
		}
	}
	attachmentsSync := attachments.NewSyncStore(pool).WithDriverStopFence(dispatch.NewDriverStopFence(pool, link, driverRole))
	if pool != nil && !attachmentsSync.DriverStopFenceWired() {
		panic("attachments: driver stop fence not wired - a driver login's attachments pull would answer every attachment in the org")
	}
	if err := syncEngine.RegisterScope("attachments", attachmentsSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	// The drive-run scopes (HH-3.5 #167) over the dispatch module's
	// own tables: runs and stops server_wins (runs per Colton's
	// ruling, written at end-of-run close), pod manual. The stops
	// adapter validates creates through the same order-read seam the
	// module's REST surface reads; all three attribute actors through
	// the same linker the pilot uses.
	// THE DRIVER FENCE: the seam is wired above the attachments scope
	// (its block carries the argument); the boot fail-fast below keeps
	// an unwired boot from serving the drive-run scopes unfenced, the
	// same idiom the will-call pick hold gate carries.
	runsSync := dispatch.NewRunsSyncStore(pool, link).WithDriverRole(driverRole)
	stopsSync := dispatch.NewStopsSyncStore(pool, dispatchOrderAdapter{repo: orderRepo}, link).WithDriverRole(driverRole).
		WithOrderLines(orderPickLineAdapter{repo: orderRepo})
	podSync := dispatch.NewPODSyncStore(pool, link).WithDriverRole(driverRole).WithStaffAuthority(syncStaff)
	if pool != nil && (!runsSync.DriverRoleWired() || !stopsSync.DriverRoleWired() || !podSync.DriverRoleWired()) {
		panic("dispatch: driver fence role seam not wired - a driver login's sync pull would answer every run in the org")
	}
	if err := syncEngine.RegisterScope("runs", runsSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("stops", stopsSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("pod", podSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	// The internal frontends' outbox scopes (HH-3.5 #168), one
	// RegisterScope per owning module: quotes and price_matrix over the
	// quote and pricing modules' own tables (the matrix's every write,
	// pushed or desk-published, running the pricing module's one
	// list-price path), loads, delivery_stops and will_call over the
	// dispatch module's own tables (the two board scopes riding the
	// stops adapter's own reads and the shared delivery_stops seq
	// stream; the will-call mint hashing the device-generated token at
	// rest exactly as the module hashes its own), purchase_orders over
	// the purchasing module's own purchasing_pos (the contract's PO
	// surface), cycle_counts over the invcount module's own
	// count_sheets (manual, never overwriting).
	boardStops := dispatch.NewStopsSyncStore(pool, dispatchOrderAdapter{repo: orderRepo}, link).WithDriverRole(driverRole)
	if err := syncEngine.RegisterScope("quotes", quote.NewSyncStore(pool, link)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("price_matrix", pricing.NewSyncStore(pool, link)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("loads", dispatch.NewLoadsSyncStore(boardStops)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("delivery_stops", dispatch.NewDeliveryStopsSyncStore(boardStops)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	// Item 9 Opus P1-2: the sync will call mint carries the pick hold gate
	// the Service door carries (the same billing read, the same adapter), and
	// the boot fail-fast keeps a deploy from serving with the sync door dark.
	willCallSync := dispatch.NewWillCallSyncStore(pool, dispatchOrderAdapter{repo: orderRepo}, dispatchPickStateAdapter{repo: tasks.NewRepository(pool)}, dispatchPickHoldAdapter{repo: holdBillingRepo(pool)}, link)
	if !willCallSync.PickHoldGateWired() {
		panic("dispatch: will call sync store pick hold gate not wired - a pay before release order's rack tag could be minted over sync unpaid")
	}
	if err := syncEngine.RegisterScope("will_call", willCallSync); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("purchase_orders", purchasing.NewSyncStore(pool, w4cVendorDirectory{pool: pool}, link)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("cycle_counts", invcount.NewSyncStore(pool, invCountAdapter{svc: inventorySvc}, link)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	// The messaging feeds (House Messages core, roles roadmap Lane E):
	// messages, acks and notify_prefs beside the pull-only channels
	// scope registered with the reference caches below, over the
	// messaging module's own tables. The Phase D stop (no org column on
	// these tables, the GLOBAL channels crossing orgs, and the two
	// per-user tables' composite keys) is closed by 0385's feed keys
	// and by the fence the stores prove, which is stronger than an org
	// predicate: every read resolves the caller's linked ERP user and
	// answers only that user's own world (its channels and their
	// messages, its own acks and prefs), so a row outside the caller's
	// membership is the same as absent whatever org it belongs to.
	if err := syncEngine.RegisterScope("messages", messaging.NewMessagesSyncStore(messagingRepo, link).WithRoomFence(messagingSvc.RoomAdmits)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("acks", messaging.NewAcksSyncStore(messagingRepo, link)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("notify_prefs", messaging.NewNotifyPrefsSyncStore(messagingRepo, link)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	// The contractor outbox scopes (HH-3.5 #169), the plan-flow grain:
	// orders and scope_items over the planflow module's plan and
	// plan_line tables (the board card and its lines; see the module's
	// doc for the scope-name bridge), and delivery_instructions over
	// the card's instruction pair, sharing the plan table's one feed
	// counter. team over the portal module's portal_users (manual, the
	// last-owner guard holding through sync), pickups over dispatch's
	// will_call_pickups (manual, the typed-name attestation sealing
	// through the same provider the public confirm path uses).
	if err := syncEngine.RegisterScope("orders", planflow.NewOrdersSyncStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("scope_items", planflow.NewItemsSyncStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("delivery_instructions", planflow.NewInstructionsSyncStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("team", portal.NewTeamSyncStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	// The pull-only scopes (Colton's START ruling on undeclared scopes,
	// HH-3.5 #169): GET implemented over the owning module's own read,
	// POST refused in the contract's own shape at the route. products
	// and catalog are the one products table read through each caller
	// class's org; trucks and drivers fence through the branch; the
	// warranty pair is two projections over the one claims feed;
	// widget_measures is the caller's own Home configuration; projects,
	// payment_methods and aging are the tenant's reads. channels is the
	// messaging module's own pull-only feed (0385 and the stores in
	// internal/messaging/syncscope.go): the caller's rooms, membership
	// fenced, the stronger-than-org fence the messaging feeds carry.
	// tracking_events stays unwired (the D26 schema-shaped stop the lane
	// reported: no table carries the contractor's tracking events), so
	// it answers 501 on both routes.
	if err := syncEngine.RegisterScope("products", product.NewProductsStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("catalog", product.NewCatalogStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("customers", customer.NewCustomersStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("inventory_lookup", inventory.NewInventoryLookupStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("trucks", dispatch.NewTrucksStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("drivers", dispatch.NewDriversStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("vendors", purchasing.NewVendorsStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("channels", messaging.NewChannelsSyncStore(messagingRepo, link).WithRoomFence(messagingSvc.RoomAdmits)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("parked_tickets", pos.NewParkedTicketsStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("warranty_claims", warranty.NewWarrantyClaimsStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("warranty_board", warranty.NewWarrantyBoardStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("widget_measures", dashboard.NewWidgetMeasuresStore(pool, link)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("projects", procurement.NewProjectsStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("payment_methods", payments.NewPaymentMethodsStore(pool)); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	if err := syncEngine.RegisterScope("aging", billing.NewAgingStore(pool, nil).WithZone(newDealerZone(pool))); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}

	// The approvals scope (HH-3.5 follow-up, server_wins, an org
	// scope) over the approvals rail's own table: the pushed decision
	// runs the rail's own authority rules and CAS stamp, the pushed
	// filing is the caller's own, and the desk's rail writes stamp the
	// feed seq so a device's stale replay meets the decided row. The
	// org fence derives off the request's NOT NULL branch (the
	// adapter's header states the total-derivation argument).
	// The approvals scope carries the JE withdrawal bridge (FW1-12a's core round): a withdrawn credit
	// memo's park is voided on the withdrawal's own transaction, the same adapter the rail's decide
	// path bridges through.
	if err := syncEngine.RegisterScope("approvals", identity.NewApprovalsSyncStore(pool, link).
		WithJEDecider(jeDeciderAdapter{svc: spine.glSvc, journals: gl.NewRepository(pool).WithZone(newDealerZone(pool)), memos: billing.NewInvoiceWriter(pool)})); err != nil {
		panic("sync: scope wiring refused to boot: " + err.Error())
	}
	syncStaffRoles, syncPortalRoles := syncFenceSeams(pool)
	syncRoutes, err := sync.NewHandler(syncEngine, syncStaffRoles, syncPortalRoles, log)
	if err != nil {
		panic("sync: handler construction refused to boot: " + err.Error())
	}
	syncRoutes.RegisterRoutes(r, g)

	// WTY-01 (DIB-153): warranty claims. The settlement journal posts
	// through the GL poster; the SLA notifier posts breaches into
	// #escalations (UNION-W5). The nightly SLA sweep is a worker-role job
	// registration (B7).
	warrantySvc := warranty.NewService(warranty.NewRepository(pool)).
		WithGLPoster(warrantyGLPoster{svc: spine.glSvc}).
		WithAudit(auditSvc).
		WithLogger(log).
		WithSLANotifier(unionSLANotifier{svc: messagingSvc, log: log})

	// The card attempt sweep's alerter (the card charge design section 4, packet C): the sweep
	// posts its operator items to the escalations channel through THIS binding, set ONCE at
	// process start beside the warranty sweep's own post, never per call. The attempt row stays
	// the system of record; the post is visibility.
	payment.AlertFunc = cardAttemptEscalations{svc: messagingSvc}.post

	warrantyHandler := warranty.NewHandler(warrantySvc, log).WithUserLinker(link).WithCustomerReach(newCustomerWall(pool))
	if !warrantyHandler.CustomerReachWired() {
		panic("warranty: customer reach not wired - a claim would file for any named account")
	}
	warrantyHandler.RegisterRoutes(r, g)
	warrantyW5Handler := warranty.NewHandler(warrantySvc, log).WithUserLinker(link)
	warrantyW5Handler.RegisterW5Routes(r, g)
	warrantyW5Handler.RegisterPublicRoutes(r)

	// SWARM-W1A-B: the printing module, print_queue's first consumer.
	printRepo := printing.NewRepository(pool)
	printQueue := printing.NewQueueService(printRepo)
	receiptSvc := printing.NewReceiptService(printRepo, printQueue).WithAudit(auditSvc).WithStandardRate(newTaxEngine(pool)).WithZone(newDealerZone(pool)).WithTaxLabel(newTaxWords(pool))
	printing.NewHandler(receiptSvc, printQueue, log).WithUserLinker(link).RegisterRoutes(r, g)

	// SWARM-W2-A: dispatch, the physical leg. The signature-capture seam
	// (pos) is unlanded and stays unwired: unwired, a POD capture refuses
	// rather than writing an attestation nobody sealed (fail closed). The
	// AR gate keeps the pin's nil default (allow; the billing predicate
	// is B6's to bind).
	dispatchSvc := dispatch.NewService(dispatch.NewRepository(pool)).
		WithActorNames(staffNameAdapter{repo: orderRepo}).
		WithOrders(dispatchOrderAdapter{repo: orderRepo}).
		WithFulfiller(dispatchFulfilAdapter{svc: orderSvc}).
		WithPrinting(dispatchPrintAdapter{queue: printQueue}).
		WithOrderValues(dispatchOrderValueAdapter{pool: pool}).
		WithOrderLines(orderPickLineAdapter{repo: orderRepo}).
		WithStockAvailability(dispatchStockAdapter{repo: inventoryRepo}).
		WithFreightApprovals(dispatchFreightApprovalAdapter{svc: spine.approvals}).
		WithOrderFreight(dispatchOrderFreightAdapter{pool: pool}).
		WithPickState(dispatchPickStateAdapter{repo: tasks.NewRepository(pool)}).
		WithAuditTrail(auditSvc).
		WithAudit(auditSvc).
		WithLogger(log)
	// The door money fence (ruling 113): the payment hold's figure and the gate's own sentence ride
	// only to a caller on the configuration's cost and margin list (dispatch/cost_gate.go), the same
	// rule the stock reads and the yard payloads answer through.
	dispatchSvc.WithCostRoles(spine.roleCfg)
	if !dispatchSvc.CostRolesWired() {
		panic("dispatch: cost roles not wired - the payment hold would answer the door money figure to every caller")
	}
	// The pod scope's typed name attestation door (Field CP-2) runs the same CapturePOD body the REST
	// door runs; the boot fail fast keeps a deploy from serving with the door dark.
	podSync.WithAttestor(dispatchSvc)
	if pool != nil && !podSync.AttestDoorWired() {
		panic("dispatch: pod attestation sync door not wired - a driver's offline proof of delivery would refuse every replay")
	}
	dispatchHandler := dispatch.NewHandler(dispatchSvc, log).WithUserLinker(link)
	dispatchHandler.RegisterRoutes(r, g)
	dispatchHandler.RegisterPublicRoutes(r)
	dispatchHandler.RegisterW3Routes(r, g)

	// The freight bridge's return leg (0195): a rail decision on a parked
	// freight raise applies or refuses it inside the decision
	// transaction. Unwired it would stamp the request and move no money
	// (the safe half of the kill switch); wired, both arms cross.
	spine.approvals.WithFreightDecider(dispatchFreightDeciderAdapter{svc: dispatchSvc})

	// SWARM-W3-C: the yard surface over the pick feed.	// The stage-outbox seam (messagingprovider, landed with the fold): the
	// pin's W2-B wiring handed the yard repository the SMS outbox so a staged
	// pick enqueues its customer notice INSIDE the stage transaction. Nil (the
	// DB-free proof constructions) leaves the seam dark, the module's own
	// unwired posture.
	yardRepo := tasks.NewRepository(pool)
	if smsOutbox != nil {
		yardRepo = yardRepo.WithStageOutbox(stageNoticeAdapter{outbox: smsOutbox})
	}
	yardTasksSvc := tasks.NewService(yardRepo).
		WithOrderReader(orderReaderAdapter{repo: orderRepo}).
		// N2 (PR 302 round 2): the yard notice fence asks SeesCostAndMargin
		// for every figure decision, the same X3 rule cost_gate reads.
		WithChatNotifier(yardChatNotifier{svc: messagingSvc, log: log, figuresOK: spine.roleCfg.SeesCostAndMargin}).
		WithPickSource(newDispatchPickSource(pool, orderRepo, log)).
		WithLogger(log)
	// The yard's shortage notice names its reporter by role label (UX-76); a nil pool (the wiring shape
	// tests) leaves it saying "a teammate".
	if pool != nil {
		roleLabels, err := erpidentity.NewStaffAuthorityResolver(pool)
		if err != nil {
			panic("tasks: yard shortage notice role labels refused to boot: " + err.Error())
		}
		yardTasksSvc.WithRoleLabels(roleLabels)
	}
	yardTasksSvc.
		WithOrderLineWriter(orderLineWeightAdapter{svc: orderSvc, repo: orderRepo}).
		WithOrderAdvancer(taskOrderAdvanceAdapter{svc: orderSvc}).
		WithAudit(auditSvc)
	// The yard's short pick: the order takes the shortfall as a backorder and the notice facts read
	// the live invoice through billing (wire_yard_short.go).
	orderSvc.WithInvoiceStanding(invoiceStandingAdapter{repo: billing.NewRepository(pool).WithZone(newDealerZone(pool))})
	// The delivery road asks billing, the owner of the deposit documents, to release them (deposit_release.go).
	orderSvc.WithDepositReleaser(newInvoiceWriter(pool))
	orderSvc.WithBackorderNotifier(backorderNoticeAdapter{chat: yardChatNotifier{svc: messagingSvc, log: log, figuresOK: spine.roleCfg.SeesCostAndMargin}})
	yardTasksSvc.WithYardOrderChanges(yardOrderAdapter{svc: orderSvc})
	// The sync record door's body is the yard service, the one the REST record door runs.
	pickSync.WithPickRecorder(yardTasksSvc)
	// The pick payload names a claimed pick's holder by role label, never by name (UX-75): identity's
	// reader serves the REST detail and list and the pick_tasks rows alike. A nil pool wires none.
	if pool != nil {
		claimerRoles, rolesErr := erpidentity.NewStaffAuthorityResolver(pool)
		if rolesErr != nil {
			panic("tasks: claimer role labels refused to boot: " + rolesErr.Error())
		}
		yardTasksSvc.WithClaimerRoles(claimerRoles)
		pickSync.WithClaimerRoles(claimerRoles)
	}
	// A shortage created through sync tells the counter as the REST report does, through the yard
	// service's notifier.
	shortageSync.WithNoticer(yardTasksSvc)
	// The creates' order reads and the ticket reads are held to the caller's org (UX-81 round 4); a box that
	// wired a seam without the org held read refuses to boot rather than read across orgs.
	if pool != nil && (!pickSync.OrgScoped() || !yardTasksSvc.OrgScoped()) {
		panic("tasks: the pick order and ticket reads are not org scoped - a caller could create on, or read the ticket of, another org's order")
	}
	if pool != nil && !pickSync.PickDoorWired() {
		panic("tasks: pick record sync door not wired - a yard device's recorded pick would refuse every replay")
	}
	// Item 9 Opus P2-1: the yard's doors (Claim, Stage) carry the pick hold
	// the will call doors carry, over the same billing read, with the boot
	// fail-fast keeping a deploy from serving with the yard door dark.
	yardTasksSvc.WithPickHoldCheck(tasksPickHoldAdapter{repo: holdBillingRepo(pool)})
	if !yardTasksSvc.PickHoldCheckWired() {
		panic("tasks: yard pick hold check not wired - a pay before release order's task could be claimed and staged unpaid")
	}
	// The yard fence's money half: a held ticket's still owed figure and the skid answer's deposit
	// pair ride only for a caller on the configuration's cost and margin list (tasks/cost_gate.go).
	yardHandler := tasks.NewHandler(yardTasksSvc, log).WithUserLinker(link).WithCostRoles(spine.roleCfg).WithBranchOrgReader(branchOrg)
	if !yardHandler.CostRolesWired() {
		panic("tasks: cost roles not wired - the yard payloads would answer the still owed and deposit figures to every caller")
	}
	yardHandler.RegisterYardRoutes(r, g)
	// The yard REST doors' fences bound a GLOBAL caller's reach to its own org's branches through the branch to org
	// seam (UX-81 round 5); a handler without it would admit another dealer's branch id.
	if pool != nil && (!tasksHandler.BranchOrgWired() || !yardHandler.BranchOrgWired()) {
		panic("tasks: the yard REST fences are not bound to the caller's org - a GLOBAL caller could reach another dealer's branch")
	}
	weightHandler := tasks.NewHandler(yardTasksSvc, log).WithUserLinker(link).WithBranchOrgReader(branchOrg)
	// The weight request holds the same fences as the yard doors beside it (UX-81c).
	if pool != nil && !weightHandler.BranchOrgWired() {
		panic("tasks: the weight REST fence is not bound to the caller's org - a GLOBAL caller could reach another dealer's branch")
	}
	weightHandler.RegisterWeightRoutes(r, g)

	// The rail's HTTP surface (the A3 ownership ruling): the staff
	// approvals routes register through the Guards with the same linker,
	// and the decide path's authority is the guard's resolution (nothing
	// on it reads the scaffold tables).
	identity.NewApprovalsHandler(spine.approvals, link).RegisterRoutes(r, g)

	// The staff roster read (identityListStaff): the identity package answers it over its own tables and
	// the route sits behind the manager tier. Read only; FB Console issues users (D38).
	identity.NewStaffRosterHandler(erpidentity.NewStaffRosterReader(pool)).RegisterRoutes(r, g)

	// SWARM-W3-B: payments (F-1's assignment to this batch). The gateway
	// selector is fail-closed at boot exactly as at the pin (DIB-416):
	// live-without-credentials refuses, production on a non-live gateway
	// refuses. The service this batch constructs for the portal union is
	// built so the selection and wiring are proven; the portal BFF that
	// consumes it lands in B7.
	unionGateway, unionGwErr := payments.SelectGateway(payments.LoadGatewayConfig())
	if unionGwErr != nil {
		panic("payments: gateway selection refused to boot: " + unionGwErr.Error())
	}
	ops.unionGateway = unionGateway
	w3bPaymentsRepo := payments.NewRepository(pool).WithTill(tillSvc)
	w3bPaySvc := payments.NewService(w3bPaymentsRepo, unionGateway).WithCurrency(newDealerCurrency(pool))
	_ = w3bPaySvc // consumed by the portal BFF at the tops batch; constructed so boot proves it wires
	// DIB-170: payment reversal, manager-gated, reason-required, audited.
	w3bReverseSvc := payments.NewReverseService(w3bPaymentsRepo).WithAudit(auditSvc)
	// FW1-06b: the true void of a mis-keyed desk receipt, manager-gated, reason-required, audited,
	// the same dealer day only, no 2310 hold. The two undos share a mounting and stay apart in their
	// services and their journals.
	w3bVoidSvc := payments.NewVoidService(w3bPaymentsRepo).WithAudit(auditSvc).WithZone(newDealerZone(pool))
	payments.NewReversalHandler(w3bReverseSvc, log).WithUserLinker(link).WithVoidService(w3bVoidSvc).RegisterRoutes(r, g)

	return &operationsSpine{
		lienSvc:            lienSvc,
		dispatchSvc:        dispatchSvc,
		yardTasksSvc:       yardTasksSvc,
		warrantySvc:        warrantySvc,
		messagingSvc:       messagingSvc,
		messagingRepo:      messagingRepo,
		printQueue:         printQueue,
		tillRepo:           tillRepo,
		tillSvc:            tillSvc,
		paymentsReverseSvc: w3bReverseSvc,
		purchasingSvc:      purchasingSvc,
		landedcostSvc:      landedcostSvc,
		syncEngine:         syncEngine,
	}
}

// ── the carried adapter belt (pin main.go, verbatim where the dependency
// landed; the identity.Service adapters re-point at the bridge's
// Approvals, the sanctioned class) ─────────────────────────────────────

// glPosterAdapter satisfies bankrec.JournalPoster over the GL provider
// (glSvc.PostExternal). It lives in the wire set so bankrec does not
// import gl's service internals.
type glPosterAdapter struct{ svc *gl.Service }

func (a glPosterAdapter) PostJournal(ctx context.Context, in gl.ExternalJournal) (string, error) {
	return a.svc.PostExternal(ctx, in)
}

// PostJournalTx is PostJournal through the caller's transaction
// (gl.Service.PostExternalTx), bankrec's JournalPosterTx arm, so a bank
// match and its clearing journal commit or roll back together (FW1-05, live
// in the served core, P1-A round 2).
func (a glPosterAdapter) PostJournalTx(ctx context.Context, tx pgx.Tx, in gl.ExternalJournal) (string, error) {
	return a.svc.PostExternalTx(ctx, tx, in)
}

// glBalanceAdapter satisfies bankrec.GLBalanceReader (and invcount's)
// over the GL provider, so both read GL balances through the service
// rather than the 0034 views directly.
type glBalanceAdapter struct{ svc *gl.Service }

func (a glBalanceAdapter) GLBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error) {
	return a.svc.GLBalanceCents(ctx, accountCode, year, month, branchID)
}

func (a glBalanceAdapter) GLClosingBalanceCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error) {
	return a.svc.GLClosingBalanceCents(ctx, accountCode, year, month, branchID)
}

func (a glBalanceAdapter) GLBalanceAsOfCents(ctx context.Context, accountCode string, asOf time.Time, branchID string) (int64, error) {
	return a.svc.GLBalanceAsOfCents(ctx, accountCode, asOf, branchID)
}

// GLBalanceThroughCents is the cumulative through-period read the inventory tie-out's GL side takes
// (FW1-11), answered by the same gl provider.
func (a glBalanceAdapter) GLBalanceThroughCents(ctx context.Context, accountCode string, year, month int, branchID string) (int64, error) {
	return a.svc.GLBalanceThroughCents(ctx, accountCode, year, month, branchID)
}

// lienGLPoster satisfies lien.GLPoster over glSvc.PostExternal (the
// 1150/2350 holdback release journal).
type lienGLPoster struct{ svc *gl.Service }

func (a lienGLPoster) PostJournal(ctx context.Context, in lien.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey, Memo: in.Memo, BranchID: in.BranchID, Lines: lines})
}

// invcountGLPoster satisfies invcount.GLPoster over glSvc.PostExternal
// (the reason-coded variance journal). The document date rides: the count's variance
// journal is dated at the count's own instant, the one its COUNT move carries, so the two
// sides of one count are cut at the same moment (W2-5 fix r10, S5's P2-7).
type invcountGLPoster struct{ svc *gl.Service }

func (a invcountGLPoster) PostJournal(ctx context.Context, in invcount.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey, Memo: in.Memo, BranchID: in.BranchID, DocumentDate: in.DocumentDate, Lines: lines})
}

// finworldPurchasingComposition builds the purchasing service the way the production wiring does,
// with the shared pool-level seams: the dealer zone, the vendors projection, the branch directory,
// the vendor address book and the AP subledger's settlement reader over the ledger. The caller adds
// the request-shaped seams (stock receiver, landed cost, GL poster, rules) it needs.
func finworldPurchasingComposition(pool *pgxpool.Pool, log *slog.Logger, auditSvc *audit.Service) *purchasing.Service {
	return purchasing.NewService(purchasing.NewRepository(pool)).
		WithZone(newDealerZone(pool)).
		WithVendors(w4cVendorDirectory{pool: pool}).
		WithAPPaymentsReader(glPaidAPReader{pool: pool, accountCode: "2100"}).
		WithAudit(auditSvc).
		WithLogger(log).
		WithBranches(item5BranchDirectory{pool: pool}).
		WithVendorAddresses(item5VendorAddressBook{pool: pool})
}

// glPaidAPReader satisfies purchasing.APPaymentsReader over the ledger: the net of the
// PAYMENT-source journals on the trade payables account, the settlements side of the AP subledger.
// A supplier payment is a PAYMENT journal on the payables account; nothing else posts that
// combination, so no idempotency-key pattern matching is needed.
type glPaidAPReader struct {
	pool        *pgxpool.Pool
	accountCode string
}

func (r glPaidAPReader) PaidAPCentsAsOf(ctx context.Context, asOf time.Time) (int64, error) {
	var paid int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED'
		   AND j.source = 'PAYMENT'
		   AND a.code = $1
		   AND j.posted_at <= $2`, r.accountCode, asOf).Scan(&paid)
	if err != nil {
		return 0, fmt.Errorf("the supplier payment read: %w", err)
	}
	return paid, nil
}

// landedGLPoster satisfies landedcost.GLPoster over glSvc.PostExternal
// (the capitalization / retro-split journal) and glSvc.PostExternalTx
// (the Void reversing journal through the void's own transaction).
type landedGLPoster struct{ svc *gl.Service }

func landedGLExtLines(in []landedcost.GLLine) []gl.ExternalLine {
	lines := make([]gl.ExternalLine, len(in))
	for i, l := range in {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return lines
}

func (a landedGLPoster) PostJournal(ctx context.Context, in landedcost.GLJournal) (string, error) {
	return a.svc.PostExternal(ctx, gl.ExternalJournal{Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey, Memo: in.Memo, BranchID: in.BranchID, DocumentDate: in.DocumentDate, Lines: landedGLExtLines(in.Lines)})
}

// PostJournalTx is PostJournal through the caller's transaction (gl.Service.PostExternalTx).
func (a landedGLPoster) PostJournalTx(ctx context.Context, tx pgx.Tx, in landedcost.GLJournal) (string, error) {
	return a.svc.PostExternalTx(ctx, tx, gl.ExternalJournal{Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey, Memo: in.Memo, BranchID: in.BranchID, DocumentDate: in.DocumentDate, Lines: landedGLExtLines(in.Lines)})
}

// warrantyGLPoster satisfies warranty.GLPoster over glSvc.PostExternal
// (the warranty.settled recovery/expense journal).
type warrantyGLPoster struct{ svc *gl.Service }

func (a warrantyGLPoster) PostJournal(ctx context.Context, in warranty.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey, Memo: in.Memo, BranchID: in.BranchID, Lines: lines})
}

// staffNameAdapter satisfies dispatch's actor-name resolver (and the pos
// and billing histories at the pin) over order.Repository's existing
// users join, so no module gains a copy of the display-name SELECT.
type staffNameAdapter struct{ repo *order.Repository }

func (a staffNameAdapter) ActorNames(ctx context.Context, ids []string) (map[string]string, error) {
	return a.repo.ActorNames(ctx, ids)
}

// posBranchNameAdapter satisfies pos.BranchNamer over the branches module's service, so the customer
// lines read names each order's branch without pos reading the locations table.
type posBranchNameAdapter struct{ svc *branches.Service }

func (a posBranchNameAdapter) BranchNames(ctx context.Context) (map[string]string, error) {
	list, err := a.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, b := range list {
		out[b.ID] = b.Name
	}
	return out, nil
}

// orderReaderAdapter satisfies tasks.OrderReader over the order
// Repository's narrow OrderBrief read (id/branch/fulfilment), mapping
// order.ErrNotFound onto tasks.ErrUnknownOrder so tasks never imports
// order.
type orderReaderAdapter struct{ repo *order.Repository }

func (a orderReaderAdapter) OrderBrief(ctx context.Context, orderID string) (tasks.OrderBrief, error) {
	b, err := a.repo.OrderBrief(ctx, orderID)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			return tasks.OrderBrief{}, tasks.ErrUnknownOrder
		}
		return tasks.OrderBrief{}, err
	}
	return tasks.OrderBrief{ID: b.ID, BranchID: b.BranchID, Fulfillment: b.Fulfillment, Status: b.Status}, nil
}

// OrderBriefInOrg is the org held read the tasks creates use (UX-81 round 4): an order of another org maps onto
// ErrUnknownOrder, the same as a missing one.
func (a orderReaderAdapter) OrderBriefInOrg(ctx context.Context, orgID, orderID string) (tasks.OrderBrief, error) {
	b, err := a.repo.OrderBriefInOrg(ctx, orgID, orderID)
	if err != nil {
		if errors.Is(err, order.ErrNotFound) {
			return tasks.OrderBrief{}, tasks.ErrUnknownOrder
		}
		return tasks.OrderBrief{}, err
	}
	return tasks.OrderBrief{ID: b.ID, BranchID: b.BranchID, Fulfillment: b.Fulfillment, Status: b.Status}, nil
}

var (
	_ tasks.OrgOrderReader = orderReaderAdapter{}
	_ tasks.OrgPickSource  = dispatchPickSource{}
	// OrgPendingSource: the pending feed keyed by branch, held to the caller's org (UX-81 round 5).
	_ tasks.OrgPendingSource = dispatchPickSource{}
	_ tasks.BranchOrgReader  = branchOrgAdapter{}
)

// branchOrgAdapter answers the one fact the tasks module may not read itself: whether a location is a branch of an
// org (UX-81 round 5). It rides the branches module's own read.
type branchOrgAdapter struct{ repo *branches.Repository }

func (a branchOrgAdapter) BranchInOrg(ctx context.Context, orgID, branchID string) (bool, error) {
	return a.repo.BranchInOrg(ctx, orgID, branchID)
}

// taskChatNotifier satisfies tasks.ChatNotifier over messaging's
// event-post seam: the order's branch yard channel when known, else the
// default channel, idempotent on the event key.
type taskChatNotifier struct{ svc *messaging.Service }

func (a taskChatNotifier) PostTaskEvent(ctx context.Context, ref tasks.TaskEventRef) error {
	slug := tasksDefaultChannelSlug
	if ref.BranchID != "" {
		if s, err := a.svc.YardSlugForBranch(ctx, ref.BranchID); err == nil && s != "" {
			slug = s
		}
	}
	_, _, err := a.svc.PostEventToChannelSlug(ctx, slug, ref.EventKey, ref.Summary, messaging.EventRef{
		Topic: ref.Topic, Entity: ref.Entity, DeepLink: ref.DeepLink, Summary: ref.Summary,
	}, false)
	return err
}

// yardChatNotifier is taskChatNotifier reading ref.Audience and routing
// accordingly: yard events to the branch yard channel, counter events to the
// counter channel with the yard channel and #office as fallbacks.
type yardChatNotifier struct {
	svc       yardChatMessenger
	log       *slog.Logger
	figuresOK func(role string) (bool, error)
}

// yardChatMessenger is the messaging seam yardChatNotifier reads: the yard
// channel for a branch, the channel post and the order rep routing, which
// picks the body for the rep it routes to (PR 302 round 3, P2-1).
// *messaging.Service satisfies it; the notice fence's untagged legs
// (yard_notice_fence_test.go) drive the routing with a double that routes as
// messaging does, and the rep DM integration legs drive the real service.
type yardChatMessenger interface {
	YardSlugForBranch(ctx context.Context, branchID string) (string, error)
	PostEventToChannelSlug(ctx context.Context, slug, eventKey, body string, ref messaging.EventRef, requiresAck bool) (messaging.Message, bool, error)
	NotifyOrderRep(ctx context.Context, in messaging.OrderRepNotice) (messaging.OrderRepResult, error)
}

func (a yardChatNotifier) PostTaskEvent(ctx context.Context, ref tasks.TaskEventRef) error {
	yardSlug := tasksDefaultChannelSlug
	if ref.BranchID != "" {
		if s, err := a.svc.YardSlugForBranch(ctx, ref.BranchID); err == nil && s != "" {
			yardSlug = s
		}
	}
	if ref.Audience == tasks.AudienceOrderRep {
		return a.postOrderRepNotice(ctx, ref, yardSlug)
	}
	candidates := []string{yardSlug, yardCounterFallbackSlug}
	if ref.Audience == tasks.AudienceCounter {
		if counter := tasks.CounterSlugForYardSlug(yardSlug); counter != "" {
			candidates = append([]string{counter}, candidates...)
		}
	}

	var lastErr error
	for _, slug := range candidates {
		_, _, err := a.svc.PostEventToChannelSlug(ctx, slug, ref.EventKey, ref.Summary, messaging.EventRef{
			Topic: ref.Topic, Entity: ref.Entity, DeepLink: ref.DeepLink, Summary: ref.Summary,
		}, false)
		if err == nil {
			return nil
		}
		lastErr = err
		if a.log != nil {
			a.log.Warn("yard: channel post failed, trying the next route", "slug", slug, "topic", ref.Topic, "err", err)
		}
	}
	return lastErr
}

// orderRepFallbackNote is the sentence put in front of an order notice that reached the counter
// because the order has no sales rep, so a reader knows why it arrived there.
const orderRepFallbackNote = "No sales rep is on this order, so this came to the counter. "

// unreachableRepNote is the same sentence for a notice whose rep route itself failed.
const unreachableRepNote = "The sales rep could not be reached, so this came to the counter. "

// orderRepFallbackRoute is one fallback channel for an order rep notice, with the body it may
// carry. The figures flag is decided by the cost and margin rule (N2, PR 302
// round 2), not a hardcoded `true`: the counter channel (#sales-<branch>,
// BRANCH scope narrowed to the OUTSIDE_SALES role) seats the sales floor
// alone by derivation, so its figures ride only when SeesCostAndMargin says
// yes for OUTSIDE_SALES; #office (GLOBAL, every active staff member) and the
// yard channel (#yard-<branch>, BRANCH, every role granted the branch) seat
// off list readers by construction, so their copies carry the figureless
// words in every configuration (ruling 91: a figure never reaches an off
// list role, in cents or in words). An error reading the rule fails closed
// to the figureless copy.
type orderRepFallbackRoute struct {
	slug    string
	figures bool
}

// counterAudienceRole is the canonical role id the counter channel seats
// by derivation (the SyncMembership derived rule's auto_role on the seeded
// #sales-<branch> channels in migration 0076 reads "OUTSIDE_SALES", the
// ERP-coded form of `outside_sales`). The rule is asked the canonical form
// because roleconfig.IsRole and SeesCostAndMargin answer against the
// carried vocabulary's lowercase canonical ids. A dealer that takes the
// counter's audience off the list flips the counter copy to figureless
// (N2's fail open direction, closed by the rule).
const counterAudienceRole = "outside_sales"

// orderRepFallbackRoutes builds the chain in order: the counter channel, then #office, then the
// yard channel. The figures flag is computed by the cost and margin rule
// (N2); counterFigures may be false when the dealer takes the counter's
// audience off the list, in which case every channel in the chain carries
// the figureless twin.
func orderRepFallbackRoutes(yardSlug string, counterFigures bool) []orderRepFallbackRoute {
	routes := []orderRepFallbackRoute{{slug: yardCounterFallbackSlug}}
	if counter := tasks.CounterSlugForYardSlug(yardSlug); counter != "" {
		routes = append([]orderRepFallbackRoute{{slug: counter, figures: counterFigures}}, routes...)
	}
	return append(routes, orderRepFallbackRoute{slug: yardSlug})
}

// roleAllowed is the yard notice fence's SeesCostAndMargin read, fail closed
// on every leg but a resolved yes. Wired at the composition root from the
// same roleCfg the dispatch and yard fences read; the rule is the one X3
// predicate, never a second rule that can drift.
func roleAllowed(ask func(role string) (bool, error), role string) bool {
	if ask == nil || role == "" {
		return false
	}
	ok, err := ask(role)
	return err == nil && ok
}

// postOrderRepNotice delivers a yard change on an order to the order's sales rep through the
// messaging module's rep routing (the order's override, then its own salesperson, then the
// customer's rep). N2 (PR 302 round 2) asks the cost and margin rule for
// every figure decision, with two independent decisions:
//   - the rep DM body: figures ride only when the role of the rep the DM goes
//     to is on the list (messaging decides once it has resolved that rep, so
//     an order override, the salesperson and the customer's rep are each
//     judged on their own role); otherwise the figureless twin rides (the
//     rep DM still happens, just without the figure, so a rep knows what
//     changed)
//   - the counter channel copy: figures ride only when OUTSIDE_SALES is on
//     the list (the channel's derived audience)
//
// The two decisions do not bleed: a ref whose rep DM goes figureless still
// posts the figured body to the counter when the rule admits it, because the
// counter's audience is the cost and margin rule on OUTSIDE_SALES, not the
// notice's rep. With no rep, the rep route failed, or the rule refused at
// either decision point, the notice falls through to the chain. Each copy
// is worded for the channel it lands in (orderRepFallbackRoute): the notice
// must arrive somewhere a person reads it, and the figures must not arrive
// where an off list role reads them. "Figureless on error or no", every
// error path leaves the figure in the figureless twin rather than letting
// it ride to a reader the rule has not admitted.
func (a yardChatNotifier) postOrderRepNotice(ctx context.Context, ref tasks.TaskEventRef, yardSlug string) error {
	// The counter route's figure decision (N2, OUTSIDE_SALES today).
	counterFigures := roleAllowed(a.figuresOK, counterAudienceRole)
	// The rep DM's figure decision is made by messaging once it knows which
	// rep the DM goes to (the order's override, the salesperson, then the
	// customer's rep): it gets both bodies and the rule, never a guess here
	// about whose DM it is.
	in := messaging.OrderRepNotice{
		CustomerID: ref.CustomerID, OrderID: ref.OrderID, OrderRepUserID: ref.RepUserID,
		EventKey: ref.EventKey, Body: ref.Summary, BodyNoFigures: ref.SummaryNoFigures,
		RoleSeesFigures: func(role string) bool { return roleAllowed(a.figuresOK, role) },
		Ref:             messaging.EventRef{Topic: ref.Topic, Entity: ref.Entity, DeepLink: ref.DeepLink, Summary: ref.Summary},
	}
	res, err := a.svc.NotifyOrderRep(ctx, in)
	if err == nil && res.Kind == messaging.RouteRepDM {
		return nil
	}
	prefix := orderRepFallbackNote
	if err != nil {
		if a.log != nil {
			a.log.Warn("yard: order rep notice failed, sending to the counter", "order", ref.OrderID, "err", err)
		}
		prefix = unreachableRepNote
	}
	return a.fallbackOrderNotice(ctx, ref, orderRepFallbackRoutes(yardSlug, counterFigures), prefix)
}

// fallbackOrderNotice posts the notice to the first reachable fallback channel, wording each
// copy for the channel it lands in. Every channel with figures=false carries
// ref.SummaryNoFigures (the figureless twin, computed upstream); the rest
// carry ref.Summary verbatim. Office and yard always sit on figures=false
// here; the counter is the only one whose figures flag the rule sets, and
// when OUTSIDE_SALES is off the list every channel in the chain is figureless
// (N2). A body with no figureless twin carries its own words everywhere: it
// names no figure to begin with, and a summary that names a figure with no twin is refused
// before any copy is posted (the fence reads the messaging module's own figure predicate, so
// the two fences cannot drift).
var errFallbackFigure = errors.New("yard: order notice summary names a currency figure and no figureless twin was given")

func (a yardChatNotifier) fallbackOrderNotice(ctx context.Context, ref tasks.TaskEventRef, routes []orderRepFallbackRoute, prefix string) error {
	plain := ref.SummaryNoFigures
	if plain == "" {
		plain = ref.Summary
		if messaging.ContainsCurrencyFigure(ref.Summary) {
			for _, r := range routes {
				if !r.figures {
					return errFallbackFigure
				}
			}
		}
	}
	var lastErr error
	for _, r := range routes {
		body := ref.Summary
		if !r.figures {
			body = plain
		}
		body = prefix + body
		eref := messaging.EventRef{Topic: ref.Topic, Entity: ref.Entity, DeepLink: ref.DeepLink, Summary: body}
		if _, _, err := a.svc.PostEventToChannelSlug(ctx, r.slug, ref.EventKey, body, eref, false); err == nil {
			return nil
		} else {
			lastErr = err
			if a.log != nil {
				a.log.Warn("yard: channel post failed, trying the next route", "slug", r.slug, "topic", ref.Topic, "err", err)
			}
		}
	}
	return lastErr
}

// dispatchOrderAdapter satisfies dispatch.OrderReader over the order
// Repository's OrderBrief (the authority read for a stop's branch and
// schedulability).
type dispatchOrderAdapter struct{ repo *order.Repository }

func (a dispatchOrderAdapter) Brief(ctx context.Context, orderID string) (dispatch.OrderBrief, error) {
	b, err := a.repo.OrderBrief(ctx, orderID)
	if err != nil {
		return dispatch.OrderBrief{}, err
	}
	return dispatch.OrderBrief{ID: b.ID, BranchID: b.BranchID, Fulfillment: b.Fulfillment, Status: b.Status}, nil
}

// dispatchTenantOrders satisfies dispatch.TenantOrderReader over the
// order Repository's tenant-scoped reads (the pickups sync adapter's
// only order access: the tenant's brief for a will-call request, the
// tenant's order id set for the feed predicate).
type dispatchTenantOrders struct{ repo *order.Repository }

func (a dispatchTenantOrders) TenantBrief(ctx context.Context, tenantID, orderID string) (dispatch.OrderBrief, bool, error) {
	b, ok, err := a.repo.OrderBriefForTenant(ctx, tenantID, orderID)
	if err != nil || !ok {
		return dispatch.OrderBrief{}, false, err
	}
	return dispatch.OrderBrief{ID: b.ID, BranchID: b.BranchID, Fulfillment: b.Fulfillment, Status: b.Status}, true, nil
}

func (a dispatchTenantOrders) TenantOrderIDs(ctx context.Context, tenantID string) ([]string, error) {
	return a.repo.OrderIDsByCustomer(ctx, tenantID)
}

// ResolveOrderID completes dispatch.OrderReader (DIB-483): pure
// delegation to order.Repository.ResolveOrderID, the system's single
// order-number resolver, already carrying the UUID passthrough and the
// branch-scoped number lookup that keep #SO- numbers from being an
// existence oracle for another branch's book.
func (a dispatchOrderAdapter) ResolveOrderID(ctx context.Context, idOrNo string, global bool, branchIDs []string) (string, error) {
	return a.repo.ResolveOrderID(ctx, idOrNo, global, branchIDs)
}

// dispatchFulfilAdapter satisfies dispatch.OrderFulfiller over
// order.Service.AdvanceFromDelivery, the fulfilment roads' door over the
// guarded event writer (it also releases a supplier deposit): the move is
// graded against the forward-only ladder inside the guarded write.
type dispatchFulfilAdapter struct{ svc *order.Service }

func (a dispatchFulfilAdapter) MarkFulfilled(ctx context.Context, orderID, reason string) error {
	return a.svc.AdvanceFromDelivery(ctx, orderID, reason)
}

// MarkFulfilledTx is MarkFulfilled on the caller's transaction (round 2, S3's P2): the offline
// pickup's confirm runs the order's fulfilment inside the confirm's own transaction.
func (a dispatchFulfilAdapter) MarkFulfilledTx(ctx context.Context, tx pgx.Tx, orderID, reason string) error {
	return a.svc.AdvanceFromDeliveryTx(ctx, tx, orderID, reason)
}

// dispatchPrintAdapter satisfies dispatch.PrintEnqueuer over
// printing.QueueService.Enqueue (DIB-411).
type dispatchPrintAdapter struct{ queue *printing.QueueService }

func (a dispatchPrintAdapter) Enqueue(ctx context.Context, in dispatch.PrintJobRequest) (string, error) {
	job, err := a.queue.Enqueue(ctx, printing.EnqueueInput{
		DocType:      in.DocType,
		RefID:        in.RefID,
		BranchID:     in.BranchID,
		RenderTarget: in.RenderTarget,
	})
	if err != nil {
		return "", err
	}
	return job.ID, nil
}

// orderPickLineAdapter satisfies dispatch.OrderLineReader over the order
// Repository's STAFF-side line read. It drops the money: a pick ticket
// carries what to pull and how much of it, and the picker's number is the
// ENTERED quantity in the ENTERED unit, never qty_base.
type orderPickLineAdapter struct{ repo *order.Repository }

// orderedWithBackorder is what the line was ordered for (the quantity now to fulfil plus any held on
// backorder) and the held part, as decimal strings; the line's own text when nothing is held. The
// yard compares what it picked with the order line's own ordered quantity, so the ticket carries it.
func orderedWithBackorder(qty, backorder string) (ordered, backordered string) {
	q, okQ := new(big.Rat).SetString(qty)
	b, okB := new(big.Rat).SetString(backorder)
	if !okQ || !okB || b.Sign() <= 0 {
		return qty, ""
	}
	return new(big.Rat).Add(q, b).FloatString(4), b.FloatString(4)
}

func (a orderPickLineAdapter) OrderPickLines(ctx context.Context, orderID string) ([]dispatch.PickFeedLine, error) {
	lines, _, _, err := a.repo.OrderLines(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := make([]dispatch.PickFeedLine, 0, len(lines))
	for _, l := range lines {
		ordered, backordered := orderedWithBackorder(l.QtyEntered, l.BackorderEntered)
		out = append(out, dispatch.PickFeedLine{
			LineNo:         l.LineNo,
			ProductID:      l.ProductID,
			Description:    l.Description,
			QtyOrdered:     ordered,
			QtyBackordered: backordered,
			UOM:            l.UOMEntered,
		})
	}
	return out, nil
}

// dispatchPickStateAdapter satisfies dispatch.PickCompletionReader over
// the yard module's own read: STAGED and DONE both mean the material was
// pulled, which is what minting a will-call square asserts.
type dispatchPickStateAdapter struct{ repo *tasks.Repository }

func (a dispatchPickStateAdapter) OrderPickState(ctx context.Context, orderID string) (dispatch.PickCompletion, error) {
	status, err := a.repo.OrderPickStatus(ctx, orderID)
	if err != nil {
		return dispatch.PickCompletion{}, err
	}
	if status == "" {
		return dispatch.PickCompletion{}, nil
	}
	return dispatch.PickCompletion{
		Exists: true,
		Staged: status == tasks.StatusStaged || status == tasks.StatusDone,
		Status: status,
	}, nil
}

func (a dispatchPickStateAdapter) OrderPickStates(ctx context.Context, orderIDs []string) (map[string]dispatch.PickCompletion, error) {
	statuses, err := a.repo.OrderPickStatuses(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]dispatch.PickCompletion, len(statuses))
	for id, status := range statuses {
		out[id] = dispatch.PickCompletion{
			Exists: true,
			Staged: status == tasks.StatusStaged || status == tasks.StatusDone,
			Status: status,
		}
	}
	return out, nil
}

// dispatchOrderValueAdapter satisfies dispatch.OrderValueReader: the load
// board's two figures, read on the server, never accepted from a request.
type dispatchOrderValueAdapter struct{ pool *pgxpool.Pool }

func (a dispatchOrderValueAdapter) OrderValueCents(ctx context.Context, orderID string) (int64, bool, error) {
	return a.readCents(ctx, orderID, "total_cents")
}

func (a dispatchOrderValueAdapter) OrderFreightCents(ctx context.Context, orderID string) (int64, bool, error) {
	return a.readCents(ctx, orderID, "freight_cents")
}

// OrderQuotedFreightCents reads the frozen baseline (0194) the
// second-truck gate measures against. NULL is "never came from a quote"
// and any value, ZERO INCLUDED, is a real baseline; it deliberately does
// not go through readCents's `cents <= 0` rule.
func (a dispatchOrderValueAdapter) OrderQuotedFreightCents(ctx context.Context, orderID string) (int64, bool, error) {
	if a.pool == nil {
		return 0, false, nil
	}
	var cents *int64
	err := a.pool.QueryRow(ctx, `SELECT quoted_freight_cents FROM orders WHERE id = $1`, orderID).Scan(&cents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if cents == nil {
		return 0, false, nil
	}
	return *cents, true, nil
}

// readCents answers (cents, known, err); known=false with a nil error is
// "there is no figure", which the load board renders as a dash, never as
// zero.
func (a dispatchOrderValueAdapter) readCents(ctx context.Context, orderID, col string) (int64, bool, error) {
	if a.pool == nil {
		return 0, false, nil
	}
	// col is one of two package-local literals, never caller input.
	var cents int64
	err := a.pool.QueryRow(ctx, `SELECT `+col+` FROM orders WHERE id = $1`, orderID).Scan(&cents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if cents <= 0 {
		return 0, false, nil
	}
	return cents, true, nil
}

// dispatchOrderFreightAdapter satisfies dispatch.OrderFreightWriter, the
// one write that moves an order's own freight figure when a raise is
// applied. It takes the TRANSACTION rather than the pool because the
// approve path runs inside the rail's decide transaction, so the money
// and the APPROVED stamp are atomic.
type dispatchOrderFreightAdapter struct{ pool *pgxpool.Pool }

func (a dispatchOrderFreightAdapter) SetOrderFreightTx(ctx context.Context, tx pgx.Tx, orderID string, cents int64) error {
	if tx == nil {
		return errors.New("dispatch freight: no transaction")
	}
	_, err := tx.Exec(ctx, `UPDATE orders SET freight_cents = $2 WHERE id = $1`, orderID, cents)
	return err
}

// dispatchFreightApprovalAdapter satisfies dispatch.FreightApprovalFiler
// over the rail: dispatch parks a raise, this files the request that
// decides it on the ONE approvals queue. The seam carries no amount, so
// no figure from a customer's delivery charge can reach the queue.
type dispatchFreightApprovalAdapter struct{ svc *identity.Approvals }

func (a dispatchFreightApprovalAdapter) FileFreightApproval(ctx context.Context, raiseID, branchID, requestedBy, detail string) (string, error) {
	req, err := a.svc.FileFreightApproval(ctx, raiseID, branchID, requestedBy, detail)
	if err != nil {
		return "", err
	}
	return req.ID, nil
}

// dispatchFreightDeciderAdapter satisfies the rail's FreightDecider over
// dispatch.Service, the return leg: an approve applies the parked split,
// a refusal marks it refused, both inside the decision's transaction.
type dispatchFreightDeciderAdapter struct{ svc *dispatch.Service }

func (a dispatchFreightDeciderAdapter) ApplyFreightRaiseTx(ctx context.Context, tx pgx.Tx, raiseID, deciderID string) error {
	return asIdentityError(a.svc.ApplyFreightRaiseTx(ctx, tx, raiseID, deciderID))
}

func (a dispatchFreightDeciderAdapter) RefuseFreightRaiseTx(ctx context.Context, tx pgx.Tx, raiseID, deciderID string) error {
	return asIdentityError(a.svc.RefuseFreightRaiseTx(ctx, tx, raiseID, deciderID))
}

// asIdentityError re-grades a dispatch refusal into the rail's taxonomy
// so the decide handler answers the real status; anything unrecognized
// passes through to the 500 arm.
func asIdentityError(err error) error {
	if err == nil {
		return nil
	}
	if status, msg, ok := dispatch.ErrorHTTPStatus(err); ok {
		return identity.NewClientError(status, msg)
	}
	return err
}

// dispatchStockAdapter satisfies dispatch.StockAvailabilityReader over
// the inventory repository's per-branch on-hand read; a product nobody
// stocks answers 0 with no error.
type dispatchStockAdapter struct{ repo *inventory.Repository }

func (a dispatchStockAdapter) OnHandBase(ctx context.Context, productID, branchID string) (float64, error) {
	if a.repo == nil {
		return 0, nil
	}
	qty, err := a.repo.OnHandQtyBranch(ctx, nil, productID, branchID)
	if err != nil {
		return 0, err
	}
	return qty, nil
}

// dispatchPickSource satisfies tasks.PickSource over dispatch.PickFeed.
// This type is the entire coupling between the two modules.
type dispatchPickSource struct {
	feed *dispatch.PickFeed
	hold tasks.PickHoldCheck
}

func newDispatchPickSource(pool *pgxpool.Pool, orderRepo *order.Repository, log *slog.Logger) dispatchPickSource {
	return dispatchPickSource{
		feed: dispatch.NewPickFeed(dispatch.NewRepository(pool)).
			WithOrderLines(orderPickLineAdapter{repo: orderRepo}).
			WithLogger(log),
		// The pay before release mark rides billing's one hold read (item 9
		// F review N1): dispatch holds no invoice SQL, so the feed's mark
		// cannot be a second copy of the owed arithmetic.
		hold: tasksPickHoldAdapter{repo: holdBillingRepo(pool)},
	}
}

// PickTicket maps dispatch.ErrNotFound onto tasks.ErrUnknownOrder, the
// seam contract's one required error translation.
func (a dispatchPickSource) PickTicket(ctx context.Context, orderID string) (tasks.PickTicket, error) {
	t, err := a.feed.TicketForOrder(ctx, orderID)
	if err != nil {
		if errors.Is(err, dispatch.ErrNotFound) {
			return tasks.PickTicket{}, tasks.ErrUnknownOrder
		}
		return tasks.PickTicket{}, err
	}
	return a.markHeld(ctx, pickTicketFromFeed(t))
}

// PickTicketInOrg is PickTicket held to the caller's org (UX-81 round 4): a foreign order's ticket is the same
// tasks.ErrUnknownOrder as an unknown one.
func (a dispatchPickSource) PickTicketInOrg(ctx context.Context, orgID, orderID string) (tasks.PickTicket, error) {
	t, err := a.feed.TicketForOrderInOrg(ctx, orgID, orderID)
	if err != nil {
		if errors.Is(err, dispatch.ErrNotFound) {
			return tasks.PickTicket{}, tasks.ErrUnknownOrder
		}
		return tasks.PickTicket{}, err
	}
	return a.markHeld(ctx, pickTicketFromFeed(t))
}

// markHeld stamps the pay before release hold's mark on a pickup ticket from
// billing's one hold read. Only a pickup or will call order can be held (a
// delivery keeps dispatch's own gate), so a delivery ticket costs no read.
// An unreadable hold fails the read rather than listing a held order clear.
func (a dispatchPickSource) markHeld(ctx context.Context, t tasks.PickTicket) (tasks.PickTicket, error) {
	if a.hold == nil || (t.Fulfillment != "PICKUP" && t.Fulfillment != "WILL_CALL") {
		return t, nil
	}
	held, owed, err := a.hold.OrderHeldFromPicking(ctx, t.OrderID)
	if err != nil {
		return tasks.PickTicket{}, err
	}
	if held {
		t.HeldFromPicking = true
		t.HoldWords = "Balance due"
		t.StillOwedCents = owed
	}
	return t, nil
}

func (a dispatchPickSource) PendingTickets(ctx context.Context, branchID string, limit, offset int) ([]tasks.PickTicket, int, error) {
	feed, total, err := a.feed.PendingTickets(ctx, branchID, limit, offset)
	return a.pendingTickets(ctx, feed, total, err)
}

// PendingTicketsInOrg is PendingTickets held to the caller's org (UX-81 round 5): the branch keyed feed lists
// nothing of another org's customers, orders or lines.
func (a dispatchPickSource) PendingTicketsInOrg(ctx context.Context, orgID, branchID string, limit, offset int) ([]tasks.PickTicket, int, error) {
	feed, total, err := a.feed.PendingTicketsInOrg(ctx, orgID, branchID, limit, offset)
	if err != nil && errors.Is(err, dispatch.ErrNotFound) {
		return nil, 0, tasks.ErrUnknownOrder
	}
	return a.pendingTickets(ctx, feed, total, err)
}

func (a dispatchPickSource) pendingTickets(ctx context.Context, feed []dispatch.PickFeedTicket, total int, err error) ([]tasks.PickTicket, int, error) {
	if err != nil {
		return nil, 0, err
	}
	out := make([]tasks.PickTicket, 0, len(feed))
	for _, t := range feed {
		marked, err := a.markHeld(ctx, pickTicketFromFeed(t))
		if err != nil {
			return nil, 0, err
		}
		out = append(out, marked)
	}
	return out, total, nil
}

// pickTicketFromFeed is the field copy. Source is "DISPATCH" and that
// string is load-bearing: the yard console renders its fixture warning
// off exactly this value.
func pickTicketFromFeed(t dispatch.PickFeedTicket) tasks.PickTicket {
	lines := make([]tasks.PickLine, 0, len(t.Lines))
	for _, l := range t.Lines {
		lines = append(lines, tasks.PickLine{
			LineNo:         l.LineNo,
			ProductID:      l.ProductID,
			SKU:            l.SKU,
			Description:    l.Description,
			QtyOrdered:     l.QtyOrdered,
			QtyBackordered: l.QtyBackordered,
			UOM:            l.UOM,
			Location:       l.Location,
		})
	}
	return tasks.PickTicket{
		OrderID:      t.OrderID,
		OrderNo:      t.OrderNo,
		BranchID:     t.BranchID,
		CustomerName: t.CustomerName,
		Fulfillment:  t.Fulfillment,
		MTOClass:     t.MTOClass,
		Priority:     t.Priority,
		Reference:    t.Reference,
		RequestedAt:  t.RequestedAt,
		Lines:        lines,
		Source:       "DISPATCH",
		RunNo:        t.RunNo,
		RunDepartsAt: t.RunDepartsAt,
		LoadPosition: t.LoadPosition,
	}
}

// orderLineWeightAdapter satisfies tasks.OrderLineWriter (DIB-324):
// SetLineQty goes through order.Service.EditLineQty, the module's own
// status-guarded, repriced, audited edit, never a repository write.
type orderLineWeightAdapter struct {
	svc  *order.Service
	repo *order.Repository
}

func (a orderLineWeightAdapter) LineUOM(ctx context.Context, orderID string, lineNo int) (string, error) {
	_, uom, _, _, err := a.repo.LineForEdit(ctx, orderID, lineNo)
	return uom, err
}

func (a orderLineWeightAdapter) SetLineQty(ctx context.Context, orderID string, lineNo int, qtyEntered string) error {
	_, err := a.svc.EditLineQty(ctx, orderID, lineNo, qtyEntered)
	return err
}

// taskOrderAdvanceAdapter satisfies tasks.OrderAdvancer (DIB-325) over
// the same state-guarded event writer dispatch's handoff uses.
type taskOrderAdvanceAdapter struct{ svc *order.Service }

func (a taskOrderAdvanceAdapter) AdvanceOrder(ctx context.Context, orderID, targetStatus, reason string) error {
	return a.svc.AdvanceFromEvent(ctx, orderID, targetStatus, reason)
}

// invCountAdapter satisfies invcount.StockCounter over inventory's count
// seam (the type translation between the two modules' count shapes).
type invCountAdapter struct{ svc *inventory.Service }

func (a invCountAdapter) SnapshotOnHand(ctx context.Context, branchID string) ([]invcount.Snapshot, error) {
	return a.SnapshotOnHandTx(ctx, nil, branchID)
}

func (a invCountAdapter) SnapshotOnHandTx(ctx context.Context, tx pgx.Tx, branchID string) ([]invcount.Snapshot, error) {
	snaps, err := a.svc.SnapshotOnHandTx(ctx, tx, branchID)
	if err != nil {
		return nil, err
	}
	out := make([]invcount.Snapshot, len(snaps))
	for i, s := range snaps {
		out[i] = invcount.Snapshot{ProductID: s.ProductID, LocationID: s.LocationID, OnHandBase: s.OnHandBase, AvgCostCents: s.AvgCostCents}
	}
	return out, nil
}

func (a invCountAdapter) WeightedAvgCostCents(ctx context.Context, productID, branchID string) (int64, error) {
	return a.WeightedAvgCostCentsTx(ctx, nil, productID, branchID)
}

func (a invCountAdapter) WeightedAvgCostCentsTx(ctx context.Context, tx pgx.Tx, productID, branchID string) (int64, error) {
	return a.svc.WeightedAvgCostCentsTx(ctx, tx, productID, branchID)
}

func (a invCountAdapter) PostCountAdjustmentTx(ctx context.Context, tx pgx.Tx, productID, locationID, branchID string, qtyDelta float64, unitCostCents int64, reason string) (string, error) {
	return a.svc.PostCountAdjustmentTx(ctx, tx, productID, locationID, branchID, qtyDelta, unitCostCents, reason)
}

// landedAdapter satisfies landedcost.Capitalizer over inventory's
// landed-cost seam (the landed-seam types into landedcost types).
type landedAdapter struct{ svc *inventory.Service }

func (a landedAdapter) ReceivedLinesForPO(ctx context.Context, poID string) ([]landedcost.ReceivedLine, error) {
	lines, err := a.svc.ReceivedLinesForPO(ctx, poID)
	if err != nil {
		return nil, err
	}
	out := make([]landedcost.ReceivedLine, len(lines))
	for i, l := range lines {
		out[i] = landedcost.ReceivedLine{POLineID: l.POLineID, ProductID: l.ProductID, QtyBase: l.QtyBase, ValueCents: l.ValueCents, WeightKg: l.WeightKg, IsSpecialBuy: l.IsSpecialBuy}
	}
	return out, nil
}

func (a landedAdapter) CapitalizeLandedCostTx(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64, at time.Time, sourceRefID string) (int64, error) {
	return a.svc.CapitalizeLandedCostTx(ctx, tx, productID, branchID, allocatedCadCents, at, sourceRefID)
}

func (a landedAdapter) CapitalizeLandedCostSnapTx(ctx context.Context, tx pgx.Tx, productID, branchID string, allocatedCadCents int64, at time.Time, sourceRefID string) (int64, int64, bool, error) {
	return a.svc.CapitalizeLandedCostSnapTx(ctx, tx, productID, branchID, allocatedCadCents, at, sourceRefID)
}

func (a landedAdapter) RestoreLandedCostTx(ctx context.Context, tx pgx.Tx, productID, branchID string, snapAvg, absorbed int64, at time.Time, sourceRefID string) (int64, error) {
	return a.svc.RestoreLandedCostTx(ctx, tx, productID, branchID, snapAvg, absorbed, at, sourceRefID)
}

// w4cStockReceiver satisfies purchasing.StockReceiver over
// inventory.Service.ReceivePOTx: purchasing writes the RECEIVING
// DOCUMENT, inventory writes the receipt moves.
type w4cStockReceiver struct{ svc *inventory.Service }

func (a w4cStockReceiver) ReceiveDocumentTx(ctx context.Context, tx pgx.Tx, receivingDocID string) error {
	return a.svc.ReceivePOTx(ctx, tx, receivingDocID)
}

// w4cVendorDirectory satisfies purchasing.VendorDirectory with a
// read-only projection over the vendors table, satisfied here because no
// package owns that table.
type w4cVendorDirectory struct{ pool *pgxpool.Pool }

const w4cVendorCols = `id::text, vendor_no::text, name, payment_terms_days, is_active`

func (a w4cVendorDirectory) ListVendors(ctx context.Context) ([]purchasing.Vendor, error) {
	if a.pool == nil {
		return nil, nil
	}
	rows, err := a.pool.Query(ctx, `SELECT `+w4cVendorCols+` FROM vendors ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("vendor directory: list: %w", err)
	}
	defer rows.Close()
	var out []purchasing.Vendor
	for rows.Next() {
		var v purchasing.Vendor
		if err := rows.Scan(&v.ID, &v.VendorNo, &v.Name, &v.PaymentTermsDays, &v.IsActive); err != nil {
			return nil, fmt.Errorf("vendor directory: scan: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (a w4cVendorDirectory) Vendor(ctx context.Context, vendorID string) (purchasing.Vendor, error) {
	if a.pool == nil {
		return purchasing.Vendor{}, fmt.Errorf("vendor directory: no database")
	}
	var v purchasing.Vendor
	err := a.pool.QueryRow(ctx, `SELECT `+w4cVendorCols+` FROM vendors WHERE id = $1`, vendorID).
		Scan(&v.ID, &v.VendorNo, &v.Name, &v.PaymentTermsDays, &v.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return purchasing.Vendor{}, purchasing.ErrUnknownVendor
	}
	if err != nil {
		return purchasing.Vendor{}, fmt.Errorf("vendor directory: get %s: %w", vendorID, err)
	}
	return v, nil
}

// w4cWarrantyCreditReader satisfies purchasing.WarrantyCreditReader over warranty_claims,
// warranty_recovery_balances and warranty_claim_lines.
type w4cWarrantyCreditReader struct{ pool *pgxpool.Pool }

func (w w4cWarrantyCreditReader) OpenCreditForVendor(ctx context.Context, vendorID, branchID string) (*purchasing.OpenWarrantyCredit, error) {
	if w.pool == nil {
		return nil, nil
	}
	var credit purchasing.OpenWarrantyCredit
	var lineDesc *string
	err := w.pool.QueryRow(ctx, `
		SELECT b.claim_id::text, b.claim_no::text, b.outstanding_cents,
		       COALESCE(c.description, ''),
		       v.id::text, v.name,
		       (SELECT COALESCE(NULLIF(product_text, ''), '') FROM warranty_claim_lines WHERE claim_id = b.claim_id LIMIT 1)
		  FROM warranty_recovery_balances b
		  JOIN warranty_claims c ON c.id = b.claim_id
		  LEFT JOIN vendors v ON v.id = b.vendor_id
		 WHERE b.vendor_id = $1::uuid
		   AND ($2 = '' OR b.branch_id = $2::uuid)
		   AND b.outstanding_cents > 0
		 ORDER BY b.accrued_at DESC
		 LIMIT 1`, vendorID, branchID).Scan(
		&credit.ClaimID, &credit.ClaimNo, &credit.AmountCents,
		&credit.Description, &credit.VendorID, &credit.VendorName, &lineDesc)
	if err == nil {
		if lineDesc != nil && *lineDesc != "" {
			if credit.Description != "" {
				s := fmt.Sprintf("%s (%s)", credit.Description, *lineDesc)
				credit.Summary = &s
			} else {
				credit.Description = *lineDesc
			}
		}
		return &credit, nil
	}

	err = w.pool.QueryRow(ctx, `
		SELECT c.id::text, c.claim_no::text,
		       COALESCE(NULLIF(c.realized_cents, 0), c.expected_cents),
		       c.description,
		       v.id::text, v.name,
		       (SELECT COALESCE(NULLIF(product_text, ''), '') FROM warranty_claim_lines WHERE claim_id = c.id LIMIT 1)
		  FROM warranty_claims c
		  LEFT JOIN vendors v ON v.id = c.vendor_id
		 WHERE c.vendor_id = $1::uuid
		   AND ($2 = '' OR c.branch_id = $2::uuid)
		   AND (c.outcome = 'VENDOR_CREDIT' OR c.phase IN ('SETTLEMENT', 'CLOSED'))
		   AND COALESCE(NULLIF(c.realized_cents, 0), c.expected_cents) > 0
		 ORDER BY c.opened_at DESC
		 LIMIT 1`, vendorID, branchID).Scan(
		&credit.ClaimID, &credit.ClaimNo, &credit.AmountCents,
		&credit.Description, &credit.VendorID, &credit.VendorName, &lineDesc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("warranty credit reader: open credit: %w", err)
	}
	if lineDesc != nil && *lineDesc != "" {
		if credit.Description != "" {
			s := fmt.Sprintf("%s (%s)", credit.Description, *lineDesc)
			credit.Summary = &s
		} else {
			credit.Description = *lineDesc
		}
	}
	return &credit, nil
}

func (w w4cWarrantyCreditReader) ClaimInfo(ctx context.Context, claimID string) (*purchasing.OpenWarrantyCredit, error) {
	if w.pool == nil {
		return nil, nil
	}
	var credit purchasing.OpenWarrantyCredit
	var lineDesc *string
	err := w.pool.QueryRow(ctx, `
		SELECT c.id::text, c.claim_no::text,
		       COALESCE(NULLIF(c.realized_cents, 0), c.expected_cents),
		       c.description,
		       v.id::text, v.name,
		       (SELECT COALESCE(NULLIF(product_text, ''), '') FROM warranty_claim_lines WHERE claim_id = c.id LIMIT 1)
		  FROM warranty_claims c
		  LEFT JOIN vendors v ON v.id = c.vendor_id
		 WHERE c.id = $1::uuid`, claimID).Scan(
		&credit.ClaimID, &credit.ClaimNo, &credit.AmountCents,
		&credit.Description, &credit.VendorID, &credit.VendorName, &lineDesc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("warranty credit reader: claim info %s: %w", claimID, err)
	}
	if lineDesc != nil && *lineDesc != "" {
		if credit.Description != "" {
			s := fmt.Sprintf("%s (%s)", credit.Description, *lineDesc)
			credit.Summary = &s
		} else {
			credit.Description = *lineDesc
		}
	}
	return &credit, nil
}

// item5BranchDirectory satisfies purchasing.BranchDirectory over
// locations + system_settings, so the standardized PO document carries a
// ship-to and the dealer's own name without purchasing learning either
// table's shape.
type item5BranchDirectory struct{ pool *pgxpool.Pool }

func (a item5BranchDirectory) Branch(ctx context.Context, branchID string) (purchasing.BranchBrief, error) {
	if a.pool == nil {
		return purchasing.BranchBrief{}, fmt.Errorf("branch directory: no database")
	}
	var b purchasing.BranchBrief
	var line1, city, province, postal string
	err := a.pool.QueryRow(ctx, `
		SELECT l.name, l.code::text,
		       COALESCE(l.address->>'line1',''), COALESCE(l.address->>'city',''),
		       COALESCE(l.address->>'province',''), COALESCE(l.address->>'postal',''),
		       COALESCE((SELECT value #>> '{}' FROM system_settings WHERE key = 'org_name'), ''),
		       COALESCE((SELECT value #>> '{}' FROM system_settings WHERE key = 'org_phone'), ''),
		       COALESCE((SELECT value #>> '{}' FROM system_settings WHERE key = 'org_email'), '')
		  FROM locations l
		 WHERE l.id = $1`, branchID).
		Scan(&b.Name, &b.Code, &line1, &city, &province, &postal, &b.OrgName, &b.Phone, &b.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return purchasing.BranchBrief{}, fmt.Errorf("branch directory: unknown branch %s", branchID)
	}
	if err != nil {
		return purchasing.BranchBrief{}, fmt.Errorf("branch directory: get %s: %w", branchID, err)
	}
	parts := make([]string, 0, 4)
	for _, p := range []string{line1, city, province, postal} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	b.Address = strings.Join(parts, ", ")
	return b, nil
}

// item5VendorAddressBook satisfies purchasing.VendorAddressBook over the
// two free-form JSONB blobs the vendors row carries: deliberately narrow,
// deliberately empty-by-default, and complete rather than choosing.
type item5VendorAddressBook struct{ pool *pgxpool.Pool }

func (a item5VendorAddressBook) VendorOrderCandidates(ctx context.Context, vendorID string) ([]string, error) {
	if a.pool == nil {
		return nil, fmt.Errorf("vendor address book: no database")
	}
	var remit, meta string
	err := a.pool.QueryRow(ctx, `
		SELECT COALESCE(remit_address->>'email',''), COALESCE(metadata->>'email','')
		  FROM vendors WHERE id = $1`, vendorID).Scan(&remit, &meta)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("vendor address book: get %s: %w", vendorID, err)
	}
	out := make([]string, 0, 2)
	for _, c := range []string{remit, meta} {
		if strings.TrimSpace(c) != "" {
			out = append(out, strings.TrimSpace(c))
		}
	}
	return out, nil
}

// w4cLandedCoster satisfies purchasing.LandedCoster over landedcost's
// existing Create + Apply path, unmodified.
type w4cLandedCoster struct{ svc *landedcost.Service }

func (a w4cLandedCoster) CreateAndApply(ctx context.Context, in purchasing.LandedCostDraft) (string, error) {
	id, err := a.svc.Create(ctx, landedcost.CreateInput{
		POID:            in.POID,
		PORef:           in.PORef,
		BranchID:        in.BranchID,
		Currency:        in.Currency,
		FXRate:          in.FXRate,
		FreightCents:    in.FreightCents,
		DutyCents:       in.DutyCents,
		HandlingCents:   in.HandlingCents,
		AllocationBasis: in.Basis,
	})
	if err != nil {
		return "", err
	}
	if err := a.svc.Apply(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

func (a w4cLandedCoster) OpenPayablesAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]purchasing.LandedCostPayable, error) {
	if a.svc == nil {
		return nil, nil
	}
	rows, err := a.svc.OpenPayablesAsOf(ctx, asOf, branchIDs, global)
	if err != nil {
		return nil, err
	}
	out := make([]purchasing.LandedCostPayable, len(rows))
	for i, r := range rows {
		out[i] = purchasing.LandedCostPayable{
			ID:               r.ID,
			POID:             r.POID,
			PORef:            r.PORef,
			BranchID:         r.BranchID,
			Currency:         r.Currency,
			FreightCadCents:  r.FreightCadCents,
			DutyCadCents:     r.DutyCadCents,
			HandlingCadCents: r.HandlingCadCents,
			TotalCadCents:    r.TotalCadCents,
			ChargeKind:       r.ChargeKind,
			PayeeLabel:       r.PayeeLabel,
			CreatedAt:        r.CreatedAt,
			UpdatedAt:        r.UpdatedAt,
		}
	}
	return out, nil
}

// w4cSupplierPaymentReader satisfies purchasing.SupplierPaymentReader over
// posted GL journals debiting account 2100 from external payment postings.
type w4cSupplierPaymentReader struct{ pool *pgxpool.Pool }

func (a w4cSupplierPaymentReader) ExternalSupplierPaymentsAsOf(ctx context.Context, asOf time.Time, branchIDs []string, global bool) ([]purchasing.SupplierPayment, error) {
	if a.pool == nil {
		return nil, nil
	}
	if !global && len(branchIDs) == 0 {
		return []purchasing.SupplierPayment{}, nil
	}
	if branchIDs == nil {
		branchIDs = []string{}
	}
	rows, err := a.pool.Query(ctx, `
		SELECT j.id::text, COALESCE(j.source_ref_id::text, ''), COALESCE(j.idempotency_key, ''),
		       COALESCE(j.branch_id::text, ''), COALESCE(j.memo, ''),
		       COALESCE(jl.debit_cents, 0) - COALESCE(jl.credit_cents, 0), j.posted_at
		  FROM gl_journal_lines jl
		  JOIN gl_journals j ON j.id = jl.journal_id
		  JOIN gl_accounts ga ON ga.id = jl.account_id
		 WHERE ga.code = '2100'
		   AND j.status = 'POSTED'
		   AND j.source IN ('PAYMENT', 'MANUAL')
		   AND COALESCE(j.idempotency_key, '') NOT LIKE 'purchasing.bill.payment:%'
		   AND (COALESCE(jl.debit_cents, 0) - COALESCE(jl.credit_cents, 0)) > 0
		   AND j.posted_at <= $1
		   AND ($2::boolean OR j.branch_id = ANY($3::uuid[]))
		 ORDER BY j.posted_at ASC, j.id ASC, jl.line_no ASC`,
		asOf, global, branchIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("supplier payment reader: %w", err)
	}
	defer rows.Close()
	out := make([]purchasing.SupplierPayment, 0)
	for rows.Next() {
		var sp purchasing.SupplierPayment
		if err := rows.Scan(
			&sp.JournalID, &sp.SourceRefID, &sp.IdempotencyKey,
			&sp.BranchID, &sp.Memo, &sp.AmountCents, &sp.PostedAt,
		); err != nil {
			return nil, fmt.Errorf("supplier payment reader: scan: %w", err)
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// w4cGLPoster satisfies purchasing.GLPoster over glSvc.PostExternal (the
// vendor-bill journal).
type w4cGLPoster struct{ svc *gl.Service }

func (a w4cGLPoster) PostJournal(ctx context.Context, in purchasing.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines, DocumentDate: in.DocumentDate,
	})
}

// PostJournalTx is PostJournal through the caller's transaction (gl.Service.PostExternalTx).
func (a w4cGLPoster) PostJournalTx(ctx context.Context, tx pgx.Tx, in purchasing.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternalTx(ctx, tx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines, DocumentDate: in.DocumentDate,
	})
}

// unionPOCreator adapts inventory's POCreator seam onto purchasing's
// CreatePO; the caller's context carries the staff identity, so
// purchasing's own branch fence applies.
type unionPOCreator struct{ svc *purchasing.Service }

func (u unionPOCreator) CreateDraftPO(ctx context.Context, in inventory.SuggestedPO) (string, string, error) {
	lines := make([]purchasing.AddLineParams, 0, len(in.Lines))
	for _, l := range in.Lines {
		lines = append(lines, purchasing.AddLineParams{
			ProductID:      l.ProductID,
			QtyOrderedBase: l.QtyBase,
			UnitCostCents:  l.UnitCostCents,
		})
	}
	po, err := u.svc.CreatePO(ctx, purchasing.CreatePORequest{
		VendorID: in.VendorID,
		BranchID: in.BranchID,
		Notes:    in.Memo,
		Lines:    lines,
	})
	if err != nil {
		return "", "", err
	}
	return po.ID, po.PONo, nil
}

// inv470OnOrder satisfies inventory.OnOrderReader over purchasing's
// outstanding-on-order aggregate, the same read the on-order panel is
// built from.
type inv470OnOrder struct{ svc *purchasing.Service }

func (a inv470OnOrder) OutstandingOnOrderBase(ctx context.Context, branchID string, productIDs []string) ([]inventory.OnOrderQty, error) {
	rows, err := a.svc.OutstandingOnOrderBase(ctx, branchID, productIDs)
	if err != nil {
		return nil, err
	}
	out := make([]inventory.OnOrderQty, len(rows))
	for i, r := range rows {
		out[i] = inventory.OnOrderQty{ProductID: r.ProductID, BranchID: r.BranchID, QtyBase: r.QtyBase}
	}
	return out, nil
}

// invDraftPOs satisfies inventory.DraftPOReader over purchasing's open draft read: the draft a
// worklist row points at ("drafted, not placed"). A draft is not on order, so it is not in the
// aggregate above; it rides beside it as a pointer.
type invDraftPOs struct{ svc *purchasing.Service }

func (a invDraftPOs) OpenDraftsFor(ctx context.Context, branchID string, productIDs []string) ([]inventory.DraftPO, error) {
	rows, err := a.svc.OpenDraftsFor(ctx, branchID, productIDs)
	if err != nil {
		return nil, err
	}
	out := make([]inventory.DraftPO, len(rows))
	for i, r := range rows {
		out[i] = inventory.DraftPO{ProductID: r.ProductID, BranchID: r.BranchID, POID: r.POID, PONo: r.PONo}
	}
	return out, nil
}

// invShortOrders satisfies inventory.ShortOrderReader over the order module's open-order numbers
// read: the sales orders a shortfall row names, oldest first. Numbers and ids only cross the seam;
// the orders tables stay this module's alone.
// invBranchNames satisfies inventory.BranchNameReader over the branches module's name read: the
// stock lookup's out-at line. Ids cross the seam; the branch table stays this module's alone.
type invBranchNames struct{ svc *branches.Service }

func (a invBranchNames) BranchNames(ctx context.Context, ids []string) (map[string]string, error) {
	return a.svc.BranchNames(ctx, ids)
}

type invShortOrders struct{ svc *order.Service }

func (a invShortOrders) OpenOrdersFor(ctx context.Context, branchID string, productIDs []string) ([]inventory.ShortOrder, error) {
	rows, err := a.svc.OpenSaleOrdersFor(ctx, branchID, productIDs)
	if err != nil {
		return nil, err
	}
	out := make([]inventory.ShortOrder, len(rows))
	for i, r := range rows {
		out[i] = inventory.ShortOrder{ProductID: r.ProductID, BranchID: r.BranchID, OrderID: r.OrderID, OrderNo: r.OrderNo}
	}
	return out, nil
}

// inv461Nudger satisfies inventory.ProcurementNudger over messaging's
// internal post path, refusing any tier but the NOTABLE one the module
// asked for.
type inv461Nudger struct{ svc *messaging.Service }

func (n inv461Nudger) PostProcurementNudge(ctx context.Context, post inventory.NudgePost) (bool, error) {
	if post.Class != inventory.NudgeClassNotable {
		return false, fmt.Errorf("procurement nudge: refusing to post at tier %q (NOTABLE only)", post.Class)
	}
	_, inserted, err := n.svc.PostEventToChannelSlug(ctx, post.ChannelSlug, post.EventKey, post.Body,
		messaging.EventRef{Topic: post.Topic, Entity: post.Entity, Summary: post.Summary},
		false)
	return inserted, err
}

// unionSLANotifier satisfies warranty's SLA-breach notifier over
// messaging's unmutable ESCALATION tier: the ALERT row is the system of
// record, the escalation post is visibility.
type unionSLANotifier struct {
	svc *messaging.Service
	log *slog.Logger
}

func (u unionSLANotifier) NotifySLABreach(ctx context.Context, in warranty.SLABreachEvent) error {
	body := "SLA breached: claim " + in.ClaimNo + ", phase " + in.Phase + ", due " + in.DueAt
	if in.VendorName != "" {
		body += ", " + in.VendorName
	}
	_, _, err := u.svc.PostEventToChannelSlug(ctx, "escalations", "warranty-sla:"+in.AlertID, body,
		messaging.EventRef{Topic: in.Topic, Entity: "warranty_claim:" + in.ClaimID, Summary: body}, false)
	return err
}

// cardAttemptEscalations is the card attempt sweep's alerter over messaging's finance
// channel (a money room fenced by the payment trail: the body names an amount and a sale key), the unionSLANotifier precedent (the card charge design section 4): it is what
// payment.AlertFunc is bound to, once at process start, and its signature is the seam's. The
// event key arrives already shaped card-attempt:<id>:<status>, so one state of one attempt
// posts once; the body is the sweep's money facts, never a token or a customer name.
type cardAttemptEscalations struct{ svc slugPoster }

// slugPoster is the one messaging post the card attempt alert needs; *messaging.Service satisfies it.
type slugPoster interface {
	PostEventToChannelSlug(ctx context.Context, slug, eventKey, body string, ref messaging.EventRef, requiresAck bool) (messaging.Message, bool, error)
}

func (a cardAttemptEscalations) post(ctx context.Context, key, body string) error {
	_, _, err := a.svc.PostEventToChannelSlug(ctx, "finance", key, body,
		messaging.EventRef{Topic: "payment.card_attempt", Entity: key, Summary: body}, false)
	return err
}

// approvalNotifierAdapter satisfies the rail's ApprovalNotifier over
// messaging's per-person channel posts, best-effort: a messaging blip
// never fails a filing or a decision.
type approvalNotifierAdapter struct {
	svc *messaging.Service
	log *slog.Logger
}

func (a approvalNotifierAdapter) ApprovalRequested(ctx context.Context, n identity.ApprovalNotice) {
	ref := messaging.ApprovalRef{
		ApprovalID:  n.ApprovalID,
		PolicyType:  string(n.Policy),
		SubjectRef:  n.SubjectRef,
		BranchID:    n.BranchID,
		AmountCents: n.AmountCents,
		RequestedBy: n.RequestedBy,
		Detail:      n.Detail,
		DeepLink:    n.DeepLink,
		Status:      "PENDING",
	}
	if err := a.svc.PostApprovalToUserChannels(ctx, n.ApproverIDs, ref); err != nil {
		a.log.Warn("approvals: fan-out card post partial failure", "approval_id", n.ApprovalID, "err", err)
	}
}

func (a approvalNotifierAdapter) ApprovalDecided(ctx context.Context, n identity.ApprovalDecidedNotice) {
	if err := a.svc.PostApprovalDecision(ctx, n.ApprovalID, n.Status, n.DecidedBy, n.Note); err != nil {
		a.log.Warn("approvals: decision echo partial failure", "approval_id", n.ApprovalID, "err", err)
	}
	if n.RequestedBy != "" {
		a.svc.NotifyUser(n.RequestedBy, "approval.decided", map[string]string{
			"approval_id": n.ApprovalID,
			"status":      n.Status,
		})
	}
}

// messagingRoomChecker adapts messaging.Repository to attachments.RoomMembershipChecker.
type messagingRoomChecker struct {
	repo *messaging.Repository
}

func (c messagingRoomChecker) IsRoomMember(ctx context.Context, roomID, userID string) (bool, error) {
	return c.repo.IsMember(ctx, roomID, userID)
}

func (c messagingRoomChecker) RoomExists(ctx context.Context, roomID string) (bool, error) {
	return c.repo.ChannelExists(ctx, roomID)
}

// messagingAttachmentMirror adapts messaging.Service to attachments.RoomAttachmentMirror.
type messagingAttachmentMirror struct {
	svc *messaging.Service
}

func (m messagingAttachmentMirror) MirrorAttachment(ctx context.Context, channelID string, att attachments.Attachment, role string) error {
	meta := messaging.AttachmentMeta{
		ID:             att.ID,
		AppwriteFileID: att.AppwriteFileID,
		Bucket:         att.Bucket,
		Kind:           att.Kind,
		Mime:           att.Mime,
	}
	if att.SizeBytes != nil {
		meta.SizeBytes = *att.SizeBytes
	}
	return m.svc.MirrorRoomAttachment(ctx, channelID, meta, att.CreatedBy, "", role)
}

// attachmentsSeamAdapter adapts attachments.Repository to messaging.AttachmentChecker.
type attachmentsSeamAdapter struct {
	repo *attachments.Repository
}

func (a attachmentsSeamAdapter) VerifyRoomAttachment(ctx context.Context, orgID, attachmentID, channelID string) (*messaging.AttachmentMeta, error) {
	m, err := a.repo.VerifyRoomAttachment(ctx, orgID, attachmentID, channelID)
	if err != nil {
		return nil, err
	}
	return &messaging.AttachmentMeta{
		ID:             m.ID,
		AppwriteFileID: m.AppwriteFileID,
		Bucket:         m.Bucket,
		Kind:           m.Kind,
		Mime:           m.Mime,
		SizeBytes:      m.SizeBytes,
	}, nil
}

func (a attachmentsSeamAdapter) ResolveAttachmentMetas(ctx context.Context, orgID string, attachmentIDs []string) (map[string]messaging.AttachmentMeta, error) {
	metas, err := a.repo.ResolveAttachmentMetas(ctx, orgID, attachmentIDs)
	if err != nil {
		return nil, err
	}
	res := make(map[string]messaging.AttachmentMeta, len(metas))
	for k, v := range metas {
		res[k] = messaging.AttachmentMeta{
			ID:             v.ID,
			AppwriteFileID: v.AppwriteFileID,
			Bucket:         v.Bucket,
			Kind:           v.Kind,
			Mime:           v.Mime,
			SizeBytes:      v.SizeBytes,
		}
	}
	return res, nil
}

// paymentRecipientReader answers messaging.PaymentRecipients over the identity module's own reach read
// (StaffAuthorityResolver.ReachOfUser), so messaging touches none of the identity tables and the branch
// reach is the guard's walk, not a second definition.
type paymentRecipientReader struct {
	reach *erpidentity.StaffAuthorityResolver
}

func (p paymentRecipientReader) PaymentRecipient(ctx context.Context, userID string) (messaging.PaymentRecipient, error) {
	r, err := p.reach.ReachOfUser(ctx, userID)
	if err != nil {
		return messaging.PaymentRecipient{}, err
	}
	return messaging.PaymentRecipient{PlatformUserID: r.PlatformUserID, OrgIDs: r.OrgIDs, Global: r.Global, BranchIDs: r.BranchIDs}, nil
}
