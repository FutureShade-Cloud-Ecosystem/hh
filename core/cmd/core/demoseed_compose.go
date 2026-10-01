// compose.go, the demo layer's composition subset.
//
// WHY THIS FILE EXISTS AT ALL. The point of this command is that a demo order which reads "picked"
// arrived there the way the software takes it: through the shipped services, over their real
// transitions, writing their real audit rows. That means the command needs the same wiring
// cmd/server builds, not a parallel one. Every constructor and every adapter below is a
// transcription of the composition root (cmd/server/main.go), narrowed to the modules this layer
// drives. Nothing here invents a seam, relaxes a gate, or substitutes a stub for something the
// server runs for real.
//
// THE THREE DELIBERATE DIFFERENCES FROM cmd/server, each stated so a reader does not have to
// diff two files to find them:
//
//  1. NO ROUTES. Nothing is registered on a mux; the services are called in-process. The staff
//     identity every one of them fences on is stamped into the context with
//     identity.ContextWithStaff, the same StaffContext the RequireStaff middleware would have
//     put there, so every branch fence, tier check and caller-status check still runs.
//  2. NO DISPATCHER. The command emits domain events through the real service paths and does NOT
//     drain events_outbox. Draining is the running server's job: a dispatcher registered with a
//     SMALLER handler map than the server's DEAD-LETTERS every topic it does not know, which on a
//     populated staging database would destroy pending real work. See NOTES-DEMO-DATA.md.
//  3. NO GL. The GL consumer is a dispatcher consumer (see 2), so the journals for these sales post
//     when the server drains, exactly as they do for a counter sale rung at 9am.
//
// demoseed_compose carries the pin's cmd/demoseed/compose.go (the fold:
// one-shot subcommand of cmd/core, plan and apply modes preserved).
package main

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// world is the wired module set the day script drives.
type world struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	pos       *pos.Service
	till      *till.Service
	order     *order.Service
	orderRepo *order.Repository
	tasks     *tasks.Service
	dispatch  *dispatch.Service
	inventory *inventory.Service
	messaging *messaging.Service
	audit     *audit.Service
}

// compose builds the module set. vaultKey is cfg.PaymentVaultKey, empty is fine here for the same
// reason it is fine in dev: the vault seals a CARD token whose plaintext is a stub processor ref,
// and the demo layer never asks for it back.
func compose(pool *pgxpool.Pool, log *slog.Logger, vaultKey string) (*world, error) {
	auditSvc := audit.NewService(audit.NewRepository(pool), log)

	productSvc := product.NewService(product.NewRepository(pool))
	pricingSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(productSvc).
		WithUOMConverter(productSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())

	inventorySvc := inventory.NewService(inventory.NewRepository(pool)).WithAudit(auditSvc)
	taxSvc := tax.NewService(tax.NewRepository(pool)).WithAudit(auditSvc)

	payVault, err := payment.NewVault(vaultKey)
	if err != nil {
		return nil, err
	}
	paySvc := payment.NewService(payment.NewStore(pool, payVault), payment.RunPaymentsStub{})

	tillRepo := till.NewRepository(pool)
	tillSvc := till.NewService(tillRepo).
		WithCardCharger(cardChargerAdapter{svc: paySvc}).
		WithAudit(auditSvc)

	// The till's role-configuration rules ride the demo compose too (RT-B):
	// the configuration is loaded, so the seeded world's service answers
	// the rules from the same document the server does. This compose builds
	// no serving surface (the one-shot seeds and stops), so the approver
	// credential source has no gate to resolve through here; the demo
	// serve path wires it with wireTillRules like the serve role.
	posSvc := pos.NewService(pos.NewRepository(pool)).
		WithLineResolver(pricingSvc).
		WithTenderTaker(tillSvc).
		WithCreditGate(till.NewHoldAwareCreditGate(tillRepo)).
		WithSignatureProvider(pos.NewTypedNameSignatureProvider(payVault)).
		WithTaxAssessor(posTaxAdapter{svc: taxSvc}).
		WithStock(posStockAdapter{svc: inventorySvc}).
		WithAudit(auditSvc).
		WithRoleConfig(loadRoleConfig())

	orderRepo := order.NewRepository(pool)
	orderSvc := order.NewService(orderRepo).WithAudit(auditSvc).WithAuditTrail(auditSvc)

	messagingSvc := messaging.NewService(messaging.NewRepository(pool))

	tasksSvc := tasks.NewService(tasks.NewRepository(pool)).
		WithOrderReader(orderReaderAdapter{repo: orderRepo}).
		WithLogger(log)

	dispatchSvc := dispatch.NewService(dispatch.NewRepository(pool)).
		WithOrders(dispatchOrderAdapter{repo: orderRepo}).
		WithSignatures(dispatchSignatureAdapter{svc: posSvc}).
		WithAudit(auditSvc).
		WithLogger(log)

	return &world{
		pool: pool, log: log,
		pos: posSvc, till: tillSvc, order: orderSvc, orderRepo: orderRepo,
		tasks: tasksSvc, dispatch: dispatchSvc, inventory: inventorySvc,
		messaging: messagingSvc, audit: auditSvc,
	}, nil
}

// -- the adapters: the pin's cmd/demoseed transcribed five from
// cmd/server/main.go (cardChargerAdapter, posTaxAdapter, posStockAdapter,
// orderReaderAdapter, dispatchOrderAdapter, dispatchSignatureAdapter).
// The fold puts both commands in one package, so the transcriptions are
// the SAME declarations now: the shared belt in tops_adapters.go and
// wire_operations.go owns them, and this file keeps the note instead of a
// duplicate (the one-package constraint forces exactly this collapse; the
// bodies were identical at the pin).
