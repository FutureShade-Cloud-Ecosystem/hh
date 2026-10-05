//go:build integration

// mapdoors_pool_integration_test.go is the pool 4 leg for the two map
// doors (HH-LOAD-1 LD-4, decision row 786): pool of four, three
// contending batches, the suite holding the fourth connection, every
// read and write inside a batch on the batch's own transaction. The
// readers the maps attach through join the frame's transaction, so all
// three finish; a reader that reaches for the pool instead is bitten by
// the stall (the last test), which is what proves the leg can fail.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5625/ld4_gate?sslmode=disable' \
//	  go test -tags integration ./internal/load/... -count=1 -run TestMapDoorsPool
package load

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runThreeContenders submits the same two item batch under three batch
// ids through the door on the small pool, one connection of the four
// held by the suite, and returns what each answered.
func runThreeContenders(t *testing.T, pool *pgxpool.Pool, door *Door, items []Item, timeout time.Duration, tag string) ([]BatchResult, []error) {
	t.Helper()
	ctx := context.Background()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	results := make([]BatchResult, 3)
	errs := make([]error, 3)
	var wg sync.WaitGroup
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			results[k], errs[k] = door.UpsertBatch(cctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
				BatchID: fmt.Sprintf("b-%s-%d", tag, k), Caller: testActorID, Items: append([]Item(nil), items...)})
		}(k)
	}
	wg.Wait()
	return results, errs
}

func TestMapDoorsPoolFourThreeContendersEveryCallOnTheBatchTransaction(t *testing.T) {
	mapSetup(t)
	pool := smallPool(t)
	sink := &countingAuditSink{}
	cases := []struct {
		name  string
		door  *Door
		items []Item
		want  map[string]string
	}{
		{"branches", NewAttachDoor(NewStore(pool), KindBranch, &BranchMapWriter{Branches: branches.BranchCodeReader{}}, sink),
			[]Item{mapItem("PB-1", fxBranchOneCode), mapItem("PB-2", fxBranchTwoCode)},
			map[string]string{"PB-1": fxBranchOneID, "PB-2": fxBranchTwoID}},
		{"users", NewAttachDoor(NewStore(pool), KindUser, &UserMapWriter{Staff: identity.StaffEmailReader{}}, sink),
			[]Item{userItem("PU-1", fxStaffOneEmail), userItem("PU-2", fxStaffTwoEmail)},
			map[string]string{"PU-1": fxStaffOneID, "PU-2": fxStaffTwoID}},
	}
	for _, c := range cases {
		results, errs := runThreeContenders(t, pool, c.door, c.items, 20*time.Second, "mp-"+c.name)
		attached := 0
		for k, err := range errs {
			if err != nil {
				t.Fatalf("%s contender %d: %v (a batch needed a connection beyond its transaction)", c.name, k, err)
			}
			for _, o := range results[k].Items {
				switch o.Status {
				case OutcomeAttached:
					attached++
				case OutcomeUnchanged, OutcomeUpdated:
				default:
					t.Fatalf("%s contender %d answered %+v", c.name, k, o)
				}
				if o.HHID != c.want[o.LegacyID] {
					t.Fatalf("%s %s bound to %q, want the pre existing row %q", c.name, o.LegacyID, o.HHID, c.want[o.LegacyID])
				}
			}
		}
		if attached != 2 {
			t.Fatalf("%s: %d attaches across the contenders, want exactly two (one per name)", c.name, attached)
		}
	}
}

// strayBranchReader reaches for the pool inside the batch transaction,
// the violation the leg exists to catch.
type strayBranchReader struct{ pool *pgxpool.Pool }

func (r strayBranchReader) BranchRowByCodeTx(ctx context.Context, tx pgx.Tx, code string) (string, bool, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`SELECT id::text FROM locations WHERE code = $1 AND loc_type = 'BRANCH' AND deleted_at IS NULL`, code).Scan(&id)
	if err != nil {
		return "", false, fmt.Errorf("stray pool read: %w", err)
	}
	return id, true, nil
}

func TestMapDoorsPoolFourBitesAReaderThatUsesThePool(t *testing.T) {
	mapSetup(t)
	pool := smallPool(t)
	door := NewAttachDoor(NewStore(pool), KindBranch, &BranchMapWriter{Branches: strayBranchReader{pool: pool}}, &countingAuditSink{})
	_, errs := runThreeContenders(t, pool, door, []Item{mapItem("PX-1", fxBranchOneCode)}, 3*time.Second, "mp-stray")
	for k, err := range errs {
		if err == nil {
			t.Fatalf("contender %d finished: the pool 4 leg missed a reader that uses the pool", k)
		}
	}
}
