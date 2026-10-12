package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The P-256 base point: a public, well known curve constant. The golden
// tests use it as a fixed public key so no private key is ever committed.
const (
	baseX = "6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296"
	baseY = "4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5"
)

func basePointKey(t *testing.T) *ecdsa.PublicKey {
	t.Helper()
	x, _ := new(big.Int).SetString(baseX, 16)
	y, _ := new(big.Int).SetString(baseY, 16)
	if !elliptic.P256().IsOnCurve(x, y) {
		t.Fatal("base point is not on the curve")
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
}

func freshPub(t *testing.T) *ecdsa.PublicKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &k.PublicKey
}

// The kid is the RFC 7638 thumbprint: SHA-256 over the canonical JWK with
// the required EC members in lexicographic order, base64url without padding.
func TestSessionKeyThumbprintIsRFC7638(t *testing.T) {
	pub := basePointKey(t)
	xb := make([]byte, 32)
	yb := make([]byte, 32)
	pub.X.FillBytes(xb)
	pub.Y.FillBytes(yb)
	b64 := base64.RawURLEncoding.EncodeToString
	canonical := `{"crv":"P-256","kty":"EC","x":"` + b64(xb) + `","y":"` + b64(yb) + `"}`
	sum := sha256.Sum256([]byte(canonical))
	want := b64(sum[:])
	got, err := SessionKeyThumbprint(pub)
	if err != nil {
		t.Fatalf("thumbprint: %v", err)
	}
	if got != want {
		t.Fatalf("thumbprint = %q, want %q", got, want)
	}
}

func TestSessionKeyThumbprintRefusesOtherCurves(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SessionKeyThumbprint(&k.PublicKey); err == nil {
		t.Fatal("a P-384 key was given a thumbprint")
	}
}

func TestSessionKeySetRefusesEqualKeys(t *testing.T) {
	a, b := freshPub(t), freshPub(t)
	cases := []struct {
		name                  string
		current, next, previous *ecdsa.PublicKey
	}{
		{"current equals next", a, a, nil},
		{"current equals previous", a, nil, a},
		{"next equals previous", a, b, b},
		{"all three equal", a, a, a},
		{"no current", nil, b, nil},
	}
	for _, c := range cases {
		if _, err := NewSessionKeySet(c.current, c.next, c.previous); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	if _, err := NewSessionKeySet(a, b, nil); err != nil {
		t.Errorf("distinct keys refused: %v", err)
	}
}

func TestSessionKeySetResolvesCurrentNextAndPrevious(t *testing.T) {
	cur, next, prev, other := freshPub(t), freshPub(t), freshPub(t), freshPub(t)
	set, err := NewSessionKeySet(cur, next, prev)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]*ecdsa.PublicKey{"current": cur, "next": next, "previous": prev} {
		kid, _ := SessionKeyThumbprint(want)
		got, ok, err := set.KeyByID(context.Background(), kid)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", name, ok, err)
		}
		if !want.Equal(got) {
			t.Fatalf("%s: wrong key", name)
		}
	}
	otherKid, _ := SessionKeyThumbprint(other)
	if _, ok, err := set.KeyByID(context.Background(), otherKid); ok || err != nil {
		t.Fatalf("unknown kid: ok=%v err=%v", ok, err)
	}
}

// The golden: the exact bytes the route serves for a fixed public key.
func TestSessionJWKSGolden(t *testing.T) {
	set, err := NewSessionKeySet(basePointKey(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	kid, _ := SessionKeyThumbprint(basePointKey(t))
	want := `{"keys":[{"kty":"EC","crv":"P-256","x":"axfR8uEsQkf4vOblY6RA8ncDfYEt6zOg9KE5RdiYwpY","y":"T-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU","kid":"` + kid + `","use":"sig","alg":"ES256"}]}`
	if got := string(set.JWKS()); got != want {
		t.Fatalf("JWK Set =\n%s\nwant\n%s", got, want)
	}
}

func TestSessionJWKSOrderAndNoPrivateMember(t *testing.T) {
	cur, next, prev := freshPub(t), freshPub(t), freshPub(t)
	set, err := NewSessionKeySet(cur, next, prev)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(set.JWKS(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Keys) != 3 {
		t.Fatalf("keys = %d", len(doc.Keys))
	}
	allowed := map[string]bool{"kty": true, "crv": true, "x": true, "y": true, "kid": true, "use": true, "alg": true}
	for i, k := range doc.Keys {
		for member := range k {
			if !allowed[member] {
				t.Errorf("key %d carries member %q", i, member)
			}
		}
		if k["use"] != "sig" || k["alg"] != "ES256" || k["kty"] != "EC" || k["crv"] != "P-256" {
			t.Errorf("key %d: %v", i, k)
		}
	}
	for i, want := range []*ecdsa.PublicKey{cur, next, prev} {
		kid, _ := SessionKeyThumbprint(want)
		if doc.Keys[i]["kid"] != kid {
			t.Errorf("key %d is not in current, next, previous order", i)
		}
	}
}

func TestSessionJWKSHandler(t *testing.T) {
	set, err := NewSessionKeySet(freshPub(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := set.JWKSHandler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if rec.Body.String() != string(set.JWKS()) {
		t.Fatal("body is not the JWK Set")
	}
	// The remote key source reads what the route serves.
	srv := httptest.NewServer(h)
	defer srv.Close()
	remote, err := NewRemoteJWKS(srv.URL, 0, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	kid := set.CurrentKID()
	if _, ok, err := remote.KeyByID(context.Background(), kid); err != nil || !ok {
		t.Fatalf("remote verifier cannot read the served set: ok=%v err=%v", ok, err)
	}
}

func TestParseSessionPublicKeyPEM(t *testing.T) {
	pub := freshPub(t)
	der, err := marshalPKIXForTest(pub)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSessionPublicKeyPEM(der)
	if err != nil || !pub.Equal(got) {
		t.Fatalf("round trip: %v", err)
	}
	for _, bad := range [][]byte{nil, []byte("not pem"), []byte("-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n")} {
		if _, err := ParseSessionPublicKeyPEM(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
