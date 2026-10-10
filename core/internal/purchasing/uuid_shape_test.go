package purchasing

import "testing"

// P3-1 of the Opus review: the shape check behind the line doors, the vendor edit's path id and the
// create's org stamp must refuse everything Postgres's uuid cast refuses. The old check passed any
// 32 to 36 characters of hex and dashes, so 34 zeros and 36 dashes reached the cast as a 500.
func TestIsUUIDString_ExactShapeOnly(t *testing.T) {
	good := []string{
		"5d1b3f6e-0000-4000-9000-000000000001",
		"5D1B3F6E-0000-4000-9000-000000000001",
		"5d1b3f6e000040009000000000000001",
	}
	for _, s := range good {
		if !isUUIDString(s) {
			t.Errorf("isUUIDString(%q) = false, want true", s)
		}
	}
	bad := []string{
		"", "not-a-uuid",
		"0000000000000000000000000000000000",    // 34 zeros
		"------------------------------------",  // 36 dashes
		"5d1b3f6e-0000-4000-9000-00000000000",   // one short
		"5d1b3f6e-0000-4000-9000-0000000000011", // one long
		"5d1b3f6e00-00-4000-9000-000000000001",  // dashes in the wrong places
		"5d1b3f6e-0000-4000-9000-00000000000g",  // not hex
		"5d1b3f6e0000400090000000000000011",     // 33 digits
		" 5d1b3f6e-0000-4000-9000-00000000001",  // leading space, 36 long
	}
	for _, s := range bad {
		if isUUIDString(s) {
			t.Errorf("isUUIDString(%q) = true, want false", s)
		}
	}
}
