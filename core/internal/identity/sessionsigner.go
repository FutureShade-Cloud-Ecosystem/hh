package identity

// The session signing path. This file and the SessionMinter type are the
// only place in the package that holds a private key; no verifier code
// references it (sessionfence_test.go holds that), and the verifier in verifier.go still has no signing path of
// its own. The key is loaded once from a file the platform mounts, the raw
// bytes are dropped, and the key has no marshal, write or log path: a
// SessionMinter has no exported field and no method that returns the
// private scalar.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// demoIssuerValue is the issuer of the demo build's in memory issuer. A
// persistent key must never mint under it, so a token from the public copy
// or the testers' copy can never verify on a deployment that kept the demo
// name.
// maxKeyFileBytes bounds what a key file read takes.
const maxKeyFileBytes = 64 << 10

const demoIssuerValue = "hh-demo"

// SessionKeyFiles names the three key files. Current holds the private key
// (PEM, EC P-256, SEC1 or PKCS8); Next and Previous, when set, hold PUBLIC
// halves only (PEM PUBLIC KEY).
type SessionKeyFiles struct {
	Current  string
	Next     string
	Previous string
}

// LoadedSessionKeys is what a boot gets from the files: the minter over the
// current private key and the key set over all three public halves.
type LoadedSessionKeys struct {
	Minter *SessionMinter
	Keys   *SessionKeySet
}

// LoadSessionKeys reads the files once and drops the raw bytes. Every
// refusal is static text: neither the path nor any content of a file enters
// an error.
func LoadSessionKeys(files SessionKeyFiles, issuer, staffAudience string) (*LoadedSessionKeys, error) {
	if files.Current == "" {
		return nil, errors.New("identity: the session signing key file is required")
	}
	priv, err := loadSessionPrivateKey(files.Current)
	if err != nil {
		return nil, err
	}
	var next, previous *ecdsa.PublicKey
	if files.Next != "" {
		if next, err = loadSessionPublicKey(files.Next, "NEXT"); err != nil {
			return nil, err
		}
	}
	if files.Previous != "" {
		if previous, err = loadSessionPublicKey(files.Previous, "PREVIOUS"); err != nil {
			return nil, err
		}
	}
	keys, err := NewSessionKeySet(&priv.PublicKey, next, previous)
	if err != nil {
		return nil, err
	}
	minter, err := NewSessionMinter(priv, issuer, staffAudience)
	if err != nil {
		return nil, err
	}
	return &LoadedSessionKeys{Minter: minter, Keys: keys}, nil
}

func loadSessionPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("identity: the session signing key file cannot be read")
	}
	defer f.Close()
	// The mode is read from the open descriptor, so the file checked is the
	// file read. A key readable by group or other is refused: the platform
	// mounts the secret file 0400 or 0600 and owned by the core's user.
	info, err := f.Stat()
	if err != nil {
		return nil, errors.New("identity: the session signing key file cannot be read")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("identity: the session signing key file must not be readable by group or other (mode 0400 or 0600)")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxKeyFileBytes))
	if err != nil {
		return nil, errors.New("identity: the session signing key file cannot be read")
	}
	defer wipe(raw)
	block := singlePEMBlock(raw)
	if block == nil {
		return nil, errors.New("identity: the session signing key file is not a PEM private key")
	}
	defer wipe(block.Bytes)
	var key *ecdsa.PrivateKey
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("identity: the session signing key file is not a PEM private key")
		}
	case "PRIVATE KEY":
		parsed, perr := x509.ParsePKCS8PrivateKey(block.Bytes)
		ec, ok := parsed.(*ecdsa.PrivateKey)
		if perr != nil || !ok {
			return nil, errors.New("identity: the session signing key file is not an EC private key")
		}
		key = ec
	default:
		return nil, errors.New("identity: the session signing key file is not a PEM private key")
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("identity: the session signing key must be an EC P-256 key")
	}
	return key, nil
}

func loadSessionPublicKey(path, slot string) (*ecdsa.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("identity: the " + slot + " session key file cannot be read")
	}
	defer wipe(raw)
	pub, err := ParseSessionPublicKeyPEM(raw)
	if err != nil {
		return nil, errors.New("identity: the " + slot + " session key file is not an EC P-256 PEM public key")
	}
	return pub, nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// MinSessionWindow is the shortest window a token is minted for. Claims
// carry whole seconds, so anything shorter would truncate to a token dead
// on arrival (exp equal to iat).
const MinSessionWindow = time.Second

// SessionMinter signs ES256 session tokens with the deployment's key. The
// claims are iss, sub, aud, iat and exp, nothing else: authority is the
// database's, never the token's (D3).
type SessionMinter struct {
	key      *ecdsa.PrivateKey
	kid      string
	issuer   string
	audience string
	now      func() time.Time
}

// NewSessionMinter binds a private key to an issuer and the staff
// audience. The issuer "hh-demo" is refused, as is a non P-256 key.
func NewSessionMinter(key *ecdsa.PrivateKey, issuer, staffAudience string) (*SessionMinter, error) {
	if key == nil {
		return nil, errors.New("identity: the session minter needs a key")
	}
	if issuer == "" || staffAudience == "" {
		return nil, errors.New("identity: the session minter needs an issuer and an audience")
	}
	if issuer == demoIssuerValue {
		return nil, errors.New("identity: the issuer " + demoIssuerValue + " is reserved for the demo issuer and refused for a persistent key")
	}
	kid, err := SessionKeyThumbprint(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &SessionMinter{key: key, kid: kid, issuer: issuer, audience: staffAudience, now: time.Now}, nil
}

// PublicKey returns the public half.
func (m *SessionMinter) PublicKey() *ecdsa.PublicKey { return &m.key.PublicKey }

// Mint signs a token for the subject valid for the window, issued now.
// The window is bounded MinSessionWindow <= window <= MaxDriverSessionTTL; the caller
// computes it from the role's ceiling and the exchange knob.
func (m *SessionMinter) Mint(subject string, window time.Duration) (string, error) {
	return m.MintAt(subject, window, m.now())
}

// MintAt is Mint with an explicit issue time (tests drive their own clock).
func (m *SessionMinter) MintAt(subject string, window time.Duration, at time.Time) (string, error) {
	if subject == "" {
		return "", errors.New("identity: a session token needs a subject")
	}
	if window < MinSessionWindow || window > MaxDriverSessionTTL {
		return "", errors.New("identity: the session window is outside the allowed bounds")
	}
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": m.kid, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"iss": m.issuer,
		"sub": subject,
		"aud": m.audience,
		"iat": at.Unix(),
		"exp": at.Add(window).Unix(),
	})
	if err != nil {
		return "", err
	}
	b64 := base64.RawURLEncoding.EncodeToString
	signed := b64(header) + "." + b64(claims)
	digest := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, m.key, digest[:])
	if err != nil {
		return "", errors.New("identity: the session token could not be signed")
	}
	// JWS elliptic signatures are the fixed width r||s concatenation.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signed + "." + b64(sig), nil
}

const redactedMinter = "SessionMinter{redacted}"

// String and Format redact: a minter handed to a logger, a panic message or
// a debug dump prints this fixed text under every verb, whether passed by
// pointer or by value, and never the key.
func (m SessionMinter) String() string { return redactedMinter }

// Format implements fmt.Formatter.
func (m SessionMinter) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redactedMinter)) }

// GoString redacts the %#v form for any caller that bypasses Format.
func (m SessionMinter) GoString() string { return redactedMinter }
