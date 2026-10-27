package customer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/likeutil"
)

var errNoDB = errors.New("customer: database not configured")

// isUniqueViolation reports whether err is a Postgres unique_violation (23505), the signal
// that a UNIQUE constraint (here: customers.account_no CITEXT UNIQUE, migration 0006) was
// hit. Mirrors the identity/till pattern so a duplicate surfaces as a clean 4xx, not a 500.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// accountColumns is the canonical SELECT list for an Account. home_branch_id is
// nullable (locations FK with ON DELETE SET NULL), so it is COALESCEd to an empty
// string to map an unset home branch to a blank Account.BranchID. BalanceCents is
// NOT selected here: it derives from the billing module and is composed in by the BFF.
const accountColumns = `id::text, account_no::text, name, customer_type,
	COALESCE(home_branch_id::text, ''), payment_terms_days, credit_limit_cents,
	on_hold, tax_exempt, is_active`

// contactColumns is the canonical SELECT list for a Contact. email is nullable
// (CITEXT, non-unique), so it is COALESCEd to an empty string to map a missing
// email to a blank Contact.Email.
const contactColumns = `id::text, customer_id::text, name, COALESCE(email::text, ''),
	phone, role, is_primary, is_active, created_at, updated_at`

// Repository owns reads of customers + customer_contacts (pgx). Zero cross-module
// access, pricing/billing/portal read account facts THROUGH this seam.
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// CustomerHomeBranch answers the account's home branch ("" when the account carries none) and whether
// the account exists: the one fact another module needs to hold the account to an org, through the
// branches module's own org read. The module's own table, read only.
func (r *Repository) CustomerHomeBranch(ctx context.Context, customerID string) (string, bool, error) {
	if r.pool == nil {
		return "", false, errNoDB
	}
	var home string
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(home_branch_id::text, '') FROM customers WHERE id = $1::uuid`, customerID).Scan(&home)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("customer: read the account's home branch: %w", err)
	}
	return home, true, nil
}

func scanAccount(row interface {
	Scan(dest ...any) error
}) (Account, error) {
	var a Account
	// BalanceCents is intentionally left at its zero value (composed by the BFF).
	if err := row.Scan(
		&a.CustomerID, &a.AccountNo, &a.Name, &a.CustomerType,
		&a.BranchID, &a.PaymentTermsDays, &a.CreditLimitCents,
		&a.OnHold, &a.TaxExempt, &a.IsActive,
	); err != nil {
		return Account{}, err
	}
	return a, nil
}

func scanContact(row interface {
	Scan(dest ...any) error
}) (Contact, error) {
	var c Contact
	if err := row.Scan(
		&c.ID, &c.CustomerID, &c.Name, &c.Email,
		&c.Phone, &c.Role, &c.IsPrimary, &c.IsActive,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return Contact{}, err
	}
	return c, nil
}

// ResolveAccount loads the account facts for one customer id. Account.BalanceCents
// is left at 0 here (the BFF composes it from billing.OpenBalanceCents); this module
// does not read customer_transactions.
func (r *Repository) ResolveAccount(ctx context.Context, customerID string) (Account, error) {
	if r.pool == nil {
		return Account{}, errNoDB
	}
	q := fmt.Sprintf("SELECT %s FROM customers WHERE id = $1", accountColumns)
	a, err := scanAccount(r.pool.QueryRow(ctx, q, customerID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("resolve account %q: %w", customerID, err)
	}
	return a, nil
}

// SearchAccounts finds active accounts by name or account_no (case-insensitive, contains)
// for the POS customer picker. The WALK-IN house account is EXCLUDED, it is the implicit
// no-account default, not a chargeable account. OpenBalanceCents reuses the POS credit
// gate's running-balance basis (the latest customer_transactions.balance_after, 0009);
// this module does not invent a new AR calc. q is passed as a parameter ($1 wrapped in
// %…% below), never string-interpolated into SQL. limit is capped by the service.
func (r *Repository) SearchAccounts(ctx context.Context, q string, limit int) ([]SearchResult, error) {
	// The unfenced entry point is retained for the accountStore interface, which is implemented
	// outside this package. It delegates to the fenced query with an empty scope, so there is exactly
	// ONE search SQL statement in this module and no chance of the two drifting apart.
	return r.SearchAccountsScoped(ctx, q, limit, SearchScope{Global: true})
}

// SearchAccountsScoped is the U-park E4 branch-fenced search (see search_scope.go).
func (r *Repository) SearchAccountsScoped(ctx context.Context, q string, limit int, scope SearchScope) ([]SearchResult, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// U-park E4 branch fence. $3 is NULL for an unfenced (GLOBAL, no branch named) search, in which
	// case the predicate short-circuits true and the query is byte-for-byte what it was before. When a
	// branch IS named the fence admits accounts homed to it PLUS accounts with no home branch, which
	// are company-wide by construction (see search_scope.go).
	const query = `
SELECT c.id::text, c.name, c.account_no::text, c.customer_type,
       c.payment_terms_days, c.credit_limit_cents,
       COALESCE((SELECT t.balance_after FROM customer_transactions t
                  WHERE t.customer_id = c.id ORDER BY t.occurred_at DESC LIMIT 1), 0)
  FROM customers c
 WHERE c.is_active
   AND (c.name ILIKE '%' || $1 || '%' ESCAPE '\' OR c.account_no::text ILIKE '%' || $1 || '%' ESCAPE '\')
   AND c.account_no <> 'WALK-IN'
   AND c.name NOT ILIKE 'walk-in%'
   AND ($3::uuid IS NULL OR c.home_branch_id = $3::uuid OR c.home_branch_id IS NULL)
 ORDER BY c.name
 LIMIT $2`
	// $1 is a pattern here, so it is bound escaped: a typed '%' or '_' matches text, never a wildcard.
	rows, err := r.pool.Query(ctx, query, likeutil.EscapeLikePattern(q), limit, nullUUID(scope.BranchID))
	if err != nil {
		return nil, fmt.Errorf("search accounts %q: %w", q, err)
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var s SearchResult
		if err := rows.Scan(
			&s.ID, &s.Name, &s.AccountNo, &s.CustomerType,
			&s.PaymentTermsDays, &s.CreditLimitCents, &s.OpenBalanceCents,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListContacts returns every contact for the account, primary first.
func (r *Repository) ListContacts(ctx context.Context, customerID string) ([]Contact, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := fmt.Sprintf("SELECT %s FROM customer_contacts WHERE customer_id = $1 ORDER BY is_primary DESC, name", contactColumns)
	rows, err := r.pool.Query(ctx, q, customerID)
	if err != nil {
		return nil, fmt.Errorf("list contacts for %q: %w", customerID, err)
	}
	defer rows.Close()

	var out []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetPrimaryContact returns the account's is_primary, is_active contact. pgx.ErrNoRows
// maps to ErrNotFound (no primary contact to seed an admin from).
func (r *Repository) GetPrimaryContact(ctx context.Context, customerID string) (Contact, error) {
	if r.pool == nil {
		return Contact{}, errNoDB
	}
	q := fmt.Sprintf("SELECT %s FROM customer_contacts WHERE customer_id = $1 AND is_primary AND is_active ORDER BY name LIMIT 1", contactColumns)
	c, err := scanContact(r.pool.QueryRow(ctx, q, customerID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Contact{}, ErrNotFound
		}
		return Contact{}, fmt.Errorf("get primary contact for %q: %w", customerID, err)
	}
	return c, nil
}

// -- write path (M5 / A-5) ----------------------------------------------------
// The service has already validated + normalized billing_address (postal/province for the dealer's country)
// before these run; the repository just persists. billing_address is stored as JSONB.

// InsertCustomer inserts a new customer and returns the resolved Account. home_branch_id ""
// is stored as NULL. The service validated the payload (incl. the address) beforehand.
func (r *Repository) InsertCustomer(ctx context.Context, in CreateInput) (Account, error) {
	if r.pool == nil {
		return Account{}, errNoDB
	}
	addr, err := json.Marshal(orEmptyObject(in.BillingAddress))
	if err != nil {
		return Account{}, fmt.Errorf("encode billing_address: %w", err)
	}
	var homeBranch any
	if in.HomeBranchID != "" {
		homeBranch = in.HomeBranchID
	}
	var id string
	err = r.pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id,
		                       payment_terms_days, credit_limit_cents, tax_exempt, billing_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text`,
		in.AccountNo, in.Name, in.CustomerType, homeBranch,
		in.PaymentTermsDays, in.CreditLimitCents, in.TaxExempt, addr).Scan(&id)
	if err != nil {
		// account_no is CITEXT NOT NULL UNIQUE (migration 0006). A collision is a client
		// error (the account number is already taken), not a server fault: return the
		// sentinel so the handler maps it to 409, not a leaked 500.
		if isUniqueViolation(err) {
			return Account{}, ErrDuplicateAccountNo
		}
		return Account{}, fmt.Errorf("insert customer: %w", err)
	}
	return r.ResolveAccount(ctx, id)
}

// UpdateBillingAddress overwrites a customer's billing_address (already validated + normalized
// by the service) and returns the refreshed Account. ErrNotFound when the id is unknown.
func (r *Repository) UpdateBillingAddress(ctx context.Context, customerID string, addr map[string]any) (Account, error) {
	if r.pool == nil {
		return Account{}, errNoDB
	}
	b, err := json.Marshal(orEmptyObject(addr))
	if err != nil {
		return Account{}, fmt.Errorf("encode billing_address: %w", err)
	}
	// The customers counter first, without advancing it, then the row (a load batch holds the counter before its
	// item rows, LD-5; PR 556 round 3e).
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("update billing_address for %q: %w", customerID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := LockCustomersSeqTx(ctx, tx); err != nil {
		return Account{}, err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE customers SET billing_address = $1, updated_at = now() WHERE id = $2`, b, customerID)
	if err != nil {
		// A malformed (non-UUID) customerID raises SQLSTATE 22P02 on the uuid PK cast. Fold it into
		// ErrNotFound → clean 404, indistinguishable from an unknown id (the RowsAffected()==0 case
		// below), matching order/procurement's garbage-id contract.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("update billing_address for %q: %w", customerID, err)
	}
	if tag.RowsAffected() == 0 {
		return Account{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return Account{}, fmt.Errorf("update billing_address for %q: %w", customerID, err)
	}
	return r.ResolveAccount(ctx, customerID)
}

// orEmptyObject maps a nil address to a non-nil empty map so it serializes as {} (never
// null) into the JSONB column.
func orEmptyObject(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// GetAccountScoped is the pgx read behind GetAccount (account_read.go). It lives here, beside the
// search, because it reads the same running balance (customer_transactions) the allowlist states
// for this file.
func (r *Repository) GetAccountScoped(ctx context.Context, customerID string, reach AccountReach) (Account, bool, error) {
	if r.pool == nil {
		return Account{}, false, errNoDB
	}
	q := fmt.Sprintf(`SELECT %s,
	       COALESCE((SELECT t.balance_after FROM customer_transactions t
	                  WHERE t.customer_id = customers.id ORDER BY t.occurred_at DESC LIMIT 1), 0)
	  FROM customers
	 WHERE id = $1
	   AND ($2::uuid[] IS NULL OR home_branch_id = ANY($2::uuid[]) OR home_branch_id IS NULL)`, accountColumns)
	var a Account
	err := r.pool.QueryRow(ctx, q, customerID, reach.branchFilter()).Scan(
		&a.CustomerID, &a.AccountNo, &a.Name, &a.CustomerType,
		&a.BranchID, &a.PaymentTermsDays, &a.CreditLimitCents,
		&a.OnHold, &a.TaxExempt, &a.IsActive, &a.BalanceCents)
	switch {
	case err == nil:
		return a, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		return Account{}, false, nil
	default:
		return Account{}, false, fmt.Errorf("customer: get account %q: %w", customerID, err)
	}
}
