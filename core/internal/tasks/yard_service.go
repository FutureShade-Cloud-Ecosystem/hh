package tasks

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// yard_service.go, the Wave 2 service layer for pick → STAGE → shortage. It adds no new persistence
// pattern: every write funnels through the same transactional store the 0073 skeleton established, so
// a status move and its task_events audit row still commit together, and a shortage report and the
// EVENT that carries it to the counter cannot drift apart.

// WithPickSource injects the queue feed (see the PickSource doc in yard.go). Un-wired, the pick-queue
// reads still work, they simply carry no ticket, and PickTicket-only calls return ErrNoPickSource.
// Unwired never means "silently empty queue": the tasks themselves always come from the database.
func (s *Service) WithPickSource(p PickSource) *Service {
	s.picks = p
	s.orgPicks, _ = p.(OrgPickSource) // the org held ticket read, taken from the source when it has one (UX-81 round 4)
	return s
}

// orgCtxKey carries the caller's org from a REST handler to the ticket reads under it (UX-81 round 4).
type orgCtxKey struct{}

// withOrg binds the caller's org to a request context; an empty org binds nothing.
func withOrg(ctx context.Context, orgID string) context.Context {
	if orgID == "" {
		return ctx
	}
	return context.WithValue(ctx, orgCtxKey{}, orgID)
}

// pickTicket reads one order's ticket under the org bound to the context when the source can hold it (every REST
// door that reads a ticket binds its caller's org); a context with no org is a system read (the sweep, tests).
func (s *Service) pickTicket(ctx context.Context, orderID string) (PickTicket, error) {
	if org, _ := ctx.Value(orgCtxKey{}).(string); org != "" && s.orgPicks != nil {
		return s.orgPicks.PickTicketInOrg(ctx, org, orderID)
	}
	return s.picks.PickTicket(ctx, orderID)
}

// Stage moves an IN_PROGRESS pick to STAGED and records WHERE the material is parked and under what
// handle, then tells the COUNTER (not the yard) that the order is ready.
//
// Location is required. A staged pick with no location is a pallet somewhere in eleven acres of yard,
// which is strictly worse than an unstaged one, so this rejects rather than accepting a blank.
// Ref defaults to a stable derived handle (STG-XXXXXX) when the picker does not type one, because the
// counter needs SOMETHING short to say out loud.
func (s *Service) Stage(ctx context.Context, in StageInput) (Task, error) {
	if in.TaskID == "" {
		return Task{}, httpx.BadRequestf("task id is required")
	}
	in.Location = strings.TrimSpace(in.Location)
	if in.Location == "" {
		return Task{}, httpx.BadRequestf("stage location is required")
	}
	if len(in.Location) > 120 {
		return Task{}, httpx.BadRequestf("stage location is too long (max 120)")
	}
	in.Ref = strings.TrimSpace(in.Ref)
	if in.Ref == "" {
		in.Ref = "STG-" + shortRef(in.TaskID)
	}
	if len(in.Ref) > 40 {
		return Task{}, httpx.BadRequestf("stage ref is too long (max 40)")
	}
	// Item 9 Opus P2-1: a held order's material does not reach the staging
	// area. The queue row was already marked ("Balance due"); this is the door.
	if err := s.refuseHeldTask(ctx, in.TaskID); err != nil {
		return Task{}, err
	}
	t, err := s.store.Stage(ctx, in)
	if err != nil {
		return Task{}, err
	}
	s.postAudienceEvent(ctx, t, "staged", AudienceCounter, fmt.Sprintf(
		"Order %s is STAGED at %s (ref %s), ready for the customer.", t.OrderID, t.StageLocation, t.StageRef))
	return t, nil
}

// FlagShortage records one shortfall against a pick and pushes it to the COUNTER as a system EVENT.
//
// It does NOT change the task's status. That is deliberate: a short line does not stop the rest of
// the pick, and forcing a status change here would mean the yard has to choose between "keep picking"
// and "tell someone". The shortage is a fact attached to the task; the counter decides what it means.
func (s *Service) FlagShortage(ctx context.Context, in ShortageInput) (Shortage, error) {
	if in.TaskID == "" {
		return Shortage{}, httpx.BadRequestf("task id is required")
	}
	in.Description = strings.TrimSpace(in.Description)
	if in.Description == "" {
		return Shortage{}, httpx.BadRequestf("description is required")
	}
	if len(in.Description) > 400 {
		return Shortage{}, httpx.BadRequestf("description is too long (max 400)")
	}
	qty, err := parsePositiveQty(in.QtyShort)
	if err != nil {
		return Shortage{}, err
	}
	in.QtyShort = qty
	in.UOM = strings.TrimSpace(in.UOM)
	if in.UOM == "" {
		in.UOM = "EA"
	}
	if in.OrderLineNo != nil && *in.OrderLineNo <= 0 {
		return Shortage{}, httpx.BadRequestf("order_line_no must be positive")
	}
	if in.ProductID != "" && !looksLikeUUID(in.ProductID) {
		return Shortage{}, httpx.BadRequestf("product_id must be a uuid")
	}

	sh, task, err := s.store.InsertShortage(ctx, in)
	if err != nil {
		return Shortage{}, err
	}
	// The counter is the audience: the yard already knows it is short. Idempotency key is the
	// shortage row id, so a redelivered post is a silent no-op in messaging.
	s.postShortageEvent(ctx, task, sh)
	return sh, nil
}

// NoticeShortage posts the counter's chat event for a shortage that is already stored: the sync create's
// twin of the post FlagShortage makes after its insert. Best effort like every yard notice: a shortage
// or task that cannot be read posts nothing.
func (s *Service) NoticeShortage(ctx context.Context, taskID, shortageID string) {
	if s.notifier == nil {
		return
	}
	t, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return
	}
	rows, err := s.store.ListShortages(ctx, taskID, false)
	if err != nil {
		return
	}
	for _, sh := range rows {
		if sh.ID == shortageID {
			s.postShortageEvent(ctx, t, sh)
			return
		}
	}
}

// RecordPick writes what the yard actually pulled, line by line (0187).
//
// EVERY LINE THE PICKER TOUCHED, IN ONE CALL, and the whole set is what a "record pick" gesture
// sends. Recording line by line over N requests would let a stage happen between two of them, and a
// ticket half-recorded at the moment it was staged is exactly the record nobody can reason about
// afterwards.
//
// ZERO IS ACCEPTED AND NEGATIVE IS NOT. "We went to pick it and there was none" is a real,
// recordable outcome and the one a counter most needs to hear about; a negative pick is not a fact
// about a yard. This is why it does NOT reuse parsePositiveQty, which the shortage path uses because
// a shortage of nothing is not a shortage.
//
// IT DOES NOT WRITE A SHORTAGE, deliberately. Recording 7 of 10 is a measurement; telling the counter
// is a separate, deliberate act with a note and a resolution attached (FlagShortage above). Making
// this call post one automatically would mean the counter is told about every mid-pick correction,
// and a counter told everything is a counter who reads nothing. Migration 0187's header records the
// rule this rests on: qty_picked is the measurement and task_shortages is the message.
func (s *Service) RecordPick(ctx context.Context, taskID, actorID string, lines []PickedLineInput) ([]PickedLine, error) {
	if taskID == "" {
		return nil, httpx.BadRequestf("task id is required")
	}
	if len(lines) == 0 {
		return nil, httpx.BadRequestf("say what was picked, an empty record is not the same as a pick of nothing")
	}
	if len(lines) > 500 {
		return nil, httpx.BadRequestf("that is more lines than any one ticket carries (max 500)")
	}
	seen := make(map[int]bool, len(lines))
	clean := make([]PickedLineInput, 0, len(lines))
	for _, l := range lines {
		if err := checkPickText(l); err != nil {
			return nil, err
		}
		if l.OrderLineNo <= 0 {
			return nil, httpx.BadRequestf("every recorded line needs the ticket's own line number")
		}
		if seen[l.OrderLineNo] {
			return nil, httpx.BadRequestf("line %d was sent twice, one number per line", l.OrderLineNo)
		}
		seen[l.OrderLineNo] = true

		picked, err := parseNonNegativeQty(l.QtyPicked)
		if err != nil {
			return nil, err
		}
		l.QtyPicked = picked
		ordered, err := parseNonNegativeQty(l.QtyOrdered)
		if err != nil {
			return nil, err
		}
		l.QtyOrdered = ordered

		l.UOM = strings.TrimSpace(l.UOM)
		if l.UOM == "" {
			l.UOM = "EA"
		}
		l.Description = strings.TrimSpace(l.Description)
		if len(l.Description) > 400 {
			// On a character boundary: a cut inside a multibyte character is invalid UTF-8 and fails the write.
			cut := 400
			for cut > 0 && !utf8.RuneStart(l.Description[cut]) {
				cut--
			}
			l.Description = l.Description[:cut]
		}
		if l.ProductID != "" && !looksLikeUUID(l.ProductID) {
			return nil, httpx.BadRequestf("product_id must be a uuid")
		}
		if err := cleanShortReason(&l, s.yardOrders != nil); err != nil {
			return nil, err
		}
		clean = append(clean, l)
	}
	// One transaction holds the task row across the order call (RecordPickLocked): a closed pick takes no
	// record and the order is not told, and a cancel or complete that arrives during the order call waits
	// and lands after the record instead of leaving the order moved with no lines behind it.
	//
	// The order first: it refuses a pick above what the order line holds, and a refused pick must
	// not be recorded. The order's effect sets absolute quantities, so a retry after a failed
	// record converges rather than doubling.
	var res YardPickResult
	picked, err := s.store.RecordPickLocked(ctx, taskID, actorID, func(ctx context.Context, task Task) ([]PickedLineInput, error) {
		r, err := s.applyToOrder(ctx, task, actorID, clean)
		if err != nil {
			return nil, err
		}
		res = r
		// What is stored is what the order holds: its own ordered quantity (not the ticket's) and the
		// reason and note it keeps, so a re-save with none never blanks them.
		byLine := make(map[int]YardLineEffect, len(res.Lines))
		for _, e := range res.Lines {
			byLine[e.LineNo] = e
		}
		for i := range clean {
			if e, ok := byLine[clean[i].OrderLineNo]; ok {
				clean[i].QtyOrdered, clean[i].ShortReason, clean[i].ShortNote = e.QtyOrdered, e.Reason, e.Note
			}
		}
		return clean, nil
	})
	if err != nil {
		return nil, err
	}
	// The rep hears at once, after the record is safe. Best effort: see notifyOrderRep.
	s.noticeShortPicks(ctx, taskID, clean, res)
	return picked, nil
}

// checkPickText refuses text a record could not hold or show safely, before the order is told anything
// (a NUL fails the write after ApplyPicks has already moved the order). Every C0 control, DEL and the
// bidi controls (U+202A to U+202E, U+2066 to U+2069) are refused in every text leg; the note and the
// description, which a picker types or copies on several lines, keep tab, LF and CR. Both doors reach
// this through RecordPick, so a record queued offline is refused as that op's rejection, never a fault.
func checkPickText(l PickedLineInput) error {
	for _, f := range []struct {
		name, val string
		multiline bool
	}{
		{"product id", l.ProductID, false}, {"sku", l.SKU, false}, {"description", l.Description, true},
		{"ordered quantity", l.QtyOrdered, false}, {"picked quantity", l.QtyPicked, false}, {"unit", l.UOM, false},
		{"short reason", l.ShortReason, false}, {"note", l.ShortNote, true},
	} {
		for _, r := range f.val {
			if (r < 0x20 && !(f.multiline && (r == '\t' || r == '\n' || r == '\r'))) || r == 0x7f ||
				(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
				return httpx.BadRequestf("line %d: the %s cannot hold control characters, enter it as plain text", l.OrderLineNo, f.name)
			}
		}
	}
	return nil
}

// applyToOrder hands a recorded pick to the order module, when the port is wired.
func (s *Service) applyToOrder(ctx context.Context, task Task, actorID string, lines []PickedLineInput) (YardPickResult, error) {
	if s.yardOrders == nil {
		return YardPickResult{}, nil
	}
	in := YardPickChange{OrderID: task.OrderID, ActorID: actorID}
	for _, l := range lines {
		in.Lines = append(in.Lines, YardPickLine{
			LineNo: l.OrderLineNo, QtyOrdered: l.QtyOrdered, QtyPicked: l.QtyPicked, UOM: l.UOM,
			Reason: l.ShortReason, Note: l.ShortNote,
		})
	}
	return s.yardOrders.ApplyPicks(ctx, in)
}

// ListPickedLines returns what the yard recorded pulling for one task, in ticket order.
func (s *Service) ListPickedLines(ctx context.Context, taskID string) ([]PickedLine, error) {
	if taskID == "" {
		return nil, httpx.BadRequestf("task id is required")
	}
	return s.store.ListPickedLines(ctx, taskID)
}

// ListShortages returns a task's shortages oldest-first (openOnly filters to unresolved).
func (s *Service) ListShortages(ctx context.Context, taskID string, openOnly bool) ([]Shortage, error) {
	if taskID == "" {
		return nil, httpx.BadRequestf("task id is required")
	}
	return s.store.ListShortages(ctx, taskID, openOnly)
}

// ResolveShortage clears one shortage with a stated resolution and echoes the outcome to the counter
// channel, so the thread that raised the alarm also carries how it ended. Resolving an already
// resolved shortage is ErrShortageResolved, never a silent overwrite of who cleared it first.
func (s *Service) ResolveShortage(ctx context.Context, in ResolveShortageInput) (Shortage, error) {
	if in.TaskID == "" || in.ShortageID == "" {
		return Shortage{}, httpx.BadRequestf("task id and shortage id are required")
	}
	in.Resolution = strings.TrimSpace(in.Resolution)
	if in.Resolution == "" {
		return Shortage{}, httpx.BadRequestf("resolution is required")
	}
	if len(in.Resolution) > 400 {
		return Shortage{}, httpx.BadRequestf("resolution is too long (max 400)")
	}
	sh, task, err := s.store.ResolveShortage(ctx, in)
	if err != nil {
		return Shortage{}, err
	}
	s.postAudienceEvent(ctx, task, "shortage.resolved:"+sh.ID, AudienceCounter, fmt.Sprintf(
		"Shortage cleared on order %s, %s (%s).", task.OrderID, sh.Description, sh.Resolution))
	return sh, nil
}

// PickQueue is the yard board: one page of tasks for a branch, each joined IN MEMORY to its ticket
// (from the PickSource) and its open shortages. There is no SQL join, the ticket comes from another
// system entirely, and the shortages come from this module's own table.
//
// A task whose order the PickSource does not know is STILL RETURNED, with a nil ticket. Hiding it
// would turn a broken feed into an invisible one; a yard lead has to be able to see that a queued
// pick has nothing behind it.
func (s *Service) PickQueue(ctx context.Context, f TaskFilter) ([]PickQueueEntry, error) {
	tasks, err := s.ListTasks(ctx, f)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*Task, len(tasks))
	for i := range tasks {
		ptrs[i] = &tasks[i]
	}
	s.labelClaimers(ctx, ptrs) // one role read for the board (UX-75)
	out := make([]PickQueueEntry, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, s.decorate(ctx, t))
	}
	return out, nil
}

// PickDetail is one queue entry: the task, its ticket, and ALL its shortages (resolved included, the
// history of what went wrong on this pick is part of the pick).
func (s *Service) PickDetail(ctx context.Context, taskID string) (PickQueueEntry, error) {
	if taskID == "" {
		return PickQueueEntry{}, httpx.BadRequestf("task id is required")
	}
	t, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return PickQueueEntry{}, err
	}
	s.labelClaimers(ctx, []*Task{&t}) // the claimer by role, never by name (UX-75)
	e := PickQueueEntry{Task: t}
	if s.picks != nil {
		if ticket, err := s.pickTicket(ctx, t.OrderID); err == nil {
			e.Ticket = &ticket
			e.RunNo = ticket.RunNo
			e.RunDepartsAt = ticket.RunDepartsAt
			e.LoadPosition = ticket.LoadPosition
		} else if s.log != nil {
			s.log.Warn("tasks: pick source has no ticket", "task", t.ID, "order", t.OrderID, "err", err)
		}
	}
	sh, err := s.store.ListShortages(ctx, taskID, false)
	if err != nil {
		return PickQueueEntry{}, err
	}
	e.Shortages = sh
	// What was actually pulled (0187). A read failure DEGRADES the detail rather than failing it: a
	// picker who can see the ticket and the shortages is better served than one shown an error
	// because a second table did not answer. Absent reads as "nobody wrote down what was pulled",
	// which is exactly what it means.
	if picked, perr := s.store.ListPickedLines(ctx, taskID); perr == nil {
		e.Picked = picked
	} else if s.log != nil {
		s.log.Warn("tasks: recorded pick unavailable", "task", taskID, "err", perr)
	}
	return e, nil
}

// PendingTickets exposes the raw feed, orders the source says are waiting, whether or not a task
// exists for them yet. This is the "what have I not even queued" view; ErrNoPickSource when unwired.
func (s *Service) PendingTickets(ctx context.Context, branchID string, limit, offset int) ([]PickTicket, int, error) {
	if s.picks == nil {
		return nil, 0, ErrNoPickSource
	}
	if branchID != "" && !looksLikeUUID(branchID) {
		return nil, 0, httpx.BadRequestf("branch_id must be a uuid")
	}
	return s.picks.PendingTickets(ctx, branchID, limit, offset)
}

// SweepStalePicks is the nightly job body (registered with internal/jobs at the composition root): it
// finds PICK tasks still QUEUED past the staleness window and posts ONE nudge per task into its yard
// channel. It changes no status, a sweep that silently cancelled work would be a data-loss bug
// wearing a housekeeping hat. The EventKey is bucketed per UTC day so re-running the sweep on the
// same day is a no-op in messaging rather than a second ping.
//
// Returns (scanned, notified).
func (s *Service) SweepStalePicks(ctx context.Context, olderThan time.Duration, limit int) (int, int, error) {
	if olderThan <= 0 {
		olderThan = 12 * time.Hour
	}
	if limit <= 0 {
		limit = 200
	}
	cutoff := time.Now().Add(-olderThan)
	stale, err := s.store.StalePickTasks(ctx, cutoff, limit)
	if err != nil {
		return 0, 0, err
	}
	day := time.Now().UTC().Format("2006-01-02")
	notified := 0
	for _, t := range stale {
		age := time.Since(t.CreatedAt).Round(time.Hour)
		if s.postAudienceEventKeyed(ctx, t, "stale:"+day, "task.stale", AudienceYard, fmt.Sprintf(
			"PICK task for order %s has been queued for %s and is still unclaimed.", t.OrderID, age)) {
			notified++
		}
	}
	return len(stale), notified, nil
}

// decorate joins one task to its ticket + open shortages. Both lookups are best-effort reads: a
// failing PickSource or shortage read degrades the row, it does not fail the whole board.
func (s *Service) decorate(ctx context.Context, t Task) PickQueueEntry {
	e := PickQueueEntry{Task: t}
	if s.picks != nil {
		if ticket, err := s.pickTicket(ctx, t.OrderID); err == nil {
			e.Ticket = &ticket
			e.RunNo = ticket.RunNo
			e.RunDepartsAt = ticket.RunDepartsAt
			e.LoadPosition = ticket.LoadPosition
		}
	}
	if t.OpenShortages > 0 {
		if sh, err := s.store.ListShortages(ctx, t.ID, true); err == nil {
			e.Shortages = sh
		}
	}
	return e
}

// postShortageEvent builds the counter-facing EVENT for one shortage. Kept separate from the generic
// helper because its body is the message a human at the counter reads to a customer, it names the
// order, the quantity, the item and the note, in that order.
func (s *Service) postShortageEvent(ctx context.Context, t Task, sh Shortage) {
	body := fmt.Sprintf("SHORTAGE on order %s, short %s %s of %s.",
		t.OrderID, sh.QtyShort, sh.UOM, sh.Description)
	if sh.Note != "" {
		body += " Yard note: " + sh.Note
	}
	// The yard channel hears first and the counter's notice stays the last post, the one the counter acts on.
	s.postShortageYardNotice(ctx, t, sh)
	s.postAudienceEventKeyed(ctx, t, "shortage:"+sh.ID, "task.shortage", AudienceCounter, body)
}

// postShortageYardNotice tells the branch's yard channel that a shortage was reported on a pick, beside the
// counter's own notice (UX-76: anyone in the branch may report one, and the holder, who is in that channel,
// must hear it). It names the pick and the reporter's role label, never a name or an id.
func (s *Service) postShortageYardNotice(ctx context.Context, t Task, sh Shortage) {
	who := "a teammate"
	if s.roleLabels != nil && sh.ReportedBy != "" {
		if labels, err := s.roleLabels.RoleLabelsForUsers(ctx, []string{sh.ReportedBy}); err == nil && labels[sh.ReportedBy] != "" {
			who = "the " + labels[sh.ReportedBy]
		}
	}
	pick := "a pick on this branch"
	if e := s.decorate(ctx, t); e.Ticket != nil && strings.TrimSpace(e.Ticket.OrderNo) != "" {
		pick = "pick " + strings.TrimSpace(e.Ticket.OrderNo)
	}
	body := fmt.Sprintf("A shortage was reported on %s by %s: short %s %s of %s.", pick, who, sh.QtyShort, sh.UOM, sh.Description)
	s.postAudienceEventKeyed(ctx, t, "shortage.yard:"+sh.ID, "task.shortage_yard", AudienceYard, body)
}

// postAudienceEvent posts a lifecycle EVENT with an explicit audience, keying idempotency on the
// moment (one post per task per moment).
func (s *Service) postAudienceEvent(ctx context.Context, t Task, moment string, aud Audience, summary string) {
	s.postAudienceEventKeyed(ctx, t, moment, "task."+strings.SplitN(moment, ":", 2)[0], aud, summary)
}

// postAudienceEventKeyed is the single chat-post path for the yard flow. Best-effort by contract (the
// FR-3 non-owning-observer doctrine): a nil notifier is a no-op and a post error is logged, never
// returned, a messaging blip must never fail a yard write. Returns whether the post was attempted
// AND succeeded, which is what the sweep counts.
func (s *Service) postAudienceEventKeyed(ctx context.Context, t Task, keyPart, topic string, aud Audience, summary string) bool {
	if s.notifier == nil {
		return false
	}
	ref := TaskEventRef{
		EventKey: "task:" + t.ID + ":" + keyPart,
		BranchID: t.BranchID,
		Topic:    topic,
		Entity:   "task:" + t.ID,
		DeepLink: "/app/yard/" + t.ID,
		Summary:  summary,
		Audience: aud,
	}
	if err := s.notifier.PostTaskEvent(ctx, ref); err != nil {
		if s.log != nil {
			s.log.Warn("tasks: yard chat notify failed", "task", t.ID, "key", keyPart, "err", err)
		}
		return false
	}
	return true
}

// parsePositiveQty validates a DECIMAL-as-string quantity is a well-formed positive number and
// returns it normalised to 4 decimal places (the NUMERIC(19,4) the column stores). Rejecting here
// rather than at the database keeps a bad quantity a 400 instead of a 500.
func parsePositiveQty(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", httpx.BadRequestf("qty_short is required")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return "", httpx.BadRequestf("qty_short must be a number")
	}
	if f <= 0 {
		return "", httpx.BadRequestf("qty_short must be greater than zero")
	}
	if f > 1e12 {
		return "", httpx.BadRequestf("qty_short is implausibly large")
	}
	return strconv.FormatFloat(f, 'f', 4, 64), nil
}

// parseNonNegativeQty is parsePositiveQty's twin for a RECORDED PICK, and the difference between
// them is the whole point of having two.
//
// A SHORTAGE OF ZERO IS NOT A SHORTAGE, nothing to tell the counter, so parsePositiveQty refuses
// it. A PICK of zero is "we went to pick it and there was none", which is a real outcome and the one
// a counter most needs to hear about; refusing it would force a picker to either lie or say nothing,
// and saying nothing is what leaves a ticket looking un-picked when somebody did in fact walk out to
// the bay. Negative is still refused: it is not a fact about a yard.
func parseNonNegativeQty(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", httpx.BadRequestf("say how many were picked, zero is an answer, blank is not")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return "", httpx.BadRequestf("a picked quantity must be a number")
	}
	if f < 0 {
		return "", httpx.BadRequestf("a picked quantity cannot be negative")
	}
	if f > 1e12 {
		return "", httpx.BadRequestf("that picked quantity is implausibly large")
	}
	return strconv.FormatFloat(f, 'f', 4, 64), nil
}
