//go:build demo

package main

// demoworld_ar_work_level_test.go: rulings 77 (a) and 87, proved through the real routes and the real
// role gate.
//
// Payables and Receivables holds the money desk at WORK. Roadmap 2.8 has it mark a payment bounced and
// work the queue, run statements and send reminders, and read a payment's history, so those routes are
// served at work; a payment reverse stays at manage. Ruling 87 then holds the payment trail (a
// payment's history and an invoice's payments) to money desk WORK: the persona and the managers above
// it keep them, each fenced to their branches, while the money desk readers with no counter till
// (outside sales, the yard manager, the procurement manager) lose them.
//
// Each route is walked four ways: the persona is let through; a selling role (the demo Sales login,
// money desk read) and the yard manager (money desk read) are refused the work routes in the gate's
// shared body; the history reads are refused a role with neither the counter nor the money desk (HR);
// the reverse is refused the persona. Nothing here asserts a sum, only the gate's answer.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	wlYardManagerSubject = "hh-demo-worklevel-proof-yard-manager"
	wlHRSubject          = "hh-demo-worklevel-proof-hr"
	// Ruling 87's proof logins: the three money desk readers it excludes, and the managers above
	// the persona it names as still passing (the branch manager planted twice, one at each branch,
	// so the fence half of "each fenced to its branches" is proved with a 200 and a not found).
	wlOutsideSalesSubject       = "hh-demo-worklevel-proof-outside-sales"
	wlProcurementSubject        = "hh-demo-worklevel-proof-procurement-manager"
	wlSalesManagerSubject       = "hh-demo-worklevel-proof-sales-manager"
	wlBranchManagerSubject      = "hh-demo-worklevel-proof-branch-manager"
	wlBranchManagerKgnSubject   = "hh-demo-worklevel-proof-branch-manager-kgn"
	wlGeneralManagerSubject     = "hh-demo-worklevel-proof-general-manager"
	wlKingstonBranchID          = "5d1b3f6e-0000-4000-9000-000000000002"
)

func TestDemoWorld_PayablesReceivablesWorkLevel(t *testing.T) {
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
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_worklevel")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	t.Setenv("APP_ENV", "dev")
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the second apply created nothing, the rest of the world never landed")
	}

	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the demo org: %v", err)
	}
	for _, p := range []struct{ subject, role, email, branch string }{
		{wlYardManagerSubject, "yard_manager", "worklevel.proof.yard.manager@hh-demo.invalid", trentonBranchID},
		{wlHRSubject, "hr", "worklevel.proof.hr@hh-demo.invalid", ""},
		{wlOutsideSalesSubject, "outside_sales", "worklevel.proof.outside.sales@hh-demo.invalid", trentonBranchID},
		{wlProcurementSubject, "procurement_manager", "worklevel.proof.procurement@hh-demo.invalid", trentonBranchID},
		{wlSalesManagerSubject, "sales_manager", "worklevel.proof.sales.manager@hh-demo.invalid", trentonBranchID},
		{wlBranchManagerSubject, "branch_manager", "worklevel.proof.branch.manager@hh-demo.invalid", trentonBranchID},
		{wlBranchManagerKgnSubject, "branch_manager", "worklevel.proof.branch.manager.kgn@hh-demo.invalid", wlKingstonBranchID},
		{wlGeneralManagerSubject, "general_manager", "worklevel.proof.general.manager@hh-demo.invalid", ""},
	} {
		if _, err := pool.Exec(ctx, `
			WITH u AS (
			  INSERT INTO users (email, display_name, status, org_id, platform_user_id)
			  VALUES ($4, $5, 'ACTIVE', $1, $2) RETURNING id)
			INSERT INTO user_branch_grants (user_id, branch_id, org_id)
			SELECT id, COALESCE(NULLIF($6, '')::uuid, $3::uuid), $1 FROM u`,
			orgID, p.subject, trentonBranchID, p.email, p.role+" (work level proof)", p.branch); err != nil {
			t.Fatalf("plant the %s login: %v", p.role, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id) VALUES ($1, $2, $3, NULLIF($4, '')::uuid)`,
			p.subject, p.role, orgID, p.branch); err != nil {
			t.Fatalf("plant the %s grant: %v", p.role, err)
		}
	}

	appwrite := newAppwriteFake(t)
	for _, s := range []string{prPayRecSubject, qwSalesUserID, wlYardManagerSubject, wlHRSubject,
		wlOutsideSalesSubject, wlProcurementSubject, wlSalesManagerSubject, wlBranchManagerSubject,
		wlBranchManagerKgnSubject, wlGeneralManagerSubject} {
		appwrite.setTeam(s, "org:"+demoOrgSlug)
	}
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
	as := func(subject, who string) *tillClient {
		return &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, subject), who: who}
	}
	payRec := as(prPayRecSubject, "Payables and Receivables")
	sales := as(qwSalesUserID, "Sales")
	yardManager := as(wlYardManagerSubject, "Yard manager")
	hr := as(wlHRSubject, "HR")
	outsideSales := as(wlOutsideSalesSubject, "Outside sales")
	procurement := as(wlProcurementSubject, "Procurement manager")
	salesManager := as(wlSalesManagerSubject, "Sales manager")
	branchManager := as(wlBranchManagerSubject, "Branch manager at Trenton")
	branchManagerKgn := as(wlBranchManagerKgnSubject, "Branch manager at Kingston")
	generalManager := as(wlGeneralManagerSubject, "General manager")

	forbidden := func(name string, rsp demoResponse) {
		t.Helper()
		if rsp.status != http.StatusForbidden || strings.TrimSpace(string(rsp.body)) != strings.TrimSpace(string(authz.BodyForbidden)) {
			t.Errorf("%s answered %d %q, want the gate's shared refusal %q", name, rsp.status, rsp.body, authz.BodyForbidden)
		}
	}

	allowed := func(name string, rsp demoResponse, want ...int) {
		t.Helper()
		for _, w := range want {
			if rsp.status == w {
				return
			}
		}
		t.Errorf("RED %s answered %d %q, want %v", name, rsp.status, rsp.body, want)
	}

	// A real payment to bounce: the persona records a cheque against a Kingston invoice (I06).
	i06 := demoID("invoice:I06")
	payRec.call("POST", "/api/v1/ar/register/invoices/"+i06+"/payments", map[string]any{
		"method": "CHEQUE", "amount_cents": 10_000, "reference": "work level proof", "idempotency_key": uuid.NewString()}).ok(t, http.StatusOK)
	var paymentID string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
		 WHERE a.invoice_id = $1::uuid AND p.status = 'RECEIVED' AND p.method = 'CHEQUE' ORDER BY p.created_at DESC LIMIT 1`,
		i06).Scan(&paymentID); err != nil {
		t.Fatalf("the recorded cheque: %v", err)
	}
	c01 := demoID("customer:C01")
	i08 := demoID("invoice:I08")

	// 1. The work routes: refused for a selling role and the yard manager, with a payment id that
	// need not exist (the gate answers before the handler).
	other := uuid.NewString()
	work := []struct {
		name, method, path string
		body               map[string]any
	}{
		{"mark bounced", "POST", "/api/v1/ar/payments/" + other + "/nsf", map[string]any{"reason": "returned"}},
		{"statement run", "POST", "/api/v1/ar/statement-runs", map[string]any{"mode": "INTERIM", "customer_id": c01}},
		{"register statements", "POST", "/api/v1/ar/register/statements", map[string]any{"invoice_ids": []string{i08}, "idempotency_key": uuid.NewString()}},
		{"register reminders", "POST", "/api/v1/ar/register/reminders", map[string]any{"invoice_ids": []string{i08}, "tone": "FRIENDLY", "idempotency_key": uuid.NewString()}},
		{"account reminder", "POST", "/api/v1/ar/reminders", map[string]any{"customer_id": c01, "channel": "EMAIL", "tone": "FRIENDLY", "idempotency_key": uuid.NewString()}},
	}
	for _, r := range work {
		forbidden("a selling role on "+r.name, sales.call(r.method, r.path, r.body))
		forbidden("the yard manager on "+r.name, yardManager.call(r.method, r.path, r.body))
		forbidden("HR on "+r.name, hr.call(r.method, r.path, r.body))
	}

	// 2. The same routes for the persona: through the gate (the real payment is marked bounced, the
	// run answers 201; the sender-bound routes answer something other than the gate's refusal).
	allowed("the persona on mark bounced", payRec.call("POST", "/api/v1/ar/payments/"+paymentID+"/nsf", map[string]any{"reason": "returned"}), http.StatusOK)
	allowed("the persona on the statement run", payRec.call("POST", "/api/v1/ar/statement-runs", work[1].body), http.StatusCreated)
	for _, r := range work[2:] {
		// The gate's refusal is its shared body; a route's own plan refusal (the base plan has no
		// reminders) is a different 403 and proves the gate let the persona in.
		if rsp := payRec.call(r.method, r.path, r.body); rsp.status == http.StatusForbidden &&
			strings.TrimSpace(string(rsp.body)) == strings.TrimSpace(string(authz.BodyForbidden)) {
			t.Errorf("RED the persona was refused by the gate on %s: %s", r.name, rsp.body)
		}
	}

	// 3. The reads, at ruling 87's level: the payment trail is held to money desk WORK, so the
	// persona (money desk work) and the general manager (money desk manage, global) read both; the
	// sales manager and the branch manager pass the level through counter till manage, fenced to
	// their branches (the cheque and I06 sit at Trenton, so the Trenton pair reads them and a
	// Kingston only branch manager finds no such payment); outside sales, the yard manager and the
	// procurement manager (money desk read, no counter till) are refused both in the gate's shared
	// body, as is HR (neither surface).
	hist := "/api/v1/ar/payments/" + paymentID + "/history"
	invPays := "/api/v1/ar/invoices/" + i06 + "/payments"
	allowed("the persona on the payment history", payRec.call("GET", hist, nil), http.StatusOK)
	allowed("the persona on the invoice's payments", payRec.call("GET", invPays, nil), http.StatusOK)
	allowed("the general manager on the payment history", generalManager.call("GET", hist, nil), http.StatusOK)
	allowed("the general manager on the invoice's payments", generalManager.call("GET", invPays, nil), http.StatusOK)
	allowed("the Trenton sales manager on the Trenton payment history", salesManager.call("GET", hist, nil), http.StatusOK)
	allowed("the Trenton sales manager on the Trenton invoice's payments", salesManager.call("GET", invPays, nil), http.StatusOK)
	allowed("the Trenton branch manager on the Trenton payment history", branchManager.call("GET", hist, nil), http.StatusOK)
	allowed("the Trenton branch manager on the Trenton invoice's payments", branchManager.call("GET", invPays, nil), http.StatusOK)
	notFound := func(name string, rsp demoResponse) {
		t.Helper()
		if rsp.status == http.StatusForbidden &&
			strings.TrimSpace(string(rsp.body)) == strings.TrimSpace(string(authz.BodyForbidden)) {
			t.Errorf("RED %s answered the gate's refusal, want the branch fence's not found: %s", name, rsp.body)
			return
		}
		allowed(name, rsp, http.StatusNotFound)
	}
	notFound("the Kingston branch manager on the Trenton payment history", branchManagerKgn.call("GET", hist, nil))
	notFound("the Kingston branch manager on the Trenton invoice's payments", branchManagerKgn.call("GET", invPays, nil))
	forbidden("outside sales on the payment history", outsideSales.call("GET", hist, nil))
	forbidden("outside sales on the invoice's payments", outsideSales.call("GET", invPays, nil))
	forbidden("the yard manager on the payment history", yardManager.call("GET", hist, nil))
	forbidden("the yard manager on the invoice's payments", yardManager.call("GET", invPays, nil))
	forbidden("the procurement manager on the payment history", procurement.call("GET", hist, nil))
	forbidden("the procurement manager on the invoice's payments", procurement.call("GET", invPays, nil))
	forbidden("HR on the payment history", hr.call("GET", hist, nil))
	forbidden("HR on the invoice's payments", hr.call("GET", invPays, nil))

	// 3b. The third surface of the same ruling: the register's invoice detail is money desk READ, and
	// its activity timeline lists a PAYMENT entry per allocation (number, method, amount, who applied
	// it). Those entries are the payment trail, so they reach only a caller who holds the trail reads
	// above: the persona and the managers see them (the Kingston manager is fenced out of the Trenton
	// invoice altogether), the three money desk readers still open the invoice but the entries are
	// absent from their timeline, and HR is refused at the gate.
	// The first cheque was marked bounced above, which lifts its allocation, so the timeline needs a
	// fresh one.
	payRec.call("POST", "/api/v1/ar/register/invoices/"+i06+"/payments", map[string]any{
		"method": "CHEQUE", "amount_cents": 5_000, "reference": "trail proof", "idempotency_key": uuid.NewString()}).ok(t, http.StatusOK)
	detail := "/api/v1/ar/register/invoices/" + i06
	hasPayment := func(rsp demoResponse) bool { return strings.Contains(string(rsp.body), `"PAYMENT"`) }
	trailed := func(name string, c *tillClient) {
		t.Helper()
		rsp := c.call("GET", detail, nil)
		if rsp.status != http.StatusOK || !hasPayment(rsp) {
			t.Errorf("RED %s answered %d without the PAYMENT entries, want 200 with them: %s", name, rsp.status, rsp.body)
		}
	}
	trailless := func(name string, c *tillClient) {
		t.Helper()
		rsp := c.call("GET", detail, nil)
		if rsp.status != http.StatusOK {
			t.Errorf("RED %s answered %d, want 200 with the rest of the timeline: %s", name, rsp.status, rsp.body)
			return
		}
		if hasPayment(rsp) {
			t.Errorf("RED %s got the payment trail in the invoice's activity: %s", name, rsp.body)
		}
		if !strings.Contains(string(rsp.body), `"ISSUED"`) {
			t.Errorf("RED %s lost the rest of the timeline (no ISSUED entry): %s", name, rsp.body)
		}
	}
	trailed("the persona on the invoice detail", payRec)
	trailed("the general manager on the invoice detail", generalManager)
	trailed("the Trenton sales manager on the invoice detail", salesManager)
	trailed("the Trenton branch manager on the invoice detail", branchManager)
	trailless("outside sales on the invoice detail", outsideSales)
	trailless("the yard manager on the invoice detail", yardManager)
	trailless("the procurement manager on the invoice detail", procurement)
	notFound("the Kingston branch manager on the Trenton invoice detail", branchManagerKgn.call("GET", detail, nil))
	forbidden("HR on the invoice detail", hr.call("GET", detail, nil))

	// 4. The reverse stays at manage.
	forbidden("the persona on payment reverse", payRec.call("POST", "/api/v1/ar/payments/"+paymentID+"/reverse", map[string]any{"reason": "x"}))
}
