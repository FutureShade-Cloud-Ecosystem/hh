package quoteassist

// ai_degrade_test.go, D-075's "AI optional" half, from this side of the seam.
//
// The rails themselves (budget resolved, spend checked, ledger appended) are tested where they live:
// internal/assist/adapters/quoteassist_rails_test.go. What is tested HERE is the promise this package
// makes to a rep: THERE IS NO STATE OF THIS FEATURE IN WHICH A MISSING, UNFUNDED OR BROKEN PROVIDER
// PRODUCES AN ERROR. The worst it produces is the answer the deterministic engine would have given
// anyway, plus one plain sentence.
//
// Four ways the AI path can fail and four notices, none of them an error:
//
//	not asked for      → no note, no call, deterministic source
//	not wired          → AINoteUnavailable
//	over budget        → AINoteOverBudget           (the assist rails' own refusal)
//	provider broke     → AINoteFailed

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

// TestExtract_AIIsOffByDefault, the zero value of the request is the deterministic engine, and the
// seam is not touched. A rep who has not asked for the AI path must not spend a token on it.
func TestExtract_AIIsOffByDefault(t *testing.T) {
	ai := &fakeAI{seg: Segmentation{Phrases: []Phrase{{Text: "nonsense the model made up"}}}}
	svc := NewService(seededCatalogue(), nil, nil, nil).WithAI(ai)

	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if ai.call != 0 {
		t.Fatalf("the AI seam was called %d times on a request that did not ask for it", ai.call)
	}
	if got.Source != SourceDeterministic {
		t.Errorf("source = %q, want %q", got.Source, SourceDeterministic)
	}
	if got.AINote != "" {
		t.Errorf("ai_note = %q, nothing to explain when AI was never asked for", got.AINote)
	}
	if got.Usage != nil {
		t.Errorf("usage = %+v on a path that spent nothing", got.Usage)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("the deterministic path produced %d proposals", len(got.Proposals))
	}
}

// TestExtract_AIAskedForButUnwiredDegradesQuietly, the shipped default is no provider, and asking
// for one must still answer the question.
func TestExtract_AIAskedForButUnwiredDegradesQuietly(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil) // no WithAI
	if svc.AIWired() {
		t.Fatal("AIWired() is true with no seam attached")
	}
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60", UseAI: true})
	if err != nil {
		t.Fatalf("Extract: %v, an unwired provider is not an error", err)
	}
	if got.AINote != AINoteUnavailable {
		t.Errorf("ai_note = %q, want AINoteUnavailable", got.AINote)
	}
	if got.Source != SourceDeterministic {
		t.Errorf("source = %q, want the deterministic path", got.Source)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, the answer must still arrive", len(got.Proposals))
	}
}

// TestExtract_OverBudgetDegradesToTheDeterministicPath is the budget rail, seen from the surface.
//
// assist.ErrOverBudget comes back from the rails, and the rep gets their proposals with one sentence
// that names what happened and what still works. Never a 4xx, never a red box: nothing is broken, a
// person reached a number their administrator chose.
func TestExtract_OverBudgetDegradesToTheDeterministicPath(t *testing.T) {
	ai := &fakeAI{err: assist.ErrOverBudget}
	svc := NewService(seededCatalogue(), nil, nil, nil).WithAI(ai)

	got, err := svc.Extract(context.Background(), staffCaller(),
		ExtractRequest{Text: "480 sq ft Blu 60\n6 bags polymeric sand", UseAI: true})
	if err != nil {
		t.Fatalf("Extract: %v, over budget is a NOTICE, not an error", err)
	}
	if got.AINote != AINoteOverBudget {
		t.Errorf("ai_note = %q, want AINoteOverBudget", got.AINote)
	}
	if got.Source != SourceDeterministic {
		t.Errorf("source = %q, want the deterministic path", got.Source)
	}
	if len(got.Proposals) != 2 {
		t.Fatalf("proposals = %d, want 2, the deterministic engine costs nothing and must still run", len(got.Proposals))
	}
	if got.Usage != nil {
		t.Errorf("usage = %+v, nothing was sent and nothing was spent", got.Usage)
	}
}

// TestExtract_ProviderFailureDegradesAndSaysSoCalmly.
func TestExtract_ProviderFailureDegradesAndSaysSoCalmly(t *testing.T) {
	ai := &fakeAI{err: errors.New("upstream 503")}
	svc := NewService(seededCatalogue(), nil, nil, nil).WithAI(ai)

	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60", UseAI: true})
	if err != nil {
		t.Fatalf("Extract: %v, a broken provider must not break the feature", err)
	}
	if got.AINote != AINoteFailed {
		t.Errorf("ai_note = %q, want AINoteFailed", got.AINote)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d", len(got.Proposals))
	}
}

// TestExtract_ProviderAnsweringNothingUsableDegrades, a provider that returns an empty segmentation
// has not failed and has not helped. The deterministic phrases are already in hand.
func TestExtract_ProviderAnsweringNothingUsableDegrades(t *testing.T) {
	ai := &fakeAI{seg: Segmentation{Provider: "test", Model: "t"}} // no phrases
	svc := NewService(seededCatalogue(), nil, nil, nil).WithAI(ai)

	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60", UseAI: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got.AINote != AINoteFailed {
		t.Errorf("ai_note = %q, want AINoteFailed", got.AINote)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, the deterministic phrases must be used", len(got.Proposals))
	}
}

// TestExtract_AISuccessReportsTheProvidersOwnNumbers, usage is what the provider said, carried
// through untouched. Never estimated here: a budget enforced against a guess cannot be explained to
// the person it refuses (assist/provider.go).
func TestExtract_AISuccessReportsTheProvidersOwnNumbers(t *testing.T) {
	ai := &fakeAI{seg: Segmentation{
		Provider: "anthropic", Model: "claude-x",
		TokensIn: 412, TokensOut: 96,
		Phrases: []Phrase{{Text: "480 sq ft of Techo-Bloc Blu 60", Qty: "480", Unit: "sq ft"}},
	}}
	svc := NewService(seededCatalogue(), nil, nil, nil).WithAI(ai)
	if !svc.AIWired() {
		t.Fatal("AIWired() is false with a seam attached")
	}

	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "anything", UseAI: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got.Source != SourceAIAssisted {
		t.Errorf("source = %q, want %q", got.Source, SourceAIAssisted)
	}
	if got.AINote != "" {
		t.Errorf("ai_note = %q, nothing to explain when the AI path ran", got.AINote)
	}
	if got.Usage == nil {
		t.Fatal("usage is nil on a path that spent tokens")
	}
	if got.Usage.TokensIn != 412 || got.Usage.TokensOut != 96 {
		t.Errorf("usage = %+v, want the provider's own 412/96", *got.Usage)
	}
	if got.Usage.Provider != "anthropic" || got.Usage.Model != "claude-x" {
		t.Errorf("usage names %q/%q, the ledger records what it ACTUALLY was, not what was configured",
			got.Usage.Provider, got.Usage.Model)
	}
}

// TestExtract_WithAINilLeavesTheDefaultAlone, a nil argument must not store a nil interface that
// later looks wired.
func TestExtract_WithAINilLeavesTheDefaultAlone(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil).WithAI(nil)
	if svc.AIWired() {
		t.Fatal("WithAI(nil) attached something")
	}
}

// TestExtract_RefusedCallerIsStillAnAnswer, the rails refuse a role-less caller (assist.ErrRefused).
// The deterministic engine has no notion of authority beyond the route guard, so the rep still gets
// their proposals; only the AI half is refused, and it is refused as a notice.
func TestExtract_RefusedCallerIsStillAnAnswer(t *testing.T) {
	ai := &fakeAI{err: assist.ErrRefused}
	svc := NewService(seededCatalogue(), nil, nil, nil).WithAI(ai)
	got, err := svc.Extract(context.Background(), assist.Caller{UserID: "u"}, ExtractRequest{Text: "480 sq ft Blu 60", UseAI: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got.AINote != AINoteFailed {
		t.Errorf("ai_note = %q", got.AINote)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d", len(got.Proposals))
	}
}
