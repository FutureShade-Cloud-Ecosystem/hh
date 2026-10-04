package main

// qd1f_approvals_delete_test.go is the approvals sync door's Delete (P2-A,
// round 4): POST /api/v1/sync/approvals with a delete, every guard real. The
// door once fenced the tombstone by org alone, so any staff member withdrew
// anyone's request, and since the tombstone now takes effect (lists, decide
// and consumption ignore it) that withdrawal cancelled a seller's pending or
// already approved clearing. Only the requester withdraws, and only while the
// request is still pending: a decided or consumed one is a record.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1fDeleted reads one request's tombstone back.
func qd1fDeleted(t *testing.T, q *qd1bFixture, id string) bool {
	t.Helper()
	var deleted bool
	if err := q.pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL FROM permission_approval_requests WHERE id = $1`, id).Scan(&deleted); err != nil {
		t.Fatalf("read the request back: %v", err)
	}
	return deleted
}

// qd1fSetState moves a planted request to a state the decide rail would leave
// it in (status, and the used stamp a consumption leaves).
func qd1fSetState(t *testing.T, q *qd1bFixture, id, status string, used bool) {
	t.Helper()
	if _, err := q.pool.Exec(context.Background(), `
		UPDATE permission_approval_requests
		   SET status = $2, data = CASE WHEN $3 THEN data || '{"used": true}'::jsonb ELSE data END
		 WHERE id = $1`, id, status, used); err != nil {
		t.Fatalf("move the request to %s: %v", status, err)
	}
}

// Each push states a fresh client id: the engine dedups on it, and a rerun
// against the same scratch database must meet the door, not a remembered answer.
func qd1fDelete(t *testing.T, q *qd1bFixture, sub, key, id string) qd1bPushResult {
	t.Helper()
	return qd1bPush(t, q, sub, qd1bPushOp{ClientID: key, Op: "delete", Payload: map[string]any{"id": id}})
}

func TestQD1F_ApprovalsSyncDeleteIsTheRequestersOwnAndOnlyWhilePending(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)

	// Nobody but the requester tombstones a floor request, pending or approved.
	for i, state := range []struct {
		name   string
		status string
		used   bool
	}{{"pending", "PENDING", false}, {"approved", "APPROVED", false}} {
		for j, sub := range []string{xrSubYardTeam, xrSubHR, testkit.SubSalesManager, testkit.SubGeneralMgr, xrSubFinancialController, xrSubBranchManager} {
			id := qd1cPlantMargin(t, q, clerk, "0000000f"+string(rune('1'+i))+string(rune('a'+j))+"0")
			qd1fSetState(t, q, id, state.status, state.used)
			res := qd1fDelete(t, q, sub, uuid.NewString(), id)
			qd1bRejected(t, sub+" withdraws a "+state.name+" floor request", res, "")
			if qd1fDeleted(t, q, id) {
				t.Errorf("%s tombstoned a %s floor request that is not theirs", sub, state.name)
			}
		}
	}

	// The requester withdraws its own pending request.
	own := qd1cPlantMargin(t, q, clerk, "0000000f2000")
	qd1bApplied(t, "the requester withdraws its own pending request",
		qd1fDelete(t, q, testkit.SubInsideSales, uuid.NewString(), own))
	if !qd1fDeleted(t, q, own) {
		t.Errorf("the requester's own pending request was not withdrawn")
	}

	// A decided or consumed request is a record, even for its requester.
	for i, state := range []struct {
		name   string
		status string
		used   bool
	}{{"approved", "APPROVED", false}, {"rejected", "REJECTED", false}, {"consumed", "APPROVED", true}} {
		id := qd1cPlantMargin(t, q, clerk, "0000000f3"+string(rune('a'+i))+"00")
		qd1fSetState(t, q, id, state.status, state.used)
		res := qd1fDelete(t, q, testkit.SubInsideSales, uuid.NewString(), id)
		qd1bRejected(t, "the requester withdraws a "+state.name+" request", res, "")
		if qd1fDeleted(t, q, id) {
			t.Errorf("a %s request was withdrawn", state.name)
		}
	}
}

// TestQD1F_ApprovalsSyncStaleUpdateEnvelopeCarriesNoFloorRequestItsCallerCannotRead
// pins the Load mask (round 3 P2-3, the review's round 4 P3-1): a stale push
// meets the engine's conflict envelope, which carries Load's projection. For a
// floor request the caller could not read through the pull, that projection
// would carry the margin figures. The envelope must still arrive (the engine
// needs the row's id and seq), with no server state.
func TestQD1F_ApprovalsSyncStaleUpdateEnvelopeCarriesNoFloorRequestItsCallerCannotRead(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	id := qd1cPlantMargin(t, q, clerk, "0000000f4000")
	stale := 0
	for _, sub := range []string{xrSubHR, xrSubYardTeam, xrSubBranchManager} {
		code, body := qd1bCall(t, q, http.MethodPost, "/api/v1/sync/approvals", sub, map[string]any{
			"ops": []map[string]any{{"client_id": uuid.NewString(), "op": "update",
				"payload": map[string]any{"id": id, "detail": "words"}, "base_seq": stale}}})
		if code != http.StatusOK {
			t.Fatalf("stale push as %s: %d %s", sub, code, body)
		}
		if !strings.Contains(body, "conflict") {
			t.Fatalf("%s: the stale push did not meet the conflict envelope: %s", sub, body)
		}
		if strings.Contains(body, "bps against") || strings.Contains(body, qd1cMarginDetail) {
			t.Errorf("%s: the conflict envelope carries a floor request it cannot read: %s", sub, body)
		}
	}
}
