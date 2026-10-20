// The reorder move: buy the same again, from an order card on the board. The
// preview re-prices every line at today's rates and re-checks availability
// (the core's numbers; this page adds none up, the new order's totals are the
// core's). The reorder lands as a draft the contractor reviews on the board,
// and the create rides an idempotency key so a retried submit cannot make a
// twin. The move is gated: opening the preview takes view_orders_deliveries,
// creating takes create_orders, and a refused create reads the kit's refusal
// words. Ported from the original portal's reorder line picker
// (portal-reorder-modal.ts); the running total it summed on the page is left
// out, because money arithmetic is the core's, never the screen's.
import { html, nothing, type TemplateResult } from "lit";
import { tenantOrders, type TenantReorderLine, type TenantReorderPreview } from "@hardscapehouse/api-client";
import { priceWords, qtyWords } from "./figures.js";
import { actionFailure, button, cancelButton, modalSheet, type Page, type SurfaceAnswer } from "./views.js";

export function reorderModalWords(): string {
  return "Prices are today's rates, not the originals. The reorder lands as a draft you can review before it is placed.";
}

export async function reorderPreview(page: Page, orderId: string): Promise<{ kind: "ready"; data: TenantReorderPreview } | { kind: "error"; status?: number }> {
  const answer = await tenantOrders.tenantGetReorderPreview(page.client, orderId);
  if (answer.ok) return { kind: "ready", data: answer.value };
  return { kind: "error", status: answer.status };
}

export async function reorderCreate(
  page: Page,
  orderId: string,
  lines: number[],
  idempotencyKey: string,
): Promise<{ kind: "ready"; data: { order_id: string; order_no: string; status: string } } | { kind: "refusal" } | { kind: "error"; error: string; status?: number }> {
  // The screen's gate before the wire: the core answers 403 to a role without
  // create_orders, and the screen never offers the move it holds.
  if (!page.session.can("create_orders")) return { kind: "refusal" };
  const answer = await tenantOrders.tenantCreateReorder(page.client, orderId, {
    lines: [...lines].sort((a, b) => a - b),
    idempotency_key: idempotencyKey,
  });
  if (answer.ok) return { kind: "ready", data: answer.value };
  return { kind: "error", error: answer.error, status: answer.status };
}

// ---- the modal over the board ---------------------------------------------------------------------

interface ReorderState {
  orderId: string;
  preview: TenantReorderPreview | null;
  loading: boolean;
  busy: boolean;
  error: string;
  chosen: Set<number>;
  mayCreate: boolean;
  /**
   * The create's idempotency key, minted when the modal opened and sent
   * unchanged on every press: the core replays the draft it made under a
   * repeated key, so a retry after a transport error cannot mint a twin. It
   * is minted again only when the selection changes (a different reorder) or
   * on the next open.
   */
  key: string;
}

const reorderStates = new WeakMap<Page, ReorderState>();

function lineRow(state: ReorderState, line: TenantReorderLine): TemplateResult {
  const chosen = state.chosen.has(line.line_no);
  if (line.available !== true) {
    return html`<li>
      <div class="result off" data-testid="reorder-line-off">
        <span class="name">${line.description}</span>
        <span class="dim">${qtyWords(line.qty_entered, line.uom_entered)}</span>
        ${line.reason === undefined || line.reason === "" ? nothing : html`<span class="mono dim">${line.reason}</span>`}
      </div>
    </li>`;
  }
  return html`<li>
    <button type="button" class="result ${chosen ? "chosen" : ""}" data-action="toggle-reorder-line" data-line="${line.line_no}" data-testid="reorder-line">
      <span class="name">${line.description}</span>
      <span class="dim">${qtyWords(line.qty_entered, line.uom_entered)}</span>
      <span class="mono">${priceWords(line.line_total_cents)}</span>
    </button>
  </li>`;
}

function reorderModal(state: ReorderState): TemplateResult {
  const lines = state.preview?.lines ?? [];
  const body = html`<p class="words">${reorderModalWords()}</p>
    ${state.loading
      ? html`<hh-pro-state data-state="loading" data-testid="reorder-state"></hh-pro-state>`
      : html`<ul class="picker-results" data-testid="reorder-lines">${lines.map((line) => lineRow(state, line))}</ul>`}
    ${state.error === "" ? nothing : html`<p class="error" role="alert" data-testid="reorder-error">${state.error}</p>`}`;
  const count = state.chosen.size;
  const footer =
    state.mayCreate === true
      ? html`${cancelButton}${button({
          label: count === 1 ? "Create draft order (1 line)" : `Create draft order (${count} lines)`,
          action: "create-reorder",
          disabled: state.loading === true || state.busy === true || count === 0,
        })}`
      : html`<span class="dim">Your role does not open this. Ask the Owner on your account to change your access.</span>`;
  return modalSheet(`Reorder ${state.preview?.source_order_no ?? ""}`, body, footer, "reorder-modal");
}

export function openReorder(page: Page, orderId: string, orderNo: string): void {
  const state: ReorderState = { orderId, preview: null, loading: true, busy: false, error: "", chosen: new Set(), mayCreate: page.session.can("create_orders"), key: crypto.randomUUID() };
  reorderStates.set(page, state);
  page.openModal("reorder-modal", () => reorderModal(state));
  void (async () => {
    const answer = await reorderPreview(page, orderId);
    const held = reorderStates.get(page);
    if (held !== state) return;
    state.loading = false;
    if (answer.kind === "ready") {
      state.preview = answer.data;
      // The whole order starts chosen: a reorder in two taps.
      state.chosen = new Set(answer.data.lines.filter((line) => line.available === true).map((line) => line.line_no));
    } else {
      state.error = "The reorder could not be priced. Try again in a moment.";
    }
    page.touch();
  })();
}

export function reorderCommand(action: string, control: Element, page: Page): boolean {
  const state = reorderStates.get(page);
  if (state === null || state === undefined) return false;
  if (action === "toggle-reorder-line") {
    const lineNo = Number(control.getAttribute("data-line"));
    if (Number.isFinite(lineNo)) {
      if (state.chosen.has(lineNo)) state.chosen.delete(lineNo);
      else state.chosen.add(lineNo);
      // The lines the key covers changed: the next press creates under a fresh key.
      state.key = crypto.randomUUID();
      page.touch();
    }
    return true;
  }
  if (action === "create-reorder") {
    void createDraft(page);
    return true;
  }
  return false;
}

async function createDraft(page: Page): Promise<void> {
  const state = reorderStates.get(page);
  if (state === undefined || state.busy || state.chosen.size === 0) return;
  state.busy = true;
  state.error = "";
  page.touch();
  const answer = await reorderCreate(page, state.orderId, [...state.chosen], state.key);
  state.busy = false;
  if (answer.kind === "ready") {
    reorderStates.delete(page);
    page.closeModal();
    // Navigate in place: the board reloads and the draft stands in Plan.
    page.refresh();
    page.say("ok", `Draft order ${answer.data.order_no} created.`);
  } else if (answer.kind === "refusal") {
    state.mayCreate = false;
    page.touch();
  } else {
    // A create the transport lost may stand on the core's side: the words send
    // the person to look, they never promise nothing changed.
    state.error = await actionFailure(page, answer, true);
    page.touch();
  }
}
