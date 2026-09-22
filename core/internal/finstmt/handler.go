package finstmt

// handler.go, the finstmt HTTP surface. Statements answer JSON by default and, when ?format=csv|xlsx is
// present, stream the CSV / Excel-compatible export (with an A-21 access stamp). Schedules CRUD is
// registered admin-only (see module.go RegisterRoutes). Frozen: the handler struct, NewHandler, and the
// RegisterRoutes signature (in module.go).

import (
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/export"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
	"strconv"
	"strings"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// -- statement reads ----------------------------------------------------------

func (h *Handler) incomeStatement(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	year, ok := h.queryYear(w, r)
	if !ok {
		return
	}
	month := atoiOr(r.URL.Query().Get("month"), 0)
	branch := r.URL.Query().Get("branch_id")
	if r.URL.Query().Get("compare") == "1" {
		rows, err := h.svc.IncomeStatementComparison(r.Context(), year, month, branch)
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": rows})
		return
	}
	rep, err := h.svc.IncomeStatementReportFor(r.Context(), year, month, branch)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if format := r.URL.Query().Get("format"); format != "" {
		h.svc.recordExport(r.Context(), "INCOME_STATEMENT", format, identity.StaffUserIDFromContext(r.Context()), branch)
		if isXLSX(format) {
			q := export.StatementQuery{Report: export.ReportIncomeStatement, Year: year, Month: month, BranchID: branch, BranchName: branchNameParam(r)}
			h.writeXLSX(w, r, export.StatementFilename(export.ReportIncomeStatement, year, month), h.svc.incomeStatementWorkbook(rep, q))
			return
		}
		h.writeExport(w, format, "income-statement", incomeStatementTable(rep))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handler) balanceSheet(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	year, ok := h.queryYear(w, r)
	if !ok {
		return
	}
	branch := r.URL.Query().Get("branch_id")
	if r.URL.Query().Get("compare") == "1" {
		rows, err := h.svc.BalanceSheetComparison(r.Context(), year, branch)
		if err != nil {
			h.writeErr(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": rows})
		return
	}
	rep, err := h.svc.BalanceSheetReportFor(r.Context(), year, branch)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if format := r.URL.Query().Get("format"); format != "" {
		h.svc.recordExport(r.Context(), "BALANCE_SHEET", format, identity.StaffUserIDFromContext(r.Context()), branch)
		if isXLSX(format) {
			q := export.StatementQuery{Report: export.ReportBalanceSheet, Year: year, BranchID: branch, BranchName: branchNameParam(r)}
			h.writeXLSX(w, r, export.StatementFilename(export.ReportBalanceSheet, year, 0), h.svc.balanceSheetWorkbook(rep, q))
			return
		}
		h.writeExport(w, format, "balance-sheet", balanceSheetTable(rep))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handler) trialBalance(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	year, ok := h.queryYear(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.TrialBalanceReportFor(r.Context(), year)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if format := r.URL.Query().Get("format"); format != "" {
		h.svc.recordExport(r.Context(), "TRIAL_BALANCE", format, identity.StaffUserIDFromContext(r.Context()), "")
		if isXLSX(format) {
			q := export.StatementQuery{Report: export.ReportTrialBalance, Year: year}
			h.writeXLSX(w, r, export.StatementFilename(export.ReportTrialBalance, year, 0), h.svc.trialBalanceWorkbook(rep, q))
			return
		}
		h.writeExport(w, format, "trial-balance", trialBalanceTable(rep))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handler) glDetail(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	f := GLDetailFilter{
		Year:        atoiOr(q.Get("year"), 0),
		Month:       atoiOr(q.Get("month"), 0),
		AccountCode: q.Get("account_code"),
		BranchID:    q.Get("branch_id"),
	}
	rows, err := h.svc.GLDetail(r.Context(), f)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if format := q.Get("format"); format != "" {
		h.svc.recordExport(r.Context(), "GL_DETAIL", format, identity.StaffUserIDFromContext(r.Context()), f.BranchID)
		if isXLSX(format) {
			// DIB-203: the GL detail exports as the TWO-sheet transaction workbook (per-account
			// summary + every posted line), built from the rows already read above, never a second
			// query, which would be a second definition of the total inside one file.
			tq := export.TransactionQuery{Year: f.Year, Month: f.Month, AccountCode: f.AccountCode, BranchID: f.BranchID, BranchName: branchNameParam(r)}
			h.writeXLSX(w, r, export.TransactionsFilename(f.Year, f.Month), h.svc.transactionWorkbook(rows, tq))
			return
		}
		h.writeExport(w, format, "gl-detail", glDetailTable(rows))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rows": rows, "tie_cents": sumDetailNatural(rows, q.Get("account_type"))})
}

func (h *Handler) arAging(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	branch := r.URL.Query().Get("branch_id")
	sum, err := h.svc.AgingSummaryFor(r.Context(), branch)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if format := r.URL.Query().Get("format"); format != "" {
		h.svc.recordExport(r.Context(), "AR_AGING", format, identity.StaffUserIDFromContext(r.Context()), branch)
		if isXLSX(format) {
			q := export.StatementQuery{Report: export.ReportARAging, BranchID: branch, BranchName: branchNameParam(r)}
			h.writeXLSX(w, r, export.AgingFilename(h.svc.nowFn()), h.svc.agingWorkbook(sum, q))
			return
		}
		h.writeExport(w, format, "ar-aging", agingTable(sum))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sum)
}

// -- schedules CRUD -----------------------------------------------------------

func (h *Handler) listSchedules(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	list, err := h.svc.ListSchedules(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if list == nil {
		list = []Schedule{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"schedules": list})
}

func (h *Handler) createSchedule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ScheduleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sched, err := h.svc.CreateSchedule(r.Context(), in, identity.StaffUserIDFromContext(r.Context()))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sched)
}

func (h *Handler) updateSchedule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var in ScheduleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sched, err := h.svc.UpdateSchedule(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sched)
}

func (h *Handler) deleteSchedule(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	if err := h.svc.DeleteSchedule(r.Context(), r.PathValue("id")); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -- helpers ------------------------------------------------------------------

func (h *Handler) queryYear(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("year")
	if raw == "" {
		httpx.Error(w, http.StatusBadRequest, "year is required")
		return 0, false
	}
	y, err := strconv.Atoi(raw)
	if err != nil || y < 1900 || y > 9999 {
		httpx.Error(w, http.StatusBadRequest, "year must be a 4-digit fiscal year")
		return 0, false
	}
	return y, true
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// isXLSX reports whether a ?format= value asks for a spreadsheet.
//
// Three spellings, because all three shipped and callers use them. "xls" is kept ACCEPTED but no
// longer HONOURED literally: it now answers with a real .xlsx too. That is the DIB-454 fix, not an
// oversight, the old path answered every one of these with SpreadsheetML 2003 under a .xls name,
// and a caller asking for "xls" was getting the mislabelled file just as surely as one asking for
// "xlsx". There is no code path left that produces the old bytes, so there is nothing for "xls" to
// select.
func isXLSX(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "xls", "xlsx", "excel":
		return true
	}
	return false
}

// writeXLSX encodes a workbook and streams it as a download.
//
// The encode happens BEFORE anything is written, so a failure is a JSON 500 rather than a spreadsheet
// MIME type over a half-written body, a truncated .xlsx is a file the user opens, watches Excel
// refuse to repair, and blames the data for.
func (h *Handler) writeXLSX(w http.ResponseWriter, r *http.Request, filename string, wb *export.Workbook) {
	data, err := wb.Bytes()
	if err != nil {
		h.log.Error("finstmt: xlsx encode failed", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "export failed")
		return
	}
	export.Download(w, filename, data)
}

// branchNameParam reads the optional human branch label the screen already knows, for the sheet's
// subtitle. It is a LABEL ONLY, the scope is the branch_id, which the service authorizes, so a
// caller cannot widen what they can see by lying about the name.
func branchNameParam(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("branch_name"))
}

// writeExport streams an exportTable as CSV. The Excel branch used to live here and produced
// SpreadsheetML 2003 under a .xls filename; DIB-454 moved it onto the shared native-.xlsx exporter
// (see xlsx.go), and the reports now dispatch to writeXLSX before reaching this function.
func (h *Handler) writeExport(w http.ResponseWriter, format, base string, t exportTable) {
	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+base+".csv\"")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(renderCSV(t))
	default:
		httpx.Error(w, http.StatusBadRequest, "format must be csv or xlsx")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// writeErr maps the finstmt error taxonomy to HTTP.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ve validationError
	var ae authzError
	switch {
	case errors.As(err, &ve):
		httpx.Error(w, http.StatusBadRequest, ve.msg)
	// DIB-391: the branch fence answers with its own status (403 for a branch the caller cannot read,
	// 401 for no caller at all) instead of falling through to a 500.
	case errors.As(err, &ae):
		httpx.Error(w, ae.status, ae.msg)
	case errors.Is(err, errNotFound):
		httpx.Error(w, http.StatusNotFound, "not found")
	default:
		h.log.Error("finstmt: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
