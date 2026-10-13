// The Catalog surface: the dealer's catalog, searched. Stub: the real surface
// is this file's own commit.
import type { Page, SurfaceAnswer } from "./views.js";
import { html, type TemplateResult } from "lit";

export const CATALOG_WORDS = {
  empty: "Search the dealer's catalog.",
  none: "No matches. Try another word.",
};

export async function catalogLoad(page: Page): Promise<SurfaceAnswer> {
  void page;
  return { kind: "empty" };
}

export function catalogView(data: unknown, page: Page): TemplateResult {
  void data;
  void page;
  return html``;
}

export function catalogCommand(action: string, control: Element, page: Page): boolean {
  void action;
  void control;
  void page;
  return false;
}

export function catalogInput(name: string, value: string, page: Page): void {
  void name;
  void value;
  void page;
}
