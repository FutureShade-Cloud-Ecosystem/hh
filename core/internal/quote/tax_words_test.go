package quote

// tax_words_test.go, the name of the dealer's tax rides every quote surface as a field the screens
// render (item 3b): "HST" for an Ontario dealer exactly as before, "Sales tax" for a California one,
// and the neutral word when the dealer's jurisdiction could not be read. Terms, the public page, the
// staff preview and the acceptance confirmation all carry it.

import (
	"context"
	"errors"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

type fixedWords struct {
	w   string
	err error
}

func (f fixedWords) TaxLabel(context.Context) (string, error) { return f.w, f.err }

func TestTermsCarryTheDealersTaxWord(t *testing.T) {
	cases := []struct {
		name string
		src  *fixedWords
		want string
	}{
		{"unwired is the Ontario dealer", nil, "HST"},
		{"ontario", &fixedWords{w: "HST"}, "HST"},
		{"california", &fixedWords{w: "Sales tax"}, "Sales tax"},
		{"a failed read is the neutral word", &fixedWords{err: errors.New("down")}, "Tax"},
	}
	for _, c := range cases {
		store := newDib506Store("DRAFT")
		svc := dib506Svc(store, dib506Caller(identity.TierFrontline, identity.ScopeBranch))
		if c.src != nil {
			svc.WithTaxLabel(*c.src)
		}
		got, err := svc.UpdateTerms(context.Background(), qOrg, TermsInput{Fulfillment: "PICKUP"})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.TaxLabel != c.want {
			t.Errorf("%s: UpdateTerms tax label %q, want %q", c.name, got.TaxLabel, c.want)
		}
		read, err := svc.Terms(context.Background(), qOrg)
		if err != nil {
			t.Fatalf("%s: read: %v", c.name, err)
		}
		if read.TaxLabel != c.want {
			t.Errorf("%s: Terms tax label %q, want %q", c.name, read.TaxLabel, c.want)
		}
	}
}

// wordsShares is a share store answering one quote, for the public page and the acceptance.
type wordsShares struct {
	shareStore
}

func (wordsShares) PublicQuote(context.Context, string) (PublicQuoteView, error) {
	return PublicQuoteView{QuoteNo: "Q-1", Status: "SENT", TaxCents: 775}, nil
}

func (wordsShares) ResolveShareToken(context.Context, string) (ShareLink, string, error) {
	return ShareLink{ID: "l1", ExpiresAt: time.Now().Add(time.Hour)}, "q1", nil
}

func (wordsShares) LogShareEvent(context.Context, string, string) error { return nil }

func (wordsShares) AcceptQuoteGuarded(context.Context, acceptWrite, acceptHooks) (AcceptanceResult, error) {
	return AcceptanceResult{QuoteNo: "Q-1", TaxCents: 775}, nil
}

func TestPublicPageAndAcceptanceCarryTheDealersTaxWord(t *testing.T) {
	for _, c := range []struct {
		src  *fixedWords
		want string
	}{{nil, "HST"}, {&fixedWords{w: "Sales tax"}, "Sales tax"}, {&fixedWords{err: errors.New("x")}, "Tax"}} {
		svc := NewStaffService(newDib506Store("SENT")).WithShares(wordsShares{})
		if c.src != nil {
			svc.WithTaxLabel(*c.src)
		}
		view, err := svc.PublicQuoteByToken(context.Background(), "tok")
		if err != nil || view.TaxLabel != c.want {
			t.Errorf("public page: %q %v, want %q", view.TaxLabel, err, c.want)
		}
		acc, err := svc.AcceptByToken(context.Background(), "tok", "A Customer", true, true)
		if err != nil || acc.TaxLabel != c.want {
			t.Errorf("acceptance: %q %v, want %q", acc.TaxLabel, err, c.want)
		}
	}
}
