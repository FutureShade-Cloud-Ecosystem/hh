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
