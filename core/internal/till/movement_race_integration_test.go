//go:build integration

package till

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// A movement beside a count of the same session (SF-2j, S3's finding): recordMovement used to read the session OPEN
// with a plain read and insert with no session lock, while SetCounted is a separate guarded UPDATE, so a movement
// could land on a session counted between the read and the insert. The movement is the parked session
// (testkit.NewInsertPark: a BEFORE INSERT trigger on till_movements, after the OPEN check and before the row
// exists); the count of the same session runs while it stands there.
//
// Red before the fix: the count commits and the movement then inserts onto a COUNTED session (its row's transaction
// is later than the count's). After: the movement takes the session row FOR NO KEY UPDATE before its OPEN check, so
// the count queues behind it (or the movement is refused when the count came first), and no movement is ever
// written after the count.
func TestIntegration_MovementBesideACountOfTheSameSession(t *testing.T) {
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	cctx := globalCtx(userID)
	svc := realSvc(pool)
	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "RACE-MV", FloatCents: 0})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })

	park := testkit.NewInsertPark(t, pool, "till_movements", 55020001)
	move := make(chan error, 1)
	go func() {
		move <- realSvc(park.Pool).Lodge(cctx, MovementInput{TillSessionID: sid, Method: MethodCash, AmountCents: 1000, Reference: "race-slip"})
	}()
	park.WaitParked("the movement stands after its OPEN read, before its insert")

	var countDone atomic.Bool
	count := make(chan error, 1)
	go func() {
		defer countDone.Store(true)
		count <- svc.CountTill(cctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 0}}})
	}()
	park.WaitBlockedOr("the count waits behind the movement, or has already committed", countDone.Load)

	park.AnswersAllowing(map[string]chan error{"movement": move, "count": count}, map[string]string{"movement": "OPEN session"})

	var movementTx, countTx int64
	var status string
	var rows int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM till_movements WHERE till_session_id = $1::uuid`, sid).Scan(&rows)
	_ = pool.QueryRow(context.Background(), `SELECT status, xmin::text::bigint FROM till_sessions WHERE id = $1::uuid`, sid).Scan(&status, &countTx)
	if rows > 0 {
		_ = pool.QueryRow(context.Background(), `SELECT max(xmin::text::bigint) FROM till_movements WHERE till_session_id = $1::uuid`, sid).Scan(&movementTx)
		t.Logf("session %s (the count's transaction %d), %d movement row(s) (the movement's transaction %d)", status, countTx, rows, movementTx)
		if status != StatusOpen && movementTx > countTx {
			t.Errorf("a movement landed on a counted session: the movement's transaction %d is after the count's %d", movementTx, countTx)
		}
	} else {
		t.Logf("session %s, no movement written (the count came first and the movement was refused)", status)
	}
}
