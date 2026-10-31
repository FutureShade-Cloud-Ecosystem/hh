//go:build integration

package reporting

// saved_reports_integration_test.go, W3-RW2's database half: the owner gate and its 404, the run time
// reach and role gate, the rerun equality, the CSV equal to the run, and filter values that stay bound
// parameters. Every saved report a leg creates is deleted by its own id, in the leg's cleanup, and a
// failed delete fails the test naming the table.

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rw2TwoUsers returns two distinct seeded users, picked by id order so the pick holds, to act as the owner
// and a stranger. Neither is written to.
func rw2TwoUsers(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT id::text FROM users WHERE status='ACTIVE' ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatalf("read users: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan user: %v", err)
		}
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		t.Fatalf("the saved report legs need two seeded active users, found %d", len(ids))
	}
	return ids[0], ids[1]
}

// rw2Actor is a switchable caller, so one service can pose as the owner, a stranger, or the owner with a
// narrower reach or a role the data set refuses.
type rw2Actor struct{ c identity.StaffContext }

func (a *rw2Actor) get(context.Context) (identity.StaffContext, bool) { return a.c, true }

func rw2Controller(userID string) identity.StaffContext {
	return identity.StaffContext{UserID: userID, RoleCode: "FINANCIAL_CONTROLLER",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

func rw2Svc(t *testing.T, pool *pgxpool.Pool, who *rw2Actor, ar *fakeARSource) *Service {
	t.Helper()
	return NewService(NewRepository(pool)).WithDatasetARSource(ar).withCaller(who.get)
}

// keep registers the delete of one saved report by id, failing loudly on an error.
func rw2Keep(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM report_saved_reports WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("cleanup of table report_saved_reports failed: %v", err)
		}
	})
}

func rw2ARLines() []ARAgingLine {
	return []ARAgingLine{
		{CustomerID: "c1", CustomerName: "Alpha", BranchID: trentonBranch, TotalCents: 500},
		{CustomerID: "c2", CustomerName: "Bravo", BranchID: kingstonBranch, TotalCents: 900},
		{CustomerID: "c3", CustomerName: "Charlie", BranchID: trentonBranch, TotalCents: 500},
		{CustomerID: "c4", CustomerName: "=Delta", BranchID: trentonBranch, TotalCents: -75},
	}
}

func rw2Def(name string) SavedReportDefinition {
	return SavedReportDefinition{
		Name: name, Dataset: "ar", Columns: []string{"customer_name", "branch_id", "total_cents"},
		Sort: []SavedReportSort{{Column: "total_cents", Desc: true}},
	}
}

func TestIntegration_RW2_OnlyTheOwnerReadsEditsRunsOrDeletes_AStrangerGets404(t *testing.T) {
	pool := intPool(t)
	owner, stranger := rw2TwoUsers(t, pool)
	who := &rw2Actor{c: rw2Controller(owner)}
	svc := rw2Svc(t, pool, who, &fakeARSource{lines: rw2ARLines()})
	ctx := context.Background()

	made, err := svc.CreateSavedReport(ctx, rw2Def("RW2 gate "+owner[:8]))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rw2Keep(t, pool, made.ID)

	who.c = rw2Controller(stranger)
	if _, err := svc.GetSavedReport(ctx, made.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger's read must be not found, got %v", err)
	}
	if _, err := svc.RunSavedReport(ctx, made.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger's run must be not found, got %v", err)
	}
	if _, _, err := svc.ExportSavedReportCSV(ctx, made.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger's export must be not found, got %v", err)
	}
	if _, err := svc.UpdateSavedReport(ctx, made.ID, rw2Def("hijacked")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger's edit must be not found, got %v", err)
	}
	if err := svc.DeleteSavedReport(ctx, made.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger's delete must be not found, got %v", err)
	}
	// An id that never existed answers the same, so the answer does not say whether an id exists.
	if _, err := svc.GetSavedReport(ctx, "00000000-0000-4000-8000-00000000dead"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing id is not found, got %v", err)
	}
	if _, err := svc.GetSavedReport(ctx, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a malformed id is not found, got %v", err)
	}
	list, err := svc.ListSavedReports(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range list {
		if r.ID == made.ID {
			t.Fatal("a stranger's list must not carry the owner's report")
		}
	}

	who.c = rw2Controller(owner)
	got, err := svc.GetSavedReport(ctx, made.ID)
	if err != nil || got.Name != made.Name {
		t.Fatalf("the owner still reads it untouched: %v %+v", err, got)
	}
	edited := rw2Def(made.Name)
	edited.Columns = []string{"customer_name", "total_cents"}
	edited.Sort = nil
	up, err := svc.UpdateSavedReport(ctx, made.ID, edited)
	if err != nil || len(up.Columns) != 2 {
		t.Fatalf("the owner edits: %v %+v", err, up)
	}
	if err := svc.DeleteSavedReport(ctx, made.ID); err != nil {
		t.Fatalf("the owner deletes: %v", err)
	}
	if _, err := svc.GetSavedReport(ctx, made.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a deleted report is gone, got %v", err)
	}
}

func TestIntegration_RW2_ANameIsOneReportPerOwner(t *testing.T) {
	pool := intPool(t)
	owner, stranger := rw2TwoUsers(t, pool)
	who := &rw2Actor{c: rw2Controller(owner)}
	svc := rw2Svc(t, pool, who, &fakeARSource{lines: rw2ARLines()})
	ctx := context.Background()
	name := "RW2 name " + owner[:8]
	a, err := svc.CreateSavedReport(ctx, rw2Def(name))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rw2Keep(t, pool, a.ID)
	if _, err := svc.CreateSavedReport(ctx, rw2Def(strings.ToUpper(name))); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("a second report under the same name is refused in words, got %v", err)
	}
	who.c = rw2Controller(stranger)
	b, err := svc.CreateSavedReport(ctx, rw2Def(name))
	if err != nil {
		t.Fatalf("another owner may use the same name: %v", err)
	}
	rw2Keep(t, pool, b.ID)
}

func TestIntegration_RW2_RunAppliesTheCallersReachAndRoleAtRunTime(t *testing.T) {
	pool := intPool(t)
	owner, _ := rw2TwoUsers(t, pool)
	who := &rw2Actor{c: rw2Controller(owner)}
	svc := rw2Svc(t, pool, who, &fakeARSource{lines: rw2ARLines()})
	ctx := context.Background()

	d := rw2Def("RW2 reach " + owner[:8])
	d.Filters = []DatasetFilter{{Column: "branch_id", Op: "eq", Values: []any{kingstonBranch}}}
	made, err := svc.CreateSavedReport(ctx, d)
	if err != nil {
		t.Fatalf("a global owner saves a Kingston report: %v", err)
	}
	rw2Keep(t, pool, made.ID)
	if res, err := svc.RunSavedReport(ctx, made.ID); err != nil || res.RowCount != 1 {
		t.Fatalf("while the owner reaches Kingston the run answers: %v %+v", err, res.RowCount)
	}

	// The same owner, now reaching Trenton alone: the report was saved with wider reach, the run uses
	// today's.
	who.c = identity.StaffContext{UserID: owner, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{trentonBranch: true}}
	if _, err := svc.RunSavedReport(ctx, made.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a run outside today's reach is forbidden, got %v", err)
	}
	if _, _, err := svc.ExportSavedReportCSV(ctx, made.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an export outside today's reach is forbidden, got %v", err)
	}

	// The same owner holding a role the money desk refuses: the data set's role gate is read at run
	// time, not at save time.
	who.c = identity.StaffContext{UserID: owner, RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeGlobal, Status: "ACTIVE"}
	if _, err := svc.RunSavedReport(ctx, made.ID); !errors.Is(err, ErrDatasetRoleForbidden) {
		t.Fatalf("a role the data set's owner surface refuses cannot run it, got %v", err)
	}
	if _, err := svc.CreateSavedReport(ctx, rw2Def("RW2 refused "+owner[:8])); !errors.Is(err, ErrDatasetRoleForbidden) {
		t.Fatalf("nor save one, got %v", err)
	}
}

func TestIntegration_RW2_AReRunIsTheSameRowsOnTheSameData_WhateverOrderTheSourceAnswersIn(t *testing.T) {
	pool := intPool(t)
	owner, _ := rw2TwoUsers(t, pool)
	who := &rw2Actor{c: rw2Controller(owner)}
	src := &fakeARSource{lines: rw2ARLines()}
	svc := rw2Svc(t, pool, who, src)
	ctx := context.Background()

	made, err := svc.CreateSavedReport(ctx, rw2Def("RW2 rerun "+owner[:8]))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rw2Keep(t, pool, made.ID)
	first, err := svc.RunSavedReport(ctx, made.ID)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	rev := rw2ARLines()
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	src.lines = rev
	second, err := svc.RunSavedReport(ctx, made.ID)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if first.RowCount != 4 || first.RowCount != second.RowCount {
		t.Fatalf("row counts %d vs %d", first.RowCount, second.RowCount)
	}
	for i := range first.Rows {
		for j := range first.Rows[i] {
			if first.Rows[i][j] != second.Rows[i][j] {
				t.Fatalf("row %d differs between runs: %v vs %v", i, first.Rows[i], second.Rows[i])
			}
		}
	}
	if first.Rows[0][2] != int64(900) || first.Rows[3][2] != int64(-75) {
		t.Fatalf("the saved sort (balance descending) holds: %v", first.Rows)
	}
}

func TestIntegration_RW2_TheCSVIsTheRunRowForRow(t *testing.T) {
	pool := intPool(t)
	owner, _ := rw2TwoUsers(t, pool)
	who := &rw2Actor{c: rw2Controller(owner)}
	svc := rw2Svc(t, pool, who, &fakeARSource{lines: rw2ARLines()})
	ctx := context.Background()
	made, err := svc.CreateSavedReport(ctx, rw2Def("RW2 csv "+owner[:8]))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rw2Keep(t, pool, made.ID)
	res, err := svc.RunSavedReport(ctx, made.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	name, body, err := svc.ExportSavedReportCSV(ctx, made.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.HasSuffix(name, ".csv") || strings.ContainsAny(name, " \"/\\") {
		t.Fatalf("the download name is a safe file name, got %q", name)
	}
	recs := parseCSV(t, body)
	if len(recs) != res.RowCount+2 { // header, rows, total
		t.Fatalf("csv records %d, run rows %d", len(recs), res.RowCount)
	}
	for i, row := range res.Rows {
		for j, c := range res.Columns {
			want := formatCell(c.Kind, row[j])
			if c.Kind == KindText {
				if strings.ContainsRune("=+-@\t\r", rune(want[0])) {
					want = "'" + want
				}
			}
			if recs[i+1][j] != want {
				t.Fatalf("csv row %d col %d = %q, run says %q", i, j, recs[i+1][j], want)
			}
		}
	}
	if recs[1][2] != "9.00" || recs[4][2] != "-0.75" {
		t.Fatalf("money is two decimal strings from cents: %v", recs)
	}
	if recs[4][0] != "'=Delta" {
		t.Fatalf("a customer named with a leading = is quoted in the file, got %q", recs[4][0])
	}
}

func TestIntegration_RW2_FilterValuesAreBoundParametersNeverSQLText(t *testing.T) {
	pool := intPool(t)
	owner, _ := rw2TwoUsers(t, pool)
	who := &rw2Actor{c: rw2Controller(owner)}
	svc := rw2Svc(t, pool, who, &fakeARSource{lines: rw2ARLines()})
	ctx := context.Background()
	hostile := "x'); DROP TABLE report_saved_reports; --"
	d := rw2Def("RW2 bound " + owner[:8])
	d.Filters = []DatasetFilter{{Column: "customer_id", Op: "eq", Values: []any{hostile}}}
	made, err := svc.CreateSavedReport(ctx, d)
	if err != nil {
		t.Fatalf("create with a hostile value: %v", err)
	}
	rw2Keep(t, pool, made.ID)
	got, err := svc.GetSavedReport(ctx, made.ID)
	if err != nil || got.Filters[0].Values[0] != hostile {
		t.Fatalf("the value round trips as data: %v %+v", err, got.Filters)
	}
	res, err := svc.RunSavedReport(ctx, made.ID)
	if err != nil || res.RowCount != 0 {
		t.Fatalf("the hostile value matches nothing and breaks nothing: %v %d", err, res.RowCount)
	}
	// Numbers survive the store as numbers, not floats.
	d2 := rw2Def("RW2 number " + owner[:8])
	d2.Filters = []DatasetFilter{{Column: "terms_days", Op: "ge", Values: []any{int64(30)}}}
	m2, err := svc.CreateSavedReport(ctx, d2)
	if err != nil {
		t.Fatalf("create with a number: %v", err)
	}
	rw2Keep(t, pool, m2.ID)
	if _, err := svc.RunSavedReport(ctx, m2.ID); err != nil {
		t.Fatalf("a stored number filter runs: %v", err)
	}
}
