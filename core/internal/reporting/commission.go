package reporting

// commission.go, DIB-96 / A-22. The arithmetic, the rule selection, and the model. The SQL lives in
// commission_repo.go; the HTTP surface in handler.go.
//
// -- THE WHOLE THING IN FOUR SENTENCES ----------------------------------------------------------------
// A commission RULE says: this rep earns this rate, on either REVENUE or MARGIN, optionally only for
// this category and/or this branch. A commission RUN takes a period, walks every invoice line
// attributed to a rep, picks the most specific rule in force for that line, applies the rate, and
// freezes the answer with its drill-through. An ACCRUAL is a run that has not been paid and, this
// wave, cannot be: nothing here posts to the general ledger. The report says so on its face.
//
// -- WHY BASIS IS THE THING MOST WORTH TESTING --------------------------------------------------------
// REVENUE and MARGIN are not variations on a theme; on a 25%-margin line a 5% revenue rule pays FOUR
// TIMES a 5% margin rule. A swap between them is the single most expensive one-character mistake
// available in this file and it produces numbers that look entirely plausible, no crash, no zero, just
// a payroll that is wrong by a multiple. commissionBase is therefore a pure function with a named test
// (TestCommission_MarginBasisIsNotRevenueBasis) whose whole job is to die if the two arms are swapped.
//
// -- INTEGER CENTS, BASIS POINTS, ROUND ONCE PER LINE -------------------------------------------------
// Rates are integers in basis points and money is integer cents; no float touches a commission figure.
// Rounding is HALF-UP on the magnitude, applied ONCE PER LINE, and the run total is the sum of the
// rounded lines, not the rounded sum. The difference is at most a cent per line, and the reason to
// prefer per-line is that the drill-through must add up to the total a rep is paid. A rep who adds the
// drill-through by hand and gets a different number than the header stops trusting the report, and they
// are right to.

import (
	"sort"
	"time"
)

// Commission bases (0144 CHECK).
const (
	BasisRevenue = "REVENUE"
	BasisMargin  = "MARGIN"
)

// CommissionRule is one commission_rules row.
type CommissionRule struct {
	ID            string     `json:"id"`
	RepUserID     string     `json:"rep_user_id"`
	RepName       string     `json:"rep_name,omitempty"`
	Basis         string     `json:"basis"`
	RateBps       int        `json:"rate_bps"`
	CategoryID    string     `json:"category_id,omitempty"`
	CategoryCode  string     `json:"category_code,omitempty"`
	BranchID      string     `json:"branch_id,omitempty"`
	EffectiveFrom string     `json:"effective_from"`
	EffectiveTo   string     `json:"effective_to,omitempty"`
	IsActive      bool       `json:"is_active"`
	Note          string     `json:"note,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	effFrom       time.Time  // parsed, for selection
	effTo         *time.Time // parsed, nil = open-ended
}

// AttributedLine is one invoice line with its rep and its cost, as read for a commission run.
type AttributedLine struct {
	InvoiceID     string  `json:"invoice_id"`
	InvoiceLineID string  `json:"invoice_line_id"`
	InvoiceNo     string  `json:"invoice_no"`
	InvoiceDate   string  `json:"invoice_date"`
	CustomerName  string  `json:"customer_name"`
	ProductSKU    string  `json:"product_sku"`
	CategoryID    string  `json:"category_id,omitempty"`
	CategoryCode  string  `json:"category_code,omitempty"`
	BranchID      string  `json:"branch_id"`
	QtyBase       float64 `json:"qty_base"`
	RevenueCents  int64   `json:"revenue_cents"`
	CostCents     int64   `json:"cost_cents"`
	RepUserID     string  `json:"rep_user_id,omitempty"`
}

// MarginCents is revenue less cost for the line.
func (l AttributedLine) MarginCents() int64 { return l.RevenueCents - l.CostCents }

// CommissionLine is one computed drill-through row (and one commission_run_lines row).
type CommissionLine struct {
	AttributedLine
	MarginCentsV    int64  `json:"margin_cents"`
	Basis           string `json:"basis"`
	BasisCents      int64  `json:"basis_cents"`
	RateBps         int    `json:"rate_bps"`
	CommissionCents int64  `json:"commission_cents"`
	RuleID          string `json:"rule_id,omitempty"`
	CommissionRunID string `json:"run_id,omitempty"`
	// NoRuleReason is set when a line was attributed to a rep but earned nothing, and says WHY. A line
	// that silently earns zero is indistinguishable from a line the report forgot, and the difference
	// matters to the person whose pay it is.
	NoRuleReason string `json:"no_rule_reason,omitempty"`
}

// CommissionPeriod is one rep's computed period: the header plus its lines.
type CommissionPeriod struct {
	RunID           string           `json:"run_id,omitempty"`
	RepUserID       string           `json:"rep_user_id"`
	RepName         string           `json:"rep_name"`
	PeriodStart     string           `json:"period_start"`
	PeriodEnd       string           `json:"period_end"`
	BranchID        string           `json:"branch_id,omitempty"`
	SaleCount       int              `json:"sale_count"`
	RevenueCents    int64            `json:"revenue_cents"`
	CostCents       int64            `json:"cost_cents"`
	MarginCents     int64            `json:"margin_cents"`
	CommissionCents int64            `json:"commission_cents"`
	Status          string           `json:"status"`
	Lines           []CommissionLine `json:"lines,omitempty"`
	ComputedAt      time.Time        `json:"computed_at"`
	// PostsToGL is ALWAYS false this wave and is serialized anyway, so the surface renders the caveat
	// from the server's own answer rather than from a hard-coded string in a component that someone
	// could later remove without touching the backend.
	PostsToGL bool `json:"posts_to_gl"`
	// UnattributedCents is revenue in the window that belongs to NO rep (the house queue). Reported so
	// the commission report's revenue can be reconciled against sales_by_day rather than quietly
	// falling short of it.
	UnattributedCents int64 `json:"unattributed_cents"`
	UnattributedCount int   `json:"unattributed_count"`
}

// AccrualLine is one row of the accrual register: what is owed and unpaid.
type AccrualLine struct {
	RepUserID       string `json:"rep_user_id"`
	RepName         string `json:"rep_name"`
	PeriodCount     int    `json:"period_count"`
	CommissionCents int64  `json:"commission_cents"`
}

// Accrual is the register plus its total. PostsToGL is false, always, this wave.
type Accrual struct {
	From       string        `json:"from"`
	To         string        `json:"to"`
	Lines      []AccrualLine `json:"lines"`
	TotalCents int64         `json:"total_cents"`
	PostsToGL  bool          `json:"posts_to_gl"`
	// Note is the sentence a controller needs to read before they believe the number. Server-owned for
	// the same reason PostsToGL is.
	Note string `json:"note"`
}

// AccrualNote is that sentence.
const AccrualNote = "Accrued, not posted. These figures are computed from invoiced sales and have no " +
	"general-ledger entry: nothing here appears in the trial balance and no liability has been raised."

// -- the arithmetic -----------------------------------------------------------

// commissionBase returns the amount a rate applies to under a basis.
//
// THE MUTATION TARGET. Swapping the two arms compiles, runs, and pays the wrong money.
// TestCommission_MarginBasisIsNotRevenueBasis exists to fail when that happens.
func commissionBase(basis string, revenueCents, costCents int64) int64 {
	switch basis {
	case BasisMargin:
		return revenueCents - costCents
	case BasisRevenue:
		return revenueCents
	default:
		// An unknown basis earns nothing. It cannot occur through the substrate (0144 CHECKs the column)
		// and returning zero rather than guessing is the only safe answer if it ever does.
		return 0
	}
}

// commissionOn applies a basis-point rate to a base, HALF-UP on the magnitude.
//
// Half-up on the MAGNITUDE (not toward +infinity) so a negative base, a margin-basis line sold below
// cost, rounds symmetrically: -0.5 cents becomes -1, the mirror of +0.5 becoming +1. Rounding a
// negative toward zero would quietly forgive part of a clawback.
func commissionOn(baseCents int64, rateBps int) int64 {
	if rateBps <= 0 || baseCents == 0 {
		return 0
	}
	neg := baseCents < 0
	if neg {
		baseCents = -baseCents
	}
	// (base * bps + 5000) / 10000 is half-up for non-negative operands.
	out := (baseCents*int64(rateBps) + 5000) / 10000
	if neg {
		return -out
	}
	return out
}

// -- rule selection -----------------------------------------------------------

// ruleSpecificity encodes the ordering stated in 0144's header:
//
//	(category set, branch set) 3 > (category set) 2 > (branch set) 1 > (neither) 0
//
// Category outranks branch on purpose: a category carve-out ("pavers pay 3, everything else 5") is a
// deliberate product decision, whereas a branch rule is usually an administrative default. When both
// are set the rule is unambiguously the most specific and wins outright.
func ruleSpecificity(r CommissionRule) int {
	s := 0
	if r.CategoryID != "" {
		s += 2
	}
	if r.BranchID != "" {
		s += 1
	}
	return s
}

// chooseRule picks the rule in force for a line: matching rep (already filtered by the caller),
// matching or wildcard category and branch, effective on the line's date. Ties break by specificity,
// then by the later effective_from, the substrate refuses the remaining tie (0144's partial unique
// index), so this function cannot be asked to choose between two identical rules.
//
// Returns nil when the rep has no rule covering the line. The caller records that as a NO_RULE reason
// rather than as zero commission with no explanation.
func chooseRule(rules []CommissionRule, categoryID, branchID string, on time.Time) *CommissionRule {
	var candidates []CommissionRule
	for _, r := range rules {
		if !r.IsActive {
			continue
		}
		if r.CategoryID != "" && r.CategoryID != categoryID {
			continue
		}
		if r.BranchID != "" && r.BranchID != branchID {
			continue
		}
		if on.Before(r.effFrom) {
			continue
		}
		if r.effTo != nil && on.After(*r.effTo) {
			continue
		}
		candidates = append(candidates, r)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		si, sj := ruleSpecificity(candidates[i]), ruleSpecificity(candidates[j])
		if si != sj {
			return si > sj
		}
		if !candidates[i].effFrom.Equal(candidates[j].effFrom) {
			return candidates[i].effFrom.After(candidates[j].effFrom)
		}
		return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
	})
	winner := candidates[0]
	return &winner
}

// -- computation --------------------------------------------------------------

// computePeriod is the pure core: lines in, a computed period out. It takes no database and no clock
// beyond the one handed to it, which is what makes the arithmetic testable without a fixture.
//
// Lines with no rep are NOT dropped, they are counted into UnattributedCents so the report can be
// reconciled against sales. Lines with a rep but no covering rule earn zero and carry a NoRuleReason.
func computePeriod(rep string, repName, from, to, branchID string, lines []AttributedLine,
	rules []CommissionRule, computedAt time.Time) CommissionPeriod {

	out := CommissionPeriod{
		RepUserID: rep, RepName: repName, PeriodStart: from, PeriodEnd: to, BranchID: branchID,
		Status: "ACCRUED", ComputedAt: computedAt, PostsToGL: false,
	}
	for _, l := range lines {
		if l.RepUserID == "" {
			out.UnattributedCents += l.RevenueCents
			out.UnattributedCount++
			continue
		}
		if l.RepUserID != rep {
			continue
		}
		on, err := time.Parse(DateLayout, l.InvoiceDate)
		if err != nil {
			on = computedAt
		}
		cl := CommissionLine{AttributedLine: l, MarginCentsV: l.MarginCents()}
		rule := chooseRule(rules, l.CategoryID, l.BranchID, on)
		if rule == nil {
			cl.NoRuleReason = "no commission rule in force for this rep on this line"
		} else {
			cl.Basis = rule.Basis
			cl.RateBps = rule.RateBps
			cl.RuleID = rule.ID
			cl.BasisCents = commissionBase(rule.Basis, l.RevenueCents, l.CostCents)
			cl.CommissionCents = commissionOn(cl.BasisCents, rule.RateBps)
		}
		out.Lines = append(out.Lines, cl)
		out.SaleCount++
		out.RevenueCents += l.RevenueCents
		out.CostCents += l.CostCents
		out.MarginCents += l.MarginCents()
		out.CommissionCents += cl.CommissionCents
	}
	return out
}
