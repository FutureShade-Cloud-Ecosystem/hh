package quote

// terms_standard_rate_test.go, the tax the terms write stamps on a quote follows the dealer's standard
// rate (item 3b): with the engine's rate wired the stamp is the engine's, so a quote and the ring it
// becomes agree; unwired, the dealer's system_settings figure stands, and a failed rate read refuses the
// write rather than stamping a rate the dealer does not charge.

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

type rateCapturingStore struct {
	*dib506Store
	gotRate int64
}

func (s *rateCapturingStore) UpdateTermsGuarded(ctx context.Context, id string, in TermsInput, from []string, actor string, rate int64) (QuoteTerms, error) {
	s.gotRate = rate
	return s.dib506Store.UpdateTermsGuarded(ctx, id, in, from, actor, rate)
}

type fakeQuoteStandardRate struct {
	bps int
	err error
}

func (f fakeQuoteStandardRate) StandardRateBps(context.Context, string) (int, error) {
	return f.bps, f.err
}

func termsWith(t *testing.T, r StandardRateReader) (int64, error) {
	t.Helper()
	store := &rateCapturingStore{dib506Store: newDib506Store("DRAFT")}
	svc := dib506Svc(store.dib506Store, dib506Caller(identity.TierFrontline, identity.ScopeBranch))
	svc.store = store // the capturing wrapper answers the same builder store
	if r != nil {
		svc.WithStandardRate(r)
	}
	_, err := svc.UpdateTerms(context.Background(), qOrg, TermsInput{Fulfillment: "PICKUP"})
	return store.gotRate, err
}

func TestTerms_StampsTheEnginesStandardRateWhenWired(t *testing.T) {
	if got, err := termsWith(t, fakeQuoteStandardRate{bps: 775}); err != nil || got != 775 {
		t.Fatalf("california: rate %d err %v, want 775", got, err)
	}
}

func TestTerms_UnwiredKeepsTheSettingsFigure(t *testing.T) {
	if got, err := termsWith(t, nil); err != nil || got != 1300 {
		t.Fatalf("unwired: rate %d err %v, want the settings figure 1300", got, err)
	}
}

func TestTerms_AFailedRateReadRefusesTheWrite(t *testing.T) {
	if _, err := termsWith(t, fakeQuoteStandardRate{err: errors.New("no standard rate")}); err == nil {
		t.Fatal("terms were stamped at a guessed rate")
	}
}
