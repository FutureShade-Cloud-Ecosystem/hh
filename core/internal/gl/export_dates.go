package gl

// export_dates.go, W3 AX-3 item 2: the one date clamp both import bodies share. QBO books an
// entry by its JournalDate and Sage 50 books a transaction by its first line's date, and the GL
// books the entry by its period, so both bodies date an entry inside its own journal's period
// through this helper and neither can drift back to the raw posted day (PR 742's P1-1, whose
// delta read carried the rule to this body).

import (
	"fmt"
	"time"
)

// journalEntryDate dates a row inside its own journal's period. The posted house day dates the
// entry when it falls in the period; a day before the period (an automated post rolled forward
// out of a closed month) goes out on the period's first day, and a day after it (a parked draft
// approved later) on the period's last day. A clamped entry's memo then carries a note in words
// naming which side was clamped and carrying the year (PR 742's delta read, P3-1): the posted
// day of a late approval is the approval instant (ApproveJournal stamps now()), so it says
// "approved 2 June 2020, posted to May 2020"; the posted day of a roll forward is the document's
// own day, so it says "dated 28 June 2096, posted to July 2096". A row with no period (a planted
// row in a unit leg) is not clamped. The date comes back as YYYY-MM-DD words; each body renders
// it in its importer's own shape.
func journalEntryDate(r ExportRow) (date, memo string) {
	memo = r.Memo
	if r.PeriodMonth < 1 || r.PeriodMonth > 12 {
		return r.PostedAt, memo
	}
	// The period's days are calendar days, not instants: parsed from the date words, so no clock
	// zone decides them.
	first, ferr := time.Parse("2006-01-02", fmt.Sprintf("%04d-%02d-01", r.PeriodYear, r.PeriodMonth))
	posted, err := time.Parse("2006-01-02", r.PostedAt)
	if ferr != nil || err != nil {
		return r.PostedAt, memo
	}
	last := first.AddDate(0, 1, -1)
	if !posted.Before(first) && !posted.After(last) {
		return r.PostedAt, memo
	}
	date = first.Format("2006-01-02")
	word := "dated"
	if posted.After(last) {
		date = last.Format("2006-01-02")
		word = "approved"
	}
	note := fmt.Sprintf("%s %d %s %d, posted to %s %d", word, posted.Day(), posted.Month(), posted.Year(), first.Month(), first.Year())
	if memo == "" {
		return date, note
	}
	return date, memo + " (" + note + ")"
}
