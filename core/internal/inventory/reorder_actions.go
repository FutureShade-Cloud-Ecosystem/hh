package inventory

// reorder_actions.go, DIB-461: quick reorder from the product view, in the two shapes the client
// asked for on the 2026-08-11 call.
//
//	"if certain people have the rights … even if that's just a push to procurement, a nudge
//	 saying hey we're getting low."
//
// TWO SHAPES, ONE QUESTION ("is anybody going to do something about this?"), split by what the
// person in front of the screen is ALLOWED to do:
//
//	ORDER NOW      → a DRAFT purchase order, prefilled, deep-linked. Never an ordered one.
//	NUDGE          → one NOTABLE system message naming the product, the branch and the position.
//
// - ORDER NOW REUSES THE DIB-72 PATH. IT DOES NOT FORK IT. -
//
// CreatePOFromShortfall (replenish.go) already turns replenishment rows into a SuggestedPO and hands
// it to the POCreator seam, which the composition root binds to purchasing.CreatePO. This ticket
// adds ONE thing that path could not do: start from a product somebody is LOOKING AT rather than
// from a row the worklist happened to list. Everything after that is the same code -
//
//	the same suggested quantity     SuggestQty(point, projected, reorder_qty, multiple)
//	the same vendor rule            the replenishment line's preferred vendor, or a refusal
//	the same seam                   POCreator.CreateDraftPO
//	the same stub posture           StubPOCreator returns the draft WITH the 503
//
//, so the number a buyer sees on the product page is the number the worklist would have suggested.
// Two screens that disagree about how much to buy make a buyer pick one, and they will pick wrong.
//
// A DRAFT, ALWAYS. purchasing.CreatePO writes status DRAFT and this ticket never transitions it. A
// button on a product page that sent an order to a vendor would be a purchase commitment made by
// someone who was looking something up.
//
// - THE NUDGE IS A MESSAGE, NOT A DOCUMENT -
//
// It writes NOTHING here: no reservation, no order, no flag on the product. It posts one system
// event and returns. That is deliberate, the person nudging has no authority to commit money, so
// the nudge must not create anything that looks like a commitment. What it produces is a sentence
// in a channel a procurement manager reads, with the numbers already in it, so the reply is a
// decision rather than "how many have we got?".

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// - ORDER NOW -

// ReorderRequest is one product's quick reorder.
//
// QtyBase is OPTIONAL: 0 means "use the replenishment suggestion", which is what the button sends.
// A buyer who knows better may say so, and the number they typed is used verbatim, the suggestion
// is a default, not a rule, and a control that silently replaced a typed quantity would be worse
// than one that had no field at all.
type ReorderRequest struct {
	ProductID string  `json:"product_id"`
	BranchID  string  `json:"branch_id"`
	QtyBase   float64 `json:"qty_base"`
}

// ReorderResult is what the surface needs to say what happened and take the buyer to it.
type ReorderResult struct {
	POID string `json:"po_id"`
	PONo string `json:"po_no"`
	// Draft is the order as it was sent, so a surface can show exactly what WOULD have been ordered
	// even when the purchasing seam is unbound, the StubPOCreator posture, kept.
	Draft SuggestedPO `json:"draft"`
	// Suggested is the quantity the replenishment arithmetic proposed, whether or not the caller
	// overrode it. Carried so the buyer can see that they typed something different.
	Suggested float64 `json:"suggested_qty_base"`
	UOM       string  `json:"uom"`
	// VendorName is the "usual vendor" the draft went to, named so the buyer can tell at a glance
	// that it went to the right one.
	VendorName string `json:"vendor_name"`
	// ExistingDraftPOID and ExistingDraftPONo name the unplaced draft this product already had at
	// this branch BEFORE this press (the Opus review's P3-3). A draft never covers a shortfall, so the
	// press still drafts the full quantity; these let the surface say "a draft is already open" in
	// the same words the worklist row uses. Absent when there was none.
	ExistingDraftPOID string `json:"existing_draft_po_id,omitempty"`
	ExistingDraftPONo string `json:"existing_draft_po_no,omitempty"`
}

// replenishReader is the optional persistence seam for the single-line replenishment read, resolved
// by type assertion off the base store like every other optional read in this package.
type replenishReader interface {
	ReplenishLineFor(ctx context.Context, productID, branchID string, onOrderBase float64) (ShortfallRow, bool, error)
}

func (s *Service) replenishReader() (replenishReader, error) {
	rr, ok := s.db().(replenishReader)
	if !ok {
		return nil, ErrStockLookupNotConfigured
	}
	return rr, nil
}

// branchPositionReader is the four-bucket read the nudge quotes, reached through the store seam
// rather than through s.repo directly.
//
// *Repository satisfies it as written (visibility.go's BranchStock), so production takes exactly the
// path AvailabilityAtBranch takes, this is not a second read, it is the same one reached through
// the seam so a unit test can state what the message says without a database. It drives off
// LOCATIONS, which is why the branch can always be NAMED even for a product that has never been
// stocked there: a nudge that said "at 5d1b3f6e-…" would be unreadable.
type branchPositionReader interface {
	BranchStock(ctx context.Context, productID, branchID, orgID string) ([]BranchStock, error)
}

func (s *Service) branchPositionReader() (branchPositionReader, error) {
	pr, ok := s.db().(branchPositionReader)
	if !ok {
		return nil, ErrStockLookupNotConfigured
	}
	return pr, nil
}

// fenceNamedBranch answers a branch the caller named: itself when it is in the reach, the 403 in
// words when it is not, and "" for an unnamed one (the caller of an all-branches read applies the
// reach set itself). One rule for the reorder controls and the worklist.
func fenceNamedBranch(reach BranchReach, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || reach.Global {
		return requested, nil
	}
	for _, b := range reach.BranchIDs {
		if b == requested {
			return requested, nil
		}
	}
	return "", clientError{status: http.StatusForbidden,
		msg: "inventory: you do not have access to that branch"}
}

// resolveBranch decides which yard a reorder control acts on.
//
// AN EMPTY branch_id MEANS "MY YARD", and it is the ordinary case rather than a shortcut. A
// counter clerk granted one branch has exactly one answer to "which yard is this for", and making
// the surface send an id it has no other reason to know is how a control ends up acting on the
// wrong branch, the walkthrough found precisely that: the card lists every branch (the four-bucket
// read is deliberately cross-branch), so a Trenton-only clerk was offered a control aimed at
// Kingston, which the fence then refused. The reach is the authority, so the reach is what resolves
// it.
//
// The three cases are purchasing.branchFence's, for the same reasons:
//
//	one grant       → that branch, implicitly.
//	several/global  → REFUSED with a sentence that says what to do. Picking one for them would make
//	                  the answer depend on map iteration order.
//	no grants       → refused: there is no yard to act on.
func resolveBranch(reach BranchReach, requested string) (string, error) {
	if requested = strings.TrimSpace(requested); requested != "" {
		return fenceNamedBranch(reach, requested)
	}
	if reach.Global || len(reach.BranchIDs) != 1 {
		return "", badReq("inventory: branch_id is required, your access spans more than one branch")
	}
	return reach.BranchIDs[0], nil
}

// CreateReorderPO drafts a purchase order for ONE product at ONE branch and returns where it landed.
//
// The refusals are all the same shape: say what is missing and what to do about it. A dead button
// that reports "failed" teaches people to stop pressing it.
func (s *Service) CreateReorderPO(ctx context.Context, reach BranchReach, in ReorderRequest) (ReorderResult, error) {
	if strings.TrimSpace(in.ProductID) == "" {
		return ReorderResult{}, badReq("inventory: product_id is required")
	}
	branchID, err := resolveBranch(reach, in.BranchID)
	if err != nil {
		return ReorderResult{}, err
	}
	in.BranchID = branchID
	if in.QtyBase < 0 {
		return ReorderResult{}, badReq("inventory: qty_base must be greater than zero")
	}
	rr, rerr := s.replenishReader()
	if rerr != nil {
		return ReorderResult{}, rerr
	}
	// The DIB-470 on-order leg, for THIS product at THIS branch, from the same seam the worklist
	// grades with. A degraded (unbound) seam contributes zero here exactly as it does there, the
	// two surfaces are wrong together or right together, never one of each.
	onOrder, _, err := s.onOrderFor(ctx, in.BranchID, []string{in.ProductID})
	if err != nil {
		return ReorderResult{}, err
	}
	line, found, err := rr.ReplenishLineFor(ctx, in.ProductID, in.BranchID,
		onOrderBaseFor(onOrder, in.ProductID, in.BranchID))
	if err != nil {
		return ReorderResult{}, err
	}
	if !found {
		// No managed line means nobody has said who we buy this from or how much of it we keep. The
		// schema has exactly one product→vendor link and it lives on that row (0117), so this is not
		// a shortcut, there is nowhere else to look.
		return ReorderResult{}, badReq(
			"inventory: this product has no replenishment line at this branch, set its reorder point and preferred vendor first")
	}
	if line.PreferredVendorID == "" {
		return ReorderResult{}, badReq(
			"inventory: %s has no preferred vendor, set one before creating a purchase order", line.SKU)
	}
	qty := line.SuggestedQtyBase
	if in.QtyBase > 0 {
		qty = in.QtyBase
	}
	if qty <= 0 {
		// Reachable when the policy's reorder quantity is zero and the position is already above the
		// point: the honest answer is that there is nothing to order, not a PO for nothing.
		return ReorderResult{}, badReq(
			"inventory: nothing to order for %s, its replenishment quantity is zero and its position is above the reorder point", line.SKU)
	}

	draft := SuggestedPO{
		BranchID: in.BranchID,
		VendorID: line.PreferredVendorID,
		Memo:     "quick reorder from the product view (DIB-461)",
		Lines: []SuggestedPOLine{{
			ProductID: in.ProductID, QtyBase: qty, UnitCostCents: line.AvgCostCents,
		}},
	}
	res := ReorderResult{Draft: draft, Suggested: line.SuggestedQtyBase, UOM: line.UOM, VendorName: line.PreferredVendorName}
	// Read BEFORE the new draft is made, so the pointer names the draft that was already open, never
	// the one this press creates. An unbound seam leaves it absent; a bound seam that fails is an
	// error, as it is on the worklist: silence would read as "nothing drafted".
	if s.draftPOs != nil {
		open, err := s.draftPOs.OpenDraftsFor(ctx, in.BranchID, []string{in.ProductID})
		if err != nil {
			return ReorderResult{}, err
		}
		for _, d := range open {
			if d.ProductID == in.ProductID && d.BranchID == in.BranchID {
				res.ExistingDraftPOID, res.ExistingDraftPONo = d.POID, d.PONo
				break
			}
		}
	}

	creator := s.poCreator
	if creator == nil {
		creator = StubPOCreator{}
	}
	poID, poNo, err := creator.CreateDraftPO(ctx, draft)
	if err != nil {
		// The draft rides along with the error, exactly as CreatePOFromShortfall does it.
		return res, err
	}
	res.POID, res.PONo = poID, poNo
	s.log.Info("inventory: quick reorder drafted", "po_no", poNo, "sku", line.SKU,
		"branch_id", in.BranchID, "qty_base", qty)
	return res, nil
}

// - NUDGE PROCUREMENT -

// NudgeClassNotable is the DIB-449 event tier this module posts a nudge at.
//
// NOTABLE means "someone should look": ONE message per event in its home channel, today's shipped
// per-event behaviour. The two neighbours are both wrong for this:
//
//	ROUTINE  folds the event into the day's rollup. A nudge that arrives as a line in tomorrow's
//	         summary is a nudge nobody acted on.
//	CRITICAL is the DIB-217 forced-ack / #escalations path, which BLOCKS a kiosk until somebody
//	         acknowledges it. "We're getting low on Blu 60" is not a credit hold or an SLA breach,
//	         and a low-stock warning that stops a till is how people learn to dismiss alerts.
//
// The string is inventory's own copy of the vocabulary (the house zero-trust convention: a module
// does not import a sibling's constants), and the adapter that binds this seam maps it onto
// messaging's per-event post path.
const NudgeClassNotable = "NOTABLE"

// ProcurementChannelSlug is the channel a nudge is posted into.
//
// #office is a GLOBAL team channel that every active staff member is auto-joined to (migration
// 0052), which is the only seeded channel a procurement manager is certainly in whichever branch
// they are granted. The alternatives were considered and rejected: #finance is the money desk
// (payments, credit holds), and #yard-<branch> is one yard's operational feed, a nudge posted
// there is invisible to procurement sitting at the other branch, which is the exact failure the
// ticket describes.
//
// This is a ROUTING decision and it lives in the module rather than in the composition root so it is
// testable. When a #purchasing channel is seeded, this constant is the one line that changes
// (NOTES-FIX-INV460.md, PARK P3).
const ProcurementChannelSlug = "office"

// NudgeTopic is the event topic a nudge carries. It is NOT on messaging's forced-ack allowlist and
// must not be added to it, see NudgeClassNotable.
const NudgeTopic = "inventory.reorder.nudge"

// NudgePost is the by-value request the ProcurementNudger seam takes. Primitive-only, so neither
// module imports the other's types (the frozen-seam convention this package already uses for
// POCreator).
type NudgePost struct {
	// ChannelSlug is where it goes. Named by this module, not chosen by the adapter.
	ChannelSlug string
	// EventKey is what makes a second nudge for the same product, at the same branch, on the same
	// day a no-op. See NudgeEventKey.
	EventKey string
	// Class is the DIB-449 tier. Always NudgeClassNotable.
	Class string
	Topic string
	// Entity is the ref the message carries back to what it is about.
	Entity  string
	Body    string
	Summary string
}

// ProcurementNudger is the CONSUMER-DECLARED SEAM for the nudge. messaging implements the post; a
// composition-root adapter binds it (that binding is PARK P1, this ticket may not edit main.go).
//
// inserted=false means an identical nudge is already on the board today and nothing was written.
// That is not an error and the surface says so in words a person can act on.
type ProcurementNudger interface {
	PostProcurementNudge(ctx context.Context, post NudgePost) (inserted bool, err error)
}

// WithProcurementNudger injects the messaging seam.
func (s *Service) WithProcurementNudger(n ProcurementNudger) *Service { s.nudger = n; return s }

// StubProcurementNudger is the default: it posts nothing and says so, out loud.
//
// The StubPOCreator posture, for the same reason. A nudge that silently succeeded while no channel
// received it would leave someone waiting for procurement to answer a message that was never sent -
// and unlike a missing purchase order, nobody would ever find out.
type StubProcurementNudger struct{}

// PostProcurementNudge reports that messaging is not wired.
func (StubProcurementNudger) PostProcurementNudge(context.Context, NudgePost) (bool, error) {
	return false, clientError{
		status: 503,
		msg:    "inventory: messaging is not wired, the nudge was not posted (ProcurementNudger seam unbound)",
	}
}

// NudgeResult is what the surface says back.
type NudgeResult struct {
	// Posted false means the nudge was already on the board today (see NudgeEventKey).
	Posted  bool   `json:"posted"`
	Channel string `json:"channel"`
	Class   string `json:"class"`
	Body    string `json:"body"`
	Note    string `json:"note"`
}

// The two sentences the surface shows. Written here, beside the decision, so the words and the
// behaviour change together.
const (
	NudgePostedNote  = "Procurement has been nudged."
	NudgeAlreadyNote = "Procurement was already nudged about this today, the message is on the board."
)

// NudgeEventKey is the idempotency key: one nudge per (product, branch, DAY).
//
// WHY POLITELY REFUSED RATHER THAN APPENDED TO A THREAD. Both were on the table. A thread reply
// re-raises the channel's unread badge, so ten frustrated presses become ten notifications, which
// is the loudness DIB-449 exists to control, arriving from the one place that is meant to be a
// courtesy. And a second nudge carries NO new information: the position has barely moved, and the
// first message already says it. So the second press is a no-op that TELLS THE PERSON it was a
// no-op ("already nudged today, it's on the board"), which answers what they actually wanted to
// know: yes, procurement has been told.
//
// The DAY is the window, not an hour or a shift: procurement works a day at a time, and a nudge that
// re-fired every hour would be a countdown nobody asked for. Tomorrow it can be raised again, which
// is exactly right, a product still short tomorrow is new information.
//
// The idempotency itself is SHIPPED machinery: messaging's event message insert is unique on
// (channel_id, event_key) and reports whether it inserted. This function only has to name the key.
func NudgeEventKey(productID, branchID string, at time.Time) string {
	return fmt.Sprintf("reorder-nudge:%s:%s:%s", productID, branchID, at.UTC().Format("2006-01-02"))
}

// NudgeBody is the message procurement reads. It names the product, the branch and the position -
// the three things that otherwise get asked back before anyone can act.
//
// The position is stated in the units the product is stocked in. "We're getting low on X" without
// numbers starts a conversation; with them, it starts a decision.
//
// IT SAYS NOTHING ABOUT WHAT IS ON ORDER. When this was written that was forced: the only on-order
// figure this module could compute was the DIB-72 replenishment leg, which counted the 0011
// purchase_orders rows, RECEIVING DOCUMENTS since migration 0120, therefore never open, therefore
// structurally 0, and a nudge asserting "nothing on order" would have flatly contradicted the
// On-order section the same person was looking at.
//
// DIB-470 CLOSED THAT DEFECT (on_order_seam.go), so the number is now available and correct here.
// The message still does not quote it, and that is now a WORDING decision rather than a data one:
// what a nudge should say when procurement has already raised the order is a sentence somebody
// should choose on purpose, not a field this ticket appends. Stating a position and letting
// procurement read the order board, which DIB-461 put on the product page, remains honest either
// way. PARK P5 in NOTES-FIX-470.md.
func NudgeBody(p ProductLookup, branchCode string, pos BranchStock) string {
	uom := p.UOM
	if uom == "" {
		uom = "EA"
	}
	q := func(v float64) string { return fmt.Sprintf("%s %s", trimQty(v), uom) }
	b := fmt.Sprintf("Getting low, %s (%s) at %s: %s available, %s on hand",
		p.Name, p.SKU, branchCode, q(pos.AvailableBase), q(pos.OnHandBase))
	if pos.InTransitBase > 0 {
		b += fmt.Sprintf(", %s in transit", q(pos.InTransitBase))
	}
	return b + "."
}

// trimQty renders a DECIMAL(19,4) the way a person writes it: 120, not 120.0000.
func trimQty(v float64) string {
	s := fmt.Sprintf("%.4f", v)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// positionAt reads one branch's four buckets through the seam. A branch with no stock record
// returns a ZEROED row rather than not-found, exactly as AvailabilityAtBranch does, "none at
// Kingston" is a real answer, and it is the answer a nudge is most likely to be about.
func (s *Service) positionAt(ctx context.Context, pr branchPositionReader, productID, branchID string) (BranchStock, error) {
	rows, err := pr.BranchStock(ctx, productID, branchID, "")
	if err != nil {
		return BranchStock{}, err
	}
	if len(rows) == 0 {
		return BranchStock{ProductID: productID, BranchID: branchID}, nil
	}
	return rows[0], nil
}

// NudgeProcurement posts one NOTABLE system event about a product's position at a branch.
//
// The caller's reach is applied to the POSITION read, not to the message: a person may only nudge
// about a yard they can see, and the numbers in the message are the numbers they were looking at.
func (s *Service) NudgeProcurement(ctx context.Context, reach BranchReach, productID, branchID string) (NudgeResult, error) {
	if strings.TrimSpace(productID) == "" {
		return NudgeResult{}, badReq("inventory: product_id is required")
	}
	// Empty means "my yard"; an explicit branch must be one the caller can reach. Nudging about a
	// yard you cannot see would put numbers you are not allowed to read into a message you CAN read
	// back, the read fence defeated by the write beside it.
	branchID, berr := resolveBranch(reach, branchID)
	if berr != nil {
		return NudgeResult{}, berr
	}
	ls, err := s.stockLookupStore()
	if err != nil {
		return NudgeResult{}, err
	}
	p, found, err := ls.ResolveProductRef(ctx, productID)
	if err != nil {
		return NudgeResult{}, err
	}
	if !found {
		return NudgeResult{}, errNotFound
	}
	pr, err := s.branchPositionReader()
	if err != nil {
		return NudgeResult{}, err
	}
	posRows, err := s.positionAt(ctx, pr, p.ProductID, branchID)
	if err != nil {
		return NudgeResult{}, err
	}
	pos := posRows
	branchCode := pos.BranchCode
	if branchCode == "" {
		branchCode = branchID
	}
	body := NudgeBody(p, branchCode, pos)

	nudger := s.nudger
	if nudger == nil {
		nudger = StubProcurementNudger{}
	}
	inserted, err := nudger.PostProcurementNudge(ctx, NudgePost{
		ChannelSlug: ProcurementChannelSlug,
		EventKey:    NudgeEventKey(p.ProductID, branchID, time.Now()),
		Class:       NudgeClassNotable,
		Topic:       NudgeTopic,
		Entity:      "product:" + p.ProductID,
		Body:        body,
		Summary:     body,
	})
	if err != nil {
		return NudgeResult{Channel: ProcurementChannelSlug, Class: NudgeClassNotable, Body: body}, err
	}
	note := NudgePostedNote
	if !inserted {
		note = NudgeAlreadyNote
	}
	s.log.Info("inventory: procurement nudged", "sku", p.SKU, "branch_id", branchID, "posted", inserted)
	return NudgeResult{
		Posted: inserted, Channel: ProcurementChannelSlug, Class: NudgeClassNotable, Body: body, Note: note,
	}, nil
}
