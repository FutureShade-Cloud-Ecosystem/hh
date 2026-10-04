// Ported from hardscapeos_dibbits master 87d7941b app/src/_pricing-audit.ts; adapted for HH.
//
// _pricing-audit.ts: the pricing log's date range and paging, and the link from a log row
// to the record it is about (DOM-free, unit tested on the Node runner).
//
// The date range and the pager compose: the range narrows the rows already fetched, the
// pager walks what is left. The server takes no date bounds, so the range is client side
// over the window the server sent, and auditWindowNote says so on screen when the fetch
// came back at its cap. A row whose date cannot be read is kept, never hidden: this is a
// log, and a row vanishing because of a parse failure must not happen quietly.

/** The subset of a log row the date range reads. */
export interface DatedRow {
  created_at: string;
}

/** An inclusive calendar day range; either end may be "" for open. */
export interface DateRange {
  from: string;
  to: string;
}

export const EMPTY_RANGE: DateRange = { from: "", to: "" };

/** Rows per page. */
export const PAGE_SIZE = 25;

const ISO_DAY = /^\d{4}-\d{2}-\d{2}$/;

/**
 * The yyyy-mm-dd the VIEWER sees for a timestamp. Comparing local calendar days, not UTC
 * instants, keeps the filter in agreement with the When column beside it. "" when the
 * timestamp does not parse.
 */
export function localDay(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/**
 * Drops any bound that is not a yyyy-mm-dd day and swaps the ends when they are the wrong
 * way round: a half typed date input emits garbage on the way to a valid value, and a
 * transposed range is a typo, not a request for nothing.
 */
export function normaliseRange(range: Partial<Record<keyof DateRange, unknown>> | null | undefined): DateRange {
  const from = typeof range?.from === "string" && ISO_DAY.test(range.from) ? range.from : "";
  const to = typeof range?.to === "string" && ISO_DAY.test(range.to) ? range.to : "";
  if (from !== "" && to !== "" && from > to) return { from: to, to: from };
  return { from, to };
}

export function inDateRange(row: DatedRow, range: Partial<Record<keyof DateRange, unknown>>): boolean {
  const { from, to } = normaliseRange(range);
  if (from === "" && to === "") return true;
  const day = localDay(row.created_at);
  if (day === "") return true;
  if (from !== "" && day < from) return false;
  if (to !== "" && day > to) return false;
  return true;
}

export function filterByDate<T extends DatedRow>(rows: readonly T[], range: Partial<Record<keyof DateRange, unknown>>): T[] {
  const r = normaliseRange(range);
  if (r.from === "" && r.to === "") return [...rows];
  return rows.filter((row) => inDateRange(row, r));
}

/** How many pages a set needs; at least 1 so the pager reads "1 of 1". */
export function pageCount(total: number, size: number = PAGE_SIZE): number {
  if (!Number.isFinite(total) || total <= 0) return 1;
  const s = Number.isFinite(size) && size > 0 ? size : PAGE_SIZE;
  return Math.max(1, Math.ceil(total / s));
}

/** Clamp a 1 based page into range, for a filter that shrinks the set under the reader. */
export function clampPage(page: number, total: number, size: number = PAGE_SIZE): number {
  const last = pageCount(total, size);
  if (!Number.isFinite(page)) return 1;
  return Math.min(Math.max(1, Math.trunc(page)), last);
}

export interface Page<T> {
  rows: T[];
  page: number;
  pages: number;
  firstIndex: number;
  lastIndex: number;
  total: number;
  hasPrev: boolean;
  hasNext: boolean;
}

/** One page of an already filtered set, with the counts the pager reads ("26 to 50 of 137"). */
export function pageOf<T>(rows: readonly T[], page: number, size: number = PAGE_SIZE): Page<T> {
  const s = Number.isFinite(size) && size > 0 ? Math.trunc(size) : PAGE_SIZE;
  const total = rows.length;
  const pages = pageCount(total, s);
  const current = clampPage(page, total, s);
  const start = (current - 1) * s;
  const slice = rows.slice(start, start + s);
  return {
    rows: slice,
    page: current,
    pages,
    firstIndex: total === 0 ? 0 : start + 1,
    lastIndex: total === 0 ? 0 : start + slice.length,
    total,
    hasPrev: current > 1,
    hasNext: current < pages,
  };
}

/**
 * The honesty line under the pager: the range runs over the rows the server sent, and a
 * fetch that came back at its cap has older rows the range never saw. null when the
 * fetch came back short, which means the window is the whole log.
 */
export function auditWindowNote(fetched: number, limit: number): string | null {
  if (!Number.isFinite(fetched) || !Number.isFinite(limit) || fetched < limit) return null;
  return `Filtering the most recent ${limit} entries: older entries are outside this window.`;
}

// ── the link from a row to its record ────────────────────────────────────────

/** The panel's tabs a log row can open. */
export type PricingTabId = "margin" | "rules" | "customers" | "breaks" | "audit";

export interface AuditRef {
  ref_entity_type?: string;
  ref_entity_id?: string;
}

export interface DrillTarget {
  kind: "customer" | "category";
  id: string;
  tab: PricingTabId;
  label: string;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * Where a row's reference opens. A customer opens that account on the Customer prices tab
 * and a category opens its row on the Margin policy tab. A product, quote or order
 * reference has no destination in this panel, so it is no link at all: a control that
 * looks clickable and is not is worse than plain text. Null is the expected answer for a
 * large minority of rows.
 */
export function drillTarget(ref: AuditRef | null | undefined): DrillTarget | null {
  const kind = typeof ref?.ref_entity_type === "string" ? ref.ref_entity_type.trim() : "";
  const id = typeof ref?.ref_entity_id === "string" ? ref.ref_entity_id.trim() : "";
  if (kind === "" || id === "" || !UUID.test(id)) return null;
  if (kind === "customer") return { kind, id, tab: "customers", label: "Open customer pricing" };
  if (kind === "category") return { kind, id, tab: "margin", label: "Open category policy" };
  return null;
}

// ── the Change column ────────────────────────────────────────────────────────

const BYPASS_WORDS: Readonly<Record<string, string>> = {
  PRODUCT_POLICY_DISABLED: "the product's margin policy is switched off",
  CATEGORY_POLICY_DISABLED: "the category's margin policy is switched off",
  APPROVED_OVERRIDE: "an approved override",
  MANAGER_SCOPE_OVERRIDE: "a manager's override",
};

/** Why a BYPASS row bypassed the floor, in words; the code with spaces for a reason this app does not know yet. */
export function bypassWord(reason: string | undefined): string {
  if (reason === undefined || reason === "") return "";
  return BYPASS_WORDS[reason] ?? reason.replace(/_/g, " ");
}

const UUID_G = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;

/** The product ids a floor confirmation's text names, each once, in order. */
export function floorProductIds(text: string | undefined): string[] {
  if (text === undefined) return [];
  return [...new Set(text.match(UUID_G) ?? [])];
}

/** A floor confirmation's text with each product id replaced by its name where known, else its first eight characters. */
export function floorConfirmationText(text: string, names: ReadonlyMap<string, string>): string {
  return text.replace(UUID_G, (id) => names.get(id) ?? id.slice(0, 8));
}
