//go:build integration

package main

// finance_reset_roles_integration_test.go is finding 3 (W2-RESET-FIX-r1): the
// world's dump does not depend on the build cluster's role names. A world
// dumped from a database named one way restores into a box database named
// another way, with a role that is not postgres, and the assist grants land
// on the box's own roles.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
)

// financeResetDumpWorld dumps a database the way the world's own build dumps
// it: the owner statements are left out of the dump, and the world's own
// owner statements are appended to it, the way deploy/finance/world/run.sh
// writes them.
func financeResetDumpWorld(t *testing.T, dsn string) []byte {
	t.Helper()
	servicePath, cleanup, err := libpqServiceFile(dsn)
	if err != nil {
		t.Fatalf("the libpq service file: %v", err)
	}
	defer cleanup()
	cmd := exec.Command("pg_dump", "--no-owner")
	cmd.Env = append(os.Environ(), "PGSERVICEFILE="+servicePath, "PGSERVICE="+pgServiceName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the dump: %v (%s)", err, stderr.String())
	}
	if len(out) == 0 {
		t.Fatalf("the dump is empty")
	}
	ownership := financeResetOwnershipText(t, dsn)
	return append(out, ownership...)
}

// financeResetOwnershipText answers the world's own owner statements, read
// with the same query the world's build appends to its dump
// (deploy/finance/world/ownership.sql).
func financeResetOwnershipText(t *testing.T, dsn string) []byte {
	t.Helper()
	servicePath, cleanup, err := libpqServiceFile(dsn)
	if err != nil {
		t.Fatalf("the libpq service file: %v", err)
	}
	defer cleanup()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "finance", "world", "ownership.sql"))
	if err != nil {
		t.Fatalf("the world's ownership query: %v", err)
	}
	cmd := exec.Command("psql", "-tAq")
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = append(os.Environ(), "PGSERVICEFILE="+servicePath, "PGSERVICE="+pgServiceName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the world's ownership statements: %v (%s)", err, stderr.String())
	}
	return out
}

// TestIntegration_FinanceResetRoleMapping is finding 3: a world dumped from
// a database named one way restores into a box database named another way,
// with a role that is not postgres, and the assist grants land on the box's
// own roles.
//
// The world's dump is taken from a database whose name gives the assist roles
// one suffix, and it is restored into a box database whose name gives them
// another. The restoring role is a plain role, never a superuser and never
// named postgres: the shape the deploy documents for the box's own
// DATABASE_URL. The world's grants must land on the box's own roles, and the
// build's migrating role must not be needed at all.
func TestIntegration_FinanceResetRoleMapping(t *testing.T) {
	ctx := context.Background()

	// The deployed box's shape: the database named hhfinance, owned by the
	// role postgres, the role the deployed box connects as. The box's own
	// suffix on the assist roles is the one readtx.Suffix gives it, and on
	// hhfinance that is hhfinance.
	boxDSN := financeResetFreshDeployedDB(t, "hhfinance")
	boxPool, err := db.Connect(ctx, boxDSN)
	if err != nil {
		t.Fatalf("connecting to the box: %v", err)
	}
	defer boxPool.Close()
	financeResetMarkBox(t, ctx, boxPool)
	if got := readtx.Suffix("hhfinance"); got != "hhfinance" {
		t.Fatalf("the box's suffix is %q, want %q", got, "hhfinance")
	}

	// The world: the database named finworld, the one the world's own build
	// makes, whose suffix on the assist roles is finworld. The dump is taken
	// by the admin connection, the way the world's own build takes it.
	financeResetFreshDeployedDB(t, "finworld")
	worldDumpDSN := swapDatabase(financeResetAdminDSN(t), "finworld")
	dump := financeResetDumpWorld(t, worldDumpDSN)
	// The world's build cluster is not the box's cluster: the world's roles
	// are not on the box. They go away with the world's database, so the
	// world's dump names roles the box does not carry, the way it does on the
	// deployed box.
	financeResetDropWorldRoles(t, "finworld", readtx.Suffix("finworld"))
	digest := financeResetDigest(dump)
	worldFile := filepath.Join(t.TempDir(), "world.sql.gz")
	if err := writeWorldGzip(t, worldFile, dump); err != nil {
		t.Fatalf("the world file: %v", err)
	}

	env := finEnvFor(boxDSN, nil, map[string]string{
		envFinanceWorldFile: worldFile,
		envFinanceWorldSHA:  digest,
		envFinanceResetDir:  t.TempDir(),
	})
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	if err := financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, env); err != nil {
		t.Fatalf("the reset: %v", err)
	}
	if line := strings.TrimSpace(out.String()); !strings.HasPrefix(line, "dump_bytes=") {
		t.Fatalf("the reset printed %q, want the counts line", line)
	}

	restored, err := db.Connect(ctx, boxDSN)
	if err != nil {
		t.Fatalf("reconnecting to the restored box: %v", err)
	}
	defer restored.Close()

	// The world's grants landed on the box's own roles: the box's view owner
	// holds the world's grant on the ai schema, and the build's view owner,
	// whose name the world's dump carries, is not on the box at all.
	boxRoles := readtx.RolesFor(readtx.Suffix("hhfinance"))
	buildRoles := readtx.RolesFor(readtx.Suffix("finworld"))
	boxViewOwner := boxRoles.ViewOwner
	var onBox bool
	if err := restored.QueryRow(ctx, `SELECT has_schema_privilege($1, 'ai', 'USAGE')`, boxViewOwner).Scan(&onBox); err != nil {
		t.Fatalf("reading the box's own grant: %v", err)
	}
	if !onBox {
		t.Fatalf("the world's grant did not land on the box's own role %s", boxViewOwner)
	}
	var buildPresent bool
	if err := restored.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)`, buildRoles.ViewOwner).Scan(&buildPresent); err != nil {
		t.Fatalf("reading the build's role: %v", err)
	}
	if buildPresent {
		t.Fatalf("the build's role %s is on the box: the world's roles belong to the world's own cluster", buildRoles.ViewOwner)
	}

	// The world's objects keep their owners: the ai schema's views belong to
	// the box's own view owner role, and to the build's role no longer.
	var owner string
	if err := restored.QueryRow(ctx, `
		SELECT pg_get_userbyid(c.relowner) FROM pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'ai' AND c.relname = 'orders'`).Scan(&owner); err != nil {
		t.Fatalf("reading the view's owner: %v", err)
	}
	if owner != boxViewOwner {
		t.Fatalf("the world's view is owned by %q, want the box's own role %q", owner, boxViewOwner)
	}

	// The world's default privileges landed on the box's own restoring role,
	// and they name the box's own function users role: the dump must not
	// depend on the build cluster's roles.
	var defaults int
	if err := restored.QueryRow(ctx, `
		SELECT count(*) FROM pg_default_acl
		 WHERE defaclrole = (SELECT oid FROM pg_roles WHERE rolname = $1)
		   AND defaclacl::text LIKE '%' || $2 || '%'`,
		"postgres", boxRoles.FunctionUsers).Scan(&defaults); err != nil {
		t.Fatalf("reading the box's default privileges: %v", err)
	}
	if defaults == 0 {
		t.Fatalf("the world's default privileges did not land on the box's own role %s for %s", "postgres", boxRoles.FunctionUsers)
	}
}

// financeResetFreshDeployedDB builds the deployed box's shape: a database of
// that name, owned by the role the deployed box connects as (postgres), with
// the migration set applied by that role.
func financeResetFreshDeployedDB(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	adminDSN := financeResetAdminDSN(t)
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer admin.Close()
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
		t.Fatalf("dropping %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	dsn := swapDatabase(adminDSN, name)
	financeResetMigrate(t, dsn, "")
	return dsn
}

// financeResetDropWorldRoles takes the world's roles away with the world's
// database, the way the world's own cluster keeps them and the box's cluster
// does not: the box carries only its own roles, and the world's dump names
// roles that are not there.
func financeResetDropWorldRoles(t *testing.T, database, suffix string) {
	t.Helper()
	ctx := context.Background()
	adminDSN := financeResetAdminDSN(t)
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer admin.Close()
	var member string
	if err := admin.QueryRow(ctx, "SELECT current_user").Scan(&member); err != nil {
		t.Fatalf("reading the admin role: %v", err)
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)"); err != nil {
		t.Fatalf("dropping the world's database: %v", err)
	}
	roles := readtx.RolesFor(suffix)
	revokes := []string{
		fmt.Sprintf("REVOKE %s FROM %s", roles.Reader, roles.Login),
		fmt.Sprintf("REVOKE %s FROM %s", roles.ProReader, roles.ProLogin),
		fmt.Sprintf("REVOKE %s FROM %s", roles.ViewOwner, member),
		fmt.Sprintf("REVOKE %s FROM %s", roles.ClaimsOwner, member),
		fmt.Sprintf("REVOKE %s FROM %s", roles.ZoneReader, member),
		fmt.Sprintf("REVOKE %s FROM %s", roles.FunctionUsers, member),
	}
	for _, statement := range revokes {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	for _, role := range []string{
		roles.Login, roles.ProLogin, roles.Reader, roles.ProReader,
		roles.ViewOwner, roles.ClaimsOwner, roles.ZoneReader, roles.FunctionUsers,
	} {
		if _, err := admin.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Fatalf("dropping the world's role %s: %v", role, err)
		}
	}
}
