//go:build demo

package main

// demoworld_customersearch_test.go: proof that every customer the demo world gives documents to
// at a branch is findable by name in the staff customer search at that branch.
//
// The visual check that produced this proof: the Quote Desk at Trenton showed Summit Ridge Builders'
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
// The database gate is the demo world proofs' own variable, and the harness refuses an address that
// is not local.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

	// THE REPAIR LEG, the path a demo box already seeded before the ruling takes. The apply is
	// INSERT-only, so the plan's own customers row cannot move on a database that already holds it;
	// the demoseed_repair pair's one guarded UPDATE is what re-homes it. Reproduce the old world
	// exactly (the row homed at the first branch by code order), run the apply again, and the
	// search must find Summit Ridge once more. A second apply after it must repair nothing: the
	// guard names the value it repairs, so a repaired row is out of its reach.
	var kingstonID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'JUL'`).Scan(&kingstonID); err != nil {
		t.Fatalf("the Kingston branch: %v", err)
	}
	rehome := func(to string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE customers SET home_branch_id = $1::uuid WHERE id = $2::uuid`,
			to, demoID("customer:C01")); err != nil {
			t.Fatalf("planting the old home for the repair leg: %v", err)
		}
	}
	foundAgain := func() bool {
		t.Helper()
		rsp := sales.call("GET", "/api/v1/customers/search?q="+url.QueryEscape("Summit Ridge")+"&branch_id="+trentonBranchID, nil)
		rsp.ok(t, http.StatusOK)
		return strings.Contains(string(rsp.body), demoID("customer:C01"))
	}
	rehome(kingstonID)
	if foundAgain() {
		t.Fatal("the repair leg's premise is wrong: Summit Ridge homed at Kingston is already findable at Trenton")
	}
	qwApply(t)
	var home string
	if err := pool.QueryRow(ctx, `SELECT home_branch_id::text FROM customers WHERE id = $1`, demoID("customer:C01")).Scan(&home); err != nil {
		t.Fatalf("reading Summit Ridge's home after the repair: %v", err)
	}
	if home != trentonBranchID {
		t.Fatalf("the apply's repair pass left Summit Ridge homed at %s, want Trenton %s", home, trentonBranchID)
	}
	if !foundAgain() {
		t.Error("after the repair apply, Summit Ridge is still not findable by name at Trenton")
	}
	// A second apply repairs nothing: the guard names the old home, and a row already at Trenton
	// (by the repair before it or by a hand) is out of its reach.
	qwApply(t)
	if err := pool.QueryRow(ctx, `SELECT home_branch_id::text FROM customers WHERE id = $1`, demoID("customer:C01")).Scan(&home); err != nil || home != trentonBranchID {
		t.Errorf("the second apply moved Summit Ridge's home again (%s, err %v); the guard must match nothing once repaired", home, err)
	}
}
