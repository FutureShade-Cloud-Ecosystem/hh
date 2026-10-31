package gl

// export_qbo.go, W3 AX-2 item 1: the QuickBooks Online target's journal entry import format. QBO
// takes journal entries from a CSV of its own ten columns, one journal entry per JournalNo, so the
// period's posted lines go out as the GL's own journals: every line of one GL journal is one QBO
// journal entry, dated the house day the journal posted, and every account speaks the package's
// code through AX-1's mapping for this target. Money is the integer cents of the books rendered as
// dollars with two decimals, never a float, the same as every other export door.
//
// The file is the import file itself: the header row is QBO's column vocabulary and nothing else
// rides above it, because a banner or a totals line inside the CSV is a row the importer would
// read as an entry. The draft word lives where the importer never looks: ExportResult.IsDraft and
// the batch row the service records.
//
// The tie is asserted, not assumed. A QBO journal entry must balance, so an entry whose two sides
// differ, or a file whose totals differ, refuses in words naming the entry, before any row is
// written: the caller records its batch only after this answers, so a refused period leaves
// nothing behind (S7's P3-4 on 719, where the totals line was printed but never asserted).

import (
	"fmt"
	"strings"
)

// qboJournalHeader is QBO's journal entry import vocabulary, verbatim and in its order: the ten
// columns its importer reads. Name, Location, Class and Currency stay empty cells: the GL carries
// no QBO name, location or class, and the books are one currency, which an empty Currency cell
// lets QBO read as its home currency, the books' own.
const qboJournalHeader = "JournalNo,JournalDate,Account,Debits,Credits,Description,Name,Location,Class,Currency"

// renderQBOJournals renders the period's posted lines as QBO journal entry import rows. The rows
// arrive in entry order (ExportRows orders by entry_no), and the lines of one entry share its
// posted day, so the entry's first row dates the entry. Every account must come through the
// mapping: a row whose account has no mapped package code refuses the whole file in words, the
// same rule the unmapped-movement read refuses by, so the two reads cannot drift apart and hand
// the file a hole.
func renderQBOJournals(rows []ExportRow, packageAccount map[string]string) (string, error) {
	target := exportTargetWords(TargetQuickBooksOnline)
	var b strings.Builder
	b.WriteString(qboJournalHeader)
	b.WriteByte('\n')
	entryNo := 0
	var entryDebit, entryCredit int64
	var totalDebit, totalCredit int64
	// entryTie is the entry's own assertion, run on the LAST row of every entry (and once more
	// after the final row): a QBO journal entry balances or the file refuses to exist.
	entryTie := func() error {
		if entryNo == 0 {
			return nil
		}
		if entryDebit != entryCredit {
			return badReq("gl: the %s export refuses the period: journal entry %d does not balance (debits %s, credits %s); no row was written",
				target, entryNo, dollarsExact(entryDebit), dollarsExact(entryCredit))
		}
		return nil
	}
	for _, r := range rows {
		if r.EntryNo != entryNo {
			if err := entryTie(); err != nil {
				return "", err
			}
			entryNo = r.EntryNo
			entryDebit, entryCredit = 0, 0
		}
		pkg := packageAccount[r.AccountCode]
		if pkg == "" {
			return "", badReq("gl: the %s export refuses the period: %s %s has movement and no mapping for the target; set the mapping before exporting",
				target, r.AccountCode, r.AccountName)
		}
		fmt.Fprintf(&b, "%d,%s,%s,%s,%s,%s,,,,\n",
			r.EntryNo, r.PostedAt, csvEscape(pkg), dollars(r.DebitCents), dollars(r.CreditCents), csvEscape(r.Memo))
		entryDebit += r.DebitCents
		entryCredit += r.CreditCents
		totalDebit += r.DebitCents
		totalCredit += r.CreditCents
	}
	if err := entryTie(); err != nil {
		return "", err
	}
	if totalDebit != totalCredit {
		return "", badReq("gl: the %s export refuses the period: the file's debits (%s) do not equal its credits (%s); no row was written",
			target, dollarsExact(totalDebit), dollarsExact(totalCredit))
	}
	return b.String(), nil
}

// dollarsExact is dollars for words: zero renders as 0.00 and a negative keeps its sign, because a
// refusal that names an amount must name it, not leave an empty cell where a number belongs.
func dollarsExact(cents int64) string {
	if cents < 0 {
		return "-" + dollarsExact(-cents)
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
