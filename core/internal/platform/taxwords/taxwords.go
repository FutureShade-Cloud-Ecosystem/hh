// Package taxwords is the one place the name of a dealer's tax is decided: "HST" for an Ontario
// dealer, exactly as before the tax engine bound by jurisdiction, and "Sales tax" for every other
// jurisdiction. A pure leaf with no state, so the quote, receipt, till and ledger modules each take
// the word from the dealer's jurisdiction without importing one another, and the screens render the
// field the core sends rather than guessing from a rate or a country.
package taxwords

import (
	"context"
	"strings"
)

// Ontario is the jurisdiction code whose tax is the HST.
const Ontario = "ON"

// The words.
const (
	HST      = "HST"
	SalesTax = "Sales tax"
	// Neutral is the word a surface uses when the dealer's jurisdiction could not be read: true for
	// every dealer, so a failed read never prints the wrong tax's name.
	Neutral = "Tax"
)

// For answers the name of the standard tax of a jurisdiction. A blank jurisdiction is Ontario, the
// dealer a configuration with no tax region has always been.
func For(jurisdiction string) string {
	j := strings.ToUpper(strings.TrimSpace(jurisdiction))
	if j == "" || j == Ontario {
		return HST
	}
	return SalesTax
}

// Source answers the word for the dealer's own jurisdiction. The composition root binds an adapter
// over the settings module's dealer configuration; a consumer holds a nil Source as the Ontario
// dealer.
type Source interface {
	TaxLabel(ctx context.Context) (string, error)
}

// Resolve reads a Source: nil is "HST" (the Ontario dealer), a failed read is the neutral word and
// the error, so a caller that must refuse can, and one that may degrade does.
func Resolve(ctx context.Context, s Source) (string, error) {
	if s == nil {
		return HST, nil
	}
	w, err := s.TaxLabel(ctx)
	if err != nil || strings.TrimSpace(w) == "" {
		return Neutral, err
	}
	return w, nil
}
