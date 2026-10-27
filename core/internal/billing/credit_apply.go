package billing

// credit_apply.go, FW1-12b: APPLYING AN ACCOUNT CREDIT OR AN OPEN CREDIT MEMO TO A LATER INVOICE.
//
// -- THE TWO KINDS OF CREDIT, AND WHERE EACH ONE LIVES -------------------------------------------
// A customer can hold value in two places, and both must reach this door:
//
//   · An OPEN CREDIT MEMO is a document: a CREDIT_MEMO invoice (0146) whose balance_cents is a
//     NEGATIVE magnitude. It rides the invoices arm of ARBalanceCents. Issued by the credit memo
//     writer (credit_memo.go) or the POS return's offset rows.
//   · An ACCOUNT CREDIT is a subledger row family: customer_transactions whose txn_type is one of
//     the 0009 vocabulary's AR kinds, whose source_type is NOT 'invoice', and whose amount is
//     negative (the POS return's store credit remainder, a supplier deposit credited back). It
//     rides the non-invoice arm of ARBalanceCents, grouped by (customer, branch, source_type,
//     source_ref_id) exactly as agingSelect groups it.
//
// -- WHAT AN APPLICATION IS, AND WHAT IT POSTS ----------------------------------------------------
// Applying a credit to a later invoice moves value FROM the credit TO the invoice. Both live in AR
// (account 1100): the credit's open figure shrinks by X and the invoice's balance shrinks by X, so
// the account's AR total is unchanged and the control account has nothing to book. THE APPLICATION
// EMITS NO EVENT AND POSTS NO JOURNAL. This is the deposit cancel's closed part's discipline
// (deposit_cancel.go): a subledger-only act between two AR positions needs no journal, and a journal
// that debits and credits 1100 by the same figure would be noise the trial balance carries forever.
// The 1100 tie holds by construction: ARBalanceCents is unchanged, the aging grand total is
// unchanged, and GL 1100 was never touched.
//
// The allocation rule this follows is the brief's first option: "posts nothing new to 1100 in total
// (both sides are 1100)". No posting rule names a credit application journal (gl/posting.go's topic
// map has no such topic), so none is invented here.
//
// -- THE SUBLEDGER ROWS, AND WHY EACH ONE CARRIES THE TAG IT CARRIES ------------------------------
//   · The invoice side gets ONE row: txn_type ADJUSTMENT, source_type 'invoice', source_ref_id the
//     invoice's id. source_type 'invoice' is LOAD-BEARING (credit_memo.go's rule): the row mirrors a
//     document balance that already moved on the invoices arm, and a non-invoice tag would count the
//     same money twice and break the 1100 tie-out by exactly the application.
//   · A MEMO credit gets ONE mirror row: txn_type ADJUSTMENT, source_type 'invoice', source_ref_id
//     the memo's id, amount POSITIVE (the credit shrinking is money owed again). Same rule: the memo
//     document's own balance moved on the invoices arm. The type is ADJUSTMENT, not CREDIT_MEMO,
//     ON PURPOSE (S6 P1-4): 626's past date rebuild keeps an invoice tagged row only when its
//     txn_type differs from the document's own doc_type, so a CREDIT_MEMO tagged row on a
//     CREDIT_MEMO document would drop out of a past date's aging by exactly the application.
//   · An ACCOUNT credit gets ONE offsetting row INSIDE ITS OWN GROUP: the anchor row's txn_type,
//     source_type and source_ref_id, amount POSITIVE. That is the NSF fee payment's spelling
//     (payments' applyAllocations): the aging groups non-invoice rows by (source_type,
//     source_ref_id) and keeps only groups that do not sum to zero, so the offset nets the credit's
//     group to zero and it leaves the aging, and ARBalanceCents' second arm nets to zero with it.
//
// -- IDEMPOTENCY WITHOUT A NEW TABLE --------------------------------------------------------------
// The act is recorded on the TARGET INVOICE's metadata, under the target invoice's own row lock
// (payments stamped NSF fee allocations into payments.metadata under the payment row lock; this is
// the same discipline on billing's own table, which nothing overwrites wholesale). The record is
// keyed by the request's idempotency key: a replay is judged under the lock, the same key with the
// same body answers the first result and writes nothing, a spent key with another body is refused.
// The record is also what a reversal reads to restore both sides, so no application exists anywhere
// but on the invoice it moved.
//
// -- THE LOCKS -------------------------------------------------------------------------------------
// The one money lock order (payments/LOCK-ORDER.md) as the reapply walks it: the named invoice rows
// FOR NO KEY UPDATE in ascending id order, then the credit's own lock step, an ACCOUNT credit's
// group anchor row FOR UPDATE (lockCreditGroupAnchor) or a MEMO credit's document row FOR NO KEY
// UPDATE (lockCreditMemoRow; the apply takes the memo among the invoice rows ascending), and only
// then the first balance move's draw of the invoices counter. No payments counter is drawn: nothing
// here stamps a payments row. The credit's lock before the counter in BOTH doors of either kind is
// what keeps a staged reversal and apply from deadlocking (SQLSTATE 40P01, rounds 4 and 5): a
// reversal once drew the counter first while the apply took the group's or the memo's row first,
// so each could hold what the other needed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ActCreditApplied and ActCreditApplicationReversed are the audit verbs.
const (
	ActCreditApplied             = "CREDIT_APPLIED"
	ActCreditApplicationReversed = "CREDIT_APPLICATION_REVERSED"
)

// The two kinds of credit the door takes.
const (
	CreditKindMemo    = "MEMO"
	CreditKindAccount = "ACCOUNT"
)

// CreditApplicationNotOpen is the refusal for a credit that holds nothing open.
var CreditApplicationNotOpen = errors.New("billing: that credit has nothing left to apply")

// ErrCreditApplicationReasonRequired is the reason rule, as its own error so a UI can prompt for it.
var ErrCreditApplicationReasonRequired = errors.New("billing: a reason is required")

// NoSuchCreditError names a credit the request may not touch: missing, another account's, outside
// the caller's branch reach, or not a credit at all. The route answers it 404, the same no
// existence leak the reapply door carries across the branch wall.
type NoSuchCreditError struct{ CreditID string }

func (e NoSuchCreditError) Error() string { return "billing: no such credit: " + e.CreditID }

// NoSuchInvoiceError names an invoice the request may not apply against: missing, another
// account's, or outside the caller's branch reach. The route answers it 404.
type NoSuchInvoiceError struct{ InvoiceID string }

func (e NoSuchInvoiceError) Error() string { return "billing: no such invoice: " + e.InvoiceID }

// CreditApplicationConflictError is the spent-key refusal, 409 like the reapply's.
type CreditApplicationConflictError struct{ Msg string }

func (e CreditApplicationConflictError) Error() string { return e.Msg }

// ApplyCreditInput is one application request.
type ApplyCreditInput struct {
	CustomerID string // the account both the credit and the invoice belong to
	// CreditKind selects MEMO (a CREDIT_MEMO document, CreditID the invoice id) or ACCOUNT
	// (a non-invoice subledger row family, CreditID the id of one of its negative rows).
	CreditKind  string
	CreditID    string
	InvoiceID   string
	AmountCents int64
	Reason      string
	ActorID     string
	// IdempotencyKey is mandatory. The application is recorded under it, and a retry under the same
	// key with the same body answers the first result and writes nothing.
	IdempotencyKey string
	// CanAccessBranch is the caller's branch reach; nil reaches every branch.
	CanAccessBranch func(branchID string) bool
	// OrgID is the caller's org slug scope id; an invoice of another org is refused. Empty on
	// callers that carry none, and then nothing is judged.
	OrgID string
}

// CreditApplicationLine is the invoice's side of the answer.
type CreditApplicationLine struct {
	InvoiceID         string `json:"invoice_id"`
	InvoiceNo         string `json:"invoice_no"`
	AppliedCents      int64  `json:"applied_cents"`
	Outcome           string `json:"outcome"` // CLOSES or PART, the settle's own words
	BalanceAfterCents int64  `json:"balance_after_cents"`
}

// CreditApplication is the answer.
type CreditApplication struct {
	ApplicationID   string                  `json:"application_id"`
	CreditKind      string                  `json:"credit_kind"`
	CreditID        string                  `json:"credit_id"`
	InvoiceID       string                  `json:"invoice_id"`
	InvoiceNo       string                  `json:"invoice_no"`
	AppliedCents    int64                   `json:"applied_cents"`
	CreditOpenAfter int64                   `json:"credit_open_after_cents"`
	Lines           []CreditApplicationLine `json:"lines"`
	Replayed        bool                    `json:"replayed"`
}

// creditApplyClock is the writer's clock seam (tests).
type creditApplyClock interface{ Now() time.Time }

// CreditApplyService is the permission-shaped door over the repository write. The handler gates the
// route with RequireManager AND the service re-checks IsManager: the route gate stops the request,
// the service check stops an in-process caller that bypassed HTTP, and neither is a substitute for
// the other (the reversal handler's own rule).
type CreditApplyService struct {
	repo  *Repository
	audit audit.Recorder
	now   func() time.Time
	zone  *houseday.Zone
}

// NewCreditApplyService wires the service over the billing repository.
func NewCreditApplyService(repo *Repository) *CreditApplyService {
	return &CreditApplyService{repo: repo, now: time.Now}
}

// WithAudit attaches the audit recorder (best effort: an audit failure never blocks the books, the
// finstmt export trail's own rule, but the act is recorded whenever a recorder is wired).
func (s *CreditApplyService) WithAudit(r audit.Recorder) *CreditApplyService { s.audit = r; return s }

// WithClock overrides the clock (tests).
func (s *CreditApplyService) WithClock(now func() time.Time) *CreditApplyService {
	if now != nil {
		s.now = now
	}
	return s
}

// WithZone binds the dealer's house zone, so the record's applied_at day is the dealer's day.
func (s *CreditApplyService) WithZone(z *houseday.Zone) *CreditApplyService { s.zone = z; return s }

// Apply applies a credit to a later invoice. Manager only, reason required, audited.
func (s *CreditApplyService) Apply(ctx context.Context, in ApplyCreditInput, isManager bool) (CreditApplication, error) {
	if !isManager {
		return CreditApplication{}, errors.New("billing: applying a credit to an invoice is a manager action")
	}
	in, err := validateApplyCredit(in)
	if err != nil {
		return CreditApplication{}, err
	}
	res, err := s.repo.ApplyCredit(ctx, in, s.now())
	if err != nil {
		return CreditApplication{}, err
	}
	if s.audit != nil && !res.Replayed {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActCreditApplied, EntityType: "invoice", EntityID: in.InvoiceID,
			ActorID: in.ActorID, Purpose: "CREDIT_APPLY", Reason: in.Reason,
			Extra: map[string]any{
				"application_id": res.ApplicationID,
				"credit_kind":    in.CreditKind,
				"credit_id":      in.CreditID,
				"applied_cents":  res.AppliedCents,
				"invoice_no":     res.InvoiceNo,
				"balance_after":  res.Lines[0].BalanceAfterCents,
			},
		})
	}
	return res, nil
}

// validateApplyCredit is the request check that runs before any database is consulted, the validateApply
// posture: ids normalised to one canonical spelling, money positive, the kind in its vocabulary.
func validateApplyCredit(in ApplyCreditInput) (ApplyCreditInput, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return in, ErrCreditApplicationReasonRequired
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return in, httpx.BadRequestf("applying a credit needs an idempotency key")
	}
	if in.CreditKind != CreditKindMemo && in.CreditKind != CreditKindAccount {
		return in, httpx.BadRequestf("credit_kind must be %s or %s", CreditKindMemo, CreditKindAccount)
	}
	if in.AmountCents <= 0 {
		return in, httpx.BadRequestf("the amount applied must be greater than zero")
	}
	for name, raw := range map[string]*string{
		"customer id": &in.CustomerID, "credit id": &in.CreditID, "invoice id": &in.InvoiceID,
	} {
		u, err := uuid.Parse(strings.TrimSpace(*raw))
		if err != nil {
			return in, httpx.BadRequestf("%s must be a uuid", name)
		}
		*raw = u.String()
	}
	in.Reason = strings.TrimSpace(in.Reason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	return in, nil
}

// creditApplicationRecord is what the target invoice's metadata carries for one application. It is
// the whole state of the act: a reversal reads it and restores both sides from it.
type creditApplicationRecord struct {
	ApplicationID string `json:"application_id"`
	CreditKind    string `json:"credit_kind"`
	CreditID      string `json:"credit_id"`
	InvoiceID     string `json:"invoice_id"`
	AmountCents   int64  `json:"amount_cents"`
	Reason        string `json:"reason"`
	AppliedBy     string `json:"applied_by,omitempty"`
	AppliedAt     string `json:"applied_at"`
	// Reversal is set when the application has been undone; the record then stays as the act's history.
	Reversal *creditReversalRecord `json:"reversal,omitempty"`
}

type creditReversalRecord struct {
	Reason     string `json:"reason"`
	ReversedBy string `json:"reversed_by,omitempty"`
	ReversedAt string `json:"reversed_at"`
	Key        string `json:"key"`
}

// creditAppMetadata is the metadata envelope the target invoice carries.
type creditAppMetadata struct {
	CreditApplications map[string]creditApplicationRecord `json:"credit_applications"`
}

// readCreditApplications parses the target invoice's metadata for the application records. No
// metadata, or none of this act's, reads as an empty map.
func readCreditApplications(raw string) (map[string]creditApplicationRecord, error) {
	out := map[string]creditApplicationRecord{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	var m creditAppMetadata
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return out, nil // foreign metadata: not this act's, and never this act's to fail on
	}
	if m.CreditApplications == nil {
		return out, nil
	}
	return m.CreditApplications, nil
}

// ApplyCredit is the repository write. ONE transaction: the invoice rows locked in ascending id
// order, every judgement under the locks, the replay judged under the target invoice's row, then
// the balance moves, the subledger rows and the metadata stamp. A refused request writes nothing.
func (r *Repository) ApplyCredit(ctx context.Context, in ApplyCreditInput, now time.Time) (CreditApplication, error) {
	if r.pool == nil {
		return CreditApplication{}, errNoDB
	}
	in, err := validateApplyCredit(in)
	if err != nil {
		return CreditApplication{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return CreditApplication{}, fmt.Errorf("credit apply: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The two invoice rows (the target, and the memo when the credit is a memo), ascending id, both
	// before any write: the multi invoice order the settle and the returns walk, so this writer can
	// never meet another head on. An ACCOUNT credit's anchor is not an invoice; only the target is
	// locked here.
	rowsToLock := []string{in.InvoiceID}
	if in.CreditKind == CreditKindMemo {
		rowsToLock = []string{in.InvoiceID, in.CreditID}
		if rowsToLock[0] > rowsToLock[1] {
			rowsToLock[0], rowsToLock[1] = rowsToLock[1], rowsToLock[0]
		}
	}
	for _, id := range rowsToLock {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, id); err != nil {
			return CreditApplication{}, fmt.Errorf("credit apply: lock invoice %s: %w", id, err)
		}
	}

	// The target invoice, judged under its own lock. Another account's invoice, one outside the
	// caller's branch reach and a missing one answer the same not found; the rest refuse in words.
	var (
		invNo, invBranch, invStatus, invOrg string
		invBalance, invHoldback             int64
		metadataRaw                         string
	)
	err = tx.QueryRow(ctx, `
SELECT invoice_no::text, balance_cents, holdback_cents, branch_id::text, status,
       COALESCE(org_id::text, ''), COALESCE(metadata::text, '')
  FROM invoices WHERE id = $1::uuid AND customer_id = $2::uuid`, in.InvoiceID, in.CustomerID).
		Scan(&invNo, &invBalance, &invHoldback, &invBranch, &invStatus, &invOrg, &metadataRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditApplication{}, NoSuchInvoiceError{InvoiceID: in.InvoiceID}
		}
		return CreditApplication{}, fmt.Errorf("credit apply: read the invoice: %w", err)
	}
	if in.CanAccessBranch != nil && !in.CanAccessBranch(invBranch) {
		return CreditApplication{}, NoSuchInvoiceError{InvoiceID: in.InvoiceID}
	}
	if in.OrgID != "" && invOrg != "" && invOrg != in.OrgID {
		return CreditApplication{}, httpx.BadRequestf("invoice %s is another org's document, this desk cannot apply a credit to it", invNo)
	}
	if invStatus != "FINALIZED" && invStatus != "PARTIALLY_PAID" {
		return CreditApplication{}, httpx.BadRequestf("invoice %s is not open, a credit can only be applied to an open invoice", invNo)
	}
	payable := invBalance - invHoldback
	if payable < 0 {
		payable = 0
	}

	// The replay is judged under the target invoice's row, so a twin that committed while this
	// request waited is already visible here: the same key and the same body answers the first
	// result and writes nothing, a spent key with another body is refused.
	apps, err := readCreditApplications(metadataRaw)
	if err != nil {
		return CreditApplication{}, err
	}
	if prior, ok := apps[in.IdempotencyKey]; ok {
		if prior.CreditKind == in.CreditKind && prior.CreditID == in.CreditID &&
			prior.InvoiceID == in.InvoiceID && prior.AmountCents == in.AmountCents {
			return r.replayApplication(ctx, tx, prior)
		}
		return CreditApplication{}, CreditApplicationConflictError{
			Msg: "that idempotency key already applied a credit differently; use a new key"}
	}

	// The credit, judged under the locks. kind MEMO: the memo document's row. kind ACCOUNT: the
	// anchor row and its group sum, read the way the aging reads the group.
	var creditOpen int64 // a positive magnitude: what the credit still holds
	var creditBranch, creditOrg string
	switch in.CreditKind {
	case CreditKindMemo:
		var balance int64
		err := tx.QueryRow(ctx, `
SELECT balance_cents, COALESCE(org_id::text, ''), branch_id::text
  FROM invoices
 WHERE id = $1::uuid AND customer_id = $2::uuid AND doc_type = 'CREDIT_MEMO' AND status <> 'VOID'`,
			in.CreditID, in.CustomerID).Scan(&balance, &creditOrg, &creditBranch)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreditApplication{}, NoSuchCreditError{CreditID: in.CreditID}
			}
			return CreditApplication{}, fmt.Errorf("credit apply: read the memo: %w", err)
		}
		// The branch reach is judged directly after the row is read, BEFORE the not open judgement
		// (S6 P1-3): a spent credit at another branch answers the same no such credit as an unknown
		// one, so the credit's state never leaks across the wall.
		if in.CanAccessBranch != nil && !in.CanAccessBranch(creditBranch) {
			return CreditApplication{}, NoSuchCreditError{CreditID: in.CreditID}
		}
		if balance >= 0 {
			return CreditApplication{}, CreditApplicationNotOpen
		}
		creditOpen = -balance
	case CreditKindAccount:
		var groupSourceType, groupRef string
		err := tx.QueryRow(ctx, `
SELECT COALESCE(source_type, ''), COALESCE(source_ref_id, id)::text, COALESCE(branch_id::text, '')
  FROM customer_transactions
 WHERE id = $1::uuid AND customer_id = $2::uuid AND COALESCE(source_type, '') <> 'invoice'
   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO')
   FOR UPDATE`,
			in.CreditID, in.CustomerID).Scan(&groupSourceType, &groupRef, &creditBranch)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreditApplication{}, NoSuchCreditError{CreditID: in.CreditID}
			}
			return CreditApplication{}, fmt.Errorf("credit apply: read the account credit: %w", err)
		}
		// The reach is judged directly after the group's row is read, before any open figure
		// judgement, the memo case's rule.
		if in.CanAccessBranch != nil && !in.CanAccessBranch(creditBranch) {
			return CreditApplication{}, NoSuchCreditError{CreditID: in.CreditID}
		}
		// The anchor step is the shared helper's (round 4): the group's FIRST NEGATIVE row is
		// the lock every writer of the group queues on (S6 P2-1), and it is taken here, before
		// this door's counter draw below. No negative row left means the group holds nothing
		// open.
		anchorFound, err := lockCreditGroupAnchor(ctx, tx, in.CustomerID, groupSourceType, groupRef, creditBranch)
		if err != nil {
			return CreditApplication{}, fmt.Errorf("credit apply: resolve the credit's anchor: %w", err)
		}
		if !anchorFound {
			return CreditApplication{}, CreditApplicationNotOpen
		}
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)
  FROM customer_transactions
 WHERE customer_id = $1::uuid
   AND COALESCE(source_type, '') = $2
   AND COALESCE(source_ref_id, id) = $3::uuid
   AND COALESCE(branch_id::text, '') = $4
   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO')`,
			in.CustomerID, groupSourceType, groupRef, creditBranch).Scan(&creditOpen); err != nil {
			return CreditApplication{}, fmt.Errorf("credit apply: read the credit's group: %w", err)
		}
		if creditOpen >= 0 {
			return CreditApplication{}, CreditApplicationNotOpen
		}
		creditOpen = -creditOpen
	}
	if in.OrgID != "" && creditOrg != "" && creditOrg != in.OrgID {
		return CreditApplication{}, httpx.BadRequestf("that credit belongs to another org")
	}
	if creditBranch != invBranch {
		return CreditApplication{}, httpx.BadRequestf(
			"the credit sits at another branch than invoice %s: a credit applies to its own branch's invoices, so every branch's receivables keep tying to their own ledger", invNo)
	}
	if in.AmountCents > creditOpen {
		return CreditApplication{}, httpx.BadRequestf("the credit has %d cents open; that is more than it holds", creditOpen)
	}
	if in.AmountCents > payable {
		return CreditApplication{}, httpx.BadRequestf("invoice %s can take at most %d cents right now; refresh and retry", invNo, payable)
	}

	// The balance moves, each stamping the INVOICES feed: a credit landing is the change the payer's
	// device must see on its invoices feed, the settle's own posture.
	after := invBalance - in.AmountCents
	outcome := "PART"
	if after <= 0 {
		outcome = "CLOSES"
	}
	if _, err := tx.Exec(ctx, creditFeedSeqCTE+`
UPDATE invoices
   SET balance_cents = $2::bigint, status = CASE WHEN $2::bigint <= 0 THEN 'PAID' ELSE 'PARTIALLY_PAID' END,
       updated_at = now(), seq = (SELECT next_value FROM iseq)
 WHERE id = $1::uuid`, in.InvoiceID, after); err != nil {
		return CreditApplication{}, fmt.Errorf("credit apply: move the invoice: %w", err)
	}
	creditOpenAfter := creditOpen - in.AmountCents
	if in.CreditKind == CreditKindMemo {
		if _, err := tx.Exec(ctx, creditFeedSeqCTE+`
UPDATE invoices
   SET balance_cents = $2::bigint, status = CASE WHEN $2::bigint >= 0 THEN 'PAID' ELSE status END,
       updated_at = now(), seq = (SELECT next_value FROM iseq)
 WHERE id = $1::uuid`, in.CreditID, -creditOpenAfter); err != nil {
			return CreditApplication{}, fmt.Errorf("credit apply: move the memo: %w", err)
		}
	}

	applicationID := uuid.NewString()
	memWords := "credit applied to " + invNo
	if in.Reason != "" {
		memWords += ": " + in.Reason
	}
	if in.CreditKind == CreditKindAccount {
		// Offsetting row INSIDE the credit's own group: the aging nets the group to zero and it
		// leaves the aging, the same spelling as the NSF fee payment.
		runningBefore, err := customerRunningAR(ctx, tx, in.CustomerID)
		if err != nil {
			return CreditApplication{}, fmt.Errorf("credit apply: read the account's AR: %w", err)
		}
		runningAfter := runningBefore + in.AmountCents
		if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions
  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
SELECT customer_id, txn_type, $2, $3, source_type, COALESCE(source_ref_id, id), branch_id, $4, $5
  FROM customer_transactions WHERE id = $1::uuid`,
			in.CreditID, in.AmountCents, runningAfter, memWords, now); err != nil {
			return CreditApplication{}, fmt.Errorf("credit apply: the credit's offsetting row: %w", err)
		}
	}

	// The running account balance after the moves: both sides moved by the same figure, so the
	// account total is unchanged, and both subledger rows carry that one figure.
	runningAR, err := customerRunningAR(ctx, tx, in.CustomerID)
	if err != nil {
		return CreditApplication{}, fmt.Errorf("credit apply: read the account's AR: %w", err)
	}

	// The invoice's side, source_type 'invoice': it mirrors a document balance that already moved on
	// the invoices arm, so the non-invoice arm must never count it (the 1100 tie).
	if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions
  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'ADJUSTMENT', $2, $3, 'invoice', $4::uuid, $5::uuid, $6, $7)`,
		in.CustomerID, -in.AmountCents, runningAR, in.InvoiceID, invBranch, memWords, now); err != nil {
		return CreditApplication{}, fmt.Errorf("credit apply: the invoice's subledger row: %w", err)
	}
	if in.CreditKind == CreditKindMemo {
		if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions
  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'ADJUSTMENT', $2, $3, 'invoice', $4::uuid, $5::uuid, $6, $7)`,
			in.CustomerID, in.AmountCents, runningAR, in.CreditID, invBranch, "credit memo applied: "+memWords, now); err != nil {
			return CreditApplication{}, fmt.Errorf("credit apply: the memo's subledger row: %w", err)
		}
	}

	// The record of the act, on the target invoice's metadata, keyed by the idempotency key: the
	// replay's answer and the reversal's source of truth.
	apps[in.IdempotencyKey] = creditApplicationRecord{
		ApplicationID: applicationID, CreditKind: in.CreditKind, CreditID: in.CreditID,
		InvoiceID: in.InvoiceID, AmountCents: in.AmountCents, Reason: in.Reason,
		AppliedBy: in.ActorID, AppliedAt: now.UTC().Format(time.RFC3339),
	}
	if err := stampCreditApplications(ctx, tx, in.InvoiceID, apps); err != nil {
		return CreditApplication{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return CreditApplication{}, fmt.Errorf("credit apply: commit: %w", err)
	}
	return CreditApplication{
		ApplicationID: applicationID, CreditKind: in.CreditKind, CreditID: in.CreditID,
		InvoiceID: in.InvoiceID, InvoiceNo: invNo, AppliedCents: in.AmountCents,
		CreditOpenAfter: creditOpenAfter,
		Lines: []CreditApplicationLine{{
			InvoiceID: in.InvoiceID, InvoiceNo: invNo, AppliedCents: in.AmountCents,
			Outcome: outcome, BalanceAfterCents: after,
		}},
	}, nil
}

// customerRunningAR computes the customer's running AR across both arms of ARBalanceCents:
// open invoices/credit memos plus non-invoice AR rows in customer_transactions.
func customerRunningAR(ctx context.Context, tx pgx.Tx, customerID string) (int64, error) {
	var running int64
	err := tx.QueryRow(ctx, `
SELECT (SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
         WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents <> 0)
     + (SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
         WHERE customer_id = $1::uuid AND COALESCE(source_type, '') <> 'invoice'
           AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF'))`,
		customerID).Scan(&running)
	return running, err
}

// replayApplication answers a spent key's own body: the application as it stands now, the invoice's
// balance at its current figure, nothing written.
func (r *Repository) replayApplication(ctx context.Context, tx pgx.Tx, prior creditApplicationRecord) (CreditApplication, error) {
	var no string
	var balance int64
	err := tx.QueryRow(ctx, `SELECT invoice_no::text, balance_cents FROM invoices WHERE id = $1::uuid`,
		prior.InvoiceID).Scan(&no, &balance)
	if err != nil {
		return CreditApplication{}, fmt.Errorf("credit apply: read the replayed invoice: %w", err)
	}
	outcome := "PART"
	if balance <= 0 {
		outcome = "CLOSES"
	}
	return CreditApplication{
		ApplicationID: prior.ApplicationID, CreditKind: prior.CreditKind, CreditID: prior.CreditID,
		InvoiceID: prior.InvoiceID, InvoiceNo: no, AppliedCents: prior.AmountCents,
		Lines: []CreditApplicationLine{{
			InvoiceID: prior.InvoiceID, InvoiceNo: no, AppliedCents: prior.AmountCents,
			Outcome: outcome, BalanceAfterCents: balance,
		}},
		Replayed: true,
	}, nil
}

// stampCreditApplications writes the application records back onto the invoice's metadata, keeping
// every other key the metadata already carried.
func stampCreditApplications(ctx context.Context, tx pgx.Tx, invoiceID string, apps map[string]creditApplicationRecord) error {
	raw, err := json.Marshal(apps)
	if err != nil {
		return fmt.Errorf("credit apply: marshal the record: %w", err)
	}
	// jsonb_set on the one key: a foreign metadata key this act does not own stays exactly as it was.
	if _, err := tx.Exec(ctx, `
UPDATE invoices
   SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{credit_applications}', $2::jsonb, true),
       updated_at = now()
 WHERE id = $1::uuid`, invoiceID, string(raw)); err != nil {
		return fmt.Errorf("credit apply: stamp the record: %w", err)
	}
	return nil
}

// creditFeedSeqCTE is the INVOICES counter CTE every invoice balance move here draws beside its
// write, the payments settle's own spelling.
const creditFeedSeqCTE = `WITH iseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('invoices')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// lockCreditGroupAnchor is the one anchor step both doors of an ACCOUNT credit share (round 4).
//
// THE ORDER RULE: every writer of a credit's group resolves and locks the group's ANCHOR ROW, the
// group's FIRST NEGATIVE row (ORDER BY occurred_at ASC, id ASC, FOR UPDATE), BEFORE it draws the
// invoices feed counter, and no door draws the counter while the group is still unlocked. The
// apply once took the group first and the reversal drew the counter first, so a staged reversal
// and apply of one group could each hold what the other needed and one answered SQLSTATE 40P01,
// the books right and the door a raw 400. One shared helper, called before either door's counter
// draw, is what keeps the order from drifting. WRITE_OFF stays out of the type list on purpose: a
// write off is never value the customer holds (S6 P3), so it is no part of a credit group and
// never the anchor.
//
// It answers found = false when the group holds no negative row left. The apply reads that as the
// group holding nothing open and refuses (CreditApplicationNotOpen); the reversal keeps its
// answer of today, the restore completed and creditOpenAfter reported at the sum as it stands,
// because a reversal undoes an act of the past and does not judge the group's open figure.
func lockCreditGroupAnchor(ctx context.Context, tx pgx.Tx, customerID, sourceType, groupRef, branchID string) (bool, error) {
	var one int
	err := tx.QueryRow(ctx, `
SELECT 1
  FROM customer_transactions
 WHERE customer_id = $1::uuid
   AND COALESCE(source_type, '') = $2
   AND COALESCE(source_ref_id, id) = $3::uuid
   AND COALESCE(branch_id::text, '') = $4
   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO')
   AND amount_cents < 0
 ORDER BY occurred_at ASC, id ASC
 LIMIT 1
   FOR UPDATE`, customerID, sourceType, groupRef, branchID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// lockCreditMemoRow is the one memo step of the MEMO doors (round 5), the anchor helper's sibling
// for the memo document's own row, so both credit kinds share the one order rule the file's THE
// LOCKS header states: the target invoice rows first (ascending id), then the credit's own lock,
// the memo document's row here or the group's anchor row there (lockCreditGroupAnchor), and only
// then the first draw of the invoices feed counter. The reversal once drew the counter on its
// invoice restore before it locked the memo row, while an apply of the same memo locks the memo
// row among the invoice rows before its own counter draw, so a staged reversal and apply of one
// memo could each hold what the other needed (SQLSTATE 40P01, round 5). Taking the lock and the
// read as one step lets the reversal judge balance and status against the committed state.
//
// A missing row, or one of another account's, answers NoSuchCreditError, the apply's memo read's
// own answer, so no existence leaks.
func lockCreditMemoRow(ctx context.Context, tx pgx.Tx, customerID, memoID string) (int64, string, error) {
	var balance int64
	var status string
	err := tx.QueryRow(ctx, `
SELECT balance_cents, status
  FROM invoices
 WHERE id = $1::uuid AND customer_id = $2::uuid
   FOR NO KEY UPDATE`, memoID, customerID).Scan(&balance, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", NoSuchCreditError{CreditID: memoID}
		}
		return 0, "", err
	}
	return balance, status, nil
}

// -- the reversal ---------------------------------------------------------------------------------

// ReverseCreditApplicationInput is one reversal request.
type ReverseCreditApplicationInput struct {
	InvoiceID       string // the invoice the application sits on
	ApplicationID   string
	Reason          string
	ActorID         string
	OrgID           string
	CanAccessBranch func(branchID string) bool
}

// CreditApplicationReversed is the reversal's answer.
type CreditApplicationReversed struct {
	ApplicationID   string                  `json:"application_id"`
	InvoiceID       string                  `json:"invoice_id"`
	InvoiceNo       string                  `json:"invoice_no"`
	RestoredCents   int64                   `json:"restored_cents"`
	InvoiceBalance  int64                   `json:"invoice_balance_after_cents"`
	CreditOpenAfter int64                   `json:"credit_open_after_cents"`
	Lines           []CreditApplicationLine `json:"lines"`
	Replayed        bool                    `json:"replayed"`
}

// ApplicationNotReversible is the refusal for an application already reversed.
var ApplicationNotReversible = errors.New("billing: that application has already been reversed")

// ReverseCreditApplication undoes one application: the invoice's balance back up, the credit's open
// figure back to what it was, both restored in ONE transaction. Manager only, reason required.
func (s *CreditApplyService) Reverse(ctx context.Context, in ReverseCreditApplicationInput, isManager bool) (CreditApplicationReversed, error) {
	if !isManager {
		return CreditApplicationReversed{}, errors.New("billing: reversing a credit application is a manager action")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return CreditApplicationReversed{}, ErrCreditApplicationReasonRequired
	}
	invID, err := uuid.Parse(strings.TrimSpace(in.InvoiceID))
	if err != nil {
		return CreditApplicationReversed{}, httpx.BadRequestf("invoice id must be a uuid")
	}
	appID, err := uuid.Parse(strings.TrimSpace(in.ApplicationID))
	if err != nil {
		return CreditApplicationReversed{}, httpx.BadRequestf("application id must be a uuid")
	}
	in.InvoiceID, in.ApplicationID = invID.String(), appID.String()
	in.Reason = strings.TrimSpace(in.Reason)
	res, err := s.repo.ReverseCreditApplication(ctx, in, s.now())
	if err != nil {
		return CreditApplicationReversed{}, err
	}
	if s.audit != nil && !res.Replayed {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActCreditApplicationReversed, EntityType: "invoice", EntityID: in.InvoiceID,
			ActorID: in.ActorID, Purpose: "CREDIT_APPLY_REVERSE", Reason: in.Reason,
			Extra: map[string]any{
				"application_id": res.ApplicationID, "restored_cents": res.RestoredCents,
			},
		})
	}
	return res, nil
}

// ReverseCreditApplication is the reversal write. The target invoice's row is the lock and the
// record's home: the record is read from its metadata, judged, and stamped reversed.
func (r *Repository) ReverseCreditApplication(ctx context.Context, in ReverseCreditApplicationInput, now time.Time) (CreditApplicationReversed, error) {
	if r.pool == nil {
		return CreditApplicationReversed{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return CreditApplicationReversed{}, fmt.Errorf("credit reverse: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, in.InvoiceID); err != nil {
		return CreditApplicationReversed{}, fmt.Errorf("credit reverse: lock the invoice: %w", err)
	}
	var (
		invNo, invBranch, invStatus, invOrg, invCust string
		invBalance, invHoldback, invTotal            int64
		metadataRaw                                  string
	)
	err = tx.QueryRow(ctx, `
SELECT invoice_no::text, customer_id::text, total_cents, balance_cents, holdback_cents, branch_id::text, status,
       COALESCE(org_id::text, ''), COALESCE(metadata::text, '')
  FROM invoices WHERE id = $1::uuid`, in.InvoiceID).
		Scan(&invNo, &invCust, &invTotal, &invBalance, &invHoldback, &invBranch, &invStatus, &invOrg, &metadataRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditApplicationReversed{}, NoSuchInvoiceError{InvoiceID: in.InvoiceID}
		}
		return CreditApplicationReversed{}, fmt.Errorf("credit reverse: read the invoice: %w", err)
	}
	if in.CanAccessBranch != nil && !in.CanAccessBranch(invBranch) {
		return CreditApplicationReversed{}, NoSuchInvoiceError{InvoiceID: in.InvoiceID}
	}
	if in.OrgID != "" && invOrg != "" && invOrg != in.OrgID {
		return CreditApplicationReversed{}, httpx.BadRequestf("invoice %s is another org's document, this desk cannot reverse its applications", invNo)
	}
	apps, err := readCreditApplications(metadataRaw)
	if err != nil {
		return CreditApplicationReversed{}, err
	}
	var (
		record    creditApplicationRecord
		recordKey string
		found     bool
	)
	for key, rec := range apps {
		if rec.ApplicationID == in.ApplicationID {
			record, recordKey, found = rec, key, true
			break
		}
	}
	if !found {
		return CreditApplicationReversed{}, httpx.BadRequestf("no credit application %s sits on invoice %s", in.ApplicationID, invNo)
	}
	if record.Reversal != nil {
		return CreditApplicationReversed{}, ApplicationNotReversible
	}
	amount := record.AmountCents

	// ACCOUNT: the group's identity and the anchor lock come BEFORE the counter (round 4, the
	// shared helper both doors call). The record names one row of the group; the anchor is the
	// group's first negative row, whatever row the record names. No negative row left keeps the
	// reversal's answer of today: the restore completes and creditOpenAfter reports the sum as
	// it stands.
	isAccount := record.CreditKind == CreditKindAccount
	var groupSourceType, groupRef, groupBranch string
	if isAccount {
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(source_type, ''), COALESCE(source_ref_id, id)::text, COALESCE(branch_id::text, '')
  FROM customer_transactions WHERE id = $1::uuid FOR UPDATE`, record.CreditID).
			Scan(&groupSourceType, &groupRef, &groupBranch); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreditApplicationReversed{}, NoSuchCreditError{CreditID: record.CreditID}
			}
			return CreditApplicationReversed{}, fmt.Errorf("credit reverse: read the account credit: %w", err)
		}
		if _, err := lockCreditGroupAnchor(ctx, tx, invCust, groupSourceType, groupRef, groupBranch); err != nil {
			return CreditApplicationReversed{}, fmt.Errorf("credit reverse: resolve the credit's anchor: %w", err)
		}
	}

	// MEMO: the memo document's row is the credit step's lock (round 5, the shared helper), taken
	// here BEFORE the restore's counter draw below, the same order the apply walks (the memo among
	// the invoice rows ascending, before its own counter draw). Balance and status are judged
	// under that lock: a memo VOIDED after its application stays VOID (S6 P3), so the restore is
	// refused rather than bringing the memo back FINALIZED, and nothing has been written yet.
	isMemo := record.CreditKind == CreditKindMemo
	var memoBalance int64
	if isMemo {
		var memoStatus string
		memoBalance, memoStatus, err = lockCreditMemoRow(ctx, tx, invCust, record.CreditID)
		if err != nil {
			return CreditApplicationReversed{}, fmt.Errorf("credit reverse: read the memo: %w", err)
		}
		if memoStatus == "VOID" {
			return CreditApplicationReversed{}, httpx.BadRequestf(
				"credit memo %s is void, its application cannot be reversed", record.CreditID)
		}
	}

	// Restore the invoice side. Both of the credit step's locks (the anchor, or the memo row) are
	// held by the time this first counter draw happens.
	after := invBalance + amount
	if _, err := tx.Exec(ctx, creditFeedSeqCTE+`
UPDATE invoices
   SET balance_cents = $2::bigint,
       status = CASE WHEN $2::bigint >= $3::bigint THEN 'FINALIZED' WHEN $2::bigint <= 0 THEN 'PAID' ELSE 'PARTIALLY_PAID' END,
       updated_at = now(), seq = (SELECT next_value FROM iseq)
 WHERE id = $1::uuid`, in.InvoiceID, after, invTotal); err != nil {
		return CreditApplicationReversed{}, fmt.Errorf("credit reverse: restore the invoice: %w", err)
	}

	creditOpenAfter := int64(0)
	switch record.CreditKind {
	case CreditKindMemo:
		// The memo's row is already locked and read (the credit step above); only the restore's
		// counter draw remains.
		creditOpenAfter = -memoBalance + amount
		if _, err := tx.Exec(ctx, creditFeedSeqCTE+`
UPDATE invoices
   SET balance_cents = $2::bigint, status = CASE WHEN $2::bigint >= 0 THEN 'PAID' ELSE 'FINALIZED' END,
       updated_at = now(), seq = (SELECT next_value FROM iseq)
 WHERE id = $1::uuid`, record.CreditID, memoBalance-amount); err != nil {
			return CreditApplicationReversed{}, fmt.Errorf("credit reverse: restore the memo: %w", err)
		}
	case CreditKindAccount:
		// The group's sum, read under the anchor lock the door took before the counter: no
		// other writer of the group can sit between the anchor and its commit, so this is the
		// committed state, not a raced one.
		var anchorSum int64
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)
  FROM customer_transactions
 WHERE customer_id = $1::uuid
   AND COALESCE(source_type, '') = $2
   AND COALESCE(source_ref_id, id) = $3::uuid
   AND COALESCE(branch_id::text, '') = $4
   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO')`,
			invCust, groupSourceType, groupRef, groupBranch).Scan(&anchorSum); err != nil {
			return CreditApplicationReversed{}, fmt.Errorf("credit reverse: read the credit's group: %w", err)
		}
		creditOpenAfter = -anchorSum + amount
		runningBefore, err := customerRunningAR(ctx, tx, invCust)
		if err != nil {
			return CreditApplicationReversed{}, fmt.Errorf("credit reverse: read the account's AR: %w", err)
		}
		runningAfter := runningBefore - amount
		if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions
  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
SELECT customer_id, txn_type, $2, $3, source_type, COALESCE(source_ref_id, id), branch_id, $4, $5
  FROM customer_transactions WHERE id = $1::uuid`,
			record.CreditID, -amount, runningAfter, "credit application reversed: "+in.Reason, now); err != nil {
			return CreditApplicationReversed{}, fmt.Errorf("credit reverse: the credit's restore row: %w", err)
		}
	}

	// The running account balance after the restores, for the rows' balance_after.
	runningAR, err := customerRunningAR(ctx, tx, invCust)
	if err != nil {
		return CreditApplicationReversed{}, fmt.Errorf("credit reverse: read the account's AR: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions
  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
SELECT customer_id, 'ADJUSTMENT', $2, $3, 'invoice', id::uuid, branch_id, $4, $5
  FROM invoices WHERE id = $1::uuid`,
		in.InvoiceID, amount, runningAR, "credit application reversed: "+in.Reason, now); err != nil {
		return CreditApplicationReversed{}, fmt.Errorf("credit reverse: the invoice's restore row: %w", err)
	}
	if record.CreditKind == CreditKindMemo {
		if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions
  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
SELECT customer_id, 'ADJUSTMENT', $2, $3, 'invoice', id::uuid, branch_id, $4, $5
  FROM invoices WHERE id = $1::uuid`,
			record.CreditID, -amount, runningAR, "credit memo restored: "+in.Reason, now); err != nil {
			return CreditApplicationReversed{}, fmt.Errorf("credit reverse: the memo's restore row: %w", err)
		}
	}

	// Stamp the reversal onto the record.
	record.Reversal = &creditReversalRecord{Reason: in.Reason, ReversedBy: in.ActorID, ReversedAt: now.UTC().Format(time.RFC3339)}
	apps[recordKey] = record
	if err := stampCreditApplications(ctx, tx, in.InvoiceID, apps); err != nil {
		return CreditApplicationReversed{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreditApplicationReversed{}, fmt.Errorf("credit reverse: commit: %w", err)
	}
	outcome := "PART"
	if after <= 0 {
		outcome = "CLOSES"
	}
	return CreditApplicationReversed{
		ApplicationID: record.ApplicationID, InvoiceID: in.InvoiceID, InvoiceNo: invNo,
		RestoredCents: amount, InvoiceBalance: after, CreditOpenAfter: creditOpenAfter,
		Lines: []CreditApplicationLine{{
			InvoiceID: in.InvoiceID, InvoiceNo: invNo, AppliedCents: amount,
			Outcome: outcome, BalanceAfterCents: after,
		}},
	}, nil
}

// OpenCreditsFor reads the open credit memos (MEMO) and unapplied account credits (ACCOUNT) for one
// customer at a branch so the invoice detail panel can offer them in one round trip.
func (r *Repository) OpenCreditsFor(ctx context.Context, customerID, branchID string) ([]OpenCreditSummary, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	out := []OpenCreditSummary{}
	memoRows, err := r.pool.Query(ctx, `
SELECT id::text, invoice_no::text, COALESCE(metadata->>'reason', ''), issue_date, -total_cents, -balance_cents
  FROM invoices
 WHERE customer_id = $1::uuid AND branch_id = $2::uuid
   AND doc_type = 'CREDIT_MEMO'
   AND status IN ('FINALIZED', 'PARTIALLY_PAID')
   AND balance_cents < 0
 ORDER BY issue_date ASC, invoice_no ASC`, customerID, branchID)
	if err != nil {
		return nil, fmt.Errorf("credit apply: list open credit memos: %w", err)
	}
	defer memoRows.Close()
	for memoRows.Next() {
		var c OpenCreditSummary
		c.CreditKind = CreditKindMemo
		if err := memoRows.Scan(&c.CreditID, &c.Reference, &c.Memo, &c.IssuedAt, &c.AmountCents, &c.OpenCents); err != nil {
			return nil, fmt.Errorf("credit apply: scan open credit memo: %w", err)
		}
		out = append(out, c)
	}
	if err := memoRows.Err(); err != nil {
		return nil, err
	}

	acctRows, err := r.pool.Query(ctx, `
SELECT (ARRAY_AGG(id::text ORDER BY occurred_at ASC, id ASC))[1] AS anchor_id,
       COALESCE(source_type, '') AS src_type,
       COALESCE((ARRAY_AGG(memo ORDER BY occurred_at ASC, id ASC))[1], '') AS anchor_memo,
       MIN(occurred_at) AS issued_at,
       -COALESCE(SUM(amount_cents) FILTER (WHERE amount_cents < 0), 0) AS orig_cents,
       -SUM(amount_cents) AS open_cents
  FROM customer_transactions
 WHERE customer_id = $1::uuid
   AND COALESCE(branch_id::text, '') = $2
   AND COALESCE(source_type, '') <> 'invoice'
   AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO')
 GROUP BY COALESCE(source_type, ''), COALESCE(source_ref_id, id)
HAVING SUM(amount_cents) < 0
 ORDER BY MIN(occurred_at) ASC`, customerID, branchID)
	if err != nil {
		return nil, fmt.Errorf("credit apply: list open account credits: %w", err)
	}
	defer acctRows.Close()
	for acctRows.Next() {
		var c OpenCreditSummary
		var srcType string
		c.CreditKind = CreditKindAccount
		if err := acctRows.Scan(&c.CreditID, &srcType, &c.Memo, &c.IssuedAt, &c.AmountCents, &c.OpenCents); err != nil {
			return nil, fmt.Errorf("credit apply: scan open account credit: %w", err)
		}
		short := strings.ToUpper(c.CreditID)
		if len(short) > 4 {
			short = short[:4]
		}
		c.Reference = "AC-" + short
		out = append(out, c)
	}
	return out, acctRows.Err()
}

// CreditApplicationsFor reads the credit applications recorded on an invoice's metadata, active or reversed.
func (r *Repository) CreditApplicationsFor(ctx context.Context, invoiceID string) ([]CreditApplicationView, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var raw string
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(metadata::text, '') FROM invoices WHERE id = $1::uuid`, invoiceID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []CreditApplicationView{}, nil
		}
		return nil, fmt.Errorf("credit apply: read invoice credit applications: %w", err)
	}
	apps, err := readCreditApplications(raw)
	if err != nil {
		return nil, err
	}
	out := make([]CreditApplicationView, 0, len(apps))
	for _, rec := range apps {
		ref := ""
		if rec.CreditKind == CreditKindMemo {
			_ = r.pool.QueryRow(ctx, `SELECT invoice_no::text FROM invoices WHERE id = $1::uuid`, rec.CreditID).Scan(&ref)
		}
		if ref == "" {
			short := strings.ToUpper(rec.CreditID)
			if len(short) > 4 {
				short = short[:4]
			}
			if rec.CreditKind == CreditKindMemo {
				ref = "CM-" + short
			} else {
				ref = "AC-" + short
			}
		}
		v := CreditApplicationView{
			ApplicationID:   rec.ApplicationID,
			CreditKind:      rec.CreditKind,
			CreditID:        rec.CreditID,
			CreditReference: ref,
			AmountCents:     rec.AmountCents,
			Reason:          rec.Reason,
			AppliedBy:       rec.AppliedBy,
			AppliedAt:       rec.AppliedAt,
		}
		if rec.Reversal != nil {
			v.Reversed = true
			v.ReversalReason = rec.Reversal.Reason
			v.ReversedBy = rec.Reversal.ReversedBy
			v.ReversedAt = rec.Reversal.ReversedAt
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppliedAt != out[j].AppliedAt {
			return out[i].AppliedAt < out[j].AppliedAt
		}
		return out[i].ApplicationID < out[j].ApplicationID
	})
	return out, nil
}
