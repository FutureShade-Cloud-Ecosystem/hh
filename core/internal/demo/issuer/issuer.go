//go:build demo

// Package issuer is the demo's token issuer: an ES256 key generated in
// memory when the process starts, a minter over it, and the public half
// packaged as the static key source the verifier is constructed with. It
// exists so the demo build can mint its own session tokens while the
// identity package stays exactly what it is in every build: verification
// only, issuance nobody's but the issuer's (ADR-0016). The private key
// has no marshal path, no write path and no log path anywhere in this
// file: it is born here, signs here, and dies with the process, which is
// what makes a demo token worthless on any other box and worthless on
// this one after a restart (G5).
//
// The demo issuer is NewStaticKeySource's one non-test caller.
package issuer

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// KeyID is the single key id the demo issuer ever names. One process
// holds one key; the id only has to let the verifier find the public half
// in the static source it was handed.
const KeyID = "hh-demo-es256"

// ClaimIssuer is the iss value of every demo token and the Issuer value
// the demo build constructs its verifier with. It is a constant of the
// demo build, not an environment input: the verifier must accept exactly
// the issuer this package mints as and nothing else.
const ClaimIssuer = "hh-demo"

// TokenTTLVar is the environment name the serve wiring reads the token
// expiry from. It falls under the CORE_ prefix of the demo box's
// environment allow list (G4). Unset selects DefaultTTL; a set but
// unparseable or non positive value is a boot refusal, never a fallback.
const TokenTTLVar = "CORE_DEMO_TOKEN_TTL"

// DefaultTTL is the token expiry when the configuration does not name
// one. The expiry is deliberately short and the client cannot renew a
// token: when it runs out, the tester taps the role card again.
const DefaultTTL = 8 * time.Hour

// Issuer holds the in-memory key and the token vocabulary.
type Issuer struct {
	key      *ecdsa.PrivateKey
	audience string
	ttl      time.Duration
	now      func() time.Time
}

// New generates the key and returns the issuer. The audience is the staff
// audience the demo build constructed its verifier with, so every token
// classifies as a staff bearer and nothing else; the verifier's
// audience vocabulary is unchanged. A non positive ttl selects DefaultTTL.
func New(audience string, ttl time.Duration) (*Issuer, error) {
	if audience == "" {
		return nil, fmt.Errorf("demoissuer: audience is required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("demoissuer: key generation: %w", err)
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Issuer{key: key, audience: audience, ttl: ttl, now: time.Now}, nil
}

// KeySource returns the verifier's key source over the public half. The
// map holds the one key this process generated; the verifier consumes it
// unchanged, through the same KeySource seam the remote JWK Set fetcher
// normally fills, so no key set route exists anywhere in the demo build.
func (i *Issuer) KeySource() identity.KeySource {
	return identity.NewStaticKeySource(map[string]crypto.PublicKey{KeyID: i.key.Public()})
}

// Mint signs one session token for the account. The subject is the
// account's platform user id: the identity layer and the guards resolve
// what that subject may do from the database, per request, exactly as
// they do for any platform-issued bearer.
func (i *Issuer) Mint(subject string) (string, error) {
	return i.mintAt(subject, i.now())
}

// mintAt signs at an explicit moment; the isolation tests and the expiry
// test drive it with their own clocks.
func (i *Issuer) mintAt(subject string, at time.Time) (string, error) {
	if subject == "" {
		return "", fmt.Errorf("demoissuer: subject is required")
	}
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": KeyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"iss": ClaimIssuer,
		"sub": subject,
		"aud": i.audience,
		"iat": at.Unix(),
		"exp": at.Add(i.ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	b64 := base64.RawURLEncoding.EncodeToString
	signed := b64(header) + "." + b64(claims)
	digest := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, i.key, digest[:])
	if err != nil {
		return "", fmt.Errorf("demoissuer: sign: %w", err)
	}
	// JWS elliptic signatures are the fixed width r||s concatenation, not
	// ASN.1 DER, the same shape the verifier reads.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signed + "." + b64(sig), nil
}
