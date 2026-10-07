package main

// The default graph keeps the dealer's refund approval limit: the till's
// POS service is built with the configuration the composition root passes
// to wireTops, and the embedded default asks for an approver above 250.00.

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestDefaultTopsPOSServiceRequiresRefundApproval(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	roleCfg := mustRoleConfigForWire()
	money := wireMoney(mux, nil, sg.Guards, nil, "", roleCfg)
	ops := wireOperations(mux, nil, sg.Guards, log, money, "test-ticket-key", nil)
	tops := wireTops(mux, nil, sg.Guards, log, money, ops, config.Load(), roleCfg, buildSmsBelt(nil, log))
	if tops.posSvc.RoleConfig() != roleCfg {
		t.Fatal("wireTops built the POS service with a role configuration other than the one it was handed")
	}
	if !tops.posSvc.RoleConfig().RefundRequiresApproval(100_000) {
		t.Fatal("the default graph's POS service no longer asks for an approver on a refund above 250.00")
	}
}
