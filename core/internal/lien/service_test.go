package lien

import (
	"context"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - fakes -

type ctxKeyT struct{}

func ctxWith(sc identity.StaffContext) context.Context {
	return context.WithValue(context.Background(), ctxKeyT{}, sc)
}
func testCallerFrom(ctx context.Context) (identity.StaffContext, bool) {
	sc, ok := ctx.Value(ctxKeyT{}).(identity.StaffContext)
	return sc, ok
}

func gm(uid string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: roleGatekeeper, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}
func controller(uid string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: roleController, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

// fakeStore is an in-memory lienStore.
type fakeStore struct {
	jobs    map[string]Job
	entries []HoldbackEntry
	docs    []Document
	seq     int
}

func newFakeStore() *fakeStore { return &fakeStore{jobs: map[string]Job{}} }

func (f *fakeStore) id(p string) string { f.seq++; return p + "-" + itoa(f.seq) }

func (f *fakeStore) InsertJob(_ context.Context, in JobInput, _ string) (Job, error) {
	id := f.id("job")
	j := s2Job(id, in, testClock()) // the fake store's clock IS the service's clock, DIB-427
	f.jobs[id] = j
	return j, nil
}
func (f *fakeStore) GetJob(_ context.Context, id string) (Job, error) {
	j, ok := f.jobs[id]
	if !ok {
		return Job{}, errJobNotFound
	}
	return j, nil
}
func (f *fakeStore) ListJobs(_ context.Context, branchID string) ([]Job, error) {
	var out []Job
	for _, j := range f.jobs {
		if branchID == "" || j.BranchID == branchID {
			out = append(out, j)
		}
	}
	return out, nil
}
func (f *fakeStore) RetentionInvoiceIDs(_ context.Context, jobID string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, e := range f.entries {
		if e.JobID == jobID && e.EntryType == "RETENTION" && e.InvoiceID != "" {
			out[e.InvoiceID] = true
		}
	}
	return out, nil
}
func (f *fakeStore) HeldInvoiceIDs(_ context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	for _, e := range f.entries {
		if e.EntryType == "RETENTION" && e.AmountCents > 0 && e.InvoiceID != "" {
			out[e.InvoiceID] = true
		}
	}
	return out, nil
}
func (f *fakeStore) InsertHoldbackEntry(_ context.Context, e HoldbackEntry) (string, error) {
	e.ID = f.id("hb")
	if e.CreatedAt.IsZero() {
		e.CreatedAt = testClock() // stands in for the DB's now() default, pinned to the service's clock, DIB-427
	}
	f.entries = append(f.entries, e)
	return e.ID, nil
}
func (f *fakeStore) InsertRetentionOnce(ctx context.Context, e HoldbackEntry) (int64, error) {
	for _, x := range f.entries {
		if x.InvoiceID == e.InvoiceID && x.EntryType == "RETENTION" && x.AmountCents > 0 {
			return 0, nil
		}
	}
	_, err := f.InsertHoldbackEntry(ctx, e)
	return 1, err
}
func (f *fakeStore) HoldbackEntriesForJob(_ context.Context, jobID string) ([]HoldbackEntry, error) {
	var out []HoldbackEntry
	for _, e := range f.entries {
		if e.JobID == jobID {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeStore) HoldbackLedger(_ context.Context, branchID string) ([]HoldbackEntry, error) {
	var out []HoldbackEntry
	for _, e := range f.entries {
		if branchID == "" || e.BranchID == branchID {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeStore) NextDocumentVersion(_ context.Context, jobID, kind string) (int, error) {
	max := 0
	for _, d := range f.docs {
		if d.JobID == jobID && d.TemplateKind == kind && d.Version > max {
			max = d.Version
		}
	}
	return max + 1, nil
}
func (f *fakeStore) InsertDocument(_ context.Context, d Document, _ string) (Document, error) {
	d.ID = f.id("doc")
	d.CreatedAt = testClock().Format(time.RFC3339) // DIB-427: one clock for fixture and service
	f.docs = append(f.docs, d)
	return d, nil
}
func (f *fakeStore) DocumentsForJob(_ context.Context, jobID string) ([]Document, error) {
	latest := map[string]Document{}
	for _, d := range f.docs {
		if d.JobID != jobID {
			continue
		}
		if cur, ok := latest[d.TemplateKind]; !ok || d.Version > cur.Version {
			latest[d.TemplateKind] = d
		}
	}
	var out []Document
	for _, d := range latest {
		out = append(out, d)
	}
	return out, nil
}
func (f *fakeStore) DocumentVersions(_ context.Context, documentID string) ([]Document, error) {
	var target Document
	for _, d := range f.docs {
		if d.ID == documentID {
			target = d
		}
	}
	if target.ID == "" {
		return nil, errDocNotFound
	}
	var out []Document
	for _, d := range f.docs {
		if d.JobID == target.JobID && d.TemplateKind == target.TemplateKind {
			out = append(out, d)
		}
	}
	return out, nil
}

// fakeGL records the posted release journal.
type fakeGL struct {
	posted  []GLJournal
	nextID  string
	callCnt int
}

func (g *fakeGL) PostJournal(_ context.Context, in GLJournal) (string, error) {
	g.callCnt++
	g.posted = append(g.posted, in)
	if g.nextID == "" {
		g.nextID = "jrnl-1"
	}
	return g.nextID, nil
}

// fakeHoldback is a fake billing HoldbackFactsReader.
type fakeHoldback struct{ invoices []HoldbackInvoice }

func (f fakeHoldback) InvoicesWithHoldback(_ context.Context, branchID string) ([]HoldbackInvoice, error) {
	var out []HoldbackInvoice
	for _, i := range f.invoices {
		if branchID == "" || i.BranchID == branchID {
			out = append(out, i)
		}
	}
	return out, nil
}
func (f fakeHoldback) InvoiceHoldbackFacts(_ context.Context, invoiceID string) (HoldbackFacts, error) {
	for _, i := range f.invoices {
		if i.InvoiceID == invoiceID {
			return HoldbackFacts{InvoiceID: i.InvoiceID, HoldbackCents: i.HoldbackCents, BranchID: i.BranchID, CustomerID: i.CustomerID}, nil
		}
	}
	return HoldbackFacts{}, errJobNotFound
}

// fakeCoSigner resolves co-signer authority server-side (the B4 gate). The body-asserted role is irrelevant -
// only what this table returns matters. Unknown ids resolve to an error (co-signer does not exist).
type fakeCoSigner map[string]identity.StaffContext

func (f fakeCoSigner) ResolveStaff(_ context.Context, userID string) (identity.StaffContext, error) {
	sc, ok := f[userID]
	if !ok {
		return identity.StaffContext{}, clientErr(404, "co-signer not found")
	}
	return sc, nil
}

// defaultCoSigner seeds the ids the release matrix exercises. Roles are what the SERVER holds, not what a body
// might claim: ctrl/c are the controller, pr is payables-receivables, x is a non-finance role, susp is a
// controller who has been deactivated.
func defaultCoSigner() fakeCoSigner {
	staff := func(role, status string) identity.StaffContext {
		return identity.StaffContext{UserID: "", RoleCode: role, Status: status}
	}
	return fakeCoSigner{
		"ctrl": staff(roleController, identity.StatusActive),
		"c":    staff(roleController, identity.StatusActive),
		"pr":   staff(rolePayRec, identity.StatusActive),
		"x":    staff("INSIDE_SALES", identity.StatusActive),
		"susp": staff(roleController, "INACTIVE"),
	}
}

// testClock is THE clock for this file: the instant every service built by newSvc runs at, and the
// instant every fixture date must be derived from.
//
// DIB-427. seedJobWithRetention used to date its RETENTION row at time.Now().AddDate(0, 0, -60), the
// WALL clock, while the service reading it ran at this fixed instant. The two meet in
// buildAging(entries, facts, s.clock()), so the "60-day-old retention" the fixture claims to create
// was in fact 25 days old on 2026-08-11, would be 0 days old on 2026-09-05, and NEGATIVE after that
// (daysBetween clamps at 0, so it degrades silently rather than failing). Both sides now derive from
// testClock, so a 60-day-old retention is 60 days old on every calendar date the suite runs.
// (House pattern: internal/messagingprovider/collections_integration_test.go.)
func testClock() time.Time { return time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC) }

func newSvc(st lienStore) *Service {
	return NewService(nil).withStore(st).withCaller(testCallerFrom).WithCoSigner(defaultCoSigner()).
		withClock(testClock)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// - AC: sync idempotent -

func TestSyncHoldbacks_Idempotent(t *testing.T) {
	st := newFakeStore()
	job := linkedJob(st, JobInput{JobNo: "J1", BranchID: "br1", CustomerID: "cust1", Direction: dirReceivable}, "cj1")
	hb := fakeHoldback{invoices: []HoldbackInvoice{
		{InvoiceID: "inv1", InvoiceNo: "INV-1", CustomerID: "cust1", CustomerJobID: "cj1", BranchID: "br1", HoldbackCents: 5000},
		{InvoiceID: "inv2", InvoiceNo: "INV-2", CustomerID: "cust1", CustomerJobID: "cj1", BranchID: "br1", HoldbackCents: 3000},
	}}
	svc := newSvc(st).WithHoldbackFacts(hb)
	ctx := ctxWith(gm("u"))

	r1, err := svc.SyncHoldbacks(ctx, job.ID)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if r1.BookedCount != 2 || r1.BookedCents != 8000 {
		t.Fatalf("first sync booked=%d cents=%d, want 2/8000", r1.BookedCount, r1.BookedCents)
	}
	r2, err := svc.SyncHoldbacks(ctx, job.ID)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if r2.BookedCount != 0 || r2.SkippedCount != 2 {
		t.Fatalf("second sync booked=%d skipped=%d, want 0/2 (idempotent)", r2.BookedCount, r2.SkippedCount)
	}
	// A customer-scoped job ignores another customer's holdback invoice.
	hb.invoices = append(hb.invoices, HoldbackInvoice{InvoiceID: "inv3", InvoiceNo: "INV-3", CustomerID: "other", CustomerJobID: "cj-other", BranchID: "br1", HoldbackCents: 9000})
	svc2 := newSvc(st).WithHoldbackFacts(hb)
	r3, _ := svc2.SyncHoldbacks(ctx, job.ID)
	if r3.BookedCount != 0 {
		t.Fatalf("cross-customer invoice booked=%d, want 0", r3.BookedCount)
	}
}

// linkedJob inserts a lien job linked to a customer job (RT-1's link), as the real store holds it.
func linkedJob(st *fakeStore, in JobInput, customerJobID string) Job {
	j, _ := st.InsertJob(context.Background(), in, "u")
	j.CustomerJobID = customerJobID
	st.jobs[j.ID] = j
	return j
}

// P1-4: a second lien job for the same customer never books the first job's invoice, and a lien job
// linked to no customer job books nothing.
func TestSyncHoldbacks_BooksOnlyTheInvoicesOfItsOwnLinkedJob(t *testing.T) {
	st := newFakeStore()
	a := linkedJob(st, JobInput{JobNo: "A", BranchID: "br1", CustomerID: "cust1", Direction: dirReceivable}, "cjA")
	b := linkedJob(st, JobInput{JobNo: "B", BranchID: "br1", CustomerID: "cust1", Direction: dirReceivable}, "cjB")
	unlinked, _ := st.InsertJob(context.Background(), JobInput{JobNo: "U", BranchID: "br1", CustomerID: "cust1", Direction: dirReceivable}, "u")
	hb := fakeHoldback{invoices: []HoldbackInvoice{
		{InvoiceID: "invA", InvoiceNo: "INV-A", CustomerID: "cust1", CustomerJobID: "cjA", BranchID: "br1", HoldbackCents: 10000},
	}}
	svc := newSvc(st).WithHoldbackFacts(hb)
	ctx := ctxWith(gm("u"))
	if r, err := svc.SyncHoldbacks(ctx, a.ID); err != nil || r.BookedCount != 1 || r.BookedCents != 10000 {
		t.Fatalf("job A: %+v %v, want its own invoice booked once", r, err)
	}
	if r, err := svc.SyncHoldbacks(ctx, b.ID); err != nil || r.BookedCount != 0 {
		t.Fatalf("job B booked %d of job A's invoice (%v)", r.BookedCount, err)
	}
	if r, err := svc.SyncHoldbacks(ctx, unlinked.ID); err != nil || r.BookedCount != 0 {
		t.Fatalf("an unlinked lien job booked %d (%v)", r.BookedCount, err)
	}
	// An invoice some other job's ledger already holds is skipped even on its own job.
	hb.invoices = append(hb.invoices, HoldbackInvoice{InvoiceID: "invB", InvoiceNo: "INV-B", CustomerID: "cust1", CustomerJobID: "cjB", BranchID: "br1", HoldbackCents: 4000})
	_, _ = st.InsertHoldbackEntry(context.Background(), HoldbackEntry{JobID: a.ID, InvoiceID: "invB", BranchID: "br1", EntryType: "RETENTION", AmountCents: 4000})
	if r, _ := newSvc(st).WithHoldbackFacts(hb).SyncHoldbacks(ctx, b.ID); r.BookedCount != 0 {
		t.Fatalf("an invoice held by another job was booked again")
	}
}

// P3 (HST gross up): the ledger never books more than 1150 holds, whatever the job's HST flag says.
func TestSyncHoldbacks_NeverGrossesTheRetentionUp(t *testing.T) {
	st := newFakeStore()
	j := linkedJob(st, JobInput{JobNo: "H", BranchID: "br1", CustomerID: "cust1", Direction: dirReceivable, HSTOnHoldback: true}, "cj1")
	hb := fakeHoldback{invoices: []HoldbackInvoice{{InvoiceID: "inv1", InvoiceNo: "INV-1", CustomerID: "cust1", CustomerJobID: "cj1", BranchID: "br1", HoldbackCents: 10000}}}
	r, err := newSvc(st).WithHoldbackFacts(hb).SyncHoldbacks(ctxWith(gm("u")), j.ID)
	if err != nil || r.BookedCents != 10000 {
		t.Fatalf("booked %d (%v), want the 10000 that 1150 holds", r.BookedCents, err)
	}
}

// - AC: dual gate + release journal matrix + PAYABLE 422 -

func seedJobWithRetention(st *fakeStore, dir string) Job {
	job, _ := st.InsertJob(context.Background(), JobInput{JobNo: "J", BranchID: "br1", Direction: dir}, "u")
	// 60 days before the SERVICE's clock (not the wall clock), see testClock, DIB-427.
	_, _ = st.InsertHoldbackEntry(context.Background(), HoldbackEntry{JobID: job.ID, BranchID: "br1", EntryType: "RETENTION", AmountCents: 10000, CreatedAt: testClock().AddDate(0, 0, -60)})
	return job
}

func TestReleaseHoldback_DualGateMatrix(t *testing.T) {
	coApprove := ReleaseApproval{ApproverID: "ctrl", ApproverRole: roleController}

	cases := []struct {
		name       string
		caller     identity.StaffContext
		appr       ReleaseApproval
		wantStatus int // 0 == success
	}{
		{"happy: GM + controller co-sign", gm("gmuser"), coApprove, 0},
		{"gatekeeper not GM", controller("c"), coApprove, 403},
		{"missing co-signer", gm("gmuser"), ReleaseApproval{}, 403},
		{"co-signer wrong role", gm("gmuser"), ReleaseApproval{ApproverID: "x", ApproverRole: "INSIDE_SALES"}, 403},
		{"four-eyes: co-signer is the gatekeeper", gm("gmuser"), ReleaseApproval{ApproverID: "gmuser", ApproverRole: roleController}, 403},
		{"payables/receivables co-sign ok", gm("gmuser"), ReleaseApproval{ApproverID: "pr", ApproverRole: rolePayRec}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			job := seedJobWithRetention(st, dirReceivable)
			gl := &fakeGL{}
			svc := newSvc(st).WithGLPoster(gl)
			res, err := svc.ReleaseHoldbackApproved(ctxWith(tc.caller), job.ID, tc.appr)
			if tc.wantStatus == 0 {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				if res.AmountCents != 10000 || res.GLJournalID == "" {
					t.Fatalf("release result %+v, want amount 10000 + journal id", res)
				}
				// DR 2350 / CR 1150 exactly.
				j := gl.posted[len(gl.posted)-1]
				if len(j.Lines) != 2 || j.Lines[0].AccountCode != "2350" || j.Lines[0].DebitCents != 10000 ||
					j.Lines[1].AccountCode != "1150" || j.Lines[1].CreditCents != 10000 {
					t.Fatalf("journal lines %+v, want DR 2350 / CR 1150 of 10000", j.Lines)
				}
				// The RELEASE ledger row carries the journal id (the 0038 CHECK).
				ents, _ := st.HoldbackEntriesForJob(context.Background(), job.ID)
				var rel *HoldbackEntry
				for i := range ents {
					if ents[i].EntryType == "RELEASE" {
						rel = &ents[i]
					}
				}
				if rel == nil || rel.GLJournalID == "" {
					t.Fatalf("RELEASE row missing gl_journal_id: %+v", ents)
				}
				return
			}
			if status(err) != tc.wantStatus {
				t.Fatalf("status=%d, want %d (err=%v)", status(err), tc.wantStatus, err)
			}
			if gl.callCnt != 0 {
				t.Fatalf("a refused release must not post a journal (posts=%d)", gl.callCnt)
			}
		})
	}
}

// S3 W6 B4 regression: the co-signer's authority is resolved server-side, so a gatekeeper cannot self-satisfy
// the four-eyes gate by asserting a role in the request body. The body role is ignored entirely.
func TestReleaseHoldback_CoSignerVerifiedServerSide(t *testing.T) {
	cases := []struct {
		name       string
		appr       ReleaseApproval
		wantStatus int // 0 == success
	}{
		// Body claims CONTROLLER but the id resolves to a non-finance role → refused despite the claim.
		{"body-asserted role is ignored", ReleaseApproval{ApproverID: "x", ApproverRole: roleController}, 403},
		// Body claims a bogus role but the id truly is the controller → accepted on the resolved role.
		{"resolved role overrides bogus body claim", ReleaseApproval{ApproverID: "ctrl", ApproverRole: "INSIDE_SALES"}, 0},
		// A deactivated finance user cannot co-sign.
		{"inactive co-signer refused", ReleaseApproval{ApproverID: "susp", ApproverRole: roleController}, 403},
		// An id that does not resolve to any staff member is refused.
		{"unknown co-signer refused", ReleaseApproval{ApproverID: "ghost", ApproverRole: roleController}, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			job := seedJobWithRetention(st, dirReceivable)
			gl := &fakeGL{}
			svc := newSvc(st).WithGLPoster(gl)
			_, err := svc.ReleaseHoldbackApproved(ctxWith(gm("gmuser")), job.ID, tc.appr)
			if tc.wantStatus == 0 {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				return
			}
			if status(err) != tc.wantStatus {
				t.Fatalf("status=%d, want %d (err=%v)", status(err), tc.wantStatus, err)
			}
			if gl.callCnt != 0 {
				t.Fatalf("a refused release must not post a journal (posts=%d)", gl.callCnt)
			}
		})
	}
}

func TestReleaseHoldback_PayableRefused422(t *testing.T) {
	st := newFakeStore()
	job := seedJobWithRetention(st, dirPayable)
	gl := &fakeGL{}
	svc := newSvc(st).WithGLPoster(gl)
	_, err := svc.ReleaseHoldbackApproved(ctxWith(gm("gmuser")), job.ID, ReleaseApproval{ApproverID: "ctrl", ApproverRole: roleController})
	if status(err) != 422 {
		t.Fatalf("PAYABLE release status=%d, want 422 (err=%v)", status(err), err)
	}
	if gl.callCnt != 0 {
		t.Fatalf("PAYABLE release must not post GL (posts=%d)", gl.callCnt)
	}
}

func TestReleaseHoldback_NothingOutstanding422(t *testing.T) {
	st := newFakeStore()
	job, _ := st.InsertJob(context.Background(), JobInput{JobNo: "J", BranchID: "br1", Direction: dirReceivable}, "u")
	svc := newSvc(st).WithGLPoster(&fakeGL{})
	_, err := svc.ReleaseHoldbackApproved(ctxWith(gm("g")), job.ID, ReleaseApproval{ApproverID: "c", ApproverRole: roleController})
	if status(err) != 422 {
		t.Fatalf("empty-ledger release status=%d, want 422", status(err))
	}
}

// The frozen single-arg entry carries no co-signer → refused (403).
func TestReleaseHoldback_FrozenEntryRefusesWithoutCosigner(t *testing.T) {
	st := newFakeStore()
	job := seedJobWithRetention(st, dirReceivable)
	svc := newSvc(st).WithGLPoster(&fakeGL{})
	err := svc.ReleaseHoldback(ctxWith(gm("g")), job.ID)
	if status(err) != 403 {
		t.Fatalf("frozen ReleaseHoldback status=%d, want 403", status(err))
	}
}

// - AC: template render + data-driven banner + version history -

func TestCreateDocument_RendersVersionedDraftBanner(t *testing.T) {
	st := newFakeStore()
	job, _ := st.InsertJob(context.Background(), JobInput{JobNo: "J-77", BranchID: "br1", Direction: dirReceivable}, "u")
	svc := newSvc(st)
	ctx := ctxWith(gm("u"))

	d1, err := svc.CreateDocument(ctx, DocumentInput{JobID: job.ID, TemplateKind: "CONDITIONAL_RELEASE", Data: map[string]any{"job_no": "J-77", "owner": "Acme"}})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if d1.Version != 1 || !d1.IsDraftBanner {
		t.Fatalf("v=%d draftBanner=%v, want 1/true", d1.Version, d1.IsDraftBanner)
	}
	if !strings.Contains(d1.RenderedContent, "DRAFT") {
		t.Fatalf("rendered content missing DRAFT banner: %q", d1.RenderedContent)
	}
	if !strings.Contains(d1.RenderedContent, "Acme") || !strings.Contains(d1.RenderedContent, "____") {
		t.Fatalf("content should merge owner=Acme and blank missing fields as ____: %q", d1.RenderedContent)
	}
	// Second render bumps the version; version history returns both.
	d2, _ := svc.CreateDocument(ctx, DocumentInput{JobID: job.ID, TemplateKind: "CONDITIONAL_RELEASE", Data: map[string]any{"job_no": "J-77"}})
	if d2.Version != 2 {
		t.Fatalf("second version=%d, want 2", d2.Version)
	}
	hist, _ := svc.DocumentVersions(ctx, d2.ID)
	if len(hist) != 2 {
		t.Fatalf("version history len=%d, want 2", len(hist))
	}
	// Unknown template kind is a 422.
	_, err = svc.CreateDocument(ctx, DocumentInput{JobID: job.ID, TemplateKind: "BOGUS"})
	if status(err) != 422 {
		t.Fatalf("unknown template status=%d, want 422", status(err))
	}
}

// - AC: aging buckets + HST flag + triage badges (pure) -

func TestAgeBucket(t *testing.T) {
	cases := []struct {
		days int
		want string
	}{{0, "0-30"}, {30, "0-30"}, {31, "31-60"}, {60, "31-60"}, {61, "61-90"}, {90, "61-90"}, {91, "90+"}, {400, "90+"}}
	for _, c := range cases {
		if got := ageBucket(c.days); got != c.want {
			t.Errorf("ageBucket(%d)=%s, want %s", c.days, got, c.want)
		}
	}
}

func TestBuildAging_NetsAndBucketsAndEligibility(t *testing.T) {
	now := testClock()
	entries := []HoldbackEntry{
		{JobID: "j1", EntryType: "RETENTION", AmountCents: 10000, CreatedAt: now.AddDate(0, 0, -60)},
		{JobID: "j1", EntryType: "RELEASE", AmountCents: 4000, CreatedAt: now.AddDate(0, 0, -1)},
		{JobID: "j2", EntryType: "RETENTION", AmountCents: 5000, CreatedAt: now.AddDate(0, 0, -10)},
		{JobID: "j3", EntryType: "RETENTION", AmountCents: 7000, CreatedAt: now.AddDate(0, 0, -80)},
		{JobID: "j3", EntryType: "RELEASE", AmountCents: 7000, CreatedAt: now}, // fully released → dropped
	}
	facts := map[string]jobFacts{
		"j1": {JobNo: "J1", BranchID: "br1", Direction: dirReceivable},
		"j2": {JobNo: "J2", BranchID: "br1", Direction: dirReceivable},
		"j3": {JobNo: "J3", BranchID: "br1", Direction: dirReceivable},
	}
	rows := buildAging(entries, facts, now)
	if len(rows) != 2 {
		t.Fatalf("aging rows=%d, want 2 (j3 fully released)", len(rows))
	}
	byJob := map[string]AgingRow{}
	for _, r := range rows {
		byJob[r.JobID] = r
	}
	if byJob["j1"].OutstandingCents != 6000 || byJob["j1"].Bucket != "31-60" || !byJob["j1"].ReleaseEligible {
		t.Fatalf("j1 %+v, want 6000/31-60/eligible", byJob["j1"])
	}
	if byJob["j2"].OutstandingCents != 5000 || byJob["j2"].Bucket != "0-30" || byJob["j2"].ReleaseEligible {
		t.Fatalf("j2 %+v, want 5000/0-30/not-eligible", byJob["j2"])
	}
}

func TestBuildTriage_Badges(t *testing.T) {
	rows := []AgingRow{
		{JobID: "p", Direction: dirPayable, OutstandingCents: 100, OldestDays: 120},
		{JobID: "over", Direction: dirReceivable, OutstandingCents: 100, OldestDays: 200},
		{JobID: "due", Direction: dirReceivable, OutstandingCents: 100, OldestDays: 50},
		{JobID: "aging", Direction: dirReceivable, OutstandingCents: 100, OldestDays: 10},
	}
	got := map[string]string{}
	for _, it := range buildTriage(rows) {
		got[it.JobID] = it.Badge
	}
	want := map[string]string{"p": "PAYABLE_MANUAL", "over": "OVERDUE", "due": "RELEASE_DUE", "aging": "AGING"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("triage[%s]=%s, want %s", k, got[k], v)
		}
	}
}

// status extracts the clientError HTTP status (0 when not a clientError / nil).
func status(err error) int {
	if err == nil {
		return 0
	}
	var ce clientError
	if ok := asClient(err, &ce); ok {
		return ce.status
	}
	return -1
}

func asClient(err error, target *clientError) bool {
	if ce, ok := err.(clientError); ok {
		*target = ce
		return true
	}
	return false
}
