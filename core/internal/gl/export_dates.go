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

// journalEntryDate dates a row inside its own journal's period on the base memo the caller hands
// it, folding the clamp note onto the memo's tail. The base is the caller's choice: the QBO body
// hands the row's own memo, the Sage 50 body the journal's, so one clamp serves both bodies and
// neither can drift (PR 742's P1-1, whose delta read carried the rule here).
func journalEntryDate(r ExportRow, base string) (date, memo string) {
	date, _, note := journalEntryDateParts(r, base)
	if note == "" {
		return date, base
	}
	if base == "" {
		return date, note
	}
	return date, base + " (" + note + ")"
}

// journalEntryDateParts answers the clamp note APART from the memo, so a body whose importer caps
// the comment (Sage 50 at 75 runes) can keep the whole note and cut the memo to the room the note
// leaves instead of cutting the note off (the AX-3 read's P3-1: the note is the only place the
// bookkeeper sees why the file's date differs from the document's).
//
// The posted house day dates the entry when it falls in the period; a day before the period (an
// automated post rolled forward out of a closed month) goes out on the period's first day, and a
// day after it (a parked draft approved later) on the period's last day. The note names which side
// was clamped and carries the year (PR 742's delta read, P3-1): the posted day of a late approval
// is the approval instant (ApproveJournal stamps now()), so it says "approved 2 June 2020, posted
// to May 2020"; the posted day of a roll forward is the document's own day, so it says "dated 28
// June 2096, posted to July 2096". A row with no period (a planted row in a unit leg) is not
// clamped. The date comes back as YYYY-MM-DD words; each body renders it in its importer's own
// shape.
func journalEntryDateParts(r ExportRow, base string) (date, memo, note string) {
	memo = base
	if r.PeriodMonth < 1 || r.PeriodMonth > 12 {
		return r.PostedAt, memo, ""
	}
	// The period's days are calendar days, not instants: parsed from the date words, so no clock
	// zone decides them.
	first, ferr := time.Parse("2006-01-02", fmt.Sprintf("%04d-%02d-01", r.PeriodYear, r.PeriodMonth))
	posted, err := time.Parse("2006-01-02", r.PostedAt)
	if ferr != nil || err != nil {
		return r.PostedAt, memo, ""
	}
	last := first.AddDate(0, 1, -1)
	if !posted.Before(first) && !posted.After(last) {
		return r.PostedAt, memo, ""
	}
	date = first.Format("2006-01-02")
	word := "dated"
	if posted.After(last) {
		date = last.Format("2006-01-02")
		word = "approved"
	}
	note = fmt.Sprintf("%s %d %s %d, posted to %s %d", word, posted.Day(), posted.Month(), posted.Year(), first.Month(), first.Year())
	return date, memo, note
}
