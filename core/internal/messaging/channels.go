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
	"strings"
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
	// namespaces (dm-, rep-, approvals-); a room founded there would capture
	// a later system provision that converges on the same slug.
	ErrReservedSlug = errors.New("messaging: that slug prefix is reserved for system channels")
)

// CreateChannelInput is one caller-founded channel. Slug is optional; an
// empty slug is derived from the name, so the same founding twice converges
// on one channel (the idempotent create). BranchID is optional and only
// meaningful for a TEAM scoped to one yard.
type CreateChannelInput struct {
	Kind     string
	Name     string
	Topic    string
	Slug     string
	BranchID string
}

// CreateChannel founds a TEAM or TOPIC channel idempotently (same slug, same
// channel) and seats the caller as its OWNER. The branch wall runs here: a
// channel naming a branch the caller neither holds nor reaches (GLOBAL) is
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
	in.Slug = strings.TrimSpace(in.Slug)
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

// AddMember seats one staff member in a channel the caller belongs to
// (assertMember, the module's existing wall). An already-seated member is a
// no-op; the row is EXPLICIT so the derived-membership prune never drops it.
func (s *Service) AddMember(ctx context.Context, c Caller, channelID, userID string) (ChannelMember, error) {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return ChannelMember{}, err
	}
	return s.repo.AddChannelMember(ctx, channelID, userID)
}

// RemoveMember drops one EXPLICIT member row from a channel the caller
// belongs to. A DERIVED row is refused: it belongs to the role and branch
// model and would be re-derived on the next membership sync, so removing it
// by hand would be a lie the next sync exposes.
func (s *Service) RemoveMember(ctx context.Context, c Caller, channelID, userID string) error {
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return err
	}
	return s.repo.RemoveChannelMember(ctx, channelID, userID)
}
