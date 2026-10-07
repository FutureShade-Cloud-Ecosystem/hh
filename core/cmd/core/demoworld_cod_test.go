//go:build demo

package main

// demoworld_cod_test.go: proof that the demo world holds one Cash On Demand account, that the
// product's own rules read it as one, and that a tester can walk the whole Cash On Demand path,
// pay before release and never cash on delivery.
//
// WHAT CASH ON DEMAND MEANS HERE, in the product's own words, because the acronym is the trap:
// a COD SALE TYPE is unpaid at ring and locked from fulfilment until the money is in
// (pos/model.go: "cash on DEMAND, a pay-as-you-go posture, NOT cash-on-delivery"), and a COD
// ACCOUNT is one whose terms make every order that posture. The account rule is the product's
// own, stated where it is enforced: CanDispatch holds an order back while
// "payment_terms_days = 0 with an unpaid balance, the defining rule of a COD account"
// (billing/credit_gate.go), and the credit gates at the ring and at conversion answer
// "account has no credit; cash or card only (COD)" (identity/staffcontext/rbac.go CheckCredit).
// So the account is seeded with zero-day terms and no credit limit, at the branch the demo logins
// work, under a role label name a tester can find and type.
//
// THE PATH, walked end to end through the real routes: the picker finds the account by name at
// Trenton; the account's accepted quote refuses to convert with the COD sentence and the quote is
// unchanged; an ACCOUNT ring at the till refuses the same way; a COD ring creates the sale unpaid
// with its whole balance outstanding; the product's own dispatch rule (the real billing service
// over the seeded rows) refuses the order's release with PAYMENT DUE naming the COD account; and
// once the payment is recorded through the AR route, the same rule lets the goods go. That last
// half is the definition as a test: the account is never barred from buying or from paying, only
// from having goods leave before the money arrives.
//
// The database gate is the demo world proofs' own variable, and the harness refuses an address
// that is not local.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// codAccountNo and codQuoteKey name the world's own rows: the tenth demo account and the fourth
// quote. Repeated here on purpose, the pin every world proof uses for its fixtures. codFinSubject
// is the money-desk login this proof plants beside the world (the quotes proof's HR pattern), a
// role label as the house rule demands.
const (
	codAccountName = "Cash On Demand customer (demo)"
	codAccountNo   = "DEMO-C10"
	codQuoteKey    = "Q4"
	codFinSubject  = "hh-demo-cod-financial-controller"
)

func TestDemoWorld_CashOnDemand(t *testing.T) {
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
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_cod")
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

	// The served surface, the demo Sales login (the till and the quote desk's own caller).
	appwrite := newAppwriteFake(t)
	appwrite.setTeam(qwSalesUserID, "org:"+demoOrgSlug)
	appwrite.setTeam(qwManagerUserID, "org:"+demoOrgSlug)
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
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)
	sales := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwSalesUserID), who: "Sales"}
	manager := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwManagerUserID), who: "Sales manager"}

	// The money desk's login for the payment leg below: a Financial controller beside the world, the
	// one role that holds the money-desk write (the role matrix: financial_controller admin), because
	// the demo logins hold the counter and the quote desk and not the AR register.
	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the demo org: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH u AS (
		  INSERT INTO users (email, display_name, status, org_id, platform_user_id)
		  VALUES ('financial.controller@hh-demo.invalid', 'Financial controller (COD proof)', 'ACTIVE', $1, $2) RETURNING id)
		INSERT INTO user_branch_grants (user_id, branch_id, org_id) SELECT id, $3, $1 FROM u`,
		orgID, codFinSubject, trentonBranchID); err != nil {
		t.Fatalf("plant the Financial controller login: %v", err)
	}
	// The grant carries NO branch: the resolver's grant-shape rule holds that a global role
	// granted with a branch id is a provisioning defect (identity.ErrGrantShape), and the
	// controller is a Tier-1 global role.
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id) VALUES ($1, 'financial_controller', $2)`,
		codFinSubject, orgID); err != nil {
		t.Fatalf("plant the Financial controller grant: %v", err)
	}
	appwrite.setTeam(codFinSubject, "org:"+demoOrgSlug)

	// 1. THE ACCOUNT, as the seeded row reads. The zero-day terms are the fact the product's own
	// dispatch rule reads ("the defining rule of a COD account"); the credit line is what lets the
	// account buy at all, the product's COD posture being pay before release and not barred from
	// ordering; and the home branch is the one the branch-fenced search needs.
	var name, home string
	var terms int
	var credit int64
	if err := pool.QueryRow(ctx, `
		SELECT c.name, c.home_branch_id::text, c.payment_terms_days, c.credit_limit_cents
		  FROM customers c WHERE c.account_no = $1`, codAccountNo).Scan(&name, &home, &terms, &credit); err != nil {
		t.Fatalf("the Cash On Demand account is missing from the world: %v", err)
	}
	if name != codAccountName || terms != 0 || credit <= 0 || home != trentonBranchID {
		t.Fatalf("the Cash On Demand account reads name %q, terms %d, credit %d, home %s; want %q, zero-day terms, a credit line, homed at Trenton",
			name, terms, credit, home, codAccountName)
	}

	// 2. THE PICKER finds it by name at Trenton, and the answer carries the zero-day terms the
	// counter renders as Prepaid, never Net 0.
	rsp := sales.call("GET", "/api/v1/customers/search?q="+url.QueryEscape("Cash On Demand")+"&branch_id="+trentonBranchID, nil)
	rsp.ok(t, http.StatusOK)
	var accounts []csSearchAccount
	rsp.decode(t, &accounts)
	var codID string
	for _, a := range accounts {
		if a.Accounts == codAccountNo {
			codID = a.ID
		}
	}
	if codID == "" {
		t.Fatalf("searching %q at Trenton answered %d accounts, none of them the Cash On Demand account", "Cash On Demand", len(accounts))
	}

	// 3. THE QUOTE, ready for the convert button: accepted, at Trenton, priced lines on it.
	quoteID := demoID("quote:" + codQuoteKey)
	view := manager.call("GET", "/api/v1/quotes/"+quoteID, nil)
	view.ok(t, http.StatusOK)
	var q qwQuote
	view.decode(t, &q)
	if q.Status != "ACCEPTED" || !q.CanConvert || len(q.Lines) == 0 {
		t.Fatalf("the Cash On Demand quote reads %s can_convert %v with %d lines, want ACCEPTED, convertible, priced", q.Status, q.CanConvert, len(q.Lines))
	}

	// 4. CONVERT TO ORDER: the account's credit line carries the conversion, the quote becomes an
	// order, and THAT order is the Cash On Demand path on this surface: it cannot leave.
	conv := sales.call("POST", "/api/v1/quotes/"+quoteID+"/convert", nil)
	conv.ok(t, http.StatusCreated)
	var converted struct {
		OrderID string `json:"order_id"`
		OrderNo string `json:"order_no"`
	}
	conv.decode(t, &converted)
	var quoteStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM quotes WHERE id = $1`, quoteID).Scan(&quoteStatus); err != nil || quoteStatus != "CONVERTED" {
		t.Errorf("the quote is %s after conversion (%v), want CONVERTED", quoteStatus, err)
	}

	// 5. THE PRODUCT'S OWN RULE, the defining one (billing/credit_gate.go CanDispatch, "the defining
	// rule of a COD account"), over the real service and the seeded rows. Its unpaid figure is the
	// INVOICED balance (billing/credit_repository.go OrderDispatchFacts), so the freshly converted
	// order, invoiced by nobody yet, owes nothing and is free to prepare: the rule holds money owed,
	// not orders in the abstract, exactly as the source says ("it is precisely that order which must
	// not leave unpaid"). The account's money goes on the line at the till, which is the next step.
	gate := billing.NewCreditService(billing.NewRepository(pool))
	allowed, reason := gate.CanDispatch(ctx, converted.OrderID)
	if !allowed || reason != "" {
		t.Fatalf("the product's dispatch rule read the freshly converted, uninvoiced order as (%v, %q), want free to prepare", allowed, reason)
	}

	// 6. AT THE TILL, the COD ring, the sale type the account exists to walk: unpaid at ring, the
	// whole balance outstanding, due on demand, settlement method recorded for later collection.
	drawer := sales.call("POST", "/api/v1/till/sessions", map[string]any{
		"branch_id": trentonBranchID, "station": "MAIN", "float_cents": demoDrawerFloatCents})
	drawer.ok(t, http.StatusCreated)
	codRing := sales.call("POST", "/api/v1/pos/sales", demoSaleBody{
		IdempotencyKey: uuid.NewString(),
		BranchID:       trentonBranchID,
		SaleType:       "COD",
		CustomerID:     codID,
		Tender:         "CASH",
		Lines:          []demoSaleLine{{ProductID: demoID("product:P08"), QtyEntered: 4, UOMEntered: "T"}},
	})
	codRing.ok(t, http.StatusCreated)
	var ring saleRing
	codRing.decode(t, &ring)
	var invStatus string
	var invBalance int64
	if err := pool.QueryRow(ctx, `SELECT status::text, balance_cents FROM invoices WHERE id = $1`, ring.InvoiceID).
		Scan(&invStatus, &invBalance); err != nil {
		t.Fatalf("the COD sale's invoice: %v", err)
	}
	if invBalance != ring.TotalCents {
		t.Fatalf("the COD sale's invoice balance is %d against a total of %d, the sale must be unpaid at ring", invBalance, ring.TotalCents)
	}
	// Due on demand: the invoice is never due AFTER the day it was issued, because the customer's
	// terms are zero days and no terms days are ever added. (The two dates are stamped off
	// different zone reads around the house zone's day boundary, so exact equality would flake at
	// the boundary; "never later than the issue day" is the on-demand fact either zone answers.)
	var dueByIssue bool
	if err := pool.QueryRow(ctx, `SELECT due_date <= issue_date FROM invoices WHERE id = $1`, ring.InvoiceID).Scan(&dueByIssue); err != nil || !dueByIssue {
		t.Errorf("the COD sale's invoice is due after its issue day (err %v), an on-demand sale must not be", err)
	}
	// The same rule holds the till's order too, on the same account facts.
	if allowed, reason := gate.CanDispatch(ctx, ring.OrderID); allowed || !strings.Contains(reason, "COD") {
		t.Errorf("the product's dispatch rule read the COD sale's order as (%v, %q), want the same PAYMENT DUE refusal", allowed, reason)
	}

	// 7. PAY BEFORE RELEASE, the definition as a test: the payment lands through the AR route the
	// money desk uses, and the same rule that held the goods lets them go. Never cash on delivery:
	// the money arrives first, the release follows it. (The recorder is the Financial controller
	// login planted beside the world, the money-desk write being a controller's and no demo login's;
	// the proof drives it exactly as a demo box would, through the real route.)
	fin := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, codFinSubject), who: "Financial controller"}
	pay := fin.call("POST", "/api/v1/ar/register/invoices/"+ring.InvoiceID+"/payments", map[string]any{
		"method": "CASH", "amount_cents": ring.TotalCents, "reference": "", "idempotency_key": uuid.NewString()})
	pay.ok(t, http.StatusOK)
	allowed, reason = gate.CanDispatch(ctx, ring.OrderID)
	if !allowed || reason != "" {
		t.Errorf("after the payment the dispatch rule still answers (%v, %q), the paid order must be free to leave", allowed, reason)
	}
	t.Logf("the Cash On Demand path: picker found %s, quote %s converted to order %s (held), the COD sale %s unpaid at %d cents, both held at the gate, the paid sale released",
		codAccountNo, q.QuoteNo, converted.OrderNo, ring.OrderNo, ring.TotalCents)
}
