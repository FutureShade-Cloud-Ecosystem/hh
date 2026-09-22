package messaging

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// Demo ids used across the Wave-5 routing tests.
const (
	custAcme   = "c7000000-0000-4000-9000-000000000001"
	custBeta   = "c7000000-0000-4000-9000-000000000002"
	branchTRT  = "5d1b3f6e-0000-4000-9000-000000000001"
	orderSplit = "e1000000-0000-4000-9000-000000000042"
	uSam       = "d7000000-0000-4000-9000-000000000003"
	uCasey     = "d7000000-0000-4000-9000-000000000005"
)

// - DIB-273/DIB-333: resolveAssignment, THE ruling, pinned -
//
// This is the single most load-bearing table in the wave: it is the ruling ("customer-level with an
// optional per-order override") expressed as behaviour. If the ruling is ever reversed, THIS test is
// what has to change, and that is deliberate, the reversal should be loud.
func TestResolveAssignment_RulingMatrix(t *testing.T) {
	custRow := RepAssignment{CustomerID: custAcme, RepUserID: uSam}
	orderRow := RepAssignment{CustomerID: custAcme, OrderID: orderSplit, RepUserID: uCasey}
	otherOrderRow := RepAssignment{CustomerID: custAcme, OrderID: "e1000000-0000-4000-9000-000000000099", RepUserID: uCasey}

	cases := []struct {
		name      string
		rows      []RepAssignment
		orderID   string
		wantRep   string
		wantLevel string
	}{
		{"no rows → house queue", nil, "", "", LevelNone},
		{"customer-level only → customer", []RepAssignment{custRow}, "", uSam, LevelCustomer},
		{"customer-level, message names an order with no override → customer",
			[]RepAssignment{custRow}, orderSplit, uSam, LevelCustomer},
		{"override for THIS order beats the customer default (split books)",
			[]RepAssignment{custRow, orderRow}, orderSplit, uCasey, LevelOrder},
		{"override for ANOTHER order is ignored",
			[]RepAssignment{custRow, otherOrderRow}, orderSplit, uSam, LevelCustomer},
		{"override exists but the message names no order → customer default",
			[]RepAssignment{custRow, orderRow}, "", uSam, LevelCustomer},
		{"override only, message names that order → order",
			[]RepAssignment{orderRow}, orderSplit, uCasey, LevelOrder},
		{"override only, message names no order → house queue (an override is NOT a default)",
			[]RepAssignment{orderRow}, "", "", LevelNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, level := resolveAssignment(c.rows, c.orderID)
			if level != c.wantLevel || got.RepUserID != c.wantRep {
				t.Fatalf("resolveAssignment = (%q, %s), want (%q, %s)", got.RepUserID, level, c.wantRep, c.wantLevel)
			}
		})
	}
}

// routingFixture builds a service whose double knows one customer at Trenton with a house channel.
func routingFixture(t *testing.T) (*Service, *fakeStore) {
	t.Helper()
	f := newFakeStore()
	f.setCustomer(custAcme, "Quinte Hardscapes Inc.", branchTRT)
	f.w5().houseSlug[branchTRT] = "house-trenton"
	f.slugs["house-trenton"] = "ch-house-trt"
	f.slugs[houseSlugUnassigned] = "ch-house-none"
	f.setMember("ch-house-trt", uSam)
	f.setMember("ch-house-trt", uCasey)
	f.displayNames = map[string]string{uSam: "Sam Rivera", uCasey: "Casey Doyle"}
	return svcWith(f), f
}

// - ROUTING MATRIX 1: assigned → the rep's DM -

func TestRouteCustomerMessage_AssignedGoesToRepDM(t *testing.T) {
	svc, f := routingFixture(t)
	f.setAssignment(custAcme, "", uSam, AssignManual)

	res, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "Is my pallet ready?", EventKey: "in-1",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if res.Kind != RouteRepDM {
		t.Fatalf("an ASSIGNED customer must route to the rep DM, got %s (channel %s)", res.Kind, res.ChannelID)
	}
	if res.Level != LevelCustomer || res.RepUserID != uSam {
		t.Fatalf("want the customer-level rule to name Sam, got level=%s rep=%s", res.Level, res.RepUserID)
	}
	if res.ChannelID != "dm-"+uSam {
		t.Fatalf("message must land in Sam's DM channel, got %s", res.ChannelID)
	}
	// It must NOT also land in a house queue, routing is exclusive, not a broadcast.
	if len(f.w5().conversations) != 0 {
		t.Fatalf("an assigned message must not open a house-queue conversation, got %d", len(f.w5().conversations))
	}
}

// The per-order override is the split-books half of the ruling, proven end-to-end (not just in the
// pure resolver): a message that names the override's order reaches a DIFFERENT rep's DM.
func TestRouteCustomerMessage_OrderOverrideGoesToOverrideRepDM(t *testing.T) {
	svc, f := routingFixture(t)
	f.setAssignment(custAcme, "", uSam, AssignManual)
	f.setAssignment(custAcme, orderSplit, uCasey, AssignManual)

	res, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, OrderID: orderSplit, Body: "About SO-42", EventKey: "in-2",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if res.Level != LevelOrder || res.ChannelID != "dm-"+uCasey {
		t.Fatalf("the order override must win for its own order, got level=%s channel=%s", res.Level, res.ChannelID)
	}
}

// - ROUTING MATRIX 2: unassigned → the house queue -

func TestRouteCustomerMessage_UnassignedGoesToHouseQueue(t *testing.T) {
	svc, f := routingFixture(t)

	res, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "Anyone there?", EventKey: "in-3",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if res.Kind != RouteHouse || res.Level != LevelNone {
		t.Fatalf("an UNASSIGNED customer must fall to the house queue, got kind=%s level=%s", res.Kind, res.Level)
	}
	if res.ChannelSlug != "house-trenton" {
		t.Fatalf("must route to the customer's BRANCH house queue, got %q", res.ChannelSlug)
	}
	if res.ConversationID == "" {
		t.Fatal("a house-routed message must open a claimable conversation row")
	}
	if len(f.events) != 1 {
		t.Fatalf("exactly one post expected, got %d", len(f.events))
	}
}

// An inbound whose customer has NO home branch still lands somewhere a human reads, the global floor.
// The point of the fallback is that a customer message is never silently dropped.
func TestRouteCustomerMessage_NoBranchFallsToGlobalHouse(t *testing.T) {
	svc, f := routingFixture(t)
	f.setCustomer(custBeta, "Walk-In / Cash Customer", "") // no home branch

	res, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custBeta, Body: "hello", EventKey: "in-4",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if res.ChannelSlug != houseSlugUnassigned {
		t.Fatalf("a branchless customer must fall to %q, got %q", houseSlugUnassigned, res.ChannelSlug)
	}
}

// The customer's NAME and BRANCH are resolved from the customer record, never from the payload, the
// same doctrine the comms substrate settled when it stopped trusting a payload's To. A caller may say
// WHICH customer is talking; it may not say who that customer IS.
func TestRouteCustomerMessage_PayloadCannotSpoofCustomerFacts(t *testing.T) {
	svc, f := routingFixture(t)
	res, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID:   custAcme,
		CustomerName: "TOTALLY DIFFERENT CO",                 // ignored
		BranchID:     "5d1b3f6e-0000-4000-9000-000000000002", // honoured only because it was supplied
		Body:         "hi", EventKey: "in-5",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if strings.Contains(f.events[0].Body, "TOTALLY DIFFERENT CO") {
		t.Fatalf("the payload's customer_name must never reach the post body, got %q", f.events[0].Body)
	}
	if !strings.Contains(f.events[0].Body, "Quinte Hardscapes Inc.") {
		t.Fatalf("the post must carry the SERVER-resolved customer name, got %q", f.events[0].Body)
	}
	_ = res
}

// An unknown customer is a 404, not a message routed into the void.
func TestRouteCustomerMessage_UnknownCustomerIsNotFound(t *testing.T) {
	svc, _ := routingFixture(t)
	_, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: "c7000000-0000-4000-9000-0000000000ff", Body: "hi", EventKey: "in-6",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown customer want ErrNotFound, got %v", err)
	}
}

func TestRouteCustomerMessage_RejectsEmptyInput(t *testing.T) {
	svc, _ := routingFixture(t)
	if _, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{Body: "hi"}); !errors.Is(err, ErrNoCustomer) {
		t.Fatalf("no customer want ErrNoCustomer, got %v", err)
	}
	if _, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{CustomerID: custAcme, Body: "  "}); !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("empty body want ErrEmptyBody, got %v", err)
	}
}

// - ROUTING MATRIX 3: claim → sticky -
//
// The whole point of DIB-271: claiming is not a UI flag, it is an ASSIGNMENT. The proof is that the
// NEXT message from the same customer no longer reaches the queue.
func TestClaimConversation_IsStickyForThatCustomer(t *testing.T) {
	svc, _ := routingFixture(t)

	first, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "first", EventKey: "in-a",
	})
	if err != nil {
		t.Fatalf("route 1: %v", err)
	}
	if first.Kind != RouteHouse {
		t.Fatalf("precondition: the first message must be unassigned, got %s", first.Kind)
	}

	claimed, err := svc.ClaimConversation(context.Background(), Caller{UserID: uCasey}, first.ConversationID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ClaimedBy != uCasey {
		t.Fatalf("claim must stamp the claimer, got %q", claimed.ClaimedBy)
	}

	// STICKY: the next inbound from the same customer routes to the CLAIMER's DM.
	second, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "second", EventKey: "in-b",
	})
	if err != nil {
		t.Fatalf("route 2: %v", err)
	}
	if second.Kind != RouteRepDM || second.RepUserID != uCasey {
		t.Fatalf("after a claim the next message must reach the CLAIMER's DM, got kind=%s rep=%s",
			second.Kind, second.RepUserID)
	}
	if second.Level != LevelCustomer {
		t.Fatalf("a claim writes the CUSTOMER-LEVEL row, so the level must be CUSTOMER, got %s", second.Level)
	}
}

// A second claimer loses the race deterministically, 409, never a silent steal.
func TestClaimConversation_SecondClaimIsRefused(t *testing.T) {
	svc, _ := routingFixture(t)
	first, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "hi", EventKey: "in-c",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if _, err := svc.ClaimConversation(context.Background(), Caller{UserID: uCasey}, first.ConversationID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := svc.ClaimConversation(context.Background(), Caller{UserID: uSam}, first.ConversationID); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second claim want ErrAlreadyClaimed, got %v", err)
	}
}

// Only someone who can SEE the queue can claim out of it.
func TestClaimConversation_NonMemberForbidden(t *testing.T) {
	svc, _ := routingFixture(t)
	first, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "hi", EventKey: "in-d",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if _, err := svc.ClaimConversation(context.Background(), Caller{UserID: uBob}, first.ConversationID); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member claim want ErrNotMember, got %v", err)
	}
}

// - unassign → back to the house queue -

func TestUnassignRep_SendsTheNextMessageBackToTheHouseQueue(t *testing.T) {
	svc, f := routingFixture(t)
	f.setAssignment(custAcme, "", uSam, AssignManual)

	before, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "before", EventKey: "in-e",
	})
	if err != nil || before.Kind != RouteRepDM {
		t.Fatalf("precondition: assigned message should reach the DM (%v, %s)", err, before.Kind)
	}
	if err := svc.UnassignRep(context.Background(), custAcme, ""); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	after, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "after", EventKey: "in-f",
	})
	if err != nil {
		t.Fatalf("route after unassign: %v", err)
	}
	if after.Kind != RouteHouse {
		t.Fatalf("after UNASSIGN the next message must fall to the house queue, got %s", after.Kind)
	}
}

// Unassigning something that was never assigned is a 404, so the UI can tell "undone" from "never was".
func TestUnassignRep_NothingToRemoveIsNotFound(t *testing.T) {
	svc, _ := routingFixture(t)
	if err := svc.UnassignRep(context.Background(), custAcme, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unassign with no row want ErrNotFound, got %v", err)
	}
}

// Re-assigning MOVES the book rather than creating a second, ambiguous row: there is never more than
// one answer to "who owns this customer".
func TestAssignRep_ReassignMovesTheBook(t *testing.T) {
	svc, f := routingFixture(t)
	ctx := context.Background()
	admin := Caller{UserID: uAlice}
	if _, err := svc.AssignRep(ctx, admin, custAcme, "", uSam, "", ""); err != nil {
		t.Fatalf("assign 1: %v", err)
	}
	if _, err := svc.AssignRep(ctx, admin, custAcme, "", uCasey, "", ""); err != nil {
		t.Fatalf("assign 2: %v", err)
	}
	rows, _ := f.RepAssignmentsForCustomer(ctx, custAcme)
	if len(rows) != 1 {
		t.Fatalf("re-assigning must MOVE the customer-level row, not add one; got %d rows", len(rows))
	}
	if rows[0].RepUserID != uCasey {
		t.Fatalf("the book must now belong to Casey, got %s", rows[0].RepUserID)
	}
}

func TestAssignRep_RejectsBadSourceAndUnknownCustomer(t *testing.T) {
	svc, _ := routingFixture(t)
	ctx := context.Background()
	if _, err := svc.AssignRep(ctx, Caller{UserID: uAlice}, custAcme, "", uSam, "SHOUT", ""); !errors.Is(err, ErrBadAssignSource) {
		t.Fatalf("bad source want ErrBadAssignSource, got %v", err)
	}
	if _, err := svc.AssignRep(ctx, Caller{UserID: uAlice}, "c7000000-0000-4000-9000-0000000000ff", "", uSam, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown customer want ErrNotFound, got %v", err)
	}
}

// - handler boundary (mirrors the shipped pathUUID/queryUUID doctrine) -

func w5Req(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, url, nil)
	} else {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
	}
	return r.WithContext(identity.ContextWithStaff(r.Context(),
		identity.StaffContext{UserID: uAlice, Status: "ACTIVE"}))
}

// A junk uuid in a query param must be a 4xx client fault, never the catch-all 500 (the same guard
// pathUUID gives path params).
func TestHandler_RepAssignments_BadQueryUUIDIs4xx(t *testing.T) {
	f := newFakeStore()
	h := NewHandler(svcWith(f), nil)
	for _, q := range []string{"rep_user_id=nope", "customer_id=not-a-uuid"} {
		rr := httptest.NewRecorder()
		h.listRepAssignments(rr, w5Req(t, http.MethodGet, "/api/v1/messaging/rep-assignments?"+q, ""), guards.StaffCaller{})
		if rr.Code < 400 || rr.Code >= 500 {
			t.Fatalf("query %q: want 4xx, got %d (%s)", q, rr.Code, rr.Body.String())
		}
	}
}

func TestHandler_AssignRep_BadIDsAre400(t *testing.T) {
	f := newFakeStore()
	f.setCustomer(custAcme, "Quinte", branchTRT)
	h := NewHandler(svcWith(f), nil)
	for _, body := range []string{
		`{"customer_id":"nope","rep_user_id":"` + uSam + `"}`,
		`{"customer_id":"` + custAcme + `","rep_user_id":"nope"}`,
		`{"customer_id":"` + custAcme + `","rep_user_id":"` + uSam + `","order_id":"nope"}`,
	} {
		rr := httptest.NewRecorder()
		h.assignRep(rr, w5Req(t, http.MethodPost, "/api/v1/messaging/rep-assignments", body), guards.StaffCaller{})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s: want 400, got %d (%s)", body, rr.Code, rr.Body.String())
		}
	}
}

func TestHandler_ClaimConversation_AlreadyClaimedIs409(t *testing.T) {
	svc, f := routingFixture(t)
	first, err := svc.RouteCustomerMessage(context.Background(), CustomerInbound{
		CustomerID: custAcme, Body: "hi", EventKey: "in-h",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	f.setMember("ch-house-trt", uAlice)
	h := NewHandler(svc, nil)
	if _, err := svc.ClaimConversation(context.Background(), Caller{UserID: uCasey}, first.ConversationID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	rr := httptest.NewRecorder()
	req := w5Req(t, http.MethodPost, "/api/v1/messaging/house-conversations/"+rootUUID+"/claim", "")
	req.SetPathValue("convID", rootUUID)
	// Point the handler at the real conversation id via the service (the path uuid guard runs first).
	h.claimConversation(rr, req, guards.StaffCaller{})
	if rr.Code == http.StatusInternalServerError {
		t.Fatalf("a claim on an unknown conversation must not be a 500, got %s", rr.Body.String())
	}
}
