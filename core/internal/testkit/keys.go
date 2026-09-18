// Package testkit is the synthetic fixture kit for the HH-3.3 Phase B
// identity sweeps (QA B1 inventory, SW-0). Everything here is invented: no
// repository data, no real names, emails or addresses, no committed key
// material. Keys are generated at test runtime from the platform CSPRNG,
// never persisted, never printed, discarded with the process. The minter
// mirrors the test-only issuer idioms of core/internal/identity
// (testissuer_test.go) and core/internal/platform/authz (mint_test.go), and
// stamps the pinned vocabulary (ADR-0020 addendum): iss, sub, aud, iat, exp,
// tenant, and no org claim, which no code consumes. This package is test
// support only: it is imported by test files, never by the binary, and it
// writes fixture rows across module-owned tables deliberately, as database
// state for the sweeps, not as a module repository; the module fence rules
// bind module code, not the fixture kit that prepares a sweep's database.
package testkit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Key ids of the kit's runtime key set. One active RSA key, one retired RSA
// key (served until the set rotates, then the unknown-kid leg), one EC key
// (both allowed algorithms are exercised), and one key the served set never
// carries (the wrong-key leg).
const (
	KidActiveRSA  = "fx-kit-rsa-active"
	KidRetiredRSA = "fx-kit-rsa-retired"
	KidActiveEC   = "fx-kit-ec-active"
	KidWrongKey   = "fx-kit-rsa-wrong"
	KidNeverKnown = "fx-kit-never-served"
)

// Keys is the runtime-generated key set. Asymmetric only: nothing in the
// core under test holds any symmetric secret, and the HS256 secret that
// builds the alg-confusion token exists only inside the minter.
type Keys struct {
	RSAActive  *rsa.PrivateKey
	RSARetired *rsa.PrivateKey
	RSAWrong   *rsa.PrivateKey
	ECActive   *ecdsa.PrivateKey
}

// NewKeys generates the kit's key set from the platform CSPRNG.
func NewKeys(t *testing.T) *Keys {
	t.Helper()
	k := &Keys{}
	var err error
	if k.RSAActive, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatalf("testkit: generate active rsa key: %v", err)
	}
	if k.RSARetired, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatalf("testkit: generate retired rsa key: %v", err)
	}
	if k.RSAWrong, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatalf("testkit: generate wrong rsa key: %v", err)
	}
	if k.ECActive, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		t.Fatalf("testkit: generate active ec key: %v", err)
	}
	return k
}

// rsaJWK and ecJWK build RFC 7517 entries, mirroring the B2 issuer shapes.
func rsaJWK(pub rsa.PublicKey, kid string) jwkEntry {
	return jwkEntry{
		Kty: "RSA", KID: kid, Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func ecJWK(pub *ecdsa.PublicKey, kid string) jwkEntry {
	size := (pub.Curve.Params().N.BitLen() + 7) / 8
	xb := make([]byte, size)
	yb := make([]byte, size)
	pub.X.FillBytes(xb)
	pub.Y.FillBytes(yb)
	return jwkEntry{
		Kty: "EC", KID: kid, Use: "sig", Alg: "ES256", Crv: "P-256",
		X: base64.RawURLEncoding.EncodeToString(xb),
		Y: base64.RawURLEncoding.EncodeToString(yb),
	}
}

type jwkEntry struct {
	Kty string `json:"kty"`
	KID string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

type jwkSetDoc struct {
	Keys []jwkEntry `json:"keys"`
}

// JWKSJSON returns the served JWK Set document. activeOnly false carries the
// active and retired kids (the rotation-start set); activeOnly true carries
// the active kids only (the rotated set, which turns a token naming the
// retired kid into the unknown-kid leg).
func (k *Keys) JWKSJSON(activeOnly bool) string {
	entries := []jwkEntry{
		rsaJWK(k.RSAActive.PublicKey, KidActiveRSA),
		ecJWK(&k.ECActive.PublicKey, KidActiveEC),
	}
	if !activeOnly {
		entries = append(entries, rsaJWK(k.RSARetired.PublicKey, KidRetiredRSA))
	}
	b, err := json.Marshal(jwkSetDoc{Keys: entries})
	if err != nil {
		panic(err) // marshal of plain string fields cannot fail
	}
	return string(b)
}

// KeyServer serves the kit's JWK Set over HTTP, so a verifier is constructed
// against a live JWKS URL and the F3 injectability requirement is exercised
// in exactly the form the sweeps assert it. The served set can rotate and
// the endpoint can fail, both flippable between requests.
type KeyServer struct {
	srv        *httptest.Server
	keys       *Keys
	activeOnly atomic.Bool
	failing    atomic.Bool

	mu       sync.Mutex
	requests int
}

// NewKeyServer starts the server. It stops itself with the test.
func NewKeyServer(t *testing.T, keys *Keys) *KeyServer {
	t.Helper()
	ks := &KeyServer{keys: keys}
	ks.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ks.mu.Lock()
		ks.requests++
		ks.mu.Unlock()
		if ks.failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ks.keys.JWKSJSON(ks.activeOnly.Load())))
	}))
	t.Cleanup(ks.srv.Close)
	return ks
}

// URL is the JWK Set URL to hand identity.Config.JWKSURL.
func (ks *KeyServer) URL() string { return ks.srv.URL }

// Rotate drops the retired kid from the served set. A token naming the
// retired kid becomes an unknown kid to any verifier that refetches (the
// cache expires within the TTL below, or the gated unknown-kid refetch
// runs past the refresh floor).
func (ks *KeyServer) Rotate() { ks.activeOnly.Store(true) }

// Fail makes the endpoint answer 500, the unreachable JWK Set leg (503 at
// the boundary through the operational error class).
func (ks *KeyServer) Fail() { ks.failing.Store(true) }

// Requests counts endpoint fetches.
func (ks *KeyServer) Requests() int {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.requests
}

// Clock is a mutable clock shared by the minter and the verifier, so token
// windows and cache TTLs are driven without sleeping.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock at the current time.
func NewClock() *Clock { return &Clock{now: time.Now()} }

// Now implements the time source the verifier and minter take.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock, driving expiry windows and cache TTLs.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// Kit verifier tuning, stated as code per the house rule on committed
// values. The TTL and refresh floor keep the served-set cache short so
// Rotate and Fail take effect on the next request without sleeping; the
// HTTP timeout mirrors the B2 default.
const (
	verifierKeyCacheTTL  = 50 * time.Millisecond
	verifierRefreshFloor = time.Millisecond
	verifierHTTPTimeout  = 10 * time.Second
)

// VerifierHTTPClient is the client the harness hands the verifier so the
// served set is fetched against this kit's server with a bounded timeout.
func VerifierHTTPClient() *http.Client {
	return &http.Client{Timeout: verifierHTTPTimeout}
}
