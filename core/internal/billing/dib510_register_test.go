package billing

// dib510_register_test.go, the receivables register's DECISIONS, unit-tested without a database.
//
// Everything in here is a pure function or a service call over an in-memory fake, and that split is
// deliberate: the SQL is proved in dib510_register_integration_test.go against the real schema, and
// the judgements, which pill a document wears, who may see an account's position, what a refusal
// says, are proved here where a failure names the rule rather than a query plan.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 9, 30, 0, 0, time.UTC)
}

// -- the derived status -------------------------------------------------------------------------

func TestRegisterStatus_DerivesThePillFromDueDateNotStoredStatus(t *testing.T) {
	asOf := day(2026, time.August, 26)
	cases := []struct {
		name       string
		row        RegisterRow
		wantStatus string
		wantDays   int
	}{
		{"current invoice inside the window is DUE_SOON",
			RegisterRow{StoredStatus: "FINALIZED", DueDate: day(2026, time.August, 30)}, StatusDueSoon, 0},
		{"current invoice beyond the window is OPEN",
			RegisterRow{StoredStatus: "FINALIZED", DueDate: day(2026, time.September, 30)}, StatusOpen, 0},
		{"past due is OVERDUE with the day count",
			RegisterRow{StoredStatus: "FINALIZED", DueDate: day(2026, time.May, 27)}, StatusOverdue, 91},
		{"part-paid and not yet due is PARTIALLY_PAID",
			RegisterRow{StoredStatus: "PARTIALLY_PAID", DueDate: day(2026, time.September, 20)}, StatusPartiallyPaid, 0},
		{"part-paid AND late is OVERDUE, lateness outranks part payment",
			RegisterRow{StoredStatus: "PARTIALLY_PAID", DueDate: day(2026, time.July, 10)}, StatusOverdue, 47},
		{"settled is PAID whatever the date says",
			RegisterRow{StoredStatus: "PAID", DueDate: day(2026, time.January, 1)}, StatusPaid, 0},
		{"a credit memo is a CREDIT, never an overdue invoice",
			RegisterRow{DocType: "CREDIT_MEMO", StoredStatus: "FINALIZED", DueDate: day(2026, time.January, 1)}, StatusCredit, 0},
		{"draft and void keep their own names",
			RegisterRow{StoredStatus: "DRAFT", DueDate: day(2026, time.January, 1)}, StatusDraft, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, days := registerStatus(tc.row, asOf, 7)
			if got != tc.wantStatus || days != tc.wantDays {
				t.Fatalf("got %s/%d, want %s/%d", got, days, tc.wantStatus, tc.wantDays)
			}
		})
	}
}

func TestRegisterStatus_DisputeOutranksLateness(t *testing.T) {
	asOf := day(2026, time.August, 26)
	row := RegisterRow{StoredStatus: "FINALIZED", DueDate: day(2026, time.June, 19), Disputed: true}
	got, days := registerStatus(row, asOf, 7)
	if got != StatusDisputed {
		t.Fatalf("a disputed invoice must read DISPUTED, got %s", got)
	}
	// The day count survives: the pill says why it is unpaid, the number still says how long.
	if days != 68 {
		t.Fatalf("the day count must survive the dispute, got %d", days)
	}
}

func TestDaysBetween_IsWholeCalendarDaysAndInclusiveOfToday(t *testing.T) {
	// Two instants on the same calendar day are zero days apart however far apart their clocks are -
	// an invoice is never "0.6 days late". The due date is a calendar date and the clock is read on
	// the house day, so the clock here is early in the house morning, not at midnight UTC (which is
	// the previous evening in the yard).
	if got := daysBetween(
		time.Date(2026, time.August, 26, 23, 59, 0, 0, time.UTC),
		time.Date(2026, time.August, 26, 0, 1, 0, 0, houseday.Location())); got != 0 {
		t.Fatalf("same day must be 0, got %d", got)
	}
	if got := daysBetween(day(2026, time.August, 25), day(2026, time.August, 26)); got != 1 {
		t.Fatalf("due yesterday is 1 day overdue, got %d", got)
	}
	if got := daysBetween(day(2026, time.August, 27), day(2026, time.August, 26)); got != -1 {
		t.Fatalf("due tomorrow is -1, got %d", got)
	}
}

// -- the account-status line --------------------------------------------------------------------

func TestAccountStatusFor_SeverityOrder(t *testing.T) {
	if got := accountStatusFor(true, true, 500); got != AccountCreditHold {
		t.Fatalf("a hold outranks everything, got %s", got)
	}
	if got := accountStatusFor(false, true, 500); got != AccountUnderReview {
		t.Fatalf("an open dispute reads UNDER_REVIEW, got %s", got)
	}
	if got := accountStatusFor(false, false, 500); got != AccountCreditOK {
		t.Fatalf("a balance inside terms reads CREDIT_APPROVED, got %s", got)
	}
	if got := accountStatusFor(false, false, 0); got != AccountCurrent {
		t.Fatalf("nothing outstanding reads CURRENT, got %s", got)
	}
}

// -- the gate -----------------------------------------------------------------------------------

func callerAs(tier identity.Tier, scope identity.Scope) func(context.Context) (identity.StaffContext, bool) {
	return func(context.Context) (identity.StaffContext, bool) {
		// A branch scoped caller holds its branch grant, as a real one does (the register's branch fence).
		return identity.StaffContext{UserID: "u1", Tier: tier, Scope: scope, Status: "ACTIVE",
			Branches: map[string]bool{"b1": true}}, true
	}
}

func noCaller() func(context.Context) (identity.StaffContext, bool) {
	return func(context.Context) (identity.StaffContext, bool) { return identity.StaffContext{}, false }
}

func TestAccountAllowed_FailsClosed(t *testing.T) {
	ctx := context.Background()
	manager := callerAs(identity.TierManager, identity.ScopeGlobal)
	frontline := callerAs(identity.TierFrontline, identity.ScopeBranch)

	if !accountAllowed(ctx, manager, VisibilityManagerUp) {
		t.Fatal("a manager must receive the account position under MANAGER_UP")
	}
	if accountAllowed(ctx, frontline, VisibilityManagerUp) {
		t.Fatal("a frontline caller must NOT receive it under MANAGER_UP")
	}
	if !accountAllowed(ctx, frontline, VisibilityAllStaff) {
		t.Fatal("ALL_STAFF is the dealer's explicit choice and must be honoured")
	}
	if accountAllowed(ctx, manager, VisibilityHidden) {
		t.Fatal("HIDDEN refuses everybody, manager included")
	}
	if accountAllowed(ctx, noCaller(), VisibilityAllStaff) {
		t.Fatal("an unauthenticated caller is refused even under ALL_STAFF")
	}
	// An unrecognised value, a future migration adding a fourth vocabulary word without teaching
	// this function about it, must refuse rather than default open.
	if accountAllowed(ctx, manager, "EVERYONE_PLEASE") {
		t.Fatal("an unknown visibility value must fail closed")
	}
	if accountAllowed(ctx, manager, "") {
		t.Fatal("an empty visibility value must fail closed")
	}
}

// -- policy parsing -----------------------------------------------------------------------------

func TestSplitTones_TrimsBlanksAndUppercases(t *testing.T) {
	got := splitTones(" friendly , ,FIRM ,, final ")
	want := []string{"FRIENDLY", "FIRM", "FINAL"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(splitTones("  , ,")) != 0 {
		t.Fatal("an all-blank setting is no tones at all, not one blank tone")
	}
}

func TestToneRank_KeepsEscalationOrder(t *testing.T) {
	if !(toneRank(ToneFriendly) < toneRank(ToneFirm) && toneRank(ToneFirm) < toneRank(ToneFinal)) {
		t.Fatal("tones must rank friendly < firm < final so a selector never invites the wrong click")
	}
}

// -- money formatting on the printed document ---------------------------------------------------

func TestMoney_FormatsCentsWithoutInventingAnything(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "$0.00"}, {5, "$0.05"}, {99999, "$999.99"},
		{549180, "$5,491.80"}, {104864000, "$1,048,640.00"}, {-47460, "−$474.60"},
	} {
		if got := money(tc.in); got != tc.want {
			t.Fatalf("money(%d) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestTrimQty_DropsStoredScaleForDisplayOnly(t *testing.T) {
	for in, want := range map[string]string{
		"18.0000": "18", "4.8611": "4.8611", "10.5000": "10.5", "9": "9",
	} {
		if got := trimQty(in); got != want {
			t.Fatalf("trimQty(%q) = %q, want %q", in, got, want)
		}
	}
}

// -- the reminder copy --------------------------------------------------------------------------

func TestReminderBody_SameFactsEveryTone_ConsequenceOnlyInFinal(t *testing.T) {
	due := day(2026, time.May, 27)
	facts := "$4,542.60"
	var bodies []string
	for _, tone := range []string{ToneFriendly, ToneFirm, ToneFinal} {
		b := reminderBody(tone, "Quinte Hardscapes Inc.", "INV-2019", 454260, due)
		if !strings.Contains(b, facts) || !strings.Contains(b, "INV-2019") || !strings.Contains(b, "27 May 2026") {
			t.Fatalf("%s tone dropped a fact: %s", tone, b)
		}
		bodies = append(bodies, b)
	}
	if strings.Contains(bodies[0], "hold") || strings.Contains(bodies[1], "hold") {
		t.Fatal("only the final notice names a consequence")
	}
	if !strings.Contains(bodies[2], "on hold") {
		t.Fatal("the final notice must name the consequence this system can actually carry out")
	}
	// And it must be the one we can carry out, not a legal threat nobody authorised.
	for _, b := range bodies {
		for _, forbidden := range []string{"legal", "collections agency", "court", "lawyer"} {
			if strings.Contains(strings.ToLower(b), forbidden) {
				t.Fatalf("a reminder must not threaten what this system cannot do: %q", forbidden)
			}
		}
	}
}

func TestReminderSubject_EscalatesWithTone(t *testing.T) {
	if reminderSubject(ToneFriendly, "INV-1") == reminderSubject(ToneFinal, "INV-1") {
		t.Fatal("a final notice must not arrive under the same subject line as a nudge")
	}
	if !strings.Contains(reminderSubject(ToneFinal, "INV-1"), "Final notice") {
		t.Fatal("the final notice says so in the subject")
	}
}

// -- CSV export ---------------------------------------------------------------------------------

func TestCsvCell_QuotesSeparatorsSoOneCustomerStaysOneColumn(t *testing.T) {
	if got := csvCell(`Bay of Quinte Landscaping, Inc.`); got != `"Bay of Quinte Landscaping, Inc."` {
		t.Fatalf("a comma in a name must be quoted, got %s", got)
	}
	if got := csvCell(`He said "yes"`); got != `"He said ""yes"""` {
		t.Fatalf("embedded quotes must be doubled, got %s", got)
	}
	if got := csvCell("Plain"); got != "Plain" {
		t.Fatalf("a plain cell is not quoted, got %s", got)
	}
}

func TestCentsCell_IsASummableNumberNotASentence(t *testing.T) {
	for in, want := range map[int64]string{549180: "5491.80", 0: "0.00", -47460: "-474.60"} {
		if got := centsCell(in); got != want {
			t.Fatalf("centsCell(%d) = %s, want %s", in, got, want)
		}
	}
	if strings.ContainsAny(centsCell(549180), "$,") {
		t.Fatal("an export cell carries no currency mark or separator, the destination is a sum")
	}
}

// -- the printed document -----------------------------------------------------------------------

func TestRenderInvoiceHTML_PrintsServerFiguresAndEscapes(t *testing.T) {
	d := InvoiceDetail{
		InvoiceNo: "INV-2117", IssueDate: day(2026, time.July, 15), DueDate: day(2026, time.August, 14),
		Customer: CustomerBlock{Name: `Quinte & Sons <Hardscapes>`, AccountNo: "CUST-1001", TermsDays: 30},
		Lines: []DetailLine{{LineNo: 1, SKU: "DBS-AGG-34CLEAR", Description: "3/4\" Clear Stone",
			QtyEntered: "18.0000", UOMEntered: "T", UnitPriceCents: 1800, LineTotalCents: 324000}},
		Totals: Totals{SubtotalCents: 486000, TaxCents: 63180, TotalCents: 549180,
			AppliedCents: 150000, BalanceCents: 399180},
	}
	html := renderInvoiceHTML(d, "Dibbits, Trenton")
	for _, want := range []string{"INV-2117", "$4,860.00", "$631.80", "$5,491.80", "−$1,500.00", "$3,991.80",
		"Dibbits, Trenton", "18", "Quinte &amp; Sons &lt;Hardscapes&gt;"} {
		if !strings.Contains(html, want) {
			t.Fatalf("the printed invoice is missing %q", want)
		}
	}
	if strings.Contains(html, "<Hardscapes>") {
		t.Fatal("a customer name must be escaped, not injected")
	}
	// Self-contained: no external asset can be fetched by a machine that cannot reach this server.
	for _, forbidden := range []string{"<script", "http://", "https://"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("the document must be self-contained; found %q", forbidden)
		}
	}
	// A document with nothing applied does not print a zero deposit line, an empty row labelled
	// "payments" reads as a payment that failed.
	d.Totals.AppliedCents = 0
	if strings.Contains(renderInvoiceHTML(d, "x"), "Deposit &amp; payments") {
		t.Fatal("no applied money means no applied-money line")
	}
}

// -- the service's refusals, over a fake store --------------------------------------------------

type fakeRegisterStore struct {
	policy   RegisterPolicy
	header   InvoiceHeaderFacts
	headerNF bool
	promises []PromiseWrite
	sends    []SendRow
	// seenKeys are the idempotency keys the store has already honoured, the fake's own replay
	// guard, so the fake behaves like the unique index the real store carries.
	seenKeys map[string]bool
}

func (f *fakeRegisterStore) RegisterPolicyFor(context.Context, string) (RegisterPolicy, error) {
	return f.policy, nil
}
func (f *fakeRegisterStore) SetRegisterPolicy(context.Context, RegisterPolicy) (bool, error) {
	return true, nil
}
func (f *fakeRegisterStore) RegisterRows(context.Context, time.Time, int, RegisterFilter) ([]RegisterRow, int, error) {
	return nil, 0, nil
}
func (f *fakeRegisterStore) RegisterCountsFor(context.Context, time.Time, int, RegisterFilter) (RegisterCounts, error) {
	return RegisterCounts{}, nil
}
func (f *fakeRegisterStore) SelectionTotals(context.Context, []string, BranchReach) (int64, int, error) {
	return 0, 0, nil
}
func (f *fakeRegisterStore) InvoiceHeader(context.Context, string) (InvoiceHeaderFacts, error) {
	if f.headerNF {
		return InvoiceHeaderFacts{}, ErrNotFound
	}
	return f.header, nil
}
func (f *fakeRegisterStore) DetailLines(context.Context, string) ([]DetailLine, error) {
	return nil, nil
}
func (f *fakeRegisterStore) AppliedCents(context.Context, string) (int64, error) { return 0, nil }
func (f *fakeRegisterStore) CustomerBlockFor(context.Context, string) (CustomerBlock, bool, bool, error) {
	return CustomerBlock{Name: "Test Co", ContactEmail: "a@b.example"}, false, false, nil
}
func (f *fakeRegisterStore) AccountPositionFor(context.Context, time.Time, string) (AccountPosition, error) {
	return AccountPosition{OpenBalanceCents: 1}, nil
}
func (f *fakeRegisterStore) ProofFor(context.Context, string) (Proof, bool, error) {
	return Proof{}, false, nil
}
func (f *fakeRegisterStore) OrderFactsFor(context.Context, string) (string, string, string, error) {
	return "", "", "", nil
}
func (f *fakeRegisterStore) ActivityFor(context.Context, string, string, bool) ([]ActivityEntry, error) {
	return nil, nil
}
func (f *fakeRegisterStore) PromiseFor(context.Context, string) (PromiseView, bool, error) {
	return PromiseView{}, false, nil
}
func (f *fakeRegisterStore) DisputeFor(context.Context, string) (DisputeView, bool, error) {
	return DisputeView{}, false, nil
}
func (f *fakeRegisterStore) LastContactFor(context.Context, string) (*time.Time, error) {
	return nil, nil
}
func (f *fakeRegisterStore) RecordSend(_ context.Context, d SendRow) (string, bool, error) {
	f.sends = append(f.sends, d)
	if f.seenKeys == nil {
		f.seenKeys = map[string]bool{}
	}
	if f.seenKeys[d.IdempotencyKey] {
		return "send-1", true, nil
	}
	f.seenKeys[d.IdempotencyKey] = true
	return "send-1", false, nil
}
func (f *fakeRegisterStore) SendByKey(_ context.Context, key string) (string, bool, error) {
	if f.seenKeys[key] {
		return "send-1", true, nil
	}
	return "", false, nil
}
func (f *fakeRegisterStore) MarkOpened(context.Context, string, time.Time) (bool, error) {
	return true, nil
}
func (f *fakeRegisterStore) LatestSendID(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeRegisterStore) AddPromise(_ context.Context, p PromiseWrite) (PromiseView, bool, bool, error) {
	f.promises = append(f.promises, p)
	return PromiseView{PromisedCents: p.PromisedCents}, false, false, nil
}
func (f *fakeRegisterStore) SettlePromise(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (f *fakeRegisterStore) OpenDispute(context.Context, DisputeWrite) (DisputeView, bool, bool, error) {
	return DisputeView{}, false, false, nil
}
func (f *fakeRegisterStore) ResolveDispute(context.Context, string, string, string) (bool, error) {
	return true, nil
}

func newFakeService(t *testing.T, store *fakeRegisterStore, now time.Time) *RegisterService {
	t.Helper()
	return NewRegisterService(store).
		WithClock(func() time.Time { return now }).
		withCaller(callerAs(identity.TierManager, identity.ScopeGlobal))
}

func TestAddPromise_RefusesTheThingsThatWouldPoisonTheWorklist(t *testing.T) {
	now := day(2026, time.August, 26)
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7},
		header: InvoiceHeaderFacts{ID: "i1", CustomerID: "c1", BalanceCents: 100000},
	}
	svc := newFakeService(t, store, now)
	ctx := context.Background()

	if _, _, err := svc.AddPromise(ctx, PromiseInput{PromisedCents: 1000, PromisedDate: "2026-09-01"}); err == nil {
		t.Fatal("a promise with no idempotency key must be refused, not defaulted")
	}
	if _, _, err := svc.AddPromise(ctx, PromiseInput{PromisedCents: 0, PromisedDate: "2026-09-01", IdempotencyKey: "k"}); err == nil {
		t.Fatal("a zero promise must be refused")
	}
	if _, _, err := svc.AddPromise(ctx, PromiseInput{PromisedCents: 1000, PromisedDate: "next tuesday", IdempotencyKey: "k"}); err == nil {
		t.Fatal("a promise date must be a calendar date")
	}
	if _, _, err := svc.AddPromise(ctx, PromiseInput{PromisedCents: 1000, PromisedDate: "2026-08-25", IdempotencyKey: "k"}); err == nil {
		t.Fatal("a promise for a date already gone is broken the moment it is written")
	}
	if _, _, err := svc.AddPromise(ctx, PromiseInput{PromisedCents: 100001, PromisedDate: "2026-09-01", IdempotencyKey: "k"}); err == nil {
		t.Fatal("a promise cannot exceed what is owed")
	}
	if _, _, err := svc.AddPromise(ctx, PromiseInput{PromisedCents: 100000, PromisedDate: "2026-08-26", IdempotencyKey: "k"}); err != nil {
		t.Fatalf("today is a valid promise date: %v", err)
	}
	if len(store.promises) != 1 {
		t.Fatalf("exactly one promise should have reached the store, got %d", len(store.promises))
	}
}

func TestSendReminder_RefusesATonePolicyDoesNotOffer(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly, ToneFirm}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 5000},
	}
	svc := newFakeService(t, store, day(2026, time.August, 26))
	_, err := svc.SendReminder(context.Background(), ReminderInput{
		InvoiceIDs: []string{"i1"}, Tone: ToneFinal, IdempotencyKey: "k"})
	if err == nil {
		t.Fatal("curation hides controls; POLICY is the authority and must refuse the tone server-side")
	}
	if !strings.Contains(err.Error(), "FRIENDLY, FIRM") {
		t.Fatalf("the refusal must name what IS offered, got %v", err)
	}
}

func TestSendReminder_NeverChasesMoneyAlreadyReceived(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
			ReminderTones: []string{ToneFriendly}},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BalanceCents: 0},
	}
	svc := newFakeService(t, store, day(2026, time.August, 26))
	res, err := svc.SendReminder(context.Background(), ReminderInput{
		InvoiceIDs: []string{"i1"}, Tone: ToneFriendly, IdempotencyKey: "k"})
	if err != nil {
		t.Fatalf("a settled document is skipped, not an error: %v", err)
	}
	if res.Sent != 0 || res.Skipped["INV-1"] != "nothing outstanding" {
		t.Fatalf("a settled invoice must be skipped with a reason, got %+v", res)
	}
	if len(store.sends) != 0 {
		t.Fatal("nothing may be recorded as sent when nothing was sent")
	}
}

func TestRecordPayment_RefusesWhenTheMoneySeamIsUnwired(t *testing.T) {
	svc := newFakeService(t, &fakeRegisterStore{}, day(2026, time.August, 26))
	_, err := svc.RecordPayment(context.Background(), ReceiptInput{
		InvoiceID: "i1", Method: "CHEQUE", AmountCents: 100, IdempotencyKey: "k"})
	if err == nil {
		t.Fatal("an unwired money seam must refuse in words, never half-work")
	}
}

func TestSetPolicy_IsAdminOnlyAndValidatesInWords(t *testing.T) {
	store := &fakeRegisterStore{}
	base := RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7,
		ReminderCooldownHours: 48, WorkspaceSplitPct: 58, WorkspaceArrangement: "PRIMARY_LEFT"}

	manager := NewRegisterService(store).withCaller(callerAs(identity.TierManager, identity.ScopeGlobal))
	if err := manager.SetPolicy(context.Background(), base); err == nil {
		t.Fatal("a manager may not change dealer screen settings")
	}

	admin := NewRegisterService(store).withCaller(callerAs(identity.TierLeadership, identity.ScopeGlobal))
	if err := admin.SetPolicy(context.Background(), base); err != nil {
		t.Fatalf("an admin may: %v", err)
	}
	bad := base
	bad.AccountPositionVisibility = "EVERYONE"
	if err := admin.SetPolicy(context.Background(), bad); err == nil {
		t.Fatal("an unknown visibility value must be refused before it reaches a CHECK")
	}
	bad = base
	bad.DueSoonDays = 0
	if err := admin.SetPolicy(context.Background(), bad); err == nil {
		t.Fatal("a zero due-soon window must be refused")
	}
	bad = base
	bad.ReminderTones = []string{"SHOUTY"}
	if err := admin.SetPolicy(context.Background(), bad); err == nil {
		t.Fatal("an invented tone must be refused")
	}
}

func TestSetPolicy_NormalisesTonesIntoEscalationOrderAndDeduplicates(t *testing.T) {
	captured := RegisterPolicy{}
	wrapped := &capturingStore{
		fakeRegisterStore: &fakeRegisterStore{},
		onSet:             func(p RegisterPolicy) { captured = p },
	}
	admin := NewRegisterService(wrapped).withCaller(callerAs(identity.TierLeadership, identity.ScopeGlobal))

	err := admin.SetPolicy(context.Background(), RegisterPolicy{
		AccountPositionVisibility: VisibilityAllStaff, DueSoonDays: 14, ReminderCooldownHours: 24,
		WorkspaceSplitPct: 60, WorkspaceArrangement: "PRIMARY_RIGHT",
		ReminderTones: []string{"final", "FRIENDLY", "final", "firm"},
	})
	if err != nil {
		t.Fatalf("valid policy: %v", err)
	}
	if strings.Join(captured.ReminderTones, ",") != "FRIENDLY,FIRM,FINAL" {
		t.Fatalf("tones must be deduplicated and ordered by escalation, got %v", captured.ReminderTones)
	}
}

type capturingStore struct {
	*fakeRegisterStore
	onSet func(RegisterPolicy)
}

func (c *capturingStore) SetRegisterPolicy(_ context.Context, p RegisterPolicy) (bool, error) {
	c.onSet(p)
	return true, nil
}

func TestDetail_AccountPositionIsAbsentForARefusedCaller(t *testing.T) {
	store := &fakeRegisterStore{
		policy: RegisterPolicy{AccountPositionVisibility: VisibilityManagerUp, DueSoonDays: 7},
		header: InvoiceHeaderFacts{ID: "i1", InvoiceNo: "INV-1", CustomerID: "c1", BranchID: "b1",
			StoredStatus: "FINALIZED", DueDate: day(2026, time.September, 30),
			SubtotalCents: 1000, TaxCents: 130, TotalCents: 1130, BalanceCents: 1130},
	}
	frontline := NewRegisterService(store).
		WithClock(func() time.Time { return day(2026, time.August, 26) }).
		withCaller(callerAs(identity.TierFrontline, identity.ScopeBranch))
	d, err := frontline.Detail(context.Background(), "i1", true)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if d.Customer.Account != nil {
		t.Fatal("the account position must be ABSENT (a nil pointer, therefore no JSON key) for a refused caller")
	}
	// The invoice's own figures are NOT gated, the customer is holding that document.
	if d.Totals.BalanceCents != 1130 || d.Totals.TotalCents != 1130 {
		t.Fatal("an invoice's own money is never gated")
	}
	// ...and the operational account STATUS still reaches them, because the POS gate will stop the
	// sale anyway and the person making it is entitled to know why.
	if d.Customer.AccountStatus == "" {
		t.Fatal("the account status line is operational and must reach every staff caller")
	}

	manager := NewRegisterService(store).
		WithClock(func() time.Time { return day(2026, time.August, 26) }).
		withCaller(callerAs(identity.TierManager, identity.ScopeGlobal))
	dm, err := manager.Detail(context.Background(), "i1", true)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if dm.Customer.Account == nil {
		t.Fatal("a manager must receive the account position")
	}
}

func TestDetail_NotFoundIsNotFound(t *testing.T) {
	store := &fakeRegisterStore{headerNF: true}
	svc := newFakeService(t, store, day(2026, time.August, 26))
	if _, err := svc.Detail(context.Background(), "nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing invoice is ErrNotFound, got %v", err)
	}
}
