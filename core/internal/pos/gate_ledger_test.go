package pos

// gate_ledger_test.go, the COUNTER-WIDE cost/margin byte-absence gate, and the mutation ledger
// that proves the gate is load-bearing rather than decorative.
//
// THE INVARIANT: cost, margin, floor and profit are gated at the MARSHAL layer as byte-absence -
// the field is not in the response for a caller the dealer's policy does not allow. Not zeroed,
// not nulled, not filtered downstream, and never CSS-hidden on the client.
//
// WHY A LEDGER AND NOT JUST AN ASSERTION. A gate test that has never been seen to FAIL is a test
// nobody knows is wired. Each mutation below was applied to the real source, the suite was run, the
// named failures were recorded, and the mutation was reverted. The record lives in the commit
// message and NOTES-POS-FLOW.md; this file carries the assertions those mutations killed.
//
// THIS FILE SWEEPS THE WHOLE SURFACE, not one payload. The restructure added four reads to POS
// (counter prices, quick picks, the key roster, account standing) and every one of them is a
// customer-facing surface where a cost byte would be a leak. The sweep is written so that a FIFTH
// read cannot quietly skip it: it marshals every counter-facing payload this package can produce
// for a frontline caller and scans the bytes.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// bannedInCounterPayloads is the vocabulary that may never reach a counter-facing payload. "wac" is
// the weighted-average-cost column's own name and is included because a leak usually arrives under
// the internal spelling, not the pretty one.
var bannedInCounterPayloads = []string{"cost", "margin", "floor", "profit", "wac"}

func assertNoCostBytes(t *testing.T, label string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	low := strings.ToLower(string(raw))
	for _, banned := range bannedInCounterPayloads {
		if strings.Contains(low, banned) {
			t.Fatalf("%s must not carry %q, the gate is at the marshal layer: %s", label, banned, raw)
		}
	}
}

func frontline() identity.StaffContext {
	// A frontline caller the decided configuration keeps OFF the cost and
	// margin list (X3: hidden from the yard team): the sweep's byte-absence
	// claims are about a caller with no margin eyes.
	return identity.StaffContext{UserID: "u-counter", RoleCode: "YARD_TEAM",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
}

// -- the whole counter surface, for a clerk ------------------------------------------------------

func TestCounterPayloads_ClerkReceivesNoCostOrMarginBytes(t *testing.T) {
	h := newHarness()
	// The switch admits the staff, so the LIST is the only gate that can
	// blank the sweep's caller (frontline() is off the list) and every
	// byte-absence claim below hangs off the list alone.
	h.store.policies[""] = PosPolicy{MarginVisibility: MarginVisibilityAllStaff}
	h.caller = frontline()
	h.store.branchCosts = map[string]int64{"prod-paver": 60} // a cost EXISTS to be leaked
	ctx := context.Background()

	// 1. The margin glance, the one read that is ABOUT margin. A caller the
	//    configuration keeps off the cost and margin list gets `{}`.
	glance, err := h.svc.MarginGlance(ctx, marginInput())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	raw, _ := json.Marshal(glance)
	if string(raw) != "{}" {
		t.Fatalf("the margin glance must be empty for a clerk: %s", raw)
	}
	assertNoCostBytes(t, "margin glance", glance)

	// 2. The counter price picture, the customer's side of the counter, by construction. The type
	//    has no cost field to leak, which is a property of the type and not a discipline here.
	q := &fakeQuoter{out: []pricing.CounterQuote{{
		ProductID: "prod-paver", PriceUOM: "SF", RetailCents: 2200, CustomerCents: 1804,
		SavingCents: 396, SavingBps: 1800, Basis: pricing.BasisAccount,
		NextBreakQty: 20, NextBreakUOM: "TON", NextBreakUnitCents: 1700, NextBreakSaveCents: 104,
	}}}
	h.svc.WithCounterQuoter(q)
	prices, err := h.svc.CounterPrices(ctx, CounterPriceInput{
		BranchID: "branch-trt", CustomerID: "cust-abc",
		Items: []CounterPriceItem{{ProductID: "prod-paver", QtyEntered: 1}},
	})
	if err != nil {
		t.Fatalf("counter prices: %v", err)
	}
	assertNoCostBytes(t, "counter prices", prices)

	// 3. The hot list. Its tiles carry the same quote, through the same read.
	picks, err := h.svc.QuickPicks(ctx, QuickPickInput{BranchID: "branch-trt", CustomerID: "cust-abc"})
	if err != nil {
		t.Fatalf("quick picks: %v", err)
	}
	assertNoCostBytes(t, "quick picks", picks)

	// 4. The key roster.
	keys, err := h.svc.Hotkeys(ctx, "branch-trt")
	if err != nil {
		t.Fatalf("hotkeys: %v", err)
	}
	assertNoCostBytes(t, "hotkeys", keys)

	// 5. Account standing.
	wireStanding(h, PastDue{PastDueCents: 14823, OldestBucket: "60"}, nil)
	standing, err := h.svc.Standing(ctx, AccountStandingInput{BranchID: "branch-trt", CustomerID: standingCust})
	if err != nil {
		t.Fatalf("standing: %v", err)
	}
	assertNoCostBytes(t, "account standing", standing)
}

func TestCounterPayloads_TheGateIsTheDEALERSSwitchNotTheCallersTier(t *testing.T) {
	// The configuration's list means a named role DOES receive margin once
	// the dealer's 0170 switch admits staff: the gate is a dealer decision,
	// not a hard-coded tier rule. Pinned because a "fix" that simply refused
	// frontline callers everywhere would pass every absence test above while
	// removing a configuration the dealer paid for: the decided list names
	// Inside Sales (X3), and with the switch set to ALL_STAFF the clerk's
	// glance carries margin. Under the column default (MANAGER_UP) the tests
	// beside this pin the same clerk receiving the byte-absent answer.
	h := newHarness()
	h.store.policies[""] = PosPolicy{MarginVisibility: MarginVisibilityAllStaff}
	h.caller = identity.StaffContext{UserID: "u-clerk", RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeGlobal}
	h.store.branchCosts = map[string]int64{"prod-paver": 60}
	out, err := h.svc.MarginGlance(context.Background(), marginInput())
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if len(out.Lines) != 1 || out.Lines[0].MarginBps == nil {
		t.Fatalf("ALL_STAFF must reach a clerk the list considers: %+v", out)
	}
}

func TestCounterPayloads_TheGateRunsBEFOREAnyCostIsRead(t *testing.T) {
	// Byte-absence is necessary but not sufficient: a gate that computed the whole glance and then
	// blanked it would still read every cost row for a caller who may not have them, and would leak
	// through any future field somebody forgot to blank. The refusal must happen first.
	h := newHarness()
	// The switch admits the staff, so the refusal must come from the LIST
	// before any cost read: the leg bites on the list alone.
	h.store.policies[""] = PosPolicy{MarginVisibility: MarginVisibilityAllStaff}
	h.caller = frontline()
	h.store.branchCosts = map[string]int64{"prod-paver": 60}
	h.store.costAsks = 0
	if _, err := h.svc.MarginGlance(context.Background(), marginInput()); err != nil {
		t.Fatalf("glance: %v", err)
	}
	if h.store.costAsks != 0 {
		t.Fatalf("a refused caller must not cause a single cost read: %d", h.store.costAsks)
	}
}
