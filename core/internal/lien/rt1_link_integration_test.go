//go:build integration

package lien

import (
	"context"
	"testing"
)

// RT-1 item 4: a lien job links to one customer job of the same customer. Another customer's job and a
// closed job are refused in the customer module's words; JA-1's closed job rule is the customer module's.

func (w *rt1World) linked(t *testing.T, lienJobID string) string {
	t.Helper()
	var id *string
	if err := w.pool.QueryRow(context.Background(), `SELECT customer_job_id::text FROM lien_jobs WHERE id = $1`, lienJobID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id == nil {
		return ""
	}
	return *id
}

func TestIntegration_RT1_LinkTheSameCustomersJob(t *testing.T) {
	w := rt1Plant(t)
	mgr := w.as(w.manager(w.branchA))
	lj := w.lienJob(t, w.customerA, w.branchA)
	got, err := w.svc.LinkCustomerJob(mgr, lj, w.jobOnSiteON)
	if err != nil {
		t.Fatal(err)
	}
	if got.CustomerJobID != w.jobOnSiteON || w.linked(t, lj) != w.jobOnSiteON {
		t.Fatalf("link not stored: %+v", got)
	}
	read, err := w.svc.GetJob(mgr, lj)
	if err != nil || read.CustomerJobID != w.jobOnSiteON {
		t.Fatalf("the lien job read must carry its link: %+v, %v", read, err)
	}
	// Moving the link to another open job of the same customer is allowed; an empty id unlinks.
	if _, err := w.svc.LinkCustomerJob(mgr, lj, w.jobOnSiteZA); err != nil || w.linked(t, lj) != w.jobOnSiteZA {
		t.Fatalf("relink: %v", err)
	}
	if _, err := w.svc.LinkCustomerJob(mgr, lj, ""); err != nil || w.linked(t, lj) != "" {
		t.Fatalf("unlink: %v", err)
	}
}

func TestIntegration_RT1_LinkFence(t *testing.T) {
	w := rt1Plant(t)
	mgr := w.as(w.manager(w.branchA))
	lj := w.lienJob(t, w.customerA, w.branchA)

	_, err := w.svc.LinkCustomerJob(mgr, lj, w.jobOfB)
	wantClientErr(t, err, 422, "that job is another customer's job")

	_, err = w.svc.LinkCustomerJob(mgr, lj, w.closedJob)
	wantClientErr(t, err, 422, "the job is closed, so it takes no new document")

	_, err = w.svc.LinkCustomerJob(mgr, lj, "00000000-0000-4000-8000-000000000000")
	wantClientErr(t, err, 404, "not found")

	if w.linked(t, lj) != "" {
		t.Fatal("a refused link must store nothing")
	}

	noCustomer := w.lienJob(t, "", w.branchA)
	_, err = w.svc.LinkCustomerJob(mgr, noCustomer, w.jobOnSiteON)
	wantClientErr(t, err, 422, "has no customer")

	// The gate: a clerk, and a manager who does not reach the lien job's branch.
	_, err = w.svc.LinkCustomerJob(w.as(w.clerk(w.branchA)), lj, w.jobOnSiteON)
	wantClientErr(t, err, 403, "manager")
	_, err = w.svc.LinkCustomerJob(w.as(w.manager(w.branchB)), lj, w.jobOnSiteON)
	wantClientErr(t, err, 403, "outside your reach")
}
