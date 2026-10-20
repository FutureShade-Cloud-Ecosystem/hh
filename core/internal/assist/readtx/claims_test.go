package readtx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testKeyMaterial = "0123456789abcdef0123456789abcdef-test-only"

func fixedSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner(redact.NewSecret(testKeyMaterial))
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return s
}

func TestSignProducesTheWireFormatTheVerifierReads(t *testing.T) {
	s := fixedSigner(t)
	tok, err := s.Sign(Claims{Audience: "staff", AskerID: "u1", BranchIDs: []string{"b1"}}, 4242)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok.Reveal(), ".")
	if len(parts) != 4 || parts[0] != "v1" {
		t.Fatalf("want v1.<id>.<payload>.<mac>, got %d parts", len(parts))
	}
	if parts[1] != strconv.Itoa(int(KeyID(redact.NewSecret(testKeyMaterial)))) {
		t.Errorf("key id %s is not the derived id", parts[1])
	}
	m := hmac.New(sha256.New, []byte(testKeyMaterial))
	m.Write([]byte(strings.Join(parts[:3], ".")))
	if parts[3] != hex.EncodeToString(m.Sum(nil)) {
		t.Error("the MAC does not cover everything before its dot")
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["pid"].(float64) != 4242 || got["exp"].(float64) != 1_700_000_000+float64(DefaultClaimsTTL/time.Second) {
		t.Errorf("pid or expiry wrong: %v %v", got["pid"], got["exp"])
	}
	if got["nonce"] == "" || got["nonce"] == nil {
		t.Error("no nonce")
	}
	// Absent lists are empty arrays, never null, so an array comparison in a policy sees an empty set.
	for _, k := range []string{"branch_ids", "cost_branch_ids", "pii_classes", "capabilities", "channel_ids"} {
		if _, ok := got[k].([]any); !ok {
			t.Errorf("%s = %v, want an array", k, got[k])
		}
	}
}

func TestSignRefusesWhatTheVerifierWouldRefuse(t *testing.T) {
	s := fixedSigner(t)
	for name, c := range map[string]Claims{
		"no audience":  {AskerID: "u"},
		"bad audience": {Audience: "admin", AskerID: "u"},
		"no asker":     {Audience: "staff"},
	} {
		if _, err := s.Sign(c, 1); err == nil {
			t.Errorf("%s was signed", name)
		}
	}
	if _, err := s.Sign(Claims{Audience: "staff", AskerID: "u"}, 0); err == nil {
		t.Error("a zero pid was signed")
	}
}

func TestNewSignerRefusesAShortOrEmptyKey(t *testing.T) {
	for _, k := range []string{"", "short"} {
		if _, err := NewSigner(redact.NewSecret(k)); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
}

func TestSignedClaimsNeverPrint(t *testing.T) {
	tok, _ := fixedSigner(t).Sign(Claims{Audience: "staff", AskerID: "u"}, 7)
	if strings.Contains(fmt.Sprintf("%v %s %+v %#v", tok, tok, tok, tok), "v1.") {
		t.Error("a signed claim printed")
	}
}

func TestKeyIDIsStableAndDependsOnTheKey(t *testing.T) {
	a, b := KeyID(redact.NewSecret(testKeyMaterial)), KeyID(redact.NewSecret(testKeyMaterial+"x"))
	if a == b || a < 0 || a != KeyID(redact.NewSecret(testKeyMaterial)) {
		t.Fatalf("ids %d %d", a, b)
	}
}
