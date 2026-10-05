package main

// load_customer_wiring_test.go proves the three customer doors' composed
// wiring (HH-LOAD-1 LD-6) on the real startup path: with the doors off
// (the default) the customer, contact and job routes do not exist; with
// the doors on, a wrong caller gets the machine gate's one 403 body, and
// a principal holding the doors loads a customer with its home branch by
// key, a contact under it and a job under it, then exit line 6 is shown
// by request: the loaded customer appears in the staff sync pull of its
// org. The address that fails validation is left out and the wire body
// names the field and the reason, never a value. All principals, slugs
// and keys are fixtures; the machine key material is generated inside the
// test and discarded with the process.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	psync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// customerDoorRoutes are the three doors' registration patterns.
var customerDoorRoutes = []string{
	"PUT /api/v1/load/customers",
	"PUT /api/v1/load/contacts",
	"PUT /api/v1/load/jobs",
}

func TestCustomerDoorsFollowTheSwitchAndTheGate(t *testing.T) {
	offEnv := newCompEnv(t)
	offRec, _ := registeredSurface(t, offEnv)
	for _, route := range customerDoorRoutes {
		rec := serveLoad(t, offRec.inner, http.MethodPut, route[len("PUT "):], "", "", `{}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s with the doors off answered %d, want 404: the route does not exist", route, rec.Code)
		}
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate machine key: %v", err)
	}
	srv := machineJWKSServer(t, key)
	onEnv := newCompEnv(t)
	plantSubmitPrincipal(t, onEnv) // holds the branch map door alone
	onEnv.set("CORE_LOAD_DOORS", "on")
	onEnv.set("CORE_MACHINE_AUDIENCE", fixtureMachineAudience)
	onEnv.set("CORE_MACHINE_JWKS_URL", srv.URL)
	onRec, _ := registeredSurface(t, onEnv)
	mux := onRec.inner
	right := machineBearer(t, onEnv, key, fixtureMoverRefID)
	body := `{"source_system":"fixture","batch_id":"b-cw-1","items":[{"legacy_id":"C-1","record":{"name":"Fixture"}}]}`
	staff := onEnv.minter.StaffToken(t, testkit.SubGeneralMgr)
	for _, route := range customerDoorRoutes {
		path := route[len("PUT "):]
		if rec := serveLoad(t, mux, http.MethodPut, path, "", testkit.OrgNorthSlug, body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s no bearer answered %d, want the machine gate's 401", route, rec.Code)
		}
		if rec := serveLoad(t, mux, http.MethodPut, path, staff, testkit.OrgNorthSlug, body); rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
			t.Fatalf("%s staff bearer answered %d %q, want the one 403 body", route, rec.Code, rec.Body.String())
		}
		// The right principal on a door it does not hold: the gate's 403.
		if rec := serveLoad(t, mux, http.MethodPut, path, right, testkit.OrgNorthSlug, body); rec.Code != http.StatusForbidden || rec.Body.String() != string(authz.BodyForbidden) {
			t.Fatalf("%s unheld door answered %d %q, want the one 403 body", route, rec.Code, rec.Body.String())
		}
	}
}

func TestCustomerDoorsServeEndToEndAndTheLoadedCustomerReachesTheStaffPull(t *testing.T) {
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate machine key: %v", err)
	}
	srv := machineJWKSServer(t, key)
	env := newCompEnv(t)
	pool := env.fixturePool()
	if _, err := pool.Exec(ctx, `
		INSERT INTO machine_principal (id, org_id, name, enabled, doors)
		VALUES ($1::uuid, $2::uuid, 'fixture customer mover', TRUE, $3::text[])
		ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, doors = EXCLUDED.doors`,
		fixtureMoverRefID, testkit.OrgNorthID,
		[]string{"loadMapBranches", "loadUpsertCustomers", "loadUpsertContacts", "loadUpsertJobs"}); err != nil {
		t.Fatalf("plant principal: %v", err)
	}
	clean := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE entity_id IN (SELECT id FROM load_batch WHERE org_id = $1::uuid AND source_system = 'custfx')`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid AND source_system = 'custfx'`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid AND source_system = 'custfx'`, testkit.OrgNorthID)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE account_no::text LIKE 'fxcw-%'`)
	}
	clean()
	t.Cleanup(func() {
		clean()
		_, _ = pool.Exec(ctx, `DELETE FROM machine_principal WHERE id = $1::uuid`, fixtureMoverRefID)
	})
	env.set("CORE_LOAD_DOORS", "on")
	env.set("CORE_MACHINE_AUDIENCE", fixtureMachineAudience)
	env.set("CORE_MACHINE_JWKS_URL", srv.URL)
	rec, _ := registeredSurface(t, env)
	mux := rec.inner
	right := machineBearer(t, env, key, fixtureMoverRefID)

	type answer struct {
		Counts map[string]int
		Items  []struct {
			LegacyID string `json:"legacy_id"`
			Status   string `json:"status"`
			HHID     string `json:"hh_id"`
			Reason   string `json:"reason"`
			Field    string `json:"field"`
			Detail   string `json:"detail"`
		}
		raw string
	}
	put := func(route, body string) answer {
		t.Helper()
		resp := serveLoad(t, mux, http.MethodPut, route, right, testkit.OrgNorthSlug, body)
		if resp.Code != http.StatusOK {
			t.Fatalf("PUT %s answered %d %q, want 200", route, resp.Code, resp.Body.String())
		}
		var a answer
		if err := json.Unmarshal(resp.Body.Bytes(), &a); err != nil {
			t.Fatalf("PUT %s body: %v", route, err)
		}
		a.raw = resp.Body.String()
		return a
	}

	a := put("/api/v1/load/branches",
		`{"source_system":"custfx","batch_id":"b-cw-1","items":[{"legacy_id":"WB-1","record":{"code":"TRT"}}]}`)
	if a.Items[0].Status != "attached" {
		t.Fatalf("the branch map answered %+v", a.Items)
	}
	a = put("/api/v1/load/customers",
		`{"source_system":"custfx","batch_id":"b-cw-2","items":[{"legacy_id":"fxcw-1","record":{"name":"Fixture Wired Customer","billing_address":{"line1":"1 Fixture Rd","province":"ZZ","postal":"90210"}},"requires":[{"kind":"branch","legacy_id":"WB-1"}]}]}`)
	cust := a.Items[0]
	if cust.Status != "created" || cust.Reason != "address_left_out" || cust.Field != "billing_address" {
		t.Fatalf("the customer door answered %+v, want created with the address noted left out", cust)
	}
	for _, v := range []string{"90210", "ZZ", "1 Fixture Rd"} {
		if strings.Contains(a.raw, v) {
			t.Fatalf("the wire body carries a record value %q: %s", v, a.raw)
		}
	}
	a = put("/api/v1/load/contacts",
		`{"source_system":"custfx","batch_id":"b-cw-3","items":[{"legacy_id":"CON-1","record":{"name":"Fixture Contact","phone":"555-0100"},"requires":[{"kind":"customer","legacy_id":"fxcw-1"}]}]}`)
	if a.Items[0].Status != "created" {
		t.Fatalf("the contact door answered %+v", a.Items)
	}
	a = put("/api/v1/load/jobs",
		`{"source_system":"custfx","batch_id":"b-cw-4","items":[{"legacy_id":"JOB-1","record":{"name":"Fixture Job","job_address":"3 Fixture Lane"},"requires":[{"kind":"customer","legacy_id":"fxcw-1"}]}]}`)
	if a.Items[0].Status != "created" {
		t.Fatalf("the job door answered %+v", a.Items)
	}

	// Exit line 6: the loaded customer appears in the staff sync pull of
	// its org, and in no other org's.
	store := customer.NewCustomersStore(pool)
	items, _, err := store.Pull(ctx, psync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}, 0, 1000)
	if err != nil {
		t.Fatalf("staff pull: %v", err)
	}
	found := false
	for _, it := range items {
		if it.ID == cust.HHID && it.Seq > 0 && it.Data["name"] == "Fixture Wired Customer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the loaded customer %s is absent from its org's staff sync pull", cust.HHID)
	}
	south, _, err := store.Pull(ctx, psync.ScopeFilter{OrgID: testkit.OrgSouthID, Actor: testkit.SubGeneralMgr}, 0, 1000)
	if err != nil {
		t.Fatalf("south pull: %v", err)
	}
	for _, it := range south {
		if it.ID == cust.HHID {
			t.Fatalf("the loaded customer appears in another org's staff pull")
		}
	}
}
