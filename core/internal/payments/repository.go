package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

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

	var out []PaymentMethod
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

	var out []Payment
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
	if len(paymentIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT pa.payment_id::text, pa.invoice_id::text, pa.amount_cents,
		        COALESCE(pa.allocated_by::text, ''), COALESCE(u.display_name, ''), pa.created_at
		 FROM payment_allocations pa
		 LEFT JOIN users u ON u.id = pa.allocated_by
		 WHERE pa.payment_id = ANY($1) ORDER BY pa.invoice_id`, paymentIDs)
	if err != nil {
		return nil, fmt.Errorf("list allocations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var paymentID string
		var a Allocation
		if err := rows.Scan(&paymentID, &a.InvoiceID, &a.AmountCents,
			&a.AllocatedBy, &a.AllocatedByName, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan allocation: %w", err)
		}
		out[paymentID] = append(out[paymentID], a)
	}
	return out, rows.Err()
}

// Pay records ONE settled charge across N invoices in a single transaction (the
// all-or-nothing split that designs away card partial-failure, sec. 4.2). Each invoice is
// re-read FOR UPDATE and its balance re-validated against the allocation before any write,
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

	total, branchID, err := lockAndTotal(ctx, tx, customerID, allocs)
	if err != nil {
		return Payment{}, err
	}

	// Insert the payment header, drawing a fresh payment_no from the sequence.
	var p Payment
	var pCustomerID, pGatewayRef *string
	err = tx.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id, gateway_ref)
		 VALUES (nextval('payment_no_seq')::text, $1, $2, $3, 'RECEIVED', $4, $5)
		 RETURNING id::text, payment_no::text, customer_id::text, method, amount_cents, status, gateway_ref, received_at`,
		customerID, method, total, branchID, gatewayRef).Scan(
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

	if err := applyAllocations(ctx, tx, &p, customerID, method, gatewayRef, branchID, allocs, ""); err != nil {
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

// lockAndTotal locks + re-validates every requested invoice, sums the settled amount and captures a
// branch for the payment header (payments.branch_id is NOT NULL).
//
// The payable ceiling is balance − holdback floored at zero: the Bill 60 legally-retained portion is
// not collectable and must be excluded before any pay slice (§4.2 #7).
func lockAndTotal(ctx context.Context, tx pgx.Tx, customerID string, allocs []Allocation) (int64, string, error) {
	var total int64
	var branchID string
	for _, a := range allocs {
		var balance, holdback int64
		var invBranch string
		err := tx.QueryRow(ctx,
			`SELECT balance_cents, holdback_cents, branch_id::text
			 FROM invoices WHERE id = $2 AND customer_id = $1 FOR UPDATE`,
			customerID, a.InvoiceID).Scan(&balance, &holdback, &invBranch)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, "", ErrNotFound
			}
			return 0, "", fmt.Errorf("pay: lock invoice %q: %w", a.InvoiceID, err)
		}
		payable := balance - holdback
		if payable < 0 {
			payable = 0
		}
		if a.AmountCents <= 0 || a.AmountCents > payable {
			return 0, "", httpx.BadRequestf("invoice %q balance changed; refresh and retry", a.InvoiceID)
		}
		total += a.AmountCents
		if branchID == "" {
			branchID = invBranch
		}
	}
	return total, branchID, nil
}

// applyAllocations writes the N allocations, moves the N invoice balances, writes the N subledger
// rows and emits payment.received on the transactional outbox, everything that happens AFTER the
// header row exists. allocatedBy stamps the 0043 attribution column: a staff id for a counter-taken
// payment, "" for a portal (customer-initiated) one, which is exactly what that column means.
func applyAllocations(ctx context.Context, tx pgx.Tx, p *Payment, customerID, method, gatewayRef, branchID string, allocs []Allocation, allocatedBy string) error {
	// Running account AR balance for the subledger balance_after, captured under the locks
	// above (so it reflects pre-settlement state, then decremented per allocation).
	var runningAR int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
		 WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		customerID).Scan(&runningAR); err != nil {
		return fmt.Errorf("pay: read AR balance: %w", err)
	}

	for _, a := range allocs {
		// RETURNING created_at only (Q-044): the response echoes the persisted allocation
		// timestamp; the write itself is unchanged (a portal payment has no staff allocator).
		var by any
		if allocatedBy != "" {
			by = allocatedBy
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, allocated_by)
			 VALUES ($1, $2, $3, $4::uuid) RETURNING created_at`,
			p.ID, a.InvoiceID, a.AmountCents, by).Scan(&a.CreatedAt); err != nil {
			return fmt.Errorf("pay: insert allocation: %w", err)
		}
		a.AllocatedBy = allocatedBy

		if _, err := tx.Exec(ctx,
			`UPDATE invoices
			 SET balance_cents = balance_cents - $3,
				 status = CASE WHEN balance_cents - $3 <= 0 THEN 'PAID' ELSE 'PARTIALLY_PAID' END,
				 updated_at = now()
			 WHERE id = $2 AND customer_id = $1`,
			customerID, a.InvoiceID, a.AmountCents); err != nil {
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

		p.Allocations = append(p.Allocations, a)
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
