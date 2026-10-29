package reporting

// datasets.go, the report writer's curated data sets (W3-RW1): five typed sources, each read through
// its owning module's own seam, with a column catalog per set and ONE read engine over the typed rows.
//
// -- NO FREE QUERIES, AND NO CALLER INPUT ANYWHERE NEAR SQL -------------------------------------------
// The rule Colton approved is the negative half of this file's whole shape. A read names columns from
// the set's catalog, filters them with the operators the catalog carries per column, and groups by the
// columns the catalog marks groupable. Everything else is refused in words. Nothing a caller sends is
// ever assembled into SQL: the sources are typed Go reads the owning module serves, and this engine
// filters, groups and sums the typed rows in Go. The worst a malformed read can do is refuse itself.
//
// -- THE SEAMS ----------------------------------------------------------------------------------------
// Each set names the module that owns its facts, and reads them through that module's own exported
// read (billing's aging, purchasing's AP aging, gl's journal lines, billing's invoice lines,
// inventory's stock valuation). This package holds NO SQL for any of them and imports no peer module:
// the sources are consumer-declared interfaces (seams.go's discipline), and the composition root binds
// the owners. A branch fenced caller is fenced twice over: reporting refuses a named branch outside
// the caller's reach before the seam is called, and each owner cuts its own read to the stamped
// caller's reach, the zero-trust shape every module keeps for itself.
//
// -- WHAT THE ENGINE DOES, AND DOES NOT, GUARANTEE ----------------------------------------------------
// Totals are sums of the requested summable columns over the returned rows, so a grouped read's totals
// are the sums of its groups and a raw read's totals are the sums of its lines: the two answers agree
// by construction. Order is the source's own order for a raw read and the group key's ascending order
// for a grouped one. Nothing here persists: a dataset read is a read, and the artefacts, saved
// reports and CSV of the report writer are later wave items that will run over this same engine.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// -- the row types -------------------------------------------------------------------
// One struct per set, typed end to end: money is int64 cents, quantities are float64, dates are
// YYYY-MM-DD strings, never a timezone in flight. The owner modules never see these types and this
// package never sees the owners': the composition root's adapters copy field by field.

// ARAgingLine is one account's aging row, the read the month-end AR console renders. BranchID is the
// account's home branch, the branch the aging surface reports the account under.
type ARAgingLine struct {
	CustomerID       string
	AccountNo        string
	CustomerName     string
	BranchID         string
	TermsDays        int
	CreditLimitCents int64
	CurrentCents     int64 // not yet due
	Days30Cents      int64 // 1 to 30 days past due
	Days60Cents      int64 // 31 to 60 days past due
	Days60To90Cents  int64 // 61 to 90 days past due
	Days90PlusCents  int64 // 91 days past due and over
	TotalCents       int64
}

// APAgingLine is one open payables obligation, a vendor bill or a landed cost payable, as the AP
// aging reports it.
type APAgingLine struct {
	VendorID   string
	VendorName string
	BranchID   string
	Kind       string // BILL or the landed cost payable kind
	Bucket     string
	RefNo      string
	BillDate   string
	DueDate    string
	TotalCents int64
	PaidCents  int64
	OpenCents  int64
}

// GLJournalLine is one leg of one POSTED journal, on the filed period basis the trial balance reads
// (the FW1-01 rule): FiscalYear and FiscalMonth name the period the figure landed in, not the day it
// was posted.
type GLJournalLine struct {
	AccountCode string
	AccountName string
	FiscalYear  int
	FiscalMonth int
	BranchID    string
	EntryNo     int
	JournalMemo string
	DebitCents  int64
	CreditCents int64
}

// SalesInvoiceLine is one line of one live invoice (never DRAFT or VOID).
type SalesInvoiceLine struct {
	InvoiceID      string
	InvoiceNo      string
	CustomerID     string
	BranchID       string
	ProductID      string
	SourceKind     string
	InvoiceDate    string
	Description    string
	QtyBase        float64
	UnitPriceCents int64
	LineTotalCents int64
	LineTaxCents   int64
}

// StockOnHandLine is one product's on hand at one branch, valued the way the module's own valuation
// values it: the sum of the per balance rounded figures at the branch's weighted average cost, so the
// set's total is StockValueCents by construction.
type StockOnHandLine struct {
	ProductID     string
	BranchID      string
	OnHandBase    float64
	UnitCostCents int64
	ValueCents    int64
}

// -- the seams ------------------------------------------------------------------------

// ARAgingSource is billing's own aging read, as a data set source.
type ARAgingSource interface {
	ReadARAging(ctx context.Context, asOf time.Time) ([]ARAgingLine, error)
}

// APAgingSource is purchasing's own AP aging read, as a data set source.
type APAgingSource interface {
	ReadAPAging(ctx context.Context, asOf time.Time) ([]APAgingLine, error)
}

// GLJournalSource is gl's own posted journal lines read, as a data set source.
type GLJournalSource interface {
	ReadGLJournalLines(ctx context.Context, year, month int) ([]GLJournalLine, error)
}

// SalesSource is billing's own invoice lines read, as a data set source.
type SalesSource interface {
	ReadSalesLines(ctx context.Context, from, to time.Time) ([]SalesInvoiceLine, error)
}

// StockSource is inventory's own on hand valuation read, as a data set source.
type StockSource interface {
	ReadStockOnHand(ctx context.Context) ([]StockOnHandLine, error)
}

// WithDatasetARSource binds billing's aging read.
func (s *Service) WithDatasetARSource(src ARAgingSource) *Service { s.dsAR = src; return s }

// WithDatasetAPSource binds purchasing's AP aging read.
func (s *Service) WithDatasetAPSource(src APAgingSource) *Service { s.dsAP = src; return s }

// WithDatasetGLSource binds gl's journal lines read.
func (s *Service) WithDatasetGLSource(src GLJournalSource) *Service { s.dsGL = src; return s }

// WithDatasetSalesSource binds billing's invoice lines read.
func (s *Service) WithDatasetSalesSource(src SalesSource) *Service { s.dsSales = src; return s }

// WithDatasetStockSource binds inventory's on hand read.
func (s *Service) WithDatasetStockSource(src StockSource) *Service { s.dsStock = src; return s }

// DatasetRoleGate is the role gate's own seam (satisfied by *authz.RoleGate) for checking whether
// the caller holds the access the dataset's owning route names.
type DatasetRoleGate interface {
	Holds(ctx context.Context, platformUserID, orgID string, access authz.RouteAccess) (bool, error)
}

// WithDatasetRoleGate binds the role gate that checks each dataset against its owner route's surface.
func (s *Service) WithDatasetRoleGate(g DatasetRoleGate) *Service { s.dsGate = g; return s }

// WithRoleConfig binds the loaded role configuration for service-level dataset reads.
func (s *Service) WithRoleConfig(cfg *roleconfig.Config) *Service { s.roleCfg = cfg; return s }

// datasetOwnerRoutes maps each curated dataset to the owner route whose x-access annotation gates it:
// ar, ap, and gl on money-desk read; sales on GET /api/v1/ar/register (money-desk read); stock on
// GET /api/v1/inventory/on-hand (buying-and-stock read).
var datasetOwnerRoutes = map[string]struct {
	method string
	path   string
}{
	"ar":    {method: "GET", path: "/api/v1/ar/aging"},
	"ap":    {method: "GET", path: "/api/v1/purchasing/ap-aging"},
	"gl":    {method: "GET", path: "/api/v1/gl/trial-balance"},
	"sales": {method: "GET", path: "/api/v1/ar/register"},
	"stock": {method: "GET", path: "/api/v1/inventory/on-hand"},
}

// DatasetAccessFor returns the authz.RouteAccess of the route that owns the named dataset's facts.
func DatasetAccessFor(set string) (authz.RouteAccess, bool) {
	owner, ok := datasetOwnerRoutes[set]
	if !ok {
		return authz.RouteAccess{}, false
	}
	return authz.RouteAccessFor(owner.method, owner.path)
}

var (
	defaultRoleCfgOnce sync.Once
	defaultRoleCfg     *roleconfig.Config
	defaultRoleCfgErr  error
)

func fallbackRoleConfig() (*roleconfig.Config, error) {
	defaultRoleCfgOnce.Do(func() {
		defaultRoleCfg, defaultRoleCfgErr = roleconfig.Default()
	})
	return defaultRoleCfg, defaultRoleCfgErr
}

// authorizeDataset checks that the caller holds the surface and level of the route that owns the set's
// facts. On an HTTP request carrying authz.StaffScope and a wired DatasetRoleGate, it asks
// DatasetRoleGate.Holds directly; for an in-process service call carrying StaffContext, it checks
// roleconfig.Config.Holds over the same RouteAccess.
func (s *Service) authorizeDataset(ctx context.Context, set string, c identity.StaffContext) error {
	access, ok := DatasetAccessFor(set)
	if !ok {
		return ErrDatasetRoleForbidden
	}
	if s.dsGate != nil {
		if scope, hasScope := authz.StaffScopeFrom(ctx); hasScope && scope.PlatformUserID != "" && scope.OrgID != "" {
			held, err := s.dsGate.Holds(ctx, scope.PlatformUserID, scope.OrgID, access)
			if err != nil {
				return err
			}
			if !held {
				return ErrDatasetRoleForbidden
			}
			return nil
		}
		if c.RoleCode == "" {
			return ErrDatasetRoleForbidden
		}
	}
	cfg := s.roleCfg
	if cfg == nil {
		var err error
		cfg, err = fallbackRoleConfig()
		if err != nil {
			return err
		}
	}
	if c.RoleCode == "SUPER_ADMIN" ||
		c.RoleCode == authz.PlatformRoleSuperAdmin ||
		c.RoleCode == authz.PlatformRoleDealerAdmin ||
		c.RoleCode == authz.PlatformRoleFBOperator {
		if access.List == "" {
			return nil
		}
	}
	role, ok := identity.CanonicalForERPCode(c.RoleCode)
	if !ok && roleconfig.IsRole(c.RoleCode) {
		role = c.RoleCode
	}
	if role == "" {
		return ErrDatasetRoleForbidden
	}
	for _, surface := range access.Surfaces {
		held, err := cfg.Holds(role, surface, access.Level)
		if err != nil {
			return err
		}
		if held {
			return nil
		}
	}
	return ErrDatasetRoleForbidden
}

// -- the catalog ----------------------------------------------------------------------

// DatasetColumn is one entry of a set's column catalog: what the column is called, what kind of value
// it carries, and what a read may do with it. The catalog is the whole contract: a filter on a column
// that cannot filter, an operator the column's kind does not carry, a group by a column that cannot
// group, are all refusals, in words.
type DatasetColumn struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	// Filter marks a column a read may filter on. The operators a column carries follow its kind.
	Filter bool `json:"filter"`
	// Group marks a column a read may group by.
	Group bool `json:"group"`
	// Sum marks a column the engine totals, and sums when the read groups.
	Sum bool `json:"sum"`
}

// Dataset is one curated set: its catalog and the words a reader sees about it.
type Dataset struct {
	Set         string          `json:"set"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	AsOf        bool            `json:"as_of"`
	Columns     []DatasetColumn `json:"columns"`
}

// datasetFilterOp lists the operators each kind carries. A closed set, on purpose: an operator that is
// not here is a refusal, not a string that reaches a comparison.
var datasetFilterOp = map[string][]string{
	KindText:  {"eq", "ne", "in"},
	KindInt:   {"eq", "ne", "gt", "ge", "lt", "le", "in"},
	KindCents: {"eq", "ne", "gt", "ge", "lt", "le", "in"},
	KindQty:   {"eq", "ne", "gt", "ge", "lt", "le", "in"},
	KindDate:  {"eq", "ne", "gt", "ge", "lt", "le", "in"},
	KindBool:  {"eq"},
}

func datasetColumn(name, label, kind string, filter, group, sum bool) DatasetColumn {
	return DatasetColumn{Name: name, Label: label, Kind: kind, Filter: filter, Group: group, Sum: sum}
}

// datasetCatalog is the five sets. A set is a contract with one owning module; a column joins it only
// through that module's own read.
var datasetCatalog = map[string][]DatasetColumn{
	"ar": {
		datasetColumn("customer_id", "Customer ID", KindText, true, true, false),
		datasetColumn("account_no", "Account", KindText, false, true, false),
		datasetColumn("customer_name", "Customer", KindText, false, true, false),
		datasetColumn("branch_id", "Branch", KindText, true, true, false),
		datasetColumn("terms_days", "Terms (days)", KindInt, true, true, false),
		datasetColumn("credit_limit_cents", "Credit limit", KindCents, true, false, false),
		datasetColumn("current_cents", "Current", KindCents, false, false, true),
		datasetColumn("days_1_30_cents", "1 to 30 days past due", KindCents, false, false, true),
		datasetColumn("days_31_60_cents", "31 to 60 days past due", KindCents, false, false, true),
		datasetColumn("days_61_90_cents", "61 to 90 days past due", KindCents, false, false, true),
		datasetColumn("days_90_plus_cents", "90 and over days past due", KindCents, false, false, true),
		datasetColumn("total_cents", "Balance", KindCents, false, false, true),
	},
	"ap": {
		datasetColumn("vendor_id", "Vendor ID", KindText, true, true, false),
		datasetColumn("vendor_name", "Vendor", KindText, false, true, false),
		datasetColumn("branch_id", "Branch", KindText, true, true, false),
		datasetColumn("kind", "Kind", KindText, true, true, false),
		datasetColumn("bucket", "Bucket", KindText, true, true, false),
		datasetColumn("ref_no", "Reference", KindText, false, true, false),
		datasetColumn("bill_date", "Bill date", KindDate, true, true, false),
		datasetColumn("due_date", "Due date", KindDate, true, true, false),
		datasetColumn("total_cents", "Total", KindCents, false, false, true),
		datasetColumn("paid_cents", "Paid", KindCents, false, false, true),
		datasetColumn("open_cents", "Open", KindCents, false, false, true),
	},
	"gl": {
		datasetColumn("account_code", "Account", KindText, true, true, false),
		datasetColumn("account_name", "Account name", KindText, false, true, false),
		datasetColumn("fiscal_year", "Fiscal year", KindInt, true, true, false),
		datasetColumn("fiscal_month", "Month", KindInt, true, true, false),
		datasetColumn("branch_id", "Branch", KindText, true, true, false),
		datasetColumn("entry_no", "Entry", KindInt, false, false, false),
		datasetColumn("journal_memo", "Memo", KindText, false, false, false),
		datasetColumn("debit_cents", "Debit", KindCents, false, false, true),
		datasetColumn("credit_cents", "Credit", KindCents, false, false, true),
	},
	"sales": {
		datasetColumn("invoice_id", "Invoice ID", KindText, true, true, false),
		datasetColumn("invoice_no", "Invoice", KindText, true, true, false),
		datasetColumn("customer_id", "Customer ID", KindText, true, true, false),
		datasetColumn("branch_id", "Branch", KindText, true, true, false),
		datasetColumn("product_id", "Product ID", KindText, true, true, false),
		datasetColumn("source_kind", "Source", KindText, true, true, false),
		datasetColumn("invoice_date", "Invoice date", KindDate, true, true, false),
		datasetColumn("description", "Description", KindText, false, false, false),
		datasetColumn("qty_base", "Quantity", KindQty, false, false, true),
		datasetColumn("unit_price_cents", "Unit price", KindCents, true, false, false),
		datasetColumn("line_total_cents", "Line total", KindCents, false, false, true),
		datasetColumn("line_tax_cents", "Line tax", KindCents, false, false, true),
	},
	"stock": {
		datasetColumn("product_id", "Product ID", KindText, true, true, false),
		datasetColumn("branch_id", "Branch", KindText, true, true, false),
		datasetColumn("on_hand_base", "On hand", KindQty, false, false, true),
		datasetColumn("unit_cost_cents", "Unit cost", KindCents, true, false, false),
		datasetColumn("value_cents", "Value", KindCents, false, false, true),
	},
}

var datasetLabels = map[string]struct {
	label       string
	description string
	asOf        bool
}{
	"ar": {
		label: "Accounts receivable aging",
		description: "One row per account with an open balance: the aging buckets the month-end AR " +
			"console renders, read through billing's own aging. branch_id is the account's home branch.",
		asOf: true,
	},
	"ap": {
		label: "Accounts payable aging",
		description: "One row per open obligation: posted vendor bills and open landed cost payables, " +
			"aged the way the payables board ages them, read through purchasing's own aging.",
		asOf: true,
	},
	"gl": {
		label: "General ledger journal lines",
		description: "One row per leg of every POSTED journal, on the filed period basis the trial " +
			"balance reads, read through gl's own books. Reads one fiscal year at a time.",
	},
	"sales": {
		label: "Invoice lines",
		description: "One row per line of every live invoice, never DRAFT or VOID, read through " +
			"billing's own documents. Reads a window of issue dates.",
	},
	"stock": {
		label: "Stock on hand with value",
		description: "One row per product at a branch: on hand at the branch's weighted average cost, " +
			"valued the way the inventory module's own valuation values it.",
	},
}

// DatasetCatalog returns the five sets with their column catalogs, in the set name's order.
func (s *Service) DatasetCatalog(ctx context.Context) ([]Dataset, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	sets := make([]string, 0, len(datasetCatalog))
	for code := range datasetCatalog {
		sets = append(sets, code)
	}
	sort.Strings(sets)
	out := make([]Dataset, 0, len(sets))
	for _, set := range sets {
		meta := datasetLabels[set]
		out = append(out, Dataset{
			Set: set, Label: meta.label, Description: meta.description, AsOf: meta.asOf,
			Columns: datasetCatalog[set],
		})
	}
	return out, nil
}

// -- the read -------------------------------------------------------------------------

// DatasetFilter is one filter: a catalog column, an operator the column's kind carries, and the values
// to compare. Values arrive as JSON (the handler decodes numbers as json.Number so no money value ever
// rides a float) and are coerced against the column's kind before anything is compared.
type DatasetFilter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Values []any  `json:"values"`
}

// DatasetQuery is one read: the columns to project, the filters to apply, the group by, and, for the
// sets that carry one, the house day the read answers for.
type DatasetQuery struct {
	Columns []string        `json:"columns"`
	Filters []DatasetFilter `json:"filters,omitempty"`
	GroupBy []string        `json:"group_by,omitempty"`
	AsOf    string          `json:"as_of,omitempty"`
}

// DatasetResult is one executed read. Rows are positional and align with Columns by index; totals sum
// every requested summable column over the returned rows.
type DatasetResult struct {
	Set      string          `json:"set"`
	Label    string          `json:"label"`
	Columns  []DatasetColumn `json:"columns"`
	Rows     [][]any         `json:"rows"`
	RowCount int             `json:"row_count"`
	Totals   map[string]any  `json:"totals"`
	// Query echoes what was applied, the pushdowns included, so a screenshot of a read says what it
	// is a read of.
	Query DatasetQuery `json:"query"`
	RunAt time.Time    `json:"run_at"`
}

// datasetRow is one source line projected to its catalog cells, keyed by column name.
type datasetRow map[string]any

// RunDataset executes one curated read: validate against the catalog, fence the named branches, call
// the owning module's read, filter, group, sum. Nothing is persisted.
func (s *Service) RunDataset(ctx context.Context, set string, q DatasetQuery) (DatasetResult, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return DatasetResult{}, err
	}
	cols, ok := datasetCatalog[set]
	if !ok {
		return DatasetResult{}, ErrNotFound
	}
	if err := s.authorizeDataset(ctx, set, c); err != nil {
		return DatasetResult{}, err
	}
	meta := datasetLabels[set]

	// The columns. A read projects at least one, and every name must be the catalog's, byte for byte.
	if len(q.Columns) == 0 {
		return DatasetResult{}, errBadRequest("name at least one column of the %s set: its columns are %s",
			set, datasetColumnWords(cols))
	}
	seen := map[string]bool{}
	projected := make([]DatasetColumn, 0, len(q.Columns))
	for _, name := range q.Columns {
		col, ok := datasetColumnByName(cols, name)
		if !ok {
			return DatasetResult{}, errBadRequest(
				"the %s set has no column %q: its columns are %s", set, name, datasetColumnWords(cols))
		}
		if seen[name] {
			return DatasetResult{}, errBadRequest("column %q is named twice in one read", name)
		}
		seen[name] = true
		projected = append(projected, col)
	}

	// The filters. Each must name a filterable column and carry operators and values the column's kind
	// admits. The window pushdowns are consumed here too: the sources apply them in their own reads.
	filters, pushdown, err := validateDatasetFilters(set, cols, q.Filters)
	if err != nil {
		return DatasetResult{}, err
	}

	// The branch fence. A named branch outside the caller's reach is forbidden before any source is
	// asked; inside the read, each owner cuts its own rows to the stamped caller's reach.
	for _, f := range filters {
		if f.column.Name != "branch_id" {
			continue
		}
		for _, v := range f.values {
			branch, _ := v.(string)
			if !c.CanAccessBranch(branch) {
				return DatasetResult{}, ErrForbidden
			}
		}
	}

	// The group by. Its columns must be requested, groupable, and leave nothing requested that can
	// neither group nor sum.
	groupBy := make([]DatasetColumn, 0, len(q.GroupBy))
	for _, name := range q.GroupBy {
		col, ok := datasetColumnByName(cols, name)
		if !ok {
			return DatasetResult{}, errBadRequest(
				"the %s set has no column %q: its columns are %s", set, name, datasetColumnWords(cols))
		}
		if !col.Group {
			return DatasetResult{}, errBadRequest(
				"column %q of the %s set cannot group: a group by needs a column the catalog marks groupable", name, set)
		}
		if !seen[name] {
			return DatasetResult{}, errBadRequest(
				"group_by names %q, which the read does not carry: group by columns of the %s set must be among the requested columns", name, set)
		}
		groupBy = append(groupBy, col)
	}
	if len(groupBy) > 0 {
		for _, col := range projected {
			grouped := false
			for _, g := range groupBy {
				if g.Name == col.Name {
					grouped = true
					break
				}
			}
			// A groupable column the read does not group by is refused too, not silently dropped: its
			// per group value would be one row's value wearing the group's name, which is how a report
			// ends up with a customer column that lies.
			if !grouped && !col.Sum {
				return DatasetResult{}, errBadRequest(
					"a grouped read cannot carry %q: it is not in the group by and it cannot sum", col.Name)
			}
		}
	}

	// The read itself, through the owning module's own seam.
	rows, asOfEcho, err := s.readDatasetRows(ctx, set, q, pushdown)
	if err != nil {
		return DatasetResult{}, err
	}

	// The engine: filter the typed rows, project, group or keep, total.
	kept := make([]datasetRow, 0, len(rows))
	for _, row := range rows {
		if datasetMatches(row, filters) {
			kept = append(kept, row)
		}
	}
	var out [][]any
	if len(groupBy) > 0 {
		out, err = datasetGroupRows(kept, groupBy, projected)
		if err != nil {
			return DatasetResult{}, err
		}
	} else {
		out = make([][]any, 0, len(kept))
		for _, row := range kept {
			out = append(out, datasetProject(row, projected))
		}
	}
	totals := datasetTotals(projected, kept)

	echo := DatasetQuery{Columns: q.Columns, GroupBy: q.GroupBy, AsOf: asOfEcho}
	for _, f := range q.Filters {
		echo.Filters = append(echo.Filters, DatasetFilter{Column: f.Column, Op: f.Op, Values: f.Values})
	}
	return DatasetResult{
		Set: set, Label: meta.label, Columns: projected, Rows: out, RowCount: len(out),
		Totals: totals, Query: echo, RunAt: nowUTC(),
	}, nil
}

// readDatasetRows calls the set's owning module and returns its typed lines as catalog cells, with the
// as-of echo the result carries back. The pushdowns the engine consumed in validation (the gl fiscal
// year, the sales window) drive the call.
func (s *Service) readDatasetRows(ctx context.Context, set string, q DatasetQuery, pushdown datasetPushdown) ([]datasetRow, string, error) {
	switch set {
	case "ar":
		if s.dsAR == nil {
			return nil, "", errDatasetUnwired(set)
		}
		asOf, err := parseDatasetAsOf(q.AsOf)
		if err != nil {
			return nil, "", err
		}
		lines, err := s.dsAR.ReadARAging(ctx, asOf)
		if err != nil {
			return nil, "", err
		}
		out := make([]datasetRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, l.cells())
		}
		return out, q.AsOf, nil
	case "ap":
		if s.dsAP == nil {
			return nil, "", errDatasetUnwired(set)
		}
		asOf, err := parseDatasetAsOf(q.AsOf)
		if err != nil {
			return nil, "", err
		}
		lines, err := s.dsAP.ReadAPAging(ctx, asOf)
		if err != nil {
			return nil, "", err
		}
		out := make([]datasetRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, l.cells())
		}
		return out, q.AsOf, nil
	case "gl":
		if s.dsGL == nil {
			return nil, "", errDatasetUnwired(set)
		}
		lines, err := s.dsGL.ReadGLJournalLines(ctx, pushdown.glYear, pushdown.glMonth)
		if err != nil {
			return nil, "", err
		}
		out := make([]datasetRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, l.cells())
		}
		return out, "", nil
	case "sales":
		if s.dsSales == nil {
			return nil, "", errDatasetUnwired(set)
		}
		lines, err := s.dsSales.ReadSalesLines(ctx, pushdown.salesFrom, pushdown.salesTo)
		if err != nil {
			return nil, "", err
		}
		out := make([]datasetRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, l.cells())
		}
		return out, "", nil
	case "stock":
		if s.dsStock == nil {
			return nil, "", errDatasetUnwired(set)
		}
		lines, err := s.dsStock.ReadStockOnHand(ctx)
		if err != nil {
			return nil, "", err
		}
		out := make([]datasetRow, 0, len(lines))
		for _, l := range lines {
			out = append(out, l.cells())
		}
		return out, "", nil
	}
	return nil, "", ErrNotFound
}

// -- validation -----------------------------------------------------------------------

// datasetPushdown carries the filters a source applies in its own read: the gl set reads one fiscal
// year (and optionally one month) of its books, the sales set one window of issue dates.
type datasetPushdown struct {
	glYear, glMonth    int
	salesFrom, salesTo time.Time
}

// validateDatasetFilters checks every filter against the catalog and splits the pushdowns out. The
// returned list carries coerced values, ready to compare.
func validateDatasetFilters(set string, cols []DatasetColumn, in []DatasetFilter) ([]datasetFilter, datasetPushdown, error) {
	var pushdown datasetPushdown
	out := make([]datasetFilter, 0, len(in))
	for _, f := range in {
		col, ok := datasetColumnByName(cols, f.Column)
		if !ok {
			return nil, pushdown, errBadRequest(
				"the %s set has no column %q: its columns are %s", set, f.Column, datasetColumnWords(cols))
		}
		if !col.Filter {
			return nil, pushdown, errBadRequest(
				"column %q of the %s set cannot be filtered: the catalog marks no filter on it", col.Name, set)
		}
		if !datasetOpAllowed(col.Kind, f.Op) {
			return nil, pushdown, errBadRequest(
				"the operator %q is not one the %s set's column %q carries (%s): the catalog's operators for a %s column are %s",
				f.Op, set, col.Name, col.Kind, col.Kind, strings.Join(datasetFilterOp[col.Kind], ", "))
		}
		if f.Op == "in" && len(f.Values) < 1 {
			return nil, pushdown, errBadRequest("the operator in needs at least one value for %q", col.Name)
		}
		if f.Op != "in" && len(f.Values) != 1 {
			return nil, pushdown, errBadRequest(
				"the operator %s takes one value for %q, got %d", f.Op, col.Name, len(f.Values))
		}
		vals := make([]any, 0, len(f.Values))
		for _, v := range f.Values {
			coerced, err := datasetCoerce(col, v)
			if err != nil {
				return nil, pushdown, err
			}
			vals = append(vals, coerced)
		}
		filter := datasetFilter{column: col, op: f.Op, values: vals}

		// The pushdowns, consumed by the owning module's read instead of the Go side.
		switch {
		case set == "gl" && col.Name == "fiscal_year" && f.Op == "eq":
			pushdown.glYear = int(vals[0].(int64))
			if pushdown.glYear <= 0 {
				return nil, pushdown, errBadRequest("fiscal_year must be a whole year, got %d", pushdown.glYear)
			}
		case set == "gl" && col.Name == "fiscal_month" && f.Op == "eq":
			pushdown.glMonth = int(vals[0].(int64))
			if pushdown.glMonth < 1 || pushdown.glMonth > 12 {
				return nil, pushdown, errBadRequest("fiscal_month must be a whole month between 1 and 12, got %d", pushdown.glMonth)
			}
		case set == "sales" && col.Name == "invoice_date" && f.Op == "ge":
			pushdown.salesFrom = datasetDayStart(vals[0].(string))
		case set == "sales" && col.Name == "invoice_date" && f.Op == "le":
			pushdown.salesTo = datasetDayEnd(vals[0].(string))
		default:
			out = append(out, filter)
		}
	}
	if set == "gl" && pushdown.glYear == 0 {
		return nil, pushdown, errBadRequest(
			"the gl set reads one fiscal year at a time: give fiscal_year (for example fiscal_year eq 2026)")
	}
	if set == "sales" && (pushdown.salesFrom.IsZero() || pushdown.salesTo.IsZero()) {
		return nil, pushdown, errBadRequest(
			"the sales set reads a window of issue dates: give invoice_date from (ge) and to (le)")
	}
	return out, pushdown, nil
}

// datasetFilter is one validated filter: the catalog column, the operator, the coerced values.
type datasetFilter struct {
	column DatasetColumn
	op     string
	values []any
}

func datasetColumnByName(cols []DatasetColumn, name string) (DatasetColumn, bool) {
	for _, c := range cols {
		if c.Name == name {
			return c, true
		}
	}
	return DatasetColumn{}, false
}

func datasetOpAllowed(kind, op string) bool {
	for _, o := range datasetFilterOp[kind] {
		if o == op {
			return true
		}
	}
	return false
}

func datasetColumnWords(cols []DatasetColumn) string {
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

// datasetCoerce turns one filter value into the column kind's Go value, refusing in words whatever the
// kind does not admit. json.Number is the shape the handler decodes numbers into, so no money value
// ever rides a float; the native Go numbers are here so the tests (and any Go caller) can speak them.
func datasetCoerce(col DatasetColumn, v any) (any, error) {
	words := func(want string) error {
		return errBadRequest("column %q needs %s values, and %v is not one", col.Name, want, v)
	}
	switch col.Kind {
	case KindText:
		s, ok := v.(string)
		if !ok {
			return nil, words("text")
		}
		return s, nil
	case KindDate:
		s, ok := v.(string)
		if !ok {
			return nil, words("date (YYYY-MM-DD)")
		}
		if _, err := time.Parse("2006-01-02", s); err != nil || len(s) != 10 {
			return nil, errBadRequest("column %q needs a YYYY-MM-DD date, and %q is not one", col.Name, s)
		}
		return s, nil
	case KindInt, KindCents:
		n, ok := datasetWholeNumber(v)
		if !ok {
			return nil, words("whole " + col.Kind + " numbers")
		}
		return n, nil
	case KindQty:
		f, ok := datasetNumber(v)
		if !ok {
			return nil, words("numeric")
		}
		return f, nil
	case KindBool:
		b, ok := v.(bool)
		if !ok {
			return nil, words("true or false")
		}
		return b, nil
	}
	return nil, errBadRequest("column %q carries an unknown kind %q", col.Name, col.Kind)
}

// datasetWholeNumber reads any JSON number shaped value as an int64, refusing a fraction: money and
// counts cross as whole numbers or not at all.
func datasetWholeNumber(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		return 0, false
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
		return 0, false
	}
	return 0, false
}

func datasetNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
		return 0, false
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

func parseDatasetAsOf(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	d, err := time.Parse("2006-01-02", raw)
	if err != nil || len(raw) != 10 {
		return time.Time{}, errBadRequest("as_of must be a day (YYYY-MM-DD), and %q is not one", raw)
	}
	return d.Add(12 * time.Hour), nil
}

func datasetDayStart(day string) time.Time {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Time{}
	}
	return t
}

func datasetDayEnd(day string) time.Time {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Time{}
	}
	return t.Add(24*time.Hour - time.Nanosecond)
}

func errDatasetUnwired(set string) error {
	return fmt.Errorf("%w: the %s data set is not wired in this deployment", ErrDatasetUnwired, set)
}

// -- the engine -----------------------------------------------------------------------

// datasetMatches answers whether one row survives one validated filter set. Every comparison is over
// the coerced Go values against the typed cells: nothing here reads SQL.
func datasetMatches(row datasetRow, filters []datasetFilter) bool {
	for _, f := range filters {
		cell := row[f.column.Name]
		if !datasetMatchOne(f.op, f.column.Kind, cell, f.values) {
			return false
		}
	}
	return true
}

func datasetMatchOne(op, kind string, cell any, values []any) bool {
	switch op {
	case "in":
		for _, v := range values {
			if datasetEqual(kind, cell, v) {
				return true
			}
		}
		return false
	case "eq":
		return datasetEqual(kind, cell, values[0])
	case "ne":
		return !datasetEqual(kind, cell, values[0])
	}
	cmp, ok := datasetCompare(kind, cell, values[0])
	if !ok {
		return false
	}
	switch op {
	case "gt":
		return cmp > 0
	case "ge":
		return cmp >= 0
	case "lt":
		return cmp < 0
	case "le":
		return cmp <= 0
	}
	return false
}

func datasetEqual(kind string, a, b any) bool {
	switch kind {
	case KindText, KindDate:
		as, aok := a.(string)
		bs, bok := b.(string)
		return aok && bok && as == bs
	case KindInt, KindCents:
		ai, aok := a.(int64)
		bi, bok := b.(int64)
		return aok && bok && ai == bi
	case KindQty:
		af, aok := a.(float64)
		bf, bok := b.(float64)
		return aok && bok && af == bf
	case KindBool:
		ab, aok := a.(bool)
		bb, bok := b.(bool)
		return aok && bok && ab == bb
	}
	return false
}

// datasetCompare answers -1, 0 or 1 for the ordered kinds, and false for a kind that does not order.
func datasetCompare(kind string, a, b any) (int, bool) {
	switch kind {
	case KindText, KindDate:
		as, aok := a.(string)
		bs, bok := b.(string)
		if !aok || !bok {
			return 0, false
		}
		return strings.Compare(as, bs), true
	case KindInt, KindCents:
		ai, aok := a.(int64)
		bi, bok := b.(int64)
		if !aok || !bok {
			return 0, false
		}
		switch {
		case ai < bi:
			return -1, true
		case ai > bi:
			return 1, true
		}
		return 0, true
	case KindQty:
		af, aok := a.(float64)
		bf, bok := b.(float64)
		if !aok || !bok {
			return 0, false
		}
		switch {
		case af < bf:
			return -1, true
		case af > bf:
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// datasetProject reads one row's cells in the requested column order.
func datasetProject(row datasetRow, cols []DatasetColumn) []any {
	out := make([]any, 0, len(cols))
	for _, c := range cols {
		out = append(out, row[c.Name])
	}
	return out
}

// datasetGroupRows groups the rows by the group columns, orders the groups by their keys ascending,
// and sums every other requested column. A requested column that neither groups nor sums was refused
// in validation, so every cell here has an answer.
func datasetGroupRows(rows []datasetRow, groupBy, projected []DatasetColumn) ([][]any, error) {
	type group struct {
		keys []any
		sums map[string]any
	}
	groups := make(map[string]*group)
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		keys := make([]any, 0, len(groupBy))
		keyParts := make([]string, 0, len(groupBy))
		for _, g := range groupBy {
			v := row[g.Name]
			keys = append(keys, v)
			keyParts = append(keyParts, datasetKeyWord(g.Kind, v))
		}
		key := strings.Join(keyParts, "\x00")
		g, ok := groups[key]
		if !ok {
			g = &group{keys: keys, sums: map[string]any{}}
			groups[key] = g
			order = append(order, key)
		}
		for _, c := range projected {
			if c.Group {
				continue
			}
			g.sums[c.Name] = datasetAdd(c.Kind, g.sums[c.Name], row[c.Name])
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([][]any, 0, len(order))
	for _, key := range order {
		g := groups[key]
		row := make([]any, 0, len(projected))
		ki := 0
		for _, c := range projected {
			if c.Group {
				row = append(row, g.keys[ki])
				ki++
				continue
			}
			row = append(row, g.sums[c.Name])
		}
		out = append(out, row)
	}
	return out, nil
}

// datasetKeyWord renders one key cell into the sort key. Same kind, same rendering, same order: the
// groups of a read sort by their keys ascending, whatever the kind.
func datasetKeyWord(kind string, v any) string {
	switch kind {
	case KindInt, KindCents:
		if n, ok := v.(int64); ok {
			return strconv.FormatInt(n, 10)
		}
	case KindQty:
		if f, ok := v.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	case KindBool:
		if b, ok := v.(bool); ok {
			return strconv.FormatBool(b)
		}
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// datasetAdd folds one more cell into a running sum. Cents and counts sum as whole numbers, quantities
// as float, and a column with no value yet starts its sum from the first row.
func datasetAdd(kind string, sum, v any) any {
	switch kind {
	case KindInt, KindCents:
		n, _ := datasetWholeNumber(v)
		if s, ok := sum.(int64); ok {
			return s + n
		}
		return n
	case KindQty:
		f, _ := datasetNumber(v)
		if s, ok := sum.(float64); ok {
			return s + f
		}
		return f
	}
	return nil
}

// datasetTotals sums every requested summable column over all rows, grouped or not: a grouped read's
// totals and a raw read's totals are the same sums of the same lines.
func datasetTotals(cols []DatasetColumn, rows []datasetRow) map[string]any {
	totals := map[string]any{}
	for _, c := range cols {
		if !c.Sum {
			continue
		}
		var sum any
		for _, row := range rows {
			sum = datasetAdd(c.Kind, sum, row[c.Name])
		}
		totals[c.Name] = sum
	}
	return totals
}

// -- the cells ------------------------------------------------------------------------

func (l ARAgingLine) cells() datasetRow {
	return datasetRow{
		"customer_id":        l.CustomerID,
		"account_no":         l.AccountNo,
		"customer_name":      l.CustomerName,
		"branch_id":          l.BranchID,
		"terms_days":         int64(l.TermsDays),
		"credit_limit_cents": l.CreditLimitCents,
		"current_cents":      l.CurrentCents,
		"days_1_30_cents":    l.Days30Cents,
		"days_31_60_cents":   l.Days60Cents,
		"days_61_90_cents":   l.Days60To90Cents,
		"days_90_plus_cents": l.Days90PlusCents,
		"total_cents":        l.TotalCents,
	}
}

func (l APAgingLine) cells() datasetRow {
	return datasetRow{
		"vendor_id":   l.VendorID,
		"vendor_name": l.VendorName,
		"branch_id":   l.BranchID,
		"kind":        l.Kind,
		"bucket":      l.Bucket,
		"ref_no":      l.RefNo,
		"bill_date":   l.BillDate,
		"due_date":    l.DueDate,
		"total_cents": l.TotalCents,
		"paid_cents":  l.PaidCents,
		"open_cents":  l.OpenCents,
	}
}

func (l GLJournalLine) cells() datasetRow {
	return datasetRow{
		"account_code": l.AccountCode,
		"account_name": l.AccountName,
		"fiscal_year":  int64(l.FiscalYear),
		"fiscal_month": int64(l.FiscalMonth),
		"branch_id":    l.BranchID,
		"entry_no":     int64(l.EntryNo),
		"journal_memo": l.JournalMemo,
		"debit_cents":  l.DebitCents,
		"credit_cents": l.CreditCents,
	}
}

func (l SalesInvoiceLine) cells() datasetRow {
	return datasetRow{
		"invoice_id":       l.InvoiceID,
		"invoice_no":       l.InvoiceNo,
		"customer_id":      l.CustomerID,
		"branch_id":        l.BranchID,
		"product_id":       l.ProductID,
		"source_kind":      l.SourceKind,
		"invoice_date":     l.InvoiceDate,
		"description":      l.Description,
		"qty_base":         l.QtyBase,
		"unit_price_cents": l.UnitPriceCents,
		"line_total_cents": l.LineTotalCents,
		"line_tax_cents":   l.LineTaxCents,
	}
}

func (l StockOnHandLine) cells() datasetRow {
	return datasetRow{
		"product_id":      l.ProductID,
		"branch_id":       l.BranchID,
		"on_hand_base":    l.OnHandBase,
		"unit_cost_cents": l.UnitCostCents,
		"value_cents":     l.ValueCents,
	}
}
