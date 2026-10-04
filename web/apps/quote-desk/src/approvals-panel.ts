// Ported from hardscapeos_dibbits master 87d7941b app/src/pending-approvals.ts
// (A-20 Pending Approvals) with the approve and refuse flow of
// return-approvals.ts; adapted for HH (CP0-1 answer B: the branch's Sales
// manager decides margin floor requests).
//
// The approvals list for the Sales manager. It lists what the server returns
// for this caller (listApprovals) filtered to the margin below minimum kind,
// each row with the server's own detail sentence verbatim (the line, the
// discount and, for a caller on the cost and margin list only, the margin
// against the floor), its status in words, and Approve and Refuse buttons on
// PENDING rows only. The app never decides who may decide: the contract
// carries no may_decide flag, so a pending row offers the controls, and when a
// decision answers 403 the server's refusal text is shown verbatim and the
// decide controls leave the list from then on for this sign in. Nothing is
// shown or hidden by role name. A decision updates the row and announces
// quote-changed so the sibling panels re-read.
//
// The templates carry no property or event bindings: the panel base delegates
// by data-action and data-field, and the reason input is uncontrolled.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import type { IdentityApproval } from "@hardscapehouse/api-client";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { approvalDetail, decisionWord, isDecidable, MARGIN_POLICY, policyLabel } from "./_approval-copy.js";

@customElement("qd-approvals")
export class QdApprovals extends PanelBase {
  @state() private items: IdentityApproval[] = [];
  @state() private loaded = false;
  /** A decision refusal keyed by the row that refused; the other rows stay as they were. */
  @state() private rowError: Record<string, string> = {};
  @state() private busyId = "";
  /** The server's 403 text, once a decision was refused: the decide controls leave the list for this sign in. */
  @state() private refusedText = "";
  private lastClient: unknown = null;

  static styles = [
    panelStyles,
    css`
      .head {
        display: flex;
        align-items: baseline;
        justify-content: space-between;
        gap: 8px;
      }
      .count {
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .queue {
        display: flex;
        flex-direction: column;
        gap: 10px;
        margin-top: 8px;
      }
      .req {
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: var(--hos-radius-card, var(--hh-radius-card));
        padding: 10px 12px;
      }
      .top {
        display: flex;
        gap: 6px;
        align-items: center;
      }
      .pill {
        display: inline-block;
        font-size: var(--qp-caption);
        border: 1px solid var(--hos-safety-red, var(--hh-danger));
        color: var(--hos-safety-red, var(--hh-danger));
        border-radius: 999px;
        padding: 1px 8px;
      }
      .pill.good {
        border-color: var(--hos-success, var(--hh-success));
        color: var(--hos-success, var(--hh-success));
      }
      .pill.wait {
        border-color: var(--hos-line, var(--hh-line));
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .detail {
        margin: 8px 0 0;
      }
      .actions {
        display: flex;
        gap: 8px;
        margin-top: 10px;
      }
      .row-error {
        margin-top: 8px;
        padding-left: 10px;
        border-left: 3px solid var(--hos-safety-red, var(--hh-danger));
        color: var(--hos-safety-red, var(--hh-danger));
        font-size: var(--qp-caption);
      }
      .reason {
        margin-top: 8px;
      }
      .reason input {
        width: 100%;
        box-sizing: border-box;
      }
      .outcome:empty {
        display: none;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("client")) {
      // A new client is a new sign in: a refusal no longer stands.
      if (this.lastClient !== this.client) this.refusedText = "";
      this.lastClient = this.client;
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  private async load(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const result = await client.listApprovals("");
    if (!result.ok) {
      this.error = result.error;
      return;
    }
    this.error = "";
    this.items = result.value.filter((row) => row.policy_type === MARGIN_POLICY);
    this.loaded = true;
  }

  protected override onClickAction(action: string, control: HTMLElement): void {
    const id = control.getAttribute("data-id") ?? "";
    if (action === "approve") void this.decide(id, true);
    if (action === "refuse") void this.decide(id, false);
  }

  private reasonText(): string {
    return this.renderRoot.querySelector<HTMLInputElement>('[data-field="reason"]')?.value.trim() ?? "";
  }

  private async decide(id: string, approve: boolean): Promise<void> {
    const client = this.client;
    if (client === null || id === "" || this.busyId !== "") return;
    this.busyId = id;
    this.notice = "";
    this.rowError = { ...this.rowError, [id]: "" };
    const result = await client.decideApproval(id, approve, this.reasonText());
    this.busyId = "";
    if (!result.ok) {
      // The server's own text, verbatim.
      this.rowError = { ...this.rowError, [id]: result.error };
      if (result.status === 403) this.refusedText = result.error;
      if (result.status === 409) await this.load();
      return;
    }
    this.notice = approve ? "Approved." : "Refused.";
    await this.load();
    this.announceChanged();
  }

  private renderRow(row: IdentityApproval) {
    const label = policyLabel(row.policy_type);
    const detail = approvalDetail(row.detail);
    const refusal = this.rowError[row.id] ?? "";
    const offer = isDecidable(row.status) && this.refusedText === "";
    const tone = row.status === "APPROVED" ? "good" : row.status === "PENDING" ? "wait" : "";
    return html`
      <div class="req" data-testid="ap-row" data-status=${row.status}>
        <div class="top">
          <span class="pill">${label}</span>
          <span class="pill ${tone}" data-testid="ap-status">${decisionWord(row.status)}</span>
        </div>
        <p class="detail" data-testid="ap-detail">${detail}</p>
        ${offer
          ? html`<div class="actions">
              <button type="button" class="primary" data-action="approve" data-id=${row.id} ?disabled=${this.busyId !== ""} data-testid="ap-approve">Approve<span class="sr"> ${detail}</span></button>
              <button type="button" class="danger" data-action="refuse" data-id=${row.id} ?disabled=${this.busyId !== ""} data-testid="ap-refuse">Refuse<span class="sr"> ${detail}</span></button>
            </div>`
          : nothing}
        ${refusal !== "" ? html`<div class="row-error" role="alert" data-testid="ap-row-error">${refusal}</div>` : nothing}
      </div>
    `;
  }

  override render() {
    const pending = this.items.filter((row) => isDecidable(row.status)).length;
    const decidable = this.items.some((row) => isDecidable(row.status)) && this.refusedText === "";
    return html`
      <div class="box" data-testid="ap-box">
        <div class="head">
          <h3>Pending approvals</h3>
          ${this.loaded ? html`<span class="count" data-testid="ap-count">${pending} pending</span>` : nothing}
        </div>
        <div class="sub">Margin floor requests for your branch. The server says who may decide.</div>
        <div class="ok outcome" role="status" data-testid="ap-outcome">${this.notice}</div>
        ${this.error !== "" ? html`<p class="err" role="alert" data-testid="ap-error">${this.error}</p>` : nothing}
        ${this.refusedText !== "" ? html`<p class="lock" role="status" data-testid="ap-refused">${this.refusedText}</p>` : nothing}
        ${decidable
          ? html`<div class="reason"><label for="ap-reason">Reason for the decision (optional)</label><input id="ap-reason" data-field="reason" type="text" maxlength="300" data-testid="ap-reason" /></div>`
          : nothing}
        ${this.error === "" && this.loaded && this.items.length === 0 ? html`<p class="dim" data-testid="ap-empty">Nobody is held up.</p>` : nothing}
        ${this.items.length > 0 ? html`<div class="queue">${this.items.map((row) => this.renderRow(row))}</div>` : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-approvals": QdApprovals;
  }
}
