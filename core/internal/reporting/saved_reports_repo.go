package reporting

// saved_reports_repo.go, the saved report store (0474, table report_saved_reports, owned here). Every
// statement is fixed text with bound parameters, and every read, edit and delete of a single report is
// keyed by the owner's user id, so a report that is not the caller's is indistinguishable from one that
// does not exist.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const savedReportColumns = `id::text, owner_user_id::text, name, dataset, columns, filters, group_by, sort, as_of, created_at, updated_at`

// savedReportKeyed reports an id that cannot be a uuid, which is not found, not a database error.
func savedReportID(id string) (string, bool) {
	if len(id) != 36 {
		return "", false
	}
	for i, r := range id {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return "", false
			}
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return "", false
		}
	}
	return strings.ToLower(id), true
}

type savedReportJSON struct{ columns, filters, groupBy, sort []byte }

func marshalSavedReport(def SavedReportDefinition) (savedReportJSON, error) {
	var out savedReportJSON
	var err error
	nonNil := func(v any, isNil bool) any {
		if isNil {
			return []any{}
		}
		return v
	}
	if out.columns, err = json.Marshal(nonNil(def.Columns, def.Columns == nil)); err != nil {
		return out, fmt.Errorf("marshal columns: %w", err)
	}
	if out.filters, err = json.Marshal(nonNil(def.Filters, def.Filters == nil)); err != nil {
		return out, fmt.Errorf("marshal filters: %w", err)
	}
	if out.groupBy, err = json.Marshal(nonNil(def.GroupBy, def.GroupBy == nil)); err != nil {
		return out, fmt.Errorf("marshal group by: %w", err)
	}
	if out.sort, err = json.Marshal(nonNil(def.Sort, def.Sort == nil)); err != nil {
		return out, fmt.Errorf("marshal sort: %w", err)
	}
	return out, nil
}

func scanSavedReport(row pgx.Row) (SavedReport, error) {
	var r SavedReport
	var cols, filters, groupBy, sortKeys []byte
	if err := row.Scan(&r.ID, &r.OwnerUserID, &r.Name, &r.Dataset, &cols, &filters, &groupBy, &sortKeys,
		&r.AsOf, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return SavedReport{}, err
	}
	if err := json.Unmarshal(cols, &r.Columns); err != nil {
		return SavedReport{}, fmt.Errorf("decode columns: %w", err)
	}
	// Numbers come back as json.Number, the shape the engine's coercion expects, so a stored whole
	// number is never read through a float.
	dec := json.NewDecoder(bytes.NewReader(filters))
	dec.UseNumber()
	if err := dec.Decode(&r.Filters); err != nil {
		return SavedReport{}, fmt.Errorf("decode filters: %w", err)
	}
	if err := json.Unmarshal(groupBy, &r.GroupBy); err != nil {
		return SavedReport{}, fmt.Errorf("decode group by: %w", err)
	}
	if err := json.Unmarshal(sortKeys, &r.Sort); err != nil {
		return SavedReport{}, fmt.Errorf("decode sort: %w", err)
	}
	return r, nil
}

func errSavedReportName(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return errBadRequest("you already have a saved report with that name: pick another, or edit the one you have")
	}
	return nil
}

// InsertSavedReport stores a new definition for the owner.
func (r *Repository) InsertSavedReport(ctx context.Context, owner string, def SavedReportDefinition) (SavedReport, error) {
	if r.pool == nil {
		return SavedReport{}, ErrNoDatabase
	}
	j, err := marshalSavedReport(def)
	if err != nil {
		return SavedReport{}, err
	}
	out, err := scanSavedReport(r.pool.QueryRow(ctx, `
INSERT INTO report_saved_reports (owner_user_id, name, dataset, columns, filters, group_by, sort, as_of)
VALUES ($1::uuid, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb, $8)
RETURNING `+savedReportColumns, owner, def.Name, def.Dataset, string(j.columns), string(j.filters),
		string(j.groupBy), string(j.sort), def.AsOf))
	if err != nil {
		if bad := errSavedReportName(err); bad != nil {
			return SavedReport{}, bad
		}
		return SavedReport{}, fmt.Errorf("save report: %w", err)
	}
	return out, nil
}

// UpdateSavedReport replaces the owner's definition, not found for anyone else's.
func (r *Repository) UpdateSavedReport(ctx context.Context, id, owner string, def SavedReportDefinition) (SavedReport, error) {
	if r.pool == nil {
		return SavedReport{}, ErrNoDatabase
	}
	id, ok := savedReportID(id)
	if !ok {
		return SavedReport{}, ErrNotFound
	}
	j, err := marshalSavedReport(def)
	if err != nil {
		return SavedReport{}, err
	}
	out, err := scanSavedReport(r.pool.QueryRow(ctx, `
UPDATE report_saved_reports
   SET name = $3, dataset = $4, columns = $5::jsonb, filters = $6::jsonb, group_by = $7::jsonb,
       sort = $8::jsonb, as_of = $9, updated_at = now()
 WHERE id = $1::uuid AND owner_user_id = $2::uuid
RETURNING `+savedReportColumns, id, owner, def.Name, def.Dataset, string(j.columns), string(j.filters),
		string(j.groupBy), string(j.sort), def.AsOf))
	if errors.Is(err, pgx.ErrNoRows) {
		return SavedReport{}, ErrNotFound
	}
	if err != nil {
		if bad := errSavedReportName(err); bad != nil {
			return SavedReport{}, bad
		}
		return SavedReport{}, fmt.Errorf("update report: %w", err)
	}
	return out, nil
}

// GetSavedReport reads the owner's report, not found for anyone else's.
func (r *Repository) GetSavedReport(ctx context.Context, id, owner string) (SavedReport, error) {
	if r.pool == nil {
		return SavedReport{}, ErrNoDatabase
	}
	id, ok := savedReportID(id)
	if !ok {
		return SavedReport{}, ErrNotFound
	}
	out, err := scanSavedReport(r.pool.QueryRow(ctx,
		`SELECT `+savedReportColumns+` FROM report_saved_reports WHERE id = $1::uuid AND owner_user_id = $2::uuid`,
		id, owner))
	if errors.Is(err, pgx.ErrNoRows) {
		return SavedReport{}, ErrNotFound
	}
	if err != nil {
		return SavedReport{}, fmt.Errorf("read report: %w", err)
	}
	return out, nil
}

// ListSavedReports lists the owner's reports by name.
func (r *Repository) ListSavedReports(ctx context.Context, owner string) ([]SavedReport, error) {
	if r.pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+savedReportColumns+` FROM report_saved_reports WHERE owner_user_id = $1::uuid ORDER BY lower(name), id`,
		owner)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()
	out := []SavedReport{}
	for rows.Next() {
		rep, err := scanSavedReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

// DeleteSavedReport removes the owner's report, not found for anyone else's.
func (r *Repository) DeleteSavedReport(ctx context.Context, id, owner string) error {
	if r.pool == nil {
		return ErrNoDatabase
	}
	id, ok := savedReportID(id)
	if !ok {
		return ErrNotFound
	}
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM report_saved_reports WHERE id = $1::uuid AND owner_user_id = $2::uuid`, id, owner)
	if err != nil {
		return fmt.Errorf("delete report: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
