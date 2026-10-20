//go:build integration

package parity

// Per record and per column parity for the quote views (plan r3 section 1, slice 6b). For callers built by the
// handler's own path over the dealer's real role list, and for every quote in the database (the seeded ones and
// the test's own, planted at two branches of its own):
//   - the quote is in ai.quotes iff the quote module's own caller scoped read (StaffService.Terms) returns it;
//   - the personal text and the totals equal what that read carries;
//   - each line's margin cells are present iff the quote screen's margin glance (StaffService.MarginGlance, gated
//     by the dealer's cost and margin role list) carries them for the caller, with the same figures.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

type quoteWorld struct {
	a, b string // the test's own two branches
}

// plantQuoteWorld plants two branches, average costs at them, and quotes with lines that exercise every case of
// the margin glance: a line with a cost, a line whose product has no cost row (absent, not a 100 percent
// margin), a line whose cost on file is zero (a real cost of zero), a fractional quantity (round half up), and a
// line with a zero total (absent).
func plantQuoteWorld(t *testing.T, env *testsupport.Env) quoteWorld {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	w := quoteWorld{a: plantBranch(t, env, tag+"A", "S6b fixture A"), b: plantBranch(t, env, tag+"B", "S6b fixture B")}
	var products []string
	rows, err := env.Admin.Query(ctx, `SELECT id::text FROM products ORDER BY sku LIMIT 4`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		products = append(products, p)
	}
	rows.Close()
	if len(products) < 4 {
		t.Fatalf("need four products, have %d", len(products))
	}
	var uom string
	if err := env.Admin.QueryRow(ctx, `SELECT uom_entered::text FROM quote_lines LIMIT 1`).Scan(&uom); err != nil {
		t.Fatal(err)
	}
	for _, br := range []string{w.a, w.b} {
		// products[0] costs 1250, products[1] has no cost row, products[2] costs 0 on file, products[3] costs 333.
		for p, cost := range map[string]int{products[0]: 1250, products[2]: 0, products[3]: 333} {
			if _, err := env.Admin.Exec(ctx, `INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents) VALUES ($1::uuid, $2::uuid, $3)`, p, br, cost); err != nil {
				t.Fatalf("plant a cost: %v", err)
			}
		}
	}
	type line struct {
		product    string
		qty        string
		totalCents int
	}
	lines := []line{{products[0], "2", 5000}, {products[1], "1", 800}, {products[2], "3", 900}, {products[3], "1.5", 1000}, {products[0], "4", 0}}
	for qi := 1; qi <= 4; qi++ {
		for _, br := range []string{w.a, w.b} {
			var qid string
			no := fmt.Sprintf("Q-S6B-%s-%s%d", tag, map[string]string{w.a: "A", w.b: "B"}[br], qi)
			err := env.Admin.QueryRow(ctx, `INSERT INTO quotes (quote_no, customer_id, branch_id, status, contact_name, delivery_address, customer_notes, fulfillment, subtotal_cents, tax_cents, total_cents)
				VALUES ($1, (SELECT customer_id FROM orders LIMIT 1), $2::uuid, $3, $4, $5, $6, 'DELIVERY', 1000, 130, 1130) RETURNING id::text`,
				no, br, []string{"DRAFT", "SENT", "ACCEPTED", "LOST"}[qi-1], "Contact "+no, "1 Planted Road "+no, "note "+no).Scan(&qid)
			if err != nil {
				t.Fatalf("plant quote: %v", err)
			}
			for i, l := range lines {
				if _, err := env.Admin.Exec(ctx, `INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
					VALUES ($1::uuid, $2, $3::uuid, 'planted', $4::numeric, $5, $4::numeric, 100, $5, $6)`, qid, i+1, l.product, l.qty, uom, l.totalCents); err != nil {
					t.Fatalf("plant quote line: %v", err)
				}
			}
		}
	}
	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM quotes WHERE quote_no LIKE $1`, "Q-S6B-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM product_branch_costs WHERE branch_id = ANY($1::uuid[])`, []string{w.a, w.b})
	})
	return w
}

type quoteCells struct{ contact, address, notes, total, subtotal string }

func TestParity_EveryQuoteAndItsMarginFollowTheQuoteModulesOwnReads(t *testing.T) {
	env := testsupport.Open(t)
	w := plantQuoteWorld(t, env)
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
	oracle := quote.NewStaffService(quote.NewRepository(pool)).WithRoleConfig(rc)
	h := assist.NewHandler(nil, nil).WithCostGate(rc, allBranches{{BranchID: w.a}, {BranchID: w.b}})
	callers := map[string]identity.StaffContext{
		"general manager, global":     {UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"},
		"sales manager at a":          scoped("SALES_MANAGER", w.a),
		"sales manager at a and b":    scoped("SALES_MANAGER", w.a, w.b),
		"hr manager at a (off list)":  scoped("HR", w.a),
		"inside sales at b":           scoped("INSIDE_SALES", w.b),
		"sales manager with no grant": scoped("SALES_MANAGER"),
	}
	type quoteRow struct{ id, no, branch string }
	var all []quoteRow
	qrows, err := env.Admin.Query(ctx, `SELECT id::text, quote_no::text, branch_id::text FROM quotes`)
	if err != nil {
		t.Fatal(err)
	}
	for qrows.Next() {
		var q quoteRow
		if err := qrows.Scan(&q.id, &q.no, &q.branch); err != nil {
			t.Fatal(err)
		}
		all = append(all, q)
	}
	qrows.Close()
	if len(all) < 8 {
		t.Fatalf("the quote world holds %d quotes", len(all))
	}
	compared, margins := 0, 0
	for name, sc := range callers {
		req := httptest.NewRequest("POST", "/api/v1/assist/chat", nil).WithContext(identity.ContextWithStaff(ctx, sc))
		c, err := h.CallerFor(req, guards.StaffCaller{})
		if err != nil {
			t.Fatal(err)
		}
		claims, err := c.ReadClaims()
		if err != nil {
			t.Fatal(err)
		}
		inView := map[string]quoteCells{}
		err = env.Readers.Staff.Run(ctx, claims, `SELECT id::text, contact_name, delivery_address, customer_notes, total_cents::text, subtotal_cents::text FROM ai.quotes`, nil, func(rs pgx.Rows) error {
			for rs.Next() {
				var id string
				var q quoteCells
				if err := rs.Scan(&id, &q.contact, &q.address, &q.notes, &q.total, &q.subtotal); err != nil {
					return err
				}
				inView[id] = q
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: read ai.quotes: %v", name, err)
		}
		type marginCells struct{ cost, bps, profit *int64 }
		inLines := map[string]marginCells{} // quote id | line no
		err = env.Readers.Staff.Run(ctx, claims, `SELECT quote_id::text, line_no, line_cost_cents::text, margin_bps::text, est_profit_cents::text FROM ai.quote_lines`, nil, func(rs pgx.Rows) error {
			for rs.Next() {
				var qid string
				var ln int
				var cost, bps, profit *string
				if err := rs.Scan(&qid, &ln, &cost, &bps, &profit); err != nil {
					return err
				}
				toI := func(s *string) *int64 {
					if s == nil {
						return nil
					}
					v, _ := strconv.ParseInt(*s, 10, 64)
					return &v
				}
				inLines[fmt.Sprintf("%s|%d", qid, ln)] = marginCells{toI(cost), toI(bps), toI(profit)}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: read ai.quote_lines: %v", name, err)
		}
		octx := identity.ContextWithStaff(ctx, sc)
		for _, q := range all {
			terms, terr := oracle.Terms(octx, q.id)
			reachable := reaches(c, q.branch)
			if (terr == nil) != reachable {
				t.Fatalf("%s: the quote module's read of %s: err=%v but the caller's reach says reachable=%v", name, q.no, terr, reachable)
			}
			_, in := inView[q.id]
			if in != (terr == nil) {
				t.Errorf("%s: quote %s is in the view = %v but the quote module's read returns it = %v", name, q.no, in, terr == nil)
				continue
			}
			if terr != nil {
				continue
			}
			compared++
			v := inView[q.id]
			if v.contact != terms.ContactName || v.address != terms.DeliveryAddress || v.notes != terms.CustomerNotes ||
				v.total != strconv.FormatInt(terms.TotalCents, 10) || v.subtotal != strconv.FormatInt(terms.SubtotalCents, 10) {
				t.Errorf("%s: %s differs from the quote screen's terms: view %+v, terms %+v", name, q.no, v, terms)
			}
			glance, gerr := oracle.MarginGlance(octx, q.id)
			if gerr != nil {
				t.Fatalf("%s: margin glance of %s: %v", name, q.no, gerr)
			}
			want := map[int]quote.QuoteMarginLine{}
			for _, l := range glance.Lines {
				if l.CostCents != nil {
					want[l.LineNo] = l
				}
			}
			for key, cells := range inLines {
				var qid string
				var ln int
				fmt.Sscanf(key, "%36s|%d", &qid, &ln)
				if len(key) < 37 || key[:36] != q.id {
					continue
				}
				fmt.Sscanf(key[37:], "%d", &ln)
				wl, has := want[ln]
				if (cells.cost != nil) != has {
					t.Errorf("%s: %s line %d margin present = %v, the glance carries it = %v", name, q.no, ln, cells.cost != nil, has)
					continue
				}
				if has {
					margins++
					if *cells.cost != *wl.CostCents || *cells.bps != *wl.MarginBps || *cells.profit != *wl.EstProfitCents {
						t.Errorf("%s: %s line %d margin differs: view %d/%d/%d, glance %d/%d/%d", name, q.no, ln, *cells.cost, *cells.bps, *cells.profit, *wl.CostCents, *wl.MarginBps, *wl.EstProfitCents)
					}
				}
			}
		}
	}
	if compared < 30 || margins < 20 {
		t.Fatalf("too little was compared: %d quotes, %d margin lines", compared, margins)
	}
	_ = readtx.AudienceStaff
}

func reaches(c assist.Caller, branch string) bool {
	if c.Global {
		return true
	}
	for _, b := range c.BranchIDs {
		if b == branch {
			return true
		}
	}
	return false
}
