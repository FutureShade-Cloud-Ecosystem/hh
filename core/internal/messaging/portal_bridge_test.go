package messaging

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// The observed topic must be a RAW domain topic (owned elsewhere), never our own msg.event.* family -
// the bridge is a non-owning observer, exactly like the FR-3 raw-topic relays.
func TestPortalBridge_ObservesRawDomainTopic(t *testing.T) {
	if RawNewPortalContact != "crm.NewPortalContact" {
		t.Fatalf("bridge must observe the portal's exact topic, got %q", RawNewPortalContact)
	}
	if strings.HasPrefix(RawNewPortalContact, "msg.event.") {
		t.Fatal("the observed topic must be a raw domain topic, not messaging's own family")
	}
}

func portalEvent(id, payload string) notify.Event {
	return notify.Event{EventID: id, Topic: RawNewPortalContact, Payload: []byte(payload)}
}

// The core Feature-2 contract: given a crm.NewPortalContact event, the bridge creates EXACTLY ONE
// EVENT message in #portal-inbox with the right ref (topic + customer entity + a summary body).
func TestPortalBridge_PostsOneEventToPortalInbox(t *testing.T) {
	f := newFakeStore()
	f.slugs[portalInboxSlug] = "ch-portal-inbox"
	b := NewPortalBridge(svcWith(f), nil)

	ev := portalEvent("ob-1", `{"customer_id":"c-9","user_id":"u-1","name":"Quinte Hardscapes","email":"ops@quinte.example","role":"OWNER"}`)
	if err := b.handle(context.Background(), ev); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if len(f.events) != 1 {
		t.Fatalf("expected exactly one EVENT message, got %d", len(f.events))
	}
	m := f.events[0]
	if m.ChannelID != "ch-portal-inbox" {
		t.Fatalf("portal contact must land in #portal-inbox, got channel %q", m.ChannelID)
	}
	if m.Kind != MsgEvent {
		t.Fatalf("relayed message must be a system EVENT, got kind %q", m.Kind)
	}
	if !strings.Contains(m.Body, "Quinte Hardscapes") || !strings.Contains(m.Body, "OWNER") {
		t.Fatalf("body should name the contact + role, got %q", m.Body)
	}
	if m.RequiresAck {
		t.Fatal("the #portal-inbox house queue must NOT be a forced-ack (screen-takeover) post")
	}
	ref := f.eventRefs[0]
	if ref.Topic != RawNewPortalContact {
		t.Fatalf("ref.topic want %q, got %q", RawNewPortalContact, ref.Topic)
	}
	if ref.Entity != "customer:c-9" {
		t.Fatalf("ref.entity should point at the customer, got %q", ref.Entity)
	}
	if ref.Summary == "" {
		t.Fatal("ref.summary should mirror the body")
	}
}

// Redelivery of the same outbox row must post at most once (idempotent on the outbox id, via the
// (channel, event_key) unique index the fake models with eventDup).
func TestPortalBridge_IdempotentOnOutboxID(t *testing.T) {
	f := newFakeStore()
	f.slugs[portalInboxSlug] = "ch-portal-inbox"
	b := NewPortalBridge(svcWith(f), nil)
	ev := portalEvent("ob-dup", `{"customer_id":"c-1","name":"Acme","email":"a@acme.example"}`)
	_ = b.handle(context.Background(), ev)
	_ = b.handle(context.Background(), ev)
	if len(f.events) != 1 {
		t.Fatalf("a redelivered portal event must post at most once, got %d", len(f.events))
	}
}

// A malformed payload is a logged no-op (nil), it will never parse on retry, and as an observer an
// error would only be swallowed anyway. Nothing must be posted.
func TestPortalBridge_BadPayloadNoPost(t *testing.T) {
	f := newFakeStore()
	f.slugs[portalInboxSlug] = "ch-portal-inbox"
	b := NewPortalBridge(svcWith(f), nil)
	if err := b.handle(context.Background(), portalEvent("x", "not json")); err != nil {
		t.Fatalf("undecodable payload should be dropped (nil), got %v", err)
	}
	if len(f.events) != 0 {
		t.Fatal("nothing should be posted for a malformed payload")
	}
}

// A minimal payload (no name) still relays, falling back to the email (then a placeholder) so the
// house queue never shows an empty contact line.
func TestPortalBridge_FallsBackToEmail(t *testing.T) {
	f := newFakeStore()
	f.slugs[portalInboxSlug] = "ch-portal-inbox"
	b := NewPortalBridge(svcWith(f), nil)
	if err := b.handle(context.Background(), portalEvent("ob-2", `{"customer_id":"c-2","email":"solo@builder.example"}`)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(f.events) != 1 || !strings.Contains(f.events[0].Body, "solo@builder.example") {
		t.Fatalf("no-name contact should fall back to the email, got %+v", f.events)
	}
}

// A missing seed channel surfaces an error so the dispatcher logs it (a setup problem, the channel is
// seeded in 0072). The bridge does not silently swallow a routing miss.
func TestPortalBridge_MissingChannelErrors(t *testing.T) {
	f := newFakeStore() // no portal-inbox slug seeded
	b := NewPortalBridge(svcWith(f), nil)
	if err := b.handle(context.Background(), portalEvent("ob-3", `{"customer_id":"c-3","name":"Nobody"}`)); err == nil {
		t.Fatal("a missing #portal-inbox channel should surface an error")
	}
}
