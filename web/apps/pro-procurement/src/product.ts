// The Product surface: one catalog product, and the add to a plan move. Stub:
// the real surface is this file's own commit.
import type { Page, SurfaceAnswer } from "./views.js";
import { html, type TemplateResult } from "lit";

export async function productLoad(page: Page): Promise<SurfaceAnswer> {
  void page;
  return { kind: "empty" };
}

export function productHeading(data: unknown): string {
  void data;
  return "Product";
}

export function productView(data: unknown, page: Page): TemplateResult {
  void data;
  void page;
  return html``;
}

export function productCommand(action: string, control: Element, page: Page): boolean {
  void action;
  void control;
  void page;
  return false;
}
