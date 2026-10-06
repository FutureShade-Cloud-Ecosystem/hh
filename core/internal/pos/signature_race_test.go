// signature_race_test.go, the simulated provider's race leg (row 834). The service binds the
// simulated provider's persistence deps lazily at EVERY capture (CaptureSignature), exactly the
// shape the typed provider's first-capture leg covers, and the round 1 review's P3 recorded the
// same race on this stand-in: bind fills store and audit unlocked, and Capture reads both fields
// unlocked. The leg fires the concurrent submit through the REAL service and stand-in (locked
// fakes, no database): two legs bind at the same moment, the stand-in's bind counter must read
// exactly 1 (one bind won the fill, the other waited behind it and must not re-run the fill, the
// capture twice shape), and both captures still answer their own ref and land their own row and
// trail row, because serialization must not change the simulator's honest semantics. Red on the
// unlocked stand-in, twice over: the race detector reports the fill racing the capture reads, and
// the counter reads 2 under the concurrent submit. Green with the lock.
package pos

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// simSubmitRounds is how many fresh stand-ins the concurrent submit runs over. Each round gives
// the two submissions a fresh provider (an unbound one: only a first fill can count), so the red
// does not rest on one scheduler coin flip: an unlocked round can double fill, the rewrites race,
// and one double fill anywhere fails the round on the counter.
const simSubmitRounds = 75

func TestSimulatedProvider_ConcurrentSubmitsFillExactlyOnce(t *testing.T) {
	h := newHarness()
	store := &raceStore{fakeStore: h.store}
	rec := &raceRecorder{}
	h.svc.store = store
	h.svc.audit = rec
	for round := 0; round < simSubmitRounds; round++ {
		// Constructed bare, not through the constructor: with the wall clock left unbound too, the
		// lazy fill covers all three fields (store, audit and clock), which is the widest honest
		// window for the double fill the counter watches. The service still drives every bind.
		h.svc = h.svc.WithSignatureProvider(&SimulatedSignatureProvider{vault: plainSealer{}})
		provider := h.svc.sig.(*SimulatedSignatureProvider)
		// A spin barrier, not a channel close: the channel wakes its waiters far enough apart that
		// the first bind is done before the second bind's nil check runs, which would let an
		// unlocked double fill hide behind wakeup spacing forever. Spinning to a shared deadline
		// releases both submissions within one clock read of each other, so on the unlocked stand-in
		// both nil checks really can run before either fill lands.
		goTime := time.Now().Add(200 * time.Microsecond)
		start := make(chan struct{})
		errs := make([]error, 2)
		refs := make([]string, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func(i int) {
				defer wg.Done()
				<-start
				for time.Now().Before(goTime) {
				}
				refs[i], errs[i] = h.svc.CaptureSignature(typedCtx(), "CARD_PRESENT", "order",
					fmt.Sprintf("33333333-3333-4333-8333-%012d", round*2+i))
			}(i)
		}
		close(start)
		wg.Wait()

		// The serialization pin, the round's own count reply: two concurrent submits run two lazy
		// binds, and exactly one of them may run the fill. The loser waits, sees the winner's
		// fields and changes nothing; a count of 2 is two fills, the un-serialized shape.
		if got := provider.bindFills.Load(); got != 1 {
			t.Fatalf("round %d: the stand-in's bind counter reads %d under the concurrent submit, want 1: two first captures both ran the fill, the simulator does not lock", round, got)
		}
		for i := range errs {
			if errs[i] != nil {
				t.Fatalf("round %d: concurrent submit %d must succeed, got %v", round, i, errs[i])
			}
			if refs[i] == "" {
				t.Fatalf("round %d: concurrent submit %d must return its ref", round, i)
			}
		}
		if refs[0] == refs[1] {
			t.Fatalf("round %d: the two submits minted the same ref %q", round, refs[0])
		}
	}
	// Both captures of every round persist honestly: one row and one trail row each, two per
	// round, so the lock the fix adds must never swallow a capture.
	if got := len(store.signatures); got != 2*simSubmitRounds {
		t.Fatalf("every submit must land one row, got %d rows, want %d", got, 2*simSubmitRounds)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.records != 2*simSubmitRounds {
		t.Fatalf("the trail must carry one row per submit, got %d, want %d", rec.records, 2*simSubmitRounds)
	}
}
