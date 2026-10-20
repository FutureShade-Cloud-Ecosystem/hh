package readtx

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

// Every type in this package that holds a Secret, by value and by pointer, in
// every verb: the table fails on a new holder that prints the value.
func TestEveryHolderInThepackageFormatsWithoutTheSecret(t *testing.T) {
	s := NewSecret(canary)
	sg, err := NewSigner(NewSecret(canary + "-padding-to-length"))
	if err != nil {
		t.Fatal(err)
	}
	holders := map[string]any{
		"Secret":  s,
		"Config":  Config{ReaderURL: s, ProReaderURL: s, ClaimsKey: s},
		"Signer":  *sg,
		"Reader":  Reader{audience: AudienceStaff, role: "r", signer: sg},
		"Readers": Readers{Staff: &Reader{signer: sg}},
		"anon unexported": struct {
			k Secret
			p *Secret
		}{s, &s},
		"slice": []Secret{s},
		"map":   map[string]Secret{"k": s},
	}
	for name, h := range holders {
		ptr := &h
		for _, v := range []any{h, ptr, &holders} {
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
				out := fmt.Sprintf(verb, v)
				if strings.Contains(out, "CANARY") || strings.Contains(out, "-padding-to-length") {
					t.Errorf("%s under %s leaked: %q", name, verb, out)
				}
			}
		}
		if b, err := json.Marshal(h); err == nil && strings.Contains(string(b), "CANARY") {
			t.Errorf("%s JSON leaked", name)
		}
	}
	if NewSecret("").String() != "[secret unset]" || s.String() != "[secret set]" {
		t.Error("String must say only set or unset")
	}
}
