package quote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Go default and the column default are one wording: a deployment whose policy row is missing must
// read the same notice the column serves.
func TestDefaultQuantityNoticeEqualsTheColumnDefault(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0367_quote_policy_quantity_notice.sql"))
	if err != nil {
		t.Fatalf("reading 0367: %v", err)
	}
	if !strings.Contains(string(body), "DEFAULT '"+DefaultQuantityNotice+"'") {
		t.Errorf("0367 does not default the column to DefaultQuantityNotice %q", DefaultQuantityNotice)
	}
	if n := len([]rune(DefaultQuantityNotice)); n < 1 || n > maxQuantityNotice {
		t.Errorf("the default is %d characters, outside the bound", n)
	}
}

func TestValidateQuotePolicy_QuantityNotice(t *testing.T) {
	p, err := validateQuotePolicy(QuotePolicy{CostVisibility: "MANAGER_UP", QuantityNotice: "  Check them.  "})
	if err != nil || p.QuantityNotice != "Check them." {
		t.Fatalf("trimmed notice = %q, %v", p.QuantityNotice, err)
	}
	p, err = validateQuotePolicy(QuotePolicy{CostVisibility: "MANAGER_UP"})
	if err != nil || p.QuantityNotice != DefaultQuantityNotice {
		t.Fatalf("blank must default; got %q, %v", p.QuantityNotice, err)
	}
	if _, err = validateQuotePolicy(QuotePolicy{CostVisibility: "MANAGER_UP", QuantityNotice: strings.Repeat("x", maxQuantityNotice+1)}); err == nil {
		t.Error("an over long notice must be refused")
	}
}
