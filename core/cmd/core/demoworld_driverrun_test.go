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
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// driverPersonaUserID is the 0200 login the demo populate links to the seeded driver D1. The
// literal is repeated here on purpose: if the populate ever moves it, the driver's day stops
// resolving and this proof goes red, which is the point.
const driverPersonaUserID = "d0200000-0000-4000-9000-000000000003"

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

	// The world, in the order a demo box builds it: migrate, first apply (no role logins yet), the
	// real demo populate (the Driver persona's login, grant and driver link), second apply.
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
	appwrite.setTeam(driverPersonaUserID, "org:"+demoOrgSlug)
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
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)
	driver := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, driverPersonaUserID), who: "Driver"}

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
		date := day.In(houseday.Location()).Format("2006-01-02")

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
		if len(run.Stops) < 2 {
			t.Fatalf("%s: the dispatched run %s carries %d stops, the walk needs one to deliver and one to hand back",
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

		// The run closes, by the driver who drove it.
		done := driver.call("POST", "/api/v1/dispatch/runs/"+run.ID+"/complete", nil)
		done.ok(t, http.StatusOK)
		var closed driveRun
		done.decode(t, &closed)
		if closed.Status != "COMPLETED" {
			t.Fatalf("%s: Complete run left %s %s, want COMPLETED", label, run.RunNo, closed.Status)
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
