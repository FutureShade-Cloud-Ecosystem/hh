package main

// r15_fix_leg7_test.go is the card B fix round's red leg 7 (A2's round 3 P3, held at the
// packet B review pair, taken into this fix round at decision log row 860): on a row whose
// manager note folds last_error to the cap, the FAILING REVERSAL END must still land its own
// cause text on the row. The pins, in order:
//
//  1. A claim, stamped UNKNOWN (the sweep's shape), then Resolve with a note past 250 runes
//     (616 here, the probe's shape): the row is RESOLVED and resolution_note_full keeps the
//     whole sanitised note (F3's pin, still holding).
//  2. StampLateCapture over that row: the row moves to VOIDING and its fold keeps the late
//     capture's own cause at the head (it fits: the fixed cause is 62 runes, the cap is 300),
//     with the manager's decision folded beside it. The fold fills the column to the cap.
//  3. The failing reversal end, StampVoidUnknown: the reversal's own cause text
//     ("void lost: status 502") must appear in last_error verbatim, and the column stays
//     within the house cap.
//
// On the tip before the fix the third pin fails: the append is
// left(old || '; ' || diagnostic, 300) on a column already 300 runes, so the diagnostic lands
// zero runes and the manager note's head shows where the cause should be. The fix bounds the
// KEPT PREFIX of the old text (attempts.go's failing-end stamps) so the newest story always
// lands; the empty-column case is byte identical. The whole note still lives in
// resolution_note_full throughout. Gated on HH_CORE_TEST_DATABASE_URL like the other
// composition suites; each run names its own throwaway database on the command.

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
)

func TestR15FixLeg7_LongManagerNoteKeepsTheReversalCauseOnLastError(t *testing.T) {
	pool := wiringPool(t)
	ctx := context.Background()

	// The long manager note: plain words, every word well under the opaque run's floor, so
	// Resolve's sanitiser keeps it verbatim and the pin below holds the count.
	longNote := string([]rune(strings.Repeat("manager found the charge already reversed at run payments ", 11))[:616])

	store := payment.NewStore(pool, nil)
	w := plantWiringCounter(t, pool)
	a, created, err := store.Claim(ctx, payment.ClaimInput{
		LegKey: "r15-fix-leg7-" + uuidKey(t), SaleKey: uuidKey(t), LegSeq: 1, AmountCents: 3400,
		BranchID: w.branchID, TillSessionID: w.sessionID, CardType: "CREDIT", Processor: "run_payments",
	})
	if err != nil || !created {
		t.Fatalf("claim: created=%v err=%v", created, err)
	}

	if _, ok, err := store.StampUnknown(ctx, a.ID, "charge answer lost"); err != nil || !ok {
		t.Fatalf("StampUnknown: ok=%v err=%v", ok, err)
	}
	resolved, ok, err := store.Resolve(ctx, a.ID, payment.ResolutionNotCharged, longNote, w.userID)
	if err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}
	if resolved.Resolution != payment.ResolutionNotCharged {
		t.Fatalf("resolved row carries resolution %q, want NOT_CHARGED", resolved.Resolution)
	}
	if got := utf8.RuneCountInString(resolved.ResolutionNoteFull); got != 616 {
		t.Fatalf("resolution_note_full kept %d runes, want the whole 616 (F3 must keep holding)", got)
	}

	// The late capture over the manager's close: the fold keeps the late capture's cause at
	// the head and fills the column to the cap with the manager's decision.
	late, ok, err := store.StampLateCapture(ctx, a.ID, "late-trans-fix7", "00")
	if err != nil || !ok {
		t.Fatalf("StampLateCapture: ok=%v err=%v", ok, err)
	}
	if late.Status != payment.AttemptVoiding {
		t.Fatalf("late capture row status = %s, want VOIDING", late.Status)
	}
	const lateCause = "capture answered after the attempt left CHARGING, reversing it"
	if !strings.HasPrefix(late.LastError, lateCause) {
		t.Fatalf("the fold lost the late capture's own cause at the head: %q", late.LastError)
	}
	if n := utf8.RuneCountInString(late.LastError); n != 300 {
		t.Fatalf("the fold left %d runes on last_error, want the fold at the cap (the red shape's premise)", n)
	}

	// The failing reversal end: the reversal's own cause text must land on the row.
	stamped, ok, err := store.StampVoidUnknown(ctx, a.ID, "void lost: status 502")
	if err != nil || !ok {
		t.Fatalf("StampVoidUnknown: ok=%v err=%v", ok, err)
	}
	if stamped.Status != payment.AttemptVoidUnknown {
		t.Fatalf("failing end row status = %s, want VOID_UNKNOWN", stamped.Status)
	}
	if n := utf8.RuneCountInString(stamped.LastError); n > 300 {
		t.Fatalf("last_error carries %d runes, over the house cap", n)
	}
	if !strings.Contains(stamped.LastError, "void lost: status 502") {
		t.Fatalf("the reversal's own cause text is not on the row (the append landed nothing on a column the fold filled to the cap); last_error head: %q", late.LastError)
	}
	if got := utf8.RuneCountInString(stamped.ResolutionNoteFull); got != 616 {
		t.Fatalf("resolution_note_full lost the whole note across the failing end: %d runes", got)
	}
}
