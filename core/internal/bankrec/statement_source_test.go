package bankrec

import (
	"context"
	"os"
	"strings"
	"testing"
)

func rbcProfile() ImportProfile {
	return ImportProfile{
		Code:   "rbc-default",
		Format: "CSV",
		Mapping: map[string]any{
			"date_col":        "Date",
			"description_col": "Description",
			"amount_col":      "Amount",
			"reference_col":   "Reference",
			"date_format":     "2006-01-02",
		},
	}
}

func visaProfile() ImportProfile {
	p := rbcProfile()
	p.Code = "visa-default"
	p.Mapping["date_col"] = "Transaction Date"
	return p
}

// AC8, the CSV StatementSource parses a profile-mapped export into normalized signed lines.
func TestCSVParse_RBCGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/rbc_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := CSVStatementSource{}.Parse(context.Background(), rbcProfile(), strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	wantAmounts := []int64{100000, 50000, 98000, -4500, 25050}
	for i, w := range wantAmounts {
		if lines[i].AmountCents != w {
			t.Errorf("line %d amount = %d, want %d", i, lines[i].AmountCents, w)
		}
		if lines[i].DedupeHash == "" {
			t.Errorf("line %d has no dedupe hash", i)
		}
	}
	if lines[0].Reference != "GTW-1001" {
		t.Errorf("line 0 ref = %q", lines[0].Reference)
	}
	if lines[3].AmountCents >= 0 {
		t.Errorf("a parenthesized debit must be negative, got %d", lines[3].AmountCents)
	}
}

// AC8, a second profile (visa, different date column) parses under the same pluggable interface.
func TestCSVParse_VisaGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/visa_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := CSVStatementSource{}.Parse(context.Background(), visaProfile(), strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	want := []int64{-12045, -8810, 45000}
	for i, w := range want {
		if lines[i].AmountCents != w {
			t.Errorf("visa line %d amount = %d, want %d", i, lines[i].AmountCents, w)
		}
	}
}

// AC1, dedupe hashing is deterministic: re-parsing the identical file yields identical hashes, which is
// what makes the DB re-import a no-op.
func TestCSVParse_DedupeDeterministic(t *testing.T) {
	raw, err := os.ReadFile("testdata/rbc_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := CSVStatementSource{}.Parse(context.Background(), rbcProfile(), strings.NewReader(string(raw)))
	second, _ := CSVStatementSource{}.Parse(context.Background(), rbcProfile(), strings.NewReader(string(raw)))
	if len(first) != len(second) {
		t.Fatal("parse count differs across runs")
	}
	seen := map[string]bool{}
	for i := range first {
		if first[i].DedupeHash != second[i].DedupeHash {
			t.Errorf("line %d hash not stable: %s vs %s", i, first[i].DedupeHash, second[i].DedupeHash)
		}
		if seen[first[i].DedupeHash] {
			t.Errorf("line %d produced a duplicate hash within one file", i)
		}
		seen[first[i].DedupeHash] = true
	}
}

// AC8, a mapping that points at a missing column is a hard parse error (recorded as a FAILED import).
func TestCSVParse_MissingColumn(t *testing.T) {
	p := rbcProfile()
	p.Mapping["amount_col"] = "Nonexistent"
	_, err := CSVStatementSource{}.Parse(context.Background(), p, strings.NewReader("Date,Description,Amount,Reference\n2026-06-01,x,1.00,r\n"))
	if err == nil {
		t.Fatal("expected an error for a missing mapped column")
	}
}

func TestParseCents(t *testing.T) {
	cases := map[string]int64{
		"1000.00":   100000,
		"$1,234.56": 123456,
		"(45.00)":   -4500,
		"-12.30":    -1230,
		"250.5":     25050,
		"0":         0,
		"":          0,
	}
	for in, want := range cases {
		got, err := parseCents(in)
		if err != nil {
			t.Errorf("parseCents(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseCents(%q) = %d, want %d", in, got, want)
		}
	}
}
