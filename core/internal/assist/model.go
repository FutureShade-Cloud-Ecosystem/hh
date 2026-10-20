// Package assist is TanyaAI: the in-app assistant, built as two halves that can be reasoned about
// separately and shipped separately.
//
// HALF B, THE STRUCTURED COMMAND LAYER (commands.go, registry.go). A registry of deterministic
// READ commands: find a payment, find an order, check stock, check a customer's balance or recent
// activity. No model, no network, no keys. Given the same question and the same data it returns the
// same answer forever, and it keeps working on the day the AI is switched off, the vendor changes
// its pricing, or the internet is down at the yard. Every command is READ-ONLY in this pilot;
// nothing in this package writes to another domain's tables.
//
// HALF C, THE CHAT PILOT (provider.go, stub_provider.go, anthropic_provider.go, service.go). A
// conversational surface whose ONLY power is to invoke the commands from half B. The assistant does
// not query the database, does not hold a connection, and cannot reach a table that is not behind a
// registered command. It composes sentences around results it was handed.
//
// THE ORDER OF THE TWO HALVES IS THE DESIGN. Half B is the product; half C is a nicer way to type
// into it. Written the other way round, a model with database access, and structure added later -
// every question about what the assistant can see becomes a question about what a model chose to
// do, and there is no answer that survives an auditor.
//
// THE INVARIANT THIS PACKAGE EXISTS TO HOLD
// -----------------------------------------
//
//	A command can never show a user something the app itself would not show them.
//
// Which is held by never having a second path to the data. Commands call the SAME read services the
// screens call, through the consumer interfaces below, carrying the asking user's Caller. There is
// no service account, no elevated context, and no cache keyed on anything but the request in hand -
// so a yard hand asking about a branch they cannot reach gets exactly what the yard console would
// give them, because it is the yard console's own answer.
//
// HOW THE ASSISTANT READS DATA. The assistant reads data only through the assist logins (package
// readtx): two database roles, one for staff and one for contractors, that hold no privilege but
// the ai and ai_pro objects and the one function that verifies the signed claims of who is asking.
// Each tool call is one read only transaction on that login, with the asker's claims bound to the
// transaction's backend, rolled back at the end. It is never the app role, which owns every table
// and, on some boxes, is a superuser. A user's data reach is decided in the database by those
// claims, so a model that wrote its own SQL could still reach nothing the asker could not.
//
// WHAT THIS PACKAGE IMPORTS: nothing from another domain. The readers below are CONSUMER
// interfaces, declared here in the language of the questions being asked, and satisfied by thin
// adapters wired in main.go (see NOTES-FIX-431.md, PARK 1). That keeps the dependency arrow
// pointing one way, keeps this package testable against fakes, and means payments/order/inventory/
// customer never learn that an assistant exists.
package assist

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Caller is the asking user's authority, in the plain types this package speaks.
//
// It mirrors identity.StaffContext deliberately rather than importing it: the registry, the
// commands, the limits resolver and the ledger all handle a Caller and none of them should be able
// to reach an identity helper that RE-DERIVES authority. Authority is decided once, by the existing
// middleware, and travels here as a value that can only be read. handler.go performs the one
// conversion (the same seam internal/dashboard uses).
//
// Tier follows identity.Tier: 1 = leadership, 2 = manager, 3 = frontline, LOWER IS MORE AUTHORITY.
// Tier 0 means "no role assigned" and is refused everywhere, an unassigned account is not a
// least-privileged account, it is an account whose authority is unknown.
type Caller struct {
	UserID    string
	RoleCode  string
	Tier      int
	Global    bool     // identity.ScopeGlobal, reaches every branch
	BranchIDs []string // the caller's granted branches; ignored when Global
	IsAdmin   bool     // tier-1 GLOBAL: the tech-admin surfaces' gate
	// PaymentTrail is the role gate's answer to "does this caller hold the payment trail reads"
	// (ruling 87), set by the handler. False means the reads that list payments leave them out.
	PaymentTrail bool
	// CostBranchIDs are the branches whose cost this caller's own screens would show, and Margin whether it
	// sees margin: both decided by the handler from the dealer's cost and margin role list (the predicate the
	// inventory, quote, pricing, tasks and dispatch screens ask), never from the request. Empty and false
	// mean no cost and no margin.
	CostBranchIDs []string
	Margin        bool
	// Address is the connection's trusted address (the demo mint's rule); it keys the demo rate
	// limits and never grants anything.
	Address string
	// SessionKey is the verified bearer's jti: one value per sign in. It keys server kept
	// conversations (conversation_store.go) so two visitors on one persona user never share a
	// history. Empty when the token carries none. It is never an authority.
	SessionKey string
}

// HasRole reports whether the caller has any assigned role at all.
func (c Caller) HasRole() bool { return c.Tier >= 1 && c.Tier <= 3 }

// -- errors -------------------------------------------------------------------------------------
//
// Four named errors, because the four situations are genuinely different to the person reading the
// screen and mapping them all to "something went wrong" is how a typo gets reported as an outage
// (the DIB-443 lesson, one ticket earlier).

var (
	// ErrUnknownCommand: no command by that code. A client bug or a stale menu, never the user's
	// fault, and never a hint about which codes DO exist, the command list endpoint is the only
	// enumeration, and it is already filtered by authority.
	ErrUnknownCommand = errors.New("assist: unknown command")

	// ErrRefused: the command exists and the caller may not run it. Distinct from unknown so the
	// LOG can tell the two apart; the user-facing message for both is deliberately the same shape,
	// because "you are not allowed to run the command you have never heard of" is a way of
	// confirming it exists.
	ErrRefused = errors.New("assist: not permitted for this user")

	// ErrDisabled: an administrator switched this command off in assist_command_policy. Separate
	// from ErrRefused because the remedy is different, one is "ask for access", the other is
	// "this is off for everyone right now".
	ErrDisabled = errors.New("assist: command is switched off")

	// ErrOverBudget: the caller has spent their monthly token allowance. It is carried as an error
	// INSIDE the service and converted to an ordinary, friendly answer before it reaches the user
	// (see Service.Ask), the chat surface must never render a red box for this. Nothing is broken.
	ErrOverBudget = errors.New("assist: monthly AI limit reached")
)

// -- command results ----------------------------------------------------------------------------

// Field is one labelled value on a result. Values are pre-formatted strings: the command layer
// decides how money and quantities read ("$1,240.00", "18.5 t"), once, so the chat template and the
// structured panel and any future consumer cannot disagree about the same number.
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Item is one record in an answer, a payment, an order, a branch's stock line.
type Item struct {
	Title  string  `json:"title"`
	Fields []Field `json:"fields"`
	// Ref is the record this row is about (data.query rows), built by the server. Cite is the number the
	// model may write as [N] to cite it; the service assigns it per exchange and checks every [N] it reads.
	Ref  *Ref `json:"ref,omitempty"`
	Cite int  `json:"cite,omitempty"`
}

// Result is what a command returns: a plain-English headline plus the records behind it.
//
// WHY BOTH. The headline is what the chat speaks and what a screen reader hears first; the items
// are what the panel renders as a table and what a user copies out. Generating the sentence from
// the items at render time would put a second formatter in the frontend, and the two would drift.
//
// Empty is a FIRST-CLASS state, not an error and not an absence. "No payment matches that
// reference" is a true, useful answer; dressing it as a failure is the exact defect DIB-443 was
// filed for. Callers must not infer emptiness from len(Items), a command may legitimately return a
// headline with no items (a balance is one number), which is why the flag is explicit.
type Result struct {
	Command  string `json:"command"`
	Headline string `json:"headline"`
	Items    []Item `json:"items"`
	Empty    bool   `json:"empty"`
	// Note carries an honest caveat when there is one: a truncated list, a scope that narrowed the
	// search. Empty string means there is nothing to add, never a placeholder.
	Note string `json:"note,omitempty"`
	// Ref names the ONE record this answer is about, when there is exactly one. See Ref.
	Ref *Ref `json:"ref,omitempty"`
	// Notice and Scope ride up to the Answer (data.query).
	Notice *Notice `json:"notice,omitempty"`
	Scope  *Scope  `json:"scope,omitempty"`
}

// Ref is the record an answer is ABOUT, DIB-465's deep link, carried as data rather than as a URL.
//
// WHY NOT A URL. Routes are the frontend's business: the shell owns a frozen hash contract
// (#<unit>/<module>/<kind>:<id>, dashboard-nav.parseDeepLink) and it changes when screens move. A
// backend that emitted '#sales/catalogue/product:…' would be a second, un-enumerable copy of that
// contract living in Go, and the day a screen moved the assistant would start handing out links to
// nowhere with nothing to catch it. So this package answers the question it actually knows the
// answer to, WHICH RECORD, and app/src/_assist-links.ts turns that into a hash through the same
// table the audit drill-through uses, with a test that parses every link back through the shell's
// own parser.
//
// WHEN IT IS ABSENT, AND WHY THAT IS NOT A GAP. A payment lookup returns no Ref: this app has no
// per-payment surface to open (the AR console is a list, not a record view), and a link that lands
// somewhere the user then has to search is worse than no link, it teaches people that the
// assistant's links do not work. The DIB-402 rule, unchanged: never invent a target.
//
// Kind is the frontend's RefKind vocabulary ('product', 'order', 'customer', …) and ID is the
// record's uuid, never a display number. 'SO-1042' is not addressable; the id behind it is.
type Ref struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Label is what to CALL the record, "Techo-Bloc Blu 60 (TB-BLU60)", "Q-1042", "Northgate
	// Landscaping", so the link reads "Open Northgate Landscaping" rather than "Open customer
	// pricing" (the route table's own words, written for an audit log).
	//
	// DIB-478 ADDED IT BECAUSE THE FRONTEND WAS GUESSING. _assist-links.linkSubject read the name out
	// of the headline with per-command string surgery, which worked for five commands and became a
	// table of special cases at sixteen, and every special case is a label that silently goes wrong
	// when a headline is reworded. The server already knows exactly which record it is pointing at;
	// naming it is the same act as identifying it, so it does both in one place.
	//
	// Optional. An empty label leaves the frontend's fallbacks in charge, which is what every ref
	// built before this field existed does.
	Label string `json:"label,omitempty"`
}

// Ref kinds. Constants rather than literals at each call site, because the SPELLING is the contract
// with app/src/_audit-drilldown.ts REF_ROUTES, a typo here is a silently missing link, not an error.
const (
	RefProduct  = "product"
	RefOrder    = "order"
	RefCustomer = "customer"
	// RefQuote is DIB-478's addition, and it costs the frontend nothing: 'quote' has been a routable
	// kind in REF_ROUTES since W4-A (#sales/quotes, the staff Quote Builder). The spelling is the
	// contract, a typo here is a silently missing link, not an error, so it is a constant here for
	// the same reason the other three are.
	RefQuote = "quote"
)

// refTo builds a Ref, or nil when there is no addressable id. A half-ref (a kind with no id) would
// render as a link to nothing, so it is never constructed.
func refTo(kind, id string) *Ref {
	if id == "" {
		return nil
	}
	return &Ref{Kind: kind, ID: id}
}

// refNamed is refTo with the record's own name attached. Same nil rule: a half-ref renders as a link
// to nothing, so it is never constructed, and a label without an id is not a ref at all.
func refNamed(kind, id, label string) *Ref {
	r := refTo(kind, id)
	if r != nil {
		r.Label = strings.TrimSpace(label)
	}
	return r
}

// -- consumer interfaces: what this package needs from the rest of the app -----------------------
//
// Each of these is declared in terms of the QUESTION, not in terms of the domain package that
// happens to answer it today. That is what makes the adapters thin and replaceable, and it is what
// lets every test in this package run against a fake in memory with no database and no import of
// payments/order/inventory/customer.
//
// EVERY METHOD TAKES A Caller. Not a context with a caller buried in it, and not an option, a
// required argument, so an adapter physically cannot be written that forgets to scope the read. The
// adapters in PARK 1 pass it straight into the existing services' own scoping.

// PaymentQuery is "find me that payment" in the three shapes a person actually asks it.
//
// Reference matches the payment number or the gateway reference. AmountCents finds a payment for an
// exact amount (the cheque on the desk). CustomerRef narrows to one account, and may be combined
// with either of the others. All three empty is refused by the command before it reaches an adapter
// , an unfiltered payment list is a report, not a lookup, and it belongs on the AR console where it
// can be paged.
type PaymentQuery struct {
	Reference   string
	AmountCents int64
	CustomerRef string
	Limit       int
}

// PaymentRecord is one payment, already scoped to what the caller may see.
type PaymentRecord struct {
	PaymentNo    string
	Reference    string
	CustomerName string
	BranchName   string
	Method       string
	Status       string
	AmountCents  int64
	ReceivedAt   time.Time
}

// PaymentReader answers payment lookups as the asking user.
type PaymentReader interface {
	FindPayments(ctx context.Context, c Caller, q PaymentQuery) ([]PaymentRecord, error)
}

// OrderRecord is one order's headline facts, the answer to "where is #SO-4?".
type OrderRecord struct {
	// OrderID is the record's uuid, what a deep link addresses (DIB-465). OrderNo is what a person
	// says; only one of the two is routable, and it is not the one they say.
	OrderID      string
	OrderNo      string
	Status       string
	CustomerName string
	BranchName   string
	TotalCents   int64
	PlacedAt     time.Time
	LineCount    int
}

// OrderReader resolves an order by its visible number as the asking user. The bool is "found",
// distinct from an error: an order number that does not exist is an answer, not a fault.
type OrderReader interface {
	OrderByNumber(ctx context.Context, c Caller, orderNo string) (OrderRecord, bool, error)
}

// ProductRef identifies the product a stock question resolved to, so the answer can name it back
// ("Techo-Bloc Blu 60 (TB-BLU60)") instead of echoing the id the user typed.
type ProductRef struct {
	ProductID string
	SKU       string
	Name      string
}

// StockRecord is one branch's position for a product. Available is the SERVER'S number, carried
// through untouched, this package does not recompute on_hand - reserved, for the same reason the
// stock surface does not (one owner per arithmetic).
type StockRecord struct {
	BranchName string
	OnHand     float64
	Reserved   float64
	Available  float64
	UOM        string
}

// StockReader answers "how many have we got" as the asking user. found=false means the product
// reference did not resolve at all; a resolved product with an empty slice means it has no stock
// history, which is an ordinary state and must be reported as one.
type StockReader interface {
	StockFor(ctx context.Context, c Caller, productRef string) (p ProductRef, rows []StockRecord, found bool, err error)
}

// CustomerRecord identifies a customer account.
type CustomerRecord struct {
	CustomerID string
	Name       string
	AccountNo  string
}

// BalanceRecord is a customer's AR position: what is open, and how much of it is late.
type BalanceRecord struct {
	OpenCents    int64
	OverdueCents int64
	AsOf         time.Time
}

// ActivityRecord is one line of "what has happened on this account lately", an order, a payment,
// an invoice. Kind is the human word for the row ("Order", "Payment").
type ActivityRecord struct {
	Kind      string
	Reference string
	When      time.Time
	Cents     int64
	Status    string
}

// CustomerReader answers account questions as the asking user. Resolve is separate from the two
// reads so that "no such customer" is answered once, in one voice, rather than as two different
// empty results depending on which question was asked.
type CustomerReader interface {
	ResolveCustomer(ctx context.Context, c Caller, ref string) (CustomerRecord, bool, error)
	Balance(ctx context.Context, c Caller, customerID string) (BalanceRecord, error)
	RecentActivity(ctx context.Context, c Caller, customerID string, limit int) ([]ActivityRecord, error)
}
