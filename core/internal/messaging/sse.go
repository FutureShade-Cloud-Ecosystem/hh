package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// SSEHub is the FR-7 real-time delivery mechanism: Server-Sent Events over the existing Go API (no
// WebSocket upgrade, no new infra). It is an in-process fan-out, every open staff session
// subscribes, and a new message is pushed to the sessions whose user is a member of the message's
// channel. This is the P1 real-time approach (SSE, chosen over short-poll); the poll of /unread is
// the graceful fallback when the hub is not wired or a proxy buffers the stream.
//
// Membership filtering is done at publish time via a per-message channel-member lookup so a user
// only receives events for channels they belong to (channel-scoping holds on the live stream, not
// just the REST reads). For P1's volume (a handful of staff) the per-message lookup is fine; a
// higher-throughput design would cache each subscriber's channel set and invalidate on membership
// change (noted, not needed now).
type SSEHub struct {
	mu   sync.RWMutex
	subs map[string]map[*subscriber]struct{} // userID -> set of that user's open connections
	repo memberChecker
	// fence, when set, is asked after the membership filter: the money rooms follow the payment trail
	// and the branch reach, not the seat alone (payment_access.go).
	fence roomFence
	log   *slog.Logger
}

// memberChecker is the one membership read the hub's publish filter needs.
type memberChecker interface {
	IsMember(ctx context.Context, channelID, userID string) (bool, error)
}

// roomFence says whether a seated user may hear a room. It is the service's RoomAdmits.
type roomFence func(ctx context.Context, userID, channelID string) bool

// WithRoomFence binds the money room fence to the publish filter. Unbound keeps the seat alone.
func (h *SSEHub) WithRoomFence(f roomFence) *SSEHub { h.fence = f; return h }

type subscriber struct {
	ch chan sseEvent
}

// sseEvent is one server-sent event: an event name + a JSON data payload.
type sseEvent struct {
	Name string
	Data any
}

// NewSSEHub builds the hub over the repository (used for the publish-time membership filter).
func NewSSEHub(repo *Repository, log *slog.Logger) *SSEHub {
	if log == nil {
		log = slog.Default()
	}
	return &SSEHub{subs: map[string]map[*subscriber]struct{}{}, repo: repo, log: log}
}

// PublishMessage implements the service's realtime seam: fan a new message out to every subscribed
// session whose user is a member of the channel. A system EVENT message reaches all channel members;
// a TEXT message likewise (the author's own session simply re-renders the echo it already has).
func (h *SSEHub) PublishMessage(channelID string, m Message) {
	// Snapshot the currently-subscribed user ids, then filter to channel members. Best-effort: a
	// publish failure never blocks the poster (the message is already committed).
	h.mu.RLock()
	userIDs := make([]string, 0, len(h.subs))
	for uid := range h.subs {
		userIDs = append(userIDs, uid)
	}
	h.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, uid := range userIDs {
		ok, err := h.repo.IsMember(ctx, channelID, uid)
		if err != nil || !ok {
			continue
		}
		h.deliver(uid, sseEvent{Name: "message", Data: m})
		// Also nudge the unread badge to refresh for members other than the author.
		if m.AuthorID != uid {
			h.deliver(uid, sseEvent{Name: "unread", Data: map[string]string{"channel_id": channelID}})
		}
	}
}

// NotifyUser is the approvals-rail's own-request delivery seam (D-P1-1): a thin PUBLIC wrapper over
// deliver that pushes an event straight to one user's open sessions, bypassing the channel-membership
// filter PublishMessage applies. It is used ONLY to notify a requester about the fate of THEIR OWN
// approval request (event "approval.decided"), it never carries another user's channel content, so it
// leaks nothing beyond the requester's own request status. Best-effort like every SSE push (a full
// buffer drops; the client's poll fallback reconciles).
func (h *SSEHub) NotifyUser(userID, event string, data any) {
	h.deliver(userID, sseEvent{Name: event, Data: data})
}

// deliver pushes an event to all of a user's open connections, dropping it for any connection whose
// buffer is full (a slow/stuck client must never block the hub).
func (h *SSEHub) deliver(userID string, ev sseEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subs[userID] {
		select {
		case sub.ch <- ev:
		default:
			// Buffer full, drop; the client's next poll/reconnect reconciles from the DB.
		}
	}
}

// Serve holds one SSE connection open for a user until the client disconnects. It registers a
// subscriber, streams events as they arrive, and sends a periodic heartbeat comment so proxies keep
// the connection alive and a dead peer is detected. svc is unused here but kept in the signature so
// a future initial-state push (e.g. current unread) has the seam.
func (h *SSEHub) Serve(w http.ResponseWriter, r *http.Request, userID string, _ *Service) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering so events flush promptly

	// Use http.NewResponseController rather than a direct w.(http.Flusher) assertion: the controller
	// walks Unwrap() chains, so the flush reaches the REAL writer even through the audit middleware's
	// statusRecorder wrapper (main.go:471). The old direct assertion failed on EVERY request through
	// that chain, the diagnosed prod/staging SSE bug. A truly unflushable writer (no Flusher anywhere
	// in its chain, e.g. behind a buffering proxy) returns ErrNotSupported and has written NOTHING, so
	// we can still cleanly degrade to 501 and the client short-polls /unread. The first Flush both
	// commits the stream headers AND proves flushability.
	rc := http.NewResponseController(w)
	if err := rc.Flush(); errors.Is(err, http.ErrNotSupported) {
		httpx.NotImplemented(w, "sse streaming unavailable; poll /api/v1/messaging/unread")
		return
	}

	sub := &subscriber{ch: make(chan sseEvent, 64)}
	h.add(userID, sub)
	defer h.remove(userID, sub)

	// Tell the client we're live so it can stop any poll fallback.
	writeSSE(w, "ready", map[string]bool{"ok": true})
	_ = rc.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.ch:
			writeSSE(w, ev.Name, ev.Data)
			_ = rc.Flush()
		case <-heartbeat.C:
			// SSE comment line, keeps intermediaries from closing an idle stream.
			_, _ = w.Write([]byte(": ping\n\n"))
			_ = rc.Flush()
		}
	}
}

func (h *SSEHub) add(userID string, sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[userID] == nil {
		h.subs[userID] = map[*subscriber]struct{}{}
	}
	h.subs[userID][sub] = struct{}{}
}

func (h *SSEHub) remove(userID string, sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set := h.subs[userID]; set != nil {
		delete(set, sub)
		if len(set) == 0 {
			delete(h.subs, userID)
		}
	}
	close(sub.ch)
}

// writeSSE serializes one event in the SSE wire format (event: <name>\n data: <json>\n\n).
func writeSSE(w http.ResponseWriter, name string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("event: " + name + "\ndata: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
}
