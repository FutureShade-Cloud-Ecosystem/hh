package pos

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// The counter discount's unit tests over the DB-free harness. The harness resolver prices every
// line at 10000 cents (qty 100 SF), the flat tax is 13%, and the caller is a global manager.

const (
	mgrSales   = "11111111-0000-4000-8000-0000000000a1"
	mgrBranch  = "11111111-0000-4000-8000-0000000000a2"
	clerkInner = "11111111-0000-4000-8000-0000000000a3"
	mgrYard    = "11111111-0000-4000-8000-0000000000a4"
)

// withApprovers plants the candidate logins the store would answer for the branch: a Sales
// manager and a Branch manager (both on the dealer's clearing list), an Inside Sales clerk and a
// Yard manager (neither is).
func withApprovers(h *harness) {
	h.store.discountStaff = []discountStaffRow{
		{UserID: mgrSales, Name: "Sales Mgr", RoleID: "sales_manager", RoleLabel: "Sales Manager", Tier: 2},
		{UserID: mgrBranch, Name: "Branch Mgr", RoleID: "branch_manager", RoleLabel: "Branch Manager", Tier: 2},
		{UserID: clerkInner, Name: "Inside Clerk", RoleID: "inside_sales", RoleLabel: "Inside Sales", Tier: 3},
		{UserID: mgrYard, Name: "Yard Mgr", RoleID: "yard_manager", RoleLabel: "Yard Manager", Tier: 2},
	}
}

// atLine stamps the quantity and base the harness line (100 SF at 10000 cents) was approved at;
// atBase stamps the base a ticket discount was approved at.
func atLine(d DiscountInput) *DiscountInput {
	d.ApprovedQty, d.ApprovedBaseCents = 100, 10000
	return &d
}

func atBase(d DiscountInput, base int64) *DiscountInput {
	d.ApprovedBaseCents = base
	return &d
}

func discountedInput(d *DiscountInput) SaleInput {
	in := baseInput()
	in.Lines[0].Discount = d
	return in
}

func wantRefusal(t *testing.T, err error, status int, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %d refusal containing %q, got none", status, contains)
	}
	st, ok := ClientStatus(err)
	if !ok || st != status {
		t.Fatalf("want status %d, got %d (ok=%v): %v", status, st, ok, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("refusal %q does not say %q", err.Error(), contains)
	}
}

// A percent line discount by a manager comes off the line, the tax follows the net, the lines
// store the discount and the approver, and the sale reports the approver's role.
func TestDiscount_LinePercent_AppliesStoresAndReports(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	out, err := h.svc.Sale(context.Background(), discountedInput(atLine(DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales})))
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.SubtotalCents != 9000 || out.TaxCents != 1170 || out.TotalCents != 10170 {
		t.Fatalf("want 9000/1170/10170 after 10%% off 10000, got %d/%d/%d", out.SubtotalCents, out.TaxCents, out.TotalCents)
	}
	l := h.store.orderLines[0]
	if l.LineTotalCents != 9000 || l.DiscountCents != 1000 || l.DiscountPercentBps != 1000 ||
		l.DiscountApprovedBy != mgrSales || l.DiscountRoleID != "sales_manager" {
		t.Fatalf("line did not store the discount: %+v", l)
	}
	if out.DiscountCents != 1000 || len(out.Discounts) != 1 {
		t.Fatalf("sale did not report the discount: %+v", out)
	}
	d := out.Discounts[0]
	if d.Scope != DiscountScopeLine || d.LineNo != 1 || d.AmountCents != 1000 || d.ApprovedBy != mgrSales || d.ApprovedByLabel != "Sales Manager" {
		t.Fatalf("bad discount report: %+v", d)
	}
	if h.tender.last.AmountCents != 10170 {
		t.Fatalf("the till must take the discounted total, took %d", h.tender.last.AmountCents)
	}
}

func TestDiscount_LineAmount_Applies(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	out, err := h.svc.Sale(context.Background(), discountedInput(atLine(DiscountInput{AmountCents: 2500, ApprovedBy: mgrBranch})))
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.SubtotalCents != 7500 || out.Discounts[0].ApprovedByLabel != "Branch Manager" {
		t.Fatalf("want subtotal 7500 approved by Branch Manager, got %+v", out)
	}
}

func TestDiscount_RefusedWithoutAnApprover(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	_, err := h.svc.Sale(context.Background(), discountedInput(atLine(DiscountInput{PercentBps: 500})))
	wantRefusal(t, err, http.StatusUnprocessableEntity, "approved_by")
	if len(h.store.orders) != 0 || h.tender.called {
		t.Fatalf("a refused discount wrote or took money: orders=%d tender=%v", len(h.store.orders), h.tender.called)
	}
}

func TestDiscount_RefusedForANonManagerApprover(t *testing.T) {
	for name, id := range map[string]string{
		"inside sales clerk": clerkInner,
		"yard manager":       mgrYard,
		"unknown login":      "11111111-0000-4000-8000-0000000000ff",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			withApprovers(h)
			_, err := h.svc.Sale(context.Background(), discountedInput(atLine(DiscountInput{PercentBps: 500, ApprovedBy: id})))
			wantRefusal(t, err, http.StatusUnprocessableEntity, "cannot approve a discount")
			if len(h.store.orders) != 0 {
				t.Fatalf("a refused discount wrote an order")
			}
		})
	}
}

// The list is read from the dealer's configuration: with the clearing setting off nobody at the
// counter may approve a discount, whatever their role.
func TestDiscount_FollowsTheDealersClearingSetting(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	cfg := testRoleConfig()
	if cfg.ManagerOwnBranchFloorClearing() {
		cfg = withClearingOff(t, cfg)
	}
	h.svc.WithRoleConfig(cfg)
	_, err := h.svc.Sale(context.Background(), discountedInput(atLine(DiscountInput{PercentBps: 500, ApprovedBy: mgrSales})))
	wantRefusal(t, err, http.StatusUnprocessableEntity, "cannot approve a discount")
}

func TestDiscount_ShapeRefusals(t *testing.T) {
	cases := map[string]struct {
		d    DiscountInput
		want string
	}{
		"neither":      {DiscountInput{ApprovedBy: mgrSales}, "percent or an amount"},
		"both":         {DiscountInput{PercentBps: 500, AmountCents: 100, ApprovedBy: mgrSales}, "percent or an amount"},
		"percent over": {DiscountInput{PercentBps: 10001, ApprovedBy: mgrSales}, "percent"},
		"amount over":  {DiscountInput{AmountCents: 10001, ApprovedBy: mgrSales}, "more than the line"},
		"negative":     {DiscountInput{AmountCents: -5, ApprovedBy: mgrSales}, "percent or an amount"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			withApprovers(h)
			_, err := h.svc.Sale(context.Background(), discountedInput(atLine(c.d)))
			wantRefusal(t, err, http.StatusUnprocessableEntity, c.want)
		})
	}
}

// A whole-ticket discount comes off the subtotal after the line discounts and is shared over the
// lines by largest remainder, so the stored lines add up to the charged subtotal exactly.
func TestDiscount_Ticket_SharedOverTheLines(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	in := baseInput()
	in.Lines = append(in.Lines, LineInput{ProductID: "prod-blu", QtyEntered: 100, UOMEntered: "SF"})
	in.Lines[0].Discount = atLine(DiscountInput{AmountCents: 1000, ApprovedBy: mgrSales}) // 9000 + 10000
	in.Discount = atBase(DiscountInput{PercentBps: 1000, ApprovedBy: mgrBranch}, 19000)   // 10% of 19000 = 1900
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.SubtotalCents != 17100 || out.TotalCents != 17100+2223 || out.DiscountCents != 2900 {
		t.Fatalf("want subtotal 17100 and 2900 off, got %+v", out)
	}
	var sum int64
	for _, l := range h.store.orderLines {
		sum += l.LineTotalCents
	}
	if sum != out.SubtotalCents {
		t.Fatalf("stored lines sum %d, charged subtotal %d", sum, out.SubtotalCents)
	}
	if got := h.store.orderLines[0].SaleDiscountCents + h.store.orderLines[1].SaleDiscountCents; got != 1900 {
		t.Fatalf("the ticket discount shares must add to 1900, got %d", got)
	}
	ps := h.store.posSales[0].Discount
	if ps.AmountCents != 1900 || ps.PercentBps != 1000 || ps.ApprovedBy != mgrBranch || ps.RoleID != "branch_manager" {
		t.Fatalf("pos_sale did not store the ticket discount: %+v", ps)
	}
	if len(out.Discounts) != 2 || out.Discounts[1].Scope != DiscountScopeSale || out.Discounts[1].ApprovedByLabel != "Branch Manager" {
		t.Fatalf("want a line and a ticket discount reported, got %+v", out.Discounts)
	}
}

// The ring writes the audit trail and the pricing log of every discount.
func TestDiscount_AuditAndPricingLog(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	in := discountedInput(atLine(DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales}))
	in.Discount = atBase(DiscountInput{AmountCents: 500, ApprovedBy: mgrBranch}, 9000)
	if _, err := h.svc.Sale(context.Background(), in); err != nil {
		t.Fatalf("sale: %v", err)
	}
	var rows []audit.SensitiveAccess
	for _, r := range h.rec.rows {
		if r.Action == audit.ActPosDiscountApproved {
			rows = append(rows, r)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 discount audit rows, got %d", len(rows))
	}
	if rows[0].Extra["approver_id"] != mgrSales || rows[0].Extra["amount_cents"] != int64(1000) || rows[0].ActorID != "" && rows[0].EntityType != "pos.sale" {
		t.Fatalf("bad line audit row: %+v", rows[0])
	}
	if rows[1].Extra["approver_id"] != mgrBranch || rows[1].Extra["amount_cents"] != int64(500) {
		t.Fatalf("bad ticket audit row: %+v", rows[1])
	}
	if len(h.resolver.discountLogs) != 2 {
		t.Fatalf("want 2 pricing log rows, got %d", len(h.resolver.discountLogs))
	}
	if g := h.resolver.discountLogs[0]; g.ApproverID != mgrSales || g.LineNo != 1 || g.AmountCents != 1000 || g.BaseCents != 10000 {
		t.Fatalf("bad line pricing log: %+v", g)
	}
	if g := h.resolver.discountLogs[1]; g.ApproverID != mgrBranch || g.LineNo != 0 || g.AmountCents != 500 {
		t.Fatalf("bad ticket pricing log: %+v", g)
	}
}

// The preview answers the total the ring charges, discount included.
func TestDiscount_PreviewEqualsTheRing(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	in := discountedInput(atLine(DiscountInput{PercentBps: 1250, ApprovedBy: mgrSales}))
	in.Discount = atBase(DiscountInput{AmountCents: 333, ApprovedBy: mgrBranch}, 8750)
	prev, err := h.svc.SalePreview(context.Background(), SalePreviewInput{
		BranchID: in.BranchID, CustomerID: in.CustomerID, Lines: in.Lines, Discount: in.Discount,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	sale, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if prev.SubtotalCents != sale.SubtotalCents || prev.TaxCents != sale.TaxCents || prev.TotalCents != sale.TotalCents ||
		prev.DiscountCents != sale.DiscountCents {
		t.Fatalf("preview %+v is not the ring %+v", prev, sale)
	}
	// The preview answers the figures the till would otherwise have to work out: the ticket's base
	// (the subtotal after the line discounts), the most a ticket discount may take off, and each
	// line's price less its own discount.
	if prev.TicketBaseCents != 8750 || prev.TicketDiscountMaxCents != 8749 || prev.Lines[0].BeforeTicketDiscountCents != 8750-0 {
		t.Fatalf("preview base %d max %d before %d: %+v", prev.TicketBaseCents, prev.TicketDiscountMaxCents, prev.Lines[0].BeforeTicketDiscountCents, prev)
	}
	if len(prev.Lines) != 1 || prev.Lines[0].DiscountCents != 1250 || prev.Lines[0].SaleDiscountCents != 333 || prev.SaleDiscountCents != 333 {
		t.Fatalf("preview lines do not carry the discounts: %+v", prev)
	}
	// And the preview refuses what the ring refuses.
	_, err = h.svc.SalePreview(context.Background(), SalePreviewInput{
		BranchID: in.BranchID, Lines: in.Lines, Discount: atBase(DiscountInput{PercentBps: 500, ApprovedBy: clerkInner}, 10000),
	})
	wantRefusal(t, err, http.StatusUnprocessableEntity, "cannot approve a discount")
}

// An idempotent replay answers the discounts verbatim, read back off the committed sale.
func TestDiscount_ReplayReportsTheDiscounts(t *testing.T) {
	h := newHarness()
	h.store.cached["11111111-1111-4111-8111-111111111111"] = Sale{OrderID: "o-1", TotalCents: 10170, SubtotalCents: 9000}
	h.store.discountReads = []SaleDiscount{{Scope: DiscountScopeLine, LineNo: 1, AmountCents: 1000, ApprovedBy: mgrSales, ApprovedByLabel: "Sales Manager"}}
	out, err := h.svc.Sale(context.Background(), discountedInput(atLine(DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales})))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !out.Replayed || out.DiscountCents != 1000 || len(out.Discounts) != 1 || out.Discounts[0].ApprovedByLabel != "Sales Manager" {
		t.Fatalf("replay lost the discount: %+v", out)
	}
}

// The managers a till person may name: only logins on the dealer's list, one row each, by name.
func TestDiscount_ApproversRead(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	h.store.discountStaff = append(h.store.discountStaff,
		discountStaffRow{UserID: mgrSales, Name: "Sales Mgr", RoleID: "inside_sales", RoleLabel: "Inside Sales", Tier: 3})
	got, err := h.svc.ListDiscountApprovers(context.Background(), "branch-trt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || got[0].ID != mgrBranch || got[0].RoleLabel != "Branch Manager" || got[1].ID != mgrSales {
		t.Fatalf("want [Branch Mgr, Sales Mgr], got %+v", got)
	}
	h.caller.Scope = "branch"
	h.caller.Branches = map[string]bool{"other": true}
	_, err = h.svc.ListDiscountApprovers(context.Background(), "branch-trt")
	wantRefusal(t, err, http.StatusForbidden, "branch")
	_ = errors.New
}

// withClearingOff reloads the decided default with the own-branch clearing setting off.
func withClearingOff(t *testing.T, _ *roleconfig.Config) *roleconfig.Config {
	t.Helper()
	data := bytes.Replace(roleconfig.DefaultData(), []byte(`"manager_own_branch_floor_clearing": true`), []byte(`"manager_own_branch_floor_clearing": false`), 1)
	cfg, err := roleconfig.Load(data)
	if err != nil {
		t.Fatalf("load the default with clearing off: %v", err)
	}
	return cfg
}

// A manager's yes is for the line and quantity it was given. The approval carries the quantity and
// the base it was struck from, the core stores them, and a discount whose approval no longer
// matches the line or the ticket is refused in words even if a client sends it anyway.
func TestDiscount_ApprovalMustMatchTheLineAndTheTicket(t *testing.T) {
	stale := "different quantity or price"
	cases := map[string]struct {
		line   *DiscountInput
		ticket *DiscountInput
		want   string // "" accepted
	}{
		"line at its quantity and base": {line: atLine(DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales})},
		"line quantity grew":            {line: &DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales, ApprovedQty: 10, ApprovedBaseCents: 10000}, want: stale},
		"line price moved":              {line: &DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales, ApprovedQty: 100, ApprovedBaseCents: 9000}, want: stale},
		"line approval absent":          {line: &DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales}, want: "approved for"},
		"ticket at its base":            {ticket: atBase(DiscountInput{AmountCents: 500, ApprovedBy: mgrSales}, 10000)},
		"ticket grew":                   {ticket: atBase(DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales}, 5000), want: stale},
		"ticket approval absent":        {ticket: &DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales}, want: "approved for"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			withApprovers(h)
			in := baseInput()
			in.Lines[0].Discount, in.Discount = c.line, c.ticket
			out, err := h.svc.Sale(context.Background(), in)
			if c.want == "" {
				if err != nil {
					t.Fatalf("a matching approval is accepted: %v", err)
				}
				l := h.store.orderLines[0]
				if c.line != nil && (l.DiscountApprovedQty != 100 || l.DiscountApprovedBase != 10000) {
					t.Fatalf("the approved quantity and base must be stored with the discount: %+v", l)
				}
				if c.ticket != nil && h.store.posSales[0].Discount.ApprovedBase != 10000 {
					t.Fatalf("the ticket's approved base must be stored: %+v", h.store.posSales[0].Discount)
				}
				_ = out
				return
			}
			wantRefusal(t, err, http.StatusUnprocessableEntity, c.want)
			if len(h.store.orders) != 0 {
				t.Fatalf("a stale approval wrote an order")
			}
		})
	}
}

// A discount that leaves nothing to pay is refused in the same words at the preview and the ring
// (the review's P2-3): the preview answered 200 with a zero total and the ring then refused with a
// field name nobody saw. A line at 100 percent beside other lines stays fine.
func TestDiscount_ATicketTotalOfZeroIsRefusedInWordsAtThePreviewAndTheRing(t *testing.T) {
	zero := "cannot total $0.00"
	cases := map[string]struct {
		line, ticket *DiscountInput
		want         string
	}{
		"ticket at 100 percent":        {ticket: atBase(DiscountInput{PercentBps: 10000, ApprovedBy: mgrSales}, 10000), want: zero},
		"ticket amount is the ticket":  {ticket: atBase(DiscountInput{AmountCents: 10000, ApprovedBy: mgrSales}, 10000), want: zero},
		"the only line at 100 percent": {line: atLine(DiscountInput{PercentBps: 10000, ApprovedBy: mgrSales}), want: zero},
		"ticket one cent short":        {ticket: atBase(DiscountInput{AmountCents: 9999, ApprovedBy: mgrSales}, 10000)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			withApprovers(h)
			in := baseInput()
			in.Lines[0].Discount, in.Discount = c.line, c.ticket
			_, ringErr := h.svc.Sale(context.Background(), in)
			_, prevErr := h.svc.SalePreview(context.Background(), SalePreviewInput{
				BranchID: in.BranchID, CustomerID: in.CustomerID, Lines: in.Lines, Discount: in.Discount,
			})
			if c.want == "" {
				if ringErr != nil || prevErr != nil {
					t.Fatalf("one cent to pay is a sale: ring %v preview %v", ringErr, prevErr)
				}
				return
			}
			wantRefusal(t, ringErr, http.StatusUnprocessableEntity, c.want)
			wantRefusal(t, prevErr, http.StatusUnprocessableEntity, c.want)
			if ringErr.Error() != prevErr.Error() {
				t.Fatalf("the preview and the ring must say the same words: %q vs %q", prevErr, ringErr)
			}
			if len(h.store.orders) != 0 || h.tender.called {
				t.Fatalf("a refused ticket wrote or took money")
			}
		})
	}
}

// The audit row names the scope of the discount under "scope" (not "actor_role", which is a role),
// and marks a manager approving their own discount: allowed (they are the manager the SOP asks
// for) but marked, so a trail reader can tell.
func TestDiscount_AuditNamesTheScopeAndMarksASelfApproval(t *testing.T) {
	h := newHarness()
	withApprovers(h)
	ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: mgrSales, RoleCode: "SALES_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})
	in := discountedInput(atLine(DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales}))
	in.Discount = atBase(DiscountInput{AmountCents: 500, ApprovedBy: mgrBranch}, 9000)
	if _, err := h.svc.Sale(ctx, in); err != nil {
		t.Fatalf("a manager may approve their own discount: %v", err)
	}
	var rows []audit.SensitiveAccess
	for _, r := range h.rec.rows {
		if r.Action == audit.ActPosDiscountApproved {
			rows = append(rows, r)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 discount audit rows, got %d", len(rows))
	}
	if _, has := rows[0].Extra["actor_role"]; has {
		t.Fatalf("actor_role holds a role, not the scope: %v", rows[0].Extra)
	}
	if rows[0].Extra["scope"] != "LINE" || rows[1].Extra["scope"] != "SALE" {
		t.Fatalf("the scope belongs under scope: %v %v", rows[0].Extra, rows[1].Extra)
	}
	if rows[0].Extra["self_approved"] != true {
		t.Fatalf("a manager approving their own discount must be marked self approved: %v", rows[0].Extra)
	}
	if _, has := rows[1].Extra["self_approved"]; has {
		t.Fatalf("another manager's approval is not marked: %v", rows[1].Extra)
	}
}
