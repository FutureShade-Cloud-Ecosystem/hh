package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errNoDB      = errors.New("messaging: database not configured")
	errNotFound  = errors.New("messaging: not found")
	errForbidden = errors.New("messaging: not a channel member")
	// errBadCursor marks a malformed pagination cursor (undecodable timestamp/id), a CLIENT input
	// fault, not a server fault. decodeCursor wraps its parse failures in this sentinel so the
	// handler can map it to HTTP 400 instead of the catch-all 500. Exported to the handler as
	// ErrBadCursor (service.go).
	errBadCursor = errors.New("messaging: malformed pagination cursor")
)

// Repository owns the msg_* tables (pgx). It tolerates a nil pool (scaffold/offline mode): every
// method returns errNoDB rather than panicking, so the server still boots without a database.
// Zero cross-module table access, it reads users/locations/user_branch_grants (identity's tables)
// ONLY through the derived-membership materialization, which is the identity-is-SoT seam (FR-1);
// all writes stay within the msg_* namespace.
type Repository struct {
	pool *pgxpool.Pool // nil in scaffold/tests
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// - Derived membership (FR-1) -
// SyncMembership materializes the FR-1 role/branch rule into msg_members for one user. It runs on
// every list-my-channels resolve so a role/branch change takes effect immediately (identity is the
// SoT; membership is derived, never hand-maintained). It:
//   - INSERTs a DERIVED member row for each auto-rule channel the user now qualifies for
//     (GLOBAL channels: any active staff; BRANCH channels: user granted that branch; auto_role
//     narrows to a role code), preserving last_read_at on conflict (never resets the read cursor).
//   - DELETEs DERIVED rows the user no longer qualifies for (a transfer/role change self-heals).
//
// EXPLICIT rows (DM participants, manual TOPIC joins) are never touched.
//
// GATED (do NOT build here): config-driven cross-yard visibility. Today a BRANCH channel is visible
// only to users granted that branch (or GLOBAL-scoped users), the existing GLOBAL-scope default. A
// config that widens visibility across yards (e.g. a region seeing sibling yards) is NOT built.
// TODO(decision: cross-yard visibility config): once the visibility model (per-region? per-role? a
// config table?) is settled, extend the `qualifies` predicate below to honour it, deliberately left
// as the GLOBAL-scope default until then.
func (r *Repository) SyncMembership(ctx context.Context, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Only ACTIVE staff get derived channels; a suspended/deactivated user is dropped from them.
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, userID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return fmt.Errorf("resolve user: %w", err)
	}

	// Resolve the caller's identity facts once: their scope (GLOBAL reaches every branch, mirroring
	// StaffContext.CanAccessBranch) and their role codes. Passing these as parameters lets the SAME
	// qualification predicate drive both the insert and the prune (defined once, so they can never
	// drift out of sync). A user with no assigned role has scope '' and no role rows.
	var scope string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(ro.scope), '')
		FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		WHERE ur.user_id = $1`, userID).Scan(&scope); err != nil {
		return fmt.Errorf("resolve scope: %w", err)
	}
	isGlobal := scope == "GLOBAL"

	// qualifies is the derived-membership predicate for one channel `ch` and this user. Bind order is
	// FIXED so insert + prune can share ONE definition: $1 = userID, $2 = isGlobal (bool).
	//   - GLOBAL auto_scope: any ACTIVE staff;
	//   - BRANCH auto_scope: the user is GLOBAL-scoped (reaches every branch) OR holds a grant for
	//     ch.branch_id (mirrors StaffContext.CanAccessBranch);
	//   - auto_role narrows to a role code ('*'/NULL = any role).
	const qualifies = `
		ch.auto_scope IS NOT NULL
		AND ch.is_archived = FALSE
		AND (
		      ch.auto_scope = 'GLOBAL'
		   OR (ch.auto_scope = 'BRANCH' AND ($2 OR EXISTS (
		         SELECT 1 FROM user_branch_grants g
		         WHERE g.user_id = $1 AND g.branch_id = ch.branch_id)))
		    )
		AND (
		      ch.auto_role IS NULL OR ch.auto_role = '*'
		   OR EXISTS (
		         SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		         WHERE ur.user_id = $1 AND ro.code = ch.auto_role)
		    )`

	// Insert the derived rows the user qualifies for. A non-ACTIVE user qualifies for nothing (the
	// Go-side status guard) so it is skipped entirely.
	if status == "ACTIVE" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO msg_members (channel_id, user_id, role, source)
			SELECT ch.id, $1, 'MEMBER', 'DERIVED'
			FROM msg_channels ch
			WHERE `+qualifies+`
			ON CONFLICT (channel_id, user_id) DO NOTHING`, userID, isGlobal); err != nil {
			return fmt.Errorf("insert derived members: %w", err)
		}
	}

	// Delete DERIVED rows the user no longer qualifies for (role/branch change, deactivation, or
	// channel archival). EXPLICIT rows are untouched (source filter). An ACTIVE user is re-tested
	// against `qualifies`; a non-ACTIVE user qualifies for NOTHING, so ALL their derived rows are
	// pruned (the branch below avoids binding the now-unused $2).
	if status == "ACTIVE" {
		if _, err := tx.Exec(ctx, `
			DELETE FROM msg_members m
			WHERE m.user_id = $1
			  AND m.source = 'DERIVED'
			  AND NOT EXISTS (
			        SELECT 1 FROM msg_channels ch
			        WHERE ch.id = m.channel_id
			          AND `+qualifies+`
			      )`, userID, isGlobal); err != nil {
			return fmt.Errorf("prune derived members: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx,
			`DELETE FROM msg_members WHERE user_id = $1 AND source = 'DERIVED'`, userID); err != nil {
			return fmt.Errorf("prune derived members (inactive): %w", err)
		}
	}

	return tx.Commit(ctx)
}

// - Channels -
// ListChannelsForUser returns the caller's channels (the channels they are a member of, derived or
// explicit) with the per-caller unread projection joined from msg_unread_summary. Ordered by most
// recent activity so the sidebar reads like Slack.
func (r *Repository) ListChannelsForUser(ctx context.Context, userID string) ([]Channel, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ch.id::text, ch.kind, COALESCE(ch.branch_id::text,''), COALESCE(ch.slug::text,''),
		       ch.name, ch.topic, ch.tier, ch.is_archived, ch.created_at,
		       COALESCE(us.unread_count,0), COALESCE(us.mentioned,FALSE), us.latest_at
		FROM msg_members mem
		JOIN msg_channels ch ON ch.id = mem.channel_id
		LEFT JOIN msg_unread_effective us ON us.channel_id = ch.id AND us.user_id = mem.user_id
		WHERE mem.user_id = $1 AND ch.is_archived = FALSE
		ORDER BY COALESCE(us.latest_at, ch.created_at) DESC, ch.name ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var c Channel
		var latest *time.Time
		if err := rows.Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier,
			&c.IsArchived, &c.CreatedAt, &c.UnreadCount, &c.Mentioned, &latest); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		c.LatestAt = latest
		out = append(out, c)
	}
	return out, rows.Err()
}

// IsMember reports whether the user is a member of the channel, the channel-scoping authz check
// every message read/write goes through (a non-member can never see or post to a channel).
func (r *Repository) IsMember(ctx context.Context, channelID, userID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM msg_members WHERE channel_id = $1 AND user_id = $2)`,
		channelID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is-member: %w", err)
	}
	return exists, nil
}

// channelExists checks a channel is present (and not archived), used to distinguish 404 from 403.
func (r *Repository) channelExists(ctx context.Context, channelID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM msg_channels WHERE id = $1 AND is_archived = FALSE)`,
		channelID).Scan(&exists)
	return exists, err
}

// ChannelTier returns a channel's notification tier (STANDARD/ESCALATION/FIREHOSE, 0069). It is the
// server-side input to shouldNotify at the per-recipient notification-decision point. An unknown
// channel is errNotFound (never a silent default).
func (r *Repository) ChannelTier(ctx context.Context, channelID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var tier string
	err := r.pool.QueryRow(ctx, `SELECT tier FROM msg_channels WHERE id = $1`, channelID).Scan(&tier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", fmt.Errorf("channel tier: %w", err)
	}
	return tier, nil
}

// - Feature A: notification preferences (0069) -
// GetNotificationPrefs returns the caller's prefs for a channel, or the defaults when no row exists
// (an unset user simply notifies normally, never an error). The service authorizes membership first.
// DIB-449: an absent row now falls back to the CHANNEL's default_mode (0152), not to a hardcoded ALL.
// This is not cosmetic. #payments is quiet for everyone by default; if this read kept answering "ALL"
// for a user with no row, the notification control would show "All messages" selected on a channel
// that is in fact silent, the UI telling a confident lie about the user's own settings, which is the
// same failure DIB-269 refused a mute for. Inherited marks the answer as the channel's default rather
// than the user's choice, so the surface can say which it is.
func (r *Repository) GetNotificationPrefs(ctx context.Context, channelID, userID string) (NotificationPrefs, error) {
	if r.pool == nil {
		return NotificationPrefs{}, errNoDB
	}
	p := DefaultNotificationPrefs(channelID, userID)
	var explicit bool
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(p.desktop_enabled, TRUE),
		       COALESCE(p.escalation_override, TRUE),
		       -- ESCALATION forces ALL here as well as in msg_unread_effective: two independent guards,
		       -- so no read path can report an escalation channel as mutable (DIB-269).
		       CASE WHEN ch.tier = 'ESCALATION' THEN 'ALL'
		            ELSE COALESCE(p.mode, ch.default_mode, 'ALL') END,
		       COALESCE(p.updated_at, ch.created_at),
		       (p.user_id IS NOT NULL)
		FROM msg_channels ch
		LEFT JOIN msg_notification_prefs p ON p.channel_id = ch.id AND p.user_id = $2
		WHERE ch.id = $1`, channelID, userID).
		Scan(&p.DesktopEnabled, &p.EscalationOverride, &p.Mode, &p.UpdatedAt, &explicit)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultNotificationPrefs(channelID, userID), nil // unknown channel: the flat defaults
		}
		return NotificationPrefs{}, fmt.Errorf("get notification prefs: %w", err)
	}
	// muted is mode's boolean shadow and is derived, never read independently, applyMode is the ONE
	// place the pair is written, so the badge pipeline's field and shouldNotify's field cannot drift.
	p = applyMode(p, p.Mode)
	p.Inherited = !explicit
	return p, nil
}

// UpsertNotificationPrefs writes the caller's prefs for a channel (insert or update), bumping
// updated_at, and returns the persisted row. The service pre-merges omitted fields, so every field is
// authoritative here.
func (r *Repository) UpsertNotificationPrefs(ctx context.Context, p NotificationPrefs) (NotificationPrefs, error) {
	if r.pool == nil {
		return NotificationPrefs{}, errNoDB
	}
	out := NotificationPrefs{ChannelID: p.ChannelID, UserID: p.UserID}
	// mode + muted are written from the SAME normalized pair (applyMode) so the badge pipeline's field
	// and shouldNotify's field can never disagree (0137).
	p = applyMode(p, ModeOf(p))
	err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_notification_prefs (channel_id, user_id, desktop_enabled, escalation_override, muted, mode, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (channel_id, user_id) DO UPDATE
		  SET desktop_enabled = EXCLUDED.desktop_enabled,
		      escalation_override = EXCLUDED.escalation_override,
		      muted = EXCLUDED.muted,
		      mode = EXCLUDED.mode,
		      updated_at = now(),
		      seq = nextval(pg_get_serial_sequence('msg_notification_prefs', 'seq'))
		RETURNING desktop_enabled, escalation_override, muted, mode, updated_at`,
		p.ChannelID, p.UserID, p.DesktopEnabled, p.EscalationOverride, p.Muted, p.Mode).
		Scan(&out.DesktopEnabled, &out.EscalationOverride, &out.Muted, &out.Mode, &out.UpdatedAt)
	if err != nil {
		return NotificationPrefs{}, fmt.Errorf("upsert notification prefs: %w", err)
	}
	return out, nil
}

// - Messages -
// ListMessages returns a channel's top-level messages newest-first, cursor-paginated. The cursor is
// a FULL keyset, "<created_at RFC3339Nano>|<id>", over the (created_at DESC, id DESC) index, so
// pagination is stable under concurrent inserts AND correct when messages share a created_at (system
// EVENT posts fanned from one outbox tick, or now()-defaulted seed rows, collide at microsecond
// resolution). The next-page predicate is a row comparison `(created_at, id) < (cursor_ts, cursor_id)`
// so a same-timestamp group is never split across a page boundary and no row is silently skipped.
// limit is clamped by the caller. Only top-level messages (thread_root_id IS NULL) are returned;
// replies are fetched per-thread. reply_count is computed per row so the stream can render "N replies".
func (r *Repository) ListMessages(ctx context.Context, channelID, cursor string, limit int) (MessagePage, error) {
	if r.pool == nil {
		return MessagePage{}, errNoDB
	}
	args := []any{channelID, limit + 1}
	cursorClause := ""
	if cursor != "" {
		ts, id, err := decodeCursor(cursor)
		if err != nil {
			return MessagePage{}, fmt.Errorf("bad cursor: %w", err)
		}
		// Tuple keyset: strictly "older than" the cursor row in the (created_at DESC, id DESC) order.
		cursorClause = " AND (m.created_at, m.id) < ($3::timestamptz, $4::uuid)"
		args = append(args, ts, id)
	}
	q := `
		SELECT m.id::text, m.channel_id::text, COALESCE(m.author_id::text,''), COALESCE(u.display_name,''),
		       m.kind, m.body, m.ref, COALESCE(m.thread_root_id::text,''),
		       (SELECT COUNT(*) FROM msg_messages c WHERE c.thread_root_id = m.id AND c.deleted_at IS NULL),
		       m.edited_at, m.deleted_at, m.created_at
		FROM msg_messages m
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.channel_id = $1 AND m.thread_root_id IS NULL` + cursorClause + `
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $2`
	return r.scanMessagePage(ctx, q, args, limit)
}

// cursorSep joins the created_at + id halves of a page cursor. '|' cannot appear in an RFC3339Nano
// timestamp or a UUID, so a single split is unambiguous.
const cursorSep = "|"

// encodeCursor builds the opaque page cursor from the last row's (created_at, id).
func encodeCursor(m Message) string {
	return m.CreatedAt.Format(time.RFC3339Nano) + cursorSep + m.ID
}

// decodeCursor parses "<created_at>|<id>" back into its parts. A legacy timestamp-only cursor (no
// separator) is still accepted, id defaults to the max UUID so the boundary row itself is excluded,
// preserving forward-compat for any cursor minted before this fix.
func decodeCursor(cursor string) (time.Time, string, error) {
	tsStr, idStr, hasID := strings.Cut(cursor, cursorSep)
	ts, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		// A client-supplied garbage cursor is a 400, not a 500: wrap in errBadCursor so ListMessages
		// propagates it and the handler maps it to HTTP 400 (%w keeps the parse detail for the log).
		return time.Time{}, "", fmt.Errorf("%w: %v", errBadCursor, err)
	}
	if !hasID || idStr == "" {
		// Legacy cursor: fall back to the tuple's upper bound so `(created_at, id) < (ts, MAX)` keeps
		// every row strictly older by timestamp, matching the old created_at-only semantics.
		idStr = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	} else if _, err := uuid.Parse(idStr); err != nil {
		// The id half is bound straight into `$4::uuid`. A non-empty, non-UUID id would otherwise reach
		// Postgres and fail the cast, surfacing as a wrapped "list messages" error → HTTP 500. Validate
		// it here (like the timestamp half) and wrap in errBadCursor so it maps to a 400 client fault.
		return time.Time{}, "", fmt.Errorf("%w: %v", errBadCursor, err)
	}
	return ts, idStr, nil
}

// ListThread returns the replies under a root message, oldest-first (a thread reads top-to-bottom).
// It is bound to channelID: replies are only returned when the root itself lives in the caller's
// authorized channel (m.channel_id = $2). This closes the cross-channel thread-read IDOR, the
// caller is authorized against {id}, so the read must be constrained to that same channel and never
// keyed on the client-supplied {rootID} alone.
func (r *Repository) ListThread(ctx context.Context, channelID, rootID string) ([]Message, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.id::text, m.channel_id::text, COALESCE(m.author_id::text,''), COALESCE(u.display_name,''),
		       m.kind, m.body, m.ref, COALESCE(m.thread_root_id::text,''), 0,
		       m.edited_at, m.deleted_at, m.created_at
		FROM msg_messages m
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.thread_root_id = $1 AND m.channel_id = $2
		ORDER BY m.created_at ASC, m.id ASC`, rootID, channelID)
	if err != nil {
		return nil, fmt.Errorf("list thread: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

// scanMessagePage runs the given keyset query (which selects limit+1 rows) and splits the extra row
// into a NextCursor. Rows come back newest-first.
func (r *Repository) scanMessagePage(ctx context.Context, q string, args []any, limit int) (MessagePage, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return MessagePage{}, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return MessagePage{}, err
	}
	page := MessagePage{}
	if len(msgs) > limit {
		// The extra row proves there is an older page; the last row we KEEP is the cursor. Encoding
		// both its created_at AND id makes the next page resume exactly after it, even when several
		// rows share a created_at (no split group, no skipped row).
		page.NextCursor = encodeCursor(msgs[limit-1])
		msgs = msgs[:limit]
	}
	page.Messages = msgs
	return page, nil
}

func scanMessages(rows pgx.Rows) ([]Message, error) {
	var out []Message
	for rows.Next() {
		var m Message
		var ref []byte
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.AuthorName, &m.Kind, &m.Body,
			&ref, &m.ThreadRootID, &m.ReplyCount, &m.EditedAt, &m.DeletedAt, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		if len(ref) > 0 && string(ref) != "{}" {
			m.Ref = json.RawMessage(ref)
		}
		// Soft-deleted messages are tombstoned in place (audit doctrine), never leak the body.
		if m.DeletedAt != nil {
			m.Body = ""
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// InsertMessage writes a TEXT message (or reply) authored by userID. threadRootID is "" for a
// top-level post. It validates the root belongs to the same channel (a reply can't jump channels).
func (r *Repository) InsertMessage(ctx context.Context, channelID, userID, body, threadRootID string) (Message, error) {
	if r.pool == nil {
		return Message{}, errNoDB
	}
	var rootArg any
	if threadRootID != "" {
		// The root must exist, be in THIS channel, and itself be a top-level message (one-level
		// threads in P1). There is NO re-rooting: the predicate requires thread_root_id IS NULL, so
		// passing a REPLY's id as the root fails the EXISTS check and is rejected as not-found
		// (errNotFound → HTTP 404), a reply can never itself become a thread root. This same check
		// also rejects a cross-channel root as a hard integrity guard.
		var ok bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM msg_messages WHERE id = $1 AND channel_id = $2 AND thread_root_id IS NULL)`,
			threadRootID, channelID).Scan(&ok); err != nil {
			return Message{}, fmt.Errorf("validate thread root: %w", err)
		}
		if !ok {
			return Message{}, fmt.Errorf("%w: thread root not in channel", errNotFound)
		}
		rootArg = threadRootID
	}
	var m Message
	err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, thread_root_id)
		VALUES ($1, $2, 'TEXT', $3, $4)
		RETURNING id::text, channel_id::text, author_id::text, kind, body, COALESCE(thread_root_id::text,''), created_at`,
		channelID, userID, body, rootArg).Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.Kind, &m.Body, &m.ThreadRootID, &m.CreatedAt)
	if err != nil {
		return Message{}, fmt.Errorf("insert message: %w", err)
	}
	// Resolve the author's display name for the immediate echo (the stream shows it).
	_ = r.pool.QueryRow(ctx, `SELECT COALESCE(display_name,'') FROM users WHERE id = $1`, userID).Scan(&m.AuthorName)
	return m, nil
}

// MarkRead advances the caller's read cursor for a channel to `at` (typically now), which zeroes the
// channel's unread count via the msg_unread_summary view. It never moves the cursor backwards.
func (r *Repository) MarkRead(ctx context.Context, channelID, userID string, at time.Time) error {
	if r.pool == nil {
		return errNoDB
	}
	ct, err := r.pool.Exec(ctx, `
		UPDATE msg_members SET last_read_at = GREATEST(last_read_at, $3)
		WHERE channel_id = $1 AND user_id = $2`, channelID, userID, at)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return errForbidden
	}
	return nil
}

// UnreadSummary returns the caller's per-channel unread counts + the total, from the view.
func (r *Repository) UnreadSummary(ctx context.Context, userID string) (UnreadCounts, error) {
	if r.pool == nil {
		return UnreadCounts{}, errNoDB
	}
	// DIB-268: the badge endpoint reads msg_unread_effective, the PREFS-AWARE view (0137), not the raw
	// msg_unread_summary. A MUTED channel yields unread_count 0 there and is therefore filtered out by
	// the `> 0` predicate below, which is precisely what makes "mute a channel → badge stops" true.
	rows, err := r.pool.Query(ctx, `
		SELECT channel_id::text, unread_count, mentioned, latest_at
		FROM msg_unread_effective
		WHERE user_id = $1 AND unread_count > 0
		ORDER BY latest_at DESC`, userID)
	if err != nil {
		return UnreadCounts{}, fmt.Errorf("unread summary: %w", err)
	}
	defer rows.Close()
	out := UnreadCounts{Channels: []ChannelUnread{}}
	for rows.Next() {
		var cu ChannelUnread
		var latest *time.Time
		if err := rows.Scan(&cu.ChannelID, &cu.Unread, &cu.Mentioned, &latest); err != nil {
			return UnreadCounts{}, fmt.Errorf("scan unread: %w", err)
		}
		cu.LatestAt = latest
		out.Total += cu.Unread
		out.Channels = append(out.Channels, cu)
	}
	return out, rows.Err()
}

// - FR-3: system event → message -
// InsertEventMessage writes a system EVENT message (author NULL) into a channel, idempotently: the
// (channel_id, event_key) unique index means a redelivered outbox row is a no-op. Returns the
// inserted message and inserted=true, or inserted=false when it already existed. ref is the
// {topic, entity, deep_link, summary} JSON. requiresAck (0071) flags a forced-ack post the kiosk
// screen-takeover must block on. This is the single write path for FR-3.
func (r *Repository) InsertEventMessage(ctx context.Context, channelID, eventKey, body string, ref EventRef, requiresAck bool) (Message, bool, error) {
	if r.pool == nil {
		return Message{}, false, errNoDB
	}
	refBytes, err := json.Marshal(ref)
	if err != nil {
		return Message{}, false, fmt.Errorf("marshal ref: %w", err)
	}
	var m Message
	err = r.pool.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, ref, event_key, requires_ack)
		VALUES ($1, NULL, 'EVENT', $2, $3, $4, $5)
		ON CONFLICT (channel_id, event_key) WHERE event_key IS NOT NULL DO NOTHING
		RETURNING id::text, channel_id::text, kind, body, requires_ack, created_at`,
		channelID, body, refBytes, eventKey, requiresAck).
		Scan(&m.ID, &m.ChannelID, &m.Kind, &m.Body, &m.RequiresAck, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, false, nil // already posted (idempotent redelivery)
		}
		return Message{}, false, fmt.Errorf("insert event message: %w", err)
	}
	m.Ref = json.RawMessage(refBytes)
	return m, true, nil
}

// - Approvals rail (0075) -
// EnsureApprovalChannel lazily + idempotently provisions the caller-approver's dedicated APPROVAL
// channel (slug approvals-<username>, sole member the approver) and returns its id. Idempotent two ways:
// the channel upserts on its unique slug (a second call returns the same id), and the member row upserts
// on the (channel_id, user_id) PK. The member is EXPLICIT so the FR-1 derived-membership prune never
// garbage-collects it. Returns the channel id.
func (r *Repository) EnsureApprovalChannel(ctx context.Context, userID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	// Resolve a human handle for the slug (display name, else email local-part, else the id).
	var displayName, email string
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(display_name,''), COALESCE(email::text,'') FROM users WHERE id = $1`, userID).
		Scan(&displayName, &email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", fmt.Errorf("resolve approver: %w", err)
	}
	handle := displayName
	if handle == "" {
		handle, _, _ = strings.Cut(email, "@")
	}
	slug := approvalChannelSlug(handle, userID)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var channelID string
	// DO UPDATE (not DO NOTHING) so RETURNING always yields the id, whether the row is new or pre-existing.
	if err := tx.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, topic, tier)
		VALUES ('APPROVAL', $1, $2, 'Your approval requests', 'STANDARD')
		ON CONFLICT (slug) DO UPDATE SET updated_at = now()
		RETURNING id::text`, slug, "Approvals, "+handle).Scan(&channelID); err != nil {
		return "", fmt.Errorf("ensure approval channel: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'OWNER', 'EXPLICIT')
		ON CONFLICT (channel_id, user_id) DO NOTHING`, channelID, userID); err != nil {
		return "", fmt.Errorf("ensure approval member: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return channelID, nil
}

// PostApprovalMessage writes a system-authored APPROVAL card (author NULL) into a channel, idempotent on
// (channel_id, event_key): a re-notify or a reconcile-sweep re-fan of the same request is a silent no-op
// (inserted=false). ref is the typed ApprovalRef JSON. This mirrors InsertEventMessage but with kind
// APPROVAL, so the card carries the request pointer + display status rather than an EVENT topic.
func (r *Repository) PostApprovalMessage(ctx context.Context, channelID, eventKey, body string, ref ApprovalRef) (Message, bool, error) {
	if r.pool == nil {
		return Message{}, false, errNoDB
	}
	refBytes, err := json.Marshal(ref)
	if err != nil {
		return Message{}, false, fmt.Errorf("marshal approval ref: %w", err)
	}
	var m Message
	err = r.pool.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, ref, event_key)
		VALUES ($1, NULL, 'APPROVAL', $2, $3, $4)
		ON CONFLICT (channel_id, event_key) WHERE event_key IS NOT NULL DO NOTHING
		RETURNING id::text, channel_id::text, kind, body, created_at`,
		channelID, body, refBytes, eventKey).
		Scan(&m.ID, &m.ChannelID, &m.Kind, &m.Body, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, false, nil // already posted (idempotent re-notify)
		}
		return Message{}, false, fmt.Errorf("post approval message: %w", err)
	}
	m.Ref = json.RawMessage(refBytes)
	return m, true, nil
}

// StampApprovalStatus updates the display `status` on EVERY APPROVAL card copy of one request (one per
// approver channel, they share the exact card event_key), the cross-surface decision echo. It is
// display-only: the live truth is the permission_approval_requests row. Idempotent (a re-stamp to the
// same value is a no-op UPDATE). Returns the number of card copies stamped.
func (r *Repository) StampApprovalStatus(ctx context.Context, approvalID, status string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	ct, err := r.pool.Exec(ctx, `
		UPDATE msg_messages
		SET ref = jsonb_set(ref, '{status}', to_jsonb($2::text), true),
		    seq = nextval(pg_get_serial_sequence('msg_messages', 'seq'))
		WHERE kind = 'APPROVAL' AND event_key = $1`, approvalCardKey(approvalID), status)
	if err != nil {
		return 0, fmt.Errorf("stamp approval status: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

// ApprovalChannelIDs returns the channels that already hold a card for one request (one per approver
// fanned to). The decision echo posts its EVENT into exactly these channels, so an approver who never got
// a card never gets a stray echo either.
func (r *Repository) ApprovalChannelIDs(ctx context.Context, approvalID string) ([]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT channel_id::text FROM msg_messages WHERE kind = 'APPROVAL' AND event_key = $1`,
		approvalCardKey(approvalID))
	if err != nil {
		return nil, fmt.Errorf("approval channel ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan approval channel id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PostApprovalEcho writes the decision EVENT ("Approved/Declined by <name>, <note>") into one channel,
// idempotent on the decided event_key. Reuses the EVENT write path so the echo renders like any other
// system event in the hub.
func (r *Repository) PostApprovalEcho(ctx context.Context, channelID, approvalID, body string, ref EventRef) (Message, bool, error) {
	return r.InsertEventMessage(ctx, channelID, approvalDecidedKey(approvalID), body, ref, false)
}

// UserDisplayName resolves a user's display name (empty when unknown), used to render the decider's
// name in the decision echo without leaking any other user field.
func (r *Repository) UserDisplayName(ctx context.Context, userID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var name string
	if err := r.pool.QueryRow(ctx, `SELECT COALESCE(display_name,'') FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("user display name: %w", err)
	}
	return name, nil
}

// - Feature 1: Screen-Takeover forced acknowledgement (0071) -
// unackedPollLimit bounds one kiosk poll: the kiosk only ever shows the single newest un-acked
// message, so a small ceiling is plenty and keeps the anti-join response light.
const unackedPollLimit = 20

// AckMessage upserts the caller's acknowledgement of a forced-ack message. The INSERT ... SELECT binds
// the write to (messageID, channelID): the source row exists only when that message lives in that
// channel, so a member of channel A cannot ack a foreign channel's message id (a cross-channel IDOR,
// mirroring ListThread's channel binding). A message that is absent / in another channel yields no
// source row → no RETURNING → errNotFound (404). ON CONFLICT DO UPDATE makes a repeat tap idempotent
// (and always RETURNs the row, so a duplicate ack is not mis-reported as not-found).
func (r *Repository) AckMessage(ctx context.Context, channelID, messageID, userID, method string) (Ack, error) {
	if r.pool == nil {
		return Ack{}, errNoDB
	}
	var a Ack
	err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_acks (message_id, user_id, method)
		SELECT m.id, $2, $4 FROM msg_messages m WHERE m.id = $1 AND m.channel_id = $3
		ON CONFLICT (message_id, user_id) DO UPDATE
		  SET method = EXCLUDED.method, acked_at = now(),
		      seq = nextval(pg_get_serial_sequence('msg_acks', 'seq'))
		RETURNING message_id::text, user_id::text, method, acked_at`,
		messageID, userID, channelID, method).
		Scan(&a.MessageID, &a.UserID, &a.Method, &a.AckedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Ack{}, errNotFound // message absent or not in this channel
		}
		return Ack{}, fmt.Errorf("ack message: %w", err)
	}
	return a, nil
}

// UnackedRequiresAck returns a user's latest forced-ack messages they have NOT yet acked, newest-first
// , the kiosk poll. It joins msg_members so only channels the user belongs to are considered (the
// membership authz is intrinsic to the read), and anti-joins msg_acks for that user. Soft-deleted
// messages are excluded (a tombstoned alert should never block the screen).
//
// DIB-217: SUPERSEDED messages are excluded too (superseded_at IS NULL). This one predicate is what
// makes the LAST-WRITE-WINS ruling real on BOTH consumers of the shared forced-ack schema at once -
// the kiosk takeover and the in-hub bubble poll this same query, so an auto-resolved stale fact stops
// blocking everywhere simultaneously. A user never acks a stale fact presented as current.
func (r *Repository) UnackedRequiresAck(ctx context.Context, userID string) ([]Message, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.id::text, m.channel_id::text, COALESCE(m.author_id::text,''), COALESCE(u.display_name,''),
		       m.kind, m.body, m.ref, COALESCE(m.thread_root_id::text,''), 0,
		       m.edited_at, m.deleted_at, m.created_at
		FROM msg_messages m
		JOIN msg_members mem ON mem.channel_id = m.channel_id AND mem.user_id = $1
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.requires_ack = TRUE
		  AND m.deleted_at IS NULL
		  AND m.superseded_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM msg_acks a WHERE a.message_id = m.id AND a.user_id = $1)
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $2`, userID, unackedPollLimit)
	if err != nil {
		return nil, fmt.Errorf("unacked requires-ack: %w", err)
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	// Every returned row is requires_ack by the WHERE clause; stamp the flag the read model exposes so
	// the kiosk does not have to infer it from the endpoint it called.
	for i := range msgs {
		msgs[i].RequiresAck = true
	}
	return msgs, nil
}

// ChannelIDBySlug resolves a stable channel slug to its id (used by the FR-3 consumer to route an
// event to e.g. #yard-trenton). Returns errNotFound when the slug is unknown.
func (r *Repository) ChannelIDBySlug(ctx context.Context, slug string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx, `SELECT id::text FROM msg_channels WHERE slug = $1 AND is_archived = FALSE`, slug).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", fmt.Errorf("resolve slug: %w", err)
	}
	return id, nil
}

// BranchYardSlug returns the yard channel slug for a branch id (the FR-3 map: order/stock/delivery
// events for a branch land in that branch's #yard-<name> channel). It resolves the branch's short
// code from locations and lowercases it, so #yard-trenton / #yard-kingston fall out of the data.
func (r *Repository) BranchYardSlug(ctx context.Context, branchID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var code string
	err := r.pool.QueryRow(ctx, `SELECT lower(code) FROM locations WHERE id = $1 AND loc_type = 'BRANCH'`, branchID).Scan(&code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", fmt.Errorf("resolve branch code: %w", err)
	}
	// locations.code is TRT/KGN; the seeded yard slugs are yard-trenton/yard-kingston. Map via a
	// lookup on the branch's city name so the slug matches the seed regardless of code casing.
	switch code {
	case "trt":
		return "yard-trenton", nil
	case "kgn":
		return "yard-kingston", nil
	default:
		return "yard-" + code, nil
	}
}

// - Feature B: HR/audit export (0070) -
// maxExportRows caps one export page so an admin query can never table-scan the whole history in one
// response (mirrors audit's 1..500 clamp; export rows are lighter so the ceiling is higher).
const maxExportRows = 5000

// ExportMessages returns raw message rows for the admin HR/audit export, filtered by channel/user/time
// window. It returns UNMASKED rows exactly like audit's repo.List, the admin-masked projection
// (projectForExport) runs in the service before the response leaves, mirroring the audit read seam.
// The four filter params are $ = " OR <cast> guards, matching audit.List, so an empty filter is a
// no-op and the handler has already validated the uuid/timestamp shapes at the boundary.
func (r *Repository) ExportMessages(ctx context.Context, f ExportFilter) ([]Message, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := `
		SELECT m.id::text, m.channel_id::text, COALESCE(m.author_id::text,''), COALESCE(u.display_name,''),
		       m.kind, m.body, m.ref, COALESCE(m.thread_root_id::text,''), 0,
		       m.edited_at, m.deleted_at, m.created_at
		FROM msg_messages m
		LEFT JOIN users u ON u.id = m.author_id
		WHERE ($1 = '' OR m.channel_id = $1::uuid)
		  AND ($2 = '' OR m.author_id = $2::uuid)
		  AND ($3 = '' OR m.created_at >= $3::timestamptz)
		  AND ($4 = '' OR m.created_at <  $4::timestamptz)
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $5`
	rows, err := r.pool.Query(ctx, q, f.ChannelID, f.UserID, f.Since, f.Until, clampExport(f.Limit))
	if err != nil {
		return nil, fmt.Errorf("export messages: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func clampExport(n int) int {
	switch {
	case n <= 0:
		return maxExportRows
	case n > maxExportRows:
		return maxExportRows
	default:
		return n
	}
}

// - Channel create, DM create, membership writes -

// channelCols is the channel projection the create paths return (the same
// columns ListChannelsForUser selects, minus the per-caller unread legs).
const channelCols = `id::text, kind, COALESCE(branch_id::text,''), COALESCE(slug::text,''),
	name, topic, tier, is_archived, created_at`

func scanChannel(row pgx.Row) (Channel, error) {
	var c Channel
	if err := row.Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier,
		&c.IsArchived, &c.CreatedAt); err != nil {
		return Channel{}, err
	}
	return c, nil
}

// userActive resolves one staff row's status: found+ACTIVE, found+other
// (ErrUserNotActive), or absent (errNotFound). The users read is the
// module's existing attribution seam (EnsureApprovalChannel reads the same
// columns through the identity-is-SoT allowance repository.go carries).
func (r *Repository) userActive(ctx context.Context, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	var status string
	if err := r.pool.QueryRow(ctx, `SELECT status FROM users WHERE id = $1`, userID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return fmt.Errorf("resolve user: %w", err)
	}
	if status != "ACTIVE" {
		return ErrUserNotActive
	}
	return nil
}

// CreateChannel inserts one caller-founded channel idempotently: the slug's
// unique index is the convergence point, so a second founding of the same
// room returns the existing row (created false). DO UPDATE (not DO NOTHING)
// keeps RETURNING answering on both arms, and xmax = 0 is the insert marker.
// The founder is seated as OWNER, EXPLICIT, so the derived prune never drops
// them out of their own room.
func (r *Repository) CreateChannel(ctx context.Context, in CreateChannelInput, ownerUserID string) (Channel, bool, error) {
	if r.pool == nil {
		return Channel{}, false, errNoDB
	}
	// A named branch must be a real branch root: a branch that does not exist
	// names no channel, and 404 leaks nothing a caller could use.
	if in.BranchID != "" {
		var ok bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM locations WHERE id = $1 AND loc_type = 'BRANCH')`,
			in.BranchID).Scan(&ok); err != nil {
			return Channel{}, false, fmt.Errorf("resolve branch: %w", err)
		}
		if !ok {
			return Channel{}, false, errNotFound
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Channel{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var c Channel
	var created bool
	if err := tx.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, topic, tier)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5, 'STANDARD')
		ON CONFLICT (slug) DO UPDATE SET updated_at = now()
		RETURNING `+channelCols+`, (xmax = 0)`,
		in.Kind, in.BranchID, in.Slug, in.Name, in.Topic).
		Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier,
			&c.IsArchived, &c.CreatedAt, &created); err != nil {
		return Channel{}, false, fmt.Errorf("create channel: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'OWNER', 'EXPLICIT')
		ON CONFLICT (channel_id, user_id) DO NOTHING`, c.ID, ownerUserID); err != nil {
		return Channel{}, false, fmt.Errorf("seat channel owner: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Channel{}, false, err
	}
	return c, created, nil
}

// dmPairSlug is the STABLE, PAIR-UNIQUE handle for a direct message channel
// (the two user ids in sorted order, the same discipline
// approvalChannelSlug and repDMSlug carry: the handle must be unique per
// pair no matter what display names change later).
func dmPairSlug(aUserID, bUserID string) string {
	if aUserID > bUserID {
		aUserID, bUserID = bUserID, aUserID
	}
	return "dm-" + aUserID + "-" + bUserID
}

// CreateDM finds the DM channel a pair of staffers already shares (even a
// seeded one, whose slug is NULL) or creates it exactly once, seating both
// parties EXPLICIT. The pre-lookup covers the seeded NULL-slug pairs; the
// deterministic slug's unique index is the race backstop (two simultaneous
// openings converge on one row).
func (r *Repository) CreateDM(ctx context.Context, aUserID, bUserID string) (Channel, bool, error) {
	if r.pool == nil {
		return Channel{}, false, errNoDB
	}
	if err := r.userActive(ctx, aUserID); err != nil {
		return Channel{}, false, err
	}
	if err := r.userActive(ctx, bUserID); err != nil {
		return Channel{}, false, err
	}
	var aName, bName string
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(display_name,'') FROM users WHERE id = $1`, aUserID).Scan(&aName); err != nil {
		return Channel{}, false, fmt.Errorf("resolve caller: %w", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(display_name,'') FROM users WHERE id = $1`, bUserID).Scan(&bName); err != nil {
		return Channel{}, false, fmt.Errorf("resolve counterpart: %w", err)
	}
	// The seeded demo DMs carry no slug, so the pair lookup is by shape: a
	// live DM channel whose members are exactly the two staffers.
	var existing string
	if err := r.pool.QueryRow(ctx, `
		SELECT ch.id::text
		FROM msg_channels ch
		JOIN msg_members ma ON ma.channel_id = ch.id AND ma.user_id = $1
		JOIN msg_members mb ON mb.channel_id = ch.id AND mb.user_id = $2
		WHERE ch.kind = 'DM' AND ch.is_archived = FALSE
		  AND (SELECT count(*) FROM msg_members m WHERE m.channel_id = ch.id) = 2
		LIMIT 1`, aUserID, bUserID).Scan(&existing); err == nil {
		c, cerr := scanChannel(r.pool.QueryRow(ctx,
			`SELECT `+channelCols+` FROM msg_channels WHERE id = $1`, existing))
		if cerr != nil && !errors.Is(cerr, pgx.ErrNoRows) {
			return Channel{}, false, fmt.Errorf("load pair dm: %w", cerr)
		}
		if cerr == nil {
			return c, false, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, false, fmt.Errorf("find pair dm: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Channel{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var c Channel
	var created bool
	if err := tx.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, topic, tier)
		VALUES ('DM', $1, $2, '', 'STANDARD')
		ON CONFLICT (slug) DO UPDATE SET updated_at = now()
		RETURNING `+channelCols+`, (xmax = 0)`,
		dmPairSlug(aUserID, bUserID), aName+", "+bName).
		Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier,
			&c.IsArchived, &c.CreatedAt, &created); err != nil {
		return Channel{}, false, fmt.Errorf("create dm: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'OWNER', 'EXPLICIT'), ($1, $3, 'MEMBER', 'EXPLICIT')
		ON CONFLICT (channel_id, user_id) DO NOTHING`, c.ID, aUserID, bUserID); err != nil {
		return Channel{}, false, fmt.Errorf("seat dm parties: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Channel{}, false, err
	}
	return c, created, nil
}

// AddChannelMember seats one staff member EXPLICIT in a channel
// (idempotent; an already-seated member changes nothing, whatever the
// existing row's source). The unknown-user and inactive-user refusals run
// before anything is written.
func (r *Repository) AddChannelMember(ctx context.Context, channelID, userID string) (ChannelMember, error) {
	if r.pool == nil {
		return ChannelMember{}, errNoDB
	}
	if err := r.userActive(ctx, userID); err != nil {
		return ChannelMember{}, err
	}
	var m ChannelMember
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'MEMBER', 'EXPLICIT')
		ON CONFLICT (channel_id, user_id) DO UPDATE SET source = 'EXPLICIT'
		RETURNING channel_id::text, user_id::text, role, source, joined_at`,
		channelID, userID).
		Scan(&m.ChannelID, &m.UserID, &m.Role, &m.Source, &m.JoinedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChannelMember{}, errNotFound // no such channel
		}
		return ChannelMember{}, fmt.Errorf("add channel member: %w", err)
	}
	return m, nil
}

// RemoveChannelMember drops one member row. A DERIVED row is refused
// (ErrDerivedMember): the role and branch model re-derives it on the next
// sync, so a hand removal would be undone and lie in between. An absent row
// is not-found.
func (r *Repository) RemoveChannelMember(ctx context.Context, channelID, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	var source string
	if err := r.pool.QueryRow(ctx,
		`SELECT source FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
		channelID, userID).Scan(&source); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return fmt.Errorf("resolve member: %w", err)
	}
	if source == SourceDerived {
		return ErrDerivedMember
	}
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM msg_members WHERE channel_id = $1 AND user_id = $2 AND source = 'EXPLICIT'`,
		channelID, userID); err != nil {
		return fmt.Errorf("remove channel member: %w", err)
	}
	if err := r.bumpChannelSeq(ctx, channelID); err != nil {
		return err
	}
	return nil
}

// - The sync feeds (0381): per-caller reads and the stamped writes -
//
// Every feed query here is fenced to ONE user's own world: the channels and
// messages feeds join the caller's membership, the acks and prefs feeds bind
// user_id to the caller. That fence is stronger than an org predicate on
// these tables (which carry no org column; the GLOBAL channels cross orgs by
// design), and it is the fence the stores in syncscope.go serve.

// channelFeedRow is the channels feed's projection (the channel's intrinsic
// facts; the per-caller unread legs are read-projections, not feed facts).
type channelFeedRow struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	BranchID   string    `json:"branch_id,omitempty"`
	Slug       string    `json:"slug,omitempty"`
	Name       string    `json:"name"`
	Topic      string    `json:"topic"`
	Tier       string    `json:"tier"`
	IsArchived bool      `json:"is_archived"`
	CreatedAt  time.Time `json:"created_at"`
}

// messageFeedRow is the messages feed's projection, the REST Message shape
// minus the reply-count read projection.
type messageFeedRow struct {
	ID           string          `json:"id"`
	ChannelID    string          `json:"channel_id"`
	AuthorID     string          `json:"author_id,omitempty"`
	AuthorName   string          `json:"author_name,omitempty"`
	Kind         string          `json:"kind"`
	Body         string          `json:"body"`
	Ref          json.RawMessage `json:"ref,omitempty"`
	ThreadRootID string          `json:"thread_root_id,omitempty"`
	RequiresAck  bool            `json:"requires_ack,omitempty"`
	Priority     string          `json:"priority,omitempty"`
	StateKey     string          `json:"state_key,omitempty"`
	SupersededBy string          `json:"superseded_by,omitempty"`
	SupersededAt *time.Time      `json:"superseded_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// ackFeedRow is the acks feed's projection (the caller's own rows only).
type ackFeedRow struct {
	ID        string    `json:"id"`
	MessageID string    `json:"message_id"`
	UserID    string    `json:"user_id"`
	Method    string    `json:"method"`
	AckedAt   time.Time `json:"acked_at"`
}

// prefsFeedRow is the notify_prefs feed's projection (the caller's own rows).
type prefsFeedRow struct {
	ID                 string    `json:"id"`
	ChannelID          string    `json:"channel_id"`
	UserID             string    `json:"user_id"`
	DesktopEnabled     bool      `json:"desktop_enabled"`
	EscalationOverride bool      `json:"escalation_override"`
	Muted              bool      `json:"muted"`
	Mode               string    `json:"mode"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// syncChannelsPage returns the caller's channels past since, in seq order.
func (r *Repository) syncChannelsPage(ctx context.Context, userID string, since int64, limit int) ([]channelFeedRow, []int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ch.id::text, ch.kind, COALESCE(ch.branch_id::text,''), COALESCE(ch.slug::text,''),
		       ch.name, ch.topic, ch.tier, ch.is_archived, ch.created_at, ch.seq
		FROM msg_members mem
		JOIN msg_channels ch ON ch.id = mem.channel_id
		WHERE mem.user_id = $1 AND ch.seq > $2
		  AND ch.deleted_at IS NULL AND ch.is_archived = FALSE
		ORDER BY ch.seq ASC
		LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("sync channels page: %w", err)
	}
	defer rows.Close()
	var out []channelFeedRow
	var seqs []int64
	for rows.Next() {
		var c channelFeedRow
		var seq int64
		if err := rows.Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic,
			&c.Tier, &c.IsArchived, &c.CreatedAt, &seq); err != nil {
			return nil, nil, fmt.Errorf("sync channels scan: %w", err)
		}
		out = append(out, c)
		seqs = append(seqs, seq)
	}
	return out, seqs, rows.Err()
}

// syncChannelLoad reads one channel under the caller's membership fence.
func (r *Repository) syncChannelLoad(ctx context.Context, userID, channelID string) (channelFeedRow, int64, bool, error) {
	var c channelFeedRow
	var seq int64
	err := r.pool.QueryRow(ctx, `
		SELECT ch.id::text, ch.kind, COALESCE(ch.branch_id::text,''), COALESCE(ch.slug::text,''),
		       ch.name, ch.topic, ch.tier, ch.is_archived, ch.created_at, ch.seq
		FROM msg_members mem
		JOIN msg_channels ch ON ch.id = mem.channel_id
		WHERE mem.user_id = $1 AND ch.id = $2::uuid
		  AND ch.deleted_at IS NULL AND ch.is_archived = FALSE`,
		userID, channelID).
		Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier, &c.IsArchived, &c.CreatedAt, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return channelFeedRow{}, 0, false, nil
	}
	if err != nil {
		return channelFeedRow{}, 0, false, fmt.Errorf("sync channel load: %w", err)
	}
	return c, seq, true, nil
}

// syncMessagesPage returns the messages of the caller's channels past since.
func (r *Repository) syncMessagesPage(ctx context.Context, userID string, since int64, limit int) ([]messageFeedRow, []int64, []bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id::text, m.channel_id::text, COALESCE(m.author_id::text,''), COALESCE(u.display_name,''),
		       m.kind, m.body, m.ref, COALESCE(m.thread_root_id::text,''),
		       m.requires_ack, COALESCE(m.priority,''), COALESCE(m.state_key,''),
		       COALESCE(m.superseded_by::text,''), m.superseded_at, m.created_at, m.seq,
		       (m.deleted_at IS NOT NULL)
		FROM msg_messages m
		JOIN msg_members mem ON mem.channel_id = m.channel_id AND mem.user_id = $1
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.seq > $2
		ORDER BY m.seq ASC
		LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sync messages page: %w", err)
	}
	defer rows.Close()
	var out []messageFeedRow
	var seqs []int64
	var deleted []bool
	for rows.Next() {
		var m messageFeedRow
		var ref []byte
		var seq int64
		var del bool
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.AuthorName, &m.Kind, &m.Body,
			&ref, &m.ThreadRootID, &m.RequiresAck, &m.Priority, &m.StateKey,
			&m.SupersededBy, &m.SupersededAt, &m.CreatedAt, &seq, &del); err != nil {
			return nil, nil, nil, fmt.Errorf("sync messages scan: %w", err)
		}
		if len(ref) > 0 && string(ref) != "{}" {
			m.Ref = json.RawMessage(ref)
		}
		if del {
			m.Body = "" // a tombstone never carries content
		}
		out = append(out, m)
		seqs = append(seqs, seq)
		deleted = append(deleted, del)
	}
	return out, seqs, deleted, rows.Err()
}

// syncMessageLoad reads one message under the caller's membership fence.
func (r *Repository) syncMessageLoad(ctx context.Context, userID, messageID string) (messageFeedRow, int64, bool, error) {
	var m messageFeedRow
	var ref []byte
	var seq int64
	var del bool
	err := r.pool.QueryRow(ctx, `
		SELECT m.id::text, m.channel_id::text, COALESCE(m.author_id::text,''), COALESCE(u.display_name,''),
		       m.kind, m.body, m.ref, COALESCE(m.thread_root_id::text,''),
		       m.requires_ack, COALESCE(m.priority,''), COALESCE(m.state_key,''),
		       COALESCE(m.superseded_by::text,''), m.superseded_at, m.created_at, m.seq,
		       (m.deleted_at IS NOT NULL)
		FROM msg_messages m
		JOIN msg_members mem ON mem.channel_id = m.channel_id AND mem.user_id = $1
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.id = $2::uuid`,
		userID, messageID).
		Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.AuthorName, &m.Kind, &m.Body,
			&ref, &m.ThreadRootID, &m.RequiresAck, &m.Priority, &m.StateKey,
			&m.SupersededBy, &m.SupersededAt, &m.CreatedAt, &seq, &del)
	if errors.Is(err, pgx.ErrNoRows) {
		return messageFeedRow{}, 0, false, nil
	}
	if err != nil {
		return messageFeedRow{}, 0, false, fmt.Errorf("sync message load: %w", err)
	}
	if len(ref) > 0 && string(ref) != "{}" {
		m.Ref = json.RawMessage(ref)
	}
	if del {
		m.Body = ""
	}
	return m, seq, true, nil
}

// syncMessageFindApplied resolves one idempotency key to the caller's own
// message (the fence holds even for a row the caller has since left).
func (r *Repository) syncMessageFindApplied(ctx context.Context, userID, key string) (string, int64, bool, error) {
	var id string
	var seq int64
	err := r.pool.QueryRow(ctx, `
		SELECT m.id::text, m.seq FROM msg_messages m
		JOIN msg_members mem ON mem.channel_id = m.channel_id AND mem.user_id = $1
		WHERE m.idempotency_key = $2::uuid`, userID, key).Scan(&id, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("sync message find-applied: %w", err)
	}
	return id, seq, true, nil
}

// syncMessageCreate writes one offline post through the module's own rules
// (membership, the same-channel thread root) stamping the client key; the
// identity column stamps the seq. A refused precondition answers
// errNotFound, exactly like the REST thread-root guard.
func (r *Repository) syncMessageCreate(ctx context.Context, authorID, channelID, body, threadRootID, key string) (string, int64, error) {
	member, err := r.IsMember(ctx, channelID, authorID)
	if err != nil {
		return "", 0, err
	}
	if !member {
		return "", 0, fmt.Errorf("%w: not a channel member", errNotFound)
	}
	var id string
	var seq int64
	err = r.pool.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, thread_root_id, idempotency_key)
		SELECT $1, $2, 'TEXT', $3, NULLIF($4,'')::uuid, $5::uuid
		WHERE $4 = '' OR EXISTS (
		        SELECT 1 FROM msg_messages root
		        WHERE root.id = $4::uuid AND root.channel_id = $1 AND root.thread_root_id IS NULL)
		RETURNING id::text, seq`,
		channelID, authorID, body, threadRootID, key).Scan(&id, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, fmt.Errorf("%w: thread root not in channel", errNotFound)
	}
	if err != nil {
		return "", 0, fmt.Errorf("sync message create: %w", err)
	}
	return id, seq, nil
}

// syncAcksPage returns the caller's own ack rows past since.
func (r *Repository) syncAcksPage(ctx context.Context, userID string, since int64, limit int) ([]ackFeedRow, []int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id::text, a.message_id::text, a.user_id::text, a.method, a.acked_at, a.seq,
		       (a.deleted_at IS NOT NULL) AS deleted
		FROM msg_acks a
		WHERE a.user_id = $1 AND a.seq > $2 AND a.deleted_at IS NULL
		ORDER BY a.seq ASC
		LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("sync acks page: %w", err)
	}
	defer rows.Close()
	var out []ackFeedRow
	var seqs []int64
	for rows.Next() {
		var a ackFeedRow
		var seq int64
		var deleted bool
		if err := rows.Scan(&a.ID, &a.MessageID, &a.UserID, &a.Method, &a.AckedAt, &seq, &deleted); err != nil {
			return nil, nil, fmt.Errorf("sync acks scan: %w", err)
		}
		if deleted {
			continue // a tombstone never carries a projection
		}
		out = append(out, a)
		seqs = append(seqs, seq)
	}
	return out, seqs, rows.Err()
}

// syncAckLoad reads one ack under the caller's own-row fence.
func (r *Repository) syncAckLoad(ctx context.Context, userID, ackID string) (ackFeedRow, int64, bool, error) {
	var a ackFeedRow
	var seq int64
	err := r.pool.QueryRow(ctx, `
		SELECT a.id::text, a.message_id::text, a.user_id::text, a.method, a.acked_at, a.seq
		FROM msg_acks a
		WHERE a.user_id = $1 AND a.id = $2::uuid AND a.deleted_at IS NULL`,
		userID, ackID).Scan(&a.ID, &a.MessageID, &a.UserID, &a.Method, &a.AckedAt, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return ackFeedRow{}, 0, false, nil
	}
	if err != nil {
		return ackFeedRow{}, 0, false, fmt.Errorf("sync ack load: %w", err)
	}
	return a, seq, true, nil
}

// syncAckFindApplied resolves one idempotency key to the caller's own ack.
func (r *Repository) syncAckFindApplied(ctx context.Context, userID, key string) (string, int64, bool, error) {
	var id string
	var seq int64
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, seq FROM msg_acks
		WHERE user_id = $1 AND idempotency_key = $2::uuid`, userID, key).Scan(&id, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("sync ack find-applied: %w", err)
	}
	return id, seq, true, nil
}

// syncAckCreate records the caller's acknowledgement of one message through
// the module's own rules: the write binds to (message, its channel, the
// caller's membership), idempotent on the upsert, method validated at the
// adapter. The key column records the latest applied key; a fresh seq is
// drawn on both arms so the feed refreshes the row.
func (r *Repository) syncAckCreate(ctx context.Context, userID, messageID, method, key string) (string, int64, error) {
	var id string
	var seq int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_acks (message_id, user_id, method, idempotency_key, seq)
		SELECT m.id, $2, $3, $4::uuid, nextval(pg_get_serial_sequence('msg_acks', 'seq'))
		FROM msg_messages m
		WHERE m.id = $1 AND EXISTS (
		        SELECT 1 FROM msg_members mem
		        WHERE mem.channel_id = m.channel_id AND mem.user_id = $2)
		ON CONFLICT (message_id, user_id) DO UPDATE
		  SET method = EXCLUDED.method, acked_at = now(),
		      idempotency_key = EXCLUDED.idempotency_key,
		      seq = nextval(pg_get_serial_sequence('msg_acks', 'seq'))
		RETURNING id::text, seq`,
		messageID, userID, method, key).Scan(&id, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, fmt.Errorf("%w: message absent or not in a channel the caller belongs to", errNotFound)
	}
	if err != nil {
		return "", 0, fmt.Errorf("sync ack create: %w", err)
	}
	return id, seq, nil
}

// syncPrefsPage returns the caller's own pref rows past since.
func (r *Repository) syncPrefsPage(ctx context.Context, userID string, since int64, limit int) ([]prefsFeedRow, []int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id::text, p.channel_id::text, p.user_id::text,
		       p.desktop_enabled, p.escalation_override, p.muted, p.mode,
		       p.updated_at, p.seq, (p.deleted_at IS NOT NULL) AS deleted
		FROM msg_notification_prefs p
		WHERE p.user_id = $1 AND p.seq > $2 AND p.deleted_at IS NULL
		ORDER BY p.seq ASC
		LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("sync prefs page: %w", err)
	}
	defer rows.Close()
	var out []prefsFeedRow
	var seqs []int64
	for rows.Next() {
		var p prefsFeedRow
		var seq int64
		var deleted bool
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.UserID, &p.DesktopEnabled, &p.EscalationOverride,
			&p.Muted, &p.Mode, &p.UpdatedAt, &seq, &deleted); err != nil {
			return nil, nil, fmt.Errorf("sync prefs scan: %w", err)
		}
		if deleted {
			continue // a tombstone never carries a projection
		}
		out = append(out, p)
		seqs = append(seqs, seq)
	}
	return out, seqs, rows.Err()
}

// syncPrefsLoad reads one pref row under the caller's own-row fence.
func (r *Repository) syncPrefsLoad(ctx context.Context, userID, prefID string) (prefsFeedRow, int64, bool, error) {
	var p prefsFeedRow
	var seq int64
	err := r.pool.QueryRow(ctx, `
		SELECT p.id::text, p.channel_id::text, p.user_id::text,
		       p.desktop_enabled, p.escalation_override, p.muted, p.mode, p.updated_at, p.seq
		FROM msg_notification_prefs p
		WHERE p.user_id = $1 AND p.id = $2::uuid AND p.deleted_at IS NULL`,
		userID, prefID).
		Scan(&p.ID, &p.ChannelID, &p.UserID, &p.DesktopEnabled, &p.EscalationOverride, &p.Muted, &p.Mode, &p.UpdatedAt, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return prefsFeedRow{}, 0, false, nil
	}
	if err != nil {
		return prefsFeedRow{}, 0, false, fmt.Errorf("sync prefs load: %w", err)
	}
	return p, seq, true, nil
}

// syncPrefsFindApplied resolves one idempotency key to the caller's own row.
func (r *Repository) syncPrefsFindApplied(ctx context.Context, userID, key string) (string, int64, bool, error) {
	var id string
	var seq int64
	err := r.pool.QueryRow(ctx, `
		SELECT id::text, seq FROM msg_notification_prefs
		WHERE user_id = $1 AND idempotency_key = $2::uuid`, userID, key).Scan(&id, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("sync prefs find-applied: %w", err)
	}
	return id, seq, true, nil
}

// syncPrefsWrite upserts the caller's prefs for one channel with the client
// key stamped and a fresh seq on both arms (a pref row has no create act
// distinct from its write; the adapter runs the module's own merge and
// escalation rules first).
func (r *Repository) syncPrefsWrite(ctx context.Context, p NotificationPrefs, key string) (string, int64, error) {
	p = applyMode(p, ModeOf(p))
	var id string
	var seq int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_notification_prefs
		  (channel_id, user_id, desktop_enabled, escalation_override, muted, mode,
		   updated_at, idempotency_key, seq)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7::uuid,
		        nextval(pg_get_serial_sequence('msg_notification_prefs', 'seq')))
		ON CONFLICT (channel_id, user_id) DO UPDATE
		  SET desktop_enabled = EXCLUDED.desktop_enabled,
		      escalation_override = EXCLUDED.escalation_override,
		      muted = EXCLUDED.muted,
		      mode = EXCLUDED.mode,
		      updated_at = now(),
		      idempotency_key = EXCLUDED.idempotency_key,
		      seq = nextval(pg_get_serial_sequence('msg_notification_prefs', 'seq'))
		RETURNING id::text, seq`,
		p.ChannelID, p.UserID, p.DesktopEnabled, p.EscalationOverride, p.Muted, p.Mode, key).
		Scan(&id, &seq)
	if err != nil {
		return "", 0, fmt.Errorf("sync prefs write: %w", err)
	}
	return id, seq, nil
}

// bumpChannelSeq draws a fresh channels-feed seq for one room, the tell a
// membership change owes every device that caches the room.
func (r *Repository) bumpChannelSeq(ctx context.Context, channelID string) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE msg_channels SET seq = nextval(pg_get_serial_sequence('msg_channels', 'seq'))
		 WHERE id = $1`, channelID); err != nil {
		return fmt.Errorf("bump channel seq: %w", err)
	}
	return nil
}

// - Feature B: retention archival (0070) -
// ChannelRetentions returns every live channel with its EFFECTIVE hot-retention window in months:
// per-channel policy override, else the global default row, else defaultRetentionMonths. The retention
// worker turns each (channel, months) into a per-channel cutoff (retentionCutoff) and archives past it.
//
// LIVE-DB: exercised under the integration tag; needs a migrated Postgres. The worker's selection loop
// + the pure cutoff math are unit-tested DB-free against a fake retentionStore.
func (r *Repository) ChannelRetentions(ctx context.Context) ([]ChannelRetention, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ch.id::text, COALESCE(pc.hot_retention_months, pg.hot_retention_months, $1)
		FROM msg_channels ch
		LEFT JOIN msg_retention_policy pc ON pc.channel_id = ch.id
		LEFT JOIN msg_retention_policy pg ON pg.channel_id IS NULL
		WHERE ch.is_archived = FALSE`, defaultRetentionMonths)
	if err != nil {
		return nil, fmt.Errorf("channel retentions: %w", err)
	}
	defer rows.Close()
	var out []ChannelRetention
	for rows.Next() {
		var cr ChannelRetention
		if err := rows.Scan(&cr.ChannelID, &cr.Months); err != nil {
			return nil, fmt.Errorf("scan retention: %w", err)
		}
		out = append(out, cr)
	}
	return out, rows.Err()
}

// ArchiveChannelOlderThan archives (summarizes, NEVER deletes) one channel's messages older than
// `cutoff`. It is idempotent two ways: it only covers the DELTA window since the channel's last
// archived_through (so a message is summarized at most once across runs), and re-inserting the same
// (channel, archived_through) boundary conflicts on uq_msg_message_archive_window and is a no-op.
// Returns the number of archive rows written (0 when nothing aged out since the last run).
//
// LIVE-DB: exercised under the integration tag; needs a migrated Postgres to verify.
func (r *Repository) ArchiveChannelOlderThan(ctx context.Context, channelID string, cutoff time.Time) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	ct, err := r.pool.Exec(ctx, `
		WITH last_arch AS (
		  SELECT max(archived_through) AS through FROM msg_message_archive WHERE channel_id = $1::uuid
		),
		aged AS (
		  SELECT count(*) AS message_count, min(m.created_at) AS oldest, max(m.created_at) AS newest
		  FROM msg_messages m, last_arch la
		  WHERE m.channel_id = $1::uuid
		    AND m.created_at <  $2::timestamptz
		    AND m.created_at >= COALESCE(la.through, to_timestamp(0))
		)
		INSERT INTO msg_message_archive (channel_id, archived_through, summary, message_count)
		SELECT $1::uuid, $2::timestamptz,
		       jsonb_build_object('oldest', a.oldest, 'newest', a.newest, 'message_count', a.message_count),
		       a.message_count
		FROM aged a
		WHERE a.message_count > 0
		ON CONFLICT (channel_id, archived_through) DO NOTHING`, channelID, cutoff)
	if err != nil {
		return 0, fmt.Errorf("archive channel: %w", err)
	}
	return int(ct.RowsAffected()), nil
}
