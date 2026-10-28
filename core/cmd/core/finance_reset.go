// finance_reset.go carries the finance-reset role (W2-1b): the one-shot that
// puts the finance box's world back.
//
// The reset is its own command, never a side effect of anything else. It
// refuses before it fetches unless the finance box's own guard passes (the
// environment names the finance box and the staging kind, the sign in is the
// console exchange, and the database carries the box kind mark) and unless
// -confirm names this box's org. It then reads the world's dump from the
// private storage file the environment names, checks the dump's sha256
// against the one the environment names, and refuses on a mismatch BEFORE
// anything is dropped: a reset that half restored a world is worse than a
// reset that did not run.
//
// The dump is restored with psql, and the database URL never sits on a
// command line: it is parsed into a libpq service file on the shared memory
// tmpfs, mode owner only, and removed the moment the restore is done. The
// fetch credential travels in a request header and is never printed, never
// written to a file and never put on a command line.
//
// The reset prints counts only: how many bytes it fetched, how many schemas
// it dropped, how many migrations it applied, how many tables came back and
// how many audit rows it wrote. It writes no world value anywhere.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
)

// The world dump's three names. The principal sets them only when a reset is
// run; the token's value lives in the vault and is read here, never printed.
const (
	envFinanceWorldURL   = "HH_FINANCE_WORLD_URL"
	envFinanceWorldSHA   = "HH_FINANCE_WORLD_SHA256"
	envFinanceWorldToken = "HH_FINANCE_WORLD_TOKEN"
)

// pgServiceName is the libpq service name the restore reads. It names the
// service, not the target, so it says nothing about the database.
const pgServiceName = "finance-box"

// financeResetUsage is the command's help. It says the one thing a reader of
// the help has to know before running a reset: the box's people are not in
// the dump and do not have to be, because the next boot applies them again
// from HH_FINANCE_USERS. A reader who expected the dump to carry the
// identities would otherwise read the restored box as broken.
func financeResetUsage(fs *flag.FlagSet) {
	fmt.Fprint(fs.Output(), `finance-reset puts the finance box's world back.

It refuses unless the finance box's own guard passes (HH_BOX_KIND=finance,
APP_ENV=staging, the console sign in and the database's own box_kind) and
unless -confirm names this box's org slug. It reads the world dump the
environment names (HH_FINANCE_WORLD_URL, HH_FINANCE_WORLD_SHA256 and
HH_FINANCE_WORLD_TOKEN), checks the digest BEFORE anything is dropped,
restores the dump through psql, reruns the migrations, writes an audit_log
row through the audit module, and prints counts only.

The dump does not have to carry the box's people. The live box's users are
applied again at its next boot from HH_FINANCE_USERS, so the reset does not
need to keep identity rows.

`)
	fs.PrintDefaults()
}

// financeResetRole is the one-shot: guard, confirm, fetch, check the sha,
// drop, restore, migrate, audit, print counts.
func financeResetRole(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("finance-reset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { financeResetUsage(fs) }
	confirm := fs.String("confirm", "", "the org slug of the box to reset (required)")
	sha := fs.String("sha256", "", "the dump's sha256, when the environment does not name it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("finance-reset: unexpected arguments %q", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(*confirm) == "" {
		return errors.New("finance-reset: -confirm is required (the org slug of the box to reset)")
	}
	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("finance-reset: %w", err)
	}
	printOneshotDatabaseTarget(stderr, "finance-reset", dsn)
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("finance-reset: cannot connect: %w", err)
	}
	defer pool.Close()
	if err := financeBoxReady(ctx, "finance-reset", getenv, pool); err != nil {
		return err
	}
	orgID, err := financeResetConfirm(ctx, pool, getenv, *confirm)
	if err != nil {
		return err
	}
	dumpURL := strings.TrimSpace(getenv(envFinanceWorldURL))
	token := strings.TrimSpace(getenv(envFinanceWorldToken))
	expected := strings.TrimSpace(getenv(envFinanceWorldSHA))
	if flagSHA := strings.TrimSpace(*sha); flagSHA != "" {
		if expected != "" && !strings.EqualFold(expected, flagSHA) {
			return fmt.Errorf("finance-reset: -sha256 and %s name different digests", envFinanceWorldSHA)
		}
		expected = flagSHA
	}
	if dumpURL == "" {
		return fmt.Errorf("finance-reset: %s must name the world dump", envFinanceWorldURL)
	}
	if token == "" {
		return fmt.Errorf("finance-reset: %s must carry the fetch credential", envFinanceWorldToken)
	}
	if expected == "" {
		return fmt.Errorf("finance-reset: %s must name the dump's sha256", envFinanceWorldSHA)
	}
	dump, err := financeWorldFetch(ctx, dumpURL, token)
	if err != nil {
		return fmt.Errorf("finance-reset: the world dump: %w", err)
	}
	sum := sha256.Sum256(dump)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("finance-reset: the dump's sha256 is not the one %s names: nothing was dropped", envFinanceWorldSHA)
	}
	// The schemas the dump owns: the public schema every dump of this tree's
	// world carries, and every schema the dump creates for itself.
	schemas := dumpOwnedSchemas(dump)
	dropped, err := financeResetDropSchemas(ctx, pool, schemas)
	if err != nil {
		return err
	}
	dumpPath, cleanupDump, err := financeResetWriteDump(dump)
	if err != nil {
		return err
	}
	defer cleanupDump()
	if err := financeResetRestore(ctx, dsn, dumpPath); err != nil {
		return err
	}
	// A fresh pool: the restore dropped and recreated the schemas the old
	// connections had prepared against.
	pool.Close()
	pool2, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("finance-reset: cannot reconnect after the restore: %w", err)
	}
	defer pool2.Close()
	applied, err := applyMigrations(ctx, pool2, io.Discard)
	if err != nil {
		return fmt.Errorf("finance-reset: the migrations: %w", err)
	}
	tables, err := financeResetTableCount(ctx, pool2)
	if err != nil {
		return err
	}
	if err := financeResetAudit(ctx, pool2, orgID); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "dump_bytes=%d schemas_dropped=%d migrations_applied=%d tables_restored=%d audit_rows=1\n",
		len(dump), dropped, applied, tables)
	return nil
}

// financeResetConfirm checks the -confirm against this box's org and answers
// the org's id. The slug comes from the environment the box runs under, and
// the database has to carry it: a confirm that names another org's slug, or
// an org this database does not have, is refused before anything is fetched.
func financeResetConfirm(ctx context.Context, pool *pgxpool.Pool, getenv func(string) string, confirm string) (string, error) {
	slug := strings.TrimSpace(getenv(envWebOrg))
	if slug == "" {
		return "", fmt.Errorf("finance-reset: %s must name the org slug", envWebOrg)
	}
	if authz.NormalizeOrgSlug(confirm) != authz.NormalizeOrgSlug(slug) {
		return "", fmt.Errorf("finance-reset: -confirm does not name this box's org")
	}
	resolver, err := authz.NewPgxOrgResolver(pool)
	if err != nil {
		return "", fmt.Errorf("finance-reset: the org resolver could not be built: %w", err)
	}
	orgID, ok, err := resolver.ResolveSlug(ctx, slug)
	if err != nil {
		return "", fmt.Errorf("finance-reset: the org anchor could not be read: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("finance-reset: this database carries no org by that slug")
	}
	return orgID, nil
}

// financeWorldFetch reads the dump. The credential travels in a request
// header and never reaches a log, a file or a command line.
func financeWorldFetch(ctx context.Context, dumpURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dumpURL, nil)
	if err != nil {
		return nil, fmt.Errorf("the dump address could not be read: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the dump could not be fetched: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the dump could not be fetched (the storage answered %s)", resp.Status)
	}
	dump, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("the dump could not be read: %w", err)
	}
	if len(dump) == 0 {
		return nil, errors.New("the dump is empty")
	}
	return dump, nil
}

// dumpOwnedSchemas are the schemas the dump owns: the public schema every
// dump of this tree's world carries, and every schema the dump creates for
// itself.
func dumpOwnedSchemas(dump []byte) []string {
	seen := map[string]bool{"public": true}
	var out []string
	for _, line := range strings.Split(string(dump), "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if !strings.HasPrefix(upper, "CREATE SCHEMA ") {
			continue
		}
		rest := strings.TrimSpace(trimmed[len("CREATE SCHEMA "):])
		rest = strings.TrimPrefix(rest, "IF NOT EXISTS ")
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), ";"))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return append([]string{"public"}, out...)
}

// financeResetDropSchemas drops the schemas the dump owns and answers how
// many it dropped. It runs only after the dump's sha256 matched.
func financeResetDropSchemas(ctx context.Context, pool *pgxpool.Pool, schemas []string) (int, error) {
	dropped := 0
	for _, name := range schemas {
		if _, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pgx.Identifier{name}.Sanitize())); err != nil {
			return dropped, fmt.Errorf("finance-reset: the schema %s could not be dropped: %w", name, err)
		}
		dropped++
	}
	// The public schema is the one the dump does not create for itself: its
	// tables are named public.something, so the schema has to be there.
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS public"); err != nil {
		return dropped, fmt.Errorf("finance-reset: the public schema could not be recreated: %w", err)
	}
	return dropped, nil
}

// financeResetWriteDump writes the dump to the shared memory tmpfs and
// answers its path and the remover.
func financeResetWriteDump(dump []byte) (string, func(), error) {
	dir := "/dev/shm"
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		dir = ""
	}
	file, err := os.CreateTemp(dir, "finance-world-dump-*.sql")
	if err != nil {
		return "", func() {}, fmt.Errorf("finance-reset: the dump file could not be written: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.Write(dump); err != nil {
		file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("finance-reset: the dump file could not be written: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("finance-reset: the dump file could not be written: %w", err)
	}
	return path, cleanup, nil
}

// financeResetRestore restores the dump with psql. The database URL never
// sits on a command line: psql reads the connection from a libpq service
// file on the shared memory tmpfs, mode owner only, and the file is removed
// the moment the restore is done.
func financeResetRestore(ctx context.Context, dsn, dumpPath string) error {
	servicePath, cleanup, err := libpqServiceFile(dsn)
	if err != nil {
		return fmt.Errorf("finance-reset: %w", err)
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, "psql", "-v", "ON_ERROR_STOP=1", "-f", dumpPath)
	cmd.Env = append(os.Environ(), "PGSERVICEFILE="+servicePath, "PGSERVICE="+pgServiceName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("finance-reset: the restore failed (%s): %s", err, firstLine(stderr.String(), dsn))
	}
	return nil
}

// financeResetTableCount answers how many tables the restored world carries.
func financeResetTableCount(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var tables int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`).Scan(&tables); err != nil {
		return 0, fmt.Errorf("finance-reset: the restored table count: %w", err)
	}
	return tables, nil
}

// financeResetAudit writes the audit row through the audit module: the reset
// is a privileged action on the world's books and the trail says so.
func financeResetAudit(ctx context.Context, pool *pgxpool.Pool, orgID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finance-reset: the audit row: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	svc := audit.NewService(audit.NewRepository(pool), slog.Default())
	if err := svc.RecordTx(ctx, tx, audit.SensitiveAccess{
		Action:     "FINANCE_WORLD_RESET",
		EntityType: "finance.world",
		EntityID:   orgID,
		Purpose:    "FINANCE_WORLD_RESET",
		Extra:      map[string]any{"status": "restored"},
	}); err != nil {
		return fmt.Errorf("finance-reset: the audit row: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finance-reset: the audit row: %w", err)
	}
	return nil
}

// firstLine is the first line of a text, with the database URL replaced by a
// name: an error message is the one line guaranteed to reach a log, and a
// database URL carries the password.
func firstLine(text, dsn string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if dsn != "" {
		line = strings.ReplaceAll(line, dsn, "the database url")
	}
	if len(line) > 300 {
		line = line[:300]
	}
	return line
}
