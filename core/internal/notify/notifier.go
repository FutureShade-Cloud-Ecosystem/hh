// Package notify is the contractor-portal notification seam (F0.2): a transactional
// outbox dispatcher plus a provider-port Notifier that delivers email (and, in the same
// abstraction, SMS). It is the universal delivery dependency behind invite onboarding,
// password reset, and event notifications, everything that must reach a human out of band.
//
// The package imports no other internal module: handlers map a topic's payload to a
// notify.Message and live in the owning domain (e.g. internal/portal), registered against
// the Dispatcher at wire time. This keeps notify free of domain coupling, exactly as the
// payments PaymentGateway port stays free of invoice knowledge.
package notify

import (
	"context"
	"log/slog"
)

// Channel is the delivery medium. EMAIL is built now; SMS shares the same Notifier
// abstraction so a Twilio-style sender slots in without a second interface (plan §4.6).
type Channel string

const (
	EmailChannel Channel = "EMAIL"
	SMSChannel   Channel = "SMS"
)

// Message is one notification to deliver. Subject is ignored for SMS. To is an email
// address or an E.164 phone number depending on Channel. Body may contain a one-time
// link (invite/reset), see the LogNotifier note on why that token is dev-only at rest.
type Message struct {
	Channel Channel
	To      string
	Subject string
	Body    string
}

// Notifier is the delivery PORT. The deterministic LogNotifier is the dev default; a real
// SMTP/provider sender (and an SMS sender) slot in here by config later WITHOUT touching
// callers, mirroring payments.PaymentGateway. The boot path warns (non-dev) when the
// active notifier is not Live, so onboarding/reset mail is never silently swallowed in prod.
type Notifier interface {
	// Name identifies the active provider ("log", "smtp", …) for the boot check and audit.
	Name() string
	// Live reports whether this notifier actually delivers to a human inbox (false for the
	// dev log stub). A prod go-live must wire a Live notifier before onboarding email matters.
	Live() bool
	// Send delivers one message. An error is retried by the dispatcher (attempts→dead-letter);
	// it must therefore be safe to call again for the same logical message.
	Send(ctx context.Context, m Message) error
}

// LogNotifier is the DEV/stub transport: it has no inbox, so it writes the message, link
// included, to the structured log, which IS the delivery channel in development (the same
// pattern as a console "magic link"). It is NOT for production: Live() is false and main
// warns when it runs outside dev. A real provider must replace it before invite/reset mail
// goes to actual contractors. Because it logs the body (which can carry a one-time token),
// it must never be the active notifier in a non-dev environment.
type LogNotifier struct{ log *slog.Logger }

// NewLogNotifier builds the dev log transport.
func NewLogNotifier(log *slog.Logger) *LogNotifier { return &LogNotifier{log: log} }

func (n *LogNotifier) Name() string { return "log" }
func (n *LogNotifier) Live() bool   { return false }

// Send records the message to the log. Channel/recipient/subject at Info; the full body
// (the dev "inbox", including any link) at Info too, since in dev the log is the only way to
// retrieve the link. This is precisely why Live() is false and prod refuses this notifier.
func (n *LogNotifier) Send(_ context.Context, m Message) error {
	if n.log != nil {
		n.log.Info("notify: deliver (dev log stub, not a real send)",
			"channel", string(m.Channel), "to", m.To, "subject", m.Subject, "body", m.Body)
	}
	return nil
}
