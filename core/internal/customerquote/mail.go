package customerquote

// The delivery seam of the send (D34, MAIN docs/analysis/DECISIONS.md:195):
// all HH product messaging, email, text, push and campaigns, sends through
// Messaging in FB Console behind the bought relays; the core holds no mail
// server and no provider accounts of its own. This file is that rule's local
// shape. The send hands the minted link to the Mailer seam exactly once per
// committed send, after the transaction; the default implementation is the
// no-provider stub, which delivers nothing because that is D34's state of
// the world for this core, not a missing collaborator; and the send
// response carries the raw token to the desk, the dealer mint's posture
// (the operator receives the link and pastes it into whatever channel the
// desk uses). A live provider behind this seam is the FB Console Messaging
// integration's to land, never a provider client inside this module.

import (
	"context"
	"time"
)

// SentLink is the send event: everything the delivery half needs about one
// send and nothing else. The raw token appears here and in the send
// response, and nowhere else, ever.
type SentLink struct {
	QuoteID      string
	OrgID        string
	TenantID     string
	CustomerName string
	Token        string // the raw magic-link token, this one handoff only
	ValidUntil   *time.Time
	SentAt       time.Time
}

// Mailer is the quote delivery seam (D34). The call happens after the send
// transaction has committed, once per send, on the request's context; the
// send itself has already succeeded, so the call can never misstate it.
type Mailer interface {
	SendQuoteLink(ctx context.Context, link SentLink) error
}

// NoProviderMailer is the seam's default: the no-provider stub. D34 rules
// the core holds no mail server and no provider accounts, so delivering
// nothing is the honest production behaviour today, not a degraded mode:
// the desk is the delivery path, through the raw token in the send
// response.
type NoProviderMailer struct{}

// SendQuoteLink records the send event and delivers nothing (D34).
func (NoProviderMailer) SendQuoteLink(context.Context, SentLink) error { return nil }
