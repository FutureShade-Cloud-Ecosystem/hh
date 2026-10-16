//go:build integration

package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type s5SearchBody struct {
	Messages []map[string]any `json:"messages"`
	People   []map[string]any `json:"people"`
	Channels []map[string]any `json:"channels"`
}

func s5Search(t *testing.T, pool *pgxpool.Pool, callerID, target string) s5SearchBody {
	t.Helper()
	mux, bearer := newS5Routes(t, pool, callerID)
	rec := s5Do(t, mux, bearer, http.MethodGet, target, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s, want 200", target, rec.Code, rec.Body.String())
	}
	var body s5SearchBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search: %v (%s)", err, rec.Body.String())
	}
	return body
}

func s5SearchFind(items []map[string]any, key string, value any) map[string]any {
	for _, item := range items {
		if item[key] == value {
			return item
		}
	}
	return nil
}

func TestIntegrationSearch_MessagesStopAtMembership(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	repo := NewRepository(pool)
	memberRoom := s5PlantChannel(t, pool, KindTopic, "S5 member search", "", s5CallerID)
	foreignRoom := s5PlantChannel(t, pool, KindTopic, "S5 foreign search", "", s5OutsiderID)
	memberMessage := s5InsertMessage(t, repo, memberRoom, s5CallerID, "s5membershipneedle member")
	s5InsertMessage(t, repo, foreignRoom, s5OutsiderID, "s5membershipneedle foreign")

	body := s5Search(t, pool, s5CallerID, "/api/v1/messaging/search?q=s5membershipneedle")
	if len(body.Messages) != 1 || body.Messages[0]["message_id"] != memberMessage.ID {
		t.Fatalf("membership search leaked or missed messages: %+v", body.Messages)
	}
	if body.Messages[0]["channel_id"] != memberRoom {
		t.Fatalf("search returned the wrong room: %+v", body.Messages[0])
	}
}

func TestIntegrationSearch_MessagesHotWindowAndDeleted(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	repo := NewRepository(pool)
	room := s5PlantChannel(t, pool, KindTopic, "S5 search retention", "", s5CallerID)
	live := s5InsertMessage(t, repo, room, s5CallerID, "s5windowprobe live")
	old := s5InsertMessage(t, repo, room, s5CallerID, "s5windowprobe old")
	deleted := s5InsertMessage(t, repo, room, s5CallerID, "s5windowprobe deleted")
	if _, err := pool.Exec(context.Background(), `
		UPDATE msg_messages SET created_at = NOW() - INTERVAL '366 days' WHERE id = $1`, old.ID); err != nil {
		t.Fatalf("age search message: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE msg_messages SET deleted_at = NOW() WHERE id = $1`, deleted.ID); err != nil {
		t.Fatalf("soft-delete search message: %v", err)
	}

	body := s5Search(t, pool, s5CallerID, "/api/v1/messaging/search?q=s5windowprobe")
	if len(body.Messages) != 1 || body.Messages[0]["message_id"] != live.ID {
		t.Fatalf("hot window or deletion filter returned the wrong rows: %+v", body.Messages)
	}
}

const (
	s5DirectoryActiveID      = "d7000000-0000-4000-9000-000000000f11"
	s5DirectorySuspendedID   = "d7000000-0000-4000-9000-000000000f12"
	s5DirectoryDeactivatedID = "d7000000-0000-4000-9000-000000000f13"
	s5DirectorySuperID       = "d7000000-0000-4000-9000-000000000f14"
	s5DirectoryForeignID     = "d7000000-0000-4000-9000-000000000f15"
	s5DirectoryForeignOrg    = "eeeeeeee-0000-4000-8000-0000000000f5"
	s5RoleYardTeam           = "e7000000-0000-4000-9000-000000000012"
	s5RoleSuperAdmin         = "e7000000-0000-4000-9000-000000000014"
)

func seedS5DirectoryUsers(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, email, display_name, status) VALUES
		  ($1, 's5.directory.active@test.invalid', 'S5Directory Active', 'ACTIVE'),
		  ($2, 's5.directory.suspended@test.invalid', 'S5Directory Suspended', 'SUSPENDED'),
		  ($3, 's5.directory.deactivated@test.invalid', 'S5Directory Deactivated', 'DEACTIVATED'),
		  ($4, 's5.directory.super@test.invalid', 'S5Directory Platform', 'ACTIVE'),
		  ($5, 's5.directory.foreign@test.invalid', 'S5Directory Foreign', 'ACTIVE')
		ON CONFLICT (id) DO UPDATE SET
		  email = EXCLUDED.email, display_name = EXCLUDED.display_name,
		  status = EXCLUDED.status, deleted_at = NULL`,
		s5DirectoryActiveID, s5DirectorySuspendedID, s5DirectoryDeactivatedID,
		s5DirectorySuperID, s5DirectoryForeignID)
	if err != nil {
		t.Fatalf("seed directory users: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2), ($3, $4)
		ON CONFLICT DO NOTHING`, s5DirectoryActiveID, s5RoleYardTeam,
		s5DirectorySuperID, s5RoleSuperAdmin); err != nil {
		t.Fatalf("seed directory roles: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO user_branch_grants (user_id, branch_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, s5DirectoryActiveID, branchTrenton); err != nil {
		t.Fatalf("seed directory branch: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE users SET org_id = $2::uuid WHERE id = $1`,
		s5DirectoryForeignID, s5DirectoryForeignOrg); err != nil {
		t.Fatalf("anchor foreign directory user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, []string{
			s5DirectoryActiveID, s5DirectorySuspendedID, s5DirectoryDeactivatedID,
			s5DirectorySuperID, s5DirectoryForeignID,
		})
	})
}

func TestIntegrationSearch_PeopleDirectoryWallsAndFields(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	seedS5DirectoryUsers(t, pool)

	body := s5Search(t, pool, s5CallerID, "/api/v1/messaging/search?q=s5directory&limit=50")
	if len(body.People) != 1 {
		t.Fatalf("directory returned inactive, platform, or foreign users: %+v", body.People)
	}
	person := body.People[0]
	if person["user_id"] != s5DirectoryActiveID {
		t.Fatalf("directory returned the wrong person: %+v", person)
	}
	wantKeys := []string{"user_id", "display_name", "role_code", "role_label", "branch_label"}
	if len(person) != len(wantKeys) {
		t.Errorf("person keys %v, want exactly %v", person, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := person[key]; !ok {
			t.Errorf("person is missing %q: %+v", key, person)
		}
	}
	for _, banned := range []string{"email", "phone"} {
		if _, ok := person[banned]; ok {
			t.Errorf("person exposes %q: %+v", banned, person)
		}
	}
	if person["role_code"] != "YARD_TEAM" || person["role_label"] != "Yard Team" {
		t.Errorf("role projection is wrong: %+v", person)
	}
	if label, _ := person["branch_label"].(string); !strings.Contains(label, "Trenton") {
		t.Errorf("branch_label = %q, want the branch name", label)
	}

	body = s5Search(t, pool, s5CallerID, "/api/v1/messaging/search?q=Yard+Team&limit=50")
	if s5SearchFind(body.People, "user_id", s5DirectoryActiveID) == nil {
		t.Error("role name search did not find the active directory user")
	}
	body = s5Search(t, pool, s5CallerID, "/api/v1/messaging/search?q=Trenton&limit=50")
	if s5SearchFind(body.People, "user_id", s5DirectoryActiveID) == nil {
		t.Error("branch name search did not find the active directory user")
	}
}

func TestIntegrationSearch_ChannelsExcludeForeignPrivateRooms(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	publicID := s5PlantChannel(t, pool, KindTopic, "S5Channel Public", "S5Channel topic", s5OutsiderID)
	s5PlantChannel(t, pool, KindDM, "S5Channel Foreign DM", "", s5OutsiderID)
	s5PlantChannel(t, pool, KindApproval, "S5Channel Foreign Approval", "", s5OutsiderID)
	memberDM := s5PlantChannel(t, pool, KindDM, "S5Channel Member DM", "", s5OutsiderID, s5CallerID)

	body := s5Search(t, pool, s5CallerID, "/api/v1/messaging/search?q=s5channel&limit=50")
	if len(body.Channels) != 2 {
		t.Fatalf("channel search returned foreign private rooms: %+v", body.Channels)
	}
	public := s5SearchFind(body.Channels, "id", publicID)
	if public == nil || public["is_member"] != false {
		t.Errorf("public channel projection is wrong: %+v", public)
	}
	member := s5SearchFind(body.Channels, "id", memberDM)
	if member == nil || member["is_member"] != true {
		t.Errorf("member room projection is wrong: %+v", member)
	}
}

func TestIntegrationSearch_EscapesPatternWildcards(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	repo := NewRepository(pool)
	room := s5PlantChannel(t, pool, KindTopic, "S5 escaping", "", s5CallerID)
	percent := s5InsertMessage(t, repo, room, s5CallerID, "s5 100% probe")
	s5InsertMessage(t, repo, room, s5CallerID, "s5 100X probe")
	underscore := s5InsertMessage(t, repo, room, s5CallerID, "s5 under_score probe")
	s5InsertMessage(t, repo, room, s5CallerID, "s5 underXscore probe")
	backslash := s5InsertMessage(t, repo, room, s5CallerID, `s5 back\slash probe`)
	s5InsertMessage(t, repo, room, s5CallerID, "s5 backslash probe")

	for _, tc := range []struct {
		target string
		wantID string
	}{
		{"/api/v1/messaging/search?q=100%25", percent.ID},
		{"/api/v1/messaging/search?q=under%5Fscore", underscore.ID},
		{"/api/v1/messaging/search?q=back%5Cslash", backslash.ID},
	} {
		body := s5Search(t, pool, s5CallerID, tc.target)
		if len(body.Messages) != 1 || body.Messages[0]["message_id"] != tc.wantID {
			t.Errorf("GET %s returned %+v, want only %s", tc.target, body.Messages, tc.wantID)
		}
	}
}

func TestIntegrationSearch_LimitDefaultsAndCaps(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	repo := NewRepository(pool)
	room := s5PlantChannel(t, pool, KindTopic, "S5 limits", "", s5CallerID)
	for i := 0; i < 55; i++ {
		s5InsertMessage(t, repo, room, s5CallerID, fmt.Sprintf("s5limitprobe %02d", i))
	}

	body := s5Search(t, pool, s5CallerID, "/api/v1/messaging/search?q=s5limitprobe")
	if len(body.Messages) != 20 {
		t.Errorf("default limit returned %d messages, want 20", len(body.Messages))
	}
	body = s5Search(t, pool, s5CallerID, "/api/v1/messaging/search?q=s5limitprobe&limit=500")
	if len(body.Messages) != 50 {
		t.Errorf("capped limit returned %d messages, want 50", len(body.Messages))
	}
}
