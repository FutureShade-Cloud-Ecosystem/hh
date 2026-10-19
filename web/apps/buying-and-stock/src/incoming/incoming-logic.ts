// The Incoming area's plain logic: one row per line of a placed order that is
// still to arrive, and the soonest promised first. Nothing here computes money;
// every figure is the server's own, carried into the row's shape. The all
// arrivals list is the register plus each placed order's own detail read: the
// core's incoming read answers for one product only, so this join is the app's.
import type { PurchasingPO, PurchasingPODetail } from "@hardscapehouse/api-client";

/** One line still to arrive: the order it rides, what and how much, and the promise. */
export interface ArrivalRow {
  readonly poId: string;
  readonly poNo: string;
  readonly vendor: string;
  readonly product: string;
  readonly qtyBase: number;
  readonly uom: string;
  /** The order's promised stamp, "" when it carries none. */
  readonly expected: string;
  /** The order's delivery method, "" when it carries none. */
  readonly method: string;
}

/** The lines of one placed order that have not landed yet: what is still coming. */
export function arrivalRows(po: PurchasingPO, detail: PurchasingPODetail): ArrivalRow[] {
  const rows: ArrivalRow[] = [];
  for (const line of detail.lines) {
    const outstanding = line.qty_ordered_base - line.qty_received_base;
    if (outstanding <= 0) continue;
    rows.push({
      poId: po.id,
      poNo: po.po_no,
      vendor: po.vendor_name,
      product: line.description,
      qtyBase: outstanding,
      uom: line.uom,
      expected: po.expected_at ?? "",
      method: po.delivery_method ?? "",
    });
  }
  return rows;
}

/** Soonest promised first, the core's own order for its incoming read; a line without a promise waits at the foot. */
export function sortByPromise(rows: readonly ArrivalRow[]): ArrivalRow[] {
  return [...rows].sort((a, b) => {
    if (a.expected === b.expected) return 0;
    if (a.expected === "") return 1;
    if (b.expected === "") return -1;
    return a.expected < b.expected ? -1 : 1;
  });
}
