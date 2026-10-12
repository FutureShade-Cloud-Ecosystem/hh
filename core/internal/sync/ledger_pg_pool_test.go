package sync

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// lazyPool returns a pool of the given size that never dials (nothing here
// queries it).
func lazyPool(t *testing.T, conns int) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(fmt.Sprintf("postgres://u:p@127.0.0.1:1/db?sslmode=disable&pool_max_conns=%d", conns))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// A lock holder needs one connection for the lock and one for the store, so
// a pool of one wedges every keyed push. The constructor refuses it at boot,
// loudly, rather than leave the first push to hang.
func TestPgKeyLedgerRefusesAPoolOfOneConnection(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("NewPgKeyLedger accepted a pool of one connection")
		}
		if msg, _ := r.(string); msg == "" {
			t.Fatalf("panic %v is not the boot refusal message", r)
		}
	}()
	NewPgKeyLedger(lazyPool(t, 1))
}

func TestPgKeyLedgerHolderCapIsHalfThePool(t *testing.T) {
	for conns, want := range map[int]int{2: 1, 3: 1, 4: 2, 8: 4} {
		if got := cap(NewPgKeyLedger(lazyPool(t, conns)).locks); got != want {
			t.Errorf("pool %d: %d lock holders, want %d", conns, got, want)
		}
	}
	if NewPgKeyLedger(nil) == nil {
		t.Fatalf("a nil pool (the composition tests) must still build")
	}
}
