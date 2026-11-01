package billing

// statement_by_job.go, JA-4: the customer's statement grouped by job, and the statement of one job.
//
// The sections are StatementLinesByJob's: one for account level (a finance charge, an NSF fee, an
// adjustment, the unapplied part of a receipt) and one per job, each with its own running balance,
// so a section's last balance is its subtotal and the subtotals together are the closing balance of
// the plain statement. A job statement is the same read narrowed to one job, addressed to the job's
// contact, or to the customer's primary contact when the job has none (and it says so).

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// StatementContact is who a statement is addressed to. Source is "job" (the job's own contact),
// "customer" (the job has none, so the customer's primary contact), or "none".
type StatementContact struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	Phone  string `json:"phone"`
	Source string `json:"source"`
	Note   string `json:"note,omitempty"`
}

// StatementJobSection is one job's (or the account level) part of a statement.
type StatementJobSection struct {
	JobID         string          `json:"job_id"`
	JobNo         string          `json:"job_no"`
	JobName       string          `json:"job_name"`
	AccountLevel  bool            `json:"account_level"`
	Lines         []StatementLine `json:"lines"`
	SubtotalCents int64           `json:"subtotal_cents"`
}

// JobStatement is a customer's statement grouped by job, or one job's statement.
type JobStatement struct {
	CustomerID   string                `json:"customer_id"`
	CustomerName string                `json:"customer_name"`
	AccountNo    string                `json:"account_no"`
	AsOfDate     time.Time             `json:"as_of_date"`
	JobID        string                `json:"job_id,omitempty"`
	Sections     []StatementJobSection `json:"sections"`
	ClosingCents int64                 `json:"closing_balance_cents"`
	Contact      StatementContact      `json:"contact"`
}

// JobStatementParties is the people and names a by job statement needs.
type JobStatementParties struct {
	CustomerName string
	AccountNo    string
	Jobs         map[string]StatementJobSection // id to its number and name (no lines)
	JobBranchID  map[string]string
	JobContact   map[string]StatementContact
	Customer     StatementContact // the customer's primary active contact, Source "customer"
}

type statementByJobStore interface {
	StatementLinesByJob(ctx context.Context, customerID string, asOf time.Time, reach BranchReach) ([]StatementLine, error)
	JobStatementParties(ctx context.Context, customerID string) (JobStatementParties, error)
	// JobDocumentBranches is every branch the job's documents sit at, whatever the caller's reach.
	JobDocumentBranches(ctx context.Context, customerID, jobID string) ([]string, error)
}

// ErrJobBranchesOutOfReach refuses a job statement to a caller who cannot reach every branch the job's
// documents sit at: a figure cut to the caller's branches would read as the job's whole account.
var ErrJobBranchesOutOfReach = errors.New("billing: this job has documents at a branch the caller cannot see")

// JobBranchesOutOfReachWords is what the caller is told, and it names why.
const JobBranchesOutOfReachWords = "this job has documents at a branch you cannot see; ask someone who can reach every branch on it"

// jobReachFarDay is the as of day of the branch check: later than any document, so the check reads
// the job's whole account and an earlier as of on the statement can never skip it.
const jobReachFarDay = "9000-01-01"

// JobDocumentBranches reads the distinct branches of everything the by job statement can put on the
// job: its invoices and credit memos, and every part the by job read builds for it (the ledger rows,
// the receipts, voids, reversals, reapplications and bounces split onto it through their allocations,
// the holdback lines), each at the branch it sits at. The second half is the statement's own read
// (statementActivityCTE and statementByJobParts) run with the whole book and no date cut, so the check
// cannot drift from what the statement reads. It is date free.
func (r *Repository) JobDocumentBranches(ctx context.Context, customerID, jobID string) ([]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	zone, err := r.zoneFor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT i.branch_id::text FROM invoices i WHERE i.customer_id = $1::uuid AND i.job_id = $5::uuid AND i.branch_id IS NOT NULL
		 UNION
		 SELECT t.branch_id::text FROM customer_transactions t
		   JOIN invoices i ON i.id = t.source_ref_id AND t.source_type = 'invoice'
		  WHERE t.customer_id = $1::uuid AND i.job_id = $5::uuid AND t.branch_id IS NOT NULL
		 UNION
		 SELECT x.b FROM (`+statementActivityCTE+statementByJobParts+statementByJobBranches+`) x`,
		customerID, jobReachFarDay, nil, zone.Name(), jobID)
	if err != nil {
		return nil, fmt.Errorf("billing: job document branches: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, fmt.Errorf("billing: scan job document branch: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// JobStatementParties reads the customer's name, its jobs and the contacts a statement goes to.
func (r *Repository) JobStatementParties(ctx context.Context, customerID string) (JobStatementParties, error) {
	if r.pool == nil {
		return JobStatementParties{}, errNoDB
	}
	if !looksLikeUUIDish(customerID) {
		return JobStatementParties{}, ErrNotFound
	}
	p := JobStatementParties{Jobs: map[string]StatementJobSection{}, JobBranchID: map[string]string{}, JobContact: map[string]StatementContact{}}
	if err := r.pool.QueryRow(ctx, `SELECT name, account_no::text FROM customers WHERE id = $1::uuid`, customerID).
		Scan(&p.CustomerName, &p.AccountNo); err != nil {
		if err == pgx.ErrNoRows {
			return p, ErrNotFound
		}
		return p, fmt.Errorf("billing: job statement customer: %w", err)
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, job_no::text, name, branch_id::text, contact_name, COALESCE(contact_email::text, ''), contact_phone
		   FROM customer_jobs WHERE customer_id = $1::uuid`, customerID)
	if err != nil {
		return p, fmt.Errorf("billing: job statement jobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, no, name, branch string
		var c StatementContact
		if err := rows.Scan(&id, &no, &name, &branch, &c.Name, &c.Email, &c.Phone); err != nil {
			return p, fmt.Errorf("billing: scan job statement job: %w", err)
		}
		p.Jobs[id] = StatementJobSection{JobID: id, JobNo: no, JobName: name}
		p.JobBranchID[id] = branch
		c.Source = "job"
		p.JobContact[id] = c
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	rows.Close()
	cc := StatementContact{Source: "none"}
	err = r.pool.QueryRow(ctx,
		`SELECT name, COALESCE(email::text, ''), phone FROM customer_contacts
		  WHERE customer_id = $1::uuid AND is_active
		  ORDER BY is_primary DESC, created_at, id LIMIT 1`, customerID).Scan(&cc.Name, &cc.Email, &cc.Phone)
	if err != nil && err != pgx.ErrNoRows {
		return p, fmt.Errorf("billing: job statement contact: %w", err)
	}
	if err == nil {
		cc.Source = "customer"
	}
	p.Customer = cc
	return p, nil
}

// StatementByJob is the customer's statement grouped by job (jobID ""), or one job's statement. It is
// read inside the caller's branch reach; a job of another customer, or of a branch outside the reach,
// is not found.
func (s *StatementService) StatementByJob(ctx context.Context, customerID string, asOf time.Time, jobID string) (JobStatement, error) {
	st, ok := s.store.(statementByJobStore)
	if !ok {
		return JobStatement{}, ErrCustomerActivityNotConfigured
	}
	jobID = strings.ToLower(strings.TrimSpace(jobID))
	if jobID != "" && !looksLikeUUIDish(jobID) {
		return JobStatement{}, errBadJobID
	}
	if asOf.IsZero() {
		asOf = s.clock()
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return JobStatement{}, err
	}
	asOf = houseClockIn(zone, asOf)
	reach := reachFromContext(ctx)
	parties, err := st.JobStatementParties(ctx, customerID)
	if err != nil {
		return JobStatement{}, err
	}
	if jobID != "" {
		branch, known := parties.JobBranchID[jobID]
		if !known || (!reach.Global && !reach.Allows(branch)) {
			return JobStatement{}, ErrNotFound
		}
		if !reach.Global {
			docBranches, derr := st.JobDocumentBranches(ctx, customerID, jobID)
			if derr != nil {
				return JobStatement{}, derr
			}
			for _, b := range docBranches {
				if !reach.Allows(b) {
					return JobStatement{}, ErrJobBranchesOutOfReach
				}
			}
		}
	}
	lines, err := st.StatementLinesByJob(ctx, customerID, asOf, reach)
	if err != nil {
		return JobStatement{}, err
	}
	out := JobStatement{CustomerID: customerID, CustomerName: parties.CustomerName, AccountNo: parties.AccountNo, AsOfDate: asOf, JobID: jobID}
	byJob := map[string]*StatementJobSection{}
	order := []string{}
	for _, ln := range lines {
		if jobID != "" && ln.JobID != jobID {
			continue
		}
		sec, seen := byJob[ln.JobID]
		if !seen {
			base := parties.Jobs[ln.JobID]
			base.JobID, base.AccountLevel = ln.JobID, ln.JobID == ""
			// A job homed outside the caller's reach is not named (the customer module hides it too);
			// the lines in reach and their money stay.
			if ln.JobID != "" && !reach.Allows(parties.JobBranchID[ln.JobID]) {
				base.JobNo, base.JobName = "", ""
			}
			sec = &base
			byJob[ln.JobID] = sec
			order = append(order, ln.JobID)
		}
		sec.Lines = append(sec.Lines, ln)
		sec.SubtotalCents = ln.BalanceRun
	}
	for _, id := range order {
		out.Sections = append(out.Sections, *byJob[id])
		out.ClosingCents += byJob[id].SubtotalCents
	}
	if out.Sections == nil {
		out.Sections = []StatementJobSection{}
	}
	switch {
	case jobID == "":
		out.Contact = parties.Customer
	case parties.JobContact[jobID].Name != "" || parties.JobContact[jobID].Email != "":
		out.Contact = parties.JobContact[jobID]
	default:
		out.Contact = parties.Customer
		if out.Contact.Source == "customer" {
			out.Contact.Note = "This job has no contact of its own, so the statement goes to the customer's statement contact."
		} else {
			out.Contact.Note = "Neither this job nor the customer has a contact on file."
		}
	}
	return out, nil
}

// RenderJobStatementHTML renders a by job statement as one self-contained document: a table per
// section with its subtotal, and the closing balance. Everything customer supplied is escaped.
func RenderJobStatementHTML(h StatementHeader, st JobStatement) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Statement, ` + esc(st.CustomerName) + `</title></head>`)
	b.WriteString(`<body style="font:14px/1.5 system-ui,sans-serif;color:#1a1a1a;max-width:760px;margin:0 auto;padding:32px">`)
	b.WriteString(`<h1 style="margin:0;font-size:20px">` + esc(h.BranchName) + `</h1>`)
	b.WriteString(`<h2 style="font-size:18px">STATEMENT, as of ` + st.AsOfDate.Format("Jan 2, 2006") + `</h2>`)
	b.WriteString(`<div style="font-weight:600">` + esc(st.CustomerName) + `</div><div style="color:#555;font-size:12px">Account ` + esc(st.AccountNo) + `</div>`)
	if st.Contact.Name != "" || st.Contact.Email != "" {
		b.WriteString(`<div style="margin:12px 0">Attention: ` + esc(st.Contact.Name) + ` ` + esc(st.Contact.Email) + `</div>`)
	}
	for _, sec := range st.Sections {
		title := "Account level"
		if !sec.AccountLevel {
			title = "Job " + sec.JobNo + ", " + sec.JobName
			if sec.JobNo == "" && sec.JobName == "" {
				title = "A job at another branch"
			}
		}
		b.WriteString(`<h3 style="margin:24px 0 4px;font-size:15px">` + esc(title) + `</h3><table style="width:100%;border-collapse:collapse"><tbody>`)
		for _, ln := range sec.Lines {
			b.WriteString(`<tr style="border-bottom:1px solid #eee"><td style="padding:4px">` + ln.Date.Format("Jan 2, 2006") + `</td><td style="padding:4px">` +
				esc(ln.DocNo) + `</td><td style="padding:4px">` + esc(ln.Description) + `</td><td style="padding:4px;text-align:right">` +
				moneyOrBlank(ln.ChargeCents) + `</td><td style="padding:4px;text-align:right">` + moneyOrBlank(ln.CreditCents) + `</td></tr>`)
		}
		b.WriteString(`<tr><td colspan="4" style="padding:6px 4px;font-weight:600">Subtotal</td><td style="padding:6px 4px;text-align:right;font-weight:600">` +
			Money(sec.SubtotalCents) + `</td></tr></tbody></table>`)
	}
	b.WriteString(`<p style="margin-top:24px;font-weight:700">Total due ` + Money(st.ClosingCents) + `</p></body></html>`)
	return b.String()
}
