//go:build integration

package main

// finance_reset_write_integration_test.go is finding 1 (W2-RESET-FIX-r1): the
// world reset writes the dump before it drops any schema, so a write that
// fails leaves the database exactly as it was.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
)

// TestIntegration_FinanceResetWriteBeforeDrop is finding 1: the reset writes
// the dump before it drops any schema, so a write that fails leaves the
// database exactly as it was.
//
// The directory the reset is given cannot hold the file: its parent is a
// regular file, so the directory can never be made. The refusal has to name
// the directory, and the box's own table has to still be there afterwards.
func TestIntegration_FinanceResetWriteBeforeDrop(t *testing.T) {
	ctx := context.Background()
	dsn := financeResetFreshDB(t, "demoseed_it_finrstwrite", "")

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to the box: %v", err)
	}
	defer pool.Close()
	financeResetMarkBox(t, ctx, pool)
	// The box's own table, of its own shape: a restore that ran would take
	// it with the schema it dropped.
	if _, err := pool.Exec(ctx, `CREATE TABLE reset_marker (id int PRIMARY KEY, note text NOT NULL)`); err != nil {
		t.Fatalf("the box's own table: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reset_marker (id, note) VALUES (1, 'the box was here')`); err != nil {
		t.Fatalf("the box's own row: %v", err)
	}

	// The world the reset would put back: another database, dumped. The dump
	// is taken by the admin connection, the way the world's own build takes
	// it: the app role cannot read every table the world carries (the claims
	// key is readable by nobody else), so the build's dump is the superuser's.
	financeResetFreshDB(t, "demoseed_it_finrstworld", "")
	worldDumpDSN := swapDatabase(financeResetAdminDSN(t), "demoseed_it_finrstworld")
	dump := financeResetDump(t, worldDumpDSN)
	digest := financeResetDigest(dump)
	worldFile := filepath.Join(t.TempDir(), "world.sql.gz")
	if err := writeWorldGzip(t, worldFile, dump); err != nil {
		t.Fatalf("the world file: %v", err)
	}

	// The directory the reset is given cannot be written: its parent is a
	// regular file, so no directory can be made there at all.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("the blocked parent: %v", err)
	}
	resetDir := filepath.Join(blocked, "reset")

	env := finEnvFor(dsn, nil, map[string]string{
		envFinanceWorldFile: worldFile,
		envFinanceWorldSHA:  digest,
		// The wire name of the directory the reset is given, pinned here in
		// words so the test does not lean on the implementation's own name.
		"HH_FINANCE_RESET_DIR": resetDir,
	})
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	err = financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, env)
	if err == nil {
		t.Fatalf("the reset wrote its dump somewhere else and went on: %q", out.String())
	}
	if !strings.Contains(err.Error(), resetDir) {
		t.Fatalf("the failed write did not name the directory it was given: %v", err)
	}

	// The box's own table is still there, with its row: nothing was dropped.
	var markers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reset_marker`).Scan(&markers); err != nil {
		t.Fatalf("reading the box's own table: %v", err)
	}
	if markers != 1 {
		t.Fatalf("the failed write dropped the box's own table: %d rows left, want the one it had", markers)
	}
}
