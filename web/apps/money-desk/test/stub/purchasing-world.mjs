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

/** The band each of the core's bucket keys holds its money in. */
const BUCKET_FIELD = {
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

export function createPurchasingWorld({ dayOffset, houseDay }) {
  // The documents the world holds. The empty scenario holds none, so the board reads the
  // shape of a dealer who owes nothing on that day.
  let documents = DOCUMENTS;
  // The payments the route recorded, by bill: what was paid, on what day, and the note it went
  // with. A payment lands on its own day, so a bill paid after the day the books are read as of
  // is still open on that day.
  const payments = new Map();
  // The key every press carried: one key is one payment across every bill, so a key spent on one
  // bill is a conflict on another, and a key spent on this bill is a replay that writes nothing.
  const keys = new Map();

  /** What has been paid against a document on the day the books are read as of. */
  function paidBy(doc, asOf) {
    let paid = doc.paid === undefined || dayOffset(doc.paid_on) > asOf ? 0 : doc.paid;
    for (const payment of payments.get(doc.n) ?? []) {
      if (payment.on <= asOf) paid += payment.amount;
    }
    return paid;
  }

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
        const paid = paidBy(doc, asOf);
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

  /**
   * One payment against one bill: the core's POST /api/v1/purchasing/bills/{id}/payments.
   *
   * The key is judged first, the way the core judges it: a key already spent on this bill is a
   * replay, which answers the first payment and writes nothing, and a key spent on another bill is
   * a conflict. Then the amount: it must be positive, and it must not be more than the bill has
   * open. The refusal words are the core's own sentences.
   */
  function payBill(id, body, key) {
    if (key === "") return { status: 400, error: "purchasing: idempotency_key is required on a bill payment (one key per press)" };
    const amount = Number(body.amount_cents);
    if (!Number.isInteger(amount) || amount <= 0) return { status: 400, error: "purchasing: payment amount_cents must be positive" };
    const spent = keys.get(key);
    if (spent !== undefined) {
      if (spent.id !== id) return { status: 409, error: "purchasing: idempotency key already used for another bill's payment" };
      // A replay of this bill's own payment: the first outcome, and nothing written.
      return { status: 200, body: billDetail(spent.id) };
    }
    const doc = documents.find((d) => billId(d.n) === id);
    if (doc === undefined) return { status: 404, error: "purchasing: bill not found" };
    const open = doc.total - paidBy(doc, dayOffset(0));
    if (amount > open) return { status: 409, error: `purchasing: payment of ${amount} cents exceeds open balance ${open} cents on bill ${doc.doc}` };
    // The payment lands on the day it carries: the instant's own day in the house zone.
    const on = body.paid_at === undefined || body.paid_at === "" ? dayOffset(0) : houseDay(new Date(body.paid_at));
    payments.set(doc.n, [...(payments.get(doc.n) ?? []), { amount, on, note: body.note ?? "" }]);
    keys.set(key, { id, amount });
    return { status: 200, body: billDetail(id) };
  }

  /** The bill as the route answers it: the header, its lines and the instalments it carries. */
  function billDetail(id) {
    const doc = documents.find((d) => billId(d.n) === id);
    const payee = PAYEES.find((p) => p.n === doc.payee);
    const instalments = (payments.get(doc.n) ?? []).map((payment, index) => ({
      id: `${billId(doc.n)}-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      seq: index + 1,
      due_date: dayOffset(doc.due),
      amount_cents: payment.amount,
      status: "PAID",
      note: payment.note,
    }));
    return {
      id: billId(doc.n),
      bill_no: doc.doc,
      vendor_id: payeeId(doc.payee),
      vendor_name: payee.name,
      branch_id: branchId(),
      po_id: "",
      po_no: "",
      status: "POSTED",
      match_state: "MATCHED",
      bill_date: dayOffset(doc.issued),
      due_date: dayOffset(doc.due),
      subtotal_cents: doc.total,
      tax_cents: 0,
      total_cents: doc.total,
      currency: "CAD",
      fx_rate: 1,
      created_at: `${dayOffset(doc.issued)}T12:00:00Z`,
      lines: [],
      instalments,
    };
  }

  /** Puts the world back to the start; the empty scenario holds no documents at all. */
  function reset(kind = "full") {
    documents = kind === "empty" ? [] : DOCUMENTS;
    payments.clear();
    keys.clear();
  }

  return { apAging, payBill, reset };
}
