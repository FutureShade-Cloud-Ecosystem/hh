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

	"github.com/jackc/pgx/v5"

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
// than being smuggled through the old one. CaptureTypedTx is the same act riding the CALLER'S
// transaction, for callers already inside one (the dispatch pickups confirm): the row and its audit
// trail commit or roll back with the caller's write instead of landing on the pool mid-transaction.
type TypedNameCapturer interface {
	CaptureTyped(ctx context.Context, in TypedAttestation) (signatureRef string, err error)
	CaptureTypedTx(ctx context.Context, tx pgx.Tx, in TypedAttestation) (signatureRef string, err error)
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
// shape, so main.go swaps one call). The clock is deliberately NOT set here: the provider is
// constructed with the vault only, and the clock is a service dependency the lazy bind delivers at
// first capture (WithClock's clock, or NewService's wall-clock default). Setting time.Now here made
// the bind's fill-only-nil check a no-op, so the service clock never reached either capture path and
// every sealed attestation was stamped with the wall clock however the service was clocked. A
// provider captured with no service behind it stays on the wall clock through the sealTypedAttestation
// fallback.
func NewTypedNameSignatureProvider(vault Sealer) *TypedNameSignatureProvider {
	return &TypedNameSignatureProvider{vault: vault}
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

// validateTypedAttestation is THE refusal gate both capture paths run, in this order, returning the
// trimmed name the sealed payload carries.
//
// THE TWO REFUSALS. A blank name is not a signature. And the affirmative is a REQUIRED SEPARATE ACT,
// not an implied consequence of submitting the form, an attestation someone could produce by
// accident is not an attestation (share.go AcceptByToken, verbatim reasoning). Every refusal lands
// BEFORE anything is sealed or written, so a refused capture leaves no row behind on either path,
// and the pool capture and the tx capture refuse a given attestation identically because this
// function is the only gate there is.
func validateTypedAttestation(in TypedAttestation) (string, error) {
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
	return name, nil
}

// sealTypedAttestation mints the reference, seals the payload, and returns the row both capture
// paths persist plus the reference the caller and the audit row carry. Shared so a pool capture and
// a tx capture write the SAME evidence shape.
func (p *TypedNameSignatureProvider) sealTypedAttestation(ctx context.Context, name string, in TypedAttestation) (signatureRow, string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return signatureRow{}, "", err
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
		return signatureRow{}, "", err
	}
	// The captured content is sealed before it ever lands, same Encrypt path the stub used, now
	// carrying something worth encrypting.
	enc, keyVersion := payload, 0
	if p.vault != nil {
		sealed, kv, err := p.vault.Encrypt(payload)
		if err != nil {
			return signatureRow{}, "", err
		}
		enc, keyVersion = sealed, kv
	}
	return signatureRow{
		Purpose: in.Purpose, RefType: in.RefType, RefID: in.RefID, SignatureEnc: enc,
		KeyVersion: keyVersion, Provider: ProviderTypedNameV1, CapturedBy: actor,
	}, ref, nil
}

// signatureAuditEntry is the audit row both capture paths write. The typed NAME is deliberately NOT
// on the audit row, audit_log is the who/what/when index, not a second copy of the sealed evidence
// (ref_type is already allowlisted; a name is not).
func signatureAuditEntry(in TypedAttestation, actor, ref string) audit.SensitiveAccess {
	return audit.SensitiveAccess{
		Action: audit.ActSignatureCapture, EntityType: "signature", EntityID: in.RefID,
		ActorID: actor, Purpose: in.Purpose, MaskedRef: ref,
		Extra: map[string]any{"ref_type": in.RefType},
	}
}

// CaptureTyped records the attestation on the SHARED POOL: the standalone capture, for callers with
// no transaction of their own (the public will-call confirm, the delivery POD, the pos HTTP
// handler). A caller already inside a transaction must call CaptureTypedTx instead, a pool write
// here would be a second acquisition while that transaction holds its connection.
func (p *TypedNameSignatureProvider) CaptureTyped(ctx context.Context, in TypedAttestation) (string, error) {
	name, err := validateTypedAttestation(in)
	if err != nil {
		return "", err
	}
	row, ref, err := p.sealTypedAttestation(ctx, name, in)
	if err != nil {
		return "", err
	}
	if p.store != nil {
		if _, err := p.store.InsertSignature(ctx, row); err != nil {
			return "", err
		}
	}
	if p.audit != nil {
		// The pool capture's audit write stays best effort: this caller has no transaction, so
		// there is nothing to abort, and the standalone capture has always declined to fail the
		// response over the trail row. CaptureTypedTx is the strict one.
		_ = p.audit.Record(ctx, signatureAuditEntry(in, row.CapturedBy, ref))
	}
	return ref, nil
}

// txRidingAudit is the narrow seam the tx capture needs of the wired recorder, satisfied by
// audit.Service.RecordTx. A recorder that cannot take the transaction cannot write the evidence row
// ON it, and writing it on the pool instead would be a second acquisition while the caller's
// transaction holds its connection, the exact defect this capture exists to delete, so the
// capability is demanded and never fallen back.
type txRidingAudit interface {
	RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error
}

// insertSignatureOnTx is the statement the tx capture runs on the CALLER'S transaction. It is the
// pool capture's insert (Repository.InsertSignature) verbatim, one writer, two surfaces, so a
// signature row reads the same however it was captured: editing one means editing the other. The
// statement lives here rather than behind the store seam because the capture must write through the
// caller's own transaction handle, and the store seam's contract is pool writes.
const insertSignatureOnTx = `
INSERT INTO pos_signature (purpose, ref_type, ref_id, signature_enc, provider, captured_by)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`

// CaptureTypedTx records the attestation on the CALLER'S transaction: the signature row inserts on
// it, the audit row writes on it through RecordTx, so both commit or roll back WITH the caller's
// mutation, and a capture inside a transaction never asks the shared pool for a second connection.
//
// ORDERING AND THE AUDIT SEMANTICS. The attestation refusals run first (the shared gate, identical
// refusals on both paths), then the caller's transaction and the wiring are checked, then the two
// writes run. Unlike the pool capture, whose audit row is best effort because its caller has
// nothing to abort, a FAILED audit write here FAILS THE CAPTURE: the error propagates, the caller's
// transaction rolls back, and nothing commits, because a SensitiveAccess row that cannot be written
// must not be lost silently under a confirm that says the act happened. A wired recorder that
// cannot ride the transaction at all refuses for the same reason, and an unbound provider refuses
// rather than answer success for a capture that persisted nothing, which on a transaction path
// would commit the caller's mutation believing it carried evidence.
func (p *TypedNameSignatureProvider) CaptureTypedTx(ctx context.Context, tx pgx.Tx, in TypedAttestation) (string, error) {
	name, err := validateTypedAttestation(in)
	if err != nil {
		return "", err
	}
	if tx == nil {
		return "", clientErr(http.StatusInternalServerError, "pos: capturing inside a transaction requires that transaction")
	}
	if p.store == nil {
		return "", clientErr(http.StatusInternalServerError, "pos: the signature provider is not bound to a store, it cannot capture inside a transaction")
	}
	var ride txRidingAudit
	if p.audit != nil {
		r, ok := p.audit.(txRidingAudit)
		if !ok {
			return "", clientErr(http.StatusInternalServerError, "pos: the wired audit recorder cannot write on the caller's transaction")
		}
		ride = r
	}
	row, ref, err := p.sealTypedAttestation(ctx, name, in)
	if err != nil {
		return "", err
	}
	var id string
	if err := tx.QueryRow(ctx, insertSignatureOnTx, row.Purpose, row.RefType, row.RefID, row.SignatureEnc,
		row.Provider, nullStr(row.CapturedBy)).Scan(&id); err != nil {
		return "", err
	}
	if ride != nil {
		if err := ride.RecordTx(ctx, tx, signatureAuditEntry(in, row.CapturedBy, ref)); err != nil {
			return "", err
		}
	}
	return ref, nil
}

// captureTypedProvider is the shared entry check of both service capture entry points: it binds the
// A-19-internal persistence deps lazily (a provider is constructed with the vault only; store, audit
// and clock arrive from the service at first capture), then returns the typed-name capturer,
// refusing honestly rather than silently downgrading when the wired provider cannot take an
// attestation (the old simulated stub).
func (s *Service) captureTypedProvider() (TypedNameCapturer, error) {
	if s.sig == nil {
		return nil, errNoSigProvider
	}
	if b, ok := s.sig.(signatureBinder); ok {
		b.bind(s.store, s.audit, s.now)
	}
	tp, ok := s.sig.(TypedNameCapturer)
	if !ok {
		return nil, errNoTypedProvider
	}
	return tp, nil
}

// CaptureTypedSignature is the service entry point for the typed-name attestation on the SHARED
// POOL, mirroring CaptureSignature (service.go). Callers with no transaction of their own capture
// here; a caller already inside a transaction must call CaptureTypedSignatureTx instead.
func (s *Service) CaptureTypedSignature(ctx context.Context, in TypedAttestation) (string, error) {
	tp, err := s.captureTypedProvider()
	if err != nil {
		return "", err
	}
	return tp.CaptureTyped(ctx, in)
}

// CaptureTypedSignatureTx is CaptureTypedSignature riding the CALLER'S transaction: the dispatch
// pickups confirm (and any other caller already inside a transaction) captures through here, so the
// signature row and its audit row commit or roll back with the caller's write and no capture asks
// the shared pool for a second connection mid-transaction.
func (s *Service) CaptureTypedSignatureTx(ctx context.Context, tx pgx.Tx, in TypedAttestation) (string, error) {
	tp, err := s.captureTypedProvider()
	if err != nil {
		return "", err
	}
	return tp.CaptureTypedTx(ctx, tx, in)
}

var (
	// errAttestationRequired, the un-attested Capture seam on a typed-name provider.
	errAttestationRequired = clientError{status: 400, msg: "pos: a typed-name attestation (name + affirmation) is required to sign"}
	// errNoTypedProvider, the wired provider cannot take a typed attestation.
	errNoTypedProvider = clientError{status: 501, msg: "pos: the wired signature provider does not capture typed attestations"}
)
