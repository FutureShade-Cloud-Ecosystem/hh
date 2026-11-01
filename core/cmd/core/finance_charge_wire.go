package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

// newFinanceChargeWriter builds the invoice writer the finance charge run raises its charge documents
// through, with the real audit recorder so a pressed run is recorded (W3 FC-1b). It is the common
// writer (dealer zone, terms, holdback seam) and nothing else: a charge is billed as stated with no
// assessed tax, so it binds no tax assessor, order source or credit memo poster. The route and the
// worker both build it here so neither can drift.
func newFinanceChargeWriter(pool *pgxpool.Pool, auditSvc *audit.Service) *billing.InvoiceWriter {
	return newInvoiceWriter(pool).WithAudit(auditSvc)
}

// newFinanceChargeHandler builds the Controller's finance charge doors: the press and the read over
// the writer's run, and the waiver door with the real audit recorder on both.
func newFinanceChargeHandler(pool *pgxpool.Pool, auditSvc *audit.Service) *billing.FinanceChargeRouteHandler {
	repo := billing.NewRepository(pool)
	return billing.NewFinanceChargeRouteHandler(billing.NewFinanceChargeService(
		newFinanceChargeWriter(pool, auditSvc),
		billing.NewFinanceChargeWaiverService(repo).WithAudit(auditSvc),
		repo))
}
