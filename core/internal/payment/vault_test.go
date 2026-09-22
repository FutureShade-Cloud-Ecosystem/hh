package payment

import (
	"bytes"
	"strings"
	"testing"
)

// testVaultKey is a valid 32-byte (64 hex char) AES-256-GCM key for the vault tests.
const testVaultKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// T1, encrypt→decrypt round-trips the plaintext AND the ciphertext never contains the plaintext
// (US-RP01 "encrypted at rest"). The credential confidentiality keystone.
func TestVault_RoundTrip(t *testing.T) {
	v, err := NewVault(testVaultKey)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	secret := []byte("dealer-secret-api_key-tkn_ppc_supersecret")
	ct, kv, err := v.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if kv != 1 {
		t.Fatalf("key_version = %d, want 1", kv)
	}
	if bytes.Contains(ct, secret) {
		t.Fatal("ciphertext contains the plaintext secret, not encrypted")
	}
	if strings.Contains(string(ct), "tkn_ppc_supersecret") {
		t.Fatal("ciphertext leaks the plaintext token")
	}
	got, err := v.Decrypt(ct, kv)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, secret)
	}
}

// T2, a blob sealed under key v1 fails to open under an unknown key version (no silent garbage).
func TestVault_WrongKeyVersion_Fails(t *testing.T) {
	v, err := NewVault(testVaultKey)
	if err != nil {
		t.Fatalf("NewVault: %v", err)
	}
	ct, kv, err := v.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := v.Decrypt(ct, kv+1); err == nil {
		t.Fatal("Decrypt under an unknown key version must fail, not return garbage")
	}
}

// A wrong-length / non-hex key must be rejected (fail closed); an empty key yields an ephemeral
// dev/test vault (NewVault("") must not error).
func TestVault_KeyValidation(t *testing.T) {
	if _, err := NewVault("zznothex"); err == nil {
		t.Fatal("a non-hex key must be rejected")
	}
	if _, err := NewVault("00112233"); err == nil {
		t.Fatal("a key that is not 32 bytes must be rejected")
	}
	if _, err := NewVault(""); err != nil {
		t.Fatalf("an empty key must yield an ephemeral dev vault, got %v", err)
	}
}
