package assist

// conversation_store.go, TanyAI slice 1 sub item 9 (review r2, P2-5): server rebuilt history, keyed
// by conversation AND session, never by user.
//
// WHY NOT BY USER. On the demo every visitor of a flow is the same persona user. History keyed by
// user would hand one visitor another's questions and answers, which is wrong on a demo and a leak in
// principle. A conversation here is bound to the asker's user id AND a session key taken from the
// verified bearer's jti (two sign ins, two jtis, even for one user), and the stored history is read
// back only when BOTH match the request. Anything else starts a new conversation, with no error
// that says an id existed.
//
// APPEND ONLY. The interface has no update and no delete: an exchange, once logged, is never
// changed (the shape of the 0154 ledger trigger). The Postgres implementation, and its migration,
// come with the principal's migration number; MemoryConversations is the implementation wired until
// then and the one the tests use.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Exchange is one logged turn pair: what was asked, and what the assistant answered with the
// structured half the resolver reads on the next turn.
type Exchange struct {
	Question string
	Answer   string
	Ran      []Invocation
	Clarify  *Clarify
}

// ConversationStore is the append only exchange log.
type ConversationStore interface {
	// NewConversation issues an opaque id bound to the user and the session.
	NewConversation(ctx context.Context, userID, sessionKey string) (string, error)
	// Exchanges returns the logged exchanges when the conversation exists AND both the user and the
	// session match. ok is false for every other case, and the caller cannot tell which.
	Exchanges(ctx context.Context, id, userID, sessionKey string) (ex []Exchange, ok bool, err error)
	// AppendExchange logs one exchange; a no-op when the conversation does not match.
	AppendExchange(ctx context.Context, id, userID, sessionKey string, e Exchange) error
}

type memConversation struct {
	userID, sessionKey string
	exchanges          []Exchange
}

// MemoryConversations is a bounded in memory ConversationStore.
type MemoryConversations struct {
	mu       sync.Mutex
	byID     map[string]*memConversation
	order    []string // creation order, for eviction
	maxConvs int
	maxEx    int
}

// NewMemoryConversations builds the in memory store. Both bounds keep a long lived process from
// growing without limit; losing a conversation only starts a new one.
func NewMemoryConversations() *MemoryConversations {
	return &MemoryConversations{byID: map[string]*memConversation{}, maxConvs: 20000, maxEx: 50}
}

func (m *MemoryConversations) NewConversation(_ context.Context, userID, sessionKey string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := "cv_" + hex.EncodeToString(b[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.order) >= m.maxConvs {
		delete(m.byID, m.order[0])
		m.order = m.order[1:]
	}
	m.byID[id] = &memConversation{userID: userID, sessionKey: sessionKey}
	m.order = append(m.order, id)
	return id, nil
}

func (m *MemoryConversations) match(id, userID, sessionKey string) *memConversation {
	c := m.byID[id]
	if c == nil || userID == "" || sessionKey == "" || c.userID != userID || c.sessionKey != sessionKey {
		return nil
	}
	return c
}

func (m *MemoryConversations) Exchanges(_ context.Context, id, userID, sessionKey string) ([]Exchange, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.match(id, userID, sessionKey)
	if c == nil {
		return nil, false, nil
	}
	return append([]Exchange(nil), c.exchanges...), true, nil
}

func (m *MemoryConversations) AppendExchange(_ context.Context, id, userID, sessionKey string, e Exchange) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.match(id, userID, sessionKey)
	if c == nil {
		return nil
	}
	c.exchanges = append(c.exchanges, e)
	if len(c.exchanges) > m.maxEx {
		c.exchanges = c.exchanges[len(c.exchanges)-m.maxEx:]
	}
	return nil
}

// historyFrom rebuilds the message history a logged conversation stands for. The structured half
// (Ran, Clarify) comes from the server's own log, which is the point: it is no longer client input.
func historyFrom(ex []Exchange) []Message {
	out := make([]Message, 0, 2*len(ex))
	for _, e := range ex {
		out = append(out,
			Message{Role: RoleUser, Text: e.Question},
			Message{Role: RoleAssistant, Text: e.Answer, Ran: e.Ran, Clarify: e.Clarify},
		)
	}
	return out
}

// maxConversationIDLen bounds an id read from a request; anything longer is not one of ours.
const maxConversationIDLen = 64

// AskConversation answers one question inside a server kept conversation.
//
//   - With no session key (a bearer with no jti), no user id, or no store wired: stateless. Nothing
//     is stored and no id is returned. When the request named a conversation, client history is
//     ignored too, so a client cannot pick the stateless path to smuggle history in.
//   - Otherwise a conversation_id that matches both the user and the session supplies the history,
//     and client history and ran are ignored. An id that does not match (absent, unknown, someone
//     else's, from another sign in of the same user) starts a new conversation with nothing from the
//     old one and no error. Without a conversation_id the v1 behaviour stays: the client's history is
//     used for this turn, and a conversation is opened so the client can adopt the id.
func (s *Service) AskConversation(ctx context.Context, c Caller, conversationID, question string, clientHistory []Message) (Answer, error) {
	if s.convs == nil || c.SessionKey == "" || c.UserID == "" {
		var h []Message
		if conversationID == "" {
			h = clientHistory
		}
		return s.Ask(ctx, c, question, h)
	}

	id := ""
	var history []Message
	if conversationID != "" && len(conversationID) <= maxConversationIDLen {
		ex, ok, err := s.convs.Exchanges(ctx, conversationID, c.UserID, c.SessionKey)
		if err != nil {
			return Answer{}, err
		}
		if ok {
			id, history = conversationID, historyFrom(ex)
		}
	}
	if id == "" {
		fresh, err := s.convs.NewConversation(ctx, c.UserID, c.SessionKey)
		if err != nil {
			return Answer{}, err
		}
		id = fresh
		if conversationID == "" {
			history = clientHistory // v1: this one turn only
		}
	}

	ans, err := s.Ask(ctx, c, question, history)
	if err != nil {
		return ans, err
	}
	ans.ConversationID = id
	if aerr := s.convs.AppendExchange(ctx, id, c.UserID, c.SessionKey, Exchange{
		Question: question, Answer: ans.Text, Ran: ans.Ran, Clarify: ans.Clarify,
	}); aerr != nil && s.log != nil {
		s.log.Error("assist: conversation log write failed, this exchange is not kept", "err", aerr)
	}
	return ans, nil
}
