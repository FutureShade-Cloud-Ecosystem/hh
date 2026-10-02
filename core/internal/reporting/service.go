package reporting

// service.go, the rules layer: who may read what, what a run means, and what gets persisted.
//
// -- THE BRANCH FENCE IS WRITTEN ONCE -----------------------------------------------------------------
// authorizeBranch below is copied in SHAPE, deliberately, from internal/finstmt (DIB-391): a scoped
// caller must name a branch they can reach; only a GLOBAL caller may run a report with no branch
// dimension at all. Reports are finance information and the consolidated view is the whole company's
// numbers, so a Kingston manager gets Kingston. Writing the rule in one function means a new surface
// that forgets it is a MISSING CALL, visible in review, rather than a subtle leak.
//
// A subtlety worth stating: three templates (sales_by_branch, ar_aging_*, po_status …) have an optional
// or absent branch parameter. Omitting the parameter is exactly "consolidated", so it goes through the
// same fence with an empty branchID and a scoped caller is refused. That is stricter than BisTrack,
// where anyone who could open a report could open all of it.
//
// -- A RUN IS NOT AN EVENT. A PRINT IS ----------------------------------------------------------------
// Running a report on screen writes nothing. Printing one writes a report_runs artefact carrying the
// rendered HTML, the parameters and the totals, plus its print_queue row, because a printed report is
// a claim somebody carried into a meeting, and it must keep saying what it said. Every browse would
// otherwise fill the table with a log of clicks and lose the artefacts in it.

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// Service is reporting's rules layer.
type Service struct {
	repo  *Repository
	log   *slog.Logger
	audit audit.Recorder
	reps  RepAssignmentReader
	// callerFrom reads the guard-stamped staff caller. Injectable so unit tests can pose as a scoped or
	// global caller with no HTTP round trip (the finstmt/landedcost/gl idiom).
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewService wires the repository.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, log: slog.Default(), callerFrom: identity.StaffContextFromContext}
}

// WithLogger swaps the logger.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	if l != nil {
		s.log = l
	}
	return s
}

// WithAudit injects the A-21 Recorder. Exports and prints of finance data are SENSITIVE ACCESS: the
// question "who took the margin report out of the building" has to have an answer.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// WithRepAssignments binds the W5-D read seam (see seams.go). Optional: unbound removes a conformance
// CHECK, never a feature.
func (s *Service) WithRepAssignments(r RepAssignmentReader) *Service { s.reps = r; return s }

// RepAssignments exposes the bound seam (nil when unwired) for the conformance test.
func (s *Service) RepAssignments() RepAssignmentReader { return s.reps }

// withCaller swaps the caller resolver (tests only).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// -- authorization ------------------------------------------------------------

func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, ErrUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, ErrUnauthorized
	}
	return c, nil
}

// authorizeBranch applies the fence. "" means consolidated and is GLOBAL-only.
func (s *Service) authorizeBranch(ctx context.Context, branchID string) (identity.StaffContext, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, err
	}
	if branchID == "" {
		if c.Scope != identity.ScopeGlobal {
			return identity.StaffContext{}, ErrForbidden
		}
		return c, nil
	}
	if !c.CanAccessBranch(branchID) {
		return identity.StaffContext{}, ErrForbidden
	}
	return c, nil
}

// branchReach is a caller's branch authority projected onto a LIST read: every branch for a global
// caller, the granted branches for a scoped one. reporting's own copy of the shape purchasing,
// inventory and payments each keep, by the codebase's zero-trust convention: a module must not be able
// to accept another module's authority object by accident.
type branchReach struct {
	global    bool
	branchIDs []string
}

// filter renders the reach for the ($n::uuid[] IS NULL OR branch_id = ANY($n::uuid[])) predicate a
// fenced list read uses. nil means SQL NULL, the predicate short-circuits true, and a global caller
// keeps the whole list. A scoped caller with no grants binds an EMPTY array, and = ANY('{}') is false
// for every row, so the fence fails closed rather than open.
//
// The nil normalisation is load-bearing for the ZERO value as well as the built reach: pgx binds a
// nil slice as SQL NULL, which is the global arm, so a branchReach that reached a query uninitialised
// would read every row in every branch. Empty is the honest binding for no branches, and it is what
// the zero value means.
func (r branchReach) filter() any {
	if r.global {
		return nil
	}
	if r.branchIDs == nil {
		return []string{}
	}
	return r.branchIDs
}

// reachOf projects the caller onto a list read. Unlike authorizeBranch it refuses nothing: a read has
// a truthful answer for every caller, which is the rows inside their reach, and an empty list is that
// answer for a scoped caller with no grants.
func (s *Service) reachOf(ctx context.Context) (branchReach, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return branchReach{}, err
	}
	if c.Scope == identity.ScopeGlobal {
		return branchReach{global: true}, nil
	}
	ids := make([]string, 0, len(c.Branches))
	for id, granted := range c.Branches {
		if granted {
			ids = append(ids, id)
		}
	}
	// Sorted so the SQL parameter, and therefore the query plan, does not depend on Go's randomised
	// map iteration order.
	sort.Strings(ids)
	return branchReach{branchIDs: ids}, nil
}

// canReadBranch answers whether this caller may read ONE artefact scoped to branchID. An empty branch
// is a consolidated artefact, the whole company's numbers, and answers to a GLOBAL caller alone, the
// same ruling authorizeBranch makes for a run. A caller outside the branch gets NOT FOUND rather than
// forbidden: a 403 confirms the artefact exists, and whether it exists is branch information too.
func canReadBranch(c identity.StaffContext, branchID string) bool {
	if branchID == "" {
		return c.Scope == identity.ScopeGlobal
	}
	return c.CanAccessBranch(branchID)
}

// -- catalogue ----------------------------------------------------------------

// Categories returns the runnable catalogue, grouped, in nav order. Any authenticated staff member may
// SEE the catalogue; the fence applies when a report is RUN. Hiding the list from a scoped user would
// mean they cannot discover a report they are entitled to run for their own branch.
func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	templates, err := s.repo.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	byCode := map[string][]Template{}
	var order []string
	for _, t := range templates {
		if _, seen := byCode[t.Category]; !seen {
			order = append(order, t.Category)
		}
		byCode[t.Category] = append(byCode[t.Category], t)
	}
	sort.Strings(order)
	out := make([]Category, 0, len(order))
	for _, code := range order {
		label := CategoryLabels[code]
		if label == "" {
			label = code
		}
		out = append(out, Category{Code: code, Label: label, Templates: byCode[code]})
	}
	return out, nil
}

// Template reads one definition (params + columns, never the SQL).
func (s *Service) Template(ctx context.Context, code string) (Template, error) {
	if _, err := s.caller(ctx); err != nil {
		return Template{}, err
	}
	return s.repo.GetTemplate(ctx, code)
}

// -- running ------------------------------------------------------------------

// Run validates the parameters, applies the branch fence, executes the ONE compiled query for this
// code, and totals the rows it returns. Nothing is persisted.
func (s *Service) Run(ctx context.Context, code string, in map[string]string) (Result, error) {
	t, err := s.repo.GetTemplate(ctx, code)
	if err != nil {
		return Result{}, err
	}
	args, norm, err := bindParams(t.Params, in)
	if err != nil {
		return Result{}, err
	}
	if _, err := s.authorizeBranch(ctx, paramBranchID(norm)); err != nil {
		return Result{}, err
	}
	rows, err := runTemplate(ctx, s.repo.pool, t, args)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Code:     t.Code,
		Name:     t.Name,
		Category: t.Category,
		Columns:  t.Columns,
		Rows:     rows,
		RowCount: len(rows),
		Totals:   totalsFor(t.Columns, rows),
		Params:   norm,
		RunAt:    nowUTC(),
	}, nil
}

// Export runs a report and renders it as CSV. Recorded as sensitive access when the Recorder is bound:
// an export leaves the building, and unlike a screen it can be forwarded.
func (s *Service) Export(ctx context.Context, code string, in map[string]string) (Result, []byte, error) {
	res, err := s.Run(ctx, code, in)
	if err != nil {
		return Result{}, nil, err
	}
	s.recordAccess(ctx, ActReportExport, res.Code, "", res.Params)
	return res, renderCSV(res), nil
}

// Print runs a report, freezes it as a report_runs artefact with its rendered HTML, and enqueues the
// print_queue row, one transaction, in the repository.
//
// branchID resolution, and why it is not simply the report's parameter: a consolidated report has no
// branch parameter at all, but print_queue.branch_id is NOT NULL because paper comes out of a printer
// that lives somewhere. So the branch printed AT is: the report's own branch parameter if it has one,
// else the caller's single granted branch, else the explicit printBranchID the caller supplied. A
// GLOBAL caller with no branch named is refused with a message that says what to do, rather than
// having a branch picked for them.
func (s *Service) Print(ctx context.Context, code string, in map[string]string, printBranchID string) (Run, error) {
	res, err := s.Run(ctx, code, in)
	if err != nil {
		return Run{}, err
	}
	c, err := s.caller(ctx)
	if err != nil {
		return Run{}, err
	}
	branch := s.resolvePrintBranch(c, paramBranchID(res.Params), printBranchID)
	if branch == "" {
		return Run{}, errBadRequest("this report covers every branch, name the branch to print it at")
	}
	if !c.CanAccessBranch(branch) {
		return Run{}, ErrForbidden
	}
	run, err := s.repo.InsertRunWithPrintJob(ctx, Run{
		Kind:         RunKindTemplate,
		TemplateCode: res.Code,
		Params:       res.Params,
		BranchID:     branch,
		RowCount:     res.RowCount,
		Totals:       res.Totals,
		HTML:         renderHTML(res),
		RunBy:        c.UserID,
	})
	if err != nil {
		return Run{}, err
	}
	run.Name = res.Name
	// The artefact id, not the code: this print IS a row, and the audit trail should point at it.
	s.recordAccess(ctx, ActReportPrint, res.Code, run.ID, res.Params)
	return run, nil
}

// resolvePrintBranch is the rule above, in one place so the commission print path uses the same one.
func (s *Service) resolvePrintBranch(c identity.StaffContext, paramBranch, explicit string) string {
	if paramBranch != "" {
		return paramBranch
	}
	if explicit != "" {
		return explicit
	}
	if c.Scope != identity.ScopeGlobal && len(c.Branches) == 1 {
		for id := range c.Branches {
			return id
		}
	}
	return ""
}

// Runs lists recent artefacts, held to the caller's branch reach. A global caller keeps the whole
// list; a scoped caller sees their branches' artefacts alone, and a consolidated artefact (no branch)
// never appears for them because its numbers are the whole company's.
func (s *Service) Runs(ctx context.Context, limit int) ([]Run, error) {
	reach, err := s.reachOf(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListRuns(ctx, limit, reach)
}

// RunArtefact reads one persisted artefact by id, behind the branch fence. The fence is on the DATA
// the artefact carries (artefactScopes below), never on run.BranchID, which is only where the paper
// came out: print_queue.branch_id is NOT NULL, so even a consolidated report is printed AT a branch,
// and fencing on the printer would hand a scoped manager the whole company's numbers. A refusal is
// NOT FOUND rather than forbidden, so the id itself does not confirm the artefact exists.
func (s *Service) RunArtefact(ctx context.Context, id string) (Run, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return Run{}, err
	}
	run, err := s.repo.GetRun(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if c.Scope == identity.ScopeGlobal {
		return run, nil // a global caller reads everything, the scope need not be derived
	}
	scopes, err := s.artefactScopes(ctx, run)
	if err != nil {
		return Run{}, err
	}
	if !artefactReadable(c, scopes) {
		return Run{}, ErrNotFound
	}
	return run, nil
}

// artefactScopes returns the BRANCH SCOPE of the data an artefact carries, as a set the reader must
// be inside entirely. It is deliberately not run.BranchID (the print branch, see RunArtefact):
//
//	TEMPLATE    the report's own branch parameter, params.branch_id. Empty or absent is the
//	            consolidated report, the whole company's numbers.
//	COMMISSION  the artefact row names no period id; params carries the (rep, window) linkage its
//	            print rendered and nothing else, so the scope is the branches of every frozen period
//	            that linkage matches. The same window can be frozen more than once (branch scoped and
//	            consolidated), the row cannot say which one it rendered, and the reader has to be
//	            inside all of them. No match at all (the periods are gone) is the genuinely
//	            underivable case and reads as consolidated: global only, fail closed.
func (s *Service) artefactScopes(ctx context.Context, run Run) ([]string, error) {
	if run.Kind != RunKindCommission {
		return []string{run.Params["branch_id"]}, nil
	}
	return s.repo.CommissionArtefactScopes(ctx,
		run.Params["rep_user_id"], run.Params["period_start"], run.Params["period_end"])
}

// artefactReadable answers the fence for one artefact against its scope set: every branch of the
// data must be readable, and the empty set (underivable) reads as consolidated, global only.
func artefactReadable(c identity.StaffContext, scopes []string) bool {
	if len(scopes) == 0 {
		scopes = []string{""}
	}
	for _, b := range scopes {
		if !canReadBranch(c, b) {
			return false
		}
	}
	return true
}

// -- saved views --------------------------------------------------------------

// SavedViews lists the caller's own plus shared views.
func (s *Service) SavedViews(ctx context.Context, templateCode string) ([]SavedView, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListSavedViews(ctx, c.UserID, templateCode)
}

// SaveView stores a parameter set. The parameters are validated HERE too, not because the run path
// will not validate them again (it will), but so a view that could never run is refused at save time
// rather than becoming a button that always errors.
func (s *Service) SaveView(ctx context.Context, templateCode, name string, params map[string]string, shared bool) (SavedView, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return SavedView{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return SavedView{}, errBadRequest("a saved view needs a name")
	}
	t, err := s.repo.GetTemplate(ctx, templateCode)
	if err != nil {
		return SavedView{}, err
	}
	if _, norm, err := bindParams(t.Params, params); err != nil {
		return SavedView{}, err
	} else {
		params = norm
	}
	return s.repo.UpsertSavedView(ctx, SavedView{
		TemplateCode: templateCode, OwnerUserID: c.UserID, Name: name, Params: params, IsShared: shared,
	})
}

// DeleteView removes one of the caller's own views.
func (s *Service) DeleteView(ctx context.Context, id string) error {
	c, err := s.caller(ctx)
	if err != nil {
		return err
	}
	return s.repo.DeleteSavedView(ctx, id, c.UserID)
}

// -- audit --------------------------------------------------------------------

// Audit actions this module records. They are NOT in audit's sensitiveActions map, widening that map
// is internal/audit's fence, so it is a PARK for the union (the exact two-line diff is in
// NOTES-W6-B.md). The rows are written and readable in the viewer either way; what the park costs is
// the viewer's sensitive BADGE, not the record.
const (
	ActReportExport = "REPORT_EXPORT"
	ActReportPrint  = "REPORT_PRINT"
)

// recordAccess stamps a sensitive-access row when the Recorder is bound. A failure to record is LOGGED,
// not propagated: refusing to hand a manager their margin report because the audit table is busy would
// be the wrong trade, and the access still happened either way.
//
// WHAT GOES IN WHICH FIELD, AND WHY. Two of these were learned by watching the write FAIL rather than by
// reading the schema, which is the honest way to have found them:
//
//	EntityID   audit_log.entity_id is a UUID COLUMN. Putting a template CODE there (the obvious first
//	           instinct, it is the thing being accessed) makes every insert fail with SQLSTATE 22P02,
//	           and because this path deliberately swallows errors, it fails SILENTLY: the report is
//	           served, a warning goes to the log, and the audit trail nobody is watching is simply
//	           empty. So a uuid goes here or nothing does, on the PRINT path that is the report_runs
//	           id, which is a better answer anyway because it points at the artefact.
//	Purpose    carries the template code. Free text, allowlisted, and it survives the boundary.
//	BranchID   a first-class column, so a branch uuid is not run through the redactor.
//	Reason     ONLY the date parameters. audit's boundary redacts any value containing a run of 7+
//	           digits, which a joined parameter string containing a uuid trips, turning the whole
//	           reason into "[redacted]" and taking the date range with it.
//
// The result is a row that says who exported which report, for which branch, over which period, with
// every part of it intact.
func (s *Service) recordAccess(ctx context.Context, action, code, entityID string, params map[string]string) {
	if s.audit == nil {
		return
	}
	dates := make([]string, 0, 2)
	for k, v := range params {
		if v != "" && strings.HasSuffix(k, "_date") {
			dates = append(dates, k+"="+v)
		}
	}
	sort.Strings(dates)
	entityType := "report_template"
	if entityID != "" {
		entityType = "report_run"
	}
	if err := s.audit.Record(ctx, audit.SensitiveAccess{
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID, // a UUID or nothing, never a code
		ActorID:    identity.StaffUserIDFromContext(ctx),
		BranchID:   params["branch_id"],
		Purpose:    code,
		Reason:     strings.Join(dates, " "),
	}); err != nil {
		s.log.Warn("reporting: sensitive-access record failed", "action", action, "code", code, "err", err)
	}
}
