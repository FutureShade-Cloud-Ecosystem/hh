//go:build integration

package parity

// Per record and per column parity for the account position on ai.customers (credit limit and open balance), against
// the receivables register's own read (billing.RegisterService.Register, whose rows carry the account position only
// where the branch's ar_invoice_policy.account_position_visibility lets the caller see it), with the policy set per
// branch in the test to each of its three values, and the dealer default varied through all three as well.
//
// The register gates a page by the policy of the branch it was asked for (the dealer default when none), and the
// invoice detail by the invoice's branch. A customer row has one branch, its home branch, so the view follows the
// policy of the home branch, and the dealer default for a customer homed to none; the oracle asks the register for
// exactly that page (the home branch as the filter, none for an account homed to none).

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/adapters"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

type positionWorld struct {
	tag       string
	branches  map[string]string // x ALL_STAFF, y HIDDEN, z MANAGER_UP, w no row of its own
	customers map[string]string // one per branch plus "none" (homed to no branch)
	limits    map[string]int64
}

func plantPositionWorld(t *testing.T, env *testsupport.Env) *positionWorld {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	w := &positionWorld{tag: tag, branches: map[string]string{}, customers: map[string]string{}, limits: map[string]int64{}}
	for _, b := range []string{"x", "y", "z", "w"} {
		w.branches[b] = plantBranch(t, env, tag+"P"+b, "S7c position "+b)
	}
	for b, word := range map[string]string{"x": "ALL_STAFF", "y": "HIDDEN", "z": "MANAGER_UP"} {
		if _, err := env.Admin.Exec(ctx, `INSERT INTO ar_invoice_policy (branch_id, account_position_visibility) VALUES ($1::uuid, $2)`, w.branches[b], word); err != nil {
			t.Fatalf("plant policy: %v", err)
		}
	}
	limit := int64(100000)
	for _, home := range []string{"x", "y", "z", "w", "none"} {
		var id string
		homeID := ""
		if home != "none" {
			homeID = w.branches[home]
		}
		limit += 25000
		if err := env.Admin.QueryRow(ctx, `INSERT INTO customers (account_no, name, home_branch_id, credit_limit_cents) VALUES ($1, $2, NULLIF($3,'')::uuid, $4) RETURNING id::text`,
			"S7C-"+tag+"-"+home, "Position Customer "+home+" "+tag, homeID, limit).Scan(&id); err != nil {
			t.Fatalf("plant customer: %v", err)
		}
		w.customers[home], w.limits[home] = id, limit
	}
	inv := func(name, cust, branch, status string, balance int64, dueOffset int) {
		if _, err := env.Admin.Exec(ctx, `INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, total_cents, balance_cents)
			VALUES ($1, $2::uuid, $3::uuid, $4, CURRENT_DATE - 60, CURRENT_DATE + $5::int, $6, $6, $6)`, fmt.Sprintf("INV-S7C-%s-%s", tag, name), w.customers[cust], branch, status, dueOffset, balance); err != nil {
			t.Fatalf("plant invoice %s: %v", name, err)
		}
	}
	// Each homed account has an open invoice at home, one at another branch (so the account spans branches), a paid
	// one and a draft (which the balance leaves out). The account homed to none has an open invoice at every branch.
	other := map[string]string{"x": "y", "y": "z", "z": "w", "w": "x"}
	for i, home := range []string{"x", "y", "z", "w"} {
		inv(home+"-open", home, w.branches[home], "FINALIZED", int64(1000*(i+1)), -5)
		inv(home+"-part", home, w.branches[other[home]], "PARTIALLY_PAID", int64(300*(i+1)), 10)
		inv(home+"-paid", home, w.branches[home], "PAID", 0, -20)
		inv(home+"-draft", home, w.branches[home], "DRAFT", 700, 30)
	}
	for i, b := range []string{"x", "y", "z", "w"} {
		inv("none-"+b, "none", w.branches[b], "FINALIZED", int64(111*(i+1)), -1)
	}
	var prior string
	if err := env.Admin.QueryRow(ctx, `SELECT account_position_visibility FROM ar_invoice_policy WHERE branch_id IS NULL`).Scan(&prior); err != nil {
		t.Fatal(err)
	}
	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `UPDATE ar_invoice_policy SET account_position_visibility = $1 WHERE branch_id IS NULL`, prior)
		_, _ = c.Exec(context.Background(), `DELETE FROM ar_invoice_policy WHERE branch_id = ANY($1::uuid[])`, []string{w.branches["x"], w.branches["y"], w.branches["z"]})
		_, _ = c.Exec(context.Background(), `DELETE FROM invoices WHERE invoice_no LIKE $1`, "INV-S7C-"+tag+"-%")
		_, _ = c.Exec(context.Background(), `DELETE FROM customers WHERE account_no LIKE $1`, "S7C-"+tag+"-%")
	})
	return w
}

func (w *positionWorld) all() []string {
	return []string{w.branches["x"], w.branches["y"], w.branches["z"], w.branches["w"]}
}

func (w *positionWorld) callers() []staffCallerCase {
	gm := staffCallerCase{"global manager", identity.StaffContext{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}}
	front := scoped("YARD", w.all()...)
	front.Tier = identity.TierFrontline
	return []staffCallerCase{
		gm,
		{"manager at all four", scoped("BRANCH_MANAGER", w.all()...)},
		{"manager at x only", scoped("BRANCH_MANAGER", w.branches["x"])},
		{"manager at y only", scoped("BRANCH_MANAGER", w.branches["y"])},
		{"manager at z and w", scoped("BRANCH_MANAGER", w.branches["z"], w.branches["w"])},
		{"manager with no grant", scoped("BRANCH_MANAGER")},
		{"frontline at all four", front},
	}
}

func TestParity_EveryAccountPositionFollowsTheRegistersPolicyPerBranch(t *testing.T) {
	env := testsupport.Open(t)
	w := plantPositionWorld(t, env)
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
	bill := billing.NewRepository(pool)
	reg := billing.NewRegisterService(bill)
	h := assist.NewHandler(nil, nil).WithCostGate(rc, everyBranch(t, env)).WithAccountPositionPolicy(adapters.NewAccountPositionPolicy(bill))
	shown, hiddenSeen, emptyPages := 0, 0, 0
	for _, word := range []string{"MANAGER_UP", "ALL_STAFF", "HIDDEN"} {
		if _, err := env.Admin.Exec(ctx, `UPDATE ar_invoice_policy SET account_position_visibility = $1 WHERE branch_id IS NULL`, word); err != nil {
			t.Fatal(err)
		}
		for _, c := range w.callers() {
			floor := c.sc.Tier == identity.TierLeadership || c.sc.Tier == identity.TierManager
			_, claims := claimsOf(t, h, c.sc)
			type cells struct {
				limit, open *int64
			}
			got := map[string]cells{}
			readRows(t, env, claims, `SELECT id::text, credit_limit_cents::text, open_balance_cents::text FROM ai.customers WHERE account_no LIKE 'S7C-`+w.tag+`-%'`, func(rs pgx.Rows) error {
				var id string
				var l, o *string
				if err := rs.Scan(&id, &l, &o); err != nil {
					return err
				}
				got[id] = cells{i64p(l), i64p(o)}
				return nil
			})
			octx := identity.ContextWithStaff(ctx, c.sc)
			for home, id := range w.customers {
				filter := billing.RegisterFilter{CustomerID: id, ShowCredits: true, All: true}
				if home != "none" {
					filter.BranchID = w.branches[home]
				}
				page, perr := reg.Register(octx, filter)
				reachable := perr == nil
				x, in := got[id]
				// The customer row itself: home in reach, or homed to none, for a tier 1 or 2 caller.
				wantRow := floor && (home == "none" || reachable || c.sc.Scope == identity.ScopeGlobal)
				if in != wantRow {
					t.Errorf("[default %s] %s: customer %s is in the view = %v, want %v", word, c.name, home, in, wantRow)
					continue
				}
				if !in {
					continue
				}
				var acct *billing.AccountPosition
				if perr == nil {
					for _, row := range page.Items {
						if row.Account != nil {
							acct = row.Account
							break
						}
					}
					if len(page.Items) == 0 {
						// The register has no row to carry the position: an account homed to none, and a caller with no
						// invoice of it in reach. The register cannot say; the view follows the dealer default's word
						// (the policy read) and is checked against it here.
						pol, perr := reg.Policy(octx, "")
						if perr != nil {
							t.Fatal(perr)
						}
						allowedByDefault := pol.AccountPositionVisibility == billing.VisibilityAllStaff ||
							(pol.AccountPositionVisibility == billing.VisibilityManagerUp && floor)
						if (x.limit != nil) != allowedByDefault || (x.open != nil) != allowedByDefault {
							t.Errorf("[default %s] %s: %s shows %v / %v with the dealer default %s", word, c.name, home, x.limit, x.open, pol.AccountPositionVisibility)
						}
						emptyPages++
						continue
					}
				}
				if acct == nil {
					if x.limit != nil || x.open != nil {
						t.Errorf("[default %s] %s: %s shows credit limit %v and balance %v, the register shows no account position", word, c.name, home, x.limit, x.open)
					}
					hiddenSeen++
					continue
				}
				shown++
				if x.limit == nil || *x.limit != acct.CreditLimitCents || x.open == nil || *x.open != acct.OpenBalanceCents {
					t.Errorf("[default %s] %s: %s shows %v / %v, the register's account position is %d / %d", word, c.name, home, x.limit, x.open, acct.CreditLimitCents, acct.OpenBalanceCents)
				}
			}
		}
	}
	if shown < 20 || hiddenSeen < 10 || emptyPages != 3 {
		t.Fatalf("too little was compared: %d shown, %d hidden, %d accounts the register could not say", shown, hiddenSeen, emptyPages)
	}
}

// data.query: a caller who holds the account position nowhere is refused the two cells by every route (named, summed,
// filtered, ordered, grouped, counted), with the class word and no rows; a caller who holds it at some branches sees
// the cells on those branches' accounts only, absent (not blank) on the others, and an aggregate reaches only those.
func TestQuery_AccountPositionCellsAreRefusedWithoutTheGateAndAbsentPerRowWithIt(t *testing.T) {
	env := testsupport.Open(t)
	w := plantPositionWorld(t, env)
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
	h := assist.NewHandler(nil, nil).WithCostGate(rc, everyBranch(t, env)).WithAccountPositionPolicy(adapters.NewAccountPositionPolicy(billing.NewRepository(pool)))
	q := &assist.ReadQuerier{Reader: env.Readers.Staff, Now: func() time.Time { return time.Now() }}
	// The dealer default is HIDDEN for this test, so only x (ALL_STAFF) and z (MANAGER_UP) allow a manager.
	if _, err := env.Admin.Exec(ctx, `UPDATE ar_invoice_policy SET account_position_visibility = 'HIDDEN' WHERE branch_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	none := scoped("BRANCH_MANAGER", w.branches["y"], w.branches["w"]) // y is HIDDEN, w follows the HIDDEN default
	some := scoped("BRANCH_MANAGER", w.all()...)
	callerNone, _ := claimsOf(t, h, none)
	callerSome, _ := claimsOf(t, h, some)
	if len(callerNone.AccountPositionBranchIDs) != 0 || len(callerSome.AccountPositionBranchIDs) != 2 {
		t.Fatalf("the callers are not what the test needs: none=%v some=%v", callerNone.AccountPositionBranchIDs, callerSome.AccountPositionBranchIDs)
	}
	for _, v := range assist.CatalogueFor(callerNone) {
		if v.Name == "ai.customers" {
			for _, col := range []string{"credit_limit_cents", "open_balance_cents"} {
				if _, ok := v.Column(col); ok {
					t.Fatalf("the catalogue of a caller with no account position carries %s", col)
				}
			}
		}
	}
	for name, spec := range map[string]string{
		"the limit named":       `{"view":"ai.customers","select":["name","credit_limit_cents"]}`,
		"the balance named":     `{"view":"ai.customers","select":["name","open_balance_cents"]}`,
		"a sum of the limit":    `{"view":"ai.customers","aggregate":[{"fn":"sum","col":"credit_limit_cents"}]}`,
		"a maximum by branch":   `{"view":"ai.customers","group_by":["branch_label"],"aggregate":[{"fn":"max","col":"open_balance_cents"}]}`,
		"a filter on the limit": `{"view":"ai.customers","select":["name"],"where":[{"col":"credit_limit_cents","op":">","value":0}]}`,
		"an order on balance":   `{"view":"ai.customers","select":["name"],"order_by":[{"col":"open_balance_cents","dir":"desc"}]}`,
		"a null test":           `{"view":"ai.customers","select":["name"],"where":[{"col":"open_balance_cents","op":"is_null","value":false}]}`,
	} {
		res, err := q.Query(ctx, callerNone, spec)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if res.Notice == nil || res.Notice.Code != assist.NoticeRefused || res.Notice.Class == "" || !res.Empty || len(res.Items) != 0 {
			t.Errorf("%s: want a refusal with a class and no rows, got %+v", name, res)
		}
	}
	// With the gate at x and z: the cells are present on the accounts homed there and absent on the others.
	res, err := q.Query(ctx, callerSome, `{"view":"ai.customers","select":["account_no","credit_limit_cents","open_balance_cents"],"where":[{"col":"account_no","op":"contains","value":"S7C-`+w.tag+`"}],"limit":50}`)
	if err != nil || res.Notice != nil || len(res.Items) != 5 {
		t.Fatalf("a caller who holds the position at x and z: %v, %d rows, %+v", err, len(res.Items), res.Notice)
	}
	with := map[string]bool{}
	for _, it := range res.Items {
		acct, _ := fieldValue(it, "Account")
		_, hasLimit := fieldValue(it, "Credit limit")
		_, hasBal := fieldValue(it, "Open balance")
		if hasLimit != hasBal {
			t.Errorf("%s: the limit and the balance differ in presence", acct)
		}
		with[strings.TrimPrefix(acct, "S7C-"+w.tag+"-")] = hasLimit
	}
	want := map[string]bool{"x": true, "y": false, "z": true, "w": false, "none": false}
	for k, v := range want {
		if with[k] != v {
			t.Errorf("customer %s: cells present = %v, want %v (%v)", k, with[k], v, with)
		}
	}
	agg, err := q.Query(ctx, callerSome, `{"view":"ai.customers","aggregate":[{"fn":"sum","col":"credit_limit_cents"}],"where":[{"col":"account_no","op":"contains","value":"S7C-`+w.tag+`"}]}`)
	if err != nil || agg.Notice != nil || len(agg.Items) != 1 {
		t.Fatalf("a sum of the limit: %v %+v", err, agg)
	}
	if v, _ := fieldValue(agg.Items[0], "Total credit limit"); v != money(w.limits["x"]+w.limits["z"]) {
		t.Errorf("the sum reaches %q, want only x and z: %s", v, money(w.limits["x"]+w.limits["z"]))
	}
}

func money(cents int64) string {
	return fmt.Sprintf("$%s.%02d", commaInt(cents/100), cents%100)
}

func commaInt(n int64) string {
	s := fmt.Sprint(n)
	var out []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// The position owner's one policy on invoices, pinned directly: a staff claim at tier 1 or 2 sees every branch's
// invoices (the whole account), whatever its branches; a pro claim, no claim, tier 0 or 3 sees none. The view
// owner's own policy on invoices, which narrows to the asker's branches, is untouched.
func TestPolicy_ThePositionOwnerSeesEveryBranchForAStaffTierOneOrTwoClaimAndNothingElse(t *testing.T) {
	env := testsupport.Open(t)
	w := plantPositionWorld(t, env)
	total := countWhere(t, env, `SELECT count(*) FROM invoices`)
	narrow := countWhere(t, env, `SELECT count(*) FROM invoices WHERE branch_id = $1::uuid`, w.branches["x"])
	if narrow == 0 || narrow == total {
		t.Fatalf("the world is wrong: %d of %d invoices at x", narrow, total)
	}
	owner := env.Roles.PositionOwner
	if got := countAsRoleWith(t, env, owner, staffClaim([]string{w.branches["x"]}, false), "public.invoices"); got != total {
		t.Errorf("the position owner under a claim for branch x reads %d invoices, want all %d", got, total)
	}
	if got := countAsRoleWith(t, env, owner, staffClaim(nil, false), "public.invoices"); got != total {
		t.Errorf("the position owner under a claim with no branch reads %d invoices, want all %d (the account is whole)", got, total)
	}
	pro := staffClaim(w.all(), true)
	pro.Audience = "pro"
	pro.CustomerID = "33333333-3333-3333-3333-333333333333"
	if got := countAsRoleWith(t, env, owner, pro, "public.invoices"); got != 0 {
		t.Errorf("the position owner under a pro claim reads %d invoices, want 0", got)
	}
	for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
		c := staffClaim(w.all(), true)
		c.Tier = tier
		if got := countAsRoleWith(t, env, owner, c, "public.invoices"); (got > 0) != want {
			t.Errorf("the position owner under a tier %d claim reads %d invoices, readable want %v", tier, got, want)
		}
	}
	// The view owner still reads only the claim's branches.
	if got := countAsViewOwner(t, env, staffClaim([]string{w.branches["x"]}, false), "public.invoices"); got != narrow {
		t.Errorf("the view owner under a claim for branch x reads %d invoices, want %d", got, narrow)
	}
}
