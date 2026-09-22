package payment

// ontarioSurchargeCapBps is the Ontario credit-surcharge cap (2.4%, since 2022-10-06).
const ontarioSurchargeCapBps int64 = 240

// OntarioSurcharge returns the surcharge in cents. ENABLED comes from payment_config.surcharge_enabled
// (default FALSE → always 0 at launch, D-RUNPAY-2). Surcharge is CREDIT-ONLY and capped at min(2.4%, MDR);
// debit/prepaid are NEVER surcharged (prohibited in Ontario). Pure function, the W16 flip is
// surcharge_enabled=true + the POS surfacing the line, not new logic.
func OntarioSurcharge(amountCents int64, cardType string, mdrBps int64, enabled bool) int64 {
	if !enabled || cardType != "CREDIT" || amountCents <= 0 {
		return 0
	}
	bps := mdrBps
	if bps > ontarioSurchargeCapBps {
		bps = ontarioSurchargeCapBps // clamp to the legal cap
	}
	if bps < 0 {
		bps = 0
	}
	return (amountCents * bps) / 10000
}
