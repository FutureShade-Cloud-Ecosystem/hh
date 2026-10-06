package landedcost

// service.go, the landed-cost orchestration the frozen module.go entry points (Apply / RetroAdjust) and the
// handler delegate to: Create (USD→CAD conversion at capture), Apply (allocate + capitalize into inventory),
// RetroAdjust (late-freight: capitalize the still-on-hand portion, expense the sold-through portion to COGS),
// Void (reverse, nets to zero), and Preview (the live per-basis allocation the allocate screen renders).
//
// GL / inventory ordering (self-healing dual-write): the GL journal posts FIRST (idempotent on its key), then
// the allocation rows + the inventory weighted-average capitalization + the status flip commit in ONE tx under
// a FOR UPDATE header lock. A retry after a mid-flight failure re-posts the (deduped) journal and re-runs the
// tx; the FOR UPDATE + status guard guarantees the capitalization happens exactly once. landed cost thus flows
// into the NEXT COGS through the raised weighted-average cost (the frozen Capitalizer seam).

import (
	"context"
	"net/http"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// Create validates + persists a landed-cost header. It converts the USD document amounts to CAD via the manual
// fx rate (the DX-19 scoped exception, the GL and inventory valuation only ever see the *_cad_cents) and, for
// a retro doc, seeds the per-product still-on-hand base used by RetroAdjust to split capitalize-vs-sold-through.
func (s *Service) Create(ctx context.Context, in CreateInput) (string, error) {
	if in.BranchID == "" {
		return "", httpx.BadRequestf("landedcost: branch_id required")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "CAD"
	}
	if currency != "CAD" && currency != "USD" {
		return "", httpx.BadRequestf("landedcost: currency must be CAD or USD")
	}
	fx := in.FXRate
	if fx <= 0 {
		fx = 1.0
	}
	if currency == "CAD" {
		fx = 1.0 // a CAD document never carries a manual fx (the 0041 default)
	}
	basis := strings.ToUpper(strings.TrimSpace(in.AllocationBasis))
	switch basis {
	case BasisWeight, BasisValue, BasisQuantity:
	case "":
		basis = BasisValue // default value-proportional
	default:
		return "", httpx.BadRequestf("landedcost: allocation_basis must be WEIGHT, VALUE or QUANTITY")
	}
	if in.FreightCents < 0 || in.DutyCents < 0 || in.HandlingCents < 0 {
		return "", httpx.BadRequestf("landedcost: freight/duty/handling must be non-negative")
	}
	excludeSpecial := true // gl_policy default: special buys don't absorb landed cost
	if in.ExcludeSpecialBuys != nil {
		excludeSpecial = *in.ExcludeSpecialBuys
	}
	lc := LandedCost{
		POID:               in.POID,
		PORef:              in.PORef,
		BranchID:           in.BranchID,
		Currency:           currency,
		FXRate:             fx,
		FreightCents:       in.FreightCents,
		DutyCents:          in.DutyCents,
		HandlingCents:      in.HandlingCents,
		FreightCadCents:    convertToCAD(in.FreightCents, fx),
		DutyCadCents:       convertToCAD(in.DutyCents, fx),
		HandlingCadCents:   convertToCAD(in.HandlingCents, fx),
		AllocationBasis:    basis,
		ExcludeSpecialBuys: excludeSpecial,
		Status:             StatusDraft,
		RetroOfID:          in.RetroOfID,
	}
	id, err := s.repo.Insert(ctx, lc)
	if err != nil {
		return "", err
	}
	if in.RetroOfID != "" && len(in.OnHand) > 0 {
		if err := s.repo.SeedRetroOnHand(ctx, id, in.OnHand); err != nil {
			return "", err
		}
	}
	return id, nil
}

// List returns landed costs filtered by branch/status ("" = all). Branch-scoped (L8): a scoped caller may only
// list within a branch it can access, and may not enumerate ALL branches, an empty branch_id from a scoped
// caller is a 403, forcing an explicit accessible branch. A GLOBAL caller lists any/all branches.
func (s *Service) List(ctx context.Context, branchID, status string) ([]LandedCost, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if branchID == "" {
		if caller.Scope != identity.ScopeGlobal {
			return nil, errForbidden // a scoped caller must name an accessible branch, not enumerate all
		}
	} else if !caller.CanAccessBranch(branchID) {
		return nil, errForbidden
	}
	return s.repo.List(ctx, branchID, status)
}

// Get returns a landed-cost header + its allocations. Branch-scoped (L8): the caller must be able to reach the
// doc's owning branch, else 403, a scoped staffer cannot read another branch's landed cost by iterating ids
// (the IDOR boundary the order-history / till reads already close). A missing doc stays not-found (no leak).
func (s *Service) Get(ctx context.Context, id string) (LandedCostDetail, error) {
	lc, err := s.guardBranch(ctx, id)
	if err != nil {
		return LandedCostDetail{}, err
	}
	allocs, err := s.repo.Allocations(ctx, id)
	if err != nil {
		return LandedCostDetail{}, err
	}
	return LandedCostDetail{LandedCost: lc, Allocations: allocs}, nil
}

// guardBranch resolves the trusted caller, loads the landed cost, and gates on CanAccessBranch. A MISSING doc
// returns not-found (never forbidden, no existence leak); an EXISTING doc in an unreachable branch returns
// forbidden. Every branch-scoped surface (Get/Preview/Apply/RetroAdjust/Void) funnels through this one check.
func (s *Service) guardBranch(ctx context.Context, id string) (LandedCost, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return LandedCost{}, err
	}
	lc, err := s.repo.Get(ctx, id)
	if err != nil {
		return LandedCost{}, err // not-found for a missing doc, no existence leak
	}
	if !caller.CanAccessBranch(lc.BranchID) {
		return LandedCost{}, errForbidden
	}
	return lc, nil
}

// Preview computes the live per-basis allocation for a landed cost WITHOUT persisting, the allocate screen's
// basis picker calls this to show each product's share as the user flips WEIGHT/VALUE/QUANTITY. basis "" uses
// the doc's own basis.
func (s *Service) Preview(ctx context.Context, id, basis string) (AllocationPreview, error) {
	lc, err := s.guardBranch(ctx, id)
	if err != nil {
		return AllocationPreview{}, err
	}
	b := strings.ToUpper(strings.TrimSpace(basis))
	switch b {
	case BasisWeight, BasisValue, BasisQuantity:
	default:
		b = lc.AllocationBasis
	}
	lines, err := s.receivedLines(ctx, lc.POID)
	if err != nil {
		return AllocationPreview{}, err
	}
	total := lc.totalCadCents()
	return AllocationPreview{
		Basis:         b,
		TotalCadCents: total,
		Lines:         previewLines(total, b, lines, lc.ExcludeSpecialBuys),
	}, nil
}

// apply is the body of the frozen Service.Apply: allocate the full landed cost across the received lines and
// capitalize every share into the weighted-average cost. Idempotent, a non-DRAFT doc is a no-op.
//
// Costing correctness (S3 W6 B1/B2): the GL 1200 debit is booked from what the subledger ACTUALLY absorbs, not
// from the raw allocation. Capitalizing a share into inventory whose on-hand is ≤ 0 absorbs nothing (the goods
// are gone), and integer-cent average costing cannot bank every cent, so each share's un-absorbed remainder is
// expensed to COGS (5000). This keeps GL 1200 == the perpetual subledger the W5 tie-out measures, the earlier
// version debited the whole total to 1200 while capitalize silently no-op'd, stranding value on ordinary
// inputs. Because the split is only known after capitalize runs, the journal is posted AFTER the capitalize tx
// (idempotent on key); the header status is advanced last, so a crash before that leaves the committed
// allocation rows as the capitalize-once marker (a replay recomputes from them rather than double-capitalizing).
func (s *Service) apply(ctx context.Context, id string) error {
	lc, err := s.guardBranch(ctx, id)
	if err != nil {
		return err
	}
	if lc.Status != StatusDraft {
		return nil // already applied/voided, idempotent no-op
	}
	lines, err := s.receivedLines(ctx, lc.POID)
	if err != nil {
		return err
	}
	total := lc.totalCadCents()
	allocs := allocate(total, lc.AllocationBasis, lines, lc.ExcludeSpecialBuys)

	var capTotal, cogsTotal int64
	var alreadyFinal bool
	if err := s.repo.WithinTx(ctx, func(tx pgx.Tx) error {
		locked, err := s.repo.LockForUpdateTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.Status != StatusDraft {
			alreadyFinal = true // a concurrent apply already finalized, nothing to do
			return nil
		}
		existing, err := s.repo.AllocationsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			// A prior attempt already capitalized (rows committed, status not yet advanced). Recompute the
			// split totals from them, never capitalize twice.
			for _, a := range existing {
				capTotal += a.AllocatedCadCents
				cogsTotal += a.SoldThroughCadCents
			}
			return nil
		}
		for _, a := range allocs {
			absorbed, snapAvg, hadCost, err := s.capitalizeSnap(ctx, tx, a.ProductID, lc.BranchID, a.AllocatedCadCents)
			if err != nil {
				return err
			}
			a.SoldThroughCadCents = a.AllocatedCadCents - absorbed // no-on-hand + rounding remainder → COGS
			a.AllocatedCadCents = absorbed
			// Snapshot the exact pre-Apply WAC so Void restores it verbatim (cent-exact Apply/Void round-trip, L8
			// MAJOR). Persist it only when a real capitalization happened AND a cost row existed to snapshot, an
			// absorbed==0 no-on-hand share, or a product with no prior cost row, has nothing to restore, so we leave
			// the column NULL (Void skips restore for it).
			if absorbed != 0 && hadCost {
				a.PreApplyAvgUnitCost = snapAvg
				a.HasPreApplyAvg = true
			}
			capTotal += a.AllocatedCadCents
			cogsTotal += a.SoldThroughCadCents
			if err := s.repo.InsertAllocationTx(ctx, tx, id, a); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if alreadyFinal {
		return nil
	}

	journalID, err := s.postJournal(ctx, glJournal{
		key:    "landedcost.apply:" + id,
		source: glSourcePOReceipt,
		refID:  id,
		memo:   "Landed cost capitalized (" + lc.AllocationBasis + ")",
		branch: lc.BranchID,
		lines:  capitalizeLines(capTotal, cogsTotal),
	})
	if err != nil {
		return err
	}
	return s.repo.WithinTx(ctx, func(tx pgx.Tx) error {
		return s.repo.SetStatusTx(ctx, tx, id, StatusPosted, journalID)
	})
}

// capitalizeLines builds the balanced capitalization journal legs: the on-hand value to inventory (1200), the
// un-capitalizable remainder (no on-hand / integer-cent rounding) to COGS (5000), against freight payable
// (2100). Zero legs are omitted so the journal never carries a no-op line.
func capitalizeLines(capCents, cogsCents int64) []glLine {
	lines := make([]glLine, 0, 3)
	if capCents != 0 {
		lines = append(lines, glLine{acct: acctInventory, debit: capCents, memo: "capitalize landed cost (on-hand)"})
	}
	if cogsCents != 0 {
		lines = append(lines, glLine{acct: acctCOGS, debit: cogsCents, memo: "landed cost with no on-hand to capitalize into"})
	}
	if capCents+cogsCents != 0 {
		lines = append(lines, glLine{acct: acctFreightPayable, credit: capCents + cogsCents, memo: "freight/duty/handling payable"})
	}
	return lines
}

// retroAdjust is the body of the frozen Service.RetroAdjust: allocate the late-freight amount across the
// received lines, then split each share by the still-on-hand ratio, capitalize the on-hand portion into
// inventory (flows to the NEXT COGS) and expense the sold-through portion straight to COGS now.
func (s *Service) retroAdjust(ctx context.Context, id string) error {
	lc, err := s.guardBranch(ctx, id)
	if err != nil {
		return err
	}
	if lc.Status != StatusDraft {
		return nil // idempotent no-op
	}
	lines, err := s.receivedLines(ctx, lc.POID)
	if err != nil {
		return err
	}
	onHand, err := s.repo.RetroOnHand(ctx, id)
	if err != nil {
		return err
	}
	total := lc.totalCadCents()
	allocs := allocate(total, lc.AllocationBasis, lines, lc.ExcludeSpecialBuys)

	// received base per line for the split denominator.
	receivedBase := make(map[string]float64, len(lines))
	for _, l := range lines {
		receivedBase[l.ProductID] = l.QtyBase
	}
	// Pre-split each share by the still-on-hand ratio (deterministic). The on-hand portion capitalizes; the
	// sold-through portion expenses to COGS. absorbed (below) may trim the on-hand portion further by the
	// integer-cent rounding remainder, that remainder also lands in COGS so GL 1200 == the subledger.
	type planLine struct {
		a         Allocation
		onHandCad int64
		soldCad   int64
	}
	plan := make([]planLine, len(allocs))
	for i, a := range allocs {
		oh, present := onHand[a.ProductID]
		if !present {
			oh = receivedBase[a.ProductID] // no capture → treat as fully on hand (nothing sold through)
		}
		onHandCad, soldCad := splitSoldThrough(a.AllocatedCadCents, oh, receivedBase[a.ProductID])
		plan[i] = planLine{a: a, onHandCad: onHandCad, soldCad: soldCad}
	}

	var capTotal, cogsTotal int64
	var alreadyFinal bool
	if err := s.repo.WithinTx(ctx, func(tx pgx.Tx) error {
		locked, err := s.repo.LockForUpdateTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.Status != StatusDraft {
			alreadyFinal = true
			return nil
		}
		existing, err := s.repo.AllocationsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		capitalized := false // seed rows carry 0/0; a real split leaves non-zero rows
		for _, a := range existing {
			if a.AllocatedCadCents != 0 || a.SoldThroughCadCents != 0 {
				capitalized = true
				break
			}
		}
		if capitalized {
			for _, a := range existing {
				capTotal += a.AllocatedCadCents
				cogsTotal += a.SoldThroughCadCents
			}
			return nil
		}
		if err := s.repo.ClearAllocationsTx(ctx, tx, id); err != nil { // drop the on-hand seed rows
			return err
		}
		for _, p := range plan {
			var absorbed int64
			if p.onHandCad != 0 {
				if absorbed, err = s.capitalize(ctx, tx, p.a.ProductID, lc.BranchID, p.onHandCad); err != nil {
					return err
				}
			}
			a := p.a
			a.AllocatedCadCents = absorbed
			a.SoldThroughCadCents = p.soldCad + (p.onHandCad - absorbed) // sold-through + rounding remainder → COGS
			capTotal += a.AllocatedCadCents
			cogsTotal += a.SoldThroughCadCents
			if err := s.repo.InsertAllocationTx(ctx, tx, id, a); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if alreadyFinal {
		return nil
	}

	lines2 := make([]glLine, 0, 3)
	if capTotal != 0 {
		lines2 = append(lines2, glLine{acct: acctInventory, debit: capTotal, memo: "capitalize on-hand portion"})
	}
	if cogsTotal != 0 {
		lines2 = append(lines2, glLine{acct: acctCOGS, debit: cogsTotal, memo: "sold-through portion to COGS"})
	}
	if capTotal+cogsTotal != 0 {
		lines2 = append(lines2, glLine{acct: acctFreightPayable, credit: capTotal + cogsTotal, memo: "late freight payable"})
	}
	journalID, err := s.postJournal(ctx, glJournal{
		key:    "landedcost.retro:" + id,
		source: glSourceAdjustment,
		refID:  id,
		memo:   "Landed cost retro-adjustment (capitalize/sold-through split)",
		branch: lc.BranchID,
		lines:  lines2,
	})
	if err != nil {
		return err
	}
	return s.repo.WithinTx(ctx, func(tx pgx.Tx) error {
		return s.repo.SetStatusTx(ctx, tx, id, StatusRetroAdjusted, journalID)
	})
}

// Void reverses an applied/retro-adjusted landed cost: it capitalizes the negative of every on-hand share
// (netting the inventory bump to zero) and posts the reversing journal. Idempotent, an already-VOIDED doc is
// a no-op.
func (s *Service) Void(ctx context.Context, id string) error {
	lc, err := s.guardBranch(ctx, id)
	if err != nil {
		return err
	}
	switch lc.Status {
	case StatusVoided:
		return nil
	case StatusPosted, StatusAllocated, StatusRetroAdjusted:
	default:
		return clientErr(409, "landedcost: cannot void a %s landed cost", lc.Status)
	}
	allocs, err := s.repo.Allocations(ctx, id)
	if err != nil {
		return err
	}
	var capTotal, soldTotal int64
	for _, a := range allocs {
		capTotal += a.AllocatedCadCents
		soldTotal += a.SoldThroughCadCents
	}

	// The reversing GL 1200 credit MUST be booked from what the subledger ACTUALLY reverses at void time, NOT
	// from the stored capTotal absorbed at apply (L8 MAJOR). If stock sold between Apply and Void, the reversing
	// capitalize can only pull value out of the units STILL on hand, capitalizeLanded clamps/floors at the
	// current on-hand, so it reverses less than capTotal. Booking the CR 1200 from the stale capTotal would
	// credit inventory more than the perpetual subledger dropped, diverging ledger from subledger (~+1c and worse
	// on a partial-sale void, unbounded once most stock is gone). So we capitalize FIRST, sum the value actually
	// reversed, and build the journal from that: CR 1200 == the real subledger delta, and the portion of the
	// original capitalization that can no longer be un-capitalized (it was sold through since apply) rolls into
	// the COGS credit so the reversing journal still balances against the full payable reversal.
	//
	// Crash-durability (L8 REGRESSION fix): the reversing capitalize (subledger reversal) + the reversing GL
	// journal + the status flip to VOIDED are MONEY-ATOMIC, all committed together, or none. The GL seam is
	// tx-aware (PostJournalTx over gl.Service.PostExternalTx), so the idempotent journal posts INSIDE this
	// pgx.Tx and commits or rolls back WITH the void: the atomicity argument is true by construction, not by
	// ordering. If any step fails, the whole tx ROLLS BACK, the journal and the subledger reversal are undone,
	// the doc stays un-VOIDED, and a retry re-runs the full void with CR 1200 booked exactly once. Riding the
	// void's own transaction also removes the second pool acquisition: the pool variant of the post opens its
	// own transaction while this closure holds one, so pool-size concurrent voids each held a connection and
	// waited for a second from the same pool until the action timeout (connection starvation by second
	// acquisition; no row lock need be involved, Postgres only sees idle-in-transaction sessions). The older
	// orders failed in opposite directions: posting after the capitalize+flip commit could strand a reversed
	// subledger with NO journal, and the alreadyVoided guard then short-circuited the retry, permanent
	// GL/subledger divergence; posting inside the closure but through the pool variant committed the journal in
	// its own transaction, so a failure after the post (the flip, the commit) orphaned a journal for a doc that
	// stayed un-VOIDED. Because the journal now commits only with the VOIDED flip, alreadyVoided can only see a
	// doc whose journal was already durably posted, so it never short-circuits an un-posted reversal.
	return s.repo.WithinTx(ctx, func(tx pgx.Tx) error {
		locked, err := s.repo.LockForUpdateTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if locked.Status == StatusVoided {
			return nil // a prior void already reversed + posted + committed, do not re-capitalize or re-post
		}
		var reversedTotal int64
		for _, a := range allocs {
			if a.AllocatedCadCents == 0 {
				continue
			}
			if a.HasPreApplyAvg {
				// Cent-exact inverse: restore the WAC to the snapshot captured at Apply (L8 MAJOR). This reproduces the
				// pre-Apply per-unit cost for ALL on-hand, including sub-unit fractions where the forward round()
				// quantized oldValue and the legacy reverse-via-negate could not reconstruct oldAvg (drifted ±cents).
				// restoreLandedCost returns the realized subledger delta (negative); accumulate its magnitude as the
				// CR 1200 leg. It measures against the CURRENT on-hand, so it reverses only value still in stock; the
				// sold-through portion rolls into the COGS credit below exactly as the negate path did.
				reversed, err := s.restore(ctx, tx, a.ProductID, lc.BranchID, a.PreApplyAvgUnitCost, a.AllocatedCadCents)
				if err != nil {
					return err
				}
				reversedTotal += -reversed // restore returns a negative delta on a void; accumulate magnitude
				continue
			}
			// Legacy fallback (rows applied before 0058 carry no snapshot): reverse by negating the absorbed amount.
			reversed, err := s.capitalize(ctx, tx, a.ProductID, lc.BranchID, -a.AllocatedCadCents)
			if err != nil {
				return err
			}
			reversedTotal += -reversed // capitalize returns a negative delta on a reversal; accumulate magnitude
		}
		// COGS credit soaks up (a) the original sold-through leg and (b) the portion of the apply-time
		// capitalization that could no longer be reversed out of inventory because it was sold through between
		// Apply and Void. This keeps CR(1200)+CR(5000) == DR(2100) == the full payable being reversed.
		// The MAJOR fix removes at most `absorbed` per allocation, so Σ reversedTotal ≤ capTotal by construction and
		// (capTotal − reversedTotal) is always ≥ 0. Guard explicitly anyway (L8 MINOR): a legacy/negate reversal that
		// somehow over-reversed would make (capTotal − reversedTotal) negative and construct a NEGATIVE COGS credit leg
		//, the exact un-postable-doc failure mode we are eradicating. Clamp reversedTotal at capTotal so the COGS leg
		// can never go negative. Both legs use the clamped value so CR(1200)+CR(5000) == DR(2100) stays balanced.
		if reversedTotal > capTotal {
			reversedTotal = capTotal
		}
		invCredit := reversedTotal
		cogsCredit := soldTotal + (capTotal - reversedTotal)
		rev := make([]glLine, 0, 3)
		if capTotal+soldTotal != 0 {
			rev = append(rev, glLine{acct: acctFreightPayable, debit: capTotal + soldTotal, memo: "reverse landed cost payable"})
		}
		if invCredit != 0 {
			rev = append(rev, glLine{acct: acctInventory, credit: invCredit, memo: "reverse capitalized landed cost"})
		}
		if cogsCredit != 0 {
			rev = append(rev, glLine{acct: acctCOGS, credit: cogsCredit, memo: "reverse sold-through COGS"})
		}
		// Post the reversing journal (idempotent on its key) through THIS transaction, before the flip: a failure
		// here returns an error, rolling back the capitalize, the journal and the (not-yet-written) status flip
		// together, the doc stays voidable and a retry re-posts the deduped journal and completes the void.
		journalRef, err := s.postJournalTx(ctx, tx, glJournal{
			key:    "landedcost.void:" + id,
			source: glSourceAdjustment,
			refID:  id,
			memo:   "Landed cost voided",
			branch: lc.BranchID,
			lines:  rev,
		})
		if err != nil {
			return err
		}
		return s.repo.SetStatusTx(ctx, tx, id, StatusVoided, journalRef)
	})
}

// - seam helpers -

func (s *Service) receivedLines(ctx context.Context, poID string) ([]ReceivedLine, error) {
	if s.stock == nil {
		return nil, clientErr(503, "landedcost: inventory seam not configured")
	}
	// A DRAFT landed cost may carry an empty/blank po_id (it was created before the PO was linked). Preview /
	// Apply / RetroAdjust all funnel their received-lines read through here; without this guard an empty po_id
	// reaches the store as a bad/absent UUID and surfaces as a bare 500. Return a clean 4xx instead (L8 MINOR):
	// the caller is asking to allocate a landed cost that has no PO to allocate across.
	if strings.TrimSpace(poID) == "" {
		return nil, clientErr(http.StatusUnprocessableEntity, "landedcost: landed cost has no po_id to allocate across")
	}
	return s.stock.ReceivedLinesForPO(ctx, poID)
}

// capitalize applies cadCents of landed cost to a product's weighted-average cost through the inventory seam
// and returns the value the subledger actually absorbed (see Capitalizer.CapitalizeLandedCostTx). A negative
// cadCents reverses (Void).
func (s *Service) capitalize(ctx context.Context, tx pgx.Tx, productID, branchID string, cadCents int64) (int64, error) {
	if s.stock == nil {
		return 0, clientErr(503, "landedcost: inventory seam not configured")
	}
	return s.stock.CapitalizeLandedCostTx(ctx, tx, productID, branchID, cadCents)
}

// capitalizeSnap is capitalize plus the exact pre-Apply WAC snapshot (and whether a cost row existed) that Apply
// persists on the allocation row for a cent-exact Void restore (L8 MAJOR).
func (s *Service) capitalizeSnap(ctx context.Context, tx pgx.Tx, productID, branchID string, cadCents int64) (int64, int64, bool, error) {
	if s.stock == nil {
		return 0, 0, false, clientErr(503, "landedcost: inventory seam not configured")
	}
	return s.stock.CapitalizeLandedCostSnapTx(ctx, tx, productID, branchID, cadCents)
}

// restore reverses a capitalization on Void by removing EXACTLY `absorbed` (the value the allocation capitalized at
// Apply), returning the realized subledger value delta (negative). The GENERAL cent-exact inverse for every intervening
// scenario; snapAvg drives the drift-0 restore when on-hand is unchanged since Apply (L8 MAJOR).
func (s *Service) restore(ctx context.Context, tx pgx.Tx, productID, branchID string, snapAvg, absorbed int64) (int64, error) {
	if s.stock == nil {
		return 0, clientErr(503, "landedcost: inventory seam not configured")
	}
	return s.stock.RestoreLandedCostTx(ctx, tx, productID, branchID, snapAvg, absorbed)
}

// glJournal / glLine are the module-internal journal shape the postJournal helper maps onto the frozen
// GLPoster seam.
type glJournal struct {
	key    string
	source string
	refID  string
	memo   string
	branch string
	lines  []glLine
}

type glLine struct {
	acct   string
	debit  int64
	credit int64
	memo   string
}

// postJournal posts a journal through the GL seam (idempotent on j.key). A nil GL seam or an empty/zero
// journal is a legitimate no-op (returns "").
func (s *Service) postJournal(ctx context.Context, j glJournal) (string, error) {
	if s.gl == nil || len(j.lines) == 0 {
		return "", nil
	}
	lines := make([]GLLine, len(j.lines))
	for i, l := range j.lines {
		lines[i] = GLLine{AccountCode: l.acct, DebitCents: l.debit, CreditCents: l.credit, Memo: l.memo}
	}
	return s.gl.PostJournal(ctx, GLJournal{
		Source:         j.source,
		SourceRefID:    j.refID,
		IdempotencyKey: j.key,
		Memo:           j.memo,
		BranchID:       j.branch,
		Lines:          lines,
	})
}

// postJournalTx is postJournal through the caller's transaction (idempotent on j.key): the journal's reads and
// writes ride tx, so a caller already holding a pool connection (Void's WithinTx closure, with the header and
// allocation row locks) never asks the shared pool for a second one, and the journal commits or rolls back with
// the caller's transaction. apply / retroAdjust post OUTSIDE their transactions (between two WithinTx calls)
// and stay on the pool variant. A nil GL seam or an empty/zero journal is a legitimate no-op (returns "").
func (s *Service) postJournalTx(ctx context.Context, tx pgx.Tx, j glJournal) (string, error) {
	if s.gl == nil || len(j.lines) == 0 {
		return "", nil
	}
	lines := make([]GLLine, len(j.lines))
	for i, l := range j.lines {
		lines[i] = GLLine{AccountCode: l.acct, DebitCents: l.debit, CreditCents: l.credit, Memo: l.memo}
	}
	return s.gl.PostJournalTx(ctx, tx, GLJournal{
		Source:         j.source,
		SourceRefID:    j.refID,
		IdempotencyKey: j.key,
		Memo:           j.memo,
		BranchID:       j.branch,
		Lines:          lines,
	})
}
