package main

// wire_roles_test.go, the composition-level proof of the till's
// role-configuration rules (RT-B, the lead's P1-1 ruling). Two halves:
//
// The pin: the serve composition and the demo compose must carry the
// till-rules wiring, read from the wiring sources themselves (the
// viz_label_vocabulary pattern): a wiring call deleted from either site
// fails here, not at the counter.
//
// The behavior: over the composition fixture database (HH_CORE_TEST_DATABASE_URL,
// the composition suites' gate variable, skip politely when absent), the
// REAL wiring functions loadRoleConfig and wireTillRules build the pos
// service the serve role builds, and that service answers the two questions
// the rows name: the margin glance for the clerk (X3, the configuration's
// list decides) and the 300.00 refusal (X7, the configuration's amount),
// the audit trail holding the refusal row.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identityctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	pos "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// TestTillRules_WiringIsPinned reads the two composition sites and refuses
// to let the till-rules wiring drift away from them.
func TestTillRules_WiringIsPinned(t *testing.T) {
	assertContains := func(file, want string) {
		t.Helper()
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !containsStr(string(raw), want) {
			t.Fatalf("%s must wire the till rules through %q; the composition-level leg (RT-B P1-1) depends on it", file, want)
		}
	}
	assertContains("wire_tops.go", "wireTillRules(")
	assertContains("wire_tops.go", "loadRoleConfig()")
	assertContains("demoseed_compose.go", "WithRoleConfig(loadRoleConfig())")
}

func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// portalStill is a PortalRoleSource the staff-only surface never consults.
type portalStill struct{}

func (portalStill) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

// TestTillRules_WiredCompositionRefusesAndShows drives the REAL wiring
// functions over the composition fixture database and asserts the built
// service answers the rows' two questions.
func TestTillRules_WiredCompositionRefusesAndShows(t *testing.T) {
	dsn := os.Getenv(coreDBEnvVar)
	if dsn == "" {
		t.Skip(coreDBEnvVar + " is not set: the composition suites need the scratch database with the frozen migrations applied")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("fixture pool: %v", err)
	}
	defer pool.Close()
	db := testkit.PgxPool(pool)
	if err := testkit.Build(ctx, db); err != nil {
		t.Fatalf("fixture build: %v", err)
	}

	// The fixture product priced at an exact 10.00 per EA at the north
	// branch, with a cost row so the glance has a margin to show, and one
	// open drawer for the cash rails. The sale and return keys are uuids
	// salted per run (the DIB-384 pattern).
	salt := fmt.Sprintf("%08x", os.Getpid())[:8]
	saleKey := "5d1be000-0000-4000-9000-" + salt + "0a01"
	returnKey := "5d1be000-0000-4000-9000-" + salt + "0a02"
	station := fmt.Sprintf("tillrules-%d", os.Getpid())
	for _, stmt := range []string{
		fmt.Sprintf(`INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
			VALUES ('%s', 'EA', 1, TRUE) ON CONFLICT (product_id, uom_code) DO NOTHING`, testkit.ProductID),
		fmt.Sprintf(`INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
			VALUES ('%s', 1000, 'EA') ON CONFLICT (product_id) DO NOTHING`, testkit.ProductID),
		fmt.Sprintf(`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
			VALUES ('%s', '%s', 600) ON CONFLICT (product_id, branch_id) DO NOTHING`,
			testkit.ProductID, testkit.BranchNorthAID),
		fmt.Sprintf(`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents)
			SELECT '%s', '%s', 'OPEN', u.id, 100000 FROM users u WHERE u.platform_user_id = '%s'
			AND NOT EXISTS (SELECT 1 FROM till_sessions WHERE station = '%s')`,
			testkit.BranchNorthAID, station, testkit.SubInsideSales, station),
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture plant: %v", err)
		}
	}
	var drawerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM till_sessions WHERE station = $1`, station).Scan(&drawerID); err != nil {
		t.Fatalf("drawer: %v", err)
	}

	// The guards exactly as the serve role builds them: the kit harness for
	// the platform doubles, the real staff authority resolver over the
	// identity tables.
	harness := testkit.NewHarness(t)
	harness.Membership.SetMember(testkit.SubInsideSales, testkit.OrgNorthSlug, true)
	harness.Resolver.Set(testkit.OrgNorthSlug, testkit.OrgNorthID)
	authority, err := identity.NewStaffAuthorityResolver(pool)
	if err != nil {
		t.Fatalf("staff authority resolver: %v", err)
	}
	g, err := guards.NewGuards(harness.Gate, authority, portalStill{}, nil)
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}

	// The REAL wiring: the load whose refusal stops the core, and the
	// till-rules wiring the serve role calls.
	productSvc := product.NewService(product.NewRepository(pool))
	pricingSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(productSvc).
		WithUOMConverter(productSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())
	svc := wireTillRules(pos.NewService(pos.NewRepository(pool)).
		WithLineResolver(pricingSvc).
		WithTenderTaker(till.NewService(till.NewRepository(pool))).
		WithCreditGate(pos.NewIdentityCreditGate()).
		WithAudit(audit.NewService(audit.NewRepository(pool), slog.Default())),
		loadRoleConfig(), g, linkedUserID(pool))

	// The clerk, exactly as the guard adapter stamps it: the bridged
	// caller with the linked users id.
	clerk := identityctx.ContextWithStaff(identityctx.ContextWithStaffUserID(ctx, testkit.UserInsideSalesID), identityctx.StaffContext{
		UserID: testkit.UserInsideSalesID, RoleCode: "INSIDE_SALES",
		Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		Status: identityctx.StatusActive, Branches: map[string]bool{testkit.BranchNorthAID: true},
	})

	// X3 at the composition: the glance answers margin for the clerk,
	// because the configuration's list names the role.
	glance, err := svc.MarginGlance(clerk, pos.MarginGlanceInput{
		BranchID: testkit.BranchNorthAID, CustomerType: "RETAIL",
		Lines: []pos.LineInput{{ProductID: testkit.ProductID, QtyEntered: 10, UOMEntered: "EA"}},
	})
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if len(glance.Lines) != 1 || glance.Lines[0].MarginBps == nil {
		t.Fatalf("the wired glance must carry margin for the configured clerk: %+v", glance)
	}

	// X7 at the composition: the 300.00 return is refused with the
	// configured amount, no configuration-free admission.
	sale, err := svc.Sale(clerk, pos.SaleInput{
		IdempotencyKey: saleKey,
		BranchID:       testkit.BranchNorthAID, TillSessionID: drawerID,
		SaleType: pos.SaleCollectNow, Tender: pos.TenderCash,
		Lines: []pos.LineInput{{ProductID: testkit.ProductID, QtyEntered: 30, UOMEntered: "EA"}},
	})
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	var lineID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM order_lines WHERE order_id = $1::uuid LIMIT 1`, sale.OrderID).Scan(&lineID); err != nil {
		t.Fatalf("line: %v", err)
	}
	_, err = svc.Return(clerk, pos.ReturnInput{
		IdempotencyKey: returnKey,
		OrderID:        sale.OrderID, RefundMethod: pos.RefundCash, TillSessionID: drawerID,
		Lines: []pos.ReturnLineInput{{OrderLineID: lineID, QtyBase: 30}},
	})
	var refusal pos.ErrRefundApprovalRequired
	if !asRefusal(err, &refusal) {
		t.Fatalf("the wired composition must refuse the 300.00 return, got %v", err)
	}
	if refusal.AmountCents != 25000 {
		t.Fatalf("the refusal names the configured amount, got %d", refusal.AmountCents)
	}

	// The trail: the refusal's own audit row, filed under the sale.
	auditSvc := audit.NewService(audit.NewRepository(pool), slog.Default())
	entries, err := auditSvc.ReopenTransaction(ctx, "pos.return", sale.OrderID)
	if err != nil {
		t.Fatalf("trail: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "REFUND_APPROVAL_REFUSED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the wired composition must write the refusal's audit row: %v", entries)
	}
}

func asRefusal(err error, target *pos.ErrRefundApprovalRequired) bool {
	if err == nil {
		return false
	}
	if errors.As(err, target) {
		return true
	}
	var typed pos.ErrRefundApprovalRequired
	if errors.As(err, &typed) {
		*target = typed
		return true
	}
	return false
}
