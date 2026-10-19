package messaging

import (
	"context"
	"errors"
	"sort"
)

// - DIB-462/463 doubles -
//
// Extends the SAME fakeStore the rest of the package tests against (the w5/rollup precedent) with the
// two new seams, paymentRuleStore and paymentsTodayStore. One double for the whole package means a
// behaviour proven in one test cannot be contradicted by a different fake in another.
//
// The doubles mirror the SQL's invariants, not merely its happy path:
//   - an absent rule row is FALSE (there is no third state);
//   - UsersWithPaymentRule returns only ENABLED rows, in a deterministic order;
//   - PaymentsToday folds the recorded rollup ROOTS' refs, the same rows the channel renders, and
//     has NO access to the fake's raw payment list, so a test can make the two disagree on purpose.

type payRuleState struct {
	facts map[string]PaymentFacts // paymentID -> facts (absent → Found=false)
	rules map[string]bool         // "userID|rule" -> enabled
	users map[string]bool         // userID -> exists (kept so a rule row for an unknown user is inert)
	// rawPayments is a deliberately-separate "what a naive tile would count" list. NOTHING in the
	// shipped code can reach it. It exists so TestPaymentsToday_ReadsTheRollupNotThePayments can make
	// the raw ledger disagree with the rollup and prove which one the tile followed.
	rawPaymentCount int
	rawPaymentCents int64
	factsErr        error
}

var payRuleStates = map[*fakeStore]*payRuleState{}

func (f *fakeStore) pay() *payRuleState {
	s, ok := payRuleStates[f]
	if !ok {
		s = &payRuleState{
			facts: map[string]PaymentFacts{},
			rules: map[string]bool{},
			users: map[string]bool{},
		}
		payRuleStates[f] = s
	}
	return s
}

// setPaymentFacts seeds the server-resolved truth about one payment.
func (f *fakeStore) setPaymentFacts(paymentID string, facts PaymentFacts) {
	facts.PaymentID = paymentID
	facts.Found = true
	f.pay().facts[paymentID] = facts
}

// optIn switches a rule ON for a user (what the prefs surface does).
func (f *fakeStore) optIn(userID, rule string) {
	p := f.pay()
	p.rules[userID+"|"+rule] = true
	p.users[userID] = true
}

// optOut records an EXPLICIT off, the row that exists and says no.
func (f *fakeStore) optOut(userID, rule string) {
	p := f.pay()
	p.rules[userID+"|"+rule] = false
	p.users[userID] = true
}

// setRawPayments seeds the "naive recompute" answer the tile must NOT return.
func (f *fakeStore) setRawPayments(count int, cents int64) {
	p := f.pay()
	p.rawPaymentCount, p.rawPaymentCents = count, cents
}

// - paymentRuleStore -

func (f *fakeStore) PaymentNotifyFacts(_ context.Context, paymentID string) (PaymentFacts, error) {
	p := f.pay()
	if p.factsErr != nil {
		return PaymentFacts{}, p.factsErr
	}
	if facts, ok := p.facts[paymentID]; ok {
		return facts, nil
	}
	// Mirrors the repository: an unknown payment is Found=false and NO error.
	return PaymentFacts{PaymentID: paymentID}, nil
}

func (f *fakeStore) PaymentRulesFor(_ context.Context, userID string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, r := range PaymentRules {
		if v, ok := f.pay().rules[userID+"|"+r]; ok {
			out[r] = v
		}
	}
	return out, nil
}

func (f *fakeStore) SetPaymentRule(ctx context.Context, userID, rule string, enabled bool) (map[string]bool, error) {
	p := f.pay()
	p.rules[userID+"|"+rule] = enabled
	p.users[userID] = true
	return f.PaymentRulesFor(ctx, userID)
}

func (f *fakeStore) UsersWithPaymentRule(_ context.Context, rule string) ([]string, error) {
	out := []string{}
	for k, enabled := range f.pay().rules {
		if !enabled {
			continue
		}
		user, r, ok := cutLast(k, "|")
		if !ok || r != rule {
			continue
		}
		out = append(out, user)
	}
	sort.Strings(out)
	return out, nil
}

// cutLast splits on the LAST separator, so a user id containing the separator cannot corrupt the key.
func cutLast(s, sep string) (string, string, bool) {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return "", "", false
}

// - paymentsTodayStore -

// RollupTotalsForDay folds the recorded rollup ROOTS for the day's event key, the same rows the
// channel surface renders. It cannot see rawPayments, which is exactly the property the one-source
// test relies on: if the shipped code ever started recomputing, this double could not supply the
// answer it would need.
func (f *fakeStore) RollupTotalsForDay(_ context.Context, eventKey string, branchIDs []string, allBranches bool) (int, int64, int, error) {
	r := f.rollup()
	var count, channels int
	var cents int64
	for i := range f.events {
		m := f.events[i]
		if m.ID != "ev-"+eventKey {
			continue // the fake's root ids are "ev-<eventKey>" (see FindEventMessage)
		}
		if !f.channelInScope(m.ChannelID, branchIDs, allBranches) {
			continue
		}
		ref := r.refByID[m.ID]
		count += ref.Count
		cents += ref.AmountCents
		channels++
	}
	return count, cents, channels, nil
}

// channelInScope mirrors the SQL's branch predicate: a GLOBAL caller sees everything, a branch-scoped
// caller sees their branches plus the branchless GLOBAL channel.
func (f *fakeStore) channelInScope(channelID string, branchIDs []string, allBranches bool) bool {
	if allBranches {
		return true
	}
	branch, ok := f.pay().channelBranch()[channelID]
	if !ok || branch == "" {
		return true // the GLOBAL #payments channel
	}
	for _, b := range branchIDs {
		if b == branch {
			return true
		}
	}
	return false
}

// channelBranch maps a fake channel id to a branch, for the scope predicate above.
var fakeChannelBranches = map[*payRuleState]map[string]string{}

func (p *payRuleState) channelBranch() map[string]string {
	m, ok := fakeChannelBranches[p]
	if !ok {
		m = map[string]string{}
		fakeChannelBranches[p] = m
	}
	return m
}

// setChannelBranch stamps a branch onto a fake channel so a scoped read can exclude it.
func (f *fakeStore) setChannelBranch(channelID, branchID string) {
	f.pay().channelBranch()[channelID] = branchID
}

// errFacts makes the facts lookup fail, to prove a broken rule store cannot take the rollup down.
var errFacts = errors.New("payment facts unavailable")

func (f *fakeStore) breakPaymentFacts() { f.pay().factsErr = errFacts }

// - paymentRecipientStore and PaymentAccess -

var recipientStates = map[*fakeStore]map[string]PaymentRecipient{}

// setRecipient seeds one staffer's platform identity, orgs and branch reach.
func (f *fakeStore) setRecipient(userID string, r PaymentRecipient) {
	m, ok := recipientStates[f]
	if !ok {
		m = map[string]PaymentRecipient{}
		recipientStates[f] = m
	}
	m[userID] = r
}

// PaymentRecipient mirrors the repository: an unseeded staffer is the zero value (no platform
// identity), which the fence reads as "the gate cannot answer for them".
func (f *fakeStore) PaymentRecipient(_ context.Context, userID string) (PaymentRecipient, error) {
	return recipientStates[f][userID], nil
}

// fakeTrail is a PaymentAccess keyed by platform user id; an unlisted id does not hold the trail.
type fakeTrail struct {
	holds map[string]bool
	err   error
}

func (t *fakeTrail) HoldsPaymentTrail(_ context.Context, platformUserID, _ string) (bool, error) {
	if t.err != nil {
		return false, t.err
	}
	return t.holds[platformUserID], nil
}
