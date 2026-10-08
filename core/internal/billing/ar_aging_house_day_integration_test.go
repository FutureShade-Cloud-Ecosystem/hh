//go:build integration

package billing

// ar_aging_house_day_integration_test.go, the AR aging view and the aging sync projection on the
// dealer's day.
//
// The view (0034, re-stated by 0064) and the projection both computed every age from the database
// session's CURRENT_DATE, which is the UTC day on every deployment this house runs: between the UTC
// day change and the house zone's own, an open item aged one day more than the dealer's wall clock
// says, enough to cross a 30/60/90 bucket edge an evening early.
//
// A view cannot see a Go clock and no test can pin the database's now(), so the legs here are
// three, each honest about what it can prove:
//   - the CATALOG leg reads the view definition the migration installed and fails unless it
//     derives its day from the house zone expression. Red on the old view at any hour.
//   - the EXPRESSION leg proves the SQL expression itself at chosen instants, both sides of the
//     UTC day change and both sides of a daylight saving change, so the zone string in the view
//     can never quietly stop meaning the dealer's zone.
//   - the ARITHMETIC legs seed rows relative to the DATABASE'S OWN current house day and assert
//     the ages through the view and through the projection, so they hold at whatever hour the
//     suite runs. On the old code they are red only in the hours where the UTC day has rolled
//     over and the house zone's has not, which is the defect's own window.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func TestIntegrationARAgingView_DerivesItsDayFromTheHouseZone(t *testing.T) {
	pool := intPool(t)
	var def string
	if err := pool.QueryRow(context.Background(),
		`SELECT pg_get_viewdef('v_ar_aging'::regclass, true)`).Scan(&def); err != nil {
		t.Fatalf("read the installed view: %v", err)
	}
	if !strings.Contains(strings.ToLower(def), "at time zone 'america/toronto'") {
		t.Fatalf("the AR aging view must derive its day from the house zone expression %s, the one "+
			"choice the 0376 migration names; installed definition: %s", houseDaySQL, def)
	}
	if strings.Contains(strings.ToLower(def), "current_date") {
		t.Fatalf("the AR aging view must not read the session's CURRENT_DATE, it is the UTC day on "+
			"every deployment and ages every open item one day early through the Ontario evening; "+
			"installed definition: %s", def)
	}
}

func TestIntegrationHouseDaySQLExpression_AtTheChosenInstants(t *testing.T) {
	pool := intPool(t)
	cases := []struct{ at, want string }{
		// Just after the UTC day change: the house zone is still on the previous evening.
		{"2026-03-09 00:30:00+00", "2026-03-08"},
		// The same UTC day at midday: both zones agree, the expression must too.
		{"2026-03-09 12:00:00+00", "2026-03-09"},
		// The evening after the fall-back change: the house zone is a (standard time) evening behind.
		{"2026-11-02 00:30:00+00", "2026-11-01"},
	}
	for _, c := range cases {
		var got string
		if err := pool.QueryRow(context.Background(),
			`SELECT (($1::timestamptz AT TIME ZONE 'America/Toronto')::date)::text`, c.at).
			Scan(&got); err != nil {
			t.Fatalf("evaluate the house day expression at %s: %v", c.at, err)
		}
		if got != c.want {
			t.Errorf("the house day at %s: got %s, want %s", c.at, got, c.want)
		}
	}
}

// seedAgingInvoice plants one FINALIZED invoice due houseDueDaysAgo from the database's own current
// house day, on the house day itself so the seed and the reader can never name different todays.
func seedAgingInvoice(t *testing.T, pool *pgxpool.Pool, tag, customerID string, houseDueDaysAgo int) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), fmt.Sprintf(`
		%s
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, seq)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', %s - $4::int, %s - $4::int,
		        9000, 0, 9000, 9000, now(), (SELECT next_value FROM iseq))
		RETURNING id::text`, invoiceWriterSeqCTE, houseDaySQL, houseDaySQL),
		"#INV-HDAY-"+tag, customerID, trentonBranch, houseDueDaysAgo).Scan(&id)
	if err != nil {
		t.Fatalf("seed the aging invoice: %v", err)
	}
	return id
}

func TestIntegrationARAgingView_AgesWholeDaysOnTheHousesDay(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	seedAgingInvoice(t, pool, ns(t), cust, 45)
	var age int
	var bucket string
	if err := pool.QueryRow(ctx,
		`SELECT age_days, bucket FROM v_ar_aging WHERE customer_id = $1::uuid AND source_type = 'invoice'`,
		cust).Scan(&age, &bucket); err != nil {
		t.Fatalf("read the aging view: %v", err)
	}
	if age != 45 || bucket != "31_60" {
		t.Fatalf("the view ages the invoice on the house day: got %d days in bucket %s, want 45 in 31_60", age, bucket)
	}
}

func TestIntegrationAgingSync_DaysPastDueOnTheHousesDay(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, ns(t))
	id := seedAgingInvoice(t, pool, ns(t), cust, 45)
	store := NewAgingStore(pool, nil)
	row, found, err := store.Load(ctx, sync.ScopeFilter{Tenant: true, TenantID: cust}, id)
	if err != nil || !found {
		t.Fatalf("aging load (found %v, err %v)", found, err)
	}
	days, ok := row.Data["days_past_due"].(float64)
	if !ok {
		t.Fatalf("the projection carries days_past_due: %v", row.Data)
	}
	if int(days) != 45 {
		t.Fatalf("the projection ages the invoice on the house day: got %v days, want 45", days)
	}
}
