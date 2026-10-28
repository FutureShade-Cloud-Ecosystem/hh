//go:build integration

package payments_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestApplyReceived_TheSeenLegLeavesTheGateAsItFoundIt is the gate rule the SEEN leg owes the
// shared database: a leg plants its rows, proves what it planted, and takes every one of them
// back, so the next package's leg reads the gate as this leg found it.
//
// The SEEN leg posts two journals on its way (the invoice's own and the payment's) and both are
// the leg's rows: a journal is written before the leg's last read and read again after, so it
// cannot be taken back before the leg is done with it. A journal left on the shared gate is a
// POSTED line no later package can decide or reverse, and gl's DemoSeed leg reads the invoice's
// as a residue on the retired accounts.
//
// The journal is read through its own source row: a journal names the invoice it finalised or
// the payment it applied, and the leg takes those rows back. A journal whose own row is gone is
// the leg's leftover. The journals the leg's drain posted for the seed's own pending events are
// not the leg's rows and their rows are still there, so they are not counted.
//
// The leg runs here as a subtest, so the journals read are the leg's own: the gate's journals
// before it and the gate's journals after it.
func TestApplyReceived_TheSeenLegLeavesTheGateAsItFoundIt(t *testing.T) {
	pool := journalPool(t)
	before := glJournalIDs(t, pool)
	t.Run("the seen leg", TestApplyReceived_AnEventThatNeverBookedShowsInTheARTieOut)
	for id := range glJournalIDs(t, pool) {
		if before[id] {
			continue
		}
		var source string
		var ref *string
		if err := pool.QueryRow(context.Background(),
			`SELECT source, source_ref_id::text FROM gl_journals WHERE id = $1::uuid`, id).
			Scan(&source, &ref); err != nil {
			t.Fatalf("read the journal %s: %v", id, err)
		}
		if ref == nil {
			continue
		}
		var alive int
		switch source {
		case "INVOICE":
			if err := pool.QueryRow(context.Background(),
				`SELECT count(*) FROM invoices WHERE id = $1::uuid`, *ref).Scan(&alive); err != nil {
				t.Fatalf("read the invoice %s: %v", *ref, err)
			}
		case "PAYMENT":
			if err := pool.QueryRow(context.Background(),
				`SELECT count(*) FROM payments WHERE id = $1::uuid`, *ref).Scan(&alive); err != nil {
				t.Fatalf("read the payment %s: %v", *ref, err)
			}
		default:
			continue
		}
		if alive == 0 {
			t.Errorf("the SEEN leg left the journal %s behind: the %s row it names (%s) is gone",
				id, source, *ref)
		}
	}
}

// glJournalIDs reads the gate's journals by id.
func glJournalIDs(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id::text FROM gl_journals`)
	if err != nil {
		t.Fatalf("read the gate's journals: %v", err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("read a journal id: %v", err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the gate's journals: %v", err)
	}
	return ids
}
