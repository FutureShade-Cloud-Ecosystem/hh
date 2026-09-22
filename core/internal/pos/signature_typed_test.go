package pos

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// plainSealer is a transparent Sealer: it returns the payload verbatim so a unit test can read what
// WOULD be encrypted without pulling in the payment vault (the integration test uses the real one).
type plainSealer struct{}

func (plainSealer) Encrypt(pt []byte) ([]byte, int, error) { return pt, 7, nil }
func (plainSealer) Decrypt(ct []byte, _ int) ([]byte, error) {
	return ct, nil
}

func typedProvider(t *testing.T) (*TypedNameSignatureProvider, *fakeStore) {
	t.Helper()
	var tr []string
	store := newFakeStore(&tr)
	p := NewTypedNameSignatureProvider(plainSealer{})
	p.bind(store, nil, nil)
	return p, store
}

func typedCtx() context.Context {
	return identity.ContextWithStaffUserID(context.Background(), "00000000-0000-4000-8000-0000000000aa")
}

func okAttestation() TypedAttestation {
	return TypedAttestation{
		Purpose: "ORDER_SIGNOFF", RefType: "order", RefID: "00000000-0000-4000-8000-000000000001",
		TypedName: "Ryan Rood", Affirmed: true,
	}
}

// DIB-386, THE GUARD. The affirmative is a REQUIRED SEPARATE ACT, not an implied consequence of
// submitting the form (the DIB-57 ruling). An attestation someone could produce by accident is not
// an attestation. Bypassing the `!in.Affirmed` refusal makes this test fail.
func TestTypedName_UnaffirmedIsRefusedAndWritesNothing(t *testing.T) {
	p, store := typedProvider(t)
	in := okAttestation()
	in.Affirmed = false
	ref, err := p.CaptureTyped(typedCtx(), in)
	if err == nil {
		t.Fatal("an attestation without the explicit affirmative must be refused")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 400 {
		t.Fatalf("refusal must be a 400 the operator can act on, got %v", err)
	}
	if ref != "" {
		t.Fatalf("a refused capture must mint no ref, got %q", ref)
	}
	if len(store.signatures) != 0 {
		t.Fatalf("a refused capture must leave NO row behind, got %d", len(store.signatures))
	}
}

// A blank (or whitespace-only) name is not a signature. Same posture: refuse before anything is
// sealed or written.
func TestTypedName_EmptyNameIsRefusedAndWritesNothing(t *testing.T) {
	p, store := typedProvider(t)
	for _, name := range []string{"", "   ", "\t\n"} {
		in := okAttestation()
		in.TypedName = name
		if _, err := p.CaptureTyped(typedCtx(), in); err == nil {
			t.Fatalf("a blank typed name (%q) must be refused", name)
		}
	}
	in := okAttestation()
	in.TypedName = strings.Repeat("a", maxTypedNameLen+1)
	if _, err := p.CaptureTyped(typedCtx(), in); err == nil {
		t.Fatal("an over-long name must be refused")
	}
	if len(store.signatures) != 0 {
		t.Fatalf("no refused capture may write a row, got %d", len(store.signatures))
	}
}

// The subject and the purpose are part of the evidence: an attestation that names nothing, or that
// carries a purpose the schema does not know, is refused in Go rather than by a constraint name.
func TestTypedName_PurposeAndSubjectAreRequired(t *testing.T) {
	p, _ := typedProvider(t)
	in := okAttestation()
	in.Purpose = "VIBES"
	if _, err := p.CaptureTyped(typedCtx(), in); err == nil {
		t.Fatal("an unknown purpose must be refused")
	}
	in = okAttestation()
	in.RefID = "  "
	if _, err := p.CaptureTyped(typedCtx(), in); err == nil {
		t.Fatal("an attestation must name what it is signing")
	}
}

// DIB-386, the payload is the REAL captured act, and the provider string tells the truth. The stub
// sealed "SIMULATED:<purpose>:<ref>" and stamped SIMULATED; nothing of that survives.
func TestTypedName_SealsTheRealPayloadAndStampsTheProvider(t *testing.T) {
	p, store := typedProvider(t)
	in := okAttestation()
	in.TypedName = "  Ryan Rood  " // trimmed on the way in
	ref, err := p.CaptureTyped(typedCtx(), in)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.HasPrefix(ref, "sig_") {
		t.Fatalf("ref shape changed: %q", ref)
	}
	if len(store.signatures) != 1 {
		t.Fatalf("exactly one row must be written, got %d", len(store.signatures))
	}
	row := store.signatures[0]
	if row.Provider != ProviderTypedNameV1 {
		t.Fatalf("provider = %q, want %q, the row must report what actually happened", row.Provider, ProviderTypedNameV1)
	}
	if row.KeyVersion != 7 {
		t.Fatalf("the sealer's key version must be carried, got %d", row.KeyVersion)
	}
	if strings.Contains(string(row.SignatureEnc), "SIMULATED") {
		t.Fatal("the simulated placeholder payload must be gone")
	}
	att, err := DecodeAttestation(row.SignatureEnc)
	if err != nil {
		t.Fatalf("sealed payload must decode: %v", err)
	}
	if att.TypedName != "Ryan Rood" {
		t.Fatalf("typed name = %q, want the trimmed typed name", att.TypedName)
	}
	if !att.Affirmed || att.Version != ProviderTypedNameV1 {
		t.Fatalf("payload must carry the affirmative + the version: %+v", att)
	}
	if att.Purpose != in.Purpose || att.RefType != in.RefType || att.RefID != in.RefID {
		t.Fatalf("payload must carry what was signed: %+v", att)
	}
	if att.ActorID != "00000000-0000-4000-8000-0000000000aa" {
		t.Fatalf("payload must carry the witnessing staff actor, got %q", att.ActorID)
	}
	if att.CapturedAt.IsZero() {
		t.Fatal("payload must carry when it was captured")
	}
	if att.Ref != ref {
		t.Fatalf("payload ref %q must match the returned ref %q", att.Ref, ref)
	}
}

// The un-attested SignatureProvider.Capture seam cannot produce a typed-name attestation, nobody
// typed anything, so it refuses instead of fabricating one. That refusal IS the DIB-386 fix.
func TestTypedName_BareCaptureSeamRefuses(t *testing.T) {
	p, store := typedProvider(t)
	if _, err := p.Capture(typedCtx(), "ORDER_SIGNOFF", "order", "00000000-0000-4000-8000-000000000001"); err == nil {
		t.Fatal("the un-attested capture seam must refuse on a typed-name provider")
	}
	if len(store.signatures) != 0 {
		t.Fatalf("the refused seam must write nothing, got %d", len(store.signatures))
	}
}

// The service entry point binds the A-19-internal deps lazily (main.go wires the provider with the
// vault only) and refuses honestly when the wired provider cannot take an attestation.
func TestCaptureTypedSignature_ServiceSeam(t *testing.T) {
	h := newHarness()
	h.svc.WithSignatureProvider(NewTypedNameSignatureProvider(plainSealer{}))
	ref, err := h.svc.CaptureTypedSignature(typedCtx(), okAttestation())
	if err != nil {
		t.Fatalf("typed capture through the service: %v", err)
	}
	if ref == "" {
		t.Fatal("a captured attestation must return its ref")
	}
	if len(h.store.signatures) != 1 {
		t.Fatalf("the service must bind the store so the row lands, got %d", len(h.store.signatures))
	}

	// The old stub cannot take an attestation, say so, do not silently downgrade to a fake capture.
	h2 := newHarness()
	h2.svc.WithSignatureProvider(NewSimulatedSignatureProvider(plainSealer{}))
	if _, err := h2.svc.CaptureTypedSignature(typedCtx(), okAttestation()); !errors.Is(err, errNoTypedProvider) {
		t.Fatalf("a non-typed provider must refuse honestly, got %v", err)
	}
	if len(h2.store.signatures) != 0 {
		t.Fatalf("the refusal must write nothing, got %d", len(h2.store.signatures))
	}
}

// The audit row indexes the capture WITHOUT copying the sealed evidence: audit_log is the
// who/what/when index, not a second, weaker home for the typed name.
func TestTypedName_AuditRowCarriesNoTypedName(t *testing.T) {
	var tr []string
	store := newFakeStore(&tr)
	rec := &captureRecorder{}
	p := NewTypedNameSignatureProvider(plainSealer{})
	p.bind(store, rec, nil)
	if _, err := p.CaptureTyped(typedCtx(), okAttestation()); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("the capture must be audited exactly once, got %d", len(rec.entries))
	}
	e := rec.entries[0]
	if e.Action != audit.ActSignatureCapture {
		t.Fatalf("action = %q", e.Action)
	}
	blob, _ := json.Marshal(e)
	if strings.Contains(string(blob), "Ryan Rood") {
		t.Fatal("the typed name must NOT be copied onto the audit row, it lives sealed, once")
	}
}

// captureRecorder collects audit rows.
type captureRecorder struct{ entries []audit.SensitiveAccess }

func (r *captureRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.entries = append(r.entries, a)
	return nil
}
