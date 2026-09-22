package settings

// platform_hotkeys_effective_test.go, item 4c: the effective roster, and the distinction the
// collision rule was missing.
//
// THE ONE THING THIS SUITE EXISTS TO PROTECT, and it is a two-sided thing, which is why the two
// halves are asserted against each other rather than separately:
//
//	a REFUSAL that rejected the shipped defaults would be a bug wearing a safety feature's clothes -
//	0191 double-claims F2, F4 and F9 across app.shell and pos.counter ON PURPOSE;
//	a SILENCE where the same surface has two answers for one key is the bug this item was opened to
//	close.
//
// So the first test below feeds the two SHIPPED rosters to the rule and demands a pass, and the
// rest demand a refusal for the cases that are genuinely one keyboard. Getting either without the
// other is the failure worth catching.
//
//	go test ./internal/settings/ -count=1 -run PlatformRosterEffective

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The rosters 0191 actually seeds, spelled in each surface's own grammar. Copied here rather than
// imported because they are MIGRATION data: a test that read them from the same constant the code
// reads would pass no matter what either one became.
var shippedShellRoster = []PlatformHotkey{
	{Action: "sale.cash.new", Binding: "F1", Position: 0},
	{Action: "sales.tab.new", Binding: "F2", Position: 1},
	{Action: "order.quick.open", Binding: "F3", Position: 2},
	{Action: "search.focus", Binding: "F4", Position: 3},
	{Action: "assist.open", Binding: "F9", Position: 4},
}

var shippedCounterRoster = []PlatformHotkey{
	{Action: "step-next", Binding: "f4", Position: 0},
	{Action: "step-back", Binding: "shift-f4", Position: 1},
	{Action: "pay", Binding: "f12", Position: 2},
	{Action: "park", Binding: "f9", Position: 3},
	{Action: "search", Binding: "f2", Position: 4},
}

func TestPlatformRosterEffective_TheShippedCrossSubstrateDoubleClaimIsNotACollision(t *testing.T) {
	// F2, F4 and F9 are bound on BOTH surfaces on the shipped default, to different actions, and
	// that is the design: the till listens in capture and stopPropagations, the shell in bubble.
	shared := ClaimedElsewhere(shippedCounterRoster, shippedShellRoster)
	if len(shared) != 3 {
		t.Fatalf("the shipped rosters share three keys; got %v", shared)
	}
	for _, want := range []string{"f4", "f9", "f2"} {
		if !contains(shared, want) {
			t.Fatalf("expected %s among the shared keys, got %v", want, shared)
		}
	}
	// And the rule must not refuse either of them for it. Each surface is validated against the
	// tier ABOVE it, never against the other substrate.
	if err := ValidatePlatformRosterAgainst(PlatformSurfaceShell, shippedShellRoster, nil); err != nil {
		t.Fatalf("the shipped shell roster must validate: %v", err)
	}
	if err := ValidatePlatformRosterAgainst(PlatformSurfaceCounter, shippedCounterRoster, nil); err != nil {
		t.Fatalf("the shipped counter roster must validate: %v", err)
	}
	// THE STRUCTURAL HALF, and it is the one that will still be true after a refactor: the rule
	// takes ONE surface's keys and THAT surface's dealer tier, and there is no argument through
	// which the other substrate could ever refuse anything. The overlap is reported as DATA
	// (ClaimedElsewhere, above) precisely so that the screen can say the third thing a validator
	// cannot say, "this works, and here is why", instead of a status code saying no.
	//
	// Feeding the shell's roster in as the counter's DEALER tier is deliberately NOT the assertion
	// here: it is a category error that would refuse, and rightly, because that parameter means
	// "the tier above THIS surface". The two substrates stay two by never meeting, not by meeting
	// and being forgiven.
	if err := ValidatePlatformRosterAgainst(PlatformSurfaceCounter, shippedCounterRoster, []PlatformHotkey{}); err != nil {
		t.Fatalf("a cross-substrate double claim must never be refused: %v", err)
	}
}

func TestPlatformRosterEffective_RefusesAKeyTheDealerAlreadyUsesForSomethingElse(t *testing.T) {
	dealer := []PlatformHotkey{{Action: "pay", Binding: "f11"}}
	keys := []PlatformHotkey{
		{Action: "step-next", Binding: "f4"},
		{Action: "park", Binding: "f11"},
	}
	err := ValidatePlatformRosterAgainst(PlatformSurfaceCounter, keys, dealer)
	if err == nil {
		t.Fatal("a platform binding on a key the dealer holds for another action must be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "f11") {
		t.Fatalf("the refusal must name the key; got %q", msg)
	}
	// NAMED BY THE KEY, NEVER BY THE STORED ACTION, the server has no human label for 'pay', and a
	// refusal that printed the id would paint a raw enum on a screen.
	if strings.Contains(msg, "pay") {
		t.Fatalf("the refusal must not print a stored action id; got %q", msg)
	}
	// And it must name where the other key is freed, because that is the part an operator cannot
	// guess from the screen they are standing on.
	if !strings.Contains(msg, "Counter settings") {
		t.Fatalf("the refusal must name the way forward; got %q", msg)
	}
}

func TestPlatformRosterEffective_TheSameKeyForTheSameActionIsAgreement(t *testing.T) {
	// A dealer who bound f4 to step-next and a platform operator who binds f4 to step-next have not
	// collided; they have agreed. Refusing that would make "set the floor to what everyone already
	// uses" impossible, which is the most ordinary edit this screen has.
	dealer := []PlatformHotkey{{Action: "step-next", Binding: "f4"}}
	keys := []PlatformHotkey{{Action: "step-next", Binding: "f4"}}
	if err := ValidatePlatformRosterAgainst(PlatformSurfaceCounter, keys, dealer); err != nil {
		t.Fatalf("agreement is not a collision: %v", err)
	}
}

func TestPlatformRosterEffective_RefusesTheKeysTheShellHardcodes(t *testing.T) {
	// `?` opens the shortcut overlay and Alt+Shift+← walks back a screen. Neither is dealer data at
	// any tier, and the overlay is the only accessible route to the counter's key map.
	for _, spelling := range []string{"?", "Alt+Shift+←", "alt+shift+arrowleft", "shift-alt-arrowleft", "ALT+SHIFT+←"} {
		err := ValidatePlatformRosterAgainst(PlatformSurfaceShell,
			[]PlatformHotkey{{Action: "assist.open", Binding: spelling}}, nil)
		if err == nil {
			t.Fatalf("%q is the shell's own key and must be refused at every spelling", spelling)
		}
		if !strings.Contains(err.Error(), "pick another key") {
			t.Fatalf("the refusal must name the way forward; got %q", err.Error())
		}
	}
	// The counter roster cannot take them either: the overlay is the till's accessible key map too.
	if err := ValidatePlatformRosterAgainst(PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "park", Binding: "?"}}, nil); err == nil {
		t.Fatal("the counter roster must not be able to claim the shell's overlay key")
	}
	// And an ordinary key that merely CONTAINS a reserved base is untouched.
	if err := ValidatePlatformRosterAgainst(PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "park", Binding: "shift-f9"}}, nil); err != nil {
		t.Fatalf("an ordinary chord must still validate: %v", err)
	}
}

func TestPlatformRosterEffective_ReservedKeysAreServedAndCopied(t *testing.T) {
	got := ReservedPlatformKeys()
	if len(got) != 2 {
		t.Fatalf("expected the two keys the shell hardcodes; got %d", len(got))
	}
	if got[0].Binding != "?" || got[0].Holds == "" {
		t.Fatalf("the overlay key must be served with what it does; got %+v", got[0])
	}
	// A copy, not the package's own slice: a caller that mutated the answer would silently unbind
	// the shell's own key for the rest of the process.
	got[0].Binding = "mutated"
	if ReservedPlatformKeys()[0].Binding != "?" {
		t.Fatal("ReservedPlatformKeys must hand out a copy")
	}
}

func TestPlatformRosterEffective_ValidatePlatformRosterStillMeansOneRoster(t *testing.T) {
	// The old validator keeps its exact meaning: dashboard's shell-shape gate and pos's action-set
	// gate both call it with no dealer tier in sight, and a widened signature would have made both
	// of them ask a question they do not mean.
	if err := ValidatePlatformRoster(PlatformSurfaceCounter, shippedCounterRoster); err != nil {
		t.Fatalf("the one-roster validator must still pass the shipped roster: %v", err)
	}
	dup := []PlatformHotkey{{Action: "pay", Binding: "f4"}, {Action: "park", Binding: "f4"}}
	if err := ValidatePlatformRoster(PlatformSurfaceCounter, dup); err == nil {
		t.Fatal("the same-roster duplicate refusal is unchanged")
	}
	// And the cross-tier rule is the same refusal for that degenerate case, not a second one.
	if err := ValidatePlatformRosterAgainst(PlatformSurfaceCounter, dup, nil); err == nil {
		t.Fatal("the widened rule must still refuse a same-roster duplicate")
	}
}

// - the dealer tier -

func TestDealerRosterFor_DecodesTheRosterID(t *testing.T) {
	store := newPlatformStore()
	store.roleItems = []Item{
		{Kind: KindAction, ID: "pay.f11", Position: 0, Visible: true},
		{Kind: KindAction, ID: "search.shift-f2", Position: 1, Visible: true},
		{Kind: KindAction, ID: "park.f9", Position: 2, Visible: false},         // hidden is not claimed
		{Kind: KindAction, ID: "not-an-action.f7", Position: 3, Visible: true}, // unrenderable
		{Kind: KindAction, ID: "nodot", Position: 4, Visible: true},
		{Kind: KindPanel, ID: "hotlist", Position: 5, Visible: true}, // a different kind entirely
	}
	svc := platformSvc(store, superAdmin())
	got, err := svc.DealerRosterFor(context.Background(), PlatformSurfaceCounter)
	if err != nil {
		t.Fatalf("DealerRosterFor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected the two bindable rows; got %+v", got)
	}
	if got[0].Action != "pay" || got[0].Binding != "f11" {
		t.Fatalf("the action is everything before the LAST dot; got %+v", got[0])
	}
	if got[1].Action != "search" || got[1].Binding != "shift-f2" {
		t.Fatalf("a chord key keeps its dashes; got %+v", got[1])
	}
}

func TestDealerRosterFor_TheShellTierIsEmptyByDecision(t *testing.T) {
	store := newPlatformStore()
	store.roleItems = []Item{{Kind: KindAction, ID: "pay.f11", Visible: true}}
	svc := platformSvc(store, superAdmin())
	got, err := svc.DealerRosterFor(context.Background(), PlatformSurfaceShell)
	if err != nil {
		t.Fatalf("DealerRosterFor: %v", err)
	}
	// dealer_hotkeys belongs to internal/dashboard and has no editor to adapt; the counter's rows
	// must not leak across as if they were the shell's.
	if len(got) != 0 {
		t.Fatalf("the shell's dealer tier is deliberately empty; got %+v", got)
	}
	if _, err := svc.DealerRosterFor(context.Background(), "yard.console"); err == nil {
		t.Fatal("an unknown surface is still refused in words")
	}
}

// oneBadProfileStore fails exactly one profile's read. It exists because the degrade posture here is
// per-profile CONTINUE rather than return: an unreadable job shape must not take the other four
// with it, because a partial answer can only ever under-report a clash.
type oneBadProfileStore struct {
	fakeKitStore
	bad string
}

func (s *oneBadProfileStore) ReadRoleItems(ctx context.Context, surfaceID, profile string) ([]Item, error) {
	if profile == s.bad {
		return nil, errors.New("relation surface_role_items does not exist")
	}
	return s.fakeKitStore.ReadRoleItems(ctx, surfaceID, profile)
}

func TestDealerRosterFor_AnUnreadableProfileDoesNotTakeTheOthersWithIt(t *testing.T) {
	store := &oneBadProfileStore{bad: "counter"}
	store.roleItems = []Item{{Kind: KindAction, ID: "pay.f11", Visible: true}}
	svc := platformSvc(store, superAdmin())
	got, err := svc.DealerRosterFor(context.Background(), PlatformSurfaceCounter)
	if err != nil {
		t.Fatalf("an unreadable tier degrades downward, it never errors: %v", err)
	}
	if len(got) != 1 || got[0].Binding != "f11" {
		t.Fatalf("the readable profiles must still answer; got %+v", got)
	}
}

// - the service -

func TestPlatformRosterEffective_PutRefusesAClashWithTheDealersCounterKeys(t *testing.T) {
	store := newPlatformStore()
	store.roleItems = []Item{{Kind: KindAction, ID: "pay.f11", Visible: true}}
	svc := platformSvc(store, superAdmin())
	err := svc.PutPlatformHotkeys(context.Background(), PlatformSurfaceCounter,
		[]PlatformHotkey{{Action: "park", Binding: "f11"}})
	if err == nil {
		t.Fatal("the write must be refused before it reaches the store")
	}
	if len(store.wrote) != 0 {
		t.Fatalf("nothing may be written on a refusal; wrote %+v", store.wrote)
	}
	if !strings.Contains(err.Error(), "f11") {
		t.Fatalf("the refusal must name the key; got %q", err.Error())
	}
	// The SHELL roster is untouched by the counter's dealer tier, the two substrates stay two even
	// here, where a lazy implementation would have pooled the keys.
	if err := svc.PutPlatformHotkeys(context.Background(), PlatformSurfaceShell,
		[]PlatformHotkey{{Action: "assist.open", Binding: "F11"}}); err != nil {
		t.Fatalf("the counter's dealer keys must not constrain the shell roster: %v", err)
	}
}

func TestPlatformRosterEffective_TheViewCarriesTheDealerTierAndTheReservedKeys(t *testing.T) {
	store := newPlatformStore()
	store.rosters[PlatformSurfaceCounter] = shippedCounterRoster
	store.roleItems = []Item{{Kind: KindAction, ID: "pay.f11", Visible: true}}
	svc := platformSvc(store, superAdmin())
	view, err := svc.PlatformHotkeys(context.Background())
	if err != nil {
		t.Fatalf("PlatformHotkeys: %v", err)
	}
	// Asserted against BYTES, like the rest of this package's gates: a field that is set in Go and
	// never marshalled is a refusal the browser cannot make.
	body := marshalForGate(t, view)
	if !strings.Contains(body, `"reserved":[`) || !strings.Contains(body, `"binding":"?"`) {
		t.Fatalf("the reserved keys must ship to the screen: %s", body)
	}
	if !strings.Contains(body, `"dealer":[`) {
		t.Fatalf("the dealer tier must ship beside the roster it constrains: %s", body)
	}
	var counter, shell PlatformRoster
	for _, r := range view.Rosters {
		if r.Surface == PlatformSurfaceCounter {
			counter = r
		}
		if r.Surface == PlatformSurfaceShell {
			shell = r
		}
	}
	if len(counter.Dealer) != 1 || counter.Dealer[0].Binding != "f11" {
		t.Fatalf("the counter's dealer tier must be resolved; got %+v", counter.Dealer)
	}
	if len(shell.Dealer) != 0 {
		t.Fatalf("the shell's dealer tier is empty by decision; got %+v", shell.Dealer)
	}
	// EMPTY, NEVER NULL, a screen that has to tell `null` from `[]` before it can draw a list is a
	// screen with a branch nobody remembers to write.
	if !strings.Contains(body, `"dealer":[]`) {
		t.Fatalf("an absent dealer tier must marshal as an empty list: %s", body)
	}
}

func TestPlatformRosterEffective_ClaimedElsewhereIsCaseBlindAcrossTheTwoGrammars(t *testing.T) {
	// The shell spells it "F2" and the counter spells it "f2". They are one physical key, and a
	// warning that missed it because of a capital letter would be a warning that never fires.
	got := ClaimedElsewhere(
		[]PlatformHotkey{{Action: "search", Binding: "f2"}, {Action: "pay", Binding: "f12"}},
		[]PlatformHotkey{{Action: "sales.tab.new", Binding: "F2"}},
	)
	if len(got) != 1 || got[0] != "f2" {
		t.Fatalf("expected the shared key in the asking roster's own spelling; got %v", got)
	}
	if len(ClaimedElsewhere(nil, nil)) != 0 {
		t.Fatal("ClaimedElsewhere must answer an empty list, never nil")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
