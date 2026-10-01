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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
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
