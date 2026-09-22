package assist

// dates.go, DIB-478. "and last week?" turned into two dates, once, in one place.
//
// WHY THIS IS ITS OWN FILE AND NOT A CLOSURE IN THE RESOLVER. Three things need the same answer to
// "what does last week mean": the resolver (to shift a range), the commands (to default a range
// nobody gave), and the tests (to assert without re-implementing). Two copies of a week boundary is
// how a report and a chat answer end up disagreeing about a Monday.
//
// THE WEEK STARTS ON MONDAY, and that is a decision rather than a default. Go's time.Weekday puts
// Sunday first; the yard's week, the one on the whiteboard, the one delivery schedules are drawn
// against, the one "last week's takings" means to the person asking, runs Monday to Sunday. It is
// pinned by a test, so if it is ever wrong it is wrong on purpose.
//
// EVERY BOUNDARY IS INCLUSIVE AND EVERY DATE IS A PLAIN DAY. The reads behind these ranges are
// date-typed and day-granular; carrying an instant would imply an accuracy about the hour that none
// of them has, and the DIB-431 rule about not printing a timestamp to the second is the same rule.

import (
	"strings"
	"time"
)

// DateRange is a closed [From, To] window of plain days, plus the words a person used for it.
//
// Label is carried so the answer can say "last week (11-17 Aug)" rather than echoing two dates the
// reader then has to check against their own idea of last week. A range whose label and dates
// disagree is caught by reading one sentence.
type DateRange struct {
	From  string
	To    string
	Label string
}

const dayFmt = "2006-01-02"

// rangeWord is one phrase and the window it names, resolved against a clock.
type rangeWord struct {
	words []string
	of    func(now time.Time) DateRange
}

// rangeWords is checked LONGEST PHRASE FIRST (see parseRangeWords): "last 7 days" must not be read
// as "last" plus a number, and "this month" must not match on "month" alone.
var rangeWords = []rangeWord{
	{[]string{"the day before yesterday"}, func(n time.Time) DateRange { return oneDay(n.AddDate(0, 0, -2), "the day before yesterday") }},
	{[]string{"last 30 days", "past 30 days", "last thirty days"}, func(n time.Time) DateRange { return backDays(n, 29, "the last 30 days") }},
	{[]string{"last 14 days", "past 14 days", "last fortnight"}, func(n time.Time) DateRange { return backDays(n, 13, "the last 14 days") }},
	{[]string{"last 7 days", "past 7 days", "last seven days"}, func(n time.Time) DateRange { return backDays(n, 6, "the last 7 days") }},
	{[]string{"month to date", "this month so far", "so far this month"}, func(n time.Time) DateRange { return monthToDate(n) }},
	{[]string{"last month", "previous month", "the month before"}, func(n time.Time) DateRange { return lastMonth(n) }},
	{[]string{"this month"}, func(n time.Time) DateRange { return monthToDate(n) }},
	{[]string{"last week", "previous week", "the week before"}, func(n time.Time) DateRange { return lastWeek(n) }},
	{[]string{"this week", "week to date"}, func(n time.Time) DateRange { return thisWeek(n) }},
	{[]string{"yesterday"}, func(n time.Time) DateRange { return oneDay(n.AddDate(0, 0, -1), "yesterday") }},
	{[]string{"today", "so far today"}, func(n time.Time) DateRange { return oneDay(n, "today") }},
}

// parseRangeWords reports whether a sentence names a window at all, without needing a clock. Used by
// the follow-up classifier, which has to decide "is this a range shift?" before it decides anything
// else.
func parseRangeWords(s string) (rangeWord, bool) {
	l := strings.ToLower(s)
	for _, rw := range rangeWords {
		for _, w := range rw.words {
			if strings.Contains(l, w) {
				return rw, true
			}
		}
	}
	return rangeWord{}, false
}

// parseRange resolves the window a sentence names, against a clock.
func parseRange(s string, now time.Time) (DateRange, bool) {
	rw, ok := parseRangeWords(s)
	if !ok {
		return DateRange{}, false
	}
	return rw.of(now), true
}

func oneDay(t time.Time, label string) DateRange {
	d := t.Format(dayFmt)
	return DateRange{From: d, To: d, Label: label + " (" + humanDay(t) + ")"}
}

func backDays(now time.Time, n int, label string) DateRange {
	from := now.AddDate(0, 0, -n)
	return DateRange{From: from.Format(dayFmt), To: now.Format(dayFmt), Label: label + " (" + span(from, now) + ")"}
}

// weekStart is the Monday of t's week. Go counts Sunday as 0, so Sunday is the SEVENTH day of the
// week here, not the first.
func weekStart(t time.Time) time.Time {
	off := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -off)
}

func thisWeek(now time.Time) DateRange {
	from := weekStart(now)
	return DateRange{From: from.Format(dayFmt), To: now.Format(dayFmt), Label: "this week (" + span(from, now) + ")"}
}

func lastWeek(now time.Time) DateRange {
	from := weekStart(now).AddDate(0, 0, -7)
	to := from.AddDate(0, 0, 6)
	return DateRange{From: from.Format(dayFmt), To: to.Format(dayFmt), Label: "last week (" + span(from, to) + ")"}
}

func monthToDate(now time.Time) DateRange {
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return DateRange{From: from.Format(dayFmt), To: now.Format(dayFmt), Label: "this month (" + span(from, now) + ")"}
}

func lastMonth(now time.Time) DateRange {
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	from := first.AddDate(0, -1, 0)
	to := first.AddDate(0, 0, -1)
	return DateRange{From: from.Format(dayFmt), To: to.Format(dayFmt), Label: "last month (" + span(from, to) + ")"}
}

func humanDay(t time.Time) string { return t.Format("2 Jan") }

// span renders a window the way a person reads one: "11-17 Aug", collapsing a repeated month, and
// collapsing a single day to itself rather than printing "11 Aug-11 Aug".
func span(from, to time.Time) string {
	if from.Format(dayFmt) == to.Format(dayFmt) {
		return humanDay(from)
	}
	if from.Month() == to.Month() && from.Year() == to.Year() {
		return from.Format("2") + "-" + to.Format("2 Jan")
	}
	return humanDay(from) + "-" + humanDay(to)
}

// parseDay reads a YYYY-MM-DD argument, refusing anything else rather than defaulting.
//
// A date that could not be read must NOT fall back to today: an answer for the wrong day that looks
// like an answer for the right one is the most expensive shape of wrong this package can produce.
func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse(dayFmt, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
