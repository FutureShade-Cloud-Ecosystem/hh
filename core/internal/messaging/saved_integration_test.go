//go:build integration

package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	s5CallerID   = "d7000000-0000-4000-9000-000000000f01"
	s5PeerID     = "d7000000-0000-4000-9000-000000000f02"
	s5OutsiderID = "d7000000-0000-4000-9000-000000000f03"
)

func seedS5Users(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, email, display_name, status) VALUES
		  ($1, 's5.caller@test.invalid', 'S5 Caller', 'ACTIVE'),
		  ($2, 's5.peer@test.invalid', 'S5 Peer', 'ACTIVE'),
		  ($3, 's5.outsider@test.invalid', 'S5 Outsider', 'ACTIVE')
		ON CONFLICT (id) DO UPDATE SET
		  email = EXCLUDED.email, display_name = EXCLUDED.display_name,
		  status = EXCLUDED.status, org_id = NULL, deleted_at = NULL`,
		s5CallerID, s5PeerID, s5OutsiderID)
	if err != nil {
		t.Fatalf("seed S5 users: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`,
			[]string{s5CallerID, s5PeerID, s5OutsiderID})
	})
}

func newS5Routes(t *testing.T, pool *pgxpool.Pool, linkedUser string) (*http.ServeMux, string) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{
		RoleID: "inside_sales", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
	})
	h := NewHandler(NewService(NewRepository(pool)), nil).
		WithUserLinker(func(_ context.Context, _ string) (string, bool) { return linkedUser, true })
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	return mux, sg.Bearer(t)
}

func s5Do(t *testing.T, mux *http.ServeMux, bearer, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func s5PlantChannel(t *testing.T, pool *pgxpool.Pool, kind, name, topic string, memberIDs ...string) string {
	t.Helper()
	ctx := context.Background()
	slug := fmt.Sprintf("s5-%d", time.Now().UnixNano())
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, topic)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text`, kind, slug, name, topic).Scan(&id); err != nil {
		t.Fatalf("plant %s channel: %v", kind, err)
	}
	for i, userID := range memberIDs {
		role := "MEMBER"
		if i == 0 {
			role = "OWNER"
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO msg_members (channel_id, user_id, role, source)
			VALUES ($1, $2, $3, 'EXPLICIT')`, id, userID, role); err != nil {
			t.Fatalf("seat channel member: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM msg_channels WHERE id = $1`, id)
	})
	return id
}

func s5InsertMessage(t *testing.T, repo *Repository, channelID, authorID, body string) Message {
	t.Helper()
	m, err := repo.InsertMessage(context.Background(), channelID, authorID, body, "")
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	return m
}

func s5SavedItems(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("saved list: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode saved list: %v (%s)", err, rec.Body.String())
	}
	return body.Items
}

func s5Save(t *testing.T, mux *http.ServeMux, bearer, messageID string) *httptest.ResponseRecorder {
	t.Helper()
	return s5Do(t, mux, bearer, http.MethodPost, "/api/v1/messaging/saved",
		[]byte(`{"message_id":"`+messageID+`"}`))
}

func TestIntegrationSaved_SaveRequiresMembership(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	repo := NewRepository(pool)
	ownRoom := s5PlantChannel(t, pool, KindTopic, "S5 member room", "", s5CallerID)
	foreignRoom := s5PlantChannel(t, pool, KindTopic, "S5 foreign room", "", s5OutsiderID)
	own := s5InsertMessage(t, repo, ownRoom, s5CallerID, "S5 save member")
	foreign := s5InsertMessage(t, repo, foreignRoom, s5OutsiderID, "S5 save foreign")
	mux, bearer := newS5Routes(t, pool, s5CallerID)

	if rec := s5Save(t, mux, bearer, foreign.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("non-member save: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := s5Save(t, mux, bearer, "e5000000-0000-4000-9000-0000000000ff"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing message save: %d %s, want 404", rec.Code, rec.Body.String())
	}
	first := s5Save(t, mux, bearer, own.ID)
	if first.Code != http.StatusOK {
		t.Fatalf("member save: %d %s, want 200", first.Code, first.Body.String())
	}
	second := s5Save(t, mux, bearer, own.ID)
	if second.Code != http.StatusOK {
		t.Fatalf("replayed save: %d %s, want 200", second.Code, second.Body.String())
	}
	var one, two map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &one); err != nil {
		t.Fatalf("decode first save: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &two); err != nil {
		t.Fatalf("decode replayed save: %v", err)
	}
	if one["saved_at"] != two["saved_at"] {
		t.Fatalf("replayed save moved saved_at: %v then %v", one["saved_at"], two["saved_at"])
	}
	for _, key := range []string{"message_id", "channel_id", "channel_slug", "channel_name", "author_id", "author_name", "kind", "body", "created_at", "saved_at"} {
		if _, ok := one[key]; !ok {
			t.Errorf("saved item is missing %q: %+v", key, one)
		}
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM msg_saved_messages WHERE user_id = $1 AND message_id = $2`,
		s5CallerID, own.ID).Scan(&rows); err != nil {
		t.Fatalf("count saved rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("idempotent save wrote %d rows, want 1", rows)
	}
}

func TestIntegrationSaved_ListIsPrivateAndUnsaveRemoves(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	repo := NewRepository(pool)
	room := s5PlantChannel(t, pool, KindTopic, "S5 privacy room", "", s5CallerID, s5PeerID)
	message := s5InsertMessage(t, repo, room, s5CallerID, "S5 private saved item")
	callerMux, callerBearer := newS5Routes(t, pool, s5CallerID)
	peerMux, peerBearer := newS5Routes(t, pool, s5PeerID)

	if rec := s5Save(t, callerMux, callerBearer, message.ID); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s, want 200", rec.Code, rec.Body.String())
	}
	if items := s5SavedItems(t, s5Do(t, callerMux, callerBearer, http.MethodGet, "/api/v1/messaging/saved", nil)); len(items) != 1 {
		t.Fatalf("caller saved list has %d items, want 1", len(items))
	}
	if items := s5SavedItems(t, s5Do(t, peerMux, peerBearer, http.MethodGet, "/api/v1/messaging/saved", nil)); len(items) != 0 {
		t.Fatalf("peer sees %d of the caller's saved items, want 0", len(items))
	}
	for attempt := 0; attempt < 2; attempt++ {
		rec := s5Do(t, callerMux, callerBearer, http.MethodDelete, "/api/v1/messaging/saved/"+message.ID, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("unsave attempt %d: %d %s, want 204", attempt+1, rec.Code, rec.Body.String())
		}
	}
	if items := s5SavedItems(t, s5Do(t, callerMux, callerBearer, http.MethodGet, "/api/v1/messaging/saved", nil)); len(items) != 0 {
		t.Fatalf("unsave left %d items, want 0", len(items))
	}
}

func TestIntegrationSaved_HotWindowAndTombstone(t *testing.T) {
	pool := intPool(t)
	seedS5Users(t, pool)
	repo := NewRepository(pool)
	room := s5PlantChannel(t, pool, KindTopic, "S5 retention room", "", s5CallerID)
	oldMessage := s5InsertMessage(t, repo, room, s5CallerID, "S5 old saved item")
	deletedMessage := s5InsertMessage(t, repo, room, s5CallerID, "S5 deleted saved item")
	mux, bearer := newS5Routes(t, pool, s5CallerID)
	for _, id := range []string{oldMessage.ID, deletedMessage.ID} {
		if rec := s5Save(t, mux, bearer, id); rec.Code != http.StatusOK {
			t.Fatalf("save %s: %d %s, want 200", id, rec.Code, rec.Body.String())
		}
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE msg_messages SET created_at = NOW() - INTERVAL '366 days' WHERE id = $1`, oldMessage.ID); err != nil {
		t.Fatalf("age saved message: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE msg_messages SET deleted_at = NOW() WHERE id = $1`, deletedMessage.ID); err != nil {
		t.Fatalf("soft-delete saved message: %v", err)
	}
	items := s5SavedItems(t, s5Do(t, mux, bearer, http.MethodGet, "/api/v1/messaging/saved", nil))
	if len(items) != 1 {
		t.Fatalf("saved list has %d items, want only the tombstone: %+v", len(items), items)
	}
	if items[0]["message_id"] != deletedMessage.ID || items[0]["body"] != "" {
		t.Fatalf("deleted saved message is not a tombstone: %+v", items[0])
	}
}
