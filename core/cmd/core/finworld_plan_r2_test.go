//go:build finworld

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// finworldTestDealerName is the invented dealer name the tests hand to the plan. The plan never invents a
// dealer name of its own: the caller reads the deploy's setting and passes it in.
const finworldTestDealerName = "Quarryfen Hardscape Supply"

// TestFinWorldPlan_DealerNameComesFromTheCaller asserts the plan uses the dealer name it is handed and
// derives the legal entity name from it.
func TestFinWorldPlan_DealerNameComesFromTheCaller(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s, %q) failed: %v", anchor, finworldTestDealerName, err)
	}
	if plan.Dealer.Name != finworldTestDealerName {
		t.Fatalf("dealer name = %q, want the caller's %q", plan.Dealer.Name, finworldTestDealerName)
	}
	if plan.Dealer.LegalName == plan.Dealer.Name {
		t.Fatalf("legal name %q must be derived from the dealer name, not copy it", plan.Dealer.LegalName)
	}
	if !strings.HasPrefix(plan.Dealer.LegalName, finworldTestDealerName) {
		t.Fatalf("legal name %q is not derived from the caller's dealer name %q",
			plan.Dealer.LegalName, finworldTestDealerName)
	}
}

// TestFinWorldPlan_RejectsAnEmptyDealerName asserts the plan refuses to build a world with no dealer name.
func TestFinWorldPlan_RejectsAnEmptyDealerName(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	if _, err := FinWorldPlan(1, anchor, ""); err == nil {
		t.Fatal("FinWorldPlan with an empty dealer name returned no error")
	}
}

// finworldTaxEngineMigration is the tax engine's own rate table. The engine reads these rows live at
// every assessment, so they are the source of truth for the plan's rate constants.
const finworldTaxEngineMigration = "0042_tax_engine.sql"

// finworldEngineTaxRates parses the tax_rates rows out of the tax engine migration and returns the rate in
// bps keyed by reason code.
func finworldEngineTaxRates(t *testing.T) map[string]int {
	t.Helper()
	path := filepath.Join(finworldPackageDir(t), "..", "..", "db", "migrations", finworldTaxEngineMigration)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the tax engine migration %s: %v", path, err)
	}
	text := string(src)

	const marker = "INSERT INTO tax_rates"
	start := strings.Index(text, marker)
	if start < 0 {
		t.Fatalf("no %s statement in %s", marker, path)
	}
	end := strings.Index(text[start:], ";")
	if end < 0 {
		t.Fatalf("no terminating semicolon in the %s statement of %s", marker, path)
	}

	rates := make(map[string]int)
	rowRe := regexp.MustCompile(`'ON'\s*,\s*'([A-Z0-9_]+)'\s*,\s*(\d+)`)
	for _, m := range rowRe.FindAllStringSubmatch(text[start:start+end], -1) {
		bps, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("parse the rate %q of reason %q: %v", m[2], m[1], err)
		}
		rates[m[1]] = bps
	}
	if len(rates) == 0 {
		t.Fatalf("no tax_rates rows parsed from %s", path)
	}
	return rates
}

// TestFinWorldPlan_TaxRatesMatchTheEngineTable asserts the plan's rate constants are the tax engine's own
// rows. The engine reads tax_rates at every assessment, so a plan carrying a different number would post a
// tax the engine never charged.
func TestFinWorldPlan_TaxRatesMatchTheEngineTable(t *testing.T) {
	rates := finworldEngineTaxRates(t)

	cases := []struct {
		reason    string
		planBps   int
		planLabel string
	}{
		{FinWorldTaxReasonStandardON, FinWorldTaxRateStandardBps, "standard rate"},
		{FinWorldTaxReasonReliefFN8, FinWorldTaxRateReliefBps, "relief rate"},
	}
	for _, c := range cases {
		engineBps, ok := rates[c.reason]
		if !ok {
			t.Fatalf("the tax engine migration has no tax_rates row for %q", c.reason)
		}
		if c.planBps != engineBps {
			t.Fatalf("the plan's %s for %q is %d bps, the tax engine's tax_rates row is %d bps",
				c.planLabel, c.reason, c.planBps, engineBps)
		}
	}

	// The Ontario credit tracked alongside the relief rate is the standard less the relief rate: the 13%
	// standard minus the 5% net the engine collects is the 8% credited at the point of sale.
	wantCredit := rates[FinWorldTaxReasonStandardON] - rates[FinWorldTaxReasonReliefFN8]
	if FinWorldTaxOntarioCreditBps != wantCredit {
		t.Fatalf("the plan's Ontario credit is %d bps, the tax engine's rows imply %d bps (the standard less the relief rate)",
			FinWorldTaxOntarioCreditBps, wantCredit)
	}
}

// TestFinWorldPlan_ReliefLinesCarryTheEngineRateAndCredit asserts every relief line in the plan is taxed
// at the engine's ON_FN_POS_8 rate and carries the Ontario credit the engine tracks alongside it.
func TestFinWorldPlan_ReliefLinesCarryTheEngineRateAndCredit(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s, %q) failed: %v", anchor, finworldTestDealerName, err)
	}

	rates := finworldEngineTaxRates(t)
	reliefLines := 0
	for _, m := range plan.Months {
		for _, td := range m.TradingDays {
			for _, ev := range td.Events {
				if ev.Sale == nil {
					continue
				}
				for _, ln := range ev.Sale.Lines {
					switch ln.TaxReasonCode {
					case FinWorldTaxReasonReliefFN8:
						reliefLines++
						if ln.TaxRateBps != rates[FinWorldTaxReasonReliefFN8] {
							t.Fatalf("relief line %s rate %d, the engine's row is %d",
								ln.LineKey, ln.TaxRateBps, rates[FinWorldTaxReasonReliefFN8])
						}
						wantTax := finworldHSTOn(ln.LineSubtotalCents, rates[FinWorldTaxReasonReliefFN8])
						if ln.TaxCents != wantTax {
							t.Fatalf("relief line %s tax %d, want hstOn(%d, %d)",
								ln.LineKey, ln.TaxCents, ln.LineSubtotalCents, rates[FinWorldTaxReasonReliefFN8])
						}
						wantCredit := finworldHSTOn(ln.LineSubtotalCents, FinWorldTaxOntarioCreditBps)
						if ln.OntarioCreditCents != wantCredit {
							t.Fatalf("relief line %s Ontario credit %d, want hstOn(%d, %d)",
								ln.LineKey, ln.OntarioCreditCents, ln.LineSubtotalCents, FinWorldTaxOntarioCreditBps)
						}
						// The engine collects the bound rate and tracks the credit beside it, so the net
						// collected equals the assessed tax.
						if ln.NetTaxAfterCreditCents != ln.TaxCents {
							t.Fatalf("relief line %s net tax %d, the assessed tax is %d",
								ln.LineKey, ln.NetTaxAfterCreditCents, ln.TaxCents)
						}
					case FinWorldTaxReasonStandardON:
						if ln.TaxRateBps != rates[FinWorldTaxReasonStandardON] {
							t.Fatalf("standard line %s rate %d, the engine's row is %d",
								ln.LineKey, ln.TaxRateBps, rates[FinWorldTaxReasonStandardON])
						}
						if ln.OntarioCreditCents != 0 {
							t.Fatalf("standard line %s carries an Ontario credit of %d", ln.LineKey, ln.OntarioCreditCents)
						}
					default:
						t.Fatalf("line %s has tax reason %q", ln.LineKey, ln.TaxReasonCode)
					}
				}
			}
		}
	}
	if reliefLines == 0 {
		t.Fatal("the plan has no relief lines")
	}
}

// TestFinWorldPlan_WordListsCarryNoRealBusinessName asserts the invented word lists carry no real
// business's trading name. Stonebridge is a live trading name in the landscape and masonry trade, so
// the street and word list entries that used it are replaced with plainly invented words.
func TestFinWorldPlan_WordListsCarryNoRealBusinessName(t *testing.T) {
	forbidden := []string{"stonebridge"}

	lists := []struct {
		label string
		words []string
	}{
		{"the customer first word list", finworldCustomerFirstWords},
		{"the customer second word list", finworldCustomerSecondWords},
		{"the street name list", finworldStreetNames},
		{"the contractor trade word list", finworldContractorTradeWords},
		{"the contractor suffix list", finworldContractorSuffixes},
		{"the household word list", finworldHouseholdWords},
	}
	for _, l := range lists {
		for _, entry := range l.words {
			lower := strings.ToLower(entry)
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					t.Fatalf("%s carries the entry %q, which holds the trading name of a real business", l.label, entry)
				}
			}
		}
	}
}

// finworldPackageDir returns the directory holding this package's Go source files.
func finworldPackageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the test file path")
	}
	return filepath.Dir(file)
}

// finworldNonTestSourceFiles lists the package's non test finworld_*.go files.
func finworldNonTestSourceFiles(t *testing.T) []string {
	t.Helper()
	dir := finworldPackageDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "finworld_") || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	return files
}

// TestFinWorldPlan_NoDealerNameLiteralInSource asserts the world code carries no dealer name literal.
// The dealer's display name and its legal entity name are inputs the caller reads from the deploy,
// never strings the plan writes out.
func TestFinWorldPlan_NoDealerNameLiteralInSource(t *testing.T) {
	files := finworldNonTestSourceFiles(t)
	if len(files) == 0 {
		t.Fatal("no non test finworld_*.go files found to scan")
	}

	forbidden := []string{
		"Stone & Yard Supply",
		"Stonebridge Ridge",
	}

	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(src)
		for _, bad := range forbidden {
			if strings.Contains(text, bad) {
				t.Fatalf("%s contains the dealer name literal %q: the dealer's name is an input, not a literal",
					filepath.Base(path), bad)
			}
		}
	}
}
