// Package messaging is the MSG-01 Internal Messaging Hub, P1 slice (channels/DMs/threads,
// system EVENT messages for the top-6 topics, unread badges, headless web route). It follows
// the house modular-monolith pattern (handler → service → repository → model) and reuses the
// existing transactional outbox + notify.Dispatcher for FR-3 system events (no new broker).
//
// P1 owns TEXT + EVENT messages only. Approvals-in-chat (FR-4), slash commands (FR-5),
// assignment routing (FR-6), native apps (FR-8), TanyAI (FR-11) and AI governance (FR-12) are
// out of P1 and are left as clean seams (the schema's ref JSONB + kind enum, the derived-
// membership rule) rather than built.
package messaging

import (
	"encoding/json"
	"strings"
	"time"
)

// Channel kinds (mirror the msg_channels CHECK).
const (
	KindBranch = "BRANCH"
	KindTeam   = "TEAM"
	KindTopic  = "TOPIC"
	KindDM     = "DM"
	// KindApproval is a per-person approvals-rail channel (0075): slug approvals-<username>, sole member
	// the approver. One dedicated surface per approver so a decision card is never lost in a busy channel.
	KindApproval = "APPROVAL"
)

// Message kinds (mirror the msg_messages CHECK).
const (
	MsgText  = "TEXT"
	MsgEvent = "EVENT"
	// MsgApproval is a system-authored approvals-rail card (0075): author_id NULL, carrying a typed
	// ApprovalRef + an event_key so the same request fans out at-most-once per channel (idempotent).
	MsgApproval = "APPROVAL"
)

// ApprovalRef is the typed `ref` an APPROVAL-kind message carries, the rail's read-only pointer back at
// the permission_approval_requests row (0016), NEVER a second copy of approval state. Status is a display
// echo stamped across every card copy on decision (StampApprovalStatus); the live truth is the request
// row. This shape + the decide interface are the ONLY two seams the P2 warranty (DIB-223) binds to.
type ApprovalRef struct {
	ApprovalID  string `json:"approval_id"`
	PolicyType  string `json:"policy_type"`
	SubjectRef  string `json:"subject_ref,omitempty"` // the quote/journal the override binds to
	BranchID    string `json:"branch_id,omitempty"`
	AmountCents int64  `json:"amount_cents,omitempty"` // JE debit total (margin cards carry 0)
	RequestedBy string `json:"requested_by,omitempty"`
	Detail      string `json:"detail,omitempty"`
	DeepLink    string `json:"deep_link,omitempty"`
	Status      string `json:"status"` // PENDING | APPROVED | REJECTED, display echo, stamped on decide
}

// approvalCardKey / approvalDecidedKey are the per-request event_keys the rail dedups on. The card
// (approval:<id>) is at-most-once per channel (the 0050 uq_msg_messages_event_key), so a re-notify or a
// reconcile-sweep re-fan is idempotent; the decision echo (approval:<id>:decided) is a separate EVENT so
// its own re-post is likewise idempotent. Stamping targets the card copies by exact key across channels.
func approvalCardKey(approvalID string) string    { return "approval:" + approvalID }
func approvalDecidedKey(approvalID string) string { return "approval:" + approvalID + ":decided" }

// approvalChannelSlug is the STABLE, PER-USER-UNIQUE channel handle. The slug is the CITEXT-UNIQUE key on
// msg_channels, so it MUST incorporate the userID: building it from the display name alone collapsed two
// approvers who share a display name, or names that sanitize equal ("Jo.Smith" vs "Jo Smith"), CITEXT
// also making it case-insensitive, into ONE shared channel, leaking each approver's cards to the other
// (audit schema finding). The human handle stays cosmetic (it lives in the channel NAME/topic set by
// EnsureApprovalChannel); the userID suffix guarantees exactly one channel per approver.
func approvalChannelSlug(username, userID string) string {
	id := sanitizeSlugPart(userID)
	if h := sanitizeSlugPart(username); h != "" {
		return "approvals-" + h + "-" + id
	}
	return "approvals-" + id
}

// sanitizeSlugPart lowercases and reduces a name to [a-z0-9-], collapsing any other run to a single '-',
// so a display name / email local-part becomes a safe channel slug.
func sanitizeSlugPart(s string) string {
	out := make([]rune, 0, len(s))
	prevDash := false
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
			prevDash = false
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			out = append(out, c)
			prevDash = false
		default:
			if !prevDash && len(out) > 0 {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	// Trim a trailing dash.
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return string(out)
}

// reservedChannelSlugPrefixes are the system namespaces no caller founded
// room may enter: the DM pair slugs (dm-<a>-<b>), the rep customer-message
// channels (rep-...), the approvals rail's per-approver channels
// (approvals-...), and the branch seeded families whose names are derived
// from branch facts and converged on by system routing: the house queues
// (house-<city>, the branch's home for unassigned customers), the
// payments channels (payments-<city>, the day's rollup) and the yard
// rooms (yard-<city>). A caller founded room that squatted one of them
// would capture the next system convergence on the same slug (the
// branch's pick or the post by slug), so a slug arriving there (chosen
// or derived from the name) is refused before the insert runs. The cost
// is honest: a room whose name happens to derive into the namespace
// ("DM guidelines", "House 0 mine") is told to pick another name.
var reservedChannelSlugPrefixes = []string{"dm-", "rep-", "approvals-", "house-", "payments-", "yard-"}

// reservedChannelSlugs are the exact system slugs no caller founded room
// may take: the GLOBAL payments floor ("payments"), which the routine
// router and the rollup reader match exactly. A bare slug carries no
// prefix, so it stands on its own beside the families.
var reservedChannelSlugs = []string{"payments"}

// isReservedChannelSlug reports whether the slug lands in one of the system
// namespaces reservedChannelSlugPrefixes names, or is one of the exact
// system slugs reservedChannelSlugs lists. The slug column is CITEXT,
// so the unique index folds case: the namespace wall must fold the same
// way or an upper cased APPROVALS- slug squats a system room the next
// provision converges on. The service lower cases caller chosen slugs
// before this runs; folding here too keeps the wall whole for any other
// caller.
func isReservedChannelSlug(slug string) bool {
	lower := strings.ToLower(slug)
	for _, p := range reservedChannelSlugPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	for _, s := range reservedChannelSlugs {
		if lower == s {
			return true
		}
	}
	return false
}

// Membership sources.
const (
	SourceDerived  = "DERIVED"
	SourceExplicit = "EXPLICIT"
)

// Channel tiers (mirror the msg_channels.tier CHECK added in 0069). The tier drives how loud a channel
// is at notification time (see shouldNotify): a FIREHOSE escalation pierces a mute for a user whose
// escalation_override is on.
const (
	TierStandard   = "STANDARD"
	TierEscalation = "ESCALATION"
	TierFirehose   = "FIREHOSE"
)

// defaultRetentionMonths is the hot-retention window seeded as the global default in 0070. The
// retention worker archives content older than now() - defaultRetentionMonths (per-channel overrides
// live in msg_retention_policy).
const defaultRetentionMonths = 12

// Channel is one conversation surface. UnreadCount / Mentioned / LatestAt are populated only in
// the list-my-channels projection (they are per-caller, not intrinsic to the channel).
type Channel struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	BranchID   string    `json:"branch_id,omitempty"`
	Slug       string    `json:"slug,omitempty"`
	Name       string    `json:"name"`
	Topic      string    `json:"topic"`
	Tier       string    `json:"tier"` // STANDARD | ESCALATION | FIREHOSE (0069)
	IsArchived bool      `json:"is_archived"`
	CreatedAt  time.Time `json:"created_at"`
	// Per-caller projection (list-my-channels only):
	UnreadCount int        `json:"unread_count"`
	Mentioned   bool       `json:"mentioned"`
	LatestAt    *time.Time `json:"latest_at,omitempty"`
}

// ChannelMember is one membership row as the member-add surface returns it:
// the channel, the member, their role in the room, and the membership's
// source (EXPLICIT rows are the hand-manageable ones; DERIVED rows belong to
// the role and branch model).
type ChannelMember struct {
	ChannelID string    `json:"channel_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	Source    string    `json:"source"`
	JoinedAt  time.Time `json:"joined_at"`
}

// Message is one post. AuthorID is empty for a system EVENT message. Ref carries {topic, entity,
// deep_link} for EVENT messages. ThreadRootID is empty for a top-level message.
type Message struct {
	ID           string          `json:"id"`
	ChannelID    string          `json:"channel_id"`
	AuthorID     string          `json:"author_id,omitempty"`
	AuthorName   string          `json:"author_name,omitempty"` // resolved display name (system => "")
	Kind         string          `json:"kind"`
	Body         string          `json:"body"`
	Ref          json.RawMessage `json:"ref,omitempty"`
	ThreadRootID string          `json:"thread_root_id,omitempty"`
	ReplyCount   int             `json:"reply_count"` // populated for top-level messages in the stream
	// RequiresAck (0071) flags a forced-ack message the kiosk screen-takeover must block on. It is set
	// only on the UnackedRequiresAck read model + a fresh EVENT insert; omitempty keeps a normal post's
	// payload byte-identical to before this feature.
	RequiresAck bool `json:"requires_ack,omitempty"`
	// Priority / StateKey / SupersededBy / SupersededAt are the DIB-217 state-conflict fields (0138).
	// All omitempty, so an ordinary post's payload stays byte-identical to before this feature.
	// SupersededBy non-empty means this message was auto-resolved by a newer fact about the same record
	// (the LAST-WRITE-WINS ruling), the bubble stops blocking and renders as superseded, never acked.
	Priority     string     `json:"priority,omitempty"`
	StateKey     string     `json:"state_key,omitempty"`
	SupersededBy string     `json:"superseded_by,omitempty"`
	SupersededAt *time.Time `json:"superseded_at,omitempty"`
	EditedAt     *time.Time `json:"edited_at,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// EventRef is the structured ref stored on an EVENT message.
type EventRef struct {
	Topic    string `json:"topic"`
	Entity   string `json:"entity,omitempty"`
	DeepLink string `json:"deep_link,omitempty"`
	Summary  string `json:"summary,omitempty"`
	// - DIB-449 rollup fields (0152) -
	// AmountCents is the money the event carries, as a NUMBER. It is stamped on each routine event's
	// thread reply so the day's rollup is a fold over structured facts rather than a re-parse of the
	// rendered sentence, and it is stamped on the rollup root as the day's total. Count/RollupDay are
	// the root's machine-readable twin of its body, so a client never parses the line to learn what it
	// says. All three are omitempty: an ordinary event's ref serializes byte-identically to before.
	AmountCents int64  `json:"amount_cents,omitempty"`
	Count       int    `json:"count,omitempty"`
	RollupDay   string `json:"rollup_day,omitempty"`
	// - DIB-462 rule fields (0157) -
	// Rules names WHY a targeted payment notice reached this recipient (ONLINE / CROSS_BRANCH /
	// OWN_ORDER). It rides the ref rather than only the sentence so a surface, or a test, can tell a
	// rule-driven notice from an ordinary event without parsing prose. omitempty: every message this
	// module wrote before DIB-462 still serializes byte-identically.
	Rules []string `json:"rules,omitempty"`
}

// UnreadCounts is the FR-7 unread summary: per-channel unread + mention flag, plus the total.
type UnreadCounts struct {
	Total    int             `json:"total"`
	Channels []ChannelUnread `json:"channels"`
}

// ChannelUnread is one row of the unread summary.
type ChannelUnread struct {
	ChannelID string     `json:"channel_id"`
	Unread    int        `json:"unread_count"`
	Mentioned bool       `json:"mentioned"`
	LatestAt  *time.Time `json:"latest_at,omitempty"`
}

// MessagePage is the cursor-paginated message read model. NextCursor is empty when there is no
// older page. Messages are returned newest-first (the stream reverses them for display).
type MessagePage struct {
	Messages   []Message `json:"messages"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// Caller is the minimal authenticated-staff projection the service needs (mirrors the fields the
// identity guards stamp onto the request context: user id, scope, and granted branches). It is
// built by the handler from identity.StaffContextFromContext so messaging never re-implements
// auth, identity stays the SoT (FR-1).
type Caller struct {
	UserID   string
	RoleCode string
	Scope    string // "GLOBAL" | "BRANCH"
	Branches map[string]bool
}

// IsGlobal reports whether the caller reaches every branch (GLOBAL scope).
func (c Caller) IsGlobal() bool { return c.Scope == "GLOBAL" }

// - Feature A: per-user notification preferences (0069) -

// NotificationPrefs is one user's notification tuning for one channel (the msg_notification_prefs
// row). An absent row is the defaults (DefaultNotificationPrefs): desktop on, escalation overrides a
// mute, not muted. These drive the shouldNotify desktop-notification decision, distinct from
// msg_members.muted (the legacy channel-hide flag).
type NotificationPrefs struct {
	ChannelID          string `json:"channel_id"`
	UserID             string `json:"user_id"`
	DesktopEnabled     bool   `json:"desktop_enabled"`
	EscalationOverride bool   `json:"escalation_override"`
	Muted              bool   `json:"muted"`
	// Mode (DIB-268, 0137) is the three-way per-channel setting the BADGE pipeline honours:
	// ModeAll | ModeMentions | ModeMuted. Muted above is its boolean shadow, kept in sync by
	// applyMode so the shipped shouldNotify matrix (which reads Muted) can never disagree with it.
	Mode string `json:"mode"`
	// Inherited (DIB-449) is TRUE when this user has made NO choice for this channel and the mode shown
	// is the CHANNEL's default (msg_channels.default_mode, 0152). The surface needs the distinction to
	// be honest: "quiet because that is how this channel arrives" and "quiet because you muted it" look
	// identical in a radio group and mean completely different things to the person reading it.
	Inherited bool      `json:"inherited,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Notification modes (mirror the msg_notification_prefs.mode CHECK, 0137).
const (
	// ModeAll, every message badges (the default; pre-0137 behaviour).
	ModeAll = "ALL"
	// ModeMentions, only messages that mention the user badge ("quiet but reachable").
	ModeMentions = "MENTIONS"
	// ModeMuted, nothing badges, nothing notifies.
	ModeMuted = "MUTED"
)

// normalizeMode validates a mode against the CHECK set, defaulting empty to ALL. ok is false for an
// unknown value so a bad client mode is a 400 at the service boundary, never a Postgres CHECK
// violation → 500 (mirrors normalizeAckMethod).
func normalizeMode(mode string) (string, bool) {
	switch mode {
	case "":
		return ModeAll, true
	case ModeAll, ModeMentions, ModeMuted:
		return mode, true
	default:
		return "", false
	}
}

// applyMode sets the mode AND its boolean shadow together. Nothing else in this package may write one
// without the other: `muted` is what the shipped shouldNotify predicate reads, `mode` is what the
// badge pipeline reads, and a user whose toast is silent while their badge still counts (or vice
// versa) is a bug report nobody can reproduce.
func applyMode(p NotificationPrefs, mode string) NotificationPrefs {
	p.Mode = mode
	p.Muted = mode == ModeMuted
	return p
}

// ModeOf returns a prefs row's effective mode, deriving it from the legacy `muted` boolean when Mode
// is unset. This is what makes a pre-0137 row (or any code path that only knows about `muted`) read
// correctly instead of silently un-muting a user who had muted a channel.
func ModeOf(p NotificationPrefs) string {
	if p.Mode != "" {
		return p.Mode
	}
	if p.Muted {
		return ModeMuted
	}
	return ModeAll
}

// DefaultNotificationPrefs returns the defaults applied when a user has no explicit pref row for a
// channel (matches the msg_notification_prefs column defaults in 0069).
func DefaultNotificationPrefs(channelID, userID string) NotificationPrefs {
	return NotificationPrefs{
		ChannelID:          channelID,
		UserID:             userID,
		DesktopEnabled:     true,
		EscalationOverride: true,
		Muted:              false,
		Mode:               ModeAll,
	}
}

// PrefsPatch is the PUT notify-prefs body: each field is optional (pointer), so an omitted field keeps
// the caller's current value on upsert rather than being coerced to false.
type PrefsPatch struct {
	DesktopEnabled     *bool `json:"desktop_enabled"`
	EscalationOverride *bool `json:"escalation_override"`
	Muted              *bool `json:"muted"`
	// Mode (DIB-268) is the richer three-way control. When BOTH Mode and Muted are sent, Mode WINS -
	// it is the more specific instruction, and a client that sends {"mode":"MENTIONS","muted":false}
	// means mention-only, not "all".
	Mode *string `json:"mode"`
}

// - Feature B: HR/audit export + retention (0070) -

// ExportFilter is the admin HR/audit export query (mirrors audit.Filter). All fields optional; empty
// means no constraint. Since/Until are RFC3339 strings validated at the handler boundary before they
// reach the ::timestamptz casts.
type ExportFilter struct {
	ChannelID string
	UserID    string
	Since     string // RFC3339; optional lower bound (created_at >= Since)
	Until     string // RFC3339; optional upper bound (created_at <  Until)
	Limit     int    // clamped 1..maxExportRows
}

// ChannelRetention pairs a channel with its EFFECTIVE hot-retention window (months), the per-channel
// policy override, else the global default, else defaultRetentionMonths. The retention worker turns
// each into a cutoff (retentionCutoff) and archives content older than it.
type ChannelRetention struct {
	ChannelID string
	Months    int
}

// - Feature 1: Screen-Takeover forced acknowledgement (0071) -

// Ack methods (mirror the msg_acks.method CHECK). TAP is the v1 canned kiosk path; QUICK_REPLY and
// VOICE are reserved for the gated TanyAI device/quick-reply work (DIB-218), the schema admits them
// so the input channel is recorded, but P1 only ever writes TAP.
const (
	AckTap        = "TAP"
	AckQuickReply = "QUICK_REPLY"
	AckVoice      = "VOICE"
)

// Ack is one acknowledgement of a forced-ack (requires_ack) message by one user, the msg_acks row.
// It is returned by the ack endpoint so the kiosk can confirm the write and dismiss the overlay.
type Ack struct {
	MessageID string    `json:"message_id"`
	UserID    string    `json:"user_id"`
	Method    string    `json:"method"`
	AckedAt   time.Time `json:"acked_at"`
}

// normalizeAckMethod defaults an empty method to TAP and validates it against the msg_acks CHECK set,
// so a bad client value is a 400 at the service boundary rather than a Postgres CHECK violation → 500.
// ok is false for an unknown method.
func normalizeAckMethod(method string) (string, bool) {
	switch method {
	case "":
		return AckTap, true // default TAP (the kiosk "I got it" button)
	case AckTap, AckQuickReply, AckVoice:
		return method, true
	default:
		return "", false
	}
}

// ExportMessage is one admin-masked row of the HR/audit export (mirrors the audit projectForOperator
// read seam). Body is passed through projectForExport, which tombstones deleted messages and redacts
// cleartext-looking identifiers so a raw PAN/SIN can never leave the system in an export.
type ExportMessage struct {
	ID         string     `json:"id"`
	ChannelID  string     `json:"channel_id"`
	AuthorID   string     `json:"author_id,omitempty"`
	AuthorName string     `json:"author_name,omitempty"`
	Kind       string     `json:"kind"`
	Body       string     `json:"body"`
	CreatedAt  time.Time  `json:"created_at"`
	EditedAt   *time.Time `json:"edited_at,omitempty"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}
