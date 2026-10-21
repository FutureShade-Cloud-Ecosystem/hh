package quote

// stock_seam.go, W4 seam 3, quote side: SOFT holds on a live quote, and the at-quote availability
// read. Both are consumer-declared ports with types this module owns; nothing here imports inventory.
//
// - SOFT, AND WHY THAT IS THE WHOLE POINT -
// A quote hold is graded SOFT by the default branch policy, which means it is VISIBLE and it EXPIRES
// but it does NOT reduce available. A soft hold that blocked a cash sale would be worse than no hold
// at all: the quote may never convert, and a counter clerk must not be told "none left" because a
// contractor is thinking about it. A branch that genuinely wants quotes to hold stock sets
// quote_mode=HARD in its allocation policy and this same code starts holding, no change here.
//
// - WHEN THE HOLD IS TAKEN -
// On every quote LINE WRITE, not on quote creation. CreateQuote inserts a header with NO lines
// (edit_repository.go), so there is nothing to hold at that moment, a "reserve on create" would be
// a no-op with a comment claiming otherwise. The holds are therefore SYNCED from the quote's current
// lines whenever those lines change, inside the line write's own transaction.
//
// SYNC (release-all + re-reserve), not incremental adjustment. A per-line diff would have to decide
// what to do about a hold whose product left the quote, a line that was repriced into a different
// UOM, a quantity that moved twice. Each is a place to strand a hold. A sync has one outcome: after
// it commits, this quote's holds ARE its lines.
//
// - EXPIRY COMES FROM THE QUOTE, NOT THE CLOCK -
// A hold expires when the QUOTE does (valid_until), falling back to the branch policy's TTL when the
// quote never expires. A 72-hour hold on a quote valid for three weeks would vanish while the quote
// is still live and the customer still deciding, which is exactly the state the visibility surface
// exists to show.
//
// - SOFT → HARD HAPPENS AT CONVERT, NOT AT ACCEPT, AND HERE IS WHY -
// The brief says "quote ACCEPT converts soft→the order's hard reserve". In THIS codebase ACCEPT and
// CONVERT are two steps: Accept moves the quote SENT→ACCEPTED and creates nothing; Convert creates
// the order (repository.go) behind the DIB-387 credit gate. The order's hard hold is keyed by
// source_ref_id = the ORDER id, which does not exist until Convert. So the conversion is performed
// inside Convert's transaction, where both the release of the soft holds and the raise of the hard
// ones commit with the order row itself.
//
// Doing it at Accept would mean either inventing an order id early or writing a hard hold with no
// order to point at. Both are worse than a one-step-later boundary, and the boundary chosen is the
// one where the order actually comes into existence.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reservation vocabulary this module writes. Values match inventory's 0115 CHECK sets by contract,
// restated here so the constants this module writes are ones this module owns.
const (
	ResTypeQuote = "QUOTE"
	ResTypeOrder = "ORDER"

	ResReleased = "RELEASED"
	ResConsumed = "CONSUMED"

	ResSourceQuote = "quote"
	ResSourceOrder = "order"
)

// StockReserveInput mirrors inventory.ReserveInput field for field, declared here so this module's
// call sites compile against a type it owns. The main.go adapter maps between the two.
type StockReserveInput struct {
	ProductID   string
	BranchID    string
	LocationID  string
	ResType     string
	QtyBase     float64
	SourceType  string
	SourceRefID string
	Reason      string
	CreatedBy   string
	// EnforceAvailable is always FALSE from this module. A quote must be writable for stock the
	// branch does not have, that is half of what quoting is for, and a SOFT hold does not consume
	// availability anyway, so the guard would be enforcing a limit the hold does not impose.
	EnforceAvailable bool
	// ExpiresAt overrides the branch policy TTL. Zero = use the policy.
	ExpiresAt time.Time
}

// StockReserveResult is what a reserve returns. Applied=false means the branch graded QUOTE holds
// NONE and nothing was written, success, not failure: a quote must still save at a branch that has
// switched quote allocation off.
type StockReserveResult struct {
	ReservationID string
	HoldMode      string
	Applied       bool
}

// StockReserver is the consumer-declared reservation port. *inventory.Service satisfies the shape
// through the main.go adapter.
type StockReserver interface {
	ReserveTx(ctx context.Context, tx pgx.Tx, in StockReserveInput) (StockReserveResult, error)
	ReleaseBySourceTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error)
}

// BranchStock is the four-bucket view of one product at one branch, the at-quote availability read.
// Field-for-field inventory.BranchStock; the adapter maps it.
//
// SoftHeldBase is carried and rendered even though it does NOT gate, because a counter clerk asking
// "why does everyone keep quoting this pallet" needs to see the interest that is not a commitment.
type BranchStock struct {
	ProductID     string  `json:"product_id"`
	BranchID      string  `json:"branch_id"`
	BranchCode    string  `json:"branch_code"`
	BranchName    string  `json:"branch_name"`
	OnHandBase    float64 `json:"on_hand_base"`
	ReservedBase  float64 `json:"reserved_base"`
	InTransitBase float64 `json:"in_transit_base"`
	AvailableBase float64 `json:"available_base"`
	SoftHeldBase  float64 `json:"soft_held_base"`
	AvgCostCents  int64   `json:"avg_cost_cents,omitempty"`
}

// BranchPeek is one branch's row in the cross-branch peek: the four buckets and nothing else. It has
// no cost field at all, so no role can be handed a cost through the peek (UX-88, row 2809).
type BranchPeek struct {
	ProductID     string  `json:"product_id"`
	BranchID      string  `json:"branch_id"`
	BranchCode    string  `json:"branch_code"`
	BranchName    string  `json:"branch_name"`
	OnHandBase    float64 `json:"on_hand_base"`
	ReservedBase  float64 `json:"reserved_base"`
	InTransitBase float64 `json:"in_transit_base"`
	AvailableBase float64 `json:"available_base"`
	SoftHeldBase  float64 `json:"soft_held_base"`
}

// AvailabilityReader is the consumer-declared half of inventory's published QuoteAvailabilityReader.
// The provider side compile-asserts *inventory.Service against its own copy (inventory/visibility.go);
// this is the identically-shaped, differently-named consumer copy the convention calls for.
type AvailabilityReader interface {
	AvailabilityAtBranch(ctx context.Context, productID, branchID string) (BranchStock, error)
	AvailabilityPeek(ctx context.Context, orgID, productID string) ([]BranchPeek, error)
}

// WithStockReserver injects the soft-hold port.
func (s *StaffService) WithStockReserver(r StockReserver) *StaffService { s.stock = r; return s }

// WithAvailability injects the at-quote availability port.
func (s *StaffService) WithAvailability(a AvailabilityReader) *StaffService { s.avail = a; return s }

// StockSeamWired / AvailabilitySeamWired report the bindings to the surface, so an unwired deploy
// renders "availability is not being reported" rather than a silent column of zeros, a zero that
// means "we did not ask" and a zero that means "there is none" are not the same answer.
func (s *StaffService) StockSeamWired() bool        { return s.stock != nil }
func (s *StaffService) AvailabilitySeamWired() bool { return s.avail != nil }
