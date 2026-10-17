package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// - DIB-217 persistence: priority events, supersession, ack arithmetic -

// InsertEventMessageOpts is InsertEventMessage with the 0138 columns. The shipped 5-arg
// InsertEventMessage stays (it delegates here) so no existing caller changes: the portal bridge and
// the task/quote notifiers wired in main.go keep their signatures.
func (r *Repository) InsertEventMessageOpts(ctx context.Context, channelID, eventKey, body string, ref EventRef, opts EventOpts) (Message, bool, error) {
	if r.pool == nil {
		return Message{}, false, errNoDB
	}
	opts = opts.normalize()
	refBytes, err := json.Marshal(ref)
	if err != nil {
		return Message{}, false, fmt.Errorf("marshal ref: %w", err)
	}
	stateKey := any(nil)
	if opts.StateKey != "" {
		stateKey = opts.StateKey
	}
	var m Message
	err = r.pool.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, ref, event_key, requires_ack, priority, state_key)
		VALUES ($1, NULL, 'EVENT', $2, $3, $4, $5, $6, $7)
		ON CONFLICT (channel_id, event_key) WHERE event_key IS NOT NULL DO NOTHING
		RETURNING id::text, channel_id::text, kind, body, requires_ack, created_at`,
		channelID, body, refBytes, eventKey, opts.RequiresAck, opts.Priority, stateKey).
		Scan(&m.ID, &m.ChannelID, &m.Kind, &m.Body, &m.RequiresAck, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, false, nil // already posted (idempotent redelivery)
		}
		return Message{}, false, fmt.Errorf("insert event message: %w", err)
	}
	m.Ref = json.RawMessage(refBytes)
	m.Priority = opts.Priority
	m.StateKey = opts.StateKey
	return m, true, nil
}

// SupersedeOlderForState is the LAST-WRITE-WINS sweep (the DIB-217 ruling). Given the message that
// just landed, it auto-resolves every OLDER, LIVE, UN-ACKED forced-ack message asserting a fact about
// the SAME record.
//
// Every clause is load-bearing:
//
//	state_key = $1       , the record, not the topic: "order released" resolves "order blocked".
//	id <> $2             , a message never supersedes itself (redelivery is the event_key no-op).
//	created_at < (new)   , ordering is by CREATION, not arrival, so an out-of-order redelivery of an
//	                        older fact cannot resurrect itself over the newer one.
//	requires_ack         , only bubbles are auto-resolved; an ordinary informational post is history
//	                        and history is not rewritten.
//	superseded_at IS NULL, idempotent: a second sweep re-resolves nothing.
//	NOT EXISTS (msg_acks), an ALREADY-ACKED message keeps its ack. Someone read it and said so; that
//	                        record is not ours to overwrite.
func (r *Repository) SupersedeOlderForState(ctx context.Context, stateKey, newMessageID string) ([]Supersession, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if stateKey == "" || newMessageID == "" {
		return nil, nil // not a state assertion, supersedes nothing
	}
	rows, err := r.pool.Query(ctx, `
		UPDATE msg_messages old
		SET superseded_by = $2, superseded_at = now(),
		    seq = nextval(pg_get_serial_sequence('msg_messages', 'seq'))
		FROM msg_messages new
		WHERE new.id = $2
		  AND old.state_key = $1
		  AND old.id <> new.id
		  AND old.created_at < new.created_at
		  AND old.requires_ack = TRUE
		  AND old.superseded_at IS NULL
		  AND old.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM msg_acks a WHERE a.message_id = old.id)
		RETURNING old.id::text, old.superseded_at`, stateKey, newMessageID)
	if err != nil {
		return nil, fmt.Errorf("supersede for state: %w", err)
	}
	defer rows.Close()
	out := []Supersession{}
	for rows.Next() {
		s := Supersession{SupersededBy: newMessageID, StateKey: stateKey}
		if err := rows.Scan(&s.MessageID, &s.At); err != nil {
			return nil, fmt.Errorf("scan supersession: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AckProgressFor returns the ack arithmetic for one forced-ack message (DIB-275). Required is the
// channel's CURRENT member count; Acked is the DISTINCT number of ack rows, the msg_acks
// (message_id, user_id) primary key is what makes a re-ack an UPDATE rather than a second row, so
// idempotent re-acking is guaranteed by the schema, not by a Go-side de-dup that could be forgotten.
//
// The message is bound to channelID exactly like AckMessage, so a member of one channel cannot read
// another channel's ack roll-up.
func (r *Repository) AckProgressFor(ctx context.Context, channelID, messageID string) (AckProgress, error) {
	if r.pool == nil {
		return AckProgress{}, errNoDB
	}
	var required, acked int
	err := r.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM msg_members mem WHERE mem.channel_id = m.channel_id),
		  (SELECT count(*) FROM msg_acks a WHERE a.message_id = m.id)
		FROM msg_messages m
		WHERE m.id = $1 AND m.channel_id = $2`, messageID, channelID).Scan(&required, &acked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AckProgress{}, errNotFound
		}
		return AckProgress{}, fmt.Errorf("ack progress: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.message_id::text, a.user_id::text, a.method, a.acked_at
		FROM msg_acks a WHERE a.message_id = $1 ORDER BY a.acked_at`, messageID)
	if err != nil {
		return AckProgress{}, fmt.Errorf("ack roll: %w", err)
	}
	defer rows.Close()
	acks := []Ack{}
	for rows.Next() {
		var a Ack
		if err := rows.Scan(&a.MessageID, &a.UserID, &a.Method, &a.AckedAt); err != nil {
			return AckProgress{}, fmt.Errorf("scan ack: %w", err)
		}
		acks = append(acks, a)
	}
	if err := rows.Err(); err != nil {
		return AckProgress{}, err
	}
	return computeAckProgress(messageID, required, acked, acks), nil
}
