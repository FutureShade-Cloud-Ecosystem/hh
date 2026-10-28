//go:build integration

// syncscope_pod_attest_replay_fence_integration_test.go: the bound key gate's second round on the pod
// scope's typed name attestation. The ledger is org keyed, so a colleague driver's replay of another
// driver's bound key reaches the gate; the gate may hide the key only when the caller's fence hides the
// stop the key applied (an attestation create answers the stop), and the owner's and the desk's own
// answers stand. Run with DATABASE_URL set to a migrated and seeded throwaway database.
package dispatch

import (
	"testing"
)

func TestPodAttestReplayFence_AColleaguesReplayOfABoundAttestationKeyAnswersLikeTheAbsentKey(t *testing.T) {
	a := newAttestWorld(t)
	op := attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true)
	if first := a.push(a.driverA, op); first[0].Status != "applied" {
		t.Fatalf("first: %+v %s", first[0], errText(first[0]))
	}
	// The control: the same stop under a fresh key from the colleague, whose fence hides it. The
	// replays below must answer exactly this, whatever body they carry.
	absent := a.push(a.driverB, attestOp(syncFreshKey(), a.stopA2, "Site Foreman", true))
	if absent[0].Status != "rejected" {
		t.Fatalf("the control: a colleague's stop is the same as absent: %+v (%s)", absent[0], errText(absent[0]))
	}
	want := errText(absent[0])
	same := a.push(a.driverB, op)
	if same[0].Status != absent[0].Status || errText(same[0]) != want {
		t.Fatalf("a colleague's replay of a bound key (same body) answers like the absent key: got %+v (%s) want %+v (%s)", same[0], errText(same[0]), absent[0], want)
	}
	other := a.push(a.driverB, attestOp(op.ClientId, a.stopA2, "Another Person", true))
	if other[0].Status != absent[0].Status || errText(other[0]) != want {
		t.Fatalf("a colleague's replay of a bound key (another body) answers like the absent key, never key_reused: got %+v (%s) want %+v (%s)", other[0], errText(other[0]), absent[0], want)
	}
	if len(a.h.signer.calls) != 1 {
		t.Fatalf("the colleague's replays attest nothing: signatures %d", len(a.h.signer.calls))
	}
	// The desk's fence leaves the stop visible, so the desk's replay keeps the bound answer.
	if desk := a.push(a.desk, op); desk[0].Status != "applied" {
		t.Fatalf("the desk's replay of the bound key answers as before: %+v (%s)", desk[0], errText(desk[0]))
	}
}
