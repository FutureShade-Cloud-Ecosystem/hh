// share.go, DIB-57 quote share links + customer acceptance (bundle W10-A).
//
// THE SECURITY MODEL (R4). A share link is a CREDENTIAL, not an authentication. Whoever holds the URL
// is treated as the customer, because that is the only thing a link can prove. Everything else follows
// from taking that seriously:
//
//   - the token grants read + accept of EXACTLY ONE quote, and nothing else
//   - it EXPIRES (per-link, stored on the row, not a global constant, so a link's life is a property
//     of the link and can differ per customer)
//   - it is REVOCABLE
//   - the public payload carries NO staff-only field, margin, cost, other customers, other quotes
//   - every view and every accept is logged against the token
//   - a dead link answers 410 GONE, not 404: the customer should learn the link died, not be told the
//     quote never existed. 404 is reserved for a token that never existed at all.
//
// Only the sha256 hash is stored. The raw token is returned exactly once, at mint time, to the staff
// member who created it, this mirrors portal.newInviteToken and identity.newResetToken, the two
// existing token primitives in this codebase.
//
// THE ACCEPTANCE (ruling, 2026-07-30, supersedes the brief's R5 on this item). Acceptance is a typed
// full name plus an explicit affirmative, presented honestly as an electronic attestation. It does NOT
// reuse pos-signature.ts: that component is a stub with no canvas, no ink and no image, it flips a
// boolean on pointerdown and the server seals the literal string "SIMULATED:<purpose>:<ref>". Building
// on it would have produced an artifact implying more than it captured. The POS-side stub is filed
// separately as DIB-386 and is NOT touched here.
package quote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// defaultShareTTL is the fallback link lifetime (R4). It is a DEFAULT, not a policy: the value is
// persisted per link, so changing this constant never retroactively extends or kills a live link.
const defaultShareTTL = 30 * 24 * time.Hour

// maxTypedName bounds the attested name. Long enough for any real name; short enough that the column
// cannot be used as free-text storage.
const maxTypedName = 120

var (
	// ErrLinkGone, the token exists but is revoked or past its expiry. Mapped to 410 GONE, so the
	// holder learns the LINK died rather than being told the quote never existed.
	ErrLinkGone = errors.New("quote: share link is no longer valid")
	// ErrLinkNotFound, no such token, ever. Mapped to 404.
	ErrLinkNotFound = errors.New("quote: share link not found")
	// ErrNotAcceptable, the quote is not in a state where a customer may accept it. Mapped to 409.
	ErrNotAcceptable = errors.New("quote: this quote is not awaiting acceptance")
	// ErrEmptyQuote, DIB-395: the first mint (DRAFT -> SENT) of a quote with no lines. Mapped to 409.
	// The message says what to DO, not just that it refused, because the person hitting this is a staff
	// member mid-task who needs to know the next action.
	ErrEmptyQuote = errors.New("quote: add at least one line before sending this quote")
)

// - the token primitive -

// newShareToken returns a random raw token (shown once to the staff member who minted it) and its
// sha256 hex hash (stored). Only the hash ever touches the DB.
//
// 32 bytes of crypto/rand is 256 bits of entropy, guessing a live token is not a threat model anyone
// needs to rate-limit against, which is why no throttle is added here. Mirrors portal.newInviteToken.
func newShareToken() (raw, hashed string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashShareToken(raw), nil
}

func hashShareToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// - models -

// ShareLink is one issued link as STAFF sees it. It deliberately carries no token: the raw value is
// unrecoverable after minting, and the hash is not something a UI should ever handle.
type ShareLink struct {
	ID        string     `json:"id"`
	QuoteID   string     `json:"quote_id"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// Live is the computed "is this link usable right now", not stored, so a link is never wrong
	// about itself after a clock tick.
	Live bool `json:"live"`
}

// MintedShareLink is the mint response. Token is present EXACTLY here and nowhere else, ever.
type MintedShareLink struct {
	ShareLink
	Token string `json:"token"`
}

// PublicQuoteView is what an anonymous link holder sees.
//
// This is a hand-built allowlist, NOT a projection of the staff model. That is the whole point: a
// struct built by naming what may be shown cannot leak a field someone adds upstream later, whereas a
// struct built by removing fields leaks by default the moment the source grows. Anything not listed
// here is invisible to the customer, cost, margin, branch internals, created_by/updated_by, metadata,
// the editable-field offer, and every other quote or customer in the system.
type PublicQuoteView struct {
	QuoteNo       string            `json:"quote_no"`
	CustomerName  string            `json:"customer_name"`
	Status        string            `json:"status"`
	ValidUntil    string            `json:"valid_until,omitempty"`
	Expired       bool              `json:"expired"`
	Lines         []PublicQuoteLine `json:"lines"`
	SubtotalCents int64             `json:"subtotal_cents"`
	TaxCents      int64             `json:"tax_cents"`
	TotalCents    int64             `json:"total_cents"`
	// AsOf is the quote's updated_at. The public page states "as of <AsOf>" so a customer looking at a
	// link they opened yesterday knows whether they are reading current numbers (C2). Links are NOT
	// auto-revoked when a quote is edited, an edit is normal, and killing the customer's link every
	// time staff adjust a line would make the feature unusable, so saying WHEN is how the page stays
	// honest instead.
	AsOf time.Time `json:"as_of"`
	// Acceptable is whether this link may still be used to accept: the quote is SENT, not expired, and
	// the link is live. Drives whether the accept form renders at all.
	Acceptable bool `json:"acceptable"`

	// - DIB-506: the terms the work area put on the quote, customer-visible by design -
	// All omitempty: a quote whose terms were never touched serves the exact pre-0173 payload.
	CustomerNotes    string `json:"customer_notes,omitempty"`
	DepositRequired  bool   `json:"deposit_required,omitempty"`
	DepositPct       int    `json:"deposit_pct,omitempty"`
	DepositCents     int64  `json:"deposit_cents,omitempty"`
	IncludeImages    bool   `json:"include_images,omitempty"`
	Fulfillment      string `json:"fulfillment,omitempty"`
	DeliveryAddress  string `json:"delivery_address,omitempty"`
	RequestedDate    string `json:"requested_date,omitempty"`
	DeliveryFeeCents int64  `json:"delivery_fee_cents,omitempty"`
}

// PublicQuoteLine is the customer-visible shape of a line. Note what is absent: no product_id (an
// internal identifier), no qty_base, no price_uom mechanics, the customer sees what they are buying,
// how much, and what it costs.
type PublicQuoteLine struct {
	LineNo         int    `json:"line_no"`
	Description    string `json:"description"`
	QtyEntered     string `json:"qty_entered"`
	UOMEntered     string `json:"uom_entered"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
	// DIB-506: the discount facts + the thumbnail reference. DiscPct/gross serve the customer's
	// "you saved" line. ImageRef is the ONE deliberate exception to the "no product_id" rule above:
	// it is populated ONLY when the dealer's include-product-images choice is ON and the product
	// has a primary image, and it exists solely to address the already-public image byte route
	// (GET /api/v1/products/{id}/image, an <img> can send no Authorization header). A quote whose
	// dealer keeps images off serves the exact pre-0173 line shape.
	DiscPct               int    `json:"disc_pct,omitempty"`
	PreDiscountTotalCents int64  `json:"pre_discount_total_cents,omitempty"`
	ImageRef              string `json:"image_ref,omitempty"`
}

// AcceptanceResult is what the customer gets back after accepting. It echoes the totals deliberately
// (C2): the confirmation restates exactly what was agreed to, so the customer's own record of the
// transaction does not depend on them having read the page carefully before clicking.
type AcceptanceResult struct {
	QuoteNo       string    `json:"quote_no"`
	TypedName     string    `json:"typed_name"`
	AcceptedAt    time.Time `json:"accepted_at"`
	SubtotalCents int64     `json:"subtotal_cents"`
	TaxCents      int64     `json:"tax_cents"`
	TotalCents    int64     `json:"total_cents"`
	// Repeat is DIB-491's honesty flag: true when this acceptance is a REPEAT of one already recorded
	// (multi-accept ON). The confirmation page can then say so, per C2, a customer who accepts twice
	// deserves a page that admits it was twice, not one that pretends this was the first time.
	Repeat bool `json:"repeat_acceptance,omitempty"`
	// priorStatus is the state the quote was in when this acceptance landed (SENT for a first accept;
	// ACCEPTED/CONVERTED for a repeat). Unexported: it exists for the audit hook, which must record
	// the true before→after rather than hardcoding SENT→ACCEPTED, and it never crosses the wire.
	priorStatus string
	// idemKey is the Idempotency-Key this acceptance carried, for the audit hook only (QD-3d). Unexported,
	// it never crosses the wire.
	idemKey string
}

// shareStore is the persistence the share surface needs.
type shareStore interface {
	CreateShareLink(ctx context.Context, quoteID, tokenHash string, expiresAt time.Time, createdBy string) (ShareLink, error)
	ListShareLinks(ctx context.Context, quoteID string) ([]ShareLink, error)
	RevokeShareLink(ctx context.Context, quoteID, linkID string, at time.Time) error
	// ResolveShareToken returns the link row for a token hash, WITHOUT judging liveness, the caller
	// distinguishes "never existed" (404) from "died" (410), which requires seeing the dead row.
	ResolveShareToken(ctx context.Context, tokenHash string) (link ShareLink, quoteID string, err error)
	PublicQuote(ctx context.Context, quoteID string) (PublicQuoteView, error)
	LogShareEvent(ctx context.Context, shareLinkID, event string) error
	// AcceptQuoteGuarded flips SENT -> ACCEPTED under the quote row lock, writes the acceptance record
	// and the audit row on the SAME transaction, and returns the agreed money.
	AcceptQuoteGuarded(ctx context.Context, in acceptWrite, hooks acceptHooks) (AcceptanceResult, error)
	// MarkSent moves DRAFT -> SENT under the lock. Idempotent for an already-SENT quote.
	MarkSent(ctx context.Context, quoteID, actorID string, hooks quoteDateHooks) error
}

// acceptWrite is the acceptance payload.
type acceptWrite struct {
	QuoteID     string
	ShareLinkID string
	TypedName   string
	Now         time.Time
	// IdempotencyKey is the optional, already validated and lowercased Idempotency-Key. Empty means
	// the caller sent none and the acceptance behaves exactly as it did before the key existed.
	IdempotencyKey string
}

// acceptHooks folds the audit row into the acceptance transaction, exactly as the edit hooks do.
type acceptHooks struct {
	// record runs after the status flip and before commit, on the SAME tx, and returns the audit_log
	// seq it wrote so the acceptance record can anchor itself in the change trail. A non-nil error
	// rolls the whole acceptance back.
	record func(ctx context.Context, tx pgx.Tx, res AcceptanceResult) (int64, error)
}

// - staff: mint / list / revoke -

// MintShareLink issues a share link for a quote and returns the raw token ONCE.
//
// Minting also moves a DRAFT quote to SENT (D4). That edge had no implementation anywhere, nothing in
// the codebase could move a quote out of DRAFT, and "sent" and "shared" are the same act in this
// workflow: the moment a customer can see it, it has been sent.
func (s *StaffService) MintShareLink(ctx context.Context, quoteID string, ttl time.Duration) (MintedShareLink, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return MintedShareLink{}, err
	}
	if caller.Status != identity.StatusActive {
		return MintedShareLink{}, ErrForbidden
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return MintedShareLink{}, err
	}
	status, branchID, validUntil, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return MintedShareLink{}, err
	}
	if !caller.CanAccessBranch(branchID) {
		return MintedShareLink{}, ErrForbidden
	}
	// Only a quote that is still being offered may be shared. Sharing an ACCEPTED/CONVERTED/LOST quote
	// would invite an acceptance that can never be honoured, UNLESS (DIB-491) the dealer has switched
	// multi-accept ON, in which case a repeat acceptance CAN be honoured and refusing the link would
	// make the switch unusable the day the original link expires. LOST stays refused either way: a
	// lost quote is a closed conversation, not an accepted one.
	if status != "DRAFT" && status != "SENT" {
		reAcceptable := (status == "ACCEPTED" || status == "CONVERTED") && s.multiAcceptOn(ctx)
		if !reAcceptable {
			return MintedShareLink{}, &NotEditableError{Field: "share_link", Status: status}
		}
	}
	if err := assertNotExpired(validUntil, s.now()); err != nil {
		return MintedShareLink{}, err
	}
	if ttl <= 0 {
		ttl = defaultShareTTL
	}
	raw, hashed, err := newShareToken()
	if err != nil {
		return MintedShareLink{}, fmt.Errorf("mint share token: %w", err)
	}
	if status == "DRAFT" {
		if err := s.shares.MarkSent(ctx, quoteID, caller.UserID, quoteDateHooks{
			record: func(ctx context.Context, tx pgx.Tx, before, after string) error {
				return s.recordFieldChange(ctx, tx, caller.UserID, quoteID, 0, "status", before, after)
			},
		}); err != nil {
			return MintedShareLink{}, err
		}
	}
	link, err := s.shares.CreateShareLink(ctx, quoteID, hashed, s.now().Add(ttl), caller.UserID)
	if err != nil {
		return MintedShareLink{}, err
	}
	link.Live = true

	// DIB-482 (M3-G3): sharing a quote starts its follow-up cadence. This is the natural moment and
	// the one the Follow-ups panel already promises, the customer now has the document, and the
	// clock the salesperson thinks in ("I sent it Tuesday") starts here. Until this line the ONLY
	// path that started a cadence was ShareBySMS, so the ordinary way of sending a quote, mint a
	// link, paste it into an email, produced a quote nobody ever followed up.
	//
	// AFTER the link exists, not before: a cadence for a quote whose share failed to mint would be a
	// reminder about a document the customer never received. See startCadenceOnShare for why a
	// failure here does not fail the mint.
	s.startCadenceOnShare(ctx, quoteID, s.now())

	return MintedShareLink{ShareLink: link, Token: raw}, nil
}

// ListShareLinks returns the links issued for a quote. No tokens, they are unrecoverable by design.
func (s *StaffService) ListShareLinks(ctx context.Context, quoteID string) ([]ShareLink, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return nil, err
	}
	_, branchID, _, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	if !caller.CanAccessBranch(branchID) {
		return nil, ErrForbidden
	}
	links, err := s.shares.ListShareLinks(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	for i := range links {
		links[i].Live = links[i].RevokedAt == nil && now.Before(links[i].ExpiresAt)
	}
	return links, nil
}

// RevokeShareLink kills a link immediately. Subsequent use answers 410, not 404.
func (s *StaffService) RevokeShareLink(ctx context.Context, quoteID, linkID string) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	if caller.Status != identity.StatusActive {
		return ErrForbidden
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return err
	}
	_, branchID, _, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return err
	}
	if !caller.CanAccessBranch(branchID) {
		return ErrForbidden
	}
	return s.shares.RevokeShareLink(ctx, quoteID, linkID, s.now())
}

// - public: read / accept -

// resolveLive resolves a raw token to its link, distinguishing "never existed" from "died".
func (s *StaffService) resolveLive(ctx context.Context, rawToken string) (ShareLink, string, error) {
	tok := strings.TrimSpace(rawToken)
	if tok == "" {
		return ShareLink{}, "", ErrLinkNotFound
	}
	link, quoteID, err := s.shares.ResolveShareToken(ctx, hashShareToken(tok))
	if err != nil {
		return ShareLink{}, "", err // ErrLinkNotFound, the token never existed
	}
	if link.RevokedAt != nil || !s.now().Before(link.ExpiresAt) {
		return ShareLink{}, "", ErrLinkGone // it existed and died, 410, not 404
	}
	link.Live = true
	return link, quoteID, nil
}

// PublicQuoteByToken serves the customer-facing quote view for a live token, and logs the view.
func (s *StaffService) PublicQuoteByToken(ctx context.Context, rawToken string) (PublicQuoteView, error) {
	link, quoteID, err := s.resolveLive(ctx, rawToken)
	if err != nil {
		return PublicQuoteView{}, err
	}
	view, err := s.shares.PublicQuote(ctx, quoteID)
	if err != nil {
		return PublicQuoteView{}, err
	}
	view.Expired = rowExpired(view.ValidUntil, s.now())
	// Acceptable only when the quote is genuinely awaiting acceptance and still inside its window -
	// OR (DIB-491) when the dealer allows repeat acceptance and the quote has already been accepted.
	// This flag is DISPLAY: it decides whether the accept form renders. The enforcement is
	// AcceptQuoteGuarded, which re-reads the switch inside its own transaction; a stale offer here is
	// refused there with the order named, which is the ordinary fate of every optimistic offer.
	view.Acceptable = view.Status == "SENT" && !view.Expired
	if !view.Acceptable && (view.Status == "ACCEPTED" || view.Status == "CONVERTED") && !view.Expired {
		view.Acceptable = s.multiAcceptOn(ctx)
	}
	// R4: log every view against the token. Best-effort, a logging failure must not deny a customer
	// the quote they were legitimately sent.
	_ = s.shares.LogShareEvent(ctx, link.ID, "VIEW")
	return view, nil
}

// AcceptByToken records a customer's acceptance and flips the quote to ACCEPTED.
//
// The status flip, the acceptance record and the audit row all land on ONE transaction under the quote
// row lock (R3), so an acceptance is exactly as reconstructable as a staff edit, and an acceptance
// can never exist without the trail that explains it, or vice versa.
func (s *StaffService) AcceptByToken(ctx context.Context, rawToken, typedName string, affirmed bool) (AcceptanceResult, error) {
	return s.AcceptByTokenKeyed(ctx, rawToken, typedName, affirmed, "")
}

// AcceptByTokenKeyed is AcceptByToken carrying the optional Idempotency-Key (see share_idempotency.go).
func (s *StaffService) AcceptByTokenKeyed(ctx context.Context, rawToken, typedName string, affirmed bool, idemKey string) (AcceptanceResult, error) {
	idemKey, kerr := normalizeIdempotencyKey(idemKey)
	if kerr != nil {
		return AcceptanceResult{}, kerr
	}
	link, quoteID, err := s.resolveLive(ctx, rawToken)
	if err != nil {
		return AcceptanceResult{}, err
	}
	name := strings.TrimSpace(typedName)
	if name == "" {
		return AcceptanceResult{}, httpx.BadRequestf("enter your full name to accept this quote")
	}
	if len(name) > maxTypedName {
		return AcceptanceResult{}, httpx.BadRequestf("name is too long")
	}
	// The affirmative is a REQUIRED separate act, not an implied consequence of submitting the form.
	// An attestation that someone could produce by accident is not worth recording.
	if !affirmed {
		return AcceptanceResult{}, httpx.BadRequestf("you must confirm that you accept this quote")
	}
	res, err := s.shares.AcceptQuoteGuarded(ctx, acceptWrite{
		QuoteID: quoteID, ShareLinkID: link.ID, TypedName: name, Now: s.now(),
		IdempotencyKey: idemKey,
	}, acceptHooks{
		record: func(ctx context.Context, tx pgx.Tx, r AcceptanceResult) (int64, error) {
			return s.recordAcceptance(ctx, tx, quoteID, link.ID, r)
		},
	})
	if err != nil {
		return AcceptanceResult{}, err
	}
	_ = s.shares.LogShareEvent(ctx, link.ID, "ACCEPT")
	return res, nil
}

// recordAcceptance writes the QUOTE_ACCEPTED audit row on the acceptance's own transaction and returns
// its seq, which the acceptance record stores as its anchor in the change trail.
//
// The actor is the LINK, not a staff user: an acceptance has no authenticated principal, and stamping a
// staff id here would misattribute a customer's decision to whoever happened to mint the link.
func (s *StaffService) recordAcceptance(ctx context.Context, tx pgx.Tx, quoteID, linkID string, r AcceptanceResult) (int64, error) {
	if s.auditTx == nil {
		if r.idemKey != "" {
			// A keyed acceptance with no trail to carry the key could not be replayed safely.
			return 0, errors.New("quote-acceptance audit: an idempotency key needs the audit trail")
		}
		return 0, nil
	}
	// DIB-491: the before→after pair is the TRUE transition, not a hardcoded SENT→ACCEPTED. A repeat
	// acceptance (multi-accept ON) moves nothing, before and after are both the standing status -
	// and a trail that claimed SENT→ACCEPTED for it would invent a transition that never ran.
	before, after := r.priorStatus, "ACCEPTED"
	if before == "" {
		before = "SENT"
	}
	if r.Repeat {
		after = before
	}
	extra := map[string]any{
		"field":  "status",
		"before": before,
		"after":  after,
		// amount_cents and ref_type are already on the audit allowlist; the typed name is NOT
		// recorded here, it lives on quote_acceptances, which is the artifact built to hold it.
		"amount_cents": r.TotalCents,
		"ref_type":     "quote_share_link",
	}
	if r.idemKey != "" {
		// QD-3d: the Idempotency-Key rides the allowlisted client_id (share_idempotency.go).
		extra["client_id"] = r.idemKey
	}
	err := s.auditTx.RecordTx(ctx, tx, audit.SensitiveAccess{
		Action: audit.ActQuoteAccepted, EntityType: "quote", EntityID: quoteID,
		Purpose: "CUSTOMER_QUOTE_ACCEPTANCE", Reason: "status",
		Extra: extra,
	})
	if err != nil {
		return 0, fmt.Errorf("quote-acceptance audit: %w", err)
	}
	// The seq is read back inside the same tx so the acceptance row can anchor to it.
	var seq int64
	if serr := tx.QueryRow(ctx,
		`SELECT seq FROM audit_log WHERE entity_type='quote' AND entity_id=$1 AND action=$2
		  ORDER BY seq DESC LIMIT 1`, quoteID, audit.ActQuoteAccepted).Scan(&seq); serr != nil {
		if r.idemKey != "" {
			// The key is found through the anchor, so a keyed acceptance without one is refused.
			return 0, fmt.Errorf("quote-acceptance audit anchor: %w", serr)
		}
		// A missing seq is not grounds to refuse an acceptance the customer legitimately made, the
		// audit row itself is already written and is the authoritative trail. Anchor degrades to 0.
		return 0, nil
	}
	return seq, nil
}
