package till

// holds_caller_tx_test.go, the unit leg for the context-carried transaction seam on the credit hold
// read. HasActiveHold must run its credit_holds read on the CALLER's transaction when one is in flight
// on the context and keep the pool when none is (a nil tx rides nothing), the inventory store's
// nil-means-pool convention. The integration leg (holds_pool_cycle_integration_test.go) proves the
// pool cycle this seam closes; this one pins the read's routing itself, no database.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// stubHoldTx records that the read ran on it; it satisfies pgx.Tx by panic guard, the read may reach
// QueryRow and nothing else.
type stubHoldTx struct {
	pgx.Tx
	queries int
}

func (s *stubHoldTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	s.queries++
	return stubHoldRow{exists: true}
}

// stubHoldRow answers the one column HasActiveHold scans.
type stubHoldRow struct{ exists bool }

func (r stubHoldRow) Scan(dest ...any) error {
	b, ok := dest[0].(*bool)
	if !ok {
		return errors.New("stubHoldRow: want a *bool destination")
	}
	*b = r.exists
	return nil
}

func TestHasActiveHold_JoinsCallerTxInFlight(t *testing.T) {
	repo := &Repository{} // no pool at all: only a transaction-carried read can answer
	tx := &stubHoldTx{}

	held, err := repo.HasActiveHold(WithCallerTx(context.Background(), tx), "cust-1")
	if err != nil {
		t.Fatalf("HasActiveHold with a transaction in flight: %v", err)
	}
	if !held {
		t.Error("HasActiveHold = false, want the stub transaction's true")
	}
	if tx.queries != 1 {
		t.Errorf("the read ran %d queries on the caller's transaction, want exactly 1", tx.queries)
	}
}

func TestHasActiveHold_NoTxInFlightKeepsThePool(t *testing.T) {
	repo := &Repository{} // no pool: the pool path must answer errNoDB, never panic

	if _, err := repo.HasActiveHold(context.Background(), "cust-1"); !errors.Is(err, errNoDB) {
		t.Errorf("no transaction in flight, no pool: err = %v, want errNoDB", err)
	}
	// A nil tx stamps nothing onto the context, so this is the pool path too.
	if _, err := repo.HasActiveHold(WithCallerTx(context.Background(), nil), "cust-1"); !errors.Is(err, errNoDB) {
		t.Errorf("nil transaction in flight, no pool: err = %v, want errNoDB", err)
	}
}

func TestHasActiveHold_WalkInShortCircuitsBeforeAnyRead(t *testing.T) {
	repo := &Repository{} // no pool and no transaction: the empty customer check fires first

	held, err := repo.HasActiveHold(WithCallerTx(context.Background(), &stubHoldTx{}), "")
	if err != nil {
		t.Fatalf("HasActiveHold with an empty customer id: %v", err)
	}
	if held {
		t.Error("HasActiveHold = true for an empty customer id, want false")
	}
}
