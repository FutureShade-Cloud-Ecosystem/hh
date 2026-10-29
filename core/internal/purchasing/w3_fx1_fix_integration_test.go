//go:build integration

package purchasing

import (
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
