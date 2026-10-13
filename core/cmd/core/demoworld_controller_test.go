//go:build demo

package main

// demoworld_controller_test.go, the Financial Controller's world, proven on a throwaway local Postgres
// through the real registered routes (roadmap 2.9, the first increment): the controller decides the
// seeded floor request FR1 and the approval is used up on that one quote; it approves the parked
// journal; it cannot decide its own request; it reads the trial balance; and its walls hold.
//
// The world is built the way a box builds it: the frozen migration set, the first `demoseed -apply`,
// the REAL demo populate in process (now three role accounts), then `demoseed -apply` again, which adds
// the quote and controller worlds. The database gate is the demo world proof's own variable.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The controller account the demo populate plants (core/internal/demo/populate.go, pinned there). The
// literal is repeated on purpose: if the populate ever moves it, this test goes red.
const cwControllerUserID = "de110000-0000-4000-9000-0000000000c1"

// cwForbidden is the role gate's shared refusal body.
const cwForbidden = `{"error":"Forbidden"}`

type cwApproval struct {
	ID          string `json:"id"`
	RequestedBy string `json:"requested_by"`
	BranchID    string `json:"branch_id"`
	Policy      string `json:"policy_type"`
	Status      string `json:"status"`
	SubjectRef  string `json:"subject_ref"`
}

type cwJournal struct {
	ID         string `json:"id"`
	EntryNo    int    `json:"entry_no"`
	FiscalYear int    `json:"fiscal_year"`
	Status     string `json:"status"`
	ApprovedBy string `json:"approved_by"`
	CreatedBy  string `json:"created_by"`
}

type cwTrialBalance struct {
	FiscalYear   int   `json:"fiscal_year"`
	TotalDebit   int64 `json:"total_debit_cents"`
	TotalCredit  int64 `json:"total_credit_cents"`
	BalanceCents int64 `json:"balance_cents"`
	Rows         []struct {
		AccountCode string `json:"account_code"`
		DebitCents  int64  `json:"debit_cents"`
		CreditCents int64  `json:"credit_cents"`
	} `json:"rows"`
}

func TestDemoWorld_Controller(t *testing.T) {
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

	// 1. the world: migrate, first apply (no logins, so no controller rows), populate, second apply.
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_controller")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	t.Setenv("APP_ENV", "dev")
	qwApply(t)
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM gl_journals WHERE created_by IS NOT NULL AND memo LIKE '%(demo)'`); n != 0 {
		t.Fatalf("the first apply (no demo logins yet) left %d parked demo journals", n)
	}
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM users WHERE id = $1::uuid AND email = 'financial.controller@hh-demo.invalid' AND status = 'ACTIVE'`, cwControllerUserID); n != 1 {
		t.Fatalf("the populate planted %d controller accounts, want 1", n)
	}
	var tables = []string{"gl_journals", "gl_journal_lines", "permission_approval_requests"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = qwCount(t, ctx, pool, `SELECT count(*) FROM `+tb)
	}
	var sequencesBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_entry_no_sequences`).Scan(&sequencesBefore); err != nil {
		t.Fatal(err)
	}
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the second apply created nothing")
	}
	census := map[string]int{"gl_journals": 2, "gl_journal_lines": 4, "permission_approval_requests": 1 + 2}
	for tb, add := range census {
		if n := qwCount(t, ctx, pool, `SELECT count(*) FROM `+tb); n != before[tb]+add {
			t.Errorf("%s holds %d rows after the second apply, want %d plus the worlds' %d", tb, n, before[tb], add)
		}
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM permission_approval_requests WHERE policy_type = 'JE_OVER_THRESHOLD' AND status = 'PENDING'`); n != 2 {
		t.Errorf("pending journal requests: %d, want 2", n)
	}

	// 2. the entry numbers are the real counter's: distinct, the counter sits on the last one, and a
	// repeat apply writes nothing and burns no number.
	var lastEntry int
	if err := pool.QueryRow(ctx, `SELECT max(entry_no) FROM gl_journals`).Scan(&lastEntry); err != nil || lastEntry < 2 {
		t.Fatalf("the parked journals' entry numbers read max %d (%v)", lastEntry, err)
	}
	var counter int
	if err := pool.QueryRow(ctx, `SELECT max(last_value) FROM gl_entry_no_sequences`).Scan(&counter); err != nil || counter != lastEntry {
		t.Errorf("the entry counter reads %d (%v), want the last journal's number %d", counter, err, lastEntry)
	}
	if _, created := qwApply(t); created != 0 {
		t.Errorf("a repeat apply created %d rows, the seed must be idempotent", created)
	}
	var counterAfter int
	if err := pool.QueryRow(ctx, `SELECT max(last_value) FROM gl_entry_no_sequences`).Scan(&counterAfter); err != nil || counterAfter != counter {
		t.Errorf("a repeat apply moved the entry counter from %d to %d (%v): it burned a number", counter, counterAfter, err)
	}

	// 3. the served surface, one login per role the legs need.
	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the demo org: %v", err)
	}
	appwrite := newAppwriteFake(t)
	for _, sub := range []string{cwControllerUserID, qwSalesUserID, qwManagerUserID} {
		appwrite.setTeam(sub, "org-"+demoOrgSlug)
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
	consoleSessionValues(t, env.minter, values)
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)
	controller := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, cwControllerUserID), who: "Financial Controller"}
	sales := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwSalesUserID), who: "Sales"}
	manager := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwManagerUserID), who: "Sales manager"}

	listPending := func(c *tillClient) map[string]cwApproval {
		t.Helper()
		// The contract's page: items and next_cursor, followed two rows at a time to prove the cursor
		// walks every row once.
		out := map[string]cwApproval{}
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > 20 {
				t.Fatal("the approvals cursor never ended")
			}
			path := "/api/v1/staff/approvals?status=PENDING&limit=2"
			if cursor != "" {
				path += "&cursor=" + cursor
			}
			rsp := c.call("GET", path, nil)
			rsp.ok(t, http.StatusOK)
			var page struct {
				Items      []cwApproval `json:"items"`
				NextCursor *string      `json:"next_cursor"`
			}
			rsp.decode(t, &page)
			for _, r := range page.Items {
				if _, dup := out[r.ID]; dup {
					t.Fatalf("approval %s came on two pages", r.ID)
				}
				out[r.ID] = r
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		return out
	}
	decide := func(c *tillClient, id string, approve bool) demoResponse {
		return c.call("POST", "/api/v1/staff/approvals/"+id+"/decide", map[string]any{"approve": approve, "reason": "demo decision"})
	}
	fr1 := demoID("floorrequest:FR1")
	j1Req, j2Req := demoID("jerequest:J1"), demoID("jerequest:J2")
	j1, j2 := demoID("journal:J1"), demoID("journal:J2")
	q1 := demoID("quote:Q1")

	// 4. the card lands on approvals: the controller's queue holds the floor request, the journal it can
	// decide, its own journal, and the freight raises of both branches (it reaches every branch).
	queue := listPending(controller)
	for name, id := range map[string]string{"FR1": fr1, "J1's request": j1Req, "J2's request (its own)": j2Req} {
		if _, ok := queue[id]; !ok {
			t.Errorf("the controller's queue does not hold %s", name)
		}
	}
	branches := map[string]bool{}
	for _, r := range queue {
		branches[r.BranchID] = true
	}
	if len(branches) < 2 {
		t.Errorf("the controller's queue spans %d branch(es), want both (a global role)", len(branches))
	}
	// The branch Sales manager filed J1 itself, so the four eyes rule bars it first; it holds no authority
	// over a journal it did not file either (the leadership set decides a journal).
	if rsp := decide(manager, j1Req, true); rsp.status != http.StatusForbidden || !strings.Contains(string(rsp.body), "separation of duties") {
		t.Errorf("the Sales manager decided its own journal request: status %d body %s", rsp.status, rsp.body)
	}
	if rsp := decide(manager, j2Req, true); rsp.status != http.StatusForbidden || !strings.Contains(string(rsp.body), "not authorized") {
		t.Errorf("the Sales manager decided the controller's journal request: status %d body %s, want the policy's refusal", rsp.status, rsp.body)
	}

	// 5. FR1: sales is refused before the decision; the controller approves; the approval is used up on
	// that one line, and a second use (the same discount asked again after backing it out) is refused.
	patch := func(disc int) demoResponse {
		return sales.call("PATCH", "/api/v1/quotes/"+q1+"/lines/1", map[string]any{"disc_pct": disc})
	}
	patch(12).ok(t, http.StatusConflict)
	if rsp := decide(sales, fr1, true); rsp.status != http.StatusForbidden {
		t.Errorf("the requester decided its own floor request: status %d", rsp.status)
	}
	decide(controller, fr1, true).ok(t, http.StatusOK)
	if got := qwCount(t, ctx, pool, `SELECT count(*) FROM permission_approval_requests WHERE id = $1::uuid AND status = 'APPROVED' AND decided_by = $2::uuid`, fr1, cwControllerUserID); got != 1 {
		t.Errorf("FR1 is not approved by the controller in the table (%d rows)", got)
	}
	patch(12).ok(t, http.StatusOK)
	if got := qwCount(t, ctx, pool, `SELECT count(*) FROM permission_approval_requests WHERE id = $1::uuid AND data->>'used' = 'true'`, fr1); got != 1 {
		t.Errorf("FR1 is not marked used after the write that spent it (%d rows)", got)
	}
	// Used up: a deeper discount on the same line is not covered, and neither is any other line.
	deeper := sales.call("PATCH", "/api/v1/quotes/"+q1+"/lines/1", map[string]any{"disc_pct": 14})
	deeper.ok(t, http.StatusConflict)
	if !strings.Contains(string(deeper.body), "approver_roles") {
		t.Errorf("the second use is refused without the floor refusal body: %s", deeper.body)
	}
	sales.call("PATCH", "/api/v1/quotes/"+q1+"/lines/2", map[string]any{"disc_pct": 40}).ok(t, http.StatusConflict)
	decide(controller, fr1, true).ok(t, http.StatusConflict)

	// 6. J1: the trial balance before, the controller approves through the decide route, the journal is
	// posted by the controller and the trial balance carries it.
	year := qwCount(t, ctx, pool, `SELECT fiscal_year FROM gl_journals WHERE id = $1::uuid`, j1)
	tb := func() cwTrialBalance {
		t.Helper()
		rsp := controller.call("GET", "/api/v1/gl/trial-balance?year="+strconv.Itoa(year), nil)
		rsp.ok(t, http.StatusOK)
		var out cwTrialBalance
		rsp.decode(t, &out)
		return out
	}
	tbBefore := tb()
	if tbBefore.TotalDebit != tbBefore.TotalCredit || tbBefore.BalanceCents != 0 {
		t.Errorf("the trial balance before is out of balance: %+v", tbBefore)
	}
	readJournal := func(id string) cwJournal {
		t.Helper()
		rsp := controller.call("GET", "/api/v1/gl/journals/"+id, nil)
		rsp.ok(t, http.StatusOK)
		var j cwJournal
		rsp.decode(t, &j)
		return j
	}
	if j := readJournal(j1); j.Status != "DRAFT" {
		t.Fatalf("J1 reads %s before the decision, want DRAFT", j.Status)
	}
	// One way to approve a parked journal: the GL's own approve route refuses a journal whose request is
	// pending, in words naming the approvals queue, and writes nothing (finding 3).
	viaGL := controller.call("POST", "/api/v1/gl/journals/"+j1+"/approve", nil)
	viaGL.ok(t, http.StatusConflict)
	if !strings.Contains(string(viaGL.body), "approvals queue") {
		t.Errorf("the GL approve route refused without naming the approvals queue: %s", viaGL.body)
	}
	if j := readJournal(j1); j.Status != "DRAFT" {
		t.Fatalf("J1 reads %s after the refused GL approve, want DRAFT", j.Status)
	}
	if got := qwCount(t, ctx, pool, `SELECT count(*) FROM permission_approval_requests WHERE id = $1::uuid AND status = 'PENDING'`, j1Req); got != 1 {
		t.Errorf("J1's request left PENDING after the refused GL approve (%d rows)", got)
	}
	decide(controller, j1Req, true).ok(t, http.StatusOK)
	if j := readJournal(j1); j.Status != "POSTED" || j.ApprovedBy != cwControllerUserID {
		t.Errorf("J1 reads %s approved by %q after the decision, want POSTED by the controller", j.Status, j.ApprovedBy)
	}
	tbAfter := tb()
	if tbAfter.TotalDebit-tbBefore.TotalDebit != 750_000 || tbAfter.TotalCredit-tbBefore.TotalCredit != 750_000 || tbAfter.BalanceCents != 0 {
		t.Errorf("the trial balance moved by debit %d credit %d (balance %d), want the journal's 750000 each side",
			tbAfter.TotalDebit-tbBefore.TotalDebit, tbAfter.TotalCredit-tbBefore.TotalCredit, tbAfter.BalanceCents)
	}
	decide(controller, j1Req, true).ok(t, http.StatusConflict)

	// 7. J2 is the controller's own: both decisions are refused in the separation of duties words and
	// the journal stays parked; the GL's own approve route refuses the maker too.
	for _, approve := range []bool{true, false} {
		rsp := decide(controller, j2Req, approve)
		rsp.ok(t, http.StatusForbidden)
		if !strings.Contains(string(rsp.body), "separation of duties") {
			t.Errorf("the self decision (approve=%v) is refused without the separation of duties words: %s", approve, rsp.body)
		}
	}
	controller.call("POST", "/api/v1/gl/journals/"+j2+"/approve", nil).ok(t, http.StatusForbidden)
	if j := readJournal(j2); j.Status != "DRAFT" || j.CreatedBy != cwControllerUserID {
		t.Errorf("J2 reads %s made by %q, want a DRAFT made by the controller", j.Status, j.CreatedBy)
	}
	if got := qwCount(t, ctx, pool, `SELECT count(*) FROM permission_approval_requests WHERE id = $1::uuid AND status = 'PENDING'`, j2Req); got != 1 {
		t.Errorf("J2's request left PENDING (%d rows)", got)
	}

	// 8. the walls: surfaces the matrix gives the controller at None are refused in the gate's shared
	// body, and the same routes answer for the roles that hold them.
	quoteRead := controller.call("GET", "/api/v1/quotes/"+q1, nil)
	if quoteRead.status != http.StatusForbidden || strings.TrimSpace(string(quoteRead.body)) != cwForbidden {
		t.Errorf("the controller reads a quote (quote desk, None): status %d body %s, want the gate's refusal", quoteRead.status, quoteRead.body)
	}
	quoteMake := controller.call("POST", "/api/v1/quotes", map[string]any{"customer_id": demoID("customer:C02"), "branch_id": trentonBranchID})
	if quoteMake.status != http.StatusForbidden || strings.TrimSpace(string(quoteMake.body)) != cwForbidden {
		t.Errorf("the controller starts a quote: status %d body %s, want the gate's refusal", quoteMake.status, quoteMake.body)
	}
	manager.call("GET", "/api/v1/quotes/"+q1, nil).ok(t, http.StatusOK)
	// The sales login holds no Money Desk: the ledger is refused to it in the same body.
	ledger := sales.call("GET", "/api/v1/gl/trial-balance?year="+strconv.Itoa(year), nil)
	if ledger.status != http.StatusForbidden {
		t.Errorf("Sales reads the trial balance: status %d, want the gate's refusal", ledger.status)
	}
}
