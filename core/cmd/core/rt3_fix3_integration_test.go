//go:build integration

package main

// rt3_fix3_integration_test.go, the RT-3 fix round 3. Item 1: RT-3 judges every lien job that bears on the
// money, not only the linked one. Item 2: a move of the link is refused like an unlink. Item 3: the
// release's audit row rides its transaction. Driven over the served mux where a door is involved.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

func (l *rt2Leg) lienNo(id string) string {
	l.t.Helper()
	var no string
	if err := l.pool.QueryRow(context.Background(), `SELECT job_no::text FROM lien_jobs WHERE id = $1::uuid`, id).Scan(&no); err != nil {
		l.t.Fatalf("read a lien job's number: %v", err)
	}
	return no
}

func (l *rt2Leg) preserveOver(lienID string) {
	l.t.Helper()
	l.asGeneralManager()
	defer l.asController()
	day := rt3Today().Format("2006-01-02")
	if code, body := l.call("POST", "/api/v1/lien/jobs/"+lienID+"/preserved-lien", `{"preserved_on":"`+day+`","note":"rt3f3"}`); code != http.StatusOK {
		l.t.Fatalf("preserve a lien: want 200, got %d %s", code, body)
	}
}

// olderLinkedBeside plants lien job C (the same customer, performed 70 days ago, no lien, created before A)
// and links it to the leg's customer job beside A, which holds the job's invoice.
func (l *rt2Leg) olderLinkedBeside(lienA string) string {
	l.t.Helper()
	c := l.freshLienJob()
	mustExec(l.t, context.Background(), l.pool, `UPDATE lien_jobs SET created_at = (SELECT created_at FROM lien_jobs WHERE id = $2::uuid) - interval '1 day' WHERE id = $1::uuid`, c, lienA)
	if code, body := l.linkOver(c, l.job); code != http.StatusOK {
		l.t.Fatalf("link a second lien job beside the first: more than one link stays allowed, want 200, got %d %s", code, body)
	}
	return c
}

func (l *rt2Leg) wantUnmoved(step string, doc billing.InvoiceDoc, b1150, b2350 [2]int64, rows int) {
	l.t.Helper()
	l.drain()
	eq(l.t, step+": 1150 unchanged", l.glBalance("1150"), b1150)
	eq(l.t, step+": 2350 unchanged", l.glBalance("2350"), b2350)
	eq(l.t, step+": ledger rows unchanged", l.ledgerRows(), rows)
	eq(l.t, step+": the column unchanged", l.holdbackOf(doc.InvoiceID), int64(10_000))
}

// P1-1 probe OlderJobLinkedBeside: A holds the invoice and carries a preserved lien; an older job C, with
// its period over, is linked beside it. RT-3 refuses, naming A.
func TestRT3Fix3_OlderJobLinkedBeside(t *testing.T) {
	l := rt2Setup(t, "f3a")
	l.newJob(true)
	lienA := l.lien
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(10)
	l.preserveOver(lienA)
	l.olderLinkedBeside(lienA)
	b1150, b2350, rows := l.glBalance("1150"), l.glBalance("2350"), l.ledgerRows()

	code, body := l.rt3Over("f3a-key")
	if code != http.StatusConflict || !strings.Contains(body, l.lienNo(lienA)) {
		t.Errorf("RT-3 with a lien preserved on the job holding the invoice: want 409 naming %s, got %d %s", l.lienNo(lienA), code, body)
	}
	l.wantUnmoved("preserved lien beside an older job", doc, b1150, b2350, rows)
}

// The same, with no lien: A's period has not run out while C's has. RT-3 refuses, naming A.
func TestRT3Fix3_OlderJobLinkedBesideWhosePeriodRuns(t *testing.T) {
	l := rt2Setup(t, "f3b")
	l.newJob(true)
	lienA := l.lien
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(10)
	l.olderLinkedBeside(lienA)
	b1150, b2350, rows := l.glBalance("1150"), l.glBalance("2350"), l.ledgerRows()

	code, body := l.rt3Over("f3b-key")
	if code != http.StatusConflict || !strings.Contains(body, l.lienNo(lienA)) {
		t.Errorf("RT-3 with a period still running on the job holding the invoice: want 409 naming %s, got %d %s", l.lienNo(lienA), code, body)
	}
	l.wantUnmoved("running period beside an older job", doc, b1150, b2350, rows)

	// The worklist and the release agree: the row is not due and says why.
	rowsDue, err := holdBillingRepo(l.pool).HoldbackDueToRelease(context.Background(), billing.HoldbackDueQuery{Now: time.Now()})
	if err != nil {
		t.Fatalf("the due read: %v", err)
	}
	for _, r := range rowsDue {
		if r.InvoiceID == doc.InvoiceID && (r.Due || !strings.Contains(r.BlockedReason, l.lienNo(lienA))) {
			t.Errorf("the worklist must agree with the release: not due, naming %s, got due=%v %q", l.lienNo(lienA), r.Due, r.BlockedReason)
		}
	}
}

// P1-1 probe MoveSkipsThePreservedLien: moving A's link away is refused (P2-1), and so a fresh B linked
// to the job cannot stand in for A. RT-3 still refuses, naming A.
func TestRT3Fix3_MoveSkipsThePreservedLien(t *testing.T) {
	l := rt2Setup(t, "f3c")
	l.newJob(true)
	lienA, jobX := l.lien, l.job
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(10)
	l.preserveOver(lienA)
	jobY, lienY := l.secondLinkedJob()
	_ = lienY
	l.job = jobX
	b1150, b2350, rows := l.glBalance("1150"), l.glBalance("2350"), l.ledgerRows()

	if code, body := l.rt3Over("f3c-first"); code != http.StatusConflict {
		t.Fatalf("RT-3 first: want 409, got %d %s", code, body)
	}
	if code, body := l.linkOver(lienA, jobY); code != http.StatusUnprocessableEntity || !strings.Contains(body, "retention") {
		t.Errorf("a move of the link while the ledger holds invoice level retention: want 422 in words, got %d %s", code, body)
	}
	var link string
	if err := l.pool.QueryRow(context.Background(), `SELECT COALESCE(customer_job_id::text, '') FROM lien_jobs WHERE id = $1::uuid`, lienA).Scan(&link); err != nil {
		t.Fatalf("read the link: %v", err)
	}
	if link != jobX {
		t.Errorf("the refused move must leave the link: want %s, got %s", jobX, link)
	}
	// A fresh job linked to X, with its period over, does not stand in for A.
	l.olderLinkedBeside(lienA)
	code, body := l.rt3Over("f3c-second")
	if code != http.StatusConflict || !strings.Contains(body, l.lienNo(lienA)) {
		t.Errorf("RT-3 after a fresh job is linked: want 409 naming %s, got %d %s", l.lienNo(lienA), code, body)
	}
	l.wantUnmoved("move then fresh link", doc, b1150, b2350, rows)
}

// P2-1: a move to another customer job is refused like an unlink, and the link stays.
func TestRT3Fix3_AMoveIsRefusedLikeAnUnlink(t *testing.T) {
	l := rt2Setup(t, "f3d")
	l.newJob(true)
	lienA, jobX := l.lien, l.job
	l.invoice(100_000, 0)
	jobY, _ := l.secondLinkedJob()

	code, body := l.linkOver(lienA, jobY)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "holds invoice level retention") {
		t.Errorf("a move: want the unlink's refusal, got %d %s", code, body)
	}
	var link string
	if err := l.pool.QueryRow(context.Background(), `SELECT COALESCE(customer_job_id::text, '') FROM lien_jobs WHERE id = $1::uuid`, lienA).Scan(&link); err != nil {
		t.Fatalf("read the link: %v", err)
	}
	if link != jobX {
		t.Errorf("the link must be unchanged: want %s, got %s", jobX, link)
	}
	// Linking the job it already has stays a no change, not a refusal.
	if code, body := l.linkOver(lienA, jobX); code != http.StatusOK {
		t.Errorf("re-linking the same job: want 200, got %d %s", code, body)
	}
}

// P3-1: the release and its audit row are one transaction; a failing audit write leaves no release row, no
// GL entry and the column unchanged.
func TestRT3Fix3_AReleaseWhoseAuditFailsRollsBack(t *testing.T) {
	l := rt2Setup(t, "f3e")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)
	b1150, b1100, rows := l.glBalance("1150"), l.glBalance("1100"), l.ledgerRows()

	svc := billing.NewHoldbackReleaseService(holdBillingRepo(l.pool)).WithAudit(failingAudit{})
	if _, err := svc.Release(context.Background(), l.rt3Input("f3e-key")); err == nil {
		t.Fatalf("a release whose audit write fails must fail")
	}
	l.drain()
	eq(t, "no release row", l.rt3Releases(doc.InvoiceID), 0)
	eq(t, "1150 unchanged", l.glBalance("1150"), b1150)
	eq(t, "1100 unchanged", l.glBalance("1100"), b1100)
	eq(t, "ledger rows unchanged", l.ledgerRows(), rows)
	eq(t, "the column unchanged", l.holdbackOf(doc.InvoiceID), int64(10_000))
	l.ties("after the failed release")
}
