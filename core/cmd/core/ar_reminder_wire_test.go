package main

// ar_reminder_wire_test.go pins the AR reminder transport's production
// wiring at the composition root (HH-185 round 2): the module's own
// suites prove the unwired default over the nil sender, but after
// wire_tops names its transport in words, nothing in them would catch the
// argument going back to billing.StubSender{}, which reintroduces the
// defect (every send DELIVERED, the paper-path sweep counting phantoms)
// with every suite green. The round 1 review proved exactly that mutation
// survives. This test drives the FULL served surface over the fixture
// database, exactly as the binary wires it: the global general manager
// posts the send route for a customer the test plants (AP contact tagged,
// the policy tier raised to PRO for the duration, both put back), and the
// answer must be the unwired transport's own words: status FAILED,
// provider unwired, no delivered_at, and the stored row the same. Swap
// the wiring argument for the delivering stub and every assertion below
// fails; drop the wiring line and the route answers 503 instead of 201.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestComposition_HH185ReminderTransport_UnwiredAtTheServedSurface(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	ctx := context.Background()

	// The send route's capability gate reads the dealer policy's tier; stash it, run on PRO, put
	// it back. Everything else on the default row is the seed's own.
	repo := billing.NewARLandingRepository(graph.pool)
	stashed, err := repo.ReadARLandingPolicy(ctx, "")
	if err != nil {
		t.Fatalf("read policy: %v", err)
	}
	pro := stashed
	pro.Tier = billing.TierPro
	if err := repo.UpsertARLandingPolicy(ctx, pro); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	t.Cleanup(func() { _ = repo.UpsertARLandingPolicy(context.Background(), stashed) })

	// One contractor in branch A, with a tagged AP contact so the send has
	// a recipient without the fallback confirmation. The sends cascade from customers, so one
	// delete unwinds the rig.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	var customerID string
	if err := graph.pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, org_id)
		 VALUES ('HH185W-'||$1, 'HH 185 Wire Rig '||$1, 'CONTRACTOR', $2::uuid, $3::uuid)
		 RETURNING id::text`,
		suffix, testkit.BranchNorthAID, testkit.OrgNorthID).Scan(&customerID); err != nil {
		t.Fatalf("plant customer: %v", err)
	}
	if _, err := graph.pool.Exec(ctx,
		`INSERT INTO ar_ap_contacts (customer_id, contact_name, email)
		 VALUES ($1::uuid, 'AP Desk', 'ap@hh185wire.test')`, customerID); err != nil {
		t.Fatalf("plant AP contact: %v", err)
	}
	// An open invoice: a reminder to an account with nothing due is refused (HH-195), and this leg
	// is about the transport, so the account owes something.
	if _, err := graph.pool.Exec(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ('HH185W-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE - 40, CURRENT_DATE - 10,
		         12345, 0, 12345, 12345, now(), 'INVOICE')`,
		suffix, customerID, testkit.BranchNorthAID); err != nil {
		t.Fatalf("plant open invoice: %v", err)
	}
	t.Cleanup(func() {
		_, _ = graph.pool.Exec(context.Background(), `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = graph.pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ar/reminders", strings.NewReader(
		`{"customer_id":"`+customerID+`","channel":"EMAIL"}`))
	// The leg runs as the global general manager: the surface's write level
	// in the composition fixture's default role configuration starts at tier 1,
	// so the branch-scoped tier 2 principal is refused by the configured gate
	// before the transport is reached, a fact about the fixture's config, not
	// the wiring under test.
	req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, testkit.SubGeneralMgr))
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	rec.inner.ServeHTTP(rsp, req)

	if rsp.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: the send route must be wired and answer through the service (body %s)", rsp.Code, rsp.Body.String())
	}
	var sent billing.ReminderSend
	if err := json.Unmarshal(rsp.Body.Bytes(), &sent); err != nil {
		t.Fatalf("decode answer: %v (body %s)", err, rsp.Body.String())
	}
	if sent.Status != billing.SendFailed {
		t.Errorf("status = %s, want %s: the served surface ships the unwired transport, a send that never left the server must not answer DELIVERED", sent.Status, billing.SendFailed)
	}
	if sent.Provider != "unwired" {
		t.Errorf("provider = %q, want unwired: the composition root must not hand the reminder service the delivering stub", sent.Provider)
	}
	if sent.DeliveredAt != nil {
		t.Errorf("delivered_at stamped on a message that never left the server")
	}

	var stored string
	var storedDelivered *time.Time
	if err := graph.pool.QueryRow(ctx,
		`SELECT status::text, delivered_at FROM ar_reminder_sends WHERE customer_id = $1::uuid`,
		customerID).Scan(&stored, &storedDelivered); err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	if stored != billing.SendFailed || storedDelivered != nil {
		t.Errorf("stored row = %s / delivered_at %v, want FAILED and never delivered: the row the sweep and the cadence read must not say DELIVERED", stored, storedDelivered)
	}
}
