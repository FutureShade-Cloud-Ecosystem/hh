package audit

import "testing"

// The signature reference the pos provider mints is "sig_" plus 8 random bytes hex-encoded, 16
// lowercase hex characters. About one capture in five that hex carries a run of 7 or more consecutive
// decimal digits, which redactCleartext reads as a raw identifier (a PAN runs 13-19 digits, a SIN 9,
// a registry number about 10), so the audit row stored masked_ref as "[redacted]" and the trail lost
// its only reference. These tests pin the cure at the chokepoint: the exemption is NARROW (the
// masked_ref of a SIGNATURE_CAPTURE row only, the exact minted shape, no digit run over 12), so the
// general filter, every other field and action, and every identifier-shaped value (a PAN-shaped
// all-decimal tail, a card number-length run beside a letter) still redact.

// TestIsSignatureReference_BoundedToTheMintedShape pins the matcher: the exact minted shape with a
// digit run up to 12 matches; everything else (an all decimal tail, a run of 13 or more digits, a
// PAN-length run beside one letter, uppercase, wrong lengths, extra text around it) does not.
func TestIsSignatureReference_BoundedToTheMintedShape(t *testing.T) {
	yes := []string{"sig_1234567890abcdef", "sig_7777777777deadbe", "sig_abc12345678901de", "sig_abcdefabcdefabcd", "sig_123456789012abcd"}
	no := []string{
		"sig_4111111111111111",  // all decimal: the PAN shape
		"sig_378282246310005a",  // a 15 digit card number plus one letter
		"sig_30569309025904ab",  // a 14 digit card number plus letters
		"sig_4222222222222abc",  // a 13 digit card number plus letters
		"sig_1234567890123abc",  // a 13 digit run, the cap is 12
		"sig_1234567890ABCDEF",  // uppercase
		"sig_1234567890abcdeg",  // not hex
		"sig_1234567890abcde",   // 15 tail characters
		"sig_11234567890abcdef", // 17 tail characters
		"x sig_1234567890abcdef",
		"sig_1234567890abcdef ",
		"sig_1234567890abcdef\n",
		"",
	}
	for _, s := range yes {
		if !isSignatureReference(s) {
			t.Errorf("isSignatureReference(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isSignatureReference(s) {
			t.Errorf("isSignatureReference(%q) = true, want false", s)
		}
	}
}

// TestRedactCleartext_SignatureReferenceKept proves redactCleartext itself no longer grants the
// exemption anywhere (the old shape of this test asserted the opposite): a sig_ shaped string with a
// long digit run redacts when it reaches any field through the general filter.
func TestRedactCleartext_SignatureReferenceKept(t *testing.T) {
	for _, ref := range []string{"sig_1234567890abcdef", "sig_7777777777deadbe"} {
		if got, redacted := redactCleartext(ref); !redacted || got != "[redacted]" {
			t.Errorf("redactCleartext(%q) = %q, %v; the general filter must still redact a 7+ digit run (the exemption lives only in the SIGNATURE_CAPTURE masked_ref path)", ref, got, redacted)
		}
	}
	if got, redacted := redactCleartext("sig_abcdefabcdefabcd"); redacted || got != "sig_abcdefabcdefabcd" {
		t.Errorf("a reference with no long digit run must pass the general filter as before, got %q, %v", got, redacted)
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

// TestRecord_SignatureReferenceExemptionIsNarrow proves the exemption is bounded to the one action and
// the one field: the same minted-shaped reference as masked_ref of another action, in the reason or
// purpose, or as an allowlisted Extra string, redacts; and card number-length runs redact even in the
// capture row's masked_ref.
func TestRecord_SignatureReferenceExemptionIsNarrow(t *testing.T) {
	const ref = "sig_1234567890abcdef"
	other := recordOne(t, SensitiveAccess{
		Action: ActReturnCompleted, EntityType: "pos.return", EntityID: sampleUUID,
		ActorID: sampleUUID, Purpose: "POS_RETURN", MaskedRef: ref,
	})
	if got := other.Changes["masked_ref"]; got != "[redacted]" {
		t.Errorf("masked_ref of another action = %v, want [redacted]", got)
	}
	free := recordOne(t, SensitiveAccess{
		Action: ActSignatureCapture, EntityType: "signature", EntityID: sampleUUID,
		ActorID: sampleUUID, Purpose: ref, Reason: ref, MaskedRef: ref,
	})
	if free.Changes["masked_ref"] != ref {
		t.Errorf("the capture row's own masked_ref = %v, want the reference intact", free.Changes["masked_ref"])
	}
	if free.Changes["purpose"] != "[redacted]" || free.Changes["reason"] != "[redacted]" {
		t.Errorf("purpose and reason carrying the reference shape = %v and %v, want both [redacted]", free.Changes["purpose"], free.Changes["reason"])
	}
	for _, card := range []string{"sig_378282246310005a", "sig_30569309025904ab", "sig_4222222222222abc", "sig_4111111111111111"} {
		e := recordOne(t, SensitiveAccess{
			Action: ActSignatureCapture, EntityType: "signature", EntityID: sampleUUID,
			ActorID: sampleUUID, Purpose: "CARD_PRESENT", MaskedRef: card,
		})
		if got := e.Changes["masked_ref"]; got != "[redacted]" {
			t.Errorf("masked_ref %q = %v, want [redacted] (a card number-length run)", card, got)
		}
	}
}
