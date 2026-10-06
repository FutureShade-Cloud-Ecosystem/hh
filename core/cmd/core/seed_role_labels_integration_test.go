//go:build integration

// seed_role_labels_integration_test.go: the ruling is that every demo login and persona carries a role
// label, never a person's name (nobody can tell an invented name from a real one). Migration 0364 did that
// for the rows the migrations plant; this file holds `core seed`, whose embedded SQL plants the "(sample)"
// logins, to the same rule, and holds 0364 to being a one-time rewrite.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run 'SeedRoleLabels|Migration0364SecondRun' -count=1
package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seededLoginLabels is what the seed writes on each login it plants, by fixed id: the role the login holds,
// with the seed's own "(sample)" suffix (the demo populate's logins read "(demo)"). The platform admin row
// is not here: the migrations plant it first and the seed's insert of it is a conflict no-op.
var seededLoginLabels = map[string]string{
	"d7000000-0000-4000-9000-000000000002": "General manager (sample)",
	"d7000000-0000-4000-9000-000000000003": "Sales manager (sample)",
	"d7000000-0000-4000-9000-000000000005": "Yard manager (sample)",
	"d7000000-0000-4000-9000-000000000006": "Financial controller (sample)",
}

// seededPool is a migrated database with the embedded seed applied on top, as `core seed` leaves one.
func seededPool(t *testing.T, suffix string) *pgxpool.Pool {
	t.Helper()
	pool := freshMigratedDB(t, suffix)
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	mustExec(t, context.Background(), pool, string(body))
	return pool
}

// seededPersonNames derives, from the seeded logins' own addresses (first.last@ on the sample domain, on the
// seed's own login ids), the full name each login could carry, so this file holds none. A name shaped address is an identifier the
// seed's guard and the suites key on and stays as it is; only what a person reads is held to the rule.
func seededPersonNames(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (full, first []string) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT email FROM users WHERE id::text LIKE 'd7000000-0000-4000-9000-00000000000%' AND status = 'ACTIVE' AND email LIKE '%@dibbits.example' ORDER BY email`)
	if err != nil {
		t.Fatalf("reading the seeded addresses: %v", err)
	}
	defer rows.Close()
	re := regexp.MustCompile(`^([a-z]+)\.([a-z]+)@`)
	title := func(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			t.Fatalf("scanning an address: %v", err)
		}
		if m := re.FindStringSubmatch(email); m != nil {
			full = append(full, title(m[1])+" "+title(m[2]))
			first = append(first, title(m[1]))
		}
	}
	if len(full) != 4 {
		t.Fatalf("derived %d person shaped logins from the seed, want 4: the leg is not testing what it says", len(full))
	}
	return full, first
}

// Nothing the seed plants may read as a person's name: not a login's display name, not a conversation
// title, not a note. Every text column of every table is scanned for each derived full name and first name,
// and the failure names table and column only. The labels the seed writes are pinned as well, so the scan
// cannot pass by the seed planting nothing.
func TestIntegration_SeedRoleLabelsNoPersonNameInASeededDatabase(t *testing.T) {
	ctx := context.Background()
	pool := seededPool(t, "seedlabels")
	full, first := seededPersonNames(t, ctx, pool)
	for _, n := range full {
		if bad := columnsMatching(t, ctx, pool, `~*`, `\m`+n+`\M`); len(bad) != 0 {
			t.Errorf("after seed, a login's full name still reads in: %s", strings.Join(bad, ", "))
		}
	}
	for _, n := range first {
		if bad := columnsMatching(t, ctx, pool, `~`, `\m`+n+`\M`); len(bad) != 0 {
			t.Errorf("after seed, a login's first name still reads in: %s", strings.Join(bad, ", "))
		}
	}
	for id, want := range seededLoginLabels {
		var got string
		if err := pool.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1::uuid`, id).Scan(&got); err != nil {
			t.Fatalf("reading login %s: %v", id, err)
		}
		if got != want {
			t.Errorf("login %s = %q, want %q", id, got, want)
		}
	}
}

// tableDigest is one md5 over every row of a table, every column included (updated_at too), so a second
// run that touches a row for any reason moves it.
func tableDigest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) string {
	t.Helper()
	var sum string
	if err := pool.QueryRow(ctx, `SELECT coalesce(md5(string_agg(x::text, '|' ORDER BY x.id::text)), 'empty') FROM `+table+` x`).Scan(&sum); err != nil {
		t.Fatalf("digesting %s: %v", table, err)
	}
	return sum
}

// Migration 0364 is a one-time rewrite: a second run matches nothing and moves no row, updated_at included.
// The control makes the digest earn its keep: on a database built WITHOUT 0364 the first run must change
// the three tables, so an unchanged digest after the second run is not the digest being blind.
func TestIntegration_Migration0364SecondRunChangesNothing(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDBWithout(t, "mig0364twice", migration0364File)
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", migration0364File))
	if err != nil {
		t.Fatalf("reading %s: %v", migration0364File, err)
	}
	tables := []string{"users", "dispatch_drivers", "msg_channels"}
	digest := func() map[string]string {
		d := map[string]string{}
		for _, tb := range tables {
			d[tb] = tableDigest(t, ctx, pool, tb)
		}
		return d
	}
	before := digest()
	mustExec(t, ctx, pool, string(body))
	first := digest()
	for _, tb := range tables {
		if first[tb] == before[tb] {
			t.Errorf("control: the first run of 0364 left %s unchanged, so the digest cannot tell a change", tb)
		}
	}
	mustExec(t, ctx, pool, string(body))
	second := digest()
	for _, tb := range tables {
		if second[tb] != first[tb] {
			t.Errorf("a second run of 0364 changed rows in %s", tb)
		}
	}
}
