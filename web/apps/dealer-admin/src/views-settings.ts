// The Deployment settings surface's screens: the dealer's brand, the card
// rail's configuration, the counter's policy with its history, and the
// walkthrough's policy and step copy. Four sections, one surface; each
// section owns its own states. Pure template functions over plain model
// objects; app-root owns every call and every state change.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings. Controls ride data-action names and
// the host's listeners act on them; selects answer their change event, text
// and number fields mirror through data-field and updated(), and checkboxes
// report their own toggled state on the click the host listener reads.
//
// THE VISUAL REFERENCES. The counter policy's knobs follow the original's
// own settings screen (pos-counter-settings.ts, the same knobs in the same
// words); the brand, the card rail and the walkthrough follow this app's own
// card style, because no original screen gathered them as one Settings
// surface. PROVENANCE.md carries the rows.
import { html, nothing, type TemplateResult } from "lit";
import type { PaymentsConfig, PolicyChange, TourView } from "./settings-api.js";
import {
  BRAND_WAITS_WORDS,
  TOUR_READONLY_WORDS,
  chipSetError,
  chipsOf,
  policyHistoryWho,
  restockingFeeError,
  workspaceSplitError,
} from "./settings-logic.js";

// -- the models --------------------------------------------------------------------------------------

export interface BrandSection {
  readonly phase: "loading" | "ready" | "waits" | "error";
  readonly displayName: string;
  readonly footerText: string;
  readonly primaryColor: string;
  readonly busy: boolean;
  readonly notice: string;
  readonly error: string;
}

export interface PaymentsSection {
  readonly phase: "loading" | "ready" | "error";
  readonly status: PaymentsConfig | null;
  readonly processor: string;
  readonly merchantId: string;
  readonly currency: string;
  readonly busy: boolean;
  readonly notice: string;
  readonly error: string;
}

export interface PolicySection {
  readonly phase: "loading" | "ready" | "refused" | "error";
  readonly words: string;
  readonly fee: number;
  readonly notes: string;
  readonly chips: string;
  readonly split: number;
  readonly cod: boolean;
  readonly quickChips: boolean;
  readonly linePresentation: string;
  readonly lookupPresentation: string;
  readonly arrangement: string;
  readonly marginVisibility: string;
  readonly balanceVisibility: string;
  readonly history: readonly PolicyChange[];
  readonly historyPhase: "loading" | "ready" | "refused" | "error";
  readonly historyWords: string;
  readonly busy: boolean;
  readonly notice: string;
  readonly error: string;
}

export interface TourSection {
  readonly phase: "loading" | "ready" | "error";
  readonly words: string;
  readonly view: TourView | null;
  readonly editChapter: string;
  readonly editStep: string;
  readonly editLine: string;
  readonly editRoute: string;
  readonly editAnchor: string;
  readonly busy: boolean;
  readonly notice: string;
  readonly error: string;
}

export interface SettingsModel {
  readonly brand: BrandSection;
  readonly payments: PaymentsSection;
  readonly policy: PolicySection;
  readonly tour: TourSection;
}

export function renderSettings(model: SettingsModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Deployment settings</h1>
      <p class="page-sub">The dealer's brand, the card rail, the counter's policy, and the walkthrough.</p>
    </header>
    ${renderBrand(model.brand)}
    ${renderPayments(model.payments)}
    ${renderPolicy(model.policy)}
    ${renderTour(model.tour)}
  `;
}

// -- the brand -----------------------------------------------------------------------------------------

function renderBrand(s: BrandSection): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Branding</h2>
        ${s.phase === "waits" ? html`<span class="chip chip-wait" data-test="brand-waits">Waits for the platform</span>` : nothing}
      </div>
      ${s.phase === "waits" ? html`<p class="card-sub" data-test="brand-waits-words">${BRAND_WAITS_WORDS}</p>` : nothing}
      ${s.phase === "error" ? html`<p class="error-line" data-test="brand-error">${s.error}</p>` : nothing}
      ${s.phase === "loading" ? html`<p class="notice-line" data-test="brand-loading">Reading the dealer's brand…</p>` : nothing}
      ${s.phase === "ready" || s.phase === "waits"
        ? html`
            ${s.notice === "" ? nothing : html`<p class="notice-line" data-test="brand-notice">${s.notice}</p>`}
            <div class="form-grid">
              <label class="field">
                <span class="field-label">Dealer name</span>
                <input class="text-field" type="text" data-field="brand-name" ?disabled=${s.phase === "waits" || s.busy} />
              </label>
              <label class="field">
                <span class="field-label">Mark colour (hex)</span>
                <input class="text-field" type="text" data-field="brand-color" ?disabled=${s.phase === "waits" || s.busy} />
              </label>
              <label class="field field-full">
                <span class="field-label">Receipt footer</span>
                <input class="text-field" type="text" data-field="brand-footer" ?disabled=${s.phase === "waits" || s.busy} />
              </label>
            </div>
            ${s.phase === "ready"
              ? html`
                  <div class="editor-actions">
                    <button type="button" class="btn btn-primary" data-action="settings-save-brand" ?disabled=${s.busy}>
                      ${s.busy ? "Saving…" : "Save the brand"}
                    </button>
                  </div>
                `
              : nothing}
          `
        : nothing}
    </section>
  `;
}

// -- the card rail --------------------------------------------------------------------------------------

function renderPayments(s: PaymentsSection): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Payments configuration</h2>
        ${s.status?.configured ? html`<span class="chip">Configured</span>` : html`<span class="chip chip-wait">Not configured</span>`}
        ${s.status?.simulated ? html`<span class="chip" data-test="payments-simulated">Test mode: the card rail is the sandbox</span>` : nothing}
      </div>
      <p class="card-sub">The card never touches this system: the till holds a token, and the credentials stay at the processor.</p>
      ${s.phase === "loading" ? html`<p class="notice-line" data-test="payments-loading">Reading the card rail's configuration…</p>` : nothing}
      ${s.phase === "error" ? html`<p class="error-line" data-test="payments-error">${s.error}</p>` : nothing}
      ${s.phase === "ready"
        ? html`
            ${s.notice === "" ? nothing : html`<p class="notice-line" data-test="payments-notice">${s.notice}</p>`}
            ${s.error === "" ? nothing : html`<p class="error-line" data-test="payments-save-error">${s.error}</p>`}
            <p class="card-sub">
              ${s.status !== null && s.status.processor !== ""
                ? html`The rail answers through <code class="mono">${s.status.processor}</code> for ${s.status.currency.toUpperCase()}.`
                : nothing}
            </p>
            <div class="form-grid">
              <label class="field">
                <span class="field-label">Processor</span>
                <input class="text-field" type="text" data-field="pay-processor" ?disabled=${s.busy} />
              </label>
              <label class="field">
                <span class="field-label">Merchant id</span>
                <input class="text-field" type="text" data-field="pay-merchant" ?disabled=${s.busy} />
              </label>
              <label class="field">
                <span class="field-label">Currency</span>
                <input class="text-field" type="text" data-field="pay-currency" ?disabled=${s.busy} />
              </label>
            </div>
            <div class="editor-actions">
              <button type="button" class="btn btn-primary" data-action="settings-save-payments" ?disabled=${s.busy}>
                ${s.busy ? "Saving…" : "Save the card rail"}
              </button>
            </div>
          `
        : nothing}
    </section>
  `;
}

// -- the counter's policy ---------------------------------------------------------------------------------

function renderPolicy(s: PolicySection): TemplateResult {
  const fault =
    s.fee < 0 || !Number.isInteger(s.fee)
      ? restockingFeeError(s.fee)
      : workspaceSplitError(s.split) !== ""
        ? workspaceSplitError(s.split)
        : chipSetError(chipsOf(s.chips));
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Counter policy</h2>
        <span class="count">the dealer's default; a branch can override it at the till</span>
      </div>
      ${s.phase === "loading" ? html`<p class="notice-line" data-test="policy-loading">Reading the counter's policy…</p>` : nothing}
      ${s.phase === "refused" || s.phase === "error"
        ? html`
            <p class="error-line" data-test="policy-error">${s.words}</p>
            <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-settings">Try again</button></div>
          `
        : nothing}
      ${s.phase === "ready"
        ? html`
            ${s.notice === "" ? nothing : html`<p class="notice-line" data-test="policy-notice">${s.notice}</p>`}
            ${s.error === "" ? nothing : html`<p class="error-line" data-test="policy-save-error">${s.error}</p>`}
            ${fault === "" ? nothing : html`<p class="error-line" data-test="policy-fault">${fault}</p>`}
            <div class="form-grid">
              <label class="branch-choice">
                <input type="checkbox" data-action="policy-flag" data-field="cod" ?disabled=${s.busy} />
                Cash on demand at the counter
              </label>
              <label class="branch-choice">
                <input type="checkbox" data-action="policy-flag" data-field="quickChips" ?disabled=${s.busy} />
                The quick chip row
              </label>
              <label class="field">
                <span class="field-label">Restocking fee (basis points)</span>
                <input class="text-field" type="number" data-field="policy-fee" ?disabled=${s.busy} />
              </label>
              <label class="field">
                <span class="field-label">Workspace split (20 to 80)</span>
                <input class="text-field" type="number" data-field="policy-split" ?disabled=${s.busy} />
              </label>
              <label class="field">
                <span class="field-label">Lines render as</span>
                <select class="select" data-action="policy-line" data-field="policy-line" ?disabled=${s.busy}>
                  <option value="LINES">Lines</option>
                  <option value="CARDS">Cards</option>
                </select>
              </label>
              <label class="field">
                <span class="field-label">Lookup results render as</span>
                <select class="select" data-action="policy-lookup" data-field="policy-lookup" ?disabled=${s.busy}>
                  <option value="CARDS">Cards</option>
                  <option value="ROWS">Rows</option>
                </select>
              </label>
              <label class="field field-full">
                <span class="field-label">The till's split, which side leads</span>
                <select class="select" data-action="policy-arrangement" data-field="policy-arrangement" ?disabled=${s.busy}>
                  <option value="LOOKUP_LEFT">Lookup on the left, the sale on the right</option>
                  <option value="SALE_LEFT">Sale on the left, the lookup on the right</option>
                </select>
              </label>
              <label class="field">
                <span class="field-label">Cost and margin, seen by</span>
                <select class="select" data-action="policy-margin" data-field="policy-margin" ?disabled=${s.busy}>
                  <option value="MANAGER_UP">Managers and above</option>
                  <option value="ALL_STAFF">Everyone</option>
                  <option value="HIDDEN">Hidden</option>
                </select>
              </label>
              <label class="field">
                <span class="field-label">Past due balance, seen by</span>
                <select class="select" data-action="policy-balance" data-field="policy-balance" ?disabled=${s.busy}>
                  <option value="ALL_STAFF">Everyone</option>
                  <option value="MANAGER_UP">Managers and above</option>
                  <option value="HIDDEN">Hidden</option>
                </select>
              </label>
              <label class="field field-full">
                <span class="field-label">Quick chips, one category per line (at most 8)</span>
                <textarea class="text-field" rows="3" data-field="policy-chips" ?disabled=${s.busy}></textarea>
              </label>
              <label class="field field-full">
                <span class="field-label">Notes</span>
                <input class="text-field" type="text" data-field="policy-notes" ?disabled=${s.busy} />
              </label>
            </div>
            <div class="editor-actions">
              <button type="button" class="btn btn-primary" data-action="settings-save-policy" ?disabled=${s.busy || fault !== ""}>
                ${s.busy ? "Saving…" : "Save the counter's policy"}
              </button>
            </div>
            ${renderPolicyHistory(s)}
          `
        : nothing}
    </section>
  `;
}

function renderPolicyHistory(s: PolicySection): TemplateResult {
  if (s.historyPhase === "refused" || s.historyPhase === "error") {
    return html`<p class="error-line" data-test="policy-history-error">${s.historyWords}</p>`;
  }
  if (s.historyPhase === "loading") {
    return html`<p class="notice-line" data-test="policy-history-loading">Reading the policy's history…</p>`;
  }
  return html`
    <div class="card-head">
      <h3 class="section-title">What changed, and who changed it</h3>
      <span class="count" data-test="policy-history-count">${s.history.length} changes</span>
    </div>
    ${s.history.length === 0
      ? html`<p class="notice-line" data-test="policy-history-empty">No change yet: the policy stands as it was seeded.</p>`
      : html`
          <div class="tablewrap">
            <table data-test="policy-history">
              <thead>
                <tr>
                  <th scope="col">Knob</th>
                  <th scope="col">From</th>
                  <th scope="col">To</th>
                  <th scope="col">By</th>
                  <th scope="col">When</th>
                </tr>
              </thead>
              <tbody>
                ${s.history.map(
                  (row) => html`
                    <tr data-test="policy-history-row">
                      <td><code class="mono">${row.field}</code></td>
                      <td class="mono">${row.before}</td>
                      <td class="mono">${row.after}</td>
                      <td><span class="chip">${policyHistoryWho(row.who)}</span></td>
                      <td class="dim">${row.when}</td>
                    </tr>
                  `,
                )}
              </tbody>
            </table>
          </div>
        `}
  `;
}

// -- the walkthrough ---------------------------------------------------------------------------------------

function renderTour(s: TourSection): TemplateResult {
  const chapters = s.view?.chapters ?? [];
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Guided tour</h2>
        ${s.view === null ? nothing : html`<span class="count">${chapters.length} chapters</span>`}
      </div>
      ${s.phase === "loading" ? html`<p class="notice-line" data-test="tour-loading">Reading the walkthrough…</p>` : nothing}
      ${s.phase === "error" ? html`<p class="error-line" data-test="tour-error">${s.words}</p>` : nothing}
      ${s.phase === "ready" && s.view !== null
        ? html`
            ${s.notice === "" ? nothing : html`<p class="notice-line" data-test="tour-notice">${s.notice}</p>`}
            ${s.error === "" ? nothing : html`<p class="error-line" data-test="tour-save-error">${s.error}</p>`}
            ${chapters.length === 0
              ? html`<p class="notice-line" data-test="tour-off">The walkthrough answers nothing here: the platform's demo switch or the dealer's own switch has it off.</p>`
              : html`
                  <ul class="role-list" data-test="tour-chapters">
                    ${chapters.map(
                      (c) => html`
                        <li class="role-row">
                          <span class="role-name">${c.title}</span>
                          <span class="chip">${c.steps.length} steps</span>
                          ${c.promise === "" ? nothing : html`<span class="dim">${c.promise}</span>`}
                        </li>
                      `,
                    )}
                  </ul>
                `}
            ${s.view.can_edit ? renderTourEditor(s, chapters.length > 0) : html`<p class="card-sub" data-test="tour-readonly">${TOUR_READONLY_WORDS}</p>`}
          `
        : nothing}
    </section>
  `;
}

function renderTourEditor(s: TourSection, live: boolean): TemplateResult {
  const chapter = s.view?.chapters.find((c) => c.chapter_key === s.editChapter);
  const steps = chapter?.steps ?? [];
  return html`
    <div class="card-head">
      <h3 class="section-title">The platform's switches</h3>
      <span class="count">the walkthrough's copy and its demo switch</span>
    </div>
    <div class="editor-actions">
      ${live
        ? html`<button type="button" class="btn btn-secondary" data-action="settings-tour-demo-off" ?disabled=${s.busy}>Turn the demo switch off</button>`
        : html`<button type="button" class="btn btn-secondary" data-action="settings-tour-demo-on" ?disabled=${s.busy}>Turn the demo switch on</button>`}
    </div>
    <div class="card-head">
      <h3 class="section-title">One step's copy</h3>
      <span class="count">a step says one line, at most 72 characters</span>
    </div>
    <div class="form-grid">
      <label class="field">
        <span class="field-label">Chapter</span>
        <select class="select" data-action="tour-chapter" data-field="tour-chapter" ?disabled=${s.busy}>
          ${(s.view?.chapters ?? []).map((c) => html`<option>${c.chapter_key}</option>`)}
        </select>
      </label>
      <label class="field">
        <span class="field-label">Step</span>
        <select class="select" data-action="tour-step" data-field="tour-step" ?disabled=${s.busy}>
          ${steps.map((st) => html`<option>${st.step_key}</option>`)}
        </select>
      </label>
      <label class="field field-full">
        <span class="field-label">What the step says</span>
        <input class="text-field" type="text" data-field="tour-line" ?disabled=${s.busy} />
      </label>
      <label class="field">
        <span class="field-label">Route, like #sales/pos (empty stays put)</span>
        <input class="text-field" type="text" data-field="tour-route" ?disabled=${s.busy} />
      </label>
      <label class="field">
        <span class="field-label">Anchor, like testid:pos-browse</span>
        <input class="text-field" type="text" data-field="tour-anchor" ?disabled=${s.busy} />
      </label>
    </div>
    <div class="editor-actions">
      <button type="button" class="btn btn-primary" data-action="settings-save-tour-step" ?disabled=${s.busy}>Save the step's copy</button>
    </div>
  `;
}
