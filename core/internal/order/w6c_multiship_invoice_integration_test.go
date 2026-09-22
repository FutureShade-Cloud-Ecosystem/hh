//go:build integration

// w6c_multiship_invoice_integration_test.go, DIB-422's headline acceptance test:
//
//	READY → invoice → closed
//
// W5-A stopped one step short of this. It could prove readiness, and its last assertion was that
// InvoiceShipments "refuses LOUDLY rather than fabricating an invoice number (the PARK)". This file
// binds the real writer and walks the rest of the road, against real Postgres.
//
// -- WHY THIS TEST LIVES IN internal/order AND IMPORTS internal/billing ------------------------
// It is the only place both halves can be observed at once, and the thing under test is precisely
// the JOIN between them: order decides, billing documents, and the 0147 link closes the loop. The
// import is a TEST import that reconstructs the main.go adapter by hand, the same shape, in twenty
// lines, so the production fence (neither module imports the other) is untouched. This is the
// warranty/gl precedent from W5-B, applied to the same problem.
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/swarm6_c?sslmode=disable' \
//	  go test -tags=integration ./internal/order/... -run W6C -count=1
package order

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// -- the main.go adapter, rebuilt here ----------------------------------------------------------

// w6cOrderSource is a byte-for-byte re-statement of main.go's w6cOrderInvoiceSource. Kept in sync by
// the compiler: it must satisfy billing.OrderInvoiceSource, and any field billing adds breaks BOTH
// this and the composition root at once.
type w6cOrderSource struct{ svc *Service }

func (a w6cOrderSource) InvoiceableOrder(ctx context.Context, orderID string, groupIDs []string) (billing.OrderInvoiceFacts, error) {
	f, err := a.svc.InvoiceableOrder(ctx, orderID, groupIDs)
	if err != nil {
		return billing.OrderInvoiceFacts{}, err
	}
	out := billing.OrderInvoiceFacts{
		OrderID: f.OrderID, OrderNo: f.OrderNo, CustomerID: f.CustomerID, BranchID: f.BranchID,
		TermsDays: f.TermsDays, TaxReasonCode: f.TaxReasonCode, ClosesOrder: f.ClosesOrder,
		GroupIDs: f.GroupIDs,
	}
	for _, l := range f.Lines {
		out.Lines = append(out.Lines, billing.OrderInvoiceLine{
			LineNo: l.LineNo, OrderLineID: l.OrderLineID, ProductID: l.ProductID,
			Description: l.Description, QtyEntered: l.QtyEntered, UOMEntered: l.UOMEntered,
			QtyBase: l.QtyBase, UnitPriceCents: l.UnitPriceCents, PriceUOM: l.PriceUOM,
			LineTotalCents: l.LineTotalCents,
		})
	}
	return out, nil
}

func (a w6cOrderSource) MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error {
	return a.svc.MarkGroupsInvoiced(ctx, groupIDs, invoiceID, at)
}

// w6cInvoicer is main.go's w6cMultiShipInvoicer.
type w6cInvoicer struct{ w *billing.InvoiceWriter }

func (a w6cInvoicer) InvoiceGroups(ctx context.Context, orderID string, groupIDs []string, policy string) (string, string, error) {
	doc, err := a.w.InvoiceOrderGroups(ctx, orderID, groupIDs, policy)
	if err != nil {
		return "", "", err
	}
	return doc.InvoiceID, doc.InvoiceNo, nil
}

// w6cWireWriter binds a real writer to a real order service, exactly as the composition root does.
func w6cWireWriter(t *testing.T, pool *pgxpool.Pool, svc *Service) *billing.InvoiceWriter {
	t.Helper()
	w := billing.NewInvoiceWriter(pool).WithOrderSource(w6cOrderSource{svc: svc})
	svc.WithMultiShipInvoicer(w6cInvoicer{w: w})
	return w
}

// w6cCleanupInvoices removes the documents a test raised. FIXTURES RESTORE WHAT THEY TOUCH: invoices
// are AR, and a stray FINALIZED row inflates every aging report and the 1100 tie-out for every later
// test in the suite, the W5-B recovery-board lesson, in a table where the number is the balance
// sheet.
func w6cCleanupInvoices(t *testing.T, pool *pgxpool.Pool, orderID string) {
	t.Helper()
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE source_ref_id IN
			(SELECT id FROM invoices WHERE order_id = $1::uuid)`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'invoice_id' IN
			(SELECT id::text FROM invoices WHERE order_id = $1::uuid)`, orderID)
		// The link must go before the document, or 0147's FK sets it NULL and the group is orphaned
		// mid-cleanup with a stamped invoiced_at, which its own CHECK forbids.
		_, _ = pool.Exec(c, `UPDATE shipment_groups SET invoice_id = NULL, invoiced_at = NULL
			WHERE order_id = $1::uuid`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE order_id = $1::uuid`, orderID)
	})
}

// -- the acceptance test ------------------------------------------------------------------------

// TestIntegrationW6C_MultiShipReadyToInvoiceToClosed walks the whole road: a two-truck order, one
// truck delivered, a progress invoice, the second truck, the closing invoice, and asserts at every
// step the thing that would actually go wrong.
func TestIntegrationW6C_MultiShipReadyToInvoiceToClosed(t *testing.T) {
	pool := intPool(t)
	ord, _ := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	w6cCleanupInvoices(t, pool, ord)
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))
	w6cWireWriter(t, pool, svc)

	plan, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 1", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 10}}},
		{Label: "Truck 2", Lines: []ShipmentGroupLineInput{{LineNo: 2, QtyBase: 4}}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	g1, g2 := plan.Groups[0].ID, plan.Groups[1].ID

	// The writer being bound is itself observable, and it is what W5-A published and could not set.
	if !plan.Readiness.WriterBound {
		t.Fatal("with an invoicer bound, readiness must report WriterBound, W5-A's park, closed")
	}

	// -- truck one goes out, and progress billing raises the FIRST document --
	if _, err := svc.SetInvoicePolicy(ctx, ord, InvoiceOnDemandSubset); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	w6cComplete(t, svc, ctx, g1)

	readiness, invID1, invNo1, err := svc.InvoiceShipments(ctx, ord)
	if err != nil {
		t.Fatalf("invoice truck one: %v", err)
	}
	if invID1 == "" || invNo1 == "" {
		t.Fatal("a raised invoice must come back named, the whole point of closing the park")
	}
	if readiness.InvoiceableCents != 1000 {
		t.Errorf("the progress invoice must bill truck one's share only; got %d", readiness.InvoiceableCents)
	}

	// It is a PROGRESS invoice: closes_order FALSE, because truck two is still owed. That flag is what
	// keeps the order's lifecycle from advancing to INVOICED on a partial bill.
	var closes bool
	var srcKind string
	var sub1, tax1, tot1, bal1 int64
	if err := pool.QueryRow(ctx, `SELECT closes_order, source_kind, subtotal_cents, tax_cents,
		total_cents, balance_cents FROM invoices WHERE id = $1`, invID1).
		Scan(&closes, &srcKind, &sub1, &tax1, &tot1, &bal1); err != nil {
		t.Fatalf("read invoice 1: %v", err)
	}
	if closes {
		t.Error("a progress invoice must NOT claim to close the order, truck two is still owed")
	}
	if srcKind != billing.SourceMultiShip {
		t.Errorf("source_kind = %q, want MULTISHIP, a row must say which producer made it", srcKind)
	}
	if sub1 != 1000 {
		t.Errorf("subtotal = %d, want 1000 (truck one's pro-rated share)", sub1)
	}
	// Billed on TERMS: no money was taken, so the balance is the whole total. This is G5, the writer
	// has no tender concept, and an invoice that arrived pre-paid would mean it had invented a payment.
	if bal1 != tot1 || tot1 != sub1+tax1 {
		t.Errorf("an order billed on terms must be FINALIZED at full balance: sub=%d tax=%d tot=%d bal=%d",
			sub1, tax1, tot1, bal1)
	}

	// The 0147 link closed the loop.
	w6cAssertLinked(t, pool, g1, invID1)

	// -- THE DOUBLE-BILL GUARD, at the database --
	// Truck one is delivered and billed. Nothing new has gone out. Asking again must not raise a
	// second document for work already invoiced.
	after, err := svc.GetShipmentPlan(ctx, ord)
	if err != nil {
		t.Fatal(err)
	}
	if after.Readiness.Ready {
		t.Fatal("nothing new has been delivered, a second invoice must not be offered")
	}
	if after.Readiness.GroupsInvoiced != 1 {
		t.Errorf("groups_invoiced = %d, want 1", after.Readiness.GroupsInvoiced)
	}
	if _, _, _, err := svc.InvoiceShipments(ctx, ord); err == nil {
		t.Fatal("re-invoicing an already-billed group must be refused")
	}

	// -- truck two goes out; the closing invoice bills ONLY the remainder --
	w6cComplete(t, svc, ctx, g2)
	readiness2, invID2, _, err := svc.InvoiceShipments(ctx, ord)
	if err != nil {
		t.Fatalf("invoice truck two: %v", err)
	}
	if invID2 == invID1 {
		t.Fatal("the second delivery must produce its OWN document, not the first one again")
	}
	if readiness2.InvoiceableCents != 400 {
		t.Errorf("the closing invoice must bill truck two's share only (400); got %d",
			readiness2.InvoiceableCents)
	}
	var closes2 bool
	var sub2 int64
	if err := pool.QueryRow(ctx,
		`SELECT closes_order, subtotal_cents FROM invoices WHERE id = $1`, invID2).Scan(&closes2, &sub2); err != nil {
		t.Fatal(err)
	}
	// CLOSED. Nothing live is left unbilled, so this document ends the order's billing, decided by
	// arithmetic, not by the policy name, which is why an ON_DEMAND_SUBSET slice can close an order.
	if !closes2 {
		t.Error("the last slice of a phased job MUST close the order's billing, whatever the policy is called")
	}
	if sub2 != 400 {
		t.Errorf("closing subtotal = %d, want 400", sub2)
	}

	// -- the money adds up --
	// Σ(progress invoices) == the order. This is the property pro-rating exists to guarantee, and the
	// one a naive unit_price × qty recomputation silently breaks by a cent on discounted lines.
	if sub1+sub2 != 1400 {
		t.Errorf("the progress invoices must sum to the order: %d + %d ≠ 1400", sub1, sub2)
	}

	w6cAssertLinked(t, pool, g2, invID2)

	// -- and the order is done --
	final, err := svc.GetShipmentPlan(ctx, ord)
	if err != nil {
		t.Fatal(err)
	}
	if final.Readiness.Ready || final.Readiness.InvoiceableCents != 0 {
		t.Errorf("a fully-billed order offers nothing: ready=%v invoiceable=%d",
			final.Readiness.Ready, final.Readiness.InvoiceableCents)
	}
	if final.Readiness.GroupsInvoiced != 2 {
		t.Errorf("groups_invoiced = %d, want 2", final.Readiness.GroupsInvoiced)
	}
	if final.Readiness.Reason == "" {
		t.Error("the finished state must say what it is, not read as a refusal")
	}
}

// TestIntegrationW6C_InvoicingIsIdempotentAtTheDatabase pins the retry guarantee: the SAME group set
// asked for twice yields ONE document, and the second caller is handed the first caller's.
//
// This is the double-clicked button, and it matters more here than almost anywhere else in the
// product: the failure mode is a customer receiving two invoices for one delivery, and the person who
// discovers it is the customer.
func TestIntegrationW6C_InvoicingIsIdempotentAtTheDatabase(t *testing.T) {
	pool := intPool(t)
	ord, _ := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	w6cCleanupInvoices(t, pool, ord)
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))
	writer := w6cWireWriter(t, pool, svc)

	plan, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 1", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 10}}},
		{Label: "Truck 2", Lines: []ShipmentGroupLineInput{{LineNo: 2, QtyBase: 4}}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	g1 := plan.Groups[0].ID
	w6cComplete(t, svc, ctx, g1)

	// Call the WRITER directly, twice, with the same group set, bypassing order's readiness guard,
	// because the guarantee under test is billing's own and must not depend on a caller remembering.
	doc1, err := writer.InvoiceOrderGroups(ctx, ord, []string{g1}, InvoiceOnDemandSubset)
	if err != nil {
		t.Fatalf("first invoice: %v", err)
	}
	if doc1.Reused {
		t.Error("the first call raised the document; it cannot be a reuse")
	}
	doc2, err := writer.InvoiceOrderGroups(ctx, ord, []string{g1}, InvoiceOnDemandSubset)
	if err != nil {
		t.Fatalf("second invoice: %v", err)
	}
	if doc2.InvoiceID != doc1.InvoiceID {
		t.Fatalf("a repeat must return THE SAME document, got %s then %s", doc1.InvoiceID, doc2.InvoiceID)
	}
	if !doc2.Reused {
		t.Error("the repeat must SAY it reused, the surface shows 'already invoiced as …' rather than pretending")
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE order_id = $1::uuid`, ord).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("two clicks must leave ONE invoice in the table; found %d", n)
	}
	// And exactly one subledger row: a duplicate there would double the customer's balance even if
	// the document table were clean.
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_transactions WHERE source_ref_id = $1::uuid`, doc1.InvoiceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the AR subledger must carry ONE row for one invoice; found %d", n)
	}
}

// -- helpers ------------------------------------------------------------------------------------

func w6cComplete(t *testing.T, svc *Service, ctx context.Context, groupID string) {
	t.Helper()
	for _, step := range []string{GroupPicking, GroupReady, GroupDispatched, GroupComplete} {
		if _, err := svc.TransitionGroup(ctx, groupID, step); err != nil {
			t.Fatalf("group %s → %s: %v", groupID, step, err)
		}
	}
}

func w6cAssertLinked(t *testing.T, pool *pgxpool.Pool, groupID, invoiceID string) {
	t.Helper()
	var linked string
	var at *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(invoice_id::text,''), invoiced_at FROM shipment_groups WHERE id = $1`,
		groupID).Scan(&linked, &at); err != nil {
		t.Fatalf("read group link: %v", err)
	}
	if linked != invoiceID {
		t.Errorf("group %s links to %q, want %q, an unlinked group is a group that can be re-billed",
			groupID, linked, invoiceID)
	}
	// 0147 CHECKs (invoice_id IS NULL) = (invoiced_at IS NULL), so this is belt and braces, but the
	// stamp is what an operator reads to answer "when was this billed?", so it is asserted, not assumed.
	if at == nil {
		t.Errorf("group %s is billed but carries no invoiced_at", groupID)
	}
}

// TestIntegrationW6C_ASplitLineIsProRatedAcrossItsGroups closes a gap a mutation found.
//
// The headline test above plans one order LINE per truck, so every group carries a whole line and the
// pro-rating branch never executes. Replacing the pro-rated share with the whole line total was a
// SURVIVING mutation: both progress invoices would bill the ENTIRE line, the customer would be
// charged 10/10 twice for a line delivered 6 then 4, and every assertion in that test still passed.
//
// This plans ONE line across TWO trucks, which is the actual shape of a phased job and the only shape
// that exercises the arithmetic.
func TestIntegrationW6C_ASplitLineIsProRatedAcrossItsGroups(t *testing.T) {
	pool := intPool(t)
	// Line 1 is qty 10 at $1.00 → a $10.00 line. Split 6 / 4.
	ord, _ := w5aSeedTwoLineOrder(t, pool, "CONFIRMED")
	w6cCleanupInvoices(t, pool, ord)
	svc := w5aChangeService(t, 100, true)
	ctx := identity.ContextWithStaff(context.Background(), w5aStaff(true))
	w6cWireWriter(t, pool, svc)

	plan, err := svc.PlanShipment(ctx, ord, []ShipmentGroupInput{
		{Label: "Truck 1", Lines: []ShipmentGroupLineInput{{LineNo: 1, QtyBase: 6}}},
		{Label: "Truck 2", Lines: []ShipmentGroupLineInput{
			{LineNo: 1, QtyBase: 4}, {LineNo: 2, QtyBase: 4},
		}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	g1, g2 := plan.Groups[0].ID, plan.Groups[1].ID

	if _, err := svc.SetInvoicePolicy(ctx, ord, InvoiceOnDemandSubset); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	// -- truck one: SIX of the ten --
	w6cComplete(t, svc, ctx, g1)
	if _, invID1, _, err := svc.InvoiceShipments(ctx, ord); err != nil {
		t.Fatalf("invoice truck one: %v", err)
	} else {
		var sub, lineTotal int64
		if err := pool.QueryRow(ctx, `SELECT subtotal_cents FROM invoices WHERE id = $1`, invID1).Scan(&sub); err != nil {
			t.Fatal(err)
		}
		// 6/10 of a $10.00 line = $6.00. NOT $10.00.
		if sub != 600 {
			t.Errorf("subtotal = %d, want 600, six tenths of a $10.00 line, not the whole line", sub)
		}
		if err := pool.QueryRow(ctx,
			`SELECT line_total_cents FROM invoice_lines WHERE invoice_id = $1 ORDER BY line_no LIMIT 1`,
			invID1).Scan(&lineTotal); err != nil {
			t.Fatal(err)
		}
		if lineTotal != 600 {
			t.Errorf("the invoice LINE must carry the pro-rated share too; got %d want 600", lineTotal)
		}
	}

	// -- truck two: the remaining FOUR, plus line 2 whole --
	w6cComplete(t, svc, ctx, g2)
	_, invID2, _, err := svc.InvoiceShipments(ctx, ord)
	if err != nil {
		t.Fatalf("invoice truck two: %v", err)
	}
	var sub2 int64
	if err := pool.QueryRow(ctx, `SELECT subtotal_cents FROM invoices WHERE id = $1`, invID2).Scan(&sub2); err != nil {
		t.Fatal(err)
	}
	// 4/10 of $10.00 = $4.00, plus line 2's whole $4.00 = $8.00.
	if sub2 != 800 {
		t.Errorf("subtotal = %d, want 800 (four tenths of line 1, plus line 2 whole)", sub2)
	}

	// THE INVARIANT pro-rating exists for: the slices sum EXACTLY to the order, with no cent lost or
	// invented. $6.00 + $8.00 = $14.00 = the order.
	if got := int64(600) + sub2; got != 1400 {
		t.Errorf("the progress invoices must sum to the order exactly: got %d want 1400", got)
	}
}
