//go:build integration

// receivables_integration_test.go proves the open receivables door
// (HH-LOAD-1 LD-11, CP0-6 answer B) against the real billing tables and
// the real load frame: an invented fixture loads as imported documents
// with their own numbers and dates, the receivables ledger reads in
// date order per customer with true running balances, the aging buckets
// survive, unapplied cash lands as a negative credit item, a batch is
// all or nothing (one refused item refuses it and nothing lands), a
// replayed batch id answers its first outcomes and writes nothing, a
// changed item under a played batch id refuses, the cross door replay
// refuses, a contended batch answers ErrBatchContended with the
// resubmission converging, and a load writes no journal, no finalized
// event, no customer message, no reminder and no credit sweep row
// anywhere. Every fixture is invented; no real dealer value appears.
//
// The file lives in the external test package so it can drive billing's
// own writer beside the frame without an import cycle.
//
// Run (the throwaway database URL rides the command line, never a
// default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5627/ld11_gate?sslmode=disable' \
//	  go test -tags integration ./internal/load/... -count=1 -v
package load_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// The fixture's invented ids: an org no other row points at, two
// branches, two customers, one machine principal as the audit actor.
// The source system names this suite alone so the registry slice cleans
// up whole.
const (
	ld11OrgID     = "6a110000-0000-4000-8000-0000000000a1"
	ld11BranchAID = "6a110000-0000-4000-8000-0000000000b1"
	ld11BranchBID = "6a110000-0000-4000-8000-0000000000b2"
	ld11CustOneID = "6a110000-0000-4000-8000-0000000000c1"
	ld11CustTwoID = "6a110000-0000-4000-8000-0000000000c2"
	ld11ActorID   = "6a110000-0000-4000-8000-0000000000d1"
	ld11Source    = "ld11suite"

	// Registry fixture hashes: 64 hex characters, the shape 0320 checks.
	// The values themselves mean nothing; the names they record are what
	// the door resolves.
	ld11CustOneHash = "aa1100000000000000000000000000000000000000000000000000000000c001"
	ld11CustTwoHash = "aa1100000000000000000000000000000000000000000000000000000000c002"
	ld11BranchAHash = "aa1100000000000000000000000000000000000000000000000000000000b001"
	ld11BranchBHash = "aa1100000000000000000000000000000000000000000000000000000000b002"
)

// ld11Pool opens the suite's pool off DATABASE_URL and skips politely
// when the gate variable is absent.
func ld11Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// ld11Env is one test's fixtures: the pool, the planted rows, the store
// and the receivables door over billing's own writer.
type ld11Env struct {
	pool  *pgxpool.Pool
	store *load.Store
	door  *load.ReceivablesDoor
	audit *ld11AuditSink
}

// setupLD11 plants the org, branches, customers and their registry
// names, builds the door over billing's writer, and undoes all of it at
// the end.
func setupLD11(t *testing.T) *ld11Env {
	t.Helper()
	pool := ld11Pool(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO org (id, slug, name) VALUES ('` + ld11OrgID + `', 'ld11-fixture', 'LD11 Fixture Yard')
			ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address, org_id) VALUES
			('` + ld11BranchAID + `', '` + ld11BranchAID + `', NULL, 'BRANCH', 'LD11-A', 'LD11 Fixture Branch A', '{}', '` + ld11OrgID + `'),
			('` + ld11BranchBID + `', '` + ld11BranchBID + `', NULL, 'BRANCH', 'LD11-B', 'LD11 Fixture Branch B', '{}', '` + ld11OrgID + `')
			ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO customers (id, account_no, name, customer_type, home_branch_id, org_id) VALUES
			('` + ld11CustOneID + `', 'LD11-FX-1', 'LD11 Fixture Contractor One', 'CONTRACTOR', '` + ld11BranchAID + `', '` + ld11OrgID + `'),
			('` + ld11CustTwoID + `', 'LD11-FX-2', 'LD11 Fixture Contractor Two', 'CONTRACTOR', '` + ld11BranchAID + `', '` + ld11OrgID + `')
			ON CONFLICT (id) DO NOTHING`,
		// The registry names the customer and branch doors would have
		// recorded: this suite owns the receivables door, so the names
		// land directly, with the hashes 0320's check demands.
		`INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash, last_batch_id) VALUES
			('` + ld11OrgID + `', '` + ld11Source + `', 'customer', 'CUST-ONE', '` + ld11CustOneID + `', '` + ld11CustOneHash + `', 'fixture'),
			('` + ld11OrgID + `', '` + ld11Source + `', 'customer', 'CUST-TWO', '` + ld11CustTwoID + `', '` + ld11CustTwoHash + `', 'fixture'),
			('` + ld11OrgID + `', '` + ld11Source + `', 'branch', 'BR-A', '` + ld11BranchAID + `', '` + ld11BranchAHash + `', 'fixture'),
			('` + ld11OrgID + `', '` + ld11Source + `', 'branch', 'BR-B', '` + ld11BranchBID + `', '` + ld11BranchBHash + `', 'fixture')
			ON CONFLICT DO NOTHING`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup: %v (statement %.80s...)", err, stmt)
		}
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM customer_transactions WHERE source_ref_id IN (SELECT id FROM invoices WHERE org_id = '` + ld11OrgID + `'::uuid AND source_kind = 'IMPORT')`,
			`DELETE FROM invoices WHERE org_id = '` + ld11OrgID + `'::uuid AND source_kind = 'IMPORT'`,
			`DELETE FROM load_key_registry WHERE org_id = '` + ld11OrgID + `'::uuid`,
			`DELETE FROM load_batch WHERE org_id = '` + ld11OrgID + `'::uuid`,
			`DELETE FROM customers WHERE id IN ('` + ld11CustOneID + `', '` + ld11CustTwoID + `')`,
			`DELETE FROM locations WHERE id IN ('` + ld11BranchAID + `', '` + ld11BranchBID + `')`,
			`DELETE FROM org WHERE id = '` + ld11OrgID + `'::uuid`,
		} {
			_, _ = pool.Exec(ctx, stmt)
		}
		pool.Close()
	})
	store := load.NewStore(pool)
	audit := &ld11AuditSink{}
	return &ld11Env{
		pool:  pool,
		store: store,
		door:  load.NewReceivablesDoor(store, billing.NewReceivablesLoadWriter(), audit),
		audit: audit,
	}
}

// ld11AuditSink captures the batch audit rows the frame writes.
type ld11AuditSink struct {
	mu   sync.Mutex
	rows []load.BatchAuditRow
}

func (s *ld11AuditSink) RecordBatchTx(_ context.Context, _ pgx.Tx, row load.BatchAuditRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, row)
	return nil
}

func (s *ld11AuditSink) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

// ld11Item builds one receivable item: money as the wire hands it
// (json.Number under the hood, an exact integer here).
func ld11Item(legacyID, customer, branch, docNo string, issue, due string, cents int64, job, address string) load.Item {
	rec := map[string]any{
		"source_doc_no":     docNo,
		"issue_date":        issue,
		"due_date":          due,
		"open_amount_cents": cents,
	}
	if job != "" {
		rec["source_job_key"] = job
	}
	if address != "" {
		rec["source_address_key"] = address
	}
	return load.Item{
		LegacyID: legacyID,
		Record:   rec,
		Requires: []load.Ref{
			{Kind: load.RefKindCustomer, LegacyID: customer},
			{Kind: load.RefKindBranch, LegacyID: branch},
		},
	}
}

// ld11Batch wraps the items in the batch shape.
func ld11Batch(batchID string, items ...load.Item) load.Batch {
	return load.Batch{OrgID: ld11OrgID, SourceSystem: ld11Source, BatchID: batchID, Caller: ld11ActorID, Items: items}
}

// ld11Count reads one scalar count.
func (e *ld11Env) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// ld11Invoice is one imported document as the proof reads it.
type ld11Invoice struct {
	ID         string
	InvoiceNo  string
	DocType    string
	SourceKind string
	SourceDoc  string
	Customer   string
	Status     string
	IssueDate  string
	DueDate    string
	Subtotal   int64
	Tax        int64
	Total      int64
	Balance    int64
	OrgID      string
	Seq        int64
	Closes     bool
	Finalized  bool
	JobKey     string
	AddressKey string
}

// ld11Invoices reads the org's imported documents by source number.
func (e *ld11Env) invoices(t *testing.T) map[string]ld11Invoice {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `
		SELECT id::text, invoice_no::text, doc_type, source_kind, coalesce(source_doc_no,''),
		       customer_id::text, status, to_char(issue_date,'YYYY-MM-DD'), to_char(due_date,'YYYY-MM-DD'),
		       subtotal_cents, tax_cents, total_cents, balance_cents, coalesce(org_id::text,''),
		       coalesce(seq,0), closes_order, (finalized_at IS NOT NULL),
		       coalesce(metadata->>'load_source_job_key',''), coalesce(metadata->>'load_source_address_key','')
		FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'
		ORDER BY source_doc_no`, ld11OrgID)
	if err != nil {
		t.Fatalf("invoices read: %v", err)
	}
	defer rows.Close()
	out := map[string]ld11Invoice{}
	for rows.Next() {
		var inv ld11Invoice
		if err := rows.Scan(&inv.ID, &inv.InvoiceNo, &inv.DocType, &inv.SourceKind, &inv.SourceDoc,
			&inv.Customer, &inv.Status, &inv.IssueDate, &inv.DueDate,
			&inv.Subtotal, &inv.Tax, &inv.Total, &inv.Balance, &inv.OrgID,
			&inv.Seq, &inv.Closes, &inv.Finalized, &inv.JobKey, &inv.AddressKey); err != nil {
			t.Fatalf("invoice scan: %v", err)
		}
		out[inv.SourceDoc] = inv
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("invoices walk: %v", err)
	}
	return out
}

// ld11LedgerRow is one receivables ledger row as the proof reads it.
type ld11LedgerRow struct {
	SourceDoc    string
	TxnType      string
	Amount       int64
	BalanceAfter int64
	OccurredAt   time.Time
	Memo         string
}

// ld11Ledger reads one customer's imported ledger rows oldest first,
// each row named by its document's source number.
func (e *ld11Env) ledger(t *testing.T, customerID string) []ld11LedgerRow {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `
		SELECT coalesce(i.source_doc_no,''), ct.txn_type, ct.amount_cents, ct.balance_after, ct.occurred_at, ct.memo
		FROM customer_transactions ct
		JOIN invoices i ON i.id = ct.source_ref_id
		WHERE ct.customer_id = $1::uuid
		  AND ct.source_type = 'invoice'
		  AND i.org_id = $2::uuid AND i.source_kind = 'IMPORT'
		ORDER BY ct.occurred_at ASC, ct.created_at ASC`, customerID, ld11OrgID)
	if err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	defer rows.Close()
	var out []ld11LedgerRow
	for rows.Next() {
		var r ld11LedgerRow
		if err := rows.Scan(&r.SourceDoc, &r.TxnType, &r.Amount, &r.BalanceAfter, &r.OccurredAt, &r.Memo); err != nil {
			t.Fatalf("ledger scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("ledger walk: %v", err)
	}
	return out
}

// ld11BatchRefused asserts the error is the batch level refusal and
// answers its outcome.
func ld11BatchRefused(t *testing.T, err error) *load.BatchRefusal {
	t.Helper()
	var ref *load.BatchRefusal
	if err == nil || !errors.As(err, &ref) {
		t.Fatalf("the batch answered %v, want a batch refusal", err)
	}
	return ref
}

// TestReceivablesBatchLandsInDateOrderWithAgingSurviving is the exit
// line's proof: the fixture's open items land as imported documents
// with their own numbers and dates, the ledger reads in date order per
// customer with true running balances, the per customer and total
// balances equal the fixture to the cent, unapplied cash lands as a
// negative credit item, and the aging buckets carry the fixture's due
// dates.
func TestReceivablesBatchLandsInDateOrderWithAgingSurviving(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	asOf := time.Now().UTC()
	day := 24 * time.Hour
	fmtDate := func(tm time.Time) string { return tm.Format("2006-01-02") }
	// Customer one carries three open items whose due dates sit one to
	// a bucket: current, the first past bucket, the second. Customer two
	// carries one negative item, the unapplied cash shape.
	items := []load.Item{
		ld11Item("AR-MID", "CUST-ONE", "BR-A", "AR-2002", fmtDate(asOf.Add(-20*day)), fmtDate(asOf.Add(-10*day)), 40000, "", ""),
		ld11Item("AR-OLD", "CUST-ONE", "BR-A", "AR-2001", fmtDate(asOf.Add(-60*day)), fmtDate(asOf.Add(-45*day)), 70000, "JOB-4021", "ADDR-4021"),
		ld11Item("AR-NOW", "CUST-ONE", "BR-B", "AR-2003", fmtDate(asOf.Add(-5*day)), fmtDate(asOf.Add(10*day)), 10000, "", ""),
		ld11Item("AR-CASH", "CUST-TWO", "BR-A", "CM-3001", fmtDate(asOf.Add(-15*day)), fmtDate(asOf.Add(-9*day)), -2500, "", ""),
	}
	r, err := e.door.UpsertReceivables(ctx, ld11Batch("b-land-1", items...))
	if err != nil {
		t.Fatalf("the batch refused: %v", err)
	}
	// The outcomes answer in the wire's order, one created per item.
	for i, want := range []string{"AR-MID", "AR-OLD", "AR-NOW", "AR-CASH"} {
		if r.Items[i].LegacyID != want || r.Items[i].Status != load.OutcomeCreated {
			t.Fatalf("outcome %d is %+v, want %s created", i, r.Items[i], want)
		}
		if r.Items[i].HHID == "" {
			t.Fatalf("outcome %d carries no hh id", i)
		}
	}
	if r.Counts[load.OutcomeCreated] != 4 {
		t.Fatalf("counts %+v, want four created", r.Counts)
	}

	invoices := e.invoices(t)
	if len(invoices) != 4 {
		t.Fatalf("the batch landed %d imported documents, want four", len(invoices))
	}
	wantDoc := map[string]struct {
		docType  string
		amount   int64
		noPrefix string
	}{
		"AR-2001": {"INVOICE", 70000, "#INV-"},
		"AR-2002": {"INVOICE", 40000, "#INV-"},
		"AR-2003": {"INVOICE", 10000, "#INV-"},
		"CM-3001": {"CREDIT_MEMO", -2500, "#CM-"},
	}
	for docNo, want := range wantDoc {
		inv, ok := invoices[docNo]
		if !ok {
			t.Fatalf("the document %s did not land", docNo)
		}
		if inv.DocType != want.docType || inv.SourceKind != "IMPORT" || inv.Status != "FINALIZED" || !inv.Finalized {
			t.Fatalf("%s landed as %s %s %s finalized=%v, want an imported finalized %s",
				docNo, inv.SourceKind, inv.DocType, inv.Status, inv.Finalized, want.docType)
		}
		if inv.Subtotal != want.amount || inv.Total != want.amount || inv.Balance != want.amount {
			t.Fatalf("%s carries subtotal %d total %d balance %d, want the carried open amount %d in every money column",
				docNo, inv.Subtotal, inv.Total, inv.Balance, want.amount)
		}
		if inv.Tax != 0 {
			t.Fatalf("%s carries tax %d, want none: the composition is not carried, the amount is", docNo, inv.Tax)
		}
		if inv.OrgID != ld11OrgID {
			t.Fatalf("%s carries org %s, want the machine principal's org", docNo, inv.OrgID)
		}
		if inv.Seq == 0 {
			t.Fatalf("%s drew no invoices feed sequence", docNo)
		}
		if inv.Closes {
			t.Fatalf("%s closes an order, but no HH order exists behind an import", docNo)
		}
		if len(inv.InvoiceNo) < 5 || !strings.HasPrefix(inv.InvoiceNo, want.noPrefix) {
			t.Fatalf("%s carries HH number %q, want a minted %s number", docNo, inv.InvoiceNo, want.noPrefix)
		}
	}
	// The dates land as recorded, and the carried job and address keys
	// ride on the record.
	if invoices["AR-2001"].IssueDate != fmtDate(asOf.Add(-60*day)) || invoices["AR-2001"].DueDate != fmtDate(asOf.Add(-45*day)) {
		t.Fatalf("AR-2001 dates %s to %s, want the recorded ones", invoices["AR-2001"].IssueDate, invoices["AR-2001"].DueDate)
	}
	if invoices["AR-2001"].JobKey != "JOB-4021" || invoices["AR-2001"].AddressKey != "ADDR-4021" {
		t.Fatalf("AR-2001 carries job %q address %q, want the carried legacy keys", invoices["AR-2001"].JobKey, invoices["AR-2001"].AddressKey)
	}
	if invoices["AR-2003"].Customer != ld11CustTwoID && invoices["AR-2003"].Customer != ld11CustOneID {
		t.Fatalf("AR-2003 carries customer %s", invoices["AR-2003"].Customer)
	}
	if invoices["CM-3001"].Customer != ld11CustTwoID {
		t.Fatalf("the credit item carries customer %s, want the second fixture customer", invoices["CM-3001"].Customer)
	}

	// The ledger reads in date order per customer with true running
	// balances: customer one's three rows oldest first, each balance
	// the running sum.
	ledger := e.ledger(t, ld11CustOneID)
	if len(ledger) != 3 {
		t.Fatalf("customer one carries %d ledger rows, want one per item", len(ledger))
	}
	wantOrder := []struct {
		doc     string
		amount  int64
		running int64
	}{
		{"AR-2001", 70000, 70000},
		{"AR-2002", 40000, 110000},
		{"AR-2003", 10000, 120000},
	}
	for i, want := range wantOrder {
		row := ledger[i]
		if row.TxnType != "INVOICE" || row.Amount != want.amount || row.BalanceAfter != want.running {
			t.Fatalf("ledger row %d is %+v, want an INVOICE of %d with the running balance %d (date order per customer)",
				i, row, want.amount, want.running)
		}
	}
	cash := e.ledger(t, ld11CustTwoID)
	if len(cash) != 1 || cash[0].TxnType != "CREDIT_MEMO" || cash[0].Amount != -2500 || cash[0].BalanceAfter != -2500 {
		t.Fatalf("customer two's ledger is %+v, want one CREDIT_MEMO row of -2500 with the running balance -2500", cash)
	}

	// The per customer and in total balances equal the fixture to the
	// cent, read the way every AR read reads them.
	var openOne, openTwo int64
	if err := e.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		ld11CustOneID).Scan(&openOne); err != nil {
		t.Fatalf("open balance read: %v", err)
	}
	if err := e.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		ld11CustTwoID).Scan(&openTwo); err != nil {
		t.Fatalf("open balance read: %v", err)
	}
	if openOne != 120000 || openTwo != -2500 || openOne+openTwo != 117500 {
		t.Fatalf("open balances %d and %d, want 120000 and -2500 to the cent", openOne, openTwo)
	}

	// The aging buckets survive: one item to a bucket by its recorded
	// due date.
	repo := billing.NewRepository(e.pool)
	aging, err := repo.AgingRows(ctx, asOf, 30, "")
	if err != nil {
		t.Fatalf("aging read: %v", err)
	}
	var one *billing.AgingRow
	for i := range aging {
		if aging[i].CustomerID == ld11CustOneID {
			one = &aging[i]
		}
	}
	if one == nil {
		t.Fatalf("the aging worklist carries no row for the fixture customer: %+v", aging)
	}
	if one.CurrentCents != 10000 || one.Days30Cents != 40000 || one.Days60Cents != 70000 || one.Days90PlusCents != 0 {
		t.Fatalf("aging buckets current %d, 30 %d, 60 %d, 90+ %d, want 10000, 40000, 70000, 0",
			one.CurrentCents, one.Days30Cents, one.Days60Cents, one.Days90PlusCents)
	}
	if one.TotalCents != 120000 {
		t.Fatalf("aging total %d, want 120000", one.TotalCents)
	}

	// One audit row per batch, from the frame, with the counts.
	if e.audit.len() != 1 {
		t.Fatalf("%d audit rows stand, want the frame's one per batch", e.audit.len())
	}
	if row := e.audit.rows[0]; row.ActorID != ld11ActorID || row.Counts[load.OutcomeCreated] != 4 || row.Kind != load.ReceivablesKind {
		t.Fatalf("the audit row carries %+v, want the principal as actor and four created", row)
	}
}

// TestReceivablesBatchIsAllOrNothing: one refused item refuses the
// batch and nothing lands, and the corrected batch lands under the same
// id because the refused batch recorded nothing.
func TestReceivablesBatchIsAllOrNothing(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	good := ld11Item("AR-OK-1", "CUST-ONE", "BR-A", "AR-5001", "2021-06-01", "2021-07-01", 90000, "", "")
	goodTwo := ld11Item("AR-OK-2", "CUST-TWO", "BR-A", "AR-5002", "2021-06-02", "2021-07-02", 6000, "", "")
	// The refusing item: a customer name no load recorded.
	bad := ld11Item("AR-BAD-1", "CUST-NONE", "BR-A", "AR-5003", "2021-06-03", "2021-07-03", 1000, "", "")

	_, err := e.door.UpsertReceivables(ctx, ld11Batch("b-atomic-1", good, bad, goodTwo))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.LegacyID != "AR-BAD-1" || ref.Refusing.Reason != load.ReasonMissingReference {
		t.Fatalf("the refusal names %+v, want the unresolvable item", ref.Refusing)
	}
	if ref.Refusing.Field != load.RefKindCustomer {
		t.Fatalf("the refusal field is %q, want the customer kind", ref.Refusing.Field)
	}
	// Nothing landed: no document, no ledger row, no batch record, no
	// registry name, no audit row.
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != 0 {
		t.Fatalf("the refused batch landed %d documents, want none", n)
	}
	if n := e.count(t, `SELECT count(*) FROM customer_transactions WHERE customer_id = ANY($1::uuid[])`, []string{ld11CustOneID, ld11CustTwoID}); n != 0 {
		t.Fatalf("the refused batch landed %d ledger rows, want none", n)
	}
	if n := e.count(t, `SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND source_system = $2`, ld11OrgID, ld11Source); n != 0 {
		t.Fatalf("the refused batch recorded %d batch rows, want none", n)
	}
	if n := e.count(t, `SELECT count(*) FROM load_key_registry WHERE org_id = $1::uuid AND kind = $2`, ld11OrgID, load.ReceivablesKind); n != 0 {
		t.Fatalf("the refused batch recorded %d names, want none", n)
	}
	if e.audit.len() != 0 {
		t.Fatalf("the refused batch landed %d audit rows, want none", e.audit.len())
	}

	// The corrected batch lands under the same id: the refusal recorded
	// nothing, so the id was never played.
	r, err := e.door.UpsertReceivables(ctx, ld11Batch("b-atomic-1", good, goodTwo))
	if err != nil {
		t.Fatalf("the corrected batch refused: %v", err)
	}
	if r.Counts[load.OutcomeCreated] != 2 {
		t.Fatalf("the corrected batch counts %+v, want two created", r.Counts)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != 2 {
		t.Fatalf("the corrected batch landed %d documents, want two", n)
	}
}

// TestReceivablesReplayAnswersFirstOutcomes: a replayed batch id
// returns its first outcomes and writes nothing; a changed item under
// the played id refuses; the cross door replay refuses.
func TestReceivablesReplayAnswersFirstOutcomes(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	items := []load.Item{
		ld11Item("AR-R-1", "CUST-ONE", "BR-A", "AR-6001", "2021-06-01", "2021-07-01", 30000, "", ""),
		ld11Item("AR-R-2", "CUST-TWO", "BR-A", "AR-6002", "2021-06-02", "2021-07-02", 5000, "", ""),
	}
	first, err := e.door.UpsertReceivables(ctx, ld11Batch("b-replay-1", items...))
	if err != nil {
		t.Fatalf("first submission: %v", err)
	}
	rowsBefore := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID)
	seqBefore := e.count(t, `SELECT coalesce(max(seq),0) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID)

	// The faithful replay: same items, same id.
	second, err := e.door.UpsertReceivables(ctx, ld11Batch("b-replay-1", items...))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(second.Items) != len(first.Items) {
		t.Fatalf("the replay answered %d outcomes, want the first %d", len(second.Items), len(first.Items))
	}
	for i := range first.Items {
		if second.Items[i] != first.Items[i] {
			t.Fatalf("replay outcome %d is %+v, want the first submission's %+v", i, second.Items[i], first.Items[i])
		}
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != rowsBefore {
		t.Fatalf("the replay wrote rows: %d, want %d", n, rowsBefore)
	}
	if n := e.count(t, `SELECT coalesce(max(seq),0) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != seqBefore {
		t.Fatalf("the replay drew sequences: max seq %d, want %d", n, seqBefore)
	}
	if e.audit.len() != 1 {
		t.Fatalf("the replay landed %d audit rows, want the one from the first submission", e.audit.len())
	}

	// A changed item under the played id refuses and writes nothing.
	changed := ld11Item("AR-R-1", "CUST-ONE", "BR-A", "AR-6001", "2021-06-01", "2021-07-01", 31000, "", "")
	_, err = e.door.UpsertReceivables(ctx, ld11Batch("b-replay-1", changed, items[1]))
	ref := ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "AR-R-1" {
		t.Fatalf("the changed replay answered %+v, want %s naming AR-R-1", ref.Refusing, load.ReasonReplayChanged)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE org_id = $1::uuid AND source_kind = 'IMPORT'`, ld11OrgID); n != rowsBefore {
		t.Fatalf("the changed replay wrote rows: %d, want %d", n, rowsBefore)
	}
	// An item the played batch never carried is the same refusal.
	extra := ld11Item("AR-R-9", "CUST-ONE", "BR-A", "AR-6009", "2021-06-09", "2021-07-09", 900, "", "")
	_, err = e.door.UpsertReceivables(ctx, ld11Batch("b-replay-1", items[0], items[1], extra))
	ref = ld11BatchRefused(t, err)
	if ref.Refusing.Reason != load.ReasonReplayChanged || ref.Refusing.LegacyID != "AR-R-9" {
		t.Fatalf("the extra item replay answered %+v, want %s naming AR-R-9", ref.Refusing, load.ReasonReplayChanged)
	}

	// The cross door replay refuses: the batch id is one door's.
	otherDoor := load.NewDoor(e.store, "probe-other", ld11RefusingWriter{}, e.audit)
	_, err = otherDoor.UpsertBatch(ctx, load.Batch{OrgID: ld11OrgID, SourceSystem: ld11Source,
		BatchID: "b-replay-1", Caller: ld11ActorID,
		Items: []load.Item{{LegacyID: "AR-R-1", Record: map[string]any{"payload": "x"}}}})
	if !errors.Is(err, load.ErrBatchDoorMismatch) {
		t.Fatalf("the cross door replay answered %v, want ErrBatchDoorMismatch", err)
	}
}

// TestReceivablesUpdateArmMovesThatRowAlone: a changed item under a new
// batch id updates its row alone, its ledger row with it, and the
// customer's total moves by exactly the change.
func TestReceivablesUpdateArmMovesThatRowAlone(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	first := []load.Item{
		ld11Item("AR-U-1", "CUST-ONE", "BR-A", "AR-7001", "2021-06-01", "2021-07-01", 50000, "", ""),
		ld11Item("AR-U-2", "CUST-ONE", "BR-A", "AR-7002", "2021-06-05", "2021-07-05", 20000, "", ""),
	}
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-upd-1", first...)); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	before := e.invoices(t)
	ledgerBefore := len(e.ledger(t, ld11CustOneID))
	var seqOneBefore, seqTwoBefore int64
	for _, inv := range before {
		if inv.SourceDoc == "AR-7001" {
			seqOneBefore = inv.Seq
		}
		if inv.SourceDoc == "AR-7002" {
			seqTwoBefore = inv.Seq
		}
	}

	// The open amount moved on one item: the source says the customer
	// paid part of it between extracts.
	changed := ld11Item("AR-U-1", "CUST-ONE", "BR-A", "AR-7001", "2021-06-01", "2021-07-01", 44000, "", "")
	r, err := e.door.UpsertReceivables(ctx, ld11Batch("b-upd-2", changed, first[1]))
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if o := r.Items[0]; o.Status != load.OutcomeUpdated || o.LegacyID != "AR-U-1" {
		t.Fatalf("the changed item answered %+v, want updated", o)
	}
	if o := r.Items[1]; o.Status != load.OutcomeUnchanged {
		t.Fatalf("the untouched item answered %+v, want unchanged", o)
	}
	after := e.invoices(t)
	if len(after) != 2 {
		t.Fatalf("the update arm landed %d documents, want the same two", len(after))
	}
	if after["AR-7001"].Balance != 44000 || after["AR-7001"].Total != 44000 {
		t.Fatalf("the changed row carries balance %d total %d, want 44000", after["AR-7001"].Balance, after["AR-7001"].Total)
	}
	if after["AR-7002"].Balance != 20000 {
		t.Fatalf("the untouched row carries balance %d, want 20000", after["AR-7002"].Balance)
	}
	if after["AR-7001"].ID != before["AR-7001"].ID {
		t.Fatalf("the update arm minted a second row for the same name")
	}
	if after["AR-7001"].Seq <= seqOneBefore {
		t.Fatalf("the changed row drew no new sequence: %d then %d", seqOneBefore, after["AR-7001"].Seq)
	}
	if after["AR-7002"].Seq != seqTwoBefore {
		t.Fatalf("the untouched row moved its sequence: %d then %d", seqTwoBefore, after["AR-7002"].Seq)
	}
	// One ledger row per item still, the customer's total moved by the
	// change, and the final running balance is the new total.
	ledger := e.ledger(t, ld11CustOneID)
	if len(ledger) != ledgerBefore {
		t.Fatalf("the update arm left %d ledger rows, want %d (one per item)", len(ledger), ledgerBefore)
	}
	var open int64
	if err := e.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		ld11CustOneID).Scan(&open); err != nil {
		t.Fatalf("open balance read: %v", err)
	}
	if open != 64000 {
		t.Fatalf("the customer's open balance is %d, want 64000 after the change", open)
	}
	if ledger[len(ledger)-1].BalanceAfter != open {
		t.Fatalf("the last running balance is %d, want the customer's open total %d", ledger[len(ledger)-1].BalanceAfter, open)
	}
}

// TestReceivablesLoadTriggersNothingDownstream: no journal, no
// finalized event, no customer message, no reminder, no statement, no
// credit sweep row: the tables those writers would leave hold nothing
// after a load, and the outbox the whole event chain hangs off holds no
// row at all.
func TestReceivablesLoadTriggersNothingDownstream(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	items := []load.Item{
		ld11Item("AR-Q-1", "CUST-ONE", "BR-A", "AR-8001", "2021-06-01", "2021-07-01", 22000, "", ""),
		ld11Item("AR-Q-2", "CUST-TWO", "BR-B", "AR-8002", "2021-06-02", "2021-07-02", -1100, "", ""),
	}
	// A migrated database already carries the seeded sample rows the
	// older migrations queued (the outbox among them), so the proof is
	// the count before the load against the count after it.
	cases := []struct {
		table string
		query string
	}{
		{"the transactional outbox, the whole event chain's source", `SELECT count(*) FROM events_outbox`},
		{"the GL journals", `SELECT count(*) FROM gl_journals`},
		{"the customer messages", `SELECT count(*) FROM msg_messages`},
		{"the AR reminder sends", `SELECT count(*) FROM ar_reminder_sends`},
		{"the statement runs", `SELECT count(*) FROM ar_statement_runs`},
		{"the statements", `SELECT count(*) FROM ar_statements`},
		{"the credit holds, the sweep's writes", `SELECT count(*) FROM credit_holds`},
		{"the invoice lines, an import carries none", `SELECT count(*) FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE org_id = '` + ld11OrgID + `'::uuid AND source_kind = 'IMPORT')`},
	}
	before := make([]int, len(cases))
	for i, tc := range cases {
		before[i] = e.count(t, tc.query)
	}
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-quiet-1", items...)); err != nil {
		t.Fatalf("the batch refused: %v", err)
	}
	for i, tc := range cases {
		after := e.count(t, tc.query)
		if after != before[i] {
			t.Fatalf("%s held %d rows before the load and %d after, want no change: a load triggers nothing downstream", tc.table, before[i], after)
		}
	}
}

// gatingWriter wraps the billing writer for the contention proof: the
// first write of each contending batch parks until the test releases
// it, so both batches hold their first name's row lock before each asks
// for the other's, the shape Postgres answers with a deadlock.
type gatingWriter struct {
	inner   load.Writer
	once    sync.Once
	ready   chan struct{}
	release chan struct{}
}

func (w *gatingWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	w.once.Do(func() {
		close(w.ready)
		<-w.release
	})
	return w.inner.Apply(ctx, tx, in)
}

// waitLD11 waits on a channel, releasing both gates and draining the
// racers before failing: a hung coordination wire fails loudly without
// leaving a parked goroutine holding a row lock behind it.
func waitLD11(t *testing.T, ch <-chan struct{}, name string, gates ...*gatingWriter) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		for _, g := range gates {
			select {
			case <-g.release:
			default:
				close(g.release)
			}
		}
		t.Fatalf("%s never signalled: the contention fixture is miswired", name)
	}
}

// TestReceivablesContendedBatchAnswersContendedAndResubmissionConverges:
// two batches updating the same two names contend, Postgres rolls one
// back whole, the victim answers ErrBatchContended with nothing of its
// own landed, and resubmitting the victim's batch id converges: both
// its items land as updates. The two batches carry opposite issue
// dates, because the door writes each customer's items oldest first:
// batch A runs the first name before the second, batch B the second
// before the first, so each holds one name's row lock while asking for
// the other's, the shape Postgres answers with a deadlock.
func TestReceivablesContendedBatchAnswersContendedAndResubmissionConverges(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	// The two names exist first, so the contending batches take the
	// update arm and its row locks.
	seed := []load.Item{
		ld11Item("AR-D-1", "CUST-ONE", "BR-A", "AR-9001", "2021-06-01", "2021-07-01", 10000, "", ""),
		ld11Item("AR-D-2", "CUST-ONE", "BR-A", "AR-9002", "2021-06-02", "2021-07-02", 20000, "", ""),
	}
	if _, err := e.door.UpsertReceivables(ctx, ld11Batch("b-dead-0", seed...)); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	namesBefore := e.count(t, `SELECT count(*) FROM load_key_registry WHERE org_id = $1::uuid AND kind = $2`, ld11OrgID, load.ReceivablesKind)

	gateA := &gatingWriter{inner: billing.NewReceivablesLoadWriter(), ready: make(chan struct{}), release: make(chan struct{})}
	gateB := &gatingWriter{inner: billing.NewReceivablesLoadWriter(), ready: make(chan struct{}), release: make(chan struct{})}
	doorA := load.NewReceivablesDoor(e.store, gateA, e.audit)
	doorB := load.NewReceivablesDoor(e.store, gateB, e.audit)
	// Batch A's dates order the first name before the second; batch B's
	// dates order the second before the first. Every value is invented.
	a1 := ld11Item("AR-D-1", "CUST-ONE", "BR-A", "AR-9001", "2021-06-01", "2021-07-01", 11000, "", "")
	a2 := ld11Item("AR-D-2", "CUST-ONE", "BR-A", "AR-9002", "2021-06-02", "2021-07-02", 21000, "", "")
	b1 := ld11Item("AR-D-1", "CUST-ONE", "BR-A", "AR-9001", "2021-06-03", "2021-07-03", 12000, "", "")
	b2 := ld11Item("AR-D-2", "CUST-ONE", "BR-A", "AR-9002", "2021-05-31", "2021-06-30", 22000, "", "")

	results := make([]load.BatchResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], errs[0] = doorA.UpsertReceivables(ctx, ld11Batch("b-dead-a", a1, a2))
	}()
	go func() {
		defer wg.Done()
		results[1], errs[1] = doorB.UpsertReceivables(ctx, ld11Batch("b-dead-b", b2, b1))
	}()
	waitLD11(t, gateA.ready, "batch A's first write", gateA, gateB)
	waitLD11(t, gateB.ready, "batch B's first write", gateA, gateB)
	close(gateA.release)
	close(gateB.release)
	wg.Wait()

	contended := -1
	for i, err := range errs {
		if err == nil {
			continue
		}
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("batch %d answered %v, want ErrBatchContended or nil", i, err)
		}
		contended = i
	}
	if contended == -1 {
		t.Fatal("neither batch contended: the fixture's locks never crossed, the proof is vacuous")
	}
	winner := 1 - contended
	if errs[winner] != nil {
		t.Fatalf("both batches contended: %v and %v", errs[0], errs[1])
	}
	if len(results[winner].Items) != 2 {
		t.Fatalf("the survivor answered %d outcomes, want its two", len(results[winner].Items))
	}
	// The victim landed nothing of its own: no batch record, no second
	// audit row, and the names still hold one row each.
	if n := e.count(t, `SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND source_system = $2`, ld11OrgID, ld11Source); n != 2 {
		t.Fatalf("%d batch records stand, want the seed's and the survivor's alone", n)
	}
	if n := e.count(t, `SELECT count(*) FROM load_key_registry WHERE org_id = $1::uuid AND kind = $2`, ld11OrgID, load.ReceivablesKind); n != namesBefore {
		t.Fatalf("the contention moved the registry: %d names, want %d", n, namesBefore)
	}

	// The resubmission converges: the victim's id was never recorded,
	// so its batch re-runs whole and lands as updates.
	victim := contended
	victimItems := []load.Item{b1, b2}
	if victim == 0 {
		victimItems = []load.Item{a1, a2}
	}
	r, err := e.door.UpsertReceivables(ctx, ld11Batch(fmt.Sprintf("b-dead-%s", map[int]string{0: "a", 1: "b"}[victim]), victimItems...))
	if err != nil {
		t.Fatalf("the resubmission refused: %v", err)
	}
	for _, o := range r.Items {
		if o.Status != load.OutcomeUpdated {
			t.Fatalf("the resubmission answered %+v, want updates", o)
		}
	}
	want := map[string]int64{"AR-9001": victimItems[0].Record["open_amount_cents"].(int64), "AR-9002": victimItems[1].Record["open_amount_cents"].(int64)}
	invoices := e.invoices(t)
	for docNo, amount := range want {
		if invoices[docNo].Balance != amount {
			t.Fatalf("%s carries balance %d after the resubmission, want the converging %d", docNo, invoices[docNo].Balance, amount)
		}
	}
}

// TestReceivablesWriterRefusesFloatMoneyItself: the writer's own guard,
// past the boundary: a record whose money arrived as a float refuses
// even when a caller built the map in process.
func TestReceivablesWriterRefusesFloatMoneyItself(t *testing.T) {
	e := setupLD11(t)
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	w := billing.NewReceivablesLoadWriter()
	rec := map[string]any{
		"source_doc_no":     "AR-9501",
		"issue_date":        "2021-06-01",
		"due_date":          "2021-07-01",
		"open_amount_cents": float64(1000),
	}
	_, err = w.Apply(ctx, tx, load.Write{
		OrgID:  ld11OrgID,
		Record: rec,
		Refs: []load.RefMatch{
			{Kind: load.RefKindCustomer, LegacyID: "CUST-ONE", HHID: ld11CustOneID},
			{Kind: load.RefKindBranch, LegacyID: "BR-A", HHID: ld11BranchAID},
		},
	})
	var ref *load.Refusal
	if err == nil || !errors.As(err, &ref) {
		t.Fatalf("float money answered %v, want a refusal", err)
	}
	if ref.Code != load.ReasonRecordShape || ref.Field != "open_amount_cents" {
		t.Fatalf("float money refused as %+v, want %s on open_amount_cents", ref, load.ReasonRecordShape)
	}
}

// ld11RefusingWriter backs the cross door replay probe: it must never
// be reached.
type ld11RefusingWriter struct{}

func (ld11RefusingWriter) Apply(_ context.Context, _ pgx.Tx, _ load.Write) (string, error) {
	return "", errors.New("the probe writer was reached")
}
