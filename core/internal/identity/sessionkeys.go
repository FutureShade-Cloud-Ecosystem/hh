package identity

// The public half of the core's own session keys: the thumbprint kid, the
// in process key source the verifier reads, and the JWK Set document the
// public route serves. Nothing in this file holds, parses or writes a
// private key; the signing path lives apart in sessionsigner.go and no
// verifier code imports it.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strconv"
)

// SessionKeyThumbprint is the RFC 7638 thumbprint of an EC P-256 public
// key: SHA-256 over the canonical JWK carrying only crv, kty, x and y in
// lexicographic order, base64url without padding. It is the kid of every
// session token and of the key's JWK Set entry.
func SessionKeyThumbprint(pub *ecdsa.PublicKey) (string, error) {
	x, y, err := sessionKeyCoordinates(pub)
	if err != nil {
		return "", err
	}
	canonical := `{"crv":"P-256","kty":"EC","x":"` + x + `","y":"` + y + `"}`
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// sessionKeyCoordinates returns the fixed width base64url x and y of a
// P-256 public key, refusing any other curve or an off curve point.
func sessionKeyCoordinates(pub *ecdsa.PublicKey) (x, y string, err error) {
	if pub == nil || pub.X == nil || pub.Y == nil || pub.Curve != elliptic.P256() {
		return "", "", errors.New("identity: session key must be an EC P-256 public key")
	}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return "", "", errors.New("identity: session key point is not on the curve")
	}
	xb := make([]byte, 32)
	yb := make([]byte, 32)
	pub.X.FillBytes(xb)
	pub.Y.FillBytes(yb)
	enc := base64.RawURLEncoding.EncodeToString
	return enc(xb), enc(yb), nil
}

// ParseSessionPublicKeyPEM parses one PEM "PUBLIC KEY" block holding an EC
// P-256 key. A private key block is refused: NEXT and PREVIOUS files carry
// public halves only. Errors are static text; file content never enters
// them.
func ParseSessionPublicKeyPEM(data []byte) (*ecdsa.PublicKey, error) {
	block := singlePEMBlock(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("identity: session public key file is not a PEM public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("identity: session public key file is not a PEM public key")
	}
	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errors.New("identity: session public key must be an EC P-256 key")
	}
	return pub, nil
}

// singlePEMBlock decodes a file that holds exactly one PEM block: no text
// before it and nothing but whitespace after it. A second block (a private
// key behind a public one) or stray text makes the whole file unusable, so
// a file the procedure treats as public can never carry private material.
func singlePEMBlock(data []byte) *pem.Block {
	if !bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("-----BEGIN ")) {
		return nil
	}
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil
	}
	return block
}

// sessionKeyEntry is one served key.
type sessionKeyEntry struct {
	pub *ecdsa.PublicKey
	kid string
	x   string
	y   string
}

func newSessionKeyEntry(pub *ecdsa.PublicKey) (sessionKeyEntry, error) {
	x, y, err := sessionKeyCoordinates(pub)
	if err != nil {
		return sessionKeyEntry{}, err
	}
	kid, err := SessionKeyThumbprint(pub)
	if err != nil {
		return sessionKeyEntry{}, err
	}
	return sessionKeyEntry{pub: pub, kid: kid, x: x, y: y}, nil
}

// SessionKeySet holds the public halves of the current, NEXT and PREVIOUS
// session keys. It is a KeySource, so the core verifies its own tokens
// with no HTTP hop, and it renders the JWK Set the public route serves.
// It is immutable after construction.
type SessionKeySet struct {
	entries []sessionKeyEntry // current, then NEXT, then PREVIOUS
	byKID   map[string]*ecdsa.PublicKey
	jwks    []byte
}

// NewSessionKeySet builds the set. The current key is required; NEXT and
// PREVIOUS may be nil. Two keys with the same thumbprint are refused: one
// key never occupies two slots.
func NewSessionKeySet(current, next, previous *ecdsa.PublicKey) (*SessionKeySet, error) {
	if current == nil {
		return nil, errors.New("identity: the current session key is required")
	}
	s := &SessionKeySet{byKID: make(map[string]*ecdsa.PublicKey, 3)}
	for _, pub := range []*ecdsa.PublicKey{current, next, previous} {
		if pub == nil {
			continue
		}
		e, err := newSessionKeyEntry(pub)
		if err != nil {
			return nil, err
		}
		if _, dup := s.byKID[e.kid]; dup {
			return nil, errors.New("identity: the current, NEXT and PREVIOUS session keys must all differ")
		}
		s.byKID[e.kid] = pub
		s.entries = append(s.entries, e)
	}
	doc := sessionJWKSDoc{Keys: make([]sessionJWK, 0, len(s.entries))}
	for _, e := range s.entries {
		doc.Keys = append(doc.Keys, sessionJWK{
			Kty: "EC", Crv: "P-256", X: e.x, Y: e.y, KID: e.kid, Use: "sig", Alg: "ES256",
		})
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, errors.New("identity: session JWK Set could not be rendered")
	}
	s.jwks = b
	return s, nil
}

// CurrentKID is the kid of the current key, the one new tokens carry.
func (s *SessionKeySet) CurrentKID() string { return s.entries[0].kid }

// KeyByID implements KeySource over the three slots.
func (s *SessionKeySet) KeyByID(_ context.Context, kid string) (crypto.PublicKey, bool, error) {
	pub, ok := s.byKID[kid]
	if !ok {
		return nil, false, nil
	}
	return pub, true, nil
}

// sessionJWKSDoc and sessionJWK fix the served member order. There is no
// private member in the type, so none can be served.
type sessionJWKSDoc struct {
	Keys []sessionJWK `json:"keys"`
}

type sessionJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	KID string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
}

// JWKS returns the JWK Set document: current, NEXT, PREVIOUS, in that order.
func (s *SessionKeySet) JWKS() []byte {
	return append([]byte(nil), s.jwks...)
}

// JWKSHandler serves GET /.well-known/jwks.json. It is public and
// cacheable for as long as a verifier caches its key set by default.
func (s *SessionKeySet) JWKSHandler() http.Handler {
	maxAge := "public, max-age=" + strconv.Itoa(int(defaultKeyCacheTTL.Seconds()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", maxAge)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(s.jwks)
		}
	})
}
