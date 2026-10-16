// The Settings tab's screens, as plain render functions of a model. They hold no state and own no events. A manager
// reads every rule it may and the boxes are held for it, with the words naming who may change them; an administrator's
// boxes take what the server last said (the desk fills them after the paint, the screens bind no value).
import { html, nothing, type TemplateResult } from "lit";
import { relativeStamp } from "./office-logic.js";
import { policyFieldWord, policyValueWord } from "./office-logic.js";
import type { Loaded, SettingsModel } from "./office-settings.js";

const WHO = "Changing this is for the Financial Controller or the General Manager. Payables and Receivables can read it.";

function state<T>(l: Loaded<T>, what: string): TemplateResult | null {
  if (l.phase === "none" || l.phase === "loading") return html`<p class="dim" role="status" aria-busy="true">Loading ${what}.</p><div class="ghost"></div>`;
  if (l.phase === "error") return html`<p class="alert alert-error" role="alert" data-error>${l.words}</p>`;
  if (l.phase === "locked") return html`<p class="dim" data-locked>${WHO}</p>`;
  return null;
}

function toggle(sm: SettingsModel, label: string, choice: string, on: boolean, note: string): TemplateResult {
  return html`
    <div class="check-row">
      <button type="button" class="pick ${sm.canAdmin ? "" : "held"}" role="checkbox" aria-checked="${on ? "true" : "false"}" aria-label="${label}" data-action="set-toggle" data-choice="${choice}"></button>
      <span><strong>${label}</strong><span class="dim small">${note}</span></span>
    </div>
  `;
}

function numberField(sm: SettingsModel, label: string, name: string, note: string): TemplateResult {
  return html`<label>${label}<input type="text" inputmode="numeric" data-field="${name}" autocomplete="off" ?disabled=${!sm.canAdmin} /><span class="dim small">${note}</span></label>`;
}

function saveRow(sm: SettingsModel, action: string, word: string): TemplateResult {
  return html`
    <div class="actions">
      <button type="button" class="btn btn-primary ${sm.canAdmin ? "" : "held"}" data-action="${action}" aria-disabled="${sm.canAdmin ? "false" : "true"}" ?disabled=${sm.busy}>${word}</button>
      ${sm.canAdmin ? nothing : html`<span class="dim small" data-held-words>${WHO}</span>`}
    </div>
  `;
}

function rulesPanel(sm: SettingsModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Receivables rules" data-settings-rules>
      <div class="panel-head"><h3>Receivables rules</h3></div>
      <p class="dim small">Where an account moves from overdue to late to escalation, how often it is reminded, and when it is flagged for the paper path.</p>
      ${state(sm.landing, "the rules") ?? html`
        <form class="held-form" aria-label="Receivables rules">
          <div class="two">
            ${numberField(sm, "Overdue warning from (days)", "set-warn", "Past due this long an account shows as overdue.")}
            ${numberField(sm, "Escalation from (days)", "set-escalate", "At this age an account is escalated.")}
          </div>
          <div class="two">
            ${numberField(sm, "Days between reminders", "set-cadence", "The gap before an account can be reminded again.")}
            ${numberField(sm, "Reminders per cycle", "set-max", "No more than this many in one cycle.")}
          </div>
          <div class="two">
            ${numberField(sm, "Paper path window (days)", "set-window", "How far back reminders are counted.")}
            ${numberField(sm, "Reminders before the paper path flag", "set-minsends", "Sent and not opened, then flagged.")}
          </div>
          <div class="two">
            <label>With no AP contact tagged
              <select data-field="set-fallback" ?disabled=${!sm.canAdmin}>
                <option value="CONFIRM">Ask to confirm the account's address</option>
                <option value="REFUSE">Refuse to send</option>
              </select>
            </label>
            <label>Plan
              <select data-field="set-tier" ?disabled=${!sm.canAdmin}>
                <option value="BASE">Base</option>
                <option value="PRO">Accelerated AR</option>
              </select>
            </label>
          </div>
          ${toggle(sm, "Email reminders", "email", sm.toggles.email, "Off, and no email reminder is sent.")}
          ${toggle(sm, "Text reminders", "sms", sm.toggles.sms, "Off, and no text reminder is sent.")}
          ${saveRow(sm, "set-save-landing", "Save the rules")}
        </form>
      `}
    </section>
  `;
}

const TONES = [
  { id: "FRIENDLY", word: "Friendly" },
  { id: "FIRM", word: "Firm" },
  { id: "FINAL", word: "Final" },
];

function registerPanel(sm: SettingsModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Register settings" data-settings-register>
      <div class="panel-head"><h3>Register settings</h3></div>
      <p class="dim small">What the register shows and which reminder tones it offers.</p>
      ${state(sm.register, "the register settings") ?? html`
        <form class="held-form" aria-label="Register settings">
          <label>Who sees an account's position
            <select data-field="set-visibility" ?disabled=${!sm.canAdmin}>
              <option value="MANAGER_UP">Managers and above</option>
              <option value="ALL_STAFF">All staff</option>
              <option value="HIDDEN">Nobody</option>
            </select>
          </label>
          <div class="two">
            ${numberField(sm, "Due soon window (days)", "set-duesoon", "An invoice due within this many days reads due soon.")}
            ${numberField(sm, "Reminder cooldown (hours)", "set-cooldown", "The gap before the same invoice is chased again.")}
          </div>
          <div><div class="label" id="tones-label">Reminder tones offered</div>
            <div class="seg" role="group" aria-labelledby="tones-label">
              ${TONES.map((t) => html`<button type="button" class="seg-item" data-action="set-tone" data-choice="${t.id}" aria-pressed="${sm.tones.includes(t.id) ? "true" : "false"}">${t.word}</button>`)}
            </div>
          </div>
          ${toggle(sm, "Show credits by default", "showCredits", sm.toggles.showCredits, "Credit memos appear in the register without asking.")}
          ${saveRow(sm, "set-save-register", "Save the register settings")}
        </form>
      `}
    </section>
  `;
}

function nsfPanel(sm: SettingsModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Returned payment fee" data-settings-nsf>
      <div class="panel-head"><h3>Returned payment fee</h3></div>
      <p class="dim small">What a payment marked bounced is charged, and whether it holds the account.</p>
      ${state(sm.nsf, "the fee") ?? html`
        <form class="held-form" aria-label="Returned payment fee">
          <div class="two">
            <label>Fee (dollars)<input type="text" inputmode="decimal" class="amount" data-field="set-fee" autocomplete="off" ?disabled=${!sm.canAdmin} /></label>
            <label>Ledger account code<input type="text" data-field="set-code" autocomplete="off" ?disabled=${!sm.canAdmin} /></label>
          </div>
          <label>Notes<input type="text" data-field="set-notes" autocomplete="off" ?disabled=${!sm.canAdmin} /></label>
          ${toggle(sm, "The fee is taxable", "taxable", sm.toggles.taxable, "Sales tax is charged on the fee.")}
          ${toggle(sm, "A returned payment holds the account", "nsfHold", sm.toggles.nsfHold, "The account is held until the controller lifts it.")}
          ${saveRow(sm, "set-save-nsf", "Save the fee")}
        </form>
      `}
    </section>
  `;
}

function holdPanel(sm: SettingsModel): TemplateResult {
  const a = sm.autoHold;
  return html`
    <section class="panel" aria-label="Automatic credit holds" data-settings-hold>
      <div class="panel-head"><h3>Automatic credit holds</h3>${a.phase === "ready" ? html`<span class="pill pill-${a.value.auto_hold_enabled ? "good" : "plain"}" data-hold-state>${a.value.auto_hold_enabled ? "On" : "Off"}</span>` : nothing}</div>
      ${state(a, "the switch") ?? (a.phase === "ready"
        ? html`
            ${sm.holdLines.map((l) => html`<p data-hold-line>${l}</p>`)}
            ${a.value.switch_wired
              ? sm.confirmingHold === null
                ? html`<div class="actions"><button type="button" class="btn btn-secondary" data-action="set-hold-start" data-choice="${a.value.auto_hold_enabled ? "off" : "on"}">${a.value.auto_hold_enabled ? "Turn automatic holds off" : "Turn automatic holds on"}</button></div>`
                : html`
                    <form class="held-form" aria-label="Confirm the switch">
                      <p class="alert alert-warn" data-hold-confirm>${sm.confirmingHold ? "Turning this on lets the sweep stop accounts from transacting by itself." : "Turning this off stops the sweep placing holds by itself."} Say why: the reason goes on the record.</p>
                      <label>Reason<input type="text" data-field="set-hold-reason" autocomplete="off" maxlength="200" /></label>
                      <div class="actions">
                        <button type="button" class="btn btn-primary" data-action="set-hold-confirm" ?disabled=${sm.busy}>${sm.confirmingHold ? "Turn on" : "Turn off"}</button>
                        <button type="button" class="btn btn-secondary" data-action="set-hold-cancel">Cancel</button>
                      </div>
                    </form>
                  `
              : nothing}
          `
        : nothing)}
    </section>
  `;
}

function historyPanel(sm: SettingsModel, nowMs: number): TemplateResult {
  const h = sm.history;
  return html`
    <section class="panel" aria-label="Rule changes" data-settings-history>
      <div class="panel-head"><h3>Rule changes</h3></div>
      ${state(h, "the changes") ?? (h.phase === "ready" && h.value.length === 0
        ? html`<p class="dim" data-history-none>No rule has been changed yet.</p>`
        : h.phase === "ready"
          ? html`
              <div class="scroll">
                <table class="stack chg">
                  <thead><tr><th>Rule</th><th>From</th><th>To</th><th>By</th><th>When</th></tr></thead>
                  <tbody>
                    ${h.value.map(
                      (c) => html`<tr data-change-row><td>${policyFieldWord(c.field)}</td><td>${policyValueWord(c.field, c.before)}</td><td>${policyValueWord(c.field, c.after)}</td><td>${c.who ?? "Somebody"}</td><td class="nowrap">${c.when === undefined ? "" : relativeStamp(c.when, nowMs)}</td></tr>`,
                    )}
                  </tbody>
                </table>
              </div>
            `
          : nothing)}
    </section>
  `;
}

export function renderSettingsPanels(sm: SettingsModel, nowMs: number): TemplateResult {
  return html`
    <div class="settings-grid">
      <div>${rulesPanel(sm)}${registerPanel(sm)}</div>
      <div>${nsfPanel(sm)}${holdPanel(sm)}${historyPanel(sm, nowMs)}</div>
    </div>
  `;
}
