package warranty

import (
	"context"
	"fmt"
	"strings"
)

// moneypath.go, ERP:warranty-p2. PER-OUTCOME SETTLEMENT MONEY-PATH WIRING.
//
// P1 recorded a settlement and posted ONE generic journal for the two outcomes that happened to move cash. The
// other two money consequences were comments. This file makes each of the four outcomes execute its OWN named
// money path, and makes the path a claim took a ROW (warranty_settlement_postings, 0129) rather than something
// you infer from the outcome column.
//
//	VENDOR_CREDIT          → VENDOR_RECOVERY       (GL seam)        DR clearing / CR warranty recovery,
//	                                                                AND accrues the outstanding vendor balance
//	                                                                the WTY-RECOVERY board (DIB-221) chases.
//	CUSTOMER_ACCOMMODATION → CUSTOMER_CREDIT       (BILLING seam)   billing issues the credit memo and owns its
//	                                                                journal; warranty records the document ref.
//	REPLACEMENT_STOCK      → INVENTORY_REPLACEMENT (INVENTORY seam) inventory owns the cost movement; warranty
//	                                                                records the movement ref. Never a cash
//	                                                                journal from here.
//	WRITE_OFF (denied)     → EXPENSE_WRITE_OFF     (GL seam)        DR warranty expense / CR clearing.
//
// THE MAPPING IS DATA. It comes from warranty_outcome_paths, not from a switch statement, because the account
// pair is precisely what the accountant has not ratified (PRD §9 Q5) and "every tunable is a DB row". An
// outcome with no configured path is REFUSED rather than defaulted: inventing a money path for money is worse
// than declining to move it.
//
// CONSUMER-DECLARED SEAMS. Warranty imports no peer. The two non-GL paths are ports declared HERE, in the
// consuming package, and bound by an adapter in main.go, the order/quote/dispatch/tasks precedent. Unbound,
// a path records SEAM_UNWIRED with the EXACT request it would have sent, so the union binds one adapter and
// replays, and an operator meanwhile can see what is owed and to whom. The customer-credit path additionally
// carries a FALLBACK: an accommodation is warranty EXPENSE however the credit is delivered, so when billing is
// unbound warranty posts the expense journal itself and says so (FALLBACK_GL, deliberately not POSTED, the
// books are right, the document the customer expects does not exist yet). The replacement path has no fallback
// by design: fabricating a cost movement for stock that may never have left the yard would be a lie in the
// inventory valuation, and a SEAM_UNWIRED row is the honest answer.

// - consumer-declared seams (adapters land in main.go) -

// CustomerCreditIssuer is the BILLING seam: issue the customer a credit memo for an accommodation settlement.
// Declared here (never an import of internal/billing) so warranty depends on a shape it owns.
//
// Exact signature for the union adapter:
//
//	IssueCustomerCredit(ctx context.Context, in CustomerCreditRequest) (CustomerCreditResult, error)
type CustomerCreditIssuer interface {
	IssueCustomerCredit(ctx context.Context, in CustomerCreditRequest) (CustomerCreditResult, error)
}

// CustomerCreditRequest is what warranty hands billing. AmountCents is always positive, the SIGN is billing's
// business (a credit memo is a negative invoice in its own ledger, and encoding that here would put billing's
// convention in warranty's head).
type CustomerCreditRequest struct {
	CustomerID     string // may be "" for a WEB claim that triage has not linked to a customer yet
	BranchID       string
	ClaimID        string
	ClaimNo        string
	AmountCents    int64
	Memo           string
	IdempotencyKey string // "warranty-credit:<claimID>", a retry must never raise a second memo
}

// CustomerCreditResult is billing's answer: the document the customer will see.
type CustomerCreditResult struct {
	CreditMemoID string
	CreditMemoNo string
	GLJournalID  string // billing's own journal, when it posted one
}

// InventoryCostMover is the INVENTORY seam: book the cost of the replacement stock leaving the yard.
//
// Exact signature for the union adapter:
//
//	MoveReplacementCost(ctx context.Context, in ReplacementCostRequest) (ReplacementCostResult, error)
type InventoryCostMover interface {
	MoveReplacementCost(ctx context.Context, in ReplacementCostRequest) (ReplacementCostResult, error)
}

// ReplacementCostRequest is what warranty hands inventory. Lines carry the claim's products + quantities; when
// a claim line is free text (a WEB intake naming "the grey pavers"), ProductID is "" and inventory refuses that
// line rather than guessing, a replacement whose product nobody identified is a triage failure, not a stock
// movement.
type ReplacementCostRequest struct {
	BranchID       string
	ClaimID        string
	ClaimNo        string
	AmountCents    int64 // the recorded value of the replacement (the settlement's realized amount)
	Lines          []ReplacementLine
	Memo           string
	IdempotencyKey string // "warranty-replace:<claimID>"
}

// ReplacementLine is one product+qty going back out for a replacement.
type ReplacementLine struct {
	ProductID   string
	ProductText string
	Qty         string // DECIMAL as string, never a float for a quantity
	UOMCode     string
}

// ReplacementCostResult is inventory's answer: the movement it recorded.
type ReplacementCostResult struct {
	MovementRef string
	GLJournalID string
}

// WithCustomerCredit binds the BILLING credit-memo seam. Unbound, CUSTOMER_ACCOMMODATION settlements record
// SEAM_UNWIRED and fall back to the warranty-expense journal so the books stay right.
func (s *Service) WithCustomerCredit(c CustomerCreditIssuer) *Service { s.credit = c; return s }

// WithInventoryCost binds the INVENTORY replacement-cost seam. Unbound, REPLACEMENT_STOCK settlements record
// SEAM_UNWIRED with the exact request, and post nothing, because there is no honest fallback.
func (s *Service) WithInventoryCost(m InventoryCostMover) *Service { s.invCost = m; return s }

// - money-path statuses (mirror the 0129 CHECK) -

const (
	PathPosted      = "POSTED"       // the path completed and produced a ref
	PathNoEffect    = "NO_EFFECT"    // the path ran and correctly produced nothing (zero amount)
	PathSeamUnwired = "SEAM_UNWIRED" // the owning seam is not bound here; the request is preserved
	PathFallbackGL  = "FALLBACK_GL"  // seam unbound, warranty posted the fallback journal itself
	PathFailed      = "FAILED"       // the seam was bound and refused
	PathManual      = "MANUAL"       // P1 manual settlement used as an explicit override
)

// Money-path names (mirror the 0129 CHECK).
const (
	PathVendorRecovery       = "VENDOR_RECOVERY"
	PathCustomerCredit       = "CUSTOMER_CREDIT"
	PathInventoryReplacement = "INVENTORY_REPLACEMENT"
	PathExpenseWriteOff      = "EXPENSE_WRITE_OFF"
)

// Seam owners (mirror the 0129 CHECK).
const (
	SeamGL        = "GL"
	SeamBilling   = "BILLING"
	SeamInventory = "INVENTORY"
)

// - the executor -

// executeMoneyPath runs the ONE money path configured for a settlement's outcome and returns the posting row
// that describes what happened. It never returns an error for a path that could not complete: a settlement is
// already durably recorded by the time this runs (the P1 rule, a money-path failure must never un-book a
// settlement the manager was told succeeded), so every failure mode is expressed as a STATUS on the row. The
// only error it returns is a CONFIGURATION error (no path row for this outcome), and even that is surfaced by
// the caller as a note rather than a rollback.
//
// manualOverride short-circuits the whole thing: the manager is telling us the money was booked by hand.
func (s *Service) executeMoneyPath(ctx context.Context, claim Claim, lines []ClaimLine, outcome string, amountCents int64, manualOverride bool, overrideNote string) (SettlementPosting, error) {
	cfg, err := s.store.OutcomePath(ctx, outcome)
	if err != nil {
		return SettlementPosting{}, err
	}

	p := SettlementPosting{
		ClaimID:         claim.ID,
		Outcome:         outcome,
		MoneyPath:       cfg.MoneyPath,
		Seam:            cfg.Seam,
		AmountCents:     amountCents,
		AccruesRecovery: cfg.AccruesRecovery,
	}

	if manualOverride {
		// P1's manual settlement, kept as the override. Nothing is posted and nothing is accrued: an accrual
		// for money the bookkeeper already collected would put the claim on the recovery worklist forever.
		p.Status = PathManual
		p.AccruesRecovery = false
		p.Note = strings.TrimSpace(overrideNote)
		if p.Note == "" {
			p.Note = "manual settlement override, money booked outside the module"
		}
		return p, nil
	}

	if amountCents <= 0 {
		// A settlement that moved no money. The path still ran and this row is its receipt, that is the
		// difference between "no journal because nothing was owed" and "no journal because something broke",
		// which P1 could not tell apart.
		p.Status = PathNoEffect
		p.AccruesRecovery = false
		p.Note = "settlement records no amount, no money to move"
		return p, nil
	}

	switch cfg.MoneyPath {
	case PathVendorRecovery, PathExpenseWriteOff:
		s.runGLPath(ctx, claim, cfg, &p)
	case PathCustomerCredit:
		s.runCustomerCreditPath(ctx, claim, cfg, &p)
	case PathInventoryReplacement:
		s.runReplacementPath(ctx, claim, lines, &p)
	default:
		// Unreachable while the 0129 CHECK and this switch agree; if they ever diverge, refuse rather than
		// silently record a settlement whose money went nowhere.
		return SettlementPosting{}, fmt.Errorf("warranty: no executor for money path %q", cfg.MoneyPath)
	}
	return p, nil
}

// runGLPath posts the journal warranty owns itself (vendor recovery, or the write-off expense). Account codes
// come from the 0129 config row, never from a constant here.
func (s *Service) runGLPath(ctx context.Context, claim Claim, cfg MoneyPathConfig, p *SettlementPosting) {
	p.Request = map[string]any{
		"debit_account":  cfg.DebitAccount,
		"credit_account": cfg.CreditAccount,
		"amount_cents":   p.AmountCents,
		"branch_id":      claim.BranchID,
	}
	if s.gl == nil {
		// The GL seam has no fallback below it, it IS the bottom. Record the request so the union can replay.
		p.Status = PathSeamUnwired
		p.Note = "GL seam unwired, settlement recorded without a journal"
		return
	}
	journalID, err := s.gl.PostJournal(ctx, s.buildJournal(claim, cfg, p.MoneyPath, p.AmountCents))
	switch {
	case err != nil:
		p.Status = PathFailed
		p.ErrorText = err.Error()
		p.Note = "GL post failed, settlement recorded without a journal"
		s.log.Warn("warranty: money-path GL post failed", "claim", claim.ClaimNo, "path", p.MoneyPath, "err", err)
	case journalID == "":
		// A provider that accepted the journal but returned no id: treat as failed rather than POSTED, because
		// the 0129 CHECK requires a POSTED row to show its work and a blank id is not work shown.
		p.Status = PathFailed
		p.ErrorText = "GL provider returned an empty journal id"
		p.Note = "GL post returned no journal id, settlement recorded without a journal"
	default:
		p.Status = PathPosted
		p.GLJournalID = journalID
		p.Note = "posted " + cfg.DebitAccount + " / " + cfg.CreditAccount
	}
	if p.Status != PathPosted {
		// An accrual only exists once the recovery is actually ON the books. Accruing off a failed post would
		// put money on the recovery board that the ledger has never heard of.
		p.AccruesRecovery = false
	}
}

// runCustomerCreditPath hands the accommodation to billing. Unbound, it falls back to the warranty-expense
// journal, the expense is real regardless of who raises the paperwork.
func (s *Service) runCustomerCreditPath(ctx context.Context, claim Claim, cfg MoneyPathConfig, p *SettlementPosting) {
	req := CustomerCreditRequest{
		CustomerID:     claim.CustomerID,
		BranchID:       claim.BranchID,
		ClaimID:        claim.ID,
		ClaimNo:        claim.ClaimNo,
		AmountCents:    p.AmountCents,
		Memo:           "Warranty accommodation, claim " + claim.ClaimNo,
		IdempotencyKey: "warranty-credit:" + claim.ID,
	}
	p.Request = map[string]any{
		"seam":            "billing.IssueCustomerCredit",
		"customer_id":     req.CustomerID,
		"branch_id":       req.BranchID,
		"amount_cents":    req.AmountCents,
		"memo":            req.Memo,
		"idempotency_key": req.IdempotencyKey,
	}
	if s.credit == nil {
		s.fallbackToGL(ctx, claim, cfg, p, "billing credit-memo seam unwired")
		return
	}
	res, err := s.credit.IssueCustomerCredit(ctx, req)
	if err != nil {
		// A BOUND seam that refused is NOT a fallback case: billing may have partially created the memo, and
		// posting the expense ourselves on top of that is how a double-book happens. Record the failure.
		p.Status = PathFailed
		p.ErrorText = err.Error()
		p.Note = "billing refused the credit memo, settlement recorded, no credit issued"
		s.log.Warn("warranty: customer-credit seam refused", "claim", claim.ClaimNo, "err", err)
		return
	}
	ref := res.CreditMemoNo
	if ref == "" {
		ref = res.CreditMemoID
	}
	if ref == "" {
		p.Status = PathFailed
		p.ErrorText = "billing returned no credit-memo reference"
		p.Note = "billing accepted the credit memo but named no document"
		return
	}
	p.Status = PathPosted
	p.ExternalRef = ref
	p.GLJournalID = res.GLJournalID
	p.Note = "customer credit memo " + ref
}

// runReplacementPath hands the replacement to inventory. There is deliberately NO fallback: see the file header.
func (s *Service) runReplacementPath(ctx context.Context, claim Claim, lines []ClaimLine, p *SettlementPosting) {
	req := ReplacementCostRequest{
		BranchID:       claim.BranchID,
		ClaimID:        claim.ID,
		ClaimNo:        claim.ClaimNo,
		AmountCents:    p.AmountCents,
		Lines:          replacementLines(lines),
		Memo:           "Warranty replacement, claim " + claim.ClaimNo,
		IdempotencyKey: "warranty-replace:" + claim.ID,
	}
	reqLines := make([]map[string]any, 0, len(req.Lines))
	for _, l := range req.Lines {
		reqLines = append(reqLines, map[string]any{
			"product_id": l.ProductID, "product_text": l.ProductText, "qty": l.Qty, "uom_code": l.UOMCode,
		})
	}
	p.Request = map[string]any{
		"seam":            "inventory.MoveReplacementCost",
		"branch_id":       req.BranchID,
		"amount_cents":    req.AmountCents,
		"lines":           reqLines,
		"memo":            req.Memo,
		"idempotency_key": req.IdempotencyKey,
	}
	if s.invCost == nil {
		p.Status = PathSeamUnwired
		p.Note = "inventory replacement-cost seam unwired, no cash journal is posted for a replacement by design"
		return
	}
	res, err := s.invCost.MoveReplacementCost(ctx, req)
	if err != nil {
		p.Status = PathFailed
		p.ErrorText = err.Error()
		p.Note = "inventory refused the replacement cost movement"
		s.log.Warn("warranty: replacement-cost seam refused", "claim", claim.ClaimNo, "err", err)
		return
	}
	if res.MovementRef == "" && res.GLJournalID == "" {
		p.Status = PathFailed
		p.ErrorText = "inventory returned no movement reference"
		p.Note = "inventory accepted the replacement but named no movement"
		return
	}
	p.Status = PathPosted
	p.ExternalRef = res.MovementRef
	p.GLJournalID = res.GLJournalID
	p.Note = "replacement cost movement " + res.MovementRef
}

// fallbackToGL posts the configured fallback pair when a non-GL seam is unbound. Reported as FALLBACK_GL, never
// POSTED: the ledger is right and the customer's document does not exist, and conflating the two would hide a
// missing credit memo behind a green tick.
func (s *Service) fallbackToGL(ctx context.Context, claim Claim, cfg MoneyPathConfig, p *SettlementPosting, why string) {
	if s.gl == nil || cfg.DebitAccount == "" || cfg.CreditAccount == "" {
		p.Status = PathSeamUnwired
		p.Note = why + "; no GL fallback available, settlement recorded only"
		return
	}
	journalID, err := s.gl.PostJournal(ctx, s.buildJournal(claim, cfg, p.MoneyPath, p.AmountCents))
	if err != nil || journalID == "" {
		p.Status = PathSeamUnwired
		if err != nil {
			p.ErrorText = err.Error()
		} else {
			p.ErrorText = "GL provider returned an empty journal id"
		}
		p.Note = why + "; GL fallback also failed, settlement recorded only"
		return
	}
	p.Status = PathFallbackGL
	p.GLJournalID = journalID
	p.Note = why + ", warranty posted the fallback expense journal " + cfg.DebitAccount + " / " + cfg.CreditAccount
}

// buildJournal assembles the two-legged journal for a money path from its CONFIGURED account pair.
func (s *Service) buildJournal(claim Claim, cfg MoneyPathConfig, path string, amountCents int64) GLJournal {
	return GLJournal{
		Source:      "ADJUSTMENT",
		SourceRefID: claim.ID,
		// Unchanged from P1 on purpose: re-settlement is refused, so one key per claim is exactly one journal,
		// and keeping the key stable means a claim settled before this wave cannot be double-posted after it.
		IdempotencyKey: "warranty-settle:" + claim.ID,
		Memo:           "Warranty settlement (" + path + "), claim " + claim.ClaimNo,
		BranchID:       claim.BranchID,
		Lines: []GLLine{
			{AccountCode: cfg.DebitAccount, DebitCents: amountCents, Memo: strings.ToLower(path) + " debit"},
			{AccountCode: cfg.CreditAccount, CreditCents: amountCents, Memo: strings.ToLower(path) + " credit"},
		},
	}
}

// replacementLines projects claim lines onto the inventory seam's shape.
func replacementLines(lines []ClaimLine) []ReplacementLine {
	out := make([]ReplacementLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, ReplacementLine{
			ProductID: l.ProductID, ProductText: l.ProductText, Qty: l.Qty, UOMCode: l.UOMCode,
		})
	}
	return out
}

// SettlementPostings returns the money-path rows recorded for a claim (branch-scoped, like every other claim
// read), the claim card shows "what the money actually did".
func (s *Service) SettlementPostings(ctx context.Context, claimID string) ([]SettlementPosting, error) {
	if _, _, err := s.callerScopedClaim(ctx, claimID); err != nil {
		return nil, err
	}
	return s.store.PostingsForClaim(ctx, claimID)
}
