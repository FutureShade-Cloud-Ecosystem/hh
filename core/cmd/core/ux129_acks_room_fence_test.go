package main

// ux129_acks_room_fence_test.go: UX-129. The acks sync write checked only the seat, while REST AckMessage
// goes through assertMember and so the money room fence. The composition left the acks store's fence unbound,
// so a member seated in a money room the payment trail and branch reach refuse could record an ack there over
// sync while REST answered 403. The leg drives the full served surface the binary wires, over the migrated
// scratch database, and holds: the refused-by-fence answer, the non-member answer and the missing message
// answer byte for byte the same with no row and no seq taken; and the admitted cases still applying (an
// ordinary room, a money room through the payment trail alone, a money room through the trail and the branch
// reach). Gated on HH_CORE_TEST_DATABASE_URL like every composition suite.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	ux129RefusedRoom  = "a5000000-0000-4000-9000-000000000449" // payments-trenton: branch reach refuses inside sales
	ux129BranchRoom   = "a5000000-0000-4000-9000-000000000129" // built here: a branch A money room, trail and branch reach admit
	ux129TrailRoom    = "a5000000-0000-4000-9000-000000000451" // payments, branchless: the trail alone admits
	ux129OpenRoom     = "a5000000-0000-4000-9000-000000000003" // office, no member_roles
	ux129UnseatedRoom = "a5000000-0000-4000-9000-000000000002" // yard-kingston, caller not seated
)

func TestSyncAckHonoursTheMoneyRoomFenceAtTheComposition(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	pool := graph.pool
	ctx := context.Background()

	var uid string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE platform_user_id = $1`, testkit.SubInsideSales).Scan(&uid); err != nil {
		t.Fatalf("the staff fixture user: %v", err)
	}
	// A money room on inside sales' own branch (north A), with the seeded money rooms' member roles: the
	// trail holder it reaches is admitted through both arms of the fence.
	if _, err := pool.Exec(ctx,
		`INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, tier, member_roles)
		 SELECT $1::uuid, 'TEAM', $2::uuid, 'ux129-payments-fxna', '#ux129-payments-fxna', 'ux129 branch A money room', 'STANDARD', member_roles
		 FROM msg_channels WHERE id = $3::uuid
		 ON CONFLICT (id) DO NOTHING`, ux129BranchRoom, testkit.BranchNorthAID, ux129RefusedRoom); err != nil {
		t.Fatalf("build the branch A money room: %v", err)
	}
	seated := []string{ux129RefusedRoom, ux129BranchRoom, ux129TrailRoom, ux129OpenRoom}
	for _, ch := range seated {
		if _, err := pool.Exec(ctx,
			`INSERT INTO msg_members (channel_id, user_id, role, source) VALUES ($1::uuid, $2::uuid, 'MEMBER', 'EXPLICIT')
			 ON CONFLICT (channel_id, user_id) DO NOTHING`, ch, uid); err != nil {
			t.Fatalf("seat in %s: %v", ch, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ux129UnseatedRoom, uid); err != nil {
		t.Fatalf("unseat: %v", err)
	}
	msgs := map[string]string{}
	for _, ch := range append(append([]string{}, seated...), ux129UnseatedRoom) {
		id := uuid.NewString()
		msgs[ch] = id
		if _, err := pool.Exec(ctx,
			`INSERT INTO msg_messages (id, channel_id, kind, event_key, body, requires_ack)
			 VALUES ($1::uuid, $2::uuid, 'EVENT', 'ux129.probe', 'ux129 probe', true)`, id, ch); err != nil {
			t.Fatalf("message in %s: %v", ch, err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, m := range msgs {
			_, _ = pool.Exec(c, `DELETE FROM msg_acks WHERE message_id = $1::uuid`, m)
			_, _ = pool.Exec(c, `DELETE FROM msg_messages WHERE id = $1::uuid`, m)
		}
		for _, ch := range seated {
			_, _ = pool.Exec(c, `DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ch, uid)
		}
		_, _ = pool.Exec(c, `DELETE FROM msg_channels WHERE id = $1::uuid`, ux129BranchRoom)
	})

	push := func(messageID string) (status, errText string) {
		t.Helper()
		body := fmt.Sprintf(`{"ops":[{"client_id":%q,"op":"create","payload":{"message_id":%q,"method":"TAP"}}]}`,
			uuid.NewString(), messageID)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/acks", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, testkit.SubInsideSales))
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		if rsp.Code != http.StatusOK {
			t.Fatalf("push of %s: status %d body %s", messageID, rsp.Code, rsp.Body.String())
		}
		var out struct {
			Results []struct {
				Status string  `json:"status"`
				Error  *string `json:"error"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rsp.Body.Bytes(), &out); err != nil || len(out.Results) != 1 {
			t.Fatalf("push of %s: unreadable answer %s (%v)", messageID, rsp.Body.String(), err)
		}
		e := ""
		if out.Results[0].Error != nil {
			e = *out.Results[0].Error
		}
		return out.Results[0].Status, e
	}
	// The ack seq sequence's state: a refused push must leave it where it was (no seq taken).
	seqState := func() string {
		t.Helper()
		var name string
		if err := pool.QueryRow(ctx, `SELECT pg_get_serial_sequence('msg_acks', 'seq')`).Scan(&name); err != nil {
			t.Fatalf("name the ack seq: %v", err)
		}
		var last int64
		var called bool
		if err := pool.QueryRow(ctx, `SELECT last_value, is_called FROM `+name).Scan(&last, &called); err != nil {
			t.Fatalf("read the ack seq: %v", err)
		}
		return fmt.Sprintf("%d/%v", last, called)
	}
	ackRows := func(messageID string) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM msg_acks WHERE message_id = $1::uuid`, messageID).Scan(&n); err != nil {
			t.Fatalf("count acks: %v", err)
		}
		return n
	}

	before := seqState()
	missStatus, missErr := push(uuid.NewString())
	nonMemberStatus, nonMemberErr := push(msgs[ux129UnseatedRoom])
	refusedStatus, refusedErr := push(msgs[ux129RefusedRoom])
	t.Logf("ux129 missing: %s %q; non-member: %s %q; seated in the refused money room: %s %q",
		missStatus, missErr, nonMemberStatus, nonMemberErr, refusedStatus, refusedErr)
	if nonMemberStatus != "rejected" || missStatus != "rejected" {
		t.Fatalf("the controls are not rejected: missing %s %q, non-member %s %q", missStatus, missErr, nonMemberStatus, nonMemberErr)
	}
	if refusedStatus != "rejected" {
		t.Errorf("RED: a member seated in a money room the fence refuses applies an ack over sync: %s %q", refusedStatus, refusedErr)
	}
	if refusedErr != nonMemberErr || missErr != nonMemberErr {
		t.Errorf("the three refusals differ: refused %q, non-member %q, missing %q", refusedErr, nonMemberErr, missErr)
	}
	if !strings.Contains(refusedErr, "row not found") {
		t.Errorf("the refusal is not the bare row-not-found answer: %q", refusedErr)
	}
	if n := ackRows(msgs[ux129RefusedRoom]); n != 0 {
		t.Errorf("RED: %d ack rows written in the refused money room", n)
	}
	if after := seqState(); after != before {
		t.Errorf("a refused ack took a seq: %s then %s", before, after)
	}

	for name, ch := range map[string]string{
		"an ordinary room": ux129OpenRoom,
		"a money room the payment trail alone admits":   ux129TrailRoom,
		"a money room the trail and branch reach admit": ux129BranchRoom,
	} {
		if st, er := push(msgs[ch]); st != "applied" {
			t.Errorf("%s still applies: %s %q", name, st, er)
		} else if n := ackRows(msgs[ch]); n != 1 {
			t.Errorf("%s: %d ack rows after an applied push", name, n)
		}
	}
}
