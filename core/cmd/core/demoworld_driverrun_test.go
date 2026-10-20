//go:build demo

package main

// demoworld_driverrun_test.go: proof that the demo world gives the Driver persona a run out for
// delivery, on the seed day AND on a later day, and that the run walks the whole delivery day
// through the core's real routes as the Driver persona.
//
// THE RULE THIS FILE PROVES. The demo seed gives the Driver one run DISPATCHED for the dealer's
// current day, day keyed like the other day keyed demo rows, so a fresh box and every later day
// both show a run out for delivery. A live Driver card whose walk stops at "Mark loaded" is not
// ready: until the change the Driver's only same day run was planned, and the core refuses Mark
// en route on a run that was never dispatched, so Delivered with proof, Could not deliver and
// Complete run could never be reached from the Driver's own sign in.
//
// The world is built the way a demo box builds it (the frozen migrations, the first apply, the
// real demo populate, the second apply), then the next day's deploy runs the way the box's
// commander does (one apply on the next calendar day of the dealer's zone), and BOTH days are
// walked end to end: the run is out for delivery with its departure stamped, stop one is marked
// loaded and en route and delivered with a typed proof of delivery, stop two is marked loaded
// and en route and closed as not delivered with a reason, and the driver who drove the run
// completes it.
//
// The database gate is the demo world proofs' own variable (the till's and the quote world's
// tests read the same one), and the harness refuses an address that is not local.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// driverPersonaUserID is the 0200 login the demo populate links to the seeded driver D1. The
// literal is repeated here on purpose: if the populate ever moves it, the driver's day stops
// resolving and this proof goes red, which is the point.
const driverPersonaUserID = "d0200000-0000-4000-9000-000000000003"

// driveWorld is the booted demo world plus the Driver persona's client on the real served
// surface. Both proofs in this file boot it the same way a demo box does.
type driveWorld struct {
	pool   *pgxpool.Pool
	driver *tillClient
}

// bootDriveWorld builds the world in the order a demo box builds it (the frozen migrations, the
// first apply, the real demo populate, the second apply) and hands back the Driver persona's
// client on the full registered surface.
func bootDriveWorld(t *testing.T) *driveWorld {
	t.Helper()
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	vaultKeyHex := hex.EncodeToString(rawKey)

	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_driverrun")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", vaultKeyHex)
	t.Setenv("APP_ENV", "dev")
	if _, created := driveApplyAt(t, qwClock); created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	if _, created := driveApplyAt(t, qwClock); created == 0 {
		t.Fatal("the second apply created nothing, the world never landed")
	}

	// The real served surface, and the Driver persona's credential on it.
	appwrite := newAppwriteFake(t)
	appwrite.setTeam(driverPersonaUserID, "org-"+demoOrgSlug)
	keys := testkit.NewKeys(t)
	acceptKey := make([]byte, 32)
	if _, err := rand.Read(acceptKey); err != nil {
		t.Fatalf("generate acceptance key: %v", err)
	}
	env := &compEnv{
		keys:   keys,
		minter: &testkit.Minter{Keys: keys, Clock: testkit.NewClock()},
		keySrv: testkit.NewKeyServer(t, keys), appwrite: appwrite, dsn: testDSN,
	}
	values := map[string]string{
		"CORE_TOKEN_ISSUER":      testkit.KitIssuer,
		"CORE_STAFF_AUDIENCE":    testkit.KitStaffAud,
		"CORE_TENANT_AUDIENCE":   testkit.KitTenantAud,
		"CORE_JWKS_URL":          env.keySrv.URL(),
		"CORE_DATABASE_URL":      testDSN,
		"CORE_APPWRITE_ENDPOINT": appwrite.server.URL,
		"CORE_APPWRITE_PROJECT":  "fx-appwrite-project",
		"CORE_APPWRITE_API_KEY":  "fx-appwrite-key-placeholder",
		customerquote.EnvKeyID:   "fx-accept-sign-key",
		customerquote.EnvKey:     hex.EncodeToString(acceptKey),
	}
	consoleSessionValues(t, env.minter, values)
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)
	return &driveWorld{
		pool:   pool,
		driver: &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, driverPersonaUserID), who: "Driver"},
	}
}

// driveApplyAt runs `demoseed -apply` the way the entrypoint does, at the caller's clock. The
// quote world's qwApply pins the shared qwClock; the second day's deploy needs its own instant,
// which is the whole claim under test.
func driveApplyAt(t *testing.T, at time.Time) (proposed, created int) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "demoseed-out")
	if err != nil {
		t.Fatalf("temp output: %v", err)
	}
	defer f.Close()
	if err := demoseedRole([]string{"-apply", "-now", at.Format(time.RFC3339)}, f, os.Getenv); err != nil {
		t.Fatalf("demoseed -apply: %v", err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("reading demoseed output: %v", err)
	}
	var skipped int
	for _, line := range strings.Split(string(raw), "\n") {
		if n, err := fmt.Sscanf(line, "applied: %d proposed · %d created · %d already present",
			&proposed, &created, &skipped); err == nil && n == 3 {
			return proposed, created
		}
	}
	t.Fatalf("demoseed -apply printed no result line:\n%s", raw)
	return 0, 0
}

// driveRun and driveStop mirror the fields of the driver's own reads this proof walks.
type driveRun struct {
	ID           string      `json:"id"`
	RunNo        string      `json:"run_no"`
	Status       string      `json:"status"`
	DispatchedAt *string     `json:"dispatched_at"`
	Stops        []driveStop `json:"stops"`
}

type driveStop struct {
	ID            string `json:"id"`
	StopNo        int    `json:"stop_no"`
	Status        string `json:"status"`
	PODReceivedBy string `json:"pod_received_by"`
	FailureReason string `json:"failure_reason"`
}

// TestDemoWorld_DriverRunOutForDelivery walks the Driver's delivery day, end to end, on the seed
// day and again on the next day's deploy, through the core's real routes as the Driver persona.
func TestDemoWorld_DriverRunOutForDelivery(t *testing.T) {
	world := bootDriveWorld(t)
	pool, driver := world.pool, world.driver
	ctx := context.Background()

	// The driver record the persona reaches, so the walk's refusals can name the linkage it
	// depended on rather than leaving a reader to infer it.
	var linkedRuns int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_runs r
		  JOIN dispatch_drivers d ON d.id = r.driver_id
		  JOIN users u ON u.id = d.user_id
		 WHERE u.id = $1 AND d.is_active`, driverPersonaUserID).Scan(&linkedRuns); err != nil {
		t.Fatalf("reading the persona's driver record: %v", err)
	}
	if linkedRuns == 0 {
		t.Fatal("the Driver persona reaches no run, the walk below would assert nothing about a driver's day")
	}

	// walkTheDay is the driver's whole delivery day, the same story on both days: the run is out
	// for delivery, one stop delivers with proof, one stop is handed back with a reason, the run
	// is completed by the driver who drove it.
	walkTheDay := func(label string, day time.Time) {
		t.Helper()
		date := day.In(seedLocation()).Format("2006-01-02")

		rsp := driver.call("GET", "/api/v1/dispatch/my-runs?date="+date, nil)
		rsp.ok(t, http.StatusOK)
		var list struct {
			Items []driveRun `json:"items"`
		}
		rsp.decode(t, &list)
		if len(list.Items) == 0 {
			t.Fatalf("%s: the Driver's day %s holds no run, a fresh box must show a run out for delivery", label, date)
		}
		run := list.Items[0]
		if run.Status != "DISPATCHED" {
			t.Fatalf("%s: the first run of the Driver's day (%s) is %s, want DISPATCHED: a live Driver card whose "+
				"walk stops at Mark loaded is not the demo this box promises", label, run.RunNo, run.Status)
		}
		if run.DispatchedAt == nil || *run.DispatchedAt == "" {
			t.Fatalf("%s: the dispatched run %s carries no departure stamp, the core's own dispatch always sets one", label, run.RunNo)
		}
		if len(run.Stops) != 3 {
			t.Fatalf("%s: the dispatched run %s carries %d stops, the walk needs three: two to deliver and one to hand back",
				label, run.RunNo, len(run.Stops))
		}

		// Stop one: loaded, en route, delivered with a typed proof of delivery.
		first := run.Stops[0]
		for _, move := range []struct{ to, word string }{{"LOADED", "loaded"}, {"EN_ROUTE", "en route"}} {
			m := driver.call("POST", "/api/v1/dispatch/stops/"+first.ID+"/status",
				map[string]string{"status": move.to, "reason": ""})
			m.ok(t, http.StatusOK)
		}
		const receiver = "Receiving clerk (demo)"
		pod := driver.call("POST", "/api/v1/dispatch/stops/"+first.ID+"/pod",
			map[string]any{"received_by": receiver, "affirmed": true})
		pod.ok(t, http.StatusOK)
		var after driveStop
		pod.decode(t, &after)
		if after.Status != "DELIVERED" || after.PODReceivedBy != receiver {
			t.Fatalf("%s: the proof of delivery left stop %d %q signed by %q, want DELIVERED signed by %q",
				label, first.StopNo, after.Status, after.PODReceivedBy, receiver)
		}

		// Stop two: loaded, en route, closed as not delivered with a reason.
		second := run.Stops[1]
		for _, move := range []string{"LOADED", "EN_ROUTE"} {
			m := driver.call("POST", "/api/v1/dispatch/stops/"+second.ID+"/status",
				map[string]string{"status": move, "reason": ""})
			m.ok(t, http.StatusOK)
		}
		const reason = "Nobody on site (demo)"
		f := driver.call("POST", "/api/v1/dispatch/stops/"+second.ID+"/status",
			map[string]string{"status": "FAILED", "reason": reason})
		f.ok(t, http.StatusOK)
		var failed driveStop
		f.decode(t, &failed)
		if failed.Status != "FAILED" || failed.FailureReason != reason {
			t.Fatalf("%s: the handback left stop %d %q with reason %q, want FAILED with %q",
				label, second.StopNo, failed.Status, failed.FailureReason, reason)
		}

		// Stop three: loaded, en route, delivered with a typed proof of delivery.
		third := run.Stops[2]
		for _, move := range []string{"LOADED", "EN_ROUTE"} {
			m := driver.call("POST", "/api/v1/dispatch/stops/"+third.ID+"/status",
				map[string]string{"status": move, "reason": ""})
			m.ok(t, http.StatusOK)
		}
		driver.call("POST", "/api/v1/dispatch/stops/"+third.ID+"/pod",
			map[string]any{"received_by": receiver, "affirmed": true}).ok(t, http.StatusOK)

		// The run closes, by the driver who drove it.
		done := driver.call("POST", "/api/v1/dispatch/runs/"+run.ID+"/complete", nil)
		done.ok(t, http.StatusOK)
		var closed driveRun
		done.decode(t, &closed)
		if closed.Status != "COMPLETED" {
			t.Fatalf("%s: Complete run left %s %s, want COMPLETED", label, run.RunNo, closed.Status)
		}

		// The sync legs the offline pull predicates on: the demo org on the row (the seed's own stamp,
		// read off the org row the populate provisions, never a constant) and the seq the walk's own
		// moves just drew. Both must sit on the run AND on its stops, or the driver's sync scopes
		// answer nothing for the day and the offline half of the run goes dark on the demo box.
		var orgID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
			t.Fatalf("%s: reading the demo org the populate provisions: %v", label, err)
		}
		var runsStamped, stopsStamped int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM delivery_runs WHERE id = $1 AND org_id = $2 AND seq IS NOT NULL`,
			run.ID, orgID).Scan(&runsStamped); err != nil {
			t.Fatalf("%s: reading the run's sync legs: %v", label, err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM delivery_stops WHERE run_id = $1 AND org_id = $2 AND seq IS NOT NULL`,
			run.ID, orgID).Scan(&stopsStamped); err != nil {
			t.Fatalf("%s: reading the stops' sync legs: %v", label, err)
		}
		if runsStamped != 1 || stopsStamped != len(run.Stops) {
			t.Fatalf("%s: the walked run %s is not in the driver's sync scopes: %d of 1 run and %d of %d stops carry "+
				"the demo org and a seq, so an offline pull answers the day as empty",
				label, run.RunNo, runsStamped, stopsStamped, len(run.Stops))
		}
	}

	// The seed day.
	walkTheDay("the seed day", qwClock)

	// The next day's deploy, the way the commander runs it: an apply on the next calendar day,
	// which adds that day's day keyed rows and leaves the stable world alone.
	nextDay := qwClock.AddDate(0, 0, 1)
	if _, created := driveApplyAt(t, nextDay); created == 0 {
		t.Fatal("the next day's apply created nothing, the Driver's run would go dark after the first day")
	}
	walkTheDay("the next day", nextDay)
}

// -- the money half (ruling 91) -------------------------------------------------------------------
//
// TestDemoWorld_DriverRunPayloadsCarryNoMoney proves the Driver's payloads on the seeded stops of
// the dispatched run carry no money: no owed amount, no deposit pair, no door money. The keys are
// the ones the core's own fence names: the dispatch cost gate's figure (amount_due_cents, the
// byte-absent-not-zero shape cost_gate.go rules), and ruling 91's pair (deposit_cents and
// previous_deposit_cents, the skid answer) with the held pick ticket's still_owed_cents beside
// them. The sweep half of the ruling rides along: no key of ANY cents shape and no money word as a
// key anywhere in the driver's own reads, and no figure formatted as money in any value, the same
// list the drive run app's fence scans.
func TestDemoWorld_DriverRunPayloadsCarryNoMoney(t *testing.T) {
	world := bootDriveWorld(t)
	driver, pool := world.driver, world.pool
	ctx := context.Background()

	// The dispatched run of the day, and its stops: the payloads under test. The precondition
	// fails loudly on a world without the run, so the leg can never pass vacuously.
	rsp := driver.call("GET", "/api/v1/dispatch/my-runs?date="+qwClock.In(seedLocation()).Format("2006-01-02"), nil)
	rsp.ok(t, http.StatusOK)
	var list struct {
		Items []driveRun `json:"items"`
	}
	rsp.decode(t, &list)
	if len(list.Items) == 0 || list.Items[0].Status != "DISPATCHED" {
		t.Fatalf("the Driver's day holds no dispatched run (%d runs, first %s), there is no seeded "+
			"stop whose payload this leg could hold to the rule", len(list.Items), firstStatus(list.Items))
	}
	run := list.Items[0]
	if len(run.Stops) == 0 {
		t.Fatal("the dispatched run carries no stop, the leg would pass vacuously")
	}

	// Every read the Driver's day can obtain from the core, held to the fence: the day itself
	// (my-runs, whose items already carry each stop's whole payload shape) and the run detail.
	// The per stop detail read and both payment hold reads are dispatch-board reads the Driver is
	// refused by the role fence, which is the strongest shape the rule can take for them: the
	// driver cannot even ask for the payload the cost gate's figure would arrive on.
	type payload struct {
		label string
		body  []byte
	}
	scans := []payload{{"my-runs", rsp.body}}
	for _, stop := range run.Stops {
		refused := driver.call("GET", "/api/v1/dispatch/stops/"+stop.ID, nil)
		refused.ok(t, http.StatusForbidden)
		hold := driver.call("GET", "/api/v1/dispatch/stops/"+stop.ID+"/payment-hold", nil)
		hold.ok(t, http.StatusForbidden)
	}
	runDetail := driver.call("GET", "/api/v1/dispatch/runs/"+run.ID, nil)
	runDetail.ok(t, http.StatusOK)
	scans = append(scans, payload{"run detail", runDetail.body})
	holds := driver.call("GET", "/api/v1/dispatch/runs/"+run.ID+"/payment-holds", nil)
	holds.ok(t, http.StatusForbidden)

	// And the world agrees with the fence: the day's stops are about orders that owe nothing, so
	// the hold a permitted caller would read is "not held" and wordless, the gate's own facts (the
	// open invoice balance and the credit hold) both absent for them.
	var invoiced, held int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE i.id IS NOT NULL),
		       count(*) FILTER (WHERE h.status = 'ACTIVE')
		  FROM delivery_stops s
		  JOIN orders o ON o.id = s.order_id AND o.metadata->>'demo_seed' = $1
		  LEFT JOIN invoices i ON i.order_id = o.id
		  LEFT JOIN credit_holds h ON h.customer_id = o.customer_id
		 WHERE s.run_id = $2`, demoMarker, run.ID).Scan(&invoiced, &held); err != nil {
		t.Fatalf("reading the day's orders against the gate's facts: %v", err)
	}
	if invoiced != 0 || held != 0 {
		t.Errorf("the dispatched run's orders carry %d invoice rows and %d active credit holds, its stops would read held at the door",
			invoiced, held)
	}

	for _, scan := range scans {
		driveScanPayloadKeys(t, scan.label, scan.body)
	}
}

// firstStatus names the first run's status for the refusal above, or none.
func firstStatus(runs []driveRun) string {
	if len(runs) == 0 {
		return "no runs"
	}
	return runs[0].Status
}

// driveMoneyKeys is the key list the fences name (the figure's tag is on AmountDueCents in
// dispatch/model_w3.go, the held pick ticket's and the skid answer's in tasks/yard.go and
// tasks/yard_handler.go) and is hand copied: TestDemoWorld_DriveMoneyKeysCoverTheModelTags below
// fails when a money looking json tag in those packages is not matched by an entry here.
//
// It is the list of key words the scan holds payloads to: the dispatch cost gate's figure, ruling 91's
// deposit pair and the held pick ticket's owed figure, and the wider money words the drive run
// app's own fence scans (helpers.ts), held here against the KEYS of the payloads the core sends.
var driveMoneyKeys = []string{
	"amount_due_cents", "still_owed_cents", "deposit_cents", "previous_deposit_cents",
	"value_cents", "freight_cents", "total_cents", "unit_price", "margin", "cost", "price",
	"cents", "currency", "dollar", "amount", "balance",
}

// driveMoneyTag picks the json tags that look like money from the model files.
var driveMoneyTag = regexp.MustCompile(`json:"([a-z_]*(?:cents|deposit|owed|amount|price|cost|margin|balance|dollar|currency)[a-z_]*)`)

// TestDemoWorld_DriveMoneyKeysCoverTheModelTags keeps driveMoneyKeys from drifting: every money
// looking json tag in the dispatch and tasks models must contain an entry of the list, so a
// renamed or new money key cannot slip past the Driver payload scan unnoticed. It reads source
// files only and needs no database.
func TestDemoWorld_DriveMoneyKeysCoverTheModelTags(t *testing.T) {
	var seen int
	for _, dir := range []string{"../../internal/dispatch", "../../internal/tasks"} {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(dir + "/" + name)
			if err != nil {
				t.Fatalf("reading %s/%s: %v", dir, name, err)
			}
			for _, m := range driveMoneyTag.FindAllStringSubmatch(string(src), -1) {
				seen++
				covered := false
				for _, word := range driveMoneyKeys {
					if strings.Contains(m[1], word) {
						covered = true
						break
					}
				}
				if !covered {
					t.Errorf("%s/%s: the json tag %q looks like money and matches no entry of driveMoneyKeys", dir, name, m[1])
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no money looking json tag found in the dispatch and tasks models, the check would pass vacuously")
	}
}

// driveMoneyFigure is a figure formatted as money: a dollar sign, digits and two decimals, the
// same shape the app's fence matches.
var driveMoneyFigure = regexp.MustCompile(`\$[ ]?[0-9][0-9,]*\.[0-9]{2}\b`)

// driveScanPayloadKeys walks one JSON payload the core sent the Driver and fails naming the read,
// the key and the path for every money key or money figure it finds.
func driveScanPayloadKeys(t *testing.T, label string, body []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%s: the payload does not decode as JSON: %v", label, err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				lower := strings.ToLower(k)
				for _, word := range driveMoneyKeys {
					if strings.Contains(lower, word) {
						t.Errorf("%s: the key %s at %s carries the money word %q, the Driver's payloads carry no money",
							label, k, path, word)
					}
				}
				walk(path+"."+k, child)
			}
		case []any:
			for i, child := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), child)
			}
		case string:
			if driveMoneyFigure.MatchString(x) {
				t.Errorf("%s: the value at %s is a money figure (%q)", label, path, x)
			}
		}
	}
	walk("$", v)
}
