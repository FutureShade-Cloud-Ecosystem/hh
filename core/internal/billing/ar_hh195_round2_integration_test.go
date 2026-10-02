//go:build integration

// ar_hh195_round2_integration_test.go, HH-195 review round 1 findings: the refusal and the escalation queue.
//
// Run: DATABASE_URL=... go test -tags integration ./internal/billing/... -run HH195R2 -count=1
package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

type hh195Rig struct {
	pool     *pgxpool.Pool
	branchID string
	suffix   string
}

func newHH195Rig(t *testing.T) *hh195Rig {
	t.Helper()
	pool := intPool(t)
	r := &hh195Rig{pool: pool, suffix: fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)}
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&r.branchID); err != nil {
		t.Fatalf("no seeded BRANCH (migrate the gate database first): %v", err)
	}
	return r
}

func (r *hh195Rig) customer(t *testing.T, tag string) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(),
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id)
		 VALUES ('H195'||$1||'-'||$2, 'HH 195 '||$1||' '||$2, 'CONTRACTOR', $3::uuid) RETURNING id::text`,
		tag, r.suffix, r.branchID).Scan(&id); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM invoices WHERE customer_id = $1::uuid`, id)
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, id)
	})
	return id
}

// doc plants one document and returns its id. cents is the balance; docType INVOICE or CREDIT_MEMO.
func (r *hh195Rig) doc(t *testing.T, customerID, no, docType, status string, cents int64, dueDaysAgo int) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
		                      doc_type, source_kind, closes_order)
		VALUES ($1||'-'||$7, $2::uuid, $3::uuid, $4, CURRENT_DATE - $5::int, CURRENT_DATE - $5::int,
		        $6, 0, $6, $6, now(), $8, 'ORDER', FALSE) RETURNING id::text`,
		no, customerID, r.branchID, status, dueDaysAgo, cents, r.suffix, docType).Scan(&id); err != nil {
		t.Fatalf("seed %s: %v", no, err)
	}
	return id
}

func (r *hh195Rig) count(t *testing.T, q, customerID string) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(), q, customerID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestIntegrationHH195R2_ReminderRefusesWhenNothingIsDue(t *testing.T) {
	rig := newHH195Rig(t)
	ctx := context.Background()
	repo := NewARLandingRepository(rig.pool)
	snd := &capturingSender{}
	svc := NewARReminderService(repo, NewARLandingService(repo), snd, "https://dibbits.example")

	empty := rig.customer(t, "E")
	creditOnly := rig.customer(t, "C")
	rig.doc(t, creditOnly, "CM-C", "CREDIT_MEMO", "FINALIZED", -5000, 40)
	mixed := rig.customer(t, "M")
	rig.doc(t, mixed, "INV-M", "INVOICE", "FINALIZED", 7000, 5)
	memoID := rig.doc(t, mixed, "CM-M", "CREDIT_MEMO", "FINALIZED", -300, 50)
	paidID := rig.doc(t, mixed, "INV-PAID", "INVOICE", "PAID", 0, 60)
	other := rig.customer(t, "O")
	otherInvID := rig.doc(t, other, "INV-O", "INVOICE", "FINALIZED", 9000, 20)

	for _, c := range []struct{ name, customer, invoice string }{
		{"nothing at all", empty, ""},
		{"credit memo only", creditOnly, ""},
		{"named credit memo", mixed, memoID},
		{"named paid invoice", mixed, paidID},
		{"named invoice of another customer", mixed, otherInvID},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.TagAPContact(ctx, APContact{CustomerID: c.customer, Name: "AP", Email: "ap@hh195.example"}, ""); err != nil {
				t.Fatalf("tag AP contact: %v", err)
			}
			before := len(snd.msgs)
			_, err := svc.Send(ctx, SendReminderInput{CustomerID: c.customer, InvoiceID: c.invoice, Channel: ChannelEmail}, "u1")
			var se *settings.StatusError
			if err == nil {
				t.Fatalf("send succeeded, want a refusal: nothing is due")
			}
			if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity || !strings.Contains(se.Msg, "no open invoice") {
				t.Errorf("err = %v, want a 422 saying there is no open invoice to remind about", err)
			}
			if len(snd.msgs) != before {
				t.Errorf("a message left the building")
			}
			if n := rig.count(t, `SELECT count(*) FROM ar_reminder_sends WHERE customer_id = $1::uuid`, c.customer); n != 0 {
				t.Errorf("%d send rows written, want 0", n)
			}
			if n := rig.count(t, `SELECT count(*) FROM ar_paper_path_flags WHERE customer_id = $1::uuid`, c.customer); n != 0 {
				t.Errorf("%d flags raised, want 0", n)
			}
		})
	}
}

func TestIntegrationHH195R2_EscalationQueueIgnoresCreditMemos(t *testing.T) {
	rig := newHH195Rig(t)
	ctx := context.Background()
	repo := NewARLandingRepository(rig.pool)
	svc := NewARLandingService(repo)

	creditOnly := rig.customer(t, "C")
	rig.doc(t, creditOnly, "CM-C", "CREDIT_MEMO", "FINALIZED", -5000, 120)
	mixed := rig.customer(t, "M")
	rig.doc(t, mixed, "CM-M", "CREDIT_MEMO", "FINALIZED", -300, 200)
	rig.doc(t, mixed, "INV-M-YOUNG", "INVOICE", "FINALIZED", 7000, 120)
	rig.doc(t, mixed, "INV-M-OLD", "INVOICE", "FINALIZED", 1000, 100)

	view, err := svc.Landing(ctx, rig.branchID, time.Time{})
	if err != nil {
		t.Fatalf("landing: %v", err)
	}
	var gotCredit bool
	var m *EscalationEntry
	for i := range view.Escalation {
		switch view.Escalation[i].CustomerID {
		case creditOnly:
			gotCredit = true
		case mixed:
			m = &view.Escalation[i]
		}
	}
	if gotCredit {
		t.Errorf("credit-only account is in the escalation queue: nobody owes us money there")
	}
	if m == nil {
		t.Fatalf("mixed account missing from the queue, it has two invoices past escalation")
	}
	if m.Invoices != 2 || m.BalanceCents != 8000 || m.OldestDays != 120 {
		t.Errorf("mixed entry = %d invoices, %d cents, oldest %d; want 2, 8000, 120 (the memo's age and balance must not count)",
			m.Invoices, m.BalanceCents, m.OldestDays)
	}
}
