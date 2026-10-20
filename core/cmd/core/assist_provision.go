package main

// The assist provisioning oneshots, run by the operator at deploy with the migrate
// role's database URL (the same one-shot rule as migrate, seed and demoseed):
//
//	core assist-logins      sets each assist login's password from its own value
//	core assist-claims-key  writes the claims signing key to ai.claims_key
//
// Neither prints a value, a URL or a key: only role names, the database target
// (host and name) and the key id, which is in every signed claim anyway. The
// passwords and the key come from the deployment's vault through the environment,
// never from this repository.

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
)

func assistLoginsRole(getenv func(string) string, out io.Writer) error {
	staff := readtx.NewSecret(getenv(readtx.EnvReaderPassword))
	pro := readtx.NewSecret(getenv(readtx.EnvProReaderPassword))
	if staff.IsEmpty() {
		return fmt.Errorf("assist-logins: %s is not set", readtx.EnvReaderPassword)
	}
	conn, err := assistOneshotConn("assist-logins", getenv, out)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	names, err := readtx.ProvisionLogins(context.Background(), conn, staff, pro)
	if err != nil {
		return fmt.Errorf("assist-logins: %w", err)
	}
	for _, n := range names {
		fmt.Fprintf(out, "assist-logins: password set for role %s\n", n)
	}
	return nil
}

func assistClaimsKeyRole(getenv func(string) string, out io.Writer) error {
	key := readtx.NewSecret(getenv(readtx.EnvClaimsKey))
	if key.IsEmpty() {
		return fmt.Errorf("assist-claims-key: %s is not set", readtx.EnvClaimsKey)
	}
	conn, err := assistOneshotConn("assist-claims-key", getenv, out)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	id, err := readtx.ProvisionClaimsKey(context.Background(), conn, key)
	if err != nil {
		return fmt.Errorf("assist-claims-key: %w", err)
	}
	fmt.Fprintf(out, "assist-claims-key: key current under key id %d\n", id)
	return nil
}

// assistOneshotConn resolves the target by the one-shot rule, prints it (host and
// database only) and connects. A connection failure reports no driver text: the
// driver's message can carry the URL.
func assistOneshotConn(role string, getenv func(string) string, out io.Writer) (*pgx.Conn, error) {
	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", role, err)
	}
	printOneshotDatabaseTarget(out, role, dsn)
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot connect to the database", role)
	}
	return conn, nil
}

// assistRoleDispatch is called from main for the two role names.
func assistRoleDispatch(role string) (handled bool) {
	switch role {
	case "assist-logins":
		if err := assistLoginsRole(os.Getenv, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "core:", err)
			os.Exit(1)
		}
		return true
	case "assist-claims-key":
		if err := assistClaimsKeyRole(os.Getenv, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "core:", err)
			os.Exit(1)
		}
		return true
	}
	return false
}
