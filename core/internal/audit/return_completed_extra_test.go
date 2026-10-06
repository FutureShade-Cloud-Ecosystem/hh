package audit

import "testing"

// The pos RETURN_COMPLETED completion record (returns.go) sets five Extra
// keys: the return's own number and committed total always, and, on the
// approved path, the approver's staff id, the platform subject and the
// approved amount. All five must survive the deny-by-default allowlist into
// the stored entry: "which return, how much, who decided" is exactly the
// fact the counter trail exists to keep, and a key the allowlist lacks is
// dropped with only a log line to show for it.
func TestReturnCompletedExtraKeys_SurviveTheAllowlist(t *testing.T) {
	base := recordOne(t, SensitiveAccess{
		Action: ActReturnCompleted, EntityType: "pos.return", EntityID: sampleUUID,
		Reason: "CASH",
		Extra: map[string]any{
			"return_no":   "#RET-000042",
			"total_cents": int64(22600),
		},
	})
	// The number rides as '#RET-' plus the return_no_seq value: a sequence
	// count whose digit run (here six) stays under the redaction threshold,
	// so the value must survive the detector VERBATIM, never [redacted].
	if base.Changes["return_no"] != "#RET-000042" {
		t.Errorf("return_no must survive the allowlist verbatim: %+v", base.Changes)
	}
	// total_cents rides as an int64: a non-string allowlisted value passes
	// the chokepoint unchanged, the digits never reach redactCleartext, so
	// even a nine digit cent value cannot trip the seven digit run rule.
	if base.Changes["total_cents"] != int64(22600) {
		t.Errorf("total_cents must survive the allowlist as the committed total: %+v", base.Changes)
	}

	approved := recordOne(t, SensitiveAccess{
		Action: ActReturnCompleted, EntityType: "pos.return", EntityID: sampleUUID,
		Reason: "refund_approval_required",
		Extra: map[string]any{
			"return_no":    "#RET-000042",
			"total_cents":  int64(100000000), // 1,000,000.00 dollars in cents, the run the string rule would eat
			"approver_id":  sampleUUID,
			"subject_ref":  "fxportal-absent",
			"amount_cents": int64(100000000),
		},
	})
	if approved.Changes["return_no"] != "#RET-000042" {
		t.Errorf("approved path: return_no must survive: %+v", approved.Changes)
	}
	if approved.Changes["total_cents"] != int64(100000000) || approved.Changes["amount_cents"] != int64(100000000) {
		t.Errorf("approved path: the integer cents must survive unchanged, never redacted: %+v", approved.Changes)
	}
	if approved.Changes["approver_id"] != sampleUUID {
		t.Errorf("approved path: approver_id must survive: %+v", approved.Changes)
	}
	if approved.Changes["subject_ref"] != "fxportal-absent" {
		t.Errorf("approved path: subject_ref must survive the allowlist: %+v", approved.Changes)
	}
}
