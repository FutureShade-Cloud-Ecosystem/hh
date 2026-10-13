//go:build integration

// money_rooms_integration_test.go: who may hold a seat in the money rooms
// (#payments, #payments-<branch>, #finance) on a migrated and seeded
// database. The rooms were seeded open to every active staff member, so the
// sales floor and the yard managers read the day's payment lines. The core
// decides who may hold them: the roles the decided matrix gives the money
// desk at work level or above. One planted user per role, derived
// membership synced exactly as a sign in syncs it, then the member listing.

package messaging

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

// moneyRoomSlugs lists the system money rooms the migrated database carries.
const moneyRoomsSQL = `
	SELECT slug::text FROM msg_channels
	WHERE auto_scope IS NOT NULL AND (slug = 'finance' OR slug = 'payments' OR slug LIKE 'payments-%')`

// financeRolesFromTheMatrix is the decided set: every ERP role code whose
// canonical role holds the money desk at work level or above.
func financeRolesFromTheMatrix(t *testing.T, codes []string) map[string]bool {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatalf("roleconfig default: %v", err)
	}
	out := map[string]bool{}
	for _, code := range codes {
		canonical, ok := identity.CanonicalForERPCode(code)
		if !ok {
			continue
		}
		holds, err := cfg.Holds(canonical, "money-desk", roleconfig.LevelWork)
		if err != nil && code != "SUPER_ADMIN" {
			t.Fatalf("matrix %s: %v", canonical, err)
		}
		out[code] = holds && err == nil
	}
	return out
}

func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

func TestIntegration_MoneyRooms_OnlyTheFinanceRolesAreSeated(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	rows, err := pool.Query(ctx, `SELECT code::text FROM roles ORDER BY code`)
	if err != nil {
		t.Fatalf("roles: %v", err)
	}
	var codes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan role: %v", err)
		}
		codes = append(codes, c)
	}
	rows.Close()
	// The 13 personas plus SUPER_ADMIN, the platform role the matrix does not
	// carry: it is not a finance role either, so it is tested as a non finance one.
	if len(codes) != 14 {
		t.Fatalf("the migrated roles table holds %d roles, want the 13 personas and SUPER_ADMIN", len(codes))
	}
	finance := financeRolesFromTheMatrix(t, codes)
	if got := len(finance); got != 14 {
		t.Fatalf("the matrix answered for %d of 14 roles", got)
	}
	if n := countTrue(finance); n != 3 {
		t.Fatalf("the matrix names %d finance roles, want 3 (payables and receivables, financial controller, general manager)", n)
	}

	var money []string
	mrows, err := pool.Query(ctx, moneyRoomsSQL)
	if err != nil {
		t.Fatalf("money rooms: %v", err)
	}
	for mrows.Next() {
		var s string
		if err := mrows.Scan(&s); err != nil {
			t.Fatalf("scan room: %v", err)
		}
		money = append(money, s)
	}
	mrows.Close()
	if len(money) < 4 {
		t.Fatalf("expected #payments, #finance and the branch payments rooms, found %v", money)
	}

	users := map[string]string{}
	t.Cleanup(func() {
		for _, id := range users {
			_, _ = pool.Exec(ctx, `DELETE FROM msg_members WHERE user_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM user_branch_grants WHERE user_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		}
	})
	for i, code := range codes {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, display_name, status)
			VALUES ($1, 'Money room probe', 'ACTIVE') RETURNING id::text`,
			fmt.Sprintf("money-room-%d@fixture.test", i)).Scan(&id); err != nil {
			t.Fatalf("plant %s: %v", code, err)
		}
		users[code] = id
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) SELECT $1, r.id FROM roles r WHERE r.code = $2`, id, code); err != nil {
			t.Fatalf("role %s: %v", code, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_branch_grants (user_id, branch_id) VALUES ($1, $2)`, id, branchTrenton); err != nil {
			t.Fatalf("grant %s: %v", code, err)
		}
		if err := repo.SyncMembership(ctx, id); err != nil {
			t.Fatalf("sync %s: %v", code, err)
		}
	}

	for _, code := range codes {
		held := map[string]bool{}
		hrows, err := pool.Query(ctx, `
			SELECT ch.slug::text FROM msg_members m JOIN msg_channels ch ON ch.id = m.channel_id
			WHERE m.user_id = $1`, users[code])
		if err != nil {
			t.Fatalf("members %s: %v", code, err)
		}
		for hrows.Next() {
			var s string
			if err := hrows.Scan(&s); err != nil {
				t.Fatalf("scan %s: %v", code, err)
			}
			held[s] = true
		}
		hrows.Close()

		var heldMoney []string
		for _, s := range money {
			if held[s] {
				heldMoney = append(heldMoney, s)
			}
		}
		sort.Strings(heldMoney)
		if finance[code] {
			// A finance role holds every money room it reaches: the
			// global ones and the branch rooms (these roles are GLOBAL).
			if len(heldMoney) != len(money) {
				t.Errorf("%s is a finance role and holds %v of %v", code, heldMoney, money)
			}
		} else if len(heldMoney) != 0 {
			t.Errorf("%s is not a finance role and sits in %s", code, strings.Join(heldMoney, ", "))
		}
		// The fence takes nothing else away: every persona keeps #office.
		if !held["office"] {
			t.Errorf("%s lost #office", code)
		}
	}
}

// The explicit seat obeys the same wall: an admin cannot hand a money room
// to a role the core keeps out of it, but can seat one it admits.
func TestIntegration_MoneyRooms_AddMemberRefusesANonFinanceRole(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)

	gm := Caller{UserID: uAlexMorgan, RoleCode: "GENERAL_MANAGER", Scope: "GLOBAL"}
	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync the admin: %v", err)
	}
	var room string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM msg_channels WHERE slug = 'payments-trenton'`).Scan(&room); err != nil {
		t.Fatalf("the payments room: %v", err)
	}
	// The seeded Trenton inside sales persona is not a finance role.
	target := uatInsideSalesID(ctx, t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_members WHERE channel_id = $1 AND user_id = $2`, room, target)
	})
	_, err := svc.AddMember(ctx, gm, room, target)
	if err == nil {
		t.Fatalf("a non finance role was seated in a money room")
	}
	if ok, _ := repo.IsMember(ctx, room, target); ok {
		t.Fatalf("the refused target holds a seat")
	}
	if !errors.Is(err, ErrTargetNotInRoom) {
		t.Fatalf("refusal = %v, want ErrTargetNotInRoom", err)
	}
}
