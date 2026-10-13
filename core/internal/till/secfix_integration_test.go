//go:build integration

package till

import (
	"context"
	"errors"
	"testing"
)

// A well formed branch id that is no location (a global caller passes the reach check for any id) is the
// client's error: 422, never the foreign key failure as a 500.
func TestIntegration_AttemptForANonexistentBranchIsUnprocessable(t *testing.T) {
	pool := intPool(t)
	_, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	err := svc.RecordPaymentAttempt(globalCtx(userID), secfixAttempt("k-int-nolocation", "00000000-0000-4000-8000-00000000dead"))
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("attempt for a branch that is no location: got %v, want a 422", err)
	}
	var n int
	if qerr := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_attempts WHERE idempotency_key = 'k-int-nolocation'`).Scan(&n); qerr != nil || n != 0 {
		t.Fatalf("nothing may be written for it: count %d (%v)", n, qerr)
	}
}
