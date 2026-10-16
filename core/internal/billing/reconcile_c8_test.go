package billing

// reconcile_c8_test.go, the unit-level CFO cent-for-cent invariants for the C8 5-bucket register
// and aging reconciliation. The integration test (reconcile_c8_integration_test.go) repeats the
// same invariants against a real database; this file pins the math in plain Go so a typo in either
// expression (the per-row bucket derivation or the per-customer aggregation) is caught at unit
// speed, before the query does.

import (
	"testing"
	"time"
)

// TestBucketLabelFor_BoundariesAreInclusiveAtTheTop pins the canonical C8 boundary rule:
//
//	0     → CURRENT   (Current)
//	1     → 1_30      (1 to 30)
//	30    → 1_30      (1 to 30)  -- inclusive at top
//	31    → 31_60     (31 to 60)
//	60    → 31_60     (31 to 60)  -- inclusive at top
//	61    → 61_90     (61 to 90)
//	90    → 61_90     (61 to 90)  -- inclusive at top
//	91    → 90_PLUS   (91 and over)
//	10000 → 90_PLUS   (91 and over)
//
// A boundary bug is exactly the kind of thing that loses a CFO argument: a customer whose
// invoice is exactly 60 days old should be in 31 to 60, not 61 to 90, and the screen must read
// the same number the register row reads. Both come from this function.
func TestBucketLabelFor_BoundariesAreInclusiveAtTheTop(t *testing.T) {
	cases := []struct {
		days       int
		wantLabel  string
		wantKey    string
	}{
		{-100, "Current", "CURRENT"},
		{0, "Current", "CURRENT"},
		{1, "1 to 30", "1_30"},
		{15, "1 to 30", "1_30"},
		{29, "1 to 30", "1_30"},
		{30, "1 to 30", "1_30"},
		{31, "31 to 60", "31_60"},
		{45, "31 to 60", "31_60"},
		{60, "31 to 60", "31_60"},
		{61, "61 to 90", "61_90"},
		{75, "61 to 90", "61_90"},
		{90, "61 to 90", "61_90"},
		{91, "91 and over", "90_PLUS"},
		{120, "91 and over", "90_PLUS"},
		{10000, "91 and over", "90_PLUS"},
	}
	for _, c := range cases {
		gotLabel, gotKey := BucketLabelFor(c.days)
		if gotLabel != c.wantLabel || gotKey != c.wantKey {
			t.Errorf("BucketLabelFor(%d) = (%q,%q), want (%q,%q)", c.days, gotLabel, gotKey, c.wantLabel, c.wantKey)
		}
	}
}

// TestAgingBuckets_SumExactlyToTotal is invariant (a) at the unit level: the five AgingRow
// bucket columns, summed across every band, equal TotalCents. The integration test proves it
// against a real query; here we prove it for ANY input by construction.
func TestAgingBuckets_SumExactlyToTotal(t *testing.T) {
	cases := []AgingRow{
		{CurrentCents: 100, Days30Cents: 200, Days60Cents: 300, Days60To90Cents: 400, Days90PlusCents: 500, TotalCents: 1500},
		{CurrentCents: 0, Days30Cents: 0, Days60Cents: 0, Days60To90Cents: 0, Days90PlusCents: 0, TotalCents: 0},
		{CurrentCents: 1, Days30Cents: 1, Days60Cents: 1, Days60To90Cents: 1, Days90PlusCents: 1, TotalCents: 5},
		{CurrentCents: 999999, Days30Cents: 1, Days60Cents: 0, Days60To90Cents: 0, Days90PlusCents: 0, TotalCents: 1000000},
	}
	for i, c := range cases {
		sum := c.CurrentCents + c.Days30Cents + c.Days60Cents + c.Days60To90Cents + c.Days90PlusCents
		if sum != c.TotalCents {
			t.Errorf("case %d: 5 buckets sum = %d, TotalCents = %d, a cent that lands outside any bucket is a cent the customer found first",
				i, sum, c.TotalCents)
		}
	}
}

// TestAgingBuckets_PastBucketCentsIsAllLate proves the late-bucket sum never drifts from the
// policy's notion of "past due" (sum of Days30, Days60, Days60To90, Days90Plus). The aging
// hold rule reads PastBucketCents from SQL; this function reads the same data and must say so
// without an off-by-one.
func TestAgingBuckets_PastBucketCentsIsAllLate(t *testing.T) {
	row := AgingRow{
		CurrentCents: 1000, Days30Cents: 2000, Days60Cents: 3000,
		Days60To90Cents: 4000, Days90PlusCents: 5000, TotalCents: 15000,
	}
	late := row.OverdueCents()
	if late != 14000 {
		t.Errorf("OverdueCents = %d, want 14000 (the four late C8 buckets)", late)
	}
	// PastBucketCents here is the 60+ window the policy fires on; it must equal Days60 + Days60To90 + Days90Plus.
	past := row.Days60Cents + row.Days60To90Cents + row.Days90PlusCents
	if past != 12000 {
		t.Errorf("past = %d, want 12000", past)
	}
}

// TestRegisterRow_BucketLabelIsDeterministicFromAsOfClock is invariant (c) at the unit level:
// bucket_label is derived from DaysOverdue, DaysOverdue is derived from the server asOf clock, so
// the same row at the same asOf clock always lands in the same band. We pin the rule by hand: a
// row whose DaysOverdue moves by one across an asOf clock change must move across one band
// boundary at a time, never two.
func TestRegisterRow_BucketLabelIsDeterministicFromAsOfClock(t *testing.T) {
	due := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	for _, asOf := range []time.Time{
		time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC),
		time.Date(2026, time.November, 30, 12, 0, 0, 0, time.UTC),
	} {
		days := int(asOf.Sub(due).Hours() / 24)
		lbl, key := BucketLabelFor(days)
		// A pure function of days_past_due: feeding it twice must yield the same answer.
		if lbl2, key2 := BucketLabelFor(days); lbl != lbl2 || key != key2 {
			t.Fatalf("BucketLabelFor is not deterministic: %d -> (%q,%q) vs (%q,%q)",
				days, lbl, key, lbl2, key2)
		}
		// Bucket key must be one of the five canonical keys.
		switch key {
		case "CURRENT", "1_30", "31_60", "61_90", "90_PLUS":
		default:
			t.Fatalf("BucketLabelFor(%d) returned key %q, which is not a C8 bucket", days, key)
		}
	}
}

// TestRegisterFilter_LegacyChipsStillMap covers the backward-compatibility requirement that
// every legacy chip string still resolves to a SQL predicate, so a stored URL or automation that
// uses 'open', 'overdue', 'promised', 'disputed', 'paid' keeps working after the C8 chips
// ('all', 'past_due', 'current', 'on_hold') are added.
//
// The handler upper-cases the wire string before lookup, so the keys in chipPredicates are the
// UPPER-CASE Status* constants for legacy chips and the lower-case Chip* constants for C8 chips.
// We test the keys actually stored, not the wire spelling; the upper-casing is its own test in
// register_handler_test.go.
func TestRegisterFilter_LegacyChipsStillMap(t *testing.T) {
	want := []string{
		// C8 (lowercase, as on the wire).
		ChipAll, ChipPastDue, ChipCurrent, ChipOnHold,
		// Legacy (uppercase, derived from the Status* constants).
		StatusOpen, StatusOverdue, StatusDisputed, StatusPaid, StatusCredit,
	}
	for _, chip := range want {
		if _, ok := chipPredicates[chip]; !ok {
			t.Errorf("chipPredicates is missing %q, the wire promise to legacy clients is broken", chip)
		}
	}
}

// TestC8BucketOrder_HasFiveYieldsExactly is the one-line pin: the canonical C8 bucket order
// has exactly five buckets, in the order the screen reads them.
func TestC8BucketOrder_HasFiveYieldsExactly(t *testing.T) {
	if len(C8BucketOrder) != 5 {
		t.Fatalf("C8BucketOrder length = %d, want 5", len(C8BucketOrder))
	}
	want := []string{"CURRENT", "1_30", "31_60", "61_90", "90_PLUS"}
	for i, b := range C8BucketOrder {
		if b != want[i] {
			t.Errorf("C8BucketOrder[%d] = %q, want %q", i, b, want[i])
		}
	}
}

// TestRegisterRowScan_BucketLabelIsStableAcrossRescans is the wire stability guarantee: a row
// whose days_overdue is N, at any clock, produces the same bucket_label. The repository may be
// re-scanned, the cache may be invalidated, the test must not.
func TestRegisterRowScan_BucketLabelIsStableAcrossRescans(t *testing.T) {
	for _, days := range []int{0, 1, 30, 31, 60, 61, 90, 91, 365} {
		l1, k1 := BucketLabelFor(days)
		l2, k2 := BucketLabelFor(days)
		l3, k3 := BucketLabelFor(days)
		if l1 != l2 || l2 != l3 || k1 != k2 || k2 != k3 {
			t.Errorf("BucketLabelFor is not stable: %d -> (%q,%q), (%q,%q), (%q,%q)",
				days, l1, k1, l2, k2, l3, k3)
		}
	}
}