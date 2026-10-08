// wire_money.go is the money-spine wiring of HH-3.2 batch B4: the wire
// functions for gl, pricing, inventory, order, quote (plus product and
// settings, the minimal compile closure this batch carried with them).
// Each function constructs one module's service over the shared pool and
// registers its routes through the Guards, carrying the pin's composition
// root adapters for these modules verbatim where the adapter's own
// dependencies have landed, and leaving the not-yet-landed seams unwired
// with their designed fail-closed behavior (recorded per seam in the
// batch report). The B7 composition-root decomposition folds these
// functions into the full wire set; they exist now so the real modules
// register the routes the identity shims held.
package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// linkedUserID resolves the ERP users.id a platform user links to
// (users.platform_user_id, migration 0215): the B4 linkage ruling's id
// translation. It is never authority (tier, scope and role resolve from
// the app-role tables in the guard); an unlinked or deleted user answers
// not-ok and every id-consuming read fails closed rather than attributing
// the write to nobody.
func linkedUserID(pool *pgxpool.Pool) identity.UserIDLinker {
	return func(ctx context.Context, platformUserID string) (string, bool) {
		var id string
		err := pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE platform_user_id = $1 AND deleted_at IS NULL`,
			platformUserID).Scan(&id)
		if err != nil {
			return "", false
		}
		return id, true
	}
}

// wireMoney constructs and registers the money spine in dependency order
// (the pin's own staging: product before pricing, pricing before order and
// quote, gl before inventory's GL adapters). It registers every registrar
// the landed seam set supports; the seams whose owners land in later
// batches (billing, pos, till, customer) stay unwired and their surfaces
// answer the modules' designed fail-closed refusals.
func wireMoney(r httpx.Registrar, pool *pgxpool.Pool, g *guards.Guards, log *slog.Logger, staffBaseURL string, roleCfg *roleconfig.Config) *moneySpine {
	link := linkedUserID(pool)
	outboxStore := notify.NewPgOutboxStore(pool)
	auditSvc := audit.NewService(audit.NewRepository(pool), log)

	// The approvals rail (bridge batch): GL parks a manual journal through
	// the filer, and a rail-approve posts the journal through the decider
	// inside the decision transaction (GL-first ordering, the NewClientError
	// translation), exactly the two rail seams the pin bound here. The
	// notifier (messaging) and freight decider (dispatch) land in B5.
	approvals := identity.NewApprovals(identity.NewApprovalRepository(pool))

	// A-18 (W2): product catalogue, then A-07 (W3): pricing over it.
	productRepo := product.NewRepository(pool)
	productSvc := product.NewService(productRepo).WithUomReader(productRepo).WithCategoryReader(productRepo)
	productHandler := product.NewHandler(productSvc, log).WithUserLinker(link)
	productHandler.RegisterRoutes(r, g)
	productHandler.RegisterSearchRoute(r, g)
	productHandler.RegisterCounterRoutes(r, g)

	pricingSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(productSvc).
		WithUOMConverter(productSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())
	pricing.NewHandler(pricingSvc, log).WithUserLinker(link).RegisterRoutes(r, g)
	// SWARM-W5-C: the price-file import's three composition-root answers,
	// and the below-floor override authority over the rail plus the bridged
	// caller (the two-source order is the adapter's own, carried).
	pricingSvc.
		WithCostVisibility(pricingCostVisibility{}).
		WithVendorDirectory(pricingVendorDirectory{pool: pool}).
		WithCatalogSKUs(pricingCatalogSKUs{pool: pool}).
		WithOverrideAuthority(w5cOverrideAuthority{roles: roleCfg, log: log}).
		WithLogger(log)
	pricing.NewHandler(pricingSvc, log).WithUserLinker(link).RegisterW4Routes(r, g)

	// A-01 (W5): GL, with the rail filer bound. The invoice/tax/payment
	// readers (billing, pos, till) land in B6 and B7; unwired, the posting
	// legs answer their not-configured errors, never a silent skip.
	glSvc := gl.NewService(gl.NewRepository(pool)).
		WithDeadRequeuer(outboxStore).
		WithAudit(auditSvc).
		WithApprovalFiler(jeApprovalFilerAdapter{svc: approvals})
	gl.NewHandler(glSvc, log).WithUserLinker(link).RegisterRoutes(r, g)
	approvals.WithJEDecider(jeDeciderAdapter{svc: glSvc})

	// W6: inventory over GL (poster + rule resolver, both in-batch).
	inventorySvc := inventory.NewService(inventory.NewRepository(pool)).WithAudit(auditSvc)
	inventorySvc.WithGLPoster(invGLPoster{svc: glSvc}).WithRuleResolver(glRuleAdapter{svc: glSvc})
	productSvc.WithStock(productStockReader{svc: inventorySvc}).WithBranchStock(inventorySvc).WithListPrices(pricingSvc)
	// X3, the yard fence: the stock reads' cost legs (on hand, branch
	// stock, browse, the replenishment worklist) ride only for a caller
	// on the configuration's cost and margin list, the same rule the
	// quote and till reads answer through. Both route bundles carry the
	// rule, and the boot fail-fast keeps an unwired boot from handing
	// the yard team every branch's costs.
	inventoryHandler := inventory.NewHandler(inventorySvc, log).WithUserLinker(link).WithCostRoles(roleCfg)
	if !inventoryHandler.CostRolesWired() {
		panic("inventory: cost roles not wired - the stock reads would answer the cost legs to every caller")
	}
	inventoryHandler.RegisterRoutes(r, g)
	inventoryHandler.RegisterStockTruthRoutes(r, g)

	// The order lifecycle: base service, audit seams, reorder seams (pricing
	// and inventory are in-batch), actor names. The customer-tier seam
	// (customer module, B3-unlanded) and the cancel guards (billing, pos)
	// stay unwired: repricing edits and cancels answer their fail-closed
	// refusals, and the wiring batch that lands those modules closes them.
	orderRepo := order.NewRepository(pool)
	orderSvc := order.NewService(orderRepo).
		WithAudit(auditSvc).
		WithAuditTrail(auditSvc).
		WithReorderPricing(pricingSvc).
		WithReorderInventory(inventorySvc)
	orderHandler := order.NewHandler(orderSvc).WithUserLinker(link)
	orderHandler.RegisterRoutes(r, g)
	orderHandler.RegisterTransitionRoutes(r, g)
	orderHandler.RegisterEditRoutes(r, g)
	orderHandler.RegisterChangeRoutes(r, g)

	// The quote desk: the staff builder (audit, pricing, preview, cost basis,
	// actor names, shares, stock reserver, availability, soft-hold expiry;
	// the customer-tier seam lands with customer) and the public share
	// routes (token in path, no guard, R4).
	quoteRepo := quote.NewRepository(pool)
	// The quote floor's approvals rail: the margin decide authority from
	// the role configuration (D11, CP0-1 answer B), the floor list's dealer
	// wide approvers plus the branch scoped quote floor approvers, mapped
	// onto the ERP role codes the rail speaks. The platform admin rides
	// beside them (outside the configuration's thirteen role vocabulary).
	quoteFloorApprovers := identity.FloorApprovers{
		DealerWide: []string{identity.RoleSuperAdmin},
		OwnBranch:  []string{},
	}
	if wide, err := roleCfg.ExceptionApprovers(roleconfig.ExceptionFloorQuote); err == nil {
		for _, role := range wide {
			if erp, ok := identity.ERPCodeFor(role); ok {
				quoteFloorApprovers.DealerWide = append(quoteFloorApprovers.DealerWide, erp)
			}
		}
	}
	for _, role := range roleCfg.QuoteFloorBranchApprovers() {
		if erp, ok := identity.ERPCodeFor(role); ok {
			quoteFloorApprovers.OwnBranch = append(quoteFloorApprovers.OwnBranch, erp)
		}
	}
	approvals.WithFloorApprovers(quoteFloorApprovers)
	// The approvals sync door decides through the same configured
	// authority the rail now holds: its store is constructed on another
	// wire (wire_operations) the role configuration does not pass, so the
	// set the rail was given is published for the door to read (D11): one
	// authority, one source.
	identity.PublishFloorApprovers(quoteFloorApprovers)
	// The approvals sync door holds the REST routes' role walls (Work to
	// file, Manage to decide) from the same configuration the role gate was
	// built with, published for the store the operations wire builds.
	identity.PublishApprovalRoles(roleCfg)
	// The quotes sync door runs the same ownership ladder the quote
	// service runs, from the same configuration: the store is built on the
	// operations wire, so the configuration is published for it (P2-4).
	quote.PublishRoleConfig(roleCfg)
	quoteStaffSvc := quote.NewStaffService(quoteRepo).
		WithAudit(auditSvc).
		WithAuditTrail(auditSvc).
		WithPricing(pricingSvc).
		WithPricePreview(pricingSvc).
		WithCostBasis(inventorySvc).
		WithActorNamer(quoteRepo).
		WithRoleConfig(roleCfg).
		WithFloorArbiter(pricingSvc).
		WithFloorRail(approvals).
		WithShares(quoteRepo).
		WithStockReserver(w5aQuoteStockReserver{svc: inventorySvc}).
		WithAvailability(w5aQuoteAvailability{svc: inventorySvc}).
		WithSoftHoldExpiry(w6cQuoteSoftHoldExpiry{svc: orderSvc})
	// QD-5: the counter's payment door on a converted quote's order. The quote module declares
	// the two seams; the order and billing and payments modules answer them here and the quote
	// package never imports any of the three.
	quoteInvoicer, quotePayer := quoteMoneyDoor(pool, orderSvc)
	quoteStaffSvc.WithOrderInvoicer(quoteInvoicer).WithCounterPayer(quotePayer)
	quoteRepo.WithStockReserver(w5aQuoteStockReserver{svc: inventorySvc})
	quoteStaffHandler := quote.NewStaffHandler(quoteStaffSvc).WithUserLinker(link)
	quoteStaffHandler.RegisterStaffRoutes(r, g)
	quoteStaffHandler.RegisterShareRoutes(r, g)
	quoteStaffHandler.RegisterPublicRoutes(r)
	// The quote automation surface: staff + base URL + logger wired; the
	// notice queue and contact reader (messagingprovider, customer) land in
	// their own batches and the followup send path degrades without them.
	quoteAutomation := quote.NewAutomationService(quote.NewFollowUpRepository(pool)).
		WithStaff(quoteStaffSvc).
		WithBaseURL(staffBaseURL).
		WithLogger(log)
	quote.NewAutomationHandler(quoteAutomation).WithUserLinker(link).RegisterAutomationRoutes(r, g)
	quoteStaffHandler.RegisterAvailabilityRoutes(r, g)

	// HH-180: the audit reopen fence resolves an entity's owning branch through its owner. The two
	// owners the counter trail reads are wired here; every other entity type fails closed for a
	// scoped caller (an empty trail), the ruling recorded in docs/BLOCKERS.md.
	auditSvc.WithEntityBranchResolvers(map[string]audit.EntityBranchResolver{
		"order": auditOrderBranchResolver{repo: orderRepo},
		"quote": auditQuoteBranchResolver{repo: quoteRepo},
	})

	return &moneySpine{
		glSvc:           glSvc,
		inventorySvc:    inventorySvc,
		orderSvc:        orderSvc,
		orderRepo:       orderRepo,
		quoteRepo:       quoteRepo,
		approvals:       approvals,
		auditSvc:        auditSvc,
		outboxStore:     outboxStore,
		productSvc:      productSvc,
		pricingSvc:      pricingSvc,
		quoteStaffSvc:   quoteStaffSvc,
		quoteAutomation: quoteAutomation,
		roleCfg:         roleCfg,
	}
}

// moneySpine carries the money batch's shared handles to the operations
// batch's wire function: the services whose pointers later seams mutate
// (the rail's deciders), the repositories later adapters read (order,
// quote), and the audit service every operations module takes. One struct,
// because the operations modules were composed against exactly these
// instances at the pin and a second construction would strand a setter on
// an instance nobody serves.
type moneySpine struct {
	glSvc           *gl.Service
	inventorySvc    *inventory.Service
	orderSvc        *order.Service
	orderRepo       *order.Repository
	quoteRepo       *quote.Repository
	approvals       *identity.Approvals
	auditSvc        *audit.Service
	outboxStore     *notify.PgOutboxStore
	productSvc      *product.Service
	pricingSvc      *pricing.Service
	quoteStaffSvc   *quote.StaffService
	quoteAutomation *quote.AutomationService
	// roleCfg rides the spine for the operations wiring's role-gated
	// reads (the counts' X3 cost gate), the same configuration the
	// money-side reads already gate through here.
	roleCfg *roleconfig.Config
}

// jeApprovalFilerAdapter satisfies gl.ApprovalFiler over the approvals
// rail's FileJEApproval (the pin's adapter, re-pointed at the bridge).
type jeApprovalFilerAdapter struct{ svc *identity.Approvals }

func (a jeApprovalFilerAdapter) FileJEApproval(ctx context.Context, journalID, branchID string, amountCents int64, createdBy string) error {
	_, err := a.svc.FileJEApproval(ctx, journalID, branchID, amountCents, createdBy)
	return err
}

// jeDeciderAdapter satisfies the rail's JEDecider over gl's
// ApproveJournalTx: a rail-approve posts the journal inside the decision
// transaction, translating gl's typed refusals into the rail's client-error
// taxonomy (carried verbatim from the pin's adapter belt).
type jeDeciderAdapter struct{ svc *gl.Service }

func (a jeDeciderAdapter) ApproveJournalTx(ctx context.Context, tx pgx.Tx, journalID, deciderID string) error {
	if err := a.svc.ApproveJournalTx(ctx, tx, journalID, deciderID); err != nil {
		if st, ok := gl.ErrorHTTPStatus(err); ok {
			return identity.NewClientError(st, err.Error())
		}
		return err
	}
	return nil
}

// glRuleAdapter satisfies inventory's posting-rule resolver over gl
// (carried verbatim).
type glRuleAdapter struct{ svc *gl.Service }

func (a glRuleAdapter) ResolveRule(ctx context.Context, eventType, lineRole, branchID string) (string, error) {
	return a.svc.ResolveRule(ctx, eventType, lineRole, branchID)
}

// ResolveRuleTx is ResolveRule read through the caller's transaction (purchasing PostBill).
func (a glRuleAdapter) ResolveRuleTx(ctx context.Context, tx pgx.Tx, eventType, lineRole, branchID string) (string, error) {
	return a.svc.ResolveRuleTx(ctx, tx, eventType, lineRole, branchID)
}

// invGLPoster satisfies inventory's GL poster over gl's external posting
// seam (carried verbatim).
type invGLPoster struct{ svc *gl.Service }

func (a invGLPoster) PostJournal(ctx context.Context, in inventory.GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey, Memo: in.Memo, BranchID: in.BranchID, Lines: lines})
}

// w5aQuoteStockReserver satisfies quote's stock reservation seam over
// inventory (carried verbatim).
type w5aQuoteStockReserver struct{ svc *inventory.Service }

func (a w5aQuoteStockReserver) ReserveTx(ctx context.Context, tx pgx.Tx, in quote.StockReserveInput) (quote.StockReserveResult, error) {
	res, err := a.svc.ReserveTx(ctx, tx, inventory.ReserveInput{
		ProductID:        in.ProductID,
		BranchID:         in.BranchID,
		LocationID:       in.LocationID,
		ResType:          in.ResType,
		QtyBase:          in.QtyBase,
		SourceType:       in.SourceType,
		SourceRefID:      in.SourceRefID,
		Reason:           in.Reason,
		CreatedBy:        in.CreatedBy,
		EnforceAvailable: in.EnforceAvailable,
		ExpiresAt:        in.ExpiresAt,
	})
	if err != nil {
		return quote.StockReserveResult{}, err
	}
	return quote.StockReserveResult{
		ReservationID: res.Reservation.ID,
		HoldMode:      res.Reservation.HoldMode,
		Applied:       res.Applied,
	}, nil
}

func (a w5aQuoteStockReserver) ReleaseBySourceTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error) {
	return a.svc.ReleaseBySourceTx(ctx, tx, sourceType, sourceRefID, status)
}

// w5aQuoteAvailability satisfies quote.AvailabilityReader over the seam
// inventory published (carried verbatim, with its projection helper).
type w5aQuoteAvailability struct{ svc *inventory.Service }

func w5aBranchStock(b inventory.BranchStock) quote.BranchStock {
	return quote.BranchStock{
		ProductID:     b.ProductID,
		BranchID:      b.BranchID,
		BranchCode:    b.BranchCode,
		BranchName:    b.BranchName,
		OnHandBase:    b.OnHandBase,
		ReservedBase:  b.ReservedBase,
		InTransitBase: b.InTransitBase,
		AvailableBase: b.AvailableBase,
		SoftHeldBase:  b.SoftHeldBase,
		AvgCostCents:  b.AvgCostCents,
	}
}

func (a w5aQuoteAvailability) AvailabilityAtBranch(ctx context.Context, productID, branchID string) (quote.BranchStock, error) {
	b, err := a.svc.AvailabilityAtBranch(ctx, productID, branchID)
	if err != nil {
		return quote.BranchStock{}, err
	}
	return w5aBranchStock(b), nil
}

func (a w5aQuoteAvailability) AvailabilityAllBranches(ctx context.Context, productID string) ([]quote.BranchStock, error) {
	rows, err := a.svc.AvailabilityAllBranches(ctx, productID)
	if err != nil {
		return nil, err
	}
	out := make([]quote.BranchStock, len(rows))
	for i, b := range rows {
		out[i] = w5aBranchStock(b)
	}
	return out, nil
}

// w6cQuoteSoftHoldExpiry satisfies quote's soft-hold expiry seam over
// order (carried verbatim).
type w6cQuoteSoftHoldExpiry struct{ svc *order.Service }

func (a w6cQuoteSoftHoldExpiry) ExpiredSoftHolds(ctx context.Context, quoteID string) (quote.SoftHoldStatus, error) {
	st, err := a.svc.ExpiredSoftHolds(ctx, quoteID)
	if err != nil {
		return quote.SoftHoldStatus{}, err
	}
	out := quote.SoftHoldStatus{
		QuoteID: st.QuoteID, ActiveCount: st.ActiveCount, AnyExpired: st.AnyExpired,
		Expired: make([]quote.ExpiredHold, 0, len(st.Expired)),
	}
	for _, h := range st.Expired {
		out.Expired = append(out.Expired, quote.ExpiredHold{
			ReservationID: h.ReservationID, ProductID: h.ProductID, ProductName: h.ProductName,
			SKU: h.SKU, BranchID: h.BranchID, QtyBase: h.QtyBase, Status: h.Status,
			ExpiresAt: h.ExpiresAt, EndedAt: h.EndedAt,
		})
	}
	return out, nil
}

// pricingCostVisibility satisfies pricing's cost visibility gate over the
// bridged caller (carried verbatim; the context read is the bridge stamp
// the wire's registrars make from the guard-resolved caller).
type pricingCostVisibility struct{}

func (pricingCostVisibility) IsManager(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		// Not an error: there is nothing wrong, there is simply nobody to admit.
		return false, nil
	}
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || caller.UserID != userID {
		return false, nil
	}
	return caller.IsManager(), nil
}

// pricingVendorDirectory satisfies pricing's vendor picker read (carried
// verbatim).
type pricingVendorDirectory struct{ pool *pgxpool.Pool }

func (a pricingVendorDirectory) ListVendors(ctx context.Context) ([]pricing.PriceFileVendor, error) {
	if a.pool == nil {
		return nil, nil
	}
	rows, err := a.pool.Query(ctx, `SELECT id::text, vendor_no::text, name FROM vendors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pricing.PriceFileVendor
	for rows.Next() {
		var v pricing.PriceFileVendor
		if err := rows.Scan(&v.VendorID, &v.VendorNo, &v.Name); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// pricingCatalogSKUs satisfies pricing's catalogue SKU lookup: one call for
// the whole file, a SKU missing from the result is a SKU we do not stock,
// and an error is propagated so pricing flags the preview unchecked (carried
// verbatim).
type pricingCatalogSKUs struct{ pool *pgxpool.Pool }

func (a pricingCatalogSKUs) LookupSKUs(ctx context.Context, skus []string) (map[string]pricing.CatalogSKU, error) {
	if a.pool == nil || len(skus) == 0 {
		return map[string]pricing.CatalogSKU{}, nil
	}
	rows, err := a.pool.Query(ctx,
		`SELECT id::text, sku::text, name FROM products WHERE sku = ANY($1::citext[])`, skus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]pricing.CatalogSKU, len(skus))
	for rows.Next() {
		var e pricing.CatalogSKU
		if err := rows.Scan(&e.ProductID, &e.SKU, &e.Name); err != nil {
			return nil, err
		}
		out[e.SKU] = e
	}
	return out, rows.Err()
}

// w5cOverrideAuthority satisfies pricing.OverrideAuthority over the
// bridged caller stamp and the dealer's role configuration: the caller's
// own manager scope, held to the CONFIGURED floor clearers
// (floor_clearers_own_branch under the manager_own_branch_floor_clearing
// setting, D6) and to the branch being priced. The arm once cleared for
// any manager that reached the branch, the Yard manager included, where
// the configuration names the Sales manager and the Branch manager alone.
//
// The approvals substrate arms NOTHING here. A generic resolve carries no
// document, so an approval lookup would match an EMPTY subject: through
// the preview route's body flag any approved margin card answered
// "granted" whatever its subject and however used up (D3, the QD-1
// review's P2-5; the flag no longer reaches the resolver either, the
// pricing handler reads and drops it). Until a live caller can bind its
// request to a document, no approval arm may grant; the quote floor path
// consumes subject narrowed requests on its own transaction instead.
type w5cOverrideAuthority struct {
	roles *roleconfig.Config
	log   *slog.Logger
}

func (a w5cOverrideAuthority) MarginOverrideAuthority(ctx context.Context, userID, branchID string) (pricing.OverrideGrant, error) {
	if userID == "" || branchID == "" {
		// An unattributed or unlocated request cannot be authorised. Not an error: there is nothing
		// wrong, there is simply nobody to grant anything to.
		return pricing.OverrideGrant{}, nil
	}
	// No configuration, no grant: the configured list IS the authority.
	if a.roles == nil {
		return pricing.OverrideGrant{}, nil
	}
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || caller.UserID != userID {
		return pricing.OverrideGrant{}, nil
	}
	if !caller.CanAccessBranch(branchID) {
		return pricing.OverrideGrant{}, nil
	}
	role, ok := identity.CanonicalForERPCode(caller.RoleCode)
	if !ok {
		return pricing.OverrideGrant{}, nil
	}
	clears, err := a.roles.ClearsOwnBranchFloor(role)
	if err != nil || !clears {
		return pricing.OverrideGrant{}, nil
	}
	if a.log != nil {
		a.log.Info("pricing: below-floor price cleared on MANAGER SCOPE (no approval record)",
			"user_id", userID, "branch_id", branchID, "role", caller.RoleCode)
	}
	return pricing.OverrideGrant{Granted: true, Source: pricing.AuthorityManagerScope}, nil
}

// quoteMoneyDoor builds the two seams the quote module's payment door needs, over the real order,
// billing and payments modules (QD-5). The writer is the same type the money tail uses, bound to an
// order source that also answers the agreed facts; the group invoicer of an order that later gains a
// shipment plan is the money tail's writer, which looks for this document first.
func quoteMoneyDoor(pool *pgxpool.Pool, orderSvc *order.Service) (quote.OrderInvoicer, quote.CounterPayer) {
	w := billing.NewInvoiceWriter(pool).WithOrderSource(quoteAgreedOrderSource{
		w6cOrderInvoiceSource: w6cOrderInvoiceSource{svc: orderSvc}, svc: orderSvc,
	})
	return quoteOrderInvoicer{w: w}, quoteCounterPayer{repo: payments.NewRepository(pool)}
}

// posPayBalanceInvoicer binds the Pay balance take's invoicer seam to the same billing writer the
// desk's order payment raises with (item 9 review P2-2): an order that owes an uninvoiced remainder
// gets its agreed invoice raised inside the take's transaction, one writer, never a copy. Every
// refusal the raise can return crosses as the take door's own fixed words (payBalanceRaiseRefusal).
type posPayBalanceInvoicer struct{ w *billing.InvoiceWriter }

func (a posPayBalanceInvoicer) RaiseOrderInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (pos.RaisedOrderInvoice, error) {
	doc, err := a.w.InvoiceOrderAtAgreedTx(ctx, tx, orderID)
	if err != nil {
		if mapped := payBalanceRaiseRefusal(err); mapped != nil {
			return pos.RaisedOrderInvoice{}, mapped
		}
		return pos.RaisedOrderInvoice{}, err
	}
	return pos.RaisedOrderInvoice{
		InvoiceID: doc.InvoiceID, InvoiceNo: doc.InvoiceNo,
		TotalCents: doc.TotalCents, BalanceCents: doc.BalanceCents,
	}, nil
}

// payBalanceRaiseRefusal maps every refusal the raise can still return once the take's pre read has
// done its work to the door's own fixed 409 sentence (the delta review's P2-A): the order module's
// refusals arrive wrapped as the desk's payment conflict (agreedFactsFor below), ErrNothingToInvoice
// arrives raw, and an unwrapped AgreedBillingRefusal is mapped too in case a future source forgets
// the wrapper. Any other error is not a refusal (it is a failure to answer, for the log) and comes
// back nil to cross untouched.
func payBalanceRaiseRefusal(err error) error {
	var already *billing.OrderAlreadyInvoicedError
	if errors.As(err, &already) {
		return pos.ErrPayBalanceRestUnbillable
	}
	var conflict *quote.OrderPaymentConflictError
	var refusal *order.AgreedBillingRefusal
	if errors.As(err, &conflict) || errors.As(err, &refusal) || errors.Is(err, billing.ErrNothingToInvoice) {
		return pos.ErrPayBalanceRaiseRefused
	}
	return nil
}

// quoteAgreedOrderSource is the money tail's order source plus the one answer the counter's agreed
// invoice needs: every line at its agreed total, the delivery fee and the tax the quote agreed, read
// through the pool or on the door's transaction.
type quoteAgreedOrderSource struct {
	w6cOrderInvoiceSource
	svc *order.Service
}

func (a quoteAgreedOrderSource) AgreedInvoiceableOrder(ctx context.Context, orderID string) (billing.OrderInvoiceFacts, error) {
	f, err := a.svc.AgreedInvoiceableOrder(ctx, orderID)
	return agreedFactsFor(f, err)
}

func (a quoteAgreedOrderSource) AgreedInvoiceableOrderTx(ctx context.Context, tx pgx.Tx, orderID string) (billing.OrderInvoiceFacts, error) {
	f, err := a.svc.AgreedInvoiceableOrderTx(ctx, tx, orderID)
	return agreedFactsFor(f, err)
}

// agreedFactsFor shapes the order module's answer into billing's facts, carrying its refusal across
// as the quote door's 409.
func agreedFactsFor(f order.InvoiceFacts, err error) (billing.OrderInvoiceFacts, error) {
	if err != nil {
		var refusal *order.AgreedBillingRefusal
		if errors.As(err, &refusal) {
			return billing.OrderInvoiceFacts{}, quote.NewOrderPaymentConflict(refusal.Error())
		}
		return billing.OrderInvoiceFacts{}, err
	}
	out := billing.OrderInvoiceFacts{
		OrderID: f.OrderID, OrderNo: f.OrderNo, CustomerID: f.CustomerID, BranchID: f.BranchID,
		TermsDays: f.TermsDays, TaxReasonCode: f.TaxReasonCode, ClosesOrder: true,
		FreightCents: f.FreightCents, AgreedTaxCents: f.AgreedTaxCents,
	}
	out.Lines = make([]billing.OrderInvoiceLine, 0, len(f.Lines))
	for _, l := range f.Lines {
		out.Lines = append(out.Lines, billing.OrderInvoiceLine{
			LineNo: l.LineNo, OrderLineID: l.OrderLineID, ProductID: l.ProductID,
			Description: l.Description, QtyEntered: l.QtyEntered, UOMEntered: l.UOMEntered,
			QtyBase: l.QtyBase, UnitPriceCents: l.UnitPriceCents, PriceUOM: l.PriceUOM,
			LineTotalCents: l.LineTotalCents,
		})
	}
	return out, nil
}

// quoteOrderInvoicer binds the quote module's invoicer seam to the billing writer, every call on the
// door's transaction.
type quoteOrderInvoicer struct{ w *billing.InvoiceWriter }

func (a quoteOrderInvoicer) AgreedInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (quote.AgreedInvoice, error) {
	doc, exists, err := a.w.AgreedOrderInvoiceTx(ctx, tx, orderID)
	if err != nil {
		return quote.AgreedInvoice{}, mapBillingRefusal(err)
	}
	return agreedFromDoc(doc, exists), nil
}

func (a quoteOrderInvoicer) RaiseAgreedInvoiceTx(ctx context.Context, tx pgx.Tx, orderID string) (quote.AgreedInvoice, error) {
	doc, err := a.w.InvoiceOrderAtAgreedTx(ctx, tx, orderID)
	if err != nil {
		return quote.AgreedInvoice{}, mapBillingRefusal(err)
	}
	return agreedFromDoc(doc, true), nil
}

func agreedFromDoc(d billing.InvoiceDoc, exists bool) quote.AgreedInvoice {
	return quote.AgreedInvoice{
		Exists: exists, InvoiceID: d.InvoiceID, InvoiceNo: d.InvoiceNo, CustomerID: d.CustomerID,
		BranchID: d.BranchID, TotalCents: d.TotalCents, BalanceCents: d.BalanceCents,
	}
}

// mapBillingRefusal turns billing's business refusal into the quote door's 409; every other error
// passes through unchanged.
func mapBillingRefusal(err error) error {
	if errors.Is(err, billing.ErrNothingToInvoice) {
		return quote.NewOrderPaymentConflict("the order has no billable lines")
	}
	var already *billing.OrderAlreadyInvoicedError
	if errors.As(err, &already) {
		return quote.NewOrderPaymentConflict(already.Error())
	}
	return err
}

// quoteCounterPayer binds the quote module's payer seam to the payments settle, on the door's
// transaction.
type quoteCounterPayer struct{ repo *payments.Repository }

func (a quoteCounterPayer) KeyTakenTx(ctx context.Context, tx pgx.Tx, key string) (bool, error) {
	return a.repo.ReceiptKeyTakenTx(ctx, tx, key)
}

// LockPaymentsSequenceTx binds the door's sequence order seam to the payments module's shared
// non incrementing lock on the caller's transaction (payments/LOCK-ORDER.md), the same adapter
// belt as the other two methods.
func (a quoteCounterPayer) LockPaymentsSequenceTx(ctx context.Context, tx pgx.Tx) error {
	return a.repo.LockPaymentsSequenceTx(ctx, tx)
}

func (a quoteCounterPayer) TakePaymentTx(ctx context.Context, tx pgx.Tx, in quote.CounterReceiptInput) (quote.CounterReceipt, error) {
	out, err := a.repo.RecordReceiptTx(ctx, tx, payments.ReceiptRequest{
		CustomerID:     in.CustomerID,
		Method:         in.Method,
		Reference:      in.Reference,
		IdempotencyKey: in.IdempotencyKey,
		ReceivedBy:     in.ReceivedBy,
		BranchID:       in.BranchID,
		Allocations:    []payments.Allocation{{InvoiceID: in.InvoiceID, AmountCents: in.AmountCents}},
	})
	if err != nil {
		return quote.CounterReceipt{}, err
	}
	return quote.CounterReceipt{
		PaymentID: out.Payment.ID, PaymentNo: out.Payment.PaymentNo, Method: out.Payment.Method,
		AmountCents: out.Payment.AmountCents, ReceivedAt: out.Payment.ReceivedAt,
		Reference: out.Reference, ReceivedBy: out.ReceivedBy, Replayed: out.Replayed,
	}, nil
}
