package compliance

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// complianceStore is the persistence seam (mirrors the lien lienStore pattern): the service holds an interface
// so unit tests inject a DB-free fake and the integration suite injects the real pgx *Repository. Every method
// reads/writes ONLY compliance-owned tables (compliance_calendar, review_status).
type complianceStore interface {
	// UpsertDeadline records a deadline idempotently on (job_id, deadline_kind, due_date); inserted=false on a
	// duplicate.
	UpsertDeadline(ctx context.Context, jobID, kind, dueDate string, ruleDays int) (inserted bool, err error)
	OpenDeadlines(ctx context.Context) ([]Deadline, error)
	ListDeadlines(ctx context.Context, status string) ([]Deadline, error)
	GetReviewStatus(ctx context.Context, subjectType, subjectID string) (ReviewStatus, bool, error)
	UpsertReviewStatus(ctx context.Context, in ReviewStatusInput, reviewedBy string) (ReviewStatus, error)
}

var _ complianceStore = (*Repository)(nil)

// withStore / withClock are the unit-test seams.
func (s *Service) withStore(st complianceStore) *Service   { s.store = st; return s }
func (s *Service) withClock(now func() time.Time) *Service { s.now = now; return s }

func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// -- pure helpers --------------------------------------------------------------

// filterUpcoming keeps open deadlines due on/before asOf+horizon, stamps OVERDUE on any already past due, and
// orders soonest-first. Kept pure so the badge worklist is DB-free unit-testable.
func filterUpcoming(items []Deadline, asOf time.Time, horizonDays int) []Deadline {
	today := asOf.Truncate(24 * time.Hour)
	cutoff := today.AddDate(0, 0, horizonDays)
	var out []Deadline
	for _, d := range items {
		due, err := time.Parse("2006-01-02", d.DueDate)
		if err != nil {
			continue
		}
		if due.After(cutoff) {
			continue
		}
		if due.Before(today) {
			d.Status = "OVERDUE"
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueDate < out[j].DueDate })
	return out
}

// clientError carries an HTTP status (mirrors the lien taxonomy).
type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }
func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

var errNoDB = clientError{status: http.StatusServiceUnavailable, msg: "compliance: database not configured"}
