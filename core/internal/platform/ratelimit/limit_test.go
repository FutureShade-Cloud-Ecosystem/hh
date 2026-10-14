package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

// testClock is a controllable clock for the limit's window.
type testClock struct{ at time.Time }

func (c *testClock) Now() time.Time { return c.at }

func (c *testClock) advance(d time.Duration) { c.at = c.at.Add(d) }

// The allowance holds: the first admissions from an address pass, the one
// past the allowance is refused, and a refusal spends nothing, so the
// count does not move while the burst hammers.
func TestAddressLimitRefusesTheBurst(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	l := NewAddressLimit(3, time.Minute, clock.Now)

	for i := 0; i < 3; i++ {
		if !l.Allow("10.0.0.1") {
			t.Fatalf("admission %d refused, want it inside the allowance", i+1)
		}
	}
	for i := 0; i < 5; i++ {
		if l.Allow("10.0.0.1") {
			t.Fatalf("refused admission %d passed, the allowance is spent", i+1)
		}
	}
	if got := len(l.entries["10.0.0.1"]); got != 3 {
		t.Errorf("the address holds %d recorded admissions, want the allowance's 3: a refusal records nothing", got)
	}
}

// The window slides: once every recorded admission sits outside it, the
// address is admitted again.
func TestAddressLimitReopensAfterTheWindow(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	l := NewAddressLimit(2, 30*time.Second, clock.Now)

	if !l.Allow("10.0.0.1") || !l.Allow("10.0.0.1") {
		t.Fatal("the first two admissions were refused, want them inside the allowance")
	}
	if l.Allow("10.0.0.1") {
		t.Fatal("the allowance was not spent")
	}
	clock.advance(29 * time.Second)
	if l.Allow("10.0.0.1") {
		t.Fatal("an admission inside the window passed while the allowance was spent")
	}
	clock.advance(2 * time.Second)
	if !l.Allow("10.0.0.1") {
		t.Fatal("the window passed and the address is still refused")
	}
}

// Addresses are independent: one address's burst never spends another
// address's allowance.
func TestAddressLimitKeepsAddressesApart(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	l := NewAddressLimit(1, time.Minute, clock.Now)

	if !l.Allow("10.0.0.1") {
		t.Fatal("the first address's first admission was refused")
	}
	if l.Allow("10.0.0.1") {
		t.Fatal("the first address's second admission passed a spent allowance")
	}
	if !l.Allow("10.0.0.2") {
		t.Fatal("the second address was refused for the first address's burst")
	}
}

// The map is bounded: a flood of distinct addresses cannot grow it
// without end, and the sweep prefers the addresses the window has
// already forgotten.
func TestAddressLimitBoundsItsMap(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	l := NewAddressLimit(1, time.Minute, clock.Now)

	// Fill the map past its bound with fresh addresses, each spending its
	// allowance now. The bound must hold after every admission, not only
	// at the end: the sweep's batching to a low-water mark keeps the
	// eviction amortised, one pass per batch of additions rather than one
	// pass per addition.
	for i := 0; i < maxTrackedAddresses+100; i++ {
		l.Allow(fmt.Sprintf("10.1.%d.%d", i/256, i%256))
		if len(l.entries) > maxTrackedAddresses {
			t.Fatalf("the map reached %d addresses during the flood, want it held at the bound %d",
				len(l.entries), maxTrackedAddresses)
		}
	}
	if len(l.entries) > maxTrackedAddresses {
		t.Fatalf("the map holds %d addresses, want it held at %d", len(l.entries), maxTrackedAddresses)
	}

	// Age everything out, then fill again with fresh addresses: the sweep
	// must take the forgotten ones first, and the map still ends bounded.
	clock.advance(2 * time.Minute)
	for i := 0; i < maxTrackedAddresses+100; i++ {
		l.Allow(fmt.Sprintf("10.2.%d.%d", i/256, i%256))
	}
	if len(l.entries) > maxTrackedAddresses {
		t.Fatalf("after a second flood the map holds %d addresses, want it held at %d", len(l.entries), maxTrackedAddresses)
	}
}

// A nil allowance, window or clock selects the defaults, so a wiring that
// names none still gets a bounded limit.
func TestAddressLimitDefaults(t *testing.T) {
	l := NewAddressLimit(0, 0, nil)
	if l.allow != DefaultBurstAllowance || l.window != DefaultBurstWindow {
		t.Fatalf("defaults are %d and %v, want the package's", l.allow, l.window)
	}
	for i := 0; i < DefaultBurstAllowance; i++ {
		if !l.Allow("10.0.0.9") {
			t.Fatalf("admission %d refused under the defaults", i+1)
		}
	}
	if l.Allow("10.0.0.9") {
		t.Fatal("the default allowance was not spent")
	}
}

// Peek is a question, not a spend: asked twice in a row it answers the
// same thing both times, and the stored stamps come out unchanged, stamp
// for stamp, after any number of peeks across a partial slide of the
// window. Where the peek compacted the stored slice in place without
// storing the shortened slice back, the expired positions kept copies of
// live ones: the second peek read the phantoms and flipped its answer.
func TestAddressLimitPeekAnswersTwiceAlikeAndStoresNothing(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	l := NewAddressLimit(3, 30*time.Second, clock.Now)

	for _, step := range []time.Duration{0, 10 * time.Second, 10 * time.Second} {
		clock.advance(step)
		if !l.Allow("10.0.0.1") {
			t.Fatalf("the admission at %v was refused, want it recorded", clock.at)
		}
	}
	before := append([]time.Time(nil), l.entries["10.0.0.1"]...)

	clock.advance(15 * time.Second)
	// The clock at +35: the 0 stamp has slid out, two stamps are live
	// under the allowance of three.
	if first, second := l.Peek("10.0.0.1"), l.Peek("10.0.0.1"); first != second {
		t.Fatalf("two peeks in a row answered %v then %v, want the same answer twice", first, second)
	}
	if !l.Peek("10.0.0.1") {
		t.Fatal("the peek answered false with two live stamps under an allowance of three")
	}
	clock.advance(6 * time.Second)
	if !l.Peek("10.0.0.1") || !l.Peek("10.0.0.1") {
		t.Fatal("a peek across the partial slide changed its answer or refused outright")
	}

	after := l.entries["10.0.0.1"]
	if len(after) != len(before) {
		t.Fatalf("the stored stamps went from %d to %d across the peeks, want them untouched", len(before), len(after))
	}
	for i := range before {
		if !before[i].Equal(after[i]) {
			t.Fatalf("stamp %d reads %v after the peeks, want it unchanged at %v", i, after[i], before[i])
		}
	}

	// The peeks spent nothing, so the allowance still takes a full
	// admission through Allow, whose compaction stores itself back.
	clock.advance(6 * time.Second)
	if !l.Allow("10.0.0.1") {
		t.Fatal("Allow refused after the peeks, want the admission: the peeks must have spent nothing")
	}
}
