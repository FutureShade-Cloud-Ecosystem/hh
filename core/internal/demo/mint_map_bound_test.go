//go:build demo

package demo

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// The mint path's own map bound legs: the session route spends its two
// buckets with Peek and Record, so the bound on either bucket's map is
// Record's sweep to keep, not Allow's. These legs drive the real handler
// past the bound, once at the per key bucket with a flood of distinct
// visitor cookies from one address, once at the address ceiling with a
// flood of distinct addresses, and hold both maps at the bound during
// the flood and after it, the same discipline the Allow leg holds on
// its own path. The driven clock keeps every stamp inside the window
// without a sleep, so the sweep is exercised only as the bound's
// keeper, not as the window's.

// floodMints is each leg's flood: one hundred admissions past the map
// bound, so only the sweep stands between the maps and the bound.
const floodMints = maxTrackedAddresses + 100

// holdBothMapsAtTheBound asserts neither bucket's map grew past the
// bound, naming the map that did when one did.
func holdBothMapsAtTheBound(t *testing.T, m *Mint, when string) {
	t.Helper()
	if got := len(m.limit.entries); got > maxTrackedAddresses {
		t.Fatalf("the per key limit's map holds %d entries %s, want it held at the bound %d", got, when, maxTrackedAddresses)
	}
	if got := len(m.ceiling.entries); got > maxTrackedAddresses {
		t.Fatalf("the address ceiling's map holds %d entries %s, want it held at the bound %d", got, when, maxTrackedAddresses)
	}
}

// The per key bucket under a flood of distinct visitor cookies from one
// address: every mint carries a fresh well formed id, so no single
// key's allowance is spent twice and the per key limit refuses nothing,
// and the ceiling rides a flood sized allowance so it refuses nothing
// either. Each admitted mint records a fresh key in the per key limit's
// map, and that map must come back to the bound after every admission
// and stay there after the flood.
func TestMintHoldsTheKeyMapAtTheBoundUnderACookieFlood(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	m, mintAs := spendMint(t, clock, 1, time.Minute, floodMints)

	for i := 0; i < floodMints; i++ {
		if rsp := mintAs(fmt.Sprintf("%032x", i)); rsp.Code != http.StatusOK {
			t.Fatalf("flood mint %d: status %d, want 200 (body: %s)", i+1, rsp.Code, rsp.Body.String())
		}
		holdBothMapsAtTheBound(t, m, "during the flood")
	}
	holdBothMapsAtTheBound(t, m, "after the flood")
}

// The address ceiling under a flood of distinct addresses: every
// request names its own peer address and carries no cookie, so the
// mint's key falls back to the address and both buckets take a fresh
// entry per mint. The per key limit rides an allowance of one, which a
// once only key never exhausts, and the ceiling rides a flood sized
// allowance, so the flood is admitted end to end. Both maps must come
// back to the bound after every admission and stay there after the
// flood.
func TestMintHoldsBothMapsAtTheBoundUnderAnAddressFlood(t *testing.T) {
	clock := &testClock{at: time.Unix(0, 0).UTC()}
	m, _ := spendMint(t, clock, 1, time.Minute, floodMints)

	for i := 0; i < floodMints; i++ {
		addr := fmt.Sprintf("10.1.%d.%d:4444", i/256, i%256)
		if rsp := postSessionFrom(t, m, `{"flow":"sales"}`, addr, nil); rsp.Code != http.StatusOK {
			t.Fatalf("flood mint %d: status %d, want 200 (body: %s)", i+1, rsp.Code, rsp.Body.String())
		}
		holdBothMapsAtTheBound(t, m, "during the flood")
	}
	holdBothMapsAtTheBound(t, m, "after the flood")
}
