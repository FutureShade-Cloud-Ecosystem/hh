//go:build integration

// postbill_gl_tx_wire_integration_test.go, the production adapters (w4cGLPoster, glRuleAdapter) over the real
// gl service, driven through a caller's transaction.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 \
//	     -run PostBillGLTxAdapters ./cmd/core/
package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

func TestIntegration_PostBillGLTxAdapters_RideTheCallersTransaction(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	svc := gl.NewService(gl.NewRepository(pool))
	rules, poster := glRuleAdapter{svc: svc}, w4cGLPoster{svc: svc}

	key := fmt.Sprintf("wire-gltx-%d", time.Now().UnixNano())
	count := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ap, err := rules.ResolveRuleTx(ctx, tx, "vendor.bill.posted", "ap", "")
	if err != nil || ap == "" {
		t.Fatalf("resolve through tx: %q %v", ap, err)
	}
	accrual, err := rules.ResolveRuleTx(ctx, tx, "vendor.bill.posted", "ap_accrual", "")
	if err != nil {
		t.Fatalf("resolve accrual through tx: %v", err)
	}
	id, err := poster.PostJournalTx(ctx, tx, purchasing.GLJournal{
		Source: "PO_RECEIPT", IdempotencyKey: key, Memo: "wire leg",
		Lines: []purchasing.GLLine{{AccountCode: accrual, DebitCents: 700}, {AccountCode: ap, CreditCents: 700}},
	})
	if err != nil || id == "" {
		t.Fatalf("post through tx: %q %v", id, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("a rolled back caller left %d journal(s)", n)
	}
}
