//go:build integration

// intent_integration_test.go, DIB-415: the double-charge proof.
//
// WHY THIS FILE EXISTS AND WHY IT COULD NOT BE WRITTEN BEFORE. Wave 2 escalated DIB-T3-F1: the
// portal replay guard checked, after the charge, whether a payment already existed for the gateway's
// returned ref. That only de-duplicates when the processor hands back the SAME ref for a repeated
// idempotency key. StubGateway does exactly that, its ref is a hash of the key, so every test that
// existed passed, and the hole was structurally invisible. Run exposes no idempotency header on
// /charge, so on the live rail a retry comes back with a fresh trans_id, matches nothing, and the
// customer is charged twice.
//
// The tests below are therefore driven by a gateway shaped like Run, not like the stub: unstableRef
// returns a NEW ref on every single call. Against the old guard it would double-charge on the first
// assertion in this file. Against the durable pre-charge intent (0103) it cannot, because the second
// request never reaches it.
//
// Run: go test -tags integration -count=1 ./internal/payments/ -run TestAcceptance_Intent
package payments

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// unstableRefGateway models the processor we actually have to survive: it approves, and it returns a
// DIFFERENT gateway ref every time, exactly as Run does for a repeated request. It is the whole point
// of this file, a guard that only works against a gateway with stable refs is not a guard, it is a
// coincidence.
type unstableRefGateway struct {
	*StubGateway
	mu      sync.Mutex
	charges int
	refs    []string
}

func newUnstableRefGateway() *unstableRefGateway {
	return &unstableRefGateway{StubGateway: NewStubGateway()}
}

func (g *unstableRefGateway) Name() string { return "run-shaped-test" }

func (g *unstableRefGateway) Charge(_ context.Context, req ChargeRequest) (ChargeResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.charges++
	ref := fmt.Sprintf("run_trans_%d_%d", req.AmountCents, g.charges)
	g.refs = append(g.refs, ref)
	return ChargeResult{Outcome: OutcomeApproved, GatewayRef: ref, AuthCode: "OK", Message: "approved"}, nil
}

func (g *unstableRefGateway) chargeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.charges
}

// settlementCounts reads the three things a second charge would necessarily disturb.
func settlementCounts(t *testing.T, rig *payRig) (payments, allocations, txns int) {
	t.Helper()
	ctx := context.Background()
	if err := rig.pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE customer_id = $1::uuid`, rig.customerID).Scan(&payments); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`,
		rig.customerID).Scan(&allocations); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND txn_type = 'PAYMENT'`,
		rig.customerID).Scan(&txns); err != nil {
		t.Fatalf("count subledger txns: %v", err)
	}
	return
}

// cleanupIntents removes the intents this tenant created. payment_intents has no FK to customers'
// delete cascade path in the rig's teardown, so the fixture owns its own rows (order-independence is
// a gate: a leaked intent would collide with the next run's identical idempotency key).
func cleanupIntents(t *testing.T, rig *payRig) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = rig.pool.Exec(context.Background(), `DELETE FROM payment_intents WHERE customer_id = $1::uuid`, rig.customerID)
	})
}

// TestAcceptance_Intent_UnstableRefGateway_CannotDoubleCharge is the acceptance DIB-415 owes: fire
// the SAME payment twice against a gateway that returns a fresh ref each time, and assert exactly
// one charge and exactly one settlement set.
func TestAcceptance_Intent_UnstableRefGateway_CannotDoubleCharge(t *testing.T) {
	ctx := context.Background()
	gw := newUnstableRefGateway()
	rig := newPayRig(t, gw)
	cleanupIntents(t, rig)

	req := PayRequest{InvoiceIDs: []string{rig.invoiceA, rig.invoiceB}, PaymentMethodID: rig.methodID, ExpectedCents: 55000}

	first, err := rig.svc.Pay(ctx, rig.customerID, req)
	if err != nil {
		t.Fatalf("first payment: %v", err)
	}
	if first.AmountCents != 55000 {
		t.Fatalf("first payment amount = %d, want the server-derived 55000", first.AmountCents)
	}

	// - THE RETRY -
	// Same request, same server-bound key. Under the pre-0103 guard this reached the gateway, got
	// run_trans_55000_2 back, matched no existing payment (different ref!) and wrote a SECOND
	// settlement, i.e. the customer paid $550 twice.
	second, err := rig.svc.Pay(ctx, rig.customerID, req)
	if err != nil {
		t.Fatalf("the retry must return the original payment, not an error: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("the retry returned payment %s, want the original %s", second.ID, first.ID)
	}

	if n := gw.chargeCount(); n != 1 {
		t.Fatalf("the gateway was charged %d times (refs %v), DIB-415's whole point is that this is 1", n, gw.refs)
	}
	pays, allocs, txns := settlementCounts(t, rig)
	if pays != 1 || allocs != 2 || txns != 2 {
		t.Fatalf("settlement rows after the retry = payments %d / allocations %d / txns %d, want 1/2/2", pays, allocs, txns)
	}

	// The AR side must show ONE payment's worth of relief, not two.
	var totalPaid int64
	if err := rig.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(-amount_cents),0) FROM customer_transactions
		  WHERE customer_id = $1::uuid AND txn_type = 'PAYMENT'`, rig.customerID).Scan(&totalPaid); err != nil {
		t.Fatalf("sum subledger: %v", err)
	}
	if totalPaid != 55000 {
		t.Fatalf("AR relief = %d cents, want 55000, anything more means the account was credited twice", totalPaid)
	}

	// And exactly ONE intent exists, SETTLED, naming the payment.
	var nIntents int
	var status, paymentID string
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_intents WHERE customer_id = $1::uuid`, rig.customerID).Scan(&nIntents); err != nil {
		t.Fatalf("count intents: %v", err)
	}
	if nIntents != 1 {
		t.Fatalf("payment_intents rows = %d, want exactly 1, the key is what makes the retry the same request", nIntents)
	}
	if err := rig.pool.QueryRow(ctx,
		`SELECT status, COALESCE(payment_id::text,'') FROM payment_intents WHERE customer_id = $1::uuid`,
		rig.customerID).Scan(&status, &paymentID); err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if status != IntentSettled || paymentID != first.ID {
		t.Fatalf("intent = %s/%s, want SETTLED naming payment %s", status, paymentID, first.ID)
	}
}

// TestAcceptance_Intent_CrashWindowRetry_ResumesTheStampedChargeNeverRecharges is the failure this
// whole mechanism exists for, reproduced directly: the capture succeeded at the processor, its ref
// was stamped, and the settle never committed. The invoices are still FINALIZED and fully payable,
// so every pre-charge check passes and the old code would have charged again.
//
// A stamped ref exists only after the processor approved, so the money has moved and the retry on
// the same key settles against that ref: one payment, no second charge. (Before PRO-I this answered
// ErrPaymentInFlight and left the intent stuck for a human; the unstamped window below still does.)
func TestAcceptance_Intent_CrashWindowRetry_ResumesTheStampedChargeNeverRecharges(t *testing.T) {
	ctx := context.Background()
	gw := newUnstableRefGateway()
	rig := newPayRig(t, gw)
	cleanupIntents(t, rig)

	req := PayRequest{InvoiceIDs: []string{rig.invoiceA}, PaymentMethodID: rig.methodID, ExpectedCents: 40000}

	// Reproduce the crash: claim the intent and stamp a processor ref, then stop, exactly the state
	// a pod that died between the capture and the settle commit leaves behind.
	repo := NewRepository(rig.pool)
	key := idempotencyKey(rig.customerID, req)
	intent, claimed, err := repo.ClaimIntent(ctx, IntentClaim{
		Key: key, CustomerID: rig.customerID, AmountCents: 40000,
		InvoiceIDs: rig.invoiceA, Gateway: "run-shaped-test",
	})
	if err != nil || !claimed {
		t.Fatalf("seed the crash window: claimed=%v err=%v", claimed, err)
	}
	if err := repo.StampIntentRef(ctx, intent.ID, "run-shaped-test", "run_trans_orphan_1"); err != nil {
		t.Fatalf("stamp ref: %v", err)
	}

	p, err := rig.svc.Pay(ctx, rig.customerID, req)
	if err != nil {
		t.Fatalf("a retry on a stamped, approved charge must settle it: %v", err)
	}
	if p.GatewayRef != "run_trans_orphan_1" || p.AmountCents != 40000 {
		t.Fatalf("settled %+v, want the stamped ref for 40000", p)
	}
	if n := gw.chargeCount(); n != 0 {
		t.Fatalf("the retry dispatched %d charge(s), on a live processor that is the second time this card is hit", n)
	}
	pays, allocs, txns := settlementCounts(t, rig)
	if pays != 1 || allocs != 1 || txns != 1 {
		t.Fatalf("settlement rows = %d/%d/%d, want exactly 1/1/1", pays, allocs, txns)
	}
	var istatus string
	if err := rig.pool.QueryRow(ctx, `SELECT status FROM payment_intents WHERE id = $1::uuid`, intent.ID).Scan(&istatus); err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if istatus != IntentSettled {
		t.Fatalf("intent = %s, want SETTLED", istatus)
	}

	// A further press replays the same payment and writes nothing more.
	again, err := rig.svc.Pay(ctx, rig.customerID, req)
	if err != nil || again.ID != p.ID {
		t.Fatalf("replay = %+v err=%v, want the settled payment %s", again, err, p.ID)
	}
	if pays, _, _ := settlementCounts(t, rig); pays != 1 || gw.chargeCount() != 0 {
		t.Fatalf("replay wrote rows or charged: payments=%d charges=%d", pays, gw.chargeCount())
	}
}

// The other crash window: the intent is CHARGING with no ref (the processor's answer never came
// back). That one cannot be told from "never saw it", so it is refused as in flight and never
// charged, never settled.
func TestAcceptance_Intent_CrashWindowRetry_UnstampedRefusesRatherThanRecharges(t *testing.T) {
	ctx := context.Background()
	gw := newUnstableRefGateway()
	rig := newPayRig(t, gw)
	cleanupIntents(t, rig)

	req := PayRequest{InvoiceIDs: []string{rig.invoiceA}, PaymentMethodID: rig.methodID, ExpectedCents: 40000}
	repo := NewRepository(rig.pool)
	if _, claimed, err := repo.ClaimIntent(ctx, IntentClaim{
		Key: idempotencyKey(rig.customerID, req), CustomerID: rig.customerID, AmountCents: 40000,
		InvoiceIDs: rig.invoiceA, Gateway: "run-shaped-test",
	}); err != nil || !claimed {
		t.Fatalf("seed the crash window: claimed=%v err=%v", claimed, err)
	}

	_, err := rig.svc.Pay(ctx, rig.customerID, req)
	if !errors.Is(err, ErrPaymentInFlight) {
		t.Fatalf("a retry into an unstamped crash window must return ErrPaymentInFlight, got %v", err)
	}
	if n := gw.chargeCount(); n != 0 {
		t.Fatalf("the retry dispatched %d charge(s)", n)
	}
	if pays, allocs, txns := settlementCounts(t, rig); pays != 0 || allocs != 0 || txns != 0 {
		t.Fatalf("a refused retry wrote settlement rows: %d/%d/%d, want 0/0/0", pays, allocs, txns)
	}
}

// TestAcceptance_Intent_ConcurrentDuplicates_OnlyOneReachesTheGateway is the race the fast-path read
// alone could not settle: two identical requests arriving at the same instant. The guard is the
// unique index, so the database picks the winner and the loser cannot charge.
func TestAcceptance_Intent_ConcurrentDuplicates_OnlyOneReachesTheGateway(t *testing.T) {
	ctx := context.Background()
	gw := newUnstableRefGateway()
	rig := newPayRig(t, gw)
	cleanupIntents(t, rig)

	req := PayRequest{InvoiceIDs: []string{rig.invoiceA, rig.invoiceB}, PaymentMethodID: rig.methodID, ExpectedCents: 55000}

	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = rig.svc.Pay(ctx, rig.customerID, req)
		}(i)
	}
	close(start)
	wg.Wait()

	// One of them may legitimately win, and the other may either replay the settled payment or be
	// refused as in-flight, both are correct. What is NOT correct, under any interleaving, is two
	// charges.
	if n := gw.chargeCount(); n != 1 {
		t.Fatalf("concurrent duplicates dispatched %d charges (refs %v), want exactly 1", n, gw.refs)
	}
	pays, allocs, txns := settlementCounts(t, rig)
	if pays != 1 || allocs != 2 || txns != 2 {
		t.Fatalf("concurrent duplicates settled %d/%d/%d, want 1/2/2 (results: %v)", pays, allocs, txns, results)
	}
}

// barrierStore wraps the real Repository and holds every caller inside GetIntentByKey until N of
// them have arrived, then releases them together.
//
// WHY THIS CONTRAPTION IS NECESSARY, stated plainly because it is the kind of thing that looks like
// over-engineering until you have watched a mutation survive. Pay has TWO defences: a fast-path read
// (cheap, answers the common replay) and ClaimIntent's atomic INSERT ... ON CONFLICT ... WHERE (the
// actual guard). In ordinary timing the fast path answers first, which means a test that merely runs
// two goroutines proves the fast path and says nothing at all about the claim. I confirmed that the
// hard way: widening the claim's conflict clause to let ANY held intent be re-claimed left
// TestAcceptance_Intent_ConcurrentDuplicates green three runs in a row.
//
// So this store forces the interleaving that only the claim can survive: both callers read "no
// intent exists", both proceed, and the database picks the winner. That is the real race, and this
// is the only test in the file that exercises it.
type barrierStore struct {
	*Repository
	once    sync.Once
	arrived sync.WaitGroup
	release chan struct{}
}

func newBarrierStore(repo *Repository, n int) *barrierStore {
	b := &barrierStore{Repository: repo, release: make(chan struct{})}
	b.arrived.Add(n)
	return b
}

func (b *barrierStore) GetIntentByKey(ctx context.Context, key string) (Intent, bool, error) {
	in, ok, err := b.Repository.GetIntentByKey(ctx, key)
	b.arrived.Done()
	b.once.Do(func() {
		go func() {
			b.arrived.Wait()
			close(b.release)
		}()
	})
	<-b.release
	return in, ok, err
}

// TestAcceptance_Intent_SimultaneousClaim_UniqueIndexPicksOneWinner drives two identical payments
// through the fast path SIMULTANEOUSLY, so the only thing standing between the customer and a double
// charge is payment_intents' unique index and the guarded conflict clause above it.
func TestAcceptance_Intent_SimultaneousClaim_UniqueIndexPicksOneWinner(t *testing.T) {
	ctx := context.Background()
	gw := newUnstableRefGateway()
	rig := newPayRig(t, gw)
	cleanupIntents(t, rig)

	// Rebuild the service over the barrier-wrapped store; everything else is the production path.
	barrier := newBarrierStore(NewRepository(rig.pool), 2)
	svc := NewService(barrier, gw)

	req := PayRequest{InvoiceIDs: []string{rig.invoiceA, rig.invoiceB}, PaymentMethodID: rig.methodID, ExpectedCents: 55000}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.Pay(ctx, rig.customerID, req)
		}(i)
	}
	wg.Wait()

	if n := gw.chargeCount(); n != 1 {
		t.Fatalf("two callers past the fast path dispatched %d charges (refs %v), want exactly 1, "+
			"the claim's ON CONFLICT ... WHERE is what has to hold here, and it did not (errs: %v)", n, gw.refs, errs)
	}
	pays, allocs, txns := settlementCounts(t, rig)
	if pays != 1 || allocs != 2 || txns != 2 {
		t.Fatalf("simultaneous claims settled %d/%d/%d, want 1/2/2 (errs: %v)", pays, allocs, txns, errs)
	}
	// Exactly one intent, and it must not have been re-claimed: a second claim would bump attempts.
	var nIntents, attempts int
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(attempts),0) FROM payment_intents WHERE customer_id = $1::uuid`,
		rig.customerID).Scan(&nIntents, &attempts); err != nil {
		t.Fatalf("read intents: %v", err)
	}
	if nIntents != 1 || attempts != 1 {
		t.Fatalf("intents = %d rows / max attempts %d, want 1/1, attempts > 1 means the loser re-claimed a held intent", nIntents, attempts)
	}
}

// TestAcceptance_Intent_DeclineReleasesTheKeyForAGenuineRetry guards the other direction. A guard
// that refuses everything is trivially safe and useless: a DECLINE moved no money, so the customer
// must be able to fix their card and try the same invoices again.
func TestAcceptance_Intent_DeclineReleasesTheKeyForAGenuineRetry(t *testing.T) {
	ctx := context.Background()
	// The real StubGateway declines an amount whose cents end in .13, a documented, deterministic
	// hook, so this exercises the production decline path rather than a bespoke fake.
	gw := &countingGateway{StubGateway: NewStubGateway()}
	rig := newPayRig(t, gw)
	cleanupIntents(t, rig)

	// Re-price invoice A so the summed charge trips the decline rule (40013 + 15000 = 55013).
	if _, err := rig.pool.Exec(ctx,
		`UPDATE invoices SET total_cents = 40013, balance_cents = 40013 WHERE id = $1::uuid`, rig.invoiceA); err != nil {
		t.Fatalf("re-price for decline: %v", err)
	}

	req := PayRequest{InvoiceIDs: []string{rig.invoiceA, rig.invoiceB}, PaymentMethodID: rig.methodID, ExpectedCents: 55013}
	if _, err := rig.svc.Pay(ctx, rig.customerID, req); !errors.Is(err, ErrDeclined) {
		t.Fatalf("declined charge = %v, want ErrDeclined", err)
	}

	var status string
	if err := rig.pool.QueryRow(ctx,
		`SELECT status FROM payment_intents WHERE customer_id = $1::uuid`, rig.customerID).Scan(&status); err != nil {
		t.Fatalf("read intent after decline: %v", err)
	}
	if status != IntentFailed {
		t.Fatalf("intent after a decline = %s, want FAILED, a decline moved no money, so the key must be re-claimable", status)
	}

	// Fix the amount and retry the SAME selection. It must reach the gateway this time.
	if _, err := rig.pool.Exec(ctx,
		`UPDATE invoices SET total_cents = 40000, balance_cents = 40000 WHERE id = $1::uuid`, rig.invoiceA); err != nil {
		t.Fatalf("re-price for approval: %v", err)
	}
	req.ExpectedCents = 55000
	p, err := rig.svc.Pay(ctx, rig.customerID, req)
	if err != nil {
		t.Fatalf("a retry after a decline must be allowed through: %v", err)
	}
	if p.AmountCents != 55000 {
		t.Fatalf("retry settled %d, want 55000", p.AmountCents)
	}
	if gw.charges != 2 {
		t.Fatalf("gateway dispatches = %d, want 2 (the decline, then the genuine retry)", gw.charges)
	}
	var attempts int
	if err := rig.pool.QueryRow(ctx,
		`SELECT attempts FROM payment_intents WHERE customer_id = $1::uuid`, rig.customerID).Scan(&attempts); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("intent attempts = %d, want 2, a re-claim must be counted, not silently reset", attempts)
	}
}
