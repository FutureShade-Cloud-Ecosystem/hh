package portal

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Sentinel errors the handler maps to HTTP statuses.
var (
	errInvalidCredentials = errors.New("invalid credentials")                                 // 401, generic (no enumeration)
	errLastAdmin          = errors.New("last account_admin cannot be demoted or deactivated") // 409
	errEmailTaken         = errors.New("an account with that email already exists")           // 409
)

// portalStore is the persistence the Service needs. Real impl: Repository (pgx); tests use a fake.
type portalStore interface {
	FindUserByEmail(ctx context.Context, email string) (User, error)
	// ResolveTenantUser is the bearer-split read (HH-3.2 fold): the row id,
	// customer and role for a platform user within one tenant, live rows only.
	ResolveTenantUser(ctx context.Context, platformUserID, tenantID string) (PortalContext, error)
	GetUser(ctx context.Context, id string) (User, error)
	ListUsers(ctx context.Context, customerID string) ([]User, error)
	// UpdateRole / UpdateStatus mutate one user. When enforceLastAdmin is set the
	// store performs the change atomically with the last-active-admin invariant
	// (count + update under a per-customer lock), so concurrent demotions can never
	// race the tenant down to zero admins (M-1).
	UpdateRole(ctx context.Context, id, role string, enforceLastAdmin bool) error
	UpdateStatus(ctx context.Context, id, status string, enforceLastAdmin bool) error
	TouchLastLogin(ctx context.Context, id string) error
	CreateInvite(ctx context.Context, customerID, email, role, rawToken, tokenHash, createdBy string, expiresAt time.Time) (Invite, error)
	ListInvites(ctx context.Context, customerID string) ([]Invite, error)
	// AcceptInvite validates+consumes the invite, then calls hashPassword (only for
	// a real, unconsumed invite) to provision the user, so bcrypt never runs for a
	// bogus token. Returns errInvalidToken (invalid/expired/used) or errEmailTaken.
	AcceptInvite(ctx context.Context, tokenHash, name string, hashPassword func() (string, error), now time.Time) (User, error)

	// -- self-service: profile + password + reset (settings.go / password_reset.go) --

	// GetUserWithHash loads one user INCLUDING the bcrypt hash, for the change-password
	// current-credential check. errNotFound when the row is gone.
	GetUserWithHash(ctx context.Context, id string) (User, error)
	// UpdateProfile PARTIALLY updates the user's name + notification_prefs: a nil argument
	// leaves that column unchanged (the FE saves each field independently). errNotFound when
	// the row is gone.
	UpdateProfile(ctx context.Context, id string, name *string, notificationPrefs *[]byte) error
	// UpdatePassword swaps the bcrypt hash (and bumps updated_at). errNotFound when gone.
	UpdatePassword(ctx context.Context, id, passwordHash string) error
	// CreatePasswordReset stores a single-use, time-boxed, hashed reset-token row and emits
	// the reset-delivery signal (carrying the raw token) to the outbox in the same tx.
	CreatePasswordReset(ctx context.Context, userID, email, rawToken, tokenHash string, expiresAt time.Time) error
	// ConsumePasswordReset atomically validates + spends a reset token (single-use) and
	// returns its user id. errInvalidToken for any invalid/expired/already-used token.
	ConsumePasswordReset(ctx context.Context, tokenHash string, now time.Time) (string, error)
	// RevokeSessions consumes the user's outstanding reset tokens after a credential change
	// (best-effort). It revokes no session: the platform issues every session (HH-197).
	RevokeSessions(ctx context.Context, userID string) error
}

// Service holds contractor-portal auth logic.
type Service struct {
	store  portalStore
	secret string
	now    func() time.Time
	// config is the dealer branding the login and refresh answers carry;
	// the zero value selects defaultConfig, which names no dealer.
	config *Config
}

func NewService(store portalStore, secret string) *Service {
	return &Service{store: store, secret: secret, now: time.Now}
}

// WithConfig sets the dealer config the login and refresh answers carry,
// returning the service. Call it at the wiring, before serving.
func (s *Service) WithConfig(c Config) *Service {
	s.config = &c
	return s
}

// dealerConfig is the config the answers carry: the one the wiring gave, or
// the neutral default.
func (s *Service) dealerConfig() Config {
	if s.config != nil {
		return *s.config
	}
	return defaultConfig()
}

// Login authenticates by email+password and returns a signed JWT. Not-found, bad
// password, and inactive accounts all return the same generic error (US-CP07-1).
func (s *Service) Login(ctx context.Context, email, password string) (LoginResponse, error) {
	u, err := s.store.FindUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil || u.Status != "active" {
		burnPassword(password) // constant-time miss path, no email enumeration via latency
		return LoginResponse{}, errInvalidCredentials
	}
	if !checkPassword(u.passwordHash, password) {
		return LoginResponse{}, errInvalidCredentials
	}
	token, err := issueJWT(s.secret, Claims{Sub: u.ID, CustomerID: u.CustomerID, Role: u.Role}, s.now())
	if err != nil {
		return LoginResponse{}, err
	}
	_ = s.store.TouchLastLogin(ctx, u.ID) // best-effort
	return LoginResponse{Token: token, User: u, Config: s.dealerConfig()}, nil
}

// Authenticate verifies a Bearer token AND re-loads the user from the DB, so the
// caller's CURRENT role and status decide capabilities, never the (possibly
// stale) token claims. A user deactivated or demoted after their token was issued
// loses access on their very next request rather than at token expiry (H-4).
func (s *Service) Authenticate(ctx context.Context, token string) (PortalContext, error) {
	c, _, err := s.authenticate(ctx, token)
	return c, err
}

// authenticate is Authenticate plus the VERIFIED CLAIMS it derived. Only the refresh path needs
// the claims (it has to carry the original-login anchor forward), and letting it re-implement the
// verify + reload would give the ERP two places that decide whether a session is alive. There is
// one, and this is it.
func (s *Service) authenticate(ctx context.Context, token string) (PortalContext, Claims, error) {
	c, err := verifyJWT(s.secret, token, s.now())
	if err != nil {
		return PortalContext{}, Claims{}, err
	}
	u, err := s.store.GetUser(ctx, c.Sub)
	if err != nil || u.Status != "active" {
		return PortalContext{}, Claims{}, errInvalidToken // deleted / deactivated since issue
	}
	return PortalContext{UserID: u.ID, CustomerID: u.CustomerID, Role: u.Role}, c, nil
}

// ListUsers returns the caller's company users.
func (s *Service) ListUsers(ctx context.Context, caller PortalContext) ([]User, error) {
	return s.store.ListUsers(ctx, caller.CustomerID)
}

// ListInvites returns the caller's company pending invites.
func (s *Service) ListInvites(ctx context.Context, caller PortalContext) ([]Invite, error) {
	return s.store.ListInvites(ctx, caller.CustomerID)
}

// Invite creates a single-use, time-boxed invite. The raw accept token is returned
// in the response (email deferred, D-026).
func (s *Service) Invite(ctx context.Context, caller PortalContext, email, role string) (Invite, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return Invite{}, httpx.BadRequestf("email is required")
	}
	if !validRole(role) {
		return Invite{}, httpx.BadRequestf("unknown role %q", role)
	}
	raw, hashed, err := newInviteToken()
	if err != nil {
		return Invite{}, err
	}
	inv, err := s.store.CreateInvite(ctx, caller.CustomerID, email, role, raw, hashed, caller.UserID, s.now().Add(tokenTTL))
	if err != nil {
		return Invite{}, err
	}
	inv.Token = raw // returned once to the admin (deferred-email accept-link, D-026)
	return inv, nil
}

// AcceptInvite consumes an invite token and provisions the user. The password is
// hashed lazily inside the store, only after the invite is confirmed valid, so a
// flood of bogus tokens never triggers bcrypt (no CPU-amplification DoS).
func (s *Service) AcceptInvite(ctx context.Context, rawToken, name, password string) (User, error) {
	if strings.TrimSpace(rawToken) == "" || len(password) < 8 {
		return User{}, errInvalidToken // generic; also rejects weak/empty passwords
	}
	return s.store.AcceptInvite(ctx, hashToken(rawToken), strings.TrimSpace(name),
		func() (string, error) { return hashPassword(password) }, s.now())
}

// UpdateRole changes a teammate's role. Tenancy is checked here; the last-admin
// invariant is enforced atomically inside the store when demoting an admin (M-1).
func (s *Service) UpdateRole(ctx context.Context, caller PortalContext, targetID, newRole string) error {
	if !validRole(newRole) {
		return httpx.BadRequestf("unknown role %q", newRole)
	}
	target, err := s.sameTenantTarget(ctx, caller, targetID)
	if err != nil {
		return err
	}
	demotingAdmin := target.Role == string(RoleAccountAdmin) && newRole != string(RoleAccountAdmin)
	return s.store.UpdateRole(ctx, targetID, newRole, demotingAdmin)
}

// UpdateStatus changes a teammate's status. Tenancy is checked here; the last-admin
// invariant is enforced atomically inside the store when deactivating an admin (M-1).
func (s *Service) UpdateStatus(ctx context.Context, caller PortalContext, targetID, newStatus string) error {
	switch newStatus {
	case "active", "inactive", "pending":
	default:
		return httpx.BadRequestf("unknown status %q", newStatus)
	}
	target, err := s.sameTenantTarget(ctx, caller, targetID)
	if err != nil {
		return err
	}
	deactivatingAdmin := target.Role == string(RoleAccountAdmin) && target.Status == "active" && newStatus != "active"
	return s.store.UpdateStatus(ctx, targetID, newStatus, deactivatingAdmin)
}

// sameTenantTarget loads the target and verifies it belongs to the caller's company.
func (s *Service) sameTenantTarget(ctx context.Context, caller PortalContext, targetID string) (User, error) {
	target, err := s.store.GetUser(ctx, targetID)
	if err != nil {
		return User{}, err
	}
	if target.CustomerID != caller.CustomerID {
		return User{}, errNotFound // do not leak cross-tenant existence
	}
	return target, nil
}

// -- self-service: profile + password + reset ---------------------------------

// errWrongPassword is returned by ChangePassword when the supplied current password
// does not match. Kept distinct from errInvalidCredentials (login) so the self-service
// handler answers 403 without the login enumeration framing.
var errWrongPassword = errors.New("current password is incorrect")

// minPasswordLen mirrors the AcceptInvite policy (US-CP07-3): a password shorter than
// 8 characters is never hashed or stored.
const minPasswordLen = 8

// Me returns the caller's own user record (the password hash is never serialized).
func (s *Service) Me(ctx context.Context, caller PortalContext) (User, error) {
	return s.store.GetUser(ctx, caller.UserID)
}

// UpdateProfile PARTIALLY edits the caller's display name and/or notification preferences and
// returns the refreshed record. A nil name or nil prefs means "field omitted" and leaves that
// column unchanged - the FE saves name and prefs in separate single-field forms, so a
// full-replace would let each save wipe the other (re-review #3). prefs, when present, is the
// raw notification_prefs JSON object already validated/normalized by the handler.
func (s *Service) UpdateProfile(ctx context.Context, caller PortalContext, name *string, prefs *[]byte) (User, error) {
	var trimmed *string
	if name != nil {
		t := strings.TrimSpace(*name)
		trimmed = &t
	}
	// Nothing to change -> just return the current record (no needless write).
	if trimmed == nil && prefs == nil {
		return s.store.GetUser(ctx, caller.UserID)
	}
	if err := s.store.UpdateProfile(ctx, caller.UserID, trimmed, prefs); err != nil {
		return User{}, err
	}
	return s.store.GetUser(ctx, caller.UserID)
}

// ChangePassword verifies the caller's current password, then sets a new one and revokes
// the account's reset tokens (§4.6 review #2). NOT SERVED: the route answers 501 until HH-4.2 (HH-197),
// because the platform issues every session and this hash is read only by unserved paths. The current-password check runs against
// the freshly loaded hash, never the token. A wrong current password → errWrongPassword;
// a too-short new password → a bad request.
func (s *Service) ChangePassword(ctx context.Context, caller PortalContext, current, next string) error {
	u, err := s.store.GetUserWithHash(ctx, caller.UserID)
	if err != nil {
		return err
	}
	if !checkPassword(u.passwordHash, current) {
		return errWrongPassword
	}
	if len(next) < minPasswordLen {
		return httpx.BadRequestf("new password must be at least %d characters", minPasswordLen)
	}
	hash, err := hashPassword(next)
	if err != nil {
		return err
	}
	if err := s.store.UpdatePassword(ctx, caller.UserID, hash); err != nil {
		return err
	}
	_ = s.store.RevokeSessions(ctx, caller.UserID) // best-effort, like TouchLastLogin
	return nil
}

// RequestPasswordReset is enumeration-safe: it reports nothing about whether the email
// exists. Only when the email maps to a real, active user is a single-use, time-boxed,
// hashed reset row created; the raw token is returned so the caller (the email seam, F0.2)
// can deliver it out-of-band, never echoed to the requester. A miss is a silent no-op.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) (string, error) {
	u, err := s.store.FindUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil || u.Status != "active" {
		return "", nil // no such (active) user, say nothing, do nothing
	}
	raw, hashed, err := newInviteToken()
	if err != nil {
		return "", err
	}
	if err := s.store.CreatePasswordReset(ctx, u.ID, u.Email, raw, hashed, s.now().Add(passwordResetTTL)); err != nil {
		return "", err
	}
	return raw, nil
}

// ConfirmPasswordReset validates and consumes a reset token (single-use, atomic), sets the
// new password, and revokes the user's other outstanding reset tokens. Any
// invalid/expired/already-used token returns errInvalidToken, identical, no enumeration -
// exactly like AcceptInvite; a too-short password is rejected before the token is spent.
func (s *Service) ConfirmPasswordReset(ctx context.Context, rawToken, next string) error {
	if strings.TrimSpace(rawToken) == "" || len(next) < minPasswordLen {
		return errInvalidToken // generic; also rejects weak/empty without revealing which
	}
	userID, err := s.store.ConsumePasswordReset(ctx, hashToken(rawToken), s.now())
	if err != nil {
		return err // errInvalidToken on miss/expired/already-used
	}
	hash, err := hashPassword(next)
	if err != nil {
		return err
	}
	if err := s.store.UpdatePassword(ctx, userID, hash); err != nil {
		return err
	}
	_ = s.store.RevokeSessions(ctx, userID) // best-effort
	return nil
}
