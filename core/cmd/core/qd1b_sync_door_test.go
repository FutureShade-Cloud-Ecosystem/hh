package main

// qd1b_sync_door_test.go is the sync door's grant legs (row 740, the
// widened grant): POST /api/v1/sync/approvals driven by request through
// the registered surface, every guard real. The three rules the grant
// names: a margin filing is refused exactly as CreateApproval refuses it
// (D7; the door files what the rail's own door refuses, words the
// approver would read as the server's own), the requester of a floor
// request cannot rewrite its detail (the approver reads the server's own
// record, never words the server did not check), and a pushed decision
// resolves its authority through the CONFIGURED floor approvers, so the
// branch's Sales manager decides a floor request of its own branch from
// the device as it does at the desk. Four eyes and the branch fence hold
// on the same door.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1bPushOp is one pushed operation's fixed shape (client ids are fixed
// uuids; the rows they stamp are cleaned by id, so a re-run against the
// same scratch database dedups nothing).
type qd1bPushOp struct {
	ClientID string         `json:"client_id"`
	Op       string         `json:"op"`
	Payload  map[string]any `json:"payload"`
}

// qd1bPushResult is one pushed operation's answer.
type qd1bPushResult struct {
	Status string  `json:"status"`
	Error  *string `json:"error"`
}

// qd1bPush pushes one operation as one principal and returns the result.
func qd1bPush(t *testing.T, q *qd1bFixture, sub string, op qd1bPushOp) qd1bPushResult {
	t.Helper()
	code, body := qd1bCall(t, q, http.MethodPost, "/api/v1/sync/approvals", sub,
		map[string]any{"ops": []qd1bPushOp{op}})
	if code != http.StatusOK {
		t.Fatalf("push %s %s: %d %s", op.Op, sub, code, body)
	}
	var resp struct {
		Results []qd1bPushResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode the push answer: %v (%s)", err, body)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("one operation must answer one result, got %d (%s)", len(resp.Results), body)
	}
	return resp.Results[0]
}

// qd1bRejected asserts the operation was rejected with the words.
func qd1bRejected(t *testing.T, name string, res qd1bPushResult, words string) {
	t.Helper()
	if res.Status != "rejected" {
		t.Fatalf("%s: push status %q, want rejected", name, res.Status)
	}
	if res.Error == nil || !strings.Contains(*res.Error, words) {
		t.Fatalf("%s: rejection %v, want words containing %q", name, res.Error, words)
	}
}

// qd1bApplied asserts the operation applied.
func qd1bApplied(t *testing.T, name string, res qd1bPushResult) {
	t.Helper()
	if res.Status != "applied" {
		t.Fatalf("%s: push status %q (error %v), want applied", name, res.Status, res.Error)
	}
}

// qd1bCleanSync removes this suite's pushed rows and their audit trail
// from the shared scratch database.
func qd1bCleanSync(t *testing.T, q *qd1bFixture, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, id)
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE entity_type = 'sync.approvals' AND entity_id = $1`, id)
	})
}

// qd1bSweepKey removes a push key's residue rows before the leg runs: a
// red run of a refused leg lands the very row the green run expects the
// door to refuse, and the engine's idempotency dedup would answer applied
// off the earlier run's row.
func qd1bSweepKey(t *testing.T, q *qd1bFixture, key string) {
	t.Helper()
	if _, err := q.pool.Exec(context.Background(),
		`DELETE FROM permission_approval_requests WHERE idempotency_key = $1`, key); err != nil {
		t.Fatalf("sweep push key residue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = q.pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE idempotency_key = $1`, key)
	})
}

// TestQD1B_SyncDoorRefusesAMarginFiling is D7's door leg: the sync door
// files what CreateApproval now refuses, a margin card with a subject and
// a detail of the filer's own choosing, and the approver would read words
// the server never checked. The door refuses it with the rail's own
// words, exactly as the create route does.
func TestQD1B_SyncDoorRefusesAMarginFiling(t *testing.T) {
	q := newQD1BFixture(t)
	qd1bSweepKey(t, q, "3ef10000-0d1b-4000-8000-0000000000c1")
	res := qd1bPush(t, q, testkit.SubInsideSales, qd1bPushOp{
		ClientID: "3ef10000-0d1b-4000-8000-0000000000c1",
		Op:       "create",
		Payload: map[string]any{
			"branch_id":   testkit.BranchNorthAID,
			"policy_type": "MARGIN_BELOW_MINIMUM",
			"detail":      "words the filer chose",
			"subject_ref": "a-quote-of-my-choosing",
		},
	})
	qd1bRejected(t, "a pushed margin filing", res,
		"margin approvals are filed by the quote desk for a quote line, not directly")

	// The door stays open to the caller-facing kinds it served: a credit
	// filing applies, so the refusal is the margin rule, not a closed door.
	res = qd1bPush(t, q, testkit.SubInsideSales, qd1bPushOp{
		ClientID: "3ef10000-0d1b-4000-8000-0000000000c2",
		Op:       "create",
		Payload: map[string]any{
			"branch_id":   testkit.BranchNorthAID,
			"policy_type": "CREDIT_LIMIT_EXCEEDED",
			"detail":      "a credit filing through the door",
		},
	})
	qd1bApplied(t, "a pushed credit filing", res)
	var creditID string
	if err := q.pool.QueryRow(context.Background(),
		`SELECT id::text FROM permission_approval_requests WHERE idempotency_key = $1`,
		"3ef10000-0d1b-4000-8000-0000000000c2").Scan(&creditID); err != nil {
		t.Fatalf("read the pushed credit row back: %v", err)
	}
	qd1bCleanSync(t, q, creditID)
}

// TestQD1B_SyncDoorRefusesAFloorRequestsNoteRewrite is the requester's
// rewrite leg: the detail a floor request carries is the quote desk's own
// record, built from the server's figures at filing, and the requester
// cannot replace it with words of their own. A plain filing's note stays
// the requester's to edit, so the refusal is the floor binding's, not a
// blanket lock on the note arm.
func TestQD1B_SyncDoorRefusesAFloorRequestsNoteRewrite(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	floorID := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthAID, "000000000c11")

	res := qd1bPush(t, q, testkit.SubInsideSales, qd1bPushOp{
		ClientID: "3ef10000-0d1b-4000-8000-0000000000c3",
		Op:       "update",
		Payload:  map[string]any{"id": floorID, "detail": "words the requester chose"},
	})
	qd1bRejected(t, "the requester rewrites a floor request's detail", res,
		"the floor request's detail is the quote desk's own record")
	// The row is unchanged: the server's own detail still stands.
	var detail string
	if err := q.pool.QueryRow(context.Background(),
		`SELECT detail FROM permission_approval_requests WHERE id = $1`, floorID).Scan(&detail); err != nil {
		t.Fatalf("read the floor request back: %v", err)
	}
	if detail != "qd1b fixture: pending floor request" {
		t.Fatalf("the floor request's detail was rewritten to %q", detail)
	}

	// A plain filing's note stays editable by its requester.
	credit, err := identity.NewApprovals(identity.NewApprovalRepository(q.pool)).
		CreateApproval(context.Background(), clerk, testkit.BranchNorthAID, identity.PolicyCredit,
			"the clerk's own note", "")
	if err != nil {
		t.Fatalf("plant a plain credit filing: %v", err)
	}
	qd1bCleanSync(t, q, credit.ID)
	res = qd1bPush(t, q, testkit.SubInsideSales, qd1bPushOp{
		ClientID: "3ef10000-0d1b-4000-8000-0000000000c4",
		Op:       "update",
		Payload:  map[string]any{"id": credit.ID, "detail": "the clerk's revised note"},
	})
	qd1bApplied(t, "the requester edits their own plain filing's note", res)
}

// TestQD1B_SyncDoorRefusesTheSalesManagerAnUnboundMarginCard is P2-C's
// device leg: the pushed decide path resolves the same rule the desk
// holds, so a legacy margin card with no line binding stays refused to
// the branch Sales manager from the device too, and the refusal decides
// nothing.
func TestQD1B_SyncDoorRefusesTheSalesManagerAnUnboundMarginCard(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	legacy := qd1bPlantUnboundMargin(t, q.pool, clerk, testkit.BranchNorthAID)
	qd1bCleanSync(t, q, legacy)

	res := qd1bPush(t, q, testkit.SubSalesManager, qd1bPushOp{
		ClientID: "3ef10000-0d1b-4000-8000-0000000000c8",
		Op:       "update",
		Payload:  map[string]any{"id": legacy, "status": "APPROVED"},
	})
	qd1bRejected(t, "the Sales manager decides an unbound margin card from the device", res,
		"no line binding")

	var status string
	if err := q.pool.QueryRow(context.Background(),
		`SELECT status FROM permission_approval_requests WHERE id = $1`, legacy).Scan(&status); err != nil {
		t.Fatalf("read the legacy card back: %v", err)
	}
	if status != "PENDING" {
		t.Fatalf("the refused legacy card is %s, want PENDING", status)
	}
}

// TestQD1B_SyncDoorDecidesWithTheConfiguredApprovers is the decide leg:
// a pushed decision resolves its authority through the configured floor
// approvers (D11, CP0-1 answer B), the same set the rail's decide path
// holds, so the branch's Sales manager approves a floor request of its
// own branch from the device. Four eyes and the branch fence hold on the
// same door: the requester's own pushed decision is refused whatever
// their role reaches, and the branch's Sales manager is refused another
// branch's floor request.
func TestQD1B_SyncDoorDecidesWithTheConfiguredApprovers(t *testing.T) {
	q := newQD1BFixture(t)
	clerk := qd1bUserID(t, q.pool, testkit.SubInsideSales)
	own := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthAID, "000000000c21")
	other := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthBID, "000000000c22")

	res := qd1bPush(t, q, testkit.SubSalesManager, qd1bPushOp{
		ClientID: "3ef10000-0d1b-4000-8000-0000000000c5",
		Op:       "update",
		Payload:  map[string]any{"id": own, "status": "APPROVED"},
	})
	qd1bApplied(t, "the Sales manager decides its own branch's floor request from the device", res)

	res = qd1bPush(t, q, testkit.SubSalesManager, qd1bPushOp{
		ClientID: "3ef10000-0d1b-4000-8000-0000000000c6",
		Op:       "update",
		Payload:  map[string]any{"id": other, "status": "APPROVED"},
	})
	qd1bRejected(t, "the Sales manager decides another branch's floor request", res, "")

	ownTwo := qd1bPlantFloor(t, q, clerk, testkit.BranchNorthAID, "000000000c23")
	res = qd1bPush(t, q, testkit.SubInsideSales, qd1bPushOp{
		ClientID: "3ef10000-0d1b-4000-8000-0000000000c7",
		Op:       "update",
		Payload:  map[string]any{"id": ownTwo, "status": "APPROVED"},
	})
	qd1bRejected(t, "the requester decides their own floor request", res, "")
}
