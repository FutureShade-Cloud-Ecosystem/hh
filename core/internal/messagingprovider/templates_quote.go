package messagingprovider

import (
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

// templates_quote.go, the two quote-side message bodies (Wave 3: DIB-77 quote delivery, DIB-58
// follow-up cadence), and the payloads that feed them.
//
// -------------------------------------------------------------------------------------------------
// THESE TEMPLATES CARRY A LINK, AND THE EXISTING ONES DELIBERATELY DO NOT. HERE IS WHY THAT IS NOT A
// CONTRADICTION.
// templates.go states the rule: a link in an UNSOLICITED text reads as phishing and gets the sending
// number filtered. Every message in that file is unsolicited, the customer did not ask to be told
// their invoice is late.
//
// A quote is the opposite case. A salesperson is standing in front of (or on the phone with) a
// customer who has just asked for a price, the customer has given express SMS consent (0109), and the
// entire content of the message is the document they asked for. A text that says "your quote is ready,
// call us for the link" is worse in every dimension: it is more work for the customer, it looks more
// evasive, and it produces a second phone call to the branch.
//
// The link is therefore allowed here, under three conditions that are enforced, not hoped for:
//  1. It is a SHARE link (0086), a single-quote, expiring, revocable read+accept credential. It is
//     not an account login and it grants nothing beyond the one quote.
//  2. IT IS NEVER A PAYMENT LINK. CP-05 is the portal fork and that ruling stands: no message this
//     package produces will ever carry a way to pay. The payload has no field for one.
//  3. The message names the business and the quote number, so a customer who has forgotten asking can
//     tell at a glance whether it is real.
//
// -------------------------------------------------------------------------------------------------

// Topics owned by this package's dispatcher (Wave 3). Like TopicARNotice they live on sms_outbox and
// are drained by the SMS dispatcher only.
const (
	// TopicQuoteShare, a quote's public share link, sent by a salesperson from the quote surface.
	TopicQuoteShare = "sms.quote.share"
	// TopicQuoteFollowupSMS, a scheduled cadence nudge about a quote that has gone quiet.
	TopicQuoteFollowupSMS = "sms.quote.followup"
	// TopicQuoteFollowupEmail, the SAME cadence's email step.
	//
	// It rides sms_outbox despite being email, and that is deliberate: the shared events_outbox
	// dispatcher freezes its handler map before it starts running, so a module wired after boot cannot
	// register a handler there without racing it (0097's header, and the Wave 2 ruling). This package
	// already owns a dispatcher with the same retry and dead-letter policy, so the follow-up's email
	// step travels the queue this bundle owns and is delivered through the notify.Notifier port -
	// which is the same port the portal's mail travels. The table's name is the only thing that is
	// wrong about it, and renaming a shipped table is not worth a migration.
	TopicQuoteFollowupEmail = "notice.quote.followup.email"
)

// QuoteShareNotice is the DIB-77 payload: send this quote's share link to this customer.
//
// It carries the RAW share URL because a share token is unrecoverable after minting (0086 stores only
// the hash), there is nothing to look up at delivery time. That makes this payload sensitive in the
// same way an invite mail is: the row's payload is redacted to '{}' the moment it is published, by the
// outbox's existing published-row redaction.
type QuoteShareNotice struct {
	QuoteID    string `json:"quote_id"`
	QuoteNo    string `json:"quote_no"`
	CustomerID string `json:"customer_id"`
	TotalCents int64  `json:"total_cents"`
	ValidUntil string `json:"valid_until"` // ISO date
	ShareURL   string `json:"share_url"`
	// To is an optional pre-resolved E.164 recipient (see ARNotice.To). Left empty by the quote
	// surface so that consent is re-checked at delivery time.
	To string `json:"to,omitempty"`
	// NOTE: there is deliberately no PayURL, no AmountDue and no payment token. CP-05 ruling.
}

// QuoteFollowupNotice is the DIB-58 payload for one cadence step, on either channel. StepDay is on it
// so the wording can escalate gently rather than repeating one sentence three times.
type QuoteFollowupNotice struct {
	QuoteID    string `json:"quote_id"`
	QuoteNo    string `json:"quote_no"`
	CustomerID string `json:"customer_id"`
	TotalCents int64  `json:"total_cents"`
	ValidUntil string `json:"valid_until"`
	StepDay    int    `json:"step_day"`
	ShareURL   string `json:"share_url,omitempty"` // present when a live share link exists
	Email      string `json:"email,omitempty"`     // EMAIL channel only
	To         string `json:"to,omitempty"`
}

// RenderQuoteShare builds the quote-delivery text.
func RenderQuoteShare(n QuoteShareNotice, r Recipient) notify.Message {
	ref := n.QuoteNo
	if ref == "" {
		ref = shortID(n.QuoteID)
	}
	head := fmt.Sprintf("%s: your quote %s", greet(r), ref)
	if n.TotalCents > 0 {
		head += " for " + formatCAD(n.TotalCents)
	}
	if name := dealer.Name(); name != "" {
		head += " from " + name
	}
	tail := " is ready: " + n.ShareURL
	if n.ValidUntil != "" {
		tail += fmt.Sprintf(" (valid to %s)", n.ValidUntil)
	}
	tail += ". Reply STOP to opt out."
	// The clamp shortens the words before the link, never the link or the opt out line.
	if room := smsSegment - len([]rune(tail)); len([]rune(head)) > room {
		if room < 1 {
			room = 1
		}
		head = string([]rune(head)[:room-1]) + "…"
	}
	return notify.Message{Channel: notify.SMSChannel, To: r.Phone, Body: clampSegment(head + tail)}
}

// RenderQuoteFollowupSMS builds one cadence step's text.
//
// THE WORDING ESCALATES, AND IT NEVER PRESSURES. Day 3 asks whether they have questions; day 7 offers
// to walk through it; the last step says the quote is about to expire and then stops. A fourth message
// does not exist, because the cadence that keeps going after three unanswered texts is the one that
// gets the number reported.
func RenderQuoteFollowupSMS(n QuoteFollowupNotice, r Recipient) notify.Message {
	ref := n.QuoteNo
	if ref == "" {
		ref = shortID(n.QuoteID)
	}
	var body string
	switch {
	case n.StepDay <= 3:
		body = fmt.Sprintf("%s: just checking you got quote %s from %s. Any questions, reply or call us.",
			greet(r), ref, senderName())
	case n.StepDay <= 7:
		body = fmt.Sprintf("%s: quote %s is still open, happy to walk through the pricing or adjust quantities., %s",
			greet(r), ref, senderName())
	default:
		body = fmt.Sprintf("%s: quote %s expires %s. Let us know if you'd like it held or re-priced., %s",
			greet(r), ref, orText(n.ValidUntil, "soon"), senderName())
	}
	if n.ShareURL != "" {
		body += " " + n.ShareURL
	}
	return notify.Message{Channel: notify.SMSChannel, To: r.Phone, Body: clampSegment(body)}
}

// RenderQuoteFollowupEmail builds the email step. It is NOT clamped to a segment (email has no
// segments) and it may say more than a text can, but it says the same thing, because a customer who
// gets both must not receive two different stories.
func RenderQuoteFollowupEmail(n QuoteFollowupNotice, name string) notify.Message {
	ref := n.QuoteNo
	if ref == "" {
		ref = shortID(n.QuoteID)
	}
	greeting := "Hello"
	if name = strings.TrimSpace(name); name != "" {
		greeting = "Hello " + name
	}
	var lead string
	switch {
	case n.StepDay <= 3:
		lead = fmt.Sprintf("Just checking that quote %s reached you.", ref)
	case n.StepDay <= 7:
		lead = fmt.Sprintf("Quote %s is still open. We're happy to walk through the pricing or adjust quantities.", ref)
	default:
		lead = fmt.Sprintf("Quote %s expires %s.", ref, orText(n.ValidUntil, "shortly"))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s,\n\n%s\n", greeting, lead)
	if n.TotalCents > 0 {
		fmt.Fprintf(&b, "\nQuoted total: %s\n", formatCAD(n.TotalCents))
	}
	if n.ShareURL != "" {
		fmt.Fprintf(&b, "\nYou can review and accept it here:\n%s\n", n.ShareURL)
	}
	fmt.Fprintf(&b, "\nIf you'd rather not receive follow-ups on this quote, reply to this message and we'll stop them.\n\n- %s\n", senderName())

	return notify.Message{
		Channel: notify.EmailChannel,
		To:      n.Email,
		Subject: quoteSubject(ref),
		Body:    b.String(),
	}
}

// orText returns s, or the fallback when s is blank. It exists so a missing date renders as "soon"
// rather than as an empty gap in a sentence a customer reads.
func orText(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// quoteSubject names the dealer when one is configured, else a plain capitalised form.
func quoteSubject(ref string) string {
	if name := dealer.Name(); name != "" {
		return fmt.Sprintf("%s, quote %s", name, ref)
	}
	return "Your quote " + ref
}
