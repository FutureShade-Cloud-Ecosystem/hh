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
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

// glPosterOver posts bankrec's clearing journals through the real GL service, the same
// adapter shape the payments walk's tests use.
type glPosterOver struct{ svc *gl.Service }

func (a glPosterOver) PostJournal(ctx context.Context, in gl.ExternalJournal) (string, error) {
	return a.svc.PostExternal(ctx, in)
}

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
		// The cleanup is SCOPED to this leg's own rows: the shared gate database holds other packages'
		// movements and journals, and a leg that deleted every till.movement.posted row in the
		// database would take their markers with it (P3).
		_, _ = pool.Exec(c, `
			DELETE FROM gl_journal_lines WHERE journal_id IN (
			  SELECT j.id FROM gl_journals j
			   WHERE j.idempotency_key IN (
			     SELECT 'till.movement.posted:' || m.id::text FROM till_movements m
			      WHERE m.till_session_id = $1::uuid))`, id)
		_, _ = pool.Exec(c, `
			DELETE FROM gl_journals WHERE idempotency_key IN (
			  SELECT 'till.movement.posted:' || m.id::text FROM till_movements m
			   WHERE m.till_session_id = $1::uuid)`, id)
		_, _ = pool.Exec(c, `
			DELETE FROM events_outbox WHERE topic = 'till.movement.posted' AND payload->>'movement_id' IN (
			  SELECT m.id::text FROM till_movements m WHERE m.till_session_id = $1::uuid)`, id)
		// The bankrec clearing journals of this leg's own statement lines (their source_ref_id is the
		// line, and the line carries this process's reference).
		_, _ = pool.Exec(c, `
			DELETE FROM gl_journal_lines WHERE journal_id IN (
			  SELECT j.id FROM gl_journals j
			   WHERE j.source_ref_id IN (SELECT l.id FROM bank_statement_lines l WHERE l.reference = $2))`, id, "MJ-"+fmt.Sprint(os.Getpid()))
		_, _ = pool.Exec(c, `
			DELETE FROM gl_journals WHERE source_ref_id IN (
			  SELECT l.id FROM bank_statement_lines l WHERE l.reference = $1)`, "MJ-"+fmt.Sprint(os.Getpid()))
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

// mjLeg is one journal line as the books carry it: the account, the money, the words and the branch.
type mjLeg struct {
	Debit  int64
	Credit int64
	Memo   string
	Branch string
}

// legsWithMemo reads one journal's lines with their memos and their branch, by idempotency key.
func (w *mjWorld) legsWithMemo(idempotencyKey string) map[string]mjLeg {
	w.t.Helper()
	rows, err := w.pool.Query(context.Background(), `
		SELECT a.code, COALESCE(l.debit_cents, 0), COALESCE(l.credit_cents, 0), COALESCE(l.memo, ''),
		       COALESCE(l.branch_id::text, '')
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1`, idempotencyKey)
	if err != nil {
		w.t.Fatalf("read the journal lines: %v", err)
	}
	defer rows.Close()
	out := map[string]mjLeg{}
	for rows.Next() {
		var code string
		var l mjLeg
		if err := rows.Scan(&code, &l.Debit, &l.Credit, &l.Memo, &l.Branch); err != nil {
			w.t.Fatalf("scan the journal line: %v", err)
		}
		out[code] = l
	}
	if err := rows.Err(); err != nil {
		w.t.Fatalf("walk the journal lines: %v", err)
	}
	return out
}

// mjPrivateDay names a day in the leg's OWN year: ten past the newest seeded fiscal year, the same
// window the dated leg below opens. The bankrec suite's own legs complete the CURRENT month's
// reconciliation while they run, and a match inside a completed month is refused, so a line dated today
// would fail the match whenever that suite runs beside this one. A month no other leg completes is the
// leg's own, and the date of a statement line never reaches the books: the clearing journal is dated by
// the GL's own clock.
func (w *mjWorld) privateDay() time.Time {
	w.t.Helper()
	var maxYear int
	if err := w.pool.QueryRow(context.Background(), `SELECT COALESCE(max(year), 2026) FROM fiscal_periods`).Scan(&maxYear); err != nil {
		w.t.Skipf("no fiscal periods to read: %v", err)
	}
	return time.Date(maxYear+10, time.July, 1, 0, 0, 0, 0, time.UTC)
}

// statementLine seeds one unmatched bank statement line for a lodgement match, the same shape the
// payments walk's legs use, cleaned up by exact id.
func (w *mjWorld) statementLine(cents int64) string {
	w.t.Helper()
	ctx := context.Background()
	var acct string
	if err := w.pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&acct); err != nil {
		w.t.Skipf("no seeded bank account: %v", err)
	}
	day := w.privateDay()
	ref := "MJ-" + fmt.Sprint(os.Getpid())
	var id string
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1, $2, 'FW1-13 LODGEMENT', $3, $4, $5, 'UNMATCHED') RETURNING id::text`,
		acct, day, ref, cents, bankrec.DedupeHash(day, cents, "FW1-13 LODGEMENT", ref)).Scan(&id); err != nil {
		w.t.Fatalf("seed statement line: %v", err)
	}
	w.t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM bank_matches WHERE statement_line_id = $1::uuid`, id)
		_, _ = w.pool.Exec(c, `DELETE FROM bank_statement_lines WHERE id = $1::uuid`, id)
	})
	return id
}

// TestFW113PaidOutPostsItsJournal is the brief's red leg: a paid out moves the drawer's expected
// cash and its journal (DR 9999 Suspense / CR 1020) posts through the outbox exactly once. 6350 is for
// the counted over or short alone, so a paid out never touches it (FROM-PRINCIPAL 132).
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
	suspense, ok := legs["9999"]
	if !ok || suspense[0] != amount || suspense[1] != 0 {
		t.Fatalf("the paid out's suspense leg is wrong: want DR 9999 %d, got %v", amount, legs)
	}
	undeposited, ok := legs["1020"]
	if !ok || undeposited[0] != 0 || undeposited[1] != amount {
		t.Fatalf("the paid out's undeposited leg is wrong: want CR 1020 %d, got %v", amount, legs)
	}
	if _, over := legs["6350"]; over {
		t.Fatalf("a paid out touched 6350, which is for the counted over or short alone: got %v", legs)
	}

	// Every line carries the drawer's own reason words and the branch.
	detailed := w.legsWithMemo("till.movement.posted:" + id)
	for _, code := range []string{"9999", "1020"} {
		line, ok := detailed[code]
		if !ok {
			t.Fatalf("the %s leg is missing: %v", code, detailed)
		}
		if !strings.Contains(line.Memo, "FW1-13 walk") {
			t.Fatalf("the %s leg does not carry the drawer's reason words: %q", code, line.Memo)
		}
		if line.Branch != w.branchID {
			t.Fatalf("the %s leg carries branch %q, want the drawer's branch %q", code, line.Branch, w.branchID)
		}
	}
}

// TestFW113PaidInMirrorsThePaidOut: a paid in is the mirror journal (DR 1020 / CR 9999 Suspense),
// through the same outbox path, once.
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
	suspense, ok := legs["9999"]
	if !ok || suspense[0] != 0 || suspense[1] != amount {
		t.Fatalf("the paid in's suspense leg is wrong: want CR 9999 %d, got %v", amount, legs)
	}
	if _, over := legs["6350"]; over {
		t.Fatalf("a paid in touched 6350, which is for the counted over or short alone: got %v", legs)
	}
	detailed := w.legsWithMemo("till.movement.posted:" + id)
	for _, code := range []string{"1020", "9999"} {
		line, ok := detailed[code]
		if !ok {
			t.Fatalf("the %s leg is missing: %v", code, detailed)
		}
		if !strings.Contains(line.Memo, "FW1-13 walk") {
			t.Fatalf("the %s leg does not carry the drawer's reason words: %q", code, line.Memo)
		}
		if line.Branch != w.branchID {
			t.Fatalf("the %s leg carries branch %q, want the drawer's branch %q", code, line.Branch, w.branchID)
		}
	}
}

// TestFW113LodgementPostsItsJournalAtLodgement is the ruled lodgement (FROM-PRINCIPAL 117, 120): the
// lodgement posts DR 1000 Bank / CR 1020 Undeposited Funds at lodgement, through the outbox, once.
// Round 1's pin held the opposite (book nothing at record time, the match posts it); the principal's
// ruling overrules that reading, and FW1-05 makes the match of a lodgement that carries its journal
// post nothing, so the pair never books twice.
func TestFW113LodgementPostsItsJournalAtLodgement(t *testing.T) {
	w := newMJWorld(t)
	amount := int64(20_00)
	id := w.record("LODGEMENT", amount)

	w.drain()
	legs := w.legs("till.movement.posted:" + id)
	if len(legs) == 0 {
		t.Fatalf("a lodgement posts no journal at lodgement: no gl_journals row for idempotency_key till.movement.posted:%s", id)
	}
	bank, ok := legs["1000"]
	if !ok || bank[0] != amount || bank[1] != 0 {
		t.Fatalf("the lodgement's bank leg is wrong: want DR 1000 %d, got %v", amount, legs)
	}
	undeposited, ok := legs["1020"]
	if !ok || undeposited[0] != 0 || undeposited[1] != amount {
		t.Fatalf("the lodgement's undeposited leg is wrong: want CR 1020 %d, got %v", amount, legs)
	}
}

// TestFW113LodgementJournalIsDatedByItsBusinessDay: the journal's instant is the movement's own, so
// its period is the house zone's business day of the lodgement, never the outbox consumer's clock. A
// Toronto late evening sits in the UTC next day, and the books must keep the dealer's day.
func TestFW113LodgementJournalIsDatedByItsBusinessDay(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()

	// The house zone's late evening on the last day of a 31 day month: 23:30 in America/Toronto is
	// past midnight UTC, so a server-zone date would file next month. The leg sets up its OWN fiscal
	// year, ten past the newest seeded one: the shared gate database's months get closed by earlier
	// packages' legs, and a closed period refuses the journal silently through the outbox (the row
	// goes DEAD and the leg would read a missing journal).
	var maxYear int
	if err := w.pool.QueryRow(ctx, `SELECT COALESCE(max(year), 2026) FROM fiscal_periods`).Scan(&maxYear); err != nil {
		t.Skipf("no fiscal periods to read: %v", err)
	}
	year := maxYear + 10
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO fiscal_periods (year, month, status) VALUES ($1, 7, 'OPEN')
		ON CONFLICT (year, month) DO UPDATE SET status = 'OPEN'`, year); err != nil {
		t.Fatalf("set up the leg's own period: %v", err)
	}
	// Registered BEFORE the journal cleanup below, so it runs AFTER it: the journal names the period.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM fiscal_periods WHERE year = $1 AND month = 7`, year)
	})
	day := time.Date(year, 7, 31, 23, 30, 0, 0, time.FixedZone("EDT", -4*3600))
	// A fixed UUID: the journal's source_ref_id is a uuid column, as every real movement id is.
	movementID := "00000000-0000-4000-8000-0000000d47ed"
	payload, err := json.Marshal(map[string]any{
		"movement_id":   movementID,
		"session_id":    w.sessionID,
		"branch_id":     w.branchID,
		"movement_type": "LODGEMENT",
		"method":        "CASH",
		"amount_cents":  5_00,
		"occurred_at":   day.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal the dated payload: %v", err)
	}
	if _, err := w.pool.Exec(ctx,
		`INSERT INTO events_outbox (topic, payload, occurred_at) VALUES ('till.movement.posted', $1, $2)`,
		payload, day.UTC()); err != nil {
		t.Fatalf("seed the outbox row: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "till.movement.posted:"+movementID)
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "till.movement.posted:"+movementID)
		_, _ = w.pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'till.movement.posted' AND payload->>'movement_id' = $1`, movementID)
	})

	// The consumer's own clock is a month later; the journal must still carry the movement's instant.
	w.drain()

	var postedAt time.Time
	var fy, fm int
	err = w.pool.QueryRow(ctx, `
		SELECT j.posted_at, j.fiscal_year, fp.month
		  FROM gl_journals j
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.idempotency_key = $1`, "till.movement.posted:"+movementID).Scan(&postedAt, &fy, &fm)
	if err != nil {
		t.Fatalf("a lodgement journal dated by its business day: no journal for %s: %v", movementID, err)
	}
	if !postedAt.Equal(day.UTC()) {
		t.Fatalf("the lodgement journal is dated %s, want the movement's own instant %s", postedAt, day.UTC())
	}
	if fy != day.In(houseday.Location()).Year() || fm != int(day.In(houseday.Location()).Month()) {
		t.Fatalf("the lodgement journal filed to %04d-%02d, want the house zone's day %04d-%02d", fy, fm,
			day.In(houseday.Location()).Year(), day.In(houseday.Location()).Month())
	}
}

// slipPayment seeds one RECEIVED payment on the session, the deposit slip's raw material, cleaned up
// by its own id.
func (w *mjWorld) slipPayment(method string, cents int64) string {
	w.t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(), `
		INSERT INTO payments (payment_no, method, amount_cents, status, branch_id, till_session_id)
		VALUES ('MJ-SLIP-' || md5(random()::text), $1, $2, 'RECEIVED', $3::uuid, $4::uuid)
		RETURNING id::text`, method, cents, w.branchID, w.sessionID).Scan(&id); err != nil {
		w.t.Fatalf("seed the slip payment: %v", err)
	}
	w.t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM till_lodgement_payments WHERE payment_id = $1::uuid`, id)
		_, _ = w.pool.Exec(c, `DELETE FROM payments WHERE id = $1::uuid`, id)
	})
	return id
}

// lodgeWithSlip records a lodgement naming its payments.
func (w *mjWorld) lodgeWithSlip(cents int64, paymentIDs []string) error {
	return w.svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "LODGEMENT", Method: "CASH",
		AmountCents: cents, Reason: "FW1-13 slip", PaymentIDs: paymentIDs,
	})
}

// TestFW113LodgementMarksItsNamedPaymentsDeposited is the deposit slip (FROM-PRINCIPAL 128): the
// lodgement names its payments, the link rows land, the named payments turn DEPOSITED, and the ruled
// journal posts, all from the one movement call. A slip that does not add up is refused and marks
// nothing.
func TestFW113LodgementMarksItsNamedPaymentsDeposited(t *testing.T) {
	w := newMJWorld(t)
	p1 := w.slipPayment("CASH", 12_00)
	p2 := w.slipPayment("CASH", 8_00)

	if err := w.lodgeWithSlip(20_00, []string{p1, p2}); err != nil {
		t.Fatalf("the lodgement with its slip: %v", err)
	}
	var marks, links int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payments WHERE id = ANY($1::uuid[]) AND status = 'DEPOSITED'`, []string{p1, p2}).Scan(&marks); err != nil {
		t.Fatalf("read the marks: %v", err)
	}
	if marks != 2 {
		t.Fatalf("the slip marked %d of 2 payments DEPOSITED", marks)
	}
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM till_lodgement_payments WHERE payment_id = ANY($1::uuid[])`, []string{p1, p2}).Scan(&links); err != nil {
		t.Fatalf("read the slip links: %v", err)
	}
	if links != 2 {
		t.Fatalf("the slip wrote %d of 2 link rows", links)
	}
	w.drain()
	var movementID string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = 'LODGEMENT'`, w.sessionID).Scan(&movementID); err != nil {
		t.Fatalf("find the lodgement: %v", err)
	}
	legs := w.legs("till.movement.posted:" + movementID)
	if bank := legs["1000"]; bank[0] != 20_00 {
		t.Fatalf("the slip's journal posted no bank debit: got %v", legs)
	}

	// A partial slip is refused, never claimed: a second slip under the drawer's sum answers 422 and
	// marks nothing.
	p3 := w.slipPayment("CASH", 5_00)
	if err := w.lodgeWithSlip(9_00, []string{p3}); err == nil {
		t.Fatalf("a slip that does not add up must be refused")
	}
	var still int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payments WHERE id = $1::uuid AND status = 'DEPOSITED'`, p3).Scan(&still); err != nil {
		t.Fatalf("read the refused slip's payment: %v", err)
	}
	if still != 0 {
		t.Fatalf("a refused slip marked its payment DEPOSITED anyway")
	}
}

// TestFW113ChequeLodgedThenBouncedBeforeTheMatchCreditsTheBank is ruling 120's between case: a
// cheque lodged with its slip is DEPOSITED before any bank line exists, and its bounce then credits
// 1000 by the 06a bounce rule, whether or not the line has been matched.
func TestFW113ChequeLodgedThenBouncedBeforeTheMatchCreditsTheBank(t *testing.T) {
	w := newMJWorld(t)
	cheque := w.slipPayment("CHEQUE", 30_00)
	if err := w.svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "LODGEMENT", Method: "CHEQUE",
		AmountCents: 30_00, Reason: "FW1-13 cheque slip", PaymentIDs: []string{cheque},
	}); err != nil {
		t.Fatalf("the cheque lodgement: %v", err)
	}
	var status string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT status FROM payments WHERE id = $1::uuid`, cheque).Scan(&status); err != nil {
		t.Fatalf("read the cheque: %v", err)
	}
	if status != "DEPOSITED" {
		t.Fatalf("the lodged cheque is %s, want DEPOSITED before any bank match", status)
	}

	// The bounce before any match: the till's NSF path, whose event carries that the money had
	// settled to the bank.
	if _, err := w.svc.MarkNSF(staffCtx(w.userID, w.branchID), till.NSFInput{PaymentID: cheque, Reason: "bounced before the match"}); err != nil {
		t.Fatalf("the bounce: %v", err)
	}
	w.drain()

	var credits int64
	if err := w.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(l.credit_cents), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND a.code = '1000'`, "payment.bounced:"+cheque).Scan(&credits); err != nil {
		t.Fatalf("read the bounce journal: %v", err)
	}
	if credits != 30_00 {
		t.Fatalf("the bounce of a lodged cheque credited %d to 1000, want 3000", credits)
	}
}

// TestFW113OldLodgementKeepsTodaysMatch is the box-with-history leg (FROM-PRINCIPAL 117): a lodgement
// from before this change has no lodgement journal, and its bank match keeps today's path, posting
// DR 1000 / CR 1020 itself. Nothing is booked twice.
func TestFW113OldLodgementKeepsTodaysMatch(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()

	// A pre-change lodgement: the movement row lands without the round-trip, so no outbox row and no
	// journal exist for it, exactly a box with history.
	var oldID string
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
		VALUES ($1::uuid, 'LODGEMENT', 'CASH', 15_00, 'pre change drop', 'OLD-SLIP', NULL)
		RETURNING id::text`, w.sessionID).Scan(&oldID); err != nil {
		t.Fatalf("seed the old lodgement: %v", err)
	}

	var n int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE source_ref_id::text = $1`, oldID).Scan(&n); err != nil {
		t.Fatalf("count the old lodgement's journals: %v", err)
	}
	if n != 0 {
		t.Fatalf("the old lodgement already carries %d journals; the leg wants a pre-change row", n)
	}

	// Today's match: the statement line pairs the lodgement and the clearing journal posts.
	line := w.statementLine(15_00)
	bank := bankrec.NewService(bankrec.NewRepository(w.pool)).
		WithJournalPoster(glPosterOver{gl.NewService(gl.NewRepository(w.pool)).WithPaymentReader(till.NewRepository(w.pool))}).
		WithLodgements(till.NewRepository(w.pool))
	m, err := bank.ManualMatch(ctx, bankrec.MatchInput{
		Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetLodgement,
		TargetRefID: oldID, AmountCents: 15_00, CanAccessBranch: bankrec.AllBranches,
	}, w.userID)
	if err != nil {
		t.Fatalf("the old lodgement's match: %v", err)
	}

	legs := w.legs("bankrec:match:" + m.ID)
	bankLegs, ok := legs["1000"]
	if !ok || bankLegs[0] != 15_00 {
		t.Fatalf("the old lodgement's match posted no bank leg: got %v", legs)
	}
	if undeposited := legs["1020"]; undeposited[1] != 15_00 {
		t.Fatalf("the old lodgement's match posted no undeposited credit: got %v", legs)
	}
}

// TestFW113LodgementThatCarriesItsJournalIsOnlyConfirmed is the match half of the lodgement rule
// (FROM-PRINCIPAL 117 and 120): a lodgement that carries its own journal (it has link rows, and the
// till posted DR 1000 / CR 1020 at lodgement) is CONFIRMED by its bank match and posts nothing, so the
// deposit is booked once and never twice. Its undo reverses nothing either, because the match booked
// nothing. The older lodgement's match, in the leg above, keeps today's posting.
func TestFW113LodgementThatCarriesItsJournalIsOnlyConfirmed(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()

	// The lodgement with its slip: the link rows land, the named payments turn DEPOSITED at lodgement,
	// and the ruled journal posts DR 1000 / CR 1020.
	cheque := w.slipPayment("CASH", 15_00)
	if err := w.lodgeWithSlip(15_00, []string{cheque}); err != nil {
		t.Fatalf("the lodgement with its slip: %v", err)
	}
	var movementID string
	if err := w.pool.QueryRow(ctx,
		`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = 'LODGEMENT'`, w.sessionID).Scan(&movementID); err != nil {
		t.Fatalf("find the lodgement: %v", err)
	}
	var links int
	if err := w.pool.QueryRow(ctx,
		`SELECT count(*) FROM till_lodgement_payments WHERE till_movement_id = $1::uuid`, movementID).Scan(&links); err != nil {
		t.Fatalf("read the link rows: %v", err)
	}
	if links != 1 {
		t.Fatalf("the lodgement carries %d link rows, want 1", links)
	}
	w.drain()
	if bankLeg := w.legs("till.movement.posted:" + movementID)["1000"]; bankLeg[0] != 15_00 {
		t.Fatalf("the lodgement posted no bank debit at lodgement: got %v", w.legs("till.movement.posted:"+movementID))
	}

	// The bank match: it confirms the line and posts NOTHING.
	line := w.statementLine(15_00)
	bank := bankrec.NewService(bankrec.NewRepository(w.pool)).
		WithJournalPoster(glPosterOver{gl.NewService(gl.NewRepository(w.pool)).WithPaymentReader(till.NewRepository(w.pool))}).
		WithLodgements(till.NewRepository(w.pool))
	m, err := bank.ManualMatch(ctx, bankrec.MatchInput{
		Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetLodgement,
		TargetRefID: movementID, AmountCents: 15_00, CanAccessBranch: bankrec.AllBranches,
	}, w.userID)
	if err != nil {
		t.Fatalf("the lodgement's match: %v", err)
	}
	if legs := w.legs("bankrec:match:" + m.ID); len(legs) != 0 {
		t.Fatalf("a lodgement that carries its own journal must be confirmed, not journaled: got %v", legs)
	}

	// The match claimed no payment either: the lodgement already marked its slip DEPOSITED, and the
	// match must not mark anything again.
	var status string
	if err := w.pool.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1::uuid`, cheque).Scan(&status); err != nil {
		t.Fatalf("read the slip payment: %v", err)
	}
	if status != "DEPOSITED" {
		t.Fatalf("the slip payment is %s, want DEPOSITED (marked at lodgement, never by the match)", status)
	}

	// The undo reverses nothing: the match booked nothing.
	if err := bank.UnmatchReach(ctx, m.ID, w.userID, bankrec.AllBranches); err != nil {
		t.Fatalf("the lodgement's unmatch: %v", err)
	}
	if legs := w.legs("bankrec:unmatch:" + m.ID); len(legs) != 0 {
		t.Fatalf("the undo of a confirmed match must reverse nothing: got %v", legs)
	}
}

// TestFW113ZReportListsTheDrawerMovements is the Z report half of the 9999 rule (FROM-PRINCIPAL 132):
// the Z report lists the drawer's movements, each line with the drawer's own reason words and the
// branch it belongs to, so the controller can read the suspense balance back to the movements behind it.
func TestFW113ZReportListsTheDrawerMovements(t *testing.T) {
	w := newMJWorld(t)
	if err := w.svc.PaidIn(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "PAID_IN", Method: "CASH",
		AmountCents: 45_67, Reason: "change from the safe",
	}); err != nil {
		t.Fatalf("the paid in: %v", err)
	}
	if err := w.svc.PaidOut(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "PAID_OUT", Method: "CASH",
		AmountCents: 12_34, Reason: "petty cash for the courier",
	}); err != nil {
		t.Fatalf("the paid out: %v", err)
	}

	z, err := w.svc.ZReportFor(staffCtx(w.userID, w.branchID), w.sessionID)
	if err != nil {
		t.Fatalf("the Z report: %v", err)
	}
	seen := map[string]till.ZReportMovement{}
	for _, m := range z.Movements {
		seen[m.MovementType] = m
	}
	in, ok := seen["PAID_IN"]
	if !ok {
		t.Fatalf("the Z report lists no paid in: %+v", z.Movements)
	}
	if in.AmountCents != 45_67 {
		t.Fatalf("the paid in line on the Z report is %d, want 4567", in.AmountCents)
	}
	if in.Reason != "change from the safe" {
		t.Fatalf("the paid in line does not carry the drawer's reason words: %q", in.Reason)
	}
	if in.BranchID != w.branchID {
		t.Fatalf("the paid in line carries branch %q, want the drawer's branch %q", in.BranchID, w.branchID)
	}
	out, ok := seen["PAID_OUT"]
	if !ok {
		t.Fatalf("the Z report lists no paid out: %+v", z.Movements)
	}
	if out.AmountCents != 12_34 {
		t.Fatalf("the paid out line on the Z report is %d, want 1234", out.AmountCents)
	}
	if out.Reason != "petty cash for the courier" {
		t.Fatalf("the paid out line does not carry the drawer's reason words: %q", out.Reason)
	}
	if out.BranchID != w.branchID {
		t.Fatalf("the paid out line carries branch %q, want the drawer's branch %q", out.BranchID, w.branchID)
	}
}

// TestFW113ChequeLodgementWithoutASlipIsRefused is P1-1 (FROM-PRINCIPAL 166): a CHEQUE lodgement must
// name its cheques, because a cheque bounces and the deposit has to say which payments it carries, or
// the bounce credits the wrong account. The refusal speaks to the drawer: the cheques come to one amount
// and the lodgement says another. A CASH lodgement may go without a slip (cash does not bounce).
func TestFW113ChequeLodgementWithoutASlipIsRefused(t *testing.T) {
	w := newMJWorld(t)
	cheque := w.slipPayment("CHEQUE", 30_00)

	err := w.svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "LODGEMENT", Method: "CHEQUE",
		AmountCents: 30_00, Reason: "FW1-13 bank run",
	})
	if err == nil {
		t.Fatal("a cheque lodgement without its slip must be refused")
	}
	if !strings.Contains(err.Error(), "Tick the cheques in this lodgement") {
		t.Fatalf("the refusal must speak to the drawer, got %v", err)
	}
	if !strings.Contains(err.Error(), "$30.00") {
		t.Fatalf("the refusal must name the lodgement's amount, got %v", err)
	}
	if !strings.Contains(err.Error(), "$0.00") {
		t.Fatalf("the refusal must name what the slip comes to, got %v", err)
	}

	// Nothing was written: no movement, and the cheque still RECEIVED.
	var movements int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM till_movements WHERE till_session_id = $1::uuid`, w.sessionID).Scan(&movements); err != nil {
		t.Fatalf("read the movements: %v", err)
	}
	if movements != 0 {
		t.Fatalf("the refused lodgement wrote %d movements", movements)
	}
	var status string
	if err := w.pool.QueryRow(context.Background(), `SELECT status FROM payments WHERE id = $1::uuid`, cheque).Scan(&status); err != nil {
		t.Fatalf("read the cheque: %v", err)
	}
	if status != "RECEIVED" {
		t.Fatalf("the refused lodgement marked the cheque %s", status)
	}

	// A cash lodgement may go without a slip: cash does not bounce.
	if err := w.svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "LODGEMENT", Method: "CASH",
		AmountCents: 30_00, Reason: "FW1-13 bank run",
	}); err != nil {
		t.Fatalf("a cash lodgement without a slip must be allowed: %v", err)
	}
}

// TestFW113TheCloseListsTheUnlodgedCheques is the screen's list (P1-1): the session detail names the
// drawer's unlodged cheques, the ones a lodgement's slip can carry, and a cheque that has been lodged
// leaves the list.
func TestFW113TheCloseListsTheUnlodgedCheques(t *testing.T) {
	w := newMJWorld(t)
	first := w.slipPayment("CHEQUE", 30_00)
	second := w.slipPayment("CHEQUE", 12_00)
	_ = w.slipPayment("CASH", 5_00) // cash is never on a cheque slip

	detail, err := w.svc.GetSession(staffCtx(w.userID, w.branchID), w.sessionID)
	if err != nil {
		t.Fatalf("the session detail: %v", err)
	}
	listed := map[string]int64{}
	for _, c := range detail.SlipCandidates {
		listed[c.PaymentID] = c.AmountCents
	}
	if len(listed) != 2 {
		t.Fatalf("the close lists %d unlodged cheques, want 2: %+v", len(listed), detail.SlipCandidates)
	}
	if listed[first] != 30_00 || listed[second] != 12_00 {
		t.Fatalf("the close lists the wrong cheques: %+v", listed)
	}
	for _, c := range detail.SlipCandidates {
		if c.Method != "CHEQUE" {
			t.Fatalf("the close lists a %s payment as a cheque candidate", c.Method)
		}
		if c.PaymentNo == "" {
			t.Fatalf("the close lists a cheque with no number: %+v", c)
		}
	}

	// The lodgement carries one of them, and that one leaves the list.
	if err := w.svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "LODGEMENT", Method: "CHEQUE",
		AmountCents: 30_00, Reason: "FW1-13 bank run", PaymentIDs: []string{first},
	}); err != nil {
		t.Fatalf("the cheque lodgement: %v", err)
	}
	detail, err = w.svc.GetSession(staffCtx(w.userID, w.branchID), w.sessionID)
	if err != nil {
		t.Fatalf("the session detail after the lodgement: %v", err)
	}
	listed = map[string]int64{}
	for _, c := range detail.SlipCandidates {
		listed[c.PaymentID] = c.AmountCents
	}
	if len(listed) != 1 || listed[second] != 12_00 {
		t.Fatalf("after the lodgement the close lists %+v, want only the second cheque", listed)
	}
}

// TestFW113TheSlipLodgementEmptiesTheDrawerOnce is the drawer's own math after a slip lodgement: the
// slip's cheques left the drawer ONCE. The slip marked them DEPOSITED (the bank's fact) and the
// lodgement is the drawer's statement that it banked them, so the expected is the drawer emptying by
// the slip's sum and NOT by the slip's sum twice. A drawer that read short by the whole slip would
// post a variance that never happened.
func TestFW113TheSlipLodgementEmptiesTheDrawerOnce(t *testing.T) {
	w := newMJWorld(t)
	ctx := staffCtx(w.userID, w.branchID)
	cheque := w.slipPayment("CHEQUE", 30_00)

	// The cheque is on the drawer: the expected carries it.
	z, err := w.svc.ZReportFor(ctx, w.sessionID)
	if err != nil {
		t.Fatalf("the z report: %v", err)
	}
	if got := tillChequeExpected(z); got != 30_00 {
		t.Fatalf("the drawer's expected cheque before the lodgement is %d, want the slip's 3000", got)
	}

	// The lodgement carries the slip.
	if err := w.svc.Lodge(ctx, till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "LODGEMENT", Method: "CHEQUE",
		AmountCents: 30_00, Reason: "FW1-13 bank run", PaymentIDs: []string{cheque},
	}); err != nil {
		t.Fatalf("the cheque lodgement: %v", err)
	}

	// The drawer holds no cheque now: the slip took it out, once.
	if err := w.svc.CountTill(ctx, till.CountInput{TillSessionID: w.sessionID, Tenders: []till.TenderActual{
		{Method: "CASH", ActualCents: 100_00}, {Method: "CHEQUE", ActualCents: 0},
	}}); err != nil {
		t.Fatalf("the count: %v", err)
	}
	z, err = w.svc.ZReportFor(ctx, w.sessionID)
	if err != nil {
		t.Fatalf("the z report after the lodgement: %v", err)
	}
	for _, tt := range z.PerTender {
		if tt.Method != "CHEQUE" {
			continue
		}
		if tt.ExpectedCents != 0 {
			t.Fatalf("the drawer's expected cheque is %d after the slip lodgement, want 0: the slip's cheques left the drawer once, not twice", tt.ExpectedCents)
		}
		if tt.VarianceCents != 0 {
			t.Fatalf("the drawer's cheque variance is %d after the slip lodgement, want 0", tt.VarianceCents)
		}
		return
	}
	t.Fatalf("the z report has no CHEQUE line: %+v", z.PerTender)
}

// tillChequeExpected reads the z report's cheque line expected (0 when the report has no cheque line).
func tillChequeExpected(z till.ZReport) int64 {
	for _, tt := range z.PerTender {
		if tt.Method == "CHEQUE" {
			return tt.ExpectedCents
		}
	}
	return 0
}

// TestFW113TheMatchDoesNotRestOnTheOutboxRow is P2 (raised from P3): the confirm-only rule must not
// rest on an outbox row. An outbox row can be archived, and the match would then post the deposit a
// second time. The lodgement's own journal, which names the movement for good, carries the fact.
func TestFW113TheMatchDoesNotRestOnTheOutboxRow(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()

	// A cash lodgement: no slip, so no link row can carry the fact. Its journal posts.
	if err := w.svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID, MovementType: "LODGEMENT", Method: "CASH",
		AmountCents: 30_00, Reason: "FW1-13 bank run",
	}); err != nil {
		t.Fatalf("the lodgement: %v", err)
	}
	var movementID string
	if err := w.pool.QueryRow(ctx,
		`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = 'LODGEMENT'`, w.sessionID).Scan(&movementID); err != nil {
		t.Fatalf("find the lodgement: %v", err)
	}
	w.drain()
	if bank := w.legs("till.movement.posted:" + movementID)["1000"]; bank[0] != 30_00 {
		t.Fatalf("the lodgement posted no bank debit at lodgement: got %v", w.legs("till.movement.posted:"+movementID))
	}

	// The outbox row is archived: the emission is gone, the journal is not.
	if _, err := w.pool.Exec(ctx, `DELETE FROM events_outbox WHERE topic = 'till.movement.posted' AND payload->>'movement_id' = $1`, movementID); err != nil {
		t.Fatalf("archive the outbox row: %v", err)
	}

	// The match still only confirms: the lodgement's own journal carries the fact.
	line := w.statementLine(30_00)
	bank := bankrec.NewService(bankrec.NewRepository(w.pool)).
		WithJournalPoster(glPosterOver{gl.NewService(gl.NewRepository(w.pool)).WithPaymentReader(till.NewRepository(w.pool))}).
		WithLodgements(till.NewRepository(w.pool)).
		WithJournalKeys(gl.NewService(gl.NewRepository(w.pool)))
	m, err := bank.ManualMatch(ctx, bankrec.MatchInput{
		Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetLodgement,
		TargetRefID: movementID, AmountCents: 30_00, CanAccessBranch: bankrec.AllBranches,
	}, w.userID)
	if err != nil {
		t.Fatalf("the lodgement's match: %v", err)
	}
	if legs := w.legs("bankrec:match:" + m.ID); len(legs) != 0 {
		t.Fatalf("a lodgement that carries its own journal must be confirmed, not journaled: got %v", legs)
	}
}

// TestFW113TheTillEmitsTheMovementsOwnInstant is the emission half of the dating rule (P3): the till
// writes the movement's own instant into the event payload, so the GL's builder dates the journal by
// the movement and never by the consumer's clock. The dated leg pins the builder's read; this one pins
// the till's write.
func TestFW113TheTillEmitsTheMovementsOwnInstant(t *testing.T) {
	w := newMJWorld(t)
	id := w.record("PAID_OUT", 12_34)

	var createdAt time.Time
	if err := w.pool.QueryRow(context.Background(),
		`SELECT created_at FROM till_movements WHERE id = $1::uuid`, id).Scan(&createdAt); err != nil {
		t.Fatalf("read the movement: %v", err)
	}
	var payload []byte
	if err := w.pool.QueryRow(context.Background(),
		`SELECT payload FROM events_outbox WHERE topic = 'till.movement.posted' AND payload->>'movement_id' = $1`, id).Scan(&payload); err != nil {
		t.Fatalf("read the emitted payload: %v", err)
	}
	var p struct {
		MovementID string    `json:"movement_id"`
		OccurredAt time.Time `json:"occurred_at"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("decode the emitted payload: %v", err)
	}
	if p.MovementID != id {
		t.Fatalf("the emitted payload names %q, want the movement %q", p.MovementID, id)
	}
	if p.OccurredAt.IsZero() {
		t.Fatalf("the emitted payload carries no instant: %s", payload)
	}
	if !p.OccurredAt.Equal(createdAt) {
		t.Fatalf("the emitted instant is %s, want the movement's own %s", p.OccurredAt, createdAt)
	}
}

// TestFW113OldNoLinkLodgementNextToNewSlipLodgementInOneStatement proves both lodgement match rules
// hold together when matched in one bank statement:
//   - a new cheque lodgement recorded with a deposit slip (link rows in till_lodgement_payments and
//     its own journal at lodgement) is confirm-only: its match posts no second journal, claims no new
//     payments, and answers PaymentsUnresolved = false;
//   - an older cheque lodgement without link rows or a lodgement journal keeps today's match (posting
//     DR 1000 / CR 1020), and the exact-set rule marks its unique session cheque DEPOSITED when unique
//     or carries PaymentsUnresolved = true when ambiguous.
func TestFW113OldNoLinkLodgementNextToNewSlipLodgementInOneStatement(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()

	var acct string
	if err := w.pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&acct); err != nil {
		t.Skipf("no seeded bank account: %v", err)
	}

	// Same session, two $25.00 cheques: one will be named on the new lodgement's slip (turning DEPOSITED
	// at lodgement), leaving the other as the unique RECEIVED $25.00 cheque for the older no-link lodgement.
	oldCheque := w.slipPayment("CHEQUE", 25_00)
	newCheque := w.slipPayment("CHEQUE", 25_00)

	// Two $40.00 cheques on the same session make a $40.00 older no-link lodgement ambiguous.
	ambigChequeA := w.slipPayment("CHEQUE", 40_00)
	ambigChequeB := w.slipPayment("CHEQUE", 40_00)

	newRef := fmt.Sprintf("MJ-NEW-SLIP-%d", time.Now().UnixNano())
	if err := w.svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID,
		MovementType:  "LODGEMENT",
		Method:        "CHEQUE",
		AmountCents:   25_00,
		Reason:        "FW1-13 new cheque slip",
		Reference:     newRef,
		PaymentIDs:    []string{newCheque},
	}); err != nil {
		t.Fatalf("lodge new cheque with slip: %v", err)
	}
	var newLodgID string
	if err := w.pool.QueryRow(ctx,
		`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND reference = $2`,
		w.sessionID, newRef).Scan(&newLodgID); err != nil {
		t.Fatalf("find new slip lodgement: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM till_lodgement_payments WHERE till_movement_id = $1::uuid`, newLodgID)
	})
	w.drain()

	// Older cheque lodgement with no link rows and no lodgement journal, uniquely matching oldCheque ($25.00).
	oldRef := fmt.Sprintf("MJ-OLD-NOSLIP-%d", time.Now().UnixNano())
	var oldLodgID string
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
		VALUES ($1::uuid, 'LODGEMENT', 'CHEQUE', 2500, 'pre-slip cheque deposit', $2, NULL)
		RETURNING id::text`, w.sessionID, oldRef).Scan(&oldLodgID); err != nil {
		t.Fatalf("seed older no-link cheque lodgement: %v", err)
	}

	// Second older cheque lodgement with no link rows whose $40.00 session cheques are ambiguous.
	unresRef := fmt.Sprintf("MJ-OLD-UNRES-%d", time.Now().UnixNano())
	var unresLodgID string
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
		VALUES ($1::uuid, 'LODGEMENT', 'CHEQUE', 4000, 'pre-slip ambiguous cheque deposit', $2, NULL)
		RETURNING id::text`, w.sessionID, unresRef).Scan(&unresLodgID); err != nil {
		t.Fatalf("seed older ambiguous cheque lodgement: %v", err)
	}

	day := w.privateDay()
	seedLine := func(ref string, cents int64) string {
		var lineID string
		if err := w.pool.QueryRow(ctx, `
			INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
			VALUES ($1::uuid, $2, 'FW1-13 STATEMENT DEPOSIT', $3, $4, $5, 'UNMATCHED')
			RETURNING id::text`,
			acct, day, ref, cents, bankrec.DedupeHash(day, cents, "FW1-13 STATEMENT DEPOSIT", ref)).Scan(&lineID); err != nil {
			t.Fatalf("seed statement line %s: %v", ref, err)
		}
		t.Cleanup(func() {
			c := context.Background()
			_, _ = w.pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1::uuid)`, lineID)
			_, _ = w.pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id = $1::uuid`, lineID)
			_, _ = w.pool.Exec(c, `DELETE FROM bank_matches WHERE statement_line_id = $1::uuid`, lineID)
			_, _ = w.pool.Exec(c, `DELETE FROM bank_statement_lines WHERE id = $1::uuid`, lineID)
		})
		return lineID
	}
	oldLine := seedLine(oldRef, 25_00)
	newLine := seedLine(newRef, 25_00)
	unresLine := seedLine(unresRef, 40_00)

	glSvc := gl.NewService(gl.NewRepository(w.pool)).WithPaymentReader(till.NewRepository(w.pool))
	bank := bankrec.NewService(bankrec.NewRepository(w.pool)).
		WithPayments(till.NewRepository(w.pool)).
		WithLodgements(till.NewRepository(w.pool)).
		WithJournalKeys(glSvc).
		WithJournalPoster(glPosterOver{glSvc})

	from := time.Now().UTC().Add(-24 * time.Hour)
	to := day.Add(48 * time.Hour)
	summary, err := bank.AutoMatchReach(ctx, acct, from, to, w.userID, func(b string) bool { return b == w.branchID })
	if err != nil {
		t.Fatalf("AutoMatchReach across combined statement: %v", err)
	}
	byLine := map[string]bankrec.Match{}
	for _, m := range summary.Matches {
		byLine[m.StatementLineID] = m
	}

	// 1. New slip lodgement: confirm-only, no second journal, no new payment claims, PaymentsUnresolved = false.
	newMatch, ok := byLine[newLine]
	if !ok {
		t.Fatalf("new slip lodgement line %s was not matched in summary: %+v", newLine, summary)
	}
	if newMatch.TargetRefID != newLodgID || newMatch.JournalID != "" || newMatch.PaymentsUnresolved {
		t.Fatalf("new slip lodgement match = %+v; want TargetRefID=%s, empty JournalID, PaymentsUnresolved=false", newMatch, newLodgID)
	}
	if legs := w.legs("bankrec:match:" + newMatch.ID); len(legs) != 0 {
		t.Fatalf("new slip lodgement match posted a second journal: %v", legs)
	}
	if bankLeg := w.legs("till.movement.posted:" + newLodgID)["1000"]; bankLeg[0] != 25_00 {
		t.Fatalf("new slip lodgement did not keep its lodgement-time DR 1000 journal: %v", w.legs("till.movement.posted:"+newLodgID))
	}
	var newStatus, newStmtLine string
	if err := w.pool.QueryRow(ctx,
		`SELECT status, COALESCE(metadata->>'bank_statement_line_id', '') FROM payments WHERE id = $1::uuid`,
		newCheque).Scan(&newStatus, &newStmtLine); err != nil {
		t.Fatalf("read newCheque: %v", err)
	}
	if newStatus != "DEPOSITED" || newStmtLine != "" {
		t.Fatalf("newCheque status=%s bank_statement_line_id=%q; want DEPOSITED at lodgement with no statement claim", newStatus, newStmtLine)
	}

	// 2. Older no-link lodgement with unique exact set: posts DR 1000 / CR 1020, marks oldCheque DEPOSITED, PaymentsUnresolved = false.
	oldMatch, ok := byLine[oldLine]
	if !ok {
		t.Fatalf("older no-link lodgement line %s was not matched in summary: %+v", oldLine, summary)
	}
	if oldMatch.TargetRefID != oldLodgID || oldMatch.JournalID == "" || oldMatch.PaymentsUnresolved {
		t.Fatalf("older no-link lodgement match = %+v; want TargetRefID=%s, non-empty JournalID, PaymentsUnresolved=false", oldMatch, oldLodgID)
	}
	oldLegs := w.legs("bankrec:match:" + oldMatch.ID)
	if oldLegs["1000"][0] != 25_00 || oldLegs["1020"][1] != 25_00 {
		t.Fatalf("older no-link lodgement match did not post DR 1000 / CR 1020 for 2500: got %v", oldLegs)
	}
	var oldStatus, oldStmtLine string
	if err := w.pool.QueryRow(ctx,
		`SELECT status, COALESCE(metadata->>'bank_statement_line_id', '') FROM payments WHERE id = $1::uuid`,
		oldCheque).Scan(&oldStatus, &oldStmtLine); err != nil {
		t.Fatalf("read oldCheque: %v", err)
	}
	if oldStatus != "DEPOSITED" || oldStmtLine != oldLine {
		t.Fatalf("oldCheque status=%s bank_statement_line_id=%q; want DEPOSITED claimed by line %s", oldStatus, oldStmtLine, oldLine)
	}

	// 3. Older no-link lodgement with ambiguous cheques: posts DR 1000 / CR 1020, claims neither cheque, carries PaymentsUnresolved = true.
	unresMatch, ok := byLine[unresLine]
	if !ok {
		t.Fatalf("older ambiguous lodgement line %s was not matched in summary: %+v", unresLine, summary)
	}
	if unresMatch.TargetRefID != unresLodgID || unresMatch.JournalID == "" || !unresMatch.PaymentsUnresolved {
		t.Fatalf("older ambiguous lodgement match = %+v; want TargetRefID=%s, non-empty JournalID, PaymentsUnresolved=true", unresMatch, unresLodgID)
	}
	unresLegs := w.legs("bankrec:match:" + unresMatch.ID)
	if unresLegs["1000"][0] != 40_00 || unresLegs["1020"][1] != 40_00 {
		t.Fatalf("older ambiguous lodgement match did not post DR 1000 / CR 1020 for 4000: got %v", unresLegs)
	}
	var stillReceived int
	if err := w.pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE id = ANY($1::uuid[]) AND status = 'RECEIVED'`,
		[]string{ambigChequeA, ambigChequeB}).Scan(&stillReceived); err != nil {
		t.Fatalf("read ambiguous cheques: %v", err)
	}
	if stillReceived != 2 {
		t.Fatalf("ambiguous older lodgement claimed %d of 2 cheques; want both still RECEIVED", 2-stillReceived)
	}
}

// TestFW113ManualMatchOfSlipLodgementClaimsNothing pins the manual arm (reviewer S4's read of
// PR 619, M4) through the real ManualMatch: a slip lodgement carries its own journal, so a MANUAL
// match of its deposit line posts no journal, claims nothing, and an unlodged RECEIVED cheque of
// the same amount on the same session stays RECEIVED. The auto arm's leg above cannot see this
// path: without the two manual gates in bankrec/service.go (the claim gated on not carrying a
// journal, and manualJournal's carries journal argument) a manual match of a slip lodgement would
// rescan the session's RECEIVED cheques and could claim the unlodged cheque of the same amount.
func TestFW113ManualMatchOfSlipLodgementClaimsNothing(t *testing.T) {
	w := newMJWorld(t)
	ctx := context.Background()

	var acct string
	if err := w.pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&acct); err != nil {
		t.Skipf("no seeded bank account: %v", err)
	}

	// Same session, two $45.00 cheques: one named on the slip (DEPOSITED at lodgement), the other
	// left in the drawer, RECEIVED, at exactly the slip's amount.
	lodgedCheque := w.slipPayment("CHEQUE", 45_00)
	unlodgedCheque := w.slipPayment("CHEQUE", 45_00)

	ref := fmt.Sprintf("MJ-MANUAL-SLIP-%d", time.Now().UnixNano())
	if err := w.svc.Lodge(staffCtx(w.userID, w.branchID), till.MovementInput{
		TillSessionID: w.sessionID,
		MovementType:  "LODGEMENT",
		Method:        "CHEQUE",
		AmountCents:   45_00,
		Reason:        "FW1-13 manual match slip",
		Reference:     ref,
		PaymentIDs:    []string{lodgedCheque},
	}); err != nil {
		t.Fatalf("lodge the cheque with its slip: %v", err)
	}
	var lodgID string
	if err := w.pool.QueryRow(ctx,
		`SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND reference = $2`,
		w.sessionID, ref).Scan(&lodgID); err != nil {
		t.Fatalf("find the slip lodgement: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM till_lodgement_payments WHERE till_movement_id = $1::uuid`, lodgID)
	})
	w.drain()

	day := w.privateDay()
	var lineID string
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1::uuid, $2, 'FW1-13 MANUAL DEPOSIT', $3, 4500, $4, 'UNMATCHED')
		RETURNING id::text`,
		acct, day, ref, bankrec.DedupeHash(day, 45_00, "FW1-13 MANUAL DEPOSIT", ref)).Scan(&lineID); err != nil {
		t.Fatalf("seed the statement line: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id = $1::uuid)`, lineID)
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id = $1::uuid`, lineID)
		_, _ = w.pool.Exec(c, `DELETE FROM bank_matches WHERE statement_line_id = $1::uuid`, lineID)
		_, _ = w.pool.Exec(c, `DELETE FROM bank_statement_lines WHERE id = $1::uuid`, lineID)
	})

	glSvc := gl.NewService(gl.NewRepository(w.pool)).WithPaymentReader(till.NewRepository(w.pool))
	bank := bankrec.NewService(bankrec.NewRepository(w.pool)).
		WithPayments(till.NewRepository(w.pool)).
		WithLodgements(till.NewRepository(w.pool)).
		WithJournalKeys(glSvc).
		WithJournalPoster(glPosterOver{glSvc})

	m, err := bank.ManualMatch(ctx, bankrec.MatchInput{
		StatementLineID: lineID,
		TargetType:      bankrec.TargetLodgement,
		TargetRefID:     lodgID,
		CanAccessBranch: func(b string) bool { return b == w.branchID },
	}, w.userID)
	if err != nil {
		t.Fatalf("manual match of the slip lodgement: %v", err)
	}

	// Posts no journal: the lodgement carried its own DR 1000 / CR 1020 at lodgement, so the match
	// only confirms the line.
	if m.JournalID != "" {
		t.Fatalf("the manual match of a slip lodgement posted journal %s; want confirm only", m.JournalID)
	}
	if legs := w.legs("bankrec:match:" + m.ID); len(legs) != 0 {
		t.Fatalf("the manual match of a slip lodgement posted a second journal: %v", legs)
	}
	if m.PaymentsUnresolved {
		t.Fatalf("the manual match of a slip lodgement answers PaymentsUnresolved; want false")
	}

	// Claims nothing: the drawer's other cheque of the same amount stays RECEIVED with no statement
	// line, and the slip's own cheque keeps its lodgement-time DEPOSITED with no statement line.
	var status, stmtLine string
	if err := w.pool.QueryRow(ctx,
		`SELECT status, COALESCE(metadata->>'bank_statement_line_id', '') FROM payments WHERE id = $1::uuid`,
		unlodgedCheque).Scan(&status, &stmtLine); err != nil {
		t.Fatalf("read the unlodged cheque: %v", err)
	}
	if status != "RECEIVED" || stmtLine != "" {
		t.Fatalf("the unlodged cheque is %s claimed by line %q; want RECEIVED with no statement claim", status, stmtLine)
	}
	if err := w.pool.QueryRow(ctx,
		`SELECT status, COALESCE(metadata->>'bank_statement_line_id', '') FROM payments WHERE id = $1::uuid`,
		lodgedCheque).Scan(&status, &stmtLine); err != nil {
		t.Fatalf("read the lodged cheque: %v", err)
	}
	if status != "DEPOSITED" || stmtLine != "" {
		t.Fatalf("the lodged cheque is %s claimed by line %q; want DEPOSITED from the lodgement with no statement claim", status, stmtLine)
	}

	// And the slip's own lodgement journal is still the deposit's only DR 1000.
	if bankLeg := w.legs("till.movement.posted:" + lodgID)["1000"]; bankLeg[0] != 45_00 {
		t.Fatalf("the slip lodgement did not keep its lodgement-time DR 1000 journal: %v", w.legs("till.movement.posted:"+lodgID))
	}
}
