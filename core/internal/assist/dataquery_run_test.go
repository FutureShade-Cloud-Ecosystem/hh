package assist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
)

// -- a scripted database: EXPLAIN answers a plan, the query answers rows ------------------------------

type fakeRows struct {
	pgx.Rows
	names []string
	data  [][]any
	i     int
	err   error
}

func (r *fakeRows) Next() bool {
	r.i++
	return r.i <= len(r.data)
}
func (r *fakeRows) Values() ([]any, error) { return r.data[r.i-1], nil }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	out := make([]pgconn.FieldDescription, len(r.names))
	for i, n := range r.names {
		out[i] = pgconn.FieldDescription{Name: n}
	}
	return out
}
func (r *fakeRows) Scan(dest ...any) error {
	if p, ok := dest[0].(*[]byte); ok {
		*p = r.data[r.i-1][0].([]byte)
	}
	return nil
}
func (r *fakeRows) Err() error { return r.err }
func (r *fakeRows) Close()     {}

type scriptTx struct {
	pgx.Tx
	plan    string
	names   []string
	data    [][]any
	failOn  string
	failErr error
	ran     []string
}

func (s *scriptTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	s.ran = append(s.ran, sql)
	if s.failOn != "" && strings.Contains(sql, s.failOn) {
		return nil, s.failErr
	}
	if strings.HasPrefix(sql, "EXPLAIN") {
		return &fakeRows{names: []string{"QUERY PLAN"}, data: [][]any{{[]byte(s.plan)}}}, nil
	}
	return &fakeRows{names: s.names, data: s.data}, nil
}

type scriptDoer struct {
	tx     *scriptTx
	claims readtx.Claims
	err    error
}

func (d *scriptDoer) Do(_ context.Context, c readtx.Claims, fn func(pgx.Tx) error) error {
	d.claims = c
	if d.err != nil {
		return d.err
	}
	return fn(d.tx)
}

type captureLog struct{ rows []QueryLogEntry }

func (c *captureLog) AppendQueryLog(_ context.Context, e QueryLogEntry) error {
	c.rows = append(c.rows, e)
	return nil
}

const cheapPlan = `[{"Plan":{"Node Type":"Seq Scan","Relation Name":"orders","Total Cost":12.5,"Plan Rows":5}}]`

func newQuerier(tx *scriptTx) (*ReadQuerier, *scriptDoer, *captureLog) {
	d := &scriptDoer{tx: tx}
	l := &captureLog{}
	return &ReadQuerier{Reader: d, Now: func() time.Time { return dqClock }, Log: l}, d, l
}

var orderCols = []string{"order_no", "total_cents", "__id", "__label"}

func TestQueryBuildsCitableRowsAndLogsTheOutline(t *testing.T) {
	tx := &scriptTx{plan: cheapPlan, names: orderCols, data: [][]any{
		{"SO-1", "123456", "11111111-1111-1111-1111-111111111111", "SO-1"},
		{"SO-2", "50", "22222222-2222-2222-2222-222222222222", "SO-2"},
	}}
	q, d, l := newQuerier(tx)
	res, err := q.Query(withExchange(context.Background()), dqManager, `{"view":"ai.orders","select":["order_no","total_cents"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || res.Items[0].Fields[1].Value != "$1,234.56" || res.Items[1].Fields[1].Value != "$0.50" {
		t.Errorf("items = %+v", res.Items)
	}
	if res.Items[0].Ref == nil || res.Items[0].Ref.Kind != RefOrder || res.Items[0].Ref.ID != "11111111-1111-1111-1111-111111111111" || res.Items[0].Ref.Label != "Order SO-1" {
		t.Errorf("ref = %+v", res.Items[0].Ref)
	}
	if res.Scope == nil || len(res.Scope.Branches) != 1 || res.Scope.AsOf == "" {
		t.Errorf("scope = %+v", res.Scope)
	}
	if d.claims.Audience != readtx.AudienceStaff || d.claims.AskerID != "u1" || len(d.claims.BranchIDs) != 1 {
		t.Errorf("claims = %+v", d.claims)
	}
	if len(tx.ran) != 2 || !strings.HasPrefix(tx.ran[0], "EXPLAIN (FORMAT JSON) SELECT") {
		t.Errorf("statements = %v", tx.ran)
	}
	if len(l.rows) != 1 || l.rows[0].RowCount != 2 || len(l.rows[0].CitedIDs) != 2 || l.rows[0].Refusal != "" || l.rows[0].View != "ai.orders" {
		t.Errorf("log = %+v", l.rows)
	}
	if l.rows[0].ExchangeID == "" {
		t.Error("the log row carries no exchange id")
	}
}

func TestQueryRefusesATooBroadPlanWithOneFixedSentenceAndNoEstimate(t *testing.T) {
	big := `[{"Plan":{"Node Type":"Seq Scan","Relation Name":"orders","Total Cost":987654.32,"Plan Rows":4242424}}]`
	tx := &scriptTx{plan: big}
	q, _, l := newQuerier(tx)
	res, err := q.Query(context.Background(), dqManager, `{"view":"ai.orders"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Headline != TooBroadMessage || res.Notice == nil || res.Notice.Code != NoticeTooBroad || !res.Empty {
		t.Errorf("res = %+v", res)
	}
	for _, s := range []string{res.Headline, res.Note, fmt.Sprint(res.Items), fmt.Sprint(res.Notice)} {
		if strings.ContainsAny(s, "0123456789") {
			t.Errorf("a number reached the answer: %q", s)
		}
	}
	if len(tx.ran) != 1 {
		t.Errorf("the query ran after a refused plan: %v", tx.ran)
	}
	if len(l.rows) != 1 || l.rows[0].Refusal != "too_broad" {
		t.Errorf("log = %+v", l.rows)
	}
}

func TestShapeCheck(t *testing.T) {
	seq := func(rel string, cost float64) string {
		return fmt.Sprintf(`[{"Plan":{"Node Type":"Aggregate","Total Cost":%v,"Plans":[{"Node Type":"Seq Scan","Relation Name":"%s","Total Cost":%v}]}}]`, cost+1, rel, cost)
	}
	for name, c := range map[string]struct {
		plan string
		want bool
	}{
		"a cheap scan":                 {seq("orders", 50), false},
		"a costly scan of a big table": {seq("orders", DefaultSeqScanCostLimit+1), true},
		"a costly scan of a small one": {seq("branches", DefaultSeqScanCostLimit+1), false},
		"a plan over the ceiling":      {`[{"Plan":{"Node Type":"Nested Loop","Total Cost":99999999}}]`, true},
		"an index scan":                {`[{"Plan":{"Node Type":"Index Scan","Relation Name":"orders","Total Cost":20000}}]`, false},
		"garbage":                      {`not json`, true},
		"empty":                        {`[]`, true},
	} {
		if got := CheckShape([]byte(c.plan), DefaultCostCeiling, DefaultSeqScanCostLimit); got.TooBroad != c.want {
			t.Errorf("%s: TooBroad = %v, want %v (%s)", name, got.TooBroad, c.want, got.Reason)
		}
	}
}

// Red team 11: every database failure reaches the answer as a code and a fixed sentence. The text of a
// Postgres error, which can carry an input value or function source, is never in the result, the error or
// the log row.
func TestQueryMapsEveryDatabaseErrorToACodeAndNeverEchoesItsText(t *testing.T) {
	leak := `invalid input syntax for type uuid: "SECRET-VALUE" CONTEXT: PL/pgSQL function ai.claims() line 9 at RETURN`
	for name, c := range map[string]struct {
		err      error
		headline string
		notice   string
	}{
		"a driver error mid query": {&pgconn.PgError{Code: "22P02", Message: leak, Detail: leak, Where: leak}, QueryUnavailable, ""},
		"a statement timeout":      {&pgconn.PgError{Code: "57014", Message: leak}, TooBroadMessage, NoticeTooBroad},
		"a lock timeout":           {&pgconn.PgError{Code: "55P03", Message: leak}, TooBroadMessage, NoticeTooBroad},
		"a privilege error":        {&pgconn.PgError{Code: "42501", Message: leak}, QueryUnavailable, ""},
		"a plain error":            {errors.New(leak), QueryUnavailable, ""},
	} {
		tx := &scriptTx{plan: cheapPlan, failOn: "EXPLAIN", failErr: c.err}
		q, _, l := newQuerier(tx)
		res, err := q.Query(context.Background(), dqManager, `{"view":"ai.orders"}`)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if res.Headline != c.headline || (c.notice == "") != (res.Notice == nil) || (res.Notice != nil && res.Notice.Code != c.notice) {
			t.Errorf("%s: res = %+v", name, res)
		}
		dump := fmt.Sprintf("%+v %+v %+v", res, err, l.rows)
		if strings.Contains(dump, "SECRET-VALUE") || strings.Contains(dump, "claims()") || strings.Contains(dump, "PL/pgSQL") {
			t.Errorf("%s: database text leaked: %s", name, dump)
		}
		if len(l.rows) != 1 || l.rows[0].Refusal == "" {
			t.Errorf("%s: log = %+v", name, l.rows)
		}
	}
	// A coded error from the read path (the other audience, an unreadable claim) is handled the same way.
	q, _, l := newQuerier(&scriptTx{})
	q.Reader = &scriptDoer{err: readtx.MapError(&pgconn.PgError{Code: "XX000", Message: leak})}
	res, _ := q.Query(context.Background(), dqManager, `{"view":"ai.orders"}`)
	if res.Headline != QueryUnavailable || strings.Contains(fmt.Sprint(res, l.rows), "SECRET") {
		t.Errorf("res = %+v", res)
	}
}

func TestQueryRefusesWithAClassAndNeverRunsAnything(t *testing.T) {
	tx := &scriptTx{plan: cheapPlan}
	q, d, l := newQuerier(tx)
	// The real catalogue has no gated column yet, so a made up column is unknown (no class oracle).
	_, err := q.Query(context.Background(), dqManager, `{"view":"ai.orders","select":["cost_cents"]}`)
	if err == nil || strings.Contains(err.Error(), "cost_cents") {
		t.Errorf("err = %v, want a fixed refusal", err)
	}
	if len(tx.ran) != 0 || d.claims.AskerID != "" {
		t.Errorf("the database was reached: %v", tx.ran)
	}
	if len(l.rows) != 1 || l.rows[0].Refusal != "invalid" {
		t.Errorf("log = %+v", l.rows)
	}
}

func TestQueryCutsAResultThatIsTooLargeAndSaysSo(t *testing.T) {
	big := strings.Repeat("x", 9000)
	var data [][]any
	for i := 0; i < 5; i++ {
		data = append(data, []any{big, "1", fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i), "SO"})
	}
	q, _, _ := newQuerier(&scriptTx{plan: cheapPlan, names: orderCols, data: data})
	res, err := q.Query(context.Background(), dqManager, `{"view":"ai.orders","select":["order_no","total_cents"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) >= 5 || !strings.Contains(res.Note, "cut") {
		t.Errorf("items=%d note=%q", len(res.Items), res.Note)
	}
}

func TestQueryForAGlobalCallerScopesAllBranches(t *testing.T) {
	q, _, _ := newQuerier(&scriptTx{plan: cheapPlan, names: orderCols})
	res, err := q.Query(context.Background(), Caller{UserID: "u", Tier: 1, Global: true}, `{"view":"ai.orders","where":[{"col":"created_at","op":"between","value":["2000-02-01","2000-02-28"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Scope.AllBranches || res.Scope.Period == nil || res.Scope.Period.Label != "2000-02-01 to 2000-02-28" || !res.Empty {
		t.Errorf("scope = %+v empty=%v", res.Scope, res.Empty)
	}
}

// A citation label is the record's own label column, whatever personal text the row carries.
func TestQueryLabelNeverCarriesAPersonalColumn(t *testing.T) {
	tx := &scriptTx{plan: cheapPlan, names: []string{"order_no", "customer_name", "__id", "__label"}, data: [][]any{
		{"SO-1", "Pat Example", "11111111-1111-1111-1111-111111111111", "SO-1"},
	}}
	q, _, l := newQuerier(tx)
	res, err := q.Query(context.Background(), dqManager, `{"view":"ai.orders","select":["order_no","customer_name"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Items[0].Ref.Label; got != "Order SO-1" || strings.Contains(got, "Pat") {
		t.Errorf("label = %q", got)
	}
	if strings.Contains(fmt.Sprintf("%+v", l.rows), "Pat Example") {
		t.Errorf("a personal value reached the query log: %+v", l.rows)
	}
}
