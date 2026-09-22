package portal

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// dashboardRecentOrders caps the recent-orders strip on the account home so the
// dashboard read never pulls an unbounded order set.
const dashboardRecentOrders = 5

// DashboardResponse is the account-home payload: the resolved account facts (with the
// composed AR balance filled in), the open balance, the available credit headroom, and
// a short recent-orders strip.
type DashboardResponse struct {
	Account              customer.Account `json:"account"`
	OpenBalanceCents     int64            `json:"open_balance_cents"`
	AvailableCreditCents int64            `json:"available_credit_cents"`
	RecentOrders         []order.Order    `json:"recent_orders"`
}

// BillingSummaryResponse is the AR roll-up: open balance, the credit limit, and the
// available credit headroom (limit - open AR, floored at 0), plus the hold flag.
type BillingSummaryResponse struct {
	OpenBalanceCents     int64 `json:"open_balance_cents"`
	CreditLimitCents     int64 `json:"credit_limit_cents"`
	AvailableCreditCents int64 `json:"available_credit_cents"`
	OnHold               bool  `json:"on_hold"`
	PaymentTermsDays     int   `json:"payment_terms_days"`
}

// registerDashboardRoutes wires the account home + the AR billing summary. Both require
// ViewBilling, so field_crew is shut out (403) while Owner/Buyer pass.
func (h *Handler) registerDashboardRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/portal/v1/dashboard", g.RequireCapability(identity.CapViewBilling, h.tenant(h.dashboard)))
	r.Handle("GET /api/portal/v1/billing/summary", g.RequireCapability(identity.CapViewBilling, h.tenant(h.billingSummary)))
}

// dashboard returns the account-home KPIs: balance, credit limit, available credit
// (limit - open AR), and a recent-orders strip. Composes the customer + billing seams
// (and the order seam when wired); every read is scoped to the caller's tenant.
func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	if h.customerSvc == nil || h.billingSvc == nil {
		httpx.NotImplemented(w, "account dashboard not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)

	acct, err := h.customerSvc.ResolveAccount(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	bal, err := h.billingSvc.OpenBalanceCents(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// The resolver leaves BalanceCents at 0; the BFF composes the AR figure in here so
	// the account view and the available-credit math share one balance snapshot.
	acct.BalanceCents = bal
	avail := availableCredit(acct.CreditLimitCents, bal)

	var recent []order.Order
	if h.orderSvc != nil {
		items, err := h.orderSvc.ListOrders(ctx, caller.CustomerID, order.Filter{Limit: dashboardRecentOrders})
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		recent = items
	}
	if recent == nil {
		recent = []order.Order{}
	}

	httpx.WriteJSON(w, http.StatusOK, DashboardResponse{
		Account:              acct,
		OpenBalanceCents:     bal,
		AvailableCreditCents: avail,
		RecentOrders:         recent,
	})
}

// billingSummary is the AR roll-up. Gated on ViewBilling. Composes the credit facts from
// the customer seam with the open balance from the billing seam.
func (h *Handler) billingSummary(w http.ResponseWriter, r *http.Request) {
	if h.customerSvc == nil || h.billingSvc == nil {
		httpx.NotImplemented(w, "billing summary not implemented")
		return
	}
	ctx := r.Context()
	caller := callerFrom(ctx)

	acct, err := h.customerSvc.ResolveAccount(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	bal, err := h.billingSvc.OpenBalanceCents(ctx, caller.CustomerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, BillingSummaryResponse{
		OpenBalanceCents:     bal,
		CreditLimitCents:     acct.CreditLimitCents,
		AvailableCreditCents: availableCredit(acct.CreditLimitCents, bal),
		OnHold:               acct.OnHold,
		PaymentTermsDays:     acct.PaymentTermsDays,
	})
}

// availableCredit is creditLimit - open AR, floored at 0 (an over-limit account has zero
// headroom, never a negative one). Mirrors billing.AvailableCreditCents so the dashboard
// can reuse the single balance read instead of issuing a second AR query.
func availableCredit(creditLimitCents, openBalanceCents int64) int64 {
	avail := creditLimitCents - openBalanceCents
	if avail < 0 {
		return 0
	}
	return avail
}
