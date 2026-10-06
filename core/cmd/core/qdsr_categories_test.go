package main

// qdsr_categories_test.go, the product categories read (Quote Desk server reads, read five): over
// the registered surface and real rows, the read answers id, code, name and is_active for every
// category in code order, pages by the code cursor with no row repeated or dropped, refuses a
// malformed cursor with a 400, and sits behind the grant the pricing reads already use (Dealer Admin
// at read): inside sales, who holds no Dealer Admin cell, meets the gate's 403. There is no by-id
// leg because the read is a list; its refusal legs are the grant and the cursor.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	qdsrCatA = "5d5a0000-0000-4000-8000-0000000000c1"
	qdsrCatB = "5d5a0000-0000-4000-8000-0000000000c2"
	qdsrCatC = "5d5a0000-0000-4000-8000-0000000000c3"
	qdsrCatD = "5d5a0000-0000-4000-8000-0000000000c4" // stamped to the other org
)

func qdsrPlantCategories(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO product_categories (id, code, name, is_active) VALUES
		($1, 'QDSR-A', 'Fixture Pavers', true),
		($2, 'QDSR-B', 'Fixture Walls', true),
		($3, 'QDSR-C', 'Fixture Retired', false)`, qdsrCatA, qdsrCatB, qdsrCatC); err != nil {
		t.Fatalf("plant categories: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO product_categories (id, code, name, is_active, org_id)
		VALUES ($1, 'QDSR-D', 'Fixture Other Org', true, $2)`, qdsrCatD, testkit.OrgSouthID); err != nil {
		t.Fatalf("plant the other org's category: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM product_categories WHERE id = ANY($1::uuid[])`,
			[]string{qdsrCatA, qdsrCatB, qdsrCatC, qdsrCatD}); err != nil {
			t.Errorf("cleanup categories: %v", err)
		}
	})
}

type qdsrCategoryPage struct {
	Items []struct {
		ID       string `json:"id"`
		Code     string `json:"code"`
		Name     string `json:"name"`
		IsActive bool   `json:"is_active"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func qdsrCategories(t *testing.T, fx *xrFixture, sub, query string) qdsrCategoryPage {
	t.Helper()
	code, body := qdsrGet(t, fx, sub, "/api/v1/product-categories"+query)
	if code != http.StatusOK {
		t.Fatalf("categories%s answers %d %s, want 200", query, code, body)
	}
	var page qdsrCategoryPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return page
}

func TestQDSRCategoriesReadPagesByCodeBehindTheDealerAdminGrant(t *testing.T) {
	fx := newXRoleFixture(t)
	pool := fx.env.fixturePool()
	t.Cleanup(pool.Close)
	qdsrPlantCategories(t, pool)

	// The whole list carries the planted rows with their fields, retired one included and marked.
	all := qdsrCategories(t, fx, testkit.SubSalesManager, "?limit=200")
	seen := map[string]bool{}
	for _, c := range all.Items {
		seen[c.ID] = true
		switch c.ID {
		case qdsrCatA:
			if c.Code != "QDSR-A" || c.Name != "Fixture Pavers" || !c.IsActive {
				t.Fatalf("category A reads %+v", c)
			}
		case qdsrCatC:
			if c.Code != "QDSR-C" || c.IsActive {
				t.Fatalf("a retired category says so: %+v", c)
			}
		}
	}
	if !seen[qdsrCatA] || !seen[qdsrCatB] || !seen[qdsrCatC] {
		t.Fatalf("planted categories missing from the list: %+v", all.Items)
	}
	if seen[qdsrCatD] {
		t.Fatalf("another org's category reached this org's caller: %+v", all.Items)
	}
	codes := make([]string, len(all.Items))
	for i, c := range all.Items {
		codes[i] = c.Code
	}
	if !sort.StringsAreSorted(codes) {
		t.Fatalf("the list is not in code order: %v", codes)
	}

	// A walk at limit one visits every row once, in the same order.
	var walked []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > len(all.Items)+1 {
			t.Fatal("the walk ran past the list: the cursor does not advance")
		}
		q := "?limit=1"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		p := qdsrCategories(t, fx, testkit.SubSalesManager, q)
		for _, c := range p.Items {
			walked = append(walked, c.Code)
		}
		if p.NextCursor == nil || *p.NextCursor == "" {
			break
		}
		cursor = *p.NextCursor
	}
	if len(walked) != len(codes) {
		t.Fatalf("the walk answered %d rows, the whole list %d", len(walked), len(codes))
	}
	for i := range walked {
		if walked[i] != codes[i] {
			t.Fatalf("the walk differs from the list at %d: %v vs %v", i, walked, codes)
		}
	}

	// The grant is the pricing reads' own: Dealer Admin at read. These roles hold it.
	for _, sub := range []string{testkit.SubGeneralMgr, xrSubHR, xrSubFinancialController} {
		qdsrCategories(t, fx, sub, "?limit=1")
	}
	// These do not, and meet the gate's refusal.
	for _, sub := range []string{testkit.SubInsideSales, testkit.SubDriver, xrSubYardTeam} {
		code, body := qdsrGet(t, fx, sub, "/api/v1/product-categories")
		rgRefused(t, sub+" is held off the categories read", code, body)
	}
	// The annotation is exactly the margin policy grid's and the pricing rules read's, from the
	// document: the Sales manager's Pricing tab picker holds it, a plain Sales role does not.
	byOp := map[string]apigen.Route{}
	for _, r := range apigen.Routes {
		byOp[r.OperationID] = r
	}
	cat := byOp["productListCategories"]
	for _, op := range []string{"pricingListCategoryPolicies", "pricingListRules"} {
		ref := byOp[op]
		if cat.XRole != ref.XRole || cat.Level != ref.Level || cat.List != ref.List || strings.Join(cat.Surfaces, ",") != strings.Join(ref.Surfaces, ",") {
			t.Fatalf("categories annotation %+v differs from %s %+v", cat, op, ref)
		}
	}
	// The pricing read the grant is borrowed from agrees on the same roles.
	for _, sub := range []string{testkit.SubInsideSales, testkit.SubDriver} {
		code, body := qdsrGet(t, fx, sub, "/api/v1/pricing/category-policies")
		rgRefused(t, sub+" is held off the category policy grid", code, body)
	}

	// A cursor nobody minted is a 400, never a server fault.
	code, body := qdsrGet(t, fx, testkit.SubSalesManager, "/api/v1/product-categories?cursor=not-a-cursor")
	if code != http.StatusBadRequest {
		t.Fatalf("a forged cursor answers %d %s, want 400", code, body)
	}
}
