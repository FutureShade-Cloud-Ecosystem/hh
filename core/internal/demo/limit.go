//go:build demo

// The per-address mint limit (the plan's P3-3 answer): the demo session
// route is pre-auth by design, so its one expensive, audited, credential
// shaped effect must carry its own bound. Without one, a scripted burst
// from any address writes an audit row per call and mints a token per
// call, which is precisely the shape the plan's finding names. The limit
// is the module's own, in memory, keyed by the address the connection
// arrived from: at most the allowance of mints from one address inside
// one window, whatever the flows, whatever the outcomes.
//
// The address lives here and nowhere else: it is never written to the
// database and never rendered in a response, so the limit keeps no
// record a tester's address could be read back from.
package demo

import (
	"sync"
	"time"
)

// The allowance a serve wiring gets when it names none: enough that no
// tester tapping role cards ever meets it, small enough that a scripted
// burst stops inside the first window.
const (
	DefaultBurstAllowance = 5
	DefaultBurstWindow    = 30 * time.Second
	// maxTrackedAddresses bounds the map: past it, addresses whose newest
	// attempt sits outside the window give way first, then arbitrary ones,
	// exactly the discipline the membership cache's store holds. Dropping
	// an address's history can let one extra mint through; unbounded memory
	// on a pre-auth route can let a burst take the box.
	maxTrackedAddresses = 4096
)

// AddressLimit refuses a burst of mints from one address: at most
// allowance admissions per address inside one sliding window. A refusal
// spends nothing and records nothing, so hammering does not extend a
// block: the window is the mints', not the refusals'.
type AddressLimit struct {
	mu      sync.Mutex
	allow   int
	window  time.Duration
	now     func() time.Time
	entries map[string][]time.Time
}

// NewAddressLimit returns the limit. A non positive allowance or window
// selects the defaults; a nil clock selects the wall clock. Tests drive
// both with their own values.
func NewAddressLimit(allowance int, window time.Duration, now func() time.Time) *AddressLimit {
	if allowance <= 0 {
		allowance = DefaultBurstAllowance
	}
	if window <= 0 {
		window = DefaultBurstWindow
	}
	if now == nil {
		now = time.Now
	}
	return &AddressLimit{allow: allowance, window: window, now: now, entries: map[string][]time.Time{}}
}

// Allow reports whether one more mint from the address is inside the
// allowance, recording the admission when it is.
func (l *AddressLimit) Allow(address string) bool {
	now := l.now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()

	stamps := l.live(l.entries[address], cutoff)
	if len(stamps) >= l.allow {
		l.entries[address] = stamps
		return false
	}
	l.entries[address] = append(stamps, now)
	if len(l.entries) > maxTrackedAddresses {
		l.sweep(cutoff)
	}
	return true
}

// live returns the address's admissions still inside the window, oldest
// first.
func (l *AddressLimit) live(stamps []time.Time, cutoff time.Time) []time.Time {
	keep := stamps[:0]
	for _, s := range stamps {
		if s.After(cutoff) {
			keep = append(keep, s)
		}
	}
	return keep
}

// sweep drops addresses the window has already forgotten, then arbitrary
// ones if the bound still stands, so a pre-auth route cannot grow the map
// without end. Losing an entry only ever loses a count: the worst case is
// an extra mint, never a wrong answer and never a held credential.
func (l *AddressLimit) sweep(cutoff time.Time) {
	for address, stamps := range l.entries {
		if len(stamps) == 0 || !stamps[len(stamps)-1].After(cutoff) {
			delete(l.entries, address)
		}
	}
	for address := range l.entries {
		if len(l.entries) <= maxTrackedAddresses {
			break
		}
		delete(l.entries, address)
	}
}
