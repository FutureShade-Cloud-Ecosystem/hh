package readtx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Limits are the per call timeouts set inside the transaction. The login role
// carries the same settings as ALTER ROLE defaults (migration 0418); these are
// the explicit values for the call.
type Limits struct {
	Statement time.Duration
	Lock      time.Duration
}

// DefaultLimits match the role defaults of migration 0418.
var DefaultLimits = Limits{Statement: 5 * time.Second, Lock: 2 * time.Second}

// Error codes. A Postgres error is mapped to one of these before it leaves this
// package: the text of a database error can carry values, so it is never passed
// on (plan 3.5).
const (
	CodeTimeout     = "timeout"
	CodeRefused     = "refused"
	CodeUnavailable = "unavailable"
	CodeAudience    = "audience"
	CodeClaims      = "claims"
)

// Error is the only error Run returns for a database or claims failure.
type Error struct{ Code string }

func (e *Error) Error() string { return "assist read: " + e.Code }

func fail(code string) error { return &Error{Code: code} }

// mapError turns any driver error into a coded one, keeping only the SQLSTATE
// class that decides the code.
func mapError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "57014", "55P03", "25P03":
			return fail(CodeTimeout)
		case "42501":
			return fail(CodeRefused)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fail(CodeTimeout)
	}
	return fail(CodeUnavailable)
}

type beginFunc func(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)

// Reader runs tool calls as one audience's assist login.
type Reader struct {
	audience string
	role     string // the reader role SET LOCAL ROLE switches to
	signer   *Signer
	limits   Limits
	begin    beginFunc
}

// Run runs one tool call: one read only transaction that is always rolled back.
//
//	BEGIN READ ONLY
//	SELECT pg_backend_pid()
//	SELECT set_config('ai.claims', $1, true)   -- the signed claim, a bind parameter
//	SET LOCAL statement_timeout, lock_timeout
//	SET LOCAL ROLE <reader>
//	<stmt>
//	ROLLBACK
//
// scan receives the rows of stmt and must read them inside the call. c must
// carry this reader's audience. The caller builds c from an already resolved
// Caller; Run never takes claims from request input.
func (r *Reader) Run(ctx context.Context, c Claims, stmt string, args []any, scan func(pgx.Rows) error) error {
	return r.Do(ctx, c, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, stmt, args...)
		if err != nil {
			return mapError(err)
		}
		defer rows.Close()
		if err := scan(rows); err != nil {
			return err
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return mapError(err)
		}
		return nil
	})
}

// Do opens the same read only transaction as Run, sets it up the same way (backend pid, bound
// claim, timeouts, SET LOCAL ROLE the reader) and hands it to fn, then rolls back. It exists for a
// caller that needs more than one statement under one claim (an EXPLAIN, then the query). fn gets
// the transaction after the role is set, so every statement it runs is the reader's. Errors fn
// returns pass through untouched; errors from the driver inside fn are the caller's to map (MapError).
func (r *Reader) Do(ctx context.Context, c Claims, fn func(tx pgx.Tx) error) error {
	if c.Audience != r.audience {
		return fail(CodeAudience)
	}
	tx, err := r.begin(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return mapError(err)
	}
	defer func() {
		rb, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rb)
	}()

	var pid int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		return mapError(err)
	}
	tok, err := r.signer.Sign(c, pid)
	if err != nil {
		return fail(CodeClaims)
	}
	// The claim is a bind parameter, never part of a statement's text, so it does
	// not appear in pg_stat_activity or a statement log.
	if _, err := tx.Exec(ctx, `SELECT set_config('ai.claims', $1, true)`, tok.Reveal()); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL statement_timeout = %d`, r.limits.Statement.Milliseconds())); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL lock_timeout = %d`, r.limits.Lock.Milliseconds())); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{r.role}.Sanitize()); err != nil {
		return mapError(err)
	}
	return fn(tx)
}

// MapError turns a driver error from a statement fn ran inside Do into the coded Error. Nothing of
// the driver's text survives.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return mapError(err)
}
