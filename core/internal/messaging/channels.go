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
	return s.repo.CreateChannel(ctx, in, c.UserID)
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
