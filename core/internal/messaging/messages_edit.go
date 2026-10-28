package messaging

// messages_edit.go is the edit and delete half of a person's own messages
// (House Messages change 7, slice S4, ruling 6): a person edits or deletes
// ONLY their own messages, ONLY inside the edit window the msg_policy row
// keeps as a setting; system messages (an EVENT, the approvals rail's
// APPROVAL card) are never editable and never deletable; a forced
// acknowledgement notice is never editable once sent (a correction is a new
// notice with its own acknowledgements); a deleted message is a tombstone,
// its body dropped, its row kept for the author and the time. The writes are
// REST only: the sync write fence holds, a sent message is not editable or
// deletable over sync (syncscope.go), and each write draws a fresh feed seq
// so the offline devices are told to refresh (0385's own rule for a write
// that changes a row the feed already served).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// - errors surfaced to the handler -

var (
	// ErrNotMessageAuthor: the pen is the author's own. A member may read
	// the room; they may not write into another person's words.
	ErrNotMessageAuthor = errors.New("messaging: only the message's author may edit or delete it")
	// ErrMessageImmutable: the message is the system's, an EVENT or an
	// APPROVAL card, or a forced acknowledgement notice: a conflict with
	// what the message IS, not a permission failure. The author wall cannot
	// even answer these (a system message has no author), so the kind wall
	// runs first and refuses in words for admins and members alike.
	ErrMessageImmutable = errors.New("messaging: system messages cannot be edited or deleted")
	// ErrMessageDeleted: the message is already a tombstone. An edit is a
	// conflict, never a revival; a second delete is the no-op it reads as.
	ErrMessageDeleted = errors.New("messaging: a deleted message cannot be edited")
	// ErrEditWindowClosed: the message is the author's own but the window
	// the setting keeps has closed, a conflict with the clock.
	ErrEditWindowClosed = errors.New("messaging: the edit window has closed")
)

// defaultEditWindowMinutes is the window an absent msg_policy row answers,
// the same number the migration's seed row carries.
const defaultEditWindowMinutes = 15

// messageFacts is what the walls read before any write: the message bound to
// its channel, its author (empty for a system message), its kind, whether it
// is a forced acknowledgement notice, and its clock state.
//
// Now is the DATABASE's clock at the read: the window is measured against the
// same clock that stamped created_at, so the two are one clock and not two
// (UX-109).
type messageFacts struct {
	ChannelID   string
	AuthorID    string
	Kind        string
	RequiresAck bool
	CreatedAt   time.Time
	DeletedAt   *time.Time
	Now         time.Time
}

// editWindowOpen reports whether a message created at createdAt is still
// inside a window of mins minutes at now. Zero minutes closes the window at
// once: nothing is editable after sending.
func editWindowOpen(createdAt time.Time, mins int, now time.Time) bool {
	return now.Sub(createdAt) <= time.Duration(mins)*time.Minute
}

// - the repository seam -

// EditWindowMinutes reads the dealer's edit window from the module's own
// policy row (0398), uncached: a dealer admin's change is live at the next
// write. An absent row answers the default the row seeds.
func (r *Repository) EditWindowMinutes(ctx context.Context) (int, error) {
	if r.pool == nil {
		return defaultEditWindowMinutes, nil
	}
	var mins int
	err := r.pool.QueryRow(ctx, `SELECT edit_window_minutes FROM msg_policy WHERE id = 1`).Scan(&mins)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return defaultEditWindowMinutes, nil
		}
		return 0, fmt.Errorf("edit window: %w", err)
	}
	return mins, nil
}

// messageFactsFor reads one message's walls' facts, bound to its channel: a
// message id that names no message in THIS channel is errNotFound, the same
// answer an absent id gives, so no existence leaks across channels.
//
// The read carries the DATABASE's clock (clock_timestamp), because the window
// is measured against the same clock that stamped created_at: the two must be
// one clock, or an app whose clock runs ahead of the database's closes the
// window on a message the database says is fresh (UX-109).
func (r *Repository) messageFactsFor(ctx context.Context, channelID, messageID string) (messageFacts, error) {
	if r.pool == nil {
		return messageFacts{}, errNoDB
	}
	var f messageFacts
	err := r.pool.QueryRow(ctx, `
		SELECT channel_id::text, COALESCE(author_id::text,''), kind, COALESCE(requires_ack, FALSE), created_at, deleted_at,
		       clock_timestamp()
		FROM msg_messages WHERE id = $1 AND channel_id = $2`, messageID, channelID).
		Scan(&f.ChannelID, &f.AuthorID, &f.Kind, &f.RequiresAck, &f.CreatedAt, &f.DeletedAt, &f.Now)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return messageFacts{}, errNotFound
		}
		return messageFacts{}, fmt.Errorf("message facts: %w", err)
	}
	return f, nil
}

// EditMessage writes the author's new body inside the window, re-checking
// every wall in the WHERE so a race between the facts read and the write
// cannot land an out-of-wall edit. The fresh seq tells the feed to refresh
// the row on every device (0385).
func (r *Repository) EditMessage(ctx context.Context, channelID, messageID, body string) (Message, error) {
	if r.pool == nil {
		return Message{}, errNoDB
	}
	var m Message
	var ref []byte
	err := r.pool.QueryRow(ctx, `
		UPDATE msg_messages SET body = $3, edited_at = now(),
		       seq = nextval(pg_get_serial_sequence('msg_messages', 'seq'))
		WHERE id = $2 AND channel_id = $1 AND deleted_at IS NULL
		  AND kind = 'TEXT' AND COALESCE(requires_ack, FALSE) = FALSE
		RETURNING id::text, channel_id::text, COALESCE(author_id::text,''), kind, body,
		          COALESCE(thread_root_id::text,''), ref, edited_at, created_at,
		          (SELECT COUNT(*) FROM msg_messages c
		           WHERE c.thread_root_id = msg_messages.id AND c.deleted_at IS NULL)`,
		channelID, messageID, body).
		Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.Kind, &m.Body, &m.ThreadRootID, &ref, &m.EditedAt, &m.CreatedAt, &m.ReplyCount)
	if err != nil {
		return Message{}, errNotFound
	}
	if len(ref) > 0 && string(ref) != "{}" {
		m.Ref = json.RawMessage(ref)
	}
	return m, nil
}

// DeleteMessage tombstones the author's own message inside the window: the
// body is dropped in the row itself (no read path can leak it), deleted_at
// is stamped once (the tombstone's own delete answers the row as it stands),
// and the fresh seq tells the feed to refresh (the messages scope serves the
// row as a tombstone).
func (r *Repository) DeleteMessage(ctx context.Context, channelID, messageID string) (Message, error) {
	if r.pool == nil {
		return Message{}, errNoDB
	}
	var m Message
	var ref []byte
	err := r.pool.QueryRow(ctx, `
		UPDATE msg_messages SET body = '', deleted_at = COALESCE(deleted_at, now()),
		       seq = nextval(pg_get_serial_sequence('msg_messages', 'seq'))
		WHERE id = $2 AND channel_id = $1
		  AND kind = 'TEXT' AND COALESCE(requires_ack, FALSE) = FALSE
		RETURNING id::text, channel_id::text, COALESCE(author_id::text,''), kind, body,
		          COALESCE(thread_root_id::text,''), ref, created_at, deleted_at,
		          (SELECT COUNT(*) FROM msg_messages c
		           WHERE c.thread_root_id = msg_messages.id AND c.deleted_at IS NULL)`,
		channelID, messageID).
		Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.Kind, &m.Body, &m.ThreadRootID, &ref, &m.CreatedAt, &m.DeletedAt, &m.ReplyCount)
	if err != nil {
		return Message{}, errNotFound
	}
	if len(ref) > 0 && string(ref) != "{}" {
		m.Ref = json.RawMessage(ref)
	}
	return m, nil
}

// - the service walls -

// EditMessage applies the caller's new body to their own message. The walls,
// in order: the channel wall (assertMember, the module's own scoping gate),
// the message's facts bound to the channel (404 for an id that names no
// message here), the kind wall (a system message is nobody's, an admin
// included), the forced acknowledgement wall (a notice is never editable
// once sent), the author wall (the pen is the author's own), the tombstone
// wall (an edit is not a revival), and the window (the setting's clock, read
// on the DATABASE's clock, the one that stamped the message: UX-109).
// The body runs the post's own trim and bound, so an edit cannot carry what
// a post cannot. An edit is not a new message: no realtime fan-out, the
// change reaches the room through the feed seq and the next poll.
func (s *Service) EditMessage(ctx context.Context, c Caller, channelID, messageID, body string) (Message, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return Message{}, err
	}
	facts, err := s.repo.messageFactsFor(ctx, channelID, messageID)
	if err != nil {
		return Message{}, err
	}
	if facts.Kind != MsgText {
		return Message{}, ErrMessageImmutable
	}
	if facts.RequiresAck {
		return Message{}, ErrMessageImmutable
	}
	if facts.AuthorID != c.UserID {
		return Message{}, ErrNotMessageAuthor
	}
	if facts.DeletedAt != nil {
		return Message{}, ErrMessageDeleted
	}
	mins, err := s.repo.EditWindowMinutes(ctx)
	if err != nil {
		return Message{}, err
	}
	if !editWindowOpen(facts.CreatedAt, mins, facts.Now) {
		return Message{}, ErrEditWindowClosed
	}
	body = trimMessageBody(body)
	if body == "" {
		return Message{}, ErrEmptyBody
	}
	return s.repo.EditMessage(ctx, channelID, messageID, body)
}

// DeleteMessage tombstones the caller's own message under the same walls,
// minus the tombstone one: deleting a tombstone again is the no-op it reads
// as, the row answered as it stands. The window is the same window the edit
// reads, on the same database's clock. No realtime fan-out, the same as an
// edit.
func (s *Service) DeleteMessage(ctx context.Context, c Caller, channelID, messageID string) (Message, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return Message{}, err
	}
	facts, err := s.repo.messageFactsFor(ctx, channelID, messageID)
	if err != nil {
		return Message{}, err
	}
	if facts.Kind != MsgText {
		return Message{}, ErrMessageImmutable
	}
	if facts.RequiresAck {
		return Message{}, ErrMessageImmutable
	}
	if facts.AuthorID != c.UserID {
		return Message{}, ErrNotMessageAuthor
	}
	mins, err := s.repo.EditWindowMinutes(ctx)
	if err != nil {
		return Message{}, err
	}
	if facts.DeletedAt == nil && !editWindowOpen(facts.CreatedAt, mins, facts.Now) {
		return Message{}, ErrEditWindowClosed
	}
	// A repeat delete of a tombstone changes nothing (the body is already dropped, deleted_at
	// already stamped), so it runs no write: the UPDATE would draw a fresh feed seq for a row
	// that did not change. The tombstone is answered as it stands.
	if facts.DeletedAt != nil {
		return s.repo.GetMessage(ctx, messageID)
	}
	return s.repo.DeleteMessage(ctx, channelID, messageID)
}
