package main

// r15_coder_b_leg5_test.go is packet B's concurrent double submit leg
// (design 5.3 leg 5, section 3.7): two goroutines post ONE body and ONE
// key. The stand-in holds the first charge until a second charge arrives or
// the bound ends. The ring must see exactly one charge: one answer 201, the
// other 409 card_charge_in_flight, and a third same-key post then replays
// the committed sale. On the tip both goroutines charge (the sale
// transaction is the only arbiter and it serializes too late): two charges,
// one 201 and one bare 500.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestR15B_Leg5_ConcurrentDoubleSubmitChargesExactlyOnce.
func TestR15B_Leg5_ConcurrentDoubleSubmitChargesExactlyOnce(t *testing.T) {
	host, seen := newR15RunHost(t)
	seen.hold = true
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": "r15-leg-not-a-secret"})
	w := plantWiringCounter(t, pool)
	_, mux, sg := wireFold(t, pool, key, w.branchID)

	saleKey := uuidKey(t)
	body := r15RingBody(w, saleKey, "tok_r15_leg5")

	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i] = postJSONBounded(t, mux, sg, "/api/v1/pos/sales", body)
		}(i)
	}
	wg.Wait()

	created, inFlight, other := 0, 0, 0
	for _, rec := range recs {
		switch {
		case rec.Code == http.StatusCreated || rec.Code == http.StatusOK:
			created++
		case rec.Code == http.StatusConflict && jsonErrWord(rec.Body.String(), "card_charge_in_flight"):
			inFlight++
		default:
			other++
			t.Logf("the double submit's other answer: %d (%s)", rec.Code, rec.Body.String())
		}
	}
	if created != 1 || inFlight != 1 || other != 0 {
		t.Fatalf("the double submit answered created=%d in_flight=%d other=%d, want exactly one of each of the first two and none other", created, inFlight, other)
	}
	charges, voids, refunds := seen.counts()
	if charges != 1 || voids != 0 || refunds != 0 {
		t.Fatalf("the processor saw charges=%d voids=%d refunds=%d, want exactly one charge and no reversal", charges, voids, refunds)
	}

	// The third same-key post replays the committed sale, charging nothing.
	rec3 := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", body)
	if rec3.Code != http.StatusOK {
		t.Fatalf("the third same-key post = %d (%s), want the replay's 200", rec3.Code, rec3.Body.String())
	}
	charges, _, _ = seen.counts()
	if charges != 1 {
		t.Fatalf("the replay charged again: charges=%d, want 1", charges)
	}
}
