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

// replayBatch looks up what a batch id first recorded: the outcomes ride
// in the batch row, so a replay answers from the record and touches
// nothing. The door's kind is part of the lookup's meaning: a batch id
// recorded under another door's kind is not a replay but a collision of
// two doors' batch namespaces, and it refuses rather than answering with
// the other door's outcomes.
func (s *Store) replayBatch(ctx context.Context, orgID, sourceSystem, batchID, kind string) (BatchResult, bool, error) {
	var countsJSON, itemsJSON []byte
	var recordedKind string
	err := s.pool.QueryRow(ctx, `
		SELECT kind, counts::text, items::text FROM load_batch
		WHERE org_id = $1::uuid AND source_system = $2 AND batch_id = $3`,
		orgID, sourceSystem, batchID).Scan(&recordedKind, &countsJSON, &itemsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return BatchResult{}, false, nil
	}
	if err != nil {
		return BatchResult{}, false, fmt.Errorf("load: batch replay read: %w", err)
	}
	if recordedKind != kind {
		return BatchResult{}, false, ErrBatchDoorMismatch
	}
	r := BatchResult{BatchID: batchID}
	if err := json.Unmarshal(countsJSON, &r.Counts); err != nil {
		return BatchResult{}, false, fmt.Errorf("load: batch counts decode: %w", err)
	}
	if err := json.Unmarshal(itemsJSON, &r.Items); err != nil {
		return BatchResult{}, false, fmt.Errorf("load: batch items decode: %w", err)
	}
	return r, true, nil
}

// recordBatch lands the batch row with the counts by outcome, the per
// item outcomes and the state, and the batch's one audit row on the same
// transaction (the audit ruling: the row commits or rolls back with the
// record it describes). A concurrent same-id submission loses the row
// write and keeps its computed answer; the first record stands, the
// plan's replay rule read sequentially.
func (s *Store) recordBatch(ctx context.Context, in Batch, kind string, r BatchResult, sink BatchAuditSink) error {
	countsJSON, err := json.Marshal(r.Counts)
	if err != nil {
		return fmt.Errorf("load: batch counts encode: %w", err)
	}
	itemsJSON, err := json.Marshal(r.Items)
	if err != nil {
		return fmt.Errorf("load: batch items encode: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("load: batch record begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var batchUUID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO load_batch (org_id, source_system, batch_id, caller, kind, state, counts, items)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb)
		ON CONFLICT (org_id, source_system, batch_id) DO NOTHING
		RETURNING id::text`,
		in.OrgID, in.SourceSystem, in.BatchID, in.Caller, kind, BatchStateRecorded, countsJSON, itemsJSON).Scan(&batchUUID); err != nil {
		return fmt.Errorf("load: batch record: %w", err)
	}
	if err := sink.RecordBatchTx(ctx, tx, BatchAuditRow{
		BatchUUID:    batchUUID,
		BatchID:      in.BatchID,
		Kind:         kind,
		SourceSystem: in.SourceSystem,
		ActorID:      in.Caller,
		Counts:       r.Counts,
	}); err != nil {
		return fmt.Errorf("load: batch audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
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

// validDerivedKey reports whether a derived key is either empty (the
// row's idempotency column stays untouched) or a canonical uuid, the
// shape NULLIF($,”)::uuid casts clean. The pricing module's boundary
// guard, restated local to this package.
func validDerivedKey(s string) bool {
	if s == "" {
		return true
	}
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
