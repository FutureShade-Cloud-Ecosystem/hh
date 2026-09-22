package till

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errNoDB = errors.New("till: database not configured")

// Repository is the till's pgx access layer. It reads/writes ONLY till-owned tables (till_sessions,
// payments, payment_allocations, till_movements, till_tender_totals, till_denomination_counts,
// till_policy). The money/state writes ride INSIDE the caller's sale tx (pgx.Tx); the lifecycle paths
// own their tx via the pool.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold/offline mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isInvalidUUID reports whether err is a Postgres invalid-text-representation (22P02), raised when
// a malformed non-UUID path id reaches a `WHERE id = $1` on a uuid column. Folding it into the same
// not-found branch as pgx.ErrNoRows keeps a garbage id indistinguishable from a missing one (404,
// IDOR-safe) instead of surfacing as an opaque HTTP 500.
func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// sessionCols is the canonical session projection (kept in one place so the tx + pool reads agree).
const sessionCols = `id::text, branch_id::text, station, status, opened_by::text, float_cents,
	COALESCE(closing_float_cents, 0), blind_close, override_notes`

func scanSession(row pgx.Row) (TillSession, error) {
	var s TillSession
	err := row.Scan(&s.ID, &s.BranchID, &s.Station, &s.Status, &s.OpenedBy,
		&s.FloatCents, &s.ClosingFloatCents, &s.BlindClose, &s.OverrideNotes)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return TillSession{}, errNotFound
	}
	if err != nil {
		return TillSession{}, fmt.Errorf("till: scan session: %w", err)
	}
	return s, nil
}

// - in-caller-tx writes (A-19 sale path) -

func (r *Repository) GetSessionTx(ctx context.Context, tx pgx.Tx, id string) (TillSession, error) {
	return scanSession(tx.QueryRow(ctx, `SELECT `+sessionCols+` FROM till_sessions WHERE id = $1`, id))
}

func (r *Repository) FindPaymentByKey(ctx context.Context, tx pgx.Tx, key string) (string, string, bool, error) {
	if key == "" {
		return "", "", false, nil
	}
	var id, gatewayRef string
	err := tx.QueryRow(ctx,
		`SELECT id::text, COALESCE(gateway_ref, '') FROM payments WHERE idempotency_key = $1 AND idempotency_key <> '' LIMIT 1`, key,
	).Scan(&id, &gatewayRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("till: find payment by key: %w", err)
	}
	return id, gatewayRef, true, nil
}

// InsertPayment writes the OWNED payments row. It is replay-safe: ON CONFLICT (idempotency_key) DO
// NOTHING RETURNING; on a conflict (DO NOTHING returns no row) it SELECTs the existing id. payment_no
// is generated from the 0010 sequence. The raw token is NEVER written, only the opaque gateway_ref.
func (r *Repository) InsertPayment(ctx context.Context, tx pgx.Tx, p PaymentRow) (string, string, bool, error) {
	const q = `
		INSERT INTO payments
		  (payment_no, customer_id, method, amount_cents, status, branch_id, till_session_id,
		   gateway_ref, card_type, received_by, idempotency_key, offline_captured, signature_ref)
		VALUES
		  ('PMT-' || lpad(nextval('payment_no_seq')::text, 6, '0'),
		   $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING
		RETURNING id::text, payment_no::text`
	var id, paymentNo string
	err := tx.QueryRow(ctx, q,
		nullStr(p.CustomerID), p.Method, p.AmountCents, p.Status, p.BranchID, nullStr(p.TillSessionID),
		p.GatewayRef, nullStr(p.CardType), nullStr(p.ReceivedBy), p.IdempotencyKey, p.OfflineCaptured, p.SignatureRef,
	).Scan(&id, &paymentNo)
	if err == nil {
		return id, paymentNo, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, fmt.Errorf("till: insert payment: %w", err)
	}
	// Conflict on the idempotency key: the row already exists, return its id (no second row).
	existing, _, found, ferr := r.FindPaymentByKey(ctx, tx, p.IdempotencyKey)
	if ferr != nil {
		return "", "", false, ferr
	}
	if !found {
		return "", "", false, fmt.Errorf("till: insert payment conflict but no existing row for key")
	}
	return existing, "", false, nil
}

func (r *Repository) InsertAllocation(ctx context.Context, tx pgx.Tx, paymentID, invoiceID string, amountCents int64, allocatedBy string) error {
	// allocated_by (0043, Q-044): WHO matched this payment to this invoice, stamped on the allocation.
	_, err := tx.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, allocated_by) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (payment_id, invoice_id) DO UPDATE SET amount_cents = payment_allocations.amount_cents + EXCLUDED.amount_cents`,
		paymentID, invoiceID, amountCents, nullStr(allocatedBy))
	if err != nil {
		return fmt.Errorf("till: insert allocation: %w", err)
	}
	return nil
}

func (r *Repository) AllocatedTotal(ctx context.Context, tx pgx.Tx, paymentID string) (int64, error) {
	var total int64
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents), 0) FROM payment_allocations WHERE payment_id = $1`, paymentID,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("till: allocated total: %w", err)
	}
	return total, nil
}

func (r *Repository) PaymentAmount(ctx context.Context, tx pgx.Tx, paymentID string) (int64, error) {
	var amt int64
	err := tx.QueryRow(ctx, `SELECT amount_cents FROM payments WHERE id = $1`, paymentID).Scan(&amt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("till: payment amount: %w", err)
	}
	return amt, nil
}

// - drawer lifecycle (own pool/tx) -

func (r *Repository) OpenSession(ctx context.Context, s TillSession, denoms []DenominationCount) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("till: begin open: %w", err)
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents, blind_close)
		 VALUES ($1, $2, 'OPEN', $3, $4, $5) RETURNING id::text`,
		s.BranchID, s.Station, s.OpenedBy, s.FloatCents, s.BlindClose,
	).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			// uq_till_open_per_drawer: the drawer is already open. Opening an open drawer ATTACHES to the
			// existing session, counter semantics: a station has ONE physical drawer, and its float was
			// declared when it was first opened (the new float/denoms are ignored, the original count
			// stands). The failed INSERT aborted the tx, so the lookup runs on the pool after rollback.
			_ = tx.Rollback(ctx)
			if aerr := r.pool.QueryRow(ctx,
				`SELECT id::text FROM till_sessions WHERE branch_id = $1 AND station = $2 AND status = 'OPEN'`,
				s.BranchID, s.Station).Scan(&id); aerr != nil {
				return "", errConflict // raced with a close between INSERT and lookup, surface the conflict
			}
			return id, nil
		}
		return "", fmt.Errorf("till: open session: %w", err)
	}
	if err := insertDenoms(ctx, tx, id, "OPEN", denoms); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("till: commit open: %w", err)
	}
	return id, nil
}

func insertDenoms(ctx context.Context, tx pgx.Tx, sessionID, phase string, denoms []DenominationCount) error {
	for _, d := range denoms {
		if _, err := tx.Exec(ctx,
			`INSERT INTO till_denomination_counts (till_session_id, phase, denomination_cents, quantity)
			 VALUES ($1, $2, $3, $4)`,
			sessionID, phase, d.DenominationCents, d.Quantity); err != nil {
			return fmt.Errorf("till: insert denomination: %w", err)
		}
	}
	return nil
}

// FindOpenSession answers the OPEN drawer at a branch/station, DIB-488 rev 5 (the POS mount-time
// till rediscovery). No row is an ordinary answer: found=false, nil error.
func (r *Repository) FindOpenSession(ctx context.Context, branchID, station string) (string, time.Time, bool, error) {
	if r.pool == nil {
		return "", time.Time{}, false, errNoDB
	}
	var id string
	var openedAt time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, opened_at FROM till_sessions WHERE branch_id = $1 AND station = $2 AND status = 'OPEN'`,
		branchID, station).Scan(&id, &openedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("till: find open session: %w", err)
	}
	return id, openedAt, true, nil
}

func (r *Repository) GetSession(ctx context.Context, id string) (TillSession, error) {
	if r.pool == nil {
		return TillSession{}, errNoDB
	}
	return scanSession(r.pool.QueryRow(ctx, `SELECT `+sessionCols+` FROM till_sessions WHERE id = $1`, id))
}

func (r *Repository) SessionTenders(ctx context.Context, sessionID string) ([]TenderTotal, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT method, expected_cents, COALESCE(actual_cents, 0), lodged_cents
		 FROM till_tender_totals WHERE till_session_id = $1 ORDER BY method`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("till: session tenders: %w", err)
	}
	defer rows.Close()
	var out []TenderTotal
	for rows.Next() {
		var t TenderTotal
		if err := rows.Scan(&t.Method, &t.ExpectedCents, &t.ActualCents, &t.LodgedCents); err != nil {
			return nil, fmt.Errorf("till: scan tender: %w", err)
		}
		t.VarianceCents = t.ActualCents - t.ExpectedCents
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repository) SessionMovements(ctx context.Context, sessionID string) ([]Movement, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, movement_type, method, amount_cents, reason, reference
		 FROM till_movements WHERE till_session_id = $1 ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("till: session movements: %w", err)
	}
	defer rows.Close()
	var out []Movement
	for rows.Next() {
		var m Movement
		if err := rows.Scan(&m.ID, &m.MovementType, &m.Method, &m.AmountCents, &m.Reason, &m.Reference); err != nil {
			return nil, fmt.Errorf("till: scan movement: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repository) InsertMovement(ctx context.Context, m MovementRow) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx, insertMovementSQL,
		m.TillSessionID, m.MovementType, m.Method, m.AmountCents, m.Reason, m.Reference, nullStr(m.CreatedBy))
	if err != nil {
		return fmt.Errorf("till: insert movement: %w", err)
	}
	return nil
}

const insertMovementSQL = `INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`

// InsertMovementTx is InsertMovement inside the CALLER'S tx (the POS return path, the PAID_OUT
// cash-refund movement must commit or roll back with the return it belongs to).
func (r *Repository) InsertMovementTx(ctx context.Context, tx pgx.Tx, m MovementRow) error {
	_, err := tx.Exec(ctx, insertMovementSQL,
		m.TillSessionID, m.MovementType, m.Method, m.AmountCents, m.Reason, m.Reference, nullStr(m.CreatedBy))
	if err != nil {
		return fmt.Errorf("till: insert movement: %w", err)
	}
	return nil
}

func (r *Repository) ExpectedTenders(ctx context.Context, sessionID string) (map[string]int64, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT method, COALESCE(SUM(amount_cents), 0) FROM payments
		 WHERE till_session_id = $1 AND status = 'RECEIVED' GROUP BY method`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("till: expected tenders: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var method string
		var sum int64
		if err := rows.Scan(&method, &sum); err != nil {
			return nil, fmt.Errorf("till: scan expected: %w", err)
		}
		out[method] = sum
	}
	return out, rows.Err()
}

func (r *Repository) TenderActuals(ctx context.Context, sessionID string) (map[string]int64, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT method, COALESCE(actual_cents, 0) FROM till_tender_totals WHERE till_session_id = $1`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("till: tender actuals: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var method string
		var actual int64
		if err := rows.Scan(&method, &actual); err != nil {
			return nil, fmt.Errorf("till: scan actual: %w", err)
		}
		out[method] = actual
	}
	return out, rows.Err()
}

func (r *Repository) MovementCashTotals(ctx context.Context, sessionID string) (MovementTotals, error) {
	if r.pool == nil {
		return MovementTotals{}, errNoDB
	}
	var mt MovementTotals
	err := r.pool.QueryRow(ctx,
		`SELECT
		   COALESCE(SUM(amount_cents) FILTER (WHERE movement_type = 'PAID_IN'), 0),
		   COALESCE(SUM(amount_cents) FILTER (WHERE movement_type IN ('PAID_OUT', 'DROP')), 0),
		   COALESCE(SUM(amount_cents) FILTER (WHERE movement_type = 'LODGEMENT'), 0)
		 FROM till_movements WHERE till_session_id = $1 AND method = 'CASH'`, sessionID,
	).Scan(&mt.PaidInCash, &mt.PaidOutCash, &mt.LodgementCash)
	if err != nil {
		return MovementTotals{}, fmt.Errorf("till: movement totals: %w", err)
	}
	return mt, nil
}

func (r *Repository) UpsertTenderActuals(ctx context.Context, sessionID string, actuals []TenderActual) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("till: begin count: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, a := range actuals {
		// expected_cents is the close-time snapshot of Σ(payments of this method); actual_cents is the
		// blind count. Both keyed UNIQUE (till_session_id, method).
		if _, err := tx.Exec(ctx,
			`INSERT INTO till_tender_totals (till_session_id, method, actual_cents, expected_cents)
			 VALUES ($1, $2, $3,
			   COALESCE((SELECT SUM(amount_cents) FROM payments
			              WHERE till_session_id = $1 AND method = $2 AND status = 'RECEIVED'), 0))
			 ON CONFLICT (till_session_id, method)
			 DO UPDATE SET actual_cents = EXCLUDED.actual_cents, expected_cents = EXCLUDED.expected_cents`,
			sessionID, a.Method, a.ActualCents); err != nil {
			return fmt.Errorf("till: upsert tender actual: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) InsertDenominations(ctx context.Context, sessionID, phase string, denoms []DenominationCount) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("till: begin denoms: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := insertDenoms(ctx, tx, sessionID, phase, denoms); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) SetCounted(ctx context.Context, sessionID, countedBy string, at time.Time, closingFloatCents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE till_sessions
		 SET status = 'COUNTED', counted_by = $2, counted_at = $3, closing_float_cents = $4, updated_at = now()
		 WHERE id = $1 AND status = 'OPEN'`,
		sessionID, nullStr(countedBy), at, closingFloatCents)
	if err != nil {
		return fmt.Errorf("till: set counted: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errConflict
	}
	return nil
}

func (r *Repository) SetCompleted(ctx context.Context, sessionID, branchID, completedBy string, at time.Time, overShortCents, countedCashCents int64, overrideNotes string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("till: begin complete: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE till_sessions
		 SET status = 'CLOSED', completed_by = $2, completed_at = $3, closed_by = $2, closed_at = $3,
		     over_short_cents = $4, counted_cents = $5, override_notes = $6, updated_at = now()
		 WHERE id = $1 AND status = 'COUNTED'`,
		sessionID, nullStr(completedBy), at, overShortCents, countedCashCents, overrideNotes)
	if err != nil {
		return fmt.Errorf("till: set completed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errConflict
	}

	// Transactional outbox: emit till.session.completed (over/short facts) in the SAME tx as the close so
	// the GL over/short journal (6350 / 1020) posts exactly once (W5/A-05 AC9).
	payload, err := json.Marshal(map[string]any{
		"session_id":       sessionID,
		"branch_id":        branchID,
		"over_short_cents": overShortCents,
		"counted_cents":    countedCashCents,
	})
	if err != nil {
		return fmt.Errorf("till: marshal session completed: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ('till.session.completed', $1)`, payload); err != nil {
		return fmt.Errorf("till: emit session completed: %w", err)
	}
	return tx.Commit(ctx)
}

// Policy returns the branch variance threshold + close grain. A branch with no row falls back to the
// conservative org default (threshold 0 → any non-zero variance routes to a manager).
func (r *Repository) Policy(ctx context.Context, branchID string) (TillPolicy, error) {
	if r.pool == nil {
		return TillPolicy{}, errNoDB
	}
	var p TillPolicy
	err := r.pool.QueryRow(ctx,
		`SELECT branch_id::text, variance_threshold_cents, close_grain, require_blind_close
		 FROM till_policy WHERE branch_id = $1`, branchID,
	).Scan(&p.BranchID, &p.VarianceThresholdCents, &p.CloseGrain, &p.RequireBlindClose)
	if errors.Is(err, pgx.ErrNoRows) {
		return TillPolicy{BranchID: branchID, VarianceThresholdCents: 0, CloseGrain: "SESSION", RequireBlindClose: true}, nil
	}
	if err != nil {
		return TillPolicy{}, fmt.Errorf("till: policy: %w", err)
	}
	return p, nil
}

// - W5 A-05: NSF + credit holds + declined-attempt log -

// PaymentNSFContext resolves the finance facts of a payment for the NSF decision (non-tx read).
func (r *Repository) PaymentNSFContext(ctx context.Context, paymentID string) (customerID, branchID, status, method string, amountCents int64, found bool, err error) {
	if r.pool == nil {
		return "", "", "", "", 0, false, errNoDB
	}
	err = r.pool.QueryRow(ctx,
		`SELECT COALESCE(customer_id::text, ''), branch_id::text, status, method, amount_cents
		 FROM payments WHERE id = $1`, paymentID,
	).Scan(&customerID, &branchID, &status, &method, &amountCents)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return "", "", "", "", 0, false, nil
	}
	if err != nil {
		return "", "", "", "", 0, false, fmt.Errorf("till: payment nsf context: %w", err)
	}
	return customerID, branchID, status, method, amountCents, true, nil
}

// NSFPolicyFor reads the effective NSF policy: a branch row overrides the org default (branch_id IS NULL).
func (r *Repository) NSFPolicyFor(ctx context.Context, branchID string) (NSFPolicy, error) {
	if r.pool == nil {
		return NSFPolicy{}, errNoDB
	}
	var p NSFPolicy
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(branch_id::text, ''), fee_cents, fee_taxable, fee_account_code::text, auto_hold, notes
		 FROM nsf_policy
		 WHERE branch_id = $1::uuid OR branch_id IS NULL
		 ORDER BY branch_id NULLS LAST
		 LIMIT 1`, nullStr(branchID),
	).Scan(&p.BranchID, &p.FeeCents, &p.FeeTaxable, &p.FeeAccountCode, &p.AutoHold, &p.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		// Conservative built-in default if the seed row is somehow absent (matches 0035 placeholders).
		return NSFPolicy{FeeCents: 4500, FeeTaxable: false, FeeAccountCode: "4200", AutoHold: true}, nil
	}
	if err != nil {
		return NSFPolicy{}, fmt.Errorf("till: nsf policy: %w", err)
	}
	return p, nil
}

// MarkNSF performs the whole bounce atomically (see the service doc). Idempotent: a payment already BOUNCED
// returns errAlreadyBounced under the in-tx FOR UPDATE guard. It NEVER writes a reversing tax snapshot.
func (r *Repository) MarkNSF(ctx context.Context, in nsfReverseInput) (nsfReverseResult, error) {
	if r.pool == nil {
		return nsfReverseResult{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: begin nsf: %w", err)
	}
	defer tx.Rollback(ctx)

	var res nsfReverseResult
	var status string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(customer_id::text, ''), branch_id::text, method, status, amount_cents
		 FROM payments WHERE id = $1 FOR UPDATE`, in.PaymentID,
	).Scan(&res.CustomerID, &res.BranchID, &res.Method, &status, &res.AmountCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return nsfReverseResult{}, errNotFound
	}
	if err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: lock payment: %w", err)
	}
	if status == "BOUNCED" {
		return nsfReverseResult{}, errAlreadyBounced
	}
	// Capture whether the receipt had already settled to the bank (DEPOSITED) BEFORE we flip it to BOUNCED.
	// The GL payment.bounced consumer needs this to reverse the right leg: DEPOSITED → bank 1000, else the
	// still-undeposited 1020. It cannot be recovered from PaymentReader after the flip, so it rides the event.
	clearedToBank := status == "DEPOSITED"

	if _, err := tx.Exec(ctx, `UPDATE payments SET status = 'BOUNCED', updated_at = now() WHERE id = $1`, in.PaymentID); err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: flip bounced: %w", err)
	}

	// Reverse every allocation + reopen its invoice (restore the balance the payment had cleared).
	rows, err := tx.Query(ctx, `SELECT invoice_id::text, amount_cents FROM payment_allocations WHERE payment_id = $1`, in.PaymentID)
	if err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: read allocations: %w", err)
	}
	type alloc struct {
		inv string
		amt int64
	}
	var allocs []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.inv, &a.amt); err != nil {
			rows.Close()
			return nsfReverseResult{}, fmt.Errorf("till: scan allocation: %w", err)
		}
		allocs = append(allocs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nsfReverseResult{}, err
	}
	for _, a := range allocs {
		if _, err := tx.Exec(ctx,
			`UPDATE invoices
			 SET balance_cents = balance_cents + $2,
			     status = CASE WHEN status = 'VOID' THEN status
			                   WHEN balance_cents + $2 >= total_cents THEN 'FINALIZED'
			                   ELSE 'PARTIALLY_PAID' END,
			     updated_at = now()
			 WHERE id = $1`, a.inv, a.amt); err != nil {
			return nsfReverseResult{}, fmt.Errorf("till: reopen invoice: %w", err)
		}
		res.ReversedCents += a.amt
		res.ReopenedInvoices++
	}
	if _, err := tx.Exec(ctx, `DELETE FROM payment_allocations WHERE payment_id = $1`, in.PaymentID); err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: reverse allocations: %w", err)
	}

	// NSF record, fee captured from policy AT bounce time (a later policy change never rewrites history).
	var nsfID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO payment_nsf (payment_id, customer_id, reason, fee_cents, fee_taxable, fee_account_code, recorded_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id::text`,
		in.PaymentID, nullStr(res.CustomerID), in.Reason, in.FeeCents, in.FeeTaxable, nullStr(in.FeeAccountCode), nullStr(in.ActorID),
	).Scan(&nsfID); err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: insert nsf: %w", err)
	}

	if res.CustomerID != "" {
		// AR after reopen (invoice-carried + existing non-invoice AR) → the subledger balance_after basis.
		var arAfter int64
		if err := tx.QueryRow(ctx,
			`SELECT
			   (SELECT COALESCE(SUM(balance_cents), 0) FROM invoices
			     WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents <> 0)
			 + (SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions
			     WHERE customer_id = $1 AND txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND source_type <> 'invoice')`,
			res.CustomerID).Scan(&arAfter); err != nil {
			return nsfReverseResult{}, fmt.Errorf("till: read AR after reopen: %w", err)
		}
		// Reversal-of-cash ledger row, recorded for the customer subledger history but EXCLUDED from the AR
		// aging/tie-out union (source_type='invoice'), because the reopened invoice balances already carry
		// the restored AR (no double count). This mirrors how the original negative PAYMENT row is excluded.
		if res.ReversedCents > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
				   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
				 VALUES ($1, 'NSF', $2, $3, 'invoice', $4, $5, $6)`,
				res.CustomerID, res.ReversedCents, arAfter, in.PaymentID, nullStr(res.BranchID),
				fmt.Sprintf("NSF reversal of payment %s (%d cents returned to AR)", in.PaymentID, res.ReversedCents)); err != nil {
				return nsfReverseResult{}, fmt.Errorf("till: insert reversal txn: %w", err)
			}
		}
		// Fee ADJUSTMENT row, COUNTED non-invoice AR (the customer now owes the NSF fee).
		if in.FeeCents > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
				   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
				 VALUES ($1, 'ADJUSTMENT', $2, $3, 'payment_nsf', $4, $5, 'NSF fee')`,
				res.CustomerID, in.FeeCents, arAfter+in.FeeCents, nsfID, nullStr(res.BranchID)); err != nil {
				return nsfReverseResult{}, fmt.Errorf("till: insert fee txn: %w", err)
			}
		}
		// Auto credit hold, one ACTIVE per customer (partial unique index closes the race).
		if in.AutoHold {
			reason := in.Reason
			if reason == "" {
				reason = "auto credit hold on bounced payment"
			}
			tag, err := tx.Exec(ctx,
				`INSERT INTO credit_holds (customer_id, source_payment_id, status, reason, placed_by)
				 VALUES ($1, $2, 'ACTIVE', $3, $4)
				 ON CONFLICT (customer_id) WHERE status = 'ACTIVE' DO NOTHING`,
				res.CustomerID, in.PaymentID, reason, nullStr(in.ActorID))
			if err != nil {
				return nsfReverseResult{}, fmt.Errorf("till: place credit hold: %w", err)
			}
			res.HoldPlaced = tag.RowsAffected() > 0
		}
	}

	// Emit payment.bounced + the fee journal facts in the SAME tx (transactional outbox → GL posts once).
	payload, err := json.Marshal(map[string]any{
		"payment_id":       in.PaymentID,
		"customer_id":      res.CustomerID,
		"branch_id":        res.BranchID,
		"method":           res.Method,
		"amount_cents":     res.AmountCents,
		"reversed_cents":   res.ReversedCents,
		"fee_cents":        in.FeeCents,
		"fee_taxable":      in.FeeTaxable,
		"fee_account_code": in.FeeAccountCode,
		"cleared":          clearedToBank, // true → original receipt had settled to bank 1000; false → still 1020
	})
	if err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: marshal payment.bounced: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload) VALUES ('payment.bounced', $1)`, payload); err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: emit payment.bounced: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: commit nsf: %w", err)
	}
	return res, nil
}

// ReleaseHold flips one ACTIVE hold to RELEASED and returns its customer id (for the audit record). A
// missing or already-released hold is a 404 (the release is a no-op the caller should see as not-found).
func (r *Repository) ReleaseHold(ctx context.Context, holdID, releasedBy, reason string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var customerID string
	err := r.pool.QueryRow(ctx,
		`UPDATE credit_holds
		 SET status = 'RELEASED', release_reason = $3, released_by = $2, released_at = now(), updated_at = now()
		 WHERE id = $1 AND status = 'ACTIVE'
		 RETURNING customer_id::text`, holdID, nullStr(releasedBy), reason,
	).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return "", clientErr(404, "till: no ACTIVE credit hold for id %s", holdID)
	}
	if err != nil {
		return "", fmt.Errorf("till: release hold: %w", err)
	}
	return customerID, nil
}

// ListCreditHolds returns holds filtered by status ("" = all), newest first.
func (r *Repository) ListCreditHolds(ctx context.Context, status string) ([]CreditHold, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT id::text, customer_id::text, COALESCE(source_payment_id::text, ''), status, reason,
	             release_reason, COALESCE(placed_by::text, ''), COALESCE(released_by::text, ''),
	             created_at, released_at
	      FROM credit_holds`
	var args []any
	if status != "" {
		q += ` WHERE status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("till: list credit holds: %w", err)
	}
	defer rows.Close()
	var out []CreditHold
	for rows.Next() {
		var h CreditHold
		if err := rows.Scan(&h.ID, &h.CustomerID, &h.SourcePaymentID, &h.Status, &h.Reason,
			&h.ReleaseReason, &h.PlacedBy, &h.ReleasedBy, &h.CreatedAt, &h.ReleasedAt); err != nil {
			return nil, fmt.Errorf("till: scan credit hold: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListBouncedPayments returns the bounced-payment queue (payments joined to their NSF record). gatewayRef
// "" = all; a non-empty value is an EXACT gateway_ref search (the AR-console lookup).
func (r *Repository) ListBouncedPayments(ctx context.Context, gatewayRef string) ([]BouncedPayment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `SELECT p.id::text, p.payment_no::text, COALESCE(p.customer_id::text, ''), p.branch_id::text,
	             p.method, p.amount_cents, COALESCE(p.gateway_ref, ''), n.reason, n.fee_cents, n.fee_taxable,
	             n.created_at
	      FROM payments p
	      JOIN payment_nsf n ON n.payment_id = p.id
	      WHERE p.status = 'BOUNCED'`
	var args []any
	if gatewayRef != "" {
		q += ` AND p.gateway_ref = $1`
		args = append(args, gatewayRef)
	}
	q += ` ORDER BY n.created_at DESC`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("till: list bounced payments: %w", err)
	}
	defer rows.Close()
	var out []BouncedPayment
	for rows.Next() {
		var b BouncedPayment
		if err := rows.Scan(&b.PaymentID, &b.PaymentNo, &b.CustomerID, &b.BranchID, &b.Method,
			&b.AmountCents, &b.GatewayRef, &b.Reason, &b.FeeCents, &b.FeeTaxable, &b.BouncedAt); err != nil {
			return nil, fmt.Errorf("till: scan bounced payment: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpdateNSFPolicy upserts the ORG-DEFAULT (branch_id IS NULL) NSF policy row. Because a UNIQUE on a
// nullable column treats NULLs as distinct, ON CONFLICT cannot target the NULL row, so this updates the
// existing default in place and inserts one only when none exists (single-writer, so the check is safe).
func (r *Repository) UpdateNSFPolicy(ctx context.Context, in NSFPolicy) error {
	if r.pool == nil {
		return errNoDB
	}
	if in.FeeAccountCode == "" {
		in.FeeAccountCode = "4200"
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE nsf_policy
		 SET fee_cents = $1, fee_taxable = $2, fee_account_code = $3, auto_hold = $4, notes = $5, updated_at = now()
		 WHERE branch_id IS NULL`,
		in.FeeCents, in.FeeTaxable, in.FeeAccountCode, in.AutoHold, in.Notes)
	if err != nil {
		return fmt.Errorf("till: update nsf policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO nsf_policy (branch_id, fee_cents, fee_taxable, fee_account_code, auto_hold, notes)
			 VALUES (NULL, $1, $2, $3, $4, $5)`,
			in.FeeCents, in.FeeTaxable, in.FeeAccountCode, in.AutoHold, in.Notes); err != nil {
			return fmt.Errorf("till: insert nsf policy: %w", err)
		}
	}
	return nil
}

// RecordPaymentAttempt persists one declined/errored gateway attempt (DX-14). Idempotent on the key: a
// retried attempt with the same idempotency_key is a no-op (ux_payment_attempts_key).
func (r *Repository) RecordPaymentAttempt(ctx context.Context, in PaymentAttempt) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO payment_attempts (idempotency_key, customer_id, amount_cents, method, processor, result, detail, branch_id, attempted_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING`,
		in.IdempotencyKey, nullStr(in.CustomerID), in.AmountCents, in.Method, in.Processor, in.Result, in.Detail, nullStr(in.BranchID), nullStr(in.AttemptedBy))
	if err != nil {
		return fmt.Errorf("till: record payment attempt: %w", err)
	}
	return nil
}
