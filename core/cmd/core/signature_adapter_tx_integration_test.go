//go:build integration

package main

// signature_adapter_tx_integration_test.go pins the PRODUCTION adapter's transaction method.
//
// Every other leg of the pickup confirm's signature capture (core/internal/dispatch) drives the seam
// through a test-only copy of dispatchSignatureAdapter, because a test in another package cannot import
// package main. Nothing there would notice if the real adapter's CaptureTypedTx were changed to call the
// pool capture again: the pool-size starvation and the orphan rows would come back in production while
// every committed leg stayed green. This leg closes that gap: it builds the world exactly as the
// composition root does (compose: the real pos service with the real typed name provider and the real
// audit service), reaches the capture through the real dispatchSignatureAdapter, and proves that the
// signature row and its audit row ride the CALLER's transaction: visible inside it, invisible outside
// it before the commit, gone after a rollback, present exactly once after a commit.
//
// It reads DATABASE_URL (a migrated and seeded database) and skips without one.

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestDispatchSignatureAdapter_CaptureTypedTx_RidesTheCallersTransaction(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("signature adapter tx leg: DATABASE_URL not set (it needs a migrated, seeded database and skips without one)")
	}
	bg := context.Background()
	pool, err := pgxpool.New(bg, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(bg); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}
	var actor string
	if err := pool.QueryRow(bg, `SELECT id::text FROM users ORDER BY created_at LIMIT 1`).Scan(&actor); err != nil {
		t.Fatalf("this leg needs a seeded users row for the capture's actor: %v", err)
	}

	w, err := compose(pool, slog.Default(), "")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	adapter := dispatchSignatureAdapter{svc: w.pos}
	ctx := identity.ContextWithStaffUserID(bg, actor)

	counts := func(refID string, fromTx func(string, ...any) (int, error)) (sigs, audits int) {
		var err error
		if sigs, err = fromTx(`SELECT count(*) FROM pos_signature WHERE ref_type = $1 AND ref_id = $2`, dispatch.RefTypeWillCall, refID); err != nil {
			t.Fatalf("count signatures: %v", err)
		}
		if audits, err = fromTx(`SELECT count(*) FROM audit_log WHERE action = $1 AND entity_id = $2`, audit.ActSignatureCapture, refID); err != nil {
			t.Fatalf("count audit rows: %v", err)
		}
		return sigs, audits
	}
	poolCounts := func(refID string) (int, int) {
		return counts(refID, func(q string, a ...any) (int, error) {
			var n int
			err := pool.QueryRow(bg, q, a...).Scan(&n)
			return n, err
		})
	}

	attest := func(refID string) dispatch.TypedAttestation {
		return dispatch.TypedAttestation{
			Purpose: dispatch.SignaturePurposePOD, RefType: dispatch.RefTypeWillCall, RefID: refID,
			TypedName: "Signature Adapter Leg", Affirmed: true,
		}
	}
	newRef := func() string {
		var id string
		if err := pool.QueryRow(bg, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
			t.Fatalf("uuid: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM pos_signature WHERE ref_type = $1 AND ref_id = $2`, dispatch.RefTypeWillCall, id)
			_, _ = pool.Exec(context.Background(), `DELETE FROM audit_log WHERE entity_id = $1`, id)
		})
		return id
	}

	// 1. Capture inside a transaction, then roll it back: the rows were never visible outside it and
	// nothing remains after the rollback.
	refRolled := newRef()
	tx, err := pool.Begin(bg)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	ref, err := adapter.CaptureTypedTx(ctx, tx, attest(refRolled))
	if err != nil {
		_ = tx.Rollback(bg)
		t.Fatalf("CaptureTypedTx: %v", err)
	}
	if ref == "" {
		_ = tx.Rollback(bg)
		t.Fatalf("CaptureTypedTx answered an empty signature reference")
	}
	inTxSigs, inTxAudits := counts(refRolled, func(q string, a ...any) (int, error) {
		var n int
		err := tx.QueryRow(bg, q, a...).Scan(&n)
		return n, err
	})
	if inTxSigs != 1 || inTxAudits != 1 {
		_ = tx.Rollback(bg)
		t.Fatalf("inside the caller's transaction want 1 signature row and 1 audit row, got %d and %d (the capture did not write on the transaction)", inTxSigs, inTxAudits)
	}
	if s, a := poolCounts(refRolled); s != 0 || a != 0 {
		_ = tx.Rollback(bg)
		t.Fatalf("before the commit the pool must see no signature and no audit row, got %d and %d (the capture committed through the pool, outside the caller's transaction)", s, a)
	}
	if err := tx.Rollback(bg); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if s, a := poolCounts(refRolled); s != 0 || a != 0 {
		t.Fatalf("after the rollback want no signature and no audit row, got %d and %d (orphans)", s, a)
	}

	// 2. Capture inside a transaction and commit: exactly one of each.
	refCommitted := newRef()
	tx2, err := pool.Begin(bg)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := adapter.CaptureTypedTx(ctx, tx2, attest(refCommitted)); err != nil {
		_ = tx2.Rollback(bg)
		t.Fatalf("CaptureTypedTx: %v", err)
	}
	if err := tx2.Commit(bg); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if s, a := poolCounts(refCommitted); s != 1 || a != 1 {
		t.Fatalf("after the commit want exactly 1 signature row and 1 audit row, got %d and %d", s, a)
	}
}
