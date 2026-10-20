package assist

// export_test.go, slice 2 sub item 6: the billing export equals the spend table.

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func exportRows() []SpendRow {
	mk := func(ex string, aud Audience, staff, portal, role, alias, model string, in, out, cached int, cost int64) SpendRow {
		return SpendRow{Kind: SpendSettlement, ExchangeID: ex, CallNo: 1, Audience: aud, StaffUserID: staff, PortalUserID: portal,
			RoleCode: role, Alias: alias, Model: model, TokensIn: in, TokensOut: out, TokensCached: cached, CostMicroCents: cost, At: at(4)}
	}
	return []SpendRow{
		mk("e1", AudienceStaff, "u-sm", "", "SALES_MANAGER", "tanyai-main", "m1", 1000, 200, 50, 4000),
		mk("e2", AudienceStaff, "u-sm", "", "SALES_MANAGER", "tanyai-main", "m1", 500, 100, 0, 1900),
		mk("e3", AudienceStaff, "u-sm", "", "SALES_MANAGER", "tanyai-alt", "m2", 80, 20, 0, 300),
		mk("e4", AudienceStaff, "u-is", "", "INSIDE_SALES", "tanyai-main", "m1", 10, 5, 0, 60),
		mk("e5", AudiencePro, "", "p-1", "", "tanyai-main", "m1", 700, 90, 10, 2500),
		mk("e6", AudienceEmbed, "", "", "", "embed", "e1", 9000, 0, 0, 800),
		mk("e7", AudienceMessages, "u-sm", "", "SALES_MANAGER", "tanyai-main", "m1", 60, 30, 0, 250),
		{Kind: SpendSettlement, ExchangeID: "e10", CallNo: 1, Audience: AudienceStaff, StaffUserID: "u-is", RoleCode: "INSIDE_SALES",
			Alias: "tanyai-main", Model: "m1", TokensIn: 5, TokensOut: 5, CostMicroCents: 40, SettledBy: SettledBySweep, At: at(4)},
		// Not billable: an open reservation and a void.
		{Kind: SpendReservation, ExchangeID: "e8", CallNo: 1, Audience: AudienceStaff, StaffUserID: "u-sm", TokensIn: 99999, CostMicroCents: 99999, At: at(4)},
		{Kind: SpendVoid, ExchangeID: "e9", CallNo: 1, Audience: AudienceStaff, StaffUserID: "u-sm", At: at(4)},
	}
}

func TestExportTotalsEqualTheSpendTablesSums(t *testing.T) {
	sp := &memSpend{rows: exportRows()}
	from, to := MonthWindow(at(4))
	settled, err := sp.Settled(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	exp := BuildBillingExport("hh-demo", "2026-08", settled)

	var wantIn, wantOut, wantCached, wantCost int64
	var wantReq int
	for _, r := range sp.rows {
		if r.Kind == SpendSettlement {
			wantReq++
			wantIn += int64(r.TokensIn)
			wantOut += int64(r.TokensOut)
			wantCached += int64(r.TokensCached)
			wantCost += r.CostMicroCents
		}
	}
	var gotReq int
	var gotIn, gotOut, gotCached, gotCost int64
	for _, l := range exp.Lines {
		gotReq += l.Requests
		gotIn += l.TokensIn
		gotOut += l.TokensOut
		gotCached += l.TokensCached
		gotCost += l.CostMicroCents
	}
	if gotReq != wantReq || gotIn != wantIn || gotOut != wantOut || gotCached != wantCached || gotCost != wantCost {
		t.Fatalf("lines sum to %d/%d/%d/%d/%d, table sums to %d/%d/%d/%d/%d", gotReq, gotIn, gotOut, gotCached, gotCost, wantReq, wantIn, wantOut, wantCached, wantCost)
	}
	s := exp.Summary
	if s.Requests != wantReq || s.TokensIn != wantIn || s.TokensOut != wantOut || s.TokensCached != wantCached || s.CostMicroCents != wantCost {
		t.Fatalf("summary %+v does not equal the table's sums", s)
	}
	if s.SweptRequests != 1 || s.SweptCostMicroCents != 40 {
		t.Fatalf("swept part = %d / %d, want 1 / 40", s.SweptRequests, s.SweptCostMicroCents)
	}
	if s.Org != "hh-demo" || s.Month != "2026-08" {
		t.Fatalf("summary carries org %q month %q", s.Org, s.Month)
	}
	// The dealer cap's own number is the same table summed across audiences, so the export total
	// and the cap see one dealer total (the open reservation is the only difference, by design).
	capView, _ := sp.DealerMonthMicroCents(context.Background(), from, to)
	if capView != wantCost+99999 {
		t.Fatalf("cap view %d, want billable %d plus the open reservation's estimate", capView, wantCost)
	}
}

func TestExportGroupsPerUserRoleAudienceAliasAndModel(t *testing.T) {
	exp := BuildBillingExport("o", "2026-08", exportRows())
	type k struct {
		aud         Audience
		user, alias string
		model       string
	}
	got := map[k]ExportLine{}
	for _, l := range exp.Lines {
		got[k{l.Audience, l.UserID, l.Alias, l.Model}] = l
	}
	main := got[k{AudienceStaff, "u-sm", "tanyai-main", "m1"}]
	if main.Requests != 2 || main.TokensIn != 1500 || main.CostMicroCents != 5900 || main.RoleCode != "SALES_MANAGER" {
		t.Fatalf("u-sm on tanyai-main = %+v", main)
	}
	if _, ok := got[k{AudienceStaff, "u-sm", "tanyai-alt", "m2"}]; !ok {
		t.Fatal("a second alias for one user must be its own line")
	}
	if _, ok := got[k{AudienceMessages, "u-sm", "tanyai-main", "m1"}]; !ok {
		t.Fatal("the messages audience must be its own line, apart from staff")
	}
	if l, ok := got[k{AudiencePro, "p-1", "tanyai-main", "m1"}]; !ok || l.RoleCode != "" {
		t.Fatalf("pro line = %+v", l)
	}
	if _, ok := got[k{AudienceEmbed, "", "embed", "e1"}]; !ok {
		t.Fatal("the embed audience has a line with no user")
	}
	if len(exp.Lines) != 6 {
		t.Fatalf("%d lines, want 6", len(exp.Lines))
	}
}

func TestExportOrderIsStable(t *testing.T) {
	a := BuildBillingExport("o", "m", exportRows())
	rows := exportRows()
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	b := BuildBillingExport("o", "m", rows)
	var ba, bb bytes.Buffer
	_ = a.WriteJSON(&ba)
	_ = b.WriteJSON(&bb)
	if ba.String() != bb.String() {
		t.Fatal("the export depends on the order the rows arrived in")
	}
}

func TestExportJSONAndCSVCarryTheSameNumbers(t *testing.T) {
	exp := BuildBillingExport("hh-demo", "2026-08", exportRows())

	var jb bytes.Buffer
	if err := exp.WriteJSON(&jb); err != nil {
		t.Fatal(err)
	}
	var back BillingExport
	if err := json.Unmarshal(jb.Bytes(), &back); err != nil || back.Summary != exp.Summary || len(back.Lines) != len(exp.Lines) {
		t.Fatalf("json round trip: %v %+v", err, back.Summary)
	}

	var cb bytes.Buffer
	if err := exp.WriteCSV(&cb); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(cb.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(exp.Lines)+2 || strings.Join(recs[0], ",") != strings.Join(CSVHeader, ",") {
		t.Fatalf("csv has %d records, header %v", len(recs), recs[0])
	}
	last := recs[len(recs)-1]
	if last[0] != "summary" || last[1] != "hh-demo" || last[len(last)-1] != strconv.FormatInt(exp.Summary.CostMicroCents, 10) {
		t.Fatalf("summary row = %v", last)
	}
	var csvCost int64
	for _, r := range recs[1 : len(recs)-1] {
		v, _ := strconv.ParseInt(r[len(r)-1], 10, 64)
		csvCost += v
	}
	if csvCost != exp.Summary.CostMicroCents {
		t.Fatalf("csv line costs sum to %d, summary says %d", csvCost, exp.Summary.CostMicroCents)
	}
}

func TestExportOfAnEmptyMonthIsEmptyNotNull(t *testing.T) {
	var jb bytes.Buffer
	if err := BuildBillingExport("o", "2026-07", nil).WriteJSON(&jb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jb.String(), `"lines": []`) {
		t.Fatalf("empty month json = %s", jb.String())
	}
}
