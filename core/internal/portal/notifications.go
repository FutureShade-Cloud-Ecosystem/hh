package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

// Outbox topics the portal emits for out-of-band delivery (F0.2). Both invite and reset
// carry a one-time token, so they are registered as SECRET handlers, the dispatcher redacts
// the stored payload once the link has been delivered.
const (
	TopicInvite        = "portal.invite"
	TopicPasswordReset = "portal.password_reset"
)

// InviteNotice is the outbox payload for an invite email: who to mail, the role being
// offered, the raw accept token (redacted at rest after delivery), and the expiry.
type InviteNotice struct {
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PasswordResetNotice is the outbox payload for a reset email. The token is redacted at rest
// after delivery; it is the only place the raw reset token ever appears (it is never echoed
// in an API response).
type PasswordResetNotice struct {
	Email     string    `json:"email"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RegisterNotificationHandlers wires the portal's outbox topics to the notifier. Invite and
// reset are SECRET (their token is redacted at rest on publish); crm.NewPortalContact has no
// external consumer yet (D-026), so it is drained with a log line so PENDING rows do not
// accumulate. Call this once at wire time, before the dispatcher runs.
//
// baseURL is the contractor portal entry the email links point at (config.PortalBaseURL):
// the shell reads ?token= → accept-invite and ?reset_token= → reset-confirm.
func RegisterNotificationHandlers(d *notify.Dispatcher, n notify.Notifier, baseURL string, log *slog.Logger) {
	d.RegisterSecret(TopicInvite, func(ctx context.Context, e notify.Event) error {
		var p InviteNotice
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("decode invite notice: %w", err)
		}
		link := buildLink(baseURL, "token", p.Token)
		return n.Send(ctx, notify.Message{
			Channel: notify.EmailChannel,
			To:      p.Email,
			Subject: "You're invited to " + portalPhrase(),
			Body: fmt.Sprintf("You've been invited to %s as %s.\n\n"+
				"Set your password and activate your account:\n%s\n\n"+
				"This link expires %s.", portalPhrase(), p.Role, link, p.ExpiresAt.Format(time.RFC1123)),
		})
	})

	d.RegisterSecret(TopicPasswordReset, func(ctx context.Context, e notify.Event) error {
		var p PasswordResetNotice
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("decode reset notice: %w", err)
		}
		link := buildLink(baseURL, "reset_token", p.Token)
		return n.Send(ctx, notify.Message{
			Channel: notify.EmailChannel,
			To:      p.Email,
			Subject: "Reset your " + portalLabel() + " password",
			Body: fmt.Sprintf("A password reset was requested for your account.\n\n"+
				"Choose a new password:\n%s\n\n"+
				"This link expires %s. If you didn't request this, ignore this email.",
				link, p.ExpiresAt.Format(time.RFC1123)),
		})
	})

	d.Register(TopicNewPortalContact, func(_ context.Context, e notify.Event) error {
		// No external CRM consumer yet (D-026): record the new contact so the row drains and
		// the signal is at least observable until a consumer is built.
		var p NewPortalContact
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("decode new-contact: %w", err)
		}
		if log != nil {
			log.Info("portal: new contact activated", "customer_id", p.CustomerID, "email", p.Email, "role", p.Role)
		}
		return nil
	})
}

// portalLabel names the portal as a person reads it: the dealer's own configured name, or a neutral
// word when none is set. It is read at send time, never fixed in code (no dealer's name in code).
func portalLabel() string {
	if n := dealer.Name(); n != "" {
		return n + " Contractor Portal"
	}
	return "contractor portal"
}

// portalPhrase is portalLabel with its article, for "invited to ...".
func portalPhrase() string {
	if n := dealer.Name(); n != "" {
		return "the " + n + " Contractor Portal"
	}
	return "your dealer's contractor portal"
}

// buildLink appends a single query parameter to the portal base URL, encoding correctly even
// when the base already carries a path or query. On a malformed base it falls back to the
// base unchanged (the dev log stub still surfaces the raw token in the body for recovery).
func buildLink(base, param, token string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set(param, token)
	u.RawQuery = q.Encode()
	return u.String()
}
