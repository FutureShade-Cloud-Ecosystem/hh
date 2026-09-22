package order

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// consumer_test.go, DIB-191 event-driven drivers (acceptance T1, T2, P5). Drives the LifecycleConsumer
// handlers directly with synthesized outbox events against a real Service + fake store, asserting the
// status advance, the attributed system timeline row (actor "" → renders "system", reason = event id),
// and idempotency on a redelivered event.

const (
	consOrder   = "77777777-7777-4777-8777-777777777777"
	consInvoice = "88888888-8888-4888-8888-888888888888"
	consCust    = "99999999-9999-4999-8999-999999999999"
)

// fakeInvoiceResolver maps one invoice id to one order id (the billing seam stand-in).
type fakeInvoiceResolver struct {
	orderID string
	err     error
	calls   int
}

func (f *fakeInvoiceResolver) OrderIDForInvoice(_ context.Context, _ string) (string, error) {
	f.calls++
	return f.orderID, f.err
}

// capturingRegistrar records the topics a consumer registers as observers.
type capturingRegistrar struct{ topics []string }

func (c *capturingRegistrar) RegisterObserver(topic string, _ notify.Handler) {
	c.topics = append(c.topics, topic)
}

func consumerFixture(t *testing.T, status string, resolver InvoiceOrderResolver) (*LifecycleConsumer, *fakeStore, *recordingRecorder) {
	t.Helper()
	store := newFakeStore(mkDetail(consOrder, consCust, status))
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec)
	return NewLifecycleConsumer(svc, resolver, nil), store, rec
}

func evt(topic, eventID, payload string) notify.Event {
	return notify.Event{EventID: eventID, Topic: topic, Payload: []byte(payload)}
}

// TestLifecycleConsumer_RegistersObservers asserts both topics wire as OBSERVERS (never as primary
// owners, those belong to GL).
func TestLifecycleConsumer_RegistersObservers(t *testing.T) {
	c, _, _ := consumerFixture(t, "CONFIRMED", &fakeInvoiceResolver{})
	reg := &capturingRegistrar{}
	c.RegisterHandlers(reg)
	want := map[string]bool{"order.fulfilled": false, "ar.invoice.finalized": false}
	for _, tp := range reg.topics {
		if _, ok := want[tp]; !ok {
			t.Fatalf("unexpected observer topic %q", tp)
		}
		want[tp] = true
	}
	for tp, seen := range want {
		if !seen {
			t.Fatalf("topic %q was not registered", tp)
		}
	}
}

// TestOnFulfilled_AdvancesToFulfilled covers T1: order.fulfilled → FULFILLED with a system-attributed
// timeline row carrying the source event id as its reason.
func TestOnFulfilled_AdvancesToFulfilled(t *testing.T) {
	c, store, rec := consumerFixture(t, "CONFIRMED", &fakeInvoiceResolver{})
	if err := c.onFulfilled(context.Background(), evt("order.fulfilled", "evt-fulfil-1", `{"order_id":"`+consOrder+`"}`)); err != nil {
		t.Fatalf("onFulfilled: %v", err)
	}
	if got := store.details[consOrder].Status; got != "FULFILLED" {
		t.Fatalf("status = %q, want FULFILLED", got)
	}
	if n := rec.countAction(audit.ActOrderStatusChanged); n != 1 {
		t.Fatalf("timeline rows = %d, want 1", n)
	}
	a := rec.records[0]
	if a.ActorID != "" {
		t.Errorf("actor = %q, want \"\" (system → NULL actor_id → renders 'system')", a.ActorID)
	}
	if a.Extra["old_status"] != "CONFIRMED" || a.Extra["new_status"] != "FULFILLED" {
		t.Errorf("from→to = %v→%v, want CONFIRMED→FULFILLED", a.Extra["old_status"], a.Extra["new_status"])
	}
	if a.Reason != "evt-fulfil-1" {
		t.Errorf("reason = %q, want the source event id evt-fulfil-1", a.Reason)
	}
}

// TestOnInvoiceFinalized_AdvancesToInvoiced covers T2: ar.invoice.finalized (invoice_id only) → resolve
// order → INVOICED, timeline appended.
func TestOnInvoiceFinalized_AdvancesToInvoiced(t *testing.T) {
	resolver := &fakeInvoiceResolver{orderID: consOrder}
	c, store, rec := consumerFixture(t, "FULFILLED", resolver)
	if err := c.onInvoiceFinalized(context.Background(), evt("ar.invoice.finalized", "evt-inv-1", `{"invoice_id":"`+consInvoice+`"}`)); err != nil {
		t.Fatalf("onInvoiceFinalized: %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver called %d times, want 1", resolver.calls)
	}
	if got := store.details[consOrder].Status; got != "INVOICED" {
		t.Fatalf("status = %q, want INVOICED", got)
	}
	if rec.countAction(audit.ActOrderStatusChanged) != 1 {
		t.Fatal("want exactly one INVOICED timeline row")
	}
}

// TestOnFulfilled_Idempotent covers P5: a duplicate/redelivered event finds the order already FULFILLED,
// so the choke-point no-ops (ErrNoTransition, swallowed to nil) and writes NO second timeline row.
func TestOnFulfilled_Idempotent(t *testing.T) {
	c, store, rec := consumerFixture(t, "CONFIRMED", &fakeInvoiceResolver{})
	e := evt("order.fulfilled", "evt-dup", `{"order_id":"`+consOrder+`"}`)
	for i := 0; i < 3; i++ {
		if err := c.onFulfilled(context.Background(), e); err != nil {
			t.Fatalf("onFulfilled #%d: %v", i, err)
		}
	}
	if got := store.details[consOrder].Status; got != "FULFILLED" {
		t.Fatalf("status = %q, want FULFILLED", got)
	}
	if n := rec.countAction(audit.ActOrderStatusChanged); n != 1 {
		t.Fatalf("timeline rows = %d, want exactly 1 across 3 deliveries (idempotent)", n)
	}
}

// - B2 / R1: forward-only event ladder -

// TestEventAllowedFrom pins the derived allowedFrom sets. CANCELLED must never appear (it is unranked
// and terminal) and CLOSED must never appear for either event target (it outranks both), those two
// absences are what block resurrection.
func TestEventAllowedFrom(t *testing.T) {
	cases := map[string][]string{
		"FULFILLED": {"CONFIRMED", "DRAFT", "PARTIALLY_FULFILLED", "PICKING", "READY"},
		"INVOICED":  {"CONFIRMED", "DRAFT", "FULFILLED", "PARTIALLY_FULFILLED", "PICKING", "READY"},
		"DRAFT":     {}, // nothing is below the first rung
	}
	for target, want := range cases {
		got := eventAllowedFrom(target)
		if len(got) != len(want) {
			t.Fatalf("eventAllowedFrom(%s) = %v, want %v", target, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("eventAllowedFrom(%s) = %v, want %v", target, got, want)
			}
		}
		for _, forbidden := range []string{"CANCELLED", "CLOSED"} {
			for _, s := range got {
				if s == forbidden {
					t.Errorf("eventAllowedFrom(%s) contains %q, resurrection is possible", target, forbidden)
				}
			}
		}
	}
	if got := eventAllowedFrom("NOT_A_STATUS"); got != nil {
		t.Errorf("eventAllowedFrom(unknown) = %v, want nil", got)
	}
}

// TestPOSCashSale_TerminalStateIsDeterministic is the R1 headline regression (audit H1).
//
// pos emits order.fulfilled and ar.invoice.finalized in ONE transaction, so both outbox rows carry an
// identical occurred_at and the dispatcher's `ORDER BY occurred_at` claim has no tiebreaker, the drain
// order is arbitrary. This drives BOTH orderings and asserts the same terminal status either way.
//
// Before the ladder this test fails in the invoiced-first ordering: the trailing order.fulfilled found
// INVOICED merely DISTINCT FROM FULFILLED and wrote the order backward to FULFILLED, so the same sale
// settled FULFILLED or INVOICED at random.
func TestPOSCashSale_TerminalStateIsDeterministic(t *testing.T) {
	const sameEventTime = "identical occurred_at, the transaction timestamp"
	fulfilled := evt("order.fulfilled", "evt-pos-fulfil", `{"order_id":"`+consOrder+`"}`)
	invoiced := evt("ar.invoice.finalized", "evt-pos-invoice", `{"invoice_id":"`+consInvoice+`"}`)

	orderings := []struct {
		name  string
		first func(*LifecycleConsumer) error
		then  func(*LifecycleConsumer) error
	}{
		{
			"fulfilled drains first",
			func(c *LifecycleConsumer) error { return c.onFulfilled(context.Background(), fulfilled) },
			func(c *LifecycleConsumer) error { return c.onInvoiceFinalized(context.Background(), invoiced) },
		},
		{
			"invoiced drains first",
			func(c *LifecycleConsumer) error { return c.onInvoiceFinalized(context.Background(), invoiced) },
			func(c *LifecycleConsumer) error { return c.onFulfilled(context.Background(), fulfilled) },
		},
	}

	for _, o := range orderings {
		t.Run(o.name, func(t *testing.T) {
			c, store, rec := consumerFixture(t, "CONFIRMED", &fakeInvoiceResolver{orderID: consOrder})
			if err := o.first(c); err != nil {
				t.Fatalf("first delivery (%s): %v", sameEventTime, err)
			}
			if err := o.then(c); err != nil {
				t.Fatalf("second delivery: %v", err)
			}
			if got := store.details[consOrder].Status; got != "INVOICED" {
				t.Fatalf("terminal status = %q, want INVOICED regardless of drain order "+
					"(a POS cash sale must not settle nondeterministically)", got)
			}
			// Only the moves that actually advanced the order may appear on the timeline; the
			// refused one writes nothing.
			if n := rec.countAction(audit.ActOrderStatusChanged); n < 1 || n > 2 {
				t.Fatalf("timeline rows = %d, want 1 or 2 (never a backward edge)", n)
			}
			for _, a := range rec.records {
				from, _ := a.Extra["old_status"].(string)
				to, _ := a.Extra["new_status"].(string)
				if statusRank[from] >= statusRank[to] {
					t.Errorf("timeline contains a non-forward edge %s→%s", from, to)
				}
			}
		})
	}
}

// TestOnInvoiceFinalized_Idempotent covers the R1-named ar.invoice.finalized redelivery case: the order
// is already INVOICED, so a redelivery no-ops and writes no second row.
func TestOnInvoiceFinalized_Idempotent(t *testing.T) {
	c, store, rec := consumerFixture(t, "FULFILLED", &fakeInvoiceResolver{orderID: consOrder})
	e := evt("ar.invoice.finalized", "evt-inv-dup", `{"invoice_id":"`+consInvoice+`"}`)
	for i := 0; i < 3; i++ {
		if err := c.onInvoiceFinalized(context.Background(), e); err != nil {
			t.Fatalf("delivery #%d: %v", i, err)
		}
	}
	if got := store.details[consOrder].Status; got != "INVOICED" {
		t.Fatalf("status = %q, want INVOICED", got)
	}
	if n := rec.countAction(audit.ActOrderStatusChanged); n != 1 {
		t.Fatalf("timeline rows = %d, want exactly 1 across 3 deliveries", n)
	}
}

// TestEventDrivers_NoTerminalResurrection asserts an event landing on a CANCELLED or CLOSED order is a
// logged no-op: the status is untouched and NO timeline row is written. Before the ladder, both events
// were merely "DISTINCT FROM" the terminal status and would have resurrected the order.
func TestEventDrivers_NoTerminalResurrection(t *testing.T) {
	for _, terminal := range []string{"CANCELLED", "CLOSED"} {
		for _, tc := range []struct {
			topic string
			fire  func(*LifecycleConsumer) error
		}{
			{"order.fulfilled", func(c *LifecycleConsumer) error {
				return c.onFulfilled(context.Background(), evt("order.fulfilled", "e1", `{"order_id":"`+consOrder+`"}`))
			}},
			{"ar.invoice.finalized", func(c *LifecycleConsumer) error {
				return c.onInvoiceFinalized(context.Background(), evt("ar.invoice.finalized", "e2", `{"invoice_id":"`+consInvoice+`"}`))
			}},
		} {
			t.Run(terminal+"/"+tc.topic, func(t *testing.T) {
				c, store, rec := consumerFixture(t, terminal, &fakeInvoiceResolver{orderID: consOrder})
				if err := tc.fire(c); err != nil {
					t.Fatalf("want a benign no-op, got %v", err)
				}
				if got := store.details[consOrder].Status; got != terminal {
					t.Fatalf("status = %q, want %s (a terminal order must never be resurrected)", got, terminal)
				}
				if n := rec.countAction(audit.ActOrderStatusChanged); n != 0 {
					t.Fatalf("timeline rows = %d, want 0", n)
				}
			})
		}
	}
}

// TestEventDrivers_NoBackwardEdge asserts the specific INVOICED→FULFILLED backward edge named in the
// audit is refused.
func TestEventDrivers_NoBackwardEdge(t *testing.T) {
	c, store, rec := consumerFixture(t, "INVOICED", &fakeInvoiceResolver{orderID: consOrder})
	if err := c.onFulfilled(context.Background(), evt("order.fulfilled", "e", `{"order_id":"`+consOrder+`"}`)); err != nil {
		t.Fatalf("want a benign no-op, got %v", err)
	}
	if got := store.details[consOrder].Status; got != "INVOICED" {
		t.Fatalf("status = %q, want INVOICED (INVOICED→FULFILLED is the audit's backward edge)", got)
	}
	if n := rec.countAction(audit.ActOrderStatusChanged); n != 0 {
		t.Fatalf("timeline rows = %d, want 0", n)
	}
}

// TestConsumer_BenignInputs asserts malformed/empty/unknown inputs never error and never write a row:
// a bad payload, an empty id, a missing order, a nil resolver, and an unresolved invoice.
func TestConsumer_BenignInputs(t *testing.T) {
	t.Run("bad fulfilled payload", func(t *testing.T) {
		c, _, rec := consumerFixture(t, "CONFIRMED", &fakeInvoiceResolver{})
		if err := c.onFulfilled(context.Background(), evt("order.fulfilled", "e", `{not json`)); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no row on a bad payload")
		}
	})

	t.Run("empty order id", func(t *testing.T) {
		c, _, rec := consumerFixture(t, "CONFIRMED", &fakeInvoiceResolver{})
		if err := c.onFulfilled(context.Background(), evt("order.fulfilled", "e", `{"order_id":""}`)); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no row on an empty id")
		}
	})

	t.Run("missing order swallowed", func(t *testing.T) {
		c, _, rec := consumerFixture(t, "CONFIRMED", &fakeInvoiceResolver{})
		if err := c.onFulfilled(context.Background(), evt("order.fulfilled", "e", `{"order_id":"12345678-1234-4234-8234-123456789abc"}`)); err != nil {
			t.Fatalf("want nil (ErrNotFound swallowed), got %v", err)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no row when the order is absent")
		}
	})

	t.Run("nil resolver disables invoiced driver", func(t *testing.T) {
		c, _, rec := consumerFixture(t, "FULFILLED", nil)
		if err := c.onInvoiceFinalized(context.Background(), evt("ar.invoice.finalized", "e", `{"invoice_id":"`+consInvoice+`"}`)); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no row when the resolver is unwired")
		}
	})

	t.Run("unresolved invoice", func(t *testing.T) {
		c, _, rec := consumerFixture(t, "FULFILLED", &fakeInvoiceResolver{orderID: ""})
		if err := c.onInvoiceFinalized(context.Background(), evt("ar.invoice.finalized", "e", `{"invoice_id":"`+consInvoice+`"}`)); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no row when the invoice resolves to no order")
		}
	})
}
