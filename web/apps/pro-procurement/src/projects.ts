// The Projects surface: every procurement project of the account in one flat
// list. The Board keeps only the planning projects (a project past planning
// has no stage of its own there), so this list is where a QUOTING project
// stands while the dealer prices it, and its way back to Recall. Each card
// carries the kit's status chip, the line count, and the indicative subtotal
// only past PLANNING (the scope page's rule), and opens the scope page as
// having come from Projects, so back returns here. Visual reference: the HH: Pro
// board's plan cards, the same card the Board's Plan column carries.
import { html, type TemplateResult } from "lit";
import { tenantProcurement, type TenantProject } from "@hardscapehouse/api-client";
import { money, statusChip } from "@hardscapehouse/pro-kit";
import type { Page, SurfaceAnswer } from "./views.js";

export const PROJECTS_EMPTY = "No projects yet. Start a plan from the catalog.";

export async function projectsLoad(page: Page): Promise<SurfaceAnswer> {
  // The gate before the wire: a role without the surface's capability makes no call at all.
  if (!page.session.can("submit_rfq")) return { kind: "refusal" };
  const answer = await tenantProcurement.tenantListProcurementProjects(page.client);
  if (answer.ok) {
    if (answer.value.projects.length === 0) return { kind: "empty" };
    return { kind: "ready", data: answer.value.projects };
  }
  if (answer.status === 403) return { kind: "refusal" };
  return { kind: "error", status: answer.status };
}

function projectCard(project: TenantProject): TemplateResult {
  const priced = project.status !== "PLANNING";
  return html`<button type="button" class="card" data-action="open-scope" data-project="${project.id}" data-from="projects" data-testid="projects-row">
    <span class="rowline"><span class="name figure">${project.name}</span></span>
    <span class="rowline">${statusChip("project", project.status)}<span class="grow"></span><span class="mono">${project.line_count} lines</span></span>
    <span class="rowline"><span class="dim">${priced ? "Indicative" : "Awaiting price"}</span><span class="grow"></span><span class="mono figure">${priced ? money(project.subtotal_cents) : money(null)}</span></span>
  </button>`;
}

export function projectsView(dataRaw: unknown, page: Page): TemplateResult {
  void page;
  const projects = dataRaw as TenantProject[];
  return html`<ul class="cards" data-testid="projects-rows">${projects.map((project) => html`<li>${projectCard(project)}</li>`)}</ul>`;
}

export function projectsCommand(action: string, control: Element, page: Page): boolean {
  void action;
  void control;
  void page;
  return false;
}
