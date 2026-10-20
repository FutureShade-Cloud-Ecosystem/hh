package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeStore is a DB-free store double for the service unit tests. It records calls and returns
// scripted results so channel-scoping authz, unread math, thread replies, and event routing can be
// proven without a live Postgres.
type fakeStore struct {
	roleCodes   map[string]string          // userID -> canonical role code
	members     map[string]map[string]bool // channelID -> userID -> isMember
	exists      map[string]bool            // channelID -> exists
	channels    map[string][]Channel       // userID -> channels
	messages    map[string]MessagePage     // channelID -> page
	threads     map[string][]Message       // "channelID|rootID" -> replies (channel-bound, mirrors the SQL guard)
	unread      map[string]UnreadCounts    // userID -> summary
	slugs       map[string]string          // slug -> channelID
	roomsBySlug map[string]Channel         // slug -> the room that holds it (the convergence the slug index carries)
	yard        map[string]string          // branchID -> slug
	archived    map[string]bool            // channelID -> already archived (rule 7's "Channel delete admins only" refusal)
	// The membership walls' doubles:
	facts         map[string]channelFactsRow // channelID -> kind/branch/auto_scope
	memberRoles   map[string]string          // "channelID|userID" -> role
	memberSources map[string]string          // "channelID|userID" -> source
	userBranches  map[string]map[string]bool // userID -> branchID -> granted
	userGlobal    map[string]bool            // userID -> GLOBAL scope
	syncErr       error
	inserted      []Message       // messages posted via InsertMessage
	events        []Message       // event messages posted
	eventRefs     []EventRef      // refs captured parallel to events (Feature 2 ref assertions)
	eventDup      map[string]bool // (channelID|eventKey) already inserted
	markCalls     []string        // "channelID|userID"
	// The group rooms the fake minted (CreateGroup's own double).
	createdGroups []Channel
	// Feature A / B doubles:
	prefs      map[string]NotificationPrefs // "channelID|userID" -> stored prefs
	tiers      map[string]string            // channelID -> tier (default STANDARD)
	exportRows []Message                    // rows ExportMessages returns
	// Feature 1 (0071) doubles:
	acks    map[string]Ack       // "messageID|userID" -> ack (upsert)
	unacked map[string][]Message // userID -> un-acked forced-ack messages the poll returns
	// Slice S7 (0401) doubles: per-person notification settings (event-category prefs + quiet hours).
	personPrefs     map[string]map[string]PersonEventPref // userID -> category -> stored pref
	personPrefReads int                                   // GetPersonEventPrefsForUser calls (the settings PUT loads the map once)
	quietHours      map[string]QuietHours                 // userID -> stored quiet-hours row
	// Approvals rail (0075) doubles:
	approvalChannels   map[string]string // userID -> channelID (EnsureApprovalChannel idempotent)
	approvalCards      map[string]bool   // "channelID|cardEventKey" -> inserted (card dedup)
	approvalMsgs       []Message         // card inserts captured (count assertions)
	approvalEchoes     map[string]bool   // "channelID|decidedEventKey" -> inserted (echo dedup)
	stamped            map[string]string // approvalID -> last status stamped
	stampCalls         int               // StampApprovalStatus invocation count
	displayNames       map[string]string // userID -> display name (decider name resolution)
	savedRows          map[string]map[string]SavedMessage
	roster             map[string][]StaffRosterRow
	channelAttachments map[string][]MessageAttachment
	memberCounts       map[string]int
	singleMessages     map[string]Message
	// Change 7 S4 doubles: the planted message rows the edit and delete
	// walls read, and the writes the legs assert on.
	editFacts       map[string]messageFacts // messageID -> the planted row (channel bound)
	editedMessages  []Message               // EditMessage writes captured
	deletedMessages []string                // DeleteMessage writes captured (message ids)
}

func (f *fakeStore) SaveMessage(_ context.Context, userID, messageID string) (SavedMessage, error) {
	for _, m := range f.inserted {
		if m.ID != messageID {
			continue
		}
		if m.DeletedAt != nil {
			return SavedMessage{}, ErrNotFound
		}
		if !f.members[m.ChannelID][userID] {
			return SavedMessage{}, ErrNotMember
		}
		if f.savedRows == nil {
			f.savedRows = map[string]map[string]SavedMessage{}
		}
		if f.savedRows[userID] == nil {
			f.savedRows[userID] = map[string]SavedMessage{}
		}
		if item, ok := f.savedRows[userID][messageID]; ok {
			return item, nil
		}
		var channel Channel
		for _, candidate := range f.roomsBySlug {
			if candidate.ID == m.ChannelID {
				channel = candidate
				break
			}
		}
		item := SavedMessage{
			MessageID: m.ID, ChannelID: m.ChannelID, ChannelSlug: channel.Slug,
			ChannelName: channel.Name, AuthorID: m.AuthorID, AuthorName: f.displayNames[m.AuthorID],
			Kind: m.Kind, Body: m.Body, CreatedAt: m.CreatedAt, SavedAt: time.Now(),
		}
		f.savedRows[userID][messageID] = item
		return item, nil
	}
	return SavedMessage{}, ErrNotFound
}

func (f *fakeStore) ListSavedMessages(_ context.Context, userID string, limit int) ([]SavedMessage, error) {
	var out []SavedMessage
	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	for id, item := range f.savedRows[userID] {
		var current Message
		for _, m := range f.inserted {
			if m.ID == id {
				current = m
				break
			}
		}
		if current.ID == "" || !f.members[item.ChannelID][userID] || current.CreatedAt.Before(cutoff) {
			continue
		}
		if current.DeletedAt != nil {
			item.Body = ""
		}
		out = append(out, item)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteSavedMessage(_ context.Context, userID, messageID string) error {
	delete(f.savedRows[userID], messageID)
	return nil
}

func (f *fakeStore) SearchMessages(_ context.Context, _, _ string, _ int) ([]SearchMessage, error) {
	return nil, nil
}

func (f *fakeStore) SearchPeople(_ context.Context, _, _ string, _ int) ([]SearchPerson, error) {
	return nil, nil
}

func (f *fakeStore) SearchChannels(_ context.Context, _, _, _ string, _ int) ([]SearchChannel, error) {
	return nil, nil
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		members:            map[string]map[string]bool{},
		exists:             map[string]bool{},
		channels:           map[string][]Channel{},
		messages:           map[string]MessagePage{},
		threads:            map[string][]Message{},
		unread:             map[string]UnreadCounts{},
		slugs:              map[string]string{},
		roomsBySlug:        map[string]Channel{},
		yard:               map[string]string{},
		archived:           map[string]bool{},
		facts:              map[string]channelFactsRow{},
		memberRoles:        map[string]string{},
		memberSources:      map[string]string{},
		userBranches:       map[string]map[string]bool{},
		userGlobal:         map[string]bool{},
		eventDup:           map[string]bool{},
		prefs:              map[string]NotificationPrefs{},
		tiers:              map[string]string{},
		acks:               map[string]Ack{},
		unacked:            map[string][]Message{},
		personPrefs:        map[string]map[string]PersonEventPref{},
		quietHours:         map[string]QuietHours{},
		roster:             map[string][]StaffRosterRow{},
		channelAttachments: map[string][]MessageAttachment{},
		memberCounts:       map[string]int{},
		singleMessages:     map[string]Message{},
	}
}

func (f *fakeStore) setMember(channelID, userID string) {
	if f.members[channelID] == nil {
		f.members[channelID] = map[string]bool{}
	}
	f.members[channelID][userID] = true
	f.exists[channelID] = true
}

func (f *fakeStore) SyncMembership(_ context.Context, _ string) error { return f.syncErr }
func (f *fakeStore) ListChannelsForUser(_ context.Context, userID string) ([]Channel, error) {
	return f.channels[userID], nil
}
func (f *fakeStore) IsMember(_ context.Context, channelID, userID string) (bool, error) {
	return f.members[channelID][userID], nil
}
func (f *fakeStore) channelExists(_ context.Context, channelID string) (bool, error) {
	return f.exists[channelID], nil
}
func (f *fakeStore) ListMessages(_ context.Context, channelID, _ string, _ int) (MessagePage, error) {
	return f.messages[channelID], nil
}
func (f *fakeStore) ListThread(_ context.Context, channelID, rootID string) ([]Message, error) {
	// Channel-bound, exactly like the SQL (WHERE thread_root_id=$1 AND channel_id=$2): a root that
	// does not live in channelID yields no replies, so a cross-channel probe reads nothing.
	return f.threads[channelID+"|"+rootID], nil
}
func (f *fakeStore) InsertMessage(_ context.Context, channelID, userID, body, threadRootID string) (Message, error) {
	m := Message{ID: "m-" + body, ChannelID: channelID, AuthorID: userID, Kind: MsgText, Body: body, ThreadRootID: threadRootID, CreatedAt: time.Now()}
	f.inserted = append(f.inserted, m)
	return m, nil
}
func (f *fakeStore) MarkRead(_ context.Context, channelID, userID string, _ time.Time) error {
	f.markCalls = append(f.markCalls, channelID+"|"+userID)
	return nil
}
func (f *fakeStore) UnreadSummary(_ context.Context, userID string) (UnreadCounts, error) {
	return f.unread[userID], nil
}
func (f *fakeStore) InsertEventMessage(_ context.Context, channelID, eventKey, body string, ref EventRef, requiresAck bool) (Message, bool, error) {
	key := channelID + "|" + eventKey
	if f.eventDup[key] {
		return Message{}, false, nil // idempotent redelivery
	}
	f.eventDup[key] = true
	m := Message{ID: "ev-" + eventKey, ChannelID: channelID, Kind: MsgEvent, Body: body, RequiresAck: requiresAck}
	f.events = append(f.events, m)
	f.eventRefs = append(f.eventRefs, ref)
	return m, true, nil
}
func (f *fakeStore) ChannelIDBySlug(_ context.Context, slug string) (string, error) {
	id, ok := f.slugs[slug]
	if !ok {
		return "", errNotFound
	}
	return id, nil
}
func (f *fakeStore) BranchYardSlug(_ context.Context, branchID string) (string, error) {
	s, ok := f.yard[branchID]
	if !ok {
		return "", errNotFound
	}
	return s, nil
}

// Slice S1 double: the roster the service pages, keyed by org id.
func (f *fakeStore) StaffRoster(_ context.Context, orgID string) ([]StaffRosterRow, error) {
	return f.roster[orgID], nil
}

// Feature A / B store doubles.
func (f *fakeStore) ChannelTier(_ context.Context, channelID string) (string, error) {
	if t, ok := f.tiers[channelID]; ok {
		return t, nil
	}
	return TierStandard, nil // unset defaults to STANDARD (mirrors a plain channel)
}
func (f *fakeStore) GetNotificationPrefs(_ context.Context, channelID, userID string) (NotificationPrefs, error) {
	if p, ok := f.prefs[channelID+"|"+userID]; ok {
		return p, nil
	}
	return DefaultNotificationPrefs(channelID, userID), nil // defaults when unset (mirrors repo)
}
func (f *fakeStore) UpsertNotificationPrefs(_ context.Context, p NotificationPrefs) (NotificationPrefs, error) {
	f.prefs[p.ChannelID+"|"+p.UserID] = p
	return p, nil
}
func (f *fakeStore) ExportMessages(_ context.Context, _ ExportFilter) ([]Message, error) {
	return f.exportRows, nil
}

// Feature 1 (0071) store doubles.
func (f *fakeStore) AckMessage(_ context.Context, _, messageID, userID, method string) (Ack, error) {
	a := Ack{MessageID: messageID, UserID: userID, Method: method, AckedAt: time.Now()}
	f.acks[messageID+"|"+userID] = a // upsert: a repeat ack overwrites, so the map stays one row
	return a, nil
}
func (f *fakeStore) UnackedRequiresAck(_ context.Context, userID string) ([]Message, error) {
	return f.unacked[userID], nil
}

// Slice S7 (0401) store doubles: per-person notification settings. The fake stores the rows
// exactly as the SQL would, so the service's merge-over-current logic is proven without a DB.
func (f *fakeStore) GetPersonEventPrefsForUser(_ context.Context, userID string) (map[string]PersonEventPref, error) {
	f.personPrefReads++
	return f.personPrefs[userID], nil
}
func (f *fakeStore) UpsertPersonEventPref(_ context.Context, p PersonEventPref) (PersonEventPref, error) {
	if f.personPrefs[p.UserID] == nil {
		f.personPrefs[p.UserID] = map[string]PersonEventPref{}
	}
	f.personPrefs[p.UserID][p.EventCategory] = p
	return p, nil
}
func (f *fakeStore) GetQuietHoursForUser(_ context.Context, userID string) (QuietHours, error) {
	if q, ok := f.quietHours[userID]; ok {
		return q, nil
	}
	return DefaultQuietHours(userID), nil // absent row is the shipped default (mirrors the SQL)
}
func (f *fakeStore) UpsertQuietHours(_ context.Context, q QuietHours) (QuietHours, error) {
	if q.BypassUserIDs == nil {
		q.BypassUserIDs = []string{} // never persist NULL; matches the SQL's COALESCE
	}
	f.quietHours[q.UserID] = q
	return q, nil
}

// - Approvals rail (0075) fake store methods -
func (f *fakeStore) EnsureApprovalChannel(_ context.Context, userID string) (string, error) {
	if f.approvalChannels == nil {
		f.approvalChannels = map[string]string{}
	}
	if id, ok := f.approvalChannels[userID]; ok {
		return id, nil
	}
	id := "appch-" + userID
	f.approvalChannels[userID] = id
	return id, nil
}

func (f *fakeStore) PostApprovalMessage(_ context.Context, channelID, eventKey, body string, ref ApprovalRef) (Message, bool, error) {
	if f.approvalCards == nil {
		f.approvalCards = map[string]bool{}
	}
	key := channelID + "|" + eventKey
	if f.approvalCards[key] {
		return Message{}, false, nil // idempotent: already fanned to this channel
	}
	f.approvalCards[key] = true
	m := Message{ID: "card-" + key, ChannelID: channelID, Kind: MsgApproval, Body: body}
	f.approvalMsgs = append(f.approvalMsgs, m)
	return m, true, nil
}

func (f *fakeStore) StampApprovalStatus(_ context.Context, approvalID, status string) (int, error) {
	if f.stamped == nil {
		f.stamped = map[string]string{}
	}
	f.stamped[approvalID] = status
	f.stampCalls++
	suffix := "|" + approvalCardKey(approvalID)
	n := 0
	for k := range f.approvalCards {
		if strings.HasSuffix(k, suffix) {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) ApprovalChannelIDs(_ context.Context, approvalID string) ([]string, error) {
	suffix := "|" + approvalCardKey(approvalID)
	var out []string
	for k := range f.approvalCards {
		if strings.HasSuffix(k, suffix) {
			out = append(out, strings.TrimSuffix(k, suffix))
		}
	}
	return out, nil
}

func (f *fakeStore) PostApprovalEcho(_ context.Context, channelID, approvalID, body string, _ EventRef) (Message, bool, error) {
	if f.approvalEchoes == nil {
		f.approvalEchoes = map[string]bool{}
	}
	key := channelID + "|" + approvalDecidedKey(approvalID)
	if f.approvalEchoes[key] {
		return Message{}, false, nil // idempotent echo
	}
	f.approvalEchoes[key] = true
	m := Message{ID: "echo-" + key, ChannelID: channelID, Kind: MsgEvent, Body: body}
	f.events = append(f.events, m)
	return m, true, nil
}

func (f *fakeStore) UserDisplayName(_ context.Context, userID string) (string, error) {
	return f.displayNames[userID], nil
}

// UserRoleCode answers the canonical role code planted with setRoleCode, an
// empty code for a user without one (the real query's answer for no role).
func (f *fakeStore) UserRoleCode(_ context.Context, userID string) (string, error) {
	return f.roleCodes[userID], nil
}

func (f *fakeStore) setRoleCode(userID, code string) {
	if f.roleCodes == nil {
		f.roleCodes = map[string]string{}
	}
	f.roleCodes[userID] = code
}

// The channel/DM/member seams' base double: honest minimums (a slug always
// converges on one channel, members seat idempotently). The route-level
// double in channels_admin_route_test.go wraps this with the recording the
// route legs assert on; service-level detail tests use these defaults.
func (f *fakeStore) CreateChannel(_ context.Context, in CreateChannelInput, ownerID string) (Channel, bool, error) {
	if in.Slug == "" {
		in.Slug = sanitizeSlugPart(in.Name)
	}
	// The convergence the repository's slug index carries: the conflict arm
	// answers the room as it stands (its own kind and branch) and writes
	// nothing, no seat; only the fresh arm seats the founder.
	if c, ok := f.roomsBySlug[in.Slug]; ok {
		return c, false, nil
	}
	id := "ch-" + in.Slug
	c := Channel{ID: id, Kind: in.Kind, BranchID: in.BranchID, Slug: in.Slug, Name: in.Name}
	f.roomsBySlug[in.Slug] = c
	f.slugs[in.Slug] = id
	f.setMember(id, ownerID)
	return c, true, nil
}

func (f *fakeStore) CreateDM(_ context.Context, aUserID, bUserID string) (Channel, bool, error) {
	return Channel{ID: "dm-" + aUserID + "-" + bUserID, Kind: KindDM}, true, nil
}

func (f *fakeStore) AddChannelMember(_ context.Context, in AddMemberInput) (ChannelMember, error) {
	if src, ok := f.memberSources[in.ChannelID+"|"+in.UserID]; ok {
		return ChannelMember{ChannelID: in.ChannelID, UserID: in.UserID, Role: "MEMBER", Source: src, AddedReason: in.AddedReason, AddedBy: in.AddedBy}, nil
	}
	f.setMember(in.ChannelID, in.UserID)
	f.memberSources[in.ChannelID+"|"+in.UserID] = SourceExplicit
	return ChannelMember{ChannelID: in.ChannelID, UserID: in.UserID, Role: "MEMBER", Source: SourceExplicit, AddedReason: in.AddedReason, AddedBy: in.AddedBy}, nil
}

func (f *fakeStore) RemoveChannelMember(_ context.Context, channelID, userID string) error {
	if f.members[channelID] != nil {
		delete(f.members[channelID], userID)
		delete(f.memberSources, channelID+"|"+userID)
	}
	return nil
}

// SetChannelVisibility satisfies the new settings-panel route's store seam
// (rule 7's "New channel admins only" hand on visibility); the fake
// records the new value and answers the channel as it stands.
func (f *fakeStore) SetChannelVisibility(_ context.Context, channelID, visibility string) (Channel, error) {
	if visibility != VisibilityPublic && visibility != VisibilityPrivate {
		return Channel{}, ErrBadVisibility
	}
	if f.exists[channelID] {
		return Channel{ID: channelID, Visibility: visibility}, nil
	}
	return Channel{}, errNotFound
}

// ArchiveChannel satisfies the channel-delete route's store seam (rule 7's
// "Channel delete admins only"): the fake records the archive, and a
// re-archive on an already-archived channel answers ErrChannelArchived in
// the same shape the live repository hands back.
func (f *fakeStore) ArchiveChannel(_ context.Context, channelID string) error {
	if f.archived[channelID] {
		return ErrChannelArchived
	}
	if !f.exists[channelID] {
		return errNotFound
	}
	f.archived[channelID] = true
	return nil
}

// The group room doubles (walk change 7, slice S2): the fake mints rooms the
// way the repository does (facts recorded, the caller OWNER, the set MEMBER),
// seats with the created fact, and renames the minted room.
func (f *fakeStore) CreateGroup(_ context.Context, ownerUserID, name string, memberIDs []string) (Channel, error) {
	c := Channel{ID: fmt.Sprintf("e2000000-0000-4000-9000-%012d", len(f.createdGroups)+1), Kind: KindGroup, Name: name, Tier: TierStandard}
	f.createdGroups = append(f.createdGroups, c)
	f.exists[c.ID] = true
	f.facts[c.ID] = channelFactsRow{Kind: KindGroup}
	f.setMember(c.ID, ownerUserID)
	f.memberRoles[c.ID+"|"+ownerUserID] = "OWNER"
	f.memberSources[c.ID+"|"+ownerUserID] = SourceExplicit
	for _, id := range memberIDs {
		f.setMember(c.ID, id)
		f.memberRoles[c.ID+"|"+id] = "MEMBER"
		f.memberSources[c.ID+"|"+id] = SourceExplicit
	}
	return c, nil
}

func (f *fakeStore) SeatGroupMember(_ context.Context, channelID, userID, _, _ string) (ChannelMember, bool, error) {
	if src, ok := f.memberSources[channelID+"|"+userID]; ok {
		return ChannelMember{ChannelID: channelID, UserID: userID, Role: "MEMBER", Source: src}, false, nil
	}
	f.setMember(channelID, userID)
	f.memberSources[channelID+"|"+userID] = SourceExplicit
	return ChannelMember{ChannelID: channelID, UserID: userID, Role: "MEMBER", Source: SourceExplicit}, true, nil
}

func (f *fakeStore) RenameGroup(_ context.Context, channelID, name, _ string) (Channel, error) {
	for i := range f.createdGroups {
		if f.createdGroups[i].ID == channelID {
			f.createdGroups[i].Name = name
			return f.createdGroups[i], nil
		}
	}
	return Channel{}, errNotFound
}

func (f *fakeStore) userActive(_ context.Context, _ string) error { return nil }

func (f *fakeStore) channelFacts(_ context.Context, channelID string) (channelFactsRow, error) {
	if facts, ok := f.facts[channelID]; ok {
		return facts, nil
	}
	if f.exists[channelID] {
		return channelFactsRow{Kind: KindTeam, Visibility: VisibilityPublic}, nil
	}
	return channelFactsRow{}, errNotFound
}

func (f *fakeStore) memberRole(_ context.Context, channelID, userID string) (string, error) {
	return f.memberRoles[channelID+"|"+userID], nil
}

func (f *fakeStore) memberSource(_ context.Context, channelID, userID string) (string, error) {
	return f.memberSources[channelID+"|"+userID], nil
}

func (f *fakeStore) userReachesBranch(_ context.Context, userID, branchID string) (bool, error) {
	if f.userBranches[userID] == nil {
		return f.userGlobal[userID], nil
	}
	return f.userBranches[userID][branchID] || f.userGlobal[userID], nil
}

// userHoldsAnyRole: the fake carries no role list for a target; no fake room names member_roles, so
// the wall never asks it.
func (f *fakeStore) userHoldsAnyRole(_ context.Context, _ string, _ []string) (bool, error) {
	return true, nil
}

func (f *fakeStore) ListChannelAttachments(_ context.Context, channelID string) ([]MessageAttachment, error) {
	return f.channelAttachments[channelID], nil
}

func (f *fakeStore) CountChannelMembers(_ context.Context, channelID string) (int, error) {
	if f.memberCounts != nil {
		if c, ok := f.memberCounts[channelID]; ok {
			return c, nil
		}
	}
	if f.members[channelID] != nil {
		return len(f.members[channelID]), nil
	}
	return 0, nil
}

func (f *fakeStore) InsertChannelAttachment(_ context.Context, channelID, uploadedBy string, in ChannelAttachmentCreateInput) (MessageAttachment, error) {
	if f.channelAttachments == nil {
		f.channelAttachments = map[string][]MessageAttachment{}
	}
	var uPtr *string
	if uploadedBy != "" {
		uPtr = &uploadedBy
	}
	sizeBytes := int64(0)
	if in.SizeBytes != nil {
		sizeBytes = *in.SizeBytes
	}
	att := MessageAttachment{
		ID:           "att-" + in.AttachmentID,
		ChannelID:    &channelID,
		MessageID:    in.MessageID,
		AttachmentID: &in.AttachmentID,
		Filename:     in.Filename,
		ContentType:  in.ContentType,
		SizeBytes:    sizeBytes,
		Kind:         in.Kind,
		UploadedBy:   uPtr,
		CreatedAt:    time.Now(),
	}
	f.channelAttachments[channelID] = append([]MessageAttachment{att}, f.channelAttachments[channelID]...)
	return att, nil
}

func (f *fakeStore) UpsertChannelAttachmentFromMirror(_ context.Context, channelID string, att MessageAttachment) error {
	if f.channelAttachments == nil {
		f.channelAttachments = map[string][]MessageAttachment{}
	}
	f.channelAttachments[channelID] = append([]MessageAttachment{att}, f.channelAttachments[channelID]...)
	return nil
}

func (f *fakeStore) GetMessage(_ context.Context, messageID string) (Message, error) {
	if f.singleMessages != nil {
		if m, ok := f.singleMessages[messageID]; ok {
			return m, nil
		}
	}
	return Message{ID: messageID}, nil
}

// S6 directory + join + leave + membership-rule stubs.

func (f *fakeStore) ListDirectory(_ context.Context, _ string, _ bool, _ map[string]bool) (map[string]DirectoryEntry, error) {
	return map[string]DirectoryEntry{}, nil
}

func (f *fakeStore) ListChannelMembers(_ context.Context, _ string) ([]ChannelMemberEntry, error) {
	return nil, nil
}

func (f *fakeStore) GetChannelRule(_ context.Context, channelID string) (MembershipRule, error) {
	if !f.exists[channelID] {
		return MembershipRule{}, errNotFound
	}
	return MembershipRule{Visibility: VisibilityPublic}, nil
}

func (f *fakeStore) SetChannelRule(_ context.Context, _ string, _ MembershipRule) error {
	return nil
}

func (f *fakeStore) ListChannelRules(_ context.Context, _ string, _ bool, _ map[string]bool) ([]MembershipRuleEntry, error) {
	return nil, nil
}

// - Change 7, slice S3 doubles: reactions, seen-by, mentions -
// The base fake answers inertly (a message facts read is not-found, the
// reaction writes land nowhere, the reads answer empty); the route double in
// reactions_route_test.go overrides what a leg scripts.

func (f *fakeStore) messageFacts(_ context.Context, _, _ string) (messageFactsRow, error) {
	return messageFactsRow{}, errNotFound
}

func (f *fakeStore) AddReaction(_ context.Context, _, _, _, _ string) error { return nil }

func (f *fakeStore) RemoveReaction(_ context.Context, _, _, _, _ string) error { return nil }

func (f *fakeStore) reactionsForMessages(_ context.Context, _ []string, _ string) (map[string][]ReactionCount, error) {
	return map[string][]ReactionCount{}, nil
}

func (f *fakeStore) MessageSeenBy(_ context.Context, _, _ string, _ time.Time) (SeenByReport, error) {
	return SeenByReport{Readers: []SeenByReader{}}, nil
}

func (f *fakeStore) ListMentions(_ context.Context, _, _ string, _ int) (MessagePage, error) {
	return MessagePage{Messages: []Message{}}, nil
}

// - change 7 S4: the edit and delete doubles -

// messageFactsFor answers a planted row bound to its channel; a row of
// another channel is the same not-found an absent id is.
func (f *fakeStore) messageFactsFor(_ context.Context, channelID, messageID string) (messageFacts, error) {
	mf, ok := f.editFacts[messageID]
	if !ok || mf.ChannelID != channelID {
		return messageFacts{}, errNotFound
	}
	return mf, nil
}

func (f *fakeStore) EditMessage(_ context.Context, channelID, messageID, body string) (Message, error) {
	mf, ok := f.editFacts[messageID]
	if !ok || mf.ChannelID != channelID || mf.DeletedAt != nil || mf.Kind != MsgText || mf.RequiresAck {
		return Message{}, errNotFound
	}
	now := time.Now()
	m := Message{ID: messageID, ChannelID: channelID, AuthorID: mf.AuthorID, Kind: mf.Kind,
		Body: body, EditedAt: &now, CreatedAt: mf.CreatedAt}
	f.editedMessages = append(f.editedMessages, m)
	return m, nil
}

func (f *fakeStore) DeleteMessage(_ context.Context, channelID, messageID string) (Message, error) {
	mf, ok := f.editFacts[messageID]
	if !ok || mf.ChannelID != channelID || mf.Kind != MsgText || mf.RequiresAck {
		return Message{}, errNotFound
	}
	deleted := time.Now()
	if mf.DeletedAt != nil {
		deleted = *mf.DeletedAt // the tombstone's own delete keeps the first stamp
	}
	m := Message{ID: messageID, ChannelID: channelID, AuthorID: mf.AuthorID, Kind: mf.Kind,
		Body: "", DeletedAt: &deleted, CreatedAt: mf.CreatedAt}
	f.deletedMessages = append(f.deletedMessages, messageID)
	return m, nil
}

// EditWindowMinutes answers the default the policy row seeds; the window
// legs plant their facts' created_at around it.
func (f *fakeStore) EditWindowMinutes(context.Context) (int, error) {
	return defaultEditWindowMinutes, nil
}

// svcWith builds a service over a fake store (bypassing the *Repository constructor).
func svcWith(f *fakeStore) *Service { return &Service{repo: f} }

const (
	uAlice = "u-alice"
	uBob   = "u-bob"
	chGen  = "ch-general"
)

// - channel-scoping authz -

func TestGetMessages_NonMemberForbidden(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice) // Alice is a member; Bob is not
	svc := svcWith(f)

	// Alice can read.
	if _, err := svc.GetMessages(context.Background(), Caller{UserID: uAlice}, chGen, "", 0); err != nil {
		t.Fatalf("member read should succeed: %v", err)
	}
	// Bob (non-member) is forbidden, and the channel EXISTS so it's 403 not 404.
	_, err := svc.GetMessages(context.Background(), Caller{UserID: uBob}, chGen, "", 0)
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member read want ErrNotMember, got %v", err)
	}
}

// TestGetMessages_EmptyChannelReturnsNonNilSlice is the regression for the {"messages":null} defect:
// an empty channel's page must carry a non-nil (empty) slice so it serializes as {"messages":[]}.
// A nil slice serialized to JSON null, which broke the Lit hub's [...page.messages].reverse() with a
// TypeError and confused cross-channel content. The fakeStore returns a zero MessagePage (nil
// Messages) for an unset channel, exactly as the real repo does for a channel with no rows.
func TestGetMessages_EmptyChannelReturnsNonNilSlice(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice) // member, but no messages scripted → repo yields nil slice
	svc := svcWith(f)
	page, err := svc.GetMessages(context.Background(), Caller{UserID: uAlice}, chGen, "", 0)
	if err != nil {
		t.Fatalf("empty-channel read should succeed: %v", err)
	}
	if page.Messages == nil {
		t.Fatalf("empty channel must return a non-nil slice (serializes []), got nil ({\"messages\":null})")
	}
	if len(page.Messages) != 0 {
		t.Fatalf("empty channel should have zero messages, got %d", len(page.Messages))
	}
}

func TestGetMessages_UnknownChannelNotFound(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	_, err := svc.GetMessages(context.Background(), Caller{UserID: uAlice}, "ch-nope", "", 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown channel want ErrNotFound, got %v", err)
	}
}

func TestPostMessage_NonMemberForbidden(t *testing.T) {
	f := newFakeStore()
	f.exists[chGen] = true // channel exists, Bob not a member
	svc := svcWith(f)
	_, err := svc.PostMessage(context.Background(), Caller{UserID: uBob}, chGen, "hi", "")
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member post want ErrNotMember, got %v", err)
	}
	if len(f.inserted) != 0 {
		t.Fatalf("no message should be inserted for a forbidden post, got %d", len(f.inserted))
	}
}

func TestPostMessage_EmptyBodyRejected(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)
	if _, err := svc.PostMessage(context.Background(), Caller{UserID: uAlice}, chGen, "   ", ""); !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("whitespace body want ErrEmptyBody, got %v", err)
	}
}

// TestPostMessage_TruncatesOnRuneBoundary is the regression for the byte-slice truncation splitting a
// multibyte rune. body[:maxBody] could cut a 3-byte '…' (U+2026) mid-rune, leaving a dangling
// partial byte that serializes as U+FFFD and corrupts the trailing character. Building a body whose
// maxBody-th byte lands in the MIDDLE of a multibyte rune proves the rune-aware trim keeps only whole
// runes (and that the stored body is still valid UTF-8).
func TestPostMessage_TruncatesOnRuneBoundary(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)

	// '…' is 3 bytes (U+2026). Pad with ASCII so the cut at maxBody falls inside a '…' rune:
	// maxBody = 8192; 8191 ASCII bytes puts the boundary 1 byte into the following 3-byte rune.
	body := strings.Repeat("a", maxBody-1) + strings.Repeat("…", 10)
	if len(body) <= maxBody {
		t.Fatalf("test setup: body must exceed maxBody, got %d", len(body))
	}
	m, err := svc.PostMessage(context.Background(), Caller{UserID: uAlice}, chGen, body, "")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if !utf8.ValidString(m.Body) {
		t.Fatalf("stored body must be valid UTF-8 after truncation (no split rune), got invalid")
	}
	if len(m.Body) > maxBody {
		t.Fatalf("stored body must not exceed maxBody bytes, got %d", len(m.Body))
	}
	// The rune-aware trim drops the straddling '…' entirely, so the body is exactly the ASCII run.
	if m.Body != strings.Repeat("a", maxBody-1) {
		t.Fatalf("rune-aware trim should stop before the split rune; got tail %q", m.Body[len(m.Body)-5:])
	}
}

func TestPostMessage_MemberSucceeds_AndFansOut(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	rt := &fakeRealtime{}
	svc := svcWith(f).WithRealtime(rt)
	m, err := svc.PostMessage(context.Background(), Caller{UserID: uAlice}, chGen, "  hello  ", "")
	if err != nil {
		t.Fatalf("member post: %v", err)
	}
	if m.Body != "hello" {
		t.Fatalf("body should be trimmed, got %q", m.Body)
	}
	if len(rt.published) != 1 || rt.published[0].channelID != chGen {
		t.Fatalf("post should fan out to realtime once, got %+v", rt.published)
	}
}

// - thread replies -

func TestPostMessage_ThreadReplyCarriesRoot(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)
	_, err := svc.PostMessage(context.Background(), Caller{UserID: uAlice}, chGen, "a reply", "root-1")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got := f.inserted[0].ThreadRootID; got != "root-1" {
		t.Fatalf("reply should carry thread root, got %q", got)
	}
}

func TestGetThread_NonMemberForbidden(t *testing.T) {
	f := newFakeStore()
	f.exists[chGen] = true
	svc := svcWith(f)
	if _, err := svc.GetThread(context.Background(), Caller{UserID: uBob}, chGen, "root-1"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member thread want ErrNotMember, got %v", err)
	}
}

func TestGetThread_MemberGetsReplies(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	f.threads[chGen+"|root-1"] = []Message{{ID: "r1", Body: "one"}, {ID: "r2", Body: "two"}}
	svc := svcWith(f)
	replies, err := svc.GetThread(context.Background(), Caller{UserID: uAlice}, chGen, "root-1")
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(replies) != 2 {
		t.Fatalf("want 2 replies, got %d", len(replies))
	}
}

// Regression for the cross-channel thread-read IDOR: a member of chGen who supplies a rootID that
// belongs to a DIFFERENT channel (chOther) must get ZERO replies, the read is bound to the
// authorized channel, so a foreign root leaks nothing even though the caller is a legitimate member
// of chGen. Pre-fix, ListThread keyed on rootID alone and returned chOther's replies.
func TestGetThread_ForeignRootLeaksNothing(t *testing.T) {
	const chOther = "ch-other"
	f := newFakeStore()
	f.setMember(chGen, uAlice) // Alice is a member of chGen only, NOT chOther
	// The thread lives in chOther. Alice will pass chGen (which she can see) + the chOther root id.
	f.threads[chOther+"|secret-root"] = []Message{{ID: "s1", Body: "confidential"}}
	svc := svcWith(f)
	replies, err := svc.GetThread(context.Background(), Caller{UserID: uAlice}, chGen, "secret-root")
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(replies) != 0 {
		t.Fatalf("cross-channel root must leak no replies, got %d: %+v", len(replies), replies)
	}
}

// - mark-read + unread -

func TestMarkRead_RequiresMembership(t *testing.T) {
	f := newFakeStore()
	f.exists[chGen] = true
	svc := svcWith(f)
	if err := svc.MarkRead(context.Background(), Caller{UserID: uBob}, chGen); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member mark-read want ErrNotMember, got %v", err)
	}
	if len(f.markCalls) != 0 {
		t.Fatalf("mark-read must not touch a channel the caller can't see")
	}
}

func TestMarkRead_MemberAdvancesCursor(t *testing.T) {
	f := newFakeStore()
	f.setMember(chGen, uAlice)
	svc := svcWith(f)
	if err := svc.MarkRead(context.Background(), Caller{UserID: uAlice}, chGen); err != nil {
		t.Fatalf("mark-read: %v", err)
	}
	if len(f.markCalls) != 1 || f.markCalls[0] != chGen+"|"+uAlice {
		t.Fatalf("mark-read should advance the caller's cursor, got %v", f.markCalls)
	}
}

func TestUnreadSummary_ReturnsTotals(t *testing.T) {
	f := newFakeStore()
	f.unread[uAlice] = UnreadCounts{Total: 5, Channels: []ChannelUnread{{ChannelID: chGen, Unread: 5}}}
	svc := svcWith(f)
	got, err := svc.UnreadSummary(context.Background(), Caller{UserID: uAlice})
	if err != nil {
		t.Fatalf("unread: %v", err)
	}
	if got.Total != 5 || len(got.Channels) != 1 {
		t.Fatalf("unread summary passthrough wrong: %+v", got)
	}
}

// - FR-3 event routing (idempotency at the service boundary) -

func TestPostEventToChannelSlug_Idempotent(t *testing.T) {
	f := newFakeStore()
	f.slugs["finance"] = "ch-finance"
	svc := svcWith(f)
	ref := EventRef{Topic: TopicPaymentReceived}
	if _, inserted, err := svc.PostEventToChannelSlug(context.Background(), "finance", "evt-1", "Payment received.", ref, false); err != nil || !inserted {
		t.Fatalf("first post should insert: inserted=%v err=%v", inserted, err)
	}
	// Redelivery of the same event key must be a no-op (inserted=false), not a duplicate message.
	if _, inserted, err := svc.PostEventToChannelSlug(context.Background(), "finance", "evt-1", "Payment received.", ref, false); err != nil || inserted {
		t.Fatalf("redelivery should be a no-op: inserted=%v err=%v", inserted, err)
	}
	if len(f.events) != 1 {
		t.Fatalf("exactly one event message should exist, got %d", len(f.events))
	}
}

func TestPostEventToChannelSlug_UnknownSlugErrors(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	if _, _, err := svc.PostEventToChannelSlug(context.Background(), "nope", "e", "x", EventRef{}, false); err == nil {
		t.Fatalf("unknown slug should error so the outbox row retries")
	}
}

// fakeRealtime records fan-out for assertions.
type fakeRealtime struct {
	published []struct {
		channelID string
		m         Message
	}
}

func (r *fakeRealtime) PublishMessage(channelID string, m Message) {
	r.published = append(r.published, struct {
		channelID string
		m         Message
	}{channelID, m})
}

// The founding set is capped: a group may name at most maxGroupFoundingMembers others after
// deduplication, so the per member walls (userActive, UserDisplayName) stay bounded.
func TestCreateGroup_FoundingMemberCap(t *testing.T) {
	f := newFakeStore()
	svc := svcWith(f)
	ids := make([]string, 0, maxGroupFoundingMembers+1)
	for i := 0; i <= maxGroupFoundingMembers; i++ {
		ids = append(ids, fmt.Sprintf("u-group-%03d", i))
	}
	if _, err := svc.CreateGroup(context.Background(), Caller{UserID: uAlice}, GroupCreateInput{MemberIDs: ids}); !errors.Is(err, ErrTooManyGroupMembers) {
		t.Fatalf("one over the cap must be refused, got %v", err)
	}
	at := ids[:maxGroupFoundingMembers]
	if _, err := svc.CreateGroup(context.Background(), Caller{UserID: uAlice}, GroupCreateInput{Name: "At the cap", MemberIDs: at}); err != nil {
		t.Fatalf("exactly at the cap founds: %v", err)
	}
	dup := append(append([]string{}, at...), at[0], at[1])
	if _, err := svc.CreateGroup(context.Background(), Caller{UserID: uAlice}, GroupCreateInput{Name: "Dupes collapse", MemberIDs: dup}); err != nil {
		t.Fatalf("duplicates collapse before the cap counts: %v", err)
	}
}
