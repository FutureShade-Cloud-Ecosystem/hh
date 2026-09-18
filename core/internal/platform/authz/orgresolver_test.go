package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// countingTracer counts executed statements through the pgx tracer, the
// form the zero-statement proof takes once the resolver is concrete
// (B2 review-notes item 6): the middleware's counting doubles are
// superseded here by the real pool wiring shape with a tracer attached.
type countingTracer struct {
	queries int32
}

func (t *countingTracer) TraceQueryStart(context.Context, *pgx.Conn, pgx.TraceQueryStartData) context.Context {
	atomic.AddInt32(&t.queries, 1)
	return context.Background()
}

func (t *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *countingTracer) count() int { return int(atomic.LoadInt32(&t.queries)) }

func TestNormalizeOrgSlugLowercases(t *testing.T) {
	if got := NormalizeOrgSlug("B3Yard"); got != "b3yard" {
		t.Fatalf("NormalizeOrgSlug = %q", got)
	}
	if got := NormalizeOrgSlug("b3yard"); got != "b3yard" {
		t.Fatalf("NormalizeOrgSlug = %q", got)
	}
}

// testDSN names the scratch database for the pgx-form proofs. Absent the
// variable the database legs skip; the unit legs above still run.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("HH_B3_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HH_B3_TEST_DATABASE_URL is not set; pgx-form proof legs need the scratch database")
	}
	return dsn
}

// insertTestOrg anchors one synthetic org row and returns its id; the
// caller registers the deletion.
func insertTestOrg(t *testing.T, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO org (slug, name) VALUES ($1, $2) RETURNING id::text`,
		slug, "B3 Proof Org").Scan(&id)
	if err != nil {
		t.Fatalf("insert test org: %v", err)
	}
	return id
}

func deleteTestOrg(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM org WHERE id = $1`, id); err != nil {
		t.Fatalf("delete test org: %v", err)
	}
}

// The slug legs against a real anchor row: a mixed-case slug resolves to
// the lowercase anchor (CITEXT plus the resolver's normalization), an
// unknown slug is the provisioning gap (ok false, no error), and a
// shape-invalid slug is refused before any statement runs.
func TestPgxOrgResolverSlugLegs(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool, err := NewPgxPoolWithTracer(ctx, dsn, nil)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	tracer := &countingTracer{}
	traced, err := NewPgxPoolWithTracer(ctx, dsn, tracer)
	if err != nil {
		t.Fatalf("traced pool: %v", err)
	}
	defer traced.Close()
	resolver, err := NewPgxOrgResolver(traced)
	if err != nil {
		t.Fatalf("NewPgxOrgResolver: %v", err)
	}

	const slug = "b3yardone"
	orgID := insertTestOrg(t, pool, slug)
	defer deleteTestOrg(t, pool, orgID)

	id, ok, err := resolver.ResolveSlug(ctx, "B3YARDONE")
	if err != nil || !ok || id != orgID {
		t.Fatalf("mixed-case resolve: id=%s ok=%v err=%v", id, ok, err)
	}
	if tracer.count() != 1 {
		t.Fatalf("resolve statements = %d, want one", tracer.count())
	}

	id, ok, err = resolver.ResolveSlug(ctx, slug)
	if err != nil || !ok || id != orgID {
		t.Fatalf("lowercase resolve: id=%s ok=%v err=%v", id, ok, err)
	}

	before := tracer.count()
	_, ok, err = resolver.ResolveSlug(ctx, "b3ghost")
	if err != nil || ok {
		t.Fatalf("unknown slug: ok=%v err=%v", ok, err)
	}
	if tracer.count() != before+1 {
		t.Fatalf("unknown slug statements = %d, want one", tracer.count()-before)
	}

	before = tracer.count()
	if _, _, err = resolver.ResolveSlug(ctx, "not a slug"); err == nil {
		t.Fatalf("shape-invalid slug accepted")
	}
	if tracer.count() != before {
		t.Fatalf("shape-invalid slug executed a statement")
	}
}

// The zero-statement property in the pgx form (ADR-0020 addendum, QA
// finding F5): through a Gate whose resolver is the concrete pgx reader,
// every pre-membership refusal leg executes no core-database statement,
// the success leg executes exactly one anchor read, and the one refusal
// that reaches the anchor (the provisioning gap, membership passed with
// the anchor row missing) executes exactly one and answers 403 with the
// shared body.
func TestZeroStatementsPgxForm(t *testing.T) {
	dsn := testDSN(t)
	f := newFixture(t)
	ctx := context.Background()
	pool, err := NewPgxPoolWithTracer(ctx, dsn, nil)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	const slug = "b3zeroyard"
	orgID := insertTestOrg(t, pool, slug)
	defer func() {
		// Idempotent: the gap leg below deletes it first.
		_, _ = pool.Exec(context.Background(), `DELETE FROM org WHERE id = $1`, orgID)
	}()

	tracer := &countingTracer{}
	traced, err := NewPgxPoolWithTracer(ctx, dsn, tracer)
	if err != nil {
		t.Fatalf("traced pool: %v", err)
	}
	defer traced.Close()
	resolver, err := NewPgxOrgResolver(traced)
	if err != nil {
		t.Fatalf("NewPgxOrgResolver: %v", err)
	}
	gate, err := NewGate(f.verifier, f.members, resolver)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	rec := &recorder{}
	h := gate.Staff(rec.handler())
	auth := "Bearer " + f.staffToken(t, fixtureSubject)

	// Membership keyed on the slug so the no-membership leg and the gap
	// leg are distinct: ghostorg is never a member, the anchored slug is.
	anchoredSlug := slug
	f.members.mu.Lock()
	f.members.member = func(uid, orgSlug string) (bool, error) { return orgSlug == anchoredSlug, nil }
	f.members.mu.Unlock()
	legs := []struct {
		name       string
		headers    map[string]string
		wantStatus int
		wantStmts  int
	}{
		{"no token", map[string]string{HeaderXOrg: slug}, http.StatusUnauthorized, 0},
		{"missing header", map[string]string{"Authorization": auth}, http.StatusBadRequest, 0},
		{"malformed slug", map[string]string{"Authorization": auth, HeaderXOrg: "Not-A-Slug"}, http.StatusBadRequest, 0},
		{"no membership", map[string]string{"Authorization": auth, HeaderXOrg: "ghostorg"}, http.StatusForbidden, 0},
		{"admitted", map[string]string{"Authorization": auth, HeaderXOrg: slug}, http.StatusOK, 1},
	}
	for _, leg := range legs {
		before := tracer.count()
		resp := capture(h, leg.headers)
		if resp.status != leg.wantStatus {
			t.Fatalf("%s: status %d, want %d (body %s)", leg.name, resp.status, leg.wantStatus, resp.body)
		}
		if got := tracer.count() - before; got != leg.wantStmts {
			t.Fatalf("%s: statements = %d, want %d", leg.name, got, leg.wantStmts)
		}
	}

	// A repeated header is a shape refusal too; capture cannot build it
	// (Header.Set holds one value), so the request is built directly.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	req.Header.Add("Authorization", auth)
	req.Header.Add(HeaderXOrg, slug)
	req.Header.Add(HeaderXOrg, slug)
	srec := httptest.NewRecorder()
	before := tracer.count()
	h.ServeHTTP(srec, req)
	if srec.Code != http.StatusBadRequest {
		t.Fatalf("repeated header: status %d", srec.Code)
	}
	if tracer.count() != before {
		t.Fatalf("repeated header: statements = %d, want zero", tracer.count()-before)
	}

	// The provisioning gap: membership passed, the anchor row missing.
	deleteTestOrg(t, pool, orgID)
	before = tracer.count()
	resp := capture(h, map[string]string{"Authorization": auth, HeaderXOrg: slug})
	if resp.status != http.StatusForbidden {
		t.Fatalf("gap leg: status %d", resp.status)
	}
	if got := tracer.count() - before; got != 1 {
		t.Fatalf("gap leg statements = %d, want exactly one", got)
	}
	if resp.body != string(BodyForbidden) {
		t.Fatalf("gap leg body = %s", resp.body)
	}
	if reached, _, _ := rec.result(); reached != 1 {
		t.Fatalf("handler reached %d times, want only the admitted leg", reached)
	}
}
