package landedcost

// Unit tests for the pure landed-cost math + the inventory-seam dispatch (fake Capitalizer). These carry the
// A16 ACs that don't need a DB: allocation exactness across the 3 bases, USD→CAD isolation, exclude-special-
// buys, the retro capitalize/sold-through split, and the void-nets-to-zero invariant.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// statusOf extracts the HTTP status a landedcost error maps to (clientError carries it directly).
func statusOf(err error) int {
	var ce clientError
	if errors.As(err, &ce) {
		return ce.status
	}
	return 0
}

// ctxCaller stamps a StaffContext into the context the way the identity guard does at runtime, so the service's
// caller() resolves it. It returns a service whose callerFrom reads that stamped context.
func scopedSvc(sc identity.StaffContext) (*Service, context.Context) {
	svc := NewService(NewRepository(nil)) // nil pool: the tenant gates below all short-circuit before any repo read
	ctx := identity.ContextWithStaff(context.Background(), sc)
	return svc, ctx
}

// fakeCapitalizer is the DB-free stand-in for the inventory cost seam (INV-CORE lands at integration). It
// records the NET cad capitalized per product so a test can assert the amounts flow into inventory. Products
// listed in onHandZero model the B2 "no on-hand to capitalize into" path, the seam absorbs nothing and the
// service must expense that share to COGS instead of stranding it in inventory.
type fakeCapitalizer struct {
	lines      []ReceivedLine
	caps       map[string]int64
	onHandZero map[string]bool
}

func (f *fakeCapitalizer) ReceivedLinesForPO(_ context.Context, _ string) ([]ReceivedLine, error) {
	return f.lines, nil
}

func (f *fakeCapitalizer) CapitalizeLandedCostTx(_ context.Context, _ pgx.Tx, productID, _ string, cadCents int64, _ time.Time, _ string) (int64, error) {
	if f.onHandZero[productID] {
		return 0, nil // nothing on hand → absorbed 0; caller expenses the share to COGS
	}
	if f.caps == nil {
		f.caps = map[string]int64{}
	}
	f.caps[productID] += cadCents
	return cadCents, nil
}

// CapitalizeLandedCostSnapTx mirrors CapitalizeLandedCostTx and returns a fixed pre-Apply snapshot (100) with a cost
// row present, so the landedcost service persists a snapshot the Void path can restore.
func (f *fakeCapitalizer) CapitalizeLandedCostSnapTx(_ context.Context, _ pgx.Tx, productID, _ string, cadCents int64, _ time.Time, _ string) (int64, int64, bool, error) {
	if f.onHandZero[productID] {
		return 0, 0, false, nil // nothing on hand → absorbed 0, no cost row to snapshot
	}
	if f.caps == nil {
		f.caps = map[string]int64{}
	}
	f.caps[productID] += cadCents
	return cadCents, 100, true, nil
}

// RestoreLandedCostTx models the exact-restore Void: it removes EXACTLY `absorbed` from the recorded cap and returns the
// negative realized delta the Void journal books as CR 1200. Draining exactly the capitalized amount (never a delta over
// current on-hand) is the general inverse that ties the reversal for nothing/receipt/sale between Apply and Void.
func (f *fakeCapitalizer) RestoreLandedCostTx(_ context.Context, _ pgx.Tx, productID, _ string, _, absorbed int64, _ time.Time, _ string) (int64, error) {
	if f.caps == nil {
		f.caps = map[string]int64{}
	}
	reversed := absorbed
	if reversed > f.caps[productID] {
		reversed = f.caps[productID] // can't drain more value than is on the books
	}
	f.caps[productID] -= reversed
	return -reversed, nil
}

func sumAlloc(a []Allocation) int64 {
	var t int64
	for _, x := range a {
		t += x.AllocatedCadCents
	}
	return t
}

// AC2, USD isolation: the manual fx rate converts USD document cents to CAD cents; a CAD doc is a passthrough.
func TestConvertToCAD(t *testing.T) {
	cases := []struct {
		cents int64
		fx    float64
		want  int64
	}{
		{10000, 1.0, 10000},   // CAD passthrough
		{10000, 1.35, 13500},  // USD → CAD
		{10000, 1.355, 13550}, // rounds to the nearest cent
		{333, 1.37, 456},      // 456.21 → 456
		{0, 1.35, 0},
	}
	for _, c := range cases {
		if got := convertToCAD(c.cents, c.fx); got != c.want {
			t.Errorf("convertToCAD(%d, %v) = %d, want %d", c.cents, c.fx, got, c.want)
		}
	}
	if got := convertToCAD(100, 0); got != 100 {
		t.Errorf("non-positive fx must be treated as 1.0, got %d", got)
	}
}

// AC1, 3-basis allocation exactness: every basis apportions the exact total with no penny lost/invented.
func TestAllocate_ThreeBasisExact(t *testing.T) {
	lines := []ReceivedLine{
		{POLineID: "l1", ProductID: "p1", QtyBase: 1, ValueCents: 300, WeightKg: 2},
		{POLineID: "l2", ProductID: "p2", QtyBase: 3, ValueCents: 700, WeightKg: 3},
	}
	total := int64(1000)

	value := allocate(total, BasisValue, lines, true)
	if value[0].AllocatedCadCents != 300 || value[1].AllocatedCadCents != 700 {
		t.Errorf("VALUE basis: got %d/%d, want 300/700", value[0].AllocatedCadCents, value[1].AllocatedCadCents)
	}

	quantity := allocate(total, BasisQuantity, lines, true)
	if quantity[0].AllocatedCadCents != 250 || quantity[1].AllocatedCadCents != 750 {
		t.Errorf("QUANTITY basis: got %d/%d, want 250/750", quantity[0].AllocatedCadCents, quantity[1].AllocatedCadCents)
	}

	weight := allocate(total, BasisWeight, lines, true)
	if weight[0].AllocatedCadCents != 400 || weight[1].AllocatedCadCents != 600 {
		t.Errorf("WEIGHT basis: got %d/%d, want 400/600", weight[0].AllocatedCadCents, weight[1].AllocatedCadCents)
	}

	for _, name := range []string{BasisValue, BasisQuantity, BasisWeight} {
		if s := sumAlloc(allocate(total, name, lines, true)); s != total {
			t.Errorf("%s basis: Σ shares = %d, want exactly %d", name, s, total)
		}
	}
}

// AC1, largest-remainder handles indivisible totals exactly (lowest-index buckets absorb the pennies).
func TestAllocate_IndivisibleExact(t *testing.T) {
	lines := []ReceivedLine{
		{ProductID: "a", ValueCents: 1},
		{ProductID: "b", ValueCents: 1},
		{ProductID: "c", ValueCents: 1},
	}
	got := allocate(1000, BasisValue, lines, true)
	if got[0].AllocatedCadCents != 334 || got[1].AllocatedCadCents != 333 || got[2].AllocatedCadCents != 333 {
		t.Errorf("got %d/%d/%d, want 334/333/333", got[0].AllocatedCadCents, got[1].AllocatedCadCents, got[2].AllocatedCadCents)
	}
	if s := sumAlloc(got); s != 1000 {
		t.Errorf("Σ = %d, want 1000", s)
	}
}

// AC3, exclude special buys: special-buy lines absorb NO landed cost and drop out of the allocation entirely.
func TestAllocate_ExcludeSpecialBuys(t *testing.T) {
	lines := []ReceivedLine{
		{ProductID: "p1", ValueCents: 100},
		{ProductID: "special", ValueCents: 100, IsSpecialBuy: true},
		{ProductID: "p2", ValueCents: 100},
	}
	excluded := allocate(900, BasisValue, lines, true)
	if len(excluded) != 2 {
		t.Fatalf("exclude=true: got %d allocations, want 2 (special dropped)", len(excluded))
	}
	for _, a := range excluded {
		if a.ProductID == "special" {
			t.Errorf("special buy must not receive an allocation")
		}
		if a.AllocatedCadCents != 450 {
			t.Errorf("included line got %d, want 450", a.AllocatedCadCents)
		}
	}
	if s := sumAlloc(excluded); s != 900 {
		t.Errorf("Σ = %d, want 900 (whole cost on the two non-special lines)", s)
	}

	// With exclude=false the special buy DOES absorb its share.
	included := allocate(900, BasisValue, lines, false)
	if len(included) != 3 {
		t.Fatalf("exclude=false: got %d allocations, want 3", len(included))
	}
	if s := sumAlloc(included); s != 900 {
		t.Errorf("Σ = %d, want 900", s)
	}
}

// AC4, retro capitalize/sold-through split: the on-hand ratio splits a share; the two parts sum exactly.
func TestSplitSoldThrough(t *testing.T) {
	cases := []struct {
		allocated            int64
		onHand, received     float64
		wantOnHand, wantSold int64
	}{
		{1000, 3, 4, 750, 250}, // 75% still on hand
		{1000, 0, 4, 0, 1000},  // fully sold through → all to COGS
		{1000, 4, 4, 1000, 0},  // fully on hand → all capitalized
		{1000, 9, 4, 1000, 0},  // on-hand clamps to received
		{1000, 1, 3, 333, 667}, // remainder lands on the sold portion
		{1000, 2, 0, 1000, 0},  // unknown received → capitalize the whole amount
	}
	for _, c := range cases {
		oh, sold := splitSoldThrough(c.allocated, c.onHand, c.received)
		if oh != c.wantOnHand || sold != c.wantSold {
			t.Errorf("splitSoldThrough(%d, %v, %v) = (%d,%d), want (%d,%d)",
				c.allocated, c.onHand, c.received, oh, sold, c.wantOnHand, c.wantSold)
		}
		if oh+sold != c.allocated {
			t.Errorf("split parts %d+%d != allocated %d", oh, sold, c.allocated)
		}
	}
}

// AC6, landed cost flows into inventory via the (faked) cost seam: allocate then dispatch, assert per-product.
func TestCapitalizeDispatch_FakeSeam(t *testing.T) {
	fake := &fakeCapitalizer{lines: []ReceivedLine{
		{ProductID: "p1", ValueCents: 300},
		{ProductID: "p2", ValueCents: 700},
	}}
	allocs := allocate(1000, BasisValue, fake.lines, true)
	for _, a := range allocs {
		if _, err := fake.CapitalizeLandedCostTx(context.Background(), nil, a.ProductID, "branch", a.AllocatedCadCents, time.Time{}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if fake.caps["p1"] != 300 || fake.caps["p2"] != 700 {
		t.Errorf("capitalized p1=%d p2=%d, want 300/700", fake.caps["p1"], fake.caps["p2"])
	}
}

// AC5, void nets to zero: capitalizing the shares then their negation leaves the inventory bump at zero.
func TestVoidNetsToZero_FakeSeam(t *testing.T) {
	fake := &fakeCapitalizer{lines: []ReceivedLine{
		{ProductID: "p1", ValueCents: 333},
		{ProductID: "p2", ValueCents: 667},
	}}
	allocs := allocate(1000, BasisValue, fake.lines, true)
	for _, a := range allocs { // apply
		_, _ = fake.CapitalizeLandedCostTx(context.Background(), nil, a.ProductID, "b", a.AllocatedCadCents, time.Time{}, "")
	}
	for _, a := range allocs { // void → negate
		_, _ = fake.CapitalizeLandedCostTx(context.Background(), nil, a.ProductID, "b", -a.AllocatedCadCents, time.Time{}, "")
	}
	for pid, net := range fake.caps {
		if net != 0 {
			t.Errorf("product %s net capitalized = %d, want 0 (void must net to zero)", pid, net)
		}
	}
}

// The preview surfaces every line (special buys flagged with a zero share) so the allocate screen can explain
// why an excluded line gets nothing.
func TestPreviewLines_FlagsSpecialBuys(t *testing.T) {
	lines := []ReceivedLine{
		{ProductID: "p1", ValueCents: 100},
		{ProductID: "special", ValueCents: 100, IsSpecialBuy: true},
	}
	prev := previewLines(1000, BasisValue, lines, true)
	if len(prev) != 2 {
		t.Fatalf("got %d preview lines, want 2", len(prev))
	}
	for _, p := range prev {
		if p.ProductID == "special" {
			if !p.IsSpecialBuy || p.AllocatedCadCents != 0 {
				t.Errorf("special buy preview = %+v, want flagged with 0 share", p)
			}
		} else if p.AllocatedCadCents != 1000 {
			t.Errorf("included preview share = %d, want 1000", p.AllocatedCadCents)
		}
	}
}

// TestTenantScope_ListGating is the L8 MAJOR regression: the landed-cost surface must branch-scope the caller,
// mirroring the till/order house pattern. Route-level staff gating proves the caller is staff but NOT that they
// may reach a given branch, so the service itself enforces CanAccessBranch. This exercises the DB-free gates on
// List: (a) no caller → 401; (b) a scoped caller naming a branch they lack → 403; (c) a scoped caller trying to
// enumerate ALL branches (empty branch_id) → 403; (d) a scoped caller on THEIR branch, and a GLOBAL caller,
// pass the gate (they reach the repo, which, with a nil pool here, answers 503, proving they cleared authz).
// It FAILS without the caller/CanAccessBranch gating (List would return the repo error, never 401/403).
func TestTenantScope_ListGating(t *testing.T) {
	scoped := identity.StaffContext{UserID: "u-scoped", Scope: identity.ScopeBranch, Tier: identity.TierManager,
		Branches: map[string]bool{"branch-A": true}}
	global := identity.StaffContext{UserID: "u-global", Scope: identity.ScopeGlobal, Tier: identity.TierLeadership}

	// (a) No caller in context → 401.
	svc := NewService(NewRepository(nil))
	if _, err := svc.List(context.Background(), "branch-A", ""); statusOf(err) != http.StatusUnauthorized {
		t.Errorf("no-caller List: status=%d, want 401", statusOf(err))
	}

	// (b) Scoped caller, branch they cannot access → 403.
	svcB, ctxB := scopedSvc(scoped)
	if _, err := svcB.List(ctxB, "branch-B", ""); statusOf(err) != http.StatusForbidden {
		t.Errorf("scoped caller other-branch List: status=%d, want 403", statusOf(err))
	}

	// (c) Scoped caller enumerating ALL branches (empty branch_id) → 403 (no cross-branch leak).
	if _, err := svcB.List(ctxB, "", ""); statusOf(err) != http.StatusForbidden {
		t.Errorf("scoped caller all-branches List: status=%d, want 403", statusOf(err))
	}

	// (d) Scoped caller on their OWN branch clears authz (reaches repo → 503 with nil pool, not 401/403).
	if _, err := svcB.List(ctxB, "branch-A", ""); statusOf(err) == http.StatusForbidden || statusOf(err) == http.StatusUnauthorized {
		t.Errorf("scoped caller own-branch List should clear authz, got status=%d", statusOf(err))
	}
	// GLOBAL caller reaches any/all branches (also clears authz).
	svcG, ctxG := scopedSvc(global)
	if _, err := svcG.List(ctxG, "", ""); statusOf(err) == http.StatusForbidden || statusOf(err) == http.StatusUnauthorized {
		t.Errorf("global caller all-branches List should clear authz, got status=%d", statusOf(err))
	}
}

// TestReceivedLines_EmptyPOIDIsCleanClientError is the L8 MINOR regression: a DRAFT landed cost with an empty /
// blank po_id must not surface as a bare 500 when Preview/Apply/Retro read its received lines. The shared
// received-lines choke point returns a clean 4xx (422, the doc has no PO to allocate across) BEFORE reaching
// the store. It FAILS without the empty-po_id guard (the empty id would fall through to the seam and 500).
func TestReceivedLines_EmptyPOIDIsCleanClientError(t *testing.T) {
	svc := NewService(NewRepository(nil)).WithCapitalizer(&fakeCapitalizer{})
	for _, poID := range []string{"", "   "} {
		_, err := svc.receivedLines(context.Background(), poID)
		if err == nil {
			t.Fatalf("empty po_id %q: want a client error, got nil", poID)
		}
		if got := statusOf(err); got < 400 || got >= 500 {
			t.Errorf("empty po_id %q: status=%d, want a 4xx (not a 500)", poID, got)
		}
	}
}
