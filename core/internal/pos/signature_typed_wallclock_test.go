// signature_typed_wallclock_test.go, the default the fix must not disturb: a service that never had
// WithClock called (NewService wires now to the wall clock) keeps stamping captures with the wall
// clock, on both capture paths. The assertion brackets the capture between two real clock reads and
// demands the sealed CapturedAt land inside the bracket, which a pinned or absent clock fails. This
// file never pins a calendar instant (the pinned-clock tests live in signature_typed_clock_test.go,
// a separate file so a pinned instant and the wall clock never share a file).
package pos

import (
	"context"
	"testing"
	"time"
)

func TestTypedName_WithoutWithClockTheWallClockStands(t *testing.T) {
	var tr []string
	// The NewService default, mirrored: now is the wall clock and no WithClock was called. The
	// recorder is left unwired, neither path's semantics turn on it here.
	sealer := &recordingSealer{}
	svc := &Service{
		store: newFakeStore(&tr),
		sig:   NewTypedNameSignatureProvider(sealer),
		now:   time.Now,
	}
	tx := &sealedCaptureTx{}

	before := time.Now().UTC()
	if _, err := svc.CaptureTypedSignature(context.Background(), okAttestation()); err != nil {
		t.Fatalf("pool capture: %v", err)
	}
	after := time.Now().UTC()
	att, err := DecodeAttestation(svc.store.(*fakeStore).signatures[0].SignatureEnc)
	if err != nil {
		t.Fatalf("sealed payload must decode: %v", err)
	}
	if att.CapturedAt.Before(before) || att.CapturedAt.After(after) {
		t.Fatalf("the pool capture's sealed CapturedAt = %v, want the wall clock inside [%v, %v]", att.CapturedAt, before, after)
	}

	before = time.Now().UTC()
	if _, err := svc.CaptureTypedSignatureTx(context.Background(), tx, okAttestation()); err != nil {
		t.Fatalf("tx capture: %v", err)
	}
	after = time.Now().UTC()
	att, err = DecodeAttestation(sealer.last)
	if err != nil {
		t.Fatalf("sealed payload must decode: %v", err)
	}
	if att.CapturedAt.Before(before) || att.CapturedAt.After(after) {
		t.Fatalf("the tx capture's sealed CapturedAt = %v, want the wall clock inside [%v, %v]", att.CapturedAt, before, after)
	}
}
