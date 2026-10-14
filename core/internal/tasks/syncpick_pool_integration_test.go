//go:build integration

// syncpick_pool_integration_test.go: the pool arithmetic of the locked record. A sync record holds the
// key ledger's lock connection (the ledger lets at most half the pool hold one), the task row's
// transaction, and the order call's own connection: three at the peak. recordSlotsFor caps the records in
// flight so a pool full of ledger holders and open records still has a connection for an order call to
// finish on. These legs push more concurrent records than the pool has connections and require every one
// to finish.
package tasks

import (
	"context"
	gosync "sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// connOrder is the recording order double that, when hold is set, takes a pooled connection for the
// length of ApplyPicks, as the real order module's transaction does.
type connOrder struct {
	*fakeYardOrder
	pool *pgxpool.Pool
	hold bool
}

func (c *connOrder) ApplyPicks(ctx context.Context, in YardPickChange) (YardPickResult, error) {
	if c.hold {
		// The order module reads before it takes its transaction connection: every record in flight is
		// already holding the task row's transaction when the first one asks for the order's.
		time.Sleep(30 * time.Millisecond)
		conn, err := c.pool.Acquire(ctx)
		if err != nil {
			return YardPickResult{}, err
		}
		defer conn.Release()
		time.Sleep(15 * time.Millisecond)
	}
	return c.fakeYardOrder.ApplyPicks(ctx, in)
}

func TestRecordSlotsFor(t *testing.T) {
	for n, want := range map[int32]int{3: 1, 4: 1, 5: 2, 6: 2, 8: 3, 10: 4, 20: 9} {
		if got := recordSlotsFor(n); got != want {
			t.Errorf("pool %d: %d record slots, want %d", n, got, want)
		}
		// The least connections left once the ledger holders (n/2) and the open records are counted.
		if free := int(n) - int(n)/2 - recordSlotsFor(n); free < 1 {
			t.Errorf("pool %d leaves %d connections for an order call", n, free)
		}
	}
}

func TestPickDoor_ManyConcurrentRecordsOnASmallPoolAllFinish(t *testing.T) {
	for _, conns := range []int32{3, 4, 6} {
		d := newPickDoorConns(t, conns)
		d.hold.hold = true
		const records = 16
		ids := make([]string, records)
		for i := range ids {
			ids[i] = d.task(d.branchA)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		var wg gosync.WaitGroup
		results := make([]string, records)
		for i := range ids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				resp, err := d.engine.Push(ctx, d.info, d.f, []apigen.SyncPushOperation{recordOp(uuid.NewString(), ids[i], pickLine(1))})
				if err != nil {
					results[i] = "push error: " + err.Error()
					return
				}
				results[i] = resp.Results[0].Status
			}(i)
		}
		wg.Wait()
		cancel()
		for i, r := range results {
			if r != "applied" {
				t.Fatalf("pool of %d: record %d did not finish applied: %q (a wedged pool shows as the 40s deadline)", conns, i, r)
			}
		}
	}
}

func TestPickDoor_APoolOfTwoRefusesARecordFastAndNeverHangs(t *testing.T) {
	d := newPickDoorConns(t, 2)
	id := d.task(d.branchA)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := d.engine.Push(ctx, d.info, d.f, []apigen.SyncPushOperation{recordOp(uuid.NewString(), id, pickLine(1))})
	if err == nil || ctx.Err() != nil {
		t.Fatalf("want a prompt operational refusal, got err %v ctx %v", err, ctx.Err())
	}
}
