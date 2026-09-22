package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
)

// credit_memo_repost_test.go, DIB-429, the unit half: the sweep's arithmetic and its failure
// behaviour, against a fake poster. The anti-join that FINDS the memos is SQL and is tested in
// credit_memo_repost_integration_test.go, where it can be wrong.

// repostPoster captures what the sweep asked the GL to post, and can refuse for named memos, which
// is the case invoice_writer_test.go's recordingPoster cannot express: it fails ALL or none, and the
// whole point of the batch loop is what happens when ONE of several refuses.
type repostPoster struct {
	got     []CreditMemoJournal
	failFor map[string]error // credit memo NO → the refusal
}

func (p *repostPoster) PostCreditMemoJournal(_ context.Context, in CreditMemoJournal) (string, error) {
	p.got = append(p.got, in)
	if err, ok := p.failFor[in.CreditMemoNo]; ok {
		return "", err
	}
	return "jrnl-" + in.CreditMemoID, nil
}

// A memo whose journal refuses AGAIN does not stop the batch, and it is counted as failed rather
// than swallowed.
func TestRepostCreditMemos_OneRefusalDoesNotStopTheBatch(t *testing.T) {
	pending := []failedCreditMemo{
		{id: "m-1", no: "#CM-1", branchID: "b-1", arCents: 113_00, revenueCents: 100_00, taxCents: 13_00},
		{id: "m-2", no: "#CM-2", branchID: "b-1", arCents: 226_00, revenueCents: 200_00, taxCents: 26_00},
		{id: "m-3", no: "#CM-3", branchID: "b-1", arCents: 339_00, revenueCents: 300_00, taxCents: 39_00},
	}
	p := &repostPoster{failFor: map[string]error{"#CM-2": errors.New("account 4000 not found")}}
	res := repostPending(t, pending, p)

	if res.Scanned != 3 || res.Reposted != 2 || res.Failed != 1 {
		t.Fatalf("result scanned=%d reposted=%d failed=%d, want 3/2/1", res.Scanned, res.Reposted, res.Failed)
	}
	if len(p.got) != 3 {
		t.Fatalf("the poster saw %d memos, want all 3, one refusal must not abandon the rest", len(p.got))
	}
	if res.RepostedCents != 113_00+339_00 {
		t.Fatalf("RepostedCents=%d, want only the two that succeeded", res.RepostedCents)
	}
	// The diagnosis is carried, once. Fifty copies of one message is not fifty facts.
	if res.FirstError == "" {
		t.Fatal("a refusal left no diagnosis; the operator has nothing to act on")
	}
	if !contains(res.FirstError, "#CM-2") || !contains(res.FirstError, "account 4000 not found") {
		t.Fatalf("FirstError=%q, want the memo number and the refusal", res.FirstError)
	}
}

// The key the sweep posts under is the SAME derived key the inline path uses. A different one would
// let the sweep write a SECOND journal for a memo whose inline post succeeded moments after the scan.
func TestRepostCreditMemos_PostsUnderTheSameDerivedKeyAsTheInlinePath(t *testing.T) {
	p := &repostPoster{}
	repostPending(t, []failedCreditMemo{{id: "m-9", no: "#CM-9", arCents: 100, revenueCents: 100}}, p)
	if got, want := p.got[0].IdempotencyKey, creditMemoJournalKey("m-9"); got != want {
		t.Fatalf("re-post key = %q, want %q, the sweep and IssueCreditMemo must derive one key", got, want)
	}
	if want := "ar.credit_memo.issued:m-9"; p.got[0].IdempotencyKey != want {
		t.Fatalf("the derived key changed shape to %q; every memo already in the ledger is keyed %q",
			p.got[0].IdempotencyKey, want)
	}
}

// The journal is dated the MEMO's date, not the night the sweep ran. A journal stamped tonight lands
// in the wrong period and re-opens a month that was already closed.
func TestRepostCreditMemos_UsesTheMemosOwnDateNotTonights(t *testing.T) {
	memoDate := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	p := &repostPoster{}
	w := NewInvoiceWriter(nil).WithCreditMemoPoster(p).
		WithClock(func() time.Time { return time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC) })
	w.repostAll(context.Background(), []failedCreditMemo{
		{id: "m-1", no: "#CM-1", arCents: 100, revenueCents: 100, occurredAt: memoDate},
	})
	if !p.got[0].OccurredAt.Equal(memoDate) {
		t.Fatalf("re-post dated %s, want the memo's own %s, a re-post must not move the entry into another period",
			p.got[0].OccurredAt.Format(time.RFC3339), memoDate.Format(time.RFC3339))
	}
}

// A zero-tax memo still posts; the tax leg's absence is gl's business (a zero leg violates the
// gl_journal_lines CHECK), not the sweep's.
func TestRepostCreditMemos_AZeroTaxMemoStillPosts(t *testing.T) {
	p := &repostPoster{}
	res := repostPending(t, []failedCreditMemo{
		{id: "m-1", no: "#CM-1", arCents: 100_00, revenueCents: 100_00, taxCents: 0},
	}, p)
	if res.Reposted != 1 {
		t.Fatalf("a zero-tax memo was not re-posted (%+v)", res)
	}
	if p.got[0].TaxCents != 0 || p.got[0].ARCents != p.got[0].RevenueCents {
		t.Fatalf("zero-tax memo posted as %+v", p.got[0])
	}
}

// A nil pool is an error, not a silent no-op: a sweep that cannot reach the database has not checked
// the ledger, and reporting that as a clean run is the failure mode this whole job exists to remove.
func TestRepostCreditMemos_ANilPoolIsAnErrorNotACleanRun(t *testing.T) {
	res, err := NewInvoiceWriter(nil).RepostFailedCreditMemoJournals(context.Background(), 0)
	if err == nil {
		t.Fatalf("a sweep with no database returned %+v and no error", res)
	}
}

// An UNBOUND poster is a SKIP, not a success. A deployment with no poster is one where credit memos
// never post journals at all: there is nothing to repair and no way to repair it, and recording that
// as SUCCEEDED would make job_runs claim the ledger was checked.
func TestCreditMemoRepostJob_NoPosterIsSkippedNotSucceeded(t *testing.T) {
	got := jobResultFor(t, CreditMemoRepostResult{Skipped: true})
	if !got.Skipped {
		t.Fatal("a no-poster sweep recorded as SUCCEEDED")
	}
	if got.Detail["no_poster"] != true {
		t.Fatalf("the run detail does not say WHY it skipped: %+v", got.Detail)
	}
}

// The JobFunc's SKIPPED rule: a night with nothing to repair is SKIPPED, so a run history of
// SUCCEEDED rows is a history of nights the ledger had a hole in it.
func TestCreditMemoRepostJob_NothingToRepairIsSkippedNotSucceeded(t *testing.T) {
	if got := jobResultFor(t, CreditMemoRepostResult{Scanned: 0}); !got.Skipped {
		t.Fatal("a sweep that found nothing recorded as SUCCEEDED; the healthy state must read as SKIPPED")
	}
	got := jobResultFor(t, CreditMemoRepostResult{Scanned: 2, Reposted: 2, RepostedCents: 500})
	if got.Skipped {
		t.Fatal("a sweep that WROTE journals recorded as SKIPPED")
	}
	if got.RowsRead != 2 || got.RowsWritten != 2 {
		t.Fatalf("job result rows read=%d written=%d, want 2/2", got.RowsRead, got.RowsWritten)
	}
	if got.Detail["reposted_cents"] != int64(500) {
		t.Fatalf("the run detail does not state the size of the hole closed: %+v", got.Detail)
	}
	// A partial repair states its diagnosis in the run detail, where an operator reading job_runs
	// finds it, the whole point of DIB-429 over the status quo, which was a field on an HTTP
	// response nobody kept.
	partial := jobResultFor(t, CreditMemoRepostResult{Scanned: 3, Reposted: 2, Failed: 1, FirstError: "#CM-2: account 4000 not found"})
	if partial.Detail["first_error"] != "#CM-2: account 4000 not found" {
		t.Fatalf("a partial sweep's diagnosis is missing from the run detail: %+v", partial.Detail)
	}
	if partial.Detail["failed"] != 1 {
		t.Fatalf("failed count missing from the run detail: %+v", partial.Detail)
	}
}

// -- helpers --------------------------------------------------------------------------------------

// repostPending drives the sweep's per-memo loop over a fixed pending set, so the arithmetic can be
// asserted without a database. The SQL that produces the set is tested in the integration file.
func repostPending(t *testing.T, pending []failedCreditMemo, p *repostPoster) CreditMemoRepostResult {
	t.Helper()
	w := NewInvoiceWriter(nil).WithCreditMemoPoster(p)
	return w.repostAll(context.Background(), pending)
}

// jobResultFor renders a sweep result through the JobFunc's mapping, which is the part an operator
// reads in job_runs.
func jobResultFor(t *testing.T, res CreditMemoRepostResult) jobs.Result {
	t.Helper()
	return renderRepostJobResult(res)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
