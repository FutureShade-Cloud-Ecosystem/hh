package billing

// ar_reminders_test.go, the reminder machinery's refusals, its record-keeping and its detector.
//
// The two things pinned hardest here are the two the client actually asked for:
//
//   1. A SEND HAS ONE RECIPIENT. There is no path through ResolveTarget that yields a list, and the
//      untagged path is refused unless a person confirmed it. A test that only checked the happy
//      path would pass on a build that blasts.
//   2. THE RECORD OUTLIVES THE TRANSPORT. The row is written before the sender is called, and it
//      says FAILED when the sender fails rather than disappearing.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// -- a fake that satisfies both landing and reminder stores -------------------------------------

type fakeARStore struct {
	policy    ARLandingPolicy
	invoices  []OpenInvoice
	holds     map[string]bool
	engage    map[string]EngagementInput
	flags     []PaperPathEntry
	ctxRow    ReminderContext
	sends     []ReminderSend
	tokens    map[string]string // token → send id
	events    []string
	windows   []EngagementWindow
	raised    []string
	apContact *APContact
	insertErr error
}

func newFakeARStore() *fakeARStore {
	return &fakeARStore{
		policy: DefaultARLandingPolicy(),
		holds:  map[string]bool{},
		engage: map[string]EngagementInput{},
		tokens: map[string]string{},
	}
}

func (f *fakeARStore) ReadARLandingPolicy(context.Context, string) (ARLandingPolicy, error) {
	return f.policy, nil
}
func (f *fakeARStore) UpsertARLandingPolicy(_ context.Context, p ARLandingPolicy) error {
	f.policy = p
	return nil
}
func (f *fakeARStore) OpenInvoices(context.Context, time.Time, string, BranchReach) ([]OpenInvoice, error) {
	return f.invoices, nil
}
func (f *fakeARStore) ActiveHoldCustomers(context.Context) (map[string]bool, error) {
	return f.holds, nil
}
func (f *fakeARStore) EngagementFor(context.Context, []string, int, time.Time) (map[string]EngagementInput, error) {
	return f.engage, nil
}
func (f *fakeARStore) OpenPaperPathFlags(context.Context, BranchReach) ([]PaperPathEntry, error) {
	return f.flags, nil
}
func (f *fakeARStore) ReminderContextFor(context.Context, string, string, string) (ReminderContext, error) {
	rc := f.ctxRow
	rc.APContact = f.apContact
	return rc, nil
}
func (f *fakeARStore) FindSendByKey(_ context.Context, key string) (ReminderSend, bool, error) {
	for _, s := range f.sends {
		if s.IdempotencyKey != "" && s.IdempotencyKey == key {
			return s, true, nil
		}
	}
	return ReminderSend{}, false, nil
}
func (f *fakeARStore) InsertSend(_ context.Context, s ReminderSend, token string) (ReminderSend, error) {
	if f.insertErr != nil {
		return ReminderSend{}, f.insertErr
	}
	s.ID = "send-" + time.Now().Format("150405.000000000")
	f.sends = append(f.sends, s)
	f.tokens[token] = s.ID
	return s, nil
}
func (f *fakeARStore) MarkSendResult(_ context.Context, sendID, status, provider, ref, detail string, at time.Time) error {
	for i := range f.sends {
		if f.sends[i].ID == sendID {
			f.sends[i].Status = status
			f.sends[i].Provider = provider
			if status == SendDelivered {
				t := at
				f.sends[i].DeliveredAt = &t
			}
		}
	}
	return nil
}
func (f *fakeARStore) RecordTrackEvent(_ context.Context, token, event, _ string, at time.Time) (string, bool, error) {
	id, ok := f.tokens[token]
	if !ok {
		return "", false, nil
	}
	f.events = append(f.events, event)
	for i := range f.sends {
		if f.sends[i].ID == id {
			if event == EventOpen {
				f.sends[i].OpenCount++
				if f.sends[i].FirstOpenAt == nil {
					t := at
					f.sends[i].FirstOpenAt = &t
				}
			} else {
				f.sends[i].ClickCount++
				if f.sends[i].FirstClickAt == nil {
					t := at
					f.sends[i].FirstClickAt = &t
				}
			}
		}
	}
	return id, true, nil
}
func (f *fakeARStore) ListSends(context.Context, string, int) ([]ReminderSend, error) {
	return f.sends, nil
}
func (f *fakeARStore) EngagementWindows(context.Context, int, time.Time) ([]EngagementWindow, error) {
	return f.windows, nil
}
func (f *fakeARStore) RaisePaperPathFlag(_ context.Context, e EngagementWindow, _ int, _ time.Time) (bool, error) {
	for _, id := range f.raised {
		if id == e.CustomerID {
			return false, nil
		}
	}
	f.raised = append(f.raised, e.CustomerID)
	return true, nil
}
func (f *fakeARStore) ResolvePaperPathFlag(context.Context, string, string, string, string, time.Time) error {
	return nil
}
func (f *fakeARStore) ReadAPContact(context.Context, string) (APContact, bool, error) {
	if f.apContact == nil {
		return APContact{}, false, nil
	}
	return *f.apContact, true, nil
}
func (f *fakeARStore) UpsertAPContact(_ context.Context, c APContact, _ string) error {
	f.apContact = &c
	return nil
}
func (f *fakeARStore) DeleteAPContact(context.Context, string) error {
	f.apContact = nil
	return nil
}

// recordingSender captures what crossed the seam, and can be made to fail.
type recordingSender struct {
	sent []ReminderMessage
	err  error
}

func (r *recordingSender) Name() string { return "recording" }
func (r *recordingSender) Send(_ context.Context, m ReminderMessage) (ReminderReceipt, error) {
	r.sent = append(r.sent, m)
	if r.err != nil {
		return ReminderReceipt{Provider: "recording"}, r.err
	}
	return ReminderReceipt{Provider: "recording", Ref: "r1", Delivered: true}, nil
}

// -- the recipient rule -------------------------------------------------------------------------

func emailPolicy() ARLandingPolicy {
	p := DefaultARLandingPolicy()
	p.ReminderEmailEnabled = true
	return p
}

func TestResolveTarget_TaggedContactWins(t *testing.T) {
	ap := &APContact{Email: "ap@rood.example", Phone: "+16135550100"}
	got, err := ResolveTarget(ChannelEmail, ap, []string{"foreman@rood.example", "site@rood.example"}, emailPolicy(), false)
	if err != nil {
		t.Fatalf("tagged contact must send: %v", err)
	}
	if got.Recipient != "ap@rood.example" || got.Kind != RecipientAPContact {
		t.Fatalf("target = %+v; the notice goes to the desk that pays, not the account's address book", got)
	}
}

// TestResolveTarget_UntaggedNeedsConfirmation is the requirement in one assertion: with nobody
// tagged, the send does NOT happen quietly.
func TestResolveTarget_UntaggedNeedsConfirmation(t *testing.T) {
	_, err := ResolveTarget(ChannelEmail, nil, []string{"office@rood.example"}, emailPolicy(), false)
	if err == nil {
		t.Fatal("an untagged send with no confirmation must be refused, a silent fallback is the blast")
	}
	if !arContains(err.Error(), "AP") {
		t.Fatalf("the refusal %q must name what is missing", err)
	}
	got, err := ResolveTarget(ChannelEmail, nil, []string{"office@rood.example"}, emailPolicy(), true)
	if err != nil {
		t.Fatalf("a confirmed fallback must send: %v", err)
	}
	if got.Kind != RecipientFallback || got.FallbackReason == "" {
		t.Fatalf("target = %+v; a fallback is recorded as one, with its reason", got)
	}
}

// TestResolveTarget_AmbiguousFallbackIsRefused: three addresses and no tagged contact is a guess,
// and the wrong guess sends a past-due notice to a job site.
func TestResolveTarget_AmbiguousFallbackIsRefused(t *testing.T) {
	_, err := ResolveTarget(ChannelEmail, nil,
		[]string{"a@x.example", "b@x.example", "c@x.example"}, emailPolicy(), true)
	if err == nil {
		t.Fatal("an ambiguous fallback must be refused, not resolved to 'the first one'")
	}
	if !arContains(err.Error(), "3") {
		t.Fatalf("the refusal %q should say how many it found", err)
	}
}

func TestResolveTarget_RefusePolicyStopsUntaggedEntirely(t *testing.T) {
	p := emailPolicy()
	p.UntaggedFallback = FallbackRefuse
	if _, err := ResolveTarget(ChannelEmail, nil, []string{"office@rood.example"}, p, true); err == nil {
		t.Fatal("under REFUSE, even a confirmed untagged send is refused")
	}
}

func TestResolveTarget_DisabledChannelIsRefusedInWords(t *testing.T) {
	p := emailPolicy() // SMS ships off
	ap := &APContact{Phone: "+16135550100"}
	_, err := ResolveTarget(ChannelSMS, ap, nil, p, false)
	if err == nil || !arContains(err.Error(), "text reminders") {
		t.Fatalf("a switched-off channel must refuse by name, got %v", err)
	}
}

func TestResolveTarget_ChannelFallsBackToTheRightField(t *testing.T) {
	p := emailPolicy()
	p.ReminderSMSEnabled = true
	ap := &APContact{Email: "ap@rood.example", Phone: "+16135550100"}
	got, err := ResolveTarget(ChannelSMS, ap, nil, p, false)
	if err != nil {
		t.Fatalf("SMS to a tagged phone: %v", err)
	}
	if got.Recipient != "+16135550100" {
		t.Fatalf("SMS went to %q", got.Recipient)
	}
}

// TestResolveTarget_TaggedButUnreachableOnThisChannel: an AP contact with only an email is not a
// text recipient, and the fallback rules take over rather than the send silently failing.
func TestResolveTarget_TaggedButUnreachableOnThisChannel(t *testing.T) {
	p := emailPolicy()
	p.ReminderSMSEnabled = true
	ap := &APContact{Email: "ap@rood.example"}
	if _, err := ResolveTarget(ChannelSMS, ap, nil, p, true); err == nil {
		t.Fatal("no phone anywhere must be refused, not sent to an email address")
	}
}

func TestValidateAPContact_NeedsAWayToReachThem(t *testing.T) {
	if _, err := ValidateAPContact(APContact{Name: "Dana"}); err == nil {
		t.Fatal("a name with no address or number is not a contact")
	}
	if _, err := ValidateAPContact(APContact{Email: "not-an-address"}); err == nil {
		t.Fatal("an email with no @ must be refused")
	}
	c, err := ValidateAPContact(APContact{Name: " Dana ", Email: " ap@rood.example "})
	if err != nil {
		t.Fatalf("a good contact: %v", err)
	}
	if c.Name != "Dana" || c.Email != "ap@rood.example" {
		t.Fatalf("not trimmed: %+v", c)
	}
}

// -- the watermark ------------------------------------------------------------------------------

func TestWatermarkFor_ResendIsVisiblyAResend(t *testing.T) {
	if got := WatermarkFor(45, false); got != "" {
		t.Fatalf("a first send carries %q; an invoice is not past due because it was emailed", got)
	}
	if got := WatermarkFor(45, true); got != "PAST DUE · 45 DAYS" {
		t.Fatalf("resend watermark = %q", got)
	}
	if got := WatermarkFor(0, true); got != "REISSUED" {
		t.Fatalf("a resend of a current invoice = %q; it is still visibly a resend", got)
	}
	if n := len([]rune(WatermarkFor(9999, true))); n > 40 {
		t.Fatalf("watermark is %d runes; the column caps at 40", n)
	}
}

// -- the black-hole detector --------------------------------------------------------------------

func TestBlackHoleCandidates_BothConditionsAreLoadBearing(t *testing.T) {
	p := DefaultARLandingPolicy() // 45-day window, 3 sends
	now := arDay(2026, 8, 27)
	recent := now.AddDate(0, 0, -3)
	stale := now.AddDate(0, 0, -200)

	rows := []EngagementWindow{
		{CustomerID: "flag-me", DeliveredSends: 4, OpenedSends: 0, FirstSendAt: recent, LastSendAt: recent},
		{CustomerID: "opened-one", DeliveredSends: 9, OpenedSends: 1, FirstSendAt: recent, LastSendAt: recent},
		{CustomerID: "only-two", DeliveredSends: 2, OpenedSends: 0, FirstSendAt: recent, LastSendAt: recent},
		{CustomerID: "ancient", DeliveredSends: 8, OpenedSends: 0, FirstSendAt: stale, LastSendAt: stale},
	}
	got := BlackHoleCandidates(rows, p, now)
	if len(got) != 1 || got[0].CustomerID != "flag-me" {
		t.Fatalf("candidates = %+v; want only flag-me", got)
	}
}

func TestBlackHoleCandidates_WindowIsConfiguration(t *testing.T) {
	p := DefaultARLandingPolicy()
	p.BlackHoleMinSends = 2
	now := arDay(2026, 8, 27)
	recent := now.AddDate(0, 0, -3)
	rows := []EngagementWindow{{CustomerID: "c", DeliveredSends: 2, OpenedSends: 0, LastSendAt: recent}}
	if got := BlackHoleCandidates(rows, p, now); len(got) != 1 {
		t.Fatal("lowering the send floor must change who is flagged, with no code edit")
	}
}

func TestNewTrackToken_IsUnguessableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok, err := NewTrackToken()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if len(tok) < 16 {
			t.Fatalf("token %q is too short for a public URL", tok)
		}
		if seen[tok] {
			t.Fatal("a repeated token would attribute one customer's open to another")
		}
		seen[tok] = true
	}
}

// -- the send, end to end over fakes ------------------------------------------------------------

func sendService(f *fakeARStore, sender ReminderSender, now time.Time) *ARReminderService {
	landing := NewARLandingService(f).WithClock(func() time.Time { return now })
	return NewARReminderService(f, landing, sender, "https://dibbits.example").
		WithClock(func() time.Time { return now })
}

func TestSend_WritesTheRecordAndTheTrackingURLs(t *testing.T) {
	now := arDay(2026, 8, 27)
	f := newFakeARStore()
	// DISTINCTIVE IDS, on purpose. The absence assertion below scans the tracking URL for the
	// internal ids, and the token is 24 random URL-safe bytes, a two-character id like "c1" or "i1"
	// turns up inside a random base64 string often enough to fail this test on a bad afternoon, which
	// is a flake that would eventually be "fixed" by deleting the assertion that matters.
	const custID = "cust-9f3a1c7e"
	const invID = "inv-7a2b4d6f"
	f.apContact = &APContact{CustomerID: custID, Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: custID, CustomerName: "Rood", AccountNo: "A-1",
		InvoiceNo: "INV-1001", DueDate: now.AddDate(0, 0, -95), BalanceCents: 125_00}
	sender := &recordingSender{}

	got, err := sendService(f, sender, now).Send(context.Background(),
		SendReminderInput{CustomerID: custID, InvoiceID: invID, Channel: ChannelEmail, Resend: true}, "u1")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.Status != SendDelivered || got.Bucket != BucketEscalation {
		t.Fatalf("send row = %+v", got)
	}
	if got.RecipientKind != RecipientAPContact || got.Recipient != "ap@rood.example" {
		t.Fatalf("recipient = %s / %s", got.Recipient, got.RecipientKind)
	}
	if got.Watermark != "PAST DUE · 95 DAYS" {
		t.Fatalf("watermark = %q", got.Watermark)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sender saw %d messages", len(sender.sent))
	}
	m := sender.sent[0]
	if m.TrackOpenURL == "" || m.TrackClickURL == "" {
		t.Fatal("a send with no tracking URLs cannot ever be known to have been opened")
	}
	if arContains(m.TrackOpenURL, custID) || arContains(m.TrackOpenURL, invID) {
		t.Fatalf("tracking URL %q carries an internal id, that URL leaves the building", m.TrackOpenURL)
	}
	// Client-facing copy carries no internal vocabulary.
	for _, s := range []string{m.Subject, m.Body} {
		for _, banned := range []string{"ESCALATION", "DIB-", "bucket", "CUSTOMER_ID"} {
			if arContains(s, banned) {
				t.Fatalf("customer-facing copy %q carries %q", s, banned)
			}
		}
	}
}

// TestSend_RefusesWhenNoDocumentQualifies (HH-195): when the reminder context names no document (the
// account owes nothing, or the named one is a credit memo, paid, unknown or another account's), the
// send is refused with a 422 before any row is written and nothing is sent. Unit level on purpose: the
// integration suite that also covers it is on the CI job's red list.
func TestSend_RefusesWhenNoDocumentQualifies(t *testing.T) {
	now := arDay(2026, 8, 27)
	f := newFakeARStore()
	f.apContact = &APContact{CustomerID: "c1", Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: "c1"} // no InvoiceNo: nothing qualified
	sender := &recordingSender{}

	_, err := sendService(f, sender, now).Send(context.Background(),
		SendReminderInput{CustomerID: "c1", Channel: ChannelEmail}, "u1")
	var se *settings.StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity {
		t.Fatalf("err = %v, want a 422 refusal", err)
	}
	if len(f.sends) != 0 {
		t.Fatalf("%d rows written for a refused send, want none", len(f.sends))
	}
	if len(sender.sent) != 0 {
		t.Fatalf("%d messages sent for a refused send, want none", len(sender.sent))
	}
}

// TestSend_FailedTransportStillLeavesARecord: a message that did not go out is a row that says so.
func TestSend_FailedTransportStillLeavesARecord(t *testing.T) {
	now := arDay(2026, 8, 27)
	f := newFakeARStore()
	f.apContact = &APContact{CustomerID: "c1", Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: "c1", InvoiceNo: "INV-1", DueDate: now.AddDate(0, 0, -10)}
	sender := &recordingSender{err: errors.New("transport unreachable")}

	got, err := sendService(f, sender, now).Send(context.Background(),
		SendReminderInput{CustomerID: "c1", Channel: ChannelEmail}, "u1")
	if err != nil {
		t.Fatalf("a failed transport is a recorded outcome, not a request error: %v", err)
	}
	if got.Status != SendFailed {
		t.Fatalf("status = %s, want FAILED", got.Status)
	}
	if len(f.sends) != 1 {
		t.Fatalf("%d rows written; the row is written BEFORE the transport is called", len(f.sends))
	}
}

// TestSend_IdempotentRetryIsOneMessage: a double-clicked button does not text a customer twice.
func TestSend_IdempotentRetryIsOneMessage(t *testing.T) {
	now := arDay(2026, 8, 27)
	f := newFakeARStore()
	f.apContact = &APContact{CustomerID: "c1", Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: "c1", InvoiceNo: "INV-1", DueDate: now.AddDate(0, 0, -10)}
	sender := &recordingSender{}
	svc := sendService(f, sender, now)
	in := SendReminderInput{CustomerID: "c1", Channel: ChannelEmail, IdempotencyKey: "key-1"}

	first, err := svc.Send(context.Background(), in, "u1")
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	second, err := svc.Send(context.Background(), in, "u1")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry made a new row (%s vs %s)", first.ID, second.ID)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("the transport was called %d times for one key", len(sender.sent))
	}
}

// TestTrack_RecordsOpenAndClickAndIgnoresUnknownTokens.
func TestTrack_RecordsOpenAndClick(t *testing.T) {
	now := arDay(2026, 8, 27)
	f := newFakeARStore()
	f.apContact = &APContact{CustomerID: "c1", Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: "c1", InvoiceNo: "INV-1", DueDate: now.AddDate(0, 0, -10)}
	svc := sendService(f, &recordingSender{}, now)
	if _, err := svc.Send(context.Background(), SendReminderInput{CustomerID: "c1", Channel: ChannelEmail}, "u1"); err != nil {
		t.Fatalf("send: %v", err)
	}
	var token string
	for tok := range f.tokens {
		token = tok
	}
	if _, ok := svc.Track(context.Background(), token, EventOpen, "MailClient/1"); !ok {
		t.Fatal("a known token must record an open")
	}
	if _, ok := svc.Track(context.Background(), token, EventClick, "MailClient/1"); !ok {
		t.Fatal("a known token must record a click")
	}
	if _, ok := svc.Track(context.Background(), "not-a-token", EventOpen, ""); ok {
		t.Fatal("an unknown token must not report a hit, that is an enumeration oracle")
	}
	if f.sends[0].OpenCount != 1 || f.sends[0].ClickCount != 1 {
		t.Fatalf("counters = %d open / %d click", f.sends[0].OpenCount, f.sends[0].ClickCount)
	}
	if f.sends[0].FirstOpenAt == nil {
		t.Fatal("first_open_at is what the black-hole rule reads; it must be stamped")
	}
}

// TestStubSenderNeverFabricatesAnOpen: the delivering TEST SEAM reports delivery and nothing else.
// The stub reaches a service only by explicit injection, never as a nil default, see HH-185.
func TestStubSenderNeverFabricatesAnOpen(t *testing.T) {
	now := arDay(2026, 8, 27)
	f := newFakeARStore()
	f.apContact = &APContact{CustomerID: "c1", Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: "c1", InvoiceNo: "INV-1", DueDate: now.AddDate(0, 0, -10)}
	svc := sendService(f, StubSender{}, now) // the stub, injected explicitly as the test seam
	got, err := svc.Send(context.Background(), SendReminderInput{CustomerID: "c1", Channel: ChannelEmail}, "u1")
	if err != nil {
		t.Fatalf("stub send: %v", err)
	}
	if got.Provider != "stub" {
		t.Fatalf("provider = %q, want stub", got.Provider)
	}
	if got.FirstOpenAt != nil || got.OpenCount != 0 {
		t.Fatal("the stub must never fabricate an open, a flag raised on stub data is raised for the true reason")
	}
}

// TestSend_NoTransportIsNeverADeliveredSend, HH-185: the production wiring builds this service with
// NO transport, and a message that never left the server must not be recorded or returned as
// DELIVERED. On the defect the nil sender resolved to the delivering stub, the row said DELIVERED
// with a delivered_at, and the paper-path sweep counted the account as a contacted one.
func TestSend_NoTransportIsNeverADeliveredSend(t *testing.T) {
	now := arDay(2026, 8, 27)
	f := newFakeARStore()
	f.apContact = &APContact{CustomerID: "c1", Email: "ap@rood.example"}
	f.ctxRow = ReminderContext{CustomerID: "c1", InvoiceNo: "INV-1", DueDate: now.AddDate(0, 0, -10)}

	svc := sendService(f, nil, now) // a nil sender resolves to the unwired default, the same transport the composition root names in words
	got, err := svc.Send(context.Background(), SendReminderInput{CustomerID: "c1", Channel: ChannelEmail}, "u1")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.Status != SendFailed {
		t.Fatalf("status = %s, want %s: FAILED is this module's own word for a send that did not go out", got.Status, SendFailed)
	}
	if got.DeliveredAt != nil {
		t.Fatal("delivered_at is stamped on a message that never left the server")
	}
	if got.Provider == "stub" {
		t.Fatalf("provider = %q: the delivering stub must never be what a nil sender resolves to", got.Provider)
	}
	if len(f.sends) != 1 {
		t.Fatalf("%d rows written; the record is the durable part, an unsent message is still a recorded outcome", len(f.sends))
	}
	if stored := f.sends[0]; stored.Status != SendFailed || stored.DeliveredAt != nil {
		t.Fatalf("stored row = %s / delivered_at %v: the row the sweep counts must not say DELIVERED", stored.Status, stored.DeliveredAt)
	}
}

// TestSweepPaperPath_IsIdempotent: the detector may run nightly without piling flags.
func TestSweepPaperPath_IsIdempotent(t *testing.T) {
	now := arDay(2026, 8, 27)
	f := newFakeARStore()
	f.windows = []EngagementWindow{
		{CustomerID: "dark", DeliveredSends: 5, OpenedSends: 0, LastSendAt: now.AddDate(0, 0, -2)},
		{CustomerID: "reader", DeliveredSends: 5, OpenedSends: 2, LastSendAt: now.AddDate(0, 0, -2)},
	}
	svc := sendService(f, nil, now)
	evaluated, raised, err := svc.SweepPaperPath(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if evaluated != 2 || raised != 1 {
		t.Fatalf("sweep evaluated %d raised %d, want 2 / 1", evaluated, raised)
	}
	_, raised2, _ := svc.SweepPaperPath(context.Background())
	if raised2 != 0 {
		t.Fatalf("a second nightly run raised %d more flags", raised2)
	}
}

func TestResolvePaperPathFlag_NeedsADecisionAndAReason(t *testing.T) {
	f := newFakeARStore()
	svc := sendService(f, nil, arDay(2026, 8, 27))
	if err := svc.ResolvePaperPathFlag(context.Background(), "f1", "IGNORED", "x", "u1"); err == nil {
		t.Fatal("an unknown resolution must be refused")
	}
	if err := svc.ResolvePaperPathFlag(context.Background(), "f1", "MOVED_TO_PAPER", "  ", "u1"); err == nil {
		t.Fatal("closing a flag with no reason must be refused, the next person reads it")
	}
	if err := svc.ResolvePaperPathFlag(context.Background(), "f1", "MOVED_TO_PAPER", "posting statements", "u1"); err != nil {
		t.Fatalf("a good resolution: %v", err)
	}
}
