//go:build integration

package main

// rt3_holdback_release_integration_test.go, wave 3 RT-3: the holdback falls due when the lien
// preservation period ends with no lien preserved, and a Controller releases it, one RELEASE row per
// invoice, the GL moving 1150 to 1100, the customer owing it on the same invoice.
//
// Every leg runs on the RT-2 fixture (production wiring, a fresh migrated and seeded database of its own,
// dropped when the leg ends), so a leg leaves nothing behind in a shared database.
//
//	Run: DATABASE_URL=… go test -tags integration ./cmd/core -run RT3 -count=1 -v

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

var rt3Zone = func() *time.Location {
	z, err := time.LoadLocation("America/Toronto")
	if err != nil {
		panic(err)
	}
	return z
}()

// rt3Today is the house day now, as a midnight in the house zone.
func rt3Today() time.Time {
	n := time.Now().In(rt3Zone)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, rt3Zone)
}

// rt3Service is the release service as the wiring builds it, with the clock the leg sets.
func (l *rt2Leg) rt3Service(now time.Time) *billing.HoldbackReleaseService {
	return billing.NewHoldbackReleaseService(holdBillingRepo(l.pool)).WithClock(func() time.Time { return now })
}

// rt3PerfOn fixes the linked lien job's substantial performance date to the house day `daysAgo` days back.
func (l *rt2Leg) rt3PerfOn(daysAgo int) {
	l.t.Helper()
	day := rt3Today().AddDate(0, 0, -daysAgo).Format("2006-01-02")
	if _, err := l.pool.Exec(context.Background(), `UPDATE lien_jobs SET substantial_perf_date = $2::date WHERE id = $1::uuid`, l.lien, day); err != nil {
		l.t.Fatalf("set the substantial performance date: %v", err)
	}
}

func (l *rt2Leg) rt3Input(key string) billing.HoldbackReleaseInput {
	return billing.HoldbackReleaseInput{
		CustomerJobID: l.job, IdempotencyKey: key, ActorID: "d7000000-0000-4000-9000-000000000006",
		IsController: true,
	}
}

// rt3Refusal answers the refusal kind of an error ("" when it is not a release refusal).
func rt3Refusal(err error) string {
	var r billing.HoldbackReleaseRefused
	if errors.As(err, &r) {
		return r.Kind
	}
	return ""
}

func (l *rt2Leg) rt3Releases(invoiceID string) int {
	l.t.Helper()
	var n int
	if err := l.pool.QueryRow(context.Background(), `SELECT count(*) FROM holdback_ledger WHERE invoice_id = $1::uuid AND entry_type = 'RELEASE'`, invoiceID).Scan(&n); err != nil {
		l.t.Fatalf("count the release rows: %v", err)
	}
	return n
}

// rt3Journal reads the release journal's legs by the release row's own key.
func (l *rt2Leg) rt3Journal(invoiceID string) map[string][2]int64 {
	l.t.Helper()
	rows, err := l.pool.Query(context.Background(), `
		SELECT a.code::text, COALESCE(SUM(ln.debit_cents),0), COALESCE(SUM(ln.credit_cents),0)
		  FROM holdback_ledger h
		  JOIN gl_journals j ON j.idempotency_key = 'ar.holdback.released:' || h.id::text
		  JOIN gl_journal_lines ln ON ln.journal_id = j.id JOIN gl_accounts a ON a.id = ln.account_id
		 WHERE h.invoice_id = $1::uuid AND h.entry_type = 'RELEASE' AND j.status = 'POSTED'
		 GROUP BY a.code ORDER BY a.code`, invoiceID)
	if err != nil {
		l.t.Fatalf("read the release journal: %v", err)
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var code string
		var d, c int64
		if err := rows.Scan(&code, &d, &c); err != nil {
			l.t.Fatalf("scan: %v", err)
		}
		out[code] = [2]int64{d, c}
	}
	return out
}

// 1,000.00 held 100.00. Before the period ends nothing is due; on the last day to preserve, even at 23:59,
// nothing is due; at the house midnight that follows it is.
func TestRT3_NothingIsDueBeforeTheDayAfterThePreservationPeriodEnds(t *testing.T) {
	l := rt2Setup(t, "r3a")
	l.newJob(true)
	doc := l.invoice(100_000, 0)

	// 30 days into a 60 day period: not due.
	l.rt3PerfOn(30)
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("r3a-early")); rt3Refusal(err) != billing.RefusedNotDue {
		t.Fatalf("30 days in: want a not due refusal, got %v", err)
	}

	// The last day to preserve is the date plus 60: perf 61 days ago makes that day yesterday.
	l.rt3PerfOn(61)
	lastDayLate := rt3Today().Add(-time.Minute) // yesterday 23:59, the house clock
	_, err := l.rt3Service(lastDayLate).Release(context.Background(), l.rt3Input("r3a-2359"))
	if rt3Refusal(err) != billing.RefusedNotDue {
		t.Fatalf("the last day to preserve at 23:59: want a not due refusal, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), rt3Today().AddDate(0, 0, -1).Format("2006-01-02")) {
		t.Errorf("the refusal must name the last day to preserve, got %v", err)
	}
	eq(t, "nothing moved by the refusals", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "no release row", l.rt3Releases(doc.InvoiceID), 0)

	// The house midnight that follows: due. (The evening guard shifts the clock; the day is the house day.)
	midnight := rt3Today()
	res, err := l.rt3Service(midnight).Release(context.Background(), l.rt3Input("r3a-midnight"))
	if err != nil {
		t.Fatalf("at the house midnight after the last day: %v", err)
	}
	eq(t, "released", res.TotalCents, int64(10_000))
	l.drain()
	l.ties("after the release")
}

// A preserved lien blocks the release in words; a discharge lifts it.
func TestRT3_APreservedLienBlocksTheRelease(t *testing.T) {
	l := rt2Setup(t, "r3b")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(90)
	mustExec(t, context.Background(), l.pool, `UPDATE lien_jobs SET lien_preserved_on = $2::date WHERE id = $1::uuid`, l.lien, rt3Today().AddDate(0, 0, -40).Format("2006-01-02"))

	_, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("r3b-1"))
	if rt3Refusal(err) != billing.RefusedLienPreserved {
		t.Fatalf("a preserved lien: want a lien preserved refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "lien") {
		t.Errorf("the refusal must say it in words, got %q", err.Error())
	}
	eq(t, "nothing moved", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "no release row", l.rt3Releases(doc.InvoiceID), 0)

	mustExec(t, context.Background(), l.pool, `UPDATE lien_jobs SET lien_discharged_on = $2::date WHERE id = $1::uuid`, l.lien, rt3Today().Format("2006-01-02"))
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("r3b-2")); err != nil {
		t.Fatalf("a discharged lien no longer blocks: %v", err)
	}
	eq(t, "released", l.holdbackOf(doc.InvoiceID), int64(0))
}

// A release: the column and the ledger fall together, the GL moves 1150 to 1100, the same invoice's net
// grows, and a replay answers the first result and moves nothing.
func TestRT3_AReleaseMovesTheColumnTheLedgerAndTheBooksTogether(t *testing.T) {
	l := rt2Setup(t, "r3c")
	l.newJob(true)
	doc := l.invoice(100_000, 13_000)
	l.rt3PerfOn(70)
	eq(t, "net due before", l.agingTotal()-l.agingBase, int64(113_000-10_000))

	first, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("r3c-key"))
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	eq(t, "one invoice released", len(first.Items), 1)
	eq(t, "the amount", first.Items[0].AmountCents, int64(10_000))
	l.drain()

	eq(t, "the column", l.holdbackOf(doc.InvoiceID), int64(0))
	eq(t, "one release row", l.rt3Releases(doc.InvoiceID), 1)
	eq(t, "the ledger holds nothing for the job", l.ledgerNet(), int64(0))
	j := l.rt3Journal(doc.InvoiceID)
	eq(t, "1100 debit", j["1100"][0], int64(10_000))
	eq(t, "1150 credit", j["1150"][1], int64(10_000))
	eq(t, "the customer now owes the whole invoice", l.agingTotal()-l.agingBase, int64(113_000))
	var status string
	_ = l.pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE id = $1::uuid`, doc.InvoiceID).Scan(&status)
	if status != "FINALIZED" {
		t.Errorf("the same invoice stays open as FINALIZED, got %s", status)
	}
	l.ties("after the release")

	// The same key again: the first result, nothing new.
	again, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("r3c-key"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || again.TotalCents != 10_000 {
		t.Errorf("replay: want replayed 10000, got %+v", again)
	}
	eq(t, "still one release row", l.rt3Releases(doc.InvoiceID), 1)

	// A new key after the release: refused, it is released.
	_, err = l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("r3c-other"))
	if rt3Refusal(err) != billing.RefusedAlreadyReleased {
		t.Fatalf("a second release: want an already released refusal, got %v", err)
	}
	l.drain()
	l.ties("after the refused second release")
	eq(t, "still one release row", l.rt3Releases(doc.InvoiceID), 1)
}

// A partly credited invoice releases what it still holds, not what it first withheld.
func TestRT3_APartlyCreditedInvoiceReleasesWhatIsLeft(t *testing.T) {
	l := rt2Setup(t, "r3d")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.creditMemo(doc.InvoiceID, 50_000, "r3d-credit")
	l.drain()
	eq(t, "half the holdback left", l.holdbackOf(doc.InvoiceID), int64(5_000))
	l.rt3PerfOn(80)

	res, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("r3d"))
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	eq(t, "what is left", res.TotalCents, int64(5_000))
	l.drain()
	eq(t, "the column", l.holdbackOf(doc.InvoiceID), int64(0))
	eq(t, "the ledger", l.ledgerNet(), int64(0))
	l.ties("after the release of a credited invoice")
}

// A written off invoice holds nothing and releases nothing; its reversal puts the holdback back, and then
// it releases.
func TestRT3_AWrittenOffThenReversedInvoice(t *testing.T) {
	l := rt2Setup(t, "r3e")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(80)
	ctx := context.Background()
	wo := billing.NewWriteOffService(holdBillingRepo(l.pool))
	off, err := wo.WriteOff(ctx, billing.WriteOffInput{
		InvoiceID: doc.InvoiceID, Reason: "dead account", ActorID: "d7000000-0000-4000-9000-000000000006",
		IdempotencyKey: "r3e-wo", IsController: true, OrgID: "88a20000-0000-4000-8000-0000000000c2",
	}, true)
	if err != nil {
		t.Fatalf("write off: %v", err)
	}
	l.drain()
	eq(t, "the column after the write off", l.holdbackOf(doc.InvoiceID), int64(0))

	_, err = l.rt3Service(time.Now()).Release(ctx, l.rt3Input("r3e-1"))
	if rt3Refusal(err) != billing.RefusedNothingHeld {
		t.Fatalf("a written off invoice: want a nothing held refusal, got %v", err)
	}
	eq(t, "no release row", l.rt3Releases(doc.InvoiceID), 0)

	if _, err := wo.Reverse(ctx, billing.ReverseWriteOffInput{
		InvoiceID: doc.InvoiceID, WriteOffID: off.WriteOffID, Reason: "paid after all", ActorID: "d7000000-0000-4000-9000-000000000006", OrgID: "88a20000-0000-4000-8000-0000000000c2",
	}, true); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	l.drain()
	eq(t, "the column after the reversal", l.holdbackOf(doc.InvoiceID), int64(10_000))

	res, err := l.rt3Service(time.Now()).Release(ctx, l.rt3Input("r3e-2"))
	if err != nil {
		t.Fatalf("release after the reversal: %v", err)
	}
	eq(t, "released", res.TotalCents, int64(10_000))
	l.drain()
	eq(t, "the ledger", l.ledgerNet(), int64(0))
	l.ties("after the reversal and the release")
}

// Only the Controller releases. A refusal moves nothing.
func TestRT3_OnlyAControllerReleases(t *testing.T) {
	l := rt2Setup(t, "r3f")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(80)
	in := l.rt3Input("r3f")
	in.IsController = false
	_, err := l.rt3Service(time.Now()).Release(context.Background(), in)
	if rt3Refusal(err) != billing.RefusedNotController {
		t.Fatalf("a non controller: want a not controller refusal, got %v", err)
	}
	eq(t, "the column", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "no release row", l.rt3Releases(doc.InvoiceID), 0)
}

// Ids are canonicalised: an upper case job id releases, and the same key under the lower case spelling replays.
func TestRT3_IdsInUpperCaseAreOneId(t *testing.T) {
	l := rt2Setup(t, "r3g")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(80)
	up := l.rt3Input("R3G-KEY")
	up.CustomerJobID = strings.ToUpper(l.job)
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), up); err != nil {
		t.Fatalf("release with an upper case id: %v", err)
	}
	low := l.rt3Input("R3G-KEY")
	again, err := l.rt3Service(time.Now()).Release(context.Background(), low)
	if err != nil || !again.Replayed {
		t.Fatalf("the lower case spelling of the same request must replay: %+v, %v", again, err)
	}
	eq(t, "one release row", l.rt3Releases(doc.InvoiceID), 1)
}

// Two releases at once make one.
func TestRT3_TwoConcurrentReleasesMakeOne(t *testing.T) {
	l := rt2Setup(t, "r3h")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(80)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input(fmt.Sprintf("r3h-%d", i)))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case rt3Refusal(err) == billing.RefusedAlreadyReleased:
		default:
			t.Errorf("the loser must be refused as already released, got %v", err)
		}
	}
	eq(t, "one release succeeded", ok, 1)
	eq(t, "one release row", l.rt3Releases(doc.InvoiceID), 1)
	l.drain()
	eq(t, "one journal", len(l.rt3Journal(doc.InvoiceID)), 2)
	l.ties("after two concurrent releases")
}

// The read lists what is due, per invoice, from the dated ledger, and what is waiting with why.
func TestRT3_TheDueToReleaseReadListsAmountsByInvoice(t *testing.T) {
	l := rt2Setup(t, "r3i")
	l.newJob(true)
	a := l.invoice(100_000, 0)
	b := l.invoice(50_000, 0)
	l.rt3PerfOn(70)

	rows, err := holdBillingRepo(l.pool).HoldbackDueToRelease(context.Background(), billing.HoldbackDueQuery{BranchID: l.branch, Now: time.Now()})
	if err != nil {
		t.Fatalf("the read: %v", err)
	}
	got := map[string]billing.HoldbackDueRow{}
	for _, r := range rows {
		got[r.InvoiceID] = r
	}
	for id, want := range map[string]int64{a.InvoiceID: 10_000, b.InvoiceID: 5_000} {
		r, ok := got[id]
		if !ok || !r.Due || r.HoldbackCents != want || r.BranchID != l.branch {
			t.Errorf("invoice %s: want due %d at the branch, got %+v (listed %v)", id, want, r, ok)
		}
	}
	wantName := fmt.Sprintf("RT2 Customer %d", l.n)
	for _, r := range rows {
		if r.CustomerName != wantName {
			t.Errorf("invoice %s: want the customer's name %q on the row, got %q", r.InvoiceID, wantName, r.CustomerName)
		}
	}
	l.rt3PerfOn(10)
	rows, err = holdBillingRepo(l.pool).HoldbackDueToRelease(context.Background(), billing.HoldbackDueQuery{BranchID: l.branch, Now: time.Now()})
	if err != nil {
		t.Fatalf("the read: %v", err)
	}
	for _, r := range rows {
		if r.InvoiceID == a.InvoiceID && (r.Due || r.BlockedReason == "") {
			t.Errorf("10 days in: want waiting with a reason, got %+v", r)
		}
	}
}

// glBalance is an account's posted debits and credits, all branches.
func (l *rt2Leg) glBalance(code string) [2]int64 {
	l.t.Helper()
	var d, c int64
	if err := l.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(ln.debit_cents),0), COALESCE(SUM(ln.credit_cents),0)
		  FROM gl_journal_lines ln JOIN gl_journals j ON j.id = ln.journal_id JOIN gl_accounts a ON a.id = ln.account_id
		 WHERE a.code = $1 AND j.status = 'POSTED'`, code).Scan(&d, &c); err != nil {
		l.t.Fatalf("read account %s: %v", code, err)
	}
	return [2]int64{d, c}
}

// rt3LegacyRelease plants what the old job level route wrote on a box that already ran it: DR 2350 / CR 1150
// and a RELEASE row with no invoice, on the lien job linked to the leg's customer job.
func (l *rt2Leg) rt3LegacyRelease(amount int64) {
	l.t.Helper()
	ctx := context.Background()
	jid, err := l.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source: "ADJUSTMENT", SourceRefID: l.lien, IdempotencyKey: "holdback-release:" + l.lien, Memo: "legacy job level release",
		BranchID: l.branch,
		Lines: []gl.ExternalLine{
			{AccountCode: "2350", DebitCents: amount, Memo: "release holdback payable"},
			{AccountCode: "1150", CreditCents: amount, Memo: "release holdback receivable"},
		},
	})
	if err != nil {
		l.t.Fatalf("plant the legacy journal: %v", err)
	}
	mustExec(l.t, ctx, l.pool, `
		INSERT INTO holdback_ledger (job_id, branch_id, entry_type, amount_cents, gl_journal_id, released_at, reason)
		VALUES ($1::uuid, $2::uuid, 'RELEASE', $3, $4::uuid, CURRENT_DATE, 'legacy job level release')`, l.lien, l.branch, amount, jid)
}

// RT-3 fix, item 1: a job whose holdback the old job level route already released is refused in words,
// and 1150, 2350 and the ledger stay as they were.
func TestRT3_AJobTheOldRouteAlreadyReleasedIsRefused(t *testing.T) {
	l := rt2Setup(t, "r3x")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)
	l.rt3LegacyRelease(10_000)

	before1150, before2350, rows := l.glBalance("1150"), l.glBalance("2350"), l.ledgerRows()
	_, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("r3x-key"))
	if rt3Refusal(err) != billing.RefusedAlreadyReleased {
		t.Fatalf("a job with a job level release row: want an already released refusal, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "job level") {
		t.Errorf("the refusal must say the old job level release was used, got %v", err)
	}
	l.drain()
	eq(t, "1150 unchanged", l.glBalance("1150"), before1150)
	eq(t, "2350 unchanged", l.glBalance("2350"), before2350)
	eq(t, "ledger rows unchanged", l.ledgerRows(), rows)
	eq(t, "the column unchanged", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "no invoice level release row", l.rt3Releases(doc.InvoiceID), 0)
}
