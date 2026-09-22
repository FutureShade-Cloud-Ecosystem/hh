package purchasing

// document.go, THE purchase-order document. One projection, one table, two encoders.
//
// - THE ACCEPTANCE CLAUSE IS A STRUCTURAL CLAUSE -
//
// "A PO renders identically in PDF and CSV from one source." Two independent renderers that happen
// to agree today is the thing that clause forbids, and it is the thing that always happens: someone
// adds a column to the print page in March, the export keeps nine columns until August, and the
// buyer who reconciles the spreadsheet against the printout finds the difference by being wrong in
// front of a vendor.
//
// So neither encoder formats a cell. PODocument.table() turns the projection into a HEADER ROW and
// a set of STRING ROWS, and RenderPODocumentHTML and EncodePODocumentCSV both emit exactly those
// strings in exactly that order. Adding a column means adding one entry to poDocumentFields, which
// appears in both renderings or in neither. internal/reporting/export.go argues the same shape for
// the same reason and its header says so out loud; this is that idiom re-consumed, not re-invented.
//
// - WHY THERE IS NO PDF, AND WHY THAT IS NOT A COMPROMISE -
//
// There is no PDF library in this stack, and three files already say so. The house answer, copied
// from internal/printing/render.go without deviation: a self-contained html/template page with
// @page + @media print CSS, served text/html with Cache-Control: no-store, printed by the browser.
// "Save as PDF" in the print dialog IS the PDF, and it is the same bytes whether it is printed today
// or reprinted from a lookup next year. html/template and never text/template, because every value
// on the page, a vendor's name, a product description, a buyer's note, is text somebody typed that
// lands inside markup.
//
// - WHY THE MONEY COLUMNS ARE REMOVED, NOT BLANKED -
//
// poDocumentFields takes the gate as an argument and returns a SHORTER field list when money is
// refused. A blanked column would still print the heading "Unit cost", which tells the reader there
// is a number they are not being shown, and a CSV with an empty money column imports as a column of
// nulls that somebody will eventually sum to zero. The columns are not there.
//
// - WHY THE CURRENCY IS IN THE HEADING AND NOT IN THE CELL -
//
// Because the two encoders emit the SAME STRING, and the two media want opposite things from a money
// cell: a printed page wants "CA$1,234.56" and a spreadsheet wants 1234.56 or the column will not
// sum (internal/finstmt and internal/reporting both learned this, and reporting's header names it as
// the single most common complaint about exported reports). Putting the currency in the column
// heading satisfies both and is what a real purchase order does anyway, it is also the house
// doctrine's own rule about not restating units the column header already gives.

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
)

// - the projection -

// PODocument is the ONE source. Both encoders read this and nothing else; neither one reaches back
// into a repository, a policy or a clock.
type PODocument struct {
	PONo       string
	StatusWord string // a word a vendor reads ("Ordered"), never the lifecycle enum
	IssuedOn   string // YYYY-MM-DD
	ExpectedOn string // YYYY-MM-DD, or empty when the buyer promised no date
	Currency   string

	BuyerName    string // the dealer, whose document this is
	BuyerBranch  string
	BuyerCode    string
	BuyerAddress string
	BuyerPhone   string
	BuyerEmail   string

	VendorName string
	VendorNo   string

	// The ordering desk this document is aimed at. Empty when nobody has tagged one, which the
	// rendered page states rather than hides, because "who is this going to" is the question the
	// buyer is holding when they print it.
	ContactName   string
	ContactEmail  string
	ContactPhone  string
	ContactTagged bool

	Notes string
	Lines []PODocumentLine

	// ShowMoney is the resolved gate, carried ON the projection so that both encoders read the same
	// decision rather than each asking a policy. There is exactly one place that sets it
	// (buildPODocument, from costLens).
	ShowMoney     bool
	SubtotalCents int64

	PrintedAt time.Time
}

// PODocumentLine is one ordered line as the VENDOR reads it, plus the base figures the yard counts.
type PODocumentLine struct {
	LineNo      int
	Description string

	// ItemCode is the vendor's own code where one is mapped, and OURS where none is. Mapping names
	// which, see mappingWord. A code silently swapped for ours would be a document that looks
	// translated and is not.
	ItemCode       string
	CodeFromVendor bool

	// OrderQty/OrderUOM are the vendor's selling unit where the mapping converts EXACTLY, and the
	// base pair where it does not. BaseQty/BaseUOM are always ours: they are what the receiving
	// clerk counts off the truck, and a document the yard cannot check is a document the yard
	// ignores.
	OrderQty       float64
	OrderUOM       string
	UnitFromVendor bool
	BaseQty        float64
	BaseUOM        string

	UnitCostCents  int64
	LineTotalCents int64
}

// FileName is the attachment/download name, the PO number with the characters a file system and a
// mail client both dislike removed, and nothing invented.
func (d PODocument) FileName() string {
	var b strings.Builder
	for _, r := range d.PONo {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" {
		name = "purchase-order"
	}
	return name + ".csv"
}

// - the one field spec -

// poDocField is one column of the line table: the machine name both encoders key on, the heading
// both encoders print, and the ONE formatter that turns a line into that cell's text.
type poDocField struct {
	Name   string
	Header string
	Num    bool // right-aligned on the printed page; ignored by CSV, which has no alignment
	Cell   func(l PODocumentLine) string
}

// poDocumentFields is the whole column allowlist, in order.
//
// The money arm is a SHORTER LIST, not a blanked one, see the file header. `currency` is
// interpolated into the two money headings so the cells can stay plain decimals.
func poDocumentFields(showMoney bool, currency string) []poDocField {
	fields := []poDocField{
		{Name: "line", Header: "Line", Num: true,
			Cell: func(l PODocumentLine) string { return strconv.Itoa(l.LineNo) }},
		{Name: "item_code", Header: "Item code",
			Cell: func(l PODocumentLine) string { return l.ItemCode }},
		{Name: "description", Header: "Description",
			Cell: func(l PODocumentLine) string { return l.Description }},
		{Name: "order_qty", Header: "Quantity", Num: true,
			Cell: func(l PODocumentLine) string { return docQty(l.OrderQty) }},
		{Name: "order_uom", Header: "Unit",
			Cell: func(l PODocumentLine) string { return l.OrderUOM }},
		{Name: "base_qty", Header: "Our quantity", Num: true,
			Cell: func(l PODocumentLine) string { return docQty(l.BaseQty) }},
		{Name: "base_uom", Header: "Our unit",
			Cell: func(l PODocumentLine) string { return l.BaseUOM }},
		{Name: "mapping", Header: "Codes from",
			Cell: func(l PODocumentLine) string { return mappingWord(l) }},
	}
	if !showMoney {
		return fields
	}
	return append(fields,
		poDocField{Name: "unit_cost", Header: "Unit cost (" + currency + ")", Num: true,
			Cell: func(l PODocumentLine) string { return docMoney(l.UnitCostCents) }},
		poDocField{Name: "line_total", Header: "Line total (" + currency + ")", Num: true,
			Cell: func(l PODocumentLine) string { return docMoney(l.LineTotalCents) }},
	)
}

// mappingWord names whose vocabulary this line speaks. Four words, no enum: a vendor reads this.
//
// It is a COLUMN rather than a footnote because it is per line, one order routinely mixes a mapped
// product with one nobody has mapped yet, and a document that quietly used our code for half its
// lines is a document whose vendor rings up to ask which half.
func mappingWord(l PODocumentLine) string {
	switch {
	case l.CodeFromVendor && l.UnitFromVendor:
		return "Vendor code and unit"
	case l.CodeFromVendor:
		return "Vendor code"
	case l.UnitFromVendor:
		return "Vendor unit"
	default:
		return "Our catalogue"
	}
}

// docQty renders a quantity to the four decimals its DECIMAL(19,4) column carries, then trims the
// trailing zeros so "12" does not print as "12.0000" in a column a person reads. reporting's
// formatCell, same arithmetic, same reason.
func docQty(q float64) string {
	s := strconv.FormatFloat(q, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// docMoney renders integer cents as a plain decimal with no symbol and no thousands separator, so
// the same string is a readable figure on the printed page AND a number Excel will sum. The currency
// lives in the column heading.
func docMoney(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

// - the one table -

// poDocPair is one identity fact on the document head: a machine name, a heading, and a value.
type poDocPair struct {
	Name  string
	Label string
	Value string
}

// poDocTable is everything both encoders emit, already turned into strings. Neither encoder formats
// anything; they choose markup or commas and nothing else.
type poDocTable struct {
	Summary []poDocPair
	Names   []string
	Headers []string
	Nums    []bool
	Rows    [][]string
	Totals  []poDocPair
}

// summary is the document head: who, what, when. Empty facts are DROPPED rather than rendered as a
// blank label, a heading with nothing under it reads as data that failed to load.
func (d PODocument) summary() []poDocPair {
	pairs := []poDocPair{
		{Name: "po_no", Label: "Purchase order", Value: d.PONo},
		{Name: "status", Label: "Status", Value: d.StatusWord},
		{Name: "issued_on", Label: "Issued", Value: d.IssuedOn},
	}
	if d.ExpectedOn != "" {
		pairs = append(pairs, poDocPair{Name: "expected_on", Label: "Required by", Value: d.ExpectedOn})
	}
	pairs = append(pairs,
		poDocPair{Name: "vendor", Label: "Vendor", Value: d.VendorName},
		poDocPair{Name: "vendor_no", Label: "Vendor account", Value: d.VendorNo},
	)
	// THE ORDERING DESK IS ON THE DOCUMENT, INCLUDING WHEN IT IS ABSENT. A buyer printing this to
	// hand over needs to know nobody is tagged, and the sentence is the one the send path would say.
	if d.ContactTagged {
		who := strings.TrimSpace(d.ContactName)
		if who == "" {
			who = d.ContactEmail
		}
		pairs = append(pairs, poDocPair{Name: "attention", Label: "Attention", Value: who})
		if d.ContactEmail != "" {
			pairs = append(pairs, poDocPair{Name: "attention_email", Label: "Email", Value: d.ContactEmail})
		}
		if d.ContactPhone != "" {
			pairs = append(pairs, poDocPair{Name: "attention_phone", Label: "Phone", Value: d.ContactPhone})
		}
	} else {
		pairs = append(pairs, poDocPair{Name: "attention", Label: "Attention",
			Value: "No ordering contact tagged"})
	}
	pairs = append(pairs, poDocPair{Name: "ship_to", Label: "Ship to", Value: d.shipTo()})
	if d.Notes != "" {
		pairs = append(pairs, poDocPair{Name: "notes", Label: "Notes", Value: d.Notes})
	}
	return pairs
}

// shipTo assembles the delivery line from whatever the branch directory answered. An unwired or
// silent directory degrades to the branch code alone rather than to an empty document.
func (d PODocument) shipTo() string {
	parts := make([]string, 0, 3)
	for _, p := range []string{d.BuyerBranch, d.BuyerAddress} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return d.BuyerCode
	}
	return strings.Join(parts, " · ")
}

// table is the single rendering step. Both encoders call it; neither one may format a cell itself.
func (d PODocument) table() poDocTable {
	fields := poDocumentFields(d.ShowMoney, d.Currency)
	t := poDocTable{Summary: d.summary()}
	for _, f := range fields {
		t.Names = append(t.Names, f.Name)
		t.Headers = append(t.Headers, f.Header)
		t.Nums = append(t.Nums, f.Num)
	}
	for _, l := range d.Lines {
		row := make([]string, 0, len(fields))
		for _, f := range fields {
			row = append(row, f.Cell(l))
		}
		t.Rows = append(t.Rows, row)
	}
	if d.ShowMoney {
		t.Totals = []poDocPair{{
			Name:  "subtotal",
			Label: "Order total (" + d.Currency + ")",
			Value: docMoney(d.SubtotalCents),
		}}
	}
	return t
}

// - encoder one: the print page -

// RenderPODocumentHTML renders the purchase order as a self-contained print document.
//
// Every cell it prints comes from PODocument.table(); the template chooses tags and nothing else.
// Each cell carries data-field so the agreement test can extract it by name rather than by counting
// columns, a test that counted columns would pass a document whose columns had been reordered.
func RenderPODocumentHTML(d PODocument) (string, error) {
	var buf bytes.Buffer
	if err := poDocTmpl.Execute(&buf, struct {
		Doc   PODocument
		Table poDocTable
	}{d, d.table()}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// - encoder two: the attachment -

// EncodePODocumentCSV serialises the SAME table to RFC-4180 CSV.
//
// The summary rows come first, as name/value pairs, because an exported file gets emailed and a
// spreadsheet with no idea which order it is is a spreadsheet somebody prints and then annotates by
// hand. Then the header row, the lines, and the total, the reporting-module shape.
func EncodePODocumentCSV(d PODocument) ([]byte, error) {
	t := d.table()
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	for _, p := range t.Summary {
		if err := w.Write([]string{p.Label, p.Value}); err != nil {
			return nil, err
		}
	}
	if err := w.Write(nil); err != nil {
		return nil, err
	}
	if err := w.Write(t.Headers); err != nil {
		return nil, err
	}
	for _, row := range t.Rows {
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	for _, p := range t.Totals {
		if err := w.Write([]string{p.Label, p.Value}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// - building the projection -

// buildPODocument assembles the projection for one purchase order.
//
// It is the ONLY place that decides what the document says, and it takes the cost lens as an
// argument rather than resolving one, so the send path (which is RequireManager and must carry
// prices, a purchase order with no prices is not a purchase order) and the read path (which is
// RequireStaff and must not) cannot diverge on anything else.
func (s *Service) buildPODocument(ctx context.Context, po PO, lens costLens) (PODocument, error) {
	orders, err := s.orders()
	if err != nil {
		return PODocument{}, err
	}
	lines, err := orders.POLinesFor(ctx, po.ID)
	if err != nil {
		return PODocument{}, err
	}
	doc := PODocument{
		PONo:       po.PONo,
		StatusWord: StatusWord(po.Status),
		IssuedOn:   po.CreatedAt.Format("2006-01-02"),
		ExpectedOn: po.ExpectedAt,
		Currency:   po.Currency,
		ShowMoney:  lens.allow,
		PrintedAt:  s.clock(),
	}
	if doc.Currency == "" {
		doc.Currency = "CAD"
	}

	// The vendor, through the seam. A directory that cannot answer degrades to the id we hold
	// rather than failing the render: a buyer who cannot print a purchase order because a name
	// lookup blinked is a buyer who goes back to a spreadsheet.
	if s.vendor != nil {
		if v, err := s.vendor.Vendor(ctx, po.VendorID); err == nil {
			doc.VendorName, doc.VendorNo = v.Name, v.VendorNo
		} else {
			s.log.Warn("purchasing: vendor directory silent while rendering a purchase order",
				"vendor_id", po.VendorID, "err", err)
		}
	}
	if doc.VendorName == "" {
		doc.VendorName = po.VendorName
	}

	// The buying branch, through the seam. Same posture: unwired or silent leaves the ship-to line
	// as the branch code, which is still a true statement about where the truck goes.
	doc.BuyerCode = po.BranchID
	if s.branches != nil {
		if b, err := s.branches.Branch(ctx, po.BranchID); err == nil {
			doc.BuyerName, doc.BuyerBranch, doc.BuyerCode = b.OrgName, b.Name, b.Code
			doc.BuyerAddress, doc.BuyerPhone, doc.BuyerEmail = b.Address, b.Phone, b.Email
		} else {
			s.log.Warn("purchasing: branch directory silent while rendering a purchase order",
				"branch_id", po.BranchID, "err", err)
		}
	}
	if strings.TrimSpace(doc.BuyerName) == "" {
		doc.BuyerName = doc.BuyerBranch
	}

	// The tagged ordering desk, if there is one. Absence is a fact the document states.
	if store, err := s.sends(); err == nil {
		if c, found, err := store.VendorContact(ctx, po.VendorID); err == nil && found {
			doc.ContactTagged = true
			doc.ContactName, doc.ContactEmail, doc.ContactPhone = c.Name, c.Email, c.Phone
		}
	}

	// The vendor's catalogue vocabulary. An unreadable mapping table degrades every line to our own
	// catalogue and the `mapping` column says so on each of them, which is the honest answer, and
	// visibly different from a document that quietly used our codes.
	mapped := map[string]VendorSKU{}
	if store, err := s.catalogue(); err == nil {
		if rows, err := store.VendorSKUs(ctx, po.VendorID); err == nil {
			for _, m := range rows {
				mapped[m.ProductID] = m
			}
		} else {
			s.log.Warn("purchasing: vendor catalogue unreadable, the document falls back to our codes",
				"vendor_id", po.VendorID, "err", err)
		}
	}

	doc.Notes = po.Notes
	for _, l := range lines {
		doc.Lines = append(doc.Lines, documentLine(l, mapped[l.ProductID]))
		doc.SubtotalCents += l.LineTotalCents
	}
	return doc, nil
}

// PODocumentFor is the READ path onto the document: one purchase order, rendered as the caller may
// have it.
//
// It is branch-fenced like every other read here, and it takes ITS OWN cost lens, so a clerk who
// may open the order gets the same document with two fewer columns, and a manager gets the prices.
// The send path deliberately does not come through here: see SendPO's own note on why the contract
// that leaves the building always carries its prices.
func (s *Service) PODocumentFor(ctx context.Context, poID string) (PODocument, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PODocument{}, err
	}
	orders, err := s.orders()
	if err != nil {
		return PODocument{}, err
	}
	po, err := orders.POHeader(ctx, poID)
	if err != nil {
		return PODocument{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return PODocument{}, errForbidden
	}
	return s.buildPODocument(ctx, po, s.costLensFor(ctx, po.BranchID))
}

// documentLine applies one vendor mapping to one ordered line, degrading in the open.
func documentLine(l POLine, m VendorSKU) PODocumentLine {
	out := PODocumentLine{
		LineNo:         l.LineNo,
		Description:    l.Description,
		ItemCode:       l.SKU,
		OrderQty:       l.QtyOrderedBase,
		OrderUOM:       l.UOM,
		BaseQty:        l.QtyOrderedBase,
		BaseUOM:        l.UOM,
		UnitCostCents:  l.UnitCostCents,
		LineTotalCents: l.LineTotalCents,
	}
	if out.Description == "" {
		out.Description = l.SKU
	}
	if strings.TrimSpace(m.VendorSKU) != "" {
		out.ItemCode, out.CodeFromVendor = m.VendorSKU, true
	}
	if q, uom, ok := vendorUnitFor(l.QtyOrderedBase, m); ok {
		out.OrderQty, out.OrderUOM, out.UnitFromVendor = q, uom, true
	}
	return out
}

// vendorUnitFor converts a base quantity into the vendor's selling unit, and REFUSES when it does
// not land on a whole one.
//
// THAT REFUSAL IS THE POINT, not a limitation. 100 PC against a 60-PC skid is 1.6667 skids, and a
// purchase order that asks a vendor for 1.6667 skids is a purchase order the vendor guesses at -
// they will send one, or two, and the yard finds out on the truck. A line that does not divide is
// ordered in our base unit, and the document's `mapping` column says the unit is ours.
func vendorUnitFor(qtyBase float64, m VendorSKU) (float64, string, bool) {
	if strings.TrimSpace(m.VendorUOM) == "" || m.QtyPerVendorUOM <= 0 {
		return 0, "", false
	}
	n := qtyBase / m.QtyPerVendorUOM
	rounded := float64(int64(n + 0.5))
	if rounded <= 0 {
		return 0, "", false
	}
	if diff := n - rounded; diff > qtyEpsilon || diff < -qtyEpsilon {
		return 0, "", false
	}
	return rounded, m.VendorUOM, true
}

// StatusWord is the lifecycle state as a VENDOR reads it. A word map with a default arm that
// lowercases rather than inventing a phrase, the house rule for every enum that reaches a person,
// held on the server side because this document is rendered here and never by a client.
func StatusWord(status string) string {
	switch status {
	case StatusDraft:
		return "Draft"
	case StatusOrdered:
		return "Ordered"
	case StatusPartial:
		return "Part received"
	case StatusReceived:
		return "Received"
	case StatusClosed:
		return "Closed"
	case StatusCancelled:
		return "Cancelled"
	default:
		return strings.ToLower(strings.ReplaceAll(status, "_", " "))
	}
}

var poDocTmpl = template.Must(template.New("po").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Purchase order {{.Doc.PONo}}</title>
<style>
  /* Print CSS: the page IS the artifact. Letter with real margins, ink-safe colours, and a table
     that keeps its header on every sheet, a ten-line order fits on one page and a hundred-line
     order stays readable on five. */
  @page { margin: 14mm; size: letter; }
  * { box-sizing: border-box; }
  body {
    font-family: ui-sans-serif, system-ui, "Segoe UI", Helvetica, Arial, sans-serif;
    font-size: 11px; line-height: 1.45; color: #000; background: #fff;
    max-width: 190mm; margin: 0 auto; padding: 10px;
  }
  header { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; }
  h1 { font-size: 18px; margin: 0 0 2px; letter-spacing: .01em; }
  .buyer { font-size: 11px; }
  .buyer .name { font-weight: 700; font-size: 13px; }
  .muted { color: #444; }
  .doc-no { font-size: 22px; font-weight: 700; letter-spacing: .04em; text-align: right; }
  .rule { border-top: 1px solid #000; margin: 10px 0; }
  dl.summary { display: grid; grid-template-columns: max-content 1fr; gap: 2px 14px; margin: 0; }
  dl.summary dt { color: #444; }
  dl.summary dd { margin: 0; }
  table { width: 100%; border-collapse: collapse; margin-top: 4px; }
  th, td { padding: 4px 6px; vertical-align: top; border-bottom: 1px solid #ccc; }
  th { text-align: left; font-weight: 700; border-bottom: 1.5px solid #000; white-space: nowrap; }
  td.num, th.num { text-align: right; }
  tfoot td { border-bottom: none; padding-top: 8px; font-weight: 700; }
  thead { display: table-header-group; }
  tr { break-inside: avoid; }
  .foot { margin-top: 14px; font-size: 10px; }
  /* The product mark is the quietest thing on the page, in the same place the receipt and the
     dispatch manifest put it: the header of this document belongs to the DEALER. */
  .mark { margin-top: 4px; font-size: 9px; letter-spacing: .06em; color: #555; }
  @media print { .noprint { display: none !important; } }
</style>
</head>
<body>
  <header>
    <div class="buyer">
      {{if .Doc.BuyerName}}<div class="name">{{.Doc.BuyerName}}</div>{{end}}
      {{if .Doc.BuyerBranch}}<div>{{.Doc.BuyerBranch}}</div>{{end}}
      {{if .Doc.BuyerAddress}}<div class="muted">{{.Doc.BuyerAddress}}</div>{{end}}
      {{if .Doc.BuyerPhone}}<div class="muted">{{.Doc.BuyerPhone}}{{if .Doc.BuyerEmail}} · {{.Doc.BuyerEmail}}{{end}}</div>{{else if .Doc.BuyerEmail}}<div class="muted">{{.Doc.BuyerEmail}}</div>{{end}}
    </div>
    <div>
      <h1>Purchase order</h1>
      <div class="doc-no">{{.Doc.PONo}}</div>
    </div>
  </header>

  <div class="rule"></div>

  {{/* THE NUMBER APPEARS TWICE ON THIS PAGE, AND THAT IS DELIBERATE. The masthead carries it for the
       eye, it is the first thing a vendor's order desk looks for, and the summary row carries it
       as a RECORD, which is the row the CSV rendering has instead of a masthead. Dropping it from
       the summary to satisfy "say it once" would take the order number out of the spreadsheet
       entirely, and the two renderings would stop being the same document. The same argument covers
       Ship to: the letterhead says who is BUYING and the ship-to says where the truck goes, and they
       are only equal at a dealer whose yard is its office. */}}
  <dl class="summary">
    {{range .Table.Summary}}
    <dt>{{.Label}}</dt><dd data-field="{{.Name}}">{{.Value}}</dd>
    {{end}}
  </dl>

  <table>
    <thead>
      <tr>{{range $i, $h := .Table.Headers}}<th{{if index $.Table.Nums $i}} class="num"{{end}}>{{$h}}</th>{{end}}</tr>
    </thead>
    <tbody>
      {{range $r, $row := .Table.Rows}}
      <tr>{{range $i, $cell := $row}}<td data-field="{{index $.Table.Names $i}}"{{if index $.Table.Nums $i}} class="num"{{end}}>{{$cell}}</td>{{end}}</tr>
      {{end}}
    </tbody>
    {{if .Table.Totals}}
    <tfoot>
      {{range .Table.Totals}}
      <tr><td colspan="{{len $.Table.Headers}}" class="num">{{.Label}} <span data-field="{{.Name}}">{{.Value}}</span></td></tr>
      {{end}}
    </tfoot>
    {{end}}
  </table>

  <div class="foot muted">
    Quote {{.Doc.PONo}} on your acknowledgement, packing slip and invoice.
    <div>Printed {{.Doc.PrintedAt.Format "2006-01-02 15:04"}}</div>
    <div class="mark">HH Yard · a Hardscape House product</div>
  </div>

  <div class="noprint" style="margin-top:12px">
    <button onclick="window.print()">Print</button>
  </div>
</body>
</html>
`))
