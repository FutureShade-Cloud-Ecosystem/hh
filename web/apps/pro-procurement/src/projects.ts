// The Projects surface: the flat list of procurement projects with their
// figures. Stub: the real surface is this file's own commit.
import type { Page, SurfaceAnswer } from "./views.js";
import { html, type TemplateResult } from "lit";

export const PROJECTS_EMPTY = "No projects yet. Start a plan from the catalog.";

export async function projectsLoad(page: Page): Promise<SurfaceAnswer> {
  void page;
  return { kind: "empty" };
}

export function projectsView(data: unknown, page: Page): TemplateResult {
  void data;
  void page;
  return html``;
}

export function projectsCommand(action: string, control: Element, page: Page): boolean {
  void action;
  void control;
  void page;
  return false;
}
