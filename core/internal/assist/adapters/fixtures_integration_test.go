//go:build integration

// fixtures_integration_test.go, the walkthrough's response bodies, produced by the REAL stack.
//
//	DATABASE_URL=... ASSIST431B_FIXTURE_DIR=$(pwd)/../walkthrough-431b/fixtures \
//	  go test -tags integration -count=1 ./internal/assist/adapters/ -run TestFixtures431B
//
// WHY THIS EXISTS RATHER THAN A FOLDER OF HAND-WRITTEN JSON
// ---------------------------------------------------------
// A screenshot taken over invented fixtures proves nothing about the backend: the fixture and the
// frontend agree because the same person wrote both. DIB-431's own walkthrough said so and dumped
// its fixtures from the real http.Handler; this one goes one layer further, because that is what
// PARK 3 changed.
//
// The bodies below come from:
//
//	the real assist.Handler  →  the real Registry.Run gate  →  the REAL adapters  →
//	the real domain services →  a real migrated Postgres with real rows
//
// So each shot proves: given what the server actually returns FOR REAL ROWS, as THIS user, this is
// what a user actually sees. The refused shot is the one that matters most, it is the same request
// as the answering shot, made by a caller at another branch, and the difference between the two
// images is the fence doing its job.
//
// It is skipped without the environment variable, so an ordinary integration run writes nothing.

package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestFixtures431B_ForTheWalkthrough(t *testing.T) {
	dir := os.Getenv("ASSIST431B_FIXTURE_DIR")
	if dir == "" {
		t.Skip("ASSIST431B_FIXTURE_DIR not set, not regenerating walkthrough fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	w := newWorld(t)
	ctx := t.Context()

	// A REAL user row, because /me resolves limits and spend against users.id (a uuid column), a
	// made-up caller id would come back as a driver error rather than as an allowance.
	var userID string
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, status)
		VALUES ('dib431b-walkthrough-' || gen_random_uuid() || '@example.invalid', 'Sam Marsh', 'ACTIVE')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("create walkthrough user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(ctx, `DELETE FROM assist_user_limits WHERE user_id = $1::uuid`, userID)
		_, _ = w.pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	// The real service, over the real repository and the real adapters. The provider is the stub -
	// none of these shots involves the chat surface, so no exchange is billed and no ledger row is
	// written (which matters: the ledger is append-only and a fixture run must not leave one).
	repo := assist.NewRepository(w.pool)
	inventorySvc := inventory.NewService(inventory.NewRepository(w.pool))
	orderSvc := order.NewService(order.NewRepository(w.pool))
	customerRepo := customer.NewRepository(w.pool)
	customerSvc := customer.NewService(customerRepo).WithWriter(customerRepo)
	creditSvc := billing.NewCreditService(billing.NewRepository(w.pool))
	paymentSvc := payments.NewService(payments.NewRepository(w.pool), payments.NewStubGateway())

	svc := assist.NewService(
		assist.NewRegistry(assist.Deps{
			Payments:  NewPaymentReader(paymentSvc),
			Orders:    NewOrderReader(orderSvc),
			Stock:     NewStockReader(inventorySvc),
			Customers: NewCustomerReader(customerSvc, creditSvc),
		}, repo),
		assist.NewStubProvider(), repo, repo, repo, nil,
	).WithStaff(repo)

	// Guards-swap carry note (HH-3.2 fold): the pass-through stubs became the
	// kit's real Guards; the branch-fence shots below carry their own
	// stamped contexts through the direct-handler drives the file uses.
	mux := http.NewServeMux()
	sg := testkit.NewStaffGuards(t)
	assist.NewHandler(svc, nil).RegisterRoutes(mux, sg.Guards)

	// The two callers. Same role, same tier, DIFFERENT branch, which is the whole point of the
	// refused shot: nothing about the two requests differs except who is asking.
	trenton := identity.StaffContext{
		UserID: userID, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{w.trentonID: true},
	}
	kingston := trenton
	kingston.Branches = map[string]bool{w.kingstonID: true}

	write := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, %s", name, rec.Code, rec.Body.String())
		}
		var pretty any
		if err := json.Unmarshal(rec.Body.Bytes(), &pretty); err != nil {
			t.Fatalf("%s is not JSON: %v", name, err)
		}
		b, _ := json.MarshalIndent(pretty, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	get := func(path string, sc identity.StaffContext) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil).WithContext(identity.ContextWithStaff(ctx, sc))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}
	run := func(sc identity.StaffContext, command string, args map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"command": command, "args": args})
		r := httptest.NewRequest("POST", "/api/v1/assist/command", strings.NewReader(string(body))).
			WithContext(identity.ContextWithStaff(ctx, sc))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}

	write("commands.json", get("/api/v1/assist/commands", trenton))
	write("me.json", get("/api/v1/assist/me", trenton))

	// The five commands, as the Trenton manager, over the Trenton rows.
	write("stock-answer.json", run(trenton, "stock.lookup", map[string]string{"product": w.stockedSKU}))
	write("order-answer.json", run(trenton, "order.lookup", map[string]string{"order_no": w.trentonOrderNo}))
	write("balance-answer.json", run(trenton, "customer.balance", map[string]string{"customer": w.trentonCustNo}))
	write("activity-answer.json", run(trenton, "customer.activity", map[string]string{"customer": w.trentonCustNo}))
	write("payment-answer.json", run(trenton, "payment.lookup", map[string]string{"reference": w.trentonPayNo}))

	// THE REFUSED SHOT. The SAME request the answering order shot made, asked by the manager at the
	// other branch. A 200 with an empty answer, not a red box, not a 403, because "there is no
	// order by that number that you can see" is a true sentence and the alternative would confirm
	// the order exists.
	refused := run(kingston, "order.lookup", map[string]string{"order_no": w.trentonOrderNo})
	write("refused-answer.json", refused)
	var body map[string]any
	_ = json.Unmarshal(refused.Body.Bytes(), &body)
	if body["empty"] != true {
		t.Fatalf("the refusal fixture is not a refusal: %s", refused.Body.String())
	}

	// And the answering twin, so the two images can be compared honestly: same command, same number.
	// (order-answer.json above IS that twin; re-read here so the two are asserted as a PAIR, a
	// refusal shot next to an answer shot is only evidence if both were true at the same moment.)
	_ = json.Unmarshal(get("/api/v1/assist/me", trenton).Body.Bytes(), &body)
	answered := run(trenton, "order.lookup", map[string]string{"order_no": w.trentonOrderNo})
	_ = json.Unmarshal(answered.Body.Bytes(), &body)
	if body["empty"] == true {
		t.Fatalf("the answering twin is empty: %s", answered.Body.String())
	}

	// The fixture values the harness needs to type into the form, written out rather than
	// hard-coded in the harness, since they carry a per-run tag.
	args := map[string]string{
		"product":       w.stockedSKU,
		"order_no":      w.trentonOrderNo,
		"customer":      w.trentonCustNo,
		"reference":     w.trentonPayNo,
		"customer_name": w.trentonCustName,
	}
	b, _ := json.MarshalIndent(args, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "args.json"), append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write args.json: %v", err)
	}
	fmt.Fprintf(os.Stderr, "walkthrough-431b fixtures written to %s\n", dir)
}
