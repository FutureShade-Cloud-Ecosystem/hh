// Package quoteassist is the quote-builder's assist layer: DIB-66 (SP-01) item extraction from a
// free-text job description, and DIB-67 (SP-04) scope-gap detection over a quote's lines.
//
// -----------------------------------------------------------------------------------------------
// THE TWO INVARIANTS THIS PACKAGE EXISTS TO HOLD
// -----------------------------------------------------------------------------------------------
//
//  1. NOTHING HERE WRITES A QUOTE LINE.
//
// Extraction produces PROPOSALS. A proposal is a row in a review list with a confidence band and
// a reject affordance; a rep confirms it line by line, and the confirmation is an ordinary
// POST /api/v1/quotes/{id}/lines through the shipped, audited, priced, stock-holding write path
// (quote.StaffService.AddLine). This package has no writer seam, no quote import and no INSERT
// against quote_lines, which is why "it never auto-adds" is a property of the import graph
// rather than a promise in a document. TestExtract_PackageHasNoQuoteWriter pins it by scanning
// this package's own source, and TestIntegration_Extract_AddsNoLines pins it against a real
// database by extracting over a real quote and counting the lines afterwards (still zero).
//
//  2. THE MODEL NEVER NAMES A PRODUCT.
//
// The optional AI path (see AIExtractor) is allowed to do exactly one thing: split a paragraph
// into candidate phrases and read the quantities out of them. Every product reference is then
// resolved by the DETERMINISTIC resolver against the real catalogue, through the same ranked
// counter search the POS counter uses. A provider that hallucinates a SKU, a product id, or a
// product that this yard does not sell cannot put it on a quote, because the field it would have
// to fill is filled from the catalogue read and from nowhere else.
// TestExtract_AIPhraseCannotNameAProduct pins it.
//
// -----------------------------------------------------------------------------------------------
// DETERMINISTIC FIRST, AI OPTIONAL (D-075)
// -----------------------------------------------------------------------------------------------
// The extractor works with no provider, no key and no network, and that is its DEFAULT and its
// tested path. The AI path is a segmentation aid: it is off unless the request asks for it, it
// runs only through the assist package's rails (budget resolved, spend checked, ledger row
// appended, internal/assist/adapters/quoteextract.go), and when it is over budget, unwired or
// broken it DEGRADES to the deterministic segmentation and says so in one plain sentence. There
// is no state of this feature in which a missing provider is an error.
//
// SP-04 needs no AI at all. A scope rule is a row and the evaluator is a pure function.
//
// -----------------------------------------------------------------------------------------------
// WHAT THIS PACKAGE IMPORTS
// -----------------------------------------------------------------------------------------------
// internal/assist (the Caller type and ErrOverBudget, the SAME values the rails use, not copies
// of them), internal/identity and internal/platform/httpx in the handler, and nothing else. No
// product, no quote, no pricing. Both domain reads are CONSUMER interfaces declared below in the
// language of the question, satisfied by thin adapters in internal/assist/adapters, the same
// arrangement, for the same reason, that keeps internal/assist domain-free.
package quoteassist

import (
	"context"
	"errors"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

// -- errors -------------------------------------------------------------------------------------

var (
	// ErrCatalogueUnwired: no catalogue search seam in this deployment. A 503, not a 500 -
	// nothing failed, a seam is not connected, and the honest answer names it (the assist
	// UnavailableError doctrine).
	ErrCatalogueUnwired = errors.New("quoteassist: the catalogue search seam is not wired in this deployment")

	// ErrQuoteLinesUnwired: no quote-lines seam, so scope gaps cannot be evaluated. Also a 503.
	ErrQuoteLinesUnwired = errors.New("quoteassist: the quote-lines seam is not wired in this deployment")

	// ErrRulesUnwired: no rule store. The suggestion panel renders nothing rather than guessing.
	ErrRulesUnwired = errors.New("quoteassist: the scope-rule store is not wired in this deployment")

	// ErrRuleNotFound: no rule by that code. A 404 on the admin surface.
	ErrRuleNotFound = errors.New("quoteassist: no scope rule with that code")

	// ErrAIUnavailable: the AI segmentation seam is not wired. NEVER surfaced as a failure -
	// Extract catches it and answers from the deterministic path with a note.
	ErrAIUnavailable = errors.New("quoteassist: AI-assisted extraction is not available")
)

// -- confidence ---------------------------------------------------------------------------------

// Confidence bands. THREE WORDS, NOT A NUMBER, and that is a decision rather than laziness.
//
// A proposal carrying "0.82" invites two questions nobody can answer: 0.82 of what, and what
// would 0.79 have meant? The number would be invented here, there is no calibration set behind
// it, and an invented number in front of somebody deciding what to charge a customer is worse
// than no number, because it looks like measurement. A band plus the EVIDENCE that produced it
// (Proposal.Why) is something a rep can actually act on: "matched the SKU exactly, quantity and
// unit read from your text" tells them whether to look twice.
const (
	// ConfidenceHigh: the catalogue matched on SKU or an exact term, AND a quantity was read,
	// AND the unit is one this product is actually sold in. Nothing is missing.
	ConfidenceHigh = "HIGH"
	// ConfidenceMedium: a solid name/alias match with a usable quantity and unit, or an exact
	// product match with one of the two missing.
	ConfidenceMedium = "MEDIUM"
	// ConfidenceLow: a fuzzy or description-level match, or no quantity, or a unit this product
	// is not sold in. Still shown, a low-confidence proposal a rep can fix in two keystrokes is
	// worth more than a row silently dropped (see Unmatched for the same argument).
	ConfidenceLow = "LOW"
)

// Extraction sources, carried on the answer so the surface can say which path ran.
const (
	SourceDeterministic = "deterministic"
	SourceAIAssisted    = "ai-assisted"
)

// -- what a request and an answer look like -----------------------------------------------------

// ExtractRequest is one paste of a job description.
//
// UseAI DEFAULTS TO FALSE because the zero value of a bool is false and that is the correct
// default for this feature: a rep who has not asked for the AI path must not spend a token on it.
// The field is named for what it asks for rather than for what it configures, so a client that
// omits it gets the deterministic engine and no surprise on the ledger.
type ExtractRequest struct {
	Text string `json:"text"`
	// BranchID scopes the on-hand figures the catalogue search returns with each hit. It does NOT
	// scope which products exist, a product is the same product at every branch, so an absent
	// branch narrows nothing and hides nothing; it only means the hits carry every branch's
	// on-hand instead of one.
	BranchID string `json:"branch_id"`
	UseAI    bool   `json:"use_ai"`
}

// Alternate is a runner-up catalogue hit for a proposal.
//
// Carried because the first pick being wrong is the common failure of any ranked search, and the
// difference between "delete this row and go and use the picker" and "click the other one" is the
// difference between a rep using this feature twice and using it once.
type Alternate struct {
	ProductID string `json:"product_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	// MatchReason is the search's own rank arm for THIS hit (EXACT / SKU_PREFIX / NAME / ALIAS /
	// FUZZY …), passed through untouched so the row can say why it is in the list.
	MatchReason string `json:"match_reason"`
	SellUOM     string `json:"sell_uom"`
	BaseUOM     string `json:"base_uom"`
}

// Proposal is ONE PROPOSED LINE. It is not a line. Nothing in this package can make it one.
type Proposal struct {
	// Ref is a stable, per-answer handle ("p1", "p2") the surface keys its confirm/reject state
	// on. Not the product id: the same product can legitimately appear twice in one description
	// ("120 sq ft of Blu 60 for the patio and 40 sq ft for the walk") and keying on the product
	// would make confirming one confirm both.
	Ref string `json:"ref"`
	// SourceText is the EXACT phrase this proposal came from, unedited. It is what makes the row
	// checkable in one glance, a rep compares the proposal to their own words, not to a
	// paraphrase of them.
	SourceText string `json:"source_text"`

	ProductID string `json:"product_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	// SellUOM / BaseUOM are the units the CATALOGUE declares for this product, the counter's
	// declared sell unit (products.sold_by, often blank) and the stocking unit. Carried so the
	// surface can say "quoting in SF" rather than echoing whatever the rep typed. Neither is the
	// authority on which units this product may be QUOTED in; product_uoms is, and it is consulted
	// per unit through CatalogueSearch.QuotableUnit (units.go header).
	SellUOM string `json:"sell_uom"`
	BaseUOM string `json:"base_uom"`

	// QtyEntered is the quantity as READ FROM THE TEXT, formatted the way the quote's own line
	// writer wants it (a decimal string). Empty when no quantity was found, never a fabricated 1.
	QtyEntered string `json:"qty_entered"`
	// UOMEntered is the unit to quote in: the unit the text named, when this product is sold in
	// it, and empty when the text named a unit this product is not sold in (see UnitNote).
	UOMEntered string `json:"uom_entered"`
	// UnitNote explains an empty or substituted unit in one sentence a rep can act on. Empty when
	// there is nothing to explain, never a placeholder.
	UnitNote string `json:"unit_note,omitempty"`

	Confidence  string `json:"confidence"`
	MatchReason string `json:"match_reason"`
	// Why is the evidence behind the band, in words, one clause per fact. Built from the SAME
	// values the band is computed from (confidenceFor), so the label and the explanation cannot
	// drift apart.
	Why []string `json:"why"`

	// NeedsQty / NeedsUOM are what the rep must supply before this row can be confirmed. They are
	// flags rather than an inferred emptiness check because the surface disables its confirm
	// affordance on them, and "why is this row not confirmable" must be answerable.
	NeedsQty bool `json:"needs_qty"`
	NeedsUOM bool `json:"needs_uom"`

	Alternates []Alternate `json:"alternates"`
}

// Unmatched is a phrase that produced no proposal, KEPT AND SHOWN.
//
// The temptation is to drop these, a shorter, cleaner list of things that worked. That is the
// one thing this feature must not do. A rep who pastes eleven items and is handed nine proposals
// has no way to know which two went missing, and will find out when the customer does. So every
// phrase the extractor could not turn into a proposal comes back with the original words and a
// reason in plain English, and the surface renders them under "couldn't match".
type Unmatched struct {
	SourceText string `json:"source_text"`
	// Reason names what was missing, in the words a rep would use. Never a code.
	Reason string `json:"reason"`
	// Query is the search term that found nothing, when that is what happened. Carried so the
	// surface can offer "search the catalogue for this" as one click instead of a retype.
	Query string `json:"query,omitempty"`
}

// Usage is what an AI-assisted extraction cost. Reported back exactly as the provider reported
// it, never estimated, the assist package's rule, restated here because this is a second
// consumer of the same ledger and a second estimator would make the ledger unreconcilable.
type Usage struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
}

// Extraction is the whole answer.
//
// Empty is a FIRST-CLASS state: an empty Text, or a paragraph in which nothing resolved, is
// answered with zero proposals and (in the second case) the unmatched rows that say why. Neither
// is an error, and neither may be rendered as one.
type Extraction struct {
	Proposals []Proposal  `json:"proposals"`
	Unmatched []Unmatched `json:"unmatched"`
	// Source is SourceDeterministic or SourceAIAssisted, which path actually produced the
	// phrases, not which one was requested. A request that asked for AI and got the deterministic
	// segmentation because the budget was spent says "deterministic" here, and says why in AINote.
	Source string `json:"source"`
	// AINote is the one plain sentence explaining an AI path that was asked for and did not run.
	// Empty when AI was not asked for, or when it ran. It is a NOTICE, never an error: the
	// surface renders it in the quiet voice, because nothing went wrong.
	AINote string `json:"ai_note,omitempty"`
	Usage  *Usage `json:"usage,omitempty"`
}

// -- the consumer interfaces: what this package needs from the rest of the app -------------------

// CatalogueQuery is one product lookup for one extracted phrase.
type CatalogueQuery struct {
	Text     string
	BranchID string
	Limit    int
}

// CatalogueHit is one ranked catalogue match, in the fields a proposal needs.
//
// This mirrors product.CounterHit's shape deliberately so the adapter is a rename rather than a
// transformation, and it carries MatchReason through untouched because a rep who cannot see WHY a
// row is in the list distrusts the list (the DIB-450 finding, which applies word for word here).
type CatalogueHit struct {
	ProductID string
	SKU       string
	Name      string
	// BaseUOM is products.base_uom, the stocking unit, always present.
	BaseUOM string
	// SellUOM is products.sold_by, the counter's declared sell unit, EMPTY for most products (30 of
	// 32 rows in the real seed leave it blank). Empty is carried as empty rather than filled in from
	// the base unit, because "the catalogue has not said" and "the catalogue said PC" are different
	// facts and only one of them justifies printing PC on a proposal.
	SellUOM     string
	MatchReason string
}

// CatalogueSearch is the READ-ONLY ranked product search, the same one the POS counter uses.
//
// REUSED, NOT REIMPLEMENTED. product.CounterSearch already encodes the relevance order a counter
// needs (SKU exact → SKU prefix → name prefix → name → description → synonym → trigram), already
// carries the synonym table that turns a customer's word into the catalogue's, and is already
// tested against the real catalogue. An extractor with its own matcher would be a second opinion
// about what "Blu 60" means, and the two would disagree in front of a customer.
type CatalogueSearch interface {
	SearchCatalogue(ctx context.Context, q CatalogueQuery) ([]CatalogueHit, error)
	// QuotableUnit asks the CATALOGUE whether this product can be quoted in this unit code.
	//
	// IT IS A QUESTION, NOT A CONVERSION. The yard keeps one row per (product, unit) in
	// product_uoms with the factor to base, and this seam asks only whether the row exists. The
	// factor is never returned and never used here: pricing owns that multiplication, and a second
	// copy of it inside a text parser would be a second answer to "how many pieces in 480 square
	// feet" (units.go header).
	//
	// An error is NOT a refusal. A seam that cannot answer leaves the unit for the rep to confirm
	// rather than silently accepting or silently dropping it, see resolveUnit.
	QuotableUnit(ctx context.Context, productID, uomCode string) (bool, error)
}

// Phrase is one candidate line: the words, plus whatever quantity and unit were read out of them.
//
// It is the ONLY thing an AI provider is allowed to produce (see AIExtractor). There is no product
// field on it, by construction, invariant 2 in the package header is enforced by the absence of a
// place to put a product, not by a check that could be forgotten.
type Phrase struct {
	// Text is the phrase as the rep wrote it, unedited. It becomes Proposal.SourceText.
	Text string `json:"text"`
	// Qty is the quantity as a decimal string, or empty when none was found. A STRING because the
	// quote line writer takes a decimal string and because "0" and "not stated" must not collapse.
	Qty string `json:"qty"`
	// Unit is the raw unit word the text used ("sq ft", "tonnes", "bags"), unnormalised. The unit
	// vocabulary belongs to this package (units.go), so a provider naming a unit this yard does
	// not sell in cannot invent one, the word is mapped or it is reported.
	Unit string `json:"unit"`
}

// Segmentation is what an AI-assisted pass returns.
type Segmentation struct {
	Phrases   []Phrase
	Provider  string
	Model     string
	TokensIn  int
	TokensOut int
}

// AIExtractor is the OPTIONAL AI segmentation seam.
//
// WHAT IT MAY DO: split a paragraph into phrases and read quantities. That is all, and the return
// type is why, a Segmentation cannot carry a product, a price, a SKU or a line.
//
// WHAT IT MUST DO, and what the adapter that satisfies it is tested for: resolve the caller's
// budget, refuse over it with assist.ErrOverBudget BEFORE calling any provider, and append
// exactly one assist ledger row per exchange that actually happened. Those rails are the assist
// package's own (assist.BudgetFor / assist.OverBudget / LedgerStore.Append) and this seam exists
// so that they are reached through one door, internal/assist/adapters/quoteextract.go, rather
// than reimplemented here.
//
// A nil AIExtractor is the SHIPPED DEFAULT and is not a degraded state.
type AIExtractor interface {
	SegmentText(ctx context.Context, c assist.Caller, text string) (Segmentation, error)
}

// ScopeLine is one quote line, in the two facts a scope rule needs: which line it is (so a
// finding can point at it) and what the product is FOR.
type ScopeLine struct {
	LineNo int    `json:"line_no"`
	Name   string `json:"name"`
	// Application is products.application, 'Paver', 'Jointing', 'Base', 'Wall' … Free text owned
	// by the catalogue, compared case-insensitively here (scope.go normaliseApp).
	Application string `json:"application"`
}

// QuoteLineReader is the read behind scope-gap detection: this quote's lines, with each line's
// catalogue application, scoped to a caller who may see the quote.
//
// found=false means "no such quote, or not yours", deliberately one answer, because the quote
// module answers a cross-branch probe with a flat refusal and this seam must not be the place
// that turns that into an existence oracle.
type QuoteLineReader interface {
	QuoteScopeLines(ctx context.Context, quoteID string) (lines []ScopeLine, found bool, err error)
}

// RuleStore reads and writes quote_scope_rules (migration 0159).
//
// Rules returns EVERY rule, enabled and disabled. That is not laziness about filtering: the admin
// listing needs the disabled ones, and Evaluate is the single place `enabled` is honoured. A
// store that pre-filtered would make `enabled` a rule implemented twice, and the second copy is
// the one that gets forgotten.
type RuleStore interface {
	Rules(ctx context.Context) ([]ScopeRule, error)
	SetRuleEnabled(ctx context.Context, code string, enabled bool, actorID string) (ScopeRule, error)
}
