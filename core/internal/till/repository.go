package till

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

var errNoDB = errors.New("till: database not configured")

// errNoInstant is the refusal a store hands back when a row arrives with no instant. The movement and
// the bounce write the service clock's own value into their created_at (one clock rules the
// transaction, FW1-13b), so a caller that hands nothing down would write the zero instant where the
// column default used to stand: a day that is not a day, in every read of the drawer. The refusal
// names the missing instant in words, before any write, so the mistake is loud instead of silent.
func errNoInstant(what string) error {
	return fmt.Errorf("till: a %s needs the service clock's instant; a zero stamp is refused", what)
}

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

// actorUUID returns the actor id when it has a UUID's shape and nil otherwise, so an optional uuid
// column is never handed a string the database cannot cast. A malformed actor id must not refuse the
// money move: the attribution is kept in the NSF record and the audit trail either way, and the
// reversal row's own facts (the application it names, the negative, the instant) are the money.
func actorUUID(s string) any {
	if s == "" || !uuidLike(s) {
		return nil
	}
	return s
}

// nullTime maps the zero instant to SQL NULL, so a caller that named no instant leaves the column
// default standing (the column default is the transaction's own timestamp) instead of writing the
// zero instant, where every date read of the row would name a day that is not a day.
func nullTime(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at
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

// GetSessionTx reads the session row FOR SHARE inside the caller's transaction (SF-2j round 2). The caller checks
// the session is OPEN on this read, and the share lock holds until it commits, so a count (SetCounted's UPDATE takes
// FOR NO KEY UPDATE) queues behind the tender or the cash refund that read OPEN, or finds it committed: nothing is
// written to a session after its count. FOR SHARE and not FOR NO KEY UPDATE because tenders and refunds on one
// session need not wait on each other (shares do not conflict), only on the count and on a movement, which takes
// FOR NO KEY UPDATE. The session row is the first row either transaction takes in this module, so no cycle results.
func (r *Repository) GetSessionTx(ctx context.Context, tx pgx.Tx, id string) (TillSession, error) {
	return scanSession(tx.QueryRow(ctx, `SELECT `+sessionCols+` FROM till_sessions WHERE id = $1 FOR SHARE`, id))
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
// The row also stamps the 0213 sync legs (the payments feed seq, the org off the branch's
// location): a counter-taken payment FOR a customer's account is that payer's history, and a
// payment path no pull can see is a feed that lies by omission, the posture the payments
// module's own settle carries on the same legs.
//
// The row's created_at is the instant the CALLER names (one clock rules the transaction, FW1-13b):
// the POS return's rail carries its own instant, so the row and the drawer movement beside it carry
// one instant. No instant named means the column default stands, exactly as before.
func (r *Repository) InsertPayment(ctx context.Context, tx pgx.Tx, p PaymentRow) (string, string, bool, error) {
	const q = `
		WITH pseq AS (
			INSERT INTO scope_sequences (scope) VALUES ('payments')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value
		)
		INSERT INTO payments
		  (payment_no, customer_id, method, amount_cents, status, branch_id, till_session_id,
		   gateway_ref, card_type, received_by, idempotency_key, offline_captured, signature_ref,
		   org_id, seq, created_at)
		VALUES
		  ('PMT-' || lpad(nextval('payment_no_seq')::text, 6, '0'),
		   $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
		   (SELECT org_id FROM locations WHERE id = $5::uuid),
		   (SELECT next_value FROM pseq),
		   COALESCE($13::timestamptz, now()))
		ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING
		RETURNING id::text, payment_no::text`
	var id, paymentNo string
	err := tx.QueryRow(ctx, q,
		nullStr(p.CustomerID), p.Method, p.AmountCents, p.Status, p.BranchID, nullStr(p.TillSessionID),
		p.GatewayRef, nullStr(p.CardType), nullStr(p.ReceivedBy), p.IdempotencyKey, p.OfflineCaptured, p.SignatureRef,
		nullTime(p.CreatedAt),
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
	// The conflict target carries the kind (0450): the primary key is (payment_id, invoice_id, kind), so
	// the upsert adds to the APPLICATION row and never to a reversal row, which is written once and is
	// never added to. The WHERE refuses a pair a reversal already stands on (0450, W2-6A fix 1): the
	// update never runs, so the pair is refused by the code before the trigger, and a zero row count
	// is reported here in words.
	res, err := tx.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, allocated_by) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (payment_id, invoice_id, kind) DO UPDATE SET amount_cents = payment_allocations.amount_cents + EXCLUDED.amount_cents
		 WHERE NOT EXISTS (SELECT 1 FROM payment_allocations r
		                    WHERE r.payment_id = payment_allocations.payment_id
		                      AND r.invoice_id = payment_allocations.invoice_id
		                      AND r.kind = 'REVERSAL')`,
		paymentID, invoiceID, amountCents, nullStr(allocatedBy))
	if err != nil {
		return fmt.Errorf("till: insert allocation: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("till: insert allocation: the payment and invoice pair is reversed, the application cannot be added to")
	}
	return nil
}

func (r *Repository) AllocatedTotal(ctx context.Context, tx pgx.Tx, paymentID string) (int64, error) {
	// The sum of every row the payment carries, so a reversal row (0450) takes its application back
	// and the figure is the money still applied. Before the kind, a bounced payment's rows were gone
	// and the figure was zero; it is zero now for the same reason and by the same arithmetic.
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

// InsertMovement writes a drawer movement against an OPEN session, serialised with the count (SF-2j). It takes the
// session row FOR NO KEY UPDATE in its own transaction, re-reads the status UNDER that lock, and only then inserts:
// the service's earlier OPEN read is a courtesy, this is the check. SetCounted's UPDATE of the same row takes the
// same lock strength, so a count and a movement serialise (whichever holds the row first, the other sees the
// result): a movement can no longer land on a session counted between a plain read and the insert.
//
// FOR NO KEY UPDATE and not FOR UPDATE: the movement's foreign key to the session takes FOR KEY SHARE on the
// session row, which NO KEY UPDATE does not conflict with, and no key column of till_sessions changes here or in
// the count (its only unique index besides the id is partial). The lock order is the till session row before any
// movement insert or count (LOCK-ORDER.md).
func (r *Repository) InsertMovement(ctx context.Context, m MovementRow) error {
	if r.pool == nil {
		return errNoDB
	}
	if m.CreatedAt.IsZero() {
		return errNoInstant("movement")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("till: begin movement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, branchID string
	if err := tx.QueryRow(ctx, `SELECT status, branch_id::text FROM till_sessions WHERE id = $1 FOR NO KEY UPDATE`, m.TillSessionID).Scan(&status, &branchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return fmt.Errorf("till: lock the session for a movement: %w", err)
	}
	if status != StatusOpen {
		return clientErr(409, "till: movements require an OPEN session")
	}
	var movementID string
	var movementAt time.Time
	if err := tx.QueryRow(ctx, insertMovementSQL+` RETURNING id, created_at`,
		m.TillSessionID, m.MovementType, m.Method, m.AmountCents, m.Reason, m.Reference, nullStr(m.CreatedBy), m.CreatedAt).Scan(&movementID, &movementAt); err != nil {
		return fmt.Errorf("till: insert movement: %w", err)
	}
	// The deposit slip's rows and the DEPOSITED marks ride THIS transaction (FROM-PRINCIPAL 120): the
	// link rows first, then the payment rows locked ascending (the one money lock order; the session
	// row this movement already holds is not in it), then the marks. A payment the marks could not
	// move fails the movement, so the slip and the books move together. The service has validated the
	// slip; the locked UPDATE re-checks the status under the row locks, which is how a concurrent
	// bounce or reversal is seen.
	if len(m.PaymentIDs) > 0 {
		for _, pid := range m.PaymentIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO till_lodgement_payments (till_movement_id, payment_id, created_at) VALUES ($1::uuid, $2::uuid, $3)`,
				movementID, pid, m.CreatedAt); err != nil {
				return fmt.Errorf("till: insert lodgement slip row: %w", err)
			}
		}
		if err := payments.LockPaymentRowsTx(ctx, tx, m.PaymentIDs); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE payments SET status = 'DEPOSITED', updated_at = now()
			  WHERE id = ANY($1::uuid[]) AND status = 'RECEIVED' AND reversed_at IS NULL`,
			m.PaymentIDs)
		if err != nil {
			return fmt.Errorf("till: mark the slip's payments deposited: %w", err)
		}
		if tag.RowsAffected() != int64(len(m.PaymentIDs)) {
			return clientErr(409, "till: the slip's money moved while it was recorded; record the slip again")
		}
	}
	// Transactional outbox, the session close's own rail (till.session.completed, 0032): a paid in, a
	// paid out or a lodgement carries its GL facts in the SAME tx as the row, so the journal posts
	// exactly once. The lodgement's journal is the ruled one (FROM-PRINCIPAL 117): DR 1000 / CR 1020 at
	// lodgement, and the bank match of a lodgement that carries it posts nothing, so the pair never
	// books twice. A DROP deliberately emits nothing: the cash is still the dealer's undeposited cash,
	// only out of the drawer. The payload carries the drawer's own reason words, because every line of
	// the journal must carry them (FROM-PRINCIPAL 132).
	if m.MovementType == MovementPaidIn || m.MovementType == MovementPaidOut || m.MovementType == MovementLodgement {
		payload, err := json.Marshal(map[string]any{
			"movement_id":   movementID,
			"session_id":    m.TillSessionID,
			"branch_id":     branchID,
			"movement_type": m.MovementType,
			"method":        m.Method,
			"amount_cents":  m.AmountCents,
			"occurred_at":   movementAt,
			"reason":        m.Reason,
		})
		if err != nil {
			return fmt.Errorf("till: marshal movement posted: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO events_outbox (topic, payload) VALUES ('till.movement.posted', $1)`, payload); err != nil {
			return fmt.Errorf("till: emit movement posted: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// insertMovementSQL is the movement INSERT. created_at is written from the service clock the
// caller passes on the row (one clock rules the transaction, FW1-13b), never the column default:
// the outbox payload's occurred_at is this row's created_at, and both journal builders date from
// it. The pool path (InsertMovement) appends RETURNING id for the outbox payload's own key; the
// in-caller-tx path (InsertMovementTx, the POS return's drawer movement) shares the statement
// without it and never emits: the return event's journal already carries the drawer credit.
const insertMovementSQL = `INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// PaymentsForSlip reads the named payments for a lodgement's deposit slip validation. The read is
// fenced to the session and the method: a name that is not on this drawer as this method reads as
// absent, and the service refuses the slip for it.
func (r *Repository) PaymentsForSlip(ctx context.Context, sessionID, method string, ids []string) ([]SlipPayment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, amount_cents, status, reversed_at IS NOT NULL
		   FROM payments
		  WHERE till_session_id = $1::uuid AND method = $2 AND id = ANY($3::uuid[])`, sessionID, method, ids)
	if err != nil {
		return nil, fmt.Errorf("till: slip payments: %w", err)
	}
	defer rows.Close()
	var out []SlipPayment
	for rows.Next() {
		var p SlipPayment
		if err := rows.Scan(&p.ID, &p.AmountCents, &p.Status, &p.Reversed); err != nil {
			return nil, fmt.Errorf("till: scan slip payment: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SlipCandidates reads the drawer's unlodged payments of a method: the RECEIVED, unreversed payments on
// this session that no lodgement has named yet (0441's link rows), so the drawer can tick the ones the
// lodgement carries. The order is the drawer's own: the oldest first.
func (r *Repository) SlipCandidates(ctx context.Context, sessionID, method string) ([]SlipCandidate, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT p.id::text, p.payment_no::text, p.amount_cents, p.method,
		        COALESCE(NULLIF(p.gateway_ref, ''), p.payment_no::text)
		   FROM payments p
		  WHERE p.till_session_id = $1::uuid AND p.method = $2
		    AND p.status = 'RECEIVED' AND p.reversed_at IS NULL
		    AND NOT EXISTS (SELECT 1 FROM till_lodgement_payments lp WHERE lp.payment_id = p.id)
		  ORDER BY p.received_at, p.id`, sessionID, method)
	if err != nil {
		return nil, fmt.Errorf("till: slip candidates: %w", err)
	}
	defer rows.Close()
	var out []SlipCandidate
	for rows.Next() {
		var c SlipCandidate
		if err := rows.Scan(&c.PaymentID, &c.PaymentNo, &c.AmountCents, &c.Method, &c.Reference); err != nil {
			return nil, fmt.Errorf("till: scan slip candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertMovementTx is InsertMovement inside the CALLER'S tx (the POS return path, the PAID_OUT
// cash-refund movement must commit or roll back with the return it belongs to).
func (r *Repository) InsertMovementTx(ctx context.Context, tx pgx.Tx, m MovementRow) error {
	if m.CreatedAt.IsZero() {
		return errNoInstant("movement")
	}
	if _, err := tx.Exec(ctx, insertMovementSQL,
		m.TillSessionID, m.MovementType, m.Method, m.AmountCents, m.Reason, m.Reference, nullStr(m.CreatedBy), m.CreatedAt); err != nil {
		return fmt.Errorf("till: insert movement: %w", err)
	}
	return nil
}

// ExpectedTenders reads the drawer's received money per tender method: the RECEIVED payments, plus the
// payments a lodgement's deposit slip carried.
//
// A slip-lodgement's payments are DEPOSITED now (the bank's fact: they are in the deposit, and a bounce
// of one credits 1000). But the DRAWER received them and the lodgement is the drawer's own statement that
// it banked them, so they stay in the drawer's in-term and the lodgement takes them out of it, ONCE.
// Leaving them out of the in-term (the DEPOSITED status alone) takes the same money out twice - the
// slip's mark and the lodgement - and the drawer reads short by the whole slip.
func (r *Repository) ExpectedTenders(ctx context.Context, sessionID string) (map[string]int64, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT method, COALESCE(SUM(amount_cents), 0) FROM payments
		 WHERE till_session_id = $1
		   AND (status = 'RECEIVED' OR EXISTS (
		         SELECT 1 FROM till_lodgement_payments lp WHERE lp.payment_id = payments.id))
		 GROUP BY method`, sessionID)
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

// MovementTotalsByMethod returns the session's drawer movement sums per tender method, following
// the movement table's own vocabulary (0028): paid in, paid out and dropped, lodgements, per
// method. A method with no movements is absent; callers read a missing method as zero.
func (r *Repository) MovementTotalsByMethod(ctx context.Context, sessionID string) (map[string]MovementTotals, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT method,
		   COALESCE(SUM(amount_cents) FILTER (WHERE movement_type = 'PAID_IN'), 0),
		   COALESCE(SUM(amount_cents) FILTER (WHERE movement_type IN ('PAID_OUT', 'DROP')), 0),
		   COALESCE(SUM(amount_cents) FILTER (WHERE movement_type = 'LODGEMENT'), 0)
		 FROM till_movements WHERE till_session_id = $1
		 GROUP BY method`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("till: movement totals: %w", err)
	}
	defer rows.Close()
	out := map[string]MovementTotals{}
	for rows.Next() {
		var method string
		var mt MovementTotals
		if err := rows.Scan(&method, &mt.PaidIn, &mt.PaidOut, &mt.Lodgement); err != nil {
			return nil, fmt.Errorf("till: scan movement totals: %w", err)
		}
		out[method] = mt
	}
	return out, rows.Err()
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

// SetCounted flips an OPEN session to COUNTED. The guarded UPDATE takes the session row's lock (FOR NO KEY UPDATE
// strength: no key column is set), the same lock a movement's insert takes first (InsertMovement), so the two
// serialise (SF-2j).
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

	// The session's own business day: the day the drawer was OPENED, which is the day the
	// over/short belongs to. A count taken after midnight belongs to the trading day the
	// drawer was opened for, not to the day the count happened, so the date rides the event
	// and the GL files the over/short journal in the session's business day.
	var openedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT opened_at FROM till_sessions WHERE id = $1`, sessionID).Scan(&openedAt); err != nil {
		return fmt.Errorf("till: read the session's business day: %w", err)
	}

	// Transactional outbox: emit till.session.completed (over/short facts) in the SAME tx as the close so
	// the GL over/short journal (6350 / 1020) posts exactly once (W5/A-05 AC9). FW1-01b: the event carries
	// the session's business day (occurred_at), so the journal is dated by the session and not by the day
	// the consumer happens to read the outbox.
	payload, err := json.Marshal(map[string]any{
		"session_id":       sessionID,
		"branch_id":        branchID,
		"over_short_cents": overShortCents,
		"counted_cents":    countedCashCents,
		"occurred_at":      openedAt,
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
func (r *Repository) PaymentNSFContext(ctx context.Context, paymentID string) (customerID, branchID, status, method string, amountCents int64, reversed, found bool, err error) {
	if r.pool == nil {
		return "", "", "", "", 0, false, false, errNoDB
	}
	err = r.pool.QueryRow(ctx,
		`SELECT COALESCE(customer_id::text, ''), branch_id::text, status, method, amount_cents, reversed_at IS NOT NULL
		 FROM payments WHERE id = $1`, paymentID,
	).Scan(&customerID, &branchID, &status, &method, &amountCents, &reversed)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return "", "", "", "", 0, false, false, nil
	}
	if err != nil {
		return "", "", "", "", 0, false, false, fmt.Errorf("till: payment nsf context: %w", err)
	}
	return customerID, branchID, status, method, amountCents, reversed, true, nil
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
	if in.At.IsZero() {
		return nsfReverseResult{}, errNoInstant("bounce")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: begin nsf: %w", err)
	}
	defer tx.Rollback(ctx)

	var res nsfReverseResult
	var status string
	var reversed bool
	var nsfAllocJSON []byte
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(customer_id::text, ''), branch_id::text, method, status, amount_cents, reversed_at IS NOT NULL,
		        COALESCE(metadata->'nsf_fee_allocations', '{}'::jsonb)
		 FROM payments WHERE id = $1 FOR UPDATE`, in.PaymentID,
	).Scan(&res.CustomerID, &res.BranchID, &res.Method, &status, &res.AmountCents, &reversed, &nsfAllocJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nsfReverseResult{}, errNotFound
	}
	if err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: lock payment: %w", err)
	}
	if status == "BOUNCED" {
		return nsfReverseResult{}, errAlreadyBounced
	}
	// A reversal and a bounce exclude each other (SF-2d round 7): decided here, in the locked read, so a reversal
	// that committed first is seen and one that has not yet taken the row waits behind this transaction.
	if reversed {
		return nsfReverseResult{}, errPaymentReversed
	}
	// A bounce undoes a live receipt: a reversing entry, a voided desk receipt (FW1-06b) or a refund row has
	// nothing to give back, and reinstating its invoices would post money that never arrived.
	if status != "RECEIVED" && status != "DEPOSITED" {
		return nsfReverseResult{}, errPaymentNotBounceable
	}
	// Capture whether the receipt had already settled to the bank (DEPOSITED) BEFORE we flip it to BOUNCED.
	// The GL payment.bounced consumer needs this to reverse the right leg: DEPOSITED → bank 1000, else the
	// still-undeposited 1020. It cannot be recovered from PaymentReader after the flip, so it rides the event.
	clearedToBank := status == "DEPOSITED"

	// updated_at is bookkeeping only: nothing reads it back as a money date (the AR aging reads customer_transactions.occurred_at, dated with the bounce's own instant), so this row keeps the database's clock while every money date in this transaction takes the service clock's instant.
	if _, err := tx.Exec(ctx, `UPDATE payments SET status = 'BOUNCED', updated_at = now() WHERE id = $1`, in.PaymentID); err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: flip bounced: %w", err)
	}

	// Reverse every allocation + reopen its invoice (restore the balance the payment had cleared).
	// Only the APPLICATION rows: a reversal row (0450) is the undo of an application, and a payment
	// that has not been bounced carries none.
	rows, err := tx.Query(ctx,
		`SELECT pa.id::text, pa.invoice_id::text, pa.amount_cents FROM payment_allocations pa
		  WHERE pa.payment_id = $1 AND pa.kind = 'APPLICATION'`, in.PaymentID)
	if err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: read allocations: %w", err)
	}
	type alloc struct {
		id  string
		inv string
		amt int64
	}
	var allocs []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.id, &a.inv, &a.amt); err != nil {
			rows.Close()
			return nsfReverseResult{}, fmt.Errorf("till: scan allocation: %w", err)
		}
		allocs = append(allocs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nsfReverseResult{}, err
	}
	// The payments counter row comes next, taken WITHOUT advancing it, before any invoice row. The
	// settle draws this same row first, so the two writers can never interleave their invoice rows:
	// the bounce walks its rows in ascending id order and the settle locks the order the caller
	// named, and two writers taking the same rows in opposite orders is a deadlock waiting for its
	// interleaving. MarkNSF writes no payments feed row, so for the bounce this lock is purely the
	// serialising one. It is the payments module's own non advancing upsert (the till package can
	// import it; payments imports nothing here), run on this transaction.
	if err := payments.NewRepository(nil).LockPaymentsSequenceTx(ctx, tx); err != nil {
		return nsfReverseResult{}, fmt.Errorf("till: lock payments sequence: %w", err)
	}
	// The re-open's UPDATE draws the invoices sequence row while it computes the new row, so the
	// invoice rows must already be held before that statement runs: a settle holding the invoice and
	// waiting on the counter otherwise meets the bounce head on and one side is aborted with a 40P01.
	// The rows are taken in ascending id order, the one order every multi-invoice writer uses (the
	// money lock order recorded in payments/LOCK-ORDER.md), and FOR NO KEY UPDATE, not FOR UPDATE:
	// the bounce writes no key column, and a FOR UPDATE would conflict with the FOR KEY SHARE a POS
	// return's foreign key check holds on the returned sale's invoice while the return waits on this
	// transaction's payments counter, closing the same cycle the settle's lock once did.
	lockIDs := make([]string, 0, len(allocs))
	for _, a := range allocs {
		lockIDs = append(lockIDs, a.inv)
	}
	slices.Sort(lockIDs)
	for _, id := range lockIDs {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1 FOR NO KEY UPDATE`, id); err != nil {
			return nsfReverseResult{}, fmt.Errorf("till: lock invoice %s: %w", id, err)
		}
	}
	nsfAllocs := map[string]int64{}
	_ = json.Unmarshal(nsfAllocJSON, &nsfAllocs)
	nsfFeeIDs := make([]string, 0, len(nsfAllocs))
	for id, amt := range nsfAllocs {
		if amt > 0 {
			nsfFeeIDs = append(nsfFeeIDs, id)
		}
	}
	slices.Sort(nsfFeeIDs)
	for _, id := range nsfFeeIDs {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM payment_nsf WHERE id = $1::uuid FOR NO KEY UPDATE`, id); err != nil {
			return nsfReverseResult{}, fmt.Errorf("till: lock nsf fee %s: %w", id, err)
		}
	}
	var invReversedCents int64
	for _, a := range allocs {
		// The re-open stamps the invoices feed seq beside the move
		// (the settle's own posture in the payments module): a bounced
		// cheque re-opening the payer's invoice is the change the
		// payer's device must see.
		if _, err := tx.Exec(ctx,
			`WITH iseq AS (
				INSERT INTO scope_sequences (scope) VALUES ('invoices')
				ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
				RETURNING next_value
			)
			UPDATE invoices
			 SET balance_cents = balance_cents + $2,
			     status = CASE WHEN status = 'VOID' THEN status
			                   WHEN balance_cents + $2 >= total_cents THEN 'FINALIZED'
			                   ELSE 'PARTIALLY_PAID' END,
			     updated_at = now(),
			     seq = (SELECT next_value FROM iseq)
			 WHERE id = $1`, a.inv, a.amt); err != nil {
			return nsfReverseResult{}, fmt.Errorf("till: reopen invoice: %w", err)
		}
		invReversedCents += a.amt
		res.ReversedCents += a.amt
		res.ReopenedInvoices++
	}
	for _, id := range nsfFeeIDs {
		res.ReversedCents += nsfAllocs[id]
	}
	// The application stays and the undo is written beside it (0450): the same payment, the same
	// invoice, the exact negative of the application, dated at the bounce's own instant, naming the
	// application it reverses. The bounce's figures do not move (the invoice is re-opened above and
	// the net of the two rows is zero), but "what did this cheque use to pay?" is answerable from the
	// rows themselves and not only from an audit trail, and an as-of aging rebuilt from the dated acts
	// can see the cheque was good before the bounce and owed after it.
	for _, a := range allocs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO payment_allocations
			   (payment_id, invoice_id, amount_cents, kind, reverses_allocation_id, allocated_by, created_at)
			 VALUES ($1::uuid, $2::uuid, $3, 'REVERSAL', $4::uuid, $5::uuid, $6)`,
			in.PaymentID, a.inv, -a.amt, a.id, actorUUID(in.ActorID), in.At); err != nil {
			return nsfReverseResult{}, fmt.Errorf("till: write the reversal row: %w", err)
		}
	}

	// NSF record, fee captured from policy AT bounce time (a later policy change never rewrites history).
	// The record's created_at is the bounce's own instant, which rides the event so the GL books the
	// reversal in the month the bounce happened (FW1-01b). Written from the service clock the caller
	// passed (one clock rules the transaction, FW1-13b), never the column default.
	var nsfID string
	var nsfAt time.Time
	if err := tx.QueryRow(ctx,
		`INSERT INTO payment_nsf (payment_id, customer_id, reason, fee_cents, fee_taxable, fee_account_code, recorded_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id::text, created_at`,
		in.PaymentID, nullStr(res.CustomerID), in.Reason, in.FeeCents, in.FeeTaxable, nullStr(in.FeeAccountCode), nullStr(in.ActorID), in.At,
	).Scan(&nsfID, &nsfAt); err != nil {
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
		// The row's occurred_at is the bounce's own instant (one clock rules the transaction, FW1-13b), never
		// the column default: the subledger and GL 1100 must date one bounce in the same month, or an AR
		// aging as of the bounce's month end cannot tie with GL 1100.
		if invReversedCents > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
				   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
				 VALUES ($1, 'NSF', $2, $3, 'invoice', $4, $5, $6, $7)`,
				res.CustomerID, invReversedCents, arAfter, in.PaymentID, nullStr(res.BranchID),
				fmt.Sprintf("NSF reversal of payment %s (%d cents returned to AR)", in.PaymentID, invReversedCents), in.At); err != nil {
				return nsfReverseResult{}, fmt.Errorf("till: insert reversal txn: %w", err)
			}
		}
		for _, origNSFID := range nsfFeeIDs {
			feeAmt := nsfAllocs[origNSFID]
			arAfter += feeAmt
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
				   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
				 VALUES ($1, 'ADJUSTMENT', $2, $3, 'payment_nsf', $4, $5, $6, $7)`,
				res.CustomerID, feeAmt, arAfter, origNSFID, nullStr(res.BranchID),
				"NSF reversal: "+in.Reason, in.At); err != nil {
				return nsfReverseResult{}, fmt.Errorf("till: insert nsf fee reversal txn: %w", err)
			}
		}
		// Fee ADJUSTMENT row, COUNTED non-invoice AR (the customer now owes the NSF fee).
		if in.FeeCents > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO customer_transactions
				   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
				 VALUES ($1, 'ADJUSTMENT', $2, $3, 'payment_nsf', $4, $5, 'NSF fee', $6)`,
				res.CustomerID, in.FeeCents, arAfter+in.FeeCents, nsfID, nullStr(res.BranchID), in.At); err != nil {
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
		"occurred_at":      nsfAt,         // FW1-01b: the bounce's own instant, not the processing day
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
		// A well formed branch id that is no location: the client's error, not a 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "payment_attempts_branch_id_fkey" {
			return unprocessable("till: branch_id must be a branch id")
		}
		return fmt.Errorf("till: record payment attempt: %w", err)
	}
	return nil
}

// UncompletedSessionsOpenedBefore answers how many till sessions were opened before the
// instant and are NOT YET COMPLETED: the GL close pre flight's "no till drawer opened in
// or before the month is not yet completed" check (FW1-09f). The instant is the month's end
// (the first instant of the next month in the dealer's zone).
//
// A session COUNTS when it was opened before the instant and the till has not completed it:
// still OPEN, or COUNTED (counted but not completed). A COMPLETED session never counts,
// whenever it was completed: the completion posts the over or short into the business day
// the drawer was OPENED for, so the month's books are whole once the drawer is done. And
// CLOSED is final, so a session that counted would block its month for ever with no product
// action left to clear it. A session opened after the instant does not count either: it did
// not exist at that month's end.
//
// The read before this one asked which sessions stood open AT the instant, so a drawer opened
// on the month's last evening and completed after midnight counted for ever. The read before
// THAT counted only the sessions opened inside the month window, so a drawer opened in an
// earlier month and never closed passed the close of every later month.
// A nil pool answers zero (scaffold/offline mode).
func (r *Repository) UncompletedSessionsOpenedBefore(ctx context.Context, before time.Time) (int, error) {
	if r == nil || r.pool == nil {
		return 0, nil
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM till_sessions
		  WHERE opened_at < $1
		    AND status <> 'CLOSED'`,
		before).Scan(&n); err != nil {
		return 0, fmt.Errorf("till: uncompleted sessions opened before: %w", err)
	}
	return n, nil
}
