//go:build integration

package lien

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// rt1 fixtures: every row a leg plants is deleted by the leg, in dependency order, and a delete that fails
// fails the test naming the table (W3-COMMON, FROM-PRINCIPAL 202). Rows are picked by the leg's own key.

func rt1Del(t *testing.T, pool *pgxpool.Pool, table, where string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM `+table+` WHERE `+where, args...); err != nil {
		t.Errorf("cleanup: delete from %s: %v", table, err)
	}
}

type rt1World struct {
	pool                 *pgxpool.Pool
	svc                  *Service
	custSvc              *customer.Service
	branchA, branchB     string
	customerA, customerB string
	jobOnSiteON          string // customer A's open job, no site province
	jobOnSiteZA          string // customer A's open job whose site is the planted province ZA
	jobOfB               string // customer B's job
	closedJob            string // customer A's closed job
	tag                  string
	userID               string
}

func (w *rt1World) tagN() string { return w.tag }

func rt1Plant(t *testing.T) *rt1World {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	w := &rt1World{pool: pool, tag: strconv.FormatInt(time.Now().UnixNano(), 36)}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("plant %s: %v", what, err)
		}
	}
	// The planted province: first planted, last removed (cleanups run in reverse).
	exec("province ZA", `INSERT INTO holdback_province_rules (province, holdback_pct_bps, preserve_days, perfect_days) VALUES ('ZA', 500, 30, 45)`)
	t.Cleanup(func() { rt1Del(t, pool, "holdback_province_rules", "province = 'ZA'") })
	// The dealer rule is a shared singleton: every leg puts it back exactly as it found it, updated_at
	// included (row 0.46), by the row image read here.
	var ruleProvince string
	var rulePct *int
	var ruleSetBy *string
	var ruleAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT province, holdback_pct_bps, set_by::text, updated_at FROM holdback_dealer_rule`).Scan(&ruleProvince, &rulePct, &ruleSetBy, &ruleAt); err != nil {
		t.Fatalf("plant: read holdback_dealer_rule: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `UPDATE holdback_dealer_rule SET province = $1, holdback_pct_bps = $2, set_by = $3::uuid, updated_at = $4`,
			ruleProvince, rulePct, ruleSetBy, ruleAt); err != nil {
			t.Errorf("cleanup: reset holdback_dealer_rule: %v", err)
		}
	})

	// The staff user the legs act as: set_by is a foreign key to users.
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, status) VALUES ($1, 'RT1 Manager', 'ACTIVE') RETURNING id::text`,
		"rt1-"+w.tag+"@example.test").Scan(&w.userID); err != nil {
		t.Fatalf("plant user: %v", err)
	}
	t.Cleanup(func() { rt1Del(t, pool, "users", "id = $1", w.userID) })

	for _, p := range []*string{&w.branchA, &w.branchB} {
		*p = uuid.NewString()
		exec("branch", `INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name) VALUES ($1, $1, NULL, 'BRANCH', $2, 'RT1 branch')`,
			*p, "RT1-"+w.tag+"-"+(*p)[:4])
		id := *p
		t.Cleanup(func() { rt1Del(t, pool, "locations", "id = $1", id) })
	}
	for i, p := range []*string{&w.customerA, &w.customerB} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents, payment_terms_days)
			 VALUES ($1, 'RT1 Co', 'CONTRACTOR', 500000, 30) RETURNING id::text`, "RT1-"+w.tag+"-"+strconv.Itoa(i)).Scan(p); err != nil {
			t.Fatalf("plant customer: %v", err)
		}
		id := *p
		t.Cleanup(func() { rt1Del(t, pool, "customers", "id = $1", id) })
	}
	job := func(customerID, no, status, shipTo string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, status, ship_to_address)
			 VALUES ($1, $2, $3, 'RT1 job', $4, $5::jsonb) RETURNING id::text`,
			customerID, w.branchA, no+"-"+w.tag, status, shipTo).Scan(&id); err != nil {
			t.Fatalf("plant job %s: %v", no, err)
		}
		t.Cleanup(func() { rt1Del(t, pool, "customer_jobs", "id = $1", id) })
		return id
	}
	w.jobOnSiteON = job(w.customerA, "J-ON", "OPEN", `{}`)
	w.jobOnSiteZA = job(w.customerA, "J-ZA", "OPEN", `{"province":"ZA"}`)
	w.closedJob = job(w.customerA, "J-CL", "CLOSED", `{}`)
	w.jobOfB = job(w.customerB, "J-B", "OPEN", `{}`)
	// Overrides and lien jobs are deleted before the jobs and customers they hang from.
	t.Cleanup(func() {
		rt1Del(t, pool, "holdback_job_overrides", "customer_job_id IN (SELECT id FROM customer_jobs WHERE customer_id IN ($1, $2))", w.customerA, w.customerB)
	})

	custRepo := customer.NewRepository(pool)
	w.custSvc = customer.NewService(custRepo).WithJobs(custRepo)
	w.svc = NewService(NewRepository(pool)).WithCustomerJobs(NewCustomerJobs(w.custSvc))
	w.svc.callerFrom = testCallerFrom
	return w
}

// rt1Lien plants a lien job for customerID and removes it (and anything under it) after the leg.
func (w *rt1World) lienJob(t *testing.T, customerID, branchID string) string {
	t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO lien_jobs (job_no, customer_id, branch_id) VALUES ($1, NULLIF($2,'')::uuid, $3) RETURNING id::text`,
		"RT1-L-"+w.tag+"-"+uuid.NewString()[:4], customerID, branchID).Scan(&id); err != nil {
		t.Fatalf("plant lien job: %v", err)
	}
	t.Cleanup(func() { rt1Del(t, w.pool, "lien_jobs", "id = $1", id) })
	return id
}

func (w *rt1World) as(sc identity.StaffContext) context.Context { return ctxWith(sc) }

func (w *rt1World) manager(branches ...string) identity.StaffContext {
	m := map[string]bool{}
	for _, b := range branches {
		m[b] = true
	}
	return identity.StaffContext{UserID: w.userID, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: m}
}

func (w *rt1World) clerk(branches ...string) identity.StaffContext {
	c := w.manager(branches...)
	c.RoleCode, c.Tier = "INSIDE_SALES", identity.TierFrontline
	return c
}

// rt1Audit records what the service reports.
type rt1Audit struct {
	mu   sync.Mutex
	seen []audit.SensitiveAccess
}

func (a *rt1Audit) Record(_ context.Context, s audit.SensitiveAccess) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen = append(a.seen, s)
	return nil
}

func ip(n int) *int { return &n }
