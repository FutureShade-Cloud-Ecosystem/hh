//go:build integration

// willcall_confirm_poolsig_integration_test.go, the guard for the callers that must KEEP the pool.
//
// The pickups sync fix gives the confirm-in-transaction caller a transaction-taking capture, but the
// public will-call confirm (ConfirmWillCall) has NO transaction of its own: its capture is a
// standalone pool write followed by a standalone pickup update, exactly as before. This leg drives
// that confirm through the REAL pos provider (wired as the composition root wires it, the production
// adapter lives in package main and a test cannot import it) and asserts the pool capture still
// works and still lands its two rows: one signature, one audit. It passes on the unfixed tree and
// must keep passing after it, so a tx-taking refactor cannot quietly reroute the no-transaction
// callers. The leg runs against whatever DATABASE_URL it is given and skips without one; it pins no
// port and no database name.
package dispatch

import (
	"context"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

func TestWillCallConfirm_PoolCaptureStillLandsBothRows(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("will-call pool capture guard: DATABASE_URL not set (the leg needs its own throwaway database and skips politely without it)")
	}
	pool := intPool(t)
	ctx := globalCtx(t, pool)

	// The real capturer over the real pool, the composition root's wiring modulo the vault: a
	// typed-name provider bound lazily through pos.Service at the capture call.
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	posSvc := pos.NewService(pos.NewRepository(pool)).
		WithSignatureProvider(pos.NewTypedNameSignatureProvider(nil)).
		WithAudit(auditSvc)
	fulfil := &recordingFulfiller{}
	svc := NewService(NewRepository(pool)).
		WithOrders(testOrders{pool: pool}).
		WithSignatures(sigTxLegCapturer{svc: posSvc}).
		WithFulfiller(fulfil)

	orderID := seedOrder(t, pool, branchTRT, "WILL_CALL", "READY")
	minted, err := svc.MintWillCall(ctx, orderID, 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_type = 'signature' AND entity_id = $1`, minted.ID)
		_, _ = pool.Exec(c, `DELETE FROM pos_signature WHERE ref_type = $1 AND ref_id = $2`, RefTypeWillCall, minted.ID)
	})

	confirmed, err := svc.ConfirmWillCall(context.Background(), minted.Token, "Pool Caller Leg", true)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.Status != "CONFIRMED" || confirmed.CollectedBy != "Pool Caller Leg" {
		t.Fatalf("the confirmation should be recorded: %+v", confirmed)
	}

	var sigRows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_signature WHERE ref_type = $1 AND ref_id = $2`, RefTypeWillCall, minted.ID).Scan(&sigRows); err != nil {
		t.Fatalf("count signature rows: %v", err)
	}
	if sigRows != 1 {
		t.Fatalf("the no-transaction confirm must still land exactly one signature row, found %d", sigRows)
	}
	var auditRows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND entity_type = 'signature' AND entity_id = $2`,
		audit.ActSignatureCapture, minted.ID).Scan(&auditRows); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditRows != 1 {
		t.Fatalf("the no-transaction confirm must still land exactly one signature audit row, found %d", auditRows)
	}
}
