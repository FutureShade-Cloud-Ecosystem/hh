package purchasing

// payment_run.go, the W3 payment run: the AP clerk's propose, review and pay over the open bills a
// branch owes, in three steps.
//
//	PROPOSE  the posted bills due on or before a house day, in one branch, optionally one vendor,
//	         each at its open amount, become a DRAFT run's lines.
//	REVIEW   lines come off, amounts come down, never above the bill's open. Nothing has moved.
//	PAY      every selected bill is paid through PayBill, the module's only payment door, under the
//	         key run:<run id>:<bill id>, dated the run's pay date.
//
// THE RUN MOVES NO MONEY ITSELF. Every cent moves through PayBill's own transaction and journal, so
// the aging, the AP subledger and GL 2100 keep the single source of truth they already have, and a
// run that dies mid-flight leaves the books exactly as a clerk paying each bill by hand would. The
// keys make the pay idempotent per bill: a re-press of a paid run replays the first payment per
// line and writes nothing (the run's own PAID status answers first, the keys are the belt under
// it), and a bill paid elsewhere between the review and the pay is paid for at most its open
// amount, or skipped with a reason on its line.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
)

// The run lifecycle and the per line outcomes (purchasing_payment_runs.status,
// purchasing_payment_run_lines.status, migration 0454).
const (
	RunDraft = "DRAFT"
	RunPaid  = "PAID"

	RunLineSelected = "SELECTED"
	RunLinePaid     = "PAID"
	RunLineSkipped  = "SKIPPED"
)

// - wire shapes -

// PaymentRunLine is one selected bill on a run: what the review selected (amount_cents) beside
// what actually moved (paid_cents), or the reason it did not.
type PaymentRunLine struct {
	ID          string `json:"id"`
	Seq         int    `json:"seq"`
	BillID      string `json:"bill_id"`
	BillNo      string `json:"bill_no,omitempty"`
	VendorID    string `json:"vendor_id,omitempty"`
	VendorName  string `json:"vendor_name,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	AmountCents int64  `json:"amount_cents"`
	Status      string `json:"status"`
	PaidCents   int64  `json:"paid_cents"`
	SkipReason  string `json:"skip_reason,omitempty"`
}

// PaymentRun is the document: its filter (branch, due_on, vendor, pay_date), its selection, and
// what the pay did.
type PaymentRun struct {
	ID         string           `json:"id"`
	BranchID   string           `json:"branch_id"`
	Status     string           `json:"status"`
	DueOn      string           `json:"due_on"`
	VendorID   string           `json:"vendor_id,omitempty"`
	PayDate    string           `json:"pay_date"`
	TotalCents int64            `json:"total_cents"`
	PaidCents  int64            `json:"paid_cents"`
	PaidAt     *time.Time       `json:"paid_at,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	Lines      []PaymentRunLine `json:"lines"`
}

// PaymentRunSummary is one row of the list: the run without its lines.
type PaymentRunSummary struct {
	ID         string    `json:"id"`
	BranchID   string    `json:"branch_id"`
	Status     string    `json:"status"`
	DueOn      string    `json:"due_on"`
	VendorID   string    `json:"vendor_id,omitempty"`
	PayDate    string    `json:"pay_date"`
	LineCount  int       `json:"line_count"`
	TotalCents int64     `json:"total_cents"`
	PaidCents  int64     `json:"paid_cents"`
	CreatedAt  time.Time `json:"created_at"`
}

// - requests -

// ProposePaymentRunRequest is the POST /purchasing/payment-runs body. branch_id and due_on name
// the run's reach (one branch, bills due on or before one house day); vendor_id narrows it to one
// vendor; pay_date is the house day the payments will be dated, today's house day when omitted.
type ProposePaymentRunRequest struct {
	BranchID string `json:"branch_id"`
	DueOn    string `json:"due_on"`
	VendorID string `json:"vendor_id,omitempty"`
	PayDate  string `json:"pay_date,omitempty"`
}

// PaymentRunLineAmount lowers one selected line to a new amount.
type PaymentRunLineAmount struct {
	BillID      string `json:"bill_id"`
	AmountCents int64  `json:"amount_cents"`
}

// UpdatePaymentRunRequest is the PUT /purchasing/payment-runs/{id} body: the review. remove names
// whole bills to take off the run; lower sets a line's amount, never above the bill's open.
type UpdatePaymentRunRequest struct {
	Remove []string               `json:"remove,omitempty"`
	Lower  []PaymentRunLineAmount `json:"lower,omitempty"`
}

// - the proposal -

// ProposePaymentRun opens the run: the open posted bills due on or before the house day, in the
// branch (and vendor) named, one line each at its open amount.
func (s *Service) ProposePaymentRun(ctx context.Context, in ProposePaymentRunRequest) (PaymentRun, error) {
	caller, branchID, err := s.branchFence(ctx, in.BranchID)
	if err != nil {
		return PaymentRun{}, err
	}
	if branchID == "" {
		return PaymentRun{}, badReq("purchasing: branch_id is required, a payment run is one branch's cheques")
	}
	// The dealer's day, decided BEFORE the transaction opens (a zone read must not hold a
	// connection beside the open transaction): both the run's dates are house days in it.
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return PaymentRun{}, err
	}
	dueOn := strings.TrimSpace(in.DueOn)
	if dueOn == "" {
		return PaymentRun{}, badReq("purchasing: due_on is required, the house day the run selects bills due on or before")
	}
	if _, err := zone.ParseDay(dueOn); err != nil {
		return PaymentRun{}, badReq("purchasing: due_on must be a YYYY-MM-DD house day")
	}
	payDate := strings.TrimSpace(in.PayDate)
	if payDate == "" {
		payDate = zone.Today(s.now)
	}
	if _, err := zone.ParseDay(payDate); err != nil {
		return PaymentRun{}, badReq("purchasing: pay_date must be a YYYY-MM-DD house day")
	}

	candidates, err := s.repo.OpenBillsDueOn(ctx, branchID, dueOn, strings.TrimSpace(in.VendorID))
	if err != nil {
		return PaymentRun{}, err
	}
	if len(candidates) == 0 {
		reach := fmt.Sprintf(" due on or before %s", dueOn)
		if v := strings.TrimSpace(in.VendorID); v != "" {
			reach += " for vendor " + v
		}
		return PaymentRun{}, unprocessable("purchasing: no open bills in branch %s%s, there is no run to propose", branchID, reach)
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return PaymentRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	runID, err := s.repo.InsertPaymentRun(ctx, tx, insertPaymentRunParams{
		BranchID: branchID, DueOn: dueOn, VendorID: strings.TrimSpace(in.VendorID),
		PayDate: payDate, CreatedBy: caller.UserID,
	})
	if err != nil {
		return PaymentRun{}, err
	}
	for i, c := range candidates {
		if err := s.repo.InsertPaymentRunLine(ctx, tx, runID, i+1, c.BillID, c.OpenCents); err != nil {
			return PaymentRun{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentRun{}, fmt.Errorf("purchasing: commit propose payment run: %w", err)
	}
	s.log.Info("purchasing: payment run proposed", "branch_id", branchID, "due_on", dueOn,
		"bills", len(candidates), "total_cents", sumCandidates(candidates))
	s.record(ctx, "purchasing.payment_run.proposed", runID, map[string]any{
		"branch_id": branchID, "due_on": dueOn, "bills": len(candidates), "total_cents": sumCandidates(candidates),
	})
	return s.GetPaymentRun(ctx, runID)
}

func sumCandidates(cs []runBillCandidate) int64 {
	var sum int64
	for _, c := range cs {
		sum += c.OpenCents
	}
	return sum
}

// - the review -

// UpdatePaymentRun reviews a DRAFT run: whole bills come off, amounts come down, never above the
// bill's open amount as it stands while the review lands. A PAID run is finished and refuses
// further review.
func (s *Service) UpdatePaymentRun(ctx context.Context, runID string, in UpdatePaymentRunRequest) (PaymentRun, error) {
	if len(in.Remove) == 0 && len(in.Lower) == 0 {
		return PaymentRun{}, badReq("purchasing: the body names nothing to change")
	}
	if !isUUIDString(runID) {
		return PaymentRun{}, errNotFound
	}
	// One bill may be named once: the same bill twice in a list, or in both lists, is a request
	// that contradicts itself, and refusing it in words beats guessing which press the clerk meant.
	named := map[string]bool{}
	for _, id := range in.Remove {
		if named[id] {
			return PaymentRun{}, badReq("purchasing: bill %s is named more than once in the review", id)
		}
		named[id] = true
	}
	for _, l := range in.Lower {
		if named[l.BillID] {
			return PaymentRun{}, badReq("purchasing: bill %s is named more than once in the review", l.BillID)
		}
		named[l.BillID] = true
		if l.AmountCents <= 0 {
			return PaymentRun{}, badReq("purchasing: the lowered amount for bill %s must be positive", l.BillID)
		}
	}

	caller, err := s.caller(ctx)
	if err != nil {
		return PaymentRun{}, err
	}
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return PaymentRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	run, err := s.repo.PaymentRunForUpdate(ctx, tx, runID)
	if err != nil {
		return PaymentRun{}, err
	}
	if !caller.CanAccessBranch(run.BranchID) {
		return PaymentRun{}, errNotFound
	}
	if run.Status != RunDraft {
		return PaymentRun{}, conflict("purchasing: payment run is %s, only a DRAFT run can be reviewed", run.Status)
	}
	for _, id := range in.Remove {
		if err := s.repo.DeleteRunLineByBillTx(ctx, tx, runID, id); err != nil {
			return PaymentRun{}, err
		}
	}
	for _, l := range in.Lower {
		open, err := s.repo.BillOpenCentsTx(ctx, tx, l.BillID)
		if err != nil {
			return PaymentRun{}, err
		}
		if l.AmountCents > open {
			return PaymentRun{}, conflict("purchasing: bill has %d cents open, the run cannot select %d", open, l.AmountCents)
		}
		if err := s.repo.UpdateRunLineAmountTx(ctx, tx, runID, l.BillID, l.AmountCents); err != nil {
			return PaymentRun{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentRun{}, fmt.Errorf("purchasing: commit review payment run: %w", err)
	}
	s.record(ctx, "purchasing.payment_run.reviewed", runID, map[string]any{
		"removed": len(in.Remove), "lowered": len(in.Lower),
	})
	return s.GetPaymentRun(ctx, runID)
}

// - the pay -

// PayPaymentRun pays a run: every still SELECTED line's bill goes through PayBill under the line's
// own key, dated the run's pay date. Paying a run twice pays nothing twice: the PAID run answers
// itself, and each line's key replays its first payment even if a pay died between a bill's
// journal and the run's status. A bill paid elsewhere between the review and the pay is paid for at
// most its open amount, or skipped with a reason on its line.
func (s *Service) PayPaymentRun(ctx context.Context, runID string) (PaymentRun, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PaymentRun{}, err
	}
	if !isUUIDString(runID) {
		return PaymentRun{}, errNotFound
	}
	// The dealer's day, decided BEFORE any transaction opens (a zone read must not hold a
	// connection beside the open transaction): the pay date's instant is its house midnight, the
	// same calendar day convention every document dated through houseday carries.
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return PaymentRun{}, err
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return PaymentRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	run, err := s.repo.PaymentRunForUpdate(ctx, tx, runID)
	if err != nil {
		return PaymentRun{}, err
	}
	if !caller.CanAccessBranch(run.BranchID) {
		return PaymentRun{}, errNotFound
	}
	if run.Status == RunPaid {
		// The replay answer: the run already paid, and the per line keys under this one mean a
		// second press can only ever retrace the first.
		return s.GetPaymentRun(ctx, runID)
	}
	if run.Status != RunDraft {
		return PaymentRun{}, conflict("purchasing: payment run is %s, only a DRAFT run can be paid", run.Status)
	}
	lines, err := s.repo.PaymentRunLinesTx(ctx, tx, runID)
	if err != nil {
		return PaymentRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentRun{}, fmt.Errorf("purchasing: commit payment run read: %w", err)
	}

	paidAt, err := zone.ParseDay(run.PayDate)
	if err != nil {
		return PaymentRun{}, badReq("purchasing: the run's pay_date %q is not a house day", run.PayDate)
	}

	var totalPaid int64
	var paidBills, skippedBills int
	for _, ln := range lines {
		if ln.Status != RunLineSelected {
			// A line an earlier press already decided keeps its record; the key under it proves a
			// retry can only retrace, never double.
			if ln.Status == RunLinePaid {
				totalPaid += ln.PaidCents
			}
			continue
		}
		status, paidCents, reason, err := s.payRunLine(ctx, run.ID, ln, paidAt)
		if err != nil {
			return PaymentRun{}, err
		}
		if err := s.repo.SetPaymentRunLineOutcome(ctx, runID, ln.BillID, status, paidCents, reason); err != nil {
			return PaymentRun{}, err
		}
		if status == RunLinePaid {
			totalPaid += paidCents
			paidBills++
		} else {
			skippedBills++
		}
	}
	if err := s.repo.SetPaymentRunPaid(ctx, runID); err != nil {
		return PaymentRun{}, err
	}
	s.log.Info("purchasing: payment run paid", "run_id", runID, "bills_paid", paidBills,
		"bills_skipped", skippedBills, "paid_cents", totalPaid)
	s.record(ctx, "purchasing.payment_run.paid", runID, map[string]any{
		"bills_paid": paidBills, "bills_skipped": skippedBills, "paid_cents": totalPaid,
	})
	return s.GetPaymentRun(ctx, runID)
}

// payRunLine pays one selected bill through PayBill and answers the line's outcome. The bill's
// state and open balance are read fresh at the moment of the press: the run pays at most what the
// bill still owes, and a bill that no longer owes anything is skipped with the reason in words.
func (s *Service) payRunLine(ctx context.Context, runID string, ln PaymentRunLine, paidAt time.Time) (string, int64, string, error) {
	state, open, err := s.repo.BillPayState(ctx, ln.BillID)
	if err != nil {
		return "", 0, "", err
	}
	if state != BillPosted {
		return RunLineSkipped, 0, fmt.Sprintf("bill is %s, only POSTED bills can be paid", state), nil
	}
	if open <= 0 {
		return RunLineSkipped, 0, "bill was paid in full elsewhere", nil
	}
	amount := ln.AmountCents
	if amount > open {
		amount = open
	}
	if _, err := s.PayBill(ctx, ln.BillID, PayBillRequest{
		AmountCents:    amount,
		PaidAt:         paidAt,
		Note:           "Payment run " + runID,
		IdempotencyKey: runKey(runID, ln.BillID),
	}); err != nil {
		// The one race worth naming: the bill's open balance moved while this press was landing
		// and PayBill refused an overpayment. Re-read once; a bill that is now fully paid
		// elsewhere is a skip with a reason, and anything else was a real fault worth raising.
		state2, open2, rerr := s.repo.BillPayState(ctx, ln.BillID)
		if rerr == nil && state2 == BillPosted && open2 <= 0 {
			return RunLineSkipped, 0, "bill was paid in full while the run was paying", nil
		}
		return "", 0, "", err
	}
	return RunLinePaid, amount, "", nil
}

// runKey is the per line payment key the run pays under: run:<run id>:<bill id>. One bill, one
// run, one payment, however many times the press lands.
func runKey(runID, billID string) string { return "run:" + runID + ":" + billID }

// - the reads -

// GetPaymentRun answers the run with its lines, branch-fenced.
func (s *Service) GetPaymentRun(ctx context.Context, runID string) (PaymentRun, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PaymentRun{}, err
	}
	if !isUUIDString(runID) {
		return PaymentRun{}, errNotFound
	}
	run, err := s.repo.GetPaymentRun(ctx, runID)
	if err != nil {
		return PaymentRun{}, err
	}
	if !caller.CanAccessBranch(run.BranchID) {
		return PaymentRun{}, errNotFound
	}
	lines, err := s.repo.PaymentRunLines(ctx, runID)
	if err != nil {
		return PaymentRun{}, err
	}
	run.Lines = lines
	for _, ln := range lines {
		run.TotalCents += ln.AmountCents
		run.PaidCents += ln.PaidCents
	}
	return run, nil
}

// ListPaymentRuns lists branch-fenced runs, newest first, optionally by status.
func (s *Service) ListPaymentRuns(ctx context.Context, branchID, status string) ([]PaymentRunSummary, error) {
	_, branch, err := s.branchFence(ctx, branchID)
	if err != nil {
		return nil, err
	}
	if status != "" && status != RunDraft && status != RunPaid {
		return nil, badReq("purchasing: %q is not a payment run status", status)
	}
	return s.repo.ListPaymentRuns(ctx, branch, status)
}

// - repository -

type runBillCandidate struct {
	BillID     string
	BillNo     string
	VendorID   string
	VendorName string
	DueDate    string
	OpenCents  int64
}

// OpenBillsDueOn reads the open posted bills of one branch due on or before the house day (a bill
// with no due date falls back to its bill date, the aging's own rule), optionally one vendor,
// each at its open amount.
func (r *Repository) OpenBillsDueOn(ctx context.Context, branchID, dueOn, vendorID string) ([]runBillCandidate, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT b.id::text, b.bill_no::text, b.vendor_id::text, v.name,
		       to_char(COALESCE(b.due_date, b.bill_date), 'YYYY-MM-DD'),
		       b.total_cents - COALESCE((SELECT SUM(i.amount_cents)
		           FROM purchasing_bill_instalments i
		          WHERE i.bill_id = b.id AND i.status = 'PAID'), 0)
		  FROM purchasing_bills b
		  JOIN vendors v ON v.id = b.vendor_id
		 WHERE b.status = 'POSTED'
		   AND b.branch_id = $1::uuid
		   AND COALESCE(b.due_date, b.bill_date) <= $2::date
		   AND ($3 = '' OR b.vendor_id = $3::uuid)
		 ORDER BY COALESCE(b.due_date, b.bill_date) ASC, b.bill_no ASC, b.id ASC`,
		branchID, dueOn, vendorID,
	)
	if err != nil {
		return nil, fmt.Errorf("purchasing: open bills due on: %w", err)
	}
	defer rows.Close()
	out := []runBillCandidate{}
	for rows.Next() {
		var c runBillCandidate
		if err := rows.Scan(&c.BillID, &c.BillNo, &c.VendorID, &c.VendorName, &c.DueDate, &c.OpenCents); err != nil {
			return nil, fmt.Errorf("purchasing: scan open bill due on: %w", err)
		}
		if c.OpenCents > 0 {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

type insertPaymentRunParams struct {
	BranchID  string
	DueOn     string
	VendorID  string
	PayDate   string
	CreatedBy string
}

func (r *Repository) InsertPaymentRun(ctx context.Context, tx pgx.Tx, in insertPaymentRunParams) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	if err := r.q(tx).QueryRow(ctx, `
		INSERT INTO purchasing_payment_runs (branch_id, due_on, vendor_id, pay_date, created_by)
		VALUES ($1::uuid, $2::date, NULLIF($3,'')::uuid, $4::date, NULLIF($5,'')::uuid)
		RETURNING id::text`,
		in.BranchID, in.DueOn, in.VendorID, in.PayDate, in.CreatedBy,
	).Scan(&id); err != nil {
		return "", fmt.Errorf("purchasing: insert payment run: %w", err)
	}
	return id, nil
}

func (r *Repository) InsertPaymentRunLine(ctx context.Context, tx pgx.Tx, runID string, seq int, billID string, amountCents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.q(tx).Exec(ctx, `
		INSERT INTO purchasing_payment_run_lines (run_id, seq, bill_id, amount_cents)
		VALUES ($1::uuid, $2, $3::uuid, $4)`,
		runID, seq, billID, amountCents,
	); err != nil {
		return fmt.Errorf("purchasing: insert payment run line: %w", err)
	}
	return nil
}

// PaymentRunForUpdate loads the run header under a row lock.
func (r *Repository) PaymentRunForUpdate(ctx context.Context, tx pgx.Tx, runID string) (PaymentRun, error) {
	if r.pool == nil {
		return PaymentRun{}, errNoDB
	}
	run, err := scanPaymentRun(r.q(tx).QueryRow(ctx, `SELECT `+paymentRunCols+` FROM purchasing_payment_runs r WHERE r.id = $1 FOR UPDATE`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentRun{}, errNotFound
	}
	if err != nil {
		return PaymentRun{}, fmt.Errorf("purchasing: payment run for update: %w", err)
	}
	return run, nil
}

// GetPaymentRun reads the run header through the pool.
func (r *Repository) GetPaymentRun(ctx context.Context, runID string) (PaymentRun, error) {
	if r.pool == nil {
		return PaymentRun{}, errNoDB
	}
	run, err := scanPaymentRun(r.pool.QueryRow(ctx, `SELECT `+paymentRunCols+` FROM purchasing_payment_runs r WHERE r.id = $1`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentRun{}, errNotFound
	}
	if err != nil {
		return PaymentRun{}, fmt.Errorf("purchasing: get payment run: %w", err)
	}
	return run, nil
}

const paymentRunCols = `r.id::text, r.branch_id::text, r.status,
	to_char(r.due_on, 'YYYY-MM-DD'), COALESCE(r.vendor_id::text, ''), to_char(r.pay_date, 'YYYY-MM-DD'),
	r.paid_at, r.created_at`

func scanPaymentRun(row pgx.Row) (PaymentRun, error) {
	var run PaymentRun
	err := row.Scan(&run.ID, &run.BranchID, &run.Status, &run.DueOn, &run.VendorID, &run.PayDate,
		&run.PaidAt, &run.CreatedAt)
	return run, err
}

const paymentRunLineCols = `l.id::text, l.seq, l.bill_id::text, b.bill_no::text, b.vendor_id::text, v.name,
	COALESCE(to_char(b.due_date, 'YYYY-MM-DD'), to_char(b.bill_date, 'YYYY-MM-DD')),
	l.amount_cents, l.status, l.paid_cents, l.skip_reason`

func scanPaymentRunLine(row pgx.Row) (PaymentRunLine, error) {
	var ln PaymentRunLine
	err := row.Scan(&ln.ID, &ln.Seq, &ln.BillID, &ln.BillNo, &ln.VendorID, &ln.VendorName, &ln.DueDate,
		&ln.AmountCents, &ln.Status, &ln.PaidCents, &ln.SkipReason)
	return ln, err
}

// PaymentRunLines reads the run's lines in selection order through the pool.
func (r *Repository) PaymentRunLines(ctx context.Context, runID string) ([]PaymentRunLine, error) {
	return r.PaymentRunLinesTx(ctx, nil, runID)
}

// PaymentRunLinesTx is PaymentRunLines through tx (nil means the pool).
func (r *Repository) PaymentRunLinesTx(ctx context.Context, tx pgx.Tx, runID string) ([]PaymentRunLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.q(tx).Query(ctx, `SELECT `+paymentRunLineCols+`
		  FROM purchasing_payment_run_lines l
		  JOIN purchasing_bills b ON b.id = l.bill_id
		  JOIN vendors v ON v.id = b.vendor_id
		 WHERE l.run_id = $1
		 ORDER BY l.seq`, runID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: payment run lines: %w", err)
	}
	defer rows.Close()
	out := []PaymentRunLine{}
	for rows.Next() {
		ln, err := scanPaymentRunLine(rows)
		if err != nil {
			return nil, fmt.Errorf("purchasing: scan payment run line: %w", err)
		}
		out = append(out, ln)
	}
	return out, rows.Err()
}

// DeleteRunLineByBillTx removes the run's line for billID. A bill that is not on the run is
// refused in words, never a silent no-op.
func (r *Repository) DeleteRunLineByBillTx(ctx context.Context, tx pgx.Tx, runID, billID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.q(tx).Exec(ctx,
		`DELETE FROM purchasing_payment_run_lines WHERE run_id = $1::uuid AND bill_id = $2::uuid`,
		runID, billID)
	if err != nil {
		return fmt.Errorf("purchasing: delete payment run line: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return badReq("purchasing: bill %s is not on this run", billID)
	}
	return nil
}

// UpdateRunLineAmountTx lowers the run's line for billID. A bill that is not on the run is
// refused in words.
func (r *Repository) UpdateRunLineAmountTx(ctx context.Context, tx pgx.Tx, runID, billID string, amountCents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_payment_run_lines SET amount_cents = $3
		 WHERE run_id = $1::uuid AND bill_id = $2::uuid`,
		runID, billID, amountCents)
	if err != nil {
		return fmt.Errorf("purchasing: update payment run line: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return badReq("purchasing: bill %s is not on this run", billID)
	}
	return nil
}

// BillOpenCentsTx answers the bill's open amount (total less paid instalments) inside tx.
func (r *Repository) BillOpenCentsTx(ctx context.Context, tx pgx.Tx, billID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var open int64
	err := r.q(tx).QueryRow(ctx, `
		SELECT b.total_cents - COALESCE((SELECT SUM(i.amount_cents)
		           FROM purchasing_bill_instalments i
		          WHERE i.bill_id = b.id AND i.status = 'PAID'), 0)
		  FROM purchasing_bills b WHERE b.id = $1::uuid`, billID,
	).Scan(&open)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("purchasing: bill open cents: %w", err)
	}
	return open, nil
}

// BillPayState answers the bill's status and open amount in one read, the fresh look the pay's
// skip-or-clamp decision is made from.
func (r *Repository) BillPayState(ctx context.Context, billID string) (string, int64, error) {
	if r.pool == nil {
		return "", 0, errNoDB
	}
	var status string
	var open int64
	err := r.pool.QueryRow(ctx, `
		SELECT b.status,
		       b.total_cents - COALESCE((SELECT SUM(i.amount_cents)
		           FROM purchasing_bill_instalments i
		          WHERE i.bill_id = b.id AND i.status = 'PAID'), 0)
		  FROM purchasing_bills b WHERE b.id = $1::uuid`, billID,
	).Scan(&status, &open)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, errNotFound
	}
	if err != nil {
		return "", 0, fmt.Errorf("purchasing: bill pay state: %w", err)
	}
	return status, open, nil
}

// SetPaymentRunLineOutcome records one line's pay outcome beside its selection.
func (r *Repository) SetPaymentRunLineOutcome(ctx context.Context, runID, billID, status string, paidCents int64, skipReason string) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.pool.Exec(ctx, `
		UPDATE purchasing_payment_run_lines
		   SET status = $3, paid_cents = $4, skip_reason = $5
		 WHERE run_id = $1::uuid AND bill_id = $2::uuid`,
		runID, billID, status, paidCents, skipReason,
	); err != nil {
		return fmt.Errorf("purchasing: set payment run line outcome: %w", err)
	}
	return nil
}

// SetPaymentRunPaid stamps the run PAID. A run already PAID keeps its first stamp (the WHERE), so
// two presses racing to finish the same run cannot move the moment twice.
func (r *Repository) SetPaymentRunPaid(ctx context.Context, runID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.pool.Exec(ctx, `
		UPDATE purchasing_payment_runs
		   SET status = 'PAID', paid_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND status = 'DRAFT'`, runID,
	); err != nil {
		return fmt.Errorf("purchasing: set payment run paid: %w", err)
	}
	return nil
}

// ListPaymentRuns lists runs for a branch ("" = all accessible; the service applies the branch
// fence) and optional status, with line count and the selection and paid totals.
func (r *Repository) ListPaymentRuns(ctx context.Context, branchID, status string) ([]PaymentRunSummary, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT r.id::text, r.branch_id::text, r.status,
		       to_char(r.due_on, 'YYYY-MM-DD'), COALESCE(r.vendor_id::text, ''), to_char(r.pay_date, 'YYYY-MM-DD'),
		       count(l.id), COALESCE(SUM(l.amount_cents), 0), COALESCE(SUM(l.paid_cents), 0), r.created_at
		  FROM purchasing_payment_runs r
		  LEFT JOIN purchasing_payment_run_lines l ON l.run_id = r.id
		 WHERE ($1 = '' OR r.branch_id = $1::uuid)
		   AND ($2 = '' OR r.status = $2)
		 GROUP BY r.id
		 ORDER BY r.created_at DESC, r.id DESC
		 LIMIT 500`, branchID, status,
	)
	if err != nil {
		return nil, fmt.Errorf("purchasing: list payment runs: %w", err)
	}
	defer rows.Close()
	out := []PaymentRunSummary{}
	for rows.Next() {
		var s PaymentRunSummary
		if err := rows.Scan(&s.ID, &s.BranchID, &s.Status, &s.DueOn, &s.VendorID, &s.PayDate,
			&s.LineCount, &s.TotalCents, &s.PaidCents, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("purchasing: scan payment run summary: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
