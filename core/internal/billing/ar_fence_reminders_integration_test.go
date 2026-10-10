//go:build integration

// ar_fence_reminders_integration_test.go, the security review's P2-2: reminder sends and history, paper path resolve and the AP contact routes take the fence.
package billing

import (
	"context"
	"errors"
	"testing"
	"time"
)

// arKingAccount is an account that lives wholly in Kingston: home branch and its one invoice.
func arKingAccount(t *testing.T, w *arFenceWorld) (customerID, invoiceID string) {
	t.Helper()
	ctx := context.Background()
	tag := "ARK-" + randomTag()
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents, payment_terms_days)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 900000, 30) RETURNING id::text`,
		tag, "AR fence king "+tag, kingstonBranch).Scan(&customerID); err != nil {
		t.Fatalf("seed king account: %v", err)
	}
	due := d510Now.AddDate(0, 0, -150)
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, 50000, 0, 50000, 50000, $6, 'INVOICE')
		 RETURNING id::text`,
		tag+"-K", customerID, kingstonBranch, due.AddDate(0, 0, -30).Format("2006-01-02"),
		due.Format("2006-01-02"), due.AddDate(0, 0, -30)).Scan(&invoiceID); err != nil {
		t.Fatalf("seed king invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM ar_reminder_sends WHERE customer_id = $1::uuid`, customerID)
		_, _ = w.pool.Exec(c, `DELETE FROM ar_paper_path_flags WHERE customer_id = $1::uuid`, customerID)
		_, _ = w.pool.Exec(c, `DELETE FROM ar_ap_contacts WHERE customer_id = $1::uuid`, customerID)
		_, _ = w.pool.Exec(c, `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, customerID)
		_, _ = w.pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = w.pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})
	return customerID, invoiceID
}

// P2-2: the reminder and AP contact routes.
func TestARFenceReview_ReminderAndContactRoutesFollowTheReach(t *testing.T) {
	w := newARFenceWorld(t)
	kingAcct, kingInv := arKingAccount(t, w)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM ar_reminder_sends WHERE customer_id IN ($1::uuid, $2::uuid)`, w.dual, kingAcct)
		_, _ = w.pool.Exec(c, `DELETE FROM ar_ap_contacts WHERE customer_id IN ($1::uuid, $2::uuid)`, w.dual, kingAcct)
	})
	repo := NewARLandingRepository(w.pool)
	landing := NewARLandingService(repo).WithClock(func() time.Time { return d510Now })
	svc := NewARReminderService(repo, landing, StubSender{}, "https://dibbits.example").WithClock(func() time.Time { return d510Now })
	trenton := arTrentonOnly()

	// A send for an account wholly in Kingston: not found, nothing sent, no row.
	if _, err := svc.Send(trenton, SendReminderInput{CustomerID: kingAcct, Channel: ChannelEmail, ConfirmFallback: true,
		IdempotencyKey: "arr-" + randomTag()}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a reminder for another branch's account must be not found, got %v", err)
	}
	if _, err := svc.Send(trenton, SendReminderInput{CustomerID: kingAcct, InvoiceID: kingInv, Channel: ChannelEmail, ConfirmFallback: true,
		IdempotencyKey: "arr-" + randomTag()}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("naming the other branch's invoice must be not found, got %v", err)
	}
	var sends int
	_ = w.pool.QueryRow(context.Background(), `SELECT count(*) FROM ar_reminder_sends WHERE customer_id = $1::uuid`, kingAcct).Scan(&sends)
	if sends != 0 {
		t.Fatalf("%d reminder rows were written for another branch's account", sends)
	}
	// A shared account: naming the other branch's invoice is refused, and no invoice named chooses
	// among the reach's invoices only.
	if _, err := svc.Send(trenton, SendReminderInput{CustomerID: w.dual, InvoiceID: w.dualKingston, Channel: ChannelEmail, ConfirmFallback: true,
		IdempotencyKey: "arr-" + randomTag()}, ""); err == nil {
		t.Fatalf("naming the other branch's invoice on a shared account must be refused")
	}

	// History of an account in another branch reads nothing (not found when named).
	if _, err := svc.History(trenton, kingAcct, 50); !errors.Is(err, ErrNotFound) {
		t.Fatalf("history of another branch's account must be not found, got %v", err)
	}
	// And the unfiltered history carries no other branch's sends.
	if _, err := svc.TagAPContact(arGlobalPayRec(), APContact{CustomerID: kingAcct, Name: "AP", Email: "ap.king@example.test"}, ""); err != nil {
		t.Fatalf("a global caller tags the contact: %v", err)
	}
	if _, err := svc.Send(arGlobalPayRec(), SendReminderInput{CustomerID: kingAcct, Channel: ChannelEmail,
		IdempotencyKey: "arr-" + randomTag()}, ""); err != nil {
		t.Fatalf("a global caller sends: %v", err)
	}
	all, err := svc.History(trenton, "", 200)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, s := range all {
		if s.CustomerID == kingAcct {
			t.Fatalf("the unfiltered history must not carry another branch's account")
		}
	}

	// The paper path flag of another branch's account cannot be closed.
	var flagID string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO ar_paper_path_flags (customer_id, delivered_sends, window_days) VALUES ($1::uuid, 3, 30) RETURNING id::text`,
		kingAcct).Scan(&flagID); err != nil {
		t.Fatalf("seed flag: %v", err)
	}
	if err := svc.ResolvePaperPathFlag(trenton, flagID, "DISMISSED", "not ours", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("closing another branch's flag must be not found, got %v", err)
	}
	if err := svc.ResolvePaperPathFlag(arGlobalPayRec(), flagID, "DISMISSED", "ours", ""); err != nil {
		t.Fatalf("a global caller closes it: %v", err)
	}

	// The AP contact: read, tag and untag of another branch's account are not found; the caller's
	// own shared account works.
	if _, _, err := svc.APContact(trenton, kingAcct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reading another branch's AP contact must be not found, got %v", err)
	}
	if _, err := svc.TagAPContact(trenton, APContact{CustomerID: kingAcct, Name: "AP", Email: "ap@example.test"}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tagging another branch's AP contact must be not found, got %v", err)
	}
	if err := svc.UntagAPContact(trenton, kingAcct); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing another branch's AP contact must be not found, got %v", err)
	}
	if _, err := svc.TagAPContact(trenton, APContact{CustomerID: w.dual, Name: "AP", Email: "ap@example.test"}, ""); err != nil {
		t.Fatalf("tagging a shared account's AP contact: %v", err)
	}
}
