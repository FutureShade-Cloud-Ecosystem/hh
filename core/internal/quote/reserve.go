package quote

// reserve.go, the quote-side reservation lifecycle and the at-quote availability read.
//
//	line write (add / qty / remove) → sync SOFT holds from the quote's current lines
//	Convert (quote → order)          → release the SOFT holds CONSUMED, raise the ORDER's HARD holds
//	Reject (→ LOST) / Expire         → release the SOFT holds RELEASED
//
// Every one of those happens INSIDE the transaction that does the thing it follows. A line write
// that committed and then failed to sync would leave a hold for a quantity nobody quoted; a Convert
// that committed the order and then failed to hard-reserve would produce a confirmed-order-shaped
// document holding nothing. Sharing the transaction is the only way to make either impossible.
//
// See stock_seam.go for the SOFT grading, the sync-not-diff choice, and the ruling on where soft→hard
// actually happens in this codebase.

import (
	"context"
	"strconv"
	"strings"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// reservableLine is the narrow projection the hold sync needs off a quote line.
type reservableLine struct {
	LineNo    int
	ProductID string
	QtyBase   float64
}

// holdStore is the extra persistence the hold sync needs, asserted OFF the injected staffStore (the
// established pattern in this module) so every existing fake keeps compiling.
type holdStore interface {
	// ReservableLinesTx reads a quote's (product, qty_base) projection on the caller's transaction.
	ReservableLinesTx(ctx context.Context, tx pgx.Tx, quoteID string) ([]reservableLine, error)
	// QuoteHoldContextTx reads the branch and the expiry the holds should carry, on the caller's tx.
	QuoteHoldContextTx(ctx context.Context, tx pgx.Tx, quoteID string) (branchID string, validUntil *time.Time, err error)
	// QuoteHolds reads this quote's ACTIVE holds for the surface.
	QuoteHolds(ctx context.Context, quoteID string) ([]QuoteHold, error)
	// LockQuoteTx runs fn under the quote row's write lock, the seam for the explicit resync.
	LockQuoteTx(ctx context.Context, quoteID string, fn func(ctx context.Context, tx pgx.Tx) error) error
}

// QuoteHold is one ACTIVE hold this quote owns, decorated for the surface.
type QuoteHold struct {
	ReservationID string  `json:"reservation_id"`
	ProductID     string  `json:"product_id"`
	ProductName   string  `json:"product_name"`
	BranchID      string  `json:"branch_id"`
	HoldMode      string  `json:"hold_mode"`
	QtyBase       float64 `json:"qty_base"`
	ExpiresAt     string  `json:"expires_at,omitempty"`
}

// syncQuoteHoldsTx re-derives this quote's SOFT holds from its CURRENT lines, on the caller's tx.
//
// It is the hook the guarded line writes call, and it is deliberately TOTAL rather than incremental:
// release everything this quote holds, then reserve what it now has. See stock_seam.go.
//
// A quote at a branch whose policy grades QUOTE holds NONE writes nothing and returns 0 applied.
// That is success, the quote still saves.
func (s *StaffService) syncQuoteHoldsTx(ctx context.Context, tx pgx.Tx, quoteID, actor string) (int, error) {
	if s.stock == nil {
		return 0, nil // seam off: the whole quote-hold feature is inert, loudly (see StockSeamWired)
	}
	hs, ok := s.store.(holdStore)
	if !ok {
		return 0, nil
	}
	if _, err := s.stock.ReleaseBySourceTx(ctx, tx, ResSourceQuote, quoteID, ResReleased); err != nil {
		return 0, err
	}
	branchID, validUntil, err := hs.QuoteHoldContextTx(ctx, tx, quoteID)
	if err != nil {
		return 0, err
	}
	if branchID == "" {
		// A quote with no branch cannot be graded by a branch policy. Rather than reserve against ""
		// (which inventory rejects) the sync writes nothing, the quote is still perfectly valid.
		return 0, nil
	}
	lines, err := hs.ReservableLinesTx(ctx, tx, quoteID)
	if err != nil {
		return 0, err
	}
	// The hold expires WITH the quote. A NULL valid_until means the quote never expires, and the zero
	// time.Time here means "use the branch policy TTL", the two different fallbacks are the reason
	// this is a pointer and not a bare time.
	var expires time.Time
	if validUntil != nil {
		expires = *validUntil
	}
	applied := 0
	for _, ln := range lines {
		if ln.ProductID == "" || ln.QtyBase <= 0 {
			continue // a free-text line has no stock to hold; see order.reserveOrderLinesTx
		}
		res, err := s.stock.ReserveTx(ctx, tx, StockReserveInput{
			ProductID:   ln.ProductID,
			BranchID:    branchID,
			ResType:     ResTypeQuote,
			QtyBase:     ln.QtyBase,
			SourceType:  ResSourceQuote,
			SourceRefID: quoteID,
			Reason:      "live quote",
			CreatedBy:   actor,
			ExpiresAt:   expires,
		})
		if err != nil {
			return applied, err
		}
		if res.Applied {
			applied++
		}
	}
	return applied, nil
}

// ResyncQuoteHolds is the explicit, operator-facing form of the sync (and the one an integration test
// can call directly). It takes the quote row lock so it serializes against a concurrent line write.
func (s *StaffService) ResyncQuoteHolds(ctx context.Context, quoteIDOrNo string) (int, error) {
	caller, quoteID, err := s.resolveQuoteForCaller(ctx, quoteIDOrNo)
	if err != nil {
		return 0, err
	}
	hs, ok := s.store.(holdStore)
	if !ok {
		return 0, httpx.BadRequestf("quote holds are not available on this store")
	}
	var applied int
	err = hs.LockQuoteTx(ctx, quoteID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		applied, err = s.syncQuoteHoldsTx(ctx, tx, quoteID, caller.UserID)
		return err
	})
	return applied, err
}

// ReleaseQuoteHolds closes this quote's SOFT holds with a terminal status. It is the REJECT/EXPIRE
// half of the lifecycle: a quote that is lost or out of date is no longer interest in that stock, and
// leaving the hold behind is what makes a visibility surface stop being believed.
func (s *StaffService) ReleaseQuoteHolds(ctx context.Context, quoteIDOrNo, status string) (int, error) {
	_, quoteID, err := s.resolveQuoteForCaller(ctx, quoteIDOrNo)
	if err != nil {
		return 0, err
	}
	if s.stock == nil {
		return 0, nil
	}
	hs, ok := s.store.(holdStore)
	if !ok {
		return 0, nil
	}
	if status != ResReleased && status != ResConsumed {
		return 0, httpx.BadRequestf("invalid terminal reservation status %q", status)
	}
	var n int
	err = hs.LockQuoteTx(ctx, quoteID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = s.stock.ReleaseBySourceTx(ctx, tx, ResSourceQuote, quoteID, status)
		return err
	})
	return n, err
}

// QuoteHolds returns this quote's ACTIVE holds for the surface.
func (s *StaffService) QuoteHolds(ctx context.Context, quoteIDOrNo string) ([]QuoteHold, error) {
	_, quoteID, err := s.resolveQuoteForCaller(ctx, quoteIDOrNo)
	if err != nil {
		return nil, err
	}
	hs, ok := s.store.(holdStore)
	if !ok {
		return []QuoteHold{}, nil
	}
	return hs.QuoteHolds(ctx, quoteID)
}

// - the at-quote availability read -

// LineAvailability is one quote line graded against the four-bucket stock view at the quote's branch.
type LineAvailability struct {
	LineNo    int     `json:"line_no"`
	ProductID string  `json:"product_id"`
	QtyBase   float64 `json:"qty_base"`
	// Grade is the CHIP: GREEN (available covers the line), THIN (some, not enough), NONE (nothing
	// available), UNKNOWN (no product to grade, a free-text line, or the seam is unwired).
	Grade  string      `json:"grade"`
	Branch BranchStock `json:"branch"`
}

// Availability grades. Three states, not two, because "we have four of the twelve you want" is the
// answer a quote most needs to surface and a boolean cannot express it.
const (
	AvailGreen   = "GREEN"
	AvailThin    = "THIN"
	AvailNone    = "NONE"
	AvailUnknown = "UNKNOWN"
)

// gradeAvailability is the chip rule as a PURE function, so the three-state boundary is testable
// without a database and the UI grades identically to the API.
//
// available <= 0 is NONE even when on-hand is large: stock that is hard-held for someone else is not
// stock this customer can have, and showing green because the yard looks full is precisely the
// oversell DIB-60 exists to stop.
func gradeAvailability(qtyBase, availableBase float64) string {
	switch {
	case qtyBase <= 0:
		return AvailUnknown
	case availableBase <= 0:
		return AvailNone
	case availableBase+0.00005 >= qtyBase:
		return AvailGreen
	default:
		return AvailThin
	}
}

// QuoteAvailability grades every line of a quote against its branch's four-bucket view.
//
// An UNWIRED seam returns UNKNOWN for every line rather than an error: the quote surface must still
// render, and a grey chip that says "not reported" is a better answer than a failed page. The wired
// flag rides in the response so the client can say which it is.
func (s *StaffService) QuoteAvailability(ctx context.Context, quoteIDOrNo string) ([]LineAvailability, bool, error) {
	caller, quoteID, err := s.resolveQuoteForCaller(ctx, quoteIDOrNo)
	if err != nil {
		return nil, false, err
	}
	view, err := s.store.StaffDetail(ctx, quoteID)
	if err != nil {
		return nil, false, err
	}
	// X3: the cost figure on the stock view is cost information, gated by
	// the configuration's cost and margin list like every other cost read.
	// The grades render for every caller; the cost does not (absent on the
	// wire for a caller off the list, never a zero that reads as free).
	seeCost := s.seesCostAndMargin(caller)
	out := make([]LineAvailability, 0, len(view.Lines))
	for _, ln := range view.Lines {
		row := LineAvailability{
			LineNo:    ln.LineNo,
			ProductID: ln.ProductID,
			QtyBase:   parseQtyOrZero(ln.QtyBase),
			Grade:     AvailUnknown,
		}
		if s.avail == nil || ln.ProductID == "" || view.BranchID == "" {
			out = append(out, row)
			continue
		}
		bs, err := s.avail.AvailabilityAtBranch(ctx, ln.ProductID, view.BranchID)
		if err != nil {
			// One product failing to read must not fail the whole quote view. The line grades UNKNOWN,
			// which is honest, and the rest of the quote still renders.
			out = append(out, row)
			continue
		}
		if !seeCost {
			bs.AvgCostCents = 0
		}
		row.Branch = bs
		row.Grade = gradeAvailability(row.QtyBase, bs.AvailableBase)
		out = append(out, row)
	}
	return out, s.avail != nil, nil
}

// CrossBranchPeek is the "who else has it" read behind a THIN or NONE chip: one row per branch of
// the caller's dealer org, every such branch present even when it has none (inventory's own stated
// rule, a missing row reads as a bug rather than as an answer). orgID is the org the middleware
// resolved for the request. The rows carry quantities only: no role is handed a cost through the peek.
func (s *StaffService) CrossBranchPeek(ctx context.Context, orgID, productID string) ([]BranchPeek, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	if !looksLikeUUID(productID) {
		return nil, httpx.BadRequestf("product_id must be a uuid")
	}
	if s.avail == nil {
		return []BranchPeek{}, nil
	}
	if orgID == "" {
		return nil, httpx.BadRequestf("the caller's org is required")
	}
	return s.avail.AvailabilityPeek(ctx, orgID, productID)
}

// - plumbing -

// resolveQuoteForCaller applies the module's standard authority ladder, authenticated staff, then a
// BRANCH-SCOPED resolution of the id-or-quote_no, so a cross-branch '#Q-' answers not-found rather
// than confirming another branch's quote exists via the number space (the B2-4 rule this module
// already applies in authorizeQuoteEdit).
func (s *StaffService) resolveQuoteForCaller(ctx context.Context, quoteIDOrNo string) (identity.StaffContext, string, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, "", err
	}
	if strings.TrimSpace(quoteIDOrNo) == "" {
		return identity.StaffContext{}, "", httpx.BadRequestf("quote id is required")
	}
	global, branches := callerBranchScope(caller)
	quoteID, err := s.store.ResolveQuoteID(ctx, quoteIDOrNo, global, branches)
	if err != nil {
		return identity.StaffContext{}, "", err
	}
	_, branchID, _, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return identity.StaffContext{}, "", err
	}
	if !caller.CanAccessBranch(branchID) {
		return identity.StaffContext{}, "", ErrNotFound
	}
	return caller, quoteID, nil
}

// parseQtyOrZero reads a DECIMAL-as-string quantity coming back from the database.
func parseQtyOrZero(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

// holdSyncHook builds the closure the guarded line writes run inside their own transaction. It
// returns nil when the reservation seam is unwired, so the write path carries no hook at all rather
// than a hook that does nothing, the difference matters when reading a stack trace.
func (s *StaffService) holdSyncHook(quoteID, actor string) func(context.Context, pgx.Tx) error {
	if s.stock == nil {
		return nil
	}
	return func(ctx context.Context, tx pgx.Tx) error {
		_, err := s.syncQuoteHoldsTx(ctx, tx, quoteID, actor)
		return err
	}
}
