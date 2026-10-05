package main

// r15_coder_b_leg3_test.go is packet B's committed-before-the-call leg
// (design 5.3 leg 3): the stand-in, on receiving a charge, reads the
// CHARGING attempt rows through a SECOND pool on the same database, and the
// test asserts it found one. That is the property A1's table exists for:
// the attempt row is committed on the pool BEFORE the processor is called,
// so a crash between the call and the stamp leaves a row the sweep can
// find. On the tip no row is ever written (the charge rides the sale
// transaction and no claim exists), so the probe finds nothing while the
// charge is in flight.

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// probeDSN is the same database URL wiringPool read, named again on the
// probe pool so the second pool dials exactly what the wiring dials.
func probeDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(coreDBEnvVar)
	if dsn == "" {
		t.Skip(coreDBEnvVar + " is not set")
	}
	return dsn
}

// TestR15B_Leg3_TheAttemptRowIsCommittedBeforeTheProcessorCall: a plain
// card sale commits, and the stand-in saw a CHARGING row while the charge
// was in flight (the claim commits before the dispatch, and the stamp
// cannot land before the answer the stand-in has not yet given).
func TestR15B_Leg3_TheAttemptRowIsCommittedBeforeTheProcessorCall(t *testing.T) {
	host, seen := newR15RunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	// The stand-in's own pool: a SECOND set of connections on the same
	// database, the reader a crash investigator would be.
	probePool, err := pgxpool.New(context.Background(), probeDSN(t))
	if err != nil {
		t.Fatalf("probe pool: %v", err)
	}
	t.Cleanup(probePool.Close)
	seen.probe = probePool

	key := wiringRailDB(t, pool, map[string]string{"api_key": "r15-leg-not-a-secret"})
	w := plantWiringCounter(t, pool)
	_, mux, sg := wireFold(t, pool, key, w.branchID)

	rec := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", r15RingBody(w, uuidKey(t), "tok_r15_leg3"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the ring = %d (%s), want the committed 201", rec.Code, rec.Body.String())
	}
	seen.mu.Lock()
	charging := seen.probeCharging
	seen.mu.Unlock()
	if charging < 1 {
		t.Fatalf("the stand-in found %d CHARGING attempt rows while the charge was in flight, want at least one: the claim must commit before the processor call", charging)
	}
}
