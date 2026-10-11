//go:build demo

package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The demo module's exit test (W2). It builds the world exactly the way
// W3's tests do, through the real one-shot (migrations in process, the
// seed and the populate through the built demo-shaped core), then boots
// the real serve role as a subprocess, the image's own shape with the
// boot guards live, and drives the two routes over HTTP:
//
//	POST /api/v1/demo/session {"flow":"sales"} answers a token, and that
//	token on GET /api/v1/pos/policy with X-Org: hh-demo answers 200;
//	a burst from one address is refused; the audit rows sit in audit_log
//	where the ledger writes, one per minted session and no more.
//
//	Run: point DATABASE_URL at a throwaway local Postgres (any container
//	or cluster on this machine; the helper refuses a non-local address),
//	then
//	     go test -tags demo ./internal/demo/ -run TestDemoModule -count=1 -p 1 -v
//
// Without DATABASE_URL the test skips, so the suite stays green wherever
// no throwaway Postgres exists.
func TestDemoModule_MintsASessionThatReachesTheTill(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "w2exit")
	bin := buildDemoCore(t)
	seedAndPopulate(t, bin, dsn)

	base := startDemoServe(t, bin, dsn)
	client := &http.Client{Timeout: 10 * time.Second}

	get := func(path string, headers map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatalf("build GET %s: %v", path, err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rsp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return rsp
	}
	post := func(path, body string) *http.Response {
		t.Helper()
		rsp, err := client.Post(base+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return rsp
	}
	// postWith is the burst leg's post: it carries a Cookie header the
	// test names, so the burst mints under its own fresh visitor key and
	// never spends the address key the earlier persona mints share.
	postWith := func(path, body string, headers map[string]string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("build POST %s: %v", path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rsp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return rsp
	}
	body := func(rsp *http.Response) string {
		t.Helper()
		raw, err := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		if err != nil {
			t.Fatalf("reading the body: %v", err)
		}
		return string(raw)
	}

	// The flow listing: the closed table, eight flows, the contract's three
	// fields, the labels the entry page renders. The yard-manager flow
	// (Manager Home's first per-role landing) shows on the front door's
	// picker, never on the till's own entry.
	flowsRsp := get("/api/v1/demo/flows", nil)
	if flowsRsp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/demo/flows: status %d, want 200 (body: %s)", flowsRsp.StatusCode, body(flowsRsp))
	}
	var listing struct {
		Flows []struct {
			Flow     string `json:"flow"`
			RoleCode string `json:"role_code"`
			Label    string `json:"label"`
		} `json:"flows"`
	}
	if err := json.Unmarshal([]byte(body(flowsRsp)), &listing); err != nil {
		t.Fatalf("the flows body does not decode: %v", err)
	}
	if len(listing.Flows) != 8 {
		t.Fatalf("the listing carries %d flows, want the closed table's eight", len(listing.Flows))
	}
	for i, want := range []struct{ flow, role, label string }{
		{"sales", "inside_sales", "Sales"},
		{"sales-manager", "sales_manager", "Sales manager"},
		{"yard-manager", "yard_manager", "Yard manager"},
		{"yard-team", "yard_team", "Yard Team"},
		{"driver", "driver", "Driver"},
		{"payables-receivables", "payables_receivables", "Payables and Receivables"},
		{"procurement-manager", "procurement_manager", "Procurement manager"},
		{"financial-controller", "financial_controller", "Financial Controller"},
	} {
		if listing.Flows[i].Flow != want.flow || listing.Flows[i].RoleCode != want.role ||
			listing.Flows[i].Label != want.label {
			t.Fatalf("listing row %d is %+v, want %q/%q/%q", i, listing.Flows[i], want.flow, want.role, want.label)
		}
	}

	// The exit pair: mint the sales flow, read the till's policy with the
	// token over the demo org.
	sessionRsp := post("/api/v1/demo/session", `{"flow":"sales"}`)
	if sessionRsp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/demo/session: status %d, want 200 (body: %s)", sessionRsp.StatusCode, body(sessionRsp))
	}
	var session struct {
		Token string `json:"token"`
		User  struct {
			UserID      string `json:"user_id"`
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
			RoleCode    string `json:"role_code"`
			RoleName    string `json:"role_name"`
			Tier        int    `json:"tier"`
			Scope       string `json:"scope"`
		} `json:"user"`
		Flow string `json:"flow"`
	}
	if err := json.Unmarshal([]byte(body(sessionRsp)), &session); err != nil {
		t.Fatalf("the session body does not decode: %v", err)
	}
	if strings.Count(session.Token, ".") != 2 {
		t.Fatalf("the token is not a JWT three-segment string: %q", session.Token)
	}
	if session.Flow != "sales" {
		t.Errorf("the answer echoes flow %q, want sales", session.Flow)
	}
	if session.User.UserID != "de110000-0000-4000-9000-000000000001" ||
		session.User.Email != "sales@hh-demo.invalid" ||
		session.User.DisplayName != "Sales (demo)" ||
		session.User.RoleCode != "inside_sales" ||
		session.User.RoleName != "Sales" {
		t.Errorf("the user block is %+v, want the flow table's sales account with the table's label", session.User)
	}

	policyRsp := get("/api/v1/pos/policy", map[string]string{
		"Authorization": "Bearer " + session.Token,
		"X-Org":         "hh-demo",
	})
	if policyRsp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/pos/policy with the minted token: status %d, want 200 (body: %s)",
			policyRsp.StatusCode, body(policyRsp))
	}
	var policy map[string]any
	if err := json.Unmarshal([]byte(body(policyRsp)), &policy); err != nil {
		t.Fatalf("the policy body does not decode: %v", err)
	}

	// The same token against another org is refused by the real gate: the
	// demo membership source answers the demo org only, so the minted
	// credential rides exactly the org it was minted for.
	otherRsp := get("/api/v1/pos/policy", map[string]string{
		"Authorization": "Bearer " + session.Token,
		"X-Org":         "other-yard",
	})
	if otherRsp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /api/v1/pos/policy with X-Org other-yard: status %d, want 403", otherRsp.StatusCode)
	}

	// The manager flow mints the credential W8's approval dialog sends:
	// a token for the manager account carrying the flow table's label.
	managerRsp := post("/api/v1/demo/session", `{"flow":"sales-manager"}`)
	if managerRsp.StatusCode != http.StatusOK {
		t.Fatalf("the manager mint: status %d, want 200 (body: %s)", managerRsp.StatusCode, body(managerRsp))
	}
	var manager struct {
		Token string `json:"token"`
		User  struct {
			UserID   string `json:"user_id"`
			RoleCode string `json:"role_code"`
			RoleName string `json:"role_name"`
			Tier     int    `json:"tier"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(body(managerRsp)), &manager); err != nil {
		t.Fatalf("the manager body does not decode: %v", err)
	}
	if manager.User.UserID != "de110000-0000-4000-9000-000000000002" ||
		manager.User.RoleCode != "sales_manager" || manager.User.RoleName != "Sales manager" {
		t.Errorf("the manager credential is %+v, want the flow table's manager account", manager.User)
	}
	if manager.User.Tier != 2 {
		t.Errorf("the manager tier is %d, want the resolver's answer (2)", manager.User.Tier)
	}

	// The buyer flow mints the Buying and Stock login: a branch scoped
	// manager whose one wall the roadmap names is the vendor bill post,
	// and the credential is the flow table's own account. The mint sits
	// before the burst loop, on the same address key the till's pair
	// (sales, sales-manager) just spent; the burst loop that follows
	// carries its own fresh visitor cookie, so the five pre-burst mints
	// on the address key (sales, sales-manager, buyer, yard, driver) do
	// not share the burst's key and the burst still has its full
	// DefaultBurstAllowance of unspent admissions to exercise the limit.
	buyerRsp := post("/api/v1/demo/session", `{"flow":"procurement-manager"}`)
	if buyerRsp.StatusCode != http.StatusOK {
		t.Fatalf("the buyer mint: status %d, want 200 (body: %s)", buyerRsp.StatusCode, body(buyerRsp))
	}
	var buyer struct {
		Token string `json:"token"`
		User  struct {
			UserID   string `json:"user_id"`
			RoleCode string `json:"role_code"`
			Tier     int    `json:"tier"`
			Scope    string `json:"scope"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(body(buyerRsp)), &buyer); err != nil {
		t.Fatalf("the buyer body does not decode: %v", err)
	}
	if buyer.User.UserID != "de64b000-0000-4000-9000-000000000001" ||
		buyer.User.RoleCode != "procurement_manager" || buyer.User.Scope != "BRANCH" {
		t.Errorf("the buyer credential is %+v, want the flow table's branch scoped buyer", buyer.User)
	}
	if buyer.User.Tier != 2 {
		t.Errorf("the buyer tier is %d, want the resolver's answer (2)", buyer.User.Tier)
	}

	// A flow outside the closed table is refused by the table, before the
	// limit is asked.
	unknownRsp := post("/api/v1/demo/session", `{"flow":"receivables"}`)
	if unknownRsp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unknown flow answered %d, want 400", unknownRsp.StatusCode)
	}

	// The personas. Each mints through the same gate as the till's pair (the
	// resolver's live answer, the table's label), each reads its own branch
	// only (the L-branch wall: its own branch answers, the other branch is
	// refused by the fence), and a surface the role holds at none is refused
	// by the server (the L-wall). The yard team's world is Trenton (the
	// roadmap's queue); the driver's is Kingston, where its seeded driver
	// and runs are.
	var trentonID, kingstonID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT'`).Scan(&trentonID); err != nil {
		t.Fatalf("the Trenton branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'KGN'`).Scan(&kingstonID); err != nil {
		t.Fatalf("the Kingston branch: %v", err)
	}
	mintPersona := func(flow, wantRole, wantLabel, wantID string) (string, map[string]string) {
		t.Helper()
		rsp := post("/api/v1/demo/session", `{"flow":"`+flow+`"}`)
		if rsp.StatusCode != http.StatusOK {
			t.Fatalf("the %s mint: status %d, want 200 (body: %s)", flow, rsp.StatusCode, body(rsp))
		}
		var session struct {
			Token string `json:"token"`
			User  struct {
				UserID   string `json:"user_id"`
				RoleCode string `json:"role_code"`
				RoleName string `json:"role_name"`
				Tier     int    `json:"tier"`
				Scope    string `json:"scope"`
			} `json:"user"`
		}
		if err := json.Unmarshal([]byte(body(rsp)), &session); err != nil {
			t.Fatalf("the %s session body does not decode: %v", flow, err)
		}
		if session.User.UserID != wantID || session.User.RoleCode != wantRole ||
			session.User.RoleName != wantLabel || session.User.Tier != 3 || session.User.Scope != "BRANCH" {
			t.Errorf("the %s credential is %+v, want %s/%s/%s at tier 3 BRANCH", flow, session.User, wantID, wantRole, wantLabel)
		}
		return session.Token, map[string]string{
			"Authorization": "Bearer " + session.Token,
			"X-Org":         "hh-demo",
		}
	}
	branchCodes := func(headers map[string]string) []string {
		t.Helper()
		rsp := get("/api/v1/branches", headers)
		if rsp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/v1/branches: status %d, want 200 (body: %s)", rsp.StatusCode, body(rsp))
		}
		var rows []struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(body(rsp)), &rows); err != nil {
			t.Fatalf("the branches body does not decode: %v", err)
		}
		codes := make([]string, 0, len(rows))
		for _, r := range rows {
			codes = append(codes, r.Code)
		}
		return codes
	}

	yardToken, yardHeaders := mintPersona("yard-team", "yard_team", "Yard Team", "d0200000-0000-4000-9000-000000000002")
	if codes := branchCodes(yardHeaders); len(codes) != 1 || codes[0] != "TRT" {
		t.Errorf("the yard persona reaches %v, want Trenton alone", codes)
	}
	if rsp := get("/api/v1/yard/pick-queue?branch_id="+trentonID, yardHeaders); rsp.StatusCode != http.StatusOK {
		t.Errorf("the yard queue at Trenton: status %d, want 200 (body: %s)", rsp.StatusCode, body(rsp))
	}
	if rsp := get("/api/v1/yard/pick-queue?branch_id="+kingstonID, yardHeaders); rsp.StatusCode != http.StatusForbidden {
		t.Errorf("the yard queue at Kingston: status %d, want the branch fence's 403", rsp.StatusCode)
	}
	if rsp := get("/api/v1/pos/policy", yardHeaders); rsp.StatusCode != http.StatusForbidden {
		t.Errorf("the till's policy as the yard team: status %d, want the server's 403", rsp.StatusCode)
	}

	driverToken, driverHeaders := mintPersona("driver", "driver", "Driver", "d0200000-0000-4000-9000-000000000003")
	if codes := branchCodes(driverHeaders); len(codes) != 1 || codes[0] != "KGN" {
		t.Errorf("the driver persona reaches %v, want Kingston alone", codes)
	}
	runsRsp := get("/api/v1/dispatch/my-runs?date="+seedClock.UTC().Format("2006-01-02"), driverHeaders)
	if runsRsp.StatusCode != http.StatusOK {
		t.Errorf("the driver's runs: status %d, want 200 (body: %s)", runsRsp.StatusCode, body(runsRsp))
	}
	// The runs themselves are Kingston's: the 200 alone only shows the
	// grant does not trap the login. The seeded driver row, and every run
	// the driver's own read answers, sit at Kingston.
	var driverRuns struct {
		Items []struct {
			RunNo    string `json:"run_no"`
			BranchID string `json:"branch_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(runsRsp)), &driverRuns); err != nil {
		t.Fatalf("the driver's runs body does not decode: %v", err)
	}
	if len(driverRuns.Items) == 0 {
		t.Errorf("the driver's day holds no run, want the seeded Kingston runs")
	}
	for _, run := range driverRuns.Items {
		if run.BranchID != kingstonID {
			t.Errorf("the driver's run %s sits at branch %s, want Kingston %s", run.RunNo, run.BranchID, kingstonID)
		}
	}
	var driverRowBranch string
	if err := pool.QueryRow(ctx,
		`SELECT branch_id::text FROM dispatch_drivers WHERE user_id = $1::uuid AND is_active`,
		"d0200000-0000-4000-9000-000000000003").Scan(&driverRowBranch); err != nil {
		t.Fatalf("the persona's seeded driver row: %v", err)
	}
	if driverRowBranch != kingstonID {
		t.Errorf("the persona's driver row sits at branch %s, want Kingston %s", driverRowBranch, kingstonID)
	}
	// Refused at Trenton: the branch list leaves it out (checked above) and
	// the server fences a read named at it. The role already holds yard-pick
	// at none, so this 403 is the gates' joint answer, and the one grant
	// check below is what pins the branch half.
	if rsp := get("/api/v1/yard/pick-queue?branch_id="+trentonID, driverHeaders); rsp.StatusCode != http.StatusForbidden {
		t.Errorf("the driver at Trenton: status %d, want 403", rsp.StatusCode)
	}
	var liveGrants int
	var liveAt string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), coalesce(min(branch_id::text), '') FROM user_branch_grants WHERE user_id = $1::uuid AND deleted_at IS NULL`,
		"d0200000-0000-4000-9000-000000000003").Scan(&liveGrants, &liveAt); err != nil {
		t.Fatalf("the driver's live branch grants: %v", err)
	}
	if liveGrants != 1 || liveAt != kingstonID {
		t.Errorf("the driver holds %d live branch grants (first %s), want one at Kingston %s", liveGrants, liveAt, kingstonID)
	}
	if rsp := get("/api/v1/pos/policy", driverHeaders); rsp.StatusCode != http.StatusForbidden {
		t.Errorf("the till's policy as the driver: status %d, want the server's 403", rsp.StatusCode)
	}
	_ = yardToken
	_ = driverToken

	// A burst from this address is refused: hammer the mint until the
	// limit answers, and hold that everything before the refusal minted
	// and the refusal itself did not. The burst carries its own fresh
	// visitor cookie, so the five earlier persona mints on the bare
	// address key (they posted without one) do not share the burst's
	// key: the burst sits at allowance unspent when it starts, admits
	// DefaultBurstAllowance mints, and the next iteration is the
	// refusal. The address ceiling of 40 still covers the run.
	burstCookie := "demo_visitor=" + fmt.Sprintf("%032x", uint64(time.Now().UnixNano()))
	burstMinted := 0
	refused := false
	for i := 0; i < DefaultBurstAllowance*3; i++ {
		rsp := postWith("/api/v1/demo/session", `{"flow":"sales"}`, map[string]string{"Cookie": burstCookie})
		raw := body(rsp)
		if rsp.StatusCode == http.StatusOK {
			burstMinted++
			continue
		}
		if rsp.StatusCode == http.StatusTooManyRequests {
			refused = true
			if !strings.Contains(raw, "too many sign-in attempts") {
				t.Errorf("the burst refusal does not name the limit: %s", raw)
			}
			if strings.Contains(raw, "token") {
				t.Errorf("the burst refusal carries a token: %s", raw)
			}
			break
		}
		t.Fatalf("the burst hit %d with body %s, want only admissions and the limit's refusal", rsp.StatusCode, raw)
	}
	if !refused {
		t.Fatal("the burst was never refused, the limit did not fire")
	}
	if burstMinted == 0 {
		t.Fatal("the burst admitted nothing, the earlier mints should have left allowance unspent")
	}

	// The trail: one DEMO_SESSION_MINTED row per minted session and no
	// more, in audit_log where the ledger writes, naming the accounts the
	// sessions went to. The refusals left none, of either kind: the coarse
	// middleware takes no row for this route, so a refused mint (400, 429)
	// leaves audit_log untouched and a successful mint leaves exactly the
	// one module row. The coarse write is asynchronous off the response,
	// so the count waits out its goroutines before holding the zero.
	time.Sleep(500 * time.Millisecond)
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'DEMO_SESSION_MINTED'`).Scan(&rows); err != nil {
		t.Fatalf("the audit read failed: %v", err)
	}
	if rows != 5+burstMinted {
		t.Errorf("audit_log holds %d DEMO_SESSION_MINTED rows, want one per minted session (%d)", rows, 5+burstMinted)
	}
	var namedActors int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		 WHERE action = 'DEMO_SESSION_MINTED'
		   AND entity_type = 'demo.session'
		   AND actor_id::text IN ('de110000-0000-4000-9000-000000000001', 'de110000-0000-4000-9000-000000000002',
		                          'd0200000-0000-4000-9000-000000000002', 'd0200000-0000-4000-9000-000000000003',
		                          'de64b000-0000-4000-9000-000000000001')`).Scan(&namedActors); err != nil {
		t.Fatalf("the audit actor read failed: %v", err)
	}
	if namedActors != rows {
		t.Errorf("%d of %d trail rows name a demo role account, want every one", namedActors, rows)
	}

	var coarseRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		 WHERE action = 'HTTP.POST'
		   AND changes->>'path' = '/api/v1/demo/session'`).Scan(&coarseRows); err != nil {
		t.Fatalf("the coarse trail read failed: %v", err)
	}
	if coarseRows != 0 {
		t.Errorf("%d coarse HTTP.POST rows name the session route, want none: the mint's own row is the whole record", coarseRows)
	}
}

// startDemoServe boots the built binary's serve role as a subprocess with
// the minimal demo environment (the box says demo twice, one database by
// both names, the audiences, the vault key the world was sealed under,
// the acceptance signing pair, one local address) and waits for the
// listener. The environment is exactly this list: the provider guard runs
// against it, so nothing else may ride along. The subprocess is stopped
// at cleanup.
func startDemoServe(t *testing.T, bin, dsn string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("a local port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	env := []string{
		"DEMO_MODE=true",
		"APP_ENV=demo",
		"CORE_DATABASE_URL=" + dsn,
		"CORE_STAFF_AUDIENCE=hh-staff",
		"CORE_TENANT_AUDIENCE=hh-tenant",
		"PAYMENT_VAULT_KEY=" + testVaultKey,
		"HH_ACCEPTANCE_SIGN_KEY_ID=demo-accept-1",
		"HH_ACCEPTANCE_SIGN_KEY=" + testVaultKey,
		"CORE_LISTEN_ADDR=127.0.0.1:" + fmt.Sprint(port),
	}
	cmd := exec.Command(bin, "serve")
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the demo serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("demo serve output:\n%s", out.String())
		}
	})

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	for {
		rsp, err := client.Get(base + "/healthz")
		if err == nil {
			rsp.Body.Close()
			if rsp.StatusCode == http.StatusOK {
				return base
			}
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("the demo serve never answered /healthz; its output:\n%s", out.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
