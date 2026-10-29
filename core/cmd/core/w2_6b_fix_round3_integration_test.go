//go:build integration

package main

// w2_6b_fix_round3_integration_test.go, W2-6b fix round 3: a code's account can never become unpostable
// under a live movement.
//
// Round 1 checked the account when a manager WROTE the reason code. A code written while its account was
// live still found out later: the account was retired afterwards, the till took the movement anyway, and
// its journal dead lettered with "account 9996 is inactive; cannot post". The drawer's cash was 2000 cents
// short, the GL held no journal for it, and the month close was blocked.
//
// The three legs:
//   - the till refuses at record time, in words, and nothing is recorded
//   - the movement carries the account it resolved, so a repoint cannot move a movement that has not
//     posted yet
//   - the chart refuses to retire an account a live reason code names
//
// The legs run on a real migrated database seeded with the default build's Ontario Dibbits seed, every
// step through the real services wired exactly as cmd/core wires them, and the GL consumer's posting loop
// run synchronously after each step, exactly as worker.go registers it.
//
//	Run: DATABASE_URL='postgres://postgres@127.0.0.1:57726/postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W2B6FixRound3 -count=1 -v

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// movementCount answers how many movement rows the session carries.
//
// A drawer is one physical drawer: opening a drawer that is already open attaches to the session that
// is open, so the count before a refused movement is the count the session already had.
func (w *w2b6Walk) movementCount(t *testing.T, sessionID string) int {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM till_movements WHERE till_session_id = $1::uuid`, sessionID).Scan(&n); err != nil {
		t.Fatalf("counting the session's movements: %v", err)
	}
	return n
}

// movementOutboxCount answers how many movement posted rows the session's outbox carries.
func (w *w2b6Walk) movementOutboxCount(t *testing.T, sessionID string) int {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events_outbox
		  WHERE topic = 'till.movement.posted' AND payload->>'session_id' = $1`, sessionID).Scan(&n); err != nil {
		t.Fatalf("counting the session's outbox rows: %v", err)
	}
	return n
}

// TestW2B6FixRound3TillRefusesAtRecordTime is the till's leg: a movement naming a code whose account is
// no longer postable is refused in words, and nothing is recorded.
//
// The state is reached through the API alone, which is how a dealer reaches it: the account is retired
// while the code is off, and the code is turned on afterwards. Turning a code on does not recheck the
// account, so a live code can name an account the chart has already retired.
func TestW2B6FixRound3TillRefusesAtRecordTime(t *testing.T) {
	w := newW2B6FixWalkNamed(t, "w2b6r3a")

	// 1. The account is planted and a code points at it. The code ships off: while it is off, no live
	//    code names the account.
	w.plantAccount(t, "9996", "A counter account turned off under a live code")
	codeID := w.createReasonCode(t, w2b6TRT, map[string]any{
		"code": "LATER", "label": "Postage at an account turned off later",
		"movement_kind": "PAID_OUT", "gl_account_code": "9996", "active": false,
	})

	// 2. The account is retired. No live code names it, so the chart lets it go.
	w.retireAccount(t, "9996")

	// 3. The code is turned on. Now a live code names an account the chart has retired.
	w.editReasonCode(t, codeID, map[string]any{"active": true})

	// 4. A paid out naming the code is refused at record time, in words, and nothing is recorded.
	session := w.openSession(t, w2b6TRT)
	movementsBefore := w.movementCount(t, session)
	outboxBefore := w.movementOutboxCount(t, session)
	status, out := w.call(http.MethodPost, "/api/v1/till/sessions/"+session+"/movements", map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  2000,
		"reason":        "the postage money, after the account was turned off",
		"reason_code":   "LATER",
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("the till answered a movement naming a code whose account is retired with %d, want 422: %s", status, out)
	}
	if !strings.Contains(string(out), "the account 9996 is inactive") {
		t.Errorf("the refusal of a movement naming a code whose account is retired does not name the inactive account: %s", out)
	}

	// 5. Nothing is recorded: no movement row and no outbox row. The drawer keeps its cash and the
	//    books keep no half movement.
	if got := w.movementCount(t, session); got != movementsBefore {
		t.Errorf("the refused movement left %d movement rows behind, want %d", got, movementsBefore)
	}
	if got := w.movementOutboxCount(t, session); got != outboxBefore {
		t.Errorf("the refused movement left %d outbox rows behind, want %d", got, outboxBefore)
	}
}

// TestW2B6FixRound3MovementCarriesItsAccount is the movement's leg: the movement carries the account it
// resolved at record time, so a repoint cannot move a movement that has not posted yet.
func TestW2B6FixRound3MovementCarriesItsAccount(t *testing.T) {
	w := newW2B6FixWalkNamed(t, "w2b6r3b")
	ctx := context.Background()

	// 1. The postage code is turned on. It points at 6400.
	postage := w.codeNamed(t, w2b6TRT, "POSTAGE")
	w.editReasonCode(t, postage.ID, map[string]any{"active": true})

	// 2. A paid out names it. The account resolves now, at record time.
	session := w.openSession(t, w2b6TRT)
	w.movement(t, session, map[string]any{
		"movement_type": "PAID_OUT",
		"method":        "CASH",
		"amount_cents":  3000,
		"reason":        "the stamp money",
		"reason_code":   "POSTAGE",
	})
	movement := w.movementID(t, session)

	// 3. The movement carries the account it resolved, so the drawer and the books agree on where the
	//    money went whatever the list does later.
	var carried string
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(gl_account_code, '') FROM till_movements WHERE id = $1::uuid`, movement).Scan(&carried); err != nil {
		t.Fatalf("reading the movement's account: %v", err)
	}
	if carried != "6400" {
		t.Errorf("the movement carries the account %q, want the 6400 it resolved", carried)
	}

	// 4. Before the outbox drains, a manager repoints POSTAGE at 6500.
	w.editReasonCode(t, postage.ID, map[string]any{"gl_account_code": "6500"})

	// 5. The drain posts the movement to the account it resolved, not to the account the code points at
	//    now.
	w.drain()
	legs := w.journalLegs(t, movement)
	if legs["6400"].Debit != 3000 {
		t.Errorf("the postage paid out debited 6400 by %d, want 3000: %+v", legs["6400"].Debit, legs)
	}
	if legs["1020"].Credit != 3000 {
		t.Errorf("the postage paid out credited 1020 by %d, want 3000: %+v", legs["1020"].Credit, legs)
	}
	if l, ok := legs["6500"]; ok && (l.Debit != 0 || l.Credit != 0) {
		t.Errorf("the postage paid out left %+v at 6500, want nothing: the repoint reached a movement that had not posted", l)
	}
}

// TestW2B6FixRound3ChartRefusesToRetireALiveCodesAccount is the chart's leg: an account a live reason
// code names may not be retired, because a movement naming that code would post to a dead account.
func TestW2B6FixRound3ChartRefusesToRetireALiveCodesAccount(t *testing.T) {
	w := newW2B6FixWalkNamed(t, "w2b6r3c")
	ctx := context.Background()

	// 1. The account is planted and a live code names it.
	w.plantAccount(t, "9996", "A counter account a live code names")
	codeID := w.createReasonCode(t, w2b6TRT, map[string]any{
		"code": "LIVE", "label": "A spend at a live account",
		"movement_kind": "PAID_OUT", "gl_account_code": "9996", "active": true,
	})

	// 2. The chart refuses to retire it, in words, naming the code and how many branches it sits on.
	status, out := w.call(http.MethodPost, "/api/v1/gl/accounts/9996/deactivate", nil)
	if status != http.StatusConflict {
		t.Errorf("the chart answered the deactivate of an account a live code names with %d, want 409: %s", status, out)
	}
	if !strings.Contains(string(out), "LIVE") {
		t.Errorf("the refusal of the deactivate does not name the code that needs the account: %s", out)
	}
	if !strings.Contains(string(out), "1 branch") {
		t.Errorf("the refusal of the deactivate does not say how many branches the code sits on: %s", out)
	}
	var active bool
	if err := w.pool.QueryRow(ctx, `SELECT is_active FROM gl_accounts WHERE code = '9996'`).Scan(&active); err != nil {
		t.Fatalf("reading the account: %v", err)
	}
	if !active {
		t.Errorf("the refused deactivate left 9996 inactive, want it live")
	}

	// 3. Once the code is turned off, the account retires. The guard follows the live codes, not the
	//    codes that once existed.
	w.editReasonCode(t, codeID, map[string]any{"active": false})
	w.retireAccount(t, "9996")
	if err := w.pool.QueryRow(ctx, `SELECT is_active FROM gl_accounts WHERE code = '9996'`).Scan(&active); err != nil {
		t.Fatalf("reading the retired account: %v", err)
	}
	if active {
		t.Errorf("the account is still live after the code was turned off and the account retired")
	}
}
