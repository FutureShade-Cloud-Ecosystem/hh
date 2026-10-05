//go:build integration

// lockorder_integration_test.go, the one money lock order (see LOCK-ORDER.md and the block at the top
// of repository.go). Every leg here is a DEADLOCK leg: two writers that take the same rows meet in a
// window the test widens on purpose, and the leg passes only when BOTH answer. A 40P01 on either side
// is the failure, and for a card settle it is the worst one: the card is charged before the settle.
//
// Nothing here is a bare sleep race. A one-shot trigger parks the first writer in pg_sleep while it
// holds its rows, and the test reads pg_stat_activity to know the second writer is queued before it
// lets anything move. Each leg runs at pool 4 and pool 16.
//
//	Run: DATABASE_URL=<migrated and seeded database> go test -tags integration ./internal/payments/ -run LockOrder -count=1
package payments_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// - rig -

type loRig struct {
	t        *testing.T
	pool     *pgxpool.Pool // the pool under test, sized by the leg
	obs      *pgxpool.Pool // the observer's own pool, so watching never takes a connection from the pool under test
	repo     *payments.Repository
	branchID string
	userID   string
	custID   string
	suffix   string
}

func loPoolSizes(t *testing.T, leg func(t *testing.T, size int32)) {
	for _, size := range []int32{4, 16} {
		t.Run(fmt.Sprintf("pool %d", size), func(t *testing.T) { leg(t, size) })
	}
}

func newLORig(t *testing.T, size int32) *loRig {
	t.Helper()
	url := loDatabaseURL(t)
	ctx := context.Background()
	open := func(max int32) *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatalf("parse database url: %v", err)
		}
		cfg.MaxConns = max
		p, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("open pool: %v", err)
		}
		t.Cleanup(p.Close)
		return p
	}
	r := &loRig{t: t, pool: open(size), obs: open(2)}
	r.repo = payments.NewRepository(r.pool)
	r.suffix = fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	if err := r.obs.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&r.branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	if err := r.obs.QueryRow(ctx, `SELECT id::text FROM users WHERE status = 'ACTIVE' LIMIT 1`).Scan(&r.userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	if err := r.obs.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('LO-'||$1, 'Lock Order '||$1, 'CONTRACTOR', $2::uuid, 10000000) RETURNING id::text`,
		r.suffix, r.branchID).Scan(&r.custID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// Both counter rows exist before any leg runs, so no leg races to create one.
	if _, err := r.obs.Exec(ctx, `INSERT INTO scope_sequences (scope, next_value) VALUES ('payments', 0), ('invoices', 0)
	                              ON CONFLICT (scope) DO NOTHING`); err != nil {
		t.Fatalf("seed sequence rows: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = r.obs.Exec(c, `DROP TRIGGER IF EXISTS lo_slow_seq ON scope_sequences`)
		_, _ = r.obs.Exec(c, `DROP TRIGGER IF EXISTS lo_slow_seq_before ON scope_sequences`)
		_, _ = r.obs.Exec(c, `DROP TRIGGER IF EXISTS lo_slow_alloc ON payment_allocations`)
		_, _ = r.obs.Exec(c, `DROP TRIGGER IF EXISTS lo_slow_sub ON customer_transactions`)
		_, _ = r.obs.Exec(c, `DROP TABLE IF EXISTS lo_flag`)
		_, _ = r.obs.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM payment_nsf WHERE customer_id = $1::uuid`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM payment_allocations WHERE payment_id IN
			(SELECT id FROM payments WHERE customer_id = $1::uuid)`, r.custID)
		_, _ = r.obs.Exec(c, `UPDATE payments SET reversed_by_payment_id = NULL WHERE customer_id = $1::uuid`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM orders WHERE customer_id = $1::uuid`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, r.custID)
	})
	return r
}

func loDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	return url
}

func (r *loRig) invoice(tag string, cents int64) string {
	r.t.Helper()
	var id string
	if err := r.obs.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		 VALUES ('LO-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30, $4, 0, $4, $4, now())
		 RETURNING id::text`, tag+"-"+r.suffix, r.custID, r.branchID, cents).Scan(&id); err != nil {
		r.t.Fatalf("seed invoice %s: %v", tag, err)
	}
	return id
}

func (r *loRig) order() string {
	r.t.Helper()
	var id string
	if err := r.obs.QueryRow(context.Background(),
		`INSERT INTO orders (order_no, customer_id, branch_id) VALUES ('LO-'||$1, $2::uuid, $3::uuid) RETURNING id::text`,
		r.suffix, r.custID, r.branchID).Scan(&id); err != nil {
		r.t.Fatalf("seed order: %v", err)
	}
	return id
}

// payOnce settles `cents` on one invoice through the payments settle and returns the payment.
func (r *loRig) payOnce(invoiceID string, cents int64) payments.Payment {
	r.t.Helper()
	p, err := r.repo.Pay(context.Background(), r.custID, "CARD", fmt.Sprintf("gw-lo-%d", time.Now().UnixNano()),
		[]payments.Allocation{{InvoiceID: invoiceID, AmountCents: cents}})
	if err != nil {
		r.t.Fatalf("seed payment: %v", err)
	}
	return p
}

func (r *loRig) register() *billing.RegisterService {
	return billing.NewRegisterService(billing.NewRepository(r.pool)).
		WithMoney(billing.NewPaymentsReceiptWriter(r.repo))
}

// settleKind names the settle a leg runs beside the writer under test: the register's RecordPayment
// (which reaches the counter's RecordReceipt) or the payments settle Pay (PayWithStamps and
// PayWithIntent are its two thin wrappers: one body, one lock).
func (r *loRig) settle(kind, invoiceID string, cents int64) error {
	ctx := context.Background()
	switch kind {
	case "register":
		_, err := r.register().RecordPayment(ctx, billing.ReceiptInput{
			InvoiceID: invoiceID, Method: "CHEQUE", AmountCents: cents, Reference: "chq lo",
			IdempotencyKey: fmt.Sprintf("lo-%d", time.Now().UnixNano()),
		})
		return err
	case "paysettle":
		_, err := r.repo.Pay(ctx, r.custID, "CARD", fmt.Sprintf("gw-lo-%d", time.Now().UnixNano()),
			[]payments.Allocation{{InvoiceID: invoiceID, AmountCents: cents}})
		return err
	}
	return fmt.Errorf("unknown settle kind %q", kind)
}

// - barriers: the observer reads pg_stat_activity, it never sleeps on a guess -

// waitSessions blocks until `want` sessions of this database match cond, or fails the leg.
func (r *loRig) waitSessions(what string, want int, cond string) {
	r.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var n int
		if err := r.obs.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND pid <> pg_backend_pid() AND (`+cond+`)`).Scan(&n); err != nil {
			r.t.Fatalf("observer: %v", err)
		}
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("barrier never reached: %s (want %d sessions where %s)", what, want, cond)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

const (
	condSleepingInSeq   = `wait_event = 'PgSleep' AND query LIKE '%scope_sequences%'`
	condSleepingInAlloc = `wait_event = 'PgSleep' AND query LIKE '%payment_allocations%'`
	condSleepingInSub   = `wait_event = 'PgSleep' AND query LIKE '%customer_transactions%'`
	condLockWaiter      = `wait_event_type = 'Lock'`
)

// armSeqWindow parks the NEXT writer to touch the named counter row in pg_sleep, once, AFTER the
// row has been written. The trigger consumes a one-shot flag, so every later touch runs at full
// speed. This is the widened window: the first writer holds its counter row long enough for the
// test to see the second writer queue behind it. The park sits mid-statement for a CTE writer (the
// trigger fires once the counter row is updated, before the statement's remaining parts run), but
// every row the writer locked in an EARLIER statement is already held at the park, so a leg that
// needs the counter held WITHOUT those rows parks with armSeqBeforeWindow instead.
func (r *loRig) armSeqWindow(scope string) {
	r.t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS lo_flag (id int PRIMARY KEY)`,
		`CREATE OR REPLACE FUNCTION lo_slow_seq() RETURNS trigger LANGUAGE plpgsql AS $f$
		 BEGIN
		   IF NEW.scope = TG_ARGV[0] THEN
		     DELETE FROM lo_flag WHERE id = 1;
		     IF FOUND THEN PERFORM pg_sleep(3); END IF;
		   END IF;
		   RETURN NEW;
		 END $f$`,
		`DROP TRIGGER IF EXISTS lo_slow_seq ON scope_sequences`,
		fmt.Sprintf(`CREATE TRIGGER lo_slow_seq AFTER UPDATE ON scope_sequences FOR EACH ROW EXECUTE FUNCTION lo_slow_seq('%s')`, scope),
		`INSERT INTO lo_flag VALUES (1) ON CONFLICT DO NOTHING`,
	} {
		if _, err := r.obs.Exec(ctx, q); err != nil {
			r.t.Fatalf("arm window: %v", err)
		}
	}
}

// armAllocWindow parks the NEXT payment_allocations insert in pg_sleep, once: a settle that has locked
// its invoice and written its payment header sits there, before it reaches the invoices counter row.
func (r *loRig) armAllocWindow() {
	r.t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS lo_flag (id int PRIMARY KEY)`,
		`CREATE OR REPLACE FUNCTION lo_slow_alloc() RETURNS trigger LANGUAGE plpgsql AS $f$
		 BEGIN
		   DELETE FROM lo_flag WHERE id = 2;
		   IF FOUND THEN PERFORM pg_sleep(3); END IF;
		   RETURN NEW;
		 END $f$`,
		`DROP TRIGGER IF EXISTS lo_slow_alloc ON payment_allocations`,
		`CREATE TRIGGER lo_slow_alloc BEFORE INSERT ON payment_allocations FOR EACH ROW EXECUTE FUNCTION lo_slow_alloc()`,
		`INSERT INTO lo_flag VALUES (2) ON CONFLICT DO NOTHING`,
	} {
		if _, err := r.obs.Exec(ctx, q); err != nil {
			r.t.Fatalf("arm alloc window: %v", err)
		}
	}
}

// armSubledgerWindow parks the NEXT customer_transactions insert in pg_sleep, once: a multi-invoice
// writer sits there with its FIRST invoice fully re-opened (its counter row drawn and held) and its
// second invoice not yet touched, the window where a writer resting on incidental locks holds the
// counter with no lock on the row it is about to need.
func (r *loRig) armSubledgerWindow() {
	r.t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS lo_flag (id int PRIMARY KEY)`,
		`CREATE OR REPLACE FUNCTION lo_slow_sub() RETURNS trigger LANGUAGE plpgsql AS $f$
		 BEGIN
		   DELETE FROM lo_flag WHERE id = 3;
		   IF FOUND THEN PERFORM pg_sleep(3); END IF;
		   RETURN NEW;
		 END $f$`,
		`DROP TRIGGER IF EXISTS lo_slow_sub ON customer_transactions`,
		`CREATE TRIGGER lo_slow_sub BEFORE INSERT ON customer_transactions FOR EACH ROW EXECUTE FUNCTION lo_slow_sub()`,
		`INSERT INTO lo_flag VALUES (3) ON CONFLICT DO NOTHING`,
	} {
		if _, err := r.obs.Exec(ctx, q); err != nil {
			r.t.Fatalf("arm subledger window: %v", err)
		}
	}
}

func (r *loRig) seqValue(scope string) int64 {
	r.t.Helper()
	var v int64
	if err := r.obs.QueryRow(context.Background(), `SELECT next_value FROM scope_sequences WHERE scope = $1`, scope).Scan(&v); err != nil {
		r.t.Fatalf("read %s counter: %v", scope, err)
	}
	return v
}

// armSeqBeforeWindow parks the NEXT writer to hit the named counter row INSIDE its upsert, once: a
// BEFORE UPDATE trigger runs after ON CONFLICT has locked the conflicting row and before the row is
// written, so the writer sleeps holding the counter row and nothing its statement takes later. A
// writer whose row locks live in EARLIER statements (the reversal's and MarkNSF's explicit invoice
// locks) is parked already holding them under armSeqWindow; this is the one window that shows a
// writer the way the main branch really ran some of them, drawing the counter with no row held.
func (r *loRig) armSeqBeforeWindow(scope string) {
	r.t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS lo_flag (id int PRIMARY KEY)`,
		`CREATE OR REPLACE FUNCTION lo_slow_seq_before() RETURNS trigger LANGUAGE plpgsql AS $f$
		 BEGIN
		   IF NEW.scope = TG_ARGV[0] THEN
		     DELETE FROM lo_flag WHERE id = 4;
		     IF FOUND THEN PERFORM pg_sleep(3); END IF;
		   END IF;
		   RETURN NEW;
		 END $f$`,
		`DROP TRIGGER IF EXISTS lo_slow_seq_before ON scope_sequences`,
		fmt.Sprintf(`CREATE TRIGGER lo_slow_seq_before BEFORE UPDATE ON scope_sequences FOR EACH ROW EXECUTE FUNCTION lo_slow_seq_before('%s')`, scope),
		`INSERT INTO lo_flag VALUES (4) ON CONFLICT DO NOTHING`,
	} {
		if _, err := r.obs.Exec(ctx, q); err != nil {
			r.t.Fatalf("arm before window: %v", err)
		}
	}
	r.t.Cleanup(func() {
		_, _ = r.obs.Exec(context.Background(), `DROP TRIGGER IF EXISTS lo_slow_seq_before ON scope_sequences`)
	})
}

// answer waits for one writer's result, bounded.
func loAnswer(t *testing.T, what string, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(60 * time.Second):
		t.Fatalf("%s never answered", what)
		return nil
	}
}

func loBothAnswered(t *testing.T, aName string, a error, bName string, b error) {
	t.Helper()
	t.Logf("%s: %v | %s: %v", aName, a, bName, b)
	if a != nil || b != nil {
		t.Fatalf("NOT BOTH ANSWERED: %s %v, %s %v", aName, a, bName, b)
	}
}

func loIsDeadlock(err error) bool {
	return err != nil && strings.Contains(err.Error(), "deadlock detected")
}

// - the door's first steps, modelled -

// doorShaped runs what the quote counter door will do on its own transaction: the ORDER row first, then
// the shared non-incrementing payments lock, then (hold) the existing invoice row or a new invoice's
// counter row, then the door's own payment write. `holding` is closed once the lock is held and
// `release` gates the next step, so the test decides when the door moves.
func (r *loRig) doorShaped(orderID, existingInvoiceID string, holding chan<- struct{}, release <-chan struct{}) error {
	ctx := context.Background()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM orders WHERE id = $1::uuid FOR UPDATE`, orderID); err != nil {
		return fmt.Errorf("door: lock order: %w", err)
	}
	if err := r.repo.LockPaymentsSequenceTx(ctx, tx); err != nil {
		return err
	}
	if holding != nil {
		close(holding)
	}
	if release != nil {
		<-release
	}
	if existingInvoiceID != "" {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR UPDATE`, existingInvoiceID); err != nil {
			return fmt.Errorf("door: lock invoice: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `INSERT INTO scope_sequences (scope) VALUES ('invoices')
	                                  ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1`); err != nil {
		return fmt.Errorf("door: new invoice counter: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO scope_sequences (scope) VALUES ('payments')
	                           ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1`); err != nil {
		return fmt.Errorf("door: payment feed number: %w", err)
	}
	if existingInvoiceID != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO scope_sequences (scope) VALUES ('invoices')
		                           ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1`); err != nil {
			return fmt.Errorf("door: invoice counter: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// - leg 1 and 2: a settle against ReversePayment on the same invoice -

func TestIntegration_LockOrder_SettleBesideReversePayment(t *testing.T) {
	for _, kind := range []string{"register", "paysettle"} {
		t.Run(kind, func(t *testing.T) {
			loPoolSizes(t, func(t *testing.T, size int32) {
				r := newLORig(t, size)
				inv := r.invoice("A", 1_000_000)
				p := r.payOnce(inv, 10_000)
				r.armSeqWindow("payments")
				revErr := make(chan error, 1)
				go func() {
					_, err := r.repo.ReversePayment(context.Background(), payments.ReverseInput{
						PaymentID: p.ID, Reason: "lock order leg", ActorID: r.userID, IsManager: true}, time.Now())
					revErr <- err
				}()
				r.waitSessions("reversal parked holding the payments counter row", 1, condSleepingInSeq)
				setErr := make(chan error, 1)
				go func() { setErr <- r.settle(kind, inv, 500) }()
				r.waitSessions("settle queued behind the reversal", 1, condLockWaiter)
				loBothAnswered(t, "reversal", loAnswer(t, "reversal", revErr), kind, loAnswer(t, kind, setErr))
			})
		})
	}
}

// - leg 3: a door-shaped writer against a settle, with an existing invoice and without one -

func TestIntegration_LockOrder_DoorShapedBesideSettle(t *testing.T) {
	for _, existing := range []bool{true, false} {
		for _, kind := range []string{"register", "paysettle"} {
			name := fmt.Sprintf("%s invoice, %s", map[bool]string{true: "existing", false: "no"}[existing], kind)
			t.Run(name, func(t *testing.T) {
				loPoolSizes(t, func(t *testing.T, size int32) {
					r := newLORig(t, size)
					settleInv := r.invoice("S", 1_000_000)
					doorInv := ""
					if existing {
						doorInv = settleInv // the credit order's invoice, already carrying its deposit
					}
					orderID := r.order()
					holding, release := make(chan struct{}), make(chan struct{})
					doorErr := make(chan error, 1)
					go func() { doorErr <- r.doorShaped(orderID, doorInv, holding, release) }()
					<-holding
					setErr := make(chan error, 1)
					go func() { setErr <- r.settle(kind, settleInv, 500) }()
					r.waitSessions("settle queued against the door", 1, condLockWaiter)
					close(release)
					loBothAnswered(t, "door", loAnswer(t, "door", doorErr), kind, loAnswer(t, kind, setErr))
				})
			})
		}
	}
}

// - leg 4: the multi-invoice card settle that locks the door's invoice first and closes the cycle last -

func TestIntegration_LockOrder_MultiInvoiceCardSettleBesideDoor(t *testing.T) {
	loPoolSizes(t, func(t *testing.T, size int32) {
		r := newLORig(t, size)
		doorInv := r.invoice("D", 1_000_000)
		otherInv := r.invoice("O", 80_000)
		orderID := r.order()
		ctx := context.Background()
		helper, err := r.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := helper.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR UPDATE`, otherInv); err != nil {
			t.Fatal(err)
		}
		setErr := make(chan error, 1)
		go func() {
			_, err := r.repo.Pay(ctx, r.custID, "CARD", fmt.Sprintf("gw-lo-%d", time.Now().UnixNano()),
				[]payments.Allocation{{InvoiceID: doorInv, AmountCents: 500}, {InvoiceID: otherInv, AmountCents: 500}})
			setErr <- err
		}()
		r.waitSessions("card settle holding the door's invoice, waiting on the other", 1, condLockWaiter)
		doorErr := make(chan error, 1)
		go func() { doorErr <- r.doorShaped(orderID, doorInv, nil, nil) }()
		// Two queued: the settle on the other invoice, and the door on whatever the settle holds.
		r.waitSessions("door queued behind the settle", 2, condLockWaiter)
		_ = helper.Rollback(ctx) // the settle moves on to the row the door already holds, or the other way round
		serr, derr := loAnswer(t, "card settle", setErr), loAnswer(t, "door", doorErr)
		loBothAnswered(t, "card settle", serr, "door", derr)
	})
}

// - leg 5: the lock does not increment -

func TestIntegration_LockOrder_LockDoesNotAdvanceTheFeed(t *testing.T) {
	loPoolSizes(t, func(t *testing.T, size int32) {
		r := newLORig(t, size)
		ctx := context.Background()

		// (a) the lock itself, taken and read inside one transaction.
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		_ = tx.QueryRow(ctx, `SELECT next_value FROM scope_sequences WHERE scope = 'payments'`).Scan(&before)
		for i := 0; i < 3; i++ { // taking it again on the same transaction is a no-op too
			if err := r.repo.LockPaymentsSequenceTx(ctx, tx); err != nil {
				t.Fatal(err)
			}
		}
		_ = tx.QueryRow(ctx, `SELECT next_value FROM scope_sequences WHERE scope = 'payments'`).Scan(&after)
		_ = tx.Rollback(ctx)
		if before != after {
			t.Fatalf("the lock advanced the payments feed from %d to %d", before, after)
		}

		// (b) a missing row is seeded at 0, so the settle's own first increment still hands out 1.
		tx, err = r.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM scope_sequences WHERE scope = 'payments'`); err != nil {
			t.Fatal(err)
		}
		if err := r.repo.LockPaymentsSequenceTx(ctx, tx); err != nil {
			t.Fatal(err)
		}
		var seeded int64
		_ = tx.QueryRow(ctx, `SELECT next_value FROM scope_sequences WHERE scope = 'payments'`).Scan(&seeded)
		_ = tx.Rollback(ctx)
		if seeded != 0 {
			t.Fatalf("a missing payments row was seeded at %d, want 0 (so the first settle is feed number 1)", seeded)
		}

		// (c) one settle advances the feed by exactly one and the payment row carries that number.
		inv := r.invoice("N", 1_000_000)
		for _, kind := range []string{"paysettle", "register"} {
			b := r.seqValue("payments")
			if err := r.settle(kind, inv, 500); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			a := r.seqValue("payments")
			var rowSeq int64
			if err := r.obs.QueryRow(ctx, `SELECT seq FROM payments WHERE customer_id = $1::uuid ORDER BY seq DESC LIMIT 1`, r.custID).Scan(&rowSeq); err != nil {
				t.Fatal(err)
			}
			if a-b != 1 || rowSeq != a {
				t.Fatalf("%s: feed went %d -> %d (advance %d), payment row seq %d; want an advance of exactly 1 carried on the row", kind, b, a, a-b, rowSeq)
			}
		}

		// (d) a door-shaped writer that locks and then writes its payment advances the feed by one.
		orderID := r.order()
		b := r.seqValue("payments")
		if err := r.doorShaped(orderID, inv, nil, nil); err != nil {
			t.Fatal(err)
		}
		if a := r.seqValue("payments"); a-b != 1 {
			t.Fatalf("door-shaped: feed advanced %d, want exactly 1 (the lock must not use a number up)", a-b)
		}
	})
}

// - leg 6: the pairs read for order that could take rows in opposite orders -

// The multi-invoice reversal against the POS return's account credit on its second invoice, parked
// with a BEFORE UPDATE trigger: the reversal sleeps inside the invoices counter upsert of its first
// re-open, holding the counter row and nothing its statement takes later. A reversal that rested on
// the contra allocation's incidental foreign key share lock (the main shape, no explicit row locks)
// reached that window with no invoice row held at all, and the credit, holding the second invoice
// FOR UPDATE while it waited on the counter, met the reversal's next key share head on: the probe
// on that shape aborted the reversal at the contra allocation insert. The explicit row locks in
// ascending id order, taken before the first counter draw, close the window: the parked reversal
// already holds every allocated row, the credit queues, and both answer.
func TestIntegration_LockOrder_MultiInvoiceReversalBesidePOSAccountCreditBeforeWindow(t *testing.T) {
	loPoolSizes(t, func(t *testing.T, size int32) {
		r := newLORig(t, size)
		one := r.invoice("W1", 1_000_000)
		two := r.invoice("W2", 1_000_000)
		p, err := r.repo.Pay(context.Background(), r.custID, "CARD", fmt.Sprintf("gw-lo-%d", time.Now().UnixNano()),
			[]payments.Allocation{{InvoiceID: one, AmountCents: 10_000}, {InvoiceID: two, AmountCents: 10_000}})
		if err != nil {
			t.Fatalf("seed payment: %v", err)
		}
		r.armSeqBeforeWindow("invoices")
		revErr := make(chan error, 1)
		go func() {
			_, err := r.repo.ReversePayment(context.Background(), payments.ReverseInput{
				PaymentID: p.ID, Reason: "lock order leg", ActorID: r.userID, IsManager: true}, time.Now())
			revErr <- err
		}()
		r.waitSessions("reversal parked inside the invoices counter upsert of its first re-open", 1, condSleepingInSeq)
		// The credit goes on the invoice the reversal's loop has not reached, the second one.
		second := two
		if one > two {
			second = one
		}
		credErr := make(chan error, 1)
		go func() {
			ctx := context.Background()
			tx, err := r.pool.Begin(ctx)
			if err != nil {
				credErr <- err
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := pos.NewRepository(r.pool).ApplyCreditToInvoice(ctx, tx, second, 300); err != nil {
				credErr <- err
				return
			}
			credErr <- tx.Commit(ctx)
		}()
		r.waitSessions("credit queued", 1, condLockWaiter)
		loBothAnswered(t, "reversal", loAnswer(t, "reversal", revErr), "pos credit", loAnswer(t, "pos credit", credErr))
	})
}

// A NSF bounce against a settle on the invoice the bounced payment paid. MarkNSF locks the payment
// row, then each allocated invoice row in ascending id order, and only then re-opens the invoice
// (drawing the invoices counter row): the re-open never wants the counter while a settle holds the
// invoice, so both answer.
func TestIntegration_LockOrder_NSFBesideSettle(t *testing.T) {
	for _, kind := range []string{"register", "paysettle"} {
		t.Run(kind, func(t *testing.T) {
			loPoolSizes(t, func(t *testing.T, size int32) {
				r := newLORig(t, size)
				inv := r.invoice("F", 1_000_000)
				p := r.payOnce(inv, 10_000)
				r.armAllocWindow()
				setErr := make(chan error, 1)
				go func() { setErr <- r.settle(kind, inv, 500) }()
				r.waitSessions("settle parked holding the invoice, before the invoices counter row", 1, condSleepingInAlloc)
				nsfErr := make(chan error, 1)
				go func() {
					ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
						UserID: r.userID, Tier: staffcontext.TierLeadership, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE"})
					_, err := till.NewService(till.NewRepository(r.pool)).MarkNSF(ctx, till.NSFInput{PaymentID: p.ID, Reason: "lock order leg"})
					nsfErr <- err
				}()
				r.waitSessions("bounce queued", 1, condLockWaiter)
				loBothAnswered(t, "nsf", loAnswer(t, "nsf", nsfErr), kind, loAnswer(t, kind, setErr))
			})
		})
	}
}

// The multi-invoice reversal against the POS return's account credit, parked past its first
// re-open (inside the contra subledger insert of the loop's first invoice). On the main shape the
// reversal reached that window holding the invoices counter with no lock on its second invoice: the
// contra allocation's foreign key share lock covered only the invoice the loop had already reached,
// and a credit holding that row FOR UPDATE while it waited on the counter met the reversal's next
// share lock head on. The explicit row locks in ascending id order, all taken before the first
// counter draw, close the window: the parked reversal already holds every allocated row.
func TestIntegration_LockOrder_MultiInvoiceReversePaymentBesidePOSAccountCredit(t *testing.T) {
	loPoolSizes(t, func(t *testing.T, size int32) {
		r := newLORig(t, size)
		one := r.invoice("U1", 1_000_000)
		two := r.invoice("U2", 1_000_000)
		p, err := r.repo.Pay(context.Background(), r.custID, "CARD", fmt.Sprintf("gw-lo-%d", time.Now().UnixNano()),
			[]payments.Allocation{{InvoiceID: one, AmountCents: 10_000}, {InvoiceID: two, AmountCents: 10_000}})
		if err != nil {
			t.Fatalf("seed payment: %v", err)
		}
		r.armSubledgerWindow()
		revErr := make(chan error, 1)
		go func() {
			_, err := r.repo.ReversePayment(context.Background(), payments.ReverseInput{
				PaymentID: p.ID, Reason: "lock order leg", ActorID: r.userID, IsManager: true}, time.Now())
			revErr <- err
		}()
		r.waitSessions("reversal parked past its first re-open, holding the invoices counter row", 1, condSleepingInSub)
		// A credit on EACH invoice: whichever the reversal reaches second is the one that holds its
		// row while the reversal holds the counter, so the leg does not depend on the order the
		// database returns the payment's allocations in.
		credOne, credTwo := make(chan error, 1), make(chan error, 1)
		for _, leg := range []struct {
			inv string
			ch  chan error
		}{{one, credOne}, {two, credTwo}} {
			go func(inv string, ch chan error) {
				ctx := context.Background()
				tx, err := r.pool.Begin(ctx)
				if err != nil {
					ch <- err
					return
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err := pos.NewRepository(r.pool).ApplyCreditToInvoice(ctx, tx, inv, 300); err != nil {
					ch <- err
					return
				}
				ch <- tx.Commit(ctx)
			}(leg.inv, leg.ch)
		}
		r.waitSessions("credits queued", 2, condLockWaiter)
		credits := errors.Join(loAnswer(t, "credit on the first invoice", credOne), loAnswer(t, "credit on the second invoice", credTwo))
		loBothAnswered(t, "reversal", loAnswer(t, "reversal", revErr), "account credits", credits)
	})
}

// The multi-invoice reversal against NSF bounces on its invoices. MarkNSF locks its allocated
// invoice row FOR UPDATE before it draws the counter; a reversal resting on the foreign key share
// lock reaches its second invoice holding the counter with no row lock, and the bounce holding that
// row while it waits on the counter closes the cycle. Both writers taking their rows in ascending id
// order before any counter draw keeps the pair green whatever order the ids fall in.
func TestIntegration_LockOrder_MultiInvoiceReversePaymentBesideNSF(t *testing.T) {
	loPoolSizes(t, func(t *testing.T, size int32) {
		r := newLORig(t, size)
		one := r.invoice("V1", 1_000_000)
		two := r.invoice("V2", 1_000_000)
		p, err := r.repo.Pay(context.Background(), r.custID, "CARD", fmt.Sprintf("gw-lo-%d", time.Now().UnixNano()),
			[]payments.Allocation{{InvoiceID: one, AmountCents: 10_000}, {InvoiceID: two, AmountCents: 10_000}})
		if err != nil {
			t.Fatalf("seed payment: %v", err)
		}
		// Each invoice's bounce is its own payment on that invoice alone, and its own goroutine:
		// whichever invoice the reversal has not reached yet is the one whose bounce holds the row.
		nsfOne, nsfTwo := r.payOnce(one, 10_000), r.payOnce(two, 10_000)
		r.armSubledgerWindow()
		revErr := make(chan error, 1)
		go func() {
			_, err := r.repo.ReversePayment(context.Background(), payments.ReverseInput{
				PaymentID: p.ID, Reason: "lock order leg", ActorID: r.userID, IsManager: true}, time.Now())
			revErr <- err
		}()
		r.waitSessions("reversal parked past its first re-open, holding the invoices counter row", 1, condSleepingInSub)
		bOne, bTwo := make(chan error, 1), make(chan error, 1)
		for _, leg := range []struct {
			pay string
			ch  chan error
		}{{nsfOne.ID, bOne}, {nsfTwo.ID, bTwo}} {
			go func(payID string, ch chan error) {
				ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
					UserID: r.userID, Tier: staffcontext.TierLeadership, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE"})
				_, err := till.NewService(till.NewRepository(r.pool)).MarkNSF(ctx, till.NSFInput{PaymentID: payID, Reason: "lock order leg"})
				ch <- err
			}(leg.pay, leg.ch)
		}
		r.waitSessions("bounces queued", 2, condLockWaiter)
		bounces := errors.Join(loAnswer(t, "bounce on the first invoice", bOne), loAnswer(t, "bounce on the second invoice", bTwo))
		loBothAnswered(t, "reversal", loAnswer(t, "reversal", revErr), "bounces", bounces)
	})
}

// A multi-invoice NSF bounce beside a settle that names the same invoices in the OTHER order. The
// bounce walks its invoices in ascending id order; the settle locks its allocations in the order the
// caller names them, which nothing pins to ascending. Two writers taking the same rows in opposite
// orders is a deadlock waiting for its interleaving, so the leg builds that interleaving exactly,
// with one helper and no grant-timing window: the helper holds the lower id row, the bounce queues
// on it, and only then does the settle run, taking the higher id row freely (nothing competes for
// it yet) and queueing for the lower BEHIND the bounce. When the helper steps aside the bounce is
// granted the lower row and asks for the higher, which the settle now holds, while the settle asks
// for the lower, which the bounce holds: a cycle with both sides provably parked. The payments
// counter row is what removes the interleaving: once the bounce takes it before any invoice row,
// the settle queues at the counter instead of ever holding the higher row, and the two never
// overlap their rows.
func TestIntegration_LockOrder_MultiInvoiceNSFBesideSettleOtherOrder(t *testing.T) {
	loPoolSizes(t, func(t *testing.T, size int32) {
		r := newLORig(t, size)
		low, high := r.invoice("Z1", 1_000_000), r.invoice("Z2", 1_000_000)
		if low > high {
			low, high = high, low
		}
		p, err := r.repo.Pay(context.Background(), r.custID, "CHEQUE", "",
			[]payments.Allocation{{InvoiceID: low, AmountCents: 5_000}, {InvoiceID: high, AmountCents: 5_000}})
		if err != nil {
			t.Fatalf("seed payment: %v", err)
		}
		ctx := context.Background()
		holdLow, err := r.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := holdLow.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR UPDATE`, low); err != nil {
			t.Fatal(err)
		}
		nsfErr := make(chan error, 1)
		go func() {
			ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
				UserID: r.userID, Tier: staffcontext.TierLeadership, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE"})
			_, err := till.NewService(till.NewRepository(r.pool)).MarkNSF(ctx, till.NSFInput{PaymentID: p.ID, Reason: "lock order leg"})
			nsfErr <- err
		}()
		// The barriers name the statements, not just any lock waiter: the bounce's invoice lock and
		// the settle's own locks are plain SELECTs whose text pg_stat_activity shows in full, and a
		// generic waiter count could fire on an unrelated session before a side has truly queued.
		r.waitSessions("bounce queued on the lower id row", 1,
			`wait_event_type = 'Lock' AND query LIKE '%SELECT 1 FROM invoices%'`)
		setErr := make(chan error, 1)
		go func() {
			_, err := r.repo.Pay(context.Background(), r.custID, "CARD", fmt.Sprintf("gw-lo-%d", time.Now().UnixNano()),
				[]payments.Allocation{{InvoiceID: high, AmountCents: 500}, {InvoiceID: low, AmountCents: 500}})
			setErr <- err
		}()
		// Before the fix the settle takes the higher row and queues on the lower, behind the bounce;
		// after it the bounce holds the payments counter first and the settle queues there instead,
		// never touching the higher row. Either shape proves each side is committed before the
		// helper steps aside.
		r.waitSessions("settle queued on its own next lock", 1,
			`wait_event_type = 'Lock' AND (query LIKE '%balance_cents%' OR query LIKE '%scope_sequences%')`)
		_ = holdLow.Rollback(ctx)
		loBothAnswered(t, "nsf", loAnswer(t, "nsf", nsfErr), "settle", loAnswer(t, "settle", setErr))
	})
}

// A bounce beside the POS return's account credit on the same invoice, with the bounce parked inside
// its invoices counter upsert by a BEFORE UPDATE trigger: it sleeps holding the counter row and
// nothing its statement takes later. On the main shape the bounce's re-open statement was its first
// touch of the invoice, so this window held the counter with no row at all, and the credit, taking
// the row FOR UPDATE on its way to the counter, closed a cycle. The row locks taken before the
// re-open keep the parked bounce holding every row it will need: the credit queues and both answer.
func TestIntegration_LockOrder_NSFBesidePOSAccountCreditBeforeWindow(t *testing.T) {
	loPoolSizes(t, func(t *testing.T, size int32) {
		r := newLORig(t, size)
		inv := r.invoice("Y1", 1_000_000)
		p := r.payOnce(inv, 10_000)
		r.armSeqBeforeWindow("invoices")
		nsfErr := make(chan error, 1)
		go func() {
			ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
				UserID: r.userID, Tier: staffcontext.TierLeadership, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE"})
			_, err := till.NewService(till.NewRepository(r.pool)).MarkNSF(ctx, till.NSFInput{PaymentID: p.ID, Reason: "lock order leg"})
			nsfErr <- err
		}()
		r.waitSessions("bounce parked inside its invoices counter upsert", 1, condSleepingInSeq)
		credErr := make(chan error, 1)
		go func() {
			ctx := context.Background()
			tx, err := r.pool.Begin(ctx)
			if err != nil {
				credErr <- err
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := pos.NewRepository(r.pool).ApplyCreditToInvoice(ctx, tx, inv, 300); err != nil {
				credErr <- err
				return
			}
			credErr <- tx.Commit(ctx)
		}()
		r.waitSessions("credit queued", 1, condLockWaiter)
		loBothAnswered(t, "nsf", loAnswer(t, "nsf", nsfErr), "pos credit", loAnswer(t, "pos credit", credErr))
	})
}
