package main

// demoworld_countertill_test.go, item W0: proof of the demo world at the till.
//
// The test builds the demo world the way a demo box does, over a throwaway local Postgres: the
// frozen migration set, then demoseed -plan and demoseed -apply, then, in the test only, the
// identity steps the demo populate will take (identity.ProvisionOrgAnchor and
// identity.StampOrgScope for hh-demo), two counter staff rows with platform_user_id set, their
// user_branch_grants and staff_role_grant rows at Trenton, and one active payment configuration
// with an invented merchant id and invented credentials that are not keys, sealed under a vault
// key this test generates and never writes anywhere. testkit.Build is NOT called: the database
// holds exactly one org, hh-demo, and none of the kit's fixture orgs.
//
// Everything after setup runs through the real registerRoutes as Sales (inside_sales) and, where
// the story says so, as the Sales manager (sales_manager): the MAIN drawer at Trenton, a walk-in
// cash sale of three lines with cash tendered, a card sale on a fixed demo token, an account sale
// for C02 and a refused one for C09, C01's Holland pavers with no cost in the body, a parked
// ticket resumed by the manager, the margin glance as each role, a seeded sale above 250.00 at
// Trenton, a cash return of one line within 250.00, the card sale returned whole above 250.00,
// and GET /api/v1/payments/config.
//
// The database gate is this test's own variable, and the harness refuses an address on it that is
// not local, judged on what the driver would actually dial (localhost, a loopback address or a
// local socket only): a proof of the demo world never points at a shared database. Absent the
// variable the test skips politely.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// tillDemoDBEnvVar gates this test on its own throwaway database. It is deliberately separate from
// the composition suites' gate: those build the kit's fixture world with testkit.Build, and this
// test's whole point is a database that holds ONLY the demo world.
const tillDemoDBEnvVar = "HH_TILL_DEMO_TEST_DATABASE_URL"

// The demo facts W0 pins. The Trenton branch id is migration 0003's own; the subjects, the
// merchant id, the card token and the credential are invented demo values (the credential is NOT
// a key), and the vault key that seals the credential is generated at run time and never printed
// or filed.
const (
	// demoOrgSlug lives in demoseed_build.go now (the seed stamps it); this block keeps the
	// rest of the demo facts W0 pins.
	trentonBranchID = "5d1b3f6e-0000-4000-9000-000000000001"
	salesSubject    = "hh-demo-counter-sales"
	salesMgrSubject = "hh-demo-counter-sales-manager"
	demoMerchantID  = "DEMO-MID-0001"
	demoCardToken   = "demo-card-token-0001"
	demoInvCred     = "demo-invented-credential-not-a-key"
	demoSeedMarker  = "hos-demoseed-v1"

	// The drawer's opening float and the cash tendered on the walk-in sale, in cents. The tendered
	// figure must clear the walk-in sale's total so the ring carries a change-due answer.
	demoDrawerFloatCents = 20_000
	demoCashTendered     = 20_000

	// The refund amount the till's rules name (the dealer's default), in cents, as the story's
	// within/above line for the two returns.
	refundAmountCents = 25_000
)

// requireLocalDSN refuses any database address that is not local. The judgment lives in
// checkLocalDSN so the refusal itself has its own test; this wrapper is its one fatal call site.
func requireLocalDSN(t *testing.T, dsn string) {
	t.Helper()
	if err := checkLocalDSN(dsn); err != nil {
		t.Fatal(err.Error())
	}
}

// checkLocalDSN judges a database address on what the driver would actually dial, not on the
// address text: it is parsed with pgconn.ParseConfig, the same parser pgxpool uses, at home in
// both the URL and the keyword form, with the host= parameter and the environment fallbacks
// already applied, and every host it would dial, the primary and every fallback, must be local:
// localhost, a loopback address, or a unix socket directory. An address the parser refuses is
// refused, and so is an empty resolved host: that would leave the dial target to the
// environment.
func checkLocalDSN(dsn string) error {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("the database address does not parse: %w", err)
	}
	hosts := make([]string, 0, 1+len(cfg.Fallbacks))
	hosts = append(hosts, cfg.Host)
	for _, fb := range cfg.Fallbacks {
		hosts = append(hosts, fb.Host)
	}
	for _, host := range hosts {
		if hostIsLocal(host) {
			continue
		}
		return fmt.Errorf("the database address is not local (it would dial host %q): the demo world proof runs only against localhost, a loopback address or a local socket", host)
	}
	return nil
}

// hostIsLocal answers for one resolved dial target: localhost, an address the net package
// judges loopback (parsed, never a text prefix, so the IPv4 loopback range and the IPv6
// loopback alike hold while a host name that merely begins like one is refused), or a unix
// socket directory (an absolute path).
func hostIsLocal(host string) bool {
	if host == "localhost" {
		return true
	}
	if strings.HasPrefix(host, "/") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// freshDemoDB creates this test's own database from zero on the server the DSN names and applies
// the frozen migration set, the same files and the same order the migrate role applies. Nothing is
// shared with another test or another run. It returns the pool and the DSN of the created
// database, which every later step (the seed, the served surface) must point at.
func freshDemoDB(t *testing.T, ctx context.Context, dsn string) (*pgxpool.Pool, string) {
	t.Helper()
	name := "demoworld_till"
	adminDSN, testDSN := swapDSNDatabase(dsn, "postgres"), swapDSNDatabase(dsn, name)

	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("dropping %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}
	t.Cleanup(pool.Close)

	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found (err=%v)", err)
	}
	sort.Strings(files)
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", filepath.Base(f), err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("applying %s: %v", filepath.Base(f), err)
		}
	}
	return pool, testDSN
}

// swapDSNDatabase rewrites the database name in a postgres DSN, leaving the server the address
// dials alone. It parses with the same parser the local-only guard judges the address with and
// re-serializes the keyword form, so every form the guard accepts (a URL, the keyword form, a
// host list) swaps correctly. The swapped address spells sslmode=disable: the throwaway local
// server carries no TLS and the proof never leaves the box.
func swapDSNDatabase(dsn, name string) string {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		// The local-only guard refuses an address the parser cannot read before this runs.
		return dsn
	}
	hosts := []string{cfg.Host}
	ports := []string{strconv.Itoa(int(cfg.Port))}
	for _, fb := range cfg.Fallbacks {
		hosts = append(hosts, fb.Host)
		ports = append(ports, strconv.Itoa(int(fb.Port)))
	}
	parts := []string{
		"host=" + strings.Join(hosts, ","),
		"port=" + strings.Join(ports, ","),
	}
	if cfg.User != "" {
		parts = append(parts, "user="+quoteKeyword(cfg.User))
	}
	if cfg.Password != "" {
		parts = append(parts, "password="+quoteKeyword(cfg.Password))
	}
	parts = append(parts, "dbname="+quoteKeyword(name), "sslmode=disable")
	return strings.Join(parts, " ")
}

// quoteKeyword renders one keyword-form value, quoted and escaped when it carries a character
// the unquoted form cannot hold.
func quoteKeyword(v string) string {
	if strings.ContainsAny(v, " '\\") {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(v, "\\", "\\\\"), "'", "\\'") + "'"
	}
	return v
}

// demoseedPlanOutput is what the test reads out of the -plan output. The clock is load bearing:
// the world's clock assertions compare against it. The row count and the report's two dated
// figures are recorded only (the lead's W0 ruling: the report is not the world), so an
// unreadable dated range is carried as empty and logged, never fatal.
type demoseedPlanOutput struct {
	clock     time.Time
	firstDate string
	lastDate  string
	rows      int
}

// demoseedRun drives the demoseed subcommand the way a demo box's entrypoint does, -plan first and
// then -apply, and returns what the -plan output holds.
func demoseedRun(t *testing.T, dsn string) demoseedPlanOutput {
	t.Helper()
	runRole := func(args ...string) string {
		f, err := os.CreateTemp(t.TempDir(), "demoseed-out")
		if err != nil {
			t.Fatalf("temp output: %v", err)
		}
		defer f.Close()
		if err := demoseedRole(args, f, os.Getenv); err != nil {
			t.Fatalf("demoseed %s: %v", strings.Join(args, " "), err)
		}
		raw, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatalf("reading demoseed output: %v", err)
		}
		return string(raw)
	}

	planOut := runRole("-plan")
	var parsed demoseedPlanOutput
	for _, line := range strings.Split(planOut, "\n") {
		if v, ok := strings.CutPrefix(line, "  clock          "); ok {
			at, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
			if err != nil {
				t.Fatalf("demoseed -plan clock line %q does not parse: %v", v, err)
			}
			parsed.clock = at
		}
		if rest, ok := strings.CutPrefix(line, "  plan           "); ok {
			// Recorded only: the report's own summary of its row count and dated range. A line
			// that changed shape costs the run its recorded figures, never its verdict.
			rest = strings.TrimSpace(rest)
			var rows int
			if _, err := fmt.Sscanf(rest, "%d rows across", &rows); err == nil {
				parsed.rows = rows
			}
			if at := strings.Index(rest, ", dated "); at >= 0 {
				dates := strings.SplitN(rest[at+len(", dated "):], " … ", 2)
				if len(dates) == 2 {
					first, last := strings.TrimSpace(dates[0]), strings.TrimSpace(dates[1])
					_, ferr := time.Parse("2006-01-02", first)
					_, lerr := time.Parse("2006-01-02", last)
					if ferr == nil && lerr == nil {
						parsed.firstDate, parsed.lastDate = first, last
					}
				}
			}
		}
	}
	if parsed.clock.IsZero() {
		t.Fatalf("demoseed -plan output is missing its clock line, the one figure the world's clock assertions hang off:\n%s", planOut)
	}

	// The apply is pinned to the plan's own clock: each invocation reads the wall clock
	// once, and the world's clock assertions hang off the plan's printed clock, so an
	// unpinned apply that starts in the next wall second stamps the day's counter sale
	// rows past the clock the assertions read. The -now pin is the command's own
	// determinism mechanism, and it holds every row to the clock the plan printed.
	applyOut := runRole("-apply", "-now", parsed.clock.Format(time.RFC3339))
	var proposed, created, skipped int
	for _, line := range strings.Split(applyOut, "\n") {
		if n, err := fmt.Sscanf(line, "applied: %d proposed · %d created · %d already present",
			&proposed, &created, &skipped); err == nil && n == 3 {
			break
		}
	}
	if created == 0 {
		t.Fatalf("demoseed -apply created nothing on a fresh database, the world did not apply:\n%s", applyOut)
	}
	if created+skipped != proposed {
		t.Fatalf("demoseed -apply lost rows: %d proposed, %d created, %d already present:\n%s",
			proposed, created, skipped, applyOut)
	}
	t.Logf("demoseed -apply: %d proposed, %d created, %d already present", proposed, created, skipped)
	return parsed
}

// demoWorldRows plants the identity W0's story needs, the same steps the demo populate will take
// in the same order: the org anchor, the org scope stamp, then the two counter staff rows with
// their Trenton branch grants and their staff role grants.
func demoWorldRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (orgID string) {
	t.Helper()
	orgID, inserted, err := identity.ProvisionOrgAnchor(ctx, pool, demoOrgSlug, "Hardscape House (demo)")
	if err != nil {
		t.Fatalf("provision the org anchor: %v", err)
	}
	if !inserted {
		t.Fatal("the hh-demo org anchor already existed on a fresh database")
	}
	if _, err := identity.StampOrgScope(ctx, pool, orgID); err != nil {
		t.Fatalf("stamp the org scope: %v", err)
	}

	staff := func(email, name, subject, role string) {
		t.Helper()
		var userID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, display_name, status, org_id, platform_user_id)
			VALUES ($1, $2, 'ACTIVE', $3, $4)
			RETURNING id::text`, email, name, orgID, subject).Scan(&userID); err != nil {
			t.Fatalf("insert staff row for %s: %v", role, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_branch_grants (user_id, branch_id, org_id)
			VALUES ($1, $2, $3)`, userID, trentonBranchID, orgID); err != nil {
			t.Fatalf("insert branch grant for %s: %v", role, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
			VALUES ($1, $2, $3, $4)`, subject, role, orgID, trentonBranchID); err != nil {
			t.Fatalf("insert role grant for %s: %v", role, err)
		}
	}
	staff("sales.demo@hh-demo.invalid", "Sales (demo)", salesSubject, "inside_sales")
	staff("salesmanager.demo@hh-demo.invalid", "Sales manager (demo)", salesMgrSubject, "sales_manager")
	return orgID
}

// demoPaymentConfig plants the one active payment configuration the card rail needs before even
// the simulator will charge: an invented merchant id, an invented credential that is not a key,
// sealed under the test's own vault key, no host.
func demoPaymentConfig(t *testing.T, ctx context.Context, pool *pgxpool.Pool, vaultKeyHex string) {
	t.Helper()
	vault, err := payment.NewVault(vaultKeyHex)
	if err != nil {
		t.Fatalf("payment vault: %v", err)
	}
	plain, err := json.Marshal(map[string]string{"api_key": demoInvCred})
	if err != nil {
		t.Fatalf("seal creds: %v", err)
	}
	ct, kv, err := vault.Encrypt(plain)
	if err != nil {
		t.Fatalf("seal creds: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_config (processor, merchant_id, currency, credentials_ciphertext,
			key_version, base_url, mdr_bps, surcharge_enabled, status)
		VALUES ('run_payments', $1, 'CAD', $2, $3, '', 0, FALSE, 'active')`,
		demoMerchantID, ct, kv); err != nil {
		t.Fatalf("insert payment config: %v", err)
	}
}

// demoResponse is one route call's answer.
type demoResponse struct {
	status int
	body   []byte
}

// ok fails the test unless the call answered with one of the wanted statuses.
func (r demoResponse) ok(t *testing.T, want ...int) {
	t.Helper()
	for _, s := range want {
		if r.status == s {
			return
		}
	}
	t.Fatalf("route answered %d, want %v (body: %s)", r.status, want, string(r.body))
}

// decode decodes the response body into out, failing the test on a decode error.
func (r demoResponse) decode(t *testing.T, out any) {
	t.Helper()
	if err := json.NewDecoder(bytes.NewReader(r.body)).Decode(out); err != nil {
		t.Fatalf("decoding response %d: %v (body: %s)", r.status, err, string(r.body))
	}
}

// tillClient drives the registered surface as one demo login.
type tillClient struct {
	t     *testing.T
	mux   *http.ServeMux
	token string
	who   string
}

// call issues one request through the registered surface with this client's credential and the
// hh-demo org selection header.
func (c *tillClient) call(method, path string, body any) demoResponse {
	c.t.Helper()
	reader := bytes.NewReader(nil)
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal body for %s %s: %v", method, path, err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Org", demoOrgSlug)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rsp := httptest.NewRecorder()
	c.mux.ServeHTTP(rsp, req)
	return demoResponse{status: rsp.Code, body: rsp.Body.Bytes()}
}

// callWithApprover issues one request carrying a second credential in its own
// header beside the clerk's bearer (the manager's walk over, D49): the same
// request, the approver's token riding X-Approver-Authorization.
func (c *tillClient) callWithApprover(method, path string, body any, approverToken string) demoResponse {
	c.t.Helper()
	reader := bytes.NewReader(nil)
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal body for %s %s: %v", method, path, err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Approver-Authorization", "Bearer "+approverToken)
	req.Header.Set("X-Org", demoOrgSlug)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rsp := httptest.NewRecorder()
	c.mux.ServeHTTP(rsp, req)
	return demoResponse{status: rsp.Code, body: rsp.Body.Bytes()}
}

// demoSaleLine and demoSaleBody mirror the ring body's fields this test sends. The cost stays out
// of the body (a zero cost_cents, exactly what the ring path sends today).
type demoSaleLine struct {
	ProductID  string  `json:"product_id"`
	QtyEntered float64 `json:"qty_entered"`
	UOMEntered string  `json:"uom_entered"`
	CostCents  int64   `json:"cost_cents"`
}

type demoSaleBody struct {
	IdempotencyKey    string         `json:"idempotency_key"`
	BranchID          string         `json:"branch_id"`
	TillSessionID     string         `json:"till_session_id,omitempty"`
	CustomerID        string         `json:"customer_id,omitempty"`
	SaleType          string         `json:"sale_type"`
	CashSale          bool           `json:"cash_sale"`
	Tender            string         `json:"tender"`
	PaymentToken      string         `json:"payment_token,omitempty"`
	CashTenderedCents int64          `json:"cash_tendered_cents,omitempty"`
	Lines             []demoSaleLine `json:"lines"`
}

// saleRing mirrors the POST /api/v1/pos/sales answer's fields this test reads.
type saleRing struct {
	OrderID           string `json:"order_id"`
	OrderNo           string `json:"order_no"`
	InvoiceID         string `json:"invoice_id"`
	GatewayRef        string `json:"gateway_ref"`
	Tender            string `json:"tender"`
	SubtotalCents     int64  `json:"subtotal_cents"`
	TaxCents          int64  `json:"tax_cents"`
	TotalCents        int64  `json:"total_cents"`
	CashTenderedCents int64  `json:"cash_tendered_cents"`
	ChangeDueCents    int64  `json:"change_due_cents"`
}

// saleViewLine and saleView mirror GET /api/v1/pos/sales/{id}.
type saleViewLine struct {
	LineNo         int     `json:"line_no"`
	ProductID      string  `json:"product_id"`
	QtyEntered     float64 `json:"qty_entered"`
	UOMEntered     string  `json:"uom_entered"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	PriceUOM       string  `json:"price_uom"`
	LineTotalCents int64   `json:"line_total_cents"`
}

type saleView struct {
	OrderID       string         `json:"order_id"`
	OrderNo       string         `json:"order_no"`
	SubtotalCents int64          `json:"subtotal_cents"`
	TaxCents      int64          `json:"tax_cents"`
	TotalCents    int64          `json:"total_cents"`
	Lines         []saleViewLine `json:"lines"`
}

// counterPriceRow and counterPrices mirror POST /api/v1/pos/counter-prices, the engine's own
// price picture, the same waterfall the ring prices through.
type counterPriceRow struct {
	ProductID     string `json:"product_id"`
	PriceUOM      string `json:"price_uom"`
	RetailCents   int64  `json:"retail_cents"`
	CustomerCents int64  `json:"customer_cents"`
	Unpriceable   bool   `json:"unpriceable"`
}

type counterPrices struct {
	Prices []counterPriceRow `json:"prices"`
}

// returnCommit mirrors the POST /api/v1/pos/returns answer.
type returnCommit struct {
	ReturnID        string `json:"return_id"`
	TotalCents      int64  `json:"total_cents"`
	RefundPaymentID string `json:"refund_payment_id"`
	Status          string `json:"status"`
}

// returnLookup mirrors GET /api/v1/pos/returns/lookup.
type returnLookup struct {
	OrderID        string `json:"order_id"`
	OrderNo        string `json:"order_no"`
	BranchID       string `json:"branch_id"`
	CardRefundable bool   `json:"card_refundable"`
	Lines          []struct {
		OrderLineID string  `json:"order_line_id"`
		ProductID   string  `json:"product_id"`
		QtySold     float64 `json:"qty_sold"`
	} `json:"lines"`
}

// saleSummaryItem mirrors one row of GET /api/v1/pos/sales.
type saleSummaryItem struct {
	OrderID    string `json:"order_id"`
	OrderNo    string `json:"order_no"`
	Tender     string `json:"tender"`
	BranchID   string `json:"branch_id"`
	TotalCents int64  `json:"total_cents"`
}

// zreportView mirrors the cash block of GET /api/v1/till/sessions/{id}/zreport.
type zreportView struct {
	Cash struct {
		OpeningFloatCents int64 `json:"opening_float_cents"`
		ExpectedCents     int64 `json:"expected_cents"`
	} `json:"cash"`
}

// demoHST is the engine's own half-up-once header arithmetic, applied at the engine's resolved
// rate rather than a constant. The half-cent sale below exists so this formula's rounding is
// actually discriminated: at a subtotal landing exactly on a half cent, half down and half even
// both answer one cent less and fail.
func demoHST(subtotalCents int64, rateBps int) int64 {
	return (subtotalCents*int64(rateBps) + 5000) / 10000
}

// engineRate asks the engine's own surface for the standard rate: GET /api/v1/tax/rates, the
// manager read over the same table the sale's assessment resolves, with the engine's own
// effective-dating rule applied (rows for the reason effective on the run's day, the latest
// effective_from wins). No effective row would mean the engine falls back to a built-in figure
// silently; the proof refuses to guess a fallback and fails instead, so a change in the engine's
// table or its wiring fails here rather than passing on a figure the test supplied itself.
func engineRate(t *testing.T, c *tillClient, day time.Time) int {
	t.Helper()
	rsp := c.call("GET", "/api/v1/tax/rates", nil)
	rsp.ok(t, http.StatusOK)
	var ratesView struct {
		Rates []struct {
			Jurisdiction  string `json:"jurisdiction"`
			ReasonCode    string `json:"reason_code"`
			RateBps       int    `json:"rate_bps"`
			EffectiveFrom string `json:"effective_from"`
			EffectiveTo   string `json:"effective_to"`
		} `json:"rates"`
	}
	rsp.decode(t, &ratesView)
	rates := ratesView.Rates
	dayStr := day.Format("2006-01-02")
	best, bestFrom := -1, ""
	for _, r := range rates {
		if r.Jurisdiction != "ON" || r.ReasonCode != "STD_ON_HST_13" {
			continue
		}
		if r.EffectiveFrom > dayStr {
			continue
		}
		if r.EffectiveTo != "" && r.EffectiveTo < dayStr {
			continue
		}
		if best == -1 || r.EffectiveFrom > bestFrom {
			best, bestFrom = r.RateBps, r.EffectiveFrom
		}
	}
	if best == -1 {
		t.Fatalf("the engine's rate table answers no standard rate for %s: the sale's assessment would fall back silently and the proof will not guess the fallback", dayStr)
	}
	return best
}

func TestDemoWorld_CounterTill(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	// The vault key is generated here and handed to the core through the process environment
	// alone: never printed, never filed, dead with the test run.
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	vaultKeyHex := hex.EncodeToString(rawKey)

	// 1. migrate, then the demo seed, exactly as a demo box runs them. Every later step points at
	// the created database's DSN, never at the gate DSN's own database.
	pool, testDSN := freshDemoDB(t, ctx, dsn)
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", vaultKeyHex)
	t.Setenv("APP_ENV", "dev") // Validate refuses an unset APP_ENV (HH-198)
	plan := demoseedRun(t, testDSN)
	t.Logf("demoseed -plan: clock %s, %d rows planned, dated %s .. %s",
		plan.clock.Format(time.RFC3339), plan.rows, plan.firstDate, plan.lastDate)

	// 2. the demo populate's identity steps, then the payment configuration.
	demoWorldRows(t, ctx, pool)
	demoPaymentConfig(t, ctx, pool, vaultKeyHex)

	// 3. the demo membership and tokens (all test support, never shipped), then the FULL served
	// surface through the real registerRoutes.
	appwrite := newAppwriteFake(t)
	appwrite.setTeam(salesSubject, "org-"+demoOrgSlug)
	appwrite.setTeam(salesMgrSubject, "org-"+demoOrgSlug)
	keys := testkit.NewKeys(t)
	acceptKey := make([]byte, 32)
	if _, err := rand.Read(acceptKey); err != nil {
		t.Fatalf("generate acceptance key: %v", err)
	}
	env := &compEnv{
		keys:     keys,
		minter:   &testkit.Minter{Keys: keys, Clock: testkit.NewClock()},
		keySrv:   testkit.NewKeyServer(t, keys),
		appwrite: appwrite,
		dsn:      testDSN,
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
	consoleSessionValues(t, env.minter, values)
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)

	sales := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, salesSubject), who: "Sales"}
	manager := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, salesMgrSubject), who: "Sales manager"}

	// 4. the demo facts the routes need: the Trenton branch row, the demo products, the demo
	// customers, and the engine's tax rate on the run's day.
	var trentonCode string
	if err := pool.QueryRow(ctx,
		`SELECT code::text FROM locations WHERE id = $1`, trentonBranchID).Scan(&trentonCode); err != nil {
		t.Fatalf("the Trenton branch row is missing: %v", err)
	}
	product := func(sku string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM products WHERE sku = $1`, sku).Scan(&id); err != nil {
			t.Fatalf("demo product %s is missing: %v", sku, err)
		}
		return id
	}
	holland := product(worldWord("DEMO-PAV-HOLLAND"))
	brussels := product(worldWord("DEMO-PAV-BRUSSELS"))
	wallBlock := product(worldWord("DEMO-WALL-BLOCK"))
	granularA := product(worldWord("DEMO-AGG-GA"))
	edging := product(worldWord("DEMO-EDGE-ALU"))
	customer := func(key string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM customers WHERE account_no = $1`, key).Scan(&id); err != nil {
			t.Fatalf("demo customer %s is missing: %v", key, err)
		}
		return id
	}
	c01, c02, c09 := customer("DEMO-C01"), customer("DEMO-C02"), customer("DEMO-C09")
	rateBps := engineRate(t, manager, plan.clock)
	t.Logf("the engine's standard rate on the run's day, from the engine's own rates read: %d bps", rateBps)

	// 5. the drawer: Sales opens the MAIN drawer at Trenton.
	drawerRsp := sales.call("POST", "/api/v1/till/sessions", map[string]any{
		"branch_id":   trentonBranchID,
		"station":     "MAIN",
		"float_cents": demoDrawerFloatCents,
	})
	drawerRsp.ok(t, http.StatusCreated)
	var drawer struct {
		ID string `json:"id"`
	}
	drawerRsp.decode(t, &drawer)
	if drawer.ID == "" {
		t.Fatal("the drawer open answered no session id")
	}
	t.Logf("the MAIN drawer at %s is open as Sales (%s)", trentonCode, drawer.ID)

	// 6. the margin glance as each role. The demo's default policy is the
	// pos_policy column default (MANAGER_UP): the configuration's list
	// considers Sales, but the policy holds the answer at the server, so
	// the Sales glance is the byte-absent {} and is asserted; the manager's
	// answer carries the ticket's lines.
	glanceBody := map[string]any{
		"branch_id": trentonBranchID,
		"lines": []map[string]any{
			{"product_id": holland, "qty_entered": 240, "uom_entered": "PC", "cost_cents": 0},
		},
	}
	salesGlance := sales.call("POST", "/api/v1/pos/margin-glance", glanceBody)
	salesGlance.ok(t, http.StatusOK)
	if body := strings.TrimSpace(string(salesGlance.body)); body != "{}" {
		t.Fatalf("the margin glance as Sales must answer the byte-absent {} under the demo's default policy, got: %s", body)
	}
	mgrGlance := manager.call("POST", "/api/v1/pos/margin-glance", glanceBody)
	mgrGlance.ok(t, http.StatusOK)
	var mgrView struct {
		Lines []json.RawMessage `json:"lines"`
	}
	mgrGlance.decode(t, &mgrView)
	if len(mgrView.Lines) == 0 {
		t.Fatalf("the margin glance as the Sales manager carries no line answers (body: %s)", string(mgrGlance.body))
	}

	// 7. the walk-in cash sale: three lines, cash tendered, change due.
	cashRingRsp := sales.call("POST", "/api/v1/pos/sales", demoSaleBody{
		IdempotencyKey:    uuid.NewString(),
		BranchID:          trentonBranchID,
		TillSessionID:     drawer.ID,
		SaleType:          "COLLECT_NOW",
		CashSale:          true,
		Tender:            "CASH",
		CashTenderedCents: demoCashTendered,
		Lines: []demoSaleLine{
			{ProductID: holland, QtyEntered: 2, UOMEntered: "PC"},
			{ProductID: granularA, QtyEntered: 2, UOMEntered: "T"},
			{ProductID: edging, QtyEntered: 3, UOMEntered: "EA"},
		},
	})
	cashRingRsp.ok(t, http.StatusCreated)
	var cashRing saleRing
	cashRingRsp.decode(t, &cashRing)
	if cashRing.TotalCents != cashRing.SubtotalCents+cashRing.TaxCents {
		t.Fatalf("the cash sale's total %d is not its subtotal %d plus its tax %d",
			cashRing.TotalCents, cashRing.SubtotalCents, cashRing.TaxCents)
	}
	if cashRing.TaxCents != demoHST(cashRing.SubtotalCents, rateBps) {
		t.Fatalf("the cash sale's tax %d is not its subtotal %d at the engine's %d bps, half up once (want %d)",
			cashRing.TaxCents, cashRing.SubtotalCents, rateBps, demoHST(cashRing.SubtotalCents, rateBps))
	}
	if cashRing.CashTenderedCents != demoCashTendered {
		t.Fatalf("the cash sale's answer echoes tendered %d, the ring sent %d",
			cashRing.CashTenderedCents, demoCashTendered)
	}
	if cashRing.ChangeDueCents != demoCashTendered-cashRing.TotalCents {
		t.Fatalf("the cash sale's change due %d is not the tendered %d less the total %d",
			cashRing.ChangeDueCents, demoCashTendered, cashRing.TotalCents)
	}
	t.Logf("the walk-in cash sale %s: subtotal %d, tax %d, total %d, tendered %d, change due %d",
		cashRing.OrderNo, cashRing.SubtotalCents, cashRing.TaxCents, cashRing.TotalCents,
		demoCashTendered, cashRing.ChangeDueCents)

	// 8. the card sale on the fixed demo token, above the refund amount.
	cardRingRsp := sales.call("POST", "/api/v1/pos/sales", demoSaleBody{
		IdempotencyKey: uuid.NewString(),
		BranchID:       trentonBranchID,
		TillSessionID:  drawer.ID,
		SaleType:       "COLLECT_NOW",
		Tender:         "CARD",
		PaymentToken:   demoCardToken,
		Lines: []demoSaleLine{
			{ProductID: brussels, QtyEntered: 60, UOMEntered: "PC"},
		},
	})
	cardRingRsp.ok(t, http.StatusCreated)
	var cardRing saleRing
	cardRingRsp.decode(t, &cardRing)
	if !strings.HasPrefix(cardRing.GatewayRef, "rp_sim_") {
		t.Fatalf("the card sale's reference %q does not begin rp_sim_", cardRing.GatewayRef)
	}
	if cardRing.TotalCents <= refundAmountCents {
		t.Fatalf("the card sale's total %d is not above the refund amount %d, the story's whole return needs it to be",
			cardRing.TotalCents, refundAmountCents)
	}
	t.Logf("the card sale %s: total %d, reference %s", cardRing.OrderNo, cardRing.TotalCents, cardRing.GatewayRef)

	// 9. the account sale for C02: the invoice finalizes with its balance outstanding.
	acctC02Rsp := sales.call("POST", "/api/v1/pos/sales", demoSaleBody{
		IdempotencyKey: uuid.NewString(),
		BranchID:       trentonBranchID,
		SaleType:       "COLLECT_NOW",
		CustomerID:     c02,
		Tender:         "ACCOUNT",
		Lines: []demoSaleLine{
			{ProductID: wallBlock, QtyEntered: 5, UOMEntered: "PC"},
		},
	})
	acctC02Rsp.ok(t, http.StatusCreated)
	var acctC02 saleRing
	acctC02Rsp.decode(t, &acctC02)
	var invStatus string
	var invBalance, invTotal int64
	if err := pool.QueryRow(ctx, `
		SELECT status::text, balance_cents, total_cents FROM invoices WHERE id = $1`,
		acctC02.InvoiceID).Scan(&invStatus, &invBalance, &invTotal); err != nil {
		t.Fatalf("C02's invoice row is missing: %v", err)
	}
	if invStatus != "FINALIZED" {
		t.Fatalf("C02's invoice is %s, want FINALIZED", invStatus)
	}
	if invTotal != acctC02.TotalCents {
		t.Fatalf("C02's invoice totals %d, the ring answered %d, the invoice is not the sale's",
			invTotal, acctC02.TotalCents)
	}
	if invBalance != acctC02.TotalCents {
		t.Fatalf("C02's invoice balance %d is not the sale's total %d outstanding",
			invBalance, acctC02.TotalCents)
	}
	t.Logf("the account sale for C02 %s: invoice FINALIZED, balance %d", acctC02.OrderNo, invBalance)

	// 10. the account sale for C09: refused for its hold.
	c09Rsp := sales.call("POST", "/api/v1/pos/sales", demoSaleBody{
		IdempotencyKey: uuid.NewString(),
		BranchID:       trentonBranchID,
		SaleType:       "COLLECT_NOW",
		CustomerID:     c09,
		Tender:         "ACCOUNT",
		Lines: []demoSaleLine{
			{ProductID: holland, QtyEntered: 1, UOMEntered: "PC"},
		},
	})
	if c09Rsp.status != http.StatusConflict {
		t.Fatalf("the account sale for C09 answered %d, want 409 for its hold (body: %s)",
			c09Rsp.status, string(c09Rsp.body))
	}
	if !strings.Contains(string(c09Rsp.body), "credit_blocked") {
		t.Fatalf("the account sale for C09 answered 409 but its body does not name the credit block: %s", string(c09Rsp.body))
	}
	t.Logf("the account sale for C09 is refused for its hold: %s", strings.TrimSpace(string(c09Rsp.body)))

	// 11. C01's Holland pavers with no cost in the body. The line price is a recorded item (the
	// rule price is expected today, a missing cost reading as 0; RT-A and the pricing fix change
	// this), so it is logged and not asserted.
	c01Rsp := sales.call("POST", "/api/v1/pos/sales", demoSaleBody{
		IdempotencyKey: uuid.NewString(),
		BranchID:       trentonBranchID,
		SaleType:       "COLLECT_NOW",
		CustomerID:     c01,
		Tender:         "ACCOUNT",
		Lines: []demoSaleLine{
			{ProductID: holland, QtyEntered: 1, UOMEntered: "PC"},
		},
	})
	c01Rsp.ok(t, http.StatusCreated)
	var acctC01 saleRing
	c01Rsp.decode(t, &acctC01)
	c01View := getSaleView(t, sales, acctC01.OrderID)
	t.Logf("RECORDED (RT-A and the pricing fix change it): C01's Holland paver line rang at %d cents per PC with no cost in the body (subtotal %d)",
		c01View.Lines[0].UnitPriceCents, c01View.SubtotalCents)

	// 11b. The rounding proof: one sale whose subtotal lands exactly on a half cent at the
	// engine's rate, so half up passes and half down and half even both fail. C01's wall block
	// rule price is exact in every rounding mode (the list 1250 less its 1800 bps, 225, with no
	// remainder), so two units price 2050, and 2050 at 1300 bps is exactly 266.5: half up
	// answers 267, half down and half even answer 266.
	halfRingRsp := sales.call("POST", "/api/v1/pos/sales", demoSaleBody{
		IdempotencyKey: uuid.NewString(),
		BranchID:       trentonBranchID,
		SaleType:       "COLLECT_NOW",
		CustomerID:     c01,
		Tender:         "ACCOUNT",
		Lines: []demoSaleLine{
			{ProductID: wallBlock, QtyEntered: 2, UOMEntered: "PC"},
		},
	})
	halfRingRsp.ok(t, http.StatusCreated)
	var halfRing saleRing
	halfRingRsp.decode(t, &halfRing)
	if halfRing.SubtotalCents != 2050 {
		t.Fatalf("the half-cent sale's subtotal is %d, want the rule's exact 2050, the figure the rounding assertion hangs off", halfRing.SubtotalCents)
	}
	if remainder := halfRing.SubtotalCents * int64(rateBps) % 10000; remainder != 5000 {
		t.Fatalf("the half-cent sale's subtotal no longer lands on a half cent at the engine's %d bps (remainder %d), the rounding assertion would not bite", rateBps, remainder)
	}
	if formula := demoHST(halfRing.SubtotalCents, rateBps); halfRing.TaxCents != 267 || halfRing.TaxCents != formula {
		t.Fatalf("the half-cent sale's rounding: the sale answered %d cents of tax, the half-up formula answers %d and the committed answer is 267; 2050 at the engine's %d bps is exactly 266.5, so half down or half even would answer 266",
			halfRing.TaxCents, formula, rateBps)
	}
	t.Logf("the half-cent sale %s: subtotal 2050 at %d bps lands on exactly 266.5, the tax is 267, half up once",
		halfRing.OrderNo, rateBps)

	// 12. a parked ticket, parked by Sales, resumed by the Sales manager.
	parkRsp := sales.call("POST", "/api/v1/pos/parked", map[string]any{
		"branch_id": trentonBranchID,
		"sale_type": "COLLECT_NOW",
		"lines": []map[string]any{
			{"product_id": wallBlock, "qty_entered": 2, "uom_entered": "PC"},
		},
	})
	parkRsp.ok(t, http.StatusCreated)
	var parked struct {
		ID       string `json:"id"`
		TicketNo string `json:"ticket_no"`
	}
	parkRsp.decode(t, &parked)
	resumeRsp := manager.call("POST", "/api/v1/pos/parked/"+parked.ID+"/resume", nil)
	resumeRsp.ok(t, http.StatusOK)
	t.Logf("the ticket %s parked by Sales was resumed by the Sales manager", parked.TicketNo)

	// 13. a seeded sale above 250.00 at Trenton, found through the counter's own search. Which
	// seeded sale it is today is a recorded item (the seed orders the branches by code), so the
	// candidates are logged and only the find itself is asserted.
	listRsp := sales.call("GET",
		"/api/v1/pos/sales?branch_id="+trentonBranchID+"&q=DEMO&limit=100", nil)
	listRsp.ok(t, http.StatusOK)
	var list struct {
		Items []saleSummaryItem `json:"items"`
	}
	listRsp.decode(t, &list)
	var above []saleSummaryItem
	for _, it := range list.Items {
		if strings.HasPrefix(it.OrderNo, "DEMO-") && it.TotalCents > refundAmountCents {
			above = append(above, it)
		}
	}
	if len(above) == 0 {
		t.Fatalf("no seeded sale above %d sits at Trenton; the search returned %d demo sales",
			refundAmountCents, len(list.Items))
	}
	for _, it := range above {
		t.Logf("RECORDED (the seed orders the branches by code): the seeded sale %s at Trenton totals %d cents (%s)",
			it.OrderNo, it.TotalCents, it.Tender)
	}
	seeded := above[0]
	seededView := getSaleView(t, sales, seeded.OrderID)
	var seededDBTotal int64
	var seededMarker string
	if err := pool.QueryRow(ctx, `
		SELECT total_cents, COALESCE(metadata->>'demo_seed','') FROM orders WHERE id = $1`,
		seeded.OrderID).Scan(&seededDBTotal, &seededMarker); err != nil {
		t.Fatalf("the found seeded sale's order row is missing: %v", err)
	}
	if seededMarker != demoSeedMarker {
		t.Fatalf("the found sale %s is not the demo seed's (marker %q)", seeded.OrderNo, seededMarker)
	}
	if seededView.TotalCents != seededDBTotal {
		t.Fatalf("the seeded sale's view total %d does not equal the order row's %d",
			seededView.TotalCents, seededDBTotal)
	}

	// 14. one line of the cash sale returned in cash, within the refund amount.
	cashLookupRsp := sales.call("GET", "/api/v1/pos/returns/lookup?order_id="+cashRing.OrderID, nil)
	cashLookupRsp.ok(t, http.StatusOK)
	var cashLookup returnLookup
	cashLookupRsp.decode(t, &cashLookup)
	var edgingLineID string
	for _, ln := range cashLookup.Lines {
		if ln.ProductID == edging {
			edgingLineID = ln.OrderLineID
		}
	}
	if edgingLineID == "" {
		t.Fatalf("the cash sale's return lookup carries no edging line (%d lines)", len(cashLookup.Lines))
	}
	cashRetRsp := sales.call("POST", "/api/v1/pos/returns", map[string]any{
		"idempotency_key": uuid.NewString(),
		"order_id":        cashRing.OrderID,
		"refund_method":   "CASH",
		"till_session_id": drawer.ID,
		"restock":         true,
		"reason":          "demo world proof, one line back over the counter",
		"lines": []map[string]any{
			{"order_line_id": edgingLineID, "qty_base": 3, "reason_code": "DAMAGED"},
		},
	})
	cashRetRsp.ok(t, http.StatusCreated)
	var cashRet returnCommit
	cashRetRsp.decode(t, &cashRet)
	if cashRet.TotalCents >= refundAmountCents {
		t.Fatalf("the cash return totals %d, the story needs it within the refund amount %d",
			cashRet.TotalCents, refundAmountCents)
	}
	t.Logf("the cash return %s on the cash sale: total %d, within the refund amount", cashRet.ReturnID, cashRet.TotalCents)

	// 15. the card sale returned whole, above the refund amount. That it goes through as Sales
	// today is a recorded item (RT-B changes it); the route step itself must complete for the
	// card refund's reference to exist at all.
	cardLookupRsp := sales.call("GET", "/api/v1/pos/returns/lookup?order_id="+cardRing.OrderID, nil)
	cardLookupRsp.ok(t, http.StatusOK)
	var cardLookup returnLookup
	cardLookupRsp.decode(t, &cardLookup)
	if !cardLookup.CardRefundable {
		t.Fatalf("the card sale's return lookup does not name it card refundable (body: %s)", string(cardLookupRsp.body))
	}
	cardLines := make([]map[string]any, 0, len(cardLookup.Lines))
	for _, ln := range cardLookup.Lines {
		cardLines = append(cardLines, map[string]any{
			"order_line_id": ln.OrderLineID, "qty_base": ln.QtySold, "reason_code": "DAMAGED",
		})
	}
	cardRetBody := map[string]any{
		"idempotency_key": uuid.NewString(),
		"order_id":        cardRing.OrderID,
		"refund_method":   "CARD",
		"restock":         true,
		"reason":          "demo world proof, the whole card sale back",
		"lines":           cardLines,
	}
	// Under the landed refund rule (RT-B) the clerk's own return above the
	// amount refuses with the approval body naming the configured amount and
	// approvers, and the manager's credential in its own header completes it
	// as the manager's decision with the clerk named (D49).
	refusedRsp := sales.call("POST", "/api/v1/pos/returns", cardRetBody)
	refusedRsp.ok(t, http.StatusConflict)
	if !strings.Contains(string(refusedRsp.body), "refund_approval_required") ||
		!strings.Contains(string(refusedRsp.body), "sales_manager") {
		t.Fatalf("the above amount card return must refuse with the approval body, got: %s", string(refusedRsp.body))
	}
	t.Logf("the above amount card return refuses as Sales with the approval body: %s", string(refusedRsp.body))
	cardRetRsp := sales.callWithApprover("POST", "/api/v1/pos/returns", cardRetBody, manager.token)
	cardRetRsp.ok(t, http.StatusCreated)
	var cardRet returnCommit
	cardRetRsp.decode(t, &cardRet)
	if cardRet.TotalCents <= refundAmountCents {
		t.Fatalf("the card return totals %d, the story needs it above the refund amount %d",
			cardRet.TotalCents, refundAmountCents)
	}
	t.Logf("the whole card return, %d cents, above 250.00, refused as Sales and completed by the Sales manager's credential", cardRet.TotalCents)
	var cardRefundRef string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(gateway_ref::text,'') FROM payments WHERE id = $1`,
		cardRet.RefundPaymentID).Scan(&cardRefundRef); err != nil {
		t.Fatalf("the card refund's payments row is missing: %v", err)
	}
	if !strings.HasPrefix(cardRefundRef, "rp_ref_") {
		t.Fatalf("the card refund's reference %q does not begin rp_ref_", cardRefundRef)
	}
	t.Logf("the card refund's reference: %s", cardRefundRef)

	// 16. the payments status read. The simulated flag is a recorded item (expected false today,
	// PX changes it), so it is logged and not asserted.
	cfgRsp := sales.call("GET", "/api/v1/payments/config", nil)
	cfgRsp.ok(t, http.StatusOK)
	var cfg struct {
		Configured bool   `json:"configured"`
		MerchantID string `json:"merchant_id"`
		Processor  string `json:"processor"`
		Currency   string `json:"currency"`
		Simulated  bool   `json:"simulated"`
	}
	cfgRsp.decode(t, &cfg)
	if !cfg.Configured {
		t.Fatal("the payments status answers unconfigured, the demo payment configuration is not active")
	}
	if cfg.MerchantID != demoMerchantID {
		t.Fatalf("the payments status names merchant %q, want the demo configuration's %q", cfg.MerchantID, demoMerchantID)
	}
	if cfg.Processor != "run_payments" || cfg.Currency != "CAD" {
		t.Fatalf("the payments status names processor %q currency %q, want run_payments and CAD", cfg.Processor, cfg.Currency)
	}
	t.Logf("RECORDED (PX changes it): the status route answers simulated: %v", cfg.Simulated)

	// 17. every sale's lines priced by the engine and equal to the sale view.
	for _, tc := range []struct {
		who        string
		customerID string
		ring       saleRing
	}{
		{"the walk-in cash sale", "", cashRing},
		{"the card sale", "", cardRing},
		{"the account sale for C02", c02, acctC02},
		{"C01's paver sale", c01, acctC01},
		{"C01's half-cent sale", c01, halfRing},
	} {
		view := getSaleView(t, sales, tc.ring.OrderID)
		if view.SubtotalCents != tc.ring.SubtotalCents || view.TaxCents != tc.ring.TaxCents || view.TotalCents != tc.ring.TotalCents {
			t.Fatalf("%s: the sale view (%d/%d/%d) does not equal the ring answer (%d/%d/%d)",
				tc.who, view.SubtotalCents, view.TaxCents, view.TotalCents,
				tc.ring.SubtotalCents, tc.ring.TaxCents, tc.ring.TotalCents)
		}
		if view.TaxCents != demoHST(view.SubtotalCents, rateBps) {
			t.Fatalf("%s: the view's tax %d is not its subtotal %d at the engine's %d bps, half up once",
				tc.who, view.TaxCents, view.SubtotalCents, rateBps)
		}
		engine := engineUnitPrices(t, sales, tc.customerID, view)
		var lineSum int64
		for _, ln := range view.Lines {
			want, ok := engine[ln.ProductID]
			if !ok {
				t.Fatalf("%s: the engine's price picture carries no answer for line %d (%s)", tc.who, ln.LineNo, ln.ProductID)
			}
			if ln.UnitPriceCents != want {
				t.Fatalf("%s: line %d rang at %d cents per %s, the engine answers %d",
					tc.who, ln.LineNo, ln.UnitPriceCents, ln.PriceUOM, want)
			}
			if ln.LineTotalCents != int64(ln.QtyEntered)*ln.UnitPriceCents {
				t.Fatalf("%s: line %d totals %d, not its qty %v times its unit price %d",
					tc.who, ln.LineNo, ln.LineTotalCents, ln.QtyEntered, ln.UnitPriceCents)
			}
			lineSum += ln.LineTotalCents
		}
		if lineSum != view.SubtotalCents {
			t.Fatalf("%s: the lines sum to %d, the view's subtotal is %d", tc.who, lineSum, view.SubtotalCents)
		}
	}

	// 18. the world in the database: one org; the newest seeded counter sale on the run's day;
	// the world's own rows for the rest (the lead's W0 ruling: the test asserts the world, not
	// the report).
	var orgCount, demoOrgCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE slug = $1) FROM org`, demoOrgSlug).
		Scan(&orgCount, &demoOrgCount); err != nil {
		t.Fatalf("counting org rows: %v", err)
	}
	if orgCount != 1 || demoOrgCount != 1 {
		t.Fatalf("the database holds %d org rows, %d of them hh-demo, want exactly one, hh-demo", orgCount, demoOrgCount)
	}

	// The run's day here is the seed clock's own UTC day: the seed reads its one clock in UTC
	// and stamps every row from it, so the world's day is that clock's UTC day. The house rule
	// that a daily roll means the dealer's own day governs the product's daily windows; this is
	// the proof of the seed's clock, and the seed's clock is UTC.
	var newestSeeded time.Time
	if err := pool.QueryRow(ctx, `
		SELECT max(ps.created_at) FROM pos_sale ps
		  JOIN orders o ON o.id = ps.order_id
		 WHERE o.metadata->>'demo_seed' = $1`, demoSeedMarker).Scan(&newestSeeded); err != nil {
		t.Fatalf("reading the newest seeded counter sale: %v", err)
	}
	if newestSeeded.UTC().Format("2006-01-02") != plan.clock.UTC().Format("2006-01-02") {
		t.Fatalf("the newest seeded counter sale is dated %s, the run's clock is %s, the world is not dated to the run",
			newestSeeded.UTC().Format(time.RFC3339), plan.clock.Format(time.RFC3339))
	}

	// What the world guarantees, asserted over the seven marker-stamped tables (customers,
	// products, orders, order_lines, invoices, payments, stock_moves): no row among them is
	// stamped after the run's clock, at the precision the report prints. That the history
	// REACHES the clock rests on the newest seeded counter sale check above, and the delivery
	// tables stand outside this population on purpose: the planned runs and stops are the
	// world's forward calendar, recorded below, never treated as its history. The earliest
	// written row here is the earliest dated fact this test reads over the seeded tables,
	// recorded rather than pinned to a figure.
	var worldOldest, worldNewest time.Time
	if err := pool.QueryRow(ctx, `
		SELECT min(d), max(d) FROM (
			       SELECT created_at AS d FROM customers    WHERE metadata->>'demo_seed' = $1
			UNION ALL SELECT created_at      FROM products     WHERE metadata->>'demo_seed' = $1
			UNION ALL SELECT created_at      FROM orders       WHERE metadata->>'demo_seed' = $1
			UNION ALL SELECT created_at      FROM order_lines  WHERE metadata->>'demo_seed' = $1
			UNION ALL SELECT created_at      FROM invoices     WHERE metadata->>'demo_seed' = $1
			UNION ALL SELECT created_at      FROM payments     WHERE metadata->>'demo_seed' = $1
			UNION ALL SELECT created_at      FROM stock_moves  WHERE metadata->>'demo_seed' = $1
		) t`, demoSeedMarker).Scan(&worldOldest, &worldNewest); err != nil {
		t.Fatalf("reading the world's written span: %v", err)
	}
	if worldNewest.Truncate(time.Second).After(plan.clock) {
		t.Fatalf("the world's newest written row is %s, after the run's clock %s: the written history does not end at the clock",
			worldNewest.UTC().Format(time.RFC3339), plan.clock.Format(time.RFC3339))
	}
	t.Logf("RECORDED: the world's earliest written row is dated %s, the earliest dated fact the test reads over the seeded tables",
		worldOldest.UTC().Format("2006-01-02"))

	// Dated facts that reach past the clock are the world's forward calendar, not its end: a
	// seeded account invoice's due date (the customer's terms added), the furthest scheduled
	// delivery, and the planned runs' and stops' own service-day row stamps. Recorded, never
	// treated as the world's span.
	var furthestDue, furthestScheduled, furthestPlannedStamp time.Time
	if err := pool.QueryRow(ctx, `
		SELECT max(due_date) FROM invoices WHERE metadata->>'demo_seed' = $1`, demoSeedMarker).
		Scan(&furthestDue); err != nil {
		t.Fatalf("reading the furthest seeded invoice due date: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT max(d) FROM (
			       SELECT scheduled_date AS d FROM delivery_runs
			UNION ALL SELECT scheduled_date      FROM delivery_stops
		) t`).Scan(&furthestScheduled); err != nil {
		t.Fatalf("reading the furthest scheduled delivery: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT max(d) FROM (
			       SELECT created_at AS d FROM delivery_runs
			UNION ALL SELECT created_at      FROM delivery_stops
		) t`).Scan(&furthestPlannedStamp); err != nil {
		t.Fatalf("reading the planned deliveries' row stamps: %v", err)
	}
	t.Logf("RECORDED: dated facts past the clock: the furthest seeded invoice due date %s, the furthest scheduled delivery %s, the planned deliveries' own row stamps to %s",
		furthestDue.UTC().Format("2006-01-02"), furthestScheduled.UTC().Format("2006-01-02"), furthestPlannedStamp.UTC().Format("2006-01-02"))

	// demoseed -plan's two figures, recorded with what each figure actually is (the lead's W0
	// ruling: the report's wording overstates its span, fixing the report is outside this
	// cycle's path, and the finding goes to the goal report).
	var oldestInvoiceIssue time.Time
	if err := pool.QueryRow(ctx, `
		SELECT min(created_at) FROM invoices WHERE metadata->>'demo_seed' = $1`, demoSeedMarker).
		Scan(&oldestInvoiceIssue); err != nil {
		t.Fatalf("reading the oldest seeded invoice: %v", err)
	}
	if plan.firstDate == "" || plan.lastDate == "" {
		t.Logf("RECORDED: demoseed -plan's dated range did not parse, its two figures go unrecorded (the report may have changed shape)")
	} else {
		t.Logf("RECORDED: demoseed -plan's first figure %s is the oldest seeded invoice issue date (the database answers %s), its last figure %s is the furthest scheduled delivery (the database answers %s)",
			plan.firstDate, oldestInvoiceIssue.UTC().Format("2006-01-02"), plan.lastDate, furthestScheduled.UTC().Format("2006-01-02"))
	}

	// 19. the drawer's expected cash: its float plus the cash sale less the cash refund.
	zRsp := sales.call("GET", "/api/v1/till/sessions/"+drawer.ID+"/zreport", nil)
	zRsp.ok(t, http.StatusOK)
	var z zreportView
	zRsp.decode(t, &z)
	wantCash := int64(demoDrawerFloatCents) + cashRing.TotalCents - cashRet.TotalCents
	if z.Cash.OpeningFloatCents != demoDrawerFloatCents {
		t.Fatalf("the Z report's opening float is %d, want the drawer's %d", z.Cash.OpeningFloatCents, demoDrawerFloatCents)
	}
	if z.Cash.ExpectedCents != wantCash {
		t.Fatalf("the drawer's expected cash is %d, want its float %d plus the cash sale %d less the cash refund %d = %d",
			z.Cash.ExpectedCents, demoDrawerFloatCents, cashRing.TotalCents, cashRet.TotalCents, wantCash)
	}
	t.Logf("the drawer's expected cash: %d (float %d + cash sale %d - cash refund %d)",
		z.Cash.ExpectedCents, demoDrawerFloatCents, cashRing.TotalCents, cashRet.TotalCents)
}

// getSaleView reads one sale through GET /api/v1/pos/sales/{id} and fails the test when the read
// does not answer.
func getSaleView(t *testing.T, c *tillClient, orderID string) saleView {
	t.Helper()
	rsp := c.call("GET", "/api/v1/pos/sales/"+orderID, nil)
	rsp.ok(t, http.StatusOK)
	var v saleView
	rsp.decode(t, &v)
	if len(v.Lines) == 0 {
		t.Fatalf("the sale view for %s carries no lines (body: %s)", orderID, string(rsp.body))
	}
	return v
}

// engineUnitPrices asks the engine's counter price picture for one view's lines, at the sale's own
// customer and quantities, and returns product id to unit price in cents. An empty customer id is
// the walk-in case, retail.
func engineUnitPrices(t *testing.T, c *tillClient, customerID string, view saleView) map[string]int64 {
	t.Helper()
	items := make([]map[string]any, 0, len(view.Lines))
	for _, ln := range view.Lines {
		items = append(items, map[string]any{
			"product_id": ln.ProductID, "qty_entered": ln.QtyEntered, "uom_entered": ln.UOMEntered,
		})
	}
	body := map[string]any{"branch_id": trentonBranchID, "items": items}
	if customerID != "" {
		body["customer_id"] = customerID
	}
	rsp := c.call("POST", "/api/v1/pos/counter-prices", body)
	rsp.ok(t, http.StatusOK)
	var prices counterPrices
	rsp.decode(t, &prices)
	out := make(map[string]int64, len(prices.Prices))
	for _, p := range prices.Prices {
		if p.Unpriceable {
			continue
		}
		out[p.ProductID] = p.CustomerCents
	}
	return out
}

// TestDemoWorldHarness_RefusesNonLocalAddresses exercises the harness's local-only refusal
// itself, on the forms a URL-text guard accepted before: the keyword form, the host= parameter
// override, an empty host with PGHOST naming a remote server, and a plain remote URL, beside the
// accepted local forms, each accepted form's database-name swap judged by parsing what the swap
// produced.
func TestDemoWorldHarness_RefusesNonLocalAddresses(t *testing.T) {
	refused := []struct{ name, dsn string }{
		{"plain remote URL", "postgres://hardscape:hardscape@db.remote.example:5432/hardscapeos?sslmode=disable"},
		{"keyword form, remote host", "host=db.remote.example port=5432 user=hardscape password=hardscape dbname=hardscapeos sslmode=disable"},
		{"host parameter overrides a local URL host", "postgres://hardscape@localhost:5590/hardscapeos?host=db.remote.example&sslmode=disable"},
		{"fallback host remote", "postgres://hardscape@127.0.0.1:5590/hardscapeos?sslmode=disable&host=127.0.0.1,db.remote.example"},
		{"host name dressed as loopback, URL form", "postgres://hardscape:hardscape@127.db.remote.example:5432/hardscapeos?sslmode=disable"},
		{"host name dressed as loopback, keyword form", "host=127.db.remote.example port=5432 user=hardscape password=hardscape dbname=hardscapeos sslmode=disable"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkLocalDSN(tc.dsn); err == nil {
				t.Fatal("the address was judged local")
			}
		})
	}
	t.Run("empty host with PGHOST remote", func(t *testing.T) {
		t.Setenv("PGHOST", "db.remote.example")
		if err := checkLocalDSN("postgres:///hardscapeos?sslmode=disable"); err == nil {
			t.Fatal("the address was judged local while PGHOST names a remote server")
		}
	})

	accepted := []struct{ name, dsn string }{
		{"loopback URL", "postgres://hardscape:hardscape@127.0.0.1:5590/hardscapeos?sslmode=disable"},
		{"localhost URL", "postgres://hardscape:hardscape@localhost/hardscapeos?sslmode=disable"},
		{"IPv6 loopback URL", "postgres://hardscape:hardscape@[::1]:5590/hardscapeos?sslmode=disable"},
		{"keyword form, local host", "host=127.0.0.1 port=5590 user=hardscape password=hardscape dbname=hardscapeos sslmode=disable"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkLocalDSN(tc.dsn); err != nil {
				t.Fatalf("the local address was refused: %v", err)
			}
			swapped := swapDSNDatabase(tc.dsn, "demoworld_till")
			cfg, err := pgconn.ParseConfig(swapped)
			if err != nil {
				t.Fatalf("the swapped address does not parse: %v", err)
			}
			if cfg.Database != "demoworld_till" {
				t.Fatalf("the swapped address names database %q", cfg.Database)
			}
			orig, err := pgconn.ParseConfig(tc.dsn)
			if err != nil {
				t.Fatalf("the original address does not parse: %v", err)
			}
			if cfg.Host != orig.Host || cfg.Port != orig.Port || cfg.User != orig.User || cfg.Password != orig.Password {
				t.Fatalf("the swap moved the server: host %q to %q, port %d to %d", orig.Host, cfg.Host, orig.Port, cfg.Port)
			}
		})
	}
	t.Run("empty host with PGHOST unset falls back local", func(t *testing.T) {
		t.Setenv("PGHOST", "")
		dsn := "postgres:///hardscapeos?sslmode=disable"
		if err := checkLocalDSN(dsn); err != nil {
			t.Fatalf("the socket default was refused: %v", err)
		}
		swapped := swapDSNDatabase(dsn, "demoworld_till")
		cfg, err := pgconn.ParseConfig(swapped)
		if err != nil {
			t.Fatalf("the swapped address does not parse: %v", err)
		}
		if cfg.Database != "demoworld_till" || !hostIsLocal(cfg.Host) {
			t.Fatalf("the swapped address names database %q at host %q", cfg.Database, cfg.Host)
		}
	})
}
