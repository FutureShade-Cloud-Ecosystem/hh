package main

// dashboard_user_linker_wire_test.go is the wiring-level evidence for
// HH-189: the dashboard handler the fold registers must carry the ERP
// users.id linker every sibling handler gets, because the handler's own
// my-space routes (save MY tile layout, save MY widget layout) and the
// Needs-you queue's four-eyes filter all read the UserID the request
// stamp carries. The tests drive the REAL wiring: wireMoney,
// wireOperations and wireTops over the scratch database, the composition
// root's own construction, and then, through the REGISTERED ROUTES:
//
//   - the sibling contrast first: the same bearer's own messaging read (a
//     handler that DID get the linker) answers 200 in the same fold, so a
//     refusal on the dashboard legs below names the missing linker, not a
//     broken fold or an unlinked caller;
//   - a staffer saves their own tile layout through PUT
//     /api/v1/dashboard/layout and their own widget layout through PUT
//     /api/v1/dashboard/widgets, both of which refuse with 400 no caller
//     when the stamp carries no linked id (the widget body is drawn from
//     the caller's own catalog read, so the leg validates nothing but the
//     stamp it exists to pin);
//   - the Needs four-eyes half: a general manager's own PENDING margin
//     request, planted through the approvals rail's own seam, is NOT
//     waiting on them. With the linked id on the stamp the queue's
//     approvals row is absent; with the id empty the four-eyes skip never
//     matches and the manager's own request is counted as waiting on
//     them, the overstated approval count of the record.
//
// The caller world is the migration-planted one (a branch from 0003) plus
// a users row linking the kit's staff subject, exactly the W0 harness
// shape the card rail wiring test uses. Rows the legs write or plant are
// cleaned with the test, except the users rows, which stay like the card
// rail kit's (they are inert, keyed by the kit's fixed subjects, and the
// planting is idempotent); a shared scratch database survives the run.
// The tests gate on HH_CORE_TEST_DATABASE_URL like the other composition
// suites.
//
// The profile-editor half: a role default saved through PUT
// /api/v1/dashboard/profiles/{profile} stores the editor's linked users.id
// in updated_by, which stays NULL when the stamp carries no linked id.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// dashVaultKey mints the hex key buildCardRail needs to boot. No rail rows
// are planted, so the payments surface stays unconfigured and the dashboard
// legs never touch it.
func dashVaultKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("mint vault key: %v", err)
	}
	return hex.EncodeToString(raw)
}

// plantDashboardCaller plants the users row linking the kit's staff subject
// (the linkage the linker reads, users.platform_user_id) and reads a
// migration-planted branch, the same shape the card rail wiring counter
// uses without the counter world.
func plantDashboardCaller(t *testing.T, pool *pgxpool.Pool) (userID, branchID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, display_name, platform_user_id, status) VALUES ($1, $2, $3, 'ACTIVE')
		 ON CONFLICT DO NOTHING`, "fxstaff-any@dashboard.invalid", "Dashboard Caller", testkit.StaffSubAny); err != nil {
		t.Fatalf("plant linked user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE platform_user_id = $1`, testkit.StaffSubAny).Scan(&userID); err != nil {
		t.Fatalf("linked user: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY id LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no migration-planted branch: %v", err)
	}
	return userID, branchID
}

// dashFold wires the composition root's own construction the way the card
// rail wiring test does: wireMoney, wireOperations and wireTops on a plain
// mux behind the kit's staff guards, with the vault key buildCardRail
// needs and nothing else configured.
func dashFold(t *testing.T, pool *pgxpool.Pool, sg *testkit.StaffGuards) *http.ServeMux {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
	ops := wireOperations(mux, pool, sg.Guards, log, money, "fx-ticket-key", belt.outbox)
	wireTops(mux, pool, sg.Guards, log, money, ops, config.Config{PaymentVaultKey: dashVaultKey(t)}, mustRoleConfigForWire(), belt)
	return mux
}

// driveJSON drives one authenticated JSON request through the wired mux
// with any method, the generalization of the card rail kit's postJSON.
func driveJSON(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode %s: %v", path, err)
		}
		rdr = strings.NewReader(string(raw))
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

// needsApprovalsCount reads the Needs queue and returns the approvals row's
// count, or -1 when the row is absent (nothing waiting).
func needsApprovalsCount(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards) int {
	t.Helper()
	rec := driveJSON(t, mux, sg, http.MethodGet, "/api/v1/dashboard/needs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("needs queue = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var queue struct {
		Rows []struct {
			SourceKey string `json:"source_key"`
			Count     int    `json:"count"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &queue); err != nil {
		t.Fatalf("decode needs queue: %v", err)
	}
	for _, row := range queue.Rows {
		if row.SourceKey == dashboard.NeedsApprovals {
			return row.Count
		}
	}
	return -1
}

// TestDashboardUserLinkerWiring_MySavesReachTheStore, HH-189: through the
// real wiring, a staffer's own tile and widget saves reach the store
// instead of being refused with 400 no caller.
func TestDashboardUserLinkerWiring_MySavesReachTheStore(t *testing.T) {
	pool := wiringPool(t)
	userID, branchID := plantDashboardCaller(t, pool)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM dashboard_user_tiles WHERE user_id = $1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM dashboard_user_widgets WHERE user_id = $1`, userID)
	})

	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{
		RoleID: "inside_sales", Tier: 3, Scope: identity.ScopeBranch, Branches: []string{branchID},
	})
	mux := dashFold(t, pool, sg)

	// The sibling contrast: the messaging handler got the linker, so the
	// same bearer's own read answers 200 in this fold. A refusal here
	// would mean the caller is not linked at all and the dashboard legs
	// below would prove nothing about the dashboard's own wiring.
	if rec := driveJSON(t, mux, sg, http.MethodGet, "/api/v1/messaging/unread", nil); rec.Code != http.StatusOK {
		t.Fatalf("sibling messaging unread = %d (%s), want 200: the fold links the caller, so a dashboard refusal below names the missing linker",
			rec.Code, rec.Body.String())
	}

	// MY tile layout: the staffer reorders their home and saves it.
	rec := driveJSON(t, mux, sg, http.MethodPut, "/api/v1/dashboard/layout", map[string]any{
		"tiles": []map[string]any{{"tile_id": dashboard.TileSalesToday, "position": 0, "visible": true}},
	})
	// Errorf, not Fatalf: the widget leg below is independent, so one run
	// on a fold without the linker shows BOTH refusals.
	if rec.Code != http.StatusOK {
		t.Errorf("save my tile layout = %d (%s), want 200: the staffer's own save was refused",
			rec.Code, rec.Body.String())
	}
	var tiles int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM dashboard_user_tiles WHERE user_id = $1::uuid`, userID).Scan(&tiles); err != nil {
		t.Fatalf("read back my tiles: %v", err)
	}
	if tiles != 1 {
		t.Errorf("my tile layout rows for the linked user = %d, want 1: the save must land under the linked users.id", tiles)
	}

	// MY widget layout: the same stamp's other half. The body is drawn
	// from the caller's own catalog read so the leg carries no fixture
	// facts about the catalog itself.
	rec = driveJSON(t, mux, sg, http.MethodGet, "/api/v1/dashboard/widgets/catalog", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("widget catalog = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var catalog struct {
		Measures []struct {
			ID           string   `json:"metric_id"`
			Splits       []string `json:"splits"`
			WidgetShapes []string `json:"widget_shapes"`
			Ranges       []string `json:"ranges"`
		} `json:"measures"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode widget catalog: %v", err)
	}
	if len(catalog.Measures) == 0 || len(catalog.Measures[0].Splits) == 0 || len(catalog.Measures[0].WidgetShapes) == 0 {
		t.Fatal("the counter catalog offers no drawable measure: the widget leg cannot build a valid body")
	}
	widget := map[string]any{
		"widget_key": "fx189w1",
		"measure":    catalog.Measures[0].ID,
		"shape":      catalog.Measures[0].WidgetShapes[0],
		"split":      catalog.Measures[0].Splits[0],
		"title":      "Fixture",
		"position":   0,
		"visible":    true,
	}
	if len(catalog.Measures[0].Ranges) > 0 {
		widget["range"] = catalog.Measures[0].Ranges[0]
	}
	if rec := driveJSON(t, mux, sg, http.MethodPut, "/api/v1/dashboard/widgets",
		map[string]any{"widgets": []map[string]any{widget}}); rec.Code != http.StatusOK {
		t.Errorf("save my widget layout = %d (%s), want 200: the staffer's own save was refused",
			rec.Code, rec.Body.String())
	}
	var widgets int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM dashboard_user_widgets WHERE user_id = $1::uuid`, userID).Scan(&widgets); err != nil {
		t.Fatalf("read back my widgets: %v", err)
	}
	if widgets != 1 {
		t.Errorf("my widget rows for the linked user = %d, want 1: the save must land under the linked users.id", widgets)
	}
}

// TestDashboardUserLinkerWiring_NeedsFourEyesSkipsTheManagersOwnRequest,
// HH-189: the Needs-you queue's approvals row never counts the caller's
// own PENDING request, which needs the linked ERP users.id on the stamp;
// with the id empty the four-eyes skip never matches and the queue tells a
// general manager that money is waiting on them when it is their own ask.
func TestDashboardUserLinkerWiring_NeedsFourEyesSkipsTheManagersOwnRequest(t *testing.T) {
	pool := wiringPool(t)
	userID, branchID := plantDashboardCaller(t, pool)

	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{
		RoleID: "general_manager", Tier: 1, Scope: identity.ScopeGlobal,
	})
	mux := dashFold(t, pool, sg)

	// The queue starts honest: nothing is planted, so nothing waits.
	if n := needsApprovalsCount(t, mux, sg); n >= 0 {
		t.Fatalf("the Needs queue counts %d approval(s) waiting on the manager before anything is planted, want none",
			n)
	}

	// The manager files their OWN pending margin request through the
	// approvals rail's floor filing door, the only path a margin card is
	// filed on (D7): CreateApproval refuses the policy outright.
	rail := staffcontext.NewApprovals(staffcontext.NewApprovalRepository(pool))
	request, err := rail.FileFloorApproval(context.Background(), staffcontext.FloorApprovalFiling{
		RequestedBy: userID,
		BranchID:    branchID,
		SubjectRef:  "fx189-own-quote",
		Detail:      "HH-189 fixture: the general manager's own pending request",
		LineNo:      1,
		DiscPct:     40,
		LineID:      "11111111-1111-4111-8111-111111001101",
		ProductID:   "prod-189-own",
	})
	if err != nil {
		t.Fatalf("plant own pending approval: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, request.ID)
	})

	// Four-eyes: the manager's own request is not waiting on them, so the
	// approvals row stays absent.
	if n := needsApprovalsCount(t, mux, sg); n >= 0 {
		t.Errorf("the Needs queue counts %d approval(s) waiting on the manager, want none: the manager's own PENDING request is their own ask, and the four-eyes skip needs the linked id on the stamp",
			n)
	}

	// The standing positive control: ANOTHER user's pending request on the
	// same rail does wait on the manager, so the absence above is the
	// four-eyes skip and not an unwired or blind approvals source.
	var otherID string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, display_name, platform_user_id, status)
		 VALUES ('fxother-189@dashboard.invalid', 'Dashboard Other', 'fx189-other-subject', 'ACTIVE')
		 ON CONFLICT (email) DO UPDATE SET display_name = EXCLUDED.display_name
		 RETURNING id::text`).Scan(&otherID); err != nil {
		t.Fatalf("plant the other requester: %v", err)
	}
	other, err := rail.FileFloorApproval(context.Background(), staffcontext.FloorApprovalFiling{
		RequestedBy: otherID,
		BranchID:    branchID,
		SubjectRef:  "fx189-other-quote",
		Detail:      "HH-189 fixture: another user's pending request",
		LineNo:      1,
		DiscPct:     40,
		LineID:      "11111111-1111-4111-8111-111111001102",
		ProductID:   "prod-189-other",
	})
	if err != nil {
		t.Fatalf("plant the other user's pending approval: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, other.ID)
	})
	if n := needsApprovalsCount(t, mux, sg); n != 1 {
		t.Errorf("the Needs queue counts %d approval(s) with one other user's pending request, want 1: the source must see requests that do wait on the manager", n)
	}
}

// TestDashboardUserLinkerWiring_ProfileEditorStampsTheEditor, HH-189: the
// role-default editor records WHO saved the default. Without the linked id
// the stamp carries no user and updated_by stays NULL.
func TestDashboardUserLinkerWiring_ProfileEditorStampsTheEditor(t *testing.T) {
	pool := wiringPool(t)
	userID, _ := plantDashboardCaller(t, pool)
	ctx := context.Background()
	profile := dashboard.Profiles[0]

	// The profile's default rows are migration-seeded; a save replaces them
	// wholesale, so snapshot them and put them back with the test.
	type saved struct {
		tile      string
		position  int
		visible   bool
		updatedBy *string
	}
	var before []saved
	rows, err := pool.Query(ctx,
		`SELECT tile_id, position, visible, updated_by::text FROM dashboard_profile_tiles WHERE profile = $1`, profile)
	if err != nil {
		t.Fatalf("snapshot profile defaults: %v", err)
	}
	for rows.Next() {
		var r saved
		if err := rows.Scan(&r.tile, &r.position, &r.visible, &r.updatedBy); err != nil {
			t.Fatalf("scan profile defaults: %v", err)
		}
		before = append(before, r)
	}
	rows.Close()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM dashboard_profile_tiles WHERE profile = $1`, profile)
		for _, r := range before {
			_, _ = pool.Exec(ctx,
				`INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible, updated_by, updated_at)
				 VALUES ($1, $2, $3, $4, $5::uuid, now())`, profile, r.tile, r.position, r.visible, r.updatedBy)
		}
	})

	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: identity.ScopeGlobal})
	mux := dashFold(t, pool, sg)

	rec := driveJSON(t, mux, sg, http.MethodPut, "/api/v1/dashboard/profiles/"+profile, map[string]any{
		"tiles": []map[string]any{{"tile_id": dashboard.TileSalesToday, "position": 0, "visible": true}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save the %s role default = %d (%s), want 200", profile, rec.Code, rec.Body.String())
	}
	var by *string
	if err := pool.QueryRow(ctx,
		`SELECT updated_by::text FROM dashboard_profile_tiles WHERE profile = $1 LIMIT 1`, profile).Scan(&by); err != nil {
		t.Fatalf("read back the role default: %v", err)
	}
	if by == nil || *by != userID {
		got := "NULL"
		if by != nil {
			got = *by
		}
		t.Errorf("the role default's updated_by = %s, want the editor's linked users.id %s", got, userID)
	}
}
