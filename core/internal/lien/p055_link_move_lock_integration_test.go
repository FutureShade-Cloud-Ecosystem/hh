//go:build integration

package lien

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// 0.55: a withhold cannot book RETENTION on a lien job while a move of that job's link is in flight, and a
// move cannot pass its ledger check while a withhold is mid way. Both orders are driven with a held
// transaction, never a sleep: one side holds its transaction open, the other starts and is observed either
// finished or waiting on a lock (pg_stat_activity), then the held side commits.

// p055Invoice plants a bare invoice for the world's customer A and removes it after the leg.
func (w *rt1World) p055Invoice(t *testing.T) (id, no string) {
	t.Helper()
	no = "P055-" + uuid.NewString()[:8]
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, due_date, holdback_cents)
		 VALUES ($1, $2, $3, CURRENT_DATE, 1000) RETURNING id::text`, no, w.customerA, w.branchA).Scan(&id); err != nil {
		t.Fatalf("plant invoice: %v", err)
	}
	t.Cleanup(func() {
		rt1Del(t, w.pool, "holdback_ledger", "invoice_id = $1", id)
		rt1Del(t, w.pool, "invoices", "id = $1", id)
	})
	return id, no
}

// p055Settle waits until the started call has finished (done closed) or a backend is waiting on a lock
// for a statement touching lien_jobs. It answers which. It polls a condition with a bound, it does not sleep
// for a fixed time.
func (w *rt1World) p055Settle(t *testing.T, done <-chan struct{}) (finished bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return true
		default:
		}
		var n int
		if err := w.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock' AND query ILIKE '%lien_jobs%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the second write neither finished nor waited on a lock")
	return false
}

// p055Impossible fails when the job is linked to a customer job other than want while its ledger holds an
// invoice level row: the money's link is wrong.
func (w *rt1World) p055Impossible(t *testing.T, lj, want string) {
	t.Helper()
	var held int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM holdback_ledger WHERE job_id = $1 AND invoice_id IS NOT NULL`, lj).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if got := w.linked(t, lj); held > 0 && got != want {
		t.Fatalf("the job is linked to %q while its ledger holds %d invoice level rows booked under %q", got, held, want)
	}
}

func TestIntegration_P055_WithholdFirstRefusesTheMove(t *testing.T) {
	w := rt1Plant(t)
	mgr := w.as(w.manager(w.branchA))
	lj := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.svc.LinkCustomerJob(mgr, lj, w.jobOnSiteON); err != nil {
		t.Fatal(err)
	}
	invID, invNo := w.p055Invoice(t)

	// The withhold is mid way: its RETENTION is written on its own open transaction.
	tx, err := w.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	booked, err := w.svc.BookRetention(customer.WithCallerTx(context.Background(), tx), RetentionBooking{
		InvoiceID: invID, InvoiceNo: invNo, CustomerJobID: w.jobOnSiteON, BranchID: w.branchA, AmountCents: 1000})
	if err != nil || !booked {
		t.Fatalf("book: %v %v", booked, err)
	}

	// The move starts now, with the withhold's row not yet committed.
	done := make(chan struct{})
	var moveErr error
	go func() {
		defer close(done)
		_, moveErr = w.svc.LinkCustomerJob(mgr, lj, w.jobOnSiteZA)
	}()
	w.p055Settle(t, done)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-done

	wantClientErr(t, moveErr, 422, "holds invoice level retention in its ledger")
	if got := w.linked(t, lj); got != w.jobOnSiteON {
		t.Fatalf("a refused move must leave the link: %q", got)
	}
	w.p055Impossible(t, lj, w.jobOnSiteON)
}

func TestIntegration_P055_MoveFirstHidesTheJobFromTheWithhold(t *testing.T) {
	w := rt1Plant(t)
	mgr := w.as(w.manager(w.branchA))
	lj := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.svc.LinkCustomerJob(mgr, lj, w.jobOnSiteON); err != nil {
		t.Fatal(err)
	}
	invID, invNo := w.p055Invoice(t)

	// The move is mid way: it has passed its check and written the new link on its own open transaction.
	tx, err := w.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := w.svc.LinkCustomerJob(customer.WithCallerTx(mgr, tx), lj, w.jobOnSiteZA); err != nil {
		t.Fatal(err)
	}

	// The withhold for the job the lien job is leaving starts now.
	done := make(chan struct{})
	var booked bool
	var bookErr error
	go func() {
		defer close(done)
		booked, bookErr = w.svc.BookRetention(context.Background(), RetentionBooking{
			InvoiceID: invID, InvoiceNo: invNo, CustomerJobID: w.jobOnSiteON, BranchID: w.branchA, AmountCents: 1000})
	}()
	w.p055Settle(t, done)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-done

	if bookErr != nil {
		t.Fatal(bookErr)
	}
	if booked {
		t.Fatal("the withhold booked RETENTION on a job that had moved away from the invoice's customer job")
	}
	if got := w.linked(t, lj); got != w.jobOnSiteZA {
		t.Fatalf("the move must have committed: %q", got)
	}
	w.p055Impossible(t, lj, w.jobOnSiteZA)
}

// The lock order, as a leg. A withhold takes the lien job row (FOR SHARE), then the invoice's advisory
// lock, then inserts; a move takes the lien job row (FOR UPDATE), then writes its audit row. Neither takes
// the other's second lock, and the first is the same row, so there is one order. Many withholds and a
// moving link at once finish with no deadlock and no impossible end state.
func TestIntegration_P055_LockOrderHasNoDeadlock(t *testing.T) {
	w := rt1Plant(t)
	mgr := w.as(w.manager(w.branchA))
	lj := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.svc.LinkCustomerJob(mgr, lj, w.jobOnSiteON); err != nil {
		t.Fatal(err)
	}
	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		invID, invNo := w.p055Invoice(t)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := w.svc.BookRetention(context.Background(), RetentionBooking{
				InvoiceID: invID, InvoiceNo: invNo, CustomerJobID: w.jobOnSiteON, BranchID: w.branchA, AmountCents: 1000})
			errs <- err
		}()
		go func(i int) {
			defer wg.Done()
			to := w.jobOnSiteZA
			if i%2 == 1 {
				to = w.jobOnSiteON
			}
			_, err := w.svc.LinkCustomerJob(mgr, lj, to)
			var ce clientError
			if errors.As(err, &ce) && ce.status == 422 {
				err = nil // a refusal is the rule working, not a fault
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "40P01" {
			t.Fatalf("deadlock: %v", err)
		}
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	}
	// Whatever order they ran in, rows booked under X and a link that left X cannot both stand.
	var held int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM holdback_ledger WHERE job_id = $1 AND invoice_id IS NOT NULL`, lj).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held > 0 && w.linked(t, lj) != w.jobOnSiteON {
		t.Fatalf("link %q while %d invoice rows stand under the first job", w.linked(t, lj), held)
	}
}

// The holdback sync is the second writer of RETENTION. It reads the job's link, then books. A held advisory
// lock on the invoice's retention key parks the sync inside its insert (after its link read); the move runs
// meanwhile; then the advisory lock is released. The sync must not leave RETENTION on a job that moved away:
// either it locked the job row first (the move waits, then refuses) or it rechecked the link and booked nothing.
func TestIntegration_P055_SyncRacesTheMove(t *testing.T) {
	w := rt1Plant(t)
	mgr := w.as(w.manager(w.branchA))
	lj := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.svc.LinkCustomerJob(mgr, lj, w.jobOnSiteON); err != nil {
		t.Fatal(err)
	}
	invID, _ := w.p055Invoice(t)
	w.svc.WithHoldbackFacts(fakeIntHoldback{inv: invID, branch: w.branchA, job: w.jobOnSiteON})

	// The held transaction owns the invoice's retention advisory key.
	tx, err := w.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(),
		`SELECT pg_advisory_xact_lock(hashtextextended('holdback_ledger:' || $1::text, 0))`, invID); err != nil {
		t.Fatal(err)
	}

	syncDone := make(chan struct{})
	var syncErr error
	go func() {
		defer close(syncDone)
		_, syncErr = w.svc.SyncHoldbacks(w.as(w.manager(w.branchA)), lj)
	}()
	// The sync has read the link and is parked on the advisory key (or finished, if it never needed it).
	deadline := time.Now().Add(15 * time.Second)
	for parked := false; !parked; {
		select {
		case <-syncDone:
			parked = true
			continue
		default:
		}
		if err := w.pool.QueryRow(context.Background(),
			`SELECT count(*) > 0 FROM pg_stat_activity WHERE datname = current_database() AND wait_event = 'advisory'`).Scan(&parked); err != nil {
			t.Fatal(err)
		}
		if !parked {
			if time.Now().After(deadline) {
				t.Fatal("the sync neither finished nor parked on the advisory lock")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// The move starts now: it finishes (nothing is booked yet) or waits on the job row the sync holds.
	moveDone := make(chan struct{})
	var moveErr error
	go func() {
		defer close(moveDone)
		_, moveErr = w.svc.LinkCustomerJob(mgr, lj, w.jobOnSiteZA)
	}()
	w.p055Settle(t, moveDone)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-syncDone
	<-moveDone

	if syncErr != nil {
		t.Fatalf("sync: %v", syncErr)
	}
	var ce clientError
	if moveErr != nil && !(errors.As(moveErr, &ce) && ce.status == 422) {
		t.Fatalf("move: %v", moveErr)
	}
	// The ledger row, if booked, names the invoice's customer job: the link must still be it.
	w.p055Impossible(t, lj, w.jobOnSiteON)
}

// The case the recheck clause guards: the move commits between the sync's unlocked link read (GetJob) and its
// row lock. A held transaction has moved the link but not committed; the sync reads the old link (MVCC, no
// wait), then parks on the lien job row lock; the move commits. The sync must book nothing and not fail.
func TestIntegration_P055_SyncSkipsAMoveThatCommittedFirst(t *testing.T) {
	w := rt1Plant(t)
	lj := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.svc.LinkCustomerJob(w.as(w.manager(w.branchA)), lj, w.jobOnSiteON); err != nil {
		t.Fatal(err)
	}
	invID, _ := w.p055Invoice(t)
	w.svc.WithHoldbackFacts(fakeIntHoldback{inv: invID, branch: w.branchA, job: w.jobOnSiteON})

	// The move is mid way: the link is rewritten on its own open transaction, holding the job row.
	tx, err := w.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(),
		`UPDATE lien_jobs SET customer_job_id = $2::uuid WHERE id = $1::uuid`, lj, w.jobOnSiteZA); err != nil {
		t.Fatal(err)
	}

	syncDone := make(chan struct{})
	var syncErr error
	var res SyncResult
	go func() {
		defer close(syncDone)
		res, syncErr = w.svc.SyncHoldbacks(w.as(w.manager(w.branchA)), lj)
	}()
	// The sync has read the old link and is waiting on the job row the move holds.
	if w.p055Settle(t, syncDone) {
		t.Fatal("the sync finished without waiting on the job row the move holds")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-syncDone

	if syncErr != nil {
		t.Fatalf("sync: %v", syncErr)
	}
	if res.BookedCount != 0 {
		t.Fatalf("the sync booked %+v on a job whose link had already moved", res)
	}
	var rows int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM holdback_ledger WHERE invoice_id = $1`, invID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("%d ledger rows for the invoice after the move committed first", rows)
	}
	w.p055Impossible(t, lj, w.jobOnSiteZA)
}
