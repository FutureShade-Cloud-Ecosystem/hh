// signature_typed_race_test.go, the first-capture race leg. The service binds the typed-name
// provider's persistence deps lazily at EVERY capture (captureTypedProvider), so several captures
// starting at once fill and read the provider's store, audit and clock fields with no lock between
// them: the PR 182 review recorded the data race the Go race detector reports on both capture
// paths. This leg fires simultaneous FIRST captures through the REAL service and provider (fake
// store and recorder as the unit tests do, no database) and its red is the race detector's report,
// so it only bites under -race:
//
//	go test -race -run TestTypedNameProvider_FirstCapturesAreRaceFree ./internal/pos
//
// A run without the detector still passes: it cannot see the race, the red lives in the detector.
package pos

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// raceContenders is how many simultaneous FIRST captures each path fires: every one of them runs
// the service's lazy bind while the others are inside their capture, the overlap the detector must
// see. All of them are FIRST captures because a first capture is what binds, and binding is the
// write side of the race.
const raceContenders = 8

// raceStore wraps the harness fake and locks the one method the pool capture writes through. The
// fake itself is not safe for concurrent use, and an unsynchronized fake would report ITS own race
// here and drown the provider defect the leg exists to catch (and stay red after the fix), so the
// leg's helpers carry their own lock and the only unsynchronized accesses left on the unfixed tree
// are the provider's.
type raceStore struct {
	*fakeStore
	mu sync.Mutex
}

func (s *raceStore) InsertSignature(ctx context.Context, sig signatureRow) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.InsertSignature(ctx, sig)
}

// raceRecorder is the audit recorder both capture paths can ride (Record for the pool capture,
// RecordTx for the tx capture, which is what makes the wired recorder acceptable to CaptureTypedTx),
// locked for the same reason the store is.
type raceRecorder struct {
	mu       sync.Mutex
	records  int
	txWrites int
}

func (r *raceRecorder) Record(context.Context, audit.SensitiveAccess) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records++
	return nil
}

func (r *raceRecorder) RecordTx(_ context.Context, _ audit.Execer, _ audit.SensitiveAccess) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.txWrites++
	return nil
}

func TestTypedNameProvider_FirstCapturesAreRaceFree(t *testing.T) {
	h := newHarness()
	store := &raceStore{fakeStore: h.store}
	rec := &raceRecorder{}
	h.svc.store = store
	h.svc.audit = rec
	h.svc = h.svc.WithSignatureProvider(NewTypedNameSignatureProvider(plainSealer{}))

	// The barrier: no goroutine starts capturing until all of them are parked at the line, so the
	// binds and the capture-path reads overlap instead of queuing behind one another.
	start := make(chan struct{})
	total := 2 * raceContenders
	errs := make([]error, total)
	refs := make([]string, total)
	var wg sync.WaitGroup
	for i := 0; i < raceContenders; i++ {
		poolAt := okAttestation()
		poolAt.RefID = fmt.Sprintf("11111111-1111-4111-8111-%012d", i)
		txAt := okAttestation()
		txAt.RefID = fmt.Sprintf("22222222-2222-4222-8222-%012d", i)
		wg.Add(2)
		go func(i int, in TypedAttestation) {
			defer wg.Done()
			<-start
			refs[i], errs[i] = h.svc.CaptureTypedSignature(typedCtx(), in)
		}(i, poolAt)
		go func(i int, in TypedAttestation) {
			defer wg.Done()
			<-start
			refs[raceContenders+i], errs[raceContenders+i] = h.svc.CaptureTypedSignatureTx(typedCtx(), &sealedCaptureTx{}, in)
		}(i, txAt)
	}
	close(start)
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("first capture %d must succeed, got %v", i, errs[i])
		}
		if refs[i] == "" {
			t.Fatalf("first capture %d must return its ref", i)
		}
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			t.Fatalf("every capture mints its own ref, %q came back twice", ref)
		}
		seen[ref] = true
	}
	if got := len(store.signatures); got != raceContenders {
		t.Fatalf("the pool captures must land one row each, got %d rows", got)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.records != raceContenders || rec.txWrites != raceContenders {
		t.Fatalf("the trail must carry %d pool rows and %d tx rows, got %d and %d",
			raceContenders, raceContenders, rec.records, rec.txWrites)
	}
}
