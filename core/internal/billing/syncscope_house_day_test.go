package billing

// syncscope_house_day_test.go, the aging projection's day, pinned at the source of its SQL.
//
// The projection computes days_past_due inside its SELECT, so the day it derives is decided by a
// SQL string in this package, not by a Go call a test could intercept. The behavioural proof is the
// integration leg against the migrated view; this pin keeps the string itself from quietly going
// back to the session's day, and names the zone constant the backend-wide zone census enumerates.

import (
	"strings"
	"testing"
)

func TestAgingColumns_DeriveTheDayFromTheNamedHouseZone(t *testing.T) {
	if !strings.Contains(agingColumnsAt("$4"), "AT TIME ZONE $4::text") {
		t.Fatalf("the aging projection must derive its day from the dealer's zone bound as a parameter, " +
			"the one choice the 0376 migration names, never from the session's now()::date")
	}
	if strings.Contains(agingColumnsAt("$4"), arAgingZone) {
		t.Fatalf("the aging projection must not spell a zone in its text: the dealer's zone is a bind parameter")
	}
	if strings.Contains(strings.ToLower(agingColumnsAt("$4")), "now()::date") {
		t.Fatalf("the aging projection must not read now()::date, it is the session zone's day and " +
			"ages every open item one day early through the Ontario evening")
	}
}
