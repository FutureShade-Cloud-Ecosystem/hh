package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// consoleSessionValues puts a composition fixture's environment on the
// console sign in mode: a fresh session signing key is written to a file
// under the test's own directory, the mode and the key path are set, and
// the kit's minter signs its default tokens under that key, so the staff
// and tenant bearers the fixtures mint verify against the persistent key
// source the default build composes. The remote JWK Set URL is dropped:
// the console mode never reads it.
func consoleSessionValues(t *testing.T, minter *testkit.Minter, values map[string]string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "session-signing.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("session key file: %v", err)
	}
	minter.Session = key
	values["CORE_SIGNIN_MODE"] = "console"
	values["CORE_SESSION_SIGNING_KEY_FILE"] = path
	delete(values, "CORE_JWKS_URL")
}
