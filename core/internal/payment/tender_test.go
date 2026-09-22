package payment

// tender_test.go, DIB-102. The flip is a money rule, so the tests are written as the questions a
// counter person or an auditor would actually ask.

import (
	"context"
	"strings"
	"testing"
)

// fakeCfg is a config store returning a fixed active row (no database, no vault).
type fakeCfg struct {
	cfg activeCfg
	err error
}

func (f fakeCfg) activeConfig(context.Context) (activeCfg, map[string]string, error) {
	return f.cfg, map[string]string{"api_key": "test-only"}, f.err
}

func quoteSvc(c activeCfg) *Service { return &Service{cfg: fakeCfg{cfg: c}, proc: RunPaymentsStub{}} }

func TestQuoteTender_SurchargeFlipAndItsLimits(t *testing.T) {
	ctx := context.Background()
	// A 2.9% MDR, above the Ontario cap, so the clamp is exercised on every enabled case.
	on := activeCfg{MDRBps: 290, Surcharge: true, SurchargeCardTypes: "CREDIT"}

	t.Run("flag off charges nothing extra", func(t *testing.T) {
		off := activeCfg{MDRBps: 290, Surcharge: false, SurchargeCardTypes: "CREDIT"}
		got, err := quoteSvc(off).QuoteTender(ctx, TenderQuote{Method: TenderCard, CardType: CardCredit, AmountCents: 100000})
		if err != nil {
			t.Fatalf("quote: %v", err)
		}
		if got.SurchargeCents != 0 || got.TotalCents != 100000 || got.Label != "" {
			t.Fatalf("flag-off quote = %+v, want no surcharge at all", got)
		}
	})

	t.Run("credit is surcharged at the CAPPED rate", func(t *testing.T) {
		got, err := quoteSvc(on).QuoteTender(ctx, TenderQuote{Method: TenderCard, CardType: CardCredit, AmountCents: 100000})
		if err != nil {
			t.Fatalf("quote: %v", err)
		}
		// 2.4% of $1000 = $24.00, NOT 2.9% ($29.00). The cap is the law, not a preference.
		if got.SurchargeCents != 2400 {
			t.Fatalf("surcharge = %d, want 2400 (2.4%% cap, not the dealer's 2.9%% MDR)", got.SurchargeCents)
		}
		if got.SurchargeBps != 240 {
			t.Fatalf("stamped rate = %d bps, want the capped 240, the receipt must record what was actually applied", got.SurchargeBps)
		}
		if got.TotalCents != 102400 {
			t.Fatalf("total = %d, want 102400", got.TotalCents)
		}
		if !strings.Contains(got.Label, "2.4%") {
			t.Fatalf("label = %q, must name the rate, 'Credit card surcharge' alone invites the question "+
				"this line exists to pre-empt", got.Label)
		}
	})

	t.Run("a sub-cap MDR is applied as-is", func(t *testing.T) {
		low := activeCfg{MDRBps: 150, Surcharge: true, SurchargeCardTypes: "CREDIT"}
		got, _ := quoteSvc(low).QuoteTender(ctx, TenderQuote{Method: TenderCard, CardType: CardCredit, AmountCents: 100000})
		if got.SurchargeCents != 1500 || got.SurchargeBps != 150 {
			t.Fatalf("sub-cap quote = %d cents / %d bps, want 1500/150", got.SurchargeCents, got.SurchargeBps)
		}
		if !strings.Contains(got.Label, "1.5%") {
			t.Fatalf("label = %q, want it to name 1.5%%", got.Label)
		}
	})

	// The prohibitions. These are the assertions that make this a compliance test rather than an
	// arithmetic one, and no configuration value may be able to flip them.
	for _, ct := range []string{CardDebit, CardPrepaid, CardUnknown} {
		t.Run("no surcharge on "+ct, func(t *testing.T) {
			got, err := quoteSvc(on).QuoteTender(ctx, TenderQuote{Method: TenderCard, CardType: ct, AmountCents: 100000})
			if err != nil {
				t.Fatalf("quote: %v", err)
			}
			if got.SurchargeCents != 0 {
				t.Fatalf("%s was surcharged %d cents, Ontario prohibits surcharging debit and prepaid, and "+
					"UNKNOWN must fail to the safe side", ct, got.SurchargeCents)
			}
		})
	}

	t.Run("a dealer may narrow to NONE", func(t *testing.T) {
		none := activeCfg{MDRBps: 290, Surcharge: true, SurchargeCardTypes: "NONE"}
		got, _ := quoteSvc(none).QuoteTender(ctx, TenderQuote{Method: TenderCard, CardType: CardCredit, AmountCents: 100000})
		if got.SurchargeCents != 0 {
			t.Fatalf("surcharge_card_types=NONE still surcharged %d cents", got.SurchargeCents)
		}
	})

	for _, m := range []string{TenderCash, TenderCheque, TenderOther} {
		t.Run(m+" is never surcharged", func(t *testing.T) {
			got, err := quoteSvc(on).QuoteTender(ctx, TenderQuote{Method: m, AmountCents: 100000})
			if err != nil {
				t.Fatalf("quote: %v", err)
			}
			if got.SurchargeCents != 0 || got.TotalCents != 100000 {
				t.Fatalf("%s quoted a surcharge: %+v", m, got)
			}
		})
	}
}

func TestQuoteTender_EFTIsGatedByConfig(t *testing.T) {
	ctx := context.Background()

	// OFF is the shipped default: a tender nobody has been trained on must not be ringable.
	off := quoteSvc(activeCfg{EFTEnabled: false})
	if _, err := off.QuoteTender(ctx, TenderQuote{Method: TenderEFT, AmountCents: 50000}); err == nil {
		t.Fatal("EFT was quotable while eft_enabled is FALSE, 0106 ships it off deliberately")
	}
	methods, err := off.TenderMethods(ctx)
	if err != nil {
		t.Fatalf("tender methods: %v", err)
	}
	for _, m := range methods {
		if m.Method == TenderEFT {
			t.Fatal("EFT appeared in the tender list while disabled")
		}
	}

	// ON: quotable at par (an EFT costs the dealer a flat bank fee, not an MDR, so surcharging it
	// would be answering a question nobody asked).
	on := quoteSvc(activeCfg{EFTEnabled: true, Surcharge: true, MDRBps: 290, SurchargeCardTypes: "CREDIT"})
	got, err := on.QuoteTender(ctx, TenderQuote{Method: TenderEFT, AmountCents: 50000})
	if err != nil {
		t.Fatalf("EFT quote with the flag on: %v", err)
	}
	if got.SurchargeCents != 0 || got.TotalCents != 50000 {
		t.Fatalf("EFT quote = %+v, want par", got)
	}
	methods, _ = on.TenderMethods(ctx)
	var found bool
	for _, m := range methods {
		if m.Method == TenderEFT {
			found = true
		}
	}
	if !found {
		t.Fatal("EFT is enabled but missing from the tender list")
	}
}

func TestQuoteTender_RejectsNonsense(t *testing.T) {
	ctx := context.Background()
	svc := quoteSvc(activeCfg{Surcharge: true, MDRBps: 240, SurchargeCardTypes: "CREDIT"})
	for _, in := range []TenderQuote{
		{Method: "", AmountCents: 100},
		{Method: TenderCard, AmountCents: 0},
		{Method: TenderCard, AmountCents: -1},
	} {
		if _, err := svc.QuoteTender(ctx, in); err == nil {
			t.Fatalf("QuoteTender(%+v) succeeded, want a rejection", in)
		}
	}
}

func TestBpsPercent(t *testing.T) {
	for _, tc := range []struct {
		bps  int64
		want string
	}{{240, "2.4"}, {150, "1.5"}, {200, "2"}, {195, "1.95"}, {0, "0"}} {
		if got := bpsPercent(tc.bps); got != tc.want {
			t.Fatalf("bpsPercent(%d) = %q, want %q", tc.bps, got, tc.want)
		}
	}
}
