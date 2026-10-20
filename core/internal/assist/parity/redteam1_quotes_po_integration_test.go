//go:build integration

package parity

// Red team case 1 of plan r3 section 7, on the quote and purchasing views (slice 6b). The margin leg: a manager
// off the dealer's cost and margin role list asks for margin by naming the column, by a spec, an aggregate, a
// filter, an order and a group on it; each is refused with the class word and none returns a value, and a plain
// read of the lines carries no margin cell. The purchase cost leg: callers built by the handler's own path over
// the real per branch purchasing policy (MANAGER_UP, ALL_STAFF, HIDDEN set per branch in the test): at the
// HIDDEN branch the cost is absent for every asker, at the ALL_STAFF branch it is present.

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/adapters"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

func TestRedTeam1_AManagerOffTheMarginListCannotGetMarginByAnySpec(t *testing.T) {
	env := testsupport.Open(t)
	w := plantQuoteWorld(t, env)
	q := &assist.ReadQuerier{Reader: env.Readers.Staff, Now: time.Now}
	ctx := testsupport.Ctx(t)
	rc, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	h := assist.NewHandler(nil, nil).WithCostGate(rc, everyBranch(t, env))
	build := func(sc identity.StaffContext) assist.Caller {
		req := httptest.NewRequest("POST", "/api/v1/assist/chat", nil).WithContext(identity.ContextWithStaff(ctx, sc))
		c, err := h.CallerFor(req, guards.StaffCaller{})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	off, on := build(scoped("HR", w.a)), build(scoped("SALES_MANAGER", w.a))
	if off.Margin || !on.Margin {
		t.Fatalf("the callers are not what the test needs: off.Margin=%v on.Margin=%v", off.Margin, on.Margin)
	}
	for _, v := range assist.CatalogueFor(off) {
		for _, col := range []string{"line_cost_cents", "margin_bps", "est_profit_cents"} {
			if _, ok := v.Column(col); ok {
				t.Fatalf("the catalogue of a caller off the margin list carries %s of %s", col, v.Name)
			}
		}
	}
	for name, spec := range map[string]string{
		"the column named":    `{"view":"ai.quote_lines","select":["quote_no","margin_bps"]}`,
		"the profit named":    `{"view":"ai.quote_lines","select":["quote_no","est_profit_cents"]}`,
		"the cost named":      `{"view":"ai.quote_lines","select":["quote_no","line_cost_cents"]}`,
		"a sum of profit":     `{"view":"ai.quote_lines","aggregate":[{"fn":"sum","col":"est_profit_cents"}]}`,
		"an average by quote": `{"view":"ai.quote_lines","group_by":["quote_no"],"aggregate":[{"fn":"avg","col":"margin_bps"}]}`,
		"a filter on it":      `{"view":"ai.quote_lines","select":["quote_no"],"where":[{"col":"margin_bps","op":">","value":0}]}`,
		"an order on it":      `{"view":"ai.quote_lines","select":["quote_no"],"order_by":[{"col":"margin_bps","dir":"desc"}]}`,
		"a null test on it":   `{"view":"ai.quote_lines","select":["quote_no"],"where":[{"col":"margin_bps","op":"is_null","value":false}]}`,
		"a group by on it":    `{"view":"ai.quote_lines","group_by":["margin_bps"],"aggregate":[{"fn":"count"}]}`,
	} {
		res, err := q.Query(ctx, off, spec)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if res.Notice == nil || res.Notice.Code != assist.NoticeRefused || res.Notice.Class != "margin" && res.Notice.Class != "cost" || !res.Empty || len(res.Items) != 0 {
			t.Errorf("%s: want a refusal of class margin (or cost) with no rows, got %+v", name, res)
		}
		if s := fmt.Sprintf("%+v", res); strings.Contains(s, "margin_bps") || strings.Contains(s, "est_profit") {
			t.Errorf("%s: the refusal names the column: %s", name, s)
		}
	}
	plain := `{"view":"ai.quote_lines","where":[{"col":"quote_no","op":"contains","value":"S6B"}],"limit":100}`
	res, err := q.Query(ctx, off, plain)
	if err != nil || len(res.Items) == 0 {
		t.Fatalf("a plain read of the lines: %v, %d rows", err, len(res.Items))
	}
	for _, it := range res.Items {
		for _, label := range []string{"Margin (basis points)", "Estimated profit", "Line cost"} {
			if _, has := fieldValue(it, label); has {
				t.Fatalf("a line carries %q for a caller off the margin list", label)
			}
		}
	}
	// The positive control: a caller on the list names the column and gets figures, line by line.
	res, err = q.Query(ctx, on, `{"view":"ai.quote_lines","select":["quote_no","line_no","margin_bps"],"where":[{"col":"quote_no","op":"contains","value":"S6B"}],"limit":100}`)
	if err != nil || res.Notice != nil && res.Notice.Code == assist.NoticeRefused {
		t.Fatalf("a caller on the margin list: %v %+v", err, res.Notice)
	}
	with := 0
	for _, it := range res.Items {
		if _, has := fieldValue(it, "Margin (basis points)"); has {
			with++
		}
	}
	if with == 0 {
		t.Fatal("a caller on the margin list sees no margin on any planted line")
	}
	// An aggregate over margin for the caller on the list works, and for the one off it was refused above.
	agg, err := q.Query(ctx, on, `{"view":"ai.quote_lines","aggregate":[{"fn":"max","col":"margin_bps"}]}`)
	if err != nil || agg.Notice != nil && agg.Notice.Code == assist.NoticeRefused || len(agg.Items) == 0 {
		t.Errorf("a maximum of margin for a caller on the list: %v %+v", err, agg)
	}
}

func TestRedTeam1_PurchaseCostFollowsTheBranchPolicyOnEveryPath(t *testing.T) {
	env := testsupport.Open(t)
	w := plantPOWorld(t, env)
	q := &assist.ReadQuerier{Reader: env.Readers.Staff, Now: time.Now}
	ctx := testsupport.Ctx(t)
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	rc, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	h := assist.NewHandler(nil, nil).
		WithCostGate(rc, everyBranch(t, env)).
		WithPurchaseCostPolicy(adapters.NewPurchaseCostPolicy(purchasing.NewService(purchasing.NewRepository(pool))))
	build := func(sc identity.StaffContext) assist.Caller {
		req := httptest.NewRequest("POST", "/api/v1/assist/chat", nil).WithContext(identity.ContextWithStaff(ctx, sc))
		c, err := h.CallerFor(req, guards.StaffCaller{})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	atHidden := build(scoped("SALES_MANAGER", w.hidden))
	atAllStaff := build(scoped("SALES_MANAGER", w.allStaff))
	if len(atHidden.POCostBranchIDs) != 0 || len(atAllStaff.POCostBranchIDs) != 1 || atAllStaff.POCostBranchIDs[0] != w.allStaff {
		t.Fatalf("the callers are not what the test needs: hidden %v, all staff %v", atHidden.POCostBranchIDs, atAllStaff.POCostBranchIDs)
	}
	for _, v := range assist.CatalogueFor(atHidden) {
		for _, col := range []string{"subtotal_cents", "unit_cost_cents", "line_total_cents", "billed_cents"} {
			if _, ok := v.Column(col); ok && strings.HasPrefix(v.Name, "ai.purchase_order") {
				t.Fatalf("the catalogue of a buyer at a HIDDEN branch carries %s of %s", col, v.Name)
			}
		}
	}
	for name, spec := range map[string]string{
		"subtotal named":  `{"view":"ai.purchase_orders","select":["po_no","subtotal_cents"]}`,
		"a sum of it":     `{"view":"ai.purchase_orders","aggregate":[{"fn":"sum","col":"subtotal_cents"}]}`,
		"unit cost named": `{"view":"ai.purchase_order_lines","select":["po_no","unit_cost_cents"]}`,
		"line total sum":  `{"view":"ai.purchase_order_lines","group_by":["po_no"],"aggregate":[{"fn":"sum","col":"line_total_cents"}]}`,
		"billed filter":   `{"view":"ai.purchase_order_lines","select":["po_no"],"where":[{"col":"billed_cents","op":">","value":0}]}`,
		"an order on it":  `{"view":"ai.purchase_orders","select":["po_no"],"order_by":[{"col":"subtotal_cents","dir":"desc"}]}`,
	} {
		res, err := q.Query(ctx, atHidden, spec)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if res.Notice == nil || res.Notice.Code != assist.NoticeRefused || res.Notice.Class != "cost" || !res.Empty || len(res.Items) != 0 {
			t.Errorf("%s: want a refusal of class cost with no rows, got %+v", name, res)
		}
	}
	// The plain order list still answers, with the orders and no cost on any row.
	list := `{"view":"ai.purchase_orders","where":[{"col":"po_no","op":"contains","value":"PO-S6B"}],"limit":100}`
	res, err := q.Query(ctx, atHidden, list)
	if err != nil || len(res.Items) == 0 {
		t.Fatalf("a plain order list at the hidden branch: %v, %d rows, %+v", err, len(res.Items), res)
	}
	for _, it := range res.Items {
		if _, has := fieldValue(it, "Subtotal"); has {
			t.Fatalf("an order carries a subtotal for a buyer at a HIDDEN branch: %+v", it)
		}
	}
	// The ALL_STAFF branch: present, on every one of its orders, and aggregates reach it.
	res, err = q.Query(ctx, atAllStaff, `{"view":"ai.purchase_orders","select":["po_no","subtotal_cents"],"where":[{"col":"po_no","op":"contains","value":"PO-S6B"}],"limit":100}`)
	if err != nil || len(res.Items) == 0 || res.Notice != nil && res.Notice.Code == assist.NoticeRefused {
		t.Fatalf("a buyer at the ALL_STAFF branch: %v %d rows %+v", err, len(res.Items), res.Notice)
	}
	for _, it := range res.Items {
		if _, has := fieldValue(it, "Subtotal"); !has {
			t.Errorf("an order at the ALL_STAFF branch carries no subtotal: %+v", it)
		}
	}
	// A manager reaching all three branches sees cost on MANAGER_UP and ALL_STAFF rows only, row by row.
	all := build(scoped("SALES_MANAGER", w.all()...))
	res, err = q.Query(ctx, all, `{"view":"ai.purchase_orders","select":["po_no","branch_label","subtotal_cents"],"where":[{"col":"po_no","op":"contains","value":"PO-S6B"}],"limit":100}`)
	if err != nil {
		t.Fatal(err)
	}
	var names = map[string]string{}
	rows, err := env.Admin.Query(ctx, `SELECT id::text, name FROM locations WHERE id = ANY($1::uuid[])`, w.all())
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, n string
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
		names[n] = id
	}
	rows.Close()
	seenHidden, seenCost := 0, 0
	for _, it := range res.Items {
		branch, _ := fieldValue(it, "Branch")
		_, has := fieldValue(it, "Subtotal")
		switch names[branch] {
		case w.hidden:
			seenHidden++
			if has {
				t.Errorf("an order at the HIDDEN branch carries a subtotal: %+v", it)
			}
		case w.manager, w.allStaff:
			seenCost++
			if !has {
				t.Errorf("an order at a branch whose policy allows the manager carries no subtotal: %+v", it)
			}
		}
	}
	if seenHidden == 0 || seenCost == 0 {
		t.Fatalf("the world is too thin: hidden rows %d, costed rows %d", seenHidden, seenCost)
	}
	// An aggregate reaches only the branches that may be seen.
	agg, err := q.Query(ctx, all, `{"view":"ai.purchase_orders","group_by":["branch_label"],"aggregate":[{"fn":"max","col":"subtotal_cents"}],"where":[{"col":"po_no","op":"contains","value":"PO-S6B"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range agg.Items {
		branch, _ := fieldValue(it, "Branch")
		if _, has := fieldValue(it, "Highest Subtotal"); has && names[branch] == w.hidden {
			t.Errorf("a maximum of cost for the HIDDEN branch: %+v", it)
		}
	}
}
