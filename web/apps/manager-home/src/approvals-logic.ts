// Which kind the Approvals view opens on. The tabs are the closed kind
// vocabulary in the core's own order, and the view used to open on the first of
// them whatever was waiting, so a controller with two freight raises landed on
// an empty Margin tab and had to find Freight themselves. The core's whole list
// (an empty kind) is every request in the caller's branches, kinds the caller
// cannot decide included, so it only names what is waiting: the view tries the
// waiting kinds in vocabulary order and keeps the first one the core does not
// refuse (app-root.ts), else the first kind.
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";
import { APPROVAL_KINDS, kindWord } from "./manager-logic.js";
import type { ApprovalRow, ChangeOrder, ChangeOrderQueueRow } from "./manager-api.js";

/** Every kind with a waiting row, in the vocabulary's order, once each. */
export function waitingKinds(
  rows: readonly Pick<ApprovalRow, "policy_type" | "status">[],
  changeOrderQueue: readonly ChangeOrderQueueRow[] = [],
): string[] {
  const waiting = new Set(rows.filter((r) => r.status === "PENDING").map((r) => r.policy_type));
  if (changeOrderQueue.length > 0) waiting.add("CHANGE_ORDER");
  return APPROVAL_KINDS.filter((k) => waiting.has(k.kind)).map((k) => k.kind);
}

export function openingKind(
  rows: readonly Pick<ApprovalRow, "policy_type" | "status">[],
  changeOrderQueue: readonly ChangeOrderQueueRow[] = [],
): string {
  return waitingKinds(rows, changeOrderQueue)[0] ?? APPROVAL_KINDS[0].kind;
}

export interface MarginMetricBand {
  readonly lineMargin: string;
  readonly floor: string;
  readonly shortOfFloor: string;
  readonly inDollars: string;
}

export interface MarginLine {
  readonly lineName: string;
  readonly lineSku: string;
  readonly qty: string;
  readonly price: string;
  readonly cost: string;
  readonly margin: string;
  readonly floor: string;
}

export interface CreditMetricBand {
  readonly creditLimit: string;
  readonly openBalance: string;
  readonly overLimitBy: string;
  readonly pastDue: string;
}

export interface JournalEntryLine {
  readonly account: string;
  readonly debit: string;
  readonly credit: string;
}

export interface MetadataRow {
  readonly label: string;
  readonly value: string;
  readonly tone?: "watch" | "good" | "stop";
}

export interface ChangeOrderMetricBand {
  readonly delta: string;
  readonly threshold: string;
  readonly financeRequired: string;
}

export interface ChangeOrderLineItem {
  readonly op: string;
  readonly product: string;
  readonly before: string;
  readonly after: string;
  readonly lineDelta: string;
}

export interface ApprovalCardItem {
  readonly id: string;
  readonly kind: string;
  readonly title: string;
  readonly amount: string;
  /** What the figure measures, said beside it ("under the floor", "at stake"); empty when there is no figure. */
  readonly amountLabel: string;
  readonly summary: string;
  readonly timeAgo: string;
  readonly status: "PENDING" | "APPROVED" | "REJECTED";
  readonly detailTitle: string;
  readonly detailSubtitle: string;
  readonly isCompanion?: boolean;
  readonly marginMetrics?: MarginMetricBand;
  readonly marginLines?: readonly MarginLine[];
  readonly creditMetrics?: CreditMetricBand;
  readonly journalLines?: readonly JournalEntryLine[];
  readonly changeMetrics?: ChangeOrderMetricBand;
  readonly changeLines?: readonly ChangeOrderLineItem[];
  readonly canSignApply?: boolean;
  readonly canReject?: boolean;
  readonly metadata: readonly MetadataRow[];
  readonly notYours?: {
    readonly waitingOn: string;
    readonly badge: string;
    readonly delegateName: string;
  };
}

export const BOARD_APPROVAL_ITEMS: readonly ApprovalCardItem[] = Object.freeze([
  {
    id: "0e000000-0000-4000-9000-0000000000a1",
    kind: "MARGIN_BELOW_MINIMUM",
    title: "Q-2001 · Bluffside Builders",
    amount: "$35.59",
    amountLabel: "under the floor",
    summary: "Margin floor, line 2",
    timeAgo: "Inside Sales asked 2 days ago",
    status: "PENDING",
    detailTitle: "Q-2001 · Seat wall block, tan blend",
    detailSubtitle: "Bluffside Builders · quote total $4,187.88 · Point Loma",
    marginMetrics: {
      lineMargin: "18.4%",
      floor: "22.0%",
      shortOfFloor: "3.6 points",
      inDollars: "$35.59 on this line",
    },
    marginLines: [
      {
        lineName: "Seat wall block, tan blend",
        lineSku: "WAL-SEAT-TN, 2% discount",
        qty: "120 pc",
        price: "$6.56",
        cost: "$5.25",
        margin: "18.4%",
        floor: "22.0%",
      },
    ],
    metadata: [
      { label: "Asked by", value: "Inside Sales, 2 days ago" },
      { label: "Their note", value: "Granite Supply quoted $6.40 on the same block; the customer will go there for the walls." },
      { label: "Customer standing", value: "Terms 30 days. Past due $14,820.00 (61 to 90), account on hold. 41 orders and $186,400.00 in the last 12 months.", tone: "watch" },
      { label: "The whole quote", value: "24.6% margin on $3,886.66, above its floor; only this line is under." },
    ],
  },
  {
    id: "0e000000-0000-4000-9000-0000000000a4",
    kind: "MARGIN_BELOW_MINIMUM",
    title: "Q-2044 · Fieldstone Pools and Patios",
    amount: "$72.10",
    amountLabel: "under the floor",
    summary: "Margin floor, line 1",
    timeAgo: "Counter Sales asked yesterday",
    status: "PENDING",
    detailTitle: "Q-2044 · Fieldstone Pools and Patios",
    detailSubtitle: "Fieldstone Pools and Patios · quote total $4,312.75 · Point Loma",
    isCompanion: true,
    marginMetrics: {
      lineMargin: "14.2%",
      floor: "20.0%",
      shortOfFloor: "5.8 points",
      inDollars: "$72.10 on this line",
    },
    marginLines: [
      {
        lineName: "Flagstone coping, charcoal blend",
        lineSku: "COP-FLG-CH, 5% discount",
        qty: "85 pc",
        price: "$18.20",
        cost: "$15.62",
        margin: "14.2%",
        floor: "20.0%",
      },
    ],
    metadata: [
      { label: "Asked by", value: "Counter Sales, yesterday" },
      { label: "Their note", value: "Customer matching existing patio coping, asked for contractor tier." },
      { label: "Customer standing", value: "In good standing: nothing past due, 3 years as a customer.", tone: "good" },
      { label: "The whole quote", value: "21.4% margin on $4,312.75." },
    ],
  },
  {
    id: "0e000000-0000-4000-9000-0000000000c1",
    kind: "CREDIT_LIMIT_EXCEEDED",
    title: "SO-PLM-1049 · Fieldstone Pools and Patios",
    amount: "$2,100.00",
    amountLabel: "over the limit",
    summary: "Credit hold, over limit by $2,100.00",
    timeAgo: "Placed by the system today",
    status: "PENDING",
    detailTitle: "SO-PLM-1049 · Fieldstone Pools and Patios",
    detailSubtitle: "Order $11,350.00 · Point Loma · held for credit",
    creditMetrics: {
      creditLimit: "$25,000.00",
      openBalance: "$27,100.00",
      overLimitBy: "$2,100.00",
      pastDue: "$0.00",
    },
    metadata: [
      { label: "Placed by", value: "The system, today, when the order was rung on account" },
      { label: "Standing", value: "In good standing: nothing past due, 3 years as a customer, $92,300.00 in the last 12 months.", tone: "good" },
      { label: "Open promise", value: "None" },
    ],
  },
  {
    id: "0e000000-0000-4000-9000-0000000000j1",
    kind: "JE_OVER_THRESHOLD",
    title: "JE-0087 · Freight reclass",
    amount: "$1,240.00",
    amountLabel: "to reclassify",
    summary: "Reclassify freight in to cost of goods",
    timeAgo: "Dispatch asked yesterday, waiting on the financial controller",
    status: "PENDING",
    detailTitle: "JE-0087 · Reclassify freight in to cost of goods",
    detailSubtitle: "Journal entry · $1,240.00 · both branches",
    journalLines: [
      { account: "5100 Cost of goods sold", debit: "$1,240.00", credit: "" },
      { account: "5400 Freight in", debit: "", credit: "$1,240.00" },
    ],
    metadata: [
      { label: "Asked by", value: "Dispatcher, yesterday" },
      { label: "Their note", value: "Inbound freight on PO-PLM-136 was posted to freight in; it belongs in cost of goods for the quarter." },
      { label: "Waiting on", value: "The financial controller. A general manager does not decide journal entries.", tone: "watch" },
    ],
    notYours: {
      waitingOn: "The financial controller. A general manager does not decide journal entries.",
      badge: "DECIDED BY THE CONTROLLER",
      delegateName: "the financial controller",
    },
  },
]);

// The reference a request names, read from its words ("Quote Q-2002 line 1 ..."): one id on a row. A row whose words
// carry none is titled by its kind, never by the raw subject reference.
export function refOfDetail(detail: string): string {
  const m = /\b([A-Z]{1,4}-[A-Z0-9][A-Z0-9-]*)\b/.exec(detail);
  return m ? m[1] : "";
}

// The row's second line: the words of the request without the reference the title already carries.
function summaryOfDetail(detail: string, ref: string): string {
  if (ref === "") return detail;
  const rest = detail.replace(new RegExp(`^(?:Quote|Order|Journal entry|Journal)?\\s*${ref}\\s*`), "").trim();
  return rest === "" ? detail : rest.charAt(0).toUpperCase() + rest.slice(1);
}

const FIGURE_LABEL: Readonly<Record<string, string>> = Object.freeze({
  MARGIN_BELOW_MINIMUM: "at stake",
  CREDIT_LIMIT_EXCEEDED: "at stake",
  COD_CONSTRAINT: "at stake",
  JE_OVER_THRESHOLD: "to post",
  FREIGHT_ABOVE_QUOTE: "above the quote",
});

export function toApprovalCardItem(row: ApprovalRow): ApprovalCardItem {
  const isPending = row.status === "PENDING";
  const amountStr = row.amount_cents > 0 ? formatDollars(row.amount_cents) : "";
  const ref = refOfDetail(row.detail);
  const status = isPending ? "PENDING" : row.status === "APPROVED" ? "APPROVED" : "REJECTED";
  return {
    id: row.id,
    kind: row.policy_type,
    title: ref !== "" ? ref : kindWord(row.policy_type),
    amount: amountStr,
    amountLabel: amountStr === "" ? "" : (FIGURE_LABEL[row.policy_type] ?? "at stake"),
    summary: summaryOfDetail(row.detail, ref),
    timeAgo: status === "PENDING" ? "Waiting for a decision" : "Decided",
    status,
    detailTitle: ref !== "" ? `${ref} · ${kindWord(row.policy_type)}` : kindWord(row.policy_type),
    detailSubtitle: "",
    metadata: [{ label: "The request", value: row.detail }],
  };
}

export function changeOrderStatusWord(status: string): string {
  if (status === "PENDING_FINANCE") return "Waiting on finance";
  if (status === "APPROVED") return "Approved, ready to apply";
  return status;
}

export function changeOrderOpWord(op: string): string {
  if (op === "ADD") return "Add";
  if (op === "QTY") return "Change quantity";
  if (op === "REMOVE") return "Remove";
  return op;
}

export function formatChangeOrderCents(cents: number): string {
  return formatDollars(cents);
}

export function changeOrderAllowsSignApply(nextActions: readonly string[] | null | undefined): boolean {
  return Array.isArray(nextActions) && nextActions.includes("sign_apply");
}

export function changeOrderAllowsReject(nextActions: readonly string[] | null | undefined): boolean {
  return Array.isArray(nextActions) && nextActions.includes("reject");
}

export function nextIdempotencyKey(): string {
  return crypto.randomUUID();
}

export interface PendingSignApply {
  readonly id: string;
  readonly key: string;
}

export function keyForSignApply(
  pending: PendingSignApply | null,
  id: string,
): { readonly pending: PendingSignApply; readonly reused: boolean } {
  if (pending !== null && pending.id === id) {
    return { pending, reused: true };
  }
  return { pending: { id, key: crypto.randomUUID() }, reused: false };
}

function qtyWord(qty: number): string {
  if (!Number.isFinite(qty)) return "0";
  const rounded = Math.round(qty * 10000) / 10000;
  return String(rounded);
}

function changeOrderLineItem(line: ChangeOrder["lines"][number]): ChangeOrderLineItem {
  return {
    op: changeOrderOpWord(line.op),
    product: line.product_name || line.description || line.product_id || "line",
    before: qtyWord(line.qty_base_before),
    after: qtyWord(line.qty_base_after),
    lineDelta: formatChangeOrderCents(line.line_delta_cents),
  };
}

export function toChangeOrderCardItem(row: ChangeOrderQueueRow, detail: ChangeOrder | null): ApprovalCardItem {
  const isPending = row.status === "PENDING_FINANCE";
  return {
    id: row.id,
    kind: "CHANGE_ORDER",
    title: `${row.change_no} · ${row.order_no}`,
    amount: formatChangeOrderCents(row.delta_cents),
    amountLabel: "change",
    summary: row.reason,
    timeAgo: `${changeOrderStatusWord(row.status)} · requested by ${row.requested_by_role || "staff"}`,
    status: isPending ? "PENDING" : row.status === "APPROVED" ? "APPROVED" : "REJECTED",
    detailTitle: `${row.change_no} · ${row.order_no}`,
    detailSubtitle: `${changeOrderStatusWord(row.status)} · ${row.reason}`,
    canSignApply: changeOrderAllowsSignApply(row.next_actions),
    canReject: changeOrderAllowsReject(row.next_actions),
    changeMetrics: {
      delta: formatChangeOrderCents(row.delta_cents),
      threshold: formatChangeOrderCents(row.finance_threshold_cents),
      financeRequired: row.finance_required ? "Finance signature required" : "Below finance threshold",
    },
    changeLines: detail ? detail.lines.map(changeOrderLineItem) : [],
    metadata: [
      { label: "Order", value: row.order_no },
      { label: "Change", value: row.change_no },
      { label: "Requested by", value: row.requested_by_role || "staff" },
      { label: "Yard note", value: row.yard_ack_note || "None" },
      { label: "Reason", value: row.reason },
    ],
  };
}

// The core's rows win. A board item only enriches the row whose id it carries; it never stands in for a row the core
// does not have (a decision on an invented id is a 404 the person is told was a yes). Waiting rows first, then the
// decided ones, so a decision moves a row down instead of leaving it looking undecided.
export function itemsForKind(kind: string, rows: readonly ApprovalRow[]): readonly ApprovalCardItem[] {
  const items = rows
    .filter((r) => r.policy_type === kind)
    .map((row) => {
      const board = BOARD_APPROVAL_ITEMS.find((b) => b.id === row.id);
      const card = toApprovalCardItem(row);
      if (board === undefined) return card;
      // The board only adds what the core row lacks (the customer in the title, the role label, the metric bands). The
      // row's own figure, its word, its summary and its status stay the core's: no figure on the row, none shown.
      const sameRef = refOfDetail(board.title) === refOfDetail(row.detail);
      return {
        ...board,
        title: sameRef ? board.title : card.title,
        amount: card.amount,
        amountLabel: card.amountLabel,
        summary: card.summary,
        timeAgo: card.status === "PENDING" ? board.timeAgo : card.timeAgo,
        status: card.status,
      };
    });
  return [...items.filter((i) => i.status === "PENDING"), ...items.filter((i) => i.status !== "PENDING")];
}

export function changeOrderItems(
  queue: readonly ChangeOrderQueueRow[],
  details: ReadonlyMap<string, ChangeOrder>,
): readonly ApprovalCardItem[] {
  return queue.map((row) => toChangeOrderCardItem(row, details.get(row.id) ?? null));
}

/** The waiting count of a tab: the core's own count when the view holds one, else this kind's waiting rows. */
export function kindCount(
  kind: string,
  rows: readonly ApprovalRow[],
  changeOrderQueue: readonly ChangeOrderQueueRow[] = [],
  counts?: Readonly<Record<string, number>>,
): number {
  if (kind === "CHANGE_ORDER") return changeOrderQueue.length;
  if (counts && kind in counts) return counts[kind];
  return rows.filter((r) => r.policy_type === kind && r.status === "PENDING").length;
}

/** Waiting rows counted per kind, from the core's list of waiting requests. */
export function pendingCounts(rows: readonly Pick<ApprovalRow, "policy_type" | "status">[]): Record<string, number> {
  const out: Record<string, number> = {};
  for (const k of APPROVAL_KINDS) out[k.kind] = 0;
  for (const r of rows) if (r.status === "PENDING") out[r.policy_type] = (out[r.policy_type] ?? 0) + 1;
  return out;
}

/** The sentence that names the thing a decision is about: "Q-2002, $35.59 under the floor". */
export function decisionSubject(item: Pick<ApprovalCardItem, "title" | "amount" | "amountLabel">): string {
  const ref = item.title.split(" · ")[0];
  return item.amount === "" ? ref : `${ref}, ${item.amount} ${item.amountLabel}`;
}
