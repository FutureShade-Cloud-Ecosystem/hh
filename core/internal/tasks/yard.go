// yard.go, Wave 2 (Swarm B): the yard pick → STAGE → shortage flow, layered onto the shipped
// WMS-Lite tasks substrate (0073) rather than beside it. Heritage: DIB-342 / DIB-343.
//
// WHAT THIS ADDS, IN PLAIN LANGUAGE
// ---------------------------------
// A picker works a queue of PICK tasks. When the material is pulled, the pick is STAGED, parked in
// a named spot with a short reference the counter can call out ("SO-2026-0001 is on Bay 3, ref
// STG-4A"). When the yard cannot fill a line, it flags a SHORTAGE, and that shortage has to reach
// the person at the counter who is standing in front of the customer, not the yard channel, where
// everyone already knows.
//
// THE SHORTAGE PATH IS A MESSAGE EVENT, NOT A NEW MODULE
// -----------------------------------------------------
// A shortage travels back to the counter as a system EVENT message through the EXISTING messaging
// package, over the ChatNotifier seam this module already owns (0073), widened by exactly one field
// (TaskEventRef.Audience). No new notification module, no second inbox, no parallel bus. The counter
// reads shortages where it already reads everything else. tasks still imports neither messaging nor
// order; the composition root maps AudienceCounter to a concrete channel slug.
//
// THE PICKSOURCE SEAM (read this before wiring dispatch)
// -----------------------------------------------------
// What FEEDS the pick queue is deliberately not decided here. Wave 2 builds dispatch in parallel, and
// this module must not depend on it, so the queue's feed is a narrow interface owned by tasks:
//
//	type PickSource interface {
//	    PickTicket(ctx context.Context, orderID string) (PickTicket, error)
//	    PendingTickets(ctx context.Context, branchID string, limit int) ([]PickTicket, error)
//	}
//
// StubPickSource (below) is the fixture implementation and the DEFAULT wiring. A dispatch module
// satisfies PickSource from the composition root, it does not import tasks, and tasks does not
// import it. Both directions stay clean; the seam is the contract.
package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrNoPickSource, the PickSource seam is not wired, so the pick queue has no feed. Distinct from a
// source that legitimately has nothing to give (which returns an empty ticket list, not an error).
var ErrNoPickSource = errors.New("tasks: pick source not configured")

// ErrShortageNotFound, the named shortage does not belong to the task (or does not exist).
var ErrShortageNotFound = errors.New("tasks: shortage not found")

// ErrShortageResolved, the shortage has already been cleared; resolving twice is a duplicate act,
// not a silent overwrite of who cleared it first.
var ErrShortageResolved = errors.New("tasks: shortage already resolved")

// PickLine is one line a picker has to pull. Quantities are DECIMAL-as-string, matching the order
// module's convention, and are the line's OWN uom (never converted to base), the number the picker
// reads is the number the counter repeats to the customer.
type PickLine struct {
	LineNo      int    `json:"line_no"`
	ProductID   string `json:"product_id,omitempty"`
	SKU         string `json:"sku,omitempty"`
	Description string `json:"description"`
	QtyOrdered  string `json:"qty_ordered"`
	// QtyBackordered is the part of QtyOrdered held on backorder, absent when none.
	QtyBackordered string `json:"qty_backordered,omitempty"`
	UOM            string `json:"uom"`
	Location       string `json:"location,omitempty"` // bin/yard hint, "" when the source has none
}

// PickTicket is everything the yard needs to pull one order: who it is for, where, and what.
// Reference carries the feeding system's own handle (a dispatch stop id, a will-call slip number);
// it is opaque to tasks and is shown to the picker verbatim.
type PickTicket struct {
	OrderID      string `json:"order_id"`
	OrderNo      string `json:"order_no,omitempty"`
	BranchID     string `json:"branch_id,omitempty"`
	CustomerName string `json:"customer_name,omitempty"`
	Fulfillment  string `json:"fulfillment,omitempty"` // PICKUP | DELIVERY | WILL_CALL
	// MTOClass is STANDARD | MTO | MIXED, whether this order is made-to-order material.
	//
	// IT REACHES THE PICKER BEFORE THE DRIVER, which is the whole reason it is on this struct. The
	// classification already rode the dispatch board to the truck; the person who has to NOT
	// substitute a pallet is the one pulling it, and they were the one not being told. Empty means
	// the source did not classify, which a pickup never is, and which must not be rendered as
	// "Standard" (see picksource.go's note on the pickup case).
	MTOClass    string     `json:"mto_class,omitempty"`
	Priority    string     `json:"priority,omitempty"`
	Reference   string     `json:"reference,omitempty"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	Lines       []PickLine `json:"lines"`
	// Source names the implementation that produced this ticket ("STUB", "DISPATCH", …). It is
	// carried all the way to the UI on purpose: a queue fed by a fixture must SAY it is fixture data.
	Source string `json:"source"`
}

// PickSource feeds the yard pick queue. It is the ONLY thing tasks needs to know about whatever
// system decides which orders get picked, orders, a dispatch board, a will-call desk.
//
// SEAM CONTRACT (stable; the composition root wires an implementation):
//   - PickTicket returns the ticket for one order. An order the source does not know MUST return
//     ErrUnknownOrder, so the caller can distinguish "no such order" from a transport failure.
//   - PendingTickets returns ONE PAGE of the tickets awaiting picking at a branch, most urgent first,
//     plus the TOTAL number awaiting picking. limit <= 0 means the implementation's own sane default;
//     a negative offset is read as zero; an offset past the end is an EMPTY page and a nil error, not
//     a refusal. An empty branch is an empty slice and a nil error, never an error.
//   - Both must be safe to call concurrently and must not mutate anything.
//
// THE WINDOW IS PART OF THE CONTRACT because truncation was not. This seam used to promise only a
// bound, and the implementation behind it merged two sources and CUT the result, so at the bound a
// pickup that sorted earlier displaced a delivery off the end and the board silently stopped being
// the board. A caller that is handed a window and a total can tell that there is more; a caller
// handed a truncated list cannot.
type PickSource interface {
	PickTicket(ctx context.Context, orderID string) (PickTicket, error)
	PendingTickets(ctx context.Context, branchID string, limit, offset int) ([]PickTicket, int, error)
}

// StubPickSource is the fixture PickSource and the default wiring until a real feed (Wave 2's
// dispatch board) is attached at the composition root.
//
// It is a STUB and says so: every ticket it returns carries Source "STUB" and every synthesized line
// carries a FIXTURE- SKU, so nothing downstream can mistake fixture material for a real pick list.
// It has two modes, and they are not the same thing:
//
//	Seeded  , tickets explicitly added with Seed(). Returned verbatim. This is what tests use.
//	Derived , for an order id the stub was not seeded with, DeriveUnknown (default true) synthesizes
//	           a small, DETERMINISTIC ticket from a hash of the order id. That exists so the flow is
//	           walkable end-to-end against real seeded orders before dispatch lands; it is fixture
//	           material with a fixture label, not a claim about that order's real contents. Set
//	           DeriveUnknown=false and an unseeded order returns ErrUnknownOrder instead.
type StubPickSource struct {
	seeded map[string]PickTicket
	order  []string // insertion order, so PendingTickets is stable rather than map-random

	// DeriveUnknown synthesizes a labelled fixture ticket for an order the stub was not seeded with.
	// False makes the stub strict (unseeded → ErrUnknownOrder).
	DeriveUnknown bool
}

// NewStubPickSource builds the fixture source in derive mode (walkable against any order id).
func NewStubPickSource() *StubPickSource {
	return &StubPickSource{seeded: map[string]PickTicket{}, DeriveUnknown: true}
}

// NewStrictStubPickSource builds the fixture source WITHOUT derivation: only seeded orders exist.
// Tests that assert the unknown-order path use this.
func NewStrictStubPickSource() *StubPickSource {
	return &StubPickSource{seeded: map[string]PickTicket{}, DeriveUnknown: false}
}

// Seed registers an explicit fixture ticket. The Source field is forced to "STUB", a fixture cannot
// launder itself into looking like a real feed by setting its own Source.
func (s *StubPickSource) Seed(t PickTicket) *StubPickSource {
	t.Source = "STUB"
	if _, dup := s.seeded[t.OrderID]; !dup {
		s.order = append(s.order, t.OrderID)
	}
	s.seeded[t.OrderID] = t
	return s
}

// PickTicket returns the seeded ticket, else a derived fixture ticket, else ErrUnknownOrder.
func (s *StubPickSource) PickTicket(_ context.Context, orderID string) (PickTicket, error) {
	if orderID == "" {
		return PickTicket{}, ErrUnknownOrder
	}
	if t, ok := s.seeded[orderID]; ok {
		return t, nil
	}
	if !s.DeriveUnknown {
		return PickTicket{}, ErrUnknownOrder
	}
	return deriveFixtureTicket(orderID), nil
}

// PendingTickets returns the seeded tickets for a branch in insertion order. Derived tickets are
// NEVER returned here: a queue feed must not invent orders that do not exist. An empty branch is an
// empty slice, not an error.
func (s *StubPickSource) PendingTickets(_ context.Context, branchID string, limit, offset int) ([]PickTicket, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	all := make([]PickTicket, 0, len(s.order))
	for _, id := range s.order {
		t := s.seeded[id]
		if branchID != "" && t.BranchID != branchID {
			continue
		}
		all = append(all, t)
	}
	// The fixture pages the same way the real feed does, window then total, so a caller cannot
	// develop against a stub that promises something the shipped source does not.
	if offset >= len(all) {
		return []PickTicket{}, len(all), nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], len(all), nil
}

// fixtureSKUs is the small catalogue the derived ticket draws from. Every entry is prefixed FIXTURE-
// so it is unmistakable on screen and in a screenshot.
var fixtureSKUs = []struct {
	sku, desc, uom, loc string
}{
	{"FIXTURE-PAVER-HOLLAND", "Holland paver, charcoal (fixture)", "PALLET", "Yard A"},
	{"FIXTURE-STONE-FLAG", "Flagstone, random (fixture)", "TONNE", "Yard B"},
	{"FIXTURE-SAND-HPB", "HPB bedding sand (fixture)", "TONNE", "Bulk 2"},
	{"FIXTURE-EDGE-RESTRAINT", "Paver edge restraint (fixture)", "EA", "Rack 7"},
	{"FIXTURE-POLY-SAND", "Polymeric sand, grey (fixture)", "BAG", "Rack 3"},
}

// deriveFixtureTicket builds a stable fixture ticket from an order id. Same id in, same ticket out -
// a walkthrough screenshot taken twice shows the same lines, and a test can assert on them.
func deriveFixtureTicket(orderID string) PickTicket {
	sum := sha256.Sum256([]byte(orderID))
	n := int(binary.BigEndian.Uint32(sum[0:4]))
	if n < 0 {
		n = -n
	}
	count := 2 + n%3 // 2..4 lines
	lines := make([]PickLine, 0, count)
	for i := 0; i < count; i++ {
		f := fixtureSKUs[(n+i*7)%len(fixtureSKUs)]
		qty := 1 + (n+i*13)%9
		lines = append(lines, PickLine{
			LineNo:      i + 1,
			SKU:         f.sku,
			Description: f.desc,
			QtyOrdered:  fmt.Sprintf("%d.0000", qty),
			UOM:         f.uom,
			Location:    f.loc,
		})
	}
	return PickTicket{
		OrderID:   orderID,
		Reference: "FIXTURE-" + shortRef(orderID),
		Lines:     lines,
		Source:    "STUB",
	}
}

// shortRef is a stable 6-char uppercase handle derived from a uuid, used for the fixture reference
// and (in the service) for a staged pick's default stage_ref.
func shortRef(s string) string {
	sum := sha256.Sum256([]byte(s))
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I/O/0/1, these get read aloud
	out := make([]byte, 6)
	for i := 0; i < 6; i++ {
		out[i] = alphabet[int(sum[i])%len(alphabet)]
	}
	return string(out)
}

// Shortage is one reported shortfall on a pick (a task_shortages row, 0095). ResolvedAt nil = still
// open, the counter has not settled it with the customer yet.
type Shortage struct {
	ID          string     `json:"id"`
	TaskID      string     `json:"task_id"`
	OrderLineNo *int       `json:"order_line_no,omitempty"`
	ProductID   string     `json:"product_id,omitempty"`
	SKU         string     `json:"sku,omitempty"`
	Description string     `json:"description"`
	QtyShort    string     `json:"qty_short"` // NUMERIC(19,4) as string
	UOM         string     `json:"uom"`
	Note        string     `json:"note,omitempty"`
	ReportedBy  string     `json:"reported_by,omitempty"`
	ReportedAt  time.Time  `json:"reported_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy  string     `json:"resolved_by,omitempty"`
	Resolution  string     `json:"resolution,omitempty"`
}

// Open reports whether the counter still has to deal with this shortage.
func (s Shortage) Open() bool { return s.ResolvedAt == nil }

// StageInput is the IN_PROGRESS → STAGED move: where the material is parked and under what handle.
// Location is required (a staged pick nobody can find is worse than an unstaged one); Ref defaults
// to a derived short handle when the picker does not type one.
type StageInput struct {
	TaskID   string
	ActorID  string
	Location string
	Ref      string
	Note     string
}

// ShortageInput is one shortage report. Description and QtyShort are required, "we're short
// something" is not a message the counter can act on.
type ShortageInput struct {
	TaskID      string
	OrderLineNo *int
	ProductID   string
	SKU         string
	Description string
	QtyShort    string // DECIMAL as string, must parse > 0
	UOM         string
	Note        string
	ReportedBy  string
}

// ResolveShortageInput clears one shortage with a stated resolution (substituted, back-ordered,
// credited). The resolution text is required: "resolved" with no account of HOW is how a customer
// ends up told two different things by two different people.
type ResolveShortageInput struct {
	TaskID     string
	ShortageID string
	ActorID    string
	Resolution string
}

// PickQueueEntry is one row of the yard board: the task (the operational truth) joined in memory to
// its ticket (the material to pull) and its open shortages. Ticket is nil when the PickSource does
// not know the order, the task is still shown, because a task whose feed went missing is exactly
// the row a yard lead needs to see, not one to hide.
type PickQueueEntry struct {
	Task      Task        `json:"task"`
	Ticket    *PickTicket `json:"ticket,omitempty"`
	Shortages []Shortage  `json:"shortages,omitempty"`
	// Picked is what the yard actually pulled, line by line (0187). Absent until somebody records a
	// pick, which reads correctly as "nobody has written down what was pulled" rather than as
	// "nothing was pulled".
	Picked []PickedLine `json:"picked,omitempty"`
}

// PickedLine is one line of a pick ticket as the yard actually pulled it (a task_pick_lines row,
// migration 0187).
//
// IT IS A MEASUREMENT, NOT A MESSAGE, and that is the whole distinction between this and Shortage.
// A Shortage is the record that somebody deliberately TOLD THE COUNTER a line could not be filled,
// with a note and a resolution the counter owns. This is what came off the shelf. A line may be
// short here with no shortage row, the picker wrote 7 of 10 and said nothing, and the difference
// between those two states is itself worth seeing on a screen.
//
// SHORTNESS IS DERIVED FROM THIS, never stored: QtyPicked < QtyOrdered. Migration 0187's header
// records that rule, because a schema cannot enforce which of two models is authoritative and two
// derivations of one fact is how they drift.
type PickedLine struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	OrderLineNo int       `json:"order_line_no"`
	ProductID   string    `json:"product_id,omitempty"`
	SKU         string    `json:"sku,omitempty"`
	Description string    `json:"description,omitempty"`
	QtyOrdered  string    `json:"qty_ordered"` // NUMERIC(19,4) as string
	QtyPicked   string    `json:"qty_picked"`  // NUMERIC(19,4) as string; "0" is a real answer
	UOM         string    `json:"uom"`
	PickedBy    string    `json:"picked_by,omitempty"`
	PickedAt    time.Time `json:"picked_at"`
	// ShortReason and ShortNote say why the line came up short (0366). Empty on a full pick.
	ShortReason string `json:"short_reason,omitempty"`
	ShortNote   string `json:"short_note,omitempty"`
}

// Short reports whether less came off the shelf than the ticket asked for. It is the ONE place that
// decides what "short" means, so no caller re-derives it with a different string comparison.
func (l PickedLine) Short() bool {
	ordered, err1 := strconv.ParseFloat(l.QtyOrdered, 64)
	picked, err2 := strconv.ParseFloat(l.QtyPicked, 64)
	if err1 != nil || err2 != nil {
		return false // an unparseable quantity is not evidence of a shortfall
	}
	return picked < ordered
}

// PickedLineInput is one line the picker is recording. QtyPicked is required and may be zero, "we
// went to pick it and there was none" is a recorded pick of nothing, not an absent line.
type PickedLineInput struct {
	OrderLineNo int
	ProductID   string
	SKU         string
	Description string
	QtyOrdered  string
	QtyPicked   string
	UOM         string
	// ShortReason is one of ShortReasons and is required when QtyPicked < QtyOrdered; ShortNote is
	// required when the reason is OTHER.
	ShortReason string
	ShortNote   string
}

// CounterSlugForYardSlug maps a branch's yard channel slug to that branch's counter channel slug
// ("yard-trenton" → "sales-trenton"). It lives here, as a pure function with its own test, rather
// than inline in the composition root, because it encodes a real convention (channels are named
// <function>-<branch>) that deserves to fail visibly if it ever changes.
//
// A slug that is not yard-prefixed returns "", the caller then falls back rather than inventing a
// channel name that does not exist.
func CounterSlugForYardSlug(yardSlug string) string {
	const prefix = "yard-"
	if !strings.HasPrefix(yardSlug, prefix) || len(yardSlug) == len(prefix) {
		return ""
	}
	return "sales-" + strings.TrimPrefix(yardSlug, prefix)
}
