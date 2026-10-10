// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-scope-rules.ts;
// adapted for HH.
//
// The scope rule table, listed and switchable. The rules are data, and the
// whole argument for that is that the yard can correct them: a rule set
// nobody can see is a hard coded rule set with extra steps. So every rule is
// listed, the switched-off ones included (an administrator cannot switch back
// on what they cannot see), and the one control the rules need is the switch.
// It deliberately does not edit the wording, the categories or the sort
// order: those are real edits with real blast radius. A read or a switch the
// server refuses shows the server's own sentence.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import type { ScopeRuleRow } from "./_panel-wire.js";
import { scopeRuleSummary, writeRefusalLine } from "./_quote-settings.js";

@customElement("qd-scope-rules")
export class QdScopeRules extends PanelBase {
  @state() private rules: ScopeRuleRow[] = [];
  @state() private loaded = false;
  @state() private pending = "";

  static styles = [
    panelStyles,
    css`
      th {
        font-size: var(--qp-caption);
        letter-spacing: 0.04em;
        text-transform: uppercase;
      }
      td {
        vertical-align: top;
      }
      tr[data-enabled="false"] td {
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .code {
        font-family: var(--hos-font-mono, var(--hh-font-mono));
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .apps {
        white-space: nowrap;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("client")) this.reload();
  }

  reload(): void {
    void this.load();
  }

  /**
   * Numbers the reads and the switches of the rules: a read's answer applies only while nothing newer has begun, and a
   * switch counts as newer when it answers, so a read begun before it never lays the old rows over it.
   */
  private generation = 0;

  private async load(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    this.error = "";
    const mine = ++this.generation;
    const result = await client.listScopeRules();
    if (mine !== this.generation) return;
    this.loaded = true;
    if (!result.ok) {
      this.rules = [];
      this.error = writeRefusalLine(result.status, result.error);
      return;
    }
    this.rules = result.value;
  }

  private async toggle(code: string): Promise<void> {
    const client = this.client;
    const rule = this.rules.find((r) => r.code === code);
    if (client === null || rule === undefined || this.pending !== "") return;
    this.pending = code;
    this.error = "";
    const result = await client.setScopeRule(code, !rule.enabled);
    this.generation += 1; // a read begun before this answer must not land over it
    this.pending = "";
    if (!result.ok) {
      this.error = writeRefusalLine(result.status, result.error);
      return;
    }
    // Re-render from the row the server returned, so a concurrent edit by
    // another administrator cannot leave this screen showing a state that was
    // never in the table.
    const saved = result.value;
    this.rules = this.rules.map((r) => (r.code === saved.code ? saved : r));
  }

  protected override onClickAction(action: string, control: HTMLElement): void {
    if (action === "toggle-rule") void this.toggle(control.getAttribute("data-code") ?? "");
  }

  override render() {
    if (!this.loaded) return html`<p class="dim">Loading the scope rules...</p>`;
    if (this.error !== "" && this.rules.length === 0) {
      return html`<p class="lock" role="status" data-testid="qr-refused">${this.error}</p>`;
    }
    if (this.rules.length === 0) {
      // An empty table is a legitimate configuration: the suggestion panel
      // stays silent for everyone.
      return html`<p class="dim">No scope rules are configured, so the quote builder makes no suggestions.</p>`;
    }
    const summary = scopeRuleSummary(this.rules);
    return html`
      <p class="dim flush-top">
        These rules decide what the quote builder suggests. They are data, not code: switch off any that are wrong for this yard.
      </p>
      <p class="dim" data-testid="qr-summary">${summary.line} A rule that is wrong for this yard can be switched off here; nothing else about the quote changes.</p>
      <table class="gap-s" data-testid="qr-table">
        <thead>
          <tr>
            <th scope="col">Suggestion</th>
            <th scope="col">When the quote has</th>
            <th scope="col">It should also have</th>
            <th scope="col">On</th>
          </tr>
        </thead>
        <tbody>
          ${this.rules.map(
            (r) => html`<tr data-enabled=${r.enabled ? "true" : "false"} data-rule=${r.code} data-testid="qr-row">
              <td>
                <div>${r.title}</div>
                <div class="code">${r.code}</div>
              </td>
              <td class="apps">${r.when_apps.join(" or ")}</td>
              <td class="apps">${r.expect_apps.join(" or ")}</td>
              <td>
                <button
                  type="button"
                  data-action="toggle-rule"
                  data-code=${r.code}
                  aria-pressed=${r.enabled ? "true" : "false"}
                  aria-label="Rule ${r.code}"
                  ?disabled=${this.pending === r.code}
                >
                  ${r.enabled ? "On" : "Off"}
                </button>
              </td>
            </tr>`,
          )}
        </tbody>
      </table>
      ${summary.anyOff ? html`<p class="dim">A switched-off rule stays listed: a rule you cannot see is a rule you cannot turn back on.</p>` : nothing}
      ${this.error !== "" ? html`<p class="lock" role="status" data-testid="qr-lock">${this.error}</p>` : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-scope-rules": QdScopeRules;
  }
}
