package readtx

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"strings"
	"testing"
)

func TestScramVerifierShapeAndSecrecy(t *testing.T) {
	pw := "correct horse battery staple 01"
	v, err := scramVerifier(redact.NewSecret(pw), []byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "SCRAM-SHA-256$4096:MDEyMzQ1Njc4OWFiY2RlZg==$") || strings.Count(v, ":") != 2 || strings.Count(v, "$") != 2 {
		t.Errorf("verifier shape: %q", v)
	}
	if strings.Contains(v, pw) {
		t.Error("the verifier contains the password")
	}
	v2, _ := scramVerifier(redact.NewSecret(pw), []byte("0123456789abcdef"))
	if v != v2 {
		t.Error("same password and salt must give the same verifier")
	}
}

func TestPasswordRules(t *testing.T) {
	for name, pw := range map[string]string{
		"empty":     "",
		"short":     "abc123",
		"non ascii": "passwörd-passwörd-passwörd",
		"control":   "abcdefghijklmnop\nqrst",
	} {
		if _, err := scramVerifier(redact.NewSecret(pw), []byte("0123456789abcdef")); err == nil {
			t.Errorf("%s password accepted", name)
		}
	}
}
