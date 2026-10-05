// syncscope.go is the portal module's sync surface (HH-3.5 #169): the
// team Store over portal_users (manual policy), the table this module
// owns. The payment_methods pull-only Store lives in the payments
// module, which owns portal_payment_methods (the boundary model.go
// there records); the fence rules hold: the sync core owns the
// protocol and no table, this module owns its table and meets the core
// at the Store seam.
//
// team, manual: a role change must never silently win (C31), so a stale
// offline role or status edit meets the engine's conflict arm, applies
// nothing, and returns the server row with the review marker; a fresh
// write passes the module's own guards, the last-admin rule first among
// them ("never lose the last owner", the catalog's own words). The
// projection is the module's public User shape: the password hash is
// never in any projection, on any path, under any policy.
//
// payment_methods, pull-only (Colton's START ruling on undeclared
// scopes): the cache carries the display metadata a payer reads
// (method type, brand, last4, expiry, default flag, status); the vault
// token is as absent from the projection as the password hash is from
// team's, because the token is the credential the vault issued and no
// device ever needs it to render a card row.
package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// teamSeqScope names the scope_sequences counter row the team writes
// draw from (the manifest's team scope; the repository's own role and
// status updates draw the same counter through the CTE).
const teamSeqScope = "team"

// allocateTeamSeqTx draws the next team seq inside the caller's
// transaction.
func allocateTeamSeqTx(ctx context.Context, tx pgx.Tx) (int64, error) {
	var next int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ($1)
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`, teamSeqScope).Scan(&next); err != nil {
		return 0, fmt.Errorf("portal: seq allocate for scope %s: %w", teamSeqScope, err)
	}
	return next, nil
}

// tenantPredicate is the mandatory scoping predicate: the tenant from
// the middleware-stamped filter (G4). portal_users rows carry
// customer_id as the tenant anchor and tenant_id as the same fact (the
// 0213 equality ADR-0020 records), so both columns predicate the one
// tenant; the org plays no part in the row-level fence (alpha and beta
// share an org and must never see each other's members).
func tenantPredicate(f sync.ScopeFilter) (string, bool) {
	if !f.Tenant || f.TenantID == "" || f.OrgID == "" {
		return "", false
	}
	return "customer_id = $1::uuid AND tenant_id = $1::uuid", true
}

// looksLikeUUID is the shared boundary guard.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// teamColumns is the team projection: the module's public User legs
// plus the sync legs. No password hash, anywhere.
const teamColumns = userColsPublic + `, COALESCE(seq, 0)::bigint AS seq, (deleted_at IS NOT NULL) AS deleted`

// teamRow is one scanned row plus its sync legs.
type teamRow struct {
	user    User
	prefs   []byte
	seq     int64
	deleted bool
}

// scanTeam reads the 11-target row over scanUserPublic's nine.
func scanTeam(row interface{ Scan(dest ...any) error }) (teamRow, error) {
	var r teamRow
	if err := row.Scan(&r.user.ID, &r.user.CustomerID, &r.user.Email, &r.user.Name, &r.user.Role,
		&r.user.Status, &r.prefs, &r.user.CreatedAt, &r.user.UpdatedAt, &r.seq, &r.deleted); err != nil {
		return teamRow{}, err
	}
	r.user.NotificationPrefs = json.RawMessage(r.prefs)
	return r, nil
}

// teamData projects one row through the module's own json tags.
func teamData(u User) (map[string]any, error) {
	raw, err := json.Marshal(u)
	if err != nil {
		return nil, fmt.Errorf("portal: team projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("portal: team projection decode: %w", err)
	}
	return data, nil
}

// teamRowStateOf folds one scanned row into the engine's RowState.
func teamRowStateOf(r teamRow) (sync.RowState, error) {
	rs := sync.RowState{ID: r.user.ID, Seq: r.seq, Deleted: r.deleted}
	if r.deleted {
		return rs, nil
	}
	data, err := teamData(r.user)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Data = data
	return rs, nil
}

// TeamSyncStore is the team Store over the portal_users table.
type TeamSyncStore struct {
	pool *pgxpool.Pool
}

// NewTeamSyncStore wires the adapter.
func NewTeamSyncStore(pool *pgxpool.Pool) *TeamSyncStore {
	return &TeamSyncStore{pool: pool}
}

// Pull returns the page of team-member changes past since under the
// tenant predicate.
func (s *TeamSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	where, ok := tenantPredicate(f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM portal_users
		 WHERE %s AND seq IS NOT NULL AND seq > $2
		 ORDER BY seq ASC LIMIT $3`, teamColumns, where),
		f.TenantID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("portal: team pull: %w", err)
	}
	defer rows.Close()
	items := []sync.Entity{}
	tombstones := []sync.Tombstone{}
	for rows.Next() {
		r, err := scanTeam(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("portal: team pull scan: %w", err)
		}
		if r.deleted {
			tombstones = append(tombstones, sync.Tombstone{ID: r.user.ID, Seq: r.seq})
			continue
		}
		data, err := teamData(r.user)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: r.user.ID, Seq: r.seq, Data: data})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("portal: team pull: %w", err)
	}
	return items, tombstones, nil
}

// FindApplied resolves one idempotency key to the member row it
// applied: the row's single D30 column.
func (s *TeamSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	where, ok := tenantPredicate(f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	r, err := scanTeam(s.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM portal_users WHERE %s AND idempotency_key = $2::uuid LIMIT 1`,
		teamColumns, where), f.TenantID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("portal: team find applied: %w", err)
	}
	rs, err := teamRowStateOf(r)
	return rs, true, err
}

// Load reads one member by id under the tenant predicate; a row outside
// the caller's tenant is the same as an absent one.
func (s *TeamSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	return s.load(ctx, s.pool, f, id)
}

// teamQuerier is the read surface a pool and a transaction share, so one
// loader body serves the public Load (the pool) and the callers that
// already hold a transaction (their own connection).
type teamQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// load is Load over a querier. A caller inside a transaction passes the
// transaction, so the read rides the connection it already holds instead
// of asking the shared pool for a second one (connection starvation by
// second acquisition when pool-size callers do this together).
func (s *TeamSyncStore) load(ctx context.Context, db teamQuerier, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	where, ok := tenantPredicate(f)
	if !ok || !looksLikeUUID(id) {
		return sync.RowState{}, false, nil
	}
	r, err := scanTeam(db.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM portal_users WHERE id = $2::uuid AND %s`,
		teamColumns, where), f.TenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, false, nil
	}
	if err != nil {
		return sync.RowState{}, false, fmt.Errorf("portal: team load: %w", err)
	}
	rs, err := teamRowStateOf(r)
	return rs, true, err
}

// teamRoles is the 0160 CHECK set, the whole capability span a member
// may hold.
var teamRoles = map[string]bool{
	"account_admin": true, "buyer": true, "buyer_no_pay": true,
	"ap_finance": true, "field_crew": true,
}

// teamWritable is the field set a pushed write may declare: the row
// address, the role, the display name and the status. Email is the
// member's identity key and refuses (the invite flow owns it); the
// password and the platform link are outside sync entirely.
var teamWritable = map[string]bool{
	"id": true, "role": true, "name": true, "status": true,
}

// teamCreateWritable is the create set: the invite legs. The birth
// status is pending (the member completes the invite online; the login
// secret never crosses sync), the tenant stamps from the filter.
var teamCreateWritable = map[string]bool{
	"id": true, "email": true, "name": true, "role": true,
}

// Create births one pending member from a pushed create: the email is
// unique across the whole table (a collision is the caller's own
// defect, refused as row-exists), the role validates against the CHECK
// set, and the row carries no login secret and no platform link until
// the online invite completes.
func (s *TeamSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	if _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	for k := range fields {
		if !teamCreateWritable[k] {
			return sync.RowState{}, fmt.Errorf("portal: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	email, _ := fields["email"].(string)
	if email == "" {
		return sync.RowState{}, fmt.Errorf("portal: email is required: %w", sync.ErrOpRefused)
	}
	name, _ := fields["name"].(string)
	role, _ := fields["role"].(string)
	if !teamRoles[role] {
		return sync.RowState{}, fmt.Errorf("portal: unknown role %q: %w", role, sync.ErrOpRefused)
	}
	id, hasID := fields["id"].(string)
	if hasID && !looksLikeUUID(id) {
		return sync.RowState{}, fmt.Errorf("portal: payload id must be a uuid: %w", sync.ErrOpRefused)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("portal: begin team create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := allocateTeamSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanTeam(tx.QueryRow(ctx, `
		INSERT INTO portal_users (id, customer_id, email, name, password_hash, role, status,
		                          tenant_id, seq, idempotency_key)
		VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), $2::uuid, $3, $4, '', $5, 'pending',
		        $6::uuid, $7, $8::uuid)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+teamColumns,
		id, f.TenantID, email, name, role, f.TenantID, seq, key))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, own, _ := s.load(ctx, tx, f, id); own {
			return sync.RowState{}, sync.ErrRowExists
		}
		return sync.RowState{}, sync.ErrRowNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The email's unique index fired: the address is taken, in this
		// tenant or any other; the refusal is one sentence either way so
		// the constraint leaks no existence.
		return sync.RowState{}, sync.ErrRowExists
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("portal: team create insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("portal: commit team create: %w", err)
	}
	return teamRowStateOf(r)
}

// Write applies the declared legs to one member under the module's own
// guards: the last-admin rule (a change that would leave the tenant
// with no active account_admin refuses), the role CHECK set, and the
// status CHECK set. A no-op write (every declared leg already equal)
// draws no seq.
func (s *TeamSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	if _, ok := tenantPredicate(f); !ok {
		return sync.RowState{}, sync.ErrOpRefused
	}
	for k := range fields {
		if !teamWritable[k] {
			return sync.RowState{}, fmt.Errorf("portal: field %q is not writable through sync: %w", k, sync.ErrOpRefused)
		}
	}
	role, hasRole := fields["role"].(string)
	if hasRole && !teamRoles[role] {
		return sync.RowState{}, fmt.Errorf("portal: unknown role %q: %w", role, sync.ErrOpRefused)
	}
	status, hasStatus := fields["status"].(string)
	if hasStatus && status != "active" && status != "inactive" {
		return sync.RowState{}, fmt.Errorf("portal: unknown status %q: %w", status, sync.ErrOpRefused)
	}
	name, _ := fields["name"].(string)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("portal: begin team write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	before, err := scanTeam(tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM portal_users WHERE id = $1::uuid AND customer_id = $2::uuid AND tenant_id = $2::uuid AND deleted_at IS NULL FOR UPDATE`,
		teamColumns), id, f.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("portal: lock member %q for sync write: %w", id, err)
	}
	// The last-admin guard, the module's own M-1 rule: a role or status
	// change on an active account_admin requires another active admin to
	// remain in the tenant.
	if (hasRole && role != before.user.Role) || (hasStatus && status != before.user.Status) {
		if before.user.Role == "account_admin" && before.user.Status == "active" {
			var others int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM portal_users
				WHERE customer_id = $1 AND role = 'account_admin' AND status = 'active'
				  AND id <> $2 AND deleted_at IS NULL`,
				f.TenantID, id).Scan(&others); err != nil {
				return sync.RowState{}, fmt.Errorf("portal: last admin count: %w", err)
			}
			if others == 0 {
				return sync.RowState{}, fmt.Errorf("portal: the last owner cannot be demoted or deactivated: %w", sync.ErrOpRefused)
			}
		}
	}
	if (!hasRole || role == before.user.Role) && (!hasStatus || status == before.user.Status) && (name == "" || name == before.user.Name) {
		if err := tx.Commit(ctx); err != nil {
			return sync.RowState{}, fmt.Errorf("portal: commit team no-op: %w", err)
		}
		return teamRowStateOf(before)
	}
	seq, err := allocateTeamSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanTeam(tx.QueryRow(ctx, `
		UPDATE portal_users SET
			role       = COALESCE(NULLIF($2,''), role),
			status     = COALESCE(NULLIF($3,''), status),
			name       = COALESCE(NULLIF($4,''), name),
			updated_at = now(),
			seq        = $5,
			idempotency_key = $6::uuid
		WHERE id = $1::uuid
		RETURNING `+teamColumns,
		id, role, status, name, seq, key))
	if err != nil {
		return sync.RowState{}, fmt.Errorf("portal: team write %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("portal: commit team write: %w", err)
	}
	return teamRowStateOf(r)
}

// Delete tombstones one member and deactivates the row: the tombstone
// is the sync fact, the status the business one (a tombstoned member
// must not render on any surface that reads status alone), and both
// land with one fresh seq.
func (s *TeamSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	_ = key
	if _, ok := tenantPredicate(f); !ok || !looksLikeUUID(id) {
		return sync.RowState{}, sync.ErrOpRefused
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sync.RowState{}, fmt.Errorf("portal: begin team delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The last-admin guard runs on a delete too.
	before, err := scanTeam(tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT %s FROM portal_users WHERE id = $1::uuid AND customer_id = $2::uuid AND tenant_id = $2::uuid AND deleted_at IS NULL FOR UPDATE`,
		teamColumns), id, f.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("portal: lock member %q for sync delete: %w", id, err)
	}
	if before.user.Role == "account_admin" && before.user.Status == "active" {
		var others int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM portal_users
			WHERE customer_id = $1 AND role = 'account_admin' AND status = 'active'
			  AND id <> $2 AND deleted_at IS NULL`,
			f.TenantID, id).Scan(&others); err != nil {
			return sync.RowState{}, fmt.Errorf("portal: last admin count: %w", err)
		}
		if others == 0 {
			return sync.RowState{}, fmt.Errorf("portal: the last owner cannot be removed: %w", sync.ErrOpRefused)
		}
	}
	seq, err := allocateTeamSeqTx(ctx, tx)
	if err != nil {
		return sync.RowState{}, err
	}
	r, err := scanTeam(tx.QueryRow(ctx, `
		UPDATE portal_users SET
			deleted_at = now(), status = 'inactive', updated_at = now(), seq = $3
		WHERE id = $1::uuid AND customer_id = $2::uuid AND tenant_id = $2::uuid AND deleted_at IS NULL
		RETURNING `+teamColumns, id, f.TenantID, seq))
	if errors.Is(err, pgx.ErrNoRows) {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if err != nil {
		return sync.RowState{}, fmt.Errorf("portal: team delete %q: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sync.RowState{}, fmt.Errorf("portal: commit team delete: %w", err)
	}
	rs, err := teamRowStateOf(r)
	if err != nil {
		return sync.RowState{}, err
	}
	rs.Deleted = true
	return rs, nil
}
