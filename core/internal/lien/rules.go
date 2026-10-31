package lien

// rules.go, the holdback rule: province rules with a dealer override and a job override (wave 3 RT-1).
//
// THE READ ORDER (HoldbackRuleForJob): the job's override, else the dealer's, else the
// province's. Only the percent is overridable; the preserve and perfect days are the province's.
//
// THE BOUNDS: a percent is basis points from 0 to 10000. Zero is a rule (no holdback), not an absence;
// an absent override is nil.
//
// THE GATE: setting an override is for managers, the line the lien routes already draw
// (RequireManager); the service holds the same line so a call that does not come through the route is
// held to it too. A job override is also held to the caller's branch reach over the job's branch. Every
// set and every clear is recorded.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const actHoldbackRuleSet = "HOLDBACK_RULE_SET"

// ruleStore is the rule tables' persistence seam; *Repository satisfies it.
type ruleStore interface {
	DealerRule(ctx context.Context) (DealerRule, error)
	SetDealerOverride(ctx context.Context, pct *int, setBy string) (DealerRule, error)
	SetJobOverride(ctx context.Context, customerJobID, branchID string, pct *int, setBy string) error
	ProvinceRule(ctx context.Context, province string) (ProvinceRule, bool, error)
	JobOverride(ctx context.Context, customerJobID string) (*int, error)
}

var _ ruleStore = (*Repository)(nil)

// WithCustomerJobs injects the customer module's job seam.
func (s *Service) WithCustomerJobs(j CustomerJobs) *Service { s.custJobs = j; return s }

func (s *Service) rules() ruleStore { return s.repo }

func checkPct(pct *int) error {
	if pct != nil && (*pct < 0 || *pct > 10000) {
		return unprocessable("lien: a holdback percent must be between 0 and 100 percent (0 to 10000 basis points)")
	}
	return nil
}

func (s *Service) managerCaller(ctx context.Context) (identity.StaffContext, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, err
	}
	if !c.IsManager() {
		return identity.StaffContext{}, forbidden("lien: only a manager may set a holdback override")
	}
	return c, nil
}

func (s *Service) recordRuleSet(ctx context.Context, c identity.StaffContext, entityType, entityID, branchID string, pct *int) error {
	if s.audit == nil {
		return nil
	}
	extra := map[string]any{"holdback_pct_bps": nil}
	if pct != nil {
		extra["holdback_pct_bps"] = *pct
	}
	return s.audit.Record(ctx, audit.SensitiveAccess{
		Action: actHoldbackRuleSet, EntityType: entityType, EntityID: entityID,
		ActorID: c.UserID, BranchID: branchID, Extra: extra,
	})
}

// SetDealerOverride sets (pct) or clears (nil) the dealer's override of the province's percent.
func (s *Service) SetDealerOverride(ctx context.Context, pct *int) (DealerRule, error) {
	c, err := s.managerCaller(ctx)
	if err != nil {
		return DealerRule{}, err
	}
	if err := checkPct(pct); err != nil {
		return DealerRule{}, err
	}
	d, err := s.rules().SetDealerOverride(ctx, pct, c.UserID)
	if err != nil {
		return DealerRule{}, err
	}
	if err := s.recordRuleSet(ctx, c, "lien.holdback_dealer_rule", "dealer", "", pct); err != nil {
		return DealerRule{}, err
	}
	return d, nil
}

// SetJobOverride sets (pct) or clears (nil) one customer job's override.
func (s *Service) SetJobOverride(ctx context.Context, customerJobID string, pct *int) error {
	c, err := s.managerCaller(ctx)
	if err != nil {
		return err
	}
	if err := checkPct(pct); err != nil {
		return err
	}
	if s.custJobs == nil {
		return clientErr(500, "lien: customer jobs not configured")
	}
	site, err := s.custJobs.JobSite(ctx, customerJobID)
	if err != nil {
		return err
	}
	if !c.CanAccessBranch(site.BranchID) {
		return forbidden("lien: that job sits at a branch outside your reach")
	}
	if err := s.rules().SetJobOverride(ctx, site.JobID, site.BranchID, pct, c.UserID); err != nil {
		return err
	}
	return s.recordRuleSet(ctx, c, "lien.holdback_job_override", site.JobID, site.BranchID, pct)
}

// Where a rule's percent came from.
const (
	PctFromJob      = "JOB"
	PctFromDealer   = "DEALER"
	PctFromProvince = "PROVINCE"
)

// HoldbackRule is the rule that answers one job.
type HoldbackRule struct {
	HoldbackPctBps int    `json:"holdback_pct_bps"`
	PreserveDays   int    `json:"preserve_days"`
	PerfectDays    int    `json:"perfect_days"`
	PctSource      string `json:"pct_source"` // JOB | DEALER | PROVINCE
	Province       string `json:"province"`
	ProvinceSource string `json:"province_source"` // SITE (the job's ship to province) | DEALER
}

// HoldbackRuleForJob answers the holdback rule for one customer job in one read: the job's override, else
// the dealer's, else the province's. The province is the job's site province when its ship to address
// names one, else the dealer's own. A province with no rule is refused in words, never guessed. The
// whole read rides a caller's transaction (customer.WithCallerTx) so RT-2 can call it inside an
// invoice's write. A staff caller must reach the job's branch; a context with no staff row (a worker
// writing the invoice) is held by the document's own fence.
func (s *Service) HoldbackRuleForJob(ctx context.Context, customerJobID string) (HoldbackRule, error) {
	if s.custJobs == nil {
		return HoldbackRule{}, clientErr(500, "lien: customer jobs not configured")
	}
	site, err := s.custJobs.JobSite(ctx, customerJobID)
	if err != nil {
		return HoldbackRule{}, err
	}
	if c, ok := s.callerFrom(ctx); ok && c.UserID != "" && !c.CanAccessBranch(site.BranchID) {
		return HoldbackRule{}, clientErr(404, "that job was not found")
	}
	dealer, err := s.rules().DealerRule(ctx)
	if err != nil {
		return HoldbackRule{}, err
	}
	out := HoldbackRule{Province: site.Province, ProvinceSource: "SITE"}
	if out.Province == "" {
		out.Province, out.ProvinceSource = dealer.Province, "DEALER"
	}
	pr, ok, err := s.rules().ProvinceRule(ctx, out.Province)
	if err != nil {
		return HoldbackRule{}, err
	}
	if !ok {
		return HoldbackRule{}, unprocessable("lien: there is no holdback rule for the province %s", out.Province)
	}
	out.HoldbackPctBps, out.PctSource = pr.HoldbackPctBps, PctFromProvince
	out.PreserveDays, out.PerfectDays = pr.PreserveDays, pr.PerfectDays
	if dealer.HoldbackPctBps != nil {
		out.HoldbackPctBps, out.PctSource = *dealer.HoldbackPctBps, PctFromDealer
	}
	jo, err := s.rules().JobOverride(ctx, site.JobID)
	if err != nil {
		return HoldbackRule{}, err
	}
	if jo != nil {
		out.HoldbackPctBps, out.PctSource = *jo, PctFromJob
	}
	return out, nil
}
