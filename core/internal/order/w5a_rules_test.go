package order

// w5a_rules_test.go, the W5 rules that are PURE functions, tested without a database.
//
// Every one of these is a rule a reader of the code has to trust: which statuses touch stock holds,
// when a change needs a controller's signature, how a line's money splits across trucks, and when a
// multi-ship order may be billed. They are pure functions precisely so they can be pinned here, and
// each case below is one where the WRONG implementation gives a DIFFERENT answer, a fixture that
// passes under both implementations proves nothing.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// - the status → hold-effect map -

// TestReservationEffectFor_EveryOrderStatusHasARuling walks the ENTIRE 0008 status CHECK set. It is
// the guard against the quiet failure mode of a map lookup: a status added to the ladder later
// inherits effectNone and silently stops maintaining holds, with nothing on any screen to say so.
func TestReservationEffectFor_EveryOrderStatusHasARuling(t *testing.T) {
	want := map[string]reservationEffect{
		"DRAFT":               effectNoChange,
		"CONFIRMED":           effectReserve,
		"PICKING":             effectNoChange,
		"READY":               effectNoChange,
		"PARTIALLY_FULFILLED": effectNoChange, // the remainder is still promised, see reservations.go
		"FULFILLED":           effectConsume,
		"INVOICED":            effectConsumeIfShipped, // the gate: only when the goods have left
		"CLOSED":              effectConsume,
		"CANCELLED":           effectRelease,
	}
	for status, exp := range want {
		if got := reservationEffectFor(status); got != exp {
			t.Errorf("reservationEffectFor(%q) = %q, want %q", status, got, exp)
		}
		if !validStatus(status) {
			t.Errorf("%q is in the effect table but not in the 0008 status set", status)
		}
	}
	if got := reservationEffectFor("SOMETHING_NEW"); got != effectNone {
		t.Errorf("an unknown status must grade effectNone (loudly unhandled), got %q", got)
	}
}

// TestTerminalStatusFor_ConsumeAndReleaseAreDistinct pins the reason releases carry a status rather
// than deleting the row: a week later, "the stock shipped" and "the order was cancelled" must be
// distinguishable answers to "why did that hold end".
func TestTerminalStatusFor_ConsumeAndReleaseAreDistinct(t *testing.T) {
	if terminalStatusFor(effectConsume) != ResConsumed {
		t.Errorf("fulfilment must close holds as CONSUMED, got %q", terminalStatusFor(effectConsume))
	}
	if terminalStatusFor(effectRelease) != ResReleased {
		t.Errorf("cancel must close holds as RELEASED, got %q", terminalStatusFor(effectRelease))
	}
	if terminalStatusFor(effectReserve) != "" {
		t.Errorf("a reserve has no terminal status, got %q", terminalStatusFor(effectReserve))
	}
}

// - which lines get a hold -

// fakeReserver records what a confirm asked for, so the SKIP rules can be asserted without a DB.
type fakeReserver struct {
	reserved []StockReserveInput
	applied  bool
	err      error
}

func (f *fakeReserver) ReserveTx(_ context.Context, _ pgx.Tx, in StockReserveInput) (StockReserveResult, error) {
	if f.err != nil {
		return StockReserveResult{}, f.err
	}
	f.reserved = append(f.reserved, in)
	return StockReserveResult{Applied: f.applied, ReservationID: "r1", HoldMode: "HARD"}, nil
}

func (f *fakeReserver) ReleaseBySourceTx(_ context.Context, _ pgx.Tx, _, _, _ string) (int, error) {
	return 0, f.err
}

func (f *fakeReserver) LockHardHoldBalancesTx(_ context.Context, _ pgx.Tx, _, _ string) error {
	return f.err
}

// TestReserveOrderLines_SkipsFreeTextAndZeroQuantityLines. order_lines.product_id is NULLABLE, a
// "delivery surcharge" line is legal and has no stock. Refusing the whole confirm because of one is
// a regression on every order that carries one, so those lines are skipped, not refused.
func TestReserveOrderLines_SkipsFreeTextAndZeroQuantityLines(t *testing.T) {
	f := &fakeReserver{applied: true}
	svc := &Service{stock: f}
	lines := []reservableLine{
		{LineNo: 1, ProductID: "p-1", QtyBase: 10},
		{LineNo: 2, ProductID: "", QtyBase: 1},     // free text, no stock to hold
		{LineNo: 3, ProductID: "p-3", QtyBase: 0},  // zero qty, inventory would reject it
		{LineNo: 4, ProductID: "p-4", QtyBase: -2}, // negative, same
		{LineNo: 5, ProductID: "p-5", QtyBase: 4},
	}
	n, err := svc.reserveOrderLinesTx(context.Background(), nil, "ord-1", "br-1", "actor", lines)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if n != 2 || len(f.reserved) != 2 {
		t.Fatalf("reserved %d line(s) (%d calls), want exactly the 2 stocked positive-qty lines", n, len(f.reserved))
	}
	for _, in := range f.reserved {
		if in.ResType != ResTypeOrder || in.SourceType != ResSourceOrder || in.SourceRefID != "ord-1" {
			t.Errorf("hold keyed wrong: %+v", in)
		}
		if in.EnforceAvailable {
			t.Error("order confirm must NOT enforce availability, a backorder is a commitment to record, " +
				"not a reason to lose the order")
		}
	}
}

// TestReserveOrderLines_PolicyNoneStillConfirms. ReserveResult.Applied == false means the branch
// graded ORDER holds NONE. That is not an error, and an order must still confirm.
func TestReserveOrderLines_PolicyNoneStillConfirms(t *testing.T) {
	f := &fakeReserver{applied: false}
	svc := &Service{stock: f}
	n, err := svc.reserveOrderLinesTx(context.Background(), nil, "ord", "br",
		"actor", []reservableLine{{LineNo: 1, ProductID: "p", QtyBase: 3}})
	if err != nil {
		t.Fatalf("a branch with allocation switched off must still confirm: %v", err)
	}
	if n != 0 {
		t.Fatalf("applied = %d, want 0 (nothing was written)", n)
	}
}

// - the $500 finance gate -

// TestFinanceRequired_GradesTheAbsoluteDelta. Ruling 4: a $600 REMOVAL is exactly as material as a
// $600 addition. An implementation that dropped the abs() would pass every addition case and fail
// only the removals, which is why they are here.
func TestFinanceRequired_GradesTheAbsoluteDelta(t *testing.T) {
	const threshold = int64(50000) // $500.00
	cases := []struct {
		name  string
		delta int64
		want  bool
	}{
		{"a small addition needs no signature", 12345, false},
		{"a small removal needs no signature", -12345, false},
		{"one cent under the threshold", 49999, false},
		{"one cent under, removing", -49999, false},
		{"exactly the threshold IS material", 50000, true},
		{"exactly the threshold, removing, IS material", -50000, true},
		{"one cent over", 50001, true},
		{"a large removal is material", -60000, true},
		{"no delta at all", 0, false},
	}
	for _, c := range cases {
		if got := financeRequired(c.delta, threshold); got != c.want {
			t.Errorf("%s: financeRequired(%d, %d) = %v, want %v", c.name, c.delta, threshold, got, c.want)
		}
	}
	// A zero/negative threshold disables the gate rather than requiring a signature for everything -
	// the row-level CHECK forbids storing one, and this is the belt to that brace.
	if financeRequired(9_999_999, 0) {
		t.Error("a zero threshold must not require a signature (0125 forbids storing one)")
	}
}

// TestNextStatusAfterYardAck_RoutesByMateriality. The ACK is ALWAYS required (ruling 3); what it
// lands on depends on the money.
func TestNextStatusAfterYardAck_RoutesByMateriality(t *testing.T) {
	if got := nextStatusAfterYardAck(true); got != ChangeStatusPendingFinance {
		t.Errorf("a material delta must go to finance after the ACK, got %q", got)
	}
	if got := nextStatusAfterYardAck(false); got != ChangeStatusApproved {
		t.Errorf("an immaterial delta is approved by the ACK alone, got %q", got)
	}
}

// nextActionsFor is the change action list for a manager or a frontline caller who is not the requester,
// with no approvals seam wired.
func nextActionsFor(status string, manager bool) []string {
	caller := staffAt("w5a-user", "w5a-branch")
	if manager {
		caller = managerAt("w5a-user", "w5a-branch")
	}
	return (&Service{}).changeActions(context.Background(), caller, status, "w5a-branch", "someone-else", nil)
}

// TestChangeNextActions_ApplyIsNeverOfferedBeforeApproval is the surface half of the gate: a client
// renders buttons from this list, so it must never offer an apply the server would refuse.
func TestChangeNextActions_ApplyIsNeverOfferedBeforeApproval(t *testing.T) {
	for _, status := range []string{ChangeStatusPendingYard, ChangeStatusPendingFinance} {
		for _, mgr := range []bool{false, true} {
			for _, a := range nextActionsFor(status, mgr) {
				if a == "apply" {
					t.Errorf("status %q (manager=%v) offered apply before approval", status, mgr)
				}
			}
		}
	}
	if !contains(nextActionsFor(ChangeStatusApproved, true), "apply") {
		t.Error("an APPROVED change must offer apply to a manager")
	}
	if contains(nextActionsFor(ChangeStatusApproved, false), "apply") {
		t.Error("apply is manager-gated, it must not be offered to a non-manager")
	}
	if !contains(nextActionsFor(ChangeStatusPendingFinance, true), "finance_approve") {
		t.Error("a manager must be offered the finance signature on a PENDING_FINANCE change")
	}
	if contains(nextActionsFor(ChangeStatusPendingFinance, false), "finance_approve") {
		t.Error("a non-manager must not be offered the finance signature")
	}
	for _, terminal := range []string{ChangeStatusApplied, ChangeStatusRejected, ChangeStatusCancelled} {
		if got := nextActionsFor(terminal, true); len(got) != 0 {
			t.Errorf("terminal status %q offered %v, want nothing", terminal, got)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// - the multi-ship money split -

// TestProrateShares_SharesSumExactlyToTheLineTotal is the property that matters: however a line is
// split across trucks, the shares add back up. An implementation that truncated each share
// independently passes the "two equal halves of an even number" case and fails every one below.
func TestProrateShares_SharesSumExactlyToTheLineTotal(t *testing.T) {
	cases := []struct {
		name  string
		total int64
		qtys  []float64
	}{
		{"three equal shares of an indivisible total", 10000, []float64{1, 1, 1}},
		{"two uneven shares", 999, []float64{1, 2}},
		{"one group takes everything", 12345, []float64{7}},
		{"awkward fractional quantities", 100003, []float64{0.3333, 0.3333, 0.3334}},
		{"many groups", 101, []float64{1, 1, 1, 1, 1, 1, 1}},
		{"a zero total", 0, []float64{2, 3}},
	}
	for _, c := range cases {
		shares := prorateShares(c.total, c.qtys)
		var sum int64
		for _, s := range shares {
			sum += s
		}
		if sum != c.total {
			t.Errorf("%s: shares %v sum to %d, want exactly %d, a cent of drift lands on a customer's invoice",
				c.name, shares, sum, c.total)
		}
		if len(shares) != len(c.qtys) {
			t.Errorf("%s: got %d shares for %d groups", c.name, len(shares), len(c.qtys))
		}
	}
	// No groups at all is not a crash.
	if got := prorateShares(500, nil); len(got) != 0 {
		t.Errorf("prorateShares with no groups = %v, want empty", got)
	}
	// A line with no quantity anywhere cannot be apportioned; every share is zero rather than NaN.
	for _, s := range prorateShares(500, []float64{0, 0}) {
		if s != 0 {
			t.Errorf("zero total quantity must yield zero shares, got %d", s)
		}
	}
}

// - the invoicing ruling -

func grp(status string, cents int64) ShipmentGroup {
	return ShipmentGroup{Status: status, SubtotalCents: cents}
}

// TestComputeReadiness_TheInvoicingRuling pins both halves of the DIB-78 ruling, including the
// asymmetry between them, which is the part a reader most needs stated.
func TestComputeReadiness_TheInvoicingRuling(t *testing.T) {
	t.Run("ON_ALL_COMPLETE bills nothing until every group is complete", func(t *testing.T) {
		r := computeReadiness(InvoiceOnAllComplete,
			[]ShipmentGroup{grp(GroupComplete, 40000), grp(GroupDispatched, 60000)}, true)
		if r.Ready {
			t.Error("a partly-delivered order must not be invoiceable under ON_ALL_COMPLETE")
		}
		if r.InvoiceableCents != 0 {
			t.Errorf("invoiceable = %d, want 0", r.InvoiceableCents)
		}
		if r.Reason == "" {
			t.Error("a refusal must carry a sentence the surface can show")
		}
	})
	t.Run("ON_ALL_COMPLETE bills the whole order when they all are", func(t *testing.T) {
		r := computeReadiness(InvoiceOnAllComplete,
			[]ShipmentGroup{grp(GroupComplete, 40000), grp(GroupComplete, 60000)}, true)
		if !r.Ready || r.InvoiceableCents != 100000 {
			t.Fatalf("ready=%v invoiceable=%d, want true/100000", r.Ready, r.InvoiceableCents)
		}
	})
	t.Run("ON_DEMAND_SUBSET bills the delivered subset", func(t *testing.T) {
		r := computeReadiness(InvoiceOnDemandSubset,
			[]ShipmentGroup{grp(GroupComplete, 40000), grp(GroupDispatched, 60000)}, true)
		if !r.Ready || r.InvoiceableCents != 40000 {
			t.Fatalf("ready=%v invoiceable=%d, want true/40000 (the delivered subset only)",
				r.Ready, r.InvoiceableCents)
		}
	})
	t.Run("ON_DEMAND_SUBSET with nothing delivered bills nothing", func(t *testing.T) {
		r := computeReadiness(InvoiceOnDemandSubset, []ShipmentGroup{grp(GroupPlanned, 40000)}, true)
		if r.Ready || r.InvoiceableCents != 0 {
			t.Fatalf("ready=%v invoiceable=%d, want false/0", r.Ready, r.InvoiceableCents)
		}
	})
	t.Run("a cancelled group is not part of the plan", func(t *testing.T) {
		// Truck 2 was cancelled; truck 1 delivered. Under ON_ALL_COMPLETE that IS every live group,
		// so the order bills. Counting the cancelled group would deadlock the order forever.
		r := computeReadiness(InvoiceOnAllComplete,
			[]ShipmentGroup{grp(GroupComplete, 40000), grp(GroupCancelled, 60000)}, true)
		if !r.Ready {
			t.Fatal("a cancelled group must not hold an otherwise-complete order hostage")
		}
		if r.GroupsTotal != 1 || r.InvoiceableCents != 40000 {
			t.Fatalf("groups=%d invoiceable=%d, want 1/40000", r.GroupsTotal, r.InvoiceableCents)
		}
	})
	t.Run("an order with no groups is not a multi-ship order", func(t *testing.T) {
		r := computeReadiness(InvoiceOnAllComplete, nil, true)
		if r.Ready || r.Reason == "" {
			t.Fatalf("ready=%v reason=%q, a non-multi-ship order must say so, not read as ready",
				r.Ready, r.Reason)
		}
	})
	t.Run("an empty policy defaults to ON_ALL_COMPLETE", func(t *testing.T) {
		if got := computeReadiness("", nil, false).Policy; got != InvoiceOnAllComplete {
			t.Errorf("default policy = %q, want %q", got, InvoiceOnAllComplete)
		}
	})
	t.Run("writer_bound is reported, not assumed", func(t *testing.T) {
		if computeReadiness(InvoiceOnAllComplete, []ShipmentGroup{grp(GroupComplete, 1)}, false).WriterBound {
			t.Error("an unbound invoice writer must be reported as unbound")
		}
	})
}

// TestGroupLegalActions_IsForwardOnly. A group ladder that offered a backward move would let a
// DISPATCHED truck be un-dispatched, which is a lie about a delivery that happened.
func TestGroupLegalActions_IsForwardOnly(t *testing.T) {
	rank := map[string]int{
		GroupPlanned: 1, GroupPicking: 2, GroupReady: 3, GroupDispatched: 4, GroupComplete: 5,
	}
	for from, tos := range groupTransitions {
		for to := range tos {
			if to == GroupCancelled {
				continue // cancel is off the ladder and reachable from any live state
			}
			if rank[to] <= rank[from] {
				t.Errorf("%s → %s is not forward", from, to)
			}
		}
	}
	for _, terminal := range []string{GroupComplete, GroupCancelled} {
		if got := groupLegalActions(terminal); len(got) != 0 {
			t.Errorf("terminal group status %q offers %v, want nothing", terminal, got)
		}
	}
	if !contains(groupLegalActions(GroupReady), GroupDispatched) {
		t.Error("a READY group must be dispatchable")
	}
	if contains(groupLegalActions(GroupPlanned), GroupDispatched) {
		t.Error("a PLANNED group must be picked and readied before it can be dispatched")
	}
}

// TestStatusIsPostPick_TheFallbackWhenTasksIsSilent. The tasks substrate is the authority, but a
// will-call pulled straight off the rack has no task row, so the order's own ladder is the
// fallback. This pins which statuses count.
func TestStatusIsPostPick_TheFallbackWhenTasksIsSilent(t *testing.T) {
	post := map[string]bool{
		"DRAFT": false, "CONFIRMED": false,
		"PICKING": true, "READY": true, "PARTIALLY_FULFILLED": true,
		"FULFILLED": false, "INVOICED": false, "CLOSED": false, "CANCELLED": false,
	}
	for status, want := range post {
		if got := statusIsPostPick(status); got != want {
			t.Errorf("statusIsPostPick(%q) = %v, want %v", status, got, want)
		}
	}
}
