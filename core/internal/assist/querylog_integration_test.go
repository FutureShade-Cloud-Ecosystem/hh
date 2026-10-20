//go:build integration

package assist

import (
	"context"
	"strings"
	"testing"
)

func TestQueryLog_AppendsOutlinesAndRefusesEveryEdit(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	e := QueryLogEntry{
		ExchangeID: "ex-" + strings.ReplaceAll(user, "-", "")[:12], AskerID: user, Audience: "staff", Tool: "data.query",
		View: "ai.orders", Spec: []byte(`{"view":"ai.orders","where":[{"col":"status","op":"=","value":"OPEN"}]}`),
		RowCount: 3, CitedIDs: []string{"11111111-1111-1111-1111-111111111111"}, TokensIn: 10, TokensOut: 5,
	}
	if err := repo.AppendQueryLog(ctx, e); err != nil {
		t.Fatal(err)
	}
	var view, refusal string
	var rows, cited int
	var spec string
	if err := pool.QueryRow(ctx, `SELECT view, spec::text, row_count, jsonb_array_length(cited_ids), refusal_code FROM assist_query_log WHERE exchange_id = $1`, e.ExchangeID).
		Scan(&view, &spec, &rows, &cited, &refusal); err != nil {
		t.Fatal(err)
	}
	if view != "ai.orders" || rows != 3 || cited != 1 || refusal != "" || !strings.Contains(spec, "OPEN") {
		t.Errorf("row = %s %s %d %d %q", view, spec, rows, cited, refusal)
	}
	for _, stmt := range []string{
		`UPDATE assist_query_log SET row_count = 0 WHERE exchange_id = $1`,
		`DELETE FROM assist_query_log WHERE exchange_id = $1`,
	} {
		if _, err := pool.Exec(ctx, stmt, e.ExchangeID); err == nil {
			t.Errorf("%q was allowed on an append only table", stmt)
		}
	}
	if _, err := pool.Exec(ctx, `TRUNCATE assist_query_log`); err == nil {
		t.Error("TRUNCATE was allowed on an append only table")
	}
	// The table holds an outline: no column can carry a result row or an error text.
	rowsC, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_name = 'assist_query_log' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rowsC.Close()
	var cols []string
	for rowsC.Next() {
		var c string
		if err := rowsC.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	want := "asker_id,at,audience,cited_ids,exchange_id,id,refusal_code,row_count,spec,tokens_in,tokens_out,tool,view"
	if strings.Join(cols, ",") != want {
		t.Errorf("columns = %v, want %s", cols, want)
	}
}
