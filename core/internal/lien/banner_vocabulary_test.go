package lien

import (
	"regexp"
	"strings"
	"testing"
)

// banner_vocabulary_test.go, item 3b (session B): the server half of "screen copy never carries an
// internal workstream code".
//
// HOW THIS ONE GOT OUT. The client-side gates (_counter-vocabulary.test.mjs, _shipped-copy.test.mjs)
// read `app/src`. This sentence is composed in Go and arrives over the wire, so both were blind to it
// by construction, and it was found only by driving the real app in a browser and reading the words
// back out of the DOM. It had been stamped at the head of EVERY rendered lien document, a statutory
// notice under the Ontario Construction Act, which goes to an owner, a contractor or their lawyer.
//
// The rule this pins is the same one httpx's counter_vocabulary_test.go pins for error envelopes, one
// layer up: a sentence this product hands a person carries the product's words, never ours.

// The internal vocabularies this product has accumulated. Kept in step with
// app/scripts/shipped-copy.mjs's WORKSTREAM_CODE by hand, there is no way to share a regex across the
// Go and TypeScript halves of this repo, and a second copy that says the same thing is better than a
// gate that only guards one side. D-\d{3} is included; the client list was missing it until item 3a.
var workstreamCode = regexp.MustCompile(`\b(A-\d{2}|WTY-\d{2}|MSG-\d{2}|ADM-\d{2}|DIB-\d+|SWARM-[A-Z0-9-]+|W\d-[A-Z]|D-\d{3})\b`)

func TestBanner_CarriesNoWorkstreamCode(t *testing.T) {
	// GUARD THE GUARD. Every assertion below is "does not contain", which a broken regex or an empty
	// constant satisfies perfectly. Prove the regex still recognises the exact string that was
	// shipping before this test existed.
	wasShipping := "*** DRAFT, statute-derived, pending client validation (DIB-42) ***"
	if !workstreamCode.MatchString(wasShipping) {
		t.Fatal("the workstream-code regex no longer matches the string this test was written for")
	}
	if strings.TrimSpace(draftBanner) == "" {
		t.Fatal("draftBanner is empty, a document with no banner passes this test and fails its reader")
	}

	if got := workstreamCode.FindString(draftBanner); got != "" {
		t.Fatalf("the banner stamped on every rendered lien document carries %q.\n"+
			"A lien document is a statutory notice served on an owner or a contractor. It may say the "+
			"document is a draft and why; it may not say which of our tickets is tracking the sign-off.\n"+
			"banner: %q", got, draftBanner)
	}

	// And the words a reader actually needs are still there, a "fix" that emptied the banner would
	// pass the assertion above and quietly remove the warning the whole mechanism exists to carry.
	for _, must := range []string{"DRAFT", "statute-derived", "pending client validation"} {
		if !strings.Contains(draftBanner, must) {
			t.Fatalf("the banner no longer says %q, the code came out, but so did the warning: %q", must, draftBanner)
		}
	}
}

// Every rendered document gets the banner, and it gets it at the HEAD. A banner further down a
// statutory notice is a banner somebody can miss.
func TestRenderedDocument_LeadsWithTheBanner(t *testing.T) {
	var found bool
	for kind := range templates {
		doc, err := renderTemplate(kind, 1, map[string]any{})
		if err != nil {
			t.Fatalf("%s failed to render: %v", kind, err)
		}
		found = true
		if !strings.HasPrefix(strings.TrimSpace(doc), strings.TrimSpace(draftBanner)) {
			t.Fatalf("%s does not lead with the draft banner:\n%.200s", kind, doc)
		}
		if got := workstreamCode.FindString(doc); got != "" {
			t.Fatalf("%s carries the workstream code %q somewhere in its body", kind, got)
		}
	}
	if !found {
		t.Fatal("no form rendered, this test proved nothing")
	}
}
