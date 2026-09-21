package customerquote

import (
	"strings"
	"testing"
)

// The sentence catalogue, asserted as written (F-8: the ported words with
// normalized punctuation; the catalogue test asserts the rewritten text, so a
// silent wording change fails the build). Each dash-bearing port records its
// deviation beside the constant and in the lane's completion report.
func TestSentenceCatalogue(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"unpriced tail, colon for the source's em dash", SentenceUnpricedTail,
			" no dealer price yet. Send this order through the quote desk before quoting your customer: a proposal with a $0 line is a promise you'd have to eat."},
		{"sent update refusal, semicolon for the source's em dash", SentenceSentUpdate,
			"%s has already been sent; the live link must show exactly what was sent. Revise it to make changes; revising takes the old link down."},
		{"sent edit refusal, semicolon for the source's em dash", SentenceSentEdit,
			"%s has already been sent; revise it to make changes. Revising takes the old link down."},
		{"accepted no change", SentenceAcceptedNoChange, "This quote has been accepted and cannot be changed."},
		{"accepted no revise", SentenceAcceptedNoRevise, "This quote has been accepted and cannot be revised."},
		{"quote gone", SentenceQuoteGone, "That quote no longer exists."},
		{"no lines", SentenceNoLines, "Add materials to this order before building a quote."},
		{"need customer name", SentenceNeedCustomerName, "Add your customer's name before sending."},
		{"already accepted", SentenceAlreadyAccepted, "This quote has already been accepted."},
		{"already expired at send", SentenceAlreadyExpiredSend, "This quote has already expired. Rebuild it to get fresh pricing."},
		{"offer expired", SentenceOfferExpired, "This proposal has expired. Please contact your contractor for an updated price."},
		{"already responded", SentenceAlreadyResponded, "This proposal has already been responded to."},
		{"changes not open", SentenceChangesNotOpen, "This proposal is no longer open for changes."},
		{"need changes message", SentenceNeedChangesMessage, "Tell your contractor what you would like changed."},
		{"need full name", SentenceNeedFullName, "Please type your full legal name."},
		{"dead link", SentenceLinkNoLongerValid, "This link is no longer valid."},
		{"name too long", SentenceNameTooLong, "name is too long"},
		{"affirmative required", SentenceAffirmativeRequired, "you must confirm that you accept this quote"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s: catalogue drifted\n got: %q\nwant: %q", c.name, c.got, c.want)
		}
	}
	// No dash survives anywhere in the catalogue or the consent sentence.
	for _, s := range []string{SentenceUnpricedTail, SentenceSentUpdate, SentenceSentEdit,
		SentenceAcceptedNoChange, SentenceAcceptedNoRevise, SentenceQuoteGone, SentenceNoLines,
		SentenceNeedCustomerName, SentenceAlreadyAccepted, SentenceAlreadyExpiredSend,
		SentenceOfferExpired, SentenceAlreadyResponded, SentenceChangesNotOpen,
		SentenceNeedChangesMessage, SentenceNeedFullName, SentenceLinkNoLongerValid,
		SentenceNameTooLong, SentenceAffirmativeRequired, ConsentText} {
		if strings.ContainsAny(s, "\u2014\u2013") {
			t.Fatalf("a catalogue string carries an em or en dash: %q", s)
		}
	}
}

// TestConsentTextVerbatim asserts the consent constant, ported word for word
// from the reference (the source sentence is dash-free; no deviation exists).
func TestConsentTextVerbatim(t *testing.T) {
	want := "By typing my name and signing below, I accept this proposal and agree to its scope and price. " +
		"I consent to signing electronically, and understand this has the same legal effect as a handwritten signature."
	if ConsentText != want {
		t.Fatalf("consent constant drifted")
	}
}

func TestRefuseUnpricedSingularAndPlural(t *testing.T) {
	one := RefuseUnpriced([]string{"Fixture Rail Kit"})
	if !strings.HasPrefix(one, "Fixture Rail Kit has no dealer price yet.") {
		t.Fatalf("singular refusal: %q", one)
	}
	two := RefuseUnpriced([]string{"Fixture Rail Kit", "Fixture Door"})
	if !strings.HasPrefix(two, "Fixture Rail Kit, Fixture Door have no dealer price yet.") {
		t.Fatalf("plural refusal: %q", two)
	}
	many := RefuseUnpriced([]string{"Fixture A", "Fixture B", "Fixture C"})
	if !strings.HasPrefix(many, "Fixture A, Fixture B and 1 more have no dealer price yet.") {
		t.Fatalf("more-than-two refusal: %q", many)
	}
	all := []string{one, two, many}
	for _, s := range all {
		if !strings.HasSuffix(s, SentenceUnpricedTail) {
			t.Fatalf("refusal tail drifted: %q", s)
		}
	}
}
