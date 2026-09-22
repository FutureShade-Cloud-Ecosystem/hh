package notify

import (
	"context"
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
