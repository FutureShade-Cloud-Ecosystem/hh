package payment

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// last_error is diagnostics only: whatever a processor or a transport put in an error string, no
// token, credential or card number survives into the attempt row.
func TestSanitizeLastError_RedactsSecrets(t *testing.T) {
	cases := []struct {
		name, in string
		gone     []string
	}{
		{"spaced PAN", "card 4111 1111 1111 1111 declined", []string{"4111", "1111"}},
		{"plain PAN", "bad pan 4111111111111111 here", []string{"4111111111111111"}},
		{"hyphen PAN", "pan=4111-1111-1111-1111", []string{"4111-1111"}},
		{"json token", `{"token":"abc123secretvalue","msg":"no"}`, []string{"abc123secretvalue"}},
		{"bearer", "Authorization: Bearer eyJhbGciOi.payload.sig failed", []string{"eyJhbGciOi", "payload.sig"}},
		{"api key", "request failed api_key=sk_live_1234567890 retry", []string{"sk_live_1234567890"}},
		{"password", "password: hunter2 rejected", []string{"hunter2"}},
		{"account token", "account_token=tok_9f8e7d6c5b4a rejected", []string{"tok_9f8e7d6c5b4a"}},
		{"opaque hex", "echo 0123456789abcdef0123456789abcdef0123456789abcdef", []string{"0123456789abcdef0123456789abcdef"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SanitizeLastError(c.in)
			for _, g := range c.gone {
				if strings.Contains(got, g) {
					t.Fatalf("SanitizeLastError(%q) = %q, still holds %q", c.in, got, g)
				}
			}
			if got == "" {
				t.Fatalf("SanitizeLastError(%q) erased the whole diagnostic", c.in)
			}
		})
	}
}

func TestSanitizeLastError_KeepsPlainDiagnostics(t *testing.T) {
	for _, in := range []string{
		"run payments: HTTP 502 from processor",
		"context deadline exceeded",
		"attempt 5d1b3f6e-0000-4000-9000-000000000001 timed out after 25s",
		"amount 12500 cents",
	} {
		if got := SanitizeLastError(in); got != in {
			t.Errorf("SanitizeLastError(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestSanitizeLastError_BoundsAndFlattens(t *testing.T) {
	got := SanitizeLastError(strings.Repeat("eé ", 400))
	if n := utf8.RuneCountInString(got); n > maxLastErrorRunes {
		t.Fatalf("kept %d runes, cap is %d", n, maxLastErrorRunes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncation split a rune: %q", got)
	}
	if flat := SanitizeLastError("line one\nline two\r\n\tline three"); strings.ContainsAny(flat, "\r\n\t") {
		t.Fatalf("control characters survived: %q", flat)
	}
	if SanitizeLastError("") != "" {
		t.Fatal("empty in, empty out")
	}
}
