package assist

// commands.go, HALF B. The five deterministic read commands, and nothing else.
//
// WHY FIVE, AND WHY THESE FIVE. They are the five questions the testers actually asked out loud
// during the session that produced this ticket: where's that payment, where's that order, have we
// got any, what do they owe, what have they been doing. Each maps onto a read the app already
// performs on a screen somewhere; none of them is a new capability. That is the entire point, the
// command layer is a faster way to ask a question the app can already answer, not a new authority.
//
// WHY A COMMAND IS A STRUCT AND NOT A HANDLER. Each one carries its own argument spec and its own
// authority floor next to its own implementation, so the three cannot drift apart, and so the
// registry can render a MENU (what may I ask?) and validate an invocation without knowing anything
// about payments or stock. The frontend's command palette is generated from this list; adding a
// sixth command lights it up with no frontend change.
//
// READ-ONLY IS STRUCTURAL, NOT A CONVENTION. The consumer interfaces in model.go expose no writer.
// There is no method on any of them that could mutate anything, so "the assistant cannot change
// your data" is not a promise about what the code does, it is a statement about what the code can
// reach. A later ticket that wants a write command has to add a writer interface, in a diff a
// reviewer will see.
//
// EVERY COMMAND HANDLES "NOTHING FOUND" AS AN ANSWER. Result.Empty with a headline that says what
// was looked for and, where it helps, what would change the answer. DIB-443 was filed because a
// surface reported an ordinary empty state in the voice of a fault; a command layer that repeats
// that mistake would repeat it in five places at once.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ArgSpec describes one argument: what it is called on the wire, what to call it on screen, and
// whether the command can run without it.
//
// Example is not decoration, it is what the frontend puts in the placeholder and what the stub
// provider's help text quotes, so a user who has never seen the command can fill it in correctly on
// the first try.
type ArgSpec struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Example  string `json:"example"`
	Required bool   `json:"required"`
}

// Args is one invocation's arguments, already trimmed. Missing keys read as "".
type Args map[string]string

func (a Args) get(k string) string { return strings.TrimSpace(a[k]) }

// Command is one deterministic read.
//
// MaxTier is the CODE FLOOR: the numerically highest (least privileged) identity tier permitted.
// A row in assist_command_policy may make this stricter and can never make it looser, the registry
// takes the tighter of the two (registry.go). Putting the floor here rather than only in the
// database means a command's authority is visible in the same file as its implementation and cannot
// be widened by anyone who can write to a config table.
type Command struct {
	Code    string    `json:"code"`
	Title   string    `json:"title"`
	Summary string    `json:"summary"`
	MaxTier int       `json:"max_tier"`
	Args    []ArgSpec `json:"args"`

	// run is unexported: a command is invoked THROUGH the registry, which is where the authority
	// check lives. An exported func field would let a caller hold a command value and skip the gate,
	// which is the one mistake this design must make impossible rather than discouraged.
	run func(ctx context.Context, deps Deps, c Caller, a Args) (Result, error)
}

// Deps is the set of read services the commands run through. A nil member is an unwired seam: the
// registry answers such a command with an honest "not available in this deployment" rather than
// panicking, because a partial wiring is a normal state during rollout and a nil-pointer panic in
// an assistant is an outage in a feature nobody depends on yet.
type Deps struct {
	Payments  PaymentReader
	Orders    OrderReader
	Stock     StockReader
	Customers CustomerReader

	// -- DIB-478: the rest of the yard (model_domain.go) ----------------------------------------
	// Same rule, one file down: a nil member is an unwired seam and its commands answer 503, so a
	// deployment that has wired six of eleven is a deployment where six commands work.
	Rollup       RollupReader
	PaymentRange PaymentRangeReader
	AR           ARReader
	Invoices     InvoiceReader
	OrderList    OrderListReader
	Quotes       QuoteReader
	Incoming     IncomingReader
	Catalogue    CatalogueReader
	Price        PriceReader
	Branches     BranchReader

	// -- DIB-494: the order's audit trail (model_activity.go) -----------------------------------
	// Same rule again: nil means unwired, and order.activity answers 503 while everything else works.
	Activity OrderActivityReader

	// Now is the clock the date-shaped commands ask. Injected rather than read from time.Now at the
	// call site so "payments today" is testable without waiting for tomorrow, and so ONE clock
	// decides the day for every command in an exchange, two commands in one answer disagreeing
	// about which day it is would be a bug nobody could reproduce after midnight.
	Now func() time.Time
}

// clock is the safe accessor. A zero Deps is a legal value (every test builds one) and a nil clock
// must not panic in a read-only assistant.
func (d Deps) clock() time.Time {
	if d.Now == nil {
		return time.Now()
	}
	return d.Now()
}

// maxItems bounds every command's result. A lookup that returns 200 rows is a report, and a report
// pasted into a chat window is unreadable, the Note field says the list was cut so nobody mistakes
// a truncated answer for a complete one.
const maxItems = 10

// BuiltinCommands is the shipped registry content, in menu order: the two "find the thing I have in
// my hand" lookups first, then stock, then the account questions, and then (DIB-478) the rest of
// the yard, appended rather than interleaved so the original five keep the order testers learned.
// DIB-494's order-activity command is appended after those, by the same reasoning.
func BuiltinCommands() []Command {
	return append(append(coreCommands(), DomainCommands()...), ActivityCommands()...)
}

func coreCommands() []Command {
	return []Command{
		{
			Code:    "payment.lookup",
			Title:   "Find a payment",
			Summary: "Look up a payment by its reference, its exact amount, or the customer it came from.",
			// Money: manager and above, matching who can already open the AR console. The payment
			// trail itself is narrower than the tier (ruling 87): runPaymentLookup also demands
			// Caller.PaymentTrail, the handler's stamp of the role gate's own answer.
			MaxTier: 2,
			Args: []ArgSpec{
				{Name: "reference", Label: "Reference", Example: "PMT-1042", Required: false},
				{Name: "amount", Label: "Amount", Example: "1240.00", Required: false},
				{Name: "customer", Label: "Customer", Example: "Northgate Landscaping", Required: false},
			},
			run: runPaymentLookup,
		},
		{
			Code:    "order.lookup",
			Title:   "Find an order",
			Summary: "Look up an order by its number and see its status, customer and total.",
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "order_no", Label: "Order number", Example: "SO-4", Required: true},
			},
			run: runOrderLookup,
		},
		{
			Code:    "stock.lookup",
			Title:   "Check stock",
			Summary: "On-hand, allocated and available for a product, branch by branch.",
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "product", Label: "Product", Example: "TB-BLU60", Required: true},
				// DIB-478: the BRANCH slot. Optional, and it exists so "what about Kingston?" has
				// somewhere to land, the resolver only swaps an entity into an argument the command
				// actually declares (resolver.slotsFor), so a command with no branch argument simply
				// is not branch-swappable. Adding the argument here is what makes stock the domain a
				// conversation can move around in.
				{Name: "branch", Label: "Branch", Example: "Kingston", Required: false},
			},
			run: runStockLookup,
		},
		{
			Code:    "customer.balance",
			Title:   "Customer balance",
			Summary: "What a customer owes right now, and how much of it is overdue.",
			MaxTier: 2,
			Args: []ArgSpec{
				{Name: "customer", Label: "Customer", Example: "Northgate Landscaping", Required: true},
			},
			run: runCustomerBalance,
		},
		{
			Code:    "customer.activity",
			Title:   "Recent activity",
			Summary: "The last few orders and payments on a customer's account.",
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "customer", Label: "Customer", Example: "Northgate Landscaping", Required: true},
			},
			run: runCustomerActivity,
		},
	}
}

// -- payment.lookup -----------------------------------------------------------------------------

func runPaymentLookup(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	// THE WHOLE ANSWER IS THE PAYMENT TRAIL (ruling 87): number, amount, method, received day,
	// customer, branch. The tier floor above lets every manager reach this command, but the trail is
	// held to the money desk, so a caller the handler did not stamp as holding the trail reads is
	// refused here, before the arguments and before the seam, the same decision customer.activity's
	// read makes over the same stamp. Answering empty instead would say "no such payment" to a person
	// the money desk simply has not admitted, a confident wrong answer rather than a refusal.
	if !c.PaymentTrail {
		return Result{}, ErrRefused
	}
	if deps.Payments == nil {
		return Result{}, errUnwired("payment lookup")
	}
	q := PaymentQuery{
		Reference:   a.get("reference"),
		CustomerRef: a.get("customer"),
		Limit:       maxItems,
	}
	if raw := a.get("amount"); raw != "" {
		cents, err := parseAmountCents(raw)
		if err != nil {
			return Result{}, err
		}
		q.AmountCents = cents
	}
	// All three filters empty is refused HERE rather than by an adapter, so every adapter cannot
	// independently decide what an unfiltered query means. "Show me the payments" is a report.
	if q.Reference == "" && q.CustomerRef == "" && q.AmountCents == 0 {
		return Result{}, badArg("Tell me the payment reference, the exact amount, or the customer, I can't search every payment.")
	}

	rows, err := deps.Payments.FindPayments(ctx, c, q)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{
			Command:  "payment.lookup",
			Headline: "No payment matches " + describePaymentQuery(q) + ".",
			Empty:    true,
			Note:     "It may be under a different reference, or at a branch you don't have access to.",
		}, nil
	}

	items := make([]Item, 0, len(rows))
	for _, p := range rows {
		if len(items) == maxItems {
			break
		}
		fields := []Field{
			{Label: "Amount", Value: money(p.AmountCents)},
			{Label: "Status", Value: p.Status},
			{Label: "Method", Value: p.Method},
			{Label: "Received", Value: day(p.ReceivedAt)},
		}
		if p.CustomerName != "" {
			fields = append(fields, Field{Label: "Customer", Value: p.CustomerName})
		}
		if p.BranchName != "" {
			fields = append(fields, Field{Label: "Branch", Value: p.BranchName})
		}
		if p.Reference != "" && p.Reference != p.PaymentNo {
			fields = append(fields, Field{Label: "Reference", Value: p.Reference})
		}
		items = append(items, Item{Title: p.PaymentNo, Fields: fields})
	}

	// NO Ref (DIB-465), deliberately. There is no per-payment surface in this app, the AR console
	// is a list you filter, not a record page you can address, so there is nothing to link to, and
	// the rule from DIB-402 stands: never invent a target. A link that lands somewhere the reader
	// then has to search for the payment is how people learn to stop trusting the assistant's links.
	res := Result{
		Command:  "payment.lookup",
		Headline: fmt.Sprintf("%d %s for %s.", len(items), plural(len(items), "payment", "payments"), describePaymentQuery(q)),
		Items:    items,
	}
	if len(rows) > maxItems {
		res.Note = fmt.Sprintf("Showing the first %d of %d matches, narrow the search to see the rest.", maxItems, len(rows))
	}
	return res, nil
}

// describePaymentQuery turns the filter back into the phrase a person would have used, so the
// headline reads as a reply to their question rather than as a row count.
func describePaymentQuery(q PaymentQuery) string {
	var parts []string
	if q.Reference != "" {
		parts = append(parts, "reference "+q.Reference)
	}
	if q.AmountCents != 0 {
		parts = append(parts, money(q.AmountCents))
	}
	if q.CustomerRef != "" {
		parts = append(parts, q.CustomerRef)
	}
	return strings.Join(parts, " / ")
}

// parseAmountCents reads "1240", "1,240.00", "$1,240.00" as cents.
//
// It refuses more than two decimal places instead of rounding. A person who typed 1240.005 meant
// something, and a lookup that silently searches for a different amount than the one on the screen
// is worse than one that says it did not understand.
func parseAmountCents(raw string) (int64, error) {
	s := strings.NewReplacer("$", "", ",", "", " ", "").Replace(raw)
	if s == "" {
		return 0, badArg("That amount is empty, try something like 1240.00.")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, badArg("I couldn't read %q as an amount, try something like 1240.00.", raw)
	}
	var f int64
	if hasFrac {
		if len(frac) > 2 {
			return 0, badArg("Amounts go to cents, %q has too many decimal places.", raw)
		}
		for len(frac) < 2 {
			frac += "0"
		}
		if f, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return 0, badArg("I couldn't read %q as an amount, try something like 1240.00.", raw)
		}
	}
	cents := w*100 + f
	if neg {
		cents = -cents
	}
	if cents == 0 {
		// Zero is not a payment, and it is also the "no amount filter" sentinel in PaymentQuery.
		// Refusing it here keeps the sentinel honest instead of letting "$0.00" mean "no filter".
		return 0, badArg("There are no payments for $0.00, did you mean a different amount?")
	}
	return cents, nil
}

// -- order.lookup -------------------------------------------------------------------------------

func runOrderLookup(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Orders == nil {
		return Result{}, errUnwired("order lookup")
	}
	// A user types "#SO-4", "SO-4" or "so-4" depending on where they copied it from. The order
	// service already resolves case-insensitively and normalises the missing '#'; trimming it here
	// keeps the ECHO in the headline reading like what they typed.
	no := strings.TrimSpace(a.get("order_no"))
	if no == "" {
		return Result{}, badArg("Which order? Give me the number, like SO-4.")
	}

	o, found, err := deps.Orders.OrderByNumber(ctx, c, no)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{
			Command:  "order.lookup",
			Headline: fmt.Sprintf("I can't find an order numbered %s.", no),
			Empty:    true,
			Note:     "Check the number, or it may belong to a branch you don't have access to.",
		}, nil
	}

	fields := []Field{
		{Label: "Status", Value: o.Status},
		{Label: "Customer", Value: o.CustomerName},
		{Label: "Total", Value: money(o.TotalCents)},
		{Label: "Placed", Value: day(o.PlacedAt)},
	}
	if o.BranchName != "" {
		fields = append(fields, Field{Label: "Branch", Value: o.BranchName})
	}
	if o.LineCount > 0 {
		fields = append(fields, Field{Label: "Lines", Value: strconv.Itoa(o.LineCount)})
	}
	return Result{
		Command:  "order.lookup",
		Headline: fmt.Sprintf("%s is %s, %s, %s.", o.OrderNo, strings.ToLower(o.Status), o.CustomerName, money(o.TotalCents)),
		Items:    []Item{{Title: o.OrderNo, Fields: fields}},
		// DIB-465: the order the answer is about. Absent when the read gave us no id, an older
		// adapter, a fake in a test, rather than faked from the visible number, which is not
		// addressable.
		Ref: refNamed(RefOrder, o.OrderID, o.OrderNo),
	}, nil
}

// -- stock.lookup -------------------------------------------------------------------------------

func runStockLookup(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Stock == nil {
		return Result{}, errUnwired("stock lookup")
	}
	ref := a.get("product")
	if ref == "" {
		return Result{}, badArg("Which product? A SKU works, like TB-BLU60.")
	}

	p, rows, found, err := deps.Stock.StockFor(ctx, c, ref)
	if err != nil {
		return Result{}, err
	}
	// TWO DIFFERENT EMPTIES, SAID DIFFERENTLY. "I don't know that product" and "that product has
	// never moved" are not the same sentence, and DIB-443 is the ticket that proves a user notices.
	if !found {
		return Result{
			Command:  "stock.lookup",
			Headline: fmt.Sprintf("I can't find a product matching %q.", ref),
			Empty:    true,
			Note:     "Try the SKU exactly as it appears in the catalogue.",
		}, nil
	}
	name := productLabel(p, ref)

	// DIB-478: the optional branch narrowing. FILTERING, NOT A SECOND READ, the caller-scoped
	// lookup already decided which yards this person may see, and this only chooses among them. A
	// branch argument that matches nothing the caller can reach is answered as "not in that yard",
	// never as "no such branch", because the two are indistinguishable from here and the second
	// would tell a scoped user which yards exist.
	if want := a.get("branch"); want != "" {
		kept := rows[:0:0]
		for _, s := range rows {
			if branchMatches(s.BranchName, want) {
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			return Result{
				Command:  "stock.lookup",
				Headline: fmt.Sprintf("%s has no stock on record at %s.", name, want),
				Empty:    true,
				Note:     "Nothing is on the books for that product at that yard, it may be held somewhere else, or at a branch you don't have access to.",
				Ref:      refNamed(RefProduct, p.ProductID, name),
			}, nil
		}
		rows = kept
	}

	if len(rows) == 0 {
		return Result{
			Command:  "stock.lookup",
			Headline: fmt.Sprintf("%s has no stock on record yet.", name),
			Empty:    true,
			Note:     "Nothing has been received, counted or sold against it, the first receipt will start its balance.",
			// EMPTY, AND STILL LINKED. The product resolved; it simply has no balances. "There is
			// nothing to show you, and here is the product" is a better answer than a dead end, and
			// it is the one case where the link is worth more than the numbers.
			Ref: refNamed(RefProduct, p.ProductID, name),
		}, nil
	}

	items := make([]Item, 0, len(rows))
	var totalAvail float64
	for _, s := range rows {
		totalAvail += s.Available
		items = append(items, Item{
			Title: s.BranchName,
			Fields: []Field{
				{Label: "On hand", Value: qtyUOM(s.OnHand, s.UOM)},
				{Label: "Allocated", Value: qtyUOM(s.Reserved, s.UOM)},
				{Label: "Available", Value: qtyUOM(s.Available, s.UOM)},
			},
		})
	}
	uom := rows[0].UOM
	// ONE BRANCH, NAMED IN THE SENTENCE (DIB-478). "…: 200 ea available across 1 branch" is a true
	// sentence that answers a different question from the one asked: after "what about Kingston?" the
	// reader needs the WORD Kingston in the headline, not a count of one. It matters most in a
	// comparison, where two headlines sit one above the other and "across 1 branch" twice is
	// indistinguishable from a rendering bug.
	headline := fmt.Sprintf("%s: %s available across %d %s.",
		name, qtyUOM(totalAvail, uom), len(rows), plural(len(rows), "branch", "branches"))
	if len(rows) == 1 {
		headline = fmt.Sprintf("%s at %s: %s available.", name, rows[0].BranchName, qtyUOM(totalAvail, uom))
	}
	res := Result{
		Command:  "stock.lookup",
		Headline: headline,
		Items:    items,
		// The PRODUCT, not a branch: the items are branch lines and there is no per-branch surface,
		// but there is a product page, and it is where "…and what else do we know about it" goes.
		Ref: refNamed(RefProduct, p.ProductID, name),
	}
	// Available can be negative by design (a sale that landed before its receipt). Saying so is
	// cheaper than a support call from whoever sees "-4" and assumes the screen is broken.
	if totalAvail < 0 {
		res.Note = "A negative available means more is committed than is on hand, usually a sale that landed before its receipt."
	}
	return res, nil
}

// branchMatches is how a person names a yard versus how the database prints it: "Kingston" against
// "Kingston Yard", case-insensitively, in either direction. Substring rather than equality because
// nobody says the whole registered name, and in EITHER direction because a person may also say more
// than the name ("the Kingston yard").
func branchMatches(actual, want string) bool {
	a := strings.ToLower(strings.TrimSpace(actual))
	w := strings.ToLower(strings.TrimSpace(want))
	if a == "" || w == "" {
		return false
	}
	return strings.Contains(a, w) || strings.Contains(w, a)
}

// productLabel names the product the way a person would: "Techo-Bloc Blu 60 (TB-BLU60)", falling
// back through what the reader actually returned rather than printing an empty pair of brackets.
func productLabel(p ProductRef, typed string) string {
	switch {
	case p.Name != "" && p.SKU != "":
		return fmt.Sprintf("%s (%s)", p.Name, p.SKU)
	case p.Name != "":
		return p.Name
	case p.SKU != "":
		return p.SKU
	default:
		return typed
	}
}

// -- customer.balance ---------------------------------------------------------------------------

func runCustomerBalance(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Customers == nil {
		return Result{}, errUnwired("customer lookup")
	}
	ref := a.get("customer")
	if ref == "" {
		return Result{}, badArg("Which customer? A name or account number works.")
	}
	cust, found, err := deps.Customers.ResolveCustomer(ctx, c, ref)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return notFoundCustomer("customer.balance", ref), nil
	}
	bal, err := deps.Customers.Balance(ctx, c, cust.CustomerID)
	if err != nil {
		return Result{}, err
	}

	fields := []Field{
		{Label: "Open balance", Value: money(bal.OpenCents)},
		{Label: "Overdue", Value: money(bal.OverdueCents)},
		{Label: "As of", Value: day(bal.AsOf)},
	}
	if cust.AccountNo != "" {
		fields = append(fields, Field{Label: "Account", Value: cust.AccountNo})
	}

	// A zero balance is a REAL, GOOD answer and gets its own sentence. Reporting it as "0 results"
	// would be the same category error as reporting an empty stock history as a fault.
	headline := fmt.Sprintf("%s owes %s, of which %s is overdue.", cust.Name, money(bal.OpenCents), money(bal.OverdueCents))
	if bal.OpenCents == 0 {
		headline = fmt.Sprintf("%s has nothing outstanding.", cust.Name)
	} else if bal.OverdueCents == 0 {
		headline = fmt.Sprintf("%s owes %s, none of it overdue.", cust.Name, money(bal.OpenCents))
	}
	return Result{
		Command:  "customer.balance",
		Headline: headline,
		Items:    []Item{{Title: cust.Name, Fields: fields}},
		Ref:      refNamed(RefCustomer, cust.CustomerID, cust.Name),
	}, nil
}

// -- customer.activity --------------------------------------------------------------------------

func runCustomerActivity(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Customers == nil {
		return Result{}, errUnwired("customer lookup")
	}
	ref := a.get("customer")
	if ref == "" {
		return Result{}, badArg("Which customer? A name or account number works.")
	}
	cust, found, err := deps.Customers.ResolveCustomer(ctx, c, ref)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return notFoundCustomer("customer.activity", ref), nil
	}
	rows, err := deps.Customers.RecentActivity(ctx, c, cust.CustomerID, maxItems)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{
			Command:  "customer.activity",
			Headline: fmt.Sprintf("Nothing recent on %s.", cust.Name),
			Empty:    true,
			Note:     "No orders or payments in the window I can see.",
			Ref:      refNamed(RefCustomer, cust.CustomerID, cust.Name),
		}, nil
	}

	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		if len(items) == maxItems {
			break
		}
		fields := []Field{
			{Label: "When", Value: day(r.When)},
			{Label: "Amount", Value: money(r.Cents)},
		}
		if r.Status != "" {
			fields = append(fields, Field{Label: "Status", Value: r.Status})
		}
		items = append(items, Item{Title: r.Kind + " " + r.Reference, Fields: fields})
	}
	return Result{
		Command:  "customer.activity",
		Headline: fmt.Sprintf("Last %d %s on %s.", len(items), plural(len(items), "entry", "entries"), cust.Name),
		Items:    items,
		// The CUSTOMER. The rows are orders and payments and this read carries no id for either, so
		// the answer links to the account rather than pretending each line is addressable.
		Ref: refNamed(RefCustomer, cust.CustomerID, cust.Name),
	}, nil
}

func notFoundCustomer(code, ref string) Result {
	return Result{
		Command:  code,
		Headline: fmt.Sprintf("I can't find a customer matching %q.", ref),
		Empty:    true,
		Note:     "Try the account number, or part of the name as it appears on the account.",
	}
}
