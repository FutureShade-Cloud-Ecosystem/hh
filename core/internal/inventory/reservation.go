package inventory

// reservation.go, DIB-60: the three-tier stock allocation policy and the reservation substrate.
//
// THE ONE RULE THIS FILE EXISTS TO ENFORCE
// ----------------------------------------
//
//	available = on_hand - reserved
//
// stated exactly once, in AvailableBase, and called by every availability read in the module. Before
// DIB-60 "available" and "on hand" were the same number, so a branch could promise the same pallet
// to a confirmed order, a live quote and an outbound transfer in the same minute and only find out
// when a picker walked to an empty bay.
//
// THREE TIERS, TWO GRADES
// -----------------------
// A reservation is TYPED by who asked (ORDER / QUOTE / TRANSFER) and GRADED by branch policy into
// whether it actually holds stock (HARD / SOFT / NONE). Only HARD is subtracted from available.
// That split is what makes the per-branch policy real rather than decorative: a branch that sets
// quote_mode=HARD genuinely begins reserving against quotes with no code change.
//
// The SYSTEM DEFAULT policy, what a branch with no configuration row does, is:
//
//	ORDER    → HARD, no expiry            hard-reserve on order confirm
//	QUOTE    → SOFT, expires in 72 hours  soft-reserve on quote
//	TRANSFER → HARD, no expiry            NOT configurable (see below)
//
// WHY SOFT DOES NOT SUBTRACT. A soft hold that blocked a cash sale would be worse than no hold at
// all: the quote may never convert, and a counter clerk must not be told "none left" because a
// contractor is thinking about it. Soft holds are visible (the visibility surface shows them) and
// they expire; they do not gate.
//
// WHY TRANSFER IS NOT IN THE POLICY TABLE. A transfer that does not hold its stock is an overdraft
// at the source branch, and DIB-71's no-overdraft guard grades against available. Making the grade
// configurable would let a branch switch off its own oversell guard from a settings screen. It is
// HARD, in code, always.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reservation types (stock_reservations.res_type CHECK set, 0115).
const (
	ResTypeOrder    = "ORDER"
	ResTypeQuote    = "QUOTE"
	ResTypeTransfer = "TRANSFER"
)

// Hold grades. HARD subtracts from available; SOFT is advisory; NONE writes no reservation at all.
const (
	HoldHard = "HARD"
	HoldSoft = "SOFT"
	HoldNone = "NONE"
)

// Reservation lifecycle (stock_reservations.status CHECK set, 0115).
const (
	ResActive   = "ACTIVE"
	ResReleased = "RELEASED"
	ResConsumed = "CONSUMED"
	ResExpired  = "EXPIRED"
)

// defaultQuoteTTLHours is the system-default soft-hold lifetime on a quote. 72h ≈ the working span
// of a contractor's decision; long enough that the hold is informative on Monday for a Friday quote,
// short enough that an abandoned quote stops occupying a number on a screen.
const defaultQuoteTTLHours = 72

// AllocationPolicy is the resolved per-branch allocation policy (branch_allocation_policy, 0115).
// IsDefault reports that no row existed and the system default was used, the settings surface says
// so out loud rather than showing configured-looking values nobody chose.
type AllocationPolicy struct {
	BranchID      string `json:"branch_id"`
	OrderMode     string `json:"order_mode"`
	QuoteMode     string `json:"quote_mode"`
	OrderTTLHours int    `json:"order_ttl_hours"`
	QuoteTTLHours int    `json:"quote_ttl_hours"`
	IsDefault     bool   `json:"is_default"`
}

// DefaultAllocationPolicy is THE stated default policy: hard-reserve on order confirm, soft on quote
// with a 72h life. A branch with no configuration row behaves exactly like this.
func DefaultAllocationPolicy() AllocationPolicy {
	return AllocationPolicy{
		OrderMode:     HoldHard,
		QuoteMode:     HoldSoft,
		OrderTTLHours: 0, // 0 = never expires: a confirmed order holds until it ships or is cancelled.
		QuoteTTLHours: defaultQuoteTTLHours,
		IsDefault:     true,
	}
}

// GradeFor resolves a reservation type to (hold mode, ttl hours) under this policy. TRANSFER is not
// policy-driven, it is always HARD with no expiry, for the reason in the file header.
func (p AllocationPolicy) GradeFor(resType string) (string, int) {
	switch resType {
	case ResTypeOrder:
		return p.OrderMode, p.OrderTTLHours
	case ResTypeQuote:
		return p.QuoteMode, p.QuoteTTLHours
	case ResTypeTransfer:
		return HoldHard, 0
	default:
		return HoldNone, 0
	}
}

// - the arithmetic -

// AvailableBase is the single definition of available stock in this module: on hand MINUS what is
// held. It is a plain function, not a method and not inlined at each call site, precisely so there
// is one place to read and one place to break, every availability path in the package routes
// through it, and the mutation test that deletes the subtraction fails in all of them at once.
//
// The result MAY be negative, and that is deliberate. A hard reservation can legitimately exceed
// on-hand (an order confirmed against inbound stock; a correction that shipped units out from under
// a hold). Clamping to zero here would report "0 available" for a branch that is actually 40 units
// in the hole, and the replenishment worklist would never see the gap it exists to find.
func AvailableBase(onHandBase, reservedBase float64) float64 {
	return onHandBase - reservedBase
}

// HoldsStock reports whether a hold grade is subtracted from available. Only HARD is. This is the
// second half of the arithmetic: AvailableBase says what the sum is, HoldsStock says which rows are
// in it.
func HoldsStock(holdMode string) bool { return holdMode == HoldHard }

// - domain shapes -

// Reservation is one typed hold on stock.
type Reservation struct {
	ID          string  `json:"id"`
	ProductID   string  `json:"product_id"`
	BranchID    string  `json:"branch_id"`
	LocationID  string  `json:"location_id"`
	ResType     string  `json:"res_type"`
	HoldMode    string  `json:"hold_mode"`
	QtyBase     float64 `json:"qty_base"`
	Status      string  `json:"status"`
	SourceType  string  `json:"source_type"`
	SourceRefID string  `json:"source_ref_id"`
	Reason      string  `json:"reason"`
	ExpiresAt   string  `json:"expires_at"`
	CreatedAt   string  `json:"created_at"`
	ClosedAt    string  `json:"closed_at"`
}

// ReserveInput is a request to hold stock.
type ReserveInput struct {
	ProductID string `json:"product_id"`
	BranchID  string `json:"branch_id"`
	// LocationID defaults to BranchID, the branch-grain projection every existing stock writer in
	// this module already uses (UpsertBalanceDelta is called with location == branch).
	LocationID  string  `json:"location_id"`
	ResType     string  `json:"res_type"`
	QtyBase     float64 `json:"qty_base"`
	SourceType  string  `json:"source_type"`
	SourceRefID string  `json:"source_ref_id"`
	Reason      string  `json:"reason"`
	CreatedBy   string  `json:"-"`
	// EnforceAvailable makes the reserve FAIL rather than push available negative. The transfer path
	// sets it (a transfer must not overdraw its source); order confirm does not (a backorder is a
	// real thing and refusing to reserve would lose the commitment rather than record it).
	EnforceAvailable bool `json:"-"`
	// ExpiresAt overrides the policy TTL. Zero = use policy.
	ExpiresAt time.Time `json:"-"`
}

// ReserveResult carries the written reservation. Applied=false means branch policy graded this type
// NONE and nothing was written, NOT an error: an order must still confirm at a branch that has
// switched allocation off.
type ReserveResult struct {
	Reservation Reservation      `json:"reservation"`
	Applied     bool             `json:"applied"`
	Policy      AllocationPolicy `json:"policy"`
}

// - service -

// ResolveAllocationPolicy returns the branch's configured policy, or the system default when the
// branch has no row. An empty branchID resolves to the system default too, a caller with no branch
// context gets defined behaviour rather than a lookup on ". The read runs on the POOL (nil tx): the
// HTTP surface calling this holds no transaction.
func (s *Service) ResolveAllocationPolicy(ctx context.Context, branchID string) (AllocationPolicy, error) {
	return s.resolveAllocationPolicyTx(ctx, nil, branchID)
}

// resolveAllocationPolicyTx is the transaction-aware core of the policy read; nil tx means the pool,
// the seam's stated convention. The reserve path passes ITS transaction so the policy read joins the
// connection the caller already holds: on the pool it would ask for a SECOND connection while the
// caller keeps its first, and pool-size in-flight reservations each doing that pin every connection
// of the pool and wait for one more, a client-side hold Postgres cannot see (no lock is waited on
// at all; the sessions sit idle in transaction) while the process stalls to the action timeout. The
// row locks a reservation holds play no part in the stall; they only make the scenario realistic.
func (s *Service) resolveAllocationPolicyTx(ctx context.Context, tx pgx.Tx, branchID string) (AllocationPolicy, error) {
	def := DefaultAllocationPolicy()
	def.BranchID = branchID
	if branchID == "" {
		return def, nil
	}
	p, found, err := s.db().AllocationPolicy(ctx, tx, branchID)
	if err != nil || !found {
		return def, err
	}
	return p, nil
}

// ReserveTx writes a typed hold inside the caller's transaction and maintains the qty_reserved
// projection in the same statement pair, so no reader ever sees the row and the projection disagree.
//
// Runs in the CALLER's tx on purpose: an order confirm that reserves stock and then fails to commit
// its own order row must not leave a hold behind, and the only way to guarantee that is to share the
// transaction rather than to compensate afterwards.
func (s *Service) ReserveTx(ctx context.Context, tx pgx.Tx, in ReserveInput) (ReserveResult, error) {
	if in.ProductID == "" {
		return ReserveResult{}, badReq("inventory: product_id is required")
	}
	if in.BranchID == "" {
		return ReserveResult{}, badReq("inventory: branch_id is required")
	}
	if in.QtyBase <= 0 {
		return ReserveResult{}, badReq("inventory: qty_base must be > 0")
	}
	switch in.ResType {
	case ResTypeOrder, ResTypeQuote, ResTypeTransfer:
	default:
		return ReserveResult{}, badReq("inventory: unknown reservation type %q", in.ResType)
	}
	if in.LocationID == "" {
		in.LocationID = in.BranchID
	}

	// The policy read runs INSIDE the caller's tx, for the same reason the overdraft guard below
	// does: a pool read here asks for a SECOND connection while the caller keeps its first, and
	// pool-size in-flight reservations doing that together pin the whole pool with no connection
	// left to answer any of them.
	policy, err := s.resolveAllocationPolicyTx(ctx, tx, in.BranchID)
	if err != nil {
		return ReserveResult{}, err
	}
	mode, ttl := policy.GradeFor(in.ResType)
	if mode == HoldNone {
		return ReserveResult{Applied: false, Policy: policy}, nil
	}

	var expires *time.Time
	switch {
	case !in.ExpiresAt.IsZero():
		e := in.ExpiresAt.UTC()
		expires = &e
	case ttl > 0:
		e := time.Now().UTC().Add(time.Duration(ttl) * time.Hour)
		expires = &e
	}

	// The overdraft guard reads INSIDE the tx so it sees this transaction's own earlier reserves -
	// two lines of one transfer for the same product must not each pass a guard the pair fails.
	if in.EnforceAvailable && HoldsStock(mode) {
		onHand, reserved, err := s.db().BalanceFactsTx(ctx, tx, in.ProductID, in.BranchID)
		if err != nil {
			return ReserveResult{}, err
		}
		if AvailableBase(onHand, reserved)+qtyEpsilon < in.QtyBase {
			return ReserveResult{}, conflictErr(
				"inventory: insufficient available stock at branch, %.4f available, %.4f requested",
				AvailableBase(onHand, reserved), in.QtyBase)
		}
	}

	res, err := s.db().InsertReservation(ctx, tx, in, mode, expires)
	if err != nil {
		return ReserveResult{}, err
	}
	if HoldsStock(mode) {
		if err := s.db().AdjustReserved(ctx, tx, in.ProductID, in.LocationID, in.BranchID, in.QtyBase); err != nil {
			return ReserveResult{}, err
		}
	}
	return ReserveResult{Reservation: res, Applied: true, Policy: policy}, nil
}

// ReleaseBySourceTx closes every ACTIVE reservation raised by one source document (an order, a
// quote, a transfer) with the given terminal status, unwinding the projection for the HARD ones.
// Returns the number of reservations closed and the quantity released from the hold.
//
// SOURCE-KEYED, not id-keyed, because that is how callers actually think: "this order shipped,
// release its holds" is one call that cannot miss a line, whereas iterating ids in the caller can.
func (s *Service) ReleaseBySourceTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error) {
	switch status {
	case ResReleased, ResConsumed, ResExpired:
	default:
		return 0, badReq("inventory: invalid terminal reservation status %q", status)
	}
	if sourceRefID == "" {
		return 0, badReq("inventory: source_ref_id is required")
	}
	return s.db().CloseReservationsBySource(ctx, tx, sourceType, sourceRefID, status)
}

// LockHardHoldBalancesTx takes the stock_balances rows of one source document's ACTIVE HARD holds
// FOR UPDATE, sorted by product then location (LockBalances' own order), on the caller's
// transaction. It exists for a writer that will move those rows later in the same transaction,
// after drawing a money counter: the order cancel's release hook unwinds these rows after the
// deposit settlement draws the invoices feed counter, and the POS ring takes a stock row before
// that same counter, so the cancel takes its stock rows first or the two can meet head on.
func (s *Service) LockHardHoldBalancesTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID string) error {
	if sourceRefID == "" {
		return badReq("inventory: source_ref_id is required")
	}
	return s.db().LockHardHoldBalancesTx(ctx, tx, sourceType, sourceRefID)
}

// ReleaseTx closes one reservation by id.
func (s *Service) ReleaseTx(ctx context.Context, tx pgx.Tx, reservationID, status string) error {
	switch status {
	case ResReleased, ResConsumed, ResExpired:
	default:
		return badReq("inventory: invalid terminal reservation status %q", status)
	}
	n, err := s.db().CloseReservationByID(ctx, tx, reservationID, status)
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}

// ExpireDueReservations flips every ACTIVE reservation whose expires_at has passed to EXPIRED and
// unwinds the projection for the HARD ones. Returns how many it closed.
//
// It opens its OWN transaction: this is the sweep, called from an operator action today and from the
// jobs runner once the union wires it (the JobFunc signature is named in the report). Doing the read
// and the writes in one transaction is what keeps the projection consistent if the sweep is racing a
// reserve on the same product.
func (s *Service) ExpireDueReservations(ctx context.Context) (int, error) {
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	n, err := s.db().ExpireDueReservations(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	if n > 0 {
		s.log.Info("inventory: expired stock reservations", "count", n)
	}
	return n, nil
}

// ListReservations returns the ACTIVE holds at a branch (optionally one product), newest first.
func (s *Service) ListReservations(ctx context.Context, branchID, productID string, limit int) ([]Reservation, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return s.repo.ListReservations(ctx, branchID, productID, limit)
}

// SetAllocationPolicy upserts a branch's policy. Modes and TTLs are validated here as well as by the
// 0115 CHECKs so a bad value gets a 400 with a readable message instead of a 500 with a constraint name.
func (s *Service) SetAllocationPolicy(ctx context.Context, p AllocationPolicy, actorID string) (AllocationPolicy, error) {
	if p.BranchID == "" {
		return AllocationPolicy{}, badReq("inventory: branch_id is required")
	}
	for _, m := range []string{p.OrderMode, p.QuoteMode} {
		switch m {
		case HoldHard, HoldSoft, HoldNone:
		default:
			return AllocationPolicy{}, badReq("inventory: invalid allocation mode %q (HARD|SOFT|NONE)", m)
		}
	}
	if p.OrderTTLHours < 0 || p.QuoteTTLHours < 0 {
		return AllocationPolicy{}, badReq("inventory: ttl hours must be >= 0")
	}
	return s.repo.UpsertAllocationPolicy(ctx, p, actorID)
}
