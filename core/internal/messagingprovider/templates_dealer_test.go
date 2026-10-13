package messagingprovider

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

func renderedTexts() []string {
	r := Recipient{Name: "Pat", Phone: "+15550100"}
	return []string{
		RenderAR(ARNotice{InvoiceNo: "INV-1", AmountCents: 1000, DueDate: "2026-01-02"}, r).Body,
		RenderAR(ARNotice{InvoiceNo: "INV-1", AmountCents: 1000, DaysPastDue: 5}, r).Body,
		RenderAR(ARNotice{InvoiceNo: "STMT-1", AmountCents: 1000}, r).Body,
		RenderDispatch(DispatchNotice{OrderNo: "SO-1", StageRef: "Q1"}, r).Body,
	}
}

// The SMS signature is the dealer's own configured name.
func TestSMSSignature_ReadsTheConfiguredDealerName(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	for _, body := range renderedTexts() {
		if !strings.HasSuffix(body, ", Gable Landscape Supply") {
			t.Errorf("text %q does not end with the configured dealer name", body)
		}
	}
}

// With no dealer name set the signature is a neutral role word, never another dealer's name.
func TestSMSSignature_NeutralWhenNoDealerName(t *testing.T) {
	dealer.Set("")
	for _, body := range renderedTexts() {
		if strings.Contains(strings.ToLower(body), "dibbits") {
			t.Errorf("text %q carries a dealer's name from code", body)
		}
		if !strings.HasSuffix(body, ", your dealer") {
			t.Errorf("text %q does not end with the neutral signature", body)
		}
	}
}

func quoteBodies() []string {
	r := Recipient{Name: "Pat", Phone: "+15550100"}
	return []string{
		RenderQuoteShare(QuoteShareNotice{QuoteNo: "Q-1", TotalCents: 1000, ShareURL: "https://x.test/q?t=abc"}, r).Body,
		RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-1", StepDay: 3}, r).Body,
		RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-1", StepDay: 7}, r).Body,
		RenderQuoteFollowupSMS(QuoteFollowupNotice{QuoteNo: "Q-1", StepDay: 14}, r).Body,
		RenderQuoteFollowupEmail(QuoteFollowupNotice{QuoteNo: "Q-1", StepDay: 3}, "Pat").Body,
	}
}

// With no dealer name the quote texts and the email read as plain English, never "your your dealer".
func TestQuoteTexts_NeutralCase(t *testing.T) {
	dealer.Set("")
	for _, body := range quoteBodies() {
		if strings.Contains(body, "your your") || strings.Contains(body, "your dealer quote") {
			t.Errorf("text %q doubles the neutral word", body)
		}
	}
	share := RenderQuoteShare(QuoteShareNotice{QuoteNo: "Q-1", ShareURL: "https://x.test/q"}, Recipient{Name: "Pat"}).Body
	if !strings.HasPrefix(share, "Hi Pat: your quote Q-1 is ready: https://x.test/q") {
		t.Errorf("share text %q", share)
	}
	subj := RenderQuoteFollowupEmail(QuoteFollowupNotice{QuoteNo: "Q-1"}, "Pat").Subject
	if subj != "Your quote Q-1" {
		t.Errorf("subject %q must be capitalised with no dangling comma", subj)
	}
}

// With a dealer name the quote texts carry it.
func TestQuoteTexts_NamedCase(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	share := RenderQuoteShare(QuoteShareNotice{QuoteNo: "Q-1", ShareURL: "https://x.test/q"}, Recipient{Name: "Pat"}).Body
	if !strings.Contains(share, "your quote Q-1 from Gable Landscape Supply is ready") {
		t.Errorf("share text %q", share)
	}
	for _, body := range quoteBodies()[1:] {
		if !strings.Contains(body, "Gable Landscape Supply") {
			t.Errorf("text %q lacks the dealer name", body)
		}
	}
	subj := RenderQuoteFollowupEmail(QuoteFollowupNotice{QuoteNo: "Q-1"}, "Pat").Subject
	if subj != "Gable Landscape Supply, quote Q-1" {
		t.Errorf("subject %q", subj)
	}
}

// A long dealer name shortens the words, never the link or the opt out line.
func TestQuoteShare_LongDealerNameKeepsTheFullLink(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	url := "https://demo.example.test/q?t=" + strings.Repeat("a", 43)
	m := RenderQuoteShare(QuoteShareNotice{QuoteNo: "Q-2026-0001", TotalCents: 123456, ValidUntil: "soon", ShareURL: url}, Recipient{Name: "Mark", Phone: "+15550100"})
	if !strings.Contains(m.Body, url) {
		t.Errorf("link cut: %q", m.Body)
	}
	if !strings.HasSuffix(m.Body, "Reply STOP to opt out.") {
		t.Errorf("opt out cut: %q", m.Body)
	}
	if n := len([]rune(m.Body)); n > smsSegment {
		t.Errorf("body is %d runes", n)
	}
}
