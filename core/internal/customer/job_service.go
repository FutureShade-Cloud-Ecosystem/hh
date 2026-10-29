package customer

// job_service.go, the job accounts under a customer (wave 3 JA-1, decision 1.1 A).
//
// A job is a site of work under one customer. It carries its own ship to address, PO reference and
// contact, and it may carry its own credit limit and payment terms; a job that carries neither
// shares the customer's, and null is not zero.
//
// THE SERVICE OWNS THE RULES, THE REPOSITORY OWNS THE ROWS. What the caller may see and what may
// be written is decided here, against the caller's own authority and the customer's own facts; the
// repository only persists what this file resolved and fences the statement by the caller's reach.
//
// THE FENCE IS THE ACCOUNT'S OWN, THEN THE JOB'S OWN. A job sits under a customer, so the customer
// is the first fence (the account read's rule: an account homed at a branch the caller cannot reach
// is not found, and an account with no home is company-wide). The job's own branch is the second
// fence, and it is the branch the job sits at, so a job of another branch is not found by a caller
// whose grants do not reach it.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ErrJobStoreNotConfigured marks a Service wired without the job persistence seam. It is returned
// rather than falling back to an unfenced write, for the reason the scoped account read gives:
// a fence that cannot be applied is not a fence.
var ErrJobStoreNotConfigured = errors.New("customer: the job store is not configured")

// jobStore is the job persistence seam.
//
// It is a SEPARATE, optional interface rather than a widened accountStore on purpose: accountStore
// is implemented outside this package (the portal BFF and the procurement module compose the
// Service with their own fakes), and widening it would be a breaking change to a module this swarm
// does not own. *Repository implements it, so the real deployment always takes the fenced path.
type jobStore interface {
	InsertJob(ctx context.Context, w JobWrite) (Job, error)
	UpdateJob(ctx context.Context, customerID, jobID string, f JobFields, reach AccountReach) (Job, error)
	CloseJob(ctx context.Context, customerID, jobID string, reach AccountReach) (Job, error)
	ListJobs(ctx context.Context, customerID string, reach AccountReach) ([]Job, error)
	GetJob(ctx context.Context, customerID, jobID string, reach AccountReach) (Job, bool, error)
	JobEffectiveTerms(ctx context.Context, jobID string) (JobTerms, error)
	JobStatusForCustomer(ctx context.Context, customerID, jobID string) (string, error)
}

// WithJobs binds the job persistence seam. Returns the Service for chaining. A Service without it
// refuses every job door rather than serving one ungated.
func (s *Service) WithJobs(j jobStore) *Service { s.jobs = j; return s }

// JobWire is the job on the wire.
//
// It embeds Job, whose credit limit is shadowed by the shallower pointer field below (encoding/json
// takes the shallowest name), so the figure is absent from the response unless the dealer's account
// position policy admits the caller. The job's own limit is the same kind of figure as the
// account's: it is a ceiling on what the customer may owe, and a job's limit may never exceed the
// customer's, so showing the job's limit while the account's is hidden would hand over a lower
// bound on the account's own limit. The refusal is byte absence at the marshal layer, never 0.
type JobWire struct {
	Job
	// CreditLimitCents is the job's own limit, absent when the job shares the customer's and
	// absent for a caller the account position policy refuses.
	CreditLimitCents *int64 `json:"credit_limit_cents,omitempty"`
}

// JobForWire shapes one job under the dealer's account position policy.
func (s *Service) JobForWire(ctx context.Context, j Job) JobWire {
	w := JobWire{Job: j}
	if s.PositionAllowed(ctx, j.BranchID) {
		w.CreditLimitCents = j.CreditLimitCents
	}
	return w
}

// JobsForWire shapes a list of jobs under the same policy.
func (s *Service) JobsForWire(ctx context.Context, jobs []Job) []JobWire {
	out := make([]JobWire, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, s.JobForWire(ctx, j))
	}
	return out
}

// jobCaller resolves the guard-stamped caller and the reach it carries.
//
// The reach is built HERE, from the stamped caller, never from the request, so passing a branch id
// cannot itself grant access to that branch. The caller's UserID must be present: it is the ERP
// users.id the job's creator is stamped with, and an unattributable write is refused, never
// attributed to nobody.
func jobCaller(ctx context.Context) (identity.StaffContext, AccountReach, error) {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || caller.UserID == "" {
		return identity.StaffContext{}, AccountReach{}, ErrUnauthenticated
	}
	return caller, reachFromCaller(caller), nil
}

// CreateJob opens a job under a customer.
//
// The job's branch is the customer's own home branch when the customer has one: a job belongs to
// its customer, and a job at another branch than its customer would sit outside the fence its
// customer's own reach implies. A customer with NO home branch is company-wide, so its job says
// where it sits, and the branch must be one the caller is granted.
//
// The job number is the customer's own, and the job's own credit limit and payment terms are the
// job's own: absent means the customer shares them, and null is not zero.
func (s *Service) CreateJob(ctx context.Context, customerID string, in JobCreateInput) (Job, error) {
	if s.jobs == nil {
		return Job{}, ErrJobStoreNotConfigured
	}
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return Job{}, httpx.BadRequestf("customer id is required")
	}
	if _, err := uuid.Parse(customerID); err != nil {
		return Job{}, httpx.BadRequestf("customer id must be a uuid")
	}
	jobNo := strings.TrimSpace(in.JobNo)
	if jobNo == "" {
		return Job{}, httpx.BadRequestf("job_no is required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return Job{}, httpx.BadRequestf("name is required")
	}
	if in.CreditLimitCents != nil && *in.CreditLimitCents < 0 {
		return Job{}, httpx.BadRequestf("credit_limit_cents must be >= 0")
	}
	if in.TermsDays != nil && *in.TermsDays < 0 {
		return Job{}, httpx.BadRequestf("terms_days must be >= 0")
	}
	caller, _, err := jobCaller(ctx)
	if err != nil {
		return Job{}, err
	}
	// The customer is the first fence: an account the caller cannot reach is not found, and a job
	// under it is not found either.
	acct, err := s.GetAccount(ctx, customerID)
	if err != nil {
		return Job{}, err
	}
	branchID, err := jobBranch(caller, acct, in.BranchID)
	if err != nil {
		return Job{}, err
	}
	fields, err := jobFieldsFromCreate(in)
	if err != nil {
		return Job{}, err
	}
	// THE CEILING. A job's own limit may never exceed the customer's own, because the customer's
	// exposure includes every job it holds.
	if err := jobLimitCeiling(fields.CreditLimitCents, acct.CreditLimitCents); err != nil {
		return Job{}, err
	}
	return s.jobs.InsertJob(ctx, JobWrite{
		CustomerID: customerID,
		BranchID:   branchID,
		JobNo:      jobNo,
		CreatedBy:  caller.UserID,
		Fields:     fields,
	})
}

// jobLimitCeiling refuses a job's own credit limit that is above the customer's own.
//
// THE REFUSAL CARRIES NO FIGURES. The customer's credit limit is an account position figure, and
// the dealer's account position policy decides who may see it; a refusal that quoted the limit
// would hand over exactly what the policy hides. The words are the whole answer: a job's own
// limit cannot be above the customer's own.
func jobLimitCeiling(jobLimit *int64, customerLimit int64) error {
	if jobLimit == nil {
		return nil
	}
	if *jobLimit > customerLimit {
		return httpx.BadRequestf("a job's own credit limit cannot exceed the customer's own credit limit")
	}
	return nil
}

// jobBranch resolves the branch the job sits at.
//
// A customer homed at a branch: the job sits at that branch, and a body that names another branch
// is refused in words rather than quietly ignored. A customer with no home branch is company-wide,
// so the job names its own branch, and that branch must be one the caller is granted.
func jobBranch(caller identity.StaffContext, acct Account, named string) (string, error) {
	named = strings.TrimSpace(named)
	if acct.BranchID != "" {
		if named != "" && named != acct.BranchID {
			return "", httpx.BadRequestf("a job under a customer homed at a branch sits at that branch")
		}
		return acct.BranchID, nil
	}
	if named == "" {
		return "", httpx.BadRequestf("branch_id is required: this customer has no home branch")
	}
	if _, err := uuid.Parse(named); err != nil {
		return "", httpx.BadRequestf("branch_id must be a uuid")
	}
	if !caller.CanAccessBranch(named) {
		return "", httpx.BadRequestf("that branch is outside your grants")
	}
	return named, nil
}

// jobFieldsFromCreate resolves the create payload into the columns a write persists.
func jobFieldsFromCreate(in JobCreateInput) (JobFields, error) {
	f := JobFields{
		Name:             strings.TrimSpace(in.Name),
		ShipToAddress:    orEmptyObject(in.ShipToAddress),
		POReference:      strings.TrimSpace(in.POReference),
		ContactName:      strings.TrimSpace(in.ContactName),
		ContactEmail:     strings.TrimSpace(in.ContactEmail),
		ContactPhone:     strings.TrimSpace(in.ContactPhone),
		CreditLimitCents: in.CreditLimitCents,
		TermsDays:        in.TermsDays,
	}
	perf, err := parseJobDate(in.SubstantialPerformanceDate)
	if err != nil {
		return JobFields{}, err
	}
	f.SubstantialPerformanceDate = perf
	supply, err := parseJobDate(in.LastSupplyDate)
	if err != nil {
		return JobFields{}, err
	}
	f.LastSupplyDate = supply
	return f, nil
}

// parseJobDate reads a YYYY-MM-DD date, empty meaning none. A date that is not a date is the
// caller's mistake, and it is refused in words.
func parseJobDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, httpx.BadRequestf("a date must be YYYY-MM-DD")
	}
	return &t, nil
}

// EditJob edits a job of a customer.
//
// THE EDIT IS A PARTIAL ONE. A field left out keeps the job's own value, and an explicit null on
// the job's own credit limit or payment terms clears it, so the job shares the customer's again.
// The edit is resolved here against the job as it stands, so the write never re-decides what an
// absent field means.
func (s *Service) EditJob(ctx context.Context, customerID, jobID string, in JobEditInput) (Job, error) {
	if s.jobs == nil {
		return Job{}, ErrJobStoreNotConfigured
	}
	_, reach, err := jobCaller(ctx)
	if err != nil {
		return Job{}, err
	}
	current, err := s.GetJob(ctx, customerID, jobID)
	if err != nil {
		return Job{}, err
	}
	// The customer's own limit is the ceiling the edit is measured against.
	acct, err := s.GetAccount(ctx, customerID)
	if err != nil {
		return Job{}, err
	}
	fields, err := applyJobEdit(current, in)
	if err != nil {
		return Job{}, err
	}
	// THE CEILING, on the edit as well: a job's own limit may never exceed the customer's own,
	// and an edit that raised it above the ceiling is refused in words.
	if err := jobLimitCeiling(fields.CreditLimitCents, acct.CreditLimitCents); err != nil {
		return Job{}, err
	}
	return s.jobs.UpdateJob(ctx, customerID, current.JobID, fields, reach)
}

// applyJobEdit resolves the edit against the job as it stands.
func applyJobEdit(current Job, in JobEditInput) (JobFields, error) {
	f := JobFields{
		Name:                       current.Name,
		ShipToAddress:              current.ShipToAddress,
		POReference:                current.POReference,
		ContactName:                current.ContactName,
		ContactEmail:               current.ContactEmail,
		ContactPhone:               current.ContactPhone,
		CreditLimitCents:           current.CreditLimitCents,
		TermsDays:                  current.TermsDays,
		SubstantialPerformanceDate: current.SubstantialPerformanceDate,
		LastSupplyDate:             current.LastSupplyDate,
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return JobFields{}, httpx.BadRequestf("name is required")
		}
		f.Name = name
	}
	if in.ShipToAddress != nil {
		f.ShipToAddress = orEmptyObject(in.ShipToAddress)
	}
	if in.POReference != nil {
		f.POReference = strings.TrimSpace(*in.POReference)
	}
	if in.ContactName != nil {
		f.ContactName = strings.TrimSpace(*in.ContactName)
	}
	if in.ContactEmail != nil {
		f.ContactEmail = strings.TrimSpace(*in.ContactEmail)
	}
	if in.ContactPhone != nil {
		f.ContactPhone = strings.TrimSpace(*in.ContactPhone)
	}
	if in.CreditLimitCents.Present {
		if in.CreditLimitCents.Value != nil && *in.CreditLimitCents.Value < 0 {
			return JobFields{}, httpx.BadRequestf("credit_limit_cents must be >= 0")
		}
		f.CreditLimitCents = in.CreditLimitCents.Value
	}
	if in.TermsDays.Present {
		if in.TermsDays.Value != nil && *in.TermsDays.Value < 0 {
			return JobFields{}, httpx.BadRequestf("terms_days must be >= 0")
		}
		f.TermsDays = in.TermsDays.Value
	}
	if in.SubstantialPerformanceDate.Present {
		if in.SubstantialPerformanceDate.Value == nil {
			f.SubstantialPerformanceDate = nil
		} else {
			d, err := parseJobDate(*in.SubstantialPerformanceDate.Value)
			if err != nil {
				return JobFields{}, err
			}
			f.SubstantialPerformanceDate = d
		}
	}
	if in.LastSupplyDate.Present {
		if in.LastSupplyDate.Value == nil {
			f.LastSupplyDate = nil
		} else {
			d, err := parseJobDate(*in.LastSupplyDate.Value)
			if err != nil {
				return JobFields{}, err
			}
			f.LastSupplyDate = d
		}
	}
	return f, nil
}

// ListJobs lists the jobs of a customer inside the caller's reach.
//
// The customer is the first fence (the account read's rule) and the job's own branch is the
// second: a job of a branch the caller cannot reach is not in the list.
func (s *Service) ListJobs(ctx context.Context, customerID string) ([]Job, error) {
	if s.jobs == nil {
		return nil, ErrJobStoreNotConfigured
	}
	_, reach, err := jobCaller(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.GetAccount(ctx, customerID); err != nil {
		return nil, err
	}
	jobs, err := s.jobs.ListJobs(ctx, customerID, reach)
	if err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []Job{}
	}
	return jobs, nil
}

// GetJob reads one job of one customer inside the caller's reach.
//
// A job of another customer, a job of another branch, and a job that nobody has all answer not
// found: the same answer, so a caller cannot learn that a job exists somewhere they may not go.
func (s *Service) GetJob(ctx context.Context, customerID, jobID string) (Job, error) {
	if s.jobs == nil {
		return Job{}, ErrJobStoreNotConfigured
	}
	_, reach, err := jobCaller(ctx)
	if err != nil {
		return Job{}, err
	}
	if customerID == "" {
		return Job{}, httpx.BadRequestf("customer id is required")
	}
	if _, err := uuid.Parse(customerID); err != nil {
		return Job{}, httpx.BadRequestf("customer id must be a uuid")
	}
	if _, err := uuid.Parse(jobID); err != nil {
		return Job{}, httpx.BadRequestf("job id must be a uuid")
	}
	job, found, err := s.jobs.GetJob(ctx, customerID, jobID, reach)
	if err != nil {
		return Job{}, err
	}
	if !found {
		return Job{}, ErrNotFound
	}
	return job, nil
}

// CloseJob closes a job of a customer inside the caller's reach.
//
// A closed job reads and reports, and it takes no new document. The close is not silent when the
// job is already closed: the caller is told, because a close that has already happened is a fact
// the caller asked about.
func (s *Service) CloseJob(ctx context.Context, customerID, jobID string) (Job, error) {
	if s.jobs == nil {
		return Job{}, ErrJobStoreNotConfigured
	}
	_, reach, err := jobCaller(ctx)
	if err != nil {
		return Job{}, err
	}
	if _, err := s.GetJob(ctx, customerID, jobID); err != nil {
		return Job{}, err
	}
	return s.jobs.CloseJob(ctx, customerID, jobID, reach)
}

// EffectiveTerms answers the payment terms a job is dated by: the job's own when it carries them,
// the customer's when it does not.
//
// IT IS THE ONE READ THE LATER WAVES USE. Wave 3 JA-2 dates an invoice by the job's terms, and
// JA-3 checks a sale against the job's limit; neither of them should have to read the job row and
// the customer row and work out the answer. The read says which of the two it answered (Own), so a
// screen can tell the customer "this job carries its own terms" without a second read.
//
// A job that does not exist is not found. A job with no own terms is not an error and not zero: it
// reads the customer's terms, and the customer's own terms are the customer's business.
func (s *Service) EffectiveTerms(ctx context.Context, jobID string) (JobTerms, error) {
	if s.jobs == nil {
		return JobTerms{}, ErrJobStoreNotConfigured
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return JobTerms{}, httpx.BadRequestf("job id is required")
	}
	if _, err := uuid.Parse(jobID); err != nil {
		return JobTerms{}, httpx.BadRequestf("job id must be a uuid")
	}
	return s.jobs.JobEffectiveTerms(ctx, jobID)
}

// ErrJobClosed is the refusal a CLOSED job answers with. It is its own sentinel and not the
// generic not-found, because the two answers are different facts: a closed job is there and is
// finished, and a job that is not there is not there.
var ErrJobClosed = errors.New("customer: the job is closed")

// JobOpenForCustomer refuses a job that is CLOSED as the job of any new document.
//
// IT IS THE DOOR WAVE 3 JA-2 OPENS A DOCUMENT ON. The answer is about the job's own status and
// nothing else: the branch fence belongs to the document write that names the job, and a caller
// with no staff row (a worker posting a document it was handed) must still be able to ask. A job of
// another customer is not the customer's job at all, and is not found rather than closed.
//
// A closed job is refused, never reopened by asking.
func (s *Service) JobOpenForCustomer(ctx context.Context, customerID, jobID string) error {
	if s.jobs == nil {
		return ErrJobStoreNotConfigured
	}
	customerID = strings.TrimSpace(customerID)
	jobID = strings.TrimSpace(jobID)
	if customerID == "" {
		return httpx.BadRequestf("customer id is required")
	}
	if jobID == "" {
		return httpx.BadRequestf("job id is required")
	}
	status, err := s.jobs.JobStatusForCustomer(ctx, customerID, jobID)
	if err != nil {
		return err
	}
	if status != JobStatusOpen {
		return ErrJobClosed
	}
	return nil
}
