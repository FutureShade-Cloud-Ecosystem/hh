package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// userColsPublic is the SELECT/RETURNING list WITHOUT password_hash, for paths
// that must never pull the secret back out of the DB. notification_prefs is read as
// COALESCE(...,'{}') so a NULL column surfaces as an empty JSON object, never null.
const userColsPublic = `id::text, customer_id::text, email::text, name, role, status,
	COALESCE(notification_prefs, '{}'::jsonb), created_at, updated_at`

func scanUserPublic(row pgx.Row) (User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.CustomerID, &u.Email, &u.Name, &u.Role, &u.Status,
		&u.NotificationPrefs, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return User{}, err
	}
	return u, nil
}

var (
	errNoDB     = errors.New("portal: database not configured")
	errNotFound = errors.New("portal: not found")
)

// Repository owns portal_users + portal_invites (pgx). It also writes the
// crm.NewPortalContact row to events_outbox inside the accept transaction.
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const userCols = `id::text, customer_id::text, email::text, name, role, status,
	COALESCE(notification_prefs, '{}'::jsonb), created_at, updated_at, password_hash`

func scanUser(row pgx.Row) (User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.CustomerID, &u.Email, &u.Name, &u.Role, &u.Status,
		&u.NotificationPrefs, &u.CreatedAt, &u.UpdatedAt, &u.passwordHash); err != nil {
		return User{}, err
	}
	return u, nil
}

// FindUserByEmail loads a user (with password hash) for login.
func (r *Repository) FindUserByEmail(ctx context.Context, email string) (User, error) {
	if r.pool == nil {
		return User{}, errNoDB
	}
	u, err := scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM portal_users WHERE email = $1`, email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, errNotFound
		}
		return User{}, err
	}
	return u, nil
}

// GetUser loads one user by id (no password hash, not needed off the login path).
// ResolveTenantUser implements the bearer-split read over the module's own
// table: platform_user_id (0215) within the token's tenant, deleted rows
// excluded, and only an ACTIVE row resolves (an inactive caller is a
// credential-lifetime fact and refuses rather than degrading).
func (r *Repository) ResolveTenantUser(ctx context.Context, platformUserID, tenantID string) (PortalContext, error) {
	if r.pool == nil {
		return PortalContext{}, errNoDB
	}
	var c PortalContext
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, customer_id::text, role FROM portal_users
		 WHERE platform_user_id = $1
		   AND tenant_id = $2
		   AND deleted_at IS NULL
		   AND status = 'active'
		 LIMIT 1`, platformUserID, tenantID).Scan(&c.UserID, &c.CustomerID, &c.Role)
	if err != nil {
		return PortalContext{}, errNoTenantCaller
	}
	return c, nil
}

func (r *Repository) GetUser(ctx context.Context, id string) (User, error) {
	if r.pool == nil {
		return User{}, errNoDB
	}
	u, err := scanUserPublic(r.pool.QueryRow(ctx,
		`SELECT `+userColsPublic+` FROM portal_users WHERE id = $1`, id))
	if err != nil {
		// Fold a malformed (non-UUID) id, SQLSTATE 22P02 on the uuid PK cast, into the same
		// IDOR-safe errNotFound as an absent/cross-tenant id, so a probe cannot distinguish them
		// (matches procurement's notFound convention). Authenticate/Me pass JWT-derived UUIDs, so
		// no valid caller regresses.
		var pgErr *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "22P02") {
			return User{}, errNotFound
		}
		return User{}, err
	}
	return u, nil
}

// ListUsers returns every user for a contractor account.
func (r *Repository) ListUsers(ctx context.Context, customerID string) ([]User, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+userColsPublic+` FROM portal_users WHERE customer_id = $1 ORDER BY name, email`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUserPublic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// teamSeqCTE is the data-modifying CTE the team-member writes prepend to
// draw a fresh team-scope seq atomically in the same statement (the 0213
// convention; the team manifest scope's projection is this table, so an
// online role or status change reappears in the tenant's pull feed).
const teamSeqCTE = `WITH seq AS (INSERT INTO scope_sequences (scope) VALUES ('team')
	              ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	              RETURNING next_value) `

// UpdateRole sets a user's role. When enforceLastAdmin is set (the caller is
// demoting an account_admin) the count + update run atomically under a per-customer
// lock so concurrent demotions cannot race the tenant to zero admins (M-1).
func (r *Repository) UpdateRole(ctx context.Context, id, role string, enforceLastAdmin bool) error {
	if !enforceLastAdmin {
		return r.exec(ctx, teamSeqCTE+`UPDATE portal_users SET role = $2, updated_at = now(), seq = (SELECT next_value FROM seq) WHERE id = $1`, id, role)
	}
	return r.guardedAdminUpdate(ctx, id, teamSeqCTE+`UPDATE portal_users SET role = $2, updated_at = now(), seq = (SELECT next_value FROM seq) WHERE id = $1`, role)
}

// UpdateStatus sets a user's status, with the same atomic last-admin guard when
// deactivating an admin (M-1).
func (r *Repository) UpdateStatus(ctx context.Context, id, status string, enforceLastAdmin bool) error {
	if !enforceLastAdmin {
		return r.exec(ctx, teamSeqCTE+`UPDATE portal_users SET status = $2, updated_at = now(), seq = (SELECT next_value FROM seq) WHERE id = $1`, id, status)
	}
	return r.guardedAdminUpdate(ctx, id, teamSeqCTE+`UPDATE portal_users SET status = $2, updated_at = now(), seq = (SELECT next_value FROM seq) WHERE id = $1`, status)
}

// guardedAdminUpdate applies updateSQL(id, arg) only if AT LEAST ONE OTHER active
// account_admin remains for the target's customer. It serializes concurrent admin
// mutations for a tenant by taking a row lock on that customer first, so the
// count it reads cannot be invalidated by a racing transaction before commit.
func (r *Repository) guardedAdminUpdate(ctx context.Context, id, updateSQL, arg string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var customerID string
	if err := tx.QueryRow(ctx, `SELECT customer_id::text FROM portal_users WHERE id = $1`, id).Scan(&customerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return err
	}
	// Serialize all admin-management for this tenant on the customer row.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM customers WHERE id = $1 FOR UPDATE`, customerID); err != nil {
		return err
	}
	var otherAdmins int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM portal_users
		WHERE customer_id = $1 AND role = 'account_admin' AND status = 'active' AND id <> $2`,
		customerID, id).Scan(&otherAdmins); err != nil {
		return err
	}
	if otherAdmins == 0 {
		return errLastAdmin
	}
	ct, err := tx.Exec(ctx, updateSQL, id, arg)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errNotFound
	}
	return tx.Commit(ctx)
}

// TouchLastLogin stamps last_login_at.
func (r *Repository) TouchLastLogin(ctx context.Context, id string) error {
	return r.exec(ctx, `UPDATE portal_users SET last_login_at = now() WHERE id = $1`, id)
}

func (r *Repository) exec(ctx context.Context, sql string, args ...any) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// -- self-service: profile + password + reset ---------------------------------

// GetUserWithHash loads one user INCLUDING the password hash, for the change-password
// current-credential check. errNotFound when the row is gone.
func (r *Repository) GetUserWithHash(ctx context.Context, id string) (User, error) {
	if r.pool == nil {
		return User{}, errNoDB
	}
	u, err := scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM portal_users WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, errNotFound
		}
		return User{}, err
	}
	return u, nil
}

// UpdateProfile PARTIALLY updates the caller's name + notification_prefs. A nil argument is
// passed as SQL NULL and COALESCE leaves that column unchanged, so an omitted field is never
// wiped (re-review #3). notificationPrefs, when present, is a raw JSON object the service has
// already normalized; pgx writes []byte straight to JSONB.
func (r *Repository) UpdateProfile(ctx context.Context, id string, name *string, notificationPrefs *[]byte) error {
	var prefsArg []byte
	if notificationPrefs != nil {
		prefsArg = *notificationPrefs
	}
	return r.exec(ctx,
		`UPDATE portal_users
		 SET name = COALESCE($2, name),
		     notification_prefs = COALESCE($3, notification_prefs),
		     updated_at = now()
		 WHERE id = $1`,
		id, name, prefsArg)
}

// UpdatePassword swaps the bcrypt hash and bumps updated_at. errNotFound when the row is gone.
func (r *Repository) UpdatePassword(ctx context.Context, id, passwordHash string) error {
	return r.exec(ctx,
		`UPDATE portal_users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		id, passwordHash)
}

// CreatePasswordReset stores a single-use, time-boxed, hashed reset-token row. Only the
// sha256 hash is persisted (the raw token is delivered out-of-band, D-026).
func (r *Repository) CreatePasswordReset(ctx context.Context, userID, email, rawToken, tokenHash string, expiresAt time.Time) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`INSERT INTO portal_password_resets (portal_user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt); err != nil {
		return err
	}
	// Emit the reset-delivery signal in the same tx. The raw token lives ONLY here (it is
	// never echoed in an API response) and is redacted at rest by the dispatcher on delivery.
	if err := emitOutbox(ctx, tx, TopicPasswordReset, PasswordResetNotice{
		Email: email, Token: rawToken, ExpiresAt: expiresAt,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ConsumePasswordReset atomically spends a reset token: the conditional UPDATE both checks
// validity (unconsumed + unexpired) and consumes it in one statement, so a replay races to
// zero rows. Returns the token's user id, or errInvalidToken for invalid/expired/used.
func (r *Repository) ConsumePasswordReset(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var userID string
	err := r.pool.QueryRow(ctx, `
		UPDATE portal_password_resets
		SET consumed_at = $2
		WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > $2
		RETURNING portal_user_id::text`, tokenHash, now).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errInvalidToken
		}
		return "", err
	}
	return userID, nil
}

// RevokeSessions consumes the user's outstanding password-reset tokens after a credential change (it
// revokes no session: HH-197). The
// contractor portal uses stateless 12h JWTs with no server-side session table, so "revoke"
// here invalidates the user's outstanding password-reset tokens, closing the window where a
// leaked reset link could be replayed after the password already changed. Live JWT
// invalidation itself is bounded by the token TTL (documented limitation). Best-effort: a
// no-op (zero rows) is success.
func (r *Repository) RevokeSessions(ctx context.Context, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE portal_password_resets SET consumed_at = now() WHERE portal_user_id = $1 AND consumed_at IS NULL`,
		userID)
	return err
}

// emitOutbox writes one event row inside the given tx (transactional outbox pattern): the
// business change and its delivery signal commit together or roll back together, so an
// invite/reset/contact can never exist without its notification, nor a notification fire for
// a change that rolled back.
func emitOutbox(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", topic, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ($1, $2)`, topic, b); err != nil {
		return fmt.Errorf("emit %s: %w", topic, err)
	}
	return nil
}

// CreateInvite stores an invite (hash only) and emits the invite-delivery signal to the
// outbox in the same transaction, then returns the invite with the raw token attached (shown
// once to the admin, the deferred-email accept-link, D-026).
func (r *Repository) CreateInvite(ctx context.Context, customerID, email, role, rawToken, tokenHash, createdBy string, expiresAt time.Time) (Invite, error) {
	if r.pool == nil {
		return Invite{}, errNoDB
	}
	var createdByArg any
	if createdBy != "" {
		createdByArg = createdBy
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Invite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inv Invite
	err = tx.QueryRow(ctx, `
		INSERT INTO portal_invites (customer_id, email, role, token_hash, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, customer_id::text, email::text, role, expires_at, created_at`,
		customerID, email, role, tokenHash, expiresAt, createdByArg).Scan(
		&inv.ID, &inv.CustomerID, &inv.Email, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return Invite{}, fmt.Errorf("create invite: %w", err)
	}

	if err := emitOutbox(ctx, tx, TopicInvite, InviteNotice{
		Email: inv.Email, Role: inv.Role, Token: rawToken, ExpiresAt: inv.ExpiresAt,
	}); err != nil {
		return Invite{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Invite{}, err
	}
	inv.Token = rawToken // returned once; never persisted in the clear
	return inv, nil
}

// ListInvites returns pending (unaccepted, unexpired) invites for a customer.
func (r *Repository) ListInvites(ctx context.Context, customerID string) ([]Invite, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, customer_id::text, email::text, role, expires_at, created_at
		FROM portal_invites
		WHERE customer_id = $1 AND accepted_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var inv Invite
		if err := rows.Scan(&inv.ID, &inv.CustomerID, &inv.Email, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// AcceptInvite consumes a valid invite and provisions the user, emitting
// crm.NewPortalContact to the events outbox. Three phases: (1) cheap validity
// check, NO bcrypt for a bogus/expired/used token (anti-DoS); (2) hash the
// password only once the invite is known real, done OUTSIDE the tx so no row
// lock is held during the ~80ms bcrypt; (3) atomically consume + provision, with
// a conditional UPDATE that closes the double-accept race. Invalid/expired/used
// all return errInvalidToken (no enumeration); a duplicate email → errEmailTaken.
func (r *Repository) AcceptInvite(ctx context.Context, tokenHash, name string, hashPassword func() (string, error), now time.Time) (User, error) {
	if r.pool == nil {
		return User{}, errNoDB
	}

	// Phase 1, validity check, no bcrypt, no lock.
	var inviteID, customerID, email, role string
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, customer_id::text, email::text, role
		FROM portal_invites
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > $2`,
		tokenHash, now).Scan(&inviteID, &customerID, &email, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, errInvalidToken
		}
		return User{}, err
	}

	// Phase 2, hash now that the invite is real (bounded work, no lock held).
	passwordHash, err := hashPassword()
	if err != nil {
		return User{}, err
	}

	// Phase 3, atomically consume + provision + emit.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ct, err := tx.Exec(ctx,
		`UPDATE portal_invites SET accepted_at = $2 WHERE id = $1 AND accepted_at IS NULL AND expires_at > $2`,
		inviteID, now)
	if err != nil {
		return User{}, err
	}
	if ct.RowsAffected() == 0 {
		return User{}, errInvalidToken // raced/expired between phase 1 and now
	}

	u, err := scanUserPublic(tx.QueryRow(ctx, `
		INSERT INTO portal_users (customer_id, email, name, password_hash, role, status)
		VALUES ($1, $2, $3, $4, $5, 'active')
		RETURNING `+userColsPublic, customerID, email, name, passwordHash, role))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation (email)
			return User{}, errEmailTaken
		}
		return User{}, fmt.Errorf("provision user: %w", err)
	}

	if err := emitOutbox(ctx, tx, TopicNewPortalContact, NewPortalContact{
		CustomerID: u.CustomerID, UserID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role,
	}); err != nil {
		return User{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return u, nil
}
