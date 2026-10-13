// The views the surfaces share: the four states, the back header a drill-down
// carries, the middot meta line, the inline notice an action answers with, and
// the modal sheet. Markup only: every value rides in text or in a quoted
// class, role, data or aria attribute (the design system's position gate),
// and every action is a data-action name the app root's host delegation acts
// on, never a bound listener.
import { html, css, nothing, type TemplateResult } from "lit";
import { baseStyles, button } from "@hardscapehouse/design-system/src/primitives.js";
import type { ApiClient } from "@hardscapehouse/api-client";
import type { TenantSession } from "@hardscapehouse/pro-kit";
import type { SurfaceId } from "./registry.js";

/** Where in the app a person is: a surface and, for a drill-down, what is open. */
export interface Route {
  surface: SurfaceId;
  projectId?: string;
  productId?: string;
  quoteId?: string;
  /** The top surface a drill-down came from, so back returns there. */
  from?: SurfaceId;
}

/** What a surface's loader answers with. An error carries the status so the root can tell a lapsed session from a fault. */
export type SurfaceAnswer =
  | { kind: "ready"; data: unknown; heading?: string }
  | { kind: "empty" }
  | { kind: "refusal" }
  | { kind: "error"; status?: number };

/** The one object a surface's code sees: its route, the session and client, the fields the person typed, and the moves it may make. */
export interface Page {
  readonly session: TenantSession;
  readonly client: ApiClient;
  route: Route;
  /** Whether an action is in flight; modals read it for their working label. */
  busy: boolean;
  /** A field the person typed, by its data-field name. */
  field(name: string): HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement | null;
  goto(route: Route): void;
  /** Reload the current route's data. */
  refresh(): void;
  /** The inline notice an action answers with. */
  say(kind: "ok" | "alert", words: string): void;
  hold(busy: boolean): void;
  /** Re-render without changing anything; a modal's own state lives in its open closure. */
  touch(): void;
  openModal(testid: string, build: (page: Page) => TemplateResult): void;
  closeModal(): void;
}

export type LoadState =
  | { kind: "loading" }
  | { kind: "ready" }
  | { kind: "empty" }
  | { kind: "error" }
  | { kind: "refusal" };

export const viewStyles = css`
  .back {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-4);
    min-height: 44px;
    margin: 0 0 var(--hh-s-4);
    padding: 0 var(--hh-s-8);
    border: 0;
    border-radius: var(--hh-radius-control);
    background: transparent;
    color: var(--hh-text-dim);
    font: inherit;
    font-size: 0.875rem;
    cursor: pointer;
  }
  .back:hover { color: var(--hh-text); }
  .meta { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); margin: 0 0 var(--hh-s-12); color: var(--hh-text-dim); font-size: 0.875rem; }
  .meta .sep { color: var(--hh-line); }
  .notice { margin: 0 0 var(--hh-s-12); padding: var(--hh-s-8) var(--hh-s-12); border: 1px solid var(--hh-line); border-inline-start: 3px solid var(--hh-info); border-radius: var(--hh-radius-card); background: var(--hh-card); font-size: 0.875rem; }
  .notice.ok { border-inline-start-color: var(--hh-success); }
  .notice.alert { border-inline-start-color: var(--hh-danger); }
  .card {
    display: block;
    width: 100%;
    padding: var(--hh-s-12) var(--hh-s-16);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    color: var(--hh-text);
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .card:hover { border-color: var(--hh-control-line-hover, var(--hh-line)); }
  ul.cards { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--hh-s-8); }
  ul.cards > li { margin: 0; }
  .rowline { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--hh-s-8); }
  .rowline .grow { flex: 1; }
  .dim { color: var(--hh-text-dim); }
  .figure { font-weight: 600; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; margin: var(--hh-s-16) 0; }
  .overlay { position: fixed; inset: 0; z-index: var(--hh-z-dropdown, 10); display: grid; place-items: end center; background: var(--hh-ground); padding: var(--hh-s-16); }
  @media (min-width: 48rem) { .overlay { place-items: center; } }
  .sheet {
    width: 100%;
    max-width: 30rem;
    max-height: 80vh;
    overflow: auto;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    padding: var(--hh-s-20);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    color: var(--hh-text);
  }
  .sheet h2 { margin: 0; font-size: 1.125rem; }
  .sheet .words { margin: 0; color: var(--hh-text-dim); font-size: 0.875rem; }
  .sheet .error { margin: 0; color: var(--hh-danger); font-size: 0.875rem; }
  .sheet footer { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); justify-content: flex-end; align-items: center; }
  label.field { display: flex; flex-direction: column; gap: var(--hh-s-4); font-size: 0.75rem; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; color: var(--hh-text-dim); }
  .section { margin: var(--hh-s-20) 0 0; }
  .section > h2 { margin: 0 0 var(--hh-s-8); font-size: 0.9375rem; }
  .emptyline { margin: var(--hh-s-8) 0; color: var(--hh-text-dim); font-size: 0.875rem; }
`;

/** The state block a surface shows in place of its content, with the kit's words. */
export function stateBlock(state: LoadState, words: { empty: string; error?: string }, testid = "surface-state"): TemplateResult {
  switch (state.kind) {
    case "ready":
      return html``;
    case "empty":
      return html`<hh-pro-state data-state="empty" data-testid="${testid}">${words.empty}</hh-pro-state>`;
    case "error":
      return html`<hh-pro-state data-state="error" data-testid="${testid}">${words.error ?? "This did not load. Nothing was changed."}</hh-pro-state>`;
    case "refusal":
      return html`<hh-pro-state data-state="refusal" data-testid="${testid}"></hh-pro-state>`;
    default:
      return html`<hh-pro-state data-state="loading" data-testid="${testid}"></hh-pro-state>`;
  }
}

/** The back header a drill-down carries: where it came from, in one word. */
export function backHeader(from: string, action: string): TemplateResult {
  return html`<button type="button" class="back" data-action="${action}" data-testid="back">${from}</button>`;
}

/** One middot line of facts. Parts arrive ready to read; empty parts drop out. */
export function metaLine(parts: (string | TemplateResult | typeof nothing | null | undefined)[]): TemplateResult {
  const live = parts.filter((part) => part === nothing || (part !== null && part !== undefined && part !== ""));
  return html`<p class="meta">${live.map((part, i) => html`${i > 0 ? html`<span class="sep" aria-hidden="true">·</span>` : nothing}${part}`)}</p>`;
}

/** The inline notice an action answers with: a status line, or an alert on a refusal. */
export function noticeLine(notice: { kind: "ok" | "alert"; words: string } | null): TemplateResult | typeof nothing {
  if (notice === null) return nothing;
  const role = notice.kind === "ok" ? "status" : "alert";
  return html`<p class="notice ${notice.kind}" role="${role}" data-testid="notice">${notice.words}</p>`;
}

/**
 * The modal sheet: one overlay, one card, a heading, the body, and a footer
 * the caller fills. The overlay is the close control (tap beside the sheet);
 * Escape rides the app root's key listener.
 */
export function modalSheet(heading: string, body: TemplateResult, footer: TemplateResult, testid: string): TemplateResult {
  return html`<div class="overlay" data-action="dismiss-modal" data-testid="${testid}">
    <section class="sheet" role="dialog" aria-modal="true" aria-label="${heading}" data-testid="${testid}-sheet">
      <h2>${heading}</h2>
      ${body}
      <footer>${footer}</footer>
    </section>
  </div>`;
}

export const cancelButton = button({ label: "Cancel", kind: "secondary", action: "dismiss-modal" });

export interface ModalSpec {
  heading: string;
  body: TemplateResult;
  confirmLabel: string;
  confirmAction: string;
  confirmDisabled?: boolean;
  busy?: boolean;
  error?: string;
  testid: string;
  extraFooter?: TemplateResult;
}

/** The confirm shape most modals take: Cancel on the left, the action on the right. */
export function confirmModal(spec: ModalSpec): TemplateResult {
  return modalSheet(
    spec.heading,
    spec.body,
    html`${spec.extraFooter ?? nothing}${cancelButton}${button({
      label: spec.busy === true ? "Working" : spec.confirmLabel,
      action: spec.confirmAction,
      disabled: spec.confirmDisabled === true || spec.busy === true,
    })}`,
    spec.testid,
  );
}

export const sheetStyles = [baseStyles, viewStyles];
