package messagingprovider

import (
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// templates.go, the two message bodies this program sends out of band, and the payload shapes that
// feed them.
//
// A TEXT MESSAGE IS NOT A LOG LINE. These bodies are written to be read on a phone by a contractor
// standing on a job site: they lead with what happened, name the reference the person will be asked
// for at the counter, and end with who sent it. They carry NO link (a link in an unsolicited text
// reads as phishing and gets carriers to block the sender), NO account balance detail beyond the
// amount owing, and NO personal data beyond the recipient's own name.
//
// LENGTH. A GSM-7 segment is 160 characters; past that a message splits and is billed per segment.
// Both templates are built to fit one segment for typical values, and Render() TRUNCATES rather than
// silently sending three segments, the cap is stated, not accidental.

// smsSegment is the single-segment budget the templates aim at.
const smsSegment = 160

// senderName is the signature on every outbound message. A text with no sender name is the single
// biggest reason people report a message as spam.
const senderName = "Dibbits"

// ARNotice is the accounts-receivable payload (topic sms.ar.notice). AmountCents is the balance the
// customer owes; DaysPastDue distinguishes a courtesy reminder from a collections nudge, and the
// template's tone follows it.
type ARNotice struct {
	InvoiceID   string `json:"invoice_id"`
	InvoiceNo   string `json:"invoice_no"`
	CustomerID  string `json:"customer_id"`
	AmountCents int64  `json:"amount_cents"`
	DueDate     string `json:"due_date"`      // ISO date, as it will be read aloud
	DaysPastDue int    `json:"days_past_due"` // 0 = not yet due
	// To is an OPTIONAL pre-resolved E.164 recipient. When empty the consumer resolves it through
	// the RecipientResolver seam. It exists so a producer that already knows the number (a portal
	// action, a manual send) does not force a second lookup.
	To string `json:"to,omitempty"`
}

// DispatchNotice is the fulfilment payload (topic sms.dispatch.notice), emitted by the yard when a
// pick is STAGED. StageRef is the handle the customer will be asked for at the counter, so it is the
// one field the template must never drop.
type DispatchNotice struct {
	TaskID        string `json:"task_id"`
	OrderID       string `json:"order_id"`
	OrderNo       string `json:"order_no"`
	BranchID      string `json:"branch_id"`
	BranchName    string `json:"branch_name"`
	StageLocation string `json:"stage_location"`
	StageRef      string `json:"stage_ref"`
	To            string `json:"to,omitempty"`
}

// RenderAR builds the AR text for a recipient. Past-due wording is firmer than a courtesy reminder,
// because a contractor who is 45 days late and a contractor whose invoice is due Friday need
// different messages and would rightly resent receiving the same one.
func RenderAR(n ARNotice, r Recipient) notify.Message {
	ref := n.InvoiceNo
	if ref == "" {
		ref = shortID(n.InvoiceID)
	}
	amount := formatCAD(n.AmountCents)

	var body string
	switch {
	// UNION-W5 (C's escalation): a STATEMENT notice must not read as an invoice. Statement runs
	// reuse sms.ar.notice (correctly, one AR topic, one consent path) with a statement-shaped
	// reference; the WORDING branches on it here rather than minting a second topic.
	case strings.HasPrefix(ref, "STMT"):
		body = fmt.Sprintf("%s: your account statement %s is ready (balance %s). Reply or call us with any questions., %s",
			greet(r), ref, amount, senderName)
	case n.DaysPastDue > 0:
		body = fmt.Sprintf("%s: invoice %s is %d days past due (%s). Please call us to settle or arrange terms., %s",
			greet(r), ref, n.DaysPastDue, amount, senderName)
	case n.DueDate != "":
		body = fmt.Sprintf("%s: invoice %s for %s is due %s., %s",
			greet(r), ref, amount, n.DueDate, senderName)
	default:
		body = fmt.Sprintf("%s: invoice %s for %s is outstanding., %s",
			greet(r), ref, amount, senderName)
	}
	return notify.Message{Channel: notify.SMSChannel, To: r.Phone, Body: clampSegment(body)}
}

// RenderDispatch builds the fulfilment text. It leads with "ready", names the order, and gives the
// stage reference, in that order, because that is the order the information is needed in.
func RenderDispatch(n DispatchNotice, r Recipient) notify.Message {
	ref := n.OrderNo
	if ref == "" {
		ref = shortID(n.OrderID)
	}
	where := n.BranchName
	if where == "" {
		where = "the yard"
	}

	body := fmt.Sprintf("%s: order %s is picked and ready at %s.", greet(r), ref, where)
	if n.StageRef != "" {
		body += fmt.Sprintf(" Quote ref %s at the counter.", n.StageRef)
	}
	body += ", " + senderName
	return notify.Message{Channel: notify.SMSChannel, To: r.Phone, Body: clampSegment(body)}
}

// greet opens with the recipient's name when we have one, and with "Hi" when we do not. A message
// addressed to nobody reads like a scam.
func greet(r Recipient) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return "Hi"
	}
	// First name only, a text is not correspondence.
	if i := strings.IndexByte(name, ' '); i > 0 {
		name = name[:i]
	}
	return "Hi " + name
}

// formatCAD renders cents as $1,234.56. Money is int64 cents everywhere in this program and is
// formatted exactly once, here, on the way out.
func formatCAD(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	dollars := cents / 100
	frac := cents % 100

	digits := fmt.Sprintf("%d", dollars)
	var grouped []byte
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped = append(grouped, ',')
		}
		grouped = append(grouped, digits[i])
	}
	out := fmt.Sprintf("$%s.%02d", string(grouped), frac)
	if neg {
		out = "-" + out
	}
	return out
}

// shortID is the fallback reference when a payload carries an id but no human number. It is the last
// segment of a uuid, uppercased, short enough to read aloud, and unmistakably not an order number.
func shortID(id string) string {
	if id == "" {
		return "(unknown)"
	}
	if i := strings.LastIndexByte(id, '-'); i >= 0 && i+1 < len(id) {
		id = id[i+1:]
	}
	// The LAST 8 characters, not the first: ids minted in a batch share their leading characters, so
	// a leading-slice reference would read identically for two different orders.
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return strings.ToUpper(id)
}

// clampSegment truncates a body that would spill past one SMS segment, ending it with an ellipsis so
// the truncation is visible to the reader rather than looking like a message that just stopped.
func clampSegment(body string) string {
	r := []rune(body)
	if len(r) <= smsSegment {
		return body
	}
	return string(r[:smsSegment-1]) + "…"
}
