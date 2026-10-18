package dispatch

// picksource.go, THE PICK FEED (Wave 3, Swarm C, item 1).
//
// WHAT THIS RETIRES. Wave 2 shipped the yard pick queue behind tasks.PickSource, a seam whose only
// implementation was tasks.StubPickSource, a labelled fixture that synthesised FIXTURE- SKUs from a
// hash of the order id. The yard console rendered a banner saying so. This file is the real feed: it
// carries the order, the customer, the branch and the date the yard needs.
//
// THE PREMISE THIS FILE SHIPPED WITH WAS A SHORTCUT, AND IT IS RETIRED HERE. The original header said
// "a stop scheduled on the dispatch board IS the instruction to pull material", and the code enforced
// it twice: PendingTickets read delivery stops only, and ticket() stamped Fulfillment "DELIVERY"
// unconditionally. A customer pickup has no delivery stop, so a will-call order could be racked and
// given a pickup code while never once appearing in the yard's pick queue, the yard was told to hand
// material over without ever being told to pull it.
//
// WHAT ACTUALLY INSTRUCTS THE YARD is not a delivery stop. It is AN ORDER WITH A FULFILMENT
// COMMITMENT. Delivery produces one (a scheduled stop). Customer pickup produces one (a promised
// collection date on a pickup-bound order). We had modelled the general thing as one of its two cases
// and it broke for the other; the fix is to name the general thing, which is what this file now does:
//
//	PendingTickets = the delivery-stop source  ∪  the pickup source.
//
// TWO READS, ONE FEED, AND THEY STAY TWO. The delivery query is untouched: it is NOT widened with an
// OR, and no synthetic delivery_stops row is minted for a pickup. loadboard already ruled that a
// pickup is not derivable from delivery rows (the load board's pickup card has always been its own
// read for exactly this reason), and that ruling stands, this change satisfies it rather than
// working around it. The two reads are disjoint at the DATABASE (the pickup query anti-joins live
// stops), so one order can never produce two tickets.
//
// THE DIRECTION OF THE DEPENDENCY IS UNCHANGED. dispatch still does not import tasks, and tasks still
// does not import dispatch. What this file produces is a DISPATCH-shaped ticket; the composition root
// writes the six-line adapter that presents it as a tasks.PickTicket (cmd/server/main.go, the
// SWARM-W3-C block). Both modules' unit suites still run with neither package linked in.
//
// THE LINE READ IS A NEW SEAM, NOT A WIDENED OLD ONE. dispatch already consumes order through
// OrderReader (module.go), a FROZEN four-field authority brief: id, branch, fulfilment, status. The
// pick ticket needs LINES, which is a different question with a different shape and a different
// failure mode (a brief that cannot be read means the order does not exist; lines that cannot be read
// means the board is degraded). Widening the authority brief to carry a line array would have made
// every consumer of the brief pay for a read none of them want, so lines arrive through their OWN
// consumer-declared interface below, satisfied in main.go over the order module's existing staff-side
// line read (order.Repository.OrderLines). The frozen brief is untouched.
//
// FENCING, STATED HONESTLY. The methods here are NOT branch-fenced and are NOT registered on any
// route. They are an internal feed consumed by the tasks yard surface, which is staff-gated at its
// own routes and filters by branch_id exactly as it did against the stub. That is parity with the
// shipped Wave 2 posture, not an improvement on it, fencing the yard surface itself is named as a
// union follow-up in REPORT-W3-C.md. Nothing here is reachable from an HTTP handler in this package.

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// PickFeedLine is one line the yard has to pull, as the feed carries it. Quantities are
// DECIMAL-as-string in the line's OWN uom, the number the picker reads is the number the counter
// repeats to the customer, never a base-unit conversion (the tasks.PickLine convention, mirrored).
type PickFeedLine struct {
	LineNo      int
	ProductID   string
	SKU         string
	Description string
	QtyOrdered  string
	// QtyBackordered is the part of QtyOrdered held on backorder (a yard short pick), empty when none.
	QtyBackordered string
	UOM            string
	Location       string
}

// OrderLineReader is the NEW consumer-declared seam: "what is on this order". It is satisfied in the
// composition root by an adapter over the order module's staff-side line read, and it is deliberately
// SEPARATE from OrderReader (see the file header).
//
// CONTRACT: an order that does not exist returns an error; an order that exists with no lines returns
// an empty slice and a nil error. The caller distinguishes "no such order" (the ticket does not
// exist) from "an order with nothing on it" (a real, if unusual, state a yard lead should see).
type OrderLineReader interface {
	OrderPickLines(ctx context.Context, orderID string) ([]PickFeedLine, error)
}

// PickFeedTicket is one order's pull instruction, built from the stop that schedules it, or, for a
// customer pickup, from the order that promised it.
//
// REFERENCE IS THE HANDLE A PICKER CAN READ ALOUD TO SOMEONE WHO CAN ANSWER. That is the point of the
// field on the far side of the seam (tasks calls it "the feeding system's own handle"): a fixture
// reference gets a shrug. It differs by source because the two sources have different handles:
//
//	DELIVERY, the STOP ID. Dispatch owns the stop, and a dispatcher can answer for it.
//	PICKUP  , the ORDER NUMBER (#SO-TRT-1041). A pickup has no stop, so there is no dispatch handle
//	           to give; the order number is what is printed on the paperwork, what the counter looks
//	           orders up by (DIB-379), and what the will-call form already takes instead of a UUID
//	           (DIB-483). It is deliberately NOT the will-call pickup id (a UUID nobody can read) and
//	           deliberately NOT the pickup CODE, which is a credential: a code spoken across a yard or
//	           written into a log is a code that no longer proves possession.
type PickFeedTicket struct {
	OrderID       string
	OrderNo       string
	BranchID      string
	CustomerName  string
	Fulfillment   string
	Priority      string
	Reference     string // the stop id for a delivery, the order number for a pickup (see above)
	ScheduledDate string // YYYY-MM-DD
	RequestedAt   *time.Time
	StopID        string
	StopStatus    string
	RunID         string
	RunNo         string
	RunStatus     string // "" when the stop is not yet on a run
	RunDepartsAt  *time.Time
	LoadPosition  int
	MTOClass      string // DIB-63: STANDARD | MTO | MIXED, the picker needs it before the driver does
	TimeWindow    string
	Lines         []PickFeedLine
	// HeldFromPicking, HoldWords and StillOwedCents are the pay before
	// release hold (item 9 Opus P2-1): a held order STAYS in the feed,
	// marked, so the yard knows why it waits. This module never computes the
	// mark (it holds no billing facts): the composition root fills it from
	// billing's one hold read. The doors (tasks' Claim and Stage) refuse. The
	// omitempty keeps a clear ticket byte identical to what this code
	// produced before the mark existed (the delivery ticket's own pin).
	HeldFromPicking bool   `json:",omitempty"`
	HoldWords       string `json:",omitempty"`
	StillOwedCents  int64  `json:",omitempty"`
}

// pickFeedPickup is the raw row the PICKUP read returns: everything a pull instruction needs when
// there is no stop, no run and no truck. It is a separate struct from pickFeedStop and not a partly
// blank one, so that "a pickup has no run" is a fact about the type rather than a convention about
// which fields somebody remembered to leave empty.
type pickFeedPickup struct {
	OrderID      string
	OrderNo      string
	BranchID     string
	CustomerName string
	Fulfillment  string // PICKUP | WILL_CALL, the order's own, never assumed
	// PromisedDate is the date the customer was told they could collect (orders.requested_date). It is
	// the pickup's urgency signal and it is NOT a delivery date: nothing is scheduled to leave.
	PromisedDate string
	OrderStatus  string
}

// pickFeedStop is the raw row the feed queries read: everything on the ticket except the lines.
type pickFeedStop struct {
	StopID        string
	BranchID      string
	OrderID       string
	OrderNo       string
	CustomerName  string
	Fulfillment   string
	ScheduledDate string
	StopStatus    string
	RunID         string
	RunNo         string
	RunStatus     string
	RunDepartsAt  *time.Time
	LoadPosition  int
	MTOClass      string
	TimeWindow    string
}

// PickFeed builds pick tickets from the dispatch board. It is a separate type from Service on
// purpose: Service is the fenced, caller-gated HTTP surface, and this is an internal feed with a
// different authority model (see the file header). Keeping them apart means a future route can never
// accidentally expose the unfenced read by being added to the wrong receiver.
type PickFeed struct {
	repo  *Repository
	lines OrderLineReader
	log   *slog.Logger
	now   func() time.Time
}

// NewPickFeed wraps the repository.
func NewPickFeed(repo *Repository) *PickFeed {
	return &PickFeed{repo: repo, log: slog.Default(), now: time.Now}
}

// WithOrderLines injects the line seam. Unwired, tickets carry no lines, the yard still sees the
// order, the customer and the date, which is strictly more than the fixture gave it.
func (f *PickFeed) WithOrderLines(r OrderLineReader) *PickFeed { f.lines = r; return f }

// WithLogger injects the server logger.
func (f *PickFeed) WithLogger(l *slog.Logger) *PickFeed {
	if l != nil {
		f.log = l
	}
	return f
}

// WithClock injects a deterministic clock (tests pin the priority boundary).
func (f *PickFeed) WithClock(now func() time.Time) *PickFeed {
	if now != nil {
		f.now = now
	}
	return f
}

// TicketForOrder returns the pick ticket for one order: its live delivery stop if it has one, else its
// pickup commitment, else ErrNotFound.
//
// THE DELIVERY READ IS TRIED FIRST AND IS UNCHANGED, so an order that resolved to a ticket before this
// change resolves to the same ticket now, the pickup read is only ever reached where the answer used
// to be ErrNotFound. ErrNotFound from the pickup read is returned as-is, which keeps the composition
// root's one required translation (dispatch.ErrNotFound → tasks.ErrUnknownOrder) working unchanged.
//
// "LIVE" HERE MEANS NOT CANCELLED AND NOT FAILED, not "still to be picked". A pick task whose stop
// has already been loaded onto the truck must still be able to show its lines: the yard board keeps
// the task visible through STAGED and DONE, and a ticket that vanished the moment the truck was
// loaded would blank the lines out from under a picker mid-shift.
func (f *PickFeed) TicketForOrder(ctx context.Context, orderID string) (PickFeedTicket, error) {
	t, err := f.ticketShell(ctx, orderID)
	if err != nil {
		return PickFeedTicket{}, err
	}
	lines, err := f.readLines(ctx, orderID)
	if err != nil {
		// A single-order read propagates: the caller asked about ONE order and deserves the reason,
		// not a ticket that silently claims the order is empty.
		return PickFeedTicket{}, err
	}
	t.Lines = lines
	return t, nil
}

// ticketShell is the two-source lookup without the lines: the delivery stop, then the pickup.
func (f *PickFeed) ticketShell(ctx context.Context, orderID string) (PickFeedTicket, error) {
	row, err := f.repo.PickFeedStopForOrder(ctx, orderID)
	if err == nil {
		return f.ticket(row), nil
	}
	if !errors.Is(err, ErrNotFound) {
		// A BROKEN READ IS NOT AN ABSENT STOP. Falling through on a transport failure would answer
		// "this is a pickup" for an order that may well be on a truck, which is the one wrong answer
		// this function can give a picker.
		return PickFeedTicket{}, err
	}
	pickup, err := f.repo.PickFeedPickupForOrder(ctx, orderID)
	if err != nil {
		return PickFeedTicket{}, err
	}
	return f.pickupTicket(pickup), nil
}

// PendingTickets returns everything still awaiting picking at a branch, most urgent first. It is the
// UNION of the two sources, and the union is the fix: a yard lead's queue is "what has to be pulled",
// which was never the same question as "what is on a truck".
//
// AWAITING PICKING, PER SOURCE, because the two commitments end differently:
//
//	DELIVERY, stop status READY and either unassigned or on a PLANNED run. A LOADED stop is already
//	           on the truck and a DISPATCHED run has left; putting either on the pick queue would be
//	           asking the yard to pull material that is already gone.
//	PICKUP  , a pickup-bound order between CONFIRMED and PARTIALLY_FULFILLED that is on no live stop.
//	           COLLECTION is what ends a pickup, exactly as departure ends a delivery, and collection
//	           is recorded as the order reaching FULFILLED.
//
// THE MERGE PRESERVES THE DELIVERY SOURCE'S ORDER EXACTLY. Both reads arrive sorted by their own date,
// and mergeByDate is a stable merge that breaks a tie in the delivery row's favour. So a branch with
// no pickups gets byte-identically the list it got before this change, the union is observably inert
// where there is nothing to union.
//
// An empty branch is an empty slice and a nil error, never an error (the tasks.PickSource contract).
//
// IT PAGES RATHER THAN TRUNCATES, which is union train 15's named deferral closed. The merged list
// used to be cut to `limit` after the merge, so at the cap a pickup that sorted earlier DISPLACED a
// delivery off the end of the board, the queue silently stopped being the queue. Paging returns the
// window the caller asked for and the TOTAL beside it, so a yard lead can see that there are more and
// go and get them.
//
// A per-order line read that fails DEGRADES that one row (logged, no lines) rather than failing the
// whole board: a yard lead with eleven tickets and one broken line read is better served by ten
// usable tickets and a warning than by an empty screen.
func (f *PickFeed) PendingTickets(ctx context.Context, branchID string, limit, offset int) ([]PickFeedTicket, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	// THE OVER-READ, AND WHY A MERGE CANNOT BE OFFSET AT THE DATABASE. The k-th row of the merged feed
	// is not the k-th row of either source, so `OFFSET k` pushed down into the two queries would skip
	// k rows of EACH and return a window that exists in neither list. What IS true is that the first
	// (offset+limit) merged rows can only be drawn from the first (offset+limit) rows of each source,
	// so both are read to that depth, merged by the unchanged stable merge, and sliced.
	//
	// The depth is capped, and the cap is refused in words rather than silently clamped (loadboard's
	// ValidatePolicy posture): a page 400 clicks deep would otherwise become an unbounded scan of two
	// tables to render twenty rows.
	depth := limit + offset
	if depth > maxPickFeedDepth {
		return nil, 0, httpx.BadRequestf(
			"dispatch: that page is too far into the queue, narrow the branch or the date instead of paging past %d tickets",
			maxPickFeedDepth)
	}

	rows, err := f.repo.PickFeedPendingStops(ctx, branchID, depth)
	if err != nil {
		return nil, 0, err
	}
	deliveries := make([]PickFeedTicket, 0, len(rows))
	for _, row := range rows {
		t := f.ticket(row)
		lines, err := f.readLines(ctx, row.OrderID)
		if err != nil {
			f.log.Warn("dispatch: pick feed line read failed", "order", row.OrderID, "stop", row.StopID, "err", err)
		} else {
			t.Lines = lines
		}
		deliveries = append(deliveries, t)
	}

	pickupRows, err := f.repo.PickFeedPendingPickups(ctx, branchID, depth)
	if err != nil {
		// THE PICKUP READ FAILING DOES NOT BLANK THE DELIVERY QUEUE. The whole point of two reads
		// behind one feed is that neither is load-bearing for the other; a yard that can still see its
		// trucks is better served than one shown an error because a second query timed out.
		//
		// The TOTAL degrades with it, deliberately: reporting the delivery count as if it were the
		// whole queue would put a confident wrong number under a list that is missing rows. The
		// delivery total is what is known, and it is what is returned.
		f.log.Warn("dispatch: pick feed pickup read failed", "branch", branchID, "err", err)
		total, cerr := f.repo.CountPickFeedPendingStops(ctx, branchID)
		if cerr != nil {
			total = len(deliveries)
		}
		return pageOf(deliveries, limit, offset), total, nil
	}
	pickups := make([]PickFeedTicket, 0, len(pickupRows))
	for _, row := range pickupRows {
		t := f.pickupTicket(row)
		lines, err := f.readLines(ctx, row.OrderID)
		if err != nil {
			f.log.Warn("dispatch: pick feed line read failed", "order", row.OrderID, "pickup", row.OrderNo, "err", err)
		} else {
			t.Lines = lines
		}
		pickups = append(pickups, t)
	}

	// THE TOTAL IS COUNTED, NOT INFERRED FROM THE PAGE. len(merged) is only ever the depth we read to,
	// so on any page but the last it would report the window as the whole queue. The two counts share
	// their WHERE clauses with the two list reads (repository_w3.go), so "1-20 of 47" cannot drift
	// from what paging through actually yields.
	stopTotal, err := f.repo.CountPickFeedPendingStops(ctx, branchID)
	if err != nil {
		return nil, 0, err
	}
	pickupTotal, err := f.repo.CountPickFeedPendingPickups(ctx, branchID)
	if err != nil {
		return nil, 0, err
	}

	return pageOf(mergeByDate(deliveries, pickups), limit, offset), stopTotal + pickupTotal, nil
}

// maxPickFeedDepth bounds how far into the merged queue a caller may page. Two hundred is the page
// cap (httpx.MaxPageLimit) and a thousand is five of those, far past any real yard's board, and far
// short of a scan that would hurt.
const maxPickFeedDepth = 1000

// pageOf slices the merged feed to the requested window. An offset past the end is an EMPTY page and
// not an error: paging one click too far is a thing people do, and it deserves an empty list under an
// honest total, not a refusal.
func pageOf(all []PickFeedTicket, limit, offset int) []PickFeedTicket {
	if offset >= len(all) {
		return []PickFeedTicket{}
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end]
}

// mergeByDate interleaves two already-date-sorted ticket lists into one, oldest promise first.
//
// A ticket with NO date sorts LAST rather than first, an absent urgency signal must not outrank a
// customer who is actually waiting, which is the same NULLS LAST rule the pickup query applies in
// SQL, restated here because the merge cannot see the SQL.
//
// THE TIE GOES TO THE DELIVERY, and that is what makes this change inert for an existing board: with
// no pickups the result is the delivery slice, element for element, in its original order.
func mergeByDate(deliveries, pickups []PickFeedTicket) []PickFeedTicket {
	out := make([]PickFeedTicket, 0, len(deliveries)+len(pickups))
	i, j := 0, 0
	for i < len(deliveries) && j < len(pickups) {
		if sortsBefore(pickups[j].ScheduledDate, deliveries[i].ScheduledDate) {
			out = append(out, pickups[j])
			j++
			continue
		}
		out = append(out, deliveries[i])
		i++
	}
	out = append(out, deliveries[i:]...)
	out = append(out, pickups[j:]...)
	return out
}

// sortsBefore is STRICTLY before: equal dates return false, which is what keeps the merge stable and
// the delivery first on a tie. An empty date is later than every real one.
func sortsBefore(a, b string) bool {
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	return a < b
}

func (f *PickFeed) readLines(ctx context.Context, orderID string) ([]PickFeedLine, error) {
	if f.lines == nil {
		return []PickFeedLine{}, nil
	}
	lines, err := f.lines.OrderPickLines(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		lines = []PickFeedLine{}
	}
	return lines, nil
}

// ticket maps a delivery board row onto the feed shape.
//
// FULFILLMENT CARRIES THE ORDER'S OWN METHOD, and this costs nothing: pickFeedSelect has always
// projected o.fulfillment into the row and this function has always thrown it away in favour of the
// literal "DELIVERY". There is no second read here, the value was already on the row.
//
// THE FALLBACK IS NOT DEFENSIVE PADDING, IT IS THE INERTNESS GUARANTEE. pickFeedSelect LEFT JOINs
// orders and COALESCEs a missing one to the empty string, so a stop whose order row cannot be read
// would otherwise have gone from saying DELIVERY to saying nothing. A stop exists because something
// is being delivered; when the row cannot say what, the stop's own existence still can. Every ticket
// that said DELIVERY before this change still says DELIVERY.
func (f *PickFeed) ticket(row pickFeedStop) PickFeedTicket {
	fulfilment := row.Fulfillment
	if fulfilment == "" {
		fulfilment = "DELIVERY"
	}
	t := PickFeedTicket{
		OrderID:       row.OrderID,
		OrderNo:       row.OrderNo,
		BranchID:      row.BranchID,
		CustomerName:  row.CustomerName,
		Fulfillment:   fulfilment,
		Priority:      f.priorityFor(row.ScheduledDate),
		Reference:     row.StopID,
		ScheduledDate: row.ScheduledDate,
		StopID:        row.StopID,
		StopStatus:    row.StopStatus,
		RunID:         row.RunID,
		RunNo:         row.RunNo,
		RunStatus:     row.RunStatus,
		MTOClass:      row.MTOClass,
		TimeWindow:    row.TimeWindow,
		Lines:         []PickFeedLine{},
	}
	if d, err := time.Parse("2006-01-02", row.ScheduledDate); err == nil {
		at := d
		t.RequestedAt = &at
	}
	return t
}

// pickupTicket maps a PICKUP order row onto the same feed shape.
//
// EVERY STOP-SHAPED FIELD IS EMPTY, ON PURPOSE. A pickup is on no stop, no run and no truck, so
// StopID, StopStatus, RunID, RunNo, RunStatus and TimeWindow are blank rather than filled with a
// plausible-looking value. Anything downstream that keys off "has a run" therefore gets the truth.
//
// MTOClass IS STANDARD, and that is a statement about where the fact lives rather than about the
// material. mto_class is a column on delivery_stops (0107), a property of a LOAD, classified when a
// load is built. A pickup has no load, so nothing has ever classified it, and STANDARD is the value
// the same column defaults to. When the yard surface gives a pickup its own classification, it will
// come from a read that exists; inventing MIXED here would be a guess wearing a fact's clothes.
//
// PRIORITY COMES FROM THE PROMISED COLLECTION DATE, never from a delivery date, nothing about this
// order is scheduled to leave. An order with no promised date grades NORMAL and carries no
// RequestedAt, which is the honest answer: nobody was told a day.
func (f *PickFeed) pickupTicket(row pickFeedPickup) PickFeedTicket {
	t := PickFeedTicket{
		OrderID:       row.OrderID,
		OrderNo:       row.OrderNo,
		BranchID:      row.BranchID,
		CustomerName:  row.CustomerName,
		Fulfillment:   row.Fulfillment,
		Priority:      f.priorityFor(row.PromisedDate),
		Reference:     row.OrderNo,
		ScheduledDate: row.PromisedDate,
		MTOClass:      MTOStandard,
		Lines:         []PickFeedLine{},
	}
	if d, err := time.Parse("2006-01-02", row.PromisedDate); err == nil {
		at := d
		t.RequestedAt = &at
	}
	return t
}

// priorityFor grades urgency by THE DATE IT IS GIVEN, and each source gives it its own: a delivery
// supplies the delivery date, a pickup supplies the promised collection date. It deliberately does not
// know which it received, reusing a delivery date for a pickup would grade a customer's collection
// against a truck that is not going anywhere.
//
// Yesterday is URGENT (the promise is already broken); today is HIGH; anything further out is NORMAL.
//
// An unparseable or ABSENT date is NORMAL, not URGENT: a formatting bug must not flood the top of the
// queue, and neither must an order nobody promised a day for.
func (f *PickFeed) priorityFor(scheduledDate string) string {
	d, err := time.Parse("2006-01-02", scheduledDate)
	if err != nil {
		return "NORMAL"
	}
	today := f.now().UTC().Truncate(24 * time.Hour)
	switch {
	case d.Before(today):
		return "URGENT"
	case d.Equal(today):
		return "HIGH"
	default:
		return "NORMAL"
	}
}
