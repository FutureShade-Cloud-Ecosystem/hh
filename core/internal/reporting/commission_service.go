package reporting

// commission_service.go, the rules layer for DIB-96: the branch fence, the compute-and-freeze path,
// the drill-through, the accrual view, and the print/export parity with the template reports.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CommissionRules lists rules (all reps, or one), held to the caller's reach. A rule is readable when
// it is in force at a branch the caller can reach, which by chooseRule's own ruling means its branch
// is empty (in force everywhere) or names a granted branch: a Trenton rate is Trenton's pay decision,
// while a company-wide rate governs the caller's own branch and is part of the arithmetic their own
// fenced preview performs.
func (s *Service) CommissionRules(ctx context.Context, repUserID string) ([]CommissionRule, error) {
	reach, err := s.reachOf(ctx)
	if err != nil {
		return nil, err
	}
	if repUserID != "" {
		if _, err := uuid.Parse(repUserID); err != nil {
			return nil, errBadRequest("rep_user_id must be a uuid")
		}
	}
	return s.repo.ListCommissionRules(ctx, repUserID, reach)
}

// CreateCommissionRule validates and writes a rule. Validation is here and not only at the substrate
// because a CHECK violation surfaces as a 500 with a constraint name; a person setting up a pay plan
// deserves a sentence.
func (s *Service) CreateCommissionRule(ctx context.Context, in CommissionRule) (CommissionRule, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return CommissionRule{}, err
	}
	if _, err := uuid.Parse(in.RepUserID); err != nil {
		return CommissionRule{}, errBadRequest("rep_user_id must be a uuid")
	}
	if in.Basis != BasisRevenue && in.Basis != BasisMargin {
		return CommissionRule{}, errBadRequest("basis must be REVENUE or MARGIN")
	}
	if in.RateBps < 0 || in.RateBps > 10000 {
		return CommissionRule{}, errBadRequest("rate_bps must be between 0 and 10000 (0-100%%)")
	}
	if _, err := time.Parse(DateLayout, in.EffectiveFrom); err != nil {
		return CommissionRule{}, errBadRequest("effective_from must be a date (YYYY-MM-DD)")
	}
	if in.EffectiveTo != "" {
		if _, err := time.Parse(DateLayout, in.EffectiveTo); err != nil {
			return CommissionRule{}, errBadRequest("effective_to must be a date (YYYY-MM-DD)")
		}
	}
	for name, v := range map[string]string{"category_id": in.CategoryID, "branch_id": in.BranchID} {
		if v == "" {
			continue
		}
		if _, err := uuid.Parse(v); err != nil {
			return CommissionRule{}, errBadRequest("%s must be a uuid", name)
		}
	}
	// A branch-scoped rule may only be written by someone who can reach that branch. A rule with no
	// branch is company-wide and therefore GLOBAL-only, by the same fence every consolidated read uses.
	if _, err := s.authorizeBranch(ctx, in.BranchID); err != nil {
		return CommissionRule{}, err
	}
	return s.repo.InsertCommissionRule(ctx, in, c.UserID)
}

// RetireCommissionRule deactivates a rule (never deletes it, see the repository).
func (s *Service) RetireCommissionRule(ctx context.Context, id string) error {
	if _, err := s.caller(ctx); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	return s.repo.RetireCommissionRule(ctx, id)
}

// -- the period ---------------------------------------------------------------

// CommissionPreview computes a period WITHOUT freezing it. This is the screen a rep and a manager argue
// over before anybody commits a number, and it deliberately writes nothing: a preview that left rows
// behind would make "how many times was this run" a question about the report rather than the pay.
func (s *Service) CommissionPreview(ctx context.Context, repUserID, from, to, branchID string) ([]CommissionPeriod, error) {
	fromT, toT, err := parseWindow(from, to)
	if err != nil {
		return nil, err
	}
	if _, err := s.authorizeBranch(ctx, branchID); err != nil {
		return nil, err
	}
	if repUserID != "" {
		if _, err := uuid.Parse(repUserID); err != nil {
			return nil, errBadRequest("rep_user_id must be a uuid")
		}
	}
	lines, err := s.repo.AttributedLines(ctx, fromT, toT, branchID)
	if err != nil {
		return nil, err
	}
	reps, err := s.repo.RepsWithSales(ctx, fromT, toT, branchID)
	if err != nil {
		return nil, err
	}
	// The rules are read inside the caller's reach too. ChooseRule can only ever pick a rule whose
	// branch is empty or matches the line's branch, and the lines above are already fenced to
	// branchID, so for a scoped caller this changes nothing about which rule wins; it only stops the
	// unreachable rules riding along in memory.
	reach, err := s.reachOf(ctx)
	if err != nil {
		return nil, err
	}
	rules, err := s.repo.ListCommissionRules(ctx, repUserID, reach)
	if err != nil {
		return nil, err
	}
	rulesByRep := map[string][]CommissionRule{}
	for _, r := range rules {
		rulesByRep[r.RepUserID] = append(rulesByRep[r.RepUserID], r)
	}

	ids := make([]string, 0, len(reps))
	for id := range reps {
		if repUserID != "" && id != repUserID {
			continue
		}
		ids = append(ids, id)
	}
	// Stable output order: by display name, so two runs of the same period render identically.
	sortByName(ids, reps)

	out := make([]CommissionPeriod, 0, len(ids))
	now := nowUTC()
	for _, id := range ids {
		p := computePeriod(id, reps[id], from, to, branchID, lines, rulesByRep[id], now)
		out = append(out, p)
	}
	return out, nil
}

// ComputeAndFreeze computes ONE rep's period and writes it, with its drill-through, as an artefact.
// Running it twice for the same rep and period writes a SECOND run rather than replacing the first:
// re-running after a correction is a legitimate act and the previous answer is evidence of what was
// said before. The surface shows the latest and the accrual register sums both, which is why a
// re-computation is an operator decision rather than a silent idempotent write.
func (s *Service) ComputeAndFreeze(ctx context.Context, repUserID, from, to, branchID string) (CommissionPeriod, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return CommissionPeriod{}, err
	}
	if repUserID == "" {
		return CommissionPeriod{}, errBadRequest("rep_user_id is required to compute a commission period")
	}
	periods, err := s.CommissionPreview(ctx, repUserID, from, to, branchID)
	if err != nil {
		return CommissionPeriod{}, err
	}
	if len(periods) == 0 {
		return CommissionPeriod{}, ErrNotFound
	}
	return s.repo.InsertCommissionRun(ctx, periods[0], c.UserID)
}

// CommissionRuns lists frozen periods, held to the caller's branch reach. A global caller keeps the
// whole list; a scoped caller sees their branches' periods alone, and a consolidated period (no
// branch) never appears for them because it is the whole company's pay.
func (s *Service) CommissionRuns(ctx context.Context, from, to, repUserID string) ([]CommissionPeriod, error) {
	reach, err := s.reachOf(ctx)
	if err != nil {
		return nil, err
	}
	fromT, toT, err := parseWindow(from, to)
	if err != nil {
		return nil, err
	}
	return s.repo.ListCommissionRuns(ctx, fromT, toT, repUserID, reach)
}

// CommissionRun reads one frozen period with its drill-through, behind the branch fence: the caller
// must be able to read the period's branch, and a period with no branch (consolidated) answers to a
// global caller alone. A refusal is NOT FOUND, for the same reason RunArtefact's is: the id itself
// must not confirm the period exists at a branch the caller cannot reach.
func (s *Service) CommissionRun(ctx context.Context, id string) (CommissionPeriod, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return CommissionPeriod{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return CommissionPeriod{}, ErrNotFound
	}
	p, err := s.repo.GetCommissionRun(ctx, id)
	if err != nil {
		return CommissionPeriod{}, err
	}
	if !canReadBranch(c, p.BranchID) {
		return CommissionPeriod{}, ErrNotFound
	}
	return p, nil
}

// CommissionAccruals is the accrual view. It answers "what is owed and unpaid" and says, in the payload
// itself, that none of it is in the general ledger. The register is grouped by REP, but the money
// underneath is branch scoped, so the fence goes on the runs being summed: a scoped caller's register
// owes their branches' periods alone.
func (s *Service) CommissionAccruals(ctx context.Context, from, to string) (Accrual, error) {
	reach, err := s.reachOf(ctx)
	if err != nil {
		return Accrual{}, err
	}
	fromT, toT, err := parseWindow(from, to)
	if err != nil {
		return Accrual{}, err
	}
	lines, err := s.repo.AccrualRegister(ctx, fromT, toT, reach)
	if err != nil {
		return Accrual{}, err
	}
	// An EMPTY register must serialise as [] and not null. A nil Go slice becomes JSON null, and every
	// consumer then has to remember to guard it, which one of them will not, and the failure is a
	// TypeError that blanks the whole screen rather than showing an empty table. Normalising here means
	// "no accruals" and "some accruals" have the same shape on the wire.
	if lines == nil {
		lines = []AccrualLine{}
	}
	acc := Accrual{From: from, To: to, Lines: lines, PostsToGL: false, Note: AccrualNote}
	for _, l := range lines {
		acc.TotalCents += l.CommissionCents
	}
	return acc, nil
}

// -- print / export parity ----------------------------------------------------

// commissionResult projects a frozen period into the SAME Result shape a template report produces, so
// the CSV writer and the HTML renderer are literally the same code. That is the parity requirement: a
// commission statement that exported through its own private formatter would drift from every other
// report the first time one of them changed.
func commissionResult(p CommissionPeriod, runAt time.Time) Result {
	cols := []Column{
		col("invoice_no", "Invoice", KindText, false),
		col("invoice_date", "Date", KindDate, false),
		col("customer_name", "Customer", KindText, false),
		col("product_sku", "SKU", KindText, false),
		col("category_code", "Category", KindText, false),
		col("qty_base", "Quantity", KindQty, true),
		col("revenue_cents", "Revenue", KindCents, true),
		col("cost_cents", "Cost", KindCents, true),
		col("margin_cents", "Margin", KindCents, true),
		col("basis", "Basis", KindText, false),
		col("basis_cents", "Basis amount", KindCents, true),
		col("rate_bps", "Rate bps", KindInt, false),
		col("commission_cents", "Commission", KindCents, true),
	}
	rows := make([][]any, 0, len(p.Lines))
	for _, l := range p.Lines {
		rows = append(rows, []any{
			l.InvoiceNo, l.InvoiceDate, l.CustomerName, l.ProductSKU, l.CategoryCode,
			l.QtyBase, l.RevenueCents, l.CostCents, l.MarginCentsV,
			l.Basis, l.BasisCents, int64(l.RateBps), l.CommissionCents,
		})
	}
	return Result{
		Code:     "commission_period",
		Name:     "Commission, " + p.RepName + " (" + p.PeriodStart + " to " + p.PeriodEnd + ")",
		Category: "COMMISSION",
		Columns:  cols,
		Rows:     rows,
		RowCount: len(rows),
		Totals:   totalsFor(cols, rows),
		Params: map[string]string{
			"rep_user_id": p.RepUserID, "period_start": p.PeriodStart, "period_end": p.PeriodEnd,
		},
		RunAt: runAt,
	}
}

// ExportCommissionRun renders a frozen period as CSV.
func (s *Service) ExportCommissionRun(ctx context.Context, id string) (CommissionPeriod, []byte, error) {
	p, err := s.CommissionRun(ctx, id)
	if err != nil {
		return CommissionPeriod{}, nil, err
	}
	s.recordAccess(ctx, ActReportExport, "commission_period", p.RunID, map[string]string{})
	return p, renderCSV(commissionResult(p, nowUTC())), nil
}

// PrintCommissionRun freezes the period's rendering as a report_runs artefact of kind COMMISSION and
// enqueues its print_queue row, the same substrate, the same doc type, the same transaction as a
// template report. The accrual caveat is appended to the printed document, because a printout is the
// thing most likely to be read by someone who never saw the screen.
func (s *Service) PrintCommissionRun(ctx context.Context, id, printBranchID string) (Run, error) {
	p, err := s.CommissionRun(ctx, id)
	if err != nil {
		return Run{}, err
	}
	c, err := s.caller(ctx)
	if err != nil {
		return Run{}, err
	}
	branch := s.resolvePrintBranch(c, p.BranchID, printBranchID)
	if branch == "" {
		return Run{}, errBadRequest("this commission period covers every branch, name the branch to print it at")
	}
	if !c.CanAccessBranch(branch) {
		return Run{}, ErrForbidden
	}
	res := commissionResult(p, nowUTC())
	body := renderHTML(res)
	body = strings.Replace(body, "</table>",
		`</table><p style="margin-top:14px;font-size:11px;color:#444">`+AccrualNote+`</p>`, 1)

	// The artefact is STAMPED with its period's branch at print time. params is JSONB, so this is a
	// row-shape change with no migration, and it is what makes the artefact's scope its OWN: the
	// (rep, window) linkage in params can only say "some period of this rep and window", so once
	// another branch freezes the same window, an ordinary month-end for a rep who sells at two
	// branches, the linkage could mean either period and an honest reader's own statement would fall
	// out of their reach. A stamped artefact answers its stamp; rows printed before the stamp exists
	// keep the linkage derivation, with its ambiguity, as the fallback. Stamped AFTER renderHTML on
	// purpose: the printed document is unchanged, the stamp is fence metadata, not copy.
	res.Params["branch_id"] = p.BranchID

	run, err := s.repo.InsertRunWithPrintJob(ctx, Run{
		Kind:         RunKindCommission,
		TemplateCode: "commission_period",
		Params:       res.Params,
		BranchID:     branch,
		RowCount:     res.RowCount,
		Totals:       res.Totals,
		HTML:         body,
		RunBy:        c.UserID,
	})
	if err != nil {
		return Run{}, err
	}
	run.Name = res.Name
	s.recordAccess(ctx, ActReportPrint, "commission_period", run.ID, map[string]string{"branch_id": branch})
	return run, nil
}

// -- helpers ------------------------------------------------------------------

// parseWindow validates a from/to date pair. Both required: a commission period with an open end is a
// period that changes every time it is opened.
func parseWindow(from, to string) (time.Time, time.Time, error) {
	f, err := time.Parse(DateLayout, from)
	if err != nil {
		return time.Time{}, time.Time{}, errBadRequest("from must be a date (YYYY-MM-DD)")
	}
	t, err := time.Parse(DateLayout, to)
	if err != nil {
		return time.Time{}, time.Time{}, errBadRequest("to must be a date (YYYY-MM-DD)")
	}
	if t.Before(f) {
		return time.Time{}, time.Time{}, errBadRequest("to must not be before from")
	}
	return f, t, nil
}

// sortByName orders ids by their display name (insertion sort, the list is a sales team, not a corpus).
func sortByName(ids []string, names map[string]string) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && names[ids[j]] < names[ids[j-1]]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// -- attribution conformance (the RepAssignmentReader seam's production use) ---

// AttributionMismatch is ONE sale where this module's bulk SQL attribution and the owner module's own
// resolver disagree about which rep owns it.
type AttributionMismatch struct {
	InvoiceID       string `json:"invoice_id"`
	InvoiceNo       string `json:"invoice_no"`
	CustomerID      string `json:"customer_id"`
	OrderID         string `json:"order_id,omitempty"`
	ReportRepUserID string `json:"report_rep_user_id"`
	SeamRepUserID   string `json:"seam_rep_user_id"`
	SeamLevel       string `json:"seam_level"`
}

// AttributionCheckResult is the operator-facing answer.
type AttributionCheckResult struct {
	Bound       bool                  `json:"bound"`
	Checked     int                   `json:"checked"`
	Mismatches  []AttributionMismatch `json:"mismatches"`
	Explanation string                `json:"explanation"`
}

// attributionUnbound is what the check says when the seam is not wired.
const attributionUnbound = "The rep-assignment resolver is not wired in this deployment, so this check " +
	"cannot run. Commission attribution still works, it reads the rep_assignments table directly, but " +
	"it is not being cross-checked against the owning module's own resolution order."

// AttributionCheck answers a question a sales manager actually asks before a commission run is
// believed: "does this report agree with who the system says owns each account?"
//
// It exists because commission attribution is done in BULK SQL (a COALESCE join implementing the W5-D
// order-override-then-customer-level ruling) while the AUTHORITY for that ruling lives in the messaging
// module. Two implementations of one rule will drift eventually, and the way it would show up in the
// world is a rep quietly losing commission on split-book orders for a quarter before anyone notices.
// This turns that silent drift into a number on a screen.
//
// Unbound seam → Bound:false and an explanation, not an error. The check is a cross-check; its absence
// removes a safety net, not a feature.
func (s *Service) AttributionCheck(ctx context.Context, from, to, branchID string) (AttributionCheckResult, error) {
	fromT, toT, err := parseWindow(from, to)
	if err != nil {
		return AttributionCheckResult{}, err
	}
	if _, err := s.authorizeBranch(ctx, branchID); err != nil {
		return AttributionCheckResult{}, err
	}
	if s.reps == nil {
		return AttributionCheckResult{Bound: false, Explanation: attributionUnbound}, nil
	}
	lines, err := s.repo.AttributedLines(ctx, fromT, toT, branchID)
	if err != nil {
		return AttributionCheckResult{}, err
	}
	// One check per INVOICE, not per line: every line of an invoice shares its customer and order, so
	// asking per line would multiply the seam calls by the basket size for no extra information.
	seen := map[string]bool{}
	out := AttributionCheckResult{Bound: true, Mismatches: []AttributionMismatch{}}
	for _, l := range lines {
		if seen[l.InvoiceID] {
			continue
		}
		seen[l.InvoiceID] = true
		out.Checked++

		customerID, orderID, err := s.repo.InvoiceParties(ctx, l.InvoiceID)
		if err != nil {
			return AttributionCheckResult{}, err
		}
		rep, level, err := s.reps.ResolveRep(ctx, customerID, orderID)
		if err != nil {
			return AttributionCheckResult{}, fmt.Errorf("rep-assignment seam: %w", err)
		}
		if rep != l.RepUserID {
			out.Mismatches = append(out.Mismatches, AttributionMismatch{
				InvoiceID: l.InvoiceID, InvoiceNo: l.InvoiceNo, CustomerID: customerID, OrderID: orderID,
				ReportRepUserID: l.RepUserID, SeamRepUserID: rep, SeamLevel: level,
			})
		}
	}
	if len(out.Mismatches) == 0 {
		out.Explanation = "Every attributed sale in the window agrees with the rep-assignment resolver."
	} else {
		out.Explanation = "These sales are attributed differently by the commission report and by the " +
			"rep-assignment resolver. Commission computed from this window may be paid to the wrong rep."
	}
	return out, nil
}
