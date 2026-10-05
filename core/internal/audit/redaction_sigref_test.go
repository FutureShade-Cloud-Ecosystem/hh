package audit

import "testing"

// The signature reference the pos provider mints is "sig_" plus 8 random bytes hex-encoded, 16
// lowercase hex characters. About one capture in five that hex carries a run of 7 or more consecutive
// decimal digits, which redactCleartext reads as a raw identifier (a PAN runs 13-19 digits, a SIN 9,
// a registry number about 10), so the audit row stored masked_ref as "[redacted]" and the trail lost
// its only reference. These tests pin the cure at the chokepoint: the exact minted shape passes with
// a long digit run aboard, and every identifier-shaped value, including a PAN-shaped all-decimal tail
// riding the sig_ prefix, still redacts.

// TestRedactCleartext_SignatureReferenceKept proves the system's own reference survives the filter
// even when its hex holds a 7+ digit run, the case that redacted one capture in five.
func TestRedactCleartext_SignatureReferenceKept(t *testing.T) {
	refs := []string{
		"sig_1234567890abcdef", // a 10 digit run opening the hex
		"sig_7777777777deadbe", // a 10 digit run in the middle
		"sig_abc12345678901de", // an 11 digit run broken by letters on both sides
		"sig_abcdefabcdefabcd", // no long digit run: passed before, must still pass
	}
	for _, ref := range refs {
		got, redacted := redactCleartext(ref)
		if redacted || got != ref {
			t.Errorf("redactCleartext(%q) = %q, %v; want the minted reference intact", ref, got, redacted)
		}
	}
}

// TestRedactCleartext_IdentifierShapesStillRedacted proves the exemption is bounded to the minted
// shape and nothing else: a bare PAN, a SIN and a registry number redact as before, and so does a
// PAN-shaped all-decimal tail riding the sig_ prefix. The minted tail is hex and almost always
// carries a letter; a 16 digit decimal run is exactly the PAN shape, and deny-by-default wins for
// anything the provider does not actually mint.
func TestRedactCleartext_IdentifierShapesStillRedacted(t *testing.T) {
	raw := []string{
		"4111111111111111",      // a 16 digit PAN
		"123456789",             // a 9 digit SIN
		"1234567890",            // a 10 digit registry number
		"sig_4111111111111111",  // a PAN-shaped all-decimal tail on the sig_ prefix
		"sig_1234567890ABCDEF",  // uppercase hex is not the minted shape
		"sig_1234567890abcdeg",  // g is not a hex character
		"sig_11234567890123456", // 17 tail characters, not the minted shape
		"sig_1234567890abcde",   // 15 tail characters, not the minted shape
	}
	for _, s := range raw {
		got, redacted := redactCleartext(s)
		if !redacted || got != "[redacted]" {
			t.Errorf("redactCleartext(%q) = %q, %v; want \"[redacted]\"", s, got, redacted)
		}
	}
}

// TestRecord_KeepsSignatureReferenceMaskedRef proves the boundary end to end at the service seam:
// a SIGNATURE_CAPTURE row whose masked_ref is a minted reference persists that reference, not the
// redaction placeholder.
func TestRecord_KeepsSignatureReferenceMaskedRef(t *testing.T) {
	const ref = "sig_1234567890abcdef" // the hex holds a 10 digit run
	e := recordOne(t, SensitiveAccess{
		Action: ActSignatureCapture, EntityType: "signature", EntityID: sampleUUID,
		ActorID: sampleUUID, Purpose: "CARD_PRESENT", MaskedRef: ref,
	})
	if got := e.Changes["masked_ref"]; got != ref {
		t.Fatalf("masked_ref = %v, want the minted signature reference %q intact", got, ref)
	}
}

// TestRecord_RedactsPANShapedMaskedRef proves the same boundary still strips a raw PAN a caller
// might pass as the masked reference: the filter is value-shape based and the exemption must not
// widen what a bad caller can smuggle onto the row.
func TestRecord_RedactsPANShapedMaskedRef(t *testing.T) {
	e := recordOne(t, SensitiveAccess{
		Action: ActSignatureCapture, EntityType: "signature", EntityID: sampleUUID,
		ActorID: sampleUUID, Purpose: "CARD_PRESENT", MaskedRef: "4111111111111111",
	})
	if got := e.Changes["masked_ref"]; got != "[redacted]" {
		t.Fatalf("masked_ref = %v, want \"[redacted]\" for a bare PAN", got)
	}
}
