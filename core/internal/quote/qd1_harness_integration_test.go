//go:build integration

// qd1_harness_integration_test.go, the shared composition of the qd1 suites:
// the real repository and audit service, the dealer's role configuration,
// and deterministic floor doubles, so a floor or ownership assertion is
// about the quote path's decision and never about the pricing fixtures'
// rates of the day.
package quote

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd1Cfg loads the decided default configuration once per process.
var qd1Cfg = func() *roleconfig.Config {
	cfg, err := roleconfig.Default()
	if err != nil {
		panic("qd1: role configuration refused: " + err.Error())
	}
	return cfg
}()

// qd1Svc composes the service the ownership and settings suites need: no
// floor arbiter decisions are consulted there.
func qd1Svc(t *testing.T, pool *pgxpool.Pool, now time.Time) *StaffService {
	t.Helper()
	return qd1SvcFloor(t, pool, now, qd1Arbiter{}, qd1Cfg)
}

// qd1SvcFloor composes the service with a specific floor arbiter double and
// configuration (a test may turn a setting off).
func qd1SvcFloor(t *testing.T, pool *pgxpool.Pool, now time.Time, arb qd1Arbiter, cfg *roleconfig.Config) *StaffService {
	t.Helper()
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	// The decision's EVALUATION is the double (deterministic figures); the
	// decision's RECORD writes through the real pricing service, so the
	// BYPASS row assertions read the real pricing_audit_log.
	arb.records = pricing.NewService(pricing.NewRepository(pool))
	svc := NewStaffService(repo).
		WithAudit(aud).
		WithAuditTrail(aud).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithActorNamer(repo).
		WithRoleConfig(cfg).
		WithFloorArbiter(&arb).
		WithFloorRail(rail).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })
	return svc
}

// qd1CfgNoManagerClearing builds the decided default with the X5 dealer
// setting turned off, the configuration a dealer who routes every below
// floor price to leadership holds.
func qd1CfgNoManagerClearing(t *testing.T) *roleconfig.Config {
	t.Helper()
	doc := roleconfig.Document{
		Cells: qd1Cfg.Cells(),
		Lists: roleconfig.Lists{
			ExceptionApprovers: map[roleconfig.ExceptionKind][]string{
				roleconfig.ExceptionFloorQuote: {"financial_controller", "general_manager"},
				roleconfig.ExceptionJournal:    {"financial_controller", "general_manager"},
				roleconfig.ExceptionFreight:    {"financial_controller", "general_manager"},
				roleconfig.ExceptionCreditCOD:  {"financial_controller", "general_manager"},
			},
			FloorClearersOwnBranch:    []string{"sales_manager", "branch_manager"},
			DeliveryDateCommitters:    []string{"yard_manager", "logistics_manager", "branch_manager"},
			QuoteFloorBranchApprovers: []string{"sales_manager"},
		},
		Settings: map[string]json.RawMessage{
			roleconfig.SettingCostVisibility:                json.RawMessage(`["inside_sales","outside_sales","sales_manager","yard_manager","logistics_manager","branch_manager","procurement_manager","payables_receivables","financial_controller","general_manager"]`),
			roleconfig.SettingCreditAtTill:                  json.RawMessage(`true`),
			roleconfig.SettingRefundAmountWithoutApproval:   json.RawMessage(`25000`),
			roleconfig.SettingRefundApprovers:               json.RawMessage(`["sales_manager","branch_manager"]`),
			roleconfig.SettingManagerOwnBranchFloorClearing: json.RawMessage(`false`),
			roleconfig.SettingPricingEditors:                json.RawMessage(`["sales_manager","general_manager"]`),
			roleconfig.SettingShortageResolvers:             json.RawMessage(`["yard_team","yard_manager"]`),
			roleconfig.SettingDispatchPolicySetters:         json.RawMessage(`["logistics_manager","branch_manager"]`),
			roleconfig.SettingGeneralManagerSelling:         json.RawMessage(`false`),
			roleconfig.SettingRefundApprovalWindowSeconds:   json.RawMessage(`300`),
		},
	}
	cfg, err := roleconfig.Validate(&doc)
	if err != nil {
		t.Fatalf("qd1: configuration with the setting off: %v", err)
	}
	return cfg
}

// qd1CfgCostHiddenFrom builds the decided default with one role taken
// OFF the cost and margin list, the configuration a dealer who narrows
// who sees cost holds (X3): everything else stands as decided.
func qd1CfgCostHiddenFrom(t *testing.T, hidden string) *roleconfig.Config {
	t.Helper()
	doc := roleconfig.Document{
		Cells: qd1Cfg.Cells(),
		Lists: roleconfig.Lists{
			ExceptionApprovers: map[roleconfig.ExceptionKind][]string{
				roleconfig.ExceptionFloorQuote: {"financial_controller", "general_manager"},
				roleconfig.ExceptionJournal:    {"financial_controller", "general_manager"},
				roleconfig.ExceptionFreight:    {"financial_controller", "general_manager"},
				roleconfig.ExceptionCreditCOD:  {"financial_controller", "general_manager"},
			},
			FloorClearersOwnBranch:    []string{"sales_manager", "branch_manager"},
			DeliveryDateCommitters:    []string{"yard_manager", "logistics_manager", "branch_manager"},
			QuoteFloorBranchApprovers: []string{"sales_manager"},
		},
		Settings: map[string]json.RawMessage{
			roleconfig.SettingCostVisibility:                json.RawMessage(`["outside_sales","sales_manager","yard_manager","logistics_manager","branch_manager","procurement_manager","payables_receivables","financial_controller","general_manager"]`),
			roleconfig.SettingCreditAtTill:                  json.RawMessage(`true`),
			roleconfig.SettingRefundAmountWithoutApproval:   json.RawMessage(`25000`),
			roleconfig.SettingRefundApprovers:               json.RawMessage(`["sales_manager","branch_manager"]`),
			roleconfig.SettingManagerOwnBranchFloorClearing: json.RawMessage(`true`),
			roleconfig.SettingPricingEditors:                json.RawMessage(`["sales_manager","general_manager"]`),
			roleconfig.SettingShortageResolvers:             json.RawMessage(`["yard_team","yard_manager"]`),
			roleconfig.SettingDispatchPolicySetters:         json.RawMessage(`["logistics_manager","branch_manager"]`),
			roleconfig.SettingGeneralManagerSelling:         json.RawMessage(`false`),
			roleconfig.SettingRefundApprovalWindowSeconds:   json.RawMessage(`300`),
		},
	}
	cfg, err := roleconfig.Validate(&doc)
	if err != nil {
		t.Fatalf("qd1: configuration with %s off the cost list: %v", hidden, err)
	}
	return cfg
}

// qd1CostAvail is an availability double whose every bucket answers a
// fixed figure, so a cost visibility assertion is about the gate and not
// about the stock fixtures of the day.
type qd1CostAvail struct{ cost int64 }

func (a qd1CostAvail) AvailabilityAtBranch(_ context.Context, productID, branchID string) (BranchStock, error) {
	return BranchStock{
		ProductID: productID, BranchID: branchID, BranchCode: "TRT", BranchName: "Trenton",
		OnHandBase: 10, AvailableBase: 10, AvgCostCents: a.cost,
	}, nil
}

func (a qd1CostAvail) AvailabilityAllBranches(ctx context.Context, productID string) ([]BranchStock, error) {
	one, err := a.AvailabilityAtBranch(ctx, productID, "b1")
	if err != nil {
		return nil, err
	}
	return []BranchStock{one}, nil
}

// qd1Arbiter is a deterministic FloorArbiter double. belowFloor is derived
// from the two figures, so a test states the margin and the floor and the
// ladder under test decides.
type qd1Arbiter struct {
	marginBps int64
	floorBps  int64
	recorded  []pricing.FloorRecord
	// records, when set, is the real pricing service the record half writes
	// through (the pricing log row), beside the in-memory record.
	records *pricing.Service
}

func (a *qd1Arbiter) EvaluateLineFloor(_ context.Context, in pricing.FloorQuestion) (pricing.FloorAnswer, error) {
	floor := a.floorBps
	if floor == 0 {
		floor = 1200
	}
	return pricing.FloorAnswer{
		MarginBps:  a.marginBps,
		FloorBps:   floor,
		BelowRule:  in.CandidateUnitCents < in.RuleUnitCents,
		BelowFloor: a.marginBps < floor,
		CostCents:  500,
	}, nil
}

func (a *qd1Arbiter) RecordFloorDecision(ctx context.Context, rec pricing.FloorRecord) {
	a.recorded = append(a.recorded, rec)
	if a.records != nil {
		a.records.RecordFloorDecision(ctx, rec)
	}
}

// qd1SetDisc writes a stored discount on a seeded line directly, the
// pre-state of the quantity reprice tests.
func qd1SetDisc(t *testing.T, pool *pgxpool.Pool, quoteID string, lineNo, discPct int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE quote_lines SET disc_pct = $3,
			pre_discount_total_cents = line_total_cents,
			line_total_cents = (line_total_cents * (100 - $3) + 50) / 100
		WHERE quote_id = $1 AND line_no = $2`, quoteID, lineNo, discPct); err != nil {
		t.Fatalf("seed stored discount: %v", err)
	}
}

// qd1BypassRows reads the pricing log's BYPASS rows for one quote. The
// action filter is the point (QD-2): a below rule above floor write now
// leaves an UPDATE row beside any BYPASS row, and this helper answers only
// the bypasses its name promises.
func qd1BypassRows(t *testing.T, pool *pgxpool.Pool, quoteID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT action FROM pricing_audit_log
		 WHERE ref_entity_type = 'quote' AND ref_entity_id = $1 AND action = 'BYPASS'
		 ORDER BY created_at`, quoteID)
	if err != nil {
		t.Fatalf("read pricing log: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatalf("scan pricing log: %v", err)
		}
		out = append(out, action)
	}
	return out
}

// qd1LineClearing reads a line's floor clearing stamp, nil when none.
func qd1LineClearing(t *testing.T, pool *pgxpool.Pool, quoteID string, lineNo int) map[string]any {
	t.Helper()
	var data []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata FROM quote_lines WHERE quote_id = $1 AND line_no = $2`, quoteID, lineNo).Scan(&data); err != nil {
		t.Fatalf("read line metadata: %v", err)
	}
	m := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal line metadata: %v", err)
		}
	}
	clear, _ := m["floor_clear"].(map[string]any)
	return clear
}
