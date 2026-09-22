package messaging

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// - Wave 5 (Swarm D) doubles -
//
// The shipped fakeStore (service_test.go) implements the base `store` seam. This file extends the
// SAME type with the two Wave-5 seams, repStore (rep assignment + house queue) and priorityStore
// (priority events + supersession + ack arithmetic), which the service discovers by type assertion.
// Extending the existing fake rather than introducing a second one keeps every DB-free test running
// against ONE double, so a behaviour proven in one test cannot be contradicted by a different fake in
// another.

// w5State is the Wave-5 half of the double. It hangs off a package-level map keyed by the fake so the
// shipped fakeStore struct literal (and its newFakeStore constructor) stay untouched.
type w5State struct {
	assignments     []RepAssignment
	repDMs          map[string]string // repUserID -> channelID
	customerName    map[string]string
	customerBranch  map[string]string
	customerMissing map[string]bool
	houseSlug       map[string]string // branchID -> slug
	conversations   map[string]*HouseConversation
	convSeq         int
	// priority-event doubles
	optsByEvent map[string]EventOpts // eventKey -> opts the insert was called with
	messages    map[string]*Message  // messageID -> stored message (supersede target)
	supersedes  []Supersession
	ackRequired map[string]int // channelID -> member count for the ack arithmetic
	ackRows     map[string][]Ack
}

var w5states = map[*fakeStore]*w5State{}

func (f *fakeStore) w5() *w5State {
	s, ok := w5states[f]
	if !ok {
		s = &w5State{
			repDMs: map[string]string{}, customerName: map[string]string{},
			customerBranch: map[string]string{}, customerMissing: map[string]bool{},
			houseSlug: map[string]string{}, conversations: map[string]*HouseConversation{},
			optsByEvent: map[string]EventOpts{}, messages: map[string]*Message{},
			ackRequired: map[string]int{}, ackRows: map[string][]Ack{},
		}
		w5states[f] = s
	}
	return s
}

// setCustomer registers a customer the routing can resolve (name + home branch).
func (f *fakeStore) setCustomer(id, name, branchID string) {
	w := f.w5()
	w.customerName[id] = name
	w.customerBranch[id] = branchID
}

// setAssignment adds a rep_assignments row to the double (orderID "" = the customer-level row).
func (f *fakeStore) setAssignment(customerID, orderID, repUserID, source string) {
	w := f.w5()
	w.assignments = append(w.assignments, RepAssignment{
		ID: "ra-" + strconv.Itoa(len(w.assignments)+1), CustomerID: customerID,
		OrderID: orderID, RepUserID: repUserID, Source: source, AssignedAt: time.Now(),
	})
}

// - repStore -

func (f *fakeStore) RepAssignmentsForCustomer(_ context.Context, customerID string) ([]RepAssignment, error) {
	out := []RepAssignment{}
	for _, a := range f.w5().assignments {
		if a.CustomerID == customerID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) ListRepAssignments(_ context.Context, repUserID, customerID string) ([]RepAssignment, error) {
	out := []RepAssignment{}
	for _, a := range f.w5().assignments {
		if repUserID != "" && a.RepUserID != repUserID {
			continue
		}
		if customerID != "" && a.CustomerID != customerID {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// UpsertRepAssignment mirrors the two partial unique indexes: the customer-level row is unique per
// customer, the override row unique per order.
func (f *fakeStore) UpsertRepAssignment(_ context.Context, a RepAssignment) (RepAssignment, error) {
	w := f.w5()
	for i, cur := range w.assignments {
		if cur.CustomerID != a.CustomerID {
			continue
		}
		if (a.OrderID == "" && cur.OrderID == "") || (a.OrderID != "" && cur.OrderID == a.OrderID) {
			a.ID = cur.ID
			a.AssignedAt = time.Now()
			w.assignments[i] = a
			return a, nil
		}
	}
	a.ID = "ra-" + strconv.Itoa(len(w.assignments)+1)
	a.AssignedAt = time.Now()
	w.assignments = append(w.assignments, a)
	return a, nil
}

func (f *fakeStore) DeleteRepAssignment(_ context.Context, customerID, orderID string) (int, error) {
	w := f.w5()
	kept := w.assignments[:0]
	n := 0
	for _, a := range w.assignments {
		if a.CustomerID == customerID && a.OrderID == orderID {
			n++
			continue
		}
		kept = append(kept, a)
	}
	w.assignments = kept
	return n, nil
}

func (f *fakeStore) EnsureRepDMChannel(_ context.Context, userID string) (string, error) {
	w := f.w5()
	if id, ok := w.repDMs[userID]; ok {
		return id, nil // idempotent, exactly like the ON CONFLICT DO UPDATE
	}
	id := "dm-" + userID
	w.repDMs[userID] = id
	f.exists[id] = true
	return id, nil
}

func (f *fakeStore) BranchHouseSlug(_ context.Context, branchID string) (string, error) {
	if branchID == "" {
		return houseSlugUnassigned, nil
	}
	if s, ok := f.w5().houseSlug[branchID]; ok {
		return s, nil
	}
	return houseSlugUnassigned, nil // the floor: never drop an inbound
}

func (f *fakeStore) CustomerFacts(_ context.Context, customerID string) (string, string, error) {
	w := f.w5()
	if w.customerMissing[customerID] {
		return "", "", errNotFound
	}
	name, ok := w.customerName[customerID]
	if !ok {
		return "", "", errNotFound
	}
	return name, w.customerBranch[customerID], nil
}

func (f *fakeStore) UpsertHouseConversation(_ context.Context, customerID, branchID, channelID string) (string, error) {
	w := f.w5()
	key := customerID + "|" + channelID
	if c, ok := w.conversations[key]; ok {
		c.LastAt = time.Now()
		c.MessageCount++
		return c.ID, nil
	}
	w.convSeq++
	id := fmt.Sprintf("conv-%d", w.convSeq)
	c := &HouseConversation{
		ID: id, CustomerID: customerID, CustomerName: w.customerName[customerID], BranchID: branchID,
		ChannelID: channelID, FirstAt: time.Now(), LastAt: time.Now(), MessageCount: 1,
	}
	w.conversations[key] = c
	w.conversations[id] = c // also addressable by id (GetHouseConversation)
	return id, nil
}

func (f *fakeStore) ListHouseConversations(_ context.Context, channelID string, openOnly bool) ([]HouseConversation, error) {
	seen := map[string]bool{}
	out := []HouseConversation{}
	for _, c := range f.w5().conversations {
		if c.ChannelID != channelID || seen[c.ID] {
			continue
		}
		if openOnly && !c.IsOpen() {
			continue
		}
		seen[c.ID] = true
		out = append(out, *c)
	}
	return out, nil
}

func (f *fakeStore) GetHouseConversation(_ context.Context, id string) (HouseConversation, error) {
	c, ok := f.w5().conversations[id]
	if !ok {
		return HouseConversation{}, errNotFound
	}
	return *c, nil
}

// ClaimHouseConversation mirrors the transactional claim: conditional stamp (already-claimed → 409)
// PLUS the customer-level assignment that makes it sticky. Both, or neither.
func (f *fakeStore) ClaimHouseConversation(_ context.Context, conversationID, userID string) (HouseConversation, error) {
	w := f.w5()
	c, ok := w.conversations[conversationID]
	if !ok {
		return HouseConversation{}, errNotFound
	}
	if c.ClaimedBy != "" {
		return HouseConversation{}, ErrAlreadyClaimed
	}
	now := time.Now()
	c.ClaimedBy, c.ClaimedAt = userID, &now
	if name, ok := f.displayNames[userID]; ok {
		c.ClaimedByName = name
	}
	_, _ = f.UpsertRepAssignment(context.Background(), RepAssignment{
		CustomerID: c.CustomerID, RepUserID: userID, Source: AssignClaim, AssignedBy: userID,
	})
	return *c, nil
}

// - priorityStore -

func (f *fakeStore) InsertEventMessageOpts(_ context.Context, channelID, eventKey, body string, ref EventRef, opts EventOpts) (Message, bool, error) {
	opts = opts.normalize()
	key := channelID + "|" + eventKey
	if f.eventDup[key] {
		return Message{}, false, nil
	}
	f.eventDup[key] = true
	m := Message{
		ID: "ev-" + eventKey, ChannelID: channelID, Kind: MsgEvent, Body: body,
		RequiresAck: opts.RequiresAck, Priority: opts.Priority, StateKey: opts.StateKey,
		CreatedAt: time.Now(),
	}
	f.events = append(f.events, m)
	f.eventRefs = append(f.eventRefs, ref)
	w := f.w5()
	w.optsByEvent[eventKey] = opts
	stored := m
	w.messages[m.ID] = &stored
	return m, true, nil
}

// SupersedeOlderForState mirrors every clause of the SQL sweep: same key, not self, strictly older,
// forced-ack only, not already superseded, and NEVER an already-acked message.
func (f *fakeStore) SupersedeOlderForState(_ context.Context, stateKey, newMessageID string) ([]Supersession, error) {
	w := f.w5()
	if stateKey == "" || newMessageID == "" {
		return nil, nil
	}
	newMsg, ok := w.messages[newMessageID]
	if !ok {
		return nil, nil
	}
	out := []Supersession{}
	for id, m := range w.messages {
		if id == newMessageID || m.StateKey != stateKey || !m.RequiresAck {
			continue
		}
		if m.SupersededAt != nil || !m.CreatedAt.Before(newMsg.CreatedAt) {
			continue
		}
		if _, acked := f.acks[id+"|any"]; acked || f.hasAnyAck(id) {
			continue // an already-acked message keeps its ack
		}
		now := time.Now()
		m.SupersededBy, m.SupersededAt = newMessageID, &now
		out = append(out, Supersession{MessageID: id, SupersededBy: newMessageID, StateKey: stateKey, At: now})
	}
	w.supersedes = append(w.supersedes, out...)
	return out, nil
}

// hasAnyAck reports whether ANY user has acked a message (the fake's stand-in for the NOT EXISTS).
func (f *fakeStore) hasAnyAck(messageID string) bool {
	for k := range f.acks {
		if strings.HasPrefix(k, messageID+"|") {
			return true
		}
	}
	return false
}

func (f *fakeStore) AckProgressFor(_ context.Context, channelID, messageID string) (AckProgress, error) {
	w := f.w5()
	required := w.ackRequired[channelID]
	acks := []Ack{}
	for k, a := range f.acks {
		if strings.HasPrefix(k, messageID+"|") {
			acks = append(acks, a)
		}
	}
	return computeAckProgress(messageID, required, len(acks), acks), nil
}

// isSuperseded reports whether the fake recorded a supersede for a message (assertion helper).
func (f *fakeStore) isSuperseded(messageID string) bool {
	m, ok := f.w5().messages[messageID]
	return ok && m.SupersededAt != nil
}
