//go:build integration

package pos

// account_credit_pool_integration_test.go, the ACCOUNT_CREDIT return rail on
// the small-pool rig (the refund-sum legs' throwaway local Postgres, the
// world and the exact 10.00 anchor are buildWorld's): four parallel
// ACCOUNT_CREDIT returns of ONE sale over a two-connection pool. Every
// Return transaction holds one connection while it waits on the per-sale
// advisory lock, so the moment any in-transaction work reads through the
// POOL the lock holder blocks forever on a second connection every other
// connection is a waiter that needs the holder's lock: the whole leg stalls
// until the context deadline. The customer subledger read at the top of
// applyAccountCredit therefore rides the transaction (ReadCustomerTx, the
// FindReturnByKeyTx discipline), and on the transaction-reading core the
// four returns serialize on the lock, every one of them commits, and the
// sale's subledger carries exactly the four CREDIT_MEMO rows the rails
// booked.
//
// Run (own throwaway container, removed afterwards, another unused port):
//
//	docker run --name hh-poolstall-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hardscapeos \
//	  -p 5631:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5631/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5631/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@localhost:5631/hardscapeos?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_AccountCredit -p 1 -v

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Four parallel ACCOUNT_CREDIT returns of one sale on a two-connection
// pool: one line of 30 SF, four returns of 5 SF each on four distinct keys,
// each paying 56.50, the four together 226.00, under the configured 250.00,
// so on a healthy core every one of them commits. Each return's customer
// read runs inside its Return transaction: through the pool it is the stall
// this leg names, on the transaction it is four clean commits.
func TestIntegration_Trenton_AccountCredit_ParallelReturnsSmallPool(t *testing.T) {
	s := newTrentonStackOnPool(t, smallPool(t, 2))
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{30}, TenderCash)

	bearer := s.bearer(trentonClerkSub)
	bodies := make([]*bytes.Reader, 4)
	for i := 0; i < 4; i++ {
		raw, err := json.Marshal(s.returnBody(key(), sale, 0, RefundAccountCredit, 5))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		bodies[i] = bytes.NewReader(raw)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	results := make([]*httptest.ResponseRecorder, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/returns", bodies[i]).WithContext(ctx)
			req.Header.Set("Authorization", bearer)
			req.Header.Set("X-Org", demoOrgSlug)
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			results[i] = rec
		}(i)
	}
	wg.Wait()

	if ctx.Err() != nil {
		t.Fatalf("the small-pool parallel ACCOUNT_CREDIT returns deadlocked until the context deadline: %v", ctx.Err())
	}
	for i, rec := range results {
		if rec.Code != http.StatusCreated {
			t.Fatalf("ACCOUNT_CREDIT return %d must commit on the small pool, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	// The rail's own trail: the sale's subledger carries exactly the four
	// CREDIT_MEMO rows, the paid invoice untouched (a collect-now sale's
	// invoice books PAID, so the whole total rides the credit memo) and the
	// account down by the four returns' total.
	var rows int
	var sum int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*), COALESCE(SUM(amount_cents), 0) FROM customer_transactions
		  WHERE source_type = 'pos_return' AND source_ref_id = $1::uuid`, sale.OrderID).Scan(&rows, &sum); err != nil {
		t.Fatalf("subledger read: %v", err)
	}
	if rows != 4 || sum != -22600 {
		t.Fatalf("the four ACCOUNT_CREDIT returns must book four credit memos totaling -226.00 on the sale's subledger, got %d rows summing %d", rows, sum)
	}
}
