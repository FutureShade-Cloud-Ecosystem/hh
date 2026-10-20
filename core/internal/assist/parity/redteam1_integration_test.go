//go:build integration

package parity

// Red team case 1 of plan r3 section 7, on the inventory view (slice 6a): a manager without cost asks for
// cost by naming the column, by a spec on the cost view, by an aggregate and by a filter, an order and a group
// on it; each is refused with notice.code = refused and the class word, and none returns a value. A manager
// with cost at ONE branch sees cost only on that branch's rows; masked cells are absent from their rows, not
// blank, and an aggregate or a filter on cost reaches only the branch it may see.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func rt1Rig(t *testing.T) (*assist.ReadQuerier, *testsupport.Env, string, string) {
	t.Helper()
	env := testsupport.Open(t)
	plantStrayStock(t, env)
	br := stockBranches(t, env)
	if len(br) < 2 {
		t.Fatalf("need two stock branches, have %d", len(br))
	}
	return &assist.ReadQuerier{Reader: env.Readers.Staff, Now: time.Now}, env, br[0], br[1]
}

// pbcCost reads the average cost on file for (sku, branch), as the admin, in cents (0 when none).
func pbcCost(t *testing.T, env *testsupport.Env, sku, branch string) int64 {
	t.Helper()
	var c int64
	err := env.Admin.QueryRow(testsupport.Ctx(t), `SELECT COALESCE((SELECT pbc.avg_unit_cost_cents FROM product_branch_costs pbc JOIN products p ON p.id = pbc.product_id
		WHERE p.sku::text = $1 AND pbc.branch_id = $2::uuid), 0)`, sku, branch).Scan(&c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fieldValue(it assist.Item, label string) (string, bool) {
	for _, f := range it.Fields {
		if f.Label == label {
			return f.Value, true
		}
	}
	return "", false
}

func TestRedTeam1_AManagerWithoutCostCannotGetCostByAnySpec(t *testing.T) {
	q, _, a, _ := rt1Rig(t)
	ctx := testsupport.Ctx(t)
	noCost := assist.Caller{UserID: "u-hr", RoleCode: "HR", Tier: 2, BranchIDs: []string{a}} // reach, but no cost branch
	for _, v := range assist.CatalogueFor(noCost) {
		if _, ok := v.Column("avg_cost_cents"); ok {
			t.Fatalf("the catalogue of a caller with no cost carries the cost column of %s", v.Name)
		}
	}
	for name, spec := range map[string]string{
		"the column named":     `{"view":"ai.stock_balances","select":["sku","avg_cost_cents"]}`,
		"a sum":                `{"view":"ai.stock_balances","aggregate":[{"fn":"sum","col":"avg_cost_cents"}]}`,
		"an average by branch": `{"view":"ai.stock_balances","group_by":["branch_label"],"aggregate":[{"fn":"avg","col":"avg_cost_cents"}]}`,
		"a minimum":            `{"view":"ai.stock_balances","aggregate":[{"fn":"min","col":"avg_cost_cents"}]}`,
		"a count of it":        `{"view":"ai.stock_balances","aggregate":[{"fn":"count","col":"avg_cost_cents"}]}`,
		"a filter on it":       `{"view":"ai.stock_balances","select":["sku"],"where":[{"col":"avg_cost_cents","op":">","value":0}]}`,
		"an order on it":       `{"view":"ai.stock_balances","select":["sku"],"order_by":[{"col":"avg_cost_cents","dir":"desc"}]}`,
		"a null test on it":    `{"view":"ai.stock_balances","select":["sku"],"where":[{"col":"avg_cost_cents","op":"is_null","value":false}]}`,
		"between on it":        `{"view":"ai.stock_balances","select":["sku"],"where":[{"col":"avg_cost_cents","op":"between","value":[1,999999]}]}`,
		"a group by on it":     `{"view":"ai.stock_balances","group_by":["avg_cost_cents"],"aggregate":[{"fn":"count"}]}`,
	} {
		res, err := q.Query(ctx, noCost, spec)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if res.Notice == nil || res.Notice.Code != assist.NoticeRefused || res.Notice.Class != "cost" || !res.Empty || len(res.Items) != 0 {
			t.Errorf("%s: want a refusal of class cost with no rows, got %+v", name, res)
		}
		if strings.Contains(fmt.Sprintf("%+v", res), "avg_cost") {
			t.Errorf("%s: the refusal names the column: %+v", name, res)
		}
	}
	// A plain select of the whole row carries no cost at all.
	res, err := q.Query(ctx, noCost, `{"view":"ai.stock_balances","limit":100}`)
	if err != nil || len(res.Items) == 0 {
		t.Fatalf("a plain stock read: %v, %d rows", err, len(res.Items))
	}
	for _, it := range res.Items {
		if _, has := fieldValue(it, "Average cost"); has {
			t.Fatal("a row carries the cost of a caller with no cost branch")
		}
	}
	// Margin has no column in the inventory family; a spec that names one is unknown, never answered.
	if _, err := q.Query(ctx, noCost, `{"view":"ai.stock_balances","select":["margin_pct"]}`); err == nil {
		t.Error("a made up margin column was accepted")
	}
	// Through the service with a scripted provider: the model asks for cost by name and the answer carries the refusal.
	reg := assist.NewRegistry(assist.Deps{Query: q}, nil)
	svc := assist.NewService(reg, &attackProvider{replies: []assist.Reply{
		spec(`{"view":"ai.stock_balances","select":["sku","avg_cost_cents"]}`), {Text: "I cannot show that."},
	}}, noLedger{}, noLimits{}, assist.StaticSettings{}, nil)
	ans, err := svc.Ask(ctx, noCost, "what does the Blu cost us?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ans.Notice == nil || ans.Notice.Code != assist.NoticeRefused || ans.Notice.Class != "cost" {
		t.Errorf("the answer notice = %+v, want refused / cost; answer %+v", ans.Notice, ans)
	}
}

func TestRedTeam1_AManagerWithCostAtOneBranchSeesCostOnlyOnThatBranchsRows(t *testing.T) {
	q, env, a, b := rt1Rig(t)
	ctx := testsupport.Ctx(t)
	oneBranch := assist.Caller{UserID: "u-sm", RoleCode: "SALES_MANAGER", Tier: 2, BranchIDs: []string{a, b}, CostBranchIDs: []string{a}, Margin: true}
	res, err := q.Query(ctx, oneBranch, `{"view":"ai.stock_balances","select":["sku","branch_label","avg_cost_cents"],"limit":100}`)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	rows, err := env.Admin.Query(ctx, `SELECT id::text, name FROM locations`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		labels[id] = name
	}
	rows.Close()
	if labels[a] == labels[b] {
		t.Fatal("the two branches share a label")
	}
	withCost, atA, atB := 0, 0, 0
	for _, it := range res.Items {
		sku, _ := fieldValue(it, "SKU")
		branch, _ := fieldValue(it, "Branch")
		cost, has := fieldValue(it, "Average cost")
		switch branch {
		case labels[a]:
			atA++
			want := pbcCost(t, env, sku, a)
			if (want != 0) != has {
				t.Errorf("%s at a: cost present = %v, on file = %d", sku, has, want)
			}
			if has {
				withCost++
				if cost != fmt.Sprintf("$%d.%02d", want/100, want%100) && !strings.HasPrefix(cost, "$") {
					t.Errorf("%s at a: cost %q", sku, cost)
				}
			}
		case labels[b]:
			atB++
			if has {
				t.Errorf("%s at b carries a cost the asker may not see: %q", sku, cost)
			}
		}
	}
	if atA == 0 || atB == 0 || withCost == 0 {
		t.Fatalf("the world is too thin: rows at a=%d at b=%d with cost=%d", atA, atB, withCost)
	}
	// An aggregate and a filter on cost reach only the branch that may be seen.
	agg, err := q.Query(ctx, oneBranch, `{"view":"ai.stock_balances","group_by":["branch_label"],"aggregate":[{"fn":"max","col":"avg_cost_cents"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range agg.Items {
		branch, _ := fieldValue(it, "Branch")
		if _, has := fieldValue(it, "Highest average cost"); has && branch == labels[b] {
			t.Errorf("a maximum of cost for the branch without cost: %+v", it)
		}
	}
	filt, err := q.Query(ctx, oneBranch, `{"view":"ai.stock_balances","select":["sku","branch_label"],"where":[{"col":"avg_cost_cents","op":">","value":0}],"limit":100}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range filt.Items {
		if branch, _ := fieldValue(it, "Branch"); branch == labels[b] {
			t.Errorf("a filter on cost returned a row of the branch without cost: %+v", it)
		}
	}
	if len(filt.Items) == 0 {
		t.Error("a filter on cost returned nothing for the branch with cost")
	}
}
