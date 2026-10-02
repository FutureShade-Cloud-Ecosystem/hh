//go:build integration

// ar_reminders_credit_memo_integration_test.go, HH-195 against the real database.
//
// An account reminder with no invoice named picks the OLDEST open document. A credit memo is an
// open document with a negative balance, so when it was the oldest the reminder dunned a credit and
// the body printed its absolute value: a customer told that CA$0.50 was due.
//
// Run: DATABASE_URL=... go test -tags integration ./internal/billing/... -run HH195 -count=1
package billing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type capturingSender struct{ msgs []ReminderMessage }

func (c *capturingSender) Name() string { return "capture" }
func (c *capturingSender) Send(_ context.Context, m ReminderMessage) (ReminderReceipt, error) {
	c.msgs = append(c.msgs, m)
	return ReminderReceipt{Provider: "capture", Ref: "x", Delivered: true}, nil
}

func TestIntegrationHH195_AccountReminderNeverDunsACreditMemo(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := NewARLandingRepository(pool)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no seeded BRANCH (migrate the gate database first): %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id)
		 VALUES ('HH195-'||$1, 'HH 195 Rig '||$1, 'CONTRACTOR', $2::uuid) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	var invID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
		                      doc_type, source_kind, closes_order)
		VALUES ('INV-195-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE - 10, CURRENT_DATE - 10,
		        12345, 0, 12345, 12345, now(), 'INVOICE', 'ORDER', FALSE) RETURNING id::text`,
		suffix, customerID, branchID).Scan(&invID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	// The credit memo is OLDER than the invoice, so the oldest-open rule lands on it.
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
		                      doc_type, source_kind, reverses_invoice_id, closes_order)
		VALUES ('CM-195-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE - 30, CURRENT_DATE - 30,
		        -50, 0, -50, -50, now(), 'CREDIT_MEMO', 'ORDER', $4::uuid, FALSE)`,
		suffix, customerID, branchID, invID); err != nil {
		t.Fatalf("seed credit memo: %v", err)
	}

	snd := &capturingSender{}
	svc := NewARReminderService(repo, NewARLandingService(repo), snd, "https://dibbits.example")
	if _, err := svc.TagAPContact(ctx, APContact{CustomerID: customerID, Name: "AP Desk", Email: "ap@hh195.example"}, ""); err != nil {
		t.Fatalf("tag AP contact: %v", err)
	}
	if _, err := svc.Send(ctx, SendReminderInput{CustomerID: customerID, Channel: ChannelEmail}, "u1"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(snd.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(snd.msgs))
	}
	body := snd.msgs[0].Body
	if strings.Contains(body, "CM-195-") || strings.Contains(body, "CA$0.50") {
		t.Errorf("reminder duns the credit memo: %q", body)
	}
	if !strings.Contains(body, "INV-195-"+suffix) || !strings.Contains(body, "CA$123.45") {
		t.Errorf("reminder does not name the invoice and its balance: %q", body)
	}
}
