package billing

// finance_charge.go, wave 3 FC-1: THE MONTHLY FINANCE CHARGE RUN.
//
// -- THE ACT ------------------------------------------------------------------------
// A dealer charges for late payment. For one branch and one house month, the run measures, as of
// the month's last house day, each customer's balance more than 30 days past due: the sum of the
// open amounts of that branch's trade invoices whose due date is more than 30 days before that day.
// The charge on a balance above zero is one and a half percent in integer cents rounded half to
// even, with a minimum of $2.00. Each charge is its own invoice, raised THROUGH THE INVOICE WRITER
// (this package's own writeCore, the same shape every document the writer raises carries: FINALIZED,
// the AR subledger row, the finalized event the GL consumer journals from), stamped with the
// FINANCE_CHARGE source kind so the GL posts DR 1100 / CR finance charge income through the
// invoice.finalized/finance_charge posting rule rather than revenue.
//
// -- THE MEASUREMENT ----------------------------------------------------------------
// The balance is measured AS OF THE MONTH'S LAST HOUSE DAY, by the aging's own dated rebuild (the
// same expression agingSelect and ARBalanceCents run): a payment that lands after the month end but
// before the run does not reduce the charged balance, and an invoice finalized after the month end
// is not on the books at it. The candidates are INVOICES alone: an NSF fee or a bare adjustment on
// the customer_transactions arm of the aging has no due date of its own to be 30 days past, so it is
// never charged (the aging still carries it, and it still ties to 1100).
//
// Earlier finance charge invoices DO NOT COUNT in the balance, by their source kind. A charge is
// not trade debt from a sale: charging a charge would compound the rate past the one and a half
// percent the plan names, on money the dealer never lent. The document still ages, still ties to
// 1100 and still collects, it simply never feeds the next run.
//
// -- ONCE PER CUSTOMER AND MONTH ----------------------------------------------------
// One run is one transaction over the branch's whole candidate set: every charge commits or none
// does, so a half charged month cannot stand on the books. Inside it, each customer's charge is
// claimed first with a row in finance_charge_runs (0473), whose UNIQUE (branch_id, customer_id,
// month) is the fact a second claim collides with: the claim is taken before the document is
// raised, so the race is decided by the row, never by a Go side check two processes can both answer
// no to. Two concurrent runs of the same branch and month serialise on an advisory lock taken
// before anything is read (the bankrec and AP aging idiom), so the second run finds every claim
// spent and charges nothing; the constraint is the belt under the lock.
//
// -- THE MONTH MUST HAVE ENDED ------------------------------------------------------
// The run is refused in words until the house day after the month's last day has begun, exactly as
// the AP revaluation run is refused (the next house day guard): a run pressed inside the month
// would read the books before the month's later invoices, payments and credits were filed, and
// would hold the month's key against the real run.
//
// -- THE WAIVER ---------------------------------------------------------------------
// The Controller's per customer waive flag lives in finance_charge_waivers (0473). A waived
// customer is excluded from the candidates. Setting and clearing the flag are the Financial
// Controller's acts alone (the period close's own controller gate, rechecked in the service the way
// the write off door rechecks IsManager), and both are recorded on the row: who, and when.
//
// -- WHAT THIS SLICE LEAVES TO THE FOLLOW ON ----------------------------------------
// The run is the writer's own method, so the composition root that binds the writer (which the
// counter, the fulfilment path and the load door already reach through) has the run with no new
// seam. The HTTP route for a pressed run and the worker trigger that presses it on the house
// calendar (the evening after each month's last day) are the follow on's doors, as the AP
// revaluation's are; the waiver door follows with them.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

const (
	// financeChargeMinimumCents is the charge's floor: a balance above zero is charged at least
	// $2.00.
	financeChargeMinimumCents = 200
	// financeChargeKeyPrefix is the charge document's idempotency key namespace.
	financeChargeKeyPrefix = "finance-charge:"
	// financeChargeRunLockPrefix is the run's advisory lock namespace, its own word so no charge
	// document key can ever spell it (a customer id is a uuid, "run" is not).
	financeChargeRunLockPrefix = "billing.finance_charge.run:"

	// ActFinanceChargeRun is the audit verb of a pressed run (the write off's own posture: a batch
	// money act carries its trail).
	ActFinanceChargeRun = "FINANCE_CHARGE_RUN"
	// ActFinanceChargeWaive and ActFinanceChargeWaiveCleared are the waiver door's audit verbs.
	ActFinanceChargeWaive        = "FINANCE_CHARGE_WAIVE"
	ActFinanceChargeWaiveCleared = "FINANCE_CHARGE_WAIVE_CLEARED"

	// RoleCodeFinanceChargeWaiver is the only role that may set or clear a customer's finance
	// charge waiver: the Financial Controller, the period close's own controller gate (the same
	// door the write off limit and the head office ruling pass through). The staff tables are the
	// role's one speller; this constant only names the answer the waiver door needs from them.
	RoleCodeFinanceChargeWaiver = "FINANCIAL_CONTROLLER"
)

// financeChargeKey is the charge document's idempotency key: one document per branch, customer and
// house month, the same triple the run table's unique constraint names.
func financeChargeKey(branchID, month, customerID string) string {
	return financeChargeKeyPrefix + branchID + ":" + month + ":" + customerID
}

// FinanceChargeMonthNotEndedError is the run's refusal while its month is still open (the AP
// revaluation's next house day guard, the same shape): the words name the month, its last day, the
// next house day the run belongs to, and the dealer's own day.
type FinanceChargeMonthNotEndedError struct {
	Month, MonthEnd, NextHouseDay, Today string
}

func (e *FinanceChargeMonthNotEndedError) Error() string {
	return "billing: month " + e.Month + " ends on " + e.MonthEnd +
		"; its finance charges belong to the next house day " + e.NextHouseDay +
		", which has not begun (the dealer's day is " + e.Today + ")"
}

// FinanceChargeRequest names one run: a branch's books, a house month, and who pressed it.
type FinanceChargeRequest struct {
	BranchID string `json:"branch_id"`
	Month    string `json:"month"`
	// ActorID is who pressed the run, stamped on every charge row and the audit act. Empty is the
	// worker's own press.
	ActorID string `json:"actor_id"`
}

// FinanceCharge is one customer's charge in a run's answer.
type FinanceCharge struct {
	CustomerID   string `json:"customer_id"`
	PastDueCents int64  `json:"past_due_cents"`
	ChargeCents  int64  `json:"charge_cents"`
	InvoiceID    string `json:"invoice_id"`
	InvoiceNo    string `json:"invoice_no"`
}

// FinanceChargeRunResult is the run's answer: what was measured, what was charged, and what a
// second press of the same month would find already charged.
type FinanceChargeRunResult struct {
	BranchID string `json:"branch_id"`
	Month    string `json:"month"`
	// MonthEnd is the month's last house day, the day the balance was measured as of.
	MonthEnd string `json:"month_end"`
	// Charged is the customers this run charged; AlreadyCharged is those a prior run had.
	Charged        int             `json:"charged"`
	AlreadyCharged int             `json:"already_charged"`
	ChargedCents   int64           `json:"charged_cents"`
	PastDueCents   int64           `json:"past_due_cents"`
	Charges        []FinanceCharge `json:"charges"`
}

// parseFinanceChargeMonth reads a strict YYYY-MM house month and answers its year and month.
func parseFinanceChargeMonth(month string) (int, int, error) {
	month = strings.TrimSpace(month)
	if len(month) != 7 || month[4] != '-' {
		return 0, 0, httpx.BadRequestf("billing: month must be a YYYY-MM house month")
	}
	y, err := strconv.Atoi(month[:4])
	if err != nil || y < 1 {
		return 0, 0, httpx.BadRequestf("billing: month must be a YYYY-MM house month")
	}
	m, err := strconv.Atoi(month[5:])
	if err != nil || m < 1 || m > 12 {
		return 0, 0, httpx.BadRequestf("billing: month must be a YYYY-MM house month")
	}
	return y, m, nil
}

// financeChargeMonthBounds answers the month's first instant (its first house midnight), its last
// house day, and its last instant (the last nanosecond of its last day, the instant the balance is
// measured as of), all in the dealer's own zone. A month end computed at Greenwich midnight would
// sit inside the dealer's previous evening, charging a month end run into the month before it.
func financeChargeMonthBounds(year, month int, loc *time.Location) (start, endInstant, lastDay time.Time) {
	start = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	firstOfNext := start.AddDate(0, 1, 0)
	lastDay = firstOfNext.AddDate(0, 0, -1)
	endInstant = time.Date(lastDay.Year(), lastDay.Month(), lastDay.Day(), 23, 59, 59, 999999999, loc)
	return start, endInstant, lastDay
}

// financeChargeCents is the charge on a past due balance: one and a half percent in integer cents
// rounded half to even (banker's rounding, the house money rule for a halved cent), with the two
// dollar minimum on any balance above zero. A balance at or below zero charges nothing.
func financeChargeCents(pastDueCents int64) int64 {
	if pastDueCents <= 0 {
		return 0
	}
	charge := roundHalfEvenDiv(pastDueCents*15, 1000)
	if charge < financeChargeMinimumCents {
		charge = financeChargeMinimumCents
	}
	return charge
}

// roundHalfEvenDiv divides and rounds half to even: a remainder of exactly half rounds to the even
// neighbour, every larger remainder away from it. num must not be negative (the callers' balances
// are positive by the time they are charged).
func roundHalfEvenDiv(num, den int64) int64 {
	q, r := num/den, num%den
	twice := r * 2
	if twice > den || (twice == den && q%2 != 0) {
		q++
	}
	return q
}

// financeChargeCandidatesSQL reads the run's candidates on the caller's transaction: each customer
// of the branch whose overdue trade balance at the as-of instant is above zero, with that balance.
//
// The balance expression is the aging's own dated rebuild (agingSelect's invoices arm, verbatim in
// shape): the document's total plus the dated ledger rows tagged to it, less the dated payment
// allocations (a reversing payment's contra gives its share back) and reapplications. It is the
// same reconstruction the aging and the 1100 tie-out run, so the run's figures and the aging's
// agree by construction.
//
// The candidate set is INVOICES alone, and trade invoices alone:
//
//   - doc_type 'INVOICE': a credit memo is its own document with its own writer, never a balance to
//     charge (an applied credit has already reduced the invoice balance it is counted against).
//   - source_kind <> 'FINANCE_CHARGE': an earlier charge document is never itself charged, the
//     compounding exclusion the plan rules out.
//   - source_kind <> 'SUPPLIER_DEPOSIT': a deposit request is money for goods not yet delivered
//     (a liability, not a trade receivable), so a past due deposit draws no charge.
//   - due date more than 30 days before the month's last day, strictly: a balance exactly 30 days
//     past due is current enough, one at 31 days is charged.
//   - the customer carries no ACTIVE waiver (a waiver row whose cleared_at IS NULL).
//
// POLICY, kept in this one place: the balance is the INVOICES arm of the aging alone. A standalone
// credit (an unapplied CREDIT_MEMO, ADJUSTMENT or WRITE_OFF row not tagged to an invoice, the aging's
// other arm) does NOT reduce the charged balance, so a customer with a past due invoice and an equal
// unapplied credit is still charged. That is as built and is a question for Colton; changing the
// rule means netting that arm in the overdue CTE below, and nowhere else.
//
// Parameters: $1 the branch, $2 the month's last house day, $3 the month's last instant.
const financeChargeCandidatesSQL = `
	WITH overdue AS (
		SELECT i.customer_id,
		       i.total_cents
		         + COALESCE((SELECT SUM(t.amount_cents) FROM customer_transactions t
		             WHERE t.source_type = 'invoice' AND t.source_ref_id = i.id
		               AND t.txn_type <> i.doc_type AND t.created_at <= $3::timestamptz), 0)
		         - COALESCE((SELECT SUM(CASE WHEN p.reverses_payment_id IS NULL THEN a.amount_cents ELSE -a.amount_cents END)
		             FROM payment_allocations a JOIN payments p ON p.id = a.payment_id
		             WHERE a.invoice_id = i.id AND a.created_at <= $3::timestamptz), 0)
		         - COALESCE((SELECT SUM(ra.amount_cents) FROM payment_reapplications ra
		             WHERE ra.invoice_id = i.id AND ra.created_at <= $3::timestamptz), 0)
		       AS bal
		  FROM invoices i
		 WHERE i.branch_id = $1::uuid
		   AND i.doc_type = 'INVOICE'
		   AND i.source_kind NOT IN ('FINANCE_CHARGE', 'SUPPLIER_DEPOSIT')
		   AND i.status IN ('FINALIZED','PARTIALLY_PAID','PAID')
		   AND COALESCE(i.finalized_at, i.created_at) <= $3::timestamptz
		   AND i.due_date < $2::date - 30
		   AND NOT EXISTS (SELECT 1 FROM finance_charge_waivers w
		           WHERE w.customer_id = i.customer_id AND w.cleared_at IS NULL)
	)
	SELECT customer_id::text, SUM(bal) AS past_due
	  FROM overdue
	 GROUP BY customer_id
	HAVING SUM(bal) > 0
	 ORDER BY customer_id`

// RunFinanceCharges runs one branch's finance charges for one house month: the measurement, the
// claims and the documents, in one transaction. See the file header for the whole ruling.
func (w *InvoiceWriter) RunFinanceCharges(ctx context.Context, in FinanceChargeRequest) (FinanceChargeRunResult, error) {
	if w.pool == nil {
		return FinanceChargeRunResult{}, errNoDB
	}
	branchID := strings.TrimSpace(in.BranchID)
	if branchID == "" {
		return FinanceChargeRunResult{}, httpx.BadRequestf("billing: branch_id is required, a finance charge run is one branch's books")
	}
	year, month, err := parseFinanceChargeMonth(in.Month)
	if err != nil {
		return FinanceChargeRunResult{}, err
	}
	monthWords := fmt.Sprintf("%04d-%02d", year, month)

	// The dealer's day, decided BEFORE the transaction opens (a zone read must not hold a
	// connection beside an open transaction).
	zone, err := houseday.Resolve(ctx, w.zone)
	if err != nil {
		return FinanceChargeRunResult{}, err
	}
	loc := zone.Location()
	monthStart, monthEndInstant, lastDay := financeChargeMonthBounds(year, month, loc)
	// The next house day guard: the run belongs to the day after the month's last day, and only
	// once that day has begun on the dealer's clock.
	nextHouseDay := monthStart.AddDate(0, 1, 0)
	if w.now().In(loc).Before(nextHouseDay) {
		return FinanceChargeRunResult{}, &FinanceChargeMonthNotEndedError{
			Month:        monthWords,
			MonthEnd:     lastDay.Format("2006-01-02"),
			NextHouseDay: nextHouseDay.Format("2006-01-02"),
			Today:        zone.Today(w.now),
		}
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return FinanceChargeRunResult{}, fmt.Errorf("billing: begin the finance charge run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One run lock, taken before anything is read: two concurrent runs of the same branch and month
	// serialise here, and the second reads the first's claims and charges nothing. Without it the
	// two runs could interleave their per customer claims and deadlock on the unique index.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		financeChargeRunLockPrefix+branchID+":"+monthWords); err != nil {
		return FinanceChargeRunResult{}, fmt.Errorf("billing: lock the finance charge run: %w", err)
	}

	// The branch must be a branch of the books (the locations anchor every branch keyed write
	// reads), so the run's documents stamp a real org, and a mistyped branch is refused in words
	// rather than charging a branch that is not one.
	var branchOrg *string
	if err := tx.QueryRow(ctx,
		`SELECT org_id::text FROM locations WHERE id = $1::uuid AND loc_type = 'BRANCH'`, branchID,
	).Scan(&branchOrg); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FinanceChargeRunResult{}, httpx.BadRequestf("billing: branch_id %s is not a branch", branchID)
		}
		return FinanceChargeRunResult{}, fmt.Errorf("billing: read the run's branch: %w", err)
	}

	rows, err := tx.Query(ctx, financeChargeCandidatesSQL,
		branchID, lastDay.Format("2006-01-02"), monthEndInstant)
	if err != nil {
		return FinanceChargeRunResult{}, fmt.Errorf("billing: read the finance charge candidates: %w", err)
	}
	type candidate struct {
		customerID string
		pastDue    int64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.customerID, &c.pastDue); err != nil {
			rows.Close()
			return FinanceChargeRunResult{}, fmt.Errorf("billing: scan a finance charge candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return FinanceChargeRunResult{}, fmt.Errorf("billing: the finance charge candidates: %w", err)
	}
	rows.Close()

	res := FinanceChargeRunResult{
		BranchID: branchID,
		Month:    monthWords,
		MonthEnd: lastDay.Format("2006-01-02"),
		Charges:  []FinanceCharge{},
	}
	var actor any
	if id := strings.TrimSpace(in.ActorID); id != "" {
		actor = id
	}
	for _, c := range candidates {
		charge := financeChargeCents(c.pastDue)
		if charge <= 0 {
			continue
		}
		// The claim first: the row decides the race, the document follows in the same transaction.
		// A claim that collides answers nothing and the customer is already charged, whichever run
		// won it.
		var claimID string
		err := tx.QueryRow(ctx, `
			INSERT INTO finance_charge_runs
			  (branch_id, customer_id, month, as_of_date, past_due_cents, charge_cents, created_by)
			VALUES ($1::uuid, $2::uuid, $3, $4::date, $5, $6, $7::uuid)
			ON CONFLICT (branch_id, customer_id, month) DO NOTHING
			RETURNING id::text`,
			branchID, c.customerID, monthWords, res.MonthEnd, c.pastDue, charge, actor).Scan(&claimID)
		if errors.Is(err, pgx.ErrNoRows) {
			res.AlreadyCharged++
			continue
		}
		if err != nil {
			return FinanceChargeRunResult{}, fmt.Errorf("billing: claim the finance charge: %w", err)
		}

		// The charge is its own invoice through the writer, on this transaction: FINALIZED at the
		// charge, one line whose words say what the document is and the month it covers, billed as
		// stated with no assessed tax (the plan's charge is the whole amount, and the journal is
		// the two legs DR 1100 / CR finance charge income). The document is dated by the
		// customer's own terms off the writer's clock, the writer's invariant for every document
		// (TM-1); its due day moves the aging's display alone, since a charge document never feeds
		// a later run.
		var noTax int64
		doc, err := w.writeCore(ctx, tx, OrderInvoiceFacts{
			CustomerID: c.customerID, BranchID: branchID,
			ClosesOrder: false,
			Lines: []OrderInvoiceLine{{
				LineNo: 1, Description: "Finance charge for " + monthWords,
				QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
				UnitPriceCents: charge, PriceUOM: "EA", LineTotalCents: charge,
			}},
			AgreedTaxCents: &noTax,
			Memo:           "finance charge for " + monthWords,
		}, SourceFinanceCharge, financeChargeKey(branchID, monthWords, c.customerID), "")
		if err != nil {
			return FinanceChargeRunResult{}, fmt.Errorf("billing: raise the finance charge document: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE finance_charge_runs SET invoice_id = $2::uuid WHERE id = $1::uuid`, claimID, doc.InvoiceID); err != nil {
			return FinanceChargeRunResult{}, fmt.Errorf("billing: stamp the charge's document: %w", err)
		}
		res.Charged++
		res.ChargedCents += charge
		res.PastDueCents += c.pastDue
		res.Charges = append(res.Charges, FinanceCharge{
			CustomerID: c.customerID, PastDueCents: c.pastDue, ChargeCents: charge,
			InvoiceID: doc.InvoiceID, InvoiceNo: doc.InvoiceNo,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return FinanceChargeRunResult{}, fmt.Errorf("billing: commit the finance charge run: %w", err)
	}
	if w.audit != nil {
		_ = w.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActFinanceChargeRun, EntityType: "branch", EntityID: branchID,
			ActorID: in.ActorID, Purpose: "FINANCE_CHARGE_RUN",
			Extra: map[string]any{
				"month": monthWords, "charged": res.Charged,
				"charged_cents": res.ChargedCents, "already_charged": res.AlreadyCharged,
			},
		})
	}
	return res, nil
}

// -- the Controller's waiver ---------------------------------------------------------

// FinanceChargeWaiverRoleError is the refusal a caller without the Controller role gets (403 shaped
// when a handler lands: the flag is a Controller's act, not a manager's).
type FinanceChargeWaiverRoleError struct{ RoleCode string }

func (e *FinanceChargeWaiverRoleError) Error() string {
	return "billing: setting or clearing a customer's finance charge waiver is the Financial Controller's act"
}

// ErrFinanceChargeWaiverReasonRequired is the reason rule, as its own error so a UI can prompt for
// it (the write off's own shape).
var ErrFinanceChargeWaiverReasonRequired = errors.New("billing: a waiver needs a reason")

// FinanceChargeWaiverInput is one waiver act: the customer, the Controller's words for why, the
// actor, and the caller's controller role answer resolved from the staff context's role code,
// never out of the request body.
type FinanceChargeWaiverInput struct {
	CustomerID   string
	Reason       string
	ActorID      string
	IsController bool
}

// FinanceChargeWaiverView is the waiver as a read answers it.
type FinanceChargeWaiverView struct {
	CustomerID string `json:"customer_id"`
	Reason     string `json:"reason"`
	// Active is true while the waiver holds the customer out of the runs.
	Active    bool   `json:"active"`
	WaivedBy  string `json:"waived_by,omitempty"`
	WaivedAt  string `json:"waived_at"`
	ClearedBy string `json:"cleared_by,omitempty"`
	ClearedAt string `json:"cleared_at,omitempty"`
}

// FinanceChargeWaiverService is the Controller's waiver door over the repository write. The service
// re-checks the controller role whatever called it, the write off's own double gate posture.
type FinanceChargeWaiverService struct {
	repo  *Repository
	audit audit.Recorder
	now   func() time.Time
}

// NewFinanceChargeWaiverService wires the waiver service over the billing repository.
func NewFinanceChargeWaiverService(repo *Repository) *FinanceChargeWaiverService {
	return &FinanceChargeWaiverService{repo: repo, now: time.Now}
}

// WithAudit attaches the audit recorder (best effort: an audit failure never blocks the books).
func (s *FinanceChargeWaiverService) WithAudit(r audit.Recorder) *FinanceChargeWaiverService {
	if r != nil {
		s.audit = r
	}
	return s
}

// WithClock overrides the clock (tests).
func (s *FinanceChargeWaiverService) WithClock(now func() time.Time) *FinanceChargeWaiverService {
	if now != nil {
		s.now = now
	}
	return s
}

// SetWaiver waives a customer's finance charges: the Controller's act, reason required, recorded
// with who and when. Setting it again refreshes the record and reactivates the flag.
func (s *FinanceChargeWaiverService) SetWaiver(ctx context.Context, in FinanceChargeWaiverInput) (FinanceChargeWaiverView, error) {
	if !in.IsController {
		return FinanceChargeWaiverView{}, &FinanceChargeWaiverRoleError{}
	}
	view, err := s.repo.SetFinanceChargeWaiver(ctx, in, s.now())
	if err != nil {
		return FinanceChargeWaiverView{}, err
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActFinanceChargeWaive, EntityType: "customer", EntityID: in.CustomerID,
			ActorID: in.ActorID, Purpose: "FINANCE_CHARGE_WAIVE", Reason: in.Reason,
		})
	}
	return view, nil
}

// ClearWaiver releases a waived customer back to the runs: the Controller's act, reason required,
// recorded with who and when. A customer with no active waiver is refused in words, the reversal's
// own shape (clearing nothing is not an act).
func (s *FinanceChargeWaiverService) ClearWaiver(ctx context.Context, in FinanceChargeWaiverInput) (FinanceChargeWaiverView, error) {
	if !in.IsController {
		return FinanceChargeWaiverView{}, &FinanceChargeWaiverRoleError{}
	}
	view, err := s.repo.ClearFinanceChargeWaiver(ctx, in, s.now())
	if err != nil {
		return FinanceChargeWaiverView{}, err
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActFinanceChargeWaiveCleared, EntityType: "customer", EntityID: in.CustomerID,
			ActorID: in.ActorID, Purpose: "FINANCE_CHARGE_WAIVE_CLEAR", Reason: in.Reason,
		})
	}
	return view, nil
}

// Waiver reads a customer's waiver: the view and whether an active waiver holds. A customer with no
// row answers false and an empty view.
func (s *FinanceChargeWaiverService) Waiver(ctx context.Context, customerID string) (FinanceChargeWaiverView, bool, error) {
	return s.repo.FinanceChargeWaiver(ctx, customerID)
}

// SetFinanceChargeWaiver writes the waiver row: an insert for a customer never waived, an
// overwrite that reactivates the flag (and clears the clearing) for one waived before. The row is
// the flag's whole history, so it is never deleted, only stamped.
func (r *Repository) SetFinanceChargeWaiver(ctx context.Context, in FinanceChargeWaiverInput, now time.Time) (FinanceChargeWaiverView, error) {
	if r.pool == nil {
		return FinanceChargeWaiverView{}, errNoDB
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return FinanceChargeWaiverView{}, ErrFinanceChargeWaiverReasonRequired
	}
	if err := r.requireFinanceChargeCustomer(ctx, in.CustomerID); err != nil {
		return FinanceChargeWaiverView{}, err
	}
	var v FinanceChargeWaiverView
	var waivedAt time.Time
	var waivedBy *string
	if id := strings.TrimSpace(in.ActorID); id != "" {
		waivedBy = &id
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO finance_charge_waivers (customer_id, reason, waived_by, waived_at)
		VALUES ($1::uuid, $2, $3::uuid, $4)
		ON CONFLICT (customer_id) DO UPDATE
		       SET reason = EXCLUDED.reason, waived_by = EXCLUDED.waived_by,
		           waived_at = EXCLUDED.waived_at, cleared_by = NULL, cleared_at = NULL
		RETURNING customer_id::text, reason, COALESCE(waived_by::text,''),
		          waived_at, cleared_at IS NULL`,
		in.CustomerID, in.Reason, waivedBy, now).Scan(
		&v.CustomerID, &v.Reason, &v.WaivedBy, &waivedAt, &v.Active)
	if err != nil {
		return FinanceChargeWaiverView{}, fmt.Errorf("billing: set the finance charge waiver: %w", err)
	}
	v.WaivedAt = waivedAt.UTC().Format(time.RFC3339)
	return v, nil
}

// ClearFinanceChargeWaiver stamps the clearing on an ACTIVE waiver: who and when. A customer with no
// active waiver is refused in words.
func (r *Repository) ClearFinanceChargeWaiver(ctx context.Context, in FinanceChargeWaiverInput, now time.Time) (FinanceChargeWaiverView, error) {
	if r.pool == nil {
		return FinanceChargeWaiverView{}, errNoDB
	}
	if strings.TrimSpace(in.Reason) == "" {
		return FinanceChargeWaiverView{}, ErrFinanceChargeWaiverReasonRequired
	}
	if err := r.requireFinanceChargeCustomer(ctx, in.CustomerID); err != nil {
		return FinanceChargeWaiverView{}, err
	}
	var v FinanceChargeWaiverView
	var waivedAt, clearedAt time.Time
	var clearedBy *string
	if id := strings.TrimSpace(in.ActorID); id != "" {
		clearedBy = &id
	}
	err := r.pool.QueryRow(ctx, `
		UPDATE finance_charge_waivers
		   SET cleared_by = $2::uuid, cleared_at = $3
		 WHERE customer_id = $1::uuid AND cleared_at IS NULL
		RETURNING customer_id::text, reason, COALESCE(waived_by::text,''),
		          waived_at, COALESCE(cleared_by::text,''), cleared_at, false`,
		in.CustomerID, clearedBy, now).Scan(
		&v.CustomerID, &v.Reason, &v.WaivedBy, &waivedAt, &v.ClearedBy, &clearedAt, &v.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return FinanceChargeWaiverView{}, httpx.BadRequestf("billing: customer %s has no active finance charge waiver to clear", in.CustomerID)
	}
	if err != nil {
		return FinanceChargeWaiverView{}, fmt.Errorf("billing: clear the finance charge waiver: %w", err)
	}
	v.WaivedAt = waivedAt.UTC().Format(time.RFC3339)
	v.ClearedAt = clearedAt.UTC().Format(time.RFC3339)
	return v, nil
}

// FinanceChargeWaiver reads a customer's waiver row, active or not.
func (r *Repository) FinanceChargeWaiver(ctx context.Context, customerID string) (FinanceChargeWaiverView, bool, error) {
	if r.pool == nil {
		return FinanceChargeWaiverView{}, false, errNoDB
	}
	var v FinanceChargeWaiverView
	var waivedAt time.Time
	var clearedAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT customer_id::text, reason, COALESCE(waived_by::text,''),
		       waived_at, COALESCE(cleared_by::text,''), cleared_at, cleared_at IS NULL
		  FROM finance_charge_waivers WHERE customer_id = $1::uuid`, customerID,
	).Scan(&v.CustomerID, &v.Reason, &v.WaivedBy, &waivedAt, &v.ClearedBy, &clearedAt, &v.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return FinanceChargeWaiverView{}, false, nil
	}
	if err != nil {
		return FinanceChargeWaiverView{}, false, fmt.Errorf("billing: read the finance charge waiver: %w", err)
	}
	v.WaivedAt = waivedAt.UTC().Format(time.RFC3339)
	if clearedAt != nil {
		v.ClearedAt = clearedAt.UTC().Format(time.RFC3339)
	}
	return v, true, nil
}

// requireFinanceChargeCustomer refuses a waiver on nobody: the customers table is the one row every
// other billing read of an account goes through.
func (r *Repository) requireFinanceChargeCustomer(ctx context.Context, customerID string) error {
	var one int
	err := r.pool.QueryRow(ctx, `SELECT 1 FROM customers WHERE id = $1::uuid`, customerID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.BadRequestf("billing: customer %s does not exist", customerID)
	}
	if err != nil {
		return fmt.Errorf("billing: read the waiver's customer: %w", err)
	}
	return nil
}
