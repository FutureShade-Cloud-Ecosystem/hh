// Package payment is the RUN-PAY card rail (W4). The dealer transacts through their own Run Payments
// merchant account; processor credentials are stored module-local and AES-256-GCM encrypted, and the
// PAN is tokenized client-side (Runner.js hosted iframe) so only a token, never a PAN, reaches the
// server (PCI SAQ-A). The till (A-03) is the SOLE writer of the payments row; this module exposes the
// Processor seam + a write-only config store + the pure Charge gateway call, and writes only
// payment_config.
package payment

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Vault encrypts the dealer's processor credentials at rest (AES-256-GCM), module-local.
type Vault struct {
	aead       cipher.AEAD
	keyVersion int
}

// NewVault builds a vault from a 32-byte hex key (PAYMENT_VAULT_KEY). An empty key generates an
// ephemeral one (dev/test ONLY, lost on restart).
func NewVault(keyHex string) (*Vault, error) {
	var key []byte
	if keyHex == "" {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	} else {
		k, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid vault key: %w", err)
		}
		if len(k) != 32 {
			return nil, errors.New("vault key must be 32 bytes (64 hex chars)")
		}
		key = k
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead, keyVersion: 1}, nil
}

// Encrypt seals plaintext as nonce||ciphertext.
func (v *Vault) Encrypt(plaintext []byte) (ciphertext []byte, keyVersion int, err error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, err
	}
	return v.aead.Seal(nonce, nonce, plaintext, nil), v.keyVersion, nil
}

// Decrypt opens a nonce||ciphertext blob.
func (v *Vault) Decrypt(ciphertext []byte, keyVersion int) ([]byte, error) {
	if keyVersion != v.keyVersion {
		return nil, fmt.Errorf("unknown key version %d", keyVersion)
	}
	ns := v.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	return v.aead.Open(nil, nonce, ct, nil)
}
