package customerquote

// The public leg: the view and the accept, where the opaque token in the
// path is the whole credential (D23: the homeowner role is carried by the
// magic-link token, not by an account). No Authorization header is read, no
// cookie is read, no principal exists downstream, no org or tenant context
// is stamped, and X-Org, if sent, is an undeclared header with zero effect
// (ADR-0020's public leg). The payload is a hand-built allowlist, never a
// projection of the tenant model, so no field anyone adds upstream can leak
// by default.

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// PublicLine is the customer-visible presentation line (R9: selections carry
// imagery and narrative, commodities carry a bare line). The per-line sell
// price is presentation and renders zero-valued when the contractor hides
// line prices; the document's money is the totals block.
type PublicLine struct {
	Name           string `json:"name"`
	Qty            string `json:"qty"`
	UOM            string `json:"uom"`
	Presentation   string `json:"presentation"`
	SKU            string `json:"sku,omitempty"`
	Description    string `json:"description,omitempty"`
	ImageURL       string `json:"image_url,omitempty"`
	Note           string `json:"note,omitempty"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
}

// PublicTotals restates the document money as the core computed it. The
// contractor's cost and margin never appear (the sell-side rule).
type PublicTotals struct {
	MaterialsCents int64 `json:"materials_cents"`
	LabourCents    int64 `json:"labour_cents"`
	OverheadCents  int64 `json:"overhead_cents"`
	GrandCents     int64 `json:"grand_cents"`
}

// PublicBrand carries the contractor's brand fields (D12, R11). The frozen
// schema has no tenant_brand storage (finding F1, routed to HH-3.7), so the
// fields render absent: an absent brand is a visible absence, never a
// dealer-branded default.
type PublicBrand struct {
	DisplayName   string `json:"display_name"`
	PrimaryColour string `json:"primary_colour"`
	FooterText    string `json:"footer_text"`
	LogoRef       string `json:"logo_ref"`
}

// PublicView is the allowlist payload.
type PublicView struct {
	QuoteID      string       `json:"quote_id"`
	Status       Status       `json:"status"`
	CustomerName string       `json:"customer_name"`
	ValidUntil   *time.Time   `json:"valid_until,omitempty"`
	Expired      bool         `json:"expired"`
	Lines        []PublicLine `json:"lines"`
	Totals       PublicTotals `json:"totals"`
	AsOf         time.Time    `json:"as_of"`
	Acceptable   bool         `json:"acceptable"`
	ConsentText  string       `json:"consent_text"`
	Brand        PublicBrand  `json:"brand"`
}

// AcceptRequest is the accept payload: a typed name and an explicit
// affirmative, two required acts.
type AcceptRequest struct {
	TypedName string `json:"typed_name"`
	Accepted  bool   `json:"accepted"`
}

// AcceptanceResult restates the agreed totals so the customer's own record
// of the transaction does not depend on a careful reading of the page.
type AcceptanceResult struct {
	AcceptanceID   string    `json:"acceptance_id"`
	QuoteID        string    `json:"quote_id"`
	TypedName      string    `json:"typed_name"`
	AcceptedAt     time.Time `json:"accepted_at"`
	MaterialsCents int64     `json:"materials_cents"`
	LabourCents    int64     `json:"labour_cents"`
	OverheadCents  int64     `json:"overhead_cents"`
	TotalCents     int64     `json:"total_cents"`
}

// PublicView serves the allowlist payload for a live token and tracks the
// view best-effort: the counters advance, sent advances to viewed only, and
// a logging failure never denies the read (the carried rule).
func (s *Service) PublicView(ctx context.Context, rawToken string) (PublicView, error) {
	row, class, err := s.resolveToken(ctx, rawToken)
	if err != nil {
		return PublicView{}, err
	}
	switch class {
	case tokenMalformed, tokenUnknown:
		return PublicView{}, refuse(http.StatusNotFound, MsgLinkNotExist)
	case tokenDead:
		return PublicView{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	doc, derr := decodeDoc(row)
	if derr != nil {
		return PublicView{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	totals, terr := ComputeTotals(doc.Lines, row.MarkupBps, doc.Labour, doc.Overhead)
	if terr != nil {
		return PublicView{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	s.store.TrackView(ctx, row.ID)
	status := row.Status
	if status == StatusSent {
		status = StatusViewed
	}
	lines := make([]PublicLine, 0, len(doc.Lines))
	for _, l := range doc.Lines {
		pl := PublicLine{
			Name: l.Name, Qty: l.Qty, UOM: l.UOM, Presentation: l.Presentation,
			SKU: l.SKU, Description: l.Description, ImageURL: l.ImageURL, Note: l.Note,
		}
		if !row.HideLinePrices {
			pl.UnitPriceCents = LineSellUnitCents(l, row.MarkupBps)
			lineTotal, lerr := MultiplyCents(pl.UnitPriceCents, l.Qty)
			if lerr == nil {
				pl.LineTotalCents = lineTotal
			}
		}
		lines = append(lines, pl)
	}
	return PublicView{
		QuoteID:      row.ID,
		Status:       status,
		CustomerName: doc.Contact.Name,
		ValidUntil:   row.ValidUntil,
		Expired:      IsOfferExpired(row.ValidUntil, s.now()),
		Lines:        lines,
		Totals: PublicTotals{
			MaterialsCents: totals.MaterialsCents, LabourCents: totals.LabourCents,
			OverheadCents: totals.OverheadCents, GrandCents: totals.GrandCents,
		},
		AsOf:        row.UpdatedAt,
		Acceptable:  CanRespond(status, row.ValidUntil, s.now()),
		ConsentText: ConsentText,
		Brand:       PublicBrand{},
	}, nil
}

// PublicAccept records a customer's acceptance with no account, no principal
// and no identity write (D23). Validation, then one transaction under the
// quote row lock: the state re-check, the acceptance row, the status flip,
// and the mandatory audit row carrying the signed artifact; any failure
// rolls the whole acceptance back, so the flip, the row and the trail are
// all present or all absent.
func (s *Service) PublicAccept(ctx context.Context, rawToken string, req AcceptRequest) (AcceptanceResult, error) {
	row, class, rerr := s.resolveToken(ctx, rawToken)
	if rerr != nil {
		return AcceptanceResult{}, rerr
	}
	switch class {
	case tokenMalformed, tokenUnknown:
		return AcceptanceResult{}, refuse(http.StatusNotFound, MsgLinkNotExist)
	case tokenDead:
		return AcceptanceResult{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	name := strings.TrimSpace(req.TypedName)
	if utf8.RuneCountInString(name) < MinTypedName {
		return AcceptanceResult{}, refuse(http.StatusBadRequest, SentenceNeedFullName)
	}
	if !ValidTypedName(name) {
		return AcceptanceResult{}, refuse(http.StatusBadRequest, SentenceNameTooLong)
	}
	// The affirmative is a required separate act, not an implied consequence
	// of submitting: an attestation someone could produce by accident is not
	// worth recording.
	if !req.Accepted {
		return AcceptanceResult{}, refuse(http.StatusBadRequest, SentenceAffirmativeRequired)
	}
	if serr := s.checkAcceptState(row); serr != nil {
		return AcceptanceResult{}, serr
	}

	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	defer tx.Rollback(ctx)
	locked, ok, lerr := s.store.GetQuoteLockTx(ctx, tx, row.ID)
	if lerr != nil || !ok {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	// Under the lock, the token must still be live: a revise or re-send that
	// raced the pre-lock checks retires it here, and the holder learns the
	// link died rather than being told the quote never existed.
	if locked.ShareTokenHash == nil {
		return AcceptanceResult{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	state, perr := ParseTokenState(*locked.ShareTokenHash)
	if perr != nil {
		s.defects.UnparsableStoredValue()
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if !state.IsLive(HashToken(strings.TrimSpace(rawToken))) || locked.DeletedAt != nil {
		return AcceptanceResult{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	if locked.ShareExpiresAt == nil || !s.now().Before(*locked.ShareExpiresAt) {
		return AcceptanceResult{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	if serr := s.checkAcceptState(locked); serr != nil {
		return AcceptanceResult{}, serr
	}
	doc, derr := decodeDoc(locked)
	if derr != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	// The accepted total is computed by the core from the frozen lines,
	// never taken from the request.
	totals, terr := ComputeTotals(doc.Lines, locked.MarkupBps, doc.Labour, doc.Overhead)
	if terr != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	acceptedAt := s.now()
	// The seq allocation rides this transaction (DESIGN 5.6, the D30 and
	// C34 lineage): the acceptance row draws under the quote_acceptance
	// scope, the status flip's fresh value under the customer_quote scope,
	// both through the scope_sequences upsert the 0213 convention records,
	// on the acceptance transaction so they commit or roll back with it.
	acceptanceSeq, serr := s.store.AllocateSeqTx(ctx, tx, ScopeQuoteAcceptance)
	if serr != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	acceptance, aerr := s.store.InsertAcceptanceTx(ctx, tx, locked, name, totals.GrandCents, acceptedAt, acceptanceSeq)
	if aerr != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	quoteSeq, qerr := s.store.AllocateSeqTx(ctx, tx, ScopeCustomerQuote)
	if qerr != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if ferr := s.store.FlipAcceptedTx(ctx, tx, locked.ID, quoteSeq); ferr != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	// The signed artifact, over the row as written.
	fields := ArtifactFields{
		AcceptanceID: acceptance.ID, QuoteID: acceptance.CustomerQuoteID,
		OrgID: acceptance.OrgID, TenantID: acceptance.TenantID,
		SignedName: acceptance.SignedName, Affirmative: acceptance.Affirmative,
		ConsentText: acceptance.ConsentText, AcceptedTotalCents: acceptance.AcceptedTotalCents,
		AcceptedAt: acceptance.AcceptedAt,
	}
	if acceptance.SignatureAttachmentID != nil {
		fields.SignatureAttachmentID = *acceptance.SignatureAttachmentID
	}
	hashHex, sigHex, derr2 := ArtifactDigest(s.keys.Key, fields)
	if derr2 != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	// The mandatory audit row, on this transaction; its failure rolls the
	// acceptance back (the deliberate divergence from the degrading dealer
	// precedent, ADR-0022's mandatory-write pin).
	auditRow := EncodeAuditRow(AcceptanceAuditRow{
		AcceptanceID: acceptance.ID, QuoteID: locked.ID, OrgID: locked.OrgID, TenantID: locked.TenantID,
		AcceptedTotalCents: totals.GrandCents, ArtifactHash: hashHex, ArtifactSig: sigHex,
		Alg: ArtifactAlg, KeyID: s.keys.KeyID, TokenHash: HashToken(strings.TrimSpace(rawToken)),
	})
	if rerr := s.seam.RecordAcceptanceTx(ctx, tx, auditRow); rerr != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if cerr := tx.Commit(ctx); cerr != nil {
		return AcceptanceResult{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	return AcceptanceResult{
		AcceptanceID: acceptance.ID, QuoteID: locked.ID, TypedName: acceptance.SignedName,
		AcceptedAt: acceptance.AcceptedAt, MaterialsCents: totals.MaterialsCents,
		LabourCents: totals.LabourCents, OverheadCents: totals.OverheadCents,
		TotalCents: totals.GrandCents,
	}, nil
}

// checkAcceptState applies the 409 family: the link is live but the quote is
// not awaiting acceptance, and the body names why.
func (s *Service) checkAcceptState(row QuoteRow) *Error {
	if IsOfferExpired(row.ValidUntil, s.now()) {
		return refuse(http.StatusConflict, SentenceOfferExpired)
	}
	switch row.Status {
	case StatusAccepted, StatusDeclined:
		return refuse(http.StatusConflict, SentenceAlreadyResponded)
	case StatusDraft:
		return refuse(http.StatusConflict, MsgNotAwaiting)
	}
	return nil
}

// RecordChangeRequest records a homeowner's change request (R8): a live
// offer can be asked-for-changes, the request appends to the column, the
// status moves to changes-requested, and the offer remains respondable. No
// route is registered for this method in this batch: the public route
// spelling is the OpenAPI document's to author (HH-3.4's half), and this
// method is the seam the route will call.
func (s *Service) RecordChangeRequest(ctx context.Context, rawToken, message string) (PublicView, error) {
	row, class, err := s.resolveToken(ctx, rawToken)
	if err != nil {
		return PublicView{}, err
	}
	switch class {
	case tokenMalformed, tokenUnknown:
		return PublicView{}, refuse(http.StatusNotFound, MsgLinkNotExist)
	case tokenDead:
		return PublicView{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	msg := strings.TrimSpace(message)
	if !CanRespond(row.Status, row.ValidUntil, s.now()) {
		return PublicView{}, refuse(http.StatusConflict, SentenceChangesNotOpen)
	}
	if msg == "" {
		return PublicView{}, refuse(http.StatusBadRequest, SentenceNeedChangesMessage)
	}
	tx, err := s.store.Pool().Begin(ctx)
	if err != nil {
		return PublicView{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	defer tx.Rollback(ctx)
	locked, ok, lerr := s.store.GetQuoteLockTx(ctx, tx, row.ID)
	if lerr != nil || !ok {
		return PublicView{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	// Under the lock, the token must still be live: the mirror of
	// PublicAccept's guard above (the re-review's N1). The pre-lock resolve
	// classified a snapshot, and a rotation that commits inside the
	// resolve-to-lock window leaves the offer respondable while the
	// presented generation sits in the dead list; without this block the
	// append below would write through a dead generation, the write being
	// answered 410 by the post-commit re-resolve after the message had
	// already landed. Null live slot, a stored value that fails the grammar
	// (loudly counted, 503), a dead presented hash, a tombstone and a passed
	// or absent link bound each refuse here, 410 with the dead-link sentence.
	if locked.ShareTokenHash == nil {
		return PublicView{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	state, perr := ParseTokenState(*locked.ShareTokenHash)
	if perr != nil {
		s.defects.UnparsableStoredValue()
		return PublicView{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if !state.IsLive(HashToken(strings.TrimSpace(rawToken))) || locked.DeletedAt != nil {
		return PublicView{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	if locked.ShareExpiresAt == nil || !s.now().Before(*locked.ShareExpiresAt) {
		return PublicView{}, refuse(http.StatusGone, SentenceLinkNoLongerValid)
	}
	if !CanRespond(locked.Status, locked.ValidUntil, s.now()) {
		return PublicView{}, refuse(http.StatusConflict, SentenceChangesNotOpen)
	}
	// The append base is the LOCKED row, never the pre-lock read. Two
	// concurrent change requests that both built their append from the
	// pre-lock column both answered 200 while the later commit silently
	// dropped the earlier customer's message (the review's R1, demonstrated
	// live): reading change_requests under the row lock and appending to
	// that is the only base that cannot overwrite a concurrent append
	// blind, because the lock serializes every writer on this row.
	lockedDoc, derr := decodeDoc(locked)
	if derr != nil {
		return PublicView{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	base := lockedDoc.ChangeRequests
	if base == nil {
		base = []ChangeRequest{}
	}
	appended := append(base, ChangeRequest{ID: newChangeID(), Message: msg, CreatedAt: s.now()})
	if err := s.store.AppendChangeRequestTx(ctx, tx, locked.ID, mustJSON(appended)); err != nil {
		return PublicView{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	if cerr := tx.Commit(ctx); cerr != nil {
		return PublicView{}, refuse(http.StatusServiceUnavailable, MsgBusy)
	}
	// One honest residual, noted and not fixed: after a legitimate append
	// commits, a rotation committing before this re-resolve still answers
	// 410 with the message stored. That is the ordinary read-after-write
	// race; the under-lock guard above removes only the
	// write-through-a-dead-generation ordering, the demonstrated N1 defect.
	return s.PublicView(ctx, rawToken)
}

// VerificationResult names whether an acceptance's artifact verifies, and
// which class of break failed.
type VerificationResult struct {
	OK     bool
	Reason string
}

// VerifyAcceptance re-computes an acceptance's artifact from the stored row
// and checks it against the audit row (ADR-0022 decision 2's verification
// path): rebuild the canonical bytes strictly from the row's columns,
// compare the hash by equality, recompute the HMAC with the key the recorded
// key id names and compare in constant time. Any mismatch, a missing or
// ambiguous audit row (zero or multiple under the linkage key, including a
// deleted row), an unknown algorithm, or a withdrawn key id fails closed.
func (s *Service) VerifyAcceptance(ctx context.Context, acceptanceID string) (VerificationResult, error) {
	row, ok, err := s.store.LoadAcceptance(ctx, acceptanceID)
	if err != nil {
		return VerificationResult{}, err
	}
	if !ok {
		return VerificationResult{Reason: "acceptance row missing"}, nil
	}
	auditRow, ok, err := s.seam.LoadAcceptance(ctx, acceptanceID)
	if err != nil {
		return VerificationResult{}, err
	}
	if !ok {
		return VerificationResult{Reason: "audit row missing or ambiguous under the linkage key"}, nil
	}
	auditRow, err = DecodeAuditRow(auditRow)
	if err != nil {
		return VerificationResult{Reason: "audit row values fail the separator encoding"}, nil
	}
	if auditRow.Alg != ArtifactAlg {
		return VerificationResult{Reason: "audit row names an unknown algorithm"}, nil
	}
	key, kerr := s.keys.KeyFor(auditRow.KeyID)
	if kerr != nil {
		return VerificationResult{Reason: "signing key id not configured (withdrawn)"}, nil
	}
	fields := ArtifactFields{
		AcceptanceID: row.ID, QuoteID: row.CustomerQuoteID, OrgID: row.OrgID, TenantID: row.TenantID,
		SignedName: row.SignedName, Affirmative: row.Affirmative, ConsentText: row.ConsentText,
		AcceptedTotalCents: row.AcceptedTotalCents, AcceptedAt: row.AcceptedAt,
	}
	if row.SignatureAttachmentID != nil {
		fields.SignatureAttachmentID = *row.SignatureAttachmentID
	}
	hashHex, sigHex, derr := ArtifactDigest(key, fields)
	if derr != nil {
		return VerificationResult{Reason: "artifact fields fail canonicalization"}, nil
	}
	if hashHex != auditRow.ArtifactHash {
		return VerificationResult{Reason: "artifact hash mismatch: the acceptance row does not match the signed bytes"}, nil
	}
	if !CompareMAC(auditRow.ArtifactSig, sigHex) {
		return VerificationResult{Reason: "artifact signature mismatch"}, nil
	}
	return VerificationResult{OK: true}, nil
}

// newChangeID mints a change-request id. It is not a credential: a random
// 128-bit hex label for the JSONB entry, in the prefixed style the reference
// used, never an authority.
func newChangeID() string {
	raw, _, err := MintToken()
	if err != nil {
		return "chg"
	}
	return "chg_" + raw[:16]
}
