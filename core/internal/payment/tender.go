package payment

// tender.go, DIB-102: the surcharge flip and EFT as a tender method.
//
// THIS IS A FLIP, NOT A BUILD, and knowing that is what keeps it small. Everything needed already
// shipped RESERVED-OFF in 0027: payment_config.surcharge_enabled (FALSE), payment_config.mdr_bps,
// payments.surcharge_cents (0), payments.card_type, and a payments.method CHECK that has admitted
// 'EFT' since 0010. OntarioSurcharge() has computed the right number since W4. What was missing is
// the thing that ASKS: no production code path ever called the calculator, and nothing decided
// whether EFT should appear in the ring.
//
// QuoteTender below is that seam. The POS calls it before ringing a tender and gets back the base,
// the surcharge, the rate actually applied and a customer-facing label. One call site, so a surcharge
// can never be shown on a screen and then not charged (or the reverse).
//
// -- THE LEGAL FLOOR IS IN CODE, NOT IN CONFIG ----------------------------------------------------
// Ontario permits a credit surcharge capped at 2.4% and PROHIBITS surcharging debit and prepaid. That
// is enforced in OntarioSurcharge(), CREDIT-only, clamped to min(mdr_bps, 240), and nothing here
// can loosen it. payment_config.surcharge_card_types can only NARROW the set ('CREDIT' → 'NONE'), so
// a dealer can decline to surcharge, and no configuration value can make us surcharge a debit card.
// Putting the legal boundary in a config column would make a typo a regulatory problem.
//
// -- THE RATE IS STAMPED, NOT RECOMPUTED ----------------------------------------------------------
// The result carries SurchargeBps, the rate ACTUALLY applied, so the caller can persist it on the
// payment (payments.surcharge_bps, 0106). A receipt dispute six months later is then answered from
// the row rather than recomputed against a config that has changed since. That is the same reasoning
// payment_nsf uses to snapshot the fee at bounce time.

import (
	"context"
	"errors"
	"fmt"
)

// Tender method values (payments.method CHECK, 0010).
const (
	TenderCash   = "CASH"
	TenderCard   = "CARD"
	TenderCheque = "CHEQUE"
	TenderEFT    = "EFT"
	TenderOther  = "OTHER"
)

// Card type values (payments.card_type CHECK, 0027).
const (
	CardCredit  = "CREDIT"
	CardDebit   = "DEBIT"
	CardPrepaid = "PREPAID"
	CardUnknown = "UNKNOWN"
)

// TenderQuote asks "what does this tender cost?".
type TenderQuote struct {
	Method      string // CASH|CARD|CHEQUE|EFT|OTHER
	CardType    string // CREDIT|DEBIT|PREPAID|UNKNOWN, ignored unless Method is CARD
	AmountCents int64
}

// TenderQuoteResult is the answer the POS renders and then persists.
type TenderQuoteResult struct {
	Method         string `json:"method"`
	CardType       string `json:"card_type,omitempty"`
	BaseCents      int64  `json:"base_cents"`
	SurchargeCents int64  `json:"surcharge_cents"`
	SurchargeBps   int64  `json:"surcharge_bps"`
	TotalCents     int64  `json:"total_cents"`
	// Label is the customer-facing line, empty when there is no surcharge. It names the RATE, because
	// "Credit card surcharge" alone invites the question this line exists to pre-empt.
	Label string `json:"surcharge_label,omitempty"`
}

// TenderOption is one tender the counter may offer.
type TenderOption struct {
	Method    string `json:"method"`
	Label     string `json:"label"`
	Surcharge bool   `json:"surcharge_applies"`
}

var errBadTender = errors.New("payment: a tender needs a method and a positive amount")

// QuoteTender computes the surcharge for one tender against the ACTIVE payment_config.
//
// A non-CARD tender always quotes at par. That is not a simplification, surcharging cash or a cheque
// has no basis at all, and EFT is deliberately excluded because its cost to the dealer is a flat bank
// fee rather than a merchant discount rate, so the mdr_bps calculation would be answering a question
// nobody asked.
func (s *Service) QuoteTender(ctx context.Context, in TenderQuote) (TenderQuoteResult, error) {
	if in.Method == "" || in.AmountCents <= 0 {
		return TenderQuoteResult{}, errBadTender
	}
	cfg, _, err := s.cfg.activeConfig(ctx)
	if err != nil {
		return TenderQuoteResult{}, err
	}
	if in.Method == TenderEFT && !cfg.EFTEnabled {
		return TenderQuoteResult{}, fmt.Errorf("payment: EFT is not enabled for this dealer (payment_config.eft_enabled)")
	}

	res := TenderQuoteResult{
		Method: in.Method, CardType: in.CardType,
		BaseCents: in.AmountCents, TotalCents: in.AmountCents,
	}
	if in.Method != TenderCard {
		return res, nil
	}

	// The per-dealer narrowing. 'NONE' means this dealer surcharges nothing even while the org flag
	// is on; it can never mean "surcharge more".
	enabled := cfg.Surcharge && cfg.SurchargeCardTypes != "NONE"
	res.SurchargeCents = OntarioSurcharge(in.AmountCents, in.CardType, cfg.MDRBps, enabled)
	if res.SurchargeCents > 0 {
		res.SurchargeBps = effectiveSurchargeBps(cfg.MDRBps)
		res.TotalCents = in.AmountCents + res.SurchargeCents
		res.Label = fmt.Sprintf("Credit card surcharge (%s%%)", bpsPercent(res.SurchargeBps))
	}
	return res, nil
}

// TenderMethods lists the tenders this dealer may offer, EFT appearing only when enabled.
//
// EFT ships OFF (0106 default FALSE). A tender nobody at the counter has been trained on should not
// appear in the ring the day a migration merges, the first person to press it would be finding out
// what it does in front of a customer.
func (s *Service) TenderMethods(ctx context.Context) ([]TenderOption, error) {
	cfg, _, err := s.cfg.activeConfig(ctx)
	if err != nil {
		return nil, err
	}
	surchargeOn := cfg.Surcharge && cfg.SurchargeCardTypes != "NONE"
	out := []TenderOption{
		{Method: TenderCash, Label: "Cash"},
		{Method: TenderCard, Label: "Card", Surcharge: surchargeOn},
		{Method: TenderCheque, Label: "Cheque"},
	}
	if cfg.EFTEnabled {
		out = append(out, TenderOption{Method: TenderEFT, Label: "EFT / bank transfer"})
	}
	out = append(out, TenderOption{Method: TenderOther, Label: "Other"})
	return out, nil
}

// effectiveSurchargeBps is the rate actually applied: the dealer's MDR, clamped to the Ontario cap.
// It mirrors OntarioSurcharge's clamp exactly; the two are kept adjacent so they cannot drift.
func effectiveSurchargeBps(mdrBps int64) int64 {
	if mdrBps > ontarioSurchargeCapBps {
		return ontarioSurchargeCapBps
	}
	if mdrBps < 0 {
		return 0
	}
	return mdrBps
}

// bpsPercent renders basis points as a percentage for the customer-facing label (240 → "2.4").
func bpsPercent(bps int64) string {
	whole, frac := bps/100, bps%100
	if frac == 0 {
		return fmt.Sprintf("%d", whole)
	}
	if frac%10 == 0 {
		return fmt.Sprintf("%d.%d", whole, frac/10)
	}
	return fmt.Sprintf("%d.%02d", whole, frac)
}
