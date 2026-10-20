package messaging

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// - DIB-462: TARGETED PAYMENT NOTIFICATIONS -
//
// DIB-449 answered "must we get a message for every payment?" with NO: payments are ROUTINE, they
// collapse into one day line per branch channel, and #payments is quiet for everyone by default.
//
// This ticket answers the follow-up the same call raised, "but there are three payments I DO want to
// hear about", WITHOUT reopening the first answer. The shape that makes both true at once:
//
//	THE TIER DECIDES PRESENTATION. A RULE DECIDES WHO GETS TOLD.
//
// A routine payment still rolls up, unchanged, always. A payment that ALSO matches a rule somebody has
// switched on gets ONE extra, targeted notice delivered to that person, and to nobody else. Delete
// every row in msg_payment_notify_rules and the system behaves exactly as DIB-449 shipped it. That is
// the invariant this file is written around, and TestPaymentRules_RollupIsUnaffectedByRules pins it.
//
// - The three rules -
//
//	ONLINE        (a) nobody at a counter took it. A portal/statement/remote payment lands on the
//	                  account while the branch is closed or busy, and today the only way anyone learns
//	                  of it is to go and look. This is the "I don't have to call them again" case.
//	CROSS_BRANCH  (b) the branch that TOOK the money is not the branch that OWNS the order. The cash
//	                  is in Kingston's till and the job is Trenton's, somebody in Trenton is still
//	                  chasing a deposit that has already been paid.
//	OWN_ORDER     (c) the payment is against an order this user is the assigned rep on. Whose order it
//	                  is, is not re-decided here: it is resolveAssignment (DIB-273/333), the one
//	                  expression of the ORDER-override → CUSTOMER-level ruling in this codebase.
//
// - Every rule is OFF until a person turns it on -
//
// The storage holds DECISIONS, not defaults (0157): no row means off. So the fan-out below starts from
// "who asked for this?" and can only ever grow to the people who did. There is no code path that
// notifies a user with no row, a property worth stating because it is exactly the property a
// well-meaning future change ("surely the rep always wants to know") would quietly destroy.

// Payment notification rules. These strings are the config's identity: they are the
// msg_payment_notify_rules.rule CHECK values and the ids the prefs surface PUTs. Add, never rename.
const (
	// RulePaymentOnline, (a) a remote/online payment: no till session and no receiving staffer.
	RulePaymentOnline = "ONLINE"
	// RulePaymentCrossBranch, (b) taken at a branch other than the order's.
	RulePaymentCrossBranch = "CROSS_BRANCH"
	// RulePaymentOwnOrder, (c) against an order the recipient is the assigned rep on.
	RulePaymentOwnOrder = "OWN_ORDER"
)

// PaymentRules is the closed rule set, in the order the prefs surface renders them. The CHECK in 0157
// is this list; a value outside it is refused at the service boundary (ErrBadPaymentRule → 400) so it
// never reaches Postgres as a constraint violation → 500.
var PaymentRules = []string{RulePaymentOnline, RulePaymentCrossBranch, RulePaymentOwnOrder}

// ErrBadPaymentRule is a rule id outside the closed set, a client fault (400).
var ErrBadPaymentRule = errors.New("messaging: unknown payment notification rule")

// normalizePaymentRule validates a rule id against the closed set.
func normalizePaymentRule(rule string) (string, bool) {
	for _, r := range PaymentRules {
		if r == rule {
			return r, true
		}
	}
	return "", false
}

// PaymentRuleLabel is the human name of a rule, used by the prefs surface and by the notice body. Kept
// beside the constants so the words a user reads and the words the message uses cannot drift apart.
func PaymentRuleLabel(rule string) string {
	switch rule {
	case RulePaymentOnline:
		return "Online and account payments"
	case RulePaymentCrossBranch:
		return "Payments taken at another branch"
	case RulePaymentOwnOrder:
		return "Payments on my orders"
	default:
		return rule
	}
}

// PaymentRulePrefs is one user's answer for every rule, ALWAYS the full set, with the unstored rules
// present and false.
//
// Returning the complete set rather than only the stored rows is the same honesty the DIB-449 prefs
// surface applies to an inherited channel default: a control that renders from a partial map has to
// invent the missing half, and "no row" and "off" must look identical to the reader because they mean
// the same thing to the system.
type PaymentRulePrefs struct {
	UserID string          `json:"user_id"`
	Rules  map[string]bool `json:"rules"`
}

// newPaymentRulePrefs builds the full-set answer from the stored rows (absent → false).
func newPaymentRulePrefs(userID string, stored map[string]bool) PaymentRulePrefs {
	out := PaymentRulePrefs{UserID: userID, Rules: make(map[string]bool, len(PaymentRules))}
	for _, r := range PaymentRules {
		out.Rules[r] = stored[r]
	}
	return out
}

// PaymentFacts is the server-resolved truth about ONE payment, everything the matchers need and
// nothing they do not.
//
// Resolved from the payments row and its allocations, NEVER from the event payload. The raw
// payment.received payload carries a payment id, an amount and a method and that is all (gl/module.go
// says so in as many words); branch, order and ownership are facts of the LEDGER, and the W2/W3
// doctrine this module already follows for customer identity (RouteCustomerMessage: "a caller can say
// WHICH customer is talking; it cannot say who that customer IS") applies with more force to money.
//
// Found is false when the payment id resolves to no row. Every matcher returns false for it: a payment
// we cannot describe is a payment we must not make claims about.
type PaymentFacts struct {
	PaymentID string `json:"payment_id"`
	Found     bool   `json:"-"`

	// TakenBranchID is payments.branch_id, where the money was received.
	TakenBranchID   string `json:"taken_branch_id,omitempty"`
	TakenBranchName string `json:"taken_branch_name,omitempty"`
	// TakenAtCounter is TRUE when a till session or a receiving staffer is stamped on the payment -
	// i.e. a human took it. Its negation is what "online/remote" MEANS here; see matchesOnline.
	TakenAtCounter bool `json:"taken_at_counter"`

	// OrderID/OrderNo/OrderBranchID are the order the payment settles, resolved through
	// payment_allocations → invoices → orders. Empty when the payment settles no order-backed invoice
	// (a pure on-account receipt), which is a normal state, not an error.
	OrderID       string `json:"order_id,omitempty"`
	OrderNo       string `json:"order_no,omitempty"`
	OrderBranchID string `json:"order_branch_id,omitempty"`
	OrderBranch   string `json:"order_branch_name,omitempty"`

	CustomerID   string `json:"customer_id,omitempty"`
	CustomerName string `json:"customer_name,omitempty"`
	AmountCents  int64  `json:"amount_cents,omitempty"`
	// OnStatement is TRUE when the payment settled at least one invoice, the receipt lands on the
	// customer's ACCOUNT/STATEMENT rather than being a bare counter tender. It is context for the
	// notice body; the ONLINE matcher does not depend on it (see matchesOnline).
	OnStatement bool `json:"on_statement,omitempty"`
}

// - THE MATCHERS -
//
// Pure functions over facts. No context, no store, no clock, the whole rule matrix is provable
// without a database, and the integration tests then prove the SQL that fills the facts.

// matchesOnline is rule (a): NOBODY TOOK THIS AT A COUNTER.
//
// The definition is deliberately the absence of a human, not the presence of a gateway. A payment is
// stamped with a till session when it is tendered at a drawer and with received_by when a staffer
// records it; a portal settlement (internal/payments Repository.Pay, "a portal payment has no staff
// allocator") carries neither. Keying on the negative catches every remote rail we have and every one
// we add later, including a statement payment posted by an integration that has no processor name.
//
// Keying on method instead would be wrong in both directions: a CARD taken at the counter is not
// online, and an EFT keyed in by an AR clerk from a bank file is not either, somebody saw both.
func matchesOnline(f PaymentFacts) bool {
	return f.Found && !f.TakenAtCounter
}

// matchesCrossBranch is rule (b): THE MONEY LANDED SOMEWHERE ELSE.
//
// Both branches must be KNOWN and they must DIFFER. An unknown branch on either side is not a match -
// "we could not tell" and "they are different" are different sentences, and only one of them is worth
// interrupting somebody for. A payment with no order has no branch to be different from, so it never
// matches: this rule is about an order's money arriving at another till, not about tills in general.
func matchesCrossBranch(f PaymentFacts) bool {
	if !f.Found || f.OrderID == "" {
		return false
	}
	if f.TakenBranchID == "" || f.OrderBranchID == "" {
		return false
	}
	return f.TakenBranchID != f.OrderBranchID
}

// matchesOwnOrder is rule (c): IT IS AGAINST YOUR ORDER.
//
// ownerUserID is the rep resolveAssignment (DIB-273/333) returned for (customer, order), the ORDER
// override first, then the customer-level default. This function does not re-derive ownership and must
// not: two expressions of "whose customer is this" is how the two drift.
//
// It requires an ORDER. A payment on an account with no order does not notify the customer's rep,
// because the rule the client stated is "an order the user owns", widening it to "a customer the user
// owns" would notify a rep about money on a job somebody else wrote. Gated in 0157, not forgotten.
func matchesOwnOrder(f PaymentFacts, ownerUserID, userID string) bool {
	if !f.Found || f.OrderID == "" {
		return false
	}
	if ownerUserID == "" || userID == "" {
		return false
	}
	return ownerUserID == userID
}

// matchedRules is the set of BROADCAST rules a payment matches, the two that are about the payment
// alone. OWN_ORDER is deliberately absent: it is a claim about a PERSON, so it can only be evaluated
// per candidate recipient (matchesOwnOrder), and folding it in here would invite a caller to treat
// "this payment matches OWN_ORDER" as a fact about the payment. It is not; it is a fact about a pair.
func matchedRules(f PaymentFacts) []string {
	out := []string{}
	if matchesOnline(f) {
		out = append(out, RulePaymentOnline)
	}
	if matchesCrossBranch(f) {
		out = append(out, RulePaymentCrossBranch)
	}
	return out
}

// - PERSISTENCE SEAM -

// paymentRuleStore is the DIB-462 persistence seam, discovered by type assertion off the base store
// exactly like classStore/rollupStore/repStore, so the shipped `store` interface, and every fake that
// implements it, stay unchanged.
type paymentRuleStore interface {
	// PaymentNotifyFacts resolves one payment into the facts the matchers read. A payment id that
	// names no row returns Found=false and NO error: an event about a payment this database cannot
	// see is not a failure of the messaging module.
	PaymentNotifyFacts(ctx context.Context, paymentID string) (PaymentFacts, error)
	// PaymentRulesFor returns the STORED rows for one user (absent rule → absent key).
	PaymentRulesFor(ctx context.Context, userID string) (map[string]bool, error)
	// SetPaymentRule upserts one user's answer for one rule and returns their whole stored set.
	SetPaymentRule(ctx context.Context, userID, rule string, enabled bool) (map[string]bool, error)
	// UsersWithPaymentRule lists the user ids that have ENABLED a rule. The fan-out read.
	UsersWithPaymentRule(ctx context.Context, rule string) ([]string, error)
}

func (s *Service) paymentRules() (paymentRuleStore, error) {
	if ps, ok := s.repo.(paymentRuleStore); ok {
		return ps, nil
	}
	return nil, errNoDB
}

// GetPaymentRules returns the caller's full rule set (unstored rules present and false).
func (s *Service) GetPaymentRules(ctx context.Context, c Caller) (PaymentRulePrefs, error) {
	if c.UserID == "" {
		return PaymentRulePrefs{}, errForbidden
	}
	ps, err := s.paymentRules()
	if err != nil {
		return PaymentRulePrefs{}, err
	}
	stored, err := ps.PaymentRulesFor(ctx, c.UserID)
	if err != nil {
		return PaymentRulePrefs{}, err
	}
	return newPaymentRulePrefs(c.UserID, stored), nil
}

// SetPaymentRule writes ONE of the caller's own toggles.
//
// Authorization is deliberately the plain staff gate and the caller's OWN id: this is a preference
// about what reaches YOUR inbox, in the same class as the per-channel notification mode (DIB-268), not
// a book-of-business decision like a rep assignment (which is admin-gated). Nobody can set anybody
// else's rules because the user id is never read from the request body.
func (s *Service) SetPaymentRule(ctx context.Context, c Caller, rule string, enabled bool) (PaymentRulePrefs, error) {
	if c.UserID == "" {
		return PaymentRulePrefs{}, errForbidden
	}
	norm, ok := normalizePaymentRule(rule)
	if !ok {
		return PaymentRulePrefs{}, ErrBadPaymentRule
	}
	ps, err := s.paymentRules()
	if err != nil {
		return PaymentRulePrefs{}, err
	}
	stored, err := ps.SetPaymentRule(ctx, c.UserID, norm, enabled)
	if err != nil {
		return PaymentRulePrefs{}, err
	}
	return newPaymentRulePrefs(c.UserID, stored), nil
}

// - THE FAN-OUT -

// PaymentNotice is one delivered targeted notice: who got it, why, and the message that was written.
// Inserted is false for an idempotent redelivery.
type PaymentNotice struct {
	UserID    string   `json:"user_id"`
	Rules     []string `json:"rules"`
	ChannelID string   `json:"channel_id"`
	MessageID string   `json:"message_id"`
	Inserted  bool     `json:"inserted"`
}

// PaymentNotifyResult is what one payment's rule evaluation did. Facts + the matched broadcast rules
// are reported even when nobody is opted in, so an operator asking "why did nobody get told?" can see
// whether the payment matched and nobody had asked, or never matched at all.
type PaymentNotifyResult struct {
	Facts   PaymentFacts    `json:"facts"`
	Matched []string        `json:"matched"`
	Owner   string          `json:"owner_user_id,omitempty"`
	Notices []PaymentNotice `json:"notices"`
}

// NotifyPaymentRules evaluates the three rules for one payment and delivers a notice to each opted-in
// recipient. Called by routeRoutine AFTER the rollup has been written, the ledger fact lands first,
// always, and a rule is a strictly additional side-effect on top of it.
//
// - Delivery reuses the shipped per-user machinery, and invents nothing -
//
// The notice is written to the recipient's DM channel via EnsureRepDMChannel, the SAME channel the
// DIB-271/273 rep routing delivers a customer's inbound message to. That is the only per-user delivery
// surface this system has, and it is already wired end to end: it provisions on demand, it is a member
// -scoped channel so nobody else can read it, it feeds the 0137/0152 badge pipeline, and the recipient
// can tune or mute it with the per-channel control they already know (DIB-268). The consumer's own
// note says the desktop-push fan-out does not exist yet; building a second, parallel notification
// transport for three rules would have been inventing a channel, which is precisely what this must not
// do.
//
// - One person, one message -
//
// A payment can match two rules for the same person (an online payment on their own order). They get
// ONE notice naming both reasons. Two messages for one payment would recreate, for the person who
// opted in, exactly the pile DIB-449 removed for everyone else.
//
// Idempotency: the event key is namespaced per (source event, recipient), so an at-least-once outbox
// redelivery re-writes nothing, the same discipline as every other event in this module.
func (s *Service) NotifyPaymentRules(ctx context.Context, eventKey, paymentID string, ref EventRef) (PaymentNotifyResult, error) {
	var res PaymentNotifyResult
	if paymentID == "" {
		// No payment id on the event: nothing to resolve facts from. Not an error, the shipped
		// msg.event.* family carries only a rendered amount, and those events still roll up correctly.
		return res, nil
	}
	ps, err := s.paymentRules()
	if err != nil {
		return res, err
	}
	facts, err := ps.PaymentNotifyFacts(ctx, paymentID)
	if err != nil {
		return res, fmt.Errorf("payment facts %s: %w", paymentID, err)
	}
	res.Facts = facts
	if !facts.Found {
		return res, nil
	}
	res.Matched = matchedRules(facts)

	// Whose order is it? Resolved through the SINGLE expression of the DIB-273/333 ruling.
	owner, err := s.resolvePaymentOwner(ctx, facts)
	if err != nil {
		return res, err
	}
	res.Owner = owner

	// Build the recipient → reasons map. A broadcast rule contributes everyone who enabled it; the
	// OWN_ORDER rule contributes at most one person, and only if THEY enabled it.
	reasons := map[string][]string{}
	for _, rule := range res.Matched {
		users, err := ps.UsersWithPaymentRule(ctx, rule)
		if err != nil {
			return res, fmt.Errorf("payment rule recipients %s: %w", rule, err)
		}
		for _, u := range users {
			if u == "" {
				continue
			}
			reasons[u] = append(reasons[u], rule)
		}
	}
	if owner != "" && matchesOwnOrder(facts, owner, owner) {
		ownerRules, err := ps.PaymentRulesFor(ctx, owner)
		if err != nil {
			return res, fmt.Errorf("payment rules for owner: %w", err)
		}
		if ownerRules[RulePaymentOwnOrder] {
			reasons[owner] = append(reasons[owner], RulePaymentOwnOrder)
		}
	}
	if len(reasons) == 0 {
		return res, nil // matched nothing anybody asked for: the rollup is the whole story
	}

	// A rule is a wish, never an authority (HH-424): a notice carries the payment's amount, customer and
	// order, so each wisher must also hold the payment trail and reach the payment's branch. A wisher
	// the fence cannot answer for is withheld, and the others are still told.
	var fenceErrs []error
	for _, userID := range sortedKeys(reasons) {
		may, ferr := s.mayHearPayment(ctx, userID, facts)
		if ferr != nil {
			fenceErrs = append(fenceErrs, fmt.Errorf("payment notice fence %s: %w", userID, ferr))
		}
		if !may {
			continue
		}
		notice, err := s.deliverPaymentNotice(ctx, eventKey, userID, orderRules(reasons[userID]), facts, ref)
		if err != nil {
			return res, err
		}
		res.Notices = append(res.Notices, notice)
	}
	return res, errors.Join(fenceErrs...)
}

// resolvePaymentOwner answers "whose order is this?" using resolveAssignment, the one place the
// ORDER-override → CUSTOMER-level ruling is expressed. A payment with no order, or a service with no
// rep store, resolves to nobody rather than failing: an unowned payment is an ordinary state.
func (s *Service) resolvePaymentOwner(ctx context.Context, f PaymentFacts) (string, error) {
	if f.OrderID == "" || f.CustomerID == "" {
		return "", nil
	}
	rs, err := s.reps()
	if err != nil {
		return "", nil
	}
	rows, err := rs.RepAssignmentsForCustomer(ctx, f.CustomerID)
	if err != nil {
		return "", fmt.Errorf("resolve payment owner: %w", err)
	}
	a, level := resolveAssignment(rows, f.OrderID)
	if level == LevelNone {
		return "", nil
	}
	return a.RepUserID, nil
}

// deliverPaymentNotice writes ONE recipient's notice into their DM channel.
func (s *Service) deliverPaymentNotice(ctx context.Context, eventKey, userID string, rules []string, f PaymentFacts, ref EventRef) (PaymentNotice, error) {
	rs, err := s.reps()
	if err != nil {
		return PaymentNotice{}, err
	}
	channelID, err := rs.EnsureRepDMChannel(ctx, userID)
	if err != nil {
		return PaymentNotice{}, fmt.Errorf("ensure dm for payment notice: %w", err)
	}
	body := renderPaymentNotice(f, rules)
	noticeRef := ref
	noticeRef.Topic = TopicPaymentReceived
	noticeRef.Summary = body
	noticeRef.AmountCents = f.AmountCents
	noticeRef.Rules = rules
	// Count/RollupDay belong to a rollup ROOT and would make this look like one to the client's
	// isRollup() test. A targeted notice is a single payment, so they are cleared.
	noticeRef.Count = 0
	noticeRef.RollupDay = ""
	if f.OrderID != "" {
		noticeRef.Entity = "order:" + f.OrderID
	}

	m, inserted, err := s.postEventToChannelID(ctx, channelID,
		paymentNoticeKey(eventKey, userID), body, noticeRef, EventOpts{Priority: PriorityNormal})
	if err != nil {
		return PaymentNotice{}, fmt.Errorf("post payment notice: %w", err)
	}
	return PaymentNotice{UserID: userID, Rules: rules, ChannelID: channelID, MessageID: m.ID, Inserted: inserted}, nil
}

// paymentNoticeKey namespaces the notice's idempotency key per (source event, recipient). Two
// recipients of the same payment are two different messages in two different channels; a redelivery of
// the same payment to the same recipient is the same message and must be a no-op.
func paymentNoticeKey(eventKey, userID string) string {
	return "pay-rule:" + eventKey + ":" + userID
}

// orderRules puts a recipient's matched rules in the canonical PaymentRules order and drops duplicates,
// so a body reads the same way every time regardless of the order the fan-out happened to add them.
func orderRules(rules []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(rules))
	for _, want := range PaymentRules {
		for _, got := range rules {
			if got == want && !seen[want] {
				seen[want] = true
				out = append(out, want)
			}
		}
	}
	return out
}

// sortedKeys returns a map's keys in a deterministic order, so the fan-out delivers (and a test
// asserts) in a stable sequence rather than by map iteration luck.
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// renderPaymentNotice is the sentence the recipient reads.
//
// It states the FACT first and the REASON second, because the reason is why this arrived and the fact
// is what they need:
//
//	"Payment $1,240.00 on SO-1041, Ferris Landscaping. Paid online (nobody took it at a counter)."
//	"Payment $3,880.50 on SO-1042, Ferris Landscaping. Taken at Kingston; the order is Trenton's."
//	"Payment $900.00 on SO-1043, Ferris Landscaping. On your order."
//
// A notice that only said "you asked to hear about cross-branch payments" would make the person open
// something to find out what happened, which is the cost this feature exists to remove.
func renderPaymentNotice(f PaymentFacts, rules []string) string {
	var b strings.Builder
	b.WriteString("Payment")
	if f.AmountCents != 0 {
		b.WriteString(" " + dollars(f.AmountCents))
	}
	switch {
	case f.OrderNo != "":
		b.WriteString(" on " + f.OrderNo)
	case f.OnStatement:
		b.WriteString(" on account")
	}
	if f.CustomerName != "" {
		b.WriteString(", " + f.CustomerName)
	}
	b.WriteString(".")
	for _, r := range rules {
		b.WriteString(" " + paymentReason(f, r))
	}
	return b.String()
}

// paymentReason is one rule's clause, in the words that explain what happened rather than the words of
// the setting that let it through.
func paymentReason(f PaymentFacts, rule string) string {
	switch rule {
	case RulePaymentOnline:
		if f.OnStatement {
			return "Paid online, against the account."
		}
		return "Paid online, nobody took it at a counter."
	case RulePaymentCrossBranch:
		taken, order := f.TakenBranchName, f.OrderBranch
		if taken == "" {
			taken = "another branch"
		}
		if order == "" {
			order = "the order's branch"
		}
		return fmt.Sprintf("Taken at %s; the order is %s's.", taken, order)
	case RulePaymentOwnOrder:
		return "On your order."
	default:
		return ""
	}
}
