package payments

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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// THE ONE MONEY LOCK ORDER.
//
// Every writer that touches a payment, an invoice balance or a feed counter takes its rows in this
// one order, and a writer that skips a step still keeps the order of the steps it takes:
//
//  1. the order row (a writer that works on one order),
//  2. the payments sequence row (scope_sequences, scope payments), taken WITHOUT advancing it by
//     LockPaymentsSequenceTx, or by the settle's own incrementing statement where the writer inserts
//     a payment and has no earlier use for the lock,
//  3. the existing invoice rows (FOR NO KEY UPDATE; a writer that walks the rows an already-written
//     payment allocated, the reversal and till MarkNSF, takes them in ascending id order),
//  4. the invoices sequence row (scope_sequences, scope invoices).
//
// The payments counter comes before every invoice because the settle holds an invoice while it
// waits for the counter, and any writer that holds the counter on its way to that same invoice would
// otherwise close a cycle (a 40P01 that aborts one side, and on the card path the card is already
// charged). The cycle was real between the register settle and ReversePayment before this order was
// written down.
//
// The invoice rows are FOR NO KEY UPDATE, not FOR UPDATE, because a FOR UPDATE conflicts with the
// FOR KEY SHARE a foreign key check takes: a POS return's pos_returns INSERT holds the returned
// sale's invoice share before its refund rail draws the payments counter, and a payments writer
// holding the counter while its FOR UPDATE waits on that share closes a cycle. NO KEY UPDATE still
// conflicts with every invoice UPDATE and with the account credit's FOR UPDATE, so nothing
// serialises more weakly against real writers, and no writer deletes invoices or changes their
// keys, so nothing relied on the key conflict. The one writer outside the order's letter is that
// POS return (key share before the counter); it is safe for exactly this reason, and till MarkNSF
// taking the counter too means every payments-walking writer now serialises there against every
// other, whatever order each names its invoices in.
//
// Writers and where each sits in the order (LOCK-ORDER.md carries the same table with the reasoning):
//   - the register's RecordPayment, which reaches the counter's RecordReceipt: steps 2, 3, 4 (lockAndTotal).
//   - the counter's RecordReceipt: steps 2, 3, 4 (lockAndTotal).
//   - PayWithStamps and PayWithIntent, and Pay over them, the portal and card settle: steps 2, 3, 4.
//   - ReversePayment: the payment row, then steps 2 (its reversing insert and the original's stamp)
//     and 3 (its allocated invoice rows, taken explicitly FOR NO KEY UPDATE in ascending id order
//     before the first counter draw; the contra allocation's foreign key share lock covers only the
//     invoice the loop has reached), 4. Among writers that take a counter or an invoice, the payment row is first only for NSF and the reversal.
//   - the POS ring: stock rows, then step 2 (the tender's payment insert), then step 4 (a new
//     invoice). It never touches an existing invoice.
//   - the POS return with a cash or card refund: the per-sale advisory lock and the returned order
//     lines, then the invoice KEY SHARE (the pos_returns foreign key), then step 2 (the refund
//     payment's insert). The share before the counter is the one exception, safe because no
//     payments-walking writer's invoice lock conflicts with a share. An account credit return takes
//     the share, then the invoice FOR UPDATE, then step 4 (it writes no payments row).
//   - the group and whole-order invoice writers: the order row its caller locked, then step 4 only
//     (a new invoice).
//   - the quote counter door (QD-5): the order row, LockPaymentsSequenceTx, then steps 3
//     and 4 on its own transaction.
//   - till MarkNSF: the payment row, then step 2 (LockPaymentsSequenceTx, not advancing: the
//     serialising lock, it writes no payments feed row), then steps 3 (its allocated invoice rows
//     in ascending id order, FOR NO KEY UPDATE) and 4.
//
// Reads, and the outbox observer, take none of these rows.

var errNoDB = errors.New("payments: database not configured")

// methodColumns is the canonical public SELECT list for a stored method (kept in one place
// so List/Get scan identically). The vault token is NEVER in this list - it is read only
// by GetMethod via the dedicated charge/detach path.
const methodColumns = `id::text, customer_id::text, gateway, method_type, brand, last4,
	exp_month, exp_year, is_default, status, created_at, updated_at`

// Repository owns portal_payment_methods (0027) and the atomic pay write across
// payments + payment_allocations + customer_transactions (0010/0009). It REUSES those
// existing tables - it never recreates them.
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
	till TillSeam      // the till's side of the held money drawer pay out and write off (payout.go); nil leaves them off
	// discountTerms is the terms master read the early payment discount runs on (discount.go);
	// nil discounts nothing. zone is the dealer's business day zone source the discount window is
	// judged in (nil is the Ontario dealer's) and discountClock is the clock it is judged on.
	discountTerms DiscountTermsReader
	zone          houseday.Source
	discountClock func() time.Time
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// scanMethod scans the public method columns. exp_month/exp_year are nullable (card only).
func scanMethod(row pgx.Row) (PaymentMethod, error) {
	var m PaymentMethod
	var expMonth, expYear *int
	if err := row.Scan(
		&m.ID, &m.CustomerID, &m.Gateway, &m.MethodType, &m.Brand, &m.Last4,
		&expMonth, &expYear, &m.IsDefault, &m.Status, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return PaymentMethod{}, err
	}
	if expMonth != nil {
		m.ExpMonth = *expMonth
	}
	if expYear != nil {
		m.ExpYear = *expYear
	}
	return m, nil
}

// ListMethods returns the tenant's active stored methods (default first, newest next).
func (r *Repository) ListMethods(ctx context.Context, customerID string) ([]PaymentMethod, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := fmt.Sprintf(
		`SELECT %s FROM portal_payment_methods
		 WHERE customer_id = $1 AND status = 'active'
		 ORDER BY is_default DESC, created_at DESC`, methodColumns)
	rows, err := r.pool.Query(ctx, q, customerID)
	if err != nil {
		return nil, fmt.Errorf("list methods: %w", err)
	}
	defer rows.Close()

	// An empty answer is the empty slice, never nil: a nil slice marshals as null, and a
	// second caller beyond the portal handler would inherit the null.
	out := []PaymentMethod{}
	for rows.Next() {
		m, err := scanMethod(rows)
		if err != nil {
			return nil, fmt.Errorf("scan method: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list methods: %w", err)
	}
	return out, nil
}

// GetMethod loads one ACTIVE method INCLUDING the vault token (for the charge/detach path),
// tenant-scoped. A missing, cross-tenant, OR soft-deleted ('removed') id surfaces as
// ErrNotFound (IDOR-safe) so a removed method can never be charged again.
func (r *Repository) GetMethod(ctx context.Context, customerID, methodID string) (PaymentMethod, error) {
	if r.pool == nil {
		return PaymentMethod{}, errNoDB
	}
	q := fmt.Sprintf(
		`SELECT %s, gateway_token FROM portal_payment_methods
		 WHERE id = $2 AND customer_id = $1 AND status = 'active'`, methodColumns)

	var m PaymentMethod
	var expMonth, expYear *int
	var token string
	err := r.pool.QueryRow(ctx, q, customerID, methodID).Scan(
		&m.ID, &m.CustomerID, &m.Gateway, &m.MethodType, &m.Brand, &m.Last4,
		&expMonth, &expYear, &m.IsDefault, &m.Status, &m.CreatedAt, &m.UpdatedAt,
		&token,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PaymentMethod{}, ErrNotFound
		}
		return PaymentMethod{}, fmt.Errorf("get method: %w", err)
	}
	if expMonth != nil {
		m.ExpMonth = *expMonth
	}
	if expYear != nil {
		m.ExpYear = *expYear
	}
	return m.WithToken(token), nil
}

// AddMethod inserts a vaulted method, clearing the prior default in the same tx when
// makeDefault is set (the partial unique index allows at most one default per account).
func (r *Repository) AddMethod(ctx context.Context, m PaymentMethod, makeDefault bool) (PaymentMethod, error) {
	if r.pool == nil {
		return PaymentMethod{}, errNoDB
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("add method: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if makeDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE portal_payment_methods SET is_default = false, updated_at = now()
			 WHERE customer_id = $1 AND is_default AND status = 'active'`,
			m.CustomerID); err != nil {
			return PaymentMethod{}, fmt.Errorf("add method: clear default: %w", err)
		}
	}

	// exp_month/exp_year are NULL for bank/ACH (no card expiry).
	var expMonth, expYear *int
	if m.ExpMonth != 0 {
		expMonth = &m.ExpMonth
	}
	if m.ExpYear != 0 {
		expYear = &m.ExpYear
	}

	q := fmt.Sprintf(
		`INSERT INTO portal_payment_methods
			(customer_id, gateway, gateway_token, method_type, brand, last4,
			 exp_month, exp_year, is_default, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active')
		 RETURNING %s`, methodColumns)

	row := tx.QueryRow(ctx, q,
		m.CustomerID, m.Gateway, m.Token(), m.MethodType, m.Brand, m.Last4,
		expMonth, expYear, makeDefault)
	out, err := scanMethod(row)
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("add method: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentMethod{}, fmt.Errorf("add method: commit: %w", err)
	}
	return out, nil
}

// RemoveMethod soft-deletes the method (status='removed'), tenant-scoped. RowsAffected==0
// (missing or cross-tenant) maps to ErrNotFound.
func (r *Repository) RemoveMethod(ctx context.Context, customerID, methodID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE portal_payment_methods
		 SET status = 'removed', is_default = false, updated_at = now()
		 WHERE id = $2 AND customer_id = $1 AND status = 'active'`,
		customerID, methodID)
	if err != nil {
		return fmt.Errorf("remove method: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDefaultMethod makes one active method the tenant's default in a single tx: it first
// clears the prior default, then flips the target on (the partial unique index allows at
// most one default per account). RowsAffected==0 on the target flip (missing, removed, or
// cross-tenant) maps to ErrNotFound (IDOR-safe).
func (r *Repository) SetDefaultMethod(ctx context.Context, customerID, methodID string) error {
	if r.pool == nil {
		return errNoDB
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set default: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE portal_payment_methods SET is_default = false, updated_at = now()
		 WHERE customer_id = $1 AND is_default AND status = 'active'`,
		customerID); err != nil {
		return fmt.Errorf("set default: clear prior: %w", err)
	}

	tag, err := tx.Exec(ctx,
		`UPDATE portal_payment_methods SET is_default = true, updated_at = now()
		 WHERE id = $2 AND customer_id = $1 AND status = 'active'`,
		customerID, methodID)
	if err != nil {
		return fmt.Errorf("set default: flip target: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// lockApplyInvoicesTx takes every named invoice of a received payment FOR NO KEY UPDATE, one
// statement per id in ascending id order (the payments counter is already held), and judges each
// one as it lands: the account's, open, and inside a scoped caller's branch reach. The account and
// the reach are FOLDED INTO the locking statement itself, so a fenced caller never locks, and never
// waits on, a row it may not see (the Opus review's P3-2): a row that fails either answers as not
// found, the same words for a missing row, another account's row and an out of reach row, so the
// refusal is no existence oracle across the account or branch walls either.
//
// FOR NO KEY UPDATE, not FOR UPDATE, for the reason LOCK-ORDER.md writes down: this writer takes
// no key column, and NO KEY UPDATE still conflicts with every other invoice writer's row lock
// while never queuing behind a foreign key's FOR KEY SHARE (a POS return's pos_returns insert).
// The ascending order is PR 277's (the till's returns lock their documents the same way), so a
// received payment and a return can never meet head on, whatever order the caller's list came in.
//
// The targets are INVOICES (doc_type = 'INVOICE'), and the filter is here, in the locking
// statement, for K3's P2-1: a hand-built request may name a CREDIT_MEMO beside its target
// INVOICE, and the memo passes this walk's open status check (it is FINALIZED) while its
// negative balance only produces zero payable, so the walk kept the memo's row lock while it
// paid the invoice beside it. With the memo's id below the invoice's, this walk takes the memo
// and then the invoice while the billing credit apply takes the invoice and then the memo: each
// door holds the row the other needs and Postgres answers one side 40P01. A memo is a document a
// payment can never land on, so the walk refuses it here, before it takes a row at all.
func (r *Repository) lockApplyInvoicesTx(ctx context.Context, tx pgx.Tx, in ApplyRequest) ([]applyInvoiceRow, error) {
	ids := append([]string(nil), in.InvoiceIDs...)
	sort.Strings(ids)
	var reach []string
	if in.BranchIDs != nil {
		reach = in.BranchIDs
	}
	rows := make([]applyInvoiceRow, 0, len(ids))
	for _, id := range ids {
		var row applyInvoiceRow
		err := tx.QueryRow(ctx, `
			SELECT id::text, invoice_no, customer_id::text, branch_id::text, status::text,
			       issue_date, due_date, balance_cents, holdback_cents,
			       discount_date, COALESCE(terms_code, '')
			  FROM invoices
			 WHERE id = $1::uuid AND customer_id = $2::uuid
			   AND doc_type = 'INVOICE'
			   AND ($3::text[] IS NULL OR branch_id = ANY($3::uuid[]))
			   FOR NO KEY UPDATE`, id, in.CustomerID, reach).
			Scan(&row.InvoiceID, &row.InvoiceNo, &row.CustomerID, &row.BranchID, &row.Status,
				&row.IssueDate, &row.DueDate, &row.BalanceCents, &row.HoldbackCents,
				&row.DiscountDate, &row.TermsCode)
		if err == nil {
			err = loadDiscountRoom(ctx, tx, &row)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			nsfRow, found, nsfErr := lockApplyNSFFeeByIDTx(ctx, tx, id, in.CustomerID, reach)
			if nsfErr != nil {
				return nil, nsfErr
			}
			if !found {
				return nil, NoSuchInvoiceError{InvoiceID: id}
			}
			rows = append(rows, nsfRow)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("apply: lock invoice %q: %w", id, err)
		}
		if row.Status != "FINALIZED" && row.Status != "PARTIALLY_PAID" {
			return nil, httpx.BadRequestf("invoice %s (%s) is not open; a payment applies to a finalized or partially paid invoice",
				row.InvoiceNo, strings.ToLower(row.Status))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

const openNSFFeeRemainingExpr = `
n.fee_cents - COALESCE((
	SELECT SUM((p2.metadata->'nsf_fee_allocations'->>n.id::text)::bigint)
	  FROM payments p2
	 WHERE p2.customer_id = COALESCE(n.customer_id, bp.customer_id)
	   AND p2.reversed_at IS NULL
	   AND p2.reverses_payment_id IS NULL
	   AND p2.status <> 'BOUNCED'
	   AND (p2.metadata->'nsf_fee_allocations'->>n.id::text) IS NOT NULL
), 0)`

func lockApplyNSFFeeByIDTx(ctx context.Context, tx pgx.Tx, id, customerID string, reach []string) (applyInvoiceRow, bool, error) {
	var row applyInvoiceRow
	err := tx.QueryRow(ctx, `
		SELECT n.id::text, 'NSF-FEE', COALESCE(n.customer_id, bp.customer_id)::text, bp.branch_id::text,
		       n.created_at, `+openNSFFeeRemainingExpr+`
		  FROM payment_nsf n
		  JOIN payments bp ON bp.id = n.payment_id
		 WHERE n.id = $1::uuid
		   AND COALESCE(n.customer_id, bp.customer_id) = $2::uuid
		   AND ($3::text[] IS NULL OR bp.branch_id = ANY($3::uuid[]))
		   FOR NO KEY UPDATE OF n`, id, customerID, reach).
		Scan(&row.InvoiceID, &row.InvoiceNo, &row.CustomerID, &row.BranchID, &row.IssueDate, &row.BalanceCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return applyInvoiceRow{}, false, nil
	}
	if err != nil {
		return applyInvoiceRow{}, false, fmt.Errorf("apply: lock nsf fee %q: %w", id, err)
	}
	if row.BalanceCents <= 0 {
		return applyInvoiceRow{}, true, httpx.BadRequestf("NSF fee %s is not open; a payment applies to a finalized or partially paid invoice", id)
	}
	row.Status = "FINALIZED"
	row.DueDate = row.IssueDate
	row.IsNSFFee = true
	return row, true, nil
}

// branchInReach answers whether a branch is in a scoped caller's reach list.
func branchInReach(reach []string, branchID string) bool {
	for _, b := range reach {
		if b == branchID {
			return true
		}
	}
	return false
}

// readApplyInvoices is the PREVIEW's read: every named invoice of a received payment in ONE
// statement, with the account and the branch reach folded into the WHERE exactly as the apply's
// locking statement folds them (so a fenced caller is answered, and never delayed, the same not
// found, no existence leak across the account or branch walls), and with no lock clause at all:
// a preview writes nothing, so it draws neither the payments counter nor an invoice row, and a
// desk's debounced keystroke never queues a till payment (item 127). One statement is one
// snapshot, so the balances the split is computed against are consistent with each other without
// a single row lock. The judgements run in the same ascending id order the apply locks in, over
// the rows the statement returned, so the refusal a preview surfaces is the refusal the apply
// would surface.
//
// The targets are INVOICES (doc_type = 'INVOICE'), the same filter the apply's locking statement
// carries (K3's P2-1), so the preview refuses a named credit memo exactly as the apply refuses
// it: a memo is a document a payment can never land on. The preview takes no row either way, so
// the filter here is about the answer the desk sees, not about a lock.
func (r *Repository) readApplyInvoices(ctx context.Context, in ApplyRequest) ([]applyInvoiceRow, error) {
	ids := append([]string(nil), in.InvoiceIDs...)
	sort.Strings(ids)
	var reach []string
	if in.BranchIDs != nil {
		reach = in.BranchIDs
	}
	found := make(map[string]applyInvoiceRow, len(ids))
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, invoice_no, customer_id::text, branch_id::text, status::text,
		       issue_date, due_date, balance_cents, holdback_cents,
		       discount_date, COALESCE(terms_code, '')
		  FROM invoices
		 WHERE id = ANY($1::uuid[]) AND customer_id = $2::uuid
		   AND doc_type = 'INVOICE'
		   AND ($3::text[] IS NULL OR branch_id = ANY($3::uuid[]))`, ids, in.CustomerID, reach)
	if err != nil {
		return nil, fmt.Errorf("apply preview: read invoices: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row applyInvoiceRow
		if err := rows.Scan(&row.InvoiceID, &row.InvoiceNo, &row.CustomerID, &row.BranchID, &row.Status,
			&row.IssueDate, &row.DueDate, &row.BalanceCents, &row.HoldbackCents,
			&row.DiscountDate, &row.TermsCode); err != nil {
			return nil, fmt.Errorf("apply preview: scan invoice: %w", err)
		}
		found[row.InvoiceID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("apply preview: read invoices: %w", err)
	}
	rows.Close()
	for id, row := range found {
		if err := loadDiscountRoom(ctx, r.pool, &row); err != nil {
			return nil, err
		}
		found[id] = row
	}
	ordered := make([]applyInvoiceRow, 0, len(ids))
	for _, id := range ids {
		row, ok := found[id]
		if !ok {
			nsfRow, nsfFound, nsfErr := r.readApplyNSFFeeByID(ctx, id, in.CustomerID, reach)
			if nsfErr != nil {
				return nil, nsfErr
			}
			if !nsfFound {
				return nil, NoSuchInvoiceError{InvoiceID: id}
			}
			ordered = append(ordered, nsfRow)
			continue
		}
		if row.Status != "FINALIZED" && row.Status != "PARTIALLY_PAID" {
			return nil, httpx.BadRequestf("invoice %s (%s) is not open; a payment applies to a finalized or partially paid invoice",
				row.InvoiceNo, strings.ToLower(row.Status))
		}
		ordered = append(ordered, row)
	}
	return ordered, nil
}

func (r *Repository) readApplyNSFFeeByID(ctx context.Context, id, customerID string, reach []string) (applyInvoiceRow, bool, error) {
	var row applyInvoiceRow
	err := r.pool.QueryRow(ctx, `
		SELECT n.id::text, 'NSF-FEE', COALESCE(n.customer_id, bp.customer_id)::text, bp.branch_id::text,
		       n.created_at, `+openNSFFeeRemainingExpr+`
		  FROM payment_nsf n
		  JOIN payments bp ON bp.id = n.payment_id
		 WHERE n.id = $1::uuid
		   AND COALESCE(n.customer_id, bp.customer_id) = $2::uuid
		   AND ($3::text[] IS NULL OR bp.branch_id = ANY($3::uuid[]))`, id, customerID, reach).
		Scan(&row.InvoiceID, &row.InvoiceNo, &row.CustomerID, &row.BranchID, &row.IssueDate, &row.BalanceCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return applyInvoiceRow{}, false, nil
	}
	if err != nil {
		return applyInvoiceRow{}, false, fmt.Errorf("apply preview: read nsf fee %q: %w", id, err)
	}
	if row.BalanceCents <= 0 {
		return applyInvoiceRow{}, true, httpx.BadRequestf("NSF fee %s is not open; a payment applies to a finalized or partially paid invoice", id)
	}
	row.Status = "FINALIZED"
	row.DueDate = row.IssueDate
	row.IsNSFFee = true
	return row, true, nil
}

// replayAnswer rebuilds the split answer for a key that already posted: the stored payment with
// its stored allocations, and each invoice's balance read live so the desk sees what the ledger
// says now, not what it said when the cheque first landed. The balances are the invoice's current
// ones (a later payment may have moved them since); the outcome words read the invoice's live
// status.
//
// The key is not a bearer of the answer (the till's replayIfAllowed rule, PR 277): a replay is
// judged like a first attempt, and then against its own body. An allocated invoice outside the
// caller's branch reach answers as not found, a key that recorded a payment for another account is
// refused in words, and so is a key re-used for a different payment (another amount, method,
// reference or invoice list): the desk that retries with a corrected amount must not be told
// "replayed" with the old figure.
func (r *Repository) replayAnswer(ctx context.Context, p Payment, in ApplyRequest) (ApplyAnswer, error) {
	ans := ApplyAnswer{
		PaymentID: p.ID, PaymentNo: p.PaymentNo, AmountCents: p.AmountCents,
		AppliedCents: 0, Replayed: true,
	}
	if p.Allocations == nil {
		p.Allocations = []Allocation{}
	}
	// The stored body: the reference and the named invoice list recorded on the row when it posted.
	// A payment recorded before the named list rode the row reads its allocations as its named
	// list, which is exactly what the per invoice settle names.
	var storedRef string
	var storedNamed []string
	var nsfAllocJSON []byte
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(metadata->>'reference', ''),
		       ARRAY(SELECT jsonb_array_elements_text(COALESCE(metadata->'invoice_ids', '[]'::jsonb))),
		       COALESCE(metadata->'nsf_fee_allocations', '{}'::jsonb)
		  FROM payments WHERE id = $1::uuid`, p.ID).Scan(&storedRef, &storedNamed, &nsfAllocJSON); err != nil {
		return ApplyAnswer{}, fmt.Errorf("apply: replay read payment body: %w", err)
	}
	nsfAllocs := map[string]int64{}
	_ = json.Unmarshal(nsfAllocJSON, &nsfAllocs)
	if len(storedNamed) == 0 {
		for _, a := range p.Allocations {
			storedNamed = append(storedNamed, a.InvoiceID)
		}
	}
	appliedBy := map[string]int64{}
	discountedBy := map[string]int64{}
	for _, a := range p.Allocations {
		appliedBy[a.InvoiceID] += a.AmountCents
		discountedBy[a.InvoiceID] += a.DiscountCents
		ans.AppliedCents += a.AmountCents
	}
	for nsfID, amt := range nsfAllocs {
		appliedBy[nsfID] += amt
		ans.AppliedCents += amt
	}
	ids := make([]string, 0, len(appliedBy))
	for id := range appliedBy {
		ids = append(ids, id)
	}
	rows := make([]applyInvoiceRow, 0, len(ids))
	for _, id := range ids {
		var row applyInvoiceRow
		err := r.pool.QueryRow(ctx, `
			SELECT id::text, invoice_no, customer_id::text, branch_id::text, status::text,
			       issue_date, due_date, balance_cents, holdback_cents,
			       discount_date, COALESCE(terms_code, '')
			  FROM invoices WHERE id = $1::uuid`, id).
			Scan(&row.InvoiceID, &row.InvoiceNo, &row.CustomerID, &row.BranchID, &row.Status,
				&row.IssueDate, &row.DueDate, &row.BalanceCents, &row.HoldbackCents,
				&row.DiscountDate, &row.TermsCode)
		if errors.Is(err, pgx.ErrNoRows) {
			err = r.pool.QueryRow(ctx, `
				SELECT n.id::text, 'NSF-FEE', COALESCE(n.customer_id, bp.customer_id)::text, bp.branch_id::text,
				       n.created_at, `+openNSFFeeRemainingExpr+`
				  FROM payment_nsf n
				  JOIN payments bp ON bp.id = n.payment_id
				 WHERE n.id = $1::uuid`, id).
				Scan(&row.InvoiceID, &row.InvoiceNo, &row.CustomerID, &row.BranchID, &row.IssueDate, &row.BalanceCents)
			if err != nil {
				return ApplyAnswer{}, fmt.Errorf("apply: replay read nsf fee %q: %w", id, err)
			}
			row.DueDate = row.IssueDate
			row.IsNSFFee = true
			if row.BalanceCents <= 0 {
				row.Status = "PAID"
			} else {
				row.Status = "PARTIALLY_PAID"
			}
		} else if err != nil {
			return ApplyAnswer{}, fmt.Errorf("apply: replay read invoice %q: %w", id, err)
		}
		if in.BranchIDs != nil && !branchInReach(in.BranchIDs, row.BranchID) {
			return ApplyAnswer{}, NoSuchInvoiceError{InvoiceID: id}
		}
		rows = append(rows, row)
	}
	// The account and the tender are normalised HERE, ahead of the body comparison, because the
	// replay judgement now runs before validateApply: the retry's account id may arrive in any
	// spelling Parse accepts (or not parse at all, a body the fresh key validation would refuse),
	// and the tender may arrive in lower case. A body that does not name this payment's account,
	// in whatever spelling, answers the account refusal, never a validation word.
	cu, err := uuid.Parse(strings.TrimSpace(in.CustomerID))
	if err != nil || p.CustomerID != cu.String() {
		return ApplyAnswer{}, ReplayConflictError{Msg: "that idempotency key already recorded a payment for another account; use a new key"}
	}
	in.CustomerID = cu.String()
	in.Method = strings.ToUpper(strings.TrimSpace(in.Method))
	if !sameReplayBody(p, storedRef, storedNamed, in) {
		return ApplyAnswer{}, ReplayConflictError{Msg: "that idempotency key already recorded a different payment; use a new key"}
	}
	// The zero amount walk answers the rows in the allocation's own oldest first order with their
	// live balances; each line is then patched with what THIS payment put on it.
	lines, _, _ := allocateOldestFirst(rows, 0, nil)
	for i := range lines {
		take := appliedBy[lines[i].InvoiceID]
		lines[i].AppliedCents = take
		// The discount this payment's own applications took on the invoice, read from the
		// allocation rows with the money: a replay answers the posting's own split, discount
		// included, never a fresh judgement of a window that has since closed.
		lines[i].DiscountCents = discountedBy[lines[i].InvoiceID]
		outcome := ApplyOutcomeNone
		if take > 0 {
			if rowStatus(rows, lines[i].InvoiceID) == "PAID" {
				outcome = ApplyOutcomeCloses
			} else {
				outcome = ApplyOutcomePart
			}
		}
		lines[i].Outcome = outcome
	}
	ans.Lines = lines
	ans.UnappliedCents = ans.AmountCents - ans.AppliedCents
	ans.OnAccountCents = ans.UnappliedCents
	return ans, nil
}

// PayableInvoices reads the payable balance of each requested invoice, tenant-scoped, and
// returns ONE Allocation per invoice with the payable amount EXCLUDING holdback (sec. 4.2 #7).
// A missing or cross-tenant id in the request yields ErrNotFound (no partial slice).
func (r *Repository) PayableInvoices(ctx context.Context, customerID string, invoiceIDs []string) ([]Allocation, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if len(invoiceIDs) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, GREATEST(balance_cents - holdback_cents, 0), status
		 FROM invoices
		 WHERE customer_id = $1 AND id = ANY($2)`,
		customerID, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("payable invoices: %w", err)
	}
	defer rows.Close()

	var out []Allocation
	for rows.Next() {
		var a Allocation
		if err := rows.Scan(&a.InvoiceID, &a.AmountCents, &a.Status); err != nil {
			return nil, fmt.Errorf("scan payable invoice: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payable invoices: %w", err)
	}
	// Every requested invoice must resolve to a tenant-owned row, else IDOR-safe not-found.
	if len(out) != len(invoiceIDs) {
		return nil, ErrNotFound
	}
	return out, nil
}

// scanPayment scans the public payment header columns. customer_id and gateway_ref are
// nullable on the underlying table.
func scanPayment(row pgx.Row) (Payment, error) {
	var p Payment
	var customerID, gatewayRef *string
	if err := row.Scan(
		&p.ID, &p.PaymentNo, &customerID, &p.Method,
		&p.AmountCents, &p.Status, &gatewayRef, &p.ReceivedAt,
	); err != nil {
		return Payment{}, err
	}
	if customerID != nil {
		p.CustomerID = *customerID
	}
	if gatewayRef != nil {
		p.GatewayRef = *gatewayRef
	}
	return p, nil
}

// ListPayments returns the tenant's payments (newest first) with their allocations.
func (r *Repository) ListPayments(ctx context.Context, customerID string) ([]Payment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, payment_no::text, customer_id::text, method,
			amount_cents, status, gateway_ref, received_at
		 FROM payments WHERE customer_id = $1 ORDER BY received_at DESC`,
		customerID)
	if err != nil {
		return nil, fmt.Errorf("list payments: %w", err)
	}
	defer rows.Close()

	// An empty answer is the empty slice, never nil: a nil slice marshals as null, and a
	// second caller beyond the portal handler would inherit the null.
	out := []Payment{}
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan payment: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list payments: %w", err)
	}

	// Attach allocations in ONE batched query (avoid the per-payment N+1).
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	byPayment, err := r.allocationsByPayment(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Allocations = byPayment[out[i].ID]
	}
	return out, nil
}

// FindByGatewayRef looks up a single payment by gateway ref in one indexed query (the
// idempotent-replay guard), tenant-scoped. found=false when no row matches.
func (r *Repository) FindByGatewayRef(ctx context.Context, customerID, ref string) (Payment, bool, error) {
	if r.pool == nil {
		return Payment{}, false, errNoDB
	}
	p, err := scanPayment(r.pool.QueryRow(ctx,
		`SELECT id::text, payment_no::text, customer_id::text, method,
			amount_cents, status, gateway_ref, received_at
		 FROM payments WHERE customer_id = $1 AND gateway_ref = $2 LIMIT 1`,
		customerID, ref))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Payment{}, false, nil
		}
		return Payment{}, false, fmt.Errorf("find by gateway ref: %w", err)
	}
	allocs, err := r.allocationsByPayment(ctx, []string{p.ID})
	if err != nil {
		return Payment{}, false, err
	}
	p.Allocations = allocs[p.ID]
	return p, true, nil
}

// allocationsByPayment loads the allocations for many payments in ONE query, grouped by
// payment id in memory (replaces the per-payment N+1 loop, sec. 4.2 #8). Q-044: each row
// carries its attribution, allocated_by (+ display name via a users LEFT JOIN) and created_at.
// A portal-initiated allocation has no staff allocator and surfaces with empty by/name.
func (r *Repository) allocationsByPayment(ctx context.Context, paymentIDs []string) (map[string][]Allocation, error) {
	out := make(map[string][]Allocation, len(paymentIDs))
	for _, id := range paymentIDs {
		// An empty answer is the empty slice, never nil: ListPayments and the sync feed
		// marshal the model's json tags, and a nil slice marshals as null.
		out[id] = []Allocation{}
	}
	if len(paymentIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT pa.payment_id::text, pa.invoice_id::text, pa.amount_cents, pa.discount_cents,
		        COALESCE(pa.allocated_by::text, ''), COALESCE(u.display_name, ''), pa.created_at
		 FROM payment_allocations pa
		 LEFT JOIN users u ON u.id = pa.allocated_by
		 WHERE pa.payment_id = ANY($1) ORDER BY pa.invoice_id, pa.kind`, paymentIDs)
	if err != nil {
		return nil, fmt.Errorf("list allocations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var paymentID string
		var a Allocation
		if err := rows.Scan(&paymentID, &a.InvoiceID, &a.AmountCents, &a.DiscountCents,
			&a.AllocatedBy, &a.AllocatedByName, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan allocation: %w", err)
		}
		out[paymentID] = append(out[paymentID], a)
	}
	return out, rows.Err()
}

// Pay records ONE settled charge across N invoices in a single transaction (the
// all-or-nothing split that designs away card partial-failure, sec. 4.2). Each invoice is
// re-read FOR NO KEY UPDATE and its balance re-validated against the allocation before any write,
// so a balance that moved between the charge decision and settlement aborts the whole tx.
func (r *Repository) Pay(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation) (Payment, error) {
	return r.PayWithIntent(ctx, customerID, method, gatewayRef, allocs, "")
}

// PayWithIntent is Pay with the DIB-415 durable intent closed out in the SAME transaction as the
// settle. intentID "" behaves exactly like the historical Pay (no intent row to close), which is why
// Pay is now a one-line delegation rather than a duplicated body.
//
// The atomicity is the point. If the intent were marked SETTLED in a second transaction after the
// settle committed, a crash between the two would leave a CHARGING intent whose payment already
// exists, and the retry, which correctly refuses to re-charge a CHARGING intent, would refuse to
// hand back a payment that is sitting right there. One transaction means the intent and the money it
// describes can never disagree.
func (r *Repository) PayWithIntent(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation, intentID string) (Payment, error) {
	return r.PayWithStamps(ctx, customerID, method, gatewayRef, allocs, intentID, settleStamps{})
}

// settleStamps are the sync legs a settled payment row may carry
// (HH-3.5): the client-chosen row id and the client idempotency key
// the offline replay dedups on. Zero values behave exactly like the
// desk's settle (the id minted, the key column left empty), so ONE
// settle body serves both doors and they cannot drift.
type settleStamps struct {
	RowID     string
	ClientKey string
}

// paymentsFeedSeqCTE draws a fresh payments feed seq atomically in
// the same statement as the settle's INSERT (scope_sequences, the
// 0213 convention), so a payment is visible on the payer's feed the
// moment it lands.
const paymentsFeedSeqCTE = `WITH pseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('payments')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// invoicesFeedSeqCTE is the same CTE over the INVOICES counter, the
// billing scope's own feed: every money move this module makes on an
// invoice (a settle's allocation, a reversal's re-open) stamps the
// invoice's seq beside the balance move, so the payer's invoices feed
// carries the change. Cross-module column stamp on a table this
// module already writes in the same transaction: the recorded
// cross-table reach of the atomic pay, pointed at the sync leg.
const invoicesFeedSeqCTE = `WITH iseq AS (
	INSERT INTO scope_sequences (scope) VALUES ('invoices')
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
	RETURNING next_value
) `

// PayWithStamps is PayWithIntent carrying the sync stamps. The
// INSERT additionally stamps the 0213 legs beside them: the org read
// off the customer (the tenant's org anchor, the same stamp every
// tenant-owned row carries) and the feed seq. The id honors the
// client-chosen row id when the replay named one, and the 0028
// idempotency_key column carries the client key: that column is the
// table's own recorded replay guard ("client-generated UUID; the
// partial-unique index makes the write replay-safe", 0028), so the
// offline outbox's key lands in exactly the column built for it.
func (r *Repository) PayWithStamps(ctx context.Context, customerID, method, gatewayRef string, allocs []Allocation, intentID string, stamps settleStamps) (Payment, error) {
	if r.pool == nil {
		return Payment{}, errNoDB
	}
	if len(allocs) == 0 {
		return Payment{}, errors.New("payments: no allocations to settle")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Payment{}, fmt.Errorf("pay: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// THE PORTAL'S RULE (TM-2 fix round 2, P2-1, the lead's choice (b)): the portal settles at face,
	// with no early payment discount and no change on account. The card was charged the face and
	// the screen shows the face, so a discount parked as a credit the customer cannot see would be
	// a confident wrong number. The money desk and the per invoice door keep the one discount rule.
	// Follow on: plan row TM-2b, the portal charges the discounted payoff and shows the discount.
	total, branchID, change, err := r.lockAndTotal(ctx, tx, customerID, allocs, nil, false)
	if err != nil {
		return Payment{}, err
	}

	// Insert the payment header, drawing a fresh payment_no from the sequence.
	var p Payment
	var pCustomerID, pGatewayRef *string
	err = tx.QueryRow(ctx, paymentsFeedSeqCTE+`
INSERT INTO payments (id, payment_no, customer_id, method, amount_cents, status, branch_id,
                      gateway_ref, org_id, seq, idempotency_key)
VALUES (COALESCE(NULLIF($1,'')::uuid, gen_random_uuid()), nextval('payment_no_seq')::text, $2, $3, $4,
        'RECEIVED', $5, $6,
        (SELECT org_id FROM customers WHERE id = $2::uuid),
        (SELECT next_value FROM pseq), $7)
RETURNING id::text, payment_no::text, customer_id::text, method, amount_cents, status, gateway_ref, received_at`,
		stamps.RowID, customerID, method, total+change, branchID, gatewayRef, stamps.ClientKey).Scan(
		&p.ID, &p.PaymentNo, &pCustomerID, &p.Method, &p.AmountCents, &p.Status, &pGatewayRef, &p.ReceivedAt)
	if err != nil {
		return Payment{}, fmt.Errorf("pay: insert payment: %w", err)
	}
	if pCustomerID != nil {
		p.CustomerID = *pCustomerID
	}
	if pGatewayRef != nil {
		p.GatewayRef = *pGatewayRef
	}

	if err := applyAllocations(ctx, tx, &p, customerID, method, gatewayRef, branchID, allocs, "", change); err != nil {
		return Payment{}, err
	}

	// DIB-415: close the durable intent out in this same transaction. The guarded WHERE means only a
	// CHARGING intent settles, an intent somebody else already settled, or an operator ABANDONED
	// mid-flight, does not get quietly overwritten by a late-arriving commit.
	if intentID != "" {
		tag, err := tx.Exec(ctx,
			`UPDATE payment_intents
			    SET status = 'SETTLED', payment_id = $2::uuid, gateway_ref = $3, last_error = '', updated_at = now()
			  WHERE id = $1::uuid AND status = 'CHARGING'`, intentID, p.ID, gatewayRef)
		if err != nil {
			return Payment{}, fmt.Errorf("pay: settle intent: %w", err)
		}
		if tag.RowsAffected() != 1 {
			// Refusing to commit is deliberate: the settle rolls back, no payment row is written, and
			// the gateway ref stays on the intent for reconciliation. A settle whose intent moved under
			// it is a state we do not understand, and writing money on a state we do not understand is
			// how a ledger stops being trustworthy.
			return Payment{}, fmt.Errorf("payments: intent %s was not CHARGING at settle time, refusing to write the payment", intentID)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Payment{}, fmt.Errorf("pay: commit: %w", err)
	}
	return p, nil
}

// - the two halves of the settle, extracted so there is exactly ONE of each -
//
// DIB-510 needed a second entry point into this transaction: a payment TAKEN OFF-GATEWAY at the
// counter (a cheque, an EFT that has already landed) applied to an open invoice from the receivables
// register. There was a choice between writing a second settle beside this one and cutting this one
// into the two pieces both callers share. A second settle is how a ledger acquires two spellings of
// "apply money to an invoice" that agree until the day one of them is fixed; so the body was cut, not
// copied, and the shipped path above now calls the same two functions the new one does.
//
// The extraction is BEHAVIOUR-PRESERVING BY CONSTRUCTION: every statement below is the statement that
// was inline here, in the same order, against the same tx. The existing payments unit and integration
// suites are the proof, and they pass unchanged.

// LockPaymentRowsTx takes every named payment row, in ASCENDING id order, FOR NO KEY UPDATE, on the caller's
// transaction. A BATCH status writer (the bank claim of several payments against one statement line, till's
// MarkDeposited) calls it before its UPDATE: since the sync seq trigger (0422) that UPDATE takes the payments
// counter on the first row it moves, and without this it holds the counter while it locks the next row, which
// a reversal of that later payment (it holds the row, then wants the counter at its reversing insert) closes
// into a cycle. With the rows locked first no batch holds the counter while it waits on a row, and a reversal
// waits on a row holding nothing: LOCK-ORDER.md's "payment rows ascending, then the counter".
//
// FOR NO KEY UPDATE and not FOR UPDATE: a batch changes a payment's status and metadata, never a key column,
// and a payment_allocations insert (a settle naming it) takes FOR KEY SHARE on the row; NO KEY UPDATE does not
// conflict with that and still conflicts with itself and with the reversal's FOR UPDATE, so batches and
// reversals still queue. The payments module owns the table, so this is the one place other modules take the
// rows from.
func LockPaymentRowsTx(ctx context.Context, tx pgx.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx,
		`SELECT 1 FROM payments WHERE id = ANY($1::uuid[]) ORDER BY id FOR NO KEY UPDATE`, ids)
	if err != nil {
		return fmt.Errorf("payments: lock the named payment rows: %w", err)
	}
	rows.Close()
	return rows.Err()
}

// PaymentReversedTx reports whether a payment has been reversed (reversed_at is set). The payments module owns the
// table, so a module that must refuse an act on a reversed payment (bankrec's unmatch) asks here, on its own
// transaction, after it has taken the payment row with LockPaymentRowsTx so the answer cannot change under it. A
// missing row reads as not reversed.
//
// It also answers the payment's tender, because the one act that asks (the unmatch of a deposit) treats a reversed
// CARD differently from every other tender (SF-2d round 7).
func PaymentReversedTx(ctx context.Context, tx pgx.Tx, id string) (reversed bool, method string, err error) {
	err = tx.QueryRow(ctx, `SELECT reversed_at IS NOT NULL, method FROM payments WHERE id = $1::uuid`, id).Scan(&reversed, &method)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("payments: read whether the payment was reversed: %w", err)
	}
	return reversed, method, nil
}

// PaymentBranchesTx returns the branch of each named payment, keyed by the lower cased id (an id with no row is
// absent). The payments module owns the table; a module that must fence an act on a payment to the caller's branch
// reach (bankrec's match) reads the branch here, on its own transaction, AFTER LockPaymentRowsTx has taken the rows:
// the branch is a fact of the row, read under its lock, so it cannot be moved by another writer between the check
// and the claim.
func PaymentBranchesTx(ctx context.Context, tx pgx.Tx, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text, branch_id::text FROM payments WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("payments: read the payment branches: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, branch string
		if err := rows.Scan(&id, &branch); err != nil {
			return nil, fmt.Errorf("payments: read the payment branches: %w", err)
		}
		out[strings.ToLower(id)] = branch
	}
	return out, rows.Err()
}

// PaymentMatchFact holds the branch, tender method, gross amount, and till session presence of a payment row read
// under LockPaymentRowsTx. InTillSession says the payment was taken in a till session (FW1-05b): a till cash
// payment reaches the bank only by its lodgement, so it can never be matched alone as a PAYMENT target.
type PaymentMatchFact struct {
	BranchID      string
	Method        string
	AmountCents   int64
	InTillSession bool
}

// PaymentFactsTx returns the branch, tender method, gross amount, and till session presence of each named payment,
// keyed by the lowercased id (an id with no row is absent). Call it on the caller's transaction after
// LockPaymentRowsTx has locked the rows.
func PaymentFactsTx(ctx context.Context, tx pgx.Tx, ids []string) (map[string]PaymentMatchFact, error) {
	out := map[string]PaymentMatchFact{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text, branch_id::text, method, amount_cents, till_session_id IS NOT NULL FROM payments WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("payments: read the payment match facts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var f PaymentMatchFact
		if err := rows.Scan(&id, &f.BranchID, &f.Method, &f.AmountCents, &f.InTillSession); err != nil {
			return nil, fmt.Errorf("payments: read the payment match facts: %w", err)
		}
		out[strings.ToLower(id)] = f
	}
	return out, rows.Err()
}

// PaymentNumbersTx returns payment_no for a set of payment ids, keyed by the lower cased id. It is UNFENCED: it
// answers for any id it is given, whatever branch the payment belongs to. Call it only with ids the caller's own
// write has already named AND held to the caller's reach (bankrec's match fences its payment targets to the caller's
// branch reach before it asks for a number, and an out of reach id is never passed here). It must never be handed
// an id straight from a request. The payments
// module owns the table, so other modules that refuse a payment in words a dealer reads (the bank claim,
// MarkDeposited) take the number from here and never print the id. An id with no row is absent from the map.
func PaymentNumbersTx(ctx context.Context, tx pgx.Tx, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text, payment_no::text FROM payments WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("payments: read the payment numbers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, no string
		if err := rows.Scan(&id, &no); err != nil {
			return nil, fmt.Errorf("payments: read the payment numbers: %w", err)
		}
		out[strings.ToLower(id)] = no
	}
	return out, rows.Err()
}

// PaymentLabel is what a dealer reads for one payment: its number, or a plain word when the row is gone. Never the id.
func PaymentLabel(numbers map[string]string, id string) string {
	if no := numbers[strings.ToLower(id)]; no != "" {
		return no
	}
	return "a payment that cannot be found"
}

// lockPaymentsSequenceSQL takes the payments feed counter row WITHOUT advancing it: the upsert writes
// the value it already holds, so the row lock is taken and no feed number is used up. The insert arm
// seeds a missing row at 0, not at the column default of 1, so the settle's own increment that
// follows in the same transaction still hands out the first number, 1.
const lockPaymentsSequenceSQL = `INSERT INTO scope_sequences (scope, next_value) VALUES ('payments', 0)
	ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value`

// LockPaymentsSequence takes the payments sequence row on any transaction, without advancing it.
// It is the first money lock every payments writer takes after the order row (see the lock order at
// the top of this file), exported so a writer that settles through its own transaction, the quote
// counter door and the till's owed return among them, can take the same lock at the same place in
// the same order and so cannot close a cycle with a settle, a reversal, a received payment or a
// refund.
func LockPaymentsSequence(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, lockPaymentsSequenceSQL); err != nil {
		return fmt.Errorf("payments: lock payments sequence: %w", err)
	}
	return nil
}

// LockPaymentsSequenceTx is LockPaymentsSequence on the repository, for the writers that already
// hold one.
func (r *Repository) LockPaymentsSequenceTx(ctx context.Context, tx pgx.Tx) error {
	return LockPaymentsSequence(ctx, tx)
}

// lockAndTotal locks + re-validates every requested invoice, sums the settled amount and captures a
// branch for the payment header (payments.branch_id is NOT NULL).
//
// The payable ceiling is balance − holdback floored at zero: the Bill 60 legally-retained portion is
// not collectable and must be excluded before any pay slice (§4.2 #7).
//
// The payments sequence row is taken FIRST, before any invoice row, and WITHOUT advancing it (the
// settle's own INSERT draws its feed number later, once). That is the one money lock order written at
// the top of this file: every other writer that takes both the payments counter and an invoice takes
// the counter first, so a settle can never hold an invoice while it waits on a counter row another
// writer holds on its way to that same invoice.
//
// The invoice row is taken FOR NO KEY UPDATE, not FOR UPDATE. The settle only writes non key columns,
// so this still conflicts with every other invoice writer's row lock (any UPDATE of the row, and the
// POS account credit's explicit FOR UPDATE) and serialises against each of them exactly as before.
// What it no longer conflicts with is a FOR KEY SHARE, the lock a foreign key check takes: a POS
// return's INSERT INTO pos_returns holds the returned sale's invoice key share before its refund
// rail draws the payments counter, and a settle holding the counter while it waits on a FOR UPDATE
// that the key share blocks closes a cycle (the return's refund rail then waits on the counter). With
// NO KEY UPDATE the settle never waits on the return's share at all: no writer deletes invoices or
// changes their keys, so the share is never something the settle needs to hold back.
//
// takeDiscount is the door's rule (TM-2 fix round 2, P2-1): the money desk and the per invoice door
// pass true; the portal settle (PayWithStamps) passes false and settles at face, with no discount
// and no change. The portal rule is recorded there, with its follow on (plan row TM-2b).
func (r *Repository) lockAndTotal(ctx context.Context, tx pgx.Tx, customerID string, allocs []Allocation, dctx *discountContext, takeDiscount bool) (int64, string, int64, error) {
	if _, err := tx.Exec(ctx, lockPaymentsSequenceSQL); err != nil {
		return 0, "", 0, fmt.Errorf("pay: lock payments sequence: %w", err)
	}
	// The discount facts read under the locks this loop takes, so a context the caller did not
	// bring (the per invoice settle, the portal settle) is built here from the rows themselves and
	// the walk that did bring one re-reads nothing: its context is handed in already resolved.
	discountRows := make([]applyInvoiceRow, 0, len(allocs))
	var total, change int64
	var branchID string
	for _, a := range allocs {
		if a.IsNSFFee {
			var remaining int64
			var nsfBranch string
			err := tx.QueryRow(ctx, `
				SELECT `+openNSFFeeRemainingExpr+`, bp.branch_id::text
				  FROM payment_nsf n
				  JOIN payments bp ON bp.id = n.payment_id
				 WHERE n.id = $1::uuid AND COALESCE(n.customer_id, bp.customer_id) = $2::uuid
				   FOR NO KEY UPDATE OF n`,
				a.InvoiceID, customerID).Scan(&remaining, &nsfBranch)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return 0, "", 0, ErrNotFound
				}
				return 0, "", 0, fmt.Errorf("pay: lock nsf fee %q: %w", a.InvoiceID, err)
			}
			if a.AmountCents <= 0 || a.AmountCents > remaining {
				return 0, "", 0, httpx.BadRequestf("NSF fee %q balance changed; refresh and retry", a.InvoiceID)
			}
			total += a.AmountCents
			if branchID == "" {
				branchID = nsfBranch
			}
			continue
		}
		var balance, holdback int64
		var invBranch string
		var discountDay *time.Time
		var termsCode string
		err := tx.QueryRow(ctx,
			`SELECT balance_cents, holdback_cents, branch_id::text, discount_date, COALESCE(terms_code, '')
			 FROM invoices WHERE id = $2 AND customer_id = $1 FOR NO KEY UPDATE`,
			customerID, a.InvoiceID).Scan(&balance, &holdback, &invBranch, &discountDay, &termsCode)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, "", 0, ErrNotFound
			}
			return 0, "", 0, fmt.Errorf("pay: lock invoice %q: %w", a.InvoiceID, err)
		}
		payable := balance - holdback
		if payable < 0 {
			payable = 0
		}
		if a.AmountCents <= 0 || a.AmountCents > payable {
			return 0, "", 0, httpx.BadRequestf("invoice %q balance changed; refresh and retry", a.InvoiceID)
		}
		dRow := applyInvoiceRow{
			InvoiceID: a.InvoiceID, BalanceCents: balance, HoldbackCents: holdback,
			DiscountDate: discountDay, TermsCode: termsCode,
		}
		if err := loadDiscountRoom(ctx, tx, &dRow); err != nil {
			return 0, "", 0, err
		}
		discountRows = append(discountRows, dRow)
		total += a.AmountCents
		if branchID == "" {
			branchID = invBranch
		}
	}
	if !takeDiscount {
		return total, branchID, 0, nil
	}
	// The early payment discount (TM-2), derived here for every caller of the one settle: the per
	// invoice door and the portal settle bring no walk of their own, so the discount they take is
	// decided on the same rows under the same locks, with the same clock the apply's walk used when
	// the apply is the caller (its context arrives resolved and is not rebuilt).
	if dctx == nil {
		var err error
		dctx, err = r.newDiscountContextTx(ctx, tx, discountRows, nil)
		if err != nil {
			return 0, "", 0, err
		}
	}
	byID := make(map[string]applyInvoiceRow, len(discountRows))
	for _, row := range discountRows {
		byID[row.InvoiceID] = row
	}
	// ONE RULE AT EVERY DOOR (P3-2): money that reaches or passes an eligible invoice's discounted
	// payoff takes the discount and closes it, whichever door brought it. The invoice takes the
	// payoff (payable less the full discount, as the desk's walk does), and the rest of what the
	// caller named is change, returned for the settle to leave on the customer's account. A caller
	// that already walked (the apply) names no more than the payoff, so nothing is clamped there.
	for i := range allocs {
		if allocs[i].IsNSFFee {
			continue
		}
		row := byID[allocs[i].InvoiceID]
		payable := row.BalanceCents - row.HoldbackCents
		if payable < 0 {
			payable = 0
		}
		if full := dctx.fullDiscountOn(payable, row); full > 0 && allocs[i].AmountCents > payable-full {
			change += allocs[i].AmountCents - (payable - full)
			total -= allocs[i].AmountCents - (payable - full)
			allocs[i].AmountCents = payable - full
		}
		allocs[i].DiscountCents = dctx.discountOn(payable, allocs[i].AmountCents, row)
	}
	return total, branchID, change, nil
}

// applyAllocations writes the N allocations, moves the N invoice balances, writes the N subledger
// rows, leaves the unapplied change on the account when there is one (W3 item 0.10) and emits
// payment.received on the transactional outbox, everything that happens AFTER the header row exists.
// allocatedBy stamps the 0043 attribution column: a staff id for a counter-taken payment, "" for a
// portal (customer-initiated) one, which is exactly what that column means.
func applyAllocations(ctx context.Context, tx pgx.Tx, p *Payment, customerID, method, gatewayRef, branchID string, allocs []Allocation, allocatedBy string, changeCents int64) error {
	// Running account AR balance for the subledger balance_after, captured under the locks
	// above (so it reflects pre-settlement state, then decremented per allocation).
	var runningAR int64
	if err := tx.QueryRow(ctx, `
		SELECT
		  (SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
		    WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID'))
		+ (SELECT COALESCE(SUM(GREATEST(`+openNSFFeeRemainingExpr+`, 0)), 0)
		     FROM payment_nsf n
		     JOIN payments bp ON bp.id = n.payment_id
		    WHERE COALESCE(n.customer_id, bp.customer_id) = $1::uuid)`,
		customerID).Scan(&runningAR); err != nil {
		return fmt.Errorf("pay: read AR balance: %w", err)
	}

	for _, a := range allocs {
		if a.IsNSFFee {
			if _, err := tx.Exec(ctx, `
				UPDATE payments
				   SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object(
				         'nsf_fee_allocations',
				         COALESCE(metadata->'nsf_fee_allocations', '{}'::jsonb) || jsonb_build_object($2::text, $3::bigint)
				       )
				 WHERE id = $1::uuid`,
				p.ID, a.InvoiceID, a.AmountCents); err != nil {
				return fmt.Errorf("pay: stamp nsf fee allocation: %w", err)
			}
			runningAR -= a.AmountCents
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
					(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
				 VALUES ($1, 'ADJUSTMENT', $2, $3, 'payment_nsf', $4, $5, 'NSF fee payment')`,
				customerID, -a.AmountCents, runningAR, a.InvoiceID, branchID); err != nil {
				return fmt.Errorf("pay: insert nsf fee subledger txn: %w", err)
			}
			continue
		}
		// RETURNING created_at only (Q-044): the response echoes the persisted allocation
		// timestamp; the write itself is unchanged (a portal payment has no staff allocator).
		var by any
		if allocatedBy != "" {
			by = allocatedBy
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, discount_cents, allocated_by)
			 VALUES ($1, $2, $3, $4, $5::uuid) RETURNING created_at`,
			p.ID, a.InvoiceID, a.AmountCents, a.DiscountCents, by).Scan(&a.CreatedAt); err != nil {
			return fmt.Errorf("pay: insert allocation: %w", err)
		}
		a.AllocatedBy = allocatedBy

		// The invoice's balance move also draws a fresh INVOICES feed
		// seq beside the write (billing's own counter, the 0213
		// convention): a payment landing is the invoice change the
		// payer's device must see on its invoices feed, and a money
		// move that no pull can see is a feed that lies by omission.
		// The reach is the same cross-table one this settle already
		// takes on invoices (the file's own recorded posture).
		//
		// The early payment discount moves the balance with the money
		// (TM-2): the invoice's own terms percent of what this
		// application landed, decided in the same transaction.
		if _, err := tx.Exec(ctx, invoicesFeedSeqCTE+`
UPDATE invoices
			 SET balance_cents = balance_cents - $3 - $4,
				 status = CASE WHEN balance_cents - $3 - $4 <= 0 THEN 'PAID' ELSE 'PARTIALLY_PAID' END,
				 updated_at = now(),
				 seq = (SELECT next_value FROM iseq)
			 WHERE id = $2 AND customer_id = $1`,
			customerID, a.InvoiceID, a.AmountCents, a.DiscountCents); err != nil {
			return fmt.Errorf("pay: update invoice balance: %w", err)
		}

		// Subledger PAYMENT row: amount is negative (reduces AR); balance_after is the
		// running account balance after applying this slice.
		runningAR -= a.AmountCents
		if _, err := tx.Exec(ctx,
			`INSERT INTO customer_transactions
				(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id)
		 VALUES ($1, 'PAYMENT', $2, $3, 'payment', $4, $5)`,
			customerID, -a.AmountCents, runningAR, p.ID, branchID); err != nil {
			return fmt.Errorf("pay: insert subledger txn: %w", err)
		}
		// The discount's own subledger row (TM-2): an ADJUSTMENT against the invoice (source_type
		// 'invoice'), so the account's statement carries the discount beside the money and the
		// aging's reconstruction at a past date reads it from here without counting it twice (the
		// aging's current arm reads the invoice balance, which the move above already dropped).
		if a.DiscountCents > 0 {
			runningAR -= a.DiscountCents
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
					(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
			 VALUES ($1, 'ADJUSTMENT', $2, $3, 'invoice', $4, $5, $6)`,
				customerID, -a.DiscountCents, runningAR, a.InvoiceID, branchID,
				"early payment discount ("+p.PaymentNo+")"); err != nil {
				return fmt.Errorf("pay: insert the discount subledger txn: %w", err)
			}
			// The GL's half of the discount, emitted in the SAME transaction as the application:
			// the consumer posts DR discounts allowed / CR 1100 on the key
			// payment.discount.taken:<payment>:<invoice>, so a redelivered row posts once and the
			// 1100 tie moves with the balance move above.
			discountPayload, err := json.Marshal(map[string]any{
				"payment_id":     p.ID,
				"customer_id":    customerID,
				"invoice_id":     a.InvoiceID,
				"branch_id":      branchID,
				"discount_cents": a.DiscountCents,
				// FW1-01b: the receipt's own instant, so the discount journal posts in the month
				// the money was applied, not the month the consumer ran.
				"occurred_at": p.ReceivedAt,
			})
			if err != nil {
				return fmt.Errorf("pay: marshal payment.discount.taken: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO events_outbox (topic, payload) VALUES ('payment.discount.taken', $1)`,
				discountPayload); err != nil {
				return fmt.Errorf("pay: emit payment.discount.taken: %w", err)
			}
		}

		p.Allocations = append(p.Allocations, a)
	}

	// The unapplied change (W3 item 0.10): a receipt larger than the invoices it is applied to
	// leaves the rest as an on account credit for that customer and branch. One negative
	// ADJUSTMENT row in the 'receipt_change' family, keyed to the payment (the group the aging
	// groups and the credit application door later takes), branch stamped beside the payment. The
	// payment row's amount already carries the whole receipt, so the receipt journal credited 1100
	// with all of it and this row is the account's side of the same money: the aging shows the
	// credit as a negative item on the customer's row and the 1100 tie holds by construction.
	if changeCents > 0 {
		runningAR -= changeCents
		if _, err := tx.Exec(ctx,
			`INSERT INTO customer_transactions
				(customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
			 VALUES ($1, 'ADJUSTMENT', $2, $3, 'receipt_change', $4, $5, $6)`,
			customerID, -changeCents, runningAR, p.ID, branchID,
			"overpaid receipt left on account ("+p.PaymentNo+")"); err != nil {
			return fmt.Errorf("pay: insert the unapplied change row: %w", err)
		}
	}

	// Transactional outbox: emit payment.received in the SAME tx as the settle write so the GL posting
	// consumer (A-01) books the receipt exactly once and the AR tie-out stays trustworthy (W5/A-05, AC8).
	// The payload carries no branch/invoice, the consumer resolves finance facts via gl.PaymentReader.
	invoiceIDs := make([]string, 0, len(allocs))
	for _, a := range allocs {
		invoiceIDs = append(invoiceIDs, a.InvoiceID)
	}
	payload, err := json.Marshal(map[string]any{
		"payment_id":   p.ID,
		"customer_id":  customerID,
		"amount_cents": p.AmountCents,
		"method":       method,
		"gateway_ref":  gatewayRef,
		"invoice_ids":  invoiceIDs,
	})
	if err != nil {
		return fmt.Errorf("pay: marshal payment.received: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ('payment.received', $1)`, payload); err != nil {
		return fmt.Errorf("pay: emit payment.received: %w", err)
	}
	return nil
}

// OrderPayment is the settle of one payment slice against one invoice that the
// POS Pay balance door (item 9.4) applies on its own transaction, after the
// till has written the payments row. AllocatedBy is the staff id ("" for none).
type OrderPayment struct {
	PaymentID   string
	InvoiceID   string
	CustomerID  string
	BranchID    string
	AmountCents int64
	AllocatedBy string
}

// AllocateToInvoiceTx applies one payment slice to one invoice on the caller's
// transaction: the allocation row, then the settle slice (applySliceTx: the balance
// move with its invoices feed stamp and status flip, and the subledger PAYMENT row
// with the running account balance under the locks the caller already holds, the
// money lock order above). It is applyAllocations' per-slice body without the
// outbox emit, which the till's tender owns for a payment it wrote: one spelling
// of applying money to an invoice, so the ledger keeps one answer to the question.
func (r *Repository) AllocateToInvoiceTx(ctx context.Context, tx pgx.Tx, s OrderPayment) error {
	var by any
	if s.AllocatedBy != "" {
		by = s.AllocatedBy
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, allocated_by)
		 VALUES ($1::uuid, $2::uuid, $3, $4)`,
		s.PaymentID, s.InvoiceID, s.AmountCents, by); err != nil {
		return fmt.Errorf("payments: allocation: %w", err)
	}
	return applySliceTx(ctx, tx, slice{
		CustomerID: s.CustomerID, InvoiceID: s.InvoiceID, BranchID: s.BranchID, AmountCents: s.AmountCents,
		SourceType: "payment", SourceRefID: s.PaymentID,
	})
}

// slice is one invoice's share of money being applied: the settle's own unit. SourceType and SourceRefID name the
// act on the subledger row ("payment" and the payment for a settle, "payment_reapply" and the reversed payment for a
// re-application), and Memo is the row's free text.
type slice struct {
	CustomerID, InvoiceID, BranchID string
	AmountCents                     int64
	SourceType, SourceRefID, Memo   string
}

// applySliceTx is the settle slice every way of applying money to an invoice shares: the invoice's balance move (with
// its invoices feed seq and the status flip to PAID at zero, PARTIALLY_PAID otherwise) and the AR subledger PAYMENT
// row, whose balance_after is the account's open balances after the move. The caller holds the invoice row (the money
// lock order, step 3) and writes whatever record of the act it owns (an allocation row, a re-application row) and its
// outbox event.
func applySliceTx(ctx context.Context, tx pgx.Tx, s slice) error {
	// The invoice's balance move also draws a fresh INVOICES feed
	// seq beside the write (billing's own counter, the 0213
	// convention): a payment landing is the invoice change the
	// payer's device must see on its invoices feed, and a money
	// move that no pull can see is a feed that lies by omission.
	if _, err := tx.Exec(ctx, invoicesFeedSeqCTE+`
UPDATE invoices
		   SET balance_cents = balance_cents - $3,
		       status = CASE WHEN balance_cents - $3 <= 0 THEN 'PAID' ELSE 'PARTIALLY_PAID' END,
		       updated_at = now(),
		       seq = (SELECT next_value FROM iseq)
		 WHERE id = $2::uuid AND customer_id = $1::uuid`,
		s.CustomerID, s.InvoiceID, s.AmountCents); err != nil {
		return fmt.Errorf("payments: invoice balance move: %w", err)
	}
	// The running account balance AFTER this slice: the balance move above
	// already ran, so the open balances' sum is the after figure.
	var runningAR int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
		  WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		s.CustomerID).Scan(&runningAR); err != nil {
		return fmt.Errorf("payments: AR balance read: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'PAYMENT', $2, $3, $4, $5::uuid, $6::uuid, $7)`,
		s.CustomerID, -s.AmountCents, runningAR, s.SourceType, s.SourceRefID, s.BranchID, s.Memo); err != nil {
		return fmt.Errorf("payments: subledger row: %w", err)
	}
	return nil
}

// KeyedPayment is the payments row an idempotency key already wrote, with the
// first invoice its allocation paid ("" when none).
type KeyedPayment struct {
	PaymentID      string
	Method         string
	AmountCents    int64
	FirstInvoiceID string
}

// PaymentByKeyTx answers the payments row an idempotency key wrote (a replay's
// facts), on the caller's transaction.
//
// FirstInvoiceID is the invoice the payment's application STILL pays (0450): an application whose
// reversal row stands beside it was taken back, so it no longer names an invoice, exactly as it named
// none before the kind existed (a bounce deleted the rows). The order is by the application's own
// instant, so the answer does not depend on which row the planner returns first.
func (r *Repository) PaymentByKeyTx(ctx context.Context, tx pgx.Tx, key string) (KeyedPayment, bool, error) {
	var k KeyedPayment
	err := tx.QueryRow(ctx, `
		SELECT p.id::text, p.method, p.amount_cents,
		       COALESCE((SELECT a.invoice_id::text FROM payment_allocations a
		                  WHERE a.payment_id = p.id AND a.kind = 'APPLICATION'
		                    AND NOT EXISTS (SELECT 1 FROM payment_allocations r
		                                     WHERE r.payment_id = a.payment_id AND r.invoice_id = a.invoice_id
		                                       AND r.kind = 'REVERSAL')
		                  ORDER BY a.created_at, a.invoice_id LIMIT 1), '')
		  FROM payments p WHERE p.idempotency_key = $1`, key).
		Scan(&k.PaymentID, &k.Method, &k.AmountCents, &k.FirstInvoiceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return KeyedPayment{}, false, nil
		}
		return KeyedPayment{}, false, fmt.Errorf("payments: payment by key: %w", err)
	}
	return k, true, nil
}

// InvoicePayment is one RECEIVED payment allocated to one invoice.
type InvoicePayment struct {
	InvoiceID   string
	PaymentID   string
	Method      string
	AmountCents int64
}

// ReceivedPaymentsForInvoices lists the RECEIVED payments allocated to the
// given invoices, oldest first (the money trail), on the caller's transaction. A reversed original keeps status
// RECEIVED (the reversal stamps reversed_at), so it is left out: its money was returned by the reversal, and the
// trail is the money now held against the order (PR 602 round 2). The list's type has no reversed marker to add, so
// leaving the row out keeps every consumer's shape.
func (r *Repository) ReceivedPaymentsForInvoices(ctx context.Context, tx pgx.Tx, invoiceIDs []string) ([]InvoicePayment, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.invoice_id::text, p.id::text, p.method, p.amount_cents
		  FROM payment_allocations a
		  JOIN payments p ON p.id = a.payment_id AND p.status = 'RECEIVED' AND p.reversed_at IS NULL
		 WHERE a.invoice_id = ANY($1::uuid[])
		 ORDER BY p.received_at, p.id`, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("payments: received payments for invoices: %w", err)
	}
	defer rows.Close()
	var out []InvoicePayment
	for rows.Next() {
		var p InvoicePayment
		if err := rows.Scan(&p.InvoiceID, &p.PaymentID, &p.Method, &p.AmountCents); err != nil {
			return nil, fmt.Errorf("payments: scan received payment: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ReappliedToInvoice lists the slices of reversed payments' held money applied to one invoice, oldest first (SF-2r): the
// register's applied entries for what re-application did, since it writes no payment_allocations row. The applier's
// display name is joined here (users is this file's to read).
func (r *Repository) ReappliedToInvoice(ctx context.Context, invoiceID string) ([]InvoiceReapplication, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	iid, err := uuid.Parse(invoiceID)
	if err != nil {
		return []InvoiceReapplication{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT ra.id::text, ra.payment_id::text, p.payment_no::text, p.method, ra.amount_cents, ra.created_at,
		        COALESCE(ra.applied_by::text, ''), COALESCE(u.display_name, '')
		   FROM payment_reapplications ra
		   JOIN payments p ON p.id = ra.payment_id
		   LEFT JOIN users u ON u.id = ra.applied_by
		  WHERE ra.invoice_id = $1::uuid
		  ORDER BY ra.created_at, ra.id`, iid.String())
	if err != nil {
		return nil, fmt.Errorf("reapplied: read: %w", err)
	}
	defer rows.Close()
	out := []InvoiceReapplication{}
	for rows.Next() {
		var x InvoiceReapplication
		if err := rows.Scan(&x.ID, &x.PaymentID, &x.PaymentNo, &x.Method, &x.AmountCents, &x.AppliedAt, &x.AppliedBy, &x.AppliedName); err != nil {
			return nil, fmt.Errorf("reapplied: scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ReappliedToOrder lists the slices of reversed payments' held money applied to the invoices of one order, oldest first
// (SF-2r round 2): the order's activity reads it through the order module's seam.
func (r *Repository) ReappliedToOrder(ctx context.Context, orderID string) ([]OrderReapplication, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	oid, err := uuid.Parse(orderID)
	if err != nil {
		return []OrderReapplication{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT ra.id::text, ra.payment_id::text, p.payment_no::text, p.method, i.invoice_no::text, ra.amount_cents,
		        ra.created_at, COALESCE(ra.applied_by::text, ''), COALESCE(u.display_name, '')
		   FROM payment_reapplications ra
		   JOIN payments p ON p.id = ra.payment_id
		   JOIN invoices i ON i.id = ra.invoice_id
		   LEFT JOIN users u ON u.id = ra.applied_by
		  WHERE i.order_id = $1::uuid
		  ORDER BY ra.created_at, ra.id`, oid.String())
	if err != nil {
		return nil, fmt.Errorf("reapplied: read by order: %w", err)
	}
	defer rows.Close()
	out := []OrderReapplication{}
	for rows.Next() {
		var x OrderReapplication
		if err := rows.Scan(&x.ID, &x.PaymentID, &x.PaymentNo, &x.Method, &x.InvoiceNo, &x.AmountCents, &x.AppliedAt, &x.AppliedBy, &x.AppliedName); err != nil {
			return nil, fmt.Errorf("reapplied: scan by order: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
