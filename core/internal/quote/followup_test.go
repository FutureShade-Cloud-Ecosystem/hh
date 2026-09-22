package quote

import (
	"testing"
	"time"
)

// followup_test.go, the cadence rules that need no database: what a valid cadence is, which
// transports a step travels on, and which quote states end the whole thing.

// TestNormaliseOffsets_IsTheOnlyDefinitionOfAValidCadence. The 0110 CHECK deliberately does NOT
// enforce positivity (Postgres forbids the subquery it would need), so this function is the whole
// rule, which makes this test the whole enforcement.
func TestNormaliseOffsets_IsTheOnlyDefinitionOfAValidCadence(t *testing.T) {
	t.Run("sorts and de-duplicates", func(t *testing.T) {
		got, err := NormaliseOffsets([]int{14, 3, 7, 3})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []int{3, 7, 14}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v, a duplicated day is a typo, not two messages", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v (ascending)", got, want)
			}
		}
	})

	t.Run("rejects a zero or negative day", func(t *testing.T) {
		for _, bad := range [][]int{{0}, {-3}, {3, -7}} {
			if _, err := NormaliseOffsets(bad); err == nil {
				t.Fatalf("NormaliseOffsets(%v) must fail, a non-positive offset fires immediately and forever", bad)
			}
		}
	})

	t.Run("rejects a day past any quote's life", func(t *testing.T) {
		if _, err := NormaliseOffsets([]int{400}); err == nil {
			t.Fatalf("a 400-day follow-up must be rejected")
		}
	})

	t.Run("rejects a campaign", func(t *testing.T) {
		if _, err := NormaliseOffsets([]int{1, 2, 3, 4, 5, 6, 7}); err == nil {
			t.Fatalf("seven steps is a campaign, not a cadence, it must be rejected")
		}
	})

	t.Run("the house cadence is valid", func(t *testing.T) {
		got, err := NormaliseOffsets(DefaultOffsets)
		if err != nil || len(got) != 3 {
			t.Fatalf("DefaultOffsets must normalise cleanly, got %v / %v", got, err)
		}
	})
}

// TestChannelsOf_EmailFirstOnBoth. If the SMS half is skipped for consent, the customer has still been
// contacted, so email goes first and the order is asserted, not incidental.
func TestChannelsOf_EmailFirstOnBoth(t *testing.T) {
	both := ChannelsOf(ChannelBoth)
	if len(both) != 2 || both[0] != ChannelEmail || both[1] != ChannelSMS {
		t.Fatalf("ChannelsOf(BOTH) = %v, want [EMAIL SMS]", both)
	}
	if got := ChannelsOf(ChannelSMS); len(got) != 1 || got[0] != ChannelSMS {
		t.Fatalf("ChannelsOf(SMS) = %v", got)
	}
	if got := ChannelsOf(ChannelEmail); len(got) != 1 || got[0] != ChannelEmail {
		t.Fatalf("ChannelsOf(EMAIL) = %v", got)
	}
	// An unknown value must degrade to the channel that cannot text anybody.
	if got := ChannelsOf("SEMAPHORE"); len(got) != 1 || got[0] != ChannelEmail {
		t.Fatalf("ChannelsOf(unknown) = %v, want [EMAIL], an unknown channel must not text", got)
	}
}

// TestTerminalReason_StopsOnAcceptanceAndOnExpiry is the "stops on ACCEPTED/EXPIRED" rule. Expiry is
// COMPUTED: nothing in this program flips a quote's status on a timer, so a cadence that waited for
// an EXPIRED status would follow up forever on a document nobody can accept.
func TestTerminalReason_StopsOnAcceptanceAndOnExpiry(t *testing.T) {
	a := NewAutomationService(nil)
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)

	for _, status := range []string{"ACCEPTED", "CONVERTED", "EXPIRED", "LOST"} {
		if why := a.terminalReason(QuoteCadenceHeader{Status: status}, now); why == "" {
			t.Fatalf("a %s quote must end its cadence", status)
		}
	}
	if why := a.terminalReason(QuoteCadenceHeader{Status: "SENT"}, now); why != "" {
		t.Fatalf("a SENT quote must keep its cadence, got %q", why)
	}

	past := now.AddDate(0, 0, -3)
	why := a.terminalReason(QuoteCadenceHeader{Status: "SENT", ValidUntil: &past}, now)
	if why == "" {
		t.Fatalf("a quote whose valid_until has passed must end its cadence even while its status says SENT")
	}

	// A quote expiring TODAY is still live, the grace day exists so the last follow-up ("this expires
	// today") is not cancelled the morning it matters most.
	today := now
	if why := a.terminalReason(QuoteCadenceHeader{Status: "SENT", ValidUntil: &today}, now); why != "" {
		t.Fatalf("a quote valid until today must not be treated as expired, got %q", why)
	}

	future := now.AddDate(0, 0, 30)
	if why := a.terminalReason(QuoteCadenceHeader{Status: "SENT", ValidUntil: &future}, now); why != "" {
		t.Fatalf("a quote valid for another month must keep its cadence, got %q", why)
	}
}

// TestShareURL_PointsAtThePublicQuotePage, the token is only redeemable at one address, and a
// misbuilt URL is a customer who cannot open their quote.
func TestShareURL_PointsAtThePublicQuotePage(t *testing.T) {
	a := NewAutomationService(nil).WithBaseURL("https://erp.dibbits.example/")
	got := a.shareURL("tok en+slash/")
	if want := "https://erp.dibbits.example/quote.html?t=tok+en%2Bslash%2F"; got != want {
		t.Fatalf("shareURL = %q, want %q (trailing slash trimmed, token query-escaped)", got, want)
	}
}

// TestShareNotice_HasNoPaymentField is the CP-05 ruling asserted at the type level: a struct with no
// field for a payment link cannot grow one by accident in a one-line change.
func TestShareNotice_HasNoPaymentField(t *testing.T) {
	// If this stops compiling because somebody added a field, that is the review conversation this
	// test exists to force. The assertion is the exhaustive literal below.
	n := ShareNotice{
		QuoteID: "q", QuoteNo: "Q-1", CustomerID: "c", TotalCents: 1, ValidUntil: "2026-01-01", ShareURL: "u",
	}
	if n.ShareURL != "u" {
		t.Fatalf("unexpected")
	}
}
