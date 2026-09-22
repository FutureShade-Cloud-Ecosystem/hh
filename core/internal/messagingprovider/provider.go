// Package messagingprovider is the out-of-band (SMS) delivery arm of HardscapeOS: the notifiers, the
// message templates, and the durable queue that carries a notice from a committed business fact to a
// provider's API.
//
// IT IS BUILT BEHIND THE EXISTING notify PACKAGE, NOT BESIDE IT.
// Every notifier here satisfies notify.Notifier, the same port the portal's invite/reset mail already
// travels through, and delivery is driven by notify.Dispatcher, with the shipped retry and
// dead-letter policy. What this package adds is (a) a real SMS provider implementation, (b) two
// message templates, and (c) a notify.OutboxStore over the disjoint sms_outbox table (0097), so an
// SMS outage can never retry, block or dead-letter a GL journal.
//
// -------------------------------------------------------------------------------------------------
// THE STUB IS THE DEFAULT. THE FLAG FLIP IS A CONFIG ACT, NOT A CODE ACT.
// Select() returns StubNotifier unless the environment BOTH asks for Twilio and supplies complete
// credentials. Nothing in this repository turns Twilio on. There are no credentials in this package,
// this repository, or any test, only the NAMES of environment variables (documented on Settings).
// Turning it on is: set four environment variables on the box. That is the D-RUNPAY-2 pattern, and it
// is why every walkthrough and every test in this program passes with SMS flag-off.
// -------------------------------------------------------------------------------------------------
package messagingprovider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// Topics owned by this package's dispatcher. They live on sms_outbox (0097), never events_outbox, so
// they cannot collide with a domain topic or be claimed by the ERP's financial dispatcher.
const (
	// TopicARNotice, an accounts-receivable notice to a customer (balance due / past due).
	TopicARNotice = "sms.ar.notice"
	// TopicDispatchNotice, a fulfilment notice to a customer (order staged / out for delivery).
	TopicDispatchNotice = "sms.dispatch.notice"
)

// ErrTwilioNotConfigured, the environment asked for the Twilio provider but did not supply a
// complete credential set. Select returns this ALONGSIDE the stub notifier: delivery degrades to the
// stub rather than crashing the server, and the caller is expected to log it loudly (or, outside dev,
// treat it as fatal). Silently running the stub while the operator believes Twilio is live is the one
// outcome this must never produce.
var ErrTwilioNotConfigured = errors.New("messagingprovider: SMS_PROVIDER=twilio but credentials are incomplete")

// Settings is the SMS configuration surface. It is read from the environment (SettingsFromEnv) rather
// than from internal/config so this module owns its own switch and adds no field to a shared struct
// several teams edit at once.
//
// ENVIRONMENT VARIABLE NAMES (names only, no value of any of these appears anywhere in this repo):
//
//	SMS_PROVIDER         "stub" (default, and the default when unset) | "twilio"
//	TWILIO_ACCOUNT_SID   the Twilio account SID  (server-side only; never reaches the frontend bundle)
//	TWILIO_AUTH_TOKEN    the Twilio auth token   (server-side only)
//	TWILIO_FROM_NUMBER   the sending number in E.164, e.g. +1613...
//	TWILIO_API_BASE      optional override of https://api.twilio.com (used by tests to point at a
//	                     local httptest server; NEVER set in production)
type Settings struct {
	Provider   string // "stub" | "twilio"
	AccountSID string
	AuthToken  string
	FromNumber string
	APIBase    string
}

// SettingsFromEnv reads the SMS switch + credentials from the environment. Absent everything, it
// returns the stub configuration, the default this program runs on.
func SettingsFromEnv() Settings {
	return Settings{
		Provider:   strings.ToLower(strings.TrimSpace(env("SMS_PROVIDER", "stub"))),
		AccountSID: env("TWILIO_ACCOUNT_SID", ""),
		AuthToken:  env("TWILIO_AUTH_TOKEN", ""),
		FromNumber: env("TWILIO_FROM_NUMBER", ""),
		APIBase:    env("TWILIO_API_BASE", ""),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// WantsTwilio reports whether the operator ASKED for the live provider (regardless of whether the
// credentials are actually complete). Kept separate from Complete() so a misconfiguration is
// distinguishable from "SMS is deliberately off".
func (s Settings) WantsTwilio() bool { return s.Provider == "twilio" }

// Complete reports whether every credential the Twilio client needs is present. It checks presence
// ONLY, it does not, and must not, log or echo any value.
func (s Settings) Complete() bool {
	return s.AccountSID != "" && s.AuthToken != "" && s.FromNumber != ""
}

// Select is the flag-flip seam: it returns the notifier the environment asks for.
//
//	stub / unset / anything else            → StubNotifier                    (nil error)
//	twilio + complete credentials           → *TwilioNotifier                 (nil error)
//	twilio + incomplete credentials         → StubNotifier, ErrTwilioNotConfigured
//
// The third case is deliberately NOT a silent fallback: the caller gets a working (stub) notifier so
// the server still boots, plus an error that says the operator's intent was not honoured.
func Select(s Settings, log *slog.Logger) (notify.Notifier, error) {
	if !s.WantsTwilio() {
		return NewStubNotifier(log), nil
	}
	if !s.Complete() {
		return NewStubNotifier(log), fmt.Errorf("%w (need TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_FROM_NUMBER)", ErrTwilioNotConfigured)
	}
	return NewTwilioNotifier(s, log), nil
}

// StubNotifier is the DEFAULT SMS transport: it has no carrier, so it records the message and writes
// it to the structured log, which IS the delivery channel in development, the same shape as
// notify.LogNotifier for email.
//
// Live() is false, which is what makes the boot check able to say out loud that texts are not
// actually going anywhere. It also RETAINS the messages it was handed (Sent()), because that is what
// lets an end-to-end test assert a notice really travelled the whole path, emit → claim → template →
// deliver, with no provider and no credentials.
type StubNotifier struct {
	log *slog.Logger
	mu  sync.Mutex
	// sent is bounded: a long-running server must not accumulate every text it ever pretended to send.
	sent []notify.Message
}

const stubSentCap = 200

// NewStubNotifier builds the default SMS transport.
func NewStubNotifier(log *slog.Logger) *StubNotifier { return &StubNotifier{log: log} }

func (n *StubNotifier) Name() string { return "sms-stub" }

// Live is false: nothing leaves the building. The boot path warns on this outside dev.
func (n *StubNotifier) Live() bool { return false }

// Send records the message and logs it. The body is logged in full because in stub mode the log IS
// the recipient's phone, and that is exactly why Live() is false and production must not run it.
func (n *StubNotifier) Send(_ context.Context, m notify.Message) error {
	n.mu.Lock()
	n.sent = append(n.sent, m)
	if len(n.sent) > stubSentCap {
		n.sent = n.sent[len(n.sent)-stubSentCap:]
	}
	n.mu.Unlock()
	if n.log != nil {
		n.log.Info("sms: deliver (stub, no carrier, nothing sent)",
			"channel", string(m.Channel), "to", m.To, "body", m.Body)
	}
	return nil
}

// Sent returns a copy of the recorded messages (newest last).
func (n *StubNotifier) Sent() []notify.Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notify.Message(nil), n.sent...)
}

// Reset clears the recorded messages, used by tests that assert on a clean slate.
func (n *StubNotifier) Reset() {
	n.mu.Lock()
	n.sent = nil
	n.mu.Unlock()
}
