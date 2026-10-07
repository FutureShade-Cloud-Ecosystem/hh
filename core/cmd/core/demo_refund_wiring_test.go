//go:build demo

package main

// The refund approval switch reaches the till's POS service on the demo
// build: the demo dealer's configuration (the limit off) is the one
// configuration the demo graph holds, so the POS service answers no
// approval needed for a refund above the default amount. PR 250's unit
// test built the service with the modified configuration directly and could
// not see the wiring; this one runs the real demoRegisterRoutes.

import "testing"

func TestDemoGraphPOSServiceNeedsNoRefundApproval(t *testing.T) {
	_, graph := rgDemoGateCase(t)
	roles := graph.tops.posSvc.RoleConfig()
	if roles == nil {
		t.Fatal("the demo graph's POS service holds no role configuration")
	}
	const aboveDefaultLimit = 100_000 // 1000.00, over the 250.00 default
	if roles.RefundRequiresApproval(aboveDefaultLimit) {
		t.Fatal("the demo graph's POS service still asks for an approver on a refund above 250.00: the demo role configuration did not reach it")
	}
}
