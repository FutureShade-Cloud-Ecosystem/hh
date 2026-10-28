// syncscope.go is the messaging module's sync surface (House Messages core,
// roles roadmap Lane E): the four Stores the contract's SyncScope enum
// already declares, over this module's own tables, meeting the sync core at
// the Store seam exactly as the dispatch and tasks modules do.
//
// THE FENCE, once and plainly: these tables carry no org column and the
// GLOBAL channels cross orgs by design, so the recorded Phase D stop said an
// org fence could not be proven and the scopes answered 501. Migration 0385
// finished 0213's feed keys, and the fence these stores prove is STRONGER
// than org: every read resolves the caller through the linked ERP user the
// filter's Actor names and answers only that user's own world, its channels
// (membership) and their messages, its own acks and its own prefs. A row
// outside that world is the same as absent, whatever org it belongs to. A
// caller that resolves to no linked ERP user fails closed: empty pages,
// not-found loads, refused writes.
//
// The write legs hold the module's own rules: a message is created through
// the membership-fenced, thread-validating write the REST surface uses
// (stamped with the client key); an ack is created through the same
// channel-bound upsert; prefs writes run the module's own merge and the
// escalation-mute refusal. What the module does not admit refuses with its
// own words: sent messages are not editable or deletable over sync, an ack
// is a fact not an editable row, and the channels feed is pull-only (the
// route refuses its POST before the store is ever asked).
package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// syncLinker names the staff identity seam the stores take (the linked ERP
// user for a platform subject), the same shape the other sync adapters hold.
type syncLinker = identity.UserIDLinker

// syncFeedUser resolves the caller's linked ERP user, failing closed: a
// tenant caller, an empty actor, a nil linker or an unlinked subject has no
// messaging world to serve, and every leg answers nothing rather than
// everything.
func syncFeedUser(ctx context.Context, link syncLinker, f sync.ScopeFilter) (string, bool) {
	if f.Tenant || f.Actor == "" || link == nil {
		return "", false
	}
	uid, ok := link(ctx, f.Actor)
	if !ok || uid == "" {
		return "", false
	}
	return uid, true
}

// syncRefused wraps one of the module's invariant refusals in the Store
// seam's classified marker, so the engine reports the operation's rejected
// result with the module's own words.
func syncRefused(err error) error {
	return fmt.Errorf("%w: %w", sync.ErrOpRefused, err)
}

// syncProjection marshals one feed row through its json tags into the wire
// map the contract's SyncEntity.data carries.
func syncProjection(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("messaging: sync projection: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("messaging: sync projection decode: %w", err)
	}
	return data, nil
}

// - channels: the pull-only reference feed of the caller's rooms -

type channelsSyncStore struct {
	repo  *Repository
	link  syncLinker
	fence roomFence
}

// NewChannelsSyncStore builds the channels Store (pull-only: the route
// refuses the scope's POST in the contract's own shape before the store is
// consulted; the write primitives here are the second lock on that door).
func NewChannelsSyncStore(repo *Repository, link syncLinker) *channelsSyncStore {
	return &channelsSyncStore{repo: repo, link: link}
}

func (s *channelsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, seqs, err := s.repo.syncChannelsPage(ctx, uid, since, limit)
	if err != nil {
		return nil, nil, err
	}
	items := make([]sync.Entity, 0, len(rows))
	for i, row := range rows {
		if !admitsRoom(ctx, s.fence, uid, row.ID) {
			continue
		}
		data, err := syncProjection(row)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: row.ID, Seq: seqs[i], Data: data})
	}
	return items, []sync.Tombstone{}, nil
}

func (s *channelsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	return sync.RowState{}, false, sync.ErrOpRefused
}

func (s *channelsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	row, seq, found, err := s.repo.syncChannelLoad(ctx, uid, id)
	if err != nil || !found {
		return sync.RowState{}, found, err
	}
	if !admitsRoom(ctx, s.fence, uid, row.ID) {
		return sync.RowState{}, false, nil
	}
	data, err := syncProjection(row)
	if err != nil {
		return sync.RowState{}, false, err
	}
	return sync.RowState{ID: row.ID, Seq: seq, Data: data}, true, nil
}

func (s *channelsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, syncRefused(fmt.Errorf("the channels feed is read-only; rooms are founded through the messaging surface"))
}

func (s *channelsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, syncRefused(fmt.Errorf("the channels feed is read-only; rooms are founded through the messaging surface"))
}

func (s *channelsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, syncRefused(fmt.Errorf("the channels feed is read-only; rooms are founded through the messaging surface"))
}

// - messages: the caller's channels' messages, and the offline post -

type messagesSyncStore struct {
	repo  *Repository
	link  syncLinker
	fence roomFence
}

// admits says whether a seated user may read a room over the sync door: the money rooms follow the
// payment trail and the branch reach here as on the REST door (HH-424). Unbound keeps the seat alone.
func admitsRoom(ctx context.Context, fence roomFence, userID, channelID string) bool {
	return fence == nil || fence(ctx, userID, channelID)
}

// WithRoomFence binds the money room fence to the channels feed.
func (s *channelsSyncStore) WithRoomFence(f roomFence) *channelsSyncStore { s.fence = f; return s }

// WithRoomFence binds the money room fence to the messages feed.
func (s *messagesSyncStore) WithRoomFence(f roomFence) *messagesSyncStore { s.fence = f; return s }

// NewMessagesSyncStore builds the messages Store (server_wins: a create is
// admitted through the module's own write; edits and deletes are not the
// module's to take over sync).
func NewMessagesSyncStore(repo *Repository, link syncLinker) *messagesSyncStore {
	return &messagesSyncStore{repo: repo, link: link}
}

func (s *messagesSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, seqs, deleted, err := s.repo.syncMessagesPage(ctx, uid, since, limit)
	if err != nil {
		return nil, nil, err
	}
	items := make([]sync.Entity, 0, len(rows))
	tombstones := []sync.Tombstone{}
	for i, row := range rows {
		if !admitsRoom(ctx, s.fence, uid, row.ChannelID) {
			continue
		}
		if deleted[i] {
			tombstones = append(tombstones, sync.Tombstone{ID: row.ID, Seq: seqs[i]})
			continue
		}
		data, err := syncProjection(row)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: row.ID, Seq: seqs[i], Data: data})
	}
	return items, tombstones, nil
}

func (s *messagesSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	id, seq, channelID, found, err := s.repo.syncMessageFindApplied(ctx, uid, key)
	if err != nil || !found {
		return sync.RowState{}, found, err
	}
	// The room fence reaches this read the way it reaches Load: a member the
	// room refuses resolves nothing, or the replayed answer crosses the read
	// fence the pull holds (UX-108).
	if !admitsRoom(ctx, s.fence, uid, channelID) {
		return sync.RowState{}, false, nil
	}
	return sync.RowState{ID: id, Seq: seq}, true, nil
}

func (s *messagesSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	row, seq, found, err := s.repo.syncMessageLoad(ctx, uid, id)
	if err != nil || !found {
		return sync.RowState{}, found, err
	}
	if !admitsRoom(ctx, s.fence, uid, row.ChannelID) {
		return sync.RowState{}, false, nil
	}
	data, err := syncProjection(row)
	if err != nil {
		return sync.RowState{}, false, err
	}
	return sync.RowState{ID: row.ID, Seq: seq, Data: data}, true, nil
}

// BoundEntityExists answers the bound key gate's existence half (UX-103):
// the message row stands, tombstone aware, for every caller at once. No
// membership or room leg rides it, those being the caller's fence Load
// speaks, and the gate may hide a key only for a live row a caller cannot
// see. The module's tables keep no org column, so the bare row read is
// the caller-independent answer the contract asks for.
func (s *messagesSyncStore) BoundEntityExists(ctx context.Context, f sync.ScopeFilter, id string) (bool, error) {
	if f.Tenant || f.OrgID == "" || uuid.Validate(id) != nil {
		return false, nil
	}
	return s.repo.syncMessageExists(ctx, id)
}

func (s *messagesSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, syncRefused(fmt.Errorf("no linked staff user for the caller"))
	}
	channelID, _ := fields["channel_id"].(string)
	if channelID == "" {
		return sync.RowState{}, syncRefused(fmt.Errorf("channel_id is required"))
	}
	body, _ := fields["body"].(string)
	if body == "" {
		return sync.RowState{}, syncRefused(fmt.Errorf("message body is empty"))
	}
	if !admitsRoom(ctx, s.fence, uid, channelID) {
		return sync.RowState{}, fmt.Errorf("%w: %w", sync.ErrRowNotFound, errNotFound)
	}
	threadRoot, _ := fields["thread_root_id"].(string)
	id, seq, err := s.repo.syncMessageCreate(ctx, uid, channelID, body, threadRoot, key)
	if err != nil {
		// The module's own not-found (membership, thread root) is the
		// operation's rejected result, never a fabricated apply.
		if isRepoNotFound(err) {
			return sync.RowState{}, fmt.Errorf("%w: %w", sync.ErrRowNotFound, err)
		}
		return sync.RowState{}, err
	}
	return sync.RowState{ID: id, Seq: seq}, nil
}

func (s *messagesSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, syncRefused(fmt.Errorf("a sent message is not editable over sync"))
}

func (s *messagesSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, syncRefused(fmt.Errorf("a sent message is not deletable over sync"))
}

// - acks: the caller's own acknowledgements, and the offline ack -

type acksSyncStore struct {
	repo *Repository
	link syncLinker
}

// NewAcksSyncStore builds the acks Store (server_wins: an ack is created
// through the channel-bound upsert; it has no client-editable field and no
// delete).
func NewAcksSyncStore(repo *Repository, link syncLinker) *acksSyncStore {
	return &acksSyncStore{repo: repo, link: link}
}

func (s *acksSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, seqs, err := s.repo.syncAcksPage(ctx, uid, since, limit)
	if err != nil {
		return nil, nil, err
	}
	items := make([]sync.Entity, 0, len(rows))
	for i, row := range rows {
		data, err := syncProjection(row)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: row.ID, Seq: seqs[i], Data: data})
	}
	return items, []sync.Tombstone{}, nil
}

func (s *acksSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	id, seq, found, err := s.repo.syncAckFindApplied(ctx, uid, key)
	if err != nil || !found {
		return sync.RowState{}, found, err
	}
	return sync.RowState{ID: id, Seq: seq}, true, nil
}

func (s *acksSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	row, seq, found, err := s.repo.syncAckLoad(ctx, uid, id)
	if err != nil || !found {
		return sync.RowState{}, found, err
	}
	data, err := syncProjection(row)
	if err != nil {
		return sync.RowState{}, false, err
	}
	return sync.RowState{ID: row.ID, Seq: seq, Data: data}, true, nil
}

// BoundEntityExists answers the bound key gate's existence half (UX-103):
// the ack row stands, tombstone aware, for every caller at once, with no
// user leg (whose ack it is being the caller's fence Load speaks).
func (s *acksSyncStore) BoundEntityExists(ctx context.Context, f sync.ScopeFilter, id string) (bool, error) {
	if f.Tenant || f.OrgID == "" || uuid.Validate(id) != nil {
		return false, nil
	}
	return s.repo.syncAckExists(ctx, id)
}

func (s *acksSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, syncRefused(fmt.Errorf("no linked staff user for the caller"))
	}
	messageID, _ := fields["message_id"].(string)
	if messageID == "" {
		return sync.RowState{}, syncRefused(fmt.Errorf("message_id is required"))
	}
	method, _ := fields["method"].(string)
	m, ok := normalizeAckMethod(method)
	if !ok {
		return sync.RowState{}, syncRefused(fmt.Errorf("ack method must be one of TAP, QUICK_REPLY, VOICE"))
	}
	id, seq, err := s.repo.syncAckCreate(ctx, uid, messageID, m, key)
	if err != nil {
		if isRepoNotFound(err) {
			return sync.RowState{}, fmt.Errorf("%w: %w", sync.ErrRowNotFound, err)
		}
		return sync.RowState{}, err
	}
	return sync.RowState{ID: id, Seq: seq}, nil
}

func (s *acksSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	return sync.RowState{}, syncRefused(fmt.Errorf("an acknowledgement is a fact, not an editable row"))
}

func (s *acksSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, syncRefused(fmt.Errorf("an acknowledgement is a fact and is never withdrawn"))
}

// - notify_prefs: the caller's own prefs, written offline through the
// module's own merge and escalation rules -

type notifyPrefsSyncStore struct {
	repo *Repository
	link syncLinker
}

// NewNotifyPrefsSyncStore builds the notify_prefs Store
// (last_write_wins_by_field: the engine merges the declared fields, the
// module's own rules validate the merged row before it lands).
func NewNotifyPrefsSyncStore(repo *Repository, link syncLinker) *notifyPrefsSyncStore {
	return &notifyPrefsSyncStore{repo: repo, link: link}
}

func (s *notifyPrefsSyncStore) Pull(ctx context.Context, f sync.ScopeFilter, since int64, limit int) ([]sync.Entity, []sync.Tombstone, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return []sync.Entity{}, []sync.Tombstone{}, nil
	}
	rows, seqs, err := s.repo.syncPrefsPage(ctx, uid, since, limit)
	if err != nil {
		return nil, nil, err
	}
	items := make([]sync.Entity, 0, len(rows))
	for i, row := range rows {
		data, err := syncProjection(row)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, sync.Entity{ID: row.ID, Seq: seqs[i], Data: data})
	}
	return items, []sync.Tombstone{}, nil
}

func (s *notifyPrefsSyncStore) FindApplied(ctx context.Context, f sync.ScopeFilter, key string) (sync.RowState, bool, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	id, seq, found, err := s.repo.syncPrefsFindApplied(ctx, uid, key)
	if err != nil || !found {
		return sync.RowState{}, found, err
	}
	return sync.RowState{ID: id, Seq: seq}, true, nil
}

func (s *notifyPrefsSyncStore) Load(ctx context.Context, f sync.ScopeFilter, id string) (sync.RowState, bool, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, false, nil
	}
	row, seq, found, err := s.repo.syncPrefsLoad(ctx, uid, id)
	if err != nil || !found {
		return sync.RowState{}, found, err
	}
	data, err := syncProjection(row)
	if err != nil {
		return sync.RowState{}, false, err
	}
	return sync.RowState{ID: row.ID, Seq: seq, Data: data}, true, nil
}

// BoundEntityExists answers the bound key gate's existence half (UX-103):
// the pref row stands, tombstone aware, for every caller at once, with no
// user leg (whose row it is being the caller's fence Load speaks).
func (s *notifyPrefsSyncStore) BoundEntityExists(ctx context.Context, f sync.ScopeFilter, id string) (bool, error) {
	if f.Tenant || f.OrgID == "" || uuid.Validate(id) != nil {
		return false, nil
	}
	return s.repo.syncPrefsExists(ctx, id)
}

// Create and Write share the one write a pref row admits: the module's own
// merge-then-upsert over the caller's current row (an omitted field keeps
// the current value, never coerces), with the escalation-mute refusal the
// REST surface answers 409 for, and the client key stamped beside it.
func (s *notifyPrefsSyncStore) write(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, syncRefused(fmt.Errorf("no linked staff user for the caller"))
	}
	channelID, _ := fields["channel_id"].(string)
	if channelID == "" {
		return sync.RowState{}, syncRefused(fmt.Errorf("channel_id is required"))
	}
	patch := PrefsPatch{
		DesktopEnabled:     boolField(fields, "desktop_enabled"),
		EscalationOverride: boolField(fields, "escalation_override"),
		Muted:              boolField(fields, "muted"),
	}
	if mode, ok := fields["mode"].(string); ok {
		patch.Mode = &mode
	}
	// The module's own rules, in the service's own order: the escalation
	// refusal, the current-row merge, the mode validation.
	if silencingPatch(patch) {
		tier, err := s.repo.ChannelTier(ctx, channelID)
		if err != nil {
			return sync.RowState{}, err
		}
		if unmutableTier(tier) {
			return sync.RowState{}, syncRefused(fmt.Errorf("escalation channels cannot be muted"))
		}
	}
	cur, err := s.repo.GetNotificationPrefs(ctx, channelID, uid)
	if err != nil {
		return sync.RowState{}, err
	}
	merged, ok := mergePrefs(cur, patch)
	if !ok {
		return sync.RowState{}, syncRefused(fmt.Errorf("mode must be one of ALL, MENTIONS, MUTED"))
	}
	id, seq, err := s.repo.syncPrefsWrite(ctx, merged, key)
	if err != nil {
		return sync.RowState{}, err
	}
	return sync.RowState{ID: id, Seq: seq}, nil
}

func (s *notifyPrefsSyncStore) Create(ctx context.Context, f sync.ScopeFilter, key string, fields map[string]any) (sync.RowState, error) {
	return s.write(ctx, f, key, fields)
}

func (s *notifyPrefsSyncStore) Write(ctx context.Context, f sync.ScopeFilter, id, key string, fields map[string]any) (sync.RowState, error) {
	// The row's own id names the channel the write targets; the caller's
	// fence is proven on the row itself before the write runs.
	uid, ok := syncFeedUser(ctx, s.link, f)
	if !ok {
		return sync.RowState{}, syncRefused(fmt.Errorf("no linked staff user for the caller"))
	}
	row, _, found, err := s.repo.syncPrefsLoad(ctx, uid, id)
	if err != nil {
		return sync.RowState{}, err
	}
	if !found {
		return sync.RowState{}, sync.ErrRowNotFound
	}
	if _, ok := fields["channel_id"].(string); !ok {
		fields["channel_id"] = row.ChannelID
	}
	return s.write(ctx, f, key, fields)
}

func (s *notifyPrefsSyncStore) Delete(ctx context.Context, f sync.ScopeFilter, id, key string) (sync.RowState, error) {
	return sync.RowState{}, syncRefused(fmt.Errorf("a preference is tuned, not deleted; set the mode instead"))
}

// boolField reads one optional boolean field of a sync payload.
func boolField(fields map[string]any, name string) *bool {
	v, ok := fields[name].(bool)
	if !ok {
		return nil
	}
	return &v
}

// isRepoNotFound reports whether err carries the repository's not-found
// sentinel (the sync adapters map it onto the seam's own miss vocabulary).
func isRepoNotFound(err error) bool {
	return errors.Is(err, errNotFound)
}
