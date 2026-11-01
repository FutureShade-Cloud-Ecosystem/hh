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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const (
	actHoldbackRuleSet = "HOLDBACK_RULE_SET"
	actHoldbackLink    = "HOLDBACK_JOB_LINK"
)

// ruleStore is the rule tables' persistence seam; *Repository satisfies it.
type ruleStore interface {
	DealerRule(ctx context.Context) (DealerRule, error)
	SetDealerOverride(ctx context.Context, pct *int, setBy string) (DealerRule, error)
	SetJobOverride(ctx context.Context, customerJobID, branchID string, pct *int, setBy string) error
	ProvinceRule(ctx context.Context, province string) (ProvinceRule, bool, error)
	JobOverride(ctx context.Context, customerJobID string) (*int, error)
	LinkCustomerJob(ctx context.Context, lienJobID, customerJobID string) error
	ProvinceRules(ctx context.Context) ([]ProvinceRule, error)
	RuleInputs(ctx context.Context, sitePro, customerJobID string) (RuleInputs, error)
	// InTx runs fn on one transaction (the caller's when one rides ctx), so a change and its audit
	// record commit or roll back together.
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// txRecorder is the audit recorder's transactional write, satisfied by *audit.Service.
type txRecorder interface {
	RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error
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

// record writes one audit row on the transaction the change rides (ctx carries it from InTx), so a
// failed record fails the change. A recorder with no transactional write (a unit fake) records on its
// own path. No recorder wired records nothing.
func (s *Service) record(ctx context.Context, a audit.SensitiveAccess) error {
	if s.audit == nil {
		return nil
	}
	if tr, ok := s.audit.(txRecorder); ok {
		if tx := customer.CallerTx(ctx); tx != nil {
			return tr.RecordTx(ctx, tx, a)
		}
	}
	return s.audit.Record(ctx, a)
}

// recordRuleSet records a set (pct) or a clear (nil) of an override. A set carries the percent and a
// clear carries none, which tells them apart.
func (s *Service) recordRuleSet(ctx context.Context, c identity.StaffContext, entityType, entityID, branchID string, pct *int) error {
	extra := map[string]any{}
	if pct != nil {
		extra["holdback_pct_bps"] = *pct
	}
	return s.record(ctx, audit.SensitiveAccess{
		Action: actHoldbackRuleSet, EntityType: entityType, EntityID: entityID,
		ActorID: c.UserID, BranchID: branchID, Extra: extra,
	})
}

// SetDealerOverride sets (pct) or clears (nil) the dealer's override of the province's percent. The
// change and its record are one transaction. The record's entity is the dealer rule row's own uuid.
func (s *Service) SetDealerOverride(ctx context.Context, pct *int) (DealerRule, error) {
	c, err := s.managerCaller(ctx)
	if err != nil {
		return DealerRule{}, err
	}
	if err := checkPct(pct); err != nil {
		return DealerRule{}, err
	}
	var d DealerRule
	err = s.rules().InTx(ctx, func(ctx context.Context) error {
		var err error
		if d, err = s.rules().SetDealerOverride(ctx, pct, c.UserID); err != nil {
			return err
		}
		return s.recordRuleSet(ctx, c, "lien.holdback_dealer_rule", d.ID, "", pct)
	})
	if err != nil {
		return DealerRule{}, err
	}
	return d, nil
}

// SetJobOverride sets (pct) or clears (nil) one customer job's override, with its record in one transaction.
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
	return s.rules().InTx(ctx, func(ctx context.Context) error {
		if err := s.rules().SetJobOverride(ctx, site.JobID, site.BranchID, pct, c.UserID); err != nil {
			return err
		}
		return s.recordRuleSet(ctx, c, "lien.holdback_job_override", site.JobID, site.BranchID, pct)
	})
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
	// One statement reads the dealer rule, the province rule and the job's override, so the answer is
	// one snapshot whether or not a caller's transaction rides ctx.
	in, err := s.rules().RuleInputs(ctx, site.Province, site.JobID)
	if err != nil {
		return HoldbackRule{}, err
	}
	out := HoldbackRule{Province: site.Province, ProvinceSource: "SITE"}
	if out.Province == "" {
		out.Province, out.ProvinceSource = in.Dealer.Province, "DEALER"
	}
	if !in.HasProvince {
		return HoldbackRule{}, unprocessable("lien: there is no holdback rule for the province %s", out.Province)
	}
	pr := in.Province
	out.HoldbackPctBps, out.PctSource = pr.HoldbackPctBps, PctFromProvince
	out.PreserveDays, out.PerfectDays = pr.PreserveDays, pr.PerfectDays
	if in.Dealer.HoldbackPctBps != nil {
		out.HoldbackPctBps, out.PctSource = *in.Dealer.HoldbackPctBps, PctFromDealer
	}
	if in.JobOverride != nil {
		out.HoldbackPctBps, out.PctSource = *in.JobOverride, PctFromJob
	}
	return out, nil
}

// LinkCustomerJob links a lien job to ONE customer job of the same customer, or unlinks it (an empty
// customerJobID). The customer module's own rule for a new document decides: a job of another customer
// and a closed job are refused in its words, and a job at a branch outside the caller's reach too. The
// lien job itself must also sit inside the caller's reach, and a lien job with no customer cannot link.
func (s *Service) LinkCustomerJob(ctx context.Context, lienJobID, customerJobID string) (Job, error) {
	c, err := s.managerCaller(ctx)
	if err != nil {
		return Job{}, err
	}
	job, err := s.store.GetJob(ctx, lienJobID)
	if err != nil {
		return Job{}, err
	}
	if !c.CanAccessBranch(job.BranchID) {
		return Job{}, forbidden("lien: that lien job sits at a branch outside your reach")
	}
	if customerJobID != "" {
		if job.CustomerID == "" {
			return Job{}, unprocessable("lien: that lien job has no customer, so it cannot link to a customer job")
		}
		if s.custJobs == nil {
			return Job{}, clientErr(500, "lien: customer jobs not configured")
		}
		site, err := s.custJobs.JobForLink(ctx, job.CustomerID, customerJobID)
		if err != nil {
			return Job{}, err
		}
		customerJobID = site.JobID
	}
	// An unlink, or a move to another customer job (the same thing for the job it leaves), while the ledger
	// holds invoice level retention would let the old job level route (which reads only the link) and RT-3's
	// release (which reads the job that holds the invoice) each release the same holdback, so both are
	// refused in the same words. Linking the job it already has is no change.
	if job.CustomerJobID != "" && customerJobID != job.CustomerJobID {
		entries, err := s.store.HoldbackEntriesForJob(ctx, lienJobID)
		if err != nil {
			return Job{}, err
		}
		for _, e := range entries {
			if e.InvoiceID != "" {
				return Job{}, unprocessable("lien: job %s holds invoice level retention in its ledger, so it cannot be unlinked from its customer job; correct the invoices' holdback first", job.JobNo)
			}
		}
	}
	before := job.CustomerJobID
	if err := s.rules().InTx(ctx, func(ctx context.Context) error {
		if err := s.rules().LinkCustomerJob(ctx, lienJobID, customerJobID); err != nil {
			return err
		}
		// The link decides which job's rule a lien job's holdback follows, so it is recorded: a link
		// names the job it linked to, an unlink only the one it left (a nil value is skipped).
		extra := map[string]any{"before": nil, "after": nil}
		if before != "" {
			extra["before"] = before
		}
		if customerJobID != "" {
			extra["after"] = customerJobID
		}
		return s.record(ctx, audit.SensitiveAccess{
			Action: actHoldbackLink, EntityType: "lien.job_customer_link", EntityID: job.ID,
			ActorID: c.UserID, BranchID: job.BranchID, Extra: extra,
		})
	}); err != nil {
		return Job{}, err
	}
	job.CustomerJobID = customerJobID
	return job, nil
}

// ProvinceRules lists every province's statutory holdback rule.
func (s *Service) ProvinceRules(ctx context.Context) ([]ProvinceRule, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	return s.rules().ProvinceRules(ctx)
}
