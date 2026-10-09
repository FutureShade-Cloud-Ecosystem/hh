//go:build demo

package main

// demoworld_payables_receivables_test.go: proof that the Payables and Receivables persona exists as the
// roles roadmap describes it and that its walls hold at the API (item 64, Lane A core).
//
// THE PERSONA is a global role (tier two, no branch fence): its grant carries no branch, and it works
// an invoice at Kingston and one at Trenton alike. THE WORLD it signs in to is the book's 18 invoices
// across the aging buckets and the one bounced payment the queue shows (demoseed_payables_receivables).
//
// THE WALLS, through the real routes and the real role gate:
//   - allowed: the aging, the bounced queue, a payment recorded against an overdue invoice, a promise to
//     pay;
//   - refused in the gate's shared refusal body: journal approval (the money desk at manage level, and
//     this role holds work) and the release of a credit hold (administer, a tier one role's).
//
// The database gate is the demo world proofs' own variable, and the harness refuses an address that is
// not local.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// prPayRecSubject is the persona's platform user id, repeated here on purpose, the pin every world
// proof uses for its fixtures (it equals the flow table's row).
const prPayRecSubject = "de110000-0000-4000-9000-0000000000a1"

// prControllerSubject is the Financial controller login this proof plants beside the world (the COD
// proof's pattern), a role label as the house rule demands: the contrast that shows the persona's
// refusals come from its role and not from the routes being shut to everyone.
const prControllerSubject = "hh-demo-payrec-proof-financial-controller"

func TestDemoWorld_PayablesReceivables(t *testing.T) {
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
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_payrec")
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

	appwrite := newAppwriteFake(t)
	appwrite.setTeam(prPayRecSubject, "org:"+demoOrgSlug)
	appwrite.setTeam(prControllerSubject, "org:"+demoOrgSlug)
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
	controller := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, prControllerSubject), who: "Financial controller"}
	payRec := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, prPayRecSubject), who: "Payables and Receivables"}

	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the demo org: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (email, display_name, status, org_id, platform_user_id)
		VALUES ('payrec.proof.controller@hh-demo.invalid', 'Financial controller (payables proof)', 'ACTIVE', $1, $2)`,
		orgID, prControllerSubject); err != nil {
		t.Fatalf("plant the Financial controller login: %v", err)
	}
	// A global role's grant carries no branch (identity.ErrGrantShape).
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id) VALUES ($1, 'financial_controller', $2)`,
		prControllerSubject, orgID); err != nil {
		t.Fatalf("plant the Financial controller grant: %v", err)
	}

	// 1. THE PERSONA: one live grant of the role, carrying no branch.
	var role string
	var branch *string
	if err := pool.QueryRow(ctx, `
		SELECT role_id, branch_id::text FROM staff_role_grant WHERE platform_user_id = $1 AND deleted_at IS NULL`,
		prPayRecSubject).Scan(&role, &branch); err != nil {
		t.Fatalf("the persona resolves no staff grant: %v", err)
	}
	if role != "payables_receivables" || branch != nil {
		t.Fatalf("the persona holds %s at branch %v, want payables_receivables with no branch (a global role)", role, branch)
	}

	// 2. THE WORLD: the book's 18 invoices and the bounced payment.
	var invoices int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE invoice_no ~ '^DEMO-INV-I[0-9]{2}$'`).Scan(&invoices); err != nil {
		t.Fatal(err)
	}
	if invoices != 18 {
		t.Fatalf("the world holds %d book invoices, want the 18 across the aging buckets", invoices)
	}

	// 3. THE QUEUE shows the bounced payment, by number and by the exact reference search.
	queue := payRec.call("GET", "/api/v1/ar/bounced", nil)
	queue.ok(t, http.StatusOK)
	if !strings.Contains(string(queue.body), "DEMO-PAY-B01") {
		t.Fatalf("the bounced queue does not show the seeded bounce: %s", queue.body)
	}
	byRef := payRec.call("GET", "/api/v1/ar/bounced?gateway_ref=DEMO-CHQ-B01", nil)
	byRef.ok(t, http.StatusOK)
	if !strings.Contains(string(byRef.body), "DEMO-PAY-B01") {
		t.Fatalf("the exact reference search does not find the bounce: %s", byRef.body)
	}

	// 4. THE AGING and the landing answer the whole book (no branch fence).
	aging := payRec.call("GET", "/api/v1/ar/aging", nil)
	aging.ok(t, http.StatusOK)
	landing := payRec.call("GET", "/api/v1/ar/landing", nil)
	landing.ok(t, http.StatusOK)

	// 5. A PAYMENT against an overdue invoice at the OTHER branch than the persona's grant (I06, a
	// Kingston invoice one to thirty days past due), then a promise to pay on another (I13, sixty days
	// and more). A role with no branch fence works either.
	i06 := demoID("invoice:I06")
	pay := payRec.call("POST", "/api/v1/ar/register/invoices/"+i06+"/payments", map[string]any{
		"method": "CHEQUE", "amount_cents": 10_000, "reference": "demo cheque", "idempotency_key": uuid.NewString()})
	pay.ok(t, http.StatusOK)
	i13 := demoID("invoice:I13")
	promise := payRec.call("POST", "/api/v1/ar/register/invoices/"+i13+"/promise", map[string]any{
		"promised_cents": 10_000, "promised_date": time.Now().AddDate(0, 0, 14).Format("2006-01-02"),
		"promised_by": "the account's payables clerk", "note": "calls on the first", "idempotency_key": uuid.NewString()})
	promise.ok(t, http.StatusOK, http.StatusCreated)

	// 5b. MARK BOUNCED (ruling 77 (a)): the cheque just recorded comes back. The persona marks it
	// bounced, the queue then shows it, and the invoice it had paid stands open for the whole again.
	var chequeID string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
		 WHERE a.invoice_id = $1::uuid AND p.status = 'RECEIVED' AND p.method = 'CHEQUE'
		 ORDER BY p.created_at DESC LIMIT 1`, i06).Scan(&chequeID); err != nil {
		t.Fatalf("the cheque the persona recorded: %v", err)
	}
	payRec.call("POST", "/api/v1/ar/payments/"+chequeID+"/nsf", map[string]any{"reason": "returned by the bank"}).ok(t, http.StatusOK)
	again := payRec.call("GET", "/api/v1/ar/bounced", nil)
	again.ok(t, http.StatusOK)
	if !strings.Contains(string(again.body), chequeID) {
		t.Fatalf("the queue does not show the payment the persona marked bounced: %s", again.body)
	}

	// 6. THE WALLS, in the gate's shared refusal body: journal approval and the release of a credit hold.
	refused := map[string]demoResponse{
		"journal approval": payRec.call("POST", "/api/v1/gl/journals/"+uuid.NewString()+"/approve", map[string]any{}),
		"hold release":     payRec.call("POST", "/api/v1/ar/holds/"+uuid.NewString()+"/ar-release", map[string]any{"reason": "x"}),
		"credit hold release": payRec.call("POST", "/api/v1/ar/credit-holds/"+uuid.NewString()+"/release",
			map[string]any{"reason": "x"}),
	}
	for name, rsp := range refused {
		rsp.ok(t, http.StatusForbidden)
		if strings.TrimSpace(string(rsp.body)) != strings.TrimSpace(string(authz.BodyForbidden)) {
			t.Errorf("%s was refused with %q, want the gate's shared refusal body %q", name, rsp.body, authz.BodyForbidden)
		}
	}

	// 7. THE CONTRAST: the Financial controller passes the same gates (the routes then answer for an id
	// that does not exist, not found), so the refusals above are the persona's role and nothing else.
	for name, path := range map[string]string{
		"journal approval": "/api/v1/gl/journals/" + uuid.NewString() + "/approve",
		"hold release":     "/api/v1/ar/holds/" + uuid.NewString() + "/ar-release",
	} {
		rsp := controller.call("POST", path, map[string]any{"reason": "x"})
		if rsp.status == http.StatusForbidden {
			t.Errorf("%s answered 403 to the Financial controller, the wall must be the persona's alone: %s", name, rsp.body)
		}
	}
}
