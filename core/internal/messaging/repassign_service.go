package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// - DIB-273/DIB-333 + DIB-271 service: assignment, routing, claim -

// repStore is the persistence seam for rep assignment + the house queue, declared HERE (in the
// consumer package) rather than imported from a peer, the house consumer-declared-seam rule. It is
// satisfied by *Repository and by the unit-test fake, so the whole routing matrix is provable DB-free.
type repStore interface {
	RepAssignmentsForCustomer(ctx context.Context, customerID string) ([]RepAssignment, error)
	ListRepAssignments(ctx context.Context, repUserID, customerID string, scope RepListScope) ([]RepAssignment, error)
	UpsertRepAssignment(ctx context.Context, a RepAssignment) (RepAssignment, error)
	DeleteRepAssignment(ctx context.Context, customerID, orderID string) (int, error)
	EnsureRepDMChannel(ctx context.Context, userID string) (string, error)
	BranchHouseSlug(ctx context.Context, branchID string) (string, error)
	CustomerFacts(ctx context.Context, customerID string) (name, branchID string, err error)
	UpsertHouseConversation(ctx context.Context, customerID, branchID, channelID string) (string, error)
	ListHouseConversations(ctx context.Context, channelID string, openOnly bool) ([]HouseConversation, error)
	GetHouseConversation(ctx context.Context, id string) (HouseConversation, error)
	ClaimHouseConversation(ctx context.Context, conversationID, userID string) (HouseConversation, error)
}

// reps returns the rep-assignment store, or an error when the service was built without one (the
// DB-free scaffold path). Discovered by type assertion off the existing store seam so the base `store`
// interface, and every fake that implements it, stays unchanged.
func (s *Service) reps() (repStore, error) {
	if rs, ok := s.repo.(repStore); ok {
		return rs, nil
	}
	return nil, errNoDB
}

// ResolveRep answers "who owns this conversation?" for one customer (and optionally one order),
// applying the DIB-273/DIB-333 ruling via resolveAssignment. It returns the winning assignment and
// the LEVEL that won (ORDER | CUSTOMER | NONE) so a caller, and a test, sees not just who, but why.
func (s *Service) ResolveRep(ctx context.Context, customerID, orderID string) (RepAssignment, string, error) {
	rs, err := s.reps()
	if err != nil {
		return RepAssignment{}, LevelNone, err
	}
	rows, err := rs.RepAssignmentsForCustomer(ctx, customerID)
	if err != nil {
		return RepAssignment{}, LevelNone, err
	}
	a, level := resolveAssignment(rows, orderID)
	return a, level, nil
}

// RouteCustomerMessage is the DIB-271 + DIB-273 routing decision, end to end:
//
//	assigned  → the rep's DM (EnsureRepDMChannel, provisioned on demand)
//	unassigned→ #house-<branch> (or the #house-unassigned floor), plus a queue row to claim
//
// The message itself is written as a system EVENT (author NULL) so it can never be mistaken for a
// staff post and so it carries the same idempotency key discipline as FR-3: a redelivered inbound with
// the same EventKey is a silent no-op, not a duplicate in someone's inbox.
//
// The customer's display name and home branch are resolved from the CUSTOMER RECORD, never from the
// caller's payload, the same doctrine the W2/W3 comms substrate settled when it stopped trusting a
// payload's To. A caller can say WHICH customer is talking; it cannot say who that customer IS or
// which branch they belong to.
func (s *Service) RouteCustomerMessage(ctx context.Context, in CustomerInbound) (RouteResult, error) {
	rs, err := s.reps()
	if err != nil {
		return RouteResult{}, err
	}
	if strings.TrimSpace(in.CustomerID) == "" {
		return RouteResult{}, ErrNoCustomer
	}
	if strings.TrimSpace(in.Body) == "" {
		return RouteResult{}, ErrEmptyBody
	}

	// Server-resolved customer facts (never the payload's).
	name, homeBranch, err := rs.CustomerFacts(ctx, in.CustomerID)
	if err != nil {
		return RouteResult{}, err
	}
	in.CustomerName = name
	if in.BranchID == "" {
		in.BranchID = homeBranch
	}
	if in.EventKey == "" {
		// No producer key: fall back to a per-customer content-free key so at worst a duplicate POST
		// creates a second message (the honest outcome) rather than silently colliding with an unrelated
		// one. Keys are namespaced per customer so two customers can never share one.
		in.EventKey = "cust-msg:" + in.CustomerID + ":" + fmt.Sprint(nowUnixNano())
	}

	body, ref := renderCustomerMessage(in)
	assignment, level, err := s.ResolveRep(ctx, in.CustomerID, in.OrderID)
	if err != nil {
		return RouteResult{}, err
	}

	if level != LevelNone {
		channelID, err := rs.EnsureRepDMChannel(ctx, assignment.RepUserID)
		if err != nil {
			return RouteResult{}, fmt.Errorf("ensure rep dm: %w", err)
		}
		m, inserted, err := s.postEventToChannelID(ctx, channelID, in.EventKey, body, ref, EventOpts{})
		if err != nil {
			return RouteResult{}, err
		}
		return RouteResult{
			Kind: RouteRepDM, Level: level, ChannelID: channelID,
			RepUserID: assignment.RepUserID, Message: m, Inserted: inserted,
		}, nil
	}

	// DIB-271: no assignment → the house queue.
	slug, err := rs.BranchHouseSlug(ctx, in.BranchID)
	if err != nil {
		return RouteResult{}, err
	}
	m, inserted, err := s.PostEventToChannelSlug(ctx, slug, in.EventKey, body, ref, false)
	if err != nil {
		return RouteResult{}, err
	}
	convID, err := rs.UpsertHouseConversation(ctx, in.CustomerID, in.BranchID, m.ChannelID)
	if err != nil {
		return RouteResult{}, err
	}
	return RouteResult{
		Kind: RouteHouse, Level: LevelNone, ChannelID: m.ChannelID, ChannelSlug: slug,
		ConversationID: convID, Message: m, Inserted: inserted,
	}, nil
}

// AssignRep sets (or moves) a rep assignment. orderID empty writes the CUSTOMER-LEVEL row, the
// routable default; orderID set writes the PER-ORDER override for split books. Authorization is the
// ADMIN gate at the route (mirroring the export surface): who owns which customer is a book-of-
// business decision, not a self-service one.
func (s *Service) AssignRep(ctx context.Context, c Caller, customerID, orderID, repUserID, source, note string) (RepAssignment, error) {
	rs, err := s.reps()
	if err != nil {
		return RepAssignment{}, err
	}
	if strings.TrimSpace(customerID) == "" || strings.TrimSpace(repUserID) == "" {
		return RepAssignment{}, ErrNoCustomer
	}
	src, ok := normalizeAssignSource(source)
	if !ok {
		return RepAssignment{}, ErrBadAssignSource
	}
	// Prove the customer exists before writing: a typo'd id would otherwise become an FK error → 500.
	if _, _, err := rs.CustomerFacts(ctx, customerID); err != nil {
		return RepAssignment{}, err
	}
	return rs.UpsertRepAssignment(ctx, RepAssignment{
		CustomerID: customerID, OrderID: orderID, RepUserID: repUserID,
		Source: src, AssignedBy: c.UserID, Note: note,
	})
}

// UnassignRep removes an assignment. Removing the CUSTOMER-LEVEL row is the "unassign → house queue"
// path: with no row left, resolveAssignment yields LevelNone and the next inbound lands in the queue.
// Returns ErrNotFound when there was nothing to remove, so the UI can tell "undone" from "never was".
func (s *Service) UnassignRep(ctx context.Context, customerID, orderID string) error {
	rs, err := s.reps()
	if err != nil {
		return err
	}
	n, err := rs.DeleteRepAssignment(ctx, customerID, orderID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRepAssignments returns the admin list (both filters optional).
//
// The list is held to the customer read's rule (UX-89 round 3 P2-B): an account homed at a branch outside the
// caller's reach is left out, an account with no home is shared. One arm stays open: a rep lists their own
// book across branches, but only when repUserID is the caller's own linked users.id (scope.OwnBook, set by
// the handler from the stamped caller, never from a value the request chooses).
func (s *Service) ListRepAssignments(ctx context.Context, repUserID, customerID string, scope RepListScope) ([]RepAssignment, error) {
	rs, err := s.reps()
	if err != nil {
		return nil, err
	}
	out, err := rs.ListRepAssignments(ctx, repUserID, customerID, scope)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []RepAssignment{}
	}
	return out, nil
}

// ListHouseQueue returns one house channel's queue, requiring channel membership, the house queue is
// a channel, and a caller who cannot read the channel cannot read its queue either.
func (s *Service) ListHouseQueue(ctx context.Context, c Caller, channelID string, openOnly bool) ([]HouseConversation, error) {
	rs, err := s.reps()
	if err != nil {
		return nil, err
	}
	if err := s.assertMember(ctx, c, channelID); err != nil {
		return nil, err
	}
	out, err := rs.ListHouseConversations(ctx, channelID, openOnly)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []HouseConversation{}
	}
	return out, nil
}

// ClaimConversation is the DIB-271 claim: it stamps the queue row AND writes the customer-level
// assignment in one transaction, so the claim is STICKY, the next inbound from that customer routes
// to the claimer's DM rather than back to the queue.
//
// Membership in the house channel is required: only someone who can SEE the queue can claim out of it.
// A second claimer gets ErrAlreadyClaimed (409), never a silent steal.
func (s *Service) ClaimConversation(ctx context.Context, c Caller, conversationID string) (HouseConversation, error) {
	rs, err := s.reps()
	if err != nil {
		return HouseConversation{}, err
	}
	conv, err := rs.GetHouseConversation(ctx, conversationID)
	if err != nil {
		return HouseConversation{}, err
	}
	if err := s.assertMember(ctx, c, conv.ChannelID); err != nil {
		return HouseConversation{}, err
	}
	claimed, err := rs.ClaimHouseConversation(ctx, conversationID, c.UserID)
	if err != nil {
		return HouseConversation{}, err
	}
	// Leave a visible trace in the queue channel so the rest of the branch sees it is handled. A failure
	// here must not undo a committed claim, the claim is the truth; the notice is courtesy.
	who := claimed.ClaimedByName
	if who == "" {
		who = "a rep"
	}
	notice := fmt.Sprintf("%s claimed the conversation with %s.", who, displayOr(claimed.CustomerName, claimed.CustomerID))
	if _, _, err := s.postEventToChannelID(ctx, conv.ChannelID, "claim:"+conversationID,
		notice, EventRef{Topic: "msg.event.conversation.claimed", Entity: "customer:" + claimed.CustomerID, Summary: notice},
		EventOpts{}); err != nil && !errors.Is(err, errNoDB) {
		return claimed, nil
	}
	return claimed, nil
}

// displayOr returns the first non-empty of the two (a name, else the raw id).
func displayOr(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return id
}

// RepListScope is the reach a rep assignment list is read under: Global callers are unbounded, others see the
// accounts homed in BranchIDs or homed nowhere, and OwnBook lifts the home test for the caller's own assignments.
type RepListScope struct {
	Global    bool
	BranchIDs []string
	OwnBook   bool
}
