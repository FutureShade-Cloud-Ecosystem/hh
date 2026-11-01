//go:build integration

package main

// w2_5_fix_r8_integration_test.go, W2-5 round 8 (finance wave 2): S5's P2-2 and P3-1,
// the moment a journal is dated at, proven through the production wiring.
//
// P2-2 (the receipts): a PO receipt books the same goods on two sides, the stock
// ledger's RECEIPT move and the GL's receipt journals. Before this round the move
// took the column default (the row's own now()) and the journals took the DRAIN's
// clock, so the two sides were cut at two different instants. A receipt in a month's
// last moment, drained after the month's end, left the stock ledger holding the
// goods and 1200 not holding them: the close refused that month by the whole
// receipt, and no later month could repair it.
//
// P3-1 (the rest of the stock movers): the same split, one round earlier, on the
// move rounding, the transfer legs, the transfer receive's rounding and the count's
// variance.
//
// THE FIX. The seam: inventory.GLJournal gains a DocumentDate, and cmd/core's
// invGLPoster carries it into gl.ExternalJournal. The receipts: applyReceipt reads
// ONE instant in its transaction (the transaction's own clock) and stamps it on the
// RECEIPT move and on the po.received payload's occurred_at; the consumer dates all
// three receipt journals with it. The rest: each movement reads its own instant
// once and hands it to the journal it raises.
//
// THE FRAME. The legs close the house's LAST ENDED month, the month before the
// running one, in the dealer's own zone: the same frame the month walk signs its
// bank month off in. The receipt lands in that month's LAST MOMENT and the drain
// runs after the month's end, so the month's end falls between the two sides of
// the receipt on the code before this round. The wall clock cannot reach a leg's
// own year, so the leg carries the movement's own instants into the frame the way
// the earlier rounds' legs do: the move row is re-dated and the event's carried
// instant is moved with it, and the event's instant is moved ONLY when the
// production event carries one.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     HH_CORE_TEST_DATABASE_URL='postgres://.../integration_core_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W25R8 -count=1 -v

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

// w25r8Frame is one leg's fiscal frame: the closing month, the instants inside it,
// and the month's end, the instant both sides of the tie read.
type w25r8Frame struct {
	leg       *w25Leg
	openAt    time.Time // inside the closing month: the opening journals' instant
	receiptAt time.Time // the closing month's LAST MOMENT: the receipt's instant
	monthEnd  time.Time // the closing month's end: the instant both sides read
	year      int
	month     int
}

// w25r8Instants are the leg's instants in the dealer's own zone.
//
// THE CLOSING MONTH IS THE HOUSE'S LAST ENDED MONTH: the month before the one the
// wall clock is running in. Two things follow, and the leg needs both. The drain
// runs NOW, after the closing month's end, so a journal the drain dates lands after
// the month's end and the month's close cannot see it. And the receipt lands in the
// closing month's last moment, inside it, so the stock ledger's as-of read at the
// month's end does see it. On the code before this round the month's end therefore
// found the receipt's two sides on different sides of itself.
//
// The frame is the house's own, not a leg's year of its own: the month walk signs
// its bank month off in the last ended month, and this leg closes the same month the
// same way, so the walk's frame and this leg's frame cannot drift apart.
func w25r8Instants(t *testing.T, leg *w25Leg) w25r8Frame {
	t.Helper()
	ctx := context.Background()
	zone, err := gl.NewRepository(leg.pool).BusinessZone(ctx)
	if err != nil {
		t.Fatalf("the dealer's business zone: %v", err)
	}
	loc := zone.Location()
	running := time.Now().In(loc)
	year, month := running.Year(), int(running.Month())-1
	if month == 0 {
		month, year = 12, year-1
	}
	monthStart := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	frame := w25r8Frame{
		leg:       leg,
		openAt:    monthStart.Add(12 * time.Hour),
		receiptAt: monthStart.AddDate(0, 1, 0).Add(-time.Second),
		monthEnd:  monthStart.AddDate(0, 1, 0),
		year:      year,
		month:     month,
	}
	return frame
}

// w25r8Periods opens the fiscal periods the frame needs: the closing month's year
// and the year after it, because the running month, where the drain dates a
// journal, falls in the year after the closing month when the house year turns over
// in December.
func w25r8Periods(t *testing.T, leg *w25Leg, year int) {
	t.Helper()
	for _, y := range []int{year, year + 1} {
		mustExec(t, context.Background(), leg.pool, `
			INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
			 ON CONFLICT (year, month) DO NOTHING`, y)
	}
}

// w25r8TieTheWorldAtItsEnd posts the opening journals that make the closing month
// tie at its end: the company's stock value AS OF the month's end, read through the
// production seam, against 1200; and the receivables as of the month's end against
// 1100. Both dated inside the closing month.
//
// The stock side is read AS OF the month's end, not as of now: the closing month is
// a month that has ended, and the movements after its end (the seed's own opening
// balances, the house's running month) are not on the books at it.
func w25r8TieTheWorldAtItsEnd(t *testing.T, frame w25r8Frame) {
	t.Helper()
	leg := frame.leg
	ctx := context.Background()
	stock, err := leg.money.inventorySvc.StockValueAsOfCents(ctx, frame.monthEnd, "")
	if err != nil {
		t.Fatalf("the company's stock value as of the month's end: %v", err)
	}
	gl1200, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", frame.monthEnd, "")
	if err != nil {
		t.Fatalf("the company's 1200 at the month's end: %v", err)
	}
	if delta := stock - gl1200; delta != 0 {
		amount := delta
		lines := []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: amount, Memo: "opening stock"},
			{AccountCode: "3000", CreditCents: amount, Memo: "opening equity"},
		}
		if delta < 0 {
			amount = -delta
			lines = []gl.ExternalLine{
				{AccountCode: "1200", CreditCents: amount, Memo: "opening stock"},
				{AccountCode: "3000", DebitCents: amount, Memo: "opening equity"},
			}
		}
		w25Post(t, leg, gl.ExternalJournal{
			Source: "MANUAL", IdempotencyKey: "w2-5:r8:opening:" + leg.suffix,
			Memo: "opening stock", BranchID: leg.branchID, DocumentDate: frame.openAt,
			Lines: lines,
		})
	}
	ar, err := billing.NewRepository(leg.pool).ARBalanceCents(ctx, frame.monthEnd, "")
	if err != nil {
		t.Fatalf("the company's receivables as of the month's end: %v", err)
	}
	if ar != 0 {
		w25Post(t, leg, gl.ExternalJournal{
			Source: "MANUAL", IdempotencyKey: "w2-5:r8:opening:ar:" + leg.suffix,
			Memo: "opening accounts receivable", BranchID: leg.branchID, DocumentDate: frame.openAt,
			Lines: []gl.ExternalLine{
				{AccountCode: "1100", DebitCents: ar, Memo: "opening AR"},
				{AccountCode: "3000", CreditCents: ar, Memo: "opening equity"},
			},
		})
	}
}

// w25r8TieTheARAtItsEnd ties the running month's receivables the way the stock side is tied: it reads the
// subledger and 1100 at the month's end through the production seams and posts only the difference, dated
// inside the month. It does not repost the open invoices outright. On the 1st of a month the closing
// month's end is today's first instant, which the subledger reads from the books' own balances (an as of on
// or after the house day), so the closing month's frame already opened every open invoice; posting them
// again for the running month doubled 1100.
func w25r8TieTheARAtItsEnd(t *testing.T, leg *w25Leg, openAt, monthEnd time.Time) {
	t.Helper()
	ctx := context.Background()
	sub, err := billing.NewRepository(leg.pool).ARBalanceCents(ctx, monthEnd, "")
	if err != nil {
		t.Fatalf("the receivables as of the running month's end: %v", err)
	}
	gl1100, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1100", monthEnd, "")
	if err != nil {
		t.Fatalf("1100 at the running month's end: %v", err)
	}
	delta := sub - gl1100
	if delta == 0 {
		return
	}
	debit, credit := "1100", "3000"
	amount := delta
	if delta < 0 {
		debit, credit, amount = "3000", "1100", -delta
	}
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "w2-5:r8:running:ar:" + leg.suffix,
		Memo: "opening accounts receivable", BranchID: leg.branchID, DocumentDate: openAt,
		Lines: []gl.ExternalLine{
			{AccountCode: debit, DebitCents: amount, Memo: "opening AR"},
			{AccountCode: credit, CreditCents: amount, Memo: "opening equity"},
		},
	})
}

// w25r8Receive orders and receives through the purchasing module's real receiving
// road into inventory's applyReceipt, and answers the receipt's move id and the
// receipt id the move and the journals key on.
//
// It does NOT drain the posting queue: the leg carries the receipt's own instant
// into its frame first, and the drain then posts the receipt's journals at the
// instant the receipt owns.
func w25r8Receive(t *testing.T, leg *w25Leg, staffCtx context.Context, year, month int, productID, branchID string, qty float64, unitCost int64) (moveID, receiptID string) {
	t.Helper()
	ctx := context.Background()
	svc := w25r3Purchasing(t, leg)
	var vendorID string
	if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active ORDER BY id LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("a seeded vendor: %v", err)
	}
	po, err := svc.CreatePO(staffCtx, purchasing.CreatePORequest{
		VendorID: vendorID, BranchID: branchID,
		ExpectedAt: fmt.Sprintf("%04d-%02d-15", year, month),
		Lines:      []purchasing.AddLineParams{{ProductID: productID, QtyOrderedBase: qty, UOM: "EA", UnitCostCents: unitCost}},
	})
	if err != nil {
		t.Fatalf("create the purchase order: %v", err)
	}
	ordered, err := svc.Transition(staffCtx, po.ID, purchasing.StatusOrdered)
	if err != nil {
		t.Fatalf("place the purchase order: %v", err)
	}
	if _, err := svc.Receive(staffCtx, ordered.ID, purchasing.ReceiveRequest{
		Lines: []purchasing.ReceiveLineParams{{POLineID: ordered.Lines[0].ID, QtyBase: qty}},
	}); err != nil {
		t.Fatalf("receive against the purchase order: %v", err)
	}
	if err := leg.pool.QueryRow(ctx, `
		SELECT id::text, source_ref_id::text FROM stock_moves
		 WHERE source_type = 'po_receipt' AND branch_id = $1::uuid AND product_id = $2::uuid
		 ORDER BY occurred_at DESC LIMIT 1`, branchID, productID).Scan(&moveID, &receiptID); err != nil {
		t.Fatalf("the receipt's move: %v", err)
	}
	return moveID, receiptID
}

// w25r8CarryTheReceiptInstant carries the receipt's own instant into the leg's
// frame: the move row is re-dated and the event's carried instant is moved with it.
//
// The event's instant is moved ONLY WHEN THE PRODUCTION EVENT CARRIES ONE. A
// receipt on the code before this round carries none, and the leg must not
// manufacture one for it: on that code the consumer falls back to the GL's clock,
// which is the whole defect this round closes.
func w25r8CarryTheReceiptInstant(t *testing.T, leg *w25Leg, moveID, receiptID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	w25r5RealignMove(t, leg, moveID, at)
	mustExec(t, ctx, leg.pool, `
		UPDATE events_outbox SET payload = jsonb_set(payload, '{occurred_at}', to_jsonb($2::timestamptz), true)
		 WHERE topic = 'po.received' AND payload->>'receipt_id' = $1 AND payload ? 'occurred_at'`, receiptID, at)
}

// w25r8MoveInstant answers the instant a movement happened.
func w25r8MoveInstant(t *testing.T, leg *w25Leg, moveID string) time.Time {
	t.Helper()
	var at time.Time
	if err := leg.pool.QueryRow(context.Background(),
		`SELECT occurred_at FROM stock_moves WHERE id = $1::uuid`, moveID).Scan(&at); err != nil {
		t.Fatalf("the movement's instant: %v", err)
	}
	return at
}

// w25r8JournalInstant answers the instant a journal is posted at.
func w25r8JournalInstant(t *testing.T, leg *w25Leg, key string) time.Time {
	t.Helper()
	var at time.Time
	if err := leg.pool.QueryRow(context.Background(),
		`SELECT posted_at FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&at); err != nil {
		t.Fatalf("the journal %s: %v", key, err)
	}
	return at
}

// w25r8AssertCarriesTheMovementsInstant proves one journal is dated at the instant
// of the movement it books: the journal and its movement are cut at the same moment,
// so a month end finds them on the same side of itself.
func w25r8AssertCarriesTheMovementsInstant(t *testing.T, leg *w25Leg, key, moveID string) {
	t.Helper()
	journalAt := w25r8JournalInstant(t, leg, key)
	moveAt := w25r8MoveInstant(t, leg, moveID)
	if !journalAt.Equal(moveAt) {
		t.Fatalf("the journal %s is posted at %s, want the movement's own instant %s: a journal dated at another instant puts the two sides of one movement on two sides of a month end",
			key, journalAt.Format(time.RFC3339Nano), moveAt.Format(time.RFC3339Nano))
	}
}

// w25r8Leg is one leg's world: the wiring, the leg's own product, the closing
// month's frame, and the world tied at that month's end.
func w25r8Leg(t *testing.T, suffix string) w25r8Frame {
	t.Helper()
	leg := w25Setup(t, "r8"+suffix)
	frame := w25r8Instants(t, leg)
	w25r8Periods(t, leg, frame.year)
	w25r8TieTheWorldAtItsEnd(t, frame)
	return frame
}

// TestW25R8AMonthEndReceiptDrainedAfterItsEndStillCloses is the red: a PO receipt
// through the purchasing module's real receiving road in the closing month's last
// moment, drained after the month's end, must not refuse that month.
//
// On the code before this round the close is refused: the stock ledger holds the
// receipt and 1200 does not, because the journal is dated at the drain's clock,
// after the month's end, while the movement is dated inside the month.
func TestW25R8AMonthEndReceiptDrainedAfterItsEndStillCloses(t *testing.T) {
	frame := w25r8Leg(t, "red")
	leg := frame.leg
	ctx := context.Background()

	staffCtx := w25r3StaffCtx(t, leg)
	moveID, receiptID := w25r8Receive(t, leg, staffCtx, frame.year, frame.month, leg.productID, leg.branchID, 2, w25UnitCost)

	// The receipt's own instant, carried into the closing month: the move row and
	// the event's carried instant move together.
	w25r8CarryTheReceiptInstant(t, leg, moveID, receiptID, frame.receiptAt)

	// The drain, now: after the closing month's end.
	w25Drain(t, leg)

	// The month closes: the receipt in the month's last moment, drained after the
	// month's end, does not refuse the month.
	code, body := w25Close(t, leg, frame.year, frame.month)
	if code != http.StatusOK {
		t.Fatalf("the close of the month that tied at its end was refused by a receipt in the month's last moment, drained after the month's end: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, frame.year, frame.month); status != "CLOSED" {
		t.Fatalf("the period reads %s after the close, want CLOSED", status)
	}

	// The receipt's journal carries the receipt's own instant.
	w25r8AssertCarriesTheMovementsInstant(t, leg, "po.received:"+receiptID, moveID)

	// Both sides read the month's end and agree.
	stock, err := leg.money.inventorySvc.StockValueAsOfCents(ctx, frame.monthEnd, "")
	if err != nil {
		t.Fatalf("the stock ledger as of the month's end: %v", err)
	}
	gl1200, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", frame.monthEnd, "")
	if err != nil {
		t.Fatalf("1200 at the month's end: %v", err)
	}
	if stock != gl1200 {
		t.Fatalf("the month's end reads the stock ledger at %d and 1200 at %d: the receipt's two sides were not cut at the same instant", stock, gl1200)
	}
}

// TestW25R8ARealDiscrepancyIsStillRefusedWithTheOppositeDiscrepancy is the honest
// guard: a receipt that leaves a real discrepancy is still refused, and the refusal
// carries the opposite discrepancy to the receipt's own.
//
// The receipt is here, in the month's last moment, and its own two sides agree (the
// red's fix). The real discrepancy is a book-only write UP inside the month: 1200
// moves with no stock behind it, so at the month's end the stock ledger holds LESS
// than 1200. That is the opposite of the receipt's own defect, where the stock
// ledger held MORE than 1200, and it is a real break: no date moves it. The close
// must still refuse it.
func TestW25R8ARealDiscrepancyIsStillRefusedWithTheOppositeDiscrepancy(t *testing.T) {
	frame := w25r8Leg(t, "guard")
	leg := frame.leg
	ctx := context.Background()

	staffCtx := w25r3StaffCtx(t, leg)
	moveID, receiptID := w25r8Receive(t, leg, staffCtx, frame.year, frame.month, leg.productID, leg.branchID, 1, w25UnitCost)
	w25r8CarryTheReceiptInstant(t, leg, moveID, receiptID, frame.receiptAt)
	w25Drain(t, leg)

	// A book-only write UP inside the month: 1200 up, no stock behind it.
	w25BookOnly(t, leg, frame.openAt, "w2-5:r8:bookonly:"+leg.suffix, "book only write up", false)

	stock, err := leg.money.inventorySvc.StockValueAsOfCents(ctx, frame.monthEnd, "")
	if err != nil {
		t.Fatalf("the stock ledger as of the month's end: %v", err)
	}
	gl1200, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", frame.monthEnd, "")
	if err != nil {
		t.Fatalf("1200 at the month's end: %v", err)
	}
	if stock >= gl1200 {
		t.Fatalf("the month's end reads the stock ledger at %d and 1200 at %d: the guard wants the OPPOSITE discrepancy, the stock ledger holding less than 1200", stock, gl1200)
	}

	code, body := w25Close(t, leg, frame.year, frame.month)
	if code == http.StatusOK {
		t.Fatalf("the close sealed a month whose 1200 and stock ledger are apart by %d cents", gl1200-stock)
	}
	if !strings.Contains(body, "stock is out of tie with 1200") {
		t.Fatalf("the refusal %q does not name the stock tie", body)
	}
	if status := w25PeriodStatus(t, leg, frame.year, frame.month); status == "CLOSED" {
		t.Fatalf("the period reads CLOSED after a refused close")
	}
}

// TestW25R8EveryInventoryJournalCarriesItsMovementsInstant is one leg per journal
// kind, each asserting its posted date equals its movement's occurred_at.
//
// The receipt's three are two legs, not one: a receipt that blends raises the GRNI
// and the WAC rounding, a receipt that trues up a provisional sale raises the GRNI
// and the pre-receipt true-up, and the module never raises both at once (the
// rounding is the blend's, the true-up's branch never blends). The rest are the
// move rounding, the count's variance journal and the transfer's legs.
func TestW25R8EveryInventoryJournalCarriesItsMovementsInstant(t *testing.T) {
	t.Run("the receipt's GRNI and WAC rounding", func(t *testing.T) {
		leg := w25Setup(t, "r8receipt")
		// One unit on hand at 100, two received at 101: the blend revalues the
		// unit on hand by a cent, and the receipt raises the rounding.
		w25r3LegCost(t, leg, 1, 100)
		staffCtx := w25r3StaffCtx(t, leg)
		moveID, receiptID := w25r8Receive(t, leg, staffCtx, w25Year, w25Month, leg.productID, leg.branchID, 2, 101)
		w25Drain(t, leg)
		for _, key := range []string{
			"po.received:" + receiptID,
			"po.received.rounding:" + receiptID,
		} {
			w25r8AssertCarriesTheMovementsInstant(t, leg, key, moveID)
		}
	})

	t.Run("the receipt's GRNI and pre-receipt true-up", func(t *testing.T) {
		leg := w25Setup(t, "r8trueup")
		ctx := context.Background()
		// A product of its own, carrying no cost anywhere, so the sale before
		// its first receipt is relieved provisionally, the move marked and
		// awaiting true-up, and the receipt trues the sale up.
		var productID string
		if err := leg.pool.QueryRow(ctx, `
			INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg)
			VALUES ('W2-5-R8-TU-' || substr(md5(random()::text), 1, 8), 'W2-5 r8 true up product', 'EA', 'EA', 2.0)
			RETURNING id::text`).Scan(&productID); err != nil {
			t.Fatalf("the leg's own product: %v", err)
		}
		var customerID string
		if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
			t.Fatalf("a seeded customer: %v", err)
		}
		var orderID string
		if err := leg.pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
			VALUES ('W2-5-R8-' || substr(md5(random()::text), 1, 8), $1, $2, 'FULFILLED', 'PICKUP', 0, 0)
			RETURNING id::text`, customerID, leg.branchID).Scan(&orderID); err != nil {
			t.Fatalf("the pre-receipt sale: %v", err)
		}
		tx, err := leg.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the pre-receipt sale: %v", err)
		}
		if err := leg.money.inventorySvc.FulfilOrderLinesTx(ctx, tx, orderID, leg.branchID, []string{productID}, []float64{1}); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("the pre-receipt sale: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit the pre-receipt sale: %v", err)
		}
		w25Drain(t, leg)

		staffCtx := w25r3StaffCtx(t, leg)
		moveID, receiptID := w25r8Receive(t, leg, staffCtx, w25Year, w25Month, productID, leg.branchID, 2, 101)
		w25Drain(t, leg)
		for _, key := range []string{
			"po.received:" + receiptID,
			"pre_receipt_trueup:" + receiptID + ":" + productID,
		} {
			w25r8AssertCarriesTheMovementsInstant(t, leg, key, moveID)
		}
	})

	t.Run("the move rounding", func(t *testing.T) {
		leg := w25Setup(t, "r8move")
		ctx := context.Background()
		// Twelve and a bit tonnes at 4510 cents, a fraction sold: the row's
		// exact change and the movement differ by a cent, and the movement's
		// own road raises the rounding.
		w25r3LegCost(t, leg, 12.38, w25r5Avg)
		var customerID string
		if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM customers ORDER BY id LIMIT 1`).Scan(&customerID); err != nil {
			t.Fatalf("a seeded customer: %v", err)
		}
		var orderID string
		if err := leg.pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
			VALUES ('W2-5-R8-MOVE-' || substr(md5(random()::text), 1, 8), $1, $2, 'FULFILLED', 'PICKUP', $3, $3)
			RETURNING id::text`, customerID, leg.branchID, w25r5Value).Scan(&orderID); err != nil {
			t.Fatalf("the sale: %v", err)
		}
		tx, err := leg.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the fulfilment: %v", err)
		}
		if err := leg.money.inventorySvc.FulfilOrderLinesTx(ctx, tx, orderID, leg.branchID, []string{leg.productID}, []float64{w25r5FracQty}); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("the fulfilment: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit the fulfilment: %v", err)
		}
		moveID, _ := w25r5MoveOf(t, leg, orderID)
		w25r8AssertCarriesTheMovementsInstant(t, leg, "inventory.ship.rounding:"+moveID, moveID)
	})

	t.Run("the count's variance journal", func(t *testing.T) {
		leg := w25Setup(t, "r8count")
		ctx := context.Background()
		// The count finds a fraction short: the count's own road writes the COUNT
		// move and raises the variance's rounding journal in the same transaction.
		w25r3LegCost(t, leg, 12.38, w25r5Avg)
		tx, err := leg.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the count: %v", err)
		}
		moveID, err := leg.money.inventorySvc.PostCountAdjustmentTx(ctx, tx, leg.productID, leg.branchID, leg.branchID, -w25r5FracQty, w25r5Avg, "SHRINK")
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("the count: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit the count: %v", err)
		}
		if moveID == "" {
			t.Fatalf("the count wrote no move")
		}
		w25r8AssertCarriesTheMovementsInstant(t, leg, "inventory.count.rounding:"+moveID, moveID)
	})

	t.Run("the transfer's legs and the receive rounding", func(t *testing.T) {
		leg := w25Setup(t, "r8transfer")
		ctx := context.Background()
		// The source holds two at 101 and the destination one at 100: the
		// receive blends them and raises the transfer's rounding. The world is
		// built through the production roads (a receipt at the destination
		// branch), so this leg writes no inventory value of its own.
		var branches []string
		rows, err := leg.pool.Query(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 2`)
		if err != nil {
			t.Fatalf("two seeded branches: %v", err)
		}
		for rows.Next() {
			var b string
			if err := rows.Scan(&b); err != nil {
				t.Fatalf("a seeded branch: %v", err)
			}
			branches = append(branches, b)
		}
		rows.Close()
		if len(branches) < 2 {
			t.Fatalf("the transfer leg needs two branches, the seed holds %d", len(branches))
		}
		srcBranch, dstBranch := branches[0], branches[1]
		w25r3LegCost(t, leg, 2, 101)

		staffCtx := w25r3StaffCtx(t, leg)
		w25r8Receive(t, leg, staffCtx, w25Year, w25Month, leg.productID, dstBranch, 1, 100)
		w25Drain(t, leg)

		var userID string
		if err := leg.pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
			t.Fatalf("a seeded user: %v", err)
		}
		tr, err := leg.money.inventorySvc.CreateTransfer(ctx, inventory.CreateTransferInput{
			FromBranchID: srcBranch, ToBranchID: dstBranch,
			Lines:     []inventory.CreateTransferLine{{ProductID: leg.productID, QtyBase: 2}},
			CreatedBy: userID,
		})
		if err != nil {
			t.Fatalf("create the transfer: %v", err)
		}
		if _, err := leg.money.inventorySvc.ShipTransfer(ctx, tr.ID, userID); err != nil {
			t.Fatalf("ship the transfer: %v", err)
		}
		var shipMoveID string
		if err := leg.pool.QueryRow(ctx, `
			SELECT id::text FROM stock_moves
			 WHERE source_ref_id = $1::uuid AND source_type = 'stock_transfer' AND to_location_id IS NULL`, tr.ID).Scan(&shipMoveID); err != nil {
			t.Fatalf("the transfer's outbound move: %v", err)
		}
		w25r8AssertCarriesTheMovementsInstant(t, leg, "inventory.transfer_shipped:"+tr.ID, shipMoveID)

		if _, err := leg.money.inventorySvc.ReceiveTransfer(ctx, tr.ID, userID); err != nil {
			t.Fatalf("receive the transfer: %v", err)
		}
		var receiveMoveID string
		if err := leg.pool.QueryRow(ctx, `
			SELECT id::text FROM stock_moves
			 WHERE source_type = 'stock_transfer' AND to_location_id IS NOT NULL AND source_ref_id = $1::uuid
			 ORDER BY occurred_at DESC LIMIT 1`, tr.ID).Scan(&receiveMoveID); err != nil {
			t.Fatalf("the transfer's inbound move: %v", err)
		}
		w25r8AssertCarriesTheMovementsInstant(t, leg, "inventory.transfer_received:"+tr.ID, receiveMoveID)
		w25r8AssertCarriesTheMovementsInstant(t, leg, "inventory.transfer_received.rounding:"+tr.ID, receiveMoveID)
	})
}

// TestW25R8TheMonthWalkStillWalks is the month walk: the leg's months still walk.
// The closing month seals with the receipt in it, and the running month after it
// seals too, so the leg's receipt does not leave the books in a state that stops
// the walk at the next month.
func TestW25R8TheMonthWalkStillWalks(t *testing.T) {
	frame := w25r8Leg(t, "walk")
	leg := frame.leg
	ctx := context.Background()

	staffCtx := w25r3StaffCtx(t, leg)
	moveID, receiptID := w25r8Receive(t, leg, staffCtx, frame.year, frame.month, leg.productID, leg.branchID, 2, w25UnitCost)
	w25r8CarryTheReceiptInstant(t, leg, moveID, receiptID, frame.receiptAt)
	w25Drain(t, leg)

	code, body := w25Close(t, leg, frame.year, frame.month)
	if code != http.StatusOK {
		t.Fatalf("the close of the last ended month was refused by a receipt in its last moment: %d: %s", code, body)
	}

	// The running month, the month after the one just sealed: the house's own
	// next step. The world ties at its end and it seals too.
	runningYear, runningMonth := frame.year, frame.month+1
	if runningMonth == 13 {
		runningMonth, runningYear = 1, runningYear+1
	}
	runningEnd := frame.monthEnd.AddDate(0, 1, 0)
	runningOpen := frame.monthEnd.Add(12 * time.Hour)
	w25r8Periods(t, leg, runningYear)
	w25r8TieTheARAtItsEnd(t, leg, runningOpen, runningEnd)
	w25TieTheMonthAtItsEnd(t, leg, runningOpen, runningEnd)

	code, body = w25Close(t, leg, runningYear, runningMonth)
	if code != http.StatusOK {
		t.Fatalf("the close of the running month was refused after the last ended month sealed: %d: %s", code, body)
	}
	if status := w25PeriodStatus(t, leg, runningYear, runningMonth); status != "CLOSED" {
		t.Fatalf("the running month's period reads %s after the close, want CLOSED", status)
	}
	_ = ctx
}
