package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// - scopeBrief #6 (0072): Contractor-Portal → Chat inbound bridge -
//
// The portal emits a crm.NewPortalContact outbox event when a contractor activates. That topic is
// OWNED (drained with a log line, D-026) by portal.RegisterNotificationHandlers, it has no real
// consumer yet. This bridge is that consumer: it OBSERVES the topic on the shared dispatcher and
// relays each new contact into the seeded #portal-inbox channel (0072) as a system EVENT message.
//
// Why an OBSERVER, not a second owner: the dispatcher enforces one owner per topic and PANICS on a
// duplicate Register (notify/dispatcher.go). The portal already owns crm.NewPortalContact, so a second
// Register would fail at boot. RegisterObserver is the sanctioned "thin relay" seam, a non-owning
// side-effect that runs after the owner succeeds and can NEVER fail/retry/dead-letter the owner's row
// (a messaging blip must not disturb the CRM signal). This is the SAME pattern the FR-3 consumer uses
// for the raw domain topics (consumer.go), and it is why messaging does NOT import the portal's
// repository: it reacts only to the topic + payload on the bus.
//
// GATED (do NOT build, see the 0072 header): rep-routing to a rep's DM (needs rep_assignments, which
// does not exist), the other two portal topics (portal.RFQSubmitted / portal.PaymentReceived), and the
// contractor-visible auto-reply text (G-CLIENT). v1 is strictly inbound → #portal-inbox, one topic.

// RawNewPortalContact is the portal outbox topic this bridge observes. Declared locally (a bare string,
// matching how consumer.go names the raw domain topics it observes) so messaging takes NO dependency on
// the portal package, it couples only to the wire topic name, exactly like the payment.received relay.
const RawNewPortalContact = "crm.NewPortalContact"

// portalInboxSlug is the seeded TOPIC channel new portal contacts land in (0072).
const portalInboxSlug = "portal-inbox"

// portalContactPayload mirrors the fields the portal marshals onto a crm.NewPortalContact row
// (portal.NewPortalContact). Redeclared here, not imported, so the bridge decodes the bus payload
// without a cross-module dependency (every field optional; the body degrades gracefully).
type portalContactPayload struct {
	CustomerID string `json:"customer_id"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Role       string `json:"role"`
}

// PortalBridge relays crm.NewPortalContact events into #portal-inbox. It holds only the messaging
// service (for the idempotent EVENT write + slug routing) and a logger, no portal internals.
type PortalBridge struct {
	svc *Service
	log *slog.Logger
}

// NewPortalBridge builds the inbound bridge.
func NewPortalBridge(svc *Service, log *slog.Logger) *PortalBridge {
	if log == nil {
		log = slog.Default()
	}
	return &PortalBridge{svc: svc, log: log}
}

// RegisterHandlers attaches the bridge as a NON-OWNING observer of crm.NewPortalContact. Wire it once
// at boot AFTER portal.RegisterNotificationHandlers, so the topic already has its primary owner (the
// dispatcher panics at Run if an observer has none, notify/dispatcher.go validateObservers).
func (b *PortalBridge) RegisterHandlers(d *notify.Dispatcher) {
	d.RegisterObserver(RawNewPortalContact, b.handle)
}

// handle relays one crm.NewPortalContact event to #portal-inbox as a system EVENT message, idempotent
// on the outbox row id (the (channel, event_key) unique index dedups a redelivery). A malformed payload
// is a logged no-op (nil), it will never parse on retry, and as an observer an error would only be
// logged-and-swallowed anyway. A missing seed channel returns an error so the dispatcher logs it (the
// channel is seeded in 0072, so this is a setup problem, not an event problem). requires_ack is FALSE:
// #portal-inbox is a low-priority house queue, not a screen-takeover alert.
func (b *PortalBridge) handle(ctx context.Context, e notify.Event) error {
	var p portalContactPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		b.log.Warn("messaging: undecodable portal-contact event, skipping", "event", e.EventID, "err", err)
		return nil
	}
	body, ref := renderPortalContact(p)
	_, inserted, err := b.svc.PostEventToChannelSlug(ctx, portalInboxSlug, e.EventID, body, ref, false)
	if err != nil {
		return fmt.Errorf("relay portal contact to #%s: %w", portalInboxSlug, err)
	}
	if inserted {
		b.log.Debug("messaging: portal contact relayed", "channel", portalInboxSlug, "event", e.EventID)
	}
	return nil
}

// renderPortalContact builds the EVENT body + the {topic, entity, deep_link, summary} ref (MSG-01's
// existing EventRef schema). Pure (no ctx/DB) so the mapping is unit-testable in isolation. deep_link
// is intentionally empty, there is no staff deep link for a raw portal contact yet (the field exists
// in the schema; it is simply unset).
func renderPortalContact(p portalContactPayload) (string, EventRef) {
	who := p.Name
	if who == "" {
		who = p.Email
	}
	if who == "" {
		who = "(unknown contact)"
	}
	body := "New contractor-portal contact, " + who
	if p.Email != "" && p.Email != who {
		body += " <" + p.Email + ">"
	}
	if p.Role != "" {
		body += " · " + p.Role
	}
	body += "."
	ref := EventRef{Topic: RawNewPortalContact, Summary: body}
	if p.CustomerID != "" {
		ref.Entity = "customer:" + p.CustomerID
	}
	return body, ref
}
