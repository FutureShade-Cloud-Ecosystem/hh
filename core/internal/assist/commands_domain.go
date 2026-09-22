package assist

// commands_domain.go, DIB-478. The rest of the yard, answerable.
//
// WHY THESE ELEVEN, AND WHY NOW. The original five (commands.go) were the five questions testers
// asked out loud in one session: where's that payment, where's that order, have we got any, what do
// they owe, what have they been doing. They proved the shape. What they could not do is hold a
// CONVERSATION, because a conversation goes sideways, from a product to a branch to what is on the
// way to what it costs, and every sideways step landed on "I can't answer that one yet".
//
// So the rule for this file is coverage, not cleverness: every CORE READ DOMAIN answerable, each
// one through the service that already owns it, with the same authority floor the screen has.
//
// THREE RULES THIS FILE INHERITS AND DOES NOT RESTATE (they are argued in commands.go):
//
//	READ-ONLY IS STRUCTURAL, the seams in model_domain.go expose no writer and take no tx.
//	EMPTY IS AN ANSWER, every command below has a written empty sentence, and none of them is a
//	  fault. "No payments yet today at Kingston" is a true, complete, quiet answer.
//	ONE FORMATTER, every number goes through format.go, so the panel and the sentence agree.
//
// AND ONE RULE THAT IS NEW HERE:
//
//	NO NEW SQL WHERE A SERVICE READ EXISTS. Not one command below reaches a table. Payments-today is
//	the messaging ROLLUP's number, not a sum over payments; the range is the reporting module's own
//	cash-receipts template; aging is the credit desk's own worklist; the price is the real resolver
//	in preview mode. Every one of those has an owner, and an assistant that computed its own version
//	would be a second opinion that eventually disagrees with the screen in front of the customer.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DomainCommands is the DIB-478 command set, in menu order: money first (it is what gets asked),
// then the documents, then the yard, then the catalogue.
func DomainCommands() []Command {
	return []Command{
		{
			Code:    "payments.today",
			Title:   "Payments today",
			Summary: "What has come in today across the branches you can see, the same figure the payments channel shows.",
			MaxTier: 2,
			run:     runPaymentsToday,
		},
		{
			Code:    "payments.range",
			Title:   "Payments over a period",
			Summary: "Payments taken between two dates, day by day, for one branch or all of yours.",
			MaxTier: 2,
			Args: []ArgSpec{
				{Name: "from", Label: "From", Example: "2026-08-10", Required: true},
				{Name: "to", Label: "To", Example: "2026-08-16", Required: true},
				{Name: "branch", Label: "Branch", Example: "Kingston", Required: false},
			},
			run: runPaymentsRange,
		},
		{
			Code:    "ar.aging",
			Title:   "AR aging",
			Summary: "Who owes what, oldest money first, across the branches you can see.",
			MaxTier: 2,
			run:     runARAging,
		},
		{
			Code:    "ar.holds",
			Title:   "Credit holds",
			Summary: "Which accounts are on credit hold right now, and the reason each one was stopped.",
			MaxTier: 2,
			run:     runARHolds,
		},
		{
			Code:    "customer.invoices",
			Title:   "Customer invoices",
			Summary: "A customer's invoices with their status, due date and what is still outstanding.",
			MaxTier: 2,
			Args: []ArgSpec{
				{Name: "customer", Label: "Customer", Example: "Northgate Landscaping", Required: true},
				{Name: "status", Label: "Status", Example: "FINALIZED", Required: false},
			},
			run: runCustomerInvoices,
		},
		{
			Code:    "customer.orders",
			Title:   "Customer orders",
			Summary: "A customer's orders, their status, and whether each is a pickup or a delivery.",
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "customer", Label: "Customer", Example: "Northgate Landscaping", Required: true},
				{Name: "status", Label: "Status", Example: "CONFIRMED", Required: false},
			},
			run: runCustomerOrders,
		},
		{
			Code:    "customer.quotes",
			Title:   "Quotes",
			Summary: "Quotes and the state each one is in, open, accepted, or aged out.",
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "customer", Label: "Customer", Example: "Northgate Landscaping", Required: false},
				{Name: "status", Label: "Status", Example: "SENT", Required: false},
			},
			run: runCustomerQuotes,
		},
		{
			Code:    "quote.holds",
			Title:   "Quote stock holds",
			Summary: "What stock a quote is holding, at which yard, and when the hold lapses.",
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "quote_no", Label: "Quote number", Example: "Q-1042", Required: true},
			},
			run: runQuoteHolds,
		},
		{
			Code:    "stock.incoming",
			Title:   "What's on the way",
			Summary: "Stock on order and in transit for a product, with the dates it is promised for.",
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "product", Label: "Product", Example: "TB-BLU60", Required: true},
				// The BRANCH slot, matching stock.lookup's. A conversation that has settled on a yard
				// must be able to stay there when it moves from "how many have we got" to "when's more
				// arriving", and the resolver only carries a branch into a command that declares one.
				{Name: "branch", Label: "Branch", Example: "Kingston", Required: false},
			},
			run: runStockIncoming,
		},
		{
			Code:    "product.find",
			Title:   "Find a product",
			Summary: "Look a product up in the catalogue by name, SKU or the words your customers use.",
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "product", Label: "Product", Example: "Blu 60", Required: true},
			},
			run: runProductFind,
		},
		{
			Code:    "product.price",
			Title:   "Price for a customer",
			Summary: "What a product costs a particular customer, and why that is the price.",
			MaxTier: 2,
			Args: []ArgSpec{
				{Name: "product", Label: "Product", Example: "TB-BLU60", Required: true},
				{Name: "customer", Label: "Customer", Example: "Northgate Landscaping", Required: true},
				{Name: "branch", Label: "Branch", Example: "Kingston", Required: false},
			},
			run: runProductPrice,
		},
	}
}

// -- payments.today -----------------------------------------------------------------------------

func runPaymentsToday(ctx context.Context, deps Deps, c Caller, _ Args) (Result, error) {
	if deps.Rollup == nil {
		return Result{}, errUnwired("today's payments")
	}
	d, err := deps.Rollup.PaymentsToday(ctx, c, deps.clock())
	if err != nil {
		return Result{}, err
	}
	if d.Count == 0 {
		// QUIET PROSE, NOT AN ERROR AND NOT A DASH. The number IS measured and it is zero, which at
		// nine in the morning is the commonest true answer there is.
		return Result{
			Command:  "payments.today",
			Headline: "No payments yet today.",
			Empty:    true,
			Note:     "This is the same figure the payments channel shows for " + d.Day + ".",
		}, nil
	}
	return Result{
		Command: "payments.today",
		Headline: fmt.Sprintf("%s in today, across %d %s.",
			money(d.Cents), d.Count, plural(d.Count, "payment", "payments")),
		Items: []Item{{
			Title: "Today (" + d.Day + ")",
			Fields: []Field{
				{Label: "Taken", Value: money(d.Cents)},
				{Label: "Payments", Value: fmt.Sprint(d.Count)},
				{Label: "Branches reporting", Value: fmt.Sprint(d.Channels)},
			},
		}},
		// NO REF, and it is not a gap: this app has no per-day payments surface to open. The DIB-402
		// rule stands, never invent a target.
		Note: "Read from the payments rollup, so it matches the channel and the dashboard tile exactly.",
	}, nil
}

// -- payments.range -----------------------------------------------------------------------------

func runPaymentsRange(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.PaymentRange == nil {
		return Result{}, errUnwired("payments over a period")
	}
	from, okFrom := parseDay(a.get("from"))
	to, okTo := parseDay(a.get("to"))
	if !okFrom || !okTo {
		return Result{}, badArg("Give me two dates like 2026-08-10 and 2026-08-16, or just say \"last week\".")
	}
	if to.Before(from) {
		// SWAPPED RATHER THAN REFUSED. "From the 16th to the 10th" is a typo with exactly one
		// sensible reading, and refusing it teaches nobody anything.
		from, to = to, from
	}
	rows, err := deps.PaymentRange.PaymentsByDay(ctx, c, PaymentRangeQuery{
		From:     from.Format(dayFmt),
		To:       to.Format(dayFmt),
		BranchID: a.get("branch"),
	})
	if err != nil {
		return Result{}, err
	}
	where := ""
	if b := a.get("branch"); b != "" {
		where = " at " + b
	}
	period := span(from, to)
	if len(rows) == 0 {
		return Result{
			Command:  "payments.range",
			Headline: fmt.Sprintf("No payments%s between %s.", where, period),
			Empty:    true,
			Note:     "Nothing was received or deposited in that window, a quiet week is an answer, not a gap.",
		}, nil
	}

	// THE FOLD IS OVER DAYS THE REPORT ALREADY TOTALLED, and it is done HERE rather than in the
	// adapter on purpose: the adapter's rule is that it computes nothing, and this is the answer
	// layer joining figures that each have one owner. Nothing is recomputed from payment rows.
	var totalCents int64
	var totalCount int
	items := make([]Item, 0, len(rows))
	for _, d := range rows {
		totalCents += d.Cents
		totalCount += d.Count
		if len(items) < maxItems {
			items = append(items, Item{
				Title: d.Day,
				Fields: []Field{
					{Label: "Taken", Value: money(d.Cents)},
					{Label: "Payments", Value: fmt.Sprint(d.Count)},
				},
			})
		}
	}
	res := Result{
		Command: "payments.range",
		Headline: fmt.Sprintf("%s%s over %s, %d %s across %d %s.",
			money(totalCents), where, period, totalCount, plural(totalCount, "payment", "payments"),
			len(rows), plural(len(rows), "day", "days")),
		Items: items,
	}
	notes := []string{}
	if len(rows) > maxItems {
		notes = append(notes, fmt.Sprintf("Showing the first %d of %d days; the total covers all of them.", maxItems, len(rows)))
	}
	// TWO SOURCES, NAMED. The rollup owns "today"; this report owns history. They can differ for the
	// current day (a payment that arrived after the rollup wrote its line), and a reader who is not
	// told that will find the difference themselves and trust neither.
	if !to.Before(deps.clock().Truncate(24 * time.Hour)) {
		notes = append(notes, "This is the cash-receipts report. For today's live figure ask \"payments today\", that reads the payments channel's own rollup.")
	}
	res.Note = strings.Join(notes, " ")
	return res, nil
}

// -- ar.aging -----------------------------------------------------------------------------------

func runARAging(ctx context.Context, deps Deps, c Caller, _ Args) (Result, error) {
	if deps.AR == nil {
		return Result{}, errUnwired("AR aging")
	}
	rows, err := deps.AR.Aging(ctx, c, deps.clock())
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{
			Command:  "ar.aging",
			Headline: "Nothing outstanding, no account is carrying a balance in the branches you can see.",
			Empty:    true,
			Note:     "A clean ledger is a real state, not a missing report.",
		}, nil
	}
	var total, overdue int64
	items := make([]Item, 0, maxItems)
	for _, r := range rows {
		total += r.TotalCents
		overdue += r.Days30Cents + r.Days60Cents + r.Days90Cents
		if len(items) >= maxItems {
			continue
		}
		fields := []Field{
			{Label: "Total", Value: money(r.TotalCents)},
			{Label: "Current", Value: money(r.CurrentCents)},
			{Label: "31-60", Value: money(r.Days30Cents)},
			{Label: "61-90", Value: money(r.Days60Cents)},
			{Label: "90+", Value: money(r.Days90Cents)},
		}
		if r.OnHold {
			fields = append(fields, Field{Label: "Credit", Value: "ON HOLD" + holdSuffix(r.HoldKind)})
		}
		items = append(items, Item{Title: r.CustomerName, Fields: fields})
	}
	res := Result{
		Command: "ar.aging",
		Headline: fmt.Sprintf("%s outstanding across %d %s, %s of it past due.",
			money(total), len(rows), plural(len(rows), "account", "accounts"), money(overdue)),
		Items: items,
	}
	if len(rows) > maxItems {
		res.Note = fmt.Sprintf("Showing the %d largest of %d accounts; the totals cover all of them.", maxItems, len(rows))
	}
	if len(rows) == 1 {
		res.Ref = refNamed(RefCustomer, rows[0].CustomerID, rows[0].CustomerName)
	}
	return res, nil
}

func holdSuffix(kind string) string {
	if strings.TrimSpace(kind) == "" {
		return ""
	}
	return " (" + strings.ToLower(kind) + ")"
}

// -- ar.holds -----------------------------------------------------------------------------------

// runARHolds answers BOTH halves of the question. "Who is on credit hold" without "and why" is the
// half nobody can act on: a rep told an account is stopped and not told it is a 90-day aging hold
// cannot tell the customer anything, and will phone the credit desk to ask, which is the call the
// assistant was supposed to save.
func runARHolds(ctx context.Context, deps Deps, c Caller, _ Args) (Result, error) {
	if deps.AR == nil {
		return Result{}, errUnwired("credit holds")
	}
	rows, scoped, err := deps.AR.Holds(ctx, c)
	if err != nil {
		return Result{}, err
	}
	scopeNote := ""
	if scoped {
		// HONEST ABOUT THE FENCE. credit_holds carries no branch of its own, so a branch-scoped
		// caller's list is narrowed through the accounts that have a balance in their yards. Saying
		// so is the difference between a short list and a WRONG list.
		scopeNote = "Limited to accounts carrying a balance at the branches you can see."
	}
	if len(rows) == 0 {
		return Result{
			Command:  "ar.holds",
			Headline: "Nobody is on credit hold right now.",
			Empty:    true,
			Note:     strings.TrimSpace("No account is stopped. " + scopeNote),
		}, nil
	}
	items := make([]Item, 0, maxItems)
	for _, h := range rows {
		if len(items) >= maxItems {
			break
		}
		fields := []Field{
			{Label: "Reason", Value: reasonOrDefault(h.Reason, h.Kind)},
			{Label: "Placed", Value: day(h.PlacedAt)},
			{Label: "Placed by", Value: placedBy(h.AutoPlaced)},
		}
		if !h.OverrideUntil.IsZero() {
			fields = append(fields, Field{Label: "Override until", Value: day(h.OverrideUntil)})
			if h.OverrideReason != "" {
				fields = append(fields, Field{Label: "Override reason", Value: h.OverrideReason})
			}
		}
		items = append(items, Item{Title: h.CustomerName, Fields: fields})
	}
	res := Result{
		Command: "ar.holds",
		Headline: fmt.Sprintf("%d %s on credit hold.",
			len(rows), plural(len(rows), "account is", "accounts are")),
		Items: items,
	}
	notes := []string{}
	if len(rows) > maxItems {
		notes = append(notes, fmt.Sprintf("Showing %d of %d.", maxItems, len(rows)))
	}
	if scopeNote != "" {
		notes = append(notes, scopeNote)
	}
	res.Note = strings.Join(notes, " ")
	if len(rows) == 1 {
		res.Ref = refNamed(RefCustomer, rows[0].CustomerID, rows[0].CustomerName)
	}
	return res, nil
}

// reasonOrDefault never prints an empty reason. A hold with no recorded reason is a real row (an
// older one, or one placed by a sweep before reasons were required) and naming the KIND is more
// honest than a blank cell that reads as a rendering bug.
func reasonOrDefault(reason, kind string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	if strings.TrimSpace(kind) != "" {
		return strings.ToLower(kind) + " hold, no reason recorded"
	}
	return "No reason recorded"
}

func placedBy(auto bool) string {
	if auto {
		return "The nightly credit sweep"
	}
	return "A person"
}

// -- customer.invoices --------------------------------------------------------------------------

func runCustomerInvoices(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Customers == nil || deps.Invoices == nil {
		return Result{}, errUnwired("customer invoices")
	}
	cust, ok, err := resolveAccount(ctx, deps, c, a.get("customer"), "customer.invoices")
	if err != nil {
		return Result{}, err
	}
	if !ok.found {
		return ok.result, nil
	}
	rows, err := deps.Invoices.InvoicesFor(ctx, c, cust.CustomerID, strings.ToUpper(a.get("status")), maxItems)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{
			Command:  "customer.invoices",
			Headline: fmt.Sprintf("No invoices on %s%s.", cust.Name, statusClause(a.get("status"))),
			Empty:    true,
			Note:     "Nothing has been invoiced on this account in the window I can see.",
			Ref:      refNamed(RefCustomer, cust.CustomerID, cust.Name),
		}, nil
	}
	var open int64
	items := make([]Item, 0, len(rows))
	for _, inv := range rows {
		open += inv.BalanceCents
		items = append(items, Item{
			Title: inv.InvoiceNo,
			Fields: []Field{
				{Label: "Status", Value: inv.Status},
				{Label: "Total", Value: money(inv.TotalCents)},
				{Label: "Outstanding", Value: money(inv.BalanceCents)},
				{Label: "Issued", Value: day(inv.IssueDate)},
				{Label: "Due", Value: day(inv.DueDate)},
			},
		})
	}
	headline := fmt.Sprintf("%d %s on %s, %s still outstanding.",
		len(rows), plural(len(rows), "invoice", "invoices"), cust.Name, money(open))
	if open == 0 {
		headline = fmt.Sprintf("%d %s on %s, all paid.", len(rows), plural(len(rows), "invoice", "invoices"), cust.Name)
	}
	return Result{
		Command:  "customer.invoices",
		Headline: headline,
		Items:    items,
		// The CUSTOMER: this app has no addressable per-invoice staff surface, so the account is the
		// nearest true target. Naming an invoice route that does not exist would be the DIB-402 error.
		Ref: refNamed(RefCustomer, cust.CustomerID, cust.Name),
	}, nil
}

func statusClause(status string) string {
	if strings.TrimSpace(status) == "" {
		return ""
	}
	return " with status " + strings.ToUpper(strings.TrimSpace(status))
}

// -- customer.orders ----------------------------------------------------------------------------

func runCustomerOrders(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Customers == nil || deps.OrderList == nil {
		return Result{}, errUnwired("customer orders")
	}
	cust, ok, err := resolveAccount(ctx, deps, c, a.get("customer"), "customer.orders")
	if err != nil {
		return Result{}, err
	}
	if !ok.found {
		return ok.result, nil
	}
	rows, err := deps.OrderList.OrdersFor(ctx, c, cust.CustomerID, strings.ToUpper(a.get("status")), maxItems)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{
			Command:  "customer.orders",
			Headline: fmt.Sprintf("No orders on %s%s.", cust.Name, statusClause(a.get("status"))),
			Empty:    true,
			Note:     "Nothing has been ordered on this account in the window I can see.",
			Ref:      refNamed(RefCustomer, cust.CustomerID, cust.Name),
		}, nil
	}
	var total int64
	items := make([]Item, 0, len(rows))
	for _, o := range rows {
		total += o.TotalCents
		items = append(items, Item{
			Title: o.OrderNo,
			Fields: []Field{
				{Label: "Status", Value: o.Status},
				{Label: "Going out", Value: fulfilmentWord(o.Fulfillment)},
				{Label: "Wanted", Value: day(o.Requested)},
				{Label: "Total", Value: money(o.TotalCents)},
			},
		})
	}
	res := Result{
		Command: "customer.orders",
		Headline: fmt.Sprintf("%d %s on %s, %s in total.",
			len(rows), plural(len(rows), "order", "orders"), cust.Name, money(total)),
		Items: items,
		Ref:   refNamed(RefCustomer, cust.CustomerID, cust.Name),
	}
	if len(rows) == 1 && rows[0].OrderID != "" {
		// ONE order: the answer is about THAT order, and the order page is a better destination than
		// the account it belongs to.
		res.Ref = refNamed(RefOrder, rows[0].OrderID, rows[0].OrderNo)
	}
	return res, nil
}

// fulfilmentWord turns the schema's enum into how the yard says it. An unrecognised value passes
// through verbatim rather than becoming "Other", a later migration's new method should read as
// itself, not be swallowed into a bucket that pretends we understood it.
func fulfilmentWord(f string) string {
	switch strings.ToUpper(strings.TrimSpace(f)) {
	case "PICKUP":
		return "Customer pickup"
	case "DELIVERY":
		return "Delivery"
	case "WILL_CALL":
		return "Will call"
	case "":
		return "-"
	default:
		return f
	}
}

// -- customer.quotes ----------------------------------------------------------------------------

func runCustomerQuotes(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Quotes == nil {
		return Result{}, errUnwired("quotes")
	}
	rows, err := deps.Quotes.Quotes(ctx, c, a.get("customer"), strings.ToUpper(a.get("status")), maxItems)
	if err != nil {
		return Result{}, err
	}
	who := a.get("customer")
	forWhom := ""
	if who != "" {
		forWhom = " for " + who
	}
	if len(rows) == 0 {
		return Result{
			Command:  "customer.quotes",
			Headline: fmt.Sprintf("No quotes%s%s.", forWhom, statusClause(a.get("status"))),
			Empty:    true,
			Note:     "Nothing matches in the quotes you can see.",
		}, nil
	}
	var open int
	items := make([]Item, 0, len(rows))
	for _, q := range rows {
		if !q.Expired && strings.EqualFold(q.Status, "SENT") {
			open++
		}
		items = append(items, Item{
			Title: q.QuoteNo,
			Fields: []Field{
				{Label: "Customer", Value: q.CustomerName},
				{Label: "State", Value: quoteState(q)},
				{Label: "Total", Value: money(q.TotalCents)},
				{Label: "Valid until", Value: validUntilWord(q.ValidUntil)},
				{Label: "Updated", Value: day(q.UpdatedAt)},
			},
		})
	}
	res := Result{
		Command: "customer.quotes",
		Headline: fmt.Sprintf("%d %s%s, %d still open.",
			len(rows), plural(len(rows), "quote", "quotes"), forWhom, open),
		Items: items,
	}
	if len(rows) == 1 {
		res.Ref = refNamed(RefQuote, rows[0].QuoteID, rows[0].QuoteNo)
	}
	return res, nil
}

// quoteState says the ONE true thing about a quote's state. Expired is computed by the quote module
// at read time and never stored, so this reads it rather than deriving it from a date here, two
// definitions of "expired" is how a screen and an assistant come to disagree about a live quote.
func quoteState(q QuoteLine) string {
	if q.Expired {
		return strings.ToUpper(q.Status) + ", aged out"
	}
	return strings.ToUpper(q.Status)
}

// validUntilWord renders an ABSENT expiry as what it means. "" in that column means the quote NEVER
// expires (D2a); printing an em-dash would leave the reader to guess, and the guess people make is
// "expired".
func validUntilWord(v string) string {
	if strings.TrimSpace(v) == "" {
		return "No expiry"
	}
	return v
}

// -- quote.holds --------------------------------------------------------------------------------

func runQuoteHolds(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Quotes == nil {
		return Result{}, errUnwired("quote stock holds")
	}
	ref := a.get("quote_no")
	q, holds, found, err := deps.Quotes.QuoteHolds(ctx, c, ref)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{
			Command:  "quote.holds",
			Headline: fmt.Sprintf("I can't find a quote numbered %s.", ref),
			Empty:    true,
			Note:     "Check the number, or it may belong to a branch you don't have access to.",
		}, nil
	}
	if len(holds) == 0 {
		return Result{
			Command:  "quote.holds",
			Headline: fmt.Sprintf("%s isn't holding any stock.", q.QuoteNo),
			Empty:    true,
			// The two reasons are different and a rep needs to know which. Saying both is honest and
			// short; saying neither invites a phone call to the yard.
			Note: "Either its lines have no soft holds, or the holds have already lapsed.",
			Ref:  refNamed(RefQuote, q.QuoteID, q.QuoteNo),
		}, nil
	}
	items := make([]Item, 0, len(holds))
	for _, h := range holds {
		if len(items) >= maxItems {
			break
		}
		fields := []Field{
			{Label: "Held", Value: qtyUOM(h.QtyBase, h.UOM)},
			{Label: "Branch", Value: h.BranchName},
			{Label: "Status", Value: h.Status},
		}
		if h.ExpiresAt != "" {
			fields = append(fields, Field{Label: "Lapses", Value: h.ExpiresAt})
		}
		items = append(items, Item{Title: productTitle(h.ProductName, h.SKU), Fields: fields})
	}
	return Result{
		Command: "quote.holds",
		Headline: fmt.Sprintf("%s is holding stock on %d %s.",
			q.QuoteNo, len(holds), plural(len(holds), "line", "lines")),
		Items: items,
		Ref:   refNamed(RefQuote, q.QuoteID, q.QuoteNo),
	}, nil
}

// labelForHit names a catalogue hit the way a person recognises it.
func labelForHit(h CatalogueHit) string { return productTitle(h.Name, h.SKU) }

func productTitle(name, sku string) string {
	switch {
	case name != "" && sku != "":
		return name + " (" + sku + ")"
	case name != "":
		return name
	default:
		return sku
	}
}

// -- stock.incoming -----------------------------------------------------------------------------

// runStockIncoming is DIB-472's payoff. The promised-arrival date on a transfer is now REAL DATA -
// a stored promise somebody looked at when they shipped it, so this answer can say "expected Thu 20
// Aug" where before it could only say "shipped Monday".
//
// AND IT STILL NEVER INVENTS ONE. A leg with no promise reports its ship date and says the arrival
// is not promised. Deriving an ETA from a ship date plus a guessed transit time would put a date on
// a screen that nothing in the database supports, and a person plans around a date.
func runStockIncoming(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Incoming == nil {
		return Result{}, errUnwired("incoming stock")
	}
	ref := a.get("product")
	p, rows, found, err := deps.Incoming.Incoming(ctx, c, ref)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{
			Command:  "stock.incoming",
			Headline: fmt.Sprintf("I can't find a product matching %q.", ref),
			Empty:    true,
			Note:     "Try the SKU exactly as it appears in the catalogue.",
		}, nil
	}
	name := productLabel(p, ref)
	into := ""
	// The branch narrowing is a FILTER over yards the caller can already reach, the same shape as
	// stock.lookup's, and for the same reason: the read decided reachability, this only chooses.
	if want := a.get("branch"); want != "" {
		into = " into " + want
		kept := rows[:0:0]
		for _, l := range rows {
			if branchMatches(l.ToBranch, want) {
				kept = append(kept, l)
			}
		}
		rows = kept
	}
	if len(rows) == 0 {
		return Result{
			Command:  "stock.incoming",
			Headline: fmt.Sprintf("Nothing is on the way for %s%s.", name, into),
			Empty:    true,
			Note:     "No open purchase order and no transfer in flight to the yards you can see.",
			Ref:      refNamed(RefProduct, p.ProductID, name),
		}, nil
	}
	var total float64
	var soonest string
	undated := 0
	items := make([]Item, 0, len(rows))
	for _, l := range rows {
		total += l.QtyBase
		if l.ExpectedAt == "" {
			undated++
		} else if soonest == "" || l.ExpectedAt < soonest {
			soonest = l.ExpectedAt
		}
		if len(items) >= maxItems {
			continue
		}
		fields := []Field{
			{Label: "Quantity", Value: qtyUOM(l.QtyBase, l.UOM)},
			{Label: "Into", Value: l.ToBranch},
			{Label: "From", Value: l.From},
			{Label: "Expected", Value: expectedWord(l)},
		}
		if l.Status != "" {
			fields = append(fields, Field{Label: "Status", Value: l.Status})
		}
		items = append(items, Item{Title: sourceWord(l.Source) + " " + l.Reference, Fields: fields})
	}
	uom := rows[0].UOM
	head := fmt.Sprintf("%s on the way for %s, across %d %s.",
		qtyUOM(total, uom), name, len(rows), plural(len(rows), "consignment", "consignments"))
	if soonest != "" {
		head = fmt.Sprintf("%s on the way for %s, the first lands %s.", qtyUOM(total, uom), name, soonest)
	}
	res := Result{Command: "stock.incoming", Headline: head, Items: items, Ref: refNamed(RefProduct, p.ProductID, name)}
	notes := []string{}
	if undated > 0 {
		notes = append(notes, fmt.Sprintf("%d of these %s no promised arrival date, I've shown what is known instead of guessing one.",
			undated, plural(undated, "has", "have")))
	}
	if len(rows) > maxItems {
		notes = append(notes, fmt.Sprintf("Showing the first %d of %d.", maxItems, len(rows)))
	}
	res.Note = strings.Join(notes, " ")
	return res, nil
}

// expectedWord is the honest per-line date sentence. Three states, three sentences, and the middle
// one is the one DIB-472 made possible.
func expectedWord(l IncomingLine) string {
	switch {
	case l.ExpectedAt != "":
		return l.ExpectedAt
	case l.ShippedAt != "":
		return "not promised, shipped " + l.ShippedAt
	default:
		return "not promised"
	}
}

func sourceWord(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PO":
		return "PO"
	case "TRANSFER":
		return "Transfer"
	default:
		return s
	}
}

// -- product.find -------------------------------------------------------------------------------

func runProductFind(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Catalogue == nil {
		return Result{}, errUnwired("the catalogue")
	}
	term := a.get("product")
	hits, err := deps.Catalogue.FindProducts(ctx, c, term, maxItems)
	if err != nil {
		return Result{}, err
	}
	if len(hits) == 0 {
		return Result{
			Command:  "product.find",
			Headline: fmt.Sprintf("Nothing in the catalogue matches %q.", term),
			Empty:    true,
			Note:     "Try fewer words, or the SKU as it appears on the pallet label.",
		}, nil
	}
	items := make([]Item, 0, len(hits))
	for _, h := range hits {
		fields := []Field{
			{Label: "SKU", Value: h.SKU},
			{Label: "Sold in", Value: h.BaseUOM},
		}
		if h.MatchReason != "" {
			fields = append(fields, Field{Label: "Matched on", Value: h.MatchReason})
		}
		items = append(items, Item{Title: h.Name, Fields: fields})
	}
	res := Result{
		Command: "product.find",
		Headline: fmt.Sprintf("%d %s match %q.",
			len(hits), plural(len(hits), "product", "products"), term),
		Items: items,
	}
	if len(hits) == 1 {
		res.Headline = fmt.Sprintf("%s (%s) is the only match for %q.", hits[0].Name, hits[0].SKU, term)
		res.Ref = refNamed(RefProduct, hits[0].ProductID, labelForHit(hits[0]))
	}
	return res, nil
}

// -- product.price ------------------------------------------------------------------------------

// runProductPrice answers through the REAL resolver in preview mode: the same waterfall a quote line
// runs, so the number the assistant says is the number the quote will print.
//
// A LIST PRICE WOULD HAVE BEEN EASIER AND WRONG. Most accounts here do not pay list, contract
// pricing, customer discounts and promotions all move it, and a rep who repeats a list price on the
// phone has quoted a number the system will not honour.
func runProductPrice(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Price == nil {
		return Result{}, errUnwired("customer pricing")
	}
	q, found, err := deps.Price.PriceFor(ctx, c, a.get("product"), a.get("customer"), a.get("branch"))
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{
			Command:  "product.price",
			Headline: fmt.Sprintf("I couldn't price %q for %q.", a.get("product"), a.get("customer")),
			Empty:    true,
			Note:     "Either the product or the account didn't resolve, try the SKU and the account number.",
		}, nil
	}
	fields := []Field{
		{Label: "Price", Value: money(q.FinalCents) + " / " + q.PriceUOM},
		{Label: "List", Value: money(q.OriginalCents) + " / " + q.PriceUOM},
		{Label: "Why", Value: priceReason(q)},
	}
	if q.BranchName != "" {
		fields = append(fields, Field{Label: "Branch", Value: q.BranchName})
	}
	if q.Clearance {
		fields = append(fields, Field{Label: "Clearance", Value: "Yes"})
	}
	head := fmt.Sprintf("%s pays %s per %s for %s.",
		q.CustomerName, money(q.FinalCents), q.PriceUOM, productTitle(q.Name, q.SKU))
	if q.FinalCents == q.OriginalCents {
		head = fmt.Sprintf("%s pays list, %s per %s for %s.",
			q.CustomerName, money(q.FinalCents), q.PriceUOM, productTitle(q.Name, q.SKU))
	}
	return Result{
		Command:  "product.price",
		Headline: head,
		Items:    []Item{{Title: productTitle(q.Name, q.SKU), Fields: fields}},
		Note:     "Priced exactly as a quote line would be, nothing was written.",
		Ref:      refNamed(RefProduct, q.ProductID, productTitle(q.Name, q.SKU)),
	}, nil
}

// priceReason is the resolver's OWN explanation, never one composed here. If the waterfall changes,
// the sentence changes with it, because it is the waterfall's sentence.
func priceReason(q PriceQuote) string {
	parts := []string{}
	if q.Source != "" {
		parts = append(parts, q.Source)
	}
	if q.PromoName != "" {
		parts = append(parts, q.PromoName)
	}
	if q.Detail != "" {
		parts = append(parts, q.Detail)
	}
	if len(parts) == 0 {
		return "the standard price"
	}
	return strings.Join(parts, ", ")
}

// -- shared: resolving the account, once --------------------------------------------------------

// accountOutcome is "resolved, or here is the answer to give instead". It exists so the three
// account commands cannot each invent their own wording for "no such customer", DIB-443's whole
// finding was one situation described three different ways depending on the surface.
type accountOutcome struct {
	found  bool
	result Result
}

func resolveAccount(ctx context.Context, deps Deps, c Caller, ref, code string) (CustomerRecord, accountOutcome, error) {
	if strings.TrimSpace(ref) == "" {
		return CustomerRecord{}, accountOutcome{}, badArg("Which customer? A name or account number works.")
	}
	cust, found, err := deps.Customers.ResolveCustomer(ctx, c, ref)
	if err != nil {
		return CustomerRecord{}, accountOutcome{}, err
	}
	if !found {
		return CustomerRecord{}, accountOutcome{found: false, result: notFoundCustomer(code, ref)}, nil
	}
	return cust, accountOutcome{found: true}, nil
}
