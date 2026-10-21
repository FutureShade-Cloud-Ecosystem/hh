//go:build integration

package till

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5"
)

// The other two doors onto a till session (SF-2j round 2, S3's P2-1): TakeTender's payment insert and RefundTender's
// cash PAID_OUT read the session OPEN with GetSessionTx inside the caller's transaction. With a plain read a count
// can finish between the read and the write, leaving a payment or a PAID_OUT on a COUNTED session (and a false
// "over" at close). The writer is the parked session (testkit.NewInsertPark: a BEFORE INSERT trigger on payments,
// after the OPEN read and before the row exists); the count of the same session runs while it stands there.
//
// Red before the fix: the count commits, then the write lands (its transaction is later than the count's). After:
// GetSessionTx reads the session FOR SHARE, which the count's UPDATE queues behind, so the write commits first or is
// refused as not OPEN.
func doorBesideACount(t *testing.T, door string, write func(ctx context.Context, svc *Service, tx txLike, sid string) error, writtenXmin string) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	cctx := globalCtx(userID)
	svc := realSvc(pool)
	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "RACE-" + door, FloatCents: 0})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	park := testkit.NewInsertPark(t, pool, "payments", 55020002)
	done := make(chan error, 1)
	go func() {
		ctx := cctx
		tx, berr := park.Pool.Begin(ctx)
		if berr != nil {
			done <- berr
			return
		}
		defer tx.Rollback(ctx)
		if werr := write(ctx, realSvc(park.Pool), tx, sid); werr != nil {
			done <- werr
			return
		}
		done <- tx.Commit(ctx)
	}()
	park.WaitParked("the " + door + " stands after its OPEN read, before its payment insert")

	var countDone atomic.Bool
	count := make(chan error, 1)
	go func() {
		defer countDone.Store(true)
		count <- svc.CountTill(cctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 0}}})
	}()
	park.WaitBlockedOr("the count waits behind the "+door+", or has already committed", countDone.Load)
	// The count finished while the writer still stood between its OPEN read and its insert: whatever the writer then
	// writes lands after the count. With the session row read FOR SHARE the count queues behind the writer instead.
	countFirst := countDone.Load()
	park.AnswersAllowing(map[string]chan error{door: done, "count": count}, map[string]string{door: "OPEN"})

	var status string
	var rows int
	var unused int64
	_ = pool.QueryRow(context.Background(), `SELECT status FROM till_sessions WHERE id = $1::uuid`, sid).Scan(&status)
	_ = pool.QueryRow(context.Background(), writtenXmin, sid).Scan(&rows, &unused)
	t.Logf("%s: session %s, the count finished first: %v, %d row(s) written", door, status, countFirst, rows)
	if rows > 0 && status != StatusOpen && countFirst {
		t.Errorf("a %s landed on a counted session: the count finished while it stood between its OPEN read and its insert, and it wrote afterwards", door)
	}
}

type txLike = pgx.Tx

func asPgxTx(tx txLike) pgx.Tx { return tx }

func TestIntegration_TenderBesideACountOfTheSameSession(t *testing.T) {
	doorBesideACount(t, "tender", func(ctx context.Context, svc *Service, tx txLike, sid string) error {
		_, err := svc.TakeTender(ctx, asPgxTx(tx), TenderInput{TillSessionID: sid, Method: MethodCash, AmountCents: 1200})
		return err
	}, `SELECT count(*), COALESCE(max(xmin::text::bigint), 0) FROM payments WHERE till_session_id = $1::uuid`)
}

func TestIntegration_CashRefundBesideACountOfTheSameSession(t *testing.T) {
	doorBesideACount(t, "refund", func(ctx context.Context, svc *Service, tx txLike, sid string) error {
		_, err := svc.RefundTender(ctx, asPgxTx(tx), RefundInput{TillSessionID: sid, Method: MethodCash, AmountCents: 700, Reference: "RETURN-RACE", IdempotencyKey: "race-refund-" + sid})
		return err
	}, `SELECT count(*), COALESCE(max(xmin::text::bigint), 0) FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = 'PAID_OUT'`)
}
