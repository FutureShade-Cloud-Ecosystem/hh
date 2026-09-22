package assist

// repository.go, the Postgres side of migrations 0153/0154/0155.
//
// One type implements all four stores (PolicyStore, SettingStore, LedgerStore, LimitStore) because
// they are four views of one small config-and-ledger surface owned by this package, and splitting
// them across four structs would produce four identical constructors and one more thing to wire in
// main.go. The INTERFACES stay separate, that is where the seams that matter are, so a test can
// still fake exactly one of them.
//
// NOTHING HERE READS ANOTHER DOMAIN'S TABLES. The only tables this file touches are the three from
// this ticket's migrations, plus users/user_roles/roles in ONE query: the admin spend list, which
// has to name the people it is reporting on. That query is read-only, admin-gated at the route, and
// deliberately the only place this package looks outside its own schema, every business read goes
// through the consumer interfaces instead.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the DB-backed implementation of this package's four stores.
type Repository struct{ pool *pgxpool.Pool }

// NewRepository builds the store over an existing pool.
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Compile-time proof that the one struct really does satisfy all four seams. Cheaper than
// discovering a missing method in main.go.
var (
	_ PolicyStore  = (*Repository)(nil)
	_ SettingStore = (*Repository)(nil)
	_ LedgerStore  = (*Repository)(nil)
	_ LimitStore   = (*Repository)(nil)
)

// -- assist_command_policy (0153) ---------------------------------------------------------------

// CommandPolicy is every command's admin policy: the rows 0153 seeded, over a DEFAULT for every
// command the code declares.
//
// WHY THE DEFAULTS ARE HERE AND NOT IN A MIGRATION (DIB-478). 0153 seeded a row per shipped command
// by hand. That works exactly once: DIB-478 added eleven commands and every one of them was missing
// from this map, so the admin surface, which lists what it finds here, silently did not show them.
// A command an admin cannot see is a command an admin cannot switch off, which is the one thing the
// config table exists for.
//
// Repeating the hand-seed in a new migration would buy the same bug for whoever adds the twelfth
// command. The command SET is a fact about the Go code (BuiltinCommands), so the code is what
// completes the map, at each command's OWN declared floor, which is what a missing row already
// meant to Registry.Run. Nothing here widens anything: a real row always wins over its default, and
// the registry still intersects policy with the code floor and takes the tighter of the pair.
//
// SetCommandPolicy's upsert then turns a default into a stored row the first time an admin touches
// it, so nothing needs backfilling and no migration has to know the command list.
func (r *Repository) CommandPolicy(ctx context.Context) (map[string]Policy, error) {
	out := map[string]Policy{}
	for _, c := range BuiltinCommands() {
		out[c.Code] = Policy{Enabled: true, MaxTier: c.MaxTier}
	}
	rows, err := r.pool.Query(ctx, `SELECT command_code, enabled, max_tier FROM assist_command_policy`)
	if err != nil {
		return nil, fmt.Errorf("assist: read command policy: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var p Policy
		if err := rows.Scan(&code, &p.Enabled, &p.MaxTier); err != nil {
			return nil, fmt.Errorf("assist: scan command policy: %w", err)
		}
		out[code] = p // a STORED row always wins over its default
	}
	return out, rows.Err()
}

// SetCommandPolicy is the admin write. It INSERTs when the row is missing so a command added by a
// later release can be configured before anyone remembers to seed it.
func (r *Repository) SetCommandPolicy(ctx context.Context, code string, p Policy, actorID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO assist_command_policy (command_code, enabled, max_tier, updated_by, updated_at)
		VALUES ($1, $2, $3, NULLIF($4,'')::uuid, now())
		ON CONFLICT (command_code) DO UPDATE
		   SET enabled = EXCLUDED.enabled,
		       max_tier = EXCLUDED.max_tier,
		       updated_by = EXCLUDED.updated_by,
		       updated_at = now()`,
		code, p.Enabled, p.MaxTier, actorID)
	if err != nil {
		return fmt.Errorf("assist: write command policy: %w", err)
	}
	return nil
}

// -- assist_settings (0153) ---------------------------------------------------------------------

func (r *Repository) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT key, value FROM assist_settings`)
	if err != nil {
		return nil, fmt.Errorf("assist: read settings: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("assist: scan settings: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (r *Repository) SetSetting(ctx context.Context, key, value, actorID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO assist_settings (key, value, updated_by, updated_at)
		VALUES ($1, $2, NULLIF($3,'')::uuid, now())
		ON CONFLICT (key) DO UPDATE
		   SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		key, value, actorID)
	if err != nil {
		return fmt.Errorf("assist: write setting %q: %w", key, err)
	}
	return nil
}

// -- assist_token_ledger (0154) -----------------------------------------------------------------

// Append writes one exchange. INSERT only, there is no update path in this file, and the trigger
// in 0154 would refuse one anyway.
func (r *Repository) Append(ctx context.Context, e Entry) error {
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}
	kind := e.Kind
	if kind == "" {
		kind = KindChat
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO assist_token_ledger (user_id, role_code, provider, model, tokens_in, tokens_out, kind, at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)`,
		e.UserID, e.RoleCode, e.Provider, e.Model, e.TokensIn, e.TokensOut, kind, at)
	if err != nil {
		return fmt.Errorf("assist: append ledger: %w", err)
	}
	return nil
}

// SpendFor sums one user's window. COALESCE so a user with no rows reads as a zero Spend rather
// than as a NULL scan error, "has never used it" is the common case, not an exception.
func (r *Repository) SpendFor(ctx context.Context, userID string, from, to time.Time) (Spend, error) {
	var s Spend
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0)
		  FROM assist_token_ledger
		 WHERE user_id = $1::uuid AND at >= $2 AND at < $3`,
		userID, from, to).Scan(&s.Exchanges, &s.TokensIn, &s.TokensOut)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Spend{}, nil
		}
		return Spend{}, fmt.Errorf("assist: read spend: %w", err)
	}
	return s, nil
}

func (r *Repository) SpendAll(ctx context.Context, from, to time.Time) (map[string]Spend, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id::text, COUNT(*), COALESCE(SUM(tokens_in),0), COALESCE(SUM(tokens_out),0)
		  FROM assist_token_ledger
		 WHERE at >= $1 AND at < $2
		 GROUP BY user_id`, from, to)
	if err != nil {
		return nil, fmt.Errorf("assist: read all spend: %w", err)
	}
	defer rows.Close()
	out := map[string]Spend{}
	for rows.Next() {
		var id string
		var s Spend
		if err := rows.Scan(&id, &s.Exchanges, &s.TokensIn, &s.TokensOut); err != nil {
			return nil, fmt.Errorf("assist: scan spend: %w", err)
		}
		out[id] = s
	}
	return out, rows.Err()
}

// -- assist_role_limits / assist_user_limits (0155) ---------------------------------------------
//
// The two single-row reads return *int so ABSENT survives the trip to Resolve. A zero budget is a
// legal, meaningful value ("no AI for this role"), so it must not share a representation with
// "there is no row".

func (r *Repository) RoleBudget(ctx context.Context, roleCode string) (*int, error) {
	var v int
	err := r.pool.QueryRow(ctx, `SELECT monthly_token_budget FROM assist_role_limits WHERE role_code = $1`, roleCode).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assist: read role budget: %w", err)
	}
	return &v, nil
}

func (r *Repository) UserBudget(ctx context.Context, userID string) (*int, error) {
	var v int
	err := r.pool.QueryRow(ctx, `SELECT monthly_token_budget FROM assist_user_limits WHERE user_id = $1::uuid`, userID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assist: read user budget: %w", err)
	}
	return &v, nil
}

func (r *Repository) RoleBudgets(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT role_code::text, monthly_token_budget FROM assist_role_limits`)
	if err != nil {
		return nil, fmt.Errorf("assist: read role budgets: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("assist: scan role budgets: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (r *Repository) UserBudgets(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT user_id::text, monthly_token_budget FROM assist_user_limits`)
	if err != nil {
		return nil, fmt.Errorf("assist: read user budgets: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("assist: scan user budgets: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (r *Repository) SetRoleBudget(ctx context.Context, roleCode string, tokens int, actorID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO assist_role_limits (role_code, monthly_token_budget, updated_by, updated_at)
		VALUES ($1, $2, NULLIF($3,'')::uuid, now())
		ON CONFLICT (role_code) DO UPDATE
		   SET monthly_token_budget = EXCLUDED.monthly_token_budget,
		       updated_by = EXCLUDED.updated_by, updated_at = now()`,
		roleCode, tokens, actorID)
	if err != nil {
		return fmt.Errorf("assist: write role budget: %w", err)
	}
	return nil
}

// SetUserBudget writes the WHOLE override. There is no partial update because there is no partial
// override, see limits.go.
func (r *Repository) SetUserBudget(ctx context.Context, userID string, tokens int, actorID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO assist_user_limits (user_id, monthly_token_budget, updated_by, updated_at)
		VALUES ($1::uuid, $2, NULLIF($3,'')::uuid, now())
		ON CONFLICT (user_id) DO UPDATE
		   SET monthly_token_budget = EXCLUDED.monthly_token_budget,
		       updated_by = EXCLUDED.updated_by, updated_at = now()`,
		userID, tokens, actorID)
	if err != nil {
		return fmt.Errorf("assist: write user budget: %w", err)
	}
	return nil
}

// ClearUserBudget IS "reset to role": the override row is deleted and the role default applies
// again. Deleting rather than writing the role's current number matters, a reset user must FOLLOW
// the role default when an admin later changes it, which a copied number would not.
func (r *Repository) ClearUserBudget(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM assist_user_limits WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return fmt.Errorf("assist: clear user budget: %w", err)
	}
	return nil
}

// -- the admin spend list -----------------------------------------------------------------------

// StaffRow is one person on the admin surface, before spend and budget are folded in.
type StaffRow struct {
	UserID      string
	DisplayName string
	Email       string
	RoleCode    string
}

// Staff lists the ACTIVE staff the spend surface reports on.
//
// The ONE query in this package that reads outside its own schema, and it reads only what it must
// print: a name, an email and a role. Restricting to ACTIVE keeps the surface about people who can
// actually use the assistant; a deactivated account's history remains in the ledger and is reachable
// by anyone querying it directly, which is the correct place for that question.
func (r *Repository) Staff(ctx context.Context) ([]StaffRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id::text, u.display_name, u.email::text, COALESCE(rl.code::text, '')
		  FROM users u
		  LEFT JOIN user_roles ur ON ur.user_id = u.id
		  LEFT JOIN roles rl ON rl.id = ur.role_id
		 WHERE u.status = 'ACTIVE'
		 ORDER BY u.display_name`)
	if err != nil {
		return nil, fmt.Errorf("assist: read staff: %w", err)
	}
	defer rows.Close()
	var out []StaffRow
	for rows.Next() {
		var s StaffRow
		if err := rows.Scan(&s.UserID, &s.DisplayName, &s.Email, &s.RoleCode); err != nil {
			return nil, fmt.Errorf("assist: scan staff: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
