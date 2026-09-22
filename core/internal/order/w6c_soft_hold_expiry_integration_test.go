//go:build integration

// w6c_soft_hold_expiry_integration_test.go, DIB-422 item 4, against real reservation rows.
//
// The projection's whole value is that it distinguishes four outcomes a quote's soft hold can reach,
// and gets the DIFFERENCE between them right. A count of "non-active holds" would pass a shallower
// test and be wrong in the one case that matters most: a CONSUMED hold means the quote converted and
// the goods shipped, and reporting that as "your hold expired" would put an alarming banner on the
// single happiest outcome in the system.
package order

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// w6cSeedSoftHold writes one quote-sourced SOFT hold in a chosen terminal state.
func w6cSeedSoftHold(t *testing.T, pool *pgxpool.Pool, quoteID, status string, qty float64) string {
	t.Helper()
	ctx := context.Background()
	var closedAt any
	if status != ResActive {
		closedAt = time.Now().Add(-time.Hour)
	}
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO stock_reservations (product_id, location_id, branch_id, qty_base, hold_mode,
		                                res_type, status, source_type, source_ref_id, expires_at, closed_at)
		SELECT $1::uuid, l.id, l.id, $2, 'SOFT', 'QUOTE', $3, 'quote', $4::uuid,
		       now() - interval '2 days', $5
		  FROM locations l WHERE l.id = $6::uuid
		RETURNING id::text`,
		w5aProduct, qty, status, quoteID, closedAt, trentonBranch).Scan(&id); err != nil {
		t.Fatalf("seed %s soft hold: %v", status, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM stock_reservations WHERE id = $1::uuid`, id)
	})
	return id
}

func TestIntegrationW6C_SoftHoldExpiry_SeparatesTheFourOutcomes(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	// A stand-in quote id. The projection keys on (source_type='quote', source_ref_id), and never
	// joins to the quotes table, which is the point: it reads holds, not quotes, so it works
	// identically whatever internal/quote does with its own rows.
	var quoteID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&quoteID); err != nil {
		t.Fatal(err)
	}

	w6cSeedSoftHold(t, pool, quoteID, ResActive, 5)
	w6cSeedSoftHold(t, pool, quoteID, ResActive, 7)
	w6cSeedSoftHold(t, pool, quoteID, ResExpired, 12)
	w6cSeedSoftHold(t, pool, quoteID, ResReleased, 3)
	w6cSeedSoftHold(t, pool, quoteID, ResConsumed, 99)

	got, err := repo.ExpiredSoftHolds(ctx, quoteID)
	if err != nil {
		t.Fatalf("projection: %v", err)
	}

	if got.ActiveCount != 2 {
		t.Errorf("active = %d, want 2, the banner says '2 lines are still held', not 'all gone'",
			got.ActiveCount)
	}
	if len(got.Expired) != 2 {
		t.Fatalf("expired = %d, want 2 (EXPIRED + RELEASED); got %+v", len(got.Expired), got.Expired)
	}
	if !got.AnyExpired {
		t.Error("AnyExpired must be true when something expired")
	}

	// THE CASE THIS TEST EXISTS FOR: a CONSUMED hold is the quote converting and the goods shipping.
	// It must never appear here. A naive "status <> ACTIVE" filter passes every other assertion in
	// this file and fails this one.
	for _, h := range got.Expired {
		if h.Status == ResConsumed {
			t.Fatal("a CONSUMED hold means the quote converted and the stock shipped, reporting it as " +
				"expired would put an alarm on the happiest outcome in the system")
		}
		if h.QtyBase == 99 {
			t.Fatal("the consumed hold leaked into the expired list by quantity")
		}
	}

	// The rows carry what a rep needs to ACT: a product and a quantity, not just a count.
	for _, h := range got.Expired {
		if h.ProductID == "" || h.QtyBase == 0 {
			t.Errorf("an expired hold must name its product and quantity; got %+v", h)
		}
		// closed_at is tied to status by the table's own CHECK, so every non-ACTIVE row has one.
		if h.EndedAt == "" {
			t.Errorf("an ended hold must say when it ended; got %+v", h)
		}
	}
}

func TestIntegrationW6C_SoftHoldExpiry_QuietWhenNothingHappened(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	var quoteID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&quoteID); err != nil {
		t.Fatal(err)
	}
	w6cSeedSoftHold(t, pool, quoteID, ResActive, 4)

	got, err := repo.ExpiredSoftHolds(ctx, quoteID)
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if got.AnyExpired || len(got.Expired) != 0 {
		t.Errorf("a quote whose holds all stand must say nothing: %+v", got)
	}
	if got.ActiveCount != 1 {
		t.Errorf("active = %d, want 1", got.ActiveCount)
	}

	// A quote with no holds at all is also quiet, and is NOT an error, most quotes in the tree
	// predate soft holds entirely.
	var otherID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	empty, err := repo.ExpiredSoftHolds(ctx, otherID)
	if err != nil {
		t.Fatalf("a quote with no holds must not error: %v", err)
	}
	if empty.AnyExpired || empty.ActiveCount != 0 {
		t.Errorf("want a silent empty status, got %+v", empty)
	}
	// Empty, never nil: a nil slice marshals to `null` and a surface iterating it in TypeScript gets
	// a runtime error on the one screen that has nothing to report.
	if empty.Expired == nil {
		t.Error("Expired must marshal as [] rather than null")
	}
}

// TestIntegrationW6C_SoftHoldExpiry_UnwiredStoreIsSilent, the degradation ruling. A quote screen
// must not lose its quote because a decoration is unavailable.
func TestIntegrationW6C_SoftHoldExpiry_UnwiredStoreIsSilent(t *testing.T) {
	svc := NewService(&fakeStore{})
	got, err := svc.ExpiredSoftHolds(context.Background(), "not-a-uuid")
	if err != nil {
		t.Fatalf("an incapable store must degrade silently, not error: %v", err)
	}
	if got.AnyExpired {
		t.Error("an unwired projection must report nothing rather than something")
	}
}
