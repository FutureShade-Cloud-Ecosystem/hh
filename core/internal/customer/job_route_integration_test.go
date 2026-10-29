//go:build integration

// job_route_integration_test.go, wave 3 JA-1: the job doors under a customer.
//
// The doors are the customer record's own (a staff read, a manager write) and they are branch
// fenced. This file proves the fence at the door, against the real migrated database and the real
// pgx repository: a job of another branch is NOT FOUND by a caller whose grants do not reach it,
// and the same job reads for the caller whose grants do.
//
// THE FIXTURE IS AN UNHOMED CUSTOMER ON PURPOSE. A customer homed at a branch is fenced by its own
// home before the job is ever read, so it cannot show the job's own fence. A company-wide customer
// is visible to every branch, and the job under it carries the branch, which is exactly the case
// the job's own fence is about.
package customer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// plantJobWorld plants the world these doors run against: a company-wide customer, two
// branches, a staff row the job's creator is stamped with, and a job at the first branch.
type jobRouteFixture struct {
	customerID string
	branchA    string
	branchB    string
	jobID      string
	userA      string
	userB      string
}

// plantJobUser plants the ERP staff row a caller's UserID names. The job's creator is that row's
// id, so a write with no linked staff row is refused rather than attributed to nobody.
func plantJobUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, status) VALUES ($1, $2, 'ACTIVE') RETURNING id::text`,
		"ja1-"+randSuffix()+"@example.test", "Job Account Staff").Scan(&id)
	if err != nil {
		t.Fatalf("plant the staff row: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id
}

func plantJobWorld(t *testing.T, pool *pgxpool.Pool) jobRouteFixture {
	t.Helper()
	f := jobRouteFixture{
		customerID: plantJobCustomer(t, pool, "JA1-ROUTE-"+randSuffix()),
		branchA:    plantJobBranch(t, pool, "JA1-ROUTE-BA-"+randSuffix()),
		branchB:    plantJobBranch(t, pool, "JA1-ROUTE-BB-"+randSuffix()),
		userA:      plantJobUser(t, pool),
		userB:      plantJobUser(t, pool),
	}
	repo := NewRepository(pool)
	job, err := repo.InsertJob(context.Background(), JobWrite{
		CustomerID: f.customerID,
		BranchID:   f.branchA,
		JobNo:      "J-ROUTE-1",
		CreatedBy:  f.userA,
		Fields:     JobFields{Name: "Riverside driveway", ShipToAddress: map[string]any{"line1": "9 Mill Rd"}},
	})
	if err != nil {
		t.Fatalf("plant the job: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customer_jobs WHERE id = $1`, job.JobID)
	})
	f.jobID = job.JobID
	return f
}

func TestIntegration_JobRoutes_AJobOfAnotherBranchIsNotFound(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithJobs(repo)
	h := NewHandler(svc, nil)

	f := plantJobWorld(t, pool)
	callerB := identity.StaffContext{UserID: f.userB, Scope: identity.ScopeBranch, Branches: map[string]bool{f.branchB: true}}
	callerA := identity.StaffContext{UserID: f.userA, Scope: identity.ScopeBranch, Branches: map[string]bool{f.branchA: true}}

	// The caller whose grants hold the job's branch reads it.
	rec := callJobRoute(h.getJob, http.MethodGet, f.customerID, f.jobID, callerA, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("the caller whose branch holds the job: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode the job: %v", err)
	}
	if got["job_no"] != "J-ROUTE-1" || got["status"] != JobStatusOpen {
		t.Fatalf("the job came back as another job: %v", got)
	}
	if got["customer_id"] != f.customerID {
		t.Fatalf("the job came back under another customer: %v", got)
	}
	// THE WIRE CARRIES THE DECLARED KEYS AND NOTHING ELSE. A key the document does not
	// declare is a drift, and so is a declared key sent as null where the document says a
	// figure: the job's own credit limit, terms and lien dates are absent when the job
	// carries none, and the effective terms are always there.
	assertJobWireKeys(t, got)

	// The caller whose grants do not hold it is told nothing: 404, not 403.
	rec = callJobRoute(h.getJob, http.MethodGet, f.customerID, f.jobID, callerB, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the caller whose branch does not hold the job: want 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// The list is fenced the same way: the job is not in the other branch's list, and it is in
	// its own.
	rec = callJobRoute(h.listJobs, http.MethodGet, f.customerID, "", callerB, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list the jobs for the other branch: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the job list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("the list answered a job of another branch: %v", list)
	}
	rec = callJobRoute(h.listJobs, http.MethodGet, f.customerID, "", callerA, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the job list: %v", err)
	}
	if len(list) != 1 || list[0]["job_id"] != f.jobID {
		t.Fatalf("the list did not answer the job of the caller's own branch: %v", list)
	}

	// A write outside the reach changes nothing: the edit is not found.
	rec = callJobRoute(h.editJob, http.MethodPut, f.customerID, f.jobID, callerB, `{"name":"Taken over"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the edit outside the reach: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	after, found, err := repo.GetJob(ctx, f.customerID, f.jobID, AccountReach{BranchIDs: []string{f.branchA}})
	if err != nil || !found {
		t.Fatalf("read the job back: found %t err %v", found, err)
	}
	if after.Name != "Riverside driveway" {
		t.Fatalf("a write outside the reach changed the job: %q", after.Name)
	}

	// The close outside the reach is not found too.
	rec = callJobRoute(h.closeJob, http.MethodPost, f.customerID, f.jobID, callerB, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the close outside the reach: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	after, _, _ = repo.GetJob(ctx, f.customerID, f.jobID, AccountReach{BranchIDs: []string{f.branchA}})
	if after.Status != JobStatusOpen {
		t.Fatalf("a close outside the reach closed the job: %q", after.Status)
	}
}

// TestIntegration_JobRoutes_TheDoorsWork: the create, the edit and the close, through the doors,
// for a caller whose grants hold the branch.
func TestIntegration_JobRoutes_TheDoorsWork(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithJobs(repo)
	h := NewHandler(svc, nil)

	f := plantJobWorld(t, pool)
	callerA := identity.StaffContext{UserID: f.userA, Scope: identity.ScopeBranch, Branches: map[string]bool{f.branchA: true}}

	// The create names the branch, because the customer is company-wide.
	rec := callJobRoute(h.createJob, http.MethodPost, f.customerID, "", callerA,
		`{"job_no":"J-ROUTE-2","name":"Mill job","branch_id":"`+f.branchA+`","po_reference":"PO-1","contact_name":"Site Foreman"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create the job: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode the created job: %v", err)
	}
	jobID, _ := created["job_id"].(string)
	if jobID == "" {
		t.Fatalf("the created job carries no id: %v", created)
	}
	if created["branch_id"] != f.branchA {
		t.Fatalf("the job did not sit at the named branch: %v", created)
	}
	if created["credit_limit_cents"] != nil {
		t.Fatalf("a job with no own limit carries one: %v", created)
	}

	// The edit keeps the fields the body leaves out.
	rec = callJobRoute(h.editJob, http.MethodPut, f.customerID, jobID, callerA, `{"name":"Mill job, phase two"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit the job: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var edited map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &edited); err != nil {
		t.Fatalf("decode the edited job: %v", err)
	}
	if edited["name"] != "Mill job, phase two" {
		t.Fatalf("the edit did not land: %v", edited)
	}
	if edited["po_reference"] != "PO-1" {
		t.Fatalf("a field the body left out did not keep its value: %v", edited)
	}

	// The close lands, and a second close is a conflict.
	rec = callJobRoute(h.closeJob, http.MethodPost, f.customerID, jobID, callerA, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("close the job: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var closed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &closed); err != nil {
		t.Fatalf("decode the closed job: %v", err)
	}
	if closed["status"] != JobStatusClosed {
		t.Fatalf("the job is not closed: %v", closed)
	}
	rec = callJobRoute(h.closeJob, http.MethodPost, f.customerID, jobID, callerA, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("the second close: want 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// A job number already in use under the same customer is a conflict.
	rec = callJobRoute(h.createJob, http.MethodPost, f.customerID, "", callerA,
		`{"job_no":"J-ROUTE-2","name":"Mill job again","branch_id":"`+f.branchA+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("the duplicate job number: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestIntegration_JobRoutes_BranchWallOnCreate: a caller may not open a job at a branch its grants
// do not hold, and a customer with no home branch needs the branch named.
func TestIntegration_JobRoutes_BranchWallOnCreate(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithJobs(repo)
	h := NewHandler(svc, nil)

	f := plantJobWorld(t, pool)
	callerB := identity.StaffContext{UserID: f.userB, Scope: identity.ScopeBranch, Branches: map[string]bool{f.branchB: true}}

	rec := callJobRoute(h.createJob, http.MethodPost, f.customerID, "", callerB,
		`{"job_no":"J-ROUTE-3","name":"Mill job","branch_id":"`+f.branchA+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a job at a branch outside the grants: want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "outside your grants") {
		t.Fatalf("the refusal is not in words: %s", rec.Body.String())
	}

	rec = callJobRoute(h.createJob, http.MethodPost, f.customerID, "", callerB,
		`{"job_no":"J-ROUTE-4","name":"Mill job"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a job with no branch: want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// assertJobWireKeys pins the job's wire to the keys the document declares.
//
// The job in the fixture carries no own limit, no own terms and no lien dates, and no account
// position gate is bound, so those keys are absent rather than null: absence and zero are
// different accounts, and a null where the document says a figure is a drift.
func assertJobWireKeys(t *testing.T, got map[string]any) {
	t.Helper()
	want := map[string]bool{
		"job_id": true, "customer_id": true, "branch_id": true, "job_no": true,
		"name": true, "ship_to_address": true, "po_reference": true,
		"contact_name": true, "contact_email": true, "contact_phone": true,
		"effective_terms_days": true, "status": true, "created_by": true,
		"created_at": true, "updated_at": true,
	}
	for key := range got {
		if !want[key] {
			t.Fatalf("the job wire carries %q, which the document does not declare: %v", key, got)
		}
	}
	for key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("the job wire is missing %q: %v", key, got)
		}
	}
}

// callJobRoute runs one job door with the caller's staff context on the request.
func callJobRoute(fn func(http.ResponseWriter, *http.Request, guards.StaffCaller), method, customerID, jobID string, sc identity.StaffContext, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/v1/customers/"+customerID+"/jobs", reader)
	req.SetPathValue("id", customerID)
	if jobID != "" {
		req.SetPathValue("jobId", jobID)
	}
	req = req.WithContext(identity.ContextWithStaff(req.Context(), sc))
	rec := httptest.NewRecorder()
	caller := guards.StaffCaller{StaffAuthority: coreidentity.StaffAuthority{
		Tier: coreidentity.TierManager, Scope: string(sc.Scope), Branches: branchList(sc),
	}}
	fn(rec, req, caller)
	return rec
}

func branchList(sc identity.StaffContext) []string {
	out := []string{}
	for id, granted := range sc.Branches {
		if granted {
			out = append(out, id)
		}
	}
	return out
}
