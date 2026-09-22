package messaging

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

// - DIB-449 doubles -
//
// Extends the SAME fakeStore the rest of the package tests against (the w5_fake_test.go precedent)
// with the two DIB-449 seams, classStore and rollupStore, which the service discovers by type
// assertion. One double for the whole package means a behaviour proven in one test cannot be
// contradicted by a different fake in another.
//
// The fake mirrors the SQL's invariants, not merely its happy path:
//   - the rollup root is unique per (channel, event_key), the schema's "a day is ONE message";
//   - a reply is idempotent on its own event key, at-least-once redelivery adds nothing;
//   - the aggregate is a FOLD over live replies, never a running counter.

type rollupState struct {
	classes  map[string]*EventClass
	replies  map[string][]Message // rootID -> replies, in arrival order
	refByID  map[string]EventRef
	bodyByID map[string]string
	seq      int
}

var rollupStates = map[*fakeStore]*rollupState{}

func (f *fakeStore) rollup() *rollupState {
	s, ok := rollupStates[f]
	if !ok {
		s = &rollupState{
			classes:  map[string]*EventClass{},
			replies:  map[string][]Message{},
			refByID:  map[string]EventRef{},
			bodyByID: map[string]string{},
		}
		rollupStates[f] = s
	}
	return s
}

// seedDefaultClasses loads the 0152 seed rows into the double so a test exercises the SHIPPED
// classification rather than one invented for the test.
func (f *fakeStore) seedDefaultClasses() {
	r := f.rollup()
	for _, c := range []EventClass{
		{Topic: TopicPaymentReceived, Class: ClassRoutine},
		{Topic: TopicOrderReleased, Class: ClassNotable},
		{Topic: TopicStockReceived, Class: ClassNotable},
		{Topic: TopicDeliveryScheduled, Class: ClassNotable},
		{Topic: TopicOrderBlocked, Class: ClassCritical, Locked: true},
		{Topic: TopicCreditHold, Class: ClassCritical, Locked: true},
	} {
		row := c
		r.classes[c.Topic] = &row
	}
}

// setClass forces one topic's configured class (used to prove the router follows CONFIG, and to
// simulate a corrupted row the code-level allowlist must override).
func (f *fakeStore) setClass(topic, class string, locked bool) {
	f.rollup().classes[topic] = &EventClass{Topic: topic, Class: class, Locked: locked}
}

// - classStore -

func (f *fakeStore) ListEventClasses(_ context.Context) ([]EventClass, error) {
	out := []EventClass{}
	for _, t := range MessagingTopics { // stable order
		if c, ok := f.rollup().classes[t]; ok {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeStore) EventClassFor(_ context.Context, topic string) (string, error) {
	if c, ok := f.rollup().classes[topic]; ok {
		return c.Class, nil
	}
	return "", nil // no row: effectiveClass applies the safe NOTABLE default
}

// SetEventClass mirrors the repository's `AND locked = FALSE` predicate, the second guard behind the
// service's refusal, so a test that bypasses the service still cannot reclassify a locked row.
func (f *fakeStore) SetEventClass(_ context.Context, topic, class, byUserID string) (EventClass, error) {
	c, ok := f.rollup().classes[topic]
	if !ok || c.Locked {
		return EventClass{}, errNotFound
	}
	c.Class = class
	c.UpdatedBy = byUserID
	c.UpdatedAt = time.Now()
	return *c, nil
}

// - rollupStore -

func (f *fakeStore) FindEventMessage(_ context.Context, channelID, eventKey string) (Message, error) {
	for i := range f.events {
		if f.events[i].ChannelID == channelID && f.events[i].ID == "ev-"+eventKey {
			m := f.events[i]
			if b, ok := f.rollup().bodyByID[m.ID]; ok {
				m.Body = b
			}
			return m, nil
		}
	}
	return Message{}, errNotFound
}

func (f *fakeStore) InsertEventReply(_ context.Context, channelID, rootID, eventKey, body string, ref EventRef) (Message, bool, error) {
	if rootID == "" {
		return Message{}, false, errNotFound
	}
	r := f.rollup()
	key := channelID + "|" + eventKey
	if f.eventDup[key] {
		return Message{}, false, nil // idempotent redelivery, mirrors ON CONFLICT DO NOTHING
	}
	f.eventDup[key] = true
	r.seq++
	m := Message{
		ID: "reply-" + strconv.Itoa(r.seq), ChannelID: channelID, Kind: MsgEvent,
		Body: body, ThreadRootID: rootID, CreatedAt: time.Now(),
	}
	if raw, err := json.Marshal(ref); err == nil {
		m.Ref = raw
	}
	r.replies[rootID] = append(r.replies[rootID], m)
	r.refByID[m.ID] = ref
	return m, true, nil
}

// RollupAggregate is a FOLD, exactly like the SQL: it counts and sums the stored replies. It does not
// keep a running total, because a running total is the bug the recompute exists to prevent.
func (f *fakeStore) RollupAggregate(_ context.Context, rootID string) (int, int64, error) {
	r := f.rollup()
	var cents int64
	for _, m := range r.replies[rootID] {
		cents += r.refByID[m.ID].AmountCents
	}
	return len(r.replies[rootID]), cents, nil
}

func (f *fakeStore) UpdateEventBody(_ context.Context, messageID, body string, ref EventRef) (Message, error) {
	r := f.rollup()
	for i := range f.events {
		if f.events[i].ID != messageID {
			continue
		}
		r.bodyByID[messageID] = body
		r.refByID[messageID] = ref
		m := f.events[i]
		m.Body = body
		m.ReplyCount = len(r.replies[messageID])
		if raw, err := json.Marshal(ref); err == nil {
			m.Ref = raw
		}
		return m, nil
	}
	return Message{}, errNotFound
}

// - assertion helpers -

// rootsIn returns the TOP-LEVEL event messages the fake recorded for a channel, i.e. what the
// channel SURFACE shows. Replies are excluded because they live under a root, not in the stream.
func (f *fakeStore) rootsIn(channelID string) []Message {
	out := []Message{}
	for i := range f.events {
		if f.events[i].ChannelID != channelID {
			continue
		}
		m := f.events[i]
		// The in-place update rewrites BOTH body and ref (the SQL does), so the read model must too -
		// otherwise a test would assert against the seed row and never see the recompute.
		r := f.rollup()
		if b, ok := r.bodyByID[m.ID]; ok {
			m.Body = b
		}
		if ref, ok := r.refByID[m.ID]; ok {
			if raw, err := json.Marshal(ref); err == nil {
				m.Ref = raw
			}
		}
		m.ReplyCount = len(r.replies[m.ID])
		out = append(out, m)
	}
	return out
}

// repliesUnder returns the thread replies recorded under a root.
func (f *fakeStore) repliesUnder(rootID string) []Message { return f.rollup().replies[rootID] }
