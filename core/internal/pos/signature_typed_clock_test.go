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
func clockedService() (*Service, *fakeStore) {
	h := newHarness()
	h.svc = h.svc.WithSignatureProvider(NewTypedNameSignatureProvider(plainSealer{}))
	h.svc = h.svc.WithClock(func() time.Time { return attestationClock })
	h.svc = h.svc.WithAudit(&recordingTxAudit{})
	return h.svc, h.store
}

// sealedCaptureTx is the transaction handle the tx-path test passes. The tx capture's insert carries
// the sealed payload as its fourth argument; the fake row answers the scan and the handle keeps the
// payload so the test can read what would have landed in the row, without a database.
type sealedCaptureTx struct {
	pgx.Tx
	sealed []byte
}

func (tx *sealedCaptureTx) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if enc, ok := args[3].([]byte); ok {
		tx.sealed = enc
	}
	return captureRow{}
}

// The pool capture: WithClock's clock, not the wall clock, is what the sealed attestation carries.
func TestWithClock_GovernsTypedAttestationOnThePoolPath(t *testing.T) {
	svc, store := clockedService()
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
	svc, _ := clockedService()
	tx := &sealedCaptureTx{}
	if _, err := svc.CaptureTypedSignatureTx(typedCtx(), tx, okAttestation()); err != nil {
		t.Fatalf("tx capture: %v", err)
	}
	if tx.sealed == nil {
		t.Fatal("the tx capture must seal a payload onto the caller's transaction")
	}
	att, err := DecodeAttestation(tx.sealed)
	if err != nil {
		t.Fatalf("sealed payload must decode: %v", err)
	}
	if !att.CapturedAt.Equal(attestationClock) {
		t.Fatalf("the tx capture's sealed CapturedAt = %v, want the WithClock clock %v: the service clock never reached the typed provider", att.CapturedAt, attestationClock)
	}
}
