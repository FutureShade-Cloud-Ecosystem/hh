//go:build integration

package main

// finance_reset_transaction_integration_test.go is item 5 (W2-RESET-FIX-r1
// addendum): the drop, the recreate and the restore are one transaction, so
// a restore that fails part way leaves the box exactly as it was.
//
// The box's own table and its own row are still there after the failure, and
// the box records no world: the world's sha256 mark is written only after
// the restore has committed.

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// TestIntegration_FinanceResetRestoreFailureLeavesBoxUntouched is item 5: a
// dump that fails part way through the restore leaves the old tables and
// their rows in place, and records no sha mark.
//
// The break sits near the end of the dump, so the restore has already
// dropped the schemas and restored most of the world when it fails. The
// whole script rolls back with it.
func TestIntegration_FinanceResetRestoreFailureLeavesBoxUntouched(t *testing.T) {
	ctx := context.Background()
	dsn := financeResetFreshDB(t, "demoseed_it_finrstfail", "")

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to the box: %v", err)
	}
	defer pool.Close()
	financeResetMarkBox(t, ctx, pool)
	// The box's own table, with its own row: a restore that had stayed
	// would have taken it with the schema it dropped.
	if _, err := pool.Exec(ctx, `CREATE TABLE box_marker (id int PRIMARY KEY, note text NOT NULL)`); err != nil {
		t.Fatalf("the box's own table: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO box_marker (id, note) VALUES (1, 'the box was here')`); err != nil {
		t.Fatalf("the box's own row: %v", err)
	}

	// The world, with a statement near its end that cannot run.
	financeResetFreshDB(t, "demoseed_it_finrstfailworld", "")
	worldDumpDSN := swapDatabase(financeResetAdminDSN(t), "demoseed_it_finrstfailworld")
	dump := financeResetDumpWorld(t, worldDumpDSN)
	broken := append(append([]byte{}, dump...),
		[]byte("\n-- A statement near the end of the dump that cannot run.\nSELECT finance_reset_no_such_function();\n")...)
	digest := financeResetDigest(broken)
	worldFile := filepath.Join(t.TempDir(), "world.sql.gz")
	if err := writeWorldGzip(t, worldFile, broken); err != nil {
		t.Fatalf("the world file: %v", err)
	}

	env := finEnvFor(dsn, nil, map[string]string{
		envFinanceWorldFile: worldFile,
		envFinanceWorldSHA:  digest,
		envFinanceResetDir:  t.TempDir(),
	})
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	if err := financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, env); err == nil {
		t.Fatalf("the reset with a broken dump succeeded: %q", out.String())
	}

	// The box's own table and its row are still there: the drop rolled back
	// with the restore that failed.
	var markers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM box_marker`).Scan(&markers); err != nil {
		t.Fatalf("reading the box's own table: %v", err)
	}
	if markers != 1 {
		t.Fatalf("the failed restore left the box's own table with %d rows, want the one it had", markers)
	}

	// And no sha mark: the world was never restored, so the box records no
	// world and the next boot may reset again.
	recorded, err := settings.NewDealerConfigReader(pool).FinanceWorldSHA256(ctx)
	if err != nil {
		t.Fatalf("reading the recorded world: %v", err)
	}
	if recorded != "" {
		t.Fatalf("the failed restore recorded a world: %q", recorded)
	}
}
