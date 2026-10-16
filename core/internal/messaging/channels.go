package messaging

// channels.go is the channel-create, DM-create and membership half of the
// module's own surfaces: a staffer founds a room (TEAM or TOPIC), opens a
// direct message with one colleague, and manages a channel's explicit
// membership. The walls are the module's existing ones: every membership read
// and write passes assertMember (the channel-scoping gate every message read
// already goes through), and a channel that names a branch may only be founded
// by a caller who reaches that branch (Caller.Branches, the same fact
// SyncMembership's qualify predicate reads). Derived membership (the role and
// branch model's rows) is not hand-manageable: removing one is a conflict
// with the model that owns it, never a silent no-op.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// - errors surfaced to the handler -
var (
	// ErrBadChannelKind: only TEAM and TOPIC channels are caller-founded. A
	// BRANCH channel is structural (one per yard, seeded), a DM goes through
	// its own route, and the APPROVAL rail's channels are system-provisioned.
	ErrBadChannelKind = errors.New("messaging: kind must be one of TEAM, TOPIC")
	// ErrChannelNameRequired: a channel with no name cannot be listed or read.
	ErrChannelNameRequired = errors.New("messaging: channel name is required")
	// ErrNotInBranch is the branch wall: the caller does not hold the branch
	// the new channel would live in (nor reach every branch).
	ErrNotInBranch = errors.New("messaging: caller does not reach this branch")
	// ErrBadDMTarget: a direct message is between two staff; naming the
	// caller's own id is a client fault.
	ErrBadDMTarget = errors.New("messaging: a direct message needs another staff member")
	// ErrUserNotActive: membership is for active staff; a deactivated account
	// can be named but not seated in a room.
	ErrUserNotActive = errors.New("messaging: that staff member is not active")
	// ErrDerivedMember: a DERIVED member row is the role and branch model's,
	// re-derived on every membership sync; removing it by hand is a conflict
	// with that model, not a permission failure.
	ErrDerivedMember = errors.New("messaging: derived membership is managed by the role and branch model")
	// ErrChannelSlugTaken: the name or slug already names a room this caller
	// may not be handed (not a member, or a room of another kind); a
	// collision is refused in words, never answered with the foreign room.
	ErrChannelSlugTaken = errors.New("messaging: a channel with that name or slug already exists")
	// ErrReservedSlug: caller chosen names may not squat the system
	// namespaces (dm-, rep-, approvals-, house-, payments-, yard-, and the
	// bare payments floor); a room founded there would capture a later
	// system convergence on the same slug.
	ErrReservedSlug = errors.New("messaging: that slug prefix is reserved for system channels")
	// ErrMembershipFixed: a DM's membership is its two parties and an
	// APPROVAL channel's is its owner; neither is hand-manageable, a
	// conflict with what the room IS rather than a permission failure.
	ErrMembershipFixed = errors.New("messaging: this channel's membership is fixed by the system")
	// ErrNotChannelOwner: membership management belongs to the room's owner
	// and to the roles the role matrix gives Admin on house-messages (the
	// general manager); a plain member holds no such pen.
	ErrNotChannelOwner = errors.New("messaging: only the channel's owner or a house messages admin may manage members")
	// ErrTargetNotInBranch: on a room whose membership the branch model
	// derives (auto_scope BRANCH), a target who reaches neither the room's
	// branch nor every branch cannot be seated in it.
	ErrTargetNotInBranch = errors.New("messaging: that staff member does not reach this branch")
	// ErrTargetNotInRoom: on a money room (a room carrying a member_roles
	// list) a target whose roles the list does not name cannot be seated,
	// whoever asks. Who may read the day's payments is the core's decision.
	ErrTargetNotInRoom = errors.New("messaging: that staff member's role may not be seated in this room")
	// ErrLastOwner: a removal may never drop a channel's last OWNER row; a
	// room without an owner is nobody's to manage.
	ErrLastOwner = errors.New("messaging: a channel keeps its owner")
	// ErrChannelArchived: the room is already archived; a re-delete is not a
	// keep-state the route answers (it is already what the manager wall asked
	// for the first time).
	ErrChannelArchived = errors.New("messaging: channel is already archived")
	// ErrBadVisibility: the visibility value is not one the migration's CHECK
	// permits; a route handler that hands a value outside PUBLIC/PRIVATE
	// refuses in shape the controller already laid down.
	ErrBadVisibility = errors.New("messaging: visibility must be PUBLIC or PRIVATE")
	// ErrPrivateRoom: a self-join is refused on a PRIVATE room (admin-or-owner
	// hand-add only; the directory hides it).
	ErrPrivateRoom = errors.New("messaging: a private channel is hand-add only")
	// ErrCannotJoin: the channel's kind or member_roles list keeps a
	// caller out of the join path (DM, APPROVAL, GROUP, money rooms the
	// caller does not hold a listed role for).
	ErrCannotJoin = errors.New("messaging: this channel does not accept self-joins")
	// ErrAdminOnly: the caller is not a house messages admin, the rule
	// write's wall.
	ErrAdminOnly = errors.New("messaging: only a house messages admin may change this rule")
	// ErrNotChannelAdmin: founding a channel is the admins' pen (change 7,
	// ruling A, and the c7 board's "New channel, admins only"). The role
	// matrix's Admin cell on house-messages (the general manager) holds it;
	// the wall reads the same published matrix and fails closed exactly as
	// the membership wall's authority half does.
	ErrNotChannelAdmin = errors.New("messaging: only a house messages admin may found a channel")
	// ErrNotGroupRoom: the rename route is the group room's own move (change
	// 7 ruling 4); a room of any other kind (a DM's name is its parties', a
	// channel's name is its founders') is a conflict with what the room IS,
	// not a permission failure.
	ErrNotGroupRoom = errors.New("messaging: only a group room renames through this route")
)

// GroupCreateInput is one new group room: the optional name (an empty name
// takes the founding members' display names until a member renames it) and
// the set of staffers to seat beside the caller. Membership is explicit and
// the room spans branches; the caller is its owner.
type GroupCreateInput struct {
	Name      string
	MemberIDs []string
}

// CreateGroup starts a group room (change 7 rulings 3 and 4): any staffer may
// start one, membership is explicit (never rule filled, like a DM's), and the
// room spans branches, so the named staffers need reach no branch the caller
// holds and no branch wall runs. The walls are the seats' own: every named
// staffer must be an active staff member (an inactive staffer can be named
// but not seated, an id that names no staffer is not found). There is no
// convergence: every call starts a new group. An empty name takes the
// founding members' display names, the DM's own naming shape.
func (s *Service) CreateGroup(ctx context.Context, c Caller, in GroupCreateInput) (Channel, error) {
	in.Name = strings.TrimSpace(in.Name)
	// The set dedupes, and the caller is dropped from it: they are seated
	// once, as the group's owner, whatever the body named.
	seen := map[string]bool{c.UserID: true}
	members := make([]string, 0, len(in.MemberIDs))
	for _, id := range in.MemberIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		members = append(members, id)
	}
	for _, id := range members {
		if err := s.repo.userActive(ctx, id); err != nil {
			return Channel{}, err
		}
	}
	if in.Name == "" {
		names := make([]string, 0, len(members)+1)
		self, err := s.repo.UserDisplayName(ctx, c.UserID)
		if err != nil {
			return Channel{}, err
		}
		if strings.TrimSpace(self) != "" {
			names = append(names, self)
		}
		for _, id := range members {
			n, err := s.repo.UserDisplayName(ctx, id)
			if err != nil {
				return Channel{}, err
			}
			if strings.TrimSpace(n) != "" {
				names = append(names, n)
			}
		}
		in.Name = strings.Join(names, ", ")
		if in.Name == "" {
			in.Name = "Group"
		}
	}
	return s.repo.CreateGroup(ctx, c.UserID, in.Name, members)
}

// RenameGroup renames a group room (change 7 ruling 4): any member may rename
// it, the new name shows to every member through the ordinary channel reads,
// and the rename writes the system line naming who renamed the group and to
// what. The walls are the module's own: assertMember first (a non member
// renames nothing, 403), then the kind (any other room refuses with
// ErrNotGroupRoom, 409), then the words (an empty name names no room, 400).
func (s *Service) RenameGroup(ctx context.Context, c Caller, channelID, name string) (Channel, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return Channel{}, err
	}
	facts, err := s.repo.channelFacts(ctx, channelID)
	if err != nil {
		return Channel{}, err
	}
	if facts.Kind != KindGroup {
		return Channel{}, ErrNotGroupRoom
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Channel{}, ErrChannelNameRequired
	}
	actor, err := s.repo.UserDisplayName(ctx, c.UserID)
	if err != nil {
		return Channel{}, err
	}
	return s.repo.RenameGroup(ctx, channelID, name, actor)
}

// CreateChannelInput is one caller-founded channel. Slug is optional; an
// empty slug is derived from the name, so the same founding twice converges
// on one channel (the idempotent create). BranchID is optional and only
// meaningful for a TEAM scoped to one yard. Visibility is the change 7
// ruling 7 hand: an empty value defaults to PUBLIC (the room's settings
// panel default, the moderation skill's rule #2 starting point); PRIVATE
// is a wall around hand-added only and lists only via membership.
// AutoScope and AutoRole carry the membership-rule fields an admin may
// plant on create (a seeded yard channel sets AutoScope=BRANCH +
// AutoRole=AutoRoleAny; an admin's "office" channel sets
// AutoScope=GLOBAL + AutoRole=AutoRoleAny). Empty AutoScope + empty
// AutoRole means no automatic membership (the channel is hand-only, the
// membership wall alone governs).
type CreateChannelInput struct {
	Kind       string
	Name       string
	Topic      string
	Slug       string
	BranchID   string
	Visibility string
	AutoScope  string
	AutoRole   string
}

// AddMemberInput is one manager-driven seat: the channel, the member, the
// role the room gives them (always MEMBER; an OWNER is the room's founder
// and is not hand-assignable here), and the change 7 ruling 7 audit trail.
// AddedReason is the manager's text reason the staff member reads in the
// row audit (a brief line that names the run, the swap, the project); an
// empty value lands as the migration's DEFAULT ” and is what the row reads
// back. AddedBy is the user id of the manager who wrote the row; an empty
// value lands NULL (the rare empty-add-by seat, the column's NULL is its
// own keep-state).
type AddMemberInput struct {
	ChannelID   string
	UserID      string
	Role        string
	AddedReason string
	AddedBy     string
}

// CreateChannel founds a TEAM or TOPIC channel idempotently (same slug, same
// channel) and seats the caller as its OWNER. The admin gate runs first
// (change 7, ruling A: founding is the admins' pen, the boards' New channel
// is for admins only): a caller the matrix does not make a house messages
// admin is refused before any wall of the founding runs, whatever the body
// names. The branch wall runs here: a channel naming a branch the caller
// neither holds nor reaches (GLOBAL) is
// refused before anything is written.
//
// A slug that already names a room is NEVER handed to a caller who is not
// already allowed in, and nobody is ever seated as owner of an existing
// channel: the repository writes nothing on the conflict arm (no seat, not
// even the updated_at touch on a foreign row), and the walls here run
// against the room that EXISTS, not the room the request described. The
// branch wall reads the existing row's own branch, so omitting branch_id
// cannot slip a founding past it; the member check answers the room only to
// a caller who already holds a row in it (the founder's replay, a derived
// member of a seeded room); anything else, and any room of another kind (a
// DM is reached only through its own find or create), is refused in words.
func (s *Service) CreateChannel(ctx context.Context, c Caller, in CreateChannelInput) (Channel, bool, error) {
	if !houseMessagesAdmin(c) {
		return Channel{}, false, ErrNotChannelAdmin
	}
	if in.Kind != KindTeam && in.Kind != KindTopic {
		return Channel{}, false, ErrBadChannelKind
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Channel{}, false, ErrChannelNameRequired
	}
	if in.BranchID != "" && !c.IsGlobal() && !c.Branches[in.BranchID] {
		return Channel{}, false, ErrNotInBranch
	}
	in.Topic = strings.TrimSpace(in.Topic)
	// A caller chosen slug is canonicalized to lower case: the slug column
	// is CITEXT, so the index folds case for uniqueness anyway, and the
	// reserved namespace wall below reads the canonical form (an upper
	// cased APPROVALS- slug must not slip past a lower case only check).
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	if in.Slug == "" {
		in.Slug = sanitizeSlugPart(in.Name)
	}
	// A name that sanitizes to nothing (punctuation alone) names no room.
	if in.Slug == "" {
		return Channel{}, false, ErrChannelNameRequired
	}
	// The system namespaces are closed to caller founded rooms, chosen slug
	// or derived: a room squatting dm-/rep-/approvals- would capture the
	// next system provision that converges on the same slug.
	if isReservedChannelSlug(in.Slug) {
		return Channel{}, false, ErrReservedSlug
	}
	ch, created, err := s.repo.CreateChannel(ctx, in, c.UserID)
	if err != nil {
		return Channel{}, false, err
	}
	if created {
		return ch, true, nil
	}
	// The conflict arm: nothing was written, and the answer is earned, not
	// assumed. A room of another kind (a DM, an APPROVAL rail room, a
	// structural BRANCH yard) is not this route's to answer, archived or not.
	if ch.Kind != in.Kind || ch.IsArchived {
		return Channel{}, false, ErrChannelSlugTaken
	}
	// The branch wall against the room that exists: the caller must reach
	// the branch the room actually lives in, whatever the request said.
	if ch.BranchID != "" && !c.IsGlobal() && !c.Branches[ch.BranchID] {
		return Channel{}, false, ErrNotInBranch
	}
	// The member check: only a caller who already holds a row in the room
	// (the founder's replay, a derived member) gets it answered.
	member, err := s.repo.IsMember(ctx, ch.ID, c.UserID)
	if err != nil {
		return Channel{}, false, err
	}
	if !member {
		return Channel{}, false, ErrChannelSlugTaken
	}
	return ch, false, nil
}

// Person is one staffer on the New message picker's roster, the five fields
// the picker reads and nothing else: no email, no phone, no permission or
// cost field, no tier.
type Person struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	RoleLabel   string `json:"role_label"`
	BranchLabel string `json:"branch_label"`
	IsMe        bool   `json:"is_me"`
}

// GetPeople answers the picker's page: the active staff of the caller's org
// (the repository's StaffRoster read, the identity read-seam), q narrowed to
// a case insensitive contains match on the display name, ordered by display
// name then id compared bytewise (the order the cursor itself compares in,
// whatever collation the database would have sorted by), and cut to the
// caller's limit with the next page's key. The caller is on their own list,
// flagged is_me, and the branch wall is the DM rule's: a direct message may
// be opened with any active staffer of the org, so the roster reaches every
// branch's staff and wields no branch wall of its own.
func (s *Service) GetPeople(ctx context.Context, c Caller, orgID, q, cursor string, limit int) ([]Person, string, error) {
	rows, err := s.repo.StaffRoster(ctx, orgID)
	if err != nil {
		return nil, "", err
	}
	needle := strings.ToLower(strings.TrimSpace(q))
	people := make([]Person, 0, len(rows))
	for _, row := range rows {
		if needle != "" && !strings.Contains(strings.ToLower(row.DisplayName), needle) {
			continue
		}
		people = append(people, Person{
			UserID:      row.UserID,
			DisplayName: row.DisplayName,
			RoleLabel:   row.RoleLabel,
			BranchLabel: row.BranchLabel,
			IsMe:        row.UserID == c.UserID,
		})
	}
	// One order for the slice, the search and the cursor, all bytewise.
	sort.SliceStable(people, func(i, j int) bool {
		if people[i].DisplayName != people[j].DisplayName {
			return people[i].DisplayName < people[j].DisplayName
		}
		return people[i].UserID < people[j].UserID
	})
	start := 0
	if cursor != "" {
		id, name, ok := parsePeopleCursor(cursor)
		if !ok {
			return nil, "", ErrBadCursor
		}
		// Positional under the list's own order, so a staffer who left the
		// roster since the cursor was minted neither fails the walk nor
		// repeats or skips a row.
		start = sort.Search(len(people), func(i int) bool {
			if people[i].DisplayName != name {
				return people[i].DisplayName > name
			}
			return people[i].UserID > id
		})
	}
	page := people[start:]
	next := ""
	if len(page) > limit {
		page = page[:limit]
		last := page[len(page)-1]
		next = last.UserID + "|" + last.DisplayName
	}
	return page, next, nil
}

// parsePeopleCursor splits "id|display name": the id first (a uuid never
// holds the separator), the free text last, so a name holding a pipe still
// cuts cleanly into two. The id must parse as a uuid, a cursor this module
// never minted is a malformed cursor (400), never a first page.
func parsePeopleCursor(key string) (id, name string, ok bool) {
	parts := strings.SplitN(key, "|", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", "", false
	}
	if _, err := uuid.Parse(parts[0]); err != nil {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// CreateDM opens (or finds) the direct message channel between the caller and
// one other staff member: the pair's channel is found when it exists (even a
// seeded one with no slug) and created exactly once otherwise, so two staffers
// always land in the same room no matter who opened it or how many times.
func (s *Service) CreateDM(ctx context.Context, c Caller, otherUserID string) (Channel, bool, error) {
	if strings.TrimSpace(otherUserID) == "" || otherUserID == c.UserID {
		return Channel{}, false, ErrBadDMTarget
	}
	return s.repo.CreateDM(ctx, c.UserID, otherUserID)
}

// AddMember seats one staff member in a channel. The walls, in order:
//
//   - assertMember, the module's existing channel-scoping wall, always
//     first: a caller who is not in the room manages nothing in it, which
//     is also what keeps anyone from seating themselves into a room they
//     hold no row in (an owner adding themselves is the no-op it reads as);
//   - the kind rule: a DM's membership is its two parties and an APPROVAL
//     channel's is its owner, neither is hand-manageable (409);
//   - the manager rule: the room's OWNER row, or a role the matrix gives
//     Admin on house-messages (the general manager), and nobody else;
//   - the branch rule: on any room that names a branch (ruling 89: the
//     rooms the branch model derives, auto_scope BRANCH, and every room
//     founded naming a branch, whatever its kind, TEAM or TOPIC), a
//     target who does not reach the room's branch (no grant for it, not
//     GLOBAL) is refused. A room founded without a branch spans branches
//     and seats anyone the owner may reach.
//
// An already-seated member is a no-op that answers the row as it stands: a
// DERIVED row is never rewritten to EXPLICIT (a rewritten row would become
// hand-removable, contradicting the DERIVED removal refusal), and an
// EXPLICIT row keeps the added_reason / added_by it already carries (a
// re-seat never overwrites the manager's audit trail). The added_reason /
// added_by fields are the change 7 ruling 7 hand: the repository writes
// them on the fresh arm and reads them back whatever they are on the
// re-seat arm.
func (s *Service) AddMember(ctx context.Context, c Caller, in AddMemberInput) (ChannelMember, error) {
	if err := s.assertMember(ctx, c, in.ChannelID); err != nil {
		return ChannelMember{}, err
	}
	facts, err := s.repo.channelFacts(ctx, in.ChannelID)
	if err != nil {
		return ChannelMember{}, err
	}
	if facts.Kind == KindDM || facts.Kind == KindApproval {
		return ChannelMember{}, ErrMembershipFixed
	}
	// A group room is the change 7 ruling's exception to the manager rule:
	// any member may add any staffer by hand, of any branch (a group spans
	// branches and names none, so the branch wall below has nothing to wall;
	// it holds no member_roles list either). The seat and its system line go
	// through the group seat, which commits both with the feed bump or not at
	// all. The DM and APPROVAL refusals above are untouched.
	if facts.Kind == KindGroup {
		adder, err := s.repo.UserDisplayName(ctx, c.UserID)
		if err != nil {
			return ChannelMember{}, err
		}
		target, err := s.repo.UserDisplayName(ctx, in.UserID)
		if err != nil {
			return ChannelMember{}, err
		}
		m, _, err := s.repo.SeatGroupMember(ctx, in.ChannelID, in.UserID, adder, target)
		return m, err
	}
	if err := s.assertMembershipManager(ctx, c, in.ChannelID); err != nil {
		return ChannelMember{}, err
	}
	if facts.BranchID != "" {
		reaches, err := s.repo.userReachesBranch(ctx, in.UserID, facts.BranchID)
		if err != nil {
			return ChannelMember{}, err
		}
		if !reaches {
			return ChannelMember{}, ErrTargetNotInBranch
		}
	}
	if len(facts.MemberRoles) > 0 {
		holds, err := s.repo.userHoldsAnyRole(ctx, in.UserID, facts.MemberRoles)
		if err != nil {
			return ChannelMember{}, err
		}
		if !holds {
			return ChannelMember{}, ErrTargetNotInRoom
		}
	}
	if in.Role == "" {
		// The room's settings panel always writes MEMBER (an OWNER is the
		// room's founder and is not hand-assignable here). The repository
		// already refuses an unknown role via its CHECK (role IN
		// ('MEMBER','OWNER')).
		in.Role = RoleMember
	}
	return s.repo.AddChannelMember(ctx, in)
}

// SetVisibility is the change 7 ruling 7 hand on the room's settings
// panel: the manager flips PUBLIC/PRIVATE and the row answers with the
// new value. The wall is the same one membership management carries:
// the room's OWNER row, or a role the matrix gives Admin on
// house-messages, and nobody else (a plain member holds no such pen).
// An invalid value (anything outside the migration's CHECK) is refused
// in words before the write runs.
func (s *Service) SetVisibility(ctx context.Context, c Caller, channelID, visibility string) error {
	visibility = strings.ToUpper(strings.TrimSpace(visibility))
	if visibility != VisibilityPublic && visibility != VisibilityPrivate {
		return ErrBadVisibility
	}
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return err
	}
	if err := s.assertMembershipManager(ctx, c, channelID); err != nil {
		return err
	}
	if _, err := s.repo.SetChannelVisibility(ctx, channelID, visibility); err != nil {
		return err
	}
	return nil
}

// DeleteChannel is the change 7 ruling 7 hand on the room's settings
// panel: the manager archives the room and the room leaves every
// list-my-channels read. The wall is the same one membership management
// carries: the room's OWNER row, or a role the matrix gives Admin on
// house-messages, and nobody else. An already-archived room answers
// ErrChannelArchived: the manager wall already wrote the truth, and a
// second archive is not a keep-state the route answers. The repository
// owns the archive's row update and feed bump.
func (s *Service) DeleteChannel(ctx context.Context, c Caller, channelID string) error {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return err
	}
	if err := s.assertMembershipManager(ctx, c, channelID); err != nil {
		return err
	}
	return s.repo.ArchiveChannel(ctx, channelID)
}

// RemoveMember drops one EXPLICIT member row. The walls are AddMember's
// (member, kind, manager); the branch-model target wall is add-only because
// a removal cannot seat anyone. Below them the repository keeps its own
// refusals: a DERIVED row belongs to the role and branch model (409), and a
// removal never drops the room's last OWNER row (409).
func (s *Service) RemoveMember(ctx context.Context, c Caller, channelID, userID string) error {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return err
	}
	facts, err := s.repo.channelFacts(ctx, channelID)
	if err != nil {
		return err
	}
	if facts.Kind == KindDM || facts.Kind == KindApproval {
		return ErrMembershipFixed
	}
	if err := s.assertMembershipManager(ctx, c, channelID); err != nil {
		return err
	}
	return s.repo.RemoveChannelMember(ctx, channelID, userID)
}

// assertMembershipManager is the manager wall: the caller must hold the
// room's OWNER row, or carry a role the role matrix gives Admin on
// house-messages. assertMember has already run, so the caller's row exists
// and its role is readable.
func (s *Service) assertMembershipManager(ctx context.Context, c Caller, channelID string) error {
	role, err := s.repo.memberRole(ctx, channelID, c.UserID)
	if err != nil {
		return err
	}
	if role == "OWNER" || houseMessagesAdmin(c) {
		return nil
	}
	return ErrNotChannelOwner
}

// ListDirectory is the change 7 ruling 7 directory: the caller reads the
// PUBLIC, non-archived rooms (TEAM, TOPIC, BRANCH) the directory's
// branch wall lets them see, with the caller's own membership state
// (joined bool, source DERIVED/EXPLICIT, member_count). PRIVATE rooms do
// not appear (the wall sits behind the visibility filter). DM and
// APPROVAL channels do not appear (the directory is non-PROVAL with the
// system namespaces; the brief says TEAM, TOPIC, BRANCH only).
func (s *Service) ListDirectory(ctx context.Context, c Caller) (map[string]DirectoryEntry, error) {
	return s.repo.ListDirectory(ctx, c.UserID, c.IsGlobal(), c.Branches)
}

// JoinChannel is the change 7 ruling 7 self-join: a staffer who reaches
// the channel's branch may join a PUBLIC TEAM or TOPIC channel. The
// seat is EXPLICIT MEMBER (idempotent: an already-seated caller
// re-joins as a no-op, never a downgrade of DERIVED or OWNER). The
// walls, in order:
//   - the room must exist and not be archived (the repository answers);
//   - the room must be PUBLIC, never private (PRIVATE rooms are
//     hand-add only, admin-or-owner; a self-join is refused);
//   - the room must accept self-joins: not DM, not APPROVAL, not a
//     money room whose member_roles list the caller does not hold;
//   - the caller must reach the room's branch (or every branch, when
//     the room names no branch).
func (s *Service) JoinChannel(ctx context.Context, c Caller, channelID string) (ChannelMember, error) {
	facts, err := s.repo.channelFacts(ctx, channelID)
	if err != nil {
		return ChannelMember{}, err
	}
	if facts.Kind == KindDM || facts.Kind == KindApproval {
		return ChannelMember{}, ErrCannotJoin
	}
	if facts.Visibility == VisibilityPrivate {
		return ChannelMember{}, ErrPrivateRoom
	}
	if facts.BranchID != "" && !c.IsGlobal() && !c.Branches[facts.BranchID] {
		return ChannelMember{}, ErrTargetNotInBranch
	}
	if len(facts.MemberRoles) > 0 {
		holds, herr := s.repo.userHoldsAnyRole(ctx, c.UserID, facts.MemberRoles)
		if herr != nil {
			return ChannelMember{}, herr
		}
		if !holds {
			return ChannelMember{}, ErrTargetNotInRoom
		}
	}
	return s.repo.AddChannelMember(ctx, AddMemberInput{
		ChannelID: channelID,
		UserID:    c.UserID,
		Role:      RoleMember,
		AddedBy:   c.UserID,
	})
}

// LeaveChannel is the change 7 ruling 7 self-leave: a caller may leave
// a channel whose seat is EXPLICIT MEMBER. The walls:
//   - the room must accept leaves: not DM, not APPROVAL (the room's
//     system-managed membership is fixed; ErrMembershipFixed);
//   - the caller's seat must not be DERIVED (the role and branch model
//     owns the row; ErrDerivedMember, the "Joined by rule, you stay"
//     wall the brief names);
//   - the caller must not be the room's last OWNER (ErrLastOwner;
//     removing the last owner leaves the room without management).
func (s *Service) LeaveChannel(ctx context.Context, c Caller, channelID string) error {
	facts, err := s.repo.channelFacts(ctx, channelID)
	if err != nil {
		return err
	}
	if facts.Kind == KindDM || facts.Kind == KindApproval {
		return ErrMembershipFixed
	}
	// A caller without a row cannot leave a room they are not in.
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return err
	}
	source, err := s.repo.memberSource(ctx, channelID, c.UserID)
	if err != nil {
		return err
	}
	if source == SourceDerived {
		return ErrDerivedMember
	}
	return s.repo.RemoveChannelMember(ctx, channelID, c.UserID)
}

// ListExplicitMembers is the read side of the Settings > Membership
// rules panel: every EXPLICIT member row of the channel, with the
// audit fields the panel shows. The caller must hold a row in the
// room (the membership wall). DERIVED rows are listed too: the panel
// names "Joined by rule" rows alongside "Added by hand", and the two
// stay readable in one list.
func (s *Service) ListExplicitMembers(ctx context.Context, c Caller, channelID string) ([]ChannelMemberEntry, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return nil, err
	}
	return s.repo.ListChannelMembers(ctx, channelID)
}

// GetMembershipRule is the read side of the membership-rule route: the
// room's auto_scope, auto_role, and visibility. The caller must be in
// the room (the rule is a room-shared read; the directory is the room
// list, the rule is the room's settings).
func (s *Service) GetMembershipRule(ctx context.Context, c Caller, channelID string) (MembershipRule, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return MembershipRule{}, err
	}
	return s.repo.GetChannelRule(ctx, channelID)
}

// UpdateMembershipRule is the change 7 ruling 7 rule write: an admin
// flips a room's auto_scope, auto_role, and visibility, and the next
// SyncMembership reconciles the derived seats (the prune runs the same
// qualifies predicate the rule itself carries). The wall is admin-only:
// only a role the role matrix gives Admin on house-messages may write
// a room's rule (ErrAdminOnly otherwise). An invalid visibility or an
// invalid auto_scope is refused before the write runs.
func (s *Service) UpdateMembershipRule(ctx context.Context, c Caller, channelID string, r MembershipRule) (MembershipRule, error) {
	if !houseMessagesAdmin(c) {
		return MembershipRule{}, ErrAdminOnly
	}
	visibility := strings.ToUpper(strings.TrimSpace(r.Visibility))
	if visibility != VisibilityPublic && visibility != VisibilityPrivate {
		return MembershipRule{}, ErrBadVisibility
	}
	r.Visibility = visibility
	autoScope := strings.ToUpper(strings.TrimSpace(r.AutoScope))
	if autoScope != "" && autoScope != AutoScopeBranch && autoScope != AutoScopeGlobal {
		return MembershipRule{}, fmt.Errorf("messaging: auto_scope must be empty, BRANCH or GLOBAL")
	}
	r.AutoScope = autoScope
	if err := s.repo.SetChannelRule(ctx, channelID, r); err != nil {
		return MembershipRule{}, err
	}
	return s.repo.GetChannelRule(ctx, channelID)
}

// ListMembershipRules is the read side of the membership-rules
// collection route: every channel's rule the caller can see. The
// collection is the directory's rule view, scoped the same way (the
// caller's branch reach, PUBLIC visibility).
func (s *Service) ListMembershipRules(ctx context.Context, c Caller) ([]MembershipRuleEntry, error) {
	return s.repo.ListChannelRules(ctx, c.UserID, c.IsGlobal(), c.Branches)
}
