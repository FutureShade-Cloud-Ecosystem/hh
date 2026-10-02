package main

// sync_audit_wire_test.go: the HH-193 composition proof of the sync audit
// row's actor. A real tenant bearer and a real staff bearer push through
// the served sync route, wrapped the way serve wraps it (the audit
// middleware around the whole registered surface), over the migrated
// scratch database, and the test reads the SYNC_PUSH rows back out of
// audit_log: a tenant row has a NULL actor_id and the separator-encoded
// subject under changes.actor_subject; a staff row has the linked
// users.id and no actor_subject. Gated on HH_CORE_TEST_DATABASE_URL like
// every composition suite.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type syncWireRow struct {
	actorID *string
	changes map[string]any
}

func TestSyncPushAuditRowsCarryTheActorThroughServe(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	served := graph.money.auditSvc.Middleware(
		bridgedAuthn{verify: graph.verify, link: graph.link})(rec.inner)
	ctx := context.Background()
	pool := graph.pool

	// The tenant's own card, planted for the applied tombstone; every
	// request below carries an id under the fx-synca- prefix and the
	// cleanup removes exactly those rows and the card.
	cardID := "3ef10000-0000-4000-8000-0000000193c1"
	var seq int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO scope_sequences (scope) VALUES ('orders')
		 ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
		 RETURNING next_value`).Scan(&seq); err != nil {
		t.Fatalf("draw seq: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan (id, org_id, tenant_id, name, stage, seq)
		VALUES ($1, $2, $3, 'FX audit actor card', 'plan', $4)`,
		cardID, testkit.OrgNorthID, testkit.TenantAlphaID, seq); err != nil {
		t.Fatalf("plant card: %v", err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM audit_log WHERE request_id LIKE 'fx-synca-%'`,
			`DELETE FROM plan WHERE id = '` + cardID + `'`,
		} {
			if _, err := pool.Exec(context.Background(), stmt); err != nil {
				t.Fatalf("cleanup: %v", err)
			}
		}
	})

	push := func(reqID, scope string, header map[string]string, op, id string) {
		t.Helper()
		body := fmt.Sprintf(`{"ops":[{"client_id":%q,"op":%q,"payload":{"id":%q}}]}`,
			reqIDClientKey(reqID), op, id)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sync/"+scope, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-Id", reqID)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rsp := httptest.NewRecorder()
		served.ServeHTTP(rsp, req)
		if rsp.Code != http.StatusOK {
			t.Fatalf("%s: push status %d body %s", reqID, rsp.Code, rsp.Body.String())
		}
	}
	syncRow := func(reqID string) syncWireRow {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var r syncWireRow
			var raw string
			err := pool.QueryRow(ctx,
				`SELECT actor_id::text, changes::text FROM audit_log WHERE request_id = $1 AND action = 'SYNC_PUSH'`,
				reqID).Scan(&r.actorID, &raw)
			if err == nil {
				_ = json.Unmarshal([]byte(raw), &r.changes)
				return r
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: no SYNC_PUSH row: %v", reqID, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	tenant := map[string]string{"Authorization": "Bearer " + env.minter.TenantToken(t, testkit.SubPortalAdminAlpha, testkit.TenantAlphaID)}
	staff := map[string]string{
		"Authorization":  "Bearer " + env.minter.StaffToken(t, testkit.SubInsideSales),
		authz.HeaderXOrg: testkit.OrgNorthSlug,
	}
	// subject encoding of "fxportal-admin-alpha", six characters a group.
	wantSubject := "fxport:al-adm:in-alp:ha"
	if got := separatorEncodeSubject(testkit.SubPortalAdminAlpha); got != wantSubject {
		t.Fatalf("fixture subject %q encodes to %q, test expects %q", testkit.SubPortalAdminAlpha, got, wantSubject)
	}

	assertTenant := func(reqID, outcome string) {
		t.Helper()
		r := syncRow(reqID)
		if r.actorID != nil {
			t.Errorf("%s: tenant row actor_id %q, want NULL", reqID, *r.actorID)
		}
		if got, _ := r.changes["actor_subject"].(string); got != wantSubject {
			t.Errorf("%s: tenant row actor_subject %q, want %q (%v)", reqID, got, wantSubject, r.changes)
		}
		if r.changes["status"] != outcome {
			t.Errorf("%s: status %v, want %s", reqID, r.changes["status"], outcome)
		}
	}

	// Tenant, applied: the tombstone of the tenant's own card.
	push("fx-synca-tenant-applied", "orders", tenant, "delete", cardID)
	assertTenant("fx-synca-tenant-applied", "applied")

	// Tenant, rejected: a tombstone of a card that does not exist.
	push("fx-synca-tenant-rejected", "orders", tenant, "delete", "3ef10000-0000-4000-8000-0000000193c2")
	assertTenant("fx-synca-tenant-rejected", "rejected")

	// Staff: the linked users.id, no subject key. An update of a row that
	// does not exist is the rejected row on the staff scope.
	push("fx-synca-staff-rejected", "pick_tasks", staff, "update", "3ef10000-0000-4000-8000-0000000193c3")
	r := syncRow("fx-synca-staff-rejected")
	if r.actorID == nil || *r.actorID != testkit.UserInsideSalesID {
		t.Errorf("staff row actor_id %v, want the linked users.id %s", r.actorID, testkit.UserInsideSalesID)
	}
	if _, ok := r.changes["actor_subject"]; ok {
		t.Errorf("staff row carries actor_subject: %v", r.changes)
	}
}

// reqIDClientKey derives a uuid-shaped idempotency key per probe from the
// request id, so reruns never collide on the dedup key.
func reqIDClientKey(reqID string) string {
	h := uint32(2166136261)
	for _, c := range []byte(reqID + time.Now().Format("150405.000000")) {
		h = (h ^ uint32(c)) * 16777619
	}
	return fmt.Sprintf("%08x-0000-4000-8000-%012x", h, h)
}
