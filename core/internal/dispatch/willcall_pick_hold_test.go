package dispatch

// willcall_pick_hold_test.go, item 9 review P2-5: the two will call doors'
// pick hold guards bite. Deleting either refuseHeldFromPicking call fails
// these legs; before them, no test in the tree referenced WithPickHoldGate,
// so a deleted guard passed the whole suite.
//
// PR 302 round 2 (N1, P2): the staff door's refusal holds the owed figure
// out for callers off the dealer's cost and margin list, the same way the
// payment hold's figure is held out (cost_gate_w3_test.go's doorRoles).
// The public confirm door keeps the figure: the customer holding the
// token is reading the order's own balance, not an internal surface.
// The fence has to ask SeesCostAndMargin at the call site, never at the
// helper, because the public door has no staff stamp to ask about.

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/jackc/pgx/v5"
)

// stagedPickState is the yard's answer that lets a mint through requirePicked.
func stagedPickState() *fakePickState {
	return &fakePickState{state: PickCompletion{Exists: true, Staged: true, Status: "STAGED"}}
}

// pickHoldCostCfg reads the dealer's cost and margin list (ruling 91
// carries it everywhere). The fixture wires it on every staff door test so
// the figure stays on the on list dispatchers (LOGISTICS_MANAGER stamped
// through refCaller) the way the prior PR had it.
func pickHoldCostCfg(t *testing.T) *roleconfig.Config {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// mintSvc is one WILL_CALL CONFIRMED order with its pick staged, the shape
// that reaches the hold guard.
func mintSvc(t *testing.T, gate PickHoldGate) *Service {
	t.Helper()
	return refSvc(t, willCallOrders(), refCaller(identity.ScopeGlobal)).
		WithPickState(stagedPickState()).
		WithCostRoles(pickHoldCostCfg(t)).
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
		WithCostRoles(pickHoldCostCfg(t)).
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

// N1 (P2): the customer's self confirmation path keeps the figure even
// with no staff context (the public door's premise): the token holder sees
// their own balance, not an internal surface, so the bare mandated
// sentence would tell a paying customer nothing they can act on. The
// helper is refuseHeldFromPickingPublic, no moneyAllowed read.
func TestConfirmWillCall_PublicDoorKeepsTheFigureWithNoStaffContext(t *testing.T) {
	svc := NewService(nil).
		WithOrders(willCallOrders()).
		WithSignatures(stubConfirmSig{ref: "sig-1"}).
		WithPickHoldGate(fakePickHoldGate{held: true, owed: 8800})
	// No caller stamp: the public confirm path carries no staff context
	// (willcall.go states so), and the helper's figure decision must NOT
	// read moneyAllowed, which would refuse on the missing stamp.
	_, err := svc.confirmWillCall(context.Background(), livePickup(), "A Collector", true)
	if err == nil {
		t.Fatal("a held order confirmed its own pickup through the public door")
	}
	if !strings.Contains(err.Error(), pickHoldWords) || !strings.Contains(err.Error(), "is owed") {
		t.Errorf("the public door keeps the figure even with no staff context: %v", err)
	}
}

// N1 (P2): the staff door's refusal holds the owed figure out for callers
// off the cost and margin list (drivers, yard team, HR), in cents and in
// words; the on list dispatchers keep the figure. The fence is the dealer's
// configuration, not a hard list here; the leg reads roleconfig.Default()
// the same way doorRoles does, so a dealer change moves every held pick
// refusal in step with every other money read.
func TestMintWillCall_StaffHoldWordsFollowTheCostList(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range doorRolesOffTheList {
		s := mintSvcWithRole(t, role, cfg).WithPickHoldGate(fakePickHoldGate{held: true, owed: 8800})
		_, err := s.MintWillCall(context.Background(), refOrderID, 0)
		if err == nil {
			t.Fatalf("%s minted a rack tag while the order was held", role)
		}
		if !strings.Contains(err.Error(), pickHoldWords) {
			t.Errorf("%s: the bare mandated sentence must stay on the refusal (%v)", role, err)
		}
		for _, leak := range []string{"is owed", "$", "88", "8800"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: the staff door must not name the owed figure to an off list role (%q in %q)", role, leak, err)
			}
		}
	}
	for _, role := range doorRolesOnTheList {
		s := mintSvcWithRole(t, role, cfg).WithPickHoldGate(fakePickHoldGate{held: true, owed: 8800})
		_, err := s.MintWillCall(context.Background(), refOrderID, 0)
		if err == nil {
			t.Fatalf("%s minted a rack tag while the order was held", role)
		}
		if !strings.Contains(err.Error(), "is owed") {
			t.Errorf("%s: the on list caller keeps the figure (%v)", role, err)
		}
	}
}

// mintSvcWithRole wires the pick hold tests' service with a stamped caller
// of the named role, the same way doorCtx does for the moneyAllowed leg.
func mintSvcWithRole(t *testing.T, role string, cfg *roleconfig.Config) *Service {
	t.Helper()
	c := identity.StaffContext{
		UserID: "eeeeeeee-0000-4000-8000-000000000001", RoleCode: role,
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	}
	return refSvc(t, willCallOrders(), c).
		WithPickState(stagedPickState()).
		WithCostRoles(cfg)
}

// N1 (P2): the staff door's refusal fails closed on a caller the rule
// cannot answer (no rule wired, unreadable configuration, no role on the
// list): no figure reaches them, the bare mandated sentence does.
// Mirrors TestDoorMoneyFailsClosed for this door.
func TestMintWillCall_StaffHoldWordsFenceFailsClosed(t *testing.T) {
	none := doorCostRoles(func(string) (bool, error) { return false, nil })
	broken := doorCostRoles(func(string) (bool, error) { return true, context.DeadlineExceeded })
	for name, roles := range map[string]CostRoles{
		"a rule that names nobody": none,
		"an unreadable rule":       broken,
		"no rule wired":            nil,
	} {
		s := mintSvcWithRole(t, "LOGISTICS_MANAGER", nil).WithCostRoles(roles).WithPickHoldGate(fakePickHoldGate{held: true, owed: 8800})
		_, err := s.MintWillCall(context.Background(), refOrderID, 0)
		if err == nil {
			t.Fatalf("%s: a fail closed fence refused the mint; the on list dispatcher still mints", name)
		}
		for _, leak := range []string{"is owed", "$", "88", "8800"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: the fence failure carries no figure (%q in %q)", name, leak, err)
			}
		}
	}
}
