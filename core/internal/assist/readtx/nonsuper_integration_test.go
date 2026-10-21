//go:build integration

package readtx

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// migrateAs applies every migration file in order, each in one transaction with the
// runner's bookkeeping, as the connection's role: the shape of `core migrate`
// without the binary.
func migrateAs(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations: %v", err)
	}
	sort.Strings(files)
	if _, err := conn.Exec(ctxT(t), `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		tx, err := conn.Begin(ctxT(t))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctxT(t), string(body)); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("apply failed %s: %v", version, err)
		}
		if _, err := tx.Exec(ctxT(t), `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("record failed %s: %v", version, err)
		}
		if err := tx.Commit(ctxT(t)); err != nil {
			t.Fatal(err)
		}
	}
}

// The hardened deployment shape of the PR 536 read: the app is its own non
// superuser CREATEROLE role that owns the dealer database, and the extensions'
// functions belong to the bootstrap superuser. Migration 0418 must apply there;
// until the revoke oneshot runs, the catalogue holds in its open state (exactly the
// extension functions keep PUBLIC EXECUTE); after the oneshot, in its closed state,
// and the verifier still works.
func TestMigrationAsANonSuperuserOwnerAndTheExtensionRevokeOneshot(t *testing.T) {
	gate := gateURL(t)
	root := connect(t, gate)
	app, db := "readtx_app_"+randomHex(t, 4), "readtx_hard_"+randomHex(t, 4)
	pw := randomHex(t, 12)
	roles := RolesFor(db)
	if _, err := root.Exec(ctxT(t), `CREATE ROLE `+pgx.Identifier{app}.Sanitize()+` LOGIN NOSUPERUSER CREATEROLE PASSWORD '`+pw+`'`); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Exec(ctxT(t), `CREATE DATABASE `+pgx.Identifier{db}.Sanitize()+` OWNER `+pgx.Identifier{app}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(ctxT(t), gate)
		if err != nil {
			return
		}
		defer c.Close(ctxT(t))
		_, _ = c.Exec(ctxT(t), `DROP DATABASE IF EXISTS `+pgx.Identifier{db}.Sanitize()+` WITH (FORCE)`)
		for _, r := range []string{roles.Login, roles.ProLogin, roles.Reader, roles.ProReader, roles.ViewOwner, roles.ClaimsOwner, roles.FunctionUsers, roles.ZoneReader, app} {

			_, _ = c.Exec(ctxT(t), `DROP ROLE IF EXISTS `+pgx.Identifier{r}.Sanitize())
		}
	})

	appConn := connect(t, withDatabase(t, withUser(t, gate, app, pw), db))
	migrateAs(t, appConn)

	// Migration 0419 enabled row security on orders and order_lines. The app here is a non superuser that
	// owns them, so it is exempt: row security is not active for it, and it reads both tables (the serve
	// startup check asserts the same property on every policied table).
	for _, table := range []string{"orders", "order_lines", "stock_balances", "product_branch_costs", "quotes", "quote_lines", "purchasing_pos", "purchasing_po_lines", "invoices", "payments", "payment_allocations"} {
		var active bool
		if err := appConn.QueryRow(ctxT(t), `SELECT row_security_active($1::regclass)`, "public."+table).Scan(&active); err != nil || active {
			t.Errorf("row security is active for the owning app role on %s (%v, %v)", table, active, err)
		}
		var n int
		if err := appConn.QueryRow(ctxT(t), `SELECT count(*) FROM public.`+table).Scan(&n); err != nil {
			t.Errorf("the owning app role cannot read %s: %v", table, err)
		}
	}

	got, err := catalogueViolations(ctxT(t), appConn, roles, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		t.Errorf("open state catalogue: %s", v)
	}
	// And the open state is not mistaken for the closed one.
	if closed, err := catalogueViolations(ctxT(t), appConn, roles, false); err != nil || len(closed) == 0 {
		t.Errorf("before the oneshot the closed state catalogue must report the open extension functions (%v, %v)", closed, err)
	}

	// The oneshot, run as the extensions' owner (the bootstrap superuser).
	dbRoot := connect(t, withDatabase(t, gate, db))
	if err := ProvisionRevokeExtensionFunctions(ctxT(t), dbRoot, roles); err != nil {
		t.Fatalf("revoke oneshot: %v", err)
	}
	got, err = catalogueViolations(ctxT(t), appConn, roles, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		t.Errorf("closed state catalogue after the oneshot: %s", v)
	}
	// The verifier still reaches hmac: the claims owner was given it back.
	if _, err := ProvisionClaimsKey(ctxT(t), appConn, redact.NewSecret("hardened-box-key-"+randomHex(t, 16)), nil); err != nil {
		t.Fatalf("claims key as the app role: %v", err)
	}
	key := "hardened-box-key-" + randomHex(t, 16)
	if _, err := ProvisionClaimsKey(ctxT(t), appConn, redact.NewSecret(key), nil); err != nil {
		t.Fatal(err)
	}
	pid := backendPID(t, dbRoot)
	if ok, _ := verified(t, dbRoot, rawSigned(int(KeyID(redact.NewSecret(key))), key, goodPayload(pid))); !ok {
		t.Error("the verifier no longer works after the revoke oneshot")
	}
}
