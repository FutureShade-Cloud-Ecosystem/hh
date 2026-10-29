package customer

// job_model.go, the job account under a customer (wave 3 JA-1, decision 1.1 A).
//
// A job is a site of work under one customer: it carries its own ship to address, PO reference and
// contact, and may carry its own credit limit and payment terms. THE TWO NULLABLE FIGURES ARE THE
// WHOLE POINT: a job with no credit_limit_cents and no terms_days shares the CUSTOMER's figures, and
// null is not zero. A job that read as zero credit because nobody filled a field in would be
// refused at the counter for a limit nobody set.
//
// The split (FIN-WAVE3-PLAN 1.1) is one sentence: a job shares the customer's limit and terms
// unless it carries its own; the customer's exposure includes every job; a job limit never raises
// the customer's. The rules live in job_service.go, the facts live here.

import (
	"bytes"
	"encoding/json"
	"time"
)

// The two statuses a job may hold. A CLOSED job takes no new document (job_service.go), which is
// why the set is this small and why the table's CHECK carries the same two words.
const (
	// JobStatusOpen is a job that may still take documents.
	JobStatusOpen = "OPEN"
	// JobStatusClosed is a job that is finished: it reads, it reports, and it takes no new document.
	JobStatusClosed = "CLOSED"
)

// Job is one customer_jobs row: the job account's own facts.
//
// CreditLimitCents and TermsDays are the job's OWN figures and are nil when the job shares the
// customer's. They are pointers for the same reason CreateInput.PaymentTermsDays is: absence and
// zero are different accounts, and a job that reads as Cash On Demand because nobody filled the
// terms in would date its invoices on issue.
type Job struct {
	JobID      string `json:"job_id"`
	CustomerID string `json:"customer_id"`
	// BranchID is the branch the job sits at, and the branch the staff reads are fenced by: a job
	// of another branch is not found by a caller whose grants do not reach it.
	BranchID string `json:"branch_id"`
	JobNo    string `json:"job_no"`
	Name     string `json:"name"`
	// ShipToAddress is the job's own delivery address (a JSONB object), kept apart from the
	// customer's billing address: a job delivers to the site, the account is billed at the office.
	ShipToAddress map[string]any `json:"ship_to_address"`
	POReference   string         `json:"po_reference"`
	ContactName   string         `json:"contact_name"`
	ContactEmail  string         `json:"contact_email"`
	ContactPhone  string         `json:"contact_phone"`
	// CreditLimitCents is the job's own credit ceiling, nil when the job shares the customer's.
	CreditLimitCents *int64 `json:"credit_limit_cents"`
	// TermsDays is the job's own payment terms, nil when the job shares the customer's.
	TermsDays *int `json:"terms_days,omitempty"`
	// EffectiveTermsDays is the terms the job is dated by: its own when it carries any, the
	// customer's when it does not. It is computed in the read and never stored: the customer may
	// edit their own terms later, and a job that shares them follows.
	EffectiveTermsDays int    `json:"effective_terms_days"`
	Status             string `json:"status"`
	// SubstantialPerformanceDate and LastSupplyDate are the job's own lien dates, kept for wave 3
	// JA-2. lien_jobs (migration 0038) is left alone; JA-2 links the two.
	SubstantialPerformanceDate *time.Time `json:"substantial_performance_date,omitempty"`
	LastSupplyDate             *time.Time `json:"last_supply_date,omitempty"`
	CreatedBy                  string     `json:"created_by"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// JobCreateInput is the create payload for a job under a customer.
//
// BranchID is the branch the job sits at, and it is only needed for a customer with NO home branch:
// a job under a customer that is homed at a branch sits at that branch, and a customer with no home
// branch is company-wide, so its job must say where it sits.
//
// CreditLimitCents and TermsDays are the job's own figures and are nil when the job shares the
// customer's. A job's own limit may never exceed the customer's (job_service.go), and the ceiling
// is refused in words.
type JobCreateInput struct {
	JobNo            string         `json:"job_no"`
	Name             string         `json:"name"`
	BranchID         string         `json:"branch_id"`
	ShipToAddress    map[string]any `json:"ship_to_address"`
	POReference      string         `json:"po_reference"`
	ContactName      string         `json:"contact_name"`
	ContactEmail     string         `json:"contact_email"`
	ContactPhone     string         `json:"contact_phone"`
	CreditLimitCents *int64         `json:"credit_limit_cents"`
	TermsDays        *int           `json:"terms_days"`
	// SubstantialPerformanceDate and LastSupplyDate are plain strings on the wire (a date is
	// YYYY-MM-DD), empty when the job carries none.
	SubstantialPerformanceDate string `json:"substantial_performance_date"`
	LastSupplyDate             string `json:"last_supply_date"`
}

// JobEditInput is the edit payload for a job the customer already holds.
//
// THE EDIT IS A PARTIAL ONE: a field left out keeps the job's own value. The two nullable figures
// and the two lien dates carry the one exception an ordinary pointer cannot: a field left out
// keeps the job's own value, and an explicit null CLEARS the job's own figure so the job shares
// the customer's again (or drops the lien date). OptionalValue is what tells the two apart.
type JobEditInput struct {
	Name          *string        `json:"name"`
	ShipToAddress map[string]any `json:"ship_to_address"`
	POReference   *string        `json:"po_reference"`
	ContactName   *string        `json:"contact_name"`
	ContactEmail  *string        `json:"contact_email"`
	ContactPhone  *string        `json:"contact_phone"`
	// CreditLimitCents and TermsDays are the job's own figures. Absent keeps them, null clears them.
	CreditLimitCents OptionalValue[int64] `json:"credit_limit_cents"`
	TermsDays        OptionalValue[int]   `json:"terms_days"`
	// SubstantialPerformanceDate and LastSupplyDate are the job's own lien dates. Absent keeps
	// them, null clears them.
	SubstantialPerformanceDate OptionalValue[string] `json:"substantial_performance_date"`
	LastSupplyDate             OptionalValue[string] `json:"last_supply_date"`
}

// OptionalValue is a value the edit may set, clear, or leave alone.
//
// It exists because encoding/json reads an absent field and an explicit null the same way for a
// plain pointer, and the job edit needs the two apart: a field left out keeps the job's own value,
// and an explicit null on the job's own credit limit or terms clears it so the job shares the
// customer's again. Present says which of the two happened; Value carries the new value, nil when
// the field was cleared.
type OptionalValue[T any] struct {
	Present bool
	Value   *T
}

// UnmarshalJSON reads a present field, null or not. An absent field never reaches this method, so
// Present stays false and the job keeps its own value.
func (o *OptionalValue[T]) UnmarshalJSON(b []byte) error {
	o.Present = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// Set reports the field was present in the body and carries the new value.
func (o OptionalValue[T]) Set() bool { return o.Present }

// Cleared reports the field was present in the body and was null: the job's own value goes and
// the job shares the customer's again.
func (o OptionalValue[T]) Cleared() bool { return o.Present && o.Value == nil }

// Or returns the new value when the field was set to one, and the job's own value otherwise.
func (o OptionalValue[T]) Or(current *T) *T {
	if !o.Present {
		return current
	}
	return o.Value
}
