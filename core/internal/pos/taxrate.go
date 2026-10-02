package pos

// taxrate.go, the counter-readable tax rate (the row 661 read): the rate the
// ring binds, read through the ring's own assessor, and the yard's configured
// rounding rule beside it. The till hides pre-ring tax and total; the rate
// and the round rule are the two facts the counter may see, staff gated (a
// receipt shows the rate, it is not a secret).

import (
	"context"
)

// taxRateJurisdiction is the jurisdiction word the read answers: the
// standard-rated Ontario facts the binding engine serves.
const taxRateJurisdiction = "ON"

// roundModeInvoiceTotal is the round rule an unwired service answers: the
// flat engine's actual behavior. The engine rounds half-up once at the
// invoice total and nothing branches on the stored rule, so the word says
// what is configured, and the default says what the engine does.
const roundModeInvoiceTotal = "INVOICE_TOTAL"

// TaxRuleSource is the optional seam behind the read's round_mode word: the
// yard's configured rounding rule (tax_config.round_mode), wired in cmd/core
// over tax.Service.GetConfig(ctx, ""). Unwired, the service answers
// INVOICE_TOTAL. It lives in its own file beside the service surface, never
// in the frozen sale_hooks.go.
type TaxRuleSource interface {
	RoundMode(ctx context.Context) (string, error)
}

// TaxRate is the counter-readable answer: the rate the ring binds and the
// configured rounding rule. It is the contract's PosTaxRate shape.
type TaxRate struct {
	Jurisdiction string `json:"jurisdiction"`
	ReasonCode   string `json:"reason_code"`
	RateBps      int    `json:"rate_bps"`
	RoundMode    string `json:"round_mode"`
}

// WithTaxRuleSource injects the round-rule read (nil default: INVOICE_TOTAL).
func (s *Service) WithTaxRuleSource(r TaxRuleSource) *Service { s.taxRules = r; return s }

// taxRuleSource returns the injected rule source, defaulting to the constant
// the flat engine's behavior names.
func (s *Service) taxRuleSource() TaxRuleSource {
	if s.taxRules != nil {
		return s.taxRules
	}
	return fixedRoundMode(roundModeInvoiceTotal)
}

// fixedRoundMode answers one constant word, the unwired default.
type fixedRoundMode string

func (f fixedRoundMode) RoundMode(context.Context) (string, error) { return string(f), nil }

// TaxRate answers the counter's tax-rate read. The RATE is the ring's rate
// BY CONSTRUCTION at the moment of the read: the method assesses a
// zero-cent standard sale through the same TaxAssessor seam the Sale
// assesses with, so the answer is the rate the ring binds in every
// composition (the flat default answers 1300, the wired engine answers the
// effective-dated standard row). Through the wired seam that is the rate
// every ticket the counter can ring today binds, a relief ticket too: the
// relief reasons are evidence-gated, the seam carries no evidence
// (tops_adapters.go pins EvidenceAttached false), so the relief reason
// rides the ticket while the standard rate binds. MANUAL_TAX_REVIEW is the
// one exception shape, an ungated row of its own (equal to the standard
// today, separately editable). A rate row taking effect between the read
// and the ring moves the ring's rate. The ROUND RULE comes from the
// TaxRuleSource seam (INVOICE_TOTAL unwired): today the header tax rounds
// half-up once at the invoice total and nothing branches on the stored
// rule, so the word says what is configured, not what the ring does yet.
func (s *Service) TaxRate(ctx context.Context) (TaxRate, error) {
	quote, err := s.taxAssessor().AssessSale(ctx, TaxQuoteInput{SubtotalCents: 0, ReasonCode: ""})
	if err != nil {
		return TaxRate{}, err
	}
	mode, err := s.taxRuleSource().RoundMode(ctx)
	if err != nil {
		return TaxRate{}, err
	}
	return TaxRate{
		Jurisdiction: taxRateJurisdiction,
		ReasonCode:   TaxStdON13,
		RateBps:      quote.RateBps,
		RoundMode:    mode,
	}, nil
}
