//go:build integration

package settings

// box_kind against a real database: the write is live at the next read, a
// malformed kind is refused before any write, and the row the test touched is
// restored, per this package's convention (the key is new, so the restore is
// almost always a delete).

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIntegration_BoxKind_WriteIsLiveAtTheNextRead(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var before []byte
	hadRow := !errors.Is(pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&before), pgx.ErrNoRows)
	t.Cleanup(func() {
		if hadRow {
			_, _ = pool.Exec(ctx, `UPDATE system_settings SET value = $2::jsonb WHERE key = $1`, KeyBoxKind, string(before))
		} else {
			_, _ = pool.Exec(ctx, `DELETE FROM system_settings WHERE key = $1`, KeyBoxKind)
		}
	})

	if err := WriteBoxKindTx(ctx, pool, "not a kind!"); err == nil {
		t.Fatal("WriteBoxKindTx must refuse a malformed kind on a real database too")
	}
	var after []byte
	if err := pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&after); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("the refused write must have touched nothing, found value=%q err=%v", after, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := WriteBoxKindTx(ctx, tx, "finance"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("WriteBoxKindTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	kind, err := ReadBoxKind(ctx, pool)
	if err != nil {
		t.Fatalf("ReadBoxKind: %v", err)
	}
	if kind != "finance" {
		t.Fatalf("the write must be live at the next read, got %q", kind)
	}
}

func TestIntegration_BoxKind_AbsentAnswersEmpty(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var found bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_settings WHERE key = $1)`, KeyBoxKind).Scan(&found); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if found {
		t.Skip("box_kind is set on this database; the absent leg needs it absent")
	}
	kind, err := ReadBoxKind(ctx, pool)
	if err != nil {
		t.Fatalf("ReadBoxKind on an absent key must answer empty, not error: %v", err)
	}
	if kind != "" {
		t.Fatalf("an absent key must answer empty, got %q", kind)
	}
}
