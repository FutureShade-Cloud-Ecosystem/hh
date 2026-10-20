//go:build integration

// messages_edit_integration_test.go: the change 7 slice S4 walls against the
// migrated + seeded database, over the real pgx Repository. The legs prove
// what the fake doubles cannot: the SQL walls hold (author, kind, forced
// ack, window), the tombstone drops the body in the ROW (no read path can
// leak it), the reply count falls with a deleted reply, the edit window is
// the msg_policy row's and a change to it is live at the next write, and
// each write draws a fresh feed seq so the offline devices are told to
// refresh (the messages sync scope serves the tombstone and the reworded
// row). The sync write fence itself keeps holding: a sent message is not
// editable or deletable over sync, the REST writes are the only pen.

package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/jackc/pgx/v5/pgxpool"
)

// editWorld plants one fresh TEAM room by SQL (a hand planted room, as only
// a seeded or provisioned room can be once the founding is the admins' pen),
// seats the two probe callers, posts the author's message and hands back the
// pieces.
func editWorld(ctx context.Context, t *testing.T, pool *pgxpool.Pool) (roomID, msgID, authorID, otherID string) {
	t.Helper()

	roomID = plantEditRoom(ctx, t, pool)
	authorID = plantEditProbeUser(ctx, t, pool, "author")
	otherID = plantEditProbeUser(ctx, t, pool, "other")
	if _, err := pool.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'OWNER', 'EXPLICIT'), ($1, $3, 'MEMBER', 'EXPLICIT')`,
		roomID, authorID, otherID); err != nil {
		t.Fatalf("seat the probe callers: %v", err)
	}

	repo := NewRepository(pool)
	m, err := repo.InsertMessage(ctx, roomID, authorID, "Counted 12 on the pallet", "")
	if err != nil {
		t.Fatalf("post the probe message: %v", err)
	}
	return roomID, m.ID, authorID, otherID
}

// plantEditRoom inserts one fresh TEAM channel with a slug the reserved
// families do not touch, cleaned up with its rows.
func plantEditRoom(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	slug := fmt.Sprintf("edit-wall-probe-%d", time.Now().UnixNano())
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, tier)
		VALUES ('TEAM', $1, 'Edit wall probe', 'STANDARD')
		RETURNING id::text`, slug).Scan(&id); err != nil {
		t.Fatalf("plant the probe room: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = $1`, id)
	})
	return id
}

// plantEditProbeUser plants one ACTIVE staffer with no roles (a plain
// member), the shape the walls read: an author, a colleague, nobody more.
func plantEditProbeUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool, handle string) string {
	t.Helper()
	email := "edit-probe-" + handle + "-" + fmt.Sprintf("%d", time.Now().UnixNano()) + "@fixture.test"
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, status)
		VALUES ($1, $2, 'ACTIVE')
		RETURNING id::text`, email, "Edit probe "+handle).Scan(&id); err != nil {
		t.Fatalf("plant %s: %v", handle, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_members WHERE user_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

// seatAdmin seats the general manager in the probe room as an EXPLICIT
// member, so the legs that prove an admin holds no pen over another
// person's words (or over a system message) reach the wall that answers.
func seatAdmin(ctx context.Context, t *testing.T, pool *pgxpool.Pool, roomID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'MEMBER', 'EXPLICIT')
		ON CONFLICT (channel_id, user_id) DO NOTHING`, roomID, uAlexMorgan); err != nil {
		t.Fatalf("seat the admin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_members WHERE channel_id = $1 AND user_id = $2`, roomID, uAlexMorgan)
	})
}

// The happy edit: the author's own fresh message is reworded inside the
// window, edited_at is stamped, and the room serves the new body.
func TestIntegration_EditMessage_Author_Rewords_Own_Message(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	room, msg, author, other := editWorld(ctx, t, pool)

	m, err := svc.EditMessage(ctx, Caller{UserID: author}, room, msg, "Counted again, 13 on the pallet")
	if err != nil {
		t.Fatalf("author edit: %v", err)
	}
	if m.Body != "Counted again, 13 on the pallet" || m.EditedAt == nil {
		t.Fatalf("the edit did not land: body=%q edited_at=%v", m.Body, m.EditedAt)
	}
	page, err := svc.GetMessages(ctx, Caller{UserID: other}, room, "", 10)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if page.Messages[0].Body != "Counted again, 13 on the pallet" || page.Messages[0].EditedAt == nil {
		t.Fatalf("the room serves the reworded row: %+v", page.Messages[0])
	}
}

// The pen is the author's own: a member colleague edits nothing, and an
// admin holds no pen over another person's words either.
func TestIntegration_EditMessage_NonAuthor_Refused(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	room, msg, _, other := editWorld(ctx, t, pool)

	if err := repo.SyncMembership(ctx, uAlexMorgan); err != nil {
		t.Fatalf("sync alex: %v", err)
	}
	prev := publishDefaultRolesForTest(t)
	defer PublishRoleConfig(prev)
	// The admin is seated like anyone: the member wall runs first, and the
	// leg proves the AUTHOR wall is what answers a seated admin.
	seatAdmin(ctx, t, pool, room)

	if _, err := svc.EditMessage(ctx, Caller{UserID: other}, room, msg, "not mine"); !errors.Is(err, ErrNotMessageAuthor) {
		t.Fatalf("colleague edit must answer the author wall, got %v", err)
	}
	if _, err := svc.EditMessage(ctx, adminCaller(), room, msg, "not the admin's either"); !errors.Is(err, ErrNotMessageAuthor) {
		t.Fatalf("admin edit must answer the author wall, got %v", err)
	}
	var body string
	if err := pool.QueryRow(ctx, `SELECT body FROM msg_messages WHERE id = $1`, msg).Scan(&body); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if body != "Counted 12 on the pallet" {
		t.Fatalf("a refused edit must leave the row alone, got %q", body)
	}
}

// A system EVENT message and a forced acknowledgement notice are nobody's:
// the kind wall refuses in words for a colleague, an admin and the system's
// own authorless shape alike, and no write lands.
func TestIntegration_EditMessage_System_And_Forced_Ack_Immutable(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	room, _, _, other := editWorld(ctx, t, pool)
	seatAdmin(ctx, t, pool, room)

	ev, inserted, err := repo.InsertEventMessage(ctx, room, "edit-probe-event", "The truck is at the yard.", EventRef{Topic: "edit.probe"}, true)
	if err != nil || !inserted {
		t.Fatalf("post the forced ack event: inserted=%v err=%v", inserted, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_messages WHERE channel_id = $1 AND event_key LIKE 'edit-probe-event%'`, room)
	})

	for _, caller := range []Caller{{UserID: other}, adminCaller()} {
		if _, err := svc.EditMessage(ctx, caller, room, ev.ID, "rewrite the record"); !errors.Is(err, ErrMessageImmutable) {
			t.Fatalf("event edit by %s must answer the kind wall, got %v", caller.UserID, err)
		}
		if _, err := svc.DeleteMessage(ctx, caller, room, ev.ID); !errors.Is(err, ErrMessageImmutable) {
			t.Fatalf("event delete by %s must answer the kind wall, got %v", caller.UserID, err)
		}
	}
	var body string
	if err := pool.QueryRow(ctx, `SELECT body FROM msg_messages WHERE id = $1`, ev.ID).Scan(&body); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if body == "" {
		t.Fatalf("the forced ack notice must keep its words")
	}
}

// The window is the setting's: with the policy row narrowed to zero, the
// author's own fresh message is already outside the window; widened, the
// same message is editable again. The read is uncached, a change is live at
// the next write.
func TestIntegration_EditMessage_Window_Is_The_Setting(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	room, msg, author, _ := editWorld(ctx, t, pool)

	setWindow := func(mins int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE msg_policy SET edit_window_minutes = $1 WHERE id = 1`, mins); err != nil {
			t.Fatalf("set the window: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE msg_policy SET edit_window_minutes = $1 WHERE id = 1`, defaultEditWindowMinutes)
	})

	setWindow(0)
	if _, err := svc.EditMessage(ctx, Caller{UserID: author}, room, msg, "too late already"); !errors.Is(err, ErrEditWindowClosed) {
		t.Fatalf("a closed window must answer the window wall, got %v", err)
	}
	if _, err := svc.DeleteMessage(ctx, Caller{UserID: author}, room, msg); !errors.Is(err, ErrEditWindowClosed) {
		t.Fatalf("the window covers the delete too, got %v", err)
	}

	setWindow(60)
	if _, err := svc.EditMessage(ctx, Caller{UserID: author}, room, msg, "still mine to reword"); err != nil {
		t.Fatalf("a widened window admits the author: %v", err)
	}
}

// The tombstone: the delete drops the body IN THE ROW, stamps deleted_at,
// the room's reads serve the empty body with the stamp, a deleted reply
// falls out of the root's reply count, a second delete is the no-op it
// reads as, and an edit of the tombstone is refused.
func TestIntegration_DeleteMessage_Tombstones_The_Row(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	room, root, author, other := editWorld(ctx, t, pool)

	reply, err := repo.InsertMessage(ctx, room, author, "a reply", root)
	if err != nil {
		t.Fatalf("post the reply: %v", err)
	}
	before, err := repo.ListMessages(ctx, room, "", 10)
	if err != nil {
		t.Fatalf("read the room: %v", err)
	}
	if before.Messages[0].ReplyCount != 1 {
		t.Fatalf("setup: the root must hold one reply, got %d", before.Messages[0].ReplyCount)
	}

	m, err := svc.DeleteMessage(ctx, Caller{UserID: author}, room, root)
	if err != nil {
		t.Fatalf("author delete: %v", err)
	}
	if m.Body != "" || m.DeletedAt == nil {
		t.Fatalf("the answer must be the tombstone: body=%q deleted_at=%v", m.Body, m.DeletedAt)
	}
	var stored string
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT body, deleted_at FROM msg_messages WHERE id = $1`, root).Scan(&stored, &deletedAt); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if stored != "" || deletedAt == nil {
		t.Fatalf("the tombstone must drop the body in the row: body=%q deleted_at=%v", stored, deletedAt)
	}
	page, err := svc.GetMessages(ctx, Caller{UserID: other}, room, "", 10)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if page.Messages[0].Body != "" || page.Messages[0].DeletedAt == nil {
		t.Fatalf("the room serves the tombstone: %+v", page.Messages[0])
	}
	if page.Messages[0].ReplyCount != 1 {
		t.Fatalf("a deleted root keeps its alive reply's count, got %d", page.Messages[0].ReplyCount)
	}

	if _, err := svc.DeleteMessage(ctx, Caller{UserID: author}, room, root); err != nil {
		t.Fatalf("a second delete of a tombstone must be a no-op, got %v", err)
	}
	if _, err := svc.EditMessage(ctx, Caller{UserID: author}, room, root, "revive"); !errors.Is(err, ErrMessageDeleted) {
		t.Fatalf("editing a tombstone must answer the tombstone wall, got %v", err)
	}

	// A deleted REPLY falls out of the root's count.
	if _, err := svc.DeleteMessage(ctx, Caller{UserID: author}, room, reply.ID); err != nil {
		t.Fatalf("delete the reply: %v", err)
	}
	page, err = repo.ListMessages(ctx, room, "", 10)
	if err != nil {
		t.Fatalf("read the room: %v", err)
	}
	if page.Messages[0].ReplyCount != 0 {
		t.Fatalf("the deleted reply must fall out of the count, got %d", page.Messages[0].ReplyCount)
	}
}

// The export's tombstone: the admin HR/audit read renders the deleted
// message as "[deleted]" and never the dropped words.
func TestIntegration_DeleteMessage_Export_Shows_The_Tombstone(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	room, root, author, _ := editWorld(ctx, t, pool)

	if _, err := svc.DeleteMessage(ctx, Caller{UserID: author}, room, root); err != nil {
		t.Fatalf("author delete: %v", err)
	}
	rows, err := svc.ExportMessages(ctx, ExportFilter{ChannelID: room})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	seen := false
	for _, row := range rows {
		if row.ID == root {
			seen = true
			if row.Body != "[deleted]" {
				t.Fatalf("the export must tombstone the deleted message, got %q", row.Body)
			}
		}
	}
	if !seen {
		t.Fatalf("the export must still carry the deleted row (the tombstone is the record)")
	}
}

// The feed: each write draws a fresh seq, and the messages sync scope serves
// the deleted row as a tombstone, membership fenced. The write fence itself
// keeps holding: the sync's own write legs refuse an edit and a delete of a
// sent message.
func TestIntegration_EditDelete_Bump_The_Feed_Seq(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	room, msg, author, _ := editWorld(ctx, t, pool)

	var seqBefore int64
	if err := pool.QueryRow(ctx, `SELECT seq FROM msg_messages WHERE id = $1`, msg).Scan(&seqBefore); err != nil {
		t.Fatalf("read seq: %v", err)
	}

	if _, err := svc.EditMessage(ctx, Caller{UserID: author}, room, msg, "reworded"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	var seqEdited int64
	if err := pool.QueryRow(ctx, `SELECT seq FROM msg_messages WHERE id = $1`, msg).Scan(&seqEdited); err != nil {
		t.Fatalf("read seq: %v", err)
	}
	if seqEdited <= seqBefore {
		t.Fatalf("the edit must draw a fresh feed seq: before=%d after=%d", seqBefore, seqEdited)
	}

	if _, err := svc.DeleteMessage(ctx, Caller{UserID: author}, room, msg); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var seqDeleted int64
	if err := pool.QueryRow(ctx, `SELECT seq FROM msg_messages WHERE id = $1`, msg).Scan(&seqDeleted); err != nil {
		t.Fatalf("read seq: %v", err)
	}
	if seqDeleted <= seqEdited {
		t.Fatalf("the delete must draw a fresh feed seq: before=%d after=%d", seqEdited, seqDeleted)
	}

	// The author's device, holding the cursor at the edited row's seq, pulls
	// and the tombstone arrives.
	store := NewMessagesSyncStore(repo, func(_ context.Context, _ string) (string, bool) {
		return author, true
	})
	filter := sync.ScopeFilter{OrgID: "org-north", Actor: "edit-probe"}
	items, tombstones, err := store.Pull(ctx, filter, seqEdited-1, 100)
	if err != nil {
		t.Fatalf("messages pull: %v", err)
	}
	seen := false
	for _, tomb := range tombstones {
		if tomb.ID == msg {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the deleted message must arrive as a tombstone (items=%d)", len(items))
	}

	// The write fence: the sync's own legs still refuse.
	if _, err := store.Write(ctx, filter, msg, "k", map[string]any{"body": "over sync"}); err == nil {
		t.Fatalf("a sent message is not editable over sync, the fence must refuse")
	}
	if _, err := store.Delete(ctx, filter, msg, "k"); err == nil {
		t.Fatalf("a sent message is not deletable over sync, the fence must refuse")
	}
}

// A repeat DELETE of an already tombstoned message changes nothing (the body is already dropped
// and deleted_at already stamped), so it must not draw a fresh feed seq either: the second
// delete answers the tombstone as it stands and the row's seq is untouched.
func TestIntegration_DeleteMessage_RepeatDeleteIsIdempotentOnTheSeq(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	room, msg, author, _ := editWorld(ctx, t, pool)

	first, err := svc.DeleteMessage(ctx, Caller{UserID: author}, room, msg)
	if err != nil {
		t.Fatalf("first delete: %v", err)
	}
	var seq1 int64
	if err := pool.QueryRow(ctx, `SELECT seq FROM msg_messages WHERE id = $1`, msg).Scan(&seq1); err != nil {
		t.Fatalf("read seq after the first delete: %v", err)
	}

	second, err := svc.DeleteMessage(ctx, Caller{UserID: author}, room, msg)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	var seq2 int64
	if err := pool.QueryRow(ctx, `SELECT seq FROM msg_messages WHERE id = $1`, msg).Scan(&seq2); err != nil {
		t.Fatalf("read seq after the second delete: %v", err)
	}
	if seq2 != seq1 {
		t.Fatalf("a repeat delete must not bump the feed seq: first=%d second=%d", seq1, seq2)
	}
	if second.Body != "" || second.DeletedAt == nil || !second.DeletedAt.Equal(*first.DeletedAt) {
		t.Fatalf("the second delete must answer the tombstone as it stands: %+v", second)
	}
}
