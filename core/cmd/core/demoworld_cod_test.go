//go:build demo

package main

// demoworld_cod_test.go: proof that the demo world holds one Cash On Demand account, that the
// product's own rules read it as one, and that a tester can walk the whole Cash On Demand path,
// pay before release and never cash on delivery.
//
// WHAT CASH ON DEMAND MEANS HERE, in the product's own words, because the acronym is the trap:
// a COD SALE TYPE is unpaid at ring and locked from fulfilment until the money is in
// (pos/model.go: "cash on DEMAND, a pay-as-you-go posture, NOT cash-on-delivery"). The product
// reads the words COD ACCOUNT in two places, and the account is seeded to the second: the credit
// gates read the CREDIT LIMIT ("account has no credit; cash or card only (COD)",
// identity/staffcontext/rbac.go CheckCredit), and the dispatch gate reads the TERMS
// ("payment_terms_days = 0 with an unpaid balance, the defining rule of a COD account",
// billing/credit_gate.go CanDispatch). The account therefore carries zero-day terms, which the
// dispatch rule reads as Cash On Demand, AND a real credit line, which lets it buy at the ring
// and at conversion: pay before release means the goods wait for the money, not that the account
// cannot order. It sits at the branch the demo logins work, under a role label name a tester can
// find and type.
//
// THE PATH, walked end to end through the real routes, asserting what the product does: the
// picker finds the account by name at Trenton and answers its zero-day terms; the accepted quote
// CONVERTS against the credit line, and the converted order is HELD FROM PICKING (the row 1047
// item 6 work, order/pick_hold.go reading the money wider than the dispatch gate: an uninvoiced
// remainder counts as owed) while the DISPATCH rule, reading invoiced balances only, still lets it
// schedule; the COD ring at the till creates the sale unpaid with its whole balance outstanding,
// and both rules hold THAT order (the dispatch one with PAYMENT DUE naming the COD account, the
// picking one with its own words); once the payment is recorded through the AR route, both rules
// let the goods go. The last half is the definition as a test: the account is never barred from
// buying or from paying, only from having goods leave before the money arrives.
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
	codAccountName = "Cash On Demand customer"
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
	appwrite.setTeam(qwSalesUserID, "org-"+demoOrgSlug)
	appwrite.setTeam(qwManagerUserID, "org-"+demoOrgSlug)
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
		  VALUES ('financial.controller.cod@hh-demo.invalid', 'Financial controller (COD proof)', 'ACTIVE', $1, $2) RETURNING id)
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
	appwrite.setTeam(codFinSubject, "org-"+demoOrgSlug)

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
	// counter renders as Prepaid, never Net 0 (the label is the client's termsLabel; the terms are
	// asserted on the wire here, at their source).
	rsp := sales.call("GET", "/api/v1/customers/search?q="+url.QueryEscape("Cash On Demand")+"&branch_id="+trentonBranchID, nil)
	rsp.ok(t, http.StatusOK)
	var accounts []csSearchAccount
	rsp.decode(t, &accounts)
	var codID string
	var codTerms int
	for _, a := range accounts {
		if a.Accounts == codAccountNo {
			codID, codTerms = a.ID, a.TermsDays
		}
	}
	if codID == "" {
		t.Fatalf("searching %q at Trenton answered %d accounts, none of them the Cash On Demand account", "Cash On Demand", len(accounts))
	}
	if codTerms != 0 {
		t.Errorf("the picker's answer carries %d day terms for the Cash On Demand account, want zero-day", codTerms)
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

	// 4. CONVERT TO ORDER: the account's credit line carries the conversion and the quote becomes an
	// order. THE ORDER IS HELD FROM PICKING, the row 1047 item 6 work made real: the picking hold
	// reads the money wider than the dispatch rule, an uninvoiced remainder counts as owed, so the
	// converted order waits for its balance. The step after asserts both rules' own answers.
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
	// The picking door refuses the converted, uninvoiced, unpaid order (item 9.3): the mandated
	// words, the stable word held_from_picking.
	pick := sales.call("POST", "/api/v1/orders/"+converted.OrderID+"/picking", nil)
	pick.ok(t, http.StatusConflict)
	var pickRefusal struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	pick.decode(t, &pickRefusal)
	if pickRefusal.Error != "held_from_picking" {
		t.Errorf("the picking refusal on the converted order carries %q, want held_from_picking", pickRefusal.Error)
	}

	// 5. THE PRODUCT'S OWN DISPATCH RULE, the defining one (billing/credit_gate.go CanDispatch, "the
	// defining rule of a COD account"), over the real service and the seeded rows. Its unpaid figure
	// is the INVOICED balance (billing/credit_repository.go OrderDispatchFacts), so the freshly
	// converted order, invoiced by nobody yet, is free to SCHEDULE: the dispatch gate keeps its own
	// reading beside the picking hold, and this proof asserts both, each its own rule.
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
	var drawerBody struct {
		ID string `json:"id"`
	}
	drawer.decode(t, &drawerBody)
	drawerSessionID := drawerBody.ID
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
	// And the picking hold holds the till's unpaid order the same way (item 9.3).
	ringPick := sales.call("POST", "/api/v1/orders/"+ring.OrderID+"/picking", nil)
	ringPick.ok(t, http.StatusConflict)
	var ringRefusal struct {
		Error string `json:"error"`
	}
	ringPick.decode(t, &ringRefusal)
	if ringRefusal.Error != "held_from_picking" {
		t.Errorf("the picking refusal on the COD sale carries %q, want held_from_picking", ringRefusal.Error)
	}

	// 7. PAY BEFORE RELEASE, the definition as a test: the payment lands through the AR route the
	// money desk uses, and the same rules that held the goods let them go. Never cash on delivery:
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
	// The picking hold lifted itself with the same dollar (item 9.3): the door that refused now moves.
	pickAgain := sales.call("POST", "/api/v1/orders/"+ring.OrderID+"/picking", nil)
	pickAgain.ok(t, http.StatusOK)
	// 8. THE DEPOSIT WORLD (item 9.9): the two orders the seed plants for a tester to walk, found
	// through the real Pay balance route at Trenton, with what each reads: the credit customer's
	// deposit order owes on its terms and is NOT held; the Cash On Demand account's is held from
	// picking, in process because its deposit landed. Then the held one is paid in full at the Pay
	// balance door and the hold lifts with the last dollar.
	find := sales.call("GET", "/api/v1/pos/pay-balance?q="+url.QueryEscape("DEMO-PLM-D0"), nil)
	find.ok(t, http.StatusOK)
	var found struct {
		Items []struct {
			OrderID         string `json:"order_id"`
			OrderNo         string `json:"order_no"`
			CustomerName    string `json:"customer_name"`
			Job             string `json:"job"`
			OrderTotalCents int64  `json:"order_total_cents"`
			PaidCents       int64  `json:"paid_cents"`
			StillOwedCents  int64  `json:"still_owed_cents"`
			Held            bool   `json:"held_from_picking"`
			HoldWords       string `json:"hold_words"`
			Payments        []struct {
				Method      string `json:"method"`
				AmountCents int64  `json:"amount_cents"`
			} `json:"payments"`
		} `json:"items"`
	}
	find.decode(t, &found)
	if len(found.Items) != 2 {
		t.Fatalf("Pay balance found %d deposit orders for DEMO-PLM-D0, want the two the seed plants", len(found.Items))
	}
	var heldOrderID string
	var heldOwed int64
	for _, it := range found.Items {
		if it.StillOwedCents <= 0 || it.PaidCents <= 0 || it.OrderTotalCents != it.PaidCents+it.StillOwedCents || len(it.Payments) != 1 {
			t.Errorf("%s reads total %d paid %d owed %d over %d payments, want a deposit taken and the rest owing",
				it.OrderNo, it.OrderTotalCents, it.PaidCents, it.StillOwedCents, len(it.Payments))
		}
		switch it.OrderNo {
		case "DEMO-PLM-D01":
			if it.Held || it.Payments[0].Method != "CARD" || it.Job == "" {
				t.Errorf("DEMO-PLM-D01 (credit) reads held %v by %s job %q, want not held, a card deposit, a job", it.Held, it.Payments[0].Method, it.Job)
			}
		case "DEMO-PLM-D02":
			if !it.Held || it.HoldWords != "This order cannot reach picking until the balance is paid." || it.Payments[0].Method != "CASH" {
				t.Errorf("DEMO-PLM-D02 (Cash On Demand) reads held %v %q by %s, want held with the hold words, a cash deposit", it.Held, it.HoldWords, it.Payments[0].Method)
			}
			heldOrderID, heldOwed = it.OrderID, it.StillOwedCents
		default:
			t.Errorf("Pay balance answered %s, want only the two deposit orders", it.OrderNo)
		}
	}
	read := sales.call("GET", "/api/v1/orders/"+heldOrderID, nil)
	read.ok(t, http.StatusOK)
	var readBody struct {
		PayHold struct {
			Held           bool   `json:"held_from_picking"`
			InProcessWords string `json:"in_process_words"`
			OwedCents      int64  `json:"owed_cents"`
		} `json:"pay_hold"`
	}
	read.decode(t, &readBody)
	if !readBody.PayHold.Held || readBody.PayHold.InProcessWords != "In process: deposit taken" || readBody.PayHold.OwedCents != heldOwed {
		t.Errorf("the held deposit order's read carries %+v, want held, in process, owing %d", readBody.PayHold, heldOwed)
	}
	take := sales.call("POST", "/api/v1/pos/pay-balance", map[string]any{
		"order_id": heldOrderID, "method": "CASH", "amount_cents": heldOwed,
		"till_session_id": drawerSessionID, "idempotency_key": uuid.NewString()})
	take.ok(t, http.StatusCreated)
	lifted := sales.call("POST", "/api/v1/orders/"+heldOrderID+"/picking", nil)
	lifted.ok(t, http.StatusOK)

	t.Logf("the Cash On Demand path as the product ships it: picker found %s, quote %s converted to order %s (held from picking, uninvoiced), the COD sale %s unpaid at %d cents and held at both gates, released when the payment landed",
		codAccountNo, q.QuoteNo, converted.OrderNo, ring.OrderNo, ring.TotalCents)
}
