//go:build integration

package billing

// deposit_release_integration_test.go, the release seam and the sweep against
// the real schema: ReleaseDeposits emits one deposit.released row per live
// deposit document of the order, and the sweep heals a delivered order whose
// emission was lost, exactly once (a posted journal takes the order out).

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// deliveredFake is the order module's delivered-orders read.
type deliveredFake struct{ delivered map[string]bool }

func (d deliveredFake) DeliveredOrderIDs(_ context.Context, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		if d.delivered[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// journalFake is the ledger's keyed existence read (gl.Service.JournalKeysExist in production). It
// reads the planted journals off the pool so the test stays on the schema, and counts its calls and
// the widest key list it was handed, which is what the sweep's paging bounds.
type journalFake struct {
	pool   *pgxpool.Pool
	calls  int
	widest int
}

func (j *journalFake) JournalKeysExist(ctx context.Context, keys []string) ([]string, error) {
	j.calls++
	if len(keys) > j.widest {
		j.widest = len(keys)
	}
	var out []string
	for _, k := range keys {
		var n int
		if err := j.pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, k).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, k)
		}
	}
	return out, nil
}

func seedDepositOrder(t *testing.T, pool *pgxpool.Pool, tag string) (cust, orderID, dep string) {
	t.Helper()
	ctx := context.Background()
	cust = seedCustomer(t, pool, tag)
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		 VALUES ('ORD-DEP-'||$1, $2::uuid, $3::uuid, 'INVOICED', 'DELIVERY') RETURNING id::text`,
		tag, cust, trentonBranch).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	dep = seedSupplierDepositInvoice(t, pool, cust, "#INV-"+tag+"-DEP", 4_000)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid WHERE id = $2::uuid`, orderID, dep); err != nil {
		t.Fatalf("bind the deposit to the order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE (payload->>'invoice_id')::uuid = $1::uuid`, dep)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "deposit.released:"+dep)
	})
	return cust, orderID, dep
}

func releasedRows(t *testing.T, pool *pgxpool.Pool, dep string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events_outbox WHERE topic = 'deposit.released' AND (payload->>'invoice_id')::uuid = $1::uuid`,
		dep).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegration_ReleaseDeposits_EmitsOnePerLiveDocument(t *testing.T) {
	pool := intPool(t)
	tag := ns(t)
	_, orderID, dep := seedDepositOrder(t, pool, tag)
	if err := NewInvoiceWriter(pool).ReleaseDeposits(context.Background(), orderID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := releasedRows(t, pool, dep); got != 1 {
		t.Fatalf("the release wrote %d rows, want the one", got)
	}
}

func TestIntegration_DepositSweep_HealsALostEmissionExactlyOnce(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	_, orderID, dep := seedDepositOrder(t, pool, tag)

	// An undelivered order is never released by the sweep, whatever its status.
	jk := &journalFake{pool: pool}
	w := NewInvoiceWriter(pool).WithDeliveredOrders(deliveredFake{delivered: map[string]bool{}}).WithJournalKeyReader(jk)
	if _, err := w.ReleaseDeliveredDeposits(ctx, 0); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := releasedRows(t, pool, dep); got != 0 {
		t.Fatalf("the sweep released an undelivered order: %d rows", got)
	}

	// A delivered order with no release journal (the emission was lost) heals.
	w = NewInvoiceWriter(pool).WithDeliveredOrders(deliveredFake{delivered: map[string]bool{orderID: true}}).WithJournalKeyReader(jk)
	res, err := w.ReleaseDeliveredDeposits(ctx, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Released < 1 || releasedRows(t, pool, dep) != 1 {
		t.Fatalf("the sweep did not heal the lost emission: %+v rows %d", res, releasedRows(t, pool, dep))
	}

	// The release journal exists (the consumer posted it; a bare header is enough for the anti-join).
	var periodID string
	var year int
	if err := pool.QueryRow(ctx, `SELECT id::text, year FROM fiscal_periods ORDER BY year, month LIMIT 1`).Scan(&periodID, &year); err != nil {
		t.Skipf("no fiscal period to plant the journal in: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, status, idempotency_key)
		VALUES ($1, 900000000 + (random()*90000000)::int, $2::uuid, 'INVOICE', 'DRAFT', $3)`,
		year, periodID, fmt.Sprintf("deposit.released:%s", dep)); err != nil {
		t.Fatalf("plant the release journal: %v", err)
	}
	if _, err := w.ReleaseDeliveredDeposits(ctx, 0); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if got := releasedRows(t, pool, dep); got != 1 {
		t.Fatalf("after the journal posted the sweep emitted again: %d rows, want the one", got)
	}
}

// The sweep pages its candidates: a long list of undelivered deposits is read a page at a time, the
// ledger is asked about one page's keys at a time, and a delivered deposit behind them still heals.
func TestIntegration_DepositSweep_PagesItsCandidates(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	delivered := map[string]bool{}
	var healed string
	for i := 0; i < 5; i++ {
		_, orderID, dep := seedDepositOrder(t, pool, fmt.Sprintf("%s-p%d", tag, i))
		if i == 4 {
			delivered[orderID] = true
			healed = dep
		}
	}
	old := depositSweepPageSize
	depositSweepPageSize = 2
	t.Cleanup(func() { depositSweepPageSize = old })

	jk := &journalFake{pool: pool}
	w := NewInvoiceWriter(pool).WithDeliveredOrders(deliveredFake{delivered: delivered}).WithJournalKeyReader(jk)
	if _, err := w.ReleaseDeliveredDeposits(ctx, 0); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if jk.widest > 2 {
		t.Fatalf("the ledger was asked about %d keys at once, want at most the page of 2", jk.widest)
	}
	if jk.calls < 2 {
		t.Fatalf("the ledger was asked %d times, want one question per page", jk.calls)
	}
	if got := releasedRows(t, pool, healed); got != 1 {
		t.Fatalf("the delivered deposit behind the pages was not healed: %d rows", got)
	}
}

// Without the ledger read the sweep cannot tell a released document from a lost one, so it skips.
func TestIntegration_DepositSweep_SkipsWithoutTheLedgerRead(t *testing.T) {
	pool := intPool(t)
	res, err := NewInvoiceWriter(pool).WithDeliveredOrders(deliveredFake{}).ReleaseDeliveredDeposits(context.Background(), 0)
	if err != nil || !res.Skipped {
		t.Fatalf("got %+v, %v; want a skip", res, err)
	}
}

// TestIntegration_DepositRelease_DocumentIDsAreReadOldestFirst: the shared reader's
// order. depositDocumentIDs feeds three callers: the release's emit order, the cancel's
// settle walk (which sorts the ids itself before it walks, so the reader's order is free
// to be the documents' own), and the POS return's account rail, which offsets the order's
// deposit documents in the order handed back (pos/returns.go applyAccountCredit). With
// two live documents whose created order differs from their id order, the reader must
// answer oldest first: a partial offset must land on the older document, not the lower
// id.
func TestIntegration_DepositRelease_DocumentIDsAreReadOldestFirst(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust := seedCustomer(t, pool, tag)
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		 VALUES ('ORD-DCO-'||$1, $2::uuid, $3::uuid, 'CONFIRMED', 'DELIVERY') RETURNING id::text`,
		tag, cust, trentonBranch).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	// createdFirst carries the HIGHER id; createdLast the LOWER, so the created order
	// and the id order disagree and the leg can tell them apart.
	const createdFirst = "00000000-0000-4000-8000-0000000000cb"
	const createdLast = "00000000-0000-4000-8000-0000000000ca"
	for _, d := range []struct {
		id    string
		older bool
	}{{createdFirst, true}, {createdLast, false}} {
		age := "now()"
		if d.older {
			age = "now() - interval '1 hour'"
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf(`
			INSERT INTO invoices (id, invoice_no, customer_id, order_id, branch_id, status, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents,
			                      source_kind, finalized_at, created_at)
			VALUES ($1::uuid, 'INV-DCO-'||$2||$3, $4::uuid, $5::uuid, $6::uuid, 'FINALIZED', CURRENT_DATE,
			        4000, 0, 4000, 4000, 'SUPPLIER_DEPOSIT', now(), %s)`, age),
			d.id, tag, d.id[len(d.id)-2:], cust, orderID, trentonBranch); err != nil {
			t.Fatalf("seed deposit document %s: %v", d.id, err)
		}
	}

	ids, err := NewInvoiceWriter(pool).DepositDocumentIDsTx(ctx, nil, orderID)
	if err != nil {
		t.Fatalf("read the order's deposit documents: %v", err)
	}
	if len(ids) != 2 || ids[0] != createdFirst || ids[1] != createdLast {
		t.Fatalf("the reader answered %v, want the oldest first [%s %s]: the POS return's account rail offsets the documents in this order, so a partial offset must land on the older document, not the lower id", ids, createdFirst, createdLast)
	}
}
