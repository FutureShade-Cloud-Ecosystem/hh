package readtx

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
)

const canary = "CANARY-key-material-0123456789abcdef0123456789"

// Every type in this package that holds a redact.Secret, by value and by pointer, in
// every verb: the table fails on a new holder that prints the value.
func TestEveryHolderInThepackageFormatsWithoutTheSecret(t *testing.T) {
	s := redact.NewSecret(canary)
	sg, err := NewSigner(redact.NewSecret(canary + "-padding-to-length"))
	if err != nil {
		t.Fatal(err)
	}
	holders := map[string]any{
		"redact.Secret": s,
		"Config":        Config{ReaderURL: s, ProReaderURL: s, ClaimsKey: s},
		"Signer":        *sg,
		"Reader":        Reader{audience: AudienceStaff, role: "r", signer: sg},
		"Readers":       Readers{Staff: &Reader{signer: sg}},
		"anon unexported": struct {
			k redact.Secret
			p *redact.Secret
		}{s, &s},
		"slice": []redact.Secret{s},
		"map":   map[string]redact.Secret{"k": s},
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
	if redact.NewSecret("").String() != "[secret unset]" || s.String() != "[secret set]" {
		t.Error("String must say only set or unset")
	}
}
