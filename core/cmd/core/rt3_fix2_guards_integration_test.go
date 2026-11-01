//go:build integration

package main

// rt3_fix2_guards_integration_test.go, the RT-3 fix round 2, item 1: both release guards key on the ledger,
// not on the link. A manager can unlink a lien job, so the old job level route and RT-3's Controller
// release must each look at what the ledger holds. Driven over the served mux (the old route as the
// General Manager with a real Controller co-signer, RT-3 as the Controller).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

const (
	rt3f2OrgID    = "88a20000-0000-4000-8000-0000000000c2"
	rt3f2CoSigner = "d7000000-0000-4000-9000-0000000000f2"
)

func (l *rt2Leg) asGeneralManager() {
	l.sg.SetAuthority(coreid.StaffAuthority{RoleID: "general_manager", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})
}

func (l *rt2Leg) asController() {
	l.sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})
}

// plantCoSigner plants a real Controller the old route's four eyes resolves server side (a users row and a
// financial_controller grant), and takes both back at the end, loudly.
func (l *rt2Leg) plantCoSigner() {
	l.t.Helper()
	ctx := context.Background()
	mustExec(l.t, ctx, l.pool, `
		INSERT INTO users (id, email, display_name, status, password_hash, platform_user_id)
		VALUES ($1::uuid, 'rt3f2-cosigner@test.invalid', 'RT3F2 Co-signer', 'ACTIVE', NULL, 'rt3f2-cosigner')`, rt3f2CoSigner)
	mustExec(l.t, ctx, l.pool, `INSERT INTO staff_role_grant (platform_user_id, role_id, org_id) VALUES ('rt3f2-cosigner', 'financial_controller', $1::uuid)`, rt3f2OrgID)
	l.t.Cleanup(func() {
		if _, err := l.pool.Exec(context.Background(), `DELETE FROM staff_role_grant WHERE platform_user_id = 'rt3f2-cosigner'`); err != nil {
			l.t.Errorf("delete from staff_role_grant: %v", err)
		}
		if _, err := l.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, rt3f2CoSigner); err != nil {
			l.t.Errorf("delete from users: %v", err)
		}
	})
}

// freshLienJob plants a second lien job of the leg's customer, not linked, with its preservation period over.
func (l *rt2Leg) freshLienJob() string {
	l.t.Helper()
	l.n++
	var id string
	if err := l.pool.QueryRow(context.Background(), `
		INSERT INTO lien_jobs (job_no, customer_id, branch_id, substantial_perf_date)
		VALUES ($1, $2::uuid, $3::uuid, $4::date) RETURNING id::text`,
		fmt.Sprintf("RT3F2-LIEN-%d", l.n), l.cust, l.branch, rt3Today().AddDate(0, 0, -70).Format("2006-01-02")).Scan(&id); err != nil {
		l.t.Fatalf("plant a fresh lien job: %v", err)
	}
	return id
}

func (l *rt2Leg) oldRoute(lienID string) (int, string) {
	l.asGeneralManager()
	defer l.asController()
	return l.call("POST", "/api/v1/lien/holdback/"+lienID+"/release", `{"approver_id":"`+rt3f2CoSigner+`"}`)
}

func (l *rt2Leg) linkOver(lienID, customerJob string) (int, string) {
	l.asGeneralManager()
	defer l.asController()
	body := `{"customer_job_id":null}`
	if customerJob != "" {
		body = `{"customer_job_id":"` + customerJob + `"}`
	}
	return l.call("PUT", "/api/v1/lien/jobs/"+lienID+"/customer-job", body)
}

func (l *rt2Leg) rt3Over(key string) (int, string) {
	l.asController()
	return l.call("POST", "/api/v1/ar/holdback/releases", `{"customer_job_id":"`+l.job+`","idempotency_key":"`+key+`"}`)
}

// Unlink, then the old route, then link a fresh job, then RT-3: nothing is released twice.
func TestRT3Fix2_UnlinkThenOldRouteThenRelinkThenRT3ReleasesOnce(t *testing.T) {
	l := rt2Setup(t, "f2a")
	l.newJob(true)
	jobA, lienA := l.job, l.lien
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)
	l.plantCoSigner()
	ctx := context.Background()
	_ = ctx

	if code, body := l.linkOver(lienA, ""); code != http.StatusUnprocessableEntity || !strings.Contains(body, "retention") {
		t.Errorf("an unlink while the ledger holds invoice level retention: want 422 in words, got %d %s", code, body)
	}
	if code, body := l.oldRoute(lienA); code != http.StatusUnprocessableEntity {
		t.Errorf("the old route on a job whose ledger holds invoice retention: want 422, got %d %s", code, body)
	}
	lienB := l.freshLienJob()
	if code, body := l.linkOver(lienB, jobA); code != http.StatusOK {
		t.Fatalf("link a fresh job: want 200, got %d %s", code, body)
	}
	l.job = jobA
	if code, body := l.rt3Over("f2a-key"); code != http.StatusCreated {
		t.Fatalf("RT-3: want 201, got %d %s", code, body)
	}
	l.drain()

	eq(t, "1150 debit and credit: released once", l.glBalance("1150"), [2]int64{10_000, 10_000})
	eq(t, "2350 untouched", l.glBalance("2350"), [2]int64{0, 0})
	eq(t, "the column", l.holdbackOf(doc.InvoiceID), int64(0))
	eq(t, "the ledger nets to nothing held", l.ledgerNet(), int64(0))
	l.ties("after the story")
}

// The old route refuses a job whose ledger holds invoice level rows even when the link is gone.
func TestRT3Fix2_TheOldRouteReadsTheLedgerNotTheLink(t *testing.T) {
	l := rt2Setup(t, "f2b")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.plantCoSigner()
	// An unlink planted by SQL: the route's own refusal is the next leg's business.
	mustExec(t, context.Background(), l.pool, `UPDATE lien_jobs SET customer_job_id = NULL WHERE id = $1::uuid`, l.lien)

	b1150, b2350, rows := l.glBalance("1150"), l.glBalance("2350"), l.ledgerRows()
	if code, body := l.oldRoute(l.lien); code != http.StatusUnprocessableEntity {
		t.Errorf("an unlinked job whose ledger holds invoice retention: want 422, got %d %s", code, body)
	}
	eq(t, "1150 unchanged", l.glBalance("1150"), b1150)
	eq(t, "2350 unchanged", l.glBalance("2350"), b2350)
	eq(t, "ledger unchanged", l.ledgerRows(), rows)
	eq(t, "the column unchanged", l.holdbackOf(doc.InvoiceID), int64(10_000))
}

// RT-3 looks for a job level release on the job whose ledger holds the invoice, not only the linked one.
func TestRT3Fix2_RT3ReadsTheJobHoldingTheInvoiceNotOnlyTheLinkedOne(t *testing.T) {
	l := rt2Setup(t, "f2c")
	l.newJob(true)
	jobA, lienA := l.job, l.lien
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)
	l.rt3LegacyRelease(10_000) // the old route's rows on A
	mustExec(t, context.Background(), l.pool, `UPDATE lien_jobs SET customer_job_id = NULL WHERE id = $1::uuid`, lienA)
	lienB := l.freshLienJob()
	if code, body := l.linkOver(lienB, jobA); code != http.StatusOK {
		t.Fatalf("link a fresh job: want 200, got %d %s", code, body)
	}
	b1150, b2350, rows := l.glBalance("1150"), l.glBalance("2350"), l.ledgerRows()
	code, body := l.rt3Over("f2c-key")
	if code == http.StatusCreated || !strings.Contains(body, "job level") {
		t.Errorf("RT-3 on a holding job the old route released: want a refusal naming the job level release, got %d %s", code, body)
	}
	l.drain()
	eq(t, "1150 unchanged", l.glBalance("1150"), b1150)
	eq(t, "2350 unchanged", l.glBalance("2350"), b2350)
	eq(t, "ledger unchanged", l.ledgerRows(), rows)
	eq(t, "the column unchanged", l.holdbackOf(doc.InvoiceID), int64(10_000))
}
