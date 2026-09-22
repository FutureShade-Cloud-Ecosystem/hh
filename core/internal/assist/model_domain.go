package assist

// model_domain.go, DIB-478. The read seams the DEEP command set needs, declared the same way
// model.go declares the original five: in the language of the QUESTION, never in the language of
// the module that happens to answer it today, and every method carrying the asking user's Caller as
// a REQUIRED argument so an adapter cannot be written that forgets to scope the read.
//
// WHY A SECOND FILE RATHER THAN A LONGER model.go. model.go is the pilot's contract and it is read
// as a whole by anyone auditing what the assistant can reach. Eleven more interfaces in it would
// bury the five-line security argument at the top. The rules are identical and stated once, there;
// this file adds vocabulary, not policy.
//
// THE ONE NEW RULE THIS FILE INTRODUCES, AND IT IS A NEGATIVE ONE
// --------------------------------------------------------------
// Not one seam below is a WRITER, and not one of them takes a tx. DIB-478's ruling is read-only -
// action powers wait for a separate client session, and that has to be structural rather than
// promised: there is no method here that could mutate anything, so "the deeper assistant still
// cannot change your data" is a statement about what the code can reach.

import (
	"context"
	"time"
)

// -- payments: the day, and the range -----------------------------------------------------------

// DayTotal is one day's payment figure as the ROLLUP states it, the count and total the #payments
// channel is literally showing, not a recomputation over the payments table.
//
// Channels is how many payments channels contributed a line. Zero is a real answer ("it is 9am") and
// the command says so in prose rather than painting an em-dash.
type DayTotal struct {
	Day      string
	Count    int
	Cents    int64
	Channels int
}

// RollupReader is the messaging rollup, read as the ONE source for "what came in today".
//
// WHY THIS AND NOT A SUM OVER PAYMENTS. messaging/payments_today.go spends thirty lines on the
// argument and it is the same argument here: a tile, a channel line and an assistant answer that
// disagree about today's money do not produce a question about the assistant, they produce a person
// who stops believing all three. So the assistant reads the rollup ROOT, exactly as the dashboard
// tile does, and there is no second definition of the day.
type RollupReader interface {
	PaymentsToday(ctx context.Context, c Caller, at time.Time) (DayTotal, error)
}

// PaymentDay is one day of the CASH RECEIPTS report, a different, separately named question from
// the rollup's day line, and the commands say which one they answered.
type PaymentDay struct {
	Day    string
	Count  int
	Cents  int64
	Branch string
}

// PaymentRangeQuery bounds a historical payments read. From/To are YYYY-MM-DD inclusive; BranchID
// narrows within the caller's reach and is refused by the report service when it is outside it.
type PaymentRangeQuery struct {
	From     string
	To       string
	BranchID string
}

// PaymentRangeReader answers "what came in between these two dates".
//
// It is the reporting module's own cash-receipts template, RECEIVED and DEPOSITED only, because a
// BOUNCED or VOID payment is money that did not stay. The assistant does not re-state that rule; it
// asks the module that owns it.
type PaymentRangeReader interface {
	PaymentsByDay(ctx context.Context, c Caller, q PaymentRangeQuery) ([]PaymentDay, error)
}

// -- AR: the aging worklist, and who is stopped -------------------------------------------------

// AgingAccount is one account's AR position across the policy's buckets.
type AgingAccount struct {
	CustomerID   string
	CustomerName string
	AccountNo    string
	BranchID     string
	CurrentCents int64
	Days30Cents  int64
	Days60Cents  int64
	Days90Cents  int64
	TotalCents   int64
	OnHold       bool
	HoldKind     string
}

// HoldRecord is one credit hold, with the REASON attached, "who is on credit hold" is half a
// question and the half nobody can act on.
type HoldRecord struct {
	CustomerID     string
	CustomerName   string
	Status         string
	Kind           string
	AutoPlaced     bool
	Reason         string
	OverrideUntil  time.Time
	OverrideReason string
	PlacedAt       time.Time
}

// ARReader answers the two credit-desk questions as the asking user.
//
// Aging is branch-fenced by the adapter to the caller's own reach. Holds is fenced THROUGH the
// aging read, credit_holds carries no branch of its own, and the adapter reports honestly, via
// Scoped, whether it had to narrow. An answer that quietly dropped rows would be worse than one
// that says which yards it looked at.
type ARReader interface {
	Aging(ctx context.Context, c Caller, asOf time.Time) ([]AgingAccount, error)
	Holds(ctx context.Context, c Caller) (rows []HoldRecord, scoped bool, err error)
}

// -- invoices -----------------------------------------------------------------------------------

// InvoiceRecord is one invoice's headline facts.
type InvoiceRecord struct {
	InvoiceID    string
	InvoiceNo    string
	Status       string
	IssueDate    time.Time
	DueDate      time.Time
	TotalCents   int64
	BalanceCents int64
}

// InvoiceReader lists one account's invoices. The account is resolved FIRST, through the same
// caller-fenced customer lookup every other account question uses, so this read is unreachable
// without a resolve that the caller was allowed to make.
type InvoiceReader interface {
	InvoicesFor(ctx context.Context, c Caller, customerID, status string, limit int) ([]InvoiceRecord, error)
}

// -- orders: the list, and how each one is going out --------------------------------------------

// OrderLine is one order in a customer's list: its state and how it leaves the yard.
type OrderLine struct {
	OrderID     string
	OrderNo     string
	Status      string
	Fulfillment string // PICKUP | DELIVERY | WILL_CALL
	Requested   time.Time
	TotalCents  int64
}

// OrderListReader lists an account's orders, newest first.
type OrderListReader interface {
	OrdersFor(ctx context.Context, c Caller, customerID, status string, limit int) ([]OrderLine, error)
}

// -- quotes: the list, the state, and the stock they are holding --------------------------------

// QuoteLine is one quote in a list: what state it is in, and whether it has aged out.
//
// Expired is COMPUTED by the quote module at read time and carried through untouched. There is no
// EXPIRED status in that schema (D2) and an assistant that invented one would be describing a state
// the app cannot show.
type QuoteLine struct {
	QuoteID      string
	QuoteNo      string
	CustomerName string
	Status       string
	ValidUntil   string // "" means NEVER EXPIRES, never "expired"
	Expired      bool
	TotalCents   int64
	UpdatedAt    time.Time
}

// QuoteHoldLine is one soft hold a quote is placing on stock.
type QuoteHoldLine struct {
	ProductName string
	SKU         string
	BranchName  string
	QtyBase     float64
	UOM         string
	Status      string
	// ExpiresAt is YYYY-MM-DDTHH:MM:SSZ as the quote module stores it, and "" when the hold does not
	// lapse. A STRING because that is what that module returns: parsing it here to re-render it would
	// put a second date format in the path of a date the quote screen already prints.
	ExpiresAt string
}

// QuoteReader answers the two quote questions. Both are fenced by the quote module's own
// caller resolve, the same fence the Quote Builder screen enforces.
type QuoteReader interface {
	Quotes(ctx context.Context, c Caller, customerRef, status string, limit int) ([]QuoteLine, error)
	QuoteHolds(ctx context.Context, c Caller, quoteRef string) (q QuoteLine, holds []QuoteHoldLine, found bool, err error)
}

// -- incoming: what is on the way, and when it is promised --------------------------------------

// IncomingLine is one leg of stock heading toward a yard the caller can reach.
//
// Source is 'PO' or 'TRANSFER'. ExpectedAt is YYYY-MM-DD and is EMPTY when nothing in the database
// promises a date, DIB-472's rule, carried across the seam without a guess. A consumer that filled
// it in would be putting a date on a screen that nothing supports, and a person plans around a date.
type IncomingLine struct {
	Source     string
	Reference  string // PO number, or transfer number
	From       string // vendor name, or the shipping branch
	ToBranch   string
	QtyBase    float64
	UOM        string
	ExpectedAt string
	ShippedAt  string
	Status     string
}

// IncomingReader answers "when is more arriving?" for one product, across both legs.
//
// found=false means the product reference did not resolve; an empty slice means nothing is on the
// way, which is an ordinary answer and is said as one.
type IncomingReader interface {
	Incoming(ctx context.Context, c Caller, productRef string) (p ProductRef, rows []IncomingLine, found bool, err error)
}

// -- catalogue and price ------------------------------------------------------------------------

// CatalogueHit is one product the catalogue matched, with the counter's OWN reason for matching it.
type CatalogueHit struct {
	ProductID   string
	SKU         string
	Name        string
	BaseUOM     string
	MatchReason string
}

// PriceQuote is the resolver's answer for one product, one customer, one branch, the REAL
// waterfall, not a list price.
//
// Source and Detail are the resolver's own words for WHY the number is what it is (a contract price,
// a promotion, a customer discount). An assistant that printed the number without the reason would
// be handing a rep a figure they cannot defend on the phone.
type PriceQuote struct {
	ProductID     string
	SKU           string
	Name          string
	CustomerName  string
	BranchName    string
	OriginalCents int64
	FinalCents    int64
	DiscountBps   int64
	PriceUOM      string
	Source        string
	Detail        string
	Clearance     bool
	PromoName     string
}

// CatalogueReader is the catalogue's ranked search, the SAME search the counter uses, so the
// assistant and the POS agree about what "Blu 60" means. It is also what the conversation resolver
// asks when a term might mean two products (resolver.go).
type CatalogueReader interface {
	FindProducts(ctx context.Context, c Caller, term string, limit int) ([]CatalogueHit, error)
}

// PriceReader answers "what does this cost THIS customer" through the real resolver, in PREVIEW
// mode: it resolves exactly as a quote line would and writes no audit row, because an assistant
// asking a question is not a document being priced.
type PriceReader interface {
	PriceFor(ctx context.Context, c Caller, productRef, customerRef, branchRef string) (q PriceQuote, found bool, err error)
}

// -- branches: the vocabulary a conversation resolves against -----------------------------------

// NamedBranch is one branch the caller can reach, by the name a person says.
type NamedBranch struct {
	BranchID string
	Code     string
	Name     string
}

// BranchReader lists the branches THIS caller may ask about. It exists for the resolver: "what
// about Kingston?" is only a branch if Kingston is a yard this person can see, and the alternative -
// a hard-coded list, or trusting the word because it looks like a place, is how a scoped user
// learns the names of yards they were not granted.
type BranchReader interface {
	Branches(ctx context.Context, c Caller) ([]NamedBranch, error)
}
