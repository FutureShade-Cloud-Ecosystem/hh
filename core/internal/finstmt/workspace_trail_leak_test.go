// workspace_trail_leak_test.go, FLAKE-10: the customer workspace's payment trail check reads
// PARSED fields, never the raw body. A timestamp's nanoseconds can hold the digits of an amount
// (the flake's run's body carried "as_of":"2026-10-28T12:48:30.034123459Z", whose fractional
// part holds the seeded payment's 41234), so a raw substring hunt read the clock as a payment.
//
// The check lives here, untagged, so the unit leg below runs in the untagged suite and the
// integration leg calls the same helper.
package finstmt

import (
	"encoding/json"
	"fmt"
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
// It reads PARSED fields, never the raw text. An amount is read as a NUMBER and a method as a
// METHOD, so a clock's nanoseconds (a timestamp is a string) and any other field's digits can
// never be read as a payment.
func workspaceTrailLeak(body string) ([]string, error) {
	var w struct {
		Payments *[]struct {
			AmountCents int64  `json:"amount_cents"`
			Method      string `json:"method"`
		} `json:"payments"`
		PaymentsWithheld bool `json:"payments_withheld"`
		Header           struct {
			LastPaymentAt    *string `json:"last_payment_at"`
			LastPaymentCents *int64  `json:"last_payment_cents"`
		} `json:"header"`
	}
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		return nil, fmt.Errorf("the body is not JSON: %w", err)
	}
	var leaks []string
	if w.Payments != nil {
		leaks = append(leaks, "the body carries the payment list")
		for _, p := range *w.Payments {
			if p.AmountCents == trailPaymentCents {
				leaks = append(leaks, fmt.Sprintf("the payment list carries the amount %d", p.AmountCents))
			}
			if p.Method == trailPaymentMethod {
				leaks = append(leaks, fmt.Sprintf("the payment list carries the method %s", p.Method))
			}
		}
	}
	if !w.PaymentsWithheld {
		leaks = append(leaks, "the body does not carry the withheld flag")
	}
	if w.Header.LastPaymentCents != nil {
		leaks = append(leaks, "the header carries the last payment's amount")
	}
	if w.Header.LastPaymentAt != nil {
		leaks = append(leaks, "the header carries the last payment's time")
	}
	paymentLeaks, err := workspacePaymentLeak(body, trailPaymentCents, trailPaymentMethod)
	if err != nil {
		return nil, err
	}
	return append(leaks, paymentLeaks...), nil
}

// workspacePaymentLeak reports the payment detail a PARSED body carries: a number equal to the
// amount (either sign) or a string naming one of the markers. It is the same check for every
// body shape that must not carry a payment: the workspace itself, the error envelope a caller
// gets for someone else's customer, and the feed a caller without the trail receives.
//
// A number is read as a number and a marker as a marker, so no clock's digits and no other
// field's digits can be read as a payment.
func workspacePaymentLeak(body string, amount int64, markers ...string) ([]string, error) {
	var tree any
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		return nil, fmt.Errorf("the body is not JSON: %w", err)
	}
	return paymentDetailIn(tree, amount, markers), nil
}

// paymentDetailIn walks a PARSED body and names the payment detail it carries. A timestamp is a
// string, so no clock can be read as an amount here.
func paymentDetailIn(v any, amount int64, markers []string) []string {
	var found []string
	switch t := v.(type) {
	case map[string]any:
		for _, item := range t {
			found = append(found, paymentDetailIn(item, amount, markers)...)
		}
	case []any:
		for _, item := range t {
			found = append(found, paymentDetailIn(item, amount, markers)...)
		}
	case float64:
		if t == float64(amount) || t == -float64(amount) {
			found = append(found, fmt.Sprintf("a field carries the amount %d", amount))
		}
	case string:
		for _, marker := range markers {
			if strings.Contains(t, marker) {
				found = append(found, fmt.Sprintf("a field carries the marker %s", marker))
			}
		}
	}
	return found
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
