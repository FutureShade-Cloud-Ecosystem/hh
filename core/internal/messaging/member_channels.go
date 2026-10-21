package messaging

import "context"

// member_channels.go is the read the assistant asks before it signs a claim: which channels a person is in right
// now, as this module's own gates answer it. It is the channel list read (ListMyChannels) without the membership
// resync and without the unread projection: the same materialised msg_members rows the message reads' membership
// gate (IsMember) uses, the same archived rooms left out, and the same money room fence (RoomAdmits) applied, so a
// seat in a room the person no longer holds the payment trail or branch reach for is not listed. It writes
// nothing (the resync is a write, and a read the assistant makes must not be one); a mid-session change in a
// person's role or branch reflects on their next channel list load, exactly as it does for every other
// membership read here (UnreadSummary documents the same trade).

// MemberChannelIDs lists the ids of the channels the person is a member of and the room fence admits them to.
func (s *Service) MemberChannelIDs(ctx context.Context, userID string) ([]string, error) {
	chans, err := s.repo.ListChannelsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	admitted := map[string]bool{}
	ids := make([]string, 0, len(chans))
	for _, ch := range chans {
		if s.admits(ctx, admitted, userID, ch.ID) {
			ids = append(ids, ch.ID)
		}
	}
	return ids, nil
}
