package export

import (
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// handler.go, the export module's HTTP surface.
//
// ONE NAMESPACE, /api/v1/exports/*, because three more consumers are queued behind this one
// (DIB-207 statements, DIB-405 audit, DIB-203 transactions) and each of them is a route that answers
// a .xlsx. Hanging them off their owning modules would scatter the same Content-Type, the same
// Content-Disposition and the same "what happens when the read fails mid-file" answer across four
// packages.
//
// AUTHENTICATED, unlike the catalogue reads it is built from. GET /api/v1/products is public (see
// product.RegisterRoutes); this route is not, and the difference is deliberate: a page of the
// catalogue is public data, and a single request that hands back the WHOLE priced catalogue as a
// file is a different thing to leave open. The guard is the house staff middleware, passed in by
// cmd/server exactly as every other module receives it.

// Handler serves the export routes.
//
// The three DIB-454 consumers are OPTIONAL seams rather than constructor arguments, and that is a
// deliberate choice about cmd/server: widening NewHandler would make every call site a compile error
// in a file that is a union conflict site for every lane running this week. Wired, each adds its
// route; unwired, the route is not registered at all, a 404 for a capability the deployment does
// not have, which is the honest answer, and never a 500 from a nil seam reached at request time.
type Handler struct {
	userLink     identity.UserIDLinker
	catalogue    *CatalogueService
	statements   StatementWorkbookReader
	transactions TransactionWorkbookReader
	audit        *AuditService
	log          *slog.Logger
	now          func() time.Time
}

// NewHandler builds the export HTTP surface over the catalogue service.
func NewHandler(catalogue *CatalogueService, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{catalogue: catalogue, log: log, now: time.Now}
}

// WithStatements binds the DIB-207 statement export (*finstmt.Service).
func (h *Handler) WithStatements(r StatementWorkbookReader) *Handler { h.statements = r; return h }

// WithTransactions binds the DIB-203 transaction export (*finstmt.Service).
func (h *Handler) WithTransactions(r TransactionWorkbookReader) *Handler {
	h.transactions = r
	return h
}

// WithAudit binds the DIB-405 audit export.
func (h *Handler) WithAudit(s *AuditService) *Handler { h.audit = s; return h }

// WithClock pins the clock so the filename is testable.
func (h *Handler) WithClock(now func() time.Time) *Handler {
	if now != nil {
		h.now = now
	}
	return h
}

// RegisterRoutes self-registers on the shared mux (platform convention).
//
// Fails closed on a missing guard, mirroring product.RegisterSearchRoute: an export route that
// silently registered unauthenticated would hand the priced catalogue to anyone who guessed the URL,
// and that is not a mistake worth leaving available.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("export.RegisterRoutes: guards are required (the exports are authenticated)")
	}
	r.Handle("GET /api/v1/exports/catalogue.xlsx", g.RequireStaff(h.staff(h.catalogueXLSX)))
	if h.statements != nil {
		// Each export carries exactly the gate of the route it mirrors (statements and transactions
		// are the finstmt reads, RequireManager; the audit trail is GET /api/v1/audit, RequireAdmin),
		// and the handler fences the caller's branch reach on top, see reachBranch.
		r.Handle("GET /api/v1/exports/statement.xlsx", g.RequireManager(h.staff(h.statementXLSX)))
	}
	if h.transactions != nil {
		r.Handle("GET /api/v1/exports/transactions.xlsx", g.RequireManager(h.staff(h.transactionsXLSX)))
	}
	if h.audit != nil {
		r.Handle("GET /api/v1/exports/audit.xlsx", g.RequireAdmin(h.staff(h.auditXLSX)))
	}
}

// statementXLSX answers a financial statement as a native .xlsx, DIB-454 / DIB-207.
//
// The same bytes are also reachable at GET /api/v1/finstmt/<report>?format=xlsx, which is the door
// the statements screen already knocks on. This route exists so every export in the system has one
// namespace and one answer to "what happens when the read fails mid-file", not because the finance
// module needed a second one; both call the SAME builder, so the two doors cannot drift.
func (h *Handler) statementXLSX(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	report, ok := ParseStatementReport(q.Get("report"))
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "unknown report")
		return
	}
	year, err := intParam(q.Get("year"), "year")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	month, err := intParam(q.Get("month"), "month")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if month > 12 {
		httpx.Error(w, http.StatusBadRequest, "month must be 1-12, or absent for the whole year")
		return
	}
	// The trial balance has no branch dimension (org wide by construction), so only a global
	// caller may run it, whatever branch_id says; every other report is fenced on the branch asked.
	reach := strings.TrimSpace(q.Get("branch_id"))
	if report == ReportTrialBalance {
		reach = ""
	}
	if !reachBranch(w, caller, reach) {
		return
	}
	wb, err := h.statements.StatementWorkbook(r.Context(), StatementQuery{
		Report: report, Year: year, Month: month,
		BranchID:   strings.TrimSpace(q.Get("branch_id")),
		BranchName: strings.TrimSpace(q.Get("branch_name")),
	})
	if err != nil {
		h.writeErr(w, r, "statement", err)
		return
	}
	name := StatementFilename(report, year, month)
	if report == ReportARAging {
		name = AgingFilename(h.now())
	}
	h.download(w, r, name, wb)
}

// transactionsXLSX answers the two-sheet transaction export, DIB-454 / DIB-203.
func (h *Handler) transactionsXLSX(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	year, err := intParam(q.Get("year"), "year")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	month, err := intParam(q.Get("month"), "month")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if !reachBranch(w, caller, strings.TrimSpace(q.Get("branch_id"))) {
		return
	}
	wb, err := h.transactions.TransactionWorkbook(r.Context(), TransactionQuery{
		Year: year, Month: month,
		AccountCode: strings.TrimSpace(q.Get("account_code")),
		BranchID:    strings.TrimSpace(q.Get("branch_id")),
		BranchName:  strings.TrimSpace(q.Get("branch_name")),
	})
	if err != nil {
		h.writeErr(w, r, "transactions", err)
		return
	}
	h.download(w, r, TransactionsFilename(year, month), wb)
}

// auditXLSX answers the audit trail as a native .xlsx, DIB-454 / DIB-405.
func (h *Handler) auditXLSX(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	f, err := ParseAuditFilter(r.URL.Query())
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// The audit read names no branch; a branch_id on the request is still held to the caller's reach
	// so no export door answers for a branch the caller cannot read.
	//
	// PRR-437: this check is DEFENSE IN DEPTH and is kept on purpose. The route is gated
	// RequireAdmin, and an admin is tier 1 global scope, always in reach, so as wired the check
	// cannot fire. It is kept because the route, not this function, is what decided that: if the
	// gate is ever relaxed to RequireManager (the statements door's gate), the reach fence is
	// already here rather than remembered. reachBranch is also the shared fence every other export
	// door applies, so removing it here would make the audit door the one door with a different
	// rule to reason about.
	if b := strings.TrimSpace(r.URL.Query().Get("branch_id")); b != "" && !reachBranch(w, caller, b) {
		return
	}
	wb, err := h.audit.Workbook(r.Context(), AuditQuery{Filter: f, FilterSummary: AuditFilterSummary(f)})
	if err != nil {
		h.writeErr(w, r, "audit", err)
		return
	}
	h.download(w, r, AuditFilename(h.now()), wb)
}

// reachBranch is the finstmt branch fence (finstmt.Service.authorizeBranch) applied at the export
// door, so the file is refused before any read runs: a global caller reads any branch and the
// consolidated books; a scoped caller must name a branch they hold a grant on. A refusal is the
// shared 403, never an empty file.
func reachBranch(w http.ResponseWriter, caller guards.StaffCaller, branchID string) bool {
	if branchID == "" {
		if caller.Scope == coreid.ScopeGlobal {
			return true
		}
	} else if caller.CanAccessBranch(branchID) {
		return true
	}
	authz.RefuseForbidden(w)
	return false
}

// download encodes and streams, in ONE place for every export route.
//
// The encode completes before a single header is written, so a failure is a JSON error rather than a
// spreadsheet MIME type over a half-written body, a truncated .xlsx is a file the user opens,
// watches the spreadsheet refuse to repair, and blames the data for.
func (h *Handler) download(w http.ResponseWriter, r *http.Request, filename string, wb *Workbook) {
	data, err := wb.Bytes()
	if err != nil {
		h.log.Error("export: encode failed", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "export failed")
		return
	}
	Download(w, filename, data)
}

// writeErr maps a consumer error: client input is a 400 with its own message, everything else is a
// logged 500 with a generic one.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	if IsBadRequest(err) {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	h.log.Error("export: "+what+" workbook failed", "path", r.URL.Path, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "export failed")
}

// intParam reads a non-negative whole-number query parameter. Absent is 0 (meaning "not narrowed");
// malformed is REFUSED rather than silently read as 0, which would answer a different period than
// the one asked for and look like success.
func intParam(raw, name string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := atoiStrict(raw)
	if err != nil {
		return 0, errBadRequest(name + " must be a whole number")
	}
	return n, nil
}

// catalogueXLSX answers the catalogue export.
//
// It takes the SAME query parameters as GET /api/v1/products, plus sort/dir, so the component can
// hand it exactly what it is showing and the file cannot disagree with the screen.
//
// An EMPTY result is a 200 with a real workbook, headers, title, zero rows. "No products matched"
// is an answer, and a download that silently does nothing is indistinguishable from a broken button.
func (h *Handler) catalogueXLSX(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := CatalogueQuery{
		Filter:        parseCatalogueFilter(r),
		Sort:          ParseSortKey(r.URL.Query().Get("sort")),
		Desc:          strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("dir")), "desc"),
		FilterSummary: strings.TrimSpace(r.URL.Query().Get("filter_summary")),
	}
	// A caller-supplied string lands in a cell, so cap it: a megabyte of "filter summary" is not a
	// provenance line, it is a payload.
	if len(q.FilterSummary) > 300 {
		q.FilterSummary = q.FilterSummary[:300]
	}
	wb, err := h.catalogue.Workbook(r.Context(), q)
	if err != nil {
		h.log.Error("export: catalogue workbook failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "export failed")
		return
	}
	data, err := wb.Bytes()
	if err != nil {
		h.log.Error("export: catalogue encode failed", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "export failed")
		return
	}
	Download(w, CatalogueFilename(h.now()), data)
}

// parseCatalogueFilter reads the catalogue's query parameters into product.Filter, the same names,
// in the same spellings, that GET /api/v1/products accepts (product.parseFilter is unexported, so
// this mirrors it; the shared contract is the WIRE, and the catalogue component builds one query
// string for both calls).
//
// active defaults to TRUE here, where the products endpoint defaults it to false. The catalogue page
// always asks with active=true, and an export that quietly included discontinued products would be a
// file someone quotes from.
func parseCatalogueFilter(r *http.Request) product.Filter {
	q := r.URL.Query()
	get := func(k string) string { return strings.TrimSpace(q.Get(k)) }
	activeOnly := true
	if raw := get("active"); raw == "false" {
		activeOnly = false
	}
	return product.Filter{
		Finish:       get("finish"),
		ColorFamily:  get("color_family"),
		Application:  get("application"),
		Color:        get("color"),
		Manufacturer: get("manufacturer"),
		Collection:   get("collection"),
		CategoryID:   get("category_id"),
		Q:            get("q"),
		ActiveOnly:   activeOnly,
		InStockOnly:  get("in_stock_only") == "true",
		BranchID:     get("branch_id"),
	}
}
