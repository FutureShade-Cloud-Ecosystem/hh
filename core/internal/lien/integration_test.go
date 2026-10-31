//go:build integration

// Integration tests run the real pgx Repository + a real gl.Service (so the holdback release journal actually
// posts to gl_journals and the holdback_ledger FK / the _HoldbackReleaseHasJournal CHECK are exercised)
// against a PRIVATE Postgres. Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hos_w6_lien?sslmode=disable' \
//	  go test -tags=integration ./internal/lien/... -count=1 -p 1
package lien

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/compliance"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

const trentonBranch = "5d1b3f6e-0000-4000-9000-000000000001"

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hos_w6_lien?sslmode=disable"
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

// realGLPoster wraps a real gl.Service (mirrors main.go's lienGLPoster).
type realGLPoster struct{ svc *gl.Service }

func (a realGLPoster) PostJournal(ctx context.Context, in GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

// intCoSigner resolves the co-signer ids the integration release tests use to an active controller, exercising
// the server-side four-eyes verification (B4) without depending on seeded identity rows.
type intCoSigner struct{}

func (intCoSigner) ResolveStaff(_ context.Context, userID string) (identity.StaffContext, error) {
	switch userID {
	case "ctrl-int", "ctrl":
		return identity.StaffContext{UserID: userID, RoleCode: roleController, Status: identity.StatusActive}, nil
	default:
		return identity.StaffContext{}, clientErr(404, "co-signer not found")
	}
}

// dbNow reads the DATABASE's clock.
//
// DIB-427 history. realSvc used to pin its clock to a fixed 2026-07-07 while every row these tests
// create was dated by Postgres's now(). That is the dangerous combination: on 2026-07-07 the two
// agreed, and on every day after that the rows the fixture creates are NEWER than the service's idea
// of "now", by 2026-08-11 already five weeks newer, so every age computed through
// buildAging(entries, facts, s.clock()) reads a negative interval that daysBetween clamps to 0. The
// aging assertion would not have failed; it would have gone quietly vacuous.
//
// DIB-427 could only fix that the WEAK way, deriving the service's clock from the database, because
// `InsertHoldbackEntry` left `created_at` to the column default and no test could supply one. The
// clock read and the INSERT are two round trips, so a fixture asking for "50 days ago" got 49-or-50
// depending on how many milliseconds passed between them. No test could assert a day-exact age or
// stand on a bucket boundary, and the assertions were widened until they fitted the slop.
//
// DIB-455 landed the seam (`InsertHoldbackEntry` honours an explicit `CreatedAt` via
// `COALESCE($9, now())`), so the date-sensitive tests below pin the ROWS, the stronger fix the P1
// park named. See agingAnchor for what that changes.
func dbNow(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var now time.Time
	if err := pool.QueryRow(context.Background(), `SELECT now()`).Scan(&now); err != nil {
		t.Fatalf("read the database clock: %v", err)
	}
	return now
}

// agingAnchor reads ONE instant and makes it both the service's "now" and the origin every fixture
// row's created_at is measured back from.
//
// It is not a calendar date, deliberately. A `time.Date(2026, 7, 7, …)` literal beside SQL `now()` is
// the exact combination the DIB-427 / W6D-7 tripwire exists to catch, and it would need an allowlist
// entry claiming the two never meet. Reading the anchor from the database costs nothing and needs no
// claim: there is no calendar in the file to expire.
//
// What DIB-455 changes is not where the anchor comes from, it is that the ROWS now carry an explicit
// created_at derived from it, instead of being stamped by a SECOND now() one round trip later. One
// instant, both sides, and the age is therefore exact to the day rather than exact-ish.
func agingAnchor(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	return dbNow(t, pool)
}

// daysAgo returns the created_at a row must carry to be exactly n days old at the anchor.
//
// A duration, not AddDate: `daysBetween` measures in hours (asOf.Sub(t).Hours()/24), and AddDate
// walks the CALENDAR, across a DST boundary in a non-UTC session that is 45 days and 23 hours, which
// truncates to 44 and puts a boundary test on the wrong side of the rule it is asserting.
func daysAgo(anchor time.Time, n int) time.Time {
	return anchor.Add(-time.Duration(n) * 24 * time.Hour)
}

func realSvc(t *testing.T, pool *pgxpool.Pool, caller identity.StaffContext) *Service {
	glSvc := gl.NewService(gl.NewRepository(pool))
	asOf := dbNow(t, pool)
	return NewService(NewRepository(pool)).
		WithGLPoster(realGLPoster{svc: glSvc}).
		WithCoSigner(intCoSigner{}).
		withCaller(func(context.Context) (identity.StaffContext, bool) { return caller, caller.UserID != "" }).
		withClock(func() time.Time { return asOf })
}

func uniqueJobNo(p string) string { return p + "-" + time.Now().Format("150405.000000") }

// I1: release posts a real DR 2350 / CR 1150 journal and the RELEASE ledger row carries its gl_journal_id
// (the 0038 _HoldbackReleaseHasJournal CHECK is satisfied structurally).
func TestIntegration_ReleaseHoldbackPostsJournal(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(t, pool, gm("gm-int"))

	job, err := svc.CreateJob(ctx, JobInput{JobNo: uniqueJobNo("REL"), BranchID: trentonBranch, Direction: dirReceivable})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := svc.store.InsertHoldbackEntry(ctx, HoldbackEntry{JobID: job.ID, BranchID: trentonBranch, EntryType: "RETENTION", AmountCents: 25000}); err != nil {
		t.Fatalf("seed retention: %v", err)
	}

	res, err := svc.ReleaseHoldbackApproved(ctx, job.ID, ReleaseApproval{ApproverID: "ctrl-int", ApproverRole: roleController})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if res.AmountCents != 25000 || res.GLJournalID == "" {
		t.Fatalf("release result %+v, want 25000 + journal id", res)
	}
	// The gl_journals row exists and the RELEASE ledger row references it.
	var jexists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gl_journals WHERE id = $1)`, res.GLJournalID).Scan(&jexists); err != nil || !jexists {
		t.Fatalf("gl_journal %s not found (err=%v)", res.GLJournalID, err)
	}
	var relJournal string
	if err := pool.QueryRow(ctx,
		`SELECT gl_journal_id::text FROM holdback_ledger WHERE job_id = $1 AND entry_type = 'RELEASE'`, job.ID).Scan(&relJournal); err != nil {
		t.Fatalf("read RELEASE row: %v", err)
	}
	if relJournal != res.GLJournalID {
		t.Fatalf("RELEASE row journal=%s, want %s", relJournal, res.GLJournalID)
	}

	// A second release finds nothing outstanding (netted) → 422.
	if _, err := svc.ReleaseHoldbackApproved(ctx, job.ID, ReleaseApproval{ApproverID: "ctrl-int", ApproverRole: roleController}); status(err) != 422 {
		t.Fatalf("second release status=%d, want 422", status(err))
	}
}

// I2: a PAYABLE job records retention but its release is refused (422), no journal posted.
func TestIntegration_PayableReleaseRefused(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(t, pool, gm("gm-int"))

	job, err := svc.CreateJob(ctx, JobInput{JobNo: uniqueJobNo("PAY"), BranchID: trentonBranch, Direction: dirPayable})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := svc.store.InsertHoldbackEntry(ctx, HoldbackEntry{JobID: job.ID, BranchID: trentonBranch, EntryType: "RETENTION", AmountCents: 5000}); err != nil {
		t.Fatalf("seed retention: %v", err)
	}
	if _, err := svc.ReleaseHoldbackApproved(ctx, job.ID, ReleaseApproval{ApproverID: "ctrl", ApproverRole: roleController}); status(err) != 422 {
		t.Fatalf("PAYABLE release status=%d, want 422 (err=%v)", status(err), err)
	}
	var relCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM holdback_ledger WHERE job_id = $1 AND entry_type = 'RELEASE'`, job.ID).Scan(&relCount)
	if relCount != 0 {
		t.Fatalf("PAYABLE refused but %d RELEASE rows written", relCount)
	}
}

// I3: holdback sync is idempotent through the real repo (HeldInvoiceIDs + InsertHoldbackEntry), and books
// only the invoices of the customer job the lien job is linked to.
func TestIntegration_SyncIdempotent(t *testing.T) {
	w := rt1Plant(t)
	ctx := context.Background()
	lienID := w.lienJob(t, w.customerA, w.branchA)
	t.Cleanup(func() { rt1Del(t, w.pool, "holdback_ledger", "job_id = $1", lienID) })
	if _, err := w.pool.Exec(ctx, `UPDATE lien_jobs SET customer_job_id = $2 WHERE id = $1`, lienID, w.jobOnSiteON); err != nil {
		t.Fatalf("link the lien job: %v", err)
	}
	svc := w.svc
	// A real seeded invoice id (holdback_ledger.invoice_id is FK'd to invoices), the fake reader supplies the
	// holdback amount and the customer job; only the id must be a valid FK.
	inv := "19000000-0000-4000-9000-000000000001"
	svc.WithHoldbackFacts(fakeIntHoldback{inv: inv, branch: w.branchA, job: w.jobOnSiteON})
	r1, err := svc.SyncHoldbacks(w.as(gm("gm-int")), lienID)
	if err != nil {
		t.Fatalf("sync1: %v", err)
	}
	r2, err := svc.SyncHoldbacks(w.as(gm("gm-int")), lienID)
	if err != nil {
		t.Fatalf("sync2: %v", err)
	}
	if r1.BookedCount != 1 || r2.BookedCount != 0 {
		t.Fatalf("sync booked r1=%d r2=%d, want 1/0 (idempotent)", r1.BookedCount, r2.BookedCount)
	}
	// A second lien job linked to ANOTHER customer job books nothing of that invoice.
	other := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.pool.Exec(ctx, `UPDATE lien_jobs SET customer_job_id = $2 WHERE id = $1`, other, w.jobOnSiteZA); err != nil {
		t.Fatalf("link the second lien job: %v", err)
	}
	if r3, err := svc.SyncHoldbacks(w.as(gm("gm-int")), other); err != nil || r3.BookedCount != 0 {
		t.Fatalf("second lien job booked %d of the first job's invoice (%v)", r3.BookedCount, err)
	}
}

type fakeIntHoldback struct{ inv, branch, job string }

func (f fakeIntHoldback) InvoicesWithHoldback(_ context.Context, _ string) ([]HoldbackInvoice, error) {
	return []HoldbackInvoice{{InvoiceID: f.inv, InvoiceNo: "INV-SYNC", BranchID: f.branch, CustomerJobID: f.job, HoldbackCents: 4000}}, nil
}
func (f fakeIntHoldback) InvoiceHoldbackFacts(_ context.Context, _ string) (HoldbackFacts, error) {
	return HoldbackFacts{}, nil
}

// I4: document render + version history round-trips through the DB (append-only versions).
func TestIntegration_DocumentVersionHistory(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(t, pool, gm("gm-int"))
	job, err := svc.CreateJob(ctx, JobInput{JobNo: uniqueJobNo("DOC"), BranchID: trentonBranch, Direction: dirReceivable})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	d1, err := svc.CreateDocument(ctx, DocumentInput{JobID: job.ID, TemplateKind: "STATUTORY_DECLARATION", Data: map[string]any{"declarant": "Grant"}})
	if err != nil {
		t.Fatalf("doc1: %v", err)
	}
	d2, err := svc.CreateDocument(ctx, DocumentInput{JobID: job.ID, TemplateKind: "STATUTORY_DECLARATION", Data: map[string]any{"declarant": "Grant"}})
	if err != nil {
		t.Fatalf("doc2: %v", err)
	}
	if d1.Version != 1 || d2.Version != 2 || !d1.IsDraftBanner {
		t.Fatalf("versions %d/%d draft=%v, want 1/2/true", d1.Version, d2.Version, d1.IsDraftBanner)
	}
	hist, err := svc.DocumentVersions(ctx, d2.ID)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(hist) != 2 || hist[0].Version != 2 {
		t.Fatalf("history len=%d newest=%d, want 2/2", len(hist), hist[0].Version)
	}
}

// -- DIB-36: Construction-Act forms render + persist with statutory fidelity --------------------------------

// I5 (DIB-36): the three statute-named forms (Conditional Release, Statutory Declaration, Discharge) each render
// from supplied merge DATA and persist to lien_documents with the fields the operator sees on the deliverable -
// the DRAFT banner, the frozen statutory Title, every supplied merge value, and a "____" blank for an omitted
// field. Ground truth = the templateSpec Titles/Lines frozen in template.go + the merge-data fixture this test
// supplies, reconciled against the rendered_content / is_draft_banner / version columns actually written to the
// 0038 lien_documents (the schema of record). NOT a tautology: the expected Titles and the omitted-field blank
// are independently derived from template.go, not read back from the same render.
func TestIntegration_ConstructionActFormsRenderAndPersist(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(t, pool, gm("gm-int"))

	job, err := svc.CreateJob(ctx, JobInput{JobNo: uniqueJobNo("FORM"), BranchID: trentonBranch, Direction: dirReceivable})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Each form supplies realistic merge data but DELIBERATELY omits exactly one line's key, which must render as
	// the "____" statutory blank (mergeValue in template.go). mustRender are supplied values that must appear.
	forms := []struct {
		kind       string
		title      string // frozen in template.go templateSpec.Title, asserted, not read back
		data       map[string]any
		blankLine  string   // "Label: ____" the omitted key must produce
		mustRender []string // supplied merge values that must appear verbatim
	}{
		{
			kind:  "CONDITIONAL_RELEASE",
			title: "Conditional Release of Preserved Lien (Payment)",
			// omit "owner" → "Owner: ____"
			data: map[string]any{
				"job_no":          job.JobNo,
				"claimant":        "Northshore Masonry Ltd.",
				"payment_amount":  "$88,000.00",
				"holdback_amount": "$9,777.00",
				"condition":       "on cheque clearing",
			},
			blankLine:  "Owner: ____",
			mustRender: []string{"$9,777.00", "on cheque clearing"},
		},
		{
			kind:  "STATUTORY_DECLARATION",
			title: "Statutory Declaration (Construction Act)",
			// omit "capacity" → "Capacity: ____"
			data: map[string]any{
				"job_no":        job.JobNo,
				"declarant":     "Priya Natarajan",
				"accounts_paid": "all labour and materials paid to date",
			},
			blankLine:  "Capacity: ____",
			mustRender: []string{"Priya Natarajan", "all labour and materials paid to date"},
		},
		{
			kind:  "DISCHARGE",
			title: "Discharge of Preserved Lien",
			// omit "claimant" → "Claimant: ____"
			data: map[string]any{
				"job_no":            job.JobNo,
				"lien_claim_no":     "T-CV-2094-0042",
				"discharged_amount": "$12,000.00",
			},
			blankLine:  "Claimant: ____",
			mustRender: []string{"T-CV-2094-0042", "$12,000.00"},
		},
	}

	for _, f := range forms {
		// (a) INSERT must succeed, proves the 0038 template_kind CHECK admits this kind.
		doc, err := svc.CreateDocument(ctx, DocumentInput{JobID: job.ID, TemplateKind: f.kind, Data: f.data})
		if err != nil {
			t.Fatalf("%s: CreateDocument: %v", f.kind, err)
		}
		if doc.Version != 1 {
			t.Fatalf("%s: first render version=%d, want 1", f.kind, doc.Version)
		}

		// Read back the persisted columns directly from lien_documents (the schema of record).
		var content string
		var draft bool
		var version int
		if err := pool.QueryRow(ctx,
			`SELECT rendered_content, is_draft_banner, version FROM lien_documents
			 WHERE job_id = $1 AND template_kind = $2`, job.ID, f.kind).Scan(&content, &draft, &version); err != nil {
			t.Fatalf("%s: read back persisted row: %v", f.kind, err)
		}
		// (b) DRAFT banner persisted.
		if !draft {
			t.Fatalf("%s: persisted is_draft_banner=false, want true", f.kind)
		}
		if !strings.Contains(content, "DRAFT") {
			t.Fatalf("%s: persisted rendered_content missing DRAFT banner:\n%s", f.kind, content)
		}
		// (f) first render persists version 1.
		if version != 1 {
			t.Fatalf("%s: persisted version=%d, want 1", f.kind, version)
		}
		// (c) statutory Title present (frozen in template.go).
		if !strings.Contains(content, f.title) {
			t.Fatalf("%s: rendered_content missing Title %q:\n%s", f.kind, f.title, content)
		}
		// (d) every supplied merge value renders.
		for _, want := range f.mustRender {
			if !strings.Contains(content, want) {
				t.Fatalf("%s: rendered_content missing merge value %q:\n%s", f.kind, want, content)
			}
		}
		// (e) the omitted field renders as the "____" statutory blank.
		if !strings.Contains(content, f.blankLine) {
			t.Fatalf("%s: omitted field did not render %q blank:\n%s", f.kind, f.blankLine, content)
		}
	}
}

// -- DIB-37: DB-backed holdback aging + Monday triage through the real pgx Repository ------------------------

// I6 (DIB-37): the aging roll-up + triage badge, computed on the REAL DB read path (HoldbackLedger → buildAging
// → buildTriage), classify a receivable job 50 days past its retention as 31-60 / release-eligible / RELEASE_DUE.
// Ground truth = the job's own holdback_ledger net (ΣRETENTION 20000 − ΣRELEASE 8000 = 12000, read back below)
// and the 45-day releaseRuleDays window (asOf ≈ 50d ≥ 45 → eligible). We filter to job.ID because the branch
// carries seeded + other-tests' ledger rows; we do NOT assert the total row count.
func TestIntegration_HoldbackAgingBucketsAndTriage(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	// DIB-455: the ROWS are pinned, not just the clock. One instant is read as the anchor; every
	// fixture row below carries an explicit created_at measured back from it, and the service reads
	// asOf = that same instant, so the retention is exactly 50 days old on every run, and the age
	// assertion below can be an equality rather than a range.
	anchor := agingAnchor(t, pool)
	svc := realSvc(t, pool, gm("gm-int")).withClock(func() time.Time { return anchor })

	job, err := svc.CreateJob(ctx, JobInput{JobNo: uniqueJobNo("AGE"), BranchID: trentonBranch, Direction: dirReceivable})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM holdback_ledger WHERE job_id = $1::uuid`, job.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM lien_jobs WHERE id = $1::uuid`, job.ID)
	})
	if _, err := svc.store.InsertHoldbackEntry(ctx, HoldbackEntry{JobID: job.ID, BranchID: trentonBranch, EntryType: "RETENTION", AmountCents: 20000, CreatedAt: daysAgo(anchor, 50)}); err != nil {
		t.Fatalf("seed retention: %v", err)
	}
	// A RELEASE row requires a real gl_journal_id (0038 holdback_release_has_journal CHECK), so post a balanced
	// DR 2350 / CR 1150 journal first and reference it, this is what makes the net 20000 − 8000 = 12000 real.
	poster := realGLPoster{svc: gl.NewService(gl.NewRepository(pool))}
	journalID, err := poster.PostJournal(ctx, GLJournal{
		Source:         "ADJUSTMENT",
		SourceRefID:    job.ID,
		IdempotencyKey: "age-partial-release:" + job.ID,
		Memo:           "partial holdback release, aging test",
		BranchID:       trentonBranch,
		Lines: []GLLine{
			{AccountCode: "2350", DebitCents: 8000, Memo: "partial release payable"},
			{AccountCode: "1150", CreditCents: 8000, Memo: "partial release receivable"},
		},
	})
	if err != nil {
		t.Fatalf("post partial-release journal: %v", err)
	}
	if _, err := svc.store.InsertHoldbackEntry(ctx, HoldbackEntry{JobID: job.ID, BranchID: trentonBranch, EntryType: "RELEASE", AmountCents: 8000, GLJournalID: journalID, CreatedAt: daysAgo(anchor, 10)}); err != nil {
		t.Fatalf("seed partial release: %v", err)
	}

	// Independently reconcile the net from the DB, then assert the roll-up matches it.
	var netCents int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(CASE entry_type WHEN 'RETENTION' THEN amount_cents WHEN 'RELEASE' THEN -amount_cents END),0)
		   FROM holdback_ledger WHERE job_id = $1`, job.ID).Scan(&netCents); err != nil {
		t.Fatalf("read back ledger net: %v", err)
	}
	if netCents != 12000 {
		t.Fatalf("ledger net = %d, want 12000 (20000 RETENTION − 8000 RELEASE)", netCents)
	}

	rows, err := svc.HoldbackAging(ctx, trentonBranch)
	if err != nil {
		t.Fatalf("aging: %v", err)
	}
	var got *AgingRow
	for i := range rows {
		if rows[i].JobID == job.ID {
			got = &rows[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("aging row for job %s not found among %d rows", job.ID, len(rows))
	}
	if got.OutstandingCents != netCents {
		t.Fatalf("aging OutstandingCents=%d, want %d (DB net)", got.OutstandingCents, netCents)
	}
	// DIB-455: an EQUALITY, not a range. The retention row carries created_at = agingAnchor − 50d, so
	// 50 is the only correct answer; under the DIB-427 dbNow fixture this could only ever be asserted as
	// "49 or 50", and a one-day drift in daysBetween would have passed.
	if got.OldestDays != 50 {
		t.Fatalf("aging OldestDays=%d, want exactly 50 (row pinned at anchor−50d)", got.OldestDays)
	}
	if got.Bucket != "31-60" {
		t.Fatalf("aging Bucket=%q (oldestDays=%d), want 31-60", got.Bucket, got.OldestDays)
	}
	if !got.ReleaseEligible {
		t.Fatalf("aging ReleaseEligible=false, want true (%dd ≥ 45)", got.OldestDays)
	}

	triage, err := svc.Triage(ctx, trentonBranch)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	var badge string
	for _, it := range triage {
		if it.JobID == job.ID {
			badge = it.Badge
			break
		}
	}
	if badge != "RELEASE_DUE" {
		t.Fatalf("triage badge=%q for job %s, want RELEASE_DUE", badge, job.ID)
	}
}

// I6b (DIB-455): the 45-day release window is a BOUNDARY, and a boundary can only be tested to the day
// if the rows sit on it to the day. A retention aged exactly releaseRuleDays is eligible (`days >= 45`);
// one day younger is not. Both jobs are written in the same run against the same anchor, so the pair
// also proves the boundary is at 45 and not at 44 or 46, the off-by-one that `>=` vs `>` would make,
// and which a fixture whose rows land "about 50 days ago" cannot see at all.
//
// This test is the reason the DIB-455 seam exists: under the DIB-427 dbNow fixture the row's age was
// whatever the millisecond gap between the clock read and the INSERT made it, so a row asked to be 45
// days old was 44.9999 days old and rounded DOWN, the eligible case would have failed, and the only
// way to make it pass was to stop testing the boundary.
func TestIntegration_HoldbackAgingReleaseWindowIsExactlyFortyFiveDays(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	anchor := agingAnchor(t, pool)
	svc := realSvc(t, pool, gm("gm-int")).withClock(func() time.Time { return anchor })

	// ageOf writes a receivable job with a single retention row pinned to exactly n days old and returns
	// the aging row the roll-up computes for it.
	ageOf := func(t *testing.T, prefix string, n int) AgingRow {
		t.Helper()
		job, err := svc.CreateJob(ctx, JobInput{JobNo: uniqueJobNo(prefix), BranchID: trentonBranch, Direction: dirReceivable})
		if err != nil {
			t.Fatalf("create job %s: %v", prefix, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM holdback_ledger WHERE job_id = $1::uuid`, job.ID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM lien_jobs WHERE id = $1::uuid`, job.ID)
		})
		if _, err := svc.store.InsertHoldbackEntry(ctx, HoldbackEntry{
			JobID: job.ID, BranchID: trentonBranch, EntryType: "RETENTION", AmountCents: 11000,
			CreatedAt: daysAgo(anchor, n),
		}); err != nil {
			t.Fatalf("seed %d-day retention: %v", n, err)
		}
		rows, err := svc.HoldbackAging(ctx, trentonBranch)
		if err != nil {
			t.Fatalf("aging: %v", err)
		}
		for _, r := range rows {
			if r.JobID == job.ID {
				return r
			}
		}
		t.Fatalf("aging row for the %d-day job %s not found among %d rows", n, job.ID, len(rows))
		return AgingRow{}
	}

	at45 := ageOf(t, "B45", releaseRuleDays)
	if at45.OldestDays != releaseRuleDays {
		t.Fatalf("row pinned at %dd reads OldestDays=%d, the created_at seam did not take", releaseRuleDays, at45.OldestDays)
	}
	if !at45.ReleaseEligible {
		t.Fatalf("a retention exactly %d days old is ReleaseEligible=false; the window is inclusive", releaseRuleDays)
	}
	if at45.Bucket != "31-60" {
		t.Fatalf("%dd bucket=%q, want 31-60", releaseRuleDays, at45.Bucket)
	}

	at44 := ageOf(t, "B44", releaseRuleDays-1)
	if at44.OldestDays != releaseRuleDays-1 {
		t.Fatalf("row pinned at %dd reads OldestDays=%d", releaseRuleDays-1, at44.OldestDays)
	}
	if at44.ReleaseEligible {
		t.Fatalf("a retention %d days old is ReleaseEligible=true, the window opened a day early", releaseRuleDays-1)
	}
}

// I6c (DIB-455): the seam itself, at the repository. An explicit CreatedAt lands verbatim in the column;
// a zero CreatedAt still gets the database's now(), which is what every existing caller passes and why
// this change is behaviour-preserving for them.
func TestIntegration_InsertHoldbackEntryHonoursAnExplicitCreatedAtAndDefaultsToNow(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := realSvc(t, pool, gm("gm-int"))

	job, err := svc.CreateJob(ctx, JobInput{JobNo: uniqueJobNo("CRA"), BranchID: trentonBranch, Direction: dirReceivable})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM holdback_ledger WHERE job_id = $1::uuid`, job.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM lien_jobs WHERE id = $1::uuid`, job.ID)
	})

	// Far enough back that it cannot be confused with a now() stamp, and derived from the database's
	// own clock so this file holds no calendar date at all.
	pinned := daysAgo(dbNow(t, pool), 500).Truncate(time.Second)
	pinnedID, err := repo.InsertHoldbackEntry(ctx, HoldbackEntry{
		JobID: job.ID, BranchID: trentonBranch, EntryType: "RETENTION", AmountCents: 1000, CreatedAt: pinned,
	})
	if err != nil {
		t.Fatalf("insert pinned: %v", err)
	}

	before := dbNow(t, pool)
	defaultedID, err := repo.InsertHoldbackEntry(ctx, HoldbackEntry{
		JobID: job.ID, BranchID: trentonBranch, EntryType: "RETENTION", AmountCents: 2000,
	})
	if err != nil {
		t.Fatalf("insert defaulted: %v", err)
	}
	after := dbNow(t, pool)

	readBack := func(id string) time.Time {
		t.Helper()
		var got time.Time
		if err := pool.QueryRow(ctx, `SELECT created_at FROM holdback_ledger WHERE id = $1::uuid`, id).Scan(&got); err != nil {
			t.Fatalf("read created_at for %s: %v", id, err)
		}
		return got
	}

	if got := readBack(pinnedID); !got.Equal(pinned) {
		t.Fatalf("explicit created_at round-tripped as %s, want %s", got.UTC().Format(time.RFC3339Nano), pinned.Format(time.RFC3339Nano))
	}
	// The defaulted row is stamped by the database, between the two clock reads that bracket the insert.
	// Asserted as an interval rather than an equality because now() is the thing under test here.
	if got := readBack(defaultedID); got.Before(before) || got.After(after) {
		t.Fatalf("a zero CreatedAt stamped %s, outside the [%s, %s] bracket the insert ran in, the COALESCE default is not now()",
			got.UTC().Format(time.RFC3339Nano), before.UTC().Format(time.RFC3339Nano), after.UTC().Format(time.RFC3339Nano))
	}

	// And the read path returns what was written: the aging roll-up's oldest-retention rule reads
	// created_at through scanEntry, so a seam that only worked on the way in would still be broken.
	entries, err := repo.HoldbackEntriesForJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	var sawPinned bool
	for _, e := range entries {
		if e.ID == pinnedID {
			sawPinned = e.CreatedAt.Equal(pinned)
		}
	}
	if !sawPinned {
		t.Fatalf("the pinned row did not come back through HoldbackEntriesForJob carrying %s", pinned.Format(time.RFC3339Nano))
	}
}

// I7 (DIB-37): creating a receivable job with a known substantial-performance date registers the 45-day
// HOLDBACK_RELEASE deadline on the compliance_calendar via the real *compliance.Service registrar. Ground truth
// = releaseRuleDays=45 applied to the seeded SubstantialPerfDate (2026-07-01 + 45d = 2026-08-15). Independently
// derived: the expected due date is computed by hand here, not read from the same code path that wrote it.
func TestIntegration_ReleaseDeadlineRegisteredAt45Days(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(t, pool, gm("gm-int")).
		WithCalendar(compliance.NewService(compliance.NewRepository(pool)))

	job, err := svc.CreateJob(ctx, JobInput{
		JobNo:               uniqueJobNo("CAL"),
		BranchID:            trentonBranch,
		Direction:           dirReceivable,
		SubstantialPerfDate: "2026-07-01",
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	// DIB-396 order-independence: this job's whole point is that it writes a row into ANOTHER
	// module's table (compliance_calendar), and neither was ever cleaned up, every run of the suite
	// added a permanent HOLDBACK_RELEASE row to the shared compliance calendar. Cross-module residue
	// is the worst kind: it is invisible from the package that created it. The job carries no
	// holdback_ledger rows (that FK is RESTRICT), so deleting it cascades exactly its calendar row.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM lien_jobs WHERE id = $1::uuid`, job.ID)
	})

	var cnt int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM compliance_calendar WHERE job_id = $1::uuid AND deadline_kind = 'HOLDBACK_RELEASE'`,
		job.ID).Scan(&cnt); err != nil {
		t.Fatalf("count calendar rows: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("HOLDBACK_RELEASE rows for job %s = %d, want exactly 1", job.ID, cnt)
	}

	var due string
	var ruleDays int
	if err := pool.QueryRow(ctx,
		`SELECT to_char(due_date,'YYYY-MM-DD'), rule_days FROM compliance_calendar
		 WHERE job_id = $1::uuid AND deadline_kind = 'HOLDBACK_RELEASE'`, job.ID).Scan(&due, &ruleDays); err != nil {
		t.Fatalf("read calendar row: %v", err)
	}
	// 2026-07-01 + 45 days = 2026-08-15 (derived independently of the production offset code).
	if due != "2026-08-15" {
		t.Fatalf("HOLDBACK_RELEASE due_date=%q, want 2026-08-15 (perf 2026-07-01 + 45d)", due)
	}
	if ruleDays != 45 {
		t.Fatalf("HOLDBACK_RELEASE rule_days=%d, want 45", ruleDays)
	}
}

// barrierHoldback releases its callers together, so concurrent syncs of one lien job read the same
// unheld invoice before either has booked it.
type barrierHoldback struct {
	fakeIntHoldback
	wg *sync.WaitGroup
}

func (b barrierHoldback) InvoicesWithHoldback(ctx context.Context, branch string) ([]HoldbackInvoice, error) {
	b.wg.Done()
	b.wg.Wait()
	return b.fakeIntHoldback.InvoicesWithHoldback(ctx, branch)
}

// RT-2 fix round 2, P3: two concurrent syncs of one lien job book an invoice once.
func TestIntegration_TwoConcurrentSyncsBookAnInvoiceOnce(t *testing.T) {
	w := rt1Plant(t)
	ctx := context.Background()
	lienID := w.lienJob(t, w.customerA, w.branchA)
	t.Cleanup(func() { rt1Del(t, w.pool, "holdback_ledger", "job_id = $1", lienID) })
	if _, err := w.pool.Exec(ctx, `UPDATE lien_jobs SET customer_job_id = $2 WHERE id = $1`, lienID, w.jobOnSiteON); err != nil {
		t.Fatalf("link the lien job: %v", err)
	}
	const callers = 6
	wg := &sync.WaitGroup{}
	wg.Add(callers)
	svc := w.svc
	svc.WithHoldbackFacts(barrierHoldback{
		fakeIntHoldback: fakeIntHoldback{inv: "19000000-0000-4000-9000-000000000001", branch: w.branchA, job: w.jobOnSiteON},
		wg:              wg,
	})
	booked := make([]int, callers)
	var run sync.WaitGroup
	for i := 0; i < callers; i++ {
		run.Add(1)
		go func(i int) {
			defer run.Done()
			r, err := svc.SyncHoldbacks(w.as(gm("gm-int")), lienID)
			if err != nil {
				t.Errorf("sync %d: %v", i, err)
				return
			}
			booked[i] = r.BookedCount
		}(i)
	}
	run.Wait()
	total := 0
	for _, n := range booked {
		total += n
	}
	var rows int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM holdback_ledger WHERE job_id = $1 AND entry_type = 'RETENTION' AND amount_cents > 0`, lienID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || total != 1 {
		t.Errorf("concurrent syncs: %d ledger rows and %d reported booked, want 1 and 1", rows, total)
	}
}
