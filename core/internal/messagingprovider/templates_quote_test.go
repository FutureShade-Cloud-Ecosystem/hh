package messagingprovider

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// templates_quote_test.go, what the customer actually reads, and the one thing that must never appear
// in it.

// TestRenderQuoteShare_CarriesTheLinkAndNamesTheBusiness. A quote text that does not say who it is
// from, or does not carry the link, is either ignored or reported as spam.
func TestRenderQuoteShare_CarriesTheLinkAndNamesTheBusiness(t *testing.T) {
	m := RenderQuoteShare(QuoteShareNotice{
		QuoteNo: "Q-2026-0001", TotalCents: 481250, ValidUntil: "2026-09-04",
		ShareURL: "https://erp.example/quote.html?t=abc123",
	}, Recipient{Name: "Mark Quinte", Phone: "+16135551001"})

	if m.Channel != notify.SMSChannel {
		t.Fatalf("channel = %s, want SMS", m.Channel)
	}
	if m.To != "+16135551001" {
		t.Fatalf("To = %q, want the recipient's number", m.To)
	}
	for _, want := range []string{"Mark", "Q-2026-0001", "https://erp.example/quote.html?t=abc123", senderName} {
		if !strings.Contains(m.Body, want) {
			t.Fatalf("body %q is missing %q", m.Body, want)
		}
	}
	if !strings.Contains(strings.ToUpper(m.Body), "STOP") {
		t.Fatalf("body %q must tell the recipient how to opt out", m.Body)
	}
}

// TestQuoteTemplates_NeverCarryAPaymentLink is the CP-05 ruling, asserted rather than assumed.
//
// The strongest form of this guarantee is structural, the payload types have no payment field, so
// this test asserts the OUTPUT of every quote-side template against the vocabulary a payment link
// would have to use. If somebody ever adds one, this fails by name.
func TestQuoteTemplates_NeverCarryAPaymentLink(t *testing.T) {
	forbidden := []string{"pay", "payment", "card", "checkout", "invoice.pay", "e-transfer"}
	r := Recipient{Name: "Dave", Phone: "+16135551003"}

	bodies := []string{
		RenderQuoteShare(QuoteShareNotice{
			QuoteNo: "Q-1", TotalCents: 10000, ValidUntil: "2026-09-04",
			ShareURL: "https://erp.example/quote.html?t=t",
		}, r).Body,
		RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-1", StepDay: 3}, r).Body,
		RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-1", StepDay: 7}, r).Body,
		RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-1", StepDay: 14, ValidUntil: "2026-09-04"}, r).Body,
		RenderQuoteFollowupEmail(QuoteFollowupNotice{QuoteNo: "Q-1", StepDay: 3, Email: "a@b.c", TotalCents: 10000}, "Dave").Body,
	}
	for _, body := range bodies {
		low := strings.ToLower(body)
		for _, bad := range forbidden {
			if strings.Contains(low, bad) {
				t.Fatalf("CP-05: a quote message must never mention payment, %q contains %q", body, bad)
			}
		}
	}
}

// TestRenderQuoteFollowupSMS_EscalatesGentlyAndStops, three different messages, and the last one is
// about expiry rather than another ask.
func TestRenderQuoteFollowupSMS_EscalatesGentlyAndStops(t *testing.T) {
	r := Recipient{Name: "Sarah Bayfield", Phone: "+16135551002"}
	d3 := RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-9", StepDay: 3}, r).Body
	d7 := RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-9", StepDay: 7}, r).Body
	d14 := RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-9", StepDay: 14, ValidUntil: "2026-09-04"}, r).Body

	if d3 == d7 || d7 == d14 || d3 == d14 {
		t.Fatalf("the three steps must not send the same sentence three times:\n%q\n%q\n%q", d3, d7, d14)
	}
	if !strings.Contains(d14, "2026-09-04") {
		t.Fatalf("the last step must name the expiry date, got %q", d14)
	}
	for _, b := range []string{d3, d7, d14} {
		if !strings.Contains(b, "Sarah") {
			t.Fatalf("every step must greet the recipient by first name, got %q", b)
		}
	}
}

// TestRenderQuoteShare_FitsOneSegment, a message that splits is billed twice and arrives out of order
// on some carriers.
func TestRenderQuoteShare_FitsOneSegment(t *testing.T) {
	m := RenderQuoteShare(QuoteShareNotice{
		QuoteNo:  "Q-2026-0001",
		ShareURL: "https://erp.dibbits.example/quote.html?t=" + strings.Repeat("x", 120),
	}, Recipient{Name: "A Very Long Contractor Name Indeed", Phone: "+16135551001"})
	if n := len([]rune(m.Body)); n > smsSegment {
		t.Fatalf("body is %d runes, want <= %d, Render must clamp", n, smsSegment)
	}
}

// TestRenderQuoteFollowupEmail_IsNotClampedAndCarriesTheSubject, email has no segments, and a mail
// with no subject line reads as machine spam.
func TestRenderQuoteFollowupEmail_IsNotClampedAndCarriesTheSubject(t *testing.T) {
	m := RenderQuoteFollowupEmail(QuoteFollowupNotice{
		QuoteNo: "Q-2026-0001", StepDay: 7, Email: "sarah@example.test", TotalCents: 481250,
		ShareURL: "https://erp.example/quote.html?t=abc",
	}, "Sarah Bayfield")

	if m.Channel != notify.EmailChannel {
		t.Fatalf("channel = %s, want EMAIL", m.Channel)
	}
	if m.To != "sarah@example.test" {
		t.Fatalf("To = %q", m.To)
	}
	if !strings.Contains(m.Subject, "Q-2026-0001") {
		t.Fatalf("subject %q must name the quote", m.Subject)
	}
	if !strings.Contains(m.Body, "Sarah Bayfield") {
		t.Fatalf("email opens with the full name, got %q", m.Body)
	}
	if !strings.Contains(m.Body, "$4,812.50") {
		t.Fatalf("email must state the quoted total, got %q", m.Body)
	}
	if !strings.Contains(m.Body, "https://erp.example/quote.html?t=abc") {
		t.Fatalf("email must carry the share link, got %q", m.Body)
	}
}
