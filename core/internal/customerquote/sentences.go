package customerquote

import (
	"fmt"
	"strings"
)

// The sentence catalogue, ported from the HH Pro reference
// (hh_pro_dibbits/src/core/actions/customer-quote.ts and
// src/core/domain/customer-quote.ts) per the frozen brief's acceptance and the
// Tech Lead's ruling F-8: the words are ported verbatim, and the three source
// sentences that carry an em dash are ported with normalized punctuation only
// (the dash becomes a colon or semicolon chosen per sentence to preserve
// cadence; every word otherwise unchanged), because the repository's no-dash
// rule binds code too. Each deviation is recorded beside its sentence and in
// the lane's completion report; a byte-for-byte copy needs a recorded
// exception only Colton grants, and that question rides the mid-Session
// checkpoint with this list attached. The catalogue test asserts these exact
// strings, so a silent wording change fails the build.

const (
	// SentenceUnpricedTail ports the unpriced-lines refusal tail
	// (customer-quote.ts:92). Deviation: the source's em dash before "a
	// proposal" becomes a colon.
	SentenceUnpricedTail = " no dealer price yet. Send this order through the quote desk before quoting your customer: a proposal with a $0 line is a promise you'd have to eat."

	// SentenceSentUpdate ports the already-sent refusal for the update
	// operation (customer-quote.ts:202). Deviation: the source's em dash
	// after "sent" becomes a semicolon.
	SentenceSentUpdate = "%s has already been sent; the live link must show exactly what was sent. Revise it to make changes; revising takes the old link down."

	// SentenceSentEdit ports the already-sent refusal for the money-bearing
	// edit operations (customer-quote.ts:228). Deviation: the source's em
	// dash after "sent" becomes a semicolon.
	SentenceSentEdit = "%s has already been sent; revise it to make changes. Revising takes the old link down."

	// The dash-free ported sentences, verbatim.
	SentenceAcceptedNoChange   = "This quote has been accepted and cannot be changed."                             // customer-quote.ts:199 and :225
	SentenceAcceptedNoRevise   = "This quote has been accepted and cannot be revised."                             // customer-quote.ts:347
	SentenceQuoteGone          = "That quote no longer exists."                                                    // customer-quote.ts:137, :197, :224, :298, :345
	SentenceNoLines            = "Add materials to this order before building a quote."                            // customer-quote.ts:149
	SentenceNeedCustomerName   = "Add your customer's name before sending."                                        // customer-quote.ts:299
	SentenceAlreadyAccepted    = "This quote has already been accepted."                                           // customer-quote.ts:300
	SentenceAlreadyExpiredSend = "This quote has already expired. Rebuild it to get fresh pricing."                // customer-quote.ts:307
	SentenceOfferExpired       = "This proposal has expired. Please contact your contractor for an updated price." // customer-quote.ts:412
	SentenceAlreadyResponded   = "This proposal has already been responded to."                                    // customer-quote.ts:415
	SentenceChangesNotOpen     = "This proposal is no longer open for changes."                                    // customer-quote.ts:448
	SentenceNeedChangesMessage = "Tell your contractor what you would like changed."                               // customer-quote.ts:449
	SentenceNeedFullName       = "Please type your full legal name."                                               // customer-quote.ts:419
	SentenceLinkNoLongerValid  = "This link is no longer valid."                                                   // customer-quote.ts:368, :408: the dead-link wording

	// SentenceNameTooLong bounds the typed name, the carried maxTypedName
	// refusal (ERP backend/internal/quote/share.go).
	SentenceNameTooLong = "name is too long"

	// SentenceAffirmativeRequired is the required-separate-act refusal (ERP
	// backend/internal/quote/share.go: the affirmative is a required separate
	// act, not an implied consequence of submitting).
	SentenceAffirmativeRequired = "you must confirm that you accept this quote"
)

// RefuseUnpriced builds the unpriced-lines refusal (R2,
// customer-quote.ts:89-93): the first two line names, a count of the rest, and
// the ported tail with the singular or plural verb.
func RefuseUnpriced(names []string) string {
	shown := names
	if len(shown) > 2 {
		shown = shown[:2]
	}
	head := strings.Join(shown, ", ")
	if len(names) > 2 {
		head += fmt.Sprintf(" and %d more", len(names)-2)
	}
	verb := "have"
	if len(names) == 1 {
		verb = "has"
	}
	return head + " " + verb + SentenceUnpricedTail
}

// ConsentText is the served-and-recorded consent sentence, ported verbatim
// from the HH Pro reference (src/core/domain/customer-quote.ts, CONSENT_TEXT;
// the source sentence is dash-free). It is a server-side constant on both
// legs: the view serves exactly this sentence and the acceptance records it,
// never a client-supplied string, so the recorded attestation cannot be
// doctored by the request that creates it.
const ConsentText = "By typing my name and signing below, I accept this proposal and agree to its scope and price. " +
	"I consent to signing electronically, and understand this has the same legal effect as a handwritten signature."

// MaxTypedName bounds the attested name (the carried maxTypedName, ERP
// backend/internal/quote/share.go): long enough for any real name, short
// enough that the column cannot be used as free-text storage.
const MaxTypedName = 120

// MinTypedName is the carried minimum (HH Pro customer-quote.ts: the typed
// name is at least two characters).
const MinTypedName = 2
