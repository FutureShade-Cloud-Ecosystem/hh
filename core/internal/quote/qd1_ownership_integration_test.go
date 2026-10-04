//go:build integration

// qd1_ownership_integration_test.go, the ownership ladder of plan 3.3 (defect
// D4): a caller at Work edits only its own quotes, a caller at Manage edits
// any quote in its branch, and "own" is the quote's salesperson when one is
// set, else its creator. Creating a quote sets the salesperson to the caller.
// A Work caller still reads any quote in its branch and converts an accepted
// one: only the edit ladder changes.
//
// Also holds the wrong role ledger's ownership rows (plan 3.4): rows 1, 2, 3,
// 4, 7 and 14, one request per row.
package quote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd1User inserts a throwaway ACTIVE staff user and returns its id.
func qd1User(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, display_name, status) VALUES ($1::citext, $1::text, 'ACTIVE')
		RETURNING id::text`, name+"@qd1.test").Scan(&id); err != nil {
		t.Fatalf("seed qd1 user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// qd1WithRole stamps a caller shape onto a context: the ERP role code the
// bridge maps, and the caller's own user id. The base context carries the
// branches (intStaff reaches one branch).
func qd1WithRole(ctx context.Context, erpRole string, branch string, userID string) context.Context {
	c, _ := qCallerFrom(ctx)
	c.RoleCode = erpRole
	c.UserID = userID
	switch erpRole {
	case "GENERAL_MANAGER", "FINANCIAL_CONTROLLER", "HR":
		c.Tier = identity.TierLeadership
		c.Scope = identity.ScopeGlobal
	case "SALES_MANAGER", "BRANCH_MANAGER", "YARD_MANAGER":
		c.Tier = identity.TierManager
		c.Scope = identity.ScopeBranch
	default:
		c.Tier = identity.TierFrontline
		c.Scope = identity.ScopeBranch
	}
	_ = branch
	return qCtxAs(c)
}

// qd1SeedOwnedQuote seeds a Trenton quote whose owner is ownerID: the
// salesperson when salespersonSet, else the creator.
func qd1SeedOwnedQuote(t *testing.T, pool *pgxpool.Pool, status string, ownerID string, salespersonSet bool) (cust, quoteID, prod, uom string) {
	t.Helper()
	ctx := context.Background()
	prod, uom = anchoredProduct(t, pool)
	tag := time.Now().UnixNano()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1::citext,$1::text,'CONTRACTOR') RETURNING id::text`,
		fmt.Sprintf("QD1-%d", tag)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var sp, creator any
	if salespersonSet {
		sp = ownerID
	} else {
		creator = ownerID
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, valid_until,
		                    subtotal_cents, tax_cents, total_cents, salesperson_id, created_by)
		VALUES ($1, $2::uuid, $3::uuid, $4, NULL, 2000, 0, 2000, $5, $6)
		RETURNING id::text`,
		fmt.Sprintf("#Q-QD1-%d", tag), cust, intTrenton, status, sp, creator).Scan(&quoteID); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered,
		                         qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1::uuid, 1, $2::uuid, 'qd1 line', 2, $3, 2, 1000, $3, 2000)`,
		quoteID, prod, uom); err != nil {
		t.Fatalf("seed quote line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_lines WHERE quote_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust, quoteID, prod, uom
}

// - ledger row 3: a Work caller cannot edit another salesperson's quote -

func TestQD1_Row3_WorkCallerCannotEditAnotherSalespersonsQuote(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	peer := qd1User(t, pool, "peer")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, peer)

	_, err := svc.EditLineQty(ctx, quoteID, 1, "5")
	if err == nil {
		t.Fatal("row 3: an inside sales caller edited another salesperson's quote, want a 403 refusal")
	}
	var owner *NotQuoteOwnerError
	if !errors.As(err, &owner) {
		t.Fatalf("row 3: want *NotQuoteOwnerError, got %T: %v", err, err)
	}
	if !strings.Contains(owner.Error(), "Sales manager") {
		t.Errorf("row 3: the refusal must name the Sales manager as the way forward, got %q", owner.Error())
	}
	if !strings.Contains(owner.Error(), "seller") {
		t.Errorf("row 3: the refusal must name the quote's owner (plan 3.4), got %q", owner.Error())
	}
	// The row is untouched.
	if got := liveLine(t, pool, quoteID, 1); got.QtyEntered != "2.0000" {
		t.Errorf("row 3: a refused edit changed the line, qty now %s", got.QtyEntered)
	}
}

// The read half: a Work caller still READS any quote in its branch, but the
// server offers it nothing to edit (editable_fields empty), and the view
// carries the quote's owner.
func TestQD1_WorkCallerReadsOthersQuoteWithEmptyOfferAndOwnerNamed(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	peer := qd1User(t, pool, "peer")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, peer)

	view, err := svc.StaffQuoteRead(ctx, quoteID)
	if err != nil {
		t.Fatalf("a Work caller must still read any quote in its branch: %v", err)
	}
	if len(view.EditableFields) != 0 {
		t.Errorf("editable_fields must be empty for a Work caller who does not own the quote, got %v", view.EditableFields)
	}
	if view.OwnerID != seller {
		t.Errorf("the view must carry the quote's owner, want %s got %q", seller, view.OwnerID)
	}

	// The owner's own read offers the fields.
	ownCtx := qCtxAs(intStaff(intTrenton))
	ownCtx = qd1WithRole(ownCtx, "INSIDE_SALES", intTrenton, seller)
	own, err := svc.StaffQuoteRead(ownCtx, quoteID)
	if err != nil {
		t.Fatalf("the owner's read: %v", err)
	}
	if len(own.EditableFields) == 0 {
		t.Error("the owner's read must offer the edit fields")
	}
}

// P2-1: the offer must take the caller's LEVEL, not ownership alone. A
// Sales manager (Manage) may take over any quote in its branch, so its
// read of another salesperson's quote offers the edit fields: a control
// that appears is a control that works, and the write accepts it.
func TestQD1_ManageCallerOfferedEditsOnOthersQuote(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	mgr := qd1User(t, pool, "mgr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "SALES_MANAGER", intTrenton, mgr)

	view, err := svc.StaffQuoteRead(ctx, quoteID)
	if err != nil {
		t.Fatalf("a Manage caller must read any quote in its branch: %v", err)
	}
	if len(view.EditableFields) == 0 {
		t.Fatal("a Sales manager reading another salesperson's quote must be offered the edit fields, got none")
	}
	// And the offered control works: the write path accepts the manager's edit.
	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
		t.Errorf("the offered edit must be accepted by the write path: %v", err)
	}
}

// "Own" is the salesperson when one is set, else the creator.
func TestQD1_OwnIsSalespersonElseCreator(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	creator := qd1User(t, pool, "creator")
	other := qd1User(t, pool, "other")

	// No salesperson: the creator owns it.
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", creator, false)
	svc := qd1Svc(t, pool, now)

	creatorCtx := qCtxAs(intStaff(intTrenton))
	creatorCtx = qd1WithRole(creatorCtx, "INSIDE_SALES", intTrenton, creator)
	if _, err := svc.EditLineQty(creatorCtx, quoteID, 1, "5"); err != nil {
		t.Fatalf("the creator must own a quote with no salesperson: %v", err)
	}

	otherCtx := qCtxAs(intStaff(intTrenton))
	otherCtx = qd1WithRole(otherCtx, "INSIDE_SALES", intTrenton, other)
	if _, err := svc.EditLineQty(otherCtx, quoteID, 1, "6"); err == nil {
		t.Fatal("a third caller must not own a quote whose creator is someone else")
	}
}

// - ledger row 4: a Work caller's list defaults to its own quotes -

func TestQD1_Row4_WorkCallerListDefaultsToOwnQuotes(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	peer := qd1User(t, pool, "peer")
	_, aliceQuote, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	_, bobQuote, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", peer, true)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	rows, err := svc.StaffQuoteList(ctx, Filter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var sawAlice, sawBob bool
	for _, r := range rows {
		if r.ID == aliceQuote {
			sawAlice = true
			if r.OwnerID != seller {
				t.Errorf("the summary must carry the owner, want %s got %q", seller, r.OwnerID)
			}
		}
		if r.ID == bobQuote {
			sawBob = true
		}
	}
	if !sawAlice {
		t.Error("the caller's own quote is missing from its default list")
	}
	if sawBob {
		t.Error("row 4: another salesperson's quote appears in a Work caller's default list")
	}

	// A Manage caller's list is not filtered to its own.
	mgrCtx := qCtxAs(intStaff(intTrenton))
	mgrCtx = qd1WithRole(mgrCtx, "SALES_MANAGER", intTrenton, peer)
	mrows, err := svc.StaffQuoteList(mgrCtx, Filter{})
	if err != nil {
		t.Fatalf("manager list: %v", err)
	}
	sawAlice = false
	for _, r := range mrows {
		if r.ID == aliceQuote {
			sawAlice = true
		}
	}
	if !sawAlice {
		t.Error("a Manage caller must reach every quote in its branch")
	}
}

// Creating a quote sets the salesperson to the caller.
func TestQD1_CreateSetsSalespersonToCaller(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	cust := qd1Customer(t, pool)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	view, err := svc.CreateQuote(ctx, QuoteCreate{CustomerID: cust, BranchID: intTrenton})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.OwnerID != seller {
		t.Errorf("creating a quote must set the salesperson to the caller, owner %q want %s", view.OwnerID, seller)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, view.ID)
	})
}

// A caller at Manage edits any quote in its branch (r3, the Sales manager).
func TestQD1_ManageCallerEditsAnyQuoteInBranch(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	mgr := qd1User(t, pool, "mgr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "SALES_MANAGER", intTrenton, mgr)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
		t.Fatalf("a Manage caller must edit any quote in its branch: %v", err)
	}
}

// - ledger row 2: the General Manager holds Read and cannot work -

func TestQD1_Row2_GeneralManagerCannotEdit(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	gm := qd1User(t, pool, "gm")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "GENERAL_MANAGER", intTrenton, gm)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("row 2: the General Manager (Read) must be refused, got %v", err)
	}
	if _, err := svc.AddLine(ctx, quoteID, prod, "x", "2", uom); !errors.Is(err, ErrForbidden) {
		t.Fatalf("row 2: add line as the General Manager must be refused, got %v", err)
	}
}

// - ledger row 1's service half: a role with no Quote Desk level edits nothing -

func TestQD1_Row1_NoQuoteDeskRoleCannotEdit(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	fc := qd1User(t, pool, "fc")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "FINANCIAL_CONTROLLER", intTrenton, fc)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("row 1: the Financial Controller holds no Quote Desk level and must be refused, got %v", err)
	}
}

// - ledger row 7: any write on a quote of another branch, as a Sales manager -

func TestQD1_Row7_SalesManagerOtherBranchRefused(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	mgr := qd1User(t, pool, "mgr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1Svc(t, pool, now)
	// The manager reaches Trenton alone; the quote sits in Trenton, so use a
	// manager whose grants hold Kingston only.
	ctx := qCtxAs(intStaff(intKingston))
	ctx = qd1WithRole(ctx, "SALES_MANAGER", intKingston, mgr)

	_, err := svc.EditLineQty(ctx, quoteID, 1, "5")
	if err == nil {
		t.Fatal("row 7: a write on another branch's quote must be refused")
	}
	if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("row 7: the refusal must be a flat 403 or 404 with no detail, got %v", err)
	}
}

// - ledger row 14: convert an accepted quote, as the Yard manager -

func TestQD1_Row14_YardManagerConvertRefused(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	ym := qd1User(t, pool, "ym")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "ACCEPTED", seller, true)

	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "YARD_MANAGER", intTrenton, ym)

	_, err := svc.ConvertQuote(ctx, quoteID, ConvertOptions{})
	if err == nil {
		t.Fatal("row 14: the Yard manager converted a quote, want the not-a-selling-role refusal")
	}
	var notSelling *NotSellingRoleError
	if !errors.As(err, &notSelling) {
		t.Fatalf("row 14: want *NotSellingRoleError naming the role, got %T: %v", err, err)
	}
}

// - D8: cost and margin by the configuration's list -

func TestQD1_MarginGlanceAnswersByConfigCostList(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	hr := qd1User(t, pool, "hr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd1SeedCost(t, pool, quoteID)

	svc := qd1Svc(t, pool, now)

	// Inside Sales is on the configuration's cost_visibility list (r3 X3).
	salesCtx := qCtxAs(intStaff(intTrenton))
	salesCtx = qd1WithRole(salesCtx, "INSIDE_SALES", intTrenton, seller)
	m, err := svc.MarginGlance(salesCtx, quoteID)
	if err != nil {
		t.Fatalf("glance as inside sales: %v", err)
	}
	if m.MarginBps == nil {
		t.Error("D8: an inside sales caller on the cost list must see cost and margin")
	}

	// HR is not on the list and must receive the byte-absent answer.
	hrCtx := qCtxAs(intStaff(intTrenton))
	hrCtx = qd1WithRole(hrCtx, "HR", intTrenton, hr)
	h, err := svc.MarginGlance(hrCtx, quoteID)
	if err != nil {
		t.Fatalf("glance as hr: %v", err)
	}
	if h.MarginBps != nil || h.EstProfitCents != nil || len(h.Lines) != 0 {
		t.Errorf("D8: HR is off the cost list and must receive the empty glance, got %+v", h)
	}
}

// qd1Customer seeds a bare customer.
func qd1Customer(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1::citext,$1::text,'CONTRACTOR') RETURNING id::text`,
		fmt.Sprintf("QD1C-%d", time.Now().UnixNano())).Scan(&id); err != nil {
		t.Fatalf("seed qd1 customer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, id) })
	return id
}

// qd1SeedCost plants a branch WAC row for the quote's line so the glance has
// a cost to speak of.
func qd1SeedCost(t *testing.T, pool *pgxpool.Pool, quoteID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		SELECT ql.product_id, q.branch_id, 500 FROM quote_lines ql
		 JOIN quotes q ON q.id = ql.quote_id WHERE ql.quote_id = $1 AND ql.line_no = 1
		ON CONFLICT (product_id, branch_id) DO UPDATE SET avg_unit_cost_cents = 500`, quoteID)
	if err != nil {
		t.Fatalf("seed qd1 cost: %v", err)
	}
}

// The role configuration's own suite pins that the default's quote desk
// cells are what the ladder reads; this pins the reader the service uses.
func TestQD1_RoleConfigHoldsQuoteDeskCells(t *testing.T) {
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	for role, want := range map[string]string{
		"inside_sales": "work", "outside_sales": "work", "yard_manager": "work",
		"branch_manager": "work", "sales_manager": "manage", "general_manager": "read",
		"financial_controller": "none", "hr": "none", "yard_team": "none",
	} {
		got, err := cfg.Level(role, "quote-desk")
		if err != nil {
			t.Fatalf("level for %s: %v", role, err)
		}
		if string(got) != want {
			t.Errorf("quote-desk level for %s: want %s got %s", role, want, got)
		}
	}
}

// The create stamp pins the COLUMN, not the ownership answer: a mutation
// that drops the salesperson stamp still leaves the caller as creator, and
// the creator fallback would answer ownership exactly the same. The stamp
// must be there in its own right, because a quote whose salesperson is
// later re-assigned must follow the salesperson, and a NULL column cannot.
func TestQD1_CreateStampsTheSalespersonColumn(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	svc := qd1Svc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	var cust string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO customers (account_no, name, customer_type)
		VALUES ('QD1-STAMP', 'QD1 stamp customer', 'CONTRACTOR')
		ON CONFLICT (account_no) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	view, err := svc.CreateQuote(ctx, QuoteCreate{CustomerID: cust, BranchID: intTrenton})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var salesperson, creator any
	if err := pool.QueryRow(context.Background(),
		`SELECT salesperson_id::text, created_by::text FROM quotes WHERE id = $1`, view.ID).
		Scan(&salesperson, &creator); err != nil {
		t.Fatalf("read the stamps: %v", err)
	}
	if salesperson != seller {
		t.Fatalf("create must stamp the salesperson column itself (the creator fallback must not be what answers), salesperson=%v want %s", salesperson, seller)
	}
	if creator != seller {
		t.Fatalf("create stamps the creator too, got %v", creator)
	}
}

// Ownership prefers the SALESPERSON over the creator when both stand on
// the row: the stamp, not the fallback, is the owner.
func TestQD1_OwnershipPrefersSalespersonOverCreator(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	creator := qd1User(t, pool, "creator")
	salesperson := qd1User(t, pool, "assignee")
	cust, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", creator, false)
	if _, err := pool.Exec(context.Background(), `
		UPDATE quotes SET salesperson_id = $2 WHERE id = $1`, quoteID, salesperson); err != nil {
		t.Fatalf("stamp the salesperson: %v", err)
	}
	_ = cust

	svc := qd1Svc(t, pool, now)
	// The salesperson edits it.
	spCtx := qCtxAs(intStaff(intTrenton))
	spCtx = qd1WithRole(spCtx, "INSIDE_SALES", intTrenton, salesperson)
	if _, err := svc.EditLineQty(spCtx, quoteID, 1, "5"); err != nil {
		t.Fatalf("the stamped salesperson must own the quote: %v", err)
	}
	// The creator, no longer the salesperson, is refused at Work.
	crCtx := qCtxAs(intStaff(intTrenton))
	crCtx = qd1WithRole(crCtx, "INSIDE_SALES", intTrenton, creator)
	if _, err := svc.EditLineQty(crCtx, quoteID, 1, "6"); err == nil {
		t.Fatal("the creator must not own a quote whose salesperson is someone else")
	}
	_ = prod
	_ = uom
}
