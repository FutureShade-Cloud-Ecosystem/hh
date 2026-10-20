package readtx

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Startup refusals. Each leaves the assist data path off; the rest of the app is
// unaffected. Their text is fixed and names no value.
var (
	// ErrStatementPooling: two statements in one transaction ran on different
	// backends, so a signed claim (bound to the backend pid) would never verify and
	// a per connection setting could land on another asker's connection. Session
	// and transaction pooling keep one backend for a transaction; statement
	// pooling does not and is unsupported (plan 3.2).
	ErrStatementPooling = errors.New("readtx: the assist pool sits behind statement pooling, which is unsupported; use a direct, session or transaction pooled connection")
	// ErrWrongLogin: the reader URL does not log in as the assist login of this
	// database, or logs in as a role with superuser power. The assist path must
	// never run as the app role.
	ErrWrongLogin = errors.New("readtx: the reader URL does not log in as this database's assist login")
	// ErrClaimsNotProvisioned: a claim signed with the configured key did not
	// verify in the database: the key is not in ai.claims_key (run core
	// assist-claims-key) or the migration has not run.
	ErrClaimsNotProvisioned = errors.New("readtx: a signed test claim did not verify; provision the claims key (core assist-claims-key) and check the migration")
)

const selfCheckAsker = "readtx-selfcheck"

// checkSessionStable runs two statements in one read only transaction and
// requires one backend pid for both.
func (r *Reader) checkSessionStable(ctx context.Context) error {
	tx, err := r.begin(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return mapError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var a, b int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&a); err != nil {
		return mapError(err)
	}
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&b); err != nil {
		return mapError(err)
	}
	if a != b {
		return ErrStatementPooling
	}
	return nil
}

// checkLogin requires the session user to be the expected assist login and not
// a superuser.
func (r *Reader) checkLogin(ctx context.Context, login string) error {
	tx, err := r.begin(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return mapError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var user string
	var super bool
	if err := tx.QueryRow(ctx, `SELECT session_user::text, (SELECT rolsuper FROM pg_roles WHERE rolname = session_user)`).Scan(&user, &super); err != nil {
		return mapError(err)
	}
	if user != login || super {
		return ErrWrongLogin
	}
	return nil
}

// checkClaims signs a test claim, carries it through the real read path and
// requires the database to return it.
func (r *Reader) checkClaims(ctx context.Context) error {
	var got string
	err := r.Run(ctx, Claims{Audience: r.audience, AskerID: selfCheckAsker}, `SELECT coalesce((ai.claims()).asker_id, '')`, nil,
		func(rows pgx.Rows) error {
			if rows.Next() {
				return rows.Scan(&got)
			}
			return nil
		})
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == CodeRefused {
			return ErrClaimsNotProvisioned
		}
		return err
	}
	if got != selfCheckAsker {
		return ErrClaimsNotProvisioned
	}
	return nil
}

// startupCheck is the whole boot gate of one reader.
func (r *Reader) startupCheck(ctx context.Context, login string) error {
	if err := r.checkLogin(ctx, login); err != nil {
		return err
	}
	if err := r.checkSessionStable(ctx); err != nil {
		return err
	}
	return r.checkClaims(ctx)
}
