//go:build integration

package billing

// ar_aging_zone_integration_test.go, the AR aging view on the DEALER'S day (migration 0394).
//
// Migration 0376 gave the view the Ontario day. Billing's own aging reads bind the dealer's
// business_zone, so for a Pacific dealer the view (read by finstmt and both reporting aging
// reports) and billing disagreed through the evening window. These legs set the dealer's zone in
// system_settings and read the view.
//
// No test can pin the database's now(), so the legs pick an instant-independent shape:
//   - the INVOICE arm picks, from two zones a whole day apart from Toronto in opposite directions,
//     the one whose calendar day differs from Toronto's right now (there is always one), and files
//     invoices due on that zone's today and its yesterday. The view must age them 0 and 1 at any
//     hour the suite runs, which the Toronto day cannot do.
//   - the TRANSACTION arm files an instant at 23:00 in Los Angeles three of its days back, which
//     is already the next day in Toronto, and expects the Pacific age of 3 at any hour.

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// zoneAgingSource answers one fixed zone, as the composition root's adapter would for a dealer.
type zoneAgingSource struct{ z houseday.Zone }

func (s zoneAgingSource) BusinessZone(context.Context) (houseday.Zone, error) { return s.z, nil }

func loadAging(store *AgingStore, customerID, id string) (sync.RowState, bool, error) {
	return store.Load(context.Background(), sync.ScopeFilter{Tenant: true, TenantID: customerID}, id)
}

// setDealerZone writes the business_zone setting for the length of the test and restores what was
// there (the row, or its absence) at the end. raw is the jsonb text, so a test can store a number or
// an object as well as a string.
func setDealerZone(t *testing.T, pool *pgxpool.Pool, raw string) {
	t.Helper()
	ctx := context.Background()
	var prior *string
	err := pool.QueryRow(ctx, `SELECT value::text FROM system_settings WHERE key = 'business_zone'`).Scan(&prior)
	had := err == nil
	if err != nil && err != pgx.ErrNoRows {
		t.Fatalf("read the prior business_zone: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		if had {
			_, _ = pool.Exec(c, `UPDATE system_settings SET value = $1::jsonb WHERE key = 'business_zone'`, *prior)
		} else {
			_, _ = pool.Exec(c, `DELETE FROM system_settings WHERE key = 'business_zone'`)
		}
	})
	if raw == "" {
		if _, err := pool.Exec(ctx, `DELETE FROM system_settings WHERE key = 'business_zone'`); err != nil {
			t.Fatalf("clear business_zone: %v", err)
		}
		return
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ('business_zone', $1::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, raw); err != nil {
		t.Fatalf("set business_zone %s: %v", raw, err)
	}
}

// otherDayZone names a zone whose calendar day differs from Toronto's at this instant.
func otherDayZone(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var z string
	err := pool.QueryRow(context.Background(), `
		SELECT z FROM unnest(ARRAY['Pacific/Pago_Pago','Pacific/Kiritimati']) AS z
		 WHERE (now() AT TIME ZONE z)::date <> (now() AT TIME ZONE 'America/Toronto')::date
		 LIMIT 1`).Scan(&z)
	if err != nil {
		t.Fatalf("pick a zone on another day than Toronto: %v", err)
	}
	return z
}

// seedInvoiceDueOn plants one open invoice due on the given calendar day.
func seedInvoiceDueOn(t *testing.T, pool *pgxpool.Pool, tag, customerID, due string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), fmt.Sprintf(`
		%s
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, seq)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date,
		        9000, 0, 9000, 9000, now(), (SELECT next_value FROM iseq))`, invoiceWriterSeqCTE),
		"#INV-ZAGE-"+tag, customerID, trentonBranch, due)
	if err != nil {
		t.Fatalf("seed the invoice due %s: %v", due, err)
	}
}

type agedRow struct {
	age    int
	bucket string
}

// viewAges reads the view's invoice rows for one customer keyed by invoice number.
func viewAges(t *testing.T, pool *pgxpool.Pool, customerID string) map[string]agedRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT i.invoice_no::text, a.age_days, a.bucket
		  FROM v_ar_aging a JOIN invoices i ON i.id = a.source_ref_id
		 WHERE a.customer_id = $1::uuid AND a.source_type = 'invoice'`, customerID)
	if err != nil {
		t.Fatalf("read the aging view: %v", err)
	}
	defer rows.Close()
	out := map[string]agedRow{}
	for rows.Next() {
		var no string
		var r agedRow
		if err := rows.Scan(&no, &r.age, &r.bucket); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[no] = r
	}
	return out
}

func TestIntegrationARAgingZone_InvoiceArmAgesOnTheDealersDay(t *testing.T) {
	pool := intPool(t)
	zone := otherDayZone(t, pool)
	setDealerZone(t, pool, `"`+zone+`"`)
	cust := seedCustomer(t, pool, ns(t))
	var today, yesterday string
	if err := pool.QueryRow(context.Background(), `
		SELECT ((now() AT TIME ZONE $1)::date)::text, ((now() AT TIME ZONE $1)::date - 1)::text`, zone).
		Scan(&today, &yesterday); err != nil {
		t.Fatalf("the dealer's days: %v", err)
	}
	seedInvoiceDueOn(t, pool, "TODAY", cust, today)
	seedInvoiceDueOn(t, pool, "YDAY", cust, yesterday)
	got := viewAges(t, pool, cust)
	if g := got["#INV-ZAGE-TODAY"]; g.age != 0 || g.bucket != "CURRENT" {
		t.Errorf("zone %s: an invoice due on the dealer's today must read current; got %d days, %s", zone, g.age, g.bucket)
	}
	if g := got["#INV-ZAGE-YDAY"]; g.age != 1 || g.bucket != "1_30" {
		t.Errorf("zone %s: an invoice due on the dealer's yesterday must read 1 day in 1_30; got %d days, %s", zone, g.age, g.bucket)
	}
}

// The view, billing's aging sync projection and the function all name one day at one instant.
func TestIntegrationARAgingZone_ViewAndBillingProjectionAgree(t *testing.T) {
	pool := intPool(t)
	zone := otherDayZone(t, pool)
	setDealerZone(t, pool, `"`+zone+`"`)
	cust := seedCustomer(t, pool, ns(t))
	var yesterday string
	if err := pool.QueryRow(context.Background(),
		`SELECT ((now() AT TIME ZONE $1)::date - 1)::text`, zone).Scan(&yesterday); err != nil {
		t.Fatalf("the dealer's yesterday: %v", err)
	}
	seedInvoiceDueOn(t, pool, "AGREE", cust, yesterday)
	z, err := houseday.NewZone(zone)
	if err != nil {
		t.Fatalf("zone %s: %v", zone, err)
	}
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM invoices WHERE customer_id = $1::uuid`, cust).Scan(&id); err != nil {
		t.Fatalf("invoice id: %v", err)
	}
	store := NewAgingStore(pool, nil).WithZone(zoneAgingSource{z})
	row, found, err := loadAging(store, cust, id)
	if err != nil || !found {
		t.Fatalf("aging load (found %v, err %v)", found, err)
	}
	days, _ := row.Data["days_past_due"].(float64)
	if view := viewAges(t, pool, cust)["#INV-ZAGE-AGREE"]; int(days) != view.age || view.age != 1 {
		t.Fatalf("billing projection says %v days, the view says %d, both want 1 on the dealer's day (%s)", days, view.age, zone)
	}
}

func TestIntegrationARAgingZone_TransactionArmFilesAnEveningUnderThePacificDay(t *testing.T) {
	pool := intPool(t)
	setDealerZone(t, pool, `"America/Los_Angeles"`)
	cust := seedCustomer(t, pool, ns(t))
	// 23:00 Pacific, three Pacific days back: already the next calendar day in Toronto.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after,
		                                   source_type, source_ref_id, branch_id, occurred_at, created_at)
		VALUES ($1::uuid, 'ADJUSTMENT', 700, 700, 'adjustment', gen_random_uuid(), $2::uuid,
		        (((now() AT TIME ZONE 'America/Los_Angeles')::date - 3)::timestamp + interval '23 hours') AT TIME ZONE 'America/Los_Angeles',
		        (((now() AT TIME ZONE 'America/Los_Angeles')::date - 3)::timestamp + interval '23 hours') AT TIME ZONE 'America/Los_Angeles')`,
		cust, trentonBranch); err != nil {
		t.Fatalf("seed the evening adjustment: %v", err)
	}
	var age int
	if err := pool.QueryRow(context.Background(),
		`SELECT age_days FROM v_ar_aging WHERE customer_id = $1::uuid AND source_type = 'customer_txn'`, cust).Scan(&age); err != nil {
		t.Fatalf("read the aging view: %v", err)
	}
	if age != 3 {
		t.Fatalf("a 23:00 Pacific adjustment three Pacific days back must age 3 days; got %d", age)
	}
}

// The function answers the Ontario default for anything that is not a usable zone name, the same
// fallbacks settings' parseDealerConfig applies.
func TestIntegrationARAgingZone_FunctionFallsBackToTorontoLikeTheSettingsReader(t *testing.T) {
	pool := intPool(t)
	cases := []struct{ name, raw, want string }{
		{"absent", "", "America/Toronto"},
		{"blank", `""`, "America/Toronto"},
		{"spaces", `"   "`, "America/Toronto"},
		{"local", `"Local"`, "America/Toronto"},
		{"unknown", `"Mars/Olympus_Mons"`, "America/Toronto"},
		{"number", `123`, "America/Toronto"},
		{"object", `{"zone":"America/Los_Angeles"}`, "America/Toronto"},
		{"null", `null`, "America/Toronto"},
		{"toronto", `"America/Toronto"`, "America/Toronto"},
		{"pacific", `"America/Los_Angeles"`, "America/Los_Angeles"},
		{"padded", `" America/Los_Angeles "`, "America/Los_Angeles"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setDealerZone(t, pool, c.raw)
			var got string
			if err := pool.QueryRow(context.Background(), `SELECT settings_business_zone()`).Scan(&got); err != nil {
				t.Fatalf("settings_business_zone(): %v", err)
			}
			if got != c.want {
				t.Fatalf("stored %q: got %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// An Ontario dealer is unchanged exactly: with the key absent, and with Toronto named, every column
// of every row the view returns equals the 0376 expression evaluated inline.
func TestIntegrationARAgingZone_OntarioDealerIsUnchangedExactly(t *testing.T) {
	pool := intPool(t)
	cust := seedCustomer(t, pool, ns(t))
	for _, d := range []int{0, 1, 30, 31, 60, 61, 90, 91} {
		seedInvoiceDueOn(t, pool, fmt.Sprintf("ON%d", d), cust, daysAgoToronto(t, pool, d))
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after,
		                                   source_type, source_ref_id, branch_id, occurred_at, created_at)
		VALUES ($1::uuid, 'NSF', 2500, 2500, 'payment', gen_random_uuid(), $2::uuid, now() - interval '40 days', now() - interval '40 days')`,
		cust, trentonBranch); err != nil {
		t.Fatalf("seed the NSF fee: %v", err)
	}
	const old0376 = `
		SELECT source_type, amount_cents, age_date::text,
		       GREATEST((now() AT TIME ZONE 'America/Toronto')::date - age_date, 0),
		       CASE WHEN (now() AT TIME ZONE 'America/Toronto')::date - age_date <= 0  THEN 'CURRENT'
		            WHEN (now() AT TIME ZONE 'America/Toronto')::date - age_date <= 30 THEN '1_30'
		            WHEN (now() AT TIME ZONE 'America/Toronto')::date - age_date <= 60 THEN '31_60'
		            WHEN (now() AT TIME ZONE 'America/Toronto')::date - age_date <= 90 THEN '61_90'
		            ELSE '90_PLUS' END
		  FROM (
		    SELECT 'invoice'::text AS source_type, i.balance_cents AS amount_cents, i.due_date AS age_date
		      FROM invoices i WHERE i.customer_id = $1::uuid AND i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.balance_cents <> 0
		    UNION ALL
		    SELECT 'customer_txn', ct.amount_cents, (ct.created_at AT TIME ZONE 'America/Toronto')::date
		      FROM customer_transactions ct
		     WHERE ct.customer_id = $1::uuid AND ct.txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND ct.source_type <> 'invoice'
		  ) s ORDER BY 1, 3, 2`
	const viaView = `
		SELECT source_type, amount_cents, age_date::text, age_days, bucket
		  FROM v_ar_aging WHERE customer_id = $1::uuid ORDER BY 1, 3, 2`
	for _, raw := range []string{"", `"America/Toronto"`} {
		setDealerZone(t, pool, raw)
		want := collectRows(t, pool, old0376, cust)
		got := collectRows(t, pool, viaView, cust)
		if len(want) != 9 || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("stored %q: the view must equal the 0376 expression row for row (9 rows)\n got  %v\n want %v", raw, got, want)
		}
	}
}

// The grand total (the AR tie out) does not depend on the zone; only ages and buckets do.
func TestIntegrationARAgingZone_GrandTotalIsTheSameInEveryZone(t *testing.T) {
	pool := intPool(t)
	cust := seedCustomer(t, pool, ns(t))
	seedInvoiceDueOn(t, pool, "TIE1", cust, daysAgoToronto(t, pool, 10))
	seedInvoiceDueOn(t, pool, "TIE2", cust, daysAgoToronto(t, pool, 70))
	var totals []int64
	for _, raw := range []string{"", `"America/Los_Angeles"`, `"Pacific/Kiritimati"`} {
		setDealerZone(t, pool, raw)
		var sum int64
		if err := pool.QueryRow(context.Background(),
			`SELECT COALESCE(SUM(amount_cents),0) FROM v_ar_aging WHERE customer_id = $1::uuid`, cust).Scan(&sum); err != nil {
			t.Fatalf("sum the view: %v", err)
		}
		totals = append(totals, sum)
	}
	if totals[0] != 18000 || totals[1] != 18000 || totals[2] != 18000 {
		t.Fatalf("the grand total must be 18000 in every zone; got %v", totals)
	}
}

func daysAgoToronto(t *testing.T, pool *pgxpool.Pool, n int) string {
	t.Helper()
	var d string
	if err := pool.QueryRow(context.Background(),
		`SELECT ((now() AT TIME ZONE 'America/Toronto')::date - $1::int)::text`, n).Scan(&d); err != nil {
		t.Fatalf("a day %d back: %v", n, err)
	}
	return d
}

func collectRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var st, ad, bucket string
		var amt int64
		var age int
		if err := rows.Scan(&st, &amt, &ad, &age, &bucket); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, fmt.Sprintf("%s|%d|%s|%d|%s", st, amt, ad, age, bucket))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}
