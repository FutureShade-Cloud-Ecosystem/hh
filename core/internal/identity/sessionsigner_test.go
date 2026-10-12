package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	signerIssuer = "hh-test-deployment"
	signerAud    = "hh-staff"
	signerTenant = "hh-tenant"
)

func marshalPKIXForTest(pub *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

func freshPriv(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func writeSEC1(t *testing.T, dir, name string, k *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return writeFile(t, dir, name, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}

func writePKCS8(t *testing.T, dir, name string, k *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return writeFile(t, dir, name, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func writePub(t *testing.T, dir, name string, pub *ecdsa.PublicKey) string {
	t.Helper()
	b, err := marshalPKIXForTest(pub)
	if err != nil {
		t.Fatal(err)
	}
	return writeFile(t, dir, name, b)
}

func writeFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestMinter(t *testing.T, k *ecdsa.PrivateKey) *SessionMinter {
	t.Helper()
	m, err := NewSessionMinter(k, signerIssuer, signerAud)
	if err != nil {
		t.Fatalf("NewSessionMinter: %v", err)
	}
	return m
}

func verifierOver(t *testing.T, src KeySource, now func() time.Time) *Verifier {
	t.Helper()
	v, err := NewVerifier(Config{
		KeySource: src, Issuer: signerIssuer,
		StaffAudience: signerAud, TenantAudience: signerTenant, Now: now,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestSessionMinterRefusesTheDemoIssuer(t *testing.T) {
	if _, err := NewSessionMinter(freshPriv(t), "hh-demo", signerAud); err == nil {
		t.Fatal("issuer hh-demo accepted")
	}
	for _, c := range []struct{ iss, aud string }{{"", signerAud}, {signerIssuer, ""}} {
		if _, err := NewSessionMinter(freshPriv(t), c.iss, c.aud); err == nil {
			t.Errorf("issuer %q audience %q accepted", c.iss, c.aud)
		}
	}
	if _, err := NewSessionMinter(nil, signerIssuer, signerAud); err == nil {
		t.Error("nil key accepted")
	}
	k384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if _, err := NewSessionMinter(k384, signerIssuer, signerAud); err == nil {
		t.Error("P-384 key accepted")
	}
}

func TestSessionMinterWindowBounds(t *testing.T) {
	m := newTestMinter(t, freshPriv(t))
	for _, w := range []time.Duration{0, -time.Second, time.Nanosecond, time.Second - time.Nanosecond, MaxDriverSessionTTL + time.Nanosecond} {
		if _, err := m.Mint("sub", w); err == nil {
			t.Errorf("window %v accepted", w)
		}
	}
	for _, w := range []time.Duration{time.Second, 2 * time.Second, time.Hour, MaxDriverSessionTTL} {
		if _, err := m.Mint("sub", w); err != nil {
			t.Errorf("window %v refused: %v", w, err)
		}
	}
	if _, err := m.Mint("", time.Hour); err == nil {
		t.Error("empty subject accepted")
	}
}

func TestSessionMinterClaimsAreExactlyTheVocabulary(t *testing.T) {
	m := newTestMinter(t, freshPriv(t))
	at := time.Unix(1_700_000_000, 0)
	tok, err := m.MintAt("console-user-1", time.Hour, at)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("parts = %d", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"iss": signerIssuer, "sub": "console-user-1", "aud": signerAud,
		"iat": float64(at.Unix()), "exp": float64(at.Add(time.Hour).Unix()),
	}
	if len(claims) != len(want) {
		t.Fatalf("claims = %v", claims)
	}
	for k, v := range want {
		if claims[k] != v {
			t.Errorf("claim %s = %v, want %v", k, claims[k], v)
		}
	}
	rawHdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var hdr map[string]string
	_ = json.Unmarshal(rawHdr, &hdr)
	kid, _ := SessionKeyThumbprint(m.PublicKey())
	if hdr["alg"] != "ES256" || hdr["kid"] != kid {
		t.Errorf("header = %v", hdr)
	}
}

func TestVerifierAcceptsAMintedToken(t *testing.T) {
	k := freshPriv(t)
	set, err := NewSessionKeySet(&k.PublicKey, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestMinter(t, k)
	v := verifierOver(t, set, nil)
	tok, err := m.Mint("console-user-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Class != ClassStaff || got.Subject != "console-user-1" {
		t.Fatalf("token = %+v", got)
	}
	if got.ExpiresAt.Sub(got.IssuedAt) != time.Hour {
		t.Fatalf("window = %v", got.ExpiresAt.Sub(got.IssuedAt))
	}
}

func TestVerifierRefusesAnotherKeysToken(t *testing.T) {
	k, other := freshPriv(t), freshPriv(t)
	set, _ := NewSessionKeySet(&k.PublicKey, nil, nil)
	v := verifierOver(t, set, nil)
	// A token signed by another key under the current key's kid.
	m := newTestMinter(t, other)
	m.kid, _ = SessionKeyThumbprint(&k.PublicKey)
	tok, err := m.Mint("sub", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("forged signature verified")
	}
	// A token under the other key's own kid is an unknown kid.
	tok, _ = newTestMinter(t, other).Mint("sub", time.Hour)
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("unknown kid verified")
	}
}

func TestVerifierRefusesADemoIssuedToken(t *testing.T) {
	k := freshPriv(t)
	set, _ := NewSessionKeySet(&k.PublicKey, nil, nil)
	v := verifierOver(t, set, nil)
	m := newTestMinter(t, k)
	m.issuer = "hh-demo" // bypass the constructor to prove the verifier alone refuses it
	tok, err := m.Mint("sub", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("iss=hh-demo verified")
	}
}

// Rotation across the three slots: a token minted under a key verifies
// while that key sits in any of current, NEXT or PREVIOUS, and stops when
// the key leaves the set.
func TestKidRotationAcrossCurrentNextPrevious(t *testing.T) {
	oldK, newK := freshPriv(t), freshPriv(t)
	tokOld, _ := newTestMinter(t, oldK).Mint("sub", time.Hour)
	tokNew, _ := newTestMinter(t, newK).Mint("sub", time.Hour)
	ctx := context.Background()

	// Stage 1: old is current, new is published as NEXT.
	set, err := NewSessionKeySet(&oldK.PublicKey, &newK.PublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := verifierOver(t, set, nil)
	if _, err := v.Verify(ctx, tokOld); err != nil {
		t.Errorf("stage 1 old: %v", err)
	}
	if _, err := v.Verify(ctx, tokNew); err != nil {
		t.Errorf("stage 1 new (NEXT): %v", err)
	}
	// Stage 2: new is current, old is PREVIOUS.
	set, _ = NewSessionKeySet(&newK.PublicKey, nil, &oldK.PublicKey)
	v = verifierOver(t, set, nil)
	if _, err := v.Verify(ctx, tokOld); err != nil {
		t.Errorf("stage 2 old (PREVIOUS): %v", err)
	}
	if _, err := v.Verify(ctx, tokNew); err != nil {
		t.Errorf("stage 2 new: %v", err)
	}
	// Stage 3: PREVIOUS retired.
	set, _ = NewSessionKeySet(&newK.PublicKey, nil, nil)
	v = verifierOver(t, set, nil)
	if _, err := v.Verify(ctx, tokOld); err == nil {
		t.Error("stage 3: a retired key's token verified")
	}
	if _, err := v.Verify(ctx, tokNew); err != nil {
		t.Errorf("stage 3 new: %v", err)
	}
}

func TestLoadSessionKeysFromFiles(t *testing.T) {
	dir := t.TempDir()
	cur, next, prev := freshPriv(t), freshPriv(t), freshPriv(t)
	curFile := writeSEC1(t, dir, "current.pem", cur)
	nextFile := writePub(t, dir, "next.pem", &next.PublicKey)
	prevFile := writePub(t, dir, "previous.pem", &prev.PublicKey)

	loaded, err := LoadSessionKeys(SessionKeyFiles{Current: curFile, Next: nextFile, Previous: prevFile}, signerIssuer, signerAud)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cur.PublicKey.Equal(loaded.Minter.PublicKey()) {
		t.Fatal("minter holds another key")
	}
	for name, pub := range map[string]*ecdsa.PublicKey{"current": &cur.PublicKey, "next": &next.PublicKey, "previous": &prev.PublicKey} {
		kid, _ := SessionKeyThumbprint(pub)
		if _, ok, _ := loaded.Keys.KeyByID(context.Background(), kid); !ok {
			t.Errorf("%s not in the key set", name)
		}
	}

	// PKCS8 loads too; NEXT and PREVIOUS are optional.
	p8 := writePKCS8(t, dir, "p8.pem", cur)
	if _, err := LoadSessionKeys(SessionKeyFiles{Current: p8}, signerIssuer, signerAud); err != nil {
		t.Fatalf("pkcs8: %v", err)
	}
}

func TestLoadSessionKeysRefusals(t *testing.T) {
	dir := t.TempDir()
	cur, other := freshPriv(t), freshPriv(t)
	curFile := writeSEC1(t, dir, "current.pem", cur)
	curPub := writePub(t, dir, "cur-pub.pem", &cur.PublicKey)
	otherPub := writePub(t, dir, "other-pub.pem", &other.PublicKey)
	garbage := writeFile(t, dir, "garbage.pem", []byte("hunter2-not-a-key"))
	pubAsCurrent := writePub(t, dir, "pub-as-current.pem", &cur.PublicKey)
	k384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	der, _ := x509.MarshalECPrivateKey(k384)
	p384 := writeFile(t, dir, "p384.pem", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))

	cases := []struct {
		name  string
		files SessionKeyFiles
	}{
		{"no current path", SessionKeyFiles{}},
		{"missing file", SessionKeyFiles{Current: filepath.Join(dir, "absent.pem")}},
		{"garbage current", SessionKeyFiles{Current: garbage}},
		{"public half as current", SessionKeyFiles{Current: pubAsCurrent}},
		{"P-384 current", SessionKeyFiles{Current: p384}},
		{"next equals current", SessionKeyFiles{Current: curFile, Next: curPub}},
		{"previous equals current", SessionKeyFiles{Current: curFile, Previous: curPub}},
		{"next equals previous", SessionKeyFiles{Current: curFile, Next: otherPub, Previous: otherPub}},
		{"garbage next", SessionKeyFiles{Current: curFile, Next: garbage}},
		{"missing previous", SessionKeyFiles{Current: curFile, Previous: filepath.Join(dir, "absent.pem")}},
	}
	for _, c := range cases {
		_, err := LoadSessionKeys(c.files, signerIssuer, signerAud)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), dir) {
			t.Errorf("%s: error carries file content or path: %v", c.name, err)
		}
	}
	// Private key given as a NEXT or PREVIOUS file is refused: those hold public halves only.
	if _, err := LoadSessionKeys(SessionKeyFiles{Current: curFile, Next: writeSEC1(t, dir, "priv-next.pem", other)}, signerIssuer, signerAud); err == nil {
		t.Error("a private key was accepted as NEXT")
	}
	// The demo issuer is refused at load too.
	if _, err := LoadSessionKeys(SessionKeyFiles{Current: curFile}, "hh-demo", signerAud); err == nil {
		t.Error("hh-demo accepted at load")
	}
}

// No output of the loaded material carries a private member: the key set
// serves only public coordinates, and the minter has no marshal path.
func TestNoPrivateMemberInAnyOutput(t *testing.T) {
	k := freshPriv(t)
	m := newTestMinter(t, k)
	set, _ := NewSessionKeySet(m.PublicKey(), nil, nil)
	dBytes := make([]byte, 32)
	k.D.FillBytes(dBytes)
	dB64 := base64.RawURLEncoding.EncodeToString(dBytes)
	tok, _ := m.Mint("sub", time.Hour)
	outputs := []string{string(set.JWKS()), tok}
	if b, err := json.Marshal(m); err == nil {
		outputs = append(outputs, string(b))
	}
	for i, out := range outputs {
		if strings.Contains(out, dB64) {
			t.Errorf("output %d carries the private scalar", i)
		}
	}
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	_ = json.Unmarshal(set.JWKS(), &doc)
	for _, key := range doc.Keys {
		if _, has := key["d"]; has {
			t.Error("JWK Set carries d")
		}
	}
}

func TestLoadErrorsAreNotWrappedPathErrors(t *testing.T) {
	_, err := LoadSessionKeys(SessionKeyFiles{Current: "/nonexistent-dir/secret-name.pem"}, signerIssuer, signerAud)
	if err == nil {
		t.Fatal("accepted")
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		t.Fatal("error wraps a path error")
	}
	if strings.Contains(err.Error(), "secret-name") {
		t.Fatalf("error names the path: %v", err)
	}
}
