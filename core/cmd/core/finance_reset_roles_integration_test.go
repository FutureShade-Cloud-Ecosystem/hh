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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

	// The box: its own database, its own suffix on the assist roles.
	boxDSN := financeResetFreshDB(t, "demoseed_it_finbox", "")
	boxPool, err := db.Connect(ctx, boxDSN)
	if err != nil {
		t.Fatalf("connecting to the box: %v", err)
	}
	defer boxPool.Close()
	financeResetMarkBox(t, ctx, boxPool)

	// The world: another database, whose name gives the assist roles another
	// suffix. The dump is taken by the admin connection, the way the world's
	// own build takes it.
	financeResetFreshDB(t, "demoseed_it_finworldbuild", "")
	worldDumpDSN := swapDatabase(financeResetAdminDSN(t), "demoseed_it_finworldbuild")
	dump := financeResetDumpWorld(t, worldDumpDSN)
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
	// whose name the world's dump carries, holds nothing.
	boxViewOwner := "assist_view_owner_demoseed_it_finbox"
	buildViewOwner := "assist_view_owner_demoseed_it_finworldbuild"
	var onBox, onBuild bool
	if err := restored.QueryRow(ctx, `SELECT has_schema_privilege($1, 'ai', 'USAGE')`, boxViewOwner).Scan(&onBox); err != nil {
		t.Fatalf("reading the box's own grant: %v", err)
	}
	if err := restored.QueryRow(ctx, `SELECT has_schema_privilege($1, 'ai', 'USAGE')`, buildViewOwner).Scan(&onBuild); err != nil {
		t.Fatalf("reading the build's grant: %v", err)
	}
	if !onBox {
		t.Fatalf("the world's grant did not land on the box's own role %s", boxViewOwner)
	}
	if onBuild {
		t.Fatalf("the world's grant landed on the build's role %s", buildViewOwner)
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
	// and the build's migrating role is not needed: the box's database holds
	// no default privileges for postgres at all.
	var appDefaults, postgresDefaults int
	if err := restored.QueryRow(ctx, `
		SELECT count(*) FROM pg_default_acl
		 WHERE defaclrole = (SELECT oid FROM pg_roles WHERE rolname = $1)`, financeResetAppRoleName).Scan(&appDefaults); err != nil {
		t.Fatalf("reading the box's default privileges: %v", err)
	}
	if err := restored.QueryRow(ctx, `
		SELECT count(*) FROM pg_default_acl
		 WHERE defaclrole = (SELECT oid FROM pg_roles WHERE rolname = $1)`, "postgres").Scan(&postgresDefaults); err != nil {
		t.Fatalf("reading the build's default privileges: %v", err)
	}
	if appDefaults == 0 {
		t.Fatalf("the world's default privileges did not land on the box's own role %s", financeResetAppRoleName)
	}
	if postgresDefaults != 0 {
		t.Fatalf("the box's database carries %d default privilege row(s) for postgres: the dump must not depend on the build's migrating role", postgresDefaults)
	}
}
