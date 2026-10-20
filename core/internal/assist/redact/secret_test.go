package redact

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const canary = "CANARY-key-material-0123456789abcdef0123456789"

type holder struct {
	Name string
	Key  Secret
	ptr  *Secret
}

// Every way Go can render a value must leave the secret out: the verbs, a
// struct that holds one by value or by pointer or unexported, an error that
// wraps one, JSON, and a structured log line.
func TestSecretNeverPrints(t *testing.T) {
	s := NewSecret(canary)
	h := holder{Name: "h", Key: s, ptr: &s}
	var out []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%T"} {
		out = append(out,
			fmt.Sprintf(verb, s), fmt.Sprintf(verb, &s), fmt.Sprintf(verb, h), fmt.Sprintf(verb, &h),
			fmt.Sprintf(verb, []Secret{s}), fmt.Sprintf(verb, map[string]Secret{"k": s}))
	}
	out = append(out, fmt.Errorf("wrapped: %w", errors.New(fmt.Sprint(s))).Error(), fmt.Sprint(s), s.String(), s.GoString())
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, string(b))
	tb, _ := s.MarshalText()
	out = append(out, string(tb))
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("x", "key", s, "holder", h)
	slog.New(slog.NewJSONHandler(&sb, nil)).Info("x", "key", s, "holder", h)
	out = append(out, sb.String())
	for i, line := range out {
		if strings.Contains(line, canary) || strings.Contains(line, "CANARY") {
			t.Errorf("rendering %d leaked the secret: %q", i, line)
		}
	}
}

func TestSecretRevealReturnsTheValueAndEmptyIsEmpty(t *testing.T) {
	if got := NewSecret(canary).Reveal(); got != canary {
		t.Fatalf("Reveal = %q", got)
	}
	var zero Secret
	if !zero.IsEmpty() || NewSecret("").IsEmpty() == false || NewSecret("x").IsEmpty() {
		t.Fatal("IsEmpty wrong")
	}
}
