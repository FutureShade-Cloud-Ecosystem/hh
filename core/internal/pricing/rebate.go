package pricing

// rebate.go, DIB-69 CORE. Manufacturer rebate accrual and the per-vendor ledger it produces.
//
// WHAT "CORE" MEANS HERE, AND WHY THE LINE IS DRAWN WHERE IT IS
// ------------------------------------------------------------
// DIB-69 is three features wearing one ticket number: accrual, a vendor scorecard, and a
// rebate-sheet upload that authors programs in bulk. Only accrual is built here. The reason is not
// time, it is that accrual is the only one of the three that is WRONG BY OMISSION. A scorecard
// that does not exist yet can be built in October and will show September's numbers, because the
// accrual rows will be there. An accrual that did not run in September cannot be recovered in
// October: the sale is closed, the cost basis has moved, and the only record that a covered product
// went out the door is a line on a paid invoice nobody is going to re-walk. Every day without
// accrual is money that quietly stops being claimable.
//
// So: the ledger is built first, and the two read-side features are parked explicitly (REPORT-W4-A).
//
// THE ONE INVARIANT THIS FILE DEFENDS
// -----------------------------------
// A sale accrues AT MOST ONCE per program. Every event transport in this system is at-least-once -
// the jobs runner retries, a POS client resubmits a ticket after a network timeout, a re-run of a
// backfill re-reads the same orders. A double claim against a manufacturer is not a rounding error;
// it is a credibility problem that costs the rebate program itself. The unique index in 0112 is the
// real guard (a database constraint, not a Go check that a concurrent second caller races past),
// and AccrueSale is written to treat the constraint firing as SUCCESS-ALREADY-DONE rather than as
// an error, so a retry is a no-op instead of a failed job that retries forever.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Rebate rate bases (mirrors the 0112 CHECK).
const (
	RebatePctOfSale  = "PCT_OF_SALE"  // rate_bps × extended pre-tax SALE
	RebatePctOfCost  = "PCT_OF_COST"  // rate_bps × extended COST
	RebateAmtPerUnit = "AMT_PER_UNIT" // rate_cents_per_unit × qty in the product BASE uom
)

var rebateBases = map[string]bool{RebatePctOfSale: true, RebatePctOfCost: true, RebateAmtPerUnit: true}

// Accrual source types (mirrors the 0112 CHECK).
const (
	RebateSourcePOS        = "POS_SALE"
	RebateSourceOrder      = "ORDER"
	RebateSourceAdjustment = "ADJUSTMENT"
)

var rebateSourceTypes = map[string]bool{
	RebateSourcePOS: true, RebateSourceOrder: true, RebateSourceAdjustment: true,
}

// errAccrualExists is the "already accrued" signal, the 0112 unique index fired. It is NOT
// surfaced to callers as an error: AccrueSale converts it into a skipped line, because a retried
// sale must look like success or the retry never stops.
var errAccrualExists = errors.New("pricing: rebate already accrued for this source line")

// RebateProgram is one rebate_programs row.
type RebateProgram struct {
	ID               string     `json:"id"`
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	VendorID         string     `json:"vendor_id"`
	VendorName       string     `json:"vendor_name,omitempty"` // projection-only, from the list read
	ProductID        string     `json:"product_id,omitempty"`
	CategoryID       string     `json:"category_id,omitempty"`
	Basis            string     `json:"basis"`
	RateBps          *int64     `json:"rate_bps,omitempty"`
	RateCentsPerUnit *int64     `json:"rate_cents_per_unit,omitempty"`
	StartsAt         time.Time  `json:"starts_at"`
	EndsAt           *time.Time `json:"ends_at,omitempty"`
	Notes            string     `json:"notes"`
	IsActive         bool       `json:"is_active"`
	Version          int64      `json:"version"`
}

// covers reports whether a program is in force at t. ends_at NULL = open-ended (the annual program
// renewed in place), and the window is half-open for the same midnight-collision reason promotions
// are, see Promotion.withinWindow.
func (p RebateProgram) covers(t time.Time) bool {
	if !p.IsActive || t.Before(p.StartsAt) {
		return false
	}
	return p.EndsAt == nil || t.Before(*p.EndsAt)
}

// RebateAccrual is one rebate_accruals row.
type RebateAccrual struct {
	ID           string    `json:"id"`
	ProgramID    string    `json:"program_id"`
	ProgramCode  string    `json:"program_code,omitempty"` // projection-only, from the ledger read
	VendorID     string    `json:"vendor_id"`
	ProductID    string    `json:"product_id"`
	ProductSKU   string    `json:"product_sku,omitempty"` // projection-only
	BranchID     string    `json:"branch_id,omitempty"`
	SourceType   string    `json:"source_type"`
	SourceID     string    `json:"source_id,omitempty"`
	QtyBase      float64   `json:"qty_base"`
	BasisCents   int64     `json:"basis_cents"`
	AccrualCents int64     `json:"accrual_cents"`
	RateNote     string    `json:"rate_note"`
	AccruedAt    time.Time `json:"accrued_at"`
}

// RebateSaleLine is one sold line offered to the engine. It carries everything the three bases
// need and NOTHING that would make pricing read a sales table: the CALLER (POS, order fulfilment,
// a backfill) supplies the facts, the same way ResolveInput.CostCents is caller-supplied.
type RebateSaleLine struct {
	ProductID    string  `json:"product_id"`
	CategoryID   string  `json:"category_id"`
	VendorID     string  `json:"vendor_id,omitempty"` // optional filter: accrue only this vendor's programs
	QtyBase      float64 `json:"qty_base"`            // qty in the product BASE uom
	SaleCents    int64   `json:"sale_cents"`          // extended PRE-TAX sale for the line
	ExtCostCents int64   `json:"ext_cost_cents"`      // extended cost for the line
	BranchID     string  `json:"branch_id,omitempty"`
}

// RebateSaleEvent is one sale offered for accrual. SourceType + SourceID are the idempotency key
// together with the program and the product, see the invariant note at the top of this file.
type RebateSaleEvent struct {
	SourceType string           `json:"source_type"`
	SourceID   string           `json:"source_id"`
	OccurredAt time.Time        `json:"occurred_at"`
	Lines      []RebateSaleLine `json:"lines"`
	UserID     string           `json:"-"`
}

// RebateAccrualResult is what AccrueSale reports back: what it wrote, and, just as importantly -
// what it deliberately did not.
type RebateAccrualResult struct {
	Accrued      []RebateAccrual `json:"accrued"`
	SkippedCount int             `json:"skipped_count"` // already-accrued lines (a retry); NOT an error
	TotalCents   int64           `json:"total_cents"`
}

// VendorRebateLedger is the per-vendor read: the rows plus the totals a manager wants at the top.
type VendorRebateLedger struct {
	VendorID     string          `json:"vendor_id"`
	VendorName   string          `json:"vendor_name"`
	From         *time.Time      `json:"from,omitempty"`
	To           *time.Time      `json:"to,omitempty"`
	TotalCents   int64           `json:"total_cents"`
	AccrualCount int             `json:"accrual_count"`
	Rows         []RebateAccrual `json:"rows"`
}

// rebateStore is the DIB-69 persistence seam. Optional and flag-off, like promoStore.
type rebateStore interface {
	ListRebatePrograms(ctx context.Context, vendorID string, includeInactive bool) ([]RebateProgram, error)
	GetRebateProgram(ctx context.Context, id string) (RebateProgram, error)
	CreateRebateProgram(ctx context.Context, p RebateProgram) (RebateProgram, error)
	SaveRebateProgram(ctx context.Context, p RebateProgram, expectedVersion int64) (RebateProgram, error)
	DeleteRebateProgram(ctx context.Context, id string, expectedVersion int64) error

	// ProgramsForLine returns the ACTIVE programs whose coverage includes (product, category),
	// optionally narrowed to one vendor. Window filtering is done in Go (RebateProgram.covers) so
	// the boundary rule lives in one testable place.
	ProgramsForLine(ctx context.Context, productID, categoryID, vendorID string) ([]RebateProgram, error)
	// InsertAccrual returns errAccrualExists when the 0112 unique index fires.
	InsertAccrual(ctx context.Context, a RebateAccrual, userID string) (RebateAccrual, error)
	VendorLedger(ctx context.Context, vendorID string, from, to *time.Time) (VendorRebateLedger, error)
}

// WithRebateStore injects the DIB-69 store (additive, flag-off).
func (s *Service) WithRebateStore(r rebateStore) *Service { s.rebate = r; return s }

func (s *Service) rebateOK() error {
	if s.rebate == nil {
		return errNoDB
	}
	return nil
}

// accrualFor computes ONE program's claim on ONE sold line. This is the arithmetic the whole
// feature exists for, so it is a pure function with no store, no clock and no context, it can be
// table-tested exhaustively and it is the mutation target named in REPORT-W4-A.md.
//
// ROUNDING: half-up on the cents, via the same mulDivRoundHalfUp the price calcs use. Truncation
// was the alternative and is wrong in a specific, cumulative way, it biases EVERY claim downward,
// and across a year of lines that is a systematic under-claim against the manufacturer rather than
// noise that cancels. Half-up matches how the rest of this package turns bps into money, so a
// rebate on a sale never disagrees with the sale about what 2.5% of $19.99 is.
//
// A NEGATIVE BASIS (a returned line offered back as a negative sale) produces a negative accrual,
// which is exactly right: a return claws back the claim. The DB CHECK forbids storing that on a
// document source type, which is why AccrueSale refuses negative-basis lines rather than writing
// rows the constraint would reject, returns route through an ADJUSTMENT, deliberately, so a human
// signs for the clawback.
func accrualFor(p RebateProgram, l RebateSaleLine) (cents int64, basis int64, note string) {
	switch p.Basis {
	case RebatePctOfSale:
		if p.RateBps == nil {
			return 0, 0, ""
		}
		return mulDivRoundHalfUp(l.SaleCents, *p.RateBps, 10000), l.SaleCents,
			fmt.Sprintf("%s of sale", pctStr(*p.RateBps))
	case RebatePctOfCost:
		if p.RateBps == nil {
			return 0, 0, ""
		}
		return mulDivRoundHalfUp(l.ExtCostCents, *p.RateBps, 10000), l.ExtCostCents,
			fmt.Sprintf("%s of cost", pctStr(*p.RateBps))
	case RebateAmtPerUnit:
		if p.RateCentsPerUnit == nil {
			return 0, 0, ""
		}
		// Base qty is fractional (tonnes, square feet), so this is a float multiply rounded through
		// the same half-up helper the money paths use rather than an int64 multiply that would
		// silently drop the fraction of a tonne.
		return roundCents(l.QtyBase * float64(*p.RateCentsPerUnit)), l.SaleCents,
			fmt.Sprintf("%s per base unit", moneyStr(*p.RateCentsPerUnit))
	}
	return 0, 0, ""
}

// AccrueSale is the "accrue on sale" entry point. It is IDEMPOTENT by construction: a line whose
// (program, source, product) row already exists is counted as skipped, not failed, so a retried
// delivery converges instead of looping.
func (s *Service) AccrueSale(ctx context.Context, ev RebateSaleEvent) (RebateAccrualResult, error) {
	if err := s.rebateOK(); err != nil {
		return RebateAccrualResult{}, err
	}
	if !rebateSourceTypes[ev.SourceType] {
		return RebateAccrualResult{}, badReq("source_type must be one of POS_SALE, ORDER, ADJUSTMENT")
	}
	if ev.SourceType != RebateSourceAdjustment && ev.SourceID == "" {
		return RebateAccrualResult{}, badReq("source_id is required for a %s accrual, without it the ledger cannot be reconciled against the sale", ev.SourceType)
	}
	if len(ev.Lines) == 0 {
		return RebateAccrualResult{}, badReq("at least one line is required")
	}
	at := ev.OccurredAt
	if at.IsZero() {
		at = s.now()
	}

	out := RebateAccrualResult{}
	for _, l := range ev.Lines {
		if l.ProductID == "" {
			return RebateAccrualResult{}, badReq("every line needs a product_id")
		}
		if l.SaleCents < 0 || l.ExtCostCents < 0 || l.QtyBase < 0 {
			// See accrualFor: a clawback is an ADJUSTMENT a human signs for, not a silently
			// negative accrual smuggled in on a sale event.
			return RebateAccrualResult{}, unprocessable("negative qty/sale/cost is not accruable on a %s, post an ADJUSTMENT instead", ev.SourceType)
		}
		programs, err := s.rebate.ProgramsForLine(ctx, l.ProductID, l.CategoryID, l.VendorID)
		if err != nil {
			return RebateAccrualResult{}, fmt.Errorf("rebate programs: %w", err)
		}
		// Deterministic order so a multi-program line writes its rows in the same sequence every
		// run, which is what makes the ledger diffable between a run and its retry.
		sort.SliceStable(programs, func(i, j int) bool { return programs[i].Code < programs[j].Code })
		for _, p := range programs {
			if !p.covers(at) {
				continue
			}
			cents, basis, note := accrualFor(p, l)
			if cents == 0 {
				// A zero claim is not a claim. Writing it would fill the ledger with rows that make
				// "which products earn rebate" unanswerable at a glance.
				continue
			}
			row := RebateAccrual{
				ProgramID: p.ID, VendorID: p.VendorID, ProductID: l.ProductID,
				BranchID: l.BranchID, SourceType: ev.SourceType, SourceID: ev.SourceID,
				QtyBase: l.QtyBase, BasisCents: basis, AccrualCents: cents,
				RateNote: note, AccruedAt: at,
			}
			saved, err := s.rebate.InsertAccrual(ctx, row, ev.UserID)
			if errors.Is(err, errAccrualExists) {
				out.SkippedCount++
				continue
			}
			if err != nil {
				return RebateAccrualResult{}, fmt.Errorf("insert accrual: %w", err)
			}
			out.Accrued = append(out.Accrued, saved)
			out.TotalCents += saved.AccrualCents
		}
	}
	return out, nil
}

// VendorRebateLedger reads one vendor's accruals. This is the "accrual ledger readable per vendor"
// half of DIB-69 core.
func (s *Service) VendorRebateLedger(ctx context.Context, vendorID string, from, to *time.Time) (VendorRebateLedger, error) {
	if err := s.rebateOK(); err != nil {
		return VendorRebateLedger{}, err
	}
	if vendorID == "" {
		return VendorRebateLedger{}, badReq("vendor_id is required")
	}
	return s.rebate.VendorLedger(ctx, vendorID, from, to)
}

// - program admin -

func (s *Service) ListRebatePrograms(ctx context.Context, vendorID string, includeInactive bool) ([]RebateProgram, error) {
	if err := s.rebateOK(); err != nil {
		return nil, err
	}
	return s.rebate.ListRebatePrograms(ctx, vendorID, includeInactive)
}

func (s *Service) CreateRebateProgram(ctx context.Context, p RebateProgram, userID string) (RebateProgram, error) {
	if err := s.rebateOK(); err != nil {
		return RebateProgram{}, err
	}
	normalizeRebate(&p)
	if err := validateRebateProgram(p); err != nil {
		return RebateProgram{}, err
	}
	p.IsActive = true
	out, err := s.rebate.CreateRebateProgram(ctx, p)
	if err != nil {
		return RebateProgram{}, err
	}
	refType, refID := rebateRef(out)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "rebate_program", EntityID: out.ID, Action: "CREATE",
		FieldName: "code", NewValue: out.Code, UserID: userID,
		RefEntityType: refType, RefEntityID: refID,
	})
	return out, nil
}

func (s *Service) UpdateRebateProgram(ctx context.Context, id string, p RebateProgram, expectedVersion int64, userID string) (RebateProgram, error) {
	if err := s.rebateOK(); err != nil {
		return RebateProgram{}, err
	}
	cur, err := s.rebate.GetRebateProgram(ctx, id)
	if err != nil {
		return RebateProgram{}, err
	}
	// A program's VENDOR is immutable. Changing it would silently re-attribute every accrual the
	// program has already written (rebate_accruals.vendor_id is denormalised from here), turning one
	// vendor's ledger into another's without a single row being updated.
	p.ID, p.VendorID = id, cur.VendorID
	normalizeRebate(&p)
	if err := validateRebateProgram(p); err != nil {
		return RebateProgram{}, err
	}
	out, err := s.rebate.SaveRebateProgram(ctx, p, expectedVersion)
	if err != nil {
		return RebateProgram{}, err
	}
	refType, refID := rebateRef(out)
	for _, e := range rebateDiff(cur, out) {
		e.UserID = userID
		e.RefEntityType, e.RefEntityID = refType, refID
		_ = s.store.AppendAudit(ctx, e)
	}
	return out, nil
}

func (s *Service) DeleteRebateProgram(ctx context.Context, id string, expectedVersion int64, userID string) error {
	if err := s.rebateOK(); err != nil {
		return err
	}
	cur, err := s.rebate.GetRebateProgram(ctx, id)
	if err != nil {
		return err
	}
	if err := s.rebate.DeleteRebateProgram(ctx, id, expectedVersion); err != nil {
		return err
	}
	refType, refID := rebateRef(cur)
	_ = s.store.AppendAudit(ctx, AuditEntry{
		EntityType: "rebate_program", EntityID: id, Action: "DELETE",
		FieldName: "is_active", OldValue: "true", NewValue: "false", UserID: userID,
		RefEntityType: refType, RefEntityID: refID,
	})
	return nil
}

func rebateRef(p RebateProgram) (string, string) {
	switch {
	case p.ProductID != "":
		return RefProduct, p.ProductID
	case p.CategoryID != "":
		return RefCategory, p.CategoryID
	default:
		// A vendor is not in the DIB-402 ref set, there is no vendor screen in this app, so a
		// vendor-wide program's audit rows ship with no link, exactly like a TIER_DEFAULT rule.
		return "", ""
	}
}

func normalizeRebate(p *RebateProgram) {
	if p.Basis == RebateAmtPerUnit {
		p.RateBps = nil
	} else {
		p.RateCentsPerUnit = nil
	}
}

func validateRebateProgram(p RebateProgram) error {
	if p.Code == "" || p.Name == "" {
		return badReq("code and name are required")
	}
	if p.VendorID == "" {
		return badReq("vendor_id is required, a rebate is a claim against someone")
	}
	if !rebateBases[p.Basis] {
		return unprocessable("basis must be one of PCT_OF_SALE, PCT_OF_COST, AMT_PER_UNIT")
	}
	if p.ProductID != "" && p.CategoryID != "" {
		return unprocessable("a rebate program covers a product OR a category, not both")
	}
	if p.StartsAt.IsZero() {
		return unprocessable("starts_at is required, a program with no start accrues on history the moment it is created")
	}
	if p.EndsAt != nil && !p.EndsAt.After(p.StartsAt) {
		return unprocessable("ends_at must be after starts_at")
	}
	switch p.Basis {
	case RebatePctOfSale, RebatePctOfCost:
		if p.RateBps == nil {
			return unprocessable("%s requires rate_bps", p.Basis)
		}
		if err := validateBps("rate_bps", p.RateBps); err != nil {
			return err
		}
	case RebateAmtPerUnit:
		if p.RateCentsPerUnit == nil {
			return unprocessable("AMT_PER_UNIT requires rate_cents_per_unit")
		}
		if *p.RateCentsPerUnit < 0 {
			return unprocessable("rate_cents_per_unit must be >= 0")
		}
	}
	return nil
}

func rebateDiff(cur, next RebateProgram) []AuditEntry {
	var out []AuditEntry
	add := func(field, o, n string) {
		if o != n {
			out = append(out, AuditEntry{EntityType: "rebate_program", EntityID: next.ID, Action: "UPDATE", FieldName: field, OldValue: o, NewValue: n})
		}
	}
	add("name", cur.Name, next.Name)
	add("basis", cur.Basis, next.Basis)
	add("rate_bps", i64s(cur.RateBps), i64s(next.RateBps))
	add("rate_cents_per_unit", i64s(cur.RateCentsPerUnit), i64s(next.RateCentsPerUnit))
	add("is_active", fmt.Sprintf("%t", cur.IsActive), fmt.Sprintf("%t", next.IsActive))
	return out
}
