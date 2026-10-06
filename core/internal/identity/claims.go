package identity

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// Sentinel parse errors, wrapped into VerifyError by the caller, so payload
// parsing carries no payload content in its messages.
var (
	errMalformedAudience = errors.New("audience is malformed")
	errMalformedDate     = errors.New("numeric date is malformed")
)

// BearerClass is the audience class of a credential, resolved from verified
// claims before any authorization decision (ADR-0020: the bearer class is
// resolved first, so the X-Org rule can be bearer-conditioned). A verified
// token classifies as staff, tenant or machine; ClassNone names the state of
// a request with no credential at all. The machine class is ADR-0020's
// addendum's third configured value (ADR-0024): it exists only where a
// deployment configures the machine audience and its own key set, and it
// never rides the platform key set.
type BearerClass int

const (
	ClassNone BearerClass = iota
	ClassStaff
	ClassTenant
	ClassMachine
)

// String names the class for operational text (metrics, test failures);
// never for response bodies.
func (c BearerClass) String() string {
	switch c {
	case ClassStaff:
		return "staff"
	case ClassTenant:
		return "tenant"
	case ClassMachine:
		return "machine"
	default:
		return "none"
	}
}

// Token is the verified identity of one bearer. It is identity only: the
// staff token carries no role claims and none are read, because authority
// is resolved from the database per request (D3; the contract's StaffBearer
// description keeps the staff roles in app-role tables keyed by platform
// user id). IssuedAt and ExpiresAt are carried so downstream stages can
// enforce session ceilings without re-parsing the token.
type Token struct {
	// Class is the bearer classification.
	Class BearerClass
	// Subject is the platform user id (the JWT sub). On the machine class
	// it names the machine principal (its id, ADR-0024): the principal
	// record, not the token, carries the org, the name and the doors.
	Subject string
	// TenantID is the contractor account linkage, carried only by tenant
	// class tokens (ADR-0016 C33: tenant from the token). No other class
	// reads or carries it, the machine class included.
	TenantID string
	// IssuedAt is the JWT iat.
	IssuedAt time.Time
	// ExpiresAt is the JWT exp.
	ExpiresAt time.Time
}

// rawClaims is the payload shape. NumericDate claims decode through
// json.Number so no float rounding can move an expiry. The staff vocabulary
// deliberately has no role, tier or branch fields: there is nothing to read
// even if a token asserts them.
type rawClaims struct {
	Issuer     string          `json:"iss"`
	Subject    string          `json:"sub"`
	Audience   json.RawMessage `json:"aud"`
	Expires    json.Number     `json:"exp"`
	IssuedAt   json.Number     `json:"iat"`
	NotBefore  json.Number     `json:"nbf"`
	TenantLink string          `json:"tenant"`
}

// parseAudience accepts the OIDC shapes of aud, one string or an array of
// strings, and rejects anything else.
func parseAudience(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	return nil, errMalformedAudience
}

// numericDate converts a JWT NumericDate (seconds since the epoch) to a
// time. Fractional values are refused: the platform issues integral
// seconds and anything else is a malformed payload.
func numericDate(n json.Number) (time.Time, error) {
	secs, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil {
		return time.Time{}, errMalformedDate
	}
	return time.Unix(secs, 0).UTC(), nil
}
