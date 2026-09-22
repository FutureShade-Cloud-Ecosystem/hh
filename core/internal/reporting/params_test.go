package reporting

// params_test.go, the injection-refusal spine. Every one of these is a real thing a caller could type
// into a query string, and every one of them must be answered with a 400 that names the parameter,
// never with a query.

import (
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

var testSpecs = []ParamSpec{
	{Name: "from_date", Kind: ParamDate, Required: true},
	{Name: "to_date", Kind: ParamDate, Required: true},
	{Name: "branch_id", Kind: ParamUUID},
	{Name: "move_type", Kind: ParamEnum, Values: []string{"RECEIPT", "PICK"}},
}

func isBadRequest(err error) bool {
	var b httpx.BadRequestError
	return errors.As(err, &b)
}

// TestParams_InjectionAttemptIsRefused is the acceptance test named in the brief: a malicious parameter
// cannot escape the allowlist.
//
// It matters WHY each of these fails. None of them fail because a string was scanned for "DROP" or
// quotes, a blocklist is a race against a dialect. They fail because every parameter kind has a TOTAL
// parse: a uuid parameter is a uuid or it is nothing, a date is a date, an enum is a member. There is
// no value of any of these types that carries SQL, so there is nothing to escape.
func TestParams_InjectionAttemptIsRefused(t *testing.T) {
	t.Parallel()
	base := map[string]string{"from_date": "2026-01-01", "to_date": "2026-01-31"}
	attempts := []struct {
		name  string
		param string
		value string
	}{
		{"classic tautology in a uuid", "branch_id", "' OR 1=1--"},
		{"statement terminator in a uuid", "branch_id", "00000000-0000-0000-0000-000000000000'; DROP TABLE invoices;--"},
		{"union select in a uuid", "branch_id", "x' UNION SELECT password_hash FROM users--"},
		{"comment splice in a date", "from_date", "2026-01-01'/**/OR/**/1=1"},
		{"cast escape in a date", "to_date", "2026-01-31)::text||(SELECT 1"},
		{"enum with a subquery", "move_type", "RECEIPT' OR (SELECT 1)=1--"},
		{"enum off the list", "move_type", "DELETE"},
		{"unicode-quote uuid", "branch_id", "’ OR ’1’=’1"},
		{"null byte in a uuid", "branch_id", "00000000-0000-0000-0000-000000000000\x00 OR 1=1"},
	}
	for _, a := range attempts {
		t.Run(a.name, func(t *testing.T) {
			in := map[string]string{}
			for k, v := range base {
				in[k] = v
			}
			in[a.param] = a.value
			args, norm, err := bindParams(testSpecs, in)
			if err == nil {
				t.Fatalf("%s: bindParams accepted %q for %s (args=%v norm=%v), it must refuse",
					a.name, a.value, a.param, args, norm)
			}
			if !isBadRequest(err) {
				t.Errorf("%s: error is %v, want a BadRequestError so the caller gets a 400 that names the parameter", a.name, err)
			}
		})
	}
}

// TestParams_RejectsUnknownParameter: an unrecognised key is refused rather than ignored. See params.go
// for why silence is the worse failure.
func TestParams_RejectsUnknownParameter(t *testing.T) {
	t.Parallel()
	_, _, err := bindParams(testSpecs, map[string]string{
		"from_date": "2026-01-01", "to_date": "2026-01-31", "branch": "KGN",
	})
	if err == nil {
		t.Fatal("bindParams accepted an unknown parameter 'branch', a typo must not silently widen a report")
	}
	if !isBadRequest(err) {
		t.Errorf("error is %v, want a BadRequestError", err)
	}
	// And the message must name it, or the caller cannot fix it.
	if got := err.Error(); got == "" || !contains(got, "branch") {
		t.Errorf("error %q does not name the offending parameter", got)
	}
}

// TestParams_ArgCountAlwaysMatchesSpecs is the invariant that makes "one code, one query" literal: an
// omitted optional parameter binds a typed NULL (or empty string) rather than shortening the argument list, so the
// query text never varies with which filters were supplied.
func TestParams_ArgCountAlwaysMatchesSpecs(t *testing.T) {
	t.Parallel()
	cases := []map[string]string{
		{"from_date": "2026-01-01", "to_date": "2026-01-31"},
		{"from_date": "2026-01-01", "to_date": "2026-01-31", "branch_id": "5d1b3f6e-0000-4000-9000-000000000001"},
		{"from_date": "2026-01-01", "to_date": "2026-01-31", "move_type": "PICK"},
		{"from_date": "2026-01-01", "to_date": "2026-01-31",
			"branch_id": "5d1b3f6e-0000-4000-9000-000000000001", "move_type": "RECEIPT"},
	}
	for i, in := range cases {
		args, _, err := bindParams(testSpecs, in)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if len(args) != len(testSpecs) {
			t.Errorf("case %d: bound %d args for %d specs, the query would take a different shape", i, len(args), len(testSpecs))
		}
	}
}

// TestParams_AbsentOptionalsBindTypedNulls pins the exact absent forms the SQL's null-checks rely on.
// If an absent uuid ever bound "" instead of nil, `$3::uuid IS NULL` would stop being true and every
// consolidated report would silently return no rows.
func TestParams_AbsentOptionalsBindTypedNulls(t *testing.T) {
	t.Parallel()
	args, _, err := bindParams(testSpecs, map[string]string{"from_date": "2026-01-01", "to_date": "2026-01-31"})
	if err != nil {
		t.Fatal(err)
	}
	if args[2] != nil {
		t.Errorf("absent uuid bound %#v, want nil (a typed SQL NULL)", args[2])
	}
	if args[3] != "" {
		t.Errorf("absent enum bound %#v, want the empty string", args[3])
	}
	if _, ok := args[0].(time.Time); !ok {
		t.Errorf("a date bound %T, want time.Time", args[0])
	}
}

// TestParams_RequiredIsEnforced.
func TestParams_RequiredIsEnforced(t *testing.T) {
	t.Parallel()
	if _, _, err := bindParams(testSpecs, map[string]string{"to_date": "2026-01-31"}); err == nil {
		t.Fatal("a missing required parameter was accepted")
	} else if !isBadRequest(err) {
		t.Errorf("error is %v, want a BadRequestError", err)
	}
}

// TestParams_NormalizedValuesAreCanonical: what is echoed back (and stored on a printed artefact) is
// the canonical form, not the caller's spelling. A report header that says what was actually run is
// the difference between a screenshot that can be reproduced and one that cannot.
func TestParams_NormalizedValuesAreCanonical(t *testing.T) {
	t.Parallel()
	_, norm, err := bindParams(testSpecs, map[string]string{
		"from_date": " 2026-01-01 ", // caller whitespace
		"to_date":   "2026-01-31",
		"branch_id": "5D1B3F6E-0000-4000-9000-000000000001", // upper case
	})
	if err != nil {
		t.Fatal(err)
	}
	if norm["from_date"] != "2026-01-01" {
		t.Errorf("from_date normalized to %q", norm["from_date"])
	}
	if norm["branch_id"] != "5d1b3f6e-0000-4000-9000-000000000001" {
		t.Errorf("branch_id normalized to %q, want the canonical lower-case uuid", norm["branch_id"])
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
