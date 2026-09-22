package messagingprovider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// provider_test.go, the flag-flip guards. Not one of these carries a real credential: the Twilio
// path is exercised with obviously-fake strings against a local httptest server, which is the whole
// point, the selector and the client must be provable WITHOUT anything live.

// fakeCreds are transparently fake values used only to prove presence checks and header shape.
const (
	fakeSID   = "AC-not-a-real-sid"
	fakeToken = "not-a-real-token"
	fakeFrom  = "+16135550100" // 555-01xx is reserved for fictional use
)

func TestSelectDefaultsToTheStubWhenNothingIsConfigured(t *testing.T) {
	n, err := Select(Settings{}, nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if n.Name() != "sms-stub" {
		t.Fatalf("notifier = %q, want the stub by default", n.Name())
	}
	if n.Live() {
		t.Fatal("the default notifier must report Live() == false")
	}
}

func TestSelectStillDefaultsToTheStubWhenCredentialsExistButTheFlagIsOff(t *testing.T) {
	// Credentials present is NOT consent to send. Only SMS_PROVIDER=twilio is.
	n, err := Select(Settings{AccountSID: fakeSID, AuthToken: fakeToken, FromNumber: fakeFrom}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if n.Name() != "sms-stub" {
		t.Fatalf("notifier = %q, credentials alone must not switch the provider on", n.Name())
	}
}

// TestFlagFlipSelectsTwilioWithoutLiveCredentials is the named acceptance guard: flipping the config
// flag (and supplying a complete credential SET, the values are fake) selects the live Twilio path,
// at the constructor/selector level, with no network call and nothing live.
func TestFlagFlipSelectsTwilioWithoutLiveCredentials(t *testing.T) {
	s := Settings{Provider: "twilio", AccountSID: fakeSID, AuthToken: fakeToken, FromNumber: fakeFrom}
	n, err := Select(s, nil)
	if err != nil {
		t.Fatalf("err = %v, want nil for a complete credential set", err)
	}
	if n.Name() != "twilio" {
		t.Fatalf("notifier = %q, want twilio", n.Name())
	}
	if !n.Live() {
		t.Fatal("the twilio notifier must report Live() == true")
	}
	if _, ok := n.(*TwilioNotifier); !ok {
		t.Fatalf("notifier concrete type = %T, want *TwilioNotifier", n)
	}
}

func TestSelectRefusesToPretendWhenTwilioIsHalfConfigured(t *testing.T) {
	// Asking for twilio without a full credential set must NOT silently run the stub: the operator
	// believes texts are going out. The stub is returned so the server still boots, WITH an error.
	cases := map[string]Settings{
		"no sid":   {Provider: "twilio", AuthToken: fakeToken, FromNumber: fakeFrom},
		"no token": {Provider: "twilio", AccountSID: fakeSID, FromNumber: fakeFrom},
		"no from":  {Provider: "twilio", AccountSID: fakeSID, AuthToken: fakeToken},
		"none":     {Provider: "twilio"},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			n, err := Select(s, nil)
			if !errors.Is(err, ErrTwilioNotConfigured) {
				t.Fatalf("err = %v, want ErrTwilioNotConfigured", err)
			}
			if n == nil || n.Name() != "sms-stub" {
				t.Fatalf("notifier = %v, want the stub so the server still boots", n)
			}
			if n.Live() {
				t.Fatal("the fallback must not claim to be live")
			}
		})
	}
}

func TestSettingsFromEnvDefaultsToStub(t *testing.T) {
	// No environment variables set in the test process → the stub configuration.
	t.Setenv("SMS_PROVIDER", "")
	s := SettingsFromEnv()
	if s.WantsTwilio() {
		t.Fatalf("provider = %q, want the stub default", s.Provider)
	}
	if s.Complete() {
		t.Fatal("Complete() true with no credentials set")
	}
}

func TestSettingsFromEnvReadsTheFlagCaseInsensitively(t *testing.T) {
	t.Setenv("SMS_PROVIDER", "  Twilio  ")
	t.Setenv("TWILIO_ACCOUNT_SID", fakeSID)
	t.Setenv("TWILIO_AUTH_TOKEN", fakeToken)
	t.Setenv("TWILIO_FROM_NUMBER", fakeFrom)
	s := SettingsFromEnv()
	if !s.WantsTwilio() || !s.Complete() {
		t.Fatalf("settings = %+v, want twilio + complete", Settings{Provider: s.Provider})
	}
}

func TestTwilioSendPostsTheExpectedRequest(t *testing.T) {
	var gotPath, gotUser, gotPass, gotTo, gotFrom, gotBody string
	var authOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUser, gotPass, authOK = r.BasicAuth()
		raw, _ := io.ReadAll(r.Body)
		_ = r.ParseForm()
		vals, _ := parseForm(string(raw))
		gotTo, gotFrom, gotBody = vals["To"], vals["From"], vals["Body"]
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM-fake"}`))
	}))
	defer srv.Close()

	n := NewTwilioNotifier(Settings{
		Provider: "twilio", AccountSID: fakeSID, AuthToken: fakeToken, FromNumber: fakeFrom, APIBase: srv.URL,
	}, nil)

	err := n.Send(context.Background(), notify.Message{
		Channel: notify.SMSChannel, To: "+16135550142", Body: "Hi Sam: order SO-1 is ready.",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/Messages.json") || !strings.Contains(gotPath, fakeSID) {
		t.Fatalf("path = %q", gotPath)
	}
	if !authOK || gotUser != fakeSID || gotPass != fakeToken {
		t.Fatalf("basic auth not set from the configured credentials")
	}
	if gotTo != "+16135550142" || gotFrom != fakeFrom || !strings.Contains(gotBody, "order SO-1 is ready") {
		t.Fatalf("form = (%q, %q, %q)", gotTo, gotFrom, gotBody)
	}
}

func TestTwilioRefusesToSendEmail(t *testing.T) {
	n := NewTwilioNotifier(Settings{AccountSID: fakeSID, AuthToken: fakeToken, FromNumber: fakeFrom}, nil)
	err := n.Send(context.Background(), notify.Message{Channel: notify.EmailChannel, To: "a@b.c", Body: "x"})
	if !errors.Is(err, errNotSMS) {
		t.Fatalf("err = %v, want errNotSMS", err)
	}
}

func TestTwilioDistinguishesPermanentFromTransientFailures(t *testing.T) {
	// A retried text is a SECOND text to a customer, so a 4xx (bad number, blocked recipient) must
	// say plainly that a retry cannot help, while 429/5xx must read as transient.
	cases := []struct {
		status      int
		wantPhrase  string
		description string
	}{
		{http.StatusBadRequest, "permanent", "400"},
		{http.StatusForbidden, "permanent", "403"},
		{http.StatusTooManyRequests, "transient", "429 is a rate limit, not a bad request"},
		{http.StatusBadGateway, "transient", "502"},
	}
	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`{"message":"nope"}`))
			}))
			defer srv.Close()
			n := NewTwilioNotifier(Settings{AccountSID: fakeSID, AuthToken: fakeToken, FromNumber: fakeFrom, APIBase: srv.URL}, nil)
			err := n.Send(context.Background(), notify.Message{Channel: notify.SMSChannel, To: fakeFrom, Body: "x"})
			if err == nil {
				t.Fatalf("status %d produced no error", c.status)
			}
			if !strings.Contains(err.Error(), c.wantPhrase) {
				t.Fatalf("err = %q, want it to read as %s", err, c.wantPhrase)
			}
		})
	}
}

func TestTwilioNeverLeaksCredentialsIntoAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"authenticate"}`))
	}))
	defer srv.Close()
	n := NewTwilioNotifier(Settings{AccountSID: fakeSID, AuthToken: fakeToken, FromNumber: fakeFrom, APIBase: srv.URL}, nil)
	err := n.Send(context.Background(), notify.Message{Channel: notify.SMSChannel, To: fakeFrom, Body: "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("the auth token appeared in an error message: %v", err)
	}
}

// parseForm is a tiny x-www-form-urlencoded reader so the test does not depend on the handler having
// already consumed the body.
func parseForm(raw string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, "&") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out[unescape(k)] = unescape(v)
	}
	return out, nil
}

func unescape(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			var v int
			_, err := fmtSscan(s[i+1:i+3], &v)
			if err == nil {
				b.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func fmtSscan(hex string, out *int) (int, error) {
	v := 0
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + int(c-'0')
		case c >= 'a' && c <= 'f':
			v = v*16 + int(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v*16 + int(c-'A'+10)
		default:
			return 0, errors.New("bad hex")
		}
	}
	*out = v
	return 1, nil
}
