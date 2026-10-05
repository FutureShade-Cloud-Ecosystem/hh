package load

// door_money.go is the money doors' batch protocol (HH-LOAD-1, the
// plan's reading 14): money is all or nothing per batch. The master
// doors answer per item, so one bad record stops no other; a money door
// refuses whole, because a money batch is one fact: the dealer's open
// receivables as one extract, landed to the cent or not at all. A
// half-landed money batch is worse than a refused one: the receivables
// ledger would tie to nothing and the aging would understate accounts
// that look current.
//
// The protocol shares the master doors' claim, savepoints and audit
// ruling and differs in exactly two places: a refused item rolls the
// whole claim back instead of just its savepoint, and a replayed batch
// id must arrive faithful (the same items it first recorded) or the
// replay refuses. The faithfulness rule is the money doors' own: the
// master doors answer a played batch id with its first outcomes and
// never compare content, which is right for a re-run extract and wrong
// for money, where a caller that changed an item under a played id
// believes its correction landed and the replay would answer it with
// outcomes that name the original items. The refusal names the item
// that diverged; nothing is written either way.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// UpsertBatchAtomic runs one money batch: the replayed-batch-id check
// first (a faithful replay returns what it first recorded and touches
// nothing, a divergent one refuses), then the items in order as
// savepoints on the claim's own transaction, and the first refused item
// rolls the claim back whole, because money lands all or nothing. An
// infrastructure failure or a Postgres deadlock rolls the claim back
// whole the same way; the deadlock maps to ErrBatchContended at this
// boundary, the answer a retryable batch level status carries, and
// resubmission is the caller's convergence: the id was never recorded,
// so the resubmission re-runs from a clean state.
func (d *Door) UpsertBatchAtomic(ctx context.Context, in Batch) (BatchResult, error) {
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
	claim, replay, err := d.store.claimBatch(ctx, in, d.kind)
	if err != nil {
		return BatchResult{}, err
	}
	if replay != nil {
		if err := d.assertFaithfulReplay(ctx, in); err != nil {
			return BatchResult{}, err
		}
		return *replay, nil
	}
	result := BatchResult{BatchID: in.BatchID, Counts: map[string]int{
		OutcomeCreated: 0, OutcomeUpdated: 0, OutcomeUnchanged: 0, OutcomeRefused: 0,
	}, Items: make([]Outcome, 0, len(in.Items))}
	for _, item := range in.Items {
		o, err := d.processItem(ctx, claim.tx, in, item)
		if err != nil {
			_ = claim.tx.Rollback(ctx)
			if isDeadlock(err) {
				return BatchResult{}, fmt.Errorf("%w: %v", ErrBatchContended, err)
			}
			return BatchResult{}, err
		}
		// The all-or-nothing arm: the refusing item's savepoint rolled
		// back with its own outcome, and the whole claim follows it, so
		// no earlier item of this batch survives its refusal.
		if o.Status == OutcomeRefused {
			_ = claim.tx.Rollback(ctx)
			return BatchResult{}, &BatchRefusal{Refusing: o}
		}
		result.Items = append(result.Items, o)
		result.Counts[o.Status]++
	}
	if err := d.finishMoneyBatch(ctx, claim, in, result); err != nil {
		_ = claim.tx.Rollback(ctx)
		return BatchResult{}, err
	}
	return result, nil
}

// recordedItem is one outcome as a money batch records it: the outcome
// the frame's readers know, with the item's content hash beside it, so
// a replay is graded against what this batch recorded and not against
// the registry's current stamp (which a later batch may have moved).
// The frame's readers decode the same JSON into plain outcomes and
// ignore the extra key.
type recordedItem struct {
	Outcome
	ContentHash string `json:"content_hash"`
}

// finishMoneyBatch is the frame's finish with the recorded outcomes
// carrying their content hashes: the counts, the items, the audit row
// and the commit, all on the claim's transaction, nothing through the
// pool.
func (d *Door) finishMoneyBatch(ctx context.Context, claim *batchClaim, in Batch, r BatchResult) error {
	hashes := make(map[string]string, len(in.Items))
	for _, it := range in.Items {
		hashes[it.LegacyID] = HashItem(it)
	}
	recorded := make([]recordedItem, 0, len(r.Items))
	for _, o := range r.Items {
		recorded = append(recorded, recordedItem{Outcome: o, ContentHash: hashes[o.LegacyID]})
	}
	countsJSON, err := json.Marshal(r.Counts)
	if err != nil {
		return fmt.Errorf("load: batch counts encode: %w", err)
	}
	itemsJSON, err := json.Marshal(recorded)
	if err != nil {
		return fmt.Errorf("load: batch items encode: %w", err)
	}
	if _, err := claim.tx.Exec(ctx, `
		UPDATE load_batch SET caller = $2, counts = $3::jsonb, items = $4::jsonb, updated_at = now()
		WHERE id = $1::uuid`,
		claim.batchUUID, in.Caller, countsJSON, itemsJSON); err != nil {
		return fmt.Errorf("load: batch record: %w", err)
	}
	if err := d.audit.RecordBatchTx(ctx, claim.tx, BatchAuditRow{
		BatchUUID:    claim.batchUUID,
		BatchID:      in.BatchID,
		Kind:         d.kind,
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

// assertFaithfulReplay grades a played batch id's resubmission against
// what that batch recorded: every submitted item must be an item the
// recorded batch carried, with the content hash it carried then. An
// item the batch never recorded, or whose hash differs, refuses naming
// the item, whatever a later batch has since done to the registry's
// stamp on that name. A batch recorded without hashes (nothing of this
// door's does) refuses as well rather than guess. This runs after the
// claim's transaction has committed: no transaction is open when it
// asks the pool.
func (d *Door) assertFaithfulReplay(ctx context.Context, in Batch) error {
	var itemsJSON []byte
	err := d.store.pool.QueryRow(ctx, `
		SELECT items::text FROM load_batch
		WHERE org_id = $1::uuid AND source_system = $2 AND batch_id = $3`,
		in.OrgID, in.SourceSystem, in.BatchID).Scan(&itemsJSON)
	if err != nil {
		return fmt.Errorf("load: replay check read: %w", err)
	}
	var recorded []recordedItem
	if err := json.Unmarshal(itemsJSON, &recorded); err != nil {
		return fmt.Errorf("load: replay check decode: %w", err)
	}
	byName := make(map[string]string, len(recorded))
	for _, r := range recorded {
		byName[r.LegacyID] = r.ContentHash
	}
	for _, item := range in.Items {
		hash := HashItem(item)
		if hash == "" {
			return &BatchRefusal{Refusing: refusedOutcome(item, &Refusal{
				Code: ReasonRecordShape, Field: "record",
				Detail: "the replayed batch carries an item that does not normalise to a content hash"})}
		}
		recordedHash, ok := byName[item.LegacyID]
		if !ok {
			return &BatchRefusal{Refusing: refusedOutcome(item, &Refusal{
				Code: ReasonReplayChanged, Field: "legacy_id",
				Detail: fmt.Sprintf("the replayed batch carries an item it never recorded: legacy key %s", item.LegacyID)})}
		}
		if recordedHash != hash {
			return &BatchRefusal{Refusing: refusedOutcome(item, &Refusal{
				Code: ReasonReplayChanged, Field: "legacy_id",
				Detail: fmt.Sprintf("the replayed batch carries a changed item under a played batch id: legacy key %s", item.LegacyID)})}
		}
	}
	return nil
}
