// signature_typed.go, the REAL signature provider (DIB-386). It replaces the SimulatedSignatureProvider
// stub in signature.go, which sealed the literal string "SIMULATED:<purpose>:<ref>" and reported
// Provider "SIMULATED": no name, no consent, no act, a row that looks like evidence and is not.
//
// THE RULING (2026-07-30, restated for DIB-386). What gets captured is an electronic ATTESTATION: a
// typed full name plus an explicit affirmative, presented honestly as what it is. NOT a drawing pad.
// A pad implies ink nobody captured and produces a bitmap no one can verify; the DIB-57 quote
// acceptance (internal/quote/share.go AcceptByToken) already ships the honest version of this exact
// act, and this is deliberately the same shape so the two attestations in this system read alike.
//
// The SignatureProvider seam is UNCHANGED, a Run terminal or a third-party pad still slots in behind
// it later. What changes is which provider is the default and what a captured row actually contains.
package pos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// ProviderTypedNameV1 is the provider string a typed-name attestation is stamped with, versioned,
// because the sealed payload's shape is part of the evidence (0089 documents why).
const ProviderTypedNameV1 = "TYPED_NAME_V1"

// maxTypedNameLen bounds the typed name (the quote-acceptance bound, share.go).
const maxTypedNameLen = 200

// signaturePurposes is the pos_signature.purpose CHECK, mirrored in Go so an unknown purpose is a
// 400 naming the value instead of a 500 carrying a constraint name.
var signaturePurposes = map[string]bool{
	"TAX_ATTESTATION": true, "PROOF_OF_DELIVERY": true, "CARD_PRESENT": true, "ORDER_SIGNOFF": true,
}

// TypedAttestation is the act being captured: WHO typed their name, that they explicitly affirmed,
// and what they affirmed it against.
type TypedAttestation struct {
	Purpose   string `json:"purpose"`
	RefType   string `json:"ref_type"`
	RefID     string `json:"ref_id"`
	TypedName string `json:"typed_name"`
	Affirmed  bool   `json:"affirmed"`
}

// SealedAttestation is what actually gets encrypted and stored. It is the artifact that has to
// answer "what exactly did this person attest to, and how do we know" months later, so it carries
// the typed name, the affirmative, the staff actor who witnessed it, the purpose, the subject and
// the timestamp. Nothing here is recoverable from the other columns: that is the whole point.
type SealedAttestation struct {
	Version    string    `json:"version"`
	TypedName  string    `json:"typed_name"`
	Affirmed   bool      `json:"affirmed"`
	Purpose    string    `json:"purpose"`
	RefType    string    `json:"ref_type"`
	RefID      string    `json:"ref_id"`
	ActorID    string    `json:"actor_id"`
	CapturedAt time.Time `json:"captured_at"`
	Ref        string    `json:"signature_ref"`
}

// DecodeAttestation parses a decrypted signature payload. Exported so an admin/evidence surface (and
// the acceptance test) can prove what a sealed row says without re-implementing the shape.
func DecodeAttestation(plaintext []byte) (SealedAttestation, error) {
	var a SealedAttestation
	err := json.Unmarshal(plaintext, &a)
	return a, err
}

// TypedNameCapturer is the widened capture seam. SignatureProvider.Capture cannot carry an
// attestation (it takes only purpose/refType/refID), so the typed act gets its own method rather
// than being smuggled through the old one.
type TypedNameCapturer interface {
	CaptureTyped(ctx context.Context, in TypedAttestation) (signatureRef string, err error)
}

// signatureBinder is the lazy persistence binding both providers implement (main.go constructs a
// provider with the vault only; store/audit/clock are A-19-internal).
type signatureBinder interface {
	bind(store posStore, rec audit.Recorder, now func() time.Time)
}

// TypedNameSignatureProvider captures a typed-name attestation, seals the REAL payload through the
// shared Sealer (AES-256-GCM, *payment.Vault), persists a transaction-linked pos_signature row
// stamped TYPED_NAME_V1, and audits the capture.
type TypedNameSignatureProvider struct {
	vault Sealer
	store posStore
	audit audit.Recorder
	now   func() time.Time
}

// NewTypedNameSignatureProvider wires the shared sealer (the SimulatedSignatureProvider constructor
// shape, so main.go swaps one call).
func NewTypedNameSignatureProvider(vault Sealer) *TypedNameSignatureProvider {
	return &TypedNameSignatureProvider{vault: vault, now: time.Now}
}

func (p *TypedNameSignatureProvider) bind(store posStore, rec audit.Recorder, now func() time.Time) {
	if p.store == nil {
		p.store = store
	}
	if p.audit == nil {
		p.audit = rec
	}
	if p.now == nil && now != nil {
		p.now = now
	}
}

// Capture satisfies SignatureProvider and REFUSES. There is no honest way to produce a typed-name
// attestation from (purpose, refType, refID) alone, nobody typed anything and nobody affirmed
// anything, and inventing one is exactly the stub behaviour DIB-386 exists to delete. A caller on
// the old shape gets a 400 that names the fix rather than a row that lies.
func (p *TypedNameSignatureProvider) Capture(_ context.Context, _, _, _ string) (string, error) {
	return "", errAttestationRequired
}

// CaptureTyped records the attestation.
//
// THE TWO REFUSALS. A blank name is not a signature. And the affirmative is a REQUIRED SEPARATE ACT,
// not an implied consequence of submitting the form, an attestation someone could produce by
// accident is not an attestation (share.go AcceptByToken, verbatim reasoning). Both refuse BEFORE
// anything is sealed or written, so a refused capture leaves no row behind.
func (p *TypedNameSignatureProvider) CaptureTyped(ctx context.Context, in TypedAttestation) (string, error) {
	name := strings.TrimSpace(in.TypedName)
	if name == "" {
		return "", clientErr(http.StatusBadRequest, "pos: type your full name to sign")
	}
	if len(name) > maxTypedNameLen {
		return "", clientErr(http.StatusBadRequest, "pos: name is too long")
	}
	if !in.Affirmed {
		return "", clientErr(http.StatusBadRequest, "pos: you must confirm this attestation before it can be signed")
	}
	if !signaturePurposes[in.Purpose] {
		return "", clientErr(http.StatusBadRequest, "pos: unknown signature purpose %q", in.Purpose)
	}
	if strings.TrimSpace(in.RefID) == "" {
		return "", clientErr(http.StatusBadRequest, "pos: an attestation must name what it is signing")
	}

	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	ref := "sig_" + hex.EncodeToString(b[:])
	actor := identity.StaffUserIDFromContext(ctx)
	now := time.Now
	if p.now != nil {
		now = p.now
	}

	payload, err := json.Marshal(SealedAttestation{
		Version: ProviderTypedNameV1, TypedName: name, Affirmed: true,
		Purpose: in.Purpose, RefType: in.RefType, RefID: in.RefID,
		ActorID: actor, CapturedAt: now().UTC(), Ref: ref,
	})
	if err != nil {
		return "", err
	}
	// The captured content is sealed before it ever lands, same Encrypt path the stub used, now
	// carrying something worth encrypting.
	enc, keyVersion := payload, 0
	if p.vault != nil {
		sealed, kv, err := p.vault.Encrypt(payload)
		if err != nil {
			return "", err
		}
		enc, keyVersion = sealed, kv
	}

	if p.store != nil {
		if _, err := p.store.InsertSignature(ctx, signatureRow{
			Purpose: in.Purpose, RefType: in.RefType, RefID: in.RefID, SignatureEnc: enc,
			KeyVersion: keyVersion, Provider: ProviderTypedNameV1, CapturedBy: actor,
		}); err != nil {
			return "", err
		}
	}
	if p.audit != nil {
		// The typed NAME is deliberately NOT on the audit row, audit_log is the who/what/when
		// index, not a second copy of the sealed evidence (ref_type is already allowlisted; a
		// name is not).
		_ = p.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActSignatureCapture, EntityType: "signature", EntityID: in.RefID,
			ActorID: actor, Purpose: in.Purpose, MaskedRef: ref,
			Extra: map[string]any{"ref_type": in.RefType},
		})
	}
	return ref, nil
}

// CaptureTypedSignature is the service entry point for the typed-name attestation, mirroring
// CaptureSignature (service.go): it binds the A-19-internal persistence deps lazily, then delegates.
// A provider that cannot take an attestation (the old simulated stub) is refused honestly rather
// than silently downgraded to a fabricated capture.
func (s *Service) CaptureTypedSignature(ctx context.Context, in TypedAttestation) (string, error) {
	if s.sig == nil {
		return "", errNoSigProvider
	}
	if b, ok := s.sig.(signatureBinder); ok {
		b.bind(s.store, s.audit, s.now)
	}
	tp, ok := s.sig.(TypedNameCapturer)
	if !ok {
		return "", errNoTypedProvider
	}
	return tp.CaptureTyped(ctx, in)
}

var (
	// errAttestationRequired, the un-attested Capture seam on a typed-name provider.
	errAttestationRequired = clientError{status: 400, msg: "pos: a typed-name attestation (name + affirmation) is required to sign"}
	// errNoTypedProvider, the wired provider cannot take a typed attestation.
	errNoTypedProvider = clientError{status: 501, msg: "pos: the wired signature provider does not capture typed attestations"}
)
