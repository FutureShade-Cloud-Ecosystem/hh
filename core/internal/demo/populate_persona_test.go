//go:build demo

package demo

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// The sign in personas (state note section 5): after the pipeline each
// enterable persona has a users row linked to the namespaced subject, its
// legacy role rows, one branch grant and one staff grant per role keyed by
// the subject; the contractor personas have nothing; no Console style
// demo+<role> account exists; and a second run writes nothing.
func TestPopulate_BuildsTheSignInPersonas(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedDB(t, "personas")
	bin := buildDemoCore(t)
	first, second := seedAndPopulate(t, bin, dsn)
	if !strings.Contains(first, "sign in personas 14 created") {
		t.Errorf("the first run should report 14 personas created, it printed:\n%s", first)
	}
	if !strings.Contains(second, "wrote nothing") {
		t.Errorf("the second run should report it wrote nothing, it printed:\n%s", second)
	}
	before := snapshotWorld(t, ctx, pool, worldTables)
	runStep(t, bin, []string{"demo-populate", "-now", seedClock.Format("2006-01-02T15:04:05Z07:00")}, subEnv(t, populateEnv(dsn)))
	assertWorldUnchanged(t, before, snapshotWorld(t, ctx, pool, worldTables))

	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE lower(slug::text) = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	resolver, err := identity.NewStaffAuthorityResolver(pool)
	if err != nil {
		t.Fatal(err)
	}

	enterable := 0
	for _, p := range flows.Personas() {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1::uuid OR lower(email::text) = $2 OR platform_user_id = $3`,
			p.UserID, strings.ToLower(p.Email), p.Subject()).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if p.Contractor {
			if n != 0 {
				t.Errorf("contractor persona %s has %d rows before the Pro cycle", p.ID, n)
			}
			continue
		}
		enterable++
		var status, display, email string
		var platform *string
		if err := pool.QueryRow(ctx, `SELECT status, display_name, email::text, platform_user_id FROM users WHERE id = $1::uuid`, p.UserID).
			Scan(&status, &display, &email, &platform); err != nil {
			t.Fatalf("persona %s has no users row: %v", p.ID, err)
		}
		if status != "ACTIVE" || display != p.DisplayName() || email != p.Email || platform == nil || *platform != p.Subject() {
			t.Errorf("persona %s row: %s %q %q %v", p.ID, status, display, email, platform)
		}
		if strings.HasSuffix(email, "@futurebuild.ai") || strings.Contains(email, "demo+") {
			t.Errorf("persona %s carries a Console style address %q", p.ID, email)
		}
		// The authority resolves: the persona's own role wins and every
		// role it holds is on the grant list.
		a, ok, err := resolver.ResolveStaff(ctx, p.Subject(), orgID)
		if err != nil || !ok {
			t.Fatalf("persona %s resolves no authority: %v %v", p.ID, ok, err)
		}
		if a.RoleID != p.Role {
			t.Errorf("persona %s resolves role %s, want %s", p.ID, a.RoleID, p.Role)
		}
		roles, err := resolver.ResolveRoles(ctx, p.Subject(), orgID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(sorted(roles), ",") != strings.Join(sorted(p.StaffRoles()), ",") {
			t.Errorf("persona %s holds %v, want %v", p.ID, roles, p.StaffRoles())
		}
		if a.Scope == "branch" {
			want := personaBranch(p)
			if len(a.Branches) != 1 || a.Branches[0] != want {
				t.Errorf("persona %s reaches %v, want exactly %s", p.ID, a.Branches, want)
			}
		}
	}
	if enterable != 14 {
		t.Fatalf("enterable personas %d", enterable)
	}

	// The yard manager is one person with two roles.
	roles, _ := resolver.ResolveRoles(ctx, "persona:yard-manager", orgID)
	if len(roles) != 2 {
		t.Errorf("the yard manager holds %v, want yard_manager and driver", roles)
	}

	// Persona grants and no others: every persona-keyed grant belongs to the list.
	var stray int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM staff_role_grant WHERE platform_user_id LIKE 'persona:%' AND deleted_at IS NULL`).Scan(&stray); err != nil {
		t.Fatal(err)
	}
	if stray != 15 {
		t.Errorf("%d persona grants, want 15 (thirteen staff roles, the dealer administrator and the yard manager's driver grant)", stray)
	}
	// No Console style account was created.
	var consoleStyle int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email::text) LIKE 'demo+%' OR lower(email::text) LIKE '%@futurebuild.ai'`).Scan(&consoleStyle); err != nil {
		t.Fatal(err)
	}
	if consoleStyle != 0 {
		t.Errorf("%d Console style accounts", consoleStyle)
	}
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
