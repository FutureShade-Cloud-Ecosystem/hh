//go:build integration

// syncscope_create_body_gate_integration_test.go: UX-116, the invalid create
// body oracle, measured through the real engine and the real key ledger on
// the migrated and seeded database. The bound gate answered a hidden bound
// key before the store validated the create body, so an invalid body met the
// gate's row not found on a spent key and the validation refusal on a free
// one, and the words told the caller the key was spent. The ValidateCreate
// seam closes the oracle: the engine validates the body before the hidden
// bound refusal, and the two keys meet the same refusal, whole.
package messaging

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// TestUX116MessagesInvalidBodyAnswersTheValidationRefusal: the messages
// scope's create body validation (a nonempty body) under a spent key and a
// free one, for a member the room fence refuses.
func TestUX116MessagesInvalidBodyAnswersTheValidationRefusal(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}
	seatCaseyInTheMoneyRoom(t, f)

	boundKey := uuid.NewString()
	freeKey := uuid.NewString()
	body := map[string]any{"channel_id": chYardTrenton, "body": "ux116 create body probe"}
	invalid := map[string]any{"channel_id": chYardTrenton, "body": ""}
	t.Cleanup(func() { cleanProbeRows(t, f, boundKey, freeKey) })

	msgs, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages scope missing from the registry")
	}
	first := push(t, f.engine, msgs, gateSamFilter(), boundKey, "create", body)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the binder's create applies: %+v", first)
	}

	// The spent key with an invalid body meets the validation refusal, whole.
	spent := push(t, f.engine, msgs, gateCaseyFilter(), boundKey, "create", invalid)
	if spent.Status != "rejected" || spent.Error == nil ||
		*spent.Error != "sync: operation refused: message body is empty" {
		t.Errorf("the invalid body under the spent key answers %+v err %v, want the validation refusal", spent, spent.Error)
	}
	if spent.Seq != nil || spent.Conflict != nil {
		t.Errorf("the validation refusal carries a seq or an envelope: %+v", spent)
	}

	// The free key with the same invalid body meets the same refusal, whole.
	free := push(t, f.engine, msgs, gateCaseyFilter(), freeKey, "create", invalid)
	if free.Status != spent.Status || free.Seq != spent.Seq || free.Conflict != spent.Conflict {
		t.Errorf("the free key answers %+v, want the same result as the spent key %+v", free, spent)
	}
	if free.Error == nil || *free.Error != *spent.Error {
		t.Errorf("the free key answers error %v, want %q", free.Error, strOf(spent.Error))
	}

	// A valid body is unchanged: the gate's own refusal, the seam's plain
	// words, no seq, no envelope.
	valid := push(t, f.engine, msgs, gateCaseyFilter(), boundKey, "create", body)
	if valid.Status != "rejected" || valid.Error == nil || *valid.Error != sync.ErrRowNotFound.Error() {
		t.Errorf("a valid body under the spent key answers %+v err %v, want the gate's refusal", valid, valid.Error)
	}
	if n := ledgerRowsFor(t, f, boundKey, freeKey); n != 1 {
		t.Errorf("the refused bodies bind nothing new: %d ledger rows, want 1", n)
	}
}

// TestUX116AcksInvalidMethodAnswersTheValidationRefusal: the acks scope's
// create body validation (the method vocabulary) under a spent key and a
// free one.
func TestUX116AcksInvalidMethodAnswersTheValidationRefusal(t *testing.T) {
	f := newGateFixture(t)
	ctx := context.Background()
	if err := f.repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync sam: %v", err)
	}

	boundKey := uuid.NewString()
	freeKey := uuid.NewString()
	body := map[string]any{"channel_id": chYardTrenton, "body": "ux116 ack probe"}
	t.Cleanup(func() {
		cleanProbeRows(t, f, boundKey, freeKey)
		if _, err := f.pool.Exec(ctx, `DELETE FROM msg_acks WHERE idempotency_key = ANY($1::uuid[])`,
			[]string{boundKey, freeKey}); err != nil {
			t.Fatalf("clean probe acks: %v", err)
		}
	})

	msgs, ok := sync.Lookup("messages")
	if !ok {
		t.Fatal("messages scope missing from the registry")
	}
	acks, ok := sync.Lookup("acks")
	if !ok {
		t.Fatal("acks scope missing from the registry")
	}
	first := push(t, f.engine, msgs, gateSamFilter(), boundKey, "create", body)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("setup: the binder's post applies: %+v", first)
	}
	postID, _ := appliedRow(t, f.engine, msgs, gateSamFilter(), *first.Seq)

	ack := map[string]any{"message_id": postID, "method": "TAP"}
	if res := push(t, f.engine, acks, gateSamFilter(), boundKey, "create", ack); res.Status != "applied" || res.Seq == nil {
		t.Fatalf("setup: the binder's ack applies: %+v", res)
	}
	invalid := map[string]any{"message_id": postID, "method": "WAVE"}

	// The acks scope has no room fence: the gate hides Sam's ack from Casey
	// through the existence read alone, and the invalid body must still meet
	// the validation refusal under the spent key.
	spent := push(t, f.engine, acks, gateCaseyFilter(), boundKey, "create", invalid)
	if spent.Status != "rejected" || spent.Error == nil ||
		*spent.Error != "sync: operation refused: ack method must be one of TAP, QUICK_REPLY, VOICE" {
		t.Errorf("the invalid method under the spent key answers %+v err %v, want the validation refusal", spent, spent.Error)
	}
	free := push(t, f.engine, acks, gateCaseyFilter(), freeKey, "create", invalid)
	if free.Status != spent.Status || free.Seq != spent.Seq || free.Conflict != spent.Conflict {
		t.Errorf("the free key answers %+v, want the same result as the spent key %+v", free, spent)
	}
	if free.Error == nil || *free.Error != *spent.Error {
		t.Errorf("the free key answers error %v, want %q", free.Error, strOf(spent.Error))
	}

	// A valid method is unchanged: the gate's own refusal.
	valid := push(t, f.engine, acks, gateCaseyFilter(), boundKey, "create", ack)
	if valid.Status != "rejected" || valid.Error == nil || *valid.Error != sync.ErrRowNotFound.Error() {
		t.Errorf("a valid method under the spent key answers %+v err %v, want the gate's refusal", valid, valid.Error)
	}
}

func strOf(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
