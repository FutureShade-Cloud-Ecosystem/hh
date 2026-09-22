package customerquote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The service layer: the tenant-side lifecycle (build, update, send, revise,
// the rules R1 to R11) and the public leg (view, accept), where the token is
// the whole authority and no principal exists (D23). Refusals carry the
// pinned families: 400 shape failures with the ported sentences, 404
// never-existed, 410 dead link, 409 not-awaiting-acceptance naming why, 503
// operational inability and data defects, fail closed everywhere.

// Error is a refusal the HTTP layer maps by status and message.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(status int, msg string) *Error { return &Error{Status: status, Message: msg} }

// The public-leg body constants. The malformed and never-existed cells share
// one body so existence does not leak; the 410 body is the ported HH Pro
// dead-link sentence.
const (
	MsgLinkNotExist  = "This quote link does not exist."
	MsgNotAwaiting   = "This proposal is not awaiting acceptance."
	MsgBusy          = "customer quote service dependencies unavailable"
	defaultValidDays = 14   // the carried default offer window (the reference's DEFAULT_VALID_DAYS)
	defaultMarkupBps = 2200 // the carried default markup (the reference's 22 percent, in basis points)
)

// Config carries the injectable collaborators of the service.
type Config struct {
	// Now is the clock; tests inject, production uses time.Now.
	Now func() time.Time
	// Mailer is the delivery seam of the send (D34). Nil selects the
	// no-provider stub, which is the honest default, not a fallback: the
	// core holds no mail server and no provider accounts of its own.
	Mailer Mailer
}

// Scope is the tenant leg's stamped scope: the org from the Team behind the
// token and the tenant from the token (ADR-0020 leg 4), handed down by the
// guard so a scopeless handler is unrepresentable. The public leg has no
// scope: the token match is the whole authorization decision.
type Scope struct {
	OrgID    string
	TenantID string
}

// Service is the customer-quote module.
type Service struct {
	store   *Store
	seam    AuditSeam
	keys    *SigningKeys
	mail    Mailer
	cfg     Config
	defects DefectCounter
}

// NewService validates every collaborator and returns the service. The audit
// seam and the signing keys are spawn-time requirements, never optional: an
// acceptance without its audit row would be an unsigned acceptance, and a
// signature without a key cannot exist (ADR-0022's mandatory-write and
// key-schedule pins).
func NewService(store *Store, seam AuditSeam, keys *SigningKeys, cfg Config) (*Service, error) {
	if store == nil {
		return nil, errors.New("customerquote: store is required")
	}
	if seam == nil {
		return nil, errors.New("customerquote: audit seam is required (the audit write is mandatory)")
	}
	if keys == nil {
		return nil, errors.New("customerquote: signing keys are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Mailer == nil {
		cfg.Mailer = NoProviderMailer{}
	}
	return &Service{store: store, seam: seam, keys: keys, mail: cfg.Mailer, cfg: cfg}, nil
}

// Defects exposes the loud data-defect counter.
func (s *Service) Defects() *DefectCounter { return &s.defects }

func (s *Service) now() time.Time { return s.cfg.Now().UTC() }

// The decoded document of one row.
type document struct {
	Lines          []Line
	Labour         []LabourLine
	Overhead       []OverheadLine
	Contact        CustomerContact
	ChangeRequests []ChangeRequest
}

func decodeDoc(row QuoteRow) (document, error) {
	var d document
	if len(row.LinesJSON) > 0 {
		if err := json.Unmarshal(row.LinesJSON, &d.Lines); err != nil {
			return d, fmt.Errorf("customerquote: stored lines: %w", err)
		}
	}
	if len(row.LabourJSON) > 0 {
		if err := json.Unmarshal(row.LabourJSON, &d.Labour); err != nil {
			return d, fmt.Errorf("customerquote: stored labour lines: %w", err)
		}
	}
	if len(row.OverheadJSON) > 0 {
		if err := json.Unmarshal(row.OverheadJSON, &d.Overhead); err != nil {
			return d, fmt.Errorf("customerquote: stored overhead lines: %w", err)
		}
	}
	if len(row.CustomerContactJSON) > 0 {
		if err := json.Unmarshal(row.CustomerContactJSON, &d.Contact); err != nil {
			return d, fmt.Errorf("customerquote: stored customer contact: %w", err)
		}
	}
	if len(row.ChangeRequestsJSON) > 0 {
		if err := json.Unmarshal(row.ChangeRequestsJSON, &d.ChangeRequests); err != nil {
			return d, fmt.Errorf("customerquote: stored change requests: %w", err)
		}
	}
	return d, nil
}

// Token resolution, per ADR-0022 decision 1's classification.

type tokenClass int

const (
	tokenMalformed tokenClass = iota
	tokenUnknown
	tokenDead
	tokenLive
)

// resolveToken classifies a presented raw token against the stored states.
// The parse happens before any match claim; unparsable stored values are
// skipped, counted loudly, and never change the response alone; more than one
// matching row is a data defect refused closed in the 503 family; a
// tombstoned quote answers 410 for its tokens; a live hash with no link bound
// fails closed to 410.
func (s *Service) resolveToken(ctx context.Context, raw string) (QuoteRow, tokenClass, error) {
	tok := strings.TrimSpace(raw)
	if !TokenWellFormed(tok) {
		return QuoteRow{}, tokenMalformed, nil
	}
	h := HashToken(tok)
	rows, err := s.store.ResolveByTokenHash(ctx, h)
	if err != nil {
		return QuoteRow{}, tokenMalformed, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	var matched *QuoteRow
	var matchedState TokenState
	for i := range rows {
		if rows[i].ShareTokenHash == nil {
			continue
		}
		state, perr := ParseTokenState(*rows[i].ShareTokenHash)
		if perr != nil {
			s.defects.UnparsableStoredValue()
			continue
		}
		live, dead := state.Matches(h)
		if !live && !dead {
			continue
		}
		if matched != nil {
			s.defects.MultiMatch()
			return QuoteRow{}, tokenMalformed, refuse(http.StatusServiceUnavailable, MsgBusy)
		}
		matched = &rows[i]
		matchedState = state
	}
	if matched == nil {
		return QuoteRow{}, tokenUnknown, nil
	}
	if matched.DeletedAt != nil {
		s.defects.TombstoneGone()
		return *matched, tokenDead, nil
	}
	if _, dead := matchedState.Matches(h); dead {
		return *matched, tokenDead, nil
	}
	if matched.ShareExpiresAt == nil {
		s.defects.NullLinkBound()
		return *matched, tokenDead, nil
	}
	if !s.now().Before(*matched.ShareExpiresAt) {
		return *matched, tokenDead, nil
	}
	return *matched, tokenLive, nil
}

// The tenant-side payload shapes. The tenant view carries the margin (R7:
// margin is for the tenant's own view only); the stored token encoding never
// renders, only the boolean that a live token exists.
type TenantQuote struct {
	ID              string          `json:"id"`
	Status          Status          `json:"status"`
	Customer        CustomerContact `json:"customer_contact"`
	MarkupBps       int64           `json:"markup_bps"`
	HideLinePrices  bool            `json:"hide_line_prices"`
	Lines           []Line          `json:"lines"`
	LabourLines     []LabourLine    `json:"labour_lines"`
	OverheadLines   []OverheadLine  `json:"overhead_lines"`
	ValidUntil      *time.Time      `json:"valid_until,omitempty"`
	SupplierHorizon *time.Time      `json:"supplier_price_horizon,omitempty"`
	Totals          Totals          `json:"totals"`
	ViewCount       int64           `json:"view_count"`
	HasLiveToken    bool            `json:"has_live_token"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// SendResult is the send response: the one place the raw token appears.
type SendResult struct {
	TenantQuote
	MagicLinkToken string `json:"magic_link_token"`
}

func (s *Service) tenantQuote(row QuoteRow) (TenantQuote, error) {
	doc, err := decodeDoc(row)
	if err != nil {
		return TenantQuote{}, err
	}
	totals, err := ComputeTotals(doc.Lines, row.MarkupBps, doc.Labour, doc.Overhead)
	if err != nil {
		return TenantQuote{}, err
	}
	live := false
	if row.ShareTokenHash != nil {
		if state, perr := ParseTokenState(*row.ShareTokenHash); perr == nil {
			live = state.Live != ""
		}
	}
	return TenantQuote{
		ID: row.ID, Status: row.Status, Customer: doc.Contact, MarkupBps: row.MarkupBps,
		HideLinePrices: row.HideLinePrices, Lines: doc.Lines, LabourLines: doc.Labour,
		OverheadLines: doc.Overhead, ValidUntil: row.ValidUntil, SupplierHorizon: row.SupplierHorizon,
		Totals: totals, ViewCount: row.ViewCount, HasLiveToken: live,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// CreateInput is the build payload.
type CreateInput struct {
	Customer       CustomerContact `json:"customer_contact"`
	MarkupBps      *int64          `json:"markup_bps"`
	HideLinePrices bool            `json:"hide_line_prices"`
	ValidUntil     *time.Time      `json:"valid_until"`
	Lines          []Line          `json:"lines"`
	LabourLines    []LabourLine    `json:"labour_lines"`
	OverheadLines  []OverheadLine  `json:"overhead_lines"`
}

// UpdateInput is the update payload; nil fields keep the stored value.
type UpdateInput struct {
	Customer       *CustomerContact `json:"customer_contact"`
	MarkupBps      *int64           `json:"markup_bps"`
	HideLinePrices *bool            `json:"hide_line_prices"`
	ValidUntil     *time.Time       `json:"valid_until"`
	Lines          []Line           `json:"lines"`
	LabourLines    []LabourLine     `json:"labour_lines"`
	OverheadLines  []OverheadLine   `json:"overhead_lines"`
}

// markupBpsOrCarried resolves the markup: the carried default when unset, the
// client's basis points otherwise (the percent surface converts at the
// boundary only).
func markupBpsOrCarried(set *int64) int64 {
	if set == nil {
		return defaultMarkupBps
	}
	return *set
}

// Create builds a draft quote (the tenant leg's build operation). R2 refuses
// unpriced lines with the ported sentence; R3 caps the offer bound by the
// earliest supplier price expiry; the status starts at draft (R1).
func (s *Service) Create(ctx context.Context, scope Scope, in CreateInput) (TenantQuote, error) {
	markup := markupBpsOrCarried(in.MarkupBps)
	if _, err := ValidateLineSet(in.Lines, markup, in.OverheadLines, s.now()); err != nil {
		return TenantQuote{}, refuse(http.StatusBadRequest, err.Error())
	}
	if names := UnpricedLineNames(in.Lines, s.now()); len(names) > 0 {
		return TenantQuote{}, refuse(http.StatusBadRequest, RefuseUnpriced(names))
	}
	horizon := SupplierPriceHorizon(in.Lines)
	requested := s.now().AddDate(0, 0, defaultValidDays)
	if in.ValidUntil != nil {
		requested = *in.ValidUntil
	}
	validUntil := CapValidUntil(requested, horizon)
	// The creation write draws the row's seq under the customer_quote scope
	// on the creation transaction itself (the 0213 convention: the server
	// assigns seq inside the business transaction, gaps allowed, never
	// reused).
	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	defer tx.Rollback(ctx)
	seq, serr := s.store.AllocateSeqTx(ctx, tx, ScopeCustomerQuote)
	if serr != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	row, err := s.store.InsertQuote(ctx, tx, QuoteRow{
		OrgID: scope.OrgID, TenantID: scope.TenantID,
		MarkupBps: markup, LinesJSON: mustJSON(in.Lines), LabourJSON: mustJSON(in.LabourLines),
		OverheadJSON: mustJSON(in.OverheadLines), HideLinePrices: in.HideLinePrices,
		CustomerContactJSON: mustJSON(in.Customer), ValidUntil: &validUntil, SupplierHorizon: horizon,
	}, seq)
	if err != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	return s.tenantQuote(row)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

// Update applies draft-state edits (R5): an accepted quote refuses
// absolutely; any sent or responded quote refuses with the sentence naming
// the revise remedy; the cap re-applies on every update (R3); the unpriced
// refusal re-runs on the new line set (R2).
func (s *Service) Update(ctx context.Context, scope Scope, quoteID string, in UpdateInput) (TenantQuote, error) {
	row, err := s.store.GetQuoteForTenant(ctx, s.store.Pool(), scope.TenantID, scope.OrgID, quoteID)
	if err != nil {
		if errors.Is(err, ErrQuoteNotFound) {
			return TenantQuote{}, refuse(http.StatusNotFound, SentenceQuoteGone)
		}
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if row.Status == StatusAccepted {
		return TenantQuote{}, refuse(http.StatusConflict, SentenceAcceptedNoChange)
	}
	if row.Status != StatusDraft {
		return TenantQuote{}, refuse(http.StatusConflict, fmt.Sprintf(SentenceSentUpdate, quoteRef(row)))
	}
	doc, err := decodeDoc(row)
	if err != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	lines := doc.Lines
	if in.Lines != nil {
		lines = in.Lines
	}
	labour := doc.Labour
	if in.LabourLines != nil {
		labour = in.LabourLines
	}
	overhead := doc.Overhead
	if in.OverheadLines != nil {
		overhead = in.OverheadLines
	}
	contact := doc.Contact
	if in.Customer != nil {
		contact = *in.Customer
	}
	// An absent markup field keeps the stored value on update (the carried
	// default applies only at build).
	markup := row.MarkupBps
	if in.MarkupBps != nil {
		markup = *in.MarkupBps
	}
	hide := row.HideLinePrices
	if in.HideLinePrices != nil {
		hide = *in.HideLinePrices
	}
	validUntil := row.ValidUntil
	if in.ValidUntil != nil {
		validUntil = in.ValidUntil
	}
	if _, err := ValidateLineSet(lines, markup, overhead, s.now()); err != nil {
		return TenantQuote{}, refuse(http.StatusBadRequest, err.Error())
	}
	if names := UnpricedLineNames(lines, s.now()); len(names) > 0 {
		return TenantQuote{}, refuse(http.StatusBadRequest, RefuseUnpriced(names))
	}
	horizon := SupplierPriceHorizon(lines)
	var capped *time.Time
	if validUntil != nil {
		v := CapValidUntil(*validUntil, horizon)
		capped = &v
	}
	updated, err := s.store.UpdateDraftQuote(ctx, s.store.Pool(), scope.TenantID, scope.OrgID, quoteID, QuoteRow{
		MarkupBps: markup, LinesJSON: mustJSON(lines), LabourJSON: mustJSON(labour),
		OverheadJSON: mustJSON(overhead), HideLinePrices: hide,
		CustomerContactJSON: mustJSON(contact), ValidUntil: capped, SupplierHorizon: horizon,
	})
	if err != nil {
		if errors.Is(err, ErrQuoteNotFound) {
			return TenantQuote{}, refuse(http.StatusConflict, fmt.Sprintf(SentenceSentUpdate, quoteRef(row)))
		}
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	return s.tenantQuote(updated)
}

// quoteRef is the quote's reference in ported sentences: the frozen table
// carries no number column, so the id is the reference (recorded as a
// deviation and routed as a schema finding; no number is fabricated).
func quoteRef(row QuoteRow) string { return row.ID }

// Send is the tenant leg's modelled send operation (the design's modelled
// tenant surface). No route reaches it: the document that landed carries
// the send as a dealer desk action (ADR 0021, Decision 2), so this method
// is the seam a tenant-side document half would call if one ever lands,
// the RecordChangeRequest posture. The lifecycle itself is shared with the
// registered staff send through send below.
func (s *Service) Send(ctx context.Context, scope Scope, quoteID string) (SendResult, error) {
	fetch := func() (QuoteRow, error) {
		return s.store.GetQuoteForTenant(ctx, s.store.Pool(), scope.TenantID, scope.OrgID, quoteID)
	}
	return s.send(ctx, quoteID, fetch)
}

// StaffSend is the registered send, the dealer desk action the document
// carries (POST /api/v1/customer-quotes/{id}/send, x-role requireStaff;
// ADR 0021, Decision 2). The org fence is the caller's whole scope: the
// issuing contractor is recorded on the row's tenant_id, and the desk that
// operates the send holds a staff bearer with X-Org, never the
// contractor's own tenant bearer; beyond the fence the lifecycle is
// identical to the tenant leg's.
func (s *Service) StaffSend(ctx context.Context, orgID, quoteID string) (SendResult, error) {
	fetch := func() (QuoteRow, error) {
		return s.store.GetQuoteForOrg(ctx, s.store.Pool(), orgID, quoteID)
	}
	return s.send(ctx, quoteID, fetch)
}

// send is the one send lifecycle (R4, R6 and the rotation of DESIGN 2.4):
// the gates, the mint, the dead-list rotation with the live hash installed,
// the re-freeze of the presentation lines, the counter reset and the status
// flip to sent, all under the quote row lock on one transaction; then the
// send event handed to the delivery seam (D34) with the raw token, which
// appears in the returned result and that handoff, and nowhere else. The
// fetch closure is the leg's scoped read: the tenant predicate or the org
// fence.
func (s *Service) send(ctx context.Context, quoteID string, fetch func() (QuoteRow, error)) (SendResult, error) {
	row, err := fetch()
	if err != nil {
		if errors.Is(err, ErrQuoteNotFound) {
			return SendResult{}, refuse(http.StatusNotFound, SentenceQuoteGone)
		}
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if err := s.checkSendGates(row); err != nil {
		return SendResult{}, err
	}
	raw, hash, err := MintToken()
	if err != nil {
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	defer tx.Rollback(ctx)
	locked, ok, err := s.store.GetQuoteLockTx(ctx, tx, quoteID)
	if err != nil || !ok {
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if err := s.checkSendGates(locked); err != nil {
		return SendResult{}, err
	}
	state := TokenState{}
	if locked.ShareTokenHash != nil {
		if state, err = ParseTokenState(*locked.ShareTokenHash); err != nil {
			s.defects.UnparsableStoredValue()
			return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
		}
	}
	if locked.ValidUntil == nil {
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	// The link bound is the offer bound: the link never outlives the offer.
	if err := s.store.RotateOnSend(ctx, tx, quoteID, state.InstallLive(hash), locked.LinesJSON, *locked.ValidUntil, *locked.ValidUntil); err != nil {
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if err := tx.Commit(ctx); err != nil {
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	// The send event: the committed minted link handed to the delivery seam
	// once (D34: the core itself delivers nothing; the stub records and
	// returns, and the desk receives the raw token in this send's
	// response). After the commit on purpose, and best-effort by
	// construction: the send has already succeeded, so the handoff can
	// never misstate it.
	sentDoc, derr := decodeDoc(locked)
	if derr != nil {
		sentDoc.Contact = CustomerContact{}
	}
	_ = s.mail.SendQuoteLink(ctx, SentLink{
		QuoteID: locked.ID, OrgID: locked.OrgID, TenantID: locked.TenantID,
		CustomerName: sentDoc.Contact.Name, Token: raw, ValidUntil: locked.ValidUntil,
		SentAt: s.now(),
	})
	final, err := fetch()
	if err != nil {
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	view, err := s.tenantQuote(final)
	if err != nil {
		return SendResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	return SendResult{TenantQuote: view, MagicLinkToken: raw}, nil
}

// checkSendGates applies R6: the customer name must be present, an accepted
// quote refuses, unpriced lines refuse (R2), and an offer bound already
// passed refuses.
func (s *Service) checkSendGates(row QuoteRow) error {
	if row.Status == StatusAccepted {
		return refuse(http.StatusConflict, SentenceAlreadyAccepted)
	}
	doc, err := decodeDoc(row)
	if err != nil {
		return refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if strings.TrimSpace(doc.Contact.Name) == "" {
		return refuse(http.StatusBadRequest, SentenceNeedCustomerName)
	}
	if len(doc.Lines) == 0 {
		return refuse(http.StatusBadRequest, SentenceNoLines)
	}
	if names := UnpricedLineNames(doc.Lines, s.now()); len(names) > 0 {
		return refuse(http.StatusBadRequest, RefuseUnpriced(names))
	}
	if IsOfferExpired(row.ValidUntil, s.now()) {
		return refuse(http.StatusConflict, SentenceAlreadyExpiredSend)
	}
	return nil
}

// Revise pulls a sent quote back to draft and retires the live token
// immediately: the moment the intent to change forms, the old link shows a
// proposal the contractor no longer stands behind. An accepted quote refuses
// absolutely, so no rotation can follow an acceptance; a draft is a no-op.
func (s *Service) Revise(ctx context.Context, scope Scope, quoteID string) (TenantQuote, error) {
	row, err := s.store.GetQuoteForTenant(ctx, s.store.Pool(), scope.TenantID, scope.OrgID, quoteID)
	if err != nil {
		if errors.Is(err, ErrQuoteNotFound) {
			return TenantQuote{}, refuse(http.StatusNotFound, SentenceQuoteGone)
		}
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if row.Status == StatusAccepted {
		return TenantQuote{}, refuse(http.StatusConflict, SentenceAcceptedNoRevise)
	}
	if row.Status == StatusDraft {
		return s.tenantQuote(row)
	}
	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	defer tx.Rollback(ctx)
	locked, ok, err := s.store.GetQuoteLockTx(ctx, tx, quoteID)
	if err != nil || !ok {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if locked.Status == StatusAccepted {
		return TenantQuote{}, refuse(http.StatusConflict, SentenceAcceptedNoRevise)
	}
	if locked.Status == StatusDraft {
		if err := tx.Commit(ctx); err != nil {
			return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
		}
		return s.tenantQuote(row)
	}
	state := TokenState{}
	if locked.ShareTokenHash != nil {
		if state, err = ParseTokenState(*locked.ShareTokenHash); err != nil {
			s.defects.UnparsableStoredValue()
			return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
		}
	}
	if err := s.store.RetireOnRevise(ctx, tx, quoteID, state.RetireLive()); err != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	final, err := s.store.GetQuoteForTenant(ctx, s.store.Pool(), scope.TenantID, scope.OrgID, quoteID)
	if err != nil {
		return TenantQuote{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	return s.tenantQuote(final)
}
