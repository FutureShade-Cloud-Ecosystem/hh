package messaging

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

// - HH-424: THE TARGETED PAYMENT NOTICE FOLLOWS THE PAYMENT TRAIL AND THE BRANCH REACH -
//
// Any staffer may switch a payment rule on (the plain staff gate, PUT /payment-rules/{rule}). A notice
// carries a payment's amount, customer and order, which is the payment trail the role gate holds to
// money desk work (ruling 87). The rule is the person's wish; it is never their authority. These tests
// take the trail, or the branch, away from a person who opted in and require silence.

// onlineFacts is a payment nobody took at a counter, taken and owned at Trenton.
func onlineFacts() PaymentFacts {
	f := counterFacts()
	f.TakenAtCounter = false
	return f
}

// MUTATION TARGET: deliver without asking the trail reader and this dies.
func TestPaymentFence_OptedInWithoutTheTrailHearsNothing(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	f.optIn(uOtherPat, RulePaymentOnline)
	c.svc.WithPaymentAccess(&fakeTrail{holds: map[string]bool{"p-" + uOtherPat: true}}) // Avi lost the trail
	f.setPaymentFacts("pay-1", onlineFacts())

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("a staffer without the payment trail must get no payment notice, got %d", n)
	}
	if n := len(noticesFor(f, uOtherPat)); n != 1 {
		t.Fatalf("the staffer who holds the trail still gets exactly one notice, got %d", n)
	}
}

// MUTATION TARGET: treat an unbound reader as "everyone holds it" and this dies.
func TestPaymentFence_NoReaderBoundWithholdsEveryNotice(t *testing.T) {
	c, f := ruleFixture(t)
	c.svc.payAccess = nil
	f.optIn(uClerkAvi, RulePaymentOnline)
	f.setPaymentFacts("pay-1", onlineFacts())

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("with no trail reader bound the notice stays withheld, got %d", n)
	}
}

// A reader that cannot answer is a no for that person, and does not stop the others.
func TestPaymentFence_ReaderErrorWithholdsAndDoesNotCrash(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	c.svc.WithPaymentAccess(&fakeTrail{err: errors.New("roles unreadable")})
	f.setPaymentFacts("pay-1", onlineFacts())

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("an unreadable trail is a no, got %d notices", n)
	}
}

// A staffer with no platform identity cannot be answered for by the gate.
func TestPaymentFence_NoPlatformIdentityHearsNothing(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	f.setRecipient(uClerkAvi, PaymentRecipient{Global: true})
	f.setPaymentFacts("pay-1", onlineFacts())

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("a staffer the gate cannot identify must get nothing, got %d", n)
	}
}

// MUTATION TARGET: skip the branch reach and this dies. Avi holds the trail but only reaches Kingston;
// the payment was taken and is owned at Trenton.
func TestPaymentFence_BranchOutsideReachHearsNothing(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	f.setRecipient(uClerkAvi, PaymentRecipient{PlatformUserID: "p-" + uClerkAvi, OrgIDs: []string{"org-1"}, BranchIDs: []string{fkBrKgn}})
	f.setPaymentFacts("pay-1", onlineFacts())

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("a payment at a branch outside the reach must not be told, got %d", n)
	}
}

func TestPaymentFence_BranchInsideReachStillHears(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	f.setRecipient(uClerkAvi, PaymentRecipient{PlatformUserID: "p-" + uClerkAvi, OrgIDs: []string{"org-1"}, BranchIDs: []string{fkBrTrenton}})
	f.setPaymentFacts("pay-1", onlineFacts())

	payWithID(t, c, "e1", "pay-1", 124000, fkBrTrenton)

	if n := len(noticesFor(f, uClerkAvi)); n != 1 {
		t.Fatalf("a payment inside the reach is told, got %d", n)
	}
}

// The cross branch rule exists for the order's branch: money taken at Kingston, order owned at Trenton.
// A Trenton only staffer reaches the order's branch and must still hear it; a Kingston only staffer
// reaches the till and hears it too; a Toronto only staffer reaches neither.
func TestPaymentFence_CrossBranchReachesEitherEnd(t *testing.T) {
	c, f := ruleFixture(t)
	for _, u := range []string{uRepSam, uClerkAvi, uOtherPat} {
		f.optIn(u, RulePaymentCrossBranch)
	}
	reach := func(u string, branches ...string) {
		f.setRecipient(u, PaymentRecipient{PlatformUserID: "p-" + u, OrgIDs: []string{"org-1"}, BranchIDs: branches})
	}
	reach(uRepSam, fkBrTrenton)    // the order's branch
	reach(uClerkAvi, fkBrKgn)      // the till's branch
	reach(uOtherPat, "br-toronto") // neither
	facts := counterFacts()
	facts.TakenBranchID, facts.TakenBranchName = fkBrKgn, "Kingston"
	f.setPaymentFacts("pay-1", facts)

	payWithID(t, c, "e1", "pay-1", 124000, fkBrKgn)

	if len(noticesFor(f, uRepSam)) != 1 || len(noticesFor(f, uClerkAvi)) != 1 {
		t.Fatalf("both ends of a cross branch payment are told")
	}
	if n := len(noticesFor(f, uOtherPat)); n != 0 {
		t.Fatalf("a staffer who reaches neither branch must not be told, got %d", n)
	}
}

// A payment whose branches are both unknown can only be told to a staffer who reaches every branch.
func TestPaymentFence_BranchlessPaymentIsForGlobalReachOnly(t *testing.T) {
	c, f := ruleFixture(t)
	f.optIn(uClerkAvi, RulePaymentOnline)
	f.optIn(uOtherPat, RulePaymentOnline)
	f.setRecipient(uClerkAvi, PaymentRecipient{PlatformUserID: "p-" + uClerkAvi, OrgIDs: []string{"org-1"}, BranchIDs: []string{fkBrTrenton}})
	facts := onlineFacts()
	facts.TakenBranchID, facts.OrderBranchID = "", ""
	f.setPaymentFacts("pay-1", facts)

	payWithID(t, c, "e1", "pay-1", 124000, "")

	if n := len(noticesFor(f, uClerkAvi)); n != 0 {
		t.Fatalf("a branch scoped staffer must not be told a branchless payment, got %d", n)
	}
	if n := len(noticesFor(f, uOtherPat)); n != 1 {
		t.Fatalf("a global staffer is told, got %d", n)
	}
}

// - THE MONEY ROOMS -
//
// A room carrying a member_roles list (#payments, #payments-<branch>, #finance) is seated by a role
// list that the migration pinned to the trail roles on the day it was written. The room follows the
// reader, not the list: a seat in the room is not enough, the staffer must hold the payment trail now,
// and a branch room must be inside their reach.

const (
	chPayBranch = "ch-payments-trenton-fence"
	chPayGlobal = "ch-payments-global-fence"
)

func moneyRoomFixture(t *testing.T) (*Service, *fakeStore, *fakeTrail) {
	t.Helper()
	f := newFakeStore()
	roles := []string{"PAYABLES_RECEIVABLES", "FINANCIAL_CONTROLLER", "GENERAL_MANAGER"}
	f.facts[chPayBranch] = channelFactsRow{Kind: KindBranch, BranchID: fkBrTrenton, AutoScope: AutoScopeBranch, Visibility: VisibilityPublic, MemberRoles: roles}
	f.facts[chPayGlobal] = channelFactsRow{Kind: KindTeam, AutoScope: AutoScopeGlobal, Visibility: VisibilityPublic, MemberRoles: roles}
	trail := &fakeTrail{holds: map[string]bool{"p-" + uClerkAvi: true}}
	f.setRecipient(uClerkAvi, PaymentRecipient{PlatformUserID: "p-" + uClerkAvi, OrgIDs: []string{"org-1"}, BranchIDs: []string{fkBrTrenton}})
	for _, ch := range []string{chPayBranch, chPayGlobal} {
		f.setMember(ch, uClerkAvi)
		f.setMember(ch, uOtherPat) // a stale seat: seated, but no trail
	}
	f.setRecipient(uOtherPat, PaymentRecipient{PlatformUserID: "p-" + uOtherPat, OrgIDs: []string{"org-1"}, Global: true})
	return svcWith(f).WithPaymentAccess(trail), f, trail
}

// MUTATION TARGET: drop the trail check from the member wall and this dies.
func TestPaymentFence_SeatWithoutTheTrailCannotReadAMoneyRoom(t *testing.T) {
	svc, _, _ := moneyRoomFixture(t)
	for _, ch := range []string{chPayBranch, chPayGlobal} {
		if _, err := svc.GetMessages(context.Background(), Caller{UserID: uClerkAvi}, ch, "", 0); err != nil {
			t.Fatalf("the trail holder reads %s: %v", ch, err)
		}
		if _, err := svc.GetMessages(context.Background(), Caller{UserID: uOtherPat}, ch, "", 0); !errors.Is(err, ErrNotMember) {
			t.Fatalf("a seat without the trail must read nothing in %s, got %v", ch, err)
		}
	}
}

func TestPaymentFence_BranchRoomOutsideReachIsClosed(t *testing.T) {
	svc, f, _ := moneyRoomFixture(t)
	f.setRecipient(uClerkAvi, PaymentRecipient{PlatformUserID: "p-" + uClerkAvi, OrgIDs: []string{"org-1"}, BranchIDs: []string{fkBrKgn}})
	if _, err := svc.GetMessages(context.Background(), Caller{UserID: uClerkAvi}, chPayBranch, "", 0); !errors.Is(err, ErrNotMember) {
		t.Fatalf("a Kingston reach must not read the Trenton payments room, got %v", err)
	}
	if _, err := svc.GetMessages(context.Background(), Caller{UserID: uClerkAvi}, chPayGlobal, "", 0); err != nil {
		t.Fatalf("the branchless payments room is not branch fenced: %v", err)
	}
}

func TestPaymentFence_MoneyRoomWithNoReaderBoundIsClosed(t *testing.T) {
	svc, _, _ := moneyRoomFixture(t)
	svc.payAccess = nil
	if _, err := svc.GetMessages(context.Background(), Caller{UserID: uClerkAvi}, chPayGlobal, "", 0); !errors.Is(err, ErrNotMember) {
		t.Fatalf("an unbound reader closes the money rooms, got %v", err)
	}
}

// An ordinary room carries no member_roles list and is untouched by the fence.
func TestPaymentFence_OrdinaryRoomIsNotFenced(t *testing.T) {
	svc, f, _ := moneyRoomFixture(t)
	f.setMember(chGen, uOtherPat)
	svc.payAccess = nil
	if _, err := svc.GetMessages(context.Background(), Caller{UserID: uOtherPat}, chGen, "", 0); err != nil {
		t.Fatalf("an ordinary room needs no trail: %v", err)
	}
}

// The live stream is a read too: the hub asks the fence after the seat.
func TestPaymentFence_LiveStreamFollowsTheFence(t *testing.T) {
	svc, f, _ := moneyRoomFixture(t)
	hub := &SSEHub{subs: map[string]map[*subscriber]struct{}{}, repo: f, log: slog.New(slog.DiscardHandler)}
	hub.WithRoomFence(svc.RoomAdmits)
	avi, pat := &subscriber{ch: make(chan sseEvent, 4)}, &subscriber{ch: make(chan sseEvent, 4)}
	hub.add(uClerkAvi, avi)
	hub.add(uOtherPat, pat)

	hub.PublishMessage(chPayGlobal, Message{ID: "m1", ChannelID: chPayGlobal, Body: "Payment $1,240.00"})

	if len(avi.ch) == 0 {
		t.Fatalf("the trail holder hears the room live")
	}
	if len(pat.ch) != 0 {
		t.Fatalf("a seat without the trail must not hear the money room live, got %d events", len(pat.ch))
	}
}
