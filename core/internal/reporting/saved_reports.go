package reporting

// saved_reports.go, W3-RW2: saved reports over the curated data sets.
//
// -- WHAT A SAVED REPORT IS ----------------------------------------------------------------------------
// A stored definition, never a stored result: one data set, columns from its catalog, filters, a group
// by and a sort. Running it hands the definition to RunDataset, the same engine a direct read uses, so a
// saved report can do nothing a direct read cannot, and it is read through the set's own module seam.
//
// -- NO FREE QUERY -------------------------------------------------------------------------------------
// A definition is checked by planDataset, the catalog check RunDataset runs, when it is saved, when it
// is edited and again every time it runs. A column, filter field, operator, value kind or grouping
// outside the set's catalog is refused in words. Filter values are stored as JSON through a bound
// parameter and are only ever compared in Go by the engine; nothing in a definition becomes SQL text.
//
// -- WHO MAY USE ONE -----------------------------------------------------------------------------------
// The owner alone. Every repository call is keyed by the owner's user id, so another user's report is not
// found (404, never 403, so an id does not say it exists). No role reads another user's report: the plan
// names none for this slice, and a manager who needs the same view saves their own. What a RUN may see is
// decided when it runs, by the caller's own branch reach and the data set owner surface's role gate
// (RunDataset), not by what the owner could see when the report was saved.
//
// -- THE SAME ROWS EVERY TIME --------------------------------------------------------------------------
// The engine keeps the source's own order for a raw read, which an owning module does not promise. The
// saved sort runs first, and every requested column then breaks any tie in column order, so the rows of a
// run are a function of the data and the definition alone.

import (
	"bytes"
	"context"
	"encoding/csv"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/csvsafe"
)

// maxSavedReportName bounds a name, the same limit the table's check holds.
const maxSavedReportName = 120

// SavedReportSort is one sort key: a requested column and a direction.
type SavedReportSort struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

// SavedReportDefinition is what a caller supplies and what is stored: the set, and a read over it.
type SavedReportDefinition struct {
	Name    string            `json:"name"`
	Dataset string            `json:"dataset"`
	Columns []string          `json:"columns"`
	Filters []DatasetFilter   `json:"filters,omitempty"`
	GroupBy []string          `json:"group_by,omitempty"`
	AsOf    string            `json:"as_of,omitempty"`
	Sort    []SavedReportSort `json:"sort,omitempty"`
}

// SavedReport is a stored definition with its owner.
type SavedReport struct {
	ID          string `json:"id"`
	OwnerUserID string `json:"owner_user_id"`
	SavedReportDefinition
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SavedReportResult is one run: the engine's result for the definition, sorted, with the report's id and name.
type SavedReportResult struct {
	ReportID   string `json:"report_id"`
	ReportName string `json:"report_name"`
	DatasetResult
}

func (d SavedReportDefinition) query() DatasetQuery {
	return DatasetQuery{Columns: d.Columns, Filters: d.Filters, GroupBy: d.GroupBy, AsOf: d.AsOf}
}

// validateSavedReport checks a definition without reading any data: the name, the set, the role gate on
// the set's owner surface, the catalog check of the read, the as_of day and the sort.
func (s *Service) validateSavedReport(ctx context.Context, def *SavedReportDefinition) error {
	c, err := s.caller(ctx)
	if err != nil {
		return err
	}
	def.Name = strings.TrimSpace(def.Name)
	if def.Name == "" {
		return errBadRequest("a saved report needs a name")
	}
	if utf8.RuneCountInString(def.Name) > maxSavedReportName {
		return errBadRequest("a saved report's name is at most %d characters", maxSavedReportName)
	}
	cols, ok := datasetCatalog[def.Dataset]
	if !ok {
		return ErrNotFound
	}
	if err := s.authorizeDataset(ctx, def.Dataset, c); err != nil {
		return err
	}
	if _, err := parseDatasetAsOf(def.AsOf); err != nil {
		return err
	}
	if def.AsOf != "" && !datasetLabels[def.Dataset].asOf {
		return errBadRequest("the %s set does not answer as of a day: leave as_of out", def.Dataset)
	}
	plan, err := planDataset(c, def.Dataset, cols, def.query())
	if err != nil {
		return err
	}
	return validateSavedSort(def.Dataset, plan.projected, def.Sort)
}

func validateSavedSort(set string, projected []DatasetColumn, keys []SavedReportSort) error {
	seen := map[string]bool{}
	for _, k := range keys {
		found := false
		for _, c := range projected {
			if c.Name == k.Column {
				found = true
				break
			}
		}
		if !found {
			return errBadRequest("sort names %q, which the read does not carry: a sort column of the %s set must be among the requested columns",
				k.Column, set)
		}
		if seen[k.Column] {
			return errBadRequest("sort names %q twice: give each column once", k.Column)
		}
		seen[k.Column] = true
	}
	return nil
}

// CreateSavedReport stores a definition for the caller.
func (s *Service) CreateSavedReport(ctx context.Context, def SavedReportDefinition) (SavedReport, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return SavedReport{}, err
	}
	if err := s.validateSavedReport(ctx, &def); err != nil {
		return SavedReport{}, err
	}
	return s.repo.InsertSavedReport(ctx, c.UserID, def)
}

// UpdateSavedReport replaces one of the caller's own definitions.
func (s *Service) UpdateSavedReport(ctx context.Context, id string, def SavedReportDefinition) (SavedReport, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return SavedReport{}, err
	}
	if err := s.validateSavedReport(ctx, &def); err != nil {
		return SavedReport{}, err
	}
	return s.repo.UpdateSavedReport(ctx, id, c.UserID, def)
}

// ListSavedReports lists the caller's own reports, by name.
func (s *Service) ListSavedReports(ctx context.Context) ([]SavedReport, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListSavedReports(ctx, c.UserID)
}

// GetSavedReport reads one of the caller's own reports. Anyone else's is not found.
func (s *Service) GetSavedReport(ctx context.Context, id string) (SavedReport, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return SavedReport{}, err
	}
	return s.repo.GetSavedReport(ctx, id, c.UserID)
}

// DeleteSavedReport removes one of the caller's own reports.
func (s *Service) DeleteSavedReport(ctx context.Context, id string) error {
	c, err := s.caller(ctx)
	if err != nil {
		return err
	}
	return s.repo.DeleteSavedReport(ctx, id, c.UserID)
}

// RunSavedReport runs one of the caller's own reports. The read is RunDataset's, so the caller's
// branch reach and the set owner surface's role gate are applied as they stand now.
func (s *Service) RunSavedReport(ctx context.Context, id string) (SavedReportResult, error) {
	rep, err := s.GetSavedReport(ctx, id)
	if err != nil {
		return SavedReportResult{}, err
	}
	res, err := s.RunDataset(ctx, rep.Dataset, rep.query())
	if err != nil {
		return SavedReportResult{}, err
	}
	sortDatasetRows(res.Columns, res.Rows, rep.Sort)
	return SavedReportResult{ReportID: rep.ID, ReportName: rep.Name, DatasetResult: res}, nil
}

// ExportSavedReportCSV runs the report and renders that run as CSV. It returns a safe download name.
func (s *Service) ExportSavedReportCSV(ctx context.Context, id string) (string, []byte, error) {
	res, err := s.RunSavedReport(ctx, id)
	if err != nil {
		return "", nil, err
	}
	return sanitizeFilename("report_"+res.Set+"_"+res.ReportID[:8]) + ".csv", renderDatasetCSV(res.DatasetResult), nil
}

// sortDatasetRows orders rows in place by the saved keys and then by every column in order, so the
// result does not depend on the order the source answered in. Columns align with each row by index.
func sortDatasetRows(cols []DatasetColumn, rows [][]any, keys []SavedReportSort) {
	type key struct {
		idx  int
		desc bool
	}
	var order []key
	used := map[int]bool{}
	for _, k := range keys {
		for i, c := range cols {
			if c.Name == k.Column {
				order = append(order, key{i, k.Desc})
				used[i] = true
			}
		}
	}
	for i := range cols {
		if !used[i] {
			order = append(order, key{i, false})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		for _, k := range order {
			cmp := compareCells(cols[k.idx].Kind, rows[a][k.idx], rows[b][k.idx])
			if cmp == 0 {
				continue
			}
			if k.desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
}

// compareCells orders two cells of one kind. A cell that is not the kind's Go type sorts first.
func compareCells(kind string, a, b any) int {
	if c, ok := datasetCompare(kind, a, b); ok {
		return c
	}
	if kind == KindBool {
		ab, _ := a.(bool)
		bb, _ := b.(bool)
		switch {
		case ab == bb:
			return 0
		case !ab:
			return -1
		}
		return 1
	}
	return 0
}

// renderDatasetCSV writes one run as RFC 4180 CSV: the column labels, one record per row in the run's
// order, then a TOTAL record when the run carries totals. Money is the two decimal string of the integer
// cents (formatCell), so the file and the grid read the same figures. Every text cell and label goes
// through csvsafe.Cell, the formula leader rule the GL exports use; money cells never do, because a
// negative amount leads with a minus sign and must stay a number.
func renderDatasetCSV(res DatasetResult) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	cell := func(c DatasetColumn, v any) string {
		text := formatCell(c.Kind, v)
		if c.Kind == KindText || c.Kind == KindDate {
			return csvsafe.Cell(text)
		}
		return text
	}
	headers := make([]string, len(res.Columns))
	for i, c := range res.Columns {
		headers[i] = csvsafe.Cell(c.Label)
	}
	_ = w.Write(headers)
	for _, row := range res.Rows {
		rec := make([]string, len(res.Columns))
		for i, c := range res.Columns {
			rec[i] = cell(c, row[i])
		}
		_ = w.Write(rec)
	}
	if len(res.Totals) > 0 && len(res.Columns) > 0 {
		rec := make([]string, len(res.Columns))
		rec[0] = "TOTAL"
		for i, c := range res.Columns {
			if i == 0 {
				continue
			}
			if v, ok := res.Totals[c.Name]; ok {
				rec[i] = formatCell(c.Kind, v)
			}
		}
		_ = w.Write(rec)
	}
	w.Flush()
	return buf.Bytes()
}
