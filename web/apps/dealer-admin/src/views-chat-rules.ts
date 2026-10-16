// The Chat rules surface's screen: House Messages channel membership rules.
// Pure template function matching the Change 11 Chat rules board.
import { html, type TemplateResult } from "lit";

export interface ChannelRuleRow {
  readonly channel: string;
  readonly joins: string;
  readonly leaves: string;
}

export const CHANNEL_RULES: readonly ChannelRuleRow[] = [
  {
    channel: "# yard-point-loma",
    joins: "Roles Yard Manager, Dispatcher, Driver at Point Loma",
    leaves: "Status ends or the role changes",
  },
  {
    channel: "# house-point-loma",
    joins: "Roles Inside Sales, Sales Manager at Point Loma",
    leaves: "Status ends or the role changes",
  },
  {
    channel: "# escalations",
    joins: "Roles Branch Manager, General Manager",
    leaves: "Role changes",
  },
  {
    channel: "# finance",
    joins: "Roles Financial Controller, General Manager",
    leaves: "Role changes",
  },
  {
    channel: "# office",
    joins: "Everyone with status Active",
    leaves: "Status ends",
  },
];

export function renderChatRules(): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Chat rules</h1>
      <p class="page-sub">Who joins which House Messages channel, by role and employment status.</p>
    </header>

    <section class="card" data-test="chat-rules-card">
      <div class="card-head">
        <h2 class="card-title">Membership of channels</h2>
      </div>
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th scope="col">CHANNEL</th>
              <th scope="col">JOINS AUTOMATICALLY</th>
              <th scope="col">LEAVES AUTOMATICALLY</th>
            </tr>
          </thead>
          <tbody>
            ${CHANNEL_RULES.map(
              (r) => html`
                <tr>
                  <td><strong>${r.channel}</strong></td>
                  <td>${r.joins}</td>
                  <td>${r.leaves}</td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
      <div class="card-actions">
        <button type="button" class="btn btn-secondary" data-action="add-chat-rule">Add a rule</button>
      </div>
    </section>
  `;
}
