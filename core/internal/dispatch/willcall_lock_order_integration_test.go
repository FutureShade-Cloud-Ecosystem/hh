//go:build integration

// willcall_lock_order_integration_test.go, the lock order leg for will_call_pickups (UX-72 round 3).
//
// Every writer of the table takes the pickup row lock first and draws the one will_call counter
// second. A writer that drew first and let its UPDATE take the row would cycle against one that
// locks the row and then draws: a Pro Field confirm against a desk revoke of the same tag
// deadlocked (40P01) and the contractor's typed name confirm failed as a server error. The leg runs
// the two shapes together on one tag, many times, each in its own connection, both inside their
// transaction together, and expects one winner and one clean "no longer active", never an error.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestWillCallPickups_LockOrder_FieldConfirmAgainstDeskRevoke(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("will_call lock order leg: DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("database not reachable: %v", err)
	}
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	t.Cleanup(func() { _ = testkit.Cleanup(context.Background(), testkit.PgxPool(pool)) })

	var orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date, org_id, tenant_id)
		VALUES ('#SO-FXLOCKORDER-1', $1, $2, 'CONFIRMED', 'WILL_CALL', CURRENT_DATE, $3::uuid, $4::uuid) RETURNING id::text`,
		testkit.TenantAlphaID, testkit.BranchNorthAID, testkit.OrgNorthID, testkit.TenantAlphaID).Scan(&orderID); err != nil {
		t.Fatalf("seed will-call order: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM will_call_pickup_events WHERE pickup_id IN (SELECT id FROM will_call_pickups WHERE order_id = $1::uuid)`, orderID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM will_call_pickups WHERE order_id = $1::uuid`, orderID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1::uuid`, orderID)
	})

	repo := NewRepository(pool)
	const iterations = 25
	for i := 0; i < iterations; i++ {
		w, err := repo.InsertPickup(ctx, orderID, testkit.BranchNorthAID, fmt.Sprintf("lock-order-hash-%d", i), "", time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("iteration %d: mint: %v", i, err)
		}

		var wg sync.WaitGroup
		inside := make(chan struct{}) // the confirm holds the row lock
		var confirmWon, revokeWon bool
		var confirmErr, revokeErr error

		wg.Add(2)
		go func() { // the Pro Field shape: the row lock, then the draw
			defer wg.Done()
			confirmErr = func() error {
				tx, err := pool.Begin(ctx)
				if err != nil {
					return err
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '20s'`); err != nil {
					return err
				}
				var status string
				if err := tx.QueryRow(ctx, `SELECT status FROM will_call_pickups WHERE id = $1::uuid FOR UPDATE`, w.ID).Scan(&status); err != nil {
					return err
				}
				close(inside)
				// Both writers are inside their transaction together: the desk has had time to
				// start its draw before this one draws.
				time.Sleep(150 * time.Millisecond)
				if status != "ACTIVE" {
					return nil
				}
				seq, err := allocateSyncSeqTx(ctx, tx, willCallSyncScope)
				if err != nil {
					return err
				}
				tag, err := tx.Exec(ctx, `UPDATE will_call_pickups SET status = 'CONFIRMED', signature_ref = 'lock-order-leg', collected_by = 'lock order leg', confirmed_at = now(), seq = $2
					WHERE id = $1::uuid AND status = 'ACTIVE'`, w.ID, seq)
				if err != nil {
					return err
				}
				confirmWon = tag.RowsAffected() == 1
				return tx.Commit(ctx)
			}()
			select {
			case <-inside:
			default:
				close(inside) // the lock itself failed: do not strand the desk
			}
		}()
		go func() { // the desk: RevokePickup
			defer wg.Done()
			<-inside
			revokeWon, revokeErr = repo.RevokePickup(ctx, w.ID, time.Now())
		}()
		wg.Wait()

		for _, e := range []error{confirmErr, revokeErr} {
			var pgErr *pgconn.PgError
			if errors.As(e, &pgErr) && pgErr.Code == "40P01" {
				t.Fatalf("iteration %d: deadlock detected (40P01): confirm err %v, revoke err %v", i, confirmErr, revokeErr)
			}
		}
		if confirmErr != nil || revokeErr != nil {
			t.Fatalf("iteration %d: a server error: confirm err %v, revoke err %v", i, confirmErr, revokeErr)
		}
		if confirmWon == revokeWon {
			t.Fatalf("iteration %d: want exactly one winner, confirm won %v, revoke won %v", i, confirmWon, revokeWon)
		}
	}
}
