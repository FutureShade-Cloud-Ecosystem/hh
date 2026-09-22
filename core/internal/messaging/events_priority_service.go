package messaging

import (
	"context"
	"errors"
	"time"
)

// - DIB-217 / DIB-268 / DIB-269 service layer -

// nowUnixNano is indirected so a test can pin the fallback event key. Kept package-level (not a
// Service field) because it is only ever used for a degenerate fallback path.
var nowUnixNano = func() int64 { return time.Now().UnixNano() }

// priorityStore is the DIB-217 persistence seam, declared in this package and satisfied by
// *Repository plus the unit-test fake. Discovered by type assertion off the base store seam so the
// shipped `store` interface, and every fake implementing it, stays unchanged.
type priorityStore interface {
	InsertEventMessageOpts(ctx context.Context, channelID, eventKey, body string, ref EventRef, opts EventOpts) (Message, bool, error)
	SupersedeOlderForState(ctx context.Context, stateKey, newMessageID string) ([]Supersession, error)
	AckProgressFor(ctx context.Context, channelID, messageID string) (AckProgress, error)
}

func (s *Service) prio() (priorityStore, error) {
	if ps, ok := s.repo.(priorityStore); ok {
		return ps, nil
	}
	return nil, errNoDB
}

// postEventToChannelID writes an EVENT message to a channel ID (rather than a slug) with the 0138
// options, runs the LAST-WRITE-WINS supersede sweep, and fans out to realtime on a fresh insert. It is
// the single write path for every event this wave adds (priority events, customer messages, claim
// notices), so the supersede rule cannot be forgotten at one call site and honoured at another.
func (s *Service) postEventToChannelID(ctx context.Context, channelID, eventKey, body string, ref EventRef, opts EventOpts) (Message, bool, error) {
	ps, err := s.prio()
	if err != nil {
		return Message{}, false, err
	}
	m, inserted, err := ps.InsertEventMessageOpts(ctx, channelID, eventKey, body, ref, opts)
	if err != nil {
		return Message{}, false, err
	}
	if !inserted {
		return m, false, nil // idempotent redelivery: supersedes nothing, fans out nothing
	}
	// The ruling: a NEW fact about a record auto-resolves the OLDER un-acked bubbles for that record.
	// Run for EVERY keyed event, not only high-priority ones, the common real case is an ordinary
	// "order released" resolving a forced-ack "order blocked" for the same order.
	//
	// A MIRROR is excluded: it is a copy of a fact already asserted, not a new assertion, and letting a
	// copy sweep would auto-resolve its own original (see EventOpts.Mirror).
	if opts.StateKey != "" && !opts.Mirror {
		if sup, err := s.supersede(ctx, opts.StateKey, m.ID); err == nil && len(sup) > 0 {
			// Re-publish so an open bubble on someone's screen dismisses without waiting for the poll.
			s.fanout(channelID, m)
		}
	}
	s.fanout(channelID, m)
	return m, true, nil
}

// supersede runs the auto-resolve sweep. A sweep failure is NOT fatal to the post: the new fact is
// already durably recorded, and the stale bubble self-heals on the next event or on the manual ack.
// Losing the new message because we could not tidy an old one would be strictly worse.
func (s *Service) supersede(ctx context.Context, stateKey, newMessageID string) ([]Supersession, error) {
	ps, err := s.prio()
	if err != nil {
		return nil, err
	}
	return ps.SupersedeOlderForState(ctx, stateKey, newMessageID)
}

// PostPriorityEvent is the DIB-217 producer seam: post a high-priority EVENT to a channel slug, forcing
// an ack, stamping the state key, running the supersede sweep, and MIRRORING it into the un-mutable
// #escalations channel (DIB-269's feed from the priority tier).
//
// The mirror is best-effort and deliberately posted SECOND: the home-channel post is the delivery that
// matters, and an unseeded/renamed escalation channel must not cost the branch its alert.
func (s *Service) PostPriorityEvent(ctx context.Context, slug, eventKey, body string, ref EventRef, opts EventOpts) (Message, bool, error) {
	channelID, err := s.repo.ChannelIDBySlug(ctx, slug)
	if err != nil {
		return Message{}, false, err
	}
	m, inserted, err := s.postEventToChannelID(ctx, channelID, eventKey, body, ref, opts)
	if err != nil {
		return Message{}, false, err
	}
	if opts.normalize().Priority == PriorityHigh {
		s.mirrorToEscalations(ctx, eventKey, body, ref, opts)
	}
	return m, inserted, nil
}

// mirrorToEscalations copies a HIGH-priority event into the un-mutable ESCALATION channel. The mirror
// carries its own event key (escalationMirrorKey) so it dedups independently, and it deliberately does
// NOT set requires_ack: acking is done once, on the HOME message, where the ack arithmetic lives.
// A second forced-ack copy would double-count every alert in AckProgress and force everyone to tap
// twice for one fact.
func (s *Service) mirrorToEscalations(ctx context.Context, eventKey, body string, ref EventRef, opts EventOpts) {
	channelID, err := s.repo.ChannelIDBySlug(ctx, escalationSlug)
	if err != nil {
		return // not seeded in this environment, the home post already landed
	}
	// Mirror: true is load-bearing, not decoration. Without it the mirror, same state key, written a
	// moment later, reads to the sweep as a NEWER fact about the record and auto-resolves the home
	// channel's forced-ack bubble. The escalation copy would silently dismiss the alert it exists to
	// amplify. (Caught by the Chromium walkthrough; pinned by
	// TestPriorityEvent_MirrorDoesNotSupersedeItsOwnSource.)
	mirrorOpts := EventOpts{Priority: PriorityNormal, StateKey: opts.StateKey, Mirror: true}
	_, _, _ = s.postEventToChannelID(ctx, channelID, escalationMirrorKey(eventKey), body, ref, mirrorOpts)
}

// AckProgress returns the per-message ack arithmetic (DIB-275), requiring channel membership, the
// same gate AckMessage uses, so a non-member can neither ack nor audit a channel's acks.
func (s *Service) AckProgress(ctx context.Context, c Caller, channelID, messageID string) (AckProgress, error) {
	ps, err := s.prio()
	if err != nil {
		return AckProgress{}, err
	}
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return AckProgress{}, err
	}
	return ps.AckProgressFor(ctx, channelID, messageID)
}

// - DIB-269: the escalation channel cannot be muted -

// ErrChannelUnmutable is a preference write that would silence an ESCALATION-tier channel. It maps to
// HTTP 409 (conflict with the channel's class), not 403: the caller is permitted to change their
// prefs, but not THIS pref on THIS channel.
var ErrChannelUnmutable = errors.New("messaging: escalation channels cannot be muted")

// unmutableTier reports whether a channel tier refuses a silencing preference write.
//
// ESCALATION only, deliberately NOT FIREHOSE. FIREHOSE (#alerts-all, 0069) already shipped with a
// DIFFERENT and softer contract: it is mutable, and a muted firehose still pierces for users whose
// escalation_override is on (shouldNotify). Retro-fitting a refusal onto it would silently break a
// shipped, tested behaviour and take away a choice users already have. ESCALATION is the new class
// created precisely to carry the "must never be missed" events, so the hard refusal belongs there.
func unmutableTier(tier string) bool { return tier == TierEscalation }

// silencingPatch reports whether a prefs patch would silence the channel: an explicit MUTED mode, or
// the legacy muted boolean set true. Both routes are checked because both still exist, refusing one
// and not the other would leave an open door to the same outcome.
func silencingPatch(patch PrefsPatch) bool {
	if patch.Mode != nil {
		return *patch.Mode == ModeMuted
	}
	return patch.Muted != nil && *patch.Muted
}

// mergePrefs applies a patch over the caller's current prefs. Mode WINS over the legacy Muted boolean
// when both are sent (Mode is the more specific instruction), and the pair is always written through
// applyMode so mode and its boolean shadow cannot drift. Returns ok=false for an unknown mode.
func mergePrefs(cur NotificationPrefs, patch PrefsPatch) (NotificationPrefs, bool) {
	if patch.DesktopEnabled != nil {
		cur.DesktopEnabled = *patch.DesktopEnabled
	}
	if patch.EscalationOverride != nil {
		cur.EscalationOverride = *patch.EscalationOverride
	}
	mode := ModeOf(cur)
	if patch.Muted != nil {
		if *patch.Muted {
			mode = ModeMuted
		} else if mode == ModeMuted {
			mode = ModeAll
		}
	}
	if patch.Mode != nil {
		m, ok := normalizeMode(*patch.Mode)
		if !ok {
			return NotificationPrefs{}, false
		}
		mode = m
	}
	return applyMode(cur, mode), true
}

// ErrBadMode is a prefs write with a mode outside the CHECK set (client fault → 400).
var ErrBadMode = errors.New("messaging: unknown notification mode")
