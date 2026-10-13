package portal

import (
	"context"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"strings"
	"testing"
	"time"
)

const testSecret = "test-secret-key"

// -- fake store ---------------------------------------------------------------

type fakeInvite struct {
	customerID, email, role string
	accepted, expired       bool
}

type fakeReset struct {
	userID    string
	expiresAt time.Time
	consumed  bool
}

type fakeStore struct {
	usersByID    map[string]User
	usersByEmail map[string]User
	invites      map[string]*fakeInvite // keyed by token hash
	resets       map[string]*fakeReset  // keyed by token hash
	prefsByID    map[string]string      // last notification_prefs written per user
	revoked      map[string]int         // RevokeSessions call count per user
	seq          int
}

// ResolveTenantUser implements the bearer-split read over the fake's rows:
// a live, active user of that tenant resolves; everything else does not.
func (f *fakeStore) ResolveTenantUser(_ context.Context, platformUserID, tenantID string) (PortalContext, error) {
	for _, u := range f.usersByID {
		if u.CustomerID == tenantID && u.Status == "active" {
			return PortalContext{UserID: u.ID, CustomerID: u.CustomerID, Role: u.Role}, nil
		}
	}
	return PortalContext{}, errNoTenantCaller
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByID:    map[string]User{},
		usersByEmail: map[string]User{},
		invites:      map[string]*fakeInvite{},
		resets:       map[string]*fakeReset{},
		prefsByID:    map[string]string{},
		revoked:      map[string]int{},
	}
}

// otherActiveAdmins counts active account_admins for a customer, excluding exceptID -
// the same invariant the real repo enforces transactionally (M-1).
func (f *fakeStore) otherActiveAdmins(customerID, exceptID string) int {
	n := 0
	for _, u := range f.usersByID {
		if u.ID != exceptID && u.CustomerID == customerID && u.Role == "account_admin" && u.Status == "active" {
			n++
		}
	}
	return n
}

func (f *fakeStore) addUser(u User) {
	f.usersByID[u.ID] = u
	f.usersByEmail[strings.ToLower(u.Email)] = u
}

func (f *fakeStore) FindUserByEmail(_ context.Context, email string) (User, error) {
	u, ok := f.usersByEmail[strings.ToLower(email)]
	if !ok {
		return User{}, errNotFound
	}
	return u, nil
}

func (f *fakeStore) GetUser(_ context.Context, id string) (User, error) {
	u, ok := f.usersByID[id]
	if !ok {
		return User{}, errNotFound
	}
	return u, nil
}

func (f *fakeStore) ListUsers(_ context.Context, customerID string) ([]User, error) {
	var out []User
	for _, u := range f.usersByID {
		if u.CustomerID == customerID {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateRole(_ context.Context, id, role string, enforceLastAdmin bool) error {
	u, ok := f.usersByID[id]
	if !ok {
		return errNotFound
	}
	if enforceLastAdmin && f.otherActiveAdmins(u.CustomerID, id) == 0 {
		return errLastAdmin
	}
	u.Role = role
	f.addUser(u)
	return nil
}

func (f *fakeStore) UpdateStatus(_ context.Context, id, status string, enforceLastAdmin bool) error {
	u, ok := f.usersByID[id]
	if !ok {
		return errNotFound
	}
	if enforceLastAdmin && f.otherActiveAdmins(u.CustomerID, id) == 0 {
		return errLastAdmin
	}
	u.Status = status
	f.addUser(u)
	return nil
}

func (f *fakeStore) TouchLastLogin(_ context.Context, _ string) error { return nil }

func (f *fakeStore) CreateInvite(_ context.Context, customerID, email, role, _, tokenHash, _ string, _ time.Time) (Invite, error) {
	f.invites[tokenHash] = &fakeInvite{customerID: customerID, email: email, role: role}
	return Invite{CustomerID: customerID, Email: email, Role: role}, nil
}

func (f *fakeStore) ListInvites(_ context.Context, customerID string) ([]Invite, error) {
	var out []Invite
	for _, inv := range f.invites {
		if inv.customerID == customerID && !inv.accepted && !inv.expired {
			out = append(out, Invite{CustomerID: inv.customerID, Email: inv.email, Role: inv.role})
		}
	}
	return out, nil
}

func (f *fakeStore) AcceptInvite(_ context.Context, tokenHash, name string, hashPassword func() (string, error), _ time.Time) (User, error) {
	inv, ok := f.invites[tokenHash]
	if !ok || inv.accepted || inv.expired {
		return User{}, errInvalidToken // identical for invalid/used/expired, and NO hashing
	}
	hash, err := hashPassword() // only hash for a valid invite (matches the real repo)
	if err != nil {
		return User{}, err
	}
	inv.accepted = true
	f.seq++
	u := User{
		ID: fmt.Sprintf("u%d", f.seq), CustomerID: inv.customerID, Email: inv.email,
		Name: name, Role: inv.role, Status: "active", passwordHash: hash,
	}
	f.addUser(u)
	return u, nil
}

func (f *fakeStore) GetUserWithHash(_ context.Context, id string) (User, error) {
	u, ok := f.usersByID[id]
	if !ok {
		return User{}, errNotFound
	}
	return u, nil
}

func (f *fakeStore) UpdateProfile(_ context.Context, id string, name *string, notificationPrefs *[]byte) error {
	u, ok := f.usersByID[id]
	if !ok {
		return errNotFound
	}
	// Partial update: a nil field is omitted and left unchanged (mirrors the COALESCE write).
	if name != nil {
		u.Name = *name
		f.addUser(u)
	}
	if notificationPrefs != nil {
		f.prefsByID[id] = string(*notificationPrefs)
	}
	return nil
}

func (f *fakeStore) UpdatePassword(_ context.Context, id, passwordHash string) error {
	u, ok := f.usersByID[id]
	if !ok {
		return errNotFound
	}
	u.passwordHash = passwordHash
	f.addUser(u)
	return nil
}

func (f *fakeStore) CreatePasswordReset(_ context.Context, userID, _, _, tokenHash string, expiresAt time.Time) error {
	f.resets[tokenHash] = &fakeReset{userID: userID, expiresAt: expiresAt}
	return nil
}

func (f *fakeStore) ConsumePasswordReset(_ context.Context, tokenHash string, now time.Time) (string, error) {
	rs, ok := f.resets[tokenHash]
	if !ok || rs.consumed || !rs.expiresAt.After(now) {
		return "", errInvalidToken // identical for invalid/used/expired, no enumeration
	}
	rs.consumed = true
	return rs.userID, nil
}

func (f *fakeStore) RevokeSessions(_ context.Context, userID string) error {
	f.revoked[userID]++
	for _, rs := range f.resets {
		if rs.userID == userID {
			rs.consumed = true
		}
	}
	return nil
}

// -- helpers ------------------------------------------------------------------

func tokenFor(t *testing.T, role, customerID, userID string) string {
	t.Helper()
	tok, err := issueJWT(testSecret, Claims{Sub: userID, CustomerID: customerID, Role: role}, time.Now())
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

// -- login --------------------------------------------------------------------

func TestPortal_Login_IssuesJWT(t *testing.T) {
	store := newFakeStore()
	hash, _ := hashPassword("hunter2pw")
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "admin@acme.test", Role: "account_admin", Status: "active", passwordHash: hash})
	svc := NewService(store, testSecret).WithConfig(Config{ID: "acme", DealerName: "Acme Yard"})

	resp, err := svc.Login(context.Background(), "admin@acme.test", "hunter2pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("expected a JWT")
	}
	c, err := svc.Authenticate(context.Background(), resp.Token)
	if err != nil {
		t.Fatalf("authenticate issued token: %v", err)
	}
	if c.UserID != "u1" || c.CustomerID != "c1" || c.Role != "account_admin" {
		t.Fatalf("claims mismatch: %+v", c)
	}
	if resp.Config.DealerName != "Acme Yard" {
		t.Fatalf("login config = %+v, want the dealer config the service was given", resp.Config)
	}
}

// A service given no dealer config names no dealer: the login and refresh
// answers carry the neutral default, never one dealer's identity (decision
// log row 1592).
func TestPortal_NoDealerConfigNamesNoDealer(t *testing.T) {
	store := newFakeStore()
	hash, _ := hashPassword("hunter2pw")
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "admin@acme.test", Role: "account_admin", Status: "active", passwordHash: hash})
	resp, err := NewService(store, testSecret).Login(context.Background(), "admin@acme.test", "hunter2pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	c := resp.Config
	if c.ID != "default" || c.DealerName != "" || c.LogoURL != "" || c.PrimaryColor != "" || c.SupportEmail != "" || c.SupportPhone != "" {
		t.Fatalf("the default config names a dealer: %+v", c)
	}
	if strings.Contains(strings.ToLower(c.ID+c.DealerName+c.SupportEmail), "dibbits") {
		t.Fatalf("the default config names another dealer: %+v", c)
	}
}

func TestPortal_Login_BadCreds_401_NoEnumeration(t *testing.T) {
	store := newFakeStore()
	hash, _ := hashPassword("correct-horse")
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "real@acme.test", Role: "buyer", Status: "active", passwordHash: hash})
	svc := NewService(store, testSecret)

	_, errWrongPw := svc.Login(context.Background(), "real@acme.test", "wrong")
	_, errNoUser := svc.Login(context.Background(), "ghost@acme.test", "whatever")
	if errWrongPw == nil || errNoUser == nil {
		t.Fatal("expected both to fail")
	}
	if errWrongPw.Error() != errNoUser.Error() {
		t.Fatalf("error must be identical (no enumeration): %q vs %q", errWrongPw, errNoUser)
	}

	// inactive accounts also fail identically
	store.addUser(User{ID: "u2", CustomerID: "c1", Email: "off@acme.test", Role: "buyer", Status: "inactive", passwordHash: hash})
	if _, err := svc.Login(context.Background(), "off@acme.test", "correct-horse"); err == nil {
		t.Fatal("inactive account must not log in")
	}
}

// -- invite / accept ----------------------------------------------------------

func TestPortal_Invite_SingleUse(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, testSecret)
	admin := PortalContext{UserID: "u1", CustomerID: "c1", Role: "account_admin"}

	inv, err := svc.Invite(context.Background(), admin, "new@acme.test", "buyer")
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if inv.Token == "" {
		t.Fatal("invite response must include the raw accept token (D-026)")
	}

	if _, err := svc.AcceptInvite(context.Background(), inv.Token, "New Person", "password123"); err != nil {
		t.Fatalf("first accept should succeed: %v", err)
	}
	if _, err := svc.AcceptInvite(context.Background(), inv.Token, "New Person", "password123"); err == nil {
		t.Fatal("second accept of the same token must fail (single-use)")
	}
}

func TestPortal_AcceptInvite_Expired_Generic(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, testSecret)

	// expired invite
	store.invites[hashToken("expired-tok")] = &fakeInvite{customerID: "c1", email: "x@acme.test", role: "buyer", expired: true}

	_, errExpired := svc.AcceptInvite(context.Background(), "expired-tok", "X", "password123")
	_, errInvalid := svc.AcceptInvite(context.Background(), "never-existed", "X", "password123")
	if errExpired == nil || errInvalid == nil {
		t.Fatal("both should fail")
	}
	if errExpired.Error() != errInvalid.Error() {
		t.Fatalf("expired and invalid must be identical (no enumeration): %q vs %q", errExpired, errInvalid)
	}
}

func TestPortal_AcceptInvite_RejectsWeakPassword(t *testing.T) {
	svc := NewService(newFakeStore(), testSecret)
	if _, err := svc.AcceptInvite(context.Background(), "tok", "X", "short"); err == nil {
		t.Fatal("expected rejection of a <8 char password")
	}
}

// -- last-admin guardrail -----------------------------------------------------

func TestPortal_LastAdmin_Guardrail_409(t *testing.T) {
	store := newFakeStore()
	store.addUser(User{ID: "admin1", CustomerID: "c1", Email: "a@acme.test", Role: "account_admin", Status: "active"})
	svc := NewService(store, testSecret)
	caller := PortalContext{UserID: "admin1", CustomerID: "c1", Role: "account_admin"}

	// admin1 is the only active admin → demote/deactivate must be refused.
	if err := svc.UpdateRole(context.Background(), caller, "admin1", "buyer"); err != errLastAdmin {
		t.Fatalf("demoting the last admin: want errLastAdmin, got %v", err)
	}
	if err := svc.UpdateStatus(context.Background(), caller, "admin1", "inactive"); err != errLastAdmin {
		t.Fatalf("deactivating the last admin: want errLastAdmin, got %v", err)
	}

	// Add a second active admin → demotion of admin1 is now allowed.
	store.addUser(User{ID: "admin2", CustomerID: "c1", Email: "b@acme.test", Role: "account_admin", Status: "active"})
	if err := svc.UpdateRole(context.Background(), caller, "admin1", "buyer"); err != nil {
		t.Fatalf("demotion with 2 admins should succeed: %v", err)
	}
}

func TestPortal_UpdateRole_CrossTenant_NotFound(t *testing.T) {
	store := newFakeStore()
	store.addUser(User{ID: "other", CustomerID: "OTHER", Email: "z@x.test", Role: "buyer", Status: "active"})
	svc := NewService(store, testSecret)
	caller := PortalContext{UserID: "admin1", CustomerID: "c1", Role: "account_admin"}
	if err := svc.UpdateRole(context.Background(), caller, "other", "field_crew"); err != errNotFound {
		t.Fatalf("cross-tenant target must be hidden as not-found, got %v", err)
	}
}

// -- capability gating (handler) ----------------------------------------------

// platformSub is the platform user id the bearer-split tests resolve under
// (the fake matches by tenant, the id only has to be non-empty).
const platformSub = "platform-sub-test"

// handlerWithUser seeds one active portal user with the given role (so the DB-
// resolved Authenticate path can find it) and returns a handler + a token for them.
func handlerWithUser(t *testing.T, role string) (*Handler, *fakeStore, string) {
	t.Helper()
	store := newFakeStore()
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "u1@acme.test", Role: role, Status: "active"})
	h := NewHandler(NewService(store, testSecret), nil)
	return h, store, tokenFor(t, role, "c1", "u1")
}

// Bearer-split carry note (HH-3.2 fold): these authorization halves moved
// to the mechanisms the served path now uses. The role to capability
// mapping is identity.CapabilitiesFor (carried verbatim from this
// module's rbac.go and proven there); the per-request re-read this
// module owns is Service.ResolveTenantCaller, which is what h.tenant
// resolves on every guarded leg. The HS256-token route roundtrips these
// tests used are the superseded issuer world.

func TestPortal_FieldCrew_BillingCapability_403(t *testing.T) {
	if identity.CapabilitiesFor("field_crew").Has(identity.CapViewBilling) {
		t.Fatal("field_crew must not carry view_billing")
	}
}

func TestPortal_Buyer_UserMgmtCapability_403(t *testing.T) {
	if identity.CapabilitiesFor("buyer").Has(identity.CapManageUsers) {
		t.Fatal("buyer must not carry manage_users")
	}
}

func TestPortal_Admin_Allowed(t *testing.T) {
	if !identity.CapabilitiesFor("account_admin").Has(identity.CapManageUsers) {
		t.Fatal("account_admin must carry manage_users")
	}
	if !identity.CapabilitiesFor("account_admin").Has(identity.CapViewBilling) {
		t.Fatal("account_admin must carry view_billing")
	}
}

func TestPortal_NoCaller_Refused(t *testing.T) {
	h, _, _ := handlerWithUser(t, "account_admin")
	if _, err := h.svc.ResolveTenantCaller(context.Background(), "", "c1"); err == nil {
		t.Fatal("a caller with no platform user id must not resolve")
	}
}

// H-4: a caller who is deactivated (or whose role is downgraded) loses
// access on the NEXT request, because the row is re-read per request,
// never carried from the credential.
func TestPortal_DeactivatedAfterIssue_LosesAccess(t *testing.T) {
	h, store, _ := handlerWithUser(t, "account_admin")
	if _, err := h.svc.ResolveTenantCaller(context.Background(), platformSub, "c1"); err != nil {
		t.Fatalf("active admin should resolve: %v", err)
	}
	// Deactivate the user AFTER whatever credential got them here.
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "u1@acme.test", Role: "account_admin", Status: "inactive"})
	if _, err := h.svc.ResolveTenantCaller(context.Background(), platformSub, "c1"); err == nil {
		t.Fatal("deactivated admin must not resolve on the next request")
	}
}

// H-4: a caller demoted while holding power must not retain it.
func TestPortal_DemotedAfterIssue_LosesAdmin(t *testing.T) {
	h, store, _ := handlerWithUser(t, "account_admin")
	store.addUser(User{ID: "u1", CustomerID: "c1", Email: "u1@acme.test", Role: "buyer", Status: "active"})
	c, err := h.svc.ResolveTenantCaller(context.Background(), platformSub, "c1")
	if err != nil {
		t.Fatalf("demoted caller still resolves: %v", err)
	}
	if identity.CapabilitiesFor(c.Role).Has(identity.CapManageUsers) {
		t.Fatal("the CURRENT role must decide, and buyer carries no manage_users")
	}
}
