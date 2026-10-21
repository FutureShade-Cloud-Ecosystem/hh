//go:build demo

package main

// demoworld_customersearch_test.go: proof that every customer the demo world gives documents to
// at a branch is findable by name in the staff customer search at that branch.
//
// The visual check that produced this proof: the Quote Desk at Trenton showed Bluffside Builders'
// quote, and the staff customer search at the same branch answered nothing for the name. The reason
// was the branch, not the name: the search is branch-fenced (customer/search_scope.go, U-park E4,
// a deliberate fence and not a bug to work around), it admits accounts homed to the searched branch
// and accounts with no home, and the account was homed to the other branch while its quote sat at
// Trenton. Every demo login carries exactly one branch grant, at Trenton, so the branch a tester
// can search IS the branch the demo works, and a customer with documents there must be homed there
// or it is invisible to every search a tester can run.
//
// So the proof walks the whole world, not the one account: every customer holding a document
// (quote, order, invoice, parked ticket) at Trenton is searched for by name, through the real
// route, as the demo Sales login, and must come back. The fence's own negative space is asserted
// beside it: the house WALK-IN account is excluded by the search on purpose and stays excluded.
//
// UX-94b adds Stonebrook, the held account, to the shared set, and proves it on the Trenton till.
//
// UX-94: Northline, Harbourview and the County are SHARED accounts (NULL home), because they are
// ordered from at both yards. The proof asserts the NULL after a fresh apply and after the repair
// on a box seeded with the old homes, and that each is findable by name at Trenton and by a
// caller whose one grant is the other yard (Julian).
//
// The database gate is the demo world proofs' own variable, and the harness refuses an address that
// is not local.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// csSearchAccount mirrors the CustomerSearchResult fields this proof reads.
type csSearchAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Accounts  string `json:"account_no"`
	TermsDays int    `json:"payment_terms_days"`
}

func TestDemoWorld_CustomerSearch(t *testing.T) {
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

	// The world, in the order a demo box builds it.
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_custsearch")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", vaultKeyHex)
	t.Setenv("APP_ENV", "dev")
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the second apply created nothing, the quote world never landed")
	}

	// The served surface and the demo Sales login, the one branch-scoped caller a tester actually
	// holds (every demo login's single grant is at Trenton).
	appwrite := newAppwriteFake(t)
	appwrite.setTeam(qwSalesUserID, "org-"+demoOrgSlug)
	keys := testkit.NewKeys(t)
	acceptKey := make([]byte, 32)
	if _, err := rand.Read(acceptKey); err != nil {
		t.Fatalf("generate acceptance key: %v", err)
	}
	env := &compEnv{
		keys: keys, minter: &testkit.Minter{Keys: keys, Clock: testkit.NewClock()},
		keySrv: testkit.NewKeyServer(t, keys), appwrite: appwrite, dsn: testDSN,
	}
	values := map[string]string{
		"CORE_TOKEN_ISSUER": testkit.KitIssuer, "CORE_STAFF_AUDIENCE": testkit.KitStaffAud,
		"CORE_TENANT_AUDIENCE": testkit.KitTenantAud, "CORE_JWKS_URL": env.keySrv.URL(),
		"CORE_DATABASE_URL": testDSN, "CORE_APPWRITE_ENDPOINT": appwrite.server.URL,
		"CORE_APPWRITE_PROJECT": "fx-appwrite-project", "CORE_APPWRITE_API_KEY": "fx-appwrite-key-placeholder",
		customerquote.EnvKeyID: "fx-accept-sign-key", customerquote.EnvKey: hex.EncodeToString(acceptKey),
	}
	consoleSessionValues(t, env.minter, values)
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)
	sales := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwSalesUserID), who: "Sales"}

	// Every customer with a document at Trenton, and its name. Documents are the four tables that
	// hang a customer off a branch; pos_sale is covered by its order.
	rows, err := pool.Query(ctx, `
		WITH docs AS (
		  SELECT customer_id, branch_id FROM quotes
		  UNION ALL SELECT customer_id, branch_id FROM orders
		  UNION ALL SELECT customer_id, branch_id FROM invoices
		  UNION ALL SELECT customer_id, branch_id FROM pos_parked_tickets WHERE customer_id IS NOT NULL
		)
		SELECT DISTINCT c.id::text, c.name, c.account_no::text
		  FROM docs d JOIN customers c ON c.id = d.customer_id
		 WHERE d.branch_id = $1::uuid AND c.account_no <> 'WALK-IN'
		 ORDER BY c.name`, trentonBranchID)
	if err != nil {
		t.Fatalf("collecting the customers with documents at Trenton: %v", err)
	}
	defer rows.Close()
	type want struct{ id, name, account string }
	var wants []want
	for rows.Next() {
		var w want
		if err := rows.Scan(&w.id, &w.name, &w.account); err != nil {
			t.Fatalf("scanning a customer: %v", err)
		}
		wants = append(wants, w)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walking the customer list: %v", err)
	}
	if len(wants) < 2 {
		t.Fatalf("only %d customers hold documents at Trenton, the demo world's Trenton book is missing", len(wants))
	}

	// The three shared accounts hold no home after a fresh apply.
	assertShared := func(when string) {
		t.Helper()
		for _, key := range sharedKeys {
			var home *string
			if err := pool.QueryRow(ctx, `SELECT home_branch_id::text FROM customers WHERE id = $1::uuid`,
				demoID("customer:"+key)).Scan(&home); err != nil {
				t.Fatalf("%s: reading %s's home: %v", when, key, err)
			}
			if home != nil {
				t.Errorf("%s: %s is homed at %s, want no home (a shared account)", when, key, *home)
			}
		}
	}
	assertShared("after a fresh apply")

	// UX-94b, THE HELD ACCOUNT AT TRENTON. A Trenton walker finds Stonebrook through the till's
	// customer read, and the account-standing read the till makes on picking it says on_hold, so the
	// On hold card has real data behind it. Red on main, where C09 is homed at Kingston.
	nameOf := func(key string) string {
		t.Helper()
		var name string
		if err := pool.QueryRow(ctx, `SELECT name FROM customers WHERE id = $1::uuid`, demoID("customer:"+key)).Scan(&name); err != nil {
			t.Fatalf("reading %s's name: %v", key, err)
		}
		return strings.TrimSuffix(strings.TrimSuffix(name, " (demo)"), " (demo retail)")
	}
	c09 := demoID("customer:C09")
	held := sales.call("GET", "/api/v1/customers/search?q="+url.QueryEscape(nameOf("C09"))+"&branch_id="+trentonBranchID, nil)
	held.ok(t, http.StatusOK)
	if !strings.Contains(string(held.body), c09) {
		t.Errorf("RED the held account is not findable at Trenton by a demo login: %s", held.body)
	}
	standing := sales.call("GET", "/api/v1/pos/account-standing?branch_id="+trentonBranchID+"&customer_id="+c09, nil)
	standing.ok(t, http.StatusOK)
	var heldStanding struct {
		Standing string `json:"standing"`
		OnHold   bool   `json:"on_hold"`
	}
	if err := json.Unmarshal(standing.body, &heldStanding); err != nil {
		t.Fatalf("decode the held account's standing: %v: %s", err, standing.body)
	}
	if heldStanding.Standing != "on_hold" || !heldStanding.OnHold {
		t.Errorf("RED the Trenton till read %+v for the held account, want on_hold", heldStanding)
	}

	// THE FENCE STILL REFUSES. C06, the retail walk-up homed at Kingston, is the account no demo
	// login reaches: the search at Trenton must not return it.
	c06 := demoID("customer:C06")
	var c06Home *string
	if err := pool.QueryRow(ctx, `SELECT home_branch_id::text FROM customers WHERE id = $1::uuid`, c06).Scan(&c06Home); err != nil {
		t.Fatalf("reading C06's home: %v", err)
	}
	if c06Home == nil || *c06Home == trentonBranchID {
		t.Fatalf("the fence proof needs C06 homed at the other yard, got %v", c06Home)
	}
	fenced := sales.call("GET", "/api/v1/customers/search?q="+url.QueryEscape(nameOf("C06"))+"&branch_id="+trentonBranchID, nil)
	fenced.ok(t, http.StatusOK)
	if strings.Contains(string(fenced.body), c06) {
		t.Errorf("the branch fence leaked C06, homed at the other yard, to a Trenton search: %s", fenced.body)
	}

	// Search each one by the distinctive half of its name, the way a tester types it.
	for _, w := range wants {
		term := w.name
		if cut, ok := strings.CutSuffix(term, " (demo)"); ok {
			term = cut
		} else if cut, ok = strings.CutSuffix(term, " (demo retail)"); ok {
			term = cut
		}
		rsp := sales.call("GET", "/api/v1/customers/search?q="+url.QueryEscape(term)+"&branch_id="+trentonBranchID, nil)
		if rsp.status != http.StatusOK {
			t.Errorf("%s: the search answered %d, want 200 (body: %s)", w.name, rsp.status, string(rsp.body))
			continue
		}
		var accounts []csSearchAccount
		rsp.decode(t, &accounts)
		found := ""
		for _, a := range accounts {
			if a.ID == w.id {
				found = a.Name
			}
		}
		if found == "" {
			t.Errorf("%s (%s) holds documents at Trenton and is NOT findable by name in the staff search there: %q answered %d accounts, none of them this one",
				w.name, w.account, term, len(accounts))
		}
	}

	// The house WALK-IN stays excluded everywhere (the picker's own rule), and the search as this
	// caller refuses a branch it holds no grant for, the fence working as designed.
	rsp := sales.call("GET", "/api/v1/customers/search?q="+url.QueryEscape("Walk")+"&branch_id="+trentonBranchID, nil)
	rsp.ok(t, http.StatusOK)
	if strings.Contains(string(rsp.body), "WALK-IN") {
		t.Error("the house WALK-IN account leaked into the picker's results, the search excludes it on purpose")
	}

	// THE JULIAN LEG. A caller whose one grant is the other yard finds each shared account by name
	// there too: no home means no yard to be invisible from.
	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the demo org: %v", err)
	}
	var kingstonID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'JUL'`).Scan(&kingstonID); err != nil {
		t.Fatalf("the Kingston branch: %v", err)
	}
	const julianSubject = "de110000-0000-4000-9000-0000000000a4"
	if _, err := pool.Exec(ctx, `
		WITH u AS (
		  INSERT INTO users (email, display_name, status, org_id, platform_user_id)
		  VALUES ('sales.julian.ux94@hh-demo.invalid', 'Sales at Julian (UX-94 proof)', 'ACTIVE', $1, $2) RETURNING id)
		INSERT INTO user_branch_grants (user_id, branch_id, org_id) SELECT id, $3, $1 FROM u`,
		orgID, julianSubject, kingstonID); err != nil {
		t.Fatalf("plant the Julian login: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id) VALUES ($1, 'inside_sales', $2, $3)`,
		julianSubject, orgID, kingstonID); err != nil {
		t.Fatalf("plant the Julian grant: %v", err)
	}
	appwrite.setTeam(julianSubject, "org-"+demoOrgSlug)
	julian := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, julianSubject), who: "Sales at Julian"}
	// The names are read from the database: the populate's scrub pass renames the demo accounts, so
	// the plan's own name is not the name a tester types.
	sharedTerms := map[string]string{}
	for _, key := range sharedKeys {
		var name string
		if err := pool.QueryRow(ctx, `SELECT name FROM customers WHERE id = $1::uuid`, demoID("customer:"+key)).Scan(&name); err != nil {
			t.Fatalf("reading %s's name: %v", key, err)
		}
		sharedTerms[key] = strings.TrimSuffix(name, " (demo)")
	}
	findShared := func(c *tillClient, branchID, key string) bool {
		t.Helper()
		rsp := c.call("GET", "/api/v1/customers/search?q="+url.QueryEscape(sharedTerms[key])+"&branch_id="+branchID, nil)
		rsp.ok(t, http.StatusOK)
		return strings.Contains(string(rsp.body), demoID("customer:"+key))
	}
	for _, key := range sharedKeys {
		if !findShared(julian, kingstonID, key) {
			t.Errorf("%s is not findable by name for a Julian-scoped caller, a shared account must answer at both yards", key)
		}
	}

	// THE REPAIR LEG, the path a demo box already seeded with the old homes takes. The apply is
	// INSERT-only, so the plan's own customers rows cannot move on a database that already holds
	// them; the demoseed_repair pair's one guarded UPDATE is what un-homes them. Reproduce the old
	// world (C01 and C08 homed at the first branch by code order, C04 at Trenton), run the apply
	// again, and all three must read NULL and stay findable at Trenton. A second apply must repair
	// nothing: the guard names the old homes, so a repaired row is out of its reach.
	oldHome := map[string]string{"C01": kingstonID, "C04": trentonBranchID, "C08": kingstonID, "C09": kingstonID}
	for key, home := range oldHome {
		if _, err := pool.Exec(ctx, `UPDATE customers SET home_branch_id = $1::uuid WHERE id = $2::uuid`,
			home, demoID("customer:"+key)); err != nil {
			t.Fatalf("planting %s's old home for the repair leg: %v", key, err)
		}
	}
	if findShared(sales, trentonBranchID, "C01") {
		t.Fatal("the repair leg's premise is wrong: Northline homed at Kingston is already findable at Trenton")
	}
	qwApply(t)
	assertShared("after the repair apply")
	for _, key := range sharedKeys {
		if !findShared(sales, trentonBranchID, key) {
			t.Errorf("after the repair apply, %s is not findable by name at Trenton", key)
		}
	}
	// A second apply writes nothing to those rows: a repaired row is never touched twice.
	var stamp1, stamp2 string
	read := func(dst *string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT string_agg(updated_at::text, ',' ORDER BY id) FROM customers WHERE id = ANY($1::uuid[])`,
			[]string{demoID("customer:C01"), demoID("customer:C04"), demoID("customer:C08"), demoID("customer:C09")}).Scan(dst); err != nil {
			t.Fatalf("reading the shared accounts' stamps: %v", err)
		}
	}
	read(&stamp1)
	qwApply(t)
	read(&stamp2)
	if stamp1 != stamp2 {
		t.Errorf("the second apply touched the shared accounts again (%s then %s); the guard must match nothing once repaired", stamp1, stamp2)
	}
	assertShared("after the second apply")
}

// sharedKeys are the demo accounts the plan seeds with no home branch.
var sharedKeys = []string{"C01", "C04", "C08", "C09"}
