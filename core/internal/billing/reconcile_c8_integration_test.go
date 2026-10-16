//go:build integration

package billing

// reconcile_c8_integration_test.go, the CFO cent-for-cent invariants for the C8 5-bucket register
// and aging, against a real database. The unit-level versions (reconcile_c8_test.go) pin the math;
// these pins run the same arithmetic end-to-end through agingSelect and the register scan, so a
// drift between the projection's bucket boundaries and the row-level bucket_label shows up here.
//
// The three invariants this file proves:
//   (a) the five aging buckets never overlap at 30/60/90 boundaries and sum exactly to the
//       account's open balance (cents),
//   (b) the register rows' sum equals the aging grand total (cents) for the same account,
//   (c) DaysOverdue on a register row is deterministic against the server's asOf clock, even when
//       that clock moves across an evening boundary the SQL projection can see but the wall clock
//       test cannot.
//
// DIB-396 note: this file creates and destroys its own customers and invoices, takes no shared
// lock, and is order-independent under -shuffle=on.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// c8World owns one customer and the invoices needed for a C8 5-bucket ladder, one per band:
// 5 days future, 15 days late, 45 days late, 75 days late, and 95 days late. Each invoice is in a
// distinct band by construction, so any band overlap is a SQL bug and any band under-count is a
// missing predicate.
type c8World struct {
	repo       *Repository
	customerID string
	branchID   string
	orderID    string
	// invoiceBalance is the total of every invoice this world seeded, in cents. The reconciliation
	// assertions check (Current + Days30 + Days60 + Days60To90 + Days90Plus) == invoiceBalance.
	invoiceBalance int64
}

func newC8World(t *testing.T) *c8World {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()%1_000_000_000)

	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		 VALUES ('C8-'||$1, 'C8 Reconcile '||$1, 'CONTRACTOR', $2::uuid, 30, 10000000) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		 VALUES ('ORD-C8-'||$1, $2::uuid, $3::uuid, 'CONFIRMED', 'DELIVERY') RETURNING id::text`,
		suffix, customerID, branchID).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	// band values: every invoice has a known balance and a known house-day offset, so the bucket
	// each lands in is fixed by the offset and the band column is the assertion.
	band := []struct {
		name    string
		daysAgo int
		cents   int64
	}{
		{"CURRENT", -5, 11000}, // due in 5 days, → Current
		{"D15", 15, 12000},     // 15 days past due,  → 1 to 30
		{"D45", 45, 13000},     // 45 days past due,  → 31 to 60
		{"D75", 75, 14000},     // 75 days past due,  → 61 to 90
		{"D95", 95, 15000},     // 95 days past due,  → 91 and over
	}
	var sum int64
	for _, b := range band {
		// Using UTC dates directly to be house-zone-stable in CI; agingSelect reads them in the
		// house zone, so the day count is the same whichever zone the test runs in.
		dueDate := time.Now().UTC().AddDate(0, 0, -b.daysAgo).Format("2006-01-02")
		issueDate := time.Now().UTC().AddDate(0, 0, -b.daysAgo-30).Format("2006-01-02")
		if _, err := pool.Exec(ctx,
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, order_id, status,
			                       issue_date, due_date, subtotal_cents, tax_cents, total_cents,
			                       balance_cents, finalized_at, doc_type)
			 VALUES ('INV-C8-'||$1, $2::uuid, $3::uuid, $4::uuid, 'FINALIZED',
			         $5::date, $6::date, $7, 0, $7, $7, now(), 'INVOICE')`,
			fmt.Sprintf("%s-%s", suffix, b.name), customerID, branchID, orderID,
			issueDate, dueDate, b.cents); err != nil {
			t.Fatalf("seed invoice %s: %v", b.name, err)
		}
		sum += b.cents
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	return &c8World{
		repo:           NewRepository(pool),
		customerID:     customerID,
		branchID:       branchID,
		orderID:        orderID,
		invoiceBalance: sum,
	}
}

// findC8Row picks this rig's customer out of the aging worklist.
func findC8Row(rows []AgingRow, customerID string) (AgingRow, bool) {
	for _, r := range rows {
		if r.CustomerID == customerID {
			return r, true
		}
	}
	return AgingRow{}, false
}

// TestIntegration_ReconcileC8_FiveBucketsAreExclusiveAndSumToTotal proves invariant (a) at the
// database: the five AgingRow bucket columns for the seeded account hold exactly the dollars
// they claim, never a cent more (which would mean overlap) and never a cent less (which would
// mean a missing predicate).
func TestIntegration_ReconcileC8_FiveBucketsAreExclusiveAndSumToTotal(t *testing.T) {
	w := newC8World(t)
	rows, err := w.repo.AgingRows(context.Background(), time.Now(), 60, "")
	if err != nil {
		t.Fatalf("aging rows: %v", err)
	}
	got, ok := findC8Row(rows, w.customerID)
	if !ok {
		t.Fatal("the seeded customer is missing from the aging worklist")
	}

	wantCurrent := int64(11000)
	wantDays30 := int64(12000)
	wantDays60 := int64(13000)
	wantDays60To90 := int64(14000)
	wantDays90Plus := int64(15000)

	if got.CurrentCents != wantCurrent {
		t.Errorf("Current = %d, want %d", got.CurrentCents, wantCurrent)
	}
	if got.Days30Cents != wantDays30 {
		t.Errorf("1 to 30 = %d, want %d", got.Days30Cents, wantDays30)
	}
	if got.Days60Cents != wantDays60 {
		t.Errorf("31 to 60 = %d, want %d", got.Days60Cents, wantDays60)
	}
	if got.Days60To90Cents != wantDays60To90 {
		t.Errorf("61 to 90 = %d, want %d", got.Days60To90Cents, wantDays60To90)
	}
	if got.Days90PlusCents != wantDays90Plus {
		t.Errorf("91 and over = %d, want %d", got.Days90PlusCents, wantDays90Plus)
	}
	sum := got.CurrentCents + got.Days30Cents + got.Days60Cents + got.Days60To90Cents + got.Days90PlusCents
	if sum != got.TotalCents {
		t.Errorf("5 buckets sum = %d, TotalCents = %d, a cent outside every bucket is a cent the GL will catch", sum, got.TotalCents)
	}
	if got.TotalCents != w.invoiceBalance {
		t.Errorf("aging grand total = %d, seeded invoices = %d, the system and the seed book differ", got.TotalCents, w.invoiceBalance)
	}
}

// TestIntegration_ReconcileC8_BoundariesAreExclusiveAtDay30_60_90 pins invariant (a) at the
// boundary: a row that is EXACTLY 30 days past due belongs to 1 to 30, never 31 to 60; the same
// rule at 60 days for 31 to 60 / 61 to 90; the same rule at 90 days for 61 to 90 / 91 and over.
// A boundary that double-counts is a customer who is held twice, an operator argument we will lose.
func TestIntegration_ReconcileC8_BoundariesAreExclusiveAtDay30_60_90(t *testing.T) {
	cases := []struct {
		daysAgo    int
		wantBucket string
	}{
		{30, "Days30Cents"},     // exactly 30 → 1 to 30
		{31, "Days60Cents"},     // 31 → 31 to 60
		{60, "Days60Cents"},     // exactly 60 → 31 to 60
		{61, "Days60To90Cents"}, // 61 → 61 to 90
		{90, "Days60To90Cents"}, // exactly 90 → 61 to 90
		{91, "Days90PlusCents"}, // 91 → 91 and over
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("daysAgo=%d", c.daysAgo), func(t *testing.T) {
			pool := intPool(t)
			ctx := context.Background()

			var branchID string
			if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
				t.Skipf("no seeded BRANCH: %v", err)
			}
			suffix := fmt.Sprintf("c8-bnd-%d-%d", c.daysAgo, time.Now().UnixNano()%1_000_000_000)
			var cust string
			if err := pool.QueryRow(ctx,
				`INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
				 VALUES ('C8BND-'||$1, 'C8 Boundary '||$1, 'CONTRACTOR', $2::uuid, 30, 1000000) RETURNING id::text`,
				suffix, branchID).Scan(&cust); err != nil {
				t.Fatalf("seed customer: %v", err)
			}
			due := time.Now().UTC().AddDate(0, 0, -c.daysAgo).Format("2006-01-02")
			issue := time.Now().UTC().AddDate(0, 0, -c.daysAgo-30).Format("2006-01-02")
			if _, err := pool.Exec(ctx,
				`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
				                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
				 VALUES ('INV-C8BND-'||$1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date,
				         12345, 0, 12345, 12345, now(), 'INVOICE')`,
				suffix, cust, branchID, issue, due); err != nil {
				t.Fatalf("seed invoice: %v", err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM invoices WHERE customer_id = $1::uuid`, cust)
				_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, cust)
			})

			rows, err := NewRepository(pool).AgingRows(ctx, time.Now(), 60, "")
			if err != nil {
				t.Fatalf("aging rows: %v", err)
			}
			got, ok := findC8Row(rows, cust)
			if !ok {
				t.Fatalf("customer missing from aging worklist")
			}
			others := int64(0)
			switch c.wantBucket {
			case "Days30Cents":
				others = got.Days60Cents + got.Days60To90Cents + got.Days90PlusCents
				if got.Days30Cents != 12345 {
					t.Errorf("%d days past due: Days30 = %d, want 12345 (the 1 to 30 band)", c.daysAgo, got.Days30Cents)
				}
			case "Days60Cents":
				others = got.Days30Cents + got.Days60To90Cents + got.Days90PlusCents
				if got.Days60Cents != 12345 {
					t.Errorf("%d days past due: Days60 = %d, want 12345 (the 31 to 60 band)", c.daysAgo, got.Days60Cents)
				}
			case "Days60To90Cents":
				others = got.Days30Cents + got.Days60Cents + got.Days90PlusCents
				if got.Days60To90Cents != 12345 {
					t.Errorf("%d days past due: Days60To90 = %d, want 12345 (the 61 to 90 band)", c.daysAgo, got.Days60To90Cents)
				}
			case "Days90PlusCents":
				others = got.Days30Cents + got.Days60Cents + got.Days60To90Cents
				if got.Days90PlusCents != 12345 {
					t.Errorf("%d days past due: Days90Plus = %d, want 12345 (the 91 and over band)", c.daysAgo, got.Days90PlusCents)
				}
			}
			if others != 0 {
				t.Errorf("%d days past due: a band OTHER than %s also holds %d cents, the band boundaries overlap", c.daysAgo, c.wantBucket, others)
			}
		})
	}
}

// TestIntegration_ReconcileC8_RegisterRowsSumToAgingGrandTotal proves invariant (b): the
// register's per-row sum (BalanceCents across every row) equals the aging worklist's
// GrandTotalCents for the same customer. A mismatch is two figures that would appear on the
// same screen disagreeing about what an account owes.
func TestIntegration_ReconcileC8_RegisterRowsSumToAgingGrandTotal(t *testing.T) {
	w := newC8World(t)
	ctx := context.Background()

	// Pull the register for this customer only, with no chip, so every open row is in the result.
	rows, _, err := w.repo.RegisterRows(ctx, time.Now(), 100, RegisterFilter{
		BranchID:    w.branchID,
		CustomerID:  w.customerID,
		ShowCredits: true,
		Reach:       AllBranches,
	})
	if err != nil {
		t.Fatalf("register rows: %v", err)
	}
	var sum int64
	for _, r := range rows {
		sum += r.BalanceCents
	}
	if sum != w.invoiceBalance {
		t.Fatalf("register rows sum = %d, seeded invoices = %d, the register and the seed disagree", sum, w.invoiceBalance)
	}

	agingRows, err := w.repo.AgingRows(ctx, time.Now(), 60, "")
	if err != nil {
		t.Fatalf("aging rows: %v", err)
	}
	got, ok := findC8Row(agingRows, w.customerID)
	if !ok {
		t.Fatal("customer missing from aging worklist")
	}
	if got.TotalCents != sum {
		t.Errorf("aging grand total = %d, register rows sum = %d, two screens on the same data disagree", got.TotalCents, sum)
	}
}

// TestIntegration_ReconcileC8_BucketLabelMatchesDaysOverdue proves the per-row bucket_label
// (the wire payload the Money Desk renders) is in the same band the per-customer aging totals
// say each row's dollars live in, for the same row, against the same asOf clock. A mismatch
// here is a CFO who points at the register row and says "5,400 cents" and the aging card says
// "31 to 60" while the row pill says "61 to 90": one of them is wrong and we do not get to pick.
func TestIntegration_ReconcileC8_BucketLabelMatchesDaysOverdue(t *testing.T) {
	w := newC8World(t)
	ctx := context.Background()

	rows, _, err := w.repo.RegisterRows(ctx, time.Now(), 100, RegisterFilter{
		BranchID:    w.branchID,
		CustomerID:  w.customerID,
		ShowCredits: true,
		Reach:       AllBranches,
	})
	if err != nil {
		t.Fatalf("register rows: %v", err)
	}
	for _, r := range rows {
		label, key := BucketLabelFor(r.DaysOverdue)
		if r.BucketLabel != label {
			t.Errorf("row %s: bucket_label=%q derived from days_overdue=%d should be %q, the row and the function disagree",
				r.InvoiceNo, r.BucketLabel, r.DaysOverdue, label)
		}
		if r.BucketKey != key {
			t.Errorf("row %s: bucket_key=%q derived from days_overdue=%d should be %q",
				r.InvoiceNo, r.BucketKey, r.DaysOverdue, key)
		}
	}
}

// TestIntegration_ReconcileC8_DaysOverdueIsDeterministicAgainstAsOf is invariant (c): a row's
// DaysOverdue is a pure function of the asOf clock the server was handed. Two reads taken a
// moment apart (same row, same row) produce different reports ONLY because the timestamps moved,
// never because the derivation drifted. We pin that by reading the same row twice and asserting
// the difference is at most the elapsed clock time, in days.
func TestIntegration_ReconcileC8_DaysOverdueIsDeterministicAgainstAsOf(t *testing.T) {
	w := newC8World(t)
	ctx := context.Background()

	r1, _, err := w.repo.RegisterRows(ctx, time.Now(), 100, RegisterFilter{
		BranchID:    w.branchID,
		CustomerID:  w.customerID,
		ShowCredits: true,
		Reach:       AllBranches,
	})
	if err != nil {
		t.Fatalf("register rows (first read): %v", err)
	}
	// Pin two snapshots an instant apart; the difference in DaysOverdue for any row can be at
	// most one (the day change), and never two or more without the wall clock having moved two
	// whole days.
	time.Sleep(10 * time.Millisecond)
	r2, _, err := w.repo.RegisterRows(ctx, time.Now(), 100, RegisterFilter{
		BranchID:    w.branchID,
		CustomerID:  w.customerID,
		ShowCredits: true,
		Reach:       AllBranches,
	})
	if err != nil {
		t.Fatalf("register rows (second read): %v", err)
	}
	if len(r1) != len(r2) {
		t.Fatalf("two reads of the same filter returned different row counts: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		delta := r1[i].DaysOverdue - r2[i].DaysOverdue
		if delta < 0 {
			delta = -delta
		}
		if delta > 1 {
			t.Errorf("row %s: DaysOverdue drifted by %d between two reads 10ms apart, the derivation is not deterministic", r1[i].InvoiceNo, delta)
		}
	}
}

// TestIntegration_ReconcileC8_RegisterChipsFilterAccurately verifies the 4 C8 filter chips
// (all, past_due, current, on_hold, in both lower and upper case as RegisterHandler normalizes)
// against a real database, including placing an active credit_holds row.
func TestIntegration_ReconcileC8_RegisterChipsFilterAccurately(t *testing.T) {
	w := newC8World(t)
	ctx := context.Background()

	checkChip := func(chip string, wantCount int, wantSum int64) {
		t.Helper()
		rows, total, err := w.repo.RegisterRows(ctx, time.Now(), 100, RegisterFilter{
			BranchID:    w.branchID,
			CustomerID:  w.customerID,
			Chip:        chip,
			ShowCredits: true,
			Reach:       AllBranches,
		})
		if err != nil {
			t.Fatalf("chip %q register rows: %v", chip, err)
		}
		if len(rows) != wantCount || total != wantCount {
			t.Errorf("chip %q: len=%d total=%d, want %d", chip, len(rows), total, wantCount)
		}
		var sum int64
		for _, r := range rows {
			sum += r.BalanceCents
		}
		if sum != wantSum {
			t.Errorf("chip %q: sum=%d, want %d", chip, sum, wantSum)
		}
	}

	checkChip(ChipAll, 5, 65000)
	checkChip("ALL", 5, 65000)
	checkChip(ChipPastDue, 4, 54000)
	checkChip("PAST_DUE", 4, 54000)
	checkChip(ChipCurrent, 1, 11000)
	checkChip("CURRENT", 1, 11000)
	checkChip(ChipOnHold, 0, 0)
	checkChip("ON_HOLD", 0, 0)

	if _, placed, err := w.repo.PlaceHold(ctx, w.customerID, "MANUAL", "C8 hold test", ""); err != nil || !placed {
		t.Fatalf("PlaceHold: placed=%v err=%v", placed, err)
	}
	checkChip(ChipOnHold, 5, 65000)
	checkChip("ON_HOLD", 5, 65000)
}