package load

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Door is the one keyed upsert helper bound to one owning module's
// writer: the object one load door holds. The kind is the door's
// registry kind (the batch record's kind too); the writer is the owning
// module's, and the audit sink the platform's write path, both taken by
// constructor, and every row write they make joins a transaction this
// door opens.
type Door struct {
	store  *Store
	kind   string
	writer Writer
	audit  BatchAuditSink
}

// NewDoor binds the helper to one door's kind, writer and audit sink. A
// nil sink is a construction the door refuses at the first batch: every
// batch is audited, one row per batch, or the batch does not land.
func NewDoor(store *Store, kind string, w Writer, a BatchAuditSink) *Door {
	return &Door{store: store, kind: kind, writer: w, audit: a}
}

// maxNameRetries bounds the race retry: the registry insert is the lock,
// and the item that loses a name race re-runs once the winner's row is
// visible. Three tries is two lost races; a third contention is an
// error, never a silent give-up.
const maxNameRetries = 3

// UpsertBatch runs one batch: the replayed-batch-id check first (a
// replay returns what it first recorded and touches nothing), then the
// items in order, one transaction per item so one refused record stops
// no other (the master doors' per item semantics), then the batch
// record. Validation is a batch-level 400 at the boundary, before any
// store call.
func (d *Door) UpsertBatch(ctx context.Context, in Batch) (BatchResult, error) {
	if err := validateBatch(in); err != nil {
		return BatchResult{}, err
	}
	if d.store == nil || d.store.pool == nil {
		return BatchResult{}, errors.New("load: no database")
	}
	if d.writer == nil {
		return BatchResult{}, errors.New("load: the door carries no writer")
	}
	if d.audit == nil {
		return BatchResult{}, errors.New("load: the door carries no audit sink; every batch is audited")
	}
	replay, found, err := d.store.replayBatch(ctx, in.OrgID, in.SourceSystem, in.BatchID, d.kind)
	if err != nil {
		return BatchResult{}, err
	}
	if found {
		return replay, nil
	}
	result := BatchResult{BatchID: in.BatchID, Counts: map[string]int{
		OutcomeCreated: 0, OutcomeUpdated: 0, OutcomeUnchanged: 0, OutcomeRefused: 0,
	}, Items: make([]Outcome, 0, len(in.Items))}
	for _, item := range in.Items {
		o, err := d.processItem(ctx, in, item)
		if err != nil {
			return BatchResult{}, err
		}
		result.Items = append(result.Items, o)
		result.Counts[o.Status]++
	}
	if err := d.store.recordBatch(ctx, in, d.kind, result, d.audit); err != nil {
		return BatchResult{}, err
	}
	return result, nil
}

// validateBatch is the batch-level contract: the identity fields present
// and bounded, the item list inside the bound, every item named. The
// handler runs the same rules to map them to 400.
func validateBatch(in Batch) error {
	if in.OrgID == "" || in.SourceSystem == "" || in.BatchID == "" {
		return ErrMissingField
	}
	if len(in.SourceSystem) > 64 || len(in.BatchID) > 128 || len(in.Caller) > 128 {
		return ErrMissingField
	}
	if len(in.Items) == 0 || len(in.Items) > MaxBatchItems {
		return ErrBatchBound
	}
	for _, item := range in.Items {
		if item.LegacyID == "" || len(item.LegacyID) > MaxKeyLength {
			return ErrMissingField
		}
		if !validDerivedKey(item.DerivedKey) {
			return ErrMissingField
		}
		for _, r := range item.Requires {
			if r.Kind == "" || r.LegacyID == "" || len(r.LegacyID) > MaxKeyLength {
				return ErrMissingField
			}
		}
	}
	return nil
}

// processItem answers one item, retrying the whole item when the name
// race hands the loss over: the rolled-back attempt leaves no row, no
// draw and no record behind.
func (d *Door) processItem(ctx context.Context, in Batch, item Item) (Outcome, error) {
	hash := HashItem(item)
	if hash == "" {
		return refusedOutcome(item, &Refusal{Code: ReasonRecordShape, Field: "record",
			Detail: "the record does not normalise to a content hash"}), nil
	}
	for attempt := 0; ; attempt++ {
		o, retry, err := d.tryItem(ctx, in, item, hash)
		if err != nil {
			return Outcome{}, err
		}
		if !retry {
			return o, nil
		}
		if attempt >= maxNameRetries {
			return Outcome{}, fmt.Errorf("load: the registry is contended on legacy key %s", item.LegacyID)
		}
	}
}

// writerApply calls the owning module's writer and sorts its answers:
// a *Refusal error is the item's refusal, anything else is an
// infrastructure failure the whole batch answers with.
func writerApply(ctx context.Context, tx pgx.Tx, w Writer, in Write) (rowID string, refused *Refusal, err error) {
	id, err := w.Apply(ctx, tx, in)
	if err == nil {
		return id, nil, nil
	}
	var r *Refusal
	if errors.As(err, &r) {
		return "", r, nil
	}
	return "", nil, err
}

// tryItem is one attempt: one transaction, the name resolved under the
// lock, the write through the owning module's writer, the name recorded.
// The commit decision sits here so an unchanged answer that raced never
// commits its speculative write: the replayed identical record leaves no
// draw behind, exit line 3's second half.
func (d *Door) tryItem(ctx context.Context, in Batch, item Item, hash string) (o Outcome, retry bool, err error) {
	key := nameKey{OrgID: in.OrgID, SourceSystem: in.SourceSystem, Kind: d.kind, LegacyID: item.LegacyID}
	tx, err := d.store.pool.Begin(ctx)
	if err != nil {
		return Outcome{}, false, fmt.Errorf("load: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The references resolve first, before any write: a reference to a
	// name not yet loaded refuses the item naming the kind and the key.
	refs, refused, err := resolveRefs(ctx, tx, in, item)
	if err != nil {
		return Outcome{}, false, err
	}
	if refused != nil {
		return refusedOutcome(item, refused), false, nil
	}

	hhID, curHash, found, err := readName(ctx, tx, key)
	if err != nil {
		return Outcome{}, false, err
	}

	if found {
		if curHash == hash {
			// The replayed identical record: the name's batch stamp moves,
			// nothing else writes, no sequence is drawn.
			if err := stampName(ctx, tx, key, "", in.BatchID); err != nil {
				return Outcome{}, false, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Outcome{}, false, fmt.Errorf("load: commit: %w", err)
			}
			return Outcome{LegacyID: item.LegacyID, Status: OutcomeUnchanged, HHID: hhID}, false, nil
		}
		wrote, refused, err := writerApply(ctx, tx, d.writer, Write{
			OrgID: in.OrgID, RowID: hhID, DerivedKey: item.DerivedKey, Record: item.Record, Refs: refs,
		})
		if err != nil {
			return Outcome{}, false, err
		}
		if refused != nil {
			return refusedOutcome(item, refused), false, nil
		}
		if wrote != hhID {
			// The writer's update landed somewhere other than the row the
			// name records: refused, the write rolled back, the other
			// claimant named when the registry knows one.
			other, _, err := readNameOwner(ctx, tx, key, wrote)
			if err != nil {
				return Outcome{}, false, err
			}
			return refusedOutcome(item, &Refusal{Code: ReasonKeyConflict, Field: "legacy_id",
				Detail: conflictDetail(item.LegacyID, other)}), false, nil
		}
		if err := stampName(ctx, tx, key, hash, in.BatchID); err != nil {
			return Outcome{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Outcome{}, false, fmt.Errorf("load: commit: %w", err)
		}
		return Outcome{LegacyID: item.LegacyID, Status: OutcomeUpdated, HHID: hhID}, false, nil
	}

	// The create arm: the writer inserts (or attaches the row its own
	// natural key resolves to), then the name records. The writer's row
	// lock is where a name race waits; re-read the name after it, because
	// the race's winner may have committed while this transaction stood
	// in line, and the registry insert is the lock both contend on.
	rowID, refused, err := writerApply(ctx, tx, d.writer, Write{
		OrgID: in.OrgID, DerivedKey: item.DerivedKey, Record: item.Record, Refs: refs,
	})
	if err != nil {
		return Outcome{}, false, err
	}
	if refused != nil {
		return refusedOutcome(item, refused), false, nil
	}
	other, claimed, err := readNameOwner(ctx, tx, key, rowID)
	if err != nil {
		return Outcome{}, false, err
	}
	if claimed && other != item.LegacyID {
		// A row a second name would claim: refused naming both legacy ids,
		// the attach rolled back so nothing merges silently.
		return refusedOutcome(item, &Refusal{Code: ReasonKeyConflict, Field: "legacy_id",
			Detail: conflictDetail(item.LegacyID, other)}), false, nil
	}
	nameID, nameHash, named, err := readName(ctx, tx, key)
	if err != nil {
		return Outcome{}, false, err
	}
	if named {
		if nameID != rowID {
			// The name's row and this attempt's row differ. That is the
			// race's signature, not a conflict's: the winner committed
			// between this attempt's first name read and its writer call,
			// and the 0213 draw held this writer until then. The
			// speculative write rolls back and the retry takes the found
			// arm against committed state, where the hash decides. A
			// genuine second row claim is refused where it is detectable
			// directly: the reverse-uniqueness read above.
			return Outcome{}, true, nil
		}
		if nameHash == hash {
			// The race lost and the record agrees: the speculative write
			// rolls back, the answer is unchanged, no draw stands.
			return Outcome{LegacyID: item.LegacyID, Status: OutcomeUnchanged, HHID: rowID}, false, nil
		}
		// The race lost and the record differs: the row already carries
		// this attempt's write (the writer's natural-key upsert resolved
		// to the winner's row), so the recorded hash is the only thing
		// left to move.
		if err := stampName(ctx, tx, key, hash, in.BatchID); err != nil {
			return Outcome{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Outcome{}, false, fmt.Errorf("load: commit: %w", err)
		}
		return Outcome{LegacyID: item.LegacyID, Status: OutcomeUpdated, HHID: rowID}, false, nil
	}
	if err := insertName(ctx, tx, key, rowID, hash, in.BatchID); err != nil {
		if isUniqueViolation(err) {
			return Outcome{}, true, nil
		}
		return Outcome{}, false, fmt.Errorf("load: name insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Outcome{}, false, fmt.Errorf("load: commit: %w", err)
	}
	return Outcome{LegacyID: item.LegacyID, Status: OutcomeCreated, HHID: rowID}, false, nil
}

// resolveRefs resolves the item's references against the registry inside
// the item's transaction. A reference to a name not yet loaded refuses
// the item naming the kind and the key; the batch's other items still
// land.
func resolveRefs(ctx context.Context, tx pgx.Tx, in Batch, item Item) ([]RefMatch, *Refusal, error) {
	refs := make([]RefMatch, 0, len(item.Requires))
	for _, r := range item.Requires {
		var hh string
		err := tx.QueryRow(ctx, `
			SELECT hh_id::text FROM load_key_registry
			WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = $4`,
			in.OrgID, in.SourceSystem, r.Kind, r.LegacyID).Scan(&hh)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &Refusal{Code: ReasonMissingReference, Field: r.Kind,
				Detail: fmt.Sprintf("reference not loaded: kind %s legacy key %s", r.Kind, r.LegacyID)}, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("load: reference read: %w", err)
		}
		refs = append(refs, RefMatch{Kind: r.Kind, LegacyID: r.LegacyID, HHID: hh})
	}
	return refs, nil, nil
}

// conflictDetail names both legacy ids of a key conflict, keys only,
// never a record value.
func conflictDetail(incoming, recorded string) string {
	if recorded == "" || recorded == incoming {
		return fmt.Sprintf("legacy key %s would claim a second row", incoming)
	}
	return fmt.Sprintf("legacy key %s would claim the row legacy key %s already names", incoming, recorded)
}
