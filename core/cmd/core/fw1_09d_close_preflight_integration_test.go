//go:build integration

package main

// fw1_09d_close_preflight_integration_test.go, FW1-09d: a month close passes its pre flight on the
// PRODUCTION wiring.
//
// The leg is a COMPOSITION leg: the served routes over one mux and the real money spine (wireMoney,
// wireOperations and wireTops, exactly as the binary wires them), with no test double anywhere on the
// money path. The month carries stock on hand (the Ontario Dibbits seed's branches) and the GL 1200
// balance that matches it, so the close pre flight's stock tie has something true to read, and the AR
// side ties too (the seeded open invoice against its opening journal).
//
// THE RED (FROM-PRINCIPAL 243): the pre flight asks the stock seam for the CONSOLIDATED stock value (an
// empty branch id: the pre flight reads the company as a whole), and the composition root's own adapter
// hands that empty id straight to the inventory snapshot, whose query filters on branch_id = $1. An empty
// id is not a uuid, so the read fails with 22P02, the pre flight names a stock ledger nobody could read,
// and the month close answers 409 on a month whose stock is on hand and in tie.
//
// ROUND 2's red (S5's read, P2): the adapter multiplied a float64 quantity by the cost in Go and
// truncated the product, so one row at 0.29 units and a 100 cent cost (exact value 29 cents; the float
// product evaluates 28.999999999999996) landed a cent below its own figure, and a month whose books tie
// to the cent was refused by -$0.01. TestFW109DStockTieSurvivesAFractionalRow plants that row.
//
//	Run: DATABASE_URL='postgres://.../integration_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run FW109D -count=1 -v

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The org the leg's branches carry. The seed runs before any org exists, so the branches get one here,
// exactly as the walk's fixture does.
const (
	fw109dOrgID   = "88a20000-0000-4000-8000-0000000000d9"
	fw109dOrgSlug = "fw1-09d-close"
)

// fw109dWriter routes the wired services' logs into the test log, so a refused close names its cause
// without printing outside the test run.
type fw109dWriter struct{ t *testing.T }

func (w fw109dWriter) Write(b []byte) (int, error) {
	w.t.Logf("%s", strings.TrimRight(string(b), "\n"))
	return len(b), nil
}

// fw109dBranchAmount is one branch's opening figure: the branch and the cents it carries.
type fw109dBranchAmount struct {
	branch string
	cents  int64
}

// TestFW109DClosePreflightPassesOnTheProductionWiring closes a month whose stock is on hand and in tie,
// through the served route, on the production wiring, and expects the close to seal the period.
func TestFW109DClosePreflightPassesOnTheProductionWiring(t *testing.T) {
	fw109dCloseLeg(t, "fw109d", nil)
}

// TestFW109DStockTieSurvivesAFractionalRow plants S5's round 2 row and closes the same month: one
// stock balance at 0.29 units and a 100 cent average cost. The exact value is 29 cents, but the
// float64 product (0.29 * 100.0 evaluates 28.999999999999996) truncated to 28, so the stock side of
// the tie read one cent below the 1200 opening the leg posts from its own exact SQL, and a month
// whose books tie to the cent was refused by -$0.01. The close must seal.
func TestFW109DStockTieSurvivesAFractionalRow(t *testing.T) {
	fw109dCloseLeg(t, "fw109dfrac", func(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
		var productID, locationID, branchID string
		err := pool.QueryRow(ctx, `
			SELECT sb.product_id::text, sb.location_id::text, sb.branch_id::text
			  FROM stock_balances sb
			 WHERE sb.allocated_base <= 0.29
			 ORDER BY sb.product_id, sb.location_id
			 LIMIT 1`).Scan(&productID, &locationID, &branchID)
		if err != nil {
			t.Fatalf("finding the stock row the fractional plant goes on: %v", err)
		}
		mustExec(t, ctx, pool, `
			UPDATE stock_balances SET on_hand_base = 0.29
			 WHERE product_id = $1 AND location_id = $2`, productID, locationID)
		mustExec(t, ctx, pool, `
			INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
			VALUES ($1, $2, 100)
			ON CONFLICT (product_id, branch_id)
			DO UPDATE SET avg_unit_cost_cents = 100`, productID, branchID)
		t.Logf("the fractional plant: product %s at branch %s now 0.29 units at 100 cents", productID, branchID)
	})
}

// fw109dCloseLeg is the composition leg both FW1-09d tests run: the served routes over the real money
// spine, a seeded month whose opening ties are posted through the GL's own provider path, and the
// close. The plant, when given, drops extra stock rows in right after the seed, before the leg reads
// its opening figures, so both the 1200 opening and the pre flight's stock side see them.
func fw109dCloseLeg(t *testing.T, suffix string, plant func(*testing.T, context.Context, *pgxpool.Pool)) {
	t.Helper()
	ctx := context.Background()
	pool := freshMigratedDB(t, suffix)
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	if plant != nil {
		plant(t, ctx, pool)
	}
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'FW1 09d Close Org') ON CONFLICT (id) DO NOTHING`, fw109dOrgID, fw109dOrgSlug)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, fw109dOrgID)
	// The caller's ERP users row, so the stamped staff context carries a real id (the close writes it
	// as the period's closer).
	mustExec(t, ctx, pool, `UPDATE users SET platform_user_id = $1 WHERE id = 'd7000000-0000-4000-9000-000000000006'`, testkit.SubSalesManager)

	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetMember(testkit.SubSalesManager, fw109dOrgSlug, true)
	sg.Harness.Resolver.Set(fw109dOrgSlug, fw109dOrgID)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})

	log := slog.New(slog.NewTextHandler(fw109dWriter{t}, nil))
	// The card rail needs a configured merchant before any card capture is dispatched; the leg never
	// captures one, and the key is the same one the wiring hands the vault.
	vaultKey := dashVaultKey(t)
	vault, err := payment.NewVault(vaultKey)
	if err != nil {
		t.Fatalf("the leg's vault: %v", err)
	}
	if err := (payment.NewStore(pool, vault)).Configure(ctx, "run_payments", "MID-FW109D", "CAD", "", 0, false,
		map[string]string{"api_key": "fw1-09d-not-a-secret"}, ""); err != nil {
		t.Fatalf("configuring the leg's card rail: %v", err)
	}
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
	ops := wireOperations(mux, pool, sg.Guards, log, money, "fw1-09d-fx-ticket", belt.outbox)
	wireTops(mux, pool, sg.Guards, log, money, ops, config.Config{PaymentVaultKey: vaultKey}, mustRoleConfigForWire(), belt)

	// The posting queue's own consumer, registered exactly as worker.go registers it and drained
	// synchronously, so the seeded month's posting events are journals before the close reads the
	// queue (the pre flight refuses a month whose events are still waiting).
	d := notify.NewDispatcher(money.outboxStore, log)
	money.glSvc.RegisterPostingHandlers(d)
	money.inventorySvc.RegisterPostingHandlers(d)
	for i := 0; i < 30; i++ {
		if d.DrainOnce(context.Background()) == 0 {
			break
		}
	}
	var waiting, dead int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'PENDING'), count(*) FILTER (WHERE status = 'DEAD') FROM events_outbox`).Scan(&waiting, &dead); err != nil {
		t.Fatalf("reading the month's posting queue: %v", err)
	}
	if waiting != 0 || dead != 0 {
		t.Fatalf("the month's posting queue holds %d waiting and %d dead lettered events after the consumer's pass", waiting, dead)
	}

	// The month's opening balances, posted through the GL's own provider path (the walk posts its
	// opening balances the same way): the seeded open invoice against 1100, and the stock on hand
	// against 1200. Both ties the pre flight reads then have something true to read.
	//
	// The AR figure covers the open invoices that no journal speaks for yet: the consumer's pass
	// above posted the seeded demo invoices' own journals, and an opening balance on top of those
	// would book the same receivable twice.
	openAR := fw109dBranchAmounts(t, ctx, pool, `
		SELECT i.branch_id::text, COALESCE(SUM(i.balance_cents), 0)
		  FROM invoices i
		 WHERE i.status IN ('FINALIZED','PARTIALLY_PAID') AND i.balance_cents > 0
		   AND NOT EXISTS (SELECT 1 FROM gl_journals j
		                    WHERE j.source = 'INVOICE' AND j.source_ref_id = i.id AND j.status = 'POSTED')
		 GROUP BY i.branch_id ORDER BY i.branch_id`)
	// This read is the leg's OWN statement (it never calls the adapter) and it rounds the way the
	// stock subledger itself values: ROUND per row, half away from zero, the rule inventory's
	// qtyTimesCents writes and the round 2 adapter computes, so the 1200 opening here and the pre
	// flight's stock side answer on one basis (per row, so per branch and consolidated agree).
	stockValue := fw109dBranchAmounts(t, ctx, pool, `
		SELECT sb.branch_id::text,
		       COALESCE(SUM(ROUND(sb.on_hand_base * COALESCE(c.avg_unit_cost_cents, 0)))::bigint, 0)
		  FROM stock_balances sb
		  LEFT JOIN product_branch_costs c
		    ON c.product_id = sb.product_id AND c.branch_id = sb.branch_id
		 GROUP BY sb.branch_id ORDER BY sb.branch_id`)
	if len(stockValue) == 0 {
		t.Fatal("the seeded world carries no stock on hand, so the close pre flight's stock tie has nothing to read")
	}
	for _, o := range openAR {
		fw109dPost(t, ctx, money, gl.ExternalJournal{
			Source: "MANUAL", SourceRefID: "88a20000-0000-4000-8000-0000000000d1",
			IdempotencyKey: "fw1-09d:opening:ar:" + o.branch, Memo: "opening accounts receivable",
			BranchID: o.branch,
			Lines: []gl.ExternalLine{
				{AccountCode: "1100", DebitCents: o.cents, Memo: "opening AR"},
				{AccountCode: "3000", CreditCents: o.cents, Memo: "opening equity"},
			},
		})
	}
	var stockJournalID string
	for _, s := range stockValue {
		id := fw109dPost(t, ctx, money, gl.ExternalJournal{
			Source: "MANUAL", SourceRefID: "88a20000-0000-4000-8000-0000000000d2",
			IdempotencyKey: "fw1-09d:opening:stock:" + s.branch, Memo: "opening stock",
			BranchID: s.branch,
			Lines: []gl.ExternalLine{
				{AccountCode: "1200", DebitCents: s.cents, Memo: "opening stock"},
				{AccountCode: "3000", CreditCents: s.cents, Memo: "opening equity"},
			},
		})
		if stockJournalID == "" {
			stockJournalID = id
		}
	}

	// The month the opening journals filed into is the month the close seals.
	var periodID string
	if err := pool.QueryRow(ctx, `
		SELECT fp.id::text
		  FROM gl_journals j JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.id = $1::uuid`, stockJournalID).Scan(&periodID); err != nil {
		t.Fatalf("reading the month the opening stock journal filed into: %v", err)
	}
	var periodYear, periodMonth int
	if err := pool.QueryRow(ctx, `SELECT year, month FROM fiscal_periods WHERE id = $1::uuid`, periodID).Scan(&periodYear, &periodMonth); err != nil {
		t.Fatalf("reading the month's period: %v", err)
	}

	// The close, through the served route, as the Financial Controller.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/gl/periods/"+periodID+"/close",
		strings.NewReader(`{"reason":"FW1-09d month close"}`))
	req.Header.Set("Authorization", "Bearer "+sg.Harness.Minter.StaffToken(t, testkit.SubSalesManager))
	req.Header.Set(authz.HeaderXOrg, fw109dOrgSlug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	mux.ServeHTTP(rsp, req)
	if rsp.Code != http.StatusOK {
		t.Fatalf("the %04d-%02d close answered %d: %s (the close pre flight must pass on a month whose stock is on hand and in tie)",
			periodYear, periodMonth, rsp.Code, rsp.Body.String())
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE id = $1::uuid`, periodID).Scan(&status); err != nil {
		t.Fatalf("reading the closed period: %v", err)
	}
	if status != "CLOSED" {
		t.Errorf("the %04d-%02d close answered 200 but the period is %s, want CLOSED", periodYear, periodMonth, status)
	}
}

// fw109dBranchAmounts reads one opening figure per branch.
func fw109dBranchAmounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, q string) []fw109dBranchAmount {
	t.Helper()
	rows, err := pool.Query(ctx, q)
	if err != nil {
		t.Fatalf("reading the opening figures: %v", err)
	}
	defer rows.Close()
	var out []fw109dBranchAmount
	for rows.Next() {
		var a fw109dBranchAmount
		if err := rows.Scan(&a.branch, &a.cents); err != nil {
			t.Fatalf("reading the opening figures: %v", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the opening figures: %v", err)
	}
	return out
}

// fw109dPost posts one opening journal through the GL's own provider path and answers its id.
func fw109dPost(t *testing.T, ctx context.Context, money *moneySpine, j gl.ExternalJournal) string {
	t.Helper()
	id, err := money.glSvc.PostExternal(ctx, j)
	if err != nil {
		t.Fatalf("posting the opening journal %s: %v", j.IdempotencyKey, err)
	}
	return id
}
