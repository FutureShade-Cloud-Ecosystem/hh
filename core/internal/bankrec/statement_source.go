package bankrec

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// CSVStatementSource is StatementSource impl #1: it parses a CSV export under a column-mapping profile
// (0033 bank_import_profiles.mapping). A Plaid/RBC-feed adapter is a later impl of the same interface -
// the Service depends only on the interface, so swapping parsers needs no service change (AC8).
type CSVStatementSource struct{}

// mapping keys read from ImportProfile.Mapping.
const (
	mapDateCol   = "date_col"
	mapDescCol   = "description_col"
	mapAmountCol = "amount_col"
	mapRefCol    = "reference_col"
	mapDateFmt   = "date_format"
	// mapDebitCol / mapCreditCol support statements that split debit and credit into separate columns.
	mapDebitCol  = "debit_col"
	mapCreditCol = "credit_col"
)

// Parse reads the CSV, resolves each column by header name from the profile mapping, normalizes the signed
// amount (+credit/−debit), and stamps a deterministic DedupeHash so a re-import is idempotent. A malformed
// header (missing a mapped column) or an unparseable row is a hard error the importer records as FAILED.
func (CSVStatementSource) Parse(_ context.Context, profile ImportProfile, r io.Reader) ([]RawStatementLine, error) {
	m := profile.Mapping
	dateCol := mapStr(m, mapDateCol)
	descCol := mapStr(m, mapDescCol)
	amtCol := mapStr(m, mapAmountCol)
	refCol := mapStr(m, mapRefCol)
	debitCol := mapStr(m, mapDebitCol)
	creditCol := mapStr(m, mapCreditCol)
	dateFmt := mapStr(m, mapDateFmt)
	if dateFmt == "" {
		dateFmt = "2006-01-02"
	}
	if dateCol == "" {
		return nil, fmt.Errorf("bankrec: profile %q missing date_col mapping", profile.Code)
	}
	hasSingleAmount := amtCol != ""
	if !hasSingleAmount && debitCol == "" && creditCol == "" {
		return nil, fmt.Errorf("bankrec: profile %q missing amount_col (or debit_col/credit_col) mapping", profile.Code)
	}

	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; we index by header
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("bankrec: read csv header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	need := func(col string) (int, error) {
		i, ok := idx[col]
		if !ok {
			return 0, fmt.Errorf("bankrec: csv is missing the mapped column %q", col)
		}
		return i, nil
	}
	dateI, err := need(dateCol)
	if err != nil {
		return nil, err
	}
	descI := optIdx(idx, descCol)
	refI := optIdx(idx, refCol)

	var amtI, debitI, creditI int
	if hasSingleAmount {
		if amtI, err = need(amtCol); err != nil {
			return nil, err
		}
	} else {
		if debitCol != "" {
			if debitI, err = need(debitCol); err != nil {
				return nil, err
			}
		} else {
			debitI = -1
		}
		if creditCol != "" {
			if creditI, err = need(creditCol); err != nil {
				return nil, err
			}
		} else {
			creditI = -1
		}
	}

	var out []RawStatementLine
	rowNo := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("bankrec: read csv row %d: %w", rowNo, err)
		}
		rowNo++
		if isBlankRow(rec) {
			continue
		}
		field := func(i int) string {
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		txnDate, perr := time.Parse(dateFmt, field(dateI))
		if perr != nil {
			return nil, fmt.Errorf("bankrec: csv row %d: bad date %q (want %q): %w", rowNo-1, field(dateI), dateFmt, perr)
		}
		var amountCents int64
		if hasSingleAmount {
			amountCents, perr = parseCents(field(amtI))
			if perr != nil {
				return nil, fmt.Errorf("bankrec: csv row %d: bad amount %q: %w", rowNo-1, field(amtI), perr)
			}
		} else {
			var debit, credit int64
			if debitI >= 0 && field(debitI) != "" {
				if debit, perr = parseCents(field(debitI)); perr != nil {
					return nil, fmt.Errorf("bankrec: csv row %d: bad debit %q: %w", rowNo-1, field(debitI), perr)
				}
			}
			if creditI >= 0 && field(creditI) != "" {
				if credit, perr = parseCents(field(creditI)); perr != nil {
					return nil, fmt.Errorf("bankrec: csv row %d: bad credit %q: %w", rowNo-1, field(creditI), perr)
				}
			}
			amountCents = abs64(credit) - abs64(debit) // credit positive, debit negative
		}
		desc := field(descI)
		ref := field(refI)
		out = append(out, RawStatementLine{
			TxnDate:     txnDate,
			Description: desc,
			Reference:   ref,
			AmountCents: amountCents,
			DedupeHash:  DedupeHash(txnDate, amountCents, desc, ref),
		})
	}
	return out, nil
}

// DedupeHash is the deterministic idempotency key over (date, signed amount, description, reference). A
// byte-identical re-import produces identical hashes, so the UNIQUE(bank_account_id, dedupe_hash) index
// silently drops the duplicates (AC1).
func DedupeHash(txnDate time.Time, amountCents int64, description, reference string) string {
	canonical := fmt.Sprintf("%s|%d|%s|%s",
		txnDate.UTC().Format("2006-01-02"),
		amountCents,
		strings.ToLower(strings.TrimSpace(description)),
		strings.ToLower(strings.TrimSpace(reference)),
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// parseCents converts a money string ("$1,234.56", "(45.00)", "-12.30", "1234") to signed integer cents.
// Parentheses and a leading minus both denote a negative (debit) amount.
func parseCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg = true
		s = s[1 : len(s)-1]
	}
	s = strings.NewReplacer("$", "", ",", "", " ", "", "CAD", "", "CA", "").Replace(s)
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	// Round to the nearest cent to avoid binary float drift (e.g. 1234.56 → 123456).
	cents := int64(f*100 + signRound(f))
	if neg {
		cents = -cents
	}
	return cents, nil
}

func signRound(f float64) float64 {
	if f < 0 {
		return -0.5
	}
	return 0.5
}

func mapStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func optIdx(idx map[string]int, col string) int {
	if col == "" {
		return -1
	}
	if i, ok := idx[col]; ok {
		return i
	}
	return -1
}

func isBlankRow(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
