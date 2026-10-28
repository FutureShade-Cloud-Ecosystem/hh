//go:build integration

package main

// finance_reset_integration_test.go, the finance box's world reset (W2-1b)
// proved against a real migrated database.
//
// The legs:
//
//   - a dump whose sha256 is not the one the environment names is refused
//     BEFORE anything is dropped;
//   - a -confirm that does not name the box's org is refused;
//   - a database that is not marked as the finance box is refused;
//   - a good dump replaces the world, the migrations that came after the
//     dump are applied, the audit row is written, the printed line carries
//     counts only, and the finance box's read only check passes on the
//     restored world.
//
// The dump is fetched over HTTP with the credential in a request header and
// restored with psql through a libpq service file, so the database URL never
// sits on a command line.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// financeResetDump takes a world's dump with pg_dump, through the same
// libpq service file the restore reads: the database URL never sits on a
// command line.
func financeResetDump(t *testing.T, dsn string) []byte {
	t.Helper()
	servicePath, cleanup, err := libpqServiceFile(dsn)
	if err != nil {
		t.Fatalf("the libpq service file: %v", err)
	}
	defer cleanup()
	cmd := exec.Command("pg_dump", "--no-owner", "--no-privileges")
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
	return out
}

// financeResetBookkeeping writes the migration bookkeeping of a world that
// was migrated from the frozen files: the versions the frozen set carries, so
// the reset's own migration step reads the world as current.
func financeResetBookkeeping(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("the world's bookkeeping table: %v", err)
	}
	files, err := filepath.Glob("db/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("the frozen migration set: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING`, version); err != nil {
			t.Fatalf("the world's bookkeeping row %s: %v", version, err)
		}
	}
}

func TestIntegration_FinanceReset(t *testing.T) {
	ctx := context.Background()

	// The box's world: migrated from zero, seeded, marked as the finance box.
	pool := freshMigratedDB(t, "finreset")
	dsn := swapDatabase(os.Getenv("DATABASE_URL"), "demoseed_it_finreset")
	seedEnv := func(k string) string {
		switch k {
		case "DATABASE_URL", "CORE_DATABASE_URL":
			return dsn
		case "APP_ENV":
			return "dev"
		}
		return ""
	}
	if err := seedRole(seedEnv); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := settings.WriteBoxKindTx(ctx, pool, "finance"); err != nil {
		t.Fatalf("marking the box: %v", err)
	}
	if _, _, err := coreid.ProvisionOrgAnchor(ctx, pool, finOrgSlug, "Finance Box"); err != nil {
		t.Fatalf("the org anchor: %v", err)
	}

	// The world the reset puts back: the month walk's own world, through the
	// production wiring. Its books tie (the walk's twelve assertions are the
	// ties themselves), so the check on the restored world has to pass. The
	// walk builds its own database from the package directory, so it runs
	// before the leg moves to the core directory.
	TestFW1MonthWalk(t)
	// The migration step reads the frozen set the way the migrate role does,
	// from the working directory, so the leg runs from the directory that
	// holds it.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("the working directory: %v", err)
	}
	if err := os.Chdir(filepath.Join(wd, "..", "..")); err != nil {
		t.Fatalf("the core directory: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	worldDSN := swapDatabase(os.Getenv("DATABASE_URL"), "demoseed_it_fw1walk")
	worldPool, err := db.Connect(ctx, worldDSN)
	if err != nil {
		t.Fatalf("the world: %v", err)
	}
	defer worldPool.Close()
	// The world's migration bookkeeping, the way the migrate role writes it:
	// the world was migrated from the frozen files, and the reset's own
	// migration step reads the bookkeeping to know what is left to apply.
	financeResetBookkeeping(t, ctx, worldPool)
	// The world is one migration behind the frozen set, so the reset's own
	// migration step has one to apply: 0449 is one additive column, so
	// taking it back is dropping that column and its bookkeeping row.
	if _, err := worldPool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, "0449_sync_op_hashes_bound_by"); err != nil {
		t.Fatalf("taking the world's last migration back: %v", err)
	}
	if _, err := worldPool.Exec(ctx, `ALTER TABLE sync_op_hashes DROP COLUMN IF EXISTS bound_by`); err != nil {
		t.Fatalf("taking the world's last migration back: %v", err)
	}
	if err := settings.WriteBoxKindTx(ctx, worldPool, "finance"); err != nil {
		t.Fatalf("marking the world: %v", err)
	}
	dump := financeResetDump(t, worldDSN)
	worldCounts := financeCheckCounts(t, ctx, worldPool)

	// A second revision of the same world: one comment line appended to the
	// dump changes the digest and restores the same world, so a leg can prove
	// that a new sha resets while the world itself is untouched.
	const token = "fin-reset-token-not-a-secret"
	dumpV2 := append(append([]byte{}, dump...), []byte("\n-- world revision two\n")...)
	sumV2 := sha256.Sum256(dumpV2)
	digestV2 := hex.EncodeToString(sumV2[:])

	// The storage file the world is fetched from. The credential travels in
	// a request header and never in the address. The handler counts its
	// requests, so a skip leg can prove nothing was fetched.
	var dumpRequests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dumpRequests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/v2") {
			w.Write(dumpV2)
			return
		}
		w.Write(dump)
	}))
	defer srv.Close()

	sum := sha256.Sum256(dump)
	digest := hex.EncodeToString(sum[:])
	worldEnv := func(over map[string]string) func(string) string {
		base := map[string]string{
			envFinanceWorldURL:   srv.URL,
			envFinanceWorldToken: token,
			envFinanceWorldSHA:   digest,
			// The dump file is written on disk, under the directory the
			// reset is given. The test gives it one of its own, so the run
			// never writes into the box's own var tree.
			envFinanceResetDir: t.TempDir(),
		}
		for k, v := range over {
			if v == "" {
				delete(base, k)
			} else {
				base[k] = v
			}
		}
		return finEnvFor(dsn, nil, base)
	}

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	before := financeCheckCounts(t, ctx, pool)

	// Leg 1: the dump's sha256 is not the one the environment names. The
	// reset refuses before it drops anything.
	out.Reset()
	err = financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, worldEnv(map[string]string{
		envFinanceWorldSHA: strings.Repeat("0", 64),
	}))
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("the wrong sha: a refusal naming the digest, got %v", err)
	}
	for table, n := range before {
		if now := financeCheckCounts(t, ctx, pool)[table]; now != n {
			t.Fatalf("the wrong sha dropped %s: %d rows before, %d after", table, n, now)
		}
	}

	// Leg 2: the confirm does not name the box's org.
	out.Reset()
	err = financeResetRole([]string{"--confirm", "somebody-elses-org"}, out, errOut, worldEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("the wrong confirm: a refusal naming the confirm, got %v", err)
	}
	for table, n := range before {
		if now := financeCheckCounts(t, ctx, pool)[table]; now != n {
			t.Fatalf("the wrong confirm dropped %s: %d rows before, %d after", table, n, now)
		}
	}

	// Leg 3: the database is not the finance box.
	if err := settings.WriteBoxKindTx(ctx, pool, "demo"); err != nil {
		t.Fatalf("unmarking the box: %v", err)
	}
	out.Reset()
	err = financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, worldEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "box_kind") {
		t.Fatalf("a box that is not the finance box: a refusal naming the box kind, got %v", err)
	}
	for table, n := range before {
		if now := financeCheckCounts(t, ctx, pool)[table]; now != n {
			t.Fatalf("a box that is not the finance box dropped %s: %d rows before, %d after", table, n, now)
		}
	}
	if err := settings.WriteBoxKindTx(ctx, pool, "finance"); err != nil {
		t.Fatalf("marking the box again: %v", err)
	}

	// Leg 4: the good dump replaces the world.
	out.Reset()
	if err := financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, worldEnv(nil)); err != nil {
		t.Fatalf("the reset: %v", err)
	}
	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "dump_bytes=") {
		t.Fatalf("the reset printed something other than counts: %q", line)
	}
	for _, field := range []string{"dump_bytes=", "schemas_dropped=", "migrations_applied=", "tables_restored=", "audit_rows="} {
		if !strings.Contains(line, field) {
			t.Fatalf("the reset's line does not carry %s: %q", field, line)
		}
	}
	if strings.Contains(line, srv.URL) || strings.Contains(line, dsn) || strings.Contains(line, token) {
		t.Fatalf("the reset printed a value it must never print: %q", line)
	}
	if !strings.Contains(line, "migrations_applied=1") {
		t.Fatalf("the reset did not apply the migration the dump came before: %q", line)
	}

	// The world came back: the box's own rows are the world's rows now, and
	// the world's box kind is back. The audit row is the one row the reset
	// itself added, so it is not part of the comparison.
	restoredCounts := financeCheckCounts(t, ctx, pool)
	for table, n := range worldCounts {
		if table == "audit_log" {
			continue
		}
		if restoredCounts[table] != n {
			t.Fatalf("the restored world does not carry the world's %s: %d rows in the world, %d after the reset", table, n, restoredCounts[table])
		}
	}
	kind, err := settings.NewDealerConfigReader(pool).BoxKind(ctx)
	if err != nil || kind != "finance" {
		t.Fatalf("the restored world is not the finance box: %q (%v)", kind, err)
	}

	// The audit row is there, through the audit module.
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'FINANCE_WORLD_RESET'`).Scan(&audits); err != nil {
		t.Fatalf("counting the audit rows: %v", err)
	}
	if audits != 1 {
		t.Fatalf("the reset wrote %d audit row(s), one is the trail", audits)
	}

	// The check passes on the restored world. The transaction is closed when
	// the check is done, the way the role itself closes it: the legs below
	// reset the world again, and an open read transaction holds the share
	// locks the next drop schema waits for.
	restored, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("reconnecting to the restored world: %v", err)
	}
	defer restored.Close()
	tx, err := restored.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("the read only transaction: %v", err)
	}
	out.Reset()
	failed, err := financeCheckRunWired(ctx, restored, tx, out, nil)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("the check on the restored world: %v", err)
	}
	if failed != 0 {
		t.Fatalf("the check found %d failing tie(s) on the restored world:\n%s", failed, out.String())
	}

	// Leg 5: the recorded world. The reset recorded the restored world's
	// sha256 inside the box, through the settings module, in the same step as
	// its audit row. The read rides a fresh connection, the way the reset's
	// own reads do after a restore.
	recorded := financeResetRecordedWorld(t, ctx, dsn)
	if recorded != digest {
		t.Fatalf("the recorded world is %q, want the restored world's sha256 %q", recorded, digest)
	}

	// Leg 6: -once with the same sha skips. The line says already on world,
	// nothing is dropped, nothing is fetched, and no second audit row is
	// written.
	fetches := dumpRequests.Load()
	out.Reset()
	if err := financeResetRole([]string{"-once", "--confirm", finOrgSlug}, out, errOut, worldEnv(nil)); err != nil {
		t.Fatalf("the -once skip: %v", err)
	}
	if line := strings.TrimSpace(out.String()); line != "already on world "+digest {
		t.Fatalf("the -once skip printed %q, want already on world %q", line, digest)
	}
	if dumpRequests.Load() != fetches {
		t.Fatalf("the -once skip fetched the dump: %d requests before, %d after", fetches, dumpRequests.Load())
	}
	skipCounts := financeResetFreshCounts(t, ctx, dsn)
	for table, n := range worldCounts {
		if table == "audit_log" {
			continue
		}
		if skipCounts[table] != n {
			t.Fatalf("the -once skip changed %s: %d rows before, %d after", table, n, skipCounts[table])
		}
	}
	if audits := financeResetAuditCount(t, ctx, dsn); audits != 1 {
		t.Fatalf("the -once skip wrote audit rows: %d, want the one the reset wrote", audits)
	}

	// Leg 7: -once with a new sha resets. The reset ensures the box's own org
	// anchor in its audit transaction after the restore, so a run that will
	// actually reset needs no anchor to exist beforehand: the way a fresh box
	// booting with the variable set works.
	out.Reset()
	if err := financeResetRole([]string{"-once", "--confirm", finOrgSlug}, out, errOut, worldEnv(map[string]string{
		envFinanceWorldURL: srv.URL + "/v2",
		envFinanceWorldSHA: digestV2,
	})); err != nil {
		t.Fatalf("the -once reset on a new sha: %v", err)
	}
	if line := strings.TrimSpace(out.String()); !strings.HasPrefix(line, "dump_bytes=") {
		t.Fatalf("the -once reset on a new sha printed %q, want the counts line", line)
	}
	v2Counts := financeResetFreshCounts(t, ctx, dsn)
	for table, n := range worldCounts {
		if table == "audit_log" {
			continue
		}
		if v2Counts[table] != n {
			t.Fatalf("the -once reset on a new sha changed %s: %d rows before, %d after", table, n, v2Counts[table])
		}
	}
	recorded = financeResetRecordedWorld(t, ctx, dsn)
	if recorded != digestV2 {
		t.Fatalf("the recorded world is %q, want the new digest %q", recorded, digestV2)
	}
	// The second restore wiped the first reset's audit row with the rest of
	// the world's trail: the one row the database carries is this reset's.
	if audits := financeResetAuditCount(t, ctx, dsn); audits != 1 {
		t.Fatalf("the -once reset on a new sha wrote %d audit row(s), want its own one", audits)
	}

	// Leg 8: -once again, now on the new recorded sha: the skip, with the new
	// digest in its line.
	fetches = dumpRequests.Load()
	out.Reset()
	if err := financeResetRole([]string{"-once", "--confirm", finOrgSlug}, out, errOut, worldEnv(map[string]string{
		envFinanceWorldSHA: digestV2,
	})); err != nil {
		t.Fatalf("the second -once skip: %v", err)
	}
	if line := strings.TrimSpace(out.String()); line != "already on world "+digestV2 {
		t.Fatalf("the second -once skip printed %q, want already on world %q", line, digestV2)
	}
	if dumpRequests.Load() != fetches {
		t.Fatalf("the second -once skip fetched the dump: %d requests before, %d after", fetches, dumpRequests.Load())
	}
	if audits := financeResetAuditCount(t, ctx, dsn); audits != 1 {
		t.Fatalf("the second -once skip wrote audit rows: %d, want the reset's one untouched", audits)
	}

	// Leg 9: the file source. The dump gzipped into a file the leg made
	// itself resets the box, with no URL and no token in the environment:
	// the world rides in a file. The digest is the sha256 of the decoded
	// SQL, not of the gzip.
	worldDir := t.TempDir()
	worldFile := filepath.Join(worldDir, "world.sql.gz")
	if err := writeWorldGzip(t, worldFile, dump); err != nil {
		t.Fatalf("the world file: %v", err)
	}
	fileEnv := func(over map[string]string) func(string) string {
		base := map[string]string{
			envFinanceWorldURL:   "",
			envFinanceWorldToken: "",
			envFinanceWorldFile:  worldFile,
		}
		for k, v := range over {
			base[k] = v
		}
		return worldEnv(base)
	}
	out.Reset()
	if err := financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, fileEnv(map[string]string{
		envFinanceWorldSHA: digest,
	})); err != nil {
		t.Fatalf("the file source reset: %v", err)
	}
	if line := strings.TrimSpace(out.String()); !strings.HasPrefix(line, "dump_bytes=") {
		t.Fatalf("the file source reset printed %q, want the counts line", line)
	}
	if recorded := financeResetRecordedWorld(t, ctx, dsn); recorded != digest {
		t.Fatalf("the file source recorded %q, want %q", recorded, digest)
	}

	// Leg 10: the file source with a wrong sha refuses before anything is
	// dropped.
	fileCounts := financeResetFreshCounts(t, ctx, dsn)
	out.Reset()
	err = financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, fileEnv(map[string]string{
		envFinanceWorldSHA: strings.Repeat("0", 64),
	}))
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("the file source wrong sha: a refusal naming the digest, got %v", err)
	}
	for table, n := range fileCounts {
		if now := financeResetFreshCounts(t, ctx, dsn)[table]; now != n {
			t.Fatalf("the file source wrong sha dropped %s: %d rows before, %d after", table, n, now)
		}
	}

	// Leg 11: a missing file refuses in words, naming the variable, before
	// anything is dropped.
	out.Reset()
	err = financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, fileEnv(map[string]string{
		envFinanceWorldFile: filepath.Join(worldDir, "absent.sql.gz"),
	}))
	if err == nil || !strings.Contains(err.Error(), envFinanceWorldFile) {
		t.Fatalf("the missing file: a refusal naming the variable, got %v", err)
	}
	for table, n := range fileCounts {
		if now := financeResetFreshCounts(t, ctx, dsn)[table]; now != n {
			t.Fatalf("the missing file dropped %s: %d rows before, %d after", table, n, now)
		}
	}

	// Leg 12: no source at all (no file, no URL, and the image's world
	// directory empty of candidates on this host) refuses naming the names,
	// before anything is dropped.
	out.Reset()
	err = financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, worldEnv(map[string]string{
		envFinanceWorldURL:   "",
		envFinanceWorldToken: "",
	}))
	if err == nil || (!strings.Contains(err.Error(), envFinanceWorldFile) && !strings.Contains(err.Error(), envFinanceWorldURL)) {
		t.Fatalf("no source at all: a refusal naming the names, got %v", err)
	}
	for table, n := range fileCounts {
		if now := financeResetFreshCounts(t, ctx, dsn)[table]; now != n {
			t.Fatalf("the sourceless run dropped %s: %d rows before, %d after", table, n, now)
		}
	}
}

// writeWorldGzip gzips a dump into a world file the legs made themselves.
func writeWorldGzip(t *testing.T, path string, dump []byte) error {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(dump); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0600)
}

// financeResetRecordedWorld reads the recorded world digest through a fresh
// connection: the restore drops and recreates the schemas, and a connection
// that prepared against the old shapes reads a stale catalog.
func financeResetRecordedWorld(t *testing.T, ctx context.Context, dsn string) string {
	t.Helper()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("reconnecting for the recorded world: %v", err)
	}
	defer pool.Close()
	recorded, err := settings.NewDealerConfigReader(pool).FinanceWorldSHA256(ctx)
	if err != nil {
		t.Fatalf("the recorded world could not be read: %v", err)
	}
	return recorded
}

// financeResetAuditCount answers how many FINANCE_WORLD_RESET audit rows
// the database carries, through a fresh connection like the counts above.
func financeResetAuditCount(t *testing.T, ctx context.Context, dsn string) int {
	t.Helper()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("reconnecting for the audit rows: %v", err)
	}
	defer pool.Close()
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'FINANCE_WORLD_RESET'`).Scan(&audits); err != nil {
		t.Fatalf("counting the audit rows: %v", err)
	}
	return audits
}

// financeResetFreshCounts counts the check's tables through a fresh
// connection, the way financeResetRecordedWorld reads: every count the legs
// take after a restore has to see the restored shapes, not the stale ones.
func financeResetFreshCounts(t *testing.T, ctx context.Context, dsn string) map[string]int {
	t.Helper()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("reconnecting for the counts: %v", err)
	}
	defer pool.Close()
	return financeCheckCounts(t, ctx, pool)
}
