package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// doJSON posts a JSON body to a password-reset handler and returns the
// status code + raw body. Bearer-split carry note (HH-3.2 fold): the reset
// pair are unregistered mint surfaces (the leave-behind), so the flow
// drives the handlers directly.
func doJSON(t *testing.T, h *Handler, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if strings.Contains(path, "confirm") {
		h.confirmPasswordReset(rec, req)
	} else {
		h.requestPasswordReset(rec, req)
	}
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func seedResetUser(t *testing.T, current string) (*Service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	hash, err := hashPassword(current)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "me@acme.test",
		Role: "buyer", Status: "active", passwordHash: hash})
	return NewService(store, testSecret), store
}

func TestPortal_RequestPasswordReset_Service_OnlyCreatesForRealUser(t *testing.T) {
	svc, store := seedResetUser(t, "current-pw")

	raw, err := svc.RequestPasswordReset(context.Background(), "me@acme.test")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if raw == "" {
		t.Fatal("a real user should yield a raw token (for the email seam)")
	}
	if len(store.resets) != 1 {
		t.Fatalf("expected one reset row, got %d", len(store.resets))
	}

	// Unknown email: no token, no row, no error.
	raw2, err := svc.RequestPasswordReset(context.Background(), "ghost@acme.test")
	if err != nil || raw2 != "" {
		t.Fatalf("unknown email must be a silent no-op, got raw=%q err=%v", raw2, err)
	}
	if len(store.resets) != 1 {
		t.Fatalf("no row should be created for an unknown email, have %d", len(store.resets))
	}

	// Inactive user: also a silent no-op.
	store.addUser(User{ID: "u2", CustomerID: "c1", Email: "off@acme.test", Role: "buyer", Status: "inactive"})
	raw3, err := svc.RequestPasswordReset(context.Background(), "off@acme.test")
	if err != nil || raw3 != "" {
		t.Fatalf("inactive user must be a silent no-op, got raw=%q err=%v", raw3, err)
	}
}

func TestPortal_RequestPasswordReset_HTTP_202_BothWays(t *testing.T) {
	svc, _ := seedResetUser(t, "current-pw")
	h := NewHandler(svc, nil)

	codeReal, bodyReal := doJSON(t, h, "POST", "/api/portal/v1/password-reset/request", `{"email":"me@acme.test"}`)
	codeGhost, bodyGhost := doJSON(t, h, "POST", "/api/portal/v1/password-reset/request", `{"email":"ghost@acme.test"}`)

	if codeReal != http.StatusAccepted || codeGhost != http.StatusAccepted {
		t.Fatalf("both must be 202, got real=%d ghost=%d", codeReal, codeGhost)
	}
	if bodyReal != bodyGhost {
		t.Fatalf("response body must be identical (no enumeration): %q vs %q", bodyReal, bodyGhost)
	}
}

func TestPortal_ConfirmPasswordReset_HappyPath(t *testing.T) {
	svc, store := seedResetUser(t, "current-pw")

	raw, err := svc.RequestPasswordReset(context.Background(), "me@acme.test")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := svc.ConfirmPasswordReset(context.Background(), raw, "brand-new-pw"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	u := store.usersByID["u1"]
	if !checkPassword(u.passwordHash, "brand-new-pw") {
		t.Fatal("new password should verify after a confirmed reset")
	}
	if store.revoked["u1"] != 1 {
		t.Fatalf("a confirmed reset must revoke sessions once, got %d", store.revoked["u1"])
	}
}

func TestPortal_ConfirmPasswordReset_Replayed(t *testing.T) {
	svc, _ := seedResetUser(t, "current-pw")
	raw, err := svc.RequestPasswordReset(context.Background(), "me@acme.test")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := svc.ConfirmPasswordReset(context.Background(), raw, "brand-new-pw"); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	if err := svc.ConfirmPasswordReset(context.Background(), raw, "another-new-pw"); err != errInvalidToken {
		t.Fatalf("replay of a consumed token must be errInvalidToken, got %v", err)
	}
}

func TestPortal_ConfirmPasswordReset_Expired(t *testing.T) {
	svc, store := seedResetUser(t, "current-pw")
	// Seed an already-expired reset directly.
	store.resets[hashToken("expired-tok")] = &fakeReset{userID: "u1", expiresAt: time.Now().Add(-time.Minute)}

	errExpired := svc.ConfirmPasswordReset(context.Background(), "expired-tok", "brand-new-pw")
	errInvalid := svc.ConfirmPasswordReset(context.Background(), "never-existed", "brand-new-pw")
	if errExpired != errInvalidToken || errInvalid != errInvalidToken {
		t.Fatalf("expired and unknown must both be errInvalidToken: expired=%v invalid=%v", errExpired, errInvalid)
	}
}

func TestPortal_ConfirmPasswordReset_WeakPassword_DoesNotConsume(t *testing.T) {
	svc, _ := seedResetUser(t, "current-pw")
	raw, err := svc.RequestPasswordReset(context.Background(), "me@acme.test")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := svc.ConfirmPasswordReset(context.Background(), raw, "short"); err != errInvalidToken {
		t.Fatalf("weak password must be rejected (generic errInvalidToken), got %v", err)
	}
	// Token must still be spendable, a weak attempt does not burn it.
	if err := svc.ConfirmPasswordReset(context.Background(), raw, "brand-new-pw"); err != nil {
		t.Fatalf("token should survive a weak-password attempt: %v", err)
	}
}
