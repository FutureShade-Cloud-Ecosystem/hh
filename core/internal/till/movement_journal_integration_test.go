//go:build integration

// movement_journal_integration_test.go: the FW1-13 movement journals, walked to the real GL.
//
// The brief's red leg: a paid out moves the drawer's expected cash today with no journal. A paid
// out is expense cash leaving the drawer, so its journal debits the expense account the posting
// rules hold and credits undeposited funds, through the outbox, exactly as the session close posts
// its over/short. A paid in is the mirror. A lodgement and a drop book nothing here on purpose:
// the lodgement's DR 1000 / CR 1020 is the bank match's journal (bankrec posts it when the
// statement line pairs the lodgement, matcher.go), and a drop's cash is still the dealer's
// undeposited cash, only out of the drawer. Posting the bank leg again at record time would count
// every matched deposit twice.
//
// The test runs in the external package so it may import gl (gl imports till for its payment
// reader; an in-package test would cycle). Run with DATABASE_URL on a migrated and seeded gate
// database, then drain the outbox through the real GL consumer.
package till_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

func mjPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// mjWorld is one session on one branch, cleaned up by exact id so the shared gate database stays tidy.
type mjWorld struct {
	t         *testing.T
	pool      *pgxpool.Pool
	svc       *till.Service
	sessionID string
	branchID  string
	userID    string
}

func newMJWorld(t *testing.T) *mjWorld {
	t.Helper()
	ctx := context.Background()
	pool := mjPool(t)
	var branchID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH location: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	svc := till.NewService(till.NewRepository(pool))
	station := "MJ" + fmt.Sprint(os.Getpid())
	id, err := svc.OpenTill(staffCtx(userID, branchID), till.OpenTillInput{BranchID: branchID, Station: station, FloatCents: 100_00})
	if err != nil {
		t.Fatalf("open the drawer: %v", err)
	}
	w := &mjWorld{t: t, pool: pool, svc: svc, sessionID: id, branchID: branchID, userID: userID}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key LIKE 'till.movement.posted:%')`)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key LIKE 'till.movement.posted:%'`)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'till.movement.posted'`)
		_, _ = pool.Exec(c, `DELETE FROM till_movements WHERE till_session_id = $1::uuid`, id)
		_, _ = pool.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, id)
	})
	return w
}

func staffCtx(userID, branchID string) context.Context {
	return staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
		UserID: userID, Tier: staffcontext.TierFrontline, Scope: staffcontext.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{branchID: true},
	})
}

// record posts one movement through the service and returns its row id.
func (w *mjWorld) record(movementType string, cents int64) string {
	w.t.Helper()
	in := till.MovementInput{TillSessionID: w.sessionID, MovementType: movementType, Method: "CASH", AmountCents: cents, Reason: "FW1-13 walk"}
	var err error
	switch movementType {
	case "PAID_IN":
		err = w.svc.PaidIn(staffCtx(w.userID, w.branchID), in)
	case "PAID_OUT":
		err = w.svc.PaidOut(staffCtx(w.userID, w.branchID), in)
	default:
		err = w.svc.Lodge(staffCtx(w.userID, w.branchID), in)
	}
	if err != nil {
		w.t.Fatalf("%s movement: %v", movementType, err)
	}
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = $2`, w.sessionID, movementType).Scan(&id); err != nil {
		w.t.Fatalf("find the %s row: %v", movementType, err)
	}
	return id
}

// drain runs the real outbox dispatcher with the real GL handlers until the outbox is empty.
func (w *mjWorld) drain() {
	w.t.Helper()
	glSvc := gl.NewService(gl.NewRepository(w.pool)).WithPaymentReader(till.NewRepository(w.pool))
	disp := notify.NewDispatcher(notify.NewPgOutboxStore(w.pool), nil)
	glSvc.RegisterPostingHandlers(disp)
	for i := 0; i < 200; i++ {
		if disp.DrainOnce(context.Background()) == 0 {
			return
		}
	}
	w.t.Fatal("the outbox never drained empty")
}

// legs reads one journal's legs (account code, debit, credit) by its idempotency key.
func (w *mjWorld) legs(idempotencyKey string) map[string][2]int64 {
	w.t.Helper()
	rows, err := w.pool.Query(context.Background(), `
		SELECT a.code, COALESCE(l.debit_cents, 0), COALESCE(l.credit_cents, 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1`, idempotencyKey)
	if err != nil {
		w.t.Fatalf("read the journal: %v", err)
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var code string
		var dr, cr int64
		if err := rows.Scan(&code, &dr, &cr); err != nil {
			w.t.Fatalf("scan the journal leg: %v", err)
		}
		out[code] = [2]int64{dr, cr}
	}
	if err := rows.Err(); err != nil {
		w.t.Fatalf("walk the journal legs: %v", err)
	}
	return out
}

// TestFW113PaidOutPostsItsJournal is the brief's red leg: a paid out moves the drawer's expected
// cash and its journal (DR the rules' expense account, CR 1020) posts through the outbox exactly once.
func TestFW113PaidOutPostsItsJournal(t *testing.T) {
	w := newMJWorld(t)

	// The movement is on the drawer: expected cash drops by the paid out.
	amount := int64(12_34)
	id := w.record("PAID_OUT", amount)
	var paidOut int64
	if err := w.pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(amount_cents) FILTER (WHERE movement_type IN ('PAID_OUT','DROP')), 0)
		   FROM till_movements WHERE till_session_id = $1::uuid AND method = 'CASH'`, w.sessionID).Scan(&paidOut); err != nil {
		t.Fatalf("movement totals: %v", err)
	}
	if paidOut != amount {
		t.Fatalf("the paid out did not reach the drawer math: paid out %d, want %d", paidOut, amount)
	}

	// Its journal posts through the outbox, once, on the movement's own key.
	w.drain()
	if _, err := w.pool.Exec(context.Background(),
		`UPDATE events_outbox SET status = 'PENDING', attempts = 0 WHERE topic = 'till.movement.posted'`); err != nil {
		t.Fatalf("re-arm the outbox for the replay leg: %v", err)
	}
	w.drain()

	legs := w.legs("till.movement.posted:" + id)
	if len(legs) == 0 {
		t.Fatalf("a paid out posts no journal: no gl_journals row for idempotency_key till.movement.posted:%s", id)
	}
	expense, ok := legs["6350"]
	if !ok || expense[0] != amount || expense[1] != 0 {
		t.Fatalf("the paid out's expense leg is wrong: want DR 6350 %d, got %v", amount, legs)
	}
	undeposited, ok := legs["1020"]
	if !ok || undeposited[0] != 0 || undeposited[1] != amount {
		t.Fatalf("the paid out's undeposited leg is wrong: want CR 1020 %d, got %v", amount, legs)
	}
}

// TestFW113PaidInMirrorsThePaidOut: a paid in is the mirror journal (DR 1020 / CR the expense
// account the rules hold), through the same outbox path, once.
func TestFW113PaidInMirrorsThePaidOut(t *testing.T) {
	w := newMJWorld(t)
	amount := int64(45_67)
	id := w.record("PAID_IN", amount)

	w.drain()
	legs := w.legs("till.movement.posted:" + id)
	if len(legs) == 0 {
		t.Fatalf("a paid in posts no journal: no gl_journals row for idempotency_key till.movement.posted:%s", id)
	}
	undeposited, ok := legs["1020"]
	if !ok || undeposited[0] != amount || undeposited[1] != 0 {
		t.Fatalf("the paid in's undeposited leg is wrong: want DR 1020 %d, got %v", amount, legs)
	}
	expense, ok := legs["6350"]
	if !ok || expense[0] != 0 || expense[1] != amount {
		t.Fatalf("the paid in's expense leg is wrong: want CR 6350 %d, got %v", amount, legs)
	}
}

// TestFW113LodgementMeetsBankrecs1020: the lodgement's DR 1000 / CR 1020 is the bank match's
// journal and must not also post at record time, or every matched deposit counts twice.
func TestFW113LodgementMeetsBankrecs1020(t *testing.T) {
	w := newMJWorld(t)
	id := w.record("LODGEMENT", 20_00)

	w.drain()
	if legs := w.legs("till.movement.posted:" + id); len(legs) != 0 {
		t.Fatalf("a lodgement booked a second journal at record time; its bank leg is the match's: got %v", legs)
	}
}
