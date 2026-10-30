//go:build integration

// job_table_integration_test.go, wave 3 JA-1 (decision 1.1 A): the customer_jobs table itself.
//
// The table is the first half of the job account: a site of work under one customer, carrying its own
// ship to address, PO reference and contact, and optionally its own credit limit and payment terms.
// The two NULLABLE figures are the ones the whole split turns on: a job with no credit_limit_cents
// and no terms_days shares the CUSTOMER's figures, and null is not zero. A job that read as zero
// credit because nobody filled a field in would be refused at the counter for a limit nobody set.
//
// This file pins the table against the real migrated database, not against a schema description:
// every column the brief names is written and read back, the status holds only OPEN or CLOSED, the
// job number is unique PER CUSTOMER (two customers may each hold J-1; one customer may not hold two),
// and a job carries the creator and the timestamps.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/customer/... -run TestIntegration_CustomerJobs
package customer

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// jobTableFixture plants the two customers and the two branches this file's legs need, and removes
// them again. The customers are unhomed (a company-wide customer) so the table's own constraints are
// what the legs read, not a branch the customer happens to carry.
type jobTableFixture struct {
	customerA string
	customerB string
	branchA   string
	branchB   string
}

func plantJobTable(t *testing.T, pool *pgxpool.Pool) jobTableFixture {
	t.Helper()
	f := jobTableFixture{}
	f.customerA = plantJobCustomer(t, pool, "JA1-TBL-A-"+randSuffix())
	f.customerB = plantJobCustomer(t, pool, "JA1-TBL-B-"+randSuffix())
	f.branchA = plantJobBranch(t, pool, "JA1-TBL-BA-"+randSuffix())
	f.branchB = plantJobBranch(t, pool, "JA1-TBL-BB-"+randSuffix())
	return f
}

func plantJobCustomer(t *testing.T, pool *pgxpool.Pool, accountNo string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents, payment_terms_days)
		 VALUES ($1, $2, 'CONTRACTOR', 500000, 30) RETURNING id::text`, accountNo, "Job Table Co").Scan(&id)
	if err != nil {
		t.Fatalf("plant the customer %q: %v", accountNo, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, id) })
	return id
}

// plantJobBranch plants a BRANCH location. A branch row is its own branch_id (the location's own
// id), which is the shape migration 0003 plants for the two yards.
func plantJobBranch(t *testing.T, pool *pgxpool.Pool, code string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	_, err := pool.Exec(ctx,
		`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
		 VALUES ($1, $1, NULL, 'BRANCH', $2, $3)`, id, code, "Job Table Branch")
	if err != nil {
		t.Fatalf("plant the branch %q: %v", code, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM locations WHERE id = $1`, id) })
	return id
}

// TestIntegration_CustomerJobs_TableHoldsTheBriefsColumns writes one row with every column the
// brief names and reads it back. The nullable figures stay null (a job that shares its customer's
// terms and limit carries neither), the status is OPEN, and the creator and the timestamps are
// the row's own.
func TestIntegration_CustomerJobs_TableHoldsTheBriefsColumns(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := plantJobTable(t, pool)

	var (
		id             string
		customerID     string
		branchID       string
		jobNo          string
		name           string
		shipTo         string
		poRef          string
		contactName    string
		contactEmail   string
		contactPhone   string
		limit          *int64
		terms          *int
		status         string
		substantial    *string
		lastSupply     *string
		createdBy      *string
		createdAtIsSet bool
		updatedAtIsSet bool
	)
	err := pool.QueryRow(ctx, `
		INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, ship_to_address, po_reference,
		                           contact_name, contact_email, contact_phone, status)
		VALUES ($1, $2, 'J-TBL-1', 'Riverside driveway', '{"line1":"9 Mill Rd","city":"Trenton"}',
		        'PO-8891', 'Site Foreman', 'site@example.test', '6135550142', 'OPEN')
		RETURNING id::text, customer_id::text, branch_id::text, job_no::text, name,
		          ship_to_address->>'line1', po_reference, contact_name, COALESCE(contact_email::text, ''),
		          contact_phone, credit_limit_cents, terms_days, status,
		          substantial_performance_date::text, last_supply_date::text,
		          created_by::text, created_at IS NOT NULL, updated_at IS NOT NULL`,
		f.customerA, f.branchA).
		Scan(&id, &customerID, &branchID, &jobNo, &name, &shipTo, &poRef,
			&contactName, &contactEmail, &contactPhone, &limit, &terms, &status,
			&substantial, &lastSupply, &createdBy, &createdAtIsSet, &updatedAtIsSet)
	if err != nil {
		t.Fatalf("insert a job: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customer_jobs WHERE id = $1`, id) })

	if customerID != f.customerA || branchID != f.branchA {
		t.Fatalf("the job sits under the wrong customer or branch: customer %q branch %q", customerID, branchID)
	}
	if jobNo != "J-TBL-1" || name != "Riverside driveway" {
		t.Fatalf("the job number and name are not stored: %q %q", jobNo, name)
	}
	if shipTo != "9 Mill Rd" || poRef != "PO-8891" {
		t.Fatalf("the ship to address and the PO reference are not stored: %q %q", shipTo, poRef)
	}
	if contactName != "Site Foreman" || contactEmail != "site@example.test" || contactPhone != "6135550142" {
		t.Fatalf("the contact is not stored: %q %q %q", contactName, contactEmail, contactPhone)
	}
	if limit != nil || terms != nil {
		t.Fatalf("a job with no own figures carries none: limit %v terms %v", limit, terms)
	}
	if status != "OPEN" {
		t.Fatalf("the status is not OPEN: %q", status)
	}
	if substantial != nil || lastSupply != nil {
		t.Fatalf("the lien dates are empty on a fresh job: %v %v", substantial, lastSupply)
	}
	if !createdAtIsSet || !updatedAtIsSet {
		t.Fatal("the timestamps are not the row's own")
	}
}

// TestIntegration_CustomerJobs_JobNoIsUniquePerCustomer is red leg 6: the job number is the
// CUSTOMER's own. One customer may not hold two jobs under the same number; two customers may each
// hold the same number, because the number is read on a job belonging to a customer.
func TestIntegration_CustomerJobs_JobNoIsUniquePerCustomer(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := plantJobTable(t, pool)

	insert := func(customerID, branchID, jobNo string) (string, error) {
		var id string
		err := pool.QueryRow(ctx, `
			INSERT INTO customer_jobs (customer_id, branch_id, job_no, name)
			VALUES ($1, $2, $3, 'Mill job') RETURNING id::text`, customerID, branchID, jobNo).Scan(&id)
		if err == nil {
			t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customer_jobs WHERE id = $1`, id) })
		}
		return id, err
	}

	if _, err := insert(f.customerA, f.branchA, "J-DUP"); err != nil {
		t.Fatalf("insert the first job of the customer: %v", err)
	}
	_, err := insert(f.customerA, f.branchB, "J-DUP")
	if err == nil {
		t.Fatal("the same customer took two jobs under the same number")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("the second job of the customer failed with %v, want a unique violation", err)
	}

	// The same number under another customer is a different job, and it lands.
	if _, err := insert(f.customerB, f.branchB, "J-DUP"); err != nil {
		t.Fatalf("another customer took the same job number: %v", err)
	}
}

// TestIntegration_CustomerJobs_StatusHoldsOnlyOpenOrClosed: the status is the one the split names,
// and nothing else is a status a job may hold.
func TestIntegration_CustomerJobs_StatusHoldsOnlyOpenOrClosed(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := plantJobTable(t, pool)

	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, status)
		VALUES ($1, $2, 'J-STATUS', 'Mill job', 'CLOSED') RETURNING id::text`,
		f.customerA, f.branchA).Scan(&id)
	if err != nil {
		t.Fatalf("a CLOSED job is refused: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customer_jobs WHERE id = $1`, id) })

	_, err = pool.Exec(ctx, `
		INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, status)
		VALUES ($1, $2, 'J-STATUS-BAD', 'Mill job', 'HALF DONE')`, f.customerA, f.branchA)
	if err == nil {
		t.Fatal("a job took a status the split does not name")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("the bad status failed with %v, want a check violation", err)
	}
}

// TestIntegration_CustomerJobs_NegativeFiguresAreRefused: a job's own limit and terms are money and
// days, and neither may be negative. The rule that a job's own limit may not EXCEED the customer's
// is the service's (job_service.go); the sign is the table's own.
func TestIntegration_CustomerJobs_NegativeFiguresAreRefused(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := plantJobTable(t, pool)

	_, err := pool.Exec(ctx, `
		INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, credit_limit_cents)
		VALUES ($1, $2, 'J-NEG-LIMIT', 'Mill job', -1)`, f.customerA, f.branchA)
	if err == nil {
		t.Fatal("a job took a negative credit limit")
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, terms_days)
		VALUES ($1, $2, 'J-NEG-TERMS', 'Mill job', -1)`, f.customerA, f.branchA)
	if err == nil {
		t.Fatal("a job took negative payment terms")
	}
}

// TestIntegration_CustomerJobs_CustomerGoesWithItsJobs: a job sits under its customer, so deleting
// the customer takes the job with it. The constraint is the table's own, and it is the reason the
// job can never outlive the account it belongs to.
func TestIntegration_CustomerJobs_CustomerGoesWithItsJobs(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := plantJobTable(t, pool)

	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO customer_jobs (customer_id, branch_id, job_no, name)
		VALUES ($1, $2, 'J-CASCADE', 'Mill job') RETURNING id::text`, f.customerB, f.branchA).Scan(&id)
	if err != nil {
		t.Fatalf("insert the job: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, f.customerB); err != nil {
		t.Fatalf("delete the customer: %v", err)
	}
	var found bool
	err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customer_jobs WHERE id = $1)`, id).Scan(&found)
	if err != nil {
		t.Fatalf("read the job back: %v", err)
	}
	if found {
		t.Fatal("the job outlived the customer it belongs to")
	}
}

// TestIntegration_CustomerJobs_RepositoryRoundTrip: the persistence reads and writes the row the
// table holds, and the fence is in the statement: a job of a branch the reach does not hold is not
// found by the read and is not written by the write.
func TestIntegration_CustomerJobs_RepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := plantJobTable(t, pool)
	repo := NewRepository(pool)

	limit := int64(250000)
	terms := 14
	j, err := repo.InsertJob(ctx, JobWrite{
		CustomerID: f.customerA,
		BranchID:   f.branchA,
		JobNo:      "J-REPO-1",
		CreatedBy:  "",
		Fields: JobFields{
			Name:             "Riverside driveway",
			ShipToAddress:    map[string]any{"line1": "9 Mill Rd"},
			POReference:      "PO-8891",
			ContactName:      "Site Foreman",
			ContactEmail:     "site@example.test",
			ContactPhone:     "6135550142",
			CreditLimitCents: &limit,
			TermsDays:        &terms,
		},
	})
	if err != nil {
		t.Fatalf("insert the job: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM customer_jobs WHERE id = $1`, j.JobID) })

	if j.JobNo != "J-REPO-1" || j.CustomerID != f.customerA || j.BranchID != f.branchA {
		t.Fatalf("the job came back as another job: %+v", j)
	}
	if j.Status != JobStatusOpen {
		t.Fatalf("a new job is not OPEN: %q", j.Status)
	}
	if j.CreditLimitCents == nil || *j.CreditLimitCents != limit {
		t.Fatalf("the job's own limit did not survive the write: %v", j.CreditLimitCents)
	}
	if j.TermsDays == nil || *j.TermsDays != terms {
		t.Fatalf("the job's own terms did not survive the write: %v", j.TermsDays)
	}
	if j.ShipToAddress["line1"] != "9 Mill Rd" {
		t.Fatalf("the ship to address did not survive the write: %v", j.ShipToAddress)
	}

	// The read inside the reach finds it.
	reach := AccountReach{BranchIDs: []string{f.branchA}}
	got, found, err := repo.GetJob(ctx, f.customerA, j.JobID, reach)
	if err != nil || !found {
		t.Fatalf("the job was not found inside its own branch: found %t err %v", found, err)
	}
	if got.JobID != j.JobID {
		t.Fatalf("the read answered another job: %q", got.JobID)
	}

	// The read outside the reach does not: a job of another branch is NOT FOUND, never forbidden.
	other := AccountReach{BranchIDs: []string{f.branchB}}
	if _, found, err := repo.GetJob(ctx, f.customerA, j.JobID, other); err != nil {
		t.Fatalf("read the job outside the reach: %v", err)
	} else if found {
		t.Fatal("a job of another branch was found by a caller whose reach does not hold it")
	}

	// The list is fenced the same way.
	list, err := repo.ListJobs(ctx, f.customerA, reach)
	if err != nil {
		t.Fatalf("list the jobs: %v", err)
	}
	if len(list) != 1 || list[0].JobID != j.JobID {
		t.Fatalf("the list did not answer the job inside the reach: %+v", list)
	}
	list, err = repo.ListJobs(ctx, f.customerA, other)
	if err != nil {
		t.Fatalf("list the jobs outside the reach: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("the list answered a job of another branch: %+v", list)
	}

	// The write is fenced too: a write from a caller whose reach does not hold the job changes
	// nothing and says the job is not found.
	newName := "Riverside drive, phase two"
	_, err = repo.UpdateJob(ctx, f.customerA, j.JobID, JobFields{Name: newName}, other)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("a write outside the reach answered %v, want not found", err)
	}

	// Inside the reach it lands.
	updated, err := repo.UpdateJob(ctx, f.customerA, j.JobID, JobFields{
		Name:             newName,
		ShipToAddress:    map[string]any{"line1": "11 Mill Rd"},
		POReference:      "PO-8892",
		ContactName:      "Site Foreman",
		ContactEmail:     "",
		ContactPhone:     "6135550142",
		CreditLimitCents: nil,
		TermsDays:        nil,
	}, reach)
	if err != nil {
		t.Fatalf("update the job: %v", err)
	}
	if updated.Name != newName || updated.POReference != "PO-8892" {
		t.Fatalf("the update did not land: %+v", updated)
	}
	if updated.ContactEmail != "" {
		t.Fatalf("an emptied contact email did not clear: %q", updated.ContactEmail)
	}
	if updated.CreditLimitCents != nil || updated.TermsDays != nil {
		t.Fatalf("a cleared own figure did not clear: %v %v", updated.CreditLimitCents, updated.TermsDays)
	}

	// The close lands, and a second close says the job is already closed.
	closed, err := repo.CloseJob(ctx, f.customerA, j.JobID, reach)
	if err != nil {
		t.Fatalf("close the job: %v", err)
	}
	if closed.Status != JobStatusClosed {
		t.Fatalf("the job is not closed: %q", closed.Status)
	}
	if _, err := repo.CloseJob(ctx, f.customerA, j.JobID, reach); !errors.Is(err, ErrJobAlreadyClosed) {
		t.Fatalf("the second close answered %v, want already closed", err)
	}
}

// TestIntegration_CustomerJobs_DuplicateJobNoIsTheSentinel: the repository turns the unique
// violation into the sentinel the handler maps to a 409, not a leaked 500.
func TestIntegration_CustomerJobs_DuplicateJobNoIsTheSentinel(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := plantJobTable(t, pool)
	repo := NewRepository(pool)

	write := func(jobNo string) {
		_, err := repo.InsertJob(ctx, JobWrite{
			CustomerID: f.customerA,
			BranchID:   f.branchA,
			JobNo:      jobNo,
			Fields:     JobFields{Name: "Mill job"},
		})
		if err != nil {
			t.Fatalf("insert the job %q: %v", jobNo, err)
		}
	}
	write("J-SENTINEL")
	_, err := repo.InsertJob(ctx, JobWrite{
		CustomerID: f.customerA,
		BranchID:   f.branchA,
		JobNo:      "J-SENTINEL",
		Fields:     JobFields{Name: "Mill job"},
	})
	if !errors.Is(err, ErrDuplicateJobNo) {
		t.Fatalf("the second job of the customer answered %v, want the duplicate sentinel", err)
	}
}
