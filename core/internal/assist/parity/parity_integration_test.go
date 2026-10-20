//go:build integration

package parity

// Per record parity (plan r3 section 1, review r1 P2-4): for every caller in the matrix and every seeded
// order and line, the record is in the view if and only if the order module's single record read returns
// it for that caller. Comparing lists would not do: a screen applies default filters and pagination, so a
// view can match a list and still reach rows the record read refuses by id.
//
// The oracle is order.Service.SummaryByNumber over the same ReadScope the assist adapter builds
// (adapters.orderReach): the module's own caller scoped read of one order by its visible number. The
// view side is read two ways: raw SQL as the staff reader under the signed claim, and through data.query.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

type seededOrder struct {
	id, no, branch string
	lines          []string
}

func seededWorld(t *testing.T, env *testsupport.Env) []seededOrder {
	t.Helper()
	ctx := testsupport.Ctx(t)
	rows, err := env.Admin.Query(ctx, `SELECT id::text, order_no::text, branch_id::text FROM orders ORDER BY order_no`)
	if err != nil {
		t.Fatal(err)
	}
	var out []seededOrder
	byID := map[string]int{}
	for rows.Next() {
		var o seededOrder
		if err := rows.Scan(&o.id, &o.no, &o.branch); err != nil {
			t.Fatal(err)
		}
		byID[o.id] = len(out)
		out = append(out, o)
	}
	rows.Close()
	lrows, err := env.Admin.Query(ctx, `SELECT id::text, order_id::text FROM order_lines`)
	if err != nil {
		t.Fatal(err)
	}
	defer lrows.Close()
	for lrows.Next() {
		var id, oid string
		if err := lrows.Scan(&id, &oid); err != nil {
			t.Fatal(err)
		}
		if i, ok := byID[oid]; ok {
			out[i].lines = append(out[i].lines, id)
		}
	}
	return out
}

// callers is the matrix: leadership global, a manager scoped to each branch that holds orders, a manager
// with grants at both, and a manager with no grants at all. Tier 3 is refused by the tier floor and
// tested separately.
func callers(branches []string) map[string]assist.Caller {
	m := map[string]assist.Caller{
		"tier 1 global":        {UserID: "u-t1", RoleCode: "GENERAL_MANAGER", Tier: 1, Global: true, IsAdmin: true},
		"tier 2, no grants":    {UserID: "u-t2-none", RoleCode: "SALES_MANAGER", Tier: 2},
		"tier 2, every branch": {UserID: "u-t2-all", RoleCode: "SALES_MANAGER", Tier: 2, BranchIDs: branches},
	}
	for _, b := range branches {
		m["tier 2, branch "+b[len(b)-4:]] = assist.Caller{UserID: "u-t2-" + b[len(b)-4:], RoleCode: "SALES_MANAGER", Tier: 2, BranchIDs: []string{b}}
	}
	return m
}

func setOf(ids []string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func stringsFrom(t *testing.T, r *readtx.Reader, c assist.Caller, sql string) []string {
	t.Helper()
	claims, err := c.ReadClaims()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	err = r.Run(testsupport.Ctx(t), claims, sql, nil, func(rows pgx.Rows) error {
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run %q: %v", sql, err)
	}
	return out
}

func TestParity_EveryOrderAndLineIsInTheViewIffTheOrderModulesRecordReadReturnsIt(t *testing.T) {
	t.Run("a world under one page", func(t *testing.T) { parityWorld(t, 0) })
	t.Run("a world above one page, as CI's is", func(t *testing.T) { parityWorld(t, 120) })
}

func parityWorld(t *testing.T, extra int) {
	env := testsupport.Open(t)
	plantSecondBranch(t, env)
	if extra > 0 {
		plantMany(t, env, extra)
	}
	world := seededWorld(t, env)
	branches := branchesWithOrders(t, env)
	if len(world) < 3 || len(branches) < 2 {
		t.Fatalf("the parity world is too small: %d orders at %d branches", len(world), len(branches))
	}
	pool, err := pgxpool.New(testsupport.Ctx(t), env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	oracle := order.NewService(order.NewRepository(pool))
	staff := env.Readers.Staff
	ctx := testsupport.Ctx(t)

	createdAt := createdAtOf(t, env)
	checked := 0
	for name, c := range callers(branches) {
		viewOrders := setOf(stringsFrom(t, staff, c, `SELECT id::text FROM ai.orders`))
		viewLines := setOf(stringsFrom(t, staff, c, `SELECT id::text FROM ai.order_lines`))
		lineCountByOrder := map[string]int{}
		for _, oid := range stringsFrom(t, staff, c, `SELECT order_id::text FROM ai.order_lines`) {
			lineCountByOrder[oid]++
		}
		scope := order.ScopeFor(c.Global, c.BranchIDs)
		for _, o := range world {
			sum, found, err := oracle.SummaryByNumber(ctx, scope, o.no)
			if err != nil {
				t.Fatalf("%s: record read of %s: %v", name, o.no, err)
			}
			if found && sum.OrderID != o.id {
				t.Errorf("%s: the record read resolved %s to another order", name, o.no)
			}
			if viewOrders[o.id] != found {
				t.Errorf("%s: order %s is in the view = %v but the record read returns it = %v", name, o.no, viewOrders[o.id], found)
			}
			for _, lid := range o.lines {
				if viewLines[lid] != found {
					t.Errorf("%s: a line of %s is in the view = %v but its order's record read returns it = %v", name, o.no, viewLines[lid], found)
				}
			}
			if found && lineCountByOrder[o.id] != sum.LineCount {
				t.Errorf("%s: %s has %d lines in the view, the record read counts %d", name, o.no, lineCountByOrder[o.id], sum.LineCount)
			}
			checked++
		}
		// And through data.query. A page is at most 100 rows however large the limit asked for, so this leg
		// asserts the clamp and then walks the caller's whole reach page by page.
		q := &assist.ReadQuerier{Reader: staff}
		checkPaged(t, q, c, name, viewOrders, createdAt)
	}
	if checked < 10 {
		t.Fatalf("only %d caller by order pairs were checked", checked)
	}
}

// createdAtOf maps every order id to its created_at at full precision, read as the admin; the pager uses
// it as its cursor, because the answer's own times are shown to the minute.
func createdAtOf(t *testing.T, env *testsupport.Env) map[string]time.Time {
	t.Helper()
	rows, err := env.Admin.Query(testsupport.Ctx(t), `SELECT id::text, created_at FROM orders`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	m := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			t.Fatal(err)
		}
		m[id] = at
	}
	return m
}

// checkPaged proves data.query's reach for one caller equals the view's rows. First the clamp: a spec asking
// for far more than 100 gets exactly 100 when more than 100 are in reach (and everything when fewer are).
// Then the pages: a stable order (created_at, then order_no) with a created_at window that moves to the last
// row seen, every row of every page in reach, no page over 100, and the union of the pages equal to the view.
func checkPaged(t *testing.T, q *assist.ReadQuerier, c assist.Caller, name string, view map[string]bool, createdAt map[string]time.Time) {
	t.Helper()
	ctx := testsupport.Ctx(t)
	big, err := q.Query(ctx, c, `{"view":"ai.orders","select":["order_no"],"limit":1000000}`)
	if err != nil {
		t.Fatalf("%s: data.query: %v", name, err)
	}
	want := len(view)
	if want > 100 {
		want = 100
	}
	if len(big.Items) != want {
		t.Errorf("%s: a limit of a million returned %d rows, want the clamp %d (the view holds %d)", name, len(big.Items), want, len(view))
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 20; page++ {
		where := ""
		if cursor != "" {
			where = fmt.Sprintf(`,"where":[{"col":"created_at","op":">=","value":%q}]`, cursor)
		}
		res, err := q.Query(ctx, c, `{"view":"ai.orders","select":["order_no"],"order_by":[{"col":"created_at","dir":"asc"},{"col":"order_no","dir":"asc"}],"limit":100`+where+`}`)
		if err != nil {
			t.Fatalf("%s: page %d: %v", name, page, err)
		}
		if len(res.Items) > 100 {
			t.Fatalf("%s: page %d holds %d rows", name, page, len(res.Items))
		}
		fresh := 0
		var last time.Time
		for _, it := range res.Items {
			if it.Ref == nil {
				t.Fatalf("%s: a row with no record ref", name)
			}
			if !view[it.Ref.ID] {
				t.Errorf("%s: page %d returned an order outside the caller's reach", name, page)
			}
			if !seen[it.Ref.ID] {
				seen[it.Ref.ID] = true
				fresh++
			}
			last = createdAt[it.Ref.ID]
		}
		if len(res.Items) < 100 {
			break
		}
		if fresh == 0 {
			t.Fatalf("%s: paging stalled at %s: a whole page of rows share one created_at", name, cursor)
		}
		cursor = last.UTC().Format(time.RFC3339Nano)
	}
	if len(seen) != len(view) {
		t.Errorf("%s: the pages hold %d orders, the view holds %d", name, len(seen), len(view))
	}
	for id := range view {
		if !seen[id] {
			t.Errorf("%s: an order in the view was never returned by data.query", name)
			break
		}
	}
}

// A frontline caller is refused by the tier floor before anything reaches the database.
func TestParity_ATierThreeCallerIsRefusedByTheTierFloor(t *testing.T) {
	env := testsupport.Open(t)
	q := &assist.ReadQuerier{Reader: env.Readers.Staff}
	reg := assist.NewRegistry(assist.Deps{Query: q}, nil)
	c3 := assist.Caller{UserID: "u-t3", RoleCode: "INSIDE_SALES", Tier: 3, BranchIDs: []string{"22222222-2222-2222-2222-222222222222"}}
	_, err := reg.Run(testsupport.Ctx(t), c3, assist.DataQueryCode, assist.Args{"spec": `{"view":"ai.orders"}`})
	if !errors.Is(err, assist.ErrRefused) {
		t.Fatalf("tier 3 ran data.query: %v", err)
	}
	for _, cmd := range reg.List(testsupport.Ctx(t), c3) {
		if cmd.Code == assist.DataQueryCode {
			t.Error("tier 3 was offered data.query")
		}
	}
	c2 := assist.Caller{UserID: "u-t2", RoleCode: "SALES_MANAGER", Tier: 2, Global: true}
	if _, err := reg.Run(testsupport.Ctx(t), c2, assist.DataQueryCode, assist.Args{"spec": `{"view":"ai.orders","limit":1}`}); err != nil {
		t.Errorf("tier 2 refused: %v", err)
	}
}
