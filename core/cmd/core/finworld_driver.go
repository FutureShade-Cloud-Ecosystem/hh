//go:build finworld

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	erpidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	finworldOrgID      = "88a20000-0000-4000-8000-0000000000f2"
	finworldOrgSlug    = "finworld-org"
	finworldBranchTRT  = "5d1b3f6e-0000-4000-9000-000000000001"
	finworldBranchKGN  = "5d1b3f6e-0000-4000-9000-000000000002"
	finworldStaffUser  = "d7000000-0000-4000-9000-000000000006"
	finworldStaffSub   = "finworld-controller-sub"
	finworldManagerSub = "finworld-branch-manager-sub"
	finworldClerkSub   = "finworld-clerk-sub"
	finworldMarkKey    = "finworld_mark"
	finworldMarkVal    = "v1"
)

func init() {
	if len(os.Args) >= 2 && os.Args[1] == "finworld" {
		if err := finworldMain(os.Args[2:], os.Stdout, os.Stderr, os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "core: finworld:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

type finworldIndexedDay struct {
	Day        FinWorldTradingDay
	MonthIndex int
	YearMonth  YearMonth
}

func finworldFlattenDays(plan *FinWorldPlanT) []finworldIndexedDay {
	var out []finworldIndexedDay
	for mIdx, mp := range plan.Months {
		for _, td := range mp.TradingDays {
			out = append(out, finworldIndexedDay{
				Day:        td,
				MonthIndex: mIdx,
				YearMonth:  mp.YearMonth,
			})
		}
	}
	return out
}

func finworldUUID(key string) string {
	sum := sha256.Sum256([]byte("finworld-v1:" + key))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func finworldBranchID(code string) (string, error) {
	switch code {
	case "TRT":
		return finworldBranchTRT, nil
	case "KGN":
		return finworldBranchKGN, nil
	default:
		return "", fmt.Errorf("unknown branch code %q", code)
	}
}

// finworldCheckEnvGuard refuses to run on a staging/production/box environment.
func finworldCheckEnvGuard(getenv func(string) string) error {
	env := strings.ToLower(strings.TrimSpace(getenv("APP_ENV")))
	switch env {
	case "staging", "production", "prod", "box":
		return fmt.Errorf("finworld refused: APP_ENV=%q is a box/production environment", env)
	}
	if bk := strings.TrimSpace(getenv("HH_BOX_KIND")); bk != "" {
		return fmt.Errorf("finworld refused: HH_BOX_KIND=%q is set", bk)
	}
	if sm := strings.TrimSpace(getenv("CORE_SIGNIN_MODE")); sm != "" {
		return fmt.Errorf("finworld refused: CORE_SIGNIN_MODE=%q is set", sm)
	}
	return nil
}

// finworldCheckDay0Guard ensures the database at day 0 is freshly migrated+seeded
// and has not already run finworld or non-seed trading.
func finworldCheckDay0Guard(ctx context.Context, pool *pgxpool.Pool) error {
	var markExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_settings WHERE key = $1)`, finworldMarkKey).Scan(&markExists); err != nil {
		return fmt.Errorf("day 0 guard check finworld_mark: %w", err)
	}
	if markExists {
		return errors.New("finworld refused at day 0: database already carries finworld_mark")
	}
	var tillCount, bankRecCount, posRetCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM till_sessions`).Scan(&tillCount); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM bank_reconciliations`).Scan(&bankRecCount); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM pos_returns`).Scan(&posRetCount); err != nil {
		return err
	}
	if tillCount > 0 || bankRecCount > 0 || posRetCount > 0 {
		return fmt.Errorf("finworld refused at day 0: database is not fresh (till_sessions=%d, bank_reconciliations=%d, pos_returns=%d)",
			tillCount, bankRecCount, posRetCount)
	}
	return nil
}

// finworldCheckSubsequentDayGuard ensures day > 0 only runs on a database marked by day 0.
func finworldCheckSubsequentDayGuard(ctx context.Context, pool *pgxpool.Pool, dayIdx int) error {
	var markVal string
	err := pool.QueryRow(ctx, `SELECT value #>> '{}' FROM system_settings WHERE key = $1`, finworldMarkKey).Scan(&markVal)
	if err != nil {
		return fmt.Errorf("finworld refused at day %d: missing finworld_mark (%w)", dayIdx, err)
	}
	if markVal != finworldMarkVal {
		return fmt.Errorf("finworld refused at day %d: unexpected finworld_mark %q", dayIdx, markVal)
	}
	bk, err := settings.NewDealerConfigReader(pool).BoxKind(ctx)
	if err != nil {
		return fmt.Errorf("finworld refused at day %d: BoxKind: %w", dayIdx, err)
	}
	if bk != "finance" {
		return fmt.Errorf("finworld refused at day %d: box_kind is %q, want \"finance\"", dayIdx, bk)
	}
	return nil
}

type finworldAuthDouble struct{}

func (finworldAuthDouble) OrgMember(_ context.Context, sub, orgSlug string) (bool, error) {
	return (sub == finworldStaffSub || sub == finworldManagerSub) && orgSlug == finworldOrgSlug, nil
}

func (finworldAuthDouble) TenantOrgs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (finworldAuthDouble) ResolveSlug(_ context.Context, orgSlug string) (string, bool, error) {
	if orgSlug == finworldOrgSlug {
		return finworldOrgID, true, nil
	}
	return "", false, nil
}

func (finworldAuthDouble) ResolveStaff(_ context.Context, sub, _ string) (erpidentity.StaffAuthority, bool, error) {
	if sub == finworldManagerSub {
		return erpidentity.StaffAuthority{
			RoleID: "branch_manager",
			Tier:   erpidentity.TierManager,
			Scope:  erpidentity.ScopeGlobal,
		}, true, nil
	}
	if sub == finworldClerkSub {
		// A frontline yard clerk, for proving the manager-guarded routes refuse closed.
		return erpidentity.StaffAuthority{
			RoleID: "sales_associate",
			Tier:   erpidentity.TierFrontline,
			Scope:  erpidentity.ScopeGlobal,
		}, true, nil
	}
	return erpidentity.StaffAuthority{
		RoleID: "financial_controller",
		Tier:   erpidentity.TierLeadership,
		Scope:  erpidentity.ScopeGlobal,
	}, true, nil
}

func (finworldAuthDouble) PortalRole(_ context.Context, _, _ string) (string, bool, error) {
	return "", false, nil
}

type finworldHarness struct {
	pool          *pgxpool.Pool
	mux           *http.ServeMux
	money         *moneySpine
	ops           *operationsSpine
	tops          *topsSpine
	disp          *notify.Dispatcher
	minter        *erpidentity.SessionMinter
	token         string
	managerToken  string
	vault         *payment.Vault
	bankAccountID string
}

func newFinWorldHarness(ctx context.Context, pool *pgxpool.Pool) (*finworldHarness, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	keys, err := erpidentity.NewSessionKeySet(&priv.PublicKey, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("session key set: %w", err)
	}
	minter, err := erpidentity.NewSessionMinter(priv, "finworld-issuer", "finworld-staff")
	if err != nil {
		return nil, fmt.Errorf("session minter: %w", err)
	}
	token, err := minter.MintAt(finworldStaffSub, 12*time.Hour, time.Now())
	if err != nil {
		return nil, fmt.Errorf("mint staff token: %w", err)
	}
	managerToken, err := minter.MintAt(finworldManagerSub, 12*time.Hour, time.Now())
	if err != nil {
		return nil, fmt.Errorf("mint manager token: %w", err)
	}
	verifier, err := erpidentity.NewVerifier(erpidentity.Config{
		KeySource:      keys,
		Issuer:         "finworld-issuer",
		StaffAudience:  "finworld-staff",
		TenantAudience: "finworld-tenant",
		Now:            time.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("verifier: %w", err)
	}
	ad := finworldAuthDouble{}
	gate, err := authz.NewGate(verifier, ad, ad)
	if err != nil {
		return nil, fmt.Errorf("authz gate: %w", err)
	}
	g, err := guards.NewGuards(gate, ad, ad, nil)
	if err != nil {
		return nil, fmt.Errorf("guards: %w", err)
	}

	vaultKey := hex.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	vault, err := payment.NewVault(vaultKey)
	if err != nil {
		return nil, fmt.Errorf("payment vault: %w", err)
	}

	rcfg, err := roleconfig.Default()
	if err != nil {
		return nil, fmt.Errorf("roleconfig: %w", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, g, log, "", rcfg)
	ops := wireOperations(mux, pool, g, log, money, "finworld-fx-ticket", belt.outbox)
	tops := wireTops(mux, pool, g, log, money, ops, config.Config{PaymentVaultKey: vaultKey}, rcfg, belt)
	tops.posSvc.WithTaxAssessor(finworldPosTaxAdapter{svc: newTaxEngine(pool)})

	d := notify.NewDispatcher(money.outboxStore, log)
	portal.RegisterNotificationHandlers(d, tops.notifier, "", log)
	money.glSvc.RegisterPostingHandlers(d)
	money.inventorySvc.RegisterPostingHandlers(d)
	order.NewLifecycleConsumer(money.orderSvc, tops.billingRepo, log).RegisterHandlers(d)
	registerOrderEventOwners(d, log)
	messaging.NewConsumer(ops.messagingSvc, log).RegisterHandlers(d)
	messaging.NewPortalBridge(ops.messagingSvc, log).RegisterHandlers(d)

	var bankAccountID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&bankAccountID); err != nil {
		return nil, fmt.Errorf("operating bank account 1000: %w", err)
	}

	return &finworldHarness{
		pool:          pool,
		mux:           mux,
		money:         money,
		ops:           ops,
		tops:          tops,
		disp:          d,
		minter:        minter,
		token:         token,
		managerToken:  managerToken,
		vault:         vault,
		bankAccountID: bankAccountID,
	}, nil
}

func (h *finworldHarness) drain(ctx context.Context) error {
	for i := 0; i < 200; i++ {
		if h.disp.DrainOnce(ctx) < 50 {
			break
		}
	}
	return nil
}

func (h *finworldHarness) verifyOutboxEmpty(ctx context.Context) error {
	if err := h.drain(ctx); err != nil {
		return err
	}
	var pending int
	if err := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM events_outbox WHERE status = 'PENDING'`).Scan(&pending); err != nil {
		return err
	}
	var dead int
	if err := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM events_outbox WHERE status = 'DEAD'`).Scan(&dead); err != nil {
		return err
	}
	if pending+dead > 0 {
		var topic, lastErr string
		_ = h.pool.QueryRow(ctx, `SELECT topic, COALESCE(last_error, '') FROM events_outbox WHERE status IN ('PENDING', 'DEAD') LIMIT 1`).Scan(&topic, &lastErr)
		return fmt.Errorf("outbox has %d PENDING/DEAD events after drain (first topic=%s err=%s)", pending+dead, topic, lastErr)
	}
	return nil
}

func (h *finworldHarness) callWithToken(tok, step, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("%s: marshal body: %w", step, err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set(authz.HeaderXOrg, finworldOrgSlug)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		return nil, fmt.Errorf("%s: %s %s refused with HTTP %d: %s", step, method, path, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	return rec.Body.Bytes(), nil
}

func (h *finworldHarness) call(step, method, path string, body any) ([]byte, error) {
	return h.callWithToken(h.token, step, method, path, body)
}

func finworldDefaultDealer(getenv func(string) string) string {
	if v := strings.TrimSpace(getenv("HH_WEB_DEALER_NAME")); v != "" {
		return v
	}
	return "Quarryfen Hardscape Supply"
}

// slogDefault is the discarded logger the tie-side composition reads go through.
func slogDefault() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// finworldDefaultAnchor is the world's own anchor: the twelve months the plan lays out end here.
const finworldDefaultAnchor = "2026-09"

// finworldParseAnchor parses the --anchor flag's YYYY-MM into the plan's first month.
func finworldParseAnchor(s string) (*YearMonth, error) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) != 2 {
		return nil, fmt.Errorf("anchor %q must be YYYY-MM", s)
	}
	year, err := strconv.Atoi(parts[0])
	if err != nil || year < 1 {
		return nil, fmt.Errorf("anchor %q must carry a year of digits", s)
	}
	month, err := strconv.Atoi(parts[1])
	if err != nil || month < 1 || month > 12 {
		return nil, fmt.Errorf("anchor %q must carry a month 1 to 12", s)
	}
	return &YearMonth{Year: year, Month: month}, nil
}

func finworldMain(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if err := finworldCheckEnvGuard(getenv); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: core finworld <day|check> [flags]")
	}
	sub := args[0]
	switch sub {
	case "day":
		fs := flag.NewFlagSet("finworld day", flag.ContinueOnError)
		fs.SetOutput(stderr)
		dayIdx := fs.Int("day", -1, "simulated trading day index (0..311)")
		seed := fs.Uint64("seed", 1, "FinWorldPlan seed")
		dealer := fs.String("dealer", finworldDefaultDealer(getenv), "dealer name")
		anchor := fs.String("anchor", finworldDefaultAnchor, "plan anchor year month (YYYY-MM)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		anchorYM, err := finworldParseAnchor(*anchor)
		if err != nil {
			return err
		}
		if *dayIdx < 0 {
			return errors.New("core finworld day: --day N (>= 0) is required")
		}
		dsn, err := resolveOneshotDatabaseURL(getenv)
		if err != nil {
			return fmt.Errorf("finworld day: %w", err)
		}
		ctx := context.Background()
		pool, err := db.Connect(ctx, dsn)
		if err != nil {
			return fmt.Errorf("finworld day connect: %w", err)
		}
		defer pool.Close()

		plan, err := FinWorldPlan(*seed, *anchorYM, *dealer)
		if err != nil {
			return fmt.Errorf("FinWorldPlan: %w", err)
		}
		days := finworldFlattenDays(plan)
		if err := finworldRunDay(ctx, pool, plan, *dayIdx); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "finworld day %d (%s) complete\n", *dayIdx, days[*dayIdx].Day.Date)
		return nil

	case "check":
		fs := flag.NewFlagSet("finworld check", flag.ContinueOnError)
		fs.SetOutput(stderr)
		throughDay := fs.Int("through-day", 312, "trading day index to check ties through")
		seed := fs.Uint64("seed", 1, "FinWorldPlan seed")
		dealer := fs.String("dealer", finworldDefaultDealer(getenv), "dealer name")
		anchor := fs.String("anchor", finworldDefaultAnchor, "plan anchor year month (YYYY-MM)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		anchorYM, err := finworldParseAnchor(*anchor)
		if err != nil {
			return err
		}
		dsn, err := resolveOneshotDatabaseURL(getenv)
		if err != nil {
			return fmt.Errorf("finworld check: %w", err)
		}
		ctx := context.Background()
		pool, err := db.Connect(ctx, dsn)
		if err != nil {
			return fmt.Errorf("finworld check connect: %w", err)
		}
		defer pool.Close()

		plan, err := FinWorldPlan(*seed, *anchorYM, *dealer)
		if err != nil {
			return fmt.Errorf("FinWorldPlan: %w", err)
		}
		ties, err := runFinWorldTies(ctx, pool, plan, *throughDay)
		if err != nil {
			return err
		}
		failed := 0
		for _, tr := range ties {
			status := "PASS"
			if !tr.Passed {
				status = "FAIL"
				failed++
			}
			fmt.Fprintf(stdout, "[%s] %s: %s\n", status, tr.Name, tr.Detail)
		}
		if failed > 0 {
			return fmt.Errorf("finworld check: %d tie(s) failed", failed)
		}
		return nil

	case "days":
		fs := flag.NewFlagSet("finworld days", flag.ContinueOnError)
		fs.SetOutput(stderr)
		seed := fs.Uint64("seed", 1, "FinWorldPlan seed")
		dealer := fs.String("dealer", finworldDefaultDealer(getenv), "dealer name")
		anchor := fs.String("anchor", finworldDefaultAnchor, "plan anchor year month (YYYY-MM)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		anchorYM, err := finworldParseAnchor(*anchor)
		if err != nil {
			return err
		}
		plan, err := FinWorldPlan(*seed, *anchorYM, *dealer)
		if err != nil {
			return fmt.Errorf("FinWorldPlan: %w", err)
		}
		for _, d := range finworldFlattenDays(plan) {
			fmt.Fprintf(stdout, "%d %s %s\n", d.Day.TradingDayIndex, d.Day.Date, d.YearMonth.String())
		}
		return nil

	case "census":
		// The world's books census (W2-1d item 1): builds the plan alone, no
		// database, and fails on engine data where a dealer's books carry
		// names, bookkeeper memos, or an unmeant late payable. The source scan
		// walks the product code from the core tree above the working
		// directory, so run this leg from inside a checkout.
		fs := flag.NewFlagSet("finworld census", flag.ContinueOnError)
		fs.SetOutput(stderr)
		seed := fs.Uint64("seed", 1, "FinWorldPlan seed")
		dealer := fs.String("dealer", finworldDefaultDealer(getenv), "dealer name")
		anchor := fs.String("anchor", finworldDefaultAnchor, "plan anchor year month (YYYY-MM)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		anchorYM, err := finworldParseAnchor(*anchor)
		if err != nil {
			return err
		}
		plan, err := FinWorldPlan(*seed, *anchorYM, *dealer)
		if err != nil {
			return fmt.Errorf("FinWorldPlan: %w", err)
		}
		cwd, _ := os.Getwd()
		res, err := finworldCensusPlan(plan, finworldFindCoreRoot(cwd))
		if err != nil {
			return fmt.Errorf("finworld census: %w", err)
		}
		for _, f := range res.Names {
			fmt.Fprintf(stdout, "NAME %s\n", f)
		}
		for _, f := range res.Memos {
			fmt.Fprintf(stdout, "MEMO %s\n", f)
		}
		for _, f := range res.LatePayables {
			fmt.Fprintf(stdout, "LATE %s\n", f)
		}
		for _, f := range res.SourceMemos {
			fmt.Fprintf(stdout, "SOURCE %s\n", f)
		}
		fmt.Fprintf(stdout, "finworld census: %d name(s), %d memo(s), %d late payable(s), %d source memo(s)\n",
			len(res.Names), len(res.Memos), len(res.LatePayables), len(res.SourceMemos))
		if !res.Clean() {
			return fmt.Errorf("finworld census: the world's books do not read like a real dealer's")
		}
		return nil

	default:
		return fmt.Errorf("unknown finworld subcommand %q (want day, check, days or census)", sub)
	}
}

func finworldSetupDay0(ctx context.Context, h *finworldHarness, plan *FinWorldPlanT) error {
	if err := finworldCheckDay0Guard(ctx, h.pool); err != nil {
		return err
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := settings.WriteBoxKindTx(ctx, tx, "finance"); err != nil {
		return fmt.Errorf("WriteBoxKindTx: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO system_settings (key, value)
		VALUES ($1, to_jsonb($2::text))
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		finworldMarkKey, finworldMarkVal); err != nil {
		return fmt.Errorf("write finworld_mark: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	setupSQL := []string{
		fmt.Sprintf(`INSERT INTO org (id, slug, name) VALUES ('%s', '%s', 'FinWorld Org') ON CONFLICT (id) DO NOTHING`, finworldOrgID, finworldOrgSlug),
		fmt.Sprintf(`UPDATE locations SET org_id = '%s' WHERE loc_type = 'BRANCH'`, finworldOrgID),
		fmt.Sprintf(`UPDATE users SET platform_user_id = '%s' WHERE id = '%s'`, finworldStaffSub, finworldStaffUser),
		fmt.Sprintf(`INSERT INTO users (id, email, display_name, status, org_id, platform_user_id) VALUES ('00000000-0000-0000-0000-000000000109', 'finworld-mgr@example.invalid', 'FinWorld Branch Manager', 'ACTIVE', '%s', '%s') ON CONFLICT (id) DO UPDATE SET platform_user_id = EXCLUDED.platform_user_id, status = 'ACTIVE'`, finworldOrgID, finworldManagerSub),
		// Clear pre-seeded trading rows so the world starts from a clean opening trial balance
		`TRUNCATE TABLE payments, customer_transactions, pos_returns, pos_sale, invoices, orders, quotes, landed_costs, purchasing_bills, purchasing_receipts, purchasing_pos, purchase_orders, events_outbox, stock_balances, stock_moves, product_branch_costs, gl_journals CASCADE`,
		// Clear demo pricing surcharges/quantity breaks so pricing resolves strictly from FinWorldPlan list & contractor rules
		`DELETE FROM pricing_promotions`,
		`DELETE FROM pricing_clearance_items`,
		`DELETE FROM pricing_uom_conversions`,
		`DELETE FROM quantity_break_items`,
		`DELETE FROM quantity_break_headers`,
		`DELETE FROM customer_category_overrides`,
		`DELETE FROM pricing_rules`,
		`UPDATE product_rounding_rules SET break_pallet_fee_cents = 0, waste_factor_pct = 0`,
		`UPDATE pricing_category_policy SET margin_floor_bps = 0, margin_enforcement_disabled = true`,
		`UPDATE pos_policy SET cod_enabled = true`,
		`UPDATE ar_credit_policy SET aging_hold_enabled = false, limit_hold_enabled = false, cod_enforced = false, auto_hold_enabled = false, limit_tolerance_cents = 999999999`,
		`UPDATE nsf_policy SET fee_cents = 4500, fee_taxable = false, fee_account_code = '4200', auto_hold = false`,
	}
	for _, q := range setupSQL {
		if _, err := h.pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("day 0 setup query (%s): %w", q, err)
		}
	}

	// Ensure fiscal periods for 2025, 2026, 2027 exist.
	for _, yr := range []int{2025, 2026, 2027} {
		for mo := 1; mo <= 12; mo++ {
			if _, err := h.pool.Exec(ctx, `
				INSERT INTO fiscal_periods (year, month, status)
				VALUES ($1, $2, 'OPEN')
				ON CONFLICT (year, month) DO UPDATE SET status = 'OPEN', closed_at = NULL, closed_by = NULL`,
				yr, mo); err != nil {
				return fmt.Errorf("ensure fiscal_period %04d-%02d: %w", yr, mo, err)
			}
		}
	}

	if err := payment.NewStore(h.pool, h.vault).Configure(
		ctx, "run_payments", "MID-FINWORLD", "CAD", "", 0, false,
		map[string]string{"api_key": "finworld-stub-key"}, "",
	); err != nil {
		return fmt.Errorf("configure card rail: %w", err)
	}

	// Create dedicated GL accounts through POST /api/v1/gl/accounts on free account codes:
	// - 6700: Equipment Fuel and Maintenance (EXPENSE, DEBIT)
	// - 2400: Accrued Liabilities (LIABILITY, CREDIT)
	// - 6750: Merchant Processing Fees (EXPENSE, DEBIT)
	for _, acct := range []struct {
		code          string
		name          string
		accountType   string
		normalBalance string
	}{
		{"6700", "Equipment Fuel and Maintenance", "EXPENSE", "DEBIT"},
		{"2400", "Accrued Liabilities", "LIABILITY", "CREDIT"},
		{"6750", "Merchant Processing Fees", "EXPENSE", "DEBIT"},
	} {
		if _, err := h.call("create GL account "+acct.code, http.MethodPost, "/api/v1/gl/accounts", map[string]any{
			"code":           acct.code,
			"name":           acct.name,
			"account_type":   acct.accountType,
			"normal_balance": acct.normalBalance,
		}); err != nil {
			return fmt.Errorf("create GL account %s (%s): %w", acct.code, acct.name, err)
		}
	}

	// The vendors the plan names (the catalogue's suppliers, and from W2-1d
	// item 3 the landed costs' carriers and brokers), inserted under the
	// plan's own display name and terms. A vendor the seed already plants
	// keeps its row; a carrier or broker gets its named row here.
	vendorNameByNo := make(map[string]FinWorldVendor, len(plan.Vendors))
	for _, v := range plan.Vendors {
		vendorNameByNo[v.VendorNo] = v
	}
	for _, v := range plan.Vendors {
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO vendors (vendor_no, name, payment_terms_days, is_edi)
			VALUES ($1::citext, $2::text, $3, false)
			ON CONFLICT (vendor_no) DO NOTHING`, v.VendorNo, v.Name, v.PaymentTermsDays); err != nil {
			return fmt.Errorf("ensure vendor %s: %w", v.VendorNo, err)
		}
	}

	// Align all 30 products, UOMs, and list prices with FinWorldPlan
	for _, p := range plan.Products {
		catCode := p.CategoryCode
		if p.CategoryCode == "SLABSTEP" && p.SellUOM == "SF" {
			catCode = "PAVER"
		}
		var prodID string
		err := h.pool.QueryRow(ctx, `
			UPDATE products
			   SET name = $2::text,
			       category_id = (SELECT id FROM product_categories WHERE code = $3::citext),
			       base_uom = $4::citext,
			       price_uom = $5::citext,
			       sold_by = $5::text,
			       pieces_per_sf = 1,
			       pallet_count = NULL
			 WHERE sku = $1::citext
			 RETURNING id::text`,
			p.SKU, p.Name, catCode, p.BaseUOM, p.SellUOM).Scan(&prodID)
		if err != nil {
			return fmt.Errorf("align product %s: %w", p.SKU, err)
		}
		if _, err := h.pool.Exec(ctx, `DELETE FROM product_uoms WHERE product_id = $1::uuid`, prodID); err != nil {
			return err
		}
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
			VALUES ($1::uuid, $2::citext, 1, true)`, prodID, p.BaseUOM); err != nil {
			return fmt.Errorf("insert base uom %s: %w", p.SKU, err)
		}
		if p.SellUOM != p.BaseUOM {
			if _, err := h.pool.Exec(ctx, `
				INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
				VALUES ($1::uuid, $2::citext, 1, false)
				ON CONFLICT (product_id, uom_code) DO UPDATE SET qty_in_base = 1`, prodID, p.SellUOM); err != nil {
				return fmt.Errorf("insert sell uom %s: %w", p.SKU, err)
			}
		}
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
			VALUES ($1::uuid, $2::bigint, $3::citext)
			ON CONFLICT (product_id) DO UPDATE SET list_price_cents = EXCLUDED.list_price_cents, price_uom = EXCLUDED.price_uom`,
			prodID, p.ListPriceCents, p.SellUOM); err != nil {
			return fmt.Errorf("upsert list price %s: %w", p.SKU, err)
		}
		if _, ok := vendorNameByNo[p.VendorNo]; !ok {
			return fmt.Errorf("ensure vendor %s: the plan names no vendor for this product", p.VendorNo)
		}
	}

	// Insert the contractor category rules matching FinWorldPlan's ContractorPriceCents:
	// PAVER: -18% (1800 bps), WALL: -10% (1000 bps), SLABSTEP: -10% (1000 bps), BULK: -10% (1000 bps), ACCESSORY: -8% (800 bps)
	catDiscounts := []struct {
		code string
		bps  int
		uom  string
	}{
		{"PAVER", 1800, "SF"},
		{"WALL", 1000, "SF"},
		{"SLABSTEP", 1000, "EA"},
		{"BULK", 1000, "T"},
		{"ACCESSORY", 800, "EA"},
	}
	for _, cd := range catDiscounts {
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO pricing_rules (scope, customer_type, category_id, calc_type, discount_bps, price_uom, priority, detail_text, is_active)
			SELECT 'TIER_CATEGORY', 'CONTRACTOR', id, 'PCT_OFF_LIST', $2, $3, 100, 'FinWorld Contractor Tier', true
			  FROM product_categories WHERE code = $1`, cd.code, cd.bps, cd.uom); err != nil {
			return fmt.Errorf("insert pricing rule %s: %w", cd.code, err)
		}
	}

	// Create all 250 invented customers (70 contractors + 180 homeowners) through POST /api/v1/customers
	allCustomers := make([]FinWorldCustomer, 0, len(plan.Contractors)+len(plan.Homeowners))
	allCustomers = append(allCustomers, plan.Contractors...)
	allCustomers = append(allCustomers, plan.Homeowners...)
	for _, c := range allCustomers {
		homeBranchID, err := finworldBranchID(c.HomeBranchCode)
		if err != nil {
			return err
		}
		terms := c.PaymentTermsDays
		creditLimit := c.CreditLimitCents
		if creditLimit < 500000000 {
			creditLimit = 500000000
		}
		payload := map[string]any{
			"account_no":         c.AccountNo,
			"name":               c.Name,
			"customer_type":      c.CustomerType,
			"home_branch_id":     homeBranchID,
			"payment_terms_days": &terms,
			"credit_limit_cents": creditLimit,
			"tax_exempt":         false,
			"billing_address": map[string]any{
				"line1":    c.StreetAddress,
				"city":     c.Town,
				"province": c.Province,
				"postal":   c.PostalCode,
				"country":  "CA",
			},
		}
		if _, err := h.call("create customer "+c.AccountNo, http.MethodPost, "/api/v1/customers", payload); err != nil {
			return err
		}
	}

	// Load opening stock into stock_balances, product_branch_costs, stock_moves, and post opening GL 1200 / 3000 per branch
	// The opening balances sit on the day before the world's first trading month, at noon in the
	// dealer's house zone (day 0 of the start month is the prior month's last day), so the cutover
	// follows the anchor and is decided on the house's day, never at Greenwich.
	cutoverDate := time.Date(plan.StartMonth.Year, time.Month(plan.StartMonth.Month), 0, 12, 0, 0, 0, houseday.DefaultZone().Location())
	var trtStockVal, kgnStockVal int64
	for _, p := range plan.Products {
		var prodID string
		if err := h.pool.QueryRow(ctx, `SELECT id::text FROM products WHERE sku = $1`, p.SKU).Scan(&prodID); err != nil {
			return err
		}
		type branchOpen struct {
			branchID string
			qty      int64
			val      *int64
		}
		for _, bo := range []branchOpen{
			{finworldBranchTRT, p.OpeningStockTRT, &trtStockVal},
			{finworldBranchKGN, p.OpeningStockKGN, &kgnStockVal},
		} {
			lineVal := bo.qty * p.StandardCostCents
			*bo.val += lineVal
			if _, err := h.pool.Exec(ctx, `
				INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
				VALUES ($1::uuid, $2::uuid, $3)
				ON CONFLICT (product_id, branch_id) DO UPDATE
				SET avg_unit_cost_cents = EXCLUDED.avg_unit_cost_cents`,
				prodID, bo.branchID, p.StandardCostCents); err != nil {
				return fmt.Errorf("opening cost %s/%s: %w", p.SKU, bo.branchID, err)
			}
			if _, err := h.pool.Exec(ctx, `
				INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
				VALUES ($1::uuid, $2::uuid, $2::uuid, $3)
				ON CONFLICT (product_id, location_id) DO UPDATE SET on_hand_base = EXCLUDED.on_hand_base`,
				prodID, bo.branchID, bo.qty); err != nil {
				return fmt.Errorf("opening stock_balances %s/%s: %w", p.SKU, bo.branchID, err)
			}
			if _, err := h.pool.Exec(ctx, `
				INSERT INTO stock_moves (product_id, to_location_id, qty_base, qty_entered, uom_entered, move_type, unit_cost_cents, value_cents, branch_id, reason)
				VALUES ($1::uuid, $2::uuid, $3, $3, $4, 'RECEIPT', $5, $6, $2::uuid, 'FinWorld opening stock')`,
				prodID, bo.branchID, bo.qty, p.BaseUOM, p.StandardCostCents, lineVal); err != nil {
				return fmt.Errorf("opening stock_moves %s/%s: %w", p.SKU, bo.branchID, err)
			}
		}
	}

	if _, err := h.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source:         "MANUAL",
		IdempotencyKey: "finworld:opening-stock:TRT",
		Memo:           "FinWorld opening inventory balance TRT",
		BranchID:       finworldBranchTRT,
		DocumentDate:   cutoverDate,
		Lines: []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: trtStockVal, Memo: "Opening inventory TRT"},
			{AccountCode: "3000", CreditCents: trtStockVal, Memo: "Opening equity TRT inventory"},
		},
	}); err != nil {
		return fmt.Errorf("post opening stock GL TRT: %w", err)
	}
	if _, err := h.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source:         "MANUAL",
		IdempotencyKey: "finworld:opening-stock:KGN",
		Memo:           "FinWorld opening inventory balance KGN",
		BranchID:       finworldBranchKGN,
		DocumentDate:   cutoverDate,
		Lines: []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: kgnStockVal, Memo: "Opening inventory KGN"},
			{AccountCode: "3000", CreditCents: kgnStockVal, Memo: "Opening equity KGN inventory"},
		},
	}); err != nil {
		return fmt.Errorf("post opening stock GL KGN: %w", err)
	}

	// Post opening operating bank balance ($185,000.00 = 18,500,000 cents) on GL 1000 / 3000 and record bank cutover
	openingBankCents := int64(18500000)
	if _, err := h.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source:         "MANUAL",
		IdempotencyKey: "finworld:opening-bank:TRT",
		Memo:           "FinWorld opening operating bank balance",
		BranchID:       finworldBranchTRT,
		DocumentDate:   cutoverDate,
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: openingBankCents, Memo: "Opening operating bank"},
			{AccountCode: "3000", CreditCents: openingBankCents, Memo: "Opening equity operating bank"},
		},
	}); err != nil {
		return fmt.Errorf("post opening bank GL: %w", err)
	}

	if _, err := h.call("bank cutover", http.MethodPost, "/api/v1/bankrec/accounts/"+h.bankAccountID+"/cutover", map[string]any{
		"cutover_date":                    "2025-09-30",
		"opening_statement_balance_cents": openingBankCents,
	}); err != nil {
		return err
	}

	// Mark pre-cutover months 2025-01..2025-09 CLOSED so CloseYear(2025) only pre-flights 2025-10..2025-12
	if _, err := h.pool.Exec(ctx, `
		UPDATE fiscal_periods
		   SET status = 'CLOSED', closed_at = now(), closed_by = $1::uuid
		 WHERE year = 2025 AND month <= 9`, finworldStaffUser); err != nil {
		return fmt.Errorf("close pre-cutover 2025 periods: %w", err)
	}

	return h.verifyOutboxEmpty(ctx)
}

func finworldRunDay(ctx context.Context, pool *pgxpool.Pool, plan *FinWorldPlanT, dayIdx int) error {
	days := finworldFlattenDays(plan)
	if dayIdx < 0 || dayIdx >= len(days) {
		return fmt.Errorf("dayIdx %d out of range 0..%d", dayIdx, len(days)-1)
	}
	h, err := newFinWorldHarness(ctx, pool)
	if err != nil {
		return err
	}

	if dayIdx == 0 {
		if err := finworldSetupDay0(ctx, h, plan); err != nil {
			return err
		}
	} else {
		if err := finworldCheckSubsequentDayGuard(ctx, pool, dayIdx); err != nil {
			return err
		}
	}

	day := days[dayIdx].Day
	loc, err := time.LoadLocation(plan.HouseZone)
	if err != nil {
		return err
	}
	dayDate, err := time.ParseInLocation("2006-01-02", day.Date, loc)
	if err != nil {
		return err
	}
	docTime := time.Date(dayDate.Year(), dayDate.Month(), dayDate.Day(), 12, 0, 0, 0, loc)

	prodBySKU := make(map[string]string, len(plan.Products))
	rows, err := pool.Query(ctx, `SELECT sku, id::text FROM products`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var sku, id string
		if err := rows.Scan(&sku, &id); err != nil {
			rows.Close()
			return err
		}
		prodBySKU[sku] = id
	}
	rows.Close()

	custByNo := make(map[string]string, 260)
	cRows, err := pool.Query(ctx, `SELECT account_no::text, id::text FROM customers`)
	if err != nil {
		return err
	}
	for cRows.Next() {
		var no, id string
		if err := cRows.Scan(&no, &id); err != nil {
			cRows.Close()
			return err
		}
		custByNo[no] = id
	}
	cRows.Close()

	custIDByKey := make(map[string]string, 260)
	for _, c := range plan.Contractors {
		custIDByKey[c.Key] = custByNo[c.AccountNo]
	}
	for _, c := range plan.Homeowners {
		custIDByKey[c.Key] = custByNo[c.AccountNo]
	}

	vendorByNo := make(map[string]string, 40)
	vRows, err := pool.Query(ctx, `SELECT vendor_no, id::text FROM vendors`)
	if err != nil {
		return err
	}
	for vRows.Next() {
		var no, id string
		if err := vRows.Scan(&no, &id); err != nil {
			vRows.Close()
			return err
		}
		vendorByNo[no] = id
	}
	vRows.Close()

	// Track active till session IDs by SessionKey and TillKey for this day
	tillSessByKey := make(map[string]string)

	for _, ev := range day.Events {
		switch ev.Kind {
		case FinWorldEventTillOpen:
			to := ev.TillOpen
			branchID, err := finworldBranchID(to.BranchCode)
			if err != nil {
				return err
			}
			out, err := h.call(ev.EventKey, http.MethodPost, "/api/v1/till/sessions", map[string]any{
				"branch_id":   branchID,
				"station":     to.TillKey,
				"float_cents": to.OpeningFloatCents,
			})
			if err != nil {
				return err
			}
			var res struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(out, &res); err != nil {
				return err
			}
			tillSessByKey[to.SessionKey] = res.ID
			tillSessByKey[to.TillKey] = res.ID
			if tillSessByKey[to.BranchCode] == "" {
				tillSessByKey[to.BranchCode] = res.ID
			}

		case FinWorldEventTillPaidInOut:
			pio := ev.TillPaidInOut
			sessID := tillSessByKey[pio.SessionKey]
			if sessID == "" {
				sessID = tillSessByKey[pio.TillKey]
			}
			if sessID == "" {
				return fmt.Errorf("%s: unknown till session for %s", ev.EventKey, pio.SessionKey)
			}
			if _, err := h.call(ev.EventKey, http.MethodPost, "/api/v1/till/sessions/"+sessID+"/movements", map[string]any{
				"movement_type": pio.Direction,
				"method":        "CASH",
				"amount_cents":  pio.AmountCents,
				"reason":        pio.Reason,
				"reference":     pio.MovementKey,
			}); err != nil {
				return err
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventCounterSale, FinWorldEventAccountSale, FinWorldEventDeliveredSale, FinWorldEventCollectLaterOrCODSale:
			s := ev.Sale
			if err := finworldExecuteSale(ctx, h, s, prodBySKU, custIDByKey, tillSessByKey); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, s.SaleKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventPurchaseReceipt:
			pr := ev.PurchaseReceipt
			if err := finworldExecutePurchaseReceipt(ctx, h, pr, prodBySKU, vendorByNo, day.Date); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, pr.PurchaseReceiptKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventVendorBill:
			vb := ev.VendorBill
			if err := finworldExecuteVendorBill(ctx, h, vb, prodBySKU, vendorByNo, docTime); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, vb.VendorBillKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventVendorPayment:
			vpEv := ev.VendorPayment
			if err := finworldExecuteVendorPayment(ctx, h, vpEv, docTime); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, vpEv.VendorPaymentKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventLandedCost:
			lcEv := ev.LandedCost
			if err := finworldExecuteLandedCost(ctx, h, lcEv, vendorByNo); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, lcEv.LandedCostKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventStockTransfer:
			st := ev.StockTransfer
			if err := finworldExecuteStockTransfer(ctx, h, st, prodBySKU); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, st.TransferKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventReturn:
			ret := ev.Return
			if err := finworldExecuteReturn(ctx, h, ret, prodBySKU, tillSessByKey); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, ret.ReturnKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventCreditMemo:
			cm := ev.CreditMemo
			if err := finworldExecuteCreditMemo(ctx, h, cm, custIDByKey); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, cm.CreditMemoKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventAccountReceipt:
			ar := ev.AccountReceipt
			if err := finworldExecuteAccountReceipt(ctx, h, ar, custIDByKey); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, ar.ReceiptKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventNSFBounce:
			nsf := ev.NSFBounce
			if err := finworldExecuteNSFBounce(ctx, h, nsf, docTime); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, nsf.BounceKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventManualJournal:
			mj := ev.ManualJournal
			if err := finworldExecuteManualJournal(ctx, h, mj); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, mj.JournalKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventTillCount:
			tc := ev.TillCount
			if err := finworldExecuteTillCount(ctx, h, tc, tillSessByKey); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, tc.SessionKey, err)
			}

		case FinWorldEventTillComplete:
			tcomp := ev.TillComplete
			if err := finworldExecuteTillComplete(ctx, h, tcomp, tillSessByKey); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, tcomp.SessionKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventLodgement:
			lodg := ev.Lodgement
			if err := finworldExecuteLodgement(ctx, h, lodg); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, lodg.LodgementKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventCardSettlement:
			cs := ev.CardSettlement
			if err := finworldExecuteCardSettlement(ctx, h, cs, docTime); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, cs.SettlementKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventBankStatement:
			if err := finworldStampDayTime(ctx, h.pool, docTime); err != nil {
				return err
			}
			bs := ev.BankStatement
			if err := finworldExecuteBankStatement(ctx, h, plan, bs, docTime); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, bs.StatementKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventPeriodClose:
			if err := finworldStampDayTime(ctx, h.pool, docTime); err != nil {
				return err
			}
			pc := ev.PeriodClose
			if err := finworldExecutePeriodClose(ctx, h, pc, loc); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, pc.CloseKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		case FinWorldEventYearClose:
			if err := finworldStampDayTime(ctx, h.pool, docTime); err != nil {
				return err
			}
			yc := ev.YearClose
			if err := finworldExecuteYearClose(ctx, h, yc, loc); err != nil {
				return fmt.Errorf("%s (%s): %w", ev.EventKey, yc.YearCloseKey, err)
			}
			if err := h.drain(ctx); err != nil {
				return fmt.Errorf("%s drain: %w", ev.EventKey, err)
			}

		default:
			return fmt.Errorf("unhandled FinWorldEventKind %q (%s)", ev.Kind, ev.EventKey)
		}
	}

	if err := finworldStampDayTime(ctx, h.pool, docTime); err != nil {
		return err
	}

	// On the last trading day of each month, also run the MASS statement run for that month's calendar end.
	if day.IsLastOfMonth {
		ym := days[dayIdx].YearMonth
		endOfMonthDate := time.Date(ym.Year, time.Month(ym.Month), 1, 0, 0, 0, 0, loc).AddDate(0, 1, -1).Format("2006-01-02")
		if _, err := h.call("statement run "+endOfMonthDate, http.MethodPost, "/api/v1/ar/statement-runs", map[string]any{
			"mode":  "MASS",
			"as_of": endOfMonthDate,
		}); err != nil {
			return fmt.Errorf("statement run %s: %w", endOfMonthDate, err)
		}
		if dayIdx == len(days)-1 {
			nextEndOfMonthDate := time.Date(ym.Year, time.Month(ym.Month), 1, 0, 0, 0, 0, loc).AddDate(0, 2, -1).Format("2006-01-02")
			if _, err := h.call("statement run "+nextEndOfMonthDate, http.MethodPost, "/api/v1/ar/statement-runs", map[string]any{
				"mode":  "MASS",
				"as_of": nextEndOfMonthDate,
			}); err != nil {
				return fmt.Errorf("statement run %s: %w", nextEndOfMonthDate, err)
			}
		}
	}

	return h.verifyOutboxEmpty(ctx)
}

func finworldStampDayTime(ctx context.Context, pool *pgxpool.Pool, docTime time.Time) error {
	if os.Getenv("HH_CLOCK_SHIFT_SECONDS") != "" {
		return nil
	}
	cutoff := docTime.Add(24 * time.Hour)
	queries := []string{
		`UPDATE orders SET created_at = $1::timestamptz, updated_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE invoices SET created_at = $1::timestamptz, finalized_at = CASE WHEN finalized_at IS NOT NULL THEN $1::timestamptz ELSE NULL END, updated_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE customer_transactions SET occurred_at = $1::timestamptz, created_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE payments SET received_at = $1::timestamptz, created_at = $1::timestamptz, updated_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE payment_allocations SET created_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE payment_reapplications SET created_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE payment_nsf SET created_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE pos_sale SET created_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE pos_returns SET created_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE till_sessions SET opened_at = $1::timestamptz, closed_at = CASE WHEN closed_at IS NOT NULL THEN $1::timestamptz ELSE NULL END WHERE opened_at > $2::timestamptz`,
		`UPDATE till_movements SET created_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE purchasing_pos SET created_at = $1::timestamptz, updated_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE purchasing_receipts SET received_at = $1::timestamptz, created_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE purchasing_bills SET posted_at = CASE WHEN posted_at IS NOT NULL THEN $1::timestamptz ELSE NULL END, created_at = $1::timestamptz, updated_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE stock_moves SET created_at = $1::timestamptz, occurred_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		// landed_costs carries no applied_at/voided_at: the AP tie reads the outstanding payable
		// as-of a month end from created_at (create) and updated_at (the apply/void status flip),
		// so both stamps must ride the simulated day like every other document time.
		`UPDATE landed_costs SET created_at = $1::timestamptz, updated_at = $1::timestamptz WHERE created_at > $2::timestamptz`,
		`UPDATE landed_costs SET updated_at = $1::timestamptz WHERE updated_at > $2::timestamptz`,
	}
	for _, q := range queries {
		if _, err := pool.Exec(ctx, q, docTime, cutoff); err != nil {
			return fmt.Errorf("stamp day time (%s): %w", q, err)
		}
	}
	return nil
}

func finworldExecuteSale(
	ctx context.Context,
	h *finworldHarness,
	s *FinWorldSaleEvent,
	prodBySKU, custIDByKey, tillSessByKey map[string]string,
) error {
	branchID, err := finworldBranchID(s.BranchCode)
	if err != nil {
		return err
	}
	tillSessID := tillSessByKey[s.SessionKey]
	if tillSessID == "" {
		tillSessID = tillSessByKey[s.BranchCode]
	}
	if tillSessID == "" {
		return fmt.Errorf("missing till session for %s (branch %s)", s.SessionKey, s.BranchCode)
	}
	custID := custIDByKey[s.CustomerKey]
	if custID == "" {
		return fmt.Errorf("unknown customer key %s", s.CustomerKey)
	}

	saleType := s.FulfillmentType
	if saleType == "" {
		saleType = "COLLECT_NOW"
	}

	lines := make([]map[string]any, 0, len(s.Lines))
	for _, ln := range s.Lines {
		pid := prodBySKU[ln.SKU]
		if pid == "" {
			return fmt.Errorf("unknown SKU %s", ln.SKU)
		}
		lines = append(lines, map[string]any{
			"product_id":  pid,
			"qty_entered": float64(ln.Quantity),
			"uom_entered": ln.UOM,
		})
	}

	payload := map[string]any{
		"idempotency_key": finworldUUID("sale:" + s.SaleKey),
		"branch_id":       branchID,
		"till_session_id": tillSessID,
		"customer_id":     custID,
		"tender":          s.PaymentMethod,
		"sale_type":       saleType,
		"notes":           s.SaleKey,
		"lines":           lines,
	}
	if saleType == "COD" {
		payload["fulfilment"] = "PICKUP"
	}
	if s.PaymentMethod == "CARD" {
		payload["payment_token"] = "tok_" + s.SaleKey
	}
	if s.TaxReasonCode == "ON_FN_POS_8" {
		payload["relief"] = map[string]any{
			"purchaser_type": "FIRST_NATIONS",
			"declared_use":   "delivery to reserve",
			"fulfilment":     "PICKUP",
			"reason_code":    "ON_FN_POS_8",
		}
	}

	out, err := h.call("sale "+s.SaleKey, http.MethodPost, "/api/v1/pos/sales", payload)
	if err != nil {
		return err
	}
	var saleRes struct {
		OrderID       string `json:"order_id"`
		InvoiceID     string `json:"invoice_id"`
		SubtotalCents int64  `json:"subtotal_cents"`
		TaxCents      int64  `json:"tax_cents"`
		TotalCents    int64  `json:"total_cents"`
	}
	if err := json.Unmarshal(out, &saleRes); err != nil {
		return err
	}
	if saleRes.SubtotalCents != s.SubtotalCents || saleRes.TaxCents != s.TaxCents || saleRes.TotalCents != s.TotalCents {
		return fmt.Errorf("sale %s total mismatch: got (sub=%d tax=%d tot=%d), plan (sub=%d tax=%d tot=%d)",
			s.SaleKey, saleRes.SubtotalCents, saleRes.TaxCents, saleRes.TotalCents,
			s.SubtotalCents, s.TaxCents, s.TotalCents)
	}

	if _, err := h.pool.Exec(ctx, `
		UPDATE invoices
		   SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{finworld_sale_key}', to_jsonb($2::text), true)
		 WHERE id = $1::uuid`, saleRes.InvoiceID, s.SaleKey); err != nil {
		return fmt.Errorf("tag invoice %s: %w", s.SaleKey, err)
	}

	if s.SaleMode == "COD" {
		pbPayload := map[string]any{
			"order_id":        saleRes.OrderID,
			"method":          s.PaymentMethod,
			"amount_cents":    s.TotalCents,
			"till_session_id": tillSessID,
			"idempotency_key": finworldUUID("paybal:" + s.SaleKey),
			"reference":       s.SaleKey,
		}
		if s.PaymentMethod == "CARD" {
			pbPayload["payment_token"] = "tok_pb_" + s.SaleKey
		}
		if _, err := h.call("cod pay-balance "+s.SaleKey, http.MethodPost, "/api/v1/pos/pay-balance", pbPayload); err != nil {
			return err
		}
	}

	if saleType != "COLLECT_NOW" {
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()

		prodIDs := make([]string, 0, len(s.Lines))
		qtys := make([]float64, 0, len(s.Lines))
		for _, ln := range s.Lines {
			prodIDs = append(prodIDs, prodBySKU[ln.SKU])
			qtys = append(qtys, float64(ln.Quantity))
		}
		if err := h.money.inventorySvc.FulfilOrderLinesTx(ctx, tx, saleRes.OrderID, branchID, prodIDs, qtys); err != nil {
			return fmt.Errorf("fulfill non-COLLECT_NOW stock %s: %w", s.SaleKey, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE order_lines SET fulfilled_base = qty_base WHERE order_id = $1::uuid`, saleRes.OrderID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE orders SET status = 'FULFILLED', updated_at = now() WHERE id = $1::uuid`, saleRes.OrderID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		h.drain(ctx)
	}

	return nil
}

func finworldExecutePurchaseReceipt(
	ctx context.Context,
	h *finworldHarness,
	pr *FinWorldPurchaseReceiptEvent,
	prodBySKU, vendorByNo map[string]string,
	expectedDate string,
) error {
	branchID, err := finworldBranchID(pr.BranchCode)
	if err != nil {
		return err
	}
	vendorID := vendorByNo[pr.VendorNo]
	if vendorID == "" {
		return fmt.Errorf("unknown vendor %s", pr.VendorNo)
	}
	poLines := make([]map[string]any, 0, len(pr.Lines))
	for _, ln := range pr.Lines {
		pid := prodBySKU[ln.SKU]
		if pid == "" {
			return fmt.Errorf("unknown SKU %s", ln.SKU)
		}
		poLines = append(poLines, map[string]any{
			"product_id":       pid,
			"description":      ln.SKU,
			"qty_ordered_base": float64(ln.Quantity),
			"uom":              ln.UOM,
			"unit_cost_cents":  ln.UnitCostCents,
		})
	}
	out, err := h.call("create PO "+pr.PurchaseOrderKey, http.MethodPost, "/api/v1/purchasing/pos", map[string]any{
		"vendor_id":   vendorID,
		"branch_id":   branchID,
		"expected_at": expectedDate,
		"notes":       pr.PurchaseReceiptKey,
		"lines":       poLines,
	})
	if err != nil {
		return err
	}
	var poRes struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &poRes); err != nil {
		return err
	}
	if _, err := h.call("place PO "+pr.PurchaseOrderKey, http.MethodPost, "/api/v1/purchasing/pos/"+poRes.ID+"/transition", map[string]any{
		"to": "ORDERED",
	}); err != nil {
		return err
	}
	lRows, err := h.pool.Query(ctx, `SELECT id::text FROM purchasing_po_lines WHERE po_id = $1::uuid ORDER BY line_no`, poRes.ID)
	if err != nil {
		return err
	}
	var lineIDs []string
	for lRows.Next() {
		var lid string
		if err := lRows.Scan(&lid); err != nil {
			lRows.Close()
			return err
		}
		lineIDs = append(lineIDs, lid)
	}
	lRows.Close()

	recLines := make([]map[string]any, 0, len(pr.Lines))
	for i, ln := range pr.Lines {
		recLines = append(recLines, map[string]any{
			"po_line_id": lineIDs[i],
			"qty_base":   float64(ln.Quantity),
		})
	}
	if _, err := h.call("receive PO "+pr.PurchaseReceiptKey, http.MethodPost, "/api/v1/purchasing/pos/"+poRes.ID+"/receive", map[string]any{
		"lines": recLines,
	}); err != nil {
		return err
	}
	return nil
}

func finworldExecuteVendorBill(
	ctx context.Context,
	h *finworldHarness,
	vb *FinWorldVendorBillEvent,
	prodBySKU, vendorByNo map[string]string,
	docTime time.Time,
) error {
	branchID, err := finworldBranchID(vb.BranchCode)
	if err != nil {
		return err
	}
	var poID string
	if err := h.pool.QueryRow(ctx, `SELECT id::text FROM purchasing_pos WHERE notes = $1`, vb.PurchaseReceiptKey).Scan(&poID); err != nil {
		return fmt.Errorf("lookup PO for receipt %s: %w", vb.PurchaseReceiptKey, err)
	}
	poLineRows, err := h.pool.Query(ctx, `SELECT id::text FROM purchasing_po_lines WHERE po_id = $1::uuid ORDER BY line_no`, poID)
	if err != nil {
		return err
	}
	var poLineIDs []string
	for poLineRows.Next() {
		var lid string
		if err := poLineRows.Scan(&lid); err != nil {
			poLineRows.Close()
			return err
		}
		poLineIDs = append(poLineIDs, lid)
	}
	poLineRows.Close()

	billLines := make([]map[string]any, 0, len(vb.Lines))
	for i, ln := range vb.Lines {
		bLine := map[string]any{
			"description":     ln.SKU,
			"qty_billed_base": float64(ln.Quantity),
			"unit_cost_cents": ln.UnitCostCents,
		}
		if i < len(poLineIDs) {
			bLine["po_line_id"] = poLineIDs[i]
		}
		billLines = append(billLines, bLine)
	}

	out, err := h.call("create bill "+vb.VendorBillKey, http.MethodPost, "/api/v1/purchasing/bills", map[string]any{
		"bill_no":   vb.VendorInvoiceNo,
		"po_id":     poID,
		"bill_date": vb.IssueDate,
		"due_date":  vb.DueDate,
		"tax_cents": vb.TaxCents,
		"lines":     billLines,
	})
	if err != nil {
		return err
	}
	var billRes struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &billRes); err != nil {
		return err
	}
	if _, err := h.call("post bill "+vb.VendorBillKey, http.MethodPost, "/api/v1/purchasing/bills/"+billRes.ID+"/post", map[string]any{
		"accept_variance": true,
		"note":            vb.VendorBillKey,
	}); err != nil {
		return err
	}
	if err := h.drain(ctx); err != nil {
		return err
	}

	if vb.PaymentDeferred {
		// Net 30 terms: the bill stays open across the month end and the plan settles it with a
		// VENDOR_PAYMENT event on the next month's first trading day.
		return nil
	}
	return finworldPostSupplierCheque(ctx, h, branchID, vb.VendorBillKey, vb.VendorInvoiceNo, vb.TotalCents, docTime)
}

// finworldPostSupplierCheque settles a vendor bill through purchasing's own PayBill (W2-9a round 2): a PAID
// instalment the AP aging counts, and the DR 2100 / CR 1000 journal PayBill posts at the cheque's own date. The
// world's key makes a re-run of the day return the first payment and write nothing.
func finworldPostSupplierCheque(ctx context.Context, h *finworldHarness, branchID, billKey, invoiceNo string, totalCents int64, docTime time.Time) error {
	var billID string
	if err := h.pool.QueryRow(ctx, `SELECT id::text FROM purchasing_bills WHERE bill_no = $1 AND branch_id = $2::uuid`, invoiceNo, branchID).Scan(&billID); err != nil {
		return fmt.Errorf("lookup vendor bill %s (%s): %w", billKey, invoiceNo, err)
	}
	sctx := staffcontext.ContextWithStaff(ctx, staffcontext.StaffContext{
		UserID: finworldStaffUser,
		Scope:  staffcontext.ScopeGlobal,
		Status: staffcontext.StatusActive,
	})
	if _, err := h.ops.purchasingSvc.PayBill(sctx, billID, purchasing.PayBillRequest{
		AmountCents:    totalCents,
		PaidAt:         docTime,
		Note:           "Supplier cheque " + billKey + " (" + invoiceNo + ")",
		IdempotencyKey: "finworld:vpay:" + invoiceNo,
	}); err != nil {
		return fmt.Errorf("pay vendor bill %s: %w", billKey, err)
	}
	return nil
}

// finworldExecuteVendorPayment settles a bill the plan left open across a month end: the supplier
// cheque clears on this day, the same day the statement carries it.
func finworldExecuteVendorPayment(ctx context.Context, h *finworldHarness, vp *FinWorldVendorPaymentEvent, docTime time.Time) error {
	branchID, err := finworldBranchID(vp.BranchCode)
	if err != nil {
		return err
	}
	return finworldPostSupplierCheque(ctx, h, branchID, vp.VendorBillKey, vp.VendorInvoiceNo, vp.AmountCents, docTime)
}

// finworldExecuteLandedCost posts one plan landed-cost event through the production landed-cost
// routes on the shared mux: POST /api/v1/landed-costs, then POST /api/v1/landed-costs/{id}/apply.
// A VOIDED event instead posts POST /api/v1/landed-costs/{id}/void for the document the plan
// names. Every route is RequireManager, and the driver posts them with the branch_manager token it
// already links for approvals. The world key rides po_ref at create so a later void (a fresh
// process, a later simulated day) resolves the applied document.
func finworldExecuteLandedCost(ctx context.Context, h *finworldHarness, lc *FinWorldLandedCostEvent, vendorByNo map[string]string) error {
	branchID, err := finworldBranchID(lc.BranchCode)
	if err != nil {
		return err
	}
	if lc.Status == "VOIDED" {
		var applyID string
		if err := h.pool.QueryRow(ctx, `SELECT id::text FROM landed_costs WHERE po_ref = $1`, lc.VoidOfLandedCostKey).Scan(&applyID); err != nil {
			return fmt.Errorf("lookup applied landed cost %s: %w", lc.VoidOfLandedCostKey, err)
		}
		_, err := h.callWithToken(h.managerToken, "void landed cost "+lc.LandedCostKey, http.MethodPost, "/api/v1/landed-costs/"+applyID+"/void", nil)
		return err
	}
	var poID string
	// The landed cost attaches to the RECEIVING DOCUMENT (the 0011 purchase_orders row the
	// purchasing Receive wrote for this receipt), whose po_lines carry the received_base the
	// allocation reads; purchasing_pos is the order desk's own document and no landed-cost input.
	if err := h.pool.QueryRow(ctx, `
		SELECT d.id::text
		  FROM purchase_orders d
		  JOIN purchasing_pos p ON p.id::text = d.metadata->>'purchasing_po_id'
		 WHERE p.notes = $1`, lc.PurchaseReceiptKey).Scan(&poID); err != nil {
		return fmt.Errorf("lookup receiving document for receipt %s: %w", lc.PurchaseReceiptKey, err)
	}
	payeeVendorID := vendorByNo[lc.PayeeVendorNo]
	if payeeVendorID == "" {
		return fmt.Errorf("landed cost %s names unknown payee vendor %s", lc.LandedCostKey, lc.PayeeVendorNo)
	}
	out, err := h.callWithToken(h.managerToken, "create landed cost "+lc.LandedCostKey, http.MethodPost, "/api/v1/landed-costs", map[string]any{
		"po_id":                poID,
		"po_ref":               lc.LandedCostKey,
		"payee_vendor_id":      payeeVendorID,
		"branch_id":            branchID,
		"currency":             lc.Currency,
		"fx_rate":              float64(lc.FXRateBps) / 10000,
		"freight_cents":        lc.FreightCents,
		"duty_cents":           lc.DutyCents,
		"handling_cents":       lc.HandlingCents,
		"allocation_basis":     lc.AllocationBasis,
		"exclude_special_buys": lc.ExcludeSpecialBuys,
	})
	if err != nil {
		return err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		return fmt.Errorf("create landed cost %s: %w", lc.LandedCostKey, err)
	}
	_, err = h.callWithToken(h.managerToken, "apply landed cost "+lc.LandedCostKey, http.MethodPost, "/api/v1/landed-costs/"+created.ID+"/apply", nil)
	return err
}

func finworldExecuteStockTransfer(
	ctx context.Context,
	h *finworldHarness,
	st *FinWorldStockTransferEvent, prodBySKU map[string]string,
) error {
	fromBranchID, err := finworldBranchID(st.FromBranchCode)
	if err != nil {
		return err
	}
	toBranchID, err := finworldBranchID(st.ToBranchCode)
	if err != nil {
		return err
	}
	lines := make([]map[string]any, 0, len(st.Lines))
	for _, ln := range st.Lines {
		pid := prodBySKU[ln.SKU]
		if pid == "" {
			return fmt.Errorf("unknown SKU %s", ln.SKU)
		}
		lines = append(lines, map[string]any{
			"product_id": pid,
			"qty_base":   float64(ln.Quantity),
		})
	}
	out, err := h.call("create transfer "+st.TransferKey, http.MethodPost, "/api/v1/inventory/transfers", map[string]any{
		"from_branch_id": fromBranchID,
		"to_branch_id":   toBranchID,
		"notes":          st.TransferKey,
		"lines":          lines,
	})
	if err != nil {
		return err
	}
	var trRes struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &trRes); err != nil {
		return err
	}
	if _, err := h.call("ship transfer "+st.TransferKey, http.MethodPost, "/api/v1/inventory/transfers/"+trRes.ID+"/ship", map[string]any{}); err != nil {
		return err
	}
	if err := h.drain(ctx); err != nil {
		return err
	}
	if _, err := h.call("receive transfer "+st.TransferKey, http.MethodPost, "/api/v1/inventory/transfers/"+trRes.ID+"/receive", map[string]any{}); err != nil {
		return err
	}
	return nil
}

func finworldExecuteReturn(
	ctx context.Context,
	h *finworldHarness,
	ret *FinWorldReturnEvent,
	prodBySKU, tillSessByKey map[string]string,
) error {
	var orderID string
	if err := h.pool.QueryRow(ctx, `
		SELECT order_id::text
		  FROM invoices
		 WHERE metadata->>'finworld_sale_key' = $1`, ret.OriginalSaleKey).Scan(&orderID); err != nil {
		return fmt.Errorf("lookup original sale %s: %w", ret.OriginalSaleKey, err)
	}

	retLines := make([]map[string]any, 0, len(ret.Lines))
	for _, ln := range ret.Lines {
		pid := prodBySKU[ln.SKU]
		var orderLineID string
		if err := h.pool.QueryRow(ctx, `
			SELECT id::text FROM order_lines
			 WHERE order_id = $1::uuid AND product_id = $2::uuid
			 LIMIT 1`, orderID, pid).Scan(&orderLineID); err != nil {
			return fmt.Errorf("lookup order_line for return %s (%s): %w", ret.ReturnKey, ln.SKU, err)
		}
		retLines = append(retLines, map[string]any{
			"order_line_id": orderLineID,
			"qty_base":      float64(ln.Quantity),
			"disposition":   "RESTOCK",
		})
	}

	payload := map[string]any{
		"idempotency_key": finworldUUID("ret:" + ret.ReturnKey),
		"order_id":        orderID,
		"refund_method":   ret.RefundMethod,
		"reason":          "Customer return " + ret.ReturnKey,
		"restock":         true,
		"lines":           retLines,
	}
	if ret.SessionKey != "" {
		payload["till_session_id"] = tillSessByKey[ret.SessionKey]
	}

	if ret.TotalCents > 25000 {
		_, callErr := h.callWithToken(h.managerToken, "return "+ret.ReturnKey, http.MethodPost, "/api/v1/pos/returns", payload)
		return callErr
	}
	_, callErr := h.call("return "+ret.ReturnKey, http.MethodPost, "/api/v1/pos/returns", payload)
	return callErr
}

func finworldExecuteCreditMemo(
	ctx context.Context,
	h *finworldHarness,
	cm *FinWorldCreditMemoEvent,
	custIDByKey map[string]string,
) error {
	branchID, err := finworldBranchID(cm.BranchCode)
	if err != nil {
		return err
	}
	custID := custIDByKey[cm.CustomerKey]
	if custID == "" {
		return fmt.Errorf("unknown customer key %s", cm.CustomerKey)
	}
	var targetInvID string
	if err := h.pool.QueryRow(ctx, `
		SELECT id::text FROM invoices
		 WHERE metadata->>'finworld_sale_key' = $1`, cm.OriginalSaleKey).Scan(&targetInvID); err != nil {
		return fmt.Errorf("lookup target invoice for %s: %w", cm.OriginalSaleKey, err)
	}

	out, err := h.call("credit memo "+cm.CreditMemoKey, http.MethodPost, "/api/v1/ar/credit-memos", map[string]any{
		"customer_id":     custID,
		"branch_id":       branchID,
		"amount_cents":    cm.SubtotalCents,
		"reason":          cm.Description,
		"idempotency_key": cm.CreditMemoKey,
	})
	if err != nil {
		return err
	}
	var cmRes struct {
		CreditMemoID string `json:"credit_memo_id"`
		TotalCents   int64  `json:"total_cents"`
	}
	if err := json.Unmarshal(out, &cmRes); err != nil {
		return err
	}
	if err := h.drain(ctx); err != nil {
		return err
	}

	if _, err := h.call("apply credit memo "+cm.CreditMemoKey, http.MethodPost, "/api/v1/ar/credits/apply", map[string]any{
		"customer_id":     custID,
		"credit_kind":     "MEMO",
		"credit_id":       cmRes.CreditMemoID,
		"invoice_id":      targetInvID,
		"amount_cents":    cm.TotalCents,
		"reason":          cm.Description,
		"idempotency_key": "apply:" + cm.CreditMemoKey,
	}); err != nil {
		return err
	}
	return nil
}

func finworldExecuteAccountReceipt(
	ctx context.Context,
	h *finworldHarness,
	ar *FinWorldAccountReceiptEvent,
	custIDByKey map[string]string,
) error {
	custID := custIDByKey[ar.CustomerKey]
	if custID == "" {
		return fmt.Errorf("unknown customer key %s", ar.CustomerKey)
	}
	invoiceIDs := make([]string, 0, len(ar.Allocations))
	for _, alloc := range ar.Allocations {
		var invID string
		if err := h.pool.QueryRow(ctx, `
			SELECT id::text FROM invoices WHERE metadata->>'finworld_sale_key' = $1`, alloc.SaleKey).Scan(&invID); err != nil {
			return fmt.Errorf("lookup invoice for sale %s: %w", alloc.SaleKey, err)
		}
		invoiceIDs = append(invoiceIDs, invID)
	}
	if _, err := h.call("account receipt "+ar.ReceiptKey, http.MethodPost, "/api/v1/ar/payments/apply", map[string]any{
		"customer_id":     custID,
		"method":          ar.PaymentMethod,
		"reference":       ar.PaymentKey,
		"idempotency_key": ar.ReceiptKey,
		"amount_cents":    ar.AmountCents,
		"invoice_ids":     invoiceIDs,
	}); err != nil {
		return err
	}
	branchID, err := finworldBranchID(ar.BranchCode)
	if err != nil {
		return err
	}
	if _, err := h.pool.Exec(ctx, `
		UPDATE payments SET branch_id = $2::uuid WHERE idempotency_key = $1`, ar.ReceiptKey, branchID); err != nil {
		return err
	}
	return nil
}

func finworldExecuteNSFBounce(
	ctx context.Context,
	h *finworldHarness,
	nsf *FinWorldNSFBounceEvent,
	docTime time.Time,
) error {
	branchID, err := finworldBranchID(nsf.BranchCode)
	if err != nil {
		return err
	}
	var paymentID string
	if err := h.pool.QueryRow(ctx, `
		SELECT id::text FROM payments WHERE idempotency_key = $1`, nsf.OriginalReceiptKey).Scan(&paymentID); err != nil {
		return fmt.Errorf("lookup payment for NSF %s (receipt %s): %w", nsf.BounceKey, nsf.OriginalReceiptKey, err)
	}
	if _, err := h.call("nsf "+nsf.BounceKey, http.MethodPost, "/api/v1/ar/payments/"+paymentID+"/nsf", map[string]any{
		"reason": "NSF returned cheque " + nsf.BounceKey,
	}); err != nil {
		return err
	}
	if err := h.drain(ctx); err != nil {
		return err
	}
	if nsf.NSFFeeCents > 0 {
		if _, err := h.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
			Source:         "MANUAL",
			IdempotencyKey: "finworld:nsf-bank-fee:" + nsf.BounceKey,
			Memo:           "Bank returned-item charge " + nsf.BounceKey,
			BranchID:       branchID,
			DocumentDate:   docTime,
			Lines: []gl.ExternalLine{
				{AccountCode: "6300", DebitCents: nsf.NSFFeeCents, Memo: "Bank NSF charge " + nsf.BounceKey},
				{AccountCode: "1000", CreditCents: nsf.NSFFeeCents, Memo: "Bank NSF charge " + nsf.BounceKey},
			},
		}); err != nil {
			return fmt.Errorf("post NSF bank charge %s: %w", nsf.BounceKey, err)
		}
	}
	return nil
}

func finworldExecuteManualJournal(
	ctx context.Context,
	h *finworldHarness,
	mj *FinWorldManualJournalEvent,
) error {
	branchID, err := finworldBranchID(mj.BranchCode)
	if err != nil {
		return err
	}
	lines := make([]map[string]any, 0, len(mj.Lines))
	for _, ln := range mj.Lines {
		lines = append(lines, map[string]any{
			"account_code": ln.AccountCode,
			"debit_cents":  ln.DebitCents,
			"credit_cents": ln.CreditCents,
			"memo":         ln.Memo,
		})
	}
	_, err = h.call("manual journal "+mj.JournalKey, http.MethodPost, "/api/v1/gl/journals", map[string]any{
		"source":          "MANUAL",
		"memo":            mj.Description,
		"branch_id":       branchID,
		"idempotency_key": mj.JournalKey,
		"lines":           lines,
	})
	return err
}

func finworldExecuteTillCount(
	ctx context.Context,
	h *finworldHarness,
	tc *FinWorldTillCountEvent,
	tillSessByKey map[string]string,
) error {
	sessID := tillSessByKey[tc.SessionKey]
	if sessID == "" {
		sessID = tillSessByKey[tc.TillKey]
	}
	if sessID == "" {
		return fmt.Errorf("unknown till session for %s", tc.SessionKey)
	}
	out, err := h.call("zreport "+tc.SessionKey, http.MethodGet, "/api/v1/till/sessions/"+sessID+"/zreport", nil)
	if err != nil {
		return err
	}
	var zrep struct {
		PerTender []struct {
			Method        string `json:"method"`
			ExpectedCents int64  `json:"expected_cents"`
		} `json:"per_tender"`
		Cash struct {
			ExpectedCents     int64 `json:"expected_cents"`
			OpeningFloatCents int64 `json:"opening_float_cents"`
		} `json:"cash"`
	}
	if err := json.Unmarshal(out, &zrep); err != nil {
		return err
	}
	tenders := make([]map[string]any, 0, len(zrep.PerTender))
	for _, tr := range zrep.PerTender {
		actual := tr.ExpectedCents
		if tr.Method == "CASH" {
			actual += tc.VarianceCents
		}
		tenders = append(tenders, map[string]any{
			"method":       tr.Method,
			"actual_cents": actual,
		})
	}
	if len(tenders) == 0 {
		tenders = append(tenders, map[string]any{
			"method":       "CASH",
			"actual_cents": zrep.Cash.ExpectedCents + tc.VarianceCents,
		})
	}
	_, err = h.call("count till "+tc.SessionKey, http.MethodPost, "/api/v1/till/sessions/"+sessID+"/count", map[string]any{
		"tenders":             tenders,
		"closing_float_cents": zrep.Cash.OpeningFloatCents,
		"variance_notes":      "finworld end-of-day count",
	})
	return err
}

func finworldExecuteTillComplete(
	ctx context.Context,
	h *finworldHarness,
	tcomp *FinWorldTillCompleteEvent,
	tillSessByKey map[string]string,
) error {
	sessID := tillSessByKey[tcomp.SessionKey]
	if sessID == "" {
		sessID = tillSessByKey[tcomp.TillKey]
	}
	if sessID == "" {
		return fmt.Errorf("unknown till session for %s", tcomp.SessionKey)
	}
	_, err := h.call("complete till "+tcomp.SessionKey, http.MethodPost, "/api/v1/till/sessions/"+sessID+"/complete", map[string]any{
		"override_notes": "finworld manager sign-off",
	})
	return err
}

func finworldExecuteLodgement(
	ctx context.Context,
	h *finworldHarness,
	lodg *FinWorldLodgementEvent,
) error {
	if lodg.TotalAmountCents <= 0 {
		return nil
	}
	branchID, err := finworldBranchID(lodg.BranchCode)
	if err != nil {
		return err
	}
	out, err := h.call("open safe "+lodg.LodgementKey, http.MethodPost, "/api/v1/till/sessions", map[string]any{
		"branch_id":   branchID,
		"station":     "SAFE-" + lodg.LodgementKey,
		"float_cents": lodg.TotalAmountCents,
	})
	if err != nil {
		return err
	}
	var safeSess struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &safeSess); err != nil {
		return err
	}
	if _, err := h.call("lodgement "+lodg.LodgementKey, http.MethodPost, "/api/v1/till/sessions/"+safeSess.ID+"/movements", map[string]any{
		"movement_type": "LODGEMENT",
		"method":        "CASH",
		"amount_cents":  lodg.TotalAmountCents,
		"reason":        "Bank deposit " + lodg.BankDepositRef,
		"reference":     lodg.BankDepositRef,
	}); err != nil {
		return err
	}
	if _, err := h.call("count safe "+lodg.LodgementKey, http.MethodPost, "/api/v1/till/sessions/"+safeSess.ID+"/count", map[string]any{
		"tenders": []map[string]any{
			{"method": "CASH", "actual_cents": int64(0)},
			{"method": "CARD", "actual_cents": int64(0)},
			{"method": "CHEQUE", "actual_cents": int64(0)},
		},
		"closing_float_cents": int64(0),
	}); err != nil {
		return err
	}
	if _, err := h.call("complete safe "+lodg.LodgementKey, http.MethodPost, "/api/v1/till/sessions/"+safeSess.ID+"/complete", map[string]any{
		"override_notes": "safe lodgement complete",
	}); err != nil {
		return err
	}

	if _, err := h.pool.Exec(ctx, `
		UPDATE payments
		   SET status = 'DEPOSITED', updated_at = now()
		 WHERE branch_id = $1::uuid
		   AND method = 'CASH'
		   AND status = 'RECEIVED'`, branchID); err != nil {
		return fmt.Errorf("mark lodged cash DEPOSITED: %w", err)
	}
	if len(lodg.ChequeReceiptKeys) > 0 {
		if _, err := h.pool.Exec(ctx, `
			UPDATE payments
			   SET status = 'DEPOSITED', updated_at = now()
			 WHERE idempotency_key = ANY($1)
			   AND status = 'RECEIVED'`, lodg.ChequeReceiptKeys); err != nil {
			return fmt.Errorf("mark lodged cheques DEPOSITED: %w", err)
		}
	}
	return nil
}

func finworldExecuteCardSettlement(
	ctx context.Context,
	h *finworldHarness,
	cs *FinWorldCardSettlementEvent,
	docTime time.Time,
) error {
	branchID, err := finworldBranchID(cs.BranchCode)
	if err != nil {
		return err
	}
	netCardClearing := cs.GrossCardCents - cs.RefundCardCents
	if netCardClearing <= 0 {
		return nil
	}
	if _, err := h.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source:         "MANUAL",
		IdempotencyKey: "finworld:card-settle:" + cs.StatementRef,
		Memo:           "Card batch settlement " + cs.StatementRef,
		BranchID:       branchID,
		DocumentDate:   docTime,
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: cs.NetSettledCents, Memo: "Card settlement net deposit " + cs.StatementRef},
			{AccountCode: "6750", DebitCents: cs.ProcessorFeeCents, Memo: "Card processor fee " + cs.StatementRef},
			{AccountCode: "1010", CreditCents: netCardClearing, Memo: "Clear card batch " + cs.StatementRef},
		},
	}); err != nil {
		return fmt.Errorf("post card settlement %s: %w", cs.SettlementKey, err)
	}
	if _, err := h.pool.Exec(ctx, `
		UPDATE payments
		   SET status = 'DEPOSITED', updated_at = now()
		 WHERE branch_id = $1::uuid
		   AND method = 'CARD'
		   AND status = 'RECEIVED'`, branchID); err != nil {
		return fmt.Errorf("mark settled cards DEPOSITED: %w", err)
	}
	return nil
}

func finworldExecuteBankStatement(
	ctx context.Context,
	h *finworldHarness,
	plan *FinWorldPlanT,
	bs *FinWorldBankStatementEvent,
	docTime time.Time,
) error {
	// 1. Post the monthly $35.00 bank service fee journal on GL 6300 / 1000 before importing/reconciling
	for _, ln := range bs.Lines {
		if ln.Kind == "BANK_FEE" && strings.HasPrefix(ln.Reference, "FEE-") && ln.AmountCents < 0 {
			if _, err := h.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
				Source:         "MANUAL",
				IdempotencyKey: "finworld:bank-fee:" + ln.Reference,
				Memo:           ln.Description,
				BranchID:       finworldBranchTRT,
				DocumentDate:   docTime,
				Lines: []gl.ExternalLine{
					{AccountCode: "6300", DebitCents: -ln.AmountCents, Memo: ln.Description},
					{AccountCode: "1000", CreditCents: -ln.AmountCents, Memo: ln.Description},
				},
			}); err != nil {
				return fmt.Errorf("post monthly bank fee %s: %w", ln.Reference, err)
			}
		}
	}

	// 2. Build CSV for POST /api/v1/bankrec/imports (Date,Description,Amount,Reference)
	var csvBuf strings.Builder
	csvBuf.WriteString("Date,Description,Amount,Reference\n")
	for _, ln := range bs.Lines {
		if ln.AmountCents == 0 {
			continue
		}
		sign := ""
		absCents := ln.AmountCents
		if absCents < 0 {
			sign = "-"
			absCents = -absCents
		}
		fmt.Fprintf(&csvBuf, "%s,%s,%s%d.%02d,%s\n",
			ln.ValueDate, strings.ReplaceAll(ln.Description, ",", " "), sign, absCents/100, absCents%100, ln.Reference)
	}

	if _, err := h.call("import statement "+bs.StatementKey, http.MethodPost, "/api/v1/bankrec/imports", map[string]any{
		"bank_account_id": h.bankAccountID,
		"profile_code":    "rbc-default",
		"filename":        bs.StatementKey + ".csv",
		"content":         csvBuf.String(),
	}); err != nil {
		return err
	}
	if err := h.drain(ctx); err != nil {
		return err
	}

	// 3. Run auto-match first, then manual match for any remaining UNMATCHED lines in this statement
	_, _ = h.call("auto match "+bs.StatementKey, http.MethodPost, "/api/v1/bankrec/match", map[string]any{
		"mode":            "auto",
		"bank_account_id": h.bankAccountID,
	})
	if err := h.drain(ctx); err != nil {
		return err
	}

	sRows, err := h.pool.Query(ctx, `
		SELECT id::text, COALESCE(reference, ''), amount_cents
		  FROM bank_statement_lines
		 WHERE bank_account_id = $1::uuid
		   AND status = 'UNMATCHED'
		 ORDER BY txn_date, id`,
		h.bankAccountID)
	if err != nil {
		return err
	}
	type unmatchedLine struct {
		id     string
		ref    string
		amount int64
	}
	var unmatched []unmatchedLine
	for sRows.Next() {
		var u unmatchedLine
		if err := sRows.Scan(&u.id, &u.ref, &u.amount); err != nil {
			sRows.Close()
			return err
		}
		unmatched = append(unmatched, u)
	}
	sRows.Close()

	for _, u := range unmatched {
		targetType, targetRefID, err := finworldResolveBankMatchTarget(ctx, h.pool, u.ref)
		if err != nil {
			return fmt.Errorf("resolve bank match for %s (amount=%d): %w", u.ref, u.amount, err)
		}
		if _, err := h.call("match "+u.ref, http.MethodPost, "/api/v1/bankrec/match", map[string]any{
			"mode":              "manual",
			"bank_account_id":   h.bankAccountID,
			"statement_line_id": u.id,
			"target_type":       targetType,
			"target_ref_id":     targetRefID,
			"amount_cents":      u.amount,
		}); err != nil {
			return err
		}
	}
	if err := h.drain(ctx); err != nil {
		return err
	}

	// 4. For closed months (before the open anchor month), open and sign off the reconciliation at $0.00 variance!
	if bs.YearMonth.Compare(plan.Anchor) < 0 {
		loc, _ := time.LoadLocation(plan.HouseZone)
		nextMonth := bs.YearMonth.AddMonths(1)
		nextMonthStart := time.Date(nextMonth.Year, time.Month(nextMonth.Month), 1, 0, 5, 0, 0, loc)
		bankrecSvc := bankrec.NewService(bankrec.NewRepository(h.pool)).
			WithPayments(till.NewRepository(h.pool)).
			WithLodgements(till.NewRepository(h.pool)).
			WithJournalPoster(glPosterAdapter{svc: h.money.glSvc}).
			WithGLBalances(glBalanceAdapter{svc: h.money.glSvc}).
			WithJournals(h.money.glSvc).
			WithClock(func() time.Time { return nextMonthStart })
		rec, err := bankrecSvc.OpenReconciliation(ctx, bankrec.ReconInput{
			BankAccountID:         h.bankAccountID,
			Year:                  bs.YearMonth.Year,
			Month:                 bs.YearMonth.Month,
			StatementBalanceCents: bs.ClosingBalanceCents,
		})
		if err != nil {
			return fmt.Errorf("OpenReconciliation %s: %w", bs.YearMonth.String(), err)
		}
		if rec.VarianceCents != 0 {
			return fmt.Errorf("bank reconciliation %s variance is %d cents (stmt=%d dit=%d outChq=%d gl=%d), want 0",
				bs.YearMonth.String(), rec.VarianceCents, rec.StatementBalanceCents,
				rec.DepositsInTransitCents, rec.OutstandingChequesCents, rec.GLBalanceCents)
		}
		if _, err := bankrecSvc.CompleteReconciliation(ctx, rec.ID, finworldStaffUser); err != nil {
			return fmt.Errorf("CompleteReconciliation %s: %w", bs.YearMonth.String(), err)
		}
		if err := h.drain(ctx); err != nil {
			return err
		}
	}
	return nil
}

func finworldResolveBankMatchTarget(ctx context.Context, pool *pgxpool.Pool, ref string) (string, string, error) {
	switch {
	case strings.HasPrefix(ref, "DEP-"):
		var movID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM till_movements WHERE movement_type = 'LODGEMENT' AND reference = $1`, ref).Scan(&movID); err != nil {
			return "", "", fmt.Errorf("lookup lodgement %s: %w", ref, err)
		}
		return "LODGEMENT", movID, nil

	case strings.HasPrefix(ref, "CARD-"):
		var jid string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, "finworld:card-settle:"+ref).Scan(&jid); err != nil {
			return "", "", fmt.Errorf("lookup card settlement journal %s: %w", ref, err)
		}
		return "JOURNAL", jid, nil

	case strings.HasPrefix(ref, "pay-"):
		var payID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM payments WHERE metadata->>'reference' = $1`, ref).Scan(&payID); err != nil {
			return "", "", fmt.Errorf("lookup EFT payment %s: %w", ref, err)
		}
		return "PAYMENT", payID, nil

	case strings.HasPrefix(ref, "VINV-"):
		var jid string
		if err := pool.QueryRow(ctx, `SELECT j.id::text FROM gl_journals j JOIN purchasing_bills b ON b.id = j.source_ref_id WHERE b.bill_no = $1 AND j.source = 'PAYMENT'`, ref).Scan(&jid); err != nil {
			return "", "", fmt.Errorf("lookup vendor payment journal %s: %w", ref, err)
		}
		return "JOURNAL", jid, nil

	case strings.HasPrefix(ref, "FEE-"):
		var jid string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, "finworld:bank-fee:"+ref).Scan(&jid); err != nil {
			return "", "", fmt.Errorf("lookup bank fee journal %s: %w", ref, err)
		}
		return "JOURNAL", jid, nil

	case strings.HasPrefix(ref, "NSF-FEE-"):
		bounceKey := strings.TrimPrefix(ref, "NSF-FEE-")
		var jid string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, "finworld:nsf-bank-fee:"+bounceKey).Scan(&jid); err != nil {
			return "", "", fmt.Errorf("lookup NSF fee journal %s: %w", ref, err)
		}
		return "JOURNAL", jid, nil

	case strings.HasPrefix(ref, "rcpt-"):
		var jid string
		err := pool.QueryRow(ctx, `
			SELECT j.id::text
			  FROM gl_journals j
			  JOIN payments p ON j.idempotency_key = 'payment.bounced:' || p.id::text
			 WHERE p.idempotency_key = $1`, ref).Scan(&jid)
		if err != nil {
			return "", "", fmt.Errorf("lookup payment.bounced journal for %s: %w", ref, err)
		}
		return "JOURNAL", jid, nil

	default:
		return "", "", fmt.Errorf("unknown bank statement line reference %q", ref)
	}
}

type finworldPosTaxAdapter struct{ svc *tax.Service }

func (a finworldPosTaxAdapter) AssessSale(ctx context.Context, in pos.TaxQuoteInput) (pos.QuoteResult, error) {
	q, err := a.svc.AssessSale(ctx, tax.SaleAssessment{
		BranchID:         in.BranchID,
		SubtotalCents:    in.SubtotalCents,
		ReasonCode:       in.ReasonCode,
		EvidenceAttached: in.ReasonCode == FinWorldTaxReasonReliefFN8,
	})
	if err != nil {
		var unk tax.UnknownReasonError
		if errors.As(err, &unk) {
			return pos.QuoteResult{}, fmt.Errorf("%w: %s", pos.ErrUnknownTaxReason, unk.Error())
		}
		if errors.Is(err, tax.ErrNoStandardRate) {
			return pos.QuoteResult{}, pos.ErrTaxRailUnavailable
		}
		return pos.QuoteResult{}, err
	}
	return pos.QuoteResult{
		TaxCents:        q.TaxCents,
		RateBps:         q.RateBps,
		OnCreditCents:   0,
		ReasonCode:      q.ReasonCode,
		Jurisdiction:    q.Jurisdiction,
		StandardRateBps: q.StandardRateBps,
	}, nil
}

func finworldExecutePeriodClose(
	ctx context.Context,
	h *finworldHarness,
	pc *FinWorldPeriodCloseEvent,
	loc *time.Location,
) error {
	var periodID string
	if err := h.pool.QueryRow(ctx, `
		SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = $2`,
		pc.YearMonth.Year, pc.YearMonth.Month).Scan(&periodID); err != nil {
		return fmt.Errorf("lookup fiscal_period %s: %w", pc.YearMonth.String(), err)
	}
	nextMonth := pc.YearMonth.AddMonths(1)
	nextMonthStart := time.Date(nextMonth.Year, time.Month(nextMonth.Month), 1, 0, 5, 0, 0, loc)
	prevShift, hadPrev := os.LookupEnv("HH_CLOCK_SHIFT_SECONDS")
	prevSec, _ := strconv.ParseInt(strings.TrimSpace(prevShift), 10, 64)
	realNow := time.Now().Unix() - prevSec
	_ = os.Setenv("HH_CLOCK_SHIFT_SECONDS", strconv.FormatInt(nextMonthStart.Unix()-realNow, 10))
	prevToken := h.token
	if tok, err := h.minter.MintAt(finworldStaffSub, 12*time.Hour, time.Now()); err == nil {
		h.token = tok
	}
	defer func() {
		h.token = prevToken
		if hadPrev {
			_ = os.Setenv("HH_CLOCK_SHIFT_SECONDS", prevShift)
		} else {
			_ = os.Unsetenv("HH_CLOCK_SHIFT_SECONDS")
		}
	}()

	_, err := h.call("close period "+pc.YearMonth.String(), http.MethodPost, "/api/v1/gl/periods/"+periodID+"/close", map[string]any{
		"reason": "FinWorld month-end close " + pc.YearMonth.String(),
	})
	return err
}

func finworldExecuteYearClose(
	ctx context.Context,
	h *finworldHarness,
	yc *FinWorldYearCloseEvent,
	loc *time.Location,
) error {
	nextYearStart := time.Date(yc.FiscalYear+1, time.January, 1, 0, 5, 0, 0, loc)
	prevShift, hadPrev := os.LookupEnv("HH_CLOCK_SHIFT_SECONDS")
	prevSec, _ := strconv.ParseInt(strings.TrimSpace(prevShift), 10, 64)
	realNow := time.Now().Unix() - prevSec
	_ = os.Setenv("HH_CLOCK_SHIFT_SECONDS", strconv.FormatInt(nextYearStart.Unix()-realNow, 10))
	prevToken := h.token
	if tok, err := h.minter.MintAt(finworldStaffSub, 12*time.Hour, time.Now()); err == nil {
		h.token = tok
	}
	defer func() {
		h.token = prevToken
		if hadPrev {
			_ = os.Setenv("HH_CLOCK_SHIFT_SECONDS", prevShift)
		} else {
			_ = os.Unsetenv("HH_CLOCK_SHIFT_SECONDS")
		}
	}()

	if _, err := h.call("close year "+yc.YearCloseKey, http.MethodPost, "/api/v1/gl/periods/close-year", map[string]any{
		"year":   yc.FiscalYear,
		"reason": "FinWorld fiscal year-end close",
	}); err != nil {
		return err
	}
	if _, err := h.money.glSvc.EnsureNextFiscalYear(ctx); err != nil {
		return fmt.Errorf("EnsureNextFiscalYear after year close: %w", err)
	}
	return nil
}

var _ = pgx.ErrNoRows
var _ = staffctx.ScopeGlobal
