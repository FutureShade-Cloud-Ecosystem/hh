package adapters

// channel_membership.go, assist.ChannelMembership over the messaging module's own membership read: the channels a
// person is in right now, the room fence applied, nothing written. The assist handler signs the ids into the claim.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

// ChannelLookup is what this adapter needs from the messaging module.
type ChannelLookup interface {
	MemberChannelIDs(ctx context.Context, userID string) ([]string, error)
}

type channelMembershipAdapter struct{ svc ChannelLookup }

var _ assist.ChannelMembership = channelMembershipAdapter{}

// NewChannelMembership builds the seam. A nil service returns a nil membership: the handler then gives no caller
// any channel.
func NewChannelMembership(svc ChannelLookup) assist.ChannelMembership {
	if svc == nil {
		return nil
	}
	return channelMembershipAdapter{svc: svc}
}

func (a channelMembershipAdapter) ChannelIDs(ctx context.Context, userID string) ([]string, error) {
	return a.svc.MemberChannelIDs(ctx, userID)
}
