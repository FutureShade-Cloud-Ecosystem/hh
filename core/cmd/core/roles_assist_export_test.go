package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

type exportStore struct {
	assist.SpendStore
	rows         []assist.SpendRow
	gotFrom, got time.Time
}

func (s *exportStore) Settled(_ context.Context, from, to time.Time) ([]assist.SpendRow, error) {
	s.gotFrom, s.got = from, to
	return s.rows, nil
}

func TestParseExportMonthIsTheBudgetWindow(t *testing.T) {
	from, to, err := parseExportMonth("2026-08", houseday.DefaultZone())
	if err != nil {
		t.Fatal(err)
	}
	wf, wt := assist.MonthWindow(time.Date(2026, 8, 20, 0, 0, 0, 0, houseday.Location()))
	if !from.Equal(wf) || !to.Equal(wt) {
		t.Fatalf("window %v to %v, want the budgets' %v to %v", from, to, wf, wt)
	}
	for _, bad := range []string{"", "2026", "2026-13", "08-2026", "2026-8-1"} {
		if _, _, err := parseExportMonth(bad, houseday.DefaultZone()); err == nil {
			t.Errorf("%q was accepted as a month", bad)
		}
	}
}

func TestAssistBillingExportRefusesBadArgumentsBeforeAnyConnection(t *testing.T) {
	noDB := func(string) string { return "" }
	for _, args := range [][]string{
		{},
		{"-month", "2026-08"},
		{"-org", "x"},
		{"-month", "2026-08", "-org", "x", "-format", "xml"},
	} {
		var out, errb bytes.Buffer
		if err := assistBillingExportRole(args, &out, &errb, noDB); err == nil {
			t.Errorf("%v ran", args)
		}
		if out.Len() != 0 {
			t.Errorf("%v wrote to standard output before refusing: %q", args, out.String())
		}
	}
}

func TestWriteAssistBillingExportWritesTheSettlementsAndNothingElse(t *testing.T) {
	st := &exportStore{rows: []assist.SpendRow{
		{Kind: assist.SpendSettlement, Audience: assist.AudienceStaff, StaffUserID: "u1", RoleCode: "R", Alias: "a", Model: "m", TokensIn: 10, TokensOut: 5, CostMicroCents: 7},
		{Kind: assist.SpendSettlement, Audience: assist.AudienceStaff, StaffUserID: "u1", RoleCode: "R", Alias: "a", Model: "m", TokensIn: 1, TokensOut: 1, CostMicroCents: 3},
	}}
	from, to, _ := parseExportMonth("2026-08", houseday.DefaultZone())
	var buf bytes.Buffer
	if err := writeAssistBillingExport(context.Background(), st, "hh-demo", "2026-08", from, to, "json", &buf); err != nil {
		t.Fatal(err)
	}
	if !st.gotFrom.Equal(from) {
		t.Fatalf("store was asked from %v, want %v", st.gotFrom, from)
	}
	var exp assist.BillingExport
	if err := json.Unmarshal(buf.Bytes(), &exp); err != nil {
		t.Fatalf("standard output is not the document alone: %v\n%s", err, buf.String())
	}
	if exp.Summary.Org != "hh-demo" || exp.Summary.CostMicroCents != 10 || exp.Summary.Requests != 2 {
		t.Fatalf("summary = %+v", exp.Summary)
	}
	buf.Reset()
	if err := writeAssistBillingExport(context.Background(), st, "hh-demo", "2026-08", from, to, "csv", &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "row_type,org,month,") || !strings.Contains(buf.String(), "summary,hh-demo,2026-08") {
		t.Fatalf("csv = %s", buf.String())
	}
}
