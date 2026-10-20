//go:build demo

package main

// demoseed_glsworld_integration_test.go, the demo world seam's proofs on a booted world: the
// branch rooms answer the branch codes, a built box keeps what testers typed, and no text
// column of the world reads a first dealer word. Each builds its own database beside the gate's
// (tillDemoDBEnvVar), the way the other world proofs do.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/boot"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
	"github.com/jackc/pgx/v5/pgxpool"
)

const glsJulianBranchID = "5d1b3f6e-0000-4000-9000-000000000002"

// glsBootWorld builds a demo world the way the entrypoint's empty path does: marker, migrate,
// apply, populate, apply.
func glsBootWorld(t *testing.T, ctx context.Context, name string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	pool, testDSN := qwCreateDB(t, ctx, dsn, name)
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("APP_ENV", "demo")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	if err := boot.DemoCheck(ctx, os.LookupEnv); err != nil {
		t.Fatalf("demo-check refused the empty database: %v", err)
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS demo_build_marker (started timestamptz NOT NULL DEFAULT now())`,
		`INSERT INTO demo_build_marker SELECT now() WHERE NOT EXISTS (SELECT 1 FROM demo_build_marker)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("the build marker: %v", err)
		}
	}
	qwMigrateFrozenSet(t, ctx, pool)
	qwApply(t)
	var out bytes.Buffer
	if err := demo.PopulateMain([]string{"-now", qwClock.Format(time.RFC3339)}, &out, os.Getenv); err != nil {
		t.Fatalf("demo-populate: %v", err)
	}
	qwApply(t)
	return pool
}

// TestDemoWorld_BranchRoomsAnswerTheBranchCodes: the messaging module derives every branch room
// from the branch CODE (yard-<code>, house-<code>, payments-<code>, sales-<code>), so the rooms the
// world seeds must carry the codes the world's branches carry. One yard notice, one counter notice
// and one branchless notice are posted through the composition root's own notifier and found in
// the rooms.
func TestDemoWorld_BranchRoomsAnswerTheBranchCodes(t *testing.T) {
	ctx := context.Background()
	pool := glsBootWorld(t, ctx, "demoworld_rooms")
	repo := messaging.NewRepository(pool)
	svc := messaging.NewService(repo)

	for _, b := range []struct{ id, code string }{{trentonBranchID, "plm"}, {glsJulianBranchID, "jul"}} {
		yard, err := svc.YardSlugForBranch(ctx, b.id)
		if err != nil || yard != "yard-"+b.code {
			t.Fatalf("the yard slug of branch %s reads %q (err %v), want yard-%s", b.code, yard, err, b.code)
		}
		house, err := repo.BranchHouseSlug(ctx, b.id)
		if err != nil || house != "house-"+b.code {
			t.Errorf("the house room of branch %s reads %q (err %v), want house-%s, not the global floor", b.code, house, err, b.code)
		}
		for _, slug := range []string{yard, house, "payments-" + b.code, tasks.CounterSlugForYardSlug(yard), "drivers-" + b.code} {
			if n := qwCount(t, ctx, pool, `SELECT count(*) FROM msg_channels WHERE slug = $1`, slug); n != 1 {
				t.Errorf("branch %s: the room %q is on the world %d times, want once", b.code, slug, n)
			}
		}
	}

	// The depots the distance estimates start from sit in San Diego County, not at the first
	// dealer's town centres (migration 0093 plants those; the build-only pass moves them).
	for _, id := range []string{trentonBranchID, glsJulianBranchID} {
		var lat, lng float64
		if err := pool.QueryRow(ctx, `SELECT latitude, longitude FROM dispatch_branch_depots WHERE branch_id = $1::uuid`, id).Scan(&lat, &lng); err != nil {
			t.Fatalf("the depot of %s: %v", id, err)
		}
		if lat < 32.5 || lat > 33.5 || lng < -117.6 || lng > -116.0 {
			t.Errorf("the depot of %s sits at %.4f, %.4f, outside San Diego County", id, lat, lng)
		}
	}

	notifier := yardChatNotifier{svc: svc, figuresOK: func(string) (bool, error) { return false, nil }}
	post := func(key, branch string, audience tasks.Audience, body string) {
		t.Helper()
		if err := notifier.PostTaskEvent(ctx, tasks.TaskEventRef{
			EventKey: key, BranchID: branch, Topic: "task.created", Entity: "task:" + key,
			DeepLink: "/yard", Summary: body, Audience: audience,
		}); err != nil {
			t.Fatalf("posting %s: %v", key, err)
		}
	}
	post("glsproof:yard", trentonBranchID, tasks.AudienceYard, "A pick is ready at Point Loma")
	post("glsproof:counter", trentonBranchID, tasks.AudienceCounter, "A short pick is waiting at the Point Loma counter")
	post("glsproof:none", "", tasks.AudienceYard, "A branchless task event")
	for _, want := range []struct{ slug, body string }{
		{"yard-plm", "A pick is ready at Point Loma"},
		{"sales-plm", "A short pick is waiting at the Point Loma counter"},
		{tasksDefaultChannelSlug, "A branchless task event"},
	} {
		if n := qwCount(t, ctx, pool, `
			SELECT count(*) FROM msg_messages m JOIN msg_channels c ON c.id = m.channel_id
			 WHERE c.slug = $1 AND m.body = $2`, want.slug, want.body); n != 1 {
			t.Errorf("the notice %q reached room %q %d times, want once", want.body, want.slug, n)
		}
	}
}

// TestDemoWorld_BuiltBoxKeepsTesterRows: the rename of migration-planted rows runs while the world
// is first built and never on a built box. A customer, a vendor and a message a tester typed after
// the build survive the boot's next apply untouched, and a tester value that the dictionary would
// have moved onto an existing key cannot fail the apply.
func TestDemoWorld_BuiltBoxKeepsTesterRows(t *testing.T) {
	ctx := context.Background()
	pool := glsBootWorld(t, ctx, "demoworld_tester_rows")

	const customer = "Kingston Oaks Patio (demo)"
	if _, err := pool.Exec(ctx, `INSERT INTO customers (account_no, name) VALUES ('T-100', $1)`, customer); err != nil {
		t.Fatalf("a tester customer: %v", err)
	}
	// V-UNILOCK is free once the migration vendor moved; the old pass would have moved a tester's
	// vendor of that number onto V-ALDERCREEK, a key that is taken.
	if _, err := pool.Exec(ctx, `INSERT INTO vendors (vendor_no, name) VALUES ('V-UNILOCK', 'Unilock')`); err != nil {
		t.Fatalf("a tester vendor: %v", err)
	}
	const msg = "Truck to Kingston moved, call 613-555-0142"
	if _, err := pool.Exec(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, body)
		SELECT c.id, u.id, $1 FROM msg_channels c, users u WHERE c.slug = 'office' LIMIT 1`, msg); err != nil {
		t.Fatalf("a tester message: %v", err)
	}
	before := qwFingerprint(t, ctx, pool, []string{"customers", "vendors", "msg_messages", "order_lines", "dispatch_branch_depots"})

	qwApply(t) // the boot's next apply on a built box: it must not fail and must not touch the rows
	after := qwFingerprint(t, ctx, pool, []string{"customers", "vendors", "msg_messages", "order_lines", "dispatch_branch_depots"})
	for table, was := range before {
		if after[table] != was {
			t.Errorf("a built box's apply changed table %s", table)
		}
	}
	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM customers WHERE account_no = 'T-100'`).Scan(&name); err != nil || name != customer {
		t.Errorf("the tester's customer reads %q (err %v), want %q untouched", name, err, customer)
	}
}

// TestDemoWorld_NoTextColumnReadsAFirstDealerWord: after the boot, no text column of any table
// except the migration ledger reads a first dealer's word.
func TestDemoWorld_NoTextColumnReadsAFirstDealerWord(t *testing.T) {
	ctx := context.Background()
	pool := glsBootWorld(t, ctx, "demoworld_words")
	pattern := `(?i)` + strings.Join([]string{
		`dibbits`, `quinte`, `trenton`, `belleville`, `kingston`, `brighton`, `marmora`, `cataraqui`,
		`limestone city`, `ontario`, `toronto`, `brampton`, `techo`, `unilock`, `permacon`,
		`\moaks\M`, `brown.s`, `\msika\M`, `rymar`, `alliance gator`, `in-lite`, `banas`, `bistrack`,
		`scrubbed party`, `scrubname`, `best.?way`, `brussels`, `holland`, `courtstone`, `granular a`,
		`northline`, `northland`, `bay ridge`, `hastings`, `613[.-]\d{3}[.-]\d{4}`,
		`\mkgn\M`, `\mtrt\M`,
		`\m[abceghjklmnprstvxy][0-9][abceghjklmnprstvwxyz][ -]?[0-9][abceghjklmnprstvwxyz][0-9]\M`,
		`\mcad\M`, `\mhst\M`,
	}, "|")
	// The marker suffix is gone from the tenant's own entities, and the rebate's name carries no dash.
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM projects WHERE name ~* '\(demo\)'`); n != 0 {
		t.Errorf("%d project name(s) still carry the marker suffix", n)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM rebate_programs WHERE name ~ $1`, "[\u2013\u2014]"); n != 0 {
		t.Errorf("%d rebate name(s) still carry a dash", n)
	}
	rows, err := pool.Query(ctx, `
		SELECT c.table_name, c.column_name FROM information_schema.columns c
		  JOIN information_schema.tables t ON t.table_name = c.table_name AND t.table_schema = c.table_schema
		 WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE' AND c.table_name <> 'schema_migrations'
		   AND c.data_type IN ('text','character varying','citext','jsonb','json','USER-DEFINED')
		 ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("listing the text columns: %v", err)
	}
	type col struct{ table, column string }
	var cols []col
	for rows.Next() {
		var c col
		if err := rows.Scan(&c.table, &c.column); err != nil {
			t.Fatalf("scanning a column: %v", err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	if len(cols) < 100 {
		t.Fatalf("only %d text columns listed, the proof is not looking at the world", len(cols))
	}
	for _, c := range cols {
		if c.table == "posting_rules" && c.column == "line_role" {
			continue
		}
		q := fmt.Sprintf(`SELECT count(*) FROM %q WHERE %q::text ~ $1`, c.table, c.column)
		if n := qwCount(t, ctx, pool, q, pattern); n != 0 {
			t.Errorf("%s.%s reads a first dealer word in %d row(s)", c.table, c.column, n)
		}
	}
}

// TestDemoWorld_TenantConfigRouteNamesTheDealer: the route itself, on the demo graph the box
// serves, answers the demo dealer and not the first dealer's stub.
func TestDemoWorld_TenantConfigRouteNamesTheDealer(t *testing.T) {
	rec, _ := rgDemoGateCase(t)
	code, body := rgDemoProbe(t, rec, "GET", "/api/v1/tenant/config", nil)
	if code != 200 {
		t.Fatalf("GET /api/v1/tenant/config answered %d (%s), want 200", code, body)
	}
	if !strings.Contains(body, `"dealer_name":"Gable Landscape Supply"`) || glsDenylist.MatchString(body) {
		t.Errorf("the demo box's public tenant config reads %s, want the demo dealer and no first dealer word", body)
	}
}
