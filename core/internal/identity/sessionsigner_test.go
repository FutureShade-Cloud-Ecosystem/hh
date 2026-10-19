package identity

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
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
// serves only public coordinates, and the minter has no marshal path. The
// scalar is searched for in every encoding a struct dump could use: base64url,
// decimal and hex. A minter that gained an exported key field would marshal
// the scalar as a decimal number, so the marshal must be exactly "{}".
func TestNoPrivateMemberInAnyOutput(t *testing.T) {
	k := freshPriv(t)
	m := newTestMinter(t, k)
	set, _ := NewSessionKeySet(m.PublicKey(), nil, nil)
	dBytes := make([]byte, 32)
	k.D.FillBytes(dBytes)
	needles := []string{base64.RawURLEncoding.EncodeToString(dBytes), k.D.String(), k.D.Text(16)}
	tok, _ := m.Mint("sub", time.Hour)
	marshalled, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal minter: %v", err)
	}
	if string(marshalled) != "{}" {
		t.Fatalf("the minter marshals to %s, want {}", marshalled)
	}
	outputs := []string{string(set.JWKS()), tok, string(marshalled)}
	for i, out := range outputs {
		for _, needle := range needles {
			if strings.Contains(out, needle) {
				t.Errorf("output %d carries the private scalar (%d chars)", i, len(needle))
			}
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

// A minter dumped through fmt, by pointer or by value, with any verb, prints
// a fixed redaction and none of the key.
func TestSessionMinterFormatsRedacted(t *testing.T) {
	k := freshPriv(t)
	m := newTestMinter(t, k)
	needles := []string{k.D.String(), k.D.Text(16), k.X.String(), k.Y.String()}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%x", "%q"} {
		for name, arg := range map[string]any{"pointer": m, "value": *m} {
			out := fmt.Sprintf(verb, arg)
			if !strings.Contains(out, "redacted") {
				t.Errorf("%s of the %s prints %q, want the redaction", verb, name, out)
			}
			for _, n := range needles {
				if strings.Contains(out, n) {
					t.Errorf("%s of the %s carries key material", verb, name)
				}
			}
		}
	}
	if got := m.String(); !strings.Contains(got, "redacted") {
		t.Errorf("String = %q", got)
	}
}

// A key file with a second PEM block, or text around the block, is refused
// in all three loaders: a NEXT file holding a public block followed by a
// private one must not be accepted as public.
func TestKeyFilesRefuseTrailingAndLeadingData(t *testing.T) {
	dir := t.TempDir()
	cur, other := freshPriv(t), freshPriv(t)
	privDER, _ := x509.MarshalECPrivateKey(other)
	privBlock := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
	pubOther, _ := marshalPKIXForTest(&other.PublicKey)
	curDER, _ := x509.MarshalECPrivateKey(cur)
	curPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: curDER})
	curFile := writeFile(t, dir, "cur.pem", curPEM)

	// A private block with its END line short by one dash does not decode, so
	// pem.Decode skips it and returns the next valid block: the file must
	// still be refused, or private key base64 rides in a "public" file.
	damaged := bytes.Replace(privBlock, []byte("-----END EC PRIVATE KEY-----"), []byte("----END EC PRIVATE KEY-----"), 1)
	if bytes.Equal(damaged, privBlock) {
		t.Fatal("test setup: the END line was not damaged")
	}
	bad := map[string][]byte{
		"damaged private then public": append(append([]byte{}, damaged...), pubOther...),
		"public then damaged private": append(append([]byte{}, pubOther...), damaged...),
		"public then private":         append(append([]byte{}, pubOther...), privBlock...),
		"public then text":            append(append([]byte{}, pubOther...), []byte("trailing words\n")...),
		"text then public":            append([]byte("leading words\n"), pubOther...),
	}
	for name, content := range bad {
		f := writeFile(t, dir, "x.pem", content)
		if _, err := LoadSessionKeys(SessionKeyFiles{Current: curFile, Next: f}, signerIssuer, signerAud); err == nil {
			t.Errorf("NEXT, %s: accepted", name)
		}
		if _, err := LoadSessionKeys(SessionKeyFiles{Current: curFile, Previous: f}, signerIssuer, signerAud); err == nil {
			t.Errorf("PREVIOUS, %s: accepted", name)
		}
	}
	badPriv := map[string][]byte{
		"damaged block then private": append(append([]byte{}, damaged...), curPEM...),
		"two private blocks":         append(append([]byte{}, curPEM...), privBlock...),
		"private then text":          append(append([]byte{}, curPEM...), []byte("tail\n")...),
		"text then private":          append([]byte("head\n"), curPEM...),
	}
	for name, content := range badPriv {
		f := writeFile(t, dir, "p.pem", content)
		if _, err := LoadSessionKeys(SessionKeyFiles{Current: f}, signerIssuer, signerAud); err == nil {
			t.Errorf("current, %s: accepted", name)
		}
	}
	// Trailing whitespace alone is fine.
	ok := writeFile(t, dir, "ws.pem", append(append([]byte{}, curPEM...), []byte("\n\n  \n")...))
	if _, err := LoadSessionKeys(SessionKeyFiles{Current: ok}, signerIssuer, signerAud); err != nil {
		t.Errorf("trailing whitespace refused: %v", err)
	}
}

// A key file longer than the cap is refused, never truncated: a valid block,
// a long run of whitespace and then a second block must not slip past the
// trailing data check by falling beyond the read limit. Public files are
// capped the same way.
func TestKeyFilesOverTheCapAreRefused(t *testing.T) {
	dir := t.TempDir()
	cur, other := freshPriv(t), freshPriv(t)
	curDER, _ := x509.MarshalECPrivateKey(cur)
	curPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: curDER})
	otherDER, _ := x509.MarshalECPrivateKey(other)
	otherPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: otherDER})
	pad := bytes.Repeat([]byte("\n"), maxKeyFileBytes)

	big := append(append(append([]byte{}, curPEM...), pad...), otherPEM...)
	bigFile := writeFile(t, dir, "big.pem", big)
	if _, err := LoadSessionKeys(SessionKeyFiles{Current: bigFile}, signerIssuer, signerAud); err == nil {
		t.Error("an oversized private key file was accepted")
	}

	curFile := writeSEC1(t, dir, "cur.pem", cur)
	pubPEM, _ := marshalPKIXForTest(&other.PublicKey)
	bigPub := writeFile(t, dir, "bigpub.pem", append(append(append([]byte{}, pubPEM...), pad...), otherPEM...))
	for _, files := range []SessionKeyFiles{{Current: curFile, Next: bigPub}, {Current: curFile, Previous: bigPub}} {
		if _, err := LoadSessionKeys(files, signerIssuer, signerAud); err == nil {
			t.Error("an oversized public key file was accepted")
		}
	}

	// A file exactly at the cap still loads.
	atCap := append(append([]byte{}, curPEM...), bytes.Repeat([]byte("\n"), maxKeyFileBytes-len(curPEM))...)
	if len(atCap) != maxKeyFileBytes {
		t.Fatalf("test setup: %d bytes", len(atCap))
	}
	capFile := writeFile(t, dir, "cap.pem", atCap)
	if _, err := LoadSessionKeys(SessionKeyFiles{Current: capFile}, signerIssuer, signerAud); err != nil {
		t.Errorf("a file at the cap was refused: %v", err)
	}
}

const signerTenantID = "33333333-3333-4333-8333-333333333333"

func TestMintTenantAtNeedsTheTenantAudienceAndATenantAccount(t *testing.T) {
	k := freshPriv(t)
	m := newTestMinter(t, k)
	at := time.Unix(1_700_000_000, 0)
	if _, err := m.MintTenantAt("sub", signerTenantID, time.Hour, at); err == nil {
		t.Fatal("tenant mint without a tenant audience accepted")
	}
	for _, bad := range []string{"", signerAud} {
		if err := m.WithTenantAudience(bad); err == nil {
			t.Errorf("tenant audience %q accepted", bad)
		}
	}
	if err := m.WithTenantAudience(signerTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MintTenantAt("", signerTenantID, time.Hour, at); err == nil {
		t.Error("empty subject accepted")
	}
	if _, err := m.MintTenantAt("sub", "", time.Hour, at); err == nil {
		t.Error("empty tenant account accepted")
	}
	if _, err := m.MintTenantAt("sub", signerTenantID, MaxDriverSessionTTL+time.Second, at); err == nil {
		t.Error("window over the backstop accepted")
	}
	// The staff mint is unchanged by the tenant audience.
	staff, err := m.MintAt("sub", time.Hour, at)
	if err != nil {
		t.Fatal(err)
	}
	if claimsOf(t, staff)["aud"] != signerAud || claimsOf(t, staff)["tenant"] != nil {
		t.Errorf("staff token claims = %v", claimsOf(t, staff))
	}
}

func claimsOf(t *testing.T, tok string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVerifierClassifiesAMintedTenantTokenWithItsTenantClaim(t *testing.T) {
	k := freshPriv(t)
	set, err := NewSessionKeySet(&k.PublicKey, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestMinter(t, k)
	if err := m.WithTenantAudience(signerTenant); err != nil {
		t.Fatal(err)
	}
	tok, err := m.MintTenantAt("console-user-2", signerTenantID, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := claimsOf(t, tok)
	if len(c) != 6 || c["aud"] != signerTenant || c["tenant"] != signerTenantID {
		t.Fatalf("claims = %v", c)
	}
	got, err := verifierOver(t, set, nil).Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Class != ClassTenant || got.Subject != "console-user-2" || got.TenantID != signerTenantID {
		t.Fatalf("token = %+v", got)
	}
}
