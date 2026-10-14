package dispatch

// syncscope_pod_attest_test.go: the shape rules of the pod scope's typed name attestation door (Field
// CP-2), without a database. The door runs the one CapturePOD body the REST door runs, so what is pinned
// here is only the form it accepts, how the shared body's errors are sorted, that the idempotency key
// never rides the REST body, and that an unwired door refuses.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func TestIsAttestation_TheTwoFormsDoNotMix(t *testing.T) {
	if isAttestation(map[string]any{"stop_id": "x", "content_type": "image/png", "image_bytes_b64": "AA=="}) {
		t.Fatal("the photo form is not an attestation")
	}
	for _, f := range []map[string]any{
		{"stop_id": "x", "received_by": "A Name"},
		{"stop_id": "x", "affirmed": true},
		{"stop_id": "x", "received_by": "A Name", "image_bytes_b64": "AA=="},
	} {
		if !isAttestation(f) {
			t.Fatalf("%v declares the attestation legs", f)
		}
	}
	// A photo leg beside the attestation legs is refused by the attestation form, not smuggled through.
	err := attestationFields(map[string]any{"stop_id": "x", "received_by": "A Name", "image_bytes_b64": "AA=="})
	if err == nil || !errors.Is(err, sync.ErrOpRefused) {
		t.Fatalf("a mixed payload refuses: %v", err)
	}
}

func TestAttestationFields_OnlyTheDeclaredSet(t *testing.T) {
	if err := attestationFields(map[string]any{"id": "x", "stop_id": "x", "received_by": "A", "affirmed": true}); err != nil {
		t.Fatalf("the declared set is admitted: %v", err)
	}
	for _, k := range []string{"created_at", "pod_at", "pod_received_by", "status", "pod_signature_ref", "caption"} {
		if err := attestationFields(map[string]any{"received_by": "A", k: "v"}); err == nil || !errors.Is(err, sync.ErrOpRefused) {
			t.Fatalf("%q is the server's to stamp, not the payload's: %v", k, err)
		}
	}
}

func TestClassifyAttestErr_SortsTheSharedBodysErrors(t *testing.T) {
	if err := classifyAttestErr(ErrNotFound); !errors.Is(err, sync.ErrRowNotFound) {
		t.Fatalf("an unknown stop is the not found: %v", err)
	}
	for name, in := range map[string]error{
		"illegal transition": ErrIllegalTransition,
		"forbidden":          ErrForbidden,
		"unauthenticated":    ErrUnauthorized,
		"bad request":        httpx.BadRequestf("dispatch: type the full name of the person receiving this delivery"),
	} {
		out := classifyAttestErr(in)
		if !errors.Is(out, sync.ErrOpRefused) {
			t.Fatalf("%s is the op's refusal: %v", name, out)
		}
	}
	if out := classifyAttestErr(httpx.BadRequestf("dispatch: type the full name of the person receiving this delivery")); !strings.Contains(out.Error(), "full name") {
		t.Fatalf("the direct door's words travel: %v", out)
	}
	for name, in := range map[string]error{
		"no signature provider": ErrNoSignatureProvider,
		"a database fault":      errors.New("connection reset"),
	} {
		out := classifyAttestErr(in)
		if errors.Is(out, sync.ErrOpRefused) || errors.Is(out, sync.ErrRowNotFound) {
			t.Fatalf("%s is an operational fault the push fails on, not a verdict on the op: %v", name, out)
		}
	}
}

func TestPODInput_TheIdempotencyKeyNeverRidesTheRESTBody(t *testing.T) {
	raw, err := json.Marshal(PODInput{ReceivedBy: "A Name", Affirmed: true, IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "k\"") && strings.Contains(string(raw), "dempotency") {
		t.Fatalf("the key must not be serialised: %s", raw)
	}
	var in PODInput
	if err := json.Unmarshal([]byte(`{"received_by":"A","affirmed":true,"IdempotencyKey":"forged","idempotency_key":"forged"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.IdempotencyKey != "" {
		t.Fatalf("a request body cannot set the sync key: %q", in.IdempotencyKey)
	}
}

func TestAttestationDoorRefusesUnwired(t *testing.T) {
	s := NewPODSyncStore(nil, nil)
	if s.AttestDoorWired() {
		t.Fatal("no seams, not wired")
	}
	_, err := s.Create(context.Background(), sync.ScopeFilter{OrgID: "00000000-0000-4000-8000-0000000000aa", Actor: "sub"},
		"00000000-0000-4000-8000-0000000000cc",
		map[string]any{"stop_id": "00000000-0000-4000-8000-0000000000bb", "received_by": "A Name", "affirmed": true})
	if err == nil || !errors.Is(err, sync.ErrOpRefused) || !strings.Contains(err.Error(), "attestation") {
		t.Fatalf("an unwired door refuses and says which door: %v", err)
	}
}
