package notify

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLogNotifier_Identity(t *testing.T) {
	n := NewLogNotifier(nil)
	if n.Name() != "log" {
		t.Errorf("Name() = %q, want log", n.Name())
	}
	if n.Live() {
		t.Error("Live() = true, want false for the dev stub")
	}
}

func TestLogNotifier_SendBothChannels(t *testing.T) {
	n := NewLogNotifier(nil) // nil logger must not panic
	for _, ch := range []Channel{EmailChannel, SMSChannel} {
		if err := n.Send(context.Background(), Message{Channel: ch, To: "a@b.test", Subject: "s", Body: "b"}); err != nil {
			t.Errorf("Send(%s) = %v, want nil", ch, err)
		}
	}
}

// HH-196: outside dev the log stub must never write a message body, link,
// token or recipient; in dev (bodies enabled) the body stays visible.
func TestLogNotifier_NonDevLogsNoBodyOrToken(t *testing.T) {
	const token = "RAWTOKEN-hh196-secret"
	m := Message{Channel: EmailChannel, To: "who@example.test", Subject: "Reset",
		Body: "Choose a new password:\nhttps://portal.example/?reset_token=" + token}

	var prod bytes.Buffer
	n := NewLogNotifier(slog.New(slog.NewTextHandler(&prod, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err := n.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{token, "reset_token", "who@example.test", "Choose a new password"} {
		if strings.Contains(prod.String(), leak) {
			t.Fatalf("non-dev log output leaks %q: %s", leak, prod.String())
		}
	}

	var dev bytes.Buffer
	d := NewLogNotifier(slog.New(slog.NewTextHandler(&dev, nil))).WithBodies(true)
	if err := d.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dev.String(), token) {
		t.Fatalf("dev log output should carry the link for the developer: %s", dev.String())
	}
}
