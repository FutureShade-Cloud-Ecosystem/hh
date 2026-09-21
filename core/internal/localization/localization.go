// Package localization is the M5 Canadian-locale validation layer (scope reconciliation
// 2026-07-14, A-5): Canadian postal-code validation + normalization and an ISO-3166-2:CA
// province constraint, applied server-side on the customer/address write path. It has no
// database or HTTP dependency, pure functions the write-path services call, so it stays a
// single source of truth reusable by any writer (customer create/update, branch/address
// writes) and mirrorable client-side.
//
// Reconciliation note: M5's Plane/tracker title claimed "Avalara fallback + postal-code
// routing" (neither built); this closes the real, specced gap, postal/province VALIDATION on
// the write path (M5-canadian-localization.md:27), not the overstated routing engine.
package localization

import (
	"regexp"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// postalRE matches a Canadian postal code in its canonical "A1A 1A1" form. The first letter
// excludes D, F, I, O, Q, U (never used in the leading position) and W, Z (never used
// anywhere as the first character) per Canada Post's format; subsequent letters exclude
// D, F, I, O, Q, U. This rejects US ZIPs (12345 / 12345-6789) and arbitrary garbage.
var postalRE = regexp.MustCompile(`^[ABCEGHJKLMNPRSTVXY][0-9][ABCEGHJKLMNPRSTVWXYZ][0-9][ABCEGHJKLMNPRSTVWXYZ][0-9]$`)

// provinces is the set of ISO-3166-2:CA subdivision codes (10 provinces + 3 territories),
// the constraint a province value on a Canadian address must satisfy.
var provinces = map[string]bool{
	"AB": true, "BC": true, "MB": true, "NB": true, "NL": true,
	"NS": true, "NT": true, "NU": true, "ON": true, "PE": true,
	"QC": true, "SK": true, "YT": true,
}

// NormalizePostal validates a Canadian postal code and returns it in canonical "A1A 1A1"
// form (uppercased, single space at position 3). It accepts input with any/no internal
// whitespace and any case, "k8v5p7", "k8v 5p7", "K8V5P7" all normalize to "K8V 5P7".
// A US ZIP, an over/under-length string, or an otherwise non-Canadian format is a 400
// (httpx.BadRequestError), so a delivery/billing address can never be saved to a typo.
func NormalizePostal(raw string) (string, error) {
	// Strip ALL whitespace (interior and edge) and uppercase, so casing/spacing never
	// causes a false reject; the canonical space is re-inserted after the format check.
	compact := strings.ToUpper(strings.Join(strings.Fields(raw), ""))
	if compact == "" {
		return "", httpx.BadRequestf("postal code is required")
	}
	if !postalRE.MatchString(compact) {
		return "", httpx.BadRequestf("invalid Canadian postal code %q (expected format A1A 1A1)", raw)
	}
	return compact[:3] + " " + compact[3:], nil
}

// ValidateProvince checks that code is an ISO-3166-2:CA subdivision code (case-insensitive
// on input; the canonical two-letter uppercase code is returned). A non-Canadian or unknown
// province is a 400.
func ValidateProvince(code string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(code))
	if up == "" {
		return "", httpx.BadRequestf("province is required")
	}
	if !provinces[up] {
		return "", httpx.BadRequestf("invalid province %q (expected an ISO-3166-2 Canadian code, e.g. ON)", code)
	}
	return up, nil
}

// Address is the structured Canadian address the write path validates+normalizes. It mirrors
// the JSONB address shape used across the codebase (locations.address / customers.billing_
// address): line1/city/province/postal/country.
type Address struct {
	Line1    string `json:"line1"`
	City     string `json:"city"`
	Province string `json:"province"`
	Postal   string `json:"postal"`
	Country  string `json:"country"`
}

// NormalizeAddress validates the province + postal of a Canadian address and returns a copy
// with both normalized (province uppercased ISO code, postal "A1A 1A1"). Country defaults to
// "CA" when blank and must be CA (the validation layer is Canada-only, per M5). Any invalid
// field is a 400. line1/city are passed through unchanged (free text).
func NormalizeAddress(a Address) (Address, error) {
	out := a
	country := strings.ToUpper(strings.TrimSpace(a.Country))
	if country == "" {
		country = "CA"
	}
	if country != "CA" {
		return Address{}, httpx.BadRequestf("only Canadian (CA) addresses are supported, got country %q", a.Country)
	}
	out.Country = country

	prov, err := ValidateProvince(a.Province)
	if err != nil {
		return Address{}, err
	}
	out.Province = prov

	postal, err := NormalizePostal(a.Postal)
	if err != nil {
		return Address{}, err
	}
	out.Postal = postal
	return out, nil
}

// normalizeMapAddress validates+normalizes an address carried as a decoded JSON object
// (map[string]any, the shape a customers.billing_address JSONB round-trips as). Only the
// province/postal/country keys are validated; other keys pass through untouched. Returns a
// new map so the caller's input is not mutated. A missing province/postal is a 400 (an
// address on the write path must carry both to be validatable).
func normalizeMapAddress(m map[string]any) (map[string]any, error) {
	get := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}
	norm, err := NormalizeAddress(Address{
		Line1:    get("line1"),
		City:     get("city"),
		Province: get("province"),
		Postal:   get("postal"),
		Country:  get("country"),
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(m)+3)
	for k, v := range m {
		out[k] = v
	}
	out["province"] = norm.Province
	out["postal"] = norm.Postal
	out["country"] = norm.Country
	return out, nil
}

// NormalizeBillingAddress validates+normalizes a customers.billing_address JSONB payload. An
// EMPTY address ({} / nil) is allowed through unchanged, the write path only enforces
// province/postal correctness when an address is actually supplied (billing_address is
// nullable/defaulted at the schema level). A supplied-but-invalid address is a 400.
func NormalizeBillingAddress(m map[string]any) (map[string]any, error) {
	if len(m) == 0 {
		return m, nil
	}
	// If neither province nor postal is present, treat it as a partial/free-form address the
	// validator has nothing to assert on (e.g. only line1/city captured). Enforce only when
	// at least one of the two constrained fields is present, then BOTH must be valid.
	_, hasProv := m["province"]
	_, hasPostal := m["postal"]
	if !hasProv && !hasPostal {
		return m, nil
	}
	return normalizeMapAddress(m)
}
