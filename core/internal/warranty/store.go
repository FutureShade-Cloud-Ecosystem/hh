package warranty

import (
	"context"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// warrantyStore is the persistence seam (mirrors the internal/lien lienStore + internal/gl glStore pattern): the
// service holds an interface so unit tests inject a DB-free fake and the integration suite injects the real pgx
// *Repository. Every method reads/writes ONLY warranty-owned tables (warranty_vendor_profiles,
// warranty_product_overrides, warranty_claims, warranty_claim_lines, warranty_claim_events,
// warranty_checklist_items), the zero-trust rule (modules never touch another module's tables).
type warrantyStore interface {
	// - registry (FR-1) -
	UpsertVendorProfile(ctx context.Context, in VendorProfileInput, createdBy string) (VendorProfile, error)
	GetVendorProfileByVendor(ctx context.Context, vendorID string) (VendorProfile, error)
	ListVendorProfiles(ctx context.Context) ([]VendorProfile, error)
	AddProductOverride(ctx context.Context, in ProductOverrideInput) (ProductOverride, error)
	OverridesForProfile(ctx context.Context, profileID string) ([]ProductOverride, error)

	// - claims (FR-2) -
	// InsertClaim assigns the next #WTY-n (when claimNo is "") and inserts the claim + lines atomically under an
	// advisory lock, so claim numbers are gap-free and never collide under concurrent capture.
	InsertClaim(ctx context.Context, in ClaimInput, claimNo string, openedAt time.Time, createdBy string) (Claim, error)
	GetClaim(ctx context.Context, id string) (Claim, error)
	ListClaims(ctx context.Context, f ClaimFilter) ([]Claim, error)
	ClaimLines(ctx context.Context, claimID string) ([]ClaimLine, error)
	UpdateClaimVendorOwner(ctx context.Context, claimID, vendorID, ownerID string) error

	// - lifecycle (FR-3) -
	// AdvanceClaimPhaseGuarded is the SERIALIZATION POINT for a phase advance: it SELECT … FOR UPDATE-locks the
	// claim row, re-checks the mutual-exclusion guard UNDER the lock (guardExit refuses advancing a SETTLED claim
	// to a terminal-LOST exit; a terminal claim is refused outright), then writes the phase + the PHASE_CHANGE
	// event in ONE tx. Together with the row lock ClaimSettlementWinner takes, this makes advance-to-LOST-exit and
	// settlement mutually exclusive and atomic (fixes the money-critical TOCTOU race).
	AdvanceClaimPhaseGuarded(ctx context.Context, claimID, toPhase string, guardExit bool, submittedAt, slaDue, closedAt *time.Time, event ClaimEvent) error
	InsertEvent(ctx context.Context, e ClaimEvent) (string, error)
	EventsForClaim(ctx context.Context, claimID string) ([]ClaimEvent, error)
	// TickedKeys returns the CURRENT ticked state of each checklist item on a claim (last tick wins), keyed by
	// item_key, the checklist gate + the card read this.
	TickedKeys(ctx context.Context, claimID string) (map[string]bool, error)
	ChecklistForPhase(ctx context.Context, phase, vendorID string) ([]ChecklistItem, error)
	ChecklistItemByKey(ctx context.Context, phase, vendorID, itemKey string) (ChecklistItem, error)

	// - settlement (FR-5) -
	// ClaimSettlementWinner is the atomic winner-election for settlement: it conditionally writes outcome +
	// amounts (only when the claim is unsettled) AND the SETTLEMENT event in ONE transaction, returning won=true
	// for the single winner and won=false for every concurrent/repeat settle (which the service turns into a
	// 409, posting nothing). This is the serialization point that keeps the claim, the FR-7 money, and the GL
	// journal in agreement under any concurrency.
	ClaimSettlementWinner(ctx context.Context, claimID, outcome string, expectedCents, realizedCents int64, event ClaimEvent) (won bool, err error)
	// StampSettlementJournal records the GL journal id on the (already-won) claim after a successful post.
	// Best-effort: a failure leaves the settlement recorded without the journal id, never diverging.
	StampSettlementJournal(ctx context.Context, claimID, glJournalID string) error

	// - reporting (FR-7) -
	// AllClaimsForReport returns the minimal per-claim projection the pure reporting math needs.
	AllClaimsForReport(ctx context.Context, branchID string) ([]ReportClaim, error)

	// - W5-B: per-outcome money paths (ERP:warranty-p2, 0129) -
	// OutcomePath reads the CONFIGURED money path for an outcome. A missing/inactive row is an ERROR, never a
	// default: inventing a money path for money is worse than declining to move it.
	OutcomePath(ctx context.Context, outcome string) (MoneyPathConfig, error)
	// InsertSettlementPosting records one executed money path. UPSERT on (claim_id, money_path) so a retry of
	// the same path is idempotent rather than a second accrual.
	InsertSettlementPosting(ctx context.Context, p SettlementPosting) (string, error)
	PostingsForClaim(ctx context.Context, claimID string) ([]SettlementPosting, error)

	// - W5-B: WTY-RECOVERY board (DIB-221, 0130) -
	// RecoveryRows returns the per-claim accrued/received/outstanding projection straight off the
	// warranty_recovery_balances view, the arithmetic has ONE definition and it is in SQL.
	RecoveryRows(ctx context.Context, branchID, vendorID string, openOnly bool) ([]RecoveryRow, error)
	InsertRecoveryAction(ctx context.Context, a RecoveryAction) (string, error)
	RecoveryActionsForClaim(ctx context.Context, claimID string) ([]RecoveryAction, error)
	// RecoveryBalanceForClaim reads ONE claim's accrual/received pair, the reconciliation check a RECEIVED
	// action runs against before it is allowed to over-receive.
	RecoveryBalanceForClaim(ctx context.Context, claimID string) (RecoveryRow, error)

	// - W5-B: public intake (DIB-222, 0131) -
	IntakeConfig(ctx context.Context) (IntakeConfig, error)
	// IntakeRateLimitHit atomically increments the bucket for a window and returns the post-increment count.
	// Atomic because a rate limiter that reads-then-writes is not a rate limiter under concurrency.
	IntakeRateLimitHit(ctx context.Context, bucketKey string, windowStart time.Time) (int, error)
	// InsertPublicClaim is the ONE write the public route performs: a claim + its lines + its photos in a single
	// transaction. Nothing else in the database is reachable from the public doorway.
	InsertPublicClaim(ctx context.Context, in PublicIntakeInput, claimNo, publicRef, branchID string, photos []IntakePhoto, openedAt time.Time) (Claim, error)
	IntakePhotosForClaim(ctx context.Context, claimID string) ([]IntakePhoto, error)
	IntakePhoto(ctx context.Context, claimID, photoID string) (IntakePhoto, error)

	// - W5-B: SLA timers (DIB-223, 0132) -
	SLATargets(ctx context.Context) ([]SLATarget, error)
	// OpenClaimPhaseClocks returns every open claim's current phase and when it ENTERED that phase (derived from the
	// audit spine, so no denormalized column can drift out of step with the events).
	OpenClaimPhaseClocks(ctx context.Context, branchID string) ([]PhaseClock, error)
	UpsertSLAAlert(ctx context.Context, a SLAAlert) (string, error)
	SLAAlerts(ctx context.Context, branchID string, includeAcknowledged bool) ([]SLAAlert, error)
	AcknowledgeSLAAlert(ctx context.Context, alertID, userID string) error
	MarkSLANotified(ctx context.Context, alertID, status, ref, errText string) error
}

// ReportClaim is the minimal per-claim projection the FR-7 reporting math consumes (kept DB-free so the math is
// unit-testable). Times are concrete so age/settlement-day arithmetic is exact.
type ReportClaim struct {
	ID            string
	ClaimNo       string
	BranchID      string // the owning branch, the reporting scope gate filters on it (horizontal access)
	VendorID      string
	VendorName    string
	Phase         string
	Outcome       string
	ExpectedCents int64
	RealizedCents int64
	OpenedAt      time.Time
	ClosedAt      *time.Time
}

// ClaimFilter scopes a claim list (all fields optional; "" = unfiltered).
type ClaimFilter struct {
	BranchID string
	Phase    string
	VendorID string
}

// compile-time assertion: the real pgx Repository satisfies the store seam.
var _ warrantyStore = (*Repository)(nil)

// withStore swaps the persistence seam (unit tests inject a DB-free fake).
func (s *Service) withStore(st warrantyStore) *Service { s.store = st; return s }

// withCaller swaps the guard-stamped StaffContext resolver (unit tests).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// withClock injects a deterministic clock (unit tests).
func (s *Service) withClock(now func() time.Time) *Service { s.now = now; return s }

// clock returns the service clock (defaults to time.Now).
func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// caller resolves the trusted, server-stamped acting staff member (never a spoofable header, C-1).
func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, errUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, errUnauthorized
	}
	return c, nil
}
