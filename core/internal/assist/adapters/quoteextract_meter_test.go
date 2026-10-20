package adapters

// quoteextract_meter_test.go, TanyAI slice 2: the quote extraction rails go through the same
// metering as Ask. A paid call is reserved before and settled after, inside the dealer cap, and
// counted in the asker's month; a refusal happens before the provider is reached.

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

// fakeSpend is the spend table: append only rows, summed the way the table folds them for these
// tests (every row here is either an open reservation or a settlement, never both for one call).
type fakeSpend struct {
	assist.SpendStore // the methods the rails never reach stay nil and would panic loudly
	rows              []assist.SpendRow
}

func (f *fakeSpend) Reserve(_ context.Context, r assist.SpendRow) error {
	f.rows = append(f.rows, r)
	return nil
}
func (f *fakeSpend) Settle(_ context.Context, r assist.SpendRow) error {
	f.rows = append(f.rows, r)
	return nil
}
func (f *fakeSpend) fold() []assist.SpendRow { return assist.FoldCalls(f.rows) }
func (f *fakeSpend) UserMonth(_ context.Context, user string, _, _ time.Time) (assist.Spend, error) {
	var s assist.Spend
	for _, r := range f.fold() {
		if r.StaffUserID == user {
			s.Exchanges++
			s.TokensIn += r.TokensIn
			s.TokensOut += r.TokensOut
		}
	}
	return s, nil
}
func (f *fakeSpend) DealerMonthMicroCents(context.Context, time.Time, time.Time) (int64, error) {
	var n int64
	for _, r := range f.fold() {
		n += r.CostMicroCents
	}
	return n, nil
}

type fixedPrice assist.ModelPrice

func (p fixedPrice) PriceFor(context.Context, string, string) (assist.ModelPrice, bool, error) {
	return assist.ModelPrice(p), true, nil
}

// paidProvider answers like a paid vendor under the name the price row names.
type paidProvider struct {
	calls int
	reply assist.Reply
}

func (p *paidProvider) Name() string  { return "spy" }
func (p *paidProvider) Model() string { return "spy-1" }
func (p *paidProvider) Complete(context.Context, assist.CompletionRequest) (assist.Reply, error) {
	p.calls++
	return p.reply, nil
}

func meteredRails(p assist.Provider, sp *fakeSpend, dealerCap int64) *QuoteExtractRails {
	price := fixedPrice{ID: "price-1", Alias: "spy", Model: "spy-1", InMicroPerM: 3_000_000, OutMicroPerM: 15_000_000, Seq: 1}
	svc := assist.NewService(assist.NewRegistry(assist.Deps{}, nil), p, &spyLedger{}, &spyLimits{}, assist.StaticSettings{}, nil).
		WithClock(func() time.Time { return fixedNow }).
		WithMetering(sp, price).
		WithDealerCap(dealerCap)
	return NewQuoteExtractRails(p, &spyLedger{}, &spyLimits{}, nil).
		WithClock(func() time.Time { return fixedNow }).
		WithCompleter(svc)
}

func TestQuoteExtractRails_APaidCallIsReservedAndSettledUnderTheAsker(t *testing.T) {
	sp := &fakeSpend{}
	p := &paidProvider{reply: assist.Reply{Text: `[{"text":"480 sq ft Blu 60"}]`, TokensIn: 400, TokensOut: 40}}
	rails := meteredRails(p, sp, 1<<60)

	if _, err := rails.SegmentText(context.Background(), rep(), "480 sq ft of Blu 60"); err != nil {
		t.Fatalf("SegmentText: %v", err)
	}
	if len(sp.rows) != 2 || sp.rows[0].Kind != assist.SpendReservation || sp.rows[1].Kind != assist.SpendSettlement {
		t.Fatalf("spend rows = %+v, want a reservation then a settlement", sp.rows)
	}
	set := sp.rows[1]
	if set.StaffUserID != "user-rep" || set.TokensIn != 400 || set.TokensOut != 40 || set.CostMicroCents == 0 {
		t.Fatalf("settlement = %+v, want the asker, the reported tokens and a cost", set)
	}
	month, _ := sp.UserMonth(context.Background(), "user-rep", time.Time{}, time.Time{})
	if month.TokensIn != 400 {
		t.Fatalf("the asker's month = %+v, want the call counted", month)
	}
}

func TestQuoteExtractRails_TheDealerCapRefusesBeforeTheProvider(t *testing.T) {
	sp := &fakeSpend{}
	sp.rows = append(sp.rows, assist.SpendRow{Kind: assist.SpendSettlement, ExchangeID: "other", CallNo: 1,
		Audience: assist.AudiencePro, PortalUserID: "p", CostMicroCents: 5_000_000})
	p := &paidProvider{reply: assist.Reply{Text: `[]`}}
	rails := meteredRails(p, sp, 5_000_000)

	_, err := rails.SegmentText(context.Background(), rep(), "480 sq ft of Blu 60")
	if err != assist.ErrOverBudget {
		t.Fatalf("err = %v, want the over budget refusal", err)
	}
	if p.calls != 0 || len(sp.rows) != 1 {
		t.Fatalf("provider calls %d, rows %d: the rails reached the provider or reserved past the cap", p.calls, len(sp.rows))
	}
}

func TestQuoteExtractRails_UnboundTheyRefuseAPaidProvider(t *testing.T) {
	p := &paidProvider{reply: assist.Reply{Text: `[]`}}
	rails := NewQuoteExtractRails(p, &spyLedger{}, &spyLimits{}, nil).WithClock(func() time.Time { return fixedNow })
	if _, err := rails.SegmentText(context.Background(), rep(), "x"); err == nil || p.calls != 0 {
		t.Fatalf("err %v calls %d: a paid provider was called with no meter bound", err, p.calls)
	}
}
