package messaging

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestStreamTicket_MintVerifyAndReplay pins the happy path (a minted ticket verifies back to the bound
// user) and single-use (a replay of the same ticket is rejected).
func TestStreamTicket_MintVerifyAndReplay(t *testing.T) {
	ta := newTicketAuth("stream-secret-key")
	tok, err := ta.Mint("u-alice")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	uid, err := ta.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if uid != "u-alice" {
		t.Fatalf("ticket must verify back to the bound user, got %q", uid)
	}
	// Single-use: a second verify of the SAME ticket is a replay → rejected.
	if _, err := ta.Verify(tok); !errors.Is(err, errTicketReused) {
		t.Fatalf("replay should be rejected as reused, got %v", err)
	}
}

// TestStreamTicket_UserBindingTamperFails pins that the user-binding cannot be tampered: a payload rewritten
// to claim another user, kept with the original signature, fails HMAC verification.
func TestStreamTicket_UserBindingTamperFails(t *testing.T) {
	ta := newTicketAuth("stream-secret-key")
	tokA, err := ta.Mint("u-alice")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	_, sig, _ := strings.Cut(tokA, ".")
	forgedPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"user_id":"u-bob","exp":9999999999,"nonce":"forged"}`))
	forged := forgedPayload + "." + sig
	if _, err := ta.Verify(forged); err == nil {
		t.Fatal("a ticket whose user_id was swapped under a stolen signature must fail verification")
	}
}

// TestStreamTicket_WrongSecretFails pins that a ticket signed with one key never verifies under another.
func TestStreamTicket_WrongSecretFails(t *testing.T) {
	a1 := newTicketAuth("secret-one")
	a2 := newTicketAuth("secret-two")
	tok, err := a1.Mint("u-alice")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := a2.Verify(tok); !errors.Is(err, errTicketInvalid) {
		t.Fatalf("a ticket signed by a different key must be invalid, got %v", err)
	}
}

// TestStreamTicket_Expiry pins the 60s TTL via an injected clock.
func TestStreamTicket_Expiry(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	ta := newTicketAuth("stream-secret-key")
	ta.now = func() time.Time { return base }
	tok, err := ta.Mint("u-alice")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// Still valid at +59s.
	ta.now = func() time.Time { return base.Add(59 * time.Second) }
	if _, err := ta.Verify(tok); err != nil {
		t.Fatalf("ticket should still be valid at +59s, got %v", err)
	}
	// Mint a fresh one AT base (the first was consumed), then step past its 60s TTL.
	ta.now = func() time.Time { return base }
	tok2, _ := ta.Mint("u-alice")
	ta.now = func() time.Time { return base.Add(61 * time.Second) }
	if _, err := ta.Verify(tok2); !errors.Is(err, errTicketExpired) {
		t.Fatalf("ticket should be expired at +61s, got %v", err)
	}
}

// TestStreamTicket_Malformed pins that garbage input is a clean invalid, never a panic.
func TestStreamTicket_Malformed(t *testing.T) {
	ta := newTicketAuth("stream-secret-key")
	for _, bad := range []string{"", "no-dot", "a.b.c", "....", "!!!.???"} {
		if _, err := ta.Verify(bad); err == nil {
			t.Fatalf("malformed ticket %q should be rejected", bad)
		}
	}
}
