package payments

// dib510_receipt_test.go, the counter-receipt path's decisions, without a database.
//
// The double-post guard itself is a DATABASE guarantee (a partial unique index and an ON CONFLICT),
// so it is proved in dib510_receipt_integration_test.go against the real schema. What is provable
// here is the refusals that must happen BEFORE any of that: an unkeyed receipt, an invented tender,
// and an empty application.

import (
	"context"
	"strings"
	"testing"
)

func TestCounterTenders_AreTheFourNamedWaysMoneyArrives(t *testing.T) {
	want := map[string]bool{"EFT": true, "CHEQUE": true, "CARD": true, "CASH": true}
	if len(CounterTenders) != len(want) {
		t.Fatalf("expected %d tenders, got %v", len(want), CounterTenders)
	}
	for _, m := range CounterTenders {
		if !want[m] {
			t.Fatalf("unexpected tender %q", m)
		}
	}
	// OTHER is in the 0010 CHECK and deliberately NOT offered here: an unnamed tender on an AR
	// receipt is a reconciliation problem somebody inherits.
	if validTender("OTHER") {
		t.Fatal("OTHER must not be an offered counter tender")
	}
	// And "charge account" is not a way to pay an invoice that charging the account created.
	if validTender("ACCOUNT") || validTender("CHARGE_ACCOUNT") {
		t.Fatal("charging the account is what created the invoice; it cannot settle it")
	}
}

func TestRecordReceipt_RefusesBeforeItLocksAnything(t *testing.T) {
	// A nil-pool repository: every refusal below must happen before any query is attempted, so
	// reaching the database would surface as errNoDB rather than the sentence we expect.
	r := NewRepository(nil)
	ctx := context.Background()

	if _, err := r.RecordReceipt(ctx, ReceiptRequest{Method: "CHEQUE"}); err == nil {
		t.Fatal("a receipt with no idempotency key must be refused, the guard cannot be optional")
	} else if !strings.Contains(err.Error(), "idempotency key") {
		t.Fatalf("the refusal must name what is missing, got %v", err)
	}

	_, err := r.RecordReceipt(ctx, ReceiptRequest{IdempotencyKey: "k", Method: "BITCOIN"})
	if err == nil || !strings.Contains(err.Error(), "EFT") {
		t.Fatalf("an invented tender must be refused and the refusal must list what IS accepted, got %v", err)
	}

	_, err = r.RecordReceipt(ctx, ReceiptRequest{IdempotencyKey: "k", Method: "CHEQUE"})
	if err == nil || !strings.Contains(err.Error(), "at least one invoice") {
		t.Fatalf("a payment applied to nothing must be refused, got %v", err)
	}
}
