package pos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// -- DIB-165: past-sale / till-session lookup -----------------------------------------------------
//
// The reprint path (GET /api/v1/pos/sales/{id}/receipt, printing module) has always worked, provided
// you already knew the sale id. At the counter nobody does. The customer says "I was in Tuesday", or
// hands over a receipt with an order number on it, or the cashier needs to find the sale they rang
// twenty minutes ago on this shift. This is the search that turns those into an id.
//
// TWO SCOPES, ONE QUERY. "This till session" is the shift-recall case (what did I ring today, on this
// drawer) and "this branch's history" is the customer-recall case. They differ only by whether
// till_session_id narrows the result, so they are one endpoint with an optional filter rather than
// two surfaces that will drift.
//
// THE FENCE IS THE SAME ONE THE SALE ITSELF USES. A sale is branch-stamped at ring; a scoped caller
// may search its own branches and no others, and may not omit the branch to sweep the company. That
// is the landedcost List posture (403 for a scoped caller who names no branch, 403 for one who names
// a branch it cannot reach), applied here because the data is the same kind of data: branch money.

// SaleSummary is one row of the past-sale list, enough to recognise a sale, not enough to be a
// receipt. No line detail, no evidence, no card data.
type SaleSummary struct {
	SaleID        string `json:"sale_id"`
	OrderID       string `json:"order_id"`
	OrderNo       string `json:"order_no"`
	InvoiceID     string `json:"invoice_id,omitempty"`
	InvoiceNo     string `json:"invoice_no,omitempty"`
	CustomerID    string `json:"customer_id"`
	CustomerName  string `json:"customer_name"`
	BranchID      string `json:"branch_id"`
	TillSessionID string `json:"till_session_id,omitempty"`
	SaleType      string `json:"sale_type"`
	Tender        string `json:"tender"`
	SplitTender   bool   `json:"split_tender"`
	TotalCents    int64  `json:"total_cents"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
	LineCount     int    `json:"line_count"`
}

// SaleLookupQuery is the search the counter runs.
type SaleLookupQuery struct {
	BranchID      string // required for a scoped caller (403 otherwise); optional for GLOBAL
	TillSessionID string // narrows to one drawer session, the "what did I ring on this shift" case
	Q             string // order_no | invoice_no | customer name | account_no (case-insensitive contains)
	From          string // inclusive ISO date (YYYY-MM-DD) on created_at
	To            string // inclusive ISO date
	Limit         int
}

const (
	defaultSaleLookupLimit = 25
	maxSaleLookupLimit     = 100
)

var errLookupForbidden = clientError{status: http.StatusForbidden,
	msg: "pos: caller cannot search sales for that branch"}

// A date the counter typed that is not a date is REFUSED, not ignored. Dropping an unreadable bound
// would widen the search silently, the cashier sees a longer list, assumes it is the day they
// asked for, and reads the wrong row back to the customer.
var errLookupBadDate = clientError{status: http.StatusBadRequest,
	msg: "pos: the from and to dates must be written as YYYY-MM-DD"}

// -- DIB-165 · THE LAST GREENWICH DAY IN THIS SEARCH ---------------------------------------------
//
// -- THE DEFECT ----------------------------------------------------------------------------------
//
// The two date bounds in Repository.LookupSales were written as `$N::date` against `ps.created_at`,
// and `ps.created_at` is TIMESTAMPTZ (0029_pos_counter_sales.sql:23). Postgres promotes a `date` to
// a `timestamptz` using the SESSION's TimeZone, and this product sets none: platform/db/db.go:14 is
// a bare `pgxpool.New(ctx, url)` with no `search_path`, no `TimeZone`, nothing. Every connection
// therefore runs at UTC, and `'2026-08-05'::date` did not mean midnight at the counter. It meant
// 2026-08-04 20:00 EDT, 8pm the night before.
//
// BOTH BOUNDS WERE WRONG, IN OPPOSITE DIRECTIONS, which is exactly why neither was caught by eye:
//
//	from=2026-08-05   INCLUDED the four hours of 4 August after 8pm. A cashier asking "what did we
//	                  ring on the 5th" was handed last night's closing sales as well.
//	to=2026-08-05     EXCLUDED the four hours of 5 August after 8pm, that evening's own sales. That
//	                  is the precise opposite of what the comment on that clause promised, which was
//	                  "a search 'to 2026-08-05' must include a sale rung at 16:40 that day". At 16:40
//	                  it was true. At 20:40 it was false, and on a long weekend 20:40 is the busy
//	                  hour. A customer who came in Saturday evening was invisible to a Saturday
//	                  search and present in a Sunday one, and the counter had no way to tell.
//
// A search that quietly returns the wrong day is worse than one that errors: the cashier concludes
// the sale was never rung and re-rings it, or tells the customer their receipt is not ours.
//
// -- THE RULING: RESOLVE THE DAY IN GO, BIND AN INSTANT ------------------------------------------
//
// dashboard/window.go:46-53 already ruled on this shape one module over: bounds compared against a
// TIMESTAMPTZ column are HALF-OPEN [since, until), because an inclusive end has to be written as
// "< the next midnight" anyway and writing it as "<= 23:59:59" loses every row in the last second of
// the day. Line 141's `< ($N::date + INTERVAL '1 day')` was already that shape. It stays that shape.
// The only thing that changes is WHERE the midnight comes from: Go, in the house zone, bound as a
// parameter, not Postgres, from a cast, in whatever zone the session happens to hold.
//
// WHY NOT `AT TIME ZONE 'America/Toronto'` IN THE SQL. It would work, and it is the wrong place for
// the decision. There is no such string anywhere in this repository's SQL, so it would be a new
// precedent set silently; and SQL inside a Go string literal is a place no drift gate can read, so
// the sixth copy of the house zone would be the one copy that could rot without anything failing.
// testaudit/house_day_test.go now scans for exactly this shape, and dashboard/window_test.go's
// TestHouseZone_EveryCopyInTheBackendNamesTheSameZone enumerates the constants. Both of those read
// Go. Neither reads a query. The zone belongs where the gates can see it.
//
// NOT TOUCHED: the `to_char(ps.created_at AT TIME ZONE 'UTC', …)` in the SELECT list below. That is
// the WIRE FORMAT, an RFC3339 instant with a Z on it, which is what the browser is handed and what
// thirteen other sites in this tree emit. It is not a day boundary and it must stay UTC.
//
// -- WHY A SIXTH COPY OF THE ZONE AND NOT AN IMPORT ----------------------------------------------
//
// gl/businessday.go:42-47 argues this in writing and the argument is not repeated here, only
// applied: "a shared constant would couple the ledger to the message board over a fact they merely
// agree about. Three copies that agree is the house posture." There were three when that was
// written. There are now five, messaging/rollup.go:50, inventory/transfer_eta.go:59,
// messagingprovider/policy.go:40, gl/businessday.go:56, dashboard/window.go:32, and this is the
// sixth. The price of that posture is a gate that reads them all, and the price was going unpaid:
// window_test.go enumerated three of five by hand and had been blind to two live copies since the
// day dashboard/window.go declared its own. That test is now self-enumerating, so the seventh copy
// is checked the moment it is written rather than the day somebody remembers to add it to a map.

// houseZone is the dealer's own zone. Dibbits sells out of Ontario yards, and the day a cashier
// means when they type a date is the day that ends when the gate closes, not when Greenwich rolls.
const houseZone = "America/Toronto"

// houseLocation resolves the house zone, degrading to UTC rather than failing the search if the
// image ships no tzdata, the same trade gl/businessday.go:58-69 makes and for the same reason: a
// lookup that returns the wrong four hours is a defect somebody can see and work around, and a
// lookup that will not run at all is a counter that cannot find a customer's receipt. Nothing in
// this repository imports time/tzdata, so a distroless build takes this arm silently; cmd/server's
// TestTZData_TheHouseZoneResolves is where that gets caught before a counter finds it.
func houseLocation() *time.Location {
	loc, err := time.LoadLocation(houseZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// houseMidnight turns the YYYY-MM-DD a cashier typed into the instant that civil day BEGINS at the
// yard. It is built with time.Date on the civil date rather than time.ParseInLocation so the
// construction is the same one dashboard/window.go:100-102 uses, and so the reader can see that the
// location is chosen and not inherited.
//
// DST needs no special case: America/Toronto transitions at 02:00, so local midnight is
// single-valued for every civil date. That is window.go:87-93's argument and it holds here.
func houseMidnight(day string) (time.Time, bool) {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, houseLocation()), true
}

// saleLookupBounds resolves the query's From/To into the half-open instant pair the SQL binds. A
// zero time means "this bound was not asked for", the query is open on that side.
//
// ONE FUNCTION, TWO CALLERS, ON PURPOSE. The service calls it to REFUSE a date it cannot read, and
// the repository calls it to BUILD the predicate. If they resolved the day separately, a future
// change to one would move the boundary without moving the validation, and the divergence would
// show up as a search that silently returned nothing.
func saleLookupBounds(q SaleLookupQuery) (since, until time.Time, ok bool) {
	if q.From != "" {
		start, good := houseMidnight(q.From)
		if !good {
			return time.Time{}, time.Time{}, false
		}
		since = start
	}
	if q.To != "" {
		start, good := houseMidnight(q.To)
		if !good {
			return time.Time{}, time.Time{}, false
		}
		// Half-open: the To day is INCLUSIVE, so the bound is the midnight that ENDS it. AddDate walks
		// the calendar rather than adding 24h, so a day containing a DST transition is still one day.
		until = start.AddDate(0, 0, 1)
	}
	return since, until, true
}

// LookupSales runs the branch-fenced past-sale search.
func (s *Service) LookupSales(ctx context.Context, q SaleLookupQuery) ([]SaleSummary, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if q.BranchID == "" {
		// A scoped caller must name a branch it can reach. Letting it through unfiltered would hand a
		// branch-scoped cashier the whole company's counter history, the exact enumeration the ring's
		// own DIB-389 guard closes on the write side.
		if caller.Scope != identity.ScopeGlobal {
			return nil, errLookupForbidden
		}
	} else if !caller.CanAccessBranch(q.BranchID) {
		return nil, errLookupForbidden
	}
	if q.Limit <= 0 {
		q.Limit = defaultSaleLookupLimit
	}
	if q.Limit > maxSaleLookupLimit {
		q.Limit = maxSaleLookupLimit
	}
	q.Q = strings.TrimSpace(q.Q)
	if _, _, ok := saleLookupBounds(q); !ok {
		return nil, errLookupBadDate
	}
	out, err := s.store.LookupSales(ctx, q)
	if err != nil {
		return nil, err
	}
	// The search itself is a read of counter history; record it so "who went looking through the
	// branch's sales" is answerable, the same way the reopen view is recorded.
	s.record(ctx, audit.SensitiveAccess{
		Action: audit.ActHTTPPost, EntityType: "pos.sale", EntityID: "",
		ActorID: identity.StaffUserIDFromContext(ctx), BranchID: q.BranchID, Purpose: "SALE_LOOKUP",
	})
	return out, nil
}

// LookupSales is the store read behind the past-sale search.
//
// Filters are composed as parameters, never interpolated. The text term matches the three things a
// customer can actually hand you, an order number, an invoice number, or their own name/account -
// and nothing else: matching on free text across the whole row would make the search feel random.
func (r *Repository) LookupSales(ctx context.Context, q SaleLookupQuery) ([]SaleSummary, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	var (
		where []string
		args  []any
	)
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if q.BranchID != "" {
		add("ps.branch_id = $%d", q.BranchID)
	}
	if q.TillSessionID != "" {
		add("ps.till_session_id = $%d", q.TillSessionID)
	}
	if q.Q != "" {
		add(`(o.order_no ILIKE '%%' || $%[1]d || '%%'
		      OR COALESCE(i.invoice_no,'') ILIKE '%%' || $%[1]d || '%%'
		      OR c.name ILIKE '%%' || $%[1]d || '%%'
		      OR c.account_no::text ILIKE '%%' || $%[1]d || '%%')`, q.Q)
	}
	// THE DAY IS RESOLVED IN GO, IN THE HOUSE ZONE, AND BOUND AS AN INSTANT. See the DIB-165 block
	// above for why: `$N::date` against this TIMESTAMPTZ column was promoted at the session's zone,
	// which nobody sets, so both bounds landed at 8pm the evening before the day the cashier meant.
	// A malformed date cannot arrive here, Service.LookupSales refuses it with a 400 first, but if
	// one ever did, the bound is dropped rather than the search failing, and the widened result is
	// the visible symptom rather than a 500 nobody can read.
	since, until, ok := saleLookupBounds(q)
	if ok {
		if !since.IsZero() {
			add("ps.created_at >= $%d", since)
		}
		if !until.IsZero() {
			// Inclusive end-of-day, expressed half-open: a search "to 2026-08-05" includes a sale rung
			// at 16:40 that day AND one rung at 21:15, and excludes the one rung at 00:05 on the 6th.
			add("ps.created_at < $%d", until)
		}
	}
	args = append(args, q.Limit)
	sql := `
SELECT ps.id::text, o.id::text, o.order_no,
       COALESCE(ps.invoice_id::text,''), COALESCE(i.invoice_no,''),
       o.customer_id::text, c.name, ps.branch_id::text, COALESCE(ps.till_session_id::text,''),
       ps.sale_type, ps.tender, ps.split_tender, o.total_cents, ps.status,
       to_char(ps.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
       (SELECT count(*) FROM order_lines ol WHERE ol.order_id = o.id)
  FROM pos_sale ps
  JOIN orders o ON o.id = ps.order_id
  JOIN customers c ON c.id = o.customer_id
  LEFT JOIN invoices i ON i.id = ps.invoice_id`
	if len(where) > 0 {
		sql += "\n WHERE " + strings.Join(where, "\n   AND ")
	}
	sql += fmt.Sprintf("\n ORDER BY ps.created_at DESC\n LIMIT $%d", len(args))

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SaleSummary
	for rows.Next() {
		var s SaleSummary
		if err := rows.Scan(&s.SaleID, &s.OrderID, &s.OrderNo, &s.InvoiceID, &s.InvoiceNo,
			&s.CustomerID, &s.CustomerName, &s.BranchID, &s.TillSessionID, &s.SaleType, &s.Tender,
			&s.SplitTender, &s.TotalCents, &s.Status, &s.CreatedAt, &s.LineCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// readSaleTenders loads one sale's settlement legs in submission order.
func (r *Repository) readSaleTenders(ctx context.Context, posSaleID string) ([]TenderLegResult, error) {
	const q = `
SELECT seq, method, amount_cents, COALESCE(payment_id::text,''), gateway_ref
  FROM pos_sale_tenders WHERE pos_sale_id = $1 ORDER BY seq`
	rows, err := r.pool.Query(ctx, q, posSaleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []TenderLegResult
	for rows.Next() {
		var l TenderLegResult
		if err := rows.Scan(&l.Seq, &l.Method, &l.AmountCents, &l.PaymentID, &l.GatewayRef); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
