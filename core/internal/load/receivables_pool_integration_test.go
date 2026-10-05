//go:build integration

// receivables_pool_integration_test.go is the receivables door's pool
// leg (HH-LOAD-1 LD-11, decision row 786): the door opens a transaction
// that holds one connection from claim to finish, every read and write
// inside it goes through that transaction, and nothing inside it asks
// the pool for a second connection. The proof is a pool of four and six
// concurrent submitters in the shapes that matter: batches over the same
// customers, batches over different customers, and two submitters of
// one batch id. A batch that took a second connection mid flight would
// stall here (four holders, nobody able to finish), and each submission
// carries a deadline so a stall fails with the deadline named instead of
// hanging the suite. A contended answer (ErrBatchContended) is the
// retryable batch status, so the leg resubmits those whole, as a caller
// does, and counts them.
//
// Beyond liveness the leg proves the money: the per customer and the
// total receivables equal the fixture to the cent, and every customer's
// ledger reads in date order with true running balances although the
// batches interleaved and carried older items than one another.
//
// Run (the throwaway database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5627/ld11_gate?sslmode=disable' \
//	  go test -tags integration ./internal/load/... -count=1 -run TestReceivablesSmallPool
package load_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

const (
	ld11PoolConns   = 4
	ld11PoolTimeout = 30 * time.Second
	ld11PoolBudget  = 120 * time.Second
	ld11PoolRetries = 6
)

func TestReceivablesSmallPoolServesConcurrentBatchesToTheCent(t *testing.T) {
	e := setupLD11(t)
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("small pool config: %v", err)
	}
	cfg.MaxConns = ld11PoolConns
	small, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("small pool: %v", err)
	}
	t.Cleanup(small.Close)
	audit := &ld11AuditSink{}
	door := load.NewReceivablesDoor(load.NewStore(small), billing.NewReceivablesLoadWriter(), audit)

	// Six submissions, five distinct batch ids. Batches 0 to 2 share
	// customer one (and carry interleaving dates, the later batches
	// older than the earlier ones), batch 3 carries customer two, batch 4
	// carries both, and batch 5 resubmits batch 0's id.
	mk := func(prefix, customer string, n int, startDay int, cents int64) []load.Item {
		items := make([]load.Item, 0, n)
		for i := 0; i < n; i++ {
			day := startDay + i
			items = append(items, ld11Item(fmt.Sprintf("%s-%d", prefix, i), customer, "BR-A", fmt.Sprintf("%s-DOC-%d", prefix, i),
				fmt.Sprintf("2032-03-%02d", day), fmt.Sprintf("2032-04-%02d", day), cents+int64(i)*100, "", ""))
		}
		return items
	}
	b0 := mk("PL0", "CUST-ONE", 4, 10, 10000)
	b1 := mk("PL1", "CUST-ONE", 4, 3, 20000)
	b2 := mk("PL2", "CUST-ONE", 4, 20, 30000)
	b3 := mk("PL3", "CUST-TWO", 4, 5, 40000)
	b4 := append(mk("PL4a", "CUST-ONE", 2, 1, 5000), mk("PL4b", "CUST-TWO", 2, 28, 6000)...)
	subs := []struct {
		id    string
		items []load.Item
	}{{"b-pl-0", b0}, {"b-pl-1", b1}, {"b-pl-2", b2}, {"b-pl-3", b3}, {"b-pl-4", b4}, {"b-pl-0", b0}}

	var mu sync.Mutex
	contended := 0
	answers := make([]load.BatchResult, len(subs))
	errs := make([]error, len(subs))
	var wg sync.WaitGroup
	watchdog := time.AfterFunc(ld11PoolBudget, func() {
		t.Errorf("the receivables pool leg outlived its watchdog: a batch holds a connection while it waits for a second (the stall)")
		os.Exit(1)
	})
	defer watchdog.Stop()
	for k, s := range subs {
		wg.Add(1)
		go func(k int, id string, items []load.Item) {
			defer wg.Done()
			for attempt := 0; attempt < ld11PoolRetries; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), ld11PoolTimeout)
				r, err := door.UpsertReceivables(ctx, ld11Batch(id, append([]load.Item(nil), items...)...))
				cancel()
				if errors.Is(err, load.ErrBatchContended) {
					mu.Lock()
					contended++
					mu.Unlock()
					continue
				}
				answers[k], errs[k] = r, err
				return
			}
			errs[k] = errors.New("the batch contended on every retry")
		}(k, s.id, s.items)
	}
	wg.Wait()
	for k, err := range errs {
		if err != nil {
			t.Fatalf("submitter %d (%s): %v", k, subs[k].id, err)
		}
	}
	t.Logf("contended answers resubmitted whole: %d", contended)
	// The duplicate id's two submitters answered one record.
	for i := range answers[0].Items {
		if answers[0].Items[i] != answers[5].Items[i] {
			t.Fatalf("the shared id's submitters disagree at %d: %+v vs %+v", i, answers[0].Items[i], answers[5].Items[i])
		}
	}
	if n := e.count(t, `SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND source_system = $2`, ld11OrgID, ld11Source); n != 5 {
		t.Fatalf("%d batch records stand, want one per distinct id (5)", n)
	}
	if got := audit.len(); got != 5 {
		t.Fatalf("%d audit rows, want one per distinct batch (5)", got)
	}

	// The money: per customer and in total, the receivables equal the
	// fixture to the cent, one document and one ledger row per item.
	var wantOne, wantTwo int64
	items := 0
	for _, s := range subs[:5] {
		for _, it := range s.items {
			items++
			amount := it.Record["open_amount_cents"].(int64)
			if it.Requires[0].LegacyID == "CUST-ONE" {
				wantOne += amount
			} else {
				wantTwo += amount
			}
		}
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != items {
		t.Fatalf("%d documents landed, want %d (one per item)", n, items)
	}
	gotSum := func(customer string) int64 {
		var sum int64
		if err := e.pool.QueryRow(context.Background(),
			`SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1::uuid AND source_kind = 'IMPORT'`, customer).Scan(&sum); err != nil {
			t.Fatalf("sum read: %v", err)
		}
		return sum
	}
	if g := gotSum(ld11CustOneID); g != wantOne {
		t.Fatalf("customer one carries %d, want the fixture's %d", g, wantOne)
	}
	if g := gotSum(ld11CustTwoID); g != wantTwo {
		t.Fatalf("customer two carries %d, want the fixture's %d", g, wantTwo)
	}
	if g := gotSum(ld11CustOneID) + gotSum(ld11CustTwoID); g != wantOne+wantTwo {
		t.Fatalf("the total carries %d, want %d", g, wantOne+wantTwo)
	}
	for _, c := range []struct{ id string }{{ld11CustOneID}, {ld11CustTwoID}} {
		ld11AssertRunningBalances(t, e.ledger(t, c.id))
	}
}

// ld11AssertRunningBalances: read in date order, each row's balance is
// the cumulative sum of the amounts up to it.
func ld11AssertRunningBalances(t *testing.T, ledger []ld11LedgerRow) {
	t.Helper()
	var running int64
	for i, r := range ledger {
		running += r.Amount
		if r.BalanceAfter != running {
			t.Fatalf("ledger row %d (%s) carries running balance %d, want %d in date order", i, r.SourceDoc, r.BalanceAfter, running)
		}
	}
}
