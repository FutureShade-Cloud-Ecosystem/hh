// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-guard.ts
// (A-20 Check Quote); adapted for HH.
//
// Check Quote: a price and a cost per unit checked against the margin policy
// through the client's checkPolicy door. The server owns the arithmetic and
// the verdict; this form only reads two typed amounts to integer cents
// (through the decimal string, never a float), sends them with the branch and
// shows what came back, in percent. A not allowed answer opens the floor
// refusal dialog, which for this form is a check only: the form is tied to no
// quote, so the dialog files nothing and says so. A fault is a role=alert, a
// verdict (even a blocked one) is a role=status.
//
// The templates carry no property or event bindings: the panel base delegates
// by data-action and data-field, and the inputs are uncontrolled. The check
// button is not disabled while a check is in flight (a disabled button drops
// focus, and the refusal dialog gives focus back to it when it closes); a
// second press while one is in flight is ignored.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { openGuardRefusal } from "./floor-refusal-dialog.js";
import { blockFromText, dollarsToCents, guardVerdictWords } from "./_guard-format.js";
import { MARGIN_POLICY } from "./_approval-copy.js";
import { spokenRefusal } from "./_quote-workarea.js";

const DEFAULT_PRICE = "100.00";
const DEFAULT_COST = "94.00";

@customElement("qd-quote-guard")
export class QdQuoteGuard extends PanelBase {
  /** The branch's name for the label, from the one branch this login reaches. */
  @state() private branchName = "";
  /** The sentence in the result banner, and whether it is a verdict (status) or a fault (alert). */
  @state() private status = "";
  @state() private blocked = false;
  @state() private faulted = false;

  static styles = [
    panelStyles,
    css`
      /* The three fields share the card's width instead of a 22rem column on its left. */
      .fields {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(min(14rem, 100%), 1fr));
        gap: var(--hh-s-12);
        align-items: end;
      }
      .fields label {
        margin-top: 0;
      }
      .field {
        position: relative;
      }
      .field input {
        box-sizing: border-box;
        width: 100%;
        padding: 6px 8px 6px 22px;
      }
      .cur {
        position: absolute;
        left: 8px;
        top: 50%;
        transform: translateY(-50%);
        color: var(--hos-text-dim, var(--hh-text-dim));
        pointer-events: none;
      }
      .branch {
        box-sizing: border-box;
        padding: 6px 8px;
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: var(--hos-radius-control, var(--hh-radius-control));
        background: var(--hos-deep-earth, var(--hh-page));
      }
      .result {
        margin-top: 12px;
        padding: 8px 12px;
        border-radius: var(--hos-radius-control, var(--hh-radius-control));
        border: 1px solid var(--hos-success, var(--hh-success));
        color: var(--hos-success, var(--hh-success));
        max-width: 22rem;
      }
      .result.block {
        border-color: var(--hos-safety-red, var(--hh-danger));
        color: var(--hos-safety-red, var(--hh-danger));
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("branchId") && changed.get("branchId") !== undefined) {
      // A verdict is for the branch it was asked about: another branch's quote starts with none.
      this.status = "";
      this.blocked = false;
      this.faulted = false;
      this.busy = false;
    }
    if (changed.has("client") || changed.has("branchId")) this.reload();
  }

  protected override firstUpdated(): void {
    this.syncValue('[data-field="price"]', DEFAULT_PRICE);
    this.syncValue('[data-field="cost"]', DEFAULT_COST);
  }

  reload(): void {
    void this.loadBranch();
  }

  private async loadBranch(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const wanted = this.branchId;
    const result = await client.resolveBranch();
    if (wanted !== this.branchId) return; // the branch changed while this was out: the newer read names it
    this.branchName = result.ok && (wanted === "" || result.value.id === wanted) ? result.value.name : "";
  }

  protected override onClickAction(action: string): void {
    if (action === "check") void this.check();
  }

  protected override onFieldKey(field: string, _control: HTMLElement, event: KeyboardEvent): void {
    if ((field === "price" || field === "cost") && event.key === "Enter") void this.check();
  }

  private amount(field: string): number | null {
    const input = this.renderRoot.querySelector<HTMLInputElement>(`[data-field="${field}"]`);
    return dollarsToCents(input?.value ?? "");
  }

  private async check(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    const price = this.amount("price");
    const cost = this.amount("cost");
    if (price === null || cost === null) {
      this.blocked = true;
      this.faulted = true;
      this.status = "Enter the price and the cost as amounts, like 100.00.";
      return;
    }
    if (this.branchId === "") {
      this.blocked = true;
      this.faulted = true;
      this.status = "Open a quote first: the check runs against that quote's branch.";
      return;
    }
    this.busy = true;
    const asked = this.branchId;
    const result = await client.checkPolicy(price, cost, asked);
    // The verdict is for the branch it was asked about; if the quote moved to another branch meanwhile it says nothing.
    if (asked !== this.branchId) return;
    this.busy = false;
    if (result.ok) {
      this.faulted = false;
      this.blocked = !result.value.allowed;
      this.status = guardVerdictWords(result.value);
      if (!result.value.allowed) openGuardRefusal(MARGIN_POLICY, result.value.margin_bps, "");
      return;
    }
    // The contract also lets the core refuse a below floor check with a 403 that carries the action block.
    const block = result.status === 403 ? blockFromText(result.error) : null;
    if (block !== null) {
      this.faulted = false;
      this.blocked = true;
      this.status = `Blocked. ${block.detail}`;
      openGuardRefusal(block.policyType, null, block.detail);
      return;
    }
    this.blocked = true;
    this.faulted = true;
    this.status = spokenRefusal(result.status === 401 ? "Not signed in. Log in to run a policy check." : result.error) || "The policy check failed.";
  }

  override render() {
    return html`
      <div class="box" data-testid="qg-box">
        <h3>Check Quote</h3>
        <div class="sub">Check a price against the margin policy. The server answers.</div>
        <div class="fields">
          <div>
            <label>Branch</label>
            <div class="branch" data-testid="qg-branch">${this.branchName !== "" ? this.branchName : "This quote's branch"}</div>
          </div>
          <div>
            <label for="qg-price">Selling price / unit</label>
            <div class="field"><span class="cur" aria-hidden="true">$</span><input id="qg-price" data-field="price" type="text" inputmode="decimal" data-testid="qg-price" /></div>
          </div>
          <div>
            <label for="qg-cost">Cost / unit</label>
            <div class="field"><span class="cur" aria-hidden="true">$</span><input id="qg-cost" data-field="cost" type="text" inputmode="decimal" data-testid="qg-cost" /></div>
          </div>
        </div>
        <div class="row">
          <button type="button" class="primary" data-action="check" aria-busy=${this.busy ? "true" : "false"} data-testid="qg-check">Check quote</button>
        </div>
        ${this.status === ""
          ? nothing
          : this.faulted
            ? html`<div class="result block" role="alert" data-testid="qg-result">${this.status}</div>`
            : html`<div class="result ${this.blocked ? "block" : "ok"}" role="status" data-testid="qg-result">${this.status}</div>`}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-guard": QdQuoteGuard;
  }
}
