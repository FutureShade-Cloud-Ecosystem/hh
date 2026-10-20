package readtx

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The two deployment passwords, set by the provisioning oneshot.
const (
	EnvReaderPassword    = "HOS_ASSIST_READER_PASSWORD"
	EnvProReaderPassword = "HOS_ASSIST_PRO_READER_PASSWORD"
)

const (
	minPasswordLen = 16
	scramIter      = 4096
)

// scramVerifier derives the SCRAM-SHA-256 verifier Postgres stores for a
// password, the way psql's \password does, so the password itself never crosses
// the wire or appears in any statement or log. Passwords are printable ASCII,
// for which SASLprep is the identity.
func scramVerifier(pw redact.Secret, salt []byte) (string, error) {
	p := pw.Reveal()
	if len(p) < minPasswordLen {
		return "", errors.New("readtx: a login password is missing or shorter than 16 characters")
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] > 0x7e {
			return "", errors.New("readtx: a login password must be printable ASCII")
		}
	}
	salted, err := pbkdf2.Key(sha256.New, p, salt, scramIter, sha256.Size)
	if err != nil {
		return "", errors.New("readtx: password derivation failed")
	}
	mac := func(key []byte, msg string) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(msg))
		return m.Sum(nil)
	}
	stored := sha256.Sum256(mac(salted, "Client Key"))
	server := mac(salted, "Server Key")
	b := base64.StdEncoding
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", scramIter, b.EncodeToString(salt), b.EncodeToString(stored[:]), b.EncodeToString(server)), nil
}

// quietStatementLogging turns statement logging off for the current transaction,
// so that a server set to log_statement = all or mod does not write the bind
// parameters of the statements that follow (a SCRAM verifier, the claims key) into
// its log. Both settings are superuser only: where the role lacks the privilege
// the attempt is rolled back to a savepoint, the transaction carries on, and the
// answer is false so the oneshot can say so. Any other failure is an error.
func quietStatementLogging(ctx context.Context, tx pgx.Tx) (bool, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, errors.New("readtx: cannot open a savepoint")
	}
	for _, stmt := range []string{`SET LOCAL log_statement = 'none'`, `SET LOCAL log_min_duration_statement = -1`} {
		if _, err := sp.Exec(ctx, stmt); err != nil {
			_ = sp.Rollback(ctx)
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "42501" {
				return false, nil
			}
			return false, fmt.Errorf("readtx: cannot adjust statement logging%s", sqlState(err))
		}
	}
	if err := sp.Commit(ctx); err != nil {
		return false, errors.New("readtx: cannot release the savepoint")
	}
	return true, nil
}

func warnLogging(warn io.Writer, quiet bool) {
	if !quiet && warn != nil {
		fmt.Fprintln(warn, "warning: this role may not silence statement logging; if the server logs statements (log_statement = all or mod) the values sent next can appear in its log")
	}
}

// ProvisionLogins sets the staff login's password, and the contractor login's when
// a password is given, in one transaction on conn (the migrate role's connection).
// The role names come from the connected database. Nothing is printed; the
// returned names are role names only. warn receives one line where the role may
// not silence statement logging (see quietStatementLogging).
func ProvisionLogins(ctx context.Context, conn *pgx.Conn, staff, pro redact.Secret, warn io.Writer) ([]string, error) {
	if staff.IsEmpty() {
		return nil, errors.New("readtx: " + EnvReaderPassword + " is not set")
	}
	var db string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		return nil, errors.New("readtx: cannot read the database name")
	}
	roles := RolesFor(db)
	type job struct {
		role string
		pw   redact.Secret
	}
	jobs := []job{{roles.Login, staff}}
	if !pro.IsEmpty() {
		jobs = append(jobs, job{roles.ProLogin, pro})
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, errors.New("readtx: cannot begin")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	quiet, err := quietStatementLogging(ctx, tx)
	if err != nil {
		return nil, err
	}
	warnLogging(warn, quiet)
	var done []string
	for _, j := range jobs {
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, errors.New("readtx: no randomness")
		}
		verifier, err := scramVerifier(j.pw, salt)
		if err != nil {
			return nil, err
		}
		// ALTER ROLE takes no bind parameter, so the (already hashed) verifier goes
		// through a transaction local setting and is quoted by the server.
		if _, err := tx.Exec(ctx, `SELECT set_config('readtx.role', $1, true), set_config('readtx.verifier', $2, true)`, j.role, verifier); err != nil {
			return nil, fmt.Errorf("readtx: cannot stage the verifier%s", sqlState(err))
		}
		if _, err := tx.Exec(ctx, `DO $do$ BEGIN
			EXECUTE format('ALTER ROLE %I PASSWORD %L', current_setting('readtx.role'), current_setting('readtx.verifier'));
		END $do$`); err != nil {
			return nil, fmt.Errorf("readtx: cannot set the password of %s%s", j.role, sqlState(err))
		}
		done = append(done, j.role)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, errors.New("readtx: commit failed")
	}
	return done, nil
}

// ProvisionClaimsKey writes key to ai.claims_key under its derived id, as the
// claims owner role (the only role that touches the table), makes it the current
// key, and keeps only the two most recent keys. Writing the key that is already
// current changes nothing. It returns the key id, which is not a secret: it is in
// every signed claim.
func ProvisionClaimsKey(ctx context.Context, conn *pgx.Conn, key redact.Secret, warn io.Writer) (int32, error) {
	if len(key.Reveal()) < minKeyLen {
		return 0, errors.New("readtx: the claims signing key is missing or shorter than 32 characters")
	}
	var db string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		return 0, errors.New("readtx: cannot read the database name")
	}
	owner := RolesFor(db).ClaimsOwner
	id := KeyID(key)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, errors.New("readtx: cannot begin")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	quiet, err := quietStatementLogging(ctx, tx)
	if err != nil {
		return 0, err
	}
	warnLogging(warn, quiet)
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{owner}.Sanitize()); err != nil {
		return 0, fmt.Errorf("readtx: cannot become the claims owner%s", sqlState(err))
	}
	tag, err := tx.Exec(ctx, `INSERT INTO ai.claims_key (key_id, key) VALUES ($1, $2)
		ON CONFLICT (key_id) DO UPDATE SET created_at = now() WHERE ai.claims_key.key = EXCLUDED.key`, id, key.Reveal())
	if err != nil {
		return 0, fmt.Errorf("readtx: cannot write the claims key%s", sqlState(err))
	}
	if tag.RowsAffected() != 1 {
		return 0, errors.New("readtx: the key id collides with a different stored key; choose another key")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ai.claims_key WHERE key_id NOT IN
		(SELECT key_id FROM ai.claims_key ORDER BY created_at DESC, key_id DESC LIMIT 2)`); err != nil {
		return 0, fmt.Errorf("readtx: cannot prune old claims keys%s", sqlState(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, errors.New("readtx: commit failed")
	}
	return id, nil
}
