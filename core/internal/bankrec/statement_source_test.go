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

// The parser accepts exactly what it accepted before the display text lost its CA prefix: a
// CA$ or CAD mark is stripped, a bare CA or a CA inside digits is refused.
func TestParseCents_CurrencyMarks(t *testing.T) {
	ok := map[string]int64{
		"CA$1,234.56":  123456,
		"$1,234.56":    123456,
		"-CA$12.00":    -1200,
		"(CA$12.00)":   -1200,
		"CA$-12.00":    -1200,
		"CA$ 5":        500,
		"1,234.56 CAD": 123456,
		"CAD 1,234.56": 123456,
		"-$12.00":      -1200,
		"(12.00)":      -1200,
	}
	for in, want := range ok {
		got, err := parseCents(in)
		if err != nil || got != want {
			t.Errorf("parseCents(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"CA", "CA ", "CA 1,234.56", "CA1,234.56", "5CA2", "CA11CA2", "ca$5", "US$5", "USD 5"} {
		if got, err := parseCents(in); err == nil {
			t.Errorf("parseCents(%q) = %d, want a refusal", in, got)
		}
	}
}

// FW1-04 item 6: Canadian bank CSV profile (RBC native CAD export with Transaction Date, Description 1,
// Description 2, Cheque Number, and CAD$).
func TestCSVParse_RBCNativeExport(t *testing.T) {
	raw, err := os.ReadFile("testdata/rbc_export_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	for _, prof := range []ImportProfile{DefaultRBCProfile(), rbcProfile()} {
		lines, err := CSVStatementSource{}.Parse(context.Background(), prof, strings.NewReader(string(raw)))
		if err != nil {
			t.Fatalf("parse native RBC export under %s: %v", prof.Code, err)
		}
		if len(lines) != 4 {
			t.Fatalf("%s: expected 4 lines, got %d", prof.Code, len(lines))
		}
		wantAmounts := []int64{150000, -42000, 98050, -2500}
		for i, w := range wantAmounts {
			if lines[i].AmountCents != w {
				t.Errorf("%s line %d amount = %d, want %d", prof.Code, i, lines[i].AmountCents, w)
			}
		}
		if lines[0].Description != "E-TRANSFER DEPOSIT HARDWOOD DEPOT INC" || lines[0].Reference != "HARDWOOD DEPOT INC" {
			t.Errorf("%s line 0 desc=%q ref=%q", prof.Code, lines[0].Description, lines[0].Reference)
		}
		if lines[1].Reference != "1042" {
			t.Errorf("%s line 1 cheque ref = %q, want 1042", prof.Code, lines[1].Reference)
		}
	}
}

// FW1-04 round 2 (item 2.3): an RBC row whose CAD$ amount is empty and carries a figure under USD$, or whose
// amount column is empty, must be refused in words rather than parsed as a 0-cent CAD line.
func TestCSVParse_RBCRefusesUSDAndEmptyAmountRows(t *testing.T) {
	usdCSV := "Account Type,Account Number,Transaction Date,Cheque Number,Description 1,Description 2,CAD$,USD$\n" +
		"Chequing,00123-1234567,6/10/2023,,USD WIRE PAYMENT,US VENDOR,,250.00\n"
	emptyNativeCSV := "Account Type,Account Number,Transaction Date,Cheque Number,Description 1,Description 2,CAD$,USD$\n" +
		"Chequing,00123-1234567,6/10/2023,,EMPTY ROW,MEMO,,\n"
	emptyStandardCSV := "Date,Description,Amount,Reference\n2023-06-10,EMPTY AMOUNT,,REF-1\n"

	src := CSVStatementSource{}
	for _, prof := range []ImportProfile{DefaultRBCProfile(), rbcProfile()} {
		if lines, err := src.Parse(context.Background(), prof, strings.NewReader(usdCSV)); err == nil || !strings.Contains(err.Error(), "USD") {
			t.Errorf("%s: expected USD row refusal mentioning USD, got lines=%+v err=%v", prof.Code, lines, err)
		}
		if lines, err := src.Parse(context.Background(), prof, strings.NewReader(emptyNativeCSV)); err == nil || !strings.Contains(err.Error(), "missing amount") {
			t.Errorf("%s: expected empty native amount row refusal mentioning missing amount, got lines=%+v err=%v", prof.Code, lines, err)
		}
	}
	if lines, err := src.Parse(context.Background(), rbcProfile(), strings.NewReader(emptyStandardCSV)); err == nil || !strings.Contains(err.Error(), "missing amount") {
		t.Errorf("standard profile: expected empty amount row refusal mentioning missing amount, got lines=%+v err=%v", lines, err)
	}
}
