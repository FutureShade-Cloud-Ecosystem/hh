package messagingprovider

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// templates_test.go, guards on what a customer actually reads. A body that drops the reference, or
// that quietly runs to three billed segments, is a defect nobody notices until the phone bill or the
// counter argument.

func testRecipient() Recipient { return Recipient{Name: "Sam Whitfield", Phone: "+16135550142"} }

func TestDispatchTemplateLeadsWithReadyAndKeepsTheStageRef(t *testing.T) {
	m := RenderDispatch(DispatchNotice{
		OrderID: "07000000-0000-4000-9000-000000000001", OrderNo: "SO-2026-0001",
		BranchName: "Trenton", StageLocation: "Bay 3", StageRef: "STG-4KQ7RM",
	}, testRecipient())

	if m.Channel != notify.SMSChannel {
		t.Fatalf("channel = %q", m.Channel)
	}
	if m.To != "+16135550142" {
		t.Fatalf("to = %q", m.To)
	}
	for _, want := range []string{"Hi Sam", "SO-2026-0001", "ready", "Trenton", "STG-4KQ7RM", senderName} {
		if !strings.Contains(m.Body, want) {
			t.Fatalf("body missing %q: %q", want, m.Body)
		}
	}
	// First name only, a text is not correspondence.
	if strings.Contains(m.Body, "Whitfield") {
		t.Fatalf("body uses the full name: %q", m.Body)
	}
	if len([]rune(m.Body)) > smsSegment {
		t.Fatalf("body is %d chars, over one segment: %q", len([]rune(m.Body)), m.Body)
	}
}

func TestDispatchTemplateFallsBackToAReadableReference(t *testing.T) {
	m := RenderDispatch(DispatchNotice{OrderID: "07000000-0000-4000-9000-0000000000ab"}, Recipient{Phone: "+16135550142"})
	if !strings.Contains(m.Body, "Hi:") && !strings.HasPrefix(m.Body, "Hi:") {
		t.Fatalf("nameless greeting missing: %q", m.Body)
	}
	if !strings.Contains(m.Body, "000000AB") {
		t.Fatalf("body should fall back to a short id when there is no order number: %q", m.Body)
	}
	if !strings.Contains(m.Body, "the yard") {
		t.Fatalf("body should fall back to a generic location: %q", m.Body)
	}
}

func TestARTemplateChangesToneWhenPastDue(t *testing.T) {
	courtesy := RenderAR(ARNotice{InvoiceNo: "INV-2026-0042", AmountCents: 128_450, DueDate: "2026-08-14"}, testRecipient())
	if !strings.Contains(courtesy.Body, "is due 2026-08-14") {
		t.Fatalf("courtesy body = %q", courtesy.Body)
	}
	if strings.Contains(courtesy.Body, "past due") {
		t.Fatalf("a not-yet-due invoice must not read as collections: %q", courtesy.Body)
	}

	late := RenderAR(ARNotice{InvoiceNo: "INV-2026-0042", AmountCents: 128_450, DaysPastDue: 45}, testRecipient())
	for _, want := range []string{"45 days past due", "$1,284.50", "INV-2026-0042"} {
		if !strings.Contains(late.Body, want) {
			t.Fatalf("past-due body missing %q: %q", want, late.Body)
		}
	}
}

func TestARTemplateFormatsMoneyOnceOnTheWayOut(t *testing.T) {
	cases := map[int64]string{
		0:          "$0.00",
		5:          "$0.05",
		99:         "$0.99",
		100:        "$1.00",
		128_450:    "$1,284.50",
		1_000_000:  "$10,000.00",
		-128_450:   "-$1,284.50",
		99_999_999: "$999,999.99",
	}
	for cents, want := range cases {
		if got := formatCAD(cents); got != want {
			t.Fatalf("formatCAD(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestBodyIsClampedToOneSegmentVisibly(t *testing.T) {
	long := strings.Repeat("x", 400)
	m := RenderDispatch(DispatchNotice{OrderNo: long, BranchName: "Trenton"}, testRecipient())
	r := []rune(m.Body)
	if len(r) > smsSegment {
		t.Fatalf("clamped body is still %d chars", len(r))
	}
	if r[len(r)-1] != '…' {
		t.Fatalf("truncation must be visible to the reader, got %q", string(r[len(r)-8:]))
	}
}

func TestTemplatesCarryNoLink(t *testing.T) {
	// A link in an unsolicited text reads as phishing and gets the sending number blocked.
	bodies := []string{
		RenderDispatch(DispatchNotice{OrderNo: "SO-1", StageRef: "STG-1"}, testRecipient()).Body,
		RenderAR(ARNotice{InvoiceNo: "INV-1", AmountCents: 100, DaysPastDue: 3}, testRecipient()).Body,
	}
	for _, b := range bodies {
		for _, bad := range []string{"http://", "https://", "www."} {
			if strings.Contains(b, bad) {
				t.Fatalf("body carries a link (%q): %q", bad, b)
			}
		}
	}
}

func TestE164ShapeCheck(t *testing.T) {
	good := []string{"+16135550142", "+442071838750", "+15550100999"}
	bad := []string{"", "6135550142", "(613) 555-0142", "+0613555014", "+1613555014a", "+1", "1613555014212345678"}
	for _, s := range good {
		if !looksE164(s) {
			t.Fatalf("looksE164(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if looksE164(s) {
			t.Fatalf("looksE164(%q) = true, want false", s)
		}
	}
}
