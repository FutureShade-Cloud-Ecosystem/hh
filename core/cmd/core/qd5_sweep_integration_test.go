//go:build integration

// qd5_sweep_integration_test.go, QD-5 review round 2 (P1-A): every QD-5 test leaves the shared gate
// database exactly as it found it. The module gate is read by later tests whose whole-database checks
// (the stock drift view, the outbox drain) fail on a stranger's leftovers, so each test that seeds a
// customer sweeps everything the action made in ONE transaction, in dependency order, and a statement
// that fails is the test's failure, not a silent skip.
package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd5Sweep removes the customer, the quote and everything the money action made for them: orders and
// lines, shipment groups, invoices and lines, payments and allocations, subledger rows, holds, audit
// rows, and every events_outbox row whose payload names one of those ids (scoped by id, never a
// blanket delete). The constraint triggers on shipment groups are deferred, so the whole sweep runs
// in one transaction, groups before invoices.
func qd5Sweep(t *testing.T, pool *pgxpool.Pool, customerID, quoteID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("sweep: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ids []string
	if err := tx.QueryRow(ctx, `
		SELECT array_remove(ARRAY[$1::text, $2::text], NULL)
		    || COALESCE((SELECT array_agg(id::text) FROM orders WHERE quote_id = $2::uuid), '{}')
		    || COALESCE((SELECT array_agg(id::text) FROM invoices WHERE customer_id = $1::uuid), '{}')
		    || COALESCE((SELECT array_agg(id::text) FROM payments WHERE customer_id = $1::uuid), '{}')
		    || COALESCE((SELECT array_agg(id::text) FROM shipment_groups WHERE order_id IN (SELECT id FROM orders WHERE quote_id = $2::uuid)), '{}')`,
		customerID, quoteID).Scan(&ids); err != nil {
		t.Errorf("sweep: collect ids: %v", err)
		return
	}
	// Placeholders: $1 the customer, $2 the quote, $3 the id list. A statement names only the ones it
	// uses, so each gets exactly its own arguments.
	type stmt struct {
		sql  string
		args []any
	}
	c, q, all := []any{customerID}, []any{quoteID}, []any{ids}
	for _, s := range []stmt{
		{`DELETE FROM events_outbox WHERE EXISTS (SELECT 1 FROM unnest($1::text[]) x WHERE events_outbox.payload::text LIKE '%' || x || '%')`, all},
		{`DELETE FROM audit_log WHERE entity_id::text = ANY($1::text[])`, all},
		{`DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, c},
		{`DELETE FROM payments WHERE customer_id = $1::uuid`, c},
		{`DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, c},
		{`DELETE FROM shipment_group_lines WHERE shipment_group_id IN (SELECT id FROM shipment_groups WHERE order_id IN (SELECT id FROM orders WHERE quote_id = $1::uuid))`, q},
		{`DELETE FROM shipment_groups WHERE order_id IN (SELECT id FROM orders WHERE quote_id = $1::uuid)`, q},
		{`DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, c},
		{`DELETE FROM invoices WHERE customer_id = $1::uuid`, c},
		{`DELETE FROM stock_reservations WHERE source_ref_id IN (SELECT id FROM orders WHERE quote_id = $1::uuid) OR source_ref_id = $1::uuid`, q},
		{`DELETE FROM quote_accept_provenance WHERE quote_id = $1::uuid`, q},
		{`DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE quote_id = $1::uuid)`, q},
		{`DELETE FROM orders WHERE quote_id = $1::uuid`, q},
		{`DELETE FROM quote_lines WHERE quote_id = $1::uuid`, q},
		{`DELETE FROM quotes WHERE id = $1::uuid`, q},
		{`DELETE FROM credit_holds WHERE customer_id = $1::uuid`, c},
		{`DELETE FROM customers WHERE id = $1::uuid`, c},
	} {
		if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
			t.Errorf("sweep: %s: %v", s.sql, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("sweep: commit: %v", err)
	}
}

// qd5KeepStockRow snapshots every stock_balances row of the product now and registers a cleanup that
// puts exactly those rows back (and removes any the test made), so a test that raises stock for a hold
// or a hand over leaves the balance projection equal to the ledger it never wrote to. Registered
// before the test changes anything: cleanups run last in first out, so the oldest snapshot wins.
func qd5KeepStockRow(t *testing.T, pool *pgxpool.Pool, productID string) {
	t.Helper()
	ctx := context.Background()
	var snap []byte
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(jsonb_agg(to_jsonb(b)), '[]'::jsonb) FROM stock_balances b WHERE product_id = $1::uuid`,
		productID).Scan(&snap); err != nil {
		t.Fatalf("snapshot stock balances: %v", err)
	}
	t.Cleanup(func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Errorf("restore stock: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `DELETE FROM stock_balances WHERE product_id = $1::uuid`, productID); err != nil {
			t.Errorf("restore stock: %v", err)
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO stock_balances SELECT * FROM jsonb_populate_recordset(null::stock_balances, $1::jsonb)`, snap); err != nil {
			t.Errorf("restore stock: %v", err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("restore stock: %v", err)
		}
	})
}

// qd5KeepSequences snapshots scope_sequences (the number counters the writers advance for quote,
// invoice and payment numbers) and puts them back, so the counters do not drift run on run.
func qd5KeepSequences(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var snap []byte
	if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(s)), '[]'::jsonb) FROM scope_sequences s`).Scan(&snap); err != nil {
		t.Fatalf("snapshot sequences: %v", err)
	}
	t.Cleanup(func() {
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM scope_sequences`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO scope_sequences SELECT * FROM jsonb_populate_recordset(null::scope_sequences, $1::jsonb)`, snap)
			return err
		})
		if err != nil {
			t.Errorf("restore sequences: %v", err)
		}
	})
}
