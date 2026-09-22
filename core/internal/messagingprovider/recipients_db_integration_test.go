//go:build integration

package messagingprovider

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// recipients_db_integration_test.go, item 1's named cover: the REAL recipient resolver over the real
// consent table (0109). The Wave 2 seam resolved nobody because nothing knew who had agreed to be
// texted; these tests are the proof that it now knows, and still refuses by default.

// consentFixture is one test's customer plus whatever consent it was given. Everything is removed on
// cleanup, so the package stays order-independent under -shuffle=on.
type consentFixture struct {
	f     *arFixture
	prefs *PrefsStore
	res   *ConsentResolver
}

func newConsentFixture(t *testing.T) *consentFixture {
	t.Helper()
	f := newARFixture(t)
	prefs := NewPrefsStore(f.pool)
	return &consentFixture{
		f:     f,
		prefs: prefs,
		res:   NewConsentResolver(prefs, slog.Default()),
	}
}

// TestIntegration_ConsentResolver_ReturnsNobodyWhenOptedOut is the named test item 1 owes.
func TestIntegration_ConsentResolver_ReturnsNobodyWhenOptedOut(t *testing.T) {
	c := newConsentFixture(t)
	ctx := context.Background()

	// Consent granted: the resolver answers with the verified number.
	if err := c.prefs.Upsert(ctx, c.f.CustomerID, "+16135550143", true); err != nil {
		t.Fatalf("upsert consent: %v", err)
	}
	r, err := c.res.CustomerRecipient(ctx, c.f.CustomerID)
	if err != nil {
		t.Fatalf("a verified, opted-in customer must resolve: %v", err)
	}
	if r.Phone != "+16135550143" {
		t.Fatalf("resolved phone = %q, want the verified number from customer_sms_prefs", r.Phone)
	}

	// The customer replies STOP.
	if err := c.prefs.SetOptOut(ctx, c.f.CustomerID, true, "STOP reply"); err != nil {
		t.Fatalf("opt out: %v", err)
	}
	if _, err := c.res.CustomerRecipient(ctx, c.f.CustomerID); !errors.Is(err, ErrNoRecipient) {
		t.Fatalf("an OPTED-OUT customer resolved to a recipient (err = %v), the kill switch does not work", err)
	}

	// And MaySendAt, the question a scheduled producer asks, says never, not "later".
	ok, when, err := c.res.MaySendAt(ctx, c.f.CustomerID, time.Now())
	if err != nil {
		t.Fatalf("MaySendAt: %v", err)
	}
	if ok {
		t.Fatalf("MaySendAt said yes for an opted-out customer")
	}
	if !when.IsZero() {
		t.Fatalf("MaySendAt returned a send time (%s) for a customer who may never be texted", when)
	}

	// Opting back in restores service and clears the reason.
	if err := c.prefs.SetOptOut(ctx, c.f.CustomerID, false, ""); err != nil {
		t.Fatalf("opt back in: %v", err)
	}
	if _, err := c.res.CustomerRecipient(ctx, c.f.CustomerID); err != nil {
		t.Fatalf("a customer who opted back in must resolve again: %v", err)
	}
	p, found, err := c.prefs.Get(ctx, c.f.CustomerID)
	if err != nil || !found {
		t.Fatalf("read back prefs: %v / found=%v", err, found)
	}
	if p.OptOutReason != "" {
		t.Fatalf("opt-out reason %q survived an opt-in, a stale reason beside opted_out=false reads as a bug", p.OptOutReason)
	}
}

// TestIntegration_ConsentResolver_AbsentRowMeansNo. The default is silence, and it is the default
// this program shipped with in Wave 2, that has not changed, it has only become expressible.
func TestIntegration_ConsentResolver_AbsentRowMeansNo(t *testing.T) {
	c := newConsentFixture(t) // no prefs row at all
	if _, err := c.res.CustomerRecipient(context.Background(), c.f.CustomerID); !errors.Is(err, ErrNoRecipient) {
		t.Fatalf("a customer with NO consent row resolved (err = %v), absence must mean no", err)
	}
}

// TestIntegration_ConsentResolver_UnverifiedNumberIsNotConsent. Having a number is not permission to
// use it; this is the distinction the whole table exists to make.
func TestIntegration_ConsentResolver_UnverifiedNumberIsNotConsent(t *testing.T) {
	c := newConsentFixture(t)
	ctx := context.Background()
	if err := c.prefs.Upsert(ctx, c.f.CustomerID, "+16135550144", false); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := c.res.CustomerRecipient(ctx, c.f.CustomerID); !errors.Is(err, ErrNoRecipient) {
		t.Fatalf("an UNVERIFIED number resolved (err = %v)", err)
	}
}

// TestIntegration_PrefsStore_RefusesToVerifyANonE164Number, a malformed number stored as verified is
// a message the carrier rejects and bills us for.
func TestIntegration_PrefsStore_RefusesToVerifyANonE164Number(t *testing.T) {
	c := newConsentFixture(t)
	err := c.prefs.Upsert(context.Background(), c.f.CustomerID, "613-555-0144", true)
	if err == nil {
		t.Fatalf("verifying a non-E.164 number must fail")
	}
	if got := err.Error(); len(got) > 0 && contains(got, "613-555-0144") {
		t.Fatalf("the error echoed the number in full: %q", got)
	}
}

// TestIntegration_ConsentResolver_QuietHoursAreThePerCustomerOnes, a customer who set 10:00-16:00 is
// held until 10:00, not until the house 08:00.
func TestIntegration_ConsentResolver_QuietHoursAreThePerCustomerOnes(t *testing.T) {
	loc, err := time.LoadLocation(DefaultZone)
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	c := newConsentFixture(t)
	ctx := context.Background()
	if err := c.prefs.Upsert(ctx, c.f.CustomerID, "+16135550145", true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := c.prefs.SetQuietHours(ctx, c.f.CustomerID, 10*60, 16*60, DefaultZone); err != nil {
		t.Fatalf("set quiet hours: %v", err)
	}

	early := time.Date(2026, time.August, 5, 8, 30, 0, 0, loc) // inside the HOUSE window, outside theirs
	ok, when, err := c.res.MaySendAt(ctx, c.f.CustomerID, early)
	if err != nil || !ok {
		t.Fatalf("MaySendAt = %v / %v, want a consented customer with a later send time", ok, err)
	}
	if local := when.In(loc); local.Hour() != 10 || local.Minute() != 0 {
		t.Fatalf("send time = %s local, want 10:00, the customer's own window was not honoured",
			local.Format(time.RFC3339))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
