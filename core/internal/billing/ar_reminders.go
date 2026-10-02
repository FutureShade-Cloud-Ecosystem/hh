package billing

// ar_reminders.go, the reminder machinery behind the landing: the provider-agnostic send seam, the
// per-send open/click tracking that is the durable part, the AP contact a send is AIMED at, the
// past-due watermark on a resend, and the paper-path black-hole detector.
//
// -- THE SEAM, AND WHY THERE IS NO VENDOR IN THIS FILE ------------------------------------------
//
// The client said "Twilio-style". That is a description of a capability, not an instruction to
// import a vendor. Nothing in this file, this package, or this lane imports or names a vendor SDK:
// ReminderSender is the seam, UnwiredSender is the shipped default and refuses in words, StubSender
// is the delivering test seam a test injects explicitly, and an adapter for any real transport is a
// file somebody adds later without touching a single line
// here. The SMS arm this program already ships (internal/messagingprovider) is deliberately NOT
// imported either, it owns the outbound consent/quiet-hours cadence for marketing-shaped notices
// and its own outbox, and reaching into it from AR would couple two independently-flagged switches.
// The bridge, when a deployment wants one, is an adapter that satisfies ReminderSender.
//
// -- WHAT IS ACTUALLY DURABLE -------------------------------------------------------------------
//
// Not the send. The RECORD of the send, and the record of whether a human ever opened it. A
// provider swap, a credential rotation and a vendor change all leave ar_reminder_sends intact,
// because the row records what WE decided and what came back, never a vendor's object. That is the
// whole reason the black-hole flag can exist at all: "we have sent this account four notices and
// nobody has ever opened one" is only knowable if the opens were written down at the time.
//
// -- THE BLAST IS THE DEFECT --------------------------------------------------------------------
//
// A collections notice has exactly one right recipient: the person who pays the bills. Sending it to
// every address and number on the account is not thoroughness, it is how a past-due notice reaches
// the customer's foreman, and how the office stops using the feature. So ResolveTarget returns ONE
// recipient or an error, and the no-tagged-contact path is a DELIBERATE, RECORDED fallback the
// operator has to choose: `confirmed` is a required input, `recipient_kind` is stored as FALLBACK
// forever, and the reason is stored beside it.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// -- channels and vocabulary --------------------------------------------------------------------

const (
	ChannelEmail = "EMAIL"
	ChannelSMS   = "SMS"

	RecipientAPContact = "AP_CONTACT"
	RecipientFallback  = "FALLBACK"

	SendQueued    = "QUEUED"
	SendDelivered = "DELIVERED"
	SendFailed    = "FAILED"

	EventDelivered = "DELIVERED"
	EventOpen      = "OPEN"
	EventClick     = "CLICK"
	EventBounce    = "BOUNCE"
	EventFailed    = "FAILED"
)

// -- the send seam ------------------------------------------------------------------------------

// ReminderMessage is what crosses the seam. It carries the two tracking URLs already rendered,
// because the transport must never need to know what a track token is.
type ReminderMessage struct {
	Channel       string
	To            string
	Subject       string
	Body          string
	TrackOpenURL  string
	TrackClickURL string
	// Watermark is the stamp the rendered invoice carries. Empty on a first send.
	Watermark string
}

// ReminderReceipt is what comes back. `Ref` is opaque, whatever the transport calls its own
// handle, and is stored as a string with no shape imposed on it.
type ReminderReceipt struct {
	Provider  string
	Ref       string
	Delivered bool
	Detail    string
}

// ReminderSender is the whole vendor surface: one method, one name.
type ReminderSender interface {
	Name() string
	Send(ctx context.Context, m ReminderMessage) (ReminderReceipt, error)
}

// StubSender is the TEST seam. It is injected explicitly by tests that need a delivering transport,
// and it is NEVER what a nil sender resolves to: a server that records DELIVERED for messages
// nothing sent is HH-185, the paper-path sweep counted the phantom deliveries and flagged
// customers nobody had contacted, so the nil default is UnwiredSender below.
//
// IT REPORTS DELIVERED ON PURPOSE, and that deserves a sentence rather than a shrug: the tracking
// machinery, the cadence and the black-hole detector all key off DELIVERED, and a stub that reported
// QUEUED forever would make every one of them untestable without a vendor account. What the stub
// does NOT do is fabricate an open, no stub anywhere in this package writes an OPEN or CLICK event,
// so a flag raised on stub data is raised for the true reason: nobody opened anything.
type StubSender struct{}

// Name identifies the transport in the stored row.
func (StubSender) Name() string { return "stub" }

// Send records nothing anywhere and returns a delivered receipt with a ref that is visibly a stub.
func (StubSender) Send(_ context.Context, m ReminderMessage) (ReminderReceipt, error) {
	return ReminderReceipt{Provider: "stub", Ref: "stub-" + strings.ToLower(m.Channel), Delivered: true,
		Detail: "no transport is wired on this server"}, nil
}

// UnwiredSender is the default, and the one this program ships on.
//
// IT REPORTS A FAILURE ON PURPOSE. Nothing in this build delivers AR reminders, and a nil sender
// that resolved to the delivering stub is HH-185: every send came back DELIVERED though nothing
// left the server, the paper-path sweep counted the phantom rows, and a customer who was never
// contacted was flagged as unreachable by email. The send is still recorded, the row is written
// first and then marked FAILED with these words, the same posture as purchasing's UnwiredSender:
// a collector who BELIEVES a notice went out is how an account gets chased over a channel that
// never carried it.
type UnwiredSender struct{}

// Name identifies the transport in the stored row.
func (UnwiredSender) Name() string { return "unwired" }

// Send delivers nothing and says so.
func (UnwiredSender) Send(_ context.Context, _ ReminderMessage) (ReminderReceipt, error) {
	return ReminderReceipt{Provider: "unwired", Delivered: false,
		Detail: "no transport is wired on this server, nothing left the building"}, nil
}

// -- the AP contact, and the one recipient a send is aimed at -----------------------------------

// APContact is one account's designated accounts-payable contact.
type APContact struct {
	CustomerID string `json:"customer_id"`
	Name       string `json:"contact_name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// Reachable answers whether this contact can be reached on a channel at all.
func (c APContact) Reachable(channel string) bool {
	if channel == ChannelSMS {
		return strings.TrimSpace(c.Phone) != ""
	}
	return strings.TrimSpace(c.Email) != ""
}

// ValidateAPContact refuses an unreachable or oversized contact in words before the CHECKs could
// 500. A contact with neither an address nor a number is not a contact, it is a name in a box.
func ValidateAPContact(c APContact) (APContact, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Email = strings.TrimSpace(c.Email)
	c.Phone = strings.TrimSpace(c.Phone)
	if len([]rune(c.Name)) > 120 {
		return APContact{}, settings.Errf(http.StatusUnprocessableEntity, "ar: contact_name is at most 120 characters")
	}
	if len(c.Email) > 254 || len(c.Phone) > 32 {
		return APContact{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: an AP email is at most 254 characters and a phone at most 32")
	}
	if c.Email != "" && (!strings.Contains(c.Email, "@") || strings.ContainsAny(c.Email, " \t")) {
		return APContact{}, settings.Errf(http.StatusUnprocessableEntity, "ar: that AP email address is not an address")
	}
	if c.Email == "" && c.Phone == "" {
		return APContact{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: an AP contact needs an email or a phone, one of the two is what makes it a contact")
	}
	return c, nil
}

// SendTarget is the ONE recipient a send goes to, and the story of how it was chosen.
type SendTarget struct {
	Recipient      string `json:"recipient"`
	Kind           string `json:"recipient_kind"`
	FallbackReason string `json:"fallback_reason,omitempty"`
}

// ResolveTarget picks the single recipient for a send.
//
// It returns ONE address or an error. There is no code path in this package that returns a list,
// which is the structural version of the client's requirement, a blast is not something an operator
// can accidentally choose here because it is not something this function can express.
//
//	tagged     the account's AP contact, or nil when nobody has tagged one.
//	candidates the account's other addresses/numbers for this channel, in the account's own order.
//	confirmed  the operator explicitly chose to send without a tagged contact. Under CONFIRM this is
//	           REQUIRED: an unconfirmed untagged send is refused, so the fallback is always a
//	           deliberate act with words attached rather than a default that happens quietly.
func ResolveTarget(channel string, tagged *APContact, candidates []string, p ARLandingPolicy, confirmed bool) (SendTarget, error) {
	switch channel {
	case ChannelEmail, ChannelSMS:
	default:
		return SendTarget{}, settings.Errf(http.StatusUnprocessableEntity, "ar: channel must be EMAIL or SMS")
	}
	if channel == ChannelEmail && !p.ReminderEmailEnabled {
		return SendTarget{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: email reminders are switched off for this dealer, turn them on in AR policy first")
	}
	if channel == ChannelSMS && !p.ReminderSMSEnabled {
		return SendTarget{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: text reminders are switched off for this dealer, turn them on in AR policy first")
	}
	if tagged != nil && tagged.Reachable(channel) {
		addr := strings.TrimSpace(tagged.Email)
		if channel == ChannelSMS {
			addr = strings.TrimSpace(tagged.Phone)
		}
		return SendTarget{Recipient: addr, Kind: RecipientAPContact}, nil
	}
	if p.UntaggedFallback == FallbackRefuse {
		return SendTarget{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: no AP contact is tagged on this account, tag one and the reminder goes to the right desk")
	}
	clean := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if t := strings.TrimSpace(c); t != "" {
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		return SendTarget{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: this account has no address on this channel, tag an AP contact to send anything at all")
	}
	if !confirmed {
		return SendTarget{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: nobody is tagged for AP on this account, confirm the fallback recipient or tag a contact")
	}
	// AMBIGUITY IS REFUSED, not resolved. Picking "the first one" out of three addresses is a guess
	// wearing a default's clothes, and the wrong guess sends a past-due notice to a job site.
	if len(clean) > 1 {
		return SendTarget{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: this account has %d addresses on this channel and no AP contact, tag one so the notice has one desk to land on", len(clean))
	}
	return SendTarget{
		Recipient:      clean[0],
		Kind:           RecipientFallback,
		FallbackReason: "no AP contact tagged; operator confirmed the account address",
	}, nil
}

// -- the past-due watermark ---------------------------------------------------------------------

// WatermarkFor is the stamp a RESENT invoice carries.
//
// A first send carries nothing, an invoice is not past due because it was emailed. A resend always
// carries something, because the requirement is that a resend is VISIBLY a resend: a customer who
// receives the same document twice with no mark has no way to tell which one is the current claim,
// and neither does the clerk they forward it to.
//
// The text is returned rather than rendered so the SEND ROW can store the exact words that went out.
// A dealer who rewords this next quarter must not retroactively change what a customer was shown.
func WatermarkFor(daysPastDue int, resend bool) string {
	if !resend {
		return ""
	}
	if daysPastDue <= 0 {
		return "REISSUED"
	}
	return fmt.Sprintf("PAST DUE · %d DAYS", daysPastDue)
}

// -- the black-hole detector --------------------------------------------------------------------

// EngagementWindow is one account's send/open record inside the policy window.
type EngagementWindow struct {
	CustomerID     string
	CustomerName   string
	AccountNo      string
	DeliveredSends int
	OpenedSends    int
	FirstSendAt    time.Time
	LastSendAt     time.Time
}

// BlackHoleCandidates picks the accounts whose reminders are delivered and never opened.
//
// TWO CONDITIONS, AND BOTH ARE LOAD-BEARING. A window with no send floor flags an account we
// messaged once last Tuesday; a send floor with no window flags an account that stopped opening
// email two years ago and has paid by cheque ever since. The pair together says the thing the client
// actually described: we have tried, repeatedly, recently, and it is going nowhere.
//
// An account with even ONE open is never a candidate. Not "few opens", not "a low rate", one. The
// flag's meaning is "digital delivery does not reach this customer at all", and a customer who
// opened one notice in March disproves it.
func BlackHoleCandidates(rows []EngagementWindow, p ARLandingPolicy, now time.Time) []EngagementWindow {
	cutoff := now.Add(-time.Duration(p.BlackHoleWindowDays) * 24 * time.Hour)
	out := make([]EngagementWindow, 0, len(rows))
	for _, r := range rows {
		if r.OpenedSends > 0 {
			continue
		}
		if r.DeliveredSends < p.BlackHoleMinSends {
			continue
		}
		// The evidence has to be CURRENT. A run of unopened notices that ended before the window
		// opened is history, and raising a flag on it today would put a decision in front of
		// somebody about a state of affairs nobody has tested recently.
		if r.LastSendAt.Before(cutoff) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeliveredSends != out[j].DeliveredSends {
			return out[i].DeliveredSends > out[j].DeliveredSends
		}
		return out[i].CustomerID < out[j].CustomerID
	})
	return out
}

// -- tracking tokens ----------------------------------------------------------------------------

// NewTrackToken mints the unguessable handle the public tracking endpoints accept.
//
// IT IS THE ONLY IDENTIFIER THAT LEAVES THE BUILDING. No customer id, no invoice id and no send id
// ever appears in a URL a customer's mail client fetches, a tracking pixel is a URL that travels
// through forwarding chains, corporate scanners and screenshots, and anything readable in it is
// effectively published. 24 random bytes, URL-safe, no padding.
func NewTrackToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("billing: track token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// -- the send record ----------------------------------------------------------------------------

// ReminderSend is one row of ar_reminder_sends on the wire.
type ReminderSend struct {
	ID             string     `json:"id"`
	CustomerID     string     `json:"customer_id"`
	InvoiceID      string     `json:"invoice_id,omitempty"`
	Channel        string     `json:"channel"`
	Recipient      string     `json:"recipient"`
	RecipientKind  string     `json:"recipient_kind"`
	FallbackReason string     `json:"fallback_reason,omitempty"`
	Bucket         string     `json:"bucket"`
	Resend         bool       `json:"resend"`
	Watermark      string     `json:"watermark,omitempty"`
	Status         string     `json:"status"`
	Provider       string     `json:"provider"`
	SentAt         time.Time  `json:"sent_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	FirstOpenAt    *time.Time `json:"first_open_at,omitempty"`
	FirstClickAt   *time.Time `json:"first_click_at,omitempty"`
	OpenCount      int        `json:"open_count"`
	ClickCount     int        `json:"click_count"`

	// The one-time key that makes an identical retry reuse this row. Never serialized outward: it is
	// the CLIENT's own string, and echoing somebody's idempotency keys back at them is how one
	// operator's double-click becomes another's replay.
	IdempotencyKey string `json:"-"`
}

// SendReminderInput is one deliberate reminder.
type SendReminderInput struct {
	CustomerID string `json:"customer_id"`
	InvoiceID  string `json:"invoice_id"`
	Channel    string `json:"channel"`
	Resend     bool   `json:"resend"`
	// ConfirmFallback is the operator's explicit "yes, send it to the account address", required
	// whenever no AP contact is tagged. It is an input rather than a policy so that the decision is
	// made per send, by a person, in front of the account they are looking at.
	ConfirmFallback bool   `json:"confirm_fallback"`
	IdempotencyKey  string `json:"idempotency_key"`
}

// ReminderContext is everything the send needs about the account, read in one go by the store.
type ReminderContext struct {
	CustomerID   string
	CustomerName string
	AccountNo    string
	APContact    *APContact
	Candidates   []string
	InvoiceNo    string
	DueDate      time.Time
	BalanceCents int64
}

// ARReminderStore is the reminder machinery's persistence seam.
type ARReminderStore interface {
	ReminderContextFor(ctx context.Context, customerID, invoiceID, channel string) (ReminderContext, error)
	FindSendByKey(ctx context.Context, key string) (ReminderSend, bool, error)
	InsertSend(ctx context.Context, s ReminderSend, trackToken string) (ReminderSend, error)
	MarkSendResult(ctx context.Context, sendID, status, provider, providerRef, detail string, at time.Time) error
	RecordTrackEvent(ctx context.Context, token, event, userAgent string, at time.Time) (string, bool, error)
	ListSends(ctx context.Context, customerID string, limit int) ([]ReminderSend, error)
	EngagementWindows(ctx context.Context, windowDays int, now time.Time) ([]EngagementWindow, error)
	RaisePaperPathFlag(ctx context.Context, w EngagementWindow, windowDays int, now time.Time) (bool, error)
	ResolvePaperPathFlag(ctx context.Context, flagID, status, note, actorID string, at time.Time) error
	ReadAPContact(ctx context.Context, customerID string) (APContact, bool, error)
	UpsertAPContact(ctx context.Context, c APContact, actorID string) error
	DeleteAPContact(ctx context.Context, customerID string) error
}

// ARReminderService sends reminders and owns the tracking.
type ARReminderService struct {
	store   ARReminderStore
	landing *ARLandingService
	sender  ReminderSender
	baseURL string
	now     func() time.Time
}

// NewARReminderService wires the machinery. A nil sender means UnwiredSender, which is the default
// this program ships on, an unwired transport is visibly inert, never quietly half-running: the row
// is written and marked FAILED in words rather than pretending a delivery nobody made.
func NewARReminderService(store ARReminderStore, landing *ARLandingService, sender ReminderSender, baseURL string) *ARReminderService {
	if sender == nil {
		sender = UnwiredSender{}
	}
	return &ARReminderService{store: store, landing: landing, sender: sender,
		baseURL: strings.TrimRight(baseURL, "/"), now: time.Now}
}

// WithClock injects the clock.
func (s *ARReminderService) WithClock(now func() time.Time) *ARReminderService {
	if now != nil {
		s.now = now
	}
	return s
}

// trackURL renders one public tracking URL for a token.
func (s *ARReminderService) trackURL(token, kind string) string {
	return fmt.Sprintf("%s/api/public/v1/ar/track/%s/%s", s.baseURL, token, kind)
}

// Send sends one reminder and writes the record that outlives it.
//
// ORDER MATTERS AND IS NOT AN ACCIDENT: the row is inserted BEFORE the transport is called. A send
// that leaves the building with no row behind it is a message a customer received that this business
// has no record of, which is strictly worse than a row for a message that failed, the row can say
// FAILED, and does.
func (s *ARReminderService) Send(ctx context.Context, in SendReminderInput, actorID string) (ReminderSend, error) {
	if in.IdempotencyKey != "" {
		if prior, ok, err := s.store.FindSendByKey(ctx, in.IdempotencyKey); err == nil && ok {
			// An identical retry reuses its key and its row: a double-clicked button is one message.
			return prior, nil
		}
	}
	p := s.landing.Policy(ctx, "")
	rc, err := s.store.ReminderContextFor(ctx, in.CustomerID, in.InvoiceID, in.Channel)
	if err != nil {
		return ReminderSend{}, err
	}
	if rc.InvoiceNo == "" {
		// No qualifying document: the account owes nothing, or the named document is a credit memo,
		// is paid, is unknown or belongs to another account. A reminder about nothing is a confident wrong
		// number, so refuse before any row is written.
		return ReminderSend{}, settings.Errf(http.StatusUnprocessableEntity,
			"ar: there is no open invoice to remind about for this request")
	}
	target, err := ResolveTarget(in.Channel, rc.APContact, rc.Candidates, p, in.ConfirmFallback)
	if err != nil {
		return ReminderSend{}, err
	}
	now := s.now()
	days := DaysPastDue(rc.DueDate, now)
	bucket := BucketFor(days, p)
	watermark := WatermarkFor(days, in.Resend)

	token, err := NewTrackToken()
	if err != nil {
		return ReminderSend{}, err
	}
	row := ReminderSend{
		CustomerID: rc.CustomerID, InvoiceID: in.InvoiceID, Channel: in.Channel,
		Recipient: target.Recipient, RecipientKind: target.Kind, FallbackReason: target.FallbackReason,
		Bucket: bucket, Resend: in.Resend, Watermark: watermark,
		Status: SendQueued, Provider: s.sender.Name(), SentAt: now,
		IdempotencyKey: in.IdempotencyKey,
	}
	saved, err := s.store.InsertSend(ctx, row, token)
	if err != nil {
		return ReminderSend{}, err
	}

	receipt, sendErr := s.sender.Send(ctx, ReminderMessage{
		Channel:       in.Channel,
		To:            target.Recipient,
		Subject:       arLandingReminderSubject(rc, bucket, watermark),
		Body:          arLandingReminderBody(rc, days, watermark),
		TrackOpenURL:  s.trackURL(token, "open"),
		TrackClickURL: s.trackURL(token, "click"),
		Watermark:     watermark,
	})
	status, detail := SendDelivered, receipt.Detail
	if sendErr != nil || !receipt.Delivered {
		status = SendFailed
		if sendErr != nil {
			detail = sendErr.Error()
		}
	}
	if err := s.store.MarkSendResult(ctx, saved.ID, status, receipt.Provider, receipt.Ref, detail, s.now()); err != nil {
		return saved, err
	}
	saved.Status = status
	saved.Provider = receipt.Provider
	if status == SendDelivered {
		at := s.now()
		saved.DeliveredAt = &at
	}
	return saved, nil
}

// arLandingReminderSubject and arLandingReminderBody are the AR-landing message text (union train 13
// renamed them off the plain reminderSubject/reminderBody, which the invoices register lane defines
// for its own tone-based chase in register_messages.go, two surfaces, two texts, both kept). They are plain, short, and carry ZERO
// internal vocabulary, no ticket number, no bucket code, no policy name. A customer reads this.
func arLandingReminderSubject(rc ReminderContext, bucket, watermark string) string {
	if watermark != "" {
		return fmt.Sprintf("%s, invoice %s", watermark, rc.InvoiceNo)
	}
	if bucket == BucketCurrent {
		return fmt.Sprintf("Invoice %s", rc.InvoiceNo)
	}
	return fmt.Sprintf("Invoice %s is past due", rc.InvoiceNo)
}

func arLandingReminderBody(rc ReminderContext, days int, watermark string) string {
	amount := money(rc.BalanceCents)
	if days <= 0 {
		return fmt.Sprintf("Invoice %s for %s is due %s.", rc.InvoiceNo, amount, rc.DueDate.Format("2 January 2006"))
	}
	stamp := ""
	if watermark != "" {
		stamp = " " + watermark + "."
	}
	return fmt.Sprintf("Invoice %s for %s was due %s, %d days ago.%s",
		rc.InvoiceNo, amount, rc.DueDate.Format("2 January 2006"), days, stamp)
}

// Track records one open or click against a token. It returns the send id it touched.
//
// IT NEVER ERRORS OUTWARD ON AN UNKNOWN TOKEN, and that is a security decision rather than
// sloppiness: this endpoint is public, so an unknown token that answered differently from a known
// one would turn a tracking pixel into an oracle for enumerating sends.
func (s *ARReminderService) Track(ctx context.Context, token, event, userAgent string) (string, bool) {
	switch event {
	case EventOpen, EventClick:
	default:
		return "", false
	}
	id, ok, err := s.store.RecordTrackEvent(ctx, token, event, userAgent, s.now())
	if err != nil {
		return "", false
	}
	return id, ok
}

// History lists an account's sends, newest first.
func (s *ARReminderService) History(ctx context.Context, customerID string, limit int) ([]ReminderSend, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.store.ListSends(ctx, customerID, limit)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []ReminderSend{}
	}
	return rows, nil
}

// SweepPaperPath runs the black-hole detector and raises a flag per newly-qualifying account.
// It reports how many it looked at and how many it raised, a sweep that says only "done" is a
// sweep nobody can tell apart from a sweep that did nothing.
func (s *ARReminderService) SweepPaperPath(ctx context.Context) (evaluated, raised int, err error) {
	p := s.landing.Policy(ctx, "")
	now := s.now()
	rows, err := s.store.EngagementWindows(ctx, p.BlackHoleWindowDays, now)
	if err != nil {
		return 0, 0, err
	}
	for _, c := range BlackHoleCandidates(rows, p, now) {
		ok, err := s.store.RaisePaperPathFlag(ctx, c, p.BlackHoleWindowDays, now)
		if err != nil {
			return len(rows), raised, err
		}
		if ok {
			raised++
		}
	}
	return len(rows), raised, nil
}

// ResolvePaperPathFlag closes a flag with a decision attached.
func (s *ARReminderService) ResolvePaperPathFlag(ctx context.Context, flagID, status, note, actorID string) error {
	switch status {
	case "MOVED_TO_PAPER", "DISMISSED":
	default:
		return settings.Errf(http.StatusUnprocessableEntity,
			"ar: a paper-path flag closes as MOVED_TO_PAPER or DISMISSED, both are decisions, and both need one")
	}
	if strings.TrimSpace(note) == "" {
		return settings.Errf(http.StatusUnprocessableEntity,
			"ar: say why in a few words, the next person to open this account reads it, not the flag")
	}
	if len([]rune(note)) > 300 {
		return settings.Errf(http.StatusUnprocessableEntity, "ar: that note is longer than 300 characters")
	}
	return s.store.ResolvePaperPathFlag(ctx, flagID, status, note, actorID, s.now())
}

// APContact reads one account's tagged contact.
func (s *ARReminderService) APContact(ctx context.Context, customerID string) (APContact, bool, error) {
	return s.store.ReadAPContact(ctx, customerID)
}

// TagAPContact writes one account's tagged contact.
func (s *ARReminderService) TagAPContact(ctx context.Context, c APContact, actorID string) (APContact, error) {
	clean, err := ValidateAPContact(c)
	if err != nil {
		return APContact{}, err
	}
	if err := s.store.UpsertAPContact(ctx, clean, actorID); err != nil {
		return APContact{}, err
	}
	return clean, nil
}

// UntagAPContact removes it. The sends already made keep their AP_CONTACT kind, see 0183.
func (s *ARReminderService) UntagAPContact(ctx context.Context, customerID string) error {
	return s.store.DeleteAPContact(ctx, customerID)
}
