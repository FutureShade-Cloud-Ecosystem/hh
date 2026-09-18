package identity

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// KeySource resolves verification keys by key id. Implementations must
// fail closed: an error is an error, never a miss, and a miss is never an
// error.
type KeySource interface {
	KeyByID(ctx context.Context, kid string) (key crypto.PublicKey, ok bool, err error)
}

// RemoteJWKS fetches and caches a JWK Set over HTTP. It serves keys from
// cache until the cache TTL passes, refetches on the next lookup after
// that, and treats a fetch failure as a hard failure: an expired cache is
// never served, because serving keys nobody could revalidate is a fail-open
// path. An unknown kid inside a still-live cache triggers at most one
// refetch per refresh floor, so key rotation is picked up promptly while
// garbage kids cannot be used to hammer the JWK Set endpoint.
type RemoteJWKS struct {
	url          string
	client       *http.Client
	ttl          time.Duration
	refreshFloor time.Duration
	now          func() time.Time

	mu        sync.Mutex
	keys      map[string]crypto.PublicKey
	fetched   time.Time
	expiresAt time.Time
}

// NewRemoteJWKS validates the URL and returns the key source. The cache
// and clock behavior is injected here and through Config, never hardcoded.
func NewRemoteJWKS(jwksURL string, ttl, refreshFloor time.Duration, client *http.Client, now func() time.Time) (*RemoteJWKS, error) {
	if err := validJWKSURL(jwksURL); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = defaultKeyCacheTTL
	}
	// The floor defaults on zero and on negative alike: Config.RefreshFloor
	// documents that zero selects the package default, and a negative value
	// is invalid exactly the same way (the serve configuration refuses one
	// outright). No construction path may install a zero floor, because a
	// zero floor never throttles: every unknown-kid lookup inside a live
	// cache would fetch the JWK Set under the cache mutex, hammering the
	// platform endpoint from an unauthenticated trigger (ADR-0020 addendum:
	// an unknown key id triggers at most one refetch per configured floor).
	if refreshFloor <= 0 {
		refreshFloor = defaultRefreshFloor
	}
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if now == nil {
		now = time.Now
	}
	return &RemoteJWKS{
		url:          jwksURL,
		client:       client,
		ttl:          ttl,
		refreshFloor: refreshFloor,
		now:          now,
	}, nil
}

// KeyByID implements KeySource. The fetch runs under the lock: one caller
// refetches and the rest wait, which is the single-flight property, at the
// cost of serializing behind one platform endpoint. That is the correct
// trade here: the endpoint is one per deployment and the middleware holds
// no pool that could deadlock behind it.
func (r *RemoteJWKS) KeyByID(ctx context.Context, kid string) (crypto.PublicKey, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.keys != nil && now.Before(r.expiresAt) {
		if key, ok := r.keys[kid]; ok {
			return key, true, nil
		}
		// Unknown kid in a live set: one gated refetch so rotation is
		// picked up, then fail closed.
		if now.Sub(r.fetched) < r.refreshFloor {
			return nil, false, nil
		}
		if err := r.refresh(ctx); err != nil {
			return nil, false, err
		}
		key, ok := r.keys[kid]
		return key, ok, nil
	}
	if err := r.refresh(ctx); err != nil {
		return nil, false, err
	}
	key, ok := r.keys[kid]
	return key, ok, nil
}

// refresh fetches and replaces the cached set. The caller holds the lock.
func (r *RemoteJWKS) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("identity: JWK Set endpoint answered %d", resp.StatusCode)
	}
	var set jwkSet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return err
	}
	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, jwk := range set.Keys {
		if jwk.KID == "" || jwk.Kty == "" {
			continue
		}
		if jwk.Use != "" && jwk.Use != "sig" {
			continue
		}
		key, err := jwk.publicKey()
		if err != nil {
			// An unparseable key is an unusable key: skip it, never
			// fail the whole set over one bad entry, and never
			// construct symmetric material from kty oct.
			continue
		}
		if _, dup := keys[jwk.KID]; dup {
			// First entry wins a duplicate kid; the platform JWK
			// Set is trusted content, this only keeps the map
			// deterministic.
			continue
		}
		keys[jwk.KID] = key
	}
	now := r.now()
	r.keys = keys
	r.fetched = now
	r.expiresAt = now.Add(r.ttl)
	return nil
}

// jwkSet and jwk are the RFC 7517 shapes this verifier consumes.
type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	KID string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// publicKey builds the Go public key from the JWK fields.
func (j jwk) publicKey() (crypto.PublicKey, error) {
	switch j.Kty {
	case "RSA":
		return j.rsaKey()
	case "EC":
		return j.ecKey()
	default:
		return nil, fmt.Errorf("identity: unsupported key type %q", j.Kty)
	}
}

func (j jwk) rsaKey() (crypto.PublicKey, error) {
	nb, err := decodeBase64URL(j.N)
	if err != nil {
		return nil, err
	}
	eb, err := decodeBase64URL(j.E)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nb)
	e := new(big.Int).SetBytes(eb)
	if n.Sign() <= 0 || !e.IsInt64() || e.Int64() <= 0 || e.Int64() > math.MaxInt32 {
		return nil, fmt.Errorf("identity: malformed RSA key")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func (j jwk) ecKey() (crypto.PublicKey, error) {
	var curve elliptic.Curve
	switch j.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("identity: unsupported curve %q", j.Crv)
	}
	xb, err := decodeBase64URL(j.X)
	if err != nil {
		return nil, err
	}
	yb, err := decodeBase64URL(j.Y)
	if err != nil {
		return nil, err
	}
	x := new(big.Int).SetBytes(xb)
	y := new(big.Int).SetBytes(yb)
	if x.Cmp(curve.Params().N) >= 0 || y.Cmp(curve.Params().N) >= 0 || !curve.IsOnCurve(x, y) {
		return nil, fmt.Errorf("identity: EC point is not on the curve")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// staticKeys is a fixed in-memory key set for tests and embedded
// verifications.
type staticKeys struct {
	keys map[string]crypto.PublicKey
}

// NewStaticKeySource returns a KeySource over a fixed key set. Test
// support: production serves its keys through a JWK Set endpoint.
func NewStaticKeySource(keys map[string]crypto.PublicKey) KeySource {
	copied := make(map[string]crypto.PublicKey, len(keys))
	for k, v := range keys {
		copied[k] = v
	}
	return &staticKeys{keys: copied}
}

func (s *staticKeys) KeyByID(_ context.Context, kid string) (crypto.PublicKey, bool, error) {
	key, ok := s.keys[kid]
	return key, ok, nil
}
