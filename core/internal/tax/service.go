package tax

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// attestationPendingBanner is the fixed banner every attestation ships under at W6, the FN relief attestation
// text is not yet legally cleared (Amanda on leave, D-021). It is stamped on serialized evidence, never a gate.
const attestationPendingBanner = "PENDING_LEGAL_REVIEW"

// clock returns the service clock (time.Now unless a test injects one).
// today is the dealer's business day on the service's clock: the dealer's zone, read at the moment
// the default is needed. A service with no zone source is the Ontario dealer's.
func (s *Service) today(ctx context.Context) (string, error) {
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return "", err
	}
	return zone.Today(s.clock), nil
}

// normDate is the package normDate in the dealer's zone: a blank date is the dealer's today, a named
// one is validated and kept (and reads no zone).
func (s *Service) normDate(ctx context.Context, d string) (string, error) {
	if d == "" {
		return s.today(ctx)
	}
	return normDate(d, s.clock)
}

func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// -- AssessSale, the binding tax engine (Provider) --------------------------------------------------------

// assessSale is the real M5 engine the frozen AssessSale delegates to. It reads the effective-dated rate from
// tax_rates for the reason code, PRESERVES the 0% on-reserve-delivery hard gate (an evidence-gated reason
// binds its relief rate only when evidence is attached; otherwise it falls back to the standard rate and flags
// RequiresEvidence), rounds ONCE at the total (half-up), and returns the FN split fields. The per-line split is
// the caller's job (moneyutil.AllocateLargestRemainder), so this is the single source of the binding header tax.
// Both standard fallback reads check found: with the standard row not in effect either, the engine REFUSES with
// the typed ErrNoStandardRate, never a silent 0%.
func (s *Service) assessSale(ctx context.Context, in SaleAssessment) (Quote, error) {
	asOf, err := s.normDate(ctx, in.AsOf)
	if err != nil {
		return Quote{}, err
	}
	// The dealer's own jurisdiction, read live at every assessment: the rate rows, the standard
	// reason and the standard rate all belong to it.
	jur, err := s.jurisdiction(ctx)
	if err != nil {
		return Quote{}, err
	}
	stdReason := StandardReasonFor(jur)
	reason := in.ReasonCode
	// The lawful reason universe (the mirror of the snapshot CHECK): the caller sent a reason the engine
	// cannot lawfully bind, refuse it, never resolve it through the table. The refusal is TYPED: the
	// composition's adapters key on the type, so no other 400-shaped engine refusal can reach the
	// counter worded as an unknown tax reason.
	if reason != "" && !LegalReasonCodes[reason] {
		return Quote{}, UnknownReasonError{ReasonCode: reason}
	}
	// Blank, or either spelling of a standard sale, is the dealer's own standard: the Ontario code for an
	// Ontario dealer, the neutral code for every other jurisdiction.
	if reason == "" || isStandardReason(reason) {
		reason = stdReason
	}
	// The Ontario first nations point-of-sale credit is Ontario's alone. Any other jurisdiction rings the
	// sale as a standard sale at its own standard: no relief reason, no evidence gate, no credit.
	if reason == ReasonONFNPos8 && jur != jurisdictionON {
		reason = stdReason
	}

	rateBps, found, err := s.rate(ctx, jur, reason, asOf)
	if err != nil {
		return Quote{}, err
	}
	// stdBps is the jurisdiction's standard on the sale date, read at most once per assessment.
	var stdBps int
	var stdRead, stdFound bool
	standard := func() (int, bool, error) {
		if !stdRead {
			bps, ok, err := s.rate(ctx, jur, stdReason, asOf)
			if err != nil {
				return 0, false, err
			}
			stdBps, stdFound, stdRead = bps, ok, true
		}
		return stdBps, stdFound, nil
	}
	if !found {
		// No configured row for this reason on this date → fall back to the standard rate. The fallback read
		// checks found: with the standard row not in effect either, the engine REFUSES (typed ErrNoStandardRate).
		// A zero-rate Quote with a nil error would ring every ticket untaxed while the ledger believes a rate is
		// configured (row 680 item 2).
		s.log.Warn("tax: no effective rate row for the reason; trying the standard rate row", "reason", reason, "as_of", asOf)
		std, ok, err := standard()
		if err != nil {
			return Quote{}, err
		}
		if !ok {
			s.log.Warn("tax: no standard rate row in effect; refusing the assessment (never a silent 0%)", "reason", reason, "as_of", asOf)
			return Quote{}, noStandardRate(jur, asOf)
		}
		rateBps = std
	}
	// The ungated-below-standard refusal: EVERY reason that resolves below the standard IN EFFECT for the
	// date must be evidence gated (ReasonManual is the stated exception, its row is a person's decision;
	// the standard reason is the reference itself, never the subject, because the dealer may lawfully raise or
	// lower the standard row). The comparison is against the standard row resolved for the same date, not
	// a hard-coded constant: the table exists to replace the constant, and a raised standard must not
	// open a gap below it that an ungated reason can bind in. With no standard row in effect there is no
	// reference to fall short of, so nothing is refused on this check (the absent-standard world is the
	// silent-zero item's, not this one's).
	if reason != stdReason && reason != ReasonManual && !evidenceGatedReasons[reason] {
		std, ok, err := standard()
		if err != nil {
			return Quote{}, err
		}
		if ok && rateBps < std {
			return Quote{}, MisconfiguredRateError{ReasonCode: reason, RateBps: rateBps, StdBps: std}
		}
	}

	q := Quote{ReasonCode: reason, RateBps: rateBps, Jurisdiction: jur}

	// HARD GATE: an evidence-gated relief reason binds its relief rate only with evidence attached. Without
	// evidence the BINDING rate falls back to standard, bit-identical to the W4/pos behavior, while the reason
	// code is retained and RequiresEvidence is flagged so the caller knows the relief is pending. reliefBinds
	// gates BOTH the reduced rate and any point-of-sale credit: an ungated credit would hand back the 8% with no
	// proof even though the rate correctly reverted to standard (S3 W6 B3). The gate's standard fallback read
	// checks found too: with the standard row not in effect, the engine REFUSES, never binds 0 as "the standard".
	reliefBinds := true
	if evidenceGatedReasons[reason] {
		q.RequiresEvidence = true
		if !in.EvidenceAttached {
			reliefBinds = false
			std, ok, rerr := standard()
			if rerr != nil {
				return Quote{}, rerr
			}
			if !ok {
				s.log.Warn("tax: no standard rate row in effect for the relief fallback; refusing the assessment (never a silent 0%)", "reason", reason, "as_of", asOf)
				return Quote{}, noStandardRate(jur, asOf)
			}
			q.RateBps = std
		}
	}

	// The standard rate the full-tax figures are computed at. An Ontario dealer whose standard row is
	// absent but whose bound reason resolved keeps the Ontario 13 percent the engine always used; any
	// other jurisdiction refuses, since there is no honest standard to fall back to.
	std, ok, err := standard()
	if err != nil {
		return Quote{}, err
	}
	switch {
	case ok:
		q.StandardRateBps = std
	case jur == jurisdictionON:
		q.StandardRateBps = stdRateBps
	default:
		return Quote{}, noStandardRate(jur, asOf)
	}

	q.TaxCents = hstOn(in.SubtotalCents, q.RateBps)
	q.NetCollectedCents = q.TaxCents

	// ON_FN_POS_8 tracks the 8% Ontario credit alongside the 5% net collected (FN spec §POS credit), only once
	// the relief actually binds (evidence attached).
	if reason == ReasonONFNPos8 && reliefBinds {
		q.OnCreditCents = hstOn(in.SubtotalCents, ontarioCredBps)
	}
	return q, nil
}

// jurisdiction answers the dealer's tax jurisdiction code: the injected source's answer, Ontario when
// there is no source or the source answers blank. A failed read is an error, never a guess at Ontario
// for a dealer that is not.
func (s *Service) jurisdiction(ctx context.Context) (string, error) {
	if s.region == nil {
		return jurisdictionON, nil
	}
	r, err := s.region.TaxRegion(ctx)
	if err != nil {
		return "", err
	}
	r = strings.ToUpper(strings.TrimSpace(r))
	if r == "" {
		return jurisdictionON, nil
	}
	return r, nil
}

// StandardRateBps answers the dealer's own standard rate on a date ("" is today): the rate the engine
// binds a standard sale at, for the rules and the postings that need the standard without a sale (the
// status-card profile rule, the NSF fee backout). An Ontario dealer with no row keeps the Ontario 13
// percent; any other jurisdiction refuses with the typed no standard rate error.
func (s *Service) StandardRateBps(ctx context.Context, asOf string) (int, error) {
	day, err := s.normDate(ctx, asOf)
	if err != nil {
		return 0, err
	}
	jur, err := s.jurisdiction(ctx)
	if err != nil {
		return 0, err
	}
	bps, ok, err := s.rate(ctx, jur, StandardReasonFor(jur), day)
	if err != nil {
		return 0, err
	}
	if ok {
		return bps, nil
	}
	if jur == jurisdictionON {
		return stdRateBps, nil
	}
	return 0, noStandardRate(jur, day)
}

// rate resolves the effective rate for a reason in a jurisdiction on a date, tolerating a nil repo
// (offline → the Ontario standard fallback).
func (s *Service) rate(ctx context.Context, jur, reason, asOf string) (int, bool, error) {
	if s.repo == nil {
		if jur == jurisdictionON && (reason == ReasonStdON13 || reason == ReasonManual) {
			return stdRateBps, true, nil
		}
		return 0, false, nil
	}
	return s.repo.ResolveRate(ctx, jur, reason, asOf)
}

// -- rate table admin ----------------------------------------------------------

// ListRates returns the configured effective-dated rate table.
func (s *Service) ListRates(ctx context.Context) ([]Rate, error) { return s.repo.ListRates(ctx) }

// CreateRate inserts a new effective-dated rate row (a hot change: the engine picks it up live on the sale date).
// The admin edge enforces the same two rules the bind edge does: the reason must be inside the lawful universe
// (the mirror of the snapshot CHECK), and an ungated reason cannot be planted below the highest standard
// in effect anywhere over the row's own interval, refused with the same words the engine refuses to bind it,
// so the admin edge fails first and the ring never sees the row. The standard reason itself is the
// reference, never the subject: the dealer may lawfully raise or lower the standard row. One residue the
// guard cannot reach: raising the standard does not re-judge rows already planted; the bind edge refuses
// those (its own guard), a server fault at the ring until the row is fixed.
func (s *Service) CreateRate(ctx context.Context, r Rate) (Rate, error) {
	if r.ReasonCode == "" {
		return Rate{}, httpx.BadRequestf("reason_code required")
	}
	if !LegalReasonCodes[r.ReasonCode] {
		return Rate{}, httpx.BadRequestf("unknown reason_code %s: not one of the lawful reason codes the rate table can carry", boundReason(r.ReasonCode))
	}
	if r.RateBps < 0 || r.RateBps > 10000 {
		return Rate{}, httpx.BadRequestf("rate_bps must be 0..10000")
	}
	// The jurisdiction's case is normalised as the engine normalises the dealer's region, so a row posted
	// as "ca" lands where the engine looks for "CA" instead of sitting inert.
	r.Jurisdiction = strings.ToUpper(strings.TrimSpace(r.Jurisdiction))
	if r.Jurisdiction == "" {
		// A blank area is the dealer's own jurisdiction (Ontario for an Ontario dealer).
		jur, err := s.jurisdiction(ctx)
		if err != nil {
			return Rate{}, err
		}
		r.Jurisdiction = jur
	}
	if isStandardReason(r.ReasonCode) && r.ReasonCode != StandardReasonFor(r.Jurisdiction) {
		return Rate{}, httpx.BadRequestf("the standard reason for jurisdiction %s is %s", boundReason(r.Jurisdiction), StandardReasonFor(r.Jurisdiction))
	}
	if r.EffectiveFrom == "" {
		day, err := s.today(ctx)
		if err != nil {
			return Rate{}, err
		}
		r.EffectiveFrom = day
	}
	if _, err := time.Parse("2006-01-02", r.EffectiveFrom); err != nil {
		return Rate{}, httpx.BadRequestf("effective_from must be YYYY-MM-DD")
	}
	if !isStandardReason(r.ReasonCode) && r.ReasonCode != ReasonManual && !evidenceGatedReasons[r.ReasonCode] {
		// The guard measures the row's WHOLE life, not only its first day: the highest standard in
		// effect anywhere the row's interval reaches (an open-ended row reaches into every standard
		// era that follows it). A backdated row must clear the later raised standards it will govern
		// beside, and a row planted today must clear today's (the round 2 review's P2: a backdated
		// ungated row slipped in beside a later raise and the ring then refused it as a server fault).
		maxStd, anyStd, err := s.repo.MaxRateInRange(ctx, r.Jurisdiction, StandardReasonFor(r.Jurisdiction), r.EffectiveFrom, r.EffectiveTo)
		if err != nil {
			return Rate{}, err
		}
		if anyStd && r.RateBps < maxStd {
			return Rate{}, httpx.BadRequestf("%s", MisconfiguredRateError{ReasonCode: r.ReasonCode, RateBps: r.RateBps, StdBps: maxStd}.Error())
		}
	}
	return s.repo.InsertRate(ctx, r)
}

// -- status-card profiles --------------------------------------------------------

// ListProfiles returns the admin-editable status-card exemption profiles.
func (s *Service) ListProfiles(ctx context.Context) ([]StatusCardProfile, error) {
	return s.repo.ListProfiles(ctx)
}

// UpsertProfile creates/edits a status-card profile. The requires_* forced-evidence flags cannot all be turned
// off, a relief profile MUST force at least the band-card number (D-036 forced evidence).
func (s *Service) UpsertProfile(ctx context.Context, p StatusCardProfile) (StatusCardProfile, error) {
	if p.Code == "" {
		return StatusCardProfile{}, httpx.BadRequestf("code required")
	}
	if p.RateBps < 0 || p.RateBps > 10000 {
		return StatusCardProfile{}, httpx.BadRequestf("rate_bps must be 0..10000")
	}
	std, err := s.StandardRateBps(ctx, "")
	if err != nil {
		return StatusCardProfile{}, err
	}
	if p.RateBps < std && !p.RequiresNumber {
		return StatusCardProfile{}, httpx.BadRequestf("a relief profile must force the band-card number as evidence")
	}
	if p.Status == "" {
		p.Status = "ACTIVE"
	}
	return s.repo.UpsertProfile(ctx, p)
}

// -- evidence (masked-only serialization + A-21 audit) -----------------------------

// CaptureEvidence records status-card evidence for a relief decision. The raw card number is masked and
// DISCARDED (never stored/echoed); the forced-evidence gate (photo + number + attestation per the profile)
// is enforced; an A-21 EVIDENCE_CHANGE row is written. Returns the MASKED evidence record.
func (s *Service) CaptureEvidence(ctx context.Context, in EvidenceCapture) (Evidence, error) {
	var prof StatusCardProfile
	if in.CardProfileCode != "" {
		p, ok, err := s.repo.GetProfileByCode(ctx, in.CardProfileCode)
		if err != nil {
			return Evidence{}, err
		}
		if !ok {
			return Evidence{}, httpx.BadRequestf("unknown card_profile_code")
		}
		prof = p
	}

	// Forced-evidence gate, a relief profile FORCES photo + number + attestation (D-036).
	if prof.RequiresPhoto && in.PhotoRef == "" {
		return Evidence{}, httpx.BadRequestf("band-card photo is required for this profile")
	}
	if prof.RequiresNumber && in.RawNumber == "" {
		return Evidence{}, httpx.BadRequestf("band-card number is required for this profile")
	}
	if prof.RequiresAttestation && !in.Attestation {
		return Evidence{}, httpx.BadRequestf("attestation is required for this profile")
	}

	ev := Evidence{
		ProfileID:     in.ProfileID,
		CardProfileID: prof.ID,
		OrderID:       in.OrderID,
		PhotoRef:      in.PhotoRef,
		NumberMask:    maskCardNumber(in.RawNumber), // the ONLY identifier persisted
		Attestation:   in.Attestation,
		TCRDExpiry:    in.TCRDExpiry,
		CapturedBy:    identity.StaffUserIDFromContext(ctx),
	}
	saved, err := s.repo.InsertEvidence(ctx, ev)
	if err != nil {
		return Evidence{}, err
	}
	saved.AttestationStatus = attestationPendingBanner
	s.recordAccess(ctx, audit.ActEvidenceChange, saved.ID, saved.NumberMask, "TAX_EVIDENCE_CAPTURE")
	return saved, nil
}

// ListEvidence returns masked evidence records for the evidence screen. NO raw identifier is ever present.
func (s *Service) ListEvidence(ctx context.Context, f EvidenceFilter) ([]Evidence, error) {
	rows, err := s.repo.ListEvidence(ctx, f)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].AttestationStatus = attestationPendingBanner
	}
	return rows, nil
}

// ViewEvidence returns one masked evidence record and writes an A-21 EVIDENCE_VIEW row.
func (s *Service) ViewEvidence(ctx context.Context, id string) (Evidence, bool, error) {
	ev, ok, err := s.repo.GetEvidence(ctx, id)
	if err != nil || !ok {
		return Evidence{}, ok, err
	}
	ev.AttestationStatus = attestationPendingBanner
	s.recordAccess(ctx, audit.ActEvidenceView, id, ev.NumberMask, "TAX_EVIDENCE_VIEW")
	return ev, true, nil
}

// DownloadEvidence returns the masked record + the encrypted photo reference for a download, writing an A-21
// EVIDENCE_DOWNLOAD row. The raw card number is NEVER included (masked-only contract); only the photo pointer.
func (s *Service) DownloadEvidence(ctx context.Context, id string) (Evidence, bool, error) {
	ev, ok, err := s.repo.GetEvidence(ctx, id)
	if err != nil || !ok {
		return Evidence{}, ok, err
	}
	ev.AttestationStatus = attestationPendingBanner
	s.recordAccess(ctx, audit.ActEvidenceDownload, id, ev.NumberMask, "TAX_EVIDENCE_DOWNLOAD")
	return ev, true, nil
}

// ValidateReliefEvidence checks an evidence record for relief eligibility on a date. An EXPIRED tcrd BLOCKS
// relief (tcrd_expiry gate), as does a missing forced field. It writes an A-21 EVIDENCE_VIEW row (an access).
func (s *Service) ValidateReliefEvidence(ctx context.Context, evidenceID, asOf string) (ReliefEligibility, error) {
	on, err := s.normDate(ctx, asOf)
	if err != nil {
		return ReliefEligibility{}, err
	}
	ev, ok, err := s.repo.GetEvidence(ctx, evidenceID)
	if err != nil {
		return ReliefEligibility{}, err
	}
	if !ok {
		return ReliefEligibility{}, httpx.BadRequestf("evidence not found")
	}
	s.recordAccess(ctx, audit.ActEvidenceView, evidenceID, ev.NumberMask, "TAX_RELIEF_EVIDENCE_CHECK")

	out := ReliefEligibility{NumberMask: ev.NumberMask, Eligible: true}
	if !ev.Attestation {
		out.Eligible, out.BlockReason = false, "attestation missing"
		return out, nil
	}
	if ev.TCRDExpiry != "" {
		exp, perr := time.Parse("2006-01-02", ev.TCRDExpiry)
		if perr == nil {
			onD, _ := time.Parse("2006-01-02", on)
			if exp.Before(onD) {
				out.Eligible, out.BlockReason = false, "status card (TCRD) expired"
			}
		}
	}
	return out, nil
}

// -- config --------------------------------------------------------------------

// GetConfig returns the tax config for a branch (empty branchID = the org default row). The invoice carries
// the configured hst_registration_no from here.
func (s *Service) GetConfig(ctx context.Context, branchID string) (Config, error) {
	return s.repo.GetConfig(ctx, branchID)
}

// UpdateConfig upserts the tax config (HST registration number, round mode, dormant toggles).
func (s *Service) UpdateConfig(ctx context.Context, c Config) (Config, error) {
	if c.RoundMode == "" {
		c.RoundMode = "INVOICE_TOTAL"
	}
	if c.RoundMode != "INVOICE_TOTAL" && c.RoundMode != "PER_LINE" {
		return Config{}, httpx.BadRequestf("round_mode must be INVOICE_TOTAL or PER_LINE")
	}
	// Avalara + postal routing are DORMANT stubs at W6, refuse to enable them (D-A19-5).
	if c.AvalaraEnabled || c.PostalRoutingEnabled {
		return Config{}, httpx.BadRequestf("avalara/postal routing are dormant at W6 and cannot be enabled")
	}
	return s.repo.UpsertConfig(ctx, c)
}

// -- recompute (W4 snapshot recompute, insert-only, audit-truth only, parity with the live engine) ---------

// recompute re-derives the W4 flat-13 snapshots under the M5 engine on the SAME code path (assessSale), and
// inserts a parallel M5 audit-truth snapshot per W4 row. INSERT-ONLY: it never rewrites a W4 row (there is no
// retro GL correction, the W4 collected amounts stay binding) and it is IDEMPOTENT (a re-run skips a W4 row
// that already has its M5 recompute). Because it drives the exact assessSale the live sale uses, the recomputed
// value is byte-equal to the live engine's on the same inputs (parity, asserted in the tests).
func (s *Service) recompute(ctx context.Context, fromDate, toDate string) (RecomputeResult, error) {
	res := RecomputeResult{FromDate: fromDate, ToDate: toDate}
	w4, err := s.repo.ListW4Snapshots(ctx, fromDate, toDate)
	if err != nil {
		return res, err
	}
	res.Scanned = len(w4)
	for _, src := range w4 {
		exists, err := s.repo.SnapshotExists(ctx, src.OrderID, src.OrderLineID, ruleVersionM5)
		if err != nil {
			return res, err
		}
		if exists {
			res.SkippedExists++
			continue
		}
		out, err := s.deriveRecomputed(ctx, src)
		if err != nil {
			return res, err
		}
		if _, err := s.repo.InsertSnapshot(ctx, out); err != nil {
			return res, err
		}
		res.Inserted++
	}
	return res, nil
}

// deriveRecomputed reconstructs the taxable base from a W4 snapshot (base = full_hst ÷ its rate) and re-runs
// the LIVE assessSale on it, so the recompute output is produced by the same engine the sale uses (parity).
// This is the single derivation both the recompute and the parity test call, guaranteeing byte-equality.
func (s *Service) deriveRecomputed(ctx context.Context, src Snapshot) (Snapshot, error) {
	base := reconstructBase(src)
	q, err := s.assessSale(ctx, SaleAssessment{
		SubtotalCents:    base,
		ReasonCode:       src.ReasonCode,
		EvidenceAttached: src.RateBps == 0, // a settled 0% W4 row had its evidence satisfied at the time
	})
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		OrderID:           src.OrderID,
		OrderLineID:       src.OrderLineID,
		ReasonCode:        q.ReasonCode,
		RuleVersion:       ruleVersionM5,
		RateBps:           q.RateBps,
		FullHSTCents:      hstOn(base, q.StandardRateBps),
		StandardRateBps:   q.StandardRateBps,
		OnCreditCents:     q.OnCreditCents,
		NetCollectedCents: q.NetCollectedCents,
		Automated:         true, // M5 flips this true (automated engine, not a manual W4 decision)
	}, nil
}

// reconstructBase recovers the taxable subtotal from a W4 snapshot. tax_decision_snapshot carries no subtotal
// column (frozen 0029 schema), so the base is recovered from full_hst, which is ALWAYS stored at the standard
// rate (see snapshot build: FullHSTCents = hstOn(base, the standard rate)), NOT at the relief rate. Dividing
// by the relief rate (e.g. 5%, or 0% → fallback) inflated the base by up to 2.6× and broke the HST → GL
// tie-out (S3 W6 H1). Always divide by the standard rate, and by the one the row itself was written under
// (standard_rate_bps, 0391), never the dealer's current one: a rate change must not re-price history. A row
// written before the column carries zero, which is an Ontario row at 13 percent.
func reconstructBase(src Snapshot) int64 {
	if src.FullHSTCents == 0 {
		return 0
	}
	std := int64(src.StandardRateBps)
	if std <= 0 {
		std = stdRateBps
	}
	return (src.FullHSTCents * roundHalfUpBase) / std
}

// -- HST → GL reconciliation -----------------------------------------------------

// reconcile ties the period's DOCUMENT tax to the GL 2200 movement for the same period (FW1-11): the
// sales HST on the invoices less the credit memos (the collected side, the return's line 101), the
// tax refunded on POS returns shown as its own line (returns), and the input tax credits on the
// posted vendor bills shown apart (line 108), the three netted against 2200's month movement, which
// carries all three. The document sides read through the billing, pos and purchasing seams, the GL
// side through the frozen GLBalanceReader; the relief snapshots never stand in for collected tax
// again (the audit's LEDGER-TIEOUT finding). Variance 0 = tied out. The 2201/2202 federal/Ontario
// split balances are surfaced for the report, unmoved: no posting rule writes them.
//
// Until 612's late posted filing rule is on this base, both sides read the period by its own grain:
// the documents by their dates, the GL by the journal's fiscal period. The rebase after 612 merges
// moves the as-of reads to the filed period (FROM-PRINCIPAL 105 P2-3).
func (s *Service) reconcile(ctx context.Context, year, month int, branchID string) (ReconReport, error) {
	rep := ReconReport{Year: year, Month: month, BranchID: branchID}
	if s.salesHST == nil {
		return rep, fmt.Errorf("tax: the sales document HST seam is not wired; the reconcile cannot be computed")
	}
	sales, err := s.salesHST.DocumentSalesHSTCents(ctx, year, month, branchID)
	if err != nil {
		return rep, err
	}
	rep.CollectedCents = sales
	if s.returnHST == nil {
		return rep, fmt.Errorf("tax: the POS return HST seam is not wired; the reconcile cannot be computed")
	}
	returns, err := s.returnHST.ReturnHSTCents(ctx, year, month, branchID)
	if err != nil {
		return rep, err
	}
	rep.ReturnTaxCents = returns
	if s.billITCs == nil {
		return rep, fmt.Errorf("tax: the vendor bill input tax seam is not wired; the reconcile cannot be computed")
	}
	itcs, err := s.billITCs.VendorBillITCCents(ctx, year, month, branchID)
	if err != nil {
		return rep, err
	}
	rep.InputTaxCreditsCents = itcs
	if s.glBalance != nil {
		if v, err := s.glBalance.GLBalanceCents(ctx, acctHSTPayable, year, month, branchID); err == nil {
			rep.GLPayableCents = v
		} else {
			return rep, err
		}
		if v, err := s.glBalance.GLBalanceCents(ctx, acctHSTFederal, year, month, branchID); err == nil {
			rep.GLFederalCents = v
		}
		if v, err := s.glBalance.GLBalanceCents(ctx, acctHSTOntario, year, month, branchID); err == nil {
			rep.GLOntarioCents = v
		}
	}
	rep.VarianceCents = rep.GLPayableCents - (rep.CollectedCents - rep.ReturnTaxCents - rep.InputTaxCreditsCents)
	rep.TiedOut = rep.VarianceCents == 0
	return rep, nil
}

// -- audit helper --------------------------------------------------------------

// recordAccess writes an A-21 sensitive-access row for an evidence access (VIEW/CHANGE/DOWNLOAD). Best-effort
// , a missing Recorder (offline) is a no-op. MaskedRef is the masked band-card number; the raw never enters.
func (s *Service) recordAccess(ctx context.Context, action, entityID, maskedRef, purpose string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, audit.SensitiveAccess{
		Action:     action,
		EntityType: "fn.evidence_doc",
		EntityID:   entityID,
		ActorID:    identity.StaffUserIDFromContext(ctx),
		Purpose:    purpose,
		MaskedRef:  maskedRef,
	})
}
