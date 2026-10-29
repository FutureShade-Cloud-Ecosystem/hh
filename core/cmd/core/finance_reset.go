// finance_reset.go carries the finance-reset role (W2-1b): the one-shot that
// puts the finance box's world back.
//
// The reset is its own command, never a side effect of anything else. It
// refuses before it fetches unless the finance box's own guard passes (the
// environment names the finance box and the staging kind, the sign in is the
// console exchange, and the database carries the box kind mark) and unless
// -confirm names this box's org. It then reads the world's dump: the file
// HH_FINANCE_WORLD_FILE names, or the single *.sql.gz in /srv/world/ (the
// world riding in the image, the boot's source, needing no store and no
// token), or, for local use, the private storage file the environment names,
// fetched with the credential in a request header. It checks the dump's
// sha256 against the one the environment names, and refuses on a mismatch
// BEFORE anything is dropped: a reset that half restored a world is worse
// than a reset that did not run.
//
// The dump is restored with psql, and the database URL never sits on a
// command line: it is parsed into a libpq service file on the shared memory
// tmpfs, mode owner only, and removed the moment the restore is done. The
// fetch credential travels in a request header and is never printed, never
// written to a file and never put on a command line; the file sources have
// no credential at all.
//
// The dump file itself is on disk, never on the shared memory tmpfs: the
// world's decoded dump is larger than the tmpfs the platform sizes by
// default, and the reset must not depend on a size the box does not control.
// The directory is the one HH_FINANCE_RESET_DIR names, or the on-disk
// default. The dump is decoded, written and checked there BEFORE any
// schema is dropped, so a refusal anywhere before the restore leaves the
// database exactly as it was.
//
// The reset prints counts only: how many bytes the decoded dump carried, how
// many schemas it dropped, how many migrations it applied, how many tables
// came back and how many audit rows it wrote. It writes no world value
// anywhere.
//
// After the restore succeeds, the reset records the restored world's sha256
// inside the box, in the same transaction as its audit row, through the
// settings module (the finance_world_sha256 key). The -once flag reads that
// record before it reads the dump: when the box already carries the named
// sha256, the run answers already on world and changes nothing, so a boot
// with the same world revision costs no read and no drop. The digest is the
// sha256 of the decoded SQL, not of the gzip file, so the digest names the
// world however it was packed.

package main

import (
	"bytes"
	"compress/gzip"
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
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// The world dump's four names. The principal sets them only when a reset is
// run; the token's value lives in the vault and is read here, never printed.
// The world also rides in the image itself: a gzip of the dump at
// /srv/world/ (or the single file HH_FINANCE_WORLD_FILE names), so the boot
// path needs no store and no token at all.
const (
	envFinanceWorldURL   = "HH_FINANCE_WORLD_URL"
	envFinanceWorldSHA   = "HH_FINANCE_WORLD_SHA256"
	envFinanceWorldToken = "HH_FINANCE_WORLD_TOKEN"
	envFinanceWorldFile  = "HH_FINANCE_WORLD_FILE"
)

// envFinanceResetDir is where the reset writes the dump file it restores.
// It is on disk and never on the shared memory tmpfs: the world's decoded
// dump is larger than the tmpfs the platform sizes by default, and the reset
// must not depend on a size the box does not control.
const envFinanceResetDir = "HH_FINANCE_RESET_DIR"

// financeResetDumpDir is the on-disk default for that directory: a plain
// directory under the box's own var tree, made by the reset itself when it
// is not there.
const financeResetDumpDir = "/var/lib/finance-box/reset"

// financeWorldDir is where the world rides in the image: the reset reads
// the single *.sql.gz this directory holds when the environment names
// neither a file nor a URL.
const financeWorldDir = "/srv/world"

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
unless -confirm names this box's org slug. It reads the world dump, checks
the digest BEFORE anything is dropped, restores the dump through psql,
reruns the migrations, writes an audit_log row through the audit module,
records the dump's sha256 in system_settings, and prints counts only.

The dump's source, in this order: the file HH_FINANCE_WORLD_FILE names, or
the single *.sql.gz in /srv/world/ (the world riding in the image, the
source the boot uses), or, for local use, the HH_FINANCE_WORLD_URL the
environment names, fetched with the credential in HH_FINANCE_WORLD_TOKEN.
The file sources need no token. When the environment names no file and no
URL, more than one *.sql.gz in /srv/world/ refuses rather than picking one,
and an empty /srv/world/ refuses naming the names.

The digest HH_FINANCE_WORLD_SHA256 (or -sha256) names is the sha256 of the
decoded SQL, not of the gzip file: the digest identifies the world, so
re-gzipping a world does not change it.

The dump is written to disk before anything is dropped, under the directory
HH_FINANCE_RESET_DIR names (or the on-disk default when it names none): the
dump is never held on the shared memory tmpfs, whose size the platform
sizes, not the box. A refusal before the restore leaves the database as it
was.

The dump does not have to carry the box's people. The live box's users are
applied again at its next boot from HH_FINANCE_USERS, so the reset does not
need to keep identity rows.

`)
	fs.PrintDefaults()
}

// financeResetRole is the one-shot: guard, resolve the dump's source, the
// -once skip, confirm, read, check the sha, drop, restore, migrate, audit
// and record, print counts.
func financeResetRole(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("finance-reset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { financeResetUsage(fs) }
	confirm := fs.String("confirm", "", "the org slug of the box to reset (required)")
	sha := fs.String("sha256", "", "the dump's sha256, when the environment does not name it")
	once := fs.Bool("once", false, "exit 0 with already on world when the box already carries the named sha256, changing nothing")
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
	worldFile, err := financeWorldDumpPath(getenv, financeWorldDir)
	if err != nil {
		return err
	}
	expected := strings.TrimSpace(getenv(envFinanceWorldSHA))
	if flagSHA := strings.TrimSpace(*sha); flagSHA != "" {
		if expected != "" && !strings.EqualFold(expected, flagSHA) {
			return fmt.Errorf("finance-reset: -sha256 and %s name different digests", envFinanceWorldSHA)
		}
		expected = flagSHA
	}
	if expected == "" {
		return fmt.Errorf("finance-reset: %s must name the dump's sha256", envFinanceWorldSHA)
	}
	if *once {
		// The recorded world: when the box already carries the named sha256,
		// the reset is done, so the run says so and changes nothing. The
		// comparison happens before the confirm and before the dump is
		// read: the guard above already proved this is the finance box, a
		// skip drops nothing so it does not gate on the world's identity
		// rows (the restore wipes those and the boot's user apply puts
		// them back), and a skip never reads the dump.
		recorded, err := settings.NewDealerConfigReader(pool).FinanceWorldSHA256(ctx)
		if err != nil {
			return fmt.Errorf("finance-reset: the recorded world (%s) could not be read: %w", settings.KeyFinanceWorldSHA256, err)
		}
		if recorded != "" && strings.EqualFold(recorded, expected) {
			fmt.Fprintf(stdout, "already on world %s\n", recorded)
			return nil
		}
	}
	orgSlug, err := financeResetConfirm(getenv, *confirm)
	if err != nil {
		return err
	}
	var dump []byte
	if worldFile != "" {
		dump, err = financeWorldReadFile(worldFile)
	} else {
		dumpURL := strings.TrimSpace(getenv(envFinanceWorldURL))
		token := strings.TrimSpace(getenv(envFinanceWorldToken))
		if dumpURL == "" {
			return fmt.Errorf("finance-reset: %s must name the world dump", envFinanceWorldURL)
		}
		if token == "" {
			return fmt.Errorf("finance-reset: %s must carry the fetch credential", envFinanceWorldToken)
		}
		dump, err = financeWorldFetch(ctx, dumpURL, token)
	}
	if err != nil {
		return fmt.Errorf("finance-reset: the world dump: %w", err)
	}
	sum := sha256.Sum256(dump)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("finance-reset: the dump's sha256 is not the one %s names: nothing was dropped", envFinanceWorldSHA)
	}
	// The dump is mapped onto the box's own roles before it is written: the
	// world's dump carries the build database's assist role names and the
	// build's migrating role, and the box's database has its own.
	boxSuffix, restoringRole, err := financeResetBoxRoles(ctx, pool)
	if err != nil {
		return err
	}
	buildSuffix, err := financeResetBuildSuffix(dump)
	if err != nil {
		return err
	}
	// The mapping folds every default privileges statement onto the restoring
	// role, so a dump that declares them FOR an assist role is refused first, in
	// words, before anything is dropped.
	if err := financeResetDefaultPrivilegesPossible(dump); err != nil {
		return err
	}
	mapped, err := financeResetMapRoles(dump, buildSuffix, boxSuffix, restoringRole)
	if err != nil {
		return err
	}
	// The roles the world's grants name have to be there: the box's own
	// migrate creates them, and a grant to a role that does not exist
	// refuses the restore.
	if err := financeResetAssistRolesPresent(ctx, pool, mapped); err != nil {
		return err
	}
	// The box's own role has to be able to hand the world's objects over to
	// the assist roles: a role may only give an object to a role it can set.
	// The roles are the box's own, the ones the mapping names.
	if err := financeResetOwnershipPossible(ctx, pool, financeResetOwnerRoles(mapped)); err != nil {
		return err
	}
	// The schemas the dump owns: the public schema every dump of this tree's
	// world carries, and every schema the dump creates for itself.
	schemas := dumpOwnedSchemas(dump)
	// Every statement the dump carries has to be one that may run inside the
	// reset's transaction, and the check runs before anything is dropped.
	if err := financeResetTransactionSafe(dump); err != nil {
		return err
	}
	// The recreated public schema has to carry the initdb shape, and giving
	// a schema to pg_database_owner needs a role the box's role can set.
	// The check runs before the drop, so a box that cannot carry the shape
	// is refused with its tables still in place.
	if err := financeResetPublicShapePossible(ctx, pool); err != nil {
		return err
	}
	// The script is written to disk BEFORE anything is dropped. The file is
	// the only copy the restore reads, so a write that fails, a disk that is
	// full or a directory that cannot be made is a refusal with the box
	// still standing: a reset that drops first and writes second empties the
	// box and leaves no way back.
	dumpPath, cleanupDump, err := financeResetWriteDump(financeResetDumpDirectory(getenv), financeResetRestoreScript(mapped, schemas))
	if err != nil {
		return err
	}
	defer cleanupDump()
	// The drop, the recreate and the restore are ONE transaction: psql runs
	// the script with --single-transaction and ON_ERROR_STOP, so a failure
	// part way through the restore rolls the drop back with it. The box
	// keeps the tables and the rows it had; it is never empty and never
	// half restored.
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
	if err := financeResetAudit(ctx, pool2, orgSlug, strings.ToLower(expected)); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "dump_bytes=%d schemas_dropped=%d migrations_applied=%d tables_restored=%d audit_rows=1\n",
		len(dump), len(schemas), applied, tables)
	return nil
}

// financeResetConfirm checks the -confirm against this box's org and answers
// the org slug. The slug comes from the environment the box runs under: a
// confirm that names another org's slug is refused before anything is
// fetched. The anchor itself is not resolved here: the restore wipes the
// identity rows, so the anchor the audit row names is ensured after the
// restore, in the reset's own audit transaction, the way the boot's user
// apply ensures it a step later. A box whose world never carried the anchor
// (a fresh box resetting for the first time) is healed by that ensure, not
// refused.
func financeResetConfirm(getenv func(string) string, confirm string) (string, error) {
	slug := strings.TrimSpace(getenv(envWebOrg))
	if slug == "" {
		return "", fmt.Errorf("finance-reset: %s must name the org slug", envWebOrg)
	}
	if authz.NormalizeOrgSlug(confirm) != authz.NormalizeOrgSlug(slug) {
		return "", fmt.Errorf("finance-reset: -confirm does not name this box's org")
	}
	return slug, nil
}

// financeWorldDumpPath answers where the dump comes from, before anything
// is read. A named file wins; then a named URL answers the empty path (the
// caller fetches over HTTP); then the world riding in the image is the
// single *.sql.gz in worldDir. A directory with no candidate or with more
// than one refuses, naming the names and never picking one: the wrong world
// restored is worse than no restore.
func financeWorldDumpPath(getenv func(string) string, worldDir string) (string, error) {
	if path := strings.TrimSpace(getenv(envFinanceWorldFile)); path != "" {
		return path, nil
	}
	if strings.TrimSpace(getenv(envFinanceWorldURL)) != "" {
		return "", nil
	}
	entries, err := os.ReadDir(worldDir)
	if err != nil {
		return "", fmt.Errorf("finance-reset: no world dump is named: set %s, or %s, or place the world file in %s (%s could not be read: %w)",
			envFinanceWorldFile, envFinanceWorldURL, worldDir, worldDir, err)
	}
	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql.gz") {
			continue
		}
		candidates = append(candidates, entry.Name())
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("finance-reset: no world dump is named: set %s, or %s, or place one world.sql.gz in %s (the directory holds none)",
			envFinanceWorldFile, envFinanceWorldURL, worldDir)
	case 1:
		return filepath.Join(worldDir, candidates[0]), nil
	default:
		return "", fmt.Errorf("finance-reset: %s holds more than one world dump: the reset refuses to pick one, so name %s and try again",
			worldDir, envFinanceWorldFile)
	}
}

// financeWorldReadFile reads a world dump from a file, gzip decoded. A
// missing or unreadable file refuses naming the variable, never the path's
// details; a file that is not a gzip, or decodes to nothing, refuses the
// same way. The digest check happens on the decoded SQL, after this read.
func financeWorldReadFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the world file (%s) could not be read: %w", envFinanceWorldFile, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("the world file (%s) is not a gzip: %w", envFinanceWorldFile, err)
	}
	dump, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("the world file (%s) could not be decoded: %w", envFinanceWorldFile, err)
	}
	if err := zr.Close(); err != nil {
		return nil, fmt.Errorf("the world file (%s) could not be decoded: %w", envFinanceWorldFile, err)
	}
	if len(dump) == 0 {
		return nil, fmt.Errorf("the world file (%s) decodes to nothing", envFinanceWorldFile)
	}
	return dump, nil
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

// financeResetUnsafeInTransaction are the statement heads that may not run
// inside a transaction block. The world's dump carries none of them, and the
// reset refuses before it writes anything if one is there: the restore runs
// in one transaction, and a statement that may not run inside one would
// refuse the script half way through.
var financeResetUnsafeInTransaction = []string{
	"CREATE DATABASE",
	"DROP DATABASE",
	"CREATE INDEX CONCURRENTLY",
	"REINDEX CONCURRENTLY",
	"VACUUM",
	"CLUSTER",
	"ALTER SYSTEM",
	"CREATE TABLESPACE",
	"DROP TABLESPACE",
	"SET TRANSACTION",
}

// financeResetTransactionSafe answers whether every statement the dump
// carries may run inside the reset's transaction. The world's dump is a
// pg_dump of one database: it creates tables, indexes, views, functions,
// policies and triggers, copies the rows in and grants the privileges, and
// every one of those runs inside a transaction. It carries no CREATE
// DATABASE, no CREATE INDEX CONCURRENTLY, no VACUUM, no CLUSTER, no ALTER
// SYSTEM, no tablespace work and no transaction control: the BEGIN and END
// lines in it are the bodies of its plpgsql functions.
func financeResetTransactionSafe(dump []byte) error {
	for _, line := range financeResetNonDataLines(dump) {
		upper := strings.ToUpper(strings.TrimSpace(line))
		for _, head := range financeResetUnsafeInTransaction {
			if strings.HasPrefix(upper, head) {
				return fmt.Errorf("finance-reset: the world's dump carries a statement that may not run inside the reset's transaction (%s): the reset refuses before it drops anything", head)
			}
		}
	}
	return nil
}

// financeResetRestoreScript answers the script the restore runs: the box's
// own schemas are dropped, the public schema is recreated with the initdb
// shape, and the world's dump is restored, all inside ONE transaction.
//
// psql runs the file with --single-transaction and ON_ERROR_STOP, so a
// failure anywhere rolls the whole script back: the box keeps the tables and
// the rows it had, and it is never left empty and never half restored.
func financeResetRestoreScript(mapped []byte, schemas []string) []byte {
	var b strings.Builder
	b.WriteString("-- The world reset: the drop and the restore are one transaction.\n")
	b.WriteString("-- A failure anywhere in this file leaves the box as it was.\n")
	for _, name := range schemas {
		b.WriteString("DROP SCHEMA IF EXISTS ")
		b.WriteString(pgx.Identifier{name}.Sanitize())
		b.WriteString(" CASCADE;\n")
	}
	// The public schema is the one the dump does not create for itself: its
	// tables are named public.something, so the schema has to be there, and
	// it has to carry the shape initdb gave it: pg_database_owner owns it
	// and PUBLIC holds USAGE on it, and nothing else.
	//
	// The assist migrations from 0419 on create their views under a per
	// database view owner role: a role that does not own public and that
	// reads public's tables. Without the grant that role holds no USAGE on
	// the schema, and the first migration that touches public from a non
	// owner role refuses with permission denied for schema public. The grant
	// is made as pg_database_owner, the role initdb makes the grantor, so
	// the schema's ACL is the one a fresh database has.
	b.WriteString("CREATE SCHEMA IF NOT EXISTS public;\n")
	b.WriteString("ALTER SCHEMA public OWNER TO pg_database_owner;\n")
	b.WriteString("SET LOCAL ROLE pg_database_owner;\n")
	b.WriteString("GRANT USAGE ON SCHEMA public TO PUBLIC;\n")
	b.WriteString("RESET ROLE;\n")
	b.Write(mapped)
	return []byte(b.String())
}

// financeResetPublicShapePossible answers whether the reset can give the
// recreated public schema its initdb shape at all: the owner is
// pg_database_owner, and a role may only hand a schema to a role it can
// set. The check is made on a throwaway schema inside a transaction that is
// rolled back, so nothing is left behind, and the answer does not depend on
// the server's major version.
//
// It runs before anything is dropped: a box whose role cannot carry the
// shape is refused with its tables still in place, never with them gone and
// the grant missing.
func financeResetPublicShapePossible(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finance-reset: the public schema's shape could not be checked: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `CREATE SCHEMA finance_reset_probe`); err != nil {
		return fmt.Errorf("finance-reset: the public schema's shape could not be checked: %w", err)
	}
	if _, err := tx.Exec(ctx, `ALTER SCHEMA finance_reset_probe OWNER TO pg_database_owner`); err != nil {
		return fmt.Errorf("finance-reset: this role cannot give the public schema its initdb owner (pg_database_owner), so the reset refuses before it drops anything: %w", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE pg_database_owner`); err != nil {
		return fmt.Errorf("finance-reset: this role cannot act as the public schema's initdb owner, so the reset refuses before it drops anything: %w", err)
	}
	if _, err := tx.Exec(ctx, `GRANT USAGE ON SCHEMA finance_reset_probe TO PUBLIC`); err != nil {
		return fmt.Errorf("finance-reset: this role cannot give the public schema its initdb grant, so the reset refuses before it drops anything: %w", err)
	}
	return nil
}

// financeResetDumpDirectory answers the directory the dump file is written
// under: the one the environment names, or the on-disk default. It is never
// the shared memory tmpfs: the world's decoded dump is larger than the tmpfs
// the platform sizes by default, and a reset that depends on a size the box
// does not control refuses on a box that is big enough to serve.
func financeResetDumpDirectory(getenv func(string) string) string {
	if dir := strings.TrimSpace(getenv(envFinanceResetDir)); dir != "" {
		return dir
	}
	return financeResetDumpDir
}

// financeResetWriteDump writes the dump under the directory the reset is
// given and answers its path and the remover. The directory is made when it
// is not there, the file is written owner only, and the file is read back
// and compared with the bytes that were written: a file the disk cut short
// is caught here, before anything is dropped, not halfway through a restore.
func financeResetWriteDump(dir string, dump []byte) (string, func(), error) {
	if dir == "" {
		dir = financeResetDumpDir
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", func() {}, fmt.Errorf("finance-reset: the dump directory %s could not be made: %w", dir, err)
	}
	file, err := os.CreateTemp(dir, "finance-world-dump-*.sql")
	if err != nil {
		return "", func() {}, fmt.Errorf("finance-reset: the dump file could not be written in %s: %w", dir, err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.Write(dump); err != nil {
		file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("finance-reset: the dump file could not be written in %s: %w", dir, err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("finance-reset: the dump file could not be written in %s: %w", dir, err)
	}
	if err := financeResetDumpIntact(path, dump); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

// financeResetDumpIntact answers whether the file on disk holds exactly the
// bytes it was given: the file is read back and hashed, and the hash is
// compared with the hash of the bytes written. A short write is a refusal
// before the drop, never a half restored world.
func financeResetDumpIntact(path string, dump []byte) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("finance-reset: the dump file %s could not be read back: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return fmt.Errorf("finance-reset: the dump file %s could not be read back: %w", path, err)
	}
	want := sha256.Sum256(dump)
	if !bytes.Equal(sum.Sum(nil), want[:]) {
		return fmt.Errorf("finance-reset: the dump file %s does not hold the dump: nothing was dropped", path)
	}
	return nil
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
	cmd := exec.CommandContext(ctx, "psql", "-v", "ON_ERROR_STOP=1", "--single-transaction", "-f", dumpPath)
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

// financeResetAudit writes the audit row through the audit module and
// records the restored world's sha256 through the settings module, in the
// ONE transaction, and ensures the box's own org anchor in that transaction
// too: the reset is a privileged action on the world's books and the trail
// says so, the record of which world the box now carries commits or rolls
// back with that trail, so the box never records a world the trail does not
// carry, and the anchor the audit row names exists on the restored world the
// way the boot's user apply would ensure it a step later. The restore wiped
// the identity rows, so the ensure is what puts the box's anchor back.
func financeResetAudit(ctx context.Context, pool *pgxpool.Pool, orgSlug, worldSHA string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finance-reset: the audit row: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	orgID, _, _, err := identity.EnsureOrgAnchor(ctx, tx, orgSlug, financeBoxOrgName)
	if err != nil {
		return fmt.Errorf("finance-reset: the org anchor: %w", err)
	}
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
	if err := settings.WriteFinanceWorldSHA256Tx(ctx, tx, worldSHA); err != nil {
		return fmt.Errorf("finance-reset: the recorded world: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finance-reset: the audit row: %w", err)
	}
	return nil
}

// firstLine is the one line of a command's output that reaches a log: the
// line the tool marked as the error, or the first line when it marked none.
// A database URL carries the password, so it is replaced by a name.
//
// The error line and not the first line, because a database answers with
// notices first: a restore that drops a schema says how much it took with
// it before it says what went wrong, and the log has to carry the what.
func firstLine(text, dsn string) string {
	first := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if strings.Contains(line, "ERROR:") || strings.Contains(line, "FATAL:") {
			first = line
			break
		}
	}
	line := first
	if dsn != "" {
		line = strings.ReplaceAll(line, dsn, "the database url")
	}
	if len(line) > 300 {
		line = line[:300]
	}
	return line
}
