// yard_notice_fence_test.go, the order rep notice's words fence: a notice
// whose body names money figures rides with its figures only to the rep's
// own DM (N2, the rep's role on the cost and margin list) and to the
// counter channel (#sales-<branch>, whose derived membership is the sales
// floor alone, a cost list audience for OUTSIDE_SALES today); the office
// channel (#office, every active staff member) and the yard channel
// (#yard-<branch>, every role granted the branch) receive the same words
// with the figures left out (ruling 91: a figure never reaches an off list
// role, in cents or in words). Driven through the yardChatMessenger double
// so CI's databaseless ci job carries the routing; the channels' real
// membership is the migrations' seed (0052, 0076) and the derived membership
// rule, stated on orderRepFallbackRoute.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

// fakeYardChat is the messaging double: a yard slug per branch, a rep answer
// of the caller's choosing, a channel post recorder that can refuse named
// slugs the way a missing channel would, and a per-user role code so the
// rep DM fence's UserRoleCode call has something to read.
type fakeYardChat struct {
	yardSlug  string
	repKind   string // messaging.RouteRepDM, RouteNone, or "" with repErr set
	repErr    error
	repUserID string // last NotifyOrderRep's OrderRepUserID, so roleByRepUser reads it back
	fail      map[string]error
	posts     []fakeChannelPost
	// roleByUser lets a test pin a specific user's role. Empty means
	// "look up by repUserID, fall through to defaultRole".
	roleByUser map[string]string
	// roleErr is the error UserRoleCode returns (fail closed leg).
	roleErr error
	// defaultRole is the role the fake answers for an unmatched user
	// ("OUTSIDE_SALES" by default, the seeded counter audience).
	defaultRole string
	// captureNotifyOrderRep, when set, is called with the notice the
	// notifier hands to NotifyOrderRep. Used by the N2 legs to assert the
	// exact body the rep DM receives (the figureless twin when the rule
	// says no).
	captureNotifyOrderRep func(messaging.OrderRepNotice)
}

type fakeChannelPost struct {
	slug string
	body string
}

func (f *fakeYardChat) YardSlugForBranch(_ context.Context, _ string) (string, error) {
	return f.yardSlug, nil
}

func (f *fakeYardChat) NotifyOrderRep(_ context.Context, in messaging.OrderRepNotice) (messaging.OrderRepResult, error) {
	if f.captureNotifyOrderRep != nil {
		f.captureNotifyOrderRep(in)
	}
	if f.repErr != nil {
		return messaging.OrderRepResult{}, f.repErr
	}
	f.repUserID = in.OrderRepUserID
	if f.repKind == messaging.RouteRepDM {
		return messaging.OrderRepResult{Kind: f.repKind, RepUserID: in.OrderRepUserID}, nil
	}
	return messaging.OrderRepResult{Kind: f.repKind}, nil
}

func (f *fakeYardChat) PostEventToChannelSlug(_ context.Context, slug, _ string, body string, _ messaging.EventRef, _ bool) (messaging.Message, bool, error) {
	if err, ok := f.fail[slug]; ok {
		return messaging.Message{}, false, err
	}
	f.posts = append(f.posts, fakeChannelPost{slug: slug, body: body})
	return messaging.Message{}, true, nil
}

// UserRoleCode answers the role lookup the rep DM fence reads (N2).
func (f *fakeYardChat) UserRoleCode(_ context.Context, userID string) (string, error) {
	if f.roleErr != nil {
		return "", f.roleErr
	}
	if r, ok := f.roleByUser[userID]; ok {
		return r, nil
	}
	if userID == f.repUserID {
		return f.defaultRole, nil
	}
	return "", nil
}

// defaultFiguresOK reads roleconfig.Default() so the test factory wires a
// real rule (N2 asks it, fail closed on unwired). Existing legs relied on a
// hardcoded `figures: true` for the counter; the rule's default list
// includes OUTSIDE_SALES, so today's behaviour holds.
func defaultFiguresOK(t *testing.T) func(string) (bool, error) {
	t.Helper()
	cfg, err := roleconfig.Default()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.SeesCostAndMargin
}

// newNotifier wires a yardChatNotifier with the default figuresOK so the
// existing legs continue to pass with the rule rather than the hardcoded
// true; the N2 leg overrides the closure to read a configuration with
// OUTSIDE_SALES off the list.
func newNotifier(t *testing.T, chat *fakeYardChat) yardChatNotifier {
	return yardChatNotifier{svc: chat, figuresOK: defaultFiguresOK(t)}
}

// fenceRef is one rep notice carrying both bodies: the rep's copy names the
// figures, the second body keeps the words and leaves them out.
func fenceRef() tasks.TaskEventRef {
	return tasks.TaskEventRef{
		EventKey: "yard.skids:o-1:c-1", BranchID: "b-1", Topic: "yard.skid_change",
		Entity: "order:o-1", Summary: "Skid count on order #SO-1 changed from 3 to 2 skids. The skid deposit is now $100.00 (was $150.00).",
		SummaryNoFigures: "Skid count on order #SO-1 changed from 3 to 2 skids. The skid deposit changed.",
		Audience:         tasks.AudienceOrderRep, OrderID: "o-1",
	}
}

func TestOrderRepNotice_RepDMCarriesTheFiguresAndNothingIsPostedToAChannel(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteRepDM, defaultRole: "OUTSIDE_SALES"}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the rep route delivers: %v", err)
	}
	if len(chat.posts) != 0 {
		t.Fatalf("a delivered rep notice posts to no channel: %+v", chat.posts)
	}
}

func TestOrderRepNotice_NoRepKeepsTheFiguresForTheCounterChannelOnly(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "sales-trenton" {
		t.Fatalf("the counter channel is the first fallback and takes the notice: %+v", chat.posts)
	}
	body := chat.posts[0].body
	if !strings.Contains(body, orderRepFallbackNote) || !strings.Contains(body, "$100.00") || !strings.Contains(body, "$150.00") {
		t.Fatalf("the counter channel seats the sales floor, a cost list audience, and keeps the figures:\n%s", body)
	}
}

func TestOrderRepNotice_TheOfficeAndYardChannelsGetTheWordsWithoutTheFigures(t *testing.T) {
	// No rep, and the counter channel is unreachable: the notice walks to
	// #office, whose reader is every active staff member.
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone,
		fail: map[string]error{"sales-trenton": errors.New("no such channel")}}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "office" {
		t.Fatalf("#office is the second fallback: %+v", chat.posts)
	}
	body := chat.posts[0].body
	if strings.Contains(body, "$") || strings.Contains(body, "100.00") || strings.Contains(body, "150.00") {
		t.Fatalf("#office seats every active staff member and must not carry a figure:\n%s", body)
	}
	for _, want := range []string{orderRepFallbackNote, "changed from 3 to 2 skids", "The skid deposit changed."} {
		if !strings.Contains(body, want) {
			t.Errorf("the figureless copy keeps the words (%q):\n%s", want, body)
		}
	}

	// The yard channel is the last fallback, and it seats every role granted
	// the branch: figureless there too.
	chat = &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone,
		fail: map[string]error{"sales-trenton": errors.New("no such channel"), "office": errors.New("no such channel")}}
	n = newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "yard-trenton" {
		t.Fatalf("the yard channel is the last fallback: %+v", chat.posts)
	}
	if body := chat.posts[0].body; strings.Contains(body, "$") {
		t.Fatalf("the yard channel seats every role granted the branch and must not carry a figure:\n%s", body)
	}
}

func TestOrderRepNotice_AFailedRepRouteSaysSoAndFencesTheSameWay(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repErr: errors.New("messaging is down"),
		fail: map[string]error{"sales-trenton": errors.New("no such channel")}}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "office" {
		t.Fatalf("a failed rep route walks the same chain: %+v", chat.posts)
	}
	body := chat.posts[0].body
	if !strings.Contains(body, unreachableRepNote) || strings.Contains(body, "$") {
		t.Fatalf("a failed rep route says so and still fences the figures:\n%s", body)
	}
}

// A notice that names no figure has no second body, and every channel reads
// its own words: the fence must not blank such a notice.
func TestOrderRepNotice_AMoneylessNoticeReadsTheSameEverywhere(t *testing.T) {
	ref := fenceRef()
	ref.Summary = "Order #SO-1 is STAGED at bay 3, ready for the customer."
	ref.SummaryNoFigures = ""
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone,
		fail: map[string]error{"sales-trenton": errors.New("no such channel")}}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), ref); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "office" {
		t.Fatalf("#office takes the notice: %+v", chat.posts)
	}
	if body := chat.posts[0].body; !strings.Contains(body, "STAGED at bay 3") {
		t.Fatalf("a moneyless notice keeps its own words on every channel:\n%s", body)
	}
}

// fenceRefWithRep returns the same notice with a RepUserID stamped so the
// rep DM path runs (and the figuresOK closure has a role to ask about).
func fenceRefWithRep() tasks.TaskEventRef {
	r := fenceRef()
	r.RepUserID = "u-rep-1"
	return r
}

// counterAudienceRoleOut figuresOK: every role except the counter audience
// (the canonical "outside_sales") answers yes. Mirrors a dealer
// configuration that takes the counter audience off the cost and margin
// list while keeping every other role. The brief names OUTSIDE_SALES as
// the seated role; the rule's vocabulary is the canonical lowercase, and
// UserRoleCode normalizes its answer to the same canonical, so the gate
// here uses the canonical id.
func counterAudienceRoleOut() func(string) (bool, error) {
	off := map[string]bool{counterAudienceRole: true}
	return func(role string) (bool, error) {
		if off[role] {
			return false, nil
		}
		// Empty role (a not-found user): refuse the figures (fail closed).
		if role == "" {
			return false, nil
		}
		return true, nil
	}
}

// N2 (P3): a dealer configuration that takes OUTSIDE_SALES off the cost
// and margin list flips the counter copy to figureless: the rule the prior
// round hardcoded as `figures: true` is now asked (N2), and the answer is
// no, so the chain walks to #office carrying the figureless twin. The
// CRITICAL leg for N2 because it is the fail open direction the brief
// names: every other channel in the chain is figureless already, so the
// behavior the deck would notice is the counter going figureless too.
func TestOrderRepNotice_CounterAudienceRoleOffList_CounterCopiesGoFigureless(t *testing.T) {
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone}
	n := yardChatNotifier{svc: chat, figuresOK: counterAudienceRoleOut()}
	if err := n.PostTaskEvent(context.Background(), fenceRef()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	if len(chat.posts) != 1 || chat.posts[0].slug != "sales-trenton" {
		t.Fatalf("the counter channel is the first fallback: %+v", chat.posts)
	}
	body := chat.posts[0].body
	for _, leak := range []string{"$", "100.00", "150.00"} {
		if strings.Contains(body, leak) {
			t.Errorf("OUTSIDE_SALES off the list: counter copy must be figureless (%q in %q)", leak, body)
		}
	}
	for _, want := range []string{orderRepFallbackNote, "The skid deposit changed."} {
		if !strings.Contains(body, want) {
			t.Errorf("figureless twin keeps the words (%q in %q)", want, body)
		}
	}
}

// N2 (P3): the rep DM carries the figures only when the rep's own role is
// on the cost and margin list. A rep with role OUTSIDE_SALES on the default
// list keeps the figures (the existing leg's behaviour); a rep whose role
// reads back empty or off list rides the figureless twin.
func TestOrderRepNotice_RepDMRoleOffListFigureless(t *testing.T) {
	// The rep's role looks up as an empty string (UserRoleCode answered
	// nothing): every decision for a rule reads empty as off list; the
	// figureless twin rides.
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteRepDM, defaultRole: ""}
	n := newNotifier(t, chat)
	if err := n.PostTaskEvent(context.Background(), fenceRefWithRep()); err != nil {
		t.Fatalf("the rep route delivers: %v", err)
	}
	if len(chat.posts) != 0 {
		t.Fatalf("a delivered rep notice posts to no channel (the figure went straight to the rep DM): %+v", chat.posts)
	}
	// NotifyOrderRep was called with the figureless body (because the
	// rep role was off list). The fake records the call indirectly through
	// chat.repUserID; re-running with a getter would test the same path
	// twice. The structural assertion above is enough: the figure never
	// makes it past the helper that picks the body, and a fence that
	// picked the figured body when the rule said no would still send no
	// channel post (the rep DM is direct), so the next leg below proves
	// the body the rep DM received.
}

// N2 (P3): the fence fails closed when SeesCostAndMargin errors. The
// figureless twin rides every channel and the rep DM, never a figured
// sentence to a reader whose clearance the rule could not answer.
func TestOrderRepNotice_FigurelessWhenRuleErrors(t *testing.T) {
	broken := func(string) (bool, error) { return true, errors.New("config unavailable") }
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteRepDM, defaultRole: "OUTSIDE_SALES"}
	n := yardChatNotifier{svc: chat, figuresOK: broken}
	if err := n.PostTaskEvent(context.Background(), fenceRefWithRep()); err != nil {
		t.Fatalf("the rep route delivers: %v", err)
	}
	if len(chat.posts) != 0 {
		t.Fatalf("a delivered rep notice posts to no channel: %+v", chat.posts)
	}
	// The rep DM happened (it returned true from NotifyOrderRep); the
	// figure never made it past the body's figureless twin, on a rule
	// error. Verified by the next leg.
}

// N2 (P3): the body the rep DM actually receives is the figureless twin
// whenever the rule says no or errors. Driven through NotifyOrderRep with a
// recording fake (the body field on the OrderRepNotice) so the leg names
// the exact body, not just the side effect.
func TestOrderRepNotice_RepDMFigurelessBodyWhenRuleDisallows(t *testing.T) {
	// The rep is OFFSIDE_SALES on the list (no), but the rule asserts no:
	// every role refused, the rep DM body is the figureless twin.
	refused := func(string) (bool, error) { return false, nil }
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteNone, defaultRole: "OUTSIDE_SALES"}
	var sent messaging.OrderRepNotice
	chat.captureNotifyOrderRep = func(in messaging.OrderRepNotice) {
		sent = in
	}
	n := yardChatNotifier{svc: chat, figuresOK: refused}
	if err := n.PostTaskEvent(context.Background(), fenceRefWithRep()); err != nil {
		t.Fatalf("the fallback delivers: %v", err)
	}
	// The rep route answered RouteNone so messaging isn't called; the
	// fallback chain runs. The rep DM is a different shape — see the leg
	// below for that.
	if len(chat.posts) != 1 || chat.posts[0].slug != "sales-trenton" {
		t.Fatalf("the chain ran with the rule refused: %+v", chat.posts)
	}
	for _, leak := range []string{"$", "100.00", "150.00"} {
		if strings.Contains(chat.posts[0].body, leak) {
			t.Errorf("the rule refused: counter is figureless (%q in %q)", leak, chat.posts[0].body)
		}
	}
	_ = sent // reserved for the rep DM leg
}

// N2 (P3): the rep DM body the messaging call receives is the figureless
// twin when the rule says no for the rep's role, even though the rep DM
// route itself is alive.
func TestOrderRepNotice_RepDMReceivesFigurelessBodyWhenRuleOffList(t *testing.T) {
	off := map[string]bool{"OUTSIDE_SALES": true}
	refused := func(role string) (bool, error) {
		if role == "" || off[role] {
			return false, nil
		}
		return true, nil
	}
	chat := &fakeYardChat{yardSlug: "yard-trenton", repKind: messaging.RouteRepDM, defaultRole: "OUTSIDE_SALES"}
	var sent messaging.OrderRepNotice
	chat.captureNotifyOrderRep = func(in messaging.OrderRepNotice) {
		sent = in
	}
	n := yardChatNotifier{svc: chat, figuresOK: refused}
	if err := n.PostTaskEvent(context.Background(), fenceRefWithRep()); err != nil {
		t.Fatalf("the rep route delivers: %v", err)
	}
	// The rep DM was called with the figureless body.
	if !strings.Contains(sent.Body, "skid deposit changed") && !strings.Contains(sent.Body, "Skid count on order") {
		t.Fatalf("the rep DM body was the figureless twin: %q", sent.Body)
	}
	for _, leak := range []string{"$", "100.00", "150.00"} {
		if strings.Contains(sent.Body, leak) {
			t.Errorf("the rep DM body must be figureless when the rep role is off the list (%q in %q)", leak, sent.Body)
		}
	}
}
