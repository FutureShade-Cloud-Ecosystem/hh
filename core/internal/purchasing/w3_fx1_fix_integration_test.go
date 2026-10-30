//go:build integration

package purchasing

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// legacyBackfillSQL returns the instalment backfill UPDATE exactly as migration 0462 ships it, so a
// test replays the real statement on a row written the way PayBill wrote it before the migration.
func legacyBackfillSQL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../db/migrations/0462_fx_realised_gain_loss.sql")
	if err != nil {
		t.Fatalf("read migration 0462: %v", err)
	}
	m := regexp.MustCompile(`(?s)\nUPDATE purchasing_bill_instalments.*?;`).FindString(string(raw))
	if strings.TrimSpace(m) == "" {
		t.Fatal("0462 has no instalment backfill UPDATE")
	}
	return m
}

// P2-1: a USD bill part paid before 0462 holds CAD in amount_cents. The backfill converts that CAD to
// USD at the bill's own rate, so the USD still owed is accepted and no FX posts at an unchanged rate.
func TestIntegration_W3FX1Fix_LegacyPartPaidUSDBillBackfill(t *testing.T) {
	rig := newAPAgingRig(t)
	now := time.Now().In(rig.zone)
	rig.ensurePeriod(t, now.Year(), int(now.Month()))
	day := now.Format("2006-01-02")
	bill := rig.postUSDBillAt(t, "fx1f-legacy", day, now.AddDate(0, 0, 30).Format("2006-01-02"), 100_000, 1.35, now)

	if _, err := rig.pool.Exec(rig.ctx, `INSERT INTO purchasing_bill_instalments (bill_id, seq, due_date, amount_cents, doc_amount_cents, fx_rate, status, paid_at, note)
		VALUES ($1, 1, $2::date, 67500, 0, NULL, 'PAID', now(), 'legacy CAD payment')`, bill.ID, day); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.pool.Exec(rig.ctx, legacyBackfillSQL(t)); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var docPaid int64
	if err := rig.pool.QueryRow(rig.ctx, `SELECT doc_amount_cents FROM purchasing_bill_instalments WHERE bill_id = $1 AND seq = 1`, bill.ID).Scan(&docPaid); err != nil {
		t.Fatal(err)
	}
	if docPaid != 50_000 {
		t.Fatalf("backfilled paid USD = %d, want 50000 (CAD 675.00 at 1.35)", docPaid)
	}

	key := "fx1f-legacy-pay-" + bill.ID
	if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: 50_000, FXRate: 1.35, PaidAt: now.Add(time.Minute), IdempotencyKey: key}); err != nil {
		t.Fatalf("pay the USD 500.00 still owed: %v", err)
	}
	legs := rig.paymentJournalLegs(t, key)
	if fx, ok := legs["6310"]; ok && (fx[0] != 0 || fx[1] != 0) {
		t.Fatalf("an unchanged rate posted FX %v", fx)
	}
	if got := legs["2100"]; got != [2]int64{67_500, 0} {
		t.Fatalf("2100 = %v, want DR 67500", got)
	}
	// The legacy row was written straight to the table, so it has no journal; the instalments alone
	// must clear the bill's whole CAD.
	var cleared int64
	if err := rig.pool.QueryRow(rig.ctx, `SELECT COALESCE(SUM(amount_cents), 0) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`, bill.ID).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if cleared != bill.TotalCents {
		t.Fatalf("cleared CAD %d, want the bill's %d", cleared, bill.TotalCents)
	}
}

// P2-2: the bill read and the AP aging item return the document currency, its total and the open
// document currency amount; paying that open amount read back settles the bill to zero in 2100.
func TestIntegration_W3FX1Fix_OpenDocumentAmountPaysBillToZero(t *testing.T) {
	rig := newAPAgingRig(t)
	now := time.Now().In(rig.zone)
	rig.ensurePeriod(t, now.Year(), int(now.Month()))
	day := now.Format("2006-01-02")
	bill := rig.postUSDBillAt(t, "fx1f-open", day, now.AddDate(0, 0, 30).Format("2006-01-02"), 100_001, 1.3333, now)

	if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: 33_333, FXRate: 1.3333, PaidAt: now.Add(time.Minute), IdempotencyKey: "fx1f-open-a-" + bill.ID}); err != nil {
		t.Fatal(err)
	}
	detail, err := rig.svc.GetBill(rig.ctx, bill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Currency != "USD" || detail.DocTotalCents != 100_001 || detail.DocOpenCents != 66_668 {
		t.Fatalf("bill read: currency %q doc total %d doc open %d, want USD 100001 66668", detail.Currency, detail.DocTotalCents, detail.DocOpenCents)
	}

	page, err := rig.svc.APAging(rig.ctx, now.Add(2*time.Minute), BranchReach{BranchIDs: []string{branchTRT}})
	if err != nil {
		t.Fatal(err)
	}
	var item *APAgingItem
	for _, row := range page.Items {
		for i := range row.OpenItems {
			if row.OpenItems[i].ID == bill.ID {
				item = &row.OpenItems[i]
			}
		}
	}
	if item == nil {
		t.Fatal("bill missing from the aging")
	}
	if item.Currency != "USD" || item.DocTotalCents != 100_001 || item.DocOpenCents != detail.DocOpenCents {
		t.Fatalf("aging item: currency %q doc total %d doc open %d", item.Currency, item.DocTotalCents, item.DocOpenCents)
	}

	if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: item.DocOpenCents, FXRate: 1.3333, PaidAt: now.Add(3 * time.Minute), IdempotencyKey: "fx1f-open-b-" + bill.ID}); err != nil {
		t.Fatalf("pay the open document amount: %v", err)
	}
	if got := rig.billNetAP2100Cents(t, bill.ID); got != 0 {
		t.Fatalf("2100 residual %d after paying the open document amount", got)
	}
	if got := rig.billOpenInAgingCents(t, bill.ID, now.Add(4*time.Minute)); got != 0 {
		t.Fatalf("aging still shows CAD %d open", got)
	}
}

// P3-2, as S9 ruled on PR 717: the cash is always what the bank pays, even at the bill's own rate.
// 100 units at USD 10.01 and 1.3333 carry CAD 13.35 a unit, CAD 1,335.00 the bill; the bank pays USD
// 1,001.00 at 1.3333, CAD 1,334.63. So 1000 is credited 1,334.63, 2100 clears 1,335.00, and the
// 0.37 of rounding at bill entry posts to 6310. One pip worse for the dealer (1.3334) books more
// cash, never less.
func TestIntegration_W3FX1Fix_CashIsWhatTheBankPaysAtAnyRate(t *testing.T) {
	rig := newAPAgingRig(t)
	now := time.Now().In(rig.zone)
	rig.ensurePeriod(t, now.Year(), int(now.Month()))
	cash := map[float64]int64{}
	for _, tc := range []struct {
		rate       float64
		wantCashCr int64
		wantFXCr   int64
	}{
		{1.3333, 133_463, 37},
		{1.3334, 133_473, 27},
	} {
		tag := fmt.Sprintf("fx1f-cash-%v", tc.rate)
		bill := rig.postUSDBillQtyAt(t, tag, now.Format("2006-01-02"), now.AddDate(0, 0, 30).Format("2006-01-02"), 1001, 100, 1.3333, now)
		key := tag + "-" + bill.ID
		if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: bill.DocTotalCents, FXRate: tc.rate, PaidAt: now.Add(time.Minute), IdempotencyKey: key}); err != nil {
			t.Fatal(err)
		}
		legs := rig.paymentJournalLegs(t, key)
		if got := legs["1000"][1]; got != tc.wantCashCr {
			t.Fatalf("rate %v: 1000 credited %d, want the bank's %d (legs %v)", tc.rate, got, tc.wantCashCr, legs)
		}
		if got := legs["6310"]; got[1]-got[0] != tc.wantFXCr {
			t.Fatalf("rate %v: 6310 net credit %d, want %d (legs %v)", tc.rate, got[1]-got[0], tc.wantFXCr, legs)
		}
		if got := rig.billNetAP2100Cents(t, bill.ID); got != 0 {
			t.Fatalf("rate %v: 2100 residual %d", tc.rate, got)
		}
		cash[tc.rate] = legs["1000"][1]
	}
	if cash[1.3334] <= cash[1.3333] {
		t.Fatalf("a worse rate booked less cash: %v", cash)
	}
}

// P3-3: a payment rate far from the bill's own, or with more than six decimals, is refused in words.
func TestIntegration_W3FX1Fix_PaymentRateBoundAndDecimals(t *testing.T) {
	rig := newAPAgingRig(t)
	now := time.Now().In(rig.zone)
	rig.ensurePeriod(t, now.Year(), int(now.Month()))
	bill := rig.postUSDBillAt(t, "fx1f-rate", now.Format("2006-01-02"), now.AddDate(0, 0, 30).Format("2006-01-02"), 100_000, 1.35, now)
	for _, tc := range []struct {
		name string
		rate float64
		want string
	}{
		{"typed 138 for 1.38", 138, "fx_rate"},
		{"far below", 0.5, "fx_rate"},
		{"seven decimals", 1.3512345, "decimal"},
	} {
		_, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: 1000, FXRate: tc.rate, PaidAt: now.Add(time.Minute), IdempotencyKey: "fx1f-rate-" + tc.name + bill.ID})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q", tc.name, err, tc.want)
			continue
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusBadRequest {
			t.Errorf("%s: err %v is not a 400", tc.name, err)
		}
	}
	if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: 1000, FXRate: 1.351234, PaidAt: now.Add(2 * time.Minute), IdempotencyKey: "fx1f-rate-ok-" + bill.ID}); err != nil {
		t.Fatalf("six decimals near the bill's rate: %v", err)
	}
}

// P3-4: the migration's own error text names its own number.
func TestW3FX1Fix_MigrationErrorNamesItself(t *testing.T) {
	raw, err := os.ReadFile("../../db/migrations/0462_fx_realised_gain_loss.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "'0454:") || !strings.Contains(string(raw), "'0462:") {
		t.Fatal("0462's RAISE EXCEPTION text must name 0462")
	}
}
