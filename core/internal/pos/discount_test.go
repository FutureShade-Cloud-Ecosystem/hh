package pos

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
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
	out, err := h.svc.Sale(context.Background(), discountedInput(&DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales}))
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
	out, err := h.svc.Sale(context.Background(), discountedInput(&DiscountInput{AmountCents: 2500, ApprovedBy: mgrBranch}))
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
	_, err := h.svc.Sale(context.Background(), discountedInput(&DiscountInput{PercentBps: 500}))
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
			_, err := h.svc.Sale(context.Background(), discountedInput(&DiscountInput{PercentBps: 500, ApprovedBy: id}))
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
	_, err := h.svc.Sale(context.Background(), discountedInput(&DiscountInput{PercentBps: 500, ApprovedBy: mgrSales}))
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
			d := c.d
			_, err := h.svc.Sale(context.Background(), discountedInput(&d))
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
	in.Lines[0].Discount = &DiscountInput{AmountCents: 1000, ApprovedBy: mgrSales} // 9000 + 10000
	in.Discount = &DiscountInput{PercentBps: 1000, ApprovedBy: mgrBranch}          // 10% of 19000 = 1900
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
	in := discountedInput(&DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales})
	in.Discount = &DiscountInput{AmountCents: 500, ApprovedBy: mgrBranch}
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
	in := discountedInput(&DiscountInput{PercentBps: 1250, ApprovedBy: mgrSales})
	in.Discount = &DiscountInput{AmountCents: 333, ApprovedBy: mgrBranch}
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
	if len(prev.Lines) != 1 || prev.Lines[0].DiscountCents != 1250 || prev.Lines[0].SaleDiscountCents != 333 || prev.SaleDiscountCents != 333 {
		t.Fatalf("preview lines do not carry the discounts: %+v", prev)
	}
	// And the preview refuses what the ring refuses.
	_, err = h.svc.SalePreview(context.Background(), SalePreviewInput{
		BranchID: in.BranchID, Lines: in.Lines, Discount: &DiscountInput{PercentBps: 500, ApprovedBy: clerkInner},
	})
	wantRefusal(t, err, http.StatusUnprocessableEntity, "cannot approve a discount")
}

// An idempotent replay answers the discounts verbatim, read back off the committed sale.
func TestDiscount_ReplayReportsTheDiscounts(t *testing.T) {
	h := newHarness()
	h.store.cached["11111111-1111-4111-8111-111111111111"] = Sale{OrderID: "o-1", TotalCents: 10170, SubtotalCents: 9000}
	h.store.discountReads = []SaleDiscount{{Scope: DiscountScopeLine, LineNo: 1, AmountCents: 1000, ApprovedBy: mgrSales, ApprovedByLabel: "Sales Manager"}}
	out, err := h.svc.Sale(context.Background(), discountedInput(&DiscountInput{PercentBps: 1000, ApprovedBy: mgrSales}))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !out.Replayed || out.DiscountCents != 1000 || len(out.Discounts) != 1 || out.Discounts[0].ApprovedByLabel != "Sales Manager" {
		t.Fatalf("replay lost the discount: %+v", out)
	}
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
