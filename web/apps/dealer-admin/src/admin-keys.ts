// The Platform keys surface's screens: the platform's key roster with this
// dealer's overrides, read only. Pure template functions over plain model
// objects; AdminWork owns every call and every state change.
//
// There is no original screen for this surface (the route is an HH addition,
// the operator launcher's read), so the visual reference is this app's own
// established screens: the register's table on HH's tokens. The write is the
// operator's route and no dealer control exists here; the floor and the
// override render as the server's own data, as it came.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings, bound values only on data- and aria-
// attributes.
import { html, nothing, type TemplateResult } from "lit";
import type { PlatformKeyRow } from "./admin-api.js";
import { keysValueWords, keysWaitWords } from "./admin-logic.js";

export interface KeysModel {
  readonly phase: "loading" | "ready" | "error" | "waits";
  readonly error: string;
  readonly rows: readonly PlatformKeyRow[];
}

export function renderKeys(model: KeysModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Platform keys</h1>
      <p class="page-sub">The platform's own keys, and where this dealer overrides one. Read only: the keys themselves are set by the platform.</p>
    </header>
    ${model.phase === "loading"
      ? html`<p class="notice-line" data-test="keys-loading">Loading the platform's key roster…</p>`
      : nothing}
    ${model.phase === "waits"
      ? html`<section class="card"><p class="notice-line" data-test="keys-waits">${keysWaitWords()}</p></section>`
      : nothing}
    ${model.phase === "error"
      ? html`
          <p class="error-line" data-test="keys-error">${model.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-keys">Try again</button></div>
        `
      : nothing}
    ${model.phase === "ready" ? renderRoster(model.rows) : nothing}
  `;
}

function renderRoster(rows: readonly PlatformKeyRow[]): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">The roster</h2>
        <span class="count" data-test="keys-count">${rows.length} keys</span>
      </div>
      ${rows.length === 0
        ? html`<p class="notice-line" data-test="keys-empty">The roster holds no keys. The platform sets its own keys, and it has set none.</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Key</th>
                    <th scope="col">Platform floor</th>
                    <th scope="col">This dealer's override</th>
                    <th scope="col">Credential</th>
                    <th scope="col">Updated</th>
                  </tr>
                </thead>
                <tbody>
                  ${rows.map((row, i) => renderKeyRow(row, i))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderKeyRow(row: PlatformKeyRow, index: number): TemplateResult {
  return html`
    <tr data-test="keys-row-${index}">
      <td class="mono">${row.key}</td>
      <td class="mono">${keysValueWords(row.platform_floor)}</td>
      <td class="mono">${keysValueWords(row.dealer_override)}</td>
      <td>${row.credential_configured ? html`<span class="chip">configured</span>` : html`<span class="chip chip-wait">none</span>`}</td>
      <td class="mono dim">
        ${row.updated_at === ""
          ? "never"
          : html`${row.updated_at}${row.updated_by === "" ? nothing : html`, by ${row.updated_by}`}`}
      </td>
    </tr>
  `;
}
