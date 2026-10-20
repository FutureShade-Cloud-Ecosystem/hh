//go:build integration

package parity

// Per record and per column parity for the purchasing views (plan r3 section 1, slice 6b). Callers are built by
// the handler's own path over the dealer's real role list and the real per branch purchasing policy, read from
// the purchasing_policy table through the purchasing module's own PolicyFor, with the policy set per branch in
// the test: one branch MANAGER_UP, one ALL_STAFF, one HIDDEN. The oracle is the purchasing module's own scoped
// read (Service.GetPO under the caller's staff context). For every purchase order:
//   - the order is in ai.purchase_orders iff the module's read returns it AND the asker is tier 1 or 2 (the
//     assistant's floor, which the purchasing screens do not have: a frontline caller reads orders there and
//     none here, by design, as for orders and quotes);
//   - the plain cells equal the module's, and the subtotal, unit cost, line total and billed cells are present
//     iff the module's cost lens carries them for that caller at that branch, with the same figures.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/adapters"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

type poWorld struct {
	manager, allStaff, hidden string // the test's own branches, one per policy word
}

// plantPOWorld plants three branches with a purchasing policy each and purchase orders at each, with money that
// exercises the rounding (a half cent line) and the billed amount.
func plantPOWorld(t *testing.T, env *testsupport.Env) poWorld {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	w := poWorld{
		manager:  plantBranch(t, env, tag+"M", "S6b PO manager up"),
		allStaff: plantBranch(t, env, tag+"S", "S6b PO all staff"),
		hidden:   plantBranch(t, env, tag+"H", "S6b PO hidden"),
	}
	for br, word := range map[string]string{w.manager: "MANAGER_UP", w.allStaff: "ALL_STAFF", w.hidden: "HIDDEN"} {
		if _, err := env.Admin.Exec(ctx, `INSERT INTO purchasing_policy (branch_id, cost_visibility) VALUES ($1::uuid, $2)`, br, word); err != nil {
			t.Fatalf("plant policy: %v", err)
		}
	}
	var vendor string
	if err := env.Admin.QueryRow(ctx, `SELECT id::text FROM vendors ORDER BY vendor_no LIMIT 1`).Scan(&vendor); err != nil {
		t.Fatal(err)
	}
	type prod struct{ id, uom string }
	var products []prod
	rows, err := env.Admin.Query(ctx, `SELECT id::text, base_uom::text FROM products ORDER BY sku LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p prod
		if err := rows.Scan(&p.id, &p.uom); err != nil {
			t.Fatal(err)
		}
		products = append(products, p)
	}
	rows.Close()
	if len(products) < 3 {
		t.Fatalf("need three products, have %d", len(products))
	}
	type line struct {
		qty, received string
		unit, billed  int
	}
	lines := []line{{"10", "4", 1250, 5000}, {"1.5", "0", 333, 0}, {"2.5", "2.5", 101, 253}}
	for bi, br := range []string{w.manager, w.allStaff, w.hidden} {
		for n := 1; n <= 2; n++ {
			var po string
			no := fmt.Sprintf("PO-S6B-%s-%d%d", tag, bi, n)
			if err := env.Admin.QueryRow(ctx, `INSERT INTO purchasing_pos (po_no, vendor_id, branch_id, status, notes) VALUES ($1, $2::uuid, $3::uuid, 'ORDERED', $4) RETURNING id::text`,
				no, vendor, br, "buyer note "+no).Scan(&po); err != nil {
				t.Fatalf("plant po: %v", err)
			}
			for i, l := range lines {
				if _, err := env.Admin.Exec(ctx, `INSERT INTO purchasing_po_lines (po_id, line_no, product_id, description, qty_ordered_base, uom, unit_cost_cents, qty_received_base, billed_cents)
					VALUES ($1::uuid, $2, $3::uuid, 'planted', $4::numeric, $5, $6, $7::numeric, $8)`,
					po, i+1, products[i].id, l.qty, products[i].uom, l.unit, l.received, l.billed); err != nil {
					t.Fatalf("plant po line: %v", err)
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
		_, _ = c.Exec(context.Background(), `DELETE FROM purchasing_pos WHERE po_no LIKE $1`, "PO-S6B-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM purchasing_policy WHERE branch_id = ANY($1::uuid[])`, []string{w.manager, w.allStaff, w.hidden})
	})
	return w
}

func (w poWorld) all() []string { return []string{w.manager, w.allStaff, w.hidden} }

// poCallers are the askers the purchasing parity runs over. The frontline caller reads purchase orders on the
// purchasing screens (at ALL_STAFF with cost) and none through the assistant, whose floor is tier 2.
func poCallers(w poWorld) map[string]identity.StaffContext {
	front := scoped("YARD", w.allStaff)
	front.Tier = identity.TierFrontline
	return map[string]identity.StaffContext{
		"general manager, global":     {UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"},
		"manager at all three":        scoped("SALES_MANAGER", w.all()...),
		"manager at manager-up only":  scoped("SALES_MANAGER", w.manager),
		"manager at all-staff only":   scoped("SALES_MANAGER", w.allStaff),
		"manager at hidden only":      scoped("SALES_MANAGER", w.hidden),
		"frontline at all-staff":      front,
		"manager with no grant":       scoped("SALES_MANAGER"),
		"manager at hidden and other": scoped("HR", w.hidden, w.allStaff),
	}
}

func i64p(s *string) *int64 {
	if s == nil {
		return nil
	}
	v, _ := strconv.ParseInt(*s, 10, 64)
	return &v
}

func eqP(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestParity_EveryPurchaseOrderAndItsCostFollowThePurchasingModulesOwnRead(t *testing.T) {
	env := testsupport.Open(t)
	w := plantPOWorld(t, env)
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
	svc := purchasing.NewService(purchasing.NewRepository(pool))
	h := assist.NewHandler(nil, nil).
		WithCostGate(rc, allBranches{{BranchID: w.manager}, {BranchID: w.allStaff}, {BranchID: w.hidden}}).
		WithPurchaseCostPolicy(adapters.NewPurchaseCostPolicy(svc))

	type poRow struct{ id, no, branch string }
	var all []poRow
	prs, err := env.Admin.Query(ctx, `SELECT id::text, po_no::text, branch_id::text FROM purchasing_pos`)
	if err != nil {
		t.Fatal(err)
	}
	for prs.Next() {
		var p poRow
		if err := prs.Scan(&p.id, &p.no, &p.branch); err != nil {
			t.Fatal(err)
		}
		all = append(all, p)
	}
	prs.Close()
	if len(all) < 6 {
		t.Fatalf("the purchasing world holds %d orders", len(all))
	}

	compared, costed, hiddenSeen := 0, 0, 0
	for name, sc := range poCallers(w) {
		req := newReq(ctx, sc)
		c, err := h.CallerFor(req, guards.StaffCaller{})
		if err != nil {
			t.Fatal(err)
		}
		claims, err := c.ReadClaims()
		if err != nil {
			t.Fatal(err)
		}
		type hdr struct {
			no, status, notes, currency, delivery string
			lineCount                             int
			subtotal                              *int64
		}
		heads := map[string]hdr{}
		err = env.Readers.Staff.Run(ctx, claims, `SELECT id::text, po_no, status, notes, currency, delivery_method, line_count, subtotal_cents::text FROM ai.purchase_orders`, nil, func(rs pgx.Rows) error {
			for rs.Next() {
				var id string
				var x hdr
				var sub *string
				if err := rs.Scan(&id, &x.no, &x.status, &x.notes, &x.currency, &x.delivery, &x.lineCount, &sub); err != nil {
					return err
				}
				x.subtotal = i64p(sub)
				heads[id] = x
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: read ai.purchase_orders: %v", name, err)
		}
		type cell struct {
			sku, uom, desc            string
			ordered, received, billed string
			outstanding               string
			unit, total, billedCents  *int64
		}
		cells := map[string]cell{}
		err = env.Readers.Staff.Run(ctx, claims, `SELECT po_id::text, line_no, sku, uom, description, qty_ordered_base::text, qty_received_base::text, qty_billed_base::text, qty_outstanding_base::text, unit_cost_cents::text, line_total_cents::text, billed_cents::text FROM ai.purchase_order_lines`, nil, func(rs pgx.Rows) error {
			for rs.Next() {
				var id string
				var ln int
				var x cell
				var u, tt, b *string
				if err := rs.Scan(&id, &ln, &x.sku, &x.uom, &x.desc, &x.ordered, &x.received, &x.billed, &x.outstanding, &u, &tt, &b); err != nil {
					return err
				}
				x.unit, x.total, x.billedCents = i64p(u), i64p(tt), i64p(b)
				cells[fmt.Sprintf("%s|%d", id, ln)] = x
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: read ai.purchase_order_lines: %v", name, err)
		}
		octx := identity.ContextWithStaff(ctx, sc)
		floor := sc.Tier == identity.TierLeadership || sc.Tier == identity.TierManager
		for _, p := range all {
			d, derr := svc.GetPO(octx, p.id)
			if (derr == nil) != reaches(c, p.branch) {
				t.Fatalf("%s: the purchasing module's read of %s: err=%v but the caller's reach says %v", name, p.no, derr, reaches(c, p.branch))
			}
			head, in := heads[p.id]
			want := derr == nil && floor
			if in != want {
				t.Errorf("%s: order %s is in the view = %v, want %v (module read ok=%v, tier floor met=%v)", name, p.no, in, want, derr == nil, floor)
				continue
			}
			if !want {
				for key := range cells {
					if len(key) > 36 && key[:36] == p.id {
						t.Errorf("%s: a line of %s is in ai.purchase_order_lines though the order is not readable", name, p.no)
					}
				}
				continue
			}
			compared++
			if head.no != d.PONo || head.status != d.Status || head.notes != d.Notes || head.currency != d.Currency || head.delivery != d.DeliveryMethod || head.lineCount != len(d.Lines) {
				t.Errorf("%s: %s header differs: view %+v, module %+v", name, p.no, head, d.PO)
			}
			if !eqP(head.subtotal, d.POCosts.Subtotal) {
				t.Errorf("%s: %s subtotal view=%v module=%v (branch %s)", name, p.no, head.subtotal, d.POCosts.Subtotal, p.branch)
			}
			if d.POCosts.Subtotal != nil {
				costed++
			} else if p.branch == w.hidden {
				hiddenSeen++
			}
			for _, l := range d.Lines {
				x, ok := cells[fmt.Sprintf("%s|%d", p.id, l.LineNo)]
				if !ok {
					t.Errorf("%s: %s line %d is missing from the view", name, p.no, l.LineNo)
					continue
				}
				if x.sku != l.SKU || x.uom != l.UOM || x.desc != l.Description ||
					!sameNum(t, x.ordered, l.QtyOrderedBase) || !sameNum(t, x.received, l.QtyReceivedBase) || !sameNum(t, x.billed, l.QtyBilledBase) || !sameNum(t, x.outstanding, l.QtyOutstanding) {
					t.Errorf("%s: %s line %d plain cells differ: view %+v, module %+v", name, p.no, l.LineNo, x, l)
				}
				if !eqP(x.unit, l.POCosts.UnitCost) || !eqP(x.total, l.POCosts.LineTotal) || !eqP(x.billedCents, l.POCosts.Billed) {
					t.Errorf("%s: %s line %d cost cells differ: view %v/%v/%v, module %v/%v/%v", name, p.no, l.LineNo, x.unit, x.total, x.billedCents, l.POCosts.UnitCost, l.POCosts.LineTotal, l.POCosts.Billed)
				}
			}
		}
	}
	if compared < 20 || costed < 10 || hiddenSeen < 2 {
		t.Fatalf("too little was compared: %d orders, %d with cost, %d at the hidden branch without", compared, costed, hiddenSeen)
	}
	_ = readtx.AudienceStaff
}

func sameNum(t *testing.T, s string, f float64) bool {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return same(v, f)
}

func newReq(ctx context.Context, sc identity.StaffContext) *http.Request {
	return httptest.NewRequest("POST", "/api/v1/assist/chat", nil).WithContext(identity.ContextWithStaff(ctx, sc))
}
