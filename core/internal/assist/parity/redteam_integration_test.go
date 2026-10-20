//go:build integration

package parity

// Red team cases 2, 8, 11 and 16 of plan r3 section 7, on the seeded world and a scripted provider that
// emits each attack spec. The raw database cases (6 and 16's raw half) live in readtx beside the roles.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

// attackProvider emits one scripted reply per call.
type attackProvider struct {
	replies []assist.Reply
	n       int
}

func (p *attackProvider) Name() string  { return "attack" }
func (p *attackProvider) Model() string { return "attack-1" }
func (p *attackProvider) Complete(_ context.Context, _ assist.CompletionRequest) (assist.Reply, error) {
	if p.n < len(p.replies) {
		r := p.replies[p.n]
		p.n++
		return r, nil
	}
	return assist.Reply{Text: "done"}, nil
}

func spec(s string) assist.Reply {
	return assist.Reply{ToolCalls: []assist.ToolCall{{Command: assist.DataQueryCode, Args: assist.Args{"spec": s}}}}
}

type rtRig struct {
	env      *testsupport.Env
	q        *assist.ReadQuerier
	branches []string
	labels   map[string]string
	log      *memLog
}

type memLog struct{ rows []assist.QueryLogEntry }

func (m *memLog) AppendQueryLog(_ context.Context, e assist.QueryLogEntry) error {
	m.rows = append(m.rows, e)
	return nil
}

func newRig(t *testing.T) *rtRig {
	t.Helper()
	env := testsupport.Open(t)
	plantSecondBranch(t, env)
	r := &rtRig{env: env, log: &memLog{}, labels: map[string]string{}}
	r.branches = branchesWithOrders(t, env)
	rows, err := env.Admin.Query(testsupport.Ctx(t), `SELECT id::text, name FROM locations`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		r.labels[id] = name
	}
	rows.Close()
	r.q = &assist.ReadQuerier{Reader: env.Readers.Staff, Now: time.Now, Log: r.log}
	return r
}

func (r *rtRig) asker(branch string) assist.Caller {
	return assist.Caller{UserID: "u-clerk", RoleCode: "SALES_MANAGER", Tier: 2, BranchIDs: []string{branch}}
}

// Red team 2: a branch scoped manager asks for another branch's sales by filter, by aggregate and by
// grouping. The rows are simply not in their data: the answers are the same as for a branch that does not
// exist, so nothing says rows were hidden.
func TestRedTeam2_ABranchScopedAskerCannotReachAnotherBranchBySpec(t *testing.T) {
	r := newRig(t)
	mine, theirs := r.branches[0], r.branches[1]
	c := r.asker(mine)
	ctx := testsupport.Ctx(t)

	byLabel := func(label string) assist.Result {
		res, err := r.q.Query(ctx, c, fmt.Sprintf(`{"view":"ai.orders","select":["order_no","total_cents"],"where":[{"col":"branch_label","op":"=","value":%q}]}`, label))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	other, nowhere := byLabel(r.labels[theirs]), byLabel("A branch that does not exist")
	if len(other.Items) != 0 || !other.Empty {
		t.Fatalf("the other branch's orders were returned: %d", len(other.Items))
	}
	if other.Headline != nowhere.Headline || other.Note != nowhere.Note || (other.Notice == nil) != (nowhere.Notice == nil) {
		t.Errorf("a filter on another branch answers differently from a branch that does not exist: %+v vs %+v", other, nowhere)
	}
	// By id as well as by label.
	byID, err := r.q.Query(ctx, c, fmt.Sprintf(`{"view":"ai.orders","where":[{"col":"branch_id","op":"=","value":%q}]}`, theirs))
	if err != nil || len(byID.Items) != 0 {
		t.Errorf("by branch id: %v, %d rows", err, len(byID.Items))
	}
	// By aggregate and by grouping: one group, and it is theirs.
	agg, err := r.q.Query(ctx, c, `{"view":"ai.orders","group_by":["branch_label"],"aggregate":[{"fn":"sum","col":"total_cents"},{"fn":"count"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(agg.Items) != 1 || agg.Items[0].Fields[0].Value != r.labels[mine] {
		t.Errorf("the aggregate crosses branches: %+v", agg.Items)
	}
	sum, _ := r.q.Query(ctx, c, `{"view":"ai.orders","aggregate":[{"fn":"count"}]}`)
	var mineCount int
	if err := r.env.Admin.QueryRow(ctx, `SELECT count(*) FROM orders WHERE branch_id = $1::uuid`, mine).Scan(&mineCount); err != nil {
		t.Fatal(err)
	}
	if len(sum.Items) != 1 || sum.Items[0].Fields[0].Value != fmt.Sprint(mineCount) {
		t.Errorf("a plain count covers more than the asker's branch: %+v (want %d)", sum.Items, mineCount)
	}
	// The same attacks through the service with a scripted provider: the model's spec runs as the asker.
	reg := assist.NewRegistry(assist.Deps{Query: r.q}, nil)
	svc := assist.NewService(reg, &attackProvider{replies: []assist.Reply{
		spec(fmt.Sprintf(`{"view":"ai.orders","where":[{"col":"branch_id","op":"=","value":%q}]}`, theirs)),
		{Text: "No orders matched."},
	}}, noLedger{}, noLimits{}, assist.StaticSettings{}, nil)
	ans, err := svc.Ask(ctx, c, "show me the other branch's orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range ans.Results {
		if len(res.Items) != 0 {
			t.Errorf("the service returned other branch rows: %+v", res)
		}
	}
	// An identity or scope field in a spec is refused by the grammar, not honoured.
	for _, s := range []string{
		fmt.Sprintf(`{"view":"ai.orders","branch_ids":[%q]}`, theirs),
		`{"view":"ai.orders","claims":{"global":true}}`,
		`{"view":"ai.orders","global":true}`,
	} {
		if _, err := r.q.Query(ctx, c, s); err == nil {
			t.Errorf("a spec with a scope field was accepted: %s", s)
		}
	}
}

// Red team 8: a broad scan, a huge limit and a slow plan are refused or bounded, with one fixed sentence
// and no estimate anywhere in the answer.
func TestRedTeam8_BoundsRefuseWithOneFixedSentence(t *testing.T) {
	r := newRig(t)
	c := assist.Caller{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: 1, Global: true}
	ctx := testsupport.Ctx(t)

	// A huge limit is clamped, not refused: never more than the maximum rows.
	res, err := r.q.Query(ctx, c, `{"view":"ai.order_lines","limit":2000000000}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) > 100 {
		t.Errorf("a huge limit returned %d rows", len(res.Items))
	}
	// A plan above the ceiling: refused by shape before the query runs. The thresholds are lowered to
	// the planner's cost for the seeded tables so the real EXPLAIN of the real tables trips them.
	tight := &assist.ReadQuerier{Reader: r.env.Readers.Staff, Now: time.Now, Log: r.log, CostCeiling: 0.5, SeqScanCostLimit: 0.5}
	for _, s := range []string{`{"view":"ai.orders"}`, `{"view":"ai.order_lines","aggregate":[{"fn":"sum","col":"line_total_cents"}]}`} {
		got, err := tight.Query(ctx, c, s)
		if err != nil {
			t.Fatal(err)
		}
		if got.Headline != assist.TooBroadMessage || got.Notice == nil || got.Notice.Code != assist.NoticeTooBroad || len(got.Items) != 0 {
			t.Errorf("%s: %+v", s, got)
		}
		if strings.ContainsAny(got.Headline+got.Note, "0123456789") {
			t.Errorf("an estimate or number reached the answer: %q %q", got.Headline, got.Note)
		}
	}
	last := r.log.rows[len(r.log.rows)-1]
	if last.Refusal != "too_broad" {
		t.Errorf("log refusal = %q", last.Refusal)
	}
	// A slow plan: a statement timeout inside the read transaction is a code, then the same fixed sentence.
	slow := &assist.ReadQuerier{Reader: slowDoer{r.env.Readers.Staff}, Now: time.Now, Log: r.log}
	got, err := slow.Query(ctx, c, `{"view":"ai.orders"}`)
	if err != nil || got.Headline != assist.TooBroadMessage || got.Notice == nil || got.Notice.Code != assist.NoticeTooBroad {
		t.Errorf("a timeout: %+v %v", got, err)
	}
}

// slowDoer runs the real transaction, then makes a statement time out for real.
type slowDoer struct{ r *readtx.Reader }

func (s slowDoer) Do(ctx context.Context, c readtx.Claims, fn func(pgx.Tx) error) error {
	return s.r.Do(ctx, c, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = 50`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_sleep(2)`); err != nil {
			return readtx.MapError(err)
		}
		return fn(tx)
	})
}

// Red team 11: a query that raises a Postgres error returns a code. A real error whose text carries an
// input value and function source never reaches the answer, the error or the log.
func TestRedTeam11_APostgresErrorReturnsACodeAndNoText(t *testing.T) {
	r := newRig(t)
	c := assist.Caller{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: 1, Global: true}
	failing := &assist.ReadQuerier{Reader: realErrorDoer{r.env.Readers.Staff}, Now: time.Now, Log: r.log}
	res, err := failing.Query(testsupport.Ctx(t), c, `{"view":"ai.orders","where":[{"col":"status","op":"=","value":"CONFIRMED"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	dump := fmt.Sprintf("%+v | %+v", res, r.log.rows)
	for _, leak := range []string{"SECRET-INPUT-VALUE", "invalid input syntax", "integer", "PL/pgSQL", "ai.claims", "SQLSTATE", "pg_", "context"} {
		if strings.Contains(dump, leak) {
			t.Errorf("the answer or log carries %q: %s", leak, dump)
		}
	}
	if res.Headline != assist.QueryUnavailable {
		t.Errorf("headline = %q", res.Headline)
	}
	// And the real thing through the real read path: a made up view is refused with our sentence only.
	_, err = r.q.Query(testsupport.Ctx(t), c, `{"view":"public.orders"}`)
	if err == nil || strings.Contains(err.Error(), "public") {
		t.Errorf("err = %v", err)
	}
}

// realErrorDoer provokes a genuine Postgres error that quotes the input value, inside the real
// transaction as the reader.
type realErrorDoer struct{ r *readtx.Reader }

func (d realErrorDoer) Do(ctx context.Context, c readtx.Claims, fn func(pgx.Tx) error) error {
	return d.r.Do(ctx, c, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT 'SECRET-INPUT-VALUE'::integer`)
		return err
	})
}

// Red team 16 with the new views: everything in ai is owned by its role and the views belong to the view
// owner, so row security applies to them (a superuser owned view would show every branch to every asker).
func TestRedTeam16_TheNewViewsAreOwnedByTheViewOwnerNotTheSuperuser(t *testing.T) {
	env := testsupport.Open(t)
	rows, err := env.Admin.Query(testsupport.Ctx(t), `SELECT c.relname, r.rolname, r.rolsuper, r.rolbypassrls
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_roles r ON r.oid = c.relowner
		WHERE n.nspname = 'ai' AND c.relkind = 'v'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var rel, owner string
		var super, bypass bool
		if err := rows.Scan(&rel, &owner, &super, &bypass); err != nil {
			t.Fatal(err)
		}
		n++
		if owner != env.Roles.ViewOwner || super || bypass {
			t.Errorf("ai.%s is owned by %s (super=%v bypassrls=%v), want %s", rel, owner, super, bypass, env.Roles.ViewOwner)
		}
	}
	if n < 2 {
		t.Errorf("found %d views in ai", n)
	}
}

type noLedger struct{}

func (noLedger) Append(context.Context, assist.Entry) error { return nil }
func (noLedger) SpendFor(context.Context, string, time.Time, time.Time) (assist.Spend, error) {
	return assist.Spend{}, nil
}
func (noLedger) SpendAll(context.Context, time.Time, time.Time) (map[string]assist.Spend, error) {
	return nil, nil
}

type noLimits struct{}

func (noLimits) RoleBudget(context.Context, string) (*int, error)         { return nil, nil }
func (noLimits) UserBudget(context.Context, string) (*int, error)         { return nil, nil }
func (noLimits) RoleBudgets(context.Context) (map[string]int, error)      { return nil, nil }
func (noLimits) UserBudgets(context.Context) (map[string]int, error)      { return nil, nil }
func (noLimits) SetRoleBudget(context.Context, string, int, string) error { return nil }
func (noLimits) SetUserBudget(context.Context, string, int, string) error { return nil }
func (noLimits) ClearUserBudget(context.Context, string) error            { return nil }
