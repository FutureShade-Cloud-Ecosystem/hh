//go:build integration

// FW1-09a's legs on the real migrated and seeded database: a posted journal can be reversed once only
// (the audit's SECURITY-SOD and GL-02 finding), a journal a module posted is not reversed from the ledger,
// and an account a posting rule needs cannot be deactivated (LEDGER-TIEOUT). Each leg was red on main
// before its fix; the failing line is quoted in the item's report.
package gl

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func reversalGuardService(t *testing.T, pool *pgxpool.Pool, year int) *Service {
	t.Helper()
	ensurePeriods(t, pool, year)
	user := aUser(t, pool)
	at := time.Date(year, 5, 11, 12, 0, 0, 0, time.UTC)
	return svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership}, at)
}

// A posted manual journal is refused a second reversal: the first reversal stands, and the second
// names the earlier one in plain words.
func TestIntegration_FW1_09a_AJournalCanBeReversedOnce(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := reversalGuardService(t, pool, 2094)

	orig, err := svc.CreateJournal(ctx, CreateJournalInput{Memo: "fw1-09a reversed once", Lines: []JournalLineInput{
		{AccountCode: "1000", DebitCents: 12345},
		{AccountCode: "4000", CreditCents: 12345},
	}})
	if err != nil {
		t.Fatalf("create the original: %v", err)
	}
	if _, err := svc.ReverseJournal(ctx, orig.ID); err != nil {
		t.Fatalf("the first reversal: %v", err)
	}
	_, err = svc.ReverseJournal(ctx, orig.ID)
	if err == nil {
		t.Fatal("fw1-09a red: the journal was reversed a second time today; the second reversal must be refused")
	}
	if !strings.Contains(err.Error(), "already reversed") {
		t.Fatalf("fw1-09a: the refusal must name the earlier reversal in plain words, got %v", err)
	}
}

// A journal a module posted (here the invoicing module's, through the provider seam the invoice posting
// uses) is refused from the ledger's reverse route, and the refusal names the module that owns it.
func TestIntegration_FW1_09a_ASystemJournalIsNotReversedFromTheLedger(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := reversalGuardService(t, pool, 2093)

	key := fmt.Sprintf("ar.invoice.finalized:fw1-09a-%d", time.Now().UnixNano())
	sysID, err := svc.PostExternal(ctx, ExternalJournal{
		Source:         SourceInvoice,
		IdempotencyKey: key,
		Memo:           "fw1-09a system journal",
		Lines: []ExternalLine{
			{AccountCode: "1100", DebitCents: 6789, Memo: "customer"},
			{AccountCode: "4000", CreditCents: 6789, Memo: "revenue"},
		},
	})
	if err != nil {
		t.Fatalf("post the system journal: %v", err)
	}
	_, err = svc.ReverseJournal(ctx, sysID)
	if err == nil {
		t.Fatal("fw1-09a red: a system journal was reversed from the ledger today; it must be refused")
	}
	if !strings.Contains(err.Error(), "the invoicing module") {
		t.Fatalf("fw1-09a: the refusal must name the module that owns the journal, got %v", err)
	}
}

// Deactivating an account the posting rules point at is refused, naming the rules and topics that need
// it, so the automated postings that use the account keep working.
func TestIntegration_FW1_09a_AnAccountAPostingRuleNeedsCannotBeDeactivated(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := reversalGuardService(t, pool, 2092)
	t.Cleanup(func() { _, _ = svc.store.SetAccountActive(ctx, "1100", true) })

	_, err := svc.DeactivateAccount(ctx, "1100")
	if err == nil {
		t.Fatal("fw1-09a red: account 1100 was deactivated today while posting rules point at it")
	}
	if !strings.Contains(err.Error(), "invoice.finalized") || !strings.Contains(err.Error(), "ar") {
		t.Fatalf("fw1-09a: the refusal must name the posting rule and topic that need the account, got %v", err)
	}
	j, err := svc.CreateJournal(ctx, CreateJournalInput{Memo: "fw1-09a posting still works", Lines: []JournalLineInput{
		{AccountCode: "1100", DebitCents: 500},
		{AccountCode: "4000", CreditCents: 500},
	}})
	if err != nil {
		t.Fatalf("fw1-09a: a posting on 1100 must still work after the refused deactivation: %v", err)
	}
	if j.Status != StatusPosted {
		t.Fatalf("fw1-09a: the posting on 1100 should be POSTED, got %s", j.Status)
	}
}

// Two concurrent reversals of one posted manual journal must land exactly one reversal row, and the
// second must be refused in words as already reversed (PR 621 round 2, S5 2.1). The first reversal is
// parked in a BEFORE INSERT trigger on gl_journals after its pre-insert checks; the second runs beside
// it on the shared pool.
func TestIntegration_FW1_09a_TwoConcurrentReversalsOfOneJournalLandOnlyOne(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := reversalGuardService(t, pool, 2094)

	orig, err := svc.CreateJournal(ctx, CreateJournalInput{Memo: "fw1-09a concurrent reversal race", Lines: []JournalLineInput{
		{AccountCode: "1000", DebitCents: 25000},
		{AccountCode: "4000", CreditCents: 25000},
	}})
	if err != nil {
		t.Fatalf("create the original: %v", err)
	}

	park := testkit.NewInsertPark(t, pool, "gl_journals", 55090101)
	parkSvc := reversalGuardService(t, park.Pool, 2094)

	first := make(chan error, 1)
	go func() {
		_, err := parkSvc.ReverseJournal(ctx, orig.ID)
		first <- err
	}()
	park.WaitParked("the first reversal after its check, before its gl_journals insert")

	var secondDone atomic.Bool
	var secondErr error
	second := make(chan error, 1)
	go func() {
		defer secondDone.Store(true)
		_, secondErr = svc.ReverseJournal(ctx, orig.ID)
		second <- secondErr
	}()
	park.WaitBlockedOr("the second reversal queues on the original journal row, or has already finished", secondDone.Load)

	park.AnswersAllowing(
		map[string]chan error{"the parked reversal": first, "the second reversal": second},
		map[string]string{"the second reversal": "already reversed"},
	)

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE reverses_journal_id = $1::uuid`, orig.ID).Scan(&count); err != nil {
		t.Fatalf("count reversals: %v", err)
	}
	if count != 1 {
		t.Errorf("fw1-09a red: two concurrent reversals of journal %s landed %d reversal rows, want 1", orig.ID, count)
	}
	if secondErr == nil || !strings.Contains(secondErr.Error(), "already reversed") {
		t.Fatalf("fw1-09a red: the second reversal answered %v, want it refused in words as already reversed", secondErr)
	}
}
