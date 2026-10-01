//go:build demo

package demo

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
	clock := &testClock{at: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
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
	clock := &testClock{at: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
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
	clock := &testClock{at: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
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
	clock := &testClock{at: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
	l := NewAddressLimit(1, time.Minute, clock.Now)

	// Fill the map past its bound with fresh addresses, each spending its
	// allowance now.
	for i := 0; i < maxTrackedAddresses+100; i++ {
		l.Allow(fmt.Sprintf("10.1.%d.%d", i/256, i%256))
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
