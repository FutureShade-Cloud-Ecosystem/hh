//go:build integration

// lockorder_batch_integration_test.go: the two BATCH status writers beside a reversal of one of their members.
//
// Since the sync seq trigger (0422) a status UPDATE on payments takes the payments counter on the first row it
// moves and holds it while it locks the next row. A reversal of a later member holds that payment's row
// (FOR UPDATE) and waits for the counter at its reversing insert: each waits on the other. Which member closes
// the cycle follows the order the UPDATE visits the rows, so the leg reverses each member in turn. The bank
// claim (a card batch deposit claims several payments against one statement line) and till's MarkDeposited both
// take every named payment row in ascending id order, FOR NO KEY UPDATE, before the UPDATE: then the batch holds
// no counter while it waits on a row, and a reversal waits on a row holding nothing.
//
// The batch is the first writer, parked in the rig's widened window right after its first counter touch.
package payments_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// condBatchParked: a batch writer whose payments counter draw is the trigger's shows its UPDATE as the query.
const condBatchParked = `wait_event = 'PgSleep' AND query LIKE '%UPDATE payments%'`

func (r *loRig) bwReverse(paymentID string) error {
	_, err := r.repo.ReversePayment(context.Background(), payments.ReverseInput{
		PaymentID: paymentID, Reason: "batch leg", ActorID: r.userID, IsManager: true}, time.Now())
	return err
}

func (r *loRig) bwStatementLine(cents int64) string {
	r.t.Helper()
	ctx := context.Background()
	var acct string
	if err := r.obs.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&acct); err != nil {
		r.t.Skipf("no seeded bank account: %v", err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	ref := "LOB-" + r.suffix
	var id string
	if err := r.obs.QueryRow(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1, $2, 'LOCK ORDER BATCH', $3, $4, $5, 'UNMATCHED') RETURNING id::text`,
		acct, day, ref, cents, bankrec.DedupeHash(day, cents, "LOCK ORDER BATCH", ref)).Scan(&id); err != nil {
		r.t.Fatalf("seed statement line: %v", err)
	}
	r.t.Cleanup(func() {
		c := context.Background()
		_, _ = r.obs.Exec(c, `DELETE FROM bank_matches WHERE statement_line_id = $1::uuid`, id)
		_, _ = r.obs.Exec(c, `DELETE FROM bank_statement_lines WHERE id = $1::uuid`, id)
	})
	return id
}

func TestIntegration_LockOrder_BatchStatusWritersBesideAReversalOfAMember(t *testing.T) {
	batches := map[string]func(r *loRig, ids []string) error{
		"claim": func(r *loRig, ids []string) error {
			line := r.bwStatementLine(20_000)
			_, err := bankrec.NewRepository(r.pool).PersistMatch(context.Background(), bankrec.Match{
				StatementLineID: line, TargetType: bankrec.TargetPayment, TargetRefID: ids[0],
				Method: bankrec.MethodManual, AmountCents: 20_000}, ids)
			return err
		},
		"MarkDeposited": func(r *loRig, ids []string) error {
			return till.NewRepository(r.pool).MarkDeposited(context.Background(), ids, "lo-batch-line")
		},
	}
	for name, batch := range batches {
		for _, member := range []string{"lower id", "higher id"} {
			t.Run(name+" beside a reversal of the "+member, func(t *testing.T) {
				loPoolSizes(t, func(t *testing.T, size int32) {
					r := newLORig(t, size)
					a := r.payOnce(r.invoice("BA", 1_000_000), 10_000)
					b := r.payOnce(r.invoice("BB", 1_000_000), 10_000)
					ids := []string{a.ID, b.ID}
					sort.Strings(ids)
					target := ids[0]
					if member == "higher id" {
						target = ids[1]
					}
					r.armSeqWindow("payments")
					batchErr := make(chan error, 1)
					go func() { batchErr <- batch(r, ids) }()
					r.waitSessions(name+" parked holding the payments counter after its first row", 1, condBatchParked)
					revErr := make(chan error, 1)
					go func() { revErr <- r.bwReverse(target) }()
					r.waitSessions("the reversal waiting", 1, condLockWaiter)
					be, re := loAnswer(t, name, batchErr), loAnswer(t, "reversal", revErr)
					t.Logf("%s: %v | reversal: %v", name, be, re)
					if loIsDeadlock(be) || loIsDeadlock(re) {
						t.Fatalf("%s beside a reversal of the %s deadlocked: %s %v | reversal %v", name, member, name, be, re)
					}
					if re != nil {
						t.Fatalf("the reversal failed: %v", re)
					}
					_ = fmt.Sprint
				})
			})
		}
	}
}
