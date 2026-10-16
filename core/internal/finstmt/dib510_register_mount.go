package finstmt

// dib510_register_mount.go, where the receivables register's routes get mounted, and why here.
//
// -- THE CONSTRAINT ----------------------------------------------------------------------------
// cmd/server/main.go is off limits to a lane, so a new surface has to be mounted from a registration
// the composition root ALREADY makes. The register needs three gates, staff for the reads and the
// counter acts, manager for the collections campaign, admin for the dealer policy (the reasoning is
// in billing/register_handler.go's header), and this module's registration is handed all three:
//
//     finstmt.NewHandler(finstmtSvc, log).RegisterRoutes(mux, RequireStaff, RequireManager, RequireAdmin)
//
// Its `requireStaff` parameter has been carried unused since the W5 signature was frozen. This is the
// first thing that needs it.
//
// -- WHY THIS MODULE AND NOT THE AR CONSOLE'S --------------------------------------------------
// The obvious host was internal/till, which already owns half the /api/v1/ar/* namespace and mounts
// its own invoice-scoped reads by exactly this one-line idiom. It is not possible: internal/pos
// imports till, and billing's own test suite imports pos, so an edge from till to billing closes a
// cycle Go refuses (`import cycle not allowed in test`). That is a fact about this repository's shape
// rather than a judgement about where the code belongs, and it is written down here so the next
// person does not spend an afternoon rediscovering it.
//
// finstmt is the module the Finance unit's own surfaces already come from, the statements, the
// trial balance, and `GET /api/v1/finstmt/ar-aging`, whose grand total this very module's model.go
// documents as tying to billing's AR balance. A receivables register mounted beside the AR aging read
// is in the right neighbourhood.
//
// -- THE BOUNDARY IS NOT BENT ------------------------------------------------------------------
// Nothing about invoices is IMPLEMENTED here. billing owns the register, its model, its projection,
// its assessors, its handler, exactly as the module rule requires ("this module is the sole reader of
// invoices + invoice_lines + customer_transactions"). This file is a composition seam and nothing
// else: it resolves the pool, builds billing's handler and the payments settle behind it, and calls
// RegisterRoutes. It is the same shape as the quote pilot's mount of the shared settings kit -
// "mounted when the store is the real repository", and for the same reason.
//
// A scaffold/offline repository does not mount the register at all, rather than mounting one that
// cannot answer.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/taxwords"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RegisterSender is billing.Sender, the register's delivery port, restated as this module's own
// interface so a caller can bind one without importing billing.
type RegisterSender interface {
	SendInvoiceMessage(ctx context.Context, to, subject, body string) error
}

// WithRegisterSender binds the receivables register's delivery port.
//
// UNBOUND IS THE SHIPPED STATE, and it is honest rather than broken: every send is still RECORDED in
// ar_invoice_deliveries, the ledger row is the durable fact an operator can point at, and the open
// tracking hangs off it, while nothing is handed to a transport. That is the DIB-166 statement-notice
// posture verbatim ("runs generate and mark QUEUED, and nothing is delivered until a provider is
// wired here"), and the screen reports it in the outcome line rather than claiming a delivery it
// cannot make.
func (h *Handler) WithRegisterSender(s RegisterSender) *Handler {
	h.registerSender = s
	return h
}

// WithPaymentTrail binds the reader that says whether a caller holds the payment trail reads. Unbound,
// the register's invoice detail withholds the payment entries from every caller.
func (h *Handler) WithPaymentTrail(t billing.PaymentTrailReader) *Handler {
	h.paymentTrail = t
	return h
}

// WithRegisterTaxLabel binds the dealer's tax word seam (platform/taxwords) for the register's printed
// invoice. Unbound is the Ontario dealer's "HST".
func (h *Handler) WithRegisterTaxLabel(src taxwords.Source) *Handler {
	h.registerTaxWords = src
	return h
}

// NotifierSender adapts the shipped notify port onto the register's Sender. Pure translation: the
// register composes the words (billing/register_messages.go), notify decides how they travel.
type NotifierSender struct{ N notify.Notifier }

// SendInvoiceMessage delivers one register message as email.
func (s NotifierSender) SendInvoiceMessage(ctx context.Context, to, subject, body string) error {
	return s.N.Send(ctx, notify.Message{
		Channel: notify.EmailChannel, To: to, Subject: subject, Body: body,
	})
}

// mountInvoiceRegister wires and registers the DIB-510 receivables register.
func (h *Handler) mountInvoiceRegister(r httpx.Registrar, g *guards.Guards) {
	pool := h.svc.registerPool()
	if pool == nil {
		return
	}
	repo := billing.NewRepository(pool).WithZone(h.zone)
	svc := billing.NewRegisterService(repo).
		WithZone(h.zone).
		WithMoney(billing.NewPaymentsReceiptWriter(payments.NewRepository(pool))).
		// The SHIPPED statement generator (billing/statements.go), the same one the month-end run
		// drives. The bulk bar produces statements; it does not compose them.
		WithStatements(billing.NewStatementService(repo).WithZone(h.zone))
	if h.registerSender != nil {
		svc = svc.WithSender(h.registerSender)
	}
	billing.NewRegisterHandler(svc, repo, h.log).
		WithBranchNamer(branchNamer(pool)).
		WithUserLinker(h.userLink).
		WithPaymentTrail(h.paymentTrail).
		WithTaxLabel(h.registerTaxWords).
		RegisterRoutes(r, g)
	// Walk change 8 S5/S6/S8: promises list, account payment policy, reminder rules + enrol/exempt.
	// Lives here because cmd/server/main.go is off limits to a lane and the same pool backs it;
	// see c8walk_handler.go's header for why these handlers are in finstmt and not in billing.
	NewC8WalkHandler(pool, h.log).RegisterC8WalkRoutes(r, g)

	// -- the customer account workspace (Change 8 S2 + S2M) ----------------------------------------
	// Mounted from this composition seam for the same reason the register is: cmd/server/main.go
	// is off limits to a lane, and this is the registration the Finance module already makes. The
	// workspace uses the same pool-backed repository the register just bound, and a CreditService
	// pointing at the same repo so the 5-bucket aging the workspace opens with is the AR
	// console's own read, by the discipline customer_ar_read.go spends thirty lines on.
	creditSvc := billing.NewCreditService(repo)
	if h.svc != nil && h.svc.now != nil {
		creditSvc = creditSvc.WithClock(h.svc.now)
	}
	billing.NewWorkspaceHandler(repo, creditSvc, h.log).RegisterRoutes(r, g)

	// -- the C8 S3 + S4 + S3M surface (manual hold, paylink, statement preview) ---------------------
	// Three new operations beside the workspace handler. The handler rides the same billing
	// Repository the workspace and the register just bound, the same caller-stamp middleware
	// (caller_stamp_workspacehandler.go's pattern), and the same composition seam this
	// module already owns. Mounted from this seam for the same reason the register is:
	// cmd/server/main.go is off limits to a lane.
	billing.NewC8S3S4Handler(repo, h.log).WithUserLinker(h.userLink).RegisterRoutes(r, g)
}

// registerPool resolves the pgx pool behind this service's repository, or nil in scaffold mode.
func (s *Service) registerPool() *pgxpool.Pool {
	if s == nil || s.repo == nil {
		return nil
	}
	return s.repo.pool
}

// branchNamer resolves a branch id to its business name for the printed invoice. A lookup rather than
// a join because it is asked once per printed page, and a failure returns "" so the document falls
// back to the id, a document with an ugly header still prints, and one that will not print is worse.
func branchNamer(pool *pgxpool.Pool) func(string) string {
	return func(id string) string {
		if id == "" {
			return ""
		}
		var name string
		if err := pool.QueryRow(context.Background(),
			`SELECT name FROM locations WHERE id = $1::uuid`, id).Scan(&name); err != nil {
			return ""
		}
		return name
	}
}
