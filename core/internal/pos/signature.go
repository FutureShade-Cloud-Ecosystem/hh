package pos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// SignatureProvider is the A-19-owned capture seam. SimulatedSignatureProvider is the W4 default
// (on-screen/stub); RunTerminalSignatureProvider / ThirdPartySignatureProvider slot in later behind
// the same interface, a fake provider substitutes with no caller change (the adapter pattern, U22).
type SignatureProvider interface {
	Capture(ctx context.Context, purpose, refType, refID string) (signatureRef string, err error)
}

// Sealer is the shared AES-256-GCM primitive (satisfied by *payment.Vault) used to encrypt the captured
// signature payload at rest. Kept as an interface so pos does not hard-depend on a concrete vault type.
type Sealer interface {
	Encrypt(plaintext []byte) (ciphertext []byte, keyVersion int, err error)
	Decrypt(ciphertext []byte, keyVersion int) ([]byte, error)
}

// SimulatedSignatureProvider captures on-screen / stub signatures. It encrypts the captured payload at
// rest (via the shared Sealer), persists a transaction-linked pos_signature row, and audits the capture.
// store/audit/clock are bound lazily by the service (main.go wires it with the vault only).
type SimulatedSignatureProvider struct {
	vault Sealer
	store posStore
	audit audit.Recorder
	now   func() time.Time

	// bindFills is the stand-in's bind counter, the leg's count reply (signature_race_test.go):
	// it counts the lazy binds that ran the fill, so a concurrent submit's serialization is
	// observable (one bind winning and one waiting reads 1; two binds both running the fill, the
	// un-serialized shape, reads 2). Atomic so the count stays exact while the fields it watches
	// are still unlocked; the row 834 lock then moves the count inside the mutex.
	bindFills atomic.Int64
}

// NewSimulatedSignatureProvider wires the shared sealer.
func NewSimulatedSignatureProvider(vault Sealer) *SimulatedSignatureProvider {
	return &SimulatedSignatureProvider{vault: vault, now: time.Now}
}

// bind lazily wires the persistence deps without disturbing main.go's vault-only constructor.
func (p *SimulatedSignatureProvider) bind(store posStore, rec audit.Recorder, now func() time.Time) {
	filled := false
	if p.store == nil {
		p.store = store
		filled = true
	}
	if p.audit == nil {
		p.audit = rec
		filled = true
	}
	if p.now == nil && now != nil {
		p.now = now
		filled = true
	}
	if filled {
		p.bindFills.Add(1)
	}
}

// Capture mints a SignatureRef, seals a simulated payload, persists it encrypted + transaction-linked,
// and writes a SIGNATURE_CAPTURE audit row (actor + purpose + timestamp).
func (p *SimulatedSignatureProvider) Capture(ctx context.Context, purpose, refType, refID string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	ref := "sig_" + hex.EncodeToString(b[:])

	// The simulated payload stands in for a real ink/terminal capture; it is sealed before it ever lands.
	enc, keyVersion := []byte(ref), 0
	if p.vault != nil {
		sealed, kv, err := p.vault.Encrypt([]byte("SIMULATED:" + purpose + ":" + ref))
		if err != nil {
			return "", err
		}
		enc, keyVersion = sealed, kv
	}

	actor := identity.StaffUserIDFromContext(ctx)
	if p.store != nil {
		if _, err := p.store.InsertSignature(ctx, signatureRow{
			Purpose: purpose, RefType: refType, RefID: refID, SignatureEnc: enc, KeyVersion: keyVersion,
			Provider: "SIMULATED", CapturedBy: actor,
		}); err != nil {
			return "", err
		}
	}
	if p.audit != nil {
		_ = p.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActSignatureCapture, EntityType: "signature", EntityID: refID,
			ActorID: actor, Purpose: purpose, MaskedRef: ref,
		})
	}
	return ref, nil
}
