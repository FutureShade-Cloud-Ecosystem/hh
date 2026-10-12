package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// writeSigninKey writes a fresh private key file and returns its path and
// the key. Tests generate their own keys; none is committed.
func writeSigninKey(t *testing.T, dir, name string) (string, *ecdsa.PrivateKey) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, k
}

func writeSigninPub(t *testing.T, dir, name string, pub *ecdsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// consoleEnv is fullEnv plus the console sign in mode and a key file.
func consoleEnv(t *testing.T) map[string]string {
	t.Helper()
	env := fullEnv()
	delete(env, "CORE_JWKS_URL")
	keyFile, _ := writeSigninKey(t, t.TempDir(), "current.pem")
	env["CORE_SIGNIN_MODE"] = "console"
	env["CORE_SESSION_SIGNING_KEY_FILE"] = keyFile
	return env
}

func getenvOf(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestSigninModeHasNoDefaultAndTheDefaultBuildAcceptsConsoleOnly(t *testing.T) {
	for _, mode := range []string{"", "demo", "Console", "both", " "} {
		env := consoleEnv(t)
		if mode == "" {
			delete(env, "CORE_SIGNIN_MODE")
		} else {
			env["CORE_SIGNIN_MODE"] = mode
		}
		_, err := loadServeConfig(getenvOf(env))
		if err == nil {
			t.Errorf("mode %q accepted by the default build", mode)
			continue
		}
		if !strings.Contains(err.Error(), "CORE_SIGNIN_MODE") {
			t.Errorf("mode %q: refusal does not name the variable: %v", mode, err)
		}
	}
	if _, err := loadServeConfig(getenvOf(consoleEnv(t))); err != nil {
		t.Fatalf("console refused: %v", err)
	}
}

func TestJWKSURLIsOptionalWithThePersistentKeySource(t *testing.T) {
	env := consoleEnv(t)
	if _, ok := env["CORE_JWKS_URL"]; ok {
		t.Fatal("test setup keeps a JWKS URL")
	}
	if _, err := loadServeConfig(getenvOf(env)); err != nil {
		t.Fatalf("no JWKS URL refused: %v", err)
	}
}

func TestSessionCompositionRefusals(t *testing.T) {
	dir := t.TempDir()
	keyFile, key := writeSigninKey(t, dir, "current.pem")
	curPub := writeSigninPub(t, dir, "cur-pub.pem", &key.PublicKey)
	garbage := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		set  map[string]string
		iss  string
		want string
	}{
		{"no key file", map[string]string{}, "deploy-a", "CORE_SESSION_SIGNING_KEY_FILE"},
		{"missing key file", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": filepath.Join(dir, "absent.pem")}, "deploy-a", "session signing key"},
		{"garbage key file", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": garbage}, "deploy-a", "session signing key"},
		{"issuer hh-demo", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": keyFile}, "hh-demo", "hh-demo"},
		{"next equals current", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": keyFile, "CORE_SESSION_KEY_NEXT_FILE": curPub}, "deploy-a", "differ"},
		{"previous equals current", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": keyFile, "CORE_SESSION_KEY_PREVIOUS_FILE": curPub}, "deploy-a", "differ"},
		{"ttl above the knob minimum", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": keyFile, "CORE_EXCHANGE_TOKEN_TTL": "90m"}, "deploy-a", "CORE_EXCHANGE_TOKEN_TTL"},
		{"ttl above the staff window", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": keyFile, "CORE_EXCHANGE_TOKEN_TTL": "13h"}, "deploy-a", "CORE_EXCHANGE_TOKEN_TTL"},
		{"ttl zero", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": keyFile, "CORE_EXCHANGE_TOKEN_TTL": "0s"}, "deploy-a", "CORE_EXCHANGE_TOKEN_TTL"},
		{"ttl not a duration", map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": keyFile, "CORE_EXCHANGE_TOKEN_TTL": "soon"}, "deploy-a", "CORE_EXCHANGE_TOKEN_TTL"},
	}
	for _, c := range cases {
		_, err := loadSessionComposition(getenvOf(c.set), c.iss, "hh-staff")
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not name %q", c.name, err, c.want)
		}
		if strings.Contains(err.Error(), dir) {
			t.Errorf("%s: error carries the key path", c.name)
		}
	}
}

func TestSessionCompositionLoadsAndMints(t *testing.T) {
	dir := t.TempDir()
	keyFile, key := writeSigninKey(t, dir, "current.pem")
	_, next := writeSigninKey(t, dir, "scratch.pem")
	nextFile := writeSigninPub(t, dir, "next.pem", &next.PublicKey)
	env := map[string]string{
		"CORE_SESSION_SIGNING_KEY_FILE": keyFile,
		"CORE_SESSION_KEY_NEXT_FILE":    nextFile,
	}
	comp, err := loadSessionComposition(getenvOf(env), "deploy-a", "hh-staff")
	if err != nil {
		t.Fatal(err)
	}
	if comp.exchangeTTL != identity.MinDriverSessionHours*time.Hour {
		t.Fatalf("default window = %v", comp.exchangeTTL)
	}
	if !key.PublicKey.Equal(comp.minter.PublicKey()) {
		t.Fatal("minter holds another key")
	}
	env["CORE_EXCHANGE_TOKEN_TTL"] = "30m"
	comp, err = loadSessionComposition(getenvOf(env), "deploy-a", "hh-staff")
	if err != nil || comp.exchangeTTL != 30*time.Minute {
		t.Fatalf("ttl: %v %v", err, comp)
	}
}
