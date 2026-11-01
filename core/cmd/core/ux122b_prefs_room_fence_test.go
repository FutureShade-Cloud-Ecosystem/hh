package main

// ux122b_prefs_room_fence_test.go: UX-122b. The notify_prefs sync write checks
// the seat and the room fence (UX-122), but the composition left the store's
// fence unbound, so a member seated in a money room the payment trail and
// branch reach refuse could still write their prefs there over sync while
// REST SetNotifyPrefs refused them. The leg drives the full served surface the
// binary wires, over the migrated scratch database, and holds three things:
// the refused-by-fence answer, the non-member answer (byte for byte the same),
// and a member the fence admits (still applied). Gated on
// HH_CORE_TEST_DATABASE_URL like every composition suite.

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
	ux122bMoneyRoom  = "a5000000-0000-4000-9000-000000000449" // payments-trenton, member_roles list
	ux122bOpenRoom   = "a5000000-0000-4000-9000-000000000003" // office, no member_roles
	ux122bUnseatRoom = "a5000000-0000-4000-9000-000000000002" // yard-kingston, caller not seated
)

func TestSyncPrefsWriteHonoursTheMoneyRoomFenceAtTheComposition(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	pool := graph.pool
	ctx := context.Background()

	var uid string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE platform_user_id = $1`, testkit.SubInsideSales).Scan(&uid); err != nil {
		t.Fatalf("the staff fixture user: %v", err)
	}
	// Seat the caller in the money room and the open room; unseat the third.
	for _, ch := range []string{ux122bMoneyRoom, ux122bOpenRoom} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO msg_members (channel_id, user_id, role, source) VALUES ($1::uuid, $2::uuid, 'MEMBER', 'EXPLICIT')
			 ON CONFLICT (channel_id, user_id) DO NOTHING`, ch, uid); err != nil {
			t.Fatalf("seat in %s: %v", ch, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ux122bUnseatRoom, uid); err != nil {
		t.Fatalf("unseat: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, ch := range []string{ux122bMoneyRoom, ux122bOpenRoom} {
			_, _ = pool.Exec(c, `DELETE FROM msg_notification_prefs WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ch, uid)
			_, _ = pool.Exec(c, `DELETE FROM msg_members WHERE channel_id = $1::uuid AND user_id = $2::uuid`, ch, uid)
		}
	})

	push := func(channel string) (status, errText string) {
		t.Helper()
		body := fmt.Sprintf(`{"ops":[{"client_id":%q,"op":"create","payload":{"channel_id":%q,"desktop_enabled":true}}]}`,
			uuid.NewString(), channel)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/notify_prefs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, testkit.SubInsideSales))
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		if rsp.Code != http.StatusOK {
			t.Fatalf("push to %s: status %d body %s", channel, rsp.Code, rsp.Body.String())
		}
		var out struct {
			Results []struct {
				Status string  `json:"status"`
				Error  *string `json:"error"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rsp.Body.Bytes(), &out); err != nil || len(out.Results) != 1 {
			t.Fatalf("push to %s: unreadable answer %s (%v)", channel, rsp.Body.String(), err)
		}
		e := ""
		if out.Results[0].Error != nil {
			e = *out.Results[0].Error
		}
		return out.Results[0].Status, e
	}

	nonMemberStatus, nonMemberErr := push(ux122bUnseatRoom)
	if nonMemberStatus != "rejected" {
		t.Fatalf("the non-member control is not rejected: %s %q", nonMemberStatus, nonMemberErr)
	}
	moneyStatus, moneyErr := push(ux122bMoneyRoom)
	t.Logf("ux122b non-member: %s %q; seated in the refused money room: %s %q", nonMemberStatus, nonMemberErr, moneyStatus, moneyErr)
	if moneyStatus != "rejected" {
		t.Errorf("RED: a member seated in a money room the fence refuses applies a prefs write over sync: %s %q", moneyStatus, moneyErr)
	}
	if moneyErr != nonMemberErr {
		t.Errorf("the refused-by-fence answer differs from the non-member answer: %q vs %q", moneyErr, nonMemberErr)
	}

	openStatus, openErr := push(ux122bOpenRoom)
	if openStatus != "applied" {
		t.Errorf("a member the fence admits still applies: %s %q", openStatus, openErr)
	}
}
