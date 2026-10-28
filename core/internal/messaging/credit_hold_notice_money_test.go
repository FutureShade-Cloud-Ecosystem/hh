package messaging

// credit_hold_notice_money_test.go, FLAKE-10: the credit hold notice's money check reads the
// NOTICE'S OWN WORDS, never the customer's name.
//
// The notice's body is the customer's NAME and a fixed sentence, and the customer's name is the
// customer's own string. The seed puts the clock's nanoseconds in it (the name is "Summit Ridge
// Builders <time.Now().UnixNano()>"), so the body's digits are not fixed by the test: a bare six
// digit run can sit inside a clock's digits, and a raw hunt for "125000" over the whole body was
// answered by the customer's name. The name is taken out of the haystack first, the way the repo
// takes the clock out of a haystack before it hunts digits (internal/tasks/cost_gate_test.go:
// func stripTimestamps).
//
// The check lives here, untagged, so the unit leg below runs in the untagged suite and the
// integration leg calls the same helper.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// noticeMoneyLeaks reports the money a credit hold notice carries in its words or in its ref.
//
// The customer's name is the customer's own string, so it is taken out of the haystack before the
// hunt: an amount in the name is the name's, not the notice's.
func noticeMoneyLeaks(body string, customerName string, ref []byte) []string {
	var leaks []string
	words := body
	if customerName != "" {
		words = strings.ReplaceAll(body, customerName, "<customer>")
	}
	if strings.ContainsAny(words, "$") || strings.Contains(words, "1,250") || strings.Contains(words, "125000") {
		leaks = append(leaks, fmt.Sprintf("the notice words carry an amount: %q", body))
	}
	var parsed map[string]any
	if err := json.Unmarshal(ref, &parsed); err != nil {
		return append(leaks, fmt.Sprintf("the notice ref is not JSON: %v", err))
	}
	for k, v := range parsed {
		value := fmt.Sprint(v)
		if customerName != "" {
			value = strings.ReplaceAll(value, customerName, "<customer>")
		}
		if strings.Contains(k, "amount") || strings.Contains(k, "cents") || strings.Contains(value, "1,250") {
			leaks = append(leaks, fmt.Sprintf("the notice ref carries a money field %q=%v", k, v))
		}
	}
	return leaks
}

// assertNoMoney fails when a notice carries an amount in its words or a money field in its ref.
func assertNoMoney(t *testing.T, who string, customerName string, m Message) {
	t.Helper()
	for _, leak := range noticeMoneyLeaks(m.Body, customerName, m.Ref) {
		t.Errorf("%s: %s", who, leak)
	}
}

// TestNoticeMoneyLeaks_ReadsTheNoticeWordsNotTheCustomersName is the unit leg of the check.
//
// The first notice names a customer whose name holds the amount's digits, the way a seeded clock
// does. The old raw hunt failed it, on the customer's name, which is the flake. The fixed check
// must pass it.
//
// The second notice carries an amount in the notice's own words: the fixed check must fail it.
func TestNoticeMoneyLeaks_ReadsTheNoticeWordsNotTheCustomersName(t *testing.T) {
	name := "Summit Ridge Builders 1790125000123456789"
	body := name + " is on credit hold: take payment before release."
	ref := []byte(`{"topic":"credit.hold","summary":"` + name + ` is on credit hold: take payment before release.",` +
		`"entity":"customer:1790125000123456789"}`)

	// The old check, a raw hunt for the bare digits over the whole body.
	if !strings.Contains(body, "125000") {
		t.Fatal("the crafted name must hold the digits the old raw check read as the amount")
	}
	if leaks := noticeMoneyLeaks(body, name, ref); len(leaks) != 0 {
		t.Fatalf("the check read the customer's name as the notice's money: %v", leaks)
	}

	clean := "A customer is on credit hold: take payment before release."
	if leaks := noticeMoneyLeaks(clean, name, []byte(`{"topic":"credit.hold"}`)); len(leaks) != 0 {
		t.Fatalf("the check must pass a notice that names no amount: %v", leaks)
	}

	leaky := "A customer is on credit hold: take payment of $1,250.00 before release."
	if leaks := noticeMoneyLeaks(leaky, name, []byte(`{"topic":"credit.hold"}`)); len(leaks) == 0 {
		t.Fatal("the check let an amount in the notice's own words pass")
	}
}
