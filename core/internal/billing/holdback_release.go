package billing

// holdback_release.go, wave 3 RT-3: what is due to release, and the release.
//
// -- WHEN A HOLDBACK IS DUE ------------------------------------------------------------------------
// The lien module decides (lien.Preservation, release.go there states the Ontario Construction Act's
// reading): the holdback of a job falls due on the house day AFTER the last day to preserve a lien, which
// is the lien job's substantial performance date (else its last supply date) plus the province rule's
// preserve days, and never while a lien is preserved and undischarged. This file only asks and refuses
// in the lien module's words, comparing the dates to the dealer's house day.
//
// -- THE RELEASE -----------------------------------------------------------------------------------
// One action, for one customer job, for a Controller. Under the job's advisory lock, for each open
// invoice on the job that still withholds, in ONE transaction:
//   - the invoice row is locked, its column is checked against the dated ledger (they tie or the
//     release stops), and lowerHoldbackAndLedger moves the column to zero and books the invoice's own
//     RELEASE row together (never a job level row with no invoice, which the dated readers cannot see);
//   - an outbox event ar.holdback.released rides the same transaction; the GL consumer posts DR 1100 /
//     CR 1150 on the release row's key.
// The customer is NOT billed on a new invoice. The invoice already carries the whole amount in
// balance_cents, and the aging, the statement and the payment doors read balance less holdback, so the
// released amount simply joins the net the customer owes on the same invoice, the same due date and the
// same statement line, and GL 1100 grows by the same cents. A second invoice would count the amount twice.
// The aging's dated readers see the release from its row's own instant (invoice_holdback_as_of), so last
// month's figures do not move.
//
// -- REFUSALS --------------------------------------------------------------------------------------
// Before the due day, on a preserved lien, with no date to count from, with nothing held, a second time
// and from a desk that is not the Controller's, each in words (HoldbackReleaseRefused). A refusal writes
// nothing. The request key makes a retry answer the first result.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/lien"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// The refusal kinds a release answers.
const (
	RefusedNotDue          = "NOT_DUE"
	RefusedLienPreserved   = "LIEN_PRESERVED"
	RefusedNoDate          = "NO_DATE"
	RefusedNoLienJob       = "NO_LIEN_JOB"
	RefusedNothingHeld     = "NOTHING_HELD"
	RefusedAlreadyReleased = "ALREADY_RELEASED"
	RefusedNotController   = "NOT_CONTROLLER"
	RefusedKeyReused       = "KEY_REUSED"
	// The reversal's refusals.
	RefusedOutsideBranches   = "OUTSIDE_BRANCHES"
	RefusedReleaseNotFound   = "RELEASE_NOT_FOUND"
	RefusedAlreadyReversed   = "ALREADY_REVERSED"
	RefusedReversalPaidSince = "PAID_SINCE"
)

// ActHoldbackReleaseReversed is the audit verb of a release reversed in error.
const ActHoldbackReleaseReversed = "HOLDBACK_RELEASE_REVERSED"

// HoldbackReleaseReversedTopic is the outbox topic of a reversal's journal (DR 1150 / CR 1100).
const HoldbackReleaseReversedTopic = "ar.holdback.release_reversed"

var errReversalPaidSince = HoldbackReleaseRefused{Kind: RefusedReversalPaidSince,
	Msg: "the customer has paid part of the released amount, so the holdback cannot be put back over it; reverse that payment first"}

// ActHoldbackReleased is the audit verb of an invoice level release.
const ActHoldbackReleased = "HOLDBACK_RELEASED"

// HoldbackReleaseRefused is a release (or a due read row) refused in words.
type HoldbackReleaseRefused struct {
	Kind string
	Msg  string
}

func (e HoldbackReleaseRefused) Error() string { return e.Msg }

// holdbackDue judges one job's preservation against the house day: "" kind when it is due.
func holdbackDue(p lien.Preservation, linked bool, today string) (kind, msg string) {
	switch {
	case !linked:
		return RefusedNoLienJob, "no lien job is linked to this job, so there is no preservation period to count"
	case p.LienPreserved:
		return RefusedLienPreserved, fmt.Sprintf(
			"a lien was preserved on lien job %s on %s and has not been discharged, so the holdback stays held until it is discharged or settled",
			p.LienJobNo, p.LienPreservedOn)
	case p.BasisDate == "":
		return RefusedNoDate, fmt.Sprintf(
			"lien job %s has no date to start the preservation period from (the publication of the certificate of substantial performance, the date of substantial performance or the last supply), so the holdback cannot be released", p.LienJobNo)
	case today <= p.LastDayToPreserve:
		basis := "substantial performance"
		switch p.Basis {
		case lien.BasisCompletion:
			basis = "last supply"
		case lien.BasisPublication:
			basis = "publication of the certificate of substantial performance"
		}
		return RefusedNotDue, fmt.Sprintf(
			"the holdback is not due: lien job %s counts %d days from %s (%s), so a lien can be preserved through %s and the holdback is releasable from %s",
			p.LienJobNo, p.PreserveDays, p.BasisDate, basis, p.LastDayToPreserve, p.ReleasableOn)
	}
	return "", ""
}

// HoldbackDueQuery is the due to release read: the branch ("" = every branch the caller reaches) and the
// instant it is read at.
type HoldbackDueQuery struct {
	BranchID        string
	Now             time.Time
	CanAccessBranch func(branchID string) bool
}

// HoldbackDueRow is one invoice's holdback and where it stands. HoldbackCents is read from the dated
// ledger (invoice_holdback_as_of) at the query's instant. Due is true when a Controller can release it now;
// otherwise BlockedKind and BlockedReason say why in words.
type HoldbackDueRow struct {
	InvoiceID         string `json:"invoice_id"`
	InvoiceNo         string `json:"invoice_no"`
	CustomerID        string `json:"customer_id"`
	CustomerJobID     string `json:"customer_job_id"`
	LienJobID         string `json:"lien_job_id,omitempty"`
	LienJobNo         string `json:"lien_job_no,omitempty"`
	BranchID          string `json:"branch_id"`
	HoldbackCents     int64  `json:"holdback_cents"`
	Basis             string `json:"basis,omitempty"`
	BasisDate         string `json:"basis_date,omitempty"`
	PreserveDays      int    `json:"preserve_days,omitempty"`
	LastDayToPreserve string `json:"last_day_to_preserve,omitempty"`
	ReleasableOn      string `json:"releasable_on,omitempty"`
	Due               bool   `json:"due"`
	BlockedKind       string `json:"blocked_kind,omitempty"`
	BlockedReason     string `json:"blocked_reason,omitempty"`
}

// HoldbackDueToRelease lists, per invoice, the holdback each open invoice still withholds and whether it is
// due, oldest invoices first within a branch and job. It reads the amounts from invoice_holdback_as_of at
// q.Now, so what it lists is what a release at that instant would move.
func (r *Repository) HoldbackDueToRelease(ctx context.Context, q HoldbackDueQuery) ([]HoldbackDueRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if r.holdback == nil {
		return nil, ErrNoHoldbackSeam
	}
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	z, err := r.zoneFor(ctx)
	if err != nil {
		return nil, err
	}
	today := z.Today(func() time.Time { return q.Now })
	rows, err := r.pool.Query(ctx, `
SELECT i.id::text, i.invoice_no::text, i.customer_id::text, i.job_id::text, i.branch_id::text,
       invoice_holdback_as_of(i.id, $2::timestamptz)
  FROM invoices i
 WHERE i.doc_type = 'INVOICE' AND i.job_id IS NOT NULL AND i.status IN ('FINALIZED', 'PARTIALLY_PAID')
   AND i.holdback_cents > 0 AND ($1 = '' OR i.branch_id = $1::uuid)
 ORDER BY i.branch_id, i.job_id, i.issue_date, i.id`, q.BranchID, q.Now)
	if err != nil {
		return nil, fmt.Errorf("billing: read the holdback due to release: %w", err)
	}
	defer rows.Close()
	var out []HoldbackDueRow
	for rows.Next() {
		var row HoldbackDueRow
		if err := rows.Scan(&row.InvoiceID, &row.InvoiceNo, &row.CustomerID, &row.CustomerJobID, &row.BranchID, &row.HoldbackCents); err != nil {
			return nil, fmt.Errorf("billing: scan a holdback due row: %w", err)
		}
		if row.HoldbackCents <= 0 || (q.CanAccessBranch != nil && !q.CanAccessBranch(row.BranchID)) {
			continue
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	byJob := map[string]struct {
		p      lien.Preservation
		linked bool
		err    error
	}{}
	for i := range out {
		row := &out[i]
		j, seen := byJob[row.CustomerJobID]
		if !seen {
			j.p, j.linked, j.err = r.holdback.PreservationFor(ctx, row.CustomerJobID)
			byJob[row.CustomerJobID] = j
		}
		if j.err != nil {
			row.BlockedKind, row.BlockedReason = RefusedNoDate, "the holdback rule for this job cannot be read: "+j.err.Error()
			continue
		}
		row.LienJobID, row.LienJobNo, row.Basis, row.BasisDate = j.p.LienJobID, j.p.LienJobNo, j.p.Basis, j.p.BasisDate
		row.PreserveDays, row.LastDayToPreserve, row.ReleasableOn = j.p.PreserveDays, j.p.LastDayToPreserve, j.p.ReleasableOn
		kind, msg := holdbackDue(j.p, j.linked, today)
		row.Due, row.BlockedKind, row.BlockedReason = kind == "", kind, msg
	}
	return out, nil
}

// HoldbackReleaseInput is a release request. IsController is the caller's own role answer, never the body's.
type HoldbackReleaseInput struct {
	CustomerJobID   string
	IdempotencyKey  string
	ActorID         string
	OrgID           string
	IsController    bool
	CanAccessBranch func(branchID string) bool
}

// ReleasedInvoice is one invoice's release.
type ReleasedInvoice struct {
	InvoiceID   string `json:"invoice_id"`
	InvoiceNo   string `json:"invoice_no"`
	BranchID    string `json:"branch_id"`
	ReleaseID   string `json:"release_id"`
	AmountCents int64  `json:"amount_cents"`
}

// HoldbackReleaseResult is what a release (or its replay) answers.
type HoldbackReleaseResult struct {
	CustomerJobID string            `json:"customer_job_id"`
	ReleasedOn    string            `json:"released_on"`
	TotalCents    int64             `json:"total_cents"`
	Items         []ReleasedInvoice `json:"items"`
	Replayed      bool              `json:"replayed"`
}

// HoldbackReleaseService is the permission shaped door over the repository release.
type HoldbackReleaseService struct {
	repo  *Repository
	audit audit.Recorder
	now   func() time.Time
}

// NewHoldbackReleaseService wires the service over a billing repository that carries the holdback seam.
func NewHoldbackReleaseService(repo *Repository) *HoldbackReleaseService {
	return &HoldbackReleaseService{repo: repo, now: time.Now}
}

// WithAudit attaches the audit recorder (best effort once the release has committed).
func (s *HoldbackReleaseService) WithAudit(r audit.Recorder) *HoldbackReleaseService {
	s.audit = r
	return s
}

// WithClock overrides the clock (tests).
func (s *HoldbackReleaseService) WithClock(now func() time.Time) *HoldbackReleaseService {
	if now != nil {
		s.now = now
	}
	return s
}

// Release releases the due holdback of one customer job, once, for a Controller.
func (s *HoldbackReleaseService) Release(ctx context.Context, in HoldbackReleaseInput) (HoldbackReleaseResult, error) {
	if !in.IsController {
		return HoldbackReleaseResult{}, HoldbackReleaseRefused{Kind: RefusedNotController, Msg: "releasing a holdback is the Controller's action"}
	}
	jobID, err := uuid.Parse(strings.TrimSpace(in.CustomerJobID))
	if err != nil {
		return HoldbackReleaseResult{}, httpx.BadRequestf("customer job id must be a uuid")
	}
	in.CustomerJobID = jobID.String()
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.IdempotencyKey == "" {
		return HoldbackReleaseResult{}, httpx.BadRequestf("an idempotency key is required")
	}
	if strings.TrimSpace(in.ActorID) != "" {
		actor, err := uuid.Parse(strings.TrimSpace(in.ActorID))
		if err != nil {
			return HoldbackReleaseResult{}, httpx.BadRequestf("actor id must be a uuid")
		}
		in.ActorID = actor.String()
	}
	res, err := s.repo.ReleaseHoldback(ctx, in, s.now())
	if err != nil {
		return HoldbackReleaseResult{}, err
	}
	if s.audit != nil && !res.Replayed {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActHoldbackReleased, EntityType: "customer_job", EntityID: res.CustomerJobID,
			ActorID: in.ActorID, Purpose: "HOLDBACK_RELEASE", Reason: "holdback release " + in.IdempotencyKey,
			Extra: map[string]any{"amount_cents": res.TotalCents},
		})
	}
	return res, nil
}

// ReleaseHoldback is the repository write: one transaction under the job's lock; a refusal writes nothing.
func (r *Repository) ReleaseHoldback(ctx context.Context, in HoldbackReleaseInput, now time.Time) (HoldbackReleaseResult, error) {
	if r.pool == nil {
		return HoldbackReleaseResult{}, errNoDB
	}
	if r.holdback == nil {
		return HoldbackReleaseResult{}, ErrNoHoldbackSeam
	}
	z, err := r.zoneFor(ctx)
	if err != nil {
		return HoldbackReleaseResult{}, err
	}
	today := z.Today(func() time.Time { return now })

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return HoldbackReleaseResult{}, fmt.Errorf("holdback release: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := customer.WithCallerTx(ctx, tx)

	// The job's lock: two releases of one job run one after the other, and the second reads the first's work.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('holdback_release:' || $1::text, 0))`, in.CustomerJobID); err != nil {
		return HoldbackReleaseResult{}, fmt.Errorf("holdback release: lock the job: %w", err)
	}

	// The key's judgement first: a retry answers the first result.
	prior, err := r.holdback.ReleasesForKey(txCtx, in.IdempotencyKey)
	if err != nil {
		return HoldbackReleaseResult{}, err
	}
	if len(prior) > 0 {
		return r.replayRelease(ctx, tx, in, prior, today)
	}

	// A job whose holdback the old job level route already released is refused: its 1150 was credited
	// and its 2350 debited once, and a release here would credit 1150 a second time.
	legacy, err := r.holdback.HasJobLevelRelease(txCtx, in.CustomerJobID)
	if err != nil {
		return HoldbackReleaseResult{}, err
	}
	if legacy {
		return HoldbackReleaseResult{}, HoldbackReleaseRefused{Kind: RefusedAlreadyReleased,
			Msg: "the holdback of this job was already released by the old job level release (DR 2350 / CR 1150), so releasing it again would credit the holdback receivable twice; correct that release first"}
	}

	rows, err := tx.Query(ctx, `
SELECT id::text, invoice_no::text, branch_id::text, holdback_cents
  FROM invoices
 WHERE job_id = $1::uuid AND doc_type = 'INVOICE' AND status IN ('FINALIZED', 'PARTIALLY_PAID') AND holdback_cents > 0
 ORDER BY id
   FOR NO KEY UPDATE`, in.CustomerJobID)
	if err != nil {
		return HoldbackReleaseResult{}, fmt.Errorf("holdback release: lock the job's invoices: %w", err)
	}
	type held struct {
		id, no, branch string
		cents          int64
	}
	var invs []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.id, &h.no, &h.branch, &h.cents); err != nil {
			rows.Close()
			return HoldbackReleaseResult{}, fmt.Errorf("holdback release: scan an invoice: %w", err)
		}
		if in.CanAccessBranch != nil && !in.CanAccessBranch(h.branch) {
			continue
		}
		invs = append(invs, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return HoldbackReleaseResult{}, err
	}

	if len(invs) == 0 {
		done, err := r.holdback.HasReleaseOnJob(txCtx, in.CustomerJobID)
		if err != nil {
			return HoldbackReleaseResult{}, err
		}
		if done {
			return HoldbackReleaseResult{}, HoldbackReleaseRefused{Kind: RefusedAlreadyReleased,
				Msg: "the holdback of this job has already been released, and no invoice on it withholds any more"}
		}
		return HoldbackReleaseResult{}, HoldbackReleaseRefused{Kind: RefusedNothingHeld,
			Msg: "no open invoice on this job withholds any holdback to release"}
	}

	p, linked, err := r.holdback.PreservationFor(txCtx, in.CustomerJobID)
	if err != nil {
		return HoldbackReleaseResult{}, err
	}
	if kind, msg := holdbackDue(p, linked, today); kind != "" {
		return HoldbackReleaseResult{}, HoldbackReleaseRefused{Kind: kind, Msg: msg}
	}

	res := HoldbackReleaseResult{CustomerJobID: in.CustomerJobID, ReleasedOn: today}
	for _, inv := range invs {
		// The column and the dated ledger must tie before either moves: a release of a figure the ledger
		// does not hold would split the two.
		var ledger int64
		if err := tx.QueryRow(ctx, `SELECT invoice_holdback_as_of($1::uuid, now())`, inv.id).Scan(&ledger); err != nil {
			return HoldbackReleaseResult{}, fmt.Errorf("holdback release: read the ledger of invoice %s: %w", inv.no, err)
		}
		if ledger != inv.cents {
			return HoldbackReleaseResult{}, fmt.Errorf(
				"holdback release: invoice %s withholds %d cents but its ledger holds %d; sync the holdback ledger before releasing", inv.no, inv.cents, ledger)
		}
		var releaseID string
		if err := lowerHoldbackAndLedger(txCtx, tx, r.holdback, inv.id, inv.cents, func(ctx context.Context) error {
			id, err := r.holdback.BookRelease(ctx, lien.ReleaseBooking{
				InvoiceID: inv.id, BranchID: inv.branch, AmountCents: inv.cents,
				Key: in.IdempotencyKey, ReleasedOn: today, ActorID: in.ActorID,
			})
			releaseID = id
			return err
		}); err != nil {
			return HoldbackReleaseResult{}, err
		}
		if err := emitOutbox(ctx, tx, HoldbackReleasedTopic, map[string]any{
			"invoice_id": inv.id, "release_id": releaseID, "amount_cents": inv.cents, "occurred_at": now,
		}); err != nil {
			return HoldbackReleaseResult{}, err
		}
		res.Items = append(res.Items, ReleasedInvoice{InvoiceID: inv.id, InvoiceNo: inv.no, BranchID: inv.branch, ReleaseID: releaseID, AmountCents: inv.cents})
		res.TotalCents += inv.cents
	}
	if err := tx.Commit(ctx); err != nil {
		return HoldbackReleaseResult{}, fmt.Errorf("holdback release: commit: %w", err)
	}
	return res, nil
}

// HoldbackReleasedTopic is the outbox topic the GL posting consumer owns for an invoice's release.
const HoldbackReleasedTopic = "ar.holdback.released"

// replayRelease answers a key's first result, or refuses a key already spent on another job.
func (r *Repository) replayRelease(ctx context.Context, tx pgx.Tx, in HoldbackReleaseInput, prior []lien.ReleasedRow, today string) (HoldbackReleaseResult, error) {
	res := HoldbackReleaseResult{CustomerJobID: in.CustomerJobID, ReleasedOn: today, Replayed: true}
	for _, p := range prior {
		var job, no, branch string
		err := tx.QueryRow(ctx, `SELECT COALESCE(job_id::text, ''), invoice_no::text, branch_id::text FROM invoices WHERE id = $1::uuid`, p.InvoiceID).Scan(&job, &no, &branch)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return HoldbackReleaseResult{}, HoldbackReleaseRefused{Kind: RefusedKeyReused, Msg: "that idempotency key already released a holdback; use a new key"}
			}
			return HoldbackReleaseResult{}, fmt.Errorf("holdback release: read a replayed invoice: %w", err)
		}
		if job != in.CustomerJobID {
			return HoldbackReleaseResult{}, HoldbackReleaseRefused{Kind: RefusedKeyReused,
				Msg: "that idempotency key already released the holdback of another job; a key names one release, use a new key"}
		}
		res.Items = append(res.Items, ReleasedInvoice{InvoiceID: p.InvoiceID, InvoiceNo: no, BranchID: branch, ReleaseID: p.ID, AmountCents: p.AmountCents})
		res.TotalCents += p.AmountCents
	}
	return res, nil
}

// HoldbackReversalInput is a reversal request. IsController is the caller's own role answer.
type HoldbackReversalInput struct {
	ReleaseID       string
	Reason          string
	ActorID         string
	IsController    bool
	CanAccessBranch func(branchID string) bool
}

// HoldbackReversalResult is what a reversal answers.
type HoldbackReversalResult struct {
	ReleaseID     string `json:"release_id"`
	InvoiceID     string `json:"invoice_id"`
	InvoiceNo     string `json:"invoice_no"`
	RestoredCents int64  `json:"restored_cents"`
	ReversedOn    string `json:"reversed_on"`
}

// Reverse undoes a release made in error, for a Controller, with a reason: the invoice's column goes up, a
// positive RETENTION row restores the ledger beside the (kept) RELEASE row, and the GL reverses the pair
// (DR 1150 / CR 1100). The holdback can then be released again, once. Refused in words when the release is
// unknown, already reversed, or the customer has paid some of the released amount.
func (s *HoldbackReleaseService) Reverse(ctx context.Context, in HoldbackReversalInput) (HoldbackReversalResult, error) {
	if !in.IsController {
		return HoldbackReversalResult{}, HoldbackReleaseRefused{Kind: RefusedNotController, Msg: "reversing a holdback release is the Controller's action"}
	}
	id, err := uuid.Parse(strings.TrimSpace(in.ReleaseID))
	if err != nil {
		return HoldbackReversalResult{}, httpx.BadRequestf("release id must be a uuid")
	}
	in.ReleaseID = id.String()
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return HoldbackReversalResult{}, httpx.BadRequestf("a reason is required to reverse a release")
	}
	if strings.TrimSpace(in.ActorID) != "" {
		actor, err := uuid.Parse(strings.TrimSpace(in.ActorID))
		if err != nil {
			return HoldbackReversalResult{}, httpx.BadRequestf("actor id must be a uuid")
		}
		in.ActorID = actor.String()
	}
	res, err := s.repo.ReverseHoldbackRelease(ctx, in, s.now())
	if err != nil {
		return HoldbackReversalResult{}, err
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActHoldbackReleaseReversed, EntityType: "holdback_release", EntityID: res.ReleaseID,
			ActorID: in.ActorID, Purpose: "HOLDBACK_RELEASE_REVERSAL", Reason: in.Reason,
			Extra: map[string]any{"invoice_id": res.InvoiceID, "restored_cents": res.RestoredCents},
		})
	}
	return res, nil
}

// ReverseHoldbackRelease is the repository write: one transaction under the invoice's job lock, so a reversal
// and a release of the same job run one after the other.
func (r *Repository) ReverseHoldbackRelease(ctx context.Context, in HoldbackReversalInput, now time.Time) (HoldbackReversalResult, error) {
	if r.pool == nil {
		return HoldbackReversalResult{}, errNoDB
	}
	if r.holdback == nil {
		return HoldbackReversalResult{}, ErrNoHoldbackSeam
	}
	z, err := r.zoneFor(ctx)
	if err != nil {
		return HoldbackReversalResult{}, err
	}
	today := z.Today(func() time.Time { return now })
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return HoldbackReversalResult{}, fmt.Errorf("holdback reversal: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := customer.WithCallerTx(ctx, tx)

	notFound := HoldbackReleaseRefused{Kind: RefusedReleaseNotFound, Msg: "no such holdback release"}
	row, ok, err := r.holdback.ReleaseRowByID(txCtx, in.ReleaseID)
	if err != nil {
		return HoldbackReversalResult{}, err
	}
	if !ok || (in.CanAccessBranch != nil && !in.CanAccessBranch(row.BranchID)) {
		return HoldbackReversalResult{}, notFound
	}
	var jobID, invNo string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(job_id::text, ''), invoice_no::text FROM invoices WHERE id = $1::uuid`, row.InvoiceID).Scan(&jobID, &invNo); err != nil {
		return HoldbackReversalResult{}, fmt.Errorf("holdback reversal: read the invoice: %w", err)
	}
	// The job's lock, the release's own: a reversal and a release of one job never interleave.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('holdback_release:' || $1::text, 0))`, jobID); err != nil {
		return HoldbackReversalResult{}, fmt.Errorf("holdback reversal: lock the job: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, row.InvoiceID); err != nil {
		return HoldbackReversalResult{}, fmt.Errorf("holdback reversal: lock the invoice: %w", err)
	}
	done, err := r.holdback.MarkReleaseReversed(txCtx, row.ID)
	if err != nil {
		return HoldbackReversalResult{}, err
	}
	if !done {
		return HoldbackReversalResult{}, HoldbackReleaseRefused{Kind: RefusedAlreadyReversed, Msg: "that release has already been reversed"}
	}
	if err := raiseHoldbackAndLedger(txCtx, tx, r.holdback, row.InvoiceID, row.AmountCents, func(ctx context.Context) error {
		_, err := r.holdback.BookHoldbackMove(ctx, lien.HoldbackMove{
			InvoiceID: row.InvoiceID, BranchID: row.BranchID, Reason: "holdback release reversal " + row.ID, AmountCents: row.AmountCents,
		})
		return err
	}); err != nil {
		return HoldbackReversalResult{}, err
	}
	if err := emitOutbox(ctx, tx, HoldbackReleaseReversedTopic, map[string]any{
		"invoice_id": row.InvoiceID, "release_id": row.ID, "amount_cents": row.AmountCents, "occurred_at": now,
	}); err != nil {
		return HoldbackReversalResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return HoldbackReversalResult{}, fmt.Errorf("holdback reversal: commit: %w", err)
	}
	return HoldbackReversalResult{ReleaseID: row.ID, InvoiceID: row.InvoiceID, InvoiceNo: invNo, RestoredCents: row.AmountCents, ReversedOn: today}, nil
}
