package quoteassist

// service.go, the two surfaces, and the degradation rules.
//
// Extract() IS DIB-66 IN SEVEN LINES OF CONTROL FLOW, and every one of them is a decision worth
// reading:
//
//	1. is the catalogue reachable            → 503 with the seam named, never a 500
//	2. empty text                            → an empty answer, NOT an error
//	3. segment deterministically              → the default, and the only path that always exists
//	4. if AI was ASKED FOR and is wired        → try it, through the assist rails
//	5. if the rails refuse or the provider     → keep the deterministic phrases and SAY WHY
//	   fails (over budget / unwired / error)
//	6. resolve every phrase against the        → the product is always the catalogue's answer
//	   catalogue
//	7. return proposals + unmatched            → nothing is written, nothing is dropped
//
// Step 5 is the whole D-075 ruling in one branch: there is no state of this feature in which a
// missing, unfunded or broken provider produces an error. The worst it produces is the answer the
// deterministic engine would have given anyway, plus one plain sentence.
//
// ScopeGaps() IS DIB-67, and it has no AI branch to degrade because it has no AI.

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

// Service is the quote-assist read surface. Every dependency is an interface and every one of them
// may be nil: a partially wired deployment answers the parts it can and names the parts it cannot,
// which is the normal state during a rollout (assist.Deps' rule, same reasoning).
type Service struct {
	catalogue CatalogueSearch
	lines     QuoteLineReader
	rules     RuleStore
	ai        AIExtractor
	log       *slog.Logger
}

// NewService builds the service over the deterministic seams. The AI seam is added separately by
// WithAI so that the shipped default, no AI, is the value you get from the constructor, and
// switching it on is a visible line in the composition root rather than a parameter someone passes
// nil to.
func NewService(catalogue CatalogueSearch, lines QuoteLineReader, rules RuleStore, log *slog.Logger) *Service {
	return &Service{catalogue: catalogue, lines: lines, rules: rules, log: log}
}

// WithAI attaches the optional AI segmentation seam. A nil argument leaves the service in its
// default, AI-free state rather than storing a nil interface that later looks wired.
func (s *Service) WithAI(ai AIExtractor) *Service {
	if ai == nil {
		return s
	}
	s.ai = ai
	return s
}

// AIWired reports whether the AI seam is attached, for a surface that wants to offer the toggle only
// where it would do something. Offering a switch that cannot work is worse than not offering it.
func (s *Service) AIWired() bool { return s.ai != nil }

// The sentences a degraded AI path produces. Constants so the test, the frontend fixture and the
// walkthrough screenshot are provably the same words, and so that none of them can be mistaken for
// an error message, because none of them describes a failure.
const (
	// AINoteOverBudget: the caller has spent their monthly allowance. The assist package's own
	// ruling, restated for this surface: nothing is broken, a person reached a number their
	// administrator chose, and the thing that still works is named.
	AINoteOverBudget = "This month's AI allowance is used up, so I read your text with the plain parser instead, " +
		"it needs no AI and the results below are complete."

	// AINoteUnavailable: no provider is configured in this deployment.
	AINoteUnavailable = "AI-assisted reading isn't switched on here, so I used the plain parser. " +
		"Everything below came from your text and the catalogue."

	// AINoteFailed: a provider was configured and did not answer. Still not an error to the user -
	// the answer they wanted is on the screen.
	AINoteFailed = "The AI reader didn't answer just now, so I used the plain parser. " +
		"Everything below came from your text and the catalogue."

	// NoteTruncated: the paste was longer than one review list.
	NoteTruncated = "That's a long list, I read the first 30 items. Paste the rest after you've confirmed these."
)

// Extract reads a job description into proposed quote lines.
//
// IT WRITES NOTHING. There is no store on this service that could, no quote import in this package,
// and no branch below that adds a line. The rep confirms rows one at a time through the shipped
// POST /api/v1/quotes/{id}/lines path, which prices, holds stock and writes the audit row exactly as
// a hand-typed line does.
func (s *Service) Extract(ctx context.Context, c assist.Caller, req ExtractRequest) (Extraction, error) {
	if s.catalogue == nil {
		return Extraction{}, ErrCatalogueUnwired
	}
	text := strings.TrimSpace(req.Text)
	// EMPTY IS AN ANSWER. A rep who clicks Read before typing gets an empty list and no red box -
	// the DIB-443 rule, one surface further on.
	if text == "" {
		return Extraction{Proposals: []Proposal{}, Unmatched: []Unmatched{}, Source: SourceDeterministic}, nil
	}

	phrases := Segment(text)
	source := SourceDeterministic
	note := ""
	var usage *Usage

	if req.UseAI {
		seg, err := s.assistedSegment(ctx, c, text)
		switch {
		case err == nil && len(seg.Phrases) > 0:
			phrases = seg.Phrases
			source = SourceAIAssisted
			usage = &Usage{Provider: seg.Provider, Model: seg.Model, TokensIn: seg.TokensIn, TokensOut: seg.TokensOut}
		case err == nil:
			// The provider answered with nothing usable. That is not a failure to report as one:
			// the deterministic phrases are already in hand and they are what the rep gets.
			note = AINoteFailed
		default:
			note = aiNoteFor(err)
			if s.log != nil && !errors.Is(err, assist.ErrOverBudget) && !errors.Is(err, ErrAIUnavailable) {
				// Over-budget and unwired are ordinary states and are not logged as problems. A
				// provider that broke IS one, and it is logged loudly here even though the user is
				// told something calm, the operator needs the signal the rep does not.
				s.log.Warn("quoteassist: AI-assisted extraction failed, answered from the deterministic parser", "err", err)
			}
		}
	}

	proposals, unmatched, err := resolveAll(ctx, s.catalogue, phrases, req.BranchID)
	if err != nil {
		return Extraction{}, err
	}
	if Truncated(text) {
		unmatched = append(unmatched, Unmatched{SourceText: NoteTruncated, Reason: "not read, over the 30-item limit for one paste"})
	}
	return Extraction{
		Proposals: proposals,
		Unmatched: unmatched,
		Source:    source,
		AINote:    note,
		Usage:     usage,
	}, nil
}

// assistedSegment is the ONE call site of the AI seam in this package.
//
// One, so that "every AI call goes through the assist rails" is checkable by grep rather than by
// review: the seam is called here and nowhere else, and the thing on the other side of it is
// internal/assist/adapters/quoteextract.go, which resolves the budget, refuses over it before any
// provider is reached, and appends the ledger row.
func (s *Service) assistedSegment(ctx context.Context, c assist.Caller, text string) (Segmentation, error) {
	if s.ai == nil {
		return Segmentation{}, ErrAIUnavailable
	}
	return s.ai.SegmentText(ctx, c, text)
}

// aiNoteFor maps a rail refusal onto the sentence a rep reads. Every branch returns a NOTICE; there
// is no error path out of here, by construction.
func aiNoteFor(err error) string {
	switch {
	case errors.Is(err, assist.ErrOverBudget):
		return AINoteOverBudget
	case errors.Is(err, ErrAIUnavailable), errors.Is(err, assist.ErrDisabled):
		return AINoteUnavailable
	}
	return AINoteFailed
}

// ScopeGaps evaluates one quote against the configured rules.
//
// found=false means the quote does not exist or is not this caller's to see, one answer, because
// the quote module answers a cross-branch probe with a flat refusal and this read must not become
// the oracle that tells the two apart.
func (s *Service) ScopeGaps(ctx context.Context, quoteID string) (ScopeGaps, bool, error) {
	if s.lines == nil {
		return ScopeGaps{}, false, ErrQuoteLinesUnwired
	}
	if s.rules == nil {
		return ScopeGaps{}, false, ErrRulesUnwired
	}
	lines, found, err := s.lines.QuoteScopeLines(ctx, quoteID)
	if err != nil {
		return ScopeGaps{}, false, err
	}
	if !found {
		return ScopeGaps{}, false, nil
	}
	rules, err := s.rules.Rules(ctx)
	if err != nil {
		return ScopeGaps{}, false, err
	}
	return ScopeGaps{
		QuoteID:        quoteID,
		Findings:       Evaluate(rules, lines),
		LinesChecked:   len(lines),
		RulesEvaluated: CountEnabled(rules),
	}, true, nil
}

// Rules is the admin listing: every rule, enabled and disabled, in presentation order.
func (s *Service) Rules(ctx context.Context) ([]ScopeRule, error) {
	if s.rules == nil {
		return nil, ErrRulesUnwired
	}
	rules, err := s.rules.Rules(ctx)
	if err != nil {
		return nil, err
	}
	SortRules(rules)
	if rules == nil {
		// Always an array, never null: `{"rules":null}` and `{"rules":[]}` are different claims to
		// a client that does not write `?? []`, and the difference renders as a blank panel with no
		// explanation.
		rules = []ScopeRule{}
	}
	return rules, nil
}

// SetRuleEnabled is the admin's switch. The ONLY mutation this package has, and it changes a rule's
// visibility, never a quote, never a line, never money.
func (s *Service) SetRuleEnabled(ctx context.Context, code string, enabled bool, actorID string) (ScopeRule, error) {
	if s.rules == nil {
		return ScopeRule{}, ErrRulesUnwired
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return ScopeRule{}, ErrRuleNotFound
	}
	return s.rules.SetRuleEnabled(ctx, code, enabled, actorID)
}
