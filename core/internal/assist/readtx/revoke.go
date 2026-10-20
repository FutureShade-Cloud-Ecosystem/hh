package readtx

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The SQL lives in a file, not a Go string: it reads the system catalogue to find
// the extensions' functions, which is no module's data.
//
//go:embed revoke_extension_functions.sql
var revokeExtensionFunctionsSQL string

// ProvisionRevokeExtensionFunctions closes the functions of extensions (pgcrypto,
// citext, pg_trgm) to PUBLIC. Migration 0418 cannot do it on a box where the
// migrating role is not a superuser, because those functions belong to the
// bootstrap superuser. Run it as the extensions' owner (core
// assist-revoke-extension-functions); it gives the functions to the function users
// role and hmac to the claims owner, and is safe to repeat.
func ProvisionRevokeExtensionFunctions(ctx context.Context, conn *pgx.Conn, roles Roles) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("readtx: cannot begin")
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('readtx.fn_users', $1, true), set_config('readtx.claims_owner', $2, true)`, roles.FunctionUsers, roles.ClaimsOwner); err != nil {
		return fmt.Errorf("readtx: cannot stage the role names%s", sqlState(err))
	}
	if _, err := tx.Exec(ctx, revokeExtensionFunctionsSQL); err != nil {
		return fmt.Errorf("readtx: cannot revoke the extension functions%s", sqlState(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("readtx: commit failed")
	}
	return nil
}
