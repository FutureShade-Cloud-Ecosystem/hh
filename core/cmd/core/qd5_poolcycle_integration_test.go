//go:build integration

// qd5_poolcycle_integration_test.go, the pool cycle the credit gate used to form inside the
// conversion's transaction. A conversion holds the quote row and its own pool connection and then
// consults the credit gate; the gate's hold read asked the POOL for a second connection. At the deploy
// pool of four, with three other callers holding the other three connections while they wait on the
// quote row, no connection ever frees and the process stalls to the timeout, a cycle Postgres cannot
// see. The conversion now stamps its transaction on the context (till.WithCallerTx) before the gate
// call, so the read joins the caller's own connection.
//
// The test is the real service over a pool of exactly four, with the pool size set on the command (the
// pool_max_conns query parameter of DATABASE_URL, which the pool built here keeps):
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable&pool_max_conns=4' \
//	     go test -tags integration ./cmd/core/ -run QD5PoolCycle -count=1 -p 1
package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
)

// heldGate holds the first conversion inside the credit gate call, with the quote row locked and its
// connection taken, until the test has lined the contenders up behind it; then it runs the real gate.
type heldGate struct {
	real     quote.CreditGate
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
	released atomic.Bool
}

func (h *heldGate) Check(ctx context.Context, customerID string, total, open, limit int64, credit bool, branch string, override bool) *identity.ActionBlocked {
	h.once.Do(func() {
		close(h.entered)
		select {
		case <-h.release:
		case <-ctx.Done():
		}
	})
	return h.real.Check(ctx, customerID, total, open, limit, credit, branch, override)
}

// qd5PoolCycle runs the held conversion and its three contenders on a pool of exactly four. held is
// the plain confirm held inside the gate; contenders are started once it is there, each takes a
// connection and waits on the held quote. Every connection of the pool is then in use, and the held
// conversion's gate read must run on the conversion's own transaction. Everyone completes inside the
// bound; the outcome of each caller is returned for the case's own assertions.
func qd5PoolCycle(t *testing.T, creditLimit int64, contenders [3]func(ctx context.Context, e *qd5Env, q qd5Quote) error) (*qd5Env, qd5Quote, [4]error) {
	t.Helper()
	gate := &heldGate{entered: make(chan struct{}), release: make(chan struct{})}
	e := qd5SetupGated(t, 4, true, func(real quote.CreditGate) quote.CreditGate { gate.real = real; return gate })
	if got := e.pool.Config().MaxConns; got != 4 {
		t.Fatalf("the pool must be exactly four connections, got %d", got)
	}
	e.stockFor(qd5Trenton)
	q := e.seedQuote(creditLimit, 0, qd5Trenton)

	const bound = 25 * time.Second
	ctx, cancel := context.WithTimeout(e.counter(), bound)
	defer cancel()
	var errs [4]error
	var wg sync.WaitGroup
	run := func(i int, call func() error) {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = call() }()
	}

	run(0, func() error { _, err := e.svc.ConvertQuote(ctx, q.QuoteID, quote.ConvertOptions{}); return err })
	select {
	case <-gate.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the held conversion never reached the credit gate")
	}
	for i, c := range contenders {
		run(i+1, func() error { return c(ctx, e, q) })
	}

	// Every connection of the pool is now taken: the held conversion's, and one for each contender,
	// which is waiting on the quote row (or the quote's payment lock) the held conversion owns.
	deadline := time.Now().Add(10 * time.Second)
	for e.pool.Stat().AcquiredConns() < 4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := e.pool.Stat().AcquiredConns(); n != 4 {
		t.Fatalf("the four callers must hold all four connections before the release, %d held", n)
	}
	close(gate.release)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	start := time.Now()
	select {
	case <-done:
	case <-time.After(bound + 5*time.Second):
		t.Fatalf("the callers did not finish: the pool cycle stalled them")
	}
	for i, err := range errs {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("caller %d stalled to the bound (%s) on the pool cycle: %v", i, time.Since(start), err)
		}
	}
	t.Logf("completed in %s: held %v | %v | %v | %v", time.Since(start), errs[0], errs[1], errs[2], errs[3])
	return e, q, errs
}

func qd5Confirm(ctx context.Context, e *qd5Env, q qd5Quote) error {
	_, err := e.svc.ConvertQuote(ctx, q.QuoteID, quote.ConvertOptions{})
	return err
}

func qd5CashAll(key string) func(context.Context, *qd5Env, qd5Quote) error {
	return func(ctx context.Context, e *qd5Env, q qd5Quote) error {
		_, err := e.svc.TakeOrderPayment(ctx, q.QuoteID, pay("CASH", q.Total, key))
		return err
	}
}

// TestQD5PoolCycle_AHeldCreditConfirmAndThreeConfirmsCompleteOnAPoolOfFour: a credit customer's confirm
// is held inside the gate (the hold read is made, because the order draws on the account) while three
// more confirms of the quote wait on it. The held one converts; the others are refused as a converted
// quote; one order stands.
func TestQD5PoolCycle_AHeldCreditConfirmAndThreeConfirmsCompleteOnAPoolOfFour(t *testing.T) {
	e, q, errs := qd5PoolCycle(t, 1_000_000, [3]func(context.Context, *qd5Env, qd5Quote) error{qd5Confirm, qd5Confirm, qd5Confirm})
	if errs[0] != nil {
		t.Fatalf("the held conversion: %v", errs[0])
	}
	for i := 1; i < 4; i++ {
		if errs[i] != nil && !errors.Is(errs[i], quote.ErrInvalidTransition) {
			t.Fatalf("contending confirm %d: %v", i, errs[i])
		}
	}
	if c := e.counts(q); c.Orders != 1 {
		t.Fatalf("after the cycle: %+v, want one order", c)
	}
}

// TestQD5PoolCycle_AHeldConfirmThenTheCountersCashPathAndAConfirmCompleteOnAPoolOfFour: a cash customer
// has no credit; its confirm is held inside the gate (the gate reads the hold first, then refuses on
// the limit) while two counter cash actions and a confirm wait on the quote. The held confirm is refused
// for credit; one cash action then converts, invoices and takes the payment in its one transaction, the
// other finds a paid order, and the confirm finds a converted quote.
func TestQD5PoolCycle_AHeldConfirmThenTheCountersCashPathAndAConfirmCompleteOnAPoolOfFour(t *testing.T) {
	e, q, errs := qd5PoolCycle(t, 0, [3]func(context.Context, *qd5Env, qd5Quote) error{qd5CashAll("k-cycle-1"), qd5CashAll("k-cycle-2"), qd5Confirm})
	var blocked quote.ErrCreditBlocked
	if !errors.As(errs[0], &blocked) {
		t.Fatalf("the held confirm of a no-credit customer must be refused for credit: %v", errs[0])
	}
	if errs[1] != nil && errs[2] != nil {
		t.Fatalf("one counter cash action must take the payment: %v / %v", errs[1], errs[2])
	}
	if c := e.counts(q); c != (qd5Counts{Orders: 1, Invoices: 1, Payments: 1}) {
		t.Fatalf("after the cycle: %+v, want one order, one invoice, one payment", c)
	}
}
