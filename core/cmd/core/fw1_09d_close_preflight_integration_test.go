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
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw109d")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
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
	stockValue := fw109dBranchAmounts(t, ctx, pool, `
		SELECT sb.branch_id::text,
		       COALESCE(SUM(TRUNC(sb.on_hand_base * COALESCE(c.avg_unit_cost_cents, 0)))::bigint, 0)
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
