package main

// card_charge_tx_pin_test.go pins the production adapter's CHARGE side to
// the transaction path, the card_refund_tx_pin_test.go mirror: cardCharger
// .Charge over a REAL payment.Store whose pool is nil (so any pool read
// refuses with ErrNoConfig before any query, and a successful answer is
// itself the proof no pool use is reachable), riding the same fake pgx.Tx
// the refund pin rides, which serves the sealed config row and counts the
// queries that rode it: exactly one payment_config read on the tx. The pos
// Sale transaction holds a pool connection for the whole atomic ring while
// the tender's charge runs; an adapter back on the pool-shaped Charge would
// ask the nil pool and fail here, which is the stall this seam exists to
// keep out.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
)

// The adapter answers the charge and drives the config read through the
// caller's transaction: one query on the tx, none anywhere else (the pool
// is nil and cannot answer, so the passing answer is the pool-free proof).
// The doubles are the refund pin's cardRailPinRow and cardRailPinTx: the
// same one config row, the same query count, the charge side of the same
// seam discipline.
func TestCardChargerAdapter_Charge_RidesTheTransaction(t *testing.T) {
	vault, err := payment.NewVault(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	plain, err := json.Marshal(map[string]string{"api_key": "pin-only-not-a-secret"})
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	ct, kv, err := vault.Encrypt(plain)
	if err != nil {
		t.Fatalf("seal creds: %v", err)
	}
	adapter := cardChargerAdapter{svc: payment.NewService(payment.NewStore(nil, vault), payment.RunPaymentsStub{})}
	tx := &cardRailPinTx{row: cardRailPinRow{ct: ct, kv: kv}}

	rcpt, err := adapter.Charge(context.Background(), tx, 12500, "tok_pin_charge", "pin-key")
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if !strings.HasPrefix(rcpt.ProcessorRef, "rp_sim_") {
		t.Fatalf("the capture ref = %q, want the stub's rp_sim_ shape", rcpt.ProcessorRef)
	}
	if len(tx.queries) != 1 || !strings.Contains(tx.queries[0], "payment_config") {
		t.Fatalf("the config read must ride the transaction exactly once, got %d queries: %q", len(tx.queries), tx.queries)
	}
}
