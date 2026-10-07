package printing

import (
	"bytes"
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
)

// RenderReceiptHTML renders the receipt as a self-contained print document.
//
// WHY SERVER-RENDERED HTML. The counter has no printer driver, no PDF toolchain and no device
// agent, it has a browser with a print dialog. A single self-contained HTML page (no external CSS,
// no fonts, no scripts) opens in a new window, prints on 80mm thermal paper or on letter, and is the
// same bytes whether it is printed now or reprinted from an order lookup next month. It is also the
// only rendering that needs no build step to change.
//
// html/template (NOT text/template) is the escaping boundary: every value on this page, product
// descriptions, the customer's name, an operator's note, is customer-supplied text that lands
// inside markup.
func RenderReceiptHTML(r Receipt) (string, error) {
	var buf bytes.Buffer
	if err := receiptTmpl.Execute(&buf, r); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RenderReceiptPageHTML renders the FULL-PAGE receipt (the route's format=page): the same Receipt
// struct as the roll, so the two documents can never disagree about what the sale was, laid out as
// a document for a customer who wants one.
//
// PROVENANCE (row 666 item 2): this format is HH's own. The reference product carries no full-page
// receipt, no reference file exists for it, and this template is this repository's own work: the
// letterhead, the rows and the honesty are the roll document's (the DIB-410 lineage), the page is
// ours. The same escaping boundary applies: html/template renders every customer-supplied value.
func RenderReceiptPageHTML(r Receipt) (string, error) {
	var buf bytes.Buffer
	if err := receiptPageTmpl.Execute(&buf, r); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Money renders cents as "$1,234.56", the one place receipt money is formatted.
func Money(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	// thousands separators, right to left
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := fmt.Sprintf("$%s.%02d", b.String(), cents%100)
	if neg {
		return "-" + out
	}
	return out
}

// Qty renders a quantity without trailing-zero noise ("10" not "10.000000").
func Qty(q float64) string { return strconv.FormatFloat(q, 'f', -1, 64) }

// saleTypeWords maps a pos sale type (core/internal/pos/model.go) to the plain words the receipt
// prints: the customer's paper carries words, not domain constants. The cases are keyed on pos's
// own constants so the vocabulary cannot drift. The words make no payment claim (an ACCOUNT tender
// can ride any sale type, the tender rows are what say how it was paid) and no time-relative claim
// (a reprint must read as true as the first copy). An unknown value passes through verbatim:
// nothing is hidden, nothing is guessed. Empty never reaches here, the template's if stands.
func saleTypeWords(saleType string) string {
	switch saleType {
	case pos.SaleCollectNow:
		// counter pickup: stock issues and the order fulfils at ring, the goods leave with the customer
		return "Collected at the counter"
	case pos.SaleCollectLater:
		// will-call: the mode, named, never the goods' current state (a will-call pickup
		// confirmation fulfils the order later, and the sale type on a reprint never changes)
		return "For later collection"
	case pos.SaleDelivered:
		// the fulfilment mode, named: never a claim the delivery already happened
		return "Delivery"
	case pos.SaleCOD:
		// a payment posture, not a fulfilment, stated as the sale's standing terms: the invoice
		// is FINALIZED at ring (its number is on this same paper) and fulfilment waits for
		// payment, whatever the balance's later state
		return "Invoice issued, payment before fulfilment"
	default:
		return saleType
	}
}

var receiptTmpl = template.Must(template.New("receipt").Funcs(template.FuncMap{
	"money":     Money,
	"qty":       Qty,
	"saleWords": saleTypeWords,
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Receipt {{.OrderNo}}</title>
<style>
  /* Print CSS: the page is the artifact. Margins are the printer's, colours are ink-safe, and the
     layout collapses to a single column so 80mm thermal and letter both come out readable. */
  @page { margin: 10mm; }
  * { box-sizing: border-box; }
  body {
    font-family: ui-monospace, "SFMono-Regular", "Menlo", "Consolas", monospace;
    font-size: 12px; line-height: 1.45; color: #000; background: #fff;
    max-width: 80mm; margin: 0 auto; padding: 8px;
  }
  h1 { font-size: 14px; margin: 0 0 2px; letter-spacing: .02em; }
  .muted { color: #444; }
  .center { text-align: center; }
  .right { text-align: right; }
  .rule { border-top: 1px dashed #000; margin: 8px 0; }
  .order-no { font-size: 20px; font-weight: 700; letter-spacing: .04em; margin: 6px 0 2px; }
  table { width: 100%; border-collapse: collapse; }
  td, th { padding: 1px 0; vertical-align: top; }
  th { text-align: left; font-weight: 700; border-bottom: 1px solid #000; }
  .desc { word-break: break-word; }
  .totals td { padding: 1px 0; }
  .grand { font-weight: 700; font-size: 14px; border-top: 1px solid #000; }
  .reprint {
    border: 2px solid #000; padding: 4px; margin: 6px 0; text-align: center;
    font-weight: 700; letter-spacing: .12em;
  }
  .foot { margin-top: 10px; font-size: 10px; }
  /* The product mark is the quietest thing on the page and stays that way in print: no colour to
     lose on a thermal printer, one step smaller than the foot, and tracked out so it reads as a
     mark rather than a sentence. */
  .mark { margin-top: 4px; font-size: 9px; letter-spacing: .06em; color: #555; }
  @media print { .noprint { display: none !important; } }
</style>
</head>
<body>
  <header class="center">
    {{if .OrgName}}<h1>{{.OrgName}}</h1>{{end}}
    <div>{{.BranchName}}{{if .BranchCode}} ({{.BranchCode}}){{end}}</div>
    {{if .BranchAddress}}<div class="muted">{{.BranchAddress}}</div>{{end}}
  </header>

  <div class="rule"></div>

  {{if .IsReprint}}
  <!-- The REPRINT marker is a control, not decoration: a duplicate receipt is how a return gets run
       twice. It names the ordinal so "the second copy" is distinguishable from "the fifth". -->
  <div class="reprint">REPRINT #{{.ReprintCount}}</div>
  {{end}}

  <div class="center">
    <div class="order-no">{{.OrderNo}}</div>
    {{if .InvoiceNo}}<div class="muted">Invoice {{.InvoiceNo}}</div>{{end}}
    <div class="muted">{{.SoldAt.Format "2006-01-02 15:04"}}</div>
  </div>

  <div class="rule"></div>

  <div>
    <!-- The customer row is required on every sale (row 666): a sale with no customer on it
         speaks the counter's own walk-in word, never an empty slot. -->
    <div>Customer: {{if .CustomerName}}{{.CustomerName}}{{else}}Walk-in{{end}}{{if .CustomerAccountNo}} ({{.CustomerAccountNo}}){{end}}</div>
    {{if .SaleType}}<div class="muted">{{saleWords .SaleType}}</div>{{end}}
    {{if .CashierRef}}<div class="muted">Served by {{.CashierRef}}</div>{{end}}
  </div>

  <div class="rule"></div>

  <table>
    <thead>
      <tr><th>Item</th><th class="right">Qty</th><th class="right">Unit</th><th class="right">Total</th></tr>
    </thead>
    <tbody>
      {{range .Lines}}
      <tr class="line">
        <td class="desc" colspan="4">{{if .SKU}}{{.SKU}} · {{end}}{{.Description}}</td>
      </tr>
      <tr class="line-money">
        <td></td>
        <td class="right">{{qty .Qty}} {{.UOM}}</td>
        <td class="right">{{money .UnitPriceCents}}{{if .PriceUOM}}/{{.PriceUOM}}{{end}}</td>
        <td class="right">{{money .LineTotalCents}}</td>
      </tr>
      {{if gt .DiscountCents 0}}
      <tr class="line-discount">
        <td class="desc muted" colspan="3">Discount approved by {{.DiscountApprovedByLabel}}</td>
        <td class="right muted">{{money .DiscountCents}} off</td>
      </tr>
      {{end}}
      {{end}}
    </tbody>
  </table>

  <div class="rule"></div>

  <table class="totals">
    {{if gt .SaleDiscountCents 0}}
    <tr class="sale-discount muted"><td>Discount approved by {{.SaleDiscountApprovedByLabel}}</td><td class="right">-{{money .SaleDiscountCents}}</td></tr>
    {{end}}
    <tr><td>Subtotal</td><td class="right">{{money .SubtotalCents}}</td></tr>
    <tr class="tax"><td>{{.TaxLabel}}</td><td class="right">{{money .TaxCents}}</td></tr>
    <tr class="grand"><td>Total</td><td class="right">{{money .TotalCents}}</td></tr>
  </table>

  <div class="rule"></div>

  <table class="tenders">
    {{range .Tenders}}
    <tr class="tender"><td>{{.Label}}{{if .Simulated}} <span class="muted">Simulated card: nothing reached a processor.</span>{{end}}{{if .Ref}} <span class="muted">{{.Ref}}</span>{{end}}</td><td class="right">{{money .AmountCents}}</td></tr>
    {{end}}
    {{if gt .ChangeDueCents 0}}
    <tr class="change-due"><td>Change due</td><td class="right">{{money .ChangeDueCents}}</td></tr>
    {{end}}
    {{if gt .DepositCents 0}}
    <tr class="deposit"><td>Deposit</td><td class="right">{{money .DepositCents}}</td></tr>
    {{end}}
    {{if gt .BalanceCents 0}}
    <tr class="balance"><td>Balance due</td><td class="right">{{money .BalanceCents}}</td></tr>
    {{end}}
  </table>

  <div class="foot center muted">
    Printed {{.PrintedAt.Format "2006-01-02 15:04"}}
    <div>Keep this receipt, returns require it.</div>
    <!-- DIB-456 / D-078 · the product mark. It sits in the FOOT and not the header, deliberately:
         the header of a receipt belongs to the DEALER (OrgName, branch, address), that is whose
         document this is and whose counter the customer stood at. The software's name is a
         footnote, at the smallest size on the page, in the same place the dispatch manifest and the
         financial-statement print put it. Static text, no template value, so it cannot be turned
         into an injection point. -->
    <div class="mark">HH Yard · a Hardscape House product</div>
  </div>

  <div class="noprint center" style="margin-top:12px">
    <button onclick="window.print()">Print</button>
  </div>
</body>
</html>
`))

// The FULL-PAGE receipt (format=page), HH's own format (see RenderReceiptPageHTML's provenance
// note): the roll's blocks and words on a letter and A4 page, the print CSS sized for paper instead
// of a thermal roll. Margins only, never a forced page size, so the same document prints correctly
// on both letter and A4 drawers.
var receiptPageTmpl = template.Must(template.New("receipt-page").Funcs(template.FuncMap{
	"money":     Money,
	"qty":       Qty,
	"saleWords": saleTypeWords,
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Receipt {{.OrderNo}}</title>
<style>
  /* Print CSS for a full sheet: letter and A4 both take these margins, the document keeps a
     readable measure on paper instead of the thermal roll's narrow column, and the colours stay
     ink-safe. */
  @page { margin: 18mm; }
  * { box-sizing: border-box; }
  body {
    font-family: ui-monospace, "SFMono-Regular", "Menlo", "Consolas", monospace;
    font-size: 13px; line-height: 1.5; color: #000; background: #fff;
    max-width: 180mm; margin: 0 auto; padding: 8px 0;
  }
  h1 { font-size: 20px; margin: 0 0 4px; letter-spacing: .02em; }
  .muted { color: #444; }
  .center { text-align: center; }
  .right { text-align: right; }
  .rule { border-top: 1px solid #000; margin: 14px 0; }
  .order-no { font-size: 24px; font-weight: 700; letter-spacing: .04em; margin: 10px 0 4px; }
  table { width: 100%; border-collapse: collapse; }
  td, th { padding: 4px 0; vertical-align: top; }
  th { text-align: left; font-weight: 700; border-bottom: 1px solid #000; }
  .desc { word-break: break-word; }
  .totals td { padding: 3px 0; }
  .grand { font-weight: 700; font-size: 15px; border-top: 1px solid #000; }
  .reprint {
    border: 2px solid #000; padding: 6px; margin: 10px 0; text-align: center;
    font-weight: 700; letter-spacing: .12em;
  }
  .foot { margin-top: 16px; font-size: 11px; }
  /* The product mark, the roll document's own rule carried over: the quietest thing on the page,
     in the foot, under the dealer's letterhead that owns the top. */
  .mark { margin-top: 6px; font-size: 10px; letter-spacing: .06em; color: #555; }
  @media print { .noprint { display: none !important; } }
</style>
</head>
<body>
  <header class="center">
    {{if .OrgName}}<h1>{{.OrgName}}</h1>{{end}}
    <div>{{.BranchName}}{{if .BranchCode}} ({{.BranchCode}}){{end}}</div>
    {{if .BranchAddress}}<div class="muted">{{.BranchAddress}}</div>{{end}}
  </header>

  <div class="rule"></div>

  {{if .IsReprint}}
  <!-- The REPRINT marker is a control, not decoration, on this document as on the roll: it names
       the ordinal so "the second copy" is distinguishable from "the fifth". -->
  <div class="reprint">REPRINT #{{.ReprintCount}}</div>
  {{end}}

  <div class="center">
    <div class="order-no">{{.OrderNo}}</div>
    {{if .InvoiceNo}}<div class="muted">Invoice {{.InvoiceNo}}</div>{{end}}
    <div class="muted">{{.SoldAt.Format "2006-01-02 15:04"}}</div>
  </div>

  <div class="rule"></div>

  <div>
    <!-- The customer row is required on every sale (row 666): a sale with no customer on it
         speaks the counter's own walk-in word, never an empty slot. -->
    <div>Customer: {{if .CustomerName}}{{.CustomerName}}{{else}}Walk-in{{end}}{{if .CustomerAccountNo}} ({{.CustomerAccountNo}}){{end}}</div>
    {{if .SaleType}}<div class="muted">{{saleWords .SaleType}}</div>{{end}}
    {{if .CashierRef}}<div class="muted">Served by {{.CashierRef}}</div>{{end}}
  </div>

  <div class="rule"></div>

  <table>
    <thead>
      <tr><th>Item</th><th class="right">Qty</th><th class="right">Unit</th><th class="right">Total</th></tr>
    </thead>
    <tbody>
      {{range .Lines}}
      <tr class="line">
        <td class="desc" colspan="4">{{if .SKU}}{{.SKU}} · {{end}}{{.Description}}</td>
      </tr>
      <tr class="line-money">
        <td></td>
        <td class="right">{{qty .Qty}} {{.UOM}}</td>
        <td class="right">{{money .UnitPriceCents}}{{if .PriceUOM}}/{{.PriceUOM}}{{end}}</td>
        <td class="right">{{money .LineTotalCents}}</td>
      </tr>
      {{if gt .DiscountCents 0}}
      <tr class="line-discount">
        <td class="desc muted" colspan="3">Discount approved by {{.DiscountApprovedByLabel}}</td>
        <td class="right muted">{{money .DiscountCents}} off</td>
      </tr>
      {{end}}
      {{end}}
    </tbody>
  </table>

  <div class="rule"></div>

  <table class="totals">
    {{if gt .SaleDiscountCents 0}}
    <tr class="sale-discount muted"><td>Discount approved by {{.SaleDiscountApprovedByLabel}}</td><td class="right">-{{money .SaleDiscountCents}}</td></tr>
    {{end}}
    <tr><td>Subtotal</td><td class="right">{{money .SubtotalCents}}</td></tr>
    <tr class="tax"><td>{{.TaxLabel}}</td><td class="right">{{money .TaxCents}}</td></tr>
    <tr class="grand"><td>Total</td><td class="right">{{money .TotalCents}}</td></tr>
  </table>

  <div class="rule"></div>

  <table class="tenders">
    {{range .Tenders}}
    <tr class="tender"><td>{{.Label}}{{if .Simulated}} <span class="muted">Simulated card: nothing reached a processor.</span>{{end}}{{if .Ref}} <span class="muted">{{.Ref}}</span>{{end}}</td><td class="right">{{money .AmountCents}}</td></tr>
    {{end}}
    {{if gt .ChangeDueCents 0}}
    <tr class="change-due"><td>Change due</td><td class="right">{{money .ChangeDueCents}}</td></tr>
    {{end}}
    {{if gt .DepositCents 0}}
    <tr class="deposit"><td>Deposit</td><td class="right">{{money .DepositCents}}</td></tr>
    {{end}}
    {{if gt .BalanceCents 0}}
    <tr class="balance"><td>Balance due</td><td class="right">{{money .BalanceCents}}</td></tr>
    {{end}}
  </table>

  <div class="foot center muted">
    Printed {{.PrintedAt.Format "2006-01-02 15:04"}}
    <div>Keep this receipt, returns require it.</div>
    <!-- DIB-456 / D-078 · the product mark, in the foot exactly as the roll document carries it:
         the header belongs to the DEALER, the software's name is a footnote. Static text, no
         template value, so it cannot be turned into an injection point. -->
    <div class="mark">HH Yard · a Hardscape House product</div>
  </div>

  <div class="noprint center" style="margin-top:16px">
    <button onclick="window.print()">Print</button>
  </div>
</body>
</html>
`))
