package printing

import (
	"bytes"
	"fmt"
	"html/template"
	"strconv"
	"strings"
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

var receiptTmpl = template.Must(template.New("receipt").Funcs(template.FuncMap{
	"money": Money,
	"qty":   Qty,
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
    {{if .CustomerName}}<div>Customer: {{.CustomerName}}{{if .CustomerAccountNo}} ({{.CustomerAccountNo}}){{end}}</div>{{end}}
    {{if .SaleType}}<div class="muted">{{.SaleType}}</div>{{end}}
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
      {{end}}
    </tbody>
  </table>

  <div class="rule"></div>

  <table class="totals">
    <tr><td>Subtotal</td><td class="right">{{money .SubtotalCents}}</td></tr>
    <tr class="tax"><td>{{.TaxLabel}}</td><td class="right">{{money .TaxCents}}</td></tr>
    <tr class="grand"><td>Total</td><td class="right">{{money .TotalCents}}</td></tr>
  </table>

  <div class="rule"></div>

  <table class="tenders">
    {{range .Tenders}}
    <tr class="tender"><td>{{.Label}}{{if .Ref}} <span class="muted">{{.Ref}}</span>{{end}}</td><td class="right">{{money .AmountCents}}</td></tr>
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
