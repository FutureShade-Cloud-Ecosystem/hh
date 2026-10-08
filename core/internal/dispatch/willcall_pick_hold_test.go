package dispatch

// willcall_pick_hold_test.go, item 9 review P2-5: the two will call doors'
// pick hold guards bite. Deleting either refuseHeldFromPicking call fails
// these legs; before them, no test in the tree referenced WithPickHoldGate,
// so a deleted guard passed the whole suite.

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// stagedPickState is the yard's answer that lets a mint through requirePicked.
func stagedPickState() *fakePickState {
	return &fakePickState{state: PickCompletion{Exists: true, Staged: true, Status: "STAGED"}}
}

// mintSvc is one WILL_CALL CONFIRMED order with its pick staged, the shape
// that reaches the hold guard.
func mintSvc(t *testing.T, gate PickHoldGate) *Service {
	t.Helper()
	return refSvc(t, willCallOrders(), refCaller(identity.ScopeGlobal)).
		WithPickState(stagedPickState()).
		WithPickHoldGate(gate)
}

// The mint refuses a held order with the mandated words and the owed figure.
func TestMintWillCall_HeldOrderRefusesWithTheWords(t *testing.T) {
	_, err := mintSvc(t, fakePickHoldGate{held: true, owed: 8800}).
		MintWillCall(context.Background(), refOrderID, 0)
	if err == nil {
		t.Fatal("a held order minted a rack tag")
	}
	if !strings.Contains(err.Error(), pickHoldWords) {
		t.Fatalf("the refusal's words: %v", err)
	}
	if !strings.Contains(err.Error(), "is owed") {
		t.Fatalf("the refusal names the money: %v", err)
	}
}

// A hold read that cannot be made refuses the mint (money fails closed).
func TestMintWillCall_UnreadableHoldRefuses(t *testing.T) {
	_, err := mintSvc(t, fakePickHoldGate{err: context.DeadlineExceeded}).
		MintWillCall(context.Background(), refOrderID, 0)
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("an unreadable hold must refuse the mint: %v", err)
	}
}

// An unwired gate allows (the additive lane): the mint walks on to the insert
// and fails THERE, on the nil repository, which is the proof it passed.
func TestMintWillCall_NilGateAllows(t *testing.T) {
	_, err := NewService(nil).
		WithOrders(willCallOrders()).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return refCaller(identity.ScopeGlobal), true }).
		WithPickState(stagedPickState()).
		MintWillCall(context.Background(), refOrderID, 0)
	if err == nil || strings.Contains(err.Error(), pickHoldWords) {
		t.Fatalf("an unwired gate refused the mint: %v", err)
	}
}

// stubConfirmSig captures the attestation a confirm that passes the guard takes.
type stubConfirmSig struct{ ref string }

func (s stubConfirmSig) CaptureTyped(context.Context, TypedAttestation) (string, error) {
	return s.ref, nil
}

func (s stubConfirmSig) CaptureTypedTx(context.Context, pgx.Tx, TypedAttestation) (string, error) {
	return s.ref, nil
}

// livePickup is the token row the confirm acts on.
func livePickup() WillCall {
	return WillCall{ID: "wc-1", OrderID: refOrderID, BranchID: refBranch, Status: "ACTIVE",
		ExpiresAt: time.Now().Add(time.Hour)}
}

func confirmSvc(t *testing.T, gate PickHoldGate) *Service {
	t.Helper()
	return refSvc(t, willCallOrders(), refCaller(identity.ScopeGlobal)).
		WithSignatures(stubConfirmSig{ref: "sig-1"}).
		WithPickHoldGate(gate)
}

// The customer's self confirmation refuses a held order too (a token minted
// before a reopened balance must not stay live for it).
func TestConfirmWillCall_HeldOrderRefusesWithTheWords(t *testing.T) {
	_, err := confirmSvc(t, fakePickHoldGate{held: true, owed: 8800}).
		confirmWillCall(context.Background(), livePickup(), "A Collector", true)
	if err == nil {
		t.Fatal("a held order confirmed its own pickup")
	}
	if !strings.Contains(err.Error(), pickHoldWords) || !strings.Contains(err.Error(), "is owed") {
		t.Fatalf("the refusal's words: %v", err)
	}
}

func TestConfirmWillCall_UnreadableHoldRefuses(t *testing.T) {
	_, err := confirmSvc(t, fakePickHoldGate{err: context.DeadlineExceeded}).
		confirmWillCall(context.Background(), livePickup(), "A Collector", true)
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("an unreadable hold must refuse the confirm: %v", err)
	}
}

// An unwired gate allows: the confirm walks on to the write and fails on the
// nil repository, never on the hold.
func TestConfirmWillCall_NilGateAllows(t *testing.T) {
	_, err := NewService(nil).
		WithOrders(willCallOrders()).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return refCaller(identity.ScopeGlobal), true }).
		WithSignatures(stubConfirmSig{ref: "sig-1"}).
		confirmWillCall(context.Background(), livePickup(), "A Collector", true)
	if err == nil || strings.Contains(err.Error(), pickHoldWords) {
		t.Fatalf("an unwired gate refused the confirm: %v", err)
	}
}
