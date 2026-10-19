// Package identity verifies platform-issued bearer tokens against the
// platform JWK Set (ADR-0016, context section 4.4). Phase B replaces the
// ERP's two HS256 issuers with verification only: this file, and every
// verifier code path, has no signing path. The core's own session signing
// lives apart, in sessionsigner.go (a file and a type that no verifier
// code references, held by sessionfence_test.go), and the key source over
// its public halves in sessionkeys.go; the verifier reads them only through
// KeySource. A token
// is identity only; authority is resolved from the database per request and
// never read from claims (D3, docs/analysis/DECISIONS.md). Every
// verification input, the JWK Set source, the issuer, the audience
// vocabulary, the cache behavior and the clock, arrives by configuration,
// so a sweep can serve its own JWK Set (QA finding F3).
package identity

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Alg names an allowed digital-signature algorithm. Only asymmetric
// algorithms exist in this type: the HMAC family has no value here, and any
// alg outside the configured set is refused before key material is touched.
// That is the alg-confusion defense: a symmetric token signed with the
// verifier's own public key material is still refused.
type Alg string

const (
	RS256 Alg = "RS256"
	ES256 Alg = "ES256"
)

var supportedAlgs = map[Alg]bool{
	RS256: true,
	ES256: true,
}

// Cache and clock defaults. Stated as code, not prose, per the repository
// rule on committed text; every one of them is overridable through Config.
const (
	defaultKeyCacheTTL  = 5 * time.Minute
	defaultRefreshFloor = 30 * time.Second
	defaultClockSkew    = time.Minute
	defaultHTTPTimeout  = 10 * time.Second
)

// Config configures the verifier. The zero value fails construction: the
// required fields name the deployment's platform facts and nothing defaults
// silently.
type Config struct {
	// JWKSURL is the platform JWK Set URL. Required unless KeySource is
	// set. Never hardcoded, so no deployment or sweep is bound to another
	// one's key material.
	JWKSURL string
	// KeySource, when set, is used directly instead of a remote JWK Set
	// (tests, embedded verifications).
	KeySource KeySource
	// Issuer is the required token issuer value.
	Issuer string
	// StaffAudience and TenantAudience carry the bearer classification
	// vocabulary. No decision pins the platform's claim names; the two
	// configured audience values are the B2 proposal and are flagged for
	// the Tech Lead as an open arbitration.
	StaffAudience  string
	TenantAudience string
	// MachineAudience, with MachineKeySource or MachineJWKSURL, is the
	// third bearer class (ADR-0024, HH-LOAD-1 LD-2): FB Move's machine
	// credential audience. Optional with no default: with neither set, no
	// machine token verifies, exactly as before the class existed. A
	// machine audience token verifies only against the machine key set,
	// never the platform key set above.
	MachineAudience  string
	MachineJWKSURL   string
	MachineKeySource KeySource
	// Algorithms restricts the accepted signature algorithms. Empty means
	// RS256 and ES256.
	Algorithms []Alg
	// KeyCacheTTL bounds how long a fetched JWK Set is served without
	// refetching. Zero selects the package default.
	KeyCacheTTL time.Duration
	// RefreshFloor bounds how often an unknown kid may trigger a refresh,
	// so garbage kids cannot be used to hammer the JWK Set endpoint. Zero
	// selects the package default.
	RefreshFloor time.Duration
	// ClockSkew tolerates clock drift on not-before and issued-at checks.
	// Expiry gets no grace: an expired token fails closed. Zero selects
	// the package default.
	ClockSkew time.Duration
	// HTTPClient is used for JWK Set fetches. nil selects a client with
	// the package default timeout.
	HTTPClient *http.Client
	// Now is the clock. nil selects time.Now.
	Now func() time.Time
	// Revocations, when set, is consulted for every verified token that
	// carries a jti: a token whose id is revoked answers the credential
	// refusal "token revoked" (HH-678). nil means no revocation check
	// runs (the verifier alone, tests, deployments without the store).
	Revocations RevocationChecker
}

// Verifier verifies bearer tokens. It holds no per-request state and is
// safe for concurrent use; nothing about a token is memoized, so the
// credential is verified from scratch on every request (D28: the credential
// is never cached).
type Verifier struct {
	keys        KeySource
	issuer      string
	staffAud    string
	tenantAud   string
	machineAud  string
	machineKeys KeySource
	algs        map[Alg]bool
	skew        time.Duration
	now         func() time.Time
	// revocationsMu guards revocations, which is set once at boot by
	// SetRevocations after the pool exists (the composition builds the
	// verifier before the pool) and then only read.
	revocationsMu sync.Mutex
	revocations   RevocationChecker
}

// NewVerifier validates the configuration and returns the verifier. It
// fails closed on every incomplete or contradictory input: a misconfigured
// verifier must refuse to exist rather than refuse tokens at runtime.
func NewVerifier(cfg Config) (*Verifier, error) {
	if cfg.KeySource == nil {
		src, err := NewRemoteJWKS(cfg.JWKSURL, cfg.KeyCacheTTL, cfg.RefreshFloor, cfg.HTTPClient, cfg.Now)
		if err != nil {
			return nil, err
		}
		cfg.KeySource = src
	}
	if cfg.Issuer == "" {
		return nil, errors.New("identity: issuer is required")
	}
	if cfg.StaffAudience == "" || cfg.TenantAudience == "" {
		return nil, errors.New("identity: staff and tenant audience values are required")
	}
	if cfg.StaffAudience == cfg.TenantAudience {
		return nil, errors.New("identity: staff and tenant audience values must differ")
	}
	// The machine leg is all or nothing: the audience and its key set
	// arrive together or not at all, so no half configuration can exist
	// and no default key set can silently appear (ADR-0024).
	machineKeys := cfg.MachineKeySource
	if machineKeys == nil && cfg.MachineJWKSURL != "" {
		src, err := NewRemoteJWKS(cfg.MachineJWKSURL, cfg.KeyCacheTTL, cfg.RefreshFloor, cfg.HTTPClient, cfg.Now)
		if err != nil {
			return nil, err
		}
		machineKeys = src
	}
	if cfg.MachineAudience != "" && machineKeys == nil {
		return nil, errors.New("identity: machine audience requires a machine key set")
	}
	if cfg.MachineAudience == "" && machineKeys != nil {
		return nil, errors.New("identity: machine key set requires the machine audience")
	}
	if cfg.MachineAudience != "" &&
		(cfg.MachineAudience == cfg.StaffAudience || cfg.MachineAudience == cfg.TenantAudience) {
		return nil, errors.New("identity: machine audience must differ from the staff and tenant audiences")
	}
	algs := make(map[Alg]bool, len(cfg.Algorithms))
	if len(cfg.Algorithms) == 0 {
		cfg.Algorithms = []Alg{RS256, ES256}
	}
	for _, a := range cfg.Algorithms {
		if !supportedAlgs[a] {
			return nil, fmt.Errorf("identity: unsupported algorithm %q", a)
		}
		algs[a] = true
	}
	if cfg.ClockSkew <= 0 {
		cfg.ClockSkew = defaultClockSkew
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Verifier{
		keys:        cfg.KeySource,
		issuer:      cfg.Issuer,
		staffAud:    cfg.StaffAudience,
		tenantAud:   cfg.TenantAudience,
		machineAud:  cfg.MachineAudience,
		machineKeys: machineKeys,
		algs:        algs,
		skew:        cfg.ClockSkew,
		now:         cfg.Now,
		revocations: cfg.Revocations,
	}, nil
}

// SetRevocations attaches the revocation source after construction. The
// composition root builds the verifier before the database pool exists, so
// the store lands here once at boot, before the first request is served.
func (v *Verifier) SetRevocations(r RevocationChecker) {
	v.revocationsMu.Lock()
	v.revocations = r
	v.revocationsMu.Unlock()
}

// tokenHeader is the decoded JWS protected header.
type tokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

// Verify verifies one compact JWT and returns its verified claims. The
// returned error is always a *VerifyError; its class distinguishes who you
// are (credential), what shape the token is (malformed) and whether the
// verifier could run at all (operational). Every message is static text:
// token content never enters an error, so no response or log built from
// these errors can echo it.
func (v *Verifier) Verify(ctx context.Context, token string) (Token, error) {
	headerJSON, claimsJSON, sig, signed, err := splitCompact(token)
	if err != nil {
		return Token{}, err
	}
	var hdr tokenHeader
	if err := json.Unmarshal(headerJSON, &hdr); err != nil {
		return Token{}, malformedErr("token header is not JSON")
	}
	if !v.algs[Alg(hdr.Algorithm)] {
		// Refused before any key lookup: this is the branch that closes
		// alg confusion, including a symmetric token signed with this
		// verifier's own public key material.
		return Token{}, credentialErr("algorithm not allowed")
	}
	if hdr.KeyID == "" {
		return Token{}, credentialErr("token names no key id")
	}
	// The key set is selected by the token's audience (ADR-0024, the
	// review's P1-1 ruling): a machine audience token consults the
	// machine key set alone, and every other token consults the platform
	// key set alone, so a staff or tenant login never waits on or fails
	// because of FB Move's key endpoint, and a machine credential never
	// rides the platform set. The audience read here is unverified and
	// trusted for nothing but this choice; the class is confirmed after
	// the signature verifies, below and in claimsFrom, exactly as before.
	auds, err := peekAudience(claimsJSON)
	if err == nil && v.machineKeys != nil && containsAudience(auds, v.machineAud) {
		mkey, mok, err := v.machineKeys.KeyByID(ctx, hdr.KeyID)
		if err != nil {
			return Token{}, operationalErr("machine key source unavailable")
		}
		if !mok {
			return Token{}, credentialErr("unknown key id")
		}
		// A machine audience token whose key id also sits in the platform
		// set is a deployment defect, answered as operational, never
		// resolved by an ordering guess. The check reads the platform set
		// for the machine class alone; platform tokens never read the
		// machine set.
		if _, pok, perr := v.keys.KeyByID(ctx, hdr.KeyID); perr != nil {
			return Token{}, operationalErr("key source unavailable")
		} else if pok {
			return Token{}, operationalErr("key id appears in both key sets")
		}
		if err := verifySignature(Alg(hdr.Algorithm), mkey, signed, sig); err != nil {
			return Token{}, err
		}
		tok, err := v.claimsFrom(claimsJSON)
		if err != nil {
			return Token{}, err
		}
		if tok.Class != ClassMachine {
			return Token{}, credentialErr("machine key set token does not carry the machine audience")
		}
		if err := v.checkRevocation(ctx, tok); err != nil {
			return Token{}, err
		}
		return tok, nil
	}
	key, ok, err := v.keys.KeyByID(ctx, hdr.KeyID)
	if err != nil {
		return Token{}, operationalErr("key source unavailable")
	}
	if !ok {
		return Token{}, credentialErr("unknown key id")
	}
	if err := verifySignature(Alg(hdr.Algorithm), key, signed, sig); err != nil {
		return Token{}, err
	}
	tok, err := v.claimsFrom(claimsJSON)
	if err != nil {
		return Token{}, err
	}
	if tok.Class == ClassMachine {
		// Unreachable while the machine audience is configured (such a
		// token took the machine path above); the guard stands for the
		// unconfigured deployment, where nothing may classify as machine:
		// no default key exists, so no machine token verifies anywhere.
		return Token{}, credentialErr("audience not accepted")
	}
	if err := v.checkRevocation(ctx, tok); err != nil {
		return Token{}, err
	}
	return tok, nil
}

// checkRevocation consults the revocation source for a verified token that
// carries a jti (HH-678). A token with no jti (the testers' synthetic tokens,
// machine credentials) skips the check, and a verifier with no source set
// skips it for everything: the check exists only where the store does.
func (v *Verifier) checkRevocation(ctx context.Context, tok Token) error {
	v.revocationsMu.Lock()
	revocations := v.revocations
	v.revocationsMu.Unlock()
	if revocations == nil || tok.JTI == "" {
		return nil
	}
	revoked, err := revocations.IsRevoked(ctx, tok.JTI)
	if err != nil {
		return operationalErr("revocation source unavailable")
	}
	if revoked {
		return credentialErr("token revoked")
	}
	return nil
}

// peekAudience reads the aud claim of an unverified payload. It exists
// only to select the key set before the signature is checked; nothing it
// returns is trusted past that choice, and every classification and
// validation still runs over the verified claims in claimsFrom. A payload
// whose audience cannot be parsed answers malformed: no set can be
// chosen for it, and the platform path would refuse it after signature
// verification anyway.
func peekAudience(claimsJSON []byte) ([]string, error) {
	var raw struct {
		Audience json.RawMessage `json:"aud"`
	}
	if err := json.Unmarshal(claimsJSON, &raw); err != nil {
		return nil, err
	}
	return parseAudience(raw.Audience)
}

// containsAudience reports whether the audience list names one value.
func containsAudience(auds []string, aud string) bool {
	for _, a := range auds {
		if a == aud {
			return true
		}
	}
	return false
}

// claimsFrom parses and validates the payload after the signature has been
// verified, then classifies the bearer.
func (v *Verifier) claimsFrom(claimsJSON []byte) (Token, error) {
	var probe any
	if err := json.Unmarshal(claimsJSON, &probe); err != nil {
		return Token{}, malformedErr("token claims are not JSON")
	}
	if _, isObject := probe.(map[string]any); !isObject {
		// A null payload unmarshals into a struct without error, leaving
		// zero claims; it is malformed, not a token with no issuer.
		return Token{}, malformedErr("token claims are not an object")
	}
	var raw rawClaims
	if err := json.Unmarshal(claimsJSON, &raw); err != nil {
		return Token{}, malformedErr("token claims are not JSON")
	}
	if raw.Issuer != v.issuer {
		return Token{}, credentialErr("issuer not accepted")
	}
	if raw.Subject == "" {
		return Token{}, malformedErr("token names no subject")
	}
	now := v.now()
	auds, err := parseAudience(raw.Audience)
	if err != nil {
		return Token{}, malformedErr("token audience is malformed")
	}
	class := ClassNone
	for _, aud := range auds {
		// The exclusivity guard is per arm: a repeated audience of one
		// class verifies (an issuer may repeat it, and main always
		// admitted that), and only a second, different class already
		// claimed is refused.
		switch aud {
		case v.staffAud:
			if class != ClassNone && class != ClassStaff {
				return Token{}, credentialErr("token claims both bearer audiences")
			}
			class = ClassStaff
		case v.tenantAud:
			if class != ClassNone && class != ClassTenant {
				return Token{}, credentialErr("token claims both bearer audiences")
			}
			class = ClassTenant
		case v.machineAud:
			if v.machineAud == "" {
				// An unconfigured machine audience can never match a real
				// audience value (both configured values are non-empty and
				// differ from it); this arm exists so an empty-string
				// audience cannot classify through the zero value.
				continue
			}
			if class != ClassNone && class != ClassMachine {
				return Token{}, credentialErr("token claims both bearer audiences")
			}
			class = ClassMachine
		}
	}
	if class == ClassNone {
		return Token{}, credentialErr("audience not accepted")
	}
	if raw.Expires == "" {
		// A token without expiry is a token that never expires; the
		// platform always issues one, so its absence is a defect and
		// fails closed.
		return Token{}, malformedErr("token names no expiry")
	}
	exp, err := numericDate(raw.Expires)
	if err != nil {
		return Token{}, malformedErr("token expiry is not a date")
	}
	if !now.Before(exp) {
		return Token{}, credentialErr("token expired")
	}
	if raw.IssuedAt == "" {
		// Required rather than optional: the carried driver session
		// ceiling is enforced against issued-at downstream, so a token
		// without it cannot support that check.
		return Token{}, malformedErr("token names no issue time")
	}
	iat, err := numericDate(raw.IssuedAt)
	if err != nil {
		return Token{}, malformedErr("token issue time is not a date")
	}
	if iat.After(now.Add(v.skew)) {
		return Token{}, credentialErr("token issued in the future")
	}
	if raw.NotBefore != "" {
		nbf, err := numericDate(raw.NotBefore)
		if err != nil {
			return Token{}, malformedErr("token not-before is not a date")
		}
		if nbf.After(now.Add(v.skew)) {
			return Token{}, credentialErr("token not yet valid")
		}
	}
	tok := Token{
		Class:     class,
		Subject:   raw.Subject,
		IssuedAt:  iat,
		ExpiresAt: exp,
		JTI:       raw.ID,
	}
	if class == ClassTenant {
		if raw.TenantLink == "" {
			return Token{}, malformedErr("tenant token names no contractor account")
		}
		tok.TenantID = raw.TenantLink
	}
	return tok, nil
}

// splitCompact splits a compact serialization, base64url-decodes each part
// and returns the signed input (the first two parts verbatim). Padding is
// tolerated, any other deviation is malformed.
func splitCompact(token string) (header, claims, sig []byte, signed string, err error) {
	dot := strings.LastIndex(token, ".")
	if dot < 0 {
		return nil, nil, nil, "", malformedErr("token is not a compact JWS")
	}
	signed = token[:dot]
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, nil, "", malformedErr("token is not a compact JWS")
	}
	out := make([][]byte, 3)
	for i, p := range parts {
		b, err := decodeBase64URL(p)
		if err != nil {
			return nil, nil, nil, "", malformedErr("token part is not base64url")
		}
		out[i] = b
	}
	return out[0], out[1], out[2], signed, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// verifySignature checks the JWS signature. The alg was already validated
// against the allowed set; here the key must also belong to the algorithm's
// family, so a key of the wrong type never verifies even if its kid was
// named correctly.
func verifySignature(alg Alg, key crypto.PublicKey, signed string, sig []byte) error {
	digest := sha256.Sum256([]byte(signed))
	switch alg {
	case RS256:
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return credentialErr("key does not match algorithm")
		}
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
			return credentialErr("signature did not verify")
		}
		return nil
	case ES256:
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok || pub.Curve != elliptic.P256() {
			return credentialErr("key does not match algorithm")
		}
		// JWS elliptic signatures are the fixed-width concatenation of
		// r and s, not ASN.1 DER.
		if len(sig) != 2*32 {
			return credentialErr("signature did not verify")
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(pub, digest[:], r, s) {
			return credentialErr("signature did not verify")
		}
		return nil
	default:
		// Unreachable while supportedAlgs and the allowed set agree;
		// fail closed regardless.
		return credentialErr("algorithm not allowed")
	}
}

// ErrorClass distinguishes verification failures. Malformed and credential
// failures both answer 401 at the boundary with one identical body; the
// class exists so operators can count failure kinds without any message
// ever carrying token content. Operational means the verifier could not
// run (for example the JWK Set could not be fetched), which is a failure
// to run the check, not a verdict on the token.
type ErrorClass int

const (
	ClassMalformed ErrorClass = iota
	ClassCredential
	ClassOperational
)

// VerifyError is the only error Verify returns.
type VerifyError struct {
	class ErrorClass
	msg   string
}

func malformedErr(msg string) *VerifyError  { return &VerifyError{class: ClassMalformed, msg: msg} }
func credentialErr(msg string) *VerifyError { return &VerifyError{class: ClassCredential, msg: msg} }
func operationalErr(msg string) *VerifyError {
	return &VerifyError{class: ClassOperational, msg: msg}
}

func (e *VerifyError) Error() string { return "identity: " + e.msg }

// Class reports the failure class.
func (e *VerifyError) Class() ErrorClass { return e.class }

// Reason reports the static failure word (for example "token revoked"),
// so a caller outside the package can branch on the refusal's own
// vocabulary. It is static text; it never carries token content.
func (e *VerifyError) Reason() string { return e.msg }

// validJWKSURL surfaces a misconfigured JWK Set URL through construction
// instead of at first fetch.
func validJWKSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("identity: JWK Set URL must be an absolute http or https URL")
	}
	return nil
}
