//go:build integration

// freightraise_integration_test.go, the second-truck approval gate, end to end, against real rows.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run FreightRaise ./internal/dispatch/
//
// EVERY ACCEPTANCE CLAUSE IN THIS FILE IS A NAMED TEST, and each one asserts the thing that actually
// costs money if it breaks:
//
//	TestFreightRaise_SplitWithinTheQuotedFreightSailsThrough
//	TestFreightRaise_AddedTruckLandsInTheQueueAndTheSplitIsNotApplied
//	TestFreightRaise_ForgedApprovalFromANonApprover403sAndWritesNothing
//	TestFreightRaise_ApprovalAppliesTheParkedSplitAndIsAudited
//	TestFreightRaise_RefusalAppliesNothingAndIsAudited
//	TestFreightRaise_OrderWithNoQuoteBaselineIsNeverGated
//
// WHY THE WIRING HERE IS THE REAL WIRING. The harness builds dispatch's service and identity's
// service and points them at each other exactly as cmd/server/main.go does, the filer over the real
// identity.Service, the decider over the real dispatch.Service, both crossing a real pgx transaction.
// A fake on either side would prove that two mocks agree; what has to be true is that an APPROVED
// stamp and a row of freight land or fail together on one connection, which only Postgres can say.
//
// FIXTURES RESTORE WHAT THEY TOUCH. seedOrder's cleanup already removes the order and its stops;
// every test here additionally removes the raises and approvals it caused, because both tables are
// asserted on by other suites (and permission_approval_requests carries a FK from the raise, so the
// order matters).

package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The three seeded people this file needs, and the role each one is here to be.
const (
	// A ruled rail approver (GENERAL_MANAGER, GLOBAL). The only kind of person who may decide this.
	frtApproverEmail = "alex.morgan@dibbits.example"
	// A YARD MANAGER scoped to Kingston. The forgery: somebody who CAN reach the branch, so the only
	// thing that can deny them is the role allowlist itself, which is the isolation this proves.
	frtForgerEmail = "casey.doyle@dibbits.example"
	frtPassword    = "dibbits-demo-2026"
)

// - the seams, wired as production wires them -

// frtValues satisfies OrderValueReader off the real orders table.
//
// OrderQuotedFreightCents SCANS INTO A POINTER AND DOES NOT TOUCH A `<= 0` RULE, the same shape the
// composition root's adapter uses, and for the reason 0194's header gives at length: NULL means the
// order never came from a quote and must never be gated, whereas 0 means the customer was promised
// free delivery and is precisely the case the gate exists for. A fixture that conflated them would
// make the whole file pass while the shipped rule was wrong in the direction that charges people.
type frtValues struct{ pool *pgxpool.Pool }

func (v frtValues) OrderValueCents(ctx context.Context, orderID string) (int64, bool, error) {
	return v.readPositive(ctx, orderID, "total_cents")
}

func (v frtValues) OrderFreightCents(ctx context.Context, orderID string) (int64, bool, error) {
	return v.readPositive(ctx, orderID, "freight_cents")
}

func (v frtValues) readPositive(ctx context.Context, orderID, col string) (int64, bool, error) {
	var cents int64
	if err := v.pool.QueryRow(ctx, `SELECT `+col+` FROM orders WHERE id = $1`, orderID).Scan(&cents); err != nil {
		return 0, false, err
	}
	if cents <= 0 {
		return 0, false, nil
	}
	return cents, true, nil
}

func (v frtValues) OrderQuotedFreightCents(ctx context.Context, orderID string) (int64, bool, error) {
	var cents *int64
	if err := v.pool.QueryRow(ctx, `SELECT quoted_freight_cents FROM orders WHERE id = $1`, orderID).Scan(&cents); err != nil {
		return 0, false, err
	}
	if cents == nil {
		return 0, false, nil
	}
	return *cents, true, nil
}

// frtOrderFreight satisfies OrderFreightWriter, the one write that moves an order's own freight when
// a raise is applied, on the caller's transaction.
type frtOrderFreight struct{}

func (frtOrderFreight) SetOrderFreightTx(ctx context.Context, tx pgx.Tx, orderID string, cents int64) error {
	_, err := tx.Exec(ctx, `UPDATE orders SET freight_cents = $2 WHERE id = $1`, orderID, cents)
	return err
}

// frtFiler satisfies FreightApprovalFiler over the REAL identity.Service, no fake, because the thing
// being proven is that a request lands on permission_approval_requests and passes its CHECK.
type frtFiler struct{ svc *identity.Approvals }

func (f frtFiler) FileFreightApproval(ctx context.Context, raiseID, branchID, requestedBy, detail string) (string, error) {
	req, err := f.svc.FileFreightApproval(ctx, raiseID, branchID, requestedBy, detail)
	if err != nil {
		return "", err
	}
	return req.ID, nil
}

// frtDecider satisfies identity.FreightDecider over the REAL dispatch.Service, the return leg, with
// the same error translation main.go performs so a refusal reads as a sentence and not a 500.
type frtDecider struct{ svc *Service }

func (d frtDecider) ApplyFreightRaiseTx(ctx context.Context, tx pgx.Tx, raiseID, deciderID string) error {
	return frtAsIdentityError(d.svc.ApplyFreightRaiseTx(ctx, tx, raiseID, deciderID))
}

func (d frtDecider) RefuseFreightRaiseTx(ctx context.Context, tx pgx.Tx, raiseID, deciderID string) error {
	return frtAsIdentityError(d.svc.RefuseFreightRaiseTx(ctx, tx, raiseID, deciderID))
}

func frtAsIdentityError(err error) error {
	if err == nil {
		return nil
	}
	if status, msg, ok := ErrorHTTPStatus(err); ok {
		return identity.NewClientError(status, msg)
	}
	return err
}

// - the harness -

type frtHarness struct {
	pool     *pgxpool.Pool
	svc      *Service
	identity *identity.Approvals
	mux      *http.ServeMux
}

func newFreightHarness(t *testing.T) *frtHarness {
	t.Helper()
	pool := intPool(t)
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)

	// CARRY NOTE (HH-3.2 B5): the pin built the ERP identity Service and
	// drove the decide route through its handler with an HS256 login
	// token; that session world stayed behind. Under the carry the rail
	// is *identity.Approvals and the decide path takes the bridged caller
	// the guard resolved (the A3 re-point). The guard's own resolution is
	// not exercised here: the seed links no platform user and plants no
	// staff_role_grant rows, so the harness stamps the seeded person from
	// the seed's scaffold tables (seededCaller) and hands the caller in
	// directly. What stays proven is the rail's role allowlist and branch
	// reach against the database, which the login leg used to buy through
	// a token.
	identitySvc := identity.NewApprovals(identity.NewApprovalRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithOrders(testOrders{pool: pool}).
		WithOrderValues(frtValues{pool: pool}).
		WithFreightApprovals(frtFiler{svc: identitySvc}).
		WithOrderFreight(frtOrderFreight{}).
		WithAudit(auditSvc)
	// The cycle, broken the way the wire function breaks it: mutate the already-constructed pointer.
	identitySvc.WithFreightDecider(frtDecider{svc: svc})

	mux := http.NewServeMux()
	return &frtHarness{pool: pool, svc: svc, identity: identitySvc, mux: mux}
}

// decide drives the rail's decide path as the named seeded person,
// returning the HTTP status the handler would answer for the same error.
func (h *frtHarness) decide(t *testing.T, email, approvalID string, approve bool, reason string) int {
	t.Helper()
	caller := h.seededCaller(t, email)
	err := h.identity.DecideApproval(context.Background(), approvalID, caller, approve, reason)
	if err == nil {
		return http.StatusOK
	}
	if status, ok := identity.ApprovalErrorStatus(err); ok {
		return status
	}
	return http.StatusInternalServerError
}

// seededCaller stamps the seeded person as the guard would, from the role and branch grants the seed
// plants (users, user_roles, roles, user_branch_grants). The seed links no platform user and plants
// no staff_role_grant rows, so the rail's canonical read (ResolveStaff) resolves every seeded person
// to no authority and refuses even the ruled approver; the guard's resolution is not what this file
// proves, the rail's role allowlist is.
func (h *frtHarness) seededCaller(t *testing.T, email string) identity.StaffContext {
	t.Helper()
	ctx := context.Background()
	caller := identity.StaffContext{Branches: map[string]bool{}, Status: identity.StatusActive}
	var tier int
	var scope string
	if err := h.pool.QueryRow(ctx, `
SELECT u.id::text, r.code, r.tier, r.scope
  FROM users u JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id
 WHERE u.email = $1 AND u.deleted_at IS NULL
 ORDER BY r.tier LIMIT 1`, email).Scan(&caller.UserID, &caller.RoleCode, &tier, &scope); err != nil {
		t.Skipf("seeded user %s has no role: %v", email, err)
	}
	caller.Tier = identity.Tier(tier)
	caller.Scope = identity.Scope(scope)
	rows, err := h.pool.Query(ctx,
		`SELECT branch_id::text FROM user_branch_grants WHERE user_id = $1 AND deleted_at IS NULL`, caller.UserID)
	if err != nil {
		t.Fatalf("read branch grants for %s: %v", email, err)
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatalf("scan branch grant: %v", err)
		}
		caller.Branches[b] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("branch grants for %s: %v", email, err)
	}
	return caller
}

func (h *frtHarness) userID(t *testing.T, email string) string {
	t.Helper()
	var id string
	if err := h.pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE email=$1`, email).Scan(&id); err != nil {
		t.Skipf("seeded user %s missing: %v", email, err)
	}
	return id
}

// dispatcherCtx stamps the person who ASKS for the extra truck: a logistics manager, who is a manager
// (so the load-value policy lets them see and divide freight) and is NOT a rail approver (so they can
// never decide their own raise, over and above four-eyes).
func (h *frtHarness) dispatcherCtx(t *testing.T) context.Context {
	t.Helper()
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: seededUser(t, h.pool), RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
}

// seedQuotedOrder inserts an order with a real baseline and a real opening actual, plus its first
// load, and registers the cleanup for everything this feature can hang off it.
//
// quoted is a POINTER so the NULL case, "this order never came from a quote", is expressible, which
// is the whole reason 0194 made the column nullable.
func (h *frtHarness) seedQuotedOrder(t *testing.T, branchID string, quoted *int64, freight int64) (orderID, stopID string) {
	t.Helper()
	ctx := context.Background()
	orderID = seedOrder(t, h.pool, branchID, "DELIVERY", "CONFIRMED")
	if _, err := h.pool.Exec(ctx,
		`UPDATE orders SET freight_cents = $2, quoted_freight_cents = $3 WHERE id = $1`,
		orderID, freight, quoted); err != nil {
		t.Fatalf("seed freight: %v", err)
	}
	if err := h.pool.QueryRow(ctx, `
INSERT INTO delivery_stops (branch_id, order_id, scheduled_date, status, stop_no, load_seq, load_count, freight_cents)
VALUES ($1, $2, $3::date, 'READY', 1, 1, 1, $4) RETURNING id::text`,
		branchID, orderID, today(), freight).Scan(&stopID); err != nil {
		t.Fatalf("seed stop: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		var approvals []string
		rows, err := h.pool.Query(c, `SELECT COALESCE(approval_id::text,'') FROM delivery_freight_raises WHERE order_id=$1`, orderID)
		if err == nil {
			for rows.Next() {
				var a string
				if rows.Scan(&a) == nil && a != "" {
					approvals = append(approvals, a)
				}
			}
			rows.Close()
		}
		_, _ = h.pool.Exec(c, `DELETE FROM delivery_freight_raises WHERE order_id=$1`, orderID)
		for _, a := range approvals {
			_, _ = h.pool.Exec(c, `DELETE FROM permission_approval_requests WHERE id=$1`, a)
		}
		_, _ = h.pool.Exec(c, `DELETE FROM audit_log WHERE entity_id=$1 AND action LIKE 'DELIVERY_FREIGHT%'`, orderID)
	})
	return orderID, stopID
}

// - the shape of the world, measured -

// frtWorld is every fact an acceptance clause needs to say "nothing was applied": what the order
// charges, how many trucks it is on, what each one carries, and what the queue says.
type frtWorld struct {
	OrderFreight  int64
	Loads         int
	ShareSum      int64
	RaiseState    string
	ApprovalState string
}

func (h *frtHarness) world(t *testing.T, orderID string) frtWorld {
	t.Helper()
	ctx := context.Background()
	var w frtWorld
	if err := h.pool.QueryRow(ctx, `SELECT freight_cents FROM orders WHERE id=$1`, orderID).Scan(&w.OrderFreight); err != nil {
		t.Fatalf("read order freight: %v", err)
	}
	if err := h.pool.QueryRow(ctx, `
SELECT count(*)::int, COALESCE(sum(freight_cents),0)
  FROM delivery_stops WHERE order_id=$1 AND status NOT IN ('CANCELLED','FAILED')`, orderID).Scan(&w.Loads, &w.ShareSum); err != nil {
		t.Fatalf("read loads: %v", err)
	}
	_ = h.pool.QueryRow(ctx, `SELECT state FROM delivery_freight_raises WHERE order_id=$1 ORDER BY requested_at DESC LIMIT 1`, orderID).Scan(&w.RaiseState)
	_ = h.pool.QueryRow(ctx, `
SELECT a.status FROM permission_approval_requests a
  JOIN delivery_freight_raises r ON r.approval_id = a.id
 WHERE r.order_id=$1 ORDER BY r.requested_at DESC LIMIT 1`, orderID).Scan(&w.ApprovalState)
	return w
}

func (w frtWorld) String() string {
	return fmt.Sprintf("order_freight=%d loads=%d shares=%d raise=%q approval=%q",
		w.OrderFreight, w.Loads, w.ShareSum, w.RaiseState, w.ApprovalState)
}

func cents(v int64) *int64 { return &v }

// - ACCEPTANCE 1 · a split WITHIN the quoted freight sails through -

// The client's own carve-out. Three trucks instead of one changes what each load's paperwork says and
// changes nothing the customer pays, so it must not touch the approvals queue at all, and a gate
// that fired here would refuse the ordinary work of a dispatch office all day.
func TestFreightRaise_SplitWithinTheQuotedFreightSailsThrough(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)

	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 3})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if res.Parked != nil {
		t.Fatalf("an in-quote split must not be parked: %+v", res.Parked)
	}
	if len(res.Loads) != 3 {
		t.Fatalf("want 3 loads, got %d", len(res.Loads))
	}
	got := h.world(t, orderID)
	if got.Loads != 3 || got.ShareSum != 26000 || got.OrderFreight != 26000 {
		t.Fatalf("the split did not land: %s", got)
	}
	if got.RaiseState != "" {
		t.Fatalf("an in-quote split filed a raise: %s", got)
	}
	// The shares are the server's arithmetic and no cent is lost or invented: 26000 over 3.
	var shares []int64
	rows, err := h.pool.Query(context.Background(),
		`SELECT freight_cents FROM delivery_stops WHERE order_id=$1 ORDER BY load_seq`, orderID)
	if err != nil {
		t.Fatalf("read shares: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c int64
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan share: %v", err)
		}
		shares = append(shares, c)
	}
	if len(shares) != 3 || shares[0] != 8667 || shares[1] != 8667 || shares[2] != 8666 {
		t.Fatalf("the even split is wrong: %v", shares)
	}
}

// - ACCEPTANCE 2 · the added truck lands in the queue, and NOTHING is applied -

func TestFreightRaise_AddedTruckLandsInTheQueueAndTheSplitIsNotApplied(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)

	// Three trucks within the quote first, so "nothing was applied" below is measured against a world
	// that has already been split once, the state a real order is in when the fourth truck is needed.
	if _, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 3}); err != nil {
		t.Fatalf("in-quote split: %v", err)
	}
	before := h.world(t, orderID)

	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 4, FreightCents: cents(34000)})
	if err != nil {
		t.Fatalf("the raise must be accepted and parked, not refused: %v", err)
	}
	if res.Parked == nil {
		t.Fatal("a raise above the quoted freight was applied without an approval")
	}
	if res.Parked.State != RaiseParked {
		t.Fatalf("the proposal is not parked: %+v", res.Parked)
	}
	if res.Parked.ApprovalID == "" {
		t.Fatal("a parked raise with no approval row is a decision nobody can make")
	}
	if res.Parked.Money == nil || res.Parked.Money.ProposedCents != 34000 || res.Parked.Money.DeltaCents != 8000 {
		t.Fatalf("the dispatcher (a manager) must see what they asked for: %+v", res.Parked.Money)
	}
	// The answer describes the world as it STILL is.
	if res.FreightCents != 26000 || len(res.Loads) != 3 {
		t.Fatalf("a parked split must report the loads as they still stand, got freight=%d loads=%d",
			res.FreightCents, len(res.Loads))
	}

	// THE SPLIT IS NOT APPLIED. Not the fourth truck, not the shares, not the order's own charge.
	after := h.world(t, orderID)
	if after.Loads != before.Loads {
		t.Fatalf("a truck was minted before anybody approved it: before %s / after %s", before, after)
	}
	if after.ShareSum != before.ShareSum || after.OrderFreight != before.OrderFreight {
		t.Fatalf("freight moved before anybody approved it: before %s / after %s", before, after)
	}
	if after.RaiseState != RaiseParked || after.ApprovalState != "PENDING" {
		t.Fatalf("the raise is not waiting on the queue: %s", after)
	}

	// The approval row is on the ONE queue, with the ruled policy, and carries no money, because the
	// list it sits in is readable by every authenticated staff member with no money gate of its own.
	var policy, detail string
	var amount *int64
	if err := h.pool.QueryRow(context.Background(), `
SELECT a.policy_type, a.detail, (a.data->>'amount_cents')::bigint
  FROM permission_approval_requests a
  JOIN delivery_freight_raises r ON r.approval_id = a.id
 WHERE r.order_id = $1`, orderID).Scan(&policy, &detail, &amount); err != nil {
		t.Fatalf("read the filed approval: %v", err)
	}
	if policy != string(identity.PolicyFreightAboveQuote) {
		t.Fatalf("wrong policy on the queue: %q", policy)
	}
	if amount != nil {
		t.Fatalf("the approval row carries a figure (%d) into an ungated list", *amount)
	}
	if frtDetailLeaksMoney(detail) {
		t.Fatalf("the approval sentence leaks a customer's delivery charge: %q", detail)
	}
}

// frtDetailLeaksMoney is the leak check the queue row must pass: the approval sentence carries no
// money. The rule is unchanged from the bare substring hunt it replaces; what changed is that the
// check matches MONEY SHAPES and not bare digits, because the sentence's only digits belong to the
// identifiers it names (raiseDetail files the order number and nothing else) and the fixture order
// number is random: PR 632's run 40591154455 seeded #SO-DISPATCHTEST-5682680004, whose digits hold
// 8000, and the substring hunt tripped on the id though the sentence carries no money. A dollar
// sign anywhere, a decimal amount, or a forbidden cents figure standing as a whole word is money;
// 8000 inside 5682680004 is not, and a comma-grouped spelling of a forbidden
// figure (26,000) is money exactly as the plain spelling is. Matching the
// shapes keeps the rule exact without the check having
// to know every identifier the sentence may ever carry, which stripping ids would.
var (
	frtMoneyShape   = regexp.MustCompile(`\d+\.\d{2}`)
	frtWholeFigures = regexp.MustCompile(`\b(?:26000|26,000|34000|34,000|8000|8,000)\b`)
)

func frtDetailLeaksMoney(detail string) bool {
	return strings.Contains(detail, "$") || frtMoneyShape.MatchString(detail) || frtWholeFigures.MatchString(detail)
}

// - the flake, pinned -

// The fixture order number is random digits, so once in a while its digits hold one of the forbidden
// figures: PR 632's run 40591154455 seeded #SO-DISPATCHTEST-5682680004, whose number carries 8000,
// and the leak check tripped on the identifier though the sentence carries no money. The rule is
// unchanged, the approval sentence carries no money and the approval row carries no figure; this leg
// pins the flake, the exact order number from that run parks a raise and its sentence must pass the
// leak check.
func TestFreightRaise_TheLeakCheckReadsMoneyNotDigitsInsideAnId(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE orders SET order_no = $2 WHERE id = $1`, orderID, "#SO-DISPATCHTEST-5682680004"); err != nil {
		t.Fatalf("seed the order number from the flake's run: %v", err)
	}

	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 4, FreightCents: cents(34000)})
	if err != nil || res.Parked == nil {
		t.Fatalf("the raise did not park: %v", err)
	}
	var detail string
	if err := h.pool.QueryRow(context.Background(), `
SELECT a.detail FROM permission_approval_requests a
  JOIN delivery_freight_raises r ON r.approval_id = a.id
 WHERE r.order_id = $1`, orderID).Scan(&detail); err != nil {
		t.Fatalf("read the filed approval: %v", err)
	}
	if frtDetailLeaksMoney(detail) {
		t.Fatalf("the check read the order number's digits as money: %q", detail)
	}
}

// THE CHECK MUST STILL BITE. Whatever identifier the sentence also names, a dollar sign, a decimal
// amount, or a forbidden cents figure standing as a whole word is money on the ungated queue, and
// every one of those sentences must fail the check.
func TestFreightRaise_TheLeakCheckStillBitesOnRealMoney(t *testing.T) {
	bites := []struct{ name, detail string }{
		{"a dollar sign", "Freight on #SO-DISPATCHTEST-1041 is being raised above the $80.00 the customer was quoted."},
		{"a decimal amount", "Freight on #SO-DISPATCHTEST-1041 rises by 340.00 above the quoted delivery."},
		{"the whole token 8000", "Freight on #SO-DISPATCHTEST-1041 rises by 8000 cents above the quoted delivery."},
		{"the whole token 26000", "The delivery on #SO-DISPATCHTEST-1041 charges 26000 cents."},
		{"the whole token 34000", "The delivery on #SO-DISPATCHTEST-1041 charges 34000 cents."},
		{"the grouped figure 26,000", "The delivery on #SO-DISPATCHTEST-1041 charges 26,000 cents."},
		{"a grouped figure with a decimal part", "Freight on #SO-DISPATCHTEST-1041 rises by 8,000.00 cents above the quoted delivery."},
		{"a grouped figure inside a sentence", "Freight on #SO-DISPATCHTEST-1041 is being raised above the 34,000 cents the customer was quoted."},
		{"a bare dollar sign", "Freight on #SO-DISPATCHTEST-1041 is being raised above the delivery the customer was $ quoted."},
	}
	for _, tc := range bites {
		if !frtDetailLeaksMoney(tc.detail) {
			t.Fatalf("%s: the leak check let real money through: %q", tc.name, tc.detail)
		}
	}
	// And the sentence the queue actually files, whose only digits belong to the order number, is
	// not a leak.
	if frtDetailLeaksMoney(raiseDetail("#SO-DISPATCHTEST-5682680004", "Bay of Quinte Landscaping")) {
		t.Fatal("the check read the order number inside the real sentence as money")
	}
}

// - ACCEPTANCE 3 · a forged approval from a non-approver 403s server-side and writes nothing -

// Casey Doyle is a YARD MANAGER granted Kingston, and the fixture order is in Kingston, so
// CanAccessBranch is SATISFIED, the only thing left that can deny this decision is the rail-approver
// role allowlist. That isolation is the point: a test that used somebody who could not reach the
// branch would pass with the authority arm deleted.
//
// The request is hand-crafted and sent over HTTP with a real bearer token, so what is being proven is
// the SERVER's refusal and not a screen's. Byte-absence gates the display; this gates the act.
func TestFreightRaise_ForgedApprovalFromANonApprover403sAndWritesNothing(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)
	if _, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 1}); err != nil {
		t.Fatalf("baseline split: %v", err)
	}
	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 2, FreightCents: cents(34000)})
	if err != nil || res.Parked == nil {
		t.Fatalf("the raise did not park: %v %+v", err, res.Parked)
	}
	approvalID := res.Parked.ApprovalID
	before := h.world(t, orderID)

	// Both verbs, because a rejection is a write too: a non-approver must not be able to kill a raise
	// any more than they can grant one.
	for _, approve := range []bool{true, false} {
		if code := h.decide(t, frtForgerEmail, approvalID, approve, "forged"); code != http.StatusForbidden {
			t.Fatalf("a yard manager decided a freight raise (approve=%v): %d", approve, code)
		}
	}

	// RE-READ THE ROWS. A 403 that had already written is not a refusal.
	after := h.world(t, orderID)
	if after != before {
		t.Fatalf("a forged decision changed the world: before %s / after %s", before, after)
	}
	if after.RaiseState != RaiseParked || after.ApprovalState != "PENDING" {
		t.Fatalf("the raise must still be waiting after a forged decision: %s", after)
	}
	var decidedBy *string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT decided_by::text FROM permission_approval_requests WHERE id=$1`, approvalID).Scan(&decidedBy); err != nil {
		t.Fatalf("re-read approval: %v", err)
	}
	if decidedBy != nil {
		t.Fatalf("a forged decision stamped a decider: %q", *decidedBy)
	}

	// POSITIVE CONTROL. The same decide, from a ruled approver, is accepted, so the two
	// 403s above are about WHO ASKED and not about a path that refuses everybody.
	if code := h.decide(t, frtApproverEmail, approvalID, false, "control"); code != http.StatusOK {
		t.Fatalf("positive control failed: a general manager could not decide either: %d", code)
	}
}

// - ACCEPTANCE 4 · approval applies the parked split, atomically, and is audited -

func TestFreightRaise_ApprovalAppliesTheParkedSplitAndIsAudited(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)
	if _, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 3}); err != nil {
		t.Fatalf("in-quote split: %v", err)
	}
	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 4, FreightCents: cents(34000)})
	if err != nil || res.Parked == nil {
		t.Fatalf("the raise did not park: %v", err)
	}

	if code := h.decide(t, frtApproverEmail, res.Parked.ApprovalID, true, ""); code != http.StatusOK {
		t.Fatalf("approve: %d", code)
	}

	got := h.world(t, orderID)
	if got.Loads != 4 {
		t.Fatalf("the fourth truck was not minted by the approval: %s", got)
	}
	if got.ShareSum != 34000 {
		t.Fatalf("the loads do not carry the approved freight: %s", got)
	}
	if got.OrderFreight != 34000 {
		t.Fatalf("the order still charges the old freight, so the next split would undo this raise: %s", got)
	}
	if got.RaiseState != RaiseApplied || got.ApprovalState != "APPROVED" {
		t.Fatalf("the stamp and the state disagree: %s", got)
	}

	// The decider is on both rows, and it is the approver rather than the person who asked.
	var raiseDecider, approvalDecider string
	if err := h.pool.QueryRow(context.Background(), `
SELECT r.decided_by::text, a.decided_by::text
  FROM delivery_freight_raises r JOIN permission_approval_requests a ON a.id = r.approval_id
 WHERE r.order_id=$1`, orderID).Scan(&raiseDecider, &approvalDecider); err != nil {
		t.Fatalf("read deciders: %v", err)
	}
	gmID := h.userID(t, frtApproverEmail)
	if raiseDecider != gmID || approvalDecider != gmID {
		t.Fatalf("the decider is wrong: raise=%s approval=%s want %s", raiseDecider, approvalDecider, gmID)
	}

	// AUDITED, with the figures that are allowed on a trail row.
	assertFreightTrail(t, h, orderID, "DELIVERY_FREIGHT_RAISE_APPLIED", gmID, 34000, 8000)
	// …and the ask itself was recorded when it was made, by the person who made it.
	assertFreightTrail(t, h, orderID, "DELIVERY_FREIGHT_RAISE_PARKED", "", 34000, 8000)
}

// - ACCEPTANCE 5 · a refusal applies nothing, and is audited -

func TestFreightRaise_RefusalAppliesNothingAndIsAudited(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)
	if _, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 2}); err != nil {
		t.Fatalf("in-quote split: %v", err)
	}
	before := h.world(t, orderID)

	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 3, FreightCents: cents(40000)})
	if err != nil || res.Parked == nil {
		t.Fatalf("the raise did not park: %v", err)
	}
	if code := h.decide(t, frtApproverEmail, res.Parked.ApprovalID, false, "the customer was quoted this delivery"); code != http.StatusOK {
		t.Fatalf("reject: %d", code)
	}

	after := h.world(t, orderID)
	if after.Loads != before.Loads || after.ShareSum != before.ShareSum || after.OrderFreight != before.OrderFreight {
		t.Fatalf("a refusal changed the world: before %s / after %s", before, after)
	}
	if after.RaiseState != RaiseRefused || after.ApprovalState != "REJECTED" {
		t.Fatalf("the refusal did not reach the parked proposal: %s", after)
	}
	assertFreightTrail(t, h, orderID, "DELIVERY_FREIGHT_RAISE_REFUSED", h.userID(t, frtApproverEmail), 40000, 14000)

	// The refusal FREES THE SLOT: the order can be asked about again, which is the whole reason the
	// decline is a write rather than a no-op.
	again, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 3, FreightCents: cents(30000)})
	if err != nil {
		t.Fatalf("a refused order must be askable again: %v", err)
	}
	if again.Parked == nil {
		t.Fatal("the second ask must park too")
	}
}

// - ACCEPTANCE 6 · an order with no quote baseline is never gated -

// NULL means the order never came from a quote, a counter sale, an order typed in directly, so
// there is nothing for a raise to be above and the queue must never see it. The companion assertion
// is the one that makes this non-trivial: the SAME figures on an order QUOTED ZERO do gate, so this
// test cannot be passing because the gate is dead.
func TestFreightRaise_OrderWithNoQuoteBaselineIsNeverGated(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)

	unquoted, _ := h.seedQuotedOrder(t, branchKGN, nil, 26000)
	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: unquoted, Loads: 2, FreightCents: cents(90000)})
	if err != nil {
		t.Fatalf("an unquoted order must split freely: %v", err)
	}
	if res.Parked != nil {
		t.Fatalf("an order with no quoted baseline was gated: %+v", res.Parked)
	}
	got := h.world(t, unquoted)
	if got.OrderFreight != 90000 || got.ShareSum != 90000 || got.Loads != 2 {
		t.Fatalf("the ungated raise did not land: %s", got)
	}

	// The control: quoted ZERO is a baseline, and the same act on it IS gated. 0194's header exists
	// because a `<= 0 is unknown` rule would collapse these two into one.
	freeDelivery, _ := h.seedQuotedOrder(t, branchKGN, cents(0), 0)
	gated, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: freeDelivery, Loads: 2, FreightCents: cents(90000)})
	if err != nil {
		t.Fatalf("the quoted-zero raise should park, not refuse: %v", err)
	}
	if gated.Parked == nil {
		t.Fatal("a customer promised free delivery was charged freight with nobody asked")
	}
}

// - the second ask, and the money gate on the card -

// One parked raise per order. A second, DIFFERENT ask is refused in words; an identical one converges
// on the raise already waiting, so a double click does not put two competing figures in front of two
// approvers.
func TestFreightRaise_OneParkedRaisePerOrder(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)
	if _, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 1}); err != nil {
		t.Fatalf("baseline split: %v", err)
	}
	first, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 2, FreightCents: cents(34000)})
	if err != nil || first.Parked == nil {
		t.Fatalf("first ask: %v", err)
	}
	same, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 2, FreightCents: cents(34000)})
	if err != nil {
		t.Fatalf("an identical re-ask must converge, not refuse: %v", err)
	}
	if same.Parked == nil || same.Parked.ID != first.Parked.ID {
		t.Fatalf("an identical re-ask filed a second raise: %+v", same.Parked)
	}
	if _, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 2, FreightCents: cents(41000)}); err == nil {
		t.Fatal("a competing ask must be refused while one is waiting")
	}
	var n int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*)::int FROM delivery_freight_raises WHERE order_id=$1`, orderID).Scan(&n); err != nil {
		t.Fatalf("count raises: %v", err)
	}
	if n != 1 {
		t.Fatalf("want exactly one raise row, got %d", n)
	}
}

// The card an approver reads: the money is present for a manager and ABSENT, no key, no zero, no
// null, for a frontline caller, through the real route.
func TestFreightRaise_CardMoneyIsAbsentForARefusedRole(t *testing.T) {
	h := newFreightHarness(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)
	ctx := h.dispatcherCtx(t)
	if _, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 1}); err != nil {
		t.Fatalf("baseline split: %v", err)
	}
	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 2, FreightCents: cents(34000)})
	if err != nil || res.Parked == nil {
		t.Fatalf("park: %v", err)
	}

	manager, err := h.svc.FreightRaiseByID(ctx, res.Parked.ID)
	if err != nil {
		t.Fatalf("manager read: %v", err)
	}
	mgrJSON, _ := json.Marshal(manager)
	for _, must := range []string{`"money"`, `"proposed_freight_cents":34000`, `"delta_cents":8000`} {
		if !strings.Contains(string(mgrJSON), must) {
			t.Fatalf("positive control failed: the manager's card is missing %s, %s", must, mgrJSON)
		}
	}

	driverCtx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: seededUser(t, h.pool), RoleCode: "DRIVERS", Tier: identity.TierFrontline,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
	driver, err := h.svc.FreightRaiseByID(driverCtx, res.Parked.ID)
	if err != nil {
		t.Fatalf("a driver may know the record exists: %v", err)
	}
	drvJSON, _ := json.Marshal(driver)
	assertFreightRaiseDriverCardJSON(t, drvJSON)
}

// assertFreightTrail proves ONE audit row exists for an action on an order, with the figures the
// allowlist permits. actor is optional ("" = do not assert it).
func assertFreightTrail(t *testing.T, h *frtHarness, orderID, action, actor string, amount, delta int64) {
	t.Helper()
	var raw []byte
	var gotActor *string
	err := h.pool.QueryRow(context.Background(), `
SELECT changes, actor_id::text FROM audit_log
 WHERE entity_id = $1 AND action = $2 ORDER BY created_at DESC LIMIT 1`, orderID, action).Scan(&raw, &gotActor)
	if err != nil {
		t.Fatalf("no %s audit row for %s: %v", action, orderID, err)
	}
	var changes map[string]any
	if err := json.Unmarshal(raw, &changes); err != nil {
		t.Fatalf("audit changes are not JSON: %v", err)
	}
	if got := fmt.Sprint(changes["amount_cents"]); got != fmt.Sprint(amount) {
		t.Fatalf("%s: amount_cents = %v, want %d (changes: %v)", action, changes["amount_cents"], amount, changes)
	}
	if got := fmt.Sprint(changes["delta_cents"]); got != fmt.Sprint(delta) {
		t.Fatalf("%s: delta_cents = %v, want %d", action, changes["delta_cents"], delta)
	}
	if changes["policy"] != string(identity.PolicyFreightAboveQuote) {
		t.Fatalf("%s: the trail does not name the rule: %v", action, changes["policy"])
	}
	if actor != "" && (gotActor == nil || *gotActor != actor) {
		t.Fatalf("%s: actor = %v, want %s", action, gotActor, actor)
	}
}
