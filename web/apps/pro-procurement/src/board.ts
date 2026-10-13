// The Board surface: the contractor's Plan, Quote, Order and Invoice. Stub:
// the real surface is this file's own commit.
import type { Page, SurfaceAnswer } from "./views.js";
import { html, type TemplateResult } from "lit";

export const BOARD_EMPTY = "Nothing is in motion. Start a plan from the catalog.";

export async function boardLoad(page: Page): Promise<SurfaceAnswer> {
  void page;
  return { kind: "empty" };
}

export function boardView(data: unknown, page: Page): TemplateResult {
  void data;
  void page;
  return html``;
}

export function boardCommand(action: string, control: Element, page: Page): boolean {
  void action;
  void control;
  void page;
  return false;
}
