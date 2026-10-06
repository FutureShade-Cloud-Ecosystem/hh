// signature_typed_clock_test.go, the service clock's reach: WithClock must govern CapturedAt in the
// SEALED attestation on BOTH capture paths, the pool capture and CaptureTypedTx. The mechanism under
// test is the lazy bind: captureTypedProvider hands the service's s.now to the provider at first
// capture, and sealTypedAttestation stamps the payload with whatever the provider holds. This file
// pins the clock and never reads a real one (the wall-clock default lives in
// signature_typed_wallclock_test.go, a separate file so a pinned instant and the wall clock never
// share a file).
package pos

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// attestationClock is the instant WithClock injects: a far-future sentinel no real capture can ever
// carry, so a wall-clock stamp can never pass for it.
var attestationClock = time.Date(2031, 4, 2, 9, 30, 0, 0, time.UTC)

// clockedService is a harness service whose signature provider is the real typed-name provider and
// whose clock went through WithClock, the seam a test uses to pin time. The recorder rides
// transactions so the tx path is wired the way the dispatch pickups confirm is required.
func clockedService() (*Service, *fakeStore, *recordingSealer) {
	h := newHarness()
	sealer := &recordingSealer{}
	h.svc = h.svc.WithSignatureProvider(NewTypedNameSignatureProvider(sealer))
	h.svc = h.svc.WithClock(func() time.Time { return attestationClock })
	h.svc = h.svc.WithAudit(&recordingTxAudit{})
	return h.svc, h.store, sealer
}

// recordingSealer is a transparent Sealer that remembers the last payload it was asked to seal. The
// tx path's persistence goes through whichever seam the tree wires (a statement on the transaction,
// or the store's InsertSignatureTx once the shared insert lands), so these tests read the sealed
// payload from the SEALER, the one point both persistence shapes pass through, and stay independent
// of how the row is written.
type recordingSealer struct {
	plainSealer
	last []byte
}

func (r *recordingSealer) Encrypt(pt []byte) ([]byte, int, error) {
	r.last = append([]byte(nil), pt...)
	return r.plainSealer.Encrypt(pt)
}

// sealedCaptureTx is the transaction handle the tx-path tests pass: a stub whose QueryRow answers the
// signature insert's scan with an empty row, whichever seam issues the insert.
type sealedCaptureTx struct {
	pgx.Tx
}

func (tx *sealedCaptureTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return captureRow{}
}

// The pool capture: WithClock's clock, not the wall clock, is what the sealed attestation carries.
func TestWithClock_GovernsTypedAttestationOnThePoolPath(t *testing.T) {
	svc, store, _ := clockedService()
	if _, err := svc.CaptureTypedSignature(typedCtx(), okAttestation()); err != nil {
		t.Fatalf("pool capture: %v", err)
	}
	if len(store.signatures) != 1 {
		t.Fatalf("exactly one row must be written, got %d", len(store.signatures))
	}
	att, err := DecodeAttestation(store.signatures[0].SignatureEnc)
	if err != nil {
		t.Fatalf("sealed payload must decode: %v", err)
	}
	if !att.CapturedAt.Equal(attestationClock) {
		t.Fatalf("the pool capture's sealed CapturedAt = %v, want the WithClock clock %v: the service clock never reached the typed provider", att.CapturedAt, attestationClock)
	}
}

// The tx capture: same clock, same payload rule, riding the caller's transaction.
func TestWithClock_GovernsTypedAttestationOnTheTxPath(t *testing.T) {
	svc, _, sealer := clockedService()
	tx := &sealedCaptureTx{}
	if _, err := svc.CaptureTypedSignatureTx(typedCtx(), tx, okAttestation()); err != nil {
		t.Fatalf("tx capture: %v", err)
	}
	if sealer.last == nil {
		t.Fatal("the tx capture must seal a payload")
	}
	att, err := DecodeAttestation(sealer.last)
	if err != nil {
		t.Fatalf("sealed payload must decode: %v", err)
	}
	if !att.CapturedAt.Equal(attestationClock) {
		t.Fatalf("the tx capture's sealed CapturedAt = %v, want the WithClock clock %v: the service clock never reached the typed provider", att.CapturedAt, attestationClock)
	}
}
