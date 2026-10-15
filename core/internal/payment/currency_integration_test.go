//go:build integration

package payment

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Item 3b slice 2 against the real database: the payment_config currency check admits any three
// letter uppercase code, Configure writes the dealer's currency and refuses a different one, and a
// real Store refuses a charge when the row and the dealer disagree.

func TestIntegration_PaymentConfigCurrency_AnyIsoShapeAndNothingElse(t *testing.T) {
	pool := intPool(t)
	cleanConfig(t, pool)
	ctx := context.Background()
	st := intStore(t, pool)

	if err := st.Configure(ctx, "run_payments", "MID-USD", "USD", "", 0, false, map[string]string{"api_key": "k"}, ""); err != nil {
		t.Fatalf("a USD row: %v", err)
	}
	if _, _, _, cur, err := st.Status(ctx); err != nil || cur != "USD" {
		t.Fatalf("Status currency = %q, err %v, want USD", cur, err)
	}
	for _, bad := range []string{"us", "usd", "CADX", "C4D", ""} {
		_, err := pool.Exec(ctx, `
			INSERT INTO payment_config (processor, merchant_id, currency, credentials_ciphertext, key_version, status)
			VALUES ('other_processor', 'MID-X', $1, '\x00', 1, 'disabled')`, bad)
		if err == nil {
			t.Fatalf("currency %q was accepted; the check must hold three uppercase letters", bad)
		}
	}
}

func TestIntegration_Configure_WritesTheDealersCurrencyAndRefusesAnother(t *testing.T) {
	pool := intPool(t)
	cleanConfig(t, pool)
	ctx := context.Background()
	st := intStore(t, pool).WithDealerCurrency(&fakeDealerCurrency{code: "USD"})

	if err := st.Configure(ctx, "run_payments", "MID-1", "", "", 0, false, map[string]string{"api_key": "k"}, ""); err != nil {
		t.Fatalf("blank currency: %v", err)
	}
	if _, _, _, cur, _ := st.Status(ctx); cur != "USD" {
		t.Fatalf("a blank currency stored %q, want the dealer's USD", cur)
	}

	err := st.Configure(ctx, "run_payments", "MID-2", "CAD", "", 0, false, map[string]string{"api_key": "k2"}, "")
	if err == nil || !strings.Contains(err.Error(), "CAD") || !strings.Contains(err.Error(), "USD") {
		t.Fatalf("a CAD row for a USD dealer: err = %v, want a refusal naming both codes", err)
	}
	if _, mid, _, cur, _ := st.Status(ctx); mid != "MID-1" || cur != "USD" {
		t.Fatalf("a refused configure changed the active row: mid=%q currency=%q", mid, cur)
	}
}

func TestIntegration_Charge_RefusesARowThatDisagreesWithTheDealer(t *testing.T) {
	pool := intPool(t)
	cleanConfig(t, pool)
	ctx := context.Background()
	st := intStore(t, pool)
	if err := st.Configure(ctx, "run_payments", "MID-1", "CAD", "", 0, false, map[string]string{"api_key": "k"}, ""); err != nil {
		t.Fatal(err)
	}
	st = st.WithDealerCurrency(&fakeDealerCurrency{code: "USD"})
	svc := NewService(st, RunPaymentsStub{})

	if _, err := svc.Charge(ctx, validInput()); !errors.Is(err, ErrProcessorNotDispatched) {
		t.Fatalf("Charge: err = %v, want a not dispatched refusal", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := svc.ChargeTx(ctx, tx, validInput()); !errors.Is(err, ErrProcessorNotDispatched) {
		t.Fatalf("ChargeTx: err = %v, want a not dispatched refusal", err)
	}
	// The refund of an earlier capture is never trapped by the disagreement.
	if _, err := svc.Refund(ctx, RefundInput{AmountCents: 100, ProcessorRef: "rp_sim_x", IdempotencyKey: "r1"}); err != nil {
		t.Fatalf("Refund: %v", err)
	}
}
