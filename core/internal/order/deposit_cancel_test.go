package order

// deposit_cancel_test.go, the cancelled special order's supplier deposit (row
// 1193): an order whose only money is a supplier deposit document may be
// cancelled, the cancel applies the dealer's default disposition, and a
// manager may keep the deposit with a reason. Built on the transition_test.go
// fixtures; the deposit settlement is a fake that records the call.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// fakeDepositCanceller records the settlement the cancel path asked for. The
// zero value settles nothing and reports nothing settled, the seam's own
// no-op posture for an order with no deposit documents.
type fakeDepositCanceller struct {
	calls        []depositCancelCall
	err          error
	settledCount int
}

type depositCancelCall struct {
	orderID     string
	disposition string
	reason      string
}

func (f *fakeDepositCanceller) CancelOrderDeposits(_ context.Context, _ pgx.Tx, orderID, disposition, reason string) (int, error) {
	f.calls = append(f.calls, depositCancelCall{orderID: orderID, disposition: disposition, reason: reason})
	if f.err != nil {
		return 0, f.err
	}
	return f.settledCount, nil
}

// fakeDepositDefault answers the dealer's default disposition (nil seam is the
// scaffold posture: the ruling's default).
type fakeDepositDefault struct {
	ans string
	err error
}

func (f fakeDepositDefault) SupplierDepositCancelDefault(context.Context, string) (string, error) {
	return f.ans, f.err
}

// depositCancelFixture is txnFixtureWithGuards with the deposit seams wired:
// permissive money guards (no other money attached), a recording settler and
// a dealer default.
func depositCancelFixture(t *testing.T, status string, settler *fakeDepositCanceller, def fakeDepositDefault) (*Service, *fakeStore, *fakeDepositCanceller) {
	t.Helper()
	svc, _, store := txnFixtureWithGuards(t, status, &fakeCancelGuards{})
	settler = &fakeDepositCanceller{}
	svc.WithDepositCanceller(settler).WithDepositCancelDefault(def)
	return svc, store, settler
}

// The ruling's headline: an order whose only money is a supplier deposit is
// cancellable (the money predicate no longer blocks it), and the cancel
// applies the dealer's default, crediting the deposit back.
func TestCancelOrder_DepositOnlyOrderCancelsApplyingTheDealerDefault(t *testing.T) {
	svc, _, settler := depositCancelFixture(t, "CONFIRMED", nil, fakeDepositDefault{ans: "CREDIT_BACK"})
	ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	got, err := svc.CancelOrderManual(ctx, txnOrder, "customer walked away", false)
	if err != nil {
		t.Fatalf("a deposit-only order must be cancellable: %v", err)
	}
	if got != "CANCELLED" {
		t.Fatalf("new status = %q, want CANCELLED", got)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("the cancel settled %d times, want the one call", len(settler.calls))
	}
	c := settler.calls[0]
	if c.disposition != "CREDIT_BACK" {
		t.Fatalf("the cancel applied %q, want the dealer default CREDIT_BACK", c.disposition)
	}
	if c.reason != "customer walked away" {
		t.Fatalf("the settlement carries the reason %q", c.reason)
	}
}

// A dealer whose default is KEPT gets KEPT without any override.
func TestCancelOrder_DealerDefaultKeptFlowsWithoutAnOverride(t *testing.T) {
	svc, _, settler := depositCancelFixture(t, "CONFIRMED", nil, fakeDepositDefault{ans: "KEPT"})
	ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	if _, err := svc.CancelOrderManual(ctx, txnOrder, "special order dropped", false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if settler.calls[0].disposition != "KEPT" {
		t.Fatalf("the cancel applied %q, want the dealer default KEPT", settler.calls[0].disposition)
	}
}

// The manager's override keeps the deposit, and the reason travels with it.
func TestCancelOrder_ManagerKeepOverride(t *testing.T) {
	svc, _, settler := depositCancelFixture(t, "CONFIRMED", nil, fakeDepositDefault{ans: "CREDIT_BACK"})
	ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	if _, err := svc.CancelOrderManual(ctx, txnOrder, "the supplier already ordered it", true); err != nil {
		t.Fatalf("cancel with the keep override: %v", err)
	}
	if settler.calls[0].disposition != "KEPT" {
		t.Fatalf("the override applied %q, want KEPT", settler.calls[0].disposition)
	}
}

// A non manager cannot override: the cancel action is manager-gated, so the
// override rides a refusal that names no settlement.
func TestCancelOrder_NonManagerCannotOverride(t *testing.T) {
	svc, _, settler := depositCancelFixture(t, "CONFIRMED", nil, fakeDepositDefault{ans: "CREDIT_BACK"})
	ctx := ctxWith(context.Background(), staffAt(txnUser, histBranchA)) // frontline, not a manager
	_, err := svc.CancelOrderManual(ctx, txnOrder, "i should not be able to do this", true)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("a non manager's cancel with the override must be refused, got %v", err)
	}
	if len(settler.calls) != 0 {
		t.Fatalf("the refused override settled %d times", len(settler.calls))
	}
}

// The override's reason is required: the cancel reason is mandatory already,
// and a blank one refuses before any settlement runs.
func TestCancelOrder_OverrideReasonRequired(t *testing.T) {
	svc, _, settler := depositCancelFixture(t, "CONFIRMED", nil, fakeDepositDefault{ans: "CREDIT_BACK"})
	ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	_, err := svc.CancelOrderManual(ctx, txnOrder, "   ", true)
	if !isBadRequest(err) {
		t.Fatalf("a blank reason must be a 400, got %v", err)
	}
	if len(settler.calls) != 0 {
		t.Fatalf("a refused cancel settled %d times", len(settler.calls))
	}
}

// Every other money block still holds: a finalized invoice that is not a
// deposit document, or a committed counter sale, refuses the cancel even
// with the settlement seam wired.
func TestCancelOrder_OtherMoneyStillBlocks(t *testing.T) {
	cases := []struct {
		name   string
		guards *fakeCancelGuards
	}{
		{"a finalized invoice that is not a deposit document", &fakeCancelGuards{hasInvoice: true}},
		{"a committed counter sale", &fakeCancelGuards{hasSale: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore(mkDetail(txnOrder, txnCust, "CONFIRMED"))
			store.branches = map[string]string{txnOrder: histBranchA}
			settler := &fakeDepositCanceller{}
			svc := NewService(store).withCaller(testCallerFrom).
				WithCancelGuards(tc.guards, tc.guards).
				WithDepositCanceller(settler).
				WithDepositCancelDefault(fakeDepositDefault{ans: "CREDIT_BACK"})
			ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
			for _, keep := range []bool{false, true} {
				_, err := svc.CancelOrderManual(ctx, txnOrder, "nope", keep)
				if !errors.Is(err, ErrCancelBlocked) {
					t.Fatalf("the %s must still block the cancel, got %v", tc.name, err)
				}
			}
			if len(settler.calls) != 0 {
				t.Fatalf("a blocked cancel settled %d times", len(settler.calls))
			}
		})
	}
}

// A second cancel writes nothing: the order sits at CANCELLED, the ladder
// refuses the move and the settlement is never asked again.
func TestCancelOrder_SecondCancelWritesNothing(t *testing.T) {
	svc, _, settler := depositCancelFixture(t, "CONFIRMED", nil, fakeDepositDefault{ans: "CREDIT_BACK"})
	ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	if _, err := svc.CancelOrderManual(ctx, txnOrder, "once", false); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	_, err := svc.CancelOrderManual(ctx, txnOrder, "again", false)
	// The guarded write answers "already there" (ErrNoTransition) before the
	// ladder is consulted; either way the second cancel is a refusal that
	// reaches no settlement.
	if !errors.Is(err, ErrIllegalTransition) && !errors.Is(err, ErrNoTransition) {
		t.Fatalf("the second cancel must be refused, got %v", err)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("the second cancel reached the settlement %d times", len(settler.calls)-1)
	}
}

// A settlement that refuses fails the cancel: the money must not be orphaned
// by a status flip the settlement could not keep up with.
func TestCancelOrder_SettlementRefusalFailsTheCancel(t *testing.T) {
	store := newFakeStore(mkDetail(txnOrder, txnCust, "CONFIRMED"))
	store.branches = map[string]string{txnOrder: histBranchA}
	settler := &fakeDepositCanceller{err: errors.New("the document is locked")}
	svc := NewService(store).withCaller(testCallerFrom).
		WithCancelGuards(&fakeCancelGuards{}, &fakeCancelGuards{}).
		WithDepositCanceller(settler).
		WithDepositCancelDefault(fakeDepositDefault{ans: "CREDIT_BACK"})
	ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	_, err := svc.CancelOrderManual(ctx, txnOrder, "settle me", false)
	if err == nil || errors.Is(err, ErrNoTransition) {
		t.Fatalf("a failed settlement must fail the cancel, got %v", err)
	}
	if store.details[txnOrder].Order.Status != "CONFIRMED" {
		t.Fatalf("the order flipped to %q despite the failed settlement", store.details[txnOrder].Order.Status)
	}
}

// An unreadable dealer default refuses the cancel: the disposition is money,
// a guess is not the dealer's answer.
func TestCancelOrder_UnreadableDealerDefaultRefuses(t *testing.T) {
	store := newFakeStore(mkDetail(txnOrder, txnCust, "CONFIRMED"))
	store.branches = map[string]string{txnOrder: histBranchA}
	settler := &fakeDepositCanceller{}
	svc := NewService(store).withCaller(testCallerFrom).
		WithCancelGuards(&fakeCancelGuards{}, &fakeCancelGuards{}).
		WithDepositCanceller(settler).
		WithDepositCancelDefault(fakeDepositDefault{err: errors.New("policy store down")})
	ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	if _, err := svc.CancelOrderManual(ctx, txnOrder, "nope", false); err == nil {
		t.Fatal("an unreadable dealer default must refuse the cancel")
	}
	if len(settler.calls) != 0 {
		t.Fatalf("a refused default settled %d times", len(settler.calls))
	}
}

// The offer side follows: a deposit-only order offers CANCELLED in
// legal_actions (the predicate no longer reads it as blocked money).
func TestOrderRead_DepositOnlyOrderOffersCancel(t *testing.T) {
	store := newFakeStore(mkDetail(txnOrder, txnCust, "CONFIRMED"))
	store.branches = map[string]string{txnOrder: histBranchA}
	guards := &fakeCancelGuards{} // the billing half: no NON-deposit finalized invoice
	svc := NewService(store).withCaller(testCallerFrom).WithCancelGuards(guards, guards)
	ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	view, err := svc.OrderRead(ctx, txnOrder)
	if err != nil {
		t.Fatalf("order read: %v", err)
	}
	sawCancel := false
	for _, a := range view.LegalActions {
		if a == "CANCELLED" {
			sawCancel = true
		}
	}
	if !sawCancel {
		t.Fatalf("a deposit-only order must offer CANCELLED: %v", view.LegalActions)
	}
}

var _ = identity.StaffContext{} // keeps the identity import at hand for fixture variants
