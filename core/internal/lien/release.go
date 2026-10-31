package lien

// release.go, the lien half of RT-3 (wave 3): when a job's holdback falls due, and the ledger row of an
// invoice's release.
//
// WHEN IT FALLS DUE, by the Ontario Construction Act's reading. A holdback is retained until the lien
// preservation period has run out with no lien preserved. The period is the province rule's
// PreserveDays (60 for Ontario, holdback_province_rules), counted from the lien job's substantial
// performance date (the date the certificate or declaration of substantial performance fixes), else its
// last supply date (the completion of the supply, for a job with no such date). The LAST DAY to preserve a
// lien is that date plus PreserveDays; the holdback is RELEASABLE from the next house day, never on the
// last day, because a lien preserved late that evening is still in time. A lien preserved and not
// discharged blocks the release whatever the dates say. A job with neither date is never due. Dates are
// the dealer's house days, never UTC: the callers compare them to the house day.
//
// The ledger row (BookRelease) is one RELEASE row per invoice, written on the caller's transaction. Its
// journal (DR 1100 / CR 1150) rides the outbox in that same transaction (the row says so), so billing,
// which owns the invoice, lowers the column and emits the event beside this row.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Basis names the date a preservation period counts from.
const (
	BasisSubstantialPerformance = "SUBSTANTIAL_PERFORMANCE"
	BasisCompletion             = "COMPLETION"
	BasisNone                   = "NONE"
)

// Preservation is what decides whether a customer job's holdback is due: the linked lien job, the date its
// period counts from, and the day the period ends. Dates are house days (YYYY-MM-DD), "" when unknown.
type Preservation struct {
	LienJobID         string
	LienJobNo         string
	BranchID          string
	Basis             string // BasisSubstantialPerformance | BasisCompletion | BasisNone
	BasisDate         string
	PreserveDays      int
	LastDayToPreserve string // BasisDate + PreserveDays
	ReleasableOn      string // the day after LastDayToPreserve
	LienPreserved     bool   // preserved and not discharged
	LienPreservedOn   string
}

// PreservationFor answers the preservation facts of the lien job linked to a customer job, ok false when
// none is linked. It reads on the caller's transaction when one rides ctx.
func (s *Service) PreservationFor(ctx context.Context, customerJobID string) (Preservation, bool, error) {
	jobID, ok, err := s.repo.LinkedLienJob(ctx, customerJobID)
	if err != nil || !ok {
		return Preservation{}, false, err
	}
	f, err := s.repo.preservationFacts(ctx, jobID)
	if err != nil {
		return Preservation{}, false, err
	}
	rule, err := s.HoldbackRuleForJob(ctx, customerJobID)
	if err != nil {
		return Preservation{}, false, err
	}
	p := Preservation{
		LienJobID: jobID, LienJobNo: f.jobNo, BranchID: f.branchID, Basis: BasisNone, PreserveDays: rule.PreserveDays,
		LienPreserved: f.preservedOn != nil && f.dischargedOn == nil, LienPreservedOn: dateStr(f.preservedOn),
	}
	switch {
	case f.perf != nil:
		p.Basis, p.BasisDate = BasisSubstantialPerformance, dateStr(f.perf)
	case f.lastSupply != nil:
		p.Basis, p.BasisDate = BasisCompletion, dateStr(f.lastSupply)
	}
	if p.BasisDate != "" {
		base, err := time.Parse("2006-01-02", p.BasisDate)
		if err != nil {
			return Preservation{}, false, fmt.Errorf("lien: the job's preservation date %q: %w", p.BasisDate, err)
		}
		last := base.AddDate(0, 0, rule.PreserveDays)
		p.LastDayToPreserve = last.Format("2006-01-02")
		p.ReleasableOn = last.AddDate(0, 0, 1).Format("2006-01-02")
	}
	return p, true, nil
}

type preservationFacts struct {
	jobNo, branchID                             string
	perf, lastSupply, preservedOn, dischargedOn *time.Time
}

func (r *Repository) preservationFacts(ctx context.Context, jobID string) (preservationFacts, error) {
	q, err := r.q(ctx)
	if err != nil {
		return preservationFacts{}, err
	}
	var f preservationFacts
	err = q.QueryRow(ctx, `
		SELECT job_no::text, branch_id::text, substantial_perf_date, last_supply_date, lien_preserved_on, lien_discharged_on
		  FROM lien_jobs WHERE id = $1::uuid`, jobID).
		Scan(&f.jobNo, &f.branchID, &f.perf, &f.lastSupply, &f.preservedOn, &f.dischargedOn)
	if err != nil {
		return preservationFacts{}, fmt.Errorf("lien: read the job's preservation facts: %w", err)
	}
	return f, nil
}

// ReleaseBooking is one invoice's release the caller asks the ledger to hold.
type ReleaseBooking struct {
	InvoiceID   string
	BranchID    string
	AmountCents int64  // positive
	Key         string // the request's idempotency key, the row's reason
	ReleasedOn  string // the house day, YYYY-MM-DD
	ActorID     string
}

// ReleasedRow is a RELEASE row as the release answers it.
type ReleasedRow struct {
	ID          string
	InvoiceID   string
	AmountCents int64
}

const releaseReasonPrefix = "holdback release "

// BookRelease writes the RELEASE row of one invoice on the lien job whose ledger holds the invoice, on the
// caller's transaction, and answers the row's id. The invoice must be held by a job; one release per
// invoice is the table's own rule (the unique index), so a second raises rather than doubles.
func (s *Service) BookRelease(ctx context.Context, b ReleaseBooking) (string, error) {
	if b.AmountCents <= 0 {
		return "", fmt.Errorf("lien: a release is a positive amount")
	}
	if strings.TrimSpace(b.Key) == "" {
		return "", fmt.Errorf("lien: a release names its idempotency key")
	}
	jobID, ok, err := s.repo.JobHoldingInvoice(ctx, b.InvoiceID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("lien: no lien job's ledger holds invoice %s, so there is nothing to release", b.InvoiceID)
	}
	q, err := s.repo.q(ctx)
	if err != nil {
		return "", err
	}
	var id string
	err = q.QueryRow(ctx, `
		INSERT INTO holdback_ledger (job_id, invoice_id, branch_id, entry_type, amount_cents, released_at, reason, journal_via_outbox, created_by)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'RELEASE', $4, $5::date, $6, TRUE, $7)
		RETURNING id::text`,
		jobID, b.InvoiceID, b.BranchID, b.AmountCents, b.ReleasedOn, releaseReasonPrefix+b.Key, nullUUID(b.ActorID)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("lien: book the release of invoice %s: %w", b.InvoiceID, err)
	}
	return id, nil
}

// ReleasesForKey answers the RELEASE rows a request key wrote, oldest first (the replay's first result).
func (s *Service) ReleasesForKey(ctx context.Context, key string) ([]ReleasedRow, error) {
	q, err := s.repo.q(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT id::text, invoice_id::text, amount_cents FROM holdback_ledger
		 WHERE entry_type = 'RELEASE' AND invoice_id IS NOT NULL AND reason = $1
		 ORDER BY created_at, id`, releaseReasonPrefix+key)
	if err != nil {
		return nil, fmt.Errorf("lien: read the releases of a key: %w", err)
	}
	defer rows.Close()
	var out []ReleasedRow
	for rows.Next() {
		var r ReleasedRow
		if err := rows.Scan(&r.ID, &r.InvoiceID, &r.AmountCents); err != nil {
			return nil, fmt.Errorf("lien: scan a release: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HasReleaseOnJob answers whether any invoice release was ever booked on the lien job linked to a customer
// job (the "already released" refusal's evidence).
func (s *Service) HasReleaseOnJob(ctx context.Context, customerJobID string) (bool, error) {
	jobID, ok, err := s.repo.LinkedLienJob(ctx, customerJobID)
	if err != nil || !ok {
		return false, err
	}
	q, err := s.repo.q(ctx)
	if err != nil {
		return false, err
	}
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM holdback_ledger WHERE job_id = $1::uuid AND entry_type = 'RELEASE' AND invoice_id IS NOT NULL`, jobID).Scan(&n); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("lien: count the job's releases: %w", err)
	}
	return n > 0, nil
}
