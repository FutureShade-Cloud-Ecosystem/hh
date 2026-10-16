//go:build integration

// roster_people_integration_test.go: slice S1, the staffer readable roster
// route (GET /api/v1/messaging/people) on the real repository over the
// migrated and seeded database, behind the testkit guards. Red first: before
// the route was registered the mux answered 404 on every leg, the recorded
// red evidence in the item report; each leg asserts its own status and body,
// so a leg that stops being written fails here rather than drifting.
//
// The probe staff are synthetic rows this file plants and deletes by id
// (CASCADE takes their grants), never seeded demo rows.

package messaging

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The roster probes' fixed world. The caller is the staffer the static user
// link answers, so the is_me flag lands on a row this file planted. The
// foreign org id is any uuid that is not the testkit fixture org's.
const (
	rosterCallerID  = "d7000000-0000-4000-9000-0000000001e1"
	rosterAlphaID   = "d7000000-0000-4000-9000-0000000001e2"
	rosterBetaID    = "d7000000-0000-4000-9000-0000000001e3"
	rosterGoneID    = "d7000000-0000-4000-9000-0000000001e4"
	rosterMissID    = "d7000000-0000-4000-9000-0000000001e5"
	rosterForeignID = "d7000000-0000-4000-9000-0000000001e6"
	rosterSuperID   = "d7000000-0000-4000-9000-0000000001e7"

	rosterForeignOrg = "eeeeeeee-0000-4000-8000-000000000001"

	rosterAlphaName = "Zzprobe Alpha"
	rosterBetaName  = "Zzprobe Beta"

	// rYardTeam is the seeded YARD_TEAM role (0015): display name never
	// scrubbed, so the role label assertion reads stable words.
	rYardTeam = "e7000000-0000-4000-9000-000000000012"

	// rSuperAdmin is the seeded platform role (0043): the holder is no
	// dealer's staffer and must never answer the picker.
	rSuperAdmin = "e7000000-0000-4000-9000-000000000014"

	// branchTrenton is the integration suite's seeded Trenton branch root
	// (0003), declared in integration_test.go.
)

// seedRosterProbes plants the probe staff and returns the cleanup.
func seedRosterProbes(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, status) VALUES
		  ($1, 's1roster.caller@test.invalid', 'Zzprobe Caller', 'ACTIVE'),
		  ($2, 's1roster.alpha@test.invalid',  $7,               'ACTIVE'),
		  ($3, 's1roster.beta@test.invalid',   $8,               'ACTIVE'),
		  ($4, 's1roster.gone@test.invalid',   'Zzprobe Gone',   'SUSPENDED'),
		  ($5, 's1roster.miss@test.invalid',   'Zzprobe Miss',   'DEACTIVATED'),
		  ($6, 's1roster.foreign@test.invalid','Zzprobe Foreign','ACTIVE'),
		  ($9, 's1roster.super@test.invalid',  'Platform Operator Probe', 'ACTIVE')
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, display_name = EXCLUDED.display_name`,
		rosterCallerID, rosterAlphaID, rosterBetaID, rosterGoneID, rosterMissID, rosterForeignID,
		rosterAlphaName, rosterBetaName, rosterSuperID)
	if err != nil {
		t.Fatalf("seed probe users: %v", err)
	}
	// Alpha holds the Yard Team role and the Trenton grant, so the labels
	// have words to read; Beta holds none, so the labels are empty. The
	// platform operator holds the SUPER_ADMIN role and must never answer.
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2), ($3, $4)
		ON CONFLICT DO NOTHING`, rosterAlphaID, rYardTeam, rosterSuperID, rSuperAdmin); err != nil {
		t.Fatalf("seed probe roles: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_branch_grants (user_id, branch_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, rosterAlphaID, branchTrenton); err != nil {
		t.Fatalf("seed probe grant: %v", err)
	}
	// The foreign org staffer is anchored away from the fixture org.
	if _, err := pool.Exec(ctx, `
		UPDATE users SET org_id = $2::uuid WHERE id = $1`, rosterForeignID, rosterForeignOrg); err != nil {
		t.Fatalf("anchor the foreign org staffer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`,
			[]string{rosterCallerID, rosterAlphaID, rosterBetaID, rosterGoneID, rosterMissID, rosterForeignID, rosterSuperID})
	})
}

// newRosterRoutes mounts the messaging routes over the real repository pool
// behind the testkit guards, with the static user link answering the probe
// caller. It returns the mux and the staff bearer.
func newRosterRoutes(t *testing.T, pool *pgxpool.Pool) (*http.ServeMux, string) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{
		RoleID: "yard_team", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
	})
	h := NewHandler(NewService(NewRepository(pool)), nil).
		WithUserLinker(func(_ context.Context, _ string) (string, bool) { return rosterCallerID, true })
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return mux, sg.Bearer(t)
}

// rosterDo sends one roster request with the staff bearer and the fixture org.
func rosterDo(t *testing.T, mux *http.ServeMux, bearer, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// rosterPage is the route's envelope.
type rosterPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor *string          `json:"next_cursor"`
	Limit      int              `json:"limit"`
}

func rosterGet(t *testing.T, pool *pgxpool.Pool, target string) rosterPage {
	t.Helper()
	mux, bearer := newRosterRoutes(t, pool)
	rec := rosterDo(t, mux, bearer, target)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s, want 200", target, rec.Code, rec.Body.String())
	}
	var page rosterPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode the roster page: %v (%s)", err, rec.Body.String())
	}
	return page
}

// rosterFind returns the item whose user_id matches, or nil.
func rosterFind(page rosterPage, userID string) map[string]any {
	for _, it := range page.Items {
		if it["user_id"] == userID {
			return it
		}
	}
	return nil
}

// The roster answers the org's active staff: both probe actives are on it,
// the suspended and deactivated ones are not, and the staffer anchored to
// another org is not. Alpha's labels read the role catalog's display name
// and the branch's own name; Beta, holding neither role nor grant, reads
// empty labels. The caller's own row is flagged is_me and nobody else's is.
func TestIntegrationRoster_ActiveStaffOfTheOrgOnly(t *testing.T) {
	pool := intPool(t)
	seedRosterProbes(t, pool)
	page := rosterGet(t, pool, "/api/v1/messaging/people?limit=200")

	if len(page.Items) == 0 {
		t.Fatal("the roster answered nobody")
	}
	alpha := rosterFind(page, rosterAlphaID)
	if alpha == nil {
		t.Fatalf("the active probe is missing from the roster: %d items", len(page.Items))
	}
	beta := rosterFind(page, rosterBetaID)
	if beta == nil {
		t.Fatal("the second active probe is missing from the roster")
	}
	if rosterFind(page, rosterGoneID) != nil {
		t.Error("the SUSPENDED staffer must not be on the roster")
	}
	if rosterFind(page, rosterMissID) != nil {
		t.Error("the DEACTIVATED staffer must not be on the roster")
	}
	if rosterFind(page, rosterForeignID) != nil {
		t.Error("a staffer anchored to another org must not be on the roster")
	}
	if rosterFind(page, rosterSuperID) != nil {
		t.Error("the platform operator (a SUPER_ADMIN holder) must not be on the roster")
	}
	if alpha["role_label"] != "Yard Team" {
		t.Errorf("role_label = %v, want the role catalog's display name", alpha["role_label"])
	}
	if bl, _ := alpha["branch_label"].(string); !strings.Contains(bl, "Trenton") {
		t.Errorf("branch_label = %q, want the granted branch's own name", bl)
	}
	if beta["role_label"] != "" || beta["branch_label"] != "" {
		t.Errorf("a staffer with no role and no grant reads empty labels, got %v %v",
			beta["role_label"], beta["branch_label"])
	}
	if alpha["is_me"] != false || beta["is_me"] != false {
		t.Error("nobody else's row may carry is_me")
	}
	caller := rosterFind(page, rosterCallerID)
	if caller == nil {
		t.Fatal("the caller is missing from the roster")
	}
	if caller["is_me"] != true {
		t.Error("the caller's own row must carry is_me")
	}
}

// The item shape is exactly the five picker fields: no email, no phone, no
// permission or cost field, no tier, no role code. The whole key set is
// asserted, not single keys, so an added field fails here and is a
// deliberate contract move.
func TestIntegrationRoster_NoEmailOrPhoneKeys(t *testing.T) {
	pool := intPool(t)
	seedRosterProbes(t, pool)
	page := rosterGet(t, pool, "/api/v1/messaging/people?q=zzprobe")
	if len(page.Items) != 3 {
		t.Fatalf("the probe filter answered %d items, want the three probe staffers", len(page.Items))
	}
	for _, it := range page.Items {
		keys := map[string]bool{}
		for k := range it {
			keys[k] = true
		}
		want := []string{"user_id", "display_name", "role_label", "branch_label", "is_me"}
		if len(keys) != len(want) {
			t.Errorf("item keys %v, want exactly %v", keys, want)
			continue
		}
		for _, k := range want {
			if !keys[k] {
				t.Errorf("item carries %v, missing %q", keys, k)
			}
		}
		for _, banned := range []string{"email", "phone", "tier", "role_code", "scope", "branch_ids"} {
			if keys[banned] {
				t.Errorf("the roster item carries the %q key", banned)
			}
		}
	}
}

// q is a case insensitive contains match on display name: the middle word
// matches, the case does not matter, and a word on nobody answers an empty
// page with a null next cursor.
func TestIntegrationRoster_QMatches(t *testing.T) {
	pool := intPool(t)
	seedRosterProbes(t, pool)

	page := rosterGet(t, pool, "/api/v1/messaging/people?q=ZZPROBE")
	if len(page.Items) != 3 {
		t.Fatalf("q=%q answered %d items, want the three probe staffers", "ZZPROBE", len(page.Items))
	}
	if rosterFind(page, rosterAlphaID) == nil || rosterFind(page, rosterBetaID) == nil {
		t.Fatal("the case insensitive q missed a probe")
	}

	page = rosterGet(t, pool, "/api/v1/messaging/people?q=probe+caller")
	if len(page.Items) != 1 || rosterFind(page, rosterCallerID) == nil {
		t.Fatalf("the middle-word q answered %+v, want only the caller", page.Items)
	}

	page = rosterGet(t, pool, "/api/v1/messaging/people?q=nobody-called-this")
	if len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("a word on nobody answered %d items with cursor %v, want an empty last page",
			len(page.Items), page.NextCursor)
	}
}

// The roster's order is display name then id, and the cursor walks it: the
// probe pair paged one at a time reads Alpha then Beta exactly once each,
// the second page answering a null next cursor.
func TestIntegrationRoster_OrderAndCursorWalk(t *testing.T) {
	pool := intPool(t)
	seedRosterProbes(t, pool)

	// The whole roster, one page: sorted by display name then id, compared
	// bytewise, the order the cursor itself compares in.
	page := rosterGet(t, pool, "/api/v1/messaging/people?limit=200")
	lastName := ""
	for i, it := range page.Items {
		name, _ := it["display_name"].(string)
		if i > 0 && name < lastName {
			t.Errorf("the roster is not in display name order at %d: %q after %q", i, name, lastName)
		}
		lastName = name
	}

	// The probe trio paged one at a time reads Alpha, Beta, Caller (display
	// name order), each exactly once, the walk ending on a null cursor.
	first := rosterGet(t, pool, "/api/v1/messaging/people?q=zzprobe&limit=1")
	if len(first.Items) != 1 || first.Items[0]["user_id"] != rosterAlphaID {
		t.Fatalf("the first probe page is %+v, want Alpha alone", first.Items)
	}
	if first.NextCursor == nil || *first.NextCursor == "" {
		t.Fatal("a full page of one with rows behind it must carry a next cursor")
	}
	second := rosterGet(t, pool, "/api/v1/messaging/people?q=zzprobe&limit=1&cursor="+*first.NextCursor)
	if len(second.Items) != 1 || second.Items[0]["user_id"] != rosterBetaID {
		t.Fatalf("the second probe page is %+v, want Beta alone", second.Items)
	}
	if second.NextCursor == nil {
		t.Fatal("the walk must still carry a cursor after Beta")
	}
	third := rosterGet(t, pool, "/api/v1/messaging/people?q=zzprobe&limit=1&cursor="+*second.NextCursor)
	if len(third.Items) != 1 || third.Items[0]["user_id"] != rosterCallerID {
		t.Fatalf("the third probe page is %+v, want the caller alone", third.Items)
	}
	if third.Items[0]["is_me"] != true {
		t.Error("the caller's row keeps is_me on a walked page")
	}
	if third.NextCursor != nil {
		t.Fatalf("the walk must end on the caller, got cursor %v", *third.NextCursor)
	}
}

// An unauthenticated request is the 401 family, never a leak of the roster.
func TestIntegrationRoster_Unauthenticated401(t *testing.T) {
	pool := intPool(t)
	seedRosterProbes(t, pool)
	mux, _ := newRosterRoutes(t, pool)
	rec := rosterDo(t, mux, "", "/api/v1/messaging/people")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d %s, want 401", rec.Code, rec.Body.String())
	}
}

// A role the dealer's configuration holds at none on house-messages is
// refused the roster, and a role at work passes: the route sits behind the
// same role gate the composition mounts, reading the same census annotation
// (house-messages at work). The gate's staff roles source is a fixed double
// naming the one held role.
type rosterRolesDouble struct{ roles []string }

func (d rosterRolesDouble) ResolveRoles(_ context.Context, _, _ string) ([]string, error) {
	return d.roles, nil
}

// rosterConfigWithNone builds the dealer configuration whose yard_team cell
// on house-messages is none, every other cell the decided default's.
func rosterConfigWithNone(t *testing.T) *roleconfig.Config {
	t.Helper()
	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("decode the default configuration: %v", err)
	}
	hit := false
	for i := range doc.Cells {
		if doc.Cells[i].Role == "yard_team" && doc.Cells[i].Surface == SurfaceHouseMessages {
			doc.Cells[i].Level = roleconfig.LevelNone
			hit = true
		}
	}
	if !hit {
		t.Fatal("the default configuration holds no yard_team cell on house-messages")
	}
	cfg, err := roleconfig.Validate(&doc)
	if err != nil {
		t.Fatalf("validate the modified configuration: %v", err)
	}
	return cfg
}

func TestIntegrationRoster_RoleGateRefusesNoneOnHouseMessages(t *testing.T) {
	pool := intPool(t)
	seedRosterProbes(t, pool)

	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{
		RoleID: "yard_team", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
	})
	h := NewHandler(NewService(NewRepository(pool)), nil).
		WithUserLinker(func(_ context.Context, _ string) (string, bool) { return rosterCallerID, true })

	gate, err := authz.NewRoleGate(sg.Harness.Gate, rosterRolesDouble{roles: []string{"yard_team"}}, rosterConfigWithNone(t))
	if err != nil {
		t.Fatalf("role gate: %v", err)
	}
	mux := http.NewServeMux()
	reg, err := authz.NewRoleGateRegistrar(mux, gate)
	if err != nil {
		t.Fatalf("role gate registrar: %v", err)
	}
	h.RegisterRoutes(reg, sg.Guards)
	bearer := sg.Bearer(t)

	// The role at none is the RBAC-403, the role gate's refusal.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/messaging/people", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a role at none on house-messages: %d %s, want 403", rec.Code, rec.Body.String())
	}

	// The same route admits a role whose cell reaches the level.
	gateOK, err := authz.NewRoleGate(sg.Harness.Gate, rosterRolesDouble{roles: []string{"inside_sales"}}, rosterConfigWithNone(t))
	if err != nil {
		t.Fatalf("role gate: %v", err)
	}
	muxOK := http.NewServeMux()
	regOK, err := authz.NewRoleGateRegistrar(muxOK, gateOK)
	if err != nil {
		t.Fatalf("role gate registrar: %v", err)
	}
	h.RegisterRoutes(regOK, sg.Guards)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/messaging/people", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	muxOK.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("a role at work on house-messages: %d %s, want 200", rec.Code, rec.Body.String())
	}
}
