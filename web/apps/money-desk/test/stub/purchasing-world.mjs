// The stub's purchasing world: the open accounts payable the Money Desk's Payables board reads.
// It answers the shape the core's route answers (W2-9a, the core's GET /api/v1/purchasing/ap-aging
// and its schemas PurchasingAPAgingPage, PurchasingAPAgingRow and PurchasingAPAgingItem): open AP
// per payee in the five aging bands, and a landed cost payable to its carrier or broker kept as
// its own row, never folded into a vendor's bill.
//
// The world is invented and small: four vendors, two landed cost carriers, one part paid bill,
// and every aging band occupied, so the board shows the shape a month of buying leaves behind.
// The names are invented, and the dates are cut from the host's own day, so no calendar literal
// is written here.

// The payees the board reads: the vendors the dealer buys from, and the carriers and brokers the
// landed cost is owed to. A landed cost payee is its own row because the money is owed to it, not
// to the vendor whose purchase it rides on.
const PAYEES = [
  { n: 1, name: "Ironwood Timber Supply (demo)", kind: "VENDOR", terms: 30, no: "V-1001" },
  { n: 2, name: "Harbourline Concrete Co (demo)", kind: "VENDOR", terms: 45, no: "V-1002" },
  { n: 3, name: "Meadowlark Landscape Lighting (demo)", kind: "VENDOR", terms: 30, no: "V-1003" },
  { n: 4, name: "Saltmarsh Fasteners (demo)", kind: "VENDOR", terms: 15, no: "V-1004" },
  { n: 5, name: "Northwind Freight Lines (demo)", kind: "LANDED_COST", terms: 15, no: "V-1005" },
  { n: 6, name: "Keystone Customs Brokerage (demo)", kind: "LANDED_COST", terms: 30, no: "V-1006" },
];

// One row per open document: the payee, the document number, what it is, the day it was issued
// and the day it falls due (both offsets from the host's own day), what it was for, and what has
// been paid against it. A part paid bill is one whose open amount is less than what it was for;
// the payment lands on its own day, so a bill paid after the day the books are read as of is
// still open on that day.
//
// The carrier's freight invoice and the landed cost freight on a receipt are both owed to the
// carrier, so both sit on the carrier's row: the bill is what the carrier billed, the landed
// cost is what the receipt posted, and neither folds into a vendor's bill.
const DOCUMENTS = [
  { n: 1, payee: 1, doc: "B-1041", kind: "VENDOR_BILL", issued: -42, due: -12, total: 482_500 },
  { n: 2, payee: 1, doc: "B-1042", kind: "VENDOR_BILL", issued: -12, due: 18, total: 154_000 },
  { n: 3, payee: 2, doc: "B-2053", kind: "VENDOR_BILL", issued: -90, due: -45, total: 215_000 },
  { n: 4, payee: 2, doc: "B-2054", kind: "VENDOR_BILL", issued: -120, due: -75, total: 130_500 },
  { n: 5, payee: 3, doc: "B-3102", kind: "VENDOR_BILL", issued: -160, due: -130, total: 88_750 },
  { n: 6, payee: 4, doc: "B-4107", kind: "VENDOR_BILL", issued: -35, due: -20, total: 96_000, paid: 40_000, paid_on: -6 },
  { n: 7, payee: 4, doc: "B-4108", kind: "VENDOR_BILL", issued: -60, due: -45, total: 31_500 },
  { n: 8, payee: 5, doc: "B-3301", kind: "VENDOR_BILL", issued: -18, due: -3, total: 42_000 },
  { n: 9, payee: 5, doc: "LC-2201", kind: "LANDED_COST", landed: "FREIGHT", po: "PO-1042", issued: -33, due: -18, total: 78_000 },
  { n: 10, payee: 6, doc: "LC-2207", kind: "LANDED_COST", landed: "DUTY", po: "PO-1108", issued: -105, due: -75, total: 112_000 },
];

// The vendors the Financial Controller has marked as subcontractors (W3-T51, W3-T52), with the CRA
// business number each one gave (the second gave none, so the report says the number is missing)
// and the bills paid to them during the year. Each payment's Paid in <year>, Box 22 services share
// (tax included), pre-tax services subtotal, $500 threshold mark and unmarked bill line note are
// computed by the core's own pro-rata half-to-even rules (t5018_report.go).
const SUBCONTRACTORS = [
  {
    n: 1,
    name: "Ironwood Timber Supply (demo)",
    no: "V-1001",
    number: "123456789",
    bills: [
      {
        tax_cents: 55_509,
        paid_cents: 482_500,
        lines: [{ supply_kind: null, line_total_cents: 426_991 }],
      },
    ],
  },
  {
    n: 2,
    name: "Meadowlark Landscape Lighting (demo)",
    no: "V-1003",
    number: "",
    bills: [
      {
        tax_cents: 10_210,
        paid_cents: 88_750,
        lines: [
          { supply_kind: "SERVICES", line_total_cents: 60_000 },
          { supply_kind: "GOODS", line_total_cents: 18_540 },
        ],
      },
    ],
  },
  {
    n: 4,
    name: "Saltmarsh Fasteners (demo)",
    no: "V-1004",
    number: "987654321",
    bills: [
      {
        tax_cents: 6_240,
        paid_cents: 54_240,
        lines: [{ supply_kind: "SERVICES", line_total_cents: 48_000 }],
      },
    ],
  },
];

export const T5018_RULE_NOTE = "T5018 applies only where construction is more than half the dealer's business income.";
const T5018_THRESHOLD_CENTS = 50_000;
export const T5018_UNDER_THRESHOLD_NOTE = "under the $500 slip threshold, no slip.";
export const T5018_GOODS_SHARE_PREFIX = "Box 22 counts services only;";
export const T5018_GOODS_SHARE_SUFFIX = "of goods is not counted.";
export const T5018_NOTHING_PAID_NOTE = "nothing paid this year";

/** Formats integer cents as dollars and cents, matching the core's t5018FormatDollars. */
function t5018FormatDollars(cents) {
  const abs = Math.abs(cents);
  const dollars = Math.trunc(abs / 100).toLocaleString("en-US");
  const rem = String(abs % 100).padStart(2, "0");
  return `${cents < 0 ? "-" : ""}$${dollars}.${rem}`;
}

/** The core's t5018UnderThresholdNote. */
function t5018UnderThresholdNote(servicesSubtotalCents) {
  return `Services before tax ${t5018FormatDollars(servicesSubtotalCents)}: ${T5018_UNDER_THRESHOLD_NOTE}`;
}

/** The core's t5018GoodsShareNote. */
function t5018GoodsShareNote(goodsShareCents) {
  return `${T5018_GOODS_SHARE_PREFIX} ${t5018FormatDollars(goodsShareCents)} ${T5018_GOODS_SHARE_SUFFIX}`;
}

/** The core's t5018ProRataCents (half to even in integer cents). */
function t5018ProRataCents(amountCents, partCents, wholeCents) {
  if (amountCents <= 0 || partCents <= 0 || wholeCents <= 0) return 0;
  if (partCents === wholeCents) return amountCents;
  const num = BigInt(amountCents) * BigInt(partCents);
  const whole = BigInt(wholeCents);
  let q = num / whole;
  const rem = num % whole;
  const twiceRem = rem * 2n;
  if (twiceRem > whole || (twiceRem === whole && q % 2n === 1n)) {
    q += 1n;
  }
  return Number(q);
}

/** The core's t5018PaymentShare (Box 22 services share with tax, and pre-tax services subtotal share). */
function t5018PaymentShare(paidCents, billSubtotalCents, billTotalCents, billServicesSubtotalCents) {
  if (paidCents <= 0 || billSubtotalCents <= 0 || billTotalCents <= 0 || billServicesSubtotalCents <= 0) {
    return { box22Cents: 0, servicesSubtotalCents: 0 };
  }
  return {
    box22Cents: t5018ProRataCents(paidCents, billServicesSubtotalCents, billSubtotalCents),
    servicesSubtotalCents: t5018ProRataCents(paidCents, billServicesSubtotalCents, billTotalCents),
  };
}

/** The core's t5018UnmarkedLinesNote. */
function t5018UnmarkedLinesNote(n) {
  if (n <= 0) return "";
  if (n === 1) {
    return "1 bill line is not marked goods or services; it is counted as services until marked";
  }
  return `${n} bill lines are not marked goods or services; they are counted as services until marked`;
}

/** The band each of the core's bucket keys holds its money in. */const BUCKET_FIELD = {
  CURRENT: "current_cents",
  DAYS_30: "days_30_cents",
  DAYS_60: "days_60_cents",
  DAYS_60_TO_90: "days_60_to_90_cents",
  DAYS_90_PLUS: "days_90_plus_cents",
};

/** The payee's id, the stub's own way: a kind prefix and a padded number. */
const payeeId = (n) => `p0000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

/** The landed cost's own id, the core's way of giving the payable its own identity. */
const landedCostId = (n) => `c0000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

/** The bill's own id. */
const billId = (n) => `b0000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

/** The branch every document sits on, the stub's own way. */
const branchId = () => "d0000000-0000-4000-8000-000000000001";

/** The days between two calendar days, counted the way the house counts them. */
function daysBetween(from, to) {
  return Math.round((Date.parse(to) - Date.parse(from)) / 86_400_000);
}

/** The band a balance sits in on the day the books are read as of (the core's five bands, inclusive at the top). */
function bandOf(daysOverdue) {
  if (daysOverdue <= 0) return "CURRENT";
  if (daysOverdue <= 30) return "DAYS_30";
  if (daysOverdue <= 60) return "DAYS_60";
  if (daysOverdue <= 90) return "DAYS_60_TO_90";
  return "DAYS_90_PLUS";
}

export function createPurchasingWorld({ dayOffset }) {
  // The documents the world holds. The empty scenario holds none, so the board reads the
  // shape of a dealer who owes nothing on that day.
  let documents = DOCUMENTS;

  /** The open AP as of a calendar day: one row per payee, its bands, its total and its items. */
  function apAging(asOf) {
    const items = [];
    const bands = { current_cents: 0, days_30_cents: 0, days_60_cents: 0, days_60_to_90_cents: 0, days_90_plus_cents: 0 };
    let total = 0;
    for (const payee of PAYEES) {
      const row = {
        row_kind: payee.kind,
        is_landed_cost: payee.kind === "LANDED_COST",
        vendor_id: payeeId(payee.n),
        vendor_no: payee.no,
        vendor_name: payee.name,
        payment_terms_days: payee.terms,
        current_cents: 0,
        days_30_cents: 0,
        days_60_cents: 0,
        days_60_to_90_cents: 0,
        days_90_plus_cents: 0,
        total_cents: 0,
        open_items: [],
      };
      if (row.is_landed_cost) {
        row.landed_cost_id = landedCostId(payee.n);
        row.vendor_id = row.landed_cost_id;
      }
      let earliestDue = "";
      for (const doc of documents.filter((d) => d.payee === payee.n)) {
        // A document nobody has issued yet is not owed on that day.
        if (dayOffset(doc.issued) > asOf) continue;
        // A payment after the day the books are read as of has not happened yet on that day,
        // so the bill is still open there.
        const paid = doc.paid === undefined || dayOffset(doc.paid_on) > asOf ? 0 : doc.paid;
        const open = doc.total - paid;
        if (open <= 0) continue;
        const landed = doc.kind === "LANDED_COST";
        // A landed cost falls due the day it is incurred: the core reads its bucket from the day
        // it was created, not from a due date it does not have.
        const dueOffset = landed ? doc.issued : doc.due;
        const bucket = bandOf(daysBetween(dayOffset(dueOffset), asOf));
        const item = {
          id: billId(doc.n),
          kind: doc.kind,
          is_landed_cost: landed,
          ref_no: landed ? doc.po : doc.doc,
          branch_id: branchId(),
          bill_date: dayOffset(doc.issued),
          due_date: dayOffset(dueOffset),
          total_cents: doc.total,
          paid_cents: paid,
          open_cents: open,
          bucket,
        };
        if (!landed) item.bill_no = doc.doc;
        else {
          item.po_ref = doc.po;
          item.charge_kind = doc.landed;
          item.vendor_name = payee.name;
        }
        row.open_items.push(item);
        if (earliestDue === "" || item.due_date < earliestDue) earliestDue = item.due_date;
        const band = BUCKET_FIELD[bucket];
        row[band] += open;
        bands[band] += open;
        row.total_cents += open;
        total += open;
      }
      if (row.open_items.length === 0) continue;
      if (earliestDue !== "") row.due_date = earliestDue;
      items.push(row);
    }
    // The core's own order: the vendor rows come back by vendor name, and the landed cost rows
    // follow them, the oldest created first. The board keeps its own order for the screen.
    items.sort((a, b) => {
      if (a.is_landed_cost !== b.is_landed_cost) return a.is_landed_cost ? 1 : -1;
      if (a.is_landed_cost) {
        return (a.due_date ?? "").localeCompare(b.due_date ?? "") || a.vendor_name.localeCompare(b.vendor_name);
      }
      return a.vendor_name.localeCompare(b.vendor_name);
    });
    return { as_of: asOf, ...bands, total_cents: total, items, next_cursor: null };
  }

  /** The year's T5018 report: the marked vendors, their business numbers, paid totals and Box 22 shares. */
  function t5018Report(year) {
    const items = [];
    let total = 0;
    let box22Total = 0;
    for (const sub of SUBCONTRACTORS) {
      let paidCents = 0;
      let box22Cents = 0;
      let servicesSubtotalCents = 0;
      let unmarkedLineCount = 0;
      for (const bill of sub.bills) {
        const subtotal = bill.lines.reduce((s, l) => s + l.line_total_cents, 0);
        const totalCents = subtotal + bill.tax_cents;
        const servicesSubtotal = bill.lines
          .filter((l) => l.supply_kind === null || l.supply_kind === "SERVICES")
          .reduce((s, l) => s + l.line_total_cents, 0);
        const unmarked = bill.lines.filter((l) => l.supply_kind === null).length;
        const share = t5018PaymentShare(bill.paid_cents, subtotal, totalCents, servicesSubtotal);
        paidCents += bill.paid_cents;
        box22Cents += share.box22Cents;
        servicesSubtotalCents += share.servicesSubtotalCents;
        unmarkedLineCount += unmarked;
      }
      const underThreshold = servicesSubtotalCents < T5018_THRESHOLD_CENTS;
      let thresholdNote = "";
      if (paidCents === 0 && box22Cents === 0) {
        thresholdNote = T5018_NOTHING_PAID_NOTE;
      } else if (underThreshold) {
        thresholdNote = t5018UnderThresholdNote(servicesSubtotalCents);
      } else if (paidCents > box22Cents) {
        thresholdNote = t5018GoodsShareNote(paidCents - box22Cents);
      }
      items.push({
        vendor_id: payeeId(sub.n),
        vendor_no: sub.no,
        name: sub.name,
        business_number: sub.number,
        business_number_missing: sub.number === "",
        paid_cents: paidCents,
        box_22_cents: box22Cents,
        services_subtotal_cents: servicesSubtotalCents,
        under_threshold: underThreshold,
        threshold_note: thresholdNote,
        unmarked_line_count: unmarkedLineCount,
        unmarked_lines_note: t5018UnmarkedLinesNote(unmarkedLineCount),
      });
      total += paidCents;
      box22Total += box22Cents;
    }
    return {
      year,
      rule_note: T5018_RULE_NOTE,
      items,
      total_cents: total,
      box_22_total_cents: box22Total,
    };
  }

  /** The same report as the file a bookkeeper hands the contractor's book: the core's own CSV shape. */
  function t5018Csv(year) {
    const report = t5018Report(year);
    const ruleNote = report.rule_note || T5018_RULE_NOTE;
    const rows = [
      [ruleNote],
      [
        "vendor_no",
        "vendor_name",
        "business_number",
        "paid_cents",
        "box_22_cents",
        "services_subtotal_cents",
        "under_threshold",
        "threshold_note",
        "unmarked_line_count",
        "unmarked_lines_note",
      ],
    ];
    for (const row of report.items) {
      rows.push([
        row.vendor_no,
        row.name,
        row.business_number_missing ? "(missing)" : row.business_number,
        String(row.paid_cents),
        String(row.box_22_cents),
        String(row.services_subtotal_cents),
        row.under_threshold ? "true" : "false",
        row.threshold_note ?? "",
        String(row.unmarked_line_count),
        row.unmarked_lines_note ?? "",
      ]);
    }
    rows.push([]);
    rows.push(["Total", "", "", String(report.total_cents), String(report.box_22_total_cents), "", "", "", "", ""]);
    return `${rows.map((row) => row.map(csvCell).join(",")).join("\n")}\n`;
  }

  /** Puts the world back to the start; the empty scenario holds no documents at all. */
  function reset(kind = "full") {
    documents = kind === "empty" ? [] : DOCUMENTS;
  }

  return { apAging, t5018Report, t5018Csv, reset };
}

/** A CSV cell, quoted the way a CSV quotes a cell that carries a comma or a quote. */
function csvCell(value) {
  const text = String(value);
  if (!/[",\n]/.test(text)) return text;
  return `"${text.replace(/"/g, '""')}"`;
}
