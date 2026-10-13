// The small render pieces the liens and compliance screens share: a status pill that always carries its word, the
// error and notice strips, an empty state, a tab strip, the review banner, and the branch word on a row. They hold
// no state and own no events; every control is a plain element carrying a data-action name and the host's one click
// listener acts on it (the design system's position rules allow no @ or . binding in an app, and the only bound
// values are class, role, aria- and data- attributes).
import { html, nothing, type TemplateResult } from "lit";
import type { Branch } from "@hardscapehouse/api-client";
import type { BannerView, Tone } from "../compliance/compliance-logic.js";

export function pill(tone: Tone, word: string): TemplateResult {
  return html`<span class="li-status li-status-${tone}">${word}</span>`;
}

export function messages(error: string, notice: string): TemplateResult {
  return html`
    ${error !== "" ? html`<p class="li-alert li-alert-error" role="alert" data-error>${error}</p>` : nothing}
    ${notice !== "" ? html`<p class="li-alert li-alert-ok" role="status" data-notice>${notice}</p>` : nothing}
  `;
}

export function empty(kind: string, title: string, line: string): TemplateResult {
  return html`<div class="li-empty" data-empty=${kind}><strong>${title}</strong><span class="li-dim">${line}</span></div>`;
}

export function loading(words: string): TemplateResult {
  return html`<p class="li-dim" role="status" data-loading>${words}</p>`;
}

/** The whole area failed to load: the refusal in words, and a way to read it again. */
export function loadFailed(words: string, action: string): TemplateResult {
  return html`
    <p class="li-alert li-alert-error" role="alert" data-load-error>${words}</p>
    <button type="button" class="btn btn-secondary" data-action=${action}>Try again</button>
  `;
}

export interface TabSpec {
  readonly id: string;
  readonly label: string;
  readonly count?: number | null;
}

export function tabsNav(label: string, action: string, current: string, tabs: readonly TabSpec[]): TemplateResult {
  return html`
    <nav class="li-tabs" aria-label=${label}>
      ${tabs.map(
        (t) => html`<button type="button" class="li-tab" data-action=${action} data-tab=${t.id} data-tab-id=${t.id} aria-current="${current === t.id ? "true" : "false"}">${t.label}${t.count === undefined || t.count === null ? nothing : html`<span class="li-count">${t.count}</span>`}</button>`,
      )}
    </nav>
  `;
}

/** The shared review banner: the core's own words and state, never a banner the app wrote. */
export function bannerStrip(view: BannerView, state: string): TemplateResult {
  return html`<p class="li-banner li-banner-${view.tone}" data-banner data-state=${state}>${view.text}</p>`;
}

export function branchName(branches: readonly Branch[], id: string | undefined): string {
  if (id === undefined || id === "") return "";
  return branches.find((b) => b.id === id)?.name ?? "";
}
