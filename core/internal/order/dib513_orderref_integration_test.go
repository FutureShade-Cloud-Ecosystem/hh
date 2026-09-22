//go:build integration

// DIB-513, an order number resolves in BOTH of the spellings the database actually holds.
//
// THE DEFECT, as it was found. ResolveOrderID forced a leading '#' onto whatever it was handed and
// matched that single string. The orders table does not agree with it: the seeded demo alone holds
// '#SO-DPA3-CRIT' and 'SO-2026-0001', because the number is minted by several writers and only some
// of them prefix it. So an order whose stored order_no carries no '#' was unreachable BY EITHER
// SPELLING, proved against a live stack through TanyaAI, as the caller who owns the order:
//
//	"SO-2026-0001"   -> "I can't find an order numbered SO-2026-0001."
//	"#SO-2026-0001"  -> "I can't find an order numbered #SO-2026-0001."
//
// A person reading a number off a printed ticket cannot know which form the database happens to
// hold, and "no such order" is the one answer that was never true.
//
// This is an INTEGRATION test on purpose: the whole defect lived in one SQL predicate, so a fake
// store would have proved nothing about it.
package order

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedRefOrder(t *testing.T, pool *pgxpool.Pool, orderNo string) string {
	t.Helper()
	ctx := context.Background()
	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, $2) RETURNING id::text`,
		fmt.Sprintf("D513-%d", time.Now().UnixNano()), "DIB-513 ref").Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var ord string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 12345) RETURNING id::text`,
		orderNo, cust, trentonBranch).Scan(&ord); err != nil {
		t.Fatalf("seed order %q: %v", orderNo, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1::uuid`, ord)
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, cust)
	})
	return ord
}

func TestIntegration_DIB513_ResolveOrderID_AcceptsBothSpellings(t *testing.T) {
	pool := intPool(t)
	repo := &Repository{pool: pool}
	ctx := context.Background()
	nano := time.Now().UnixNano()

	// One order stored WITHOUT the '#', one WITH it. Both must answer to both spellings.
	bareStored := fmt.Sprintf("SO-D513-BARE-%d", nano)
	hashStored := fmt.Sprintf("#SO-D513-HASH-%d", nano)
	bareID := seedRefOrder(t, pool, bareStored)
	hashID := seedRefOrder(t, pool, hashStored)

	cases := []struct {
		typed string
		want  string
		why   string
	}{
		{bareStored, bareID, "the bare number, stored bare, the case that was broken"},
		{"#" + bareStored, bareID, "the same order asked for with a '#' the database does not hold"},
		{hashStored, hashID, "the hashed number, stored hashed"},
		{hashStored[1:], hashID, "the same order asked for without its stored '#', the old behaviour, kept"},
	}
	for _, tc := range cases {
		got, err := repo.ResolveOrderID(ctx, tc.typed, true, nil)
		if err != nil {
			t.Fatalf("%s: ResolveOrderID(%q) = %v, want it to resolve", tc.why, tc.typed, err)
		}
		if got != tc.want {
			t.Fatalf("%s: ResolveOrderID(%q) = %s, want %s", tc.why, tc.typed, got, tc.want)
		}
	}
}

func TestIntegration_DIB513_ResolveOrderID_StillFencesByBranchAndStillMisses(t *testing.T) {
	// Widening the SPELLING must not widen the REACH. Both of these were true before the change and
	// have to stay true: a number nobody holds is not found, and a number in a branch this caller
	// cannot see is not found either, the fence, not the prefix, is what decides.
	pool := intPool(t)
	repo := &Repository{pool: pool}
	ctx := context.Background()
	stored := fmt.Sprintf("SO-D513-FENCE-%d", time.Now().UnixNano())
	seedRefOrder(t, pool, stored)

	for _, typed := range []string{stored, "#" + stored} {
		// A scoped caller granted a DIFFERENT branch reaches nothing.
		if _, err := repo.ResolveOrderID(ctx, typed, false, []string{"5d1b3f6e-0000-4000-9000-000000000002"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-branch %q resolved (err=%v), the branch fence must still decide", typed, err)
		}
		// A scoped caller with no grants at all reaches nothing.
		if _, err := repo.ResolveOrderID(ctx, typed, false, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ungranted %q resolved (err=%v)", typed, err)
		}
	}
	// A number that genuinely does not exist is still not found, in either spelling.
	for _, typed := range []string{"SO-D513-NOPE", "#SO-D513-NOPE"} {
		if _, err := repo.ResolveOrderID(ctx, typed, true, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("nonexistent %q resolved (err=%v)", typed, err)
		}
	}
}
