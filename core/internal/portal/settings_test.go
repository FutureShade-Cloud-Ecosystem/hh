package portal

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// seedSelfServiceUser adds one active user with a known current password and returns the
// service + caller context for the self-service (settings) flows.
func seedSelfServiceUser(t *testing.T, current string) (*Service, *fakeStore, PortalContext) {
	t.Helper()
	store := newFakeStore()
	hash, err := hashPassword(current)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "me@acme.test", Name: "Old Name",
		Role: "buyer", Status: "active", passwordHash: hash})
	return NewService(store, testSecret), store, PortalContext{UserID: "u1", CustomerID: "c1", Role: "buyer"}
}

func TestPortal_Me_ReturnsOwnRecord(t *testing.T) {
	svc, _, caller := seedSelfServiceUser(t, "current-pw")
	u, err := svc.Me(context.Background(), caller)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if u.ID != "u1" || u.Email != "me@acme.test" {
		t.Fatalf("unexpected record: %+v", u)
	}
	if u.passwordHash == "" {
		t.Fatal("fake should still hold the hash internally")
	}
}

func strptr(s string) *string   { return &s }
func bytesptr(b []byte) *[]byte { return &b }

func TestPortal_UpdateProfile_SetsNameAndPrefs(t *testing.T) {
	svc, store, caller := seedSelfServiceUser(t, "current-pw")
	prefs := []byte(`{"email":true,"sms":false}`)
	u, err := svc.UpdateProfile(context.Background(), caller, strptr("  New Name  "), bytesptr(prefs))
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if u.Name != "New Name" {
		t.Fatalf("name should be trimmed and stored, got %q", u.Name)
	}
	if got := store.prefsByID["u1"]; got != string(prefs) {
		t.Fatalf("prefs not persisted: got %q", got)
	}
}

// Re-review #3: a name-only PUT (prefs omitted) must PRESERVE the existing prefs.
func TestPortal_UpdateProfile_NameOnly_PreservesPrefs(t *testing.T) {
	svc, store, caller := seedSelfServiceUser(t, "current-pw")
	prefs := []byte(`{"email":true,"sms":false}`)
	if _, err := svc.UpdateProfile(context.Background(), caller, strptr("First"), bytesptr(prefs)); err != nil {
		t.Fatalf("seed prefs: %v", err)
	}
	// Name-only update: prefs omitted (nil).
	u, err := svc.UpdateProfile(context.Background(), caller, strptr("Second"), nil)
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if u.Name != "Second" {
		t.Fatalf("name should update, got %q", u.Name)
	}
	if got := store.prefsByID["u1"]; got != string(prefs) {
		t.Fatalf("name-only PUT wiped prefs: got %q, want %q", got, string(prefs))
	}
}

// Re-review #3: a prefs-only PUT (name omitted) must PRESERVE the existing name.
func TestPortal_UpdateProfile_PrefsOnly_PreservesName(t *testing.T) {
	svc, store, caller := seedSelfServiceUser(t, "current-pw")
	if _, err := svc.UpdateProfile(context.Background(), caller, strptr("Keep Me"), bytesptr([]byte(`{"email":false}`))); err != nil {
		t.Fatalf("seed name: %v", err)
	}
	// Prefs-only update: name omitted (nil).
	newPrefs := []byte(`{"email":true}`)
	u, err := svc.UpdateProfile(context.Background(), caller, nil, bytesptr(newPrefs))
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if u.Name != "Keep Me" {
		t.Fatalf("prefs-only PUT wiped name: got %q, want %q", u.Name, "Keep Me")
	}
	if got := store.prefsByID["u1"]; got != string(newPrefs) {
		t.Fatalf("prefs should update, got %q", got)
	}
}

// normalizePrefs (handler-layer) still defaults an absent/null payload to an empty object so
// the JSONB column never receives invalid bytes.
func TestPortal_NormalizePrefs_EmptyDefaultsToObject(t *testing.T) {
	got, err := normalizePrefs(nil)
	if err != nil {
		t.Fatalf("normalize nil: %v", err)
	}
	if string(got) != "{}" {
		t.Fatalf("nil prefs should normalize to {}, got %q", got)
	}
	got, err = normalizePrefs([]byte("null"))
	if err != nil {
		t.Fatalf("normalize null: %v", err)
	}
	if string(got) != "{}" {
		t.Fatalf("null prefs should normalize to {}, got %q", got)
	}
}

func TestPortal_ChangePassword_WrongCurrent(t *testing.T) {
	svc, store, caller := seedSelfServiceUser(t, "current-pw")
	before := store.usersByID["u1"].passwordHash
	if err := svc.ChangePassword(context.Background(), caller, "not-the-pw", "brand-new-pw"); err != errWrongPassword {
		t.Fatalf("wrong current password: want errWrongPassword, got %v", err)
	}
	if store.usersByID["u1"].passwordHash != before {
		t.Fatal("password must not change on a wrong-current attempt")
	}
	if store.revoked["u1"] != 0 {
		t.Fatal("a failed change must not revoke sessions")
	}
}

func TestPortal_ChangePassword_TooShort(t *testing.T) {
	svc, _, caller := seedSelfServiceUser(t, "current-pw")
	err := svc.ChangePassword(context.Background(), caller, "current-pw", "short")
	if err == nil {
		t.Fatal("expected rejection of a <8 char new password")
	}
	var bre httpx.BadRequestError
	if !errors.As(err, &bre) {
		t.Fatalf("want httpx.BadRequestError, got %T %v", err, err)
	}
}

func TestPortal_ChangePassword_Success_RevokesSessions(t *testing.T) {
	svc, store, caller := seedSelfServiceUser(t, "current-pw")
	if err := svc.ChangePassword(context.Background(), caller, "current-pw", "brand-new-pw"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	// New password now verifies; the old one no longer does.
	u := store.usersByID["u1"]
	if !checkPassword(u.passwordHash, "brand-new-pw") {
		t.Fatal("new password should verify after change")
	}
	if checkPassword(u.passwordHash, "current-pw") {
		t.Fatal("old password must no longer verify")
	}
	if store.revoked["u1"] != 1 {
		t.Fatalf("a successful change must revoke other sessions once, got %d", store.revoked["u1"])
	}
}
