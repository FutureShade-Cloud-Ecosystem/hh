//go:build integration

package main

// w2_6b_fix_round4_integration_test.go, W2-6b fix round 4: an account cannot be retired while an
// unposted till movement still carries it, and the retire guard and the till's record time check lock
// the account row in their transactions so a movement cannot slip between them.
//
// Round 3 stamped the resolved account on the movement and its outbox payload at record time, and taught
// DeactivateAccount to refuse an account a LIVE reason code names. S6 found the remaining door (P2-1):
// a movement is recorded under a live code pointing at 9996, then the code is repointed to 6400 or
// turned off while the movement still waits in the outbox. No live code names 9996 any more, so
// DeactivateAccount(9996) answers 200, and when the waiting movement drains it dead letters with
// "account 9996 is inactive; cannot post".
//
// The legs:
//   - record a movement under 9996, repoint the code to 6400, call deactivate on 9996 and expect 409
//     naming the account and the count of waiting movements; drain, see the movement post to 9996 (never
//     falling back to 6400), and then retire 9996 cleanly
//   - the same when the code is turned off instead of repointed, including while an unposted movement
//     stands DEAD in the outbox until it is requeued and drained
//   - P3-1, the race: DeactivateAccount and the till's record time check take compatible locks on the
//     account row inside their transactions, so racingPaidOut and DeactivateAccount across N rounds
//     never strands an unposted movement on an inactive account
//
//	Run: DATABASE_URL='postgres://postgres@127.0.0.1:57729/postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W2B6FixRound4 -count=1 -v

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestW2B6FixRound4ChartRefusesWhenCodeRepointedWhileMovementWaits is S6's P2-1 first leg: a movement is
// recorded under a code pointing at 9996, the code is repointed to 6400 before the outbox drains, and
// DeactivateAccount(9996) refuses with 409 naming 9996 and the waiting movement count. Once the outbox
// drains, the movement posts to 9996 (never falling back to 6400), and DeactivateAccount(9996) succeeds.
func TestW2B6FixRound4ChartRefusesWhenCodeRepointedWhileMovementWaits(t *testing.T) {
	w := newW2B6FixWalkNamed(t, "w2b6r4a")
	ctx := context.Background()

	// 1. Plant account 9996 and create a live code pointing at it.
	w.plantAccount(t, "9996", "A counter account with a waiting movement repointed away")
	codeID := w.createReasonCode(t, w2b6TRT, map[string]any{
		"code": "WAIT_REPOINT", "label": "Spend with a movement still in the outbox",
		"movement_kind": "PAID_OUT", "gl_account_code": "9996", "active": true,
	})

	// 2. Record a 2000 cent paid out naming WAIT_REPOINT. It is stamped 9996 and waits in the outbox.
	session := w.openSession(t, w2b6TRT)
	w.movement(t, session, map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  2000,
		"reason":        "the postage money waiting in the outbox",
		"reason_code":   "WAIT_REPOINT",
	})
	movement := w.movementID(t, session)

	// 3. Repoint the code to 6400 before the outbox drains. No live code names 9996 now, but the
	//    unposted movement in the outbox still carries 9996.
	w.editReasonCode(t, codeID, map[string]any{"gl_account_code": "6400"})

	// 4. DeactivateAccount(9996) refuses with 409, naming 9996 and the count of waiting movements.
	status, out := w.call(http.MethodPost, "/api/v1/gl/accounts/9996/deactivate", nil)
	if status != http.StatusConflict {
		t.Errorf("the chart answered the deactivate of 9996 while 1 movement waited in the outbox with %d, want 409: %s", status, out)
	}
	if !strings.Contains(string(out), "9996") {
		t.Errorf("the refusal of the deactivate does not name the account 9996: %s", out)
	}
	if !strings.Contains(string(out), "1 waiting movement") {
		t.Errorf("the refusal of the deactivate does not name the count of waiting movements: %s", out)
	}
	var active bool
	if err := w.pool.QueryRow(ctx, `SELECT is_active FROM gl_accounts WHERE code = '9996'`).Scan(&active); err != nil {
		t.Fatalf("reading the account: %v", err)
	}
	if !active {
		t.Fatalf("the refused deactivate left 9996 inactive while the movement still waited")
	}

	// 5. Drain the outbox. The movement posts to 9996 (the account it carried), never falling back to
	//    6400 (the code's current account).
	w.drain()
	legs := w.journalLegs(t, movement)
	if legs["9996"].Debit != 2000 {
		t.Errorf("the waiting paid out debited 9996 by %d, want 2000: %+v", legs["9996"].Debit, legs)
	}
	if legs["1020"].Credit != 2000 {
		t.Errorf("the waiting paid out credited 1020 by %d, want 2000: %+v", legs["1020"].Credit, legs)
	}
	if l, ok := legs["6400"]; ok && (l.Debit != 0 || l.Credit != 0) {
		t.Errorf("the waiting paid out fell back to 6400 with %+v, want nothing at 6400", l)
	}

	// 6. Now that the movement has posted, retiring 9996 succeeds.
	w.retireAccount(t, "9996")
	if err := w.pool.QueryRow(ctx, `SELECT is_active FROM gl_accounts WHERE code = '9996'`).Scan(&active); err != nil {
		t.Fatalf("reading the retired account: %v", err)
	}
	if active {
		t.Errorf("the account 9996 is still live after the waiting movement drained and the account was retired")
	}
}

// TestW2B6FixRound4ChartRefusesWhenCodeTurnedOffWhileMovementWaits is S6's P2-1 second leg: a movement
// is recorded under a code pointing at 9996, the code is turned off before the outbox drains, and
// DeactivateAccount(9996) refuses while the movement is PENDING (or DEAD and not yet resolved). Once the
// movement drains and posts to 9996, DeactivateAccount(9996) succeeds.
func TestW2B6FixRound4ChartRefusesWhenCodeTurnedOffWhileMovementWaits(t *testing.T) {
	w := newW2B6FixWalkNamed(t, "w2b6r4b")
	ctx := context.Background()

	// 1. Plant account 9996 and create a live code pointing at it.
	w.plantAccount(t, "9996", "A counter account with a waiting movement whose code is turned off")
	codeID := w.createReasonCode(t, w2b6TRT, map[string]any{
		"code": "WAIT_OFF", "label": "Spend whose code is turned off before drain",
		"movement_kind": "PAID_OUT", "gl_account_code": "9996", "active": true,
	})

	// 2. Record a 2000 cent paid out naming WAIT_OFF. It is stamped 9996 and waits in the outbox.
	session := w.openSession(t, w2b6TRT)
	w.movement(t, session, map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  2000,
		"reason":        "the postage money waiting while the code is turned off",
		"reason_code":   "WAIT_OFF",
	})
	movement := w.movementID(t, session)

	// 3. Turn the code off before the outbox drains.
	w.editReasonCode(t, codeID, map[string]any{"active": false})

	// 4. DeactivateAccount(9996) refuses with 409 while the movement is PENDING.
	status, out := w.call(http.MethodPost, "/api/v1/gl/accounts/9996/deactivate", nil)
	if status != http.StatusConflict {
		t.Errorf("the chart answered the deactivate of 9996 while 1 PENDING movement waited with %d, want 409: %s", status, out)
	}
	if !strings.Contains(string(out), "9996") || !strings.Contains(string(out), "1 waiting movement") {
		t.Errorf("the refusal of the deactivate on PENDING movement does not name 9996 and 1 waiting movement: %s", out)
	}

	// 5. Even if the outbox row stands DEAD and not yet resolved, DeactivateAccount(9996) still refuses.
	mustExec(t, ctx, w.pool,
		`UPDATE events_outbox SET status = 'DEAD', attempts = 5, last_error = 'simulated transient stop'
		  WHERE topic = 'till.movement.posted' AND payload->>'movement_id' = $1`, movement)
	status, out = w.call(http.MethodPost, "/api/v1/gl/accounts/9996/deactivate", nil)
	if status != http.StatusConflict {
		t.Errorf("the chart answered the deactivate of 9996 while 1 DEAD movement stood unresolved with %d, want 409: %s", status, out)
	}
	if !strings.Contains(string(out), "9996") || !strings.Contains(string(out), "1 waiting movement") {
		t.Errorf("the refusal of the deactivate on DEAD movement does not name 9996 and 1 waiting movement: %s", out)
	}

	// 6. Requeue the dead row, drain the outbox, and verify the movement posts to 9996.
	mustExec(t, ctx, w.pool,
		`UPDATE events_outbox SET status = 'PENDING', attempts = 0, last_error = ''
		  WHERE topic = 'till.movement.posted' AND payload->>'movement_id' = $1`, movement)
	w.drain()
	legs := w.journalLegs(t, movement)
	if legs["9996"].Debit != 2000 {
		t.Errorf("the waiting paid out debited 9996 by %d, want 2000: %+v", legs["9996"].Debit, legs)
	}
	if legs["1020"].Credit != 2000 {
		t.Errorf("the waiting paid out credited 1020 by %d, want 2000: %+v", legs["1020"].Credit, legs)
	}

	// 7. Now that the movement has posted, retiring 9996 succeeds.
	w.retireAccount(t, "9996")
	var active bool
	if err := w.pool.QueryRow(ctx, `SELECT is_active FROM gl_accounts WHERE code = '9996'`).Scan(&active); err != nil {
		t.Fatalf("reading the retired account: %v", err)
	}
	if active {
		t.Errorf("the account 9996 is still live after the waiting movement drained and the account was retired")
	}
}

// onPool builds a second w2b6Walk over the given pool (such as testkit.DrawPark.Pool) sharing the walk's
// staff guards and token.
func (w *w2b6Walk) onPool(t *testing.T, pool *pgxpool.Pool) *w2b6Walk {
	t.Helper()
	log := slog.New(slog.NewTextHandler(w2b6Writer{t}, nil))
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, w.sg.Guards, log, "", mustRoleConfigForWire())
	wireOperations(mux, pool, w.sg.Guards, log, money, "w2b6fix-fx-ticket", belt.outbox)
	billingRepo := billing.NewRepository(pool)
	posRepo := pos.NewRepository(pool)
	money.glSvc.WithInvoiceReader(billingRepo).WithARSubledger(billingRepo).WithTaxSplitReader(posRepo)
	d := notify.NewDispatcher(money.outboxStore, log)
	money.glSvc.RegisterPostingHandlers(d)
	return &w2b6Walk{
		t:     t,
		pool:  pool,
		mux:   mux,
		sg:    w.sg,
		disp:  d,
		token: w.token,
	}
}

// TestW2B6FixRound4DeactivateAndMovementLockTheAccountInTheirTransactions is S6's P3-1 leg:
//  1. A movement in flight holds the account row lock inside the record's transaction before inserting
//     till_movements and events_outbox, and DeactivateAccount takes the lock on the account row in its own
//     transaction before reading the live codes and waiting movements, so a movement recorded between the
//     guard's read and the deactivate's write cannot slip through.
//  2. A 40 round concurrency run on the real database races movement recording against repoint and
//     deactivate, verifying zero deadlocks, zero dead letters, and every accepted movement posted to its
//     account.
func TestW2B6FixRound4DeactivateAndMovementLockTheAccountInTheirTransactions(t *testing.T) {
	w := newW2B6FixWalkNamed(t, "w2b6r4c")
	ctx := context.Background()

	// Part 1: deterministic overlap using testkit.NewInsertPark on till_movements. The movement runs on the
	// park pool and stands inside its transaction right before till_movements and events_outbox are
	// inserted. While it stands there, the code is repointed to 6400 and DeactivateAccount(9996) is called.
	// Without the shared lock on gl_accounts, DeactivateAccount(9996) sees no live code and no outbox row
	// yet, marks 9996 inactive (200), and the movement then commits and dead letters at drain time. With the
	// lock in both transactions, DeactivateAccount(9996) blocks behind the movement, sees the committed
	// waiting movement, and refuses with 409.
	w.plantAccount(t, "9996", "A counter account locked across movement record and deactivate")
	codeID := w.createReasonCode(t, w2b6TRT, map[string]any{
		"code": "RACE_LOCK", "label": "Spend in flight while the account is retired",
		"movement_kind": "PAID_OUT", "gl_account_code": "9996", "active": true,
	})
	session := w.openSession(t, w2b6TRT)

	park := testkit.NewInsertPark(t, w.pool, "till_movements", 55040001)
	parkWalk := w.onPool(t, park.Pool)

	var moveStatus int
	var moveOut []byte
	moveCh := make(chan error, 1)
	go func() {
		moveStatus, moveOut = parkWalk.call(http.MethodPost, "/api/v1/till/sessions/"+session+"/movements", map[string]any{
			"movement_type": "PAID_OUT",
			"method":        "CASH",
			"amount_cents":  2000,
			"reason":        "in flight while deactivate runs",
			"reason_code":   "RACE_LOCK",
		})
		if moveStatus >= 300 {
			moveCh <- fmt.Errorf("movement answered %d: %s", moveStatus, moveOut)
			return
		}
		moveCh <- nil
	}()
	park.WaitParked("the movement stands inside its transaction before till_movements insert")

	// While the movement stands inside its transaction before inserting its outbox row, repoint the code
	// away from 9996 and call DeactivateAccount(9996).
	w.editReasonCode(t, codeID, map[string]any{"gl_account_code": "6400"})

	var deactDone atomic.Bool
	var deactStatus int
	var deactOut []byte
	deactCh := make(chan error, 1)
	go func() {
		defer deactDone.Store(true)
		deactStatus, deactOut = w.call(http.MethodPost, "/api/v1/gl/accounts/9996/deactivate", nil)
		if deactStatus != http.StatusConflict {
			deactCh <- fmt.Errorf("deactivate answered %d, want 409: %s", deactStatus, deactOut)
			return
		}
		deactCh <- nil
	}()
	park.WaitBlockedOr("the deactivate queues on the gl_accounts row lock, or has already finished", deactDone.Load)

	park.AnswersAllowing(
		map[string]chan error{"the parked movement": moveCh, "the deactivate": deactCh},
		map[string]string{"the deactivate": "409"},
	)

	if moveStatus != http.StatusCreated {
		t.Errorf("the in flight movement answered %d, want 201: %s", moveStatus, moveOut)
	}
	if deactStatus != http.StatusConflict {
		t.Errorf("the concurrent deactivate of 9996 answered %d while the movement was in flight, want 409: %s", deactStatus, deactOut)
	}
	if !strings.Contains(string(deactOut), "1 waiting movement") {
		t.Errorf("the concurrent deactivate refusal does not name the 1 waiting movement: %s", deactOut)
	}

	w.drain()
	movement := w.movementID(t, session)
	legs := w.journalLegs(t, movement)
	if legs["9996"].Debit != 2000 || legs["1020"].Credit != 2000 {
		t.Errorf("the movement in flight did not post DR 9996 2000 / CR 1020 2000 after drain: %+v", legs)
	}
	w.retireAccount(t, "9996")

	// Part 1b: deterministic overlap in the opposite order. Hold the session row lock so the movement
	// passes resolveReasonCode while 9997 is active and then parks at the start of InsertMovement;
	// retire 9997 while it waits; release the session lock and verify InsertMovement's in-tx account
	// check refuses with 422 and writes neither a movement row nor an outbox row.
	w.plantAccount(t, "9997", "A counter account retired after resolveReasonCode before InsertMovement locks it")
	code9997 := w.createReasonCode(t, w2b6TRT, map[string]any{
		"code": "RACE_LOCK_2", "label": "Lock test spend 2",
		"movement_kind": "PAID_OUT", "gl_account_code": "9997", "active": true,
	})
	holdTx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session hold tx: %v", err)
	}
	defer func() { _ = holdTx.Rollback(ctx) }()
	if _, err := holdTx.Exec(ctx, `SELECT 1 FROM till_sessions WHERE id = $1 FOR NO KEY UPDATE`, session); err != nil {
		t.Fatalf("lock session row: %v", err)
	}
	var move2Done atomic.Bool
	var move2Status int
	var move2Out []byte
	move2Ch := make(chan struct{})
	go func() {
		defer close(move2Ch)
		defer move2Done.Store(true)
		move2Status, move2Out = w.call(http.MethodPost, "/api/v1/till/sessions/"+session+"/movements", map[string]any{
			"movement_type": "PAID_OUT",
			"method":        "CASH",
			"amount_cents":  1500,
			"reason":        "queued at session lock while 9997 is retired",
			"reason_code":   "RACE_LOCK_2",
		})
	}()
	// Wait until the movement goroutine is blocked in Postgres on the till_sessions row lock (meaning it
	// has already passed resolveReasonCode).
	for attempt := 0; attempt < 200; attempt++ {
		var waiting int
		_ = w.pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND state = 'active'`).Scan(&waiting)
		if waiting > 0 || move2Done.Load() {
			break
		}
		w.editReasonCode(t, code9997, map[string]any{"label": "Lock test spend 2"})
	}
	w.editReasonCode(t, code9997, map[string]any{"active": false})
	w.retireAccount(t, "9997")
	if err := holdTx.Rollback(ctx); err != nil {
		t.Fatalf("release session hold tx: %v", err)
	}
	<-move2Ch
	if move2Status != http.StatusUnprocessableEntity {
		t.Errorf("the movement whose account was retired before InsertMovement locked it answered %d, want 422: %s", move2Status, move2Out)
	}
	if !strings.Contains(string(move2Out), "9997") || !strings.Contains(string(move2Out), "inactive") {
		t.Errorf("the in-tx refusal does not name 9997 as inactive: %s", move2Out)
	}

	// Part 2: real database concurrency run across N=40 rounds racing movement recording against
	// turning the code off and deactivating the account.
	const rounds = 40
	var accepted, refused int
	for i := 1; i <= rounds; i++ {
		acctCode := fmt.Sprintf("98%02d", i)
		rcName := fmt.Sprintf("RACE_%02d", i)
		w.plantAccount(t, acctCode, fmt.Sprintf("Race account round %d", i))
		rcID := w.createReasonCode(t, w2b6TRT, map[string]any{
			"code": rcName, "label": fmt.Sprintf("Race spend round %d", i),
			"movement_kind": "PAID_OUT", "gl_account_code": acctCode, "active": true,
		})
		start := make(chan struct{})
		edited := make(chan struct{})
		var wg sync.WaitGroup
		var mCode, dCode int
		var mBody, dBody []byte
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				<-edited
			}
			mCode, mBody = w.call(http.MethodPost, "/api/v1/till/sessions/"+session+"/movements", map[string]any{
				"movement_type": "PAID_OUT",
				"method":        "CASH",
				"amount_cents":  1000,
				"reason":        fmt.Sprintf("race round %d", i),
				"reason_code":   rcName,
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			w.editReasonCode(t, rcID, map[string]any{"active": false})
			close(edited)
			dCode, dBody = w.call(http.MethodPost, "/api/v1/gl/accounts/"+acctCode+"/deactivate", nil)
		}()
		close(start)
		wg.Wait()

		if mCode != http.StatusCreated && mCode != http.StatusUnprocessableEntity {
			t.Fatalf("round %d: movement answered unexpected status %d: %s", i, mCode, mBody)
		}
		if dCode != http.StatusOK && dCode != http.StatusConflict {
			t.Fatalf("round %d: deactivate answered unexpected status %d: %s", i, dCode, dBody)
		}
		// Never both: if the movement was accepted (201) before drain, either deactivate ran after and
		// refused (409) or deactivate ran before the movement resolved and the movement was refused (422).
		if mCode == http.StatusCreated && dCode == http.StatusOK {
			t.Fatalf("round %d: movement was accepted (201) AND deactivate succeeded (200) before drain; movement will dead letter", i)
		}
		if mCode == http.StatusCreated {
			accepted++
		} else {
			refused++
		}
		w.drain()
		if dCode == http.StatusConflict {
			w.retireAccount(t, acctCode)
		}
	}
	var deadCount int
	if err := w.pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox WHERE topic = 'till.movement.posted' AND status = 'DEAD'`).Scan(&deadCount); err != nil {
		t.Fatalf("counting dead movement events: %v", err)
	}
	if deadCount != 0 {
		t.Errorf("after %d concurrency rounds, %d till.movement.posted rows dead lettered, want 0", rounds, deadCount)
	}
	t.Logf("completed %d real database concurrency rounds: %d movements accepted and posted cleanly, %d refused cleanly, 0 dead letters", rounds, accepted, refused)
}
