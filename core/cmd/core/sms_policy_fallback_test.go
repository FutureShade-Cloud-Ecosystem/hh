package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestBuildSmsBelt_WarnsOncePerPolicyFallback, HH-202: SMS_RATE_PER_MIN=0 is refused (the cap stays
// at the default) and the boot path must say so, one warning per fallen-back variable, naming the
// variable, the value read and the default applied. A clean environment logs no such warning.
func TestBuildSmsBelt_WarnsOncePerPolicyFallback(t *testing.T) {
	t.Setenv("SMS_PROVIDER", "stub")
	t.Setenv("SMS_WINDOW_ZONE", "")
	t.Setenv("SMS_WINDOW_START_MIN", "")
	t.Setenv("SMS_WINDOW_END_MIN", "")

	run := func() []string {
		var buf bytes.Buffer
		buildSmsBelt(nil, slog.New(slog.NewTextHandler(&buf, nil)))
		var out []string
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "policy variable fell back") {
				out = append(out, l)
			}
		}
		return out
	}

	t.Setenv("SMS_RATE_PER_MIN", "")
	if got := run(); len(got) != 0 {
		t.Fatalf("clean environment logged fallback warnings: %v", got)
	}

	t.Setenv("SMS_RATE_PER_MIN", "0")
	got := run()
	if len(got) != 1 {
		t.Fatalf("SMS_RATE_PER_MIN=0 logged %d fallback warnings, want 1: %v", len(got), got)
	}
	for _, frag := range []string{"SMS_RATE_PER_MIN", "value=0", "default=30", "level=WARN"} {
		if !strings.Contains(got[0], frag) {
			t.Fatalf("warning %q lacks %q", got[0], frag)
		}
	}

	t.Setenv("SMS_RATE_PER_MIN", "abc")
	t.Setenv("SMS_WINDOW_START_MIN", "-5")
	if got := run(); len(got) != 2 {
		t.Fatalf("two bad variables logged %d fallback warnings, want 2: %v", len(got), got)
	}
}
