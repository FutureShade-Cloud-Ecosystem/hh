//go:build integration

// qd1c_sync_authority_integration_test.go injects different published role
// configurations into the quotes sync door (P2-4, round 3): the door reads
// what the composition root published, decides with the REST ladder, and
// refuses everything when nothing is published. The by-request legs over
// the real route sit in cmd/core (qd1c_quote_sync_test.go).
package quote

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1cCfgWithCell is the decided default with one Quote Desk cell moved.
func qd1cCfgWithCell(t *testing.T, role string, level roleconfig.Level) *roleconfig.Config {
	t.Helper()
	cells := append([]roleconfig.Cell(nil), qd1Cfg.Cells()...)
	for i := range cells {
		if cells[i].Role == role && cells[i].Surface == "quote-desk" {
			cells[i].Level = level
		}
	}
	doc := roleconfig.Document{
		Cells: cells,
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
			roleconfig.SettingCostVisibility:                json.RawMessage(`["sales_manager","general_manager","financial_controller"]`),
			roleconfig.SettingCreditAtTill:                  json.RawMessage(`true`),
			roleconfig.SettingRefundAmountWithoutApproval:   json.RawMessage(`25000`),
			roleconfig.SettingRefundApprovalLimitEnabled:    json.RawMessage(`true`),
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
		t.Fatalf("configuration with %s at %s: %v", role, level, err)
	}
	return cfg
}

// qd1cPublish publishes a configuration for one test and restores the prior.
func qd1cPublish(t *testing.T, cfg *roleconfig.Config) {
	t.Helper()
	prev := publishedRoles.Load()
	PublishRoleConfig(cfg)
	t.Cleanup(func() { PublishRoleConfig(prev) })
}

func TestQD1C_SyncAuthorityFollowsThePublishedConfiguration(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	userOf := func(sub string) string {
		uid, ok := linkedSyncUser(f.pool)(ctx, sub)
		if !ok {
			t.Fatalf("no linked user for %s", sub)
		}
		return uid
	}
	clerk, manager := userOf(testkit.SubInsideSales), userOf(testkit.SubSalesManager)
	plant := func(owner string) string {
		var id string
		if err := f.pool.QueryRow(ctx, `
			INSERT INTO quotes (quote_no, customer_id, branch_id, org_id, status, subtotal_cents, tax_cents,
			                    total_cents, salesperson_id, created_by, seq)
			VALUES ('#Q-QD1C-' || nextval('quote_no_seq'), $1::uuid, $2::uuid, $3::uuid, 'DRAFT', 0, 0, 0, $4::uuid, $4::uuid, 1)
			RETURNING id::text`,
			testkit.TenantAlphaID, testkit.BranchNorthAID, testkit.OrgNorthID, owner).Scan(&id); err != nil {
			t.Fatalf("plant quote: %v", err)
		}
		return id
	}
	as := func(sub string) sync.ScopeFilter {
		return sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: sub}
	}
	write := func(sub, id string) error {
		_, err := f.store.Write(ctx, as(sub), id, "3ef10000-0d1c-4000-8000-0000000000f1",
			map[string]any{"valid_until": time.Now().UTC().AddDate(5, 0, 0).Format("2006-01-02")})
		return err
	}
	del := func(sub, id string) error {
		_, err := f.store.Delete(ctx, as(sub), id, "")
		return err
	}
	refused := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, sync.ErrOpRefused) {
			t.Errorf("%s: want the operation refused, got %v", name, err)
		}
	}
	ok := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: want applied, got %v", name, err)
		}
	}

	// Nothing published: no level for anyone, the door fails closed.
	qd1cPublish(t, nil)
	refused("unpublished, Sales manager edits", write(testkit.SubSalesManager, plant(clerk)))
	refused("unpublished, Inside Sales edits its own", write(testkit.SubInsideSales, plant(clerk)))
	refused("unpublished, Sales manager withdraws", del(testkit.SubSalesManager, plant(clerk)))

	// The Sales manager at Work only: its own quote, never another's.
	qd1cPublish(t, qd1cCfgWithCell(t, "sales_manager", roleconfig.LevelWork))
	ok("Sales manager at Work edits its own", write(testkit.SubSalesManager, plant(manager)))
	refused("Sales manager at Work edits another's", write(testkit.SubSalesManager, plant(clerk)))
	refused("Sales manager at Work withdraws another's", del(testkit.SubSalesManager, plant(clerk)))

	// The Sales manager at Read cannot work at all.
	qd1cPublish(t, qd1cCfgWithCell(t, "sales_manager", roleconfig.LevelRead))
	refused("Sales manager at Read edits its own", write(testkit.SubSalesManager, plant(manager)))

	// Inside Sales raised to Manage may act on another seller's quote.
	qd1cPublish(t, qd1cCfgWithCell(t, "inside_sales", roleconfig.LevelManage))
	ok("Inside Sales at Manage edits another's", write(testkit.SubInsideSales, plant(manager)))
	ok("Inside Sales at Manage withdraws another's", del(testkit.SubInsideSales, plant(manager)))

	// The decided default: the ladder as plan 3.3 states it.
	qd1cPublish(t, qd1Cfg)
	ok("default, Inside Sales edits its own", write(testkit.SubInsideSales, plant(clerk)))
	refused("default, Inside Sales edits another's", write(testkit.SubInsideSales, plant(manager)))
	ok("default, Sales manager edits any in its branch", write(testkit.SubSalesManager, plant(clerk)))
}
