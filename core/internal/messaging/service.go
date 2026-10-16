package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
	AddChannelMember(ctx context.Context, channelID, userID string) (ChannelMember, error)
	RemoveChannelMember(ctx context.Context, channelID, userID string) error
	// Slice S1: the New message picker's roster, the identity read-seam the
	// repository's StaffRoster answers (active staff of one org, labels in).
	StaffRoster(ctx context.Context, orgID string) ([]StaffRosterRow, error)
	// Group rooms (walk change 7, slice S2, rulings 3 and 4): the create,
	// the any-member seat with its system line, and the rename with its own.
	CreateGroup(ctx context.Context, ownerUserID, name string, memberIDs []string) (Channel, error)
	SeatGroupMember(ctx context.Context, channelID, userID, adderName, targetName string) (ChannelMember, bool, error)
	RenameGroup(ctx context.Context, channelID, name, actorName string) (Channel, error)
	userActive(ctx context.Context, userID string) error
	// The membership walls' reads: the room's kind/branch/auto_scope, the
	// caller's row role, and the add-target's branch reach.
	channelFacts(ctx context.Context, channelID string) (channelFactsRow, error)
	userHoldsAnyRole(ctx context.Context, userID string, codes []string) (bool, error)
	memberRole(ctx context.Context, channelID, userID string) (string, error)
	userReachesBranch(ctx context.Context, userID, branchID string) (bool, error)
}

// Service holds the messaging business rules. It depends only on the store seam (its own tables +
// the identity read-seam) and the optional realtime hub, no other module is imported, honouring
// the modular-monolith boundary.
type Service struct {
	repo store
	rt   realtime
	// zone is the dealer's business day zone source; nil = the Ontario dealer's. The rollup's day,
	// the day a routine event is filed under, is the dealer's own calendar day.
	zone houseday.Source
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
	// Coerce a nil slice to an empty one so an empty channel serializes {"messages":[]} rather than
	// {"messages":null} (mirrors ListMyChannels / GetThread). A null broke the Lit hub's
	// [...page.messages].reverse() with a TypeError, confusing cross-channel content.
	if page.Messages == nil {
		page.Messages = []Message{}
	}
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
	if replies == nil {
		replies = []Message{}
	}
	return replies, nil
}

// PostMessage posts a TEXT message to a channel (top-level or a thread reply when threadRootID is
// set), enforcing channel-scoping. It trims + bounds the body, fans out to the realtime hub, and
// returns the persisted message.
func (s *Service) PostMessage(ctx context.Context, c Caller, channelID, body, threadRootID string) (Message, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return Message{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Message{}, ErrEmptyBody
	}
	if len(body) > maxBody {
		// Truncate on a rune boundary so a multibyte UTF-8 rune is never split (a split rune would
		// serialize as U+FFFD and corrupt the trailing character). Walk back from maxBody to the
		// start byte of the rune that straddles the cut.
		cut := maxBody
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		body = body[:cut]
	}
	m, err := s.repo.InsertMessage(ctx, channelID, c.UserID, body, threadRootID)
	if err != nil {
		return Message{}, err
	}
	s.fanout(channelID, m)
	return m, nil
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
