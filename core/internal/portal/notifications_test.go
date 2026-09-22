package portal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// recordingNotifier captures delivered messages for assertions.
type recordingNotifier struct{ sent []notify.Message }

func (r *recordingNotifier) Name() string { return "recording" }
func (r *recordingNotifier) Live() bool   { return true }
func (r *recordingNotifier) Send(_ context.Context, m notify.Message) error {
	r.sent = append(r.sent, m)
	return nil
}

// oneShotStore drives a fixed set of events through settle once, recording each settlement.
type oneShotStore struct {
	events  []notify.Event
	settled []notify.Settlement
}

func (s *oneShotStore) ProcessBatch(ctx context.Context, n int, settle func(context.Context, notify.Event) notify.Settlement) (int, error) {
	c := 0
	for c < n && len(s.events) > 0 {
		e := s.events[0]
		s.events = s.events[1:]
		s.settled = append(s.settled, settle(ctx, e))
		c++
	}
	return c, nil
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNotificationHandlers_DeliverAndRedact(t *testing.T) {
	rn := &recordingNotifier{}
	exp := time.Now().Add(24 * time.Hour)
	store := &oneShotStore{events: []notify.Event{
		{EventID: "1", Topic: TopicInvite, Payload: mustJSON(t, InviteNotice{
			Email: "crew@co.test", Role: "field_crew", Token: "INV-TOK", ExpiresAt: exp})},
		{EventID: "2", Topic: TopicPasswordReset, Payload: mustJSON(t, PasswordResetNotice{
			Email: "boss@co.test", Token: "RST-TOK", ExpiresAt: exp})},
		{EventID: "3", Topic: TopicNewPortalContact, Payload: mustJSON(t, NewPortalContact{
			CustomerID: "c1", UserID: "u1", Email: "new@co.test", Role: "buyer"})},
	}}

	d := notify.NewDispatcher(store, nil)
	RegisterNotificationHandlers(d, rn, "https://pro.dibbits.ca/portal.html", nil)
	d.DrainOnce(context.Background())

	// Invite + reset send an email; the new-contact signal drains silently (no consumer yet).
	if len(rn.sent) != 2 {
		t.Fatalf("sent %d emails, want 2 (invite + reset)", len(rn.sent))
	}

	invite := rn.sent[0]
	if invite.Channel != notify.EmailChannel || invite.To != "crew@co.test" {
		t.Errorf("invite mail = %+v, want EMAIL to crew@co.test", invite)
	}
	if !strings.Contains(invite.Body, "token=INV-TOK") {
		t.Errorf("invite body missing accept link with token: %q", invite.Body)
	}

	reset := rn.sent[1]
	if reset.To != "boss@co.test" {
		t.Errorf("reset mail To = %q, want boss@co.test", reset.To)
	}
	if !strings.Contains(reset.Body, "reset_token=RST-TOK") {
		t.Errorf("reset body missing reset link with token: %q", reset.Body)
	}

	// Both token-bearing topics must be redacted at rest on publish; the contact signal must not.
	if !store.settled[0].Redact {
		t.Error("invite settlement should redact the token at rest")
	}
	if !store.settled[1].Redact {
		t.Error("reset settlement should redact the token at rest")
	}
	if store.settled[2].Redact {
		t.Error("new-contact settlement should NOT be redacted")
	}
}
