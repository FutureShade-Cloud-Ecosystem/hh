package load

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the frame's pool. The package owns its two tables and no
// other; the writes this store makes are registry and batch rows only.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires the frame over the shared pool. A nil pool is legal for
// the boundary tests that exercise the refusals which precede any store
// call; a store over a nil pool answers every read and write with an
// error rather than a panic.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// nameKey is the registry name's four parts, the plan's section 2 rule.
type nameKey struct {
	OrgID        string
	SourceSystem string
	Kind         string
	LegacyID     string
}

// resolveKeys reads the HH rows the names record, no writes. An
// unrecorded name answers an empty id, never a guess; the counts carry
// the found and missing split the reconcile reads.
func (s *Store) ResolveKeys(ctx context.Context, orgID, sourceSystem string, refs []Ref) (ResolveResult, error) {
	if s.pool == nil {
		return ResolveResult{}, errors.New("load: no database")
	}
	if orgID == "" || sourceSystem == "" {
		return ResolveResult{}, ErrMissingField
	}
	if len(refs) == 0 || len(refs) > MaxBatchItems {
		return ResolveResult{}, ErrBatchBound
	}
	out := ResolveResult{Items: make([]Resolution, 0, len(refs)), Counts: map[string]int{"found": 0, "missing": 0}}
	for _, r := range refs {
		if r.Kind == "" || r.LegacyID == "" || len(r.LegacyID) > MaxKeyLength {
			return ResolveResult{}, ErrMissingField
		}
		res := Resolution{Kind: r.Kind, LegacyID: r.LegacyID}
		var hh string
		err := s.pool.QueryRow(ctx, `
			SELECT hh_id::text FROM load_key_registry
			WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = $4`,
			orgID, sourceSystem, r.Kind, r.LegacyID).Scan(&hh)
		if errors.Is(err, pgx.ErrNoRows) {
			out.Counts["missing"]++
		} else if err != nil {
			return ResolveResult{}, fmt.Errorf("load: key read: %w", err)
		} else {
			res.HHID = hh
			out.Counts["found"]++
		}
		out.Items = append(out.Items, res)
	}
	return out, nil
}

// getBatch reads one recorded batch by the caller's batch id inside the
// org. Two source systems may carry the same batch id, which is an
// ambiguity rather than an answer.
func (s *Store) GetBatch(ctx context.Context, orgID, batchID string) (BatchView, bool, error) {
	if s.pool == nil {
		return BatchView{}, false, errors.New("load: no database")
	}
	if orgID == "" || batchID == "" {
		return BatchView{}, false, ErrMissingField
	}
	rows, err := s.pool.Query(ctx, `
		SELECT source_system, batch_id, caller, kind, state, counts::text, items::text, created_at
		FROM load_batch
		WHERE org_id = $1::uuid AND batch_id = $2`, orgID, batchID)
	if err != nil {
		return BatchView{}, false, fmt.Errorf("load: batch read: %w", err)
	}
	defer rows.Close()
	var views []BatchView
	for rows.Next() {
		var v BatchView
		var countsJSON, itemsJSON []byte
		if err := rows.Scan(&v.SourceSystem, &v.BatchID, &v.Caller, &v.Kind, &v.State, &countsJSON, &itemsJSON, &v.CreatedAt); err != nil {
			return BatchView{}, false, fmt.Errorf("load: batch read scan: %w", err)
		}
		if err := json.Unmarshal(countsJSON, &v.Counts); err != nil {
			return BatchView{}, false, fmt.Errorf("load: batch counts decode: %w", err)
		}
		if err := json.Unmarshal(itemsJSON, &v.Items); err != nil {
			return BatchView{}, false, fmt.Errorf("load: batch items decode: %w", err)
		}
		views = append(views, v)
	}
	if err := rows.Err(); err != nil {
		return BatchView{}, false, fmt.Errorf("load: batch read: %w", err)
	}
	switch len(views) {
	case 0:
		return BatchView{}, false, nil
	case 1:
		return views[0], true, nil
	default:
		return BatchView{}, false, ErrAmbiguousBatch
	}
}

// batchClaim is a held claim on one batch id: the load_batch row is the
// lock two concurrent submissions of one id contend on, held from the
// claim to the finish inside one open transaction, so the loser waits
// for the winner's record instead of writing alongside it. Finish
// commits the transaction; every error path rolls it back, which for an
// uncommitted claim erases the row entirely, so a crashed submission
// leaves nothing for the next one to trip over.
type batchClaim struct {
	tx        pgx.Tx
	batchUUID string
}

// claimBatch claims one batch id and answers one of three ways: a held
// claim (this submission is the winner and runs its items), a replay
// (the id is already recorded; the recorded outcomes come back and the
// submission runs nothing), or an error. The INSERT's unique index is
// the arbitration: a concurrent claimant waits inside its own INSERT
// until the holder's finish commits, then takes the conflict arm, reads
// the recorded row FOR UPDATE, and replays it. A kind mismatch refuses
// as the door collision it is, winner or loser alike.
func (s *Store) claimBatch(ctx context.Context, in Batch, kind string) (claim *batchClaim, replay *BatchResult, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("load: batch claim begin: %w", err)
	}
	claim = &batchClaim{tx: tx}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
			claim = nil
		}
	}()
	var batchUUID string
	insertErr := tx.QueryRow(ctx, `
		INSERT INTO load_batch (org_id, source_system, batch_id, caller, kind, state)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
		ON CONFLICT (org_id, source_system, batch_id) DO NOTHING
		RETURNING id::text`,
		in.OrgID, in.SourceSystem, in.BatchID, in.Caller, kind, BatchStateRecorded).Scan(&batchUUID)
	if insertErr == nil {
		claim.batchUUID = batchUUID
		return claim, nil, nil
	}
	if !errors.Is(insertErr, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("load: batch claim: %w", insertErr)
	}
	// The conflict arm: the id is claimed or recorded. The row lock waits
	// for a holder's finish; a recorded row is already unlocked, so this
	// answers at once.
	var recordedKind string
	var countsJSON, itemsJSON []byte
	if err := tx.QueryRow(ctx, `
		SELECT id::text, kind, counts::text, items::text FROM load_batch
		WHERE org_id = $1::uuid AND source_system = $2 AND batch_id = $3
		FOR UPDATE`,
		in.OrgID, in.SourceSystem, in.BatchID).Scan(&batchUUID, &recordedKind, &countsJSON, &itemsJSON); err != nil {
		return nil, nil, fmt.Errorf("load: batch claim read: %w", err)
	}
	if recordedKind != kind {
		return nil, nil, ErrBatchDoorMismatch
	}
	r := BatchResult{BatchID: in.BatchID}
	if err := json.Unmarshal(countsJSON, &r.Counts); err != nil {
		return nil, nil, fmt.Errorf("load: batch counts decode: %w", err)
	}
	if err := json.Unmarshal(itemsJSON, &r.Items); err != nil {
		return nil, nil, fmt.Errorf("load: batch items decode: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("load: batch claim commit: %w", err)
	}
	return nil, &r, nil
}

// finishBatch lands the batch's record on the claim's transaction and
// commits the claim: the counts by outcome, the per item outcomes, the
// state, and the batch's one audit row, all in the one transaction the
// item writes stood beside. The record and the audit row commit or roll
// back together, and the commit releases the lock a waiting same-id
// submission reads its replay through.
func (s *Store) finishBatch(ctx context.Context, claim *batchClaim, in Batch, kind string, r BatchResult, sink BatchAuditSink) error {
	countsJSON, err := json.Marshal(r.Counts)
	if err != nil {
		return fmt.Errorf("load: batch counts encode: %w", err)
	}
	itemsJSON, err := json.Marshal(r.Items)
	if err != nil {
		return fmt.Errorf("load: batch items encode: %w", err)
	}
	if _, err := claim.tx.Exec(ctx, `
		UPDATE load_batch SET caller = $2, counts = $3::jsonb, items = $4::jsonb, updated_at = now()
		WHERE id = $1::uuid`,
		claim.batchUUID, in.Caller, countsJSON, itemsJSON); err != nil {
		return fmt.Errorf("load: batch record: %w", err)
	}
	if err := sink.RecordBatchTx(ctx, claim.tx, BatchAuditRow{
		BatchUUID:    claim.batchUUID,
		BatchID:      in.BatchID,
		Kind:         kind,
		SourceSystem: in.SourceSystem,
		ActorID:      in.Caller,
		Counts:       r.Counts,
	}); err != nil {
		return fmt.Errorf("load: batch audit: %w", err)
	}
	if err := claim.tx.Commit(ctx); err != nil {
		return fmt.Errorf("load: batch record commit: %w", err)
	}
	return nil
}

// readName reads one registry name under the transaction's lock
// (SELECT FOR UPDATE on the row; the row's absence is the create arm,
// where the name's unique index is the lock). found false is a clean
// not-recorded-yet, never an error.
func readName(ctx context.Context, tx pgx.Tx, k nameKey) (hhID, contentHash string, found bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT hh_id::text, content_hash FROM load_key_registry
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = $4
		FOR UPDATE`,
		k.OrgID, k.SourceSystem, k.Kind, k.LegacyID).Scan(&hhID, &contentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("load: name read: %w", err)
	}
	return hhID, contentHash, true, nil
}

// readNameOwner names the legacy id a row already carries, the
// reverse-uniqueness check's read.
func readNameOwner(ctx context.Context, tx pgx.Tx, k nameKey, hhID string) (string, bool, error) {
	var other string
	err := tx.QueryRow(ctx, `
		SELECT legacy_id FROM load_key_registry
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND hh_id = $4::uuid`,
		k.OrgID, k.SourceSystem, k.Kind, hhID).Scan(&other)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load: name owner read: %w", err)
	}
	return other, true, nil
}

// rowNamedBySource reports whether any source system of this org already
// names the row under the kind: the wording read behind a conflict whose
// own source system names no owner.
func rowNamedBySource(ctx context.Context, tx pgx.Tx, k nameKey, hhID string) (bool, error) {
	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM load_key_registry
		WHERE org_id = $1::uuid AND kind = $2 AND hh_id = $3::uuid`,
		k.OrgID, k.Kind, hhID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("load: name source read: %w", err)
	}
	return n > 0, nil
}

// insertName records the name: the registry insert is the load's lock.
// A unique violation comes back unwrapped so the caller can retry the
// item the race belongs to.
func insertName(ctx context.Context, tx pgx.Tx, k nameKey, hhID, contentHash, lastBatchID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO load_key_registry
			(org_id, source_system, kind, legacy_id, hh_id, content_hash, last_batch_id)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6, $7)`,
		k.OrgID, k.SourceSystem, k.Kind, k.LegacyID, hhID, contentHash, lastBatchID); err != nil {
		return err
	}
	return nil
}

// stampName moves a name's recorded hash and batch id after an update
// arm, or records the batch on an unchanged arm. The updated_at touch
// rides the statement.
func stampName(ctx context.Context, tx pgx.Tx, k nameKey, contentHash, lastBatchID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE load_key_registry SET
			content_hash = coalesce(nullif($5, ''), content_hash),
			last_batch_id = $6,
			updated_at = now()
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = $4`,
		k.OrgID, k.SourceSystem, k.Kind, k.LegacyID, contentHash, lastBatchID); err != nil {
		return fmt.Errorf("load: name stamp: %w", err)
	}
	return nil
}

// isUniqueViolation reports a Postgres unique violation, the race
// arbiter's answer.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isDeadlock reports a Postgres deadlock victim (SQLSTATE 40P01): two
// in-flight batches holding overlapping name locks, one rolled back
// whole. Under the savepoint protocol the victim's claim dies with its
// items, nothing partial survives, and the error maps to
// ErrBatchContended at the batch boundary.
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// validDerivedKey reports whether a derived key is either empty (the
// row's idempotency column stays untouched) or a canonical uuid, the
// shape NULLIF($,”)::uuid casts clean. The pricing module's boundary
// guard, restated local to this package.
func validDerivedKey(s string) bool {
	if s == "" {
		return true
	}
	return canonicalUUID(s)
}

// canonicalUUID reports whether s is a canonical 36 character uuid. The
// pricing module's boundary guard, restated local to this package.
func canonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}
