// edit.go, DIB-56 staff quote builder (bundle W10-A). This is the FIRST write path for quotes in the
// codebase: before it, `quotes`/`quote_lines` (0008) had NO creation writer in Go at all (every
// `INSERT INTO quotes` in the tree is a seed or a test fixture) and no DRAFT→SENT edge, so a quote could
// only be read, accepted, rejected or converted, never built.
//
//	POST   /api/v1/quotes                        → create a quote
//	POST   /api/v1/quotes/{id}/lines             → add a line
//	PATCH  /api/v1/quotes/{id}/lines/{lineNo}    → change a line's quantity
//	DELETE /api/v1/quotes/{id}/lines/{lineNo}    → remove a line
//	PATCH  /api/v1/quotes/{id}                   → change valid_until
//
// This mirrors the DIB-175 order-edit form deliberately, same matrix-as-data, same
// lock→guard→mutate→recompute→record→commit sequence, same atomic audit spine (RecordTx on the caller's
// tx, seq-ordered reads), because applying a proven form is cheaper and safer than inventing one. Where
// it DIVERGES from that form, it is on purpose, and each divergence is a ratified ruling:
//
//  1. STAFF-gated, not manager-gated (R1). An order line edit moves money the moment it lands: the order
//     may carry a finalized invoice, assessed tax and GL postings. A quote moves NO money until Convert,
//     so there is nothing for a manager gate to protect. quoteEditRule therefore has no managerOnly axis
//     and no movesMoney axis, the whole money-predicate apparatus (cancel_seam, tax_cents == 0) that
//     guard 3 gives the order path has no counterpart here, because there is no money to guard.
//
//  2. Frozen at ACCEPTED (R2). The order matrix opens at DRAFT and closes as stock moves. The quote
//     matrix closes at ACCEPTANCE: the customer agreed to those numbers, and silently changing them
//     afterwards is a trust violation, not a data-integrity one.
//
//  3. EXPIRY is a guard (D2, ratified 2026-07-30). `valid_until` has been stored and displayed since
//     0008 but never enforced by anything, no code has ever written EXPIRED, so a SENT quote whose
//     prices lapsed months ago is still editable and still acceptable today. It is now checked at guard
//     time on every edit and on acceptance. Computed, never stored: no EXPIRED status is written and no
//     historical row is backfilled, so a quote's stored status keeps meaning what it always meant.
//     A NULL valid_until means NEVER EXPIRES, not "expired" (D2a); the naive reading would freeze every
//     quote ever created without an expiry date, which is most of them.
package quote

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/jackc/pgx/v5"
)

// - errors -

// ErrUnauthorized, no authenticated staff caller. Mapped to 401.
var ErrUnauthorized = errors.New("quote: unauthenticated")

// ErrForbidden, the caller is inactive, or reaches a branch this quote does not belong to. Both
// answer a FLAT 403 with no detail, so a cross-branch probe learns nothing beyond "not yours".
var ErrForbidden = errors.New("quote: forbidden")

// NotEditableError is returned when a field is not editable in the quote's CURRENT status. It carries
// the status so the 409 can NAME it, the acceptance criterion is that a blocked state answers 409 with
// the status named, so a caller never has to guess why.
type NotEditableError struct {
	Field  QuoteEditField
	Status string
}

func (e *NotEditableError) Error() string {
	return fmt.Sprintf("quote: %s cannot be edited while the quote is %s", e.Field.describe(), e.Status)
}

// QuoteExpiredError is returned when an edit or an acceptance is attempted on a quote whose
// `valid_until` has passed (D2).
//
// The message is deliberately NOT a bare "expired": it names the date the quote lapsed AND the way
// forward, because the person hitting it is usually a customer on a share link with no other channel to
// ask. A refusal that does not say what to do next is a dead end (the F1/F2 lesson from this week).
type QuoteExpiredError struct {
	ValidUntil time.Time
}

func (e *QuoteExpiredError) Error() string {
	return fmt.Sprintf("quote: this quote expired on %s, request an updated quote",
		e.ValidUntil.Format(dateLayout))
}

// ErrLineNotFound, no such line_no on this quote. Mapped to 404.
var ErrLineNotFound = errors.New("quote: line not found")

// ErrEditNotConfigured, the store or the pricing/inventory/customer seams needed to price a quote line
// are not wired. Fails closed (503): an unpriceable line must never be persisted at a stale or zero
// price. main.go asserts the wiring at boot, so this is unreachable in a correct deploy.
var ErrEditNotConfigured = errors.New("quote: edit seams not configured")

// - the ratified edit-state matrix -

// QuoteEditField is a staff edit COMMAND (what the caller asked to do). As on the order side it is
// deliberately distinct from the audit `field` vocabulary (the COLUMN names that actually moved): one
// qty command can move qty_entered, qty_base, unit_price_cents and line_total_cents at once.
type QuoteEditField string

const (
	FieldQuoteLineQty    QuoteEditField = "line_qty"
	FieldQuoteLineAdd    QuoteEditField = "line_add"
	FieldQuoteLineRemove QuoteEditField = "line_remove"
	FieldQuoteValidUntil QuoteEditField = "valid_until"
)

// describe renders a QuoteEditField for the 409 message.
func (f QuoteEditField) describe() string {
	switch f {
	case FieldQuoteLineQty:
		return "line quantity"
	case FieldQuoteLineAdd, FieldQuoteLineRemove:
		return "quote lines"
	case FieldQuoteValidUntil:
		return "the expiry date"
	default:
		return string(f)
	}
}

// quoteEditRule is the legality of one edit command: the set of statuses it may be applied FROM.
//
// ONE axis, where the order side's editRule has three. There is no managerOnly (R1: a quote is
// staff-editable because it moves no money) and no movesMoney (there is no money attached to guard, so
// no predicate to run). Keeping the struct rather than a bare map[string]bool is deliberate: it is the
// extension point if a future ruling adds an axis, and it keeps the shape recognisably the order form.
type quoteEditRule struct {
	from map[string]bool
}

// quoteFieldOrder is the deterministic order edit fields are reported in (editable_fields on the quote
// read) and iterated over for matrix coverage in the tests. Go map iteration is randomised; this is the
// fix, exactly as editFieldOrder is on the order side.
var quoteFieldOrder = []QuoteEditField{
	FieldQuoteLineQty, FieldQuoteLineAdd, FieldQuoteLineRemove, FieldQuoteValidUntil,
}

// quoteEditRules is the RATIFIED quote edit-state matrix (R2) and the single source of truth for it. The
// write path, the server-computed editable_fields on the quote read, and the matrix tests all read THIS
// map, so the client can never render a control the server would refuse, and a matrix change needs no
// client redeploy.
//
//	                    DRAFT SENT ACCEPTED CONVERTED EXPIRED LOST
//	line qty/add/remove   ✅    ✅     ❌        ❌        ❌     ❌
//	valid_until           ✅    ✅     ❌        ❌        ❌     ❌
//
// Editable in DRAFT and SENT; frozen everywhere else. ACCEPTED is the interesting one: the quote is
// still "live" in every operational sense, but the customer has agreed to those numbers and changing
// them underneath that agreement is a trust violation. CONVERTED is frozen because the order is now the
// live document. LOST/EXPIRED are terminal.
//
// NOTE on vocabulary (D3): R2 named the terminal-rejection state "REJECTED". This schema has no such
// value, the 0008 CHECK allows DRAFT|SENT|ACCEPTED|CONVERTED|EXPIRED|LOST and Reject() writes 'LOST'.
// The ruling is honoured under this schema's name.
var quoteEditRules = map[QuoteEditField]quoteEditRule{
	FieldQuoteLineQty:    {from: quoteStatusSet("DRAFT", "SENT")},
	FieldQuoteLineAdd:    {from: quoteStatusSet("DRAFT", "SENT")},
	FieldQuoteLineRemove: {from: quoteStatusSet("DRAFT", "SENT")},
	FieldQuoteValidUntil: {from: quoteStatusSet("DRAFT", "SENT")},
}

// quoteStatusSet builds a status set from a varargs list (mirrors order.statusSet, which is private to
// that package).
func quoteStatusSet(ss ...string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func (r quoteEditRule) fromList() []string {
	out := make([]string, 0, len(r.from))
	for s := range r.from {
		out = append(out, s)
	}
	return out
}

// - bounds -

// maxQuoteQty bounds an edited quantity, applied to BOTH the entered qty and the derived base qty.
//
// This is not an arbitrary business cap: it is what keeps the audit trail truthful. Quantities ride into
// the audit `before`/`after` fields as DECIMAL strings, and the audit boundary redacts any string
// containing 7+ consecutive digits (audit.redactCleartext, a PAN/SIN/registry-number defence). A qty of
// 1,000,000 would therefore land in the trail as "[redacted]", silently destroying the before→after
// reconstruction this feature exists to provide. Bounding below the threshold means a legitimate value
// can never be redacted. The BASE qty is bounded too because a UOM factor multiplies it (999,999 pallets
// × 100 would clear the threshold even though the entered qty does not). Same reasoning, same constant
// as order.maxEditQty, if one moves, both must.
const maxQuoteQty = 999999

// maxQuoteDescription bounds the free-text description on an added line. The description is NOT
// auditable content (it never enters the change trail, see the audit allowlist note in
// audit/service.go), it is only persisted on the line.
const maxQuoteDescription = 500

const dateLayout = "2006-01-02"

// - models -

// QuoteLineSnapshot is the pre- or post-image of one quote line, everything the change trail needs to
// reconstruct what a line looked like before and after an edit.
type QuoteLineSnapshot struct {
	LineNo         int
	ProductID      string
	QtyEntered     string
	QtyBase        string
	UnitPriceCents int64
	LineTotalCents int64
}

// QuoteTotals is the recomputed quote money after an edit. tax_cents is deliberately absent: an edit
// never writes it (M5 owns tax), exactly as on the order side.
type QuoteTotals struct {
	SubtotalCents int64
	TotalCents    int64
}

// QuoteViewLine is one line as the STAFF quote surface renders it.
type QuoteViewLine struct {
	LineNo         int    `json:"line_no"`
	ProductID      string `json:"product_id"`
	Description    string `json:"description"`
	QtyEntered     string `json:"qty_entered"`
	UOMEntered     string `json:"uom_entered"`
	QtyBase        string `json:"qty_base"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	PriceUOM       string `json:"price_uom"`
	LineTotalCents int64  `json:"line_total_cents"`
	// RulePriceBelowFloor, for callers on the cost and margin list (X3):
	// the line's rule price itself sits below the margin floor. Pointer so
	// absent means "not asked" (a caller off the list, or no arbiter), never
	// a confident false.
	RulePriceBelowFloor *bool `json:"rule_price_below_floor,omitempty"`
	// FloorClearedBy names who cleared a below floor price on this line
	// (the clearing stamped by the write that went through); "" when the
	// line was never cleared.
	FloorClearedBy string `json:"floor_cleared_by,omitempty"`
}

// quoteEditWrite is one line mutation's payload, handed to the guarded write.
type quoteEditWrite struct {
	QuoteID        string
	LineNo         int
	AllowedFrom    []string
	ProductID      string
	Description    string
	QtyEntered     string
	UOMEntered     string
	QtyBase        string
	UnitPriceCents int64
	PriceUOM       string
	LineTotalCents int64
	ActorID        string
}

// - seams -

// PriceResolver is the today's-rates pricing seam (satisfied by *pricing.Service). Identical shape to
// order.PriceResolver, the narrow one-method interface, so a fake stays cheap.
type PriceResolver interface {
	ResolvePrice(ctx context.Context, in pricing.ResolveInput) (pricing.Breakdown, error)
}

// CostBasisReader supplies the cost the resolver derives a retail anchor from when a product has no
// explicit list price (satisfied by *inventory.Service).
type CostBasisReader interface {
	CurrentCostCents(ctx context.Context, productID, branchID string) (int64, bool, error)
}

// CustomerTypeResolver resolves a quote's customer pricing tier (CONTRACTOR/RETAIL/…), so a line is
// priced at the rate that account actually gets. Reached through a narrow seam so the quote module never
// imports the customer package.
type CustomerTypeResolver interface {
	CustomerType(ctx context.Context, customerID string) (string, error)
}

// quoteEditHooks is the record closure the service threads into a guarded write so the audit trail runs
// INSIDE the same transaction + quote-row lock as the mutation (R3, inheriting DIB-175's B2-1).
//
// There is no `precheck` counterpart to the order side's: that hook exists to run the money predicate
// under the lock, and a quote has no money attached to predicate on (R1). The EXPIRY guard, which is the
// closest thing this path has to a precheck, is not a hook, it is evaluated inline under the lock from
// the same locked read that yields the status, so it cannot drift out of sync with it.
type quoteEditHooks struct {
	// record runs AFTER the mutation + totals recompute and BEFORE commit, on the SAME tx. A non-nil
	// error ROLLS BACK the whole edit: the trail is atomic with the write. before is zero for an add;
	// after is zero for a remove.
	record func(ctx context.Context, tx pgx.Tx, before, after QuoteLineSnapshot, totals QuoteTotals) error
	// holds runs AFTER record and BEFORE commit, on the SAME tx: it re-derives this quote's SOFT
	// stock holds from the lines the mutation just produced (W5 seam 3, reserve.go). A non-nil error
	// ROLLS BACK the whole edit, for the same reason a failed audit write does, a committed line
	// change whose hold never landed is a quote holding a quantity nobody quoted, and the visibility
	// surface stops being believed the first time anyone notices. nil = the reservation seam is off.
	holds func(ctx context.Context, tx pgx.Tx) error
}

// quoteDateHooks is the record-only hook for the valid_until write.
type quoteDateHooks struct {
	record func(ctx context.Context, tx pgx.Tx, before, after string) error
}

// quoteDiscountHooks is the record closure the discount write threads into
// its guarded write (plan 4.2): the discount before and after and the line
// total, on the write's own transaction, the same trail the other line
// writes use. The percents and the line's net total are passed explicitly:
// the discount is the one column a QuoteLineSnapshot does not carry, and a
// half-populated snapshot invites a confident wrong read.
type quoteDiscountHooks struct {
	record func(ctx context.Context, tx pgx.Tx, lineNo, beforeDisc, afterDisc int, beforeTotal, afterTotal int64, totals QuoteTotals) error
}

// - expiry (D2) -

// quoteExpired reports whether a quote's validity window has closed.
//
// A NULL valid_until (nil here) means the quote NEVER EXPIRES, it is not, and must never be read as,
// "expired" (D2a). Most quotes in the system carry no expiry at all: 0008 made the column nullable, no
// writer has ever populated it outside seeds, and the naive `validUntil.Before(now)` on a zero time.Time
// would freeze every one of them. That is the single most likely way to get this ruling wrong, so it is
// the first thing the tests pin.
//
// The comparison is date-grained, not instant-grained: `valid_until` is a DATE, and a quote valid until
// the 30th is good for the whole of the 30th. Expiry begins the day AFTER.
func quoteExpired(validUntil *time.Time, now time.Time) bool {
	if validUntil == nil {
		return false // NULL = never expires (D2a)
	}
	vu := validUntil.UTC()
	end := time.Date(vu.Year(), vu.Month(), vu.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	return !now.UTC().Before(end)
}

// assertNotExpired is the write-path form: it throws so an edit or acceptance is refused with the date
// named. The read path (editableFields) uses quoteExpired directly and merely omits the offer, the
// strict/soft pairing the order module uses for its money predicate.
func assertNotExpired(validUntil *time.Time, now time.Time) error {
	// The nil case is handled HERE as well as in quoteExpired, deliberately. The dereference below is
	// only safe because quoteExpired returns false for nil, and resting a nil-dereference on the
	// behaviour of a different function is exactly the coupling that breaks the first time someone
	// edits that function. (A mutation test that flipped quoteExpired's nil branch turned this into a
	// segfault, which is how it was found.)
	if validUntil == nil {
		return nil // NULL = never expires (D2a)
	}
	if quoteExpired(validUntil, now) {
		return &QuoteExpiredError{ValidUntil: *validUntil}
	}
	return nil
}

// - authority -

// authorizeQuoteEdit runs the authority ladder shared by every quote edit command, in the same order the
// order module's authorizeEdit uses, so the HTTP mapping is deterministic and a cross-branch quote is
// never distinguishable from a nonexistent one:
//
//	authenticated caller           → ErrUnauthorized  (401)
//	ACTIVE caller                  → ErrForbidden     (403)
//	known edit field               → BadRequest       (400)
//	quote exists                   → ErrNotFound      (404)  ← resolved BEFORE any authz rejection
//	caller reaches the quote branch → ErrForbidden    (403)
//	caller holds Work+ on quote-desk, and owns the quote unless it holds Manage
//	                               → NotQuoteOwnerError (403, the way forward named) / ErrForbidden
//	field editable in this status  → NotEditableError (409, status named)
//	quote not expired              → QuoteExpiredError(409, date + path forward)
//
// The ownership rung is plan 3.3 (defect D4): a caller at Work edits only its
// own quotes, a caller at Manage edits any quote in its branch. "Own" is the
// quote's salesperson when one is set, else its creator. A Work caller still
// reads any quote in its branch and converts an accepted one: only the edit
// ladder changed. The level comes from the dealer's role configuration
// (the caller's role mapped through identity.CanonicalForERPCode); a caller
// whose level is under Work edits nothing, and a service with no
// configuration wired fails closed.
//
// It returns the caller, the rule, and the RESOLVED quote id. The status and expiry it checked are
// deliberately not returned: both are ADVISORY only, because the authoritative re-check happens inside
// the guarded write under the quote row lock, a concurrent acceptance must not be able to slip an edit
// into a now-ACCEPTED quote (the TOCTOU class DIB-191 closed for order status).
func (s *StaffService) authorizeQuoteEdit(ctx context.Context, quoteID string, field QuoteEditField) (identity.StaffContext, quoteEditRule, string, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, quoteEditRule{}, "", err
	}
	if caller.Status != identity.StatusActive {
		return identity.StaffContext{}, quoteEditRule{}, "", ErrForbidden
	}
	rule, ok := quoteEditRules[field]
	if !ok {
		return identity.StaffContext{}, quoteEditRule{}, "", httpx.BadRequestf("unknown editable field %q", field)
	}
	if quoteID == "" {
		return identity.StaffContext{}, quoteEditRule{}, "", httpx.BadRequestf("quote id is required")
	}
	// Accept the visible quote_no ("#Q-4"/"Q-4") as an alternate key, resolved to the real id ONCE up
	// front, so the branch check, the guarded write and the change trail all key off the same resolved
	// id. Branch-scoped, so a cross-branch #Q answers 404 rather than becoming an existence oracle
	// (the B2-4 lesson, applied here from the start rather than as a correction round).
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err = s.store.ResolveQuoteID(ctx, quoteID, rGlobal, rBranches)
	if err != nil {
		return identity.StaffContext{}, quoteEditRule{}, "", err
	}
	status, branchID, validUntil, ownerID, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return identity.StaffContext{}, quoteEditRule{}, "", err // ErrNotFound, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return identity.StaffContext{}, quoteEditRule{}, "", ErrForbidden
	}
	// The ownership rung (plan 3.3). Level first: under Work edits nothing,
	// a flat 403 like the branch rung.
	level := s.quoteDeskLevel(caller)
	if !level.AtLeast(roleconfig.LevelWork) {
		return identity.StaffContext{}, quoteEditRule{}, "", ErrForbidden
	}
	if level == roleconfig.LevelWork && !s.ownsQuote(caller, ownerID) {
		return identity.StaffContext{}, quoteEditRule{}, "", &NotQuoteOwnerError{OwnerName: s.ownerName(ctx, ownerID)}
	}
	if !rule.from[status] {
		return identity.StaffContext{}, quoteEditRule{}, quoteID, &NotEditableError{Field: field, Status: status}
	}
	if err := assertNotExpired(validUntil, s.now()); err != nil {
		return identity.StaffContext{}, quoteEditRule{}, quoteID, err
	}
	return caller, rule, quoteID, nil
}

// quoteDeskLevel answers the caller's configured level on the Quote Desk,
// mapping the caller's ERP role code through the bridge
// (identity.CanonicalForERPCode) onto the configuration's vocabulary. An
// unmapped or unknown role, or a service with no configuration, answers
// none: the gated paths fail closed, never permissively.
func (s *StaffService) quoteDeskLevel(c identity.StaffContext) roleconfig.Level {
	if s.roles == nil || c.RoleCode == "" {
		return roleconfig.LevelNone
	}
	canonical, ok := identity.CanonicalForERPCode(c.RoleCode)
	if !ok {
		return roleconfig.LevelNone
	}
	level, err := s.roles.Level(canonical, "quote-desk")
	if err != nil {
		return roleconfig.LevelNone
	}
	return level
}

// ownsQuote answers whether the caller owns the quote whose owner is ownerID
// (the salesperson when one is set, else the creator, plan 3.3). An
// ownerless quote (no salesperson, no creator, the pre-ownership seeds) is
// owned by nobody, so a Work caller cannot edit it: the way forward is the
// branch's Sales manager, exactly as for anyone else's quote.
func (s *StaffService) ownsQuote(c identity.StaffContext, ownerID string) bool {
	return ownerID != "" && c.UserID == ownerID
}

// NotQuoteOwnerError is the refusal a Work caller gets editing a quote it
// does not own. Typed so the message can name the way forward: the quote's
// owner or the branch's Sales manager. Mapped to 403.
// NotQuoteOwnerError names the quote's owner when the store can resolve
// it, so the caller is sent to a person and not to a role (plan 3.4).
type NotQuoteOwnerError struct{ OwnerName string }

func (e *NotQuoteOwnerError) Error() string {
	if e.OwnerName == "" {
		return "quote: this quote belongs to another salesperson, ask the branch's Sales manager to take it over"
	}
	return fmt.Sprintf("quote: this quote belongs to %s, ask the branch's Sales manager to take it over", e.OwnerName)
}

// callerBranchScope projects a caller onto the branch filter ResolveQuoteID needs: a GLOBAL caller
// reaches every branch (global=true, no ids); a scoped caller reaches only its grants. Mirrors
// order.callerBranchScope, which is private to that package.
func callerBranchScope(c identity.StaffContext) (global bool, ids []string) {
	if c.Scope == identity.ScopeGlobal {
		return true, nil
	}
	ids = make([]string, 0, len(c.Branches))
	for b := range c.Branches {
		ids = append(ids, b)
	}
	return false, ids
}

// nameQuoteField completes a *NotEditableError raised by the guarded write. The repository re-evaluates
// the status guard under the row lock but only knows the STATUS it found, which command was attempted
// is the service's knowledge, so the field is filled in here. Any other error passes through untouched.
func nameQuoteField(err error, field QuoteEditField) error {
	var ne *NotEditableError
	if errors.As(err, &ne) && ne.Field == "" {
		ne.Field = field
	}
	return err
}

// - pricing -

// priceLine resolves the pre-tax price trio for (product, qty, uom) at today's rates through the SAME
// pricing seam the POS ring, the order edit and the portal reorder use. The quote module does not own
// UOM factor math or price derivation, so it asks pricing rather than reimplementing either.
//
// A line at its RULE PRICE is accepted wherever the floor sits (plan 4.1, defect D1, CP0-3 answer A):
// the price is the dealer's own rule, the salesperson has nothing to ask for, and the till prices the
// same line the same way. The resolver's margin and floor figures are KEPT on the returned breakdown,
// so the write's caller can log the below floor rule price for the pricing editors; the floor question
// itself is asked only by the two writes that put a line below its rule price (floor.go).
//
// A nil seam FAILS CLOSED (ErrEditNotConfigured → 503): quoting a line at a stale or zero price would be
// worse than refusing the edit.
func (s *StaffService) priceLine(ctx context.Context, customerID, branchID, productID, uom string, qty float64) (pricing.LinePrice, error) {
	if s.pricer == nil || s.inv == nil || s.custType == nil {
		return pricing.LinePrice{}, ErrEditNotConfigured
	}
	custType, err := s.custType.CustomerType(ctx, customerID)
	if err != nil {
		return pricing.LinePrice{}, fmt.Errorf("resolve customer pricing tier: %w", err)
	}
	cost, _, err := s.inv.CurrentCostCents(ctx, productID, branchID)
	if err != nil {
		return pricing.LinePrice{}, fmt.Errorf("cost basis: %w", err)
	}
	// ResolvePrice (not DeriveLinePrice) to keep PriceResolver the narrow one-method interface, the
	// Breakdown already carries the whole trio, so the projection below IS DeriveLinePrice's body.
	bd, err := s.pricer.ResolvePrice(ctx, pricing.ResolveInput{
		CustomerID: customerID, CustomerType: custType,
		ProductID: productID, BranchID: branchID,
		QtyEntered: qty, UOMEntered: uom, CostCents: cost,
	})
	if err != nil {
		return pricing.LinePrice{}, httpx.BadRequestf("cannot price this line: %v", err)
	}
	if bd.QtyBase > maxQuoteQty {
		return pricing.LinePrice{}, httpx.BadRequestf(
			"resulting base quantity exceeds the %d maximum for a quote line", maxQuoteQty)
	}
	return pricing.LinePrice{
		UnitPriceCents: bd.FinalCents,
		PriceUOM:       bd.PriceUOM,
		LineTotalCents: bd.LineTotalCents,
		QtyBase:        bd.QtyBase,
		Breakdown:      bd,
	}, nil
}

// - quantity helpers -

// parseQuoteQty validates an entered quantity: a positive decimal, bounded by maxQuoteQty. Quantities
// stay STRINGS on the wire and in storage (house rule: qty is DECIMAL, never float); the float is only
// what the pricing resolver's input shape takes.
func parseQuoteQty(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, httpx.BadRequestf("qty_entered is required")
	}
	q, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, httpx.BadRequestf("qty_entered must be a decimal quantity")
	}
	if q <= 0 {
		// A zero/negative quantity is a removal, not an edit, keep the two commands distinct so the
		// change trail says which one the operator actually performed.
		return 0, httpx.BadRequestf("qty_entered must be greater than zero, remove the line instead")
	}
	if q > maxQuoteQty {
		return 0, httpx.BadRequestf("qty_entered exceeds the %d maximum for a quote line", maxQuoteQty)
	}
	return q, nil
}

// formatQty renders a resolved quantity at DECIMAL(19,4) precision, so what we persist and what we
// record on the trail are byte-identical to what the column stores.
func formatQty(q float64) string { return strconv.FormatFloat(q, 'f', 4, 64) }

// parseValidUntil validates the expiry date on create/edit. Empty clears it (→ never expires, D2a).
func parseValidUntil(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if _, err := time.Parse(dateLayout, s); err != nil {
		return "", httpx.BadRequestf("valid_until must be a date in YYYY-MM-DD form")
	}
	return s, nil
}

// - the change trail -

// recordLineChange emits one QUOTE_CHANGED audit row per COLUMN that actually moved on a line edit, plus
// the quote-level totals, all on the caller's transaction `q` (R3), so a recorder failure aborts the
// whole edit rather than committing a mutation with no trail. Every row shares the request id
// (audit.Service stamps it from context), so a multi-column edit correlates back into one operator
// action on the read side without a dedicated table or a correlation column.
//
// Emitting per column, rather than one row per command, is what makes the trail reconstructable: a qty
// edit can also move unit_price_cents (quantity-break pricing) and line_total_cents, and a reader must
// be able to see each of those transitions rather than infer them.
//
// The audit `Extra` shape is {field, line_no, before, after}, the same generic shape DIB-175 allowlisted
// (audit/service.go), so this feature needs NO new allowlist keys.
func (s *StaffService) recordLineChange(ctx context.Context, q audit.Execer, actor, quoteID string, cmd QuoteEditField, before, after QuoteLineSnapshot, totals QuoteTotals) error {
	if s.auditTx == nil {
		return nil // no transactional recorder wired (a fake without RecordTx), degenerate, no trail
	}
	lineNo := after.LineNo
	if cmd == FieldQuoteLineRemove {
		lineNo = before.LineNo
	}
	var emits []func() error
	str := func(field, b, a string) func() error {
		return func() error { return s.recordFieldChange(ctx, q, actor, quoteID, lineNo, field, b, a) }
	}
	num := func(field string, b, a *int64) func() error {
		return func() error { return s.recordIntChange(ctx, q, actor, quoteID, lineNo, field, b, a) }
	}
	switch cmd {
	case FieldQuoteLineAdd:
		// A creation has no pre-image; record WHAT was added so the trail names the product and qty.
		emits = append(emits,
			str("line_added", "", after.ProductID),
			str("qty_entered", "", after.QtyEntered),
			str("qty_base", "", after.QtyBase),
			num("unit_price_cents", nil, &after.UnitPriceCents),
			num("line_total_cents", nil, &after.LineTotalCents))
	case FieldQuoteLineRemove:
		// The pre-image is the ONLY record of what was removed, the row no longer exists.
		emits = append(emits,
			str("line_removed", before.ProductID, ""),
			str("qty_base", before.QtyBase, ""),
			num("line_total_cents", &before.LineTotalCents, nil))
	default:
		emits = append(emits,
			str("qty_entered", before.QtyEntered, after.QtyEntered),
			str("qty_base", before.QtyBase, after.QtyBase),
			num("unit_price_cents", &before.UnitPriceCents, &after.UnitPriceCents),
			num("line_total_cents", &before.LineTotalCents, &after.LineTotalCents))
	}
	// The quote-level money that every line edit moves. Recorded against line 0 (the header).
	subtotal, total := totals.SubtotalCents, totals.TotalCents
	emits = append(emits,
		func() error { return s.recordIntChange(ctx, q, actor, quoteID, 0, "subtotal_cents", nil, &subtotal) },
		func() error { return s.recordIntChange(ctx, q, actor, quoteID, 0, "total_cents", nil, &total) })
	for _, emit := range emits {
		if err := emit(); err != nil {
			return fmt.Errorf("quote-edit audit: %w", err)
		}
	}
	return nil
}

// recordFieldChange writes one QUOTE_CHANGED row for a string-valued column on the caller's tx. A no-op
// change writes nothing (the trail records transitions, not attempts) and is not an error.
func (s *StaffService) recordFieldChange(ctx context.Context, q audit.Execer, actor, quoteID string, lineNo int, field, before, after string) error {
	if s.auditTx == nil || before == after {
		return nil
	}
	extra := map[string]any{"field": field, "line_no": lineNo}
	if before != "" {
		extra["before"] = before
	}
	if after != "" {
		extra["after"] = after
	}
	return s.auditTx.RecordTx(ctx, q, audit.SensitiveAccess{
		Action: audit.ActQuoteChanged, EntityType: "quote", EntityID: quoteID,
		ActorID: actor, Purpose: "STAFF_QUOTE_EDIT", Reason: field,
		Extra: extra,
	})
}

// recordIntChange writes one QUOTE_CHANGED row for an integer (money) column on the caller's tx. Money
// rides as int64 precisely because a non-string allowlisted value bypasses the audit boundary's
// cleartext redaction, a cents figure can never be mistaken for an identifier and redacted away. nil
// means "no such image" (a creation has no before; a removal has no after).
func (s *StaffService) recordIntChange(ctx context.Context, q audit.Execer, actor, quoteID string, lineNo int, field string, before, after *int64) error {
	if s.auditTx == nil {
		return nil
	}
	if before != nil && after != nil && *before == *after {
		return nil // unchanged, no row
	}
	extra := map[string]any{"field": field, "line_no": lineNo}
	if before != nil {
		extra["before"] = *before
	}
	if after != nil {
		extra["after"] = *after
	}
	return s.auditTx.RecordTx(ctx, q, audit.SensitiveAccess{
		Action: audit.ActQuoteChanged, EntityType: "quote", EntityID: quoteID,
		ActorID: actor, Purpose: "STAFF_QUOTE_EDIT", Reason: field,
		Extra: extra,
	})
}

// - read-side matrix projection -

// editableFields computes which fields a caller WITH edit authority may edit on a quote in THIS status:
// the ratified matrix ∧ the expiry guard. editableFieldsFor folds the ownership answer in (plan 3.3).
//
// Deliberately SOFTER than the write path, mirroring canOfferCancel/editableFields on the order side: an
// expired or frozen quote simply OFFERS NOTHING, none of it may fail the quote read. The 409 belongs
// exclusively to an actual edit attempt.
//
// Since plan 3.3 the caller's authority contributes the ownership axis, and
// the axis is the WRITE LADDER's own: below Work edits nothing, a Work
// caller edits only its own quotes, and a Manage caller edits any quote in
// its branch. A Sales manager reading another salesperson's quote is
// therefore offered the fields, because a button that appears must be a
// button that works.
func (s *StaffService) editableFieldsFor(status string, validUntil *time.Time, level roleconfig.Level, owns bool) []string {
	out := make([]string, 0, len(quoteFieldOrder))
	if quoteExpired(validUntil, s.now()) {
		return out // an expired quote offers nothing (D2), [] not null
	}
	if !level.AtLeast(roleconfig.LevelWork) || (level == roleconfig.LevelWork && !owns) {
		return out // a caller the write ladder would refuse offers nothing, [] not null
	}
	for _, f := range quoteFieldOrder {
		if !quoteEditRules[f].from[status] {
			continue
		}
		out = append(out, string(f))
	}
	return out
}
