package messaging

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type SearchMessage struct {
	MessageID   string    `json:"message_id"`
	ChannelID   string    `json:"channel_id"`
	ChannelSlug string    `json:"channel_slug"`
	ChannelName string    `json:"channel_name"`
	AuthorID    string    `json:"author_id"`
	AuthorName  string    `json:"author_name"`
	Kind        string    `json:"kind"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
}

type SearchPerson struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	RoleCode    string `json:"role_code"`
	RoleLabel   string `json:"role_label"`
	BranchLabel string `json:"branch_label"`
}

type SearchChannel struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Topic    string `json:"topic"`
	IsMember bool   `json:"is_member"`
}

type SearchResults struct {
	Messages []SearchMessage `json:"messages"`
	People   []SearchPerson  `json:"people"`
	Channels []SearchChannel `json:"channels"`
}

var ErrBadSearchQuery = errors.New("messaging: search query must contain at least 2 characters")

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

func clampSearchLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchLimit
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

func escapeSearchTerm(term string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(term)
}

func (s *Service) Search(ctx context.Context, c Caller, orgID, query string, limit int) (SearchResults, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 {
		return SearchResults{}, ErrBadSearchQuery
	}
	escaped := escapeSearchTerm(query)
	limit = clampSearchLimit(limit)
	messages, err := s.repo.SearchMessages(ctx, c.UserID, escaped, limit)
	if err != nil {
		return SearchResults{}, err
	}
	people, err := s.repo.SearchPeople(ctx, orgID, escaped, limit)
	if err != nil {
		return SearchResults{}, err
	}
	channels, err := s.repo.SearchChannels(ctx, c.UserID, orgID, escaped, limit)
	if err != nil {
		return SearchResults{}, err
	}
	admitted := map[string]bool{}
	keptMessages := make([]SearchMessage, 0, len(messages))
	for _, m := range messages {
		if s.admits(ctx, admitted, c.UserID, m.ChannelID) {
			keptMessages = append(keptMessages, m)
		}
	}
	messages = keptMessages
	if people == nil {
		people = []SearchPerson{}
	}
	if channels == nil {
		channels = []SearchChannel{}
	}
	return SearchResults{Messages: messages, People: people, Channels: channels}, nil
}
