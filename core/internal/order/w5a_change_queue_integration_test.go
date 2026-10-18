//go:build integration

// w5a_change_queue_integration_test.go, the cross order change read (change_queue.go), over the real
// repository and the real handler method. The shared database holds other tests' changes, so every
// assertion looks only at the change ids this test raised.
//
// Money class: the read computes no money. delta_cents and finance_threshold_cents are asserted to
// be the numbers stored at request time, passed through unchanged.
package order

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

const secondBranch = "5d1b3f6e-0000-4000-9000-000000000002" // seeded second branch (0003)

type queueItemJSON struct {
	ID              string   `json:"id"`
	OrderNo         string   `json:"order_no"`
	BranchID        string   `json:"branch_id"`
	Status          string   `json:"status"`
	Reason          string   `json:"reason"`
	DeltaCents      int64    `json:"delta_cents"`
	RequestedByRole string   `json:"requested_by_role"`
	NextActions     []string `json:"next_actions"`
}

type queuePageJSON struct {
	Items      []queueItemJSON `json:"items"`
	NextCursor *string         `json:"next_cursor"`
	Limit      int             `json:"limit"`
}

type fakeRoleLabels map[string]string

func (f fakeRoleLabels) RoleLabelsForUsers(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if l, ok := f[id]; ok {
			out[id] = l
		}
	}
	return out, nil
}

// queueGet drives the handler method with a GET carrying the query and, when sc is not nil, a
// stamped staff context.
func queueGet(t *testing.T, svc *Service, sc *staffctx.StaffContext, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/order-changes?"+query, nil)
	if sc != nil {
		r = r.WithContext(staffctx.ContextWithStaff(r.Context(), *sc))
	}
	rr := httptest.NewRecorder()
	NewHandler(svc).listChangeQueue(rr, r, guards.StaffCaller{})
	return rr
}

func queuePage(t *testing.T, rr *httptest.ResponseRecorder) queuePageJSON {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var p queuePageJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode page: %v; body=%s", err, rr.Body.String())
	}
	return p
}

// queueAll pages through every row of a status for the caller, fetching two at a time, which also
// exercises the cursor across the shared database's other rows.
func queueAll(t *testing.T, svc *Service, sc staffctx.StaffContext, status string) []queueItemJSON {
	t.Helper()
	var all []queueItemJSON
	cursor := ""
	for i := 0; i < 2000; i++ {
		q := "limit=2&status=" + status
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		p := queuePage(t, queueGet(t, svc, &sc, q))
		all = append(all, p.Items...)
		if p.NextCursor == nil {
			return all
		}
		cursor = *p.NextCursor
	}
	t.Fatal("the queue never ended")
	return nil
}

func queueIndex(items []queueItemJSON, id string) int {
	for i, it := range items {
		if it.ID == id {
			return i
		}
	}
	return -1
}

// queueRaise seeds an order and raises a change on it: +2 units at unit cents, so the delta is
// exactly 2 * unitCents and the finance threshold (50000) is a property of the arguments.
func queueRaise(t *testing.T, pool *pgxpool.Pool, svc *Service, unitCents int64) ChangeOrder {
	t.Helper()
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, unitCents)
	ctx := staffctx.ContextWithStaff(context.Background(), w5aStaff(true))
	co, err := w5aRaise(t, svc, ctx, ord, "12")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	return co
}

func queueSvc(t *testing.T, pool *pgxpool.Pool, unitCents int64, approverErr error) *Service {
	t.Helper()
	svc := w5aChangeServiceOn(t, pool, unitCents, true)
	svc.WithFinanceApprover(w5aApprover{err: approverErr})
	return svc
}

func TestIntegration_ChangeQueue_BranchFence(t *testing.T) {
	pool := intPool(t)
	svc := queueSvc(t, pool, 100, nil)
	mine := queueRaise(t, pool, svc, 100)
	theirs := queueRaise(t, pool, svc, 100)
	if _, err := pool.Exec(context.Background(),
		`UPDATE change_orders SET branch_id = $2 WHERE id = $1`, theirs.ID, secondBranch); err != nil {
		t.Fatalf("move change to the second branch: %v", err)
	}

	trenton := w5aStaff(false)
	got := queueAll(t, svc, trenton, "PENDING_YARD")
	if queueIndex(got, mine.ID) < 0 {
		t.Error("a caller must see its own branch's pending change")
	}
	if queueIndex(got, theirs.ID) >= 0 {
		t.Fatal("a second branch's change appeared in this branch's queue")
	}
	for _, it := range got {
		if it.BranchID != trentonBranch {
			t.Fatalf("row %s is branch %s, a Trenton caller must see Trenton rows only", it.ID, it.BranchID)
		}
	}

	other := staffctx.StaffContext{UserID: w5aUser, Status: staffctx.StatusActive, Tier: staffctx.Tier(4),
		Scope: staffctx.ScopeBranch, Branches: map[string]bool{secondBranch: true}}
	got = queueAll(t, svc, other, "PENDING_YARD")
	if queueIndex(got, theirs.ID) < 0 || queueIndex(got, mine.ID) >= 0 {
		t.Error("the second branch's caller must see exactly the second branch's change")
	}

	global := staffctx.StaffContext{UserID: w5aUser, Status: staffctx.StatusActive, Tier: staffctx.TierManager,
		Scope: staffctx.ScopeGlobal}
	got = queueAll(t, svc, global, "PENDING_YARD")
	if queueIndex(got, mine.ID) < 0 || queueIndex(got, theirs.ID) < 0 {
		t.Error("a global caller reaches every branch")
	}
}

func TestIntegration_ChangeQueue_StatusFilterAndStoredNumbers(t *testing.T) {
	pool := intPool(t)
	svc := queueSvc(t, pool, 6000, nil) // +2 units at $60.00 is +$120.00 under the threshold
	yard := queueRaise(t, pool, svc, 6000)
	// 10 -> 20 units at $60.00 is +$600.00, over the threshold: yard ack lands it in PENDING_FINANCE.
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	ctx := staffctx.ContextWithStaff(context.Background(), w5aStaff(true))
	fin, err := w5aRaise(t, svc, ctx, ord, "20")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if fin, err = svc.YardAck(ctx, fin.ID, "ok"); err != nil || fin.Status != ChangeStatusPendingFinance {
		t.Fatalf("yard ack = %q, %v; want PENDING_FINANCE", fin.Status, err)
	}

	sc := w5aStaff(true)
	y := queueAll(t, svc, sc, "PENDING_YARD")
	f := queueAll(t, svc, sc, "PENDING_FINANCE")
	if queueIndex(y, yard.ID) < 0 || queueIndex(y, fin.ID) >= 0 {
		t.Error("PENDING_YARD must hold the un-acked change and not the acked one")
	}
	if i := queueIndex(f, fin.ID); i < 0 || queueIndex(f, yard.ID) >= 0 {
		t.Error("PENDING_FINANCE must hold the acked big change and not the un-acked one")
	} else {
		row := f[i]
		if row.DeltaCents != fin.DeltaCents || row.DeltaCents != 60000 {
			t.Errorf("delta_cents = %d, want the stored %d (60000)", row.DeltaCents, fin.DeltaCents)
		}
		if row.Status != ChangeStatusPendingFinance || row.Reason != fin.Reason || row.OrderNo == "" {
			t.Errorf("row = %+v, want stage, note and order reference carried", row)
		}
	}
	// No filter reads every status; a lower case status is accepted.
	if all := queueAll(t, svc, sc, ""); queueIndex(all, yard.ID) < 0 || queueIndex(all, fin.ID) < 0 {
		t.Error("no status filter must return both")
	}
	if lc := queueAll(t, svc, sc, "pending_yard"); queueIndex(lc, yard.ID) < 0 {
		t.Error("a lower case status must read as its upper case form")
	}
}

func TestIntegration_ChangeQueue_UnknownStatusAndBadCursorAre400(t *testing.T) {
	pool := intPool(t)
	svc := queueSvc(t, pool, 100, nil)
	sc := w5aStaff(false)
	// "v1:chq:junk|x" is a cursor the httpx envelope accepts (the v1: prefix) and parseChangeQueueCursor
	// refuses, so the last case reaches the queue's own parser rather than failing in httpx.decodeCursor.
	junk := base64.RawURLEncoding.EncodeToString([]byte("v1:chq:junk|x"))
	for _, q := range []string{"status=NOPE", "status=PENDING_YARD&cursor=!!notbase64", "cursor=" + "dGhpcyBpcyBub3QgYSBjdXJzb3I", "cursor=" + junk} {
		rr := queueGet(t, svc, &sc, q)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("query %q = %d, want 400; body=%s", q, rr.Code, rr.Body.String())
		}
	}
}

func TestIntegration_ChangeQueue_OldestFirstAndPaged(t *testing.T) {
	pool := intPool(t)
	svc := queueSvc(t, pool, 100, nil)
	a := queueRaise(t, pool, svc, 100)
	b := queueRaise(t, pool, svc, 100)
	c := queueRaise(t, pool, svc, 100)

	got := queueAll(t, svc, w5aStaff(false), "PENDING_YARD") // two rows a page
	ia, ib, ic := queueIndex(got, a.ID), queueIndex(got, b.ID), queueIndex(got, c.ID)
	if ia < 0 || ib < 0 || ic < 0 || !(ia < ib && ib < ic) {
		t.Fatalf("positions a=%d b=%d c=%d, want all present and oldest first", ia, ib, ic)
	}
	seen := map[string]bool{}
	for _, it := range got {
		if seen[it.ID] {
			t.Fatalf("row %s returned twice across pages", it.ID)
		}
		seen[it.ID] = true
	}
	first := queuePage(t, queueGet(t, svc, ptr(w5aStaff(false)), "limit=1&status=PENDING_YARD"))
	if len(first.Items) != 1 || first.Limit != 1 {
		t.Errorf("limit=1 page = %d items, limit %d", len(first.Items), first.Limit)
	}
}

func ptr[T any](v T) *T { return &v }

func TestIntegration_ChangeQueue_NextActionsPerCaller(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	approved := queueSvc(t, pool, 6000, nil)
	ctx := staffctx.ContextWithStaff(context.Background(), w5aStaff(true))
	yard, err := w5aRaise(t, approved, ctx, ord, "20")
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	has := func(items []queueItemJSON, id, action string) (found bool, row queueItemJSON) {
		i := queueIndex(items, id)
		if i < 0 {
			t.Fatalf("change %s missing from the page", id)
		}
		for _, a := range items[i].NextActions {
			if a == action {
				return true, items[i]
			}
		}
		return false, items[i]
	}

	// Yard role (frontline, not a manager): acks on PENDING_YARD.
	items := queueAll(t, approved, w5aStaff(false), "PENDING_YARD")
	if ok, row := has(items, yard.ID, "yard_ack"); !ok {
		t.Errorf("a yard caller must be offered yard_ack on PENDING_YARD, got %v", row.NextActions)
	}
	// The row equals what GetChange says for the same caller.
	sc := w5aStaff(false)
	got, err := approved.GetChange(staffctx.ContextWithStaff(context.Background(), sc), yard.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, row := has(items, yard.ID, "yard_ack"); !slices.Equal(row.NextActions, got.NextActions) {
		t.Errorf("list next_actions %v differ from GetChange %v", row.NextActions, got.NextActions)
	}

	if _, err := approved.YardAck(ctx, yard.ID, ""); err != nil {
		t.Fatalf("yard ack: %v", err)
	}
	// A manager other than the requester, and a named approver: finance_approve on PENDING_FINANCE. (The
	// requester is not offered the signature on their own change.)
	signer := w5aStaff(true)
	signer.UserID = sodOtherManager
	items = queueAll(t, approved, signer, "PENDING_FINANCE")
	if ok, row := has(items, yard.ID, "finance_approve"); !ok {
		t.Errorf("a named-approver manager must be offered finance_approve, got %v", row.NextActions)
	}
	// Frontline caller: sees the row, no finance_approve.
	items = queueAll(t, approved, w5aStaff(false), "PENDING_FINANCE")
	if ok, _ := has(items, yard.ID, "finance_approve"); ok {
		t.Error("a non-manager must never be offered finance_approve")
	}
	// Manager who is NOT a named approver: row still listed, finance_approve narrowed away.
	notNamed := queueSvc(t, pool, 6000, ErrForbidden)
	items = queueAll(t, notNamed, signer, "PENDING_FINANCE")
	ok, row := has(items, yard.ID, "finance_approve")
	if ok {
		t.Error("a manager who is not a named approver must not be offered finance_approve")
	}
	if len(row.NextActions) == 0 {
		t.Error("the row stays visible with its other actions (reject, cancel)")
	}
}

func TestIntegration_ChangeQueue_RequesterRoleLabel(t *testing.T) {
	pool := intPool(t)
	svc := queueSvc(t, pool, 100, nil)
	co := queueRaise(t, pool, svc, 100)
	blank := queueAll(t, svc, w5aStaff(false), "PENDING_YARD")
	if i := queueIndex(blank, co.ID); i < 0 || blank[i].RequestedByRole != "" {
		t.Fatal("an unbound reader leaves the label blank and the row present")
	}
	svc.WithRequesterRoles(fakeRoleLabels{w5aUser: "Counter Sales"})
	labelled := queueAll(t, svc, w5aStaff(false), "PENDING_YARD")
	if i := queueIndex(labelled, co.ID); i < 0 || labelled[i].RequestedByRole != "Counter Sales" {
		t.Errorf("requested_by_role not carried from the reader: %+v", labelled)
	}
	// The real identity reader answers without error for a real and an unknown user, and with a seeded
	// user joined to a live grant it answers that grant's role label (the seeded gate database holds no
	// such join, so this leg plants one and restores it).
	staff, err := identity.NewStaffAuthorityResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staff.RoleLabelsForUsers(context.Background(),
		[]string{w5aUser, "00000000-0000-4000-9000-0000000000aa"}); err != nil {
		t.Fatalf("real RoleLabelsForUsers: %v", err)
	}
	ctx := context.Background()
	platformID := "hh681-label-" + co.ID
	var wantLabel string
	if err := pool.QueryRow(ctx, `SELECT label FROM staff_role WHERE id = 'inside_sales'`).Scan(&wantLabel); err != nil {
		t.Fatalf("read the role label: %v", err)
	}
	var orgID, grantID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ($1, 'HH-681 label org') RETURNING id::text`, platformID).Scan(&orgID); err != nil {
		t.Fatalf("plant an org: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		WITH u AS (UPDATE users SET platform_user_id = $1 WHERE id = $2::uuid RETURNING id)
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id)
		SELECT $1, 'inside_sales', $3::uuid FROM u
		RETURNING id::text`, platformID, w5aUser, orgID).Scan(&grantID); err != nil {
		t.Fatalf("plant a live grant for the requester: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM staff_role_grant WHERE id = $1::uuid`, grantID)
		_, _ = pool.Exec(context.Background(), `UPDATE users SET platform_user_id = NULL WHERE id = $1::uuid`, w5aUser)
		_, _ = pool.Exec(context.Background(), `DELETE FROM org WHERE id = $1::uuid`, orgID)
	})
	svc.WithRequesterRoles(staff)
	real := queueAll(t, svc, w5aStaff(false), "PENDING_YARD")
	if i := queueIndex(real, co.ID); i < 0 || real[i].RequestedByRole != wantLabel {
		t.Errorf("the real reader labelled the requester %q, want the live grant's role label %q",
			func() string {
				if i < 0 {
					return "(row missing)"
				}
				return real[i].RequestedByRole
			}(), wantLabel)
	}
}

func TestIntegration_ChangeQueue_UnauthenticatedAndInactiveRefused(t *testing.T) {
	pool := intPool(t)
	svc := queueSvc(t, pool, 100, nil)
	if rr := queueGet(t, svc, nil, "status=PENDING_YARD"); rr.Code != http.StatusUnauthorized {
		t.Errorf("no caller = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
	sc := w5aStaff(true)
	sc.Status = "SUSPENDED"
	if rr := queueGet(t, svc, &sc, "status=PENDING_YARD"); rr.Code != http.StatusForbidden {
		t.Errorf("inactive caller = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}
