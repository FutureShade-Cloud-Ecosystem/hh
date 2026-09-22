package compliance

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// repository.go, the compliance pgx access layer over the 0038 compliance_calendar + review_status tables.

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// UpsertDeadline records a deadline idempotently on (job_id, deadline_kind, due_date). No unique index exists
// in 0038, so it guards with a NOT EXISTS insert; inserted=false means the same deadline was already present.
func (r *Repository) UpsertDeadline(ctx context.Context, jobID, kind, dueDate string, ruleDays int) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	if _, err := time.Parse("2006-01-02", dueDate); err != nil {
		return false, clientErr(422, "compliance: due_date must be YYYY-MM-DD")
	}
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO compliance_calendar (job_id, deadline_kind, due_date, rule_days)
		 SELECT $1::uuid, $2, $3::date, $4
		 WHERE NOT EXISTS (
		   SELECT 1 FROM compliance_calendar
		   WHERE job_id IS NOT DISTINCT FROM $1::uuid AND deadline_kind = $2 AND due_date = $3::date
		 )`,
		nullStr(jobID), kind, dueDate, ruleDays)
	if err != nil {
		return false, fmt.Errorf("compliance: upsert deadline: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

const calendarCols = `id::text, COALESCE(job_id::text,''), deadline_kind, to_char(due_date,'YYYY-MM-DD'), rule_days, status`

func scanDeadline(row pgx.Row) (Deadline, error) {
	var d Deadline
	if err := row.Scan(&d.ID, &d.JobID, &d.Kind, &d.DueDate, &d.RuleDays, &d.Status); err != nil {
		return Deadline{}, err
	}
	return d, nil
}

func (r *Repository) OpenDeadlines(ctx context.Context) ([]Deadline, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+calendarCols+` FROM compliance_calendar WHERE status = 'OPEN' ORDER BY due_date`)
	if err != nil {
		return nil, fmt.Errorf("compliance: open deadlines: %w", err)
	}
	defer rows.Close()
	return collectDeadlines(rows)
}

func (r *Repository) ListDeadlines(ctx context.Context, status string) ([]Deadline, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+calendarCols+` FROM compliance_calendar WHERE ($1 = '' OR status = $1) ORDER BY due_date`, status)
	if err != nil {
		return nil, fmt.Errorf("compliance: list deadlines: %w", err)
	}
	defer rows.Close()
	return collectDeadlines(rows)
}

func collectDeadlines(rows pgx.Rows) ([]Deadline, error) {
	var out []Deadline
	for rows.Next() {
		d, err := scanDeadline(rows)
		if err != nil {
			return nil, fmt.Errorf("compliance: scan deadline: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

const reviewCols = `subject_type, subject_id::text, state, banner_text, COALESCE(reviewed_by::text,''),
	COALESCE(to_char(reviewed_at,'YYYY-MM-DD"T"HH24:MI:SSOF'),'')`

func (r *Repository) GetReviewStatus(ctx context.Context, subjectType, subjectID string) (ReviewStatus, bool, error) {
	if r.pool == nil {
		return ReviewStatus{}, false, errNoDB
	}
	var rs ReviewStatus
	err := r.pool.QueryRow(ctx,
		`SELECT `+reviewCols+` FROM review_status
		 WHERE ($1 = '' OR subject_type = $1) AND subject_id = $2::uuid
		 ORDER BY updated_at DESC LIMIT 1`, subjectType, subjectID,
	).Scan(&rs.SubjectType, &rs.SubjectID, &rs.State, &rs.BannerText, &rs.ReviewedBy, &rs.ReviewedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ReviewStatus{}, false, nil
		}
		return ReviewStatus{}, false, fmt.Errorf("compliance: get review status: %w", err)
	}
	return rs, true, nil
}

// UpsertReviewStatus writes the shared banner for a subject. There is no unique index in 0038, so it updates
// the newest row for the subject and inserts when none exists.
func (r *Repository) UpsertReviewStatus(ctx context.Context, in ReviewStatusInput, reviewedBy string) (ReviewStatus, error) {
	if r.pool == nil {
		return ReviewStatus{}, errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE review_status SET state = $3, banner_text = $4, reviewed_by = $5,
		    reviewed_at = now(), updated_at = now()
		 WHERE id = (SELECT id FROM review_status WHERE subject_type = $1 AND subject_id = $2::uuid
		             ORDER BY updated_at DESC LIMIT 1)`,
		in.SubjectType, in.SubjectID, in.State, in.BannerText, nullStr(reviewedBy))
	if err != nil {
		return ReviewStatus{}, fmt.Errorf("compliance: update review status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO review_status (subject_type, subject_id, state, banner_text, reviewed_by, reviewed_at)
			 VALUES ($1, $2::uuid, $3, $4, $5, now())`,
			in.SubjectType, in.SubjectID, in.State, in.BannerText, nullStr(reviewedBy)); err != nil {
			return ReviewStatus{}, fmt.Errorf("compliance: insert review status: %w", err)
		}
	}
	rs, _, err := r.GetReviewStatus(ctx, in.SubjectType, in.SubjectID)
	return rs, err
}
