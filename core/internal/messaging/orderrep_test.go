package messaging

// orderrep_test.go: a notice about one order goes to that order's sales rep, through the existing
// rep routing, and says so when it cannot.

import (
	"context"
	"strings"
	"testing"
)

func orderRepNotice(salesperson string) OrderRepNotice {
	return OrderRepNotice{
		CustomerID: custAcme, OrderID: orderSplit, OrderRepUserID: salesperson,
		EventKey: "yard.pick:1", Body: "Order #7 line 1 is short.",
	}
}

func TestNotifyOrderRep_TheOrdersOverrideWinsThenTheSalespersonThenTheCustomersRep(t *testing.T) {
	svc, f := routingFixture(t)
	ctx := context.Background()

	// Customer-level rep only.
	f.setAssignment(custAcme, "", uSam, AssignManual)
	res, err := svc.NotifyOrderRep(ctx, orderRepNotice(""))
	if err != nil || res.Kind != RouteRepDM || res.RepUserID != uSam || res.Level != LevelCustomer {
		t.Fatalf("with only a customer rep the notice goes to that rep: %+v %v", res, err)
	}
	if res.ChannelID != "dm-"+uSam || !res.Inserted {
		t.Fatalf("it lands in the rep's DM: %+v", res)
	}

	// The order's own salesperson beats the customer's default.
	res, err = svc.NotifyOrderRep(ctx, OrderRepNotice{CustomerID: custAcme, OrderID: orderSplit, OrderRepUserID: uCasey,
		EventKey: "yard.pick:2", Body: "again"})
	if err != nil || res.RepUserID != uCasey || res.Level != LevelSalesperson {
		t.Fatalf("the order's salesperson is more specific than the customer default: %+v %v", res, err)
	}

	// A per-order override beats the salesperson.
	f.setAssignment(custAcme, orderSplit, uSam, AssignManual)
	res, err = svc.NotifyOrderRep(ctx, OrderRepNotice{CustomerID: custAcme, OrderID: orderSplit, OrderRepUserID: uCasey,
		EventKey: "yard.pick:3", Body: "third"})
	if err != nil || res.RepUserID != uSam || res.Level != LevelOrder {
		t.Fatalf("a per-order override is the most specific of all: %+v %v", res, err)
	}
}

func TestNotifyOrderRep_NoRepWritesNothingAndSaysSo(t *testing.T) {
	svc, f := routingFixture(t)
	res, err := svc.NotifyOrderRep(context.Background(), orderRepNotice(""))
	if err != nil {
		t.Fatalf("no rep is a state, not a failure: %v", err)
	}
	if res.Kind != RouteNone || res.Inserted || res.Level != LevelNone {
		t.Fatalf("with no rep nothing is posted and the caller is told to fall back: %+v", res)
	}
	if len(f.w5().conversations) != 0 {
		t.Fatal("an order notice is not a customer message and must not open a house queue conversation")
	}
}

func TestNotifyOrderRep_ARepeatedKeyIsASilentNoOp(t *testing.T) {
	svc, f := routingFixture(t)
	f.setAssignment(custAcme, "", uSam, AssignManual)
	ctx := context.Background()
	if _, err := svc.NotifyOrderRep(ctx, orderRepNotice("")); err != nil {
		t.Fatal(err)
	}
	again, err := svc.NotifyOrderRep(ctx, orderRepNotice(""))
	if err != nil || again.Inserted {
		t.Fatalf("a redelivery must not post twice: %+v %v", again, err)
	}
}

func TestNotifyOrderRep_NoRepFallsBackToTheCounterAndSaysSo(t *testing.T) {
	svc, f := routingFixture(t)
	f.slugs["sales-trenton"] = "ch-sales-trt"
	in := orderRepNotice("")
	in.FallbackSlugs = []string{"sales-nowhere", "sales-trenton"}
	in.FallbackPrefix = "No sales rep is on this order, so this came to the counter. "
	res, err := svc.NotifyOrderRep(context.Background(), in)
	if err != nil {
		t.Fatalf("the fallback must work, got %v", err)
	}
	if res.Kind != RouteCounter || res.ChannelID != "ch-sales-trt" || !res.Inserted {
		t.Fatalf("with no rep the notice lands in the first reachable fallback channel: %+v", res)
	}
	last := f.events[len(f.events)-1]
	if last.ChannelID != "ch-sales-trt" || last.Body != in.FallbackPrefix+in.Body {
		t.Fatalf("the message must say it is a fallback: %q", last.Body)
	}
}

// A fallback channel is read by roles the figure predicate may not admit, so when the caller hands
// over a figureless twin the fallback channel gets that twin, never the figured body.
func TestNotifyOrderRep_TheFallbackChannelGetsTheFigurelessBody(t *testing.T) {
	svc, f := routingFixture(t)
	f.slugs["sales-trenton"] = "ch-sales-trt"
	in := orderRepNotice("")
	in.Body = "Order O-100 ($4,250.00) short"
	in.BodyNoFigures = "Order O-100 short"
	in.FallbackSlugs = []string{"sales-trenton"}
	in.FallbackPrefix = "No sales rep is on this order, so this came to the counter. "
	res, err := svc.NotifyOrderRep(context.Background(), in)
	if err != nil {
		t.Fatalf("the fallback must work, got %v", err)
	}
	if res.Kind != RouteCounter || res.ChannelID != "ch-sales-trt" {
		t.Fatalf("with no rep the notice lands in the fallback channel: %+v", res)
	}
	last := f.events[len(f.events)-1]
	if last.Body != in.FallbackPrefix+in.BodyNoFigures {
		t.Fatalf("the fallback channel reads the figureless twin: %q", last.Body)
	}
	if strings.Contains(last.Body, "$4,250.00") {
		t.Fatalf("no figure may reach the fallback channel: %q", last.Body)
	}
}

// With no figureless twin the fallback body is the notice as written, prefixed with the fallback words.
func TestNotifyOrderRep_TheFallbackWithoutATwinUsesTheBodyAsWritten(t *testing.T) {
	svc, f := routingFixture(t)
	f.slugs["sales-trenton"] = "ch-sales-trt"
	in := orderRepNotice("")
	in.FallbackSlugs = []string{"sales-trenton"}
	in.FallbackPrefix = "No sales rep is on this order, so this came to the counter. "
	if _, err := svc.NotifyOrderRep(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if last := f.events[len(f.events)-1]; last.Body != in.FallbackPrefix+in.Body {
		t.Fatalf("a notice with no figureless twin goes as written: %q", last.Body)
	}
}

func TestNotifyOrderRep_AssignedRepNeverGetsTheFallbackWords(t *testing.T) {
	svc, f := routingFixture(t)
	f.setAssignment(custAcme, "", uSam, AssignManual)
	in := orderRepNotice("")
	in.FallbackSlugs = []string{"sales-trenton"}
	in.FallbackPrefix = "No sales rep is on this order, so this came to the counter. "
	res, err := svc.NotifyOrderRep(context.Background(), in)
	if err != nil || res.Kind != RouteRepDM {
		t.Fatalf("a rep exists: %+v %v", res, err)
	}
	if got := f.events[len(f.events)-1].Body; got != in.Body {
		t.Fatalf("the rep reads the notice as written, not the fallback sentence: %q", got)
	}
}

// The figured body goes only to the rep the notice is routed to, judged on that rep's own role: an
// override rep beats the salesperson, so the salesperson's role does not decide the override rep's copy.
func TestNotifyOrderRep_TheBodyFollowsTheRoleOfTheRepTheNoticeGoesTo(t *testing.T) {
	svc, f := routingFixture(t)
	ctx := context.Background()
	f.setRoleCode(uSam, "sales_manager")
	f.setRoleCode(uCasey, "driver")
	listed := func(role string) bool { return role == "sales_manager" }
	notice := func(key, salesperson string, pred func(string) bool) OrderRepNotice {
		return OrderRepNotice{CustomerID: custAcme, OrderID: orderSplit, OrderRepUserID: salesperson, EventKey: key,
			Body: "figured $1.00", BodyNoFigures: "plain", RoleSeesFigures: pred}
	}
	body := func(res OrderRepResult) string { return f.events[len(f.events)-1].Body }

	res, err := svc.NotifyOrderRep(ctx, notice("k1", uSam, listed))
	if err != nil || res.RepUserID != uSam || body(res) != "figured $1.00" {
		t.Fatalf("a listed salesperson keeps the figures: %+v %q %v", res, body(res), err)
	}
	res, err = svc.NotifyOrderRep(ctx, notice("k2", uCasey, listed))
	if err != nil || res.RepUserID != uCasey || body(res) != "plain" {
		t.Fatalf("an unlisted salesperson reads the plain words: %+v %q %v", res, body(res), err)
	}
	f.setAssignment(custAcme, orderSplit, uCasey, AssignManual)
	res, err = svc.NotifyOrderRep(ctx, notice("k3", uSam, listed))
	if err != nil || res.RepUserID != uCasey || res.Level != LevelOrder || body(res) != "plain" {
		t.Fatalf("an unlisted override rep reads the plain words whatever the salesperson's role: %+v %q %v", res, body(res), err)
	}
	res, err = svc.NotifyOrderRep(ctx, notice("k4", uSam, nil))
	if err != nil || body(res) != "plain" {
		t.Fatalf("no predicate is fail closed: %q %v", body(res), err)
	}
	f.w5().assignments = nil
	f.setAssignment(custAcme, orderSplit, uSam, AssignManual)
	res, err = svc.NotifyOrderRep(ctx, notice("k5", uCasey, listed))
	if err != nil || res.RepUserID != uSam || body(res) != "figured $1.00" {
		t.Fatalf("a listed override rep keeps the figures whatever the salesperson's role: %+v %q %v", res, body(res), err)
	}
	res, err = svc.NotifyOrderRep(ctx, OrderRepNotice{CustomerID: custAcme, OrderID: orderSplit, EventKey: "k6", Body: "no figures here"})
	if err != nil || body(res) != "no figures here" {
		t.Fatalf("a notice with no figureless twin goes as written: %q %v", body(res), err)
	}
}
