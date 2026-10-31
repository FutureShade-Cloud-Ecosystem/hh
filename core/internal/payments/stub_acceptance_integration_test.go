//go:build integration

// stub_acceptance_integration_test.go, DIB T3: StubGateway-mode acceptance for the payment rail.
//
// D-RUNPAY-2 is recorded as a decision ("ship on the deterministic stub; flip to the live gateway by
// config") but it had no executable evidence: the stub was proved only by unit tests against a fake
// store, and the flag-flip was proved by nothing at all. Those are the two tests here.
//
//	TestAcceptance_StubGateway_SaleToCaptureToSettlementRows
//	    the WHOLE stub path against the real DB: a portal payer with a vaulted method pays two
//	    finalized invoices → the stub captures → the settlement rows land atomically (payments +
//	    payment_allocations + customer_transactions + the AR balances + the payment.received outbox
//	    row), and a replay of the same request settles nothing new.
//
//	TestAcceptance_GatewaySelector_FlagFlipSelectsLiveWithoutCredentials
//	    the flip itself, at the seam that decides it: the service dispatches to whatever gateway it
//	    was constructed with, and the Live()/Name() facts a fail-closed boot guard reads change with
//	    it. Driven with a live-SHAPED gateway that carries no Run credentials and reaches no network,
//	    so the live branch is proved selectable without a live account.
//
// NOTE (escalated, see REPORT-W2-T.md): there is no flag. cmd/server/main.go hardcodes
// payments.NewStubGateway() at the only construction site, and the fail-closed prod boot guard the
// PaymentGateway doc promises ("a prod boot must refuse a non-live gateway") is not implemented.
// These tests therefore prove the SEAM is flip-ready; they cannot prove a switch that does not exist.
//
// Run: make reset && go test -tags integration ./internal/payments/...
package payments

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// - fixtures (test-owned, fully torn down, order-independent) -

type payRig struct {
	pool       *pgxpool.Pool
	svc        *Service
	customerID string
	invoiceA   string // 40000 payable
	invoiceB   string // 15000 payable
	methodID   string
}

// newPayRig seeds a tenant with two FINALIZED invoices and vaults a payment method through the
// service's own AddMethod path (so the token under test is one the gateway actually minted).
func newPayRig(t *testing.T, gw PaymentGateway) *payRig {
	t.Helper()
	ctx := context.Background()
	pool := intPool(t)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()%1_000_000_000)
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('STUBACC-'||$1, 'Stub Acceptance '||$1, 'CONTRACTOR', $2::uuid, 1000000) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		// In dependency order, each delete loud: the intents the pay flow claimed hold the customer, so
		// they go first (a silent miss here left a customer and its intent behind on every run).
		for _, q := range []struct{ table, sql string }{
			{"payment_intents", `DELETE FROM payment_intents WHERE customer_id = $1::uuid`},
			{"events_outbox", `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`},
			{"customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`},
			{"payment_allocations", `DELETE FROM payment_allocations WHERE payment_id IN
				(SELECT id FROM payments WHERE customer_id = $1::uuid)`},
			{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid`},
			{"portal_payment_methods", `DELETE FROM portal_payment_methods WHERE customer_id = $1::uuid`},
			{"invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`},
			{"customers", `DELETE FROM customers WHERE id = $1::uuid`},
		} {
			if _, err := pool.Exec(c, q.sql, customerID); err != nil {
				t.Errorf("cleanup %s: %v", q.table, err)
			}
		}
	})

	seedInv := func(tag string, cents int64) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date,
			                       subtotal_cents, tax_cents, total_cents, balance_cents)
			 VALUES ('INV-STUB-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30, $4, 0, $4, $4)
			 RETURNING id::text`, tag+"-"+suffix, customerID, branchID, cents).Scan(&id); err != nil {
			t.Fatalf("seed invoice %s: %v", tag, err)
		}
		return id
	}

	svc := NewService(NewRepository(pool), gw)
	rig := &payRig{
		pool: pool, svc: svc, customerID: customerID,
		invoiceA: seedInv("A", 40000), invoiceB: seedInv("B", 15000),
	}

	m, err := svc.AddMethod(ctx, customerID, AddMethodRequest{Nonce: "nonce-" + suffix, MakeDefault: true})
	if err != nil {
		t.Fatalf("vault a method through the gateway: %v", err)
	}
	rig.methodID = m.ID
	return rig
}

// - T3 / 1, the whole stub path, end to end, against the real database -

// countingGateway wraps the real StubGateway and records dispatches, so a test can assert not just
// "no second payment row" but the stronger "the processor was never asked a second time", the
// difference between a customer who was charged twice and refunded, and one who was never charged.
type countingGateway struct {
	*StubGateway
	charges int
}

func (g *countingGateway) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	g.charges++
	return g.StubGateway.Charge(ctx, req)
}

func TestAcceptance_StubGateway_SaleToCaptureToSettlementRows(t *testing.T) {
	ctx := context.Background()
	gw := &countingGateway{StubGateway: NewStubGateway()}
	rig := newPayRig(t, gw)

	// The gateway under test is the deterministic stub, and it says so, this is the fact a
	// fail-closed prod boot guard is supposed to read before it registers the payment routes.
	if rig.svc.Gateway().Live() {
		t.Fatal("the acceptance rail must be the NON-live stub; a live gateway here would move real money")
	}
	if got := rig.svc.Gateway().Name(); got != "stub" {
		t.Fatalf("gateway name = %q, want \"stub\"", got)
	}

	req := PayRequest{InvoiceIDs: []string{rig.invoiceA, rig.invoiceB}, PaymentMethodID: rig.methodID}

	// - CAPTURE -
	pay, err := rig.svc.Pay(ctx, rig.customerID, req)
	if err != nil {
		t.Fatalf("stub pay: %v", err)
	}
	if pay.AmountCents != 55000 {
		t.Fatalf("ONE charge for the summed balance: got %d, want 55000", pay.AmountCents)
	}
	if pay.GatewayRef == "" {
		t.Fatal("a capture must carry a reconcilable gateway ref (the stub mints a real one)")
	}
	if pay.Status != "RECEIVED" {
		t.Fatalf("payment status = %q, want RECEIVED", pay.Status)
	}

	// - SETTLEMENT ROWS -
	// payments: exactly one row, carrying the captured ref.
	var nPayments int
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE id = $1::uuid AND gateway_ref = $2 AND amount_cents = 55000`,
		pay.ID, pay.GatewayRef).Scan(&nPayments); err != nil {
		t.Fatalf("read payments row: %v", err)
	}
	if nPayments != 1 {
		t.Fatalf("payments rows for this capture = %d, want 1", nPayments)
	}

	// payment_allocations: the single charge split across BOTH invoices, summing to the capture.
	rows, err := rig.pool.Query(ctx,
		`SELECT invoice_id::text, amount_cents FROM payment_allocations WHERE payment_id = $1::uuid ORDER BY amount_cents DESC`, pay.ID)
	if err != nil {
		t.Fatalf("read allocations: %v", err)
	}
	defer rows.Close()
	allocs := map[string]int64{}
	var allocSum int64
	for rows.Next() {
		var inv string
		var cents int64
		if err := rows.Scan(&inv, &cents); err != nil {
			t.Fatalf("scan allocation: %v", err)
		}
		allocs[inv] = cents
		allocSum += cents
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate allocations: %v", err)
	}
	if len(allocs) != 2 || allocs[rig.invoiceA] != 40000 || allocs[rig.invoiceB] != 15000 {
		t.Fatalf("allocations = %v, want A:40000 B:15000", allocs)
	}
	if allocSum != pay.AmountCents {
		t.Fatalf("allocations sum to %d but the capture was %d, money went missing between the gateway and the ledger",
			allocSum, pay.AmountCents)
	}

	// The AR side actually moved: both invoices settle to zero and flip PAID.
	for _, inv := range []string{rig.invoiceA, rig.invoiceB} {
		var balance int64
		var status string
		if err := rig.pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, inv).
			Scan(&balance, &status); err != nil {
			t.Fatalf("read invoice %s: %v", inv, err)
		}
		if balance != 0 || status != "PAID" {
			t.Fatalf("invoice %s not settled by the capture: balance=%d status=%s", inv, balance, status)
		}
	}

	// customer_transactions: one AR PAYMENT row per settled invoice (the subledger the aging reads).
	var nTxn int
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND txn_type = 'PAYMENT'`, rig.customerID).Scan(&nTxn); err != nil {
		t.Fatalf("read customer_transactions: %v", err)
	}
	if nTxn != 2 {
		t.Fatalf("AR PAYMENT transactions = %d, want 2 (one per settled invoice)", nTxn)
	}

	// The GL hand-off is emitted in the SAME transaction as the settle, exactly once.
	var nEvent int
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox WHERE topic='payment.received' AND payload->>'payment_id' = $1`, pay.ID).
		Scan(&nEvent); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if nEvent != 1 {
		t.Fatalf("payment.received events = %d, want exactly 1", nEvent)
	}

	if gw.charges != 1 {
		t.Fatalf("the gateway was dispatched %d times for one payment, want 1", gw.charges)
	}

	// - REPLAY -
	// Wave 2 pinned the SHIPPED behaviour here: a replay of a fully-settled selection was refused by
	// the PRE-CHARGE payability validation (the invoices are PAID by then), not by the replay guard
	// the code comment described, and the instruction left in this block was, if the durable-intent
	// hardening (DIB-T3-F1) ever landed, to re-point the assertions at the guard and assert the
	// ORIGINAL payment id comes back.
	//
	// DIB-415 is that hardening, and these are the re-pointed assertions. The replay now succeeds and
	// returns the original payment, because a durable payment_intents row keyed on the server-bound
	// idempotency key is claimed and committed BEFORE the gateway is touched, and a SETTLED intent
	// replays its payment. That is a better outcome than the old refusal on two counts: the customer
	// who double-tapped Pay gets their receipt instead of a confusing "not payable" error, and, the
	// part that actually mattered, the guard now works against a processor that does NOT return a
	// stable ref for a repeated key. The old mechanism only ever worked because StubGateway derives
	// its ref from the key; see TestAcceptance_Intent_UnstableRefGateway_CannotDoubleCharge for the
	// same replay driven through a Run-shaped gateway that returns a fresh trans_id every call.
	replayed, replayErr := rig.svc.Pay(ctx, rig.customerID, req)
	if replayErr != nil {
		t.Fatalf("DIB-415 REGRESSION: the replay of a settled selection must return the ORIGINAL payment, "+
			"got error %v. Idempotency means the same request gets the same answer.", replayErr)
	}
	if replayed.ID != pay.ID {
		t.Fatalf("the replay returned payment %s, want the original %s, a different id means a second settlement", replayed.ID, pay.ID)
	}
	if gw.charges != 1 {
		t.Fatalf("the replay reached the gateway (%d charges), a live processor would have taken the money twice", gw.charges)
	}

	var totalPayments, totalAllocs, totalTxn int
	_ = rig.pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE customer_id = $1::uuid`, rig.customerID).Scan(&totalPayments)
	_ = rig.pool.QueryRow(ctx, `SELECT count(*) FROM payment_allocations WHERE payment_id = $1::uuid`, pay.ID).Scan(&totalAllocs)
	_ = rig.pool.QueryRow(ctx, `SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND txn_type='PAYMENT'`, rig.customerID).Scan(&totalTxn)
	if totalPayments != 1 || totalAllocs != 2 || totalTxn != 2 {
		t.Fatalf("a replay wrote new settlement rows: payments=%d allocations=%d txns=%d, want 1/2/2",
			totalPayments, totalAllocs, totalTxn)
	}
}

// TestAcceptance_StubGateway_DeclineLeavesNoSettlementRows is the other half of the acceptance: the
// stub's documented decline hook (an amount whose cents end in .13) must leave the AR side untouched.
// Without this, "the stub path works" only means "the happy path works".
func TestAcceptance_StubGateway_DeclineLeavesNoSettlementRows(t *testing.T) {
	ctx := context.Background()
	rig := newPayRig(t, NewStubGateway())

	// Re-price invoice A so the summed charge trips the stub's deterministic decline rule.
	if _, err := rig.pool.Exec(ctx,
		`UPDATE invoices SET total_cents = 113, balance_cents = 113, subtotal_cents = 113 WHERE id = $1::uuid`,
		rig.invoiceA); err != nil {
		t.Fatalf("re-price invoice: %v", err)
	}

	_, err := rig.svc.Pay(ctx, rig.customerID, PayRequest{
		InvoiceIDs: []string{rig.invoiceA}, PaymentMethodID: rig.methodID,
	})
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("a declined capture must surface as ErrDeclined, got %v", err)
	}

	var payments, allocs, txns int
	_ = rig.pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE customer_id = $1::uuid`, rig.customerID).Scan(&payments)
	_ = rig.pool.QueryRow(ctx, `SELECT count(*) FROM payment_allocations WHERE invoice_id = $1::uuid`, rig.invoiceA).Scan(&allocs)
	_ = rig.pool.QueryRow(ctx, `SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND txn_type='PAYMENT'`, rig.customerID).Scan(&txns)
	if payments != 0 || allocs != 0 || txns != 0 {
		t.Fatalf("a DECLINED charge left settlement rows behind: payments=%d allocations=%d txns=%d, want 0/0/0",
			payments, allocs, txns)
	}
	var balance int64
	var status string
	_ = rig.pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, rig.invoiceA).Scan(&balance, &status)
	if balance != 113 || status != "FINALIZED" {
		t.Fatalf("a declined charge moved AR: balance=%d status=%s, want 113/FINALIZED", balance, status)
	}
}

// - T3 / 2, the flag flip, at the seam that decides it -

// liveShapedGateway stands in for a configured RunGateway. It carries NO Run credentials and makes no
// network call, it only reports the identity facts a boot guard keys off (Name/Live) and records
// that the service dispatched to IT rather than to the stub. That is precisely the property a config
// flip must have: the decision is made at construction, and everything downstream follows the port.
type liveShapedGateway struct {
	name     string
	charges  int
	lastReq  ChargeRequest
	refToken string
}

func (g *liveShapedGateway) Name() string { return g.name }
func (g *liveShapedGateway) Live() bool   { return true }

func (g *liveShapedGateway) Charge(_ context.Context, req ChargeRequest) (ChargeResult, error) {
	g.charges++
	g.lastReq = req
	return ChargeResult{Outcome: OutcomeApproved, GatewayRef: "live_" + req.IdempotencyKey[:12], AuthCode: "LIVEOK"}, nil
}

func (g *liveShapedGateway) Vault(_ context.Context, req VaultRequest) (VaultedMethod, error) {
	g.refToken = "livetok_" + req.Nonce
	return VaultedMethod{Token: g.refToken, MethodType: "CARD", Brand: "VISA", Last4: "1111", ExpMonth: 1, ExpYear: 2031}, nil
}

func (g *liveShapedGateway) DetachMethod(context.Context, string) error { return nil }

var _ PaymentGateway = (*liveShapedGateway)(nil)

func TestAcceptance_GatewaySelector_FlagFlipSelectsLiveWithoutCredentials(t *testing.T) {
	ctx := context.Background()

	// - the stub side of the flip -
	stub := NewStubGateway()
	stubRig := newPayRig(t, stub)
	if stubRig.svc.Gateway().Live() || stubRig.svc.Gateway().Name() != "stub" {
		t.Fatalf("stub selection: Live=%v Name=%q, want false/\"stub\"",
			stubRig.svc.Gateway().Live(), stubRig.svc.Gateway().Name())
	}

	// - the live side of the flip, with no Run account anywhere in sight -
	live := &liveShapedGateway{name: "run"}
	liveRig := newPayRig(t, live)

	if !liveRig.svc.Gateway().Live() {
		t.Fatal("a service constructed with a live gateway must report Live(), the prod boot guard reads this")
	}
	if got := liveRig.svc.Gateway().Name(); got != "run" {
		t.Fatalf("selected gateway name = %q, want \"run\" (the audit + boot guard identify the rail by name)", got)
	}

	// The vault seam followed the flip: the stored method's token was minted by the LIVE gateway,
	// not the stub. A selector that only changed a label would fail here.
	if live.refToken == "" {
		t.Fatal("AddMethod must tokenize through the SELECTED gateway (the live one), not the stub")
	}

	// And so does the charge: the whole settle path runs against the selected gateway, and the
	// persisted gateway_ref is the one the LIVE rail minted.
	pay, err := liveRig.svc.Pay(ctx, liveRig.customerID, PayRequest{
		InvoiceIDs: []string{liveRig.invoiceA}, PaymentMethodID: liveRig.methodID,
	})
	if err != nil {
		t.Fatalf("pay through the selected live gateway: %v", err)
	}
	if live.charges != 1 {
		t.Fatalf("the live gateway saw %d charges, want 1, the flip did not reach the dispatch", live.charges)
	}
	if stubRef := stubRef("ch", liveRig.customerID, live.lastReq.IdempotencyKey); pay.GatewayRef == stubRef {
		t.Fatal("the persisted ref is the STUB's, the flip did not take at the dispatch site")
	}
	var persisted string
	if err := rigPool(liveRig).QueryRow(ctx, `SELECT gateway_ref FROM payments WHERE id = $1::uuid`, pay.ID).Scan(&persisted); err != nil {
		t.Fatalf("read persisted ref: %v", err)
	}
	if persisted != pay.GatewayRef {
		t.Fatalf("persisted gateway_ref %q != returned %q", persisted, pay.GatewayRef)
	}

	// The charge the live rail was handed is the SERVER-derived one: the amount comes from the
	// invoice balance and the idempotency key is server-bound, so flipping the flag cannot hand a
	// live processor a client-supplied amount.
	if live.lastReq.AmountCents != 40000 {
		t.Fatalf("the live rail was handed %d, want the server-derived 40000", live.lastReq.AmountCents)
	}
	if live.lastReq.IdempotencyKey == "" || live.lastReq.Currency != chargeCurrency {
		t.Fatalf("live charge request = %+v, want a server-bound key and %s", live.lastReq, chargeCurrency)
	}

	// Finally: flipping one service must not retro-flip the other. Two rails coexist, each holding
	// the gateway it was constructed with, which is what makes a staged rollout possible at all.
	if stubRig.svc.Gateway().Live() {
		t.Fatal("constructing a live service must not mutate an already-constructed stub service")
	}
}

// rigPool exposes the rig's pool to the selector test without widening the struct's surface.
func rigPool(r *payRig) *pgxpool.Pool { return r.pool }
