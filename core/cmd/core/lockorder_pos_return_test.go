//go:build integration

package main

// lockorder_pos_return_test.go, the money lock order legs that need the REAL POS return (the
// reviewer's P1-1 pair): the return's INSERT INTO pos_returns takes a FOR KEY SHARE lock on the
// returned sale's invoice through the invoice_id foreign key, BEFORE its refund rail draws the
// payments counter row; the settle takes the payments counter first and then the invoice. A settle
// that asks for the invoice FOR UPDATE conflicts with that key share, so the pair closes a cycle
// (the return holds the key share and wants the counter, the settle holds the counter and wants the
// invoice) and Postgres aborts one side. On the card paths that is the worst failure in the book:
// the card refund (or the card charge, on the settle side) has already left before the abort.
//
// The legs drive the return through the REGISTERED route POST /api/v1/pos/returns over the real
// fold (the card_rail_wiring_test rig: wireMoney, wireOperations, wireTops, the kit's staff
// guards), against the real payments settle, the way a clerk's screen does. The settle is the
// repository's own Pay, the same write the portal and card settle ride. Each leg runs at pool 4
// and pool 16, parked in a widened window and released by reading pg_stat_activity, never by a
// bare sleep race.
//
// Run: HH_CORE_TEST_DATABASE_URL=<migrated scratch database> go test -tags integration ./cmd/core/ -run LockOrder_POS -count=1
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// loposPost drives one authenticated request through the wired mux and is safe to call from the
// leg's goroutines: nothing in it touches t after the request is built, so a parked or aborted
// side reports through its channel instead of failing the leg from the wrong goroutine.
func loposPost(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

// loposPool opens the composition gate at a chosen size (the leg under test and the settle both
// borrow from it), plus the observer's own two-connection pool so watching pg_stat_activity never
// takes a connection from the pool under test.
func loposPools(t *testing.T, max int32) (leg, obs *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv(coreDBEnvVar)
	if dsn == "" {
		t.Skip(coreDBEnvVar + " is not set: the lock order POS legs need the scratch database with the frozen migrations applied")
	}
	open := func(n int32) *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatalf("parse gate url: %v", err)
		}
		cfg.MaxConns = n
		p, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatalf("open pool: %v", err)
		}
		t.Cleanup(p.Close)
		return p
	}
	leg, obs = open(max), open(2)
	if err := testkit.VerifyBaseline(context.Background(), testkit.PgxPool(leg)); err != nil {
		t.Fatalf("lock order POS baseline: %v", err)
	}
	return leg, obs
}

// loposCustomer plants one account customer for the leg (the settle pays the sale's invoice, so
// the sale must belong to a real account, not the house WALK-IN).
func loposCustomer(t *testing.T, pool *pgxpool.Pool, branchID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('LOP-'||$1, 'Lock Order POS '||$1, 'CONTRACTOR', $2::uuid, 10000000) RETURNING id::text`,
		fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000), branchID).Scan(&id); err != nil {
		t.Fatalf("plant customer: %v", err)
	}
	return id
}

// loposRingSale rings one paid sale through the REGISTERED sale route for the planted customer,
// cash or card, and returns its order id. The card tender rides the simulator (no processor is
// dialed; the refund rail's discipline, not its dialing, is what the legs exercise).
func loposRingSale(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards, w wiringCounter, customerID, tender string) string {
	t.Helper()
	body := map[string]any{
		"idempotency_key": uuidKey(t),
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_NOW",
		"customer_id":     customerID,
		"tender":          tender,
		"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 2.0, "uom_entered": w.uom, "cost_cents": 0}},
	}
	if tender == "CARD" {
		body["payment_token"] = "tok_lockorder_pos"
		body["card_type"] = "CREDIT"
	}
	rec := loposPost(t, mux, sg, "/api/v1/pos/sales", body)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("ring %s sale: %d %s", tender, rec.Code, rec.Body.String())
	}
	var sale struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sale); err != nil {
		t.Fatalf("decode sale: %v", err)
	}
	return sale.OrderID
}

// loposSaleInvoice reads the sale's invoice and re-opens it (the ring paid it; the settle's balance
// ceiling must see room again). The locks the settle takes are the same either way. A cash or card
// refund of an open invoice is refused (the interim rule of the Opus review's P2-2), so the legs put
// the status back to PAID for the moment the return is made (loposStatus) and re-open it once the
// return is parked in its transaction.
func loposSaleInvoice(t *testing.T, pool *pgxpool.Pool, orderID string) (invoiceID string) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM invoices WHERE order_id = $1::uuid`, orderID).Scan(&invoiceID); err != nil {
		t.Fatalf("read the sale's invoice: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET status='FINALIZED', balance_cents=total_cents WHERE id=$1::uuid`, invoiceID); err != nil {
		t.Fatalf("re-open the sale's invoice: %v", err)
	}
	return invoiceID
}

// loposStatus sets the invoice's status only: the balance and the allocations stay as the leg made them.
func loposStatus(t *testing.T, pool *pgxpool.Pool, invoiceID, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE invoices SET status=$2 WHERE id=$1::uuid`, invoiceID, status); err != nil {
		t.Fatalf("set the invoice %s: %v", status, err)
	}
}

// loposArmReturnWindow parks the NEXT POS return inside its pos_returns INSERT, once, AFTER the
// row's foreign key check has taken the invoice key share: an AFTER ROW trigger runs behind the
// migration-created FK triggers, so the return sleeps holding the share and nothing its refund
// rail takes later.
func loposArmReturnWindow(t *testing.T, obs *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS lopos_flag (id int PRIMARY KEY)`,
		`CREATE OR REPLACE FUNCTION lopos_park_return() RETURNS trigger LANGUAGE plpgsql AS $f$
		 BEGIN DELETE FROM lopos_flag WHERE id = 1; IF FOUND THEN PERFORM pg_sleep(2); END IF; RETURN NEW; END $f$`,
		`DROP TRIGGER IF EXISTS zz_lopos_park_return ON pos_returns`,
		`CREATE TRIGGER zz_lopos_park_return AFTER INSERT ON pos_returns FOR EACH ROW EXECUTE FUNCTION lopos_park_return()`,
		`INSERT INTO lopos_flag VALUES (1) ON CONFLICT DO NOTHING`,
	} {
		if _, err := obs.Exec(ctx, q); err != nil {
			t.Fatalf("arm return window: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = obs.Exec(c, `DROP TRIGGER IF EXISTS zz_lopos_park_return ON pos_returns`)
		_, _ = obs.Exec(c, `DROP TABLE IF EXISTS lopos_flag`)
	})
}

// loposArmCounterWindow parks the NEXT writer to UPDATE the payments counter row inside its
// statement, once (the settle's non advancing lock): the writer sleeps holding the counter row and
// nothing its statement takes later, which is how the card settle leg makes the settle the side
// that is parked rather than the side that queues.
func loposArmCounterWindow(t *testing.T, obs *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS lopos_flag (id int PRIMARY KEY)`,
		`CREATE OR REPLACE FUNCTION lopos_park_pseq() RETURNS trigger LANGUAGE plpgsql AS $f$
		 BEGIN IF NEW.scope = 'payments' THEN DELETE FROM lopos_flag WHERE id = 2; IF FOUND THEN PERFORM pg_sleep(4); END IF; END IF; RETURN NEW; END $f$`,
		`DROP TRIGGER IF EXISTS zz_lopos_park_pseq ON scope_sequences`,
		`CREATE TRIGGER zz_lopos_park_pseq AFTER UPDATE ON scope_sequences FOR EACH ROW EXECUTE FUNCTION lopos_park_pseq()`,
	} {
		if _, err := obs.Exec(ctx, q); err != nil {
			t.Fatalf("arm counter window: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = obs.Exec(context.Background(), `DROP TRIGGER IF EXISTS zz_lopos_park_pseq ON scope_sequences`)
	})
}

// loposWait blocks until `want` sessions of the gate database match cond, or fails the leg. The
// observer pool reads pg_stat_activity; nothing sleeps on a guess.
func loposWait(t *testing.T, obs *pgxpool.Pool, what string, want int, cond string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var n int
		if err := obs.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND pid <> pg_backend_pid() AND (`+cond+`)`).Scan(&n); err != nil {
			t.Fatalf("observer: %v", err)
		}
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("barrier never reached: %s (want %d sessions where %s)", what, want, cond)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func loposParkedInReturn() string {
	return `wait_event = 'PgSleep' AND query LIKE '%pos_returns%'`
}

func loposParkedInCounter() string {
	return `wait_event = 'PgSleep' AND query LIKE '%scope_sequences%'`
}

// loposAnswer waits for one side's result, bounded.
func loposAnswer(t *testing.T, what string, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(60 * time.Second):
		t.Fatalf("%s never answered", what)
		return nil
	}
}

// TestIntegration_LockOrder_POSReturnBesideSettle: the POS return (cash, then card) and the card
// settle beside it, on the returned sale's invoice. Both sides must answer: the return with a 201
// and its pos_returns row committed, the settle with no error. The window parks the return holding
// the invoice key share; the settle variant arms the payments counter window instead, so the
// settle is the parked side and the return's refund rail is the one that queues.
func TestIntegration_LockOrder_POSReturnBesideSettle(t *testing.T) {
	for _, variant := range []string{"cash return", "card return", "card settle"} {
		t.Run(variant, func(t *testing.T) {
			for _, size := range []int32{4, 16} {
				t.Run(fmt.Sprintf("pool %d", size), func(t *testing.T) {
					pool, obs := loposPools(t, size)
					key := wiringRailDB(t, pool, map[string]string{"api_key": "lockorder-pos-not-a-secret"})
					w := plantWiringCounter(t, pool)
					cust := loposCustomer(t, pool, w.branchID)
					_, mux, sg := wireFold(t, pool, key, w.branchID)
					tender := "CASH"
					if variant != "cash return" {
						tender = "CARD"
					}
					orderID := loposRingSale(t, mux, sg, w, cust, tender)
					inv := loposSaleInvoice(t, pool, orderID)
					var lineID string
					var qty float64
					if err := pool.QueryRow(context.Background(),
						`SELECT id::text, qty_base FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`,
						orderID).Scan(&lineID, &qty); err != nil {
						t.Fatalf("read the sale's line: %v", err)
					}
					loposStatus(t, pool, inv, "PAID")
					loposArmReturnWindow(t, obs)
					parkSettle := variant == "card settle"
					if parkSettle {
						loposArmCounterWindow(t, obs)
					}
					retDone := make(chan int, 1)
					retBody := make(chan string, 1)
					retKey := uuidKey(t)
					go func() {
						body := map[string]any{
							"idempotency_key": retKey,
							"order_id":        orderID,
							"refund_method":   tender,
							"restock":         false,
							"lines":           []map[string]any{{"order_line_id": lineID, "qty_base": qty}},
						}
						if tender == "CASH" {
							body["till_session_id"] = w.sessionID
						}
						rec := loposPost(t, mux, sg, "/api/v1/pos/returns", body)
						retBody <- rec.Body.String()
						retDone <- rec.Code
					}()
					loposWait(t, obs, "return parked holding the invoice key share", 1, loposParkedInReturn())
					loposStatus(t, pool, inv, "FINALIZED")
					if parkSettle {
						if _, err := obs.Exec(context.Background(), `INSERT INTO lopos_flag VALUES (2) ON CONFLICT DO NOTHING`); err != nil {
							t.Fatalf("arm the settle's counter window: %v", err)
						}
					}
					setErr := make(chan error, 1)
					go func() {
						_, err := payments.NewRepository(pool).Pay(context.Background(), cust, "CARD",
							fmt.Sprintf("gw-lopos-%d", time.Now().UnixNano()),
							[]payments.Allocation{{InvoiceID: inv, AmountCents: 500}})
						setErr <- err
					}()
					if parkSettle {
						// The parked-settle variant reads pg_stat_activity: the settle sleeps inside
						// its payments counter statement, holding the row.
						loposWait(t, obs, "settle parked holding the payments counter row", 1, loposParkedInCounter())
					}
					// In the other two variants the settle is not armed with a window of its own, and
					// after the fix it does not wait on the return at all (a NO KEY UPDATE does not
					// conflict with the key share), so there is no queued session to observe: the
					// settle's own answer channel is the barrier, one observation with no timing guess
					// in it. The return is already parked and holds the share either way.
					var code int
					select {
					case code = <-retDone:
					case <-time.After(60 * time.Second):
						t.Fatal("return never answered")
					}
					serr := loposAnswer(t, "settle", setErr)
					var returns int
					_ = obs.QueryRow(context.Background(),
						`SELECT count(*) FROM pos_returns WHERE order_id = $1::uuid`, orderID).Scan(&returns)
					t.Logf("return: %d %s | settle: %v | pos_returns rows committed: %d",
						code, <-retBody, serr, returns)
					if code != http.StatusCreated || serr != nil {
						t.Fatalf("NOT BOTH ANSWERED: return %d, settle %v", code, serr)
					}
				})
			}
		})
	}
}

// loposInvoice plants one FINALIZED invoice for the customer: the multi-invoice reversal leg needs
// a second allocated invoice beside the sale's.
func loposInvoice(t *testing.T, pool *pgxpool.Pool, cust, branchID string, cents int64) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		 VALUES ('LOP-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30, $4, 0, $4, $4, now())
		 RETURNING id::text`, fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000), cust, branchID, cents).Scan(&id); err != nil {
		t.Fatalf("plant invoice: %v", err)
	}
	return id
}

// TestIntegration_LockOrder_MultiInvoiceReversalBesidePOSReturn: the multi-invoice reversal against
// a REAL POS cash return on the sale's invoice. The return holds the invoice key share (its
// pos_returns foreign key) and then draws the payments counter for its refund rail; the reversal
// draws the payments counter first (its reversing insert) and then takes every allocated invoice
// row. Row locks at FOR UPDATE conflict with the key share, so the pair closes a cycle: the
// reversal holds the counter and waits on the share, the return holds the share and waits on the
// counter, and one side is aborted. At FOR NO KEY UPDATE the reversal's row locks no longer
// conflict with the share: the reversal passes the parked return, commits, and the return's refund
// rail takes the counter after it.
func TestIntegration_LockOrder_MultiInvoiceReversalBesidePOSReturn(t *testing.T) {
	for _, size := range []int32{4, 16} {
		t.Run(fmt.Sprintf("pool %d", size), func(t *testing.T) {
			pool, obs := loposPools(t, size)
			key := wiringRailDB(t, pool, map[string]string{"api_key": "lockorder-pos-not-a-secret"})
			w := plantWiringCounter(t, pool)
			cust := loposCustomer(t, pool, w.branchID)
			_, mux, sg := wireFold(t, pool, key, w.branchID)
			orderID := loposRingSale(t, mux, sg, w, cust, "CASH")
			inv := loposSaleInvoice(t, pool, orderID)
			other := loposInvoice(t, pool, cust, w.branchID, 1_000_000)
			p, err := payments.NewRepository(pool).Pay(context.Background(), cust, "CARD",
				fmt.Sprintf("gw-lopos-%d", time.Now().UnixNano()),
				[]payments.Allocation{{InvoiceID: inv, AmountCents: 500}, {InvoiceID: other, AmountCents: 500}})
			if err != nil {
				t.Fatalf("seed multi-invoice payment: %v", err)
			}
			var lineID string
			var qty float64
			if err := pool.QueryRow(context.Background(),
				`SELECT id::text, qty_base FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1`,
				orderID).Scan(&lineID, &qty); err != nil {
				t.Fatalf("read the sale's line: %v", err)
			}
			loposStatus(t, pool, inv, "PAID")
			loposArmReturnWindow(t, obs)
			retDone := make(chan int, 1)
			retBody := make(chan string, 1)
			retKey := uuidKey(t)
			go func() {
				rec := loposPost(t, mux, sg, "/api/v1/pos/returns", map[string]any{
					"idempotency_key": retKey,
					"order_id":        orderID,
					"refund_method":   "CASH",
					"restock":         false,
					"till_session_id": w.sessionID,
					"lines":           []map[string]any{{"order_line_id": lineID, "qty_base": qty}},
				})
				retBody <- rec.Body.String()
				retDone <- rec.Code
			}()
			loposWait(t, obs, "return parked holding the invoice key share", 1, loposParkedInReturn())
			loposStatus(t, pool, inv, "FINALIZED")
			revErr := make(chan error, 1)
			go func() {
				_, err := payments.NewRepository(pool).ReversePayment(context.Background(), payments.ReverseInput{
					PaymentID: p.ID, Reason: "lock order leg", ActorID: w.userID, IsManager: true}, time.Now())
				revErr <- err
			}()
			// After the fix the reversal does not wait on the return's share at all, so there is no
			// queued session to observe in the green world: the reversal's answer channel is the
			// barrier. The return is already parked and holds the share either way.
			var code int
			select {
			case code = <-retDone:
			case <-time.After(60 * time.Second):
				t.Fatal("return never answered")
			}
			rerr := loposAnswer(t, "reversal", revErr)
			var returns int
			_ = obs.QueryRow(context.Background(),
				`SELECT count(*) FROM pos_returns WHERE order_id = $1::uuid`, orderID).Scan(&returns)
			t.Logf("return: %d %s | reversal: %v | pos_returns rows committed: %d",
				code, <-retBody, rerr, returns)
			if code != http.StatusCreated || rerr != nil {
				t.Fatalf("NOT BOTH ANSWERED: return %d, reversal %v", code, rerr)
			}
		})
	}
}
