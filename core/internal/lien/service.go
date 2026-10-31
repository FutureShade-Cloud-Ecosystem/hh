package lien

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// service.go, the lien domain orchestration: jobs, the idempotent holdback sync, the dual-gated release (with
// the PAYABLE-refused 422 + the 1150/2350 GL post), document rendering + version history, and the Bill 60
// aging + Monday triage roll-ups. Table access is entirely through the lienStore seam.

const actHoldbackRelease = "HOLDBACK_RELEASE"

// - jobs -

// CreateJob records a lien job and, when a substantial-performance date is known, registers the 45-day
// holdback-release deadline on the compliance calendar (the registry the Monday triage reads).
func (s *Service) CreateJob(ctx context.Context, in JobInput) (Job, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Job{}, err
	}
	if strings.TrimSpace(in.JobNo) == "" {
		return Job{}, unprocessable("lien: job_no is required")
	}
	if strings.TrimSpace(in.BranchID) == "" {
		return Job{}, unprocessable("lien: branch_id is required")
	}
	if in.Direction == "" {
		in.Direction = dirReceivable
	}
	if in.Direction != dirReceivable && in.Direction != dirPayable {
		return Job{}, unprocessable("lien: holdback_direction must be RECEIVABLE or PAYABLE")
	}
	job, err := s.store.InsertJob(ctx, in, caller.UserID)
	if err != nil {
		return Job{}, err
	}
	s.registerReleaseDeadline(ctx, job)
	return job, nil
}

// GetJob reads one job.
func (s *Service) GetJob(ctx context.Context, id string) (Job, error) {
	return s.store.GetJob(ctx, id)
}

// ListJobs lists jobs for a branch ("" = all).
func (s *Service) ListJobs(ctx context.Context, branchID string) ([]Job, error) {
	return s.store.ListJobs(ctx, branchID)
}

// registerReleaseDeadline best-effort registers the Bill 60 45-day release deadline for a job (idempotent in
// the calendar). A calendar failure is logged, not fatal, the aging roll-up still computes the window itself.
func (s *Service) registerReleaseDeadline(ctx context.Context, job Job) {
	if s.calendar == nil || job.SubstantialPerfDate == "" {
		return
	}
	perf, err := time.Parse("2006-01-02", job.SubstantialPerfDate)
	if err != nil {
		return
	}
	due := perf.AddDate(0, 0, releaseRuleDays).Format("2006-01-02")
	if err := s.calendar.RegisterDeadline(ctx, job.ID, "HOLDBACK_RELEASE", due, releaseRuleDays); err != nil {
		s.log.Warn("lien: register release deadline failed", "job_id", job.ID, "err", err)
	}
}

// - holdback sync (idempotent) -

// SyncHoldbacks books RETENTION rows into the holdback ledger for a lien job from billing's holdback-bearing
// invoices. It books ONLY the invoices raised on the customer job this lien job is linked to (RT-1's link),
// and never an invoice some lien job's ledger already holds, so a second lien job for the same customer
// can never re-book the first job's invoice. A lien job linked to no customer job books nothing.
//
// IDEMPOTENT: an invoice already held is skipped, so a re-run books nothing new. The amount is the
// invoice's open holdback exactly as 1150 holds it (billing lowers it as credits and write offs take their
// parts), never grossed up: the ledger never books more than 1150 holds. Retention was posted to 1150 at
// invoice.finalized, so no GL journal rides a RETENTION row (the 0038 CHECK only requires a journal on
// RELEASE). Since RT-2 an invoice that withholds is ledgered as it is written; the sync books the ones
// that were not (a holdback written before the lien job was linked).
func (s *Service) SyncHoldbacks(ctx context.Context, jobID string) (SyncResult, error) {
	if _, err := s.caller(ctx); err != nil {
		return SyncResult{}, err
	}
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return SyncResult{}, err
	}
	res := SyncResult{JobID: jobID}
	if s.holdback == nil || job.CustomerJobID == "" {
		return res, nil
	}
	invoices, err := s.holdback.InvoicesWithHoldback(ctx, job.BranchID)
	if err != nil {
		return SyncResult{}, fmt.Errorf("lien: read holdback invoices: %w", err)
	}
	held, err := s.store.HeldInvoiceIDs(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	for _, inv := range invoices {
		if inv.CustomerJobID != job.CustomerJobID {
			continue
		}
		if held[inv.InvoiceID] {
			res.SkippedCount++
			continue
		}
		amount := inv.HoldbackCents
		if amount <= 0 {
			res.SkippedCount++
			continue
		}
		// The guard is the ledger's own: the invoice's retention is booked once, however many syncs run.
		n, err := s.store.InsertRetentionOnce(ctx, HoldbackEntry{
			JobID:       jobID,
			InvoiceID:   inv.InvoiceID,
			BranchID:    job.BranchID,
			EntryType:   "RETENTION",
			AmountCents: amount,
			Reason:      "synced from invoice " + inv.InvoiceNo,
		})
		if err != nil {
			return SyncResult{}, err
		}
		if n == 0 {
			res.SkippedCount++
			held[inv.InvoiceID] = true
			continue
		}
		held[inv.InvoiceID] = true // guard against a duplicate invoice id inside the same batch
		res.BookedCount++
		res.BookedCents += amount
	}
	return res, nil
}

// - dual-gated release -

// ReleaseHoldbackApproved is the co-signed holdback release the HTTP route drives. It enforces the dual gate
// (gatekeeper = GENERAL_MANAGER + a Controller/Payables-Receivables four-eyes co-signer), refuses a PAYABLE
// direction (422, recordable but release is manual), posts the DR 2350 / CR 1150 journal through the GL
// provider, and records the RELEASE ledger row carrying that journal id (the 0038 CHECK forbids a journal-less
// release).
func (s *Service) ReleaseHoldbackApproved(ctx context.Context, jobID string, appr ReleaseApproval) (ReleaseResult, error) {
	return s.releaseHoldback(ctx, jobID, appr)
}

func (s *Service) releaseHoldback(ctx context.Context, jobID string, appr ReleaseApproval) (ReleaseResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return ReleaseResult{}, err
	}
	// Dual gate. Gatekeeper must be the General Manager; the co-signer must be a finance authority and differ
	// from the gatekeeper (four-eyes).
	if caller.RoleCode != roleGatekeeper {
		return ReleaseResult{}, forbidden("lien: holdback release gatekeeper must be %s", roleGatekeeper)
	}
	if appr.ApproverID == "" || appr.ApproverID == caller.UserID {
		return ReleaseResult{}, forbidden("lien: four-eyes, the co-signer must differ from the gatekeeper")
	}
	// The co-signer's authority is resolved SERVER-SIDE from identity, never trusted from the request body. A
	// gatekeeper could otherwise post a body claiming any co-signer id + role and self-satisfy the four-eyes
	// gate (S3 W6 B4). We assert the id resolves to an ACTIVE user who ACTUALLY holds a finance authority; the
	// body-asserted appr.ApproverRole is ignored and overwritten with the resolved role for the audit trail.
	if s.cosigner == nil {
		return ReleaseResult{}, clientErr(500, "lien: co-signer resolver not configured")
	}
	cosigner, err := s.cosigner.ResolveStaff(ctx, appr.ApproverID)
	if err != nil {
		return ReleaseResult{}, forbidden("lien: holdback release co-signer could not be verified")
	}
	if cosigner.Status != identity.StatusActive {
		return ReleaseResult{}, forbidden("lien: holdback release co-signer must be an active staff member")
	}
	if cosigner.RoleCode != roleController && cosigner.RoleCode != rolePayRec {
		return ReleaseResult{}, forbidden("lien: holdback release co-signer must be %s or %s", roleController, rolePayRec)
	}
	appr.ApproverRole = cosigner.RoleCode // record the VERIFIED role, not the client's claim

	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return ReleaseResult{}, err
	}
	// PAYABLE retention is recordable but its release is NOT posted here (manual, a scoped PARTIAL). 422.
	if job.Direction == dirPayable {
		return ReleaseResult{}, unprocessable("lien: PAYABLE holdback release is manual, recorded, not posted (register a calendar deadline)")
	}

	// A lien job linked to a customer job holds invoice level retention that RT-3's Controller release
	// moves with the invoice's column and the dated ledger. This job level route posts DR 2350 / CR 1150 and
	// writes a row the dated readers cannot see, so on such a job it would release the same holdback a
	// second time. It is refused in words naming RT-3's release.
	if job.CustomerJobID != "" {
		return ReleaseResult{}, unprocessable("lien: job %s is linked to a customer job, so its holdback is released by the Controller's holdback release (POST /api/v1/ar/holdback/releases), once its preservation period has run out, and not by this job level release", job.JobNo)
	}

	entries, err := s.store.HoldbackEntriesForJob(ctx, jobID)
	if err != nil {
		return ReleaseResult{}, err
	}
	// The ledger decides, not the link: a manager can unlink a lien job, and a job whose ledger holds
	// invoice level retention is released by RT-3 (which moves the invoice's column and the dated ledger
	// with it), so this job level route would release the same holdback a second time.
	for _, e := range entries {
		if e.InvoiceID != "" {
			return ReleaseResult{}, unprocessable("lien: job %s holds invoice level retention in its ledger, so its holdback is released by the Controller's holdback release (POST /api/v1/ar/holdback/releases) and not by this job level release", job.JobNo)
		}
	}
	outstanding := outstandingCents(entries)
	if outstanding <= 0 {
		return ReleaseResult{}, unprocessable("lien: no outstanding retention to release for this job")
	}
	if s.gl == nil {
		return ReleaseResult{}, clientErr(500, "lien: GL poster not configured")
	}

	// DR 2350 (holdback payable) / CR 1150 (holdback receivable), the frozen release pair. Idempotent on the
	// job (a replay returns the same journal from the GL, and the RELEASE row upsert below is guarded).
	journalID, err := s.gl.PostJournal(ctx, GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    jobID,
		IdempotencyKey: "holdback-release:" + jobID,
		Memo:           "Bill 60 holdback release, job " + job.JobNo,
		BranchID:       job.BranchID,
		Lines: []GLLine{
			{AccountCode: "2350", DebitCents: outstanding, Memo: "release holdback payable"},
			{AccountCode: "1150", CreditCents: outstanding, Memo: "release holdback receivable"},
		},
	})
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("lien: post release journal: %w", err)
	}

	releasedAt := s.clock().Format("2006-01-02")
	if _, err := s.store.InsertHoldbackEntry(ctx, HoldbackEntry{
		JobID:       jobID,
		BranchID:    job.BranchID,
		EntryType:   "RELEASE",
		AmountCents: outstanding,
		GLJournalID: journalID,
		ReleasedAt:  releasedAt,
		Reason:      fmt.Sprintf("released by %s, co-signed %s (%s)", caller.UserID, appr.ApproverID, appr.ApproverRole),
	}); err != nil {
		return ReleaseResult{}, err
	}

	if s.audit != nil {
		if aerr := s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     actHoldbackRelease,
			EntityType: "lien.holdback",
			EntityID:   jobID,
			ActorID:    caller.UserID,
			Reason:     fmt.Sprintf("co-signed by %s (%s)", appr.ApproverID, appr.ApproverRole),
			Extra:      map[string]any{"amount_cents": outstanding, "gl_journal_id": journalID},
		}); aerr != nil {
			return ReleaseResult{}, fmt.Errorf("lien: audit release: %w", aerr)
		}
	}

	return ReleaseResult{
		JobID:        jobID,
		AmountCents:  outstanding,
		GLJournalID:  journalID,
		ReleasedAt:   releasedAt,
		ApprovedBy:   appr.ApproverID,
		GatekeeperBy: caller.UserID,
	}, nil
}

// outstandingCents nets a job's ledger: ΣRETENTION − ΣRELEASE.
func outstandingCents(entries []HoldbackEntry) int64 {
	var total int64
	for _, e := range entries {
		switch e.EntryType {
		case "RETENTION":
			total += e.AmountCents
		case "RELEASE":
			total -= e.AmountCents
		}
	}
	return total
}

// - documents (render + version history) -

// CreateDocument renders a statute-driven, DRAFT-bannered Construction-Act document from its merge data,
// stamping the next version for that (job, template_kind), the version history is append-only.
func (s *Service) CreateDocument(ctx context.Context, in DocumentInput) (Document, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Document{}, err
	}
	if strings.TrimSpace(in.JobID) == "" {
		return Document{}, unprocessable("lien: job_id is required")
	}
	if !knownTemplate(in.TemplateKind) {
		return Document{}, unprocessable("lien: unknown template_kind %q", in.TemplateKind)
	}
	if _, err := s.store.GetJob(ctx, in.JobID); err != nil {
		return Document{}, err
	}
	version, err := s.store.NextDocumentVersion(ctx, in.JobID, in.TemplateKind)
	if err != nil {
		return Document{}, err
	}
	if in.Data == nil {
		in.Data = map[string]any{}
	}
	content, err := renderTemplate(in.TemplateKind, version, in.Data)
	if err != nil {
		return Document{}, err
	}
	doc := Document{
		JobID:           in.JobID,
		TemplateKind:    in.TemplateKind,
		Status:          "DRAFT",
		Version:         version,
		RenderedContent: content,
		Data:            in.Data,
		IsDraftBanner:   true, // data-driven DRAFT banner, M6 has no client sign-off yet (DIB-42)
	}
	return s.store.InsertDocument(ctx, doc, caller.UserID)
}

// ListDocuments returns the latest documents for a job.
func (s *Service) ListDocuments(ctx context.Context, jobID string) ([]Document, error) {
	return s.store.DocumentsForJob(ctx, jobID)
}

// DocumentVersions returns the full version history of one document (same job + template_kind).
func (s *Service) DocumentVersions(ctx context.Context, documentID string) ([]Document, error) {
	return s.store.DocumentVersions(ctx, documentID)
}

// - aging + triage -

// HoldbackAging computes the Bill 60 outstanding-holdback aging for a branch ("" = all).
func (s *Service) HoldbackAging(ctx context.Context, branchID string) ([]AgingRow, error) {
	entries, err := s.store.HoldbackLedger(ctx, branchID)
	if err != nil {
		return nil, err
	}
	facts, err := s.jobFacts(ctx, branchID)
	if err != nil {
		return nil, err
	}
	return buildAging(entries, facts, s.clock()), nil
}

// Triage computes the Monday triage worklist (aging rows badge-classified) for a branch ("" = all).
func (s *Service) Triage(ctx context.Context, branchID string) ([]TriageItem, error) {
	rows, err := s.HoldbackAging(ctx, branchID)
	if err != nil {
		return nil, err
	}
	return buildTriage(rows), nil
}

// jobFacts loads the per-job projection the roll-ups need.
func (s *Service) jobFacts(ctx context.Context, branchID string) (map[string]jobFacts, error) {
	jobs, err := s.store.ListJobs(ctx, branchID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]jobFacts, len(jobs))
	for _, j := range jobs {
		f := jobFacts{JobNo: j.JobNo, BranchID: j.BranchID, Direction: j.Direction, Linked: j.CustomerJobID != ""}
		if f.Linked {
			f.Hold = s.linkedHoldOf(ctx, j)
		}
		out[j.ID] = f
	}
	return out, nil
}

// linkedHoldOf reads a linked job's standing under RT-3's preservation rule from the job's own dates and the
// preserve days of the rule that governs its customer job. A rule that cannot be read leaves the job
// "no date" (the worklist then says it cannot be judged), never a guess.
func (s *Service) linkedHoldOf(ctx context.Context, j Job) linkedHold {
	if j.LienPreservedOn != "" && j.LienDischargedOn == "" {
		return linkedHold{State: holdLien}
	}
	parse := func(v string) *time.Time {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			return &t
		}
		return nil
	}
	_, start := preservationStart(parse(j.CSPPublishedOn), parse(j.SubstantialPerfDate), parse(j.LastSupplyDate))
	if start == "" {
		return linkedHold{State: holdNoDate}
	}
	rule, err := s.HoldbackRuleForJob(ctx, j.CustomerJobID)
	if err != nil {
		return linkedHold{State: holdNoDate}
	}
	base, err := time.Parse("2006-01-02", start)
	if err != nil {
		return linkedHold{State: holdNoDate}
	}
	releasable := base.AddDate(0, 0, rule.PreserveDays+1).Format("2006-01-02")
	if houseday.DefaultZone().Today(s.clock) < releasable {
		return linkedHold{State: holdWaiting, ReleasableOn: releasable}
	}
	return linkedHold{State: holdReleaseD, ReleasableOn: releasable}
}
