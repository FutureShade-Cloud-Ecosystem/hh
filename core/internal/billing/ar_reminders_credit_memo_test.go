// ar_reminders_credit_memo_test.go, HH-195 unit half: the reminder body must never print a
// negative balance as a positive amount.
package billing

import (
	"strings"
	"testing"
	"time"
)

func TestARReminderBody_NegativeBalanceKeepsItsSign(t *testing.T) {
	due := time.Date(2001, 3, 4, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		cents int64
		want  string
	}{
		{-50, "−CA$0.50"},
		{-12345, "−CA$123.45"},
		{50, "CA$0.50"},
		{104864, "CA$1,048.64"},
	} {
		got := arLandingReminderBody(ReminderContext{InvoiceNo: "X1", DueDate: due, BalanceCents: tc.cents}, 10, "")
		if !strings.Contains(got, " "+tc.want+" ") {
			t.Errorf("balance %d: body %q does not carry %q", tc.cents, got, tc.want)
		}
	}
}
