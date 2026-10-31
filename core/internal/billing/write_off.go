package billing

// write_off.go, W3 item 0.10: A MANAGER WRITES OFF AN INVOICE'S REMAINING BALANCE.
//
// -- THE ACT, AND WHAT IT POSTS -------------------------------------------------------------------
// A receivable the dealer will never collect (a small balance after part payment, a dead account)
// sits open on the aging forever unless the books are allowed to say so. The write off is that
// saying: the invoice's whole remaining balance leaves AR, DR a bad debt expense account and
// CR 1100, one balanced journal through the GL module's seam (the transactional outbox row is
// written in the write off's own transaction, the deposit cancel's discipline, so the journal can
// never lag the settlement it belongs to). The subledger carries the account's side of the same
// act: one WRITE_OFF row against the invoice, negative, source_type 'invoice', the tag the aging's
// dated arms read (the same rule the deposit cancel's closed row and the credit application's
// invoice side follow: the row mirrors a document balance that moved on the invoices arm, so the
// non-invoice arm must never count it too, or the 1100 tie breaks by the write off). The aging and
// GL 1100 therefore move by the same cents, at every as-of: the invoice's balance carries the move
// from the act's instant (the dated ledger rows tagged to the document), and the journal's legs are
// dated by the same instant.
//
// -- THE LIMIT -------------------------------------------------------------------------------------
// A small balance limit keeps a desk from writing off a debt that deserves a collections call. The
// limit is a BRANCH setting on ar_credit_policy (write_off_limit_cents, 0454), read the way every
// other policy figure there is read: the branch's own row when one exists, otherwise the org
// default row (which the column default gives 2500, $25.00). A write off above the limit is refused
// in words naming the limit, unless the caller holds the controller role (the request carries the
// role's answer; the handler reads it off the caller's staff context, the role code the staff
// tables resolved, never out of the request body). The limit is judged INSIDE the write off's
// transaction, so a policy change cannot move the line between the read and the act.
//
// -- THE ONE ACT PER INVOICE, AND THE KEY'S RECORD --------------------------------------------------
// The invoice's row lock is the door's serialisation: a written off invoice is settled (balance 0,
// PAID), and the not open refusal stops any second write off under a different key. The act's
// record lives on the invoice's metadata, under the 'write_offs' envelope (jsonb_set on the one
// key, the credit applications' posture, so neither act's stamp ever clobbers the other's), keyed
// by the request's idempotency key: the same key with the same body answers the first result and
// writes nothing. The record is also what a reversal reads to restore both sides, so the act's full
// state lives nowhere else. A spent key fired at ANOTHER invoice of the same org and branch is refused
// in words, naming the invoice the key already wrote off (W3 item 0.15): the lookup reads the same
// envelope on the other invoices rather than a key table (the credit door's org scoped key table,
// 0447, is the machinery for a door whose acts are many per invoice; this door has at most one live
// act per invoice and the invoice state is the guard).
//
// -- THE REVERSAL ----------------------------------------------------------------------------------
// A write off is reversible ONCE, with a reason: the invoice's balance comes back whole, the status
// it carried before the write off comes back with it (stored on the record, the credit
// application's CreditPriorStatus rule), a second WRITE_OFF row nets the account's side to zero,
// and the reversing journal (DR 1100 / CR the expense, key ar.invoice.write_off_reversed:<write off
// id>) puts the books back. The record is stamped reversed and stays as the act's history; a second
// reversal is refused (ErrWriteOffNotReversible). An invoice that is no longer the write off's own
// settled shape (voided since, balance moved) refuses the restore rather than guessing.
//
// -- THE LOCKS -------------------------------------------------------------------------------------
// The invoice row first (FOR NO KEY UPDATE, the credit doors' lock), every judgement under it, the
// balance move drawing the invoices feed counter beside its write (creditFeedSeqCTE, the settle's
// posture). The reversal takes the same row the same way. No payments counter is drawn and no
// payments row is stamped: this is billing's own document.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/lien"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// InvoiceWrittenOffTopic and InvoiceWriteOffReversedTopic are the outbox topics the GL posting
// consumer owns; their journal keys are the topics plus the write off's id, so a replayed event
// posts nothing and the reversal's journal is its own row.
const (
	InvoiceWrittenOffTopic       = "ar.invoice.write_off"
	InvoiceWriteOffReversedTopic = "ar.invoice.write_off_reversed"
)

// ActInvoiceWrittenOff and ActInvoiceWriteOffReversed are the audit verbs.
const (
	ActInvoiceWrittenOff       = "INVOICE_WRITE_OFF"
	ActInvoiceWriteOffReversed = "INVOICE_WRITE_OFF_REVERSED"
)

// ErrWriteOffReasonRequired is the reason rule, as its own error so a UI can prompt for it.
var ErrWriteOffReasonRequired = errors.New("billing: a reason is required")

// ErrWriteOffNotReversible is the refusal for a write off already reversed (409).
var ErrWriteOffNotReversible = errors.New("billing: that write off has already been reversed")

// WriteOffOverLimitError is the over limit refusal (403): the branch's limit is named, the caller's
// desk cannot pass it, the controller's can.
type WriteOffOverLimitError struct{ LimitCents int64 }

func (e *WriteOffOverLimitError) Error() string {
	return fmt.Sprintf("billing: that balance is over this branch's %d cent write off limit; the controller can pass it", e.LimitCents)
}

// WriteOffConflictError is the spent key refusal with a different body, 409 like the credit door's.
type WriteOffConflictError struct{ Msg string }

func (e WriteOffConflictError) Error() string { return e.Msg }

// WriteOffInput is one write off request.
type WriteOffInput struct {
	InvoiceID string
	Reason    string
	ActorID   string
	// IdempotencyKey is mandatory: the act is recorded under it on the invoice, and a retry under
	// the same key with the same body answers the first result and writes nothing.
	IdempotencyKey string
	// OrgID is the caller's org slug scope id; an invoice of another org is refused.
	OrgID string
	// CanAccessBranch is the caller's branch reach; nil reaches every branch.
	CanAccessBranch func(branchID string) bool
	// IsController is the caller's controller role answer, resolved by the handler from the staff
	// context's role code, never from the request. It passes the branch's limit.
	IsController bool
}

// WriteOffResult is the write off's answer.
type WriteOffResult struct {
	WriteOffID  string `json:"write_off_id"`
	InvoiceID   string `json:"invoice_id"`
	InvoiceNo   string `json:"invoice_no"`
	AmountCents int64  `json:"amount_cents"`
	LimitCents  int64  `json:"limit_cents"`
	Replayed    bool   `json:"replayed"`
}

// ReverseWriteOffInput is one reversal request.
type ReverseWriteOffInput struct {
	InvoiceID  string
	WriteOffID string
	Reason     string
	ActorID    string
	OrgID      string
	// CanAccessBranch is the caller's branch reach; nil reaches every branch.
	CanAccessBranch func(branchID string) bool
}

// WriteOffReversal is the reversal's answer.
type WriteOffReversal struct {
	WriteOffID     string `json:"write_off_id"`
	InvoiceID      string `json:"invoice_id"`
	InvoiceNo      string `json:"invoice_no"`
	RestoredCents  int64  `json:"restored_cents"`
	InvoiceBalance int64  `json:"invoice_balance_after_cents"`
	Replayed       bool   `json:"replayed"`
}

// writeOffRecord is what the invoice's metadata carries for one write off: the whole state of the
// act, which the reversal restores from.
type writeOffRecord struct {
	WriteOffID   string `json:"write_off_id"`
	AmountCents  int64  `json:"amount_cents"`
	Reason       string `json:"reason"`
	WrittenOffBy string `json:"written_off_by,omitempty"`
	WrittenOffAt string `json:"written_off_at"`
	// HoldbackCents is the part of the written off balance that was the invoice's holdback (1150, not
	// 1100); the reversal puts it back. Zero for an invoice that withheld nothing.
	HoldbackCents int64 `json:"holdback_cents,omitempty"`
	// RequestHash is the request's own hash (writeOffKeyHash): a retry under the same key is
	// answered from the record when the hash matches and refused when it does not.
	RequestHash string `json:"request_hash"`
	// PriorStatus is the status the invoice carried BEFORE the write off; the reversal restores it.
	PriorStatus string `json:"prior_status"`
	// Reversal is set when the write off has been undone; the record stays as the act's history.
	Reversal *writeOffReversalRecord `json:"reversal,omitempty"`
}

type writeOffReversalRecord struct {
	Reason     string `json:"reason"`
	ReversedBy string `json:"reversed_by,omitempty"`
	ReversedAt string `json:"reversed_at"`
}

// writeOffMetadata is the metadata envelope the invoice carries beside the credit applications'.
type writeOffMetadata struct {
	WriteOffs map[string]writeOffRecord `json:"write_offs"`
}

// readWriteOffs parses the invoice's metadata for the write off records. No metadata, or none of
// this act's, reads as an empty map.
func readWriteOffs(raw string) (map[string]writeOffRecord, error) {
	out := map[string]writeOffRecord{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	var m writeOffMetadata
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return out, nil // foreign metadata: not this act's, and never this act's to fail on
	}
	if m.WriteOffs == nil {
		return out, nil
	}
	return m.WriteOffs, nil
}

// stampWriteOffs writes the write off records back onto the invoice's metadata, keeping every other
// key the metadata already carried.
func stampWriteOffs(ctx context.Context, tx pgx.Tx, invoiceID string, records map[string]writeOffRecord) error {
	raw, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("write off: marshal the record: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE invoices
   SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{write_offs}', $2::jsonb, true),
       updated_at = now()
 WHERE id = $1::uuid`, invoiceID, string(raw)); err != nil {
		return fmt.Errorf("write off: stamp the record: %w", err)
	}
	return nil
}

// writeOffKeyHash is the request's own hash, the credit door's rule: the sha256 of the request's
// body fields on the validated input, so a retry that spells a uuid differently still replays and a
// changed reason is a different request. The act's amount is not in the body: it is the invoice's
// whole remaining balance, judged at the door.
func writeOffKeyHash(in WriteOffInput) string {
	h := sha256.Sum256([]byte(strings.Join([]string{"write-off", in.InvoiceID, in.Reason}, "\n")))
	return hex.EncodeToString(h[:])
}

// WriteOffService is the permission-shaped door over the repository write. The handler gates the
// route with RequireManager AND the service re-checks IsManager: the route gate stops the request,
// the service check stops an in-process caller that bypassed HTTP, and neither is a substitute for
// the other (the credit apply service's own rule).
type WriteOffService struct {
	repo  *Repository
	audit audit.Recorder
	now   func() time.Time
	zone  *houseday.Zone
}

// NewWriteOffService wires the service over the billing repository.
func NewWriteOffService(repo *Repository) *WriteOffService {
	return &WriteOffService{repo: repo, now: time.Now}
}

// WithAudit attaches the audit recorder (best effort: an audit failure never blocks the books).
func (s *WriteOffService) WithAudit(r audit.Recorder) *WriteOffService { s.audit = r; return s }

// WithClock overrides the clock (tests).
func (s *WriteOffService) WithClock(now func() time.Time) *WriteOffService {
	if now != nil {
		s.now = now
	}
	return s
}

// WithZone binds the dealer's house zone, so the record's instant is stamped beside the journal's.
func (s *WriteOffService) WithZone(z *houseday.Zone) *WriteOffService { s.zone = z; return s }

// validateWriteOff is the request check that runs before any database is consulted, the credit
// door's validateApplyCredit posture: ids normalised to one canonical spelling, reason and key and
// org present.
func validateWriteOff(in WriteOffInput) (WriteOffInput, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return in, ErrWriteOffReasonRequired
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return in, httpx.BadRequestf("writing off an invoice needs an idempotency key")
	}
	if strings.TrimSpace(in.OrgID) == "" {
		return in, httpx.BadRequestf("writing off an invoice needs the caller's org")
	}
	u, err := uuid.Parse(strings.TrimSpace(in.InvoiceID))
	if err != nil {
		return in, httpx.BadRequestf("invoice id must be a uuid")
	}
	in.InvoiceID = u.String()
	in.Reason = strings.TrimSpace(in.Reason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	return in, nil
}

// WriteOff writes off an invoice's remaining balance. Manager only, reason required, audited; over
// the branch's limit only the controller passes.
func (s *WriteOffService) WriteOff(ctx context.Context, in WriteOffInput, isManager bool) (WriteOffResult, error) {
	if !isManager {
		return WriteOffResult{}, errors.New("billing: writing off an invoice is a manager action")
	}
	in, err := validateWriteOff(in)
	if err != nil {
		return WriteOffResult{}, err
	}
	res, err := s.repo.WriteOffInvoice(ctx, in, s.now())
	if err != nil {
		return WriteOffResult{}, err
	}
	if s.audit != nil && !res.Replayed {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActInvoiceWrittenOff, EntityType: "invoice", EntityID: in.InvoiceID,
			ActorID: in.ActorID, Purpose: "AR_WRITE_OFF", Reason: in.Reason,
			Extra: map[string]any{
				"write_off_id": res.WriteOffID,
				"amount_cents": res.AmountCents,
				"limit_cents":  res.LimitCents,
				"controller":   in.IsController,
				"invoice_no":   res.InvoiceNo,
			},
		})
	}
	return res, nil
}

// Reverse reverses a write off, once. Manager only, reason required, audited.
func (s *WriteOffService) Reverse(ctx context.Context, in ReverseWriteOffInput, isManager bool) (WriteOffReversal, error) {
	if !isManager {
		return WriteOffReversal{}, errors.New("billing: reversing a write off is a manager action")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return WriteOffReversal{}, ErrWriteOffReasonRequired
	}
	invID, err := uuid.Parse(strings.TrimSpace(in.InvoiceID))
	if err != nil {
		return WriteOffReversal{}, httpx.BadRequestf("invoice id must be a uuid")
	}
	woID, err := uuid.Parse(strings.TrimSpace(in.WriteOffID))
	if err != nil {
		return WriteOffReversal{}, httpx.BadRequestf("write off id must be a uuid")
	}
	in.InvoiceID, in.WriteOffID = invID.String(), woID.String()
	in.Reason = strings.TrimSpace(in.Reason)
	res, err := s.repo.ReverseWriteOff(ctx, in, s.now())
	if err != nil {
		return WriteOffReversal{}, err
	}
	if s.audit != nil && !res.Replayed {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: ActInvoiceWriteOffReversed, EntityType: "invoice", EntityID: in.InvoiceID,
			ActorID: in.ActorID, Purpose: "AR_WRITE_OFF_REVERSE", Reason: in.Reason,
			Extra: map[string]any{
				"write_off_id":   res.WriteOffID,
				"restored_cents": res.RestoredCents,
			},
		})
	}
	return res, nil
}

// WriteOffInvoice is the repository write. ONE transaction: the invoice row locked, every judgement
// under it (the replay first, then the open and limit gates, the credit door's order), then the
// balance move, the subledger row, the metadata stamp and the outbox row. A refused request writes
// nothing.
func (r *Repository) WriteOffInvoice(ctx context.Context, in WriteOffInput, now time.Time) (WriteOffResult, error) {
	if r.pool == nil {
		return WriteOffResult{}, errNoDB
	}
	in, err := validateWriteOff(in)
	if err != nil {
		return WriteOffResult{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return WriteOffResult{}, fmt.Errorf("write off: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The invoice row, the door's lock: an INVOICE document only, the credit door's target rule (a
	// memo or a deposit document matches no row and answers the same not found).
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid AND doc_type = 'INVOICE' FOR NO KEY UPDATE`, in.InvoiceID); err != nil {
		return WriteOffResult{}, fmt.Errorf("write off: lock invoice %s: %w", in.InvoiceID, err)
	}
	var (
		invNo, invBranch, invStatus, invOrg, invCust string
		invBalance, invHoldback                      int64
		metadataRaw                                  string
	)
	err = tx.QueryRow(ctx, `
SELECT invoice_no::text, balance_cents, holdback_cents, branch_id::text, status,
       COALESCE(org_id::text, ''), customer_id::text, COALESCE(metadata::text, '')
  FROM invoices WHERE id = $1::uuid AND doc_type = 'INVOICE'`, in.InvoiceID).
		Scan(&invNo, &invBalance, &invHoldback, &invBranch, &invStatus, &invOrg, &invCust, &metadataRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WriteOffResult{}, NoSuchInvoiceError{InvoiceID: in.InvoiceID}
		}
		return WriteOffResult{}, fmt.Errorf("write off: read the invoice: %w", err)
	}
	if in.CanAccessBranch != nil && !in.CanAccessBranch(invBranch) {
		return WriteOffResult{}, NoSuchInvoiceError{InvoiceID: in.InvoiceID}
	}
	if in.OrgID != "" && invOrg != "" && invOrg != in.OrgID {
		return WriteOffResult{}, httpx.BadRequestf("invoice %s is another org's document, this desk cannot write it off", invNo)
	}

	// The invoice's own write off records, read under its row lock. The stamp at the end writes them
	// back with this act's record added, so no other act's record is lost.
	records, err := readWriteOffs(metadataRaw)
	if err != nil {
		return WriteOffResult{}, err
	}

	// The key's judgement BEFORE the open gate (the credit door's K3 P2-2 order): a retry of a write
	// off that already settled the invoice must answer the first result, not trip the not open
	// refusal on the balance the first act already moved.
	requestHash := writeOffKeyHash(in)
	if prior, ok := records[in.IdempotencyKey]; ok {
		if prior.RequestHash == requestHash {
			return WriteOffResult{
				WriteOffID: prior.WriteOffID, InvoiceID: in.InvoiceID, InvoiceNo: invNo,
				AmountCents: prior.AmountCents, Replayed: true,
			}, nil
		}
		return WriteOffResult{}, WriteOffConflictError{
			Msg: "that idempotency key already wrote off an invoice differently; use a new key"}
	}

	// A key already spent on ANOTHER invoice (W3 item 0.15, S5 on 707's P3) is refused in words, before
	// the open gate: a key names one act. The lookup is the same envelope read on every other invoice
	// of the org and this invoice's branch (write off volume is small), so the invoice it names is one
	// the caller's branch fence already reaches.
	var spentNo string
	err = tx.QueryRow(ctx, `
SELECT invoice_no::text FROM invoices
 WHERE id <> $1::uuid AND doc_type = 'INVOICE' AND branch_id = $2::uuid
   AND COALESCE(org_id::text, '') = $3 AND metadata->'write_offs' ? $4
 LIMIT 1`, in.InvoiceID, invBranch, invOrg, in.IdempotencyKey).Scan(&spentNo)
	switch {
	case err == nil:
		return WriteOffResult{}, WriteOffConflictError{Msg: fmt.Sprintf(
			"that idempotency key already wrote off invoice %s; a key names one write off, use a new key for invoice %s", spentNo, invNo)}
	case !errors.Is(err, pgx.ErrNoRows):
		return WriteOffResult{}, fmt.Errorf("write off: look for the key on other invoices: %w", err)
	}

	if invStatus != "FINALIZED" && invStatus != "PARTIALLY_PAID" {
		return WriteOffResult{}, httpx.BadRequestf("invoice %s is not open, only an open invoice can be written off", invNo)
	}
	if invBalance <= 0 {
		return WriteOffResult{}, httpx.BadRequestf("invoice %s holds no balance to write off", invNo)
	}

	// The branch's limit, read INSIDE the transaction so a policy change cannot move the line
	// between the read and the act: the branch's own row when one exists, otherwise the org default
	// (the column default gives the org default 2500, $25.00). No policy row at all is the loud
	// migration failure the credit policy read answers, never a permissive silence.
	var limit int64
	err = tx.QueryRow(ctx, `
SELECT write_off_limit_cents
  FROM ar_credit_policy
 WHERE branch_id = NULLIF($1, '')::uuid OR branch_id IS NULL
 ORDER BY branch_id NULLS LAST
 LIMIT 1`, invBranch).Scan(&limit)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WriteOffResult{}, errors.New("billing: no ar_credit_policy row, migration 0104 seeds the org default and 0454 its write off limit")
		}
		return WriteOffResult{}, fmt.Errorf("write off: read the branch's limit: %w", err)
	}
	if invBalance > limit && !in.IsController {
		return WriteOffResult{}, &WriteOffOverLimitError{LimitCents: limit}
	}

	// The balance leaves: the invoice settles at zero, the settle's own status flip. The holdback part of
	// it (the invoice debited 1150 for that part, 1100 for the rest) leaves the invoice's holdback too, so
	// nothing withheld is left behind on a settled invoice (RT-2).
	writeOffID := uuid.NewString()
	holdbackPart := min(max(invHoldback, 0), invBalance)
	if err := r.requireHoldbackSeam(holdbackPart); err != nil {
		return WriteOffResult{}, err
	}
	if _, err := tx.Exec(ctx, creditFeedSeqCTE+`
UPDATE invoices
   SET balance_cents = 0, status = 'PAID', holdback_cents = holdback_cents - $2::bigint,
       updated_at = now(), seq = (SELECT next_value FROM iseq)
 WHERE id = $1::uuid`, in.InvoiceID, holdbackPart); err != nil {
		return WriteOffResult{}, fmt.Errorf("write off: settle the invoice: %w", err)
	}

	// The account's side: one WRITE_OFF row against the invoice, negative, source_type 'invoice'
	// (the load bearing tag: the row mirrors a document balance that moved on the invoices arm), the
	// running AR figure the deposit cancel's rows carry.
	runningAR, err := customerRunningAR(ctx, tx, invCust)
	if err != nil {
		return WriteOffResult{}, fmt.Errorf("write off: read the account's AR: %w", err)
	}
	words := "write off " + invNo + ": " + in.Reason
	if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions
  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'WRITE_OFF', $2, $3, 'invoice', $4::uuid, $5::uuid, $6, $7)`,
		invCust, -invBalance, runningAR, in.InvoiceID, invBranch, words, now); err != nil {
		return WriteOffResult{}, fmt.Errorf("write off: the account's row: %w", err)
	}

	// The record of the act, on the invoice's metadata under the key.
	records[in.IdempotencyKey] = writeOffRecord{
		WriteOffID: writeOffID, AmountCents: invBalance, Reason: in.Reason,
		WrittenOffBy: in.ActorID, WrittenOffAt: now.UTC().Format(time.RFC3339),
		RequestHash: requestHash, PriorStatus: invStatus, HoldbackCents: holdbackPart,
	}
	if err := stampWriteOffs(ctx, tx, in.InvoiceID, records); err != nil {
		return WriteOffResult{}, err
	}

	// The GL event, on the same transaction, the deposit cancel's discipline: DR the expense,
	// CR 1100, posted by the consumer on the act's own key and the act's own instant.
	if err := emitOutbox(ctx, tx, InvoiceWrittenOffTopic, map[string]any{
		"invoice_id": in.InvoiceID, "write_off_id": writeOffID,
		"amount_cents": invBalance, "holdback_cents": holdbackPart, "reason": in.Reason, "occurred_at": now,
	}); err != nil {
		return WriteOffResult{}, err
	}
	// The holdback part leaves the retention ledger on this same transaction, as a credit memo's does.
	if err := r.moveHoldback(ctx, tx, in.InvoiceID, invBranch, "write off "+writeOffID, -holdbackPart); err != nil {
		return WriteOffResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WriteOffResult{}, fmt.Errorf("write off: commit: %w", err)
	}
	return WriteOffResult{
		WriteOffID: writeOffID, InvoiceID: in.InvoiceID, InvoiceNo: invNo,
		AmountCents: invBalance, LimitCents: limit,
	}, nil
}

// ReverseWriteOff is the reversal write: the record is read from the invoice's metadata, judged,
// restored, and stamped reversed, in one transaction with the account's row and the outbox row.
func (r *Repository) ReverseWriteOff(ctx context.Context, in ReverseWriteOffInput, now time.Time) (WriteOffReversal, error) {
	if r.pool == nil {
		return WriteOffReversal{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return WriteOffReversal{}, fmt.Errorf("write off reverse: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, in.InvoiceID); err != nil {
		return WriteOffReversal{}, fmt.Errorf("write off reverse: lock the invoice: %w", err)
	}
	var (
		invNo, invBranch, invStatus, invOrg, invCust string
		invBalance                                   int64
		metadataRaw                                  string
	)
	err = tx.QueryRow(ctx, `
SELECT invoice_no::text, balance_cents, branch_id::text, status, COALESCE(org_id::text, ''),
       customer_id::text, COALESCE(metadata::text, '')
  FROM invoices WHERE id = $1::uuid`, in.InvoiceID).
		Scan(&invNo, &invBalance, &invBranch, &invStatus, &invOrg, &invCust, &metadataRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WriteOffReversal{}, NoSuchInvoiceError{InvoiceID: in.InvoiceID}
		}
		return WriteOffReversal{}, fmt.Errorf("write off reverse: read the invoice: %w", err)
	}
	if in.CanAccessBranch != nil && !in.CanAccessBranch(invBranch) {
		return WriteOffReversal{}, NoSuchInvoiceError{InvoiceID: in.InvoiceID}
	}
	if in.OrgID != "" && invOrg != "" && invOrg != in.OrgID {
		return WriteOffReversal{}, httpx.BadRequestf("invoice %s is another org's document, this desk cannot reverse its write off", invNo)
	}
	records, err := readWriteOffs(metadataRaw)
	if err != nil {
		return WriteOffReversal{}, err
	}
	var (
		record    writeOffRecord
		recordKey string
		found     bool
	)
	for key, rec := range records {
		if rec.WriteOffID == in.WriteOffID {
			record, recordKey, found = rec, key, true
			break
		}
	}
	if !found {
		return WriteOffReversal{}, httpx.BadRequestf("no write off %s sits on invoice %s", in.WriteOffID, invNo)
	}
	if record.Reversal != nil {
		return WriteOffReversal{}, ErrWriteOffNotReversible
	}
	// The invoice must still be the write off's own settled shape: the write off made it PAID at
	// zero, and nothing else can have moved it (a settled invoice takes no money). A VOIDed invoice
	// is not restored by this door.
	if invStatus != "PAID" || invBalance != 0 {
		return WriteOffReversal{}, httpx.BadRequestf(
			"invoice %s is %s at %d cents, not the write off's own settled shape; restore it by hand", invNo, strings.ToLower(invStatus), invBalance)
	}

	// The invoice side comes back whole, at the status it carried before the act (the credit
	// application's CreditPriorStatus rule; a record from before the rule restores to FINALIZED).
	priorStatus := record.PriorStatus
	if priorStatus == "" {
		priorStatus = "FINALIZED"
	}
	if err := r.requireHoldbackSeam(record.HoldbackCents); err != nil {
		return WriteOffReversal{}, err
	}
	if _, err := tx.Exec(ctx, creditFeedSeqCTE+`
UPDATE invoices
   SET balance_cents = $2::bigint, status = $3::text, holdback_cents = holdback_cents + $4::bigint,
       updated_at = now(), seq = (SELECT next_value FROM iseq)
 WHERE id = $1::uuid`, in.InvoiceID, record.AmountCents, priorStatus, record.HoldbackCents); err != nil {
		return WriteOffReversal{}, fmt.Errorf("write off reverse: restore the invoice: %w", err)
	}

	// The account's side nets to zero over the act and its undo.
	runningAR, err := customerRunningAR(ctx, tx, invCust)
	if err != nil {
		return WriteOffReversal{}, fmt.Errorf("write off reverse: read the account's AR: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO customer_transactions
  (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'WRITE_OFF', $2, $3, 'invoice', $4::uuid, $5::uuid, $6, $7)`,
		invCust, record.AmountCents, runningAR, in.InvoiceID, invBranch,
		"write off reversed "+invNo+": "+in.Reason, now); err != nil {
		return WriteOffReversal{}, fmt.Errorf("write off reverse: the account's restore row: %w", err)
	}

	// Stamp the reversal onto the record.
	record.Reversal = &writeOffReversalRecord{
		Reason: in.Reason, ReversedBy: in.ActorID, ReversedAt: now.UTC().Format(time.RFC3339),
	}
	records[recordKey] = record
	if err := stampWriteOffs(ctx, tx, in.InvoiceID, records); err != nil {
		return WriteOffReversal{}, err
	}

	// The reversing journal, on the same transaction: DR 1100 / CR the expense, on the act's key.
	if err := emitOutbox(ctx, tx, InvoiceWriteOffReversedTopic, map[string]any{
		"invoice_id": in.InvoiceID, "write_off_id": in.WriteOffID,
		"amount_cents": record.AmountCents, "holdback_cents": record.HoldbackCents, "reason": in.Reason, "occurred_at": now,
	}); err != nil {
		return WriteOffReversal{}, err
	}
	if err := r.moveHoldback(ctx, tx, in.InvoiceID, invBranch, "write off reversed "+record.WriteOffID, record.HoldbackCents); err != nil {
		return WriteOffReversal{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WriteOffReversal{}, fmt.Errorf("write off reverse: commit: %w", err)
	}
	return WriteOffReversal{
		WriteOffID: record.WriteOffID, InvoiceID: in.InvoiceID, InvoiceNo: invNo,
		RestoredCents: record.AmountCents, InvoiceBalance: record.AmountCents,
	}, nil
}

// WriteOffsFor reads the write offs recorded on an invoice's metadata, live or reversed, oldest
// first, so the invoice detail panel shows the act and its undo.
func (r *Repository) WriteOffsFor(ctx context.Context, invoiceID string) ([]WriteOffView, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var raw string
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(metadata::text, '') FROM invoices WHERE id = $1::uuid`, invoiceID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []WriteOffView{}, nil
		}
		return nil, fmt.Errorf("write off: read the invoice's write offs: %w", err)
	}
	records, err := readWriteOffs(raw)
	if err != nil {
		return nil, err
	}
	out := make([]WriteOffView, 0, len(records))
	for _, rec := range records {
		v := WriteOffView{
			WriteOffID:   rec.WriteOffID,
			AmountCents:  rec.AmountCents,
			Reason:       rec.Reason,
			WrittenOffBy: rec.WrittenOffBy,
			WrittenOffAt: rec.WrittenOffAt,
		}
		if rec.Reversal != nil {
			v.Reversed = true
			v.ReversalReason = rec.Reversal.Reason
			v.ReversedBy = rec.Reversal.ReversedBy
			v.ReversedAt = rec.Reversal.ReversedAt
		}
		out = append(out, v)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].WrittenOffAt < out[j-1].WrittenOffAt; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// WriteOffView is one recorded write off as a read answers it.
type WriteOffView struct {
	WriteOffID     string `json:"write_off_id"`
	AmountCents    int64  `json:"amount_cents"`
	Reason         string `json:"reason"`
	WrittenOffBy   string `json:"written_off_by,omitempty"`
	WrittenOffAt   string `json:"written_off_at"`
	Reversed       bool   `json:"reversed"`
	ReversalReason string `json:"reversal_reason,omitempty"`
	ReversedBy     string `json:"reversed_by,omitempty"`
	ReversedAt     string `json:"reversed_at,omitempty"`
}

// requireHoldbackSeam refuses, before the act touches the invoice's holdback column, a part above zero
// with no seam bound: the column and the ledger's row move together or not at all (ErrNoHoldbackSeam).
func (r *Repository) requireHoldbackSeam(part int64) error {
	if part != 0 && r.holdback == nil {
		return ErrNoHoldbackSeam
	}
	return nil
}

// moveHoldback books a write off's (or its reversal's) holdback part on the retention ledger, on the
// act's own transaction, right after the column moved in the same transaction. Zero moves nothing; a
// part with no seam bound is refused.
func (r *Repository) moveHoldback(ctx context.Context, tx pgx.Tx, invoiceID, branchID, reason string, cents int64) error {
	if cents == 0 {
		return nil
	}
	if err := r.requireHoldbackSeam(cents); err != nil {
		return err
	}
	if _, err := r.holdback.BookHoldbackMove(customer.WithCallerTx(ctx, tx), lien.HoldbackMove{
		InvoiceID: invoiceID, BranchID: branchID, Reason: reason, AmountCents: cents,
	}); err != nil {
		return fmt.Errorf("write off: move the holdback ledger: %w", err)
	}
	return nil
}
