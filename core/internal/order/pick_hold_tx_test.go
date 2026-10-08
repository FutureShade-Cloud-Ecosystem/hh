package order

// pick_hold_tx_test.go, item 9 Opus P3-2: the hold read rides the guarded
// write's own transaction, under the order row lock, instead of a pool read
// taken before it. A payment reversal landing between the read and the write
// could let one move through; inside the write's lock the read and the move
// are one act for every money route that locks the order row.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeTx is the transaction handle the fake checked write hands its precheck:
// the interface embedded unimplemented, because the hold read only carries it.
type fakeTx struct{ pgx.Tx }

// txRecordingPickHold answers the facts through BOTH halves and records which
// half answered, the proof the read joined the write's transaction.
type txRecordingPickHold struct {
	facts     PickHoldFacts
	err       error
	poolReads int
	txReads   int
}

func (f *txRecordingPickHold) PickHoldFacts(context.Context, string) (PickHoldFacts, error) {
	f.poolReads++
	return f.facts, f.err
}

func (f *txRecordingPickHold) PickHoldFactsTx(_ context.Context, tx pgx.Tx, _ string) (PickHoldFacts, error) {
	if tx == nil {
		f.poolReads++
	} else {
		f.txReads++
	}
	return f.facts, f.err
}

// A guarded move into the hold's targets reads the hold through the Tx half,
// inside the write's transaction (the pool half stays unread).
func TestGuardedMoves_ReadTheHoldInsideTheWrite(t *testing.T) {
	for _, target := range []string{"PICKING", "READY", "FULFILLED", "INVOICED"} {
		reader := &txRecordingPickHold{facts: PickHoldFacts{Held: false}}
		store := pickHoldStore()
		svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(reader)
		if err := svc.AdvanceFromEvent(pickHoldCtx(), "ord-1", target, "evt-tx-"+target); err != nil {
			t.Fatalf("a clear order's advance to %s: %v", target, err)
		}
		if reader.txReads != 1 || reader.poolReads != 0 {
			t.Fatalf("the %s move read the hold %d times on the tx and %d on the pool, want 1 and 0",
				target, reader.txReads, reader.poolReads)
		}
	}
}

// The choke point's own guard rides the checked write's transaction too.
func TestSetStatus_ReadsTheHoldInsideTheWrite(t *testing.T) {
	reader := &txRecordingPickHold{facts: PickHoldFacts{Held: false}}
	store := pickHoldStore()
	svc := NewService(store).WithPickHoldReader(reader)
	if err := svc.SetStatus(pickHoldCtx(), "", "u-1", "ord-1", "PICKING", ""); err != nil {
		t.Fatalf("a clear order's move: %v", err)
	}
	if reader.txReads != 1 || reader.poolReads != 0 {
		t.Fatalf("SetStatus read the hold %d times on the tx and %d on the pool, want 1 and 0",
			reader.txReads, reader.poolReads)
	}
}

// A refusal inside the write still refuses the move (the fake store's checked
// write rolls the status back, as the real locked write does).
func TestGuardedMove_HeldRefusalInsideTheWriteRefuses(t *testing.T) {
	reader := &txRecordingPickHold{facts: heldFacts(1)}
	store := pickHoldStore()
	svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(reader)
	if _, err := svc.TransitionManual(pickHoldCtx(), "ord-1", ActionPicking, ""); !errors.Is(err, ErrHeldFromPicking) {
		t.Fatalf("want ErrHeldFromPicking, got %v", err)
	}
	if reader.txReads != 1 {
		t.Fatalf("the refusal read the hold %d times on the tx, want 1", reader.txReads)
	}
}

// The yard pick's hold read rides its writer's transaction as well: the
// fake's ApplyYardPicksTx hands the precheck the transaction, so the pick
// consults the Tx half under the row lock the writer takes.
func TestApplyYardPicks_ReadsTheHoldInsideTheWrite(t *testing.T) {
	reader := &txRecordingPickHold{facts: PickHoldFacts{Held: false}}
	store := pickHoldStore()
	// The yard door keys by UUID (its own shape check), so this leg's fixture
	// id is one, the existing 9.3 leg's shape.
	store.details["7c191000-0000-4000-9000-00000000000a"] = store.details["ord-1"]
	svc := NewService(store).WithPickHoldReader(reader)
	if _, err := svc.ApplyYardPicks(pickHoldCtx(), YardPickInput{
		OrderID: "7c191000-0000-4000-9000-00000000000a", Lines: []YardPickLineInput{{LineNo: 1, QtyPicked: "1"}},
	}); err != nil {
		t.Fatalf("a clear order's yard pick: %v", err)
	}
	if reader.txReads != 1 || reader.poolReads != 0 {
		t.Fatalf("the yard pick read the hold %d times on the tx and %d on the pool, want 1 and 0",
			reader.txReads, reader.poolReads)
	}
}

// A reader with only the pool half keeps working (the additive lane: the
// read falls back to the pool when no Tx capability is offered).
func TestGuardedMove_PoolOnlyReaderStillReads(t *testing.T) {
	reader := &fakePickHold{facts: PickHoldFacts{Held: false}}
	store := pickHoldStore()
	svc := NewService(store).withCaller(testCallerFrom).WithPickHoldReader(reader)
	if err := svc.AdvanceFromEvent(pickHoldCtx(), "ord-1", "PICKING", "evt-pool"); err != nil {
		t.Fatalf("a clear order's advance: %v", err)
	}
}
