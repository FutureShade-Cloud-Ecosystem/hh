package payment

// currency.go, the card rail's currency discipline (item 3b slice 2). The rail's merchant account
// settles in the currency its payment_config row says; the dealer trades in the currency the dealer
// configuration says. They are the same unless someone changed one, and a charge in a currency the
// dealer does not trade in is money in the wrong unit, so a mismatch refuses before anything is sent.
// Refund and Void are deliberately not guarded: they reverse the original capture, in the currency it
// was captured in, and a disagreement must never trap money.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// DealerCurrencySource is the dealer's own currency, read through a seam the composition root adapts
// from the settings module's dealer configuration. Both reads are uncached. The Tx read exists for a
// caller that holds a connection for the length of its work (the pos Sale transaction): a pool read
// there makes the holder wait on a second connection, which a full pool of holders never frees.
type DealerCurrencySource interface {
	DealerCurrency(ctx context.Context) (string, error)
	DealerCurrencyTx(ctx context.Context, tx pgx.Tx) (string, error)
}

// dealerCurrencyOnPool is the pool carrier of the dealer currency read, nil when no source is wired.
func (s *Service) dealerCurrencyOnPool(ctx context.Context) (string, error) {
	return s.currency.DealerCurrency(ctx)
}

// guardCurrency refuses a charge whose rail currency differs from the dealer's, or whose dealer
// currency cannot be read. Both are not dispatched refusals: nothing was sent, no money moved. A
// service with no source wired is not cross-checked (the scaffold and the test fakes).
func (s *Service) guardCurrency(ctx context.Context, cfg activeCfg, read func(context.Context) (string, error)) error {
	if s.currency == nil {
		return nil
	}
	dealer, err := read(ctx)
	if err != nil {
		return notDispatched("payment: the dealer's currency could not be read, nothing was sent: %v", err)
	}
	if dealer != cfg.Currency {
		return notDispatched("payment: the card rail is configured for %s but the dealer trades in %s; reconfigure the card rail before charging, nothing was sent", cfg.Currency, dealer)
	}
	return nil
}

// configureCurrency answers the currency Configure stores. With a dealer source, a blank currency is
// the dealer's and a stated one must be the dealer's; without one a blank currency keeps the Ontario
// default (the scaffold and the test fakes).
func (s *Store) configureCurrency(ctx context.Context, stated string) (string, error) {
	if s.currency == nil {
		if stated == "" {
			return "CAD", nil
		}
		return stated, nil
	}
	dealer, err := s.currency.DealerCurrency(ctx)
	if err != nil {
		return "", fmt.Errorf("payment: the dealer's currency could not be read, the card rail was not configured: %w", err)
	}
	if stated != "" && stated != dealer {
		return "", fmt.Errorf("payment: the card rail currency %s differs from the dealer's currency %s, the card rail was not configured", stated, dealer)
	}
	return dealer, nil
}
