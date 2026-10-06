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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

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

func (s *raceStore) InsertSignatureTx(ctx context.Context, tx pgx.Tx, sig signatureRow) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.InsertSignatureTx(ctx, tx, sig)
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
	if got := len(store.signatures); got != 2*raceContenders {
		t.Fatalf("every capture must land one row (pool and tx), got %d rows, want %d", got, 2*raceContenders)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.records != raceContenders || rec.txWrites != raceContenders {
		t.Fatalf("the trail must carry %d pool rows and %d tx rows, got %d and %d",
			raceContenders, raceContenders, rec.records, rec.txWrites)
	}
}

// TestTypedNameProvider_ReadsRaceTheBindUnderRepeatedFills pins the READ side of the guard, which the
// first-capture leg cannot see (there the service's locked bind orders every later read). A provider
// bound with a nil audit recorder has bind rewrite that nil on every call (the fill is "only when
// nil"), so a capture reading the audit field, or the clock, while another goroutine binds again is
// a write racing a read. The leg drives the provider DIRECTLY: half the goroutines call bind with a
// nil recorder, half capture on both paths. Red under -race when the capture paths read the provider's
// fields without the lock (the snapshot read); green with the guard.
func TestTypedNameProvider_ReadsRaceTheBindUnderRepeatedFills(t *testing.T) {
	h := newHarness()
	store := &raceStore{fakeStore: h.store}
	p := NewTypedNameSignatureProvider(plainSealer{})
	p.bind(store, nil, nil) // bound once with a nil recorder and no clock: bind keeps rewriting those nils
	var wg sync.WaitGroup
	for i := 0; i < raceContenders; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				p.bind(store, nil, nil)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = p.CaptureTyped(typedCtx(), okAttestation())
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = p.CaptureTypedTx(typedCtx(), &sealedCaptureTx{}, okAttestation())
			}
		}()
	}
	wg.Wait()
}

// TestTypedNameProvider_NilStoreRebindsRaceTheBoundRefusals pins the bind lock on the store field
// itself, the field the round 1 review's P1 turned the tx capture's insert through (the row 834
// finding: with the tx capture reading the live store field again, no leg went red, so the closed
// behavior was unpinned). The trick is the fill's own "only when nil" rule: bound with a NIL store,
// every re-bind rewrites p.store (nil over nil is still a write), so the race phase carries a
// standing stream of store-field rewrites against both capture paths' snapshot reads. On the locked
// tree every rewrite takes the same mutex the reads snapshot under and the leg is green; with the
// bind's lock missing (the round 1 closed shape, and the shape main still ships) the rewrites are
// unsynchronized writes racing both capture paths and the race detector fails the run.
//
// The leg also pins the exact outcome shape of an unbound provider, asserted after the race phase:
// the tx capture refuses with the not-bound 500 BEFORE it touches any store (no ref, no trail row,
// and therefore no pool acquisition of any kind, the row 786 shape), and the pool capture answers
// honestly for an unbound capture, a ref and a trail row each, with nothing persisted anywhere.
func TestTypedNameProvider_NilStoreRebindsRaceTheBoundRefusals(t *testing.T) {
	rec := &raceRecorder{}
	p := NewTypedNameSignatureProvider(plainSealer{})
	p.bind(nil, rec, nil) // bound with a NIL store: bind keeps rewriting that nil on every re-bind

	// The same barrier the first-capture leg uses: every goroutine parks at the line first, so the
	// re-binds and the capture-path reads overlap instead of queuing behind one another.
	start := make(chan struct{})
	loops := 20
	poolCount := raceContenders * loops
	var wg sync.WaitGroup
	refs := make([]string, poolCount)
	poolErrs := make([]error, poolCount)
	txErrs := make([]error, poolCount)
	for i := 0; i < raceContenders; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				p.bind(nil, rec, nil)
			}
		}()
		go func(k int) {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				refs[k*loops+j], poolErrs[k*loops+j] = p.CaptureTyped(typedCtx(), okAttestation())
			}
		}(i)
		go func(k int) {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				_, txErrs[k*loops+j] = p.CaptureTypedTx(typedCtx(), &sealedCaptureTx{}, okAttestation())
			}
		}(i)
	}
	close(start)
	wg.Wait()

	// The tx capture on an unbound provider must refuse with the not-bound 500, mint no ref and
	// write no trail row: the refusal runs before any store touch, so a capture inside a
	// transaction can never answer success for evidence that persisted nowhere.
	for i, err := range txErrs {
		if err == nil {
			t.Fatalf("tx capture %d on an unbound provider must be refused, got success", i)
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusInternalServerError {
			t.Fatalf("tx capture %d refusal must be the not-bound 500, got %v", i, err)
		}
		if !strings.Contains(ce.msg, "not bound to a store") {
			t.Fatalf("tx capture %d refusal must name the missing store, got %q", i, ce.msg)
		}
	}
	// The pool capture on an unbound provider must answer with its own ref and land its trail row,
	// the unbound semantics the pool path has always carried, now pinned against the rewrite storm.
	seen := map[string]bool{}
	for i, ref := range refs {
		if poolErrs[i] != nil {
			t.Fatalf("pool capture %d on an unbound provider must answer, got %v", i, poolErrs[i])
		}
		if ref == "" {
			t.Fatalf("pool capture %d must return its ref", i)
		}
		if seen[ref] {
			t.Fatalf("every pool capture mints its own ref, %q came back twice", ref)
		}
		seen[ref] = true
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.records != poolCount {
		t.Fatalf("the trail must carry one row per pool capture, got %d, want %d", rec.records, poolCount)
	}
	if rec.txWrites != 0 {
		t.Fatalf("a refused tx capture must write no trail row, got %d", rec.txWrites)
	}
}
