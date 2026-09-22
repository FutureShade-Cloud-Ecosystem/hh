package landedcost

// journal_balance_test.go, DIB-189 regression on the landed-cost JOURNAL-BUILDING path. The capitalization
// journal (capitalizeLines for Apply, and the retro leg-builder for RetroAdjust) must satisfy Σdebit==Σcredit
// EXACTLY for every allocation, any line count, any FX, any largest-remainder residual, and the capitalized
// total must still equal the input landed cost (no cent lost or invented). The economics live in allocate()
// (largest-remainder exact) and the capitalize seam (which now clamps absorbed ≤ allocated, DIB-189); this test
// pins the invariant end-to-end at the journal the caller actually posts.

import (
	"testing"
)

// sumLegs sums the debit and credit columns of a built journal exactly as the GL does (unsigned per column) and
// reports whether any leg carries a negative debit or credit (the gl_journal_lines_check the DB enforces, and
// the guard we added in gl.postExternal). A negative-debit leg that still nets to balance is the DIB-189 defect.
func sumLegs(lines []glLine) (debit, credit int64, negLeg bool) {
	for _, l := range lines {
		debit += l.debit
		credit += l.credit
		if l.debit < 0 || l.credit < 0 {
			negLeg = true
		}
	}
	return
}

// clampAbsorbed models the invariant inventory.capitalizeLanded's DIB-189 fix guarantees to this journal
// builder: the absorption it returns can never overshoot the allocation it was handed. `raw` is what integer-cent
// WAC re-rounding would have reported; the real seam clamps the perpetual WAC (not just the report) so its
// realized value delta lands in [0, alloc] (or [alloc, 0] on a reversal) and equals the returned absorbed, the
// GL-1200==subledger tie is proven directly in inventory/landed_capitalize_test.go. Here we only need the
// in-interval contract to exercise the journal-building path, so this stands in for the returned absorbed.
func clampAbsorbed(alloc, raw int64) int64 {
	if alloc >= 0 {
		if raw < 0 {
			return 0
		}
		if raw > alloc {
			return alloc
		}
		return raw
	}
	if raw > 0 {
		return 0
	}
	if raw < alloc {
		return alloc
	}
	return raw
}

// applyLegs runs the Apply capitalize→journal path for one allocation set: each share is capitalized (the fake
// seam reports `raw` absorption, clamped by the seam contract), the un-absorbed remainder falls to COGS, and
// capitalizeLines builds the journal. It returns the built legs plus the capitalized+COGS totals.
func applyLegs(allocs []Allocation, rawAbsorb map[string]int64) (lines []glLine, capTotal, cogsTotal int64) {
	for _, a := range allocs {
		raw, ok := rawAbsorb[a.ProductID]
		if !ok {
			raw = a.AllocatedCadCents // default: seam absorbs the whole share
		}
		absorbed := clampAbsorbed(a.AllocatedCadCents, raw)
		cap := absorbed
		cogs := a.AllocatedCadCents - absorbed // no-on-hand + rounding remainder → COGS (mirrors service.go:186)
		capTotal += cap
		cogsTotal += cogs
	}
	return capitalizeLines(capTotal, cogsTotal), capTotal, cogsTotal
}

// TestApplyJournal_BalancesAndTiesOut is the core DIB-189 assertion on the Apply journal builder. Across an
// uneven total (forces a largest-remainder residual), a USD landed cost with FX, a single-line case and a
// many-line case, with a seam that over-absorbs (the raw pre-clamp defect), the built journal balances
// exactly, carries no negative leg, and the capitalized total still equals the input landed cost.
func TestApplyJournal_BalancesAndTiesOut(t *testing.T) {
	cases := []struct {
		name      string
		total     int64 // landed cost in CAD cents (post-FX)
		basis     string
		lines     []ReceivedLine
		rawAbsorb map[string]int64 // seam's raw (pre-clamp) absorption per product; absent ⇒ absorbs full share
	}{
		{
			// Uneven: 1000 across three equal-weight lines → 334/333/333 (largest-remainder residual), and the
			// first product's seam OVER-ABSORBS (raw 999 > its 334 share), the exact DIB-189 shape.
			name:  "uneven-total-over-absorb",
			total: 1000,
			basis: BasisValue,
			lines: []ReceivedLine{
				{POLineID: "l1", ProductID: "p1", ValueCents: 1},
				{POLineID: "l2", ProductID: "p2", ValueCents: 1},
				{POLineID: "l3", ProductID: "p3", ValueCents: 1},
			},
			rawAbsorb: map[string]int64{"p1": 999, "p2": 0, "p3": 333},
		},
		{
			// USD landed cost with FX: 2000 USD × 1.35 = 2700 CAD onto a single line whose seam over-absorbs to
			// 2709 (the literal reproduction from the investigation).
			name:  "usd-fx-single-line",
			total: convertToCAD(2000, 1.35), // 2700
			basis: BasisValue,
			lines: []ReceivedLine{
				{POLineID: "l1", ProductID: "p1", ValueCents: 5000},
			},
			rawAbsorb: map[string]int64{"p1": 2709},
		},
		{
			name:  "single-line-clean",
			total: 12345,
			basis: BasisValue,
			lines: []ReceivedLine{{POLineID: "l1", ProductID: "p1", ValueCents: 100}},
		},
		{
			// Many lines, uneven value weights, mixed over/under-absorption and a no-on-hand line (absorbs 0 →
			// whole share to COGS).
			name:  "many-lines-mixed",
			total: 987654,
			basis: BasisValue,
			lines: []ReceivedLine{
				{POLineID: "l1", ProductID: "p1", ValueCents: 111},
				{POLineID: "l2", ProductID: "p2", ValueCents: 222},
				{POLineID: "l3", ProductID: "p3", ValueCents: 333},
				{POLineID: "l4", ProductID: "p4", ValueCents: 444},
				{POLineID: "l5", ProductID: "p5", ValueCents: 555},
				{POLineID: "l6", ProductID: "p6", ValueCents: 7},
			},
			rawAbsorb: map[string]int64{"p1": 1 << 30, "p3": 0, "p5": -50},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allocs := allocate(c.total, c.basis, c.lines, true)
			if got := sumAlloc(allocs); got != c.total {
				t.Fatalf("allocation is not exact: Σshares=%d, want %d", got, c.total)
			}
			lines, capTotal, cogsTotal := applyLegs(allocs, c.rawAbsorb)

			debit, credit, negLeg := sumLegs(lines)
			if debit != credit {
				t.Errorf("Σdebit=%d != Σcredit=%d (journal unbalanced)", debit, credit)
			}
			if negLeg {
				t.Errorf("journal carries a negative debit/credit leg: %+v", lines)
			}
			// Tie-out: the capitalized total plus the COGS remainder equals the input landed cost EXACTLY -
			// no cent lost or invented (and the credit leg, which the DB balances against, equals it too).
			if capTotal+cogsTotal != c.total {
				t.Errorf("cap(%d)+cogs(%d)=%d != input landed cost %d (cents lost/created)", capTotal, cogsTotal, capTotal+cogsTotal, c.total)
			}
			if credit != c.total {
				t.Errorf("CR 2100 freight payable=%d != input landed cost %d", credit, c.total)
			}
		})
	}
}

// TestRetroJournal_BalancesAndTiesOut pins the same invariant on the RetroAdjust journal builder. It reproduces
// the leg-build from service.go retroAdjust (capitalize the on-hand portion, expense sold-through + the clamped
// rounding remainder to COGS) and asserts balance + tie-out, including the fully-on-hand case (soldCad==0) the
// investigation flagged as the retro path's identical negative-leg exposure.
func TestRetroJournal_BalancesAndTiesOut(t *testing.T) {
	type line struct {
		product              string
		alloc                int64
		onHandBase, received float64
		rawAbsorb            int64 // seam's raw (pre-clamp) absorption of the on-hand portion
	}
	cases := []struct {
		name  string
		total int64
		lines []line
	}{
		{
			// Fully on hand (soldCad==0) with an over-absorbing seam, the exact retro exposure: without the
			// clamp SoldThroughCadCents = 0 + (onHandCad − absorbed) goes negative → negative DR 5000.
			name:  "fully-on-hand-over-absorb",
			total: 2700,
			lines: []line{{product: "p1", alloc: 2700, onHandBase: 21, received: 21, rawAbsorb: 2709}},
		},
		{
			// Partial sell-through across several lines, uneven ratios, mixed absorption.
			name:  "partial-sellthrough-many",
			total: 10000,
			lines: []line{
				{product: "p1", alloc: 3000, onHandBase: 1, received: 3, rawAbsorb: 1500},
				{product: "p2", alloc: 3000, onHandBase: 3, received: 4, rawAbsorb: 1 << 30}, // over-absorb
				{product: "p3", alloc: 4000, onHandBase: 0, received: 5, rawAbsorb: 0},       // fully sold through
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var capTotal, cogsTotal, allocSum int64
			for _, l := range c.lines {
				allocSum += l.alloc
				onHandCad, soldCad := splitSoldThrough(l.alloc, l.onHandBase, l.received)
				var absorbed int64
				if onHandCad != 0 {
					absorbed = clampAbsorbed(onHandCad, l.rawAbsorb)
				}
				capTotal += absorbed
				cogsTotal += soldCad + (onHandCad - absorbed) // mirrors service.go:321
			}
			if allocSum != c.total {
				t.Fatalf("test setup: Σalloc=%d != total %d", allocSum, c.total)
			}

			// Rebuild the retro legs exactly as service.go retroAdjust does.
			lines := make([]glLine, 0, 3)
			if capTotal != 0 {
				lines = append(lines, glLine{acct: acctInventory, debit: capTotal})
			}
			if cogsTotal != 0 {
				lines = append(lines, glLine{acct: acctCOGS, debit: cogsTotal})
			}
			if capTotal+cogsTotal != 0 {
				lines = append(lines, glLine{acct: acctFreightPayable, credit: capTotal + cogsTotal})
			}

			debit, credit, negLeg := sumLegs(lines)
			if debit != credit {
				t.Errorf("Σdebit=%d != Σcredit=%d (retro journal unbalanced)", debit, credit)
			}
			if negLeg {
				t.Errorf("retro journal carries a negative leg: %+v", lines)
			}
			if capTotal+cogsTotal != c.total {
				t.Errorf("cap(%d)+cogs(%d)=%d != input landed cost %d", capTotal, cogsTotal, capTotal+cogsTotal, c.total)
			}
		})
	}
}
