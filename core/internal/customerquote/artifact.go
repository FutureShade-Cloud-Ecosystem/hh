package customerquote

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// The signed acceptance artifact (ADR-0022 decision 2). The artifact is the
// canonical JSON serialization of a pinned field set in a pinned order, with
// the version string inside the signed bytes; the hash is sha256 over those
// bytes; the signature is HMAC-SHA256 over the same bytes with the key the
// recorded key id names; both ride the audit row on the acceptance's own
// transaction. A later verification rebuilds the bytes strictly from the
// stored columns, so rebuild-from-row is byte-reproducible under the pins
// below.

const (
	// ArtifactVersion is the artifact version inside the signed bytes. A
	// later format change changes this value together with the field set,
	// and can never be confused with a tampered v1 artifact.
	ArtifactVersion = "1"

	// ArtifactAlg is the pinned algorithm name recorded on every signature
	// and checked at verification.
	ArtifactAlg = "HMAC-SHA256"

	// minKeyBytes is the minimum signing key entropy (256 bits).
	minKeyBytes = 32
)

// ArtifactFields is the pinned field set of the v1 artifact, in the pinned
// order. Every field is rebuilt strictly from the quote_acceptance row's
// stored columns at verification time.
type ArtifactFields struct {
	AcceptanceID          string    // canonical UUID lowercase
	QuoteID               string    // canonical UUID lowercase
	OrgID                 string    // canonical UUID lowercase
	TenantID              string    // canonical UUID lowercase
	SignedName            string    // as stored
	Affirmative           bool      // as stored (true on every signed path)
	ConsentText           string    // as stored (the served constant)
	AcceptedTotalCents    int64     // as stored
	AcceptedAt            time.Time // rendered UTC, six fractional digits
	SignatureAttachmentID string    // the id, or the empty string when null
}

// canonicalUUID validates and normalizes one id to its canonical lowercase
// text form; anything that is not a 8-4-4-4-12 hex UUID fails.
func canonicalUUID(s string) (string, error) {
	u := strings.ToLower(strings.TrimSpace(s))
	if uuidShape.MatchString(u) {
		return u, nil
	}
	return "", fmt.Errorf("customerquote: %q is not a canonical UUID", s)
}

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// artifactStamp renders accepted_at at the pinned precision: RFC 3339, UTC,
// always six fractional digits (the column's own microsecond resolution), so
// the signing path and the rebuild-from-row path render identical bytes. The
// caller truncates to microsecond before rendering.
const artifactStampLayout = "2006-01-02T15:04:05.000000Z07:00"

// writeJSONString appends one canonical JSON string. The escaping is pinned
// to match Go's encoding/json with HTML escaping disabled: quote and
// backslash escaped, the short escapes for the five named controls, other
// control characters as lowercase \u00xx, U+2028 and U+2029 escaped, invalid
// UTF-8 replaced by the replacement character (ADR-0022's canonicalization
// pin). A typed name carrying angle brackets, an ampersand or a lone
// quote can never fork the bytes between sign time and verify time.
func writeJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '"':
				dst = append(dst, '\\', '"')
			case '\\':
				dst = append(dst, '\\', '\\')
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				if c < 0x20 {
					dst = append(dst, '\\', 'u', '0', '0', hexDigit[c>>4], hexDigit[c&0xF])
				} else {
					dst = append(dst, c)
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 becomes the replacement character, emitted as
			// its raw UTF-8 bytes, matching the toolchain's encoding/json
			// with HTML escaping disabled.
			dst = append(dst, '\xef', '\xbf', '\xbd')
			i++
			continue
		}
		if r == '\u2028' {
			dst = append(dst, '\\', 'u', '2', '0', '2', '8')
		} else if r == '\u2029' {
			dst = append(dst, '\\', 'u', '2', '0', '2', '9')
		} else {
			dst = append(dst, s[i:i+size]...)
		}
		i += size
	}
	return append(dst, '"')
}

const hexDigit = "0123456789abcdef"

// CanonicalArtifactBytes builds the pinned canonical bytes. It normalizes
// the ids to lowercase, truncates the stamp to the stored microsecond
// precision, and writes the pinned field order with no insignificant
// whitespace and no additional keys.
func CanonicalArtifactBytes(f ArtifactFields) ([]byte, error) {
	acceptanceID, err := canonicalUUID(f.AcceptanceID)
	if err != nil {
		return nil, err
	}
	quoteID, err := canonicalUUID(f.QuoteID)
	if err != nil {
		return nil, err
	}
	orgID, err := canonicalUUID(f.OrgID)
	if err != nil {
		return nil, err
	}
	tenantID, err := canonicalUUID(f.TenantID)
	if err != nil {
		return nil, err
	}
	signatureAttachment := ""
	if f.SignatureAttachmentID != "" {
		signatureAttachment, err = canonicalUUID(f.SignatureAttachmentID)
		if err != nil {
			return nil, err
		}
	}
	stamp := f.AcceptedAt.UTC().Truncate(time.Microsecond).Format(artifactStampLayout)

	dst := make([]byte, 0, 512)
	dst = append(dst, `{"v":`...)
	dst = writeJSONString(dst, ArtifactVersion)
	dst = append(dst, `,"acceptance_id":`...)
	dst = writeJSONString(dst, acceptanceID)
	dst = append(dst, `,"quote_id":`...)
	dst = writeJSONString(dst, quoteID)
	dst = append(dst, `,"org_id":`...)
	dst = writeJSONString(dst, orgID)
	dst = append(dst, `,"tenant_id":`...)
	dst = writeJSONString(dst, tenantID)
	dst = append(dst, `,"signed_name":`...)
	dst = writeJSONString(dst, f.SignedName)
	dst = append(dst, `,"affirmative":`...)
	if f.Affirmative {
		dst = append(dst, "true"...)
	} else {
		dst = append(dst, "false"...)
	}
	dst = append(dst, `,"consent_text":`...)
	dst = writeJSONString(dst, f.ConsentText)
	dst = append(dst, `,"accepted_total_cents":`...)
	dst = append(dst, fmt.Appendf(nil, "%d", f.AcceptedTotalCents)...)
	dst = append(dst, `,"accepted_at":`...)
	dst = writeJSONString(dst, stamp)
	dst = append(dst, `,"signature_attachment_id":`...)
	dst = writeJSONString(dst, signatureAttachment)
	dst = append(dst, '}')
	return dst, nil
}

// ArtifactDigest computes the artifact hash (sha256 hex over the canonical
// bytes) and the signature (HMAC-SHA256 hex over the same bytes).
func ArtifactDigest(key []byte, f ArtifactFields) (hashHex, sigHex string, err error) {
	b, err := CanonicalArtifactBytes(f)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(b)
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	return hex.EncodeToString(sum[:]), hex.EncodeToString(mac.Sum(nil)), nil
}

// CompareMAC compares a stored signature hex against a computed MAC in
// constant time (ADR-0022: hmac.Equal on the MAC compare; the token scan
// elsewhere makes no timing claim and needs none).
func CompareMAC(storedSigHex, computedSigHex string) bool {
	stored, err := hex.DecodeString(storedSigHex)
	if err != nil {
		return false
	}
	computed, err := hex.DecodeString(computedSigHex)
	if err != nil {
		return false
	}
	return hmac.Equal(stored, computed)
}

// The key schedule (ADR-0022 decision 2): one signing key at a time with a
// key id, a verify-only previous key allowed during rotation, spawn failing
// without the current key, and placeholder environment names only. No value
// exists in any file; the secret store path is Colton's to name at deploy.
const (
	EnvKeyID         = "HH_ACCEPTANCE_SIGN_KEY_ID"
	EnvKey           = "HH_ACCEPTANCE_SIGN_KEY"
	EnvPreviousKey   = "HH_ACCEPTANCE_SIGN_KEY_PREVIOUS"
	EnvPreviousKeyID = "HH_ACCEPTANCE_SIGN_KEY_PREVIOUS_ID"
)

// keyIDShape pins the key id vocabulary: lowercase alphanumeric plus hyphen,
// at most 32 characters, starting lowercase alphanumeric, and no run of
// seven or more digits so the id itself can never trip the carried audit
// redaction chokepoint.
var keyIDShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// SigningKeys is the loaded key set.
type SigningKeys struct {
	KeyID         string
	Key           []byte
	PreviousKeyID string
	PreviousKey   []byte
}

// ValidateKeyID applies the pinned key id shape including the digit-run rule.
func ValidateKeyID(id string) error {
	if !keyIDShape.MatchString(id) {
		return fmt.Errorf("customerquote: key id %q fails the pinned shape", id)
	}
	run := 0
	for _, c := range id {
		if c >= '0' && c <= '9' {
			run++
			if run >= 7 {
				return fmt.Errorf("customerquote: key id %q carries a seven-digit run", id)
			}
		} else {
			run = 0
		}
	}
	return nil
}

// LoadSigningKeys loads the key schedule from environment at spawn. The
// current key and its id are required; the previous key is optional and, when
// set, requires its own id. Keys decode from hex and must carry at least 256
// bits. Spawn fails loudly on every defect.
func LoadSigningKeys(getenv func(string) string) (*SigningKeys, error) {
	if getenv == nil {
		return nil, errors.New("customerquote: signing keys require an environment source")
	}
	keyID := strings.TrimSpace(getenv(EnvKeyID))
	if keyID == "" {
		return nil, fmt.Errorf("customerquote: %s is required", EnvKeyID)
	}
	if err := ValidateKeyID(keyID); err != nil {
		return nil, err
	}
	key, err := decodeKeyHex(getenv(EnvKey))
	if err != nil {
		return nil, fmt.Errorf("customerquote: %s: %w", EnvKey, err)
	}
	keys := &SigningKeys{KeyID: keyID, Key: key}
	prev := strings.TrimSpace(getenv(EnvPreviousKey))
	if prev != "" {
		prevID := strings.TrimSpace(getenv(EnvPreviousKeyID))
		if prevID == "" {
			return nil, fmt.Errorf("customerquote: %s is required when %s is set", EnvPreviousKeyID, EnvPreviousKey)
		}
		if err := ValidateKeyID(prevID); err != nil {
			return nil, err
		}
		if prevID == keyID {
			return nil, fmt.Errorf("customerquote: previous key id must differ from the current key id")
		}
		prevKey, err := decodeKeyHex(prev)
		if err != nil {
			return nil, fmt.Errorf("customerquote: %s: %w", EnvPreviousKey, err)
		}
		keys.PreviousKeyID = prevID
		keys.PreviousKey = prevKey
	}
	return keys, nil
}

// decodeKeyHex decodes a hex key and enforces the entropy floor.
func decodeKeyHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("key is empty")
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(raw) < minKeyBytes {
		return nil, fmt.Errorf("key carries %d bytes, minimum %d", len(raw), minKeyBytes)
	}
	return raw, nil
}

// KeyFor resolves the verification key for a recorded key id: the current
// key, or the verify-only previous key during rotation. An unknown or
// withdrawn id names itself in the error, so the failure is attributable.
func (k *SigningKeys) KeyFor(keyID string) ([]byte, error) {
	if keyID == k.KeyID {
		return k.Key, nil
	}
	if k.PreviousKey != nil && keyID == k.PreviousKeyID {
		return k.PreviousKey, nil
	}
	return nil, fmt.Errorf("customerquote: signing key id %q is not configured", keyID)
}
