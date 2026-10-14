// The Assist surface's screens: the month's spend (every active person, zeros
// included), the budgets by role, and the assistant's settings. Pure template
// functions over plain model objects; AdminWork owns every call and every
// state change.
//
// Port provenance: the three panels, their order, and the words are the
// original app's assist-admin.ts (repository hardscapeos_dibbits, read only;
// see PROVENANCE.md). The meter's width is set in code through a data-
// attribute (the design system's position gate binds no style), and the
// budget sentence's em dashes are commas (the house dash rule).
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings, bound values only on data- and aria-
// attributes. Controls ride data-action names and AdminWork acts on them; a
// checkbox's checked state and the meter widths are mirrored in code, the way
// the register's role selects are.
import { html, nothing, type TemplateResult } from "lit";
import type { AssistLimitsView, AssistSettingsView } from "./admin-api.js";
import {
  budgetSentence,
  formatTokens,
  overBudget,
  spendSummary,
  spendTotal,
  usedFraction,
  type AssistSpendRow,
} from "./admin-logic.js";

/** Which row is being edited: a person's override or a role's default. */
export type AssistEditTarget = { readonly kind: "user"; readonly id: string } | { readonly kind: "role"; readonly code: string };

export interface AssistModel {
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly error: string;
  readonly report: { readonly month: string; readonly users: readonly AssistSpendRow[] } | null;
  readonly limits: AssistLimitsView | null;
  readonly settings: AssistSettingsView | null;
  /** The row mid edit, if any. */
  readonly editing: AssistEditTarget | null;
  /** A refused or failed write's words, beside the panel it belongs to. */
  readonly editError: string;
  /** The saved notice, until the next load. */
  readonly notice: string;
  /** A write in flight. */
  readonly busy: boolean;
}

/** A person's aria words for the meter, one whole string so the binding stays whole. */
function meterAriaWords(displayName: string): string {
  return `Allowance used by ${displayName}`;
}

function budgetAriaWords(who: string): string {
  return `Monthly token budget for ${who}`;
}

export function renderAssist(model: AssistModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Assist</h1>
      <p class="page-sub">The assistant's month: who is using it, what it costs, and what they may spend.</p>
    </header>
    ${model.phase === "loading"
      ? html`<p class="notice-line" data-test="assist-loading">Loading the assistant's month…</p>`
      : nothing}
    ${model.phase === "error" || model.phase === "refused"
      ? html`
          <p class="error-line" data-test="assist-error">${model.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-assist">Try again</button></div>
        `
      : nothing}
    ${model.phase === "ready" ? renderReady(model) : nothing}
  `;
}

function renderReady(model: AssistModel): TemplateResult {
  return html`
    ${model.notice === "" ? nothing : html`<p class="notice-line" data-test="assist-notice">${model.notice}</p>`}
    ${model.editError === "" ? nothing : html`<p class="error-line" data-test="assist-edit-error">${model.editError}</p>`}
    ${renderSpend(model)}
    ${renderRoleLimits(model)}
    ${renderSettings(model.settings)}
  `;
}

// -- the month's spend -------------------------------------------------------------------------

function renderSpend(model: AssistModel): TemplateResult {
  const report = model.report;
  if (report === null) {
    return html`
      <section class="card"><p class="notice-line">The month's spend did not load.</p></section>
    `;
  }
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">AI usage this month</h2>
        <span class="chip mono" data-test="assist-month">${report.month}</span>
      </div>
      <p class="card-sub">Every active person, biggest spender first. A zero is a fact, not a gap.</p>
      ${report.users.length === 0
        ? html`<p class="notice-line" data-test="assist-empty">
            The report lists no one. Every active person appears here, zeros included, and there is no one to list yet.
          </p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Person</th>
                    <th scope="col">Role</th>
                    <th scope="col" class="num">Questions</th>
                    <th scope="col" class="num">Tokens</th>
                    <th scope="col">Allowance used</th>
                    <th scope="col">Budget</th>
                  </tr>
                </thead>
                <tbody>
                  ${report.users.map((row) => renderSpendRow(row, model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderSpendRow(row: AssistSpendRow, model: AssistModel): TemplateResult {
  const total = spendTotal(row);
  const pct = Math.round(usedFraction(total, row.budget.tokens) * 100);
  const editing = model.editing?.kind === "user" && model.editing.id === row.user_id;
  return html`
    <tr data-user=${row.user_id}>
      <td>
        <div class="person">${row.display_name}</div>
        <div class="mono dim">${row.email}</div>
      </td>
      <td><span class="chip mono">${row.role_code === "" ? "no role" : row.role_code}</span></td>
      <td class="num">${formatTokens(row.exchanges)}</td>
      <td class="num">${formatTokens(total)}</td>
      <td>
        <div class="meter">
          <div
            class="meter-track"
            role="progressbar"
            aria-label=${meterAriaWords(row.display_name)}
            aria-valuemin="0"
            aria-valuemax="100"
            aria-valuenow=${pct}
          >
            <div class="meter-fill ${overBudget(total, row.budget.tokens) ? "meter-spent" : ""}" data-meter=${row.user_id}></div>
          </div>
          <span class="dim">${spendSummary(row)}</span>
        </div>
      </td>
      <td>
        ${editing
          ? html`
              <div class="edit-row">
                <input
                  class="text-field budget-field"
                  type="number"
                  min="0"
                  step="1000"
                  data-field="assist-budget"
                  aria-label=${budgetAriaWords(row.display_name)}
                />
                <button
                  type="button"
                  class="btn btn-primary btn-small"
                  data-action="assist-save"
                  data-budget-kind="user"
                  data-budget-id=${row.user_id}
                  ?disabled=${model.busy}
                >
                  Save
                </button>
                <button type="button" class="btn btn-small" data-action="assist-cancel-edit" ?disabled=${model.busy}>Cancel</button>
              </div>
            `
          : html`
              <div class="edit-row">
                <span class="dim">${budgetSentence(row.budget, row.role_code)}</span>
                <button
                  type="button"
                  class="btn btn-secondary btn-small"
                  data-action="assist-edit"
                  data-budget-kind="user"
                  data-budget-id=${row.user_id}
                >
                  Change
                </button>
                ${row.budget.source === "user"
                  ? html`<button
                      type="button"
                      class="btn btn-small"
                      data-action="assist-reset-limit"
                      data-user-id=${row.user_id}
                      ?disabled=${model.busy}
                      title="Remove this person's override and use their role's budget"
                    >
                      Reset to role
                    </button>`
                  : nothing}
              </div>
            `}
      </td>
    </tr>
  `;
}

// -- the budgets by role -------------------------------------------------------------------------

function renderRoleLimits(model: AssistModel): TemplateResult {
  const limits = model.limits;
  if (limits === null) {
    return html`<section class="card"><p class="notice-line">The role budgets did not load.</p></section>`;
  }
  const codes = Object.keys(limits.role_budgets).sort((a, b) => a.localeCompare(b));
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Monthly budgets by role</h2>
      </div>
      <p class="card-sub">
        A role with no row here gets the ${formatTokens(limits.default_budget)} token default.
      </p>
      ${codes.length === 0
        ? html`<p class="notice-line" data-test="assist-roles-empty">No role budgets are configured.</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Role</th>
                    <th scope="col">Monthly tokens</th>
                  </tr>
                </thead>
                <tbody>
                  ${codes.map((code) => renderRoleLimitRow(code, limits.role_budgets[code], model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderRoleLimitRow(code: string, tokens: number, model: AssistModel): TemplateResult {
  const editing = model.editing?.kind === "role" && model.editing.code === code;
  return html`
    <tr data-role=${code}>
      <td><span class="chip mono">${code}</span></td>
      <td>
        ${editing
          ? html`
              <div class="edit-row">
                <input
                  class="text-field budget-field"
                  type="number"
                  min="0"
                  step="1000"
                  data-field="assist-budget"
                  aria-label=${budgetAriaWords(code)}
                />
                <button
                  type="button"
                  class="btn btn-primary btn-small"
                  data-action="assist-save"
                  data-budget-kind="role"
                  data-budget-id=${code}
                  ?disabled=${model.busy}
                >
                  Save
                </button>
                <button type="button" class="btn btn-small" data-action="assist-cancel-edit" ?disabled=${model.busy}>Cancel</button>
              </div>
            `
          : html`
              <div class="edit-row">
                <span class="dim">${formatTokens(tokens)}</span>
                <button type="button" class="btn btn-secondary btn-small" data-action="assist-edit" data-budget-kind="role" data-budget-id=${code}>
                  Change
                </button>
              </div>
            `}
      </td>
    </tr>
  `;
}

// -- the settings ---------------------------------------------------------------------------------

// The two settings toggles, unrolled rather than mapped: their ids and label
// pairs are a fixed closed set, and the position gate takes bound id and for
// attributes off the table, so each stands as its own static block.
function renderSettings(settings: AssistSettingsView | null): TemplateResult {
  if (settings === null) {
    return html`<section class="card"><p class="notice-line">The settings did not load.</p></section>`;
  }
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Assistant settings</h2>
      </div>
      <p class="card-sub">Who can see what, and which provider answers.</p>
      <div class="toggle">
        <input id="assist-toggle-spend_visible_to_non_admin" type="checkbox" data-action="assist-setting" data-key="spend_visible_to_non_admin" />
        <div class="toggle-text">
          <label class="toggle-name" for="assist-toggle-spend_visible_to_non_admin">Let people see their own AI usage</label>
          <span class="toggle-why">
            When this is off, a person still sees their monthly allowance, because they need it to understand a refusal,
            but not what they have spent. Leadership always sees both.
          </span>
        </div>
      </div>
      <div class="toggle">
        <input id="assist-toggle-chat_enabled" type="checkbox" data-action="assist-setting" data-key="chat_enabled" />
        <div class="toggle-text">
          <label class="toggle-name" for="assist-toggle-chat_enabled">Chat with the assistant is available</label>
          <span class="toggle-why">
            Switching this off leaves the lookup buttons working for everyone. They cost nothing and never call a
            provider.
          </span>
        </div>
      </div>
      <dl class="kv">
        <dt>Provider</dt>
        <dd class="mono" data-test="assist-settings-provider">${settings.provider}</dd>
        <dt>Model</dt>
        <dd class="mono" data-test="assist-settings-model">${settings.model}</dd>
        <dt>API key variable</dt>
        <dd class="mono" data-test="assist-settings-keyvar">${settings.key_env_var}</dd>
        <dt>Key found at boot</dt>
        <dd class="mono" data-test="assist-settings-keypresent">${settings.key_present ? "yes" : "no"}</dd>
      </dl>
      <p class="card-sub">
        The key itself is read from the environment at start-up and is never stored, shown or logged. With no key
        present the assistant runs on the built-in deterministic provider, which costs nothing.
      </p>
    </section>
  `;
}
