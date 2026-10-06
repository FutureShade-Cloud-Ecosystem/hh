//go:build integration

// qd5_groupinvoice_integration_test.go, the manager's group invoice route under overlapping group
// sets. POST /orders/{id}/shipments/invoice takes no group set from the caller: the order module reads
// the plan and bills the groups that are COMPLETE and not yet linked to an invoice. That set is read
// before the writer takes the order's row lock, so a caller that read the plan while another was still
// writing can hold a set that overlaps a group the other is about to bill. The writer must decide
// against the committed state after its lock, so the invoices of one order never total more than the
// order.
//
// The overlap is forced the way the lock tests force their races: the first call's invoice insert is
// slowed by a trigger that sleeps inside its transaction (it holds the order lock), a second group
// completes in that window, and then three callers hit the served route at once. The pool size is set
// on the command (pool_max_conns on HH_CORE_TEST_DATABASE_URL); the leg is run at the deploy pool of
// four and at a larger one.
//
//	Run: HH_CORE_TEST_DATABASE_URL='postgres://.../scratch?sslmode=disable&pool_max_conns=4' \
//	     go test -tags integration ./cmd/core/ -run QD5GroupInvoice -count=1 -p 1
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestQD5GroupInvoice_OverlappingGroupSetsOnTheRouteNeverBillMoreThanTheOrder: a credit order of one
// line (3 units, 3390 with tax) split into group 1 (1 unit) and group 2 (2 units), on the on demand
// subset policy. Group 1 is complete and the manager invoices it; while that call is in flight group 2
// completes and three more callers invoice. Whatever the callers are told, the order is billed once.
func TestQD5GroupInvoice_OverlappingGroupSetsOnTheRouteNeverBillMoreThanTheOrder(t *testing.T) {
	e := newQD5E2E(t)
	q := e.seedQuote(1_000_000)
	orderID := e.convertAndConfirm(q)
	ctx := context.Background()
	t.Logf("pool max conns %d", e.pool.Config().MaxConns)

	if code, body := e.do(testkit.SubGeneralMgr, http.MethodPost, "/api/v1/orders/"+orderID+"/shipments/invoice-policy",
		map[string]any{"policy": "ON_DEMAND_SUBSET"}); code != http.StatusOK {
		t.Fatalf("set the invoice policy: %d %s", code, body)
	}
	var g1, g2 string
	if err := e.pool.QueryRow(ctx, `
		WITH g AS (
		  INSERT INTO shipment_groups (order_id, group_no, status, completed_at)
		  VALUES ($1, 1, 'COMPLETE', now()), ($1, 2, 'PLANNED', NULL) RETURNING id, group_no),
		l AS (
		  INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
		  SELECT g.id, ol.id, CASE g.group_no WHEN 1 THEN 1 ELSE ol.qty_base - 1 END
		    FROM g JOIN order_lines ol ON ol.order_id = $1 RETURNING 1)
		SELECT (SELECT id::text FROM g WHERE group_no = 1), (SELECT id::text FROM g WHERE group_no = 2), (SELECT count(*) FROM l)`,
		orderID).Scan(&g1, &g2, new(int)); err != nil {
		t.Fatalf("seed the two shipment groups: %v", err)
	}
	(&qd5Env{t: t, pool: e.pool}).slowInsert("invoices", q.CustomerID, 2*time.Second)

	type answer struct {
		code int
		body []byte
	}
	answers := make([]answer, 4)
	var wg sync.WaitGroup
	call := func(i int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, b := e.do(testkit.SubGeneralMgr, http.MethodPost, "/api/v1/orders/"+orderID+"/shipments/invoice", nil)
			answers[i] = answer{c, b}
		}()
	}

	call(0) // reads the plan with group 1 complete and alone, then sleeps inside its invoice insert
	time.Sleep(700 * time.Millisecond)
	if _, err := e.pool.Exec(ctx,
		`UPDATE shipment_groups SET status = 'COMPLETE', completed_at = now() WHERE id = $1`, g2); err != nil {
		t.Fatalf("complete group 2: %v", err)
	}
	call(1)
	call(2)
	call(3)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("the four route calls did not finish")
	}
	for i, a := range answers {
		t.Logf("call %d: %d %.160s", i, a.code, a.body)
		if a.code >= 500 {
			t.Fatalf("call %d answered a server error: %d %s", i, a.code, a.body)
		}
	}

	var docs int
	var billed int64
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(total_cents),0)::bigint FROM invoices WHERE customer_id = $1 AND doc_type = 'INVOICE'`,
		q.CustomerID).Scan(&docs, &billed); err != nil {
		t.Fatal(err)
	}
	if billed > q.Total {
		t.Fatalf("the order was billed %d across %d documents, order total %d: a group was billed twice", billed, docs, q.Total)
	}
	// A fresh call reads the plan again and bills what is left, so the refusal strands nothing: the
	// two documents then bill the order exactly once.
	if code, body := e.do(testkit.SubGeneralMgr, http.MethodPost, "/api/v1/orders/"+orderID+"/shipments/invoice", nil); code != http.StatusCreated {
		t.Fatalf("the repeat after the refusal: %d %s", code, body)
	}
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(total_cents),0)::bigint FROM invoices WHERE customer_id = $1 AND doc_type = 'INVOICE'`,
		q.CustomerID).Scan(&docs, &billed); err != nil {
		t.Fatal(err)
	}
	if billed != q.Total {
		t.Fatalf("after the repeat the order is billed %d across %d documents, want exactly the order total %d", billed, docs, q.Total)
	}
	// Each live group is linked to at most one document, and each unit of the line is billed once.
	var billedQty float64
	if err := e.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(il.qty_base),0)::float8 FROM invoice_lines il
		  JOIN invoices i ON i.id = il.invoice_id WHERE i.customer_id = $1 AND i.doc_type = 'INVOICE'`,
		q.CustomerID).Scan(&billedQty); err != nil {
		t.Fatal(err)
	}
	if billedQty > 3 {
		t.Fatalf("%v units billed on an order of 3", billedQty)
	}
}

// TestQD5GroupInvoice_ThreeCallersWithTheSameSetOnTheRouteBillOnce: no overlap, the plain double click
// storm. Three callers read the same plan (both groups complete) and all answer the one document.
func TestQD5GroupInvoice_ThreeCallersWithTheSameSetOnTheRouteBillOnce(t *testing.T) {
	e := newQD5E2E(t)
	q := e.seedQuote(1_000_000)
	orderID := e.convertAndConfirm(q)
	ctx := context.Background()
	t.Logf("pool max conns %d", e.pool.Config().MaxConns)
	if code, body := e.do(testkit.SubGeneralMgr, http.MethodPost, "/api/v1/orders/"+orderID+"/shipments/invoice-policy",
		map[string]any{"policy": "ON_DEMAND_SUBSET"}); code != http.StatusOK {
		t.Fatalf("set the invoice policy: %d %s", code, body)
	}
	if err := e.pool.QueryRow(ctx, `
		WITH g AS (
		  INSERT INTO shipment_groups (order_id, group_no, status, completed_at)
		  VALUES ($1, 1, 'COMPLETE', now()), ($1, 2, 'COMPLETE', now()) RETURNING id, group_no),
		l AS (
		  INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
		  SELECT g.id, ol.id, CASE g.group_no WHEN 1 THEN 1 ELSE ol.qty_base - 1 END
		    FROM g JOIN order_lines ol ON ol.order_id = $1 RETURNING 1)
		SELECT count(*) FROM l`, orderID).Scan(new(int)); err != nil {
		t.Fatalf("seed the two shipment groups: %v", err)
	}
	(&qd5Env{t: t, pool: e.pool}).slowInsert("invoices", q.CustomerID, time.Second)
	codes := make([]int, 3)
	ids := make([]string, 3)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, b := e.do(testkit.SubGeneralMgr, http.MethodPost, "/api/v1/orders/"+orderID+"/shipments/invoice", nil)
			codes[i] = c
			var out struct {
				InvoiceID string `json:"invoice_id"`
			}
			_ = json.Unmarshal(b, &out)
			ids[i] = out.InvoiceID
		}()
	}
	wg.Wait()
	for i := range codes {
		if codes[i] != http.StatusCreated || ids[i] == "" || ids[i] != ids[0] {
			t.Fatalf("call %d: %d invoice %q, every caller answers the one document %q", i, codes[i], ids[i], ids[0])
		}
	}
	var docs int
	var billed int64
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(total_cents),0)::bigint FROM invoices WHERE customer_id = $1 AND doc_type = 'INVOICE'`,
		q.CustomerID).Scan(&docs, &billed); err != nil {
		t.Fatal(err)
	}
	if docs != 1 || billed != q.Total {
		t.Fatalf("billed %d across %d documents, want one document of %d", billed, docs, q.Total)
	}
}
