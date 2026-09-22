package messaging

import (
	"errors"
	"time"
)

// - DIB-273 + DIB-333: rep assignment · DIB-271: the house fallback queue -
//
// THE RULING (see the 0136 migration header for the full statement and its reversibility):
//
//	rep assignment is CUSTOMER-LEVEL, with an OPTIONAL PER-ORDER OVERRIDE.
//
// Resolution order, and the ONE place it is expressed, is resolveAssignment below:
//
//	order override (only when the inbound names an order) → customer-level → NONE
//
// NONE is not a failure: it is DIB-271, the house queue. An unassigned customer message routes to
// #house-<branch> (or #house-unassigned when the customer has no branch) where any staffer granted
// that branch can CLAIM it. A claim writes a customer-level assignment with source CLAIM, which is
// exactly what makes the claim STICKY, the next inbound from that customer resolves to the claimer.

// Assignment sources (mirror the rep_assignments.source CHECK).
const (
	// AssignManual, an admin set it in the rep-assignment admin UI.
	AssignManual = "MANUAL"
	// AssignClaim, a rep claimed the conversation out of the house queue (DIB-271). The claim IS the
	// assignment; that is the whole mechanism of stickiness.
	AssignClaim = "CLAIM"
	// AssignImport, reserved for a bulk book-of-business import (schema admits it; nothing writes it).
	AssignImport = "IMPORT"
)

// Assignment levels, WHICH ledger row answered the routing question. Returned on the route result so
// the UI (and a test) can see not just WHO but WHY.
const (
	LevelOrder    = "ORDER"    // the per-order override won (split books)
	LevelCustomer = "CUSTOMER" // the customer-level default won
	LevelNone     = "NONE"     // no assignment, house queue (DIB-271)
)

// Route kinds, where an inbound customer message actually landed.
const (
	RouteRepDM = "REP_DM" // the assigned rep's DM
	RouteHouse = "HOUSE"  // the branch house queue (or the global floor)
)

// RepAssignment is one rep_assignments row. OrderID is empty for the CUSTOMER-LEVEL row (the routable
// default) and set for the PER-ORDER OVERRIDE (split books). CustomerName / OrderNo / RepName are
// display joins populated only by the list read model.
type RepAssignment struct {
	ID           string    `json:"id"`
	CustomerID   string    `json:"customer_id"`
	CustomerName string    `json:"customer_name,omitempty"`
	OrderID      string    `json:"order_id,omitempty"`
	OrderNo      string    `json:"order_no,omitempty"`
	RepUserID    string    `json:"rep_user_id"`
	RepName      string    `json:"rep_name,omitempty"`
	Source       string    `json:"source"`
	AssignedBy   string    `json:"assigned_by,omitempty"`
	AssignedAt   time.Time `json:"assigned_at"`
	Note         string    `json:"note,omitempty"`
}

// IsOrderOverride reports whether this row is the per-order override rather than the customer default.
func (a RepAssignment) IsOrderOverride() bool { return a.OrderID != "" }

// CustomerInbound is one inbound customer message presented for routing. CustomerID is required (a
// message with no identifiable customer is not routable and is rejected). OrderID is optional and is
// the ONLY thing that can activate the per-order override. BranchID selects the house queue when
// there is no assignment; an empty BranchID falls to the global floor. EventKey makes the resulting
// post idempotent across redelivery, exactly like an FR-3 event.
type CustomerInbound struct {
	CustomerID   string `json:"customer_id"`
	CustomerName string `json:"customer_name,omitempty"`
	BranchID     string `json:"branch_id,omitempty"`
	OrderID      string `json:"order_id,omitempty"`
	Body         string `json:"body"`
	EventKey     string `json:"event_key,omitempty"`
	Channel      string `json:"channel,omitempty"` // origin: PORTAL | SMS | EMAIL (display only)
}

// RouteResult is what happened to one inbound: which rule answered (Level), where it landed (Kind +
// channel), and the message that was written. Inserted is false for an idempotent redelivery.
type RouteResult struct {
	Kind           string  `json:"kind"`  // REP_DM | HOUSE
	Level          string  `json:"level"` // ORDER | CUSTOMER | NONE
	ChannelID      string  `json:"channel_id"`
	ChannelSlug    string  `json:"channel_slug,omitempty"`
	RepUserID      string  `json:"rep_user_id,omitempty"`
	ConversationID string  `json:"conversation_id,omitempty"` // house queue only
	Message        Message `json:"message"`
	Inserted       bool    `json:"inserted"`
}

// HouseConversation is one row of the house queue read model (msg_house_conversations), who is
// waiting, since when, and whether someone has claimed them. ClaimedBy empty means still open.
type HouseConversation struct {
	ID            string     `json:"id"`
	CustomerID    string     `json:"customer_id"`
	CustomerName  string     `json:"customer_name,omitempty"`
	BranchID      string     `json:"branch_id,omitempty"`
	ChannelID     string     `json:"channel_id"`
	FirstAt       time.Time  `json:"first_at"`
	LastAt        time.Time  `json:"last_at"`
	MessageCount  int        `json:"message_count"`
	ClaimedBy     string     `json:"claimed_by,omitempty"`
	ClaimedByName string     `json:"claimed_by_name,omitempty"`
	ClaimedAt     *time.Time `json:"claimed_at,omitempty"`
}

// IsOpen reports whether the conversation is still unclaimed (the queue's working set).
func (c HouseConversation) IsOpen() bool { return c.ClaimedBy == "" }

// - errors surfaced to the handler -
var (
	// ErrNoCustomer is an inbound with no customer id, not routable, a client fault (400).
	ErrNoCustomer = errors.New("messaging: inbound customer message has no customer_id")
	// ErrAlreadyClaimed is a claim on a conversation someone else already claimed, a genuine race, not
	// a bug, so it maps to HTTP 409 and the UI re-reads the queue.
	ErrAlreadyClaimed = errors.New("messaging: conversation already claimed")
	// ErrBadAssignSource is an assignment write with a source outside the CHECK set (400).
	ErrBadAssignSource = errors.New("messaging: unknown assignment source")
)

// resolveAssignment is THE expression of the DIB-273/DIB-333 ruling, the single place the resolution
// order lives, and therefore the single place to edit if the ruling is ever reversed (see the 0136
// header: reversing it needs no migration because both row shapes are already legal).
//
// Given every rep_assignments row for one customer and the order (if any) the inbound names, it picks
// the effective assignment:
//
//  1. the override row whose order_id == orderID          → LevelOrder
//  2. the customer-level row (order_id IS NULL)           → LevelCustomer
//  3. nothing                                             → LevelNone (house queue)
//
// An override for a DIFFERENT order is ignored, it belongs to a different job on the same account,
// which is exactly what "split books" means. Pure + total, so the whole matrix is unit-tested DB-free.
func resolveAssignment(rows []RepAssignment, orderID string) (RepAssignment, string) {
	var customerLevel RepAssignment
	var haveCustomerLevel bool
	for _, a := range rows {
		if a.IsOrderOverride() {
			// Only an override for THIS order can win; another order's override is not our business.
			if orderID != "" && a.OrderID == orderID {
				return a, LevelOrder
			}
			continue
		}
		if !haveCustomerLevel {
			customerLevel, haveCustomerLevel = a, true
		}
	}
	if haveCustomerLevel {
		return customerLevel, LevelCustomer
	}
	return RepAssignment{}, LevelNone
}

// normalizeAssignSource defaults an empty source to MANUAL and validates it against the
// rep_assignments CHECK set, so a bad client value is a 400 at the service boundary rather than a
// Postgres CHECK violation → 500 (mirrors normalizeAckMethod).
func normalizeAssignSource(source string) (string, bool) {
	switch source {
	case "":
		return AssignManual, true
	case AssignManual, AssignClaim, AssignImport:
		return source, true
	default:
		return "", false
	}
}

// houseSlugUnassigned is the GLOBAL last-resort house queue (0136): an inbound whose customer has no
// home branch still lands somewhere a human reads, rather than being dropped.
const houseSlugUnassigned = "house-unassigned"

// repDMSlug is the STABLE, PER-REP-UNIQUE handle for a rep's customer-message DM channel. It carries
// the userID for the same reason approvalChannelSlug does (0075): slug is CITEXT-UNIQUE on
// msg_channels, so building it from a display name alone would collapse two reps who share a name -
// or whose names sanitize equal, into ONE channel, leaking each rep's customers to the other. The
// human handle stays cosmetic (it lives in the channel NAME); the userID suffix guarantees exactly one
// channel per rep.
func repDMSlug(handle, userID string) string {
	id := sanitizeSlugPart(userID)
	if h := sanitizeSlugPart(handle); h != "" {
		return "rep-" + h + "-" + id
	}
	return "rep-" + id
}

// renderCustomerMessage builds the EVENT body + ref for one inbound customer message. Pure (no
// ctx/DB) so the rendering is unit-tested in isolation. The body is prefixed with the customer so a
// rep reading their DM, which mixes many customers, always knows who is talking.
func renderCustomerMessage(in CustomerInbound) (string, EventRef) {
	who := in.CustomerName
	if who == "" {
		who = in.CustomerID
	}
	body := who
	if in.Channel != "" {
		body += " (" + in.Channel + ")"
	}
	body += ": " + in.Body
	ref := EventRef{
		Topic:   TopicCustomerMessage,
		Entity:  "customer:" + in.CustomerID,
		Summary: body,
	}
	if in.OrderID != "" {
		ref.Entity = "order:" + in.OrderID
	}
	return body, ref
}

// TopicCustomerMessage is the EVENT topic an inbound customer message is posted under. It is NOT in
// MessagingTopics: nothing on the outbox emits it, it is produced by the inbound bridge/endpoint
// calling RouteCustomerMessage, so registering it on the dispatcher would create an owner with no
// producer.
const TopicCustomerMessage = "msg.event.customer.message"
