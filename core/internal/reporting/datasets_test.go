package reporting

// datasets_test.go, the report writer's curated data sets: the catalog's shape, the read engine's
// arithmetic, and every refusal in words. These run DB-free over fake sources; the ties to the owning
// modules live in cmd/core/w3rw1_datasets_integration_test.go over the production w3rw1DatasetSources.
//
// THE RULE THE TESTS PIN (W3-RW1): a read names its columns from the catalog, filters them with the
// operators the catalog carries per column, and groups by the columns the catalog marks groupable.
// Anything else, an unknown column, an operator the column does not carry, a value that is not the
// column's kind, is refused with a sentence that names what was wrong. No caller input ever reaches
// SQL: the sources are typed reads behind each owning module's own seam, and the engine only ever
// filters, groups and sums the typed rows they returned.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// -- fake sources -------------------------------------------------------------

type fakeARSource struct {
	lines []ARAgingLine
	asOf  string
}

func (f *fakeARSource) ReadARAging(_ context.Context, asOf string) ([]ARAgingLine, error) {
	f.asOf = asOf
	return f.lines, nil
}

type fakeAPSource struct {
	lines []APAgingLine
	asOf  string
}

func (f *fakeAPSource) ReadAPAging(_ context.Context, asOf string) ([]APAgingLine, error) {
	f.asOf = asOf
	return f.lines, nil
}

type fakeGLSource struct{ lines []GLJournalLine }

func (f *fakeGLSource) ReadGLJournalLines(_ context.Context, year, month int) ([]GLJournalLine, error) {
	if year != 1971 || month != 3 {
		return nil, nil
	}
	return f.lines, nil
}

type fakeSalesSource struct {
	lines    []SalesInvoiceLine
	from, to time.Time
}

func (f *fakeSalesSource) ReadSalesLines(_ context.Context, from, to time.Time) ([]SalesInvoiceLine, error) {
	f.from, f.to = from, to
	return f.lines, nil
}

type fakeStockSource struct{ lines []StockOnHandLine }

func (f *fakeStockSource) ReadStockOnHand(context.Context) ([]StockOnHandLine, error) {
	return f.lines, nil
}

// datasetTestService builds the service with every source bound and a global caller, the pose a
// controller runs a consolidated read in.
func datasetTestService(ar *fakeARSource) *Service {
	svc := NewService(NewRepository(nil))
	if ar != nil {
		svc = svc.WithDatasetARSource(ar)
	}
	return svc.
		WithDatasetAPSource(&fakeAPSource{}).
		WithDatasetGLSource(&fakeGLSource{}).
		WithDatasetSalesSource(&fakeSalesSource{}).
		WithDatasetStockSource(&fakeStockSource{}).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{
				UserID: "user-1", RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
				Scope: identity.ScopeGlobal, Status: "ACTIVE",
			}, true
		})
}

// -- the catalog --------------------------------------------------------------

func TestDatasetCatalog_CarriesTheFiveSetsAndABranchColumnEach(t *testing.T) {
	sets, err := datasetTestService(nil).DatasetCatalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	got := map[string]Dataset{}
	for _, d := range sets {
		got[d.Set] = d
	}
	for _, want := range []string{"ar", "ap", "gl", "sales", "stock"} {
		d, ok := got[want]
		if !ok {
			t.Fatalf("the catalog is missing the %q set (has: %v)", want, keysOf(sets))
		}
		var branch *DatasetColumn
		for i := range d.Columns {
			c := &d.Columns[i]
			switch c.Kind {
			case KindText, KindInt, KindCents, KindQty, KindDate, KindBool:
			default:
				t.Fatalf("set %s column %s carries unknown kind %q", want, c.Name, c.Kind)
			}
			if c.Sum && c.Kind != KindCents && c.Kind != KindInt && c.Kind != KindQty {
				t.Fatalf("set %s column %s sums but its kind %s cannot total", want, c.Name, c.Kind)
			}
			if c.Name == "branch_id" {
				branch = c
			}
		}
		if branch == nil {
			t.Fatalf("set %s carries no branch_id column", want)
		}
		if !branch.Filter || !branch.Group {
			t.Fatalf("set %s branch_id must filter and group (got filter=%v group=%v)",
				want, branch.Filter, branch.Group)
		}
	}
}

func keysOf(sets []Dataset) string {
	names := make([]string, 0, len(sets))
	for _, d := range sets {
		names = append(names, d.Set)
	}
	return strings.Join(names, ",")
}

// -- refusals, in words -------------------------------------------------------

func TestRunDataset_RefusesUnknownColumnInWords(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"customer_name", "sum(total_cents)"},
	})
	if err == nil {
		t.Fatal("an expression shaped column pick must be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "sum(total_cents)") || !strings.Contains(msg, "ar") {
		t.Fatalf("the refusal must name the column and the set in words, got: %s", msg)
	}
}

func TestRunDataset_RefusesUnknownSet(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	if _, err := svc.RunDataset(context.Background(), "free_queries", DatasetQuery{Columns: []string{"x"}}); err != ErrNotFound {
		t.Fatalf("an unknown set is not found, got %v", err)
	}
}

func TestRunDataset_RefusesUnknownOperatorInWords(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "ap", DatasetQuery{
		Columns: []string{"vendor_name"},
		Filters: []DatasetFilter{{Column: "bucket", Op: "like", Values: []any{"CURRENT"}}},
	})
	if err == nil {
		t.Fatal("an operator the catalog does not carry must be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "like") || !strings.Contains(msg, "bucket") {
		t.Fatalf("the refusal must name the operator and the column in words, got: %s", msg)
	}
}

func TestRunDataset_RefusesFilterOnAColumnThatCannotFilter(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "gl", DatasetQuery{
		Columns: []string{"account_code", "debit_cents"},
		Filters: []DatasetFilter{{Column: "account_name", Op: "eq", Values: []any{"Cash"}}},
	})
	if err == nil {
		t.Fatal("a filter on a column the catalog does not mark filterable must be refused")
	}
	if msg := err.Error(); !strings.Contains(msg, "account_name") {
		t.Fatalf("the refusal must name the column, got: %s", msg)
	}
}

func TestRunDataset_RefusesAnOperatorTheColumnKindDoesNotCarry(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"customer_id"},
		Filters: []DatasetFilter{{Column: "customer_id", Op: "ge", Values: []any{"M"}}},
	})
	if err == nil {
		t.Fatal("a comparison operator on a text column must be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ge") || !strings.Contains(msg, "customer_id") {
		t.Fatalf("the refusal must name the operator and the column, got: %s", msg)
	}
}

func TestRunDataset_RefusesAValueThatIsNotTheColumnKind(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"total_cents"},
		Filters: []DatasetFilter{{Column: "total_cents", Op: "eq", Values: []any{"abc"}}},
	})
	if err == nil {
		t.Fatal("a cents filter carrying a non numeric value must be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "total_cents") {
		t.Fatalf("the refusal must name the column, got: %s", msg)
	}
	// Half cents are not cents either.
	_, err = svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"total_cents"},
		Filters: []DatasetFilter{{Column: "total_cents", Op: "eq", Values: []any{0.5}}},
	})
	if err == nil {
		t.Fatal("a half cent filter value must be refused")
	}
}

func TestRunDataset_RefusesEmptyColumns(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{})
	if err == nil || !strings.Contains(err.Error(), "column") {
		t.Fatalf("a read with no columns is refused in words, got: %v", err)
	}
}

func TestRunDataset_RefusesGroupByAColumnThatCannotGroup(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "gl", DatasetQuery{
		Columns: []string{"journal_memo", "debit_cents"},
		Filters: []DatasetFilter{
			{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
		},
		GroupBy: []string{"journal_memo"},
	})
	if err == nil || !strings.Contains(err.Error(), "journal_memo") {
		t.Fatalf("grouping by a column the catalog does not mark groupable is refused in words, got: %v", err)
	}
}

func TestRunDataset_RefusesGroupByOutsideTheRequestedColumns(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"total_cents"},
		GroupBy: []string{"branch_id"},
	})
	if err == nil || !strings.Contains(err.Error(), "branch_id") {
		t.Fatalf("grouping by a column the read does not carry is refused in words, got: %v", err)
	}
}

func TestRunDataset_RefusesAGroupedRequestThatCarriesADimensionlessColumn(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"branch_id", "customer_name", "total_cents"},
		GroupBy: []string{"branch_id"},
	})
	if err == nil || !strings.Contains(err.Error(), "customer_name") {
		t.Fatalf("a grouped read carrying a column that neither groups nor sums is refused in words, got: %v", err)
	}
}

func TestRunDataset_GLNeedsAFiscalYear(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "gl", DatasetQuery{Columns: []string{"debit_cents"}})
	if err == nil || !strings.Contains(err.Error(), "fiscal_year") {
		t.Fatalf("the gl set refuses an unbounded read in words, got: %v", err)
	}
}

func TestRunDataset_SalesNeedsADatedWindow(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	_, err := svc.RunDataset(context.Background(), "sales", DatasetQuery{Columns: []string{"line_total_cents"}})
	if err == nil || !strings.Contains(err.Error(), "invoice_date") {
		t.Fatalf("the sales set refuses an unbounded read in words, got: %v", err)
	}
}

func TestRunDataset_RefusesASecondFilterOnOneColumnInWords(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	// Duplicate pushdown filter on fiscal_year must be refused in words, never silently dropped.
	_, err := svc.RunDataset(context.Background(), "gl", DatasetQuery{
		Columns: []string{"debit_cents"},
		Filters: []DatasetFilter{
			{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
			{Column: "fiscal_year", Op: "eq", Values: []any{1972}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "fiscal_year") {
		t.Fatalf("a second filter on fiscal_year must be refused in words, got: %v", err)
	}

	// Duplicate filter on a regular column must be refused in words.
	_, err = svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"total_cents"},
		Filters: []DatasetFilter{
			{Column: "customer_id", Op: "eq", Values: []any{"c1"}},
			{Column: "customer_id", Op: "eq", Values: []any{"c2"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "customer_id") {
		t.Fatalf("a second filter on customer_id must be refused in words, got: %v", err)
	}

	// Duplicate ge bound on sales invoice_date must be refused in words, never overwritten.
	_, err = svc.RunDataset(context.Background(), "sales", DatasetQuery{
		Columns: []string{"line_total_cents"},
		Filters: []DatasetFilter{
			{Column: "invoice_date", Op: "ge", Values: []any{"1971-03-01"}},
			{Column: "invoice_date", Op: "ge", Values: []any{"1971-03-10"}},
			{Column: "invoice_date", Op: "le", Values: []any{"1971-03-31"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invoice_date") {
		t.Fatalf("a second ge filter on invoice_date must be refused in words, got: %v", err)
	}
}

func TestRunDataset_SalesRefusesInvertedOrOverwideWindowInWords(t *testing.T) {
	svc := datasetTestService(&fakeARSource{})
	// Inverted range (from > to).
	_, err := svc.RunDataset(context.Background(), "sales", DatasetQuery{
		Columns: []string{"line_total_cents"},
		Filters: []DatasetFilter{
			{Column: "invoice_date", Op: "ge", Values: []any{"1971-03-31"}},
			{Column: "invoice_date", Op: "le", Values: []any{"1971-03-01"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invoice_date") {
		t.Fatalf("an inverted sales window must be refused in words, got: %v", err)
	}

	// Window wider than 366 days.
	_, err = svc.RunDataset(context.Background(), "sales", DatasetQuery{
		Columns: []string{"line_total_cents"},
		Filters: []DatasetFilter{
			{Column: "invoice_date", Op: "ge", Values: []any{"1970-01-01"}},
			{Column: "invoice_date", Op: "le", Values: []any{"1972-01-01"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invoice_date") {
		t.Fatalf("a sales window wider than 366 days must be refused in words, got: %v", err)
	}
}

func TestRunDataset_ABranchOutsideTheReachIsForbidden(t *testing.T) {
	svc := datasetTestService(&fakeARSource{}).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{
				UserID: "user-1", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
				Scope: identity.ScopeBranch, Status: "ACTIVE",
				Branches: map[string]bool{"branch-a": true},
			}, true
		})
	_, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"branch_id", "total_cents"},
		Filters: []DatasetFilter{{Column: "branch_id", Op: "eq", Values: []any{"branch-b"}}},
	})
	if err != ErrForbidden {
		t.Fatalf("a branch outside the reach is forbidden, got %v", err)
	}
}

func TestRunDataset_PassesTheHouseDayToTheAgingSource(t *testing.T) {
	ar := &fakeARSource{lines: []ARAgingLine{}}
	ap := &fakeAPSource{lines: []APAgingLine{}}
	svc := datasetTestService(ar).WithDatasetAPSource(ap)
	if _, err := svc.RunDataset(context.Background(), "ar", DatasetQuery{
		Columns: []string{"total_cents"},
		AsOf:    "1971-03-15",
	}); err != nil {
		t.Fatalf("ar as_of read: %v", err)
	}
	if ar.asOf != "1971-03-15" {
		t.Fatalf("the AR source must receive the YYYY-MM-DD house day, got %q", ar.asOf)
	}
	if _, err := svc.RunDataset(context.Background(), "ap", DatasetQuery{
		Columns: []string{"open_cents"},
		AsOf:    "1971-03-15",
	}); err != nil {
		t.Fatalf("ap as_of read: %v", err)
	}
	if ap.asOf != "1971-03-15" {
		t.Fatalf("the AP source must receive the YYYY-MM-DD house day, got %q", ap.asOf)
	}
}

// -- the engine's arithmetic --------------------------------------------------

func TestRunDataset_GroupBySumsTheRestAndOrdersTheGroups(t *testing.T) {
	stock := &fakeStockSource{lines: []StockOnHandLine{
		{ProductID: "p1", BranchID: "b", OnHandBase: 4, UnitCostCents: 1000, ValueCents: 4000},
		{ProductID: "p2", BranchID: "a", OnHandBase: 1.5, UnitCostCents: 200, ValueCents: 300},
		{ProductID: "p3", BranchID: "b", OnHandBase: 2, UnitCostCents: 500, ValueCents: 1000},
	}}
	svc := datasetTestService(nil).WithDatasetStockSource(stock)
	res, err := svc.RunDataset(context.Background(), "stock", DatasetQuery{
		Columns: []string{"branch_id", "on_hand_base", "value_cents"},
		GroupBy: []string{"branch_id"},
	})
	if err != nil {
		t.Fatalf("grouped read: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("two branches, two groups, got %d rows: %v", len(res.Rows), res.Rows)
	}
	// Groups order by their key: a before b.
	if res.Rows[0][0] != "a" || res.Rows[1][0] != "b" {
		t.Fatalf("groups must order by the key, got %v", res.Rows)
	}
	if res.Rows[0][1] != 1.5 || res.Rows[0][2] != int64(300) {
		t.Fatalf("branch a sums 1.5 on hand and 300 cents, got %v", res.Rows[0])
	}
	if res.Rows[1][1] != 6.0 || res.Rows[1][2] != int64(5000) {
		t.Fatalf("branch b sums 6 on hand and 5000 cents, got %v", res.Rows[1])
	}
	if res.Totals["value_cents"] != int64(5300) {
		t.Fatalf("totals carry 5300 cents, got %v", res.Totals)
	}
}

func TestRunDataset_RawReadKeepsTheSourceOrderAndTotalsTheSums(t *testing.T) {
	gl := &fakeGLSource{lines: []GLJournalLine{
		{AccountCode: "1000", FiscalYear: 1971, FiscalMonth: 3, DebitCents: 5000, CreditCents: 0},
		{AccountCode: "1100", FiscalYear: 1971, FiscalMonth: 3, DebitCents: 0, CreditCents: 5000},
	}}
	svc := datasetTestService(nil).WithDatasetGLSource(gl)
	res, err := svc.RunDataset(context.Background(), "gl", DatasetQuery{
		Columns: []string{"account_code", "debit_cents", "credit_cents"},
		Filters: []DatasetFilter{
			{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
			{Column: "fiscal_month", Op: "eq", Values: []any{3}},
		},
	})
	if err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("the read keeps the source's rows, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "1000" || res.Rows[1][0] != "1100" {
		t.Fatalf("the read keeps the source's order, got %v", res.Rows)
	}
	if res.Totals["debit_cents"] != int64(5000) || res.Totals["credit_cents"] != int64(5000) {
		t.Fatalf("totals sum the money columns, got %v", res.Totals)
	}
}

func TestRunDataset_FiltersApplyInGoOverTheTypedRows(t *testing.T) {
	ap := &fakeAPSource{lines: []APAgingLine{
		{VendorName: "north", BranchID: "b1", Kind: "BILL", Bucket: "CURRENT", OpenCents: 100},
		{VendorName: "south", BranchID: "b2", Kind: "BILL", Bucket: "1_30", OpenCents: 200},
		{VendorName: "east", BranchID: "b1", Kind: "LANDED_COST", Bucket: "CURRENT", OpenCents: 400},
	}}
	svc := datasetTestService(nil).WithDatasetAPSource(ap)
	res, err := svc.RunDataset(context.Background(), "ap", DatasetQuery{
		Columns: []string{"vendor_name", "open_cents"},
		Filters: []DatasetFilter{
			{Column: "branch_id", Op: "eq", Values: []any{"b1"}},
			{Column: "kind", Op: "eq", Values: []any{"BILL"}},
		},
	})
	if err != nil {
		t.Fatalf("filtered read: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "north" {
		t.Fatalf("the filters cut to north's bill alone, got %v", res.Rows)
	}
	if res.Totals["open_cents"] != int64(100) {
		t.Fatalf("totals answer 100 cents, got %v", res.Totals)
	}
}

func TestRunDataset_AnUnwiredSourceIsNamedNotHidden(t *testing.T) {
	svc := NewService(NewRepository(nil)). // no stock source bound
						WithDatasetAPSource(&fakeAPSource{}).
						WithDatasetGLSource(&fakeGLSource{}).
						WithDatasetSalesSource(&fakeSalesSource{}).
						WithDatasetARSource(&fakeARSource{}).
						withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{
				UserID: "u", RoleCode: "FINANCIAL_CONTROLLER",
				Scope: identity.ScopeGlobal, Status: "ACTIVE",
			}, true
		})
	_, err := svc.RunDataset(context.Background(), "stock", DatasetQuery{Columns: []string{"value_cents"}})
	if err == nil || !strings.Contains(err.Error(), "stock") {
		t.Fatalf("an unwired source answers with the set named, got: %v", err)
	}
}

type roleSourceFunc func(ctx context.Context, platformUserID, orgID string) ([]string, error)

func (f roleSourceFunc) ResolveRoles(ctx context.Context, platformUserID, orgID string) ([]string, error) {
	return f(ctx, platformUserID, orgID)
}

func TestRunDataset_EachSetAgreesWithItsOwnerRouteAcrossEveryRole(t *testing.T) {
	owners := []struct {
		set    string
		method string
		path   string
		query  DatasetQuery
	}{
		{
			set: "ar", method: "GET", path: "/api/v1/ar/aging",
			query: DatasetQuery{Columns: []string{"total_cents"}},
		},
		{
			set: "ap", method: "GET", path: "/api/v1/purchasing/ap-aging",
			query: DatasetQuery{Columns: []string{"open_cents"}},
		},
		{
			set: "gl", method: "GET", path: "/api/v1/gl/trial-balance",
			query: DatasetQuery{
				Columns: []string{"debit_cents"},
				Filters: []DatasetFilter{
					{Column: "fiscal_year", Op: "eq", Values: []any{1971}},
					{Column: "fiscal_month", Op: "eq", Values: []any{3}},
				},
			},
		},
		{
			set: "sales", method: "GET", path: "/api/v1/ar/register",
			query: DatasetQuery{
				Columns: []string{"line_total_cents"},
				Filters: []DatasetFilter{
					{Column: "invoice_date", Op: "ge", Values: []any{"1971-03-01"}},
					{Column: "invoice_date", Op: "le", Values: []any{"1971-03-31"}},
				},
			},
		},
		{
			set: "stock", method: "GET", path: "/api/v1/inventory/on-hand",
			query: DatasetQuery{Columns: []string{"value_cents"}},
		},
	}

	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	gate, err := authz.NewRoleGate(&authz.Gate{}, roleSourceFunc(func(_ context.Context, userID, _ string) ([]string, error) {
		return []string{userID}, nil
	}), cfg)
	if err != nil {
		t.Fatalf("NewRoleGate: %v", err)
	}

	for _, o := range owners {
		ownerAccess, ok := authz.RouteAccessFor(o.method, o.path)
		if !ok {
			t.Fatalf("set %s owner route %s %s has no RouteAccess in apigen.Routes", o.set, o.method, o.path)
		}

		for _, role := range roleconfig.Roles() {
			t.Run(o.set+"/"+role, func(t *testing.T) {
				ctx := authz.WithStaffScope(context.Background(), authz.StaffScope{
					PlatformUserID: role,
					OrgID:          "org-1",
				})
				ownerAllowed, err := gate.Holds(ctx, role, "org-1", ownerAccess)
				if err != nil {
					t.Fatalf("owner route Holds(%s %s, %s): %v", o.method, o.path, role, err)
				}

				svc := datasetTestService(&fakeARSource{}).
					WithDatasetRoleGate(gate).
					WithRoleConfig(cfg).
					withCaller(func(context.Context) (identity.StaffContext, bool) {
						return identity.StaffContext{
							UserID:   "user-" + role,
							RoleCode: role,
							Scope:    identity.ScopeGlobal,
							Status:   "ACTIVE",
						}, true
					})
				_, err = svc.RunDataset(ctx, o.set, o.query)
				setAllowed := err == nil
				if !setAllowed && !errors.Is(err, ErrForbidden) {
					t.Fatalf("set %s role %s returned unexpected non-forbidden error: %v", o.set, role, err)
				}
				if setAllowed != ownerAllowed {
					t.Fatalf("set %s role %s: RunDataset allowed=%v (err=%v), owner route (%s %s) allowed=%v",
						o.set, role, setAllowed, err, o.method, o.path, ownerAllowed)
				}

				// The in-process service path (no StaffScope on ctx, roleconfig.Config.Holds over StaffContext.RoleCode)
				// must answer the same across every role and every set.
				svcNoScope := datasetTestService(&fakeARSource{}).
					WithRoleConfig(cfg).
					withCaller(func(context.Context) (identity.StaffContext, bool) {
						return identity.StaffContext{
							UserID:   "user-" + role,
							RoleCode: role,
							Scope:    identity.ScopeGlobal,
							Status:   "ACTIVE",
						}, true
					})
				_, err = svcNoScope.RunDataset(context.Background(), o.set, o.query)
				noScopeAllowed := err == nil
				if !noScopeAllowed && !errors.Is(err, ErrForbidden) {
					t.Fatalf("set %s role %s (roleconfig path) returned unexpected non-forbidden error: %v", o.set, role, err)
				}
				if noScopeAllowed != ownerAllowed {
					t.Fatalf("set %s role %s (roleconfig path): RunDataset allowed=%v (err=%v), owner route (%s %s) allowed=%v",
						o.set, role, noScopeAllowed, err, o.method, o.path, ownerAllowed)
				}
			})
		}
	}
}
