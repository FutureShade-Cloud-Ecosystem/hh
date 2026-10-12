package sync

import (
	"context"
	"errors"
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
// a pool of one would wedge every keyed push. Wiring tests build the
// production wiring over a one connection pool on purpose (the card rail
// leg), so construction must not panic; the first keyed push fails fast
// instead of hanging, and serve refuses the pool at boot.
func TestPgKeyLedgerOnAPoolOfOneFailsFastAndNeverHangs(t *testing.T) {
	led := NewPgKeyLedger(lazyPool(t, 1))
	release, err := led.Lock(context.Background(), ScopeFilter{OrgID: "o"}, "scope", "k")
	if !errors.Is(err, ErrLedgerPoolTooSmall) || release != nil {
		t.Fatalf("Lock on a pool of one: release %v err %v, want ErrLedgerPoolTooSmall", release != nil, err)
	}
}

func TestRequireLedgerPoolRefusesFewerThanTwoConnections(t *testing.T) {
	if err := RequireLedgerPool(lazyPool(t, 1)); !errors.Is(err, ErrLedgerPoolTooSmall) {
		t.Fatalf("a pool of one: %v, want ErrLedgerPoolTooSmall", err)
	}
	for _, n := range []int{2, 3, 4} {
		if err := RequireLedgerPool(lazyPool(t, n)); err != nil {
			t.Fatalf("a pool of %d: %v", n, err)
		}
	}
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
