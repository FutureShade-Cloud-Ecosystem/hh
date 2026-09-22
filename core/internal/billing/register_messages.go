package billing

// register_messages.go, the words a reminder actually says, and the invoice document itself.
//
// -- WHY THE TONE IS THREE TEMPLATES AND NOT A SLIDER ------------------------------------------
// A collections message has exactly three registers and everybody in the trade knows which one they
// are in: a nudge to somebody who has probably just not opened the email, a firm note to somebody who
// has, and a final notice that names what happens next. Anything between them is a sentence somebody
// has to compose under time pressure to a customer they are annoyed with, which is how a supplier
// loses an account over four hundred dollars.
//
// So the tone is a CHOICE FROM THREE, the words are written here once, and the only variables are the
// facts. What the tone changes is the ASK, not the amount, not the date, not the politeness of the
// greeting, which stays the same in all three because the customer is still a customer in the third
// one. FINAL is the only one that names a consequence, and it names the one this system can actually
// carry out (the account stops) rather than inventing a legal threat nobody authorised.
//
// -- THE INVOICE DOCUMENT ----------------------------------------------------------------------
// renderInvoiceHTML produces the self-contained document behind Preview / Print. Self-contained with
// inline styles and no external assets, exactly as ar_statements' HTML is (0105) and for the same
// reason: it must render identically in a print dialog, in an email body, and in an archive five
// years from now, on a machine that cannot reach this server.
//
// There is no PDF library in this stack and this file does not pretend otherwise. "Preview PDF" opens
// this document and the browser's own print-to-PDF makes the file, which is what the counter
// actually does today with every other document this system prints.

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
)

// money formats integer cents for a customer-facing document, WITH thousands grouping.
//
// The grouping matters more here than anywhere else on the screen: "CA$10486.40" and "CA$1048.64"
// differ by one character in a column somebody is going to read across a desk, and a printed invoice
// is the one artefact nobody can hover over to check. The register renders the same figures the same
// way, so the paper and the screen agree character for character.
func money(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("CA$%s.%02d", group(cents/100), cents%100)
	if neg {
		return "−" + s
	}
	return s
}

// group inserts thousands separators into a non-negative whole number.
func group(n int64) string {
	digits := strconv.FormatInt(n, 10)
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

func reminderSubject(tone, invoiceNo string) string {
	switch tone {
	case ToneFinal:
		return "Final notice, invoice " + invoiceNo
	case ToneFirm:
		return "Overdue, invoice " + invoiceNo
	default:
		return "Invoice " + invoiceNo
	}
}

// reminderBody writes the chase. The facts are identical across tones on purpose: a customer who gets
// a friendly note and then a final one should be able to see they are about the same money.
func reminderBody(tone, customer, invoiceNo string, balanceCents int64, due time.Time) string {
	facts := fmt.Sprintf("Invoice %s · %s outstanding · due %s.",
		invoiceNo, money(balanceCents), due.Format("2 January 2006"))
	switch tone {
	case ToneFinal:
		return fmt.Sprintf("Hello %s,\n\n%s\n\nThis is a final notice. Unless we hear from you, the account "+
			"will be placed on hold and further orders will need to be settled at the counter.\n\n"+
			"If there is a problem with this invoice, tell us and we will stop the clock while we sort it out.\n",
			customer, facts)
	case ToneFirm:
		return fmt.Sprintf("Hello %s,\n\n%s\n\nThis one is past due. Could you let us know when it will be "+
			"settled, or tell us if something on it is wrong?\n", customer, facts)
	default:
		return fmt.Sprintf("Hello %s,\n\n%s\n\nA reminder in case it slipped past, no action needed if it is "+
			"already on its way.\n", customer, facts)
	}
}

func invoiceBody(customer, invoiceNo string, balanceCents int64, due time.Time) string {
	return fmt.Sprintf("Hello %s,\n\nInvoice %s is attached · %s · due %s.\n\n"+
		"Any questions, reply to this message and it reaches the branch that raised it.\n",
		customer, invoiceNo, money(balanceCents), due.Format("2 January 2006"))
}

// renderInvoiceHTML builds the printable document.
//
// EVERY FIGURE IS PASSED IN. This function computes nothing: it is handed the detail the server
// already assembled and it lays it out. A renderer that added a subtotal would be a second opinion
// about what an invoice says, printed on the copy the customer keeps.
func renderInvoiceHTML(d InvoiceDetail, branchName string) string {
	var b strings.Builder
	esc := html.EscapeString
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>Invoice ` + esc(d.InvoiceNo) + `</title><style>
    body{font:14px/1.5 system-ui,-apple-system,'Segoe UI',sans-serif;color:#111;margin:0;padding:32px;background:#fff}
    .wrap{max-width:760px;margin:0 auto}
    h1{font-size:22px;margin:0 0 2px}
    .sub{color:#555;margin:0 0 24px}
    .grid{display:flex;gap:32px;flex-wrap:wrap;margin-bottom:24px}
    .grid section{flex:1 1 220px}
    h2{font-size:11px;letter-spacing:.12em;text-transform:uppercase;color:#666;margin:0 0 6px}
    table{width:100%;border-collapse:collapse;margin-bottom:16px}
    th,td{text-align:left;padding:8px 6px;border-bottom:1px solid #e3e3e3;vertical-align:top}
    th{font-size:11px;letter-spacing:.08em;text-transform:uppercase;color:#666}
    td.n,th.n{text-align:right;font-variant-numeric:tabular-nums}
    .totals{margin-left:auto;width:280px}
    .totals td{border:0;padding:4px 6px}
    .totals tr.due td{border-top:2px solid #111;font-weight:700;font-size:16px;padding-top:10px}
    .stamp{margin-top:24px;color:#666;font-size:12px}
    @media print{body{padding:0}}
  </style></head><body><div class="wrap">`)

	b.WriteString(`<h1>Invoice ` + esc(d.InvoiceNo) + `</h1>`)
	b.WriteString(`<p class="sub">` + esc(branchName) + ` · issued ` + d.IssueDate.Format("2 January 2006") +
		` · due ` + d.DueDate.Format("2 January 2006") + `</p>`)

	b.WriteString(`<div class="grid"><section><h2>Billed to</h2>`)
	b.WriteString(`<div>` + esc(d.Customer.Name) + `</div>`)
	b.WriteString(`<div>Account ` + esc(d.Customer.AccountNo) + `</div>`)
	if d.Customer.ContactName != "" {
		b.WriteString(`<div>` + esc(d.Customer.ContactName) + `</div>`)
	}
	if d.Customer.ContactEmail != "" {
		b.WriteString(`<div>` + esc(d.Customer.ContactEmail) + `</div>`)
	}
	b.WriteString(`</section><section><h2>Reference</h2>`)
	if d.OrderNo != "" {
		b.WriteString(`<div>Order ` + esc(d.OrderNo) + `</div>`)
	}
	if d.CustomerPO != "" {
		b.WriteString(`<div>Your PO ` + esc(d.CustomerPO) + `</div>`)
	}
	b.WriteString(`<div>Terms net ` + fmt.Sprint(d.Customer.TermsDays) + `</div>`)
	if d.Salesperson != "" {
		b.WriteString(`<div>` + esc(d.Salesperson) + `</div>`)
	}
	b.WriteString(`</section></div>`)

	b.WriteString(`<table><thead><tr><th>Item</th><th>Description</th><th class="n">Qty</th><th>Unit</th>` +
		`<th class="n">Unit price</th><th class="n">Amount</th></tr></thead><tbody>`)
	for _, l := range d.Lines {
		b.WriteString(`<tr><td>` + esc(l.SKU) + `</td><td>` + esc(l.Description) + `</td>` +
			`<td class="n">` + esc(trimQty(l.QtyEntered)) + `</td><td>` + esc(l.UOMEntered) + `</td>` +
			`<td class="n">` + money(l.UnitPriceCents) + `</td>` +
			`<td class="n">` + money(l.LineTotalCents) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	b.WriteString(`<table class="totals"><tbody>`)
	b.WriteString(`<tr><td>Subtotal</td><td class="n">` + money(d.Totals.SubtotalCents) + `</td></tr>`)
	b.WriteString(`<tr><td>HST</td><td class="n">` + money(d.Totals.TaxCents) + `</td></tr>`)
	b.WriteString(`<tr><td>Total</td><td class="n">` + money(d.Totals.TotalCents) + `</td></tr>`)
	if d.Totals.AppliedCents != 0 {
		b.WriteString(`<tr><td>Deposit &amp; payments</td><td class="n">−` + money(d.Totals.AppliedCents) + `</td></tr>`)
	}
	if d.Totals.HoldbackCents != 0 {
		b.WriteString(`<tr><td>Holdback retained</td><td class="n">` + money(d.Totals.HoldbackCents) + `</td></tr>`)
	}
	b.WriteString(`<tr class="due"><td>Balance due</td><td class="n">` + money(d.Totals.BalanceCents) + `</td></tr>`)
	b.WriteString(`</tbody></table>`)

	if d.Proof != nil && d.Proof.Delivered && d.Proof.SignedBy != "" {
		at := ""
		if d.Proof.SignedAt != nil {
			at = " on " + d.Proof.SignedAt.Format("2 January 2006")
		}
		b.WriteString(`<p class="stamp">Delivered to ` + esc(d.Proof.Address) + `, received by ` +
			esc(d.Proof.SignedBy) + esc(at) + `.</p>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

// trimQty drops the trailing zeros a DECIMAL(19,4) carries so a document says "18" rather than
// "18.0000". Display only, the stored scale is untouched.
func trimQty(q string) string {
	if !strings.Contains(q, ".") {
		return q
	}
	q = strings.TrimRight(q, "0")
	return strings.TrimSuffix(q, ".")
}
