package messaging

import (
	"context"
	"time"
)

type SavedMessage struct {
	MessageID   string    `json:"message_id"`
	ChannelID   string    `json:"channel_id"`
	ChannelSlug string    `json:"channel_slug"`
	ChannelName string    `json:"channel_name"`
	AuthorID    string    `json:"author_id"`
	AuthorName  string    `json:"author_name"`
	Kind        string    `json:"kind"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
	SavedAt     time.Time `json:"saved_at"`
}

const (
	defaultSavedLimit = 50
	maxSavedLimit     = 100
)

func clampSavedLimit(limit int) int {
	if limit <= 0 {
		return defaultSavedLimit
	}
	if limit > maxSavedLimit {
		return maxSavedLimit
	}
	return limit
}

// SaveMessage saves a message for the caller. A message of a money room the caller's trail does not
// admit is refused as not a member (HH-424): the repository resolves the room, so the fence asks after
// the save and takes the row back, leaving nothing saved.
func (s *Service) SaveMessage(ctx context.Context, c Caller, messageID string) (SavedMessage, error) {
	item, err := s.repo.SaveMessage(ctx, c.UserID, messageID)
	if err != nil {
		return SavedMessage{}, err
	}
	if !s.RoomAdmits(ctx, c.UserID, item.ChannelID) {
		if derr := s.repo.DeleteSavedMessage(ctx, c.UserID, messageID); derr != nil {
			return SavedMessage{}, derr
		}
		return SavedMessage{}, ErrNotMember
	}
	return item, nil
}

func (s *Service) ListSavedMessages(ctx context.Context, c Caller, limit int) ([]SavedMessage, error) {
	items, err := s.repo.ListSavedMessages(ctx, c.UserID, clampSavedLimit(limit))
	if err != nil {
		return nil, err
	}
	// Saved before a role change must not read back once the reader refuses (HH-424).
	admitted := map[string]bool{}
	kept := make([]SavedMessage, 0, len(items))
	for _, it := range items {
		if s.admits(ctx, admitted, c.UserID, it.ChannelID) {
			kept = append(kept, it)
		}
	}
	return kept, nil
}

func (s *Service) DeleteSavedMessage(ctx context.Context, c Caller, messageID string) error {
	return s.repo.DeleteSavedMessage(ctx, c.UserID, messageID)
}
