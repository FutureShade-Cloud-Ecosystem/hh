package lien

import "time"

// model.go, the lien DTOs (JSON-serialized) + the input structs. Money is integer cents (BIGINT); dates are
// ISO-8601 strings ("" when null). These are the shapes the handler emits + the store round-trips.

// Job is a Construction-Act "improvement": the umbrella a holdback + release cycle attaches to.
type Job struct {
	ID                  string `json:"id"`
	JobNo               string `json:"job_no"`
	CustomerID          string `json:"customer_id,omitempty"`
	CustomerJobID       string `json:"customer_job_id,omitempty"` // the customer job this lien job links to (RT-1)
	BranchID            string `json:"branch_id"`
	Description         string `json:"description"`
	ContractCents       int64  `json:"contract_cents"`
	HoldbackPctBps      int    `json:"holdback_pct_bps"`
	SubstantialPerfDate string `json:"substantial_perf_date,omitempty"`
	LastSupplyDate      string `json:"last_supply_date,omitempty"`
	CSPPublishedOn      string `json:"csp_published_on,omitempty"` // the certificate of substantial performance's publication date, when known (RT-3 fix)
	LienPreservedOn     string `json:"lien_preserved_on,omitempty"`
	LienPreservedNote   string `json:"lien_preserved_note,omitempty"`
	LienDischargedOn    string `json:"lien_discharged_on,omitempty"`
	Status              string `json:"status"`
	Direction           string `json:"holdback_direction"` // RECEIVABLE | PAYABLE (from metadata)
	HSTOnHoldback       bool   `json:"hst_on_holdback"`    // config flag (from metadata)
	CreatedAt           string `json:"created_at,omitempty"`
}

// JobInput is the create-job payload.
type JobInput struct {
	JobNo               string `json:"job_no"`
	CustomerID          string `json:"customer_id"`
	BranchID            string `json:"branch_id"`
	Description         string `json:"description"`
	ContractCents       int64  `json:"contract_cents"`
	HoldbackPctBps      int    `json:"holdback_pct_bps"`
	SubstantialPerfDate string `json:"substantial_perf_date"`
	LastSupplyDate      string `json:"last_supply_date"`
	CSPPublishedOn      string `json:"csp_published_on"` // optional
	Direction           string `json:"holdback_direction"`
	HSTOnHoldback       bool   `json:"hst_on_holdback"`
}

// Document is a rendered, versioned, DRAFT-bannered Construction-Act document.
type Document struct {
	ID              string         `json:"id"`
	JobID           string         `json:"job_id"`
	TemplateKind    string         `json:"template_kind"`
	Status          string         `json:"status"`
	Version         int            `json:"version"`
	RenderedContent string         `json:"rendered_content"`
	Data            map[string]any `json:"data"`
	IsDraftBanner   bool           `json:"is_draft_banner"`
	CreatedAt       string         `json:"created_at,omitempty"`
}

// DocumentInput is the render-document payload. Data supplies the merge fields; the server renders the
// statute-driven template and stamps the next version.
type DocumentInput struct {
	JobID        string         `json:"job_id"`
	TemplateKind string         `json:"template_kind"`
	Data         map[string]any `json:"data"`
}

// HoldbackEntry is one holdback_ledger row (RETENTION or RELEASE).
type HoldbackEntry struct {
	ID          string    `json:"id"`
	JobID       string    `json:"job_id"`
	InvoiceID   string    `json:"invoice_id,omitempty"`
	BranchID    string    `json:"branch_id"`
	EntryType   string    `json:"entry_type"`
	AmountCents int64     `json:"amount_cents"`
	GLJournalID string    `json:"gl_journal_id,omitempty"`
	ReleasedAt  string    `json:"released_at,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// SyncResult reports an idempotent holdback sync (RETENTION rows booked from billing's holdback invoices).
type SyncResult struct {
	JobID        string `json:"job_id"`
	BookedCount  int    `json:"booked_count"`
	SkippedCount int    `json:"skipped_count"`
	BookedCents  int64  `json:"booked_cents"`
}

// ReleaseApproval is the four-eyes co-signer the dual-gate release requires (the HTTP route supplies it).
type ReleaseApproval struct {
	ApproverID   string `json:"approver_id"`
	ApproverRole string `json:"approver_role"`
}

// ReleaseResult is the outcome of a holdback release.
type ReleaseResult struct {
	JobID        string `json:"job_id"`
	AmountCents  int64  `json:"amount_cents"`
	GLJournalID  string `json:"gl_journal_id"`
	ReleasedAt   string `json:"released_at"`
	ApprovedBy   string `json:"approved_by"`
	GatekeeperBy string `json:"gatekeeper_by"`
}

// AgingRow is one job's outstanding holdback, aged into a bucket (the Bill 60 aging surface).
type AgingRow struct {
	JobID            string `json:"job_id"`
	JobNo            string `json:"job_no"`
	BranchID         string `json:"branch_id"`
	Direction        string `json:"holdback_direction"`
	OutstandingCents int64  `json:"outstanding_cents"`
	OldestDays       int    `json:"oldest_days"`
	Bucket           string `json:"bucket"`
	ReleaseEligible  bool   `json:"release_eligible"` // RECEIVABLE + past the 45-day rule window, and not linked to a customer job
	linked           bool   // linked to a customer job: released by RT-3, so the old rule's eligibility does not apply
}

// TriageItem is one Monday-triage worklist row, a job needing attention, badge-classified.
type TriageItem struct {
	JobID            string `json:"job_id"`
	JobNo            string `json:"job_no"`
	BranchID         string `json:"branch_id"`
	Direction        string `json:"holdback_direction"`
	OutstandingCents int64  `json:"outstanding_cents"`
	OldestDays       int    `json:"oldest_days"`
	Badge            string `json:"badge"` // RELEASE_DUE | OVERDUE | AGING | PAYABLE_MANUAL | OK
	Note             string `json:"note"`
}
