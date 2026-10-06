package pos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
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

	// bindMu orders the lazy bind's fill against every read of the fields it fills, the same guard
	// the typed provider carries: the service binds at EVERY capture (CaptureSignature), so
	// simultaneous first captures can fill and read store and audit at the same moment, and without
	// the lock that is the data race the round 1 review's P3 recorded on this stand-in
	// (signature_race_test.go is its leg). boundDeps is the read side. vault is set at construction
	// and never written again, so it needs no guard.
	bindMu sync.Mutex

	// bindFills is the stand-in's bind counter, the leg's count reply (signature_race_test.go):
	// it counts the lazy binds that ran the fill, so a concurrent submit's serialization is
	// observable (one bind winning and one waiting reads 1; two binds both running the fill, the
	// un-serialized shape, reads 2). The count is taken inside the lock, so the serialized fill
	// answers exactly once; atomic so the type stays honest about cross goroutine reads.
	bindFills atomic.Int64
}

// NewSimulatedSignatureProvider wires the shared sealer.
func NewSimulatedSignatureProvider(vault Sealer) *SimulatedSignatureProvider {
	return &SimulatedSignatureProvider{vault: vault, now: time.Now}
}

// bind lazily wires the persistence deps without disturbing main.go's vault-only constructor.
func (p *SimulatedSignatureProvider) bind(store posStore, rec audit.Recorder, now func() time.Time) {
	p.bindMu.Lock()
	defer p.bindMu.Unlock()
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

// boundDeps reads the lazily bound store and audit recorder under the same lock bind fills under,
// so a capture that races a concurrent first bind reads the fields after the fill instead of
// against it. The meanings travel unchanged: a nil store keeps the capture skipping the row and a
// nil recorder keeps it skipping the trail.
func (p *SimulatedSignatureProvider) boundDeps() (posStore, audit.Recorder) {
	p.bindMu.Lock()
	defer p.bindMu.Unlock()
	return p.store, p.audit
}

// Capture mints a SignatureRef, seals a simulated payload, persists it encrypted + transaction-linked,
// and writes a SIGNATURE_CAPTURE audit row (actor + purpose + timestamp). The store and recorder
// come from the locked snapshot boundDeps returns, so a capture never reads a field a concurrent
// lazy bind is filling.
func (p *SimulatedSignatureProvider) Capture(ctx context.Context, purpose, refType, refID string) (string, error) {
	store, rec := p.boundDeps()
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
	if store != nil {
		if _, err := store.InsertSignature(ctx, signatureRow{
			Purpose: purpose, RefType: refType, RefID: refID, SignatureEnc: enc, KeyVersion: keyVersion,
			Provider: "SIMULATED", CapturedBy: actor,
		}); err != nil {
			return "", err
		}
	}
	if rec != nil {
		_ = rec.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActSignatureCapture, EntityType: "signature", EntityID: refID,
			ActorID: actor, Purpose: purpose, MaskedRef: ref,
		})
	}
	return ref, nil
}
