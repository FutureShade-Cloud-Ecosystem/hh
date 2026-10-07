//go:build integration

// The yard's short pick against a real order: the shortfall becomes a backorder on the line, the
// quantity to fulfil and the money drop with it, the invoice built from the order bills what goes
// out, and the ordered quantity can never be exceeded.
//
// Run with: DATABASE_URL=... go test -tags integration -count=1 -run YardPick ./internal/order/

package order

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedYardOrder is a CONFIRMED Trenton order with one line of 5 at 1000 each (5000), tax 650.
func seedYardOrder(t *testing.T, pool *pgxpool.Pool, status string) (ord string) {
	t.Helper()
	_, ord, _, _ = seedEditOrder(t, pool, status, 650)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE order_lines SET qty_entered = 5, qty_base = 5,
		unit_price_cents = 1000, line_total_cents = 5000 WHERE order_id = $1`, ord); err != nil {
		t.Fatalf("shape line: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET subtotal_cents = 5000, total_cents = 5650 WHERE id = $1`, ord); err != nil {
		t.Fatalf("shape order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_shortfall_basis WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM order_line_shortfalls WHERE order_id = $1`, ord)
	})
	return ord
}

type backorderRow struct {
	entered, base string
}

func liveBackorder(t *testing.T, pool *pgxpool.Pool, ord string) backorderRow {
	t.Helper()
	var b backorderRow
	if err := pool.QueryRow(context.Background(),
		`SELECT backorder_entered::text, backorder_base::text FROM order_lines WHERE order_id = $1 AND line_no = 1`,
		ord).Scan(&b.entered, &b.base); err != nil {
		t.Fatalf("read backorder: %v", err)
	}
	return b
}

func liveTax(t *testing.T, pool *pgxpool.Pool, ord string) int64 {
	t.Helper()
	var tax int64
	if err := pool.QueryRow(context.Background(), `SELECT tax_cents FROM orders WHERE id = $1`, ord).Scan(&tax); err != nil {
		t.Fatalf("read tax: %v", err)
	}
	return tax
}

func yardPick(qty, reason string) YardPickInput {
	return YardPickInput{ActorID: "3b175000-0000-4000-9000-000000000002",
		Lines: []YardPickLineInput{{LineNo: 1, QtyPicked: qty, Reason: reason, Note: "yard count"}}}
}

func TestIntegration_YardPick_ShortBecomesABackorderAndTheMoneyFollows(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	svc, _ := editSvc(t, pool)
	in := yardPick("3", "CONDENSED_SKIDS")
	in.OrderID = ord

	out, err := svc.ApplyYardPicks(context.Background(), in)
	if err != nil {
		t.Fatalf("a short pick on a confirmed order must apply: %v", err)
	}
	l := out.Lines[0]
	if !l.Changed || l.QtyOrdered != "5.0000" || l.QtyPicked != "3.0000" || l.QtyBackordered != "2.0000" {
		t.Fatalf("the outcome must say ordered 5, picked 3, backordered 2: %+v", l)
	}
	qty, base, total := liveLine(t, pool, ord, 1)
	if qty != "3.0000" || base != "3.0000" || total != 3000 {
		t.Fatalf("the quantity to fulfil and the line total drop with the pick: %s %s %d", qty, base, total)
	}
	if b := liveBackorder(t, pool, ord); b.entered != "2.0000" || b.base != "2.0000" {
		t.Fatalf("the shortfall is held as a backorder on the line: %+v", b)
	}
	sub, tot := liveTotals(t, pool, ord)
	if sub != 3000 || liveTax(t, pool, ord) != 390 || tot != 3390 {
		t.Fatalf("the header follows the lines, tax pro-rated from the originals: %d %d %d", sub, liveTax(t, pool, ord), tot)
	}
	if out.Order.TotalBeforeCents != 5650 || out.Order.TotalAfterCents != 3390 {
		t.Fatalf("the notice facts carry the order total before and after: %+v", out.Order)
	}
}

func TestIntegration_YardPick_TheInvoiceBillsWhatGoesOut(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	svc, _ := editSvc(t, pool)
	in := yardPick("3", "DAMAGED")
	in.OrderID = ord
	if _, err := svc.ApplyYardPicks(context.Background(), in); err != nil {
		t.Fatalf("apply: %v", err)
	}
	f, err := svc.AgreedInvoiceableOrder(context.Background(), ord)
	if err != nil {
		t.Fatalf("the agreed invoice must still agree with its own lines after a short pick: %v", err)
	}
	var sum int64
	for _, ln := range f.Lines {
		sum += ln.LineTotalCents
	}
	if len(f.Lines) != 1 || f.Lines[0].QtyEntered != "3.0000" || sum != 3000 || *f.AgreedTaxCents != 390 {
		t.Fatalf("the invoice bills 3 at the agreed price with the pro-rated tax, never the 5 ordered: %+v tax %d", f.Lines, *f.AgreedTaxCents)
	}
}

func TestIntegration_YardPick_SameRecordAgainChangesNothing(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	svc, _ := editSvc(t, pool)
	in := yardPick("3", "CONDENSED_SKIDS")
	in.OrderID = ord
	first, err := svc.ApplyYardPicks(context.Background(), in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	out, err := svc.ApplyYardPicks(context.Background(), in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if out.Lines[0].Changed {
		t.Fatal("an identical repeat is not a change, and must not be told to anyone twice")
	}
	if first.Lines[0].Revision != 1 || out.Lines[0].Revision != 1 {
		t.Fatalf("a repeat keeps the revision its change carried, so its notice keeps its key: %d then %d",
			first.Lines[0].Revision, out.Lines[0].Revision)
	}
	next := yardPick("2", "CONDENSED_SKIDS")
	next.OrderID = ord
	if again, err := svc.ApplyYardPicks(context.Background(), next); err != nil || again.Lines[0].Revision != 2 {
		t.Fatalf("a real change is a new revision, so its notice is a new message: %+v %v", again, err)
	}
	if _, err := svc.ApplyYardPicks(context.Background(), in); err != nil {
		t.Fatalf("back to 3: %v", err)
	}
	if _, tot := liveTotals(t, pool, ord); tot != 3390 {
		t.Fatalf("a repeat must not drift the money after a round trip: %d", tot)
	}
}

func TestIntegration_YardPick_NobodyRaisesAboveOrderedButACorrectionRestores(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	svc, _ := editSvc(t, pool)
	ctx := context.Background()

	over := yardPick("6", "COUNT_CORRECTION")
	over.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, over); !errors.Is(err, ErrAboveOrdered) {
		t.Fatalf("a pick above ordered must be refused, got %v", err)
	}
	if _, tot := liveTotals(t, pool, ord); tot != 5650 {
		t.Fatalf("a refused pick leaves the order alone: %d", tot)
	}

	short := yardPick("3", "NOT_IN_YARD")
	short.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, short); err != nil {
		t.Fatalf("short: %v", err)
	}
	restore := yardPick("5", "")
	restore.OrderID = ord
	out, err := svc.ApplyYardPicks(ctx, restore)
	if err != nil {
		t.Fatalf("the yard finds the rest and records the full pick: %v", err)
	}
	if !out.Lines[0].Changed {
		t.Fatal("restoring the line is a change")
	}
	qty, _, total := liveLine(t, pool, ord, 1)
	sub, tot := liveTotals(t, pool, ord)
	if qty != "5.0000" || total != 5000 || sub != 5000 || tot != 5650 || liveTax(t, pool, ord) != 650 {
		t.Fatalf("a full pick restores the line and the money exactly: %s %d %d %d", qty, total, sub, tot)
	}
	if b := liveBackorder(t, pool, ord); b.entered != "0.0000" || b.base != "0.0000" {
		t.Fatalf("nothing is left on backorder: %+v", b)
	}
}

func TestIntegration_YardPick_RefusedWhenTheOrderCannotTakeIt(t *testing.T) {
	pool := intPool(t)
	svc, _ := editSvc(t, pool)
	for _, status := range []string{"DRAFT", "FULFILLED", "INVOICED", "CLOSED", "CANCELLED"} {
		ord := seedYardOrder(t, pool, status)
		in := yardPick("3", "DAMAGED")
		in.OrderID = ord
		var ne *NotEditableError
		if _, err := svc.ApplyYardPicks(context.Background(), in); !errors.As(err, &ne) {
			t.Fatalf("a %s order must refuse a short pick, got %v", status, err)
		}
	}
}

func TestIntegration_YardPick_FulfilledQuantityIsNotTakenBack(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "PICKING")
	if _, err := pool.Exec(context.Background(),
		`UPDATE order_lines SET fulfilled_base = 4 WHERE order_id = $1`, ord); err != nil {
		t.Fatal(err)
	}
	svc, _ := editSvc(t, pool)
	in := yardPick("3", "DAMAGED")
	in.OrderID = ord
	if _, err := svc.ApplyYardPicks(context.Background(), in); !errors.Is(err, ErrLineInUse) {
		t.Fatalf("a line with 4 already gone cannot be picked down to 3, got %v", err)
	}
}

type fixedStanding struct{ s InvoiceStanding }

func (f fixedStanding) InvoiceStanding(context.Context, string) (InvoiceStanding, error) {
	return f.s, nil
}

func TestIntegration_YardPick_PaidOrderCarriesWhatIsOwedBack(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "READY")
	svc, _ := editSvc(t, pool)
	svc.WithInvoiceStanding(fixedStanding{InvoiceStanding{Found: true, InvoiceNo: "#INV-9", TotalCents: 5650, PaidCents: 5650}})
	in := yardPick("3", "CONDENSED_SKIDS")
	in.OrderID = ord
	out, err := svc.ApplyYardPicks(context.Background(), in)
	if err != nil {
		t.Fatalf("a paid order is still picked: %v", err)
	}
	f := out.Order
	if f.InvoiceNo != "#INV-9" || f.PaidCents != 5650 || f.OwedBackCents != 5650-3390 {
		t.Fatalf("paid 5650 against a new total of 3390 means 2260 owed back: %+v", f)
	}
}

func storedReason(t *testing.T, pool *pgxpool.Pool, ord string) (reason, note string, revision int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `SELECT reason, note, revision FROM order_line_shortfalls WHERE order_id = $1 AND line_no = 1`,
		ord).Scan(&reason, &note, &revision); err != nil {
		t.Fatalf("read shortfall: %v", err)
	}
	return
}

func TestIntegration_YardPick_TheOrderRefusesAShortWithNoReason(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	svc, _ := editSvc(t, pool)
	ctx := context.Background()
	in := yardPick("3", "")
	in.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, in); err == nil {
		t.Fatal("a short on a whole line with no reason must be refused by the order itself")
	}
	other := yardPick("3", "OTHER")
	other.OrderID = ord
	other.Lines[0].Note = ""
	if _, err := svc.ApplyYardPicks(ctx, other); err == nil {
		t.Fatal("OTHER with no note must be refused by the order itself")
	}
	if _, tot := liveTotals(t, pool, ord); tot != 5650 {
		t.Fatalf("refused picks leave the order alone: %d", tot)
	}
}

func TestIntegration_YardPick_ResavingNeverBlanksTheReasonAndAReasonChangeIsAChange(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	svc, _ := editSvc(t, pool)
	ctx := context.Background()
	first := yardPick("3", "DAMAGED")
	first.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, first); err != nil {
		t.Fatal(err)
	}
	resave := yardPick("3", "")
	resave.OrderID = ord
	resave.Lines[0].Note = ""
	out, err := svc.ApplyYardPicks(ctx, resave)
	if err != nil {
		t.Fatalf("re-saving the pick as it stands is not a new short: %v", err)
	}
	if out.Lines[0].Changed || out.Lines[0].Reason != "DAMAGED" || out.Lines[0].Note != "yard count" || out.Lines[0].Revision != 1 {
		t.Fatalf("a blank re-save changes nothing and reports the stored reason and note: %+v", out.Lines[0])
	}
	if r, n, rev := storedReason(t, pool, ord); r != "DAMAGED" || n != "yard count" || rev != 1 {
		t.Fatalf("the stored reason and note survive a re-save: %q %q %d", r, n, rev)
	}
	again := yardPick("3", "NOT_IN_YARD")
	again.OrderID = ord
	out, err = svc.ApplyYardPicks(ctx, again)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Lines[0].Changed || out.Lines[0].Revision != 2 || out.Lines[0].Reason != "NOT_IN_YARD" {
		t.Fatalf("a new reason on a still short line is a change, with its own revision, so it is announced: %+v", out.Lines[0])
	}
	if _, tot := liveTotals(t, pool, ord); tot != 3390 {
		t.Fatalf("a reason only change moves no money: %d", tot)
	}
	full := yardPick("5", "")
	full.OrderID = ord
	out, err = svc.ApplyYardPicks(ctx, full)
	if err != nil || out.Lines[0].Reason != "" || out.Lines[0].QtyOrdered != "5.0000" {
		t.Fatalf("a full pick clears the reason and reports the true ordered quantity: %+v %v", out.Lines[0], err)
	}
}
