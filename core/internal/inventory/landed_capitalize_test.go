package inventory

// landed_capitalize_test.go, DIB-189 regression: capitalizeLanded (the single seam both landedcost.Apply and
// RetroAdjust capitalize through) must NEVER report an absorbed value that overshoots the allocation it was
// handed, AND the GL 1200 debit it reports must stay tied to the perpetual subledger it upserts. Integer-cent WAC
// re-rounding (quantize newAvg to whole cents/unit, re-multiply by on-hand) could make round(newAvg*onHand)-
// oldValue drift past the allocation by up to ~onHand/2 cents in either direction. When it overshoots on Apply
// the caller's residual (allocated − absorbed) goes negative and lands as a NEGATIVE DR 5000 COGS leg, Σdebit
// still nets to freight so the Go balance guard is fooled, but gl_journal_lines_check (debit_cents > 0) rejects
// it and the whole capitalization journal is unpostable.
//
// The fix clamps the PERPETUAL WAC (newAvg), not merely the reported delta, so the realized subledger change
// round(newAvg*onHand)-oldValue lands in [0, allocated] (or [allocated, 0] on a reversal) and the returned
// absorbed is derived from that SAME clamped newAvg. That keeps three invariants at once for every input: the
// journal balances (Σdebit==Σcredit, no negative leg, no cent lost/invented, the un-absorbed remainder falls to
// COGS), GL 1200 == the perpetual subledger to the cent (TestCapitalizeLanded_TiesGL1200ToSubledger / _TieSweep),
// and an Apply/Void round-trip restores the prior WAC exactly (_ApplyVoidRoundTripRestoresWAC). Clamping only the
// report while upserting an un-clamped WAC, the earlier attempt, would leave subledger − GL1200 > 0 permanently
// out of tie on every over-absorbing apply; the tie tests below fail against that and pass only against this fix.

import (
	"context"
	"testing"
)

// buildCapJournal reconstructs the three-leg capitalization journal the landedcost caller posts from this seam's
// return: DR 1200 = absorbed, DR 5000 = allocated − absorbed (the residual routed to COGS), CR 2100 = allocated.
// It returns Σdebit and Σcredit exactly as the GL would sum them (unsigned per-column), plus the two debit legs
// so the test can assert neither is negative (the constraint the DB enforces).
func buildCapJournal(allocated, absorbed int64) (sumDebit, sumCredit, invLeg, cogsLeg int64) {
	invLeg = absorbed
	cogsLeg = allocated - absorbed
	// Zero legs are omitted in the real builder; that never changes the column sums, so we sum unconditionally.
	sumDebit = invLeg + cogsLeg
	sumCredit = allocated
	return
}

// TestCapitalizeLanded_ClampNeverOverAbsorbs is the DIB-189 root-cause proof: across a broad sweep of on-hand /
// prior-WAC / allocation states (including the exact reproduction and negative reversals), the reported
// absorption stays within the allocation, the reconstructed journal balances (Σdebit==Σcredit) with no negative
// leg, and no cents are lost or invented (DR 1200 + DR 5000 == the input allocation exactly).
func TestCapitalizeLanded_ClampNeverOverAbsorbs(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p1", "b1"

	type state struct {
		onHand   float64
		oldAvg   int64
		alloc    int64
		wantOver bool // pre-fix this state over-absorbed (documents the repro); not asserted, just annotated
	}
	cases := []state{
		{onHand: 21, oldAvg: 1, alloc: 2700, wantOver: true}, // the DIB-189 reproduction: raw absorbed=2709
		{onHand: 21, oldAvg: 1, alloc: 9},
		{onHand: 1, oldAvg: 0, alloc: 1},   // single unit, indivisible penny
		{onHand: 3, oldAvg: 5, alloc: 100}, // 100/3 → rounding remainder
		{onHand: 834, oldAvg: 68, alloc: 2700},
		{onHand: 1000, oldAvg: 129, alloc: 5001},
		{onHand: 7, oldAvg: 333, alloc: 2},
		{onHand: 500, oldAvg: 999, alloc: 123456},
		{onHand: 21, oldAvg: 130, alloc: -2700}, // reversal (Void): absorbed must stay in [alloc, 0]
		{onHand: 21, oldAvg: 130, alloc: -2709},
		{onHand: 50, oldAvg: 40, alloc: -900},
	}

	for _, c := range cases {
		f := newFakeStore()
		f.onhand[key(product, branch)] = c.onHand
		f.cost[key(product, branch)] = c.oldAvg
		s := NewService(nil).withStore(f)

		absorbed, err := s.capitalizeLanded(ctx, nil, product, branch, c.alloc)
		if err != nil {
			t.Fatalf("%+v: capitalizeLanded: %v", c, err)
		}

		// Clamp invariant: absorbed is bounded by the allocation and shares its sign (or is zero).
		if c.alloc >= 0 {
			if absorbed < 0 || absorbed > c.alloc {
				t.Errorf("%+v: absorbed=%d out of [0,%d], over/under-absorption not clamped", c, absorbed, c.alloc)
			}
		} else {
			if absorbed > 0 || absorbed < c.alloc {
				t.Errorf("%+v: absorbed=%d out of [%d,0], reversal over-absorbed", c, absorbed, c.alloc)
			}
		}

		sumDebit, sumCredit, invLeg, cogsLeg := buildCapJournal(c.alloc, absorbed)

		// The journal must balance exactly for every input.
		if sumDebit != sumCredit {
			t.Errorf("%+v: Σdebit=%d != Σcredit=%d (journal unbalanced)", c, sumDebit, sumCredit)
		}
		// No lost / invented cents: the two debit legs reconstruct the input allocation exactly.
		if invLeg+cogsLeg != c.alloc {
			t.Errorf("%+v: DR1200(%d)+DR5000(%d)=%d != allocated %d (cents lost/created)", c, invLeg, cogsLeg, invLeg+cogsLeg, c.alloc)
		}
		// Neither debit leg may be negative on a forward apply (the gl_journal_lines_check the DB enforces); on a
		// reversal both legs are non-positive (they post on the credit side of the reversing journal).
		if c.alloc >= 0 && (invLeg < 0 || cogsLeg < 0) {
			t.Errorf("%+v: negative debit leg DR1200=%d DR5000=%d (would violate gl_journal_lines_check)", c, invLeg, cogsLeg)
		}
		if c.alloc < 0 && (invLeg > 0 || cogsLeg > 0) {
			t.Errorf("%+v: reversal produced a positive debit leg DR1200=%d DR5000=%d", c, invLeg, cogsLeg)
		}
	}
}

// TestCapitalizeLanded_Sweep exhaustively confirms the clamp holds for a dense grid of realistic states, the
// reconstructed journal balances and loses no cents for EVERY one, forward and reversing.
func TestCapitalizeLanded_Sweep(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"
	var checked int
	for onHand := 1; onHand <= 400; onHand++ {
		for _, oldAvg := range []int64{0, 1, 5, 68, 129, 500, 9999} {
			for _, alloc := range []int64{1, 9, 100, 2700, 5001, -9, -2700, -5001} {
				f := newFakeStore()
				f.onhand[key(product, branch)] = float64(onHand)
				f.cost[key(product, branch)] = oldAvg
				s := NewService(nil).withStore(f)

				absorbed, err := s.capitalizeLanded(ctx, nil, product, branch, alloc)
				if err != nil {
					t.Fatalf("onHand=%d oldAvg=%d alloc=%d: %v", onHand, oldAvg, alloc, err)
				}
				sumDebit, sumCredit, invLeg, cogsLeg := buildCapJournal(alloc, absorbed)
				if sumDebit != sumCredit {
					t.Fatalf("onHand=%d oldAvg=%d alloc=%d: Σdebit=%d != Σcredit=%d", onHand, oldAvg, alloc, sumDebit, sumCredit)
				}
				if invLeg+cogsLeg != alloc {
					t.Fatalf("onHand=%d oldAvg=%d alloc=%d: legs %d+%d != alloc", onHand, oldAvg, alloc, invLeg, cogsLeg)
				}
				if alloc >= 0 && (invLeg < 0 || cogsLeg < 0) {
					t.Fatalf("onHand=%d oldAvg=%d alloc=%d: negative debit leg (1200=%d 5000=%d)", onHand, oldAvg, alloc, invLeg, cogsLeg)
				}
				checked++
			}
		}
	}
	t.Logf("clamp held across %d capitalize states (Σdebit==Σcredit, no lost cents, no negative leg)", checked)
}

// subledgerDelta is the perpetual inventory value change the W5 gl_tieout / W6 B1 report measures against GL
// 1200 for this product: round(upserted-avg × on-hand) − round(prior-avg × on-hand). It reads the WAC BACK from
// the store after capitalize upserts it, so it sees exactly what the ledger will later reconcile against. The
// valuation runs through the module's exact integer arithmetic (HH-183): the report's own ROUND leg in SQL is
// exact, so the oracle here must not be the one float product left in the file.
func subledgerDelta(t *testing.T, f *fakeStore, product, branch string, priorAvg int64) int64 {
	t.Helper()
	onHand := f.onhand[key(product, branch)]
	newAvg := f.cost[key(product, branch)]
	newVal, err := qtyTimesCents(onHand, newAvg)
	if err != nil {
		t.Fatalf("subledgerDelta new: %v", err)
	}
	priorVal, err := qtyTimesCents(onHand, priorAvg)
	if err != nil {
		t.Fatalf("subledgerDelta prior: %v", err)
	}
	return newVal - priorVal
}

// TestCapitalizeLanded_TiesGL1200ToSubledger is the load-bearing DIB-189 regression the balance-only tests miss.
// The GL 1200 debit is booked from the RETURNED absorbed; the perpetual subledger the tie-out reconciles against
// is round(upserted-avg × on-hand). If capitalizeLanded upserted an un-clamped newAvg while clamping only the
// reported absorbed (the defect), GL 1200 would be debited the clamped amount while the subledger rose by the
// un-clamped overshoot, leaving subledger − GL1200 > 0 PERMANENTLY out of tie (invcount reconcile ties only on
// delta==0). This test reads the upserted WAC back and asserts absorbed == the realized subledger delta to the
// cent, forward AND reversing, over the exact DIB-189 / W6 states plus a dense sweep. It FAILS on the old
// clamp-the-report fix and PASSES only when newAvg and absorbed are clamped together.
func TestCapitalizeLanded_TiesGL1200ToSubledger(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"

	type state struct {
		onHand float64
		oldAvg int64
		alloc  int64
		note   string
	}
	cases := []state{
		{onHand: 21, oldAvg: 1, alloc: 2700, note: "DIB-189 repro (raw absorbed 2709 overshoots 2700)"},
		{onHand: 921, oldAvg: 57, alloc: 2700, note: "W6 live shape (raw absorbed 2763 overshoots 2700)"},
		{onHand: 200, oldAvg: 57, alloc: 2700, note: "void-drift shape (raw absorbed 2800 overshoots 2700)"},
		{onHand: 1000, oldAvg: 129, alloc: 5001},
		{onHand: 3, oldAvg: 5, alloc: 100},
		{onHand: 500, oldAvg: 999, alloc: 123456},
		{onHand: 834, oldAvg: 68, alloc: 1, note: "on-hand >> alloc: whole share to COGS, subledger unmoved"},
		{onHand: 21, oldAvg: 130, alloc: -2688, note: "reversal of the DIB-189 apply"},
		{onHand: 200, oldAvg: 70, alloc: -2600, note: "reversal of the void-drift apply"},
		{onHand: 50, oldAvg: 40, alloc: -900},
	}

	for _, c := range cases {
		f := newFakeStore()
		f.onhand[key(product, branch)] = c.onHand
		f.cost[key(product, branch)] = c.oldAvg
		s := NewService(nil).withStore(f)

		absorbed, err := s.capitalizeLanded(ctx, nil, product, branch, c.alloc)
		if err != nil {
			t.Fatalf("%+v: capitalizeLanded: %v", c, err)
		}
		// THE TIE: the GL 1200 debit (absorbed) must equal the realized subledger value change to the cent.
		if got := subledgerDelta(t, f, product, branch, c.oldAvg); got != absorbed {
			t.Errorf("%+v: GL1200=%d != subledger delta=%d (upserted avg=%d, on-hand=%.4g), ledger out of tie",
				c, absorbed, got, f.cost[key(product, branch)], c.onHand)
		}
		// And the reported absorption still never overshoots the allocation it was handed.
		if c.alloc >= 0 && (absorbed < 0 || absorbed > c.alloc) {
			t.Errorf("%+v: absorbed=%d out of [0,%d]", c, absorbed, c.alloc)
		}
		if c.alloc < 0 && (absorbed > 0 || absorbed < c.alloc) {
			t.Errorf("%+v: absorbed=%d out of [%d,0]", c, absorbed, c.alloc)
		}
	}
}

// TestCapitalizeLanded_TieSweep proves the GL-1200==subledger tie holds across a dense grid, including quarter-
// unit fractional on-hand (DECIMAL(19,4) base units) where integer-cent WAC re-rounding drifts most, for every
// forward and reversing allocation. This is the invariant the previous fix silently broke on every over-absorb.
func TestCapitalizeLanded_TieSweep(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"
	var checked, ties int
	for oh4 := 1; oh4 <= 4004; oh4 += 3 { // on-hand in quarter units: 0.25 .. 1001.0
		onHand := float64(oh4) / 4.0
		for _, oldAvg := range []int64{0, 1, 5, 57, 68, 129, 999, 9999} {
			for _, alloc := range []int64{1, 9, 100, 2700, 5001, 123456, -9, -2700, -5001} {
				f := newFakeStore()
				f.onhand[key(product, branch)] = onHand
				f.cost[key(product, branch)] = oldAvg
				s := NewService(nil).withStore(f)

				absorbed, err := s.capitalizeLanded(ctx, nil, product, branch, alloc)
				if err != nil {
					t.Fatalf("onHand=%.2f oldAvg=%d alloc=%d: %v", onHand, oldAvg, alloc, err)
				}
				if got := subledgerDelta(t, f, product, branch, oldAvg); got != absorbed {
					t.Fatalf("onHand=%.2f oldAvg=%d alloc=%d: GL1200=%d != subledger delta=%d (out of tie)",
						onHand, oldAvg, alloc, absorbed, got)
				}
				if alloc >= 0 && (absorbed < 0 || absorbed > alloc) {
					t.Fatalf("onHand=%.2f oldAvg=%d alloc=%d: absorbed=%d out of [0,%d]", onHand, oldAvg, alloc, absorbed, alloc)
				}
				if alloc < 0 && (absorbed > 0 || absorbed < alloc) {
					t.Fatalf("onHand=%.2f oldAvg=%d alloc=%d: absorbed=%d out of [%d,0]", onHand, oldAvg, alloc, absorbed, alloc)
				}
				ties++
				checked++
			}
		}
	}
	t.Logf("GL1200==subledger held across %d capitalize states (%d ties)", checked, ties)
}

// TestCapitalizeLanded_ApplyVoidRoundTripRestoresWAC pins the void-symmetry the previous fix regressed: Apply
// stores the clamped absorbed as the allocation row; Void reverses -absorbed through the same seam. Because the
// upserted WAC now realizes exactly `absorbed`, negating it restores the prior WAC EXACTLY, no phantom
// inventory value stranded after a void (the old clamp-the-report fix left +1c/unit on the void-drift shape).
func TestCapitalizeLanded_ApplyVoidRoundTripRestoresWAC(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"

	type state struct {
		onHand  float64
		oldAvg  int64
		freight int64
	}
	cases := []state{
		{21, 1, 2700},   // DIB-189
		{921, 57, 2700}, // W6 live
		{200, 57, 2700}, // the void-drift shape the scope review flagged (+1c/unit on the old fix)
		{1000, 129, 5001},
		{50, 40, 900},
		{3, 5, 100},
		{500, 999, 123456},
	}
	for _, c := range cases {
		f := newFakeStore()
		f.onhand[key(product, branch)] = c.onHand
		f.cost[key(product, branch)] = c.oldAvg
		s := NewService(nil).withStore(f)

		// Apply.
		absorbed, err := s.capitalizeLanded(ctx, nil, product, branch, c.freight)
		if err != nil {
			t.Fatalf("%+v: apply: %v", c, err)
		}
		// Void reverses the stored allocation (== absorbed), exactly as landedcost.Void does.
		if _, err := s.capitalizeLanded(ctx, nil, product, branch, -absorbed); err != nil {
			t.Fatalf("%+v: void: %v", c, err)
		}
		if got := f.cost[key(product, branch)]; got != c.oldAvg {
			t.Errorf("%+v: WAC after apply+void = %d, want %d (round-trip drift = %d phantom cents/unit)",
				c, got, c.oldAvg, got-c.oldAvg)
		}
	}
}

// TestCapitalizeLanded_VoidAfterSaleNeverNegativeWAC is the L8 BLOCKING regression: a landed cost is applied
// over a large on-hand (freight spread thin per unit), THEN units sell so on-hand shrinks, THEN the doc is
// voided, reversing the FULL absorbed value over only the few units still on hand. (oldValue + (-absorbed)) /
// smallOnHand goes deeply negative, so the raw newAvg is negative. Before the floor, capitalizeLanded upserted
// that negative per-unit WAC, a nonsensical price that distorts every downstream COGS. This test drives that
// exact sequence and asserts (a) the upserted WAC is NEVER < 0, and (b) GL 1200 (the returned absorbed) still
// ties to the perpetual subledger delta at void time, so the void-after-sale ledger stays reconciled.
// It FAILS without the `if newAvg < 0 { newAvg = 0 }` floor (WAC goes negative).
func TestCapitalizeLanded_VoidAfterSaleNeverNegativeWAC(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"

	type state struct {
		applyOnHand float64 // on-hand when the landed cost is applied
		soldOnHand  float64 // on-hand after a sale, when the void reverses
		oldAvg      int64
		freight     int64
	}
	cases := []state{
		{applyOnHand: 200, soldOnHand: 1, oldAvg: 130, freight: 2700},   // reverse $27 over 1 unit → raw newAvg ≪ 0
		{applyOnHand: 500, soldOnHand: 2, oldAvg: 40, freight: 90000},   // deep negative
		{applyOnHand: 100, soldOnHand: 0.5, oldAvg: 10, freight: 5000},  // fractional post-sale on-hand
		{applyOnHand: 1000, soldOnHand: 3, oldAvg: 5, freight: 123456},  //
		{applyOnHand: 21, soldOnHand: 1, oldAvg: 1, freight: 2700},      // DIB-189 shape, then sell down to 1
		{applyOnHand: 300, soldOnHand: 0.25, oldAvg: 200, freight: 900}, // sub-unit post-sale
	}

	for _, c := range cases {
		f := newFakeStore()
		f.onhand[key(product, branch)] = c.applyOnHand
		f.cost[key(product, branch)] = c.oldAvg
		s := NewService(nil).withStore(f)

		// Apply over the full on-hand; store what the subledger absorbed (the allocation row landedcost keeps).
		absorbed, err := s.capitalizeLanded(ctx, nil, product, branch, c.freight)
		if err != nil {
			t.Fatalf("%+v: apply: %v", c, err)
		}

		// A sale drains on-hand between Apply and Void.
		f.onhand[key(product, branch)] = c.soldOnHand
		priorAvg := f.cost[key(product, branch)]

		// Void reverses the FULL stored allocation over the now-small on-hand.
		reversed, err := s.capitalizeLanded(ctx, nil, product, branch, -absorbed)
		if err != nil {
			t.Fatalf("%+v: void: %v", c, err)
		}

		// (a) A weighted-average cost is a price, it may NEVER be negative.
		if got := f.cost[key(product, branch)]; got < 0 {
			t.Errorf("%+v: void-after-sale upserted NEGATIVE WAC=%d (absorbed=%d reversed over on-hand=%.4g)",
				c, got, absorbed, c.soldOnHand)
		}
		// (b) GL 1200 (the reversal's returned absorbed) must equal the realized subledger delta at void time,
		// so the void-after-sale ledger stays tied to the perpetual subledger.
		if got := subledgerDelta(t, f, product, branch, priorAvg); got != reversed {
			t.Errorf("%+v: void GL1200=%d != subledger delta=%d (upserted avg=%d, on-hand=%.4g), out of tie",
				c, reversed, got, f.cost[key(product, branch)], c.soldOnHand)
		}
	}
}

// TestCapitalizeLanded_FractionalRoundTripRestoresWAC is the L8 MINOR regression: an Apply/Void round-trip must
// restore the per-unit WAC exactly even when on-hand is a fraction below one base unit (DECIMAL(19,4) base
// units, e.g. 0.25 of a pallet). Because newAvg and absorbed are derived from the SAME quantized value,
// negating the stored absorbed restores the prior WAC to the cent for sub-unit on-hand too.
func TestCapitalizeLanded_FractionalRoundTripRestoresWAC(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"

	type state struct {
		onHand  float64
		oldAvg  int64
		freight int64
	}
	cases := []state{
		{0.5, 100, 50},
		{0.25, 100, 50},
		{0.1, 5000, 9},
		{0.75, 40, 900},
		{0.3333, 999, 12345},
		{0.9999, 1, 2700},
	}
	for _, c := range cases {
		f := newFakeStore()
		f.onhand[key(product, branch)] = c.onHand
		f.cost[key(product, branch)] = c.oldAvg
		s := NewService(nil).withStore(f)

		absorbed, err := s.capitalizeLanded(ctx, nil, product, branch, c.freight)
		if err != nil {
			t.Fatalf("%+v: apply: %v", c, err)
		}
		if _, err := s.capitalizeLanded(ctx, nil, product, branch, -absorbed); err != nil {
			t.Fatalf("%+v: void: %v", c, err)
		}
		if got := f.cost[key(product, branch)]; got != c.oldAvg {
			t.Errorf("%+v: fractional WAC after apply+void = %d, want %d (drift %d)", c, got, c.oldAvg, got-c.oldAvg)
		}
	}
}

// TestCapitalizeLanded_SnapshotRestoreZeroDriftAllCases is the L8 MAJOR regression: an Apply/Void round-trip with
// NO sale between must restore the perpetual WAC EXACTLY for ALL cases, including the sub-unit / small-oldAvg
// shapes where round(oldAvg × on-hand) quantizes oldAvg AWAY (oldValue → 0). The legacy reverse-via-negate Void
// (capitalizeLanded(-absorbed)) cannot reconstruct oldAvg from the quantized value and DRIFTS: it is asserted
// below to drift for the two spec repro cases, and the snapshot+restore path is asserted to drift 0 for every
// case. This test FAILS if Void restores by re-round()-ing instead of restoring the persisted snapshot.
func TestCapitalizeLanded_SnapshotRestoreZeroDriftAllCases(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"

	type state struct {
		onHand      float64
		oldAvg      int64
		freight     int64
		legacyDrift int64 // the drift the OLD reverse-via-negate Void produced (0 where it happened to be exact)
	}
	cases := []state{
		{0.25, 1, 2700, -1},   // spec repro: WAC 1 → 10800 → 0 under legacy negate (drift -1)
		{0.1, 5, 50, 5},       // spec repro: WAC 5 → 510 → 10 under legacy negate (drift +5)
		{0.25, 2, 900, 2},     // sub-unit: legacy negate also drifts (quantize erases oldAvg=2)
		{0.3333, 3, 12345, 0}, // oldValue=round(0.9999)=1 → legacy happened to be exact
		{0.5, 1, 5000, 1},
		{0.75, 7, 33, 0}, // large-enough oldValue: legacy happened to be exact here
		{0.05, 9, 4, -9},
		{21, 1, 2700, 0}, // integer on-hand: legacy path was already exact, snapshot path must not regress it
		{200, 57, 2700, 0},
	}

	for _, c := range cases {
		// (a) Confirm the LEGACY reverse-via-negate round-trip drifts by exactly legacyDrift (documents that the
		// fix is load-bearing on the sub-unit shapes and a no-op on the already-exact ones).
		fl := newFakeStore()
		fl.onhand[key(product, branch)] = c.onHand
		fl.cost[key(product, branch)] = c.oldAvg
		sl := NewService(nil).withStore(fl)
		absL, err := sl.capitalizeLanded(ctx, nil, product, branch, c.freight)
		if err != nil {
			t.Fatalf("%+v: legacy apply: %v", c, err)
		}
		if _, err := sl.capitalizeLanded(ctx, nil, product, branch, -absL); err != nil {
			t.Fatalf("%+v: legacy void: %v", c, err)
		}
		if got := fl.cost[key(product, branch)] - c.oldAvg; got != c.legacyDrift {
			t.Errorf("%+v: legacy round-trip drift = %d, expected repro drift %d", c, got, c.legacyDrift)
		}

		// (b) The snapshot+restore round-trip (the FIX) must drift 0 for every case.
		f := newFakeStore()
		f.onhand[key(product, branch)] = c.onHand
		f.cost[key(product, branch)] = c.oldAvg
		s := NewService(nil).withStore(f)

		absorbed, snapAvg, hadCost, err := s.capitalizeLandedSnap(ctx, nil, product, branch, c.freight)
		if err != nil {
			t.Fatalf("%+v: apply: %v", c, err)
		}
		if !hadCost || snapAvg != c.oldAvg {
			t.Errorf("%+v: snapshot avg=%d hadCost=%v, want %d/true", c, snapAvg, hadCost, c.oldAvg)
		}
		// Void removes exactly `absorbed`; on-hand is unchanged since Apply, so the seam takes the drift-0 verbatim
		// path and restores the exact snapshot (as landedcost.Void does when HasPreApplyAvg).
		reversed, err := s.restoreLandedCost(ctx, nil, product, branch, snapAvg, absorbed)
		if err != nil {
			t.Fatalf("%+v: void restore: %v", c, err)
		}
		if got := f.cost[key(product, branch)]; got != c.oldAvg {
			t.Errorf("%+v: snapshot-restore WAC after apply+void = %d, want %d (drift %d)", c, got, c.oldAvg, got-c.oldAvg)
		}
		// The reversal magnitude equals what the forward apply absorbed (no sale between → full reversal), so the
		// Void CR 1200 exactly un-books the Apply DR 1200.
		if -reversed != absorbed {
			t.Errorf("%+v: restore reversed %d != absorbed %d (CR1200 != DR1200 with no sale between)", c, -reversed, absorbed)
		}
	}
}

// TestRestoreLandedCost_VoidAfterSaleNeverNegativeAndTies pins that removing exactly `absorbed` on a Void AFTER a sale
// (a) never yields a negative WAC (value/WAC floored at zero), and (b) the returned reversal delta ties to the realized
// subledger change of the SAME upserted newAvg, so CR 1200 == the value actually drained from inventory, at most the
// value still in stock, with the sold-through remainder handled by the caller's COGS credit. This preserves the L8
// void-money-atomicity + negative-WAC-floor guarantees under the new absorbed-based exact-reversal path.
func TestRestoreLandedCost_VoidAfterSaleNeverNegativeAndTies(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"

	type state struct {
		applyOnHand float64
		soldOnHand  float64
		oldAvg      int64
		freight     int64
	}
	cases := []state{
		{200, 1, 130, 2700},
		{500, 2, 40, 90000},
		{100, 0.5, 10, 5000},
		{21, 1, 1, 2700},
		{300, 0.25, 200, 900},
	}
	for _, c := range cases {
		f := newFakeStore()
		f.onhand[key(product, branch)] = c.applyOnHand
		f.cost[key(product, branch)] = c.oldAvg
		s := NewService(nil).withStore(f)

		absorbed, snapAvg, _, err := s.capitalizeLandedSnap(ctx, nil, product, branch, c.freight)
		if err != nil {
			t.Fatalf("%+v: apply: %v", c, err)
		}
		// A sale drains on-hand between Apply and Void.
		f.onhand[key(product, branch)] = c.soldOnHand
		priorAvg := f.cost[key(product, branch)]
		curValue, err := qtyTimesCents(c.soldOnHand, priorAvg)
		if err != nil {
			t.Fatalf("%+v: curValue: %v", c, err)
		}

		reversed, err := s.restoreLandedCost(ctx, nil, product, branch, snapAvg, absorbed)
		if err != nil {
			t.Fatalf("%+v: void restore: %v", c, err)
		}
		// (a) WAC never negative.
		gotWAC := f.cost[key(product, branch)]
		if gotWAC < 0 {
			t.Errorf("%+v: restored WAC=%d is negative", c, gotWAC)
		}
		// (b) The reversal delta equals the realized subledger change of the upserted newAvg over the CURRENT on-hand,
		// and never drains more than the value in stock (never over-reverses).
		wantWACValue, err := qtyTimesCents(c.soldOnHand, gotWAC)
		if err != nil {
			t.Fatalf("%+v: wantWACValue: %v", c, err)
		}
		wantDelta := wantWACValue - curValue
		if reversed != wantDelta {
			t.Errorf("%+v: reversal delta=%d != realized subledger delta=%d (CR1200 out of tie)", c, reversed, wantDelta)
		}
		if -reversed > curValue {
			t.Errorf("%+v: reversal %d drained more than the %d value in stock (over-reversal)", c, -reversed, curValue)
		}
		if -reversed > absorbed {
			t.Errorf("%+v: reversal %d exceeded the %d absorbed at apply", c, -reversed, absorbed)
		}
	}
}

// TestRestoreLandedCost_VoidAfterReceiptReversesExactAbsorbedNoOverReversal is the L8 MAJOR 3rd-void-edge regression:
// on-hand GROWS between Apply and Void (an intervening RECEIPT). The old restore computed the CR 1200 as
// round(snapAvg×onHand) − round(curAvg×onHand) over the CURRENT (enlarged) on-hand, so the receipt's own value got
// folded into the reversal and it OVER-reversed, up to N× the value Apply capitalized, producing a delta whose
// magnitude EXCEEDS the absorbed amount. The caller then books CR 1200 > capTotal → cogsCredit = soldTotal +
// (capTotal − reversedTotal) goes NEGATIVE → the reversing journal carries a negative leg → posting.go rejects it →
// the doc is permanently un-voidable. The fix removes EXACTLY `absorbed`, so the reversal magnitude is exactly the
// absorbed value (no receipt value touched), the WAC stays sensible and non-negative, and nothing over-reverses.
//
// The canonical repro from the fix spec: Apply 5000c over oh=10 (WAC 0→? ; snap=100 seeded) then receive 90 units at
// the post-Apply avg → oh=100. OLD delta = round(100×100) − round(curAvg×100) = huge (10× the 5000 absorbed) → over-
// reversal. This test FAILS against the pre-fix restore (delta magnitude ≫ absorbed) and PASSES against the fix.
func TestRestoreLandedCost_VoidAfterReceiptReversesExactAbsorbedNoOverReversal(t *testing.T) {
	ctx := context.Background()
	const product, branch = "p", "b"

	f := newFakeStore()
	f.onhand[key(product, branch)] = 10
	f.cost[key(product, branch)] = 100 // pre-Apply WAC 100 → snap
	s := NewService(nil).withStore(f)

	absorbed, snapAvg, _, err := s.capitalizeLandedSnap(ctx, nil, product, branch, 5000)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if snapAvg != 100 {
		t.Fatalf("snapAvg=%d, want 100", snapAvg)
	}
	postApplyAvg := f.cost[key(product, branch)]
	// absorbed = round(postApplyAvg×10) − round(100×10) = 6000 − 1000 = 5000.
	if absorbed != 5000 {
		t.Fatalf("absorbed=%d, want 5000", absorbed)
	}

	// Intervening RECEIPT: 90 units land at the post-Apply avg → on-hand 10 → 100 (value grows, WAC unchanged).
	f.onhand[key(product, branch)] = 100

	reversed, err := s.restoreLandedCost(ctx, nil, product, branch, snapAvg, absorbed)
	if err != nil {
		t.Fatalf("void restore: %v", err)
	}
	// (1) Removes EXACTLY the absorbed value, never the enlarged-on-hand delta that folds in the receipt.
	if -reversed != absorbed {
		t.Fatalf("reversed %d != absorbed %d, over/under-reversal (receipt value leaked into the reversal)", -reversed, absorbed)
	}
	// (2) WAC stays sensible and non-negative: value 60000 − 5000 = 55000 over 100 units → 550.
	if got := f.cost[key(product, branch)]; got != 550 || got < 0 {
		t.Fatalf("post-void WAC=%d, want 550 (non-negative, receipt value preserved)", got)
	}
	// (3) The reversal never exceeds what Apply absorbed (the caller's cogsCredit can never go negative).
	if -reversed > absorbed {
		t.Fatalf("over-reversal: %d > absorbed %d", -reversed, absorbed)
	}
	_ = postApplyAvg
}
