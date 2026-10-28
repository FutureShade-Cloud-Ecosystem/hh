// The stub's purchasing world: the open accounts payable the Money Desk's Payables
// board reads. It answers the shape the core's route will answer (W2-9a, the core's
// GET /api/v1/purchasing/ap-aging): open AP per payee in the AR aging's bands, and a
// landed cost payable to its carrier or broker kept as its own payee row, never folded
// into a vendor's bill.
//
// The world is invented and small: four vendors, two landed cost carriers, one part
// paid bill, and every aging band occupied, so the board shows the shape a month of
// buying leaves behind. The names are invented, and the dates are cut from the host's
// own day, so no calendar literal is written here.

// The payees the board reads: the vendors the dealer buys from, and the carriers and
// brokers the landed cost is owed to. A landed cost payee is its own row because the
// money is owed to it, not to the vendor whose bill it rides on.
const PAYEES = [
  { n: 1, name: "Ironwood Timber Supply (demo)", kind: "VENDOR", terms: 30 },
  { n: 2, name: "Harbourline Concrete Co (demo)", kind: "VENDOR", terms: 45 },
  { n: 3, name: "Meadowlark Landscape Lighting (demo)", kind: "VENDOR", terms: 30 },
  { n: 4, name: "Saltmarsh Fasteners (demo)", kind: "VENDOR", terms: 15 },
  { n: 5, name: "Northwind Freight Lines (demo)", kind: "LANDED_COST", terms: 15 },
  { n: 6, name: "Keystone Customs Brokerage (demo)", kind: "LANDED_COST", terms: 30 },
];

// One row per open document: the payee, the document number, what it is, the day it was
// issued and the day it falls due (both offsets from the host's own day), what it was for,
// and what has been paid against it. A part paid bill is one whose open amount is less
// than what it was for; the payment lands on its own day, so a bill paid after the day
// the books are read as of is still open on that day.
//
// The carrier's freight invoice and the landed cost freight on a receipt are both owed to
// the carrier, so both sit on the carrier's row: the bill is what the carrier billed, the
// landed cost is what the receipt posted, and neither folds into a vendor's bill.
const DOCUMENTS = [
  { n: 1, payee: 1, doc: "B-1041", kind: "BILL", issued: -42, due: -12, total: 482_500 },
  { n: 2, payee: 1, doc: "B-1042", kind: "BILL", issued: -12, due: 18, total: 154_000 },
  { n: 3, payee: 2, doc: "B-2053", kind: "BILL", issued: -90, due: -45, total: 215_000 },
  { n: 4, payee: 2, doc: "B-2054", kind: "BILL", issued: -120, due: -75, total: 130_500 },
  { n: 5, payee: 3, doc: "B-3102", kind: "BILL", issued: -160, due: -130, total: 88_750 },
  { n: 6, payee: 4, doc: "B-4107", kind: "BILL", issued: -35, due: -20, total: 96_000, paid: 40_000, paid_on: -6 },
  { n: 7, payee: 4, doc: "B-4108", kind: "BILL", issued: -60, due: -45, total: 31_500 },
  { n: 8, payee: 5, doc: "B-3301", kind: "BILL", issued: -18, due: -3, total: 42_000 },
  { n: 9, payee: 5, doc: "LC-2201", kind: "LANDED_COST", landed: "FREIGHT", issued: -33, due: -18, total: 78_000 },
  { n: 10, payee: 6, doc: "LC-2207", kind: "LANDED_COST", landed: "DUTY", issued: -105, due: -75, total: 112_000 },
];

const BANDS = ["current_cents", "days_30_cents", "days_60_cents", "days_90_cents", "days_90_plus_cents"];

/** The payee's id, the stub's own way: a kind prefix and a padded number. */
const payeeId = (n) => `p0000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

/** The days between two calendar days, counted the way the house counts them. */
function daysBetween(from, to) {
  return Math.round((Date.parse(to) - Date.parse(from)) / 86_400_000);
}

/** The band a balance sits in on the day the books are read as of (the C8 bands, inclusive at the top). */
function bandOf(daysOverdue) {
  if (daysOverdue <= 0) return "current_cents";
  if (daysOverdue <= 30) return "days_30_cents";
  if (daysOverdue <= 60) return "days_60_cents";
  if (daysOverdue <= 90) return "days_90_cents";
  return "days_90_plus_cents";
}

export function createPurchasingWorld({ dayOffset }) {
  // The documents the world holds. The empty scenario holds none, so the board reads the
  // shape of a dealer who owes nothing on that day.
  let documents = DOCUMENTS;

  /** The open AP as of a calendar day: one row per payee, its bands, its total and its lines. */
  function apAging(asOf) {
    const items = [];
    const bands = { current_cents: 0, days_30_cents: 0, days_60_cents: 0, days_90_cents: 0, days_90_plus_cents: 0 };
    let total = 0;
    for (const payee of PAYEES) {
      const row = {
        payee_id: payeeId(payee.n),
        payee_name: payee.name,
        payee_kind: payee.kind,
        payment_terms_days: payee.terms,
        current_cents: 0,
        days_30_cents: 0,
        days_60_cents: 0,
        days_90_cents: 0,
        days_90_plus_cents: 0,
        open_cents: 0,
        lines: [],
      };
      for (const doc of documents.filter((d) => d.payee === payee.n)) {
        // A document nobody has issued yet is not owed on that day.
        if (dayOffset(doc.issued) > asOf) continue;
        // A payment after the day the books are read as of has not happened yet on that day,
        // so the bill is still open there.
        const paid = doc.paid === undefined || dayOffset(doc.paid_on) > asOf ? 0 : doc.paid;
        const open = doc.total - paid;
        if (open <= 0) continue;
        const overdue = daysBetween(dayOffset(doc.due), asOf);
        row[bandOf(overdue)] += open;
        row.open_cents += open;
        bands[bandOf(overdue)] += open;
        total += open;
        const line = {
          doc_no: doc.doc,
          doc_kind: doc.kind,
          due_date: dayOffset(doc.due),
          open_cents: open,
          paid_cents: paid,
          days_overdue: Math.max(0, overdue),
        };
        if (doc.landed !== undefined) line.landed_cost_kind = doc.landed;
        row.lines.push(line);
      }
      if (row.lines.length === 0) continue;
      items.push(row);
    }
    // The oldest money first: the payee with the most past due reads first, then the name.
    items.sort((a, b) => b.days_90_plus_cents - a.days_90_plus_cents || b.open_cents - a.open_cents || a.payee_name.localeCompare(b.payee_name));
    return { as_of: asOf, items, bands, total_cents: total };
  }

  /** Puts the world back to the start; the empty scenario holds no documents at all. */
  function reset(kind = "full") {
    documents = kind === "empty" ? [] : DOCUMENTS;
  }

  return { apAging, reset };
}
