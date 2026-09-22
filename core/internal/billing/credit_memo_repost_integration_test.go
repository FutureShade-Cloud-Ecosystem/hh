//go:build integration

package billing

// credit_memo_repost_integration_test.go, DIB-429's load-bearing half.
//
// The unit test drives the loop over a fixed pending set. THE QUERY is what can actually be wrong,
// and only a real database can say so: an anti-join against a string-concatenated idempotency key,
// over a doc_type predicate, returning amounts whose SIGN has to be flipped back. Every one of those
// is a place to be quietly wrong in a way that a fake store cannot notice.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fixtureEpoch is where this file's rows live on the timeline. It is compared only against other
// fixture values (a memo's own finalized_at, read back and asserted against the same constant), so
// no real clock can arm it, the DIB-427 rule.
var fixtureEpoch = time.Date(1990, 6, 1, 12, 0, 0, 0, time.UTC)

// seedCreditMemo writes a credit memo the way billing does, amounts NEGATIVE, doc_type CREDIT_MEMO
// , and cleans up after itself.
func seedCreditMemo(t *testing.T, pool *pgxpool.Pool, customerID, no string, subtotal, tax int64, finalizedAt time.Time) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO invoices
		   (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		    subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1,$2,$3::uuid,'FINALIZED',$4::date,$4::date,$5,$6,$7,$7,$8,'CREDIT_MEMO')
		 RETURNING id::text`,
		no, customerID, trentonBranch, finalizedAt.Format("2006-01-02"),
		-subtotal, -tax, -(subtotal + tax), finalizedAt).Scan(&id); err != nil {
		t.Fatalf("seed credit memo %s: %v", no, err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`,
			creditMemoJournalKey(id))
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, creditMemoJournalKey(id))
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1`, id)
	})
	return id
}

// markJournalPosted writes the gl_journals row a successful post would have left, POSTED, with two
// balanced legs (the unused side is NULL, not 0, the CHECK admits exactly one side per leg, which
// is the same "a leg of -$500 is not representable" rule the reversing entry is built around). The
// legs are what make
// the fixture faithful: the anti-join is keyed on idempotency_key alone, and a fixture that could
// only produce a DRAFT would leave the test unable to say whether a REAL posted journal is excluded.
func markJournalPosted(t *testing.T, pool *pgxpool.Pool, creditMemoID string) {
	t.Helper()
	ctx := context.Background()
	periodID := repostScratchPeriod(t, pool)
	accountA, accountB := repostScratchAccounts(t, pool)
	var journalID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, idempotency_key, branch_id)
		 VALUES ($4, (SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year = $4), $1::uuid, 'ADJUSTMENT',
		         'repost fixture', 'DRAFT', $2, $3::uuid)
		 RETURNING id::text`,
		periodID, creditMemoJournalKey(creditMemoID), trentonBranch, repostFixtureYear).Scan(&journalID); err != nil {
		t.Fatalf("mark journal posted for %s: %v", creditMemoID, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, branch_id, memo)
		 VALUES ($1::uuid, 1, $2::uuid, 100, NULL, $4::uuid, 'repost fixture'),
		        ($1::uuid, 2, $3::uuid, NULL, 100, $4::uuid, 'repost fixture')`,
		journalID, accountA, accountB, trentonBranch); err != nil {
		t.Fatalf("write fixture journal lines: %v", err)
	}
	// posted_at is the FIXTURE's own instant, not now(). Nothing asserts this column, but a SQL
	// clock in a file that also holds fixed dates is the combination the DIB-427 tripwire catches,
	// and the cheapest answer is not to introduce a second clock at all.
	if _, err := pool.Exec(ctx,
		`UPDATE gl_journals SET status = 'POSTED', posted_at = $2 WHERE id = $1::uuid`,
		journalID, fixtureEpoch); err != nil {
		t.Fatalf("post the fixture journal: %v", err)
	}
}

// The anti-join: a memo WITHOUT a journal is found, a memo WITH one is not, and an ordinary INVOICE
// is never in the set at all.
func TestIntegration_RepostCreditMemos_FindsOnlyMemosMissingTheirJournal(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, "repost")
	when := fixtureEpoch

	missing := seedCreditMemo(t, pool, cust, "#CM-REPOST-MISSING", 100_00, 13_00, when)
	posted := seedCreditMemo(t, pool, cust, "#CM-REPOST-POSTED", 50_00, 6_50, when)
	markJournalPosted(t, pool, posted)
	// An ordinary invoice, which has its own journal path and must never be swept here.
	seedInvoice(t, pool, cust, "REPOST", invSpec{no: "INV", status: "FINALIZED", subtotal: 900_00, tax: 117_00, total: 1_017_00, balance: 1_017_00, finalized: true})

	w := NewInvoiceWriter(pool)
	found, err := w.findCreditMemosWithoutJournals(ctx, 500)
	if err != nil {
		t.Fatalf("anti-join: %v", err)
	}

	var sawMissing, sawPosted bool
	for _, m := range found {
		switch m.id {
		case missing:
			sawMissing = true
			// The SIGN, flipped back. A memo is stored negative; the journal takes POSITIVE
			// magnitudes with the roles swapped, and gl_journal_lines' CHECK makes a negative leg
			// unrepresentable, so getting this wrong fails at the constraint, far from here.
			if m.arCents != 113_00 || m.revenueCents != 100_00 || m.taxCents != 13_00 {
				t.Fatalf("magnitudes came back ar=%d revenue=%d tax=%d, want positive 11300/10000/1300",
					m.arCents, m.revenueCents, m.taxCents)
			}
			if !m.occurredAt.Equal(when) {
				t.Fatalf("occurredAt=%s, want the memo's own %s", m.occurredAt, when)
			}
		case posted:
			sawPosted = true
		}
		if m.no == "REPOST-INV" {
			t.Fatalf("an ordinary invoice (%s) was swept as a credit memo", m.no)
		}
	}
	if !sawMissing {
		t.Fatal("the memo with no journal was not found, the sweep repairs nothing")
	}
	if sawPosted {
		t.Fatal("a memo that ALREADY has its journal was found; the sweep would post a second one")
	}
}

// End to end against the real query, with a poster: the memo is repaired, and a SECOND run finds
// nothing because the journal it wrote is now on the other side of the anti-join.
//
// That second run is the assertion that matters. The jobs runner has no lease: a sweep still running
// when its next window opens is simply started again, so "safe to run twice" is not a nicety.
func TestIntegration_RepostCreditMemos_RepairsTheMemoAndIsIdempotent(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust := seedCustomer(t, pool, "repost2")
	when := fixtureEpoch.AddDate(0, 0, 1)
	memo := seedCreditMemo(t, pool, cust, "#CM-REPOST-E2E", 200_00, 26_00, when)

	// A poster that writes the same row a real post would, so the second sweep has something real to
	// find. It records what it was asked for so the magnitudes can be asserted on the way through.
	p := &fixturePoster{t: t, pool: pool}
	w := NewInvoiceWriter(pool).WithCreditMemoPoster(p)

	first, err := w.RepostFailedCreditMemoJournals(ctx, 500)
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if first.Reposted < 1 {
		t.Fatalf("first sweep reposted %d, want at least the seeded memo (%+v)", first.Reposted, first)
	}
	var mine bool
	for _, j := range p.got {
		if j.CreditMemoID == memo {
			mine = true
			if j.ARCents != 226_00 || j.RevenueCents != 200_00 || j.TaxCents != 26_00 {
				t.Fatalf("posted magnitudes %+v, want 22600/20000/2600 positive", j)
			}
			if j.IdempotencyKey != creditMemoJournalKey(memo) {
				t.Fatalf("posted under key %q, want the derived %q", j.IdempotencyKey, creditMemoJournalKey(memo))
			}
		}
	}
	if !mine {
		t.Fatal("the seeded memo was not among the re-posted")
	}

	before := len(p.got)
	second, err := w.RepostFailedCreditMemoJournals(ctx, 500)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	for _, j := range p.got[before:] {
		if j.CreditMemoID == memo {
			t.Fatalf("the second sweep posted memo %s AGAIN, one memo, two journals, and the AR side double-counted", memo)
		}
	}
	_ = second
}

// No poster wired: nothing is attempted and the run is a SKIP, against the real query rather than a
// nil pool.
func TestIntegration_RepostCreditMemos_WithNoPosterIsASkip(t *testing.T) {
	pool := intPool(t)
	cust := seedCustomer(t, pool, "repost3")
	seedCreditMemo(t, pool, cust, "#CM-REPOST-NOPOSTER", 10_00, 1_30, fixtureEpoch.AddDate(0, 0, 2))

	res, err := NewInvoiceWriter(pool).RepostFailedCreditMemoJournals(context.Background(), 500)
	if err != nil {
		t.Fatalf("no-poster sweep: %v", err)
	}
	if !res.Skipped || res.Scanned != 0 || res.Reposted != 0 {
		t.Fatalf("no-poster sweep returned %+v, want a skip that attempted nothing", res)
	}
	if job := renderRepostJobResult(res); !job.Skipped {
		t.Fatal("a no-poster run recorded as SUCCEEDED would make job_runs claim the ledger was checked")
	}
}

// fixturePoster writes the gl_journals row a real post would leave, so the idempotency assertion has
// real state to stand on.
type fixturePoster struct {
	t    *testing.T
	pool *pgxpool.Pool
	got  []CreditMemoJournal
}

func (p *fixturePoster) PostCreditMemoJournal(_ context.Context, in CreditMemoJournal) (string, error) {
	p.got = append(p.got, in)
	markJournalPosted(p.t, p.pool, in.CreditMemoID)
	return in.IdempotencyKey, nil
}

// -- item 5c (session A), the fixture owns its accounts and its period ---------------------------
//
// WHAT IT USED TO DO. markJournalPosted needed somewhere to hang two balanced legs, and took the
// cheapest thing that would satisfy the foreign keys:
//
//	SELECT id FROM fiscal_periods ORDER BY year, month LIMIT 1                 -- the earliest period
//	SELECT id FROM gl_accounts   ORDER BY code LIMIT 1 / OFFSET 1 LIMIT 1      -- the two lowest codes
//
// Both are ORDERING, not identity. "Whatever sorts first" is a fixture that has not chosen anything:
// it is bound to whichever rows the seed happens to put at the front, and it moves the moment the
// chart of accounts or the calendar gains a row. Today those are 1000 Cash and 1010 Card Settlement
// Clearing, which the M2-C1 staging tie-out does not read; the seven codes it DOES read start at
// 1020, one row further down the same ORDER BY. Nothing but luck separates this fixture from
// writing a hundred cents into an account a staging reconciliation asserts to the cent.
//
// It was also incoherent with itself: the journal declares fiscal_year 1990 and posted_at 1990-06-01
// (fixtureEpoch), and was then parented to the earliest REAL period, 2026-01.
//
// WHAT IT DOES NOW. It names what it wants. A scratch period in the fixture's own year and a pair of
// scratch accounts in a code range no seeded account occupies, both created idempotently and torn
// down, so this file cannot reach a row any other test reasons about, and cannot be moved onto one
// by a future seed.
//
// The teardown is registered from the CREATE helpers so t.Cleanup's LIFO ordering drops the journal
// lines (registered later, by seedCreditMemo) before the accounts they reference: gl_journal_lines →
// gl_accounts is ON DELETE RESTRICT, and a fixture that cannot clean itself up is the defect this
// item is about.

// repostFixtureYear is the year this file's rows live in, the same 1990 fixtureEpoch already names.
const repostFixtureYear = 1990

// repostScratchAccountCodes are deliberately outside the seeded chart (which runs 1000 → 9999 with
// 9999 Suspense as its highest). They are asserted absent-from-the-seed below, so "outside" is a
// checked fact rather than a hopeful comment.
var repostScratchAccountCodes = [2]string{"CM-REPOST-DR", "CM-REPOST-CR"}

// repostScratchPeriod mints (idempotently) the twelve periods of the fixture's own year and returns
// the one the fixture's instant falls in. Idempotent because a killed previous run must not poison
// this one.
func repostScratchPeriod(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO fiscal_periods (year, month)
		SELECT $1, m FROM generate_series(1, 12) AS m
		ON CONFLICT (year, month) DO NOTHING`, repostFixtureYear); err != nil {
		t.Fatalf("mint the fixture's own fiscal year: %v", err)
	}
	// THE TEARDOWN IS SELF-SUFFICIENT, not order-dependent. t.Cleanup is LIFO and this helper is
	// called from markJournalPosted, i.e. AFTER seedCreditMemo has already registered the delete for
	// the journal that references this period, so a bare `DELETE FROM fiscal_periods` would run while
	// its child journal still existed, fail the foreign key, and (being best-effort) leave twelve
	// periods behind for ever. It therefore drops its own year's journals first. Everything it touches
	// is keyed on a fiscal_periods.year / gl_journals.fiscal_year of repostFixtureYear, which no other
	// test in this tree writes. (Several fixtures DATE rows into 1990, issue_date, finalized_at,
	// published_at, and none of those columns is touched here; the distinction is deliberate.)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE fiscal_year = $1)`, repostFixtureYear)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE fiscal_year = $1`, repostFixtureYear)
		_, _ = pool.Exec(c, `DELETE FROM gl_period_overrides WHERE period_id IN (SELECT id FROM fiscal_periods WHERE year = $1)`, repostFixtureYear)
		_, _ = pool.Exec(c, `DELETE FROM fiscal_periods WHERE year = $1`, repostFixtureYear)
	})
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = $2`,
		repostFixtureYear, int(fixtureEpoch.Month())).Scan(&id); err != nil {
		t.Fatalf("read the fixture's own period: %v", err)
	}
	return id
}

// repostScratchAccounts mints (idempotently) the debit/credit pair this fixture posts against, and
// proves they are not part of the seeded chart before using them.
func repostScratchAccounts(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	ctx := context.Background()
	dr, cr := repostScratchAccountCodes[0], repostScratchAccountCodes[1]

	// Defensively drop first, exactly as the gl fixtures do: a run killed mid-test leaves rows behind,
	// and a fixture that inherits them is a fixture that cannot be reasoned about.
	clean := func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE account_id IN (SELECT id FROM gl_accounts WHERE code IN ($1,$2))`, dr, cr)
		_, _ = pool.Exec(c, `DELETE FROM gl_accounts WHERE code IN ($1,$2)`, dr, cr)
	}
	clean()
	t.Cleanup(clean)

	// "Outside the seeded chart" is CHECKED, not asserted in a comment. After the defensive clean the
	// codes must not exist, if a future seed ever mints one of these, this fixture would silently be
	// borrowing again, which is the exact thing this change removes.
	var collide int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM gl_accounts WHERE code IN ($1,$2)`, dr, cr).Scan(&collide); err != nil {
		t.Fatalf("probe the fixture's account codes: %v", err)
	}
	if collide != 0 {
		t.Fatalf("the chart of accounts now seeds %s/%s, this fixture must own codes nothing else does", dr, cr)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO gl_accounts (code, name, account_type, normal_balance)
		VALUES ($1, 'credit-memo repost fixture (debit)',  'EXPENSE',   'DEBIT'),
		       ($2, 'credit-memo repost fixture (credit)', 'LIABILITY', 'CREDIT')
		ON CONFLICT (code) DO NOTHING`, dr, cr); err != nil {
		t.Fatalf("mint the fixture's own accounts: %v", err)
	}

	var a, b string
	if err := pool.QueryRow(ctx,
		`SELECT (SELECT id::text FROM gl_accounts WHERE code = $1),
		        (SELECT id::text FROM gl_accounts WHERE code = $2)`, dr, cr).Scan(&a, &b); err != nil {
		t.Fatalf("read the fixture's own accounts: %v", err)
	}
	return a, b
}
