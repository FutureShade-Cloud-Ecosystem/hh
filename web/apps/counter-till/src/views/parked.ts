// The parked tickets: this branch's list, and the way one comes back onto the
// counter. The fence is the server's (the list answers only the caller's own
// branch, and resuming another branch's ticket refuses), so this panel shows
// exactly what the server sent and nothing else. A ticket parks with its
// quantities and units only: no price rides on it, because the engine prices
// the lines fresh at resume.
import { css, html, nothing, type TemplateResult } from "lit";

export interface ParkedRow {
  id: string;
  ticketNo: string;
  /** The account's name when this till knows it, else null (the walk-in parks with no account at all). */
  customerLabel: string | null;
  lineCount: number;
}

export interface ParkedPanel {
  state: "loading" | "ready" | "refused";
  branchName: string | null;
  rows: ParkedRow[];
  message: string | null;
  /** True while a park or a resume is in flight. */
  busy: boolean;
  /** The park refusal in words (an empty ticket, mostly). */
  parkMessage: string | null;
}

export const parkedStyles = css`
  .parked-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .parked-list li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hh-s-12);
    padding: var(--hh-s-8) 0;
    border-bottom: 1px solid var(--hh-line);
  }
  .parked-list .ticket-no {
    font-weight: 600;
    min-width: 96px;
  }
  .parked-list .customer-label {
    flex: 1;
  }
  .parked-list .line-count {
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
  }
  .parked-list button {
    min-height: 36px;
    padding: 0 var(--hh-s-12);
    font-size: var(--hh-s-12);
  }
`;

export function renderParked(panel: ParkedPanel): TemplateResult {
  return html`
    <section class="panel" aria-label="Parked tickets">
      <h3>Parked tickets</h3>
      ${panel.branchName === null
        ? nothing
        : html`<p class="notice">On this branch's counter: ${panel.branchName}.</p>`}
      ${panel.state === "loading" ? html`<p class="notice" role="status">Reading the parked tickets.</p>` : nothing}
      ${panel.state === "refused" ? html`<p class="notice" role="status">${panel.message}</p>` : nothing}
      ${panel.state === "ready" && panel.rows.length === 0
        ? html`<p class="notice" role="status" data-testid="parked-empty">No tickets are parked here.</p>`
        : nothing}
      ${panel.state === "ready" && panel.rows.length > 0
        ? html`
            <ul class="parked-list" data-testid="parked-list">
              ${panel.rows.map(
                (row) => html`
                  <li data-testid="parked-ticket">
                    <span class="ticket-no">${row.ticketNo}</span>
                    <span class="customer-label">
                      ${row.customerLabel === null ? "Walk-in" : row.customerLabel}
                    </span>
                    <span class="line-count">${row.lineCount} ${row.lineCount === 1 ? "line" : "lines"}</span>
                    <button
                      type="button"
                      class="btn-secondary"
                      data-action="resume-ticket"
                      data-ticket-id=${row.id}
                      ?disabled=${panel.busy}
                    >
                      Resume
                    </button>
                  </li>
                `,
              )}
            </ul>
          `
        : nothing}
      ${panel.parkMessage === null ? nothing : html`<p class="notice" role="alert">${panel.parkMessage}</p>`}
      <div class="ticket-tools">
        <button
          type="button"
          class="btn-secondary"
          data-action="park-ticket"
          ?disabled=${panel.busy}
        >
          Park this ticket
        </button>
      </div>
    </section>
  `;
}
