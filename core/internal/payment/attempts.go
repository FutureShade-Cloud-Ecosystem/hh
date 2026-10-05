package payment

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// The card charge attempt store (migration 0361; design sections 3.2 and 5.2). One row per till leg,
// keyed by the leg's idempotency key, written and committed on the pool BEFORE the processor is
// called. This file holds the row's states and their guarded moves and nothing else: no processor
// call, no config, no vault. A transition method returns the row it moved and true, or false and no
// row when the guard matched nothing (the row is not in the status the move expects); an error is
// reserved for a failed statement or an invalid argument.
//
// Every transition is ONE guarded statement, UPDATE ... WHERE id = $1 AND status = <expected>
// RETURNING. That row is the only arbiter between "the sale owns this capture" and "the compensator
// owns it": exactly one move out of CAPTURED can win, BindTx (BOUND) or BeginVoiding (VOIDING), and
// Postgres makes the loser wait on the row lock and then re-evaluate the status predicate.
//
// Connections (decision log row 786): every method makes one statement or one transaction on ONE
// pool connection and asks for no second one. BindTx rides the caller's transaction and never
// touches the pool.

// Statuses of a card charge attempt.
const (
	AttemptCharging     = "CHARGING"
	AttemptCaptured     = "CAPTURED"
	AttemptDeclined     = "DECLINED"
	AttemptUnknown      = "UNKNOWN"
	AttemptBound        = "BOUND"
	AttemptVoiding      = "VOIDING"
	AttemptVoided       = "VOIDED"
	AttemptVoidUnknown  = "VOID_UNKNOWN"
	AttemptVoidDeclined = "VOID_DECLINED"
	AttemptResolved     = "RESOLVED"
)

// Reversal kinds (how a captured charge was undone) and manager resolutions.
const (
	ReversalVoid   = "VOID"
	ReversalRefund = "REFUND"

	ResolutionNotCharged          = "NOT_CHARGED"
	ResolutionReversedAtProcessor = "REVERSED_AT_PROCESSOR"
)

var (
	// ErrTillUnresolved is the till guard's refusal: the same till session holds an unresolved
	// attempt for the same amount under a different sale key.
	ErrTillUnresolved = errors.New("payment: the till session holds an unresolved card attempt for this amount")
	// ErrResolveInvalid is a resolve with an unknown resolution, or a missing note or resolver.
	ErrResolveInvalid = errors.New("payment: resolve needs a known resolution, a note and a resolver")
	// ErrListUnbounded is a list with neither the open filter nor a date range.
	ErrListUnbounded = errors.New("payment: attempt list needs the open filter or a date range")

	errAttemptsNoDB = errors.New("payment: database unavailable")
	errBadClaim     = errors.New("payment: invalid attempt claim (leg key, sale key, branch, till session, positive amount and leg seq required)")
	errBadStamp     = errors.New("payment: invalid attempt stamp (an id and the processor ref or reversal it records are required)")
	errBadBind      = errors.New("payment: BindTx needs the sale's transaction, the attempt id and the payment id")
)

// unresolvedForGuard are the statuses that block a new claim for the same till session and amount:
// money may be held, or an outcome is not known. It matches idx_cca_till_guard's predicate.
const unresolvedForGuard = `'CHARGING','CAPTURED','UNKNOWN','VOIDING','VOID_UNKNOWN','VOID_DECLINED'`

// needsAPerson are the statuses a manager has to close: the answer, or the reversal's answer, was
// lost or refused. The in-flight statuses (CHARGING, CAPTURED, VOIDING) belong to the sweep until
// they time out into one of these.
const needsAPerson = `'UNKNOWN','VOID_UNKNOWN','VOID_DECLINED'`

// maxLastErrorRunes caps last_error: a diagnostic, not a transcript.
const maxLastErrorRunes = 300

// listLimit bounds a manager's list.
const listLimit = 500

// Attempt is one card_charge_attempts row. Optional uuid columns read as "" when null.
type Attempt struct {
	ID, LegKey, SaleKey                     string
	LegSeq                                  int
	AmountCents                             int64
	BranchID, TillSessionID, CustomerID     string
	CardType, Processor, Status             string
	ProcessorRef, ReversalKind, ReversalRef string
	RespCode, LastError, PaymentID          string
	Resolution, ResolutionNote, ResolvedBy  string
	CreatedBy                               string
	CreatedAt, UpdatedAt                    time.Time
}

// ClaimInput is what a claim records before the processor is called.
type ClaimInput struct {
	LegKey, SaleKey                     string
	LegSeq                              int
	AmountCents                         int64
	BranchID, TillSessionID, CustomerID string
	CardType, Processor, CreatedBy      string
}

// ListQuery selects the attempts a manager reads. OpenOnly selects the ones that need a person.
// A From and or To adds every attempt created in that range that carries a processor ref and is not
// BOUND (a trans_id with no sale behind it). With neither, the list is refused.
type ListQuery struct {
	OpenOnly bool
	From, To time.Time
}

// attemptQuerier is the part of pgxpool.Pool and pgx.Tx the store reads and writes through.
type attemptQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const attemptColumns = `id::text, leg_key, sale_key::text, leg_seq, amount_cents, branch_id::text, till_session_id::text,
  COALESCE(customer_id::text, ''), card_type, processor, status, processor_ref, reversal_kind, reversal_ref,
  resp_code, last_error, COALESCE(payment_id::text, ''), resolution, resolution_note,
  COALESCE(resolved_by::text, ''), COALESCE(created_by::text, ''), created_at, updated_at`

func scanAttempt(row pgx.Row) (Attempt, error) {
	var a Attempt
	err := row.Scan(&a.ID, &a.LegKey, &a.SaleKey, &a.LegSeq, &a.AmountCents, &a.BranchID, &a.TillSessionID,
		&a.CustomerID, &a.CardType, &a.Processor, &a.Status, &a.ProcessorRef, &a.ReversalKind, &a.ReversalRef,
		&a.RespCode, &a.LastError, &a.PaymentID, &a.Resolution, &a.ResolutionNote,
		&a.ResolvedBy, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// Claim writes the attempt row and commits it before any processor call. created is true for the
// caller that inserted it; a second claim of the same leg key returns the existing row with created
// false and calls nothing. ErrTillUnresolved refuses a NEW claim while the same till session holds an
// unresolved attempt for the same amount under a DIFFERENT sale key (legs of one sale key never
// count against each other).
//
// Race-free under concurrency, with no read then write. The unique leg_key is the arbiter for one
// leg key: ON CONFLICT DO NOTHING lets exactly one insert land. The till guard is a guarded insert
// (INSERT ... SELECT ... WHERE NOT EXISTS) whose snapshot is taken AFTER a transaction scoped
// advisory lock on the (till session, amount) pair, so two claims for different sale keys on one till
// and amount are serialised: the second one's guard sees the first one's committed row. The lock and
// the insert share one transaction on one pool connection, and the winner holds the lock only for one
// statement, so a waiter never needs a second connection to make the holder finish.
func (s *Store) Claim(ctx context.Context, in ClaimInput) (Attempt, bool, error) {
	if s.pool == nil {
		return Attempt{}, false, errAttemptsNoDB
	}
	if in.LegKey == "" || in.SaleKey == "" || in.BranchID == "" || in.TillSessionID == "" ||
		in.AmountCents <= 0 || in.LegSeq < 1 {
		return Attempt{}, false, errBadClaim
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Attempt{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"card_charge_attempts:"+in.TillSessionID+":"+strconv.FormatInt(in.AmountCents, 10)); err != nil {
		return Attempt{}, false, err
	}
	a, err := scanAttempt(tx.QueryRow(ctx, `
		INSERT INTO card_charge_attempts
		  (leg_key, sale_key, leg_seq, amount_cents, branch_id, till_session_id, customer_id, card_type, processor,
		   status, created_by)
		SELECT $1, $2::uuid, $3, $4::bigint, $5::uuid, $6::uuid, NULLIF($7, '')::uuid, $8, $9, 'CHARGING', NULLIF($10, '')::uuid
		WHERE NOT EXISTS (
		  SELECT 1 FROM card_charge_attempts g
		   WHERE g.till_session_id = $6::uuid AND g.amount_cents = $4::bigint AND g.sale_key <> $2::uuid
		     AND g.status IN (`+unresolvedForGuard+`))
		ON CONFLICT (leg_key) DO NOTHING
		RETURNING `+attemptColumns,
		in.LegKey, in.SaleKey, in.LegSeq, in.AmountCents, in.BranchID, in.TillSessionID, in.CustomerID,
		in.CardType, in.Processor, in.CreatedBy))
	if err == nil {
		return a, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, false, err
	}
	// Nothing was inserted: the leg key already has its row (return it, the guard never refuses a
	// retry of its own leg), or the guard refused a new leg.
	existing, err := scanAttempt(tx.QueryRow(ctx, `SELECT `+attemptColumns+` FROM card_charge_attempts WHERE leg_key = $1`, in.LegKey))
	if err == nil {
		return existing, false, tx.Commit(ctx)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, false, ErrTillUnresolved
	}
	return Attempt{}, false, err
}

// GetByLegKey reads the attempt for a leg key; ok is false when there is none.
func (s *Store) GetByLegKey(ctx context.Context, legKey string) (Attempt, bool, error) {
	if s.pool == nil {
		return Attempt{}, false, errAttemptsNoDB
	}
	a, err := scanAttempt(s.pool.QueryRow(ctx, `SELECT `+attemptColumns+` FROM card_charge_attempts WHERE leg_key = $1`, legKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, false, nil
	}
	return a, err == nil, err
}

// move is the one guarded statement every transition is: set is a code constant naming the columns
// the move writes (arguments start at $3), from the statuses the move is allowed out of.
func move(ctx context.Context, q attemptQuerier, id string, from []string, set string, args ...any) (Attempt, bool, error) {
	a, err := scanAttempt(q.QueryRow(ctx,
		`UPDATE card_charge_attempts SET `+set+`, updated_at = now()
		  WHERE id = $1::uuid AND status = ANY($2::text[])
		  RETURNING `+attemptColumns,
		append([]any{id, from}, args...)...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, false, nil
	}
	if err != nil {
		return Attempt{}, false, err
	}
	return a, true, nil
}

func (s *Store) poolMove(ctx context.Context, id string, from []string, set string, args ...any) (Attempt, bool, error) {
	if s.pool == nil {
		return Attempt{}, false, errAttemptsNoDB
	}
	return move(ctx, s.pool, id, from, set, args...)
}

// StampCaptured records Run's approval: CHARGING to CAPTURED, with the charge's processor ref.
func (s *Store) StampCaptured(ctx context.Context, id, ref, respCode string) (Attempt, bool, error) {
	if id == "" || ref == "" {
		return Attempt{}, false, errBadStamp
	}
	return s.poolMove(ctx, id, []string{AttemptCharging}, `status = 'CAPTURED', processor_ref = $3, resp_code = $4`, ref, respCode)
}

// StampDeclined records Run's decline: CHARGING to DECLINED. No money moved.
func (s *Store) StampDeclined(ctx context.Context, id, respCode, lastErr string) (Attempt, bool, error) {
	return s.poolMove(ctx, id, []string{AttemptCharging}, `status = 'DECLINED', resp_code = $3, last_error = $4`,
		respCode, SanitizeLastError(lastErr))
}

// StampUnknown records a charge with no usable answer: CHARGING to UNKNOWN.
func (s *Store) StampUnknown(ctx context.Context, id, lastErr string) (Attempt, bool, error) {
	return s.poolMove(ctx, id, []string{AttemptCharging}, `status = 'UNKNOWN', last_error = $3`, SanitizeLastError(lastErr))
}

// BeginVoiding claims the compensation: CAPTURED to VOIDING. It is the one move that competes with
// BindTx for a captured row.
func (s *Store) BeginVoiding(ctx context.Context, id string) (Attempt, bool, error) {
	return s.poolMove(ctx, id, []string{AttemptCaptured}, `status = 'VOIDING'`)
}

// StampVoided records an approved reversal: VOIDING to VOIDED, with how (VOID or REFUND) and its ref.
func (s *Store) StampVoided(ctx context.Context, id, kind, ref string) (Attempt, bool, error) {
	if id == "" || ref == "" || (kind != ReversalVoid && kind != ReversalRefund) {
		return Attempt{}, false, errBadStamp
	}
	return s.poolMove(ctx, id, []string{AttemptVoiding}, `status = 'VOIDED', reversal_kind = $3, reversal_ref = $4`, kind, ref)
}

// StampVoidUnknown records a reversal whose answer was lost: VOIDING to VOID_UNKNOWN.
func (s *Store) StampVoidUnknown(ctx context.Context, id, lastErr string) (Attempt, bool, error) {
	return s.poolMove(ctx, id, []string{AttemptVoiding}, `status = 'VOID_UNKNOWN', last_error = $3`, SanitizeLastError(lastErr))
}

// StampVoidDeclined records a reversal Run refused: VOIDING to VOID_DECLINED. The customer's money
// is held with no sale until a manager reverses it at the processor.
func (s *Store) StampVoidDeclined(ctx context.Context, id, lastErr string) (Attempt, bool, error) {
	return s.poolMove(ctx, id, []string{AttemptVoiding}, `status = 'VOID_DECLINED', last_error = $3`, SanitizeLastError(lastErr))
}

// BindTx is the sale's own move, on the SALE's transaction: CAPTURED to BOUND with the payments row it
// created. It rides tx and never the pool, so the bind commits or rolls back with the sale: a
// rollback leaves the row CAPTURED for the compensator. Its UPDATE takes the row lock, so a sweep or
// compensator move that arrives first is waited out and then fails its own guard, and one that
// arrives after finds BOUND.
func (s *Store) BindTx(ctx context.Context, tx pgx.Tx, id, paymentID string) (Attempt, bool, error) {
	if tx == nil || id == "" || paymentID == "" {
		return Attempt{}, false, errBadBind
	}
	return move(ctx, tx, id, []string{AttemptCaptured}, `status = 'BOUND', payment_id = $3::uuid`, paymentID)
}

// Resolve is the manager's close of an item that needed a person: UNKNOWN, VOID_UNKNOWN or
// VOID_DECLINED to RESOLVED. A resolution, a note and a resolver are required. The row stays and its
// leg key never charges again.
func (s *Store) Resolve(ctx context.Context, id, resolution, note, resolvedBy string) (Attempt, bool, error) {
	note = strings.TrimSpace(note)
	if id == "" || note == "" || resolvedBy == "" ||
		(resolution != ResolutionNotCharged && resolution != ResolutionReversedAtProcessor) {
		return Attempt{}, false, ErrResolveInvalid
	}
	return s.poolMove(ctx, id, []string{AttemptUnknown, AttemptVoidUnknown, AttemptVoidDeclined},
		`status = 'RESOLVED', resolution = $3, resolution_note = $4, resolved_by = $5::uuid`, resolution, note, resolvedBy)
}

// ListOpen lists what a manager reads: the attempts that need a person (OpenOnly), plus every attempt
// created in the date range that carries a processor ref and is not BOUND, so a settlement report's
// every trans_id with no sale behind it appears with its status. Newest first, bounded.
func (s *Store) ListOpen(ctx context.Context, q ListQuery) ([]Attempt, error) {
	ranged := !q.From.IsZero() || !q.To.IsZero()
	if !q.OpenOnly && !ranged {
		return nil, ErrListUnbounded
	}
	if s.pool == nil {
		return nil, errAttemptsNoDB
	}
	var from, to *time.Time
	if !q.From.IsZero() {
		from = &q.From
	}
	if !q.To.IsZero() {
		to = &q.To
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+attemptColumns+` FROM card_charge_attempts
		 WHERE ($1::bool AND status IN (`+needsAPerson+`))
		    OR ($2::bool AND processor_ref <> '' AND status <> 'BOUND'
		        AND ($3::timestamptz IS NULL OR created_at >= $3::timestamptz)
		        AND ($4::timestamptz IS NULL OR created_at < $4::timestamptz))
		 ORDER BY created_at DESC, id
		 LIMIT `+strconv.Itoa(listLimit), q.OpenOnly, ranged, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

var (
	uuidShieldRe = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	bearerRe     = regexp.MustCompile(`(?i)\bbearer\s+[^\s"',;]+`)
	// A secret-looking key, then its value. The key may be a JSON key (quoted) or a form or log key.
	secretKVRe = regexp.MustCompile(`(?i)([a-z0-9_]*(?:token|secret|password|passwd|pwd|api_?key|authorization|credential|cvv|cvc|card_?number)[a-z0-9_]*["']?\s*[:=]\s*)(["']?)([^\s"',;&}]+)`)
	// A card number: thirteen to nineteen digits, with single spaces or hyphens between them.
	panRe = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	// Any long opaque run (a token, a key, a signature). Hyphens count so a hyphenated token is one
	// run, and a uuid (shielded above anyway) stays shorter than the floor.
	opaqueRe = regexp.MustCompile(`[A-Za-z0-9_+/=-]{40,}`)
)

// SanitizeLastError returns text safe to keep in last_error: a bounded single line with no token,
// credential or card number in it. It runs on every write of the column, whatever the caller passed
// (a processor's error text, a transport error, a response body excerpt). It redacts rather than
// rejects, so the diagnostic survives; uuids are kept (a sale key or an attempt id is not a secret).
func SanitizeLastError(s string) string {
	if s == "" {
		return ""
	}
	var ids []string
	s = uuidShieldRe.ReplaceAllStringFunc(s, func(m string) string {
		ids = append(ids, m)
		return "\x00" + strconv.Itoa(len(ids)-1) + "\x00"
	})
	s = bearerRe.ReplaceAllString(s, "Bearer [redacted]")
	s = secretKVRe.ReplaceAllString(s, "${1}${2}[redacted]")
	s = panRe.ReplaceAllString(s, "[redacted number]")
	s = opaqueRe.ReplaceAllString(s, "[redacted token]")
	for i, id := range ids {
		s = strings.Replace(s, "\x00"+strconv.Itoa(i)+"\x00", id, 1)
	}
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }), " ")
	if utf8.RuneCountInString(s) > maxLastErrorRunes {
		s = string([]rune(s)[:maxLastErrorRunes])
	}
	return strings.TrimSpace(s)
}
