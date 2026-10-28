package audit

// fw1_09c_reason_extra_key_test.go, FW1-09c round 2 (M3's P2-1, FROM-PRINCIPAL 319): the year
// end close's reason rides the TOP-LEVEL SensitiveAccess.Reason field, the house way, and the
// close's Extra map carries no "reason" key beside the year.
//
// The Extra path is deny-by-default: a key that is not allowlisted is DROPPED, and a dropped key
// can carry nothing into the row. The leg locks that from both sides: the top-level reason still
// lands as changes["reason"], and a value any caller puts under the Extra "reason" key is
// dropped, whatever its type. While "reason" sat on the allowlist the Extra path wrote the same
// key a second time, and for a non string value the Extra loop's default branch wrote it
// unredacted, so a caller's map of cleartext reached audit_log verbatim.

import "testing"

// TestYearEndClose_ReasonExtraKeyIsDropped proves the reason rides the top-level field and
// the Extra "reason" key is dropped, whatever it carries.
func TestYearEndClose_ReasonExtraKeyIsDropped(t *testing.T) {
	const reason = "the Controller sealed the year"
	e := recordOne(t, SensitiveAccess{
		Action: "GL_YEAR_END_CLOSE", EntityType: "gl.period", EntityID: sampleUUID,
		ActorID: sampleUUID, Purpose: "YEAR_END_CLOSE", Reason: reason,
		Extra: map[string]any{
			"fiscal_year": 3140,
			// A non string value: the Extra loop's default branch passes one through
			// untouched, so an allowlisted "reason" would carry it into the row verbatim.
			"reason": map[string]any{"sin": "S123456789", "note": "Jane Doe"},
		},
	})
	if e.Changes["reason"] != reason {
		t.Errorf("the row's reason = %v, want %q (the reason rides the top-level field)", e.Changes["reason"], reason)
	}
	if got, ok := e.Changes["fiscal_year"].(int); !ok || got != 3140 {
		t.Errorf("the row's fiscal_year = %v, want 3140 (the year rides Extra)", e.Changes["fiscal_year"])
	}
}
