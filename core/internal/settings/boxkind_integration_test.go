//go:build integration

package settings

// The box kind mark's write path against a real transaction: the malformed
// kind refuses as malformed on a handle that is there, and the well shaped
// kind writes through it. The transaction is rolled back, so the gate
// database keeps the rows it had.

import (
	"context"
	"strings"
	"testing"
)

func TestIntegration_WriteBoxKindOnARealTransaction(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	if err := WriteBoxKindTx(ctx, tx, "Finance Box"); err == nil {
		t.Fatal("a malformed kind must refuse on a real transaction")
	} else if !strings.Contains(err.Error(), "short lowercase word") {
		t.Fatalf("a malformed kind must refuse as malformed, got %v", err)
	}
	var value []byte
	if err := tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&value); err == nil {
		t.Fatalf("the refused write left the mark in place: %s", value)
	}
	if err := WriteBoxKindTx(ctx, tx, "finance"); err != nil {
		t.Fatalf("a well shaped kind must write through a real transaction: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&value); err != nil {
		t.Fatalf("the mark was not written: %v", err)
	}
}
