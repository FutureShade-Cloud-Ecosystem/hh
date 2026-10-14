package portal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/dealer"
)

func sendInviteAndReset(t *testing.T) (invite, reset notify.Message) {
	t.Helper()
	rn := &recordingNotifier{}
	exp := time.Now().Add(24 * time.Hour)
	store := &oneShotStore{events: []notify.Event{
		{EventID: "1", Topic: TopicInvite, Payload: mustJSON(t, InviteNotice{Email: "a@co.test", Role: "buyer", Token: "T1", ExpiresAt: exp})},
		{EventID: "2", Topic: TopicPasswordReset, Payload: mustJSON(t, PasswordResetNotice{Email: "a@co.test", Token: "T2", ExpiresAt: exp})},
	}}
	d := notify.NewDispatcher(store, nil)
	RegisterNotificationHandlers(d, rn, "https://pro.example.test/portal.html", nil)
	d.DrainOnce(context.Background())
	if len(rn.sent) != 2 {
		t.Fatalf("sent %d emails, want 2", len(rn.sent))
	}
	return rn.sent[0], rn.sent[1]
}

// The invite and reset emails read the dealer's own name from its configuration.
func TestNotificationEmails_ReadTheConfiguredDealerName(t *testing.T) {
	t.Cleanup(func() { dealer.Set("") })
	dealer.Set("Gable Landscape Supply")
	invite, reset := sendInviteAndReset(t)
	for _, text := range []string{invite.Subject, invite.Body, reset.Subject} {
		if !strings.Contains(text, "Gable Landscape Supply") {
			t.Errorf("text %q does not carry the configured dealer name", text)
		}
	}
	if strings.Contains(strings.ToLower(invite.Subject+invite.Body+reset.Subject+reset.Body), "dibbits") {
		t.Error("an email still carries a dealer's name from code")
	}
}

// With no dealer name set, the emails use a neutral role word and never another dealer's name.
func TestNotificationEmails_NeutralWhenNoDealerName(t *testing.T) {
	dealer.Set("")
	invite, reset := sendInviteAndReset(t)
	all := invite.Subject + "|" + invite.Body + "|" + reset.Subject + "|" + reset.Body
	if strings.Contains(strings.ToLower(all), "dibbits") {
		t.Errorf("a dealer's name leaked from code: %q", all)
	}
	if !strings.Contains(invite.Subject, "your dealer's contractor portal") {
		t.Errorf("invite subject = %q, want the neutral phrase", invite.Subject)
	}
	if reset.Subject != "Reset your contractor portal password" {
		t.Errorf("reset subject = %q, want the neutral phrase", reset.Subject)
	}
}
