package localization

// country.go, address validation that follows the dealer's own country. The Canadian rules in
// localization.go are the default (a dealer with no country setting is the Ontario dealer); this
// adds the United States: ZIP and ZIP+4, and the 50 states plus DC. The dealer's country comes
// from the settings module's dealer configuration; this package stays pure and takes it as an
// argument.

import (
	"regexp"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// The two countries the validation layer knows.
const (
	CountryCA = "CA"
	CountryUS = "US"
)

var zipRE = regexp.MustCompile(`^[0-9]{5}([0-9]{4})?$`)

// usStates is the 50 states plus the District of Columbia.
var usStates = map[string]bool{
	"AL": true, "AK": true, "AZ": true, "AR": true, "CA": true, "CO": true, "CT": true, "DE": true,
	"DC": true, "FL": true, "GA": true, "HI": true, "ID": true, "IL": true, "IN": true, "IA": true,
	"KS": true, "KY": true, "LA": true, "ME": true, "MD": true, "MA": true, "MI": true, "MN": true,
	"MS": true, "MO": true, "MT": true, "NE": true, "NV": true, "NH": true, "NJ": true, "NM": true,
	"NY": true, "NC": true, "ND": true, "OH": true, "OK": true, "OR": true, "PA": true, "RI": true,
	"SC": true, "SD": true, "TN": true, "TX": true, "UT": true, "VT": true, "VA": true, "WA": true,
	"WV": true, "WI": true, "WY": true,
}

// NormalizeZIP validates a US ZIP code and returns it as "12345" or "12345-6789". It accepts the
// nine digit form with a hyphen, a space or nothing between the parts. Anything else, a Canadian
// postal code included, is a 400.
func NormalizeZIP(raw string) (string, error) {
	compact := strings.Join(strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == '-' || r == '\t' }), "")
	if compact == "" {
		return "", httpx.BadRequestf("ZIP code is required")
	}
	if !zipRE.MatchString(compact) {
		return "", httpx.BadRequestf("invalid ZIP code %q (expected 12345 or 12345-6789)", raw)
	}
	if len(compact) == 9 {
		return compact[:5] + "-" + compact[5:], nil
	}
	return compact, nil
}

// ValidateState checks a two letter US state code (or DC), case-insensitive on input, and returns
// it uppercase.
func ValidateState(code string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(code))
	if up == "" {
		return "", httpx.BadRequestf("state is required")
	}
	if !usStates[up] {
		return "", httpx.BadRequestf("invalid state %q (expected a two letter US state code, e.g. CA)", code)
	}
	return up, nil
}

// NormalizeAddressFor validates and normalizes an address by the dealer's country. A blank country
// is Canada. The address's own country defaults to the dealer's and must equal it. Province holds
// the state for a US address and Postal the ZIP code: the stored keys do not change with the
// country.
func NormalizeAddressFor(country string, a Address) (Address, error) {
	dealer := strings.ToUpper(strings.TrimSpace(country))
	if dealer == "" {
		dealer = CountryCA
	}
	own := strings.ToUpper(strings.TrimSpace(a.Country))
	if own == "" {
		own = dealer
	}
	out := a
	switch dealer {
	case CountryCA:
		if own != CountryCA {
			return Address{}, httpx.BadRequestf("only Canadian (CA) addresses are supported, got country %q", a.Country)
		}
		prov, err := ValidateProvince(a.Province)
		if err != nil {
			return Address{}, err
		}
		postal, err := NormalizePostal(a.Postal)
		if err != nil {
			return Address{}, err
		}
		out.Province, out.Postal = prov, postal
	case CountryUS:
		if own != CountryUS {
			return Address{}, httpx.BadRequestf("only US addresses are supported, got country %q", a.Country)
		}
		state, err := ValidateState(a.Province)
		if err != nil {
			return Address{}, err
		}
		zip, err := NormalizeZIP(a.Postal)
		if err != nil {
			return Address{}, err
		}
		out.Province, out.Postal = state, zip
	default:
		return Address{}, httpx.BadRequestf("addresses for country %q are not supported", country)
	}
	out.Country = own
	return out, nil
}
