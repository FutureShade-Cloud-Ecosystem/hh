package pos

// taxrate_test.go, the counter tax-rate read's unit legs (no database): the
// default wiring answers the flat 1300 and INVOICE_TOTAL, an assessor
// answering another rate flows through, and a wired rule source's word flows
// through. The first leg pins the default half of the by-construction
// equality: an unwired service assesses through FlatHSTAssessor and answers
// the flat engine's own behavior, never a hard-coded number of its own.

import (
	"context"
	"testing"
)

// fixedBpsAssessor answers one rate whatever it is asked, the fake the
// flows-through leg drives: a rate no composition answers by default, so a
// method that returns the flat constant instead of the assessment fails it.
type fixedBpsAssessor struct{ bps int }

func (f fixedBpsAssessor) AssessSale(_ context.Context, _ TaxQuoteInput) (QuoteResult, error) {
	return QuoteResult{RateBps: f.bps}, nil
}

// fakeRoundMode answers one round mode word, the fake the rule-source leg
// wires: a word the default never answers, so a method that returns the
// default constant instead of the seam fails it.
type fakeRoundMode struct{ mode string }

func (f fakeRoundMode) RoundMode(context.Context) (string, error) { return f.mode, nil }

func TestTaxRate_DefaultWiringAnswers1300AndInvoiceTotal(t *testing.T) {
	h := newHarness()
	out, err := h.svc.TaxRate(context.Background())
	if err != nil {
		t.Fatalf("the default wiring answers: %v", err)
	}
	if out.RateBps != 1300 {
		t.Fatalf("the flat default answers 1300: got %d", out.RateBps)
	}
	if out.RoundMode != "INVOICE_TOTAL" {
		t.Fatalf("an unwired rule source answers INVOICE_TOTAL: got %q", out.RoundMode)
	}
	if out.Jurisdiction != "ON" || out.ReasonCode != "STD_ON_HST_13" {
		t.Fatalf("the standard ON facts ride the answer: %+v", out)
	}
}

// The rate flows from the assessor: the read assesses through the seam, so a
// wired assessor answering 800 moves the answer. Swapping the method's
// assessment for the flat constant fails this leg.
func TestTaxRate_AssessedRateFlowsThrough(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithTaxAssessor(fixedBpsAssessor{bps: 800})
	out, err := h.svc.TaxRate(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.RateBps != 800 {
		t.Fatalf("the wired assessor's rate must flow through: got %d, want 800", out.RateBps)
	}
}

// The round mode flows from the seam: a wired source answering PER_LINE moves
// the word. Returning the default constant in the method fails this leg.
func TestTaxRate_RuleSourceFlowsThrough(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithTaxRuleSource(fakeRoundMode{mode: "PER_LINE"})
	out, err := h.svc.TaxRate(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.RoundMode != "PER_LINE" {
		t.Fatalf("the wired rule source's word must flow through: got %q, want PER_LINE", out.RoundMode)
	}
}
