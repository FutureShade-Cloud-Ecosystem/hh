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

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// The audit verbs of the preserved lien door.
const (
	actLienPreserved  = "HOLDBACK_LIEN_PRESERVED"
	actLienDischarged = "HOLDBACK_LIEN_DISCHARGED"
	actLienDatesSet   = "HOLDBACK_LIEN_DATES_SET"
)

// Basis names the date a preservation period counts from.
const (
	BasisPublication            = "CSP_PUBLICATION"
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
	p.Basis, p.BasisDate = preservationStart(f.cspPublished, f.perf, f.lastSupply)
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
	jobNo, branchID                                           string
	cspPublished, perf, lastSupply, preservedOn, dischargedOn *time.Time
}

func (r *Repository) preservationFacts(ctx context.Context, jobID string) (preservationFacts, error) {
	q, err := r.q(ctx)
	if err != nil {
		return preservationFacts{}, err
	}
	var f preservationFacts
	err = q.QueryRow(ctx, `
		SELECT job_no::text, branch_id::text, csp_published_on, substantial_perf_date, last_supply_date, lien_preserved_on, lien_discharged_on
		  FROM lien_jobs WHERE id = $1::uuid`, jobID).
		Scan(&f.jobNo, &f.branchID, &f.cspPublished, &f.perf, &f.lastSupply, &f.preservedOn, &f.dischargedOn)
	if err != nil {
		return preservationFacts{}, fmt.Errorf("lien: read the job's preservation facts: %w", err)
	}
	return f, nil
}

// preservationStart is THE RULE for the date a job's preservation period counts from: the LATEST of the dates
// the job carries that can start it, the publication of the certificate of substantial performance, the date
// of substantial performance, and the last supply. A later start means a later end, so a release is never
// early. The Act's reading differs by claimant (the period runs from publication for some, from the earlier
// of publication and last supply for a supplier); this is the conservative union of them and AWAITS
// COUNSEL'S CONFIRMATION. If counsel names a different rule, it changes here and nowhere else.
// Basis names the date that won ("" date and BasisNone when the job carries none).
func preservationStart(published, performance, lastSupply *time.Time) (basis, date string) {
	var best *time.Time
	pick := func(t *time.Time, name string) {
		// Ties keep the earlier-listed basis: publication, then performance, then last supply.
		if t != nil && (best == nil || t.After(*best)) {
			best, basis = t, name
		}
	}
	pick(published, BasisPublication)
	pick(performance, BasisSubstantialPerformance)
	pick(lastSupply, BasisCompletion)
	if best == nil {
		return BasisNone, ""
	}
	return basis, dateStr(best)
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
	Reversed    bool // the release was reversed (it no longer stands)
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
		SELECT id::text, invoice_id::text, amount_cents, reversed_at IS NOT NULL FROM holdback_ledger
		 WHERE entry_type = 'RELEASE' AND invoice_id IS NOT NULL AND reason = $1
		 ORDER BY created_at, id`, releaseReasonPrefix+key)
	if err != nil {
		return nil, fmt.Errorf("lien: read the releases of a key: %w", err)
	}
	defer rows.Close()
	var out []ReleasedRow
	for rows.Next() {
		var r ReleasedRow
		if err := rows.Scan(&r.ID, &r.InvoiceID, &r.AmountCents, &r.Reversed); err != nil {
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
	if err := q.QueryRow(ctx, `SELECT count(*) FROM holdback_ledger WHERE job_id = $1::uuid AND entry_type = 'RELEASE' AND invoice_id IS NOT NULL AND reversed_at IS NULL`, jobID).Scan(&n); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("lien: count the job's releases: %w", err)
	}
	return n > 0, nil
}

// HasJobLevelRelease answers whether the old job level route already released holdback that RT-3 is about to
// release again: a job level RELEASE row (no invoice) on the lien job linked to the customer job, or on ANY
// lien job whose ledger holds one of the invoices about to be released. The two can be different jobs (a
// manager can unlink a lien job and link another), and the release books against the job that holds the
// invoice, so the guard keys on the ledger and not only on the link.
func (s *Service) HasJobLevelRelease(ctx context.Context, customerJobID string, invoiceIDs []string) (bool, error) {
	q, err := s.repo.q(ctx)
	if err != nil {
		return false, err
	}
	var linked *string
	if id, ok, err := s.repo.LinkedLienJob(ctx, customerJobID); err != nil {
		return false, err
	} else if ok {
		linked = &id
	}
	var n int
	if err := q.QueryRow(ctx, `
		SELECT count(*) FROM holdback_ledger
		 WHERE entry_type = 'RELEASE' AND invoice_id IS NULL
		   AND (job_id = $1::uuid
		        OR job_id IN (SELECT job_id FROM holdback_ledger
		                       WHERE invoice_id = ANY($2::uuid[]) AND entry_type = 'RETENTION' AND amount_cents > 0))`,
		linked, invoiceIDs).Scan(&n); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("lien: count the job level releases: %w", err)
	}
	return n > 0, nil
}

// lienDay parses a request's house day (YYYY-MM-DD).
func lienDay(what, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", unprocessable("lien: %s is required (a date, YYYY-MM-DD)", what)
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", unprocessable("lien: %s %q is not a date (YYYY-MM-DD)", what, v)
	}
	return v, nil
}

// RecordLienPreserved records that a lien was preserved against a lien job, on a date, with a note: it
// blocks the release of the job's holdback until discharged. A manager inside the job's branch reach; the
// act and its audit record are one transaction. Recording again replaces the date and note and clears a
// discharge (a new preservation).
func (s *Service) RecordLienPreserved(ctx context.Context, lienJobID, preservedOn, note string) (Job, error) {
	c, job, err := s.preservedLienCaller(ctx, lienJobID)
	if err != nil {
		return Job{}, err
	}
	day, err := lienDay("preserved_on", preservedOn)
	if err != nil {
		return Job{}, err
	}
	note = strings.TrimSpace(note)
	if err := s.rules().InTx(ctx, func(ctx context.Context) error {
		q, err := s.repo.q(ctx)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `
			UPDATE lien_jobs SET lien_preserved_on = $2::date, lien_preserved_note = $3, lien_discharged_on = NULL, updated_at = now()
			 WHERE id = $1::uuid`, job.ID, day, note); err != nil {
			return fmt.Errorf("lien: record the preserved lien: %w", err)
		}
		return s.record(ctx, audit.SensitiveAccess{
			Action: actLienPreserved, EntityType: "lien.job", EntityID: job.ID, ActorID: c.UserID, BranchID: job.BranchID,
			Reason: note, Extra: map[string]any{"preserved_on": day},
		})
	}); err != nil {
		return Job{}, err
	}
	return s.store.GetJob(ctx, job.ID)
}

// DischargeLien records that a preserved lien was discharged (vacated, paid or settled), on a date, with a
// note: the block on the job's holdback release lifts. A lien never preserved has nothing to discharge.
func (s *Service) DischargeLien(ctx context.Context, lienJobID, dischargedOn, note string) (Job, error) {
	c, job, err := s.preservedLienCaller(ctx, lienJobID)
	if err != nil {
		return Job{}, err
	}
	if job.LienPreservedOn == "" {
		return Job{}, unprocessable("lien: no lien was preserved on job %s, so there is nothing to discharge", job.JobNo)
	}
	day, err := lienDay("discharged_on", dischargedOn)
	if err != nil {
		return Job{}, err
	}
	if day < job.LienPreservedOn {
		return Job{}, unprocessable("lien: a lien preserved on %s cannot be discharged on %s, before it", job.LienPreservedOn, day)
	}
	note = strings.TrimSpace(note)
	if err := s.rules().InTx(ctx, func(ctx context.Context) error {
		q, err := s.repo.q(ctx)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE lien_jobs SET lien_discharged_on = $2::date, updated_at = now() WHERE id = $1::uuid`, job.ID, day); err != nil {
			return fmt.Errorf("lien: record the discharge: %w", err)
		}
		return s.record(ctx, audit.SensitiveAccess{
			Action: actLienDischarged, EntityType: "lien.job", EntityID: job.ID, ActorID: c.UserID, BranchID: job.BranchID,
			Reason: note, Extra: map[string]any{"discharged_on": day, "preserved_on": job.LienPreservedOn},
		})
	}); err != nil {
		return Job{}, err
	}
	return s.store.GetJob(ctx, job.ID)
}

// PreservationDates are the dates that start a lien job's preservation period. A nil field is left as it is;
// a field present with Clear set removes the date; a field with a value sets it.
type PreservationDates struct {
	CSPPublishedOn      DateChange
	SubstantialPerfDate DateChange
	LastSupplyDate      DateChange
}

// DateChange is one date's change: untouched (the zero value), cleared, or set to a YYYY-MM-DD day.
type DateChange struct {
	Present bool
	Value   string // empty with Present set means clear
}

// SetPreservationDates sets or corrects the dates a lien job's preservation period counts from: the
// publication of the certificate of substantial performance (known only after the job is opened), the date
// of substantial performance and the last supply. A manager inside the job's branch reach; the change and
// its audit record (the dates before and after) are one transaction.
func (s *Service) SetPreservationDates(ctx context.Context, lienJobID string, in PreservationDates) (Job, error) {
	c, job, err := s.preservedLienCaller(ctx, lienJobID)
	if err != nil {
		return Job{}, err
	}
	if !in.CSPPublishedOn.Present && !in.SubstantialPerfDate.Present && !in.LastSupplyDate.Present {
		return Job{}, unprocessable("lien: name at least one of csp_published_on, substantial_perf_date and last_supply_date (a date, YYYY-MM-DD, or null to clear it)")
	}
	type col struct {
		name, column string
		change       DateChange
		before       string
	}
	cols := []col{
		{"csp_published_on", "csp_published_on", in.CSPPublishedOn, job.CSPPublishedOn},
		{"substantial_perf_date", "substantial_perf_date", in.SubstantialPerfDate, job.SubstantialPerfDate},
		{"last_supply_date", "last_supply_date", in.LastSupplyDate, job.LastSupplyDate},
	}
	extra := map[string]any{}
	for i := range cols {
		if !cols[i].change.Present {
			continue
		}
		v := strings.TrimSpace(cols[i].change.Value)
		if v != "" {
			day, err := lienDay(cols[i].name, v)
			if err != nil {
				return Job{}, err
			}
			v = day
		}
		cols[i].change.Value = v
		extra[cols[i].name+"_before"] = cols[i].before
		extra[cols[i].name+"_after"] = v
	}
	if err := s.rules().InTx(ctx, func(ctx context.Context) error {
		q, err := s.repo.q(ctx)
		if err != nil {
			return err
		}
		for _, cl := range cols {
			if !cl.change.Present {
				continue
			}
			// The column names are this function's own constants, never a request's.
			if _, err := q.Exec(ctx, `UPDATE lien_jobs SET `+cl.column+` = $2::date, updated_at = now() WHERE id = $1::uuid`,
				job.ID, nullDate(cl.change.Value)); err != nil {
				return fmt.Errorf("lien: set %s: %w", cl.name, err)
			}
		}
		return s.record(ctx, audit.SensitiveAccess{
			Action: actLienDatesSet, EntityType: "lien.job", EntityID: job.ID, ActorID: c.UserID, BranchID: job.BranchID,
			Reason: "preservation dates set", Extra: extra,
		})
	}); err != nil {
		return Job{}, err
	}
	return s.store.GetJob(ctx, job.ID)
}

func (s *Service) preservedLienCaller(ctx context.Context, lienJobID string) (c identity.StaffContext, job Job, err error) {
	c, err = s.managerCaller(ctx)
	if err != nil {
		return c, Job{}, err
	}
	job, err = s.store.GetJob(ctx, lienJobID)
	if err != nil {
		return c, Job{}, err
	}
	if !c.CanAccessBranch(job.BranchID) {
		return c, Job{}, forbidden("lien: that lien job sits at a branch outside your reach")
	}
	return c, job, nil
}

// ReleaseRow is an invoice level RELEASE row as the reversal reads it.
type ReleaseRow struct {
	ID          string
	InvoiceID   string
	BranchID    string
	AmountCents int64
	Reversed    bool
}

// ReleaseRowByID answers an invoice level RELEASE row, ok false when there is none.
func (s *Service) ReleaseRowByID(ctx context.Context, releaseID string) (ReleaseRow, bool, error) {
	q, err := s.repo.q(ctx)
	if err != nil {
		return ReleaseRow{}, false, err
	}
	var r ReleaseRow
	err = q.QueryRow(ctx, `
		SELECT id::text, invoice_id::text, branch_id::text, amount_cents, reversed_at IS NOT NULL
		  FROM holdback_ledger WHERE id = $1::uuid AND entry_type = 'RELEASE' AND invoice_id IS NOT NULL`, releaseID).
		Scan(&r.ID, &r.InvoiceID, &r.BranchID, &r.AmountCents, &r.Reversed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseRow{}, false, nil
	}
	if err != nil {
		return ReleaseRow{}, false, fmt.Errorf("lien: read a release row: %w", err)
	}
	return r, true, nil
}

// MarkReleaseReversed marks an invoice level RELEASE row reversed (it no longer counts as the invoice's
// one release) and answers whether it changed: false when it was already reversed. The row stays as history.
func (s *Service) MarkReleaseReversed(ctx context.Context, releaseID string) (bool, error) {
	q, err := s.repo.q(ctx)
	if err != nil {
		return false, err
	}
	tag, err := q.Exec(ctx, `
		UPDATE holdback_ledger SET reversed_at = now()
		 WHERE id = $1::uuid AND entry_type = 'RELEASE' AND invoice_id IS NOT NULL AND reversed_at IS NULL`, releaseID)
	if err != nil {
		return false, fmt.Errorf("lien: mark the release reversed: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
