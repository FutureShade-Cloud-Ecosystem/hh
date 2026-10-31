//go:build integration

// pay_expected_integration_test.go, row 0.50: the sync create door holds a payment to the amount the
// payer was shown, the same rule the portal route has. The door CARRIES expected_cents (it is a
// writable field) and REQUIRES it above zero: a payment create that does not name the amount shown is
// refused in words, with nothing quoted, claimed or charged.
//
// Why carry it instead of refusing every create: the sync engine's money legs (the replay, the
// cross-key convergence, the decline as data, the tenant fence) all run through this door, and a door
// that refused everything would leave them untested. No first party client writes payments through
// sync today (the offline store and the Tauri profile forbid it), so the field costs nothing, and the
// door that exists is held to the same rule as the other.
//
// Run: go test -tags integration -count=1 ./internal/payments/ -run PayExpected
package payments

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func TestPayExpected_TheSyncCreateDoorRequiresTheShownAmount(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	t.Cleanup(func() {
		if _, err := pw.pool.Exec(context.Background(),
			`DELETE FROM portal_payment_methods WHERE customer_id = $1::uuid`, pw.customerID); err != nil {
			t.Errorf("cleanup portal_payment_methods: %v", err)
		}
	})
	m, err := pw.svc.AddMethod(ctx, pw.customerID, AddMethodRequest{Nonce: "nonce-m-" + pw.tag, MakeDefault: true})
	if err != nil {
		t.Fatalf("add a stored method: %v", err)
	}
	day := houseToday()
	inv := pw.discountInvoice(t, pw.customerID, "SYNC", 100000, day)
	store := NewPaymentsSyncStore(pw.pool, pw.svc)
	f := sync.ScopeFilter{TenantID: pw.customerID, Tenant: true, Actor: "pay-expected-" + pw.tag}

	// The reader's probe: the list showed 98000 at 23:50 and the tap lands at 00:02 with no amount.
	pw.setNow(houseInstant(t, dayOf(day, 1), 0, 2))
	create := func(key string, extra map[string]any) error {
		fields := map[string]any{"invoice_ids": []any{inv}, "payment_method_id": m.ID}
		for k, v := range extra {
			fields[k] = v
		}
		_, err := store.Create(ctx, f, key, fields)
		return err
	}
	for name, extra := range map[string]map[string]any{
		"omitted":  nil,
		"zero":     {"expected_cents": float64(0)},
		"negative": {"expected_cents": float64(-5)},
		"text":     {"expected_cents": "98000"},
	} {
		err := create("k-"+name+"-"+pw.tag, extra)
		if !errors.Is(err, sync.ErrOpRefused) || !strings.Contains(err.Error(), "expected_cents") {
			t.Errorf("%s: a create without a positive expected_cents must be refused in words; got %v", name, err)
		}
	}
	if n := len(pw.gw.charged()); n != 0 {
		t.Errorf("nothing is charged on a refused create; the gateway saw %d charges", n)
	}
	if n := pw.intentCount(t); n != 0 {
		t.Errorf("no intent is claimed on a refused create; found %d", n)
	}

	// A stale amount is refused the same way the portal route refuses it.
	err = create("k-stale-"+pw.tag, map[string]any{"expected_cents": float64(98000)})
	if !errors.Is(err, sync.ErrOpRefused) || !errors.Is(err, ErrAmountChanged) {
		t.Errorf("a stale expected_cents must be refused as a changed amount; got %v", err)
	}
	if n := len(pw.gw.charged()); n != 0 {
		t.Errorf("nothing is charged on a stale amount; the gateway saw %d charges", n)
	}

	// The re-quoted amount, confirmed, settles.
	if err := create("k-ok-"+pw.tag, map[string]any{"expected_cents": float64(100000)}); err != nil {
		t.Fatalf("the confirmed amount settles: %v", err)
	}
	if got := pw.lastCharge(t); got != 100000 {
		t.Errorf("the confirmed create charges 100000; got %d", got)
	}
}
