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

func (s *Service) SaveMessage(ctx context.Context, c Caller, messageID string) (SavedMessage, error) {
	return s.repo.SaveMessage(ctx, c.UserID, messageID)
}

func (s *Service) ListSavedMessages(ctx context.Context, c Caller, limit int) ([]SavedMessage, error) {
	items, err := s.repo.ListSavedMessages(ctx, c.UserID, clampSavedLimit(limit))
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []SavedMessage{}
	}
	return items, nil
}

func (s *Service) DeleteSavedMessage(ctx context.Context, c Caller, messageID string) error {
	return s.repo.DeleteSavedMessage(ctx, c.UserID, messageID)
}
