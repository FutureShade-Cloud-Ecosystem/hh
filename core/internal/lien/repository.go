package lien

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// repository.go, the lien pgx access layer over the 0038 lien_jobs / lien_participants / lien_documents /
// holdback_ledger tables. The holdback DIRECTION + the HST-on-holdback config flag ride lien_jobs.metadata
// (0038 has no direction/flag column, recorded as an integrator deviation), so a job is fully config-driven
// with no schema change.

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullUUID passes a canonical UUID through, or NULL when blank/non-UUID. created_by/approver ids are FK'd to
// users(id); a non-UUID actor label (e.g. a test caller) is recorded as NULL rather than a write error.
func nullUUID(s string) any {
	if !isUUID(s) {
		return nil
	}
	return s
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// nullDate passes an ISO date through, or NULL when blank/invalid (a bad date is treated as absent, never a
// write error, M6 is DRAFT and operator-entered).
func nullDate(s string) any {
	if s == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return nil
	}
	return s
}

// nullTime passes a timestamp through, or NULL when zero. The only caller is InsertHoldbackEntry, where
// NULL means "let the column's now() default stamp it", which is what every production caller wants and
// what every caller did before this parameter existed. A test (or a back-dated Bill 60 import) that needs
// the row to carry a specific created_at supplies one, and the COALESCE takes it instead. DIB-455 / the
// DIB-427 P1 park.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func dateStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// jobMeta is the metadata JSONB shape (direction + HST flag).
type jobMeta struct {
	Direction     string `json:"holdback_direction,omitempty"`
	HSTOnHoldback bool   `json:"hst_on_holdback,omitempty"`
}

func (r *Repository) InsertJob(ctx context.Context, in JobInput, createdBy string) (Job, error) {
	if r.pool == nil {
		return Job{}, errNoDB
	}
	if in.HoldbackPctBps == 0 {
		in.HoldbackPctBps = 1000 // 10% statutory (placeholder, mirrors the 0038 default)
	}
	meta, _ := json.Marshal(jobMeta{Direction: in.Direction, HSTOnHoldback: in.HSTOnHoldback})
	var id string
	var created time.Time
	err := r.pool.QueryRow(ctx,
		`INSERT INTO lien_jobs
		   (job_no, customer_id, branch_id, description, contract_cents, holdback_pct_bps,
		    substantial_perf_date, last_supply_date, csp_published_on, metadata, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		 RETURNING id::text, created_at`,
		in.JobNo, nullStr(in.CustomerID), in.BranchID, in.Description, in.ContractCents, in.HoldbackPctBps,
		nullDate(in.SubstantialPerfDate), nullDate(in.LastSupplyDate), nullDate(in.CSPPublishedOn), meta, nullUUID(createdBy),
	).Scan(&id, &created)
	if err != nil {
		return Job{}, fmt.Errorf("lien: insert job: %w", err)
	}
	return s2Job(id, in, created), nil
}

func s2Job(id string, in JobInput, created time.Time) Job {
	dir := in.Direction
	if dir == "" {
		dir = dirReceivable
	}
	return Job{
		ID: id, JobNo: in.JobNo, CustomerID: in.CustomerID, BranchID: in.BranchID,
		Description: in.Description, ContractCents: in.ContractCents, HoldbackPctBps: in.HoldbackPctBps,
		SubstantialPerfDate: in.SubstantialPerfDate, LastSupplyDate: in.LastSupplyDate, CSPPublishedOn: in.CSPPublishedOn, Status: "OPEN",
		Direction: dir, HSTOnHoldback: in.HSTOnHoldback, CreatedAt: created.Format(time.RFC3339),
	}
}

const jobCols = `id::text, job_no::text, COALESCE(customer_id::text,''), COALESCE(customer_job_id::text,''), branch_id::text, description,
	contract_cents, holdback_pct_bps, substantial_perf_date, last_supply_date, csp_published_on, lien_preserved_on, lien_preserved_note, lien_discharged_on, status, metadata, created_at`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	var perf, lastSupply, csp, preserved, discharged *time.Time
	var meta []byte
	var created time.Time
	if err := row.Scan(&j.ID, &j.JobNo, &j.CustomerID, &j.CustomerJobID, &j.BranchID, &j.Description, &j.ContractCents,
		&j.HoldbackPctBps, &perf, &lastSupply, &csp, &preserved, &j.LienPreservedNote, &discharged, &j.Status, &meta, &created); err != nil {
		return Job{}, err
	}
	j.SubstantialPerfDate = dateStr(perf)
	j.LastSupplyDate = dateStr(lastSupply)
	j.CSPPublishedOn, j.LienPreservedOn, j.LienDischargedOn = dateStr(csp), dateStr(preserved), dateStr(discharged)
	j.CreatedAt = created.Format(time.RFC3339)
	var m jobMeta
	if len(meta) > 0 {
		_ = json.Unmarshal(meta, &m)
	}
	j.Direction = m.Direction
	if j.Direction == "" {
		j.Direction = dirReceivable
	}
	j.HSTOnHoldback = m.HSTOnHoldback
	return j, nil
}

func (r *Repository) GetJob(ctx context.Context, id string) (Job, error) {
	if r.pool == nil {
		return Job{}, errNoDB
	}
	// A malformed id is no job: 404 in words, not a 500 from Postgres. This is the shared lookup, so
	// every lien job route that reads by id answers the same.
	if _, err := uuid.Parse(id); err != nil {
		return Job{}, errJobNotFound
	}
	j, err := scanJob(r.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM lien_jobs WHERE id = $1`, id))
	if err != nil {
		if err == pgx.ErrNoRows {
			return Job{}, errJobNotFound
		}
		return Job{}, fmt.Errorf("lien: get job %q: %w", id, err)
	}
	return j, nil
}

func (r *Repository) ListJobs(ctx context.Context, branchID string) ([]Job, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+jobCols+` FROM lien_jobs WHERE ($1 = '' OR branch_id = $1::uuid) ORDER BY created_at DESC`, branchID)
	if err != nil {
		return nil, fmt.Errorf("lien: list jobs: %w", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("lien: scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *Repository) RetentionInvoiceIDs(ctx context.Context, jobID string) (map[string]bool, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT invoice_id::text FROM holdback_ledger
		 WHERE job_id = $1 AND entry_type = 'RETENTION' AND invoice_id IS NOT NULL`, jobID)
	if err != nil {
		return nil, fmt.Errorf("lien: retention invoice ids: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// HeldInvoiceIDs is the invoice ids with a positive RETENTION on any lien job's ledger.
func (r *Repository) HeldInvoiceIDs(ctx context.Context) (map[string]bool, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT invoice_id::text FROM holdback_ledger
		 WHERE entry_type = 'RETENTION' AND amount_cents > 0 AND invoice_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("lien: held invoice ids: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (r *Repository) InsertHoldbackEntry(ctx context.Context, e HoldbackEntry) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO holdback_ledger
		   (job_id, invoice_id, branch_id, entry_type, amount_cents, gl_journal_id, released_at, reason, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8, COALESCE($9, now()))
		 RETURNING id::text`,
		e.JobID, nullStr(e.InvoiceID), e.BranchID, e.EntryType, e.AmountCents,
		nullStr(e.GLJournalID), nullDate(e.ReleasedAt), e.Reason, nullTime(e.CreatedAt),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("lien: insert holdback %s: %w", e.EntryType, err)
	}
	return id, nil
}

const ledgerCols = `id::text, job_id::text, COALESCE(invoice_id::text,''), branch_id::text, entry_type,
	amount_cents, COALESCE(gl_journal_id::text,''), released_at, reason, created_at`

func scanEntry(row pgx.Row) (HoldbackEntry, error) {
	var e HoldbackEntry
	var released *time.Time
	if err := row.Scan(&e.ID, &e.JobID, &e.InvoiceID, &e.BranchID, &e.EntryType, &e.AmountCents,
		&e.GLJournalID, &released, &e.Reason, &e.CreatedAt); err != nil {
		return HoldbackEntry{}, err
	}
	e.ReleasedAt = dateStr(released)
	return e, nil
}

func (r *Repository) HoldbackEntriesForJob(ctx context.Context, jobID string) ([]HoldbackEntry, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+ledgerCols+` FROM holdback_ledger WHERE job_id = $1 ORDER BY created_at`, jobID)
	if err != nil {
		return nil, fmt.Errorf("lien: holdback entries: %w", err)
	}
	defer rows.Close()
	return collectEntries(rows)
}

func (r *Repository) HoldbackLedger(ctx context.Context, branchID string) ([]HoldbackEntry, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+ledgerCols+` FROM holdback_ledger WHERE ($1 = '' OR branch_id = $1::uuid) ORDER BY created_at`, branchID)
	if err != nil {
		return nil, fmt.Errorf("lien: holdback ledger: %w", err)
	}
	defer rows.Close()
	return collectEntries(rows)
}

func collectEntries(rows pgx.Rows) ([]HoldbackEntry, error) {
	var out []HoldbackEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("lien: scan holdback: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) NextDocumentVersion(ctx context.Context, jobID, templateKind string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var next int
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version),0)+1 FROM lien_documents WHERE job_id = $1 AND template_kind = $2`,
		jobID, templateKind).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("lien: next document version: %w", err)
	}
	return next, nil
}

func (r *Repository) InsertDocument(ctx context.Context, d Document, createdBy string) (Document, error) {
	if r.pool == nil {
		return Document{}, errNoDB
	}
	data, _ := json.Marshal(d.Data)
	var id string
	var created time.Time
	err := r.pool.QueryRow(ctx,
		`INSERT INTO lien_documents
		   (job_id, template_kind, status, version, rendered_content, data, is_draft_banner, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id::text, created_at`,
		d.JobID, d.TemplateKind, d.Status, d.Version, d.RenderedContent, data, d.IsDraftBanner, nullUUID(createdBy),
	).Scan(&id, &created)
	if err != nil {
		return Document{}, fmt.Errorf("lien: insert document: %w", err)
	}
	d.ID = id
	d.CreatedAt = created.Format(time.RFC3339)
	return d, nil
}

const docCols = `id::text, job_id::text, template_kind, status, version, rendered_content, data, is_draft_banner, created_at`

func scanDoc(row pgx.Row) (Document, error) {
	var d Document
	var data []byte
	var created time.Time
	if err := row.Scan(&d.ID, &d.JobID, &d.TemplateKind, &d.Status, &d.Version, &d.RenderedContent,
		&data, &d.IsDraftBanner, &created); err != nil {
		return Document{}, err
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &d.Data)
	}
	d.CreatedAt = created.Format(time.RFC3339)
	return d, nil
}

// DocumentsForJob returns the LATEST version of each (template_kind) for a job.
func (r *Repository) DocumentsForJob(ctx context.Context, jobID string) ([]Document, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT ON (template_kind) `+docCols+`
		 FROM lien_documents WHERE job_id = $1
		 ORDER BY template_kind, version DESC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("lien: documents for job: %w", err)
	}
	defer rows.Close()
	return collectDocs(rows)
}

// DocumentVersions returns the full version history of the (job, template_kind) the given document belongs to,
// newest first.
func (r *Repository) DocumentVersions(ctx context.Context, documentID string) ([]Document, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+docCols+` FROM lien_documents
		 WHERE (job_id, template_kind) = (SELECT job_id, template_kind FROM lien_documents WHERE id = $1)
		 ORDER BY version DESC`, documentID)
	if err != nil {
		return nil, fmt.Errorf("lien: document versions: %w", err)
	}
	defer rows.Close()
	docs, err := collectDocs(rows)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, errDocNotFound
	}
	return docs, nil
}

func collectDocs(rows pgx.Rows) ([]Document, error) {
	var out []Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, fmt.Errorf("lien: scan document: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
