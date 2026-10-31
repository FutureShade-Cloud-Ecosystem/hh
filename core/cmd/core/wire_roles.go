package main

// wire_roles.go, the till's role-configuration wiring (RT-B, the lead's
// accepted deviation into cmd/core; RG's fuller wiring of the same
// configuration into the gate supersedes this file's load when it lands,
// and the two must share one load then). The composition wires the real
// things: the dealer's role configuration loaded at boot (the same load
// whose refusal stops the core), the configuration into the pos service's
// refund rules and margin glance, and the approver's credential resolved
// through the same guards the routes registered behind. The nil gates in
// pos fail closed, so an unwired composition refuses rather than admits;
// this file is what keeps the wiring from being needed to refuse.

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	pos "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// loadRoleConfig loads the dealer's role configuration the till's rules
// read. The startup contract: the load's refusal stops the core, a refused
// configuration never reaches the rules as a zero value and never falls
// back to the default behind the refusal's back. Today it loads the
// decided default embedded in roleconfig; a per dealer file later swaps
// the source, never the contract.
func loadRoleConfig() *roleconfig.Config {
	cfg, err := roleconfig.Default()
	if err != nil {
		panic("role configuration refused at boot: " + err.Error())
	}
	return cfg
}

// wireTillRules wires the counter's role-configuration rules into one pos
// service: the glance's cost and margin list (X3), the refund amount and
// its approvers and the approval window (X7), and the approver credential
// resolved through the same guards the routes registered behind, with the
// ERP users.id linker so a committed decision names both people in one
// identifier space.
func wireTillRules(posSvc *pos.Service, roleCfg *roleconfig.Config, g *guards.Guards, link identity.UserIDLinker) *pos.Service {
	return posSvc.WithRoleConfig(roleCfg).
		WithApproverSource(g).
		WithUserLinker(link)
}

// tillPaymentRoutes are the routes whose grant "may take a payment at the till" means: the till
// sale and the payment on a converted quote's order. The grant the payment grant read answers is
// the gate's own admission to every one of them, read from the document's annotation on each, so a
// change to a route's surface or level moves the answer with it.
var tillPaymentRoutes = []struct{ method, path string }{
	{"POST", "/api/v1/pos/sales"},
	{"POST", "/api/v1/quotes/{id}/order-payment"},
}

// tillPaymentGrant answers pos.GrantReader over the role gate.
type tillPaymentGrant struct {
	gate     *authz.RoleGate
	accesses []authz.RouteAccess
}

// newTillPaymentGrant reads each payment route's access from the census. A route the census no
// longer annotates is a wiring defect that stops the boot, never a read that answers "yes" for want
// of a question.
func newTillPaymentGrant(gate *authz.RoleGate) (*tillPaymentGrant, error) {
	if gate == nil {
		return nil, errors.New("payment grant: the role gate is required")
	}
	g := &tillPaymentGrant{gate: gate}
	for _, r := range tillPaymentRoutes {
		access, ok := authz.RouteAccessFor(r.method, r.path)
		if !ok {
			return nil, fmt.Errorf("payment grant: %s %s carries no staff access in the document", r.method, r.path)
		}
		g.accesses = append(g.accesses, access)
	}
	return g, nil
}

// HoldsPayment is true only when the gate would admit the caller to every payment route AND the
// payment door's own not-selling-role rule would let the caller convert and pay, by the one
// predicate the door itself calls (identity.CanConvertAndPay). The platform roles pass every gate
// level and are refused by the door, so the flag says false for them.
func (g *tillPaymentGrant) HoldsPayment(ctx context.Context, platformUserID, orgID string, caller identity.StaffContext) (bool, error) {
	if !identity.CanConvertAndPay(caller) {
		return false, nil
	}
	for _, access := range g.accesses {
		held, err := g.gate.Holds(ctx, platformUserID, orgID, access)
		if err != nil || !held {
			return false, err
		}
	}
	return true, nil
}

// wirePaymentGrant registers the payment grant read behind the role gate's own answer. Both
// composition roots call it after the tops wiring, where the gate and the pos handler both exist.
func wirePaymentGrant(r httpx.Registrar, g *guards.Guards, gate *authz.RoleGate, posHandler *pos.Handler) error {
	reader, err := newTillPaymentGrant(gate)
	if err != nil {
		return err
	}
	posHandler.WithGrantReader(reader).RegisterGrantRoute(r, g)
	return nil
}

// paymentTrailRoutes are the two reads ruling 87 holds to money desk WORK: a payment's history and an
// invoice's payments. A caller holds the payment trail when the gate would admit them to both.
var paymentTrailRoutes = []struct{ method, path string }{
	{"GET", "/api/v1/ar/payments/{id}/history"},
	{"GET", "/api/v1/ar/invoices/{id}/payments"},
}

// paymentTrailReader answers billing.PaymentTrailReader over the role gate. The level lives in the
// document's annotation of those two routes, so the answer is the gate's own and moves with it; this
// wiring names no role and no level.
type paymentTrailReader struct {
	gate     *authz.RoleGate
	accesses []authz.RouteAccess
}

func newPaymentTrailReader(gate *authz.RoleGate) (*paymentTrailReader, error) {
	if gate == nil {
		return nil, errors.New("payment trail: the role gate is required")
	}
	p := &paymentTrailReader{gate: gate}
	for _, r := range paymentTrailRoutes {
		access, ok := authz.RouteAccessFor(r.method, r.path)
		if !ok {
			return nil, fmt.Errorf("payment trail: %s %s carries no staff access in the document", r.method, r.path)
		}
		p.accesses = append(p.accesses, access)
	}
	return p, nil
}

// HoldsPaymentTrail is true only when the gate would admit the caller to every trail route.
func (p *paymentTrailReader) HoldsPaymentTrail(ctx context.Context, platformUserID, orgID string) (bool, error) {
	for _, access := range p.accesses {
		held, err := p.gate.Holds(ctx, platformUserID, orgID, access)
		if err != nil || !held {
			return false, err
		}
	}
	return true, nil
}

// bindPaymentTrail binds the register's late payment trail reader to the role gate. Both composition
// roots call it beside wirePaymentGrant.
func bindPaymentTrail(money *moneySpine, gate *authz.RoleGate) error {
	reader, err := newPaymentTrailReader(gate)
	if err != nil {
		return err
	}
	money.paymentTrail.Bind(reader)
	return nil
}

// lateDiscountApprovalRoutes names the route whose gate is the late discount approval's wall (TM-2
// item 2): the money desk's manager exceptions run the AR write off's annotation (requireManager,
// money-desk manage), and the caller that gate admits is the caller who may approve an early
// payment discount outside its window. The role list is therefore the route's own, read from the
// loaded configuration, never restated here.
var lateDiscountApprovalRoutes = []struct{ method, path string }{
	{"POST", "/api/v1/ar/write-offs"},
}

// lateDiscountApprovalReader adapts the role gate onto the register's approval wall.
type lateDiscountApprovalReader struct {
	gate   *authz.RoleGate
	access authz.RouteAccess
}

func newLateDiscountApprovalReader(gate *authz.RoleGate) (*lateDiscountApprovalReader, error) {
	if gate == nil {
		return nil, errors.New("late discount approval: the role gate is required")
	}
	r := &lateDiscountApprovalReader{gate: gate}
	route := lateDiscountApprovalRoutes[0]
	access, ok := authz.RouteAccessFor(route.method, route.path)
	if !ok {
		return nil, fmt.Errorf("late discount approval: %s %s carries no staff access in the document", route.method, route.path)
	}
	r.access = access
	return r, nil
}

// HoldsLateDiscountApproval is the gate's own answer for the caller on the write off route's access.
func (r *lateDiscountApprovalReader) HoldsLateDiscountApproval(ctx context.Context, platformUserID, orgID string) (bool, error) {
	return r.gate.Holds(ctx, platformUserID, orgID, r.access)
}

// bindLateDiscountApprovals binds the register's late discount approval wall to the role gate.
// Both composition roots call it beside bindPaymentTrail.
func bindLateDiscountApprovals(tops *topsSpine, gate *authz.RoleGate) error {
	if tops == nil || tops.lateDiscountApprovals == nil {
		return nil
	}
	reader, err := newLateDiscountApprovalReader(gate)
	if err != nil {
		return err
	}
	tops.lateDiscountApprovals.Bind(reader)
	return nil
}
