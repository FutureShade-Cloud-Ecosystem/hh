//go:build integration

// Integration tests run the real pgx Repository against the live Postgres 16 that
// `make reset` provisions. Run with: make test-integration. These prove the
// concurrency/atomicity properties the in-memory unit tests cannot.
package portal

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const intSecret = "integration-portal-secret-at-least-32b!"

const (
	custQuinte = "c7000000-0000-4000-9000-000000000001" // Quinte Hardscapes
	markAdmin  = "e7000000-0000-4000-9000-000000000001" // seeded bootstrap account_admin
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...) // CI has a DB configured → a failure must fail, not skip
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// A single invite accepted concurrently must provision EXACTLY ONE user and emit
// EXACTLY ONE outbox event, the double-accept race must be closed in SQL.
func TestIntegration_AcceptInvite_ConcurrentSingleUse(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool), intSecret)

	email := fmt.Sprintf("race+%d@quinte.example", time.Now().UnixNano())
	admin := PortalContext{UserID: markAdmin, CustomerID: custQuinte, Role: "account_admin"}
	inv, err := svc.Invite(ctx, admin, email, "buyer")
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE email = $1`, email)
		_, _ = pool.Exec(ctx, `DELETE FROM portal_invites WHERE email = $1`, email)
		_, _ = pool.Exec(ctx, `DELETE FROM events_outbox WHERE payload->>'email' = $1`, email)
	})

	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.AcceptInvite(ctx, inv.Token, "Race User", "password123"); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("exactly one concurrent accept must succeed, got %d", successes)
	}
	var users, events int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM portal_users WHERE email = $1`, email).Scan(&users)
	// Scope to the activation topic: since the notify dispatcher landed, the invite
	// itself also emits a portal-invite notice carrying this email, that event is
	// legitimate and not part of the double-accept race this test guards.
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE topic = $1 AND payload->>'email' = $2`,
		TopicNewPortalContact, email).Scan(&events)
	if users != 1 {
		t.Errorf("want 1 provisioned user, got %d", users)
	}
	if events != 1 {
		t.Errorf("want exactly 1 crm.NewPortalContact outbox event, got %d", events)
	}
}

// Two admins demoted concurrently must never both succeed, at least one active
// admin must always remain (M-1, atomic guard under a per-customer lock).
func TestIntegration_LastAdmin_ConcurrentDemotion(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool), intSecret)

	// Fresh isolated tenant with exactly two active admins.
	var cust string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, 'Race Tenant') RETURNING id::text`,
		fmt.Sprintf("RACE-%d", time.Now().UnixNano())).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust)
	})
	var a, b string
	for _, p := range []*string{&a, &b} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO portal_users (customer_id, email, password_hash, role, status)
			VALUES ($1, $2, 'x', 'account_admin', 'active') RETURNING id::text`,
			cust, fmt.Sprintf("admin+%d@race.example", time.Now().UnixNano())).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	caller := PortalContext{CustomerID: cust, Role: "account_admin"}

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for _, id := range []string{a, b} {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			if err := svc.UpdateRole(ctx, caller, target, "buyer"); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()

	var remaining int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM portal_users WHERE customer_id = $1 AND role='account_admin' AND status='active'`, cust).Scan(&remaining)
	if remaining < 1 {
		t.Fatalf("the tenant was raced to %d active admins, last-admin invariant violated", remaining)
	}
	if ok == 2 {
		t.Fatal("both demotions succeeded, the guard did not serialize")
	}
}

// A user deactivated in the DB after issue must fail Authenticate (H-4).
func TestIntegration_Authenticate_RejectsDeactivated(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewService(NewRepository(pool), intSecret)

	var cust, uid string
	_ = pool.QueryRow(ctx, `INSERT INTO customers (account_no, name) VALUES ($1,'H4 Tenant') RETURNING id::text`,
		fmt.Sprintf("H4-%d", time.Now().UnixNano())).Scan(&cust)
	hash, _ := hashPassword("password123")
	email := fmt.Sprintf("h4+%d@race.example", time.Now().UnixNano())
	_ = pool.QueryRow(ctx, `
		INSERT INTO portal_users (customer_id, email, password_hash, role, status)
		VALUES ($1,$2,$3,'account_admin','active') RETURNING id::text`, cust, email, hash).Scan(&uid)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE customer_id=$1`, cust)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id=$1`, cust)
	})

	resp, err := svc.Login(ctx, email, "password123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := svc.Authenticate(ctx, resp.Token); err != nil {
		t.Fatalf("active user should authenticate: %v", err)
	}
	// Deactivate, then the SAME token must be rejected on the next request.
	if _, err := pool.Exec(ctx, `UPDATE portal_users SET status='inactive' WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, resp.Token); err == nil {
		t.Fatal("a deactivated user's token must no longer authenticate")
	}
}
