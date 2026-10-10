//go:build integration

// yard_notice_rep_dm_integration_test.go, the rep DM's figures decided on the rep the DM goes to
// (PR 302 round 3, P2-1). The yard notice carries the skid deposit figure to a rep only when that
// rep's own role is on the cost and margin list. The rep is the one messaging's routing picks (the
// order override, then the order's salesperson, then the customer's rep), so these legs drive the
// real messaging service over a migrated and seeded database, never a double that routes for it.
//
//	Run: DATABASE_URL='postgres://.../integration_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run YardNoticeRepDM -count=1
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

const (
	repDMBranchTrenton = "5d1b3f6e-0000-4000-9000-000000000001"

	repDMOnListUser  = "d7000000-0000-4000-9000-000000000381" // synthetic: OUTSIDE_SALES, on the default list
	repDMOnListUser2 = "d7000000-0000-4000-9000-000000000382" // synthetic: SALES_MANAGER, on the default list
	repDMDriverUser  = "d7000000-0000-4000-9000-000000000383" // synthetic: DRIVERS, off the default list
)

type repDMFixture struct {
	pool       *pgxpool.Pool
	svc        *messaging.Service
	repo       *messaging.Repository
	customerID string
	orderID    string
	notifier   yardChatNotifier
}

// newRepDMFixture provisions three synthetic staff (one per role the legs need), a customer and an
// order, and removes them all at the end.
func newRepDMFixture(t *testing.T) *repDMFixture {
	t.Helper()
	pool := repDMPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,display_name,status,password_hash) VALUES
	    ($1,'rdm381.outside@dibbits.test','RDM Outside (test)','ACTIVE','x'),
	    ($2,'rdm382.salesmgr@dibbits.test','RDM Sales Manager (test)','ACTIVE','x'),
	    ($3,'rdm383.driver@dibbits.test','RDM Driver (test)','ACTIVE','x')
	  ON CONFLICT (id) DO NOTHING`, repDMOnListUser, repDMOnListUser2, repDMDriverUser); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id,role_id) VALUES
	    ($1,(SELECT id FROM roles WHERE code='OUTSIDE_SALES')),
	    ($2,(SELECT id FROM roles WHERE code='SALES_MANAGER')),
	    ($3,(SELECT id FROM roles WHERE code='DRIVERS'))
	  ON CONFLICT DO NOTHING`, repDMOnListUser, repDMOnListUser2, repDMDriverUser); err != nil {
		t.Fatalf("seed user_roles: %v", err)
	}
	f := &repDMFixture{pool: pool, repo: messaging.NewRepository(pool)}
	f.svc = messaging.NewService(f.repo)
	stamp := time.Now().UnixNano()
	if err := pool.QueryRow(ctx, `INSERT INTO customers (account_no, name) VALUES ($1, 'Rep DM fence customer') RETURNING id::text`,
		fmt.Sprintf("RDM-%d", stamp)).Scan(&f.customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orders (order_no, customer_id, branch_id) VALUES ($1,$2,$3) RETURNING id::text`,
		fmt.Sprintf("RDM-%d", stamp), f.customerID, repDMBranchTrenton).Scan(&f.orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM msg_messages WHERE event_key LIKE 'rdm-%'`)
		_, _ = pool.Exec(c, `DELETE FROM rep_assignments WHERE customer_id = $1`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, f.orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, f.customerID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id = ANY($1)`, []string{repDMOnListUser, repDMOnListUser2, repDMDriverUser})
	})
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	f.notifier = yardChatNotifier{svc: f.svc, figuresOK: cfg.SeesCostAndMargin}
	// The legs assume the default list: the two on list roles see cost, the driver does not.
	for role, want := range map[string]bool{"outside_sales": true, "sales_manager": true, "driver": false} {
		if got, err := cfg.SeesCostAndMargin(role); err != nil || got != want {
			t.Fatalf("the default list answers %v for %s, the legs assume %v (%v)", got, role, want, err)
		}
	}
	return f
}

func repDMPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, ok := os.LookupEnv("DATABASE_URL")
	if !ok || url == "" {
		t.Skip("DATABASE_URL is unset, the rep DM legs need a migrated and seeded Postgres")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func (f *repDMFixture) assign(t *testing.T, orderID, rep string) {
	t.Helper()
	if _, err := f.repo.UpsertRepAssignment(context.Background(), messaging.RepAssignment{
		CustomerID: f.customerID, OrderID: orderID, RepUserID: rep, Source: messaging.AssignManual,
	}); err != nil {
		t.Fatalf("assign rep: %v", err)
	}
}

func (f *repDMFixture) clearAssignments(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM rep_assignments WHERE customer_id = $1`, f.customerID); err != nil {
		t.Fatalf("clear assignments: %v", err)
	}
}

// post sends one skid notice through the real notifier and returns the body the named DM holds.
func (f *repDMFixture) post(t *testing.T, key, salesperson, dmUser string) string {
	t.Helper()
	ref := fenceRef()
	ref.EventKey = key
	ref.BranchID = repDMBranchTrenton
	ref.CustomerID = f.customerID
	ref.OrderID = f.orderID
	ref.RepUserID = salesperson
	ref.Audience = tasks.AudienceOrderRep
	if err := f.notifier.PostTaskEvent(context.Background(), ref); err != nil {
		t.Fatalf("post: %v", err)
	}
	var body string
	err := f.pool.QueryRow(context.Background(), `
		SELECT m.body FROM msg_messages m
		  JOIN msg_members mm ON mm.channel_id = m.channel_id AND mm.user_id = $2
		 WHERE m.event_key = $1`, key, dmUser).Scan(&body)
	if err != nil {
		t.Fatalf("the notice should sit in %s's DM (event %s): %v", dmUser, key, err)
	}
	return body
}

func repDMKey(t *testing.T) string {
	return "rdm-" + fmt.Sprint(time.Now().UnixNano()) + "-" + strings.ReplaceAll(t.Name(), "/", "-")
}

func repDMWantFigures(t *testing.T, who, body string) {
	t.Helper()
	if !strings.Contains(body, "$100.00") || !strings.Contains(body, "$150.00") {
		t.Errorf("%s is on the cost list and keeps the figures: %q", who, body)
	}
}

func repDMWantFigureless(t *testing.T, who, body string) {
	t.Helper()
	for _, leak := range []string{"$", "100.00", "150.00"} {
		if strings.Contains(body, leak) {
			t.Errorf("%s is off the cost list and must read no figure (%q in %q)", who, leak, body)
		}
	}
	if !strings.Contains(body, "The skid deposit changed.") {
		t.Errorf("the figureless body keeps the words: %q", body)
	}
}

// (a) An off list override rep (a driver) with an on list salesperson: the DM goes to the driver,
// so the driver's copy is the figureless one. Round 2 decided on the salesperson and sent the figure.
func TestYardNoticeRepDM_OffListOverrideRepWithOnListSalespersonGetsNoFigure(t *testing.T) {
	f := newRepDMFixture(t)
	f.assign(t, f.orderID, repDMDriverUser)
	body := f.post(t, repDMKey(t), repDMOnListUser, repDMDriverUser)
	repDMWantFigureless(t, "the override rep (a driver)", body)
}

// (b) An on list override rep with an off list salesperson: the DM goes to the override rep, who
// keeps the figures. Round 2 decided on the driver salesperson and withheld them.
func TestYardNoticeRepDM_OnListOverrideRepWithOffListSalespersonGetsTheFigures(t *testing.T) {
	f := newRepDMFixture(t)
	f.assign(t, f.orderID, repDMOnListUser)
	body := f.post(t, repDMKey(t), repDMDriverUser, repDMOnListUser)
	repDMWantFigures(t, "the override rep", body)
}

// (c) No salesperson on the order, the customer level rep is on the list: the DM goes to that rep
// and keeps the figures. Round 2 asked about nobody and went figureless (lost detail).
func TestYardNoticeRepDM_NoSalespersonAnOnListCustomerRepGetsTheFigures(t *testing.T) {
	f := newRepDMFixture(t)
	f.assign(t, "", repDMOnListUser2)
	body := f.post(t, repDMKey(t), "", repDMOnListUser2)
	repDMWantFigures(t, "the customer level rep", body)
}

// The remaining routes keep their behaviour: an on list salesperson with no override keeps the
// figures, an off list salesperson or customer level rep goes figureless.
func TestYardNoticeRepDM_TheOtherRoutesFollowTheRecipientsRole(t *testing.T) {
	f := newRepDMFixture(t)
	repDMWantFigures(t, "the salesperson", f.post(t, repDMKey(t)+"-s1", repDMOnListUser, repDMOnListUser))
	repDMWantFigureless(t, "a driver salesperson", f.post(t, repDMKey(t)+"-s2", repDMDriverUser, repDMDriverUser))
	f.assign(t, "", repDMDriverUser)
	repDMWantFigureless(t, "a driver customer level rep", f.post(t, repDMKey(t)+"-s3", "", repDMDriverUser))
}
