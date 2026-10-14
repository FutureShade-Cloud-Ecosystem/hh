//go:build integration

// syncscope_pod_attest_ledger_integration_test.go: the attestation door inside the engine's whole flow,
// the key ledger (sync_op_hashes) in front of the door's own idempotency stamp. The two agree: a replay
// of the same operation answers applied and runs nothing twice whether the stop still holds the key or
// has since taken another; a key reused with another body is refused and attests nothing; a refused
// operation binds no key, so the corrected body may retry under it.
package dispatch

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPodAttest_LedgerSameKeyDifferentBodyIsRefusedAndAttestsNothingNew(t *testing.T) {
	a := newAttestWorld(t)
	key := syncFreshKey()
	if first := a.push(a.driverA, attestOp(key, a.stopA2, "Site Foreman", true)); first[0].Status != "applied" {
		t.Fatalf("first: %+v %s", first[0], errText(first[0]))
	}
	res := a.push(a.driverA, attestOp(key, a.stopA2, "Another Person", true))
	if res[0].Status != "rejected" || !strings.Contains(errText(res[0]), "key_reused") {
		t.Fatalf("the same key with another body is key_reused: %+v (%s)", res[0], errText(res[0]))
	}
	if got := a.stop(a.stopA2); got.receivedBy != "Site Foreman" || len(a.h.signer.calls) != 1 {
		t.Fatalf("the first attestation stands and nothing else was attested: %+v, signatures %d", got, len(a.h.signer.calls))
	}
}

func TestPodAttest_LedgerReplayAfterTheStopTookAnotherKeyAnswersApplied(t *testing.T) {
	a := newAttestWorld(t)
	op := attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)
	first := a.push(a.driverA, op)
	if first[0].Status != "applied" {
		t.Fatalf("first: %+v %s", first[0], errText(first[0]))
	}
	// A later stops write overwrites the stop's one key column, so the door's own stamp no longer finds
	// the attestation's key; the ledger still holds it.
	if _, err := a.pool.Exec(context.Background(), `UPDATE delivery_stops SET idempotency_key = $2::uuid WHERE id = $1::uuid`, a.stopA2, uuid.NewString()); err != nil {
		t.Fatalf("move the stop's key: %v", err)
	}
	res := a.push(a.driverA, op)
	if res[0].Status != "applied" {
		t.Fatalf("a replay of an applied key answers applied through the ledger, not a false refusal: %+v (%s)", res[0], errText(res[0]))
	}
	if len(a.h.signer.calls) != 1 || len(a.h.fulfil.orders) != 1 || a.auditCount(a.stopA2) != 1 {
		t.Fatalf("nothing runs twice: signatures %d, hand offs %d, audit rows %d", len(a.h.signer.calls), len(a.h.fulfil.orders), a.auditCount(a.stopA2))
	}
}

func TestPodAttest_LedgerRefusedOperationBindsNoKey(t *testing.T) {
	a := newAttestWorld(t)
	key := syncFreshKey()
	bad := a.push(a.driverA, attestOp(key, a.stopA2, "Site\x00Foreman", true))
	if bad[0].Status != "rejected" {
		t.Fatalf("the unsafe name is refused: %+v", bad[0])
	}
	good := a.push(a.driverA, attestOp(key, a.stopA2, "Site Foreman", true))
	if good[0].Status != "applied" {
		t.Fatalf("the corrected body retries under the refused key: %+v (%s)", good[0], errText(good[0]))
	}
}
