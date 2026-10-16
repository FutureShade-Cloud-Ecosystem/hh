// The Sales stock lookup's plain logic (change 5, board bs-stock-lookup-sales-search-a-product):
// who holds the read only seat, the chips a row answers to, the words the list and the detail draw,
// and the coming-in rows built off the purchasing read. The figures are the server's, said; nothing
// here computes stock, and no cell on the surface draws cost, whatever the answers carry.
//
// The seat is read only by ruling R1: the two sales roles hold Buying and Stock at read, for the
// stock lookup. The core's wall stays the authority (the po_writers list and the work level on the
// receiving and manage routes refuse them); this only keeps the app from offering a sales seat a
// buying surface at all.
import { formatMoney, formatQty } from "../po/po-money.js";
import { whenWords } from "../po/po-logic.js";

export const SALES_SEATS = new Set(["inside_sales", "outside_sales"]);

export function stockLookupSeat(roleCode: string): boolean {
  return SALES_SEATS.has(roleCode);
}

// The chips, in the board's order. In stock is the roll-up's available above zero; low is in stock
// but out at one or more branches (the row names them): some to sell, not everywhere, the honest
// "check before you promise it" a sales seat filters for. A row with nothing available answers to
// neither chip; it is out, and the list says so.
export const LOOKUP_CHIPS: readonly { id: "all" | "in-stock" | "low"; label: string }[] = [
  { id: "all", label: "All" },
  { id: "in-stock", label: "In stock" },
  { id: "low", label: "Low" },
];

interface LookupRow {
  readonly available_base: number;
  readonly sold_by?: string;
  readonly out_at_branches?: readonly string[];
}

/** Whether one row passes one chip. */
export function chipGrants(chip: string, row: LookupRow): boolean {
  if (chip === "in-stock") return row.available_base > 0;
  if (chip === "low") return row.available_base > 0 && (row.out_at_branches?.length ?? 0) > 0;
  return true;
}

/** The names joined the way a sentence reads: "A", "A and B", "A, B and C". */
function andList(names: readonly string[]): string {
  if (names.length <= 1) return names[0] ?? "";
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

/**
 * The list line's availability words: the total with its unit when there is stock to sell, the
 * branches whose records show nothing available when there is not, "Out of stock" when no branch
 * holds a record at all, and no words before the answer has landed.
 */
export function availabilityWords(row: LookupRow | undefined): string {
  if (row === undefined) return "";
  if (row.available_base > 0) return `${formatQty(row.available_base, row.sold_by)} available`;
  const out = row.out_at_branches ?? [];
  if (out.length > 0) return `Out at ${andList(out)}`;
  return "Out of stock";
}

/** Whether the availability line reads as bad news, the red the board draws. */
export function availabilityIsOut(row: LookupRow | undefined): boolean {
  return row !== undefined && row.available_base <= 0;
}

// The trade's sale units, as the header's "sold by" sentence reads them; a unit the map does not
// know reads as its own code, never as a guess. The list quantities keep the bare code, as the
// app's tables always have.
const SOLD_BY_WORDS: Record<string, string> = {
  SF: "the square foot",
  PC: "the piece",
  EA: "each",
  T: "the tonne",
  BG: "the bag",
};

export function soldByWords(code: string | null | undefined): string {
  const unit = (code ?? "").trim();
  if (unit === "") return "";
  return SOLD_BY_WORDS[unit.toUpperCase()] ?? unit;
}

/** The header's list price line, from the product read: "$4.00 list", nothing when none is set. */
export function listPriceWords(listPriceCents: number | null | undefined): string {
  if (typeof listPriceCents !== "number") return "";
  return `${formatMoney(listPriceCents)} list`;
}

interface IncomingLine {
  readonly po_no: string;
  readonly vendor_name: string;
  readonly branch_id: string;
  readonly expected_at?: string;
  readonly qty_outstanding_base: number;
  readonly uom: string;
  readonly status: string;
}

export interface ComingInRow {
  /** The order and its vendor, the board's one cell: "PO-PLM-136 · Copperline Pavers". */
  readonly order: string;
  readonly branch: string;
  readonly qty: string;
  readonly expected: string;
  /** A draft is a start, not a promise: the table marks it, the words the register uses. */
  readonly draft: boolean;
}

/** The detail's coming-in rows off the purchasing read, the branch's own name when the desk holds it. */
export function comingInRows(lines: readonly IncomingLine[], branchNames: ReadonlyMap<string, string>, today: string): ComingInRow[] {
  return lines.map((l) => ({
    order: `${l.po_no} · ${l.vendor_name}`,
    branch: branchNames.get(l.branch_id) ?? l.branch_id,
    qty: formatQty(l.qty_outstanding_base, l.uom),
    expected: whenWords(l.expected_at, today),
    draft: l.status === "DRAFT",
  }));
}
