package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
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

	// afterPostBegin is a TEST SEAM: it runs inside a post's own transaction, right after the
	// transaction opens and BEFORE the channel's lock is taken. A test sets it to hold the post
	// open across a read, which is the race the channel's lock settles (UX-110): a post whose
	// write lands after the read's cursor must light the badge, and a post whose write lands
	// before it must not. It is nil in production, where it costs one branch.
	afterPostBegin func(ctx context.Context)
}

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// syncQualifies is the derived-membership predicate for one channel `ch` and one user. Bind order is
// FIXED so the seat, the prune and the room set share ONE definition: $1 = userID, $2 = isGlobal
// (bool).
//   - GLOBAL auto_scope: any ACTIVE staff;
//   - BRANCH auto_scope: the user is GLOBAL-scoped (reaches every branch) OR holds a grant for
//     ch.branch_id (mirrors StaffContext.CanAccessBranch);
//   - auto_role narrows to a role code ('*'/NULL = any role);
//   - member_roles (the money rooms, 0390) narrows to a list of role codes, NULL = no list.
const syncQualifies = `
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
		    )
		AND (
		      ch.member_roles IS NULL
		   OR EXISTS (
		         SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		         WHERE ur.user_id = $1 AND ro.code::text = ANY(ch.member_roles))
		    )`

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

	// The rooms this sync will write, read BEFORE any write. The channel's
	// lock comes before the member row everywhere in this module (UX-110
	// round 2: one lock order everywhere, the channel then the member), so
	// the sync has to know which rooms it touches before it touches one: it
	// reads the set, takes each room's lock in ONE order, and only then
	// writes the member rows.
	//
	// A room the sync will not write is not in the set and is not locked, so
	// a sync that changes nothing holds no lock at all.
	rooms, err := syncRooms(ctx, tx, userID, isGlobal, status == "ACTIVE")
	if err != nil {
		return err
	}
	sort.Strings(rooms)

	// The locks, one per room, in the room id ascending order the bumps
	// below take. The mode is the one a write takes (FOR NO KEY UPDATE):
	// the sync bumps every room it changes, and the bump asks for the very
	// lock this holds, so nothing here ever has to upgrade it (an upgrade is
	// how two syncs that cross deadlock against each other).
	locked := make([]string, 0, len(rooms))
	for _, room := range rooms {
		if err := lockChannelForWrite(ctx, tx, room); err != nil {
			if errors.Is(err, errNotFound) {
				// The room went while this sync was reading the set. It is
				// not this sync's room to write, and a write to a room whose
				// lock this transaction does not hold is the one thing the
				// order above forbids.
				continue
			}
			return err
		}
		locked = append(locked, room)
	}

	// Insert the derived rows the user qualifies for. A non-ACTIVE user qualifies for nothing (the
	// Go-side status guard) so it is skipped entirely. RETURNING hands back only the rooms that
	// gained a row (the conflict arm answers nothing), and each of them owes the channels feed the
	// same announce the explicit seat owes: a device holding a cursor past the room's stamped seq
	// sees the room arrive. The bumps ride the sync's own transaction, so the seats and their
	// announce commit together or not at all.
	//
	// The seats are written only in the rooms this transaction holds the
	// lock for (the set read above), so every member row it writes belongs
	// to a room whose lock it holds.
	var changed []string
	if status == "ACTIVE" && len(locked) > 0 {
		rows, err := tx.Query(ctx, `
			INSERT INTO msg_members (channel_id, user_id, role, source)
			SELECT ch.id, $1, 'MEMBER', 'DERIVED'
			FROM msg_channels ch
			WHERE `+syncQualifies+`
			  AND ch.id::text = ANY($3::text[])
			ON CONFLICT (channel_id, user_id) DO NOTHING
			RETURNING channel_id`, userID, isGlobal, locked)
		if err != nil {
			return fmt.Errorf("insert derived members: %w", err)
		}
		var seated []string
		for rows.Next() {
			var ch string
			if err := rows.Scan(&ch); err != nil {
				rows.Close()
				return fmt.Errorf("scan seated channel: %w", err)
			}
			seated = append(seated, ch)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("insert derived members: %w", err)
		}
		changed = append(changed, seated...)
	}

	// Delete DERIVED rows the user no longer qualifies for (role/branch change, deactivation, or
	// channel archival). EXPLICIT rows are untouched (source filter). An ACTIVE user is re-tested
	// against `syncQualifies`; a non-ACTIVE user qualifies for NOTHING, so ALL their derived rows are
	// pruned (the branch below avoids binding the now-unused $2). Every pruned room owes the feed
	// the same refresh bump a removal owes, on the same transaction.
	//
	// The prunes are written only in the rooms this transaction holds the
	// lock for, so every member row it writes belongs to a room whose lock
	// it holds.
	if status == "ACTIVE" && len(locked) > 0 {
		rows, err := tx.Query(ctx, `
			DELETE FROM msg_members m
			WHERE m.user_id = $1
			  AND m.source = 'DERIVED'
			  AND m.channel_id::text = ANY($3::text[])
			  AND NOT EXISTS (
			        SELECT 1 FROM msg_channels ch
			        WHERE ch.id = m.channel_id
			          AND `+syncQualifies+`
			      )
			RETURNING m.channel_id`, userID, isGlobal, locked)
		if err != nil {
			return fmt.Errorf("prune derived members: %w", err)
		}
		var pruned []string
		for rows.Next() {
			var ch string
			if err := rows.Scan(&ch); err != nil {
				rows.Close()
				return fmt.Errorf("scan pruned channel: %w", err)
			}
			pruned = append(pruned, ch)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("prune derived members: %w", err)
		}
		changed = append(changed, pruned...)
	} else if len(locked) > 0 {
		rows, err := tx.Query(ctx,
			`DELETE FROM msg_members WHERE user_id = $1 AND source = 'DERIVED'
			  AND channel_id::text = ANY($2::text[])
			 RETURNING channel_id`, userID, locked)
		if err != nil {
			return fmt.Errorf("prune derived members (inactive): %w", err)
		}
		var pruned []string
		for rows.Next() {
			var ch string
			if err := rows.Scan(&ch); err != nil {
				rows.Close()
				return fmt.Errorf("scan pruned channel: %w", err)
			}
			pruned = append(pruned, ch)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("prune derived members (inactive): %w", err)
		}
		changed = append(changed, pruned...)
	}

	// A room carrying a member_roles list is a fence for EVERY seat source: an EXPLICIT row the
	// user holds there, given while they held one of the roles (or before the list existed), goes
	// when they hold none of them now. It owes the feed the same bump as any other prune.
	if status == "ACTIVE" && len(locked) > 0 {
		rows, err := tx.Query(ctx, `
			DELETE FROM msg_members m
			USING msg_channels ch
			WHERE m.user_id = $1
			  AND m.source = 'EXPLICIT'
			  AND ch.id = m.channel_id
			  AND m.channel_id::text = ANY($2::text[])
			  AND ch.member_roles IS NOT NULL
			  AND NOT EXISTS (
			        SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
			        WHERE ur.user_id = $1 AND ro.code::text = ANY(ch.member_roles))
			RETURNING m.channel_id`, userID, locked)
		if err != nil {
			return fmt.Errorf("prune fenced explicit members: %w", err)
		}
		var pruned []string
		for rows.Next() {
			var ch string
			if err := rows.Scan(&ch); err != nil {
				rows.Close()
				return fmt.Errorf("scan pruned channel: %w", err)
			}
			pruned = append(pruned, ch)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("prune fenced explicit members: %w", err)
		}
		changed = append(changed, pruned...)
	}

	// Each changed room owes the feed one bump, and each bump takes the room's row lock. The ids are
	// bumped in ONE sorted order across seats and prunes alike, so two syncs that cross (one gaining
	// room A and losing room B while another gains B and loses A) take their locks in the same order
	// and cannot deadlock: a deadlock would fail the ListMyChannels read that runs the sync.
	//
	// The order is the order the locks above were taken in, so the sync never takes a room's row
	// lock after another room's, in any statement.
	sort.Strings(changed)
	for i, ch := range changed {
		if i > 0 && ch == changed[i-1] {
			continue
		}
		if err := r.bumpChannelSeq(ctx, tx, ch); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// syncRooms answers the rooms one sync will write for one user: the rooms the
// user qualifies for and is not in yet (the seats), the rooms whose derived
// row the user no longer qualifies for (the prunes) and the rooms whose
// explicit row a member_roles fence takes away (the fenced prunes).
//
// The set is read BEFORE any write, because the sync takes each room's lock
// before it touches a member row (UX-110 round 2: one lock order everywhere,
// the channel then the member). A room the sync will not write is not in the
// set and is not locked, so a sync that changes nothing holds no lock at all.
//
// The set is the rooms the sync's own writes will land in, so the writes can
// be narrowed to it: a room that qualifies while the sync runs is seated by
// the next sync, and a room that stops qualifying while the sync runs is
// pruned by the next one.
func syncRooms(ctx context.Context, tx pgx.Tx, userID string, isGlobal, active bool) ([]string, error) {
	var parts []string
	var args []any
	if active {
		args = []any{userID, isGlobal}
		parts = append(parts, `
			SELECT ch.id::text
			FROM msg_channels ch
			WHERE `+syncQualifies+`
			  AND NOT EXISTS (SELECT 1 FROM msg_members mem
			                  WHERE mem.channel_id = ch.id AND mem.user_id = $1)`)
		parts = append(parts, `
			SELECT m.channel_id::text
			FROM msg_members m
			WHERE m.user_id = $1 AND m.source = 'DERIVED'
			  AND NOT EXISTS (SELECT 1 FROM msg_channels ch
			                  WHERE ch.id = m.channel_id AND `+syncQualifies+`)`)
		parts = append(parts, `
			SELECT m.channel_id::text
			FROM msg_members m
			JOIN msg_channels ch ON ch.id = m.channel_id
			WHERE m.user_id = $1 AND m.source = 'EXPLICIT'
			  AND ch.member_roles IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
			                  WHERE ur.user_id = $1 AND ro.code::text = ANY(ch.member_roles))`)
	} else {
		// A non-ACTIVE user qualifies for nothing, so every derived row of
		// theirs goes.
		args = []any{userID}
		parts = append(parts, `
			SELECT m.channel_id::text
			FROM msg_members m
			WHERE m.user_id = $1 AND m.source = 'DERIVED'`)
	}
	rows, err := tx.Query(ctx, strings.Join(parts, "\n\t\tUNION\n"), args...)
	if err != nil {
		return nil, fmt.Errorf("read the sync's rooms: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan the sync's room: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the sync's rooms: %w", err)
	}
	return out, nil
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
		       ch.name, ch.topic, ch.tier, ch.visibility, ch.is_archived, ch.created_at,
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
			&c.Visibility, &c.IsArchived, &c.CreatedAt, &c.UnreadCount, &c.Mentioned, &latest); err != nil {
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
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	return r.populateMessageAttachments(ctx, msgs)
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
	msgs, err = r.populateMessageAttachments(ctx, msgs)
	if err != nil {
		return MessagePage{}, err
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
// The mention parse (change 7, slice S3, ruling 5) runs here, inside the insert's own transaction,
// so EVERY person post through this path stores its mentions whether it arrived over REST or was
// replayed by a test: the message row and its mention rows commit together or not at all.
func (r *Repository) InsertMessage(ctx context.Context, channelID, userID, body, threadRootID string) (Message, error) {
	if r.pool == nil {
		return Message{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The test seam, before anything else in this transaction (UX-110). A test holds the post
	// open here, across a read, and sees the race the channel's lock settles.
	if r.afterPostBegin != nil {
		r.afterPostBegin(ctx)
	}
	// The channel's lock, BEFORE the post stamps itself, so a read of this
	// channel is ordered against this post by the lock and not by a clock
	// (UX-110). It is held until this transaction commits.
	if err := lockChannelForPost(ctx, tx, channelID); err != nil {
		return Message{}, err
	}
	var rootArg any
	if threadRootID != "" {
		// The root must exist, be in THIS channel, and itself be a top-level message (one-level
		// threads in P1). There is NO re-rooting: the predicate requires thread_root_id IS NULL, so
		// passing a REPLY's id as the root fails the EXISTS check and is rejected as not-found
		// (errNotFound → HTTP 404), a reply can never itself become a thread root. This same check
		// also rejects a cross-channel root as a hard integrity guard.
		var ok bool
		if err := tx.QueryRow(ctx,
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
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, thread_root_id, created_at)
		VALUES ($1, $2, 'TEXT', $3, $4, clock_timestamp())
		RETURNING id::text, channel_id::text, author_id::text, kind, body, COALESCE(thread_root_id::text,''), created_at`,
		channelID, userID, body, rootArg).Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.Kind, &m.Body, &m.ThreadRootID, &m.CreatedAt)
	if err != nil {
		return Message{}, fmt.Errorf("insert message: %w", err)
	}
	men, err := parseBodyMentions(ctx, tx, channelID, body)
	if err != nil {
		return Message{}, err
	}
	if err := storeMentions(ctx, tx, m.ID, men); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, err
	}
	// Resolve the author's display name for the immediate echo (the stream shows it).
	_ = r.pool.QueryRow(ctx, `SELECT COALESCE(display_name,'') FROM users WHERE id = $1`, userID).Scan(&m.AuthorName)
	return m, nil
}

// MarkRead advances the caller's read cursor for a channel to the DATABASE's clock, which zeroes the
// channel's unread count via the msg_unread_summary view.
//
// The cursor is the database's own clock, in the same statement that writes it, because it is compared
// against message rows the DATABASE stamps (msg_messages.created_at). A cursor taken from the app
// server's clock puts a second clock in that comparison: when the app's clock runs ahead of the
// database's, a message posted in the gap carries a created_at BEFORE the cursor, reads as already
// read, and the badge never lights for it (UX-109).
//
// The cursor is also taken under the channel's LOCK, because a clock alone does not order a post
// against a read (UX-110). A post that is still in flight when the read lands is invisible to the
// cursor's clock, so the cursor could be stamped after the post's stamp and the post would read as
// already read. The lock settles it: the post takes the channel's lock before it stamps itself and
// holds it until it commits, and the read takes the SAME lock (FOR UPDATE) before it writes the
// cursor. A post that commits before the cursor is stamped before it. A post that comes after waits
// for the lock and is stamped after the cursor.
//
// The membership answer comes FIRST, and it comes from the member row alone, so a non-member learns
// nothing about the channel's existence: the read never touches the channel's row for a caller who
// is not a member. It is a PLAIN read, so it takes no lock of its own, and the membership is
// re-checked in the UPDATE's own WHERE with the same predicate, so a member row that went while
// the read waited for the channel's lock is not written as if it were still there. Zero rows
// updated is still forbidden.
//
// The channel's lock comes before the member row, which is the order every path in this module
// takes (UX-110 round 2: one lock order everywhere, the channel then the member). The read, a
// seat and a remove all want the same two rows of one room, and two of them taking the two tables
// in opposite orders is a deadlock the database detects and the user pays for as a 500.
//
// It never moves the cursor backwards: GREATEST keeps the later of the two stamps, so a cursor is
// never pulled back to an earlier point.
func (r *Repository) MarkRead(ctx context.Context, channelID, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The membership answer, and it is a PLAIN read: no lock, no row lock.
	// A caller who is not a member of this room is refused in words here,
	// before any lock is taken, so a non-member learns nothing about the
	// room's existence and never holds the room's lock.
	var one int
	err = tx.QueryRow(ctx, `
		SELECT 1 FROM msg_members
		WHERE channel_id = $1::uuid AND user_id = $2::uuid`, channelID, userID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return errForbidden
	}
	if err != nil {
		return fmt.Errorf("mark read membership: %w", err)
	}

	// The channel's lock: the read waits for the posts in flight. It is the
	// read's own exclusive lock (FOR UPDATE), so it conflicts with the FOR
	// SHARE a post holds, and the posts do not conflict with each other.
	//
	// The CHANNEL comes before the member row, which is the order every
	// path in this module takes (UX-110 round 2: one lock order everywhere,
	// the channel then the member). A read that took the member row first
	// and the channel second would be the inverse of a seat or a remove,
	// and the two would close a cycle on one member row of one room.
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM msg_channels WHERE id = $1::uuid FOR UPDATE`, channelID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return errForbidden
	}
	if err != nil {
		return fmt.Errorf("mark read lock: %w", err)
	}

	// The cursor, written under the lock that orders it. clock_timestamp()
	// and not now(): now() is the clock this transaction began at, which is
	// BEFORE the lock was granted, and a cursor stamped before the posts it
	// waited for would call them read.
	//
	// The membership is re-checked in the UPDATE's own WHERE, with the same
	// predicate the read above used: a member row deleted while this waited
	// for the channel's lock must not be written as if it were still there,
	// and zero rows is still forbidden.
	ct, err := tx.Exec(ctx, `
		UPDATE msg_members SET last_read_at = GREATEST(last_read_at, clock_timestamp())
		WHERE channel_id = $1::uuid AND user_id = $2::uuid`, channelID, userID)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return errForbidden
	}
	if err := tx.Commit(ctx); err != nil {
		return err
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
//
// The write runs in a transaction that takes the channel's lock first, so a read of this channel is
// ordered against this post by the lock and not by a clock (UX-110). The post stamps itself with the
// clock it holds the lock at.
func (r *Repository) InsertEventMessage(ctx context.Context, channelID, eventKey, body string, ref EventRef, requiresAck bool) (Message, bool, error) {
	if r.pool == nil {
		return Message{}, false, errNoDB
	}
	refBytes, err := json.Marshal(ref)
	if err != nil {
		return Message{}, false, fmt.Errorf("marshal ref: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Message{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The channel's lock, BEFORE the post stamps itself (UX-110).
	if err := lockChannelForPost(ctx, tx, channelID); err != nil {
		return Message{}, false, err
	}
	var m Message
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, ref, event_key, requires_ack, created_at)
		VALUES ($1, NULL, 'EVENT', $2, $3, $4, $5, clock_timestamp())
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
	if err := tx.Commit(ctx); err != nil {
		return Message{}, false, err
	}
	return m, true, nil
}

// - Approvals rail (0075) -
// EnsureApprovalChannel lazily + idempotently provisions the caller-approver's dedicated APPROVAL
// channel (slug approvals-<username>, sole member the approver) and returns its id. Idempotent two ways:
// the channel converges on its unique slug (a second call returns the same id), and the member row upserts
// on the (channel_id, user_id) PK. The member is EXPLICIT so the FR-1 derived-membership prune never
// garbage-collects it. A row of another kind already holding the slug (a squat the founding walls
// refuse but a hand written row can still plant) is NEVER adopted: the provision refuses loudly
// rather than seating the approver OWNER in a room they do not own. Returns the channel id.
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
	// DO NOTHING (not DO UPDATE): RETURNING yields the id on the fresh arm
	// alone, and the conflict arm below decides whether the row this
	// provision converges on is its own (idempotent re-run) or a squat.
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, topic, tier)
		VALUES ('APPROVAL', $1, $2, 'Your approval requests', 'STANDARD')
		ON CONFLICT (slug) DO NOTHING
		RETURNING id::text`, slug, "Approvals, "+handle).Scan(&channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		var kind string
		kerr := tx.QueryRow(ctx,
			`SELECT kind FROM msg_channels WHERE slug = $1`, slug).Scan(&kind)
		if errors.Is(kerr, pgx.ErrNoRows) {
			return "", errNotFound // the conflicting row vanished mid-flight
		}
		if kerr != nil {
			return "", fmt.Errorf("load conflicting approval channel: %w", kerr)
		}
		if kind != KindApproval {
			// The slug embeds this approver's own user id and this
			// provision is its only rightful writer: a row of another
			// kind is a squat, and seating the approver into it would
			// hand the squatter the room's cards and the OWNER pen.
			return "", fmt.Errorf("ensure approval channel: %w", ErrChannelSlugTaken)
		}
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM msg_channels WHERE slug = $1`, slug).Scan(&channelID); err != nil {
			return "", fmt.Errorf("load approval channel: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("ensure approval channel: %w", err)
	}
	// The channel's lock, BEFORE the member row this path writes (UX-110
	// round 2: the channel, then the member). On the fresh arm the room is
	// this transaction's own row; on the conflict arm it is a row that is
	// already there, and the lock is what keeps this path from writing a
	// member row of a room whose lock it does not hold. This path writes no
	// msg_channels row of its own, so the shared lock is the one a post
	// takes (FOR SHARE).
	if err := lockChannelForPost(ctx, tx, channelID); err != nil {
		return "", err
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
//
// Like InsertEventMessage, the write takes the channel's lock before it stamps itself, so a read of
// this channel is ordered against this card by the lock and not by a clock (UX-110).
func (r *Repository) PostApprovalMessage(ctx context.Context, channelID, eventKey, body string, ref ApprovalRef) (Message, bool, error) {
	if r.pool == nil {
		return Message{}, false, errNoDB
	}
	refBytes, err := json.Marshal(ref)
	if err != nil {
		return Message{}, false, fmt.Errorf("marshal approval ref: %w", err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Message{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The channel's lock, BEFORE the card stamps itself (UX-110).
	if err := lockChannelForPost(ctx, tx, channelID); err != nil {
		return Message{}, false, err
	}
	var m Message
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, ref, event_key, created_at)
		VALUES ($1, NULL, 'APPROVAL', $2, $3, $4, clock_timestamp())
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
	if err := tx.Commit(ctx); err != nil {
		return Message{}, false, err
	}
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

// UserRoleCode reads one role code for one user, the first active role row
// (a hold on the role table scopes it, the same way the SyncMembership derived
// rule reads it; a user who has no active role reads back an empty code). The
// PR 302 N2 yard notice fence reads this to ask SeesCostAndMargin for the
// rep's own role, the same X3 rule cost_gate already asks everywhere. The
// returned code is the CANONICAL form (lowercase, the staff_role.id shape)
// not the ERP code (UPPERCASE): the rule vocabulary's SeesCostAndMargin
// answers against the canonical, the same conversion identity.bridge makes
// at the dispatch door. An empty answer is fail closed: the figures ride
// only when the rule says yes for a known role, and the brief's "figureless
// on error or no" stays a safety net on top of the empty-code path.
func (r *Repository) UserRoleCode(ctx context.Context, userID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var code string
	err := r.pool.QueryRow(ctx, `
		SELECT ro.code::text
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles ro ON ro.id = ur.role_id
		WHERE u.id = $1 AND u.status = 'ACTIVE'
		ORDER BY ur.created_at ASC
		LIMIT 1`, userID).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("user role code: %w", err)
	}
	return canonicalRole(code), nil
}

// canonicalRole turns the carried roles.code (an ERP code, UPPERCASE) into
// the rule vocabulary's canonical id (lowercase) through identity's own
// mapping, the one the dispatch fence's moneyAllowed reads, so this module
// keeps no table of its own. A code outside the vocabulary comes back as it
// was read, which the cost rule refuses (fail closed).
func canonicalRole(code string) string {
	if canonical, ok := identity.CanonicalForERPCode(code); ok {
		return canonical
	}
	return code
}

// - Reactions, mentions and the seen-by read (change 7, slice S3) -

// dbQuerier is the least both the pool and an open transaction satisfy, so the
// mention parse's reads and writes run inside whichever transaction the message
// insert is already committed to (the REST insert and the sync create each hold
// one; a bare pool read never needs this).
type dbQuerier interface {
	dbExecer
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// parseBodyMentions reads the mention candidates for one channel (its members
// by display name and e-mail local part, the house's active roles by code and
// name) and runs the parse (mentions.go, the syntax's one place). The users and
// roles reads are the same identity-side joins the derived-membership rule and
// the display-name resolutions in this file already run, read only. A failed
// candidate read fails the insert: a mention stored wrong is a badge that
// never rings, not a footnote.
func parseBodyMentions(ctx context.Context, q dbQuerier, channelID string, body string) (MessageMentions, error) {
	persons := []mentionPerson{}
	rows, err := q.Query(ctx, `
		SELECT mem.user_id::text, COALESCE(u.display_name,''), lower(split_part(u.email::text, '@', 1))
		FROM msg_members mem
		JOIN users u ON u.id = mem.user_id
		WHERE mem.channel_id = $1`, channelID)
	if err != nil {
		return MessageMentions{}, fmt.Errorf("mention candidates: %w", err)
	}
	for rows.Next() {
		var p mentionPerson
		if err := rows.Scan(&p.ID, &p.Name, &p.EmailLocal); err != nil {
			rows.Close()
			return MessageMentions{}, fmt.Errorf("scan mention candidate: %w", err)
		}
		persons = append(persons, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MessageMentions{}, fmt.Errorf("mention candidates: %w", err)
	}
	roles := []mentionRole{}
	rrows, err := q.Query(ctx, `SELECT code::text, name FROM roles WHERE is_active = TRUE`)
	if err != nil {
		return MessageMentions{}, fmt.Errorf("mention role candidates: %w", err)
	}
	for rrows.Next() {
		var ro mentionRole
		if err := rrows.Scan(&ro.Code, &ro.Name); err != nil {
			rrows.Close()
			return MessageMentions{}, fmt.Errorf("scan mention role: %w", err)
		}
		roles = append(roles, ro)
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return MessageMentions{}, fmt.Errorf("mention role candidates: %w", err)
	}
	return parseMentions(body, persons, roles), nil
}

// storeMentions writes one message's parsed mention rows. Empty on both sides
// (the ordinary post) writes nothing. The unique index is the idempotency
// fence; the insert is plain because the parse never repeats a target.
func storeMentions(ctx context.Context, q dbQuerier, messageID string, men MessageMentions) error {
	if len(men.UserIDs) == 0 && len(men.RoleCodes) == 0 {
		return nil
	}
	users := men.UserIDs
	if users == nil {
		users = []string{}
	}
	roleCodes := men.RoleCodes
	if roleCodes == nil {
		roleCodes = []string{}
	}
	_, err := q.Exec(ctx, `
		INSERT INTO msg_mentions (message_id, user_id, role_code)
		SELECT $1::uuid, u.uid::uuid, NULL::citext FROM unnest($2::text[]) AS u(uid)
		UNION ALL
		SELECT $1::uuid, NULL::uuid, c.code::citext FROM unnest($3::text[]) AS c(code)
		ON CONFLICT (message_id, user_id, role_code) DO NOTHING`,
		messageID, users, roleCodes)
	if err != nil {
		return fmt.Errorf("store mentions: %w", err)
	}
	return nil
}

// messageFactsRow is the reaction and seen-by walls' read of one message: its
// kind (ruling 6: a reaction is none on EVENT, approval or other system
// messages) and its author (the seen-by count excepts them).
type messageFactsRow struct {
	Kind      string
	AuthorID  string
	CreatedAt time.Time
}

// messageFacts reads one message's kind, author and created_at, bound to the
// channel the caller named. A message absent, or present in ANOTHER channel,
// is errNotFound either way (indistinguishable, so no cross-channel existence
// leaks), the same channel-binding guard ListThread and AckMessage carry.
func (r *Repository) messageFacts(ctx context.Context, channelID, messageID string) (messageFactsRow, error) {
	if r.pool == nil {
		return messageFactsRow{}, errNoDB
	}
	var f messageFactsRow
	err := r.pool.QueryRow(ctx, `
		SELECT kind, COALESCE(author_id::text,''), created_at
		FROM msg_messages WHERE id = $1 AND channel_id = $2`,
		messageID, channelID).
		Scan(&f.Kind, &f.AuthorID, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return messageFactsRow{}, errNotFound
	}
	if err != nil {
		return messageFactsRow{}, fmt.Errorf("message facts: %w", err)
	}
	return f, nil
}

// AddReaction seats one (message, person, emoji) row. The INSERT's own
// predicate is the fence's second wall: the source row exists only for a TEXT
// message in THIS channel, so even a caller that skipped the service's kind
// check writes nothing on a system message (ruling 6). Idempotent on the
// primary key: a replayed add lands once and changes nothing.
func (r *Repository) AddReaction(ctx context.Context, channelID, messageID, userID, emoji string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO msg_reactions (message_id, user_id, emoji)
		SELECT m.id, $2, $3 FROM msg_messages m
		WHERE m.id = $1 AND m.channel_id = $4 AND m.kind = 'TEXT'
		ON CONFLICT (message_id, user_id, emoji) DO NOTHING`,
		messageID, userID, emoji, channelID)
	if err != nil {
		return fmt.Errorf("add reaction: %w", err)
	}
	return nil
}

// RemoveReaction drops the caller's one emoji on one message, fenced to a TEXT
// message in THIS channel the same way the add is (ruling 6 reads both ways: a
// reaction the add refuses is one the delete cannot remove). Idempotent:
// removing what is not there removes nothing and reports nothing.
func (r *Repository) RemoveReaction(ctx context.Context, channelID, messageID, userID, emoji string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.pool.Exec(ctx, `
		DELETE FROM msg_reactions re
		USING msg_messages m
		WHERE re.message_id = m.id AND m.id = $1 AND m.channel_id = $2 AND m.kind = 'TEXT'
		  AND re.user_id = $3 AND re.emoji = $4`,
		messageID, channelID, userID, emoji)
	if err != nil {
		return fmt.Errorf("remove reaction: %w", err)
	}
	return nil
}

// reactionsForMessages answers the reaction summary for a page of messages in
// one round trip: per (message, emoji), the count, whether the READER is among
// the reactors, ordered by the emoji's first reaction so the chips render in
// the order they were given. The map holds a slice per reacted message; a
// message with no reactions is absent, and the stitcher leaves its Reactions
// nil so the payload keeps its pre-feature shape.
func (r *Repository) reactionsForMessages(ctx context.Context, messageIDs []string, userID string) (map[string][]ReactionCount, error) {
	out := map[string][]ReactionCount{}
	if r.pool == nil {
		return out, errNoDB
	}
	if len(messageIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT re.message_id::text, re.emoji, COUNT(*)::int, BOOL_OR(re.user_id = $2::uuid)
		FROM msg_reactions re
		WHERE re.message_id = ANY($1::uuid[])
		GROUP BY re.message_id, re.emoji
		ORDER BY re.message_id, MIN(re.created_at) ASC`,
		messageIDs, userID)
	if err != nil {
		return nil, fmt.Errorf("reactions for messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var messageID, emoji string
		var count int
		var mine bool
		if err := rows.Scan(&messageID, &emoji, &count, &mine); err != nil {
			return nil, fmt.Errorf("scan reaction: %w", err)
		}
		out[messageID] = append(out[messageID], ReactionCount{Emoji: emoji, Count: count, Mine: mine})
	}
	return out, rows.Err()
}

// maxSeenByReaders caps the seen-by readers list the report carries; the count
// above it stays exact whatever the cap does (the board draws "Seen 3", the
// hover names the readers, and a branch room's whole roster is a list, not a
// page).
const maxSeenByReaders = 50

// MessageSeenBy answers the board's "Seen N" for one message: the room's
// members whose read cursor stands at or past the message's created_at, the
// author excepted (a person is never seen by themselves). Membership of the
// room is the read's whole world, so no room but the caller's named one is
// consulted; the author id arrives from messageFacts, empty for a system
// message (everyone past it counts).
func (r *Repository) MessageSeenBy(ctx context.Context, channelID, authorID string, at time.Time) (SeenByReport, error) {
	if r.pool == nil {
		return SeenByReport{}, errNoDB
	}
	var authorArg any
	if authorID != "" {
		authorArg = authorID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT mem.user_id::text, COALESCE(u.display_name,'')
		FROM msg_members mem
		LEFT JOIN users u ON u.id = mem.user_id
		WHERE mem.channel_id = $1 AND mem.last_read_at >= $2
		  AND ($3::uuid IS NULL OR mem.user_id <> $3::uuid)
		ORDER BY mem.last_read_at DESC, mem.user_id ASC`,
		channelID, at, authorArg)
	if err != nil {
		return SeenByReport{}, fmt.Errorf("message seen-by: %w", err)
	}
	defer rows.Close()
	out := SeenByReport{Readers: []SeenByReader{}}
	for rows.Next() {
		var reader SeenByReader
		if err := rows.Scan(&reader.UserID, &reader.DisplayName); err != nil {
			return SeenByReport{}, fmt.Errorf("scan seen-by reader: %w", err)
		}
		out.Count++
		if len(out.Readers) < maxSeenByReaders {
			out.Readers = append(out.Readers, reader)
		}
	}
	return out, rows.Err()
}

// ListMentions is the mentions view (ruling 5): every message in a room the
// caller belongs to that names the caller, by person or by one of the caller's
// own role codes, newest first, cursor paginated by the same (created_at, id)
// keyset the message stream uses. Deleted messages never appear. Membership is
// intrinsic to the read (the join), the same fence the feeds pull through.
func (r *Repository) ListMentions(ctx context.Context, userID, cursor string, limit int) (MessagePage, error) {
	if r.pool == nil {
		return MessagePage{}, errNoDB
	}
	args := []any{userID, limit + 1}
	cursorClause := ""
	if cursor != "" {
		ts, id, err := decodeCursor(cursor)
		if err != nil {
			return MessagePage{}, fmt.Errorf("bad cursor: %w", err)
		}
		cursorClause = " AND (m.created_at, m.id) < ($3::timestamptz, $4::uuid)"
		args = append(args, ts, id)
	}
	q := `
		SELECT m.id::text, m.channel_id::text, COALESCE(m.author_id::text,''), COALESCE(u.display_name,''),
		       m.kind, m.body, m.ref, COALESCE(m.thread_root_id::text,''), 0,
		       m.edited_at, m.deleted_at, m.created_at
		FROM msg_messages m
		JOIN msg_members mem ON mem.channel_id = m.channel_id AND mem.user_id = $1
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.deleted_at IS NULL
		  AND EXISTS (
		        SELECT 1 FROM msg_mentions men
		        WHERE men.message_id = m.id
		          AND (men.user_id = $1
		            OR EXISTS (SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		                       WHERE ur.user_id = $1 AND ro.code = men.role_code)))` + cursorClause + `
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $2`
	return r.scanMessagePage(ctx, q, args, limit)
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
//
// Every system post and route by slug goes through here (the consumer's yard, house and payments
// posts, the task notifier's counter, yard and #office, the escalations mirror, the portal bridge,
// the order notice fallbacks), so the lookup resolves ONLY a system row: one carrying the auto_scope
// marker every seeded system room has and no caller founded room ever gets (no Go insert writes it).
// A caller room that squats a name the system derives from branch facts can then never receive a
// system post, whatever family the reservation list names. The marker is enough here, unlike the
// house pick, because a slug is unique: the lookup is by exact name, never "the first of a family".
func (r *Repository) ChannelIDBySlug(ctx context.Context, slug string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.pool.QueryRow(ctx, `SELECT id::text FROM msg_channels WHERE slug = $1 AND auto_scope IS NOT NULL AND is_archived = FALSE`, slug).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errNotFound
		}
		return "", fmt.Errorf("resolve slug: %w", err)
	}
	return id, nil
}

// BranchYardSlug returns the yard channel slug for a branch id (the FR-3 map: order/stock/delivery
// events for a branch land in that branch's #yard-<name> channel). The branch's own system yard room
// names it: the BRANCH kind row for that branch carrying the auto_scope marker (no caller founded
// room ever has one), so a dealer whose rooms are named after the city (yard-point-loma on a branch
// coded PLM) routes to them, and the house and payments slugs derived from this one follow. A branch
// with no such room falls back to its short code from locations, lowercased (a branch coded NWB is
// yard-nwb), with the two first seeded yards keeping their city names.
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
	var slug string
	err = r.pool.QueryRow(ctx, `
		SELECT slug::text FROM msg_channels
		WHERE branch_id = $1 AND kind = 'BRANCH' AND auto_scope IS NOT NULL AND is_archived = FALSE
		  AND slug LIKE 'yard-%'
		ORDER BY created_at, id LIMIT 1`, branchID).Scan(&slug)
	if err == nil {
		return slug, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("resolve branch yard room: %w", err)
	}
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
// visibility is the change 7 ruling 7 hand: a PUBLIC/PRIVATE flag every
// row reads the same way (the migration's DEFAULT 'PUBLIC' and CHECK
// (visibility IN ('PUBLIC','PRIVATE'))).
const channelCols = `id::text, kind, COALESCE(branch_id::text,''), COALESCE(slug::text,''),
	name, topic, tier, visibility, is_archived, created_at`

func scanChannel(row pgx.Row) (Channel, error) {
	var c Channel
	if err := row.Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier,
		&c.Visibility, &c.IsArchived, &c.CreatedAt); err != nil {
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
// room reports the existing row (created false). On the conflict arm
// NOTHING is written: no seat (nobody is ever seated as owner of an
// existing channel), and not even the updated_at touch the old DO UPDATE
// arm made on a row the caller does not own. The room is reported as it
// stands and the SERVICE runs the walls (kind, branch, membership) that
// decide whether it is answered or refused; this method only converges.
// The founder is seated as OWNER, EXPLICIT, on the fresh arm alone, so the
// derived prune never drops them out of their own room.
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
	// Visibility defaults to PUBLIC: the migration's DEFAULT 'PUBLIC' would
	// land there on its own, but writing the value here keeps the channel's
	// own row the answer the create's RETURNING scans back (a row the
	// migration's default touched later, through a trigger, would still
	// answer correctly here, but the inserted value is the contract).
	visibility := strings.ToUpper(strings.TrimSpace(in.Visibility))
	if visibility == "" {
		visibility = VisibilityPublic
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Channel{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var c Channel
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, topic, tier, visibility, auto_scope, auto_role)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5, 'STANDARD', $6, NULLIF($7, ''), NULLIF($8, ''))
		ON CONFLICT (slug) DO NOTHING
		RETURNING `+channelCols,
		in.Kind, in.BranchID, in.Slug, in.Name, in.Topic, visibility, in.AutoScope, in.AutoRole).
		Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier,
			&c.Visibility, &c.IsArchived, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The slug already holds a room (a row returned by DO NOTHING is
		// always a fresh insert, so no row means the conflict arm). Report
		// the room as it stands; the walls live in the service.
		existing, cerr := scanChannel(tx.QueryRow(ctx,
			`SELECT `+channelCols+` FROM msg_channels WHERE slug = $1`, in.Slug))
		if cerr != nil {
			if errors.Is(cerr, pgx.ErrNoRows) {
				return Channel{}, false, errNotFound // the conflicting row vanished mid-flight
			}
			return Channel{}, false, fmt.Errorf("load conflicting channel: %w", cerr)
		}
		return existing, false, nil
	}
	if err != nil {
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
	return c, true, nil
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
// openings converge on one row). The conflict arm never touches the row it
// lands on (no updated_at write), refuses a slug squatted by a room of
// another kind, and seats only the pair the slug itself names.
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
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, slug, name, topic, tier, visibility)
		VALUES ('DM', $1, $2, '', 'STANDARD', 'PUBLIC')
		ON CONFLICT (slug) DO NOTHING
		RETURNING `+channelCols,
		dmPairSlug(aUserID, bUserID), aName+", "+bName).
		Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier,
			&c.Visibility, &c.IsArchived, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The pair's slug already holds a row (only a committed row can
		// hold it, so it is visible here). A room of another kind squatting
		// the DM namespace is refused, never converged on; a DM row is the
		// pair's own room and is answered with both parties reseated (the
		// seat upsert cannot name anyone the slug does not).
		existing, cerr := scanChannel(tx.QueryRow(ctx,
			`SELECT `+channelCols+` FROM msg_channels WHERE slug = $1`, dmPairSlug(aUserID, bUserID)))
		if cerr != nil {
			if errors.Is(cerr, pgx.ErrNoRows) {
				return Channel{}, false, errNotFound // the conflicting row vanished mid-flight
			}
			return Channel{}, false, fmt.Errorf("load pair dm by slug: %w", cerr)
		}
		if existing.Kind != KindDM {
			return Channel{}, false, ErrChannelSlugTaken
		}
		// The channel's lock, BEFORE the member rows this path writes (UX-110
		// round 2: the channel, then the member). The room is a row that is
		// already there, and this path writes no msg_channels row of its own,
		// so the shared lock is the one a post takes (FOR SHARE).
		if err := lockChannelForPost(ctx, tx, existing.ID); err != nil {
			return Channel{}, false, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO msg_members (channel_id, user_id, role, source)
			VALUES ($1, $2, 'OWNER', 'EXPLICIT'), ($1, $3, 'MEMBER', 'EXPLICIT')
			ON CONFLICT (channel_id, user_id) DO NOTHING`, existing.ID, aUserID, bUserID); err != nil {
			return Channel{}, false, fmt.Errorf("seat dm parties: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Channel{}, false, err
		}
		return existing, false, nil
	}
	if err != nil {
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
	return c, true, nil
}

// channelFactsRow is the membership walls' read of one room: its kind
// (DM and APPROVAL rooms have fixed membership), its branch, its
// auto_scope (a BRANCH-scoped room's membership belongs to the branch
// model, so a target it would not derive in is refused), and the room's
// visibility flag (rule 7's PRIVATE check refuses a self-join).
type channelFactsRow struct {
	Kind        string
	BranchID    string
	AutoScope   string
	Visibility  string
	MemberRoles []string
}

// channelFacts reads one room's kind, branch and auto_scope. An unknown
// room is not-found (the member wall has usually answered before this
// runs, but the read stands on its own).
func (r *Repository) channelFacts(ctx context.Context, channelID string) (channelFactsRow, error) {
	if r.pool == nil {
		return channelFactsRow{}, errNoDB
	}
	var f channelFactsRow
	err := r.pool.QueryRow(ctx, `
		SELECT kind, COALESCE(branch_id::text,''), COALESCE(auto_scope,''), visibility, COALESCE(member_roles, '{}')
		FROM msg_channels WHERE id = $1`, channelID).
		Scan(&f.Kind, &f.BranchID, &f.AutoScope, &f.Visibility, &f.MemberRoles)
	if errors.Is(err, pgx.ErrNoRows) {
		return channelFactsRow{}, errNotFound
	}
	if err != nil {
		return channelFactsRow{}, fmt.Errorf("channel facts: %w", err)
	}
	return f, nil
}

// memberRole reads one member row's role (the manager wall: the room's
// OWNER manages it). An absent row reads as an empty role; the member wall
// has already refused a caller with no row by the time this runs.
func (r *Repository) memberRole(ctx context.Context, channelID, userID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var role string
	err := r.pool.QueryRow(ctx,
		`SELECT role FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
		channelID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("member role: %w", err)
	}
	return role, nil
}

// memberSource reads one member row's source flag. The leave channel's
// "Joined by rule, you stay" wall answers on EXPLICIT vs DERIVED: a
// DERIVED row belongs to the role and branch model and is never
// hand-removable. An absent row reads as an empty source; the membership
// wall has already refused a caller with no row by the time this runs.
func (r *Repository) memberSource(ctx context.Context, channelID, userID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var source string
	err := r.pool.QueryRow(ctx,
		`SELECT source FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
		channelID, userID).Scan(&source)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("member source: %w", err)
	}
	return source, nil
}

// userReachesBranch reports whether one user reaches a branch: granted it,
// or GLOBAL-scoped through any role (the same facts SyncMembership's
// qualify predicate reads, mirroring StaffContext.CanAccessBranch). It is
// the add-target wall on rooms the branch model derives membership for.
func (r *Repository) userReachesBranch(ctx context.Context, userID, branchID string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var reaches bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM user_branch_grants g WHERE g.user_id = $1 AND g.branch_id = $2)
		    OR EXISTS(SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		              WHERE ur.user_id = $1 AND ro.scope = 'GLOBAL')`,
		userID, branchID).Scan(&reaches)
	if err != nil {
		return false, fmt.Errorf("user reaches branch: %w", err)
	}
	return reaches, nil
}

// userHoldsAnyRole reports whether one user holds at least one of the role codes: the add-target
// wall on a room whose member_roles list names the roles it may seat.
func (r *Repository) userHoldsAnyRole(ctx context.Context, userID string, codes []string) (bool, error) {
	if r.pool == nil {
		return false, errNoDB
	}
	var holds bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
		              WHERE ur.user_id = $1 AND ro.code::text = ANY($2))`,
		userID, codes).Scan(&holds)
	if err != nil {
		return false, fmt.Errorf("user holds role: %w", err)
	}
	return holds, nil
}

// AddChannelMember seats one staff member EXPLICIT in a channel
// (idempotent). An already-seated member answers the row AS IT STANDS: the
// insert's conflict arm writes nothing, so a DERIVED row is never
// rewritten to EXPLICIT (a rewritten row would become hand-removable,
// contradicting the DERIVED removal refusal), and an EXPLICIT row keeps
// the added_reason / added_by it already carries (a re-seat never overwrites
// the manager's audit trail). The unknown-user and inactive-user refusals
// run before anything is written.
//
// The added_reason / added_by columns are the change 7 ruling 7 hand: a
// fresh seat writes the manager's text reason and the user id who added
// them, and a re-seat reads them back whatever they are. A DERIVED row's
// read-back is the empty reason and NULL adder (the role and branch model
// owns the row).
func (r *Repository) AddChannelMember(ctx context.Context, in AddMemberInput) (ChannelMember, error) {
	if r.pool == nil {
		return ChannelMember{}, errNoDB
	}
	if err := r.userActive(ctx, in.UserID); err != nil {
		return ChannelMember{}, err
	}
	// The room's settings panel always writes MEMBER (an OWNER is the
	// room's founder and is not hand-assignable here), and the repository
	// never seats an OWNER. An empty role is normalised before the write:
	// the migration's CHECK (role IN ('MEMBER','OWNER')) would refuse the
	// empty string otherwise.
	if in.Role == "" {
		in.Role = RoleMember
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ChannelMember{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The channel's lock, BEFORE the member row this path writes. The path
	// writes msg_channels itself (the feed bump below), so it takes the
	// lock in the mode a write takes: FOR NO KEY UPDATE. The bump asks for
	// the very lock this holds and never has to upgrade it.
	//
	// The CHANNEL before the MEMBER is the one order this module takes
	// everywhere (UX-110 round 2). A path that wrote the member row first
	// and locked the channel second would be the inverse of a seat or a
	// remove, and the two would close a cycle on one member row of one
	// room: the database answers that cycle with 40P01 and the user sees a
	// 500.
	if err := lockChannelForWrite(ctx, tx, in.ChannelID); err != nil {
		return ChannelMember{}, err
	}
	var m ChannelMember
	var addedBy *string
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source, added_reason, added_by)
		VALUES ($1, $2, $3, 'EXPLICIT', $4, NULLIF($5,'')::uuid)
		ON CONFLICT (channel_id, user_id) DO NOTHING
		RETURNING channel_id::text, user_id::text, role, source, joined_at,
		          added_reason, added_by::text`,
		in.ChannelID, in.UserID, in.Role, in.AddedReason, in.AddedBy).
		Scan(&m.ChannelID, &m.UserID, &m.Role, &m.Source, &m.JoinedAt,
			&m.AddedReason, &addedBy)
	if addedBy != nil {
		m.AddedBy = *addedBy
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Already seated (and the fresh arm of a live caller is the only
		// other way to reach this point with no row): answer the row as it
		// stands, whatever its source. A DERIVED row carries no audit
		// fields; an EXPLICIT row carries whatever the manager wrote when
		// they first seated it.
		err = tx.QueryRow(ctx, `
			SELECT channel_id::text, user_id::text, role, source, joined_at,
			       added_reason, COALESCE(added_by::text,'')
			FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
			in.ChannelID, in.UserID).
			Scan(&m.ChannelID, &m.UserID, &m.Role, &m.Source, &m.JoinedAt,
				&m.AddedReason, &m.AddedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return ChannelMember{}, errNotFound // no such channel
		}
		if err != nil {
			return ChannelMember{}, fmt.Errorf("read seated member: %w", err)
		}
		return m, nil
	}
	if err != nil {
		return ChannelMember{}, fmt.Errorf("add channel member: %w", err)
	}
	// The fresh seat bumps the channel's feed seq INSIDE the seat's own
	// transaction: a device holding a cursor past the room's stamped seq
	// (every device that synced before this seat) must see the room arrive
	// through the channels feed, and the bump commits with the seat or not
	// at all. The read-back arm (an already-seated member) changed nothing
	// and bumps nothing.
	if err := r.bumpChannelSeq(ctx, tx, in.ChannelID); err != nil {
		return ChannelMember{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChannelMember{}, err
	}
	return m, nil
}

// SetChannelVisibility updates one room's visibility PUBLIC/PRIVATE. An
// invalid value is refused (the migration's CHECK is the canonical enum;
// the route handler that asked for it has already cleaned its input). The
// row and the visibility write share one transaction with the channel-feed
// bump: a device holding a cursor past the room's stamped seq must see the
// room's new visibility on its next channels feed, and the bump commits
// with the write or not at all.
func (r *Repository) SetChannelVisibility(ctx context.Context, channelID, visibility string) (Channel, error) {
	if r.pool == nil {
		return Channel{}, errNoDB
	}
	if visibility != VisibilityPublic && visibility != VisibilityPrivate {
		return Channel{}, ErrBadVisibility
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Channel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var c Channel
	err = tx.QueryRow(ctx, `
		UPDATE msg_channels SET visibility = $2 WHERE id = $1
		RETURNING `+channelCols, channelID, visibility).
		Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier,
			&c.Visibility, &c.IsArchived, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, errNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("set visibility: %w", err)
	}
	if err := r.bumpChannelSeq(ctx, tx, channelID); err != nil {
		return Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Channel{}, err
	}
	return c, nil
}

// ArchiveChannel flips one room's is_archived to true (a soft delete: the
// row stays, the channel leaves every list). An already-archived row
// answers ErrChannelArchived: the manager wall already wrote the truth,
// and a second archive is not a keep-state the route answers. The archive
// and its channel-feed bump share one transaction, mirroring the seat and
// the visibility write's discipline (a device holding a cursor past the
// room's stamped seq must see the room leave on its next sync, and the
// bump commits with the archive or not at all).
func (r *Repository) ArchiveChannel(ctx context.Context, channelID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The pre-update read answers whether the row was already archived
	// before this call: the UPDATE's RETURNING would be the fresh value
	// (always TRUE after the write), so the refusal needs the old one.
	var wasArchived bool
	if err := tx.QueryRow(ctx,
		`SELECT is_archived FROM msg_channels WHERE id = $1 FOR UPDATE`, channelID).
		Scan(&wasArchived); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return fmt.Errorf("archive channel precheck: %w", err)
	}
	if wasArchived {
		_ = tx.Rollback(ctx)
		return ErrChannelArchived
	}
	if _, err := tx.Exec(ctx,
		`UPDATE msg_channels SET is_archived = TRUE WHERE id = $1`, channelID); err != nil {
		return fmt.Errorf("archive channel: %w", err)
	}
	if err := r.bumpChannelSeq(ctx, tx, channelID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// - Group rooms (walk change 7, slice S2, rulings 3 and 4) -

// CreateGroup inserts one group room and seats its founding members in ONE
// transaction: the caller as OWNER and every named staffer as an explicit
// MEMBER, the feed bump committing with the room or not at all. There is no
// convergence arm: a group carries no slug (migration 0397's shape guard), so
// every call starts a fresh room.
func (r *Repository) CreateGroup(ctx context.Context, ownerUserID, name string, memberIDs []string) (Channel, error) {
	if r.pool == nil {
		return Channel{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Channel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var c Channel
	if err := tx.QueryRow(ctx, `
		INSERT INTO msg_channels (kind, name, topic, tier)
		VALUES ('GROUP', $1, '', 'STANDARD')
		RETURNING `+channelCols, name).
		Scan(&c.ID, &c.Kind, &c.BranchID, &c.Slug, &c.Name, &c.Topic, &c.Tier, &c.Visibility, &c.IsArchived, &c.CreatedAt); err != nil {
		return Channel{}, fmt.Errorf("create group: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'OWNER', 'EXPLICIT')
		ON CONFLICT DO NOTHING`, c.ID, ownerUserID); err != nil {
		return Channel{}, fmt.Errorf("seat the group's owner: %w", err)
	}
	for _, id := range memberIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO msg_members (channel_id, user_id, role, source)
			VALUES ($1, $2, 'MEMBER', 'EXPLICIT')
			ON CONFLICT DO NOTHING`, c.ID, id); err != nil {
			return Channel{}, fmt.Errorf("seat a group member: %w", err)
		}
	}
	if err := r.bumpChannelSeq(ctx, tx, c.ID); err != nil {
		return Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Channel{}, err
	}
	return c, nil
}

// SeatGroupMember seats one staffer in a group room and, on the fresh seat
// alone, writes the system line naming who was added and by whom, both in the
// seat's own transaction (the read-back arm answers the row as it stands,
// writes nothing and bumps nothing, the shared member-add seam's own shape).
// The active staff wall runs here as at every seat: an inactive staffer can
// be named but not seated, an id that names no staffer is not found.
func (r *Repository) SeatGroupMember(ctx context.Context, channelID, userID, adderName, targetName string) (ChannelMember, bool, error) {
	if r.pool == nil {
		return ChannelMember{}, false, errNoDB
	}
	if err := r.userActive(ctx, userID); err != nil {
		return ChannelMember{}, false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ChannelMember{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The channel's lock, BEFORE the seat and the line it writes, so a read of
	// this channel is ordered against that line by the lock and not by a clock
	// (UX-110). This path writes msg_channels itself (the feed bump below), so
	// it takes the lock in the mode a write takes: the bump asks for the very
	// lock this holds and never has to upgrade it, which is how two posts to
	// one channel would deadlock against each other.
	//
	// The CHANNEL before the MEMBER is the one order this module takes
	// everywhere (UX-110 round 2). A path that wrote the member row first
	// and locked the channel second would be the inverse of this one, and the
	// two would close a cycle on one member row of one room.
	if err := lockChannelForWrite(ctx, tx, channelID); err != nil {
		return ChannelMember{}, false, err
	}
	var m ChannelMember
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_members (channel_id, user_id, role, source)
		VALUES ($1, $2, 'MEMBER', 'EXPLICIT')
		ON CONFLICT (channel_id, user_id) DO NOTHING
		RETURNING channel_id::text, user_id::text, role, source, joined_at`,
		channelID, userID).
		Scan(&m.ChannelID, &m.UserID, &m.Role, &m.Source, &m.JoinedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already seated: answer the row as it stands, write no line and
		// bump no feed (the add's replay is the no-op it reads as).
		err = tx.QueryRow(ctx, `
			SELECT channel_id::text, user_id::text, role, source, joined_at
			FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
			channelID, userID).
			Scan(&m.ChannelID, &m.UserID, &m.Role, &m.Source, &m.JoinedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ChannelMember{}, false, errNotFound // no such channel
		}
		if err != nil {
			return ChannelMember{}, false, fmt.Errorf("read seated group member: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return ChannelMember{}, false, err
		}
		return m, false, nil
	}
	if err != nil {
		return ChannelMember{}, false, fmt.Errorf("seat group member: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, ref, event_key, requires_ack, created_at)
		VALUES ($1, NULL, 'EVENT', $2, '{}'::jsonb, $3, FALSE, clock_timestamp())`,
		channelID,
		fmt.Sprintf("%s added %s to the group.", adderName, targetName),
		fmt.Sprintf("group-added-%s-%d", userID, m.JoinedAt.UnixNano())); err != nil {
		return ChannelMember{}, false, fmt.Errorf("write the group add line: %w", err)
	}
	if err := r.bumpChannelSeq(ctx, tx, channelID); err != nil {
		return ChannelMember{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChannelMember{}, false, err
	}
	return m, true, nil
}

// RenameGroup renames a group room and writes the rename's system line in the
// UPDATE's own transaction, the feed bump committing with both or neither, so
// the caller's answer and the members' reads always agree. The UPDATE's own
// predicate holds the kind: a room of any other kind answers not-found here,
// the service's refusal having answered first.
func (r *Repository) RenameGroup(ctx context.Context, channelID, name, actorName string) (Channel, error) {
	if r.pool == nil {
		return Channel{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Channel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`UPDATE msg_channels SET name = $2, updated_at = now() WHERE id = $1::uuid AND kind = 'GROUP'`,
		channelID, name)
	if err != nil {
		return Channel{}, fmt.Errorf("rename group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Channel{}, errNotFound
	}
	// The UPDATE above IS this path's channel lock: it is the only write to
	// msg_channels in this transaction and it is the FIRST statement, so it
	// takes the channel row's lock in the mode a write takes (the columns it
	// touches are not part of a unique index) and holds it until the commit.
	// Nothing here upgrades that lock later, which is how two posts to one
	// channel would deadlock against each other. The rename's line below is
	// therefore stamped after the lock was granted, and a read of this channel
	// is ordered against it by that lock and not by a clock (UX-110).
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, ref, event_key, requires_ack, created_at)
		VALUES ($1, NULL, 'EVENT', $2, '{}'::jsonb, $3, FALSE, clock_timestamp())`,
		channelID,
		fmt.Sprintf("%s renamed this group to %s.", actorName, name),
		fmt.Sprintf("group-renamed-%d", time.Now().UnixNano())); err != nil {
		return Channel{}, fmt.Errorf("write the group rename line: %w", err)
	}
	if err := r.bumpChannelSeq(ctx, tx, channelID); err != nil {
		return Channel{}, err
	}
	var c Channel
	c, err = scanChannel(tx.QueryRow(ctx,
		`SELECT `+channelCols+` FROM msg_channels WHERE id = $1`, channelID))
	if err != nil {
		return Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Channel{}, err
	}
	return c, nil
}

// RemoveChannelMember drops one member row. A DERIVED row is refused
// (ErrDerivedMember): the role and branch model re-derives it on the next
// sync, so a hand removal would be undone and lie in between. A removal
// never drops the room's last OWNER row (ErrLastOwner): the delete's own
// predicate holds the owner count, so two concurrent removals cannot both
// pass it. A row that is already gone answers the route's not-found: the
// remove that got there first did the work, and a row that is gone says
// nothing about owners (UX-111; the read moved inside the transaction so a
// zero-row delete can no longer be mistaken for the refusal). The delete
// and its feed bump run in ONE transaction: a failure of either leaves the
// member seated and the feed unmoved, so the caller's error and the world
// always agree.
func (r *Repository) RemoveChannelMember(ctx context.Context, channelID, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The channel's lock, BEFORE the member row this path deletes. The path
	// writes msg_channels itself (the feed bump below), so it takes the
	// lock in the mode a write takes: FOR NO KEY UPDATE, and the bump asks
	// for the very lock this holds.
	//
	// The CHANNEL before the MEMBER is the one order this module takes
	// everywhere (UX-110 round 2). The remove used to take the member row
	// first and the channel second, the inverse of the seat path, and the
	// two closed a cycle on one member row of one room: the seat's
	// INSERT waited for the row this DELETE held, and this DELETE's bump
	// waited for the channel the seat held. The database answered that
	// cycle with 40P01 and the user saw a 500.
	if err := lockChannelForWrite(ctx, tx, channelID); err != nil {
		return err
	}
	// The member row's source, read INSIDE the transaction, under the
	// channel's lock the read above holds. It used to be read off the pool
	// before the transaction opened, which let a remove that raced another
	// remove of the same member read the row alive and then delete nothing:
	// the zero-row delete was taken for the last-owner refusal, and the
	// second caller got a 409 about a room whose owner was intact. Under
	// the lock nothing can remove the row between this read and the delete
	// below (every member path in this module takes the channel first), so
	// each answer has one meaning: a read that finds no row is "already
	// removed", and a delete that misses a row this read saw is the last
	// owner.
	var source string
	err = tx.QueryRow(ctx,
		`SELECT source FROM msg_members WHERE channel_id = $1 AND user_id = $2`,
		channelID, userID).Scan(&source)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Already removed, by whichever remove got here first. That is
			// not a conflict and says nothing about owners, so it answers
			// the not-found this route has always answered for a member
			// who is not there: the handler maps it to a 404 and the web
			// already carries the words for one.
			return errNotFound
		}
		return fmt.Errorf("resolve member: %w", err)
	}
	if source == SourceDerived {
		return ErrDerivedMember
	}
	ct, err := tx.Exec(ctx, `
		DELETE FROM msg_members m
		WHERE m.channel_id = $1 AND m.user_id = $2 AND m.source = 'EXPLICIT'
		  AND (m.role <> 'OWNER'
		       OR (SELECT count(*) FROM msg_members o
		           WHERE o.channel_id = $1 AND o.role = 'OWNER') > 1)`,
		channelID, userID)
	if err != nil {
		return fmt.Errorf("remove channel member: %w", err)
	}
	if ct.RowsAffected() == 0 {
		// The row is EXPLICIT (read under the lock above) and survived the
		// predicate: it is the room's last OWNER. Nothing can have removed
		// the row between the read and this delete, because both hold the
		// channel's lock this transaction already holds.
		return ErrLastOwner
	}
	// The bump commits with the delete or not at all: the add side already
	// holds this rule for the seat (P2-A), and this is the remove's
	// matching half.
	if err := r.bumpChannelSeq(ctx, tx, channelID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// personaUsersByRole resolves the ACTIVE users holding each seeded role, the
// identity tables read through the attribution seam this file already carries
// (the users/roles/user_roles reads the derived-membership rule itself runs
// on). The persona seeding (personaseed.go) keys off this: roles, never user
// ids, so a persona whose rows land later gets its rooms the next seed.
func (r *Repository) personaUsersByRole(ctx context.Context) (map[string][]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ro.code::text, u.id::text
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles ro ON ro.id = ur.role_id
		WHERE u.status = 'ACTIVE'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var code, uid string
		if err := rows.Scan(&code, &uid); err != nil {
			return nil, err
		}
		out[code] = append(out[code], uid)
	}
	return out, rows.Err()
}

// StaffRosterRow is one staffer on the New message picker's roster: the
// display name, the winning role's display name from the role catalog
// (roles.name, never the code) and the granted branches' display names
// joined with a comma and a space. Either label is empty when the person
// holds none. No email, no phone, no permission or cost field, no tier
// leaves this query.
type StaffRosterRow struct {
	UserID      string
	DisplayName string
	RoleLabel   string
	BranchLabel string
}

// StaffRoster answers every active staffer of one org, the people a direct
// message may be opened with (the DM rule admits exactly the ACTIVE staff,
// whatever branch they sit at: a DM names no branch, so the branch wall that
// governs rooms founded naming one has nothing to bite on here), one
// exception the DM rule itself does not make: a holder of the carried
// platform SUPER_ADMIN role is no dealer's staffer (0043 plants the platform
// owner, the UAT migrations plant platform testers, and the demo populate
// stamps them into the org), so such a row never answers the picker. The
// identity tables are the ones this file's allowlist entry already carries;
// the org wall is the additive 0213 anchor read the same shape the load
// doors use (a row anchored to another org is nobody else's staffer, an
// unanchored row is the carried single org world's). The winning role is the
// lowest tier's, ties by role id, the same priority the authority resolver
// applies; the order of the answer is the service's to fix (it sorts bytewise
// for the cursor), so the query orders by nothing.
func (r *Repository) StaffRoster(ctx context.Context, orgID string) ([]StaffRosterRow, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if orgID == "" {
		return nil, errors.New("messaging: staff roster requires the org id")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT u.id::text, u.display_name,
		       COALESCE(win.name, ''),
		       COALESCE(brs.label, '')
		FROM users u
		LEFT JOIN LATERAL (
			SELECT ro.name
			FROM user_roles ur
			JOIN roles ro ON ro.id = ur.role_id
			WHERE ur.user_id = u.id AND ur.deleted_at IS NULL AND ro.deleted_at IS NULL
			ORDER BY ro.tier, ro.id
			LIMIT 1
		) win ON TRUE
		LEFT JOIN LATERAL (
			SELECT string_agg(loc.name, ', ' ORDER BY loc.name) AS label
			FROM user_branch_grants ubg
			JOIN locations loc ON loc.id = ubg.branch_id AND loc.loc_type = 'BRANCH'
			WHERE ubg.user_id = u.id AND ubg.deleted_at IS NULL
		) brs ON TRUE
		WHERE u.status = 'ACTIVE'
		  AND u.deleted_at IS NULL
		  AND (u.org_id IS NULL OR u.org_id = $1::uuid)
		  AND NOT EXISTS (
			SELECT 1 FROM user_roles ur
			JOIN roles ro ON ro.id = ur.role_id
			WHERE ur.user_id = u.id AND ur.deleted_at IS NULL AND ro.code = 'SUPER_ADMIN'
		  )`, orgID)
	if err != nil {
		return nil, fmt.Errorf("staff roster: %w", err)
	}
	defer rows.Close()
	var out []StaffRosterRow
	for rows.Next() {
		var row StaffRosterRow
		if err := rows.Scan(&row.UserID, &row.DisplayName, &row.RoleLabel, &row.BranchLabel); err != nil {
			return nil, fmt.Errorf("staff roster: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// - The sync feeds (0385): per-caller reads and the stamped writes -
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
// The withdrawn flag rides with the row: a withdrawn message answers the
// tombstone the pull answers (UX-108 round 2), and a reader that cannot see
// the flag answers a live-shaped row for a message the server has dropped.
func (r *Repository) syncMessageLoad(ctx context.Context, userID, messageID string) (messageFeedRow, int64, bool, bool, error) {
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
		return messageFeedRow{}, 0, false, false, nil
	}
	if err != nil {
		return messageFeedRow{}, 0, false, false, fmt.Errorf("sync message load: %w", err)
	}
	if len(ref) > 0 && string(ref) != "{}" {
		m.Ref = json.RawMessage(ref)
	}
	if del {
		m.Body = ""
	}
	return m, seq, del, true, nil
}

// syncMessageFindApplied resolves one idempotency key to the caller's own
// message (the fence holds even for a row the caller has since left). The
// room rides along, so the door's room fence (admitsRoom) can hold this read
// the way it holds Load: a member the room refuses must not resolve the key.
// The withdrawn flag rides too: a withdrawn message answers the tombstone the
// pull answers (UX-108 round 2).
func (r *Repository) syncMessageFindApplied(ctx context.Context, userID, key string) (string, int64, string, bool, bool, error) {
	var id, channelID string
	var seq int64
	var del bool
	err := r.pool.QueryRow(ctx, `
		SELECT m.id::text, m.seq, m.channel_id::text, (m.deleted_at IS NOT NULL) FROM msg_messages m
		JOIN msg_members mem ON mem.channel_id = m.channel_id AND mem.user_id = $1
		WHERE m.idempotency_key = $2::uuid`, userID, key).Scan(&id, &seq, &channelID, &del)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, "", false, false, nil
	}
	if err != nil {
		return "", 0, "", false, false, fmt.Errorf("sync message find-applied: %w", err)
	}
	return id, seq, channelID, del, true, nil
}

// syncMessageExists answers the bound key gate's existence read (UX-103):
// the message row stands, tombstone aware, for every caller at once. No
// membership or room leg rides it: those are the caller's fence, the one
// Load speaks, and the gate may hide a key only for a row that is gone
// for nobody. The module's tables keep no org column, so the bare row
// read is the caller-independent answer the gate's contract asks for.
func (r *Repository) syncMessageExists(ctx context.Context, messageID string) (bool, error) {
	var present bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM msg_messages WHERE id = $1::uuid AND deleted_at IS NULL)`,
		messageID).Scan(&present)
	if err != nil {
		return false, fmt.Errorf("sync message exists: %w", err)
	}
	return present, nil
}

// syncMessageCreate writes one offline post through the module's own rules
// (membership, the same-channel thread root) stamping the client key; the
// identity column stamps the seq. The mention parse runs on this path too
// (ruling 5, the same rule the REST insert applies), inside the write's own
// transaction, so an offline post names the same people its online twin would.
// A refused precondition answers errNotFound, exactly like the REST thread-root
// guard.
func (r *Repository) syncMessageCreate(ctx context.Context, authorID, channelID, body, threadRootID, key string) (string, int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The channel's lock, BEFORE the post stamps itself, so a read of this
	// channel is ordered against this post by the lock and not by a clock
	// (UX-110).
	if err := lockChannelForPost(ctx, tx, channelID); err != nil {
		return "", 0, err
	}
	var member bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM msg_members WHERE channel_id = $1 AND user_id = $2)`,
		channelID, authorID).Scan(&member); err != nil {
		return "", 0, err
	}
	if !member {
		return "", 0, fmt.Errorf("%w: not a channel member", errNotFound)
	}
	var id string
	var seq int64
	err = tx.QueryRow(ctx, `
		INSERT INTO msg_messages (channel_id, author_id, kind, body, thread_root_id, idempotency_key, created_at)
		SELECT $1, $2, 'TEXT', $3, NULLIF($4,'')::uuid, $5::uuid, clock_timestamp()
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
	men, err := parseBodyMentions(ctx, tx, channelID, body)
	if err != nil {
		return "", 0, err
	}
	if err := storeMentions(ctx, tx, id, men); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", 0, err
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

// syncAckExists answers the bound key gate's existence read (UX-103): the
// ack row stands, tombstone aware, for every caller at once, with no user
// leg (whose ack it is being the caller's fence, the one Load speaks).
func (r *Repository) syncAckExists(ctx context.Context, ackID string) (bool, error) {
	var present bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM msg_acks WHERE id = $1::uuid AND deleted_at IS NULL)`,
		ackID).Scan(&present)
	if err != nil {
		return false, fmt.Errorf("sync ack exists: %w", err)
	}
	return present, nil
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

// syncPrefsExists answers the bound key gate's existence read (UX-103):
// the pref row stands, tombstone aware, for every caller at once, with no
// user leg (whose row it is being the caller's fence, the one Load
// speaks).
func (r *Repository) syncPrefsExists(ctx context.Context, prefID string) (bool, error) {
	var present bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM msg_notification_prefs WHERE id = $1::uuid AND deleted_at IS NULL)`,
		prefID).Scan(&present)
	if err != nil {
		return false, fmt.Errorf("sync prefs exists: %w", err)
	}
	return present, nil
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

// dbExecer is the least both the pool and an open transaction satisfy, so
// a feed bump can run inside the write it belongs to.
type dbExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// dbQueryer is the least both the pool and an open transaction satisfy, so a
// channel's lock can be taken inside the write it belongs to.
type dbQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// lockChannelForPost takes the channel's lock before a post stamps itself, so
// a post and a read of one channel are ordered by that lock and not by a
// clock (UX-110).
//
// The read takes the SAME channel's lock (FOR UPDATE) before it writes the
// cursor. So a post that commits before the cursor is stamped before it and
// reads as READ (the badge stays dark for it: the read came after it), and a
// post that comes after waits for the lock and is stamped after the cursor
// (the badge lights for it: the post came after the read).
//
// FOR SHARE, and not FOR UPDATE: two posts to one channel share this lock, so
// they do not block each other. It still conflicts with the read's FOR
// UPDATE, which is the whole point, and with the write mode the paths that
// write msg_channels take.
//
// The lock is taken on the channel's OWN row, before the message is written,
// and it is held until the write commits. A path that wrote its message first
// and locked the channel afterwards would break the order: the read would
// land in between and stamp its cursor after the message.
func lockChannelForPost(ctx context.Context, q dbQueryer, channelID string) error {
	var one int
	err := q.QueryRow(ctx,
		`SELECT 1 FROM msg_channels WHERE id = $1::uuid FOR SHARE`, channelID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("lock the channel: %w", err)
	}
	return nil
}

// lockChannelForWrite takes the channel's lock in the mode a WRITE of
// msg_channels takes, for the paths that write that table themselves.
//
// The mode is FOR NO KEY UPDATE, which is the mode an UPDATE of a column that
// is not part of a unique index takes: msg_channels.seq is not unique, so the
// feed bump this mode must not fight with asks for the very lock this takes
// and finds it already held. Nothing later in such a path has to UPGRADE the
// lock, and an upgrade is how two posts to one channel deadlock against each
// other: each waits for the other's weaker lock to lift.
//
// The lock conflicts with FOR SHARE, so a post to this channel waits for the
// write, and a write waits for the posts in flight. That is the order the
// badge depends on.
func lockChannelForWrite(ctx context.Context, q dbQueryer, channelID string) error {
	var one int
	err := q.QueryRow(ctx,
		`SELECT 1 FROM msg_channels WHERE id = $1::uuid FOR NO KEY UPDATE`, channelID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("lock the channel for the write: %w", err)
	}
	return nil
}

// bumpChannelSeq draws a fresh channels-feed seq for one room, the tell a
// membership change owes every device that caches the room. The querier is
// the caller's: the add passes its OPEN TRANSACTION, so the bump commits
// with the seat (a seat rolled back leaves the seq unmoved; a bump that
// committed alone would let a device advance past a seat that never
// landed, the very miss the bump exists to fix).
func (r *Repository) bumpChannelSeq(ctx context.Context, q dbExecer, channelID string) error {
	if _, err := q.Exec(ctx,
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

const savedMessageSelect = `
	SELECT s.message_id::text, m.channel_id::text, COALESCE(c.slug::text, ''), c.name,
	       COALESCE(m.author_id::text, ''), COALESCE(u.display_name, ''), m.kind,
	       CASE WHEN m.deleted_at IS NULL THEN m.body ELSE '' END,
	       m.created_at, s.saved_at
	FROM msg_saved_messages s
	JOIN msg_messages m ON m.id = s.message_id
	JOIN msg_channels c ON c.id = m.channel_id
	LEFT JOIN users u ON u.id = m.author_id`

func scanSavedMessage(row pgx.Row) (SavedMessage, error) {
	var item SavedMessage
	err := row.Scan(&item.MessageID, &item.ChannelID, &item.ChannelSlug, &item.ChannelName,
		&item.AuthorID, &item.AuthorName, &item.Kind, &item.Body, &item.CreatedAt, &item.SavedAt)
	return item, err
}

func (r *Repository) SaveMessage(ctx context.Context, userID, messageID string) (SavedMessage, error) {
	if r.pool == nil {
		return SavedMessage{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return SavedMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var channelID string
	if err := tx.QueryRow(ctx, `
		SELECT channel_id::text FROM msg_messages
		WHERE id = $1 AND deleted_at IS NULL`, messageID).Scan(&channelID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SavedMessage{}, ErrNotFound
		}
		return SavedMessage{}, fmt.Errorf("resolve saved message: %w", err)
	}
	var member bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM msg_members WHERE channel_id = $1 AND user_id = $2
		)`, channelID, userID).Scan(&member); err != nil {
		return SavedMessage{}, fmt.Errorf("saved message membership: %w", err)
	}
	if !member {
		return SavedMessage{}, ErrNotMember
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO msg_saved_messages (user_id, message_id, channel_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, message_id) DO NOTHING`, userID, messageID, channelID); err != nil {
		return SavedMessage{}, fmt.Errorf("save message: %w", err)
	}
	item, err := scanSavedMessage(tx.QueryRow(ctx, savedMessageSelect+`
		WHERE s.user_id = $1 AND s.message_id = $2`, userID, messageID))
	if err != nil {
		return SavedMessage{}, fmt.Errorf("read saved message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SavedMessage{}, err
	}
	return item, nil
}

func (r *Repository) ListSavedMessages(ctx context.Context, userID string, limit int) ([]SavedMessage, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, savedMessageSelect+`
		WHERE s.user_id = $1
		  AND m.created_at >= NOW() - INTERVAL '365 days'
		  AND c.is_archived = FALSE
		  AND c.deleted_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM msg_members mem
		    WHERE mem.channel_id = m.channel_id AND mem.user_id = $1
		  )
		ORDER BY s.saved_at DESC, s.message_id DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list saved messages: %w", err)
	}
	defer rows.Close()
	var out []SavedMessage
	for rows.Next() {
		item, err := scanSavedMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan saved message: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) DeleteSavedMessage(ctx context.Context, userID, messageID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if _, err := r.pool.Exec(ctx, `
		DELETE FROM msg_saved_messages WHERE user_id = $1 AND message_id = $2`,
		userID, messageID); err != nil {
		return fmt.Errorf("delete saved message: %w", err)
	}
	return nil
}

func (r *Repository) SearchMessages(ctx context.Context, userID, pattern string, limit int) ([]SearchMessage, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.id::text, m.channel_id::text, COALESCE(c.slug::text, ''), c.name,
		       COALESCE(m.author_id::text, ''), COALESCE(u.display_name, ''), m.kind,
		       m.body, m.created_at
		FROM msg_messages m
		JOIN msg_channels c ON c.id = m.channel_id
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.deleted_at IS NULL
		  AND m.created_at >= NOW() - INTERVAL '365 days'
		  AND c.is_archived = FALSE
		  AND c.deleted_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM msg_members cm
		    WHERE cm.channel_id = m.channel_id AND cm.user_id = $1
		  )
		  AND m.body ILIKE '%' || $2 || '%' ESCAPE '\'
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $3`, userID, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()
	var out []SearchMessage
	for rows.Next() {
		var item SearchMessage
		if err := rows.Scan(&item.MessageID, &item.ChannelID, &item.ChannelSlug, &item.ChannelName,
			&item.AuthorID, &item.AuthorName, &item.Kind, &item.Body, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message search: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) SearchPeople(ctx context.Context, orgID, pattern string, limit int) ([]SearchPerson, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if orgID == "" {
		return nil, errors.New("messaging: people search requires the org id")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT u.id::text, u.display_name,
		       COALESCE(win.code, ''), COALESCE(win.name, ''), COALESCE(brs.label, '')
		FROM users u
		LEFT JOIN LATERAL (
		  SELECT ro.code::text AS code, ro.name
		  FROM user_roles ur
		  JOIN roles ro ON ro.id = ur.role_id
		  WHERE ur.user_id = u.id AND ur.deleted_at IS NULL AND ro.deleted_at IS NULL
		  ORDER BY ro.tier, ro.id
		  LIMIT 1
		) win ON TRUE
		LEFT JOIN LATERAL (
		  SELECT string_agg(loc.name, ', ' ORDER BY loc.name) AS label
		  FROM user_branch_grants ubg
		  JOIN locations loc ON loc.id = ubg.branch_id AND loc.loc_type = 'BRANCH'
		  WHERE ubg.user_id = u.id AND ubg.deleted_at IS NULL
		) brs ON TRUE
		WHERE u.status = 'ACTIVE'
		  AND u.deleted_at IS NULL
		  AND (u.org_id IS NULL OR u.org_id = $1::uuid)
		  AND NOT EXISTS (
		    SELECT 1 FROM user_roles ur
		    JOIN roles ro ON ro.id = ur.role_id
		    WHERE ur.user_id = u.id AND ur.deleted_at IS NULL AND ro.code = 'SUPER_ADMIN'
		  )
		  AND (
		    u.display_name ILIKE '%' || $2 || '%' ESCAPE '\'
		    OR COALESCE(win.name, '') ILIKE '%' || $2 || '%' ESCAPE '\'
		    OR COALESCE(brs.label, '') ILIKE '%' || $2 || '%' ESCAPE '\'
		  )
		ORDER BY u.display_name ASC, u.id ASC
		LIMIT $3`, orgID, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("search people: %w", err)
	}
	defer rows.Close()
	var out []SearchPerson
	for rows.Next() {
		var item SearchPerson
		if err := rows.Scan(&item.UserID, &item.DisplayName, &item.RoleCode, &item.RoleLabel, &item.BranchLabel); err != nil {
			return nil, fmt.Errorf("scan people search: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) SearchChannels(ctx context.Context, userID, orgID, pattern string, limit int) ([]SearchChannel, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if orgID == "" {
		return nil, errors.New("messaging: channel search requires the org id")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT c.id::text, COALESCE(c.slug::text, ''), c.name, c.kind, c.topic,
		       EXISTS (
		         SELECT 1 FROM msg_members mine
		         WHERE mine.channel_id = c.id AND mine.user_id = $1
		       ) AS is_member
		FROM msg_channels c
		WHERE c.is_archived = FALSE
		  AND c.deleted_at IS NULL
		  AND (
		    EXISTS (
		      SELECT 1 FROM msg_members cm
		      WHERE cm.channel_id = c.id AND cm.user_id = $1
		    )
		    OR (
		      c.kind IN ('BRANCH', 'TEAM', 'TOPIC')
		      AND (
		        c.branch_id IS NULL
		        OR EXISTS (
		          SELECT 1 FROM locations loc
		          WHERE loc.id = c.branch_id
		            AND (loc.org_id IS NULL OR loc.org_id = $3::uuid)
		        )
		      )
		    )
		  )
		  AND (
		    c.slug ILIKE '%' || $2 || '%' ESCAPE '\'
		    OR c.name ILIKE '%' || $2 || '%' ESCAPE '\'
		    OR COALESCE(c.topic, '') ILIKE '%' || $2 || '%' ESCAPE '\'
		  )
		ORDER BY c.name ASC, c.id ASC
		LIMIT $4`, userID, pattern, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("search channels: %w", err)
	}
	defer rows.Close()
	var out []SearchChannel
	for rows.Next() {
		var item SearchChannel
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Kind, &item.Topic, &item.IsMember); err != nil {
			return nil, fmt.Errorf("scan channel search: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) populateMessageAttachments(ctx context.Context, msgs []Message) ([]Message, error) {
	if r.pool == nil || len(msgs) == 0 {
		return msgs, nil
	}
	msgIDs := make([]string, len(msgs))
	for i, m := range msgs {
		msgIDs[i] = m.ID
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.id::text, a.message_id::text, a.channel_id::text, a.attachment_id::text,
		       a.filename, a.content_type, a.size_bytes, COALESCE(a.storage_key, ''),
		       a.kind, a.uploaded_by::text,
		       COALESCE(u.display_name, ''),
		       a.created_at
		FROM msg_attachments a
		LEFT JOIN users u ON u.id = a.uploaded_by
		WHERE a.message_id::text = ANY($1)
		ORDER BY a.created_at ASC, a.id ASC`, msgIDs)
	if err != nil {
		return nil, fmt.Errorf("load message attachments: %w", err)
	}
	defer rows.Close()

	attMap := make(map[string][]MessageAttachment)
	for rows.Next() {
		var a MessageAttachment
		var mid, cid, aid, uid *string
		if err := rows.Scan(&a.ID, &mid, &cid, &aid, &a.Filename, &a.ContentType, &a.SizeBytes,
			&a.StorageKey, &a.Kind, &uid, &a.SenderName, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message attachment: %w", err)
		}
		a.MessageID = mid
		a.ChannelID = cid
		a.AttachmentID = aid
		a.UploadedBy = uid
		if mid != nil {
			attMap[*mid] = append(attMap[*mid], a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range msgs {
		if atts, ok := attMap[msgs[i].ID]; ok {
			msgs[i].Attachments = atts
		}
	}
	return msgs, nil
}

// ListChannelAttachments returns all attachments shared in a room, ordered newest first.
func (r *Repository) ListChannelAttachments(ctx context.Context, channelID string) ([]MessageAttachment, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.id::text, a.message_id::text, a.channel_id::text, a.attachment_id::text,
		       a.filename, a.content_type, a.size_bytes, COALESCE(a.storage_key, ''),
		       a.kind, a.uploaded_by::text,
		       COALESCE(u.display_name, mu.display_name, ''),
		       a.created_at
		FROM msg_attachments a
		LEFT JOIN users u ON u.id = a.uploaded_by
		LEFT JOIN msg_messages m ON m.id = a.message_id
		LEFT JOIN users mu ON mu.id = m.author_id
		WHERE a.channel_id = $1::uuid OR (a.channel_id IS NULL AND m.channel_id = $1::uuid)
		ORDER BY a.created_at DESC, a.id DESC`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list channel attachments: %w", err)
	}
	defer rows.Close()

	out := make([]MessageAttachment, 0)
	for rows.Next() {
		var a MessageAttachment
		var mid, cid, aid, uid *string
		if err := rows.Scan(&a.ID, &mid, &cid, &aid, &a.Filename, &a.ContentType, &a.SizeBytes,
			&a.StorageKey, &a.Kind, &uid, &a.SenderName, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan channel attachment: %w", err)
		}
		a.MessageID = mid
		a.ChannelID = cid
		a.AttachmentID = aid
		a.UploadedBy = uid
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountChannelMembers returns the count of members in a channel.
func (r *Repository) CountChannelMembers(ctx context.Context, channelID string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var count int
	if err := r.pool.QueryRow(ctx, `SELECT count(*)::int FROM msg_members WHERE channel_id = $1::uuid`, channelID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count channel members: %w", err)
	}
	return count, nil
}

// InsertChannelAttachment inserts one attachment item for a channel.
func (r *Repository) InsertChannelAttachment(ctx context.Context, channelID, uploadedBy string, in ChannelAttachmentCreateInput) (MessageAttachment, error) {
	if r.pool == nil {
		return MessageAttachment{}, errNoDB
	}
	kind := in.Kind
	if kind == "" {
		kind = "document"
	}
	contentType := in.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var sizeBytes int64
	if in.SizeBytes != nil {
		sizeBytes = *in.SizeBytes
	}

	var uArg any
	if uploadedBy != "" {
		if _, err := uuid.Parse(uploadedBy); err == nil {
			uArg = uploadedBy
		}
	}

	var mArg any
	if in.MessageID != nil && *in.MessageID != "" {
		if _, err := uuid.Parse(*in.MessageID); err == nil {
			mArg = *in.MessageID
		}
	}

	var a MessageAttachment
	var mid, cid, aid, uid *string
	row := r.pool.QueryRow(ctx, `
		INSERT INTO msg_attachments (
			channel_id, message_id, attachment_id, filename, content_type, size_bytes, storage_key, kind, uploaded_by, created_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8,
			(SELECT id FROM users WHERE id = $9::uuid),
			now()
		)
		ON CONFLICT (channel_id, attachment_id) WHERE attachment_id IS NOT NULL
		DO UPDATE SET filename = EXCLUDED.filename,
		              content_type = EXCLUDED.content_type,
		              size_bytes = EXCLUDED.size_bytes,
		              kind = EXCLUDED.kind
		RETURNING id::text, message_id::text, channel_id::text, attachment_id::text,
		          filename, content_type, size_bytes, COALESCE(storage_key, ''),
		          kind, uploaded_by::text,
		          COALESCE((SELECT COALESCE(u.display_name, '') FROM users u WHERE u.id = msg_attachments.uploaded_by), ''),
		          created_at`,
		channelID, mArg, in.AttachmentID, in.Filename, contentType, sizeBytes, in.StorageKey, kind, uArg,
	)
	if err := row.Scan(&a.ID, &mid, &cid, &aid, &a.Filename, &a.ContentType, &a.SizeBytes,
		&a.StorageKey, &a.Kind, &uid, &a.SenderName, &a.CreatedAt); err != nil {
		return MessageAttachment{}, fmt.Errorf("insert channel attachment: %w", err)
	}
	a.MessageID = mid
	a.ChannelID = cid
	a.AttachmentID = aid
	a.UploadedBy = uid
	return a, nil
}

// UpsertChannelAttachmentFromMirror mirrors an attachment link to msg_attachments.
//
// The row's created_at is the DATABASE's clock (now()), the same stamp the direct path writes, so
// the room's attachment list (ordered by created_at) carries one clock and not two. A caller that
// holds a real creation time of its own may pass it; the app's clock is never a stamp here (UX-109).
func (r *Repository) UpsertChannelAttachmentFromMirror(ctx context.Context, channelID string, att MessageAttachment) error {
	if r.pool == nil {
		return errNoDB
	}
	kind := att.Kind
	if kind == "" {
		kind = "document"
	}
	contentType := att.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var uArg any
	if att.UploadedBy != nil && *att.UploadedBy != "" {
		if _, err := uuid.Parse(*att.UploadedBy); err == nil {
			uArg = *att.UploadedBy
		}
	}

	var createdAt any
	if !att.CreatedAt.IsZero() {
		createdAt = att.CreatedAt
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO msg_attachments (
			channel_id, attachment_id, filename, content_type, size_bytes, kind, uploaded_by, created_at
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, $5, $6,
			(SELECT id FROM users WHERE id = $7::uuid),
			COALESCE($8::timestamptz, now())
		)
		ON CONFLICT (channel_id, attachment_id) WHERE attachment_id IS NOT NULL
		DO UPDATE SET filename = EXCLUDED.filename,
		              content_type = EXCLUDED.content_type,
		              size_bytes = EXCLUDED.size_bytes,
		              kind = EXCLUDED.kind`,
		channelID, att.AttachmentID, att.Filename, contentType, att.SizeBytes, kind, uArg, createdAt,
	)
	if err != nil {
		return fmt.Errorf("upsert channel attachment from mirror: %w", err)
	}
	return nil
}

// GetMessage loads one message by ID.
func (r *Repository) GetMessage(ctx context.Context, messageID string) (Message, error) {
	if r.pool == nil {
		return Message{}, errNoDB
	}
	var m Message
	var ref []byte
	row := r.pool.QueryRow(ctx, `
		SELECT m.id::text, m.channel_id::text, COALESCE(m.author_id::text,''), COALESCE(u.display_name,''),
		       m.kind, m.body, m.ref, COALESCE(m.thread_root_id::text,''), 0,
		       m.edited_at, m.deleted_at, m.created_at
		FROM msg_messages m
		LEFT JOIN users u ON u.id = m.author_id
		WHERE m.id = $1::uuid`, messageID)
	if err := row.Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.AuthorName, &m.Kind, &m.Body,
		&ref, &m.ThreadRootID, &m.ReplyCount, &m.EditedAt, &m.DeletedAt, &m.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, ErrNotFound
		}
		return Message{}, fmt.Errorf("get message: %w", err)
	}
	if len(ref) > 0 && string(ref) != "{}" {
		m.Ref = json.RawMessage(ref)
	}
	if m.DeletedAt != nil {
		m.Body = ""
	}
	return m, nil
}

// ChannelExists reports whether a channel exists.
func (r *Repository) ChannelExists(ctx context.Context, channelID string) (bool, error) {
	return r.channelExists(ctx, channelID)
}

// - Slice S7: per-person notification settings (event-category toggles + quiet hours), 0401 -

// GetPersonEventPrefsForUser returns the STORED rows for one user (absent category -> absent key),
// so the service can layer the per-category defaults (DefaultPersonEventPref) over the map and
// render the full closed set in board order. An empty user (never visited the surface) reads as a
// nil map; no error.
func (r *Repository) GetPersonEventPrefsForUser(ctx context.Context, userID string) (map[string]PersonEventPref, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT user_id::text, event_category, in_app, push, updated_at
		FROM msg_person_event_prefs WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("get person event prefs: %w", err)
	}
	defer rows.Close()
	out := map[string]PersonEventPref{}
	for rows.Next() {
		var p PersonEventPref
		if err := rows.Scan(&p.UserID, &p.EventCategory, &p.InApp, &p.Push, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan person event pref: %w", err)
		}
		out[p.EventCategory] = p
	}
	return out, rows.Err()
}

// UpsertPersonEventPref upserts ONE user's answer for ONE category (insert or update). Returns
// the persisted row. The service pre-validates the category against the closed set; an invalid
// value is a 400 at the boundary and never reaches the SQL CHECK.
func (r *Repository) UpsertPersonEventPref(ctx context.Context, p PersonEventPref) (PersonEventPref, error) {
	if r.pool == nil {
		return PersonEventPref{}, errNoDB
	}
	out := PersonEventPref{}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_person_event_prefs (user_id, event_category, in_app, push, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id, event_category) DO UPDATE
		  SET in_app = EXCLUDED.in_app,
		      push = EXCLUDED.push,
		      updated_at = now()
		RETURNING user_id::text, event_category, in_app, push, updated_at`,
		p.UserID, p.EventCategory, p.InApp, p.Push).
		Scan(&out.UserID, &out.EventCategory, &out.InApp, &out.Push, &out.UpdatedAt)
	if err != nil {
		return PersonEventPref{}, fmt.Errorf("upsert person event pref: %w", err)
	}
	return out, nil
}

// GetQuietHoursForUser returns the stored quiet-hours row for one user. Absent row -> the shipped
// defaults (DefaultQuietHours), never an error. The whole 0301r ABI for "user with no row" is the
// same as 0069's NotificationPrefs default; the brief's "filling defaults" clause leans on it.
func (r *Repository) GetQuietHoursForUser(ctx context.Context, userID string) (QuietHours, error) {
	if r.pool == nil {
		return QuietHours{}, errNoDB
	}
	out := DefaultQuietHours(userID)
	var stored bool
	err := r.pool.QueryRow(ctx, `
		SELECT user_id::text, enabled, start_local, end_local, timezone,
		       COALESCE(bypass_user_ids, '{}'::uuid[]), updated_at,
		       (user_id IS NOT NULL)
		FROM msg_quiet_hours WHERE user_id = $1`, userID).
		Scan(&out.UserID, &out.Enabled, &out.StartLocal, &out.EndLocal, &out.Timezone,
			&out.BypassUserIDs, &out.UpdatedAt, &stored)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultQuietHours(userID), nil // absent row is the shipped default
		}
		return QuietHours{}, fmt.Errorf("get quiet hours: %w", err)
	}
	return out, nil
}

// UpsertQuietHours writes one user's quiet-hours row (insert or update), bumping updated_at. The
// service pre-validates the HH:MM strings against hhmmRegex and the timezone against
// time.LoadLocation; a bad value is a 400 at the boundary and never reaches the SQL CHECKs.
//
// bypass_user_ids is a []string of UUIDs; an empty list means "no carve-outs" (the shipped
// behaviour). The repository casts it to a UUID[] server-side so the empty list serializes as
// '{}' rather than as NULL.
func (r *Repository) UpsertQuietHours(ctx context.Context, q QuietHours) (QuietHours, error) {
	if r.pool == nil {
		return QuietHours{}, errNoDB
	}
	bypass := q.BypassUserIDs
	if bypass == nil {
		bypass = []string{}
	}
	out := QuietHours{}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO msg_quiet_hours (user_id, enabled, start_local, end_local, timezone, bypass_user_ids, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid[], now())
		ON CONFLICT (user_id) DO UPDATE
		  SET enabled = EXCLUDED.enabled,
		      start_local = EXCLUDED.start_local,
		      end_local = EXCLUDED.end_local,
		      timezone = EXCLUDED.timezone,
		      bypass_user_ids = EXCLUDED.bypass_user_ids,
		      updated_at = now()
		RETURNING user_id::text, enabled, start_local, end_local, timezone,
		          COALESCE(bypass_user_ids, '{}'::uuid[]), updated_at`,
		q.UserID, q.Enabled, q.StartLocal, q.EndLocal, q.Timezone, bypass).
		Scan(&out.UserID, &out.Enabled, &out.StartLocal, &out.EndLocal, &out.Timezone,
			&out.BypassUserIDs, &out.UpdatedAt)
	if err != nil {
		return QuietHours{}, fmt.Errorf("upsert quiet hours: %w", err)
	}
	return out, nil
}

// - S6 directory + join + leave + membership-rule -

// ListDirectory is the change 7 ruling 7 directory read: every PUBLIC,
// non-archived TEAM, TOPIC or BRANCH channel the caller can see, with
// the caller's own membership state (joined bool, source DERIVED/EXPLICIT,
// member_count). DM, APPROVAL, and GROUP rooms are excluded (the
// directory is the team's hand-built rooms, not the system namespaces).
//
// The branch reach wall: a branch-scoped caller (Sam, granted Trenton)
// only sees a branch room whose branch_id they reach, and they always see
// a no-branch room (the reach-broad pass every BRANCH channel does on
// every staff). A GLOBAL caller sees every branch.
func (r *Repository) ListDirectory(ctx context.Context, userID string, isGlobal bool, branches map[string]bool) (map[string]DirectoryEntry, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// Build the branch reach predicate from the caller's grant set. A
	// GLOBAL caller is short-circuited to TRUE; a branch-scoped caller
	// matches the channel's branch_id against their grant set.
	if isGlobal {
		branches = map[string]bool{}
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ch.id::text, ch.kind, COALESCE(ch.branch_id::text,''), ch.name, ch.topic,
		       COALESCE(ch.slug::text,''), ch.visibility,
		       COALESCE(mem.source, ''), COALESCE((SELECT count(*) FROM msg_members mc WHERE mc.channel_id = ch.id), 0)
		FROM msg_channels ch
		LEFT JOIN msg_members mem ON mem.channel_id = ch.id AND mem.user_id = $1::uuid
		WHERE ch.kind IN ('TEAM', 'TOPIC', 'BRANCH')
		  AND ch.is_archived = FALSE
		  AND ch.visibility = 'PUBLIC'
		  AND (ch.branch_id IS NULL
		       OR ch.branch_id::text = ANY($2::text[])
		       OR $3 = TRUE)
		ORDER BY ch.name ASC`, userID, branchKeys(branches), isGlobal)
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	defer rows.Close()
	out := make(map[string]DirectoryEntry)
	for rows.Next() {
		var (
			id, kind, branchID, name, topic, slug, visibility, source string
			memberCount                                               int
		)
		if err := rows.Scan(&id, &kind, &branchID, &name, &topic, &slug, &visibility, &source, &memberCount); err != nil {
			return nil, fmt.Errorf("scan directory row: %w", err)
		}
		_ = visibility
		out[id] = DirectoryEntry{
			ChannelID:        id,
			Kind:             kind,
			BranchID:         branchID,
			Name:             name,
			Topic:            topic,
			Slug:             slug,
			Joined:           source != "",
			MembershipSource: source,
			MemberCount:      memberCount,
		}
	}
	return out, rows.Err()
}

// ListChannelMembers returns the channel's membership rows with the
// audit fields the Settings > Membership rules "Added by hand" panel
// shows. The membership wall has already run; the caller is in the room.
// The display name is the staff row's display_name (cosmetic, not the
// audit pair the column carries).
func (r *Repository) ListChannelMembers(ctx context.Context, channelID string) ([]ChannelMemberEntry, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.user_id::text, COALESCE(u.display_name,''), m.role, m.source,
		       m.added_reason, COALESCE(m.added_by::text,''), m.joined_at
		FROM msg_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.channel_id = $1::uuid
		ORDER BY (m.role = 'OWNER') DESC, m.joined_at ASC`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list channel members: %w", err)
	}
	defer rows.Close()
	var out []ChannelMemberEntry
	for rows.Next() {
		var e ChannelMemberEntry
		if err := rows.Scan(&e.UserID, &e.DisplayName, &e.Role, &e.Source,
			&e.AddedReason, &e.AddedBy, &e.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan channel member: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetChannelRule returns the channel's rule row (auto_scope, auto_role,
// visibility) for the membership-rule read route.
func (r *Repository) GetChannelRule(ctx context.Context, channelID string) (MembershipRule, error) {
	if r.pool == nil {
		return MembershipRule{}, errNoDB
	}
	var r0 MembershipRule
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(auto_scope,''), COALESCE(auto_role,''), visibility
		 FROM msg_channels WHERE id = $1::uuid`, channelID).
		Scan(&r0.AutoScope, &r0.AutoRole, &r0.Visibility)
	if errors.Is(err, pgx.ErrNoRows) {
		return MembershipRule{}, errNotFound
	}
	if err != nil {
		return MembershipRule{}, fmt.Errorf("get channel rule: %w", err)
	}
	return r0, nil
}

// SetChannelRule updates the channel's auto_scope, auto_role, and
// visibility in one transaction. The brief asks for an immediate
// reconciliation: the rule write owns the prune's predicate by
// rewriting the columns the qualify check reads. The next
// SyncMembership call reconciles the seats (the prune's predicate
// runs against the freshly written columns).
func (r *Repository) SetChannelRule(ctx context.Context, channelID string, rule MembershipRule) error {
	if r.pool == nil {
		return errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ct, err := tx.Exec(ctx, `
		UPDATE msg_channels
		   SET auto_scope = NULLIF($2, ''),
		       auto_role = NULLIF($3, ''),
		       visibility = $4
		 WHERE id = $1::uuid`, channelID, rule.AutoScope, rule.AutoRole, rule.Visibility)
	if err != nil {
		return fmt.Errorf("set channel rule: %w", err)
	}
	if ct.RowsAffected() == 0 {
		_ = tx.Rollback(ctx)
		return errNotFound
	}
	if err := r.bumpChannelSeq(ctx, tx, channelID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListChannelRules is the collection read for GET /api/v1/messaging/membership-rules:
// every channel's rule row the caller can see, scoped the same way as
// the directory (PUBLIC, non-archived, branch-reach wall). The caller
// is the staff reading the rules panel; the route is staff-gated.
func (r *Repository) ListChannelRules(ctx context.Context, userID string, isGlobal bool, branches map[string]bool) ([]MembershipRuleEntry, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if isGlobal {
		branches = map[string]bool{}
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ch.id::text, ch.name, ch.kind, COALESCE(ch.branch_id::text,''),
		       COALESCE(ch.auto_scope,''), COALESCE(ch.auto_role,''), ch.visibility
		FROM msg_channels ch
		WHERE ch.kind IN ('TEAM', 'TOPIC', 'BRANCH')
		  AND ch.is_archived = FALSE
		  AND ch.visibility = 'PUBLIC'
		  AND (ch.branch_id IS NULL
		       OR ch.branch_id::text = ANY($1::text[])
		       OR $2 = TRUE)
		ORDER BY ch.name ASC`, branchKeys(branches), isGlobal)
	if err != nil {
		return nil, fmt.Errorf("list channel rules: %w", err)
	}
	defer rows.Close()
	var out []MembershipRuleEntry
	for rows.Next() {
		var e MembershipRuleEntry
		if err := rows.Scan(&e.ChannelID, &e.Name, &e.Kind, &e.BranchID,
			&e.AutoScope, &e.AutoRole, &e.Visibility); err != nil {
			return nil, fmt.Errorf("scan channel rule: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// branchKeys returns the keys of a branch reach map (slice form, for an
// ANY() postgres parameter). A nil or empty map returns an empty slice;
// the SQL's NULLIF on the empty branch_id arms the no-branch channel
// to read on its own predicate.
func branchKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if m[k] {
			out = append(out, k)
		}
	}
	return out
}
