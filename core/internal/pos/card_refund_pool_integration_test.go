//go:build integration

package pos

// card_refund_pool_integration_test.go, the CARD return rail on the
// small-pool rig (the refund-sum legs' throwaway local Postgres, the world
// and the exact 10.00 anchor are buildWorld's): four parallel CARD returns
// of ONE card sale over a two-connection pool, the rig wired with a
// PRODUCTION-SHAPED card refunder, a payment.Service behind the same
// adapter the composition root builds, because the rig's counting fake
// never exercises the production rail shape. Every Return transaction holds
// one connection while it waits on the per-sale advisory lock, and the
// production refunder's first act is a config read through the POOL: the
// lock holder asks the pool for a second connection every other connection
// is a Return waiter that needs the holder's lock, and the whole pool
// deadlocks until the context deadline. On a core whose in-transaction
// reads ride the transaction (the FindReturnByKeyTx discipline, the
// ACCOUNT_CREDIT rail's fix, one package deeper here), the four returns
// serialize on the lock, every one of them commits, and the sale's trail
// carries exactly the four CARD returns the rails booked, the till's refund
// payments each carrying its own distinct reversal ref.
//
// Run (own throwaway container, removed afterwards, another unused port):
//
//	docker run --name hh-cardrail-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hardscapeos \
//	  -p 5633:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5633/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5633/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@localhost:5633/hardscapeos?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_CardRefund -p 1 -v

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/jackc/pgx/v5"
)

// cardRailAdapter mirrors cmd/core's posCardRefunderAdapter over a real
// payment.Service: the same call into Service.RefundTx and the same receipt
// handling, the reversal's own processor ref returned and the decrypted
// creds never crossing into pos. The production rail shape, which the rig's
// counting fake is not.
type cardRailAdapter struct{ svc *payment.Service }

func (a cardRailAdapter) RefundCard(ctx context.Context, tx pgx.Tx, gatewayRef string, amountCents int64, reference, idempotencyKey string) (string, error) {
	rcpt, err := a.svc.RefundTx(ctx, tx, payment.RefundInput{
		AmountCents:    amountCents,
		ProcessorRef:   gatewayRef,
		Reference:      reference,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return "", err
	}
	return rcpt.ProcessorRef, nil
}

// Four parallel CARD returns of one card sale on a two-connection pool: one
// line of 30 SF ringed CARD (the rig's stub charger captures it, so the
// sale's gateway ref is non-empty, all the stub reversal needs), four
// returns of 5 SF each on four distinct keys, each paying 56.50, the four
// together 226.00, under the configured 250.00, so on a healthy core every
// one of them commits. The dealer's payment config is planted through the
// SAME small pool before the returns fire, buildWorld plants none: the
// active row the production refunder's config read answers from. Each
// return's reversal runs inside its Return transaction: through the pool it
// is the stall this leg names, on the transaction it is four clean commits.
func TestIntegration_Trenton_CardRefund_ParallelReturnsSmallPool(t *testing.T) {
	pool := smallPool(t, 2)
	vault, err := payment.NewVault("")
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	store := payment.NewStore(pool, vault)
	s := newTrentonStackOnPoolWithRail(t, pool, cardRailAdapter{svc: payment.NewService(store, payment.RunPaymentsStub{})})
	sale := s.ringSale(key(), trentonBranchID, s.trentonTill, "trenton", []float64{30}, TenderCard)

	// The dealer's config row: surcharge stays disabled (Configure refuses
	// true), the api_key non-empty so the stub reversal passes its guards,
	// and the processor's own name. Planted before any return fires.
	if err := store.Configure(context.Background(), "run_payments", "MID-CARDRAIL-RIG", "CAD", "", 0, false,
		map[string]string{"api_key": "rig-only-not-a-secret"}, ""); err != nil {
		t.Fatalf("configure the payment config: %v", err)
	}

	bearer := s.bearer(trentonClerkSub)
	bodies := make([]*bytes.Reader, 4)
	for i := 0; i < 4; i++ {
		raw, err := json.Marshal(s.returnBody(key(), sale, 0, RefundCard, 5))
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
		t.Fatalf("the small-pool parallel CARD returns deadlocked until the context deadline: %v", ctx.Err())
	}
	for i, rec := range results {
		if rec.Code != http.StatusCreated {
			t.Fatalf("CARD return %d must commit on the small pool, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	// The order's own trail: exactly four CARD returns on the sale, the four
	// together 226.00 (each 5 SF at the exact 10.00 anchor plus the flat 13
	// percent), nothing more and nothing less.
	var returns int
	var returned int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*), COALESCE(SUM(total_cents), 0) FROM pos_returns WHERE order_id = $1::uuid`,
		sale.OrderID).Scan(&returns, &returned); err != nil {
		t.Fatalf("returns read: %v", err)
	}
	if returns != 4 || returned != 22600 {
		t.Fatalf("the sale must carry exactly the four CARD returns totaling 226.00, got %d rows summing %d", returns, returned)
	}

	// The rails' trail: the till wrote one refund payment per return, each a
	// CARD refund carrying its OWN reversal ref (the stub mints every
	// refund's ref deterministically from that return's idempotency key), so
	// four returns book four DISTINCT rp_ref_ refs, none shared and none
	// missing.
	rows, err := s.pool.Query(context.Background(),
		`SELECT p.gateway_ref FROM pos_returns r JOIN payments p ON p.id = r.refund_payment_id
		  WHERE r.order_id = $1::uuid AND p.method = 'CARD' ORDER BY r.id`, sale.OrderID)
	if err != nil {
		t.Fatalf("refund payments read: %v", err)
	}
	defer rows.Close()
	refs := map[string]bool{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			t.Fatalf("refund payment scan: %v", err)
		}
		if !strings.HasPrefix(ref, "rp_ref_") {
			t.Fatalf("a CARD refund payment must carry the reversal's rp_ref_ ref, got %q", ref)
		}
		if refs[ref] {
			t.Fatalf("two returns must never share one reversal ref, %q repeated", ref)
		}
		refs[ref] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("refund payments walk: %v", err)
	}
	if len(refs) != 4 {
		t.Fatalf("the four CARD returns must book four refund payments with distinct refs, got %d", len(refs))
	}
}
