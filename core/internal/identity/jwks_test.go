package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// countingServer serves JWK Set responses under test control and counts
// fetches, so cache behavior is measured, not assumed.
type countingServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits int
}

func (c *countingServer) add() { c.mu.Lock(); c.hits++; c.mu.Unlock() }
func (c *countingServer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

func serveJWKS(t *testing.T, next func() (int, string)) *countingServer {
	t.Helper()
	c := &countingServer{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.add()
		status, body := next()
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func TestRemoteJWKSFetchAndCache(t *testing.T) {
	k := generateTestKeys(t)
	set := k.jwksJSON()
	clk := newClock()
	srv := serveJWKS(t, func() (int, string) { return http.StatusOK, set })
	src, err := NewRemoteJWKS(srv.srv.URL, time.Hour, 0, srv.srv.Client(), clk.Now)
	if err != nil {
		t.Fatalf("NewRemoteJWKS: %v", err)
	}
	ctx := context.Background()

	if _, ok, err := src.KeyByID(ctx, testKeyID); err != nil || !ok {
		t.Fatalf("first lookup: ok=%v err=%v", ok, err)
	}
	if _, ok, err := src.KeyByID(ctx, testECKeyID); err != nil || !ok {
		t.Fatalf("second lookup: ok=%v err=%v", ok, err)
	}
	if hits := srv.count(); hits != 1 {
		t.Fatalf("cache did not hold: %d fetches", hits)
	}

	// After the TTL passes, the next lookup refetches.
	clk.advance(2 * time.Hour)
	if _, ok, err := src.KeyByID(ctx, testKeyID); err != nil || !ok {
		t.Fatalf("post-ttl lookup: ok=%v err=%v", ok, err)
	}
	if hits := srv.count(); hits != 2 {
		t.Fatalf("expected refetch after ttl, hits=%d", hits)
	}
}

// Rotation pickup: an unknown kid inside a live set triggers one refetch,
// and the refreshed set serves the new key.
func TestRemoteJWKSRotationPickup(t *testing.T) {
	k := generateTestKeys(t)
	old := generateTestKeys(t)
	clk := newClock()
	current := old.jwksJSON()
	srv := serveJWKS(t, func() (int, string) { return http.StatusOK, current })
	src, err := NewRemoteJWKS(srv.srv.URL, time.Hour, 0, srv.srv.Client(), clk.Now)
	if err != nil {
		t.Fatalf("NewRemoteJWKS: %v", err)
	}
	ctx := context.Background()

	if _, ok, err := src.KeyByID(ctx, testKeyID); err != nil || !ok {
		t.Fatalf("old key: ok=%v err=%v", ok, err)
	}

	// Rotate: the endpoint now serves a different active key under the
	// same kid slot naming a new kid.
	kid2 := "test-rsa-next"
	current = fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":%q,"use":"sig","n":%q,"e":%q}]}`,
		kid2,
		b64url(k.rsaActive.N.Bytes()),
		b64url([]byte{1, 0, 1}),
	)

	key, ok, err := src.KeyByID(ctx, kid2)
	if err != nil || !ok {
		t.Fatalf("rotated key not picked up: ok=%v err=%v", ok, err)
	}
	if key == nil {
		t.Fatalf("rotated key is nil")
	}
	if hits := srv.count(); hits != 2 {
		t.Fatalf("expected exactly one refetch, hits=%d", hits)
	}

	// The retired kid now fails closed.
	if _, ok, _ := src.KeyByID(ctx, testKeyID); ok {
		t.Fatalf("retired kid still resolves")
	}
}

// The refresh floor throttles refresh-on-miss so garbage kids cannot
// hammer the endpoint, and expires so rotation is still picked up.
func TestRemoteJWKSRefreshFloor(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	srv := serveJWKS(t, func() (int, string) { return http.StatusOK, k.jwksJSON() })
	src, err := NewRemoteJWKS(srv.srv.URL, time.Hour, time.Hour, srv.srv.Client(), clk.Now)
	if err != nil {
		t.Fatalf("NewRemoteJWKS: %v", err)
	}
	ctx := context.Background()

	if _, ok, err := src.KeyByID(ctx, testKeyID); err != nil || !ok {
		t.Fatalf("first lookup: ok=%v err=%v", ok, err)
	}
	if hits := srv.count(); hits != 1 {
		t.Fatalf("hits=%d", hits)
	}

	// Unknown kid while the floor stands: refused with no second fetch.
	if _, ok, _ := src.KeyByID(ctx, "no-such-kid"); ok {
		t.Fatalf("unknown kid resolved")
	}
	if hits := srv.count(); hits != 1 {
		t.Fatalf("floor did not throttle: hits=%d", hits)
	}

	// Once the floor passes, the miss refetches.
	clk.advance(2 * time.Hour)
	if _, ok, _ := src.KeyByID(ctx, "no-such-kid"); ok {
		t.Fatalf("unknown kid resolved after refresh")
	}
	if hits := srv.count(); hits != 2 {
		t.Fatalf("expected refetch after floor, hits=%d", hits)
	}
}

// Fail closed on fetch errors, including after a previously good fetch:
// an expired or failing set is never served stale.
func TestRemoteJWKSFailsClosed(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	status := http.StatusOK
	body := k.jwksJSON()
	srv := serveJWKS(t, func() (int, string) { return status, body })
	src, err := NewRemoteJWKS(srv.srv.URL, time.Hour, 0, srv.srv.Client(), clk.Now)
	if err != nil {
		t.Fatalf("NewRemoteJWKS: %v", err)
	}
	ctx := context.Background()

	if _, ok, err := src.KeyByID(ctx, testKeyID); err != nil || !ok {
		t.Fatalf("first lookup: ok=%v err=%v", ok, err)
	}

	clk.advance(2 * time.Hour)
	status = http.StatusInternalServerError
	if _, ok, err := src.KeyByID(ctx, testKeyID); err == nil || ok {
		t.Fatalf("stale keys served after failed fetch: ok=%v err=%v", ok, err)
	}

	status = http.StatusOK
	body = "{not json"
	if _, ok, err := src.KeyByID(ctx, testKeyID); err == nil || ok {
		t.Fatalf("garbage set accepted: ok=%v err=%v", ok, err)
	}
}

func TestRemoteJWKSSkipsUnusableKeys(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	set := `{"keys":[` +
		`{"kty":"RSA","kid":"enc-key","use":"enc","n":"aaa","e":"AQAB"},` +
		`{"kty":"oct","kid":"sym-key","k":"c2VjcmV0"},` +
		`{"kty":"RSA","kid":"bad-b64","n":"!!!","e":"AQAB"},` +
		`{"kty":"EC","kid":"off-curve","crv":"P-256","x":"AQ","y":"AQ"},` +
		`{"kty":"EC","crv":"P-256","x":"AQ","y":"AQ"},` +
		`{"kty":"RSA","kid":"dup","n":"AAA","e":"AQAB"},` +
		`{"kty":"RSA","kid":"dup","n":"BBB","e":"AQAB"},` +
		rsaJWK(k.rsaActive.PublicKey, "good-key", RS256).toJSON() +
		`]}`
	srv := serveJWKS(t, func() (int, string) { return http.StatusOK, set })
	src, err := NewRemoteJWKS(srv.srv.URL, time.Hour, 0, srv.srv.Client(), clk.Now)
	if err != nil {
		t.Fatalf("NewRemoteJWKS: %v", err)
	}
	ctx := context.Background()

	for _, kid := range []string{"enc-key", "sym-key", "bad-b64", "off-curve", ""} {
		if key, ok, _ := src.KeyByID(ctx, kid); ok {
			t.Fatalf("unusable key %q resolved: %v", kid, key)
		}
	}
	if _, ok, err := src.KeyByID(ctx, "good-key"); err != nil || !ok {
		t.Fatalf("good key refused: ok=%v err=%v", ok, err)
	}
}

// Base64url padding in JWK fields is tolerated.
func TestRemoteJWKSPaddedFields(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	j := rsaJWK(k.rsaActive.PublicKey, "padded", RS256)
	j.N = j.N + "=="
	j.E = j.E + "="
	set := `{"keys":[` + j.toJSON() + `]}`
	srv := serveJWKS(t, func() (int, string) { return http.StatusOK, set })
	src, err := NewRemoteJWKS(srv.srv.URL, time.Hour, 0, srv.srv.Client(), clk.Now)
	if err != nil {
		t.Fatalf("NewRemoteJWKS: %v", err)
	}
	if _, ok, err := src.KeyByID(context.Background(), "padded"); err != nil || !ok {
		t.Fatalf("padded key refused: ok=%v err=%v", ok, err)
	}
}

func TestRemoteJWKSConcurrentLookups(t *testing.T) {
	k := generateTestKeys(t)
	clk := newClock()
	srv := serveJWKS(t, func() (int, string) { return http.StatusOK, k.jwksJSON() })
	src, err := NewRemoteJWKS(srv.srv.URL, time.Hour, 0, srv.srv.Client(), clk.Now)
	if err != nil {
		t.Fatalf("NewRemoteJWKS: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := src.KeyByID(context.Background(), testKeyID); err != nil || !ok {
				t.Errorf("lookup: ok=%v err=%v", ok, err)
			}
		}()
	}
	wg.Wait()
	if hits := srv.count(); hits != 1 {
		t.Fatalf("expected a single fetch under concurrency, hits=%d", hits)
	}
}

func TestNewRemoteJWKSValidation(t *testing.T) {
	for _, url := range []string{"", "ftp://keys/jwks", "/relative"} {
		if _, err := NewRemoteJWKS(url, 0, 0, nil, nil); err == nil {
			t.Fatalf("%q accepted", url)
		}
	}
	if _, err := NewRemoteJWKS("http://localhost/jwks", 0, 0, nil, nil); err != nil {
		t.Fatalf("valid url refused: %v", err)
	}
}

// toJSON is test-only JSON for one JWK, kept beside the tests because the
// production type unmarshals through jwkSet only.
func (j jwk) toJSON() string {
	b, err := json.Marshal(j)
	if err != nil {
		panic(err)
	}
	return string(b)
}
