package assist

// escalation_meter_test.go, slice 2 integration with slice 1: every provider call of a multi round
// loop, a retry on the same alias or an escalation to the next one, is its own reserved and settled
// call inside the meter, and the dealer cap refuses an attempt that would pass it BEFORE that
// provider is called.

import (
	"context"
	"testing"
)

func meteredEscalationRig(t *testing.T, mainReplies, altReplies, premiumReplies []string, prices memPrices, dealerCap int64) (*escalationRig, *memSpend) {
	t.Helper()
	rig := newEscalationRig(t, mainReplies, altReplies, premiumReplies)
	sp := &memSpend{}
	rig.svc.WithMetering(sp, prices).WithDealerCap(dealerCap)
	return rig, sp
}

func escalationPrices(mainIn, altIn int64) memPrices {
	return memPrices{
		AliasMain + "/open-main":            {ID: "pm", Alias: AliasMain, Model: "open-main", InMicroPerM: mainIn, OutMicroPerM: mainIn, Seq: 1},
		AliasAlt + "/open-alt":              {ID: "pa", Alias: AliasAlt, Model: "open-alt", InMicroPerM: altIn, OutMicroPerM: altIn, Seq: 1},
		AliasPremium + "/claude-sonnet-5-5": {ID: "pp", Alias: AliasPremium, Model: "claude-sonnet-5-5", InMicroPerM: 3_000_000, OutMicroPerM: 15_000_000, Seq: 1},
	}
}

func TestEscalationEveryAttemptIsItsOwnReservedAndSettledCall(t *testing.T) {
	// main fails its check twice (the attempt and the retry), alt answers: three provider calls.
	rig, sp := meteredEscalationRig(t,
		[]string{badCommand, badCommand}, []string{oaiTextReply("alt answered")}, nil,
		escalationPrices(1_000_000, 2_000_000), 1<<60)

	ans, err := rig.svc.Ask(context.Background(), manager(), "check something", nil)
	if err != nil || ans.Text != "alt answered" {
		t.Fatalf("ask = %q (%v)", ans.Text, err)
	}
	m, a, p := rig.counts()
	if m != 2 || a != 1 || p != 0 {
		t.Fatalf("provider calls main/alt/premium = %d/%d/%d, want 2/1/0", m, a, p)
	}
	var res, set []SpendRow
	for _, r := range sp.rows {
		switch r.Kind {
		case SpendReservation:
			res = append(res, r)
		case SpendSettlement:
			set = append(set, r)
		}
	}
	if len(res) != 3 || len(set) != 3 {
		t.Fatalf("reservations %d, settlements %d, want 3 and 3: one pair per provider call", len(res), len(set))
	}
	wantAlias := []string{AliasMain, AliasMain, AliasAlt}
	exchange := res[0].ExchangeID
	for i := range res {
		if res[i].ExchangeID != exchange || res[i].CallNo != i+1 || set[i].CallNo != i+1 {
			t.Fatalf("call %d: exchange %q call no %d/%d, want one exchange and call numbers 1,2,3", i, res[i].ExchangeID, res[i].CallNo, set[i].CallNo)
		}
		if res[i].Alias != wantAlias[i] || set[i].Alias != wantAlias[i] {
			t.Fatalf("call %d priced under %q/%q, want the alias actually called, %q", i, res[i].Alias, set[i].Alias, wantAlias[i])
		}
	}
	if set[2].Model != "open-alt" || set[2].PriceID != "pa" {
		t.Fatalf("the alt call settled as %+v, want the alt model and its price row", set[2])
	}
}

func TestEscalationDealerCapRefusesAnAttemptThatWouldPassItBeforeThatProvider(t *testing.T) {
	// main is cheap and fails twice; alt's reservation estimate is enormous and passes the cap.
	rig, sp := meteredEscalationRig(t,
		[]string{badCommand, badCommand}, []string{oaiTextReply("must never be asked")}, nil,
		escalationPrices(1_000, 1_000_000_000_000), 1_000)

	ans, err := rig.svc.Ask(context.Background(), manager(), "check something", nil)
	if err != nil {
		t.Fatalf("a cap refusal is an answer, not an error: %v", err)
	}
	if !ans.LimitReached || ans.Text != LimitReachedMessage {
		t.Fatalf("answer = %+v, want the over budget answer", ans)
	}
	m, a, p := rig.counts()
	if m != 2 || a != 0 || p != 0 {
		t.Fatalf("provider calls main/alt/premium = %d/%d/%d, want 2/0/0: the alt provider must not be called", m, a, p)
	}
	for _, r := range sp.rows {
		if r.Alias == AliasAlt {
			t.Fatalf("a row was written for the refused alias: %+v", r)
		}
	}
}

func TestEscalationToTheStubIsUnmetered(t *testing.T) {
	// Every paid alias fails its check; the stub's plain results answer with no price and no cap.
	rig, sp := meteredEscalationRig(t,
		[]string{badCommand}, []string{badCommand}, []string{antToolReply(antToolUse("toolu_1", "payment__delete", `{}`))},
		escalationPrices(1, 1), 1<<60)
	if _, err := rig.svc.Ask(context.Background(), manager(), "where is order SO-4?", nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range sp.rows {
		if r.Alias == "stub" && r.CostMicroCents != 0 {
			t.Fatalf("the stub carries a cost: %+v", r)
		}
	}
}
