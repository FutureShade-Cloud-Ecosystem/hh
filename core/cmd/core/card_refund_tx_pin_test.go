package main

// card_refund_tx_pin_test.go pins the production adapter to the transaction
// path, not just the pos rig's mirror of it: posCardRefunderAdapter.RefundCard
// over a REAL payment.Store whose pool is nil (so any pool read refuses with
// ErrNoConfig before any query, and a successful answer is itself the proof
// no pool use is reachable), riding a fake pgx.Tx that serves the sealed
// config row and counts the queries that rode it. The pos Return transaction
// holds a connection and the per-sale advisory lock while the reversal runs;
// an adapter back on the pool-shaped Refund would ask the nil pool and fail
// here, which is the stall this seam exists to keep out.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5"
)

// cardRailPinRow is the one row the config read scans: a sealed creds blob
// the pin's own vault sealed, so the real Store.activeConfigTx decrypts it
// exactly as production would. The columns follow activeConfigQuery's order.
type cardRailPinRow struct {
	ct []byte
	kv int
}

func (r cardRailPinRow) Scan(dest ...any) error {
	vals := []any{"run_payments", "MID-CARDRAIL-PIN", "CAD", "", int64(0), false, "", false, r.ct, r.kv}
	if len(dest) != len(vals) {
		return errors.New("card rail pin: unexpected payment_config column count")
	}
	for i, d := range dest {
		switch out := d.(type) {
		case *string:
			*out = vals[i].(string)
		case *int64:
			*out = vals[i].(int64)
		case *int:
			*out = vals[i].(int)
		case *bool:
			*out = vals[i].(bool)
		case *[]byte:
			*out = vals[i].([]byte)
		default:
			return fmt.Errorf("card rail pin: unsupported scan dest %T", d)
		}
	}
	return nil
}

// cardRailPinTx is the open transaction the pin rides. Embedding pgx.Tx
// satisfies the interface; only QueryRow is ever called on this path, and
// every call is counted: the config read must ride the transaction, exactly
// once.
type cardRailPinTx struct {
	pgx.Tx
	row     cardRailPinRow
	queries []string
}

func (t *cardRailPinTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	t.queries = append(t.queries, sql)
	return t.row
}

// The adapter answers the reversal and drives the config read through the
// caller's transaction: one query on the tx, none anywhere else (the pool is
// nil and cannot answer, so the passing answer is the pool-free proof).
func TestPosCardRefunderAdapter_RefundCard_RidesTheTransaction(t *testing.T) {
	vault, err := payment.NewVault(strings.Repeat("ab", 32))
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
	adapter := posCardRefunderAdapter{svc: payment.NewService(payment.NewStore(nil, vault), payment.RunPaymentsStub{})}
	tx := &cardRailPinTx{row: cardRailPinRow{ct: ct, kv: kv}}

	ref, err := adapter.RefundCard(context.Background(), tx, "rp_sim_captured", 12500, "RET-1", "pin-key")
	if err != nil {
		t.Fatalf("RefundCard: %v", err)
	}
	if !strings.HasPrefix(ref, "rp_ref_") {
		t.Fatalf("the reversal ref = %q, want the stub's rp_ref_ shape", ref)
	}
	if len(tx.queries) != 1 || !strings.Contains(tx.queries[0], "payment_config") {
		t.Fatalf("the config read must ride the transaction exactly once, got %d queries: %q", len(tx.queries), tx.queries)
	}
}
