//go:build integration

// Integration tests run the real pgx Repository over the Postgres that the W5 verify loop provisions in the
// PRIVATE hos_w5_bankrec DB (migrations 0032-0033 + seeds). The GL is not merged in this worktree, so a
// DB-writing fake JournalPoster stands in for gl.PostExternal (it inserts a real gl_journals row so the
// bank_matches.journal_id FK holds and idempotency is exercised); the till read-seams are DB-free fakes.
//
//	psql ... CREATE DATABASE hos_w5_bankrec; go run ./cmd/migrate; go run ./cmd/seed
//	DATABASE_URL=...hos_w5_bankrec go test -tags integration ./internal/bankrec/... -p 1
package bankrec

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cleanTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`TRUNCATE bank_matches, bank_statement_lines, bank_statement_imports, bank_reconciliations RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	_, _ = pool.Exec(context.Background(), `
		UPDATE bank_accounts
		   SET cutover_date = NULL,
		       cutover_statement_balance_cents = NULL,
		       cutover_outstanding_journal_ids = '{}'::uuid[],
		       cutover_recorded_by = NULL,
		       cutover_recorded_at = NULL`)
}

func rbcAccountID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&id); err != nil {
		t.Fatalf("rbc account: %v", err)
	}
	return id
}

// aBranch returns a seeded location id (payments.branch_id is NOT NULL and FKs locations).
func aBranch(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded location: %v", err)
	}
	return id
}

// insertPayment seeds a real RECEIVED payment row (id fixed by the caller) so PersistMatch's in-tx claim
// (RECEIVED→DEPOSITED) has a real row to transition, the transactional payment-claim path (B2).
func insertPayment(t *testing.T, pool *pgxpool.Pool, id, branch, method, gatewayRef string, amount int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payments (id, payment_no, method, amount_cents, status, branch_id, gateway_ref)
		VALUES ($1::uuid, $2, $3, $4, 'RECEIVED', $5::uuid, $6)
		ON CONFLICT (id) DO UPDATE SET status = 'RECEIVED', gateway_ref = EXCLUDED.gateway_ref, updated_at = now()`,
		id, "PAY-"+id[:8], method, amount, branch, gatewayRef)
	if err != nil {
		t.Fatalf("insert payment: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, id) })
}

func paymentStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM payments WHERE id = $1::uuid`, id).Scan(&s); err != nil {
		t.Fatalf("payment status: %v", err)
	}
	return s
}

// insertLine writes an UNMATCHED statement line directly (bypassing import) for match/recon tests.
func insertLine(t *testing.T, pool *pgxpool.Pool, accountID string, date time.Time, desc, ref string, amount int64) string {
	t.Helper()
	hash := DedupeHash(date, amount, desc, ref)
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO bank_statement_lines (bank_account_id, txn_date, description, reference, amount_cents, dedupe_hash, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'UNMATCHED') RETURNING id::text`,
		accountID, date, desc, ref, amount, hash).Scan(&id)
	if err != nil {
		t.Fatalf("insert line: %v", err)
	}
	return id
}

// - DB-writing fake JournalPoster (stands in for the not-yet-merged gl.PostExternal) -
type dbPoster struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	seen map[string]string // idempotency_key → journal id
}

func newDBPoster(pool *pgxpool.Pool) *dbPoster {
	return &dbPoster{pool: pool, seen: map[string]string{}}
}

func (p *dbPoster) PostJournal(ctx context.Context, in gl.ExternalJournal) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if in.IdempotencyKey != "" {
		if id, ok := p.seen[in.IdempotencyKey]; ok {
			return id, nil // idempotent replay
		}
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var periodID string
	var year, month int
	if err := tx.QueryRow(ctx, `SELECT id::text, year, month FROM fiscal_periods WHERE year=2026 AND month=6`).
		Scan(&periodID, &year, &month); err != nil {
		return "", err
	}
	var journalID string
	err = tx.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, idempotency_key)
		VALUES ($1, (SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year=$1), $2, $3, $4, 'POSTED', now(), $5)
		ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING
		RETURNING id::text`,
		year, periodID, in.Source, in.Memo, in.IdempotencyKey).Scan(&journalID)
	if err != nil {
		// conflict → fetch existing
		if in.IdempotencyKey == "" {
			return "", err
		}
		if ferr := tx.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, in.IdempotencyKey).Scan(&journalID); ferr != nil {
			return "", ferr
		}
		p.seen[in.IdempotencyKey] = journalID
		return journalID, tx.Commit(ctx)
	}
	lineNo := 1
	for _, l := range in.Lines {
		// gl_journal_lines CHECK: exactly one of debit/credit is NOT NULL and > 0.
		var debit, credit any
		if l.DebitCents > 0 {
			debit = l.DebitCents
		} else {
			credit = l.CreditCents
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, memo)
			VALUES ($1, $2, (SELECT id FROM gl_accounts WHERE code = $3), $4, $5, $6)`,
			journalID, lineNo, l.AccountCode, debit, credit, l.Memo); err != nil {
			return "", err
		}
		lineNo++
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	p.seen[in.IdempotencyKey] = journalID
	return journalID, nil
}

// - DB-free till seam fakes -
type fakePayments struct {
	cands     []CandidatePayment
	deposited map[string]string // paymentID → statementLineID
}

func (f *fakePayments) UnreconciledCardPayments(_ context.Context, _, _ time.Time) ([]CandidatePayment, error) {
	var out []CandidatePayment
	for _, c := range f.cands {
		if _, done := f.deposited[c.PaymentID]; !done {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *fakePayments) MarkDeposited(_ context.Context, ids []string, lineID string) error {
	if f.deposited == nil {
		f.deposited = map[string]string{}
	}
	for _, id := range ids {
		f.deposited[id] = lineID
	}
	return nil
}

type fakeLodgements struct{ open []Lodgement }

func (f *fakeLodgements) LodgementFacts(_ context.Context, id string) (LodgementFact, bool, error) {
	for _, l := range f.open {
		if l.MovementID == id {
			return LodgementFact{BranchID: l.BranchID, AmountCents: l.AmountCents}, true, nil
		}
	}
	return LodgementFact{}, false, nil
}

func (f *fakeLodgements) OpenLodgements(_ context.Context, _, _ time.Time) ([]Lodgement, error) {
	return f.open, nil
}

type fakeBalances struct{ val int64 }

func (f *fakeBalances) GLBalanceCents(_ context.Context, _ string, _, _ int, _ string) (int64, error) {
	return f.val, nil
}

type fakeSource struct {
	lines  []RawStatementLine
	called bool
}

func (f *fakeSource) Parse(_ context.Context, _ ImportProfile, _ io.Reader) ([]RawStatementLine, error) {
	f.called = true
	return f.lines, nil
}

func win() (time.Time, time.Time) {
	return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
}

// - AC1: idempotent re-import from the golden fixture -
func TestIntegration_AC1_ReimportDedupe(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	svc := NewService(NewRepository(pool))
	content, err := os.ReadFile("testdata/rbc_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	in := ImportInput{BankAccountID: acct, ProfileCode: "rbc-default", Filename: "rbc.csv", Content: string(content)}

	first, err := svc.Import(context.Background(), in, "")
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Status != ImportStatusImported || first.Inserted != 5 {
		t.Fatalf("first import: status=%s inserted=%d (want IMPORTED, 5)", first.Status, first.Inserted)
	}
	second, err := svc.Import(context.Background(), in, "")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Inserted != 0 || second.Duplicates != 5 {
		t.Fatalf("re-import not idempotent: inserted=%d duplicates=%d", second.Inserted, second.Duplicates)
	}
}

// - AC8: pluggable StatementSource (injected parser wins) -
func TestIntegration_AC8_PluggableSource(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	d := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	fake := &fakeSource{lines: []RawStatementLine{
		{TxnDate: d, Description: "FEED LINE A", Reference: "F1", AmountCents: 1000, DedupeHash: DedupeHash(d, 1000, "FEED LINE A", "F1")},
		{TxnDate: d, Description: "FEED LINE B", Reference: "F2", AmountCents: 2000, DedupeHash: DedupeHash(d, 2000, "FEED LINE B", "F2")},
	}}
	svc := NewService(NewRepository(pool)).WithStatementSource(fake)
	res, err := svc.Import(context.Background(), ImportInput{BankAccountID: acct, ProfileCode: "rbc-default", Content: "ignored-by-fake"}, "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !fake.called {
		t.Fatal("injected StatementSource was not used")
	}
	if res.Inserted != 2 {
		t.Fatalf("expected 2 inserted from the fake feed, got %d", res.Inserted)
	}
}

// - AC2: gateway-ref match → payment DEPOSITED + DR1000/CR1010 journal linked -
func TestIntegration_AC2_GatewayMatch(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	d := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	lineID := insertLine(t, pool, acct, d, "RUN-PAY CARD SETTLEMENT", "GTW-1001", 100000)

	const payID = "11111111-1111-4111-8111-111111111111"
	insertPayment(t, pool, payID, branch, "CARD", "GTW-1001", 100000)
	pay := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, GatewayRef: "GTW-1001", AmountCents: 100000, ReceivedAt: d}}}
	poster := newDBPoster(pool)
	svc := NewService(NewRepository(pool)).WithPayments(pay).WithJournalPoster(poster)
	from, to := win()
	sum, err := svc.AutoMatchReach(context.Background(), acct, from, to, "", AllBranches)
	if err != nil {
		t.Fatalf("automatch: %v", err)
	}
	if sum.Matched != 1 {
		t.Fatalf("expected 1 match, got %d", sum.Matched)
	}
	assertLineStatus(t, pool, lineID, LineMatched)
	// The payment was CLAIMED transactionally inside PersistMatch (RECEIVED→DEPOSITED).
	if st := paymentStatus(t, pool, payID); st != "DEPOSITED" {
		t.Fatalf("payment status = %s, want DEPOSITED (in-tx claim)", st)
	}
	journalID, target := matchFacts(t, pool, lineID)
	if target != TargetPayment {
		t.Fatalf("target = %s, want PAYMENT", target)
	}
	assertJournalLegs(t, pool, journalID, map[string][2]int64{
		AcctBank:         {100000, 0},
		AcctCardClearing: {0, 100000},
	})
}

// - AC3: lodgement zero-tolerance match → DR1000/CR1020 -
func TestIntegration_AC3_LodgementMatch(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	d := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	lineID := insertLine(t, pool, acct, d, "CASH LODGEMENT DEPOSIT", "LODGE-77", 50000)

	lodg := &fakeLodgements{open: []Lodgement{{MovementID: "22222222-2222-4222-8222-222222222222", Reference: "LODGE-77", AmountCents: 50000, OccurredAt: d}}}
	poster := newDBPoster(pool)
	svc := NewService(NewRepository(pool)).WithLodgements(lodg).WithJournalPoster(poster)
	from, to := win()
	sum, err := svc.AutoMatchReach(context.Background(), acct, from, to, "", AllBranches)
	if err != nil {
		t.Fatalf("automatch: %v", err)
	}
	if sum.Matched != 1 {
		t.Fatalf("expected 1 match, got %d", sum.Matched)
	}
	journalID, target := matchFacts(t, pool, lineID)
	if target != TargetLodgement {
		t.Fatalf("target = %s, want LODGEMENT", target)
	}
	assertJournalLegs(t, pool, journalID, map[string][2]int64{
		AcctBank:        {50000, 0},
		AcctUndeposited: {0, 50000},
	})
}

// - AC4: RUN settlement fee-split, idempotent re-run -
func TestIntegration_AC4_FeeSplitIdempotent(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	d := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	lineID := insertLine(t, pool, acct, d, "RUN BATCH SETTLEMENT", "", 98000)

	const payID = "33333333-3333-4333-8333-333333333333"
	insertPayment(t, pool, payID, branch, "CARD", "", 100000)
	pay := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, AmountCents: 100000, ReceivedAt: d}}}
	poster := newDBPoster(pool)
	svc := NewService(NewRepository(pool)).WithPayments(pay).WithJournalPoster(poster)
	from, to := win()

	sum, err := svc.AutoMatchReach(context.Background(), acct, from, to, "", AllBranches)
	if err != nil {
		t.Fatalf("automatch: %v", err)
	}
	if sum.Matched != 1 {
		t.Fatalf("expected 1 match, got %d", sum.Matched)
	}
	journalID, _ := matchFacts(t, pool, lineID)
	assertJournalLegs(t, pool, journalID, map[string][2]int64{
		AcctBank:         {98000, 0},
		"6300":           {2000, 0},
		AcctCardClearing: {0, 100000},
	})
	firstCount := countJournals(t, pool)

	// Re-run: the line is already MATCHED, so nothing new matches and no second journal posts.
	sum2, err := svc.AutoMatchReach(context.Background(), acct, from, to, "", AllBranches)
	if err != nil {
		t.Fatalf("automatch replay: %v", err)
	}
	if sum2.Matched != 0 {
		t.Fatalf("replay matched %d (want 0)", sum2.Matched)
	}
	if countJournals(t, pool) != firstCount {
		t.Fatal("replay posted a second journal (not idempotent)")
	}
	// The poster is idempotent on the match-keyed clearing key: re-posting under the SAME match's key returns
	// the existing journal id, never a second journal.
	var matchID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM bank_matches WHERE statement_line_id = $1 AND unmatched_at IS NULL`, lineID).Scan(&matchID); err != nil {
		t.Fatalf("match id: %v", err)
	}
	id1, _ := poster.PostJournal(context.Background(), gl.ExternalJournal{IdempotencyKey: clearingKey(matchID), Source: "ADJUSTMENT"})
	if id1 != journalID {
		t.Fatalf("poster returned a different id for the same key: %s vs %s", id1, journalID)
	}
}

// - B2: a second match on an already-matched payment fails, posting NO second clearing journal -
func TestIntegration_B2_NoDoubleMatchOrJournal(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()
	d := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)

	const payID = "99999999-9999-4999-8999-999999999999"
	insertPayment(t, pool, payID, branch, "CARD", "GTW-DUP", 40000)
	line1 := insertLine(t, pool, acct, d, "RUN-PAY CARD SETTLEMENT", "GTW-DUP", 40000)
	line2 := insertLine(t, pool, acct, d.AddDate(0, 0, 1), "DUPLICATE CARD SETTLEMENT", "GTW-DUP", 40000)

	pay := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, GatewayRef: "GTW-DUP", AmountCents: 40000, ReceivedAt: d}}}
	poster := newDBPoster(pool)
	svc := NewService(NewRepository(pool)).WithPayments(pay).WithJournalPoster(poster)
	from, to := win()

	// First match claims the payment + posts one clearing journal.
	if _, err := svc.AutoMatchReach(ctx, acct, from, to, "", AllBranches); err != nil {
		t.Fatalf("first automatch: %v", err)
	}
	assertLineStatus(t, pool, line1, LineMatched)
	if st := paymentStatus(t, pool, payID); st != "DEPOSITED" {
		t.Fatalf("payment not claimed: %s", st)
	}
	baseline := countJournals(t, pool)

	// Second attempt: a MANUAL match of a different statement line to the SAME (now-DEPOSITED) payment must be
	// rejected by the in-tx payment-claim guard (RowsAffected != expected), and post NO second journal.
	_, err := svc.ManualMatch(ctx, MatchInput{CanAccessBranch: AllBranches,
		Mode: "manual", StatementLineID: line2, TargetType: TargetPayment, TargetRefID: payID,
	}, "")
	if err == nil {
		t.Fatal("expected the second match on an already-claimed payment to fail")
	}
	if countJournals(t, pool) != baseline {
		t.Fatal("a rejected double-match posted a second clearing journal")
	}
	assertLineStatus(t, pool, line2, LineUnmatched)
	if st := paymentStatus(t, pool, payID); st != "DEPOSITED" {
		t.Fatalf("payment status changed on a rejected match: %s", st)
	}
}

// - DIB-32: unmatch reverses the payment clearing (status + GL both symmetric) -
// Seeds an RBC credit line + a RECEIVED payment (gateway-ref match). AutoMatch posts the DR1000/CR1010
// clearing journal (AC2 legs) and claims the payment RECEIVED→DEPOSITED. Unmatch(matchID) must then be the
// exact inverse: release the payment DEPOSITED→RECEIVED, revert the line to UNMATCHED (match row stamped
// unmatched_at, history kept), and post a DISTINCT reversal journal under unmatchKey(matchID) whose legs are
// the per-account offset of the clearing legs (DR1010=amount / CR1000=amount). Ground truth: the two
// idempotency-keyed journals net to zero on both 1000 and 1010, mirroring the AC2 clearing legs.
func TestIntegration_Unmatch_ReversesPaymentClearing(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()
	d := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)

	const amount = int64(77700)
	const payID = "32323232-3232-4323-8323-323232323232"
	lineID := insertLine(t, pool, acct, d, "RUN-PAY CARD SETTLEMENT DIB32", "GTW-UNMATCH-32", amount)
	insertPayment(t, pool, payID, branch, "CARD", "GTW-UNMATCH-32", amount)
	pay := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, GatewayRef: "GTW-UNMATCH-32", AmountCents: amount, ReceivedAt: d}}}
	poster := newDBPoster(pool)
	svc := NewService(NewRepository(pool)).WithPayments(pay).WithJournalPoster(poster)
	from, to := win()

	// Match: post the AC2 clearing legs (DR1000/CR1010) and claim the payment.
	sum, err := svc.AutoMatchReach(ctx, acct, from, to, "", AllBranches)
	if err != nil {
		t.Fatalf("automatch: %v", err)
	}
	if sum.Matched != 1 {
		t.Fatalf("expected 1 match, got %d", sum.Matched)
	}
	assertLineStatus(t, pool, lineID, LineMatched)
	if st := paymentStatus(t, pool, payID); st != "DEPOSITED" {
		t.Fatalf("payment status after match = %s, want DEPOSITED", st)
	}
	clearingID, target := matchFacts(t, pool, lineID)
	if target != TargetPayment {
		t.Fatalf("target = %s, want PAYMENT", target)
	}
	// The clearing legs we will later reverse (AC2 shape).
	assertJournalLegs(t, pool, clearingID, map[string][2]int64{
		AcctBank:         {amount, 0},
		AcctCardClearing: {0, amount},
	})

	// The live match id (before it is stamped unmatched), the key both journals hang off of.
	var matchID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM bank_matches WHERE statement_line_id = $1 AND unmatched_at IS NULL`, lineID).Scan(&matchID); err != nil {
		t.Fatalf("live match id: %v", err)
	}

	// Unmatch: the inverse of the match.
	if err := svc.UnmatchReach(ctx, matchID, "", AllBranches); err != nil {
		t.Fatalf("unmatch: %v", err)
	}

	// (a) the payment is released DEPOSITED→RECEIVED.
	if st := paymentStatus(t, pool, payID); st != "RECEIVED" {
		t.Fatalf("payment status after unmatch = %s, want RECEIVED (released)", st)
	}
	// (b) the line reverts to UNMATCHED, the match row is stamped unmatched_at, and history is kept.
	assertLineStatus(t, pool, lineID, LineUnmatched)
	hist, err := svc.MatchHistory(ctx, lineID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("expected 1 historical match row (kept), got %d", len(hist))
	}
	if hist[0].UnmatchedAt == nil {
		t.Fatal("match row was not stamped unmatched_at")
	}
	if hist[0].ID != matchID {
		t.Fatalf("history match id = %s, want %s", hist[0].ID, matchID)
	}

	// (c) a DISTINCT reversal journal exists under unmatchKey(matchID) with legs DR1010=amount / CR1000=amount.
	var reversalID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(matchID)).Scan(&reversalID); err != nil {
		t.Fatalf("reversal journal lookup (key %s): %v", unmatchKey(matchID), err)
	}
	if reversalID == clearingID {
		t.Fatalf("reversal journal is not distinct from the clearing journal (both %s)", reversalID)
	}
	assertJournalLegs(t, pool, reversalID, map[string][2]int64{
		AcctCardClearing: {amount, 0},
		AcctBank:         {0, amount},
	})

	// (d) summing clearing + reversal nets to zero on both 1000 and 1010 (GL detail across the two keys).
	for _, code := range []string{AcctBank, AcctCardClearing} {
		var net int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(l.debit_cents),0) - COALESCE(SUM(l.credit_cents),0)
			FROM gl_journal_lines l
			JOIN gl_accounts a ON a.id = l.account_id
			JOIN gl_journals j ON j.id = l.journal_id
			WHERE j.idempotency_key IN ($1, $2) AND a.code = $3`,
			clearingKey(matchID), unmatchKey(matchID), code).Scan(&net); err != nil {
			t.Fatalf("net for %s: %v", code, err)
		}
		if net != 0 {
			t.Fatalf("account %s net across clearing+reversal = %d, want 0", code, net)
		}
	}
}

// - AC5: rule approval / versioning (supersedes chain) -
func TestIntegration_AC5_RuleVersioning(t *testing.T) {
	pool := intPool(t)
	svc := NewService(NewRepository(pool))
	in := RuleInput{
		Code: "gateway-ref-exact", Name: "Gateway exact v2", Priority: 10,
		Spec: map[string]any{"kind": "gateway_ref_exact", "target": "PAYMENT", "tolerance_cents": float64(0)},
	}
	newRule, err := svc.ApproveRule(context.Background(), in, "")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if newRule.Version < 2 {
		t.Fatalf("expected version >= 2, got %d", newRule.Version)
	}
	if newRule.SupersedesID == "" {
		t.Fatal("new rule does not point at the superseded rule")
	}
	// Exactly one APPROVED version per code.
	var approved int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM bank_match_rules WHERE code='gateway-ref-exact' AND status='APPROVED'`).Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if approved != 1 {
		t.Fatalf("expected exactly 1 APPROVED gateway-ref-exact, got %d", approved)
	}
	// The superseded row is RETIRED.
	var oldStatus string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM bank_match_rules WHERE id = $1`, newRule.SupersedesID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != RuleRetired {
		t.Fatalf("superseded rule status = %s, want RETIRED", oldStatus)
	}
}

// - AC6: manual match + unmatch keeps history -
func TestIntegration_AC6_ManualMatchHistory(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	d := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	lineID := insertLine(t, pool, acct, d, "MANUAL DEPOSIT", "MAN-1", 12345)
	poster := newDBPoster(pool)
	// A manual LODGEMENT target must exist (SF-2g round 2): the till's seam holds this one.
	lodgements := &fakeLodgements{open: []Lodgement{{MovementID: "44444444-4444-4444-8444-444444444444", BranchID: aBranch(t, pool), AmountCents: 12345}}}
	svc := NewService(NewRepository(pool)).WithJournalPoster(poster).WithLodgements(lodgements)

	m, err := svc.ManualMatch(context.Background(), MatchInput{CanAccessBranch: AllBranches,
		Mode: "manual", StatementLineID: lineID, TargetType: TargetLodgement, TargetRefID: "44444444-4444-4444-8444-444444444444",
	}, "")
	if err != nil {
		t.Fatalf("manual match: %v", err)
	}
	assertLineStatus(t, pool, lineID, LineMatched)

	if err := svc.UnmatchReach(context.Background(), m.ID, "", AllBranches); err != nil {
		t.Fatalf("unmatch: %v", err)
	}
	assertLineStatus(t, pool, lineID, LineUnmatched)

	hist, err := svc.MatchHistory(context.Background(), lineID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].UnmatchedAt == nil {
		t.Fatalf("expected 1 unmatched historical row, got %d", len(hist))
	}
	// Re-match is allowed once the line is UNMATCHED again; history grows.
	if _, err := svc.ManualMatch(context.Background(), MatchInput{CanAccessBranch: AllBranches,
		Mode: "manual", StatementLineID: lineID, TargetType: TargetLodgement, TargetRefID: "44444444-4444-4444-8444-444444444444",
	}, ""); err != nil {
		t.Fatalf("re-match: %v", err)
	}
	hist2, _ := svc.MatchHistory(context.Background(), lineID)
	if len(hist2) != 2 {
		t.Fatalf("expected 2 history rows after re-match, got %d", len(hist2))
	}
}

// - AC7: completion guards (no unmatched, zero variance) + emission -
func TestIntegration_AC7_ReconGuardsAndEmission(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	d := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	lineID := insertLine(t, pool, acct, d, "DEPOSIT TO RECONCILE", "REC-1", 70000)
	ctx := context.Background()

	// Balance reader = 0 → variance 70000; the line is still UNMATCHED → completion must be blocked twice over.
	svc0 := NewService(NewRepository(pool)).WithGLBalances(&fakeBalances{val: 0})
	rec, err := svc0.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: 2026, Month: 6, StatementBalanceCents: 70000})
	if err != nil {
		t.Fatalf("open recon: %v", err)
	}
	if rec.VarianceCents != 70000 {
		t.Fatalf("variance = %d, want 70000", rec.VarianceCents)
	}
	if _, err := svc0.CompleteReconciliation(ctx, rec.ID, ""); err == nil {
		t.Fatal("expected completion to be blocked (unmatched line + non-zero variance)")
	}

	// Match the line manually so 0 unmatched remain.
	poster := newDBPoster(pool)
	lodgementsM := &fakeLodgements{open: []Lodgement{{MovementID: "55555555-5555-4555-8555-555555555555", BranchID: aBranch(t, pool), AmountCents: 70000}}}
	svcM := NewService(NewRepository(pool)).WithJournalPoster(poster).WithLodgements(lodgementsM)
	if _, err := svcM.ManualMatch(ctx, MatchInput{CanAccessBranch: AllBranches, Mode: "manual", StatementLineID: lineID, TargetType: TargetLodgement, TargetRefID: "55555555-5555-4555-8555-555555555555"}, ""); err != nil {
		t.Fatalf("manual match: %v", err)
	}

	// Balance reader now equals the statement balance → variance 0; re-open refreshes the row.
	svc := NewService(NewRepository(pool)).WithGLBalances(&fakeBalances{val: 70000})
	rec2, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: acct, Year: 2026, Month: 6, StatementBalanceCents: 70000})
	if err != nil {
		t.Fatalf("reopen recon: %v", err)
	}
	if rec2.VarianceCents != 0 {
		t.Fatalf("variance after balancing = %d, want 0", rec2.VarianceCents)
	}
	done, err := svc.CompleteReconciliation(ctx, rec2.ID, "")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.Status != ReconCompleted {
		t.Fatalf("status = %s, want COMPLETED", done.Status)
	}
	// bank.recon.completed was emitted in the same tx.
	var events int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM events_outbox WHERE topic = $1`, TopicReconCompleted).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events < 1 {
		t.Fatal("bank.recon.completed was not emitted")
	}
}

// - shared assertion helpers -
func assertLineStatus(t *testing.T, pool *pgxpool.Pool, lineID, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM bank_statement_lines WHERE id = $1`, lineID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("line status = %s, want %s", got, want)
	}
}

func matchFacts(t *testing.T, pool *pgxpool.Pool, lineID string) (journalID, target string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(journal_id::text,''), target_type FROM bank_matches
		 WHERE statement_line_id = $1 AND unmatched_at IS NULL`, lineID).Scan(&journalID, &target); err != nil {
		t.Fatalf("match facts: %v", err)
	}
	if journalID == "" {
		t.Fatal("live match has no journal_id")
	}
	return journalID, target
}

// assertJournalLegs checks the posted journal's per-account debit/credit totals ([debit, credit]).
func assertJournalLegs(t *testing.T, pool *pgxpool.Pool, journalID string, want map[string][2]int64) {
	t.Helper()
	for code, dc := range want {
		var debit, credit int64
		err := pool.QueryRow(context.Background(), `
			SELECT COALESCE(SUM(l.debit_cents),0), COALESCE(SUM(l.credit_cents),0)
			FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
			WHERE l.journal_id = $1 AND a.code = $2`, journalID, code).Scan(&debit, &credit)
		if err != nil {
			t.Fatalf("journal legs %s: %v", code, err)
		}
		if debit != dc[0] || credit != dc[1] {
			t.Fatalf("account %s legs = (DR %d, CR %d), want (DR %d, CR %d)", code, debit, credit, dc[0], dc[1])
		}
	}
}

func countJournals(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM gl_journals`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
