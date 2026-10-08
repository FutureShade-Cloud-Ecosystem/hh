//go:build integration

package pos

// returns_trenton_integration_test.go, the RT-B exit test at Trenton, over
// the real composition on a throwaway local Postgres (the repository's
// integration database: migrations plus the dibbits seed, Trenton and
// Kingston seeded by 0003): the real handler behind the real guards, the
// real staff authority resolver over the identity tables, the real pricing
// and till rails, the real audit trail, and the kit's DB-free doubles for
// the two platform facts a test cannot have (the membership directory and
// the verifier's key set). The world: the demo org anchor, two people at
// Trenton (the Sales clerk and the Sales manager), a Kingston clerk, a
// Kingston-only manager, and one product priced at an exact 10.00 per SF
// so every return's money lands on whole cents.
//
// The story it proves: a cash return of 200.00 on a Trenton sale commits;
// one of 300.00 is refused with the configured 250.00 and both approver
// roles named, with an audit row naming the clerk, the sale and the
// amount; the same 300.00 return sent again with the Sales manager's
// credential in X-Approver-Authorization commits as the manager's
// decision, its audit row naming the manager as approver and Sales as
// clerk and citing the refusal; the clerk's own credential, an expired
// one, one issued outside the window, a Kingston manager's and another
// org's all stay refused; as Sales manager the 300.00 return commits with
// no second credential; a return or a return lookup on a Kingston sale is
// refused to the Trenton login for every refund method and the counting
// card rail sees no call; lowering the configured amount to 100.00 through
// a validated modified document changes the answer with no code change;
// the margin glance under the seeded MANAGER_UP policy answers the clerk
// the byte-absent {} and carries the ticket's lines for the manager; the
// admin policy write flipping the org switch to ALL_STAFF carries the
// clerk's glance with no code change, the row restored afterwards; and the
// glance refuses a branch the caller cannot reach.
//
// Run (the throwaway container this lane used, removed afterwards):
//
//	docker run --name hh-margingate-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hardscapeos \
//	  -p 5599:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5599/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5599/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@localhost:5599/hardscapeos?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton -p 1 -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The demo world's two branches, seeded by migration 0003 in every
// migrated database, and the people's fixed identifiers. The subjects are
// the platform user ids the staff authority resolver reads grants by; the
// manager's is numeric-shaped on purpose (see below).
const (
	trentonBranchID  = "5d1b3f6e-0000-4000-9000-000000000001"
	kingstonBranchID = "5d1b3f6e-0000-4000-9000-000000000002"
	demoOrgSlug      = "hh-demo"

	trentonClerkSub = "till-l2c-clerk"
	// The manager's platform subject carries a seven-digit run on purpose:
	// it is the shape the audit chokepoint redacts, so the decision row
	// naming the approver proves it names the STABLE staff users id instead
	// (the lead's P2-4 ruling).
	trentonMgrSub    = "till-l2c-mgr-7731409"
	kingstonClerkSub = "till-l2c-kclerk"
	kingstonMgrSub   = "till-l2c-km"
	elsewhereSub     = "till-l2c-elsewhere"
	// The org's general manager, the Tier-1 global login the admin-gated
	// policy write leg drives (RequireAdmin).
	trentonAdminSub = "till-l2c-admin"

	clerkUserID   = "5d1b0000-0000-4000-9000-0000000000c1"
	managerUserID = "5d1b0000-0000-4000-9000-0000000000c2"
	kClerkUserID  = "5d1b0000-0000-4000-9000-0000000000c3"
	adminUserID   = "5d1b0000-0000-4000-9000-0000000000c5"

	l2cProductID      = "5d1b0000-0000-4000-9000-0000000000d1"
	l2cProductSKU     = "DBS-TILL-L2C-PAVER"
	l2cUnitPrice      = int64(1000) // 10.00 per SF, the exact-cents anchor
	l2cTrentonDrawer  = "l2c-trenton-drawer"
	l2cKingstonDrawer = "l2c-kingston-drawer"

	// The sales' idempotency keys are built per run (the DIB-384 pattern:
	// one go test process must not collide with its predecessors on a
	// shared database, and a fresh run rings fresh sales so the returnable
	// quantities start unspent).
)

// saleKeys names the four sales of one run, the salted keys per process.
type saleKeys struct{ trenton, kingston, small, manager, tiny string }

func newSaleKeys() saleKeys {
	salt := fmt.Sprintf("%08x", os.Getpid())[:8]
	seq := 0
	next := func() string {
		seq++
		return fmt.Sprintf("5d1be000-0000-4000-9000-%s%04x", salt, seq)
	}
	return saleKeys{trenton: next(), kingston: next(), small: next(), manager: next(), tiny: next()}
}

// countingCards is the card rail the refusals must never reach. The tx
// parameter is the CardRefunder seam's discipline: the rail rides the
// caller's Return transaction, and this fake does nothing with it but count.
type countingCards struct{ calls int }

func (c *countingCards) RefundCard(context.Context, pgx.Tx, string, int64, string, string) (string, error) {
	c.calls++
	return "rp_should_never_happen", nil
}

// portalLegStub is the PortalRoleSource the staff surface never consults.
type portalLegStub struct{}

func (portalLegStub) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

var (
	worldOnce sync.Once
	worldErr  error
)

// buildWorld provisions the data side once: the org anchor, the people and
// their grants, the product with its exact price anchor and Trenton cost,
// and the two drawers. Every row lands idempotently, so a shared
// integration database is never disturbed beyond this world's own rows.
func buildWorld(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	worldOnce.Do(func() {
		ctx := context.Background()
		orgID, _, err := identity.ProvisionOrgAnchor(ctx, pool, demoOrgSlug, "The first dealer")
		if err != nil {
			worldErr = fmt.Errorf("provision org: %w", err)
			return
		}
		if _, err := identity.StampOrgScope(ctx, pool, orgID); err != nil {
			worldErr = fmt.Errorf("stamp org scope: %w", err)
			return
		}
		people := []struct {
			id, email, name, sub, role, branch string
		}{
			{clerkUserID, "till-l2c-clerk@dibbits.example", "Sales (till L2C)", trentonClerkSub, "inside_sales", trentonBranchID},
			{managerUserID, "till-l2c-manager@dibbits.example", "Sales manager (till L2C)", trentonMgrSub, "sales_manager", trentonBranchID},
			{kClerkUserID, "till-l2c-kclerk@dibbits.example", "Sales (till L2C Kingston)", kingstonClerkSub, "inside_sales", kingstonBranchID},
			{"5d1b0000-0000-4000-9000-0000000000c4", "till-l2c-km@dibbits.example", "Sales manager (till L2C Kingston)", kingstonMgrSub, "sales_manager", kingstonBranchID},
		}
		for _, p := range people {
			if _, err := pool.Exec(ctx, `
				INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
				VALUES ($1::uuid, $2, $3, 'ACTIVE', $4, $5::uuid)
				ON CONFLICT (id) DO NOTHING`,
				p.id, p.email, p.name, p.sub, orgID); err != nil {
				worldErr = fmt.Errorf("user %s: %w", p.sub, err)
				return
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO user_branch_grants (user_id, branch_id, org_id)
				SELECT $1::uuid, $2::uuid, $3::uuid
				WHERE NOT EXISTS (SELECT 1 FROM user_branch_grants WHERE user_id = $1::uuid AND branch_id = $2::uuid)`,
				p.id, p.branch, orgID); err != nil {
				worldErr = fmt.Errorf("branch grant %s: %w", p.sub, err)
				return
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
				SELECT $1, $2, $3::uuid, $4::uuid
				WHERE NOT EXISTS (
					SELECT 1 FROM staff_role_grant
					WHERE platform_user_id = $1 AND role_id = $2 AND org_id = $3::uuid
					  AND branch_id = $4::uuid AND deleted_at IS NULL)`,
				p.sub, p.role, orgID, p.branch); err != nil {
				worldErr = fmt.Errorf("role grant %s: %w", p.sub, err)
				return
			}
		}
		// The general manager, the Tier-1 global login the admin-gated
		// policy write leg drives. The grant is org-wide: a global role
		// granted with a branch_id is the resolver's ErrGrantShape data
		// defect, so this row carries the NULL branch the rule demands (a
		// global scope reaches every branch without branch grants).
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
			VALUES ($1::uuid, $2, $3, 'ACTIVE', $4, $5::uuid)
			ON CONFLICT (id) DO NOTHING`,
			adminUserID, "till-l2c-admin@hh-demo.invalid", "General manager (till L2C)", trentonAdminSub, orgID); err != nil {
			worldErr = fmt.Errorf("user %s: %w", trentonAdminSub, err)
			return
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
			SELECT $1, 'general_manager', $2::uuid, NULL
			WHERE NOT EXISTS (
				SELECT 1 FROM staff_role_grant
				WHERE platform_user_id = $1 AND role_id = 'general_manager' AND org_id = $2::uuid
				  AND branch_id IS NULL AND deleted_at IS NULL)`,
			trentonAdminSub, orgID); err != nil {
			worldErr = fmt.Errorf("admin role grant: %w", err)
			return
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO products (id, sku, name, base_uom, price_uom, is_active)
			VALUES ($1::uuid, $2, 'Till L2C test paver', 'SF', 'SF', TRUE)
			ON CONFLICT (sku) DO NOTHING`, l2cProductID, l2cProductSKU); err != nil {
			worldErr = fmt.Errorf("product: %w", err)
			return
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
			VALUES ($1::uuid, $2, 'SF')
			ON CONFLICT (product_id) DO NOTHING`, l2cProductID, l2cUnitPrice); err != nil {
			worldErr = fmt.Errorf("price anchor: %w", err)
			return
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
			VALUES ($1::uuid, 'SF', 1, TRUE)
			ON CONFLICT (product_id, uom_code) DO NOTHING`, l2cProductID); err != nil {
			worldErr = fmt.Errorf("product uom: %w", err)
			return
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
			VALUES ($1::uuid, $2::uuid, 600)
			ON CONFLICT (product_id, branch_id) DO NOTHING`, l2cProductID, trentonBranchID); err != nil {
			worldErr = fmt.Errorf("branch cost: %w", err)
			return
		}
		for _, d := range []struct {
			station, branch, byUser string
		}{
			{l2cTrentonDrawer, trentonBranchID, clerkUserID},
			{l2cKingstonDrawer, kingstonBranchID, kClerkUserID},
		} {
			if _, err := pool.Exec(ctx, `
				INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents)
				SELECT $1::uuid, $2, 'OPEN', u.id, 100000 FROM users u WHERE u.id = $3::uuid
				AND NOT EXISTS (SELECT 1 FROM till_sessions WHERE station = $2)`,
				d.branch, d.station, d.byUser); err != nil {
				worldErr = fmt.Errorf("drawer %s: %w", d.station, err)
				return
			}
		}
	})
	if worldErr != nil {
		t.Fatalf("trenton world: %v", worldErr)
	}
}

// trentonStack is one test's wiring: the real service and handler behind
// the real guards, the kit harness for the platform doubles, and a
// counting card rail.
type trentonStack struct {
	t            *testing.T
	pool         *pgxpool.Pool
	harness      *testkit.Harness
	cards        *countingCards
	mux          *http.ServeMux
	svc          *Service
	trentonTill  string
	kingstonTill string
	keys         saleKeys
}

func newTrentonStack(t *testing.T) *trentonStack {
	return newTrentonStackOnPool(t, intPool(t))
}

// newTrentonStackOnPool wires the whole rig over the caller's pool: the
// parallel-returns deadlock leg builds a second, tiny pool over the same
// DSN to prove the return path never needs a second connection while a
// Return transaction holds one.
func newTrentonStackOnPool(t *testing.T, pool *pgxpool.Pool) *trentonStack {
	return newTrentonStackOnPoolWithRail(t, pool, nil)
}

// newTrentonStackOnPoolWithRail is newTrentonStackOnPool with the card rail
// the caller chooses: nil keeps the rig's counting fake (the wall legs'
// rail, counted through s.cards), a non-nil rail is wired in its place and
// no counting fake is built at all, so s.cards keeps its one meaning: the
// fake the rig wired, nil when another rail rides instead.
func newTrentonStackOnPoolWithRail(t *testing.T, pool *pgxpool.Pool, rail CardRefunder) *trentonStack {
	t.Helper()
	buildWorld(t, pool)
	ctx := context.Background()

	var trentonTill, kingstonTill string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM till_sessions WHERE station = $1`, l2cTrentonDrawer).Scan(&trentonTill); err != nil {
		t.Fatalf("trenton drawer: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM till_sessions WHERE station = $1`, l2cKingstonDrawer).Scan(&kingstonTill); err != nil {
		t.Fatalf("kingston drawer: %v", err)
	}

	harness := testkit.NewHarness(t)
	harness.Membership.SetMember(trentonClerkSub, demoOrgSlug, true)
	harness.Membership.SetMember(trentonMgrSub, demoOrgSlug, true)
	harness.Membership.SetMember(kingstonClerkSub, demoOrgSlug, true)
	harness.Membership.SetMember(kingstonMgrSub, demoOrgSlug, true)
	harness.Membership.SetMember(trentonAdminSub, demoOrgSlug, true)
	// The elsewhere subject holds membership of another org only: the
	// from-another-org credential leg.
	harness.Membership.SetMember(elsewhereSub, testkit.OrgNorthSlug, true)
	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("org anchor: %v", err)
	}
	harness.Resolver.Set(demoOrgSlug, orgID)

	authority, err := identity.NewStaffAuthorityResolver(pool)
	if err != nil {
		t.Fatalf("staff authority resolver: %v", err)
	}
	g, err := guards.NewGuards(harness.Gate, authority, portalLegStub{}, nil)
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}

	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig.Default: %v", err)
	}
	var cards *countingCards
	if rail == nil {
		cards = &countingCards{}
		rail = cards
	}
	cat := product.NewService(product.NewRepository(pool))
	priceSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(cat).
		WithUOMConverter(cat).
		WithMarginGate(pricing.NewIdentityMarginGate())
	tillSvc := till.NewService(till.NewRepository(pool)).WithCardCharger(stubCharger{})
	auditSvc := audit.NewService(audit.NewRepository(pool), slog.Default())
	svc := NewService(NewRepository(pool)).
		WithLineResolver(priceSvc).
		WithTenderTaker(tillSvc).
		WithCreditGate(NewIdentityCreditGate()).
		WithCardRefunder(rail).
		WithAudit(auditSvc).
		WithRoleConfig(cfg).
		WithApproverSource(g).
		WithUserLinker(linkerFor(pool)).
		WithOrderInvoices(billing.NewRepository(pool)).
		WithDepositDocuments(billing.NewInvoiceWriter(pool)).
		WithBranchNames(trentonBranchNames{svc: branches.NewService(branches.NewRepository(pool))}).
		WithClock(harness.Clock.Now)

	handler := NewHandler(svc, slog.Default()).WithUserLinker(linkerFor(pool))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, g)
	return &trentonStack{t: t, pool: pool, harness: harness, cards: cards, mux: mux, svc: svc,
		trentonTill: trentonTill, kingstonTill: kingstonTill, keys: newSaleKeys()}
}

// trentonBranchNames is the cmd/core adapter's twin for the stack: branch names through the branches
// module's service.
type trentonBranchNames struct{ svc *branches.Service }

func (a trentonBranchNames) BranchNames(ctx context.Context) (map[string]string, error) {
	list, err := a.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, b := range list {
		out[b.ID] = b.Name
	}
	return out, nil
}

// linkerFor resolves the ERP users.id a platform user id links to, the B4
// linkage the composition root supplies in production.
func linkerFor(pool *pgxpool.Pool) func(context.Context, string) (string, bool) {
	return func(ctx context.Context, platformUserID string) (string, bool) {
		var id string
		err := pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE platform_user_id = $1 AND deleted_at IS NULL LIMIT 1`,
			platformUserID).Scan(&id)
		return id, err == nil
	}
}

// bearer mints a real staff bearer for one of the world's people.
func (s *trentonStack) bearer(subject string) string {
	s.t.Helper()
	return "Bearer " + s.harness.Minter.StaffToken(s.t, subject)
}

func (s *trentonStack) do(method, path, bearer, approverHeader string, body any) *httptest.ResponseRecorder {
	s.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", bearer)
	req.Header.Set("X-Org", demoOrgSlug)
	if approverHeader != "" {
		req.Header.Set(approverAuthorizationHeader, approverHeader)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func (s *trentonStack) decodeBody(rec *httptest.ResponseRecorder) map[string]any {
	s.t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		s.t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return m
}

// trentonSaleView carries the identifiers the return steps need.
type trentonSaleView struct {
	OrderID string `json:"order_id"`
	OrderNo string `json:"order_no"`
	Status  string `json:"status"`
}

// ringSale rings (or replays) one of the world's sales through the real
// composition: one line per qty in qtys, all of the one product. who rings
// it: trenton (the clerk), kingston (the Kingston clerk) or manager (the
// Sales manager, for the not-the-clerk leg). tender defaults to cash; card
// rides the stub charger and carries the processor reference.
func (s *trentonStack) ringSale(idempotencyKey, branch, drawer, who string, qtys []float64, tender string) trentonSaleView {
	s.t.Helper()
	if tender == "" {
		tender = TenderCash
	}
	lines := make([]map[string]any, 0, len(qtys))
	for _, qty := range qtys {
		lines = append(lines, map[string]any{"product_id": l2cProductID, "qty_entered": qty, "uom_entered": "SF"})
	}
	body := map[string]any{
		"idempotency_key": idempotencyKey,
		"branch_id":       branch,
		"till_session_id": drawer,
		"sale_type":       SaleCollectNow,
		"tender":          tender,
		"lines":           lines,
	}
	if tender == TenderCard {
		body["payment_token"] = "tok_till_l2c_demo"
	}
	who2 := trentonClerkSub
	switch who {
	case "kingston":
		who2 = kingstonClerkSub
	case "manager":
		who2 = trentonMgrSub
	}
	rec := s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(who2), "", body)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		s.t.Fatalf("ring sale %s: %d %s", idempotencyKey, rec.Code, rec.Body.String())
	}
	var out trentonSaleView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		s.t.Fatalf("decode sale: %v", err)
	}
	if out.Status != "COMMITTED" {
		s.t.Fatalf("sale %s not committed: %s", idempotencyKey, rec.Body.String())
	}
	return out
}

// returnBody builds a whole-line return input against one of the sales.
// The line ids come off the order lines directly, so a Kingston sale's
// body can be built without the lookup the wall refuses.
func (s *trentonStack) returnBody(key string, sale trentonSaleView, lineNo int, method string, qty float64) map[string]any {
	s.t.Helper()
	var lineID string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT id::text FROM order_lines WHERE order_id = $1::uuid ORDER BY line_no LIMIT 1 OFFSET $2`,
		sale.OrderID, lineNo).Scan(&lineID); err != nil {
		s.t.Fatalf("line %d of %s: %v", lineNo, sale.OrderID, err)
	}
	body := map[string]any{
		"idempotency_key": key,
		"order_id":        sale.OrderID,
		"refund_method":   method,
		"restock":         false,
		"lines":           []map[string]any{{"order_line_id": lineID, "qty_base": qty}},
	}
	if method == RefundCash {
		body["till_session_id"] = s.trentonTill
	}
	return body
}

// auditRows reads one action's rows filed under ONE sale, newest first
// (the lead's P3-7 ruling: a shared database holds many rows of one
// action, and the row under test is the one this sale owns).
func (s *trentonStack) auditRows(action, entityID string) []map[string]any {
	s.t.Helper()
	rows, err := s.pool.Query(context.Background(),
		`SELECT COALESCE(entity_id::text,''), COALESCE(actor_id::text,''), changes
		   FROM audit_log WHERE action = $1 AND entity_id = $2::uuid
		  ORDER BY seq DESC LIMIT 20`, action, entityID)
	if err != nil {
		s.t.Fatalf("audit read: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var entity, actor string
		var changes map[string]any
		if err := rows.Scan(&entity, &actor, &changes); err != nil {
			s.t.Fatalf("audit scan: %v", err)
		}
		changes["entity_id"] = entity
		changes["actor_id"] = actor
		out = append(out, changes)
	}
	return out
}

// committedEvent reads the newest pos.return.committed payload for one
// sale.
func (s *trentonStack) committedEvent(orderID string) map[string]any {
	s.t.Helper()
	var payload map[string]any
	err := s.pool.QueryRow(context.Background(),
		`SELECT payload FROM events_outbox WHERE topic = 'pos.return.committed' AND payload->>'order_id' = $1
		  ORDER BY occurred_at DESC LIMIT 1`, orderID).Scan(&payload)
	if err != nil {
		s.t.Fatalf("event read: %v", err)
	}
	return payload
}

// The whole story, in order, on one world.
func TestIntegration_Trenton_RefundRuleEndToEnd(t *testing.T) {
	s := newTrentonStack(t)

	// The committed sales at the exact 10.00 anchor: 500.00 of goods on the
	// Trenton sale (a 200.00 line and a 300.00 line), 100.00 on the
	// Kingston sale (CARD tendered, so the card return leg truly tests the
	// wall against the processor, the lead's P2-3 ruling), 150.00 on the
	// small sale, and 300.00 rung by the MANAGER for the not-the-clerk leg
	// (the lead's P3-2 ruling). Tax rides the composition's flat rate on
	// top of every total.
	saleTrenton := s.ringSale(s.keys.trenton, trentonBranchID, s.trentonTill, "trenton", []float64{20, 30}, TenderCash)
	saleKingston := s.ringSale(s.keys.kingston, kingstonBranchID, s.kingstonTill, "kingston", []float64{10}, TenderCard)
	saleSmall := s.ringSale(s.keys.small, trentonBranchID, s.trentonTill, "trenton", []float64{15}, TenderCash)
	saleForManager := s.ringSale(s.keys.manager, trentonBranchID, s.trentonTill, "manager", []float64{30}, TenderCash)
	saleTiny := s.ringSale(s.keys.tiny, trentonBranchID, s.trentonTill, "trenton", []float64{5}, TenderCash)

	// The lookup's positive leg first: the Trenton clerk reads the Trenton
	// sale's returnable lines.
	rec := s.do(http.MethodGet, "/api/v1/pos/returns/lookup?order_id="+saleTrenton.OrderID, s.bearer(trentonClerkSub), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the Trenton lookup must answer the clerk, got %d: %s", rec.Code, rec.Body.String())
	}
	if lk := s.decodeBody(rec); len(lk["lines"].([]any)) != 2 {
		t.Fatalf("the lookup must carry the sale's two lines: %v", lk)
	}

	// 1. The 200.00 cash return (lines[0], 20 SF) commits as Sales.
	in200 := s.returnBody(key(), saleTrenton, 0, RefundCash, 20)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", in200)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the 200.00 return must commit, got %d: %s", rec.Code, rec.Body.String())
	}
	out := s.decodeBody(rec)
	if out["total_cents"].(float64) != 22600 { // 200.00 + the flat 13 percent
		t.Fatalf("the 200.00 return's money wrong: %v", out["total_cents"])
	}

	// 2. The 300.00 return (lines[1], 30 SF) is refused with the amount and
	// both approver roles named, an audit row naming the clerk, the sale
	// and the amount, and nothing persisted.
	in300 := s.returnBody(key(), saleTrenton, 1, RefundCash, 30)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", in300)
	if rec.Code != http.StatusConflict {
		t.Fatalf("the 300.00 return must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	body := s.decodeBody(rec)
	if body["error"] != "refund_approval_required" {
		t.Fatalf("the refusal's error string wrong: %v", body["error"])
	}
	approval, ok := body["approval"].(map[string]any)
	if !ok || approval["amount_cents"].(float64) != 25000 {
		t.Fatalf("the refusal must name 250.00: %v", body)
	}
	approvers, ok := approval["approvers"].([]any)
	if !ok || len(approvers) != 2 ||
		approvers[0] != "sales_manager" || approvers[1] != "branch_manager" {
		t.Fatalf("the refusal must name both approver roles: %v", approval)
	}
	refusals := s.auditRows(auditActionRefundApprovalRefused, saleTrenton.OrderID)
	if len(refusals) == 0 {
		t.Fatal("the refusal must write its audit row")
	}
	latest := refusals[0]
	if latest["entity_id"] != saleTrenton.OrderID {
		t.Fatalf("the refusal row must be filed under the sale: %v", latest)
	}
	if latest["actor_id"] != clerkUserID {
		t.Fatalf("the refusal row must name the clerk: %v", latest)
	}
	if latest["amount_cents"] != float64(33900) { // 300.00 + the flat 13 percent
		t.Fatalf("the refusal row must keep the refused amount: %v", latest)
	}

	// The replay sits behind the wall (the lead's P2-2 ruling): the
	// KINGSTON clerk holding the Trenton return's committed key refuses
	// exactly like the first attempt, carrying none of the return.
	inReplay := s.returnBody(in200["idempotency_key"].(string), saleTrenton, 0, RefundCash, 20)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(kingstonClerkSub), "", inReplay)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a foreign replay must hit the wall, got %d: %s", rec.Code, rec.Body.String())
	}
	if replayed := s.decodeBody(rec); replayed["return_id"] != nil || replayed["total_cents"] != nil {
		t.Fatalf("a walled replay must carry none of the committed return: %v", replayed)
	}
	// Case C (the lead's P2-2R ruling): the same Kingston clerk, the same
	// Trenton key, and a KINGSTON sale they DO reach named in the body. The
	// wall judges the replayed return's own sale: refused with the wall's
	// words, the Trenton return's number, ids and money carrying nowhere.
	inC := s.returnBody(in200["idempotency_key"].(string), saleKingston, 0, RefundCash, 10)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(kingstonClerkSub), "", inC)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("case C: a foreign key over an owned sale must hit the wall, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not reachable") {
		t.Fatalf("case C: the refusal must be the wall's words: %s", rec.Body.String())
	}
	if replayed := s.decodeBody(rec); replayed["return_id"] != nil || replayed["return_no"] != nil || replayed["total_cents"] != nil {
		t.Fatalf("case C: the Trenton return must carry nowhere: %v", replayed)
	}

	// 3. The stays-refused legs: every credential short of qualifying
	// leaves the standing refusal (or refuses on its own family), and
	// nothing is committed.
	// The clerk's own credential: refused with the contract's body exactly
	// (the lead's P2-B ruling), the configured threshold and both roles,
	// never null and never zero.
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), s.bearer(trentonClerkSub), s.withKey(in300))
	if rec.Code != http.StatusConflict {
		t.Fatalf("the clerk's own credential cannot approve, got %d: %s", rec.Code, rec.Body.String())
	}
	if own := s.decodeBody(rec); own["approval"].(map[string]any)["amount_cents"] != float64(25000) {
		t.Fatalf("the refusal must carry the configured amount: %v", own)
	}
	// An expired credential refuses on the credential family.
	expired := "Bearer " + s.harness.Minter.ExpiredToken(t, testkit.StaffClass, trentonMgrSub, "")
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), expired, s.withKey(in300))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an expired credential must refuse the request, got %d: %s", rec.Code, rec.Body.String())
	}
	// A credential issued outside the window leaves the standing refusal.
	stale := s.harness.Clock.Now().Add(-6 * time.Minute)
	oldToken := "Bearer " + s.harness.Minter.Mint(t, testkit.StaffClass, trentonMgrSub, testkit.TokenShape{IssuedAt: &stale})
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), oldToken, s.withKey(in300))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a credential outside the window must leave the refusal standing, got %d: %s", rec.Code, rec.Body.String())
	}
	// The Kingston manager's credential: the listed role, a grant short of
	// the sale's branch.
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), s.bearer(kingstonMgrSub), s.withKey(in300))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a grant short of the sale's branch must leave the refusal standing, got %d: %s", rec.Code, rec.Body.String())
	}
	// Another org's credential refuses on the foreign family.
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), s.bearer(elsewhereSub), s.withKey(in300))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("another org's credential must refuse on the foreign family, got %d: %s", rec.Code, rec.Body.String())
	}
	// A credential that cannot be judged is in the trail (the lead's P3-5
	// ruling): the expired attempt above wrote its row under the sale.
	if creds := s.auditRows(auditActionRefundCredentialRefused, saleTrenton.OrderID); len(creds) == 0 {
		t.Fatal("a refused credential must write its audit row")
	}

	// 4. The same 300.00 return with the Sales manager's credential in the
	// approver header commits as the manager's decision: the audit row
	// names the manager as approver and Sales as clerk and cites the
	// refusal.
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "Bearer "+s.harness.Minter.StaffToken(t, trentonMgrSub), s.withKey(in300))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the manager's credential must commit the return, got %d: %s", rec.Code, rec.Body.String())
	}
	// The completion row is filed under the return itself; the sale is on
	// the refusal row and in the event.
	committedReturn := s.decodeBody(rec)["return_id"].(string)
	committed := s.auditRows("RETURN_COMPLETED", committedReturn)
	var decisionRow map[string]any
	for _, row := range committed {
		if row["approver_id"] == managerUserID {
			decisionRow = row
			break
		}
	}
	if decisionRow == nil {
		t.Fatalf("no completion row names the manager as approver: %v", committed)
	}
	// The approver is named by the STABLE staff users id (the lead's P2-4
	// ruling): the platform subject carries a seven-digit run, the shape
	// the chokepoint redacts, and it must not be the name that decides the
	// row.
	if decisionRow["approver_id"] == trentonMgrSub || decisionRow["approver_id"] == "[redacted]" {
		t.Fatalf("the approver must be the staff users id, not the redactable subject: %v", decisionRow)
	}
	if decisionRow["subject_ref"] == nil {
		t.Fatalf("the row keeps the platform subject as the directory reference: %v", decisionRow)
	}
	if decisionRow["actor_id"] != clerkUserID {
		t.Fatalf("the decision row must keep Sales as the clerk who rang it: %v", decisionRow)
	}
	if decisionRow["reason"] != "refund_approval_required" {
		t.Fatalf("the decision row must cite the refusal: %v", decisionRow)
	}

	// The committed record names the decision in one identifier space and
	// cites the refusal's own audit row id (the lead's P3-3 ruling).
	event := s.committedEvent(saleTrenton.OrderID)
	if event["decided_by"] != managerUserID || event["rang_by"] != clerkUserID {
		t.Fatalf("the record must name both people by staff id: %v", event)
	}
	if event["decided_by_subject"] != trentonMgrSub {
		t.Fatalf("the record keeps the platform subject as the directory reference: %v", event)
	}
	committedApproval, isMap := event["approval"].(map[string]any)
	if !isMap || committedApproval["refusal"] != "refund_approval_required" {
		t.Fatalf("the record must cite the refusal: %v", event["approval"])
	}
	if committedApproval["refund_cents"] != float64(33900) {
		t.Fatalf("the event carries the refund total under its own name: %v", committedApproval)
	}
	if committedApproval["refusal_id"] == nil || committedApproval["refusal_id"] == "" {
		t.Fatalf("the decision must cite the refusal's audit row id: %v", committedApproval)
	}

	// 5a. The isolating leg for the not-the-clerk check (the lead's P3-2
	// ruling): the MANAGER rings a sale and attaches their OWN credential.
	// The role is on the approvers list and the grants reach the branch, so
	// only the not-the-clerk check can refuse, and it does.
	inOwn := s.returnBody(key(), saleForManager, 0, RefundCash, 30)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonMgrSub), s.bearer(trentonMgrSub), inOwn)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a listed role's own credential cannot approve its own refund, got %d: %s", rec.Code, rec.Body.String())
	}
	if own := s.decodeBody(rec); own["approval"].(map[string]any)["amount_cents"] != float64(25000) {
		t.Fatalf("the refusal must carry the configured amount, never zero: %v", own)
	}
	// Within the amount the header is inert (the lead's P2-B ruling): the
	// tiny sale's return commits as the manager's own decision with the
	// manager's own credential in the header, nobody consulted.
	inInert := s.returnBody(key(), saleTiny, 0, RefundCash, 5)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonMgrSub), s.bearer(trentonMgrSub), inInert)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a within-amount return commits whoever's credential arrived, got %d: %s", rec.Code, rec.Body.String())
	}
	if inert := s.decodeBody(rec); inert["total_cents"] != float64(5650) {
		t.Fatalf("the inert return's money wrong: %v", inert)
	}
	// 5b. As Sales manager the 300.00 return commits with no second
	// credential.
	inMgr := s.returnBody(key(), saleForManager, 0, RefundCash, 30)
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonMgrSub), "", inMgr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the listed role commits with no second credential, got %d: %s", rec.Code, rec.Body.String())
	}

	// 6. The Kingston wall: every refund method refused to the Trenton
	// login, the lookup likewise, and the counting card rail sees nothing.
	for _, method := range []string{RefundCash, RefundCard, RefundAccountCredit} {
		inK := s.returnBody(key(), saleKingston, 0, method, 10)
		rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", inK)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("the %s return on a Kingston sale must hit the wall, got %d: %s", method, rec.Code, rec.Body.String())
		}
	}
	rec = s.do(http.MethodGet, "/api/v1/pos/returns/lookup?order_id="+saleKingston.OrderID, s.bearer(trentonClerkSub), "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the Kingston lookup must hit the wall, got %d: %s", rec.Code, rec.Body.String())
	}
	if s.cards.calls != 0 {
		t.Fatalf("the card rail must see no call behind the wall, saw %d", s.cards.calls)
	}

	// 7. Lowering the configured amount to 100.00 through a validated
	// modified document changes the answer with no code change.
	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("unmarshal default: %v", err)
	}
	doc.Settings[roleconfig.SettingRefundAmountWithoutApproval] = json.RawMessage(`10000`)
	lowered, err := roleconfig.Validate(&doc)
	if err != nil {
		t.Fatalf("validate modified document: %v", err)
	}
	s.svc = s.svc.WithRoleConfig(lowered)
	inSmall := s.returnBody(key(), saleSmall, 0, RefundCash, 15) // 150.00: fine at 250.00, above 100.00
	rec = s.do(http.MethodPost, "/api/v1/pos/returns", s.bearer(trentonClerkSub), "", inSmall)
	if rec.Code != http.StatusConflict {
		t.Fatalf("the 150.00 return must refuse under the lowered amount, got %d: %s", rec.Code, rec.Body.String())
	}
	body = s.decodeBody(rec)
	if approval, ok := body["approval"].(map[string]any); !ok || approval["amount_cents"].(float64) != 10000 {
		t.Fatalf("the refusal must name the lowered amount: %v", body)
	}

	// 8. The margin glance under the seeded MANAGER_UP policy: the
	// configuration's list considers Inside Sales, the policy holds the
	// answer at the server, so the clerk's glance is the byte-absent {}
	// while the manager's carries the ticket's lines; Kingston is refused
	// to the Trenton clerk. Then the rig drives the admin policy write
	// (UpdatePolicy is admin-gated, the route is RequireAdmin): the org
	// switch flipped to ALL_STAFF carries the clerk's glance with no code
	// change, and the row is restored to the captured values afterwards.
	glance := func() map[string]any {
		return map[string]any{
			"branch_id": trentonBranchID,
			"lines":     []map[string]any{{"product_id": l2cProductID, "qty_entered": 10, "uom_entered": "SF"}},
		}
	}
	rec = s.do(http.MethodPost, "/api/v1/pos/margin-glance", s.bearer(trentonClerkSub), "", glance())
	if rec.Code != http.StatusOK {
		t.Fatalf("the glance must answer the clerk, got %d: %s", rec.Code, rec.Body.String())
	}
	if clerkGlance := s.decodeBody(rec); len(clerkGlance) != 0 {
		t.Fatalf("under MANAGER_UP the clerk's glance must be the byte-absent {}, got: %v", clerkGlance)
	}
	rec = s.do(http.MethodPost, "/api/v1/pos/margin-glance", s.bearer(trentonMgrSub), "", glance())
	if rec.Code != http.StatusOK {
		t.Fatalf("the glance must answer the manager, got %d: %s", rec.Code, rec.Body.String())
	}
	out = s.decodeBody(rec)
	lines, ok := out["lines"].([]any)
	if !ok || len(lines) == 0 {
		t.Fatalf("the glance must carry margin for the manager: %v", out)
	}
	if line := lines[0].(map[string]any); line["margin_bps"] == nil {
		t.Fatalf("margin_bps must be present for the manager: %v", line)
	}
	kingstonGlance := map[string]any{
		"branch_id": kingstonBranchID,
		"lines":     []map[string]any{{"product_id": l2cProductID, "qty_entered": 10, "uom_entered": "SF"}},
	}
	rec = s.do(http.MethodPost, "/api/v1/pos/margin-glance", s.bearer(trentonClerkSub), "", kingstonGlance)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the glance must refuse Kingston to the Trenton clerk, got %d: %s", rec.Code, rec.Body.String())
	}

	// The org row is captured whole, straight from the table: the GET route
	// derives an un-curated chip set at read time, and writing that back
	// would curate it, a change the dealer never made.
	var codEnabled, chipsEnabled bool
	var restockingFee, splitPct int
	var notes, linePres, workspaceArr, lookupPres, marginVis, chips, balanceVis string
	if err := s.pool.QueryRow(context.Background(), `
		SELECT cod_enabled, restocking_fee_bps, notes, line_presentation, quick_chips_enabled,
		       workspace_split_pct, workspace_arrangement, lookup_presentation, margin_visibility,
		       COALESCE(quick_chip_set, ''), balance_visibility
		  FROM pos_policy WHERE branch_id IS NULL`).
		Scan(&codEnabled, &restockingFee, &notes, &linePres, &chipsEnabled, &splitPct,
			&workspaceArr, &lookupPres, &marginVis, &chips, &balanceVis); err != nil {
		t.Fatalf("the org policy row is missing: %v", err)
	}
	if marginVis != MarginVisibilityManagerUp {
		t.Fatalf("the seeded org policy is %q, the leg's story hangs off the MANAGER_UP default", marginVis)
	}

	// The restore is t.Cleanup's job: the cleanup runs on the leg's every
	// exit path, so a failure between the flip and the end of the test
	// cannot leave the org row clobbered behind it. The cleanup puts the
	// WHOLE captured row back through the same admin route and then
	// compares the whole row against the capture, not the visibility word
	// alone.
	t.Cleanup(func() {
		rsp := s.do(http.MethodPut, "/api/v1/pos/policy", s.bearer(trentonAdminSub), "", PosPolicy{
			CODEnabled: codEnabled, RestockingFeeBps: restockingFee, Notes: notes,
			LinePresentation: linePres, QuickChipsEnabled: chipsEnabled,
			WorkspaceSplitPct: splitPct, WorkspaceArrangement: workspaceArr,
			LookupPresentation: lookupPres, MarginVisibility: marginVis,
			QuickChipSet: splitChipSet(chips), BalanceVisibility: balanceVis,
		})
		if rsp.Code != http.StatusOK {
			t.Errorf("the policy restore must answer, got %d: %s", rsp.Code, rsp.Body.String())
			return
		}
		var gotCod, gotChipsEnabled bool
		var gotFee, gotSplit int
		var gotNotes, gotLinePres, gotArr, gotLookup, gotVis, gotChips, gotBalance string
		if err := s.pool.QueryRow(context.Background(), `
			SELECT cod_enabled, restocking_fee_bps, notes, line_presentation, quick_chips_enabled,
			       workspace_split_pct, workspace_arrangement, lookup_presentation, margin_visibility,
			       COALESCE(quick_chip_set, ''), balance_visibility
			  FROM pos_policy WHERE branch_id IS NULL`).
			Scan(&gotCod, &gotFee, &gotNotes, &gotLinePres, &gotChipsEnabled, &gotSplit,
				&gotArr, &gotLookup, &gotVis, &gotChips, &gotBalance); err != nil {
			t.Errorf("the org policy row went missing: %v", err)
			return
		}
		if gotCod != codEnabled || gotFee != restockingFee || gotNotes != notes ||
			gotLinePres != linePres || gotChipsEnabled != chipsEnabled || gotSplit != splitPct ||
			gotArr != workspaceArr || gotLookup != lookupPres || gotVis != marginVis ||
			gotChips != chips || gotBalance != balanceVis {
			t.Errorf("the org policy row must be left as it was found: cod=%v fee=%d notes=%q line=%q chipsEnabled=%v split=%d arr=%q lookup=%q vis=%q chips=%q balance=%q",
				gotCod, gotFee, gotNotes, gotLinePres, gotChipsEnabled, gotSplit,
				gotArr, gotLookup, gotVis, gotChips, gotBalance)
		}
	})

	rec = s.do(http.MethodPut, "/api/v1/pos/policy", s.bearer(trentonAdminSub), "",
		map[string]any{"branch_id": "", "margin_visibility": MarginVisibilityAllStaff})
	if rec.Code != http.StatusOK {
		t.Fatalf("the admin policy write must answer, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = s.do(http.MethodPost, "/api/v1/pos/margin-glance", s.bearer(trentonClerkSub), "", glance())
	if rec.Code != http.StatusOK {
		t.Fatalf("the glance must answer the clerk after the flip, got %d: %s", rec.Code, rec.Body.String())
	}
	out = s.decodeBody(rec)
	lines, ok = out["lines"].([]any)
	if !ok || len(lines) == 0 {
		t.Fatalf("under ALL_STAFF the clerk's glance must carry margin: %v", out)
	}
	if line := lines[0].(map[string]any); line["margin_bps"] == nil {
		t.Fatalf("margin_bps must be present for the clerk under ALL_STAFF: %v", line)
	}
}

// withKey re-keys a return body so each attempt is its own idempotency
// slot.
func (s *trentonStack) withKey(body map[string]any) map[string]any {
	s.t.Helper()
	next := make(map[string]any, len(body))
	for k, v := range body {
		next[k] = v
	}
	next["idempotency_key"] = key()
	return next
}
