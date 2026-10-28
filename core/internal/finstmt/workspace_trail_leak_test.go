// workspace_trail_leak_test.go, FLAKE-10: the customer workspace's payment trail check reads
// PARSED fields, never the raw body. A timestamp's nanoseconds can hold the digits of an amount
// (the flake's run's body carried "as_of":"2026-10-28T12:48:30.034123459Z", whose fractional
// part holds the seeded payment's 41234), so a raw substring hunt read the clock as a payment.
//
// The check lives here, untagged, so the unit leg below runs in the untagged suite and the
// integration leg calls the same helper.
//
// ROUND 2 (the M3 review's P3s): the check now sees the amount wherever it is written (as a
// NUMBER, or as TEXT in any field, anchored to a WHOLE value so a clock can never be read as an
// amount), fails on a payment list that is present and null rather than absent, and names the
// leak instead of the JSON parser when the body carries a payment the typed struct cannot read.
package finstmt

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The payment the workspace trail leg seeds: the amount and the method the check must not let
// through to a caller who does not hold the payment trail.
const (
	trailPaymentCents  = 41234
	trailPaymentMethod = "CHEQUE"
)

// workspaceTrailLeak reads a customer workspace body and reports the payment detail it carries.
//
// It reads PARSED fields, never the raw text. An amount is read as a NUMBER or as a whole TEXT
// value that parses as the amount, and a method as a METHOD, so a clock's nanoseconds (a
// timestamp is one string that does not parse as a number) and any other field's digits can never
// be read as a payment.
//
// The body is read as a generic tree, not as a typed struct, for one reason: a typed struct makes
// the JSON PARSER the judge. A body that carries the amount as text in a field the struct does
// not parse (a memo, a note, a reference) unmarshals into a typed struct without complaint and
// the amount is invisible; and a body that carries the amount as text in a field the struct DOES
// parse fails the whole unmarshal, so the check reported a parse fault where the cause was a
// leak. The tree walk reads both and names the field.
func workspaceTrailLeak(body string) ([]string, error) {
	var tree map[string]any
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		return nil, fmt.Errorf("the body is not JSON: %w", err)
	}
	var leaks []string

	// THE PAYMENT LIST, ABSENT, NOT BLANKED. The handler writes the key absent (the field carries
	// omitempty and the handler clears the list), so a key that is present at all is a leak. A key
	// that is present and NULL is a leak of its own kind: to anything that only asks "is there a
	// list", null and absent are the same answer, and a null list is exactly the shape that would
	// hide the list from a screen while the key says it is there.
	if raw, present := tree["payments"]; present {
		if raw == nil {
			leaks = append(leaks, "the body carries a null payment list")
		} else {
			leaks = append(leaks, "the body carries the payment list")
		}
	}

	// THE WITHHELD FLAG. It is a flag, so it is read as one: a string "true" is not the flag.
	if withheld, _ := tree["payments_withheld"].(bool); !withheld {
		leaks = append(leaks, "the body does not carry the withheld flag")
	}

	// THE HEADER'S LAST PAYMENT, the amount and the time. The handler clears both when the trail
	// is withheld and the fields carry omitempty, so a key that is present is a leak. The keys are
	// read, not their values: a last payment's amount of zero is still a last payment.
	if header, ok := tree["header"].(map[string]any); ok {
		if _, present := header["last_payment_cents"]; present {
			leaks = append(leaks, "the header carries the last payment's amount")
		}
		if _, present := header["last_payment_at"]; present {
			leaks = append(leaks, "the header carries the last payment's time")
		}
	}

	paymentLeaks, err := workspacePaymentLeak(body, trailPaymentCents, trailPaymentMethod)
	if err != nil {
		return nil, err
	}
	return append(leaks, paymentLeaks...), nil
}

// workspacePaymentLeak reports the payment detail a PARSED body carries: a number equal to the
// amount (either sign), a whole string value that reads as the amount, or a string naming one of
// the markers. It is the same check for every body shape that must not carry a payment: the
// workspace itself, the error envelope a caller gets for someone else's customer, and the feed a
// caller without the trail receives.
//
// A number is read as a number and a marker as a marker, so no clock's digits and no other
// field's digits can be read as a payment.
func workspacePaymentLeak(body string, amount int64, markers ...string) ([]string, error) {
	var tree any
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		return nil, fmt.Errorf("the body is not JSON: %w", err)
	}
	return paymentDetailIn(tree, "", amount, markers), nil
}

// paymentDetailIn walks a PARSED body and names the payment detail it carries, with the JSON
// PATH of the field that carries it.
//
// The amount is read as a NUMBER, and as a whole STRING value that reads as the amount. The
// string rule is what catches the amount written as text in a field no typed struct parses (a
// memo, a note, a reference), which the raw hunt used to catch and the parsed check did not.
//
// The string rule is anchored to the WHOLE value, never to a substring. That anchor is what
// keeps the clock out: a timestamp is one whole string that does not parse as a number, so
// "2026-10-28T12:48:30.034123459Z" is not the amount, while "41234" is.
func paymentDetailIn(v any, path string, amount int64, markers []string) []string {
	var found []string
	switch t := v.(type) {
	case map[string]any:
		for _, key := range sortedKeys(t) {
			found = append(found, paymentDetailIn(t[key], fieldPath(path, key), amount, markers)...)
		}
	case []any:
		for i, item := range t {
			found = append(found, paymentDetailIn(item, fmt.Sprintf("%s[%d]", path, i), amount, markers)...)
		}
	case float64:
		if t == float64(amount) || t == -float64(amount) {
			found = append(found, fmt.Sprintf("the field %s carries the amount %d", path, amount))
		}
	case string:
		if n, ok := wholeAmount(t); ok && (n == amount || n == -amount) {
			found = append(found, fmt.Sprintf("the field %s carries the amount %d", path, amount))
		}
		for _, marker := range markers {
			if strings.Contains(t, marker) {
				found = append(found, fmt.Sprintf("the field %s carries the marker %s", path, marker))
			}
		}
	}
	return found
}

// wholeAmountText matches a string that is a whole number, with or without a decimal spelling of
// a whole figure ("41234", "-41234", "41234.00"). Nothing else: a timestamp, a uuid and a date
// are all rejected here, which is what keeps a clock out of the amount comparison.
var wholeAmountText = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)

// wholeAmount reads a string that is a whole number, so an amount written as TEXT can be compared
// as an amount. A whole figure only: the amount is cents, and a string that is not a whole number
// is not the amount however it is spelled.
func wholeAmount(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if !wholeAmountText.MatchString(s) {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(f), true
}

// sortedKeys returns a map's keys in a fixed order, so a failure names its fields in the same
// order every run.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fieldPath joins a JSON path.
func fieldPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// TestWorkspaceTrailLeak_ReadsAParsedFieldNotTheRawBody is the unit leg of the check itself.
//
// The first body holds the digits 41234 in a timestamp's nanoseconds and carries no payment at
// all: the fixed check must pass it, and the old raw check must have failed it (that is the
// flake). The second body carries a real payment of 41234: the fixed check must fail it.
func TestWorkspaceTrailLeak_ReadsAParsedFieldNotTheRawBody(t *testing.T) {
	clockOnly := `{"as_of":"2026-10-28T12:48:30.034123459Z","payments_withheld":true,` +
		`"header":{"credit_limit_cents":500000,"open_cents":0},"invoices_open":[],"activity":[]}`
	if !strings.Contains(clockOnly, "41234") {
		t.Fatal("the crafted body must hold the digits the old raw check read as an amount")
	}
	leaks, err := workspaceTrailLeak(clockOnly)
	if err != nil {
		t.Fatalf("the crafted body must parse: %v", err)
	}
	if len(leaks) != 0 {
		t.Fatalf("the check read the clock as a payment: %v", leaks)
	}

	leaked := `{"as_of":"2026-10-28T12:48:30.034123459Z","payments_withheld":true,` +
		`"payments":[{"payment_no":"PAY-1","amount_cents":41234,"method":"CHEQUE"}]}`
	leaks, err = workspaceTrailLeak(leaked)
	if err != nil {
		t.Fatalf("the leaked body must parse: %v", err)
	}
	if len(leaks) == 0 {
		t.Fatal("the check let a real payment through")
	}
}

// TestWorkspaceTrailLeak_SeesTheAmountWhereverItIsWritten is the round 2 leg: the amount is the
// amount wherever it is written.
//
// The first body carries the amount as TEXT in the activity feed, a field no typed struct
// parses. The old raw check caught it, because the digits were in the body; the parsed check
// missed it, because the amount was a string and the walk compared strings only with the
// markers. The second body carries the amount as TEXT in a field the typed struct DOES parse,
// which the typed unmarshal refused outright, so the check answered with a JSON parse fault
// where the cause was a leak.
//
// The check must name the leak, in the field's own words, and must not name the parser.
func TestWorkspaceTrailLeak_SeesTheAmountWhereverItIsWritten(t *testing.T) {
	asText := `{"as_of":"2026-10-28T12:48:30.034123459Z","payments_withheld":true,` +
		`"header":{"credit_limit_cents":500000},"activity":[{"kind":"NSF","reference":"PR-1",` +
		`"amount_cents":"41234","memo":"NSF fee"}]}`
	leaks, err := workspaceTrailLeak(asText)
	if err != nil {
		t.Fatalf("the body must parse: %v", err)
	}
	if len(leaks) == 0 {
		t.Fatal("the check let the amount through as text in the activity feed")
	}
	if !strings.Contains(strings.Join(leaks, " "), "41234") {
		t.Fatalf("the leak must name the amount: %v", leaks)
	}

	inAParsedField := `{"as_of":"2026-10-28T12:48:30.034123459Z","payments_withheld":true,` +
		`"payments":[{"payment_no":"PAY-1","amount_cents":"41234","method":"CHEQUE"}]}`
	leaks, err = workspaceTrailLeak(inAParsedField)
	if err != nil {
		t.Fatalf("a payment amount written as text is a LEAK, not a parse fault: %v", err)
	}
	if len(leaks) == 0 {
		t.Fatal("the check let the amount through as text in the payment list")
	}
	for _, leak := range leaks {
		if strings.Contains(leak, "cannot unmarshal") || strings.Contains(leak, "not JSON") {
			t.Fatalf("the failure must name the leak, not the JSON parser: %q", leak)
		}
	}
}

// TestWorkspaceTrailLeak_FailsOnANullPaymentList is the round 2 leg: a payment list that is
// PRESENT and NULL.
//
// The handler writes the key absent, so null is not what the handler means, and a null list
// reads the same as an absent one to anything that only asks whether there is a list. The check
// must fail on it.
func TestWorkspaceTrailLeak_FailsOnANullPaymentList(t *testing.T) {
	nullList := `{"as_of":"2026-10-28T12:48:30.034123459Z","payments":null,"payments_withheld":true,` +
		`"header":{"credit_limit_cents":500000},"activity":[]}`
	leaks, err := workspaceTrailLeak(nullList)
	if err != nil {
		t.Fatalf("the body must parse: %v", err)
	}
	if len(leaks) == 0 {
		t.Fatal("the check read a null payment list as an absent one")
	}
	if !strings.Contains(strings.Join(leaks, " "), "null") {
		t.Fatalf("the leak must name the null list: %v", leaks)
	}
}

// TestWorkspaceTrailLeak_FailsOnAnEmptyPaymentList is the round 2 leg's neighbour: a list that is
// present and empty is still a list, and the handler writes the key absent.
func TestWorkspaceTrailLeak_FailsOnAnEmptyPaymentList(t *testing.T) {
	emptyList := `{"as_of":"2026-10-28T12:48:30.034123459Z","payments":[],"payments_withheld":true,` +
		`"header":{"credit_limit_cents":500000},"activity":[]}`
	leaks, err := workspaceTrailLeak(emptyList)
	if err != nil {
		t.Fatalf("the body must parse: %v", err)
	}
	if len(leaks) == 0 {
		t.Fatal("the check read an empty payment list as an absent one")
	}
}

// TestWorkspaceTrailLeak_IgnoresTheAmountInAnIdOrAClock is the round 2 leg's other side: the
// walk must not read an id's digits or a clock's nanoseconds as the amount.
//
// The scope narrowed on purpose, and this is the leg that says so: the old raw check read the
// digits inside a uuid and inside a timestamp as a leak. Those digits are not a leak, and the
// narrowed scope is the point of the fix.
func TestWorkspaceTrailLeak_IgnoresTheAmountInAnIdOrAClock(t *testing.T) {
	clockOnly := `{"as_of":"2026-10-28T12:48:30.034123459Z","payments_withheld":true,` +
		`"header":{"customer_id":"a41234000-0000-4000-9000-000000000001","credit_limit_cents":500000},` +
		`"activity":[]}`
	leaks, err := workspaceTrailLeak(clockOnly)
	if err != nil {
		t.Fatalf("the body must parse: %v", err)
	}
	if len(leaks) != 0 {
		t.Fatalf("the check read an id's or a clock's digits as the amount: %v", leaks)
	}
}
