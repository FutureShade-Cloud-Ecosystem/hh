package assist

// registry_test.go, the authority gate, which is the only part of this feature that is a security
// control rather than a convenience.
//
// MUTATION TARGETS. Each of these dies on a specific, plausible edit rather than on "something
// changed", that is what makes them evidence instead of decoration:
//
//	delete the `if c.Tier > tier` check in visible()        -> TestRegistry_RefusesCommandAboveCallerTier
//	                                                           TestRegistry_ListHidesWhatTheCallerCannotRun
//	make effectiveTier return the LOOSER of the two         -> TestRegistry_PolicyNarrowsButNeverWidens
//	let a tier-0 caller through                             -> TestRegistry_UnassignedRoleIsRefused
//	answer an unknown code by running something             -> TestRegistry_UnknownCommandIsRefusedByName
//	drop the required-argument loop in Run()                -> TestRegistry_MissingRequiredArgumentIs400
//	pass a fabricated Caller into the reader                -> TestRegistry_CommandsRunAsTheAskingUser

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

func TestRegistry_UnknownCommandIsRefusedByName(t *testing.T) {
	f := seededReaders()
	r := NewRegistry(f.deps(), nil)

	_, err := r.Run(context.Background(), leadership(), "payment.delete", Args{})
	wantErrIs(t, err, ErrUnknownCommand)

	// And nothing was reached on the way to finding that out.
	if f.calls != 0 {
		t.Fatalf("an unknown command reached a read seam %d times", f.calls)
	}
}

func TestRegistry_RefusesCommandAboveCallerTier(t *testing.T) {
	f := seededReaders()
	r := NewRegistry(f.deps(), nil)

	// payment.lookup is tier 2 (money). A tier-3 counter clerk may not run it.
	_, err := r.Run(context.Background(), frontline(), "payment.lookup", Args{"reference": "PMT-1042"})
	wantErrIs(t, err, ErrRefused)
	if f.calls != 0 {
		t.Fatalf("a refused command still reached the payment reader (%d calls), the gate is after the read", f.calls)
	}

	// The same command, the same arguments, a manager: allowed.
	if _, err := r.Run(context.Background(), manager(), "payment.lookup", Args{"reference": "PMT-1042"}); err != nil {
		t.Fatalf("manager refused payment.lookup: %v", err)
	}
}

func TestRegistry_UnassignedRoleIsRefused(t *testing.T) {
	f := seededReaders()
	r := NewRegistry(f.deps(), nil)

	// Tier 0 is "no role assigned". It is NOT the same as frontline: an unknown authority must not
	// resolve to the least-privileged one, because nobody decided that.
	for _, code := range []string{"order.lookup", "stock.lookup", "customer.activity"} {
		if _, err := r.Run(context.Background(), unassigned(), code, Args{"order_no": "SO-4", "product": "TB-BLU60", "customer": "Northgate"}); !errors.Is(err, ErrRefused) {
			t.Fatalf("%s allowed a caller with no role: %v", code, err)
		}
	}
	if got := r.List(context.Background(), unassigned()); len(got) != 0 {
		t.Fatalf("a caller with no role was offered %d commands", len(got))
	}
}

func TestRegistry_ListHidesWhatTheCallerCannotRun(t *testing.T) {
	r := NewRegistry(seededReaders().deps(), nil)

	names := func(c Caller) map[string]bool {
		out := map[string]bool{}
		for _, cmd := range r.List(context.Background(), c) {
			out[cmd.Code] = true
		}
		return out
	}

	front := names(frontline())
	if front["payment.lookup"] || front["customer.balance"] {
		t.Fatalf("the counter menu offered a money command: %v", front)
	}
	if !front["order.lookup"] || !front["stock.lookup"] || !front["customer.activity"] {
		t.Fatalf("the counter menu is missing an operational command: %v", front)
	}

	lead := names(leadership())
	if len(lead) != len(BuiltinCommands()) {
		t.Fatalf("leadership sees %d commands, want all %d: %v", len(lead), len(BuiltinCommands()), lead)
	}
	// DIB-478 grew the registry. The assertion that matters is not the NUMBER, that changes every
	// time a domain is covered, it is that leadership sees EVERY command and the counter sees only
	// the ones its tier allows, which is what the two halves above pin.
	for _, code := range []string{"ar.aging", "ar.holds", "payments.today", "payments.range"} {
		if front[code] {
			t.Fatalf("the counter menu offered the money command %q: %v", code, front)
		}
		if !lead[code] {
			t.Fatalf("leadership is missing %q: %v", code, lead)
		}
	}
}

func TestRegistry_PolicyNarrowsButNeverWidens(t *testing.T) {
	f := seededReaders()

	// An administrator (or an attacker with write access to the config table) tries to hand
	// payment.lookup, a tier-2 command, to the whole front line.
	widening := StaticPolicy{"payment.lookup": {Enabled: true, MaxTier: 3}}
	r := NewRegistry(f.deps(), widening)
	if _, err := r.Run(context.Background(), frontline(), "payment.lookup", Args{"reference": "X"}); !errors.Is(err, ErrRefused) {
		t.Fatalf("a config row WIDENED a command's authority: %v", err)
	}

	// The same table narrowing a command: order.lookup, normally open to everyone, restricted to
	// leadership. That direction is honoured.
	narrowing := StaticPolicy{"order.lookup": {Enabled: true, MaxTier: 1}}
	r = NewRegistry(f.deps(), narrowing)
	if _, err := r.Run(context.Background(), frontline(), "order.lookup", Args{"order_no": "SO-4"}); !errors.Is(err, ErrRefused) {
		t.Fatalf("a config row failed to NARROW a command: %v", err)
	}
	if _, err := r.Run(context.Background(), leadership(), "order.lookup", Args{"order_no": "SO-4"}); err != nil {
		t.Fatalf("leadership refused a command narrowed to leadership: %v", err)
	}
}

func TestRegistry_DisabledCommandIsOffForEveryone(t *testing.T) {
	f := seededReaders()
	r := NewRegistry(f.deps(), StaticPolicy{"stock.lookup": {Enabled: false, MaxTier: 3}})

	// Including the platform owner. "Switched off" that an admin can still use is not switched off.
	for _, c := range []Caller{frontline(), manager(), leadership()} {
		if _, err := r.Run(context.Background(), c, "stock.lookup", Args{"product": "TB-BLU60"}); !errors.Is(err, ErrDisabled) {
			t.Fatalf("%s ran a disabled command: %v", c.RoleCode, err)
		}
	}
	if f.calls != 0 {
		t.Fatalf("a disabled command reached the stock reader %d times", f.calls)
	}
}

func TestRegistry_UnreadablePolicyFallsBackToCodeFloors(t *testing.T) {
	// A policy store that fails must not open anything up. The registry degrades to the CODE
	// floors, which are the strict ones, half B keeps working, and nobody gains a permission out
	// of an outage.
	r := NewRegistry(seededReaders().deps(), failingPolicy{})

	if _, err := r.Run(context.Background(), frontline(), "payment.lookup", Args{"reference": "X"}); !errors.Is(err, ErrRefused) {
		t.Fatalf("an unreadable policy table widened payment.lookup: %v", err)
	}
	if _, err := r.Run(context.Background(), frontline(), "order.lookup", Args{"order_no": "SO-4"}); err != nil {
		t.Fatalf("an unreadable policy table broke an open command: %v", err)
	}
}

type failingPolicy struct{}

func (failingPolicy) CommandPolicy(context.Context) (map[string]Policy, error) {
	return nil, errors.New("policy table unreachable")
}

func TestRegistry_CommandsRunAsTheAskingUser(t *testing.T) {
	// THE INVARIANT, ASSERTED FROM OUTSIDE. Whatever the read seam receives must be the caller that
	// was handed to Run, same user, same role, same branch grants. If anything in the path ever
	// starts constructing its own Caller (a service account, an "internal" context, a widened
	// scope), this test sees it.
	f := seededReaders()
	r := NewRegistry(f.deps(), nil)
	who := manager()

	mustRun(t, r, who, "payment.lookup", Args{"reference": "PMT-1042"})

	got := f.gotCaller
	if got.UserID != who.UserID || got.RoleCode != who.RoleCode || got.Tier != who.Tier {
		t.Fatalf("the read seam ran as %+v, not as the asking user %+v", got, who)
	}
	if got.Global != who.Global {
		t.Fatalf("branch scope changed on the way to the read: got Global=%v want %v", got.Global, who.Global)
	}
	if len(got.BranchIDs) != len(who.BranchIDs) || (len(got.BranchIDs) > 0 && got.BranchIDs[0] != who.BranchIDs[0]) {
		t.Fatalf("branch grants changed on the way to the read: %v vs %v", got.BranchIDs, who.BranchIDs)
	}
}

func TestRegistry_MissingRequiredArgumentIs400(t *testing.T) {
	f := seededReaders()
	r := NewRegistry(f.deps(), nil)

	_, err := r.Run(context.Background(), frontline(), "order.lookup", Args{})
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("a missing required argument gave %T (%v), want a BadRequestError", err, err)
	}
	// The message has to name what is missing AND show the shape. "invalid input" is how a user
	// ends up filing a ticket for their own typo (DIB-443).
	if !contains(bad.Msg, "order number") || !contains(bad.Msg, "SO-4") {
		t.Fatalf("the message doesn't say what to type: %q", bad.Msg)
	}
	if f.calls != 0 {
		t.Fatalf("an invalid invocation reached the order reader")
	}
}

func TestRegistry_UnwiredSeamIsUnavailableNotAPanic(t *testing.T) {
	// A deployment mid-rollout, with the assist package wired but one adapter not yet attached.
	r := NewRegistry(Deps{}, nil)
	_, err := r.Run(context.Background(), leadership(), "stock.lookup", Args{"product": "TB-BLU60"})
	var u UnavailableError
	if !errors.As(err, &u) {
		t.Fatalf("an unwired seam gave %T (%v), want UnavailableError", err, err)
	}
}

func TestRegistry_CodeFloorsAreTheOnesTheMigrationSeeded(t *testing.T) {
	// Migration 0153 seeds each command at the same authority its code declares. If the two ever
	// drift, a reader of the migration is being told something false about the system, so the
	// seeded values are pinned here, next to the code they mirror.
	seeded := map[string]int{
		"payment.lookup":    2,
		"order.lookup":      3,
		"stock.lookup":      3,
		"customer.balance":  2,
		"customer.activity": 3,
	}
	// DIB-478's commands have NO row in 0153, and that is correct rather than an omission: a command
	// with no policy row runs at its CODE FLOOR (registry.go, policyFor), and the code floor is the
	// strict one. An unwritten row can never widen anything. What must not happen is a floor drifting
	// unnoticed, so every command's floor is pinned here whether or not a migration mentions it.
	floors := map[string]int{
		"payments.today":    2, // money
		"payments.range":    2, // money
		"ar.aging":          2, // money
		"ar.holds":          2, // money, and a credit decision about a named account
		"customer.invoices": 2, // money
		"product.price":     2, // what a specific customer pays is commercially sensitive
		"customer.orders":   3,
		"customer.quotes":   3,
		"quote.holds":       3,
		"stock.incoming":    3,
		"product.find":      3,
		// DIB-494. The same floor as order.lookup and the staff-gated order history/edits routes:
		// the trail is a counter-level read, and the branch fence inside the read does the rest.
		"order.activity": 3,
	}
	for k, v := range seeded {
		floors[k] = v
	}
	for _, cmd := range BuiltinCommands() {
		w, ok := floors[cmd.Code]
		if !ok {
			t.Fatalf("command %q has no pinned floor, add it to this test (and to migration 0153 if it needs a policy row)", cmd.Code)
		}
		if cmd.MaxTier != w {
			t.Fatalf("%s: code floor is tier %d, pinned at %d", cmd.Code, cmd.MaxTier, w)
		}
	}
	if len(BuiltinCommands()) != len(floors) {
		t.Fatalf("the registry has %d commands, the pinned set has %d", len(BuiltinCommands()), len(floors))
	}
	// The five 0153 rows still mirror their code floors exactly. That is the claim the migration
	// makes to whoever reads it, and it stays pinned on its own.
	for _, cmd := range coreCommands() {
		if cmd.MaxTier != seeded[cmd.Code] {
			t.Fatalf("%s: code floor is tier %d, migration 0153 seeds %d", cmd.Code, cmd.MaxTier, seeded[cmd.Code])
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
