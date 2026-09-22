//go:build integration

// freight_baseline_integration_test.go, item 2 part 1 (session B), against a LIVE Postgres.
//
// WHAT THIS PINS, AND WHY THE UNIT HALF IS NOT ENOUGH. `convert_seam_test.go` pins the SQL the splice
// builds, byte for byte. This file pins what the DATABASE ends up holding, which is what dispatch
// actually reads, and dispatch reads BOTH columns through a seam that treats them differently:
// `freight_cents` is the figure a split divides, `quoted_freight_cents` is the baseline a raise is
// measured against, and the whole gate turns on `NULL` meaning something different from `0`.
//
// THE STATE OF PLAY BEFORE THIS. `orders.freight_cents` (0184:252) had ZERO production write sites -
// a repo-wide census found one reader and three fixture .sql scripts, nothing else, so
// `dispatchOrderValueAdapter` mapped its `0` to "unknown" and `SplitFreight` refused every real order
// in words before doing any arithmetic. A baseline compared against a column nothing populates is a
// gate that can never fire, so the conversion writes both or the feature is theatre.
//
// Run: DATABASE_URL=… TZ=UTC go test -tags=integration ./internal/quote/... -run Freight -count=1
package quote

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// freightOf reads the pair as the database holds it. `quoted` is a POINTER because NULL is a real,
// load-bearing answer here and a scan into int64 would silently turn it into 0, which is the exact
// conflation this column exists to prevent.
func freightOf(t *testing.T, pool *pgxpool.Pool, orderID string) (freight int64, quoted *int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT freight_cents, quoted_freight_cents FROM orders WHERE id = $1`, orderID).
		Scan(&freight, &quoted); err != nil {
		t.Fatalf("read freight pair: %v", err)
	}
	return freight, quoted
}

func setQuoteFee(t *testing.T, pool *pgxpool.Pool, quoteID string, cents int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE quotes SET delivery_fee_cents = $2 WHERE id = $1`, quoteID, cents); err != nil {
		t.Fatalf("set delivery fee: %v", err)
	}
}

// A quoted delivery fee becomes BOTH figures: the opening actual and the frozen baseline. They are
// equal at conversion and diverge only when somebody adds a truck.
func TestIntegration_Freight_AQuotedFeeBecomesBothTheActualAndTheBaseline(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)
	setQuoteFee(t, pool, quoteID, 45_000)

	res, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	freight, quoted := freightOf(t, pool, res.OrderID)
	if freight != 45_000 {
		t.Fatalf("freight_cents = %d, want 45000, the conversion did not carry the quoted fee onto "+
			"the order, so SplitFreight will still refuse this order in words", freight)
	}
	if quoted == nil {
		t.Fatal("quoted_freight_cents is NULL on an order born from a quote, the gate reads NULL as " +
			"'no baseline, never gate', so this order could have its freight raised without anybody being asked")
	}
	if *quoted != 45_000 {
		t.Fatalf("quoted_freight_cents = %d, want 45000", *quoted)
	}
}

// A PICKUP quote is forced to a zero fee at builder.go:386-389, so "quoted zero" is the ordinary case
// rather than an edge, and it is a BASELINE, not an absence. A charge above nothing is still a
// charge above what the customer agreed to, which is precisely the client's sentence.
func TestIntegration_Freight_QuotedZeroIsABaselineAndNotAnAbsence(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 5_000_00)
	setQuoteFee(t, pool, quoteID, 0)

	res, err := gatedRepo(pool).Convert(context.Background(), cust, quoteID, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	freight, quoted := freightOf(t, pool, res.OrderID)
	if freight != 0 {
		t.Fatalf("freight_cents = %d, want 0", freight)
	}
	if quoted == nil {
		t.Fatal("a quote that promised free delivery produced a NULL baseline. NULL means 'this order " +
			"never came from a quote and must never be gated', collapsing 'quoted zero' into it hands " +
			"away the gate for every pickup quote in the product")
	}
	if *quoted != 0 {
		t.Fatalf("quoted_freight_cents = %d, want 0", *quoted)
	}
}

// The other side of the same distinction, and the brief's own acceptance clause: "an order with no
// quote baseline is never gated". An order that was not born from a quote must come out NULL.
//
// The conversion cannot produce one, by construction, every order it makes has a quote, so this
// writes the row the OTHER writers produce (the POS ring, a duplicate, a reorder: none of them names
// quote_id) and asserts the column's own default does the right thing without anybody remembering to.
func TestIntegration_Freight_AnOrderWithNoQuoteHasNoBaseline(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	_ = quoteID

	var orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2::uuid, $3::uuid, 1000, 130, 1130) RETURNING id::text`,
		"#SO-FRT-NOQUOTE-"+cust[:8], cust, intTrenton).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, orderID) })

	freight, quoted := freightOf(t, pool, orderID)
	if freight != 0 {
		t.Fatalf("freight_cents = %d, want 0 from 0184's NOT NULL DEFAULT", freight)
	}
	if quoted != nil {
		t.Fatalf("quoted_freight_cents = %d on an order with no quote, it must be NULL, which is how "+
			"the gate knows there is no promise to measure against", *quoted)
	}
}

// THE COLUMN REFUSES A NEGATIVE BASELINE, and the CHECK has to admit NULL at the same time. A CHECK
// written as `quoted_freight_cents >= 0` alone would be UNKNOWN for NULL, which Postgres accepts, so
// the naive form happens to work, but the explicit `IS NULL OR` states the intent, and this pins
// both arms so a later tidy of the constraint cannot quietly forbid the NULL the gate depends on.
func TestIntegration_Freight_TheBaselineAdmitsNullAndRefusesNegative(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust, _, _, _ := seedQuote(t, pool, "DRAFT", nil)

	var orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, subtotal_cents, tax_cents, total_cents,
		                    quoted_freight_cents)
		VALUES ($1, $2::uuid, $3::uuid, 1000, 130, 1130, NULL) RETURNING id::text`,
		"#SO-FRT-NULL-"+cust[:8], cust, intTrenton).Scan(&orderID); err != nil {
		t.Fatalf("an explicit NULL baseline was refused, and NULL is the 'never gate this' answer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, orderID) })

	_, err := pool.Exec(ctx, `UPDATE orders SET quoted_freight_cents = -1 WHERE id = $1`, orderID)
	if err == nil {
		t.Fatal("a negative quoted freight was accepted, the CHECK is not holding, and a negative " +
			"baseline makes every raise look like a reduction")
	}
}
