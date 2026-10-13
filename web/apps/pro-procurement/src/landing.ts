// The Pro Procurement landing surface: the Board, the contractor's planning
// projects (GET /api/v1/tenant/procurement/projects, tenantListProcurementProjects,
// held by submit_rfq). Visual reference: HH: Pro, proc-board-phone-light (the
// first rows of the board: a project, its stage and status words, its lines and
// its figure). The full board, the catalog and the quote studio are the next
// builds on this skeleton; this is the surface the frame lands on.
import { html, type TemplateResult } from "lit";
import { tenantProcurement, type TenantProject } from "@hardscapehouse/api-client";
import { NO_FIGURE, money, statusChip, type LandingSpec } from "@hardscapehouse/pro-kit";

export type Item = TenantProject;

// A planning project has no indicative price until its request is submitted (the core sums priced lines only), so
// its figure reads as the placeholder rather than a confident zero.
function figure(project: TenantProject): string {
  return project.status === "PLANNING" ? NO_FIGURE : money(project.subtotal_cents);
}

function row(project: Item): TemplateResult {
  return html`<span class="name">${project.name}</span>
    ${statusChip("project", project.status)}
    <span class="mono">${project.line_count} lines</span>
    <span class="mono">${figure(project)}</span>`;
}

export const landing: LandingSpec<Item> = {
  surface: "board",
  capability: "submit_rfq",
  async load(client) {
    const answer = await tenantProcurement.tenantListProcurementProjects(client);
    return answer.ok ? { ok: true, value: answer.value.projects } : answer;
  },
  row,
  emptyWords: "No projects yet. Start a plan from the catalog.",
};
