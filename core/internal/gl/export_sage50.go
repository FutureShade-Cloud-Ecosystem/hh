package gl

// export_sage50.go, W3 AX-3 item 1: the Sage 50 target's general journal import file. Sage 50's
// own help, "Importing General Journal Entries"
// (https://help-sage50.na.sage.com/en-ca/core/2017/Content/Import_Export/ImportingGeneralJournalEntries.htm),
// takes a plain text file, comma delimited, text fields in straight double quotes, one transaction
// after another and no file header: the transaction's first line is the date as mm-dd-yy, then the
// source in quotes (up to twenty characters), then the comment in quotes (up to 75 characters);
// then one detail line per GL line, the account number, the amount (debits positive, credits
// negative, two decimals), the line comment in quotes and 0 for no project allocation lines. The
// page's balancing rule is the tie this body asserts: a transaction's amounts "must balance to
// zero", so every transaction sums to zero and the file in total does, or the export refuses in
// words naming the entry before any line is written. No tax lines: the GL's lines already carry
// the tax accounts. The file is the import file itself, so nothing rides above the first
// transaction's first line, the same rule the QBO body holds.
//
// The tie is asserted, not assumed, and the caller records its batch only after this answers, so
// a refused period leaves nothing behind.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/csvsafe"
)

// renderSage50Journals renders the period's posted lines as Sage 50 general journal transactions,
// one transaction per GL journal, the entry's lines in entry order as the QBO body groups them
// (ExportRows orders by entry_no). Every account must come through the mapping: a row whose
// account has no mapped package code refuses the whole file in words, the same rule the
// unmapped-movement read refuses by, so the two reads cannot drift apart and hand the file a hole.
func renderSage50Journals(rows []ExportRow, packageAccount map[string]string) (string, error) {
	target := exportTargetWords(TargetSage50)
	var b strings.Builder
	entryNo := 0
	var entrySum, totalSum int64
	// entryTie is the transaction's own assertion, run when the entry changes and once more after
	// the last row: Sage 50 imports a transaction only when its amounts balance to zero, so the
	// file refuses in words naming the entry rather than shipping one that does not.
	entryTie := func() error {
		if entryNo == 0 {
			return nil
		}
		if entrySum != 0 {
			return badReq("gl: the %s export refuses the period: journal entry %d does not sum to zero (its amounts net %s); no line was written",
				target, entryNo, dollarsExact(entrySum))
		}
		return nil
	}
	for _, r := range rows {
		if r.EntryNo != entryNo {
			if err := entryTie(); err != nil {
				return "", err
			}
			entryNo = r.EntryNo
			entrySum = 0
			// The transaction's first line: the posted house day as mm-dd-yy, the journal's own
			// number as the source, the journal's memo as the comment. The source is
			// gl_journals.entry_no, the row's EntryNo: the number every GL report shows, minted
			// per fiscal year and unique within it, so the bookkeeper can trace the imported
			// transaction back to the journal it came from; Sage's page says the source is
			// typically a document number, and the GL journal's own number is exactly that. The
			// comment is the entry's first line's memo, because v_gl_detail carries the journal's
			// memo on every line unless the line carries one of its own.
			fmt.Fprintf(&b, "%s,%s,%s\r\n",
				sageMMDDYY(r.PostedAt),
				sageQuoted(sageCut(sageText(strconv.Itoa(r.EntryNo)), 20)),
				sageQuoted(sageCut(sageText(r.Memo), 75)))
		}
		pkg := packageAccount[r.AccountCode]
		if pkg == "" {
			return "", badReq("gl: the %s export refuses the period: %s %s has movement and no mapping for the target; set the mapping before exporting",
				target, r.AccountCode, r.AccountName)
		}
		// The account cell is not a text field and needs no quote guard: the shape check below
		// admits digits only, and the amount cell is money, never escaped.
		if err := sageAccountShape(pkg, r.AccountCode, r.AccountName, target); err != nil {
			return "", err
		}
		amount := r.DebitCents - r.CreditCents
		entrySum += amount
		totalSum += amount
		fmt.Fprintf(&b, "%s,%s,%s,%s\r\n", pkg, sageAmount(amount), sageQuoted(sageCut(sageText(r.Memo), 75)), "0")
	}
	if err := entryTie(); err != nil {
		return "", err
	}
	if totalSum != 0 {
		return "", badReq("gl: the %s export refuses the period: the file's amounts do not sum to zero (%s); no line was written",
			target, dollarsExact(totalSum))
	}
	return b.String(), nil
}

// sageMMDDYY renders date words (YYYY-MM-DD, the shape ExportRows answers) as Sage 50's mm-dd-yy.
// The words are parsed and reformatted, never sliced, so the two digit year's century is decided
// once; a word that does not parse (a planted row's) rides unchanged rather than crashing a render
// over a date the GL could not have written.
func sageMMDDYY(dateWords string) string {
	d, err := time.Parse("2006-01-02", dateWords)
	if err != nil {
		return dateWords
	}
	return d.Format("01-02-06")
}

// sageText makes one text field safe inside Sage 50's straight double quotes. It runs csvsafe's
// formula leader guard first (a leading =, +, -, @, tab or carriage return gets a single quote),
// then replaces a straight double quote with a straight single quote, and a carriage return or a
// line feed with one space each: Sage's plain text importer reads the file line by line and knows
// no quote doubling, so a double quote, CR or LF inside a field would end the field or the record
// early. A comma survives, because the field's own surrounding double quotes carry it.
func sageText(s string) string {
	s = csvsafe.Cell(s)
	s = strings.ReplaceAll(s, `"`, `'`)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// sageQuoted wraps one text field in the straight double quotes Sage 50's format asks for. Every
// text field of the file goes through it: the source and the comment of the transaction's first
// line, and every detail line's comment.
func sageQuoted(s string) string {
	return `"` + s + `"`
}

// sageCut answers the first limit runes of s, never the first limit bytes: Sage 50 caps the source
// at twenty characters and a comment at 75, and a rune cut cannot split an accented letter in half
// the way a byte cut would.
func sageCut(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}

// sageAccountShape refuses a mapped Sage account that is not four to eight digits, naming the HH
// account and the code. Sage's page says the detail line's account number is four characters;
// Sage 50's Canadian editions allow longer account numbers by setting, so the rule this body
// holds is four to eight digits: anything else (letters, punctuation, too short, too long) is not
// an account number a Sage 50 company file can post to, and the import would land the line
// nowhere.
func sageAccountShape(pkg, hhCode, hhName, target string) error {
	if n := len(pkg); n >= 4 && n <= 8 {
		digits := true
		for i := 0; i < n; i++ {
			if pkg[i] < '0' || pkg[i] > '9' {
				digits = false
				break
			}
		}
		if digits {
			return nil
		}
	}
	return badReq("gl: the %s export refuses the period: HH account %s %s maps to %q, and a Sage 50 account number is four to eight digits; set the mapping before exporting",
		target, hhCode, hhName, pkg)
}

// sageAmount renders one detail line's amount: signed dollars with two decimals out of the integer
// cents, debits positive and credits negative, never a float. The magnitude rides a uint64 (the
// same trick dollarsExact uses), so the smallest int64 renders exactly instead of overflowing back
// to itself on the negation.
func sageAmount(cents int64) string {
	mag := uint64(cents)
	if cents < 0 {
		mag = uint64(-(cents + 1)) + 1
	}
	s := fmt.Sprintf("%d.%02d", mag/100, mag%100)
	if cents < 0 {
		return "-" + s
	}
	return s
}
