package testkit

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// The token vocabulary, pinned by the ADR-0020 addendum and the B2 review
// (review notes item 3): the minter stamps iss, sub, aud, iat, exp, tenant,
// and nothing else consumed. There is no org claim: the staff caller's org
// selection is the X-Org header, and a tenant's org comes from the Team
// behind the token per request. Staff-class tokens carry no authority
// claims and none are read: role, tier and branch authority resolve from
// the app-role tables per request (D3).
const (
	KitIssuer    = "fx-kit-issuer"
	KitStaffAud  = "fx-kit-staff-audience"
	KitTenantAud = "fx-kit-tenant-audience"
)

// Token windows: issued just before now, expiring well after now, so the
// positive legs are inside the window and the negative legs are one field
// away. The verifier applies clock tolerance to issued-at only; expiry
// gets no grace.
const (
	tokenIssuedAgo  = time.Minute
	tokenValidFor   = 10 * time.Minute
	tokenExpiredAgo = time.Hour
)

// BearerClass selects the token shape's audience and the tenant claim.
type BearerClass int

const (
	// StaffClass mints the staff shape: the staff audience, no tenant claim.
	StaffClass BearerClass = iota
	// TenantClass mints the tenant shape: the tenant audience plus the
	// tenant claim carrying the contractor account UUID.
	TenantClass
)

// String names the class for failure messages.
func (c BearerClass) String() string {
	switch c {
	case StaffClass:
		return "staff"
	case TenantClass:
		return "tenant"
	default:
		return "unknown"
	}
}

// Minter is the sweep token issuer. Caller-chosen claims, kid, signature
// key, algorithm and expiry, so every negative-space leg of the sweeps is
// one TokenShape field away. It mirrors the B2 test issuers and adds the
// sweep convenience shapes on top.
type Minter struct {
	Keys  *Keys
	Clock *Clock
	// Session, when set, is the core's persistent session signing key
	// (CORE_SESSION_SIGNING_KEY_FILE): a token with no key or kid named in
	// its shape is then signed ES256 under that key with the thumbprint
	// kid, which is what a console mode composition verifies. Shapes that
	// name their own kid, key or algorithm keep them, so the negative legs
	// stay one field away. A shape that names its own kid, key or algorithm
	// carries a kid the console key set does not hold, so the composition
	// refuses it as an UNKNOWN KID, not for the signature or algorithm it
	// meant to exercise. To exercise those checks under the real kid, set
	// TokenShape.SessionKID (the session thumbprint kid with whatever key
	// and algorithm the shape names; with none named, a different EC key).
	Session *ecdsa.PrivateKey
}

// NewMinter generates fresh keys and a fresh clock. Nothing crosses runs.
func NewMinter(t *testing.T) *Minter {
	return &Minter{Keys: NewKeys(t), Clock: NewClock()}
}

// TokenShape overrides the pinned token shape for the negative-space legs.
// The zero value changes nothing. A nil pointer field drops the claim, a
// non-nil one replaces it; Extra mints authority-looking junk that no code
// may consume (the never-read leg of the B2 antecedents).
type TokenShape struct {
	// KID names the signing key id. Empty means the active RSA kid.
	KID string
	// Key overrides the signing key. nil means the key the KID implies
	// where a named constructor provides one, else the active RSA key.
	Key *rsa.PrivateKey
	// ECKey selects ES256 signing with this key (with its kid).
	ECKey *ecdsa.PrivateKey
	// Alg overrides the algorithm header. Empty means RS256, or ES256 when
	// ECKey is set. "HS256" builds the alg-confusion token.
	Alg string
	// Issuer replaces iss when non-nil.
	Issuer *string
	// Subject replaces sub when non-nil.
	Subject *string
	// Audience replaces aud: a string, a []string (the dual-audience
	// poison shape), or nil to drop the claim entirely.
	Audience any
	// IssuedAt replaces iat when non-nil; nil means the in-window value.
	IssuedAt *time.Time
	// ExpiresAt replaces exp when non-nil; nil means the in-window value.
	ExpiresAt *time.Time
	// NoIat and NoExp drop the claims outright (the required-claim legs).
	NoIat bool
	NoExp bool
	// Tenant replaces the tenant claim on the tenant shape when non-nil,
	// or stamps one on the staff shape (never consumed there).
	Tenant *string
	// NoTenant drops the tenant claim from the tenant shape.
	NoTenant bool
	// SessionKID, with Minter.Session set, names the session key's real
	// thumbprint kid instead of the kit's own, so a bad signature (Key or
	// ECKey of another key) or a wrong algorithm is judged by the signature
	// and algorithm checks and not refused earlier as an unknown kid. With
	// no Key, ECKey or Alg named, the token is signed ES256 under the kit's
	// own EC key: a forged signature under the real kid.
	SessionKID bool
	// Extra carries claims that look like authority. Nothing may read
	// them; a sweep proves it behaviorally.
	Extra map[string]any
}

// Mint builds and signs one token of the given class for sub. The base
// shape is the pinned vocabulary; shape then applies its overrides.
func (m *Minter) Mint(t *testing.T, class BearerClass, sub string, shape TokenShape) string {
	t.Helper()
	now := m.Clock.Now()
	claims := map[string]any{
		"iss": KitIssuer,
		"sub": sub,
	}
	switch class {
	case StaffClass:
		claims["aud"] = KitStaffAud
	case TenantClass:
		claims["aud"] = KitTenantAud
		claims["tenant"] = "fx-kit-tenant-required-placeholder"
	default:
		t.Fatalf("testkit: unknown bearer class %d", class)
	}
	claims["iat"] = now.Add(-tokenIssuedAgo).Unix()
	claims["exp"] = now.Add(tokenValidFor).Unix()

	// Overrides.
	if shape.Issuer != nil {
		claims["iss"] = *shape.Issuer
	}
	if shape.Subject != nil {
		claims["sub"] = *shape.Subject
	}
	if shape.Audience != nil {
		claims["aud"] = shape.Audience
	}
	if shape.IssuedAt != nil {
		claims["iat"] = shape.IssuedAt.Unix()
	}
	if shape.ExpiresAt != nil {
		claims["exp"] = shape.ExpiresAt.Unix()
	}
	if shape.NoIat {
		delete(claims, "iat")
	}
	if shape.NoExp {
		delete(claims, "exp")
	}
	if shape.Tenant != nil {
		claims["tenant"] = *shape.Tenant
	}
	if shape.NoTenant {
		delete(claims, "tenant")
	}
	for k, v := range shape.Extra {
		claims[k] = v
	}

	if m.Session != nil && shape.SessionKID {
		sessionKID, err := identity.SessionKeyThumbprint(&m.Session.PublicKey)
		if err != nil {
			t.Fatalf("testkit: session key thumbprint: %v", err)
		}
		shape.KID = sessionKID
		if shape.ECKey == nil && shape.Key == nil && shape.Alg == "" {
			shape.ECKey = m.Keys.ECActive
		}
	}
	if m.Session != nil && shape.KID == "" && shape.ECKey == nil && shape.Key == nil && shape.Alg == "" {
		sessionKID, err := identity.SessionKeyThumbprint(&m.Session.PublicKey)
		if err != nil {
			t.Fatalf("testkit: session key thumbprint: %v", err)
		}
		shape.KID, shape.ECKey = sessionKID, m.Session
	}
	kid := shape.KID
	if kid == "" {
		kid = KidActiveRSA
	}
	alg := shape.Alg
	switch {
	case alg == "":
		if shape.ECKey != nil {
			alg = "ES256"
		} else {
			alg = "RS256"
		}
	case alg == "HS256":
		// The confusion token: the symmetric secret is the kit's own RSA
		// public key material, the classic attack on a verifier that
		// trusts the token's alg header.
	default:
		// caller-named algorithm, signed below or refused by the signer.
	}
	header := map[string]any{"alg": alg, "kid": kid, "typ": "JWT"}
	return m.sign(t, header, claims, shape)
}

// sign produces the compact serialization, mirroring the B2 issuer signing
// paths exactly: RS256 and ES256 asymmetric, HS256 only for the confusion
// token, and the public key DER as the symmetric secret.
func (m *Minter) sign(t *testing.T, header, claims map[string]any, shape TokenShape) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("testkit: marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("testkit: marshal claims: %v", err)
	}
	signed := b64url(hb) + "." + b64url(cb)
	var sig []byte
	switch header["alg"] {
	case "RS256":
		key := shape.Key
		if key == nil {
			key = m.Keys.RSAActive
		}
		digest := sha256.Sum256([]byte(signed))
		sig, err = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("testkit: sign rs256: %v", err)
		}
	case "ES256":
		key := shape.ECKey
		if key == nil {
			key = m.Keys.ECActive
		}
		digest := sha256.Sum256([]byte(signed))
		r, s, err2 := ecdsa.Sign(rand.Reader, key, digest[:])
		if err2 != nil {
			t.Fatalf("testkit: sign es256: %v", err2)
		}
		// JWS elliptic signatures are the fixed-width r||s concatenation.
		rb := make([]byte, 32)
		sb := make([]byte, 32)
		r.FillBytes(rb)
		s.FillBytes(sb)
		sig = append(rb, sb...)
	case "HS256":
		pub := m.Keys.RSAActive.PublicKey
		if shape.Key != nil {
			pub = shape.Key.PublicKey
		}
		der, err2 := x509.MarshalPKIXPublicKey(&pub)
		if err2 != nil {
			t.Fatalf("testkit: marshal public key: %v", err2)
		}
		mac := hmac.New(sha256.New, der)
		mac.Write([]byte(signed))
		sig = mac.Sum(nil)
	default:
		t.Fatalf("testkit: the kit issuer cannot sign alg %v", header["alg"])
	}
	return signed + "." + b64url(sig)
}

// StaffToken mints the in-window staff bearer for sub.
func (m *Minter) StaffToken(t *testing.T, sub string) string {
	t.Helper()
	return m.Mint(t, StaffClass, sub, TokenShape{})
}

// TenantToken mints the in-window tenant bearer for sub carrying the
// contractor account tenantID.
func (m *Minter) TenantToken(t *testing.T, sub, tenantID string) string {
	t.Helper()
	return m.Mint(t, TenantClass, sub, TokenShape{Tenant: &tenantID})
}

// StaffTokenWithAuthorityJunk mints a valid staff token stuffed with
// authority-looking claims, proving behaviorally that no code consumes
// them: authority resolves from the app-role tables, never the token.
func (m *Minter) StaffTokenWithAuthorityJunk(t *testing.T, sub string) string {
	t.Helper()
	return m.Mint(t, StaffClass, sub, TokenShape{Extra: map[string]any{
		"role":  "super_admin",
		"tier":  1,
		"org":   "fixture-north",
		"scope": "global",
	}})
}

// ECStaffToken mints the staff shape signed ES256, so both allowed
// algorithms are covered by a positive leg.
func (m *Minter) ECStaffToken(t *testing.T, sub string) string {
	t.Helper()
	return m.Mint(t, StaffClass, sub, TokenShape{KID: KidActiveEC, ECKey: m.Keys.ECActive})
}

// ExpiredToken mints a well-signed token whose exp has passed: the expiry
// leg, valid key, no grace.
func (m *Minter) ExpiredToken(t *testing.T, class BearerClass, sub, tenantID string) string {
	t.Helper()
	now := m.Clock.Now()
	exp := now.Add(-tokenExpiredAgo)
	iat := now.Add(-2 * tokenExpiredAgo)
	shape := TokenShape{ExpiresAt: &exp, IssuedAt: &iat}
	if class == TenantClass {
		tn := tenantID
		shape.Tenant = &tn
	}
	return m.Mint(t, class, sub, shape)
}

// WrongKeyToken mints a token that names the active kid but is signed by
// the key the served set never carries: the signature-will-not-verify leg.
func (m *Minter) WrongKeyToken(t *testing.T, class BearerClass, sub, tenantID string) string {
	t.Helper()
	shape := TokenShape{Key: m.Keys.RSAWrong}
	if class == TenantClass {
		tn := tenantID
		shape.Tenant = &tn
	}
	return m.Mint(t, class, sub, shape)
}

// UnknownKidToken mints a token naming a kid no served set has ever
// carried.
func (m *Minter) UnknownKidToken(t *testing.T, class BearerClass, sub, tenantID string) string {
	t.Helper()
	shape := TokenShape{KID: KidNeverKnown, Key: m.Keys.RSAWrong}
	if class == TenantClass {
		tn := tenantID
		shape.Tenant = &tn
	}
	return m.Mint(t, class, sub, shape)
}

// RetiredKidToken mints a token signed by and naming the retired key.
// Before KeyServer.Rotate it verifies; after, it is the unknown-kid leg
// (the served set rotated under the token).
func (m *Minter) RetiredKidToken(t *testing.T, class BearerClass, sub, tenantID string) string {
	t.Helper()
	shape := TokenShape{KID: KidRetiredRSA, Key: m.Keys.RSARetired}
	if class == TenantClass {
		tn := tenantID
		shape.Tenant = &tn
	}
	return m.Mint(t, class, sub, shape)
}

// ConfusionToken mints the alg-confusion token: presented as HS256, the
// legacy issuer's algorithm, with the symmetric secret being the kit's own
// RSA public key material.
func (m *Minter) ConfusionToken(t *testing.T, class BearerClass, sub, tenantID string) string {
	t.Helper()
	shape := TokenShape{Alg: "HS256"}
	if class == TenantClass {
		tn := tenantID
		shape.Tenant = &tn
	}
	return m.Mint(t, class, sub, shape)
}

// DualAudienceToken mints the poison shape claiming both bearer audiences:
// a token of both classes is nobody's token and must be refused.
func (m *Minter) DualAudienceToken(t *testing.T, sub, tenantID string) string {
	t.Helper()
	tn := tenantID
	return m.Mint(t, StaffClass, sub, TokenShape{
		Audience: []string{KitStaffAud, KitTenantAud},
		Tenant:   &tn,
	})
}

// ForeignAudienceToken mints a token claiming an audience this API does not
// configure (the customer audience once it exists is still nobody here).
func (m *Minter) ForeignAudienceToken(t *testing.T, sub string) string {
	t.Helper()
	return m.Mint(t, StaffClass, sub, TokenShape{Audience: "fx-kit-customer-audience"})
}

// MissingClaimTokens returns the required-claim legs: no iat, no exp, no
// sub, wrong issuer. Each is well-signed, so the failure names the claim
// discipline, not the signature.
func (m *Minter) MissingClaimTokens(t *testing.T, sub string) map[string]string {
	t.Helper()
	foreign := "fx-kit-other-issuer"
	empty := ""
	return map[string]string{
		"no-iat":      m.Mint(t, StaffClass, sub, TokenShape{NoIat: true}),
		"no-exp":      m.Mint(t, StaffClass, sub, TokenShape{NoExp: true}),
		"no-sub":      m.Mint(t, StaffClass, sub, TokenShape{Subject: &empty}),
		"foreign-iss": m.Mint(t, StaffClass, sub, TokenShape{Issuer: &foreign}),
	}
}

// TenantWithoutTenantClaim mints a tenant-audience token missing the
// tenant claim: the contractor account linkage is required on the class.
func (m *Minter) TenantWithoutTenantClaim(t *testing.T, sub string) string {
	t.Helper()
	return m.Mint(t, TenantClass, sub, TokenShape{NoTenant: true})
}

// GarbageBearers returns the not-parseable-JWT variants of SW-2b: a
// non-JWT string, a truncated real token, random bytes, and a bare dot
// run. The no-token-at-all leg is separate (Do with an empty bearer sends
// no Authorization header). None may distinguish itself from the others
// in any response.
func (m *Minter) GarbageBearers(t *testing.T) []string {
	t.Helper()
	valid := m.StaffToken(t, "fxstaff-garbage-source")
	truncated := valid[:len(valid)-8]
	random := make([]byte, 48)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("testkit: read random bytes: %v", err)
	}
	return []string{
		"bearer-is-missing",
		truncated,
		base64.RawURLEncoding.EncodeToString(random),
		".....",
	}
}

// VerifierConfig returns an identity.Config pointed at the kit's key
// server: the remote JWKS form, which is the F3 injectability proof shape.
// The short cache TTL and refresh floor are the kit constants, so Rotate
// and Fail are observed without sleeping.
func (m *Minter) VerifierConfig(server *KeyServer) identity.Config {
	return identity.Config{
		JWKSURL:        server.URL(),
		KeyCacheTTL:    verifierKeyCacheTTL,
		RefreshFloor:   verifierRefreshFloor,
		HTTPClient:     VerifierHTTPClient(),
		Issuer:         KitIssuer,
		StaffAudience:  KitStaffAud,
		TenantAudience: KitTenantAud,
		Now:            m.Clock.Now,
	}
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
