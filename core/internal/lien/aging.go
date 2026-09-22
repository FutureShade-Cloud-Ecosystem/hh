package lien

import (
	"sort"
	"time"
)

// aging.go, PURE Bill 60 holdback aging + Monday-triage classification. Kept DB-free (input = ledger rows +
// job facts + asOf) so the buckets/badges are exhaustively unit-testable without a database.

// releaseRuleDays is the Bill 60 statutory release window (45 days from substantial performance). DOCUMENTED
// PLACEHOLDER (D-021), the compliance calendar carries the authoritative per-deadline rule_days row.
const releaseRuleDays = 45

// ageBucket classifies a holdback age (days) into the Bill 60 aging buckets.
func ageBucket(days int) string {
	switch {
	case days < 0:
		return "0-30"
	case days <= 30:
		return "0-30"
	case days <= 60:
		return "31-60"
	case days <= 90:
		return "61-90"
	default:
		return "90+"
	}
}

// jobFacts is the minimal per-job projection the aging roll-up needs (avoids re-fetching whole Jobs).
type jobFacts struct {
	JobNo     string
	BranchID  string
	Direction string
}

// buildAging rolls the ledger up per job into outstanding-holdback aging rows. Outstanding = ΣRETENTION −
// ΣRELEASE; a job that nets to zero (fully released) is dropped. oldestDays is measured from the oldest
// unreleased RETENTION's created_at to asOf. A RECEIVABLE past the 45-day window is release-eligible.
func buildAging(entries []HoldbackEntry, facts map[string]jobFacts, asOf time.Time) []AgingRow {
	type acc struct {
		outstanding int64
		oldest      time.Time
		hasRet      bool
	}
	byJob := map[string]*acc{}
	for _, e := range entries {
		a := byJob[e.JobID]
		if a == nil {
			a = &acc{}
			byJob[e.JobID] = a
		}
		switch e.EntryType {
		case "RETENTION":
			a.outstanding += e.AmountCents
			if !a.hasRet || e.CreatedAt.Before(a.oldest) {
				a.oldest = e.CreatedAt
				a.hasRet = true
			}
		case "RELEASE":
			a.outstanding -= e.AmountCents
		}
	}
	var out []AgingRow
	for jobID, a := range byJob {
		if a.outstanding <= 0 {
			continue
		}
		days := 0
		if a.hasRet {
			days = daysBetween(a.oldest, asOf)
		}
		f := facts[jobID]
		row := AgingRow{
			JobID:            jobID,
			JobNo:            f.JobNo,
			BranchID:         f.BranchID,
			Direction:        f.Direction,
			OutstandingCents: a.outstanding,
			OldestDays:       days,
			Bucket:           ageBucket(days),
			ReleaseEligible:  f.Direction == dirReceivable && days >= releaseRuleDays,
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OldestDays != out[j].OldestDays {
			return out[i].OldestDays > out[j].OldestDays // oldest first
		}
		return out[i].JobNo < out[j].JobNo
	})
	return out
}

// buildTriage badge-classifies the aging rows into the Monday worklist. Badges (no scheduler infra at W6 -
// these are worklist BADGES computed on read, the recorded PARTIAL for reminders):
//   - PAYABLE_MANUAL: a payable retention (recordable, release is manual/refused)
//   - RELEASE_DUE:    a receivable at/over the 45-day window (ready to release)
//   - OVERDUE:        a receivable well past the window (> 2× rule days)
//   - AGING:          outstanding but not yet at the window
func buildTriage(rows []AgingRow) []TriageItem {
	out := make([]TriageItem, 0, len(rows))
	for _, r := range rows {
		item := TriageItem{
			JobID:            r.JobID,
			JobNo:            r.JobNo,
			BranchID:         r.BranchID,
			Direction:        r.Direction,
			OutstandingCents: r.OutstandingCents,
			OldestDays:       r.OldestDays,
		}
		switch {
		case r.Direction == dirPayable:
			item.Badge = "PAYABLE_MANUAL"
			item.Note = "payable retention, release is manual (registers a deadline, does not post)"
		case r.OldestDays > 2*releaseRuleDays:
			item.Badge = "OVERDUE"
			item.Note = "past the release window, action overdue"
		case r.OldestDays >= releaseRuleDays:
			item.Badge = "RELEASE_DUE"
			item.Note = "at/over the 45-day window, eligible to release"
		default:
			item.Badge = "AGING"
			item.Note = "retention held, not yet at the release window"
		}
		out = append(out, item)
	}
	return out
}

// daysBetween returns whole days from t to asOf (calendar-day granularity, never negative-rounding surprises).
func daysBetween(t, asOf time.Time) int {
	d := asOf.Sub(t).Hours() / 24
	if d < 0 {
		return 0
	}
	return int(d)
}
