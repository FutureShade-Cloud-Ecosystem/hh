package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// realtime is the optional real-time fan-out seam (FR-7). The SSE hub implements it; when nil the
// service still works (clients fall back to short-poll of the unread summary). Kept as an interface
// so the service never depends on the HTTP layer.
type realtime interface {
	// PublishMessage notifies members of a channel that a new message arrived.
	PublishMessage(channelID string, m Message)
}

// store is the persistence seam the service depends on (satisfied by *Repository). Expressing it as
// an interface lets the authz / unread / thread logic be unit-tested with a fake, DB-free, so the
// channel-scoping and unread math are proven without a live Postgres.
type store interface {
	SyncMembership(ctx context.Context, userID string) error
	ListChannelsForUser(ctx context.Context, userID string) ([]Channel, error)
	IsMember(ctx context.Context, channelID, userID string) (bool, error)
	channelExists(ctx context.Context, channelID string) (bool, error)
	ListMessages(ctx context.Context, channelID, cursor string, limit int) (MessagePage, error)
	ListThread(ctx context.Context, channelID, rootID string) ([]Message, error)
	InsertMessage(ctx context.Context, channelID, userID, body, threadRootID string) (Message, error)
	// Change 7 S4: the author's own edit and delete, inside the window the
	// msg_policy row keeps. The facts read binds the message to its channel;
	// both writes re-check their walls in the WHERE.
	messageFactsFor(ctx context.Context, channelID, messageID string) (messageFacts, error)
	EditMessage(ctx context.Context, channelID, messageID, body string) (Message, error)
	DeleteMessage(ctx context.Context, channelID, messageID string) (Message, error)
	EditWindowMinutes(ctx context.Context) (int, error)
	MarkRead(ctx context.Context, channelID, userID string, at time.Time) error
	UnreadSummary(ctx context.Context, userID string) (UnreadCounts, error)
	InsertEventMessage(ctx context.Context, channelID, eventKey, body string, ref EventRef, requiresAck bool) (Message, bool, error)
	ChannelIDBySlug(ctx context.Context, slug string) (string, error)
	BranchYardSlug(ctx context.Context, branchID string) (string, error)
	// Feature A (0069): notification prefs + channel tier.
	ChannelTier(ctx context.Context, channelID string) (string, error)
	GetNotificationPrefs(ctx context.Context, channelID, userID string) (NotificationPrefs, error)
	UpsertNotificationPrefs(ctx context.Context, p NotificationPrefs) (NotificationPrefs, error)
	// Feature B (0070): HR/audit export read seam.
	ExportMessages(ctx context.Context, f ExportFilter) ([]Message, error)
	// Feature 1 (0071): forced-ack write + kiosk poll read seam.
	AckMessage(ctx context.Context, channelID, messageID, userID, method string) (Ack, error)
	UnackedRequiresAck(ctx context.Context, userID string) ([]Message, error)
	// Slice S7 (0401): per-person event-category prefs + quiet-hours read/write seam.
	GetPersonEventPrefsForUser(ctx context.Context, userID string) (map[string]PersonEventPref, error)
	UpsertPersonEventPref(ctx context.Context, p PersonEventPref) (PersonEventPref, error)
	GetQuietHoursForUser(ctx context.Context, userID string) (QuietHours, error)
	UpsertQuietHours(ctx context.Context, q QuietHours) (QuietHours, error)
	// Approvals rail (0075): per-person channel provisioning + card/echo/stamp write path.
	EnsureApprovalChannel(ctx context.Context, userID string) (string, error)
	PostApprovalMessage(ctx context.Context, channelID, eventKey, body string, ref ApprovalRef) (Message, bool, error)
	StampApprovalStatus(ctx context.Context, approvalID, status string) (int, error)
	ApprovalChannelIDs(ctx context.Context, approvalID string) ([]string, error)
	PostApprovalEcho(ctx context.Context, channelID, approvalID, body string, ref EventRef) (Message, bool, error)
	UserDisplayName(ctx context.Context, userID string) (string, error)
	// PR 302 N2: the yard notice fence looks up the rep's own role to ask
	// SeesCostAndMargin about it (the same X3 rule cost_gate reads).
	UserRoleCode(ctx context.Context, userID string) (string, error)
	// Channel create, DM create, membership writes (the house's own surfaces).
	CreateChannel(ctx context.Context, in CreateChannelInput, ownerUserID string) (Channel, bool, error)
	CreateDM(ctx context.Context, aUserID, bUserID string) (Channel, bool, error)
	AddChannelMember(ctx context.Context, in AddMemberInput) (ChannelMember, error)
	RemoveChannelMember(ctx context.Context, channelID, userID string) error
	// Settings panel (rule 7): visibility write and channel archive.
	SetChannelVisibility(ctx context.Context, channelID, visibility string) (Channel, error)
	ArchiveChannel(ctx context.Context, channelID string) error
	// S6 directory + join + leave + membership-rule reads/writes.
	ListDirectory(ctx context.Context, userID string, isGlobal bool, branches map[string]bool) (map[string]DirectoryEntry, error)
	ListChannelMembers(ctx context.Context, channelID string) ([]ChannelMemberEntry, error)
	GetChannelRule(ctx context.Context, channelID string) (MembershipRule, error)
	SetChannelRule(ctx context.Context, channelID string, rule MembershipRule) error
	ListChannelRules(ctx context.Context, userID string, isGlobal bool, branches map[string]bool) ([]MembershipRuleEntry, error)
	// Slice S1: the New message picker's roster, the identity read-seam the
	// repository's StaffRoster answers (active staff of one org, labels in).
	StaffRoster(ctx context.Context, orgID string) ([]StaffRosterRow, error)
	// Group rooms (walk change 7, slice S2, rulings 3 and 4): the create,
	// the any-member seat with its system line, and the rename with its own.
	CreateGroup(ctx context.Context, ownerUserID, name string, memberIDs []string) (Channel, error)
	SeatGroupMember(ctx context.Context, channelID, userID, adderName, targetName string) (ChannelMember, bool, error)
	RenameGroup(ctx context.Context, channelID, name, actorName string) (Channel, error)
	userActive(ctx context.Context, userID string) error
	SaveMessage(ctx context.Context, userID, messageID string) (SavedMessage, error)
	ListSavedMessages(ctx context.Context, userID string, limit int) ([]SavedMessage, error)
	DeleteSavedMessage(ctx context.Context, userID, messageID string) error
	SearchMessages(ctx context.Context, userID, pattern string, limit int) ([]SearchMessage, error)
	SearchPeople(ctx context.Context, orgID, pattern string, limit int) ([]SearchPerson, error)
	SearchChannels(ctx context.Context, userID, orgID, pattern string, limit int) ([]SearchChannel, error)
	// The membership walls' reads: the room's kind/branch/auto_scope, the
	// caller's row role, and the add-target's branch reach.
	channelFacts(ctx context.Context, channelID string) (channelFactsRow, error)
	userHoldsAnyRole(ctx context.Context, userID string, codes []string) (bool, error)
	memberRole(ctx context.Context, channelID, userID string) (string, error)
	memberSource(ctx context.Context, channelID, userID string) (string, error)
	userReachesBranch(ctx context.Context, userID, branchID string) (bool, error)
	// Room attachments (0403, S8)
	ListChannelAttachments(ctx context.Context, channelID string) ([]MessageAttachment, error)
	CountChannelMembers(ctx context.Context, channelID string) (int, error)
	InsertChannelAttachment(ctx context.Context, channelID, uploadedBy string, in ChannelAttachmentCreateInput) (MessageAttachment, error)
	UpsertChannelAttachmentFromMirror(ctx context.Context, channelID string, att MessageAttachment) error
	GetMessage(ctx context.Context, messageID string) (Message, error)
	// Change 7, slice S3: the reaction walls and write, the reaction summary
	// stitch, the seen-by read, the mentions view.
	messageFacts(ctx context.Context, channelID, messageID string) (messageFactsRow, error)
	AddReaction(ctx context.Context, channelID, messageID, userID, emoji string) error
	RemoveReaction(ctx context.Context, channelID, messageID, userID, emoji string) error
	reactionsForMessages(ctx context.Context, messageIDs []string, userID string) (map[string][]ReactionCount, error)
	MessageSeenBy(ctx context.Context, channelID, authorID string, at time.Time) (SeenByReport, error)
	ListMentions(ctx context.Context, userID, cursor string, limit int) (MessagePage, error)
}

// AttachmentChecker is the injected seam to verify and resolve attachments from the attachments module.
type AttachmentChecker interface {
	VerifyRoomAttachment(ctx context.Context, orgID, attachmentID, channelID string) (*AttachmentMeta, error)
	ResolveAttachmentMetas(ctx context.Context, orgID string, attachmentIDs []string) (map[string]AttachmentMeta, error)
}

// Service holds the messaging business rules. It depends only on the store seam (its own tables +
// the identity read-seam) and the optional realtime hub, no other module is imported, honouring
// the modular-monolith boundary.
type Service struct {
	repo              store
	rt                realtime
	attachmentChecker AttachmentChecker
	// zone is the dealer's business day zone source; nil = the Ontario dealer's. The rollup's day,
	// the day a routine event is filed under, is the dealer's own calendar day.
	zone houseday.Source
}

// WithAttachmentChecker attaches the attachment checker seam.
func (s *Service) WithAttachmentChecker(c AttachmentChecker) *Service {
	s.attachmentChecker = c
	return s
}

// WithZone binds the dealer's business day zone source. Nil keeps the Ontario dealer's.
func (s *Service) WithZone(src houseday.Source) *Service { s.zone = src; return s }

// rollupDayFor is the dealer's calendar day an instant belongs to (the rollup's day), in the zone the
// service's dealer chose. A failed zone read refuses: a payment filed under the wrong day is a total
// the clerk reconciles against, so it is not guessed.
func (s *Service) rollupDayFor(ctx context.Context, at time.Time) (string, error) {
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return "", err
	}
	return zone.Today(func() time.Time { return at }), nil
}

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// WithRealtime attaches the SSE hub (FR-7). Optional, poll fallback works without it.
func (s *Service) WithRealtime(rt realtime) *Service { s.rt = rt; return s }

const (
	// maxBody bounds a single message so a client can't post an unbounded blob.
	maxBody = 8 * 1024
	// defaultPage / maxPage clamp the message page size.
	defaultPage = 30
	maxPage     = 100
)

// - errors surfaced to the handler -
var (
	ErrNotMember = errors.New("messaging: caller is not a member of this channel")
	ErrNotFound  = errNotFound
	ErrEmptyBody = errors.New("messaging: message body is empty")
	// ErrBadCursor is a malformed pagination cursor (client fault). The handler maps it to HTTP 400.
	ErrBadCursor = errBadCursor
	// ErrBadAckMethod is an ack whose method is not one of the msg_acks CHECK values (client fault). The
	// handler maps it to HTTP 400, caught at the service boundary so it never reaches the DB CHECK.
	ErrBadAckMethod = errors.New("messaging: unknown ack method")
	// ErrBadAttachment is an invalid room attachment payload (client fault). The handler maps it to HTTP 400.
	ErrBadAttachment = errors.New("messaging: invalid attachment payload")
	// ErrBadReaction is a reaction whose emoji is empty past the trim or longer than the cap
	// (client fault). The handler maps it to HTTP 400, caught here so it never reaches the store.
	ErrBadReaction = errors.New("messaging: reaction must be one emoji or a short word")
	// ErrReactionsNotAllowed is a reaction aimed at a system message (EVENT, APPROVAL, any kind that
	// is not a person's TEXT post; ruling 6, row 2139). The handler maps it to 409: a conflict with
	// what the message IS, not a permission failure, the same shape as the escalation mute refusal.
	ErrReactionsNotAllowed = errors.New("messaging: system messages cannot carry reactions")
)

// ListMyChannels resyncs the caller's derived membership (FR-1, role/branch is the SoT, so a
// role/branch change is reflected immediately) then returns their channels with unread counts.
func (s *Service) ListMyChannels(ctx context.Context, c Caller) ([]Channel, error) {
	if err := s.repo.SyncMembership(ctx, c.UserID); err != nil && !errors.Is(err, errNoDB) {
		// A sync failure is non-fatal to the read: log-and-continue would hide a bug, so surface it.
		return nil, err
	}
	chans, err := s.repo.ListChannelsForUser(ctx, c.UserID)
	if err != nil {
		return nil, err
	}
	if chans == nil {
		chans = []Channel{}
	}
	return chans, nil
}

// GetMessages returns a channel's messages (cursor-paginated), enforcing channel-scoping: a caller
// who is not a member gets ErrNotMember (403), never the messages. This is the core authz check.
func (s *Service) GetMessages(ctx context.Context, c Caller, channelID, cursor string, limit int) (MessagePage, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return MessagePage{}, err
	}
	if limit <= 0 || limit > maxPage {
		if limit > maxPage {
			limit = maxPage
		} else {
			limit = defaultPage
		}
	}
	page, err := s.repo.ListMessages(ctx, channelID, cursor, limit)
	if err != nil {
		return MessagePage{}, err
	}
	if err := s.stitchReactions(ctx, page.Messages, c.UserID); err != nil {
		return MessagePage{}, err
	}
	// Coerce a nil slice to an empty one so an empty channel serializes {"messages":[]} rather than
	// {"messages":null} (mirrors ListMyChannels / GetThread). A null broke the Lit hub's
	// [...page.messages].reverse() with a TypeError, confusing cross-channel content.
	if page.Messages == nil {
		page.Messages = []Message{}
	}
	s.populateMessagePageAttachments(ctx, c.OrgID, page.Messages)
	return page, nil
}

// GetThread returns the replies under a root message, enforcing channel-scoping on BOTH the authz
// and the read: the caller must be a member of channelID, and the replies are fetched only when the
// root lives in that same channel. Binding the read to channelID (not the client-supplied rootID
// alone) closes the cross-channel thread-read IDOR, a member of #office can no longer pass a
// foreign channel's root id and read its replies.
func (s *Service) GetThread(ctx context.Context, c Caller, channelID, rootID string) ([]Message, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return nil, err
	}
	replies, err := s.repo.ListThread(ctx, channelID, rootID)
	if err != nil {
		return nil, err
	}
	if err := s.stitchReactions(ctx, replies, c.UserID); err != nil {
		return nil, err
	}
	if replies == nil {
		replies = []Message{}
	}
	s.populateMessagePageAttachments(ctx, c.OrgID, replies)
	return replies, nil
}

// stitchReactions fills each message's Reactions summary in place, one round
// trip for the page (reactionsForMessages answers per message and per emoji,
// with Mine answered against the reader). A message with no reactions keeps a
// nil slice, so its payload stays byte-identical to the pre-feature shape.
func (s *Service) stitchReactions(ctx context.Context, msgs []Message, userID string) error {
	if len(msgs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	byMessage, err := s.repo.reactionsForMessages(ctx, ids, userID)
	if err != nil {
		return err
	}
	for i := range msgs {
		if r, ok := byMessage[msgs[i].ID]; ok {
			msgs[i].Reactions = r
		}
	}
	return nil
}

// - Reactions, the seen-by read and the mentions view (change 7, slice S3) -

// maxReactionBytes caps one reaction key. An emoji is a few bytes; a word chip
// ("Thanks", "On it") is a short word. 64 bytes holds any composed emoji
// sequence and every word chip the boards draw, and stops a body-sized blob
// riding in a reaction.
const maxReactionBytes = 64

// normalizeReaction trims and bounds a reaction key. ok is false when what is
// left is empty or over the cap, so a bad key is a 400 at this boundary and
// never reaches the store.
func normalizeReaction(emoji string) (string, bool) {
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || len(emoji) > maxReactionBytes {
		return "", false
	}
	return emoji, true
}

// AddReaction seats the caller's reaction on one message, enforcing channel
// membership FIRST, then the channel binding (messageFacts: a message of
// another channel is not-found, no existence leaks), then ruling 6: a reaction
// is none on EVENT, approval or any other system message (ErrReactionsNotAllowed,
// 409). The write is idempotent; the answer is the message's fresh summary with
// Mine answered for the caller.
func (s *Service) AddReaction(ctx context.Context, c Caller, channelID, messageID, emoji string) ([]ReactionCount, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return nil, err
	}
	facts, err := s.repo.messageFacts(ctx, channelID, messageID)
	if err != nil {
		return nil, err
	}
	if facts.Kind != MsgText {
		return nil, ErrReactionsNotAllowed
	}
	key, ok := normalizeReaction(emoji)
	if !ok {
		return nil, ErrBadReaction
	}
	if err := s.repo.AddReaction(ctx, channelID, messageID, c.UserID, key); err != nil {
		return nil, err
	}
	byMessage, err := s.repo.reactionsForMessages(ctx, []string{messageID}, c.UserID)
	if err != nil {
		return nil, err
	}
	return byMessage[messageID], nil
}

// RemoveReaction drops the caller's one emoji on one message through the same
// walls the add runs (membership, the channel binding, ruling 6 both ways).
// Removing what is not there is not an error: the caller's end state is the
// answer, and the fresh summary reports it.
func (s *Service) RemoveReaction(ctx context.Context, c Caller, channelID, messageID, emoji string) ([]ReactionCount, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return nil, err
	}
	facts, err := s.repo.messageFacts(ctx, channelID, messageID)
	if err != nil {
		return nil, err
	}
	if facts.Kind != MsgText {
		return nil, ErrReactionsNotAllowed
	}
	key, ok := normalizeReaction(emoji)
	if !ok {
		return nil, ErrBadReaction
	}
	if err := s.repo.RemoveReaction(ctx, channelID, messageID, c.UserID, key); err != nil {
		return nil, err
	}
	byMessage, err := s.repo.reactionsForMessages(ctx, []string{messageID}, c.UserID)
	if err != nil {
		return nil, err
	}
	return byMessage[messageID], nil
}

// MessageSeenBy answers the board's "Seen N" for one message, enforcing
// membership and the channel binding first. The author is excepted by the read
// itself (a person is never seen by their own post); a system message, whose
// author is nobody, counts every member past it.
func (s *Service) MessageSeenBy(ctx context.Context, c Caller, channelID, messageID string) (SeenByReport, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return SeenByReport{}, err
	}
	facts, err := s.repo.messageFacts(ctx, channelID, messageID)
	if err != nil {
		return SeenByReport{}, err
	}
	return s.repo.MessageSeenBy(ctx, channelID, facts.AuthorID, facts.CreatedAt)
}

// ListMentions returns the caller's mentions view (ruling 5: the messages
// naming the caller or the caller's role, in the rooms the caller holds,
// newest first), cursor paginated like the stream, with the same reaction
// stitch the stream carries so a mention row renders whole.
func (s *Service) ListMentions(ctx context.Context, c Caller, cursor string, limit int) (MessagePage, error) {
	if limit <= 0 || limit > maxPage {
		if limit > maxPage {
			limit = maxPage
		} else {
			limit = defaultPage
		}
	}
	page, err := s.repo.ListMentions(ctx, c.UserID, cursor, limit)
	if err != nil {
		return MessagePage{}, err
	}
	if err := s.stitchReactions(ctx, page.Messages, c.UserID); err != nil {
		return MessagePage{}, err
	}
	if page.Messages == nil {
		page.Messages = []Message{}
	}
	return page, nil
}

// PostMessage posts a TEXT message to a channel (top-level or a thread reply when threadRootID is
// set), enforcing channel-scoping. It trims + bounds the body, fans out to the realtime hub, and
// returns the persisted message.
func (s *Service) PostMessage(ctx context.Context, c Caller, channelID, body, threadRootID string) (Message, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return Message{}, err
	}
	body = trimMessageBody(body)
	if body == "" {
		return Message{}, ErrEmptyBody
	}
	m, err := s.repo.InsertMessage(ctx, channelID, c.UserID, body, threadRootID)
	if err != nil {
		return Message{}, err
	}
	s.fanout(channelID, m)
	return m, nil
}

// trimMessageBody is the post's own trim and bound, shared with the edit
// route so an edit cannot carry what a post cannot. Trimmed, and bounded to
// maxBody on a rune boundary so a multibyte UTF-8 rune is never split (a
// split rune would serialize as U+FFFD and corrupt the trailing character):
// walk back from maxBody to the start byte of the rune that straddles the
// cut.
func trimMessageBody(body string) string {
	body = strings.TrimSpace(body)
	if len(body) > maxBody {
		cut := maxBody
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		body = body[:cut]
	}
	return body
}

// MarkRead advances the caller's read cursor for a channel to now (drives unread counts). Requires
// membership.
func (s *Service) MarkRead(ctx context.Context, c Caller, channelID string) error {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return err
	}
	return s.repo.MarkRead(ctx, channelID, c.UserID, time.Now())
}

// - Feature 1: Screen-Takeover forced acknowledgement (0071) -

// AckMessage records the caller's acknowledgement of a forced-ack message, authorizing channel
// membership FIRST (exactly like MarkRead, a non-member can neither see nor ack a channel's message).
// method defaults to TAP and is validated at this boundary (ErrBadAckMethod → 400) so a bad value
// never reaches the msg_acks CHECK. The upsert is idempotent: re-tapping simply refreshes the row.
// The repository additionally binds the write to (messageID, channelID), so a member of one channel
// cannot ack a foreign channel's message id (the same channel-binding guard GetThread uses).
func (s *Service) AckMessage(ctx context.Context, c Caller, channelID, messageID, method string) (Ack, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return Ack{}, err
	}
	m, ok := normalizeAckMethod(method)
	if !ok {
		return Ack{}, ErrBadAckMethod
	}
	return s.repo.AckMessage(ctx, channelID, messageID, c.UserID, m)
}

// UnackedRequiresAck returns the caller's latest un-acknowledged forced-ack messages (newest-first) -
// the kiosk screen-takeover polls this and blocks on the first one. Authorization is intrinsic to the
// query: it returns only messages in channels the caller is a member of, so no cross-channel leak is
// possible without a separate membership check here. A nil slice is coerced to empty so the endpoint
// serializes {"messages":[]} (mirrors GetMessages).
func (s *Service) UnackedRequiresAck(ctx context.Context, c Caller) ([]Message, error) {
	msgs, err := s.repo.UnackedRequiresAck(ctx, c.UserID)
	if err != nil {
		return nil, err
	}
	if msgs == nil {
		msgs = []Message{}
	}
	return msgs, nil
}

// - Feature A: notification prefs + the shouldNotify predicate (0069) -

// GetNotifyPrefs returns the caller's notification prefs for a channel (defaults when unset),
// requiring membership, a non-member can neither see nor tune a channel they cannot reach.
func (s *Service) GetNotifyPrefs(ctx context.Context, c Caller, channelID string) (NotificationPrefs, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return NotificationPrefs{}, err
	}
	return s.repo.GetNotificationPrefs(ctx, channelID, c.UserID)
}

// SetNotifyPrefs upserts the caller's notification prefs for a channel, requiring membership. An
// OMITTED patch field keeps the caller's CURRENT value (merge-then-upsert), so a partial PUT never
// silently coerces an unspecified toggle to false.
//
// DIB-269: a patch that would SILENCE an ESCALATION-tier channel is REFUSED (ErrChannelUnmutable →
// 409) before anything is written. Refusal rather than silent override is the deliberate choice: a
// user who believes they muted a channel and still gets paged has been lied to by the UI. The check
// covers BOTH silencing routes (mode=MUTED and the legacy muted=true) because both still exist, and
// the view (0137) forces ESCALATION back to mode ALL independently, two guards, so a legacy row or a
// future endpoint that forgets this check still cannot silence an escalation.
func (s *Service) SetNotifyPrefs(ctx context.Context, c Caller, channelID string, patch PrefsPatch) (NotificationPrefs, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return NotificationPrefs{}, err
	}
	if silencingPatch(patch) {
		tier, err := s.repo.ChannelTier(ctx, channelID)
		if err != nil && !errors.Is(err, errNoDB) {
			return NotificationPrefs{}, err
		}
		if unmutableTier(tier) {
			return NotificationPrefs{}, ErrChannelUnmutable
		}
	}
	cur, err := s.repo.GetNotificationPrefs(ctx, channelID, c.UserID) // defaults when unset
	if err != nil {
		return NotificationPrefs{}, err
	}
	merged, ok := mergePrefs(cur, patch)
	if !ok {
		return NotificationPrefs{}, ErrBadMode
	}
	return s.repo.UpsertNotificationPrefs(ctx, merged)
}

// ChannelTier returns a channel's notification tier, the server-side companion input to ShouldNotify
// at the per-recipient notification-decision point.
func (s *Service) ChannelTier(ctx context.Context, channelID string) (string, error) {
	return s.repo.ChannelTier(ctx, channelID)
}

// ShouldNotify is the exported seam for the per-recipient desktop-notification decision. The future
// desktop-push / SSE delivery path calls this once it has a recipient's prefs + the channel tier (see
// the TODO at consumer.route). It wraps the pure shouldNotify predicate.
func (s *Service) ShouldNotify(pref NotificationPrefs, channelTier string) bool {
	return shouldNotify(pref, channelTier)
}

// shouldNotify is the per-recipient desktop-notification predicate (Feature A). Given one user's prefs
// for a channel and the channel's tier, it decides whether a new post should raise a desktop
// notification for that user. The rule: a FIREHOSE channel's post bypasses `muted` when the user's
// escalation_override is on (a critical company-wide alert still reaches them); otherwise a muted
// channel suppresses the notification; otherwise the per-channel desktop toggle decides. Pure + total,
// so the FIREHOSE/escalation/mute matrix is unit-tested DB-free.
func shouldNotify(pref NotificationPrefs, channelTier string) bool {
	// FIREHOSE escalation override: a muted firehose still notifies when the user opted into overrides.
	if channelTier == TierFirehose && pref.EscalationOverride {
		return true
	}
	// A muted channel suppresses everything else.
	if pref.Muted {
		return false
	}
	// Otherwise honour the per-channel desktop toggle.
	return pref.DesktopEnabled
}

// UnreadSummary returns the caller's per-channel unread counts + mention flags (FR-7). This is the
// SSE poll-fallback endpoint (hit every few seconds per active session), so it deliberately does NOT
// resync membership, a full write transaction on this hot path is wasteful. Membership is already
// materialized by ListMyChannels (hub-open / channel-list reload); a mid-session role/branch change
// (rare, admin-initiated) reflects on the next channel-list load rather than the next badge poll.
func (s *Service) UnreadSummary(ctx context.Context, c Caller) (UnreadCounts, error) {
	return s.repo.UnreadSummary(ctx, c.UserID)
}

// assertMember is the single channel-scoping gate. It distinguishes 404 (channel gone) from 403
// (channel exists, caller not a member) so the API is precise without leaking channel existence to
// a non-member beyond the 403 they'd get anyway.
func (s *Service) assertMember(ctx context.Context, c Caller, channelID string) error {
	ok, err := s.repo.IsMember(ctx, channelID, c.UserID)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	exists, err := s.repo.channelExists(ctx, channelID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrNotMember
}

// fanout pushes a new message to the realtime hub if one is attached (best-effort; a nil hub means
// clients short-poll instead).
func (s *Service) fanout(channelID string, m Message) {
	if s.rt != nil {
		s.rt.PublishMessage(channelID, m)
	}
}

// - Approvals rail (0075) -

// userNotifier is the SSE hub's own-request delivery seam (SSEHub.NotifyUser). It is discovered by type
// assertion off the realtime seam so the base realtime interface (and its test fakes) stays unchanged.
type userNotifier interface {
	NotifyUser(userID, event string, data any)
}

// NotifyUser pushes an event straight to one user's open sessions (own-request approval status), bypassing
// channel membership. No-op when the realtime seam is nil or does not support it (poll fallback covers it).
func (s *Service) NotifyUser(userID, event string, data any) {
	if s.rt == nil {
		return
	}
	if n, ok := s.rt.(userNotifier); ok {
		n.NotifyUser(userID, event, data)
	}
}

// EnsureApprovalChannel provisions (idempotently) the approver's dedicated APPROVAL channel and returns
// its id, the thin service wrapper the reconcile sweep + fan-out share.
func (s *Service) EnsureApprovalChannel(ctx context.Context, userID string) (string, error) {
	return s.repo.EnsureApprovalChannel(ctx, userID)
}

// PostApprovalToUserChannels fans one APPROVAL card out to each approver's channel (ensuring it first),
// idempotent per channel on the card event_key so a re-notify or reconcile re-fan never duplicates. It is
// best-effort per approver: one approver's failure is captured and skipped so the others still get their
// card (a miss self-heals on the next sweep). An empty approver set is a clean no-op, the caller logs the
// zero-eligible edge; the request stays fully decidable via the existing pending-approvals surface.
func (s *Service) PostApprovalToUserChannels(ctx context.Context, approverIDs []string, ref ApprovalRef) error {
	if ref.Status == "" {
		ref.Status = "PENDING"
	}
	key := approvalCardKey(ref.ApprovalID)
	body := approvalCardBody(ref)
	var firstErr error
	for _, uid := range approverIDs {
		if uid == "" {
			continue
		}
		channelID, err := s.repo.EnsureApprovalChannel(ctx, uid)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("ensure channel for %s: %w", uid, err)
			}
			continue
		}
		m, inserted, err := s.repo.PostApprovalMessage(ctx, channelID, key, body, ref)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("post card for %s: %w", uid, err)
			}
			continue
		}
		if inserted {
			s.fanout(channelID, m)
		}
	}
	return firstErr
}

// StampApprovalStatus stamps the display status across every card copy of a request (the decision echo's
// display half). Live truth stays the request row; this is display sync only.
func (s *Service) StampApprovalStatus(ctx context.Context, approvalID, status string) error {
	_, err := s.repo.StampApprovalStatus(ctx, approvalID, status)
	return err
}

// PostApprovalDecision reflects a decided request across the rail: it stamps the display status on every
// card copy AND posts the decision EVENT echo into each channel that received a card, both idempotent
// (stamp is an UPDATE; echo dedups on the decided event_key). The decider's display name is resolved from
// decidedBy (best-effort, a miss renders a neutral label); note renders the echo body's reason clause.
func (s *Service) PostApprovalDecision(ctx context.Context, approvalID, status, decidedBy, note string) error {
	if _, err := s.repo.StampApprovalStatus(ctx, approvalID, status); err != nil {
		return err
	}
	channels, err := s.repo.ApprovalChannelIDs(ctx, approvalID)
	if err != nil {
		return err
	}
	deciderName, _ := s.repo.UserDisplayName(ctx, decidedBy) // best-effort; "" renders a neutral label
	body := approvalDecisionBody(status, deciderName, note)
	ref := EventRef{Topic: "approval.decided", Summary: body}
	var firstErr error
	for _, ch := range channels {
		m, inserted, err := s.repo.PostApprovalEcho(ctx, ch, approvalID, body, ref)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if inserted {
			s.fanout(ch, m)
		}
	}
	return firstErr
}

// approvalCardBody renders the card's human summary from its typed ref (policy-aware).
func approvalCardBody(ref ApprovalRef) string {
	switch ref.PolicyType {
	case "JE_OVER_THRESHOLD":
		return fmt.Sprintf("Journal entry of %s is at/over the approval threshold and needs your approval.", formatCents(ref.AmountCents))
	case "MARGIN_BELOW_MINIMUM":
		return "A below-minimum margin override needs your approval."
	default:
		return "An approval request needs your decision."
	}
}

// approvalDecisionBody renders the decision echo ("Approved/Declined by <name>, <note>").
func approvalDecisionBody(status, deciderName, note string) string {
	verb := "Decided"
	switch status {
	case "APPROVED":
		verb = "Approved"
	case "REJECTED":
		verb = "Declined"
	}
	who := deciderName
	if who == "" {
		who = "a manager"
	}
	b := verb + " by " + who
	if strings.TrimSpace(note) != "" {
		b += ", " + note
	}
	return b
}

// formatCents renders integer cents as CAD dollars (e.g. 500000 -> "$5,000.00").
func formatCents(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	dollars := cents / 100
	frac := cents % 100
	// Group the dollar part with thousands separators.
	ds := fmt.Sprintf("%d", dollars)
	var grouped []byte
	for i, c := range []byte(ds) {
		if i > 0 && (len(ds)-i)%3 == 0 {
			grouped = append(grouped, ',')
		}
		grouped = append(grouped, c)
	}
	out := fmt.Sprintf("$%s.%02d", string(grouped), frac)
	if neg {
		out = "-" + out
	}
	return out
}

// - FR-3: event → channel post (called by the consumer) -
// PostEventToChannelSlug writes a system EVENT message to the channel identified by slug (e.g.
// yard-trenton), idempotent on eventKey. It fans out to realtime on a fresh insert. Returns the
// message and inserted flag; a redelivered event is a silent no-op (inserted=false). requiresAck
// (0071) flags a forced-ack post the kiosk must block on, set only for the small high-priority
// allowlist (consumer.go: ackRequiredTopics); ordinary posts (and the portal bridge) pass false.
func (s *Service) PostEventToChannelSlug(ctx context.Context, slug, eventKey, body string, ref EventRef, requiresAck bool) (Message, bool, error) {
	channelID, err := s.repo.ChannelIDBySlug(ctx, slug)
	if err != nil {
		return Message{}, false, fmt.Errorf("route event to %q: %w", slug, err)
	}
	m, inserted, err := s.repo.InsertEventMessage(ctx, channelID, eventKey, body, ref, requiresAck)
	if err != nil {
		return Message{}, false, err
	}
	if inserted {
		s.fanout(channelID, m)
	}
	return m, inserted, nil
}

// YardSlugForBranch maps a branch id to its yard channel slug (FR-3 routing helper).
func (s *Service) YardSlugForBranch(ctx context.Context, branchID string) (string, error) {
	return s.repo.BranchYardSlug(ctx, branchID)
}

// - Feature B: HR/audit export (0070) -

// ExportMessages returns admin-masked message rows for the HR/audit export. Authorization is the ADMIN
// gate at the route (mirroring the audit admin viewer); this method applies the server-side
// projectForExport masking before the rows leave, exactly as audit projects on its read seam. This is
// a READ-only seam over msg_messages, audit remains the sole writer of audit_log (fully decoupled).
func (s *Service) ExportMessages(ctx context.Context, f ExportFilter) ([]ExportMessage, error) {
	rows, err := s.repo.ExportMessages(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]ExportMessage, 0, len(rows))
	for _, m := range rows {
		out = append(out, projectForExport(m))
	}
	return out, nil
}

// projectForExport masks one message for the admin HR/audit export (mirrors audit.projectForOperator: a
// server-side projection that strips sensitive content before the response leaves). A soft-deleted
// message is rendered as a tombstone (its body was already dropped by the reader). A cleartext-looking
// identifier, a run of 7+ digits, the audit PAN/SIN/registry heuristic, in a body is redacted so a
// raw identifier can never leave the system in an export. Author attribution is preserved: the export
// is an admin-only surface and HR review needs WHO said WHAT.
//
// TODO(decision: HR/audit masking policy): whether author identity should itself be masked for a
// privacy-review export (vs. an accountability export) is gated on the HR/legal decision. When settled,
// tighten this projection (e.g. add a masked-actor rendering like audit.maskActor) accordingly.
func projectForExport(m Message) ExportMessage {
	body := m.Body
	switch {
	case m.DeletedAt != nil:
		body = "[deleted]" // tombstone (reader already blanked the stored body)
	case looksLikeCleartextID(body):
		body = "[redacted]" // never let a raw PAN/SIN/registry number leave in an export
	}
	return ExportMessage{
		ID:         m.ID,
		ChannelID:  m.ChannelID,
		AuthorID:   m.AuthorID,
		AuthorName: m.AuthorName,
		Kind:       m.Kind,
		Body:       body,
		CreatedAt:  m.CreatedAt,
		EditedAt:   m.EditedAt,
		DeletedAt:  m.DeletedAt,
	}
}

// looksLikeCleartextID reports whether s contains a run of 7+ consecutive digits (a PAN 13-19, SIN 9,
// or registry number ~10), the same cleartext-identifier heuristic the audit masking boundary uses. A
// masked ref like "•••• 4321" (4 digits) passes. Self-contained here so messaging does not import
// audit's unexported helper (module boundary).
func looksLikeCleartextID(s string) bool {
	const threshold = 7
	run := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			run++
			if run >= threshold {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

func (s *Service) populateMessagePageAttachments(ctx context.Context, orgID string, msgs []Message) {
	if s.attachmentChecker == nil || len(msgs) == 0 {
		return
	}
	var aidList []string
	for _, m := range msgs {
		for _, a := range m.Attachments {
			if a.AttachmentID != nil && *a.AttachmentID != "" {
				aidList = append(aidList, *a.AttachmentID)
			}
		}
	}
	if len(aidList) == 0 {
		return
	}
	metas, err := s.attachmentChecker.ResolveAttachmentMetas(ctx, orgID, aidList)
	if err != nil || metas == nil {
		return
	}
	for i := range msgs {
		for j := range msgs[i].Attachments {
			if msgs[i].Attachments[j].AttachmentID != nil {
				if meta, ok := metas[*msgs[i].Attachments[j].AttachmentID]; ok {
					msgs[i].Attachments[j].AppwriteFileID = meta.AppwriteFileID
					msgs[i].Attachments[j].Bucket = meta.Bucket
					if msgs[i].Attachments[j].SizeBytes == 0 && meta.SizeBytes > 0 {
						msgs[i].Attachments[j].SizeBytes = meta.SizeBytes
					}
					if msgs[i].Attachments[j].ContentType == "" && meta.Mime != "" {
						msgs[i].Attachments[j].ContentType = meta.Mime
					}
				}
			}
		}
	}
}

// GetChannelAttachments returns the photos, files, and member count for a room.
func (s *Service) GetChannelAttachments(ctx context.Context, c Caller, channelID string) (ChannelAttachmentsResponse, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return ChannelAttachmentsResponse{}, err
	}
	atts, err := s.repo.ListChannelAttachments(ctx, channelID)
	if err != nil {
		return ChannelAttachmentsResponse{}, err
	}
	count, err := s.repo.CountChannelMembers(ctx, channelID)
	if err != nil {
		return ChannelAttachmentsResponse{}, err
	}

	if s.attachmentChecker != nil && len(atts) > 0 {
		var aidList []string
		for _, a := range atts {
			if a.AttachmentID != nil && *a.AttachmentID != "" {
				aidList = append(aidList, *a.AttachmentID)
			}
		}
		if len(aidList) > 0 {
			metas, err := s.attachmentChecker.ResolveAttachmentMetas(ctx, c.OrgID, aidList)
			if err == nil && metas != nil {
				for i := range atts {
					if atts[i].AttachmentID != nil {
						if meta, ok := metas[*atts[i].AttachmentID]; ok {
							atts[i].AppwriteFileID = meta.AppwriteFileID
							atts[i].Bucket = meta.Bucket
							if atts[i].SizeBytes == 0 && meta.SizeBytes > 0 {
								atts[i].SizeBytes = meta.SizeBytes
							}
							if atts[i].ContentType == "" && meta.Mime != "" {
								atts[i].ContentType = meta.Mime
							}
						}
					}
				}
			}
		}
	}

	photos := make([]MessageAttachment, 0)
	files := make([]MessageAttachment, 0)
	for _, a := range atts {
		if a.Kind == "photo" {
			photos = append(photos, a)
		} else {
			files = append(files, a)
		}
	}

	return ChannelAttachmentsResponse{
		Photos:      photos,
		Files:       files,
		MemberCount: count,
	}, nil
}

// CreateChannelAttachment links an attachment to a channel or message.
func (s *Service) CreateChannelAttachment(ctx context.Context, c Caller, channelID string, in ChannelAttachmentCreateInput) (MessageAttachment, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return MessageAttachment{}, err
	}
	if strings.TrimSpace(in.Filename) == "" {
		return MessageAttachment{}, ErrBadAttachment
	}
	if _, err := uuid.Parse(in.AttachmentID); err != nil {
		return MessageAttachment{}, ErrBadAttachment
	}
	if in.MessageID != nil && *in.MessageID != "" {
		if _, err := uuid.Parse(*in.MessageID); err != nil {
			return MessageAttachment{}, ErrBadAttachment
		}
		msg, err := s.repo.GetMessage(ctx, *in.MessageID)
		if err != nil {
			return MessageAttachment{}, ErrNotFound
		}
		if msg.ChannelID != channelID {
			return MessageAttachment{}, ErrNotFound
		}
	}
	var meta *AttachmentMeta
	if s.attachmentChecker != nil {
		m, err := s.attachmentChecker.VerifyRoomAttachment(ctx, c.OrgID, in.AttachmentID, channelID)
		if err != nil {
			return MessageAttachment{}, ErrNotFound
		}
		meta = m
		if in.ContentType == "" && m.Mime != "" {
			in.ContentType = m.Mime
		}
		if in.SizeBytes == nil && m.SizeBytes > 0 {
			in.SizeBytes = &m.SizeBytes
		}
		if in.Kind == "" && m.Kind != "" {
			if m.Kind == "photo" {
				in.Kind = "photo"
			} else {
				in.Kind = "document"
			}
		}
	}

	att, err := s.repo.InsertChannelAttachment(ctx, channelID, c.UserID, in)
	if err != nil {
		return MessageAttachment{}, err
	}
	if meta != nil {
		att.AppwriteFileID = meta.AppwriteFileID
		att.Bucket = meta.Bucket
	}
	return att, nil
}

// MirrorRoomAttachment mirrors an attachment link to msg_attachments.
func (s *Service) MirrorRoomAttachment(ctx context.Context, channelID string, meta AttachmentMeta, uploadedBy, filename, role string) error {
	name := filename
	if name == "" {
		if role != "" {
			name = role
		} else {
			name = meta.AppwriteFileID
		}
	}
	kind := "document"
	if meta.Kind == "photo" {
		kind = "photo"
	}
	var uPtr *string
	if uploadedBy != "" {
		uPtr = &uploadedBy
	}
	attID := meta.ID
	att := MessageAttachment{
		ChannelID:    &channelID,
		AttachmentID: &attID,
		Filename:     name,
		ContentType:  meta.Mime,
		SizeBytes:    meta.SizeBytes,
		Kind:         kind,
		UploadedBy:   uPtr,
		CreatedAt:    time.Now(),
	}
	return s.repo.UpsertChannelAttachmentFromMirror(ctx, channelID, att)
}

// - Slice S7: per-person notification settings (event-category toggles + quiet hours), 0401 -

// NotificationSettings is the envelope the GET /notification-settings route returns: the closed
// 6-category set in board order (always, with the per-category default filled when a user has no
// stored row) and the user's quiet-hours row (always, with the shipped defaults when no row). A
// partial payload would force the surface to invent the missing half, and "no row" and "default"
// must look identical to the reader because they mean the same thing to the system.
type NotificationSettings struct {
	EventPrefs []PersonEventPref `json:"event_prefs"`
	QuietHours QuietHours        `json:"quiet_hours"`
}

// NotificationSettingsPatch is the PUT body. event_prefs is OPTIONAL (a PUT may touch only quiet
// hours), quiet_hours is OPTIONAL (a PUT may touch only categories). Inside event_prefs, every
// entry carries a category; in_app / push are pointers so an omitted field keeps the caller's
// current value on the upsert (the same omitted-fields-keep-their-current-value rule PrefsPatch
// uses, applied per-category).
type NotificationSettingsPatch struct {
	EventPrefs []PersonEventPrefsPatch `json:"event_prefs,omitempty"`
	QuietHours QuietHoursPatch         `json:"quiet_hours,omitempty"`
}

// ErrBadQuietHours is a quiet-hours field that failed HH:MM or timezone validation (caller fault
// -> 400). Surfaced so the handler can map it without writing a Postgres CHECK violation -> 500.
var ErrBadQuietHours = errors.New("messaging: invalid quiet-hours field")

// GetNotificationSettings returns the caller's full notification-settings envelope, ALWAYS the
// closed 6-category set in board order with the per-category default filled when no row exists,
// and the user's quiet-hours row with shipped defaults when no row. Caller is taken from the
// verified StaffContext (the route stamps it via RequireStaff); there is no path through which one
// staffer can read another's notification choices.
func (s *Service) GetNotificationSettings(ctx context.Context, c Caller) (NotificationSettings, error) {
	stored, err := s.repo.GetPersonEventPrefsForUser(ctx, c.UserID)
	if err != nil {
		return NotificationSettings{}, err
	}
	qh, err := s.repo.GetQuietHoursForUser(ctx, c.UserID)
	if err != nil {
		return NotificationSettings{}, err
	}
	prefs := make([]PersonEventPref, 0, len(EventCategories))
	for _, cat := range EventCategories {
		if p, ok := stored[cat]; ok {
			prefs = append(prefs, p)
			continue
		}
		prefs = append(prefs, DefaultPersonEventPref(c.UserID, cat))
	}
	return NotificationSettings{EventPrefs: prefs, QuietHours: qh}, nil
}

// SetNotificationSettings upserts the caller's notification-settings envelope. event_prefs is
// optional; quiet_hours is optional; both may be sent in the same PUT. Validation is at the
// service boundary (ErrBadQuietHours / ErrBadEventCategory -> 400) so a bad value never reaches the
// SQL CHECKs. Each category row is merged over the caller's CURRENT stored values so a partial
// PUT does not silently coerce an omitted toggle. Returns the full updated envelope.
//
// Caller isolation is intrinsic: c.UserID comes from the verified StaffContext (never from the
// body or a query param), so Alice's PUT cannot alter Bob's settings.
func (s *Service) SetNotificationSettings(ctx context.Context, c Caller, patch NotificationSettingsPatch) (NotificationSettings, error) {
	// Validate quiet_hours first; a bad HH:MM / timezone in the patch must fail before any row is
	// written (defence-in-depth: a half-applied write is worse than a refused write).
	if patch.QuietHours.StartLocal != nil {
		if _, _, ok := parseHHMM(*patch.QuietHours.StartLocal); !ok {
			return NotificationSettings{}, ErrBadHHMM
		}
	}
	if patch.QuietHours.EndLocal != nil {
		if _, _, ok := parseHHMM(*patch.QuietHours.EndLocal); !ok {
			return NotificationSettings{}, ErrBadHHMM
		}
	}
	if patch.QuietHours.Timezone != nil {
		if loc, err := time.LoadLocation(*patch.QuietHours.Timezone); err != nil || loc == nil {
			return NotificationSettings{}, ErrBadTimezone
		}
	}

	// Upsert each category row. Per-category validation: the closed-set check runs before the
	// upsert so a single bad category refuses the WHOLE request (a partial write of the others is
	// a half-applied change the surface cannot honestly present back).
	for _, ep := range patch.EventPrefs {
		if _, ok := normalizeEventCategory(ep.Category); !ok {
			return NotificationSettings{}, ErrBadEventCategory
		}
		// Merge over the caller's CURRENT stored row so an omitted field keeps its value (the
		// omitted-fields-keep-their-current-value rule).
		cur := DefaultPersonEventPref(c.UserID, ep.Category)
		if stored, ok := s.lookupStoredEventPref(ctx, c.UserID, ep.Category); ok {
			cur = stored
		}
		if ep.InApp != nil {
			cur.InApp = *ep.InApp
		}
		if ep.Push != nil {
			cur.Push = *ep.Push
		}
		if _, err := s.repo.UpsertPersonEventPref(ctx, cur); err != nil {
			return NotificationSettings{}, err
		}
	}

	// Upsert quiet hours if any field was sent. A truly empty patch (no event_prefs, no
	// quiet_hours fields) is a clean no-op: nothing to merge, nothing to write.
	if qhHasAny(patch.QuietHours) {
		cur, _ := s.repo.GetQuietHoursForUser(ctx, c.UserID)
		if cur.UserID == "" {
			cur = DefaultQuietHours(c.UserID) // guard against an empty zero-value from a fresh repo
		}
		if patch.QuietHours.Enabled != nil {
			cur.Enabled = *patch.QuietHours.Enabled
		}
		if patch.QuietHours.StartLocal != nil {
			cur.StartLocal = *patch.QuietHours.StartLocal
		}
		if patch.QuietHours.EndLocal != nil {
			cur.EndLocal = *patch.QuietHours.EndLocal
		}
		if patch.QuietHours.Timezone != nil {
			cur.Timezone = *patch.QuietHours.Timezone
		}
		if patch.QuietHours.BypassUserIDs != nil {
			cur.BypassUserIDs = patch.QuietHours.BypassUserIDs
		}
		if _, err := s.repo.UpsertQuietHours(ctx, cur); err != nil {
			return NotificationSettings{}, err
		}
	}

	// Re-read the full envelope so the response mirrors what a follow-up GET would return (the
	// same honesty GetNotifyPrefs / GetPaymentRules apply: round-trip the truth).
	return s.GetNotificationSettings(ctx, c)
}

// lookupStoredEventPref is the merge-over-current helper for the category upsert. It returns
// ok=false when no row is stored (the caller falls back to DefaultPersonEventPref). A repo
// failure surfaces as an empty map (the upsert that follows will overwrite the defaults with the
// patch's values, which is what the caller asked for).
func (s *Service) lookupStoredEventPref(ctx context.Context, userID, category string) (PersonEventPref, bool) {
	all, err := s.repo.GetPersonEventPrefsForUser(ctx, userID)
	if err != nil {
		return PersonEventPref{}, false
	}
	p, ok := all[category]
	return p, ok
}

// qhHasAny reports whether a QuietHoursPatch carries ANY field. A truly empty patch is a
// no-op; we don't even open a transaction for it (the brief's "no row" / "default" honesty).
func qhHasAny(p QuietHoursPatch) bool {
	return p.Enabled != nil || p.StartLocal != nil || p.EndLocal != nil ||
		p.Timezone != nil || p.BypassUserIDs != nil
}

// ErrBadEventCategory is a category outside EventCategories (mirrors ErrBadPaymentRule, ErrBadMode);
// the handler maps it to HTTP 400.
var ErrBadEventCategory = errors.New("messaging: unknown event category")

// ShouldDeliverPushOnService is the Service-level wrapper that the SSE/desktop-push path will
// eventually call (the per-recipient fan-out seam the consumer.go comment names). It looks up the
// caller's stored prefs (caller pref + per-category pref + quiet hours) and runs the pure
// ShouldDeliverPush predicate. The full stored-set read is the fan-out's hot path; it is DB-free
// in the unit-test seam via Service.repo overrides.
func (s *Service) ShouldDeliverPushOnService(ctx context.Context, userID, channelID, eventCategory, dmSenderID string, isForcedAck bool) bool {
	pref, err := s.repo.GetNotificationPrefs(ctx, channelID, userID)
	if err != nil {
		return false // defensive: a missing prefs row returns the defaults; a DB failure is "no push"
	}
	tier, err := s.repo.ChannelTier(ctx, channelID)
	if err != nil {
		tier = TierStandard // mirrors the existing predicate: a missing tier is the safe default
	}
	var classPref PersonEventPref
	if eventCategory != "" {
		stored, err := s.repo.GetPersonEventPrefsForUser(ctx, userID)
		if err == nil {
			if p, ok := stored[eventCategory]; ok {
				classPref = p
			} else {
				classPref = DefaultPersonEventPref(userID, eventCategory)
			}
		}
	}
	qh, err := s.repo.GetQuietHoursForUser(ctx, userID)
	if err != nil {
		qh = DefaultQuietHours(userID)
	}
	return ShouldDeliverPush(time.Now(), pref, tier, isForcedAck, eventCategory, classPref, qh, dmSenderID)
}
