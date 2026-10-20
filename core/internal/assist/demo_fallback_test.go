package assist

// demo_fallback_test.go, TanyAI slice 3 sub item 3: on a demo copy, with no key or with a cap
// reached, the assistant answers on the stub and says so with the model_unavailable notice. It
// is an answer, never an error, and the paid provider is not reached.

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
)

// demoPremiumRig is a service whose settings route to tanyai-premium, with a strict fake behind
// it, the stub fallback switched on (as the demo composition does) and the given key and cap.
func demoPremiumRig(t *testing.T, key redact.Secret, sp *memSpend, prices memPrices, dealerCap int64) (*Service, *strictAnthropic) {
	t.Helper()
	fake := newStrictAnthropic(t, antTextReply("the model answered"))
	rows := []AliasRow{{Alias: AliasPremium, AdapterKind: AdapterAnthropic, Model: "claude-sonnet-5-5", Enabled: true}}
	router := NewRouter(StaticAliases(rows), ModelConfig{AnthropicKey: key, AnthropicBaseURL: fake.URL()}, NewStubProvider(), nil)
	set := StaticSettings{KeyProvider: ProviderAnthropic, KeyModel: AliasPremium}
	svc := chatService(NewStubProvider(), &fakeLedger{}, newFakeLimits(), set, seededReaders()).
		WithRouter(router).
		WithMetering(sp, prices).
		WithDealerCap(dealerCap).
		WithStubFallback()
	return svc, fake
}

var premiumPrices = memPrices{AliasPremium + "/claude-sonnet-5-5": {ID: "pp", Alias: AliasPremium, Model: "claude-sonnet-5-5", InMicroPerM: 300, OutMicroPerM: 1500, Seq: 1}}

func wantModelUnavailable(t *testing.T, what string, ans Answer, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: a fallback is an answer, not an error: %v", what, err)
	}
	if ans.Notice == nil || ans.Notice.Code != NoticeModelUnavailable {
		t.Fatalf("%s: notice = %+v, want %s", what, ans.Notice, NoticeModelUnavailable)
	}
	if ans.LimitReached {
		t.Fatalf("%s: the dealer cap is not the asker's allowance, LimitReached must stay false", what)
	}
	if ans.Usage == nil || ans.Usage.Provider != "stub" {
		t.Fatalf("%s: usage = %+v, want the stub as the answerer", what, ans.Usage)
	}
	if len(ans.Results) != 1 || ans.Results[0].Command != "order.lookup" {
		t.Fatalf("%s: the stub's plain order result is the answer, got %+v", what, ans.Results)
	}
}

func TestDemoFallback_NoKeyAnswersOnTheStubAndSaysSo(t *testing.T) {
	svc, fake := demoPremiumRig(t, redact.NewSecret(""), &memSpend{}, premiumPrices, 1<<60)
	ans, err := svc.Ask(context.Background(), manager(), "where is order SO-4?", nil)
	wantModelUnavailable(t, "no key", ans, err)
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("the vendor saw %d requests with no key", n)
	}
}

func TestDemoFallback_DealerCapReachedAnswersOnTheStubAndSaysSo(t *testing.T) {
	sp := &memSpend{}
	seedDealerSpend(sp, 8_000_000)
	svc, fake := demoPremiumRig(t, redact.NewSecret("k"), sp, premiumPrices, 8_000_000)
	ans, err := svc.Ask(context.Background(), manager(), "where is order SO-4?", nil)
	wantModelUnavailable(t, "cap reached", ans, err)
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("the vendor saw %d requests after the cap was reached", n)
	}
	for _, r := range sp.rows {
		if r.Alias == "stub" && r.CostMicroCents != 0 {
			t.Fatalf("the stub carries a cost: %+v", r)
		}
	}
}

func TestDemoFallback_NoCapConfiguredAnswersOnTheStubAndSaysSo(t *testing.T) {
	svc, fake := demoPremiumRig(t, redact.NewSecret("k"), &memSpend{}, premiumPrices, 0)
	ans, err := svc.Ask(context.Background(), manager(), "where is order SO-4?", nil)
	wantModelUnavailable(t, "no cap", ans, err)
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("the vendor saw %d requests with no cap", n)
	}
}

func TestDemoFallback_NoPriceRowAnswersOnTheStubAndSaysSo(t *testing.T) {
	svc, fake := demoPremiumRig(t, redact.NewSecret("k"), &memSpend{}, memPrices{}, 1<<60)
	ans, err := svc.Ask(context.Background(), manager(), "where is order SO-4?", nil)
	wantModelUnavailable(t, "no price row", ans, err)
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("the vendor saw %d requests with no price row", n)
	}
}

// The notice is for the fallback only: a keyed, capped demo answers on the model with no notice.
func TestDemoFallback_ModelAnswersCarryNoNotice(t *testing.T) {
	svc, fake := demoPremiumRig(t, redact.NewSecret("k"), &memSpend{}, premiumPrices, 1<<60)
	ans, err := svc.Ask(context.Background(), manager(), "hello", nil)
	if err != nil || ans.Text != "the model answered" {
		t.Fatalf("ask = %q (%v)", ans.Text, err)
	}
	if ans.Notice != nil {
		t.Fatalf("notice = %+v on a model answer", ans.Notice)
	}
	if n := len(fake.Requests()); n != 1 {
		t.Fatalf("vendor requests = %d, want 1", n)
	}
}

// Without the fallback switch (every build but the demo) a cap reached stays the plain over
// limit answer of slice 2, so the demo's behaviour is not the product's.
func TestDemoFallback_OffOutsideTheDemo(t *testing.T) {
	sp := &memSpend{}
	seedDealerSpend(sp, 8_000_000)
	svc, _ := demoPremiumRig(t, redact.NewSecret("k"), sp, premiumPrices, 8_000_000)
	svc.stubFallback = false
	ans, err := svc.Ask(context.Background(), manager(), "where is order SO-4?", nil)
	if err != nil || !ans.LimitReached || ans.Notice != nil {
		t.Fatalf("answer = %+v (%v), want the slice 2 limit answer and no notice", ans, err)
	}
}
