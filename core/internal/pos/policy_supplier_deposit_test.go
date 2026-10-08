package pos

// policy_supplier_deposit_test.go, the dealer setting for a cancelled special
// order's supplier deposit (decision log row 1193): the default disposition a
// cancel applies, CREDIT_BACK unless the dealer says KEPT, carried on
// pos_policy with the other counter rules.

import (
	"context"
	"testing"
)

// An unknown value is a 422 in words before the CHECK constraint could make it
// a 500, and the refused write must not land. An omitted value normalizes to
// the ruling's default, CREDIT_BACK.
func TestUpdatePolicy_SupplierDepositCancelRejectedThenDefaults(t *testing.T) {
	h := newHarness()
	_, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{SupplierDepositCancel: "DONATE_IT"})
	assertStatus(t, err, 422)
	if got := h.store.policies[""].SupplierDepositCancel; got != "" {
		t.Fatalf("a refused policy write reached the store: %q", got)
	}
	p, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{})
	if err != nil {
		t.Fatalf("omitted must normalize: %v", err)
	}
	if p.SupplierDepositCancel != SupplierDepositCancelCreditBack {
		t.Fatalf("an omitted supplier_deposit_cancel must normalize to CREDIT_BACK, got %q", p.SupplierDepositCancel)
	}
}

// KEPT is a value the dealer may set, and the read answers it back.
func TestUpdatePolicy_SupplierDepositCancelKeptRoundTrips(t *testing.T) {
	h := newHarness()
	if _, err := h.svc.UpdatePolicy(context.Background(),
		PosPolicy{SupplierDepositCancel: SupplierDepositCancelKept}); err != nil {
		t.Fatalf("update: %v", err)
	}
	p, err := h.svc.Policy(context.Background(), "")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if p.SupplierDepositCancel != SupplierDepositCancelKept {
		t.Fatalf("the stored supplier_deposit_cancel did not round trip: %q", p.SupplierDepositCancel)
	}
}

// One moved knob, one history row, naming the knob and both values.
func TestUpdatePolicy_SupplierDepositCancelHistoryRow(t *testing.T) {
	h := adminHarness()
	before := seedPolicy(h, "branch-trt")
	next := before
	next.SupplierDepositCancel = SupplierDepositCancelKept
	if _, err := h.svc.UpdatePolicy(context.Background(), next); err != nil {
		t.Fatalf("update: %v", err)
	}
	rows := policyRows(h.rec)
	if len(rows) != 1 {
		t.Fatalf("a one-knob save wrote %d rows, want 1", len(rows))
	}
	if rows[0].Extra["field"] != "supplier_deposit_cancel" ||
		rows[0].Extra["before"] != SupplierDepositCancelCreditBack ||
		rows[0].Extra["after"] != SupplierDepositCancelKept {
		t.Fatalf("the history row is wrong: %+v", rows[0].Extra)
	}
}

// The order module's read of the dealer default: the stored value, and the
// ruling's default when the row has not spoken (an un-migrated or omitted
// column answers CREDIT_BACK, never an empty word).
func TestSupplierDepositCancelDefault_AnswersTheStoredValue(t *testing.T) {
	h := newHarness()
	got, err := h.svc.SupplierDepositCancelDefault(context.Background(), "")
	if err != nil {
		t.Fatalf("default read: %v", err)
	}
	if got != SupplierDepositCancelCreditBack {
		t.Fatalf("an unset supplier_deposit_cancel must answer CREDIT_BACK, got %q", got)
	}
	if _, err := h.svc.UpdatePolicy(context.Background(),
		PosPolicy{SupplierDepositCancel: SupplierDepositCancelKept}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = h.svc.SupplierDepositCancelDefault(context.Background(), "")
	if err != nil {
		t.Fatalf("stored read: %v", err)
	}
	if got != SupplierDepositCancelKept {
		t.Fatalf("a stored KEPT must answer KEPT, got %q", got)
	}
}
