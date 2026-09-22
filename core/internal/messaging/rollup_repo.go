package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// - DIB-449 persistence: the event-class config + the rollup read/write path -

// ListEventClasses returns the classification config, ordered so the admin surface reads in a stable
// order (CRITICAL first, the rows an admin most needs to see are the ones they may not change).
func (r *Repository) ListEventClasses(ctx context.Context) ([]EventClass, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT event_topic, class, locked, label, updated_at, COALESCE(updated_by::text,'')
		FROM msg_event_classes
		ORDER BY CASE class WHEN 'CRITICAL' THEN 0 WHEN 'NOTABLE' THEN 1 ELSE 2 END, event_topic`)
	if err != nil {
		return nil, fmt.Errorf("list event classes: %w", err)
	}
	defer rows.Close()
	out := []EventClass{}
	for rows.Next() {
		var c EventClass
		if err := rows.Scan(&c.Topic, &c.Class, &c.Locked, &c.Label, &c.UpdatedAt, &c.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan event class: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// EventClassFor returns the CONFIGURED class for a topic, or "" when the topic has no row. The empty
// string is not an error: effectiveClass turns it into the safe NOTABLE default (today's shipped
// per-event behaviour), so an event whose class nobody has decided is still SEEN.
func (r *Repository) EventClassFor(ctx context.Context, topic string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var class string
	err := r.pool.QueryRow(ctx, `SELECT class FROM msg_event_classes WHERE event_topic = $1`, topic).Scan(&class)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("event class for %s: %w", topic, err)
	}
	return class, nil
}

// SetEventClass writes a reclassification. The `AND locked = FALSE` predicate is a SECOND guard behind
// the service's refusal (ErrEventClassLocked): the service check is the one that produces the honest
// 409, and this one means no future caller, a script, a migration, a new endpoint, can quietly
// reclassify a locked safety row by skipping the service. Zero rows affected surfaces as errNotFound.
func (r *Repository) SetEventClass(ctx context.Context, topic, class, byUserID string) (EventClass, error) {
	if r.pool == nil {
		return EventClass{}, errNoDB
	}
	var by any
	if byUserID != "" {
		by = byUserID
	}
	var c EventClass
	err := r.pool.QueryRow(ctx, `
		UPDATE msg_event_classes
		SET class = $2, updated_at = now(), updated_by = $3
		WHERE event_topic = $1 AND locked = FALSE
		RETURNING event_topic, class, locked, label, updated_at, COALESCE(updated_by::text,'')`,
		topic, class, by).Scan(&c.Topic, &c.Class, &c.Locked, &c.Label, &c.UpdatedAt, &c.UpdatedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EventClass{}, errNotFound // unknown topic, or locked (the service already refused)
		}
		return EventClass{}, fmt.Errorf("set event class: %w", err)
	}
	return c, nil
}

// - The rollup -

// FindEventMessage resolves an EVENT message by its (channel, event_key) identity, the lookup that
// answers "does this day already have a rollup line?". errNotFound when absent.
func (r *Repository) FindEventMessage(ctx context.Context, channelID, eventKey string) (Message, error) {
	if r.pool == nil {
		return Message{}, errNoDB
	}
	var m Message
	var ref []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, channel_id::text, kind, body, ref, requires_ack, created_at
		FROM msg_messages
		WHERE channel_id = $1 AND event_key = $2`, channelID, eventKey).
		Scan(&m.ID, &m.ChannelID, &m.Kind, &m.Body, &ref, &m.RequiresAck, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, errNotFound
		}
		return Message{}, fmt.Errorf("find event message: %w", err)
	}
	if len(ref) > 0 && string(ref) != "{}" {
		m.Ref = json.RawMessage(ref)
	}
	return m, nil
}

// InsertEventReply writes a system EVENT message as a REPLY under an existing rollup root, idempotent
// on (channel_id, event_key) exactly like every other event insert in this module, so an at-least-once
// outbox redelivery of the same payment adds nothing.
//
// The root is validated the same way InsertMessage validates a TEXT reply's root: it must exist, live in
// THIS channel, and itself be top-level (one-level threads, MSG-01). Re-checking here rather than
// trusting the caller keeps the invariant with the write, where a future caller cannot skip it.
func (r *Repository) InsertEventReply(ctx context.Context, channelID, rootID, eventKey, body string, ref EventRef) (Message, bool, error) {
	if r.pool == nil {
		return Message{}, false, errNoDB
	}
	if rootID == "" {
		return Message{}, false, fmt.Errorf("%w: rollup reply needs a thread root", errNotFound)
	}
	var ok bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM msg_messages WHERE id = $1 AND channel_id = $2 AND thread_root_id IS NULL)`,
		rootID, channelID).Scan(&ok); err != nil {
		return Message{}, false, fmt.Errorf("validate rollup root: %w", err)
	}
	if !ok {
		return Message{}, false, fmt.Errorf("%w: rollup root not in channel", errNotFound)
	}
	refBytes, err := json.Marshal(ref)
	if err != nil {
		return Message{}, false, fmt.Errorf("marshal ref: %w", err)
	}
	var m Message
	err = r.pool.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, ref, event_key, thread_root_id)
		VALUES ($1, NULL, 'EVENT', $2, $3, $4, $5)
		ON CONFLICT (channel_id, event_key) WHERE event_key IS NOT NULL DO NOTHING
		RETURNING id::text, channel_id::text, kind, body, COALESCE(thread_root_id::text,''), created_at`,
		channelID, body, refBytes, eventKey, rootID).
		Scan(&m.ID, &m.ChannelID, &m.Kind, &m.Body, &m.ThreadRootID, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, false, nil // already recorded under this day (idempotent redelivery)
		}
		return Message{}, false, fmt.Errorf("insert rollup reply: %w", err)
	}
	m.Ref = json.RawMessage(refBytes)
	return m, true, nil
}

// RollupAggregate folds the LIVE replies under a rollup root into (count, total cents). This is the
// day's truth: the rendered line is derived from it, never incremented toward it, so an at-least-once
// redelivery cannot inflate a money total an AR clerk reconciles against.
//
// deleted_at IS NULL is load-bearing rather than tidy: a payment message removed by the retention or
// moderation path must leave the day's total, or the line would keep asserting money that is no longer
// itemised beneath it.
func (r *Repository) RollupAggregate(ctx context.Context, rootID string) (int, int64, error) {
	if r.pool == nil {
		return 0, 0, errNoDB
	}
	var count int
	var cents int64
	err := r.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(COALESCE((ref->>'amount_cents')::bigint, 0)), 0)
		FROM msg_messages
		WHERE thread_root_id = $1 AND kind = 'EVENT' AND deleted_at IS NULL`, rootID).Scan(&count, &cents)
	if err != nil {
		return 0, 0, fmt.Errorf("rollup aggregate: %w", err)
	}
	return count, cents, nil
}

// UpdateEventBody rewrites an EVENT message's body + ref IN PLACE, the LAST-WRITE-WINS update that
// makes a day ONE message instead of a log of successive totals.
//
// It deliberately does NOT touch created_at or edited_at. created_at is the day's position in the
// stream and must not jump to the top on every payment (that would restore exactly the "keeps
// resurfacing" noise the rollup removes). edited_at means "a human edited this"; a system recompute is
// not an edit, and stamping it would put "(edited)" on a message nobody edited.
//
// kind = 'EVENT' AND author_id IS NULL scopes the rewrite to system posts: this path can never be
// pointed at a person's message.
func (r *Repository) UpdateEventBody(ctx context.Context, messageID, body string, ref EventRef) (Message, error) {
	if r.pool == nil {
		return Message{}, errNoDB
	}
	refBytes, err := json.Marshal(ref)
	if err != nil {
		return Message{}, fmt.Errorf("marshal ref: %w", err)
	}
	var m Message
	err = r.pool.QueryRow(ctx, `
		UPDATE msg_messages
		SET body = $2, ref = $3
		WHERE id = $1 AND kind = 'EVENT' AND author_id IS NULL
		RETURNING id::text, channel_id::text, kind, body, COALESCE(thread_root_id::text,''),
		          (SELECT COUNT(*) FROM msg_messages c WHERE c.thread_root_id = msg_messages.id AND c.deleted_at IS NULL),
		          created_at`,
		messageID, body, refBytes).
		Scan(&m.ID, &m.ChannelID, &m.Kind, &m.Body, &m.ThreadRootID, &m.ReplyCount, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, errNotFound
		}
		return Message{}, fmt.Errorf("update event body: %w", err)
	}
	m.Ref = json.RawMessage(refBytes)
	return m, nil
}
