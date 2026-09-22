package assist

// commands_activity.go, DIB-494. "Something went wrong with this order, can you tell me the
// activity on it?"
//
// ONE command, because it is one question. The order's audit trail already has two staff surfaces
// (the status timeline and the edit history) and this answer is their union spoken aloud: every
// recorded change, who made it, and when, newest first, because the person asking is almost always
// asking about the LAST thing that happened.
//
// THE RULES THIS FILE INHERITS AND DOES NOT RESTATE (commands.go, commands_domain.go):
//
//	READ-ONLY IS STRUCTURAL, OrderActivityReader exposes no writer and takes no tx.
//	EMPTY IS AN ANSWER, an order that has never moved or been edited is a quiet sentence, not a gap.
//	ONE FORMATTER, money in a before→after renders through format.go's money(), so the trail and
//	  the order screens cannot disagree about the same figure.
//
// AND THE ONE THAT IS THIS FILE'S OWN: THE ANSWER SHOWS EXACTLY WHAT THE SCREENS SHOW. The seam
// behind this command is fenced to the union of the two shipped audit surfaces, resolved through
// the same branch-scoped order lookup as every other order question, the assistant is a faster way
// to read a history the app already shows this person, never a wider one.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ActivityCommands is the DIB-494 command set, appended after the DIB-478 commands so the menu
// order testers learned is unchanged.
func ActivityCommands() []Command {
	return []Command{
		{
			Code:    "order.activity",
			Title:   "Activity on an order",
			Summary: "Everything recorded on an order, status moves and edits, who made each one, and when.",
			// The same floor as order.lookup and the order-history screens themselves: the history
			// routes are staff-gated (any tier), and the branch fence inside the read does the rest.
			MaxTier: 3,
			Args: []ArgSpec{
				{Name: "order_no", Label: "Order number", Example: "SO-4", Required: true},
			},
			run: runOrderActivity,
		},
	}
}

// -- order.activity -----------------------------------------------------------------------------

func runOrderActivity(ctx context.Context, deps Deps, c Caller, a Args) (Result, error) {
	if deps.Activity == nil {
		return Result{}, errUnwired("order activity")
	}
	no := a.get("order_no")
	if no == "" {
		return Result{}, badArg("Which order? Give me the number, like SO-4.")
	}
	o, events, found, err := deps.Activity.ActivityFor(ctx, c, no)
	if err != nil {
		return Result{}, err
	}
	if !found {
		// The same sentence order.lookup says, because it is the same situation seen through the
		// same fence, one wording per situation is the DIB-443 rule.
		return Result{
			Command:  "order.activity",
			Headline: fmt.Sprintf("I can't find an order numbered %s.", no),
			Empty:    true,
			Note:     "Check the number, or it may belong to a branch you don't have access to.",
		}, nil
	}
	if len(events) == 0 {
		return Result{
			Command:  "order.activity",
			Headline: fmt.Sprintf("Nothing has been recorded on %s yet.", o.OrderNo),
			Empty:    true,
			Note:     "Its trail starts the first time its status moves or a detail is edited, this order hasn't done either.",
			Ref:      refNamed(RefOrder, o.OrderID, o.OrderNo),
		}, nil
	}

	// NEWEST FIRST. The trail arrives oldest→newest (the causal order the screens use), and the
	// answer walks it backwards, because "what was the last change?" is the question under every
	// phrasing of this question.
	last := events[len(events)-1]
	items := make([]Item, 0, maxItems)
	for i := len(events) - 1; i >= 0 && len(items) < maxItems; i-- {
		items = append(items, activityItem(events[i]))
	}
	res := Result{
		Command: "order.activity",
		Headline: fmt.Sprintf("%d recorded %s on %s, the most recent was %s by %s on %s.",
			len(events), plural(len(events), "change", "changes"), o.OrderNo,
			eventPhrase(last), actorWord(last.ActorName, last.ActorID), day(last.At)),
		Items: items,
		Ref:   refNamed(RefOrder, o.OrderID, o.OrderNo),
	}
	if len(events) > maxItems {
		res.Note = fmt.Sprintf("Showing the most recent %d of %d changes, the order's own History and Edits panels have the rest.", maxItems, len(events))
	}
	return res, nil
}

// activityItem renders one event as a card the panel and the sentence agree about.
func activityItem(e OrderAuditEvent) Item {
	if e.Kind == AuditKindStatus {
		fields := []Field{
			{Label: "When", Value: day(e.At)},
			{Label: "By", Value: actorWord(e.ActorName, e.ActorID)},
		}
		if strings.TrimSpace(e.Reason) != "" {
			fields = append(fields, Field{Label: "Reason", Value: e.Reason})
		}
		return Item{Title: statusPhrase(e), Fields: fields}
	}
	return Item{
		Title: editTitle(e),
		Fields: []Field{
			{Label: "From", Value: changeValue(e.Field, e.Before)},
			{Label: "To", Value: changeValue(e.Field, e.After)},
			{Label: "When", Value: day(e.At)},
			{Label: "By", Value: actorWord(e.ActorName, e.ActorID)},
		},
	}
}

// eventPhrase is the headline's clause for one event: what the last change actually WAS, in the
// order it reads aloud.
func eventPhrase(e OrderAuditEvent) string {
	if e.Kind == AuditKindStatus {
		if strings.TrimSpace(e.FromStatus) == "" {
			return "its status being set to " + statusWord(e.ToStatus)
		}
		return "its status moving to " + statusWord(e.ToStatus)
	}
	return strings.ToLower(fieldWord(e.Field)) + " changing"
}

// statusPhrase titles a status event. The creation transition (empty from) is named as what it is
// rather than rendered "- → CONFIRMED", which reads as a broken cell.
func statusPhrase(e OrderAuditEvent) string {
	if strings.TrimSpace(e.FromStatus) == "" {
		return "Status set to " + statusWord(e.ToStatus)
	}
	return "Status: " + statusWord(e.FromStatus) + " → " + statusWord(e.ToStatus)
}

// statusWord renders a status code the way the order-history screen does ("PARTIALLY_FULFILLED" →
// "Partially fulfilled"), readable, and still recognisably the code.
func statusWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	words := strings.Split(strings.ToLower(s), "_")
	if len(words[0]) > 0 {
		words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	}
	return strings.Join(words, " ")
}

// editTitle titles a field edit: the human field name, with its line when it is a line edit.
func editTitle(e OrderAuditEvent) string {
	t := fieldWord(e.Field)
	if e.LineNo > 0 {
		t += fmt.Sprintf(" (line %d)", e.LineNo)
	}
	return t
}

// fieldWord is the audit `field` vocabulary in human words, the SAME labels the order edit-history
// screen uses (_order-edit.ts FIELD_LABEL), stated here because the command layer is where sentences
// are made. An unknown field passes through verbatim rather than becoming "Other": a later ticket's
// new field should read as itself (the fulfilmentWord rule, applied again).
func fieldWord(field string) string {
	switch field {
	case "qty_entered":
		return "Quantity"
	case "qty_base":
		return "Quantity (base)"
	case "unit_price_cents":
		return "Unit price"
	case "line_total_cents":
		return "Line total"
	case "subtotal_cents":
		return "Order subtotal"
	case "total_cents":
		return "Order total"
	case "requested_date":
		return "Requested date"
	case "line_added":
		return "Line added"
	case "line_removed":
		return "Line removed"
	case "delivery_instructions":
		return "Delivery instructions"
	default:
		return field
	}
}

// moneyFieldSet is which audit fields carry integer CENTS, the same set the edit-history screen
// holds (_order-edit.ts MONEY_FIELDS). Rendering "125000" where the screen shows "$1,250.00" would
// be two descriptions of one number, which is the drift format.go exists to prevent.
func isMoneyField(field string) bool {
	switch field {
	case "unit_price_cents", "line_total_cents", "subtotal_cents", "total_cents":
		return true
	}
	return false
}

// changeValue renders one side of a before→after pair. An empty value is an em-dash, a creation's
// `before` and a removal's `after` legitimately are empty, and a blank cell reads as a rendering
// bug. Money fields render as money; an unparseable money value passes through verbatim rather than
// printing $0.00 for a value that was not zero.
func changeValue(field, v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	if isMoneyField(field) {
		if cents, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return money(cents)
		}
	}
	return v
}

// actorWord resolves who made a change, exactly as the order screens do (order-history.ts who()):
// a staff display name, else a short id, else "system" for a portal/automated change that carries
// no attribution.
func actorWord(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	if len(id) >= 8 {
		return id[:8]
	}
	if id != "" {
		return id
	}
	return "system"
}
