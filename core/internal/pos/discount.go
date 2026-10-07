package pos

// discount.go, the counter's manual discount (row 1047 item 3). At the Dibbits counter a
// salesperson gives a discount only after a manager says yes in person, and the team keeps that
// verbal: no approval queue, the customer is standing there. What the till owes instead is the
// RECORD: every manual discount names the manager who approved it, so it is auditable.
//
// The rules, in one place:
//
//   - a discount is a percent (basis points) or an amount, on one line or on the whole ticket,
//     and always carries approved_by, a staff id;
//   - the approver must be a manager who may clear a price at the sale's branch: the role must be
//     on the dealer's own-branch clearing list read from roleconfig (the list the quote side's
//     floor clearing reads, never a list written here), and the approver's grants must reach the
//     sale's branch;
//   - the discount comes off the line total (a ticket discount off the subtotal after the line
//     discounts, shared over the lines by largest remainder), so every stored line total is what
//     was charged and a return refunds what was paid with no special case;
//   - the till still rings at rule and tier prices with no margin floor (row 481): a discount is a
//     manager's word on top of that price, not a floor check.
//
// The pricing path and the preview share priceAndAssess, so the total the person sees is the
// total charged.

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// DiscountInput is one manual discount: exactly one of PercentBps (1 to 10000, 1250 is 12.5%)
// or AmountCents, and the manager who approved it.
type DiscountInput struct {
	PercentBps  int    `json:"percent_bps,omitempty"`
	AmountCents int64  `json:"amount_cents,omitempty"`
	ApprovedBy  string `json:"approved_by"`
	// ApprovedQty and ApprovedBaseCents are what the manager's yes was given for: a line
	// discount's quantity and the line total it was struck from, a ticket discount's base (the
	// subtotal after the line discounts). The ring refuses a discount whose approval no longer
	// matches (discount.go), so a yes never grows with the quantity or the ticket.
	ApprovedQty       float64 `json:"approved_qty,omitempty"`
	ApprovedBaseCents int64   `json:"approved_base_cents"`
}

// Discount scopes on a SaleDiscount.
const (
	DiscountScopeLine = "LINE"
	DiscountScopeSale = "SALE"
)

// SaleDiscount is one manual discount as the sale and its receipt report it.
type SaleDiscount struct {
	Scope           string `json:"scope"`             // LINE | SALE
	LineNo          int    `json:"line_no,omitempty"` // the line, for a LINE discount
	PercentBps      int    `json:"percent_bps,omitempty"`
	AmountCents     int64  `json:"amount_cents"` // what came off, in cents
	ApprovedBy      string `json:"approved_by"`  // the approving manager's staff id
	ApprovedByRole  string `json:"approved_by_role"`
	ApprovedByLabel string `json:"approved_by_label"` // the role label the receipt prints
}

// saleDiscountRow is the ticket discount stored on pos_sale (0365).
type saleDiscountRow struct {
	AmountCents  int64
	PercentBps   int
	ApprovedBy   string
	RoleID       string
	ApprovedBase int64 // the base the ticket yes was given for
}

// DiscountApprover is one manager a till person may name: id and display name, with the role
// label the receipt prints.
type DiscountApprover struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RoleLabel string `json:"role_label"`
}

// discountStaffRow is one candidate row from the store: an active staff login with one role
// grant that reaches the branch.
type discountStaffRow struct {
	UserID    string
	Name      string
	RoleID    string
	RoleLabel string
	Tier      int
}

// CounterDiscountLogger is the pricing log's counter-discount door, asserted onto the line
// resolver already injected (the counterQuoter idiom): *pricing.Service satisfies it.
type CounterDiscountLogger interface {
	RecordCounterDiscountTx(ctx context.Context, tx pgx.Tx, rec pricing.CounterDiscountRecord) error
}

// appliedDiscount is one discount the pricing path resolved, carried to the write and the logs.
type appliedDiscount struct {
	scope      string
	lineNo     int
	productID  string
	percentBps int
	cents      int64
	baseCents  int64 // the amount the discount was struck from
	approver   approverInfo
}

// approverInfo is a qualifying approver, resolved once per body.
type approverInfo struct {
	id, roleID, roleLabel string
}

var (
	errDiscountNeedsApprover = clientErr(http.StatusUnprocessableEntity,
		"pos: a discount needs the manager who approved it (approved_by)")
	errDiscountApproverRefused = clientErr(http.StatusUnprocessableEntity,
		"pos: that person cannot approve a discount at this branch")
	errDiscountShape = clientErr(http.StatusUnprocessableEntity,
		"pos: a discount is either a percent or an amount, not both and not neither")
	errDiscountPercent = clientErr(http.StatusUnprocessableEntity,
		"pos: a discount percent must be more than 0 and no more than 100")
	errDiscountNoApproval = clientErr(http.StatusUnprocessableEntity,
		"pos: a discount carries the quantity and the amount it was approved for")
	errDiscountStale = clientErr(http.StatusUnprocessableEntity,
		"pos: the discount was approved for a different quantity or price: ask a manager again")
	errDiscountNothing = clientErr(http.StatusUnprocessableEntity,
		"pos: that discount comes to less than a cent")
)

// discountCents resolves one discount against the amount it is struck from: percent rounds half
// up to the cent, an amount may not exceed what it comes off. what names the thing for the words
// of the refusal ("the line", "the ticket").
func discountCents(d *DiscountInput, base int64, what string) (int64, error) {
	if d.PercentBps < 0 || d.AmountCents < 0 || (d.PercentBps > 0) == (d.AmountCents > 0) {
		return 0, errDiscountShape
	}
	if d.PercentBps > 0 {
		if d.PercentBps > 10000 {
			return 0, errDiscountPercent
		}
		c := (base*int64(d.PercentBps) + 5000) / 10000
		if c == 0 {
			return 0, errDiscountNothing
		}
		return c, nil
	}
	if d.AmountCents > base {
		return 0, clientErr(http.StatusUnprocessableEntity, "pos: a discount cannot be more than %s", what)
	}
	return d.AmountCents, nil
}

// discountApprovers is the rule for who may be named: the active logins whose grants reach the
// branch AND whose role the dealer's configuration lets clear a price in its own branch (the
// own-branch clearing list with its setting folded in, the reader the quote side's floor clearing
// calls). One entry per login, in name order; a login with several qualifying grants is named by
// its highest role (the store answers tier order).
func (s *Service) discountApprovers(ctx context.Context, branchID, userID string) ([]discountApproverEntry, error) {
	if s.roles == nil {
		return nil, errRoleConfigUnwired()
	}
	orgID := ""
	if org, ok := requestOrgFrom(ctx); ok {
		orgID = org.OrgID
	}
	rows, err := s.store.ListDiscountStaff(ctx, orgID, branchID, userID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []discountApproverEntry
	for _, r := range rows {
		if seen[r.UserID] {
			continue
		}
		ok, err := s.roles.ClearsOwnBranchFloor(r.RoleID)
		if err != nil || !ok {
			continue
		}
		seen[r.UserID] = true
		out = append(out, discountApproverEntry{DiscountApprover{ID: r.UserID, Name: r.Name, RoleLabel: r.RoleLabel}, r.RoleID})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

type discountApproverEntry struct {
	DiscountApprover
	roleID string
}

// checkApproval holds a discount to what the manager's yes was given for: a line discount to the
// quantity and the line total it was struck from, a ticket discount to the subtotal after the line
// discounts. A yes never grows with the quantity or the ticket; absent or stale, the ring refuses.
func checkApproval(d *DiscountInput, qty float64, base int64, line bool) error {
	if d.ApprovedBaseCents <= 0 || (line && d.ApprovedQty <= 0) {
		return errDiscountNoApproval
	}
	if d.ApprovedBaseCents != base || (line && d.ApprovedQty != qty) {
		return errDiscountStale
	}
	return nil
}

// resolveApprover names one approver for a discount, once per body (cache), refusing in words
// without an id and with a login the rule does not admit.
func (s *Service) resolveApprover(ctx context.Context, branchID, id string, cache map[string]approverInfo) (approverInfo, error) {
	if id == "" {
		return approverInfo{}, errDiscountNeedsApprover
	}
	if a, ok := cache[id]; ok {
		return a, nil
	}
	if _, err := uuid.Parse(id); err != nil {
		return approverInfo{}, errDiscountApproverRefused
	}
	list, err := s.discountApprovers(ctx, branchID, id)
	if err != nil {
		return approverInfo{}, err
	}
	if len(list) == 0 {
		return approverInfo{}, errDiscountApproverRefused
	}
	a := approverInfo{id: list[0].ID, roleID: list[0].roleID, roleLabel: list[0].RoleLabel}
	cache[id] = a
	return a, nil
}

// ListDiscountApprovers answers the managers a till person may name at the branch: id, display
// name and the role label. Behind the counter till surface at read, and the branch wall every
// branch read takes (D49).
func (s *Service) ListDiscountApprovers(ctx context.Context, branchID string) ([]DiscountApprover, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireBranchReach(caller, branchID); err != nil {
		return nil, err
	}
	list, err := s.discountApprovers(ctx, branchID, "")
	if err != nil {
		return nil, err
	}
	out := make([]DiscountApprover, len(list))
	for i, e := range list {
		out[i] = e.DiscountApprover
	}
	return out, nil
}

// applyDiscounts takes the priced lines (LineTotalCents the list line total) and the optional
// ticket discount, applies every manual discount, and returns what the ring and the preview both
// need. It mutates the lines: after it each LineTotalCents is net of every discount, which is
// what a return refunds against.
func (s *Service) applyDiscounts(ctx context.Context, branchID string, reqs []LineInput, lines []orderLineRow, ticket *DiscountInput) ([]appliedDiscount, saleDiscountRow, error) {
	var applied []appliedDiscount
	cache := map[string]approverInfo{}
	for i := range lines {
		d := reqs[i].Discount
		if d == nil {
			continue
		}
		gross := lines[i].LineTotalCents
		cents, err := discountCents(d, gross, "the line")
		if err != nil {
			return nil, saleDiscountRow{}, clientErr(http.StatusUnprocessableEntity, "line %d: %s", i+1, trimPrefixPos(err))
		}
		a, err := s.resolveApprover(ctx, branchID, d.ApprovedBy, cache)
		if err != nil {
			return nil, saleDiscountRow{}, clientErr(http.StatusUnprocessableEntity, "line %d: %s", i+1, trimPrefixPos(err))
		}
		if err := checkApproval(d, reqs[i].QtyEntered, gross, true); err != nil {
			return nil, saleDiscountRow{}, clientErr(http.StatusUnprocessableEntity, "line %d: %s", i+1, trimPrefixPos(err))
		}
		lines[i].DiscountApprovedQty = d.ApprovedQty
		lines[i].DiscountApprovedBase = d.ApprovedBaseCents
		lines[i].LineTotalCents = gross - cents
		lines[i].DiscountCents = cents
		lines[i].DiscountPercentBps = d.PercentBps
		lines[i].DiscountApprovedBy = a.id
		lines[i].DiscountRoleID = a.roleID
		applied = append(applied, appliedDiscount{
			scope: DiscountScopeLine, lineNo: lines[i].LineNo, productID: lines[i].ProductID,
			percentBps: d.PercentBps, cents: cents, baseCents: gross, approver: a,
		})
	}
	var row saleDiscountRow
	if ticket != nil {
		var subtotal int64
		weights := make([]int64, len(lines))
		for i := range lines {
			subtotal += lines[i].LineTotalCents
			weights[i] = lines[i].LineTotalCents
		}
		cents, err := discountCents(ticket, subtotal, "the ticket")
		if err != nil {
			return nil, saleDiscountRow{}, clientErr(http.StatusUnprocessableEntity, "ticket: %s", trimPrefixPos(err))
		}
		a, err := s.resolveApprover(ctx, branchID, ticket.ApprovedBy, cache)
		if err != nil {
			return nil, saleDiscountRow{}, clientErr(http.StatusUnprocessableEntity, "ticket: %s", trimPrefixPos(err))
		}
		if err := checkApproval(ticket, 0, subtotal, false); err != nil {
			return nil, saleDiscountRow{}, clientErr(http.StatusUnprocessableEntity, "ticket: %s", trimPrefixPos(err))
		}
		for i, share := range moneyutil.AllocateLargestRemainder(cents, weights) {
			lines[i].SaleDiscountCents = share
			lines[i].LineTotalCents -= share
		}
		row = saleDiscountRow{AmountCents: cents, PercentBps: ticket.PercentBps, ApprovedBy: a.id, RoleID: a.roleID, ApprovedBase: subtotal}
		applied = append(applied, appliedDiscount{
			scope: DiscountScopeSale, percentBps: ticket.PercentBps, cents: cents, baseCents: subtotal, approver: a,
		})
	}
	return applied, row, nil
}

// trimPrefixPos drops the "pos: " the sentinels carry, so a refusal that names its line reads
// "pos: line 1: ..." once.
func trimPrefixPos(err error) string {
	return strings.TrimPrefix(err.Error(), "pos: ")
}

// reportDiscounts renders the applied discounts as the sale's own report, labels included.
func reportDiscounts(applied []appliedDiscount) (int64, []SaleDiscount) {
	var total int64
	out := make([]SaleDiscount, 0, len(applied))
	for _, a := range applied {
		total += a.cents
		out = append(out, SaleDiscount{
			Scope: a.scope, LineNo: a.lineNo, PercentBps: a.percentBps, AmountCents: a.cents,
			ApprovedBy: a.approver.id, ApprovedByRole: a.approver.roleID, ApprovedByLabel: a.approver.roleLabel,
		})
	}
	if len(out) == 0 {
		return 0, nil
	}
	return total, out
}

// logDiscounts writes the pricing log row of every applied discount on the sale's own
// transaction (the quote side's clearing rows ride their write the same way): a sale that
// committed with a discount and no log row cannot happen, and a resolver that cannot log refuses
// the sale rather than let a discount go unlogged.
func (s *Service) logDiscounts(ctx context.Context, tx pgx.Tx, orderID, clerk string, applied []appliedDiscount) error {
	if len(applied) == 0 {
		return nil
	}
	lg, ok := s.pricing.(CounterDiscountLogger)
	if !ok {
		return clientErr(http.StatusServiceUnavailable, "pos: the pricing log is not wired, a discount cannot be recorded")
	}
	for _, a := range applied {
		rec := pricing.CounterDiscountRecord{
			ApproverID: a.approver.id, ClerkID: clerk, OrderID: orderID, LineNo: a.lineNo,
			ProductID: a.productID, BaseCents: a.baseCents, NetCents: a.baseCents - a.cents,
			PercentBps: a.percentBps, AmountCents: a.cents,
		}
		if err := lg.RecordCounterDiscountTx(ctx, tx, rec); err != nil {
			return err
		}
	}
	return nil
}

// recordDiscountAudit files one audit row per discount, post-commit like the sale's own record:
// the approver's staff id, the cents and the line, never a name.
func (s *Service) recordDiscountAudit(ctx context.Context, orderID, actor, branchID string, applied []appliedDiscount) {
	for _, a := range applied {
		extra := map[string]any{"approver_id": a.approver.id, "amount_cents": a.cents, "actor_role": a.scope}
		if a.lineNo > 0 {
			extra["line_no"] = a.lineNo
		}
		s.record(ctx, audit.SensitiveAccess{
			Action: audit.ActPosDiscountApproved, EntityType: "pos.sale", EntityID: orderID,
			ActorID: actor, BranchID: branchID, Purpose: "POS_DISCOUNT", Extra: extra,
		})
	}
}

// findSaleByKey is the replay read with the sale's discounts read back onto it, so a replayed
// ring answers verbatim, the receipt's approver included.
func (s *Service) findSaleByKey(ctx context.Context, key string) (Sale, bool, error) {
	sale, found, err := s.store.FindSaleByKey(ctx, key)
	if err != nil || !found {
		return sale, found, err
	}
	ds, err := s.store.ReadSaleDiscounts(ctx, sale.OrderID)
	if err != nil {
		return Sale{}, false, err
	}
	for _, d := range ds {
		sale.DiscountCents += d.AmountCents
	}
	if len(ds) > 0 {
		sale.Discounts = ds
	}
	return sale, true, nil
}
