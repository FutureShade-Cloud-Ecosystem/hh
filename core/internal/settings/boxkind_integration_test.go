//go:build integration

package settings

// The box kind mark's write path against a real transaction: the malformed
// kind refuses as malformed on a handle that is there, and the well shaped
// kind writes through it. The transaction is rolled back, so the gate
// database keeps the rows it had.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIntegration_WriteBoxKindOnARealTransaction(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	// The row's state before the refused write, snapshot: a refused write
	// must leave the mark EXACTLY as it was. On a finance seeded database
	// the mark is there and says finance (this package's gate database is
	// one), so absence is not the assertion; unchanged is.
	var before []byte
	hadRow := tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&before) == nil

	if err := WriteBoxKindTx(ctx, tx, "Finance Box"); err == nil {
		t.Fatal("a malformed kind must refuse on a real transaction")
	} else if !strings.Contains(err.Error(), "short lowercase word") {
		t.Fatalf("a malformed kind must refuse as malformed, got %v", err)
	}
	var value []byte
	afterErr := tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&value)
	if hadRow {
		if afterErr != nil {
			t.Fatalf("the refused write removed the mark: %v", afterErr)
		}
		if string(value) != string(before) {
			t.Fatalf("the refused write changed the mark: %q was %q", value, before)
		}
	} else if afterErr == nil {
		t.Fatalf("the refused write left a mark behind: %s", value)
	}
	if err := WriteBoxKindTx(ctx, tx, "finance"); err != nil {
		t.Fatalf("a well shaped kind must write through a real transaction: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&value); err != nil {
		t.Fatalf("the mark was not written: %v", err)
	}
}

// The write is live at the next read through the kept seam, and a refused
// write leaves the row exactly as it was. Carried over from the other
// branch's copy of this seam, whose callers needed the read back proved.
func TestIntegration_BoxKindWriteIsLiveAtTheNextRead(t *testing.T) {
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
	afterErr := pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, KeyBoxKind).Scan(&after)
	if hadRow {
		if afterErr != nil {
			t.Fatalf("the refused write removed the mark: %v", afterErr)
		}
		if string(after) != string(before) {
			t.Fatalf("the refused write changed the mark: %s", after)
		}
	} else if !errors.Is(afterErr, pgx.ErrNoRows) {
		t.Fatalf("the refused write must have touched nothing, found value=%q err=%v", after, afterErr)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteBoxKindTx(ctx, tx, "finance"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("WriteBoxKindTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	kind, err := NewDealerConfigReader(pool).BoxKind(ctx)
	if err != nil {
		t.Fatalf("BoxKind: %v", err)
	}
	if kind != "finance" {
		t.Fatalf("the write must be live at the next read, got %q", kind)
	}
}

// An absent key answers empty through the kept seam, never an error: the
// guard treats a database that cannot say finance as not finance. Carried
// over from the other branch's copy of this seam.
func TestIntegration_BoxKindAbsentAnswersEmpty(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	var found bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_settings WHERE key = $1)`, KeyBoxKind).Scan(&found); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if found {
		t.Skip("box_kind is set on this database; the absent leg needs it absent")
	}
	kind, err := NewDealerConfigReader(pool).BoxKind(ctx)
	if err != nil {
		t.Fatalf("BoxKind on an absent key must answer empty, not error: %v", err)
	}
	if kind != "" {
		t.Fatalf("an absent key must answer empty, got %q", kind)
	}
}
